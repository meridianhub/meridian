package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// --- GORM entity models for the integration test ---
// These mirror the migration schema where `id` is the PK and `run_id`/`snapshot_id` are business keys.

type settlementRunEntity struct {
	ID             uuid.UUID  `gorm:"column:id;type:uuid;primaryKey"`
	CreatedAt      time.Time  `gorm:"column:created_at"`
	UpdatedAt      time.Time  `gorm:"column:updated_at"`
	RunID          uuid.UUID  `gorm:"column:run_id;type:uuid"`
	AccountID      string     `gorm:"column:account_id"`
	Scope          string     `gorm:"column:scope"`
	SettlementType string     `gorm:"column:settlement_type"`
	Status         string     `gorm:"column:status"`
	PeriodStart    time.Time  `gorm:"column:period_start"`
	PeriodEnd      time.Time  `gorm:"column:period_end"`
	InitiatedBy    string     `gorm:"column:initiated_by"`
	CompletedAt    *time.Time `gorm:"column:completed_at"`
	VarianceCount  int        `gorm:"column:variance_count"`
	FailureReason  *string    `gorm:"column:failure_reason"`
	Attributes     *string    `gorm:"column:attributes"`
	Version        int64      `gorm:"column:version"`
}

func (settlementRunEntity) TableName() string { return "settlement_run" }

type settlementSnapshotEntity struct {
	ID              uuid.UUID `gorm:"column:id;type:uuid;primaryKey"`
	CreatedAt       time.Time `gorm:"column:created_at"`
	SnapshotID      uuid.UUID `gorm:"column:snapshot_id;type:uuid"`
	RunID           uuid.UUID `gorm:"column:run_id;type:uuid"` // FK to settlement_run.id (the PK)
	AccountID       string    `gorm:"column:account_id"`
	InstrumentCode  string    `gorm:"column:instrument_code"`
	ExpectedBalance string    `gorm:"column:expected_balance"`
	ActualBalance   string    `gorm:"column:actual_balance"`
	VarianceAmount  string    `gorm:"column:variance_amount"`
	SourceSystem    string    `gorm:"column:source_system"`
	Attributes      *string   `gorm:"column:attributes"`
	CapturedAt      time.Time `gorm:"column:captured_at"`
}

func (settlementSnapshotEntity) TableName() string { return "settlement_snapshot" }

// --- GORM-based repository implementations ---

type gormRunRepo struct {
	db *gorm.DB
}

func (r *gormRunRepo) Create(_ context.Context, run *domain.SettlementRun) error {
	entity := runToEntity(run)
	return r.db.Create(&entity).Error
}

func (r *gormRunRepo) FindByID(_ context.Context, runID uuid.UUID) (*domain.SettlementRun, error) {
	var entity settlementRunEntity
	err := r.db.Where("run_id = ?", runID).First(&entity).Error
	if err != nil {
		return nil, domain.ErrNotFound
	}
	return entityToRun(&entity), nil
}

