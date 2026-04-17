//go:build unit

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAdminAuthJWTValidatesTokenVersion(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{JWT: config.JWTConfig{Secret: "test-secret", ExpireHour: 1}}
	authService := service.NewAuthService(nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, nil)

	admin := &service.User{
		ID:           1,
		Email:        "admin@example.com",
		Role:         service.RoleAdmin,
		Status:       service.StatusActive,
		TokenVersion: 2,
		Concurrency:  1,
	}

	userRepo := &stubUserRepo{
		getByID: func(ctx context.Context, id int64) (*service.User, error) {
			if id != admin.ID {
				return nil, service.ErrUserNotFound
			}
			clone := *admin
			return &clone, nil
		},
	}
	userService := service.NewUserService(userRepo, nil, nil, nil)

	router := gin.New()
	router.Use(gin.HandlerFunc(NewAdminAuthMiddleware(authService, userService, nil, nil)))
	router.GET("/t", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	t.Run("token_version_mismatch_rejected", func(t *testing.T) {
		token, err := authService.GenerateToken(&service.User{
			ID:           admin.ID,
			Email:        admin.Email,
			Role:         admin.Role,
			TokenVersion: admin.TokenVersion - 1,
		})
		require.NoError(t, err)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/t", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusUnauthorized, w.Code)
		require.Contains(t, w.Body.String(), "TOKEN_REVOKED")
	})

	t.Run("token_version_match_allows", func(t *testing.T) {
		token, err := authService.GenerateToken(&service.User{
			ID:           admin.ID,
			Email:        admin.Email,
			Role:         admin.Role,
			TokenVersion: admin.TokenVersion,
		})
		require.NoError(t, err)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/t", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("websocket_token_version_mismatch_rejected", func(t *testing.T) {
		token, err := authService.GenerateToken(&service.User{
			ID:           admin.ID,
			Email:        admin.Email,
			Role:         admin.Role,
			TokenVersion: admin.TokenVersion - 1,
		})
		require.NoError(t, err)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/t", nil)
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Sec-WebSocket-Protocol", "sub2api-admin, jwt."+token)
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusUnauthorized, w.Code)
		require.Contains(t, w.Body.String(), "TOKEN_REVOKED")
	})

	t.Run("websocket_token_version_match_allows", func(t *testing.T) {
		token, err := authService.GenerateToken(&service.User{
			ID:           admin.ID,
			Email:        admin.Email,
			Role:         admin.Role,
			TokenVersion: admin.TokenVersion,
		})
		require.NoError(t, err)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/t", nil)
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Sec-WebSocket-Protocol", "sub2api-admin, jwt."+token)
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
	})
}

func TestAdminAuthManagedNodeAPIKeyAllowsAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)

	admin := &service.User{
		ID:          1,
		Email:       "admin@example.com",
		Role:        service.RoleAdmin,
		Status:      service.StatusActive,
		Concurrency: 1,
	}

	userRepo := &stubUserRepo{
		getFirstAdmin: func(ctx context.Context) (*service.User, error) {
			clone := *admin
			return &clone, nil
		},
	}
	userService := service.NewUserService(userRepo, nil, nil, nil)
	managedNodeKeyService := service.NewManagedNodeAPIKeyService(&managedNodeAPIKeyRepoStub{
		authenticateActiveByHash: func(ctx context.Context, keyHash, ip string, usedAt time.Time, audit *service.ManagedNodeAPIKeyAudit) (*service.ManagedNodeAPIKey, error) {
			return &service.ManagedNodeAPIKey{
				ID:     7,
				Name:   "node-a",
				Status: service.ManagedNodeAPIKeyStatusActive,
			}, nil
		},
	})

	router := gin.New()
	router.Use(gin.HandlerFunc(NewAdminAuthMiddleware(nil, userService, nil, managedNodeKeyService)))
	router.GET("/t", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"auth_method":               c.GetString("auth_method"),
			"managed_node_api_key_id":   c.GetInt64("managed_node_api_key_id"),
			"managed_node_api_key_name": c.GetString("managed_node_api_key_name"),
		})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.Header.Set("x-api-key", service.ManagedNodeAPIKeyPrefix+"0123456789abcdef")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "managed_node_api_key")
	require.Contains(t, w.Body.String(), "\"managed_node_api_key_id\":7")
	require.Contains(t, w.Body.String(), "node-a")
}

type stubUserRepo struct {
	getByID       func(ctx context.Context, id int64) (*service.User, error)
	getFirstAdmin func(ctx context.Context) (*service.User, error)
}

