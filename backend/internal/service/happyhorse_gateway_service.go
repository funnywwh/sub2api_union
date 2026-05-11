package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
	"github.com/tidwall/gjson"
)

const (
	HappyHorseDefaultBaseURL = "https://happyhorse.app"
	HappyHorseDefaultModel   = "happyhorse-1.0/video"
)

type HappyHorseGatewayService struct {
	accountRepo  AccountRepository
	taskRepo     HappyHorseTaskRepository
	httpUpstream HTTPUpstream
	cfg          *config.Config
}

type HappyHorseGenerateRequest struct {
	Body               []byte
	Model              string
	Prompt             string
	Duration           int
	Mode               string
	AspectRatio        string
	RequestPayloadHash string
}

func (r *HappyHorseGenerateRequest) BillingTier() string {
	if r == nil {
		return ""
	}
	parts := make([]string, 0, 3)
	if mode := strings.TrimSpace(r.Mode); mode != "" {
		parts = append(parts, mode)
	}
	if r.Duration > 0 {
		parts = append(parts, fmt.Sprintf("%ds", r.Duration))
	}
	if aspect := strings.TrimSpace(r.AspectRatio); aspect != "" {
		parts = append(parts, strings.ReplaceAll(aspect, ":", "x"))
	}
	return strings.Join(parts, "_")
}

