package domain

import (
	"time"

	"github.com/google/uuid"
)

// Role represents a named permission level within the platform.
type Role string

// Defined role constants in ascending privilege order.
const (
	RoleViewer      Role = "VIEWER"
	RoleOperator    Role = "OPERATOR"
	RoleAdmin       Role = "ADMIN"
	RoleTenantOwner Role = "TENANT_OWNER"
	RolePlatform    Role = "PLATFORM"
)

// roleHierarchy maps each role to its numeric privilege level.
// Higher values indicate greater privilege.
var roleHierarchy = map[Role]int{
	RoleViewer:      1,
	RoleOperator:    2,
	RoleAdmin:       3,
	RoleTenantOwner: 4,
	RolePlatform:    5,
}

// IsValidRole returns true if the role string is a recognized platform role.
func IsValidRole(r string) bool {
	_, ok := roleHierarchy[Role(r)]
	return ok
}

// RoleAssignment represents a granted role for an identity.
type RoleAssignment struct {
	id         uuid.UUID
	identityID uuid.UUID
	grantedBy  uuid.UUID
	role       Role
	expiresAt  *time.Time
	revokedAt  *time.Time
	revokedBy  *uuid.UUID
	createdAt  time.Time
	updatedAt  time.Time
}

// NewRoleAssignment creates a new active role assignment.
// Returns ErrInvalidRole if the role is not recognized.
func NewRoleAssignment(identityID, grantedBy uuid.UUID, role string) (*RoleAssignment, error) {
	if !IsValidRole(role) {
		return nil, ErrInvalidRole
	}
	now := time.Now()
	return &RoleAssignment{
		id:         uuid.New(),
		identityID: identityID,
		grantedBy:  grantedBy,
		role:       Role(role),
		createdAt:  now,
		updatedAt:  now,
	}, nil
}

// ReconstructRoleAssignment recreates a RoleAssignment from persistence layer data.
func ReconstructRoleAssignment(
	id uuid.UUID,
	identityID uuid.UUID,
	grantedBy uuid.UUID,
	role Role,
	expiresAt *time.Time,
	revokedAt *time.Time,
	revokedBy *uuid.UUID,
	createdAt time.Time,
	updatedAt time.Time,
) *RoleAssignment {
	return &RoleAssignment{
		id:         id,
		identityID: identityID,
		grantedBy:  grantedBy,
		role:       role,
		expiresAt:  expiresAt,
		revokedAt:  revokedAt,
		revokedBy:  revokedBy,
		createdAt:  createdAt,
		updatedAt:  updatedAt,
	}
}

// ID returns the role assignment's unique identifier.
func (r *RoleAssignment) ID() uuid.UUID {
	return r.id
}

// IdentityID returns the identity this assignment belongs to.
func (r *RoleAssignment) IdentityID() uuid.UUID {
	return r.identityID
}

// GrantedBy returns the identity that granted this role.
func (r *RoleAssignment) GrantedBy() uuid.UUID {
	return r.grantedBy
}

// Role returns the granted role.
func (r *RoleAssignment) Role() Role {
	return r.role
}

// ExpiresAt returns the optional expiry time.
func (r *RoleAssignment) ExpiresAt() *time.Time {
	return r.expiresAt
}

// RevokedAt returns when the assignment was revoked, or nil if still active.
func (r *RoleAssignment) RevokedAt() *time.Time {
	return r.revokedAt
}

// RevokedBy returns who revoked the assignment, or nil if still active.
func (r *RoleAssignment) RevokedBy() *uuid.UUID {
	return r.revokedBy
}

// CreatedAt returns when the assignment was created.
func (r *RoleAssignment) CreatedAt() time.Time {
	return r.createdAt
}

// UpdatedAt returns when the assignment was last updated.
func (r *RoleAssignment) UpdatedAt() time.Time {
	return r.updatedAt
}

// IsActive returns true when the assignment has not been revoked and has not expired.
func (r *RoleAssignment) IsActive() bool {
	if r.revokedAt != nil {
		return false
	}
	if r.expiresAt != nil && !time.Now().Before(*r.expiresAt) {
		return false
	}
	return true
}

// Revoke marks this role assignment as revoked.
// Returns ErrRoleAlreadyRevoked if it has already been revoked.
func (r *RoleAssignment) Revoke(revokedBy uuid.UUID) error {
	if r.revokedAt != nil {
		return ErrRoleAlreadyRevoked
	}
	now := time.Now()
	r.revokedAt = &now
	r.revokedBy = &revokedBy
	r.updatedAt = now
	return nil
}

// minGranterLevel is the minimum privilege level required to grant any role.
// Only Admin (level 3) and above may grant roles.
const minGranterLevel = 3

// CanGrant returns true if an identity holding granterRole is permitted to grant targetRole.
// A granter must have at least Admin-level privilege and must hold a strictly higher privilege
// level than the target role.
func CanGrant(granterRole, targetRole string) bool {
	granterLevel, ok := roleHierarchy[Role(granterRole)]
	if !ok {
		return false
	}
	if granterLevel < minGranterLevel {
		return false
	}
	targetLevel, ok := roleHierarchy[Role(targetRole)]
	if !ok {
		return false
	}
	return granterLevel > targetLevel
}
