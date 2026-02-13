//go:build integration
// +build integration

package reconciliatione2e

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// =============================================================================
// GORM Repository Implementations for E2E Tests
// =============================================================================

// These lightweight repositories implement the domain repository interfaces
// using GORM directly. They rely on search_path being set by SetupTenantSchema.

// --- Settlement Run Repository ---

type gormRunRepository struct{ db *gorm.DB }

type settlementRunEntity struct {
	ID                 uuid.UUID  `gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	CreatedAt          time.Time  `gorm:"not null;default:now()"`
	UpdatedAt          time.Time  `gorm:"not null;default:now()"`
	RunID              uuid.UUID  `gorm:"column:run_id;uniqueIndex;type:uuid;not null"`
	AccountID          string     `gorm:"column:account_id;size:34;not null"`
	Scope              string     `gorm:"column:scope;size:20;not null"`
	SettlementType     string     `gorm:"column:settlement_type;size:20;not null"`
	Status             string     `gorm:"column:status;size:20;not null;default:PENDING"`
	PeriodStart        time.Time  `gorm:"column:period_start;not null"`
	PeriodEnd          time.Time  `gorm:"column:period_end;not null"`
	InitiatedBy        string     `gorm:"column:initiated_by;size:100;not null"`
	CompletedAt        *time.Time `gorm:"column:completed_at"`
	VarianceCount      int        `gorm:"column:variance_count;not null;default:0"`
	FailureReason      string     `gorm:"column:failure_reason"`
	LastCompletedPhase *string    `gorm:"column:last_completed_phase;size:30"`
	Version            int64      `gorm:"column:version;not null;default:1"`
}

func (settlementRunEntity) TableName() string { return "settlement_run" }

func newGormRunRepository(db *gorm.DB) *gormRunRepository {
	return &gormRunRepository{db: db}
}

func (r *gormRunRepository) Create(_ context.Context, run *domain.SettlementRun) error {
	entity := toRunEntity(run)
	return r.db.Create(entity).Error
}

func (r *gormRunRepository) FindByID(_ context.Context, runID uuid.UUID) (*domain.SettlementRun, error) {
	var entity settlementRunEntity
	result := r.db.Where("run_id = ?", runID).First(&entity)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, domain.ErrNotFound
		}
		return nil, result.Error
	}
	return toRunDomain(&entity), nil
}

func (r *gormRunRepository) Update(_ context.Context, run *domain.SettlementRun) error {
	entity := toRunEntity(run)
	result := r.db.Model(&settlementRunEntity{}).
		Where("run_id = ? AND version = ?", entity.RunID, entity.Version-1).
		Updates(map[string]interface{}{
			"status":               entity.Status,
			"completed_at":         entity.CompletedAt,
			"variance_count":       entity.VarianceCount,
			"failure_reason":       entity.FailureReason,
			"last_completed_phase": entity.LastCompletedPhase,
			"version":              entity.Version,
			"updated_at":           time.Now().UTC(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return domain.ErrOptimisticLock
	}
	return nil
}

func (r *gormRunRepository) List(_ context.Context, filter domain.RunFilter) ([]*domain.SettlementRun, error) {
	query := r.db.Model(&settlementRunEntity{})

	if filter.AccountID != nil {
		query = query.Where("account_id = ?", *filter.AccountID)
	}
	if filter.Status != nil {
		query = query.Where("status = ?", string(*filter.Status))
	}
	if filter.ToDate != nil {
		query = query.Where("created_at < ?", *filter.ToDate)
	}
	if filter.Limit > 0 {
		query = query.Limit(filter.Limit)
	}

	query = query.Order("created_at DESC")

	var entities []settlementRunEntity
	if err := query.Find(&entities).Error; err != nil {
		return nil, err
	}

	runs := make([]*domain.SettlementRun, 0, len(entities))
	for i := range entities {
		runs = append(runs, toRunDomain(&entities[i]))
	}
	return runs, nil
}

func toRunEntity(run *domain.SettlementRun) *settlementRunEntity {
	e := &settlementRunEntity{
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
		FailureReason:  run.FailureReason,
		Version:        run.Version,
		CreatedAt:      run.CreatedAt,
		UpdatedAt:      run.UpdatedAt,
	}
	if run.LastCompletedPhase != nil {
		s := string(*run.LastCompletedPhase)
		e.LastCompletedPhase = &s
	}
	return e
}

func toRunDomain(e *settlementRunEntity) *domain.SettlementRun {
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
		FailureReason:  e.FailureReason,
		Version:        e.Version,
		CreatedAt:      e.CreatedAt,
		UpdatedAt:      e.UpdatedAt,
	}
	if e.LastCompletedPhase != nil {
		phase := domain.ReconciliationPhase(*e.LastCompletedPhase)
		run.LastCompletedPhase = &phase
	}
	return run
}

// --- Settlement Snapshot Repository ---

type gormSnapshotRepository struct{ db *gorm.DB }

type settlementSnapshotEntity struct {
	ID              uuid.UUID       `gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	CreatedAt       time.Time       `gorm:"not null;default:now()"`
	SnapshotID      uuid.UUID       `gorm:"column:snapshot_id;uniqueIndex;type:uuid;not null"`
	RunID           uuid.UUID       `gorm:"column:run_id;index;type:uuid;not null"`
	AccountID       string          `gorm:"column:account_id;size:34;not null"`
	InstrumentCode  string          `gorm:"column:instrument_code;size:20;not null"`
	ExpectedBalance decimal.Decimal `gorm:"column:expected_balance;type:decimal(38,18);not null"`
	ActualBalance   decimal.Decimal `gorm:"column:actual_balance;type:decimal(38,18);not null"`
	VarianceAmount  decimal.Decimal `gorm:"column:variance_amount;type:decimal(38,18);not null"`
	SourceSystem    string          `gorm:"column:source_system;size:100;not null"`
	CapturedAt      time.Time       `gorm:"column:captured_at;not null"`
}

