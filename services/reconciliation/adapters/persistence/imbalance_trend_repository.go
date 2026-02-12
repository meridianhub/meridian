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
	"gorm.io/gorm/clause"
)

// Compile-time check that ImbalanceTrendRepository implements domain.ImbalanceTrendRepository.
var _ domain.ImbalanceTrendRepository = (*ImbalanceTrendRepository)(nil)

// ImbalanceTrendEntity is the GORM entity for the imbalance_trend table.
type ImbalanceTrendEntity struct {
	ID                  uuid.UUID       `gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	CreatedAt           time.Time       `gorm:"not null;default:now()"`
	UpdatedAt           time.Time       `gorm:"not null;default:now()"`
	TrendID             uuid.UUID       `gorm:"column:trend_id;uniqueIndex:idx_it_trend_id;type:uuid;not null"`
	InstrumentCode      string          `gorm:"column:instrument_code;uniqueIndex:idx_it_instrument_code;size:20;not null"`
	FirstDetectedAt     time.Time       `gorm:"column:first_detected_at;not null"`
	LastDetectedAt      time.Time       `gorm:"column:last_detected_at;not null"`
	ConsecutiveDays     int             `gorm:"column:consecutive_days;not null;default:0"`
	TotalOccurrences    int             `gorm:"column:total_occurrences;not null;default:0"`
	LastImbalanceAmount decimal.Decimal `gorm:"column:last_imbalance_amount;type:decimal(38,18);not null"`
	LastAssertionID     *uuid.UUID      `gorm:"column:last_assertion_id;type:uuid"`
	ResolvedAt          *time.Time      `gorm:"column:resolved_at"`
	Metadata            JSONMap         `gorm:"column:metadata;type:jsonb"`
}

// TableName returns the table name for the imbalance trend entity.
func (ImbalanceTrendEntity) TableName() string {
	return "imbalance_trend"
}

// ImbalanceTrendRepository provides GORM-based persistence for imbalance trends.
type ImbalanceTrendRepository struct {
	db *gorm.DB
}

// NewImbalanceTrendRepository creates a new imbalance trend repository.
func NewImbalanceTrendRepository(db *gorm.DB) *ImbalanceTrendRepository {
	return &ImbalanceTrendRepository{db: db}
}

// withTenantTransaction executes fn within a tenant-scoped transaction.
func (r *ImbalanceTrendRepository) withTenantTransaction(ctx context.Context, fn func(tx *gorm.DB) error) error {
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
func (r *ImbalanceTrendRepository) isInTransaction() bool {
	if r.db.Statement == nil || r.db.Statement.ConnPool == nil {
		return false
	}
	committer, ok := r.db.Statement.ConnPool.(gorm.TxCommitter)
	return ok && committer != nil
}

// Upsert creates or updates an imbalance trend for an instrument code.
func (r *ImbalanceTrendRepository) Upsert(ctx context.Context, trend *domain.ImbalanceTrend) error {
	entity := toImbalanceTrendEntity(trend)
	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "instrument_code"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"last_detected_at",
				"consecutive_days",
				"total_occurrences",
				"last_imbalance_amount",
				"last_assertion_id",
				"resolved_at",
				"metadata",
				"updated_at",
			}),
		}).Create(entity).Error
	})
}

// FindByInstrumentCode retrieves the active (unresolved) trend for an instrument.
// Returns ErrNotFound if no active trend exists.
func (r *ImbalanceTrendRepository) FindByInstrumentCode(ctx context.Context, instrumentCode string) (*domain.ImbalanceTrend, error) {
	var entity ImbalanceTrendEntity
	var queryErr error

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Where("instrument_code = ? AND resolved_at IS NULL", instrumentCode).First(&entity)
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

	return toDomainImbalanceTrend(&entity), nil
}

// toImbalanceTrendEntity converts a domain ImbalanceTrend to a persistence entity.
func toImbalanceTrendEntity(t *domain.ImbalanceTrend) *ImbalanceTrendEntity {
	entity := &ImbalanceTrendEntity{
		TrendID:             t.TrendID,
		InstrumentCode:      t.InstrumentCode,
		FirstDetectedAt:     t.FirstDetectedAt,
		LastDetectedAt:      t.LastDetectedAt,
		ConsecutiveDays:     t.ConsecutiveDays,
		TotalOccurrences:    t.ConsecutiveDays, // Map from domain field
		LastImbalanceAmount: t.LastImbalanceAmount,
		ResolvedAt:          t.ResolvedAt,
		UpdatedAt:           time.Now().UTC(),
	}

	if t.LastAssertionID != uuid.Nil {
		entity.LastAssertionID = &t.LastAssertionID
	}

	return entity
}

// toDomainImbalanceTrend converts a persistence entity to a domain ImbalanceTrend.
func toDomainImbalanceTrend(e *ImbalanceTrendEntity) *domain.ImbalanceTrend {
	trend := &domain.ImbalanceTrend{
		TrendID:             e.TrendID,
		InstrumentCode:      e.InstrumentCode,
		ConsecutiveDays:     e.ConsecutiveDays,
		LastImbalanceAmount: e.LastImbalanceAmount,
		FirstDetectedAt:     e.FirstDetectedAt,
		LastDetectedAt:      e.LastDetectedAt,
		ResolvedAt:          e.ResolvedAt,
	}

	if e.LastAssertionID != nil {
		trend.LastAssertionID = *e.LastAssertionID
	}

	return trend
}
