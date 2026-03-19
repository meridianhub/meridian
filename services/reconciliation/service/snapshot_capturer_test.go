package service

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/meridianhub/meridian/services/reconciliation/testhelpers"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mock PositionDataProvider ---

type mockPositionProvider struct {
	pages []PositionPage
	calls int
	err   error
}

func (m *mockPositionProvider) FetchPositions(_ context.Context, _ string, _ int32, _ string) (*PositionPage, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.calls >= len(m.pages) {
		return &PositionPage{}, nil
	}
	page := m.pages[m.calls]
	m.calls++
	return &page, nil
}

// --- Mock SettlementRunRepository ---

type mockRunRepo struct {
	mu   sync.Mutex
	runs map[uuid.UUID]*domain.SettlementRun

	findErr   error
	updateErr error
}

func newMockRunRepo() *mockRunRepo {
	return &mockRunRepo{runs: make(map[uuid.UUID]*domain.SettlementRun)}
}

func (m *mockRunRepo) Create(_ context.Context, run *domain.SettlementRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runs[run.RunID] = run
	return nil
}

func (m *mockRunRepo) FindByID(_ context.Context, runID uuid.UUID) (*domain.SettlementRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.findErr != nil {
		return nil, m.findErr
	}
	run, ok := m.runs[runID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return run, nil
}

func (m *mockRunRepo) Update(_ context.Context, run *domain.SettlementRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.updateErr != nil {
		return m.updateErr
	}
	m.runs[run.RunID] = run
	return nil
}

func (m *mockRunRepo) List(_ context.Context, filter domain.RunFilter) ([]*domain.SettlementRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]*domain.SettlementRun, 0, len(m.runs))
	for _, run := range m.runs {
		if filter.AccountID != nil && run.AccountID != *filter.AccountID {
			continue
		}
		if filter.Status != nil && run.Status != *filter.Status {
			continue
		}
		if filter.ToDate != nil && !run.CreatedAt.Before(*filter.ToDate) {
			continue
		}
		result = append(result, run)
	}
	if filter.Limit > 0 && len(result) > filter.Limit {
		result = result[:filter.Limit]
	}
	return result, nil
}

// --- Mock SettlementSnapshotRepository ---

type mockSnapshotRepo struct {
	mu        sync.Mutex
	snapshots []*domain.SettlementSnapshot
	deleted   []uuid.UUID

	createBatchErr error
	deleteErr      error
}

func (m *mockSnapshotRepo) Create(_ context.Context, snap *domain.SettlementSnapshot) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snapshots = append(m.snapshots, snap)
	return nil
}

func (m *mockSnapshotRepo) CreateBatch(_ context.Context, snaps []*domain.SettlementSnapshot) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.createBatchErr != nil {
		return m.createBatchErr
	}
	m.snapshots = append(m.snapshots, snaps...)
	return nil
}

func (m *mockSnapshotRepo) FindByID(_ context.Context, _ uuid.UUID) (*domain.SettlementSnapshot, error) {
	return nil, domain.ErrNotFound
}

func (m *mockSnapshotRepo) FindByRunID(_ context.Context, runID uuid.UUID) ([]*domain.SettlementSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*domain.SettlementSnapshot
	for _, s := range m.snapshots {
		if s.RunID == runID {
			result = append(result, s)
		}
	}
	return result, nil
}

func (m *mockSnapshotRepo) DeleteByRunID(_ context.Context, runID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.deleted = append(m.deleted, runID)
	return nil
}

func (m *mockSnapshotRepo) MarkRunSnapshotsFinal(_ context.Context, _ uuid.UUID) error {
	return nil
}

func (m *mockSnapshotRepo) snapshotCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.snapshots)
}

// --- Helper to create a test run ---

func newTestRun(t *testing.T) *domain.SettlementRun {
	t.Helper()
	return testhelpers.NewSettlementRun(t)
}

// --- Tests ---

func TestCaptureSnapshots_SinglePage(t *testing.T) {
	runRepo := newMockRunRepo()
	snapRepo := &mockSnapshotRepo{}

	run := newTestRun(t)
	_ = runRepo.Create(context.Background(), run)

	provider := &mockPositionProvider{
		pages: []PositionPage{
			{
				Records: []PositionRecord{
					{AccountID: "ACC-001", InstrumentCode: "GBP", Balance: decimal.NewFromFloat(1000.00), SourceSystem: "pk"},
					{AccountID: "ACC-002", InstrumentCode: "GBP", Balance: decimal.NewFromFloat(2500.50), SourceSystem: "pk"},
				},
				NextPageToken: "",
			},
		},
	}

	capturer := NewSnapshotCapturer(provider, runRepo, snapRepo)
	err := capturer.CaptureSnapshots(context.Background(), run.RunID)
	require.NoError(t, err)

	assert.Equal(t, 2, snapRepo.snapshotCount())
	assert.Equal(t, domain.RunStatusCompleted, runRepo.runs[run.RunID].Status)
}

