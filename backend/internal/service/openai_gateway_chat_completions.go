package service

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"
)

// cursorResponsesUnsupportedFields are top-level Responses API parameters that
// Codex upstreams reject with "Unsupported parameter: ...". They must be
// stripped when forwarding a raw client body through the Responses-shape
// short-circuit in ForwardAsChatCompletions (see isResponsesShape branch).
// The normal Chat Completions → Responses conversion path is unaffected
// because ChatCompletionsRequest has no fields for these parameters — unknown
// fields are dropped naturally by json.Unmarshal. Kept semantically in sync
// with the list in openai_gateway_service.go:2034 used by the /v1/responses
// passthrough path.
var cursorResponsesUnsupportedFields = []string{
	"prompt_cache_retention",
	"safety_identifier",
	"metadata",
	"stream_options",
}

// ---------------------------------------------------------------------------
// Chat reasoning cache — backfills reasoning_content for clients (e.g. Codex)
// that don't preserve DeepSeek's reasoning_content field across turns.
// ---------------------------------------------------------------------------

// chatReasoningCacheEntry holds a cached reasoning string with TTL.
type chatReasoningCacheEntry struct {
	reasoningContent string
	expiresAt        time.Time
}

// chatReasoningCache maps a message fingerprint to its reasoning_content.
type chatReasoningCache struct {
	mu      sync.RWMutex
	entries map[string]chatReasoningCacheEntry
}

var defaultChatReasoningCache = &chatReasoningCache{
	entries: make(map[string]chatReasoningCacheEntry),
}

// chatReasoningCacheTTL is how long we keep a reasoning entry.
const chatReasoningCacheTTL = 30 * time.Minute

// cacheKeyForChatMessage builds a stable key from content + tool_calls.
func cacheKeyForChatMessage(msg apicompat.ChatMessage) string {
	h := sha256.New()
	_, _ = h.Write(msg.Content)
	for _, tc := range msg.ToolCalls {
		_, _ = h.Write([]byte(tc.ID))
		_, _ = h.Write([]byte(tc.Function.Name))
		_, _ = h.Write([]byte(tc.Function.Arguments))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (c *chatReasoningCache) Set(key, reasoning string) {
	if key == "" || strings.TrimSpace(reasoning) == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]chatReasoningCacheEntry)
	}
	c.entries[key] = chatReasoningCacheEntry{
		reasoningContent: reasoning,
		expiresAt:        time.Now().Add(chatReasoningCacheTTL),
	}
}

func (c *chatReasoningCache) Get(key string) string {
	if key == "" {
		return ""
	}
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok || time.Now().After(entry.expiresAt) {
		return ""
	}
	return entry.reasoningContent
}

// backfillChatMessagesReasoning injects cached reasoning_content into assistant
// messages that are missing it. Mutates the slice in-place.
func backfillChatMessagesReasoning(messages []apicompat.ChatMessage) {
	for i := range messages {
		if messages[i].Role != "assistant" {
			continue
		}
		if strings.TrimSpace(messages[i].ReasoningContent) != "" {
			continue
		}
		key := cacheKeyForChatMessage(messages[i])
		if reasoning := defaultChatReasoningCache.Get(key); reasoning != "" {
			messages[i].ReasoningContent = reasoning
		}
	}
}

