package service

import (
	"bytes"
	"context"
	"io"
	"mime"
	"mime/multipart"
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

func buildOpenAIAudioTranscriptionTestBody(t *testing.T, fields map[string]string) ([]byte, string) {
	return buildOpenAIAudioTranscriptionTestBodyWithFile(t, fields, []byte("fake-audio-data"))
}

func buildOpenAIAudioTranscriptionTestBodyWithFile(t *testing.T, fields map[string]string, fileContents []byte) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		require.NoError(t, writer.WriteField(key, value))
	}
	file, err := writer.CreateFormFile("file", "sample.webm")
	require.NoError(t, err)
	_, err = file.Write(fileContents)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return body.Bytes(), writer.FormDataContentType()
}

func newOpenAIAudioTranscriptionTestContext(body []byte, contentType string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	c.Request = req
	return c, recorder
}

func newOpenAIAudioTranscriptionPricingResolver(t *testing.T, pricing []ChannelModelPricing) *ModelPricingResolver {
	t.Helper()
	const groupID = int64(100)
	channel := &Channel{
		ID:                 1,
		Status:             StatusActive,
		BillingModelSource: BillingModelSourceChannelMapped,
		GroupIDs:           []int64{groupID},
		ModelPricing:       pricing,
	}
	cache := newEmptyChannelCache()
	cache.loadedAt = time.Now()
	cache.channelByGroupID[groupID] = channel
	cache.groupPlatform[groupID] = PlatformOpenAI
	expandPricingToCache(cache, channel, groupID, PlatformOpenAI)

	channelService := &ChannelService{}
	channelService.cache.Store(cache)
	billingService := NewBillingService(&config.Config{}, nil)
	return NewModelPricingResolver(channelService, billingService)
}

func TestParseOpenAIAudioTranscriptionRequest(t *testing.T) {
	body, contentType := buildOpenAIAudioTranscriptionTestBody(t, map[string]string{
		"model":           "gpt-4o-mini-transcribe",
		"language":        "zh",
		"prompt":          "technical discussion",
		"response_format": "json",
		"stream":          "true",
	})
	c, _ := newOpenAIAudioTranscriptionTestContext(body, contentType)

	parsed, err := (&OpenAIGatewayService{}).ParseOpenAIAudioTranscriptionRequest(c)
	require.NoError(t, err)
	require.Equal(t, "gpt-4o-mini-transcribe", parsed.Model)
	require.Equal(t, "zh", parsed.Language)
	require.Equal(t, "technical discussion", parsed.Prompt)
	require.Equal(t, "json", parsed.ResponseFormat)
	require.True(t, parsed.Stream)
	require.Equal(t, "sample.webm", parsed.FileName)
	require.Equal(t, int64(len("fake-audio-data")), parsed.FileSize)
	require.NotEmpty(t, parsed.StickySessionSeed())
}

func TestParseOpenAIAudioTranscriptionRequestRejectsMissingFile(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "gpt-4o-mini-transcribe"))
	require.NoError(t, writer.Close())
	c, _ := newOpenAIAudioTranscriptionTestContext(body.Bytes(), writer.FormDataContentType())

	parsed, err := (&OpenAIGatewayService{}).ParseOpenAIAudioTranscriptionRequest(c)
	require.Nil(t, parsed)
	require.ErrorContains(t, err, "file is required")
}

func TestParseOpenAIAudioTranscriptionRequestRejectsUnknownResponseFormat(t *testing.T) {
	body, contentType := buildOpenAIAudioTranscriptionTestBody(t, map[string]string{
		"model":           "gpt-4o-mini-transcribe",
		"response_format": "xml",
	})
	c, _ := newOpenAIAudioTranscriptionTestContext(body, contentType)

	parsed, err := (&OpenAIGatewayService{}).ParseOpenAIAudioTranscriptionRequest(c)
	require.Nil(t, parsed)
	require.ErrorContains(t, err, "invalid response_format")
}

