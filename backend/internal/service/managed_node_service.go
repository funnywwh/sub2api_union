package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/redis/go-redis/v9"
)

const (
	managedNodesSettingKey            = "managed_nodes"
	defaultManagedNodeRedirectPath    = "/admin/dashboard"
	defaultManagedNodeHTTPTimeout     = 10 * time.Second
	defaultManagedNodeSessionLinkPath = "/api/v1/admin/settings/federation/session-link"
	defaultManagedNodeInfoPath        = "/api/v1/admin/settings/federation/info"
	defaultFederationCallbackPath     = "/auth/federation/callback"
	federationTicketKeyPrefix         = "managed_node:federation_ticket:"
	defaultFederationTicketTTL        = 60 * time.Second
	defaultFederationAccessTTL        = 30 * time.Minute
)

type ManagedNode struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Description     string    `json:"description"`
	Scheme          string    `json:"scheme"`
	Host            string    `json:"host"`
	Port            int       `json:"port"`
	APIKey          string    `json:"api_key,omitempty"`
	APIKeyEncrypted string    `json:"api_key_encrypted,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func (n ManagedNode) BaseURL() string {
	scheme := strings.TrimSpace(n.Scheme)
	if scheme == "" {
		scheme = "https"
	}
	host := strings.TrimSpace(n.Host)
	if host == "" {
		return ""
	}
	if n.Port <= 0 {
		return fmt.Sprintf("%s://%s", scheme, host)
	}
	return fmt.Sprintf("%s://%s:%d", scheme, host, n.Port)
}

func (n ManagedNode) MaskedKey() string {
	key := strings.TrimSpace(n.APIKey)
	if key == "" {
		return ""
	}
	if len(key) <= 18 {
		return key
	}
	return key[:18] + "..." + key[len(key)-4:]
}

type ManagedNodeCreateInput struct {
	Name        string
	Description string
	Scheme      string
	Host        string
	Port        int
	APIKey      string
}

type ManagedNodeUpdateInput struct {
	Name        string
	Description string
	Scheme      string
	Host        string
	Port        int
	APIKey      *string
}

type ManagedNodeRemoteInfo struct {
	SiteName     string `json:"site_name"`
	AuthMethod   string `json:"auth_method,omitempty"`
	FrontendURL  string `json:"frontend_url,omitempty"`
	RequestedBy  string `json:"requested_by,omitempty"`
	ManagedKeyID int64  `json:"managed_node_api_key_id,omitempty"`
}

type ManagedNodeJumpLinkResponse struct {
	LoginURL  string `json:"login_url"`
	ExpiresIn int    `json:"expires_in"`
	SiteName  string `json:"site_name,omitempty"`
}

type federationTicketData struct {
	UserID                int64     `json:"user_id"`
	TokenVersion          int64     `json:"token_version"`
	AuthMethod            string    `json:"auth_method"`
	ManagedNodeAPIKeyID   *int64    `json:"managed_node_api_key_id,omitempty"`
	ManagedNodeAPIKeyName string    `json:"managed_node_api_key_name,omitempty"`
	Redirect              string    `json:"redirect"`
	CreatedAt             time.Time `json:"created_at"`
	ExpiresAt             time.Time `json:"expires_at"`
}

type ManagedNodeService struct {
	settingRepo    SettingRepository
	settingService *SettingService
	authService    *AuthService
	userService    *UserService
	encryptor      SecretEncryptor
	redisClient    *redis.Client
	httpClient     *http.Client
}

func NewManagedNodeService(
	settingRepo SettingRepository,
	settingService *SettingService,
	authService *AuthService,
	userService *UserService,
	encryptor SecretEncryptor,
	redisClient *redis.Client,
) *ManagedNodeService {
	return &ManagedNodeService{
		settingRepo:    settingRepo,
		settingService: settingService,
		authService:    authService,
		userService:    userService,
		encryptor:      encryptor,
		redisClient:    redisClient,
		httpClient: &http.Client{
			Timeout: defaultManagedNodeHTTPTimeout,
		},
	}
}

func (s *ManagedNodeService) ListManagedNodes(ctx context.Context) ([]ManagedNode, error) {
	nodes, err := s.loadManagedNodes(ctx)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(nodes, func(a, b ManagedNode) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return nodes, nil
}

func (s *ManagedNodeService) CreateManagedNode(ctx context.Context, input ManagedNodeCreateInput) (*ManagedNode, error) {
	validated, err := validateManagedNodeCreateInput(input)
	if err != nil {
		return nil, err
	}

	nodes, err := s.loadManagedNodes(ctx)
	if err != nil {
		return nil, err
	}
	for _, item := range nodes {
		if strings.EqualFold(item.Name, validated.Name) {
			return nil, infraerrors.Conflict("MANAGED_NODE_NAME_EXISTS", "managed node name already exists")
		}
	}

	now := time.Now().UTC()
	node := ManagedNode{
		ID:          generateManagedNodeID(),
		Name:        validated.Name,
		Description: validated.Description,
		Scheme:      validated.Scheme,
		Host:        validated.Host,
		Port:        validated.Port,
		APIKey:      validated.APIKey,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.encryptManagedNodeAPIKey(&node); err != nil {
		return nil, err
	}
	nodes = append(nodes, node)

	if err := s.saveManagedNodes(ctx, nodes); err != nil {
		return nil, err
	}
	return &node, nil
}

func (s *ManagedNodeService) UpdateManagedNode(ctx context.Context, id string, input ManagedNodeUpdateInput) (*ManagedNode, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, infraerrors.BadRequest("MANAGED_NODE_ID_REQUIRED", "managed node id is required")
	}

	nodes, err := s.loadManagedNodes(ctx)
	if err != nil {
		return nil, err
	}

	index := slices.IndexFunc(nodes, func(item ManagedNode) bool {
		return item.ID == id
	})
	if index < 0 {
		return nil, infraerrors.NotFound("MANAGED_NODE_NOT_FOUND", "managed node not found")
	}

	validated, err := validateManagedNodeUpdateInput(input)
	if err != nil {
		return nil, err
	}

	for _, item := range nodes {
		if item.ID == id {
			continue
		}
		if strings.EqualFold(item.Name, validated.Name) {
			return nil, infraerrors.Conflict("MANAGED_NODE_NAME_EXISTS", "managed node name already exists")
		}
	}

	node := nodes[index]
	node.Name = validated.Name
	node.Description = validated.Description
	node.Scheme = validated.Scheme
	node.Host = validated.Host
	node.Port = validated.Port
	if validated.APIKey != nil {
		node.APIKey = *validated.APIKey
		if err := s.encryptManagedNodeAPIKey(&node); err != nil {
			return nil, err
		}
	}
	node.UpdatedAt = time.Now().UTC()

	nodes[index] = node
	if err := s.saveManagedNodes(ctx, nodes); err != nil {
		return nil, err
	}
	return &node, nil
}

func (s *ManagedNodeService) DeleteManagedNode(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return infraerrors.BadRequest("MANAGED_NODE_ID_REQUIRED", "managed node id is required")
	}

	nodes, err := s.loadManagedNodes(ctx)
	if err != nil {
		return err
	}
	next := make([]ManagedNode, 0, len(nodes))
	found := false
	for _, item := range nodes {
		if item.ID == id {
			found = true
			continue
		}
		next = append(next, item)
	}
	if !found {
		return infraerrors.NotFound("MANAGED_NODE_NOT_FOUND", "managed node not found")
	}
	return s.saveManagedNodes(ctx, next)
}

func (s *ManagedNodeService) GetManagedNode(ctx context.Context, id string) (*ManagedNode, error) {
	nodes, err := s.loadManagedNodes(ctx)
	if err != nil {
		return nil, err
	}
	for _, item := range nodes {
		if item.ID == id {
			node := item
			return &node, nil
		}
	}
	return nil, infraerrors.NotFound("MANAGED_NODE_NOT_FOUND", "managed node not found")
}

func (s *ManagedNodeService) GetRemoteNodeInfo(ctx context.Context, node *ManagedNode) (*ManagedNodeRemoteInfo, error) {
	if node == nil {
		return nil, infraerrors.BadRequest("MANAGED_NODE_REQUIRED", "managed node is required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, node.BaseURL()+defaultManagedNodeInfoPath, nil)
	if err != nil {
		return nil, fmt.Errorf("build remote info request: %w", err)
	}
	req.Header.Set("x-api-key", node.APIKey)

	var payload ManagedNodeRemoteInfo
	if err := s.doJSON(req, &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

func (s *ManagedNodeService) RequestRemoteJumpLink(ctx context.Context, node *ManagedNode, redirectPath string) (*ManagedNodeJumpLinkResponse, error) {
	if node == nil {
		return nil, infraerrors.BadRequest("MANAGED_NODE_REQUIRED", "managed node is required")
	}

	redirectPath = sanitizeManagedNodeRedirectPath(redirectPath)
	body, err := json.Marshal(map[string]string{"redirect": redirectPath})
	if err != nil {
		return nil, fmt.Errorf("marshal remote jump request: %w", err)
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		node.BaseURL()+defaultManagedNodeSessionLinkPath,
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("build remote jump request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", node.APIKey)

	var payload ManagedNodeJumpLinkResponse
	if err := s.doJSON(req, &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

func (s *ManagedNodeService) CreateLocalJumpLink(ctx context.Context, requestBaseURL string, redirectPath string) (*ManagedNodeJumpLinkResponse, error) {
	if s == nil || s.authService == nil || s.userService == nil || s.redisClient == nil {
		return nil, infraerrors.InternalServer("MANAGED_NODE_FEDERATION_UNAVAILABLE", "managed node federation service is unavailable")
	}

	adminUser, err := s.userService.GetFirstAdmin(ctx)
	if err != nil {
		return nil, infraerrors.InternalServer("FEDERATION_ADMIN_NOT_FOUND", "no admin user found").WithCause(err)
	}
	if !adminUser.IsActive() {
		return nil, infraerrors.Forbidden("FEDERATION_ADMIN_INACTIVE", "admin user is inactive")
	}

	ticket, expiresIn, err := s.issueFederationTicket(ctx, &federationTicketData{
		UserID:                adminUser.ID,
		TokenVersion:          adminUser.TokenVersion,
		AuthMethod:            strings.TrimSpace(requestAuthMethodFromContext(ctx)),
		ManagedNodeAPIKeyID:   requestManagedNodeAPIKeyIDFromContext(ctx),
		ManagedNodeAPIKeyName: requestManagedNodeAPIKeyNameFromContext(ctx),
		Redirect:              sanitizeManagedNodeRedirectPath(redirectPath),
		CreatedAt:             time.Now().UTC(),
		ExpiresAt:             time.Now().UTC().Add(defaultFederationTicketTTL),
	})
	if err != nil {
		return nil, infraerrors.InternalServer("FEDERATION_TICKET_GENERATE_FAILED", "failed to generate federation ticket").WithCause(err)
	}

	frontendBaseURL := strings.TrimSpace(requestBaseURL)
	if s.settingService != nil {
		if configured := strings.TrimSpace(s.settingService.GetFrontendURL(ctx)); configured != "" {
			frontendBaseURL = configured
		}
	}
	if frontendBaseURL == "" {
		return nil, infraerrors.InternalServer("FEDERATION_FRONTEND_URL_MISSING", "frontend url is not configured")
	}
	frontendBaseURL = strings.TrimRight(frontendBaseURL, "/")

	fragment := url.Values{}
	fragment.Set("ticket", ticket)
	fragment.Set("expires_in", strconv.Itoa(expiresIn))
	fragment.Set("redirect", sanitizeManagedNodeRedirectPath(redirectPath))

	siteName := "Sub2API"
	if s.settingService != nil {
		siteName = s.settingService.GetSiteName(ctx)
	}

	return &ManagedNodeJumpLinkResponse{
		LoginURL:  frontendBaseURL + defaultFederationCallbackPath + "#" + fragment.Encode(),
		ExpiresIn: expiresIn,
		SiteName:  siteName,
	}, nil
}

func (s *ManagedNodeService) ExchangeFederationTicket(ctx context.Context, ticket string) (string, int, string, error) {
	if s == nil || s.redisClient == nil || s.authService == nil || s.userService == nil {
		return "", 0, "", infraerrors.InternalServer("FEDERATION_UNAVAILABLE", "managed node federation is unavailable")
	}
	data, err := s.consumeFederationTicket(ctx, ticket)
	if err != nil {
		return "", 0, "", err
	}

	user, err := s.userService.GetByID(ctx, data.UserID)
	if err != nil {
		return "", 0, "", infraerrors.Unauthorized("FEDERATION_USER_NOT_FOUND", "federation user not found").WithCause(err)
	}
	if !user.IsAdmin() || !user.IsActive() {
		return "", 0, "", infraerrors.Forbidden("FEDERATION_USER_INVALID", "federation user is not allowed")
	}
	if user.TokenVersion != data.TokenVersion {
		return "", 0, "", infraerrors.Unauthorized("FEDERATION_TICKET_REVOKED", "federation ticket has been revoked")
	}

	token, expiresIn, err := s.authService.GenerateFederationAccessToken(
		user,
		data.AuthMethod,
		data.ManagedNodeAPIKeyID,
		data.ManagedNodeAPIKeyName,
		defaultFederationAccessTTL,
	)
	if err != nil {
		return "", 0, "", infraerrors.InternalServer("FEDERATION_ACCESS_TOKEN_FAILED", "failed to issue federation access token").WithCause(err)
	}
	return token, expiresIn, data.Redirect, nil
}

func (s *ManagedNodeService) doJSON(req *http.Request, out any) error {
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request managed node: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var payload struct {
			Message string `json:"message"`
			Error   string `json:"error"`
			Code    string `json:"code"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		message := strings.TrimSpace(payload.Message)
		if message == "" {
			message = strings.TrimSpace(payload.Error)
		}
		if message == "" {
			message = "managed node request failed"
		}
		return infraerrors.New(resp.StatusCode, strings.TrimSpace(payload.Code), message)
	}

	var envelope struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("decode managed node response: %w", err)
	}
	if envelope.Code != 0 {
		return infraerrors.BadRequest("MANAGED_NODE_REMOTE_ERROR", "managed node returned an error")
	}
	if len(envelope.Data) == 0 {
		return nil
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("decode managed node data: %w", err)
	}
	return nil
}