// cacheChatResponseReasoning extracts reasoning_content from a non-streaming
// Chat Completions response and stores it in the global cache.
func cacheChatResponseReasoning(respBody []byte) {
	var chatResp struct {
		Choices []struct {
			Message apicompat.ChatMessage `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return
	}
	for _, choice := range chatResp.Choices {
		msg := choice.Message
		if msg.Role != "assistant" || strings.TrimSpace(msg.ReasoningContent) == "" {
			continue
		}
		key := cacheKeyForChatMessage(msg)
		defaultChatReasoningCache.Set(key, msg.ReasoningContent)
	}
}

func extractOpenAIUsageFromChatCompletionJSONBytes(data []byte) (OpenAIUsage, bool) {
	if len(data) == 0 || !gjson.ValidBytes(data) {
		return OpenAIUsage{}, false
	}
	if !gjson.GetBytes(data, "usage").Exists() {
		return OpenAIUsage{}, false
	}
	cachedTokens := gjson.GetBytes(data, "usage.prompt_tokens_details.cached_tokens").Int()
	if cachedTokens == 0 {
		cachedTokens = gjson.GetBytes(data, "usage.cached_tokens").Int()
	}
	if cachedTokens == 0 {
		cachedTokens = gjson.GetBytes(data, "usage.cache_read_input_tokens").Int()
	}
	return OpenAIUsage{
		InputTokens:              int(gjson.GetBytes(data, "usage.prompt_tokens").Int()),
		OutputTokens:             int(gjson.GetBytes(data, "usage.completion_tokens").Int()),
		CacheCreationInputTokens: int(gjson.GetBytes(data, "usage.cache_creation_input_tokens").Int()),
		CacheReadInputTokens:     int(cachedTokens),
	}, true
}

func openAIUsageFromChatUsage(usage *apicompat.ChatUsage) OpenAIUsage {
	if usage == nil {
		return OpenAIUsage{}
	}
	cachedTokens := 0
	if usage.PromptTokensDetails != nil {
		cachedTokens = usage.PromptTokensDetails.CachedTokens
	}
	return OpenAIUsage{
		InputTokens:              usage.PromptTokens,
		OutputTokens:             usage.CompletionTokens,
		CacheCreationInputTokens: usage.CacheCreationInputTokens,
		CacheReadInputTokens:     cachedTokens,
	}
}

func openAIUsageFromResponsesUsage(usage *apicompat.ResponsesUsage) OpenAIUsage {
	if usage == nil {
		return OpenAIUsage{}
	}
	result := OpenAIUsage{
		InputTokens:              usage.InputTokens,
		OutputTokens:             usage.OutputTokens,
		CacheCreationInputTokens: usage.CacheCreationInputTokens,
	}
	if usage.InputTokensDetails != nil {
		result.CacheReadInputTokens = usage.InputTokensDetails.CachedTokens
	}
	return result
}

func responsesUsageFromChatUsage(usage *apicompat.ChatUsage) *apicompat.ResponsesUsage {
	if usage == nil {
		return nil
	}
	totalTokens := usage.TotalTokens
	if totalTokens == 0 {
		totalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	out := &apicompat.ResponsesUsage{
		InputTokens:              usage.PromptTokens,
		OutputTokens:             usage.CompletionTokens,
		TotalTokens:              totalTokens,
		CacheCreationInputTokens: usage.CacheCreationInputTokens,
	}
	if usage.PromptTokensDetails != nil && usage.PromptTokensDetails.CachedTokens > 0 {
		out.InputTokensDetails = &apicompat.ResponsesInputTokensDetails{
			CachedTokens: usage.PromptTokensDetails.CachedTokens,
		}
	}
	return out
}

// ForwardAsChatCompletions accepts a Chat Completions request body, converts it
// to OpenAI Responses API format, forwards to the OpenAI upstream, and converts
// the response back to Chat Completions format.
//
// For API Key accounts targeting a non-OpenAI upstream (e.g., DeepSeek, Ollama),
// the request is forwarded directly as Chat Completions format — no Responses API
// conversion is performed — since those providers only support /v1/chat/completions.
func (s *OpenAIGatewayService) ForwardAsChatCompletions(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	promptCacheKey string,
	defaultMappedModel string,
) (*OpenAIForwardResult, error) {
	startTime := time.Now()

	// 1. Parse Chat Completions request
	var chatReq apicompat.ChatCompletionsRequest
	if err := json.Unmarshal(body, &chatReq); err != nil {
		return nil, fmt.Errorf("parse chat completions request: %w", err)
	}
	originalModel := chatReq.Model
	clientStream := chatReq.Stream
	includeUsage := chatReq.StreamOptions != nil && chatReq.StreamOptions.IncludeUsage

	// 2. Resolve model mapping early so compat prompt_cache_key injection can
	// derive a stable seed from the final upstream model family.
	billingModel := resolveOpenAIForwardModel(account, originalModel, defaultMappedModel)
	upstreamModel := normalizeOpenAIModelForUpstream(account, billingModel)

	// Passthrough path: for third-party OpenAI-compatible providers (DeepSeek, Ollama, etc.)
	// that only support /v1/chat/completions, forward the request directly without
	// converting to/from the Responses API format.
	if account.Type == AccountTypeAPIKey && !account.IsOpenAIOfficial() {
		return s.forwardChatCompletionsPassthrough(ctx, c, account, body, upstreamModel, originalModel, billingModel, clientStream, startTime)
	}

	promptCacheKey = strings.TrimSpace(promptCacheKey)
	compatPromptCacheInjected := false
	if promptCacheKey == "" && account.Type == AccountTypeOAuth && shouldAutoInjectPromptCacheKeyForCompat(upstreamModel) {
		promptCacheKey = deriveCompatPromptCacheKey(&chatReq, upstreamModel)
		compatPromptCacheInjected = promptCacheKey != ""
	}

	// 3. Build the upstream (Responses API) body.
	//
	// Cursor compatibility: some clients (notably Cursor cloud) send Responses
	// API shaped bodies — `input: [...]` with no `messages` field — to the
	// /v1/chat/completions URL. Running those through ChatCompletionsToResponses
	// would silently drop Cursor's `input` array (the struct has no Input field)
	// and produce `input: null`, which Codex upstreams reject with
	// "Invalid type for 'input': expected a string, but got an object".
	//
	// Detect that shape and forward the raw body as-is, only rewriting `model`
	// to the resolved upstream model. The downstream codex OAuth transform will
	// still normalize store/stream/instructions/etc.
	isResponsesShape := !gjson.GetBytes(body, "messages").Exists() && gjson.GetBytes(body, "input").Exists()

	var (
		responsesReq  *apicompat.ResponsesRequest
		responsesBody []byte
		err           error
	)
	if isResponsesShape {
		responsesBody, err = sjson.SetBytes(body, "model", upstreamModel)
		if err != nil {
			return nil, fmt.Errorf("rewrite model in responses-shape body: %w", err)
		}
		// Strip Responses API parameters that no Codex upstream accepts.
		// Because this branch forwards the raw body (the normal path rebuilds
		// it from ChatCompletionsRequest and drops unknown fields naturally),
		// we must filter these fields explicitly here — otherwise the upstream
		// rejects the request with "Unsupported parameter: ...".
		for _, field := range cursorResponsesUnsupportedFields {
			if stripped, derr := sjson.DeleteBytes(responsesBody, field); derr == nil {
				responsesBody = stripped
			}
		}
		responsesBody, normalizedServiceTier, err := normalizeResponsesBodyServiceTier(responsesBody)
		if err != nil {
			return nil, fmt.Errorf("normalize service_tier in responses-shape body: %w", err)
		}
		// Minimal stub populated from the raw body so downstream billing
		// propagation (ServiceTier, ReasoningEffort) keeps working.
		responsesReq = &apicompat.ResponsesRequest{
			Model:       upstreamModel,
			ServiceTier: normalizedServiceTier,
		}
		if effort := gjson.GetBytes(responsesBody, "reasoning.effort").String(); effort != "" {
			responsesReq.Reasoning = &apicompat.ResponsesReasoning{Effort: effort}
		}
	} else {
		// Normal path: convert Chat Completions → Responses.
		// ChatCompletionsToResponses always sets Stream=true (upstream always streams).
		responsesReq, err = apicompat.ChatCompletionsToResponses(&chatReq)
		if err != nil {
			return nil, fmt.Errorf("convert chat completions to responses: %w", err)
		}
		responsesReq.Model = upstreamModel
		normalizeResponsesRequestServiceTier(responsesReq)
		responsesBody, err = json.Marshal(responsesReq)
		if err != nil {
			return nil, fmt.Errorf("marshal responses request: %w", err)
		}
	}

	logFields := []zap.Field{
		zap.Int64("account_id", account.ID),
		zap.String("original_model", originalModel),
		zap.String("billing_model", billingModel),
		zap.String("upstream_model", upstreamModel),
		zap.Bool("stream", clientStream),
		zap.Bool("responses_shape", isResponsesShape),
	}
	if compatPromptCacheInjected {
		logFields = append(logFields,
			zap.Bool("compat_prompt_cache_key_injected", true),
			zap.String("compat_prompt_cache_key_sha256", hashSensitiveValueForLog(promptCacheKey)),
		)
	}
	logger.L().Debug("openai chat_completions: model mapping applied", logFields...)

	if account.Type == AccountTypeOAuth {
		var reqBody map[string]any
		if err := json.Unmarshal(responsesBody, &reqBody); err != nil {
			return nil, fmt.Errorf("unmarshal for codex transform: %w", err)
		}
		codexResult := applyCodexOAuthTransform(reqBody, false, false)
		if codexResult.NormalizedModel != "" {
			upstreamModel = codexResult.NormalizedModel
		}
		if codexResult.PromptCacheKey != "" {
			promptCacheKey = codexResult.PromptCacheKey
		} else if promptCacheKey != "" {
			reqBody["prompt_cache_key"] = promptCacheKey
		}
		responsesBody, err = json.Marshal(reqBody)
		if err != nil {
			return nil, fmt.Errorf("remarshal after codex transform: %w", err)
		}
	}

	// 5. Get access token
	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("get access token: %w", err)
	}

	// 6. Build upstream request
	upstreamReq, err := s.buildUpstreamRequest(ctx, c, account, responsesBody, token, true, promptCacheKey, false)
	if err != nil {
		return nil, fmt.Errorf("build upstream request: %w", err)
	}

	if promptCacheKey != "" {
		upstreamReq.Header.Set("session_id", generateSessionUUID(promptCacheKey))
	}

	// 7. Send request
	proxyURL := ""
	if account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.Do(upstreamReq, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		safeErr := sanitizeUpstreamErrorMessage(err.Error())
		setOpsUpstreamError(c, 0, safeErr, "")
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: 0,
			Kind:               "request_error",
			Message:            safeErr,
		})
		writeChatCompletionsError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed")
		return nil, fmt.Errorf("upstream request failed: %s", safeErr)
	}
	defer func() { _ = resp.Body.Close() }()

	// 8. Handle error response with failover
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(respBody))

		upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(respBody))
		upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)
		if s.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, upstreamMsg, respBody) {
			upstreamDetail := ""
			if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
				maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
				if maxBytes <= 0 {
					maxBytes = 2048
				}
				upstreamDetail = truncateString(string(respBody), maxBytes)
			}
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: resp.StatusCode,
				UpstreamRequestID:  resp.Header.Get("x-request-id"),
				Kind:               "failover",
				Message:            upstreamMsg,
				Detail:             upstreamDetail,
			})
			if s.rateLimitService != nil {
				s.rateLimitService.HandleUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody)
			}
			return nil, &UpstreamFailoverError{
				StatusCode:             resp.StatusCode,
				ResponseBody:           respBody,
				RetryableOnSameAccount: account.IsPoolMode() && (isPoolModeRetryableStatus(resp.StatusCode) || isOpenAITransientProcessingError(resp.StatusCode, upstreamMsg, respBody)),
			}
		}
		return s.handleChatCompletionsErrorResponse(resp, c, account)
	}

	// 9. Handle normal response
	var result *OpenAIForwardResult
	var handleErr error
	if clientStream {
		result, handleErr = s.handleChatStreamingResponse(resp, c, originalModel, billingModel, upstreamModel, includeUsage, startTime)
	} else {
		result, handleErr = s.handleChatBufferedStreamingResponse(resp, c, originalModel, billingModel, upstreamModel, startTime)
	}

	// Propagate ServiceTier and ReasoningEffort to result for billing
	if handleErr == nil && result != nil {
		if responsesReq.ServiceTier != "" {
			st := responsesReq.ServiceTier
			result.ServiceTier = &st
		}
		if responsesReq.Reasoning != nil && responsesReq.Reasoning.Effort != "" {
			re := responsesReq.Reasoning.Effort
			result.ReasoningEffort = &re
		}
	}

	// Extract and save Codex usage snapshot from response headers (for OAuth accounts)
	if handleErr == nil && account.Type == AccountTypeOAuth {
		if snapshot := ParseCodexRateLimitHeaders(resp.Header); snapshot != nil {
			s.updateCodexUsageSnapshot(ctx, account.ID, snapshot)
		}
	}

	return result, handleErr
}

func normalizeResponsesRequestServiceTier(req *apicompat.ResponsesRequest) {
	if req == nil {
		return
	}
	req.ServiceTier = normalizedOpenAIServiceTierValue(req.ServiceTier)
}

func normalizeResponsesBodyServiceTier(body []byte) ([]byte, string, error) {
	if len(body) == 0 {
		return body, "", nil
	}
	rawServiceTier := gjson.GetBytes(body, "service_tier").String()
	if rawServiceTier == "" {
		return body, "", nil
	}
	normalizedServiceTier := normalizedOpenAIServiceTierValue(rawServiceTier)
	if normalizedServiceTier == "" {
		trimmed, err := sjson.DeleteBytes(body, "service_tier")
		return trimmed, "", err
	}
	if normalizedServiceTier == rawServiceTier {
		return body, normalizedServiceTier, nil
	}
	trimmed, err := sjson.SetBytes(body, "service_tier", normalizedServiceTier)
	return trimmed, normalizedServiceTier, err
}

func normalizedOpenAIServiceTierValue(raw string) string {
	normalized := normalizeOpenAIServiceTier(raw)
	if normalized == nil {
		return ""
	}
	return *normalized
}

// handleChatCompletionsErrorResponse reads an upstream error and returns it in
// OpenAI Chat Completions error format.
func (s *OpenAIGatewayService) handleChatCompletionsErrorResponse(
	resp *http.Response,
	c *gin.Context,
	account *Account,
) (*OpenAIForwardResult, error) {
	return s.handleCompatErrorResponse(resp, c, account, writeChatCompletionsError)
}

// handleChatBufferedStreamingResponse reads all Responses SSE events from the
// upstream, finds the terminal event, converts to a Chat Completions JSON
// response, and writes it to the client.
func (s *OpenAIGatewayService) handleChatBufferedStreamingResponse(
	resp *http.Response,
	c *gin.Context,
	originalModel string,
	billingModel string,
	upstreamModel string,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")

	scanner := bufio.NewScanner(resp.Body)
	maxLineSize := defaultMaxLineSize
	if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = s.cfg.Gateway.MaxLineSize
	}
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)

	var finalResponse *apicompat.ResponsesResponse
	var usage OpenAIUsage
	acc := apicompat.NewBufferedResponseAccumulator()

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
			continue
		}
		payload := line[6:]

		var event apicompat.ResponsesStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			logger.L().Warn("openai chat_completions buffered: failed to parse event",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
			continue
		}

		// Accumulate delta content for fallback when terminal output is empty.
		acc.ProcessEvent(&event)

		if (event.Type == "response.completed" || event.Type == "response.done" ||
			event.Type == "response.incomplete" || event.Type == "response.failed") &&
			event.Response != nil {
			finalResponse = event.Response
			if event.Response.Usage != nil {
				usage = openAIUsageFromResponsesUsage(event.Response.Usage)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			logger.L().Warn("openai chat_completions buffered: read error",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
		}
	}

	if finalResponse == nil {
		writeChatCompletionsError(c, http.StatusBadGateway, "api_error", "Upstream stream ended without a terminal response event")
		return nil, fmt.Errorf("upstream stream ended without terminal event")
	}

	// When the terminal event has an empty output array, reconstruct from
	// accumulated delta events so the client receives the full content.
	acc.SupplementResponseOutput(finalResponse)

	chatResp := apicompat.ResponsesToChatCompletions(finalResponse, originalModel)

	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	c.JSON(http.StatusOK, chatResp)

	return &OpenAIForwardResult{
		RequestID:     requestID,
		Usage:         usage,
		Model:         originalModel,
		BillingModel:  billingModel,
		UpstreamModel: upstreamModel,
		Stream:        false,
		Duration:      time.Since(startTime),
	}, nil
}

// handleChatStreamingResponse reads Responses SSE events from upstream,
// converts each to Chat Completions SSE chunks, and writes them to the client.
func (s *OpenAIGatewayService) handleChatStreamingResponse(
	resp *http.Response,
	c *gin.Context,
	originalModel string,
	billingModel string,
	upstreamModel string,
	includeUsage bool,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")

	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	state := apicompat.NewResponsesEventToChatState()
	state.Model = originalModel
	state.IncludeUsage = includeUsage

	var usage OpenAIUsage
	var firstTokenMs *int
	firstChunk := true

	scanner := bufio.NewScanner(resp.Body)
	maxLineSize := defaultMaxLineSize
	if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = s.cfg.Gateway.MaxLineSize
	}
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)

	resultWithUsage := func() *OpenAIForwardResult {
		return &OpenAIForwardResult{
			RequestID:     requestID,
			Usage:         usage,
			Model:         originalModel,
			BillingModel:  billingModel,
			UpstreamModel: upstreamModel,
			Stream:        true,
			Duration:      time.Since(startTime),
			FirstTokenMs:  firstTokenMs,
		}
	}

	processDataLine := func(payload string) bool {
		if firstChunk {
			firstChunk = false
			ms := int(time.Since(startTime).Milliseconds())
			firstTokenMs = &ms
		}

		var event apicompat.ResponsesStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			logger.L().Warn("openai chat_completions stream: failed to parse event",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
			return false
		}

		// Extract usage from completion events
		if (event.Type == "response.completed" || event.Type == "response.incomplete" || event.Type == "response.failed") &&
			event.Response != nil && event.Response.Usage != nil {
			usage = openAIUsageFromResponsesUsage(event.Response.Usage)
		}

		chunks := apicompat.ResponsesEventToChatChunks(&event, state)
		for _, chunk := range chunks {
			sse, err := apicompat.ChatChunkToSSE(chunk)
			if err != nil {
				logger.L().Warn("openai chat_completions stream: failed to marshal chunk",
					zap.Error(err),
					zap.String("request_id", requestID),
				)
				continue
			}
			if _, err := fmt.Fprint(c.Writer, sse); err != nil {
				logger.L().Info("openai chat_completions stream: client disconnected",
					zap.String("request_id", requestID),
				)
				return true
			}
		}
		if len(chunks) > 0 {
			c.Writer.Flush()
		}
		return false
	}

	finalizeStream := func() (*OpenAIForwardResult, error) {
		if finalChunks := apicompat.FinalizeResponsesChatStream(state); len(finalChunks) > 0 {
			for _, chunk := range finalChunks {
				sse, err := apicompat.ChatChunkToSSE(chunk)
				if err != nil {
					continue
				}
				fmt.Fprint(c.Writer, sse) //nolint:errcheck
			}
		}
		// Send [DONE] sentinel
		fmt.Fprint(c.Writer, "data: [DONE]\n\n") //nolint:errcheck
		c.Writer.Flush()
		return resultWithUsage(), nil
	}

	handleScanErr := func(err error) {
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			logger.L().Warn("openai chat_completions stream: read error",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
		}
	}

	// Determine keepalive interval
	keepaliveInterval := time.Duration(0)
	if s.cfg != nil && s.cfg.Gateway.StreamKeepaliveInterval > 0 {
		keepaliveInterval = time.Duration(s.cfg.Gateway.StreamKeepaliveInterval) * time.Second
	}

	// No keepalive: fast synchronous path
	if keepaliveInterval <= 0 {
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
				continue
			}
			if processDataLine(line[6:]) {
				return resultWithUsage(), nil
			}
		}
		handleScanErr(scanner.Err())
		return finalizeStream()
	}

	// With keepalive: goroutine + channel + select
	type scanEvent struct {
		line string
		err  error
	}
	events := make(chan scanEvent, 16)
	done := make(chan struct{})
	sendEvent := func(ev scanEvent) bool {
		select {
		case events <- ev:
			return true
		case <-done:
			return false
		}
	}
	go func() {
		defer close(events)
		for scanner.Scan() {
			if !sendEvent(scanEvent{line: scanner.Text()}) {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			_ = sendEvent(scanEvent{err: err})
		}
	}()
	defer close(done)

	keepaliveTicker := time.NewTicker(keepaliveInterval)
	defer keepaliveTicker.Stop()
	lastDataAt := time.Now()

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return finalizeStream()
			}
			if ev.err != nil {
				handleScanErr(ev.err)
				return finalizeStream()
			}
			lastDataAt = time.Now()
			line := ev.line
			if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
				continue
			}
			if processDataLine(line[6:]) {
				return resultWithUsage(), nil
			}

		case <-keepaliveTicker.C:
			if time.Since(lastDataAt) < keepaliveInterval {
				continue
			}
			// Send SSE comment as keepalive
			if _, err := fmt.Fprint(c.Writer, ":\n\n"); err != nil {
				logger.L().Info("openai chat_completions stream: client disconnected during keepalive",
					zap.String("request_id", requestID),
				)
				return resultWithUsage(), nil
			}
			c.Writer.Flush()
		}
	}
}

// writeChatCompletionsError writes an error response in OpenAI Chat Completions format.
func writeChatCompletionsError(c *gin.Context, statusCode int, errType, message string) {
	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"type":    errType,
			"message": message,
		},
	})
}

// forwardChatCompletionsPassthrough forwards the Chat Completions request directly
// to a third-party OpenAI-compatible upstream (e.g., DeepSeek, Ollama) without
// converting to/from the Responses API. The upstream must support /v1/chat/completions.
func (s *OpenAIGatewayService) forwardChatCompletionsPassthrough(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	upstreamModel string,
	originalModel string,
	billingModel string,
	clientStream bool,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	// 1. Build upstream URL: <base_url>/chat/completions
	baseURL := account.GetOpenAIBaseURL()
	validatedURL, err := s.validateUpstreamBaseURL(baseURL)
	if err != nil {
		return nil, fmt.Errorf("validate upstream base URL: %w", err)
	}
	targetURL := buildUpstreamChatCompletionsURL(validatedURL)

	// 2. Rewrite model in request body
	forwardBody := body
	if gjson.GetBytes(body, "model").String() != upstreamModel {
		forwardBody, err = sjson.SetBytes(body, "model", upstreamModel)
		if err != nil {
			return nil, fmt.Errorf("rewrite model in body: %w", err)
		}
	}
	forwardBody, err = normalizeChatCompletionsPassthroughBody(forwardBody)
	if err != nil {
		return nil, fmt.Errorf("normalize chat completions tool messages: %w", err)
	}

	// 2a. Backfill reasoning_content for clients that don't preserve it (e.g. Codex).
	forwardBody, err = backfillReasoningContentInChatBody(forwardBody)
	if err != nil {
		logger.L().Warn("openai chat_completions passthrough: failed to backfill reasoning_content",
			zap.Error(err),
		)
	}

	// 3. Get access token (API key for apikey-type accounts)
	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("get access token: %w", err)
	}

	// 4. Build upstream HTTP request
	upstreamReq, err := http.NewRequestWithContext(ctx, "POST", targetURL, bytes.NewReader(forwardBody))
	if err != nil {
		return nil, fmt.Errorf("create upstream request: %w", err)
	}
	upstreamReq.Header.Set("Content-Type", "application/json")
	upstreamReq.Header.Set("Authorization", "Bearer "+token)
	// Third-party providers (e.g., Kimi Coding) whitelist specific agent User-Agents.
	// The client's original UA may not be accepted (e.g., codex-cli → Kimi rejects).
	// Use the client UA only if it's a known-accepted identity; otherwise default
	// to "claude-code" which is widely accepted by major providers.
	ua := c.GetHeader("User-Agent")
	if !isAcceptedByThirdPartyUpstream(ua) {
		ua = "claude-code/1.0.0"
	}
	upstreamReq.Header.Set("User-Agent", ua)

	// 5. Send request
	proxyURL := ""
	if account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.Do(upstreamReq, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		safeErr := sanitizeUpstreamErrorMessage(err.Error())
		setOpsUpstreamError(c, 0, safeErr, "")
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: 0,
			Kind:               "request_error",
			Message:            safeErr,
		})
		writeChatCompletionsError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed")
		return nil, fmt.Errorf("upstream request failed: %s", safeErr)
	}
	defer func() { _ = resp.Body.Close() }()

	// 6. Handle error response with failover
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(respBody))

		upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(respBody))
		upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)
		if s.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, upstreamMsg, respBody) {
			upstreamDetail := ""
			if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
				maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
				if maxBytes <= 0 {
					maxBytes = 2048
				}
				upstreamDetail = truncateString(string(respBody), maxBytes)
			}
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: resp.StatusCode,
				UpstreamRequestID:  resp.Header.Get("x-request-id"),
				Kind:               "failover",
				Message:            upstreamMsg,
				Detail:             upstreamDetail,
			})
			if s.rateLimitService != nil {
				s.rateLimitService.HandleUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody)
			}
			return nil, &UpstreamFailoverError{
				StatusCode:             resp.StatusCode,
				ResponseBody:           respBody,
				RetryableOnSameAccount: account.IsPoolMode() && (isPoolModeRetryableStatus(resp.StatusCode) || isOpenAITransientProcessingError(resp.StatusCode, upstreamMsg, respBody)),
			}
		}
		return s.handleChatCompletionsErrorResponse(resp, c, account)
	}

	// 7. Handle successful response — passthrough without format conversion
	requestID := resp.Header.Get("x-request-id")

	if clientStream {
		return s.handlePassthroughStreamingResponse(resp, c, originalModel, billingModel, upstreamModel, startTime)
	}

	// Non-streaming: read body and passthrough as-is
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		writeChatCompletionsError(c, http.StatusBadGateway, "api_error", "Failed to read upstream response")
		return nil, fmt.Errorf("read upstream response: %w", err)
	}

	// Cache reasoning_content so unsupported clients (e.g. Codex) get it backfilled
	// on the next turn.
	cacheChatResponseReasoning(respBody)

	// Extract usage from response for billing
	var usage OpenAIUsage
	if parsedUsage, ok := extractOpenAIUsageFromChatCompletionJSONBytes(respBody); ok {
		usage = parsedUsage
	}

	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	for k, vv := range resp.Header {
		if strings.HasPrefix(strings.ToLower(k), "x-") || strings.ToLower(k) == "content-type" {
			continue
		}
		for _, v := range vv {
			c.Writer.Header().Add(k, v)
		}
	}
	c.Data(resp.StatusCode, "application/json", respBody)

	return &OpenAIForwardResult{
		RequestID:     requestID,
		Usage:         usage,
		Model:         originalModel,
		BillingModel:  billingModel,
		UpstreamModel: upstreamModel,
		Stream:        false,
		Duration:      time.Since(startTime),
	}, nil
}

// handlePassthroughStreamingResponse forwards SSE events from the upstream
// Chat Completions endpoint directly to the client without format conversion.
func (s *OpenAIGatewayService) handlePassthroughStreamingResponse(
	resp *http.Response,
	c *gin.Context,
	originalModel string,
	billingModel string,
	upstreamModel string,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")

	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	var usage OpenAIUsage
	var firstTokenMs *int
	firstChunk := true

	scanner := bufio.NewScanner(resp.Body)
	maxLineSize := defaultMaxLineSize
	if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = s.cfg.Gateway.MaxLineSize
	}
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)

	resultWithUsage := func() *OpenAIForwardResult {
		return &OpenAIForwardResult{
			RequestID:     requestID,
			Usage:         usage,
			Model:         originalModel,
			BillingModel:  billingModel,
			UpstreamModel: upstreamModel,
			Stream:        true,
			Duration:      time.Since(startTime),
			FirstTokenMs:  firstTokenMs,
		}
	}

	// Collector for reasoning_content so unsupported clients get it backfilled.
	var streamContent, streamReasoning strings.Builder

	for scanner.Scan() {
		line := scanner.Text()

		if firstChunk && strings.HasPrefix(line, "data: ") && line != "data: [DONE]" {
			firstChunk = false
			ms := int(time.Since(startTime).Milliseconds())
			firstTokenMs = &ms
		}

		// Extract usage from the final chunk
		if strings.HasPrefix(line, "data: ") && line != "data: [DONE]" {
			payload := line[6:]
			if parsedUsage, ok := extractOpenAIUsageFromChatCompletionJSONBytes([]byte(payload)); ok {
				usage = parsedUsage
			}
			// Accumulate reasoning/content deltas for caching.
			var deltaChunk apicompat.ChatCompletionsChunk
			if err := json.Unmarshal([]byte(payload), &deltaChunk); err == nil && len(deltaChunk.Choices) > 0 {
				if deltaChunk.Choices[0].Delta.Content != nil {
					_, _ = streamContent.WriteString(*deltaChunk.Choices[0].Delta.Content)
				}
				if deltaChunk.Choices[0].Delta.ReasoningContent != nil {
					_, _ = streamReasoning.WriteString(*deltaChunk.Choices[0].Delta.ReasoningContent)
				}
			}
		}

		if _, err := fmt.Fprintln(c.Writer, line); err != nil {
			logger.L().Info("openai chat_completions passthrough stream: client disconnected",
				zap.String("request_id", requestID),
			)
			return resultWithUsage(), nil
		}
		// SSE spec: empty line terminates an event
		if line == "" {
			c.Writer.Flush()
		}
	}

	if err := scanner.Err(); err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			logger.L().Warn("openai chat_completions passthrough stream: read error",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
		}
	}

	// Cache collected reasoning content for backfill on the next turn.
	if streamReasoning.Len() > 0 {
		msg := apicompat.ChatMessage{
			Role:             "assistant",
			Content:          rawMessageString(streamContent.String()),
			ReasoningContent: streamReasoning.String(),
		}
		defaultChatReasoningCache.Set(cacheKeyForChatMessage(msg), msg.ReasoningContent)
	}

	// Ensure [DONE] sentinel is sent
	fmt.Fprint(c.Writer, "data: [DONE]\n\n") //nolint:errcheck
	c.Writer.Flush()

	return resultWithUsage(), nil
}

// buildUpstreamChatCompletionsURL builds the chat completions endpoint URL from
// a validated base URL. Handles common patterns:
//   - base ends with /v1          → append /chat/completions
//   - base ends with /chat/completions → return as-is
//   - other                       → append /v1/chat/completions
//
// versionSuffixRe matches a trailing version path segment like /v1, /v4, /v2, etc.
var versionSuffixRe = regexp.MustCompile(`/v\d+$`)

// isAcceptedByThirdPartyUpstream returns true if the UA string is known to be
// accepted by third-party provider whitelists (e.g., Kimi Coding accepts:
// Kimi CLI, Claude Code, Roo Code, Kilo Code).
// Unrecognized agent UAs (e.g., codex-cli) should NOT be forwarded because
// they may be rejected even though they look like agent clients.
func isAcceptedByThirdPartyUpstream(ua string) bool {
	if ua == "" {
		return false
	}
	low := strings.ToLower(ua)
	for _, keyword := range []string{
		"claude-code", "kimi", "roo-code", "roo code",
		"kilo-code", "kilo code",
	} {
		if strings.Contains(low, keyword) {
			return true
		}
	}
	return false
}

func buildUpstreamChatCompletionsURL(base string) string {
	normalized := strings.TrimRight(strings.TrimSpace(base), "/")
	if strings.HasSuffix(normalized, "/chat/completions") {
		return normalized
	}
	if versionSuffixRe.MatchString(normalized) {
		return normalized + "/chat/completions"
	}
	return normalized + "/v1/chat/completions"
}

func convertResponsesToolsToChatTools(tools []apicompat.ResponsesTool) []apicompat.ChatTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]apicompat.ChatTool, 0, len(tools))
	for _, tool := range tools {
		if strings.TrimSpace(tool.Type) != "function" || strings.TrimSpace(tool.Name) == "" {
			continue
		}
		function := &apicompat.ChatFunction{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  tool.Parameters,
			Strict:      tool.Strict,
		}
		out = append(out, apicompat.ChatTool{Type: "function", Function: function})
	}
	return out
}

func convertResponsesToolChoiceToChat(raw json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return raw
	}
	var choice map[string]any
	if err := json.Unmarshal(trimmed, &choice); err != nil {
		return raw
	}
	if strings.TrimSpace(firstNonEmptyString(choice["type"])) != "function" {
		return raw
	}
	if function, ok := choice["function"].(map[string]any); ok && strings.TrimSpace(firstNonEmptyString(function["name"])) != "" {
		return raw
	}
	name := strings.TrimSpace(firstNonEmptyString(choice["name"]))
	if name == "" {
		return raw
	}
	converted, err := json.Marshal(map[string]any{
		"type": "function",
		"function": map[string]any{
			"name": name,
		},
	})
	if err != nil {
		return raw
	}
	return converted
}

func convertChatToolCallsToResponsesOutputs(toolCalls []apicompat.ChatToolCall) []apicompat.ResponsesOutput {
	if len(toolCalls) == 0 {
		return nil
	}
	out := make([]apicompat.ResponsesOutput, 0, len(toolCalls))
	for i, toolCall := range toolCalls {
		name := strings.TrimSpace(toolCall.Function.Name)
		if name == "" {
			continue
		}
		callID := strings.TrimSpace(toolCall.ID)
		if callID == "" {
			callID = fmt.Sprintf("call_%d", i)
		}
		out = append(out, apicompat.ResponsesOutput{
			Type:      "function_call",
			CallID:    callID,
			Name:      name,
			Arguments: normalizeToolCallArguments(toolCall.Function.Arguments),
		})
	}
	return out
}

func normalizeToolCallArguments(arguments string) string {
	if strings.TrimSpace(arguments) == "" {
		return "{}"
	}
	return arguments
}

const openAIResponsesPassthroughToolContextTTL = 30 * time.Minute

type openAIResponsesPassthroughToolContextEntry struct {
	messages  []apicompat.ChatMessage
	expiresAt time.Time
}

type openAIResponsesPassthroughToolContextStore struct {
	mu      sync.Mutex
	entries map[string]openAIResponsesPassthroughToolContextEntry
}

var openAIResponsesPassthroughToolContexts = &openAIResponsesPassthroughToolContextStore{
	entries: make(map[string]openAIResponsesPassthroughToolContextEntry),
}

func storeOpenAIResponsesPassthroughToolContext(responseID string, outputs []apicompat.ResponsesOutput) {
	openAIResponsesPassthroughToolContexts.Store(responseID, outputs)
}

func loadOpenAIResponsesPassthroughToolContext(responseID string) []apicompat.ChatMessage {
	return openAIResponsesPassthroughToolContexts.Load(responseID)
}

func (s *openAIResponsesPassthroughToolContextStore) Store(responseID string, outputs []apicompat.ResponsesOutput) {
	responseID = strings.TrimSpace(responseID)
	if responseID == "" {
		return
	}
	messages := chatMessagesForResponsesFunctionCalls(outputs)
	if len(messages) == 0 {
		return
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = make(map[string]openAIResponsesPassthroughToolContextEntry)
	}
	for id, entry := range s.entries {
		if now.After(entry.expiresAt) {
			delete(s.entries, id)
		}
	}
	s.entries[responseID] = openAIResponsesPassthroughToolContextEntry{
		messages:  cloneChatMessages(messages),
		expiresAt: now.Add(openAIResponsesPassthroughToolContextTTL),
	}
}

func (s *openAIResponsesPassthroughToolContextStore) Load(responseID string) []apicompat.ChatMessage {
	responseID = strings.TrimSpace(responseID)
	if responseID == "" {
		return nil
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[responseID]
	if !ok {
		return nil
	}
	if now.After(entry.expiresAt) {
		delete(s.entries, responseID)
		return nil
	}
	return cloneChatMessages(entry.messages)
}

func chatMessagesForResponsesFunctionCalls(outputs []apicompat.ResponsesOutput) []apicompat.ChatMessage {
	var toolCalls []apicompat.ChatToolCall
	var reasoning strings.Builder
	for i, output := range outputs {
		if output.Type == "reasoning" {
			_, _ = reasoning.WriteString(responsesReasoningText(output))
			continue
		}
		if output.Type != "function_call" || strings.TrimSpace(output.Name) == "" {
			continue
		}
		callID := strings.TrimSpace(output.CallID)
		if callID == "" {
			callID = fmt.Sprintf("call_%d", i)
		}
		toolCalls = append(toolCalls, apicompat.ChatToolCall{
			ID:   callID,
			Type: "function",
			Function: apicompat.ChatFunctionCall{
				Name:      output.Name,
				Arguments: normalizeToolCallArguments(output.Arguments),
			},
		})
	}
	if len(toolCalls) == 0 {
		return nil
	}
	message := apicompat.ChatMessage{Role: "assistant", ToolCalls: toolCalls}
	if reasoningContent := reasoning.String(); reasoningContent != "" {
		message.ReasoningContent = reasoningContent
	}
	return []apicompat.ChatMessage{message}
}

func responsesReasoningText(output apicompat.ResponsesOutput) string {
	if output.EncryptedContent != "" {
		return output.EncryptedContent
	}
	var b strings.Builder
	for _, summary := range output.Summary {
		_, _ = b.WriteString(summary.Text)
	}
	return b.String()
}

func prependResponsesReasoningOutput(outputs []apicompat.ResponsesOutput, id string, reasoningContent string) []apicompat.ResponsesOutput {
	if reasoningContent == "" {
		return outputs
	}
	reasoningOutput := apicompat.ResponsesOutput{
		Type:             "reasoning",
		ID:               id,
		EncryptedContent: reasoningContent,
	}
	return append([]apicompat.ResponsesOutput{reasoningOutput}, outputs...)
}

func cloneChatMessages(messages []apicompat.ChatMessage) []apicompat.ChatMessage {
	if len(messages) == 0 {
		return nil
	}
	out := make([]apicompat.ChatMessage, len(messages))
	copy(out, messages)
	for i := range out {
		if len(messages[i].ToolCalls) > 0 {
			out[i].ToolCalls = append([]apicompat.ChatToolCall(nil), messages[i].ToolCalls...)
		}
		if len(messages[i].Content) > 0 {
			out[i].Content = append(json.RawMessage(nil), messages[i].Content...)
		}
	}
	return out
}

func chatMessagesNeedPassthroughToolContext(messages []apicompat.ChatMessage) bool {
	hasToolOutput := false
	for _, message := range messages {
		if message.Role == "assistant" && len(message.ToolCalls) > 0 {
			return false
		}
		if message.Role == "tool" || message.Role == "function" {
			hasToolOutput = true
		}
	}
	return hasToolOutput
}

type chatToolCallResponseStreamState struct {
	calls              []chatToolCallResponseStreamCall
	indexToPos         map[int]int
	nextOutputIndex    int
	messageOutputIndex int
}

type chatToolCallResponseStreamCall struct {
	OutputIndex         int
	CallID              string
	Name                string
	Arguments           string
	EmittedArgumentsLen int
	Added               bool
}

func newChatToolCallResponseStreamState() *chatToolCallResponseStreamState {
	return &chatToolCallResponseStreamState{
		indexToPos:         make(map[int]int),
		messageOutputIndex: -1,
	}
}

func (s *chatToolCallResponseStreamState) hasToolCalls() bool {
	if s == nil {
		return false
	}
	for i := range s.calls {
		if s.calls[i].Name != "" {
			return true
		}
	}
	return false
}

func (s *chatToolCallResponseStreamState) reserveMessageOutputIndex() int {
	if s == nil {
		return 0
	}
	if s.messageOutputIndex >= 0 {
		return s.messageOutputIndex
	}
	s.messageOutputIndex = s.nextOutputIndex
	s.nextOutputIndex++
	return s.messageOutputIndex
}

func (s *chatToolCallResponseStreamState) processRawToolCallDeltas(rawToolCalls []any) []apicompat.ResponsesStreamEvent {
	if s == nil || len(rawToolCalls) == 0 {
		return nil
	}
	var events []apicompat.ResponsesStreamEvent
	for _, rawToolCall := range rawToolCalls {
		toolCall, ok := rawToolCall.(map[string]any)
		if !ok {
			continue
		}
		position := s.resolveToolCallPosition(toolCall)
		call := &s.calls[position]
		if id := strings.TrimSpace(firstNonEmptyString(toolCall["id"])); id != "" {
			call.CallID = id
		}
		if typ := strings.TrimSpace(firstNonEmptyString(toolCall["type"])); typ != "" && typ != "function" {
			continue
		}
		function, _ := toolCall["function"].(map[string]any)
		if name := strings.TrimSpace(firstNonEmptyString(function["name"])); name != "" {
			call.Name = name
		}
		if delta := firstNonEmptyString(function["arguments"]); delta != "" {
			call.Arguments += delta
		}
		if call.CallID == "" {
			call.CallID = fmt.Sprintf("call_%d", call.OutputIndex)
		}
		if !call.Added && call.Name != "" {
			call.Added = true
			events = append(events, apicompat.ResponsesStreamEvent{
				Type:        "response.output_item.added",
				OutputIndex: call.OutputIndex,
				Item: &apicompat.ResponsesOutput{
					Type:   "function_call",
					CallID: call.CallID,
					Name:   call.Name,
				},
			})
		}
		if call.Added && call.EmittedArgumentsLen < len(call.Arguments) {
			delta := call.Arguments[call.EmittedArgumentsLen:]
			call.EmittedArgumentsLen = len(call.Arguments)
			events = append(events, apicompat.ResponsesStreamEvent{
				Type:        "response.function_call_arguments.delta",
				OutputIndex: call.OutputIndex,
				ItemID:      call.CallID,
				CallID:      call.CallID,
				Name:        call.Name,
				Delta:       delta,
			})
		}
	}
	return events
}

func (s *chatToolCallResponseStreamState) resolveToolCallPosition(toolCall map[string]any) int {
	idx, ok := numericToolCallIndex(toolCall["index"])
	if ok {
		if pos, exists := s.indexToPos[idx]; exists {
			return pos
		}
		pos := len(s.calls)
		s.indexToPos[idx] = pos
		s.calls = append(s.calls, chatToolCallResponseStreamCall{OutputIndex: s.allocateOutputIndex()})
		return pos
	}
	pos := len(s.calls) - 1
	if pos >= 0 {
		last := &s.calls[pos]
		if last.Name == "" || !last.Added {
			return pos
		}
	}
	pos = len(s.calls)
	s.calls = append(s.calls, chatToolCallResponseStreamCall{OutputIndex: s.allocateOutputIndex()})
	return pos
}

func (s *chatToolCallResponseStreamState) allocateOutputIndex() int {
	idx := s.nextOutputIndex
	s.nextOutputIndex++
	return idx
}

func numericToolCallIndex(value any) (int, bool) {
	switch v := value.(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	case json.Number:
		i, err := v.Int64()
		return int(i), err == nil
	default:
		return 0, false
	}
}

func (s *chatToolCallResponseStreamState) doneEvents() []apicompat.ResponsesStreamEvent {
	if s == nil || len(s.calls) == 0 {
		return nil
	}
	events := make([]apicompat.ResponsesStreamEvent, 0, len(s.calls)*2)
	for i := range s.calls {
		call := &s.calls[i]
		if call.Name == "" {
			continue
		}
		if call.CallID == "" {
			call.CallID = fmt.Sprintf("call_%d", call.OutputIndex)
		}
		arguments := normalizeToolCallArguments(call.Arguments)
		events = append(events,
			apicompat.ResponsesStreamEvent{
				Type:        "response.function_call_arguments.done",
				OutputIndex: call.OutputIndex,
				ItemID:      call.CallID,
				CallID:      call.CallID,
				Name:        call.Name,
				Arguments:   arguments,
			},
			apicompat.ResponsesStreamEvent{
				Type:        "response.output_item.done",
				OutputIndex: call.OutputIndex,
				Item: &apicompat.ResponsesOutput{
					Type:      "function_call",
					CallID:    call.CallID,
					Name:      call.Name,
					Arguments: arguments,
				},
			},
		)
	}
	return events
}

func (s *chatToolCallResponseStreamState) outputsWithMessage(includeMessage bool, message apicompat.ResponsesOutput) []apicompat.ResponsesOutput {
	if s == nil {
		if includeMessage {
			return []apicompat.ResponsesOutput{message}
		}
		return nil
	}
	byIndex := make([]*apicompat.ResponsesOutput, s.nextOutputIndex)
	if includeMessage {
		idx := s.messageOutputIndex
		if idx < 0 {
			idx = s.reserveMessageOutputIndex()
			if idx >= len(byIndex) {
				byIndex = append(byIndex, nil)
			}
		}
		msg := message
		byIndex[idx] = &msg
	}
	for i := range s.calls {
		call := &s.calls[i]
		if call.Name == "" {
			continue
		}
		if call.CallID == "" {
			call.CallID = fmt.Sprintf("call_%d", call.OutputIndex)
		}
		out := apicompat.ResponsesOutput{
			Type:      "function_call",
			CallID:    call.CallID,
			Name:      call.Name,
			Arguments: normalizeToolCallArguments(call.Arguments),
		}
		for call.OutputIndex >= len(byIndex) {
			byIndex = append(byIndex, nil)
		}
		byIndex[call.OutputIndex] = &out
	}
	out := make([]apicompat.ResponsesOutput, 0, len(byIndex))
	for _, item := range byIndex {
		if item != nil {
			out = append(out, *item)
		}
	}
	return out
}

// ForwardResponsesPassthrough converts a Responses API request to Chat Completions
// format, forwards it to a third-party OpenAI-compatible upstream (e.g., Kimi),
// then converts the Chat Completions response back to Responses API format.
// This is used when Codex (which uses the Responses API) targets a non-OpenAI provider.
func (s *OpenAIGatewayService) ForwardResponsesPassthrough(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	defaultMappedModel string,
) (*OpenAIForwardResult, error) {
	startTime := time.Now()

	// ── 1. Parse Responses request & convert to Chat Completions ──
	var respReq apicompat.ResponsesRequest
	if err := json.Unmarshal(body, &respReq); err != nil {
		return nil, fmt.Errorf("parse responses request: %w", err)
	}
	originalModel := respReq.Model

	messages, err := convertResponsesInputToMessages(respReq)
	if err != nil {
		return nil, fmt.Errorf("convert responses to chat completions: %w", err)
	}
	previousResponseID := strings.TrimSpace(gjson.GetBytes(body, "previous_response_id").String())
	if previousResponseID != "" && chatMessagesNeedPassthroughToolContext(messages) {
		if contextMessages := loadOpenAIResponsesPassthroughToolContext(previousResponseID); len(contextMessages) > 0 {
			messages = append(contextMessages, messages...)
		}
	}
	messages = normalizeChatToolCallSequences(messages)

	chatReq := apicompat.ChatCompletionsRequest{
		Model:    respReq.Model,
		Messages: messages,
		Stream:   respReq.Stream,
	}
	if len(respReq.Tools) > 0 {
		chatReq.Tools = convertResponsesToolsToChatTools(respReq.Tools)
	}
	if len(respReq.ToolChoice) > 0 {
		chatReq.ToolChoice = convertResponsesToolChoiceToChat(respReq.ToolChoice)
	}
	if respReq.MaxOutputTokens != nil {
		chatReq.MaxCompletionTokens = respReq.MaxOutputTokens
	}
	if respReq.Temperature != nil {
		chatReq.Temperature = respReq.Temperature
	}
	if respReq.TopP != nil {
		chatReq.TopP = respReq.TopP
	}
	if respReq.Stream {
		chatReq.StreamOptions = &apicompat.ChatStreamOptions{IncludeUsage: true}
	}

	chatBody, err := json.Marshal(chatReq)
	if err != nil {
		return nil, fmt.Errorf("marshal chat completions request: %w", err)
	}

	// ── 2. Resolve model mapping ──
	billingModel := resolveOpenAIForwardModel(account, originalModel, defaultMappedModel)
	upstreamModel := normalizeOpenAIModelForUpstream(account, billingModel)

	// Rewrite model in body
	forwardBody := chatBody
	if gjson.GetBytes(chatBody, "model").String() != upstreamModel {
		forwardBody, err = sjson.SetBytes(chatBody, "model", upstreamModel)
		if err != nil {
			return nil, fmt.Errorf("rewrite model in body: %w", err)
		}
	}

	// ── 3. Build upstream request ──
	baseURL := account.GetOpenAIBaseURL()
	validatedURL, err := s.validateUpstreamBaseURL(baseURL)
	if err != nil {
		return nil, fmt.Errorf("validate upstream base URL: %w", err)
	}
	targetURL := buildUpstreamChatCompletionsURL(validatedURL)

	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("get access token: %w", err)
	}

	upstreamReq, err := http.NewRequestWithContext(ctx, "POST", targetURL, bytes.NewReader(forwardBody))
	if err != nil {
		return nil, fmt.Errorf("create upstream request: %w", err)
	}
	upstreamReq.Header.Set("Content-Type", "application/json")
	upstreamReq.Header.Set("Authorization", "Bearer "+token)
	ua := c.GetHeader("User-Agent")
	if !isAcceptedByThirdPartyUpstream(ua) {
		ua = "claude-code/1.0.0"
	}
	upstreamReq.Header.Set("User-Agent", ua)

	// ── 4. Send request ──
	proxyURL := ""
	if account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.Do(upstreamReq, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		safeErr := sanitizeUpstreamErrorMessage(err.Error())
		writeResponsesError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed")
		return nil, fmt.Errorf("upstream request failed: %s", safeErr)
	}
	defer func() { _ = resp.Body.Close() }()

	// ── 5. Handle upstream error ──
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(respBody))

		upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(respBody))
		upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)

		// Client errors (4xx except 429) are request-specific and should NOT
		// trigger failover or mark the account as temporarily unschedulable.
		// They indicate the request content is invalid, not that the account is unhealthy.
		if resp.StatusCode < 500 && resp.StatusCode != 429 {
			writeResponsesError(c, resp.StatusCode, "upstream_error", upstreamMsg)
			return nil, fmt.Errorf("upstream returned %d: %s", resp.StatusCode, upstreamMsg)
		}

		if s.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, upstreamMsg, respBody) {
			if s.rateLimitService != nil {
				s.rateLimitService.HandleUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody)
			}
			return nil, &UpstreamFailoverError{
				StatusCode:   resp.StatusCode,
				ResponseBody: respBody,
			}
		}
		writeResponsesError(c, resp.StatusCode, "upstream_error", upstreamMsg)
		return nil, fmt.Errorf("upstream returned %d: %s", resp.StatusCode, upstreamMsg)
	}

	// ── 6. Convert response ──
	if respReq.Stream {
		return s.handleResponsesPassthroughStream(resp, c, originalModel, billingModel, upstreamModel, startTime)
	}
	return s.handleResponsesPassthroughNonStream(resp, c, originalModel, billingModel, upstreamModel, startTime)
}

// handleResponsesPassthroughNonStream converts a non-streaming Chat Completions response
// to Responses API format and writes it to the client.
func (s *OpenAIGatewayService) handleResponsesPassthroughNonStream(
	resp *http.Response,
	c *gin.Context,
	originalModel string,
	billingModel string,
	upstreamModel string,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		writeResponsesError(c, http.StatusBadGateway, "api_error", "Failed to read upstream response")
		return nil, fmt.Errorf("read upstream response: %w", err)
	}

	// Parse Chat Completions response
	var chatResp struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Role             string                      `json:"role"`
				Content          string                      `json:"content"`
				ReasoningContent string                      `json:"reasoning_content"`
				ToolCalls        []apicompat.ChatToolCall    `json:"tool_calls"`
				FunctionCall     *apicompat.ChatFunctionCall `json:"function_call"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage apicompat.ChatUsage `json:"usage"`
	}
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		// If we can't parse, pass through as-is
		c.Data(resp.StatusCode, "application/json", respBody)
		return nil, nil
	}
	if len(chatResp.Choices) == 0 {
		c.Data(resp.StatusCode, "application/json", respBody)
		return nil, nil
	}

	// Build Responses API response
	choice := chatResp.Choices[0]
	content := choice.Message.Content
	status := "completed"
	if choice.FinishReason == "length" {
		status = "incomplete"
	}

	var outputs []apicompat.ResponsesOutput
	if content != "" || (len(choice.Message.ToolCalls) == 0 && choice.Message.FunctionCall == nil) {
		outputs = append(outputs, apicompat.ResponsesOutput{
			Type:   "message",
			ID:     "msg-" + chatResp.ID,
			Role:   "assistant",
			Status: "completed",
			Content: []apicompat.ResponsesContentPart{
				{Type: "output_text", Text: content},
			},
		})
	}
	outputs = append(outputs, convertChatToolCallsToResponsesOutputs(choice.Message.ToolCalls)...)
	if choice.Message.FunctionCall != nil {
		outputs = append(outputs, apicompat.ResponsesOutput{
			Type:      "function_call",
			CallID:    "call_" + chatResp.ID,
			Name:      choice.Message.FunctionCall.Name,
			Arguments: normalizeToolCallArguments(choice.Message.FunctionCall.Arguments),
		})
	}

	outputs = prependResponsesReasoningOutput(outputs, "rs-"+chatResp.ID, choice.Message.ReasoningContent)

	responsesResp := apicompat.ResponsesResponse{
		ID:     "resp-" + chatResp.ID,
		Object: "response",
		Model:  originalModel,
		Status: status,
		Output: outputs,
		Usage:  responsesUsageFromChatUsage(&chatResp.Usage),
	}
	storeOpenAIResponsesPassthroughToolContext(responsesResp.ID, outputs)

	respJSON, err := json.Marshal(responsesResp)
	if err != nil {
		c.Data(resp.StatusCode, "application/json", respBody)
		return nil, nil
	}

	c.Data(http.StatusOK, "application/json", respJSON)

	return &OpenAIForwardResult{
		Usage:         openAIUsageFromChatUsage(&chatResp.Usage),
		Model:         originalModel,
		BillingModel:  billingModel,
		UpstreamModel: upstreamModel,
		Stream:        false,
		Duration:      time.Since(startTime),
	}, nil
}

