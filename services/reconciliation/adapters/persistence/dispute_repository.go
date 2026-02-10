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

// Compile-time check that DisputeRepository implements domain.DisputeRepository.
var _ domain.DisputeRepository = (*DisputeRepository)(nil)

// DisputeRepository provides GORM-based persistence for disputes.
type DisputeRepository struct {
	db *gorm.DB
}

// NewDisputeRepository creates a new dispute repository.
func NewDisputeRepository(db *gorm.DB) *DisputeRepository {
	return &DisputeRepository{db: db}
}

// withTenantTransaction executes fn within a tenant-scoped transaction.
func (r *DisputeRepository) withTenantTransaction(ctx context.Context, fn func(tx *gorm.DB) error) error {
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
func (r *DisputeRepository) isInTransaction() bool {
	if r.db.Statement == nil || r.db.Statement.ConnPool == nil {
		return false
	}
	committer, ok := r.db.Statement.ConnPool.(gorm.TxCommitter)
	return ok && committer != nil
}

// Create persists a new Dispute.
func (r *DisputeRepository) Create(ctx context.Context, dispute *domain.Dispute) error {
	entity := toDisputeEntity(dispute)
	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Create(entity).Error
	})
}

// FindByID retrieves a Dispute by its DisputeID.
func (r *DisputeRepository) FindByID(ctx context.Context, disputeID uuid.UUID) (*domain.Dispute, error) {
	var entity DisputeEntity
	var queryErr error

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Where("dispute_id = ?", disputeID).First(&entity)
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

	return toDisputeDomain(&entity), nil
}

// FindByVarianceID retrieves all disputes for a variance.
func (r *DisputeRepository) FindByVarianceID(ctx context.Context, varianceID uuid.UUID) ([]*domain.Dispute, error) {
	var entities []DisputeEntity

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Where("variance_id = ?", varianceID).
			Order("created_at ASC").
			Find(&entities).Error
	})
	if err != nil {
		return nil, err
	}

	return toDisputeDomainSlice(entities), nil
}

// Update updates an existing Dispute.
func (r *DisputeRepository) Update(ctx context.Context, dispute *domain.Dispute) error {
	entity := toDisputeEntity(dispute)
	var rowsAffected int64

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Model(&DisputeEntity{}).
			Where("dispute_id = ?", entity.DisputeID).
			Updates(map[string]interface{}{
				"status":      entity.Status,
				"resolution":  entity.Resolution,
				"resolved_by": entity.ResolvedBy,
				"resolved_at": entity.ResolvedAt,
				"updated_at":  time.Now().UTC(),
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

// List retrieves disputes matching the given filter.
func (r *DisputeRepository) List(ctx context.Context, filter domain.DisputeFilter) ([]*domain.Dispute, error) {
	var entities []DisputeEntity

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		query := tx.Model(&DisputeEntity{})

		if filter.RunID != nil {
			query = query.Where("run_id = ?", *filter.RunID)
		}
		if filter.AccountID != nil {
			query = query.Where("account_id = ?", *filter.AccountID)
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

	return toDisputeDomainSlice(entities), nil
}

// toDisputeEntity converts a domain Dispute to a persistence entity.
func toDisputeEntity(d *domain.Dispute) *DisputeEntity {
	entity := &DisputeEntity{
		DisputeID:  d.DisputeID,
		VarianceID: d.VarianceID,
		RunID:      d.RunID,
		AccountID:  d.AccountID,
		Status:     string(d.Status),
		Reason:     d.Reason,
		RaisedBy:   d.RaisedBy,
		ResolvedAt: d.ResolvedAt,
		CreatedAt:  d.CreatedAt,
		UpdatedAt:  d.UpdatedAt,
	}

	if d.Resolution != "" {
		entity.Resolution = &d.Resolution
	}
	if d.ResolvedBy != "" {
		entity.ResolvedBy = &d.ResolvedBy
	}
	if d.Attributes != nil {
		entity.Attributes = JSONMap(d.Attributes)
	}

	return entity
}

// toDisputeDomain converts a persistence entity to a domain Dispute.
func toDisputeDomain(e *DisputeEntity) *domain.Dispute {
	d := &domain.Dispute{
		DisputeID:  e.DisputeID,
		VarianceID: e.VarianceID,
		RunID:      e.RunID,
		AccountID:  e.AccountID,
		Status:     domain.DisputeStatus(e.Status),
		Reason:     e.Reason,
		RaisedBy:   e.RaisedBy,
		ResolvedAt: e.ResolvedAt,
		CreatedAt:  e.CreatedAt,
		UpdatedAt:  e.UpdatedAt,
	}

	if e.Resolution != nil {
		d.Resolution = *e.Resolution
	}
	if e.ResolvedBy != nil {
		d.ResolvedBy = *e.ResolvedBy
	}
	if e.Attributes != nil {
		d.Attributes = map[string]string(e.Attributes)
	}

	return d
}

// toDisputeDomainSlice converts a slice of entities to domain objects.
func toDisputeDomainSlice(entities []DisputeEntity) []*domain.Dispute {
	disputes := make([]*domain.Dispute, 0, len(entities))
	for i := range entities {
		disputes = append(disputes, toDisputeDomain(&entities[i]))
	}
	return disputes
}