func TestParseOpenAIAudioTranscriptionRequestRejectsOversizedRequest(t *testing.T) {
	body, contentType := buildOpenAIAudioTranscriptionTestBody(t, map[string]string{
		"model": "gpt-4o-mini-transcribe",
	})
	c, _ := newOpenAIAudioTranscriptionTestContext(body, contentType)
	c.Request.ContentLength = openAIAudioMaxRequestSize + 1

	parsed, err := (&OpenAIGatewayService{}).ParseOpenAIAudioTranscriptionRequest(c)
	require.Nil(t, parsed)
	var maxErr *http.MaxBytesError
	require.ErrorAs(t, err, &maxErr)
	require.Equal(t, int64(openAIAudioMaxRequestSize), maxErr.Limit)
}

func TestParseOpenAIAudioTranscriptionRequestRejectsOversizedFile(t *testing.T) {
	body, contentType := buildOpenAIAudioTranscriptionTestBodyWithFile(t, map[string]string{
		"model": "gpt-4o-mini-transcribe",
	}, bytes.Repeat([]byte("a"), openAIAudioMaxFileSize+1))
	c, _ := newOpenAIAudioTranscriptionTestContext(body, contentType)

	parsed, err := (&OpenAIGatewayService{}).ParseOpenAIAudioTranscriptionRequest(c)
	require.Nil(t, parsed)
	require.ErrorContains(t, err, "file exceeds maximum size of 25 MB")
	var maxErr *http.MaxBytesError
	require.ErrorAs(t, err, &maxErr)
	require.Equal(t, int64(openAIAudioMaxFileSize), maxErr.Limit)
}

func TestForwardAudioTranscriptionOAuthUsesChatGPTBackend(t *testing.T) {
	body, contentType := buildOpenAIAudioTranscriptionTestBody(t, map[string]string{
		"model":           "gpt-4o-mini-transcribe",
		"language":        "zh",
		"prompt":          "gateway-only prompt",
		"response_format": "json",
	})
	c, recorder := newOpenAIAudioTranscriptionTestContext(body, contentType)
	parsed, err := (&OpenAIGatewayService{}).ParseOpenAIAudioTranscriptionRequest(c)
	require.NoError(t, err)

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
			"X-Request-Id": []string{"req_audio_oauth"},
		},
		Body: io.NopCloser(strings.NewReader(`{"text":"hello from audio"}`)),
	}}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	account := &Account{
		ID:       11,
		Name:     "codex-oauth",
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":       "oauth-token",
			"chatgpt_account_id": "acct-123",
			"model_mapping": map[string]any{
				"gpt-4o-mini-transcribe": "gpt-5.1",
			},
		},
	}

	result, err := svc.ForwardAudioTranscription(context.Background(), c, account, parsed, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.ForceUsageRecord)
	require.True(t, result.ForcePerRequestBilling)
	require.Equal(t, "gpt-4o-mini-transcribe", result.Model)
	require.Equal(t, "gpt-4o-mini-transcribe", result.UpstreamModel)
	require.Equal(t, chatgptAudioTranscriptionsURL, upstream.lastReq.URL.String())
	require.Equal(t, "chatgpt.com", upstream.lastReq.Host)
	require.Equal(t, "Bearer oauth-token", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, "acct-123", upstream.lastReq.Header.Get("chatgpt-account-id"))
	require.Equal(t, "Codex Desktop", upstream.lastReq.Header.Get("Originator"))
	require.Contains(t, upstream.lastReq.Header.Get("Content-Type"), "multipart/form-data")
	require.Equal(t, []string{"file", "language"}, readOpenAIAudioMultipartFieldNames(t, upstream.lastReq))
	require.Equal(t, "hello from audio", gjson.Get(recorder.Body.String(), "text").String())
}

