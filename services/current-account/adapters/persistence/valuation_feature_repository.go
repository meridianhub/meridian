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

// ValuationFeature repository errors
var (
	ErrValuationFeatureNotFound        = errors.New("valuation feature not found")
	ErrValuationFeatureVersionConflict = errors.New("version conflict: valuation feature was modified by another transaction")
	ErrValuationFeatureAlreadyExists   = errors.New("valuation feature already exists for this account and instrument")
)

// ValuationFeatureRepository provides persistence operations for valuation features
type ValuationFeatureRepository struct {
	db *gorm.DB
}

// NewValuationFeatureRepository creates a new valuation feature repository
func NewValuationFeatureRepository(db *gorm.DB) *ValuationFeatureRepository {
	return &ValuationFeatureRepository{db: db}
}

// WithTx returns a new ValuationFeatureRepository that uses the provided transaction.
// This enables multiple repository operations within a single transaction.
func (r *ValuationFeatureRepository) WithTx(tx *gorm.DB) *ValuationFeatureRepository {
	return &ValuationFeatureRepository{db: tx}
}

// withTenantScope returns a GORM DB instance scoped to the tenant from context.
// The system is always multi-tenant - tenant context is ALWAYS required.
// This sets the PostgreSQL search_path to the tenant's schema (org_<tenant_id>).
//
// This must be called within a transaction for the search_path setting to work correctly.
func (r *ValuationFeatureRepository) withTenantScope(ctx context.Context, tx *gorm.DB) (*gorm.DB, error) {
	return db.WithGormTenantScope(ctx, tx)
}

// withTenantTransaction executes the given function with tenant scoping in a transaction.
// The system is always multi-tenant - tenant context is ALWAYS required.
// This wraps the function in a transaction and sets the search_path to the tenant's schema.
//
// If already in a transaction (via WithTx), it uses the existing transaction directly
// with tenant scope set. This avoids creating unnecessary savepoints in GORM v1.31+.
func (r *ValuationFeatureRepository) withTenantTransaction(ctx context.Context, fn func(tx *gorm.DB) error) error {
	if r.isInTransaction() {
		// Already in a transaction (via WithTx) - use it directly with tenant scope
		tx, err := r.withTenantScope(ctx, r.db.WithContext(ctx))
		if err != nil {
			return err
		}
		return fn(tx)
	}
	return db.WithGormTenantTransaction(ctx, r.db, fn)
}

// isInTransaction checks if the repository's db connection is already within a transaction.
// This is used to avoid creating nested transactions when the caller has already established one.
func (r *ValuationFeatureRepository) isInTransaction() bool {
	// Guard against uninitialized Statement (can happen if no query has been executed yet)
	if r.db.Statement == nil || r.db.Statement.ConnPool == nil {
		return false
	}
	// GORM sets ConnPool to a transaction object when in transaction mode.
	// In a transaction, Statement.ConnPool will be of type *sql.Tx (or GORM's tx wrapper).
	committer, ok := r.db.Statement.ConnPool.(gorm.TxCommitter)
	return ok && committer != nil
}

// Create inserts a new valuation feature.
// In multi-org mode, this operation is scoped to the organization from context.
func (r *ValuationFeatureRepository) Create(ctx context.Context, feature *domain.ValuationFeature) error {
	entity, err := toValuationFeatureEntity(feature)
	if err != nil {
		return fmt.Errorf("%w: %w", domain.ErrInvalidValuationFeatureParameters, err)
	}
	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Create(entity).Error
	})
}

// FindByID retrieves a valuation feature by its UUID.
// In multi-org mode, this query is scoped to the organization from context.
func (r *ValuationFeatureRepository) FindByID(ctx context.Context, id uuid.UUID) (*domain.ValuationFeature, error) {
	var entity ValuationFeatureEntity
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
			return nil, ErrValuationFeatureNotFound
		}
		return nil, err
	}

	return toValuationFeatureDomain(&entity)
}

// FindByAccountIDAndInstrument retrieves an active valuation feature for a specific account and instrument.
// This uses bi-temporal query: finds features valid at the given knowledge time.
// In multi-org mode, this query is scoped to the organization from context.
func (r *ValuationFeatureRepository) FindByAccountIDAndInstrument(
	ctx context.Context,
	accountID uuid.UUID,
	instrumentCode string,
	knowledgeAt time.Time,
) (*domain.ValuationFeature, error) {
	var entity ValuationFeatureEntity
	var queryErr error

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Where(
			"account_id = ? AND instrument_code = ? AND lifecycle_status = ? AND valid_from <= ? AND valid_to > ?",
			accountID, instrumentCode, string(domain.ValuationFeatureLifecycleStatusActive), knowledgeAt, knowledgeAt,
		).First(&entity)
		if result.Error != nil {
			queryErr = result.Error
			return result.Error
		}
		return nil
	})
	if err != nil {
		if errors.Is(queryErr, gorm.ErrRecordNotFound) {
			return nil, ErrValuationFeatureNotFound
		}
		return nil, err
	}

	return toValuationFeatureDomain(&entity)
}

