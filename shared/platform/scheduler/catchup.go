package scheduler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// runCatchUp detects and re-executes missed cron windows since the last known
// execution for each schedule. It runs once on startup, before the live cron
// runner begins. Only the leader pod executes catch-up (via distributed lock).
//
// For each schedule:
//   - Windows within MaxCatchUpAge are executed.
//   - Windows older than MaxCatchUpAge are recorded as MISSED (audit trail only).
//   - If no ExecutionStore is configured, catch-up is skipped entirely.
func (s *CronScheduler) runCatchUp(ctx context.Context) {
	if s.store == nil {
		return
	}

	// Acquire a dedicated catch-up lock to prevent multiple pods from running catch-up.
	if s.lock != nil {
		lockKey := fmt.Sprintf("%s:catch-up", s.config.Name)
		acquired, release, err := s.lock.Acquire(ctx, "", lockKey)
		if err != nil {
			s.logger.Error("failed to acquire catch-up lock", "error", err)
			return
		}
		if !acquired {
			s.logger.Info("catch-up lock not acquired, another pod is handling catch-up")
			return
		}
		defer release()
	}

	s.mu.Lock()
	schedules := make([]Schedule, 0, len(s.schedules))
	for _, sched := range s.schedules {
		schedules = append(schedules, sched)
	}
	s.mu.Unlock()

	if len(schedules) == 0 {
		return
	}

	s.logger.Info("starting catch-up for missed windows",
		"schedule_count", len(schedules),
		"max_catch_up_age", s.config.MaxCatchUpAge)

	now := time.Now().UTC()
	catchUpCutoff := now.Add(-s.config.MaxCatchUpAge)

	for _, sched := range schedules {
		if ctx.Err() != nil {
			return
		}
		s.catchUpSchedule(ctx, sched, now, catchUpCutoff)
	}

	s.logger.Info("catch-up completed")
}

// catchUpSchedule processes catch-up for a single schedule.
func (s *CronScheduler) catchUpSchedule(ctx context.Context, sched Schedule, now, catchUpCutoff time.Time) {
	// Set tenant context for store operations.
	schedCtx := ctx
	if sched.TenantID != "" {
		if tid, err := tenant.NewTenantID(sched.TenantID); err == nil {
			schedCtx = tenant.WithTenant(ctx, tid)
		}
	}

	// Inject Actor so downstream audit records attribute the operation correctly
	schedCtx = auth.WithActor(schedCtx, auth.Actor{
		ID:            fmt.Sprintf("system:scheduler:%s", s.config.Name),
		Type:          auth.ActorTypeScheduler,
		Authenticated: false,
		Source:        "catch-up",
	})

	// Determine start point for walking cron windows.
	// - If we have a last execution, start from there (to record MISSED for old windows).
	// - If no prior execution, start from the catch-up cutoff (only execute recent windows).
	lastExec, err := s.store.LastExecution(schedCtx, s.config.Name, sched.ID)
	if err != nil && !errors.Is(err, ErrNoExecution) {
		s.logger.Error("failed to query last execution for catch-up",
			"schedule_id", sched.ID,
			"error", err)
		return
	}

	startFrom := catchUpCutoff
	if lastExec != nil {
		startFrom = lastExec.ScheduledAt
	}

	// Parse the cron expression to walk forward through time.
	cronSched, err := s.parser.Parse(sched.CronExpr)
	if err != nil {
		s.logger.Error("failed to parse cron expression for catch-up",
			"schedule_id", sched.ID,
			"cron_expr", sched.CronExpr,
			"error", err)
		return
	}

	// Walk the cron expression forward from startFrom to now.
	var missedCount, executedCount int
	nextTime := cronSched.Next(startFrom)

	for !nextTime.After(now) {
		if ctx.Err() != nil {
			return
		}

		if nextTime.Before(catchUpCutoff) {
			// Too old to execute: record as MISSED for audit trail.
			s.recordMissedWindow(schedCtx, sched, nextTime)
			missedCount++
		} else if s.catchUpWindowEligible(schedCtx, sched, nextTime) {
			// Within catch-up age and tenant is active: execute.
			s.executeCatchUpWindow(schedCtx, sched, nextTime)
			executedCount++
		}

		nextTime = cronSched.Next(nextTime)
	}

	if missedCount > 0 || executedCount > 0 {
		s.logger.Info("catch-up processed schedule",
			"schedule_id", sched.ID,
			"executed", executedCount,
			"missed", missedCount)
	}
}

