package persistence

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/identity/domain"
	"github.com/meridianhub/meridian/shared/platform/db"
	"gorm.io/gorm"
)

// Repository provides persistence operations for the identity domain.
// It implements domain.Repository.
type Repository struct {
	db *gorm.DB
}

// NewRepository creates a new identity Repository.
func NewRepository(database *gorm.DB) *Repository {
	return &Repository{db: database}
}

// withTenantTransaction executes fn inside a tenant-scoped transaction.
func (r *Repository) withTenantTransaction(ctx context.Context, fn func(tx *gorm.DB) error) error {
	return db.WithGormTenantTransaction(ctx, r.db, fn)
}

// Save persists a new or updated identity using optimistic locking.
// For creates: inserts with version=1.
// For updates: applies WHERE id=? AND version=expectedVersion to detect conflicts.
func (r *Repository) Save(ctx context.Context, identity *domain.Identity) error {
	entity := ToEntity(identity)

	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var existing IdentityEntity
		result := tx.Where("id = ? AND deleted_at IS NULL", entity.ID).First(&existing)

		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			// New identity — insert
			if err := tx.Create(entity).Error; err != nil {
				if isDuplicateKeyError(err) {
					return domain.ErrEmailAlreadyExists
				}
				return err
			}
			identity.UpdateBaseVersion(entity.Version)
			return nil
		}

		if result.Error != nil {
			return result.Error
		}

		// Existing identity — optimistic locking update.
		// We use identity.BaseVersion() as the WHERE guard: it records the version
		// that was in the DB when the identity was loaded (set by ReconstructIdentity or
		// after a prior successful save via UpdateBaseVersion).
		// This correctly handles multiple domain mutations between load and save.
		updateResult := tx.Model(&IdentityEntity{}).
			Where("id = ? AND version = ? AND deleted_at IS NULL", entity.ID, identity.BaseVersion()).
			Updates(map[string]interface{}{
				"email":           entity.Email,
				"status":          entity.Status,
				"password_hash":   entity.PasswordHash,
				"external_idp":    entity.ExternalIDP,
				"external_sub":    entity.ExternalSub,
				"failed_attempts": entity.FailedAttempts,
				"version":         entity.Version,
				"updated_at":      entity.UpdatedAt,
			})

		if updateResult.Error != nil {
			if isDuplicateKeyError(updateResult.Error) {
				return domain.ErrEmailAlreadyExists
			}
			return updateResult.Error
		}

		if updateResult.RowsAffected == 0 {
			return ErrVersionConflict
		}

		identity.UpdateBaseVersion(entity.Version)
		return nil
	})
}

// FindByID retrieves an identity by its unique identifier.
// Returns domain.ErrIdentityNotFound if no matching record exists.
func (r *Repository) FindByID(ctx context.Context, id uuid.UUID) (*domain.Identity, error) {
	var identity *domain.Identity
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var entity IdentityEntity
		result := tx.Where("id = ? AND deleted_at IS NULL", id).First(&entity)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return domain.ErrIdentityNotFound
		}
		if result.Error != nil {
			return result.Error
		}
		identity = ToDomain(&entity)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return identity, nil
}

// FindByEmail retrieves an identity by email address within the tenant scope.
// Returns domain.ErrIdentityNotFound if no matching record exists.
func (r *Repository) FindByEmail(ctx context.Context, email string) (*domain.Identity, error) {
	var identity *domain.Identity
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var entity IdentityEntity
		result := tx.Where("email = ? AND deleted_at IS NULL", email).First(&entity)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return domain.ErrIdentityNotFound
		}
		if result.Error != nil {
			return result.Error
		}
		identity = ToDomain(&entity)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return identity, nil
}

// ListByTenant returns all non-deleted identities within the current tenant context.
func (r *Repository) ListByTenant(ctx context.Context) ([]*domain.Identity, error) {
	var identities []*domain.Identity
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var entities []IdentityEntity
		if err := tx.Where("deleted_at IS NULL").Order("created_at ASC").Find(&entities).Error; err != nil {
			return err
		}
		identities = make([]*domain.Identity, len(entities))
		for i := range entities {
			identities[i] = ToDomain(&entities[i])
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return identities, nil
}

// SaveRoleAssignment persists a new or updated role assignment.
func (r *Repository) SaveRoleAssignment(ctx context.Context, assignment *domain.RoleAssignment) error {
	entity := toRoleAssignmentEntity(assignment)

	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var existing RoleAssignmentEntity
		result := tx.Where("id = ?", entity.ID).First(&existing)

		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return tx.Create(entity).Error
		}

		if result.Error != nil {
			return result.Error
		}

		// Update mutable fields (revocation, expiry, updated_at)
		return tx.Model(&RoleAssignmentEntity{}).
			Where("id = ?", entity.ID).
			Updates(map[string]interface{}{
				"revoked_at": entity.RevokedAt,
				"revoked_by": entity.RevokedBy,
				"expires_at": entity.ExpiresAt,
				"updated_at": entity.UpdatedAt,
			}).Error
	})
}

