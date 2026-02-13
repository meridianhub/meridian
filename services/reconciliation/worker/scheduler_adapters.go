package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/meridianhub/meridian/shared/platform/scheduler"
)

// settlementScheduleProvider adapts the reconciliation service's ReferenceDataClient
// to the shared scheduler.ScheduleProvider interface.
type settlementScheduleProvider struct {
	refDataClient ReferenceDataClient
}

// NewSettlementScheduleProvider creates a ScheduleProvider backed by the reference data client.
func NewSettlementScheduleProvider(refDataClient ReferenceDataClient) scheduler.ScheduleProvider {
	return &settlementScheduleProvider{refDataClient: refDataClient}
}

func (p *settlementScheduleProvider) ListSchedules(ctx context.Context) ([]scheduler.Schedule, error) {
	settlements, err := p.refDataClient.ListSettlementSchedules(ctx)
	if err != nil {
		return nil, fmt.Errorf("list settlement schedules: %w", err)
	}

	schedules := make([]scheduler.Schedule, len(settlements))
	for i, s := range settlements {
		schedules[i] = scheduler.Schedule{
			ID:       s.ScheduleID,
			CronExpr: s.CronExpression,
			Metadata: s,
		}
	}
	return schedules, nil
}

// settlementExecutor adapts the reconciliation service's execution logic to
// the shared scheduler.Executor interface. When the shared scheduler fires a
// schedule, this executor calculates the period window and initiates a
// reconciliation run via the ReconciliationClient.
type settlementExecutor struct {
	reconClient ReconciliationClient
	metrics     *SchedulerMetrics
	logger      *slog.Logger
}

// NewSettlementExecutor creates an Executor that triggers reconciliation runs.
func NewSettlementExecutor(reconClient ReconciliationClient, metrics *SchedulerMetrics, logger *slog.Logger) scheduler.Executor {
	if metrics == nil {
		metrics = NewSchedulerMetrics()
	}
	return &settlementExecutor{
		reconClient: reconClient,
		metrics:     metrics,
		logger:      logger.With("component", "settlement_executor"),
	}
}

func (e *settlementExecutor) Execute(ctx context.Context, schedule scheduler.Schedule) error {
	sched, ok := schedule.Metadata.(SettlementSchedule)
	if !ok {
		return fmt.Errorf("%w: got %T", ErrUnexpectedMetadata, schedule.Metadata)
	}

	now := time.Now().UTC()
	periodStart, periodEnd := CalculatePeriod(now, sched.SettlementType, sched.PeriodOffset)

	e.logger.Info("executing scheduled reconciliation",
		"schedule_id", sched.ScheduleID,
		"account_id", sched.AccountID,
		"asset_type", sched.AssetType,
		"period_start", periodStart,
		"period_end", periodEnd)

	runID, err := e.reconClient.InitiateReconciliation(ctx, InitiateRequest{
		AccountID:      sched.AccountID,
		Scope:          sched.Scope,
		SettlementType: sched.SettlementType,
		PeriodStart:    periodStart,
		PeriodEnd:      periodEnd,
		InitiatedBy:    "settlement-scheduler",
	})
	if err != nil {
		if errors.Is(err, ErrRunAlreadyExists) {
			e.logger.Info("reconciliation run already exists, skipping",
				"schedule_id", sched.ScheduleID,
				"account_id", sched.AccountID,
				"period_start", periodStart,
				"period_end", periodEnd)
			return nil
		}
		e.metrics.RecordError("initiate_reconciliation")
		return fmt.Errorf("initiate reconciliation for %s: %w", sched.ScheduleID, err)
	}

	e.logger.Info("scheduled reconciliation initiated",
		"schedule_id", sched.ScheduleID,
		"run_id", runID,
		"account_id", sched.AccountID,
		"period_start", periodStart,
		"period_end", periodEnd)

	e.metrics.RecordRunTriggered(sched.AssetType)
	return nil
}
