package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/meridianhub/meridian/shared/platform/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Lien repository errors
var (
	ErrLienNotFound             = errors.New("lien not found")
	ErrLienVersionConflict      = errors.New("version conflict: lien was modified by another transaction")
	ErrLienCurrencyInconsistent = errors.New("active liens have inconsistent currencies")
)

// LienRepository provides persistence operations for liens
type LienRepository struct {
	db *gorm.DB
}

// NewLienRepository creates a new lien repository
func NewLienRepository(db *gorm.DB) *LienRepository {
	return &LienRepository{db: db}
}

// WithTx returns a new LienRepository that uses the provided transaction.
// This enables multiple repository operations within a single transaction.
func (r *LienRepository) WithTx(tx *gorm.DB) *LienRepository {
	return &LienRepository{db: tx}
}

// withTenantTransaction executes the given function with tenant scoping in a transaction.
// The system is always multi-tenant - tenant context is ALWAYS required.
// This wraps the function in a transaction and sets the search_path to the tenant's schema.
func (r *LienRepository) withTenantTransaction(ctx context.Context, fn func(tx *gorm.DB) error) error {
	return db.WithGormTenantTransaction(ctx, r.db, fn)
}

// Create inserts a new lien.
// In multi-org mode, this operation is scoped to the organization from context.
func (r *LienRepository) Create(ctx context.Context, lien *domain.Lien) error {
	entity := toLienEntity(lien)
	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Create(entity).Error
	})
}

// FindByID retrieves a lien by its UUID.
// In multi-org mode, this query is scoped to the organization from context.
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
// Use this within a transaction when you need to prevent concurrent modifications.
// In multi-org mode, this query is scoped to the organization from context.
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
// In multi-org mode, this query is scoped to the organization from context.
func (r *LienRepository) FindByAccountID(ctx context.Context, accountID uuid.UUID) ([]*domain.Lien, error) {
	var entities []LienEntity

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Where("account_id = ?", accountID).Find(&entities)
		return result.Error
	})
	if err != nil {
		return nil, err
	}

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

// FindActiveByAccountID retrieves all active non-expired liens for an account.
// Liens with status ACTIVE but past their expires_at are excluded.
// In multi-org mode, this query is scoped to the organization from context.
func (r *LienRepository) FindActiveByAccountID(ctx context.Context, accountID uuid.UUID) ([]*domain.Lien, error) {
	var entities []LienEntity

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		now := time.Now()
		result := tx.Where(
			"account_id = ? AND status = ? AND (expires_at IS NULL OR expires_at > ?)",
			accountID, string(domain.LienStatusActive), now,
		).Find(&entities)
		return result.Error
	})
	if err != nil {
		return nil, err
	}

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

// FindByPaymentOrderReference retrieves a lien by its payment order reference.
// In multi-org mode, this query is scoped to the organization from context.
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
// In multi-org mode, this operation is scoped to the organization from context.
func (r *LienRepository) Update(ctx context.Context, lien *domain.Lien) error {
	entity := toLienEntity(lien)
	var rowsAffected int64

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		// Optimistic locking: use WHERE clause with version check
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

	// Update domain model version
	lien.Version++

	return nil
}

// CountActiveByAccountID returns the count of active non-expired liens for an account.
// Used to check if an account has any active liens before closing.
// In multi-org mode, this query is scoped to the organization from context.
func (r *LienRepository) CountActiveByAccountID(ctx context.Context, accountID uuid.UUID) (int64, error) {
	var count int64
	now := time.Now()

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Model(&LienEntity{}).
			Where("account_id = ? AND status = ? AND (expires_at IS NULL OR expires_at > ?)",
				accountID, string(domain.LienStatusActive), now).
			Count(&count)
		return result.Error
	})
	if err != nil {
		return 0, err
	}

	return count, nil
}

// SumActiveAmountByAccountID returns the total amount of active non-expired liens for an account in cents.
// Returns ErrLienCurrencyInconsistent if liens with different currencies exist (indicates data corruption).
// Currency validation is enforced at the service layer when creating liens (InitiateLien).
// In multi-org mode, this query is scoped to the organization from context.
func (r *LienRepository) SumActiveAmountByAccountID(ctx context.Context, accountID uuid.UUID) (int64, error) {
	// Capture timestamp once to ensure consistency between the two queries
	now := time.Now()
	var currencyCount int64
	var totalCents int64

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		// First, check for currency consistency (defensive check for data corruption)
		countResult := tx.Model(&LienEntity{}).
			Where("account_id = ? AND status = ? AND (expires_at IS NULL OR expires_at > ?)",
				accountID, string(domain.LienStatusActive), now).
			Select("COUNT(DISTINCT currency)").
			Scan(&currencyCount)

		if countResult.Error != nil {
			return countResult.Error
		}

		if currencyCount > 1 {
			return ErrLienCurrencyInconsistent
		}

		// Sum active non-expired liens
		result := tx.Model(&LienEntity{}).
			Where("account_id = ? AND status = ? AND (expires_at IS NULL OR expires_at > ?)",
				accountID, string(domain.LienStatusActive), now).
			Select("COALESCE(SUM(amount_cents), 0)").
			Scan(&totalCents)

		return result.Error
	})
	if err != nil {
		return 0, err
	}

	return totalCents, nil
}

