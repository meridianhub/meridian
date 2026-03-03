package domain

import (
	"context"

	"github.com/google/uuid"
)

// Repository defines persistence operations for identity domain objects.
type Repository interface {
	// Identity operations

	// Save persists a new or updated identity.
	Save(ctx context.Context, identity *Identity) error

	// FindByID retrieves an identity by its unique identifier.
	// Returns ErrIdentityNotFound if no matching record exists.
	FindByID(ctx context.Context, id uuid.UUID) (*Identity, error)

	// FindByEmail retrieves an identity by email address within the tenant scope.
	// Returns ErrIdentityNotFound if no matching record exists.
	FindByEmail(ctx context.Context, email string) (*Identity, error)

	// ListByTenant returns all identities within the current tenant context.
	ListByTenant(ctx context.Context) ([]*Identity, error)

	// Role assignment operations

	// SaveRoleAssignment persists a new or updated role assignment.
	SaveRoleAssignment(ctx context.Context, assignment *RoleAssignment) error

	// FindRoleAssignments returns all role assignments for the given identity.
	FindRoleAssignments(ctx context.Context, identityID uuid.UUID) ([]*RoleAssignment, error)

	// Compound operations

	// SaveIdentityWithInvitation atomically persists both an identity and an
	// invitation within a single transaction. This prevents partial commits
	// where one entity is saved but the other fails.
	SaveIdentityWithInvitation(ctx context.Context, identity *Identity, invitation *Invitation) error

	// Invitation operations

	// SaveInvitation persists a new or updated invitation.
	SaveInvitation(ctx context.Context, invitation *Invitation) error

	// FindInvitationByTokenHash retrieves an invitation by the SHA256 hash of its token.
	// Returns ErrInvitationNotFound if no matching invitation exists.
	FindInvitationByTokenHash(ctx context.Context, tokenHash string) (*Invitation, error)
}
