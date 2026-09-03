package handler

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var (
	errOpenAIResponsesWSQuotaExhausted = errors.New("openai responses websocket quota exhausted")
	errOpenAIResponsesWSQuotaNilResult = errors.New("openai responses websocket quota loader returned nil")
)

type openAIResponsesWSQuotaLoader func(context.Context, string) (*service.APIKey, error)

type openAIResponsesWSSubscriptionLoader func(context.Context, int64, int64) (*service.UserSubscription, error)

type openAIResponsesWSQuotaFailureLogger func(apiKeyID int64, err error)

type openAIResponsesWSQuotaScanFunc func(context.Context)

const (
	openAIResponsesWSQuotaRegistrationActive uint32 = iota
	openAIResponsesWSQuotaRegistrationUnregistered
	openAIResponsesWSQuotaRegistrationTriggered
)

const openAIResponsesWSQuotaLoaderTimeout = 5 * time.Second

// openAIResponsesWSQuotaMonitor owns the live Responses WebSocket registry for
// one OpenAIGatewayHandler instance. API keys are grouped by ID; the raw key is
// retained only so the authentication loader can refresh the quota snapshot.
type openAIResponsesWSQuotaMonitor struct {
	mu sync.Mutex

	groups         map[int64]*openAIResponsesWSQuotaGroup
	nextGeneration uint64
	nextToken      uint64

	loader     openAIResponsesWSQuotaLoader
	subLoader  openAIResponsesWSSubscriptionLoader
	logFailure openAIResponsesWSQuotaFailureLogger
	scan       openAIResponsesWSQuotaScanFunc

	scanRunning atomic.Bool

	lifecycleMu sync.Mutex
	scanCtx     context.Context
	scanCancel  context.CancelFunc
	scanWG      sync.WaitGroup
	stopped     bool
}

type openAIResponsesWSQuotaGroup struct {
	generation    uint64
	rawKey        string
	registrations map[uint64]*openAIResponsesWSQuotaRegistration
	failureLogged bool
	exhausted     bool
}

type openAIResponsesWSQuotaRegistration struct {
	monitor    *openAIResponsesWSQuotaMonitor
	apiKeyID   int64
	generation uint64
	token      uint64
	cancel     context.CancelCauseFunc
	state      atomic.Uint32
}

type openAIResponsesWSQuotaTarget struct {
	apiKeyID   int64
	generation uint64
	rawKey     string
}

func newOpenAIResponsesWSQuotaMonitor(
	loader openAIResponsesWSQuotaLoader,
	logFailure openAIResponsesWSQuotaFailureLogger,
	subLoaders ...openAIResponsesWSSubscriptionLoader,
) *openAIResponsesWSQuotaMonitor {
	scanCtx, scanCancel := context.WithCancel(context.Background())
	m := &openAIResponsesWSQuotaMonitor{
		groups:     make(map[int64]*openAIResponsesWSQuotaGroup),
		loader:     loader,
		logFailure: logFailure,
		scanCtx:    scanCtx,
		scanCancel: scanCancel,
	}
	if len(subLoaders) > 0 {
		m.subLoader = subLoaders[0]
	}
	m.scan = m.scanOnce
	return m
}

// Stop prevents new scans, cancels an in-flight scan, and waits for it to
// finish before the backing services are torn down.
func (m *openAIResponsesWSQuotaMonitor) Stop() {
	if m == nil {
		return
	}
	m.lifecycleMu.Lock()
	if !m.stopped {
		m.stopped = true
		if m.scanCancel != nil {
			m.scanCancel()
		}
	}
	m.lifecycleMu.Unlock()
	m.scanWG.Wait()
}

