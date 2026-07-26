//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type realtimeVoiceHealthRepo struct {
	mockAccountRepoForGemini
	tempUnschedulableCalls int
	setErrorCalls          int
}

func (r *realtimeVoiceHealthRepo) SetTempUnschedulable(_ context.Context, _ int64, _ time.Time, _ string) error {
	r.tempUnschedulableCalls++
	return nil
}

func (r *realtimeVoiceHealthRepo) SetError(_ context.Context, _ int64, _ string) error {
	r.setErrorCalls++
	return nil
}

type realtimeVoice403Counter struct{}

func (realtimeVoice403Counter) IncrementOpenAI403Count(context.Context, int64, int) (int64, error) {
	return 1, nil
}

func (realtimeVoice403Counter) ResetOpenAI403Count(context.Context, int64) error {
	return nil
}

func newRealtimeVoiceRateLimitService(repo *realtimeVoiceHealthRepo) *RateLimitService {
	service := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	service.SetOpenAI403CounterCache(realtimeVoice403Counter{})
	return service
}

func realtimeVoiceFailureResponse(statusCode int) *http.Response {
	body := `{"error":{"message":"realtime handshake rejected"}}`
	return &http.Response{
		StatusCode: statusCode,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
			"X-Request-Id": []string{"req_realtime_failure"},
		},
		Body: io.NopCloser(strings.NewReader(body)),
	}
}

func requireRealtimeVoiceFailoverWithoutAccountMutation(t *testing.T, err error, repo *realtimeVoiceHealthRepo) {
	t.Helper()
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.NotNil(t, failoverErr)
	require.Equal(t, 0, repo.tempUnschedulableCalls)
	require.Equal(t, 0, repo.setErrorCalls)
}

func TestForwardOAuthRealtimeVoiceCallDoesNotMutateAccountOn401Or403(t *testing.T) {
	for _, statusCode := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			body, contentType := buildOpenAIRealtimeVoiceCallTestBody(t,
				"v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\n",
				`{"voice_mode":"advanced","requested_default_model":"gpt-4o"}`,
			)
			c, _ := newOpenAIRealtimeVoiceCallTestContext("/v1/realtime/vp?dcid=0", body, contentType)
			parsed, err := (&OpenAIGatewayService{}).ParseOpenAIRealtimeVoiceCallRequest(c, "vp")
			require.NoError(t, err)

			repo := &realtimeVoiceHealthRepo{}
			upstream := &httpUpstreamRecorder{resp: realtimeVoiceFailureResponse(statusCode)}
			svc := &OpenAIGatewayService{
				httpUpstream:     upstream,
				rateLimitService: newRealtimeVoiceRateLimitService(repo),
			}
			account := &Account{
				ID:       901,
				Platform: PlatformOpenAI,
				Type:     AccountTypeOAuth,
				Credentials: map[string]any{
					"access_token":       "oauth-token",
					"chatgpt_account_id": "acct-realtime",
				},
			}

			_, err = svc.ForwardOAuthRealtimeVoiceCall(context.Background(), c, account, parsed)
			requireRealtimeVoiceFailoverWithoutAccountMutation(t, err, repo)
		})
	}
}

func TestForwardOAuthRealtimeVoiceTokenDoesNotMutateAccountOn401Or403(t *testing.T) {
	for _, statusCode := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			c, _ := newOpenAIAudioTranscriptionTestContext(nil, "application/json")
			repo := &realtimeVoiceHealthRepo{}
			upstream := &httpUpstreamRecorder{resp: realtimeVoiceFailureResponse(statusCode)}
			svc := &OpenAIGatewayService{
				httpUpstream:     upstream,
				rateLimitService: newRealtimeVoiceRateLimitService(repo),
			}
			account := &Account{
				ID:       902,
				Platform: PlatformOpenAI,
				Type:     AccountTypeOAuth,
				Credentials: map[string]any{
					"access_token":       "oauth-token",
					"chatgpt_account_id": "acct-realtime",
				},
			}

			_, err := svc.ForwardOAuthRealtimeVoiceToken(context.Background(), c, account)
			requireRealtimeVoiceFailoverWithoutAccountMutation(t, err, repo)
		})
	}
}
