package handler

import (
	"net/http"
	"testing"

	pkgerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestBillingErrorDetails_MapsGroupRPMExceededToTooManyRequests(t *testing.T) {
	status, code, msg, retryAfter := billingErrorDetails(service.ErrGroupRPMExceeded)
	require.Equal(t, http.StatusTooManyRequests, status)
	require.Equal(t, "rate_limit_exceeded", code)
	require.NotEmpty(t, msg)
	require.Greater(t, retryAfter, 0, "RPM exceeded should return positive Retry-After")
	require.LessOrEqual(t, retryAfter, 60)
}

func TestBillingErrorDetails_MapsUserRPMExceededToTooManyRequests(t *testing.T) {
	status, code, msg, retryAfter := billingErrorDetails(service.ErrUserRPMExceeded)
	require.Equal(t, http.StatusTooManyRequests, status)
	require.Equal(t, "rate_limit_exceeded", code)
	require.NotEmpty(t, msg)
	require.Greater(t, retryAfter, 0, "RPM exceeded should return positive Retry-After")
	require.LessOrEqual(t, retryAfter, 60)
}

func TestBillingErrorDetails_APIKeyRateLimitStillMaps(t *testing.T) {
	// 回归保护：加 RPM 分支后不应影响已有 APIKey rate limit 的映射。
	for _, err := range []error{
		service.ErrAPIKeyRateLimit5hExceeded,
		service.ErrAPIKeyRateLimit1dExceeded,
		service.ErrAPIKeyRateLimit7dExceeded,
	} {
		status, code, _, _ := billingErrorDetails(err)
		require.Equal(t, http.StatusTooManyRequests, status, "status for %v", err)
		require.Equal(t, "rate_limit_exceeded", code)
	}
}

func TestBillingErrorDetails_MapsSubscriptionUsageLimitsToTooManyRequests(t *testing.T) {
	for _, err := range []error{
		service.ErrDailyLimitExceeded,
		service.ErrWeeklyLimitExceeded,
		service.ErrMonthlyLimitExceeded,
	} {
		status, code, msg, retryAfter := billingErrorDetails(err)
		require.Equal(t, http.StatusTooManyRequests, status, "status for %v", err)
		require.Equal(t, "USAGE_LIMIT_EXCEEDED", code, "code for %v", err)
		require.NotEmpty(t, msg)
		require.Zero(t, retryAfter)
	}
}

func TestBillingErrorDetails_MapsClonedSubscriptionLimitErrorToTooManyRequests(t *testing.T) {
	// A repository/cache boundary may clone the application error rather than
	// returning the package sentinel. The HTTP reason must still preserve the
	// quota response contract.
	err := pkgerrors.New(http.StatusTooManyRequests, "DAILY_LIMIT_EXCEEDED", "daily usage limit exceeded")
	status, code, msg, retryAfter := billingErrorDetails(err)
	require.Equal(t, http.StatusTooManyRequests, status)
	require.Equal(t, "USAGE_LIMIT_EXCEEDED", code)
	require.Equal(t, "daily usage limit exceeded", msg)
	require.Zero(t, retryAfter)
}

func TestBillingErrorDetails_BillingServiceUnavailableMapsTo503(t *testing.T) {
	status, code, _, retryAfter := billingErrorDetails(service.ErrBillingServiceUnavailable)
	require.Equal(t, http.StatusServiceUnavailable, status)
	require.Equal(t, "billing_service_error", code)
	require.Equal(t, 0, retryAfter, "non-RPM errors should not set Retry-After")
}

func TestBillingErrorDetails_UnknownErrorFallsBackTo403(t *testing.T) {
	status, code, msg, _ := billingErrorDetails(service.ErrInsufficientBalance)
	require.Equal(t, http.StatusForbidden, status)
	require.Equal(t, "billing_error", code)
	require.NotEmpty(t, msg)
}
