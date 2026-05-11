package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type HappyHorseGatewayHandler struct {
	gatewayService        *service.GatewayService
	happyHorseService     *service.HappyHorseGatewayService
	concurrencyHelper     *ConcurrencyHelper
	billingCacheService   *service.BillingCacheService
	apiKeyService         *service.APIKeyService
	usageRecordWorkerPool *service.UsageRecordWorkerPool
	maxAccountSwitches    int
}

func NewHappyHorseGatewayHandler(
	gatewayService *service.GatewayService,
	happyHorseService *service.HappyHorseGatewayService,
	concurrencyService *service.ConcurrencyService,
	billingCacheService *service.BillingCacheService,
	apiKeyService *service.APIKeyService,
	usageRecordWorkerPool *service.UsageRecordWorkerPool,
) *HappyHorseGatewayHandler {
	return &HappyHorseGatewayHandler{
		gatewayService:        gatewayService,
		happyHorseService:     happyHorseService,
		concurrencyHelper:     NewConcurrencyHelper(concurrencyService, SSEPingFormatComment, defaultPingInterval),
		billingCacheService:   billingCacheService,
		apiKeyService:         apiKeyService,
		usageRecordWorkerPool: usageRecordWorkerPool,
		maxAccountSwitches:    3,
	}
}

func (h *HappyHorseGatewayHandler) Generate(c *gin.Context) {
	requestStart := time.Now()
	apiKey, subject, ok := h.requireAuth(c)
	if !ok {
		return
	}
	if !h.requireHappyHorseGroup(c, apiKey) {
		return
	}
	body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		if maxErr, ok := extractMaxBytesError(err); ok {
			h.errorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit))
			return
		}
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}
	parsed, err := h.happyHorseService.ParseGenerateRequest(body)
	if err != nil {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	setOpsRequestContext(c, parsed.Model, false, body)
	setOpsEndpointContext(c, "", int16(service.RequestTypeSync))
	channelMapping, _ := h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), apiKey.GroupID, parsed.Model)
	mappedReq, err := parsed.WithModel(channelMapping.MappedModel)
	if err != nil {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	reqLog := requestLogger(
		c,
		"handler.happyhorse.generate",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
		zap.String("model", parsed.Model),
	)

	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	service.SetOpsLatencyMs(c, service.OpsAuthLatencyMsKey, time.Since(requestStart).Milliseconds())
	userRelease, err := h.concurrencyHelper.AcquireUserSlotWithWait(c, subject.UserID, subject.Concurrency, false, new(bool))
	if err != nil {
		h.handleConcurrencyError(c, err, "user")
		return
	}
	if userRelease != nil {
		defer userRelease()
	}
	if h.billingCacheService != nil {
		if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), apiKey.User, apiKey, apiKey.Group, subscription); err != nil {
			status, code, message, retryAfter := billingErrorDetails(err)
			if retryAfter > 0 {
				c.Header("Retry-After", strconv.Itoa(retryAfter))
			}
			h.errorResponse(c, status, code, message)
			return
		}
	}

	sessionHash := service.HashUsageRequestPayload(mappedReq.Body)
	failedAccountIDs := make(map[int64]struct{})
	switchCount := 0
	var lastFailover *service.UpstreamFailoverError
	routingStart := time.Now()
	for {
		selection, err := h.gatewayService.SelectAccountWithLoadAwareness(c.Request.Context(), apiKey.GroupID, sessionHash, mappedReq.Model, failedAccountIDs, "", subject.UserID)
		if err != nil || selection == nil || selection.Account == nil {
			if lastFailover != nil {
				h.handleFailoverExhausted(c, lastFailover)
				return
			}
			reqLog.Warn("happyhorse.account_select_failed", zap.Error(err))
			h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "No available HappyHorse accounts")
			return
		}
		account := selection.Account
		setOpsSelectedAccount(c, account.ID, account.Platform)
		accountRelease, acquired := h.acquireAccountSlot(c, selection)
		if !acquired {
			return
		}
		service.SetOpsLatencyMs(c, service.OpsRoutingLatencyMsKey, time.Since(routingStart).Milliseconds())
		forwardStart := time.Now()
		result, err := h.happyHorseService.ForwardGenerate(c.Request.Context(), account, mappedReq)
		if accountRelease != nil {
			accountRelease()
		}
		service.SetOpsLatencyMs(c, service.OpsUpstreamLatencyMsKey, time.Since(forwardStart).Milliseconds())
		service.SetOpsLatencyMs(c, service.OpsResponseLatencyMsKey, time.Since(forwardStart).Milliseconds())
		if err != nil {
			var failoverErr *service.UpstreamFailoverError
			if errors.As(err, &failoverErr) {
				failedAccountIDs[account.ID] = struct{}{}
				lastFailover = failoverErr
				if switchCount >= h.maxAccountSwitches {
					h.handleFailoverExhausted(c, failoverErr)
					return
				}
				switchCount++
				continue
			}
			h.handleUpstreamError(c, err)
			return
		}
		if strings.TrimSpace(result.TaskID) == "" {
			reqLog.Error("happyhorse.missing_task_id")
			h.errorResponse(c, http.StatusBadGateway, "upstream_error", "HappyHorse upstream did not return task_id")
			return
		}
		if err := h.happyHorseService.CreateTask(c.Request.Context(), &service.HappyHorseTask{
			UserID:             apiKey.User.ID,
			APIKeyID:           apiKey.ID,
			AccountID:          account.ID,
			GroupID:            apiKey.GroupID,
			TaskID:             result.TaskID,
			RequestID:          result.RequestID,
			Model:              mappedReq.Model,
			Prompt:             parsed.Prompt,
			Status:             service.NormalizeHappyHorseStatusForHandler(result.Status),
			ResultURLs:         result.ResultURLs,
			ErrorMessage:       result.ErrorMessage,
			UpstreamResponse:   result.UpstreamPayload,
			RequestPayloadHash: mappedReq.RequestPayloadHash,
		}); err != nil {
			reqLog.Error("happyhorse.task_create_failed", zap.Error(err), zap.String("task_id", result.TaskID))
			h.errorResponse(c, http.StatusBadGateway, "api_error", "Failed to persist HappyHorse task")
			return
		}
		h.writeUpstream(c, result.StatusCode, result.Headers, result.Body)
		return
	}
}

