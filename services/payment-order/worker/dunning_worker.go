package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/payment-order/adapters/persistence"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/meridianhub/meridian/shared/platform/redislock"
	"github.com/meridianhub/meridian/shared/platform/scheduler"
	"github.com/redis/go-redis/v9"
)

// Dunning worker errors.
var (
	ErrNilDunningCallback = errors.New("dunning callback is required")
	ErrDunningKeyTooShort = errors.New("dunning retry key too short")
)

// dunningRetryZSet is the Redis sorted set key for dunning retry scheduling.
const dunningRetryZSet = "dunning:retries"

// DunningWorkerConfig holds configuration for the dunning retry worker.
type DunningWorkerConfig struct {
	// PollInterval is how frequently the worker checks for due retries.
	PollInterval time.Duration
	// MaxDunningLevel is the level at which accounts should be frozen.
	MaxDunningLevel int
	// ShutdownTimeout is the maximum time to wait for in-flight work during shutdown.
	// Default: 30 seconds.
	ShutdownTimeout time.Duration
}

// DunningCallback is called when a dunning escalation is due.
// The callback receives the billing run and should trigger the appropriate saga.
type DunningCallback func(ctx context.Context, run *domain.BillingRun) error

// DunningWorker polls a Redis sorted set for due dunning retries and triggers
// escalation. It uses ZADD to schedule retries with the due timestamp as score
// and ZRANGEBYSCORE to find items whose due time has passed.
//
// Lifecycle management is delegated to scheduler.WorkerLifecycle.
// Per-item distributed locking uses redislock.Lock to prevent duplicate
// processing across replicas.
type DunningWorker struct {
	lifecycle *scheduler.WorkerLifecycle
	repo      persistence.BillingRepository
	redis     *redis.Client
	lock      *redislock.Lock
	config    DunningWorkerConfig
	callback  DunningCallback
	logger    *slog.Logger
	metrics   *BillingMetrics
}

// NewDunningWorker creates a new dunning retry worker.
func NewDunningWorker(
	repo persistence.BillingRepository,
	redisClient *redis.Client,
	config DunningWorkerConfig,
	callback DunningCallback,
	logger *slog.Logger,
	metrics *BillingMetrics,
) (*DunningWorker, error) {
	if repo == nil {
		return nil, ErrNilBillingRepo
	}
	if redisClient == nil {
		return nil, ErrNilRedisClient
	}
	if logger == nil {
		return nil, ErrNilBillingLogger
	}
	if callback == nil {
		return nil, ErrNilDunningCallback
	}
	if config.PollInterval == 0 {
		config.PollInterval = 60 * time.Second
	}
	if config.MaxDunningLevel == 0 {
		config.MaxDunningLevel = domain.MaxDunningLevel
	}
	if config.ShutdownTimeout == 0 {
		config.ShutdownTimeout = 30 * time.Second
	}
	if metrics == nil {
		metrics = NewBillingMetrics()
	}

	workerLogger := logger.With("component", "dunning_worker")

	return &DunningWorker{
		lifecycle: scheduler.NewWorkerLifecycle(workerLogger),
		repo:      repo,
		redis:     redisClient,
		lock: redislock.NewLock(redisClient, redislock.Config{
			KeyPrefix:  "dunning:lock",
			LockTTL:    30 * time.Second,
			RenewEvery: 10 * time.Second,
		}, workerLogger),
		config:   config,
		callback: callback,
		logger:   workerLogger,
		metrics:  metrics,
	}, nil
}

// Start begins the dunning worker polling loop.
func (w *DunningWorker) Start(ctx context.Context) error {
	w.logger.Info("dunning worker starting",
		"poll_interval", w.config.PollInterval,
		"max_dunning_level", w.config.MaxDunningLevel)

	return w.lifecycle.Start(ctx, func(ctx context.Context) error {
		return w.pollLoop(ctx)
	})
}

// Stop signals the dunning worker to shut down gracefully and waits for
// in-flight work to complete up to the configured shutdown timeout.
func (w *DunningWorker) Stop() {
	w.lock.ReleaseAll(context.Background())
	w.lifecycle.Stop(w.config.ShutdownTimeout)
}

// pollLoop runs the ticker-based polling loop. It blocks until the context
// is cancelled (via lifecycle.Stop).
func (w *DunningWorker) pollLoop(ctx context.Context) error {
	ticker := time.NewTicker(w.config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("dunning worker stopping: context cancelled")
			return nil
		case <-ticker.C:
			w.processDueRetries(ctx)
		}
	}
}

