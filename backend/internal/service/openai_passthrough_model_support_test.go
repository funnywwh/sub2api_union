package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsOpenAIModelSupportedForRequest(t *testing.T) {
	modelMapping := map[string]any{
		"model_mapping": map[string]any{
			"gpt-5.5": "gpt-5.5",
		},
	}

	t.Run("regular account still uses model mapping as allowlist", func(t *testing.T) {
		account := &Account{
			Platform:    PlatformOpenAI,
			Type:        AccountTypeAPIKey,
			Credentials: modelMapping,
		}

		require.True(t, isOpenAIModelSupportedForRequest(account, "gpt-5.5"))
		require.False(t, isOpenAIModelSupportedForRequest(account, "gpt-5.6"))
	})

	t.Run("passthrough account ignores model mapping allowlist", func(t *testing.T) {
		account := &Account{
			Platform:    PlatformOpenAI,
			Type:        AccountTypeAPIKey,
			Credentials: modelMapping,
			Extra: map[string]any{
				"openai_passthrough": true,
			},
		}

		require.True(t, isOpenAIModelSupportedForRequest(account, "gpt-5.6"))
	})

	t.Run("legacy oauth passthrough flag also ignores model mapping", func(t *testing.T) {
		account := &Account{
			Platform:    PlatformOpenAI,
			Type:        AccountTypeOAuth,
			Credentials: modelMapping,
			Extra: map[string]any{
				"openai_oauth_passthrough": true,
			},
		}

		require.True(t, isOpenAIModelSupportedForRequest(account, "gpt-5.6"))
	})

	t.Run("empty model remains unrestricted", func(t *testing.T) {
		account := &Account{
			Platform:    PlatformOpenAI,
			Type:        AccountTypeAPIKey,
			Credentials: modelMapping,
		}

		require.True(t, isOpenAIModelSupportedForRequest(account, ""))
	})

	t.Run("nil account is rejected", func(t *testing.T) {
		require.False(t, isOpenAIModelSupportedForRequest(nil, "gpt-5.6"))
	})
}

func TestIsOpenAIAccountEligibleForRequest_PassthroughIgnoresModelMapping(t *testing.T) {
	newAccount := func(passthrough bool) *Account {
		return &Account{
			Platform:    PlatformOpenAI,
			Type:        AccountTypeAPIKey,
			Status:      StatusActive,
			Schedulable: true,
			Credentials: map[string]any{
				"model_mapping": map[string]any{
					"gpt-5.5": "gpt-5.5",
				},
			},
			Extra: map[string]any{
				"openai_passthrough": passthrough,
			},
		}
	}

	require.False(t, isOpenAIAccountEligibleForRequest(newAccount(false), "gpt-5.6", false))
	require.True(t, isOpenAIAccountEligibleForRequest(newAccount(true), "gpt-5.6", false))
}