// register adds one live client connection to the API-key group and returns a
// token whose unregister and quota trigger paths race through one atomic state.
func (m *openAIResponsesWSQuotaMonitor) register(
	apiKeyID int64,
	rawKey string,
	cancel context.CancelCauseFunc,
) *openAIResponsesWSQuotaRegistration {
	if m == nil || apiKeyID <= 0 || strings.TrimSpace(rawKey) == "" || cancel == nil {
		return nil
	}
	m.lifecycleMu.Lock()
	stopped := m.stopped
	m.lifecycleMu.Unlock()
	if stopped {
		return nil
	}

	m.mu.Lock()

	group := m.groups[apiKeyID]
	if group == nil {
		m.nextGeneration++
		group = &openAIResponsesWSQuotaGroup{
			generation:    m.nextGeneration,
			rawKey:        rawKey,
			registrations: make(map[uint64]*openAIResponsesWSQuotaRegistration),
		}
		m.groups[apiKeyID] = group
	}

	m.nextToken++
	registration := &openAIResponsesWSQuotaRegistration{
		monitor:    m,
		apiKeyID:   apiKeyID,
		generation: group.generation,
		token:      m.nextToken,
		cancel:     cancel,
	}
	group.registrations[registration.token] = registration
	triggered := group.exhausted && registration.state.CompareAndSwap(
		openAIResponsesWSQuotaRegistrationActive,
		openAIResponsesWSQuotaRegistrationTriggered,
	)
	m.mu.Unlock()

	if triggered {
		cancel(errOpenAIResponsesWSQuotaExhausted)
	}
	return registration
}

// unregister removes the token regardless of which atomic state won. It never
// overwrites Triggered, allowing the handler to reliably classify quota closes
// even when its parent context was canceled first.
func (r *openAIResponsesWSQuotaRegistration) unregister() {
	if r == nil {
		return
	}
	r.state.CompareAndSwap(
		openAIResponsesWSQuotaRegistrationActive,
		openAIResponsesWSQuotaRegistrationUnregistered,
	)
	if r.monitor != nil {
		r.monitor.remove(r)
	}
}

func (r *openAIResponsesWSQuotaRegistration) wasTriggered() bool {
	return r != nil && r.state.Load() == openAIResponsesWSQuotaRegistrationTriggered
}

func (m *openAIResponsesWSQuotaMonitor) remove(registration *openAIResponsesWSQuotaRegistration) {
	if m == nil || registration == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	group := m.groups[registration.apiKeyID]
	if group == nil || group.generation != registration.generation {
		return
	}
	if current := group.registrations[registration.token]; current == registration {
		delete(group.registrations, registration.token)
	}
	if len(group.registrations) == 0 {
		delete(m.groups, registration.apiKeyID)
	}
}

// triggerScan is safe to use directly as a recurring scheduler callback. It
// returns immediately and permits at most one quota scan to run at a time.
func (m *openAIResponsesWSQuotaMonitor) triggerScan() {
	if m == nil {
		return
	}

	m.lifecycleMu.Lock()
	if m.stopped || !m.scanRunning.CompareAndSwap(false, true) {
		m.lifecycleMu.Unlock()
		return
	}
	scan := m.scan
	if scan == nil {
		scan = m.scanOnce
	}
	ctx := m.scanCtx
	m.scanWG.Add(1)
	m.lifecycleMu.Unlock()

	go func() {
		defer m.scanWG.Done()
		defer m.scanRunning.Store(false)
		scan(ctx)
	}()
}

func (m *openAIResponsesWSQuotaMonitor) scanOnce(ctx context.Context) {
	if m == nil || m.loader == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	for _, target := range m.snapshotTargets() {
		if err := ctx.Err(); err != nil {
			return
		}
		checkCtx, cancel := context.WithTimeout(ctx, openAIResponsesWSQuotaLoaderTimeout)
		apiKey, err := m.loader(checkCtx, target.rawKey)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			m.recordFailure(target, err)
			continue
		}
		if apiKey == nil {
			m.recordFailure(target, errOpenAIResponsesWSQuotaNilResult)
			continue
		}

		exhausted := apiKey.Status == service.StatusAPIKeyQuotaExhausted || apiKey.IsQuotaExhausted()
		if !exhausted && m.shouldCheckSubscription(apiKey) {
			if m.subLoader == nil || apiKey.GroupID == nil || apiKey.UserID <= 0 {
				// A missing optional subscription loader should not turn the
				// monitor into a fail-closed admission path.
				m.clearFailure(target)
				m.markActive(target)
				continue
			}
			subCtx, subCancel := context.WithTimeout(ctx, openAIResponsesWSQuotaLoaderTimeout)
			subscription, subErr := m.subLoader(subCtx, apiKey.UserID, *apiKey.GroupID)
			subCancel()
			if subErr != nil {
				if ctx.Err() != nil {
					return
				}
				m.recordFailure(target, subErr)
				continue
			}
			exhausted = subscriptionQuotaExhausted(subscription, apiKey.Group)
		}

		m.clearFailure(target)
		if exhausted {
			m.trigger(target)
		} else {
			m.markActive(target)
		}
	}
}

