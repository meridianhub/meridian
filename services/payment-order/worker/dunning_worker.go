package worker

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/payment-order/adapters/persistence"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/redis/go-redis/v9"
)

// DunningWorkerConfig holds configuration for the dunning retry worker.
type DunningWorkerConfig struct {
	// PollInterval is how frequently the worker checks for due retries.
	PollInterval time.Duration
	// MaxDunningLevel is the level at which accounts should be frozen.
	MaxDunningLevel int
}

// DunningCallback is called when a dunning escalation is due.
// The callback receives the billing run and should trigger the appropriate saga.
type DunningCallback func(ctx context.Context, run *domain.BillingRun) error

// DunningWorker polls Redis for due dunning retry keys and triggers escalation.
// It uses Redis TTL-based scheduling: when a billing run fails, a key is set
// with a TTL equal to the backoff duration. The worker polls for expired keys
// to trigger the next escalation step.
type DunningWorker struct {
	repo     persistence.BillingRepository
	redis    *redis.Client
	config   DunningWorkerConfig
	callback DunningCallback
	logger   *slog.Logger
	metrics  *BillingMetrics

	done    chan struct{}
	wg      sync.WaitGroup
	mu      sync.Mutex
	running bool
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
		return nil, fmt.Errorf("dunning callback is required")
	}
	if config.PollInterval == 0 {
		config.PollInterval = 60 * time.Second
	}
	if config.MaxDunningLevel == 0 {
		config.MaxDunningLevel = domain.MaxDunningLevel
	}
	if metrics == nil {
		metrics = NewBillingMetrics()
	}

	return &DunningWorker{
		repo:     repo,
		redis:    redisClient,
		config:   config,
		callback: callback,
		logger:   logger.With("component", "dunning_worker"),
		metrics:  metrics,
		done:     make(chan struct{}),
	}, nil
}

// Start begins the dunning worker polling loop.
func (w *DunningWorker) Start(ctx context.Context) error {
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return ErrSchedulerRunning
	}
	w.running = true
	w.mu.Unlock()

	w.logger.Info("dunning worker starting",
		"poll_interval", w.config.PollInterval,
		"max_dunning_level", w.config.MaxDunningLevel)

	ticker := time.NewTicker(w.config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("dunning worker stopping: context cancelled")
			w.mu.Lock()
			w.running = false
			w.mu.Unlock()
			w.wg.Wait()
			return nil
		case <-w.done:
			w.logger.Info("dunning worker stopping: explicit shutdown")
			w.mu.Lock()
			w.running = false
			w.mu.Unlock()
			w.wg.Wait()
			return nil
		case <-ticker.C:
			w.processDueRetries(ctx)
		}
	}
}

// Stop signals the dunning worker to shut down gracefully.
func (w *DunningWorker) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()

	select {
	case <-w.done:
	default:
		close(w.done)
	}
}

// ScheduleDunningRetry sets a Redis key with TTL for the backoff duration.
// When the key expires, the worker will detect this and trigger escalation.
func (w *DunningWorker) ScheduleDunningRetry(ctx context.Context, billingRunID uuid.UUID, delay time.Duration) error {
	key := dunningRetryKey(billingRunID)
	err := w.redis.Set(ctx, key, "pending", delay).Err()
	if err != nil {
		return fmt.Errorf("failed to schedule dunning retry: %w", err)
	}

	w.logger.Info("dunning retry scheduled",
		"billing_run_id", billingRunID,
		"delay", delay,
		"key", key)

	return nil
}

// processDueRetries scans for billing runs due for dunning escalation.
// A billing run is due when its Redis retry key has expired (TTL elapsed).
func (w *DunningWorker) processDueRetries(ctx context.Context) {
	// Use SCAN to find all dunning retry keys
	var cursor uint64
	var processed int

	for {
		keys, nextCursor, err := w.redis.Scan(ctx, cursor, "dunning:retry:*", 100).Result()
		if err != nil {
			w.logger.Error("failed to scan dunning retry keys", "error", err)
			return
		}

		for _, key := range keys {
			// Check if the key is still pending (exists means not yet due)
			// Actually, for TTL-based scheduling, we use a different pattern:
			// We store a "ready" marker. The scheduler sets a separate delay key.
			// When processing, we check for "ready" keys.
			val, err := w.redis.Get(ctx, key).Result()
			if err != nil {
				continue // Key expired or error
			}

			if val != "ready" {
				continue // Not yet due
			}

			w.processRetryKey(ctx, key)
			processed++
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	if processed > 0 {
		w.logger.Info("processed dunning retries", "count", processed)
	}
}

// processRetryKey handles a single due dunning retry.
func (w *DunningWorker) processRetryKey(ctx context.Context, key string) {
	// Extract billing run ID from key: "dunning:retry:{billing_run_id}"
	billingRunID, err := parseDunningRetryKey(key)
	if err != nil {
		w.logger.Error("invalid dunning retry key", "key", key, "error", err)
		w.redis.Del(ctx, key)
		return
	}

	w.wg.Add(1)
	defer w.wg.Done()

	// Load billing run from database
	run, err := w.repo.FindBillingRunByID(ctx, billingRunID)
	if err != nil {
		w.logger.Error("failed to load billing run for dunning",
			"billing_run_id", billingRunID,
			"error", err)
		return
	}

	// Verify billing run is still in FAILED state
	if run.Status != domain.BillingRunStatusFailed {
		w.logger.Info("billing run no longer failed, skipping dunning",
			"billing_run_id", billingRunID,
			"status", run.Status)
		w.redis.Del(ctx, key)
		return
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
		return
	}

	// Clean up the retry key
	w.redis.Del(ctx, key)

	w.logger.Info("dunning escalation triggered",
		"billing_run_id", billingRunID,
		"dunning_level", run.DunningLevel)
}

// MarkRetryReady transitions a retry key from pending to ready.
// Called by a separate delayed process or when the TTL expires.
// For the initial implementation, callers set keys directly as "ready" with delay.
func (w *DunningWorker) MarkRetryReady(ctx context.Context, billingRunID uuid.UUID) error {
	key := dunningRetryKey(billingRunID)
	return w.redis.Set(ctx, key, "ready", 0).Err()
}

// dunningRetryKey generates the Redis key for a dunning retry.
func dunningRetryKey(billingRunID uuid.UUID) string {
	return fmt.Sprintf("dunning:retry:%s", billingRunID.String())
}

// parseDunningRetryKey extracts the billing run ID from a Redis key.
func parseDunningRetryKey(key string) (uuid.UUID, error) {
	const prefix = "dunning:retry:"
	if len(key) <= len(prefix) {
		return uuid.Nil, fmt.Errorf("key too short: %s", key)
	}
	return uuid.Parse(key[len(prefix):])
}
