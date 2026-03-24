package worker

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/scheduler"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// BillingScheduleProvider
// =============================================================================

// TestBillingScheduleProvider_ListSchedules_ReturnsCorrectFormat verifies the schedule
// ID format and that cron expression is passed through correctly.
func TestBillingScheduleProvider_ListSchedules_ReturnsCorrectFormat(t *testing.T) {
	t.Parallel()

	provider := NewBillingScheduleProvider("tenant-xyz", "0 2 1 * *")
	schedules, err := provider.ListSchedules(context.Background())

	require.NoError(t, err)
	require.Len(t, schedules, 1)

	s := schedules[0]
	assert.Equal(t, "billing:tenant-xyz", s.ID)
	assert.Equal(t, "0 2 1 * *", s.CronExpr)
	assert.Equal(t, "tenant-xyz", s.TenantID)
}

// TestBillingScheduleProvider_ListSchedules_AlwaysReturnsSingleSchedule verifies that
// exactly one schedule is returned regardless of context.
func TestBillingScheduleProvider_ListSchedules_AlwaysReturnsSingleSchedule(t *testing.T) {
	t.Parallel()

	provider := NewBillingScheduleProvider("t1", "*/5 * * * *")

	for i := 0; i < 3; i++ {
		schedules, err := provider.ListSchedules(context.Background())
		require.NoError(t, err)
		assert.Len(t, schedules, 1, "should always return exactly one schedule")
	}
}

// TestBillingScheduleProvider_ListSchedules_ImplementsInterface verifies compile-time
// interface satisfaction.
func TestBillingScheduleProvider_ListSchedules_ImplementsInterface(t *testing.T) {
	t.Parallel()

	var _ scheduler.ScheduleProvider = NewBillingScheduleProvider("t", "0 * * * *")
}

// =============================================================================
// BillingExecutor - constructor and configuration
// =============================================================================

// TestNewBillingExecutor_DefaultConfig verifies a minimal executor can be created.
func TestNewBillingExecutor_DefaultConfig(t *testing.T) {
	t.Parallel()

	repo := newMockBillingRepo()
	redisClient := setupMiniredis(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	metrics := NewBillingMetricsWithRegistry(prometheus.NewRegistry())

	executor := NewBillingExecutor(repo, redisClient, metrics, BillingExecutorConfig{}, logger)

	assert.NotNil(t, executor)
	assert.Nil(t, executor.invoiceGenerator, "invoiceGenerator should start nil")
	assert.Nil(t, executor.paymentInitiator, "paymentInitiator should start nil")
	assert.False(t, executor.config.ShadowMode)
}

// TestNewBillingExecutor_ShadowMode verifies shadow mode is stored on executor.
func TestNewBillingExecutor_ShadowMode(t *testing.T) {
	t.Parallel()

	repo := newMockBillingRepo()
	redisClient := setupMiniredis(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	metrics := NewBillingMetricsWithRegistry(prometheus.NewRegistry())

	executor := NewBillingExecutor(repo, redisClient, metrics, BillingExecutorConfig{ShadowMode: true}, logger)
	assert.True(t, executor.config.ShadowMode)
}

// =============================================================================
// BillingExecutor - idempotency via Redis
// =============================================================================

// TestBillingExecutor_Execute_SkipsIfDuplicateInRedis verifies that a billing run is
// skipped when the idempotency key already exists in Redis (from a previous execution).
// Not parallel: mutates the package-level NowFunc global.
func TestBillingExecutor_Execute_SkipsIfDuplicateInRedis(t *testing.T) {
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
	schedule := scheduler.Schedule{
		ID:       "billing:tenant-redis-skip",
		CronExpr: "0 2 1 * *",
		TenantID: "tenant-redis-skip",
	}

	// First execution should succeed and mark Redis key
	err := executor.Execute(context.Background(), schedule)
	require.NoError(t, err)

	// Second execution should detect the Redis key and skip
	err = executor.Execute(context.Background(), schedule)
	require.NoError(t, err)

	// Verify only one billing run was created in the repo
	assert.Len(t, repo.runs, 1)
}

// TestBillingExecutor_Execute_ImplementsInterface verifies compile-time interface check.
func TestBillingExecutor_Execute_ImplementsInterface(t *testing.T) {
	t.Parallel()

	repo := newMockBillingRepo()
	redisClient := setupMiniredis(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	metrics := NewBillingMetricsWithRegistry(prometheus.NewRegistry())

	var _ scheduler.Executor = NewBillingExecutor(repo, redisClient, metrics, BillingExecutorConfig{}, logger)
}