func (s *ManagedNodeService) loadManagedNodes(ctx context.Context) ([]ManagedNode, error) {
	if s == nil || s.settingRepo == nil {
		return []ManagedNode{}, nil
	}
	raw, err := s.settingRepo.GetValue(ctx, managedNodesSettingKey)
	if errors.Is(err, ErrSettingNotFound) || strings.TrimSpace(raw) == "" {
		return []ManagedNode{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get managed nodes: %w", err)
	}

	var nodes []ManagedNode
	if err := json.Unmarshal([]byte(raw), &nodes); err != nil {
		return nil, fmt.Errorf("parse managed nodes: %w", err)
	}
	legacyFound := false
	for i := range nodes {
		if nodes[i].Scheme == "" {
			nodes[i].Scheme = "https"
		}
		if nodes[i].APIKeyEncrypted != "" {
			if err := s.decryptManagedNodeAPIKey(&nodes[i]); err != nil {
				return nil, err
			}
			continue
		}
		if strings.TrimSpace(nodes[i].APIKey) != "" {
			legacyFound = true
			if err := s.encryptManagedNodeAPIKey(&nodes[i]); err != nil {
				return nil, err
			}
		}
	}
	if legacyFound {
		if err := s.saveManagedNodes(ctx, nodes); err != nil {
			return nil, err
		}
	}
	return nodes, nil
}

func (s *ManagedNodeService) saveManagedNodes(ctx context.Context, nodes []ManagedNode) error {
	if s == nil || s.settingRepo == nil {
		return nil
	}
	for i := range nodes {
		if err := s.encryptManagedNodeAPIKey(&nodes[i]); err != nil {
			return err
		}
	}
	data, err := json.Marshal(nodes)
	if err != nil {
		return fmt.Errorf("marshal managed nodes: %w", err)
	}
	if err := s.settingRepo.Set(ctx, managedNodesSettingKey, string(data)); err != nil {
		return fmt.Errorf("save managed nodes: %w", err)
	}
	return nil
}

func (s *ManagedNodeService) encryptManagedNodeAPIKey(node *ManagedNode) error {
	if node == nil {
		return nil
	}
	key := strings.TrimSpace(node.APIKey)
	if key == "" {
		return nil
	}
	if s.encryptor == nil {
		return infraerrors.InternalServer("MANAGED_NODE_ENCRYPTOR_MISSING", "managed node encryptor is unavailable")
	}
	encrypted, err := s.encryptor.Encrypt(key)
	if err != nil {
		return infraerrors.InternalServer("MANAGED_NODE_ENCRYPT_FAILED", "failed to encrypt managed node key").WithCause(err)
	}
	node.APIKeyEncrypted = encrypted
	node.APIKey = ""
	return nil
}

func (s *ManagedNodeService) decryptManagedNodeAPIKey(node *ManagedNode) error {
	if node == nil || strings.TrimSpace(node.APIKeyEncrypted) == "" {
		return nil
	}
	if s.encryptor == nil {
		return infraerrors.InternalServer("MANAGED_NODE_ENCRYPTOR_MISSING", "managed node encryptor is unavailable")
	}
	plaintext, err := s.encryptor.Decrypt(node.APIKeyEncrypted)
	if err != nil {
		return infraerrors.InternalServer("MANAGED_NODE_DECRYPT_FAILED", "failed to decrypt managed node key").WithCause(err)
	}
	node.APIKey = strings.TrimSpace(plaintext)
	return nil
}

func federationTicketKey(ticket string) string {
	return federationTicketKeyPrefix + ticket
}

func (s *ManagedNodeService) issueFederationTicket(ctx context.Context, data *federationTicketData) (string, int, error) {
	if data == nil {
		return "", 0, infraerrors.BadRequest("FEDERATION_TICKET_DATA_REQUIRED", "federation ticket data is required")
	}
	raw, err := randomHexString(24)
	if err != nil {
		return "", 0, err
	}
	ticket := "fedtkt_" + raw
	payload, err := json.Marshal(data)
	if err != nil {
		return "", 0, err
	}
	ttl := time.Until(data.ExpiresAt)
	if ttl <= 0 {
		ttl = defaultFederationTicketTTL
	}
	if err := s.redisClient.Set(ctx, federationTicketKey(ticket), payload, ttl).Err(); err != nil {
		return "", 0, err
	}
	return ticket, int(ttl.Seconds()), nil
}

func (s *ManagedNodeService) consumeFederationTicket(ctx context.Context, ticket string) (*federationTicketData, error) {
	ticket = strings.TrimSpace(ticket)
	if ticket == "" {
		return nil, infraerrors.BadRequest("FEDERATION_TICKET_REQUIRED", "federation ticket is required")
	}
	val, err := s.redisClient.GetDel(ctx, federationTicketKey(ticket)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, infraerrors.Unauthorized("FEDERATION_TICKET_INVALID", "federation ticket is invalid or expired")
		}
		return nil, infraerrors.InternalServer("FEDERATION_TICKET_LOOKUP_FAILED", "failed to validate federation ticket").WithCause(err)
	}
	var data federationTicketData
	if err := json.Unmarshal([]byte(val), &data); err != nil {
		return nil, infraerrors.InternalServer("FEDERATION_TICKET_PARSE_FAILED", "failed to parse federation ticket").WithCause(err)
	}
	if time.Now().UTC().After(data.ExpiresAt) {
		return nil, infraerrors.Unauthorized("FEDERATION_TICKET_EXPIRED", "federation ticket has expired")
	}
	return &data, nil
}

