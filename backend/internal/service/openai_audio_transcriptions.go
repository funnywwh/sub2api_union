package service

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	openAIAudioTranscriptionsEndpoint = "/v1/audio/transcriptions"
	openAIAudioTranscriptionsURL      = "https://api.openai.com/v1/audio/transcriptions"
	// Codex OAuth tokens are ChatGPT credentials, not Platform API keys. ChatGPT's
	// transcription backend is therefore the OAuth upstream for this endpoint.
	chatgptAudioTranscriptionsURL = "https://chatgpt.com/backend-api/transcribe"
	chatgptRealtimeVoiceBaseURL   = "https://chatgpt.com"
	// ChatGPT bootstraps realtime voice with a small, short-lived credential
	// response. Media is sent directly by the client over the transport described
	// by that response; it must never be buffered by this gateway.
	chatgptRealtimeVoiceTokenURL = "https://chatgpt.com/backend-api/voice_token"
	realtimeVoiceTokenMaxSize    = 64 << 10

	// ChatGPT's current web client uses the Realtime unified WebRTC handshake:
	// one bounded multipart request containing an SDP offer and session JSON,
	// followed by direct ICE/DTLS/SRTP media between the client and OpenAI.
	chatgptRealtimeVoiceAdvancedPath = "/realtime/vp"
	chatgptRealtimeVoiceStandardPath = "/realtime/vps"
	chatgptRealtimeVoiceWingmanPath  = "/realtime/wm"
	realtimeVoiceSDPMaxSize          = 256 << 10
	realtimeVoiceSessionMaxSize      = 256 << 10
	realtimeVoiceCallMaxRequestSize  = 1 << 20
	realtimeVoiceCallMaxResponseSize = 1 << 20
	// OpenAIRealtimeVoiceBillingModel is deliberately independent from
	// ChatGPT's private upstream model names. Realtime voice is billed once per
	// successfully created session through channel per-request pricing.
	OpenAIRealtimeVoiceBillingModel = "chatgpt-realtime-voice"

	openAIAudioMaxFieldSize   = 1 << 20
	openAIAudioMaxFileSize    = 25 << 20
	openAIAudioMaxRequestSize = 27 << 20

	// ChatGPT's transcription endpoint does not return token usage. When no
	// channel price is configured, bill the uploaded audio by its media length.
	defaultAudioTranscriptionPerHourPriceUSD = 0.2
	audioPricingRefreshCooldown              = time.Minute
)

type audioPricingRefreshKey struct {
	groupID int64
	model   string
}

type OpenAIAudioTranscriptionRequest struct {
	ContentType     string
	Body            []byte
	Model           string
	Stream          bool
	ResponseFormat  string
	Language        string
	Prompt          string
	FileName        string
	FileContentType string
	FileSize        int64
	AudioDuration   time.Duration
	bodyHash        string
}

// OpenAIRealtimeVoiceTokenResult is intentionally a raw, size-bounded
// passthrough. ChatGPT's private response fields can evolve independently of
// this gateway, and clients need the upstream token/E2EE material unchanged.
type OpenAIRealtimeVoiceTokenResult struct {
	Body            []byte
	ContentType     string
	ResponseHeaders http.Header
}

// OpenAIRealtimeVoiceCallRequest is the bounded WebRTC signaling payload. It
// never contains microphone frames; those flow directly over the peer
// connection negotiated by SDP.
type OpenAIRealtimeVoiceCallRequest struct {
	SDP          []byte
	Session      []byte
	UpstreamPath string
	Query        string
	Model        string
}

type OpenAIRealtimeVoiceCallResult struct {
	StatusCode      int
	Body            []byte
	ContentType     string
	ResponseHeaders http.Header
}

func (r *OpenAIRealtimeVoiceCallRequest) StickySessionSeed() string {
	if r == nil {
		return ""
	}
	return strings.Join([]string{
		"openai-realtime-voice-call",
		strings.TrimSpace(r.Model),
		strings.TrimSpace(r.UpstreamPath),
		strings.TrimSpace(r.Query),
		r.UsagePayloadHash(),
	}, "|")
}

// UsagePayloadHash returns a semantic signaling fingerprint. Multipart
// boundaries are intentionally excluded so a transport retry of the same SDP
// offer remains idempotent, while a new PeerConnection produces a new lease
// and charge through its different SDP/ICE credentials.
func (r *OpenAIRealtimeVoiceCallRequest) UsagePayloadHash() string {
	if r == nil {
		return ""
	}
	hash := sha256.New()
	for _, value := range [][]byte{
		[]byte(strings.TrimSpace(r.UpstreamPath)),
		[]byte(strings.TrimSpace(r.Query)),
		bytes.TrimSpace(r.SDP),
		bytes.TrimSpace(r.Session),
	} {
		_, _ = hash.Write([]byte(strconv.Itoa(len(value))))
		_, _ = hash.Write([]byte{':'})
		_, _ = hash.Write(value)
		_, _ = hash.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (r *OpenAIRealtimeVoiceCallRequest) UsageRequestID(apiKeyID int64) string {
	if r == nil {
		return ""
	}
	return fmt.Sprintf("realtime-call:%d:%s", apiKeyID, r.UsagePayloadHash())
}

// SelectAccountWithSchedulerForRealtimeVoice selects OAuth only and skips
// text-model mapping. ChatGPT voice is an account capability, not a Codex text
// model advertised by account model_mapping.
func (s *OpenAIGatewayService) SelectAccountWithSchedulerForRealtimeVoice(
	ctx context.Context,
	groupID *int64,
	sessionHash string,
	leaseID string,
	excludedIDs map[int64]struct{},
) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	ctx = WithPersistentAccountSlotLease(ctx, leaseID)
	return s.SelectAccountWithScheduler(
		ctx,
		groupID,
		"",
		sessionHash,
		"",
		excludedIDs,
		OpenAIUpstreamTransportAny,
		false,
		AccountTypeOAuth,
	)
}

// ValidateRealtimeVoiceBilling requires an explicit positive per-session
// channel price. ChatGPT's private WebRTC path does not expose reliable token
// usage to this gateway, so zero-token or private-model-name billing would
// silently make sessions free.
func (s *OpenAIGatewayService) ValidateRealtimeVoiceBilling(ctx context.Context, apiKey *APIKey) error {
	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		return nil
	}
	resolved := s.resolveRealtimeVoiceChannelPricing(ctx, apiKey)
	if resolved == nil {
		return fmt.Errorf(
			"realtime voice requires positive per_request channel pricing for model %s",
			OpenAIRealtimeVoiceBillingModel,
		)
	}
	return nil
}