// handleResponsesPassthroughStream converts a streaming Chat Completions response
// to Responses API SSE format and writes it to the client.
func (s *OpenAIGatewayService) handleResponsesPassthroughStream(
	resp *http.Response,
	c *gin.Context,
	originalModel string,
	billingModel string,
	upstreamModel string,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	respID := "resp-" + fmt.Sprintf("%d", time.Now().UnixNano())
	msgID := "msg-" + fmt.Sprintf("%d", time.Now().UnixNano())
	seqNum := 0

	// Helper to write SSE event
	writeSSE := func(event string, data any) {
		seqNum++
		payload, _ := json.Marshal(data)
		fmt.Fprintf(c.Writer, "event: %s\n", event)
		fmt.Fprintf(c.Writer, "data: %s\n\n", payload)
		c.Writer.Flush()
	}
	toolCallState := newChatToolCallResponseStreamState()
	messageStarted := false
	messageOutputIndex := -1
	ensureMessageStarted := func() int {
		if messageStarted {
			return messageOutputIndex
		}
		messageStarted = true
		messageOutputIndex = toolCallState.reserveMessageOutputIndex()
		writeSSE("response.output_item.added", apicompat.ResponsesStreamEvent{
			Type: "response.output_item.added",
			Item: &apicompat.ResponsesOutput{
				Type:    "message",
				ID:      msgID,
				Role:    "assistant",
				Status:  "in_progress",
				Content: []apicompat.ResponsesContentPart{},
			},
			OutputIndex:    messageOutputIndex,
			SequenceNumber: seqNum,
		})
		writeSSE("response.content_part.added", apicompat.ResponsesStreamEvent{
			Type:           "response.content_part.added",
			ItemID:         msgID,
			OutputIndex:    messageOutputIndex,
			ContentIndex:   0,
			SequenceNumber: seqNum,
		})
		return messageOutputIndex
	}

	// Send response.created
	writeSSE("response.created", apicompat.ResponsesStreamEvent{
		Type: "response.created",
		Response: &apicompat.ResponsesResponse{
			ID:     respID,
			Object: "response",
			Model:  originalModel,
			Status: "in_progress",
			Output: []apicompat.ResponsesOutput{},
		},
		SequenceNumber: seqNum,
	})

	// Read Chat Completions SSE and convert to Responses API SSE
	scanner := bufio.NewScanner(resp.Body)
	maxLineSize := defaultMaxLineSize
	if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = s.cfg.Gateway.MaxLineSize
	}
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)

	var usage OpenAIUsage
	var fullContent strings.Builder
	var fullReasoning strings.Builder
	firstTokenMs := int(time.Since(startTime).Milliseconds())

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") || line == "data: [DONE]" || line == "data:[DONE]" {
			continue
		}

		// Handle both "data: {...}" (standard) and "data:{...}" (Kimi)
		payload := strings.TrimPrefix(line, "data:")
		payload = strings.TrimPrefix(payload, " ")
		var chunk map[string]any
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}

		// Extract usage from the final chunk
		if parsedUsage, ok := extractOpenAIUsageFromChatCompletionJSONBytes([]byte(payload)); ok {
			usage = parsedUsage
		}

		choices, _ := chunk["choices"].([]any)
		if len(choices) == 0 {
			continue
		}
		choice, _ := choices[0].(map[string]any)
		if choice == nil {
			continue
		}
		delta, _ := choice["delta"].(map[string]any)
		if delta == nil {
			continue
		}

		// Handle reasoning content delta
		if rc, ok := delta["reasoning_content"].(string); ok && rc != "" {
			_, _ = fullReasoning.WriteString(rc)
			// Responses API doesn't have a standard streaming event for reasoning,
			// but some clients expect it. We skip reasoning deltas for now.
		}

		// Handle content delta
		if content, ok := delta["content"].(string); ok && content != "" {
			idx := ensureMessageStarted()
			_, _ = fullContent.WriteString(content)
			writeSSE("response.output_text.delta", apicompat.ResponsesStreamEvent{
				Type:           "response.output_text.delta",
				ItemID:         msgID,
				OutputIndex:    idx,
				ContentIndex:   0,
				Delta:          content,
				SequenceNumber: seqNum,
			})
		}

		if rawToolCalls, ok := delta["tool_calls"].([]any); ok && len(rawToolCalls) > 0 {
			for _, event := range toolCallState.processRawToolCallDeltas(rawToolCalls) {
				writeSSE(event.Type, event)
			}
		}

		// Handle finish
		if fr, ok := choice["finish_reason"].(string); ok && fr != "" && fr != "null" {
			// No-op: we handle completion after the loop
			_ = fr
		}
	}

	finalText := fullContent.String()
	if finalText != "" || !toolCallState.hasToolCalls() {
		idx := ensureMessageStarted()
		writeSSE("response.output_text.done", apicompat.ResponsesStreamEvent{
			Type:           "response.output_text.done",
			ItemID:         msgID,
			OutputIndex:    idx,
			ContentIndex:   0,
			Text:           finalText,
			SequenceNumber: seqNum,
		})

		// Send response.content_part.done
		writeSSE("response.content_part.done", apicompat.ResponsesStreamEvent{
			Type:           "response.content_part.done",
			ItemID:         msgID,
			OutputIndex:    idx,
			ContentIndex:   0,
			SequenceNumber: seqNum,
		})

		// Send response.output_item.done
		writeSSE("response.output_item.done", apicompat.ResponsesStreamEvent{
			Type: "response.output_item.done",
			Item: &apicompat.ResponsesOutput{
				Type:   "message",
				ID:     msgID,
				Role:   "assistant",
				Status: "completed",
				Content: []apicompat.ResponsesContentPart{
					{Type: "output_text", Text: finalText},
				},
			},
			OutputIndex:    idx,
			SequenceNumber: seqNum,
		})
	}

	for _, event := range toolCallState.doneEvents() {
		writeSSE(event.Type, event)
	}

	// Send response.completed
	responsesUsage := &apicompat.ResponsesUsage{
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
		TotalTokens:  usage.InputTokens + usage.OutputTokens,
	}
	if usage.CacheReadInputTokens > 0 {
		responsesUsage.InputTokensDetails = &apicompat.ResponsesInputTokensDetails{
			CachedTokens: usage.CacheReadInputTokens,
		}
	}
	if usage.CacheCreationInputTokens > 0 {
		responsesUsage.CacheCreationInputTokens = usage.CacheCreationInputTokens
	}
	includeMessage := finalText != "" || !toolCallState.hasToolCalls()
	messageOutput := apicompat.ResponsesOutput{
		Type:   "message",
		ID:     msgID,
		Role:   "assistant",
		Status: "completed",
		Content: []apicompat.ResponsesContentPart{
			{Type: "output_text", Text: finalText},
		},
	}
	outputs := toolCallState.outputsWithMessage(includeMessage, messageOutput)
	outputs = prependResponsesReasoningOutput(outputs, "rs-"+respID, fullReasoning.String())
	storeOpenAIResponsesPassthroughToolContext(respID, outputs)

	writeSSE("response.completed", apicompat.ResponsesStreamEvent{
		Type: "response.completed",
		Response: &apicompat.ResponsesResponse{
			ID:     respID,
			Object: "response",
			Model:  originalModel,
			Status: "completed",
			Output: outputs,
			Usage:  responsesUsage,
		},
		SequenceNumber: seqNum,
	})

	return &OpenAIForwardResult{
		Usage:         usage,
		Model:         originalModel,
		BillingModel:  billingModel,
		UpstreamModel: upstreamModel,
		Stream:        true,
		Duration:      time.Since(startTime),
		FirstTokenMs:  &firstTokenMs,
	}, nil
}

