package repository

import (
	"context"
	"database/sql"
	"errors"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type subscriptionUsageExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func incrementSubscriptionUsageWithClient(ctx context.Context, client *dbent.Client, subscriptionID int64, costUSD float64) error {
	if client == nil || client.Driver() == nil {
		return errors.New("subscription usage database client is nil")
	}
	executor, ok := client.Driver().(subscriptionUsageExecutor)
	if !ok {
		return errors.New("subscription usage database driver does not support SQL execution")
	}
	return incrementSubscriptionUsageAtomically(ctx, executor, subscriptionID, costUSD)
}

// incrementSubscriptionUsageAtomically records subscription usage while
// enforcing the active group limits in the same database operation. The
// request-level cache check is only an early rejection; this transaction is
// the source of truth under concurrent billing.
func incrementSubscriptionUsageAtomically(ctx context.Context, db subscriptionUsageExecutor, subscriptionID int64, costUSD float64) error {
	const updateSQL = `
		UPDATE user_subscriptions us
		SET
			daily_usage_usd = CASE
				WHEN us.daily_window_start IS NULL
					OR us.daily_window_start + INTERVAL '24 hours' <= NOW()
				THEN $1
				ELSE COALESCE(us.daily_usage_usd, 0) + $1
			END,
			weekly_usage_usd = CASE
				WHEN us.weekly_window_start IS NULL
					OR us.weekly_window_start + INTERVAL '7 days' <= NOW()
				THEN $1
				ELSE COALESCE(us.weekly_usage_usd, 0) + $1
			END,
			monthly_usage_usd = CASE
				WHEN us.monthly_window_start IS NULL
					OR us.monthly_window_start + INTERVAL '30 days' <= NOW()
				THEN $1
				ELSE COALESCE(us.monthly_usage_usd, 0) + $1
			END,
			daily_window_start = CASE
				WHEN us.daily_window_start IS NULL
					OR us.daily_window_start + INTERVAL '24 hours' <= NOW()
				THEN date_trunc('day', NOW())
				ELSE us.daily_window_start
			END,
			weekly_window_start = CASE
				WHEN us.weekly_window_start IS NULL
					OR us.weekly_window_start + INTERVAL '7 days' <= NOW()
				THEN date_trunc('day', NOW())
				ELSE us.weekly_window_start
			END,
			monthly_window_start = CASE
				WHEN us.monthly_window_start IS NULL
					OR us.monthly_window_start + INTERVAL '30 days' <= NOW()
				THEN date_trunc('day', NOW())
				ELSE us.monthly_window_start
			END,
			updated_at = NOW()
		FROM groups g
		WHERE us.id = $2
			AND us.deleted_at IS NULL
			AND us.group_id = g.id
			AND g.deleted_at IS NULL
			AND (
				g.daily_limit_usd IS NULL
				OR g.daily_limit_usd <= 0
				OR us.daily_window_start IS NULL
				OR us.daily_window_start + INTERVAL '24 hours' <= NOW()
				OR COALESCE(us.daily_usage_usd, 0) + $1 <= g.daily_limit_usd
			)
			AND (
				g.weekly_limit_usd IS NULL
				OR g.weekly_limit_usd <= 0
				OR us.weekly_window_start IS NULL
				OR us.weekly_window_start + INTERVAL '7 days' <= NOW()
				OR COALESCE(us.weekly_usage_usd, 0) + $1 <= g.weekly_limit_usd
			)
			AND (
				g.monthly_limit_usd IS NULL
				OR g.monthly_limit_usd <= 0
				OR us.monthly_window_start IS NULL
				OR us.monthly_window_start + INTERVAL '30 days' <= NOW()
				OR COALESCE(us.monthly_usage_usd, 0) + $1 <= g.monthly_limit_usd
			)
	`
	result, err := db.ExecContext(ctx, updateSQL, costUSD, subscriptionID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected > 0 {
		return nil
	}

	var dailyExceeded, weeklyExceeded, monthlyExceeded bool
	err = db.QueryRowContext(ctx, `
		SELECT
			COALESCE(
				g.daily_limit_usd > 0
				AND us.daily_window_start IS NOT NULL
				AND us.daily_window_start + INTERVAL '24 hours' > NOW()
				AND COALESCE(us.daily_usage_usd, 0) + $1 > g.daily_limit_usd,
				false
			),
			COALESCE(
				g.weekly_limit_usd > 0
				AND us.weekly_window_start IS NOT NULL
				AND us.weekly_window_start + INTERVAL '7 days' > NOW()
				AND COALESCE(us.weekly_usage_usd, 0) + $1 > g.weekly_limit_usd,
				false
			),
			COALESCE(
				g.monthly_limit_usd > 0
				AND us.monthly_window_start IS NOT NULL
				AND us.monthly_window_start + INTERVAL '30 days' > NOW()
				AND COALESCE(us.monthly_usage_usd, 0) + $1 > g.monthly_limit_usd,
				false
			)
		FROM user_subscriptions us
		JOIN groups g ON g.id = us.group_id AND g.deleted_at IS NULL
		WHERE us.id = $2 AND us.deleted_at IS NULL
	`, costUSD, subscriptionID).Scan(&dailyExceeded, &weeklyExceeded, &monthlyExceeded)
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrSubscriptionNotFound
	}
	if err != nil {
		return err
	}
	if dailyExceeded {
		return service.ErrDailyLimitExceeded
	}
	if weeklyExceeded {
		return service.ErrWeeklyLimitExceeded
	}
	if monthlyExceeded {
		return service.ErrMonthlyLimitExceeded
	}
	return service.ErrSubscriptionNotFound
}
