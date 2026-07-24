package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// RealtimeVoiceToken returns ChatGPT's short-lived OAuth voice bootstrap
// credentials. The browser/mobile client then establishes the media transport
// directly, so this gateway never buffers or relays microphone audio.
func (h *OpenAIGatewayHandler) RealtimeVoiceToken(c *gin.Context) {
	streamStarted := false
	defer h.recoverResponsesPanic(c, &streamStarted)

	requestStart := time.Now()
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}

	model := service.OpenAIRealtimeVoiceBillingModel
	reqLog := requestLogger(
		c,
		"handler.openai_gateway.realtime_voice_token",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
		zap.String("model", model),
	)
	if !h.ensureResponsesDependencies(c, reqLog) {
		return
	}

	userReleaseFunc, acquired := h.acquireResponsesUserSlot(c, subject.UserID, subject.Concurrency, false, &streamStarted, reqLog)
	if !acquired {
		return
	}
	if userReleaseFunc != nil {
		defer userReleaseFunc()
	}

	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), apiKey.User, apiKey, apiKey.Group, subscription); err != nil {
		status, code, message, retryAfter := billingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.errorResponse(c, status, code, message)
		return
	}
	if err := h.gatewayService.ValidateRealtimeVoiceBilling(c.Request.Context(), apiKey); err != nil {
		reqLog.Error("openai.realtime_voice_token.billing_configuration_invalid", zap.Error(err))
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", err.Error())
		return
	}

	setOpsRequestContext(c, model, false, nil)
	setOpsEndpointContext(c, "", int16(service.RequestTypeSync))
	service.SetOpsLatencyMs(c, service.OpsAuthLatencyMsKey, time.Since(requestStart).Milliseconds())
	routingStart := time.Now()
	sessionSeed := strings.Join([]string{
		"openai-realtime-voice-token",
		strconv.FormatInt(subject.UserID, 10),
		strconv.FormatInt(apiKey.ID, 10),
	}, "|")
	sessionHash := h.gatewayService.GenerateSessionHashWithFallback(c, nil, sessionSeed)
	leaseID := realtimeVoiceTokenLeaseID(c, apiKey.ID)

	failedAccountIDs := make(map[int64]struct{})
	switchCount := 0
	var lastFailoverErr *service.UpstreamFailoverError
	var pendingAccountRelease func()
	defer func() {
		if pendingAccountRelease != nil {
			pendingAccountRelease()
		}
	}()
	for {
		selection, scheduleDecision, err := h.gatewayService.SelectAccountWithSchedulerForRealtimeVoice(
			c.Request.Context(),
			apiKey.GroupID,
			sessionHash,
			leaseID,
			failedAccountIDs,
		)
		if err != nil || selection == nil || selection.Account == nil {
			reqLog.Warn("openai.realtime_voice_token.account_select_failed",
				zap.Error(err),
				zap.Int("excluded_account_count", len(failedAccountIDs)),
			)
			if lastFailoverErr != nil {
				h.handleFailoverExhausted(c, lastFailoverErr, false)
			} else {
				h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "No available OpenAI OAuth voice account")
			}
			return
		}

		account := selection.Account
		setOpsSelectedAccount(c, account.ID, account.Platform)
		accountReleaseFunc, accountAcquired := h.acquireRealtimeVoiceAccountSlot(c, apiKey.GroupID, sessionHash, leaseID, selection, &streamStarted, reqLog)
		if !accountAcquired {
			return
		}
		pendingAccountRelease = accountReleaseFunc
		service.SetOpsLatencyMs(c, service.OpsRoutingLatencyMsKey, time.Since(routingStart).Milliseconds())

		forwardStart := time.Now()
		result, forwardErr := h.gatewayService.ForwardOAuthRealtimeVoiceToken(c.Request.Context(), c, account)
		service.SetOpsLatencyMs(c, service.OpsResponseLatencyMsKey, time.Since(forwardStart).Milliseconds())

		if forwardErr != nil {
			if pendingAccountRelease != nil {
				pendingAccountRelease()
				pendingAccountRelease = nil
			}
			var failoverErr *service.UpstreamFailoverError
			if errors.As(forwardErr, &failoverErr) {
				h.gatewayService.ReportOpenAIAccountScheduleResult(account.ID, false, nil)
				h.gatewayService.RecordOpenAIAccountSwitch()
				failedAccountIDs[account.ID] = struct{}{}
				lastFailoverErr = failoverErr
				if switchCount >= h.maxAccountSwitches {
					h.handleFailoverExhausted(c, failoverErr, false)
					return
				}
				switchCount++
				continue
			}

			h.gatewayService.ReportOpenAIAccountScheduleResult(account.ID, false, nil)
			reqLog.Warn("openai.realtime_voice_token.forward_failed",
				zap.Int64("account_id", account.ID),
				zap.Error(forwardErr),
			)
			h.errorResponse(c, http.StatusBadGateway, "api_error", "Failed to create realtime voice session")
			return
		}

		h.gatewayService.ReportOpenAIAccountScheduleResult(account.ID, true, nil)
		h.gatewayService.UpdateCodexUsageSnapshotFromHeaders(c.Request.Context(), account.ID, result.ResponseHeaders)
		usagePayloadHash := service.HashUsageRequestPayload([]byte(strings.Join([]string{
			"realtime-voice-token",
			strconv.FormatInt(apiKey.ID, 10),
			model,
		}, "|")))
		usageRequestID := realtimeVoiceTokenUsageRequestID(c, apiKey.ID, result)
		if err := h.recordRealtimeVoiceUsage(
			c,
			apiKey,
			subscription,
			account,
			model,
			usageRequestID,
			usagePayloadHash,
			"/backend-api/voice_token",
			time.Since(requestStart),
		); err != nil {
			reqLog.Error("openai.realtime_voice_token.record_usage_failed", zap.Error(err))
			h.errorResponse(c, http.StatusServiceUnavailable, "billing_service_error", "Failed to record realtime voice session usage")
			return
		}
		// The raw account slot is intentionally retained. Redis removes it after
		// the configured fixed slot TTL, bounding active direct-media sessions
		// without keeping an HTTP request or audio buffer alive.
		pendingAccountRelease = nil
		for _, name := range []string{"x-request-id", "openai-request-id", "cf-ray"} {
			if value := strings.TrimSpace(result.ResponseHeaders.Get(name)); value != "" {
				c.Header(name, value)
			}
		}
		c.Header("Cache-Control", "no-store, max-age=0")
		c.Header("Pragma", "no-cache")
		c.Header("X-Realtime-Media-Path", "direct")
		c.Data(http.StatusOK, result.ContentType, result.Body)
		reqLog.Info("openai.realtime_voice_token.created",
			zap.Int64("account_id", account.ID),
			zap.String("schedule_layer", scheduleDecision.Layer),
			zap.Int("switch_count", switchCount),
		)
		return
	}
}

