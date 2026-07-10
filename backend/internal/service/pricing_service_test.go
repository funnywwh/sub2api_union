package service

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestParsePricingData_ParsesPriorityAndServiceTierFields(t *testing.T) {
	svc := &PricingService{}
	body := []byte(`{
		"gpt-5.4": {
			"input_cost_per_token": 0.0000025,
			"input_cost_per_token_priority": 0.000005,
			"output_cost_per_token": 0.000015,
			"output_cost_per_token_priority": 0.00003,
			"cache_creation_input_token_cost": 0.0000025,
			"cache_read_input_token_cost": 0.00000025,
			"cache_read_input_token_cost_priority": 0.0000005,
			"supports_service_tier": true,
			"supports_prompt_caching": true,
			"litellm_provider": "openai",
			"mode": "chat"
		}
	}`)

	data, err := svc.parsePricingData(body)
	require.NoError(t, err)
	pricing := data["gpt-5.4"]
	require.NotNil(t, pricing)
	require.InDelta(t, 5e-6, pricing.InputCostPerTokenPriority, 1e-12)
	require.InDelta(t, 3e-5, pricing.OutputCostPerTokenPriority, 1e-12)
	require.InDelta(t, 5e-7, pricing.CacheReadInputTokenCostPriority, 1e-12)
	require.True(t, pricing.SupportsServiceTier)
}

func TestParsePricingData_DerivesLongContextAndCacheWritePriorityFromAbove272kFields(t *testing.T) {
	svc := &PricingService{}
	body := []byte(`{
		"gpt-5.6-sol": {
			"input_cost_per_token": 0.000005,
			"input_cost_per_token_above_272k_tokens": 0.00001,
			"input_cost_per_token_priority": 0.00001,
			"output_cost_per_token": 0.00003,
			"output_cost_per_token_above_272k_tokens": 0.000045,
			"output_cost_per_token_priority": 0.00006,
			"cache_creation_input_token_cost": 0.00000625,
			"cache_creation_input_token_cost_above_272k_tokens": 0.0000125,
			"cache_creation_input_token_cost_priority": 0.0000125,
			"cache_read_input_token_cost": 0.0000005,
			"cache_read_input_token_cost_above_272k_tokens": 0.000001,
			"cache_read_input_token_cost_priority": 0.000001,
			"supports_service_tier": true,
			"supports_prompt_caching": true,
			"litellm_provider": "openai",
			"mode": "chat"
		}
	}`)

	data, err := svc.parsePricingData(body)
	require.NoError(t, err)
	pricing := data["gpt-5.6-sol"]
	require.NotNil(t, pricing)
	require.InDelta(t, 1.25e-5, pricing.CacheCreationInputTokenCostPriority, 1e-12)
	require.InDelta(t, 1.25e-5, pricing.CacheCreationInputTokenCostAbove272k, 1e-12)
	require.InDelta(t, 1e-5, pricing.InputCostPerTokenAbove272k, 1e-12)
	require.InDelta(t, 4.5e-5, pricing.OutputCostPerTokenAbove272k, 1e-12)
	require.InDelta(t, 1e-6, pricing.CacheReadInputTokenCostAbove272k, 1e-12)
	require.Equal(t, 272000, pricing.LongContextInputTokenThreshold)
	require.InDelta(t, 2.0, pricing.LongContextInputCostMultiplier, 1e-12)
	require.InDelta(t, 1.5, pricing.LongContextOutputCostMultiplier, 1e-12)
}