// FindRoleAssignments returns all role assignments for the given identity.
func (r *Repository) FindRoleAssignments(ctx context.Context, identityID uuid.UUID) ([]*domain.RoleAssignment, error) {
	var assignments []*domain.RoleAssignment
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var entities []RoleAssignmentEntity
		if err := tx.Where("identity_id = ?", identityID).Find(&entities).Error; err != nil {
			return err
		}
		assignments = make([]*domain.RoleAssignment, len(entities))
		for i := range entities {
			assignments[i] = toRoleAssignmentDomain(&entities[i])
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return assignments, nil
}

// SaveIdentityWithInvitation atomically persists both an identity and an
// invitation within a single transaction.
func (r *Repository) SaveIdentityWithInvitation(ctx context.Context, identity *domain.Identity, invitation *domain.Invitation) error {
	identEntity := ToEntity(identity)
	invEntity := toInvitationEntity(invitation)

	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		// Save invitation first (mark token as consumed)
		var existingInv InvitationEntity
		invResult := tx.Where("id = ?", invEntity.ID).First(&existingInv)
		if errors.Is(invResult.Error, gorm.ErrRecordNotFound) {
			if err := tx.Create(invEntity).Error; err != nil {
				return err
			}
		} else if invResult.Error != nil {
			return invResult.Error
		} else {
			if err := tx.Model(&InvitationEntity{}).
				Where("id = ?", invEntity.ID).
				Updates(map[string]interface{}{
					"status":     invEntity.Status,
					"updated_at": invEntity.UpdatedAt,
				}).Error; err != nil {
				return err
			}
		}

		// Save identity
		var existingIdent IdentityEntity
		identResult := tx.Where("id = ? AND deleted_at IS NULL", identEntity.ID).First(&existingIdent)
		if errors.Is(identResult.Error, gorm.ErrRecordNotFound) {
			if err := tx.Create(identEntity).Error; err != nil {
				if isDuplicateKeyError(err) {
					return domain.ErrEmailAlreadyExists
				}
				return err
			}
			identity.UpdateBaseVersion(identEntity.Version)
			return nil
		}
		if identResult.Error != nil {
			return identResult.Error
		}

		// Use identity.BaseVersion() as the optimistic lock guard.
		// BaseVersion records the DB version at load time; multiple mutations may
		// occur between load and save, so this is more correct than identEntity.Version-1.
		updateResult := tx.Model(&IdentityEntity{}).
			Where("id = ? AND version = ? AND deleted_at IS NULL", identEntity.ID, identity.BaseVersion()).
			Updates(map[string]interface{}{
				"email":           identEntity.Email,
				"status":          identEntity.Status,
				"password_hash":   identEntity.PasswordHash,
				"external_idp":    identEntity.ExternalIDP,
				"external_sub":    identEntity.ExternalSub,
				"failed_attempts": identEntity.FailedAttempts,
				"version":         identEntity.Version,
				"updated_at":      identEntity.UpdatedAt,
			})
		if updateResult.Error != nil {
			if isDuplicateKeyError(updateResult.Error) {
				return domain.ErrEmailAlreadyExists
			}
			return updateResult.Error
		}
		if updateResult.RowsAffected == 0 {
			return ErrVersionConflict
		}
		identity.UpdateBaseVersion(identEntity.Version)
		return nil
	})
}

// SaveInvitation persists a new or updated invitation.
func (r *Repository) SaveInvitation(ctx context.Context, invitation *domain.Invitation) error {
	entity := toInvitationEntity(invitation)

	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var existing InvitationEntity
		result := tx.Where("id = ?", entity.ID).First(&existing)

		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return tx.Create(entity).Error
		}

		if result.Error != nil {
			return result.Error
		}

		// Update mutable fields (status, updated_at)
		return tx.Model(&InvitationEntity{}).
			Where("id = ?", entity.ID).
			Updates(map[string]interface{}{
				"status":     entity.Status,
				"updated_at": entity.UpdatedAt,
			}).Error
	})
}

// FindInvitationByTokenHash retrieves an invitation by the SHA256 hash of its token.
// Returns domain.ErrInvitationNotFound if no matching invitation exists.
func (r *Repository) FindInvitationByTokenHash(ctx context.Context, tokenHash string) (*domain.Invitation, error) {
	var invitation *domain.Invitation
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var entity InvitationEntity
		result := tx.Where("token_hash = ?", tokenHash).First(&entity)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return domain.ErrInvitationNotFound
		}
		if result.Error != nil {
			return result.Error
		}
		invitation = toInvitationDomain(&entity)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return invitation, nil
}

