package valuationfeature

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Repository provides persistence operations for valuation features
type Repository struct {
	db *gorm.DB
}

// NewRepository creates a new valuation feature repository
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// DB returns the underlying database connection for transaction support.
func (r *Repository) DB() *gorm.DB {
	return r.db
}

// WithTx returns a new Repository that uses the provided transaction.
// This enables multiple repository operations within a single transaction.
func (r *Repository) WithTx(tx *gorm.DB) *Repository {
	return &Repository{db: tx}
}

// withTenantScope returns a GORM DB instance scoped to the tenant from context.
func (r *Repository) withTenantScope(ctx context.Context, tx *gorm.DB) (*gorm.DB, error) {
	return db.WithGormTenantScope(ctx, tx)
}

// withTenantTransaction executes the given function with tenant scoping in a transaction.
func (r *Repository) withTenantTransaction(ctx context.Context, fn func(tx *gorm.DB) error) error {
	if r.isInTransaction() {
		tx, err := r.withTenantScope(ctx, r.db.WithContext(ctx))
		if err != nil {
			return err
		}
		return fn(tx)
	}
	return db.WithGormTenantTransaction(ctx, r.db, fn)
}

// isInTransaction checks if the repository's db connection is already within a transaction.
func (r *Repository) isInTransaction() bool {
	if r.db.Statement == nil || r.db.Statement.ConnPool == nil {
		return false
	}
	committer, ok := r.db.Statement.ConnPool.(gorm.TxCommitter)
	return ok && committer != nil
}

// Create inserts a new valuation feature.
func (r *Repository) Create(ctx context.Context, feature *ValuationFeature) error {
	entity, err := toEntity(feature)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidParameters, err)
	}
	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Create(entity).Error
	})
}

// FindByID retrieves a valuation feature by its UUID.
func (r *Repository) FindByID(ctx context.Context, id uuid.UUID) (*ValuationFeature, error) {
	var entity Entity
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
			return nil, ErrNotFound
		}
		return nil, err
	}

	return toDomain(&entity)
}

// FindByAccountIDAndInstrument retrieves an active valuation feature for a specific account and instrument.
// This uses bi-temporal query: finds features valid at the given knowledge time.
func (r *Repository) FindByAccountIDAndInstrument(
	ctx context.Context,
	accountID uuid.UUID,
	instrumentCode string,
	knowledgeAt time.Time,
) (*ValuationFeature, error) {
	var entity Entity
	var queryErr error

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Where(
			"account_id = ? AND instrument_code = ? AND lifecycle_status = ? AND valid_from <= ? AND valid_to > ?",
			accountID, instrumentCode, string(LifecycleStatusActive), knowledgeAt, knowledgeAt,
		).First(&entity)
		if result.Error != nil {
			queryErr = result.Error
			return result.Error
		}
		return nil
	})
	if err != nil {
		if errors.Is(queryErr, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	return toDomain(&entity)
}

// FindByAccountID retrieves all valuation features for an account.
// Optionally filters by lifecycle status if provided.
func (r *Repository) FindByAccountID(
	ctx context.Context,
	accountID uuid.UUID,
	lifecycleStatus *LifecycleStatus,
) ([]*ValuationFeature, error) {
	var entities []Entity

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		query := tx.Where("account_id = ?", accountID)
		if lifecycleStatus != nil {
			query = query.Where("lifecycle_status = ?", string(*lifecycleStatus))
		}
		result := query.Order("created_at ASC").Find(&entities)
		return result.Error
	})
	if err != nil {
		return nil, err
	}

	features := make([]*ValuationFeature, 0, len(entities))
	for _, entity := range entities {
		feature, err := toDomain(&entity)
		if err != nil {
			return nil, err
		}
		features = append(features, feature)
	}

	return features, nil
}

// FindByMethodID retrieves all active valuation features using a specific valuation method.
func (r *Repository) FindByMethodID(ctx context.Context, methodID uuid.UUID) ([]*ValuationFeature, error) {
	var entities []Entity

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Where(
			"valuation_method_id = ? AND lifecycle_status = ?",
			methodID, string(LifecycleStatusActive),
		).Find(&entities)
		return result.Error
	})
	if err != nil {
		return nil, err
	}

	features := make([]*ValuationFeature, 0, len(entities))
	for _, entity := range entities {
		feature, err := toDomain(&entity)
		if err != nil {
			return nil, err
		}
		features = append(features, feature)
	}

	return features, nil
}

// Update updates an existing valuation feature with optimistic locking.
func (r *Repository) Update(ctx context.Context, feature *ValuationFeature) error {
	entity, err := toEntity(feature)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidParameters, err)
	}
	var rowsAffected int64

	err = r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Model(&Entity{}).
			Where("id = ? AND version = ?", entity.ID, feature.Version).
			Updates(map[string]interface{}{
				"lifecycle_status": entity.LifecycleStatus,
				"valid_to":         entity.ValidTo,
				"updated_at":       entity.UpdatedAt,
				"updated_by":       entity.UpdatedBy,
				"version":          feature.Version + 1,
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
		return ErrVersionConflict
	}

	// Update domain model version
	feature.Version++

	return nil
}

// FindByIDForUpdate retrieves a valuation feature by its UUID with a pessimistic lock.
func (r *Repository) FindByIDForUpdate(ctx context.Context, id uuid.UUID) (*ValuationFeature, error) {
	var entity Entity
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
			return nil, ErrNotFound
		}
		return nil, err
	}

	return toDomain(&entity)
}

// toEntity converts domain model to database entity.
func toEntity(feature *ValuationFeature) (*Entity, error) {
	var parametersJSON []byte
	var err error
	if feature.Parameters != nil {
		parametersJSON, err = json.Marshal(feature.Parameters)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal parameters: %w", err)
		}
	}

	return &Entity{
		ID:                     feature.ID,
		AccountID:              feature.AccountID,
		InstrumentCode:         feature.InstrumentCode,
		ValuationMethodID:      feature.ValuationMethodID,
		ValuationMethodVersion: feature.ValuationMethodVersion,
		Parameters:             parametersJSON,
		LifecycleStatus:        string(feature.LifecycleStatus),
		ValidFrom:              feature.ValidFrom,
		ValidTo:                feature.ValidTo,
		CreatedAt:              feature.CreatedAt,
		CreatedBy:              feature.CreatedBy,
		UpdatedAt:              feature.UpdatedAt,
		UpdatedBy:              feature.UpdatedBy,
		Version:                feature.Version,
	}, nil
}

// toDomain converts database entity to domain model
func toDomain(entity *Entity) (*ValuationFeature, error) {
	var parameters map[string]interface{}
	if entity.Parameters != nil {
		if err := json.Unmarshal(entity.Parameters, &parameters); err != nil {
			return nil, fmt.Errorf("failed to unmarshal valuation feature parameters: %w", err)
		}
	}

	return &ValuationFeature{
		ID:                     entity.ID,
		AccountID:              entity.AccountID,
		InstrumentCode:         entity.InstrumentCode,
		ValuationMethodID:      entity.ValuationMethodID,
		ValuationMethodVersion: entity.ValuationMethodVersion,
		Parameters:             parameters,
		LifecycleStatus:        LifecycleStatus(entity.LifecycleStatus),
		ValidFrom:              entity.ValidFrom,
		ValidTo:                entity.ValidTo,
		CreatedAt:              entity.CreatedAt,
		CreatedBy:              entity.CreatedBy,
		UpdatedAt:              entity.UpdatedAt,
		UpdatedBy:              entity.UpdatedBy,
		Version:                entity.Version,
	}, nil
}
