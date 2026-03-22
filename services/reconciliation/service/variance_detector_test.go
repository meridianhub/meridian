package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/meridianhub/meridian/services/reconciliation/testhelpers"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Variance-specific mock repos (distinct from snapshot_capturer mocks) ---

type vdRunRepo struct {
	runs      map[uuid.UUID]*domain.SettlementRun
	listRuns  []*domain.SettlementRun
	findErr   error
	listErr   error
}

func newVdRunRepo() *vdRunRepo { return &vdRunRepo{runs: make(map[uuid.UUID]*domain.SettlementRun)} }

func (m *vdRunRepo) Create(_ context.Context, run *domain.SettlementRun) error {
	m.runs[run.RunID] = run
	return nil
}
func (m *vdRunRepo) FindByID(_ context.Context, runID uuid.UUID) (*domain.SettlementRun, error) {
	if m.findErr != nil {
		return nil, m.findErr
	}
	run, ok := m.runs[runID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return run, nil
}
func (m *vdRunRepo) Update(_ context.Context, run *domain.SettlementRun) error {
	m.runs[run.RunID] = run
	return nil
}
func (m *vdRunRepo) List(_ context.Context, _ domain.RunFilter) ([]*domain.SettlementRun, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.listRuns, nil
}

type vdSnapRepo struct {
	snapsByRunID map[uuid.UUID][]*domain.SettlementSnapshot
	findErr      error
}

func newVdSnapRepo() *vdSnapRepo {
	return &vdSnapRepo{snapsByRunID: make(map[uuid.UUID][]*domain.SettlementSnapshot)}
}

func (m *vdSnapRepo) Create(_ context.Context, _ *domain.SettlementSnapshot) error { return nil }
func (m *vdSnapRepo) CreateBatch(_ context.Context, _ []*domain.SettlementSnapshot) error {
	return nil
}
func (m *vdSnapRepo) FindByID(_ context.Context, _ uuid.UUID) (*domain.SettlementSnapshot, error) {
	return nil, domain.ErrNotFound
}
func (m *vdSnapRepo) FindByRunID(_ context.Context, runID uuid.UUID) ([]*domain.SettlementSnapshot, error) {
	if m.findErr != nil {
		return nil, m.findErr
	}
	return m.snapsByRunID[runID], nil
}
func (m *vdSnapRepo) DeleteByRunID(_ context.Context, _ uuid.UUID) error { return nil }
func (m *vdSnapRepo) MarkRunSnapshotsFinal(_ context.Context, _ uuid.UUID) error { return nil }

type vdVarianceRepo struct {
	created        []*domain.Variance
	deleteErr      error
	createBatchErr error
}

func (m *vdVarianceRepo) Create(_ context.Context, _ *domain.Variance) error   { return nil }
func (m *vdVarianceRepo) FindByID(_ context.Context, _ uuid.UUID) (*domain.Variance, error) {
	return nil, domain.ErrNotFound
}
func (m *vdVarianceRepo) FindByRunID(_ context.Context, _ uuid.UUID) ([]*domain.Variance, error) {
	return nil, nil
}
func (m *vdVarianceRepo) Update(_ context.Context, _ *domain.Variance) error   { return nil }
func (m *vdVarianceRepo) List(_ context.Context, _ domain.VarianceFilter) ([]*domain.Variance, error) {
	return nil, nil
}
func (m *vdVarianceRepo) DeleteByRunID(_ context.Context, _ uuid.UUID) error {
	return m.deleteErr
}
func (m *vdVarianceRepo) CreateBatch(_ context.Context, variances []*domain.Variance) error {
	if m.createBatchErr != nil {
		return m.createBatchErr
	}
	m.created = append(m.created, variances...)
	return nil
}

// --- Tests ---

func TestDetectVariances_RunNotFound(t *testing.T) {
	runRepo := newVdRunRepo()
	runRepo.findErr = errors.New("not found")
	vd := NewVarianceDetector(runRepo, newVdSnapRepo(), &vdVarianceRepo{})

	_, err := vd.DetectVariances(context.Background(), uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to find settlement run")
}

func TestDetectVariances_RunNotRunning(t *testing.T) {
	run := testhelpers.NewSettlementRun(t)
	require.NoError(t, run.Start())
	require.NoError(t, run.Complete(0))

	runRepo := newVdRunRepo()
	runRepo.runs[run.RunID] = run
	vd := NewVarianceDetector(runRepo, newVdSnapRepo(), &vdVarianceRepo{})

	_, err := vd.DetectVariances(context.Background(), run.RunID)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrRunNotRunning)
}

func TestDetectVariances_DeleteByRunIDFails(t *testing.T) {
	run := testhelpers.NewSettlementRun(t)
	require.NoError(t, run.Start())

	runRepo := newVdRunRepo()
	runRepo.runs[run.RunID] = run
	vd := NewVarianceDetector(runRepo, newVdSnapRepo(), &vdVarianceRepo{deleteErr: errors.New("delete failed")})

	_, err := vd.DetectVariances(context.Background(), run.RunID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to clean up existing variances")
}

func TestDetectVariances_NoSnapshots(t *testing.T) {
	run := testhelpers.NewSettlementRun(t)
	require.NoError(t, run.Start())

	runRepo := newVdRunRepo()
	runRepo.runs[run.RunID] = run
	vd := NewVarianceDetector(runRepo, newVdSnapRepo(), &vdVarianceRepo{})

	variances, err := vd.DetectVariances(context.Background(), run.RunID)
	require.NoError(t, err)
	assert.Nil(t, variances)
}

func TestDetectVariances_SnapshotFetchFails(t *testing.T) {
	run := testhelpers.NewSettlementRun(t)
	require.NoError(t, run.Start())

	runRepo := newVdRunRepo()
	runRepo.runs[run.RunID] = run
	snapRepo := newVdSnapRepo()
	snapRepo.findErr = errors.New("db error")
	vd := NewVarianceDetector(runRepo, snapRepo, &vdVarianceRepo{})

	_, err := vd.DetectVariances(context.Background(), run.RunID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to fetch snapshots")
}

func TestDetectVariances_D1Run_WithVariance(t *testing.T) {
	run := testhelpers.NewSettlementRun(t)
	require.NoError(t, run.Start())

	runRepo := newVdRunRepo()
	runRepo.runs[run.RunID] = run

	snapRepo := newVdSnapRepo()
	snapRepo.snapsByRunID[run.RunID] = []*domain.SettlementSnapshot{
		{
			SnapshotID:      uuid.New(),
			RunID:           run.RunID,
			AccountID:       run.AccountID,
			InstrumentCode:  "GBP",
			ExpectedBalance: decimal.NewFromInt(100),
			ActualBalance:   decimal.NewFromInt(90),
			VarianceAmount:  decimal.NewFromInt(-10),
			SourceSystem:    "ledger",
		},
	}

	varianceRepo := &vdVarianceRepo{}
	vd := NewVarianceDetector(runRepo, snapRepo, varianceRepo)

	variances, err := vd.DetectVariances(context.Background(), run.RunID)
	require.NoError(t, err)
	require.Len(t, variances, 1)
	assert.Equal(t, domain.VarianceReasonAmountMismatch, variances[0].Reason)
	assert.Equal(t, decimal.NewFromInt(100), variances[0].ExpectedAmount)
	assert.Equal(t, decimal.NewFromInt(90), variances[0].ActualAmount)
}

func TestDetectVariances_D1Run_NoVariance(t *testing.T) {
	run := testhelpers.NewSettlementRun(t)
	require.NoError(t, run.Start())

	runRepo := newVdRunRepo()
	runRepo.runs[run.RunID] = run

	snapRepo := newVdSnapRepo()
	snapRepo.snapsByRunID[run.RunID] = []*domain.SettlementSnapshot{
		{
			SnapshotID:      uuid.New(),
			AccountID:       run.AccountID,
			InstrumentCode:  "GBP",
			ExpectedBalance: decimal.NewFromInt(100),
			ActualBalance:   decimal.NewFromInt(100),
			VarianceAmount:  decimal.Zero,
			SourceSystem:    "ledger",
		},
	}

	vd := NewVarianceDetector(runRepo, snapRepo, &vdVarianceRepo{})
	variances, err := vd.DetectVariances(context.Background(), run.RunID)
	require.NoError(t, err)
	assert.Empty(t, variances)
}

func TestDetectVariances_SubsequentRun_DeltaDetected(t *testing.T) {
	// Create previous completed run
	prevRun := testhelpers.NewSettlementRun(t)
	require.NoError(t, prevRun.Start())
	require.NoError(t, prevRun.Complete(0))
	prevRun.CreatedAt = time.Now().Add(-24 * time.Hour)

	// Create current running run
	run := testhelpers.NewSettlementRunForAccount(t, prevRun.AccountID)
	require.NoError(t, run.Start())

	runRepo := newVdRunRepo()
	runRepo.runs[run.RunID] = run
	runRepo.runs[prevRun.RunID] = prevRun
	runRepo.listRuns = []*domain.SettlementRun{prevRun}

	snapRepo := newVdSnapRepo()
	snapRepo.snapsByRunID[run.RunID] = []*domain.SettlementSnapshot{
		{SnapshotID: uuid.New(), AccountID: run.AccountID, InstrumentCode: "GBP", ActualBalance: decimal.NewFromInt(200), SourceSystem: "ledger"},
	}
	snapRepo.snapsByRunID[prevRun.RunID] = []*domain.SettlementSnapshot{
		{SnapshotID: uuid.New(), AccountID: run.AccountID, InstrumentCode: "GBP", ActualBalance: decimal.NewFromInt(150), SourceSystem: "ledger"},
	}

	varianceRepo := &vdVarianceRepo{}
	vd := NewVarianceDetector(runRepo, snapRepo, varianceRepo)

	variances, err := vd.DetectVariances(context.Background(), run.RunID)
	require.NoError(t, err)
	require.Len(t, variances, 1)
	assert.Equal(t, decimal.NewFromInt(150), variances[0].ExpectedAmount)
	assert.Equal(t, decimal.NewFromInt(200), variances[0].ActualAmount)
}

func TestDetectVariances_SubsequentRun_MissingEntryInCurrent(t *testing.T) {
	prevRun := testhelpers.NewSettlementRun(t)
	require.NoError(t, prevRun.Start())
	require.NoError(t, prevRun.Complete(0))
	prevRun.CreatedAt = time.Now().Add(-24 * time.Hour)

	run := testhelpers.NewSettlementRunForAccount(t, prevRun.AccountID)
	require.NoError(t, run.Start())

	runRepo := newVdRunRepo()
	runRepo.runs[run.RunID] = run
	runRepo.listRuns = []*domain.SettlementRun{prevRun}

	snapRepo := newVdSnapRepo()
	// Current run has EUR but not GBP - the GBP from previous should be detected as missing
	snapRepo.snapsByRunID[run.RunID] = []*domain.SettlementSnapshot{
		{SnapshotID: uuid.New(), AccountID: run.AccountID, InstrumentCode: "EUR", ActualBalance: decimal.NewFromInt(100), SourceSystem: "ledger"},
	}
	snapRepo.snapsByRunID[prevRun.RunID] = []*domain.SettlementSnapshot{
		{SnapshotID: uuid.New(), AccountID: run.AccountID, InstrumentCode: "GBP", ActualBalance: decimal.NewFromInt(100), SourceSystem: "ledger"},
	}

	vd := NewVarianceDetector(runRepo, snapRepo, &vdVarianceRepo{})
	variances, err := vd.DetectVariances(context.Background(), run.RunID)
	require.NoError(t, err)
	// Two variances: EUR is new (missing in previous), GBP is missing (was in previous)
	require.Len(t, variances, 2)
	for _, v := range variances {
		assert.Equal(t, domain.VarianceReasonMissingEntry, v.Reason)
	}
}

func TestDetectVariances_SubsequentRun_NewEntryInCurrent(t *testing.T) {
	prevRun := testhelpers.NewSettlementRun(t)
	require.NoError(t, prevRun.Start())
	require.NoError(t, prevRun.Complete(0))
	prevRun.CreatedAt = time.Now().Add(-24 * time.Hour)

	run := testhelpers.NewSettlementRunForAccount(t, prevRun.AccountID)
	require.NoError(t, run.Start())

	runRepo := newVdRunRepo()
	runRepo.runs[run.RunID] = run
	runRepo.listRuns = []*domain.SettlementRun{prevRun}

	snapRepo := newVdSnapRepo()
	snapRepo.snapsByRunID[run.RunID] = []*domain.SettlementSnapshot{
		{SnapshotID: uuid.New(), AccountID: run.AccountID, InstrumentCode: "EUR", ActualBalance: decimal.NewFromInt(50), SourceSystem: "ledger"},
	}
	snapRepo.snapsByRunID[prevRun.RunID] = []*domain.SettlementSnapshot{
		{SnapshotID: uuid.New(), AccountID: run.AccountID, InstrumentCode: "GBP", ActualBalance: decimal.NewFromInt(100), SourceSystem: "ledger"},
	}

	vd := NewVarianceDetector(runRepo, snapRepo, &vdVarianceRepo{})
	variances, err := vd.DetectVariances(context.Background(), run.RunID)
	require.NoError(t, err)
	require.Len(t, variances, 2)
	// One variance for the new EUR entry, one for the missing GBP entry
	reasons := map[domain.VarianceReason]bool{}
	for _, v := range variances {
		reasons[v.Reason] = true
		assert.Equal(t, domain.VarianceReasonMissingEntry, v.Reason)
	}
}

func TestDetectVariances_CreateBatchFails(t *testing.T) {
	run := testhelpers.NewSettlementRun(t)
	require.NoError(t, run.Start())

	runRepo := newVdRunRepo()
	runRepo.runs[run.RunID] = run

	snapRepo := newVdSnapRepo()
	snapRepo.snapsByRunID[run.RunID] = []*domain.SettlementSnapshot{
		{
			SnapshotID:      uuid.New(),
			AccountID:       run.AccountID,
			InstrumentCode:  "GBP",
			ExpectedBalance: decimal.NewFromInt(100),
			ActualBalance:   decimal.NewFromInt(80),
			VarianceAmount:  decimal.NewFromInt(-20),
			SourceSystem:    "ledger",
		},
	}

	vd := NewVarianceDetector(runRepo, snapRepo, &vdVarianceRepo{createBatchErr: errors.New("batch insert failed")})
	_, err := vd.DetectVariances(context.Background(), run.RunID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to persist variances")
}

func TestDetectVariances_FindPreviousSnapshotsFails(t *testing.T) {
	run := testhelpers.NewSettlementRun(t)
	require.NoError(t, run.Start())

	runRepo := newVdRunRepo()
	runRepo.runs[run.RunID] = run
	runRepo.listErr = errors.New("list failed")

	snapRepo := newVdSnapRepo()
	snapRepo.snapsByRunID[run.RunID] = []*domain.SettlementSnapshot{
		{SnapshotID: uuid.New(), AccountID: run.AccountID, InstrumentCode: "GBP", SourceSystem: "ledger"},
	}

	vd := NewVarianceDetector(runRepo, snapRepo, &vdVarianceRepo{})
	_, err := vd.DetectVariances(context.Background(), run.RunID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to find previous snapshots")
}

func TestClassifyVarianceReason_NoPrevious(t *testing.T) {
	snap := &domain.SettlementSnapshot{AccountID: "ACC-1"}
	reason := classifyVarianceReason(snap, nil)
	assert.Equal(t, domain.VarianceReasonAmountMismatch, reason)
}

func TestClassifyVarianceReason_SourceSystemMismatch(t *testing.T) {
	current := &domain.SettlementSnapshot{SourceSystem: "systemA", Attributes: map[string]string{}}
	previous := &domain.SettlementSnapshot{SourceSystem: "systemB", Attributes: map[string]string{}}
	reason := classifyVarianceReason(current, previous)
	assert.Equal(t, domain.VarianceReasonExternalMismatch, reason)
}

func TestClassifyVarianceReason_QualityUpgrade(t *testing.T) {
	current := &domain.SettlementSnapshot{
		SourceSystem: "system",
		Attributes:   map[string]string{"quality": "ACTUAL"},
	}
	previous := &domain.SettlementSnapshot{
		SourceSystem: "system",
		Attributes:   map[string]string{"quality": "ESTIMATE"},
	}
	reason := classifyVarianceReason(current, previous)
	assert.Equal(t, domain.VarianceReasonQualityUpgrade, reason)
}

func TestClassifyVarianceReason_CorrectionApplied(t *testing.T) {
	current := &domain.SettlementSnapshot{
		SourceSystem: "system",
		Attributes:   map[string]string{"correction": "wash_and_reload"},
	}
	previous := &domain.SettlementSnapshot{
		SourceSystem: "system",
		Attributes:   map[string]string{},
	}
	reason := classifyVarianceReason(current, previous)
	assert.Equal(t, domain.VarianceReasonCorrectionApplied, reason)
}

func TestClassifyVarianceReason_DefaultAmountMismatch(t *testing.T) {
	current := &domain.SettlementSnapshot{
		SourceSystem: "system",
		Attributes:   map[string]string{},
	}
	previous := &domain.SettlementSnapshot{
		SourceSystem: "system",
		Attributes:   map[string]string{},
	}
	reason := classifyVarianceReason(current, previous)
	assert.Equal(t, domain.VarianceReasonAmountMismatch, reason)
}

func TestIsQualityUpgrade(t *testing.T) {
	tests := []struct {
		previous string
		current  string
		expected bool
	}{
		{"ESTIMATE", "ACTUAL", true},
		{"ESTIMATE", "COEFFICIENT", true},
		{"ACTUAL", "REVISED", true},
		{"ACTUAL", "ESTIMATE", false},
		{"REVISED", "ACTUAL", false},
		{"ACTUAL", "ACTUAL", false},
		{"UNKNOWN", "ACTUAL", true},
		{"ACTUAL", "UNKNOWN", false},
	}

	for _, tt := range tests {
		t.Run(tt.previous+"_to_"+tt.current, func(t *testing.T) {
			assert.Equal(t, tt.expected, isQualityUpgrade(tt.previous, tt.current))
		})
	}
}

func TestSnapshotKey(t *testing.T) {
	key := snapshotKey("ACC-1", "GBP", "ledger")
	assert.Equal(t, "ACC-1|GBP|ledger", key)
}

func TestDetectVariances_SubsequentRun_NoDelta(t *testing.T) {
	prevRun := testhelpers.NewSettlementRun(t)
	require.NoError(t, prevRun.Start())
	require.NoError(t, prevRun.Complete(0))
	prevRun.CreatedAt = time.Now().Add(-24 * time.Hour)

	run := testhelpers.NewSettlementRunForAccount(t, prevRun.AccountID)
	require.NoError(t, run.Start())

	runRepo := newVdRunRepo()
	runRepo.runs[run.RunID] = run
	runRepo.listRuns = []*domain.SettlementRun{prevRun}

	snapRepo := newVdSnapRepo()
	snap := []*domain.SettlementSnapshot{
		{SnapshotID: uuid.New(), AccountID: run.AccountID, InstrumentCode: "GBP", ActualBalance: decimal.NewFromInt(100), SourceSystem: "ledger"},
	}
	snapRepo.snapsByRunID[run.RunID] = snap
	snapRepo.snapsByRunID[prevRun.RunID] = []*domain.SettlementSnapshot{
		{SnapshotID: uuid.New(), AccountID: run.AccountID, InstrumentCode: "GBP", ActualBalance: decimal.NewFromInt(100), SourceSystem: "ledger"},
	}

	vd := NewVarianceDetector(runRepo, snapRepo, &vdVarianceRepo{})
	variances, err := vd.DetectVariances(context.Background(), run.RunID)
	require.NoError(t, err)
	assert.Empty(t, variances)
}