func TestValidatePricingURL_RequiresPinnedTrustedSource(t *testing.T) {
	svc := &PricingService{
		cfg: &config.Config{
			Security: config.SecurityConfig{
				URLAllowlist: config.URLAllowlistConfig{
					Enabled:           false,
					AllowInsecureHTTP: true,
					AllowPrivateHosts: true,
					PricingHosts:      []string{"*"},
				},
			},
		},
	}

	valid := []string{
		"https://raw.githubusercontent.com/Wei-Shaw/model-price-repo/main/model_prices_and_context_window.json",
		"https://raw.githubusercontent.com/Wei-Shaw/model-price-repo/main/model_prices_and_context_window.sha256",
	}
	for _, raw := range valid {
		t.Run("valid "+raw, func(t *testing.T) {
			got, err := svc.validatePricingURL(raw)
			require.NoError(t, err)
			require.Equal(t, raw, got)
		})
	}

	invalid := []string{
		"http://raw.githubusercontent.com/Wei-Shaw/model-price-repo/main/model_prices_and_context_window.json",
		"https://example.com/Wei-Shaw/model-price-repo/main/model_prices_and_context_window.json",
		"https://raw.githubusercontent.com/evil/model-price-repo/main/model_prices_and_context_window.json",
		"https://raw.githubusercontent.com/Wei-Shaw/model-price-repo/dev/model_prices_and_context_window.json",
		"https://raw.githubusercontent.com/Wei-Shaw/model-price-repo/main/model_prices_and_context_window.json?cache=bust",
	}
	for _, raw := range invalid {
		t.Run("invalid "+raw, func(t *testing.T) {
			_, err := svc.validatePricingURL(raw)
			require.Error(t, err)
		})
	}
}

func TestGetModelPricing_Gpt53CodexSparkUsesGpt51CodexPricing(t *testing.T) {
	sparkPricing := &LiteLLMModelPricing{InputCostPerToken: 1}
	gpt53Pricing := &LiteLLMModelPricing{InputCostPerToken: 9}

	svc := &PricingService{
		pricingData: map[string]*LiteLLMModelPricing{
			"gpt-5.1-codex": sparkPricing,
			"gpt-5.3":       gpt53Pricing,
		},
	}

	got := svc.GetModelPricing("gpt-5.3-codex-spark")
	require.Same(t, sparkPricing, got)
}

func TestGetModelPricing_Gpt53CodexFallbackStillUsesGpt52Codex(t *testing.T) {
	gpt52CodexPricing := &LiteLLMModelPricing{InputCostPerToken: 2}

	svc := &PricingService{
		pricingData: map[string]*LiteLLMModelPricing{
			"gpt-5.2-codex": gpt52CodexPricing,
		},
	}

	got := svc.GetModelPricing("gpt-5.3-codex")
	require.Same(t, gpt52CodexPricing, got)
}

func TestGetModelPricing_OpenAIFallbackMatchedLoggedAsInfo(t *testing.T) {
	logSink, restore := captureStructuredLog(t)
	defer restore()

	gpt52CodexPricing := &LiteLLMModelPricing{InputCostPerToken: 2}
	svc := &PricingService{
		pricingData: map[string]*LiteLLMModelPricing{
			"gpt-5.2-codex": gpt52CodexPricing,
		},
	}

	got := svc.GetModelPricing("gpt-5.3-codex")
	require.Same(t, gpt52CodexPricing, got)

	require.True(t, logSink.ContainsMessageAtLevel("[Pricing] OpenAI fallback matched gpt-5.3-codex -> gpt-5.2-codex", "info"))
	require.False(t, logSink.ContainsMessageAtLevel("[Pricing] OpenAI fallback matched gpt-5.3-codex -> gpt-5.2-codex", "warn"))
}

func TestGetModelPricing_Gpt54UsesStaticFallbackWhenRemoteMissing(t *testing.T) {
	svc := &PricingService{
		pricingData: map[string]*LiteLLMModelPricing{
			"gpt-5.1-codex": &LiteLLMModelPricing{InputCostPerToken: 1.25e-6},
		},
	}

	got := svc.GetModelPricing("gpt-5.4")
	require.NotNil(t, got)
	require.InDelta(t, 2.5e-6, got.InputCostPerToken, 1e-12)
	require.InDelta(t, 1.5e-5, got.OutputCostPerToken, 1e-12)
	require.InDelta(t, 2.5e-7, got.CacheReadInputTokenCost, 1e-12)
	require.Equal(t, 272000, got.LongContextInputTokenThreshold)
	require.InDelta(t, 2.0, got.LongContextInputCostMultiplier, 1e-12)
	require.InDelta(t, 1.5, got.LongContextOutputCostMultiplier, 1e-12)
}