func (m *openAIResponsesWSQuotaMonitor) shouldCheckSubscription(apiKey *service.APIKey) bool {
	if apiKey == nil || apiKey.Group == nil || !apiKey.Group.IsSubscriptionType() {
		return false
	}
	return apiKey.Group.HasDailyLimit() || apiKey.Group.HasWeeklyLimit() || apiKey.Group.HasMonthlyLimit()
}

func subscriptionQuotaExhausted(subscription *service.UserSubscription, group *service.Group) bool {
	if subscription == nil || group == nil {
		return false
	}
	if subscription.Status != service.SubscriptionStatusActive || subscription.IsExpired() {
		return true
	}
	dailyUsage := subscription.DailyUsageUSD
	weeklyUsage := subscription.WeeklyUsageUSD
	monthlyUsage := subscription.MonthlyUsageUSD
	if subscription.NeedsDailyReset() {
		dailyUsage = 0
	}
	if subscription.NeedsWeeklyReset() {
		weeklyUsage = 0
	}
	if subscription.NeedsMonthlyReset() {
		monthlyUsage = 0
	}
	return (group.HasDailyLimit() && dailyUsage >= *group.DailyLimitUSD) ||
		(group.HasWeeklyLimit() && weeklyUsage >= *group.WeeklyLimitUSD) ||
		(group.HasMonthlyLimit() && monthlyUsage >= *group.MonthlyLimitUSD)
}

func (m *openAIResponsesWSQuotaMonitor) snapshotTargets() []openAIResponsesWSQuotaTarget {
	m.mu.Lock()
	targets := make([]openAIResponsesWSQuotaTarget, 0, len(m.groups))
	for apiKeyID, group := range m.groups {
		if group == nil || len(group.registrations) == 0 {
			continue
		}
		targets = append(targets, openAIResponsesWSQuotaTarget{
			apiKeyID:   apiKeyID,
			generation: group.generation,
			rawKey:     group.rawKey,
		})
	}
	m.mu.Unlock()

	// Stable ordering keeps scans and their tests deterministic without changing
	// the one-loader-call-per-key behavior.
	sort.Slice(targets, func(i, j int) bool {
		return targets[i].apiKeyID < targets[j].apiKeyID
	})
	return targets
}

func (m *openAIResponsesWSQuotaMonitor) recordFailure(target openAIResponsesWSQuotaTarget, err error) {
	if err == nil {
		return
	}

	shouldLog := false
	m.mu.Lock()
	if group := m.groups[target.apiKeyID]; group != nil && group.generation == target.generation {
		if !group.failureLogged {
			group.failureLogged = true
			shouldLog = true
		}
	}
	m.mu.Unlock()

	if shouldLog && m.logFailure != nil {
		m.logFailure(target.apiKeyID, err)
	}
}

func (m *openAIResponsesWSQuotaMonitor) clearFailure(target openAIResponsesWSQuotaTarget) {
	m.mu.Lock()
	if group := m.groups[target.apiKeyID]; group != nil && group.generation == target.generation {
		group.failureLogged = false
	}
	m.mu.Unlock()
}

func (m *openAIResponsesWSQuotaMonitor) markActive(target openAIResponsesWSQuotaTarget) {
	m.mu.Lock()
	if group := m.groups[target.apiKeyID]; group != nil && group.generation == target.generation {
		group.exhausted = false
	}
	m.mu.Unlock()
}

func (m *openAIResponsesWSQuotaMonitor) trigger(target openAIResponsesWSQuotaTarget) {
	m.mu.Lock()
	defer m.mu.Unlock()

	group := m.groups[target.apiKeyID]
	if group == nil || group.generation != target.generation {
		return
	}
	group.exhausted = true
	for _, registration := range group.registrations {
		if registration != nil && registration.state.CompareAndSwap(
			openAIResponsesWSQuotaRegistrationActive,
			openAIResponsesWSQuotaRegistrationTriggered,
		) {
			registration.cancel(errOpenAIResponsesWSQuotaExhausted)
		}
	}
}
