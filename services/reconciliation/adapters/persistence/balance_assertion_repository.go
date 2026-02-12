package persistence

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/meridianhub/meridian/shared/platform/db"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// Compile-time check that BalanceAssertionRepository implements domain.BalanceAssertionRepository.
var _ domain.BalanceAssertionRepository = (*BalanceAssertionRepository)(nil)

// BalanceAssertionEntity is the GORM entity for the balance_assertion table.
type BalanceAssertionEntity struct {
	ID              uuid.UUID       `gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	CreatedAt       time.Time       `gorm:"not null;default:now()"`
	UpdatedAt       time.Time       `gorm:"not null;default:now()"`
	AssertionID     uuid.UUID       `gorm:"column:assertion_id;uniqueIndex:idx_ba_assertion_id;type:uuid;not null"`
	RunID           *uuid.UUID      `gorm:"column:run_id;index:idx_ba_run_id;type:uuid"`
	AccountID       string          `gorm:"column:account_id;index:idx_ba_account_id;size:34;not null"`
	InstrumentCode  string          `gorm:"column:instrument_code;index:idx_ba_instrument_code;size:20;not null"`
	Expression      string          `gorm:"column:expression;type:text;not null"`
	ExpectedBalance decimal.Decimal `gorm:"column:expected_balance;type:decimal(38,18);not null"`
	ActualBalance   decimal.Decimal `gorm:"column:actual_balance;type:decimal(38,18);not null;default:0"`
	Status          string          `gorm:"column:status;index:idx_ba_status;size:20;not null;default:PENDING"`
	FailureReason   *string         `gorm:"column:failure_reason;type:text"`
	OverrideReason  *string         `gorm:"column:override_reason;type:text"`
	Attributes      JSONMap         `gorm:"column:attributes;type:jsonb"`
	Metadata        JSONMap         `gorm:"column:metadata;type:jsonb"`
	AssertedAt      *time.Time      `gorm:"column:asserted_at"`
	Version         int64           `gorm:"column:version;not null;default:1"`
}

// TableName returns the table name for the balance assertion entity.
func (BalanceAssertionEntity) TableName() string {
	return "balance_assertion"
}

// BalanceAssertionRepository provides GORM-based persistence for balance assertions.
type BalanceAssertionRepository struct {
	db *gorm.DB
}

// NewBalanceAssertionRepository creates a new balance assertion repository.
func NewBalanceAssertionRepository(db *gorm.DB) *BalanceAssertionRepository {
	return &BalanceAssertionRepository{db: db}
}

// withTenantTransaction executes fn within a tenant-scoped transaction.
func (r *BalanceAssertionRepository) withTenantTransaction(ctx context.Context, fn func(tx *gorm.DB) error) error {
	if r.isInTransaction() {
		tx, err := db.WithGormTenantScope(ctx, r.db.WithContext(ctx))
		if err != nil {
			return err
		}
		return fn(tx)
	}
	return db.WithGormTenantTransaction(ctx, r.db, fn)
}

// isInTransaction checks if the repository's db connection is already within a transaction.
func (r *BalanceAssertionRepository) isInTransaction() bool {
	if r.db.Statement == nil || r.db.Statement.ConnPool == nil {
		return false
	}
	committer, ok := r.db.Statement.ConnPool.(gorm.TxCommitter)
	return ok && committer != nil
}

// Create persists a new BalanceAssertion.
func (r *BalanceAssertionRepository) Create(ctx context.Context, assertion *domain.BalanceAssertion) error {
	entity := toBalanceAssertionEntity(assertion)
	if entity.Version == 0 {
		entity.Version = 1
	}
	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Create(entity).Error
	})
}

// FindByID retrieves a BalanceAssertion by its AssertionID.
func (r *BalanceAssertionRepository) FindByID(ctx context.Context, assertionID uuid.UUID) (*domain.BalanceAssertion, error) {
	var entity BalanceAssertionEntity
	var queryErr error

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Where("assertion_id = ?", assertionID).First(&entity)
		if result.Error != nil {
			queryErr = result.Error
			return result.Error
		}
		return nil
	})
	if err != nil {
		if errors.Is(queryErr, gorm.ErrRecordNotFound) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}

	return toDomainBalanceAssertion(&entity), nil
}

// FindByRunID retrieves all assertions for a settlement run.
func (r *BalanceAssertionRepository) FindByRunID(ctx context.Context, runID uuid.UUID) ([]*domain.BalanceAssertion, error) {
	var entities []BalanceAssertionEntity

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Where("run_id = ?", runID).
			Order("created_at ASC").
			Find(&entities).Error
	})
	if err != nil {
		return nil, err
	}

	return toBalanceAssertionDomainSlice(entities), nil
}

// Update updates an existing BalanceAssertion using optimistic locking.
func (r *BalanceAssertionRepository) Update(ctx context.Context, assertion *domain.BalanceAssertion) error {
	entity := toBalanceAssertionEntity(assertion)
	var rowsAffected int64

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Model(&BalanceAssertionEntity{}).
			Where("assertion_id = ? AND version = ?", entity.AssertionID, entity.Version-1).
			Updates(map[string]interface{}{
				"status":          entity.Status,
				"actual_balance":  entity.ActualBalance,
				"failure_reason":  entity.FailureReason,
				"override_reason": entity.OverrideReason,
				"attributes":      entity.Attributes,
				"metadata":        entity.Metadata,
				"asserted_at":     entity.AssertedAt,
				"version":         entity.Version,
				"updated_at":      time.Now().UTC(),
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
		var count int64
		countErr := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
			return tx.Model(&BalanceAssertionEntity{}).Where("assertion_id = ?", entity.AssertionID).Count(&count).Error
		})
		if countErr != nil {
			return countErr
		}
		if count == 0 {
			return domain.ErrNotFound
		}
		return domain.ErrOptimisticLock
	}
	return nil
}

// List retrieves assertions matching the given filter with pagination.
func (r *BalanceAssertionRepository) List(ctx context.Context, filter domain.AssertionFilter) ([]*domain.BalanceAssertion, error) {
	var entities []BalanceAssertionEntity

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		query := tx.Model(&BalanceAssertionEntity{})

		if filter.RunID != nil {
			query = query.Where("run_id = ?", *filter.RunID)
		}
		if filter.AccountID != nil {
			query = query.Where("account_id = ?", *filter.AccountID)
		}
		if filter.InstrumentCode != nil {
			query = query.Where("instrument_code = ?", *filter.InstrumentCode)
		}
		if filter.Status != nil {
			query = query.Where("status = ?", string(*filter.Status))
		}

		limit := filter.Limit
		if limit <= 0 {
			limit = 50
		}
		if limit > 1000 {
			limit = 1000
		}
		query = query.Limit(limit)

		if filter.Offset > 0 {
			query = query.Offset(filter.Offset)
		}

		return query.Order("created_at DESC").Find(&entities).Error
	})
	if err != nil {
		return nil, err
	}

	return toBalanceAssertionDomainSlice(entities), nil
}

// toBalanceAssertionEntity converts a domain BalanceAssertion to a persistence entity.
func toBalanceAssertionEntity(a *domain.BalanceAssertion) *BalanceAssertionEntity {
	entity := &BalanceAssertionEntity{
		AssertionID:     a.AssertionID,
		RunID:           a.RunID,
		AccountID:       a.AccountID,
		InstrumentCode:  a.InstrumentCode,
		Expression:      a.Expression,
		ExpectedBalance: a.ExpectedBalance,
		ActualBalance:   a.ActualBalance,
		Status:          string(a.Status),
		CreatedAt:       a.CreatedAt,
		UpdatedAt:       a.UpdatedAt,
		Version:         a.Version,
	}

	if a.FailureReason != "" {
		entity.FailureReason = &a.FailureReason
	}
	if a.OverrideReason != "" {
		entity.OverrideReason = &a.OverrideReason
	}
	if a.Attributes != nil {
		entity.Attributes = JSONMap(a.Attributes)
	}
	if a.Metadata != nil {
		entity.Metadata = JSONMap(a.Metadata)
	}
	if !a.AssertedAt.IsZero() {
		assertedAt := a.AssertedAt
		entity.AssertedAt = &assertedAt
	}

	return entity
}

// toDomainBalanceAssertion converts a persistence entity to a domain BalanceAssertion.
func toDomainBalanceAssertion(e *BalanceAssertionEntity) *domain.BalanceAssertion {
	a := &domain.BalanceAssertion{
		AssertionID:     e.AssertionID,
		RunID:           e.RunID,
		AccountID:       e.AccountID,
		InstrumentCode:  e.InstrumentCode,
		Expression:      e.Expression,
		ExpectedBalance: e.ExpectedBalance,
		ActualBalance:   e.ActualBalance,
		Status:          domain.AssertionStatus(e.Status),
		CreatedAt:       e.CreatedAt,
		UpdatedAt:       e.UpdatedAt,
		Version:         e.Version,
	}

	if e.FailureReason != nil {
		a.FailureReason = *e.FailureReason
	}
	if e.OverrideReason != nil {
		a.OverrideReason = *e.OverrideReason
	}
	if e.Attributes != nil {
		a.Attributes = map[string]string(e.Attributes)
	}
	if e.Metadata != nil {
		a.Metadata = map[string]string(e.Metadata)
	}
	if e.AssertedAt != nil {
		a.AssertedAt = *e.AssertedAt
	}

	return a
}

// toBalanceAssertionDomainSlice converts a slice of entities to domain objects.
func toBalanceAssertionDomainSlice(entities []BalanceAssertionEntity) []*domain.BalanceAssertion {
	assertions := make([]*domain.BalanceAssertion, 0, len(entities))
	for i := range entities {
		assertions = append(assertions, toDomainBalanceAssertion(&entities[i]))
	}
	return assertions
}
