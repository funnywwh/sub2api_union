package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	openAIEmbeddingsEndpoint = "/v1/embeddings"
	openAIEmbeddingsURL      = "https://api.openai.com/v1/embeddings"
)

var openAIEmbeddingsAllowedHeaders = map[string]bool{
	"accept":          true,
	"accept-language": true,
	"content-type":    true,
	"user-agent":      true,
}

// SelectAccountWithSchedulerForEmbeddings keeps normal model-aware routing for
// Platform API key accounts, then falls back to Codex OAuth without applying
// the account's text-generation model mapping as an embeddings allowlist.
func (s *OpenAIGatewayService) SelectAccountWithSchedulerForEmbeddings(
	ctx context.Context,
	groupID *int64,
	sessionHash string,
	requestedModel string,
	channelMappedModel string,
	excludedIDs map[int64]struct{},
) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	if s.checkChannelPricingRestriction(ctx, groupID, requestedModel) {
		return nil, OpenAIAccountScheduleDecision{}, fmt.Errorf(
			"%w supporting model: %s (channel pricing restriction)",
			ErrNoAvailableAccounts,
			requestedModel,
		)
	}

	routingModel := strings.TrimSpace(channelMappedModel)
	if routingModel == "" {
		routingModel = strings.TrimSpace(requestedModel)
	}

	selection, decision, strictErr := s.SelectAccountWithScheduler(
		ctx,
		groupID,
		"",
		sessionHash,
		routingModel,
		excludedIDs,
		OpenAIUpstreamTransportHTTPSSE,
		false,
		AccountTypeAPIKey,
	)
	if strictErr == nil && selection != nil && selection.Account != nil {
		return selection, decision, nil
	}

	// Codex OAuth tokens are accepted by the OpenAI Platform embeddings API,
	// but the account's generic model_mapping describes Codex text models. An
	// empty requested model bypasses that unrelated allowlist during selection.
	oauthSelection, oauthDecision, oauthErr := s.SelectAccountWithScheduler(
		ctx,
		groupID,
		"",
		sessionHash,
		"",
		excludedIDs,
		OpenAIUpstreamTransportHTTPSSE,
		false,
		AccountTypeOAuth,
	)
	if oauthErr != nil || oauthSelection == nil || oauthSelection.Account == nil {
		if oauthErr != nil {
			return nil, oauthDecision, oauthErr
		}
		return nil, decision, strictErr
	}

	// Upstream-based channel restrictions must inspect the actual embedding
	// model directly. resolveOpenAIAccountUpstreamModelForRequest would apply
	// the OAuth account's generic GPT model mapping and corrupt this check.
	if groupID != nil && s.needsUpstreamChannelRestrictionCheck(ctx, groupID) &&
		s.IsModelRestricted(ctx, *groupID, routingModel) {
		if oauthSelection.ReleaseFunc != nil {
			oauthSelection.ReleaseFunc()
		}
		return nil, oauthDecision, fmt.Errorf(
			"%w supporting model: %s (channel pricing restriction)",
			ErrNoAvailableAccounts,
			routingModel,
		)
	}

	return oauthSelection, oauthDecision, nil
}