func TestCaptureSnapshots_MultiplePages(t *testing.T) {
	runRepo := newMockRunRepo()
	snapRepo := &mockSnapshotRepo{}

	run := newTestRun(t)
	_ = runRepo.Create(context.Background(), run)

	provider := &mockPositionProvider{
		pages: []PositionPage{
			{
				Records: []PositionRecord{
					{AccountID: "ACC-001", InstrumentCode: "GBP", Balance: decimal.NewFromFloat(100), SourceSystem: "pk"},
				},
				NextPageToken: "page2",
			},
			{
				Records: []PositionRecord{
					{AccountID: "ACC-002", InstrumentCode: "GBP", Balance: decimal.NewFromFloat(200), SourceSystem: "pk"},
				},
				NextPageToken: "page3",
			},
			{
				Records: []PositionRecord{
					{AccountID: "ACC-003", InstrumentCode: "GBP", Balance: decimal.NewFromFloat(300), SourceSystem: "pk"},
				},
				NextPageToken: "",
			},
		},
	}

	capturer := NewSnapshotCapturer(provider, runRepo, snapRepo)
	err := capturer.CaptureSnapshots(context.Background(), run.RunID)
	require.NoError(t, err)

	assert.Equal(t, 3, snapRepo.snapshotCount())
	assert.Equal(t, 3, provider.calls)
}

func TestCaptureSnapshots_EmptyPage(t *testing.T) {
	runRepo := newMockRunRepo()
	snapRepo := &mockSnapshotRepo{}

	run := newTestRun(t)
	_ = runRepo.Create(context.Background(), run)

	provider := &mockPositionProvider{
		pages: []PositionPage{
			{Records: []PositionRecord{}, NextPageToken: ""},
		},
	}

	capturer := NewSnapshotCapturer(provider, runRepo, snapRepo)
	err := capturer.CaptureSnapshots(context.Background(), run.RunID)
	require.NoError(t, err)

	assert.Equal(t, 0, snapRepo.snapshotCount())
	assert.Equal(t, domain.RunStatusCompleted, runRepo.runs[run.RunID].Status)
}

