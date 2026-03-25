package persistence

import (
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/identity/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// RoleAssignmentEntity represents the database persistence model for role assignments.
type RoleAssignmentEntity struct {
	ID         uuid.UUID  `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	TenantID   string     `gorm:"column:tenant_id;type:varchar(50);not null;default:''"`
	IdentityID uuid.UUID  `gorm:"column:identity_id;type:uuid;not null;index:idx_role_assignment_identity"`
	GrantedBy  uuid.UUID  `gorm:"column:granted_by;type:uuid;not null"`
	Role       string     `gorm:"column:role;type:varchar(50);not null"`
	ExpiresAt  *time.Time `gorm:"column:expires_at"`
	RevokedAt  *time.Time `gorm:"column:revoked_at"`
	RevokedBy  *uuid.UUID `gorm:"column:revoked_by;type:uuid"`
	CreatedAt  time.Time  `gorm:"column:created_at;not null;default:now()"`
	UpdatedAt  time.Time  `gorm:"column:updated_at;not null;default:now()"`
}

// TableName overrides the default table name.
// Partial unique index on (identity_id, role) WHERE revoked_at IS NULL is enforced via SQL migration (task 6).
func (RoleAssignmentEntity) TableName() string {
	return "role_assignment"
}

// toRoleAssignmentEntity converts a domain RoleAssignment to a persistence entity.
func toRoleAssignmentEntity(ra *domain.RoleAssignment) *RoleAssignmentEntity {
	return &RoleAssignmentEntity{
		ID:         ra.ID(),
		TenantID:   string(ra.TenantID()),
		IdentityID: ra.IdentityID(),
		GrantedBy:  ra.GrantedBy(),
		Role:       string(ra.Role()),
		ExpiresAt:  ra.ExpiresAt(),
		RevokedAt:  ra.RevokedAt(),
		RevokedBy:  ra.RevokedBy(),
		CreatedAt:  ra.CreatedAt(),
		UpdatedAt:  ra.UpdatedAt(),
	}
}

// toRoleAssignmentDomain converts a persistence entity to a domain RoleAssignment.
func toRoleAssignmentDomain(entity *RoleAssignmentEntity) *domain.RoleAssignment {
	return domain.ReconstructRoleAssignment(
		entity.ID,
		tenant.TenantID(entity.TenantID),
		entity.IdentityID,
		entity.GrantedBy,
		domain.Role(entity.Role),
		entity.ExpiresAt,
		entity.RevokedAt,
		entity.RevokedBy,
		entity.CreatedAt,
		entity.UpdatedAt,
	)
}