func (h *HappyHorseGatewayHandler) Status(c *gin.Context) {
	apiKey, subject, ok := h.requireAuth(c)
	if !ok {
		return
	}
	if !h.requireHappyHorseGroup(c, apiKey) {
		return
	}
	taskID := strings.TrimSpace(c.Query("task_id"))
	if taskID == "" {
		taskID = strings.TrimSpace(c.Param("task_id"))
	}
	if taskID == "" {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "task_id is required")
		return
	}
	task, err := h.happyHorseService.GetTaskByTaskID(c.Request.Context(), taskID)
	if err != nil {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "HappyHorse task not found")
		return
	}
	if task.APIKeyID != apiKey.ID || task.UserID != subject.UserID {
		h.errorResponse(c, http.StatusForbidden, "permission_error", "HappyHorse task is not accessible")
		return
	}
	account, err := h.happyHorseService.GetAccountByID(c.Request.Context(), task.AccountID)
	if err != nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "HappyHorse account is unavailable")
		return
	}
	result, err := h.happyHorseService.ForwardStatus(c.Request.Context(), account, taskID)
	if err != nil {
		h.handleUpstreamError(c, err)
		return
	}
	if err := h.happyHorseService.UpdateTaskFromStatus(c.Request.Context(), taskID, result); err != nil {
		logger.L().With(zap.String("component", "handler.happyhorse.status")).Warn("happyhorse.update_task_failed", zap.Error(err))
	}
	if service.NormalizeHappyHorseStatusForHandler(result.Status) == "success" && !task.UsageRecorded {
		claimed, err := h.happyHorseService.ClaimTaskUsageRecording(c.Request.Context(), task.TaskID)
		if err != nil {
			logger.L().With(zap.String("component", "handler.happyhorse.status")).Warn("happyhorse.claim_usage_recording_failed", zap.Error(err))
			h.writeUpstream(c, result.StatusCode, result.Headers, result.Body)
			return
		} else if !claimed {
			h.writeUpstream(c, result.StatusCode, result.Headers, result.Body)
			return
		}
		subscription, _ := middleware2.GetSubscriptionFromContext(c)
		userAgent := c.GetHeader("User-Agent")
		clientIP := ip.GetClientIP(c)
		mappedModel := result.Model
		if strings.TrimSpace(mappedModel) == "" {
			mappedModel = task.Model
		}
		billingTier := happyHorseBillingTierFromPayload(result.UpstreamPayload)
		inboundEndpoint := GetInboundEndpoint(c)
		upstreamEndpoint := "/api/status"
		channelFields := h.gatewayService.ResolveChannelMapping(c.Request.Context(), derefGroupIDForHappyHorse(apiKey.GroupID), task.Model).ToUsageFields(task.Model, mappedModel)
		h.submitUsageRecordTask(func(ctx context.Context) {
			forwardResult := &service.ForwardResult{
				RequestID:     "happyhorse:" + task.TaskID,
				Model:         task.Model,
				UpstreamModel: mappedModel,
				Duration:      result.Duration,
				ImageCount:    1,
				ImageSize:     billingTier,
			}
			if err := h.gatewayService.RecordUsage(ctx, &service.RecordUsageInput{
				Result:             forwardResult,
				APIKey:             apiKey,
				User:               apiKey.User,
				Account:            account,
				Subscription:       subscription,
				InboundEndpoint:    inboundEndpoint,
				UpstreamEndpoint:   upstreamEndpoint,
				UserAgent:          userAgent,
				IPAddress:          clientIP,
				RequestPayloadHash: task.RequestPayloadHash,
				APIKeyService:      h.apiKeyService,
				ChannelUsageFields: channelFields,
			}); err != nil {
				logger.L().With(zap.String("component", "handler.happyhorse.status")).Error("happyhorse.record_usage_failed", zap.Error(err))
				if resetErr := h.happyHorseService.ResetTaskUsageRecording(ctx, task.TaskID); resetErr != nil {
					logger.L().With(zap.String("component", "handler.happyhorse.status")).Warn("happyhorse.reset_usage_recording_failed", zap.Error(resetErr))
				}
				return
			}
			if err := h.happyHorseService.MarkTaskUsageRecorded(ctx, task.TaskID); err != nil {
				logger.L().With(zap.String("component", "handler.happyhorse.status")).Warn("happyhorse.mark_usage_recorded_failed", zap.Error(err))
			}
		})
	}
	h.writeUpstream(c, result.StatusCode, result.Headers, result.Body)
}

