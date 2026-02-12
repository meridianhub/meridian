package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/payment-order/adapters/persistence"
	"github.com/meridianhub/meridian/services/payment-order/domain"
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
}

// DunningCallback is called when a dunning escalation is due.
// The callback receives the billing run and should trigger the appropriate saga.
type DunningCallback func(ctx context.Context, run *domain.BillingRun) error

// DunningWorker polls a Redis sorted set for due dunning retries and triggers
// escalation. It uses ZADD to schedule retries with the due timestamp as score
// and ZRANGEBYSCORE to find items whose due time has passed.
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
		return nil, ErrNilDunningCallback
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
func (w *DunningWorker) processRetry(ctx context.Context, billingRunID uuid.UUID) bool {
	w.wg.Add(1)
	defer w.wg.Done()

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
