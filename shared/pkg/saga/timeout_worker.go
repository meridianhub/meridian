// Package saga provides saga orchestration runtime and persistence for durable execution.
package saga

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// TimeoutWorkerConfig holds configuration for the timeout worker.
type TimeoutWorkerConfig struct {
	// PollInterval is how often to check for expired suspensions.
	// Default: 1 minute
	PollInterval time.Duration

	// BatchSize is the maximum number of sagas to process in a single poll.
	// Default: 100
	BatchSize int
}

// DefaultTimeoutWorkerConfig returns the default timeout worker configuration.
func DefaultTimeoutWorkerConfig() *TimeoutWorkerConfig {
	return &TimeoutWorkerConfig{
		PollInterval: 1 * time.Minute,
		BatchSize:    100,
	}
}

// TimeoutWorker periodically checks for expired suspended sagas and transitions
// them to FAILED status. This ensures sagas don't wait indefinitely for external
// events that may never arrive.
type TimeoutWorker struct {
	db     *gorm.DB
	config *TimeoutWorkerConfig
	logger *slog.Logger
}

// NewTimeoutWorker creates a new TimeoutWorker.
func NewTimeoutWorker(db *gorm.DB, config *TimeoutWorkerConfig) *TimeoutWorker {
	if config == nil {
		config = DefaultTimeoutWorkerConfig()
	}
	// Guard against invalid configuration values
	if config.PollInterval <= 0 {
		config.PollInterval = DefaultTimeoutWorkerConfig().PollInterval
	}
	if config.BatchSize <= 0 {
		config.BatchSize = DefaultTimeoutWorkerConfig().BatchSize
	}
	return &TimeoutWorker{
		db:     db,
		config: config,
		logger: slog.Default(),
	}
}

// WithLogger sets the logger for the timeout worker.
func (w *TimeoutWorker) WithLogger(logger *slog.Logger) *TimeoutWorker {
	w.logger = logger
	return w
}

// Start begins the timeout worker loop. It runs until the context is cancelled.
func (w *TimeoutWorker) Start(ctx context.Context) error {
	w.logger.Info("timeout worker starting",
		"poll_interval", w.config.PollInterval,
		"batch_size", w.config.BatchSize,
	)

	ticker := time.NewTicker(w.config.PollInterval)
	defer ticker.Stop()

	// Run immediately on start
	if err := w.processExpiredSuspensions(ctx); err != nil {
		w.logger.Error("failed to process expired suspensions on startup", "error", err)
	}

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("timeout worker stopping")
			return ctx.Err()
		case <-ticker.C:
			if err := w.processExpiredSuspensions(ctx); err != nil {
				w.logger.Error("failed to process expired suspensions", "error", err)
				// Continue running - don't exit on transient errors
			}
		}
	}
}

// processExpiredSuspensions finds and fails sagas that have exceeded their suspend timeout.
// expiredSaga holds the result of the timeout query.
type expiredSaga struct {
	ID             uuid.UUID `gorm:"column:id"`
	CorrelationID  uuid.UUID `gorm:"column:correlation_id"`
	IdempotencyKey string    `gorm:"column:idempotency_key"`
}

func (w *TimeoutWorker) processExpiredSuspensions(ctx context.Context) error {
	now := time.Now()

	var expired []expiredSaga

	err := w.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := w.findExpiredSuspensions(tx, now, &expired); err != nil {
			return err
		}
		if len(expired) == 0 {
			return nil
		}
		return w.failExpiredSagas(tx, expired, now)
	})
	if err != nil {
		return err
	}

	w.logExpiredSuspensions(expired)
	return nil
}

// findExpiredSuspensions queries for sagas whose suspend timeout has elapsed, locking them for update.
func (w *TimeoutWorker) findExpiredSuspensions(tx *gorm.DB, now time.Time, dest interface{}) error {
	// CockroachDB requires FOR UPDATE at the top level of a SELECT.
	// SKIP LOCKED is omitted because CockroachDB's serializable isolation
	// silently returns empty results when it's used.
	return tx.Raw(`
		SELECT id, correlation_id, suspend_data->>'idempotency_key' as idempotency_key
		FROM saga_instances
		WHERE status = ?
		  AND (suspend_data->>'timeout_at')::timestamptz < ?
		ORDER BY id
		FOR UPDATE
		LIMIT ?
	`,
		SagaStatusWaitingForEvent,
		now,
		w.config.BatchSize,
	).Scan(dest).Error
}

// failExpiredSagas transitions expired sagas and their step results to FAILED status.
func (w *TimeoutWorker) failExpiredSagas(tx *gorm.DB, expired []expiredSaga, now time.Time) error {
	ids := make([]uuid.UUID, len(expired))
	for i, s := range expired {
		ids[i] = s.ID
	}

	if err := tx.Model(&SagaInstance{}).
		Where("id IN ?", ids).
		Updates(map[string]interface{}{
			"status":         SagaStatusFailed,
			"error_message":  "Suspend timeout exceeded - external event not received within deadline",
			"error_category": string(ErrorCategoryFatal),
			"updated_at":     now,
			"suspend_reason": nil,
			"suspend_data":   nil,
		}).Error; err != nil {
		return fmt.Errorf("failed to update expired sagas: %w", err)
	}

	for _, saga := range expired {
		stepUpdate := tx.Model(&SagaStepResult{}).
			Where("saga_instance_id = ? AND status = ?", saga.ID, StepStatusSuspended).
			Updates(map[string]interface{}{
				"status":         StepStatusFailed,
				"error":          "Timeout waiting for external event",
				"error_category": string(ErrorCategoryFatal),
				"updated_at":     now,
			})
		if stepUpdate.Error != nil {
			return fmt.Errorf("failed to update step result for saga %s: %w", saga.ID, stepUpdate.Error)
		}
	}

	return nil
}

// logExpiredSuspensions records metrics and logs for each timed-out saga.
func (w *TimeoutWorker) logExpiredSuspensions(expired []expiredSaga) {
	for _, saga := range expired {
		w.logger.Warn("saga suspension timed out",
			"saga_id", saga.ID,
			"correlation_id", saga.CorrelationID,
			"idempotency_key", saga.IdempotencyKey,
		)
		RecordSuspendTimeout()
	}

	if len(expired) > 0 {
		w.logger.Info("processed expired suspensions",
			"count", len(expired),
		)
	}
}

// ProcessExpiredSuspensions is exposed for testing - allows manual trigger of timeout processing.
func (w *TimeoutWorker) ProcessExpiredSuspensions(ctx context.Context) error {
	return w.processExpiredSuspensions(ctx)
}
