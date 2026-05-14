package service

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// ResolveUsageConversationID extracts the best available conversation/session identifier
// for usage logging without falling back to content hashes.
//
// Priority:
//  1. Header: conversation_id
//  2. Header: session_id
//  3. Body:   prompt_cache_key
//  4. Body/parsed metadata.user_id -> parsed session UUID
func ResolveUsageConversationID(c *gin.Context, body []byte, parsed *ParsedRequest) string {
	if c != nil {
		if conversationID := strings.TrimSpace(c.GetHeader("conversation_id")); conversationID != "" {
			return conversationID
		}
		if sessionID := strings.TrimSpace(c.GetHeader("session_id")); sessionID != "" {
			return sessionID
		}
	}

	if len(body) > 0 {
		if promptCacheKey := strings.TrimSpace(gjson.GetBytes(body, "prompt_cache_key").String()); promptCacheKey != "" {
			return promptCacheKey
		}
	}

	if parsed != nil {
		if metadataSessionID := resolveMetadataUserSessionID(parsed.MetadataUserID); metadataSessionID != "" {
			return metadataSessionID
		}
	}

	if len(body) > 0 {
		if metadataSessionID := resolveMetadataUserSessionID(gjson.GetBytes(body, "metadata.user_id").String()); metadataSessionID != "" {
			return metadataSessionID
		}
	}

	return ""
}

func resolveMetadataUserSessionID(raw string) string {
	if parsed := ParseMetadataUserID(raw); parsed != nil {
		return strings.TrimSpace(parsed.SessionID)
	}
	return ""
}
