// Package events provides transactional outbox pattern implementation for reliable event delivery.
//
// The outbox pattern ensures at-least-once delivery of events by storing them in a database table
// within the same transaction as the business operation. A background worker then processes
// these events and publishes them to Kafka.
//
// This is particularly important for audit-critical control operations (SUSPEND, RESUME, TERMINATE)
// where event loss would result in incomplete audit trails.
package events

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Outbox status values.
const (
	StatusPending    = "pending"
	StatusProcessing = "processing"
	StatusCompleted  = "completed"
	StatusFailed     = "failed"
)

// Errors returned by the outbox package.
var (
	// ErrOutboxEntryNotFound is returned when an outbox entry cannot be found.
	ErrOutboxEntryNotFound = errors.New("outbox entry not found")

	// ErrNilTransaction is returned when a nil transaction is passed.
	ErrNilTransaction = errors.New("transaction cannot be nil")

	// ErrInvalidEventType is returned when an invalid event type is provided.
	ErrInvalidEventType = errors.New("invalid event type")

	// ErrNilEvent is returned when a nil event is provided to publish.
	ErrNilEvent = errors.New("event cannot be nil")

	// ErrEmptyTopic is returned when an empty topic is provided.
	ErrEmptyTopic = errors.New("topic cannot be empty")

	// ErrEmptyAggregateID is returned when an empty aggregate ID is provided.
	ErrEmptyAggregateID = errors.New("aggregate ID cannot be empty")

	// ErrEmptyAggregateType is returned when an empty aggregate type is provided.
	ErrEmptyAggregateType = errors.New("aggregate type cannot be empty")

	// ErrEmptyServiceName is returned when an empty service name is provided.
	ErrEmptyServiceName = errors.New("service name cannot be empty")
)

// EventOutbox represents an event waiting to be published to Kafka.
// Events are written to the outbox within the same database transaction as the business operation,
// ensuring atomicity. The background worker then processes these events asynchronously.
type EventOutbox struct {
	ID            uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	EventType     string     `gorm:"size:200;not null;index" json:"event_type"`
	AggregateID   string     `gorm:"size:100;not null;index" json:"aggregate_id"`
	AggregateType string     `gorm:"size:100;not null;index" json:"aggregate_type"`
	EventPayload  []byte     `gorm:"type:bytea;not null" json:"event_payload"` // Serialized protobuf
	CorrelationID string     `gorm:"size:100;index" json:"correlation_id,omitempty"`
	CausationID   string     `gorm:"size:100" json:"causation_id,omitempty"`
	Status        string     `gorm:"size:20;not null;index" json:"status"`
	Topic         string     `gorm:"size:200;not null" json:"topic"`
	PartitionKey  string     `gorm:"size:200" json:"partition_key,omitempty"`
	CreatedAt     time.Time  `gorm:"not null;index" json:"created_at"`
	ProcessedAt   *time.Time `gorm:"index" json:"processed_at,omitempty"`
	RetryCount    int        `gorm:"not null" json:"retry_count"`
	LastError     *string    `gorm:"type:text" json:"last_error,omitempty"`
	ServiceName   string     `gorm:"size:100;not null;index" json:"service_name"`
	TenantID      string     `gorm:"size:100;not null;index" json:"tenant_id"`
}

// TableName returns the table name for EventOutbox.
// Uses singular unqualified name to allow PostgreSQL search_path to route queries.
func (EventOutbox) TableName() string {
	return "event_outbox"
}

