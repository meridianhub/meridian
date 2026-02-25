package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/internal-account/domain"
	"github.com/meridianhub/meridian/shared/platform/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Lien repository errors
var (
	ErrLienNotFound        = errors.New("lien not found")
	ErrLienVersionConflict = errors.New("version conflict: lien was modified by another transaction")
)

// LienRepository provides persistence operations for liens.
type LienRepository struct {
	db *gorm.DB
}

// NewLienRepository creates a new lien repository.
func NewLienRepository(db *gorm.DB) *LienRepository {
	return &LienRepository{db: db}
}

// WithTx returns a new LienRepository that uses the provided transaction.
func (r *LienRepository) WithTx(tx *gorm.DB) *LienRepository {
	return &LienRepository{db: tx}
}

func (r *LienRepository) withTenantTransaction(ctx context.Context, fn func(tx *gorm.DB) error) error {
	return db.WithGormTenantTransaction(ctx, r.db, fn)
}

// Create inserts a new lien.
func (r *LienRepository) Create(ctx context.Context, lien *domain.Lien) error {
	entity, err := toLienEntity(lien)
	if err != nil {
		return fmt.Errorf("failed to convert lien to entity: %w", err)
	}
	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Create(entity).Error
	})
}

// FindByID retrieves a lien by its UUID.
func (r *LienRepository) FindByID(ctx context.Context, id uuid.UUID) (*domain.Lien, error) {
	var entity LienEntity
	var queryErr error

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Where("id = ?", id).First(&entity)
		if result.Error != nil {
			queryErr = result.Error
			return result.Error
		}
		return nil
	})
	if err != nil {
		if errors.Is(queryErr, gorm.ErrRecordNotFound) {
			return nil, ErrLienNotFound
		}
		return nil, err
	}

	return toLienDomain(&entity)
}

// FindByIDForUpdate retrieves a lien by its UUID with a pessimistic lock.
func (r *LienRepository) FindByIDForUpdate(ctx context.Context, id uuid.UUID) (*domain.Lien, error) {
	var entity LienEntity
	var queryErr error

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", id).
			First(&entity)
		if result.Error != nil {
			queryErr = result.Error
			return result.Error
		}
		return nil
	})
	if err != nil {
		if errors.Is(queryErr, gorm.ErrRecordNotFound) {
			return nil, ErrLienNotFound
		}
		return nil, err
	}

	return toLienDomain(&entity)
}

// FindByAccountID retrieves all liens for an account.
func (r *LienRepository) FindByAccountID(ctx context.Context, accountID uuid.UUID) ([]*domain.Lien, error) {
	var entities []LienEntity

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Where("account_id = ?", accountID).Find(&entities).Error
	})
	if err != nil {
		return nil, err
	}

	return toLienDomainSlice(entities)
}

// FindActiveByAccountID retrieves all active non-expired liens for an account.
func (r *LienRepository) FindActiveByAccountID(ctx context.Context, accountID uuid.UUID) ([]*domain.Lien, error) {
	var entities []LienEntity

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		now := time.Now()
		return tx.Where(
			"account_id = ? AND status = ? AND (expires_at IS NULL OR expires_at > ?)",
			accountID, string(domain.LienStatusActive), now,
		).Find(&entities).Error
	})
	if err != nil {
		return nil, err
	}

	return toLienDomainSlice(entities)
}

// FindByPaymentOrderReference retrieves a lien by its payment order reference.
func (r *LienRepository) FindByPaymentOrderReference(ctx context.Context, reference string) (*domain.Lien, error) {
	var entity LienEntity
	var queryErr error

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Where("payment_order_reference = ?", reference).First(&entity)
		if result.Error != nil {
			queryErr = result.Error
			return result.Error
		}
		return nil
	})
	if err != nil {
		if errors.Is(queryErr, gorm.ErrRecordNotFound) {
			return nil, ErrLienNotFound
		}
		return nil, err
	}

	return toLienDomain(&entity)
}

// Update updates an existing lien with optimistic locking.
func (r *LienRepository) Update(ctx context.Context, lien *domain.Lien) error {
	entity, err := toLienEntity(lien)
	if err != nil {
		return fmt.Errorf("failed to convert lien to entity: %w", err)
	}
	var rowsAffected int64

	err = r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Model(&LienEntity{}).
			Where("id = ? AND version = ?", entity.ID, lien.Version).
			Updates(map[string]interface{}{
				"status":             entity.Status,
				"termination_reason": entity.TerminationReason,
				"updated_at":         entity.UpdatedAt,
				"version":            lien.Version + 1,
			})
		if result.Error != nil {
			return result.Error
		}
		rowsAffected = result.RowsAffected
		return nil
	})
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return ErrLienVersionConflict
	}

	lien.Version++
	return nil
}