func TestGetModelPricing_Gpt55UsesStaticFallbackWhenRemoteMissing(t *testing.T) {
	svc := &PricingService{
		pricingData: map[string]*LiteLLMModelPricing{
			"gpt-5.1-codex": {InputCostPerToken: 1.25e-6},
		},
	}

	got := svc.GetModelPricing("gpt-5.5")
	require.NotNil(t, got)
	require.InDelta(t, 5e-6, got.InputCostPerToken, 1e-12)
	require.InDelta(t, 3e-5, got.OutputCostPerToken, 1e-12)
	require.InDelta(t, 5e-6, got.CacheCreationInputTokenCost, 1e-12)
	require.InDelta(t, 5e-7, got.CacheReadInputTokenCost, 1e-12)
	require.Zero(t, got.LongContextInputTokenThreshold)
}

func TestGetModelPricing_Gpt56UsesOfficialStaticFallbackWhenRemoteMissing(t *testing.T) {
	svc := &PricingService{
		pricingData: map[string]*LiteLLMModelPricing{
			"gpt-5.1-codex": {InputCostPerToken: 1.25e-6},
		},
	}

	alias := svc.GetModelPricing("gpt-5.6")
	require.NotNil(t, alias)
	require.InDelta(t, 5e-6, alias.InputCostPerToken, 1e-12)

	got := svc.GetModelPricing("gpt-5.6-sol")
	require.NotNil(t, got)
	require.InDelta(t, 5e-6, got.InputCostPerToken, 1e-12)
	require.InDelta(t, 1e-5, got.InputCostPerTokenPriority, 1e-12)
	require.InDelta(t, 3e-5, got.OutputCostPerToken, 1e-12)
	require.InDelta(t, 6e-5, got.OutputCostPerTokenPriority, 1e-12)
	require.InDelta(t, 6.25e-6, got.CacheCreationInputTokenCost, 1e-12)
	require.InDelta(t, 12.5e-6, got.CacheCreationInputTokenCostPriority, 1e-12)
	require.InDelta(t, 5e-7, got.CacheReadInputTokenCost, 1e-12)
	require.InDelta(t, 1e-6, got.CacheReadInputTokenCostPriority, 1e-12)
	require.Equal(t, 272000, got.LongContextInputTokenThreshold)
	require.InDelta(t, 2.0, got.LongContextInputCostMultiplier, 1e-12)
	require.InDelta(t, 1.5, got.LongContextOutputCostMultiplier, 1e-12)

	terra := svc.GetModelPricing("gpt-5.6-terra")
	require.NotNil(t, terra)
	require.InDelta(t, 2.5e-6, terra.InputCostPerToken, 1e-12)
	require.InDelta(t, 1.5e-5, terra.OutputCostPerToken, 1e-12)
	require.InDelta(t, 3.125e-6, terra.CacheCreationInputTokenCost, 1e-12)
	require.InDelta(t, 2.5e-7, terra.CacheReadInputTokenCost, 1e-12)

	luna := svc.GetModelPricing("gpt-5.6-luna")
	require.NotNil(t, luna)
	require.InDelta(t, 1e-6, luna.InputCostPerToken, 1e-12)
	require.InDelta(t, 6e-6, luna.OutputCostPerToken, 1e-12)
	require.InDelta(t, 1.25e-6, luna.CacheCreationInputTokenCost, 1e-12)
	require.InDelta(t, 1e-7, luna.CacheReadInputTokenCost, 1e-12)
}

