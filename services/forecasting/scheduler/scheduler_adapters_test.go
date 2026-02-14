package scheduler_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/forecasting/domain"
	"github.com/meridianhub/meridian/services/forecasting/handler"
	"github.com/meridianhub/meridian/services/forecasting/scheduler"
	sharedscheduler "github.com/meridianhub/meridian/shared/platform/scheduler"
)

// --- Test Helpers ---

func testMetrics(t *testing.T) *scheduler.Metrics {
	t.Helper()
	reg := prometheus.NewRegistry()
	return scheduler.NewMetricsWithRegistry(reg)
}

// --- Mock Strategy Repository ---

type mockStrategyRepo struct {
	mu         sync.Mutex
	strategies []domain.ForecastingStrategy
	err        error
}

func (m *mockStrategyRepo) Save(_ context.Context, _ domain.ForecastingStrategy) error {
	return nil
}

func (m *mockStrategyRepo) FindByID(_ context.Context, id uuid.UUID) (domain.ForecastingStrategy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.strategies {
		if s.ID() == id {
			return s, nil
		}
	}
	return domain.ForecastingStrategy{}, domain.ErrStrategyNotFound
}

func (m *mockStrategyRepo) FindByTenantAndName(_ context.Context, _ string, _ string) (domain.ForecastingStrategy, error) {
	return domain.ForecastingStrategy{}, domain.ErrStrategyNotFound
}

func (m *mockStrategyRepo) ListByTenant(_ context.Context, _ string, _ domain.StrategyFilters) ([]domain.ForecastingStrategy, string, error) {
	return nil, "", nil
}

func (m *mockStrategyRepo) ListAllActive(_ context.Context) ([]domain.ForecastingStrategy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	result := make([]domain.ForecastingStrategy, len(m.strategies))
	copy(result, m.strategies)
	return result, nil
}

// --- Test Strategies ---

func newActiveStrategy(t *testing.T, tenantID, name, schedule string) domain.ForecastingStrategy {
	t.Helper()
	s, err := domain.NewForecastingStrategy(
		tenantID,
		name,
		"test strategy",
		"result = [{'timestamp': '2026-01-01T00:00:00Z', 'value': '100.0'}]",
		24,
		1,
		schedule,
		[]string{"DATASET_A"},
		"OUTPUT_DATASET",
		"",
	)
	require.NoError(t, err)
	s, err = s.Activate()
	require.NoError(t, err)
	return s
}

// --- ForecastScheduleProvider Tests ---

func TestForecastScheduleProvider_ListSchedules(t *testing.T) {
	t.Run("returns schedules from active strategies", func(t *testing.T) {
		s1 := newActiveStrategy(t, "tenant-1", "strategy-a", "0 * * * *")
		s2 := newActiveStrategy(t, "tenant-2", "strategy-b", "30 8 * * *")
		repo := &mockStrategyRepo{strategies: []domain.ForecastingStrategy{s1, s2}}

		provider := scheduler.NewForecastScheduleProvider(repo)
		schedules, err := provider.ListSchedules(context.Background())

		require.NoError(t, err)
		require.Len(t, schedules, 2)

		assert.Equal(t, s1.ID().String(), schedules[0].ID)
		assert.Equal(t, "0 * * * *", schedules[0].CronExpr)
		assert.Equal(t, "tenant-1", schedules[0].TenantID)
		assert.Equal(t, s1.ID(), schedules[0].Metadata)

		assert.Equal(t, s2.ID().String(), schedules[1].ID)
		assert.Equal(t, "30 8 * * *", schedules[1].CronExpr)
		assert.Equal(t, "tenant-2", schedules[1].TenantID)
		assert.Equal(t, s2.ID(), schedules[1].Metadata)
	})

	t.Run("returns empty slice when no active strategies", func(t *testing.T) {
		repo := &mockStrategyRepo{}
		provider := scheduler.NewForecastScheduleProvider(repo)
		schedules, err := provider.ListSchedules(context.Background())

		require.NoError(t, err)
		assert.Empty(t, schedules)
	})

	t.Run("propagates repository errors", func(t *testing.T) {
		repo := &mockStrategyRepo{err: errors.New("database unavailable")}
		provider := scheduler.NewForecastScheduleProvider(repo)
		_, err := provider.ListSchedules(context.Background())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "database unavailable")
	})
}