// SumActiveAmountByAccountIDAndBucket returns the total amount of active non-expired liens
// for a specific account and bucket in cents.
// Returns ErrLienCurrencyInconsistent if liens with different currencies exist (indicates data corruption).
// In multi-org mode, this query is scoped to the organization from context.
func (r *LienRepository) SumActiveAmountByAccountIDAndBucket(ctx context.Context, accountID uuid.UUID, bucketID string) (int64, error) {
	// Capture timestamp once to ensure consistency between the two queries
	now := time.Now()
	var currencyCount int64
	var totalCents int64

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		// First, check for currency consistency (defensive check for data corruption)
		countResult := tx.Model(&LienEntity{}).
			Where("account_id = ? AND bucket_id = ? AND status = ? AND (expires_at IS NULL OR expires_at > ?)",
				accountID, bucketID, string(domain.LienStatusActive), now).
			Select("COUNT(DISTINCT currency)").
			Scan(&currencyCount)

		if countResult.Error != nil {
			return countResult.Error
		}

		if currencyCount > 1 {
			return ErrLienCurrencyInconsistent
		}

		// Sum active non-expired liens for the specific bucket
		result := tx.Model(&LienEntity{}).
			Where("account_id = ? AND bucket_id = ? AND status = ? AND (expires_at IS NULL OR expires_at > ?)",
				accountID, bucketID, string(domain.LienStatusActive), now).
			Select("COALESCE(SUM(amount_cents), 0)").
			Scan(&totalCents)

		return result.Error
	})
	if err != nil {
		return 0, err
	}

	return totalCents, nil
}

// toLienEntity converts domain model to database entity
func toLienEntity(lien *domain.Lien) *LienEntity {
	// ToMinorUnitsUnchecked is safe here: domain layer validates amounts before persistence,
	// so overflow (>92 quadrillion cents) cannot occur for valid liens
	entity := &LienEntity{
		ID:                    lien.ID,
		AccountID:             lien.AccountID,
		AmountCents:           lien.Amount.ToMinorUnitsUnchecked(),
		Currency:              lien.Amount.InstrumentCode(),
		BucketID:              lien.BucketID,
		Status:                string(lien.Status),
		PaymentOrderReference: lien.PaymentOrderReference,
		TerminationReason:     lien.TerminationReason,
		ExpiresAt:             lien.ExpiresAt,
		CreatedAt:             lien.CreatedAt,
		UpdatedAt:             lien.UpdatedAt,
		Version:               lien.Version,
	}

	// Marshal valuation fields to JSONB (nil-safe)
	if lien.ReservedQuantity != nil {
		if data, err := json.Marshal(lien.ReservedQuantity); err == nil {
			entity.ReservedQuantity = JSONBMap(data)
		}
	}
	if lien.ValuedAmount != nil {
		if data, err := json.Marshal(lien.ValuedAmount); err == nil {
			entity.ValuedAmount = JSONBMap(data)
		}
	}
	if lien.ValuationAnalysis != nil {
		entity.ValuationAnalysis = JSONBMap(lien.ValuationAnalysis)
	}

	return entity
}

// toLienDomain converts database entity to domain model
func toLienDomain(entity *LienEntity) (*domain.Lien, error) {
	amount, err := domain.NewMoney(entity.Currency, entity.AmountCents)
	if err != nil {
		return nil, fmt.Errorf("failed to create lien amount from database: %w", err)
	}

	lien := &domain.Lien{
		ID:                    entity.ID,
		AccountID:             entity.AccountID,
		Amount:                amount,
		BucketID:              entity.BucketID,
		Status:                domain.LienStatus(entity.Status),
		PaymentOrderReference: entity.PaymentOrderReference,
		TerminationReason:     entity.TerminationReason,
		ExpiresAt:             entity.ExpiresAt,
		Version:               entity.Version,
		CreatedAt:             entity.CreatedAt,
		UpdatedAt:             entity.UpdatedAt,
	}

	// Unmarshal valuation fields from JSONB (nil-safe, fail-fast on corruption)
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