// rawMessageString creates a json.RawMessage from a Go string.
func rawMessageString(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return json.RawMessage(b)
}

func appendResponsesFunctionCallMessage(messages []apicompat.ChatMessage, item apicompat.ResponsesInputItem, reasoningContent string) []apicompat.ChatMessage {
	toolCall := apicompat.ChatToolCall{
		ID:   strings.TrimSpace(item.CallID),
		Type: "function",
		Function: apicompat.ChatFunctionCall{
			Name:      strings.TrimSpace(item.Name),
			Arguments: normalizeToolCallArguments(item.Arguments),
		},
	}

	last := len(messages) - 1
	if last >= 0 && messages[last].Role == "assistant" {
		appendChatMessageReasoningContent(&messages[last], reasoningContent)
		messages[last].ToolCalls = append(messages[last].ToolCalls, toolCall)
		return messages
	}

	message := apicompat.ChatMessage{
		Role:      "assistant",
		ToolCalls: []apicompat.ChatToolCall{toolCall},
	}
	appendChatMessageReasoningContent(&message, reasoningContent)
	return append(messages, message)
}

func appendChatMessageReasoningContent(message *apicompat.ChatMessage, reasoningContent string) {
	if strings.TrimSpace(reasoningContent) == "" {
		return
	}
	message.ReasoningContent += reasoningContent
}