func TestSelectAccountWithSchedulerForAudioTranscriptionAllowsCodexOAuthOutsideTextModelMapping(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()

	ctx := context.Background()
	groupID := int64(7101)
	account := Account{
		ID:          71001,
		Name:        "codex-oauth",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"gpt-5.1": "gpt-5.1",
			},
		},
	}
	require.False(t, account.IsModelSupported("gpt-4o-mini-transcribe"))

	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = false
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: []Account{account}},
		cache:              &schedulerTestGatewayCache{},
		cfg:                cfg,
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}

	selection, _, err := svc.SelectAccountWithSchedulerForAudioTranscription(
		ctx,
		&groupID,
		"audio-oauth-session",
		"gpt-4o-mini-transcribe",
		"gpt-4o-mini-transcribe",
		"json",
		nil,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, account.ID, selection.Account.ID)
	require.Equal(t, AccountTypeOAuth, selection.Account.Type)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestSelectAccountWithSchedulerForAudioTranscriptionDoesNotBypassAPIKeyModelMapping(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()

	ctx := context.Background()
	groupID := int64(7102)
	account := Account{
		ID:          71002,
		Name:        "platform-api-key",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"gpt-5.1": "gpt-5.1",
			},
		},
	}

	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = false
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: []Account{account}},
		cache:              &schedulerTestGatewayCache{},
		cfg:                cfg,
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}

	selection, _, err := svc.SelectAccountWithSchedulerForAudioTranscription(
		ctx,
		&groupID,
		"audio-apikey-session",
		"gpt-4o-mini-transcribe",
		"gpt-4o-mini-transcribe",
		"json",
		nil,
	)
	require.Error(t, err)
	require.Nil(t, selection)
}

func TestSelectAccountWithSchedulerForAudioTranscriptionDoesNotUseOAuthForUnsupportedCapabilities(t *testing.T) {
	tests := []struct {
		name           string
		model          string
		responseFormat string
	}{
		{name: "whisper", model: "whisper-1", responseFormat: "json"},
		{name: "subtitles", model: "gpt-4o-transcribe", responseFormat: "srt"},
		{name: "diarization", model: "gpt-4o-transcribe-diarize", responseFormat: "diarized_json"},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			groupID := int64(7200 + i)
			account := Account{
				ID:          int64(72000 + i),
				Name:        "codex-oauth",
				Platform:    PlatformOpenAI,
				Type:        AccountTypeOAuth,
				Status:      StatusActive,
				Schedulable: true,
				Concurrency: 1,
			}
			cfg := &config.Config{}
			cfg.Gateway.Scheduling.LoadBatchEnabled = false
			svc := &OpenAIGatewayService{
				accountRepo:        schedulerTestOpenAIAccountRepo{accounts: []Account{account}},
				cache:              &schedulerTestGatewayCache{},
				cfg:                cfg,
				concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
			}

			selection, _, err := svc.SelectAccountWithSchedulerForAudioTranscription(
				context.Background(),
				&groupID,
				"unsupported-oauth-session",
				tt.model,
				tt.model,
				tt.responseFormat,
				nil,
			)
			require.Error(t, err)
			require.Nil(t, selection)
		})
	}
}

func TestSelectAccountWithSchedulerForAudioTranscriptionUsesAPIKeyForWhisperSRT(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	groupID := int64(7301)
	account := Account{
		ID:          73001,
		Name:        "platform-api-key",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"whisper-1": "whisper-1",
			},
		},
	}
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = false
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: []Account{account}},
		cache:              &schedulerTestGatewayCache{},
		cfg:                cfg,
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}

	selection, _, err := svc.SelectAccountWithSchedulerForAudioTranscription(
		context.Background(),
		&groupID,
		"whisper-srt-session",
		"whisper-1",
		"whisper-1",
		"srt",
		nil,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, AccountTypeAPIKey, selection.Account.Type)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func readOpenAIAudioMultipartFieldNames(t *testing.T, req *http.Request) []string {
	t.Helper()
	require.NotNil(t, req)
	mediaType, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	require.NoError(t, err)
	require.Equal(t, "multipart/form-data", mediaType)
	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	var names []string
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		names = append(names, part.FormName())
		require.NoError(t, part.Close())
	}
	return names
}

