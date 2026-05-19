package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type UserChatHandler struct {
	apiKeyService       *service.APIKeyService
	subscriptionService *service.SubscriptionService
	gatewayService      *service.GatewayService
	gatewayHandler      *GatewayHandler
	openAIGateway       *OpenAIGatewayHandler
}

func NewUserChatHandler(
	apiKeyService *service.APIKeyService,
	subscriptionService *service.SubscriptionService,
	gatewayService *service.GatewayService,
	gatewayHandler *GatewayHandler,
	openAIGateway *OpenAIGatewayHandler,
) *UserChatHandler {
	return &UserChatHandler{
		apiKeyService:       apiKeyService,
		subscriptionService: subscriptionService,
		gatewayService:      gatewayService,
		gatewayHandler:      gatewayHandler,
		openAIGateway:       openAIGateway,
	}
}

func isSupportedBrowserChatGroup(group service.Group) bool {
	if !group.IsActive() || group.ClaudeCodeOnly {
		return false
	}

	switch group.Platform {
	case service.PlatformAnthropic, service.PlatformOpenAI, service.PlatformAntigravity:
		return true
	default:
		return false
	}
}

func (h *UserChatHandler) resolveChatGroup(ctx context.Context, userID int64, requestedGroupID *int64) (*service.Group, error) {
	groups, err := h.apiKeyService.GetAvailableGroups(ctx, userID)
	if err != nil {
		return nil, err
	}

	supported := make([]service.Group, 0, len(groups))
	for _, group := range groups {
		if isSupportedBrowserChatGroup(group) {
			supported = append(supported, group)
		}
	}

	if len(supported) == 0 {
		return nil, service.ErrGroupNotAllowed
	}

	if requestedGroupID == nil {
		group := supported[0]
		return &group, nil
	}

	for _, group := range supported {
		if group.ID == *requestedGroupID {
			cp := group
			return &cp, nil
		}
	}

	return nil, service.ErrGroupNotAllowed
}

func (h *UserChatHandler) prepareGatewayContext(c *gin.Context, apiKey *service.APIKey, group *service.Group) error {
	return h.prepareGatewayContextWithEndpoint(c, apiKey, group, EndpointChatCompletions)
}

func (h *UserChatHandler) prepareGatewayContextWithEndpoint(c *gin.Context, apiKey *service.APIKey, group *service.Group, inboundEndpoint string) error {
	if apiKey == nil || apiKey.User == nil || group == nil {
		return service.ErrAPIKeyNotFound
	}

	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		return service.ErrUserNotFound
	}

	clonedKey := cloneAPIKeyWithGroup(apiKey, group)
	c.Set(string(middleware2.ContextKeyAPIKey), clonedKey)
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{
		UserID:      subject.UserID,
		Concurrency: apiKey.User.Concurrency,
	})
	c.Set(string(middleware2.ContextKeyUserRole), apiKey.User.Role)
	c.Set(ctxKeyInboundEndpoint, inboundEndpoint)

	if group.IsSubscriptionType() && h.subscriptionService != nil {
		subscription, err := h.subscriptionService.GetActiveSubscription(c.Request.Context(), apiKey.UserID, group.ID)
		if err != nil {
			return err
		}
		c.Set(string(middleware2.ContextKeySubscription), subscription)
	}

	ctx := context.WithValue(c.Request.Context(), ctxkey.Group, group)
	c.Request = c.Request.WithContext(ctx)
	_ = h.apiKeyService.TouchLastUsed(c.Request.Context(), apiKey.ID)
	return nil
}

func isGeminiImageGenerationModel(model string) bool {
	lower := strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(lower, "gemini-") && strings.Contains(lower, "-image")
}

func buildGeminiImageGenerationPayload(prompt string) []byte {
	payload := map[string]any{
		"contents": []map[string]any{
			{
				"role": "user",
				"parts": []map[string]any{
					{"text": prompt},
				},
			},
		},
		"generationConfig": map[string]any{
			"responseModalities": []string{"TEXT", "IMAGE"},
			"imageConfig": map[string]any{
				"aspectRatio": "1:1",
			},
		},
	}
	bytes, _ := json.Marshal(payload)
	return bytes
}

func parseOptionalGroupID(raw string) (*int64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}

	value, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil || value <= 0 {
		return nil, fmt.Errorf("invalid group_id")
	}
	return &value, nil
}

