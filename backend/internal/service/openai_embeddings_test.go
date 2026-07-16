package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type openAIEmbeddingsTokenCacheStub struct {
	token    string
	getCalls int
}

func (s *openAIEmbeddingsTokenCacheStub) GetAccessToken(context.Context, string) (string, error) {
	s.getCalls++
	return s.token, nil
}

func (s *openAIEmbeddingsTokenCacheStub) SetAccessToken(context.Context, string, string, time.Duration) error {
	return nil
}

func (s *openAIEmbeddingsTokenCacheStub) DeleteAccessToken(context.Context, string) error {
	return nil
}

func (s *openAIEmbeddingsTokenCacheStub) AcquireRefreshLock(context.Context, string, time.Duration) (bool, error) {
	return true, nil
}

func (s *openAIEmbeddingsTokenCacheStub) ReleaseRefreshLock(context.Context, string) error {
	return nil
}

func newOpenAIEmbeddingsTestContext(body []byte) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/embeddings", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, recorder
}

func TestBuildOpenAIEmbeddingsURL(t *testing.T) {
	tests := []struct {
		name string
		base string
		want string
	}{
		{name: "bare domain", base: "https://api.openai.com", want: "https://api.openai.com/v1/embeddings"},
		{name: "version root", base: "https://api.openai.com/v1", want: "https://api.openai.com/v1/embeddings"},
		{name: "already complete", base: "https://api.openai.com/v1/embeddings", want: "https://api.openai.com/v1/embeddings"},
		{name: "trailing slash", base: "https://api.openai.com/v1/embeddings/", want: "https://api.openai.com/v1/embeddings"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, buildOpenAIEmbeddingsURL(tt.base))
		})
	}
}

