package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/meridianhub/meridian/services/reconciliation/testhelpers"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mock VarianceRepository ---

type mockVarianceRepoFull struct {
	mu        sync.Mutex
	variances []*domain.Variance
	deleted   []uuid.UUID

	createBatchErr error
	deleteErr      error
}

func (m *mockVarianceRepoFull) Create(_ context.Context, v *domain.Variance) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.variances = append(m.variances, v)
	return nil
}

func (m *mockVarianceRepoFull) CreateBatch(_ context.Context, vs []*domain.Variance) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.createBatchErr != nil {
		return m.createBatchErr
	}
	m.variances = append(m.variances, vs...)
	return nil
}

func (m *mockVarianceRepoFull) FindByID(_ context.Context, id uuid.UUID) (*domain.Variance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, v := range m.variances {
		if v.VarianceID == id {
			return v, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (m *mockVarianceRepoFull) FindByRunID(_ context.Context, runID uuid.UUID) ([]*domain.Variance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*domain.Variance
	for _, v := range m.variances {
		if v.RunID == runID {
			result = append(result, v)
		}
	}
	return result, nil
}

func (m *mockVarianceRepoFull) Update(_ context.Context, v *domain.Variance) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, existing := range m.variances {
		if existing.VarianceID == v.VarianceID {
			m.variances[i] = v
			return nil
		}
	}
	return domain.ErrNotFound
}

func (m *mockVarianceRepoFull) DeleteByRunID(_ context.Context, runID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.deleted = append(m.deleted, runID)
	filtered := make([]*domain.Variance, 0)
	for _, v := range m.variances {
		if v.RunID != runID {
			filtered = append(filtered, v)
		}
	}
	m.variances = filtered
	return nil
}

func (m *mockVarianceRepoFull) List(_ context.Context, _ domain.VarianceFilter) ([]*domain.Variance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.variances, nil
}

func (m *mockVarianceRepoFull) varianceCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.variances)
}

// --- Helper to create a RUNNING test run ---

func newRunningTestRun(t *testing.T) *domain.SettlementRun {
	t.Helper()
	return testhelpers.NewRunningSettlementRun(t)
}

// --- Tests ---

func TestDetectVariances_D1Run_WithVariances(t *testing.T) {
	runRepo := newMockRunRepo()
	snapRepo := &mockSnapshotRepo{}
	varianceRepo := &mockVarianceRepoFull{}

	run := newRunningTestRun(t)
	_ = runRepo.Create(context.Background(), run)

	// Create snapshots with non-zero variance (expected != actual)
	snap1, _ := domain.NewSettlementSnapshot(
		run.RunID, "ACC-001", "GBP",
		decimal.NewFromFloat(1000.00),
		decimal.NewFromFloat(995.50),
		"pk", nil,
	)
	snap2, _ := domain.NewSettlementSnapshot(
		run.RunID, "ACC-002", "KWH",
		decimal.NewFromFloat(500.00),
		decimal.NewFromFloat(500.00), // no variance
		"pk", nil,
	)
	snap3, _ := domain.NewSettlementSnapshot(
		run.RunID, "ACC-003", "USD",
		decimal.NewFromFloat(200.00),
		decimal.NewFromFloat(250.00),
		"pk", nil,
	)

	// Store snapshots in mock
	snapRepo.snapshots = []*domain.SettlementSnapshot{snap1, snap2, snap3}

	detector := NewVarianceDetector(runRepo, snapRepo, varianceRepo)
	variances, err := detector.DetectVariances(context.Background(), run.RunID)
	require.NoError(t, err)

	// Should detect 2 variances (snap2 has zero variance)
	assert.Len(t, variances, 2)
	assert.Equal(t, 2, varianceRepo.varianceCount())

	// Verify variance amounts
	for _, v := range variances {
		assert.Equal(t, domain.VarianceStatusDetected, v.Status)
		assert.Equal(t, run.RunID, v.RunID)
	}
}

