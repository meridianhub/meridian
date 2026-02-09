// Package worker provides background workers for the reconciliation service.
package worker

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

// SettlementSchedule represents a schedule record from Reference Data.
type SettlementSchedule struct {
	// ScheduleID uniquely identifies this schedule.
	ScheduleID string
	// AssetType is the instrument/asset type this schedule applies to.
	AssetType string
	// AccountID is the account to reconcile.
	AccountID string
	// CronExpression is the cron schedule (e.g., "0 2 * * *" for 2 AM daily).
	CronExpression string
	// SettlementType is the type of settlement (DAILY, WEEKLY, etc.).
	SettlementType string
	// Scope is the reconciliation scope (ACCOUNT, INSTRUMENT, etc.).
	Scope string
	// PeriodOffset is how far back from current time the period starts.
	PeriodOffset time.Duration
}

// ReferenceDataClient provides access to settlement schedule configuration.
type ReferenceDataClient interface {
	// ListSettlementSchedules retrieves all active settlement schedules.
	ListSettlementSchedules(ctx context.Context) ([]SettlementSchedule, error)
}

// ReconciliationClient initiates reconciliation runs via gRPC.
type ReconciliationClient interface {
	// InitiateReconciliation creates and starts a new settlement run.
	// Returns the run ID on success. Returns an error wrapping ErrRunAlreadyExists
	// if a run already exists for this account/period combination.
	InitiateReconciliation(ctx context.Context, req InitiateRequest) (string, error)
}

// InitiateRequest contains the parameters for initiating a reconciliation run.
type InitiateRequest struct {
	AccountID      string
	Scope          string
	SettlementType string
	PeriodStart    time.Time
	PeriodEnd      time.Time
	InitiatedBy    string
}

// LeaderElector provides distributed leader election.
type LeaderElector interface {
	// TryAcquire attempts to acquire or renew the leader lock.
	// Returns true if this instance is the leader.
	TryAcquire(ctx context.Context) (bool, error)
	// Release releases the leader lock.
	Release(ctx context.Context) error
	// IsLeader returns whether this instance currently holds the leader lock.
	IsLeader() bool
}

// SchedulerConfig holds configuration for the settlement scheduler.
type SchedulerConfig struct {
	// PollInterval is how often to refresh schedules from Reference Data.
	PollInterval time.Duration
	// ShutdownTimeout is the maximum time to wait for in-flight jobs.
	ShutdownTimeout time.Duration
}

// SettlementScheduler is a background worker that triggers reconciliation runs
// based on cron schedules from Reference Data. It uses leader election to ensure
// only one instance runs scheduled jobs across the cluster.
type SettlementScheduler struct {
	refDataClient ReferenceDataClient
	reconClient   ReconciliationClient
	leader        LeaderElector
	config        SchedulerConfig
	logger        *slog.Logger
	metrics       *SchedulerMetrics

	cron     *cron.Cron
	done     chan struct{}
	wg       sync.WaitGroup
	mu       sync.Mutex
	running  bool
	stopped  bool
	entryIDs map[string]cron.EntryID // scheduleID -> cron entry
}

// NewSettlementScheduler creates a new settlement scheduler.
func NewSettlementScheduler(
	refDataClient ReferenceDataClient,
	reconClient ReconciliationClient,
	leader LeaderElector,
	config SchedulerConfig,
	logger *slog.Logger,
	metrics *SchedulerMetrics,
) (*SettlementScheduler, error) {
	if refDataClient == nil {
		return nil, ErrNilRefDataClient
	}
	if reconClient == nil {
		return nil, ErrNilReconClient
	}
	if leader == nil {
		return nil, ErrNilLeaderElector
	}
	if logger == nil {
		return nil, ErrNilLogger
	}
	if config.PollInterval <= 0 {
		return nil, ErrInvalidPollInterval
	}
	if config.ShutdownTimeout <= 0 {
		return nil, ErrInvalidShutdownTimeout
	}
	if metrics == nil {
		metrics = NewSchedulerMetrics()
	}

	cronRunner := cron.New(
		cron.WithLocation(time.UTC),
		cron.WithParser(cron.NewParser(
			cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow,
		)),
	)

	return &SettlementScheduler{
		refDataClient: refDataClient,
		reconClient:   reconClient,
		leader:        leader,
		config:        config,
		logger:        logger.With("component", "settlement_scheduler"),
		metrics:       metrics,
		cron:          cronRunner,
		done:          make(chan struct{}),
		entryIDs:      make(map[string]cron.EntryID),
	}, nil
}