func (s *stubUserRepo) Create(ctx context.Context, user *service.User) error {
	panic("unexpected Create call")
}

func (s *stubUserRepo) GetByID(ctx context.Context, id int64) (*service.User, error) {
	if s.getByID == nil {
		panic("GetByID not stubbed")
	}
	return s.getByID(ctx, id)
}

func (s *stubUserRepo) GetByEmail(ctx context.Context, email string) (*service.User, error) {
	panic("unexpected GetByEmail call")
}

func (s *stubUserRepo) GetFirstAdmin(ctx context.Context) (*service.User, error) {
	if s.getFirstAdmin == nil {
		panic("GetFirstAdmin not stubbed")
	}
	return s.getFirstAdmin(ctx)
}

func (s *stubUserRepo) Update(ctx context.Context, user *service.User) error {
	panic("unexpected Update call")
}

func (s *stubUserRepo) Delete(ctx context.Context, id int64) error {
	panic("unexpected Delete call")
}

func (s *stubUserRepo) List(ctx context.Context, params pagination.PaginationParams) ([]service.User, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

func (s *stubUserRepo) ListWithFilters(ctx context.Context, params pagination.PaginationParams, filters service.UserListFilters) ([]service.User, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}

func (s *stubUserRepo) UpdateBalance(ctx context.Context, id int64, amount float64) error {
	panic("unexpected UpdateBalance call")
}

func (s *stubUserRepo) DeductBalance(ctx context.Context, id int64, amount float64) error {
	panic("unexpected DeductBalance call")
}

func (s *stubUserRepo) UpdateConcurrency(ctx context.Context, id int64, amount int) error {
	panic("unexpected UpdateConcurrency call")
}

func (s *stubUserRepo) ExistsByEmail(ctx context.Context, email string) (bool, error) {
	panic("unexpected ExistsByEmail call")
}

func (s *stubUserRepo) RemoveGroupFromAllowedGroups(ctx context.Context, groupID int64) (int64, error) {
	panic("unexpected RemoveGroupFromAllowedGroups call")
}

func (s *stubUserRepo) RemoveGroupFromUserAllowedGroups(ctx context.Context, userID int64, groupID int64) error {
	panic("unexpected RemoveGroupFromUserAllowedGroups call")
}

func (s *stubUserRepo) AddGroupToAllowedGroups(ctx context.Context, userID int64, groupID int64) error {
	panic("unexpected AddGroupToAllowedGroups call")
}

func (s *stubUserRepo) UpdateTotpSecret(ctx context.Context, userID int64, encryptedSecret *string) error {
	panic("unexpected UpdateTotpSecret call")
}

func (s *stubUserRepo) EnableTotp(ctx context.Context, userID int64) error {
	panic("unexpected EnableTotp call")
}

func (s *stubUserRepo) DisableTotp(ctx context.Context, userID int64) error {
	panic("unexpected DisableTotp call")
}

type managedNodeAPIKeyRepoStub struct {
	authenticateActiveByHash func(ctx context.Context, keyHash, ip string, usedAt time.Time, audit *service.ManagedNodeAPIKeyAudit) (*service.ManagedNodeAPIKey, error)
}

func (s *managedNodeAPIKeyRepoStub) Create(ctx context.Context, key *service.ManagedNodeAPIKey, audit *service.ManagedNodeAPIKeyAudit) error {
	panic("unexpected Create call")
}

func (s *managedNodeAPIKeyRepoStub) List(ctx context.Context) ([]service.ManagedNodeAPIKey, error) {
	panic("unexpected List call")
}

func (s *managedNodeAPIKeyRepoStub) AuthenticateActiveByHash(ctx context.Context, keyHash, ip string, usedAt time.Time, audit *service.ManagedNodeAPIKeyAudit) (*service.ManagedNodeAPIKey, error) {
	if s.authenticateActiveByHash == nil {
		panic("AuthenticateActiveByHash not stubbed")
	}
	return s.authenticateActiveByHash(ctx, keyHash, ip, usedAt, audit)
}

func (s *managedNodeAPIKeyRepoStub) Revoke(ctx context.Context, keyID int64, revokedBy *int64, revokedAt time.Time, audit *service.ManagedNodeAPIKeyAudit) (*service.ManagedNodeAPIKey, error) {
	panic("unexpected Revoke call")
}

func (s *managedNodeAPIKeyRepoStub) ListAudits(ctx context.Context, keyID int64, limit int) ([]service.ManagedNodeAPIKeyAudit, error) {
	panic("unexpected ListAudits call")
}
