package audit

import (
	"context"
	"testing"

	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/stretchr/testify/assert"
)

func TestGetUserFromContext_WithUserID(t *testing.T) {
	userID := "user-12345"
	ctx := context.WithValue(context.Background(), auth.UserIDContextKey, userID)

	result := GetUserFromContext(ctx)

	assert.Equal(t, userID, result)
}

func TestGetUserFromContext_WithNilContext(t *testing.T) {
	// Test that the function handles nil context gracefully
	var nilCtx context.Context
	result := GetUserFromContext(nilCtx)

	assert.Equal(t, DefaultAuditUser, result)
}

func TestGetUserFromContext_WithEmptyContext(t *testing.T) {
	ctx := context.Background()

	result := GetUserFromContext(ctx)

	assert.Equal(t, DefaultAuditUser, result)
}

func TestGetUserFromContext_WithEmptyUserID(t *testing.T) {
	// User ID exists but is empty string
	ctx := context.WithValue(context.Background(), auth.UserIDContextKey, "")

	result := GetUserFromContext(ctx)

	assert.Equal(t, DefaultAuditUser, result)
}

func TestGetUserFromContext_WithWrongType(t *testing.T) {
	// User ID key exists but value is wrong type
	ctx := context.WithValue(context.Background(), auth.UserIDContextKey, 12345)

	result := GetUserFromContext(ctx)

	assert.Equal(t, DefaultAuditUser, result)
}
