package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type chatCompletionsHTTPUpstreamRecorder struct {
	lastReq  *http.Request
	lastBody []byte
	resp     *http.Response
	err      error
}

func (u *chatCompletionsHTTPUpstreamRecorder) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	u.lastReq = req
	if req != nil && req.Body != nil {
		body, _ := io.ReadAll(req.Body)
		u.lastBody = body
		_ = req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	if u.err != nil {
		return nil, u.err
	}
	return u.resp, nil
}

func (u *chatCompletionsHTTPUpstreamRecorder) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, accountConcurrency)
}

func TestNormalizeResponsesRequestServiceTier(t *testing.T) {
	t.Parallel()

	req := &apicompat.ResponsesRequest{ServiceTier: " fast "}
	normalizeResponsesRequestServiceTier(req)
	require.Equal(t, "priority", req.ServiceTier)

	req.ServiceTier = "flex"
	normalizeResponsesRequestServiceTier(req)
	require.Equal(t, "flex", req.ServiceTier)

	req.ServiceTier = "default"
	normalizeResponsesRequestServiceTier(req)
	require.Empty(t, req.ServiceTier)
}

func TestNormalizeResponsesBodyServiceTier(t *testing.T) {
	t.Parallel()

	body, tier, err := normalizeResponsesBodyServiceTier([]byte(`{"model":"gpt-5.1","service_tier":"fast"}`))
	require.NoError(t, err)
	require.Equal(t, "priority", tier)
	require.Equal(t, "priority", gjson.GetBytes(body, "service_tier").String())

	body, tier, err = normalizeResponsesBodyServiceTier([]byte(`{"model":"gpt-5.1","service_tier":"flex"}`))
	require.NoError(t, err)
	require.Equal(t, "flex", tier)
	require.Equal(t, "flex", gjson.GetBytes(body, "service_tier").String())

	body, tier, err = normalizeResponsesBodyServiceTier([]byte(`{"model":"gpt-5.1","service_tier":"default"}`))
	require.NoError(t, err)
	require.Empty(t, tier)
	require.False(t, gjson.GetBytes(body, "service_tier").Exists())
}

func TestBuildUpstreamChatCompletionsURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		base     string
		expected string
	}{
		{"bare host", "https://api.deepseek.com", "https://api.deepseek.com/v1/chat/completions"},
		{"with v1", "https://api.deepseek.com/v1", "https://api.deepseek.com/v1/chat/completions"},
		{"already complete", "https://api.deepseek.com/v1/chat/completions", "https://api.deepseek.com/v1/chat/completions"},
		{"trailing slash", "https://api.deepseek.com/", "https://api.deepseek.com/v1/chat/completions"},
		{"local ollama", "http://localhost:11434", "http://localhost:11434/v1/chat/completions"},
		{"local with v1", "http://localhost:11434/v1", "http://localhost:11434/v1/chat/completions"},
		{"glm with v4", "https://open.bigmodel.cn/api/coding/paas/v4", "https://open.bigmodel.cn/api/coding/paas/v4/chat/completions"},
		{"with v2", "https://example.com/api/v2", "https://example.com/api/v2/chat/completions"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildUpstreamChatCompletionsURL(tt.base)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestAccountIsOpenAIOfficial(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		account  *Account
		expected bool
	}{
		{
			"OAuth account is always official",
			&Account{Platform: domain.PlatformOpenAI, Type: AccountTypeOAuth},
			true,
		},
		{
			"API Key with no base_url is official",
			&Account{Platform: domain.PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{}},
			true,
		},
		{
			"API Key with api.openai.com is official",
			&Account{Platform: domain.PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.openai.com"}},
			true,
		},
		{
			"API Key with DeepSeek is not official",
			&Account{Platform: domain.PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.deepseek.com"}},
			false,
		},
		{
			"API Key with localhost is not official",
			&Account{Platform: domain.PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "http://localhost:11434"}},
			false,
		},
		{
			"non-OpenAI platform is always official",
			&Account{Platform: domain.PlatformAnthropic, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.deepseek.com"}},
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.account.IsOpenAIOfficial()
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertResponsesToolsToChatTools(t *testing.T) {
	t.Parallel()

	tools := convertResponsesToolsToChatTools([]apicompat.ResponsesTool{
		{
			Type:        "function",
			Name:        "exec_cmd",
			Description: "Run command",
			Parameters:  []byte(`{"type":"object"}`),
		},
		{Type: "web_search", Name: "search"},
	})

	require.Len(t, tools, 1)
	require.Equal(t, "function", tools[0].Type)
	require.NotNil(t, tools[0].Function)
	require.Equal(t, "exec_cmd", tools[0].Function.Name)
	require.JSONEq(t, `{"type":"object"}`, string(tools[0].Function.Parameters))
}

func TestConvertResponsesInputToMessagesPreservesToolContext(t *testing.T) {
	t.Parallel()

	req := apicompat.ResponsesRequest{
		Input: []byte(`[
			{"role":"user","content":"continue"},
			{"type":"function_call","call_id":"call_1","name":"exec_cmd","arguments":"{\"command\":\"pwd\"}"},
			{"type":"function_call_output","call_id":"call_1","output":"/repo"}
		]`),
	}

	messages, err := convertResponsesInputToMessages(req)
	require.NoError(t, err)
	require.Len(t, messages, 3)
	require.Equal(t, "assistant", messages[1].Role)
	require.Len(t, messages[1].ToolCalls, 1)
	require.Equal(t, "call_1", messages[1].ToolCalls[0].ID)
	require.Equal(t, "exec_cmd", messages[1].ToolCalls[0].Function.Name)
	require.Equal(t, `{"command":"pwd"}`, messages[1].ToolCalls[0].Function.Arguments)
	require.Equal(t, "tool", messages[2].Role)
	require.Equal(t, "call_1", messages[2].ToolCallID)
	require.Equal(t, `"/repo"`, string(messages[2].Content))
}

func TestForwardResponsesPassthroughSendsToolsAndConvertsToolCall(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := &chatCompletionsHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid-tool"}},
		Body: io.NopCloser(strings.NewReader(`{
			"id":"chatcmpl_tool",
			"model":"deepseek-ai/deepseek-v4-pro",
			"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_abc","type":"function","function":{"name":"exec_cmd","arguments":"{\"command\":\"git status --short\"}"}}]},"finish_reason":"tool_calls"}],
			"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}
		}`)),
	}}
	svc := &OpenAIGatewayService{
		cfg:           &config.Config{},
		httpUpstream:  upstream,
		toolCorrector: NewCodexToolCorrector(),
	}
	svc.cfg.Security.URLAllowlist.Enabled = false
	svc.cfg.Security.URLAllowlist.AllowInsecureHTTP = true

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	account := &Account{
		ID:          1,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://api.deepseek.com",
		},
	}
	body := []byte(`{
		"model":"deepseek-ai/deepseek-v4-pro",
		"input":"check status",
		"tools":[{"type":"function","name":"exec_cmd","description":"Run command","parameters":{"type":"object"}}],
		"tool_choice":{"type":"function","name":"exec_cmd"}
	}`)

	result, err := svc.ForwardResponsesPassthrough(context.Background(), c, account, body, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, http.StatusOK, rec.Code)

	require.Equal(t, "https://api.deepseek.com/v1/chat/completions", upstream.lastReq.URL.String())
	require.Equal(t, "exec_cmd", gjson.GetBytes(upstream.lastBody, "tools.0.function.name").String())
	require.Equal(t, "exec_cmd", gjson.GetBytes(upstream.lastBody, "tool_choice.function.name").String())

	output := rec.Body.String()
	require.Equal(t, "function_call", gjson.Get(output, "output.0.type").String())
	require.Equal(t, "call_abc", gjson.Get(output, "output.0.call_id").String())
	require.Equal(t, "exec_cmd", gjson.Get(output, "output.0.name").String())
	require.JSONEq(t, `{"command":"git status --short"}`, gjson.Get(output, "output.0.arguments").String())
}

func TestHandleResponsesPassthroughStreamConvertsToolCalls(t *testing.T) {
	gin.SetMode(gin.TestMode)

	stream := strings.Join([]string{
		`data: {"id":"chatcmpl_1","choices":[{"delta":{"role":"assistant"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_1","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_abc","type":"function","function":{"name":"exec_cmd","arguments":"{\"command\":"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_1","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"pwd\"}"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_1","choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`,
		`data: [DONE]`,
		``,
	}, "\n")

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))

	svc := &OpenAIGatewayService{cfg: &config.Config{}, toolCorrector: NewCodexToolCorrector()}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(stream)),
	}

	result, err := svc.handleResponsesPassthroughStream(resp, c, "deepseek-ai/deepseek-v4-pro", "deepseek-ai/deepseek-v4-pro", "deepseek-ai/deepseek-v4-pro", timeNowForTest())
	require.NoError(t, err)
	require.NotNil(t, result)

	body := rec.Body.String()
	require.Contains(t, body, `"type":"response.output_item.added"`)
	require.Contains(t, body, `"type":"function_call"`)
	require.Contains(t, body, `"type":"response.function_call_arguments.delta"`)
	require.Contains(t, body, `"type":"response.function_call_arguments.done"`)
	require.Contains(t, body, `"type":"response.completed"`)
	require.NotContains(t, body, `<function`)
	require.Contains(t, body, `"arguments":"{\"command\":\"pwd\"}"`)

	addedPayloads := ssePayloadsByType(body, "response.output_item.added")
	require.NotEmpty(t, addedPayloads)
	functionAdded := ""
	for _, payload := range addedPayloads {
		if gjson.Get(payload, "item.type").String() == "function_call" {
			functionAdded = payload
			break
		}
	}
	require.NotEmpty(t, functionAdded)
	require.Equal(t, int64(0), gjson.Get(functionAdded, "output_index").Int())

	completedPayloads := ssePayloadsByType(body, "response.completed")
	require.Len(t, completedPayloads, 1)
	require.Equal(t, "function_call", gjson.Get(completedPayloads[0], "response.output.0.type").String())
	require.Equal(t, "call_abc", gjson.Get(completedPayloads[0], "response.output.0.call_id").String())
}

