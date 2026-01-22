// Package persistence provides database persistence for the party domain
package persistence

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/db"
	"gorm.io/gorm"
)

// Verification repository errors
var (
	ErrVerificationNotFound = errors.New("verification not found")
)

// VerificationRepository provides persistence operations for party verifications
type VerificationRepository struct {
	db *gorm.DB
}

// NewVerificationRepository creates a new verification repository
func NewVerificationRepository(database *gorm.DB) *VerificationRepository {
	return &VerificationRepository{db: database}
}

// DB returns the underlying database connection for transaction support.
func (r *VerificationRepository) DB() *gorm.DB {
	return r.db
}

// WithTx returns a new VerificationRepository that uses the provided transaction.
func (r *VerificationRepository) WithTx(tx *gorm.DB) *VerificationRepository {
	return &VerificationRepository{db: tx}
}

// withTenantTransaction executes the given function with tenant scoping.
// The system is always in multi-tenant mode, so this wraps the function in a transaction
// and sets the search_path.
func (r *VerificationRepository) withTenantTransaction(ctx context.Context, fn func(tx *gorm.DB) error) error {
	return db.WithGormTenantTransaction(ctx, r.db, fn)
}

// CreateVerification inserts a new verification record.
// Returns the created verification with populated ID.
func (r *VerificationRepository) CreateVerification(ctx context.Context, verification *PartyVerificationEntity) error {
	if verification.ID == uuid.Nil {
		verification.ID = uuid.New()
	}
	verification.CreatedAt = time.Now()
	verification.UpdatedAt = time.Now()
	verification.Version = 1

	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Create(verification).Error
	})
}

// UpdateVerificationStatus updates the status and optionally risk score of a verification.
// Uses optimistic locking via version field.
// Returns ErrVersionConflict if the version doesn't match.
func (r *VerificationRepository) UpdateVerificationStatus(
	ctx context.Context,
	verificationID uuid.UUID,
	status string,
	riskScore *float64,
	reason *string,
	completedAt *time.Time,
	currentVersion int64,
) error {
	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		updates := map[string]interface{}{
			"status":     status,
			"version":    currentVersion + 1,
			"updated_at": time.Now(),
		}

		if riskScore != nil {
			updates["risk_score"] = *riskScore
		}
		if reason != nil {
			updates["reason"] = *reason
		}
		if completedAt != nil {
			updates["completed_at"] = *completedAt
		}

		result := tx.Model(&PartyVerificationEntity{}).
			Where("id = ? AND version = ?", verificationID, currentVersion).
			Updates(updates)

		if result.Error != nil {
			return result.Error
		}

		if result.RowsAffected == 0 {
			return ErrVersionConflict
		}

		return nil
	})
}

// GetVerificationByID retrieves a verification by its internal UUID.
func (r *VerificationRepository) GetVerificationByID(ctx context.Context, id uuid.UUID) (*PartyVerificationEntity, error) {
	var verification *PartyVerificationEntity
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var entity PartyVerificationEntity
		result := tx.Where("id = ?", id).First(&entity)

		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return ErrVerificationNotFound
		}
		if result.Error != nil {
			return result.Error
		}

		verification = &entity
		return nil
	})
	if err != nil {
		return nil, err
	}
	return verification, nil
}

// GetVerificationByProviderID retrieves a verification by the provider's external verification ID.
func (r *VerificationRepository) GetVerificationByProviderID(ctx context.Context, verificationID string) (*PartyVerificationEntity, error) {
	var verification *PartyVerificationEntity
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var entity PartyVerificationEntity
		result := tx.Where("verification_id = ?", verificationID).First(&entity)

		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return ErrVerificationNotFound
		}
		if result.Error != nil {
			return result.Error
		}

		verification = &entity
		return nil
	})
	if err != nil {
		return nil, err
	}
	return verification, nil
}

// ListVerificationsByParty returns all verifications for a party in chronological order (oldest first).
func (r *VerificationRepository) ListVerificationsByParty(ctx context.Context, partyID uuid.UUID) ([]PartyVerificationEntity, error) {
	var verifications []PartyVerificationEntity
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Where("party_id = ?", partyID).
			Order("created_at ASC").
			Find(&verifications).Error
	})
	return verifications, err
}

// ListPendingVerifications returns all verifications with PENDING status.
// Useful for polling/retry mechanisms.
func (r *VerificationRepository) ListPendingVerifications(ctx context.Context) ([]PartyVerificationEntity, error) {
	var verifications []PartyVerificationEntity
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Where("status = ?", "PENDING").
			Order("created_at ASC").
			Find(&verifications).Error
	})
	return verifications, err
}

// UpdateVerificationMetadata updates only the metadata field for a verification.
// This is separate from status updates to allow metadata enrichment without
// triggering optimistic locking checks.
func (r *VerificationRepository) UpdateVerificationMetadata(
	ctx context.Context,
	verificationID uuid.UUID,
	metadata string,
) error {
	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		updates := map[string]interface{}{
			"metadata":   metadata,
			"updated_at": time.Now(),
		}

		result := tx.Model(&PartyVerificationEntity{}).
			Where("id = ?", verificationID).
			Updates(updates)

		if result.Error != nil {
			return result.Error
		}

		if result.RowsAffected == 0 {
			return ErrVerificationNotFound
		}

		return nil
	})
}
