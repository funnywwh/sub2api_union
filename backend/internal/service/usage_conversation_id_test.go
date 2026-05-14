package service

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestResolveUsageConversationID_Priority(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	ctx.Request.Header.Set("conversation_id", "conv-header")
	ctx.Request.Header.Set("session_id", "session-header")

	body := []byte(`{"prompt_cache_key":"prompt-cache","metadata":{"user_id":"user_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa_account__session_11111111-2222-3333-4444-555555555555"}}`)
	parsed := &ParsedRequest{
		MetadataUserID: "user_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb_account__session_66666666-7777-8888-9999-000000000000",
	}

	if got := ResolveUsageConversationID(ctx, body, parsed); got != "conv-header" {
		t.Fatalf("ResolveUsageConversationID() = %q, want %q", got, "conv-header")
	}
}

func TestResolveUsageConversationID_Fallbacks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/messages", nil)

	promptBody := []byte(`{"prompt_cache_key":"prompt-cache","metadata":{"user_id":"user_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa_account__session_11111111-2222-3333-4444-555555555555"}}`)
	if got := ResolveUsageConversationID(ctx, promptBody, nil); got != "prompt-cache" {
		t.Fatalf("ResolveUsageConversationID() prompt_cache_key = %q, want %q", got, "prompt-cache")
	}

	parsed := &ParsedRequest{
		MetadataUserID: "user_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb_account__session_66666666-7777-8888-9999-000000000000",
	}
	if got := ResolveUsageConversationID(ctx, nil, parsed); got != "66666666-7777-8888-9999-000000000000" {
		t.Fatalf("ResolveUsageConversationID() parsed metadata = %q, want parsed session", got)
	}

	metadataBody := []byte(`{"metadata":{"user_id":"user_cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc_account__session_99999999-aaaa-bbbb-cccc-dddddddddddd"}}`)
	if got := ResolveUsageConversationID(ctx, metadataBody, nil); got != "99999999-aaaa-bbbb-cccc-dddddddddddd" {
		t.Fatalf("ResolveUsageConversationID() body metadata = %q, want metadata session", got)
	}
}
