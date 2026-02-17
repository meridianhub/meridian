package http

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// StripeEventProcessor errors.
var (
	ErrNilRedisClient        = errors.New("redis client cannot be nil")
	ErrEventAlreadyProcessed = errors.New("stripe event already processed")
	ErrDunningMissingTenant  = errors.New("tenant ID is required for dunning scheduling")
)

// Stripe event processor constants.
const (
	// processedWebhookKeyPrefix is the Redis key prefix for deduplicating Stripe webhook events.
	processedWebhookKeyPrefix = "processed_webhook:"

	// processedWebhookTTL is how long processed event IDs are retained in Redis (48 hours).
	processedWebhookTTL = 48 * time.Hour

	// dunningRetryZSetPrefix is the Redis sorted set key prefix for dunning retry scheduling.
	// The full key is "dunning:retries:{tenantID}" for tenant isolation.
	// This matches the key pattern used by DunningWorker.
	dunningRetryZSetPrefix = "dunning:retries:"

	// defaultDunningDelay is the default delay before the first dunning retry.
	defaultDunningDelay = 24 * time.Hour
)

// StripeEventProcessor processes Stripe webhook events with idempotency guarantees.
// It provides a Stripe-specific deduplication layer using event IDs, complementing
// the existing idempotency in UpdatePaymentOrder (which uses webhook-level keys).
//
// Responsibilities:
//   - Deduplicate Stripe events by event ID (Redis SET with 48h TTL)
//   - Trigger dunning for failed payments (add to Redis ZSET)
type StripeEventProcessor struct {
	redis  *redis.Client
	logger *slog.Logger
}

// StripeEventProcessorConfig contains configuration for creating a StripeEventProcessor.
type StripeEventProcessorConfig struct {
	RedisClient *redis.Client
	Logger      *slog.Logger
}

// NewStripeEventProcessor creates a new StripeEventProcessor.
func NewStripeEventProcessor(cfg StripeEventProcessorConfig) (*StripeEventProcessor, error) {
	if cfg.RedisClient == nil {
		return nil, ErrNilRedisClient
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &StripeEventProcessor{
		redis:  cfg.RedisClient,
		logger: logger,
	}, nil
}

// PreProcess checks whether a Stripe event has already been processed.
// Returns ErrEventAlreadyProcessed if the event ID was already seen.
// Otherwise, marks the event as processed in Redis with a 48h TTL.
// On Redis failure, returns nil to allow processing to continue
// (the downstream UpdatePaymentOrder has its own idempotency layer).
func (p *StripeEventProcessor) PreProcess(ctx context.Context, eventID string) error {
	if eventID == "" {
		p.logger.Warn("cannot preprocess stripe event: empty event ID")
		return nil
	}

	key := processedWebhookKeyPrefix + eventID

	// SET NX - only sets if key does not exist; redis.Nil means the key already existed
	result := p.redis.SetArgs(ctx, key, time.Now().Unix(), redis.SetArgs{Mode: "NX", TTL: processedWebhookTTL})
	if err := result.Err(); err != nil && !errors.Is(err, redis.Nil) {
		p.logger.Error("failed to check stripe event idempotency",
			"event_id", eventID,
			"error", err)
		return nil
	}

	if result.Err() != nil {
		p.logger.Info("stripe event already processed, skipping",
			"event_id", eventID)
		return ErrEventAlreadyProcessed
	}

	p.logger.Debug("stripe event marked as processing",
		"event_id", eventID)

	return nil
}

// ScheduleDunning adds a payment order to the tenant-scoped dunning retry sorted set.
// Called when a payment_intent.payment_failed event is received.
// The dunning worker will pick up the entry and trigger escalation.
func (p *StripeEventProcessor) ScheduleDunning(ctx context.Context, tenantID, paymentOrderID string) error {
	if paymentOrderID == "" {
		p.logger.Warn("cannot schedule dunning: empty payment order ID")
		return nil
	}
	if tenantID == "" {
		p.logger.Error("cannot schedule dunning: empty tenant ID",
			"payment_order_id", paymentOrderID)
		return ErrDunningMissingTenant
	}

	dueAt := time.Now().Add(defaultDunningDelay)
	member := redis.Z{
		Score:  float64(dueAt.Unix()),
		Member: fmt.Sprintf("stripe:%s", paymentOrderID),
	}

	key := dunningRetryZSetPrefix + tenantID
	err := p.redis.ZAdd(ctx, key, member).Err()
	if err != nil {
		p.logger.Error("failed to schedule dunning retry",
			"payment_order_id", paymentOrderID,
			"tenant_id", tenantID,
			"error", err)
		return fmt.Errorf("failed to schedule dunning retry: %w", err)
	}

	p.logger.Info("dunning retry scheduled for failed stripe payment",
		"payment_order_id", paymentOrderID,
		"tenant_id", tenantID,
		"due_at", dueAt)

	return nil
}