func (h *HappyHorseGatewayHandler) requireAuth(c *gin.Context) (*service.APIKey, middleware2.AuthSubject, bool) {
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return nil, middleware2.AuthSubject{}, false
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return nil, middleware2.AuthSubject{}, false
	}
	return apiKey, subject, true
}

func (h *HappyHorseGatewayHandler) requireHappyHorseGroup(c *gin.Context, apiKey *service.APIKey) bool {
	if apiKey == nil || apiKey.Group == nil || apiKey.Group.Platform != service.PlatformHappyHorse {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "HappyHorse API is not supported for this group")
		return false
	}
	return true
}

func (h *HappyHorseGatewayHandler) acquireAccountSlot(c *gin.Context, selection *service.AccountSelectionResult) (func(), bool) {
	if selection == nil || selection.Account == nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "No available HappyHorse accounts")
		return nil, false
	}
	if selection.Acquired {
		return wrapReleaseOnDone(c.Request.Context(), selection.ReleaseFunc), true
	}
	if selection.WaitPlan == nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Selected account is busy")
		return nil, false
	}
	canWait, err := h.concurrencyHelper.IncrementAccountWaitCount(c.Request.Context(), selection.Account.ID, selection.WaitPlan.MaxWaiting)
	if err != nil || !canWait {
		h.errorResponse(c, http.StatusTooManyRequests, "rate_limit_error", "Account concurrency queue is full")
		return nil, false
	}
	defer h.concurrencyHelper.DecrementAccountWaitCount(c.Request.Context(), selection.Account.ID)
	streamStarted := false
	release, err := h.concurrencyHelper.AcquireAccountSlotWithWaitTimeout(c, selection.Account.ID, selection.WaitPlan.MaxConcurrency, selection.WaitPlan.Timeout, false, &streamStarted)
	if err != nil {
		h.handleConcurrencyError(c, err, "account")
		return nil, false
	}
	return wrapReleaseOnDone(c.Request.Context(), release), true
}