func TestForwardAudioTranscriptionAPIKeyUsesPlatformEndpointAndParsesAudioUsage(t *testing.T) {
	body, contentType := buildOpenAIAudioTranscriptionTestBody(t, map[string]string{
		"model": "gpt-4o-transcribe",
	})
	c, _ := newOpenAIAudioTranscriptionTestContext(body, contentType)
	parsed, err := (&OpenAIGatewayService{}).ParseOpenAIAudioTranscriptionRequest(c)
	require.NoError(t, err)

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{
			"text":"hello",
			"usage":{
				"input_tokens":100,
				"input_token_details":{"audio_tokens":80,"text_tokens":20},
				"output_tokens":10
			}
		}`)),
	}}
	svc := &OpenAIGatewayService{
		cfg:          &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
		httpUpstream: upstream,
	}
	account := &Account{
		ID:       12,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key": "sk-test",
		},
	}

	result, err := svc.ForwardAudioTranscription(context.Background(), c, account, parsed, "")
	require.NoError(t, err)
	require.Equal(t, openAIAudioTranscriptionsURL, upstream.lastReq.URL.String())
	require.Equal(t, "Bearer sk-test", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, 100, result.Usage.InputTokens)
	require.Equal(t, 80, result.Usage.AudioInputTokens)
	require.Equal(t, 10, result.Usage.OutputTokens)
	require.False(t, result.ForcePerRequestBilling)
}

func TestForwardAudioTranscriptionAPIKeyPassesThroughSRT(t *testing.T) {
	body, contentType := buildOpenAIAudioTranscriptionTestBody(t, map[string]string{
		"model":           "whisper-1",
		"response_format": "srt",
	})
	c, recorder := newOpenAIAudioTranscriptionTestContext(body, contentType)
	parsed, err := (&OpenAIGatewayService{}).ParseOpenAIAudioTranscriptionRequest(c)
	require.NoError(t, err)

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/plain; charset=utf-8"}},
		Body:       io.NopCloser(strings.NewReader("1\n00:00:00,000 --> 00:00:01,000\nhello\n")),
	}}
	svc := &OpenAIGatewayService{
		cfg:          &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
		httpUpstream: upstream,
	}
	account := &Account{
		ID:       1201,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key": "sk-test",
		},
	}

	result, err := svc.ForwardAudioTranscription(context.Background(), c, account, parsed, "")
	require.NoError(t, err)
	require.True(t, result.ForcePerRequestBilling)
	require.Equal(t, "text/plain; charset=utf-8", recorder.Header().Get("Content-Type"))
	require.Contains(t, recorder.Body.String(), "00:00:00,000 --> 00:00:01,000")
}

func TestForwardAudioTranscriptionOAuthEmulatesStreamingEvents(t *testing.T) {
	body, contentType := buildOpenAIAudioTranscriptionTestBody(t, map[string]string{
		"model":  "gpt-4o-mini-transcribe",
		"stream": "true",
	})
	c, recorder := newOpenAIAudioTranscriptionTestContext(body, contentType)
	parsed, err := (&OpenAIGatewayService{}).ParseOpenAIAudioTranscriptionRequest(c)
	require.NoError(t, err)

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"text":"streamed transcript"}`)),
	}}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	account := &Account{
		ID:          13,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "oauth-token"},
	}

	result, err := svc.ForwardAudioTranscription(context.Background(), c, account, parsed, "")
	require.NoError(t, err)
	require.NotNil(t, result.FirstTokenMs)
	require.True(t, result.ForcePerRequestBilling)
	require.Contains(t, recorder.Header().Get("Content-Type"), "text/event-stream")
	require.Contains(t, recorder.Body.String(), "event: transcript.text.delta")
	require.Contains(t, recorder.Body.String(), `"delta":"streamed transcript"`)
	require.Contains(t, recorder.Body.String(), "event: transcript.text.done")
}

