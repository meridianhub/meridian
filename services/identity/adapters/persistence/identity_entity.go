// Package persistence provides database persistence for the identity domain.
package persistence

import (
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/identity/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// IdentityEntity represents the database persistence model for identities.
// This entity must match the schema defined in migrations for the identity service.
// The mapping between domain model and entity is handled by toEntity/toDomain functions.
type IdentityEntity struct {
	// Primary key
	ID uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`

	// Tenant isolation
	TenantID string `gorm:"column:tenant_id;type:varchar(50);not null;default:''"`

	// Business fields
	Email          string `gorm:"column:email;type:varchar(255);not null"`
	Status         string `gorm:"column:status;type:varchar(30);not null;default:'PENDING_INVITE'"`
	PasswordHash   string `gorm:"column:password_hash;type:varchar(255);not null;default:''"`
	ExternalIDP    string `gorm:"column:external_idp;type:varchar(100);not null;default:''"`
	ExternalSub    string `gorm:"column:external_sub;type:varchar(255);not null;default:''"`
	FailedAttempts int    `gorm:"column:failed_attempts;not null;default:0"`

	// Optimistic locking
	Version int64 `gorm:"column:version;not null;default:1"`

	// Audit fields
	CreatedAt time.Time  `gorm:"column:created_at;not null;default:now()"`
	UpdatedAt time.Time  `gorm:"column:updated_at;not null;default:now()"`
	DeletedAt *time.Time `gorm:"column:deleted_at;index"`
}

// TableName overrides the default table name.
// Uses singular, unqualified name — search_path routing handles tenant schema.
func (IdentityEntity) TableName() string {
	return "identity"
}

// ToEntity converts a domain Identity to a persistence IdentityEntity.
func ToEntity(identity *domain.Identity) *IdentityEntity {
	return &IdentityEntity{
		ID:             identity.ID(),
		TenantID:       string(identity.TenantID()),
		Email:          identity.Email(),
		Status:         string(identity.Status()),
		PasswordHash:   identity.PasswordHash(),
		ExternalIDP:    identity.ExternalIDP(),
		ExternalSub:    identity.ExternalSub(),
		FailedAttempts: identity.FailedAttempts(),
		Version:        identity.Version(),
		CreatedAt:      identity.CreatedAt(),
		UpdatedAt:      identity.UpdatedAt(),
	}
}

// ToDomain converts a persistence IdentityEntity to a domain Identity.
func ToDomain(entity *IdentityEntity) *domain.Identity {
	return domain.ReconstructIdentity(
		entity.ID,
		tenant.TenantID(entity.TenantID),
		entity.Email,
		domain.IdentityStatus(entity.Status),
		entity.PasswordHash,
		entity.ExternalIDP,
		entity.ExternalSub,
		entity.FailedAttempts,
		entity.CreatedAt,
		entity.UpdatedAt,
		entity.Version,
	)
}