func TestDetectVariances_D1Run_NoVariances(t *testing.T) {
	runRepo := newMockRunRepo()
	snapRepo := &mockSnapshotRepo{}
	varianceRepo := &mockVarianceRepoFull{}

	run := newRunningTestRun(t)
	_ = runRepo.Create(context.Background(), run)

	// Create snapshots with zero variance
	snap, _ := domain.NewSettlementSnapshot(
		run.RunID, "ACC-001", "GBP",
		decimal.NewFromFloat(1000.00),
		decimal.NewFromFloat(1000.00),
		"pk", nil,
	)
	snapRepo.snapshots = []*domain.SettlementSnapshot{snap}

	detector := NewVarianceDetector(runRepo, snapRepo, varianceRepo)
	variances, err := detector.DetectVariances(context.Background(), run.RunID)
	require.NoError(t, err)

	assert.Empty(t, variances)
	assert.Equal(t, 0, varianceRepo.varianceCount())
}

func TestDetectVariances_SubsequentRun_DetectsDelta(t *testing.T) {
	runRepo := newMockRunRepo()
	snapRepo := &mockSnapshotRepo{}
	varianceRepo := &mockVarianceRepoFull{}

	// Create a previous completed run with an earlier CreatedAt
	prevRun, _ := domain.NewSettlementRun(
		"ACC-001",
		domain.ReconciliationScopeAccount,
		domain.SettlementTypeDaily,
		time.Now().Add(-48*time.Hour),
		time.Now().Add(-24*time.Hour),
		"test-user",
	)
	require.NoError(t, prevRun.Start())
	require.NoError(t, prevRun.Complete(0))
	_ = runRepo.Create(context.Background(), prevRun)

	// Previous run's snapshots (settled amounts)
	prevSnap, _ := domain.NewSettlementSnapshot(
		prevRun.RunID, "ACC-001", "GBP",
		decimal.NewFromFloat(1000.00),
		decimal.NewFromFloat(1000.00),
		"pk", nil,
	)

	// Current run
	currRun := newRunningTestRun(t)
	_ = runRepo.Create(context.Background(), currRun)

	// Current run's snapshots (different actual balance from previous)
	currSnap, _ := domain.NewSettlementSnapshot(
		currRun.RunID, "ACC-001", "GBP",
		decimal.NewFromFloat(1000.00),
		decimal.NewFromFloat(1050.00), // delta of 50 vs previous run's actual
		"pk", nil,
	)

	// Store both snapshots - FindByRunID filters by RunID
	snapRepo.snapshots = []*domain.SettlementSnapshot{currSnap, prevSnap}

	detector := NewVarianceDetector(runRepo, snapRepo, varianceRepo)
	variances, err := detector.DetectVariances(context.Background(), currRun.RunID)
	require.NoError(t, err)

	// Should detect exactly 1 variance from the cross-run delta
	require.Len(t, variances, 1)

	v := variances[0]
	assert.Equal(t, currRun.RunID, v.RunID)
	assert.Equal(t, "ACC-001", v.AccountID)
	assert.Equal(t, "GBP", v.InstrumentCode)
	// Cross-run comparison: expected = previous actual (1000), actual = current actual (1050)
	assert.True(t, decimal.NewFromFloat(1000.00).Equal(v.ExpectedAmount),
		"expected amount should be previous run's actual balance, got %s", v.ExpectedAmount)
	assert.True(t, decimal.NewFromFloat(1050.00).Equal(v.ActualAmount),
		"actual amount should be current run's actual balance, got %s", v.ActualAmount)
	assert.True(t, decimal.NewFromFloat(50.00).Equal(v.VarianceAmount),
		"variance amount should be delta between runs (50), got %s", v.VarianceAmount)
}

func TestDetectVariances_RunNotRunning(t *testing.T) {
	runRepo := newMockRunRepo()
	snapRepo := &mockSnapshotRepo{}
	varianceRepo := &mockVarianceRepoFull{}

	// Create a PENDING run (not RUNNING)
	run := newTestRun(t) // This is PENDING
	_ = runRepo.Create(context.Background(), run)

	detector := NewVarianceDetector(runRepo, snapRepo, varianceRepo)
	_, err := detector.DetectVariances(context.Background(), run.RunID)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrRunNotRunning)
}