func (settlementSnapshotEntity) TableName() string { return "settlement_snapshot" }

func newGormSnapshotRepository(db *gorm.DB) *gormSnapshotRepository {
	return &gormSnapshotRepository{db: db}
}

func (r *gormSnapshotRepository) Create(_ context.Context, snap *domain.SettlementSnapshot) error {
	entity := toSnapEntity(snap)
	return r.db.Create(entity).Error
}

func (r *gormSnapshotRepository) CreateBatch(_ context.Context, snaps []*domain.SettlementSnapshot) error {
	entities := make([]settlementSnapshotEntity, 0, len(snaps))
	for _, s := range snaps {
		entities = append(entities, *toSnapEntity(s))
	}
	return r.db.Create(&entities).Error
}

func (r *gormSnapshotRepository) FindByID(_ context.Context, snapshotID uuid.UUID) (*domain.SettlementSnapshot, error) {
	var entity settlementSnapshotEntity
	result := r.db.Where("snapshot_id = ?", snapshotID).First(&entity)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, domain.ErrNotFound
		}
		return nil, result.Error
	}
	return toSnapDomain(&entity), nil
}

func (r *gormSnapshotRepository) FindByRunID(_ context.Context, runID uuid.UUID) ([]*domain.SettlementSnapshot, error) {
	var entities []settlementSnapshotEntity
	if err := r.db.Where("run_id = ?", runID).Find(&entities).Error; err != nil {
		return nil, err
	}
	snaps := make([]*domain.SettlementSnapshot, 0, len(entities))
	for i := range entities {
		snaps = append(snaps, toSnapDomain(&entities[i]))
	}
	return snaps, nil
}

func (r *gormSnapshotRepository) DeleteByRunID(_ context.Context, runID uuid.UUID) error {
	return r.db.Where("run_id = ?", runID).Delete(&settlementSnapshotEntity{}).Error
}

func (r *gormSnapshotRepository) MarkRunSnapshotsFinal(_ context.Context, _ uuid.UUID) error {
	// Simplified for tests - attributes aren't tracked in this entity
	return nil
}