func (s *OpenAIGatewayService) resolveRealtimeVoiceChannelPricing(ctx context.Context, apiKey *APIKey) *ResolvedPricing {
	if apiKey == nil || apiKey.Group == nil || s.resolver == nil || s.billingService == nil {
		return nil
	}

	resolve := func() *ResolvedPricing {
		resolved := s.resolveOpenAIChannelPricing(ctx, OpenAIRealtimeVoiceBillingModel, apiKey)
		if resolved == nil || resolved.Mode != BillingModePerRequest {
			return nil
		}
		gid := apiKey.Group.ID
		cost, err := s.billingService.CalculateCostUnified(CostInput{
			Ctx:            ctx,
			Model:          OpenAIRealtimeVoiceBillingModel,
			GroupID:        &gid,
			RequestCount:   1,
			RateMultiplier: 1,
			Resolver:       s.resolver,
			Resolved:       resolved,
		})
		if err != nil || cost == nil || cost.TotalCost <= 0 {
			return nil
		}
		return resolved
	}

	if resolved := resolve(); resolved != nil {
		return resolved
	}
	if s.channelService != nil && s.shouldRefreshAudioPricing(apiKey, OpenAIRealtimeVoiceBillingModel) {
		if err := s.channelService.forceRefreshCache(ctx); err == nil {
			return resolve()
		}
	}
	return nil
}

func (s *OpenAIGatewayService) calculateRealtimeVoiceCost(
	ctx context.Context,
	apiKey *APIKey,
	multiplier float64,
) (*CostBreakdown, error) {
	resolved := s.resolveRealtimeVoiceChannelPricing(ctx, apiKey)
	if resolved == nil {
		return nil, fmt.Errorf(
			"realtime voice requires positive per_request channel pricing for model %s",
			OpenAIRealtimeVoiceBillingModel,
		)
	}
	gid := apiKey.Group.ID
	return s.billingService.CalculateCostUnified(CostInput{
		Ctx:            ctx,
		Model:          OpenAIRealtimeVoiceBillingModel,
		GroupID:        &gid,
		RequestCount:   1,
		RateMultiplier: multiplier,
		Resolver:       s.resolver,
		Resolved:       resolved,
	})
}

func (r *OpenAIAudioTranscriptionRequest) StickySessionSeed() string {
	if r == nil {
		return ""
	}
	return strings.Join([]string{
		"openai-audio-transcription",
		strings.TrimSpace(r.Model),
		strings.TrimSpace(r.Language),
		strings.TrimSpace(r.Prompt),
		strings.TrimSpace(r.FileName),
		r.bodyHash,
	}, "|")
}

// SelectAccountWithSchedulerForAudioTranscription preserves normal model-aware
// routing for Platform API key accounts, while allowing a Codex OAuth account
// to use ChatGPT's model-less transcription backend. Codex account model
// mappings describe text-generation models and must not exclude /transcribe.
func (s *OpenAIGatewayService) SelectAccountWithSchedulerForAudioTranscription(
	ctx context.Context,
	groupID *int64,
	sessionHash string,
	requestedModel string,
	oauthModel string,
	responseFormat string,
	excludedIDs map[int64]struct{},
) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	if s.checkChannelPricingRestriction(ctx, groupID, requestedModel) {
		return nil, OpenAIAccountScheduleDecision{}, fmt.Errorf(
			"%w supporting model: %s (channel pricing restriction)",
			ErrNoAvailableAccounts,
			requestedModel,
		)
	}

	selection, decision, strictErr := s.SelectAccountWithScheduler(
		ctx,
		groupID,
		"",
		sessionHash,
		requestedModel,
		excludedIDs,
		OpenAIUpstreamTransportAny,
		false,
		AccountTypeAPIKey,
	)
	if strictErr == nil && selection != nil && selection.Account != nil {
		return selection, decision, nil
	}
	if !supportsChatGPTAudioTranscription(oauthModel, responseFormat) {
		return selection, decision, strictErr
	}

	// ChatGPT /backend-api/transcribe accepts file + optional language and does
	// not accept the public API's model field. Retry OAuth-only selection without
	// applying the account's text-model mapping to this capability.
	oauthSelection, oauthDecision, oauthErr := s.SelectAccountWithScheduler(
		ctx,
		groupID,
		"",
		sessionHash,
		"",
		excludedIDs,
		OpenAIUpstreamTransportAny,
		false,
		AccountTypeOAuth,
	)
	if oauthErr != nil || oauthSelection == nil || oauthSelection.Account == nil {
		if strictErr != nil {
			return nil, decision, strictErr
		}
		return oauthSelection, oauthDecision, oauthErr
	}

	if groupID != nil && s.needsUpstreamChannelRestrictionCheck(ctx, groupID) &&
		s.isUpstreamModelRestrictedByChannel(ctx, *groupID, oauthSelection.Account, requestedModel, false) {
		if oauthSelection.ReleaseFunc != nil {
			oauthSelection.ReleaseFunc()
		}
		return nil, oauthDecision, fmt.Errorf(
			"%w supporting model: %s (channel pricing restriction)",
			ErrNoAvailableAccounts,
			requestedModel,
		)
	}

	return oauthSelection, oauthDecision, nil
}

func supportsChatGPTAudioTranscription(model, responseFormat string) bool {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "gpt-4o-transcribe",
		"gpt-4o-mini-transcribe",
		"gpt-4o-mini-transcribe-2025-03-20",
		"gpt-4o-mini-transcribe-2025-12-15":
	default:
		return false
	}

	switch strings.ToLower(strings.TrimSpace(responseFormat)) {
	case "", "json", "text":
		return true
	default:
		return false
	}
}

func isOpenAIAudioResponseFormatSupported(responseFormat string) bool {
	switch strings.ToLower(strings.TrimSpace(responseFormat)) {
	case "json", "text", "srt", "verbose_json", "vtt", "diarized_json":
		return true
	default:
		return false
	}
}

func resolveAudioTranscriptionUsageModels(account *Account, requestedModel string, channelMapping ChannelMappingResult) (string, string) {
	requestModel := strings.TrimSpace(requestedModel)
	if mapped := strings.TrimSpace(channelMapping.MappedModel); mapped != "" {
		requestModel = mapped
	}

	upstreamModel := requestModel
	if account != nil && account.Type != AccountTypeOAuth {
		upstreamModel = account.GetMappedModel(requestModel)
		if upstreamModel == "" {
			upstreamModel = requestModel
		}
	}
	return requestModel, upstreamModel
}

func isOpenAIAudioTranscriptionTokenUsageModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(model, "gpt-4o-transcribe") || strings.HasPrefix(model, "gpt-4o-mini-transcribe")
}

func audioTranscriptionRequiresPerRequestBilling(account *Account, upstreamModel, responseFormat string) bool {
	if account == nil {
		return false
	}
	if account.Type == AccountTypeOAuth {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(responseFormat)) {
	case "text", "srt", "vtt":
		return true
	}
	return !isOpenAIAudioTranscriptionTokenUsageModel(upstreamModel)
}

func audioTranscriptionHasBillableUsage(usage OpenAIUsage) bool {
	return usage.InputTokens > 0 ||
		usage.AudioInputTokens > 0 ||
		usage.OutputTokens > 0 ||
		usage.CacheCreationInputTokens > 0 ||
		usage.CacheReadInputTokens > 0 ||
		usage.ImageOutputTokens > 0
}