func (h *HappyHorseGatewayHandler) errorResponse(c *gin.Context, status int, errType, message string) {
	c.JSON(status, gin.H{"error": gin.H{"type": errType, "message": message}})
}

func (h *HappyHorseGatewayHandler) handleConcurrencyError(c *gin.Context, err error, slotType string) {
	var concurrencyErr *ConcurrencyError
	if errors.As(err, &concurrencyErr) {
		status := http.StatusTooManyRequests
		message := "Concurrency limit exceeded"
		if concurrencyErr.IsTimeout {
			status = http.StatusServiceUnavailable
			message = "Timed out waiting for " + slotType + " concurrency slot"
		}
		h.errorResponse(c, status, "rate_limit_error", message)
		return
	}
	h.errorResponse(c, http.StatusInternalServerError, "api_error", err.Error())
}

func (h *HappyHorseGatewayHandler) handleFailoverExhausted(c *gin.Context, failoverErr *service.UpstreamFailoverError) {
	if failoverErr == nil {
		h.errorResponse(c, http.StatusBadGateway, "upstream_error", "Upstream request failed")
		return
	}
	message := service.ExtractUpstreamErrorMessage(failoverErr.ResponseBody)
	if strings.TrimSpace(message) == "" {
		message = "Upstream request failed"
	}
	h.errorResponse(c, mapHappyHorseUpstreamStatus(failoverErr.StatusCode), "upstream_error", message)
}

func (h *HappyHorseGatewayHandler) handleUpstreamError(c *gin.Context, err error) {
	var upstreamErr *service.HappyHorseHTTPError
	if errors.As(err, &upstreamErr) {
		message := upstreamErr.Message
		if strings.TrimSpace(message) == "" {
			message = "Upstream request failed"
		}
		h.errorResponse(c, mapHappyHorseUpstreamStatus(upstreamErr.StatusCode), "upstream_error", message)
		return
	}
	h.errorResponse(c, http.StatusBadGateway, "upstream_error", err.Error())
}

func (h *HappyHorseGatewayHandler) writeUpstream(c *gin.Context, status int, headers http.Header, body []byte) {
	if status == 0 {
		status = http.StatusOK
	}
	if contentType := headers.Get("Content-Type"); strings.TrimSpace(contentType) != "" {
		c.Header("Content-Type", contentType)
	} else {
		c.Header("Content-Type", "application/json")
	}
	for _, key := range []string{"x-request-id", "cf-ray"} {
		if value := strings.TrimSpace(headers.Get(key)); value != "" {
			c.Header(key, value)
		}
	}
	c.Writer.WriteHeader(status)
	_, _ = c.Writer.Write(body)
}

func (h *HappyHorseGatewayHandler) submitUsageRecordTask(task func(ctx context.Context)) {
	if h.usageRecordWorkerPool != nil {
		h.usageRecordWorkerPool.Submit(task)
		return
	}
	go task(context.Background())
}

func mapHappyHorseUpstreamStatus(status int) int {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return http.StatusBadGateway
	case http.StatusTooManyRequests:
		return http.StatusTooManyRequests
	default:
		if status >= 500 {
			return http.StatusBadGateway
		}
		if status >= 400 {
			return status
		}
		return http.StatusBadGateway
	}
}

func happyHorseBillingTierFromPayload(payload map[string]any) string {
	if payload == nil {
		return "video"
	}
	data, _ := payload["data"].(map[string]any)
	mode := stringFromAny(data["mode"])
	duration := stringFromAny(data["duration"])
	aspect := strings.ReplaceAll(stringFromAny(data["aspect_ratio"]), ":", "x")
	parts := make([]string, 0, 3)
	for _, part := range []string{mode, duration, aspect} {
		if strings.TrimSpace(part) != "" {
			parts = append(parts, strings.TrimSpace(part))
		}
	}
	if len(parts) == 0 {
		return "video"
	}
	return strings.Join(parts, "_")
}

func stringFromAny(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	default:
		return ""
	}
}

func derefGroupIDForHappyHorse(groupID *int64) int64 {
	if groupID == nil {
		return 0
	}
	return *groupID
}