func toSnapEntity(s *domain.SettlementSnapshot) *settlementSnapshotEntity {
	return &settlementSnapshotEntity{
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
}

func toSnapDomain(e *settlementSnapshotEntity) *domain.SettlementSnapshot {
	return &domain.SettlementSnapshot{
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
}

// --- Variance Repository ---

type gormVarianceRepository struct{ db *gorm.DB }

type varianceEntity struct {
	ID             uuid.UUID       `gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	CreatedAt      time.Time       `gorm:"not null;default:now()"`
	UpdatedAt      time.Time       `gorm:"not null;default:now()"`
	VarianceID     uuid.UUID       `gorm:"column:variance_id;uniqueIndex;type:uuid;not null"`
	RunID          uuid.UUID       `gorm:"column:run_id;index;type:uuid;not null"`
	SnapshotID     uuid.UUID       `gorm:"column:snapshot_id;type:uuid;not null"`
	AccountID      string          `gorm:"column:account_id;size:34;not null"`
	InstrumentCode string          `gorm:"column:instrument_code;size:20;not null"`
	ExpectedAmount decimal.Decimal `gorm:"column:expected_amount;type:decimal(38,18);not null"`
	ActualAmount   decimal.Decimal `gorm:"column:actual_amount;type:decimal(38,18);not null"`
	VarianceAmount decimal.Decimal `gorm:"column:variance_amount;type:decimal(38,18);not null"`
	ValueDelta     decimal.Decimal `gorm:"column:value_delta;type:decimal(38,18);not null;default:0"`
	Currency       string          `gorm:"column:currency;size:10;not null;default:''"`
	Reason         string          `gorm:"column:reason;size:30;not null"`
	Status         string          `gorm:"column:status;size:20;not null;default:DETECTED"`
	ResolutionNote *string         `gorm:"column:resolution_note;type:text"`
	ResolvedBy     *string         `gorm:"column:resolved_by;size:100"`
	ResolvedAt     *time.Time      `gorm:"column:resolved_at"`
}

func (varianceEntity) TableName() string { return "variance" }

func newGormVarianceRepository(db *gorm.DB) *gormVarianceRepository {
	return &gormVarianceRepository{db: db}
}

func (r *gormVarianceRepository) Create(_ context.Context, v *domain.Variance) error {
	entity := toVarEntity(v)
	return r.db.Create(entity).Error
}

func (r *gormVarianceRepository) CreateBatch(_ context.Context, variances []*domain.Variance) error {
	entities := make([]varianceEntity, 0, len(variances))
	for _, v := range variances {
		entities = append(entities, *toVarEntity(v))
	}
	return r.db.Create(&entities).Error
}

func (r *gormVarianceRepository) FindByID(_ context.Context, varianceID uuid.UUID) (*domain.Variance, error) {
	var entity varianceEntity
	result := r.db.Where("variance_id = ?", varianceID).First(&entity)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, domain.ErrNotFound
		}
		return nil, result.Error
	}
	return toVarDomain(&entity), nil
}

func (r *gormVarianceRepository) FindByRunID(_ context.Context, runID uuid.UUID) ([]*domain.Variance, error) {
	var entities []varianceEntity
	if err := r.db.Where("run_id = ?", runID).Find(&entities).Error; err != nil {
		return nil, err
	}
	vars := make([]*domain.Variance, 0, len(entities))
	for i := range entities {
		vars = append(vars, toVarDomain(&entities[i]))
	}
	return vars, nil
}

func (r *gormVarianceRepository) Update(_ context.Context, v *domain.Variance) error {
	entity := toVarEntity(v)
	result := r.db.Model(&varianceEntity{}).
		Where("variance_id = ?", entity.VarianceID).
		Updates(map[string]interface{}{
			"status":          entity.Status,
			"value_delta":     entity.ValueDelta,
			"currency":        entity.Currency,
			"resolution_note": entity.ResolutionNote,
			"resolved_by":     entity.ResolvedBy,
			"resolved_at":     entity.ResolvedAt,
			"updated_at":      time.Now().UTC(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *gormVarianceRepository) DeleteByRunID(_ context.Context, runID uuid.UUID) error {
	return r.db.Where("run_id = ?", runID).Delete(&varianceEntity{}).Error
}

func (r *gormVarianceRepository) List(_ context.Context, filter domain.VarianceFilter) ([]*domain.Variance, error) {
	query := r.db.Model(&varianceEntity{})

	if filter.RunID != nil {
		query = query.Where("run_id = ?", *filter.RunID)
	}
	if filter.Status != nil {
		query = query.Where("status = ?", string(*filter.Status))
	}
	if filter.Limit > 0 {
		query = query.Limit(filter.Limit)
	}

	var entities []varianceEntity
	if err := query.Order("created_at DESC").Find(&entities).Error; err != nil {
		return nil, err
	}
	vars := make([]*domain.Variance, 0, len(entities))
	for i := range entities {
		vars = append(vars, toVarDomain(&entities[i]))
	}
	return vars, nil
}

func toVarEntity(v *domain.Variance) *varianceEntity {
	e := &varianceEntity{
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
		e.ResolutionNote = &v.ResolutionNote
	}
	if v.ResolvedBy != "" {
		e.ResolvedBy = &v.ResolvedBy
	}
	return e
}

func toVarDomain(e *varianceEntity) *domain.Variance {
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
	return v
}

// --- Balance Assertion Repository ---

type gormAssertionRepository struct{ db *gorm.DB }

type balanceAssertionEntity struct {
	ID              uuid.UUID       `gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	CreatedAt       time.Time       `gorm:"not null;default:now()"`
	AssertionID     uuid.UUID       `gorm:"column:assertion_id;uniqueIndex;type:uuid;not null"`
	RunID           *uuid.UUID      `gorm:"column:run_id;type:uuid"`
	AccountID       string          `gorm:"column:account_id;size:34;not null"`
	InstrumentCode  string          `gorm:"column:instrument_code;size:20;not null"`
	Expression      string          `gorm:"column:expression;type:text;not null"`
	ExpectedBalance decimal.Decimal `gorm:"column:expected_balance;type:decimal(38,18);not null"`
	ActualBalance   decimal.Decimal `gorm:"column:actual_balance;type:decimal(38,18);not null;default:0"`
	Status          string          `gorm:"column:status;size:20;not null;default:PENDING"`
	FailureReason   string          `gorm:"column:failure_reason;type:text"`
	OverrideReason  string          `gorm:"column:override_reason;type:text"`
	AssertedAt      *time.Time      `gorm:"column:asserted_at"`
}

func (balanceAssertionEntity) TableName() string { return "balance_assertion" }

func newGormAssertionRepository(db *gorm.DB) *gormAssertionRepository {
	return &gormAssertionRepository{db: db}
}

func (r *gormAssertionRepository) Create(_ context.Context, a *domain.BalanceAssertion) error {
	entity := toAssertionEntity(a)
	return r.db.Create(entity).Error
}

func (r *gormAssertionRepository) FindByID(_ context.Context, assertionID uuid.UUID) (*domain.BalanceAssertion, error) {
	var entity balanceAssertionEntity
	result := r.db.Where("assertion_id = ?", assertionID).First(&entity)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, domain.ErrNotFound
		}
		return nil, result.Error
	}
	return toAssertionDomain(&entity), nil
}