func resolveAudioTranscriptionBillingModel(requestedModel string, channelMapping ChannelMappingResult, upstreamModel string) string {
	requestModel := strings.TrimSpace(channelMapping.MappedModel)
	if requestModel == "" {
		requestModel = strings.TrimSpace(requestedModel)
	}
	usageFields := channelMapping.ToUsageFields(requestedModel, upstreamModel)
	return resolveUsageBillingModel(
		requestModel,
		upstreamModel,
		usageFields.BillingModelSource,
		usageFields.OriginalModel,
		usageFields.ChannelMappedModel,
	)
}

func audioTranscriptionBillingError(billingModel string) error {
	if strings.TrimSpace(billingModel) == "" {
		return fmt.Errorf("audio transcription billing is not configured")
	}
	return fmt.Errorf("audio transcription billing is not configured (model %q)", billingModel)
}

func (s *OpenAIGatewayService) ValidateAudioTranscriptionBilling(
	ctx context.Context,
	apiKey *APIKey,
	_ *UserSubscription,
	account *Account,
	requestedModel string,
	channelMapping ChannelMappingResult,
	responseFormat string,
	audioDuration time.Duration,
) error {
	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		return nil
	}
	_, upstreamModel := resolveAudioTranscriptionUsageModels(account, requestedModel, channelMapping)
	requiresAudioFallback := audioTranscriptionRequiresPerRequestBilling(account, upstreamModel, responseFormat)
	return s.validateAudioTranscriptionPricing(ctx, apiKey, requestedModel, channelMapping, upstreamModel, audioDuration, requiresAudioFallback)
}

// ValidateOAuthAudioTranscriptionBilling ensures balance-billed requests do
// not use ChatGPT's usage-less transcription endpoint with token pricing.
func (s *OpenAIGatewayService) ValidateOAuthAudioTranscriptionBilling(
	ctx context.Context,
	apiKey *APIKey,
	_ *UserSubscription,
	requestedModel string,
	channelMapping ChannelMappingResult,
	audioDuration time.Duration,
) error {
	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		return nil
	}
	upstreamModel := strings.TrimSpace(channelMapping.MappedModel)
	if upstreamModel == "" {
		upstreamModel = strings.TrimSpace(requestedModel)
	}
	return s.validateAudioTranscriptionPricing(ctx, apiKey, requestedModel, channelMapping, upstreamModel, audioDuration, true)
}

func (s *OpenAIGatewayService) validateAudioTranscriptionPricing(
	ctx context.Context,
	apiKey *APIKey,
	requestedModel string,
	channelMapping ChannelMappingResult,
	upstreamModel string,
	audioDuration time.Duration,
	requiresAudioFallback bool,
) error {
	billingModel := resolveAudioTranscriptionBillingModel(requestedModel, channelMapping, upstreamModel)
	if apiKey == nil || apiKey.Group == nil || s.resolver == nil || s.billingService == nil {
		if !requiresAudioFallback {
			if audioDuration <= 0 {
				return fmt.Errorf("audio duration could not be determined for fallback transcription billing")
			}
			return nil
		}
		return audioTranscriptionBillingError(billingModel)
	}
	if !requiresAudioFallback {
		resolved := s.resolveOpenAIChannelPricing(ctx, billingModel, apiKey)
		if resolved == nil || resolved.Mode == BillingModeToken {
			if audioDuration <= 0 {
				return fmt.Errorf("audio duration could not be determined for fallback transcription billing")
			}
			// Token pricing is valid for API-key JSON transcription models that
			// return usage. Do not probe/refresh the full channel cache merely to
			// look for an audio fallback that this request does not need.
			return nil
		}
		if resolved.Mode != BillingModePerRequest && audioDuration <= 0 {
			return fmt.Errorf("audio duration could not be determined for fallback transcription billing")
		}
	}

	resolved := s.resolveAudioTranscriptionChannelPricing(ctx, billingModel, apiKey)
	if resolved != nil && resolved.Mode == BillingModePerRequest {
		return nil
	}
	if resolved == nil && !requiresAudioFallback {
		if audioDuration <= 0 {
			return fmt.Errorf("audio duration could not be determined for fallback transcription billing")
		}
		return nil
	}
	if audioDuration <= 0 {
		return fmt.Errorf("audio duration could not be determined for per-hour transcription billing")
	}
	return nil
}

// resolveAudioTranscriptionChannelPricing accepts either per-request or
// per-hour channel pricing. A failed first lookup forces one database refresh
// so admin pricing changes take effect in other gateway processes as well.
func (s *OpenAIGatewayService) resolveAudioTranscriptionChannelPricing(
	ctx context.Context,
	billingModel string,
	apiKey *APIKey,
) *ResolvedPricing {
	if apiKey == nil || apiKey.Group == nil || s.resolver == nil || s.billingService == nil {
		return nil
	}

	resolve := func() *ResolvedPricing {
		resolved := s.resolveOpenAIChannelPricing(ctx, billingModel, apiKey)
		if resolved == nil {
			return nil
		}
		if resolved.Mode == BillingModePerHour && resolved.DefaultPerRequestPrice > 0 {
			return resolved
		}
		if resolved.Mode != BillingModePerRequest {
			return nil
		}

		gid := apiKey.Group.ID
		cost, err := s.billingService.CalculateCostUnified(CostInput{
			Ctx:            ctx,
			Model:          billingModel,
			GroupID:        &gid,
			RequestCount:   1,
			RateMultiplier: 1,
			Resolver:       s.resolver,
			Resolved:       resolved,
		})
		if err != nil || cost == nil || cost.TotalCost <= 0 {
			return nil
		}
		return resolved
	}

	if resolved := resolve(); resolved != nil {
		return resolved
	}

	if s.channelService != nil && s.shouldRefreshAudioPricing(apiKey, billingModel) {
		if err := s.channelService.forceRefreshCache(ctx); err == nil {
			if resolved := resolve(); resolved != nil {
				return resolved
			}
		}
	}

	return nil
}

func (s *OpenAIGatewayService) shouldRefreshAudioPricing(apiKey *APIKey, billingModel string) bool {
	if s == nil || apiKey == nil || apiKey.Group == nil {
		return false
	}
	key := audioPricingRefreshKey{groupID: apiKey.Group.ID, model: strings.ToLower(strings.TrimSpace(billingModel))}
	now := time.Now()
	if value, ok := s.audioPricingRefreshes.Load(key); ok {
		if last, valid := value.(time.Time); valid && now.Sub(last) < audioPricingRefreshCooldown {
			return false
		}
	}
	s.audioPricingRefreshes.Store(key, now)
	return true
}

func (s *OpenAIGatewayService) calculateAudioTranscriptionCost(
	ctx context.Context,
	billingModel string,
	apiKey *APIKey,
	multiplier float64,
	audioDuration time.Duration,
) (*CostBreakdown, error) {
	if apiKey == nil || apiKey.Group == nil || s.resolver == nil || s.billingService == nil {
		return nil, audioTranscriptionBillingError(billingModel)
	}

	resolved := s.resolveAudioTranscriptionChannelPricing(ctx, billingModel, apiKey)
	if resolved != nil && resolved.Mode == BillingModePerRequest {
		gid := apiKey.Group.ID
		return s.billingService.CalculateCostUnified(CostInput{
			Ctx:            ctx,
			Model:          billingModel,
			GroupID:        &gid,
			RequestCount:   1,
			RateMultiplier: multiplier,
			Resolver:       s.resolver,
			Resolved:       resolved,
		})
	}

	hourlyPrice := defaultAudioTranscriptionPerHourPriceUSD
	if resolved != nil && resolved.Mode == BillingModePerHour {
		hourlyPrice = resolved.DefaultPerRequestPrice
	}
	return calculateAudioTranscriptionHourlyCost(hourlyPrice, audioDuration, multiplier)
}