func (r *gormRunRepo) Update(_ context.Context, run *domain.SettlementRun) error {
	// Find the existing entity to get its DB id
	var existing settlementRunEntity
	if err := r.db.Where("run_id = ?", run.RunID).First(&existing).Error; err != nil {
		return domain.ErrNotFound
	}

	// Build the update with optimistic lock on previous version
	entity := runToEntityWithID(existing.ID, run)
	result := r.db.Model(&settlementRunEntity{}).
		Where("id = ? AND version = ?", existing.ID, run.Version-1).
		Updates(map[string]interface{}{
			"status":         entity.Status,
			"completed_at":   entity.CompletedAt,
			"variance_count": entity.VarianceCount,
			"failure_reason": entity.FailureReason,
			"version":        entity.Version,
			"updated_at":     time.Now().UTC(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return domain.ErrOptimisticLock
	}
	return nil
}

func (r *gormRunRepo) List(_ context.Context, _ domain.RunFilter) ([]*domain.SettlementRun, error) {
	return nil, nil
}

// getDBRunID resolves the settlement_run.id (DB PK) from the business run_id.
func (r *gormRunRepo) getDBRunID(runID uuid.UUID) (uuid.UUID, error) {
	var entity settlementRunEntity
	err := r.db.Select("id").Where("run_id = ?", runID).First(&entity).Error
	if err != nil {
		return uuid.Nil, err
	}
	return entity.ID, nil
}

type gormSnapshotRepo struct {
	db      *gorm.DB
	runRepo *gormRunRepo
}

func (r *gormSnapshotRepo) Create(_ context.Context, snap *domain.SettlementSnapshot) error {
	dbRunID, err := r.runRepo.getDBRunID(snap.RunID)
	if err != nil {
		return fmt.Errorf("failed to resolve run ID: %w", err)
	}
	entity := snapshotToEntity(snap, dbRunID)
	return r.db.Create(&entity).Error
}

func (r *gormSnapshotRepo) CreateBatch(_ context.Context, snaps []*domain.SettlementSnapshot) error {
	if len(snaps) == 0 {
		return nil
	}
	dbRunID, err := r.runRepo.getDBRunID(snaps[0].RunID)
	if err != nil {
		return fmt.Errorf("failed to resolve run ID: %w", err)
	}
	entities := make([]settlementSnapshotEntity, len(snaps))
	for i, s := range snaps {
		entities[i] = snapshotToEntity(s, dbRunID)
	}
	return r.db.CreateInBatches(entities, 100).Error
}

func (r *gormSnapshotRepo) FindByID(_ context.Context, snapshotID uuid.UUID) (*domain.SettlementSnapshot, error) {
	var entity settlementSnapshotEntity
	err := r.db.Where("snapshot_id = ?", snapshotID).First(&entity).Error
	if err != nil {
		return nil, domain.ErrNotFound
	}
	return entityToSnapshot(&entity), nil
}

func (r *gormSnapshotRepo) FindByRunID(_ context.Context, runID uuid.UUID) ([]*domain.SettlementSnapshot, error) {
	dbRunID, err := r.runRepo.getDBRunID(runID)
	if err != nil {
		return nil, err
	}
	var entities []settlementSnapshotEntity
	if err := r.db.Where("run_id = ?", dbRunID).Find(&entities).Error; err != nil {
		return nil, err
	}
	result := make([]*domain.SettlementSnapshot, len(entities))
	for i, e := range entities {
		s := entityToSnapshot(&e)
		s.RunID = runID // Map back to business run_id
		result[i] = s
	}
	return result, nil
}

func (r *gormSnapshotRepo) DeleteByRunID(_ context.Context, runID uuid.UUID) error {
	dbRunID, err := r.runRepo.getDBRunID(runID)
	if err != nil {
		// If run doesn't exist in DB, nothing to delete
		return nil //nolint:nilerr // intentional: missing run means no snapshots to clean up
	}
	return r.db.Where("run_id = ?", dbRunID).Delete(&settlementSnapshotEntity{}).Error
}

// --- Entity mappers ---

func runToEntity(run *domain.SettlementRun) settlementRunEntity {
	return runToEntityWithID(uuid.New(), run)
}

func runToEntityWithID(dbID uuid.UUID, run *domain.SettlementRun) settlementRunEntity {
	var failureReason *string
	if run.FailureReason != "" {
		failureReason = &run.FailureReason
	}
	var attrs *string
	if run.Attributes != nil {
		b, _ := json.Marshal(run.Attributes)
		s := string(b)
		attrs = &s
	}
	return settlementRunEntity{
		ID:             dbID,
		RunID:          run.RunID,
		AccountID:      run.AccountID,
		Scope:          string(run.Scope),
		SettlementType: string(run.SettlementType),
		Status:         string(run.Status),
		PeriodStart:    run.PeriodStart,
		PeriodEnd:      run.PeriodEnd,
		InitiatedBy:    run.InitiatedBy,
		CompletedAt:    run.CompletedAt,
		VarianceCount:  run.VarianceCount,
		FailureReason:  failureReason,
		Attributes:     attrs,
		Version:        run.Version,
		CreatedAt:      run.CreatedAt,
		UpdatedAt:      run.UpdatedAt,
	}
}

func entityToRun(e *settlementRunEntity) *domain.SettlementRun {
	failureReason := ""
	if e.FailureReason != nil {
		failureReason = *e.FailureReason
	}
	var attrs map[string]string
	if e.Attributes != nil {
		_ = json.Unmarshal([]byte(*e.Attributes), &attrs)
	}
	return &domain.SettlementRun{
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
		FailureReason:  failureReason,
		Attributes:     attrs,
		CreatedAt:      e.CreatedAt,
		UpdatedAt:      e.UpdatedAt,
		Version:        e.Version,
	}
}

func snapshotToEntity(s *domain.SettlementSnapshot, dbRunID uuid.UUID) settlementSnapshotEntity {
	var attrs *string
	if s.Attributes != nil {
		b, _ := json.Marshal(s.Attributes)
		str := string(b)
		attrs = &str
	}
	return settlementSnapshotEntity{
		ID:              uuid.New(),
		SnapshotID:      s.SnapshotID,
		RunID:           dbRunID, // FK references settlement_run.id (PK)
		AccountID:       s.AccountID,
		InstrumentCode:  s.InstrumentCode,
		ExpectedBalance: s.ExpectedBalance.StringFixed(18),
		ActualBalance:   s.ActualBalance.StringFixed(18),
		VarianceAmount:  s.VarianceAmount.StringFixed(18),
		SourceSystem:    s.SourceSystem,
		Attributes:      attrs,
		CapturedAt:      s.CapturedAt,
	}
}

func entityToSnapshot(e *settlementSnapshotEntity) *domain.SettlementSnapshot {
	expected, _ := decimal.NewFromString(e.ExpectedBalance)
	actual, _ := decimal.NewFromString(e.ActualBalance)
	variance, _ := decimal.NewFromString(e.VarianceAmount)
	var attrs map[string]string
	if e.Attributes != nil {
		_ = json.Unmarshal([]byte(*e.Attributes), &attrs)
	}
	return &domain.SettlementSnapshot{
		SnapshotID:      e.SnapshotID,
		RunID:           e.RunID,
		AccountID:       e.AccountID,
		InstrumentCode:  e.InstrumentCode,
		ExpectedBalance: expected,
		ActualBalance:   actual,
		VarianceAmount:  variance,
		SourceSystem:    e.SourceSystem,
		Attributes:      attrs,
		CapturedAt:      e.CapturedAt,
		CreatedAt:       e.CreatedAt,
	}
}

// --- Test setup ---

func setupTestDB(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	db, cleanup := testdb.SetupCockroachDB(t, nil)

	err := db.Exec(`
		CREATE TABLE settlement_run (
			id UUID NOT NULL DEFAULT gen_random_uuid(),
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			run_id UUID NOT NULL,
			account_id VARCHAR(34) NOT NULL,
			scope VARCHAR(20) NOT NULL,
			settlement_type VARCHAR(20) NOT NULL,
			status VARCHAR(20) NOT NULL DEFAULT 'PENDING',
			period_start TIMESTAMPTZ NOT NULL,
			period_end TIMESTAMPTZ NOT NULL,
			initiated_by VARCHAR(100) NOT NULL,
			completed_at TIMESTAMPTZ NULL,
			variance_count INTEGER NOT NULL DEFAULT 0,
			failure_reason TEXT NULL,
			attributes JSONB NULL,
			version BIGINT NOT NULL DEFAULT 1,
			PRIMARY KEY (id)
		)
	`).Error
	require.NoError(t, err)

	err = db.Exec(`CREATE UNIQUE INDEX idx_settlement_run_run_id ON settlement_run (run_id)`).Error
	require.NoError(t, err)

	err = db.Exec(`
		CREATE TABLE settlement_snapshot (
			id UUID NOT NULL DEFAULT gen_random_uuid(),
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			snapshot_id UUID NOT NULL,
			run_id UUID NOT NULL REFERENCES settlement_run(id) ON DELETE CASCADE,
			account_id VARCHAR(34) NOT NULL,
			instrument_code VARCHAR(20) NOT NULL,
			expected_balance DECIMAL(38, 18) NOT NULL,
			actual_balance DECIMAL(38, 18) NOT NULL,
			variance_amount DECIMAL(38, 18) NOT NULL,
			source_system VARCHAR(100) NOT NULL,
			attributes JSONB NULL,
			captured_at TIMESTAMPTZ NOT NULL,
			PRIMARY KEY (id)
		)
	`).Error
	require.NoError(t, err)

	err = db.Exec(`CREATE UNIQUE INDEX idx_settlement_snapshot_snapshot_id ON settlement_snapshot (snapshot_id)`).Error
	require.NoError(t, err)

	return db, cleanup
}

// --- Integration tests ---

func TestIntegration_CaptureSnapshots_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	runRepo := &gormRunRepo{db: db}
	snapRepo := &gormSnapshotRepo{db: db, runRepo: runRepo}

	run, err := domain.NewSettlementRun(
		"ACC-001",
		domain.ReconciliationScopeAccount,
		domain.SettlementTypeDaily,
		time.Now().Add(-24*time.Hour),
		time.Now(),
		"integration-test",
	)
	require.NoError(t, err)
	require.NoError(t, runRepo.Create(context.Background(), run))

	provider := &mockPositionProvider{
		pages: []PositionPage{
			{
				Records: []PositionRecord{
					{AccountID: "ACC-001", InstrumentCode: "GBP", Balance: decimal.NewFromFloat(1000.50), SourceSystem: "pk", Attributes: map[string]string{"log_id": "log-1"}},
					{AccountID: "ACC-002", InstrumentCode: "KWH", Balance: decimal.NewFromFloat(500.25), SourceSystem: "pk", Attributes: map[string]string{"log_id": "log-2"}},
				},
				NextPageToken: "page2",
			},
			{
				Records: []PositionRecord{
					{AccountID: "ACC-003", InstrumentCode: "USD", Balance: decimal.NewFromFloat(999.99), SourceSystem: "pk", Attributes: map[string]string{"log_id": "log-3"}},
				},
				NextPageToken: "",
			},
		},
	}

	capturer := NewSnapshotCapturer(provider, runRepo, snapRepo)
	err = capturer.CaptureSnapshots(context.Background(), run.RunID)
	require.NoError(t, err)

	updatedRun, err := runRepo.FindByID(context.Background(), run.RunID)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStatusCompleted, updatedRun.Status)

	snapshots, err := snapRepo.FindByRunID(context.Background(), run.RunID)
	require.NoError(t, err)
	assert.Equal(t, 3, len(snapshots))

	for _, snap := range snapshots {
		assert.Equal(t, run.RunID, snap.RunID)
		assert.NotEqual(t, uuid.Nil, snap.SnapshotID)
		assert.NotEmpty(t, snap.AccountID)
		assert.NotEmpty(t, snap.InstrumentCode)
		assert.NotEmpty(t, snap.SourceSystem)
	}
}