// RealtimeVoiceCall performs only the bounded SDP/session handshake used by
// ChatGPT's current WebRTC voice transport. Audio media flows directly between
// the client and OpenAI after setRemoteDescription applies the returned answer.
func (h *OpenAIGatewayHandler) RealtimeVoiceCall(c *gin.Context, explicitMode string) {
	streamStarted := false
	defer h.recoverResponsesPanic(c, &streamStarted)

	requestStart := time.Now()
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}

	reqLog := requestLogger(
		c,
		"handler.openai_gateway.realtime_voice_call",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
		zap.String("explicit_mode", explicitMode),
	)
	if !h.ensureResponsesDependencies(c, reqLog) {
		return
	}

	userReleaseFunc, acquired := h.acquireResponsesUserSlot(c, subject.UserID, subject.Concurrency, false, &streamStarted, reqLog)
	if !acquired {
		return
	}
	if userReleaseFunc != nil {
		defer userReleaseFunc()
	}

	parsed, err := h.gatewayService.ParseOpenAIRealtimeVoiceCallRequest(c, explicitMode)
	if err != nil {
		if maxErr, ok := extractMaxBytesError(err); ok {
			h.errorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit))
			return
		}
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	reqLog = reqLog.With(
		zap.String("model", parsed.Model),
		zap.String("upstream_path", parsed.UpstreamPath),
		zap.Int("sdp_size", len(parsed.SDP)),
		zap.Int("session_size", len(parsed.Session)),
	)

	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), apiKey.User, apiKey, apiKey.Group, subscription); err != nil {
		status, code, message, retryAfter := billingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.errorResponse(c, status, code, message)
		return
	}
	if err := h.gatewayService.ValidateRealtimeVoiceBilling(c.Request.Context(), apiKey); err != nil {
		reqLog.Error("openai.realtime_voice_call.billing_configuration_invalid", zap.Error(err))
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", err.Error())
		return
	}

	setOpsRequestContext(c, parsed.Model, false, nil)
	setOpsEndpointContext(c, "", int16(service.RequestTypeSync))
	service.SetOpsLatencyMs(c, service.OpsAuthLatencyMsKey, time.Since(requestStart).Milliseconds())
	routingStart := time.Now()
	sessionHash := h.gatewayService.GenerateSessionHashWithFallback(c, nil, parsed.StickySessionSeed())
	usageRequestID := parsed.UsageRequestID(apiKey.ID)

	failedAccountIDs := make(map[int64]struct{})
	switchCount := 0
	var lastFailoverErr *service.UpstreamFailoverError
	var pendingAccountRelease func()
	defer func() {
		if pendingAccountRelease != nil {
			pendingAccountRelease()
		}
	}()
	for {
		selection, scheduleDecision, selectErr := h.gatewayService.SelectAccountWithSchedulerForRealtimeVoice(
			c.Request.Context(),
			apiKey.GroupID,
			sessionHash,
			usageRequestID,
			failedAccountIDs,
		)
		if selectErr != nil || selection == nil || selection.Account == nil {
			reqLog.Warn("openai.realtime_voice_call.account_select_failed",
				zap.Error(selectErr),
				zap.Int("excluded_account_count", len(failedAccountIDs)),
			)
			if lastFailoverErr != nil {
				h.handleFailoverExhausted(c, lastFailoverErr, false)
			} else {
				h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "No available OpenAI OAuth voice account")
			}
			return
		}

		account := selection.Account
		setOpsSelectedAccount(c, account.ID, account.Platform)
		accountReleaseFunc, accountAcquired := h.acquireRealtimeVoiceAccountSlot(c, apiKey.GroupID, sessionHash, usageRequestID, selection, &streamStarted, reqLog)
		if !accountAcquired {
			return
		}
		pendingAccountRelease = accountReleaseFunc
		service.SetOpsLatencyMs(c, service.OpsRoutingLatencyMsKey, time.Since(routingStart).Milliseconds())

		forwardStart := time.Now()
		result, forwardErr := h.gatewayService.ForwardOAuthRealtimeVoiceCall(c.Request.Context(), c, account, parsed)
		service.SetOpsLatencyMs(c, service.OpsResponseLatencyMsKey, time.Since(forwardStart).Milliseconds())

		if forwardErr != nil {
			if pendingAccountRelease != nil {
				pendingAccountRelease()
				pendingAccountRelease = nil
			}
			var failoverErr *service.UpstreamFailoverError
			if errors.As(forwardErr, &failoverErr) {
				h.gatewayService.ReportOpenAIAccountScheduleResult(account.ID, false, nil)
				h.gatewayService.RecordOpenAIAccountSwitch()
				failedAccountIDs[account.ID] = struct{}{}
				lastFailoverErr = failoverErr
				if switchCount >= h.maxAccountSwitches {
					h.handleFailoverExhausted(c, failoverErr, false)
					return
				}
				switchCount++
				continue
			}

			h.gatewayService.ReportOpenAIAccountScheduleResult(account.ID, false, nil)
			reqLog.Warn("openai.realtime_voice_call.forward_failed",
				zap.Int64("account_id", account.ID),
				zap.Error(forwardErr),
			)
			h.errorResponse(c, http.StatusBadGateway, "api_error", "Failed to create realtime WebRTC session")
			return
		}

		accountReachable := result.StatusCode < http.StatusInternalServerError
		h.gatewayService.ReportOpenAIAccountScheduleResult(account.ID, accountReachable, nil)
		h.gatewayService.UpdateCodexUsageSnapshotFromHeaders(c.Request.Context(), account.ID, result.ResponseHeaders)
		if result.StatusCode < http.StatusOK || result.StatusCode >= http.StatusMultipleChoices {
			if pendingAccountRelease != nil {
				pendingAccountRelease()
				pendingAccountRelease = nil
			}
		} else {
			if err := h.recordRealtimeVoiceUsage(
				c,
				apiKey,
				subscription,
				account,
				parsed.Model,
				usageRequestID,
				parsed.UsagePayloadHash(),
				parsed.UpstreamPath,
				time.Since(requestStart),
			); err != nil {
				reqLog.Error("openai.realtime_voice_call.record_usage_failed", zap.Error(err))
				h.errorResponse(c, http.StatusServiceUnavailable, "billing_service_error", "Failed to record realtime voice session usage")
				return
			}
			pendingAccountRelease = nil
		}
		for _, name := range []string{"x-request-id", "openai-request-id", "cf-ray"} {
			if value := strings.TrimSpace(result.ResponseHeaders.Get(name)); value != "" {
				c.Header(name, value)
			}
		}
		c.Header("Cache-Control", "no-store, max-age=0")
		c.Header("Pragma", "no-cache")
		c.Header("X-Realtime-Media-Path", "direct")
		c.Header("X-Realtime-Signaling-Path", "proxied")
		c.Data(result.StatusCode, result.ContentType, result.Body)
		reqLog.Info("openai.realtime_voice_call.completed",
			zap.Int64("account_id", account.ID),
			zap.Int("status_code", result.StatusCode),
			zap.String("schedule_layer", scheduleDecision.Layer),
			zap.Int("switch_count", switchCount),
		)
		return
	}
}

