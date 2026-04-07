package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/robfig/cron/v3"
)

// Schedule represents a single cron schedule to be executed.
type Schedule struct {
	ID       string
	CronExpr string
	TenantID string
	Metadata any
}

// ScheduleProvider lists active schedules from an external source (e.g. Reference Data).
type ScheduleProvider interface {
	ListSchedules(ctx context.Context) ([]Schedule, error)
}

// Executor runs the business logic for a triggered schedule.
type Executor interface {
	Execute(ctx context.Context, schedule Schedule) error
}

// TenantStatusChecker checks if a tenant is active before scheduled execution.
type TenantStatusChecker interface {
	IsActive(ctx context.Context, tenantID string) (bool, error)
}

// DistributedLock provides distributed locking to prevent duplicate execution
// across replicas.
type DistributedLock interface {
	// Acquire attempts to acquire a lock for the given schedule.
	// Returns true + release function if acquired, false if another holder has it.
	Acquire(ctx context.Context, tenantID, resourceID string) (bool, func(), error)
}

// CronSchedulerConfig holds configuration for the cron scheduler.
type CronSchedulerConfig struct {
	// Name identifies this scheduler instance (used in execution records and lock keys).
	Name string
	// RefreshInterval is how often to reload schedules from the provider.
	RefreshInterval time.Duration
	// RefreshJitterMax is the maximum random jitter added after each refresh tick
	// to prevent thundering herd across replicas. Default: 0 (disabled).
	RefreshJitterMax time.Duration
	// ShutdownTimeout is the maximum time to wait for in-flight jobs during shutdown.
	ShutdownTimeout time.Duration
	// ExecutionTimeout is the maximum time a single job execution can take.
	ExecutionTimeout time.Duration
	// MaxCatchUpAge is the maximum age of missed cron windows to re-execute on startup.
	// Windows older than this are recorded as MISSED but not executed.
	// Default: 1 hour.
	MaxCatchUpAge time.Duration
	// MaxConcurrentExecutions is the maximum number of jobs that can execute
	// concurrently across all tenants. Default: 20.
	MaxConcurrentExecutions int
	// MaxConcurrentPerTenant is the maximum number of jobs that can execute
	// concurrently for a single tenant. Default: 3.
	MaxConcurrentPerTenant int
}

// WithDefaults returns a copy of the config with zero-value fields set to defaults.
func (c CronSchedulerConfig) WithDefaults() CronSchedulerConfig {
	if c.Name == "" {
		c.Name = "cron-scheduler"
	}
	if c.RefreshInterval <= 0 {
		c.RefreshInterval = 60 * time.Second
	}
	if c.ShutdownTimeout <= 0 {
		c.ShutdownTimeout = 30 * time.Second
	}
	if c.ExecutionTimeout <= 0 {
		c.ExecutionTimeout = 5 * time.Minute
	}
	if c.MaxCatchUpAge <= 0 {
		c.MaxCatchUpAge = time.Hour
	}
	if c.MaxConcurrentExecutions <= 0 {
		c.MaxConcurrentExecutions = 20
	}
	if c.MaxConcurrentPerTenant <= 0 {
		c.MaxConcurrentPerTenant = 3
	}
	return c
}

// CronScheduler runs cron-based schedules using WorkerLifecycle for start/stop,
// robfig/cron for cron parsing and scheduling, DistributedLock for deduplication,
// and ExecutionStore for audit trail persistence.
type CronScheduler struct {
	lifecycle *WorkerLifecycle
	provider  ScheduleProvider
	executor  Executor
	lock      DistributedLock
	store     ExecutionStore
	config    CronSchedulerConfig
	logger    *slog.Logger

	cron      *cron.Cron
	parser    cron.Parser
	mu        sync.Mutex
	entryIDs  map[string]cron.EntryID
	schedules map[string]Schedule

	semaphore        chan struct{}
	tenantSemaphores map[string]chan struct{}
	tenantSemMu      sync.Mutex

	statusChecker TenantStatusChecker
}