func calculateAudioTranscriptionHourlyCost(hourlyPrice float64, audioDuration time.Duration, multiplier float64) (*CostBreakdown, error) {
	if hourlyPrice <= 0 {
		return nil, fmt.Errorf("audio transcription hourly price must be positive")
	}
	if audioDuration <= 0 {
		return nil, fmt.Errorf("audio duration could not be determined for per-hour transcription billing")
	}
	if multiplier < 0 {
		multiplier = 0
	}
	totalCost := hourlyPrice * audioDuration.Hours()
	return &CostBreakdown{
		TotalCost:   totalCost,
		ActualCost:  totalCost * multiplier,
		BillingMode: string(BillingModePerHour),
		HourlyPrice: hourlyPrice,
	}, nil
}

func (s *OpenAIGatewayService) ParseOpenAIAudioTranscriptionRequest(c *gin.Context) (*OpenAIAudioTranscriptionRequest, error) {
	if c == nil || c.Request == nil {
		return nil, fmt.Errorf("missing request context")
	}
	if !strings.Contains(strings.TrimSpace(c.Request.URL.Path), "/audio/transcriptions") {
		return nil, fmt.Errorf("unsupported audio transcription endpoint")
	}
	if c.Request.ContentLength > openAIAudioMaxRequestSize {
		return nil, &http.MaxBytesError{Limit: openAIAudioMaxRequestSize}
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, openAIAudioMaxRequestSize)

	contentType := strings.TrimSpace(c.GetHeader("Content-Type"))
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") {
		return nil, fmt.Errorf("Content-Type must be multipart/form-data")
	}
	boundary := strings.TrimSpace(params["boundary"])
	if boundary == "" {
		return nil, fmt.Errorf("multipart boundary is required")
	}

	body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("request body is empty")
	}

	parsed := &OpenAIAudioTranscriptionRequest{
		ContentType:    contentType,
		Body:           body,
		ResponseFormat: "json",
	}
	hash := sha256.Sum256(body)
	parsed.bodyHash = hex.EncodeToString(hash[:])

	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	for {
		part, partErr := reader.NextPart()
		if partErr == io.EOF {
			break
		}
		if partErr != nil {
			return nil, fmt.Errorf("read multipart body: %w", partErr)
		}

		name := strings.TrimSpace(part.FormName())
		fileName := strings.TrimSpace(part.FileName())
		if name == "file" && fileName != "" {
			if parsed.FileName != "" {
				_ = part.Close()
				return nil, fmt.Errorf("only one file upload is supported")
			}
			fileContentType := strings.TrimSpace(part.Header.Get("Content-Type"))
			fileSize, audioDuration, copyErr := inspectAudioTranscriptionPart(part)
			_ = part.Close()
			if copyErr != nil {
				return nil, fmt.Errorf("read audio file: %w", copyErr)
			}
			if fileSize <= 0 {
				return nil, fmt.Errorf("file must not be empty")
			}
			if fileSize > openAIAudioMaxFileSize {
				return nil, fmt.Errorf("file exceeds maximum size of 25 MB: %w", &http.MaxBytesError{Limit: openAIAudioMaxFileSize})
			}
			parsed.FileName = fileName
			parsed.FileContentType = fileContentType
			parsed.FileSize = fileSize
			parsed.AudioDuration = audioDuration
			continue
		}

		field, readErr := io.ReadAll(io.LimitReader(part, openAIAudioMaxFieldSize+1))
		_ = part.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read multipart field %s: %w", name, readErr)
		}
		if len(field) > openAIAudioMaxFieldSize {
			return nil, fmt.Errorf("multipart field %s is too large", name)
		}
		value := strings.TrimSpace(string(field))
		switch name {
		case "model":
			parsed.Model = value
		case "stream":
			switch strings.ToLower(value) {
			case "true":
				parsed.Stream = true
			case "false", "":
				parsed.Stream = false
			default:
				return nil, fmt.Errorf("invalid stream field value")
			}
		case "response_format":
			if value != "" {
				parsed.ResponseFormat = strings.ToLower(value)
			}
		case "language":
			parsed.Language = value
		case "prompt":
			parsed.Prompt = value
		}
	}

	if parsed.FileName == "" {
		return nil, fmt.Errorf("file is required")
	}
	if parsed.Model == "" {
		return nil, fmt.Errorf("model is required")
	}
	if !isOpenAIAudioResponseFormatSupported(parsed.ResponseFormat) {
		return nil, fmt.Errorf("invalid response_format %q", parsed.ResponseFormat)
	}
	return parsed, nil
}