func (r *gormAssertionRepository) FindByRunID(_ context.Context, runID uuid.UUID) ([]*domain.BalanceAssertion, error) {
	var entities []balanceAssertionEntity
	if err := r.db.Where("run_id = ?", runID).Find(&entities).Error; err != nil {
		return nil, err
	}
	assertions := make([]*domain.BalanceAssertion, 0, len(entities))
	for i := range entities {
		assertions = append(assertions, toAssertionDomain(&entities[i]))
	}
	return assertions, nil
}

func (r *gormAssertionRepository) Update(_ context.Context, a *domain.BalanceAssertion) error {
	entity := toAssertionEntity(a)
	result := r.db.Model(&balanceAssertionEntity{}).
		Where("assertion_id = ?", entity.AssertionID).
		Updates(map[string]interface{}{
			"actual_balance":  entity.ActualBalance,
			"status":          entity.Status,
			"failure_reason":  entity.FailureReason,
			"override_reason": entity.OverrideReason,
			"asserted_at":     entity.AssertedAt,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *gormAssertionRepository) List(_ context.Context, filter domain.AssertionFilter) ([]*domain.BalanceAssertion, error) {
	query := r.db.Model(&balanceAssertionEntity{})

	if filter.RunID != nil {
		query = query.Where("run_id = ?", *filter.RunID)
	}
	if filter.Status != nil {
		query = query.Where("status = ?", string(*filter.Status))
	}
	if filter.Limit > 0 {
		query = query.Limit(filter.Limit)
	}

	var entities []balanceAssertionEntity
	if err := query.Find(&entities).Error; err != nil {
		return nil, err
	}
	assertions := make([]*domain.BalanceAssertion, 0, len(entities))
	for i := range entities {
		assertions = append(assertions, toAssertionDomain(&entities[i]))
	}
	return assertions, nil
}

func toAssertionEntity(a *domain.BalanceAssertion) *balanceAssertionEntity {
	e := &balanceAssertionEntity{
		AssertionID:     a.AssertionID,
		RunID:           a.RunID,
		AccountID:       a.AccountID,
		InstrumentCode:  a.InstrumentCode,
		Expression:      a.Expression,
		ExpectedBalance: a.ExpectedBalance,
		ActualBalance:   a.ActualBalance,
		Status:          string(a.Status),
		FailureReason:   a.FailureReason,
		OverrideReason:  a.OverrideReason,
		CreatedAt:       a.CreatedAt,
	}
	if !a.AssertedAt.IsZero() {
		t := a.AssertedAt
		e.AssertedAt = &t
	}
	return e
}

func toAssertionDomain(e *balanceAssertionEntity) *domain.BalanceAssertion {
	a := &domain.BalanceAssertion{
		AssertionID:     e.AssertionID,
		RunID:           e.RunID,
		AccountID:       e.AccountID,
		InstrumentCode:  e.InstrumentCode,
		Expression:      e.Expression,
		ExpectedBalance: e.ExpectedBalance,
		ActualBalance:   e.ActualBalance,
		Status:          domain.AssertionStatus(e.Status),
		FailureReason:   e.FailureReason,
		OverrideReason:  e.OverrideReason,
		CreatedAt:       e.CreatedAt,
	}
	if e.AssertedAt != nil {
		a.AssertedAt = *e.AssertedAt
	}
	return a
}

// --- Imbalance Trend Repository ---

type gormTrendRepository struct{ db *gorm.DB }

type imbalanceTrendEntity struct {
	ID                  uuid.UUID       `gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	TrendID             uuid.UUID       `gorm:"column:trend_id;uniqueIndex;type:uuid;not null"`
	InstrumentCode      string          `gorm:"column:instrument_code;size:20;not null"`
	ConsecutiveDays     int             `gorm:"column:consecutive_days;not null;default:0"`
	LastImbalanceAmount decimal.Decimal `gorm:"column:last_imbalance_amount;type:decimal(38,18);not null;default:0"`
	LastAssertionID     *uuid.UUID      `gorm:"column:last_assertion_id;type:uuid"`
	FirstDetectedAt     time.Time       `gorm:"column:first_detected_at;not null"`
	LastDetectedAt      time.Time       `gorm:"column:last_detected_at;not null"`
	ResolvedAt          *time.Time      `gorm:"column:resolved_at"`
}

func (imbalanceTrendEntity) TableName() string { return "imbalance_trend" }

func newGormTrendRepository(db *gorm.DB) *gormTrendRepository {
	return &gormTrendRepository{db: db}
}

func (r *gormTrendRepository) Upsert(_ context.Context, trend *domain.ImbalanceTrend) error {
	entity := toTrendEntity(trend)

	// Try to find existing
	var existing imbalanceTrendEntity
	result := r.db.Where("trend_id = ?", entity.TrendID).First(&existing)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return r.db.Create(entity).Error
		}
		return result.Error
	}

	return r.db.Model(&imbalanceTrendEntity{}).
		Where("trend_id = ?", entity.TrendID).
		Updates(map[string]interface{}{
			"consecutive_days":      entity.ConsecutiveDays,
			"last_imbalance_amount": entity.LastImbalanceAmount,
			"last_assertion_id":     entity.LastAssertionID,
			"last_detected_at":      entity.LastDetectedAt,
			"resolved_at":           entity.ResolvedAt,
		}).Error
}

func (r *gormTrendRepository) FindByInstrumentCode(_ context.Context, instrumentCode string) (*domain.ImbalanceTrend, error) {
	var entity imbalanceTrendEntity
	result := r.db.Where("instrument_code = ? AND resolved_at IS NULL", instrumentCode).First(&entity)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, domain.ErrNotFound
		}
		return nil, result.Error
	}
	return toTrendDomain(&entity), nil
}

func toTrendEntity(t *domain.ImbalanceTrend) *imbalanceTrendEntity {
	e := &imbalanceTrendEntity{
		TrendID:             t.TrendID,
		InstrumentCode:      t.InstrumentCode,
		ConsecutiveDays:     t.ConsecutiveDays,
		LastImbalanceAmount: t.LastImbalanceAmount,
		FirstDetectedAt:     t.FirstDetectedAt,
		LastDetectedAt:      t.LastDetectedAt,
		ResolvedAt:          t.ResolvedAt,
	}
	if t.LastAssertionID != uuid.Nil {
		e.LastAssertionID = &t.LastAssertionID
	}
	return e
}

func toTrendDomain(e *imbalanceTrendEntity) *domain.ImbalanceTrend {
	t := &domain.ImbalanceTrend{
		TrendID:             e.TrendID,
		InstrumentCode:      e.InstrumentCode,
		ConsecutiveDays:     e.ConsecutiveDays,
		LastImbalanceAmount: e.LastImbalanceAmount,
		FirstDetectedAt:     e.FirstDetectedAt,
		LastDetectedAt:      e.LastDetectedAt,
		ResolvedAt:          e.ResolvedAt,
	}
	if e.LastAssertionID != nil {
		t.LastAssertionID = *e.LastAssertionID
	}
	return t
}
