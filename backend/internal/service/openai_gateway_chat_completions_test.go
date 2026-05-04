package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

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