// ParseOpenAIRealtimeVoiceCallRequest accepts only ChatGPT/OpenAI's bounded
// WebRTC signaling form (sdp + session). Audio packets are never part of this
// HTTP request and are sent directly by the negotiated peer connection.
func (s *OpenAIGatewayService) ParseOpenAIRealtimeVoiceCallRequest(
	c *gin.Context,
	explicitMode string,
) (*OpenAIRealtimeVoiceCallRequest, error) {
	if c == nil || c.Request == nil {
		return nil, fmt.Errorf("missing request context")
	}
	if c.Request.ContentLength > realtimeVoiceCallMaxRequestSize {
		return nil, &http.MaxBytesError{Limit: realtimeVoiceCallMaxRequestSize}
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, realtimeVoiceCallMaxRequestSize)

	contentType := strings.TrimSpace(c.GetHeader("Content-Type"))
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") {
		return nil, fmt.Errorf("Content-Type must be multipart/form-data")
	}
	boundary := strings.TrimSpace(params["boundary"])
	if boundary == "" {
		return nil, fmt.Errorf("multipart boundary is required")
	}

	body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("request body is empty")
	}

	parsed := &OpenAIRealtimeVoiceCallRequest{}
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	for {
		part, partErr := reader.NextPart()
		if partErr == io.EOF {
			break
		}
		if partErr != nil {
			return nil, fmt.Errorf("read multipart body: %w", partErr)
		}

		name := strings.TrimSpace(part.FormName())
		if strings.TrimSpace(part.FileName()) != "" {
			_ = part.Close()
			return nil, fmt.Errorf("file uploads are not allowed in realtime signaling")
		}
		var limit int64
		switch name {
		case "sdp":
			if parsed.SDP != nil {
				_ = part.Close()
				return nil, fmt.Errorf("only one sdp field is supported")
			}
			limit = realtimeVoiceSDPMaxSize
		case "session":
			if parsed.Session != nil {
				_ = part.Close()
				return nil, fmt.Errorf("only one session field is supported")
			}
			limit = realtimeVoiceSessionMaxSize
		default:
			_ = part.Close()
			return nil, fmt.Errorf("unsupported realtime multipart field %q", name)
		}

		value, readErr := io.ReadAll(io.LimitReader(part, limit+1))
		_ = part.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read realtime multipart field %s: %w", name, readErr)
		}
		if int64(len(value)) > limit {
			return nil, fmt.Errorf("realtime multipart field %s exceeds %d bytes: %w", name, limit, &http.MaxBytesError{Limit: limit})
		}
		if name == "sdp" {
			parsed.SDP = value
		} else {
			parsed.Session = bytes.TrimSpace(value)
		}
	}

	if strings.TrimSpace(string(parsed.SDP)) == "" {
		return nil, fmt.Errorf("sdp is required")
	}
	if !strings.Contains(string(parsed.SDP), "v=0") {
		return nil, fmt.Errorf("sdp must contain a valid session description")
	}
	if len(parsed.Session) == 0 {
		return nil, fmt.Errorf("session is required")
	}
	if !json.Valid(parsed.Session) || !gjson.ParseBytes(parsed.Session).IsObject() {
		return nil, fmt.Errorf("session must be a valid JSON object")
	}

	root := gjson.ParseBytes(parsed.Session)
	voiceMode := strings.ToLower(strings.TrimSpace(root.Get("voice_mode").String()))
	sessionType := strings.ToLower(strings.TrimSpace(root.Get("session_type").String()))
	upstreamPath, err := resolveChatGPTRealtimeVoicePath(explicitMode, voiceMode, sessionType)
	if err != nil {
		return nil, err
	}
	query, err := sanitizeChatGPTRealtimeVoiceQuery(c.Request.URL.Query())
	if err != nil {
		return nil, err
	}

	parsed.UpstreamPath = upstreamPath
	parsed.Query = query
	for _, modelPath := range []string{"model", "requested_default_model", "model_slug"} {
		if value := strings.TrimSpace(root.Get(modelPath).String()); value != "" {
			parsed.Model = value
			break
		}
	}
	if parsed.Model == "" {
		parsed.Model = OpenAIRealtimeVoiceBillingModel
	}
	return parsed, nil
}

func resolveChatGPTRealtimeVoicePath(explicitMode, voiceMode, sessionType string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(explicitMode))
	if mode == "" {
		switch {
		case sessionType == "wm" || sessionType == "wingman" || voiceMode == "wingman":
			mode = "wm"
		case voiceMode == "standard":
			mode = "vps"
		default:
			mode = "vp"
		}
	}
	switch mode {
	case "vp", "advanced":
		return chatgptRealtimeVoiceAdvancedPath, nil
	case "vps", "standard":
		return chatgptRealtimeVoiceStandardPath, nil
	case "wm", "wingman":
		return chatgptRealtimeVoiceWingmanPath, nil
	default:
		return "", fmt.Errorf("unsupported realtime voice mode %q", explicitMode)
	}
}

func sanitizeChatGPTRealtimeVoiceQuery(input url.Values) (string, error) {
	output := make(url.Values)
	for key, values := range input {
		if len(values) != 1 {
			return "", fmt.Errorf("realtime query parameter %s must be specified once", key)
		}
		value := strings.TrimSpace(values[0])
		switch key {
		case "dcid":
			if value == "" {
				continue
			}
			dcid, err := strconv.ParseUint(value, 10, 16)
			if err != nil {
				return "", fmt.Errorf("invalid realtime dcid")
			}
			output.Set(key, strconv.FormatUint(dcid, 10))
		case "log_call", "instant_connect", "refresh":
			switch strings.ToLower(value) {
			case "1", "true":
				output.Set(key, "1")
			case "", "0", "false":
			default:
				return "", fmt.Errorf("invalid realtime query parameter %s", key)
			}
		case "ufrag":
			if value == "" || len(value) > 128 || !isSafeRealtimeICEUfrag(value) {
				return "", fmt.Errorf("invalid realtime ufrag")
			}
			output.Set(key, value)
		default:
			return "", fmt.Errorf("unsupported realtime query parameter %q", key)
		}
	}
	if output.Get("dcid") == "" {
		output.Set("dcid", "0")
	}
	return output.Encode(), nil
}

func isSafeRealtimeICEUfrag(value string) bool {
	for _, ch := range value {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' {
			continue
		}
		switch ch {
		case '+', '/', '-', '_', '.':
			continue
		default:
			return false
		}
	}
	return true
}

func inspectAudioTranscriptionPart(part io.Reader) (int64, time.Duration, error) {
	tempFile, err := os.CreateTemp("", "sub2api-audio-duration-*")
	if err != nil {
		return 0, 0, fmt.Errorf("create temporary audio file: %w", err)
	}
	tempPath := tempFile.Name()
	defer func() { _ = os.Remove(tempPath) }()
	defer func() { _ = tempFile.Close() }()

	fileSize, err := io.Copy(tempFile, io.LimitReader(part, openAIAudioMaxFileSize+1))
	if err != nil {
		return 0, 0, err
	}
	if fileSize <= 0 || fileSize > openAIAudioMaxFileSize {
		return fileSize, 0, nil
	}
	if _, err := tempFile.Seek(0, io.SeekStart); err != nil {
		return 0, 0, fmt.Errorf("rewind temporary audio file: %w", err)
	}
	return fileSize, detectAudioDurationReader(tempFile, fileSize), nil
}