func TestOpenAIGatewayServiceForwardEmbeddings_OAuthUsesPlatformEndpointAndPreservesRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{
		"model":"text-embedding-3-large",
		"input":["hello","world"],
		"dimensions":1024,
		"encoding_format":"base64",
		"user":"embedding-test-user"
	}`)
	c, recorder := newOpenAIEmbeddingsTestContext(body)
	// These Codex-specific headers must never reach the Platform embeddings API.
	c.Request.Header.Set("ChatGPT-Account-ID", "incoming-account-id")
	c.Request.Header.Set("OpenAI-Beta", "responses=experimental")
	c.Request.Header.Set("Originator", "codex_cli_rs")

	tokenCache := &openAIEmbeddingsTokenCacheStub{token: "provider-access-token"}
	responseBody := `{
		"object":"list",
		"data":[
			{"object":"embedding","index":0,"embedding":"Zmlyc3Q="},
			{"object":"embedding","index":1,"embedding":"c2Vjb25k"}
		],
		"model":"text-embedding-3-large",
		"usage":{"prompt_tokens":17,"total_tokens":17}
	}`
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
			"X-Request-Id": []string{"req_embedding_oauth"},
		},
		Body: io.NopCloser(strings.NewReader(responseBody)),
	}}
	svc := &OpenAIGatewayService{
		cfg:                 &config.Config{},
		httpUpstream:        upstream,
		openAITokenProvider: NewOpenAITokenProvider(nil, tokenCache, nil),
	}
	account := &Account{
		ID:       101,
		Name:     "codex-oauth",
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":       "stale-account-token",
			"chatgpt_account_id": "account-id-must-not-be-sent",
			"base_url":           "https://oauth-base-url-must-be-ignored.example/v1",
			"model_mapping": map[string]any{
				"*": "gpt-5.2-codex",
			},
		},
	}

	result, err := svc.ForwardEmbeddings(context.Background(), c, account, body, "")

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "req_embedding_oauth", result.RequestID)
	require.Equal(t, "text-embedding-3-large", result.Model)
	require.Equal(t, "text-embedding-3-large", result.BillingModel)
	require.Equal(t, "text-embedding-3-large", result.UpstreamModel)
	require.Equal(t, 17, result.Usage.InputTokens)
	require.Zero(t, result.Usage.OutputTokens)
	require.Equal(t, 1, tokenCache.getCalls)

	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "https://api.openai.com/v1/embeddings", upstream.lastReq.URL.String())
	require.Equal(t, "api.openai.com", upstream.lastReq.Host)
	require.Equal(t, "Bearer provider-access-token", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, "application/json", upstream.lastReq.Header.Get("Content-Type"))
	require.Equal(t, "application/json", upstream.lastReq.Header.Get("Accept"))
	require.Empty(t, upstream.lastReq.Header.Get("ChatGPT-Account-ID"))
	require.Empty(t, upstream.lastReq.Header.Get("OpenAI-Beta"))
	require.Empty(t, upstream.lastReq.Header.Get("Originator"))

	require.Equal(t, "text-embedding-3-large", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, int64(2), gjson.GetBytes(upstream.lastBody, "input.#").Int())
	require.Equal(t, "hello", gjson.GetBytes(upstream.lastBody, "input.0").String())
	require.Equal(t, "world", gjson.GetBytes(upstream.lastBody, "input.1").String())
	require.Equal(t, int64(1024), gjson.GetBytes(upstream.lastBody, "dimensions").Int())
	require.Equal(t, "base64", gjson.GetBytes(upstream.lastBody, "encoding_format").String())
	require.Equal(t, "embedding-test-user", gjson.GetBytes(upstream.lastBody, "user").String())
	require.JSONEq(t, responseBody, recorder.Body.String())
}

func TestOpenAIGatewayServiceForwardEmbeddings_OAuthAppliesOnlyChannelModelMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"public-embedding-alias","input":"hello"}`)
	c, _ := newOpenAIEmbeddingsTestContext(body)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"object":"list","data":[],"model":"text-embedding-3-large","usage":{"prompt_tokens":1,"total_tokens":1}}`)),
	}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	account := &Account{
		ID:       102,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "oauth-token",
			"model_mapping": map[string]any{
				"*": "gpt-5.2-codex",
			},
		},
	}

	result, err := svc.ForwardEmbeddings(context.Background(), c, account, body, "text-embedding-3-large")

	require.NoError(t, err)
	require.Equal(t, "public-embedding-alias", result.Model)
	require.Equal(t, "text-embedding-3-large", result.BillingModel)
	require.Equal(t, "text-embedding-3-large", result.UpstreamModel)
	require.Equal(t, "text-embedding-3-large", gjson.GetBytes(upstream.lastBody, "model").String())
}

func TestOpenAIGatewayServiceForwardEmbeddings_APIKeyUsesBaseURLAndAccountMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{
		"model":"public-embedding-alias",
		"input":["one","two"],
		"dimensions":256,
		"encoding_format":"float",
		"user":"api-key-user"
	}`)
	c, recorder := newOpenAIEmbeddingsTestContext(body)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
			"Request-Id":   []string{"req_embedding_api_key"},
		},
		Body: io.NopCloser(strings.NewReader(`{
			"object":"list",
			"data":[{"object":"embedding","index":0,"embedding":[0.1,0.2]}],
			"model":"jina-embeddings-v5-text-small",
			"usage":{"prompt_tokens":13,"total_tokens":13}
		}`)),
	}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	account := &Account{
		ID:       103,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://api.jina.ai",
			"model_mapping": map[string]any{
				"channel-embedding-model": "jina-embeddings-v5-text-small",
			},
		},
	}

	result, err := svc.ForwardEmbeddings(context.Background(), c, account, body, "channel-embedding-model")

	require.NoError(t, err)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "req_embedding_api_key", result.RequestID)
	require.Equal(t, "public-embedding-alias", result.Model)
	require.Equal(t, "jina-embeddings-v5-text-small", result.BillingModel)
	require.Equal(t, "jina-embeddings-v5-text-small", result.UpstreamModel)
	require.Equal(t, 13, result.Usage.InputTokens)
	require.Equal(t, "https://api.jina.ai/v1/embeddings", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer sk-test", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, "jina-embeddings-v5-text-small", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, int64(2), gjson.GetBytes(upstream.lastBody, "input.#").Int())
	require.Equal(t, int64(256), gjson.GetBytes(upstream.lastBody, "dimensions").Int())
	require.Equal(t, "float", gjson.GetBytes(upstream.lastBody, "encoding_format").String())
	require.Equal(t, "api-key-user", gjson.GetBytes(upstream.lastBody, "user").String())
}