// OutboxRepository defines the interface for managing event outbox entries.
//
// NOTE: PgxOutboxRepository (in outbox_pgx.go) intentionally does not implement this interface
// because it uses pgx.Tx instead of *gorm.DB for the Insert method. This is by design to allow
// services using pgx to write events within their native transaction type. For worker operations
// (FetchAndLockForProcessing, MarkCompleted, etc.), PgxOutboxRepository provides compatible methods.
type OutboxRepository interface {
	// Insert adds a new event to the outbox within an existing transaction.
	// This should be called within the same transaction as the business operation.
	Insert(ctx context.Context, tx *gorm.DB, entry *EventOutbox) error

	// FetchUnprocessed retrieves a batch of unprocessed entries for a specific service.
	// Entries are ordered by created_at for FIFO processing.
	// Note: For multi-worker deployments, prefer FetchAndLockForProcessing.
	FetchUnprocessed(ctx context.Context, serviceName string, limit int) ([]EventOutbox, error)

	// FetchAndLockForProcessing atomically fetches pending entries and marks them as processing.
	// Uses SELECT FOR UPDATE SKIP LOCKED to prevent race conditions in multi-worker deployments.
	FetchAndLockForProcessing(ctx context.Context, serviceName string, limit int) ([]EventOutbox, error)

	// MarkProcessing atomically updates entries to 'processing' status.
	// Returns the number of entries updated.
	// Note: When using FetchAndLockForProcessing, this is called internally.
	MarkProcessing(ctx context.Context, ids []uuid.UUID) (int64, error)

	// MarkCompleted marks an entry as successfully processed.
	MarkCompleted(ctx context.Context, id uuid.UUID) error

	// MarkFailed increments retry count and updates error message.
	// If retries are exhausted, status is set to 'failed'.
	MarkFailed(ctx context.Context, id uuid.UUID, err error, maxRetries int) error

	// GetPendingCount returns the number of pending entries for observability.
	GetPendingCount(ctx context.Context, serviceName string) (int64, error)

	// ResetStuckEntries resets entries stuck in 'processing' state for too long.
	ResetStuckEntries(ctx context.Context, serviceName string, olderThan time.Duration) (int64, error)
}

// Compile-time interface verification
var _ OutboxRepository = (*PostgresOutboxRepository)(nil)

// PostgresOutboxRepository implements OutboxRepository using PostgreSQL via GORM.
type PostgresOutboxRepository struct {
	db *gorm.DB
}

// NewPostgresOutboxRepository creates a new PostgreSQL-backed outbox repository.
func NewPostgresOutboxRepository(db *gorm.DB) *PostgresOutboxRepository {
	return &PostgresOutboxRepository{db: db}
}

// Insert adds a new event to the outbox within an existing transaction.
func (r *PostgresOutboxRepository) Insert(ctx context.Context, tx *gorm.DB, entry *EventOutbox) error {
	if tx == nil {
		return ErrNilTransaction
	}

	// Generate UUID if not set
	if entry.ID == uuid.Nil {
		entry.ID = uuid.New()
	}

	// Set defaults
	if entry.Status == "" {
		entry.Status = StatusPending
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now()
	}

	return tx.WithContext(ctx).Create(entry).Error
}

// FetchUnprocessed retrieves a batch of unprocessed entries for a specific service.
// Note: For multi-worker deployments, prefer FetchAndLockForProcessing to avoid race conditions.
func (r *PostgresOutboxRepository) FetchUnprocessed(ctx context.Context, serviceName string, limit int) ([]EventOutbox, error) {
	var entries []EventOutbox

	err := r.db.WithContext(ctx).
		Where("status = ?", StatusPending).
		Where("service_name = ?", serviceName).
		Order("created_at ASC").
		Limit(limit).
		Find(&entries).Error
	if err != nil {
		return nil, err
	}

	return entries, nil
}

// FetchAndLockForProcessing atomically fetches pending entries and marks them as processing
// using SELECT FOR UPDATE SKIP LOCKED. This prevents race conditions in multi-worker deployments.
func (r *PostgresOutboxRepository) FetchAndLockForProcessing(ctx context.Context, serviceName string, limit int) ([]EventOutbox, error) {
	var entries []EventOutbox

	// Use a transaction with FOR UPDATE SKIP LOCKED to atomically:
	// 1. Select pending entries (skipping any already locked by other workers)
	// 2. Update their status to 'processing'
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Raw SQL with FOR UPDATE SKIP LOCKED for proper locking
		sql := `
			SELECT * FROM event_outbox
			WHERE status = ? AND service_name = ?
			ORDER BY created_at ASC
			LIMIT ?
			FOR UPDATE SKIP LOCKED`

		if err := tx.Raw(sql, StatusPending, serviceName, limit).Scan(&entries).Error; err != nil {
			return err
		}

		if len(entries) == 0 {
			return nil
		}

		// Extract IDs and update status
		ids := make([]uuid.UUID, len(entries))
		for i, entry := range entries {
			ids[i] = entry.ID
		}

		return tx.Model(&EventOutbox{}).
			Where("id IN ?", ids).
			Update("status", StatusProcessing).Error
	})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch and lock entries: %w", err)
	}

	return entries, nil
}

