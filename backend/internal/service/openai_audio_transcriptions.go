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

	openAIAudioMaxFieldSize   = 1 << 20
	openAIAudioMaxFileSize    = 25 << 20
	openAIAudioMaxRequestSize = 27 << 20
)

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
	bodyHash        string
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

func audioTranscriptionPerRequestBillingError(billingModel string) error {
	if strings.TrimSpace(billingModel) == "" {
		return fmt.Errorf("audio transcription requires positive per-request channel pricing for balance billing")
	}
	return fmt.Errorf("audio transcription requires positive per-request channel pricing for balance billing (model %q)", billingModel)
}

func (s *OpenAIGatewayService) ValidateAudioTranscriptionBilling(
	ctx context.Context,
	apiKey *APIKey,
	_ *UserSubscription,
	account *Account,
	requestedModel string,
	channelMapping ChannelMappingResult,
	responseFormat string,
) error {
	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		return nil
	}
	_, upstreamModel := resolveAudioTranscriptionUsageModels(account, requestedModel, channelMapping)
	if !audioTranscriptionRequiresPerRequestBilling(account, upstreamModel, responseFormat) {
		return nil
	}
	return s.validatePositivePerRequestAudioTranscriptionPricing(ctx, apiKey, requestedModel, channelMapping, upstreamModel)
}

// ValidateOAuthAudioTranscriptionBilling ensures balance-billed requests do
// not use ChatGPT's usage-less transcription endpoint with token pricing.
func (s *OpenAIGatewayService) ValidateOAuthAudioTranscriptionBilling(
	ctx context.Context,
	apiKey *APIKey,
	_ *UserSubscription,
	requestedModel string,
	channelMapping ChannelMappingResult,
) error {
	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		return nil
	}
	upstreamModel := strings.TrimSpace(channelMapping.MappedModel)
	if upstreamModel == "" {
		upstreamModel = strings.TrimSpace(requestedModel)
	}
	return s.validatePositivePerRequestAudioTranscriptionPricing(ctx, apiKey, requestedModel, channelMapping, upstreamModel)
}

func (s *OpenAIGatewayService) validatePositivePerRequestAudioTranscriptionPricing(
	ctx context.Context,
	apiKey *APIKey,
	requestedModel string,
	channelMapping ChannelMappingResult,
	upstreamModel string,
) error {
	billingModel := resolveAudioTranscriptionBillingModel(requestedModel, channelMapping, upstreamModel)
	if apiKey == nil || apiKey.Group == nil || s.resolver == nil || s.billingService == nil {
		return audioTranscriptionPerRequestBillingError(billingModel)
	}

	resolved := s.resolveOpenAIChannelPricing(ctx, billingModel, apiKey)
	if resolved == nil || resolved.Mode != BillingModePerRequest {
		return audioTranscriptionPerRequestBillingError(billingModel)
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
		return audioTranscriptionPerRequestBillingError(billingModel)
	}
	return nil
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
		return nil, fmt.Errorf("Request body is empty")
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
			fileSize, copyErr := io.Copy(io.Discard, io.LimitReader(part, openAIAudioMaxFileSize+1))
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
			parsed.FileContentType = strings.TrimSpace(part.Header.Get("Content-Type"))
			parsed.FileSize = fileSize
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
	forcePerRequestBilling := audioTranscriptionRequiresPerRequestBilling(account, upstreamModel, parsed.ResponseFormat)

	var forwardBody []byte
	var forwardContentType string
	var err error
	if account.Type == AccountTypeOAuth {
		if !supportsChatGPTAudioTranscription(requestModel, parsed.ResponseFormat) {
			return nil, fmt.Errorf("Codex OAuth audio transcription does not support model %q with response_format %q", requestModel, parsed.ResponseFormat)
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
	if !forcePerRequestBilling && !audioTranscriptionHasBillableUsage(usage) {
		forcePerRequestBilling = true
	}

	return &OpenAIForwardResult{
		RequestID:              resp.Header.Get("x-request-id"),
		Usage:                  usage,
		Model:                  requestModel,
		UpstreamModel:          upstreamModel,
		Stream:                 parsed.Stream,
		ResponseHeaders:        resp.Header.Clone(),
		Duration:               time.Since(startTime),
		FirstTokenMs:           firstTokenMs,
		ForceUsageRecord:       true,
		ForcePerRequestBilling: forcePerRequestBilling,
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