// CountActiveByAccountID returns the count of active non-expired liens for an account.
func (r *LienRepository) CountActiveByAccountID(ctx context.Context, accountID uuid.UUID) (int64, error) {
	var count int64
	now := time.Now()

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Model(&LienEntity{}).
			Where("account_id = ? AND status = ? AND (expires_at IS NULL OR expires_at > ?)",
				accountID, string(domain.LienStatusActive), now).
			Count(&count).Error
	})
	return count, err
}

// SumActiveAmountByAccountID returns the total amount of active non-expired liens for an account.
func (r *LienRepository) SumActiveAmountByAccountID(ctx context.Context, accountID uuid.UUID) (int64, error) {
	var totalCents int64
	now := time.Now()

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Model(&LienEntity{}).
			Where("account_id = ? AND status = ? AND (expires_at IS NULL OR expires_at > ?)",
				accountID, string(domain.LienStatusActive), now).
			Select("COALESCE(SUM(amount_cents), 0)").
			Scan(&totalCents).Error
	})
	return totalCents, err
}

// SumActiveAmountByAccountIDAndBucket returns the total amount of active non-expired liens
// for a specific account and bucket.
func (r *LienRepository) SumActiveAmountByAccountIDAndBucket(ctx context.Context, accountID uuid.UUID, bucketID string) (int64, error) {
	var totalCents int64
	now := time.Now()

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Model(&LienEntity{}).
			Where("account_id = ? AND bucket_id = ? AND status = ? AND (expires_at IS NULL OR expires_at > ?)",
				accountID, bucketID, string(domain.LienStatusActive), now).
			Select("COALESCE(SUM(amount_cents), 0)").
			Scan(&totalCents).Error
	})
	return totalCents, err
}

// toLienEntity converts domain model to database entity.
func toLienEntity(lien *domain.Lien) (*LienEntity, error) {
	entity := &LienEntity{
		ID:                    lien.ID,
		AccountID:             lien.AccountID,
		AmountCents:           lien.AmountCents,
		Currency:              lien.Currency,
		BucketID:              lien.BucketID,
		Status:                string(lien.Status),
		PaymentOrderReference: lien.PaymentOrderReference,
		TerminationReason:     lien.TerminationReason,
		ExpiresAt:             lien.ExpiresAt,
		CreatedAt:             lien.CreatedAt,
		UpdatedAt:             lien.UpdatedAt,
		Version:               lien.Version,
	}

	if lien.ReservedQuantity != nil {
		data, err := json.Marshal(lien.ReservedQuantity)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal reserved_quantity: %w", err)
		}
		entity.ReservedQuantity = JSONBMap(data)
	}
	if lien.ValuedAmount != nil {
		data, err := json.Marshal(lien.ValuedAmount)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal valued_amount: %w", err)
		}
		entity.ValuedAmount = JSONBMap(data)
	}
	if lien.ValuationAnalysis != nil {
		entity.ValuationAnalysis = JSONBMap(lien.ValuationAnalysis)
	}

	return entity, nil
}

// toLienDomain converts database entity to domain model.
func toLienDomain(entity *LienEntity) (*domain.Lien, error) {
	lien := &domain.Lien{
		ID:                    entity.ID,
		AccountID:             entity.AccountID,
		AmountCents:           entity.AmountCents,
		Currency:              entity.Currency,
		BucketID:              entity.BucketID,
		Status:                domain.LienStatus(entity.Status),
		PaymentOrderReference: entity.PaymentOrderReference,
		TerminationReason:     entity.TerminationReason,
		ExpiresAt:             entity.ExpiresAt,
		Version:               entity.Version,
		CreatedAt:             entity.CreatedAt,
		UpdatedAt:             entity.UpdatedAt,
	}

	if entity.ReservedQuantity != nil {
		var rq domain.InstrumentAmount
		if err := json.Unmarshal(entity.ReservedQuantity, &rq); err != nil {
			return nil, fmt.Errorf("failed to unmarshal reserved_quantity: %w", err)
		}
		lien.ReservedQuantity = &rq
	}
	if entity.ValuedAmount != nil {
		var va domain.InstrumentAmount
		if err := json.Unmarshal(entity.ValuedAmount, &va); err != nil {
			return nil, fmt.Errorf("failed to unmarshal valued_amount: %w", err)
		}
		lien.ValuedAmount = &va
	}
	if entity.ValuationAnalysis != nil {
		lien.ValuationAnalysis = json.RawMessage(entity.ValuationAnalysis)
	}

	return lien, nil
}

// toLienDomainSlice converts a slice of entities to domain models.
func toLienDomainSlice(entities []LienEntity) ([]*domain.Lien, error) {
	liens := make([]*domain.Lien, 0, len(entities))
	for _, entity := range entities {
		lien, err := toLienDomain(&entity)
		if err != nil {
			return nil, err
		}
		liens = append(liens, lien)
	}
	return liens, nil
}
