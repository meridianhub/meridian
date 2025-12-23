package events

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"
)

// PgxOutboxRepository implements OutboxRepository using PostgreSQL via pgx.
// This implementation is designed to work with services that use pgx for database access,
// allowing events to be written atomically within the same pgx transaction as business operations.
type PgxOutboxRepository struct {
	pool *pgxpool.Pool
}

// NewPgxOutboxRepository creates a new pgx-backed outbox repository.
func NewPgxOutboxRepository(pool *pgxpool.Pool) *PgxOutboxRepository {
	return &PgxOutboxRepository{pool: pool}
}

// InsertWithPgxTx adds a new event to the outbox within an existing pgx transaction.
// This method enables atomic writes with business operations using pgx.
func (r *PgxOutboxRepository) InsertWithPgxTx(ctx context.Context, tx pgx.Tx, entry *EventOutbox) error {
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

	query := `
		INSERT INTO event_outbox (
			id, event_type, aggregate_id, aggregate_type, event_payload,
			correlation_id, causation_id, status, topic, partition_key,
			created_at, retry_count, service_name
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9, $10,
			$11, $12, $13
		)`

	_, err := tx.Exec(ctx, query,
		entry.ID, entry.EventType, entry.AggregateID, entry.AggregateType, entry.EventPayload,
		nullableString(entry.CorrelationID), nullableString(entry.CausationID),
		entry.Status, entry.Topic, nullableString(entry.PartitionKey),
		entry.CreatedAt, entry.RetryCount, entry.ServiceName,
	)
	if err != nil {
		return fmt.Errorf("failed to insert outbox entry: %w", err)
	}

	return nil
}

// FetchUnprocessed retrieves a batch of unprocessed entries for a specific service.
// Entries are ordered by created_at for FIFO processing.
func (r *PgxOutboxRepository) FetchUnprocessed(ctx context.Context, serviceName string, limit int) ([]EventOutbox, error) {
	query := `
		SELECT id, event_type, aggregate_id, aggregate_type, event_payload,
			correlation_id, causation_id, status, topic, partition_key,
			created_at, processed_at, retry_count, last_error, service_name
		FROM event_outbox
		WHERE status = $1 AND service_name = $2
		ORDER BY created_at ASC
		LIMIT $3`

	rows, err := r.pool.Query(ctx, query, StatusPending, serviceName, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch unprocessed entries: %w", err)
	}
	defer rows.Close()

	var entries []EventOutbox
	for rows.Next() {
		var entry EventOutbox
		var correlationID, causationID, partitionKey, lastError *string
		var processedAt *time.Time

		err := rows.Scan(
			&entry.ID, &entry.EventType, &entry.AggregateID, &entry.AggregateType, &entry.EventPayload,
			&correlationID, &causationID, &entry.Status, &entry.Topic, &partitionKey,
			&entry.CreatedAt, &processedAt, &entry.RetryCount, &lastError, &entry.ServiceName,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan outbox entry: %w", err)
		}

		if correlationID != nil {
			entry.CorrelationID = *correlationID
		}
		if causationID != nil {
			entry.CausationID = *causationID
		}
		if partitionKey != nil {
			entry.PartitionKey = *partitionKey
		}
		if lastError != nil {
			entry.LastError = lastError
		}
		if processedAt != nil {
			entry.ProcessedAt = processedAt
		}

		entries = append(entries, entry)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating outbox entries: %w", err)
	}

	return entries, nil
}

// MarkProcessing atomically updates entries to 'processing' status.
func (r *PgxOutboxRepository) MarkProcessing(ctx context.Context, ids []uuid.UUID) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}

	query := `
		UPDATE event_outbox
		SET status = $1
		WHERE id = ANY($2) AND status = $3`

	result, err := r.pool.Exec(ctx, query, StatusProcessing, ids, StatusPending)
	if err != nil {
		return 0, fmt.Errorf("failed to mark entries as processing: %w", err)
	}

	return result.RowsAffected(), nil
}