func TestForwardAudioTranscriptionOAuthConvertsTextResponseFormat(t *testing.T) {
	body, contentType := buildOpenAIAudioTranscriptionTestBody(t, map[string]string{
		"model":           "gpt-4o-mini-transcribe",
		"response_format": "text",
	})
	c, recorder := newOpenAIAudioTranscriptionTestContext(body, contentType)
	parsed, err := (&OpenAIGatewayService{}).ParseOpenAIAudioTranscriptionRequest(c)
	require.NoError(t, err)

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"text":"plain transcript"}`)),
	}}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	account := &Account{
		ID:          14,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "oauth-token"},
	}

	_, err = svc.ForwardAudioTranscription(context.Background(), c, account, parsed, "")
	require.NoError(t, err)
	require.Equal(t, "text/plain; charset=utf-8", recorder.Header().Get("Content-Type"))
	require.Equal(t, "plain transcript", recorder.Body.String())
}

func TestForwardAudioTranscriptionOAuthAllowsEmptyTextResponse(t *testing.T) {
	body, contentType := buildOpenAIAudioTranscriptionTestBody(t, map[string]string{
		"model":           "gpt-4o-mini-transcribe",
		"response_format": "text",
	})
	c, recorder := newOpenAIAudioTranscriptionTestContext(body, contentType)
	parsed, err := (&OpenAIGatewayService{}).ParseOpenAIAudioTranscriptionRequest(c)
	require.NoError(t, err)

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"text":""}`)),
	}}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	account := &Account{
		ID:          1401,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "oauth-token"},
	}

	_, err = svc.ForwardAudioTranscription(context.Background(), c, account, parsed, "")
	require.NoError(t, err)
	require.Equal(t, "text/plain; charset=utf-8", recorder.Header().Get("Content-Type"))
	require.Empty(t, recorder.Body.String())
}

func TestForwardAudioTranscriptionOAuthAllowsEmptyStreamingTranscript(t *testing.T) {
	body, contentType := buildOpenAIAudioTranscriptionTestBody(t, map[string]string{
		"model":  "gpt-4o-mini-transcribe",
		"stream": "true",
	})
	c, recorder := newOpenAIAudioTranscriptionTestContext(body, contentType)
	parsed, err := (&OpenAIGatewayService{}).ParseOpenAIAudioTranscriptionRequest(c)
	require.NoError(t, err)

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"text":""}`)),
	}}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	account := &Account{
		ID:          1402,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "oauth-token"},
	}

	result, err := svc.ForwardAudioTranscription(context.Background(), c, account, parsed, "")
	require.NoError(t, err)
	require.NotNil(t, result.FirstTokenMs)
	require.Contains(t, recorder.Body.String(), "event: transcript.text.done")
	require.Contains(t, recorder.Body.String(), `"text":""`)
}

func TestForwardAudioTranscriptionOAuthTextFormatRejectsMissingText(t *testing.T) {
	body, contentType := buildOpenAIAudioTranscriptionTestBody(t, map[string]string{
		"model":           "gpt-4o-mini-transcribe",
		"response_format": "text",
	})
	c, recorder := newOpenAIAudioTranscriptionTestContext(body, contentType)
	parsed, err := (&OpenAIGatewayService{}).ParseOpenAIAudioTranscriptionRequest(c)
	require.NoError(t, err)

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"status":"ok"}`)),
	}}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	account := &Account{
		ID:          1403,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "oauth-token"},
	}

	_, err = svc.ForwardAudioTranscription(context.Background(), c, account, parsed, "")
	require.ErrorContains(t, err, "did not contain text")
	require.Empty(t, recorder.Body.String())
}

