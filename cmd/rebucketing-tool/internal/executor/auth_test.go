package executor

import (
	"context"
	"errors"
	"testing"

	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockClaimsContext creates a context with claims for testing.
func mockClaimsContext(userID string, roles []string) context.Context {
	claims := &auth.Claims{
		UserID: userID,
		Roles:  roles,
	}
	return context.WithValue(context.Background(), auth.ClaimsContextKey, claims)
}

func TestAdminAuthorizer_AuthorizeRebucketing(t *testing.T) {
	authorizer := NewAdminAuthorizer()

	t.Run("authorizes admin user", func(t *testing.T) {
		ctx := mockClaimsContext("admin-user", []string{"admin"})

		userID, err := authorizer.AuthorizeRebucketing(ctx)

		require.NoError(t, err)
		assert.Equal(t, "admin-user", userID)
	})

	t.Run("authorizes user with multiple roles including admin", func(t *testing.T) {
		ctx := mockClaimsContext("multi-role-user", []string{"operator", "admin", "auditor"})

		userID, err := authorizer.AuthorizeRebucketing(ctx)

		require.NoError(t, err)
		assert.Equal(t, "multi-role-user", userID)
	})

	t.Run("rejects operator user", func(t *testing.T) {
		ctx := mockClaimsContext("operator-user", []string{"operator"})

		userID, err := authorizer.AuthorizeRebucketing(ctx)

		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrUnauthorized))
		assert.Empty(t, userID)

		// Verify error details
		var unauthorizedErr *UnauthorizedError
		require.ErrorAs(t, err, &unauthorizedErr)
		assert.Equal(t, "operator-user", unauthorizedErr.UserID)
		assert.Equal(t, "admin", unauthorizedErr.RequiredRole)
		assert.Equal(t, []string{"operator"}, unauthorizedErr.ActualRoles)
	})

	t.Run("rejects auditor user", func(t *testing.T) {
		ctx := mockClaimsContext("auditor-user", []string{"auditor"})

		_, err := authorizer.AuthorizeRebucketing(ctx)

		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrUnauthorized))
	})

	t.Run("rejects user with no roles", func(t *testing.T) {
		ctx := mockClaimsContext("no-role-user", []string{})

		_, err := authorizer.AuthorizeRebucketing(ctx)

		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrUnauthorized))
	})

	t.Run("returns error for missing claims", func(t *testing.T) {
		ctx := context.Background()

		_, err := authorizer.AuthorizeRebucketing(ctx)

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrMissingClaims)
	})

	t.Run("returns error for nil context", func(t *testing.T) {
		_, err := authorizer.AuthorizeRebucketing(nil) //nolint:staticcheck // testing nil context

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrMissingClaims)
	})
}

func TestAdminAuthorizer_GetUserFromContext(t *testing.T) {
	authorizer := NewAdminAuthorizer()

	t.Run("returns user ID from context", func(t *testing.T) {
		ctx := mockClaimsContext("test-user", []string{"admin"})

		userID := authorizer.GetUserFromContext(ctx)

		assert.Equal(t, "test-user", userID)
	})

	t.Run("returns system for missing claims", func(t *testing.T) {
		ctx := context.Background()

		userID := authorizer.GetUserFromContext(ctx)

		assert.Equal(t, "system", userID)
	})

	t.Run("returns system for nil context", func(t *testing.T) {
		userID := authorizer.GetUserFromContext(nil) //nolint:staticcheck // testing nil context

		assert.Equal(t, "system", userID)
	})

	t.Run("returns system for empty user ID", func(t *testing.T) {
		ctx := mockClaimsContext("", []string{"admin"})

		userID := authorizer.GetUserFromContext(ctx)

		assert.Equal(t, "system", userID)
	})
}

func TestAdminAuthorizer_HasAnyRole(t *testing.T) {
	authorizer := NewAdminAuthorizer()

	t.Run("returns true when user has one of the roles", func(t *testing.T) {
		ctx := mockClaimsContext("user", []string{"operator"})

		hasRole := authorizer.HasAnyRole(ctx, auth.RoleAdmin, auth.RoleOperator)

		assert.True(t, hasRole)
	})

	t.Run("returns false when user has none of the roles", func(t *testing.T) {
		ctx := mockClaimsContext("user", []string{"auditor"})

		hasRole := authorizer.HasAnyRole(ctx, auth.RoleAdmin, auth.RoleOperator)

		assert.False(t, hasRole)
	})

	t.Run("returns false for missing claims", func(t *testing.T) {
		ctx := context.Background()

		hasRole := authorizer.HasAnyRole(ctx, auth.RoleAdmin)

		assert.False(t, hasRole)
	})
}