// SaveIdentityWithRoles atomically persists an identity and its role assignments
// within a single transaction.
func (r *Repository) SaveIdentityWithRoles(ctx context.Context, identity *domain.Identity, roles []*domain.RoleAssignment) error {
	identEntity := ToEntity(identity)

	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		// Insert identity (bootstrap always creates a new one).
		if err := tx.Create(identEntity).Error; err != nil {
			if isDuplicateKeyError(err) {
				return domain.ErrEmailAlreadyExists
			}
			return err
		}
		identity.UpdateBaseVersion(identEntity.Version)

		// Insert all role assignments.
		for _, ra := range roles {
			entity := toRoleAssignmentEntity(ra)
			if err := tx.Create(entity).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// SaveRoleAssignments atomically persists multiple role assignments within a
// single transaction.
func (r *Repository) SaveRoleAssignments(ctx context.Context, assignments []*domain.RoleAssignment) error {
	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		for _, ra := range assignments {
			entity := toRoleAssignmentEntity(ra)
			if err := tx.Create(entity).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// SaveVerificationToken persists a new verification token.
func (r *Repository) SaveVerificationToken(ctx context.Context, token *domain.VerificationToken) error {
	entity := toVerificationTokenEntity(token)
	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Create(entity).Error
	})
}

// FindVerificationTokenByHash retrieves a verification token by its SHA256 hash.
func (r *Repository) FindVerificationTokenByHash(ctx context.Context, hash string) (*domain.VerificationToken, error) {
	var token *domain.VerificationToken
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var entity EmailVerificationTokenEntity
		result := tx.Where("token_hash = ?", hash).First(&entity)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return domain.ErrVerificationTokenNotFound
		}
		if result.Error != nil {
			return result.Error
		}
		token = toVerificationTokenDomain(&entity)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return token, nil
}

// CountVerificationTokensInWindow counts unconsumed verification tokens created for
// the given identity within the specified time window.
func (r *Repository) CountVerificationTokensInWindow(ctx context.Context, identityID uuid.UUID, window time.Duration) (int, error) {
	var count int
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var c int64
		result := tx.Model(&EmailVerificationTokenEntity{}).
			Where("identity_id = ? AND consumed_at IS NULL AND created_at >= ?", identityID, time.Now().Add(-window)).
			Count(&c)
		if result.Error != nil {
			return result.Error
		}
		count = int(c)
		return nil
	})
	return count, err
}

// SavePasswordResetToken persists a new password reset token.
func (r *Repository) SavePasswordResetToken(ctx context.Context, token *domain.PasswordResetToken) error {
	entity := toPasswordResetTokenEntity(token)
	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Create(entity).Error
	})
}

// FindPasswordResetTokenByHash retrieves a password reset token by its SHA256 hash.
func (r *Repository) FindPasswordResetTokenByHash(ctx context.Context, hash string) (*domain.PasswordResetToken, error) {
	var token *domain.PasswordResetToken
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var entity PasswordResetTokenEntity
		result := tx.Where("token_hash = ?", hash).First(&entity)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return domain.ErrPasswordResetTokenNotFound
		}
		if result.Error != nil {
			return result.Error
		}
		token = toPasswordResetTokenDomain(&entity)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return token, nil
}

// CountPasswordResetTokensInWindow counts unconsumed password reset tokens created for
// the given identity within the specified time window.
func (r *Repository) CountPasswordResetTokensInWindow(ctx context.Context, identityID uuid.UUID, window time.Duration) (int, error) {
	var count int
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var c int64
		result := tx.Model(&PasswordResetTokenEntity{}).
			Where("identity_id = ? AND consumed_at IS NULL AND created_at >= ?", identityID, time.Now().Add(-window)).
			Count(&c)
		if result.Error != nil {
			return result.Error
		}
		count = int(c)
		return nil
	})
	return count, err
}

// MarkPasswordResetTokensConsumedForIdentity marks all unconsumed password reset tokens
// for the given identity as consumed.
func (r *Repository) MarkPasswordResetTokensConsumedForIdentity(ctx context.Context, identityID uuid.UUID) error {
	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		now := time.Now()
		return tx.Model(&PasswordResetTokenEntity{}).
			Where("identity_id = ? AND consumed_at IS NULL", identityID).
			Update("consumed_at", now).Error
	})
}

// ErrVersionConflict is returned when an optimistic locking conflict is detected.
var ErrVersionConflict = domain.ErrVersionConflict

// isDuplicateKeyError returns true when err represents a unique constraint violation.
func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return errors.Is(err, gorm.ErrDuplicatedKey) ||
		strings.Contains(errStr, "23505") ||
		strings.Contains(errStr, "duplicate key") ||
		strings.Contains(errStr, "unique constraint")
}