// --- ForecastScheduleExecutor Tests ---

func TestForecastScheduleExecutor_Execute(t *testing.T) {
	t.Run("executes forecast for strategy UUID in metadata", func(t *testing.T) {
		strategyID := uuid.New()
		var calledWith uuid.UUID

		executor := scheduler.NewForecastScheduleExecutor(
			func(_ context.Context, id uuid.UUID) (*handler.ScheduledExecutionResult, error) {
				calledWith = id
				return &handler.ScheduledExecutionResult{PointCount: 24, StrategyVersion: 1}, nil
			},
			testMetrics(t),
		)

		schedule := sharedscheduler.Schedule{
			ID:       strategyID.String(),
			CronExpr: "0 * * * *",
			TenantID: "tenant-1",
			Metadata: strategyID,
		}

		err := executor.Execute(context.Background(), schedule)
		require.NoError(t, err)
		assert.Equal(t, strategyID, calledWith)
	})

	t.Run("returns error when metadata is not UUID", func(t *testing.T) {
		executor := scheduler.NewForecastScheduleExecutor(
			func(_ context.Context, _ uuid.UUID) (*handler.ScheduledExecutionResult, error) {
				t.Fatal("should not be called")
				return nil, nil
			},
			nil,
		)

		schedule := sharedscheduler.Schedule{
			ID:       "bad-schedule",
			Metadata: "not-a-uuid",
		}

		err := executor.Execute(context.Background(), schedule)
		require.Error(t, err)
		assert.ErrorIs(t, err, scheduler.ErrInvalidMetadata)
	})

	t.Run("propagates executor errors", func(t *testing.T) {
		strategyID := uuid.New()
		executor := scheduler.NewForecastScheduleExecutor(
			func(_ context.Context, _ uuid.UUID) (*handler.ScheduledExecutionResult, error) {
				return nil, errors.New("starlark execution failed")
			},
			testMetrics(t),
		)

		schedule := sharedscheduler.Schedule{
			ID:       strategyID.String(),
			TenantID: "tenant-1",
			Metadata: strategyID,
		}

		err := executor.Execute(context.Background(), schedule)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "starlark execution failed")
	})

	t.Run("works without metrics", func(t *testing.T) {
		strategyID := uuid.New()
		executor := scheduler.NewForecastScheduleExecutor(
			func(_ context.Context, _ uuid.UUID) (*handler.ScheduledExecutionResult, error) {
				return &handler.ScheduledExecutionResult{PointCount: 10}, nil
			},
			nil,
		)

		schedule := sharedscheduler.Schedule{
			ID:       strategyID.String(),
			Metadata: strategyID,
		}

		err := executor.Execute(context.Background(), schedule)
		require.NoError(t, err)
	})
}

// --- Metrics Tests ---

func TestMetrics_Creation(t *testing.T) {
	t.Run("default registry", func(t *testing.T) {
		m := scheduler.NewMetrics()
		assert.NotNil(t, m)
	})

	t.Run("custom registry", func(t *testing.T) {
		reg := prometheus.NewRegistry()
		m := scheduler.NewMetricsWithRegistry(reg)
		assert.NotNil(t, m)

		m.RecordExecution("tenant-1", "strategy-1", "success")
		m.ObserveExecutionDuration("tenant-1", "strategy-1", 1.5)
		m.RecordLeaseFailure("contention")
		m.SetActiveStrategies(3)
		m.RecordReload("success")
		m.RecordError("test_error")

		families, err := reg.Gather()
		require.NoError(t, err)
		assert.NotEmpty(t, families)
	})
}
