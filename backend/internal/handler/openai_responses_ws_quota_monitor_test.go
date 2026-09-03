package handler

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func float64Ptr(v float64) *float64 { return &v }

func int64Ptr(v int64) *int64 { return &v }

func TestOpenAIResponsesWSQuotaMonitor_ScanLoadsOncePerAPIKeyAndCancelsWholeGroup(t *testing.T) {
	var mu sync.Mutex
	loadCount := make(map[string]int)
	monitor := newOpenAIResponsesWSQuotaMonitor(
		func(_ context.Context, rawKey string) (*service.APIKey, error) {
			mu.Lock()
			loadCount[rawKey]++
			mu.Unlock()
			if rawKey == "sk-exhausted" {
				return &service.APIKey{ID: 101, Status: service.StatusAPIKeyActive, Quota: 10, QuotaUsed: 10}, nil
			}
			return &service.APIKey{ID: 202, Status: service.StatusAPIKeyActive, Quota: 10, QuotaUsed: 9}, nil
		},
		nil,
	)

	ctx1, cancel1 := context.WithCancelCause(context.Background())
	ctx2, cancel2 := context.WithCancelCause(context.Background())
	ctx3, cancel3 := context.WithCancelCause(context.Background())
	reg1 := monitor.register(101, "sk-exhausted", cancel1)
	reg2 := monitor.register(101, "sk-exhausted", cancel2)
	reg3 := monitor.register(202, "sk-active", cancel3)
	require.NotNil(t, reg1)
	require.NotNil(t, reg2)
	require.NotNil(t, reg3)

	monitor.scanOnce(context.Background())

	mu.Lock()
	require.Equal(t, map[string]int{"sk-exhausted": 1, "sk-active": 1}, loadCount)
	mu.Unlock()
	require.ErrorIs(t, context.Cause(ctx1), errOpenAIResponsesWSQuotaExhausted)
	require.ErrorIs(t, context.Cause(ctx2), errOpenAIResponsesWSQuotaExhausted)
	require.NoError(t, context.Cause(ctx3))
	require.True(t, reg1.wasTriggered())
	require.True(t, reg2.wasTriggered())
	require.False(t, reg3.wasTriggered())

	reg1.unregister()
	reg2.unregister()
	reg3.unregister()
}

func TestOpenAIResponsesWSQuotaMonitor_ExplicitExhaustedStatusCancelsUnlimitedKey(t *testing.T) {
	monitor := newOpenAIResponsesWSQuotaMonitor(
		func(context.Context, string) (*service.APIKey, error) {
			return &service.APIKey{
				ID:        101,
				Status:    service.StatusAPIKeyQuotaExhausted,
				Quota:     0,
				QuotaUsed: 0,
			}, nil
		},
		nil,
	)

	ctx, cancel := context.WithCancelCause(context.Background())
	registration := monitor.register(101, "sk-explicit-status", cancel)
	monitor.scanOnce(context.Background())

	require.ErrorIs(t, context.Cause(ctx), errOpenAIResponsesWSQuotaExhausted)
	require.True(t, registration.wasTriggered())
}

func TestOpenAIResponsesWSQuotaMonitor_SubscriptionLimitCancelsUnlimitedKey(t *testing.T) {
	group := &service.Group{
		SubscriptionType: service.SubscriptionTypeSubscription,
		DailyLimitUSD:    float64Ptr(40),
	}
	monitor := newOpenAIResponsesWSQuotaMonitor(
		func(context.Context, string) (*service.APIKey, error) {
			return &service.APIKey{
				ID:      101,
				UserID:  7,
				GroupID: int64Ptr(3),
				Quota:   0,
				Group:   group,
			}, nil
		},
		nil,
		func(context.Context, int64, int64) (*service.UserSubscription, error) {
			return &service.UserSubscription{
				Status:        service.SubscriptionStatusActive,
				DailyUsageUSD: 40,
				ExpiresAt:     time.Now().Add(time.Hour),
			}, nil
		},
	)

	ctx, cancel := context.WithCancelCause(context.Background())
	registration := monitor.register(101, "sk-subscription-exhausted", cancel)
	monitor.scanOnce(context.Background())

	require.ErrorIs(t, context.Cause(ctx), errOpenAIResponsesWSQuotaExhausted)
	require.True(t, registration.wasTriggered())
}

func TestOpenAIResponsesWSQuotaMonitor_ActiveResultClearsExhaustedGroup(t *testing.T) {
	var exhausted atomic.Bool
	exhausted.Store(true)
	monitor := newOpenAIResponsesWSQuotaMonitor(
		func(context.Context, string) (*service.APIKey, error) {
			if exhausted.Load() {
				return &service.APIKey{ID: 101, Quota: 10, QuotaUsed: 10}, nil
			}
			return &service.APIKey{ID: 101, Quota: 10, QuotaUsed: 1}, nil
		},
		nil,
	)

	ctx, cancel := context.WithCancelCause(context.Background())
	registration := monitor.register(101, "sk-resettable", cancel)
	monitor.scanOnce(context.Background())
	require.ErrorIs(t, context.Cause(ctx), errOpenAIResponsesWSQuotaExhausted)

	// A later active read must clear the tombstone so a reset quota is not
	// treated as exhausted forever.
	exhausted.Store(false)
	monitor.scanOnce(context.Background())
	newCtx, newCancel := context.WithCancelCause(context.Background())
	newRegistration := monitor.register(101, "sk-resettable", newCancel)
	require.NoError(t, context.Cause(newCtx))
	require.False(t, newRegistration.wasTriggered())

	registration.unregister()
	newRegistration.unregister()
}

