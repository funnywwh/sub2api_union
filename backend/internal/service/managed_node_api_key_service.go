package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	ManagedNodeAPIKeyStatusActive  = "active"
	ManagedNodeAPIKeyStatusRevoked = "revoked"

	ManagedNodeAPIKeyAuditActionCreated = "created"
	ManagedNodeAPIKeyAuditActionRevoked = "revoked"
	ManagedNodeAPIKeyAuditActionUsed    = "used"
)

type ManagedNodeAPIKey struct {
	ID          int64
	Name        string
	Description string
	KeyHash     string
	KeyPrefix   string
	KeySuffix   string
	Status      string
	CreatedBy   *int64
	RevokedBy   *int64
	LastUsedAt  *time.Time
	LastUsedIP  string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	RevokedAt   *time.Time
}

func (k ManagedNodeAPIKey) MaskedKey() string {
	if k.KeyPrefix == "" {
		return ""
	}
	if k.KeySuffix == "" {
		return k.KeyPrefix
	}
	return k.KeyPrefix + "..." + k.KeySuffix
}

type ManagedNodeAPIKeyAudit struct {
	ID                  int64
	ManagedNodeAPIKeyID int64
	Action              string
	OperatorUserID      *int64
	OperatorRole        string
	AuthMethod          string
	Detail              map[string]any
	CreatedAt           time.Time
}

type ManagedNodeAPIKeyCreateInput struct {
	Name                       string
	Description                string
	OperatorUserID             *int64
	OperatorRole               string
	AuthMethod                 string
	OperatorManagedNodeKeyID   *int64
	OperatorManagedNodeKeyName string
}

type ManagedNodeAPIKeyRevokeInput struct {
	Reason                     string
	OperatorUserID             *int64
	OperatorRole               string
	AuthMethod                 string
	OperatorManagedNodeKeyID   *int64
	OperatorManagedNodeKeyName string
}

type ManagedNodeAPIKeyUsageInput struct {
	IP     string
	Method string
	Path   string
}

type ManagedNodeAPIKeyRepository interface {
	Create(ctx context.Context, key *ManagedNodeAPIKey, audit *ManagedNodeAPIKeyAudit) error
	List(ctx context.Context) ([]ManagedNodeAPIKey, error)
	AuthenticateActiveByHash(ctx context.Context, keyHash, ip string, usedAt time.Time, audit *ManagedNodeAPIKeyAudit) (*ManagedNodeAPIKey, error)
	Revoke(ctx context.Context, keyID int64, revokedBy *int64, revokedAt time.Time, audit *ManagedNodeAPIKeyAudit) (*ManagedNodeAPIKey, error)
	ListAudits(ctx context.Context, keyID int64, limit int) ([]ManagedNodeAPIKeyAudit, error)
}

type ManagedNodeAPIKeyService struct {
	repo ManagedNodeAPIKeyRepository
}

func NewManagedNodeAPIKeyService(repo ManagedNodeAPIKeyRepository) *ManagedNodeAPIKeyService {
	return &ManagedNodeAPIKeyService{repo: repo}
}

func (s *ManagedNodeAPIKeyService) ListKeys(ctx context.Context) ([]ManagedNodeAPIKey, error) {
	if s == nil || s.repo == nil {
		return []ManagedNodeAPIKey{}, nil
	}
	return s.repo.List(ctx)
}