// ScheduleDunningRetry adds a billing run to the sorted set with a score
// equal to the Unix timestamp when the retry becomes due.
func (w *DunningWorker) ScheduleDunningRetry(ctx context.Context, billingRunID uuid.UUID, delay time.Duration) error {
	dueAt := NowFunc().Add(delay)
	member := redis.Z{
		Score:  float64(dueAt.Unix()),
		Member: billingRunID.String(),
	}
	err := w.redis.ZAdd(ctx, dunningRetryZSet, member).Err()
	if err != nil {
		return fmt.Errorf("failed to schedule dunning retry: %w", err)
	}

	w.logger.Info("dunning retry scheduled",
		"billing_run_id", billingRunID,
		"delay", delay,
		"due_at", dueAt)

	return nil
}

// processDueRetries queries the sorted set for all members whose score (due
// timestamp) is at or before the current time, processes each one, and removes
// it from the set.
func (w *DunningWorker) processDueRetries(ctx context.Context) {
	now := NowFunc()
	maxScore := strconv.FormatInt(now.Unix(), 10)

	// Fetch billing run IDs that are due
	members, err := w.redis.ZRangeByScore(ctx, dunningRetryZSet, &redis.ZRangeBy{
		Min:   "-inf",
		Max:   maxScore,
		Count: 100,
	}).Result()
	if err != nil {
		w.logger.Error("failed to query dunning retries", "error", err)
		return
	}

	if len(members) == 0 {
		return
	}

	var processed int
	for _, member := range members {
		billingRunID, parseErr := uuid.Parse(member)
		if parseErr != nil {
			w.logger.Error("invalid billing run ID in dunning set", "member", member, "error", parseErr)
			w.redis.ZRem(ctx, dunningRetryZSet, member)
			continue
		}

		if w.processRetry(ctx, billingRunID) {
			// Only remove on success; transient failures retain the member for next poll
			w.redis.ZRem(ctx, dunningRetryZSet, member)
			processed++
		}
	}

	if processed > 0 {
		w.logger.Info("processed dunning retries", "count", processed)
	}
}

// processRetry handles a single due dunning retry. Returns true if the retry
// was handled (success or permanently resolved) and the ZSET member should be
// removed. Returns false on transient errors so the member is retained for the
// next poll cycle.
//
// Uses redislock.Lock for per-item locking to prevent duplicate processing
// across replicas. Uses lifecycle.ExecuteGuarded to track in-flight work
// for graceful shutdown.
func (w *DunningWorker) processRetry(ctx context.Context, billingRunID uuid.UUID) bool {
	// Acquire a per-item lock to prevent duplicate processing across replicas.
	acquired, release, err := w.lock.Acquire(ctx, "dunning", billingRunID.String())
	if err != nil {
		w.logger.Error("failed to acquire dunning lock",
			"billing_run_id", billingRunID,
			"error", err)
		return false
	}
	if !acquired {
		w.logger.Debug("dunning retry already being processed by another replica",
			"billing_run_id", billingRunID)
		return false
	}
	defer release()

	var result bool
	w.lifecycle.ExecuteGuarded(func() {
		result = w.executeRetry(ctx, billingRunID)
	})
	return result
}

// executeRetry performs the actual retry logic for a single billing run.
func (w *DunningWorker) executeRetry(ctx context.Context, billingRunID uuid.UUID) bool {
	// Load billing run from database
	run, err := w.repo.FindBillingRunByID(ctx, billingRunID)
	if err != nil {
		if errors.Is(err, persistence.ErrBillingRunNotFound) {
			w.logger.Info("billing run not found, dropping retry",
				"billing_run_id", billingRunID)
			return true
		}
		w.logger.Error("failed to load billing run for dunning",
			"billing_run_id", billingRunID,
			"error", err)
		return false
	}

	// Billing run resolved externally — remove from retry set
	if run.Status != domain.BillingRunStatusFailed {
		w.logger.Info("billing run no longer failed, skipping dunning",
			"billing_run_id", billingRunID,
			"status", run.Status)
		return true
	}

	// Call the dunning callback (triggers dunning_escalation saga)
	w.logger.Info("triggering dunning escalation",
		"billing_run_id", billingRunID,
		"current_dunning_level", run.DunningLevel)

	if err := w.callback(ctx, run); err != nil {
		w.logger.Error("dunning callback failed",
			"billing_run_id", billingRunID,
			"error", err)
		w.metrics.RecordError("dunning_callback")
		return false
	}

	w.logger.Info("dunning escalation triggered",
		"billing_run_id", billingRunID,
		"dunning_level", run.DunningLevel)
	return true
}

// CancelDunningRetry removes a billing run from the retry set.
// Called when a billing run is resolved (e.g., manual payment succeeds).
func (w *DunningWorker) CancelDunningRetry(ctx context.Context, billingRunID uuid.UUID) error {
	return w.redis.ZRem(ctx, dunningRetryZSet, billingRunID.String()).Err()
}