type managedNodeContextKey string

const (
	managedNodeContextAuthMethod managedNodeContextKey = "managed_node_auth_method"
	managedNodeContextKeyID      managedNodeContextKey = "managed_node_key_id"
	managedNodeContextKeyName    managedNodeContextKey = "managed_node_key_name"
)

func ManagedNodeContextAuthMethodKey() any { return managedNodeContextAuthMethod }
func ManagedNodeContextKeyIDKey() any      { return managedNodeContextKeyID }
func ManagedNodeContextKeyNameKey() any    { return managedNodeContextKeyName }

func requestAuthMethodFromContext(ctx context.Context) string {
	if ctx == nil {
		return "managed_node_api_key"
	}
	if value := ctx.Value(managedNodeContextAuthMethod); value != nil {
		if authMethod, ok := value.(string); ok && strings.TrimSpace(authMethod) != "" {
			return strings.TrimSpace(authMethod)
		}
	}
	return "managed_node_api_key"
}

func requestManagedNodeAPIKeyIDFromContext(ctx context.Context) *int64 {
	if ctx == nil {
		return nil
	}
	if value := ctx.Value(managedNodeContextKeyID); value != nil {
		switch v := value.(type) {
		case int64:
			return &v
		case *int64:
			return v
		}
	}
	return nil
}

func requestManagedNodeAPIKeyNameFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if value := ctx.Value(managedNodeContextKeyName); value != nil {
		if name, ok := value.(string); ok {
			return strings.TrimSpace(name)
		}
	}
	return ""
}

func validateManagedNodeCreateInput(input ManagedNodeCreateInput) (ManagedNodeCreateInput, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return input, infraerrors.BadRequest("MANAGED_NODE_NAME_REQUIRED", "managed node name is required")
	}
	if len(name) > 100 {
		return input, infraerrors.BadRequest("MANAGED_NODE_NAME_TOO_LONG", "managed node name must be at most 100 characters")
	}
	description := strings.TrimSpace(input.Description)
	if len(description) > 1000 {
		return input, infraerrors.BadRequest("MANAGED_NODE_DESCRIPTION_TOO_LONG", "managed node description must be at most 1000 characters")
	}
	scheme := strings.ToLower(strings.TrimSpace(input.Scheme))
	if scheme == "" {
		scheme = "https"
	}
	if scheme != "http" && scheme != "https" {
		return input, infraerrors.BadRequest("MANAGED_NODE_SCHEME_INVALID", "managed node scheme must be http or https")
	}
	host := strings.TrimSpace(input.Host)
	if host == "" {
		return input, infraerrors.BadRequest("MANAGED_NODE_HOST_REQUIRED", "managed node host is required")
	}
	if strings.Contains(host, "/") {
		return input, infraerrors.BadRequest("MANAGED_NODE_HOST_INVALID", "managed node host must not contain path separators")
	}
	if input.Port <= 0 || input.Port > 65535 {
		return input, infraerrors.BadRequest("MANAGED_NODE_PORT_INVALID", "managed node port must be between 1 and 65535")
	}
	apiKey := strings.TrimSpace(input.APIKey)
	if apiKey == "" {
		return input, infraerrors.BadRequest("MANAGED_NODE_API_KEY_REQUIRED", "managed node api key is required")
	}
	input.Name = name
	input.Description = description
	input.Scheme = scheme
	input.Host = host
	input.APIKey = apiKey
	return input, nil
}

func validateManagedNodeUpdateInput(input ManagedNodeUpdateInput) (ManagedNodeUpdateInput, error) {
	created, err := validateManagedNodeCreateInput(ManagedNodeCreateInput{
		Name:        input.Name,
		Description: input.Description,
		Scheme:      input.Scheme,
		Host:        input.Host,
		Port:        input.Port,
		APIKey:      "__keep__",
	})
	if err != nil {
		return input, err
	}
	input.Name = created.Name
	input.Description = created.Description
	input.Scheme = created.Scheme
	input.Host = created.Host
	if input.APIKey != nil {
		trimmed := strings.TrimSpace(*input.APIKey)
		if trimmed == "" {
			input.APIKey = nil
		} else {
			input.APIKey = &trimmed
		}
	}
	return input, nil
}

func sanitizeManagedNodeRedirectPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return defaultManagedNodeRedirectPath
	}
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.Contains(path, "://") {
		return defaultManagedNodeRedirectPath
	}
	if strings.Contains(path, "\n") || strings.Contains(path, "\r") {
		return defaultManagedNodeRedirectPath
	}
	return path
}

func generateManagedNodeID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("node-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}