func TestOpenAIResponsesWSQuotaMonitor_LoadFailuresFailOpenAndLogOncePerStreak(t *testing.T) {
	loadErr := errors.New("temporary loader failure")
	var shouldFail atomic.Bool
	shouldFail.Store(true)
	var logCount atomic.Int32

	monitor := newOpenAIResponsesWSQuotaMonitor(
		func(context.Context, string) (*service.APIKey, error) {
			if shouldFail.Load() {
				return nil, loadErr
			}
			return &service.APIKey{ID: 101, Status: service.StatusAPIKeyActive, Quota: 10, QuotaUsed: 1}, nil
		},
		func(apiKeyID int64, err error) {
			require.Equal(t, int64(101), apiKeyID)
			require.ErrorIs(t, err, loadErr)
			logCount.Add(1)
		},
	)

	ctx, cancel := context.WithCancelCause(context.Background())
	registration := monitor.register(101, "sk-loader-failure", cancel)
	monitor.scanOnce(context.Background())
	monitor.scanOnce(context.Background())

	require.Equal(t, int32(1), logCount.Load())
	require.NoError(t, context.Cause(ctx))
	require.False(t, registration.wasTriggered())

	shouldFail.Store(false)
	monitor.scanOnce(context.Background())
	shouldFail.Store(true)
	monitor.scanOnce(context.Background())

	require.Equal(t, int32(2), logCount.Load())
	require.NoError(t, context.Cause(ctx))
	registration.unregister()
}

func TestOpenAIResponsesWSQuotaMonitor_NilLoaderResultFailsOpen(t *testing.T) {
	var loggedErr error
	monitor := newOpenAIResponsesWSQuotaMonitor(
		func(context.Context, string) (*service.APIKey, error) { return nil, nil },
		func(_ int64, err error) { loggedErr = err },
	)

	ctx, cancel := context.WithCancelCause(context.Background())
	registration := monitor.register(101, "sk-nil-result", cancel)
	monitor.scanOnce(context.Background())
	monitor.scanOnce(context.Background())

	require.ErrorIs(t, loggedErr, errOpenAIResponsesWSQuotaNilResult)
	require.NoError(t, context.Cause(ctx))
	require.False(t, registration.wasTriggered())
	registration.unregister()
}

func TestOpenAIResponsesWSQuotaRegistration_UnregisterWinsAtomicRace(t *testing.T) {
	monitor := newOpenAIResponsesWSQuotaMonitor(nil, nil)
	ctx, cancel := context.WithCancelCause(context.Background())
	registration := monitor.register(101, "sk-unregister-wins", cancel)
	target := monitor.snapshotTargets()[0]

	registration.unregister()
	monitor.trigger(target)

	require.NoError(t, context.Cause(ctx))
	require.False(t, registration.wasTriggered())
	require.Empty(t, monitor.snapshotTargets())
}

func TestOpenAIResponsesWSQuotaRegistration_TriggerWinsAtomicRace(t *testing.T) {
	monitor := newOpenAIResponsesWSQuotaMonitor(nil, nil)
	ctx, cancel := context.WithCancelCause(context.Background())
	registration := monitor.register(101, "sk-trigger-wins", cancel)
	target := monitor.snapshotTargets()[0]

	monitor.trigger(target)
	registration.unregister()

	require.ErrorIs(t, context.Cause(ctx), errOpenAIResponsesWSQuotaExhausted)
	require.True(t, registration.wasTriggered())
	require.Empty(t, monitor.snapshotTargets())
}

func TestOpenAIResponsesWSQuotaMonitor_StaleGenerationCannotCancelReplacementGroup(t *testing.T) {
	monitor := newOpenAIResponsesWSQuotaMonitor(nil, nil)
	oldCtx, oldCancel := context.WithCancelCause(context.Background())
	oldRegistration := monitor.register(101, "sk-same-key", oldCancel)
	staleTarget := monitor.snapshotTargets()[0]
	oldRegistration.unregister()

	newCtx, newCancel := context.WithCancelCause(context.Background())
	newRegistration := monitor.register(101, "sk-same-key", newCancel)
	currentTarget := monitor.snapshotTargets()[0]
	require.NotEqual(t, staleTarget.generation, currentTarget.generation)

	monitor.trigger(staleTarget)

	require.NoError(t, context.Cause(oldCtx))
	require.NoError(t, context.Cause(newCtx))
	require.False(t, newRegistration.wasTriggered())
	newRegistration.unregister()
}

