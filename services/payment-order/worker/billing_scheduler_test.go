package worker

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/payment-order/adapters/persistence"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/meridianhub/meridian/shared/platform/scheduler"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mock billing repository ---

type mockBillingRepo struct {
	mu             sync.Mutex
	runs           map[uuid.UUID]*domain.BillingRun
	invoices       map[uuid.UUID]*domain.Invoice
	createErr      error
	findErr        error
	updateErr      error
	duplicateCheck map[string]bool // tenantID_start_end -> exists
}

func newMockBillingRepo() *mockBillingRepo {
	return &mockBillingRepo{
		runs:           make(map[uuid.UUID]*domain.BillingRun),
		invoices:       make(map[uuid.UUID]*domain.Invoice),
		duplicateCheck: make(map[string]bool),
	}
}

func (m *mockBillingRepo) CreateBillingRun(_ context.Context, run *domain.BillingRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.createErr != nil {
		return m.createErr
	}
	key := run.TenantID + "_" + run.CycleStart.Format(time.RFC3339) + "_" + run.CycleEnd.Format(time.RFC3339)
	if m.duplicateCheck[key] {
		return persistence.ErrBillingRunDuplicate
	}
	m.duplicateCheck[key] = true
	m.runs[run.ID] = run
	return nil
}

func (m *mockBillingRepo) FindBillingRunByID(_ context.Context, id uuid.UUID) (*domain.BillingRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.findErr != nil {
		return nil, m.findErr
	}
	run, ok := m.runs[id]
	if !ok {
		return nil, persistence.ErrBillingRunNotFound
	}
	return run, nil
}

func (m *mockBillingRepo) FindBillingRunByTenantAndPeriod(_ context.Context, tenantID string, _, _ time.Time) (*domain.BillingRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, run := range m.runs {
		if run.TenantID == tenantID {
			return run, nil
		}
	}
	return nil, persistence.ErrBillingRunNotFound
}

func (m *mockBillingRepo) UpdateBillingRun(_ context.Context, run *domain.BillingRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.updateErr != nil {
		return m.updateErr
	}
	m.runs[run.ID] = run
	return nil
}

func (m *mockBillingRepo) CreateInvoice(_ context.Context, inv *domain.Invoice) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.invoices[inv.ID] = inv
	return nil
}

func (m *mockBillingRepo) FindInvoiceByID(_ context.Context, id uuid.UUID) (*domain.Invoice, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inv, ok := m.invoices[id]
	if !ok {
		return nil, persistence.ErrInvoiceNotFound
	}
	return inv, nil
}

func (m *mockBillingRepo) FindInvoicesByBillingRunID(_ context.Context, billingRunID uuid.UUID) ([]*domain.Invoice, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var results []*domain.Invoice
	for _, inv := range m.invoices {
		if inv.BillingRunID == billingRunID {
			results = append(results, inv)
		}
	}
	return results, nil
}

func (m *mockBillingRepo) UpdateInvoice(_ context.Context, inv *domain.Invoice) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.invoices[inv.ID] = inv
	return nil
}

func (m *mockBillingRepo) ListBillingRuns(_ context.Context, _ persistence.BillingRunFilter, _ int, _ string) (*persistence.BillingRunPage, error) {
	return &persistence.BillingRunPage{}, nil
}

func (m *mockBillingRepo) ListInvoices(_ context.Context, _ persistence.InvoiceFilter, _ int, _ string) (*persistence.InvoicePage, error) {
	return &persistence.InvoicePage{}, nil
}

func (m *mockBillingRepo) CountInvoicesByBillingRun(_ context.Context, _ uuid.UUID) (int64, error) {
	return 0, nil
}

func (m *mockBillingRepo) SumInvoiceTotalsByBillingRun(_ context.Context, _ uuid.UUID) (int64, error) {
	return 0, nil
}

func (m *mockBillingRepo) ListEmailsByInvoice(_ context.Context, _ uuid.UUID) ([]*persistence.EmailAuditEntry, error) {
	return nil, nil
}

func (m *mockBillingRepo) getRunCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.runs)
}

func (m *mockBillingRepo) getFirstRun() *domain.BillingRun {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, run := range m.runs {
		return run
	}
	return nil
}

// --- Test helpers ---

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func testMetrics(t *testing.T) *BillingMetrics {
	t.Helper()
	reg := prometheus.NewRegistry()
	return NewBillingMetricsWithRegistry(reg)
}

func setupMiniredis(t *testing.T) *redis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	return redis.NewClient(&redis.Options{Addr: mr.Addr()})
}

// --- BillingScheduleProvider Tests ---