func (r *HappyHorseGenerateRequest) WithModel(model string) (*HappyHorseGenerateRequest, error) {
	if r == nil {
		return nil, errors.New("happyhorse request is nil")
	}
	mapped := strings.TrimSpace(model)
	if mapped == "" || mapped == r.Model {
		return r, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(r.Body, &payload); err != nil {
		return nil, err
	}
	payload["model"] = mapped
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	cp := *r
	cp.Body = body
	cp.Model = mapped
	return &cp, nil
}

type HappyHorseForwardResult struct {
	StatusCode      int
	Body            []byte
	Headers         http.Header
	RequestID       string
	TaskID          string
	Model           string
	Duration        time.Duration
	Status          string
	ResultURLs      []string
	ErrorMessage    string
	UpstreamPayload map[string]any
}

type HappyHorseHTTPError struct {
	StatusCode int
	Body       []byte
	Headers    http.Header
	Message    string
}

func (e *HappyHorseHTTPError) Error() string {
	if e == nil {
		return "happyhorse upstream error"
	}
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("happyhorse upstream error: %d", e.StatusCode)
}

func NewHappyHorseGatewayService(accountRepo AccountRepository, taskRepo HappyHorseTaskRepository, httpUpstream HTTPUpstream, cfg *config.Config) *HappyHorseGatewayService {
	return &HappyHorseGatewayService{
		accountRepo:  accountRepo,
		taskRepo:     taskRepo,
		httpUpstream: httpUpstream,
		cfg:          cfg,
	}
}

func (s *HappyHorseGatewayService) ParseGenerateRequest(body []byte) (*HappyHorseGenerateRequest, error) {
	if len(body) == 0 {
		return nil, errors.New("request body is empty")
	}
	if !gjson.ValidBytes(body) {
		return nil, errors.New("failed to parse request body")
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	prompt := extractHappyHorsePrompt(body)
	if prompt == "" {
		return nil, errors.New("prompt or multi_prompt prompts are required")
	}
	model := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	if model == "" {
		model = HappyHorseDefaultModel
		payload["model"] = model
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			return nil, err
		}
	}
	duration := 0
	if result := gjson.GetBytes(body, "duration"); result.Exists() {
		if result.Type != gjson.Number {
			return nil, errors.New("duration must be a number")
		}
		duration = int(result.Int())
		if duration < 0 {
			return nil, errors.New("duration must be greater than or equal to 0")
		}
	}
	sum := sha256.Sum256(body)
	return &HappyHorseGenerateRequest{
		Body:               body,
		Model:              model,
		Prompt:             prompt,
		Duration:           duration,
		Mode:               strings.TrimSpace(gjson.GetBytes(body, "mode").String()),
		AspectRatio:        strings.TrimSpace(gjson.GetBytes(body, "aspect_ratio").String()),
		RequestPayloadHash: hex.EncodeToString(sum[:]),
	}, nil
}

func (s *HappyHorseGatewayService) ForwardGenerate(ctx context.Context, account *Account, req *HappyHorseGenerateRequest) (*HappyHorseForwardResult, error) {
	if account == nil {
		return nil, errors.New("account is nil")
	}
	if req == nil {
		return nil, errors.New("request is nil")
	}
	if err := validateHappyHorseAccount(account); err != nil {
		return nil, err
	}
	startedAt := time.Now()
	resp, err := s.doHappyHorseRequest(ctx, account, http.MethodPost, "/api/generate", "", req.Body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if readErr != nil {
		return nil, readErr
	}
	requestID := happyHorseRequestID(resp.Header)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, s.toUpstreamError(resp.StatusCode, respBody, resp.Header)
	}
	parsed := parseHappyHorsePayload(respBody)
	if parsed.Model == "" {
		parsed.Model = req.Model
	}
	return &HappyHorseForwardResult{
		StatusCode:      resp.StatusCode,
		Body:            respBody,
		Headers:         resp.Header.Clone(),
		RequestID:       requestID,
		TaskID:          parsed.TaskID,
		Model:           parsed.Model,
		Duration:        time.Since(startedAt),
		Status:          parsed.Status,
		ResultURLs:      parsed.ResultURLs,
		ErrorMessage:    parsed.ErrorMessage,
		UpstreamPayload: parsed.Payload,
	}, nil
}

func (s *HappyHorseGatewayService) ForwardStatus(ctx context.Context, account *Account, taskID string) (*HappyHorseForwardResult, error) {
	if account == nil {
		return nil, errors.New("account is nil")
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, errors.New("task_id is required")
	}
	if err := validateHappyHorseAccount(account); err != nil {
		return nil, err
	}
	startedAt := time.Now()
	query := "?task_id=" + url.QueryEscape(taskID)
	resp, err := s.doHappyHorseRequest(ctx, account, http.MethodGet, "/api/status", query, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if readErr != nil {
		return nil, readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HappyHorseHTTPError{StatusCode: resp.StatusCode, Body: respBody, Headers: resp.Header.Clone(), Message: ExtractUpstreamErrorMessage(respBody)}
	}
	parsed := parseHappyHorsePayload(respBody)
	if parsed.TaskID == "" {
		parsed.TaskID = taskID
	}
	return &HappyHorseForwardResult{
		StatusCode:      resp.StatusCode,
		Body:            respBody,
		Headers:         resp.Header.Clone(),
		RequestID:       happyHorseRequestID(resp.Header),
		TaskID:          parsed.TaskID,
		Model:           parsed.Model,
		Duration:        time.Since(startedAt),
		Status:          parsed.Status,
		ResultURLs:      parsed.ResultURLs,
		ErrorMessage:    parsed.ErrorMessage,
		UpstreamPayload: parsed.Payload,
	}, nil
}

func (s *HappyHorseGatewayService) CreateTask(ctx context.Context, task *HappyHorseTask) error {
	if s.taskRepo == nil {
		return nil
	}
	return s.taskRepo.Create(ctx, task)
}

func (s *HappyHorseGatewayService) GetTaskByTaskID(ctx context.Context, taskID string) (*HappyHorseTask, error) {
	if s.taskRepo == nil {
		return nil, ErrHappyHorseTaskNotFound
	}
	return s.taskRepo.GetByTaskID(ctx, strings.TrimSpace(taskID))
}

func (s *HappyHorseGatewayService) UpdateTaskFromStatus(ctx context.Context, taskID string, result *HappyHorseForwardResult) error {
	if s.taskRepo == nil || result == nil {
		return nil
	}
	status := normalizeHappyHorseStatus(result.Status)
	var completedAt *time.Time
	switch status {
	case "success", "failed", "cancelled":
		now := time.Now()
		completedAt = &now
	}
	return s.taskRepo.UpdateStatus(ctx, taskID, HappyHorseTaskStatusUpdate{
		Status:           status,
		ResultURLs:       result.ResultURLs,
		ErrorMessage:     result.ErrorMessage,
		UpstreamResponse: result.UpstreamPayload,
		CompletedAt:      completedAt,
	})
}

func (s *HappyHorseGatewayService) ClaimTaskUsageRecording(ctx context.Context, taskID string) (bool, error) {
	if s.taskRepo == nil {
		return false, nil
	}
	return s.taskRepo.ClaimUsageRecording(ctx, strings.TrimSpace(taskID))
}

func (s *HappyHorseGatewayService) MarkTaskUsageRecorded(ctx context.Context, taskID string) error {
	if s.taskRepo == nil {
		return nil
	}
	return s.taskRepo.MarkUsageRecorded(ctx, strings.TrimSpace(taskID))
}

func (s *HappyHorseGatewayService) ResetTaskUsageRecording(ctx context.Context, taskID string) error {
	if s.taskRepo == nil {
		return nil
	}
	return s.taskRepo.ResetUsageRecording(ctx, strings.TrimSpace(taskID))
}

func (s *HappyHorseGatewayService) GetAccountByID(ctx context.Context, accountID int64) (*Account, error) {
	return s.accountRepo.GetByID(ctx, accountID)
}

func (s *HappyHorseGatewayService) doHappyHorseRequest(ctx context.Context, account *Account, method, path, rawQuery string, body []byte) (*http.Response, error) {
	baseURL := account.GetHappyHorseBaseURL()
	validatedBaseURL, err := s.validateUpstreamBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	targetURL := strings.TrimRight(validatedBaseURL, "/") + path + rawQuery
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	upstreamReq, err := http.NewRequestWithContext(ctx, method, targetURL, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		upstreamReq.Header.Set("Content-Type", "application/json")
	}
	upstreamReq.Header.Set("Accept", "application/json")
	upstreamReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(account.GetHappyHorseAPIKey()))
	upstreamReq.Header.Set("User-Agent", "Sub2API-HappyHorse/1.0")
	var proxyURL string
	if account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	return s.httpUpstream.Do(upstreamReq, proxyURL, account.ID, account.Concurrency)
}

func (s *HappyHorseGatewayService) validateUpstreamBaseURL(raw string) (string, error) {
	if s.cfg != nil && !s.cfg.Security.URLAllowlist.Enabled {
		normalized, err := urlvalidator.ValidateURLFormat(raw, s.cfg.Security.URLAllowlist.AllowInsecureHTTP)
		if err != nil {
			return "", fmt.Errorf("invalid base_url: %w", err)
		}
		return normalized, nil
	}
	opts := urlvalidator.ValidationOptions{}
	if s.cfg != nil {
		opts = urlvalidator.ValidationOptions{
			AllowedHosts:     s.cfg.Security.URLAllowlist.UpstreamHosts,
			RequireAllowlist: true,
			AllowPrivate:     s.cfg.Security.URLAllowlist.AllowPrivateHosts,
		}
	}
	normalized, err := urlvalidator.ValidateHTTPSURL(raw, opts)
	if err != nil {
		return "", fmt.Errorf("invalid base_url: %w", err)
	}
	return normalized, nil
}

func (s *HappyHorseGatewayService) toUpstreamError(statusCode int, body []byte, headers http.Header) error {
	message := ExtractUpstreamErrorMessage(body)
	if shouldFailoverHappyHorseStatus(statusCode) {
		return &UpstreamFailoverError{
			StatusCode:             statusCode,
			ResponseBody:           body,
			ResponseHeaders:        headers.Clone(),
			RetryableOnSameAccount: statusCode == http.StatusTooManyRequests || statusCode >= 500,
		}
	}
	return &HappyHorseHTTPError{StatusCode: statusCode, Body: body, Headers: headers.Clone(), Message: message}
}

func shouldFailoverHappyHorseStatus(statusCode int) bool {
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests:
		return true
	default:
		return statusCode >= 500
	}
}