func realtimeVoiceTokenUsageRequestID(
	c *gin.Context,
	apiKeyID int64,
	result *service.OpenAIRealtimeVoiceTokenResult,
) string {
	if value := realtimeVoiceClientIdempotencyValue(c); value != "" {
		return strings.Join([]string{
			"realtime-token",
			strconv.FormatInt(apiKeyID, 10),
			"idempotency",
			service.HashUsageRequestPayload([]byte(value)),
		}, ":")
	}
	if result != nil {
		for _, name := range []string{"x-request-id", "openai-request-id"} {
			if value := strings.TrimSpace(result.ResponseHeaders.Get(name)); value != "" {
				return strings.Join([]string{
					"realtime-token",
					strconv.FormatInt(apiKeyID, 10),
					"upstream",
					value,
				}, ":")
			}
		}
		if len(result.Body) > 0 {
			return strings.Join([]string{
				"realtime-token",
				strconv.FormatInt(apiKeyID, 10),
				"response",
				service.HashUsageRequestPayload(result.Body),
			}, ":")
		}
	}
	return strings.Join([]string{
		"realtime-token",
		strconv.FormatInt(apiKeyID, 10),
		"generated",
		strconv.FormatInt(time.Now().UnixNano(), 10),
	}, ":")
}

func realtimeVoiceTokenLeaseID(c *gin.Context, apiKeyID int64) string {
	if value := realtimeVoiceClientIdempotencyValue(c); value != "" {
		return strings.Join([]string{
			"realtime-token",
			strconv.FormatInt(apiKeyID, 10),
			"idempotency",
			service.HashUsageRequestPayload([]byte(value)),
		}, ":")
	}
	return strings.Join([]string{
		"realtime-token",
		strconv.FormatInt(apiKeyID, 10),
		"lease",
		strconv.FormatInt(time.Now().UnixNano(), 10),
	}, ":")
}