func TestOpenAIResponsesWSQuotaMonitor_ConnectionJoiningSnapshottedGenerationIsCanceled(t *testing.T) {
	monitor := newOpenAIResponsesWSQuotaMonitor(nil, nil)
	ctx1, cancel1 := context.WithCancelCause(context.Background())
	registration1 := monitor.register(101, "sk-same-generation", cancel1)
	target := monitor.snapshotTargets()[0]

	ctx2, cancel2 := context.WithCancelCause(context.Background())
	registration2 := monitor.register(101, "sk-same-generation", cancel2)
	require.Equal(t, registration1.generation, registration2.generation)

	monitor.trigger(target)

	require.ErrorIs(t, context.Cause(ctx1), errOpenAIResponsesWSQuotaExhausted)
	require.ErrorIs(t, context.Cause(ctx2), errOpenAIResponsesWSQuotaExhausted)
	require.True(t, registration1.wasTriggered())
	require.True(t, registration2.wasTriggered())
}

func TestOpenAIResponsesWSQuotaMonitor_RegistrationIntoExhaustedGroupIsCanceledImmediately(t *testing.T) {
	monitor := newOpenAIResponsesWSQuotaMonitor(nil, nil)
	oldCtx, oldCancel := context.WithCancelCause(context.Background())
	oldRegistration := monitor.register(101, "sk-exhausted-group", oldCancel)
	target := monitor.snapshotTargets()[0]
	monitor.trigger(target)

	newCtx, newCancel := context.WithCancelCause(context.Background())
	newRegistration := monitor.register(101, "sk-exhausted-group", newCancel)

	require.ErrorIs(t, context.Cause(oldCtx), errOpenAIResponsesWSQuotaExhausted)
	require.ErrorIs(t, context.Cause(newCtx), errOpenAIResponsesWSQuotaExhausted)
	require.True(t, oldRegistration.wasTriggered())
	require.True(t, newRegistration.wasTriggered())
	require.Len(t, monitor.snapshotTargets(), 1, "exhausted groups remain reloadable so quota resets are observed")

	oldRegistration.unregister()
	newRegistration.unregister()
	require.NotContains(t, monitor.groups, int64(101))
}

func TestOpenAIResponsesWSQuotaMonitor_TriggerScanIsNonBlockingAndPreventsOverlap(t *testing.T) {
	monitor := newOpenAIResponsesWSQuotaMonitor(nil, nil)
	started := make(chan struct{}, 2)
	releaseFirst := make(chan struct{})
	var scans atomic.Int32
	monitor.scan = func(context.Context) {
		scanNumber := scans.Add(1)
		started <- struct{}{}
		if scanNumber == 1 {
			<-releaseFirst
		}
	}

	returned := make(chan struct{})
	go func() {
		monitor.triggerScan()
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("triggerScan blocked on the scan body")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first scan did not start")
	}

	monitor.triggerScan()
	require.Equal(t, int32(1), scans.Load())

	close(releaseFirst)
	require.Eventually(t, func() bool { return !monitor.scanRunning.Load() }, time.Second, time.Millisecond)
	monitor.triggerScan()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("second non-overlapping scan did not start")
	}
	require.Eventually(t, func() bool { return !monitor.scanRunning.Load() }, time.Second, time.Millisecond)
	require.Equal(t, int32(2), scans.Load())
}

func TestOpenAIResponsesWSQuotaMonitor_StopCancelsInFlightScan(t *testing.T) {
	monitor := newOpenAIResponsesWSQuotaMonitor(
		func(ctx context.Context, _ string) (*service.APIKey, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
		nil,
	)
	_, cancel := context.WithCancelCause(context.Background())
	registration := monitor.register(101, "sk-stop", cancel)
	monitor.triggerScan()
	require.Eventually(t, func() bool { return monitor.scanRunning.Load() }, time.Second, time.Millisecond)

	done := make(chan struct{})
	go func() {
		monitor.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("monitor Stop did not wait for the in-flight scan")
	}
	require.False(t, monitor.scanRunning.Load())
	monitor.triggerScan()
	require.False(t, monitor.scanRunning.Load())
	registration.unregister()
}

func TestOpenAIResponsesWSQuotaMonitor_StopRejectsNewRegistrations(t *testing.T) {
	monitor := newOpenAIResponsesWSQuotaMonitor(nil, nil)
	monitor.Stop()
	_, cancel := context.WithCancelCause(context.Background())
	require.Nil(t, monitor.register(101, "sk-stopped", cancel))
}

func TestOpenAIResponsesWSQuotaMonitor_InvalidRegistrationIsIgnored(t *testing.T) {
	monitor := newOpenAIResponsesWSQuotaMonitor(nil, nil)
	_, cancel := context.WithCancelCause(context.Background())

	require.Nil(t, monitor.register(0, "sk-test", cancel))
	require.Nil(t, monitor.register(1, "", cancel))
	require.Nil(t, monitor.register(1, "sk-test", nil))
	require.Empty(t, monitor.snapshotTargets())
}
