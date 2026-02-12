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
)

// Stripe event processor constants.
const (
	// processedWebhookKeyPrefix is the Redis key prefix for deduplicating Stripe webhook events.
	processedWebhookKeyPrefix = "processed_webhook:"

	// processedWebhookTTL is how long processed event IDs are retained in Redis (48 hours).
	processedWebhookTTL = 48 * time.Hour

	// dunningRetryZSet is the Redis sorted set key for dunning retry scheduling.
	// This matches the key used by DunningWorker.
	dunningRetryZSet = "dunning:retries"

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

	// SET NX - only sets if key does not exist, returns true if set (new event)
	wasSet, err := p.redis.SetNX(ctx, key, time.Now().Unix(), processedWebhookTTL).Result()
	if err != nil {
		p.logger.Error("failed to check stripe event idempotency",
			"event_id", eventID,
			"error", err)
		return nil
	}

	if !wasSet {
		p.logger.Info("stripe event already processed, skipping",
			"event_id", eventID)
		return ErrEventAlreadyProcessed
	}

	p.logger.Debug("stripe event marked as processing",
		"event_id", eventID)

	return nil
}

// ScheduleDunning adds a payment order to the dunning retry sorted set.
// Called when a payment_intent.payment_failed event is received.
// The dunning worker will pick up the entry and trigger escalation.
func (p *StripeEventProcessor) ScheduleDunning(ctx context.Context, paymentOrderID string) error {
	if paymentOrderID == "" {
		p.logger.Warn("cannot schedule dunning: empty payment order ID")
		return nil
	}

	dueAt := time.Now().Add(defaultDunningDelay)
	member := redis.Z{
		Score:  float64(dueAt.Unix()),
		Member: fmt.Sprintf("stripe:%s", paymentOrderID),
	}

	err := p.redis.ZAdd(ctx, dunningRetryZSet, member).Err()
	if err != nil {
		p.logger.Error("failed to schedule dunning retry",
			"payment_order_id", paymentOrderID,
			"error", err)
		return fmt.Errorf("failed to schedule dunning retry: %w", err)
	}

	p.logger.Info("dunning retry scheduled for failed stripe payment",
		"payment_order_id", paymentOrderID,
		"due_at", dueAt)

	return nil
}
