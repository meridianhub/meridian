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
	ErrNilDunningCallback   = errors.New("dunning callback is required")
	ErrDunningKeyTooShort   = errors.New("dunning retry key too short")
	ErrDunningMissingTenant = errors.New("tenant ID is required for dunning retry")
)

// dunningRetryZSetPrefix is the Redis sorted set key prefix for dunning retry scheduling.
// The full key is "dunning:retries:{tenantID}" for tenant isolation.
const dunningRetryZSetPrefix = "dunning:retries:"

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

// DunningEmailCanceller cancels pending dunning emails for a billing run.
// This is used when a billing run is resolved (e.g., payment succeeds) to
// prevent sending stale dunning notifications.
type DunningEmailCanceller interface {
	CancelByIdempotencyKeyPattern(ctx context.Context, pattern string) (int64, error)
}

// DunningWorker polls a Redis sorted set for due dunning retries and triggers
// escalation. It uses ZADD to schedule retries with the due timestamp as score
// and ZRANGEBYSCORE to find items whose due time has passed.
//
// Lifecycle management is delegated to scheduler.WorkerLifecycle.
// Per-item distributed locking uses redislock.Lock to prevent duplicate
// processing across replicas.
type DunningWorker struct {
	lifecycle      *scheduler.WorkerLifecycle
	repo           persistence.BillingRepository
	redis          *redis.Client
	lock           *redislock.Lock
	config         DunningWorkerConfig
	callback       DunningCallback
	emailCanceller DunningEmailCanceller
	logger         *slog.Logger
	metrics        *BillingMetrics
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

// SetEmailCanceller sets an optional email canceller for cancelling pending
// dunning emails when a billing run is resolved externally.
func (w *DunningWorker) SetEmailCanceller(canceller DunningEmailCanceller) {
	w.emailCanceller = canceller
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
	w.lifecycle.Stop(w.config.ShutdownTimeout)
	w.lock.ReleaseAll(context.Background())
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

// ScheduleDunningRetry adds a billing run to the tenant-scoped sorted set with
// a score equal to the Unix timestamp when the retry becomes due.
func (w *DunningWorker) ScheduleDunningRetry(ctx context.Context, tenantID string, billingRunID uuid.UUID, delay time.Duration) error {
	if tenantID == "" {
		return ErrDunningMissingTenant
	}
	dueAt := NowFunc().Add(delay)
	member := redis.Z{
		Score:  float64(dueAt.Unix()),
		Member: billingRunID.String(),
	}
	key := dunningRetryZSetPrefix + tenantID
	err := w.redis.ZAdd(ctx, key, member).Err()
	if err != nil {
		return fmt.Errorf("failed to schedule dunning retry: %w", err)
	}

	w.logger.Info("dunning retry scheduled",
		"billing_run_id", billingRunID,
		"tenant_id", tenantID,
		"delay", delay,
		"due_at", dueAt)

	return nil
}

// processDueRetries scans for all tenant-scoped dunning ZSET keys and processes
// due retries from each. Uses SCAN to discover keys matching "dunning:retries:*".
func (w *DunningWorker) processDueRetries(ctx context.Context) {
	// Discover all tenant-scoped dunning keys
	keys, err := w.scanDunningKeys(ctx)
	if err != nil {
		w.logger.Error("failed to scan dunning retry keys", "error", err)
		return
	}

	for _, key := range keys {
		w.processDueRetriesForKey(ctx, key)
	}
}

// scanDunningKeys returns all Redis keys matching the dunning retry ZSET pattern.
func (w *DunningWorker) scanDunningKeys(ctx context.Context) ([]string, error) {
	var allKeys []string
	pattern := dunningRetryZSetPrefix + "*"
	iter := w.redis.Scan(ctx, 0, pattern, 100).Iterator()
	for iter.Next(ctx) {
		allKeys = append(allKeys, iter.Val())
	}
	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("failed to scan dunning keys: %w", err)
	}
	return allKeys, nil
}

// processDueRetriesForKey queries a single tenant's sorted set for all members
// whose score (due timestamp) is at or before the current time, processes each
// one, and removes it from the set.
func (w *DunningWorker) processDueRetriesForKey(ctx context.Context, key string) {
	now := NowFunc()
	maxScore := strconv.FormatInt(now.Unix(), 10)

	members, err := w.redis.ZRangeArgs(ctx, redis.ZRangeArgs{
		Key:     key,
		Start:   "-inf",
		Stop:    maxScore,
		ByScore: true,
		Count:   100,
	}).Result()
	if err != nil {
		w.logger.Error("failed to query dunning retries", "key", key, "error", err)
		return
	}

	if len(members) == 0 {
		return
	}

	var processed int
	for _, member := range members {
		billingRunID, parseErr := uuid.Parse(member)
		if parseErr != nil {
			w.logger.Error("invalid billing run ID in dunning set", "key", key, "member", member, "error", parseErr)
			w.redis.ZRem(ctx, key, member)
			continue
		}

		if w.processRetry(ctx, billingRunID) {
			w.redis.ZRem(ctx, key, member)
			processed++
		}
	}

	if processed > 0 {
		w.logger.Info("processed dunning retries", "key", key, "count", processed)
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

	// Billing run resolved externally — remove from retry set and cancel pending emails
	if run.Status != domain.BillingRunStatusFailed {
		w.logger.Info("billing run no longer failed, skipping dunning",
			"billing_run_id", billingRunID,
			"status", run.Status)
		if err := w.cancelPendingDunningEmails(ctx, billingRunID); err != nil {
			w.logger.Error("failed to cancel pending dunning emails",
				"billing_run_id", billingRunID,
				"error", err)
			return false
		}
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

// cancelPendingDunningEmails cancels any pending escalation dunning emails for the given billing run.
// Cancels escalation keys (dunning-1-, dunning-2-, dunning-3-, dunning-frozen-) but not
// resolution keys (dunning-resolved-) to avoid cancelling confirmation emails.
func (w *DunningWorker) cancelPendingDunningEmails(ctx context.Context, billingRunID uuid.UUID) error {
	if w.emailCanceller == nil {
		return nil
	}
	// Cancel escalation emails only. The pattern dunning-[0-9]- and dunning-frozen- match
	// escalation keys but not dunning-resolved- confirmation emails.
	runID := billingRunID.String()
	escalationPrefixes := []string{"dunning-1-", "dunning-2-", "dunning-3-", "dunning-frozen-"}
	var totalCancelled int64
	for _, prefix := range escalationPrefixes {
		cancelled, err := w.emailCanceller.CancelByIdempotencyKeyPattern(ctx, prefix+runID)
		if err != nil {
			return fmt.Errorf("cancel dunning emails (prefix %s): %w", prefix, err)
		}
		totalCancelled += cancelled
	}
	if totalCancelled > 0 {
		w.logger.Info("cancelled pending dunning emails",
			"billing_run_id", billingRunID,
			"count", totalCancelled)
	}
	return nil
}

// CancelDunningRetry removes a billing run from the tenant-scoped retry set.
// Called when a billing run is resolved (e.g., manual payment succeeds).
func (w *DunningWorker) CancelDunningRetry(ctx context.Context, tenantID string, billingRunID uuid.UUID) error {
	if tenantID == "" {
		return ErrDunningMissingTenant
	}
	key := dunningRetryZSetPrefix + tenantID
	return w.redis.ZRem(ctx, key, billingRunID.String()).Err()
}
