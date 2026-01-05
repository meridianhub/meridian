package executor

import (
	"context"

	"github.com/meridianhub/meridian/shared/platform/auth"
)

const (
	// defaultSystemUser is the user ID returned when no user is found in context.
	defaultSystemUser = "system"
)

// AdminAuthorizer handles authorization checks for rebucketing operations.
// Only users with the admin role are authorized to perform rebucketing.
type AdminAuthorizer struct{}

// NewAdminAuthorizer creates a new admin authorizer.
func NewAdminAuthorizer() *AdminAuthorizer {
	return &AdminAuthorizer{}
}

// AuthorizeRebucketing checks if the current user has admin privileges.
// Returns the user ID if authorized, or an error if not.
func (a *AdminAuthorizer) AuthorizeRebucketing(ctx context.Context) (string, error) {
	if ctx == nil {
		return "", ErrMissingClaims
	}

	claims, ok := auth.GetClaimsFromContext(ctx)
	if !ok {
		return "", ErrMissingClaims
	}

	userID := claims.GetUserID()

	// Check for admin role
	if !claims.HasRole(auth.RoleAdmin.String()) {
		return "", &UnauthorizedError{
			UserID:       userID,
			RequiredRole: auth.RoleAdmin.String(),
			ActualRoles:  claims.GetRoles(),
		}
	}

	return userID, nil
}

// GetUserFromContext extracts the user ID from context for audit logging.
// Returns defaultSystemUser if no user is found (for background operations).
func (a *AdminAuthorizer) GetUserFromContext(ctx context.Context) string {
	if ctx == nil {
		return defaultSystemUser
	}

	claims, ok := auth.GetClaimsFromContext(ctx)
	if !ok {
		return defaultSystemUser
	}

	userID := claims.GetUserID()
	if userID == "" {
		return defaultSystemUser
	}

	return userID
}

// HasAnyRole checks if the user has any of the specified roles.
func (a *AdminAuthorizer) HasAnyRole(ctx context.Context, roles ...auth.Role) bool {
	if ctx == nil {
		return false
	}

	claims, ok := auth.GetClaimsFromContext(ctx)
	if !ok {
		return false
	}

	return auth.HasAnyRole(claims, roles...)
}