// Start begins the scheduler loop. It loads schedules from Reference Data,
// registers cron jobs, and periodically refreshes the schedule list.
// This method blocks until the context is cancelled or Stop() is called.
func (s *SettlementScheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return ErrAlreadyRunning
	}
	s.running = true
	s.mu.Unlock()

	s.logger.Info("settlement scheduler starting",
		"poll_interval", s.config.PollInterval,
		"shutdown_timeout", s.config.ShutdownTimeout)

	// Load initial schedules
	if err := s.refreshSchedules(ctx); err != nil {
		s.logger.Error("failed to load initial schedules", "error", err)
		// Continue running - will retry on next poll interval
	}

	// Start the cron runner
	s.cron.Start()

	// Start the refresh loop
	refreshTicker := time.NewTicker(s.config.PollInterval)
	defer refreshTicker.Stop()

	s.logger.Info("settlement scheduler started")

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("settlement scheduler stopping: context cancelled")
			s.markStopped()
			return nil
		case <-s.done:
			s.logger.Info("settlement scheduler stopping: explicit shutdown")
			s.markStopped()
			return nil
		case <-refreshTicker.C:
			if err := s.refreshSchedules(ctx); err != nil {
				s.logger.Error("failed to refresh schedules", "error", err)
				s.metrics.RecordError("schedule_refresh")
			}
		}
	}
}

// Stop signals the scheduler to shut down gracefully. It waits for in-flight
// jobs to complete up to the configured shutdown timeout.
func (s *SettlementScheduler) Stop() {
	s.mu.Lock()
	alreadyStopped := s.stopped
	s.stopped = true
	s.mu.Unlock()

	if !alreadyStopped {
		select {
		case <-s.done:
		default:
			close(s.done)
		}
	}

	// Stop the cron scheduler (waits for running jobs to finish)
	cronCtx := s.cron.Stop()

	// Wait for in-flight jobs with timeout
	waitDone := make(chan struct{})
	go func() {
		<-cronCtx.Done()
		s.wg.Wait()
		close(waitDone)
	}()

	select {
	case <-waitDone:
		s.logger.Info("settlement scheduler shutdown complete")
	case <-time.After(s.config.ShutdownTimeout):
		s.logger.Warn("settlement scheduler shutdown timeout, some jobs may still be running",
			"timeout", s.config.ShutdownTimeout)
	}

	// Release leader lock
	releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.leader.Release(releaseCtx); err != nil {
		s.logger.Error("failed to release leader lock", "error", err)
	}
}

// markStopped safely marks the scheduler as not running.
func (s *SettlementScheduler) markStopped() {
	s.mu.Lock()
	s.running = false
	s.mu.Unlock()
}

