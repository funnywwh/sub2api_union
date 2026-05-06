package service

import "testing"

func TestResolveUsageBillingModel(t *testing.T) {
	tests := []struct {
		name               string
		defaultBillingModel string
		upstreamModel      string
		source             string
		originalModel      string
		channelMappedModel string
		want               string
	}{
		{
			name:               "upstream uses actual upstream model",
			defaultBillingModel: "GPT5.4",
			upstreamModel:      "gpt-5.4",
			source:             BillingModelSourceUpstream,
			originalModel:      "GPT5.4",
			channelMappedModel: "GPT5.4",
			want:               "gpt-5.4",
		},
		{
			name:               "requested uses original model",
			defaultBillingModel: "gpt-5.4",
			upstreamModel:      "gpt-5.4",
			source:             BillingModelSourceRequested,
			originalModel:      "GPT5.4",
			channelMappedModel: "gpt-5.4",
			want:               "GPT5.4",
		},
		{
			name:               "channel mapped uses mapped model when mapping changed",
			defaultBillingModel: "gpt-5.4",
			upstreamModel:      "gpt-5.4",
			source:             BillingModelSourceChannelMapped,
			originalModel:      "GPT5.5",
			channelMappedModel: "gpt-5.4",
			want:               "gpt-5.4",
		},
		{
			name:               "channel mapped keeps default when no channel mapping",
			defaultBillingModel: "gpt-5.4",
			upstreamModel:      "gpt-5.4",
			source:             BillingModelSourceChannelMapped,
			originalModel:      "GPT5.4",
			channelMappedModel: "GPT5.4",
			want:               "gpt-5.4",
		},
		{
			name:               "upstream falls back to default when empty",
			defaultBillingModel: "GPT5.4",
			upstreamModel:      "",
			source:             BillingModelSourceUpstream,
			originalModel:      "GPT5.4",
			channelMappedModel: "GPT5.4",
			want:               "GPT5.4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveUsageBillingModel(
				t.defaultBillingModel,
				t.upstreamModel,
				t.source,
				t.originalModel,
				t.channelMappedModel,
			)
			if got != tt.want {
				t.Fatalf("resolveUsageBillingModel() = %q, want %q", got, tt.want)
			}
		})
	}
}
