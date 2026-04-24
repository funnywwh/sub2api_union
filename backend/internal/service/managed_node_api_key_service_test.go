package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestManagedNodeAPIKeyServiceCreateKey(t *testing.T) {
	t.Parallel()

	var createdKey *ManagedNodeAPIKey
	var createdAudit *ManagedNodeAPIKeyAudit
	now := time.Now().UTC()
	repo := &managedNodeAPIKeyServiceRepoStub{
		create: func(ctx context.Context, key *ManagedNodeAPIKey, audit *ManagedNodeAPIKeyAudit) error {
			createdKey = cloneManagedNodeAPIKey(key)
			createdAudit = cloneManagedNodeAPIKeyAudit(audit)
			key.ID = 11
			key.CreatedAt = now
			key.UpdatedAt = now
			return nil
		},
	}

	svc := NewManagedNodeAPIKeyService(repo)
	operatorID := int64(7)
	key, raw, err := svc.CreateKey(context.Background(), ManagedNodeAPIKeyCreateInput{
		Name:           "Node A",
		Description:    "Remote admin key",
		OperatorUserID: &operatorID,
		OperatorRole:   RoleAdmin,
		AuthMethod:     "jwt",
	})
	require.NoError(t, err)
	require.NotNil(t, key)
	require.NotEmpty(t, raw)
	require.NotNil(t, createdKey)
	require.NotNil(t, createdAudit)
	require.Equal(t, ManagedNodeAPIKeyStatusActive, createdKey.Status)
	require.Equal(t, "Node A", createdKey.Name)
	require.Equal(t, "Remote admin key", createdKey.Description)
	require.Equal(t, ManagedNodeAPIKeyAuditActionCreated, createdAudit.Action)
	require.Equal(t, RoleAdmin, createdAudit.OperatorRole)
	require.Equal(t, "jwt", createdAudit.AuthMethod)
	require.Equal(t, raw[:18], createdKey.KeyPrefix)
	require.Equal(t, raw[len(raw)-4:], createdKey.KeySuffix)
	require.Equal(t, raw[:18]+"..."+raw[len(raw)-4:], createdKey.MaskedKey())
	require.Equal(t, createdKey.MaskedKey(), createdAudit.Detail["masked_key"])
}

func TestManagedNodeAPIKeyServiceRevokeKey(t *testing.T) {
	t.Parallel()

	var revokedAudit *ManagedNodeAPIKeyAudit
	operatorID := int64(9)
	repo := &managedNodeAPIKeyServiceRepoStub{
		revoke: func(ctx context.Context, keyID int64, revokedBy *int64, revokedAt time.Time, audit *ManagedNodeAPIKeyAudit) (*ManagedNodeAPIKey, error) {
			revokedAudit = cloneManagedNodeAPIKeyAudit(audit)
			return &ManagedNodeAPIKey{
				ID:        keyID,
				Name:      "Node B",
				Status:    ManagedNodeAPIKeyStatusRevoked,
				RevokedBy: revokedBy,
				RevokedAt: &revokedAt,
			}, nil
		},
	}

	svc := NewManagedNodeAPIKeyService(repo)
	key, err := svc.RevokeKey(context.Background(), 22, ManagedNodeAPIKeyRevokeInput{
		Reason:         "rotated",
		OperatorUserID: &operatorID,
		OperatorRole:   RoleAdmin,
		AuthMethod:     "admin_api_key",
	})
	require.NoError(t, err)
	require.NotNil(t, key)
	require.Equal(t, ManagedNodeAPIKeyStatusRevoked, key.Status)
	require.NotNil(t, revokedAudit)
	require.Equal(t, ManagedNodeAPIKeyAuditActionRevoked, revokedAudit.Action)
	require.Equal(t, "rotated", revokedAudit.Detail["reason"])
	require.Equal(t, "admin_api_key", revokedAudit.AuthMethod)
}