func TestForwardAudioTranscriptionOAuthRejectsUnsupportedFormat(t *testing.T) {
	body, contentType := buildOpenAIAudioTranscriptionTestBody(t, map[string]string{
		"model":           "gpt-4o-transcribe",
		"response_format": "srt",
	})
	c, _ := newOpenAIAudioTranscriptionTestContext(body, contentType)
	parsed, err := (&OpenAIGatewayService{}).ParseOpenAIAudioTranscriptionRequest(c)
	require.NoError(t, err)

	svc := &OpenAIGatewayService{}
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	_, err = svc.ForwardAudioTranscription(context.Background(), c, account, parsed, "")
	require.ErrorContains(t, err, "does not support")
}

func TestValidateOAuthAudioTranscriptionBillingRejectsTokenPricing(t *testing.T) {
	inputPrice := 1e-6
	resolver := newOpenAIAudioTranscriptionPricingResolver(t, []ChannelModelPricing{{
		Platform:    PlatformOpenAI,
		Models:      []string{"gpt-4o-mini-transcribe"},
		BillingMode: BillingModeToken,
		InputPrice:  &inputPrice,
	}})
	svc := &OpenAIGatewayService{resolver: resolver, billingService: resolver.billingService}
	groupID := int64(100)
	apiKey := &APIKey{GroupID: &groupID, Group: &Group{ID: groupID}}

	err := svc.ValidateOAuthAudioTranscriptionBilling(
		context.Background(),
		apiKey,
		nil,
		"gpt-4o-mini-transcribe",
		ChannelMappingResult{MappedModel: "gpt-4o-mini-transcribe"},
	)
	require.ErrorContains(t, err, "per-request channel pricing")
}

func TestValidateOAuthAudioTranscriptionBillingAcceptsPositivePerRequestPricing(t *testing.T) {
	price := 0.05
	resolver := newOpenAIAudioTranscriptionPricingResolver(t, []ChannelModelPricing{{
		Platform:        PlatformOpenAI,
		Models:          []string{"gpt-4o-mini-transcribe"},
		BillingMode:     BillingModePerRequest,
		PerRequestPrice: &price,
	}})
	svc := &OpenAIGatewayService{resolver: resolver, billingService: resolver.billingService}
	groupID := int64(100)
	apiKey := &APIKey{GroupID: &groupID, Group: &Group{ID: groupID}}
	mapping := ChannelMappingResult{
		MappedModel:        "gpt-4o-mini-transcribe",
		Mapped:             true,
		BillingModelSource: BillingModelSourceChannelMapped,
	}

	require.NoError(t, svc.ValidateOAuthAudioTranscriptionBilling(
		context.Background(), apiKey, nil, "audio-transcribe-alias", mapping,
	))
	cost, err := svc.calculateOpenAIRecordUsageCost(
		context.Background(),
		&OpenAIForwardResult{ForcePerRequestBilling: true},
		apiKey,
		"gpt-4o-mini-transcribe",
		1,
		UsageTokens{},
		"",
	)
	require.NoError(t, err)
	require.Equal(t, string(BillingModePerRequest), cost.BillingMode)
	require.InDelta(t, price, cost.TotalCost, 1e-12)
	require.InDelta(t, price, cost.ActualCost, 1e-12)
}

func TestValidateAudioTranscriptionBillingRejectsSubscriptionWithoutPerRequestPricing(t *testing.T) {
	svc := &OpenAIGatewayService{}
	groupID := int64(100)
	apiKey := &APIKey{
		GroupID: &groupID,
		Group: &Group{
			ID:               groupID,
			SubscriptionType: SubscriptionTypeSubscription,
		},
	}
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}

	err := svc.ValidateAudioTranscriptionBilling(
		context.Background(),
		apiKey,
		&UserSubscription{},
		account,
		"gpt-4o-mini-transcribe",
		ChannelMappingResult{MappedModel: "gpt-4o-mini-transcribe"},
		"json",
	)
	require.ErrorContains(t, err, "per-request channel pricing")
}