// ForwardOAuthRealtimeVoiceToken obtains ChatGPT's short-lived voice session
// credentials. This is only the control-plane bootstrap: no audio bytes pass
// through this method or remain resident in gateway memory.
func (s *OpenAIGatewayService) ForwardOAuthRealtimeVoiceToken(
	ctx context.Context,
	c *gin.Context,
	account *Account,
) (*OpenAIRealtimeVoiceTokenResult, error) {
	if account == nil || !account.IsOpenAI() || account.Type != AccountTypeOAuth {
		return nil, fmt.Errorf("an OpenAI OAuth account is required")
	}
	if s.httpUpstream == nil {
		return nil, fmt.Errorf("HTTP upstream is not configured")
	}

	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, err
	}
	upstreamReq, err := buildChatGPTRealtimeVoiceTokenRequest(ctx, c, account, token)
	if err != nil {
		return nil, err
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	upstreamStart := time.Now()
	resp, err := s.httpUpstream.Do(upstreamReq, proxyURL, account.ID, account.Concurrency)
	if c != nil {
		SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
	}
	if err != nil {
		safeErr := sanitizeUpstreamErrorMessage(err.Error())
		if c != nil {
			setOpsUpstreamError(c, 0, safeErr, "")
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:    account.Platform,
				AccountID:   account.ID,
				AccountName: account.Name,
				UpstreamURL: safeUpstreamURL(upstreamReq.URL.String()),
				Kind:        "request_error",
				Message:     safeErr,
			})
		}
		return nil, fmt.Errorf("upstream request failed: %s", safeErr)
	}
	if resp == nil {
		return nil, fmt.Errorf("upstream returned an empty response")
	}
	defer func() { _ = resp.Body.Close() }()

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, realtimeVoiceTokenMaxSize+1))
	if readErr != nil {
		return nil, fmt.Errorf("read realtime voice token response: %w", readErr)
	}
	if len(body) > realtimeVoiceTokenMaxSize {
		return nil, fmt.Errorf("realtime voice token response exceeds %d bytes", realtimeVoiceTokenMaxSize)
	}

	if resp.StatusCode >= http.StatusBadRequest {
		upstreamMsg := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(body)))
		if c != nil {
			setOpsUpstreamError(c, resp.StatusCode, upstreamMsg, resp.Header.Get("x-request-id"))
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: resp.StatusCode,
				UpstreamRequestID:  resp.Header.Get("x-request-id"),
				UpstreamURL:        safeUpstreamURL(upstreamReq.URL.String()),
				Kind:               "failover",
				Message:            upstreamMsg,
			})
		}
		if s.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, upstreamMsg, body) {
			resp.Body = io.NopCloser(bytes.NewReader(body))
			s.handleFailoverSideEffects(ctx, resp, account)
			return nil, &UpstreamFailoverError{
				StatusCode:             resp.StatusCode,
				ResponseBody:           body,
				RetryableOnSameAccount: account.IsPoolMode() && isPoolModeRetryableStatus(resp.StatusCode),
			}
		}
		return nil, fmt.Errorf("ChatGPT voice token upstream returned status %d: %s", resp.StatusCode, upstreamMsg)
	}

	if !json.Valid(body) || strings.TrimSpace(gjson.GetBytes(body, "token").String()) == "" {
		return nil, fmt.Errorf("ChatGPT voice token response is missing a valid token")
	}
	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "application/json"
	}
	return &OpenAIRealtimeVoiceTokenResult{
		Body:            body,
		ContentType:     contentType,
		ResponseHeaders: resp.Header.Clone(),
	}, nil
}

func buildChatGPTRealtimeVoiceTokenRequest(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	token string,
) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, chatgptRealtimeVoiceTokenURL, nil)
	if err != nil {
		return nil, err
	}
	req.Host = "chatgpt.com"
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	if accountID := account.GetChatGPTAccountID(); accountID != "" {
		req.Header.Set("chatgpt-account-id", accountID)
	}
	if c != nil {
		if proofToken := strings.TrimSpace(c.GetHeader("OpenAI-Sentinel-Proof-Token")); proofToken != "" {
			req.Header.Set("OpenAI-Sentinel-Proof-Token", proofToken)
		}
		if originator := strings.TrimSpace(c.GetHeader("Originator")); originator != "" {
			req.Header.Set("Originator", originator)
		}
		if userAgent := strings.TrimSpace(c.GetHeader("User-Agent")); openai.IsCodexCLIRequest(userAgent) {
			req.Header.Set("User-Agent", userAgent)
		}
	}
	if req.Header.Get("Originator") == "" {
		req.Header.Set("Originator", "Codex Desktop")
	}
	if !openai.IsCodexCLIRequest(req.Header.Get("User-Agent")) {
		req.Header.Set("User-Agent", codexCLIUserAgent)
	}
	return req, nil
}

// ForwardOAuthRealtimeVoiceCall proxies only the bounded WebRTC signaling
// exchange. Once the SDP answer is applied by the client, microphone and model
// audio travel directly over ICE/DTLS/SRTP and never cross this service.
func (s *OpenAIGatewayService) ForwardOAuthRealtimeVoiceCall(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	parsed *OpenAIRealtimeVoiceCallRequest,
) (*OpenAIRealtimeVoiceCallResult, error) {
	if parsed == nil {
		return nil, fmt.Errorf("parsed realtime voice call request is required")
	}
	if account == nil || !account.IsOpenAI() || account.Type != AccountTypeOAuth {
		return nil, fmt.Errorf("an OpenAI OAuth account is required")
	}
	if s.httpUpstream == nil {
		return nil, fmt.Errorf("HTTP upstream is not configured")
	}

	requestBody, contentType, err := buildChatGPTRealtimeVoiceCallMultipart(parsed)
	if err != nil {
		return nil, err
	}
	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, err
	}
	upstreamReq, err := buildChatGPTRealtimeVoiceCallRequest(ctx, c, account, parsed, requestBody, contentType, token)
	if err != nil {
		return nil, err
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	upstreamStart := time.Now()
	resp, err := s.httpUpstream.Do(upstreamReq, proxyURL, account.ID, account.Concurrency)
	if c != nil {
		SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
	}
	if err != nil {
		safeErr := sanitizeUpstreamErrorMessage(err.Error())
		if c != nil {
			setOpsUpstreamError(c, 0, safeErr, "")
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:    account.Platform,
				AccountID:   account.ID,
				AccountName: account.Name,
				UpstreamURL: safeUpstreamURL(upstreamReq.URL.String()),
				Kind:        "request_error",
				Message:     safeErr,
			})
		}
		return nil, fmt.Errorf("upstream request failed: %s", safeErr)
	}
	if resp == nil {
		return nil, fmt.Errorf("upstream returned an empty response")
	}
	defer func() { _ = resp.Body.Close() }()

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, realtimeVoiceCallMaxResponseSize+1))
	if readErr != nil {
		return nil, fmt.Errorf("read realtime voice signaling response: %w", readErr)
	}
	if len(body) > realtimeVoiceCallMaxResponseSize {
		return nil, fmt.Errorf("realtime voice signaling response exceeds %d bytes", realtimeVoiceCallMaxResponseSize)
	}

	if resp.StatusCode >= http.StatusBadRequest {
		upstreamMsg := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(body)))
		if c != nil {
			setOpsUpstreamError(c, resp.StatusCode, upstreamMsg, resp.Header.Get("x-request-id"))
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: resp.StatusCode,
				UpstreamRequestID:  resp.Header.Get("x-request-id"),
				UpstreamURL:        safeUpstreamURL(upstreamReq.URL.String()),
				Kind:               "failover",
				Message:            upstreamMsg,
			})
		}
		if s.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, upstreamMsg, body) {
			resp.Body = io.NopCloser(bytes.NewReader(body))
			s.handleFailoverSideEffects(ctx, resp, account)
			return nil, &UpstreamFailoverError{
				StatusCode:             resp.StatusCode,
				ResponseBody:           body,
				RetryableOnSameAccount: account.IsPoolMode() && isPoolModeRetryableStatus(resp.StatusCode),
			}
		}
	}

	responseContentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if responseContentType == "" {
		if resp.StatusCode >= http.StatusBadRequest {
			responseContentType = "application/json"
		} else {
			responseContentType = "application/sdp"
		}
	}
	return &OpenAIRealtimeVoiceCallResult{
		StatusCode:      resp.StatusCode,
		Body:            body,
		ContentType:     responseContentType,
		ResponseHeaders: resp.Header.Clone(),
	}, nil
}