func validateHappyHorseAccount(account *Account) error {
	if account == nil {
		return errors.New("account is nil")
	}
	if !account.IsHappyHorse() {
		return errors.New("account is not a HappyHorse account")
	}
	if account.Type != AccountTypeAPIKey {
		return errors.New("HappyHorse account must be an API key account")
	}
	if strings.TrimSpace(account.GetHappyHorseAPIKey()) == "" {
		return errors.New("HappyHorse api_key is empty")
	}
	return nil
}

func extractHappyHorsePrompt(body []byte) string {
	if prompt := strings.TrimSpace(gjson.GetBytes(body, "prompt").String()); prompt != "" {
		return prompt
	}
	prompts := make([]string, 0)
	for _, path := range []string{"multi_prompt", "shots"} {
		items := gjson.GetBytes(body, path)
		if !items.Exists() || !items.IsArray() {
			continue
		}
		for _, item := range items.Array() {
			if prompt := strings.TrimSpace(item.Get("prompt").String()); prompt != "" {
				prompts = append(prompts, prompt)
			}
		}
	}
	return strings.Join(prompts, "\n")
}

type happyHorsePayload struct {
	TaskID       string
	Status       string
	Model        string
	ResultURLs   []string
	ErrorMessage string
	Payload      map[string]any
}

