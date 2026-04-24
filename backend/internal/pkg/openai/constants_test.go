package openai

import "testing"

func TestDefaultModels_DoesNotAdvertiseGPT55Yet(t *testing.T) {
	for _, model := range DefaultModels {
		if model.ID == "gpt-5.5" {
			t.Fatal("gpt-5.5 should not be included in DefaultModels before official API availability")
		}
	}
}