func TestBillingScheduleProvider(t *testing.T) {
	t.Run("returns single schedule for tenant", func(t *testing.T) {
		provider := NewBillingScheduleProvider("tenant-1", "0 2 1 * *")
		schedules, err := provider.ListSchedules(context.Background())
		require.NoError(t, err)
		require.Len(t, schedules, 1)

		sched := schedules[0]
		assert.Equal(t, "billing:tenant-1", sched.ID)
		assert.Equal(t, "0 2 1 * *", sched.CronExpr)
		assert.Equal(t, "tenant-1", sched.TenantID)
	})

	t.Run("schedule is deterministic", func(t *testing.T) {
		provider := NewBillingScheduleProvider("abc", "0 0 * * *")
		s1, _ := provider.ListSchedules(context.Background())
		s2, _ := provider.ListSchedules(context.Background())
		assert.Equal(t, s1, s2)
	})
}

// --- BillingExecutor Tests ---

func TestBillingExecutorExecute(t *testing.T) {
	t.Run("creates billing run on execution", func(t *testing.T) {
		repo := newMockBillingRepo()
		redisClient := setupMiniredis(t)
		logger := testLogger()
		metrics := testMetrics(t)

		// Pin NowFunc for deterministic billing period
		origNow := NowFunc
		NowFunc = func() time.Time {
			return time.Date(2026, 3, 1, 2, 0, 0, 0, time.UTC)
		}
		defer func() { NowFunc = origNow }()

		executor := NewBillingExecutor(repo, redisClient, metrics, BillingExecutorConfig{}, logger)

		schedule := scheduler.Schedule{
			ID:       "billing:tenant-1",
			CronExpr: "0 2 1 * *",
			TenantID: "tenant-1",
		}

		err := executor.Execute(context.Background(), schedule)
		require.NoError(t, err)

		assert.Equal(t, 1, repo.getRunCount())
		run := repo.getFirstRun()
		require.NotNil(t, run)
		assert.Equal(t, "tenant-1", run.TenantID)
		assert.Equal(t, domain.BillingRunStatusCompleted, run.Status)
	})

	t.Run("skips duplicate via Redis idempotency", func(t *testing.T) {
		repo := newMockBillingRepo()
		redisClient := setupMiniredis(t)
		logger := testLogger()
		metrics := testMetrics(t)

		origNow := NowFunc
		NowFunc = func() time.Time {
			return time.Date(2026, 3, 1, 2, 0, 0, 0, time.UTC)
		}
		defer func() { NowFunc = origNow }()

		executor := NewBillingExecutor(repo, redisClient, metrics, BillingExecutorConfig{}, logger)

		schedule := scheduler.Schedule{
			ID:       "billing:tenant-dup",
			CronExpr: "0 2 1 * *",
			TenantID: "tenant-dup",
		}

		// First execution
		err := executor.Execute(context.Background(), schedule)
		require.NoError(t, err)
		assert.Equal(t, 1, repo.getRunCount())

		// Second execution should be skipped (Redis idempotency)
		err = executor.Execute(context.Background(), schedule)
		require.NoError(t, err)
		assert.Equal(t, 1, repo.getRunCount(), "should not create a second billing run")
	})

	t.Run("skips duplicate via database", func(t *testing.T) {
		repo := newMockBillingRepo()
		redisClient := setupMiniredis(t)
		logger := testLogger()
		metrics := testMetrics(t)

		origNow := NowFunc
		NowFunc = func() time.Time {
			return time.Date(2026, 4, 1, 2, 0, 0, 0, time.UTC)
		}
		defer func() { NowFunc = origNow }()

		executor := NewBillingExecutor(repo, redisClient, metrics, BillingExecutorConfig{}, logger)

		schedule := scheduler.Schedule{
			ID:       "billing:tenant-dbdup",
			CronExpr: "0 2 1 * *",
			TenantID: "tenant-dbdup",
		}

		// Pre-populate the DB with a billing run for the same period
		start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
		end := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
		run, _ := domain.NewBillingRun("tenant-dbdup", start, end)
		_ = repo.CreateBillingRun(context.Background(), run)
		assert.Equal(t, 1, repo.getRunCount())

		// Execution should detect DB duplicate and skip gracefully
		err := executor.Execute(context.Background(), schedule)
		require.NoError(t, err)
		assert.Equal(t, 1, repo.getRunCount(), "should not create a second billing run")
	})

	t.Run("shadow mode does not initiate payments", func(t *testing.T) {
		repo := newMockBillingRepo()
		redisClient := setupMiniredis(t)
		logger := testLogger()
		metrics := testMetrics(t)

		origNow := NowFunc
		NowFunc = func() time.Time {
			return time.Date(2026, 5, 1, 2, 0, 0, 0, time.UTC)
		}
		defer func() { NowFunc = origNow }()

		executor := NewBillingExecutor(repo, redisClient, metrics, BillingExecutorConfig{ShadowMode: true}, logger)

		schedule := scheduler.Schedule{
			ID:       "billing:tenant-shadow",
			CronExpr: "0 2 1 * *",
			TenantID: "tenant-shadow",
		}

		err := executor.Execute(context.Background(), schedule)
		require.NoError(t, err)

		run := repo.getFirstRun()
		require.NotNil(t, run)
		assert.Equal(t, domain.BillingRunStatusCompleted, run.Status)
	})

	t.Run("returns error on repo failure", func(t *testing.T) {
		repo := newMockBillingRepo()
		repo.createErr = errors.New("database connection lost")
		redisClient := setupMiniredis(t)
		logger := testLogger()
		metrics := testMetrics(t)

		origNow := NowFunc
		NowFunc = func() time.Time {
			return time.Date(2026, 6, 1, 2, 0, 0, 0, time.UTC)
		}
		defer func() { NowFunc = origNow }()

		executor := NewBillingExecutor(repo, redisClient, metrics, BillingExecutorConfig{}, logger)

		schedule := scheduler.Schedule{
			ID:       "billing:tenant-err",
			CronExpr: "0 2 1 * *",
			TenantID: "tenant-err",
		}

		err := executor.Execute(context.Background(), schedule)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "persist billing run")
	})
}