func TestGetModelPricing_Gpt56AliasUsesDynamicCanonicalPricingBeforeStaticFallback(t *testing.T) {
	dynamicPricing := &LiteLLMModelPricing{InputCostPerToken: 9.9e-6}
	svc := &PricingService{
		pricingData: map[string]*LiteLLMModelPricing{
			"gpt-5.6-sol": dynamicPricing,
		},
	}

	for _, model := range []string{"gpt-5.6", "GPT5.6", "gpt-5.6-max"} {
		t.Run(model, func(t *testing.T) {
			got := svc.GetModelPricing(model)
			require.Same(t, dynamicPricing, got)
		})
	}
}

func TestGetModelPricing_Gpt56TierAliasPrefersDynamicCanonicalOverBaseVariant(t *testing.T) {
	basePricing := &LiteLLMModelPricing{InputCostPerToken: 1e-6}
	solPricing := &LiteLLMModelPricing{InputCostPerToken: 2e-6}
	terraPricing := &LiteLLMModelPricing{InputCostPerToken: 3e-6}
	lunaPricing := &LiteLLMModelPricing{InputCostPerToken: 4e-6}
	svc := &PricingService{
		pricingData: map[string]*LiteLLMModelPricing{
			"gpt-5.6":       basePricing,
			"gpt-5.6-sol":   solPricing,
			"gpt-5.6-terra": terraPricing,
			"gpt-5.6-luna":  lunaPricing,
		},
	}

	tests := []struct {
		model string
		want  *LiteLLMModelPricing
	}{
		{model: "gpt-5.6-max", want: solPricing},
		{model: "gpt-5.6-sol-high", want: solPricing},
		{model: "gpt-5.6-terra-high", want: terraPricing},
		{model: "gpt-5.6-luna-xhigh", want: lunaPricing},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got := svc.GetModelPricing(tt.model)
			require.Same(t, tt.want, got)
		})
	}
}

func TestGetModelPricing_OpenAIGPT5CompactAliasesUseStaticFallback(t *testing.T) {
	svc := &PricingService{
		pricingData: map[string]*LiteLLMModelPricing{
			"gpt-5.1-codex": {InputCostPerToken: 1.25e-6},
		},
	}

	tests := []struct {
		model         string
		expectedInput float64
	}{
		{model: "GPT5.4", expectedInput: 2.5e-6},
		{model: "gpt5.4", expectedInput: 2.5e-6},
		{model: "GPT5.5", expectedInput: 5e-6},
		{model: "gpt5.5", expectedInput: 5e-6},
		{model: "GPT5.6", expectedInput: 5e-6},
		{model: "gpt5.6", expectedInput: 5e-6},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got := svc.GetModelPricing(tt.model)
			require.NotNil(t, got)
			require.InDelta(t, tt.expectedInput, got.InputCostPerToken, 1e-12)
		})
	}
}

func TestGetModelPricing_Gpt54MiniUsesDedicatedStaticFallbackWhenRemoteMissing(t *testing.T) {
	svc := &PricingService{
		pricingData: map[string]*LiteLLMModelPricing{
			"gpt-5.1-codex": {InputCostPerToken: 1.25e-6},
		},
	}

	got := svc.GetModelPricing("gpt-5.4-mini")
	require.NotNil(t, got)
	require.InDelta(t, 7.5e-7, got.InputCostPerToken, 1e-12)
	require.InDelta(t, 4.5e-6, got.OutputCostPerToken, 1e-12)
	require.InDelta(t, 7.5e-8, got.CacheReadInputTokenCost, 1e-12)
	require.Zero(t, got.LongContextInputTokenThreshold)
}

func TestGetModelPricing_Gpt54NanoUsesDedicatedStaticFallbackWhenRemoteMissing(t *testing.T) {
	svc := &PricingService{
		pricingData: map[string]*LiteLLMModelPricing{
			"gpt-5.1-codex": {InputCostPerToken: 1.25e-6},
		},
	}

	got := svc.GetModelPricing("gpt-5.4-nano")
	require.NotNil(t, got)
	require.InDelta(t, 2e-7, got.InputCostPerToken, 1e-12)
	require.InDelta(t, 1.25e-6, got.OutputCostPerToken, 1e-12)
	require.InDelta(t, 2e-8, got.CacheReadInputTokenCost, 1e-12)
	require.Zero(t, got.LongContextInputTokenThreshold)
}