func appendReasoningToLastAssistantToolMessage(messages []apicompat.ChatMessage, reasoningContent string) bool {
	if strings.TrimSpace(reasoningContent) == "" {
		return false
	}
	last := len(messages) - 1
	if last < 0 || messages[last].Role != "assistant" || len(messages[last].ToolCalls) == 0 {
		return false
	}
	appendChatMessageReasoningContent(&messages[last], reasoningContent)
	return true
}

func normalizeChatCompletionsPassthroughBody(body []byte) ([]byte, error) {
	messagesResult := gjson.GetBytes(body, "messages")
	if !messagesResult.Exists() || !messagesResult.IsArray() {
		return body, nil
	}
	var messages []apicompat.ChatMessage
	if err := json.Unmarshal([]byte(messagesResult.Raw), &messages); err != nil {
		return nil, err
	}
	normalized, changed := normalizeChatToolCallSequencesWithChanged(messages)
	if !changed {
		return body, nil
	}
	messagesJSON, err := json.Marshal(normalized)
	if err != nil {
		return nil, err
	}
	return sjson.SetRawBytes(body, "messages", messagesJSON)
}

// backfillReasoningContentInChatBody parses the messages array, injects cached
// reasoning_content into assistant messages that are missing it, and rewrites
// the body. No-op when nothing changes.
func backfillReasoningContentInChatBody(body []byte) ([]byte, error) {
	messagesResult := gjson.GetBytes(body, "messages")
	if !messagesResult.Exists() || !messagesResult.IsArray() {
		return body, nil
	}
	var messages []apicompat.ChatMessage
	if err := json.Unmarshal([]byte(messagesResult.Raw), &messages); err != nil {
		return nil, err
	}
	backfillChatMessagesReasoning(messages)
	messagesJSON, err := json.Marshal(messages)
	if err != nil {
		return nil, err
	}
	return sjson.SetRawBytes(body, "messages", messagesJSON)
}