// MarkProcessing atomically updates entries to 'processing' status.
func (r *PostgresOutboxRepository) MarkProcessing(ctx context.Context, ids []uuid.UUID) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}

	result := r.db.WithContext(ctx).
		Model(&EventOutbox{}).
		Where("id IN ?", ids).
		Where("status = ?", StatusPending).
		Update("status", StatusProcessing)

	return result.RowsAffected, result.Error
}

// MarkCompleted marks an entry as successfully processed.
func (r *PostgresOutboxRepository) MarkCompleted(ctx context.Context, id uuid.UUID) error {
	now := time.Now()
	result := r.db.WithContext(ctx).
		Model(&EventOutbox{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":       StatusCompleted,
			"processed_at": now,
		})

	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrOutboxEntryNotFound
	}
	return nil
}

// MarkFailed atomically increments retry count and updates error message.
// Uses atomic SQL to avoid race conditions when multiple workers process the same entry.
func (r *PostgresOutboxRepository) MarkFailed(ctx context.Context, id uuid.UUID, err error, maxRetries int) error {
	errorMsg := err.Error()

	// Use raw SQL for atomic retry_count increment and conditional status update.
	// This avoids the read-then-update race condition.
	sql := `
		UPDATE event_outbox
		SET retry_count = retry_count + 1,
			last_error = ?,
			status = CASE
				WHEN retry_count + 1 >= ? THEN ?
				ELSE ?
			END
		WHERE id = ?`

	result := r.db.WithContext(ctx).Exec(sql, errorMsg, maxRetries, StatusFailed, StatusPending, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrOutboxEntryNotFound
	}
	return nil
}

// GetPendingCount returns the number of pending entries for observability.
func (r *PostgresOutboxRepository) GetPendingCount(ctx context.Context, serviceName string) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&EventOutbox{}).
		Where("status = ?", StatusPending).
		Where("service_name = ?", serviceName).
		Count(&count).Error
	return count, err
}

// ResetStuckEntries resets entries stuck in 'processing' state for too long.
//
// NOTE: This uses created_at as an approximation since we don't track when entries
// entered the 'processing' state. This means very old entries that are legitimately
// being processed (unlikely but possible) might get reset. Use a conservative threshold
// (e.g., 5+ minutes) to minimize this risk. In practice, events should be processed
// within seconds, so the 5-minute default is safe.
func (r *PostgresOutboxRepository) ResetStuckEntries(ctx context.Context, serviceName string, olderThan time.Duration) (int64, error) {
	threshold := time.Now().Add(-olderThan)

	result := r.db.WithContext(ctx).
		Model(&EventOutbox{}).
		Where("status = ?", StatusProcessing).
		Where("service_name = ?", serviceName).
		Where("created_at < ?", threshold).
		Update("status", StatusPending)

	return result.RowsAffected, result.Error
}

// NewEventOutbox creates a new EventOutbox entry with the given parameters.
// This is a helper function to ensure all required fields are set correctly.
func NewEventOutbox(
	eventType string,
	aggregateID string,
	aggregateType string,
	payload []byte,
	topic string,
	serviceName string,
	correlationID string,
	tenantID string,
) *EventOutbox {
	return &EventOutbox{
		ID:            uuid.New(),
		EventType:     eventType,
		AggregateID:   aggregateID,
		AggregateType: aggregateType,
		EventPayload:  payload,
		Topic:         topic,
		PartitionKey:  aggregateID, // Use aggregate ID as partition key by default
		ServiceName:   serviceName,
		CorrelationID: correlationID,
		TenantID:      tenantID,
		Status:        StatusPending,
		CreatedAt:     time.Now(),
	}
}