func TestValidateAudioTranscriptionBillingAcceptsSubscriptionWithPositivePerRequestPricing(t *testing.T) {
	price := 0.05
	resolver := newOpenAIAudioTranscriptionPricingResolver(t, []ChannelModelPricing{{
		Platform:        PlatformOpenAI,
		Models:          []string{"gpt-4o-mini-transcribe"},
		BillingMode:     BillingModePerRequest,
		PerRequestPrice: &price,
	}})
	svc := &OpenAIGatewayService{resolver: resolver, billingService: resolver.billingService}
	groupID := int64(100)
	apiKey := &APIKey{
		GroupID: &groupID,
		Group: &Group{
			ID:               groupID,
			SubscriptionType: SubscriptionTypeSubscription,
		},
	}
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}

	require.NoError(t, svc.ValidateAudioTranscriptionBilling(
		context.Background(),
		apiKey,
		&UserSubscription{},
		account,
		"gpt-4o-mini-transcribe",
		ChannelMappingResult{MappedModel: "gpt-4o-mini-transcribe"},
		"json",
	))
}

func TestValidateAudioTranscriptionBillingRejectsAPIKeyTextFormatWithoutPerRequestPricing(t *testing.T) {
	svc := &OpenAIGatewayService{}
	groupID := int64(100)
	apiKey := &APIKey{GroupID: &groupID, Group: &Group{ID: groupID}}
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

	err := svc.ValidateAudioTranscriptionBilling(
		context.Background(),
		apiKey,
		nil,
		account,
		"gpt-4o-transcribe",
		ChannelMappingResult{MappedModel: "gpt-4o-transcribe"},
		"srt",
	)
	require.ErrorContains(t, err, "per-request channel pricing")
}

func TestValidateAudioTranscriptionBillingAllowsAPIKeyJSONUsageModelWithTokenPricing(t *testing.T) {
	svc := &OpenAIGatewayService{}
	groupID := int64(100)
	apiKey := &APIKey{GroupID: &groupID, Group: &Group{ID: groupID}}
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

	require.NoError(t, svc.ValidateAudioTranscriptionBilling(
		context.Background(),
		apiKey,
		nil,
		account,
		"gpt-4o-transcribe",
		ChannelMappingResult{MappedModel: "gpt-4o-transcribe"},
		"json",
	))
}

func TestBillingServiceAudioInputTokenPricing(t *testing.T) {
	svc := &BillingService{}
	breakdown := svc.computeTokenBreakdown(&ModelPricing{
		InputPricePerToken:      2,
		AudioInputPricePerToken: 5,
		OutputPricePerToken:     7,
	}, UsageTokens{
		InputTokens:      10,
		AudioInputTokens: 6,
		OutputTokens:     3,
	}, 1, "", false)

	require.Equal(t, float64(38), breakdown.InputCost)
	require.Equal(t, float64(21), breakdown.OutputCost)
	require.Equal(t, float64(59), breakdown.TotalCost)
}

func TestBillingServiceAudioInputTokenPriorityPricing(t *testing.T) {
	svc := &BillingService{}
	breakdown := svc.computeTokenBreakdown(&ModelPricing{
		InputPricePerToken:              2,
		AudioInputPricePerToken:         5,
		AudioInputPricePerTokenPriority: 11,
		InputPricePerTokenPriority:      3,
		OutputPricePerToken:             7,
	}, UsageTokens{
		InputTokens:      10,
		AudioInputTokens: 6,
	}, 1, "priority", false)

	require.Equal(t, float64(78), breakdown.InputCost)
	require.Equal(t, float64(78), breakdown.TotalCost)
}