func realtimeVoiceClientIdempotencyValue(c *gin.Context) string {
	if c == nil {
		return ""
	}
	for _, name := range []string{"Idempotency-Key", "X-Client-Request-ID", "X-Request-ID"} {
		if value := strings.TrimSpace(c.GetHeader(name)); value != "" {
			if len(value) > 256 {
				value = value[:256]
			}
			return value
		}
	}
	return ""
}

func (h *OpenAIGatewayHandler) recordRealtimeVoiceUsage(
	c *gin.Context,
	apiKey *service.APIKey,
	subscription *service.UserSubscription,
	account *service.Account,
	requestedModel string,
	requestID string,
	requestPayloadHash string,
	upstreamEndpoint string,
	duration time.Duration,
) error {
	if h == nil || h.gatewayService == nil || apiKey == nil || account == nil {
		return errors.New("realtime voice usage dependencies are unavailable")
	}
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" {
		requestedModel = service.OpenAIRealtimeVoiceBillingModel
	}
	userAgent := ""
	clientIP := ""
	conversationID := ""
	inboundEndpoint := ""
	if c != nil {
		userAgent = c.GetHeader("User-Agent")
		clientIP = ip.GetClientIP(c)
		conversationID = service.ResolveUsageConversationID(c, nil, nil)
		inboundEndpoint = GetInboundEndpoint(c)
	}
	billingCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return h.gatewayService.RecordUsage(billingCtx, &service.OpenAIRecordUsageInput{
		Result: &service.OpenAIForwardResult{
			RequestID:              requestID,
			Model:                  requestedModel,
			BillingModel:           service.OpenAIRealtimeVoiceBillingModel,
			Duration:               duration,
			ForceUsageRecord:       true,
			ForcePerRequestBilling: true,
		},
		APIKey:             apiKey,
		User:               apiKey.User,
		Account:            account,
		Subscription:       subscription,
		ConversationID:     conversationID,
		InboundEndpoint:    inboundEndpoint,
		UpstreamEndpoint:   upstreamEndpoint,
		UserAgent:          userAgent,
		IPAddress:          clientIP,
		RequestPayloadHash: requestPayloadHash,
		APIKeyService:      h.apiKeyService,
	})
}

