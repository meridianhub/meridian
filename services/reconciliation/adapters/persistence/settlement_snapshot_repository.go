package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/meridianhub/meridian/shared/platform/db"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// Compile-time check that SettlementSnapshotRepository implements domain.SettlementSnapshotRepository.
var _ domain.SettlementSnapshotRepository = (*SettlementSnapshotRepository)(nil)

// SettlementSnapshotEntity is the GORM entity for the settlement_snapshot table.
type SettlementSnapshotEntity struct {
	ID              uuid.UUID       `gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	CreatedAt       time.Time       `gorm:"not null;default:now()"`
	SnapshotID      uuid.UUID       `gorm:"column:snapshot_id;uniqueIndex:idx_ss_snap_id;type:uuid;not null"`
	RunID           uuid.UUID       `gorm:"column:run_id;index:idx_ss_run_id;type:uuid;not null"`
	AccountID       string          `gorm:"column:account_id;index:idx_ss_account_id;size:34;not null"`
	InstrumentCode  string          `gorm:"column:instrument_code;size:20;not null"`
	ExpectedBalance decimal.Decimal `gorm:"column:expected_balance;type:decimal(38,18);not null"`
	ActualBalance   decimal.Decimal `gorm:"column:actual_balance;type:decimal(38,18);not null"`
	VarianceAmount  decimal.Decimal `gorm:"column:variance_amount;type:decimal(38,18);not null"`
	SourceSystem    string          `gorm:"column:source_system;size:100;not null"`
	Attributes      JSONMap         `gorm:"column:attributes;type:jsonb"`
	CapturedAt      time.Time       `gorm:"column:captured_at;not null"`
}

// TableName returns the table name for the settlement snapshot entity.
func (SettlementSnapshotEntity) TableName() string {
	return "settlement_snapshot"
}

// SettlementSnapshotRepository provides GORM-based persistence for settlement snapshots.
type SettlementSnapshotRepository struct {
	db *gorm.DB
}

// NewSettlementSnapshotRepository creates a new settlement snapshot repository.
func NewSettlementSnapshotRepository(db *gorm.DB) *SettlementSnapshotRepository {
	return &SettlementSnapshotRepository{db: db}
}

// withTenantTransaction executes fn within a tenant-scoped transaction.
func (r *SettlementSnapshotRepository) withTenantTransaction(ctx context.Context, fn func(tx *gorm.DB) error) error {
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
func (r *SettlementSnapshotRepository) isInTransaction() bool {
	if r.db.Statement == nil || r.db.Statement.ConnPool == nil {
		return false
	}
	committer, ok := r.db.Statement.ConnPool.(gorm.TxCommitter)
	return ok && committer != nil
}

// Create persists a new SettlementSnapshot.
func (r *SettlementSnapshotRepository) Create(ctx context.Context, snapshot *domain.SettlementSnapshot) error {
	entity := toSnapshotEntity(snapshot)
	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Create(entity).Error
	})
}

// CreateBatch persists multiple snapshots atomically.
func (r *SettlementSnapshotRepository) CreateBatch(ctx context.Context, snapshots []*domain.SettlementSnapshot) error {
	if len(snapshots) == 0 {
		return nil
	}

	entities := make([]SettlementSnapshotEntity, 0, len(snapshots))
	for _, s := range snapshots {
		entities = append(entities, *toSnapshotEntity(s))
	}

	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		return tx.CreateInBatches(entities, 100).Error
	})
}

// FindByID retrieves a SettlementSnapshot by its SnapshotID.
func (r *SettlementSnapshotRepository) FindByID(ctx context.Context, snapshotID uuid.UUID) (*domain.SettlementSnapshot, error) {
	var entity SettlementSnapshotEntity
	var queryErr error

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Where("snapshot_id = ?", snapshotID).First(&entity)
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

	return toSnapshotDomain(&entity), nil
}

// FindByRunID retrieves all snapshots for a settlement run.
func (r *SettlementSnapshotRepository) FindByRunID(ctx context.Context, runID uuid.UUID) ([]*domain.SettlementSnapshot, error) {
	var entities []SettlementSnapshotEntity

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Where("run_id = ?", runID).
			Order("created_at ASC").
			Find(&entities).Error
	})
	if err != nil {
		return nil, err
	}

	return toSnapshotDomainSlice(entities), nil
}

// DeleteByRunID removes all snapshots for a given settlement run.
func (r *SettlementSnapshotRepository) DeleteByRunID(ctx context.Context, runID uuid.UUID) error {
	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Where("run_id = ?", runID).Delete(&SettlementSnapshotEntity{}).Error
	})
}

// MarkRunSnapshotsFinal updates all snapshots for a run to include settlement_type=FINAL in their attributes.
// runID is the business identifier (settlement_run.run_id); this method resolves it to the surrogate PK
// (settlement_run.id) before updating, since settlement_snapshot.run_id is a FK to settlement_run.id.
func (r *SettlementSnapshotRepository) MarkRunSnapshotsFinal(ctx context.Context, runID uuid.UUID) error {
	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var runEntity SettlementRunEntity
		if err := tx.Select("id").Where("run_id = ?", runID).First(&runEntity).Error; err != nil {
			return fmt.Errorf("resolving surrogate ID for run %s: %w", runID, err)
		}
		// Use CASE to handle NULL/JSON-null attributes vs existing JSONB objects.
		// CockroachDB's || operator requires both operands to be JSONB objects,
		// so we must replace NULL/json-null with an empty object before merging.
		return tx.Exec(
			`UPDATE "settlement_snapshot" SET "attributes" = CASE WHEN "attributes" IS NULL OR "attributes"::text = 'null' THEN '{"settlement_type":"FINAL"}'::jsonb ELSE "attributes" || '{"settlement_type":"FINAL"}'::jsonb END WHERE "run_id" = ?`,
			runEntity.ID,
		).Error
	})
}

// toSnapshotEntity converts a domain SettlementSnapshot to a persistence entity.
func toSnapshotEntity(s *domain.SettlementSnapshot) *SettlementSnapshotEntity {
	entity := &SettlementSnapshotEntity{
		SnapshotID:      s.SnapshotID,
		RunID:           s.RunID,
		AccountID:       s.AccountID,
		InstrumentCode:  s.InstrumentCode,
		ExpectedBalance: s.ExpectedBalance,
		ActualBalance:   s.ActualBalance,
		VarianceAmount:  s.VarianceAmount,
		SourceSystem:    s.SourceSystem,
		CapturedAt:      s.CapturedAt,
		CreatedAt:       s.CreatedAt,
	}

	if s.Attributes != nil {
		entity.Attributes = JSONMap(s.Attributes)
	}

	return entity
}

// toSnapshotDomain converts a persistence entity to a domain SettlementSnapshot.
func toSnapshotDomain(e *SettlementSnapshotEntity) *domain.SettlementSnapshot {
	s := &domain.SettlementSnapshot{
		SnapshotID:      e.SnapshotID,
		RunID:           e.RunID,
		AccountID:       e.AccountID,
		InstrumentCode:  e.InstrumentCode,
		ExpectedBalance: e.ExpectedBalance,
		ActualBalance:   e.ActualBalance,
		VarianceAmount:  e.VarianceAmount,
		SourceSystem:    e.SourceSystem,
		CapturedAt:      e.CapturedAt,
		CreatedAt:       e.CreatedAt,
	}

	if e.Attributes != nil {
		s.Attributes = map[string]string(e.Attributes)
	}

	return s
}

// toSnapshotDomainSlice converts a slice of entities to domain objects.
func toSnapshotDomainSlice(entities []SettlementSnapshotEntity) []*domain.SettlementSnapshot {
	snapshots := make([]*domain.SettlementSnapshot, 0, len(entities))
	for i := range entities {
		snapshots = append(snapshots, toSnapshotDomain(&entities[i]))
	}
	return snapshots
}