func normalizeChatToolCallSequences(messages []apicompat.ChatMessage) []apicompat.ChatMessage {
	normalized, _ := normalizeChatToolCallSequencesWithChanged(messages)
	return normalized
}

func normalizeChatToolCallSequencesWithChanged(messages []apicompat.ChatMessage) ([]apicompat.ChatMessage, bool) {
	if len(messages) == 0 {
		return messages, false
	}

	changed := false
	out := make([]apicompat.ChatMessage, 0, len(messages))
	for i := 0; i < len(messages); i++ {
		message := messages[i]
		if message.Role == "assistant" && len(message.ToolCalls) > 0 {
			next := i + 1
			var toolMessages []apicompat.ChatMessage
			for next < len(messages) && messages[next].Role == "tool" {
				toolMessages = append(toolMessages, messages[next])
				next++
			}

			firstToolMessageIndexByID := make(map[string]int)
			for idx, toolMessage := range toolMessages {
				toolCallID := strings.TrimSpace(toolMessage.ToolCallID)
				if toolCallID == "" {
					changed = true
					continue
				}
				if _, exists := firstToolMessageIndexByID[toolCallID]; exists {
					changed = true
					continue
				}
				firstToolMessageIndexByID[toolCallID] = idx
			}

			filteredToolCalls := make([]apicompat.ChatToolCall, 0, len(message.ToolCalls))
			keptToolCallIDs := make([]string, 0, len(message.ToolCalls))
			seenToolCallIDs := make(map[string]struct{})
			for _, toolCall := range message.ToolCalls {
				toolCallID := strings.TrimSpace(toolCall.ID)
				if toolCallID == "" {
					changed = true
					continue
				}
				if _, duplicate := seenToolCallIDs[toolCallID]; duplicate {
					changed = true
					continue
				}
				seenToolCallIDs[toolCallID] = struct{}{}
				if _, ok := firstToolMessageIndexByID[toolCallID]; !ok {
					changed = true
					continue
				}
				filteredToolCalls = append(filteredToolCalls, toolCall)
				keptToolCallIDs = append(keptToolCallIDs, toolCallID)
			}

			message.ToolCalls = filteredToolCalls
			if len(message.ToolCalls) > 0 || chatMessageHasRenderablePayload(message) {
				out = append(out, message)
			} else {
				changed = true
			}

			emittedToolMessageIndices := make(map[int]struct{}, len(keptToolCallIDs))
			for expectedIdx, toolCallID := range keptToolCallIDs {
				toolMessageIdx := firstToolMessageIndexByID[toolCallID]
				if toolMessageIdx != expectedIdx {
					changed = true
				}
				out = append(out, toolMessages[toolMessageIdx])
				emittedToolMessageIndices[toolMessageIdx] = struct{}{}
			}

			for idx, toolMessage := range toolMessages {
				if _, emitted := emittedToolMessageIndices[idx]; emitted {
					continue
				}
				changed = true
				out = append(out, downgradeOrphanToolMessage(toolMessage))
			}
			i = next - 1
			continue
		}

		if message.Role == "tool" {
			changed = true
			out = append(out, downgradeOrphanToolMessage(message))
			continue
		}
		out = append(out, message)
	}
	return out, changed
}