// NewCronScheduler creates a new CronScheduler. The store parameter is optional;
// if nil, execution audit trail is disabled.
func NewCronScheduler(
	provider ScheduleProvider,
	executor Executor,
	lock DistributedLock,
	config CronSchedulerConfig,
	logger *slog.Logger,
	opts ...CronSchedulerOption,
) *CronScheduler {
	config = config.WithDefaults()
	if logger == nil {
		logger = slog.Default()
	}

	defaultParser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

	cronRunner := cron.New(
		cron.WithLocation(time.UTC),
		cron.WithParser(defaultParser),
	)

	s := &CronScheduler{
		lifecycle:        NewWorkerLifecycle(logger),
		provider:         provider,
		executor:         executor,
		lock:             lock,
		config:           config,
		logger:           logger.With("component", config.Name),
		cron:             cronRunner,
		parser:           defaultParser,
		entryIDs:         make(map[string]cron.EntryID),
		schedules:        make(map[string]Schedule),
		semaphore:        make(chan struct{}, config.MaxConcurrentExecutions),
		tenantSemaphores: make(map[string]chan struct{}),
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// CronSchedulerOption configures optional CronScheduler dependencies.
type CronSchedulerOption func(*CronScheduler)

// WithCronExecutionStore sets the execution store for audit trail.
func WithCronExecutionStore(store ExecutionStore) CronSchedulerOption {
	return func(s *CronScheduler) {
		s.store = store
	}
}

// WithTenantStatusChecker sets a checker that gates execution on tenant active status.
// If nil (default), all tenants are allowed to execute.
func WithTenantStatusChecker(checker TenantStatusChecker) CronSchedulerOption {
	return func(s *CronScheduler) {
		s.statusChecker = checker
	}
}

// WithCronRunner replaces the default cron runner (e.g. to inject a
// seconds-level parser for faster tests). When using a custom runner with a
// different parser, also use WithCronParser so that catch-up logic can parse
// cron expressions consistently.
func WithCronRunner(c *cron.Cron) CronSchedulerOption {
	return func(s *CronScheduler) {
		s.cron = c
	}
}

// WithCronParser overrides the cron expression parser used by catch-up logic.
// This must match the parser configured on the cron runner.
func WithCronParser(p cron.Parser) CronSchedulerOption {
	return func(s *CronScheduler) {
		s.parser = p
	}
}

// Start begins the scheduler. It loads schedules, starts cron, and periodically
// refreshes. This method blocks until the context is cancelled or Stop is called.
func (s *CronScheduler) Start(ctx context.Context) error {
	return s.lifecycle.Start(ctx, func(workCtx context.Context) error {
		s.logger.Info("cron scheduler starting",
			"name", s.config.Name,
			"refresh_interval", s.config.RefreshInterval)

		// Load initial schedules
		if err := s.refreshSchedules(workCtx); err != nil {
			s.logger.Error("failed to load initial schedules", "error", err)
		}

		// Run catch-up for missed windows before starting live cron
		s.runCatchUp(workCtx)

		s.cron.Start()
		defer func() {
			cronCtx := s.cron.Stop()
			<-cronCtx.Done()
		}()

		s.logger.Info("cron scheduler started", "name", s.config.Name)

		refreshTicker := time.NewTicker(s.config.RefreshInterval)
		defer refreshTicker.Stop()

		for {
			select {
			case <-workCtx.Done():
				s.logger.Info("cron scheduler stopping", "name", s.config.Name)
				return nil
			case <-refreshTicker.C:
				if s.config.RefreshJitterMax > 0 {
					jitter := time.Duration(rand.Int64N(int64(s.config.RefreshJitterMax)))
					select {
					case <-workCtx.Done():
						continue
					case <-time.After(jitter):
					}
				}
				if err := s.refreshSchedules(workCtx); err != nil {
					s.logger.Error("failed to refresh schedules", "error", err)
				}
			}
		}
	})
}

// Stop signals the scheduler to shut down gracefully.
func (s *CronScheduler) Stop() {
	s.lifecycle.Stop(s.config.ShutdownTimeout)
}

// ScheduleCount returns the number of currently registered schedules.
func (s *CronScheduler) ScheduleCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entryIDs)
}

