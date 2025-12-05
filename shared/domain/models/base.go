package models

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"gorm.io/gorm"
)

const (
	// SystemUser is the default user ID for background jobs and migrations
	SystemUser = "system"
)

// BaseModel contains common fields for all domain models.
//
// # Immutability Trade-off
//
// Exported fields violate strict immutability but are required for GORM ORM compatibility.
// GORM requires exported fields for scanning, marshaling, and JSON serialization.
// We enforce immutability through constructor functions, GORM hooks, and API validation.
//
// # Audit Field Strategy (CreatedBy/UpdatedBy)
//
// These fields are NOT NULL in the database but automatically populated by GORM hooks:
//
// 1. With JWT Context (normal operation):
//   - BeforeCreate extracts user_id from JWT context → CreatedBy/UpdatedBy
//   - BeforeUpdate extracts user_id from JWT context → UpdatedBy
//
// 2. Without JWT Context (migrations, background jobs):
//   - Falls back to SystemUser constant ("system")
//   - Ensures NOT NULL constraint is always satisfied
//
// 3. Application code should NOT manually set these fields:
//   - Hooks automatically populate from auth context
//   - Manual override only for testing or data imports
//
// See internal/platform/auth for JWT context injection via gRPC interceptors.
type BaseModel struct {
	ID        uuid.UUID  `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	CreatedAt time.Time  `gorm:"not null;default:now()" json:"created_at"`
	CreatedBy string     `gorm:"type:varchar(100);not null" json:"created_by"` // Auto-populated from JWT context or SystemUser
	UpdatedAt time.Time  `gorm:"not null;default:now()" json:"updated_at"`
	UpdatedBy string     `gorm:"type:varchar(100);not null" json:"updated_by"` // Auto-populated from JWT context or SystemUser
	DeletedAt *time.Time `gorm:"index" json:"deleted_at,omitempty"`
}

// BeforeCreate is a GORM hook that runs before INSERT operations.
// It sets the UUID and populates audit fields from JWT context.
//
// NOTE: This method mutates the receiver, which is required by GORM's hook interface.
// GORM expects hooks to modify the model in-place before database operations.
func (base *BaseModel) BeforeCreate(tx *gorm.DB) error {
	// Set UUID if not already set
	if base.ID == uuid.Nil {
		base.ID = uuid.New()
	}

	// Extract user ID from JWT context (set by auth interceptor)
	// Guard against nil tx or nil tx.Statement
	var userID string
	if tx != nil && tx.Statement != nil {
		userID = getUserIDFromContext(tx.Statement.Context)
	}

	if userID != "" {
		base.CreatedBy = userID
		base.UpdatedBy = userID
	} else if base.CreatedBy == "" {
		// Fallback to system for background jobs or migrations
		base.CreatedBy = SystemUser
		base.UpdatedBy = SystemUser
	}

	return nil
}

// BeforeUpdate is a GORM hook that runs before UPDATE operations.
// It populates UpdatedBy from JWT context.
//
// NOTE: This method mutates the receiver, which is required by GORM's hook interface.
func (base *BaseModel) BeforeUpdate(tx *gorm.DB) error {
	// Extract user ID from JWT context
	// Guard against nil tx or nil tx.Statement
	var userID string
	if tx != nil && tx.Statement != nil {
		userID = getUserIDFromContext(tx.Statement.Context)
	}

	if userID != "" {
		base.UpdatedBy = userID
	} else if base.UpdatedBy == "" {
		// Fallback to system for background jobs or migrations
		base.UpdatedBy = SystemUser
	}

	return nil
}

// getUserIDFromContext extracts the user ID from the context.
// Returns empty string if not found or if type assertion fails.
func getUserIDFromContext(ctx any) string {
	if ctx == nil {
		return ""
	}

	// Safely convert to context.Context interface
	stdCtx, ok := ctx.(context.Context)
	if !ok {
		return ""
	}

	// Try to extract user_id from context (set by JWT auth interceptor)
	if userID, ok := stdCtx.Value(auth.UserIDContextKey).(string); ok {
		return userID
	}

	return ""
}
