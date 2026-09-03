package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestValidateAndCheckLimitsRejectsUsageAtConfiguredLimit(t *testing.T) {
	now := time.Now()
	daily, weekly, monthly := 40.0, 100.0, 200.0
	group := &Group{
		SubscriptionType: SubscriptionTypeSubscription,
		DailyLimitUSD:    &daily,
		WeeklyLimitUSD:   &weekly,
		MonthlyLimitUSD:  &monthly,
	}
	svc := &SubscriptionService{}

	tests := []struct {
		name string
		set  func(*UserSubscription)
		want error
	}{
		{name: "daily", set: func(sub *UserSubscription) { sub.DailyUsageUSD = daily }, want: ErrDailyLimitExceeded},
		{name: "weekly", set: func(sub *UserSubscription) { sub.WeeklyUsageUSD = weekly }, want: ErrWeeklyLimitExceeded},
		{name: "monthly", set: func(sub *UserSubscription) { sub.MonthlyUsageUSD = monthly }, want: ErrMonthlyLimitExceeded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sub := &UserSubscription{
				Status:             SubscriptionStatusActive,
				ExpiresAt:          now.Add(time.Hour),
				DailyWindowStart:   &now,
				WeeklyWindowStart:  &now,
				MonthlyWindowStart: &now,
			}
			tt.set(sub)
			needsMaintenance, err := svc.ValidateAndCheckLimits(sub, group)
			require.False(t, needsMaintenance)
			require.ErrorIs(t, err, tt.want)
		})
	}
}

func TestValidateAndCheckLimitsAllowsUsageBelowConfiguredLimit(t *testing.T) {
	daily := 40.0
	group := &Group{DailyLimitUSD: &daily}
	sub := &UserSubscription{
		Status:        SubscriptionStatusActive,
		ExpiresAt:     time.Now().Add(time.Hour),
		DailyUsageUSD: daily - 0.01,
	}

	_, err := (&SubscriptionService{}).ValidateAndCheckLimits(sub, group)
	require.NoError(t, err)
}
