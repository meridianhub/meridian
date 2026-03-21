package service

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/meridianhub/meridian/services/reconciliation/testhelpers"
	"github.com/stretchr/testify/require"
)

// mockVarianceRepoFull implements domain.VarianceRepository with an in-memory
// slice of variances. Thread-safe for concurrent access from errgroup goroutines.
// Used by variance_valuator_test.go.
type mockVarianceRepoFull struct {
	mu        sync.Mutex
	variances []*domain.Variance
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

func (m *mockVarianceRepoFull) DeleteByRunID(_ context.Context, _ uuid.UUID) error {
	return nil
}

func (m *mockVarianceRepoFull) List(_ context.Context, _ domain.VarianceFilter) ([]*domain.Variance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.variances, nil
}

// newRunningTestRun creates a settlement run in RUNNING status for test use.
func newRunningTestRun(t *testing.T) *domain.SettlementRun {
	t.Helper()
	run := testhelpers.NewSettlementRun(t)
	require.NoError(t, run.Start())
	return run
}