// AudioTranscriptions handles OpenAI-compatible speech-to-text requests.
// POST /v1/audio/transcriptions
func (h *OpenAIGatewayHandler) AudioTranscriptions(c *gin.Context) {
	streamStarted := false
	defer h.recoverResponsesPanic(c, &streamStarted)

	requestStart := time.Now()
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}

	reqLog := requestLogger(
		c,
		"handler.openai_gateway.audio_transcriptions",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
	)
	if !h.ensureResponsesDependencies(c, reqLog) {
		return
	}
	// Admit the request before buffering and inspecting a potentially 25 MB
	// upload. Streaming has not started yet, so waiting errors are regular HTTP
	// responses regardless of the multipart stream flag parsed below.
	userReleaseFunc, acquired := h.acquireResponsesUserSlot(c, subject.UserID, subject.Concurrency, false, &streamStarted, reqLog)
	if !acquired {
		return
	}
	if userReleaseFunc != nil {
		defer userReleaseFunc()
	}

	parsed, err := h.gatewayService.ParseOpenAIAudioTranscriptionRequest(c)
	if err != nil {
		if maxErr, ok := extractMaxBytesError(err); ok {
			h.errorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit))
			return
		}
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	reqLog = reqLog.With(
		zap.String("model", parsed.Model),
		zap.Bool("stream", parsed.Stream),
		zap.String("response_format", parsed.ResponseFormat),
		zap.String("file_content_type", parsed.FileContentType),
		zap.Int64("file_size", parsed.FileSize),
		zap.Int64("audio_duration_ms", parsed.AudioDuration.Milliseconds()),
	)
	// Never put binary audio data into ops request logs.
	setOpsRequestContext(c, parsed.Model, parsed.Stream, nil)
	setOpsEndpointContext(c, "", int16(service.RequestTypeFromLegacy(parsed.Stream, false)))

	channelMapping, _ := h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), apiKey.GroupID, parsed.Model)
	if h.errorPassthroughService != nil {
		service.BindErrorPassthroughService(c, h.errorPassthroughService)
	}
	subscription, _ := middleware2.GetSubscriptionFromContext(c)

	service.SetOpsLatencyMs(c, service.OpsAuthLatencyMsKey, time.Since(requestStart).Milliseconds())
	routingStart := time.Now()

	if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), apiKey.User, apiKey, apiKey.Group, subscription); err != nil {
		reqLog.Info("openai.audio_transcriptions.billing_eligibility_check_failed", zap.Error(err))
		status, code, message, retryAfter := billingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.handleStreamingAwareError(c, status, code, message, streamStarted)
		return
	}

	sessionHash := h.gatewayService.GenerateSessionHashWithFallback(c, nil, parsed.StickySessionSeed())
	maxAccountSwitches := h.maxAccountSwitches
	switchCount := 0
	failedAccountIDs := make(map[int64]struct{})
	sameAccountRetryCount := make(map[int64]int)
	var lastFailoverErr *service.UpstreamFailoverError

	for {
		selection, scheduleDecision, err := h.gatewayService.SelectAccountWithSchedulerForAudioTranscription(
			c.Request.Context(),
			apiKey.GroupID,
			sessionHash,
			parsed.Model,
			channelMapping.MappedModel,
			parsed.ResponseFormat,
			failedAccountIDs,
		)
		if err != nil {
			reqLog.Warn("openai.audio_transcriptions.account_select_failed",
				zap.Error(err),
				zap.Int("excluded_account_count", len(failedAccountIDs)),
			)
			if len(failedAccountIDs) == 0 {
				h.handleStreamingAwareError(c, http.StatusServiceUnavailable, "api_error", "No available compatible accounts", streamStarted)
				return
			}
			if lastFailoverErr != nil {
				h.handleFailoverExhausted(c, lastFailoverErr, streamStarted)
			} else {
				h.handleFailoverExhaustedSimple(c, http.StatusBadGateway, streamStarted)
			}
			return
		}
		if selection == nil || selection.Account == nil {
			h.handleStreamingAwareError(c, http.StatusServiceUnavailable, "api_error", "No available compatible accounts", streamStarted)
			return
		}

		reqLog.Debug("openai.audio_transcriptions.account_schedule_decision",
			zap.String("layer", scheduleDecision.Layer),
			zap.Bool("sticky_session_hit", scheduleDecision.StickySessionHit),
			zap.Int("candidate_count", scheduleDecision.CandidateCount),
			zap.Int("top_k", scheduleDecision.TopK),
			zap.Int64("latency_ms", scheduleDecision.LatencyMs),
			zap.Float64("load_skew", scheduleDecision.LoadSkew),
		)

		account := selection.Account
		if err := h.gatewayService.ValidateAudioTranscriptionBilling(
			c.Request.Context(),
			apiKey,
			subscription,
			account,
			parsed.Model,
			channelMapping,
			parsed.ResponseFormat,
			parsed.AudioDuration,
		); err != nil {
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
			reqLog.Error("openai.audio_transcriptions.billing_configuration_invalid",
				zap.Int64("account_id", account.ID),
				zap.Error(err),
			)
			h.handleStreamingAwareError(c, http.StatusServiceUnavailable, "api_error", err.Error(), streamStarted)
			return
		}
		sessionHash = ensureOpenAIPoolModeSessionHash(sessionHash, account)
		setOpsSelectedAccount(c, account.ID, account.Platform)
		accountReleaseFunc, acquired := h.acquireResponsesAccountSlot(c, apiKey.GroupID, sessionHash, selection, parsed.Stream, &streamStarted, reqLog)
		if !acquired {
			return
		}

		service.SetOpsLatencyMs(c, service.OpsRoutingLatencyMsKey, time.Since(routingStart).Milliseconds())
		forwardStart := time.Now()
		result, err := h.gatewayService.ForwardAudioTranscription(c.Request.Context(), c, account, parsed, channelMapping.MappedModel)
		forwardDurationMs := time.Since(forwardStart).Milliseconds()
		if accountReleaseFunc != nil {
			accountReleaseFunc()
		}
		upstreamLatencyMs, _ := getContextInt64(c, service.OpsUpstreamLatencyMsKey)
		responseLatencyMs := forwardDurationMs
		if upstreamLatencyMs > 0 && forwardDurationMs > upstreamLatencyMs {
			responseLatencyMs = forwardDurationMs - upstreamLatencyMs
		}
		service.SetOpsLatencyMs(c, service.OpsResponseLatencyMsKey, responseLatencyMs)
		if err == nil && result != nil && result.FirstTokenMs != nil {
			service.SetOpsLatencyMs(c, service.OpsTimeToFirstTokenMsKey, int64(*result.FirstTokenMs))
		}

		if err != nil {
			var failoverErr *service.UpstreamFailoverError
			if errors.As(err, &failoverErr) {
				h.gatewayService.ReportOpenAIAccountScheduleResult(account.ID, false, nil)
				if failoverErr.RetryableOnSameAccount {
					retryLimit := account.GetPoolModeRetryCount()
					if sameAccountRetryCount[account.ID] < retryLimit {
						sameAccountRetryCount[account.ID]++
						select {
						case <-c.Request.Context().Done():
							return
						case <-time.After(sameAccountRetryDelay):
						}
						continue
					}
				}
				h.gatewayService.RecordOpenAIAccountSwitch()
				failedAccountIDs[account.ID] = struct{}{}
				lastFailoverErr = failoverErr
				if switchCount >= maxAccountSwitches {
					h.handleFailoverExhausted(c, failoverErr, streamStarted)
					return
				}
				switchCount++
				continue
			}

			h.gatewayService.ReportOpenAIAccountScheduleResult(account.ID, false, nil)
			wroteFallback := h.ensureForwardErrorResponse(c, streamStarted)
			fields := []zap.Field{
				zap.Int64("account_id", account.ID),
				zap.Bool("fallback_error_response_written", wroteFallback),
				zap.Error(err),
			}
			if shouldLogOpenAIForwardFailureAsWarn(c, wroteFallback) {
				reqLog.Warn("openai.audio_transcriptions.forward_failed", fields...)
				return
			}
			reqLog.Error("openai.audio_transcriptions.forward_failed", fields...)
			return
		}

		if result != nil {
			if account.Type == service.AccountTypeOAuth {
				h.gatewayService.UpdateCodexUsageSnapshotFromHeaders(c.Request.Context(), account.ID, result.ResponseHeaders)
			}
			h.gatewayService.ReportOpenAIAccountScheduleResult(account.ID, true, result.FirstTokenMs)
		} else {
			h.gatewayService.ReportOpenAIAccountScheduleResult(account.ID, true, nil)
		}

		userAgent := c.GetHeader("User-Agent")
		clientIP := ip.GetClientIP(c)
		requestPayloadHash := service.HashUsageRequestPayload([]byte(parsed.StickySessionSeed()))
		conversationID := service.ResolveUsageConversationID(c, nil, nil)
		inboundEndpoint := GetInboundEndpoint(c)
		upstreamEndpoint := GetUpstreamEndpoint(c, account.Platform)

		h.submitUsageRecordTask(func(ctx context.Context) {
			if err := h.gatewayService.RecordUsage(ctx, &service.OpenAIRecordUsageInput{
				Result:             result,
				APIKey:             apiKey,
				User:               apiKey.User,
				Account:            account,
				Subscription:       subscription,
				ConversationID:     conversationID,
				InboundEndpoint:    inboundEndpoint,
				UpstreamEndpoint:   upstreamEndpoint,
				UserAgent:          userAgent,
				IPAddress:          clientIP,
				RequestPayloadHash: requestPayloadHash,
				APIKeyService:      h.apiKeyService,
				ChannelUsageFields: channelMapping.ToUsageFields(parsed.Model, result.UpstreamModel),
			}); err != nil {
				logger.L().With(
					zap.String("component", "handler.openai_gateway.audio_transcriptions"),
					zap.Int64("user_id", subject.UserID),
					zap.Int64("api_key_id", apiKey.ID),
					zap.Any("group_id", apiKey.GroupID),
					zap.String("model", parsed.Model),
					zap.Int64("account_id", account.ID),
				).Error("openai.audio_transcriptions.record_usage_failed", zap.Error(err))
			}
		})

		reqLog.Debug("openai.audio_transcriptions.request_completed",
			zap.Int64("account_id", account.ID),
			zap.Int("switch_count", switchCount),
		)
		return
	}
}