func chatMessageHasRenderablePayload(message apicompat.ChatMessage) bool {
	content := bytes.TrimSpace(message.Content)
	if len(content) > 0 && !bytes.Equal(content, []byte("null")) && !bytes.Equal(content, []byte(`""`)) {
		return true
	}
	return strings.TrimSpace(message.ReasoningContent) != "" || message.FunctionCall != nil
}

func downgradeOrphanToolMessage(message apicompat.ChatMessage) apicompat.ChatMessage {
	content := chatMessageContentAsText(message.Content)
	if strings.TrimSpace(content) == "" {
		content = "(empty)"
	}
	if toolCallID := strings.TrimSpace(message.ToolCallID); toolCallID != "" {
		content = "Tool result for " + toolCallID + ":\n" + content
	} else {
		content = "Tool result:\n" + content
	}
	return apicompat.ChatMessage{Role: "user", Content: rawMessageString(content)}
}

func chatMessageContentAsText(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}
	var s string
	if err := json.Unmarshal(trimmed, &s); err == nil {
		return s
	}
	return string(trimmed)
}

// convertResponsesInputToMessages converts a Responses API request's input field
// into Chat Completions messages format.
func convertResponsesInputToMessages(respReq apicompat.ResponsesRequest) ([]apicompat.ChatMessage, error) {
	var messages []apicompat.ChatMessage

	// Instructions become system message
	if respReq.Instructions != "" {
		messages = append(messages, apicompat.ChatMessage{
			Role:    "system",
			Content: rawMessageString(respReq.Instructions),
		})
	}

	// Parse input — can be a string or an array of items
	if len(respReq.Input) == 0 {
		return messages, nil
	}

	// Try as string first
	var inputStr string
	if err := json.Unmarshal(respReq.Input, &inputStr); err == nil {
		messages = append(messages, apicompat.ChatMessage{
			Role:    "user",
			Content: rawMessageString(inputStr),
		})
		return messages, nil
	}

	// Try as array of input items
	var items []apicompat.ResponsesInputItem
	if err := json.Unmarshal(respReq.Input, &items); err != nil {
		return nil, fmt.Errorf("input must be a string or array: %w", err)
	}

	var pendingReasoning strings.Builder
	for _, item := range items {
		switch {
		case item.Type == "reasoning":
			reasoningContent := responsesInputReasoningText(item)
			if !appendReasoningToLastAssistantToolMessage(messages, reasoningContent) {
				_, _ = pendingReasoning.WriteString(reasoningContent)
			}
		case item.Type == "function_call":
			reasoningContent := pendingReasoning.String()
			pendingReasoning.Reset()
			messages = appendResponsesFunctionCallMessage(messages, item, reasoningContent)
		case item.Type == "function_call_output":
			messages = append(messages, apicompat.ChatMessage{
				Role:       "tool",
				ToolCallID: item.CallID,
				Content:    rawMessageString(item.Output),
			})
		case item.Role == "user" || item.Role == "assistant" || item.Role == "system" || item.Role == "developer":
			// Map "developer" to "system" — third-party providers (Kimi, DeepSeek, etc.)
			// do not support the "developer" role and return errors like
			// "Invalid request: tokenization failed".
			role := item.Role
			if role == "developer" {
				role = "system"
			}
			msg := apicompat.ChatMessage{Role: role}
			// Content can be a string or array of parts
			var contentStr string
			if err := json.Unmarshal(item.Content, &contentStr); err == nil {
				msg.Content = rawMessageString(contentStr)
			} else {
				// Complex content — try extracting text from parts
				var parts []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				}
				if err := json.Unmarshal(item.Content, &parts); err == nil {
					var texts []string
					for _, p := range parts {
						if p.Type == "input_text" || p.Type == "text" || p.Type == "output_text" {
							texts = append(texts, p.Text)
						}
					}
					if len(texts) > 0 {
						msg.Content = rawMessageString(strings.Join(texts, "\n"))
					}
				} else {
					msg.Content = item.Content
				}
			}
			// Attach pending reasoning to assistant messages so it isn't lost
			// when a reasoning item appears before a role-based assistant message.
			if role == "assistant" && pendingReasoning.Len() > 0 {
				msg.ReasoningContent = pendingReasoning.String()
				pendingReasoning.Reset()
			}
			messages = append(messages, msg)
		default:
			// Unknown type — try as a user message with content
			if item.Content != nil {
				var contentStr string
				if err := json.Unmarshal(item.Content, &contentStr); err == nil && contentStr != "" {
					messages = append(messages, apicompat.ChatMessage{
						Role:    "user",
						Content: rawMessageString(contentStr),
					})
				}
			}
		}
	}

	return messages, nil
}

func responsesInputReasoningText(item apicompat.ResponsesInputItem) string {
	if item.EncryptedContent != "" {
		return item.EncryptedContent
	}
	var b strings.Builder
	for _, summary := range item.Summary {
		_, _ = b.WriteString(summary.Text)
	}
	return b.String()
}
