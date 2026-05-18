//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type importAPIKeyRepoStub struct {
	*apiKeyRepoStub
	exists    bool
	existsErr error
	createErr error
	created   *APIKey
	existsKey string
}

func (s *importAPIKeyRepoStub) Create(_ context.Context, key *APIKey) error {
	cloned := *key
	s.created = &cloned
	return s.createErr
}

func (s *importAPIKeyRepoStub) ExistsByKey(_ context.Context, key string) (bool, error) {
	s.existsKey = key
	return s.exists, s.existsErr
}

func TestAdminService_ImportUserAPIKey_Success(t *testing.T) {
	userRepo := &mockUserRepo{
		getByIDUser: &User{
			ID:     7,
			Email:  "import@example.com",
			Status: StatusActive,
			Role:   RoleUser,
		},
	}
	apiKeyRepo := &importAPIKeyRepoStub{apiKeyRepoStub: &apiKeyRepoStub{}}
	svc := &adminServiceImpl{
		userRepo:          userRepo,
		apiKeyRepo:        apiKeyRepo,
		userSubRepo:       nil,
		userGroupRateRepo: nil,
	}

	imported, err := svc.ImportUserAPIKey(context.Background(), 7, "sk_import_test_1234567890", "My Imported Key")

	require.NoError(t, err)
	require.NotNil(t, imported)
	require.Equal(t, "sk_import_test_1234567890", apiKeyRepo.existsKey)
	require.NotNil(t, apiKeyRepo.created)
	require.Equal(t, int64(7), apiKeyRepo.created.UserID)
	require.Equal(t, "sk_import_test_1234567890", apiKeyRepo.created.Key)
	require.Equal(t, StatusActive, apiKeyRepo.created.Status)
	require.Equal(t, "My Imported Key", apiKeyRepo.created.Name)
}

func TestAdminService_ImportUserAPIKey_FallsBackToDefaultName(t *testing.T) {
	userRepo := &mockUserRepo{
		getByIDUser: &User{
			ID:     7,
			Email:  "import@example.com",
			Status: StatusActive,
			Role:   RoleUser,
		},
	}
	apiKeyRepo := &importAPIKeyRepoStub{apiKeyRepoStub: &apiKeyRepoStub{}}
	svc := &adminServiceImpl{
		userRepo:          userRepo,
		apiKeyRepo:        apiKeyRepo,
		userSubRepo:       nil,
		userGroupRateRepo: nil,
	}

	imported, err := svc.ImportUserAPIKey(context.Background(), 7, "sk_import_test_1234567890", "   ")

	require.NoError(t, err)
	require.NotNil(t, imported)
	require.NotNil(t, apiKeyRepo.created)
	require.Equal(t, "Imported Key 34567890", apiKeyRepo.created.Name)
}

func TestAdminService_ImportUserAPIKey_RejectsShortKey(t *testing.T) {
	userRepo := &mockUserRepo{
		getByIDUser: &User{ID: 7, Status: StatusActive, Role: RoleUser},
	}
	apiKeyRepo := &importAPIKeyRepoStub{apiKeyRepoStub: &apiKeyRepoStub{}}
	svc := &adminServiceImpl{
		userRepo:          userRepo,
		apiKeyRepo:        apiKeyRepo,
		userSubRepo:       nil,
		userGroupRateRepo: nil,
	}

	imported, err := svc.ImportUserAPIKey(context.Background(), 7, "short", "Some Name")

	require.Nil(t, imported)
	require.ErrorIs(t, err, ErrAPIKeyTooShort)
	require.Nil(t, apiKeyRepo.created)
}

func TestAdminService_ImportUserAPIKey_RejectsDuplicateKey(t *testing.T) {
	userRepo := &mockUserRepo{
		getByIDUser: &User{ID: 7, Status: StatusActive, Role: RoleUser},
	}
	apiKeyRepo := &importAPIKeyRepoStub{
		apiKeyRepoStub: &apiKeyRepoStub{},
		exists:         true,
	}
	svc := &adminServiceImpl{
		userRepo:          userRepo,
		apiKeyRepo:        apiKeyRepo,
		userSubRepo:       nil,
		userGroupRateRepo: nil,
	}

	imported, err := svc.ImportUserAPIKey(context.Background(), 7, "sk_import_test_duplicate_1234", "Some Name")

	require.Nil(t, imported)
	require.ErrorIs(t, err, ErrAPIKeyExists)
	require.Nil(t, apiKeyRepo.created)
}