func buildChatGPTRealtimeVoiceCallMultipart(parsed *OpenAIRealtimeVoiceCallRequest) ([]byte, string, error) {
	if parsed == nil {
		return nil, "", fmt.Errorf("parsed realtime voice call request is required")
	}
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	if err := writer.WriteField("sdp", string(parsed.SDP)); err != nil {
		return nil, "", fmt.Errorf("write realtime sdp field: %w", err)
	}
	if err := writer.WriteField("session", string(parsed.Session)); err != nil {
		return nil, "", fmt.Errorf("write realtime session field: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("finalize realtime signaling body: %w", err)
	}
	if buffer.Len() > realtimeVoiceCallMaxRequestSize {
		return nil, "", fmt.Errorf("realtime signaling body exceeds %d bytes", realtimeVoiceCallMaxRequestSize)
	}
	return buffer.Bytes(), writer.FormDataContentType(), nil
}

func buildChatGPTRealtimeVoiceCallRequest(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	parsed *OpenAIRealtimeVoiceCallRequest,
	body []byte,
	contentType string,
	token string,
) (*http.Request, error) {
	targetURL := chatgptRealtimeVoiceBaseURL + parsed.UpstreamPath
	if parsed.Query != "" {
		targetURL += "?" + parsed.Query
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Host = "chatgpt.com"
	req.Header.Set("Accept", "application/sdp, text/plain, application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", contentType)
	if accountID := account.GetChatGPTAccountID(); accountID != "" {
		req.Header.Set("chatgpt-account-id", accountID)
	}
	if c != nil {
		if proofToken := strings.TrimSpace(c.GetHeader("OpenAI-Sentinel-Proof-Token")); proofToken != "" {
			req.Header.Set("OpenAI-Sentinel-Proof-Token", proofToken)
		}
		if originator := strings.TrimSpace(c.GetHeader("Originator")); originator != "" {
			req.Header.Set("Originator", originator)
		}
		if userAgent := strings.TrimSpace(c.GetHeader("User-Agent")); openai.IsCodexCLIRequest(userAgent) {
			req.Header.Set("User-Agent", userAgent)
		}
	}
	if customUA := strings.TrimSpace(account.GetOpenAIUserAgent()); customUA != "" {
		req.Header.Set("User-Agent", customUA)
	}
	if req.Header.Get("Originator") == "" {
		req.Header.Set("Originator", "Codex Desktop")
	}
	if strings.TrimSpace(req.Header.Get("User-Agent")) == "" {
		req.Header.Set("User-Agent", codexCLIUserAgent)
	}
	return req, nil
}

func (s *OpenAIGatewayService) ForwardAudioTranscription(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	parsed *OpenAIAudioTranscriptionRequest,
	channelMappedModel string,
) (*OpenAIForwardResult, error) {
	if parsed == nil {
		return nil, fmt.Errorf("parsed audio transcription request is required")
	}
	if account == nil || !account.IsOpenAI() {
		return nil, fmt.Errorf("an OpenAI account is required")
	}

	startTime := time.Now()
	requestModel, upstreamModel := resolveAudioTranscriptionUsageModels(
		account,
		parsed.Model,
		ChannelMappingResult{MappedModel: channelMappedModel},
	)
	forceAudioBilling := audioTranscriptionRequiresPerRequestBilling(account, upstreamModel, parsed.ResponseFormat)

	var forwardBody []byte
	var forwardContentType string
	var err error
	if account.Type == AccountTypeOAuth {
		if !supportsChatGPTAudioTranscription(requestModel, parsed.ResponseFormat) {
			return nil, fmt.Errorf("codex OAuth audio transcription does not support model %q with response_format %q", requestModel, parsed.ResponseFormat)
		}
		// ChatGPT's Codex dictation endpoint accepts the audio file and optional
		// language only. Keep the public OpenAI fields at this gateway boundary.
		forwardBody, forwardContentType, err = buildChatGPTAudioTranscriptionMultipart(parsed)
	} else if upstreamModel == strings.TrimSpace(parsed.Model) {
		forwardBody = parsed.Body
		forwardContentType = parsed.ContentType
	} else {
		forwardBody, forwardContentType, err = rewriteOpenAIImagesMultipartModel(parsed.Body, parsed.ContentType, upstreamModel)
	}
	if err != nil {
		return nil, fmt.Errorf("prepare transcription request: %w", err)
	}
	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, err
	}
	upstreamReq, err := s.buildOpenAIAudioTranscriptionRequest(ctx, c, account, forwardBody, forwardContentType, token)
	if err != nil {
		return nil, err
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	upstreamStart := time.Now()
	resp, err := s.httpUpstream.Do(upstreamReq, proxyURL, account.ID, account.Concurrency)
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
	if err != nil {
		safeErr := sanitizeUpstreamErrorMessage(err.Error())
		setOpsUpstreamError(c, 0, safeErr, "")
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: 0,
			UpstreamURL:        safeUpstreamURL(upstreamReq.URL.String()),
			Kind:               "request_error",
			Message:            safeErr,
		})
		return nil, fmt.Errorf("upstream request failed: %s", safeErr)
	}

	if resp.StatusCode >= http.StatusBadRequest {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		upstreamMsg := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(respBody)))
		if s.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, upstreamMsg, respBody) {
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: resp.StatusCode,
				UpstreamRequestID:  resp.Header.Get("x-request-id"),
				UpstreamURL:        safeUpstreamURL(upstreamReq.URL.String()),
				Kind:               "failover",
				Message:            upstreamMsg,
			})
			s.handleFailoverSideEffects(ctx, resp, account)
			return nil, &UpstreamFailoverError{
				StatusCode:             resp.StatusCode,
				ResponseBody:           respBody,
				RetryableOnSameAccount: account.IsPoolMode() && isPoolModeRetryableStatus(resp.StatusCode),
			}
		}
		return s.handleErrorResponse(ctx, resp, c, account, nil)
	}
	defer func() { _ = resp.Body.Close() }()

	var (
		usage        OpenAIUsage
		firstTokenMs *int
	)
	if parsed.Stream && isEventStreamResponse(resp.Header) {
		usage, firstTokenMs, err = s.handleOpenAIAudioTranscriptionStreamingResponse(resp, c, startTime)
	} else {
		usage, firstTokenMs, err = s.handleOpenAIAudioTranscriptionBufferedResponse(resp, c, account.Type == AccountTypeOAuth, parsed, startTime)
	}
	if err != nil {
		return nil, err
	}
	if !forceAudioBilling && !audioTranscriptionHasBillableUsage(usage) {
		forceAudioBilling = true
	}

	return &OpenAIForwardResult{
		RequestID:         resp.Header.Get("x-request-id"),
		Usage:             usage,
		Model:             requestModel,
		UpstreamModel:     upstreamModel,
		Stream:            parsed.Stream,
		ResponseHeaders:   resp.Header.Clone(),
		Duration:          time.Since(startTime),
		AudioDuration:     parsed.AudioDuration,
		FirstTokenMs:      firstTokenMs,
		ForceUsageRecord:  true,
		ForceAudioBilling: forceAudioBilling,
	}, nil
}

