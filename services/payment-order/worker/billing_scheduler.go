package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/meridianhub/meridian/services/payment-order/adapters/persistence"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/redis/go-redis/v9"
	"github.com/robfig/cron/v3"
)

// Billing scheduler errors.
var (
	ErrNilBillingRepo      = errors.New("billing repository is required")
	ErrNilRedisClient      = errors.New("redis client is required")
	ErrNilBillingLogger    = errors.New("logger is required")
	ErrInvalidCronExpr     = errors.New("invalid cron expression")
	ErrSchedulerNotRunning = errors.New("scheduler is not running")
	ErrSchedulerRunning    = errors.New("scheduler is already running")
)

// NowFunc returns the current time. Replaceable for testing.
var NowFunc = func() time.Time { return time.Now().UTC() }

// IdempotencyKeyTTL is the TTL for billing run idempotency keys in Redis.
const IdempotencyKeyTTL = 48 * time.Hour

// BillingSchedulerConfig holds configuration for the billing scheduler.
type BillingSchedulerConfig struct {
	// TenantID is the tenant this scheduler runs billing for.
	TenantID string
	// CronExpression is the billing schedule (e.g., "0 2 1 * *" for 2 AM on 1st of month).
	CronExpression string
	// ShadowMode when true creates DRAFT invoices without initiating payment.
	ShadowMode bool
}

// BillingScheduler runs periodic billing cycles for a tenant using cron scheduling.
// It uses Redis for idempotency to prevent duplicate billing runs for the same period.
type BillingScheduler struct {
	repo    persistence.BillingRepository
	redis   *redis.Client
	config  BillingSchedulerConfig
	logger  *slog.Logger
	metrics *BillingMetrics

	cron    *cron.Cron
	done    chan struct{}
	wg      sync.WaitGroup
	mu      sync.Mutex
	running bool
	stopped bool
}

// NewBillingScheduler creates a new billing scheduler.
func NewBillingScheduler(
	repo persistence.BillingRepository,
	redisClient *redis.Client,
	config BillingSchedulerConfig,
	logger *slog.Logger,
	metrics *BillingMetrics,
) (*BillingScheduler, error) {
	if repo == nil {
		return nil, ErrNilBillingRepo
	}
	if redisClient == nil {
		return nil, ErrNilRedisClient
	}
	if logger == nil {
		return nil, ErrNilBillingLogger
	}
	if config.TenantID == "" {
		return nil, domain.ErrMissingTenantID
	}
	if metrics == nil {
		metrics = NewBillingMetrics()
	}

	// Validate cron expression
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	if _, err := parser.Parse(config.CronExpression); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidCronExpr, err)
	}

	cronRunner := cron.New(
		cron.WithLocation(time.UTC),
		cron.WithParser(parser),
	)

	return &BillingScheduler{
		repo:    repo,
		redis:   redisClient,
		config:  config,
		logger:  logger.With("component", "billing_scheduler", "tenant_id", config.TenantID),
		metrics: metrics,
		cron:    cronRunner,
		done:    make(chan struct{}),
	}, nil
}

// Start begins the billing scheduler. It registers the cron job and blocks
// until the context is cancelled or Stop() is called.
func (s *BillingScheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return ErrSchedulerRunning
	}
	s.running = true
	s.mu.Unlock()

	s.logger.Info("billing scheduler starting",
		"cron", s.config.CronExpression,
		"shadow_mode", s.config.ShadowMode)

	_, err := s.cron.AddFunc(s.config.CronExpression, func() {
		s.executeBillingRun(ctx)
	})
	if err != nil {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
		return fmt.Errorf("failed to register cron job: %w", err)
	}

	s.cron.Start()
	s.logger.Info("billing scheduler started")

	select {
	case <-ctx.Done():
		s.logger.Info("billing scheduler stopping: context cancelled")
	case <-s.done:
		s.logger.Info("billing scheduler stopping: explicit shutdown")
	}

	s.mu.Lock()
	s.running = false
	s.mu.Unlock()

	return nil
}

// Stop signals the scheduler to shut down gracefully.
func (s *BillingScheduler) Stop() {
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

	cronCtx := s.cron.Stop()

	waitDone := make(chan struct{})
	go func() {
		<-cronCtx.Done()
		s.wg.Wait()
		close(waitDone)
	}()

	select {
	case <-waitDone:
		s.logger.Info("billing scheduler shutdown complete")
	case <-time.After(30 * time.Second):
		s.logger.Warn("billing scheduler shutdown timeout")
	}
}