// MarkCompleted marks an entry as successfully processed.
func (r *PgxOutboxRepository) MarkCompleted(ctx context.Context, id uuid.UUID) error {
	query := `
		UPDATE event_outbox
		SET status = $1, processed_at = $2
		WHERE id = $3`

	result, err := r.pool.Exec(ctx, query, StatusCompleted, time.Now(), id)
	if err != nil {
		return fmt.Errorf("failed to mark entry as completed: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrOutboxEntryNotFound
	}

	return nil
}

// MarkFailed increments retry count and updates error message.
func (r *PgxOutboxRepository) MarkFailed(ctx context.Context, id uuid.UUID, err error, maxRetries int) error {
	// First get current retry count
	var currentRetryCount int
	querySelect := `SELECT retry_count FROM event_outbox WHERE id = $1`
	selectErr := r.pool.QueryRow(ctx, querySelect, id).Scan(&currentRetryCount)
	if selectErr != nil {
		if errors.Is(selectErr, pgx.ErrNoRows) {
			return ErrOutboxEntryNotFound
		}
		return fmt.Errorf("failed to get current retry count: %w", selectErr)
	}

	newRetryCount := currentRetryCount + 1
	errorMsg := err.Error()

	// Determine new status
	newStatus := StatusPending
	if newRetryCount >= maxRetries {
		newStatus = StatusFailed
	}

	query := `
		UPDATE event_outbox
		SET status = $1, retry_count = $2, last_error = $3
		WHERE id = $4`

	_, updateErr := r.pool.Exec(ctx, query, newStatus, newRetryCount, errorMsg, id)
	if updateErr != nil {
		return fmt.Errorf("failed to mark entry as failed: %w", updateErr)
	}

	return nil
}

// GetPendingCount returns the number of pending entries for observability.
func (r *PgxOutboxRepository) GetPendingCount(ctx context.Context, serviceName string) (int64, error) {
	query := `SELECT COUNT(*) FROM event_outbox WHERE status = $1 AND service_name = $2`

	var count int64
	err := r.pool.QueryRow(ctx, query, StatusPending, serviceName).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to get pending count: %w", err)
	}

	return count, nil
}

// ResetStuckEntries resets entries stuck in 'processing' state for too long.
func (r *PgxOutboxRepository) ResetStuckEntries(ctx context.Context, serviceName string, olderThan time.Duration) (int64, error) {
	threshold := time.Now().Add(-olderThan)

	query := `
		UPDATE event_outbox
		SET status = $1
		WHERE status = $2 AND service_name = $3 AND created_at < $4`

	result, err := r.pool.Exec(ctx, query, StatusPending, StatusProcessing, serviceName, threshold)
	if err != nil {
		return 0, fmt.Errorf("failed to reset stuck entries: %w", err)
	}

	return result.RowsAffected(), nil
}

// PgxOutboxPublisher provides methods for publishing events through the transactional outbox pattern
// using pgx transactions.
type PgxOutboxPublisher struct {
	serviceName string
}

// NewPgxOutboxPublisher creates a new PgxOutboxPublisher for the given service.
func NewPgxOutboxPublisher(serviceName string) *PgxOutboxPublisher {
	return &PgxOutboxPublisher{
		serviceName: serviceName,
	}
}

// Publish writes an event to the outbox table within the provided pgx transaction.
func (p *PgxOutboxPublisher) Publish(
	ctx context.Context,
	tx pgx.Tx,
	event proto.Message,
	config PublishConfig,
) error {
	if tx == nil {
		return ErrNilTransaction
	}
	if event == nil {
		return ErrNilEvent
	}
	if config.EventType == "" {
		return ErrInvalidEventType
	}
	if config.Topic == "" {
		return ErrEmptyTopic
	}

	// Serialize the protobuf event
	payload, err := proto.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to serialize event: %w", err)
	}

	// Use AggregateID as partition key if not specified
	partitionKey := config.PartitionKey
	if partitionKey == "" {
		partitionKey = config.AggregateID
	}

	// Create outbox entry
	entry := NewEventOutbox(
		config.EventType,
		config.AggregateID,
		config.AggregateType,
		payload,
		config.Topic,
		p.serviceName,
		config.CorrelationID,
	)
	entry.CausationID = config.CausationID
	entry.PartitionKey = partitionKey

	// Insert using raw SQL with the pgx transaction
	query := `
		INSERT INTO event_outbox (
			id, event_type, aggregate_id, aggregate_type, event_payload,
			correlation_id, causation_id, status, topic, partition_key,
			created_at, retry_count, service_name
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9, $10,
			$11, $12, $13
		)`

	_, err = tx.Exec(ctx, query,
		entry.ID, entry.EventType, entry.AggregateID, entry.AggregateType, entry.EventPayload,
		nullableString(entry.CorrelationID), nullableString(entry.CausationID),
		entry.Status, entry.Topic, nullableString(entry.PartitionKey),
		entry.CreatedAt, entry.RetryCount, entry.ServiceName,
	)
	if err != nil {
		return fmt.Errorf("failed to insert outbox entry: %w", err)
	}

	return nil
}

// PublishControlEvent is a convenience method for publishing control operation events
// (SUSPEND, RESUME, TERMINATE) using pgx transactions.
func (p *PgxOutboxPublisher) PublishControlEvent(
	ctx context.Context,
	tx pgx.Tx,
	event proto.Message,
	eventType string,
	aggregateID string,
	aggregateType string,
	topic string,
	correlationID string,
) error {
	return p.Publish(ctx, tx, event, PublishConfig{
		EventType:     eventType,
		AggregateID:   aggregateID,
		AggregateType: aggregateType,
		Topic:         topic,
		CorrelationID: correlationID,
	})
}

// nullableString returns nil for empty strings, otherwise returns a pointer to the string.
func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
