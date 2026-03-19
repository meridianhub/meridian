package worker

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/meridianhub/meridian/shared/platform/scheduler"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithInvoiceGenerator(t *testing.T) {
	repo := newMockBillingRepo()
	redisClient := setupMiniredis(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	metrics := NewBillingMetricsWithRegistry(prometheus.NewRegistry())

	executor := NewBillingExecutor(repo, redisClient, metrics, BillingExecutorConfig{}, logger)
	assert.Nil(t, executor.invoiceGenerator)

	gen := &InvoiceGenerator{}
	result := executor.WithInvoiceGenerator(gen)
	assert.Same(t, executor, result, "WithInvoiceGenerator should return the same executor for chaining")
	assert.Same(t, gen, executor.invoiceGenerator)
}

func TestWithPaymentInitiator(t *testing.T) {
	repo := newMockBillingRepo()
	redisClient := setupMiniredis(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	metrics := NewBillingMetricsWithRegistry(prometheus.NewRegistry())

	executor := NewBillingExecutor(repo, redisClient, metrics, BillingExecutorConfig{}, logger)
	assert.Nil(t, executor.paymentInitiator)

	init := &PaymentInitiator{}
	result := executor.WithPaymentInitiator(init)
	assert.Same(t, executor, result, "WithPaymentInitiator should return the same executor for chaining")
	assert.Same(t, init, executor.paymentInitiator)
}

func TestProcessInvoices_NilGenerator(t *testing.T) {
	repo := newMockBillingRepo()
	redisClient := setupMiniredis(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	metrics := NewBillingMetricsWithRegistry(prometheus.NewRegistry())

	executor := NewBillingExecutor(repo, redisClient, metrics, BillingExecutorConfig{}, logger)

	run, err := domain.NewBillingRun("tenant-1", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)

	// processInvoices with nil generator should return nil
	err = executor.processInvoices(context.Background(), run)
	assert.NoError(t, err)
}

func TestBillingExecutor_Execute_UpdateError(t *testing.T) {
	repo := newMockBillingRepo()
	redisClient := setupMiniredis(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	metrics := NewBillingMetricsWithRegistry(prometheus.NewRegistry())

	origNow := NowFunc
	NowFunc = func() time.Time {
		return time.Date(2026, 7, 1, 2, 0, 0, 0, time.UTC)
	}
	defer func() { NowFunc = origNow }()

	executor := NewBillingExecutor(repo, redisClient, metrics, BillingExecutorConfig{}, logger)

	// Set update to fail - this will fail the first UpdateBillingRun call (to processing)
	repo.updateErr = errors.New("update failed")

	schedule := scheduler.Schedule{
		ID:       "billing:tenant-upd-err",
		CronExpr: "0 2 1 * *",
		TenantID: "tenant-upd-err",
	}

	err := executor.Execute(context.Background(), schedule)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update billing run")
}

// mockPositionKeepingClient implements PositionKeepingClient for testing.
type mockPositionKeepingClient struct {
	listAccountsFunc func(ctx context.Context, tenantID string) ([]AccountInfo, error)
}

func (m *mockPositionKeepingClient) GetAccountBalance(_ context.Context, _ string) (int64, string, error) {
	return 0, "", nil
}

func (m *mockPositionKeepingClient) ListAccountsForTenant(ctx context.Context, tenantID string) ([]AccountInfo, error) {
	if m.listAccountsFunc != nil {
		return m.listAccountsFunc(ctx, tenantID)
	}
	return nil, nil
}

func (m *mockPositionKeepingClient) GetPositionLogEntries(_ context.Context, _ string, _, _ time.Time) ([]PositionEntry, error) {
	return nil, nil
}

func TestProcessInvoices_GeneratorError(t *testing.T) {
	repo := newMockBillingRepo()
	redisClient := setupMiniredis(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	metrics := NewBillingMetricsWithRegistry(prometheus.NewRegistry())

	executor := NewBillingExecutor(repo, redisClient, metrics, BillingExecutorConfig{}, logger)

	// Create an InvoiceGenerator that will fail
	failingPosClient := &mockPositionKeepingClient{
		listAccountsFunc: func(_ context.Context, _ string) ([]AccountInfo, error) {
			return nil, errors.New("position-keeping unavailable")
		},
	}
	gen := NewInvoiceGenerator(failingPosClient, repo, metrics, logger)
	executor.WithInvoiceGenerator(gen)

	run, err := domain.NewBillingRun("tenant-gen-err",
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.NoError(t, run.StartProcessing())

	err = executor.processInvoices(context.Background(), run)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invoice generation")
	// Billing run should be marked as failed
	assert.Equal(t, domain.BillingRunStatusFailed, run.Status)
}

func TestProcessInvoices_EmptyInvoices(t *testing.T) {
	repo := newMockBillingRepo()
	redisClient := setupMiniredis(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	metrics := NewBillingMetricsWithRegistry(prometheus.NewRegistry())

	executor := NewBillingExecutor(repo, redisClient, metrics, BillingExecutorConfig{}, logger)

	// Create an InvoiceGenerator that returns empty accounts (no invoices)
	emptyPosClient := &mockPositionKeepingClient{
		listAccountsFunc: func(_ context.Context, _ string) ([]AccountInfo, error) {
			return nil, nil // No accounts
		},
	}
	gen := NewInvoiceGenerator(emptyPosClient, repo, metrics, logger)
	executor.WithInvoiceGenerator(gen)

	run, err := domain.NewBillingRun("tenant-empty",
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)

	err = executor.processInvoices(context.Background(), run)
	assert.NoError(t, err)
}

func TestProcessInvoices_NilPaymentInitiator(t *testing.T) {
	repo := newMockBillingRepo()
	redisClient := setupMiniredis(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	metrics := NewBillingMetricsWithRegistry(prometheus.NewRegistry())

	executor := NewBillingExecutor(repo, redisClient, metrics, BillingExecutorConfig{}, logger)

	// Generator returns empty invoices - even with nil paymentInitiator it should be fine
	emptyPosClient := &mockPositionKeepingClient{
		listAccountsFunc: func(_ context.Context, _ string) ([]AccountInfo, error) {
			return nil, nil
		},
	}
	gen := NewInvoiceGenerator(emptyPosClient, repo, metrics, logger)
	executor.WithInvoiceGenerator(gen)
	// Don't set paymentInitiator

	run, err := domain.NewBillingRun("tenant-no-pi",
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)

	err = executor.processInvoices(context.Background(), run)
	assert.NoError(t, err)
}

func TestExecute_FullCycleWithInvoiceGenerator(t *testing.T) {
	repo := newMockBillingRepo()
	redisClient := setupMiniredis(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	metrics := NewBillingMetricsWithRegistry(prometheus.NewRegistry())

	origNow := NowFunc
	NowFunc = func() time.Time {
		return time.Date(2026, 8, 1, 2, 0, 0, 0, time.UTC)
	}
	defer func() { NowFunc = origNow }()

	executor := NewBillingExecutor(repo, redisClient, metrics, BillingExecutorConfig{}, logger)

	// Generator returns empty invoices (no accounts)
	emptyPosClient := &mockPositionKeepingClient{
		listAccountsFunc: func(_ context.Context, _ string) ([]AccountInfo, error) {
			return nil, nil
		},
	}
	gen := NewInvoiceGenerator(emptyPosClient, repo, metrics, logger)
	executor.WithInvoiceGenerator(gen)

	schedule := scheduler.Schedule{
		ID:       "billing:tenant-full",
		CronExpr: "0 2 1 * *",
		TenantID: "tenant-full",
	}

	err := executor.Execute(context.Background(), schedule)
	assert.NoError(t, err)

	run := repo.getFirstRun()
	require.NotNil(t, run)
	assert.Equal(t, domain.BillingRunStatusCompleted, run.Status)
}

func TestExecute_CreateError(t *testing.T) {
	repo := newMockBillingRepo()
	redisClient := setupMiniredis(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	metrics := NewBillingMetricsWithRegistry(prometheus.NewRegistry())
	repo.createErr = errors.New("db down")

	origNow := NowFunc
	NowFunc = func() time.Time {
		return time.Date(2026, 9, 1, 2, 0, 0, 0, time.UTC)
	}
	defer func() { NowFunc = origNow }()

	executor := NewBillingExecutor(repo, redisClient, metrics, BillingExecutorConfig{}, logger)

	schedule := scheduler.Schedule{
		ID:       "billing:tenant-create-err",
		CronExpr: "0 2 1 * *",
		TenantID: "tenant-create-err",
	}

	err := executor.Execute(context.Background(), schedule)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "persist billing run")
}

func TestNewBillingMetrics_DefaultConstructor(t *testing.T) {
	// NewBillingMetrics registers with the default prometheus registry.
	// We test that it doesn't panic and returns non-nil.
	// Note: This may cause "duplicate registration" if run alongside other tests
	// that also use the default registry, but promauto handles that gracefully.
	metrics := NewBillingMetrics()
	require.NotNil(t, metrics)

	// Verify all fields are initialized
	assert.NotNil(t, metrics.billingRunsTotal)
	assert.NotNil(t, metrics.billingInvoicesCreated)
	assert.NotNil(t, metrics.billingAmountCollected)
	assert.NotNil(t, metrics.billingSchedulerErrors)
	assert.NotNil(t, metrics.billingRunDuration)

	// Verify the metrics are functional
	metrics.RecordBillingRun("test")
	metrics.RecordInvoiceCreated()
	metrics.RecordAmountCollected(100)
	metrics.RecordError("test-error")
	metrics.ObserveRunDuration(1.5)
}