func buildChatGPTAudioTranscriptionMultipart(parsed *OpenAIAudioTranscriptionRequest) ([]byte, string, error) {
	if parsed == nil {
		return nil, "", fmt.Errorf("parsed audio transcription request is required")
	}
	_, params, err := mime.ParseMediaType(parsed.ContentType)
	if err != nil {
		return nil, "", fmt.Errorf("parse multipart content-type: %w", err)
	}
	boundary := strings.TrimSpace(params["boundary"])
	if boundary == "" {
		return nil, "", fmt.Errorf("multipart boundary is required")
	}

	reader := multipart.NewReader(bytes.NewReader(parsed.Body), boundary)
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	fileWritten := false
	for {
		part, partErr := reader.NextPart()
		if partErr == io.EOF {
			break
		}
		if partErr != nil {
			return nil, "", fmt.Errorf("read multipart body: %w", partErr)
		}

		if strings.TrimSpace(part.FormName()) != "file" || strings.TrimSpace(part.FileName()) == "" {
			_ = part.Close()
			continue
		}
		if fileWritten {
			_ = part.Close()
			return nil, "", fmt.Errorf("only one file upload is supported")
		}
		target, createErr := writer.CreatePart(cloneMultipartHeader(part.Header))
		if createErr != nil {
			_ = part.Close()
			return nil, "", fmt.Errorf("create audio file part: %w", createErr)
		}
		if _, copyErr := io.Copy(target, part); copyErr != nil {
			_ = part.Close()
			return nil, "", fmt.Errorf("copy audio file part: %w", copyErr)
		}
		_ = part.Close()
		fileWritten = true
	}
	if !fileWritten {
		return nil, "", fmt.Errorf("file is required")
	}
	if parsed.Language != "" {
		if err := writer.WriteField("language", parsed.Language); err != nil {
			return nil, "", fmt.Errorf("write language field: %w", err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("finalize multipart body: %w", err)
	}
	return buffer.Bytes(), writer.FormDataContentType(), nil
}

func (s *OpenAIGatewayService) buildOpenAIAudioTranscriptionRequest(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	contentType string,
	token string,
) (*http.Request, error) {
	targetURL := openAIAudioTranscriptionsURL
	if account.Type == AccountTypeOAuth {
		targetURL = chatgptAudioTranscriptionsURL
	} else {
		baseURL := account.GetOpenAIBaseURL()
		if baseURL != "" {
			validatedURL, err := s.validateUpstreamBaseURL(baseURL)
			if err != nil {
				return nil, err
			}
			targetURL = buildOpenAIImagesURL(validatedURL, openAIAudioTranscriptionsEndpoint)
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for key, values := range c.Request.Header {
		if !openaiPassthroughAllowedHeaders[strings.ToLower(key)] {
			continue
		}
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	req.Header.Del("Authorization")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", contentType)

	customUA := account.GetOpenAIUserAgent()
	if customUA != "" {
		req.Header.Set("User-Agent", customUA)
	}
	if account.Type == AccountTypeOAuth {
		req.Host = "chatgpt.com"
		if accountID := account.GetChatGPTAccountID(); accountID != "" {
			req.Header.Set("chatgpt-account-id", accountID)
		}
		if req.Header.Get("Originator") == "" {
			req.Header.Set("Originator", "Codex Desktop")
		}
		if !openai.IsCodexCLIRequest(req.Header.Get("User-Agent")) {
			req.Header.Set("User-Agent", codexCLIUserAgent)
		}
	}
	return req, nil
}

func (s *OpenAIGatewayService) handleOpenAIAudioTranscriptionBufferedResponse(
	resp *http.Response,
	c *gin.Context,
	oauth bool,
	parsed *OpenAIAudioTranscriptionRequest,
	startTime time.Time,
) (OpenAIUsage, *int, error) {
	body, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return OpenAIUsage{}, nil, err
	}
	usage, _ := extractOpenAIUsageFromJSONBytes(body)

	if parsed.Stream && oauth {
		text, ok := extractOpenAITranscriptionText(body)
		if !ok {
			return OpenAIUsage{}, nil, fmt.Errorf("ChatGPT transcription response did not contain text")
		}
		ms := int(time.Since(startTime).Milliseconds())
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
		c.Header("Content-Type", "text/event-stream")
		c.Status(resp.StatusCode)
		if err := writeOpenAITranscriptionSSE(c.Writer, text, usage); err != nil {
			return OpenAIUsage{}, &ms, err
		}
		return usage, &ms, nil
	}

	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if oauth && strings.EqualFold(parsed.ResponseFormat, "text") {
		text, ok := extractOpenAITranscriptionText(body)
		if !ok {
			return OpenAIUsage{}, nil, fmt.Errorf("ChatGPT transcription response did not contain text")
		}
		body = []byte(text)
		contentType = "text/plain; charset=utf-8"
	}
	if contentType == "" {
		if strings.EqualFold(parsed.ResponseFormat, "text") {
			contentType = "text/plain; charset=utf-8"
		} else {
			contentType = "application/json"
		}
	}
	responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	c.Header("Content-Type", contentType)
	c.Data(resp.StatusCode, contentType, body)
	return usage, nil, nil
}

func (s *OpenAIGatewayService) handleOpenAIAudioTranscriptionStreamingResponse(
	resp *http.Response,
	c *gin.Context,
	startTime time.Time,
) (OpenAIUsage, *int, error) {
	responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "text/event-stream"
	}
	c.Header("Content-Type", contentType)
	c.Status(resp.StatusCode)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return OpenAIUsage{}, nil, fmt.Errorf("streaming is not supported by response writer")
	}
	reader := bufio.NewReader(resp.Body)
	usage := OpenAIUsage{}
	var firstTokenMs *int
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			if firstTokenMs == nil {
				ms := int(time.Since(startTime).Milliseconds())
				firstTokenMs = &ms
			}
			if _, err := c.Writer.Write(line); err != nil {
				return OpenAIUsage{}, firstTokenMs, err
			}
			flusher.Flush()
			if data, ok := extractOpenAISSEDataLine(strings.TrimRight(string(line), "\r\n")); ok && data != "" && data != "[DONE]" {
				mergeOpenAIUsage(&usage, []byte(data))
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return OpenAIUsage{}, firstTokenMs, readErr
		}
	}
	return usage, firstTokenMs, nil
}

func extractOpenAITranscriptionText(body []byte) (string, bool) {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return "", false
	}
	for _, path := range []string{"text", "transcript", "result.text"} {
		if text := gjson.GetBytes(body, path); text.Exists() && text.Type == gjson.String {
			return text.String(), true
		}
	}
	return "", false
}

func writeOpenAITranscriptionSSE(w io.Writer, transcript string, usage OpenAIUsage) error {
	delta, err := json.Marshal(map[string]any{
		"type":  "transcript.text.delta",
		"delta": transcript,
	})
	if err != nil {
		return err
	}
	donePayload := map[string]any{
		"type": "transcript.text.done",
		"text": transcript,
	}
	if usage.InputTokens > 0 || usage.OutputTokens > 0 {
		donePayload["usage"] = usage
	}
	done, err := json.Marshal(donePayload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: transcript.text.delta\ndata: %s\n\n", delta); err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: transcript.text.done\ndata: %s\n\n", done)
	return err
}
