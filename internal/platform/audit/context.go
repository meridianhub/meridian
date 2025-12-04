// Package audit provides utilities for extracting audit information from request context.
package audit

import (
	"context"

	"github.com/meridianhub/meridian/internal/platform/auth"
)

const (
	// DefaultAuditUser is used when no user can be extracted from context.
	// This occurs for system operations, background jobs, or unauthenticated requests.
	DefaultAuditUser = "system"
)

// GetUserFromContext extracts the user identifier from the request context.
// Returns the user ID from JWT claims if available, otherwise returns DefaultAuditUser.
// This function is safe to call with any context and will never return an empty string.
func GetUserFromContext(ctx context.Context) string {
	if ctx == nil {
		return DefaultAuditUser
	}

	userID, ok := auth.GetUserIDFromContext(ctx)
	if !ok || userID == "" {
		return DefaultAuditUser
	}

	return userID
}