func ssePayloadsByType(body, eventType string) []string {
	var payloads []string
	for _, line := range strings.Split(body, "\n") {
		data, ok := extractOpenAISSEDataLine(line)
		if !ok || data == "" || data == "[DONE]" {
			continue
		}
		if gjson.Get(data, "type").String() == eventType {
			payloads = append(payloads, data)
		}
	}
	return payloads
}

func timeNowForTest() time.Time {
	return time.Now()
}

func TestChatToolCallResponseStreamStateDelaysArgumentsUntilName(t *testing.T) {
	t.Parallel()

	state := newChatToolCallResponseStreamState()
	events := state.processRawToolCallDeltas([]any{
		map[string]any{
			"index": float64(0),
			"function": map[string]any{
				"arguments": `{"command":`,
			},
		},
	})
	require.Empty(t, events)

	events = state.processRawToolCallDeltas([]any{
		map[string]any{
			"index": float64(0),
			"id":    "call_delayed",
			"function": map[string]any{
				"name":      "exec_cmd",
				"arguments": `"pwd"}`,
			},
		},
	})
	require.Len(t, events, 2)
	require.Equal(t, "response.output_item.added", events[0].Type)
	require.Equal(t, 0, events[0].OutputIndex)
	require.Equal(t, "response.function_call_arguments.delta", events[1].Type)
	require.Equal(t, 0, events[1].OutputIndex)
	require.Equal(t, `{"command":"pwd"}`, events[1].Delta)
}

func TestOpenAIResponsesPassthroughToolContextPrepend(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := &chatCompletionsHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid-tool-context-1"}},
		Body: io.NopCloser(strings.NewReader(`{
			"id":"chatcmpl_context_source",
			"model":"deepseek-ai/deepseek-v4-pro",
			"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_ctx","type":"function","function":{"name":"exec_cmd","arguments":"{\"command\":\"pwd\"}"}}]},"finish_reason":"tool_calls"}],
			"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}
		}`)),
	}}
	svc := &OpenAIGatewayService{
		cfg:           &config.Config{},
		httpUpstream:  upstream,
		toolCorrector: NewCodexToolCorrector(),
	}
	svc.cfg.Security.URLAllowlist.Enabled = false
	svc.cfg.Security.URLAllowlist.AllowInsecureHTTP = true

	account := &Account{
		ID:          1,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://api.deepseek.com",
		},
	}

	rec1 := httptest.NewRecorder()
	c1, _ := gin.CreateTestContext(rec1)
	c1.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	_, err := svc.ForwardResponsesPassthrough(context.Background(), c1, account, []byte(`{
		"model":"deepseek-ai/deepseek-v4-pro",
		"input":"check status",
		"tools":[{"type":"function","name":"exec_cmd","parameters":{"type":"object"}}]
	}`), "")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rec1.Code)

	upstream.resp = &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid-tool-context-2"}},
		Body: io.NopCloser(strings.NewReader(`{
			"id":"chatcmpl_context_done",
			"model":"deepseek-ai/deepseek-v4-pro",
			"choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":7,"completion_tokens":2,"total_tokens":9}
		}`)),
	}
	rec2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(rec2)
	c2.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	_, err = svc.ForwardResponsesPassthrough(context.Background(), c2, account, []byte(`{
		"model":"deepseek-ai/deepseek-v4-pro",
		"previous_response_id":"resp-chatcmpl_context_source",
		"input":[{"type":"function_call_output","call_id":"call_ctx","output":"/repo"}]
	}`), "")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rec2.Code)

	require.Equal(t, "assistant", gjson.GetBytes(upstream.lastBody, "messages.0.role").String())
	require.Equal(t, "call_ctx", gjson.GetBytes(upstream.lastBody, "messages.0.tool_calls.0.id").String())
	require.Equal(t, "exec_cmd", gjson.GetBytes(upstream.lastBody, "messages.0.tool_calls.0.function.name").String())
	require.Equal(t, "tool", gjson.GetBytes(upstream.lastBody, "messages.1.role").String())
	require.Equal(t, "call_ctx", gjson.GetBytes(upstream.lastBody, "messages.1.tool_call_id").String())
}
