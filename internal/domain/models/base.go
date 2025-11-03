package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/internal/platform/auth"
	"gorm.io/gorm"
)

const (
	// SystemUser is the default user ID for background jobs and migrations
	SystemUser = "system"
)

// BaseModel contains common fields for all domain models.
//
// NOTE: Exported fields violate strict immutability but are required for GORM ORM compatibility.
// GORM requires exported fields for:
// - Scanning database rows into structs
// - Marshaling structs to database queries
// - JSON serialization/deserialization
//
// Trade-off: We accept mutable fields for GORM compatibility while enforcing immutability through:
// - Constructor functions (NewAccount, NewCustomer, NewTransaction)
// - GORM hooks (BeforeCreate, BeforeUpdate) for audit trail
// - API layer validation preventing invalid states
type BaseModel struct {
	ID        uuid.UUID  `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	CreatedAt time.Time  `gorm:"not null;default:now()" json:"created_at"`
	CreatedBy string     `gorm:"type:varchar(100);not null" json:"created_by"` // Populated from JWT context
	UpdatedAt time.Time  `gorm:"not null;default:now()" json:"updated_at"`
	UpdatedBy string     `gorm:"type:varchar(100);not null" json:"updated_by"` // Populated from JWT context
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
// Returns empty string if not found.
func getUserIDFromContext(ctx any) string {
	if ctx == nil {
		return ""
	}

	// Try to extract user_id from context (set by JWT auth interceptor)
	if userID, ok := ctx.(interface{ Value(interface{}) interface{} }).Value(auth.UserIDContextKey).(string); ok {
		return userID
	}

	return ""
}
