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

// Compile-time check that VarianceRepository implements domain.VarianceRepository.
var _ domain.VarianceRepository = (*VarianceRepository)(nil)

// VarianceEntity is the GORM entity for the variance table.
type VarianceEntity struct {
	ID             uuid.UUID       `gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	CreatedAt      time.Time       `gorm:"not null;default:now()"`
	UpdatedAt      time.Time       `gorm:"not null;default:now()"`
	VarianceID     uuid.UUID       `gorm:"column:variance_id;uniqueIndex;type:uuid;not null"`
	RunID          uuid.UUID       `gorm:"column:run_id;index;type:uuid;not null"`
	SnapshotID     uuid.UUID       `gorm:"column:snapshot_id;index;type:uuid;not null"`
	AccountID      string          `gorm:"column:account_id;index;size:34;not null"`
	InstrumentCode string          `gorm:"column:instrument_code;size:20;not null"`
	ExpectedAmount decimal.Decimal `gorm:"column:expected_amount;type:decimal(38,18);not null"`
	ActualAmount   decimal.Decimal `gorm:"column:actual_amount;type:decimal(38,18);not null"`
	VarianceAmount decimal.Decimal `gorm:"column:variance_amount;type:decimal(38,18);not null"`
	ValueDelta     decimal.Decimal `gorm:"column:value_delta;type:decimal(38,18);not null;default:0"`
	Currency       string          `gorm:"column:currency;size:10;not null;default:''"`
	Reason         string          `gorm:"column:reason;size:30;not null"`
	Status         string          `gorm:"column:status;size:20;not null;default:OPEN"`
	ResolutionNote *string         `gorm:"column:resolution_note;type:text"`
	ResolvedBy     *string         `gorm:"column:resolved_by;size:100"`
	ResolvedAt     *time.Time      `gorm:"column:resolved_at"`
	Attributes     JSONMap         `gorm:"column:attributes;type:jsonb"`
}

// TableName returns the table name for the variance entity.
func (VarianceEntity) TableName() string {
	return "variance"
}

// VarianceRepository provides GORM-based persistence for variances.
type VarianceRepository struct {
	db *gorm.DB
}

// NewVarianceRepository creates a new variance repository.
func NewVarianceRepository(db *gorm.DB) *VarianceRepository {
	return &VarianceRepository{db: db}
}

// withTenantTransaction executes fn within a tenant-scoped transaction.
func (r *VarianceRepository) withTenantTransaction(ctx context.Context, fn func(tx *gorm.DB) error) error {
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
func (r *VarianceRepository) isInTransaction() bool {
	if r.db.Statement == nil || r.db.Statement.ConnPool == nil {
		return false
	}
	committer, ok := r.db.Statement.ConnPool.(gorm.TxCommitter)
	return ok && committer != nil
}

// Create persists a new Variance.
func (r *VarianceRepository) Create(ctx context.Context, variance *domain.Variance) error {
	entity := toVarianceEntity(variance)
	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Create(entity).Error
	})
}

// CreateBatch persists multiple variances atomically.
func (r *VarianceRepository) CreateBatch(ctx context.Context, variances []*domain.Variance) error {
	if len(variances) == 0 {
		return nil
	}

	entities := make([]VarianceEntity, 0, len(variances))
	for _, v := range variances {
		entities = append(entities, *toVarianceEntity(v))
	}

	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		return tx.CreateInBatches(entities, 100).Error
	})
}

// FindByID retrieves a Variance by its VarianceID.
func (r *VarianceRepository) FindByID(ctx context.Context, varianceID uuid.UUID) (*domain.Variance, error) {
	var entity VarianceEntity
	var queryErr error

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Where("variance_id = ?", varianceID).First(&entity)
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

	return toVarianceDomain(&entity), nil
}

// FindByRunID retrieves all variances for a settlement run.
func (r *VarianceRepository) FindByRunID(ctx context.Context, runID uuid.UUID) ([]*domain.Variance, error) {
	var entities []VarianceEntity

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Where("run_id = ?", runID).
			Order("created_at ASC").
			Find(&entities).Error
	})
	if err != nil {
		return nil, err
	}

	return toVarianceDomainSlice(entities), nil
}

// Update updates an existing Variance.
func (r *VarianceRepository) Update(ctx context.Context, variance *domain.Variance) error {
	entity := toVarianceEntity(variance)
	var rowsAffected int64

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Model(&VarianceEntity{}).
			Where("variance_id = ?", entity.VarianceID).
			Updates(map[string]interface{}{
				"status":          entity.Status,
				"value_delta":     entity.ValueDelta,
				"currency":        entity.Currency,
				"resolution_note": entity.ResolutionNote,
				"resolved_by":     entity.ResolvedBy,
				"resolved_at":     entity.ResolvedAt,
				"attributes":      entity.Attributes,
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
		return domain.ErrNotFound
	}
	return nil
}

// DeleteByRunID removes all variances for a given settlement run.
func (r *VarianceRepository) DeleteByRunID(ctx context.Context, runID uuid.UUID) error {
	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Where("run_id = ?", runID).Delete(&VarianceEntity{}).Error
	})
}

// List retrieves variances matching the given filter.
func (r *VarianceRepository) List(ctx context.Context, filter domain.VarianceFilter) ([]*domain.Variance, error) {
	var entities []VarianceEntity

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		query := tx.Model(&VarianceEntity{})

		if filter.RunID != nil {
			query = query.Where("run_id = ?", *filter.RunID)
		}
		if filter.AccountID != nil {
			query = query.Where("account_id = ?", *filter.AccountID)
		}
		if filter.Status != nil {
			query = query.Where("status = ?", string(*filter.Status))
		}
		if filter.Reason != nil {
			query = query.Where("reason = ?", string(*filter.Reason))
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

	return toVarianceDomainSlice(entities), nil
}

// UpdateStatus updates the status of a variance by its VarianceID.
func (r *VarianceRepository) UpdateStatus(ctx context.Context, varianceID uuid.UUID, status domain.VarianceStatus) error {
	var rowsAffected int64

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Model(&VarianceEntity{}).
			Where("variance_id = ?", varianceID).
			Updates(map[string]interface{}{
				"status":     string(status),
				"updated_at": time.Now().UTC(),
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
		return domain.ErrNotFound
	}
	return nil
}

// toVarianceEntity converts a domain Variance to a persistence entity.
func toVarianceEntity(v *domain.Variance) *VarianceEntity {
	entity := &VarianceEntity{
		VarianceID:     v.VarianceID,
		RunID:          v.RunID,
		SnapshotID:     v.SnapshotID,
		AccountID:      v.AccountID,
		InstrumentCode: v.InstrumentCode,
		ExpectedAmount: v.ExpectedAmount,
		ActualAmount:   v.ActualAmount,
		VarianceAmount: v.VarianceAmount,
		ValueDelta:     v.ValueDelta,
		Currency:       v.Currency,
		Reason:         string(v.Reason),
		Status:         string(v.Status),
		ResolvedAt:     v.ResolvedAt,
		CreatedAt:      v.CreatedAt,
		UpdatedAt:      v.UpdatedAt,
	}

	if v.ResolutionNote != "" {
		entity.ResolutionNote = &v.ResolutionNote
	}
	if v.ResolvedBy != "" {
		entity.ResolvedBy = &v.ResolvedBy
	}
	if v.Attributes != nil {
		entity.Attributes = JSONMap(v.Attributes)
	}

	return entity
}

// toVarianceDomain converts a persistence entity to a domain Variance.
func toVarianceDomain(e *VarianceEntity) *domain.Variance {
	v := &domain.Variance{
		VarianceID:     e.VarianceID,
		RunID:          e.RunID,
		SnapshotID:     e.SnapshotID,
		AccountID:      e.AccountID,
		InstrumentCode: e.InstrumentCode,
		ExpectedAmount: e.ExpectedAmount,
		ActualAmount:   e.ActualAmount,
		VarianceAmount: e.VarianceAmount,
		ValueDelta:     e.ValueDelta,
		Currency:       e.Currency,
		Reason:         domain.VarianceReason(e.Reason),
		Status:         domain.VarianceStatus(e.Status),
		ResolvedAt:     e.ResolvedAt,
		CreatedAt:      e.CreatedAt,
		UpdatedAt:      e.UpdatedAt,
	}

	if e.ResolutionNote != nil {
		v.ResolutionNote = *e.ResolutionNote
	}
	if e.ResolvedBy != nil {
		v.ResolvedBy = *e.ResolvedBy
	}
	if e.Attributes != nil {
		v.Attributes = map[string]string(e.Attributes)
	}

	return v
}

// toVarianceDomainSlice converts a slice of entities to domain objects.
func toVarianceDomainSlice(entities []VarianceEntity) []*domain.Variance {
	variances := make([]*domain.Variance, 0, len(entities))
	for i := range entities {
		variances = append(variances, toVarianceDomain(&entities[i]))
	}
	return variances
}