// executeBillingRun performs a single billing cycle execution.
func (s *BillingScheduler) executeBillingRun(ctx context.Context) {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.wg.Add(1)
	s.mu.Unlock()
	defer s.wg.Done()

	start := NowFunc()

	// Calculate billing period (previous complete period)
	periodStart, periodEnd := calculateBillingPeriod(start)

	// Generate deterministic idempotency key
	idempotencyKey := domain.BillingRunIdempotencyKey(s.config.TenantID, periodStart, periodEnd)

	s.logger.Info("executing billing run",
		"period_start", periodStart,
		"period_end", periodEnd,
		"idempotency_key", idempotencyKey)

	// Check Redis idempotency
	duplicate, err := s.checkIdempotency(ctx, idempotencyKey)
	if err != nil {
		s.logger.Error("failed to check idempotency", "error", err)
		s.metrics.RecordError("idempotency_check")
		return
	}
	if duplicate {
		s.logger.Info("billing run already exists for this period, skipping",
			"idempotency_key", idempotencyKey)
		return
	}

	// Create billing run
	run, err := domain.NewBillingRun(s.config.TenantID, periodStart, periodEnd)
	if err != nil {
		s.logger.Error("failed to create billing run", "error", err)
		s.metrics.RecordError("create_billing_run")
		return
	}

	if err := s.repo.CreateBillingRun(ctx, run); err != nil {
		if errors.Is(err, persistence.ErrBillingRunDuplicate) {
			s.logger.Info("billing run already exists in database, skipping",
				"idempotency_key", idempotencyKey)
			// Mark in Redis so future checks are fast
			s.markIdempotency(ctx, idempotencyKey)
			return
		}
		s.logger.Error("failed to persist billing run", "error", err)
		s.metrics.RecordError("persist_billing_run")
		return
	}

	// Mark in Redis for fast idempotency checks
	s.markIdempotency(ctx, idempotencyKey)

	// Transition to processing
	if err := run.StartProcessing(); err != nil {
		s.logger.Error("failed to start processing", "error", err)
		return
	}
	if err := s.repo.UpdateBillingRun(ctx, run); err != nil {
		s.logger.Error("failed to update billing run to processing", "error", err)
		return
	}

	s.metrics.RecordBillingRun(string(domain.BillingRunStatusProcessing))

	// Mark complete (invoice generation will be handled by subtask 9.3)
	if err := run.Complete(); err != nil {
		s.logger.Error("failed to complete billing run", "error", err)
		return
	}
	if err := s.repo.UpdateBillingRun(ctx, run); err != nil {
		s.logger.Error("failed to update billing run to completed", "error", err)
		return
	}

	elapsed := NowFunc().Sub(start)
	s.metrics.RecordBillingRun(string(domain.BillingRunStatusCompleted))
	s.metrics.ObserveRunDuration(elapsed.Seconds())

	s.logger.Info("billing run completed",
		"billing_run_id", run.ID,
		"duration", elapsed,
		"shadow_mode", s.config.ShadowMode)
}

// checkIdempotency checks Redis for an existing billing run key.
// Returns true if the key already exists (duplicate).
func (s *BillingScheduler) checkIdempotency(ctx context.Context, key string) (bool, error) {
	redisKey := "billing:idempotency:" + key
	exists, err := s.redis.Exists(ctx, redisKey).Result()
	if err != nil {
		return false, fmt.Errorf("redis exists check failed: %w", err)
	}
	return exists > 0, nil
}

// markIdempotency sets the idempotency key in Redis with TTL.
func (s *BillingScheduler) markIdempotency(ctx context.Context, key string) {
	redisKey := "billing:idempotency:" + key
	if err := s.redis.Set(ctx, redisKey, "1", IdempotencyKeyTTL).Err(); err != nil {
		s.logger.Error("failed to set idempotency key in Redis",
			"key", redisKey,
			"error", err)
	}
}

// calculateBillingPeriod returns the previous calendar month as the billing period.
// For a billing run at any point in month M, the period covers month M-1.
func calculateBillingPeriod(now time.Time) (time.Time, time.Time) {
	// Start of current month
	currentMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	// Previous month start
	periodStart := currentMonthStart.AddDate(0, -1, 0)
	// Previous month end = current month start
	periodEnd := currentMonthStart
	return periodStart, periodEnd
}

// TriggerManual allows manually triggering a billing run for a specific period.
// This is useful for testing and backfill scenarios.
func (s *BillingScheduler) TriggerManual(ctx context.Context, periodStart, periodEnd time.Time) error {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return ErrSchedulerNotRunning
	}
	s.wg.Add(1)
	s.mu.Unlock()
	defer s.wg.Done()

	idempotencyKey := domain.BillingRunIdempotencyKey(s.config.TenantID, periodStart, periodEnd)

	duplicate, err := s.checkIdempotency(ctx, idempotencyKey)
	if err != nil {
		return fmt.Errorf("idempotency check failed: %w", err)
	}
	if duplicate {
		return nil
	}

	run, err := domain.NewBillingRun(s.config.TenantID, periodStart, periodEnd)
	if err != nil {
		return fmt.Errorf("create billing run: %w", err)
	}

	if err := s.repo.CreateBillingRun(ctx, run); err != nil {
		if errors.Is(err, persistence.ErrBillingRunDuplicate) {
			s.markIdempotency(ctx, idempotencyKey)
			return nil
		}
		return fmt.Errorf("persist billing run: %w", err)
	}

	s.markIdempotency(ctx, idempotencyKey)
	s.metrics.RecordBillingRun(string(domain.BillingRunStatusInitiated))
	return nil
}
