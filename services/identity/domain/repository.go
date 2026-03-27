package domain

import (
	"context"
	"time"

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

	// SaveIdentityWithRoles atomically persists an identity and its role assignments
	// within a single transaction. This prevents partial commits where the identity
	// is saved but some role assignments fail.
	SaveIdentityWithRoles(ctx context.Context, identity *Identity, roles []*RoleAssignment) error

	// SaveRoleAssignments atomically persists multiple role assignments within a
	// single transaction.
	SaveRoleAssignments(ctx context.Context, assignments []*RoleAssignment) error

	// Invitation operations

	// SaveInvitation persists a new or updated invitation.
	SaveInvitation(ctx context.Context, invitation *Invitation) error

	// FindInvitationByTokenHash retrieves an invitation by the SHA256 hash of its token.
	// Returns ErrInvitationNotFound if no matching invitation exists.
	FindInvitationByTokenHash(ctx context.Context, tokenHash string) (*Invitation, error)

	// Verification token operations

	// SaveVerificationToken persists a new verification token.
	SaveVerificationToken(ctx context.Context, token *VerificationToken) error

	// FindVerificationTokenByHash retrieves a verification token by its SHA256 hash.
	// Returns ErrVerificationTokenNotFound if no matching token exists.
	FindVerificationTokenByHash(ctx context.Context, hash string) (*VerificationToken, error)

	// CountVerificationTokensInWindow counts unconsumed verification tokens created
	// for the given identity within the specified time window. Used for rate limiting.
	CountVerificationTokensInWindow(ctx context.Context, identityID uuid.UUID, window time.Duration) (int, error)

	// Password reset token operations

	// SavePasswordResetToken persists a new password reset token.
	SavePasswordResetToken(ctx context.Context, token *PasswordResetToken) error

	// FindPasswordResetTokenByHash retrieves a password reset token by its SHA256 hash.
	// Returns ErrPasswordResetTokenNotFound if no matching token exists.
	FindPasswordResetTokenByHash(ctx context.Context, hash string) (*PasswordResetToken, error)

	// CountPasswordResetTokensInWindow counts unconsumed password reset tokens created
	// for the given identity within the specified time window. Used for rate limiting.
	CountPasswordResetTokensInWindow(ctx context.Context, identityID uuid.UUID, window time.Duration) (int, error)

	// MarkPasswordResetTokensConsumedForIdentity marks all unconsumed password reset tokens
	// for the given identity as consumed. Used when a password is successfully reset.
	MarkPasswordResetTokensConsumedForIdentity(ctx context.Context, identityID uuid.UUID) error
}
