package service

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHappyHorseParseGenerateRequest_DefaultsModel(t *testing.T) {
	svc := NewHappyHorseGatewayService(nil, nil, nil, nil)
	req, err := svc.ParseGenerateRequest([]byte(`{"prompt":"make a horse video"}`))
	if err != nil {
		t.Fatalf("ParseGenerateRequest returned error: %v", err)
	}
	if req.Model != HappyHorseDefaultModel {
		t.Fatalf("model = %q, want %q", req.Model, HappyHorseDefaultModel)
	}
	if req.Prompt != "make a horse video" {
		t.Fatalf("prompt = %q", req.Prompt)
	}
	var payload map[string]any
	if err := json.Unmarshal(req.Body, &payload); err != nil {
		t.Fatalf("unmarshal rewritten body: %v", err)
	}
	if payload["model"] != HappyHorseDefaultModel {
		t.Fatalf("rewritten model = %v, want %q", payload["model"], HappyHorseDefaultModel)
	}
}

func TestHappyHorseParseGenerateRequest_AllowsMultiPrompt(t *testing.T) {
	svc := NewHappyHorseGatewayService(nil, nil, nil, nil)
	req, err := svc.ParseGenerateRequest([]byte(`{"multi_shots":true,"multi_prompt":[{"prompt":"shot one"},{"prompt":"shot two"}]}`))
	if err != nil {
		t.Fatalf("ParseGenerateRequest returned error: %v", err)
	}
	if req.Prompt != "shot one\nshot two" {
		t.Fatalf("prompt = %q", req.Prompt)
	}
}

func TestHappyHorseParseGenerateRequest_RequiresPromptOrMultiPrompt(t *testing.T) {
	svc := NewHappyHorseGatewayService(nil, nil, nil, nil)
	_, err := svc.ParseGenerateRequest([]byte(`{"multi_shots":true,"multi_prompt":[{"image":"https://example.test/a.png"}]}`))
	if err == nil || !strings.Contains(err.Error(), "prompt or multi_prompt") {
		t.Fatalf("error = %v, want prompt requirement", err)
	}
}

func TestParseHappyHorsePayload_ErrorMessage(t *testing.T) {
	payload := parseHappyHorsePayload([]byte(`{"data":{"status":"failed","error_message":"render failed"}}`))
	if payload.Status != "failed" {
		t.Fatalf("status = %q", payload.Status)
	}
	if payload.ErrorMessage != "render failed" {
		t.Fatalf("error message = %q", payload.ErrorMessage)
	}
}

func TestValidateHappyHorseAccount(t *testing.T) {
	account := &Account{
		Platform:    PlatformHappyHorse,
		Type:        AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "hh-test"},
	}
	if err := validateHappyHorseAccount(account); err != nil {
		t.Fatalf("validateHappyHorseAccount returned error: %v", err)
	}
	account.Credentials = map[string]any{"api_key": ""}
	if err := validateHappyHorseAccount(account); err == nil {
		t.Fatal("validateHappyHorseAccount should reject empty api_key")
	}
}
