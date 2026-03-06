package persistence

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/meridianhub/meridian/shared/platform/db"
	"gorm.io/gorm"
)

// Withdrawal repository errors
var (
	ErrWithdrawalNotFound        = errors.New("withdrawal not found")
	ErrWithdrawalVersionConflict = errors.New("version conflict: withdrawal was modified by another transaction")
)

// PaginationParams defines pagination parameters for list queries
type PaginationParams struct {
	Offset int
	Limit  int
}

// WithdrawalRepository provides persistence operations for withdrawals
type WithdrawalRepository struct {
	db *gorm.DB
}

// NewWithdrawalRepository creates a new withdrawal repository
func NewWithdrawalRepository(db *gorm.DB) *WithdrawalRepository {
	return &WithdrawalRepository{db: db}
}

// WithTx returns a new WithdrawalRepository that uses the provided transaction.
// This enables multiple repository operations within a single transaction.
func (r *WithdrawalRepository) WithTx(tx *gorm.DB) *WithdrawalRepository {
	return &WithdrawalRepository{db: tx}
}

// withTenantTransaction executes the given function with tenant scoping in a transaction.
// The system is always multi-tenant - tenant context is ALWAYS required.
// This wraps the function in a transaction and sets the search_path to the tenant's schema.
func (r *WithdrawalRepository) withTenantTransaction(ctx context.Context, fn func(tx *gorm.DB) error) error {
	return db.WithGormTenantTransaction(ctx, r.db, fn)
}

// Create inserts a new withdrawal
func (r *WithdrawalRepository) Create(ctx context.Context, withdrawal *domain.Withdrawal) error {
	entity := toWithdrawalEntity(withdrawal)

	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Create(entity).Error
	})
}

// FindByID retrieves a withdrawal by its UUID.
// In multi-org mode, this query is scoped to the organization from context.
func (r *WithdrawalRepository) FindByID(ctx context.Context, id uuid.UUID) (*domain.Withdrawal, error) {
	var entity WithdrawalEntity
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
			return nil, ErrWithdrawalNotFound
		}
		return nil, err
	}

	return toWithdrawalDomain(&entity)
}

// FindByReference retrieves a withdrawal by its unique reference.
// In multi-org mode, this query is scoped to the organization from context.
func (r *WithdrawalRepository) FindByReference(ctx context.Context, reference string) (*domain.Withdrawal, error) {
	var entity WithdrawalEntity
	var queryErr error

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Where("reference = ?", reference).First(&entity)
		if result.Error != nil {
			queryErr = result.Error
			return result.Error
		}
		return nil
	})
	if err != nil {
		if errors.Is(queryErr, gorm.ErrRecordNotFound) {
			return nil, ErrWithdrawalNotFound
		}
		return nil, err
	}

	return toWithdrawalDomain(&entity)
}

// Update updates an existing withdrawal with optimistic locking
func (r *WithdrawalRepository) Update(ctx context.Context, withdrawal *domain.Withdrawal) error {
	entity := toWithdrawalEntity(withdrawal)

	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		// Optimistic locking: use WHERE clause with version check
		result := tx.Model(&WithdrawalEntity{}).
			Where("id = ? AND version = ?", entity.ID, withdrawal.Version).
			Updates(map[string]interface{}{
				"status":     entity.Status,
				"updated_at": entity.UpdatedAt,
				"version":    withdrawal.Version + 1,
			})

		if result.Error != nil {
			return result.Error
		}

		if result.RowsAffected == 0 {
			return ErrWithdrawalVersionConflict
		}

		// Update domain model version
		withdrawal.Version++

		return nil
	})
}

// List retrieves withdrawals for an account with pagination.
// In multi-org mode, this query is scoped to the organization from context.
func (r *WithdrawalRepository) List(ctx context.Context, accountID uuid.UUID, pagination PaginationParams) ([]*domain.Withdrawal, error) {
	var entities []WithdrawalEntity

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		query := tx.Where("account_id = ?", accountID).
			Order("created_at DESC")

		if pagination.Limit > 0 {
			query = query.Limit(pagination.Limit)
		}
		if pagination.Offset > 0 {
			query = query.Offset(pagination.Offset)
		}

		return query.Find(&entities).Error
	})
	if err != nil {
		return nil, err
	}

	withdrawals := make([]*domain.Withdrawal, 0, len(entities))
	for _, entity := range entities {
		withdrawal, err := toWithdrawalDomain(&entity)
		if err != nil {
			return nil, err
		}
		withdrawals = append(withdrawals, withdrawal)
	}

	return withdrawals, nil
}

// toWithdrawalEntity converts domain model to database entity
func toWithdrawalEntity(withdrawal *domain.Withdrawal) *WithdrawalEntity {
	// ToMinorUnitsUnchecked is safe here: domain layer validates amounts before persistence,
	// so overflow (>92 quadrillion cents) cannot occur for valid withdrawals
	return &WithdrawalEntity{
		ID:             withdrawal.ID,
		AccountID:      withdrawal.AccountID,
		AmountCents:    withdrawal.Amount.ToMinorUnitsUnchecked(),
		InstrumentCode: withdrawal.Amount.InstrumentCode(),
		Dimension:      withdrawal.Amount.Dimension(),
		Precision:      withdrawal.Amount.Precision(),
		Status:         string(withdrawal.Status),
		Reference:      withdrawal.Reference,
		CreatedAt:      withdrawal.CreatedAt,
		UpdatedAt:      withdrawal.UpdatedAt,
		Version:        int64(withdrawal.Version),
	}
}

// toWithdrawalDomain converts database entity to domain model
func toWithdrawalDomain(entity *WithdrawalEntity) (*domain.Withdrawal, error) {
	amount, err := domain.NewAmountFromInstrument(entity.InstrumentCode, entity.Dimension, entity.Precision, entity.AmountCents)
	if err != nil {
		return nil, fmt.Errorf("failed to create withdrawal amount from database: %w", err)
	}

	return &domain.Withdrawal{
		ID:        entity.ID,
		AccountID: entity.AccountID,
		Amount:    amount,
		Status:    domain.WithdrawalStatus(entity.Status),
		Reference: entity.Reference,
		Version:   int(entity.Version),
		CreatedAt: entity.CreatedAt,
		UpdatedAt: entity.UpdatedAt,
	}, nil
}
