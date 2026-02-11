package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"

	"github.com/meridianhub/meridian/services/forecasting/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// Scheduler errors.
var (
	ErrNilRepository    = errors.New("strategy repository is required")
	ErrNilExecutor      = errors.New("forecast executor is required")
	ErrNilLeaseManager  = errors.New("lease manager is required")
	ErrNilLogger        = errors.New("logger is required")
	ErrSchedulerRunning = errors.New("scheduler is already running")
)

// Default configuration values.
const (
	DefaultPollInterval    = 60 * time.Second
	DefaultShutdownTimeout = 5 * time.Minute
)

// ForecastResult holds the outcome of a forecast execution.
type ForecastResult struct {
	PointCount      int32
	StrategyVersion int64
}

// ForecastExecutor abstracts forecast execution for testability.
// In production, this wraps handler.Service.ComputeForwardCurve.
type ForecastExecutor interface {
	ExecuteForecast(ctx context.Context, strategyID uuid.UUID) (*ForecastResult, error)
}

// Config holds configuration for the CronScheduler.
type Config struct {
	// PollInterval is how often to check for strategy changes. Default: 60s.
	PollInterval time.Duration
	// ShutdownTimeout is the maximum time to wait for in-flight forecasts during shutdown.
	// Default: 5 minutes.
	ShutdownTimeout time.Duration
}

// CronScheduler loads active forecasting strategies and registers cron jobs
// for their scheduled execution. It coordinates with other pods via LeaseManager
// to prevent duplicate executions.
type CronScheduler struct {
	strategyRepo domain.StrategyRepository
	executor     ForecastExecutor
	leaseManager *LeaseManager
	metrics      *Metrics
	logger       *slog.Logger
	config       Config

	cron    *cron.Cron
	mu      sync.Mutex
	wg      sync.WaitGroup
	jobs    map[string]registeredJob // key: strategyID string
	running bool
	stopped bool
}

// registeredJob tracks a registered cron entry for a strategy.
type registeredJob struct {
	entryID  cron.EntryID
	schedule string
	tenantID string
}

// New creates a new CronScheduler.
func New(
	strategyRepo domain.StrategyRepository,
	executor ForecastExecutor,
	leaseManager *LeaseManager,
	metrics *Metrics,
	logger *slog.Logger,
	config Config,
) (*CronScheduler, error) {
	if strategyRepo == nil {
		return nil, ErrNilRepository
	}
	if executor == nil {
		return nil, ErrNilExecutor
	}
	if leaseManager == nil {
		return nil, ErrNilLeaseManager
	}
	if logger == nil {
		return nil, ErrNilLogger
	}
	if metrics == nil {
		metrics = NewMetrics()
	}
	if config.PollInterval == 0 {
		config.PollInterval = DefaultPollInterval
	}
	if config.ShutdownTimeout == 0 {
		config.ShutdownTimeout = DefaultShutdownTimeout
	}

	cronRunner := cron.New(
		cron.WithLocation(time.UTC),
	)

	return &CronScheduler{
		strategyRepo: strategyRepo,
		executor:     executor,
		leaseManager: leaseManager,
		metrics:      metrics,
		logger:       logger.With("component", "cron_scheduler"),
		config:       config,
		cron:         cronRunner,
		jobs:         make(map[string]registeredJob),
	}, nil
}

// Start loads active strategies, registers cron jobs, and blocks until the
// context is cancelled. It starts a background poller that checks for strategy
// changes every PollInterval.
func (s *CronScheduler) Start(ctx context.Context) error { //nolint:contextcheck // uses context.Background() intentionally for shutdown lease release
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return ErrSchedulerRunning
	}
	s.running = true
	s.mu.Unlock()

	s.logger.Info("cron scheduler starting", "poll_interval", s.config.PollInterval)

	// Initial strategy load
	if err := s.reloadStrategies(ctx); err != nil {
		s.logger.Error("initial strategy load failed", "error", err)
		// Continue running - poller will retry
	}

	s.cron.Start()
	s.logger.Info("cron scheduler started")

	// Start background poller for strategy changes
	pollDone := make(chan struct{})
	go func() {
		defer close(pollDone)
		s.pollLoop(ctx)
	}()

	// Block until context cancelled
	<-ctx.Done()
	s.logger.Info("cron scheduler stopping: context cancelled")

	s.mu.Lock()
	s.stopped = true
	s.mu.Unlock()

	// Stop cron runner (no new jobs will fire)
	cronCtx := s.cron.Stop()

	// Wait for poller to finish
	<-pollDone

	// Wait for in-flight jobs
	waitDone := make(chan struct{})
	go func() {
		<-cronCtx.Done()
		s.wg.Wait()
		close(waitDone)
	}()

	select {
	case <-waitDone:
		s.logger.Info("all in-flight forecasts completed")
	case <-time.After(s.config.ShutdownTimeout):
		s.logger.Warn("shutdown timeout reached, some forecasts may not have completed",
			"timeout", s.config.ShutdownTimeout)
	}

	// Release all leases - parent context is cancelled, so use a fresh context
	s.leaseManager.ReleaseAll(context.Background()) //nolint:contextcheck // parent ctx is cancelled at this point

	s.mu.Lock()
	s.running = false
	s.mu.Unlock()

	s.logger.Info("cron scheduler stopped")
	return nil
}