func TestGetModelPricing_ImageModelDoesNotFallbackToTextModel(t *testing.T) {
	imagePricing := &LiteLLMModelPricing{InputCostPerToken: 3}
	textPricing := &LiteLLMModelPricing{InputCostPerToken: 9}

	svc := &PricingService{
		pricingData: map[string]*LiteLLMModelPricing{
			"gpt-image-2": imagePricing,
			"gpt-5.4":     textPricing,
		},
	}

	got := svc.GetModelPricing("gpt-image-3")
	require.Same(t, imagePricing, got)
}

func TestParsePricingData_PreservesPriorityAndServiceTierFields(t *testing.T) {
	raw := map[string]any{
		"gpt-5.4": map[string]any{
			"input_cost_per_token":                 2.5e-6,
			"input_cost_per_token_priority":        5e-6,
			"output_cost_per_token":                15e-6,
			"output_cost_per_token_priority":       30e-6,
			"cache_read_input_token_cost":          0.25e-6,
			"cache_read_input_token_cost_priority": 0.5e-6,
			"supports_service_tier":                true,
			"supports_prompt_caching":              true,
			"litellm_provider":                     "openai",
			"mode":                                 "chat",
		},
	}
	body, err := json.Marshal(raw)
	require.NoError(t, err)

	svc := &PricingService{}
	pricingMap, err := svc.parsePricingData(body)
	require.NoError(t, err)

	pricing := pricingMap["gpt-5.4"]
	require.NotNil(t, pricing)
	require.InDelta(t, 2.5e-6, pricing.InputCostPerToken, 1e-12)
	require.InDelta(t, 5e-6, pricing.InputCostPerTokenPriority, 1e-12)
	require.InDelta(t, 15e-6, pricing.OutputCostPerToken, 1e-12)
	require.InDelta(t, 30e-6, pricing.OutputCostPerTokenPriority, 1e-12)
	require.InDelta(t, 0.25e-6, pricing.CacheReadInputTokenCost, 1e-12)
	require.InDelta(t, 0.5e-6, pricing.CacheReadInputTokenCostPriority, 1e-12)
	require.True(t, pricing.SupportsServiceTier)
}

func TestParsePricingData_PreservesServiceTierPriorityFields(t *testing.T) {
	svc := &PricingService{}
	pricingData, err := svc.parsePricingData([]byte(`{
		"gpt-5.4": {
			"input_cost_per_token": 0.0000025,
			"input_cost_per_token_priority": 0.000005,
			"output_cost_per_token": 0.000015,
			"output_cost_per_token_priority": 0.00003,
			"cache_read_input_token_cost": 0.00000025,
			"cache_read_input_token_cost_priority": 0.0000005,
			"supports_service_tier": true,
			"litellm_provider": "openai",
			"mode": "chat"
		}
	}`))
	require.NoError(t, err)

	pricing := pricingData["gpt-5.4"]
	require.NotNil(t, pricing)
	require.InDelta(t, 0.0000025, pricing.InputCostPerToken, 1e-12)
	require.InDelta(t, 0.000005, pricing.InputCostPerTokenPriority, 1e-12)
	require.InDelta(t, 0.000015, pricing.OutputCostPerToken, 1e-12)
	require.InDelta(t, 0.00003, pricing.OutputCostPerTokenPriority, 1e-12)
	require.InDelta(t, 0.00000025, pricing.CacheReadInputTokenCost, 1e-12)
	require.InDelta(t, 0.0000005, pricing.CacheReadInputTokenCostPriority, 1e-12)
	require.True(t, pricing.SupportsServiceTier)
}
