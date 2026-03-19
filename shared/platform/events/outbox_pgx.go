package events

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"

	"github.com/meridianhub/meridian/shared/platform/tenant"
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
			created_at, retry_count, service_name, tenant_id
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9, $10,
			$11, $12, $13, $14
		)`

	_, err := tx.Exec(ctx, query,
		entry.ID, entry.EventType, entry.AggregateID, entry.AggregateType, entry.EventPayload,
		nullableString(entry.CorrelationID), nullableString(entry.CausationID),
		entry.Status, entry.Topic, nullableString(entry.PartitionKey),
		entry.CreatedAt, entry.RetryCount, entry.ServiceName, entry.TenantID,
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
			created_at, processed_at, retry_count, last_error, service_name, tenant_id
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
			&entry.CreatedAt, &processedAt, &entry.RetryCount, &lastError, &entry.ServiceName, &entry.TenantID,
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

// FetchAndLockForProcessing atomically fetches pending entries and marks them as processing
// using SELECT FOR UPDATE SKIP LOCKED. This prevents race conditions in multi-worker deployments.
func (r *PgxOutboxRepository) FetchAndLockForProcessing(ctx context.Context, serviceName string, limit int) ([]EventOutbox, error) {
	var entries []EventOutbox

	// Use a transaction with FOR UPDATE SKIP LOCKED to atomically:
	// 1. Select pending entries (skipping any already locked by other workers)
	// 2. Update their status to 'processing'
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit returns ErrTxClosed, safe to ignore

	// Raw SQL with FOR UPDATE SKIP LOCKED for proper locking
	selectQuery := `
		SELECT id, event_type, aggregate_id, aggregate_type, event_payload,
			correlation_id, causation_id, status, topic, partition_key,
			created_at, processed_at, retry_count, last_error, service_name, tenant_id
		FROM event_outbox
		WHERE status = $1 AND service_name = $2
		ORDER BY created_at ASC
		LIMIT $3
		FOR UPDATE SKIP LOCKED`

	rows, err := tx.Query(ctx, selectQuery, StatusPending, serviceName, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch entries: %w", err)
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var entry EventOutbox
		var correlationID, causationID, partitionKey, lastError *string
		var processedAt *time.Time

		scanErr := rows.Scan(
			&entry.ID, &entry.EventType, &entry.AggregateID, &entry.AggregateType, &entry.EventPayload,
			&correlationID, &causationID, &entry.Status, &entry.Topic, &partitionKey,
			&entry.CreatedAt, &processedAt, &entry.RetryCount, &lastError, &entry.ServiceName, &entry.TenantID,
		)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan outbox entry: %w", scanErr)
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
		ids = append(ids, entry.ID)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating outbox entries: %w", err)
	}

	if len(entries) == 0 {
		return entries, nil
	}

	// Update status to processing
	updateQuery := `UPDATE event_outbox SET status = $1 WHERE id = ANY($2)`
	_, err = tx.Exec(ctx, updateQuery, StatusProcessing, ids)
	if err != nil {
		return nil, fmt.Errorf("failed to mark entries as processing: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
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

// MarkFailed atomically increments retry count and updates error message.
// Uses atomic SQL to avoid race conditions when multiple workers process the same entry.
func (r *PgxOutboxRepository) MarkFailed(ctx context.Context, id uuid.UUID, err error, maxRetries int) error {
	errorMsg := err.Error()

	// Use atomic SQL for retry_count increment and conditional status update.
	// This avoids the read-then-update race condition.
	query := `
		UPDATE event_outbox
		SET retry_count = retry_count + 1,
			last_error = $1,
			status = CASE
				WHEN retry_count + 1 >= $2 THEN $3
				ELSE $4
			END
		WHERE id = $5`

	result, updateErr := r.pool.Exec(ctx, query, errorMsg, maxRetries, StatusFailed, StatusPending, id)
	if updateErr != nil {
		return fmt.Errorf("failed to mark entry as failed: %w", updateErr)
	}
	if result.RowsAffected() == 0 {
		return ErrOutboxEntryNotFound
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
//
// NOTE: This uses created_at as an approximation since we don't track when entries
// entered the 'processing' state. This means very old entries that are legitimately
// being processed (unlikely but possible) might get reset. Use a conservative threshold
// (e.g., 5+ minutes) to minimize this risk. In practice, events should be processed
// within seconds, so the 5-minute default is safe.
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

// NewPgxOutboxPublisher creates a new pgx-based outbox publisher.
// Panics if serviceName is empty to fail fast during initialization.
func NewPgxOutboxPublisher(serviceName string) *PgxOutboxPublisher {
	if serviceName == "" {
		panic("events: " + ErrEmptyServiceName.Error())
	}
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
	if config.AggregateID == "" {
		return ErrEmptyAggregateID
	}
	if config.AggregateType == "" {
		return ErrEmptyAggregateType
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

	// Extract tenant ID from context
	var tenantID string
	if tid, ok := tenant.FromContext(ctx); ok {
		tenantID = string(tid)
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
		tenantID,
	)
	entry.CausationID = config.CausationID
	entry.PartitionKey = partitionKey

	// Insert using raw SQL with the pgx transaction
	query := `
		INSERT INTO event_outbox (
			id, event_type, aggregate_id, aggregate_type, event_payload,
			correlation_id, causation_id, status, topic, partition_key,
			created_at, retry_count, service_name, tenant_id
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9, $10,
			$11, $12, $13, $14
		)`

	_, err = tx.Exec(ctx, query,
		entry.ID, entry.EventType, entry.AggregateID, entry.AggregateType, entry.EventPayload,
		nullableString(entry.CorrelationID), nullableString(entry.CausationID),
		entry.Status, entry.Topic, nullableString(entry.PartitionKey),
		entry.CreatedAt, entry.RetryCount, entry.ServiceName, entry.TenantID,
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