// pollLoop periodically reloads strategies from the database.
func (s *CronScheduler) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(s.config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.reloadStrategies(ctx); err != nil {
				s.logger.Error("strategy reload failed", "error", err)
				s.metrics.RecordReload("error")
			} else {
				s.metrics.RecordReload("success")
			}
		}
	}
}

// reloadStrategies fetches all active strategies and synchronizes cron jobs.
func (s *CronScheduler) reloadStrategies(ctx context.Context) error {
	strategies, err := s.strategyRepo.ListAllActive(ctx)
	if err != nil {
		s.metrics.RecordError("list_active_strategies")
		return err
	}

	// Build a map of desired state
	desired := make(map[string]desiredJob, len(strategies))
	for _, st := range strategies {
		desired[st.ID().String()] = desiredJob{
			strategyID: st.ID(),
			tenantID:   st.TenantID(),
			schedule:   st.Schedule(),
			name:       st.Name(),
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Remove jobs for strategies that are no longer active or have changed schedules
	for id, job := range s.jobs {
		d, exists := desired[id]
		if !exists || d.schedule != job.schedule {
			s.cron.Remove(job.entryID)
			delete(s.jobs, id)
			if !exists {
				s.logger.Info("removed cron job for deactivated strategy", "strategy_id", id)
			} else {
				s.logger.Info("removed cron job for schedule change", "strategy_id", id,
					"old_schedule", job.schedule, "new_schedule", d.schedule)
			}
		}
	}

	// Add jobs for new or re-scheduled strategies
	for id, d := range desired {
		if _, exists := s.jobs[id]; exists {
			continue
		}

		entryID, err := s.cron.AddFunc(d.schedule, s.makeJobFunc(d.strategyID, d.tenantID)) //nolint:contextcheck // cron.AddFunc callbacks have no context
		if err != nil {
			s.logger.Error("failed to register cron job",
				"strategy_id", id,
				"schedule", d.schedule,
				"error", err)
			s.metrics.RecordError("register_cron_job")
			continue
		}

		s.jobs[id] = registeredJob{
			entryID:  entryID,
			schedule: d.schedule,
			tenantID: d.tenantID,
		}
		s.logger.Info("registered cron job",
			"strategy_id", id,
			"strategy_name", d.name,
			"schedule", d.schedule,
			"tenant_id", d.tenantID)
	}

	s.metrics.SetActiveStrategies(float64(len(s.jobs)))
	return nil
}

// desiredJob holds the target state for a strategy's cron job.
type desiredJob struct {
	strategyID uuid.UUID
	tenantID   string
	schedule   string
	name       string
}

// makeJobFunc creates the function that the cron scheduler calls for a strategy.
func (s *CronScheduler) makeJobFunc(strategyID uuid.UUID, tenantID string) func() {
	return func() {
		s.executeStrategy(strategyID, tenantID)
	}
}

// executeStrategy attempts to acquire the lease and execute the forecast.
// Called by cron runner which does not provide a context.
func (s *CronScheduler) executeStrategy(strategyID uuid.UUID, tenantID string) { //nolint:contextcheck // cron callbacks have no parent context
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.wg.Add(1)
	s.mu.Unlock()
	defer s.wg.Done()

	ctx := context.Background()
	ctx = tenant.WithTenant(ctx, tenant.TenantID(tenantID))
	sid := strategyID.String()

	start := time.Now()

	s.logger.Info("executing scheduled forecast",
		"strategy_id", sid,
		"tenant_id", tenantID)

	// Acquire lease to prevent duplicate execution across pods
	acquired, err := s.leaseManager.Acquire(ctx, tenantID, sid)
	if err != nil {
		s.logger.Error("lease acquisition error",
			"strategy_id", sid,
			"tenant_id", tenantID,
			"error", err)
		s.metrics.RecordLeaseFailure("error")
		s.metrics.RecordExecution(tenantID, sid, "lease_error")
		return
	}
	if !acquired {
		s.logger.Debug("lease not acquired, another pod is executing",
			"strategy_id", sid,
			"tenant_id", tenantID)
		s.metrics.RecordLeaseFailure("contention")
		s.metrics.RecordExecution(tenantID, sid, "skipped")
		return
	}
	defer func() {
		if err := s.leaseManager.Release(ctx, tenantID, sid); err != nil {
			s.logger.Warn("failed to release lease after execution",
				"strategy_id", sid,
				"error", err)
		}
	}()

	// Execute the forecast
	result, err := s.executor.ExecuteForecast(ctx, strategyID)
	elapsed := time.Since(start)

	if err != nil {
		s.logger.Error("scheduled forecast execution failed",
			"strategy_id", sid,
			"tenant_id", tenantID,
			"duration", elapsed,
			"error", err)
		s.metrics.RecordExecution(tenantID, sid, "error")
		s.metrics.ObserveExecutionDuration(tenantID, sid, elapsed.Seconds())
		return
	}

	s.logger.Info("scheduled forecast completed",
		"strategy_id", sid,
		"tenant_id", tenantID,
		"point_count", result.PointCount,
		"strategy_version", result.StrategyVersion,
		"duration", elapsed)
	s.metrics.RecordExecution(tenantID, sid, "success")
	s.metrics.ObserveExecutionDuration(tenantID, sid, elapsed.Seconds())
}

// RegisteredJobCount returns the number of currently registered cron jobs.
func (s *CronScheduler) RegisteredJobCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.jobs)
}