// FindByAccountID retrieves all valuation features for an account.
// Optionally filters by lifecycle status if provided.
// In multi-org mode, this query is scoped to the organization from context.
func (r *ValuationFeatureRepository) FindByAccountID(
	ctx context.Context,
	accountID uuid.UUID,
	lifecycleStatus *domain.ValuationFeatureLifecycleStatus,
) ([]*domain.ValuationFeature, error) {
	var entities []ValuationFeatureEntity

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

	features := make([]*domain.ValuationFeature, 0, len(entities))
	for _, entity := range entities {
		feature, err := toValuationFeatureDomain(&entity)
		if err != nil {
			return nil, err
		}
		features = append(features, feature)
	}

	return features, nil
}

// FindByMethodID retrieves all active valuation features using a specific valuation method.
// This is useful when a valuation method is updated and we need to find all affected features.
// In multi-org mode, this query is scoped to the organization from context.
func (r *ValuationFeatureRepository) FindByMethodID(ctx context.Context, methodID uuid.UUID) ([]*domain.ValuationFeature, error) {
	var entities []ValuationFeatureEntity

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Where(
			"valuation_method_id = ? AND lifecycle_status = ?",
			methodID, string(domain.ValuationFeatureLifecycleStatusActive),
		).Find(&entities)
		return result.Error
	})
	if err != nil {
		return nil, err
	}

	features := make([]*domain.ValuationFeature, 0, len(entities))
	for _, entity := range entities {
		feature, err := toValuationFeatureDomain(&entity)
		if err != nil {
			return nil, err
		}
		features = append(features, feature)
	}

	return features, nil
}

// Update updates an existing valuation feature with optimistic locking.
// In multi-org mode, this operation is scoped to the organization from context.
func (r *ValuationFeatureRepository) Update(ctx context.Context, feature *domain.ValuationFeature) error {
	entity, err := toValuationFeatureEntity(feature)
	if err != nil {
		return fmt.Errorf("%w: %w", domain.ErrInvalidValuationFeatureParameters, err)
	}
	var rowsAffected int64

	err = r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		// Optimistic locking: use WHERE clause with version check
		result := tx.Model(&ValuationFeatureEntity{}).
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
		return ErrValuationFeatureVersionConflict
	}

	// Update domain model version
	feature.Version++

	return nil
}

// FindByIDForUpdate retrieves a valuation feature by its UUID with a pessimistic lock.
// Use this within a transaction when you need to prevent concurrent modifications.
// In multi-org mode, this query is scoped to the organization from context.
func (r *ValuationFeatureRepository) FindByIDForUpdate(ctx context.Context, id uuid.UUID) (*domain.ValuationFeature, error) {
	var entity ValuationFeatureEntity
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
			return nil, ErrValuationFeatureNotFound
		}
		return nil, err
	}

	return toValuationFeatureDomain(&entity)
}

// toValuationFeatureEntity converts domain model to database entity.
// Returns an error if parameter marshaling fails (e.g., unsupported types, cyclic structures).
func toValuationFeatureEntity(feature *domain.ValuationFeature) (*ValuationFeatureEntity, error) {
	var parametersJSON []byte
	var err error
	if feature.Parameters != nil {
		parametersJSON, err = json.Marshal(feature.Parameters)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal parameters: %w", err)
		}
	}

	return &ValuationFeatureEntity{
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

// toValuationFeatureDomain converts database entity to domain model
func toValuationFeatureDomain(entity *ValuationFeatureEntity) (*domain.ValuationFeature, error) {
	var parameters map[string]interface{}
	if entity.Parameters != nil {
		if err := json.Unmarshal(entity.Parameters, &parameters); err != nil {
			return nil, fmt.Errorf("failed to unmarshal valuation feature parameters: %w", err)
		}
	}

	return &domain.ValuationFeature{
		ID:                     entity.ID,
		AccountID:              entity.AccountID,
		InstrumentCode:         entity.InstrumentCode,
		ValuationMethodID:      entity.ValuationMethodID,
		ValuationMethodVersion: entity.ValuationMethodVersion,
		Parameters:             parameters,
		LifecycleStatus:        domain.ValuationFeatureLifecycleStatus(entity.LifecycleStatus),
		ValidFrom:              entity.ValidFrom,
		ValidTo:                entity.ValidTo,
		CreatedAt:              entity.CreatedAt,
		CreatedBy:              entity.CreatedBy,
		UpdatedAt:              entity.UpdatedAt,
		UpdatedBy:              entity.UpdatedBy,
		Version:                entity.Version,
	}, nil
}