func TestOpenAIGatewayServiceSelectAccountWithSchedulerForEmbeddings_OAuthBypassesGenericMapping(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	groupID := int64(9001)
	account := Account{
		ID:          201,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"gpt-*": "gpt-5.2-codex",
			},
		},
	}
	require.False(t, account.IsModelSupported("text-embedding-3-large"), "the fixture must prove normal model-aware selection would reject this OAuth account")
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: []Account{account}},
		cache:              &schedulerTestGatewayCache{},
		cfg:                &config.Config{},
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}

	selection, _, err := svc.SelectAccountWithSchedulerForEmbeddings(
		context.Background(),
		&groupID,
		"embedding-session",
		"text-embedding-3-large",
		"text-embedding-3-large",
		nil,
	)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, int64(201), selection.Account.ID)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestOpenAIGatewayServiceSelectAccountWithSchedulerForEmbeddings_APIKeyRemainsModelStrict(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	groupID := int64(9002)
	account := Account{
		ID:          202,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key": "sk-test",
			"model_mapping": map[string]any{
				"gpt-*": "gpt-5.2",
			},
		},
	}
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: []Account{account}},
		cache:              &schedulerTestGatewayCache{},
		cfg:                &config.Config{},
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}

	selection, _, err := svc.SelectAccountWithSchedulerForEmbeddings(
		context.Background(),
		&groupID,
		"embedding-session",
		"text-embedding-3-large",
		"text-embedding-3-large",
		nil,
	)

	require.Error(t, err)
	require.Nil(t, selection)
}

func TestOpenAIGatewayServiceSelectAccountWithSchedulerForEmbeddings_APIKeyUsesChannelMappedModel(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	groupID := int64(9003)
	account := Account{
		ID:          203,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key": "sk-test",
			"model_mapping": map[string]any{
				"channel-embedding-model": "text-embedding-3-large",
			},
		},
	}
	require.False(t, account.IsModelSupported("public-embedding-alias"))
	require.True(t, account.IsModelSupported("channel-embedding-model"))
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: []Account{account}},
		cache:              &schedulerTestGatewayCache{},
		cfg:                &config.Config{},
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}

	selection, _, err := svc.SelectAccountWithSchedulerForEmbeddings(
		context.Background(),
		&groupID,
		"embedding-session",
		"public-embedding-alias",
		"channel-embedding-model",
		nil,
	)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, int64(203), selection.Account.ID)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestOpenAIGatewayServiceForwardEmbeddings_429ReturnsFailoverWithoutWritingResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"text-embedding-3-large","input":"retry me"}`)
	c, recorder := newOpenAIEmbeddingsTestContext(body)
	upstreamBody := `{"error":{"type":"rate_limit_error","code":"rate_limit_exceeded","message":"too many requests"}}`
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
			"X-Request-Id": []string{"req_embedding_429"},
			"Retry-After":  []string{"2"},
		},
		Body: io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	account := &Account{
		ID:       301,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "oauth-token",
		},
	}

	result, err := svc.ForwardEmbeddings(context.Background(), c, account, body, "")

	require.Nil(t, result)
	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr))
	require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
	require.JSONEq(t, upstreamBody, string(failoverErr.ResponseBody))
	require.False(t, c.Writer.Written(), "retryable upstream failures must remain available for account failover")
	require.Empty(t, recorder.Body.String())
}

func TestExtractOpenAIEmbeddingsUsage(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{name: "prompt tokens take precedence", body: `{"usage":{"prompt_tokens":11,"input_tokens":12,"total_tokens":13}}`, want: 11},
		{name: "input tokens fallback", body: `{"usage":{"input_tokens":12,"total_tokens":13}}`, want: 12},
		{name: "total tokens fallback", body: `{"usage":{"prompt_tokens":0,"total_tokens":13}}`, want: 13},
		{name: "missing usage", body: `{"object":"list"}`, want: 0},
		{name: "non object usage", body: `{"usage":7}`, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usage := extractOpenAIEmbeddingsUsage([]byte(tt.body))
			require.Equal(t, tt.want, usage.InputTokens)
			require.Zero(t, usage.OutputTokens)
		})
	}
}