// refreshSchedules queries Reference Data for current settlement schedules
// and updates the cron runner to match.
func (s *SettlementScheduler) refreshSchedules(ctx context.Context) error {
	start := time.Now()
	defer func() {
		s.metrics.ObserveRefreshDuration(time.Since(start).Seconds())
	}()

	schedules, err := s.refDataClient.ListSettlementSchedules(ctx)
	if err != nil {
		return fmt.Errorf("failed to list settlement schedules: %w", err)
	}

	s.logger.Info("loaded settlement schedules", "count", len(schedules))

	// Build map of current schedules
	currentSchedules := make(map[string]SettlementSchedule, len(schedules))
	for _, sched := range schedules {
		currentSchedules[sched.ScheduleID] = sched
	}

	// Synchronize access to entryIDs map
	s.mu.Lock()
	defer s.mu.Unlock()

	// Remove schedules that no longer exist
	for id, entryID := range s.entryIDs {
		if _, exists := currentSchedules[id]; !exists {
			s.cron.Remove(entryID)
			delete(s.entryIDs, id)
			s.logger.Info("removed schedule", "schedule_id", id)
		}
	}

	// Add or update schedules
	for _, sched := range schedules {
		if _, exists := s.entryIDs[sched.ScheduleID]; exists {
			// Schedule already registered, skip for now.
			// A more sophisticated approach would compare cron expressions
			// and re-register if changed, but for the initial implementation
			// the refresh will catch changes on the next poll cycle after restart.
			continue
		}

		entryID, err := s.addSchedule(sched) //nolint:contextcheck // cron.AddFunc takes func() with no context
		if err != nil {
			s.logger.Error("failed to add schedule",
				"schedule_id", sched.ScheduleID,
				"cron", sched.CronExpression,
				"error", err)
			s.metrics.RecordError("cron_parse")
			continue
		}

		s.entryIDs[sched.ScheduleID] = entryID
		s.logger.Info("registered schedule",
			"schedule_id", sched.ScheduleID,
			"asset_type", sched.AssetType,
			"account_id", sched.AccountID,
			"cron", sched.CronExpression,
			"settlement_type", sched.SettlementType)
	}

	return nil
}

// addSchedule registers a single settlement schedule with the cron runner.
func (s *SettlementScheduler) addSchedule(sched SettlementSchedule) (cron.EntryID, error) {
	// Capture schedule in closure
	schedule := sched

	entryID, err := s.cron.AddFunc(schedule.CronExpression, func() {
		s.executeJob(schedule)
	})
	if err != nil {
		return 0, fmt.Errorf("invalid cron expression %q: %w", schedule.CronExpression, err)
	}

	return entryID, nil
}

// executeJob runs a single scheduled reconciliation job.
func (s *SettlementScheduler) executeJob(schedule SettlementSchedule) {
	// Check if we should proceed (not stopped)
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.wg.Add(1)
	s.mu.Unlock()
	defer s.wg.Done()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Only execute if we're the leader
	isLeader, err := s.leader.TryAcquire(ctx)
	if err != nil {
		s.logger.Error("leader election check failed",
			"schedule_id", schedule.ScheduleID,
			"error", err)
		s.metrics.RecordError("leader_election")
		return
	}
	if !isLeader {
		s.logger.Debug("not the leader, skipping scheduled job",
			"schedule_id", schedule.ScheduleID)
		return
	}

	// Calculate period window using aligned boundaries
	now := time.Now().UTC()
	periodStart, periodEnd := CalculatePeriod(now, schedule.SettlementType, schedule.PeriodOffset)

	s.logger.Info("executing scheduled reconciliation",
		"schedule_id", schedule.ScheduleID,
		"account_id", schedule.AccountID,
		"asset_type", schedule.AssetType,
		"period_start", periodStart,
		"period_end", periodEnd)

	runID, err := s.reconClient.InitiateReconciliation(ctx, InitiateRequest{
		AccountID:      schedule.AccountID,
		Scope:          schedule.Scope,
		SettlementType: schedule.SettlementType,
		PeriodStart:    periodStart,
		PeriodEnd:      periodEnd,
		InitiatedBy:    "settlement-scheduler",
	})
	if err != nil {
		s.logger.Error("failed to initiate reconciliation",
			"schedule_id", schedule.ScheduleID,
			"account_id", schedule.AccountID,
			"error", err)
		s.metrics.RecordError("initiate_reconciliation")
		return
	}

	s.logger.Info("scheduled reconciliation initiated",
		"schedule_id", schedule.ScheduleID,
		"run_id", runID,
		"account_id", schedule.AccountID,
		"period_start", periodStart,
		"period_end", periodEnd)

	s.metrics.RecordRunTriggered(schedule.AssetType)
}

// ScheduleCount returns the number of currently registered schedules.
func (s *SettlementScheduler) ScheduleCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entryIDs)
}
