package openai

import "testing"

func TestDefaultModels_AdvertisesGPT56Series(t *testing.T) {
	want := map[string]bool{
		"gpt-5.6":       false,
		"gpt-5.6-sol":   false,
		"gpt-5.6-terra": false,
		"gpt-5.6-luna":  false,
	}
	for _, model := range DefaultModels {
		if _, ok := want[model.ID]; ok {
			want[model.ID] = true
		}
	}
	for id, found := range want {
		if !found {
			t.Fatalf("expected %s to be included in DefaultModels", id)
		}
	}
}

func TestDefaultModels_DoesNotAdvertiseGPT55Yet(t *testing.T) {
	for _, model := range DefaultModels {
		if model.ID == "gpt-5.5" {
			t.Fatal("gpt-5.5 should not be included in DefaultModels before official API availability")
		}
	}
}