func (h *UserChatHandler) ListModels(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	groupID, err := parseOptionalGroupID(c.Query("group_id"))
	if err != nil {
		response.BadRequest(c, "Invalid group_id")
		return
	}

	group, err := h.resolveChatGroup(c.Request.Context(), subject.UserID, groupID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	availableModels := h.gatewayService.GetAvailableModels(c.Request.Context(), &group.ID, "")
	if len(availableModels) > 0 {
		models := make([]claude.Model, 0, len(availableModels))
		for _, modelID := range availableModels {
			models = append(models, claude.Model{
				ID:          modelID,
				Type:        "model",
				DisplayName: modelID,
				CreatedAt:   "2024-01-01T00:00:00Z",
			})
		}
		c.JSON(http.StatusOK, gin.H{
			"object": "list",
			"data":   models,
		})
		return
	}

	switch group.Platform {
	case service.PlatformOpenAI:
		c.JSON(http.StatusOK, gin.H{
			"object": "list",
			"data":   openai.DefaultModels,
		})
	case service.PlatformAntigravity:
		c.JSON(http.StatusOK, gin.H{
			"object": "list",
			"data":   antigravity.DefaultModels(),
		})
	default:
		c.JSON(http.StatusOK, gin.H{
			"object": "list",
			"data":   claude.DefaultModels,
		})
	}
}

func (h *UserChatHandler) ChatCompletions(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		response.BadRequest(c, "Failed to read request body")
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))

	groupIDResult := gjson.GetBytes(body, "group_id")
	var requestedGroupID *int64
	if groupIDResult.Exists() {
		value := groupIDResult.Int()
		if value <= 0 {
			response.BadRequest(c, "Invalid group_id")
			return
		}
		requestedGroupID = &value
	}

	group, err := h.resolveChatGroup(c.Request.Context(), subject.UserID, requestedGroupID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	apiKey, err := h.apiKeyService.GetOrCreateWebChatAPIKey(c.Request.Context(), subject.UserID, group.ID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	if err := h.prepareGatewayContext(c, apiKey, group); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	if group.Platform == service.PlatformOpenAI {
		h.openAIGateway.ChatCompletions(c)
		return
	}

	h.gatewayHandler.ChatCompletions(c)
}

func (h *UserChatHandler) Images(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		response.BadRequest(c, "Failed to read request body")
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))

	groupIDResult := gjson.GetBytes(body, "group_id")
	var requestedGroupID *int64
	if groupIDResult.Exists() {
		value := groupIDResult.Int()
		if value <= 0 {
			response.BadRequest(c, "Invalid group_id")
			return
		}
		requestedGroupID = &value
	}

	model := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	prompt := strings.TrimSpace(gjson.GetBytes(body, "prompt").String())
	if prompt == "" {
		response.BadRequest(c, "prompt is required")
		return
	}

	group, err := h.resolveChatGroup(c.Request.Context(), subject.UserID, requestedGroupID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	apiKey, err := h.apiKeyService.GetOrCreateWebChatAPIKey(c.Request.Context(), subject.UserID, group.ID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	switch group.Platform {
	case service.PlatformOpenAI:
		if err := h.prepareGatewayContextWithEndpoint(c, apiKey, group, EndpointImagesGenerations); err != nil {
			response.ErrorFrom(c, err)
			return
		}

		payload := map[string]any{
			"model":  strings.TrimSpace(model),
			"prompt": prompt,
			"n":      1,
		}
		payloadBytes, _ := json.Marshal(payload)
		c.Request.Body = io.NopCloser(bytes.NewReader(payloadBytes))
		c.Request.ContentLength = int64(len(payloadBytes))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Request.URL.Path = EndpointImagesGenerations
		c.Set(ctxKeyInboundEndpoint, EndpointImagesGenerations)
		h.openAIGateway.Images(c)
		return

	case service.PlatformAntigravity:
		if !isGeminiImageGenerationModel(model) {
			response.BadRequest(c, "Selected model does not support image generation")
			return
		}
		if err := h.prepareGatewayContextWithEndpoint(c, apiKey, group, EndpointGeminiModels); err != nil {
			response.ErrorFrom(c, err)
			return
		}

		payloadBytes := buildGeminiImageGenerationPayload(prompt)
		c.Request.Body = io.NopCloser(bytes.NewReader(payloadBytes))
		c.Request.ContentLength = int64(len(payloadBytes))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Request.URL.Path = fmt.Sprintf("/antigravity/v1beta/models/%s:streamGenerateContent", model)
		c.Request.URL.RawQuery = "alt=sse"
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.ForcePlatform, service.PlatformAntigravity))
		c.Set(string(middleware2.ContextKeyForcePlatform), service.PlatformAntigravity)
		c.Set(ctxKeyInboundEndpoint, EndpointGeminiModels)
		c.Params = gin.Params{
			{Key: "modelAction", Value: "/" + model + ":streamGenerateContent"},
		}
		h.gatewayHandler.GeminiV1BetaModels(c)
		return

	default:
		response.BadRequest(c, "Image generation is not supported for this group")
	}
}