// executeCatchUpWindow executes a single missed window, acquiring the per-schedule
// distributed lock (like normal executeJob) to prevent duplicates.
//
// Unlike executeJob, this does not use lifecycle.ExecuteGuarded because catch-up
// runs synchronously inside Start() before cron.Start(). The Start goroutine
// itself blocks until catch-up completes or the context is cancelled, so the
// lifecycle already knows work is in progress. Context cancellation (from Stop)
// is checked between iterations in catchUpSchedule.
// catchUpWindowEligible checks tenant status before executing a catch-up window.
// Unlike tenantIsEligible used in executeJob, this records the skipped execution
// with the actual catch-up window timestamp rather than time.Now().
func (s *CronScheduler) catchUpWindowEligible(ctx context.Context, sched Schedule, scheduledAt time.Time) bool {
	if s.statusChecker == nil || sched.TenantID == "" {
		return true
	}
	active, err := s.statusChecker.IsActive(ctx, sched.TenantID)
	if err != nil {
		s.logger.Error("failed to check tenant status for catch-up",
			"schedule_id", sched.ID,
			"error", err)
		return true // fail open
	}
	if !active {
		s.logger.Info("skipping catch-up window for inactive tenant",
			"schedule_id", sched.ID,
			"tenant_id", sched.TenantID,
			"scheduled_at", scheduledAt)
		if s.store != nil {
			exec := Execution{
				ID:            uuid.New(),
				SchedulerName: s.config.Name,
				ScheduleID:    sched.ID,
				ScheduledAt:   scheduledAt,
				Status:        ExecutionStatusSkipped,
				ErrorMessage:  strPtr("tenant not active"),
			}
			if err := s.store.RecordExecution(ctx, exec); err != nil {
				s.logger.Error("failed to record catch-up skipped execution",
					"schedule_id", sched.ID,
					"error", err)
			}
		}
		return false
	}
	return true
}

func (s *CronScheduler) executeCatchUpWindow(ctx context.Context, sched Schedule, scheduledAt time.Time) {
	// Inject correlation ID for this catch-up window
	ctx = audit.WithCorrelationID(ctx, uuid.New().String())

	// Acquire per-schedule lock (consistent with normal executeJob).
	if s.lock != nil {
		lockKey := s.lockKey(sched.ID)
		acquired, release, err := s.lock.Acquire(ctx, sched.TenantID, lockKey)
		if err != nil {
			s.logger.Error("failed to acquire lock for catch-up execution",
				"schedule_id", sched.ID,
				"scheduled_at", scheduledAt,
				"error", err)
			return
		}
		if !acquired {
			s.logger.Debug("lock not acquired for catch-up execution, skipping",
				"schedule_id", sched.ID,
				"scheduled_at", scheduledAt)
			return
		}
		defer release()
	}

	execID := uuid.New()
	s.recordExecutionStart(ctx, execID, sched, scheduledAt)

	execCtx, cancel := context.WithTimeout(ctx, s.config.ExecutionTimeout)
	defer cancel()

	err := s.executor.Execute(execCtx, sched)
	s.recordExecutionResult(ctx, execID, sched, err)

	if err != nil {
		s.logger.Error("catch-up execution failed",
			"schedule_id", sched.ID,
			"scheduled_at", scheduledAt,
			"error", err)
		return
	}

	s.logger.Info("catch-up execution completed",
		"schedule_id", sched.ID,
		"scheduled_at", scheduledAt)
}

// recordMissedWindow records a MISSED execution for audit trail without executing.
func (s *CronScheduler) recordMissedWindow(ctx context.Context, sched Schedule, scheduledAt time.Time) {
	exec := Execution{
		ID:            uuid.New(),
		SchedulerName: s.config.Name,
		ScheduleID:    sched.ID,
		ScheduledAt:   scheduledAt,
		Status:        ExecutionStatusMissed,
		ErrorMessage:  strPtr("window older than max catch-up age"),
	}
	if err := s.store.RecordExecution(ctx, exec); err != nil {
		s.logger.Error("failed to record missed window",
			"schedule_id", sched.ID,
			"scheduled_at", scheduledAt,
			"error", err)
	}
}