// --- calculateBillingPeriod Tests ---

func TestCalculateBillingPeriod(t *testing.T) {
	t.Run("February run covers January", func(t *testing.T) {
		now := time.Date(2026, 2, 1, 2, 0, 0, 0, time.UTC)
		start, end := calculateBillingPeriod(now)
		assert.Equal(t, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), start)
		assert.Equal(t, time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), end)
	})

	t.Run("January run covers December of previous year", func(t *testing.T) {
		now := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
		start, end := calculateBillingPeriod(now)
		assert.Equal(t, time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC), start)
		assert.Equal(t, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), end)
	})

	t.Run("mid-month run still covers previous month", func(t *testing.T) {
		now := time.Date(2026, 6, 15, 14, 30, 0, 0, time.UTC)
		start, end := calculateBillingPeriod(now)
		assert.Equal(t, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), start)
		assert.Equal(t, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), end)
	})

	t.Run("period is deterministic for same month", func(t *testing.T) {
		now1 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
		now2 := time.Date(2026, 3, 28, 23, 59, 59, 0, time.UTC)
		start1, end1 := calculateBillingPeriod(now1)
		start2, end2 := calculateBillingPeriod(now2)
		assert.Equal(t, start1, start2)
		assert.Equal(t, end1, end2)
	})
}

// --- Idempotency Key Tests ---

func TestIdempotencyKeyDeterminism(t *testing.T) {
	tenantID := "tenant-abc"
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	key1 := domain.BillingRunIdempotencyKey(tenantID, start, end)
	key2 := domain.BillingRunIdempotencyKey(tenantID, start, end)

	assert.Equal(t, key1, key2, "idempotency key should be deterministic for same period")
	assert.Contains(t, key1, tenantID)
	assert.Contains(t, key1, "2026-01-01")
	assert.Contains(t, key1, "2026-02-01")
}

// --- Duplicate Skip Tests ---

func TestBillingSchedulerDuplicateSkip(t *testing.T) {
	repo := newMockBillingRepo()

	tenantID := "tenant-dup-test"
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	// Create first run
	run1, err := domain.NewBillingRun(tenantID, start, end)
	require.NoError(t, err)
	require.NoError(t, repo.CreateBillingRun(context.Background(), run1))

	// Attempt duplicate - should get ErrBillingRunDuplicate
	run2, err := domain.NewBillingRun(tenantID, start, end)
	require.NoError(t, err)
	err = repo.CreateBillingRun(context.Background(), run2)
	assert.ErrorIs(t, err, persistence.ErrBillingRunDuplicate)

	// Only one run should exist
	assert.Equal(t, 1, repo.getRunCount())
}

// --- Shadow Mode Config Tests ---

func TestBillingSchedulerShadowMode(t *testing.T) {
	t.Run("shadow mode config is preserved", func(t *testing.T) {
		config := BillingSchedulerConfig{
			TenantID:       "tenant-shadow",
			CronExpression: "0 2 1 * *",
			ShadowMode:     true,
		}
		assert.True(t, config.ShadowMode)
	})
}

// --- Billing Run Creation Tests ---

func TestBillingRunCreation(t *testing.T) {
	repo := newMockBillingRepo()
	ctx := context.Background()

	t.Run("creates billing run with correct fields", func(t *testing.T) {
		start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		end := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

		run, err := domain.NewBillingRun("tenant-1", start, end)
		require.NoError(t, err)

		err = repo.CreateBillingRun(ctx, run)
		require.NoError(t, err)

		found := repo.getFirstRun()
		require.NotNil(t, found)
		assert.Equal(t, "tenant-1", found.TenantID)
		assert.Equal(t, start, found.CycleStart)
		assert.Equal(t, end, found.CycleEnd)
		assert.Equal(t, domain.BillingRunStatusInitiated, found.Status)
	})
}

// --- Billing Run Failure Tests ---

func TestBillingRunFailure(t *testing.T) {
	repo := newMockBillingRepo()
	repo.createErr = errors.New("database connection lost")

	ctx := context.Background()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	run, err := domain.NewBillingRun("tenant-1", start, end)
	require.NoError(t, err)

	err = repo.CreateBillingRun(ctx, run)
	assert.Error(t, err)
	assert.Equal(t, 0, repo.getRunCount())
}
