package scheduler

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/meridianhub/meridian/services/forecasting/domain"
	"github.com/meridianhub/meridian/services/forecasting/handler"
	"github.com/meridianhub/meridian/shared/platform/scheduler"
)

// ErrInvalidMetadata is returned when a schedule's Metadata field does not
// contain the expected uuid.UUID type.
var ErrInvalidMetadata = errors.New("schedule metadata is not a uuid.UUID")

// ForecastScheduleProvider adapts the domain StrategyRepository to the shared
// scheduler.ScheduleProvider interface. It queries for active forecasting
// strategies and maps them to scheduler.Schedule entries.
type ForecastScheduleProvider struct {
	repo domain.StrategyRepository
}

// NewForecastScheduleProvider creates a new ForecastScheduleProvider.
func NewForecastScheduleProvider(repo domain.StrategyRepository) *ForecastScheduleProvider {
	return &ForecastScheduleProvider{repo: repo}
}

// ListSchedules returns all active forecasting strategies as scheduler.Schedule entries.
func (p *ForecastScheduleProvider) ListSchedules(ctx context.Context) ([]scheduler.Schedule, error) {
	strategies, err := p.repo.ListAllActive(ctx)
	if err != nil {
		return nil, fmt.Errorf("list active strategies: %w", err)
	}

	schedules := make([]scheduler.Schedule, len(strategies))
	for i, s := range strategies {
		schedules[i] = scheduler.Schedule{
			ID:       s.ID().String(),
			CronExpr: s.Schedule(),
			TenantID: s.TenantID(),
			Metadata: s.ID(),
		}
	}
	return schedules, nil
}

// ForecastScheduleExecutor adapts the handler's internal compute method to the
// shared scheduler.Executor interface. It extracts the strategy UUID from
// the schedule metadata and delegates to the handler.
type ForecastScheduleExecutor struct {
	executor ForecastExecutorFunc
	metrics  *Metrics
}

// ForecastExecutorFunc defines the function signature for executing a forecast
// by strategy ID. This matches handler.Service.ComputeForwardCurveInternal.
type ForecastExecutorFunc func(ctx context.Context, strategyID uuid.UUID) (*handler.ScheduledExecutionResult, error)

// NewForecastScheduleExecutor creates a new ForecastScheduleExecutor adapter.
func NewForecastScheduleExecutor(executor ForecastExecutorFunc, metrics *Metrics) *ForecastScheduleExecutor {
	return &ForecastScheduleExecutor{
		executor: executor,
		metrics:  metrics,
	}
}

// Execute runs the forecast for the given schedule. The schedule's Metadata
// field is expected to contain the strategy UUID.
func (e *ForecastScheduleExecutor) Execute(ctx context.Context, schedule scheduler.Schedule) error {
	strategyID, ok := schedule.Metadata.(uuid.UUID)
	if !ok {
		return fmt.Errorf("schedule %s: %w", schedule.ID, ErrInvalidMetadata)
	}

	result, err := e.executor(ctx, strategyID)
	if err != nil {
		if e.metrics != nil {
			e.metrics.RecordExecution(schedule.TenantID, schedule.ID, "error")
		}
		return fmt.Errorf("execute forecast for strategy %s: %w", strategyID, err)
	}

	if e.metrics != nil {
		e.metrics.RecordExecution(schedule.TenantID, schedule.ID, "success")
	}

	_ = result // result is logged by the underlying executor
	return nil
}