func TestManagedNodeAPIKeyServiceAuthenticateKeyRecordsUsageAudit(t *testing.T) {
	t.Parallel()

	var usageAudit *ManagedNodeAPIKeyAudit
	repo := &managedNodeAPIKeyServiceRepoStub{
		authenticateActiveByHash: func(ctx context.Context, keyHash, ip string, usedAt time.Time, audit *ManagedNodeAPIKeyAudit) (*ManagedNodeAPIKey, error) {
			usageAudit = cloneManagedNodeAPIKeyAudit(audit)
			return &ManagedNodeAPIKey{
				ID:     33,
				Name:   "Node C",
				Status: ManagedNodeAPIKeyStatusActive,
			}, nil
		},
	}

	svc := NewManagedNodeAPIKeyService(repo)
	key, err := svc.AuthenticateKey(context.Background(), ManagedNodeAPIKeyPrefix+"abcdef0123456789", ManagedNodeAPIKeyUsageInput{
		IP:     "127.0.0.1",
		Method: "POST",
		Path:   "/api/v1/admin/settings",
	})
	require.NoError(t, err)
	require.NotNil(t, key)
	require.NotNil(t, usageAudit)
	require.Equal(t, ManagedNodeAPIKeyAuditActionUsed, usageAudit.Action)
	require.Equal(t, "managed_node_api_key", usageAudit.AuthMethod)
	require.Equal(t, "127.0.0.1", usageAudit.Detail["ip"])
	require.Equal(t, "POST", usageAudit.Detail["method"])
	require.Equal(t, "/api/v1/admin/settings", usageAudit.Detail["path"])
}

type managedNodeAPIKeyServiceRepoStub struct {
	create                   func(ctx context.Context, key *ManagedNodeAPIKey, audit *ManagedNodeAPIKeyAudit) error
	list                     func(ctx context.Context) ([]ManagedNodeAPIKey, error)
	authenticateActiveByHash func(ctx context.Context, keyHash, ip string, usedAt time.Time, audit *ManagedNodeAPIKeyAudit) (*ManagedNodeAPIKey, error)
	touchUsageByID           func(ctx context.Context, keyID int64, ip string, usedAt time.Time, audit *ManagedNodeAPIKeyAudit) error
	revoke                   func(ctx context.Context, keyID int64, revokedBy *int64, revokedAt time.Time, audit *ManagedNodeAPIKeyAudit) (*ManagedNodeAPIKey, error)
	listAudits               func(ctx context.Context, keyID int64, limit int) ([]ManagedNodeAPIKeyAudit, error)
}

func (s *managedNodeAPIKeyServiceRepoStub) Create(ctx context.Context, key *ManagedNodeAPIKey, audit *ManagedNodeAPIKeyAudit) error {
	if s.create == nil {
		panic("Create not stubbed")
	}
	return s.create(ctx, key, audit)
}

func (s *managedNodeAPIKeyServiceRepoStub) List(ctx context.Context) ([]ManagedNodeAPIKey, error) {
	if s.list == nil {
		return []ManagedNodeAPIKey{}, nil
	}
	return s.list(ctx)
}

func (s *managedNodeAPIKeyServiceRepoStub) AuthenticateActiveByHash(ctx context.Context, keyHash, ip string, usedAt time.Time, audit *ManagedNodeAPIKeyAudit) (*ManagedNodeAPIKey, error) {
	if s.authenticateActiveByHash == nil {
		return nil, nil
	}
	return s.authenticateActiveByHash(ctx, keyHash, ip, usedAt, audit)
}

func (s *managedNodeAPIKeyServiceRepoStub) TouchUsageByID(ctx context.Context, keyID int64, ip string, usedAt time.Time, audit *ManagedNodeAPIKeyAudit) error {
	if s.touchUsageByID == nil {
		return nil
	}
	return s.touchUsageByID(ctx, keyID, ip, usedAt, audit)
}

func (s *managedNodeAPIKeyServiceRepoStub) Revoke(ctx context.Context, keyID int64, revokedBy *int64, revokedAt time.Time, audit *ManagedNodeAPIKeyAudit) (*ManagedNodeAPIKey, error) {
	if s.revoke == nil {
		panic("Revoke not stubbed")
	}
	return s.revoke(ctx, keyID, revokedBy, revokedAt, audit)
}

func (s *managedNodeAPIKeyServiceRepoStub) ListAudits(ctx context.Context, keyID int64, limit int) ([]ManagedNodeAPIKeyAudit, error) {
	if s.listAudits == nil {
		return []ManagedNodeAPIKeyAudit{}, nil
	}
	return s.listAudits(ctx, keyID, limit)
}

func cloneManagedNodeAPIKey(in *ManagedNodeAPIKey) *ManagedNodeAPIKey {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func cloneManagedNodeAPIKeyAudit(in *ManagedNodeAPIKeyAudit) *ManagedNodeAPIKeyAudit {
	if in == nil {
		return nil
	}
	out := *in
	if in.Detail != nil {
		out.Detail = make(map[string]any, len(in.Detail))
		for k, v := range in.Detail {
			out.Detail[k] = v
		}
	}
	return &out
}
