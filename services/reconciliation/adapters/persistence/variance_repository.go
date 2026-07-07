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

	// BusinessRunID and BusinessSnapshotID are populated read-only on retrieval via
	// joins to settlement_run.run_id and settlement_snapshot.snapshot_id. They are not
	// stored columns: variance.run_id and variance.snapshot_id hold surrogate PKs (FK
	// targets), while the domain layer and API work in business identifiers.
	BusinessRunID      uuid.UUID `gorm:"->;column:business_run_id"`
	BusinessSnapshotID uuid.UUID `gorm:"->;column:business_snapshot_id"`
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

// Create persists a new Variance. The variance's domain RunID and SnapshotID are
// business identifiers; they are resolved to the settlement_run and settlement_snapshot
// surrogate PKs before being written to the run_id and snapshot_id FK columns.
func (r *VarianceRepository) Create(ctx context.Context, variance *domain.Variance) error {
	entities := []VarianceEntity{*toVarianceEntity(variance)}
	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		if err := assignVarianceSurrogates(tx, entities, []*domain.Variance{variance}); err != nil {
			return err
		}
		return tx.Create(&entities[0]).Error
	})
}

// CreateBatch persists multiple variances atomically. Each variance's business RunID
// and SnapshotID are resolved to surrogate PKs for the FK columns.
func (r *VarianceRepository) CreateBatch(ctx context.Context, variances []*domain.Variance) error {
	if len(variances) == 0 {
		return nil
	}

	entities := make([]VarianceEntity, 0, len(variances))
	for _, v := range variances {
		entities = append(entities, *toVarianceEntity(v))
	}

	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		if err := assignVarianceSurrogates(tx, entities, variances); err != nil {
			return err
		}
		return tx.CreateInBatches(entities, 100).Error
	})
}

// assignVarianceSurrogates resolves each variance's business RunID and SnapshotID to
// the settlement_run and settlement_snapshot surrogate PKs and writes them into the
// entity's FK columns, caching resolutions to minimize lookups within a batch.
func assignVarianceSurrogates(tx *gorm.DB, entities []VarianceEntity, variances []*domain.Variance) error {
	runCache := make(map[uuid.UUID]uuid.UUID)
	snapCache := make(map[uuid.UUID]uuid.UUID)
	for i := range entities {
		runSurrogate, err := cachedResolve(runCache, variances[i].RunID, tx, resolveRunSurrogate)
		if err != nil {
			return err
		}
		snapSurrogate, err := cachedResolve(snapCache, variances[i].SnapshotID, tx, resolveSnapshotSurrogate)
		if err != nil {
			return err
		}
		entities[i].RunID = runSurrogate
		entities[i].SnapshotID = snapSurrogate
	}
	return nil
}

// cachedResolve returns the surrogate for a business ID, resolving via fn and caching
// the result so repeated business IDs in a batch issue a single lookup.
func cachedResolve(
	cache map[uuid.UUID]uuid.UUID,
	businessID uuid.UUID,
	tx *gorm.DB,
	fn func(*gorm.DB, uuid.UUID) (uuid.UUID, error),
) (uuid.UUID, error) {
	if surrogate, ok := cache[businessID]; ok {
		return surrogate, nil
	}
	surrogate, err := fn(tx, businessID)
	if err != nil {
		return uuid.Nil, err
	}
	cache[businessID] = surrogate
	return surrogate, nil
}

// varianceReadQuery builds a query that joins settlement_run and settlement_snapshot so
// the business run_id and snapshot_id are projected into the entity, keeping the domain
// model and API in business identifiers rather than surrogate PKs.
func varianceReadQuery(tx *gorm.DB) *gorm.DB {
	return tx.Model(&VarianceEntity{}).
		Select(`"variance".*, "settlement_run"."run_id" AS business_run_id, "settlement_snapshot"."snapshot_id" AS business_snapshot_id`).
		Joins(`JOIN "settlement_run" ON "settlement_run"."id" = "variance"."run_id"`).
		Joins(`JOIN "settlement_snapshot" ON "settlement_snapshot"."id" = "variance"."snapshot_id"`)
}

// FindByID retrieves a Variance by its VarianceID.
func (r *VarianceRepository) FindByID(ctx context.Context, varianceID uuid.UUID) (*domain.Variance, error) {
	var entity VarianceEntity
	var queryErr error

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		result := varianceReadQuery(tx).Where(`"variance"."variance_id" = ?`, varianceID).First(&entity)
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

// FindByRunID retrieves all variances for a settlement run, keyed by the business run
// identifier. The join filters on settlement_run.run_id, so an unknown run yields an
// empty slice.
func (r *VarianceRepository) FindByRunID(ctx context.Context, runID uuid.UUID) ([]*domain.Variance, error) {
	var entities []VarianceEntity

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		return varianceReadQuery(tx).
			Where(`"settlement_run"."run_id" = ?`, runID).
			Order(`"variance"."created_at" ASC`).
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

// DeleteByRunID removes all variances for a given settlement run, keyed by the business
// run identifier. An unknown run is a no-op.
func (r *VarianceRepository) DeleteByRunID(ctx context.Context, runID uuid.UUID) error {
	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		surrogate, err := resolveRunSurrogate(tx, runID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		return tx.Where("run_id = ?", surrogate).Delete(&VarianceEntity{}).Error
	})
}

// List retrieves variances matching the given filter.
func (r *VarianceRepository) List(ctx context.Context, filter domain.VarianceFilter) ([]*domain.Variance, error) {
	var entities []VarianceEntity

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		query := varianceReadQuery(tx)

		if filter.RunID != nil {
			query = query.Where(`"settlement_run"."run_id" = ?`, *filter.RunID)
		}
		if filter.AccountID != nil {
			query = query.Where(`"variance"."account_id" = ?`, *filter.AccountID)
		}
		if filter.Status != nil {
			query = query.Where(`"variance"."status" = ?`, string(*filter.Status))
		}
		if filter.Reason != nil {
			query = query.Where(`"variance"."reason" = ?`, string(*filter.Reason))
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

		return query.Order(`"variance"."created_at" DESC`).Find(&entities).Error
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

// toVarianceDomain converts a persistence entity to a domain Variance. RunID and
// SnapshotID are taken from the business identifiers projected via the read-path joins
// (settlement_run.run_id and settlement_snapshot.snapshot_id), not the surrogate FKs
// stored in the run_id and snapshot_id columns.
func toVarianceDomain(e *VarianceEntity) *domain.Variance {
	v := &domain.Variance{
		VarianceID:     e.VarianceID,
		RunID:          e.BusinessRunID,
		SnapshotID:     e.BusinessSnapshotID,
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