func TestDetectVariances_RunNotFound(t *testing.T) {
	runRepo := newMockRunRepo()
	snapRepo := &mockSnapshotRepo{}
	varianceRepo := &mockVarianceRepoFull{}

	detector := NewVarianceDetector(runRepo, snapRepo, varianceRepo)
	_, err := detector.DetectVariances(context.Background(), uuid.New())
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestDetectVariances_EmptySnapshots(t *testing.T) {
	runRepo := newMockRunRepo()
	snapRepo := &mockSnapshotRepo{}
	varianceRepo := &mockVarianceRepoFull{}

	run := newRunningTestRun(t)
	_ = runRepo.Create(context.Background(), run)

	detector := NewVarianceDetector(runRepo, snapRepo, varianceRepo)
	variances, err := detector.DetectVariances(context.Background(), run.RunID)
	require.NoError(t, err)
	assert.Nil(t, variances)
}

func TestDetectVariances_Idempotent(t *testing.T) {
	runRepo := newMockRunRepo()
	snapRepo := &mockSnapshotRepo{}
	varianceRepo := &mockVarianceRepoFull{}

	run := newRunningTestRun(t)
	_ = runRepo.Create(context.Background(), run)

	snap, _ := domain.NewSettlementSnapshot(
		run.RunID, "ACC-001", "GBP",
		decimal.NewFromFloat(1000.00),
		decimal.NewFromFloat(900.00),
		"pk", nil,
	)
	snapRepo.snapshots = []*domain.SettlementSnapshot{snap}

	detector := NewVarianceDetector(runRepo, snapRepo, varianceRepo)

	// First detection
	variances1, err := detector.DetectVariances(context.Background(), run.RunID)
	require.NoError(t, err)
	assert.Len(t, variances1, 1)

	// Second detection should produce the same result (idempotent)
	variances2, err := detector.DetectVariances(context.Background(), run.RunID)
	require.NoError(t, err)
	assert.Len(t, variances2, 1)

	// Should still only have 1 variance (old ones cleaned up)
	assert.Equal(t, 1, varianceRepo.varianceCount())
}

func TestDetectVariances_DeleteCleanupError(t *testing.T) {
	runRepo := newMockRunRepo()
	snapRepo := &mockSnapshotRepo{}
	varianceRepo := &mockVarianceRepoFull{deleteErr: errors.New("cleanup failed")}

	run := newRunningTestRun(t)
	_ = runRepo.Create(context.Background(), run)

	detector := NewVarianceDetector(runRepo, snapRepo, varianceRepo)
	_, err := detector.DetectVariances(context.Background(), run.RunID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "clean up existing variances")
}

func TestDetectVariances_PersistError(t *testing.T) {
	runRepo := newMockRunRepo()
	snapRepo := &mockSnapshotRepo{}
	varianceRepo := &mockVarianceRepoFull{createBatchErr: errors.New("db error")}

	run := newRunningTestRun(t)
	_ = runRepo.Create(context.Background(), run)

	snap, _ := domain.NewSettlementSnapshot(
		run.RunID, "ACC-001", "GBP",
		decimal.NewFromFloat(1000.00),
		decimal.NewFromFloat(900.00),
		"pk", nil,
	)
	snapRepo.snapshots = []*domain.SettlementSnapshot{snap}

	detector := NewVarianceDetector(runRepo, snapRepo, varianceRepo)
	_, err := detector.DetectVariances(context.Background(), run.RunID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "persist variances")
}

func TestClassifyVarianceReason(t *testing.T) {
	tests := []struct {
		name     string
		current  *domain.SettlementSnapshot
		previous *domain.SettlementSnapshot
		want     domain.VarianceReason
	}{
		{
			name: "D+1 run returns AMOUNT_MISMATCH",
			current: &domain.SettlementSnapshot{
				AccountID: "ACC-001", InstrumentCode: "GBP",
				SourceSystem: "pk",
			},
			previous: nil,
			want:     domain.VarianceReasonAmountMismatch,
		},
		{
			name: "different source systems returns EXTERNAL_MISMATCH",
			current: &domain.SettlementSnapshot{
				AccountID: "ACC-001", InstrumentCode: "GBP",
				SourceSystem: "pk",
			},
			previous: &domain.SettlementSnapshot{
				AccountID: "ACC-001", InstrumentCode: "GBP",
				SourceSystem: "external-ledger",
			},
			want: domain.VarianceReasonExternalMismatch,
		},
		{
			name: "quality upgrade returns QUALITY_UPGRADE",
			current: &domain.SettlementSnapshot{
				AccountID: "ACC-001", InstrumentCode: "KWH",
				SourceSystem: "pk",
				Attributes:   map[string]string{"quality": "ACTUAL"},
			},
			previous: &domain.SettlementSnapshot{
				AccountID: "ACC-001", InstrumentCode: "KWH",
				SourceSystem: "pk",
				Attributes:   map[string]string{"quality": "ESTIMATE"},
			},
			want: domain.VarianceReasonQualityUpgrade,
		},
		{
			name: "correction attribute returns CORRECTION_APPLIED",
			current: &domain.SettlementSnapshot{
				AccountID: "ACC-001", InstrumentCode: "GBP",
				SourceSystem: "pk",
				Attributes:   map[string]string{"correction": "wash_and_reload"},
			},
			previous: &domain.SettlementSnapshot{
				AccountID: "ACC-001", InstrumentCode: "GBP",
				SourceSystem: "pk",
			},
			want: domain.VarianceReasonCorrectionApplied,
		},
		{
			name: "same source different amounts returns AMOUNT_MISMATCH",
			current: &domain.SettlementSnapshot{
				AccountID: "ACC-001", InstrumentCode: "GBP",
				SourceSystem: "pk",
			},
			previous: &domain.SettlementSnapshot{
				AccountID: "ACC-001", InstrumentCode: "GBP",
				SourceSystem: "pk",
			},
			want: domain.VarianceReasonAmountMismatch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyVarianceReason(tt.current, tt.previous)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsQualityUpgrade(t *testing.T) {
	tests := []struct {
		name     string
		previous string
		current  string
		want     bool
	}{
		{"estimate to actual", "ESTIMATE", "ACTUAL", true},
		{"estimate to coefficient", "ESTIMATE", "COEFFICIENT", true},
		{"actual to revised", "ACTUAL", "REVISED", true},
		{"actual to estimate", "ACTUAL", "ESTIMATE", false},
		{"same quality", "ACTUAL", "ACTUAL", false},
		{"unknown quality", "UNKNOWN", "ACTUAL", true},
		{"both unknown", "UNKNOWN", "OTHER", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isQualityUpgrade(tt.previous, tt.current)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDetectVariances_DecimalPrecision(t *testing.T) {
	runRepo := newMockRunRepo()
	snapRepo := &mockSnapshotRepo{}
	varianceRepo := &mockVarianceRepoFull{}

	run := newRunningTestRun(t)
	_ = runRepo.Create(context.Background(), run)

	// Small decimal variance
	expected, _ := decimal.NewFromString("999.999999999999999999")
	actual, _ := decimal.NewFromString("1000.000000000000000000")

	snap, _ := domain.NewSettlementSnapshot(
		run.RunID, "ACC-001", "GBP",
		expected, actual, "pk", nil,
	)
	snapRepo.snapshots = []*domain.SettlementSnapshot{snap}

	detector := NewVarianceDetector(runRepo, snapRepo, varianceRepo)
	variances, err := detector.DetectVariances(context.Background(), run.RunID)
	require.NoError(t, err)

	require.Len(t, variances, 1)
	expectedVariance := actual.Sub(expected)
	assert.True(t, expectedVariance.Equal(variances[0].VarianceAmount),
		"expected variance %s, got %s", expectedVariance, variances[0].VarianceAmount)
}