// ForwardEmbeddings forwards an OpenAI-compatible embeddings request. API key
// accounts may use their configured base URL and model mapping. OAuth accounts
// always use the official Platform endpoint and preserve the embedding model,
// except for an explicit channel-level mapping supplied by the handler.
func (s *OpenAIGatewayService) ForwardEmbeddings(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	channelMappedModel string,
) (*OpenAIForwardResult, error) {
	startTime := time.Now()
	if account == nil {
		return nil, errors.New("account is required")
	}

	originalModel := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	if originalModel == "" {
		return nil, errors.New("model is required")
	}

	requestModel := strings.TrimSpace(channelMappedModel)
	if requestModel == "" {
		requestModel = originalModel
	}
	upstreamModel := requestModel
	if account.Type == AccountTypeAPIKey {
		upstreamModel = strings.TrimSpace(account.GetMappedModel(requestModel))
	}
	if upstreamModel == "" {
		return nil, errors.New("resolved embeddings model is empty")
	}

	upstreamBody := body
	if upstreamModel != originalModel {
		upstreamBody = ReplaceModelInBody(body, upstreamModel)
	}
	setOpsUpstreamRequestBody(c, upstreamBody)

	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, err
	}
	upstreamReq, err := s.buildOpenAIEmbeddingsRequest(ctx, c, account, upstreamBody, token)
	if err != nil {
		return nil, err
	}
	if s.httpUpstream == nil {
		return nil, errors.New("openai embeddings upstream is unavailable")
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
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= http.StatusBadRequest {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(respBody))
		upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)
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
			if s.rateLimitService != nil {
				s.rateLimitService.HandleUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody)
			}
			return nil, &UpstreamFailoverError{
				StatusCode:             resp.StatusCode,
				ResponseBody:           respBody,
				RetryableOnSameAccount: account.IsPoolMode() && isPoolModeRetryableStatus(resp.StatusCode),
			}
		}
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		writeOpenAIEmbeddingsUpstreamResponse(c, resp, respBody, s.responseHeaderFilter)
		return nil, fmt.Errorf("upstream returned status %d", resp.StatusCode)
	}

	respBody, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		if !errors.Is(err, ErrUpstreamResponseBodyTooLarge) && c != nil && !c.Writer.Written() {
			c.JSON(http.StatusBadGateway, gin.H{
				"error": gin.H{
					"type":    "api_error",
					"message": "Failed to read upstream response",
				},
			})
		}
		return nil, fmt.Errorf("read upstream body: %w", err)
	}

	writeOpenAIEmbeddingsUpstreamResponse(c, resp, respBody, s.responseHeaderFilter)
	return &OpenAIForwardResult{
		RequestID:       firstNonEmptyString(resp.Header.Get("x-request-id"), resp.Header.Get("request-id")),
		Usage:           extractOpenAIEmbeddingsUsage(respBody),
		Model:           originalModel,
		BillingModel:    upstreamModel,
		UpstreamModel:   upstreamModel,
		Stream:          false,
		ResponseHeaders: resp.Header.Clone(),
		Duration:        time.Since(startTime),
	}, nil
}

func (s *OpenAIGatewayService) buildOpenAIEmbeddingsRequest(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	token string,
) (*http.Request, error) {
	targetURL := openAIEmbeddingsURL
	if account.Type == AccountTypeAPIKey {
		if baseURL := strings.TrimSpace(account.GetOpenAIBaseURL()); baseURL != "" {
			validatedURL, err := s.validateUpstreamBaseURL(baseURL)
			if err != nil {
				return nil, fmt.Errorf("invalid base_url: %w", err)
			}
			targetURL = buildOpenAIEmbeddingsURL(validatedURL)
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build upstream request: %w", err)
	}
	if c != nil && c.Request != nil {
		for key, values := range c.Request.Header {
			if !openAIEmbeddingsAllowedHeaders[strings.ToLower(key)] {
				continue
			}
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	if customUA := strings.TrimSpace(account.GetOpenAIUserAgent()); customUA != "" {
		req.Header.Set("User-Agent", customUA)
	} else if account.Type == AccountTypeOAuth {
		req.Header.Set("User-Agent", codexCLIUserAgent)
	}
	return req, nil
}

func buildOpenAIEmbeddingsURL(base string) string {
	normalized := strings.TrimRight(strings.TrimSpace(base), "/")
	relative := strings.TrimPrefix(openAIEmbeddingsEndpoint, "/v1")
	if strings.HasSuffix(normalized, openAIEmbeddingsEndpoint) || strings.HasSuffix(normalized, relative) {
		return normalized
	}
	if strings.HasSuffix(normalized, "/v1") {
		return normalized + relative
	}
	return normalized + openAIEmbeddingsEndpoint
}

func writeOpenAIEmbeddingsUpstreamResponse(c *gin.Context, resp *http.Response, body []byte, filter *responseheaders.CompiledHeaderFilter) {
	if c == nil || resp == nil || c.Writer.Written() {
		return
	}
	if resp.Header != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, filter)
	}
	if contentType := resp.Header.Get("Content-Type"); contentType != "" {
		c.Writer.Header().Set("Content-Type", contentType)
	} else {
		c.Writer.Header().Set("Content-Type", "application/json")
	}
	c.Writer.WriteHeader(resp.StatusCode)
	_, _ = c.Writer.Write(body)
}

func extractOpenAIEmbeddingsUsage(body []byte) OpenAIUsage {
	usage := gjson.GetBytes(body, "usage")
	if !usage.Exists() || !usage.IsObject() {
		return OpenAIUsage{}
	}
	inputTokens := firstPositiveGJSONInt(
		usage.Get("prompt_tokens"),
		usage.Get("input_tokens"),
		usage.Get("total_tokens"),
	)
	return OpenAIUsage{InputTokens: inputTokens}
}

func firstPositiveGJSONInt(values ...gjson.Result) int {
	for _, value := range values {
		if !value.Exists() {
			continue
		}
		if n := int(value.Int()); n > 0 {
			return n
		}
	}
	return 0
}