// refreshSchedules queries the provider for current schedules and syncs the cron runner.
func (s *CronScheduler) refreshSchedules(ctx context.Context) error {
	schedules, err := s.provider.ListSchedules(ctx)
	if err != nil {
		return fmt.Errorf("list schedules: %w", err)
	}

	s.logger.Debug("loaded schedules", "count", len(schedules))

	currentSchedules := make(map[string]Schedule, len(schedules))
	for _, sched := range schedules {
		currentSchedules[sched.ID] = sched
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Remove schedules that no longer exist
	for id, entryID := range s.entryIDs {
		if _, exists := currentSchedules[id]; !exists {
			s.cron.Remove(entryID)
			delete(s.entryIDs, id)
			delete(s.schedules, id)
			s.logger.Info("removed schedule", "schedule_id", id)
		}
	}

	// Add or update schedules
	for _, sched := range schedules {
		if prev, exists := s.schedules[sched.ID]; exists {
			if !scheduleChanged(prev, sched) {
				continue
			}
			// Re-register: remove old entry then fall through to add
			s.cron.Remove(s.entryIDs[sched.ID])
			delete(s.entryIDs, sched.ID)
			delete(s.schedules, sched.ID)
			s.logger.Info("schedule changed, re-registering",
				"schedule_id", sched.ID,
				"old_cron_expr", prev.CronExpr,
				"new_cron_expr", sched.CronExpr)
		}

		entryID, err := s.addSchedule(sched) //nolint:contextcheck // cron.AddFunc takes func() with no context
		if err != nil {
			s.logger.Error("failed to add schedule",
				"schedule_id", sched.ID,
				"cron_expr", sched.CronExpr,
				"error", err)
			continue
		}

		s.entryIDs[sched.ID] = entryID
		s.schedules[sched.ID] = sched
		s.logger.Info("registered schedule",
			"schedule_id", sched.ID,
			"cron_expr", sched.CronExpr,
			"tenant_id", sched.TenantID)
	}

	return nil
}

// addSchedule registers a single schedule with the cron runner.
func (s *CronScheduler) addSchedule(sched Schedule) (cron.EntryID, error) {
	schedule := sched
	entryID, err := s.cron.AddFunc(schedule.CronExpr, func() {
		s.executeJob(schedule)
	})
	if err != nil {
		return 0, fmt.Errorf("invalid cron expression %q: %w", schedule.CronExpr, err)
	}
	return entryID, nil
}

// tenantIsEligible returns true if execution should proceed for the schedule's tenant.
// When no checker is configured or the schedule has no tenant ID, it returns true.
// On check error it fails open (returns true) to preserve availability.
func (s *CronScheduler) tenantIsEligible(ctx context.Context, schedule Schedule) bool {
	if s.statusChecker == nil || schedule.TenantID == "" {
		return true
	}
	active, err := s.statusChecker.IsActive(ctx, schedule.TenantID)
	if err != nil {
		s.logger.Error("failed to check tenant status",
			"schedule_id", schedule.ID,
			"error", err)
		return true // fail open
	}
	if !active {
		s.logger.Info("skipping execution for inactive tenant",
			"schedule_id", schedule.ID,
			"tenant_id", schedule.TenantID)
		s.recordExecution(ctx, schedule, ExecutionStatusSkipped, nil, strPtr("tenant not active"))
		return false
	}
	return true
}

// acquireGlobalSemaphore tries to acquire the global concurrency semaphore.
// Returns a release function and true if acquired, or nil and false if the limit is reached.
func (s *CronScheduler) acquireGlobalSemaphore(ctx context.Context, schedule Schedule) (func(), bool) {
	select {
	case s.semaphore <- struct{}{}:
		return func() { <-s.semaphore }, true
	default:
		s.logger.Warn("global concurrency limit reached, skipping",
			"schedule_id", schedule.ID,
			"tenant_id", schedule.TenantID)
		s.recordExecution(ctx, schedule, ExecutionStatusSkipped, nil, strPtr("concurrency limit reached"))
		return nil, false
	}
}

// acquireTenantSemaphore tries to acquire the per-tenant concurrency semaphore.
// Returns a release function and true if acquired, or nil and false if the limit is reached.
func (s *CronScheduler) acquireTenantSemaphore(ctx context.Context, schedule Schedule) (func(), bool) {
	if schedule.TenantID == "" {
		return func() {}, true
	}
	tenantSem := s.getOrCreateTenantSemaphore(schedule.TenantID)
	select {
	case tenantSem <- struct{}{}:
		return func() { <-tenantSem }, true
	default:
		s.logger.Warn("per-tenant concurrency limit reached, skipping",
			"schedule_id", schedule.ID,
			"tenant_id", schedule.TenantID)
		s.recordExecution(ctx, schedule, ExecutionStatusSkipped, nil,
			strPtr(fmt.Sprintf("per-tenant concurrency limit reached for tenant %s", schedule.TenantID)))
		return nil, false
	}
}

// executeJob runs a single scheduled job with distributed locking and audit trail.
func (s *CronScheduler) executeJob(schedule Schedule) {
	s.lifecycle.ExecuteGuarded(func() {
		ctx, cancel := context.WithTimeout(context.Background(), s.config.ExecutionTimeout)
		defer cancel()

		// Propagate tenant context first so all audit records are properly scoped
		if schedule.TenantID != "" {
			if tid, err := tenant.NewTenantID(schedule.TenantID); err == nil {
				ctx = tenant.WithTenant(ctx, tid)
			}
		}

		// Inject Actor so downstream audit records attribute the operation correctly
		ctx = auth.WithActor(ctx, auth.Actor{
			ID:            fmt.Sprintf("system:scheduler:%s", s.config.Name),
			Type:          auth.ActorTypeScheduler,
			Authenticated: false,
			Source:        "cron-scheduler",
		})

		// Inject correlation ID for distributed tracing
		ctx = audit.WithCorrelationID(ctx, uuid.New().String())

		// Check tenant status before consuming semaphore slots
		if !s.tenantIsEligible(ctx, schedule) {
			return
		}

		// Acquire global concurrency semaphore
		releaseGlobal, ok := s.acquireGlobalSemaphore(ctx, schedule)
		if !ok {
			return
		}
		defer releaseGlobal()

		// Acquire per-tenant concurrency semaphore
		releaseTenant, ok := s.acquireTenantSemaphore(ctx, schedule)
		if !ok {
			return
		}
		defer releaseTenant()

		s.acquireLockAndExecute(ctx, schedule)
	})
}

// acquireLockAndExecute acquires the distributed lock (if configured) and runs the job.
func (s *CronScheduler) acquireLockAndExecute(ctx context.Context, schedule Schedule) {
	if s.lock != nil {
		acquired, release, err := s.lock.Acquire(ctx, schedule.TenantID, s.lockKey(schedule.ID))
		if err != nil {
			s.logger.Error("failed to acquire lock",
				"schedule_id", schedule.ID,
				"error", err)
			return
		}
		if !acquired {
			s.logger.Debug("lock not acquired, skipping",
				"schedule_id", schedule.ID)
			s.recordExecution(ctx, schedule, ExecutionStatusSkipped, nil, strPtr("lock not acquired"))
			return
		}
		defer release()
	}

	execID := uuid.New()
	now := time.Now().UTC()
	s.recordExecutionStart(ctx, execID, schedule, now)

	err := s.executor.Execute(ctx, schedule)
	s.recordExecutionResult(ctx, execID, schedule, err)

	if err != nil {
		s.logger.Error("schedule execution failed",
			"schedule_id", schedule.ID,
			"error", err)
		return
	}

	s.logger.Info("schedule execution completed",
		"schedule_id", schedule.ID,
		"tenant_id", schedule.TenantID)
}

// getOrCreateTenantSemaphore lazily creates a per-tenant semaphore channel.
func (s *CronScheduler) getOrCreateTenantSemaphore(tenantID string) chan struct{} {
	s.tenantSemMu.Lock()
	defer s.tenantSemMu.Unlock()
	sem, ok := s.tenantSemaphores[tenantID]
	if !ok {
		sem = make(chan struct{}, s.config.MaxConcurrentPerTenant)
		s.tenantSemaphores[tenantID] = sem
	}
	return sem
}

func (s *CronScheduler) lockKey(scheduleID string) string {
	return fmt.Sprintf("%s:%s", s.config.Name, scheduleID)
}

// recordExecutionStart records the start of an execution in the audit trail.
func (s *CronScheduler) recordExecutionStart(ctx context.Context, execID uuid.UUID, schedule Schedule, scheduledAt time.Time) {
	if s.store == nil {
		return
	}
	now := time.Now().UTC()
	exec := Execution{
		ID:            execID,
		SchedulerName: s.config.Name,
		ScheduleID:    schedule.ID,
		ScheduledAt:   scheduledAt,
		ExecutedAt:    &now,
		Status:        ExecutionStatusTriggered,
	}
	if err := s.store.RecordExecution(ctx, exec); err != nil {
		s.logger.Error("failed to record execution start",
			"schedule_id", schedule.ID,
			"error", err)
	}
}

// recordExecutionResult updates the audit trail with the result.
func (s *CronScheduler) recordExecutionResult(ctx context.Context, execID uuid.UUID, schedule Schedule, execErr error) {
	if s.store == nil {
		return
	}
	if execErr != nil {
		errMsg := execErr.Error()
		if err := s.store.UpdateExecution(ctx, execID, ExecutionStatusFailed, nil, &errMsg); err != nil {
			s.logger.Error("failed to record execution failure",
				"schedule_id", schedule.ID,
				"error", err)
		}
		return
	}
	if err := s.store.UpdateExecution(ctx, execID, ExecutionStatusCompleted, nil, nil); err != nil {
		s.logger.Error("failed to record execution completion",
			"schedule_id", schedule.ID,
			"error", err)
	}
}

// recordExecution is a convenience for recording a single-shot status (e.g. SKIPPED).
func (s *CronScheduler) recordExecution(ctx context.Context, schedule Schedule, status ExecutionStatus, resultRef *string, errMsg *string) {
	if s.store == nil {
		return
	}
	now := time.Now().UTC()
	exec := Execution{
		ID:            uuid.New(),
		SchedulerName: s.config.Name,
		ScheduleID:    schedule.ID,
		ScheduledAt:   now,
		Status:        status,
		ResultRef:     resultRef,
		ErrorMessage:  errMsg,
	}
	if err := s.store.RecordExecution(ctx, exec); err != nil {
		s.logger.Error("failed to record execution",
			"schedule_id", schedule.ID,
			"status", status,
			"error", err)
	}
}

// scheduleChanged returns true if any relevant field differs between two schedules.
func scheduleChanged(a, b Schedule) bool {
	return a.CronExpr != b.CronExpr || a.TenantID != b.TenantID
}

func strPtr(s string) *string {
	return &s
}
