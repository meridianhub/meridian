package persistence

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/meridianhub/meridian/shared/platform/db"
	"gorm.io/gorm"
)

// Compile-time check that SettlementRunRepository implements domain.SettlementRunRepository.
var _ domain.SettlementRunRepository = (*SettlementRunRepository)(nil)

// SettlementRunEntity is the GORM entity for the settlement_run table.
type SettlementRunEntity struct {
	ID             uuid.UUID  `gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	CreatedAt      time.Time  `gorm:"not null;default:now()"`
	UpdatedAt      time.Time  `gorm:"not null;default:now()"`
	RunID          uuid.UUID  `gorm:"column:run_id;uniqueIndex:idx_sr_run_id;type:uuid;not null"`
	AccountID      string     `gorm:"column:account_id;index:idx_sr_account_id;size:34;not null"`
	Scope          string     `gorm:"column:scope;size:20;not null;default:ACCOUNT"`
	SettlementType string     `gorm:"column:settlement_type;size:20;not null;default:DAILY"`
	Status         string     `gorm:"column:status;index:idx_sr_status;size:20;not null;default:PENDING"`
	PeriodStart    time.Time  `gorm:"column:period_start;not null"`
	PeriodEnd      time.Time  `gorm:"column:period_end;not null"`
	InitiatedBy    string     `gorm:"column:initiated_by;size:100;not null"`
	CompletedAt    *time.Time `gorm:"column:completed_at"`
	VarianceCount  int        `gorm:"column:variance_count;not null;default:0"`
	FailureReason  *string    `gorm:"column:failure_reason;type:text"`
	Attributes     JSONMap    `gorm:"column:attributes;type:jsonb"`
	Version        int64      `gorm:"column:version;not null;default:1"`
}

// TableName returns the table name for the settlement run entity.
func (SettlementRunEntity) TableName() string {
	return "settlement_run"
}

// SettlementRunRepository provides GORM-based persistence for settlement runs.
type SettlementRunRepository struct {
	db *gorm.DB
}

// NewSettlementRunRepository creates a new settlement run repository.
func NewSettlementRunRepository(db *gorm.DB) *SettlementRunRepository {
	return &SettlementRunRepository{db: db}
}

// withTenantTransaction executes fn within a tenant-scoped transaction.
func (r *SettlementRunRepository) withTenantTransaction(ctx context.Context, fn func(tx *gorm.DB) error) error {
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
func (r *SettlementRunRepository) isInTransaction() bool {
	if r.db.Statement == nil || r.db.Statement.ConnPool == nil {
		return false
	}
	committer, ok := r.db.Statement.ConnPool.(gorm.TxCommitter)
	return ok && committer != nil
}

// Create persists a new SettlementRun.
func (r *SettlementRunRepository) Create(ctx context.Context, run *domain.SettlementRun) error {
	entity := toSettlementRunEntity(run)
	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Create(entity).Error
	})
}

// FindByID retrieves a SettlementRun by its RunID.
func (r *SettlementRunRepository) FindByID(ctx context.Context, runID uuid.UUID) (*domain.SettlementRun, error) {
	var entity SettlementRunEntity
	var queryErr error

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Where("run_id = ?", runID).First(&entity)
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

	return toSettlementRunDomain(&entity), nil
}

// Update updates an existing SettlementRun using optimistic locking.
func (r *SettlementRunRepository) Update(ctx context.Context, run *domain.SettlementRun) error {
	entity := toSettlementRunEntity(run)
	var rowsAffected int64

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Model(&SettlementRunEntity{}).
			Where("run_id = ? AND version = ?", entity.RunID, entity.Version-1).
			Updates(map[string]interface{}{
				"status":         entity.Status,
				"completed_at":   entity.CompletedAt,
				"variance_count": entity.VarianceCount,
				"failure_reason": entity.FailureReason,
				"attributes":     entity.Attributes,
				"version":        entity.Version,
				"updated_at":     time.Now().UTC(),
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
		// Determine if the run doesn't exist or the version conflicted
		var count int64
		countErr := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
			return tx.Model(&SettlementRunEntity{}).Where("run_id = ?", entity.RunID).Count(&count).Error
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

// List retrieves settlement runs matching the given filter with pagination.
func (r *SettlementRunRepository) List(ctx context.Context, filter domain.RunFilter) ([]*domain.SettlementRun, error) {
	var entities []SettlementRunEntity

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		query := tx.Model(&SettlementRunEntity{})

		if filter.AccountID != nil {
			query = query.Where("account_id = ?", *filter.AccountID)
		}
		if filter.Status != nil {
			query = query.Where("status = ?", string(*filter.Status))
		}
		if filter.Scope != nil {
			query = query.Where("scope = ?", string(*filter.Scope))
		}
		if filter.FromDate != nil {
			query = query.Where("period_start >= ?", *filter.FromDate)
		}
		if filter.ToDate != nil {
			query = query.Where("period_end <= ?", *filter.ToDate)
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

	return toSettlementRunDomainSlice(entities), nil
}

// toSettlementRunEntity converts a domain SettlementRun to a persistence entity.
func toSettlementRunEntity(r *domain.SettlementRun) *SettlementRunEntity {
	entity := &SettlementRunEntity{
		RunID:          r.RunID,
		AccountID:      r.AccountID,
		Scope:          string(r.Scope),
		SettlementType: string(r.SettlementType),
		Status:         string(r.Status),
		PeriodStart:    r.PeriodStart,
		PeriodEnd:      r.PeriodEnd,
		InitiatedBy:    r.InitiatedBy,
		CompletedAt:    r.CompletedAt,
		VarianceCount:  r.VarianceCount,
		CreatedAt:      r.CreatedAt,
		UpdatedAt:      r.UpdatedAt,
		Version:        r.Version,
	}

	if r.FailureReason != "" {
		entity.FailureReason = &r.FailureReason
	}
	if r.Attributes != nil {
		entity.Attributes = JSONMap(r.Attributes)
	}

	return entity
}

// toSettlementRunDomain converts a persistence entity to a domain SettlementRun.
func toSettlementRunDomain(e *SettlementRunEntity) *domain.SettlementRun {
	run := &domain.SettlementRun{
		RunID:          e.RunID,
		AccountID:      e.AccountID,
		Scope:          domain.ReconciliationScope(e.Scope),
		SettlementType: domain.SettlementType(e.SettlementType),
		Status:         domain.RunStatus(e.Status),
		PeriodStart:    e.PeriodStart,
		PeriodEnd:      e.PeriodEnd,
		InitiatedBy:    e.InitiatedBy,
		CompletedAt:    e.CompletedAt,
		VarianceCount:  e.VarianceCount,
		CreatedAt:      e.CreatedAt,
		UpdatedAt:      e.UpdatedAt,
		Version:        e.Version,
	}

	if e.FailureReason != nil {
		run.FailureReason = *e.FailureReason
	}
	if e.Attributes != nil {
		run.Attributes = map[string]string(e.Attributes)
	}

	return run
}

// toSettlementRunDomainSlice converts a slice of entities to domain objects.
func toSettlementRunDomainSlice(entities []SettlementRunEntity) []*domain.SettlementRun {
	runs := make([]*domain.SettlementRun, 0, len(entities))
	for i := range entities {
		runs = append(runs, toSettlementRunDomain(&entities[i]))
	}
	return runs
}