func TestCaptureSnapshots_ProviderError(t *testing.T) {
	runRepo := newMockRunRepo()
	snapRepo := &mockSnapshotRepo{}

	run := newTestRun(t)
	_ = runRepo.Create(context.Background(), run)

	providerErr := errors.New("connection refused")
	provider := &mockPositionProvider{err: providerErr}

	capturer := NewSnapshotCapturer(provider, runRepo, snapRepo)
	err := capturer.CaptureSnapshots(context.Background(), run.RunID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")
	assert.Equal(t, domain.RunStatusFailed, runRepo.runs[run.RunID].Status)
}

func TestCaptureSnapshots_RunNotFound(t *testing.T) {
	runRepo := newMockRunRepo()
	snapRepo := &mockSnapshotRepo{}
	provider := &mockPositionProvider{}

	capturer := NewSnapshotCapturer(provider, runRepo, snapRepo)
	err := capturer.CaptureSnapshots(context.Background(), uuid.New())
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestCaptureSnapshots_RunAlreadyRunning(t *testing.T) {
	runRepo := newMockRunRepo()
	snapRepo := &mockSnapshotRepo{}
	provider := &mockPositionProvider{}

	run := newTestRun(t)
	_ = run.Start() // Transition to RUNNING
	_ = runRepo.Create(context.Background(), run)

	capturer := NewSnapshotCapturer(provider, runRepo, snapRepo)
	err := capturer.CaptureSnapshots(context.Background(), run.RunID)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrInvalidStatusTransition)
}

func TestCaptureSnapshots_BatchInsertError(t *testing.T) {
	runRepo := newMockRunRepo()
	snapRepo := &mockSnapshotRepo{createBatchErr: errors.New("db connection lost")}

	run := newTestRun(t)
	_ = runRepo.Create(context.Background(), run)

	provider := &mockPositionProvider{
		pages: []PositionPage{
			{
				Records: []PositionRecord{
					{AccountID: "ACC-001", InstrumentCode: "GBP", Balance: decimal.NewFromFloat(100), SourceSystem: "pk"},
				},
				NextPageToken: "",
			},
		},
	}

	capturer := NewSnapshotCapturer(provider, runRepo, snapRepo)
	err := capturer.CaptureSnapshots(context.Background(), run.RunID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db connection lost")
	assert.Equal(t, domain.RunStatusFailed, runRepo.runs[run.RunID].Status)
}

func TestCaptureSnapshots_IdempotentCleanup(t *testing.T) {
	runRepo := newMockRunRepo()
	snapRepo := &mockSnapshotRepo{}

	run := newTestRun(t)
	_ = runRepo.Create(context.Background(), run)

	provider := &mockPositionProvider{
		pages: []PositionPage{
			{
				Records: []PositionRecord{
					{AccountID: "ACC-001", InstrumentCode: "GBP", Balance: decimal.NewFromFloat(100), SourceSystem: "pk"},
				},
			},
		},
	}

	capturer := NewSnapshotCapturer(provider, runRepo, snapRepo)
	err := capturer.CaptureSnapshots(context.Background(), run.RunID)
	require.NoError(t, err)

	// Verify DeleteByRunID was called for idempotent cleanup
	assert.Contains(t, snapRepo.deleted, run.RunID)
}

func TestCaptureSnapshots_DeleteCleanupError(t *testing.T) {
	runRepo := newMockRunRepo()
	snapRepo := &mockSnapshotRepo{deleteErr: errors.New("cleanup failed")}

	run := newTestRun(t)
	_ = runRepo.Create(context.Background(), run)

	provider := &mockPositionProvider{
		pages: []PositionPage{
			{
				Records: []PositionRecord{
					{AccountID: "ACC-001", InstrumentCode: "GBP", Balance: decimal.NewFromFloat(100), SourceSystem: "pk"},
				},
			},
		},
	}

	capturer := NewSnapshotCapturer(provider, runRepo, snapRepo)
	err := capturer.CaptureSnapshots(context.Background(), run.RunID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "clean up existing snapshots")
	assert.Equal(t, domain.RunStatusFailed, runRepo.runs[run.RunID].Status)
}

func TestCaptureSnapshots_LargeDataset(t *testing.T) {
	runRepo := newMockRunRepo()
	snapRepo := &mockSnapshotRepo{}

	run := newTestRun(t)
	_ = runRepo.Create(context.Background(), run)

	// Create 5 pages of 500 records each (2500 total)
	pages := make([]PositionPage, 5)
	for i := range pages {
		records := make([]PositionRecord, 500)
		for j := range records {
			records[j] = PositionRecord{
				AccountID:      "ACC-001",
				InstrumentCode: "GBP",
				Balance:        decimal.NewFromInt(int64(i*500 + j)),
				SourceSystem:   "pk",
			}
		}
		nextToken := ""
		if i < 4 {
			nextToken = "next"
		}
		pages[i] = PositionPage{
			Records:       records,
			NextPageToken: nextToken,
		}
	}

	provider := &mockPositionProvider{pages: pages}

	capturer := NewSnapshotCapturer(provider, runRepo, snapRepo)
	err := capturer.CaptureSnapshots(context.Background(), run.RunID)
	require.NoError(t, err)

	assert.Equal(t, 2500, snapRepo.snapshotCount())
	assert.Equal(t, domain.RunStatusCompleted, runRepo.runs[run.RunID].Status)
}

func TestCaptureSnapshots_ContextCancelled(t *testing.T) {
	runRepo := newMockRunRepo()
	snapRepo := &mockSnapshotRepo{}

	run := newTestRun(t)
	_ = runRepo.Create(context.Background(), run)

	// Provider that returns a page but the context will be cancelled
	ctx, cancel := context.WithCancel(context.Background())
	provider := &mockPositionProvider{
		pages: []PositionPage{
			{
				Records: []PositionRecord{
					{AccountID: "ACC-001", InstrumentCode: "GBP", Balance: decimal.NewFromFloat(100), SourceSystem: "pk"},
				},
				NextPageToken: "page2",
			},
		},
		err: context.Canceled,
	}

	// Cancel context before calling
	cancel()

	capturer := NewSnapshotCapturer(provider, runRepo, snapRepo)
	err := capturer.CaptureSnapshots(ctx, run.RunID)
	// Should fail - either context cancelled or provider returns error
	require.Error(t, err)
}