func (s *ManagedNodeAPIKeyService) ListAudits(ctx context.Context, keyID int64, limit int) ([]ManagedNodeAPIKeyAudit, error) {
	if s == nil || s.repo == nil {
		return []ManagedNodeAPIKeyAudit{}, nil
	}
	if keyID <= 0 {
		return nil, infraerrors.BadRequest("MANAGED_NODE_API_KEY_ID_INVALID", "managed node API key id must be positive")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	return s.repo.ListAudits(ctx, keyID, limit)
}

func (s *ManagedNodeAPIKeyService) CreateKey(ctx context.Context, input ManagedNodeAPIKeyCreateInput) (*ManagedNodeAPIKey, string, error) {
	if s == nil || s.repo == nil {
		return nil, "", infraerrors.InternalServer("MANAGED_NODE_API_KEY_REPO_MISSING", "managed node API key repository is unavailable")
	}

	name := strings.TrimSpace(input.Name)
	if name == "" {
		return nil, "", infraerrors.BadRequest("MANAGED_NODE_API_KEY_NAME_REQUIRED", "managed node API key name is required")
	}
	if len(name) > 100 {
		return nil, "", infraerrors.BadRequest("MANAGED_NODE_API_KEY_NAME_TOO_LONG", "managed node API key name must be at most 100 characters")
	}

	description := strings.TrimSpace(input.Description)
	if len(description) > 1000 {
		return nil, "", infraerrors.BadRequest("MANAGED_NODE_API_KEY_DESCRIPTION_TOO_LONG", "managed node API key description must be at most 1000 characters")
	}

	rawKey, err := generateManagedNodeAPIKey()
	if err != nil {
		return nil, "", infraerrors.InternalServer("MANAGED_NODE_API_KEY_GENERATE_FAILED", "failed to generate managed node API key").WithCause(err)
	}

	key := &ManagedNodeAPIKey{
		Name:        name,
		Description: description,
		KeyHash:     hashManagedNodeAPIKey(rawKey),
		KeyPrefix:   managedNodeAPIKeyPrefix(rawKey),
		KeySuffix:   managedNodeAPIKeySuffix(rawKey),
		Status:      ManagedNodeAPIKeyStatusActive,
		CreatedBy:   input.OperatorUserID,
		LastUsedIP:  "",
	}

	audit := &ManagedNodeAPIKeyAudit{
		Action:         ManagedNodeAPIKeyAuditActionCreated,
		OperatorUserID: input.OperatorUserID,
		OperatorRole:   strings.TrimSpace(input.OperatorRole),
		AuthMethod:     strings.TrimSpace(input.AuthMethod),
		Detail: map[string]any{
			"name":        name,
			"description": description,
			"masked_key":  key.MaskedKey(),
		},
	}
	appendManagedNodeAuditActor(audit.Detail, input.OperatorManagedNodeKeyID, input.OperatorManagedNodeKeyName)

	if err := s.repo.Create(ctx, key, audit); err != nil {
		return nil, "", err
	}

	return key, rawKey, nil
}

func (s *ManagedNodeAPIKeyService) RevokeKey(ctx context.Context, keyID int64, input ManagedNodeAPIKeyRevokeInput) (*ManagedNodeAPIKey, error) {
	if s == nil || s.repo == nil {
		return nil, infraerrors.InternalServer("MANAGED_NODE_API_KEY_REPO_MISSING", "managed node API key repository is unavailable")
	}
	if keyID <= 0 {
		return nil, infraerrors.BadRequest("MANAGED_NODE_API_KEY_ID_INVALID", "managed node API key id must be positive")
	}

	reason := strings.TrimSpace(input.Reason)
	if len(reason) > 1000 {
		return nil, infraerrors.BadRequest("MANAGED_NODE_API_KEY_REASON_TOO_LONG", "managed node API key revoke reason must be at most 1000 characters")
	}

	revokedAt := time.Now().UTC()
	detail := map[string]any{}
	if reason != "" {
		detail["reason"] = reason
	}

	audit := &ManagedNodeAPIKeyAudit{
		Action:         ManagedNodeAPIKeyAuditActionRevoked,
		OperatorUserID: input.OperatorUserID,
		OperatorRole:   strings.TrimSpace(input.OperatorRole),
		AuthMethod:     strings.TrimSpace(input.AuthMethod),
		Detail:         detail,
	}
	appendManagedNodeAuditActor(audit.Detail, input.OperatorManagedNodeKeyID, input.OperatorManagedNodeKeyName)

	return s.repo.Revoke(ctx, keyID, input.OperatorUserID, revokedAt, audit)
}

func (s *ManagedNodeAPIKeyService) AuthenticateKey(ctx context.Context, rawKey string, usage ManagedNodeAPIKeyUsageInput) (*ManagedNodeAPIKey, error) {
	if s == nil || s.repo == nil {
		return nil, nil
	}

	rawKey = strings.TrimSpace(rawKey)
	if rawKey == "" || !strings.HasPrefix(rawKey, ManagedNodeAPIKeyPrefix) {
		return nil, nil
	}

	usedAt := time.Now().UTC()
	detail := map[string]any{}
	if ip := strings.TrimSpace(usage.IP); ip != "" {
		detail["ip"] = ip
	}
	if method := strings.TrimSpace(usage.Method); method != "" {
		detail["method"] = method
	}
	if path := strings.TrimSpace(usage.Path); path != "" {
		detail["path"] = path
	}
	var audit *ManagedNodeAPIKeyAudit
	if len(detail) > 0 {
		audit = &ManagedNodeAPIKeyAudit{
			Action:     ManagedNodeAPIKeyAuditActionUsed,
			AuthMethod: "managed_node_api_key",
			Detail:     detail,
			CreatedAt:  usedAt,
		}
	}
	return s.repo.AuthenticateActiveByHash(ctx, hashManagedNodeAPIKey(rawKey), strings.TrimSpace(usage.IP), usedAt, audit)
}

func generateManagedNodeAPIKey() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return ManagedNodeAPIKeyPrefix + hex.EncodeToString(bytes), nil
}

func hashManagedNodeAPIKey(rawKey string) string {
	sum := sha256.Sum256([]byte(rawKey))
	return hex.EncodeToString(sum[:])
}

func managedNodeAPIKeyPrefix(rawKey string) string {
	if len(rawKey) <= 18 {
		return rawKey
	}
	return rawKey[:18]
}

func managedNodeAPIKeySuffix(rawKey string) string {
	if len(rawKey) <= 4 {
		return rawKey
	}
	return rawKey[len(rawKey)-4:]
}

func appendManagedNodeAuditActor(detail map[string]any, keyID *int64, keyName string) {
	if detail == nil {
		return
	}
	if keyID != nil {
		detail["operator_managed_node_api_key_id"] = *keyID
	}
	if keyName = strings.TrimSpace(keyName); keyName != "" {
		detail["operator_managed_node_api_key_name"] = keyName
	}
}