func parseHappyHorsePayload(body []byte) happyHorsePayload {
	payload := happyHorsePayload{
		Status:  "submitted",
		Payload: map[string]any{},
	}
	_ = json.Unmarshal(body, &payload.Payload)
	payload.TaskID = firstGJSONBytes(body, "data.task_id", "data.taskId", "task_id", "taskId", "data.id", "id")
	payload.Status = normalizeHappyHorseStatus(firstGJSONBytes(body, "data.status", "status", "data.response.status", "response.status"))
	payload.Model = firstGJSONBytes(body, "data.model", "model")
	payload.ErrorMessage = firstGJSONBytes(body, "data.error_message", "data.response.error_message", "error_message", "data.error", "error", "data.message", "message", "msg")
	payload.ResultURLs = extractHappyHorseResultURLs(body)
	return payload
}

func firstGJSONBytes(body []byte, paths ...string) string {
	for _, path := range paths {
		if result := gjson.GetBytes(body, path); result.Exists() {
			if s := strings.TrimSpace(result.String()); s != "" {
				return s
			}
		}
	}
	return ""
}

func extractHappyHorseResultURLs(body []byte) []string {
	paths := []string{
		"data.response.resultUrls",
		"data.response.result_urls",
		"data.resultUrls",
		"data.result_urls",
		"resultUrls",
		"result_urls",
	}
	for _, path := range paths {
		result := gjson.GetBytes(body, path)
		if !result.Exists() || !result.IsArray() {
			continue
		}
		urls := make([]string, 0)
		for _, item := range result.Array() {
			if s := strings.TrimSpace(item.String()); s != "" {
				urls = append(urls, s)
			}
		}
		if len(urls) > 0 {
			return urls
		}
	}
	return nil
}

func normalizeHappyHorseStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "success", "succeeded", "completed", "complete", "done":
		return "success"
	case "failed", "failure", "error":
		return "failed"
	case "cancelled", "canceled":
		return "cancelled"
	case "processing", "running", "generating", "in_progress":
		return "processing"
	case "queued", "pending", "submitted", "":
		return "submitted"
	default:
		return strings.ToLower(strings.TrimSpace(status))
	}
}

func NormalizeHappyHorseStatusForHandler(status string) string {
	return normalizeHappyHorseStatus(status)
}

func happyHorseRequestID(headers http.Header) string {
	for _, key := range []string{"x-request-id", "X-Request-Id", "cf-ray"} {
		if value := strings.TrimSpace(headers.Get(key)); value != "" {
			return value
		}
	}
	return ""
}