func TestIntegration_CaptureSnapshots_FailureMarksRunFailed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	runRepo := &gormRunRepo{db: db}
	snapRepo := &gormSnapshotRepo{db: db, runRepo: runRepo}

	run, err := domain.NewSettlementRun(
		"ACC-001",
		domain.ReconciliationScopeAccount,
		domain.SettlementTypeDaily,
		time.Now().Add(-24*time.Hour),
		time.Now(),
		"integration-test",
	)
	require.NoError(t, err)
	require.NoError(t, runRepo.Create(context.Background(), run))

	provider := &mockPositionProvider{err: fmt.Errorf("PK service unavailable")}

	capturer := NewSnapshotCapturer(provider, runRepo, snapRepo)
	err = capturer.CaptureSnapshots(context.Background(), run.RunID)
	require.Error(t, err)

	updatedRun, err := runRepo.FindByID(context.Background(), run.RunID)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStatusFailed, updatedRun.Status)
	assert.Contains(t, updatedRun.FailureReason, "PK service unavailable")
}

func TestIntegration_CaptureSnapshots_IdempotentRetry(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	runRepo := &gormRunRepo{db: db}
	snapRepo := &gormSnapshotRepo{db: db, runRepo: runRepo}

	run, err := domain.NewSettlementRun(
		"ACC-001",
		domain.ReconciliationScopeAccount,
		domain.SettlementTypeDaily,
		time.Now().Add(-24*time.Hour),
		time.Now(),
		"integration-test",
	)
	require.NoError(t, err)
	require.NoError(t, runRepo.Create(context.Background(), run))

	// Insert a leftover snapshot from a previous failed attempt
	leftover, err := domain.NewSettlementSnapshot(
		run.RunID, "ACC-OLD", "GBP",
		decimal.NewFromFloat(999), decimal.Zero, "old-system", nil,
	)
	require.NoError(t, err)
	require.NoError(t, snapRepo.Create(context.Background(), leftover))

	// Verify leftover exists
	snaps, err := snapRepo.FindByRunID(context.Background(), run.RunID)
	require.NoError(t, err)
	assert.Equal(t, 1, len(snaps))

	provider := &mockPositionProvider{
		pages: []PositionPage{
			{
				Records: []PositionRecord{
					{AccountID: "ACC-NEW", InstrumentCode: "GBP", Balance: decimal.NewFromFloat(500), SourceSystem: "pk"},
				},
			},
		},
	}

	capturer := NewSnapshotCapturer(provider, runRepo, snapRepo)
	err = capturer.CaptureSnapshots(context.Background(), run.RunID)
	require.NoError(t, err)

	// Verify only the new snapshot exists (leftover was deleted)
	snaps, err = snapRepo.FindByRunID(context.Background(), run.RunID)
	require.NoError(t, err)
	assert.Equal(t, 1, len(snaps))
	assert.Equal(t, "ACC-NEW", snaps[0].AccountID)
}
