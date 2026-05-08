package dto

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUserFromServiceAdmin_MapsActivityTimestamps(t *testing.T) {
	t.Parallel()

	lastLoginAt := time.Date(2026, time.April, 20, 10, 0, 0, 0, time.UTC)
	lastActiveAt := lastLoginAt.Add(15 * time.Minute)
	lastUsedAt := lastLoginAt.Add(45 * time.Minute)

	out := UserFromServiceAdmin(&service.User{
		ID:           42,
		Email:        "admin@example.com",
		Username:     "admin",
		Role:         service.RoleAdmin,
		Status:       service.StatusActive,
		LastActiveAt: &lastActiveAt,
		LastUsedAt:   &lastUsedAt,
	})

	require.NotNil(t, out)
	require.NotNil(t, out.LastActiveAt)
	require.NotNil(t, out.LastUsedAt)
	require.WithinDuration(t, lastActiveAt, *out.LastActiveAt, time.Second)
	require.WithinDuration(t, lastUsedAt, *out.LastUsedAt, time.Second)
}

func TestUserSubscriptionFromServiceAdmin_MapsNestedUserNotes(t *testing.T) {
	t.Parallel()

	out := UserSubscriptionFromServiceAdmin(&service.UserSubscription{
		ID:    7,
		Notes: "subscription note",
		User: &service.User{
			ID:       42,
			Email:    "user@example.com",
			Username: "user",
			Role:     service.RoleUser,
			Status:   service.StatusActive,
			Notes:    "user note",
		},
	})

	require.NotNil(t, out)
	require.NotNil(t, out.User)
	require.Equal(t, "user note", out.User.Notes)
	require.Equal(t, "subscription note", out.Notes)

	payload, err := json.Marshal(out)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(payload, &decoded))
	require.Equal(t, "subscription note", decoded["notes"])

	user, ok := decoded["user"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "user note", user["notes"])
}
