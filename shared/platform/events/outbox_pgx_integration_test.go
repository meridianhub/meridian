package events

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
)

// setupPgxOutboxRepo creates a pgxpool.Pool with the event_outbox table for testing.
func setupPgxOutboxRepo(t *testing.T) (*PgxOutboxRepository, func()) {
	t.Helper()

	pool := testdb.NewTestPool(t)

	// Create the event_outbox table manually.
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS event_outbox (
			id UUID PRIMARY KEY,
			event_type VARCHAR(200) NOT NULL,
			aggregate_id VARCHAR(100) NOT NULL,
			aggregate_type VARCHAR(100) NOT NULL,
			event_payload BYTEA NOT NULL,
			correlation_id VARCHAR(100),
			causation_id VARCHAR(100),
			status VARCHAR(20) NOT NULL,
			topic VARCHAR(200) NOT NULL,
			partition_key VARCHAR(200),
			created_at TIMESTAMPTZ NOT NULL,
			processed_at TIMESTAMPTZ,
			retry_count INT NOT NULL DEFAULT 0,
			last_error TEXT,
			service_name VARCHAR(100) NOT NULL,
			tenant_id VARCHAR(100) NOT NULL DEFAULT ''
		)
	`)
	require.NoError(t, err)

	repo := NewPgxOutboxRepository(pool)
	cleanup := func() {
		pool.Close()
	}

	return repo, cleanup
}

func TestPgxOutboxRepository_InsertWithPgxTx(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := testdb.NewTestPool(t)
	ctx := context.Background()

	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS event_outbox (
			id UUID PRIMARY KEY,
			event_type VARCHAR(200) NOT NULL,
			aggregate_id VARCHAR(100) NOT NULL,
			aggregate_type VARCHAR(100) NOT NULL,
			event_payload BYTEA NOT NULL,
			correlation_id VARCHAR(100),
			causation_id VARCHAR(100),
			status VARCHAR(20) NOT NULL,
			topic VARCHAR(200) NOT NULL,
			partition_key VARCHAR(200),
			created_at TIMESTAMPTZ NOT NULL,
			processed_at TIMESTAMPTZ,
			retry_count INT NOT NULL DEFAULT 0,
			last_error TEXT,
			service_name VARCHAR(100) NOT NULL,
			tenant_id VARCHAR(100) NOT NULL DEFAULT ''
		)
	`)
	require.NoError(t, err)

	repo := NewPgxOutboxRepository(pool)

	t.Run("successful insert", func(t *testing.T) {
		tx, err := pool.Begin(ctx)
		require.NoError(t, err)

		entry := NewEventOutbox("event.type", "agg-1", "Type", []byte(`{"test":1}`), "topic", "service", "corr-1", "tenant-1")
		err = repo.InsertWithPgxTx(ctx, tx, entry)
		require.NoError(t, err)

		err = tx.Commit(ctx)
		require.NoError(t, err)

		// Verify persisted
		var count int
		err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM event_outbox WHERE id = $1", entry.ID).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count)
	})

	t.Run("nil transaction returns error", func(t *testing.T) {
		entry := NewEventOutbox("event.type", "agg-2", "Type", []byte(`{}`), "topic", "service", "", "")
		err := repo.InsertWithPgxTx(ctx, nil, entry)
		assert.ErrorIs(t, err, ErrNilTransaction)
	})

	t.Run("generates UUID if nil", func(t *testing.T) {
		tx, err := pool.Begin(ctx)
		require.NoError(t, err)

		entry := &EventOutbox{
			EventType:     "event.type",
			AggregateID:   "agg-3",
			AggregateType: "Type",
			EventPayload:  []byte(`{}`),
			Topic:         "topic",
			ServiceName:   "service",
		}
		err = repo.InsertWithPgxTx(ctx, tx, entry)
		require.NoError(t, err)
		assert.NotEqual(t, uuid.Nil, entry.ID)
		assert.Equal(t, StatusPending, entry.Status)
		assert.False(t, entry.CreatedAt.IsZero())

		err = tx.Commit(ctx)
		require.NoError(t, err)
	})
}

func TestPgxOutboxRepository_FetchUnprocessed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	repo, cleanup := setupPgxOutboxRepo(t)
	defer cleanup()

	ctx := context.Background()

	// Insert test entries directly.
	for i := 0; i < 5; i++ {
		entry := NewEventOutbox("event.type", "agg", "Type", []byte(`{}`), "topic", "pgx-svc", "", "")
		_, err := repo.pool.Exec(ctx,
			`INSERT INTO event_outbox (id, event_type, aggregate_id, aggregate_type, event_payload, status, topic, created_at, retry_count, service_name, tenant_id)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			entry.ID, entry.EventType, entry.AggregateID, entry.AggregateType, entry.EventPayload,
			StatusPending, entry.Topic, entry.CreatedAt, 0, entry.ServiceName, "",
		)
		require.NoError(t, err)
	}

	t.Run("fetches pending entries", func(t *testing.T) {
		entries, err := repo.FetchUnprocessed(ctx, "pgx-svc", 10)
		require.NoError(t, err)
		assert.Len(t, entries, 5)
	})

	t.Run("respects limit", func(t *testing.T) {
		entries, err := repo.FetchUnprocessed(ctx, "pgx-svc", 3)
		require.NoError(t, err)
		assert.Len(t, entries, 3)
	})

	t.Run("filters by service name", func(t *testing.T) {
		entries, err := repo.FetchUnprocessed(ctx, "other-svc", 10)
		require.NoError(t, err)
		assert.Empty(t, entries)
	})
}

func TestPgxOutboxRepository_FetchAndLockForProcessing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	repo, cleanup := setupPgxOutboxRepo(t)
	defer cleanup()

	ctx := context.Background()

	// Insert entries.
	for i := 0; i < 3; i++ {
		entry := NewEventOutbox("event.type", "agg", "Type", []byte(`{}`), "topic", "lock-svc", "", "")
		_, err := repo.pool.Exec(ctx,
			`INSERT INTO event_outbox (id, event_type, aggregate_id, aggregate_type, event_payload, status, topic, created_at, retry_count, service_name, tenant_id)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			entry.ID, entry.EventType, entry.AggregateID, entry.AggregateType, entry.EventPayload,
			StatusPending, entry.Topic, entry.CreatedAt, 0, entry.ServiceName, "",
		)
		require.NoError(t, err)
	}

	t.Run("fetches and marks as processing", func(t *testing.T) {
		entries, err := repo.FetchAndLockForProcessing(ctx, "lock-svc", 10)
		require.NoError(t, err)
		assert.Len(t, entries, 3)

		// Verify entries are now in processing state.
		var processingCount int
		err = repo.pool.QueryRow(ctx, "SELECT COUNT(*) FROM event_outbox WHERE status = $1 AND service_name = $2", StatusProcessing, "lock-svc").Scan(&processingCount)
		require.NoError(t, err)
		assert.Equal(t, 3, processingCount)
	})

	t.Run("returns empty when no pending entries", func(t *testing.T) {
		entries, err := repo.FetchAndLockForProcessing(ctx, "lock-svc", 10)
		require.NoError(t, err)
		assert.Empty(t, entries)
	})
}

func TestPgxOutboxRepository_MarkProcessing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	repo, cleanup := setupPgxOutboxRepo(t)
	defer cleanup()

	ctx := context.Background()

	entry := NewEventOutbox("event.type", "agg", "Type", []byte(`{}`), "topic", "mark-svc", "", "")
	_, err := repo.pool.Exec(ctx,
		`INSERT INTO event_outbox (id, event_type, aggregate_id, aggregate_type, event_payload, status, topic, created_at, retry_count, service_name, tenant_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		entry.ID, entry.EventType, entry.AggregateID, entry.AggregateType, entry.EventPayload,
		StatusPending, entry.Topic, entry.CreatedAt, 0, entry.ServiceName, "",
	)
	require.NoError(t, err)

	t.Run("marks pending entries as processing", func(t *testing.T) {
		count, err := repo.MarkProcessing(ctx, []uuid.UUID{entry.ID})
		require.NoError(t, err)
		assert.Equal(t, int64(1), count)
	})

	t.Run("empty ids returns zero", func(t *testing.T) {
		count, err := repo.MarkProcessing(ctx, []uuid.UUID{})
		require.NoError(t, err)
		assert.Equal(t, int64(0), count)
	})
}

func TestPgxOutboxRepository_MarkCompleted(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	repo, cleanup := setupPgxOutboxRepo(t)
	defer cleanup()

	ctx := context.Background()

	entry := NewEventOutbox("event.type", "agg", "Type", []byte(`{}`), "topic", "svc", "", "")
	_, err := repo.pool.Exec(ctx,
		`INSERT INTO event_outbox (id, event_type, aggregate_id, aggregate_type, event_payload, status, topic, created_at, retry_count, service_name, tenant_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		entry.ID, entry.EventType, entry.AggregateID, entry.AggregateType, entry.EventPayload,
		StatusProcessing, entry.Topic, entry.CreatedAt, 0, entry.ServiceName, "",
	)
	require.NoError(t, err)

	t.Run("marks entry as completed", func(t *testing.T) {
		err := repo.MarkCompleted(ctx, entry.ID)
		require.NoError(t, err)

		var status string
		err = repo.pool.QueryRow(ctx, "SELECT status FROM event_outbox WHERE id = $1", entry.ID).Scan(&status)
		require.NoError(t, err)
		assert.Equal(t, StatusCompleted, status)
	})

	t.Run("non-existent entry returns error", func(t *testing.T) {
		err := repo.MarkCompleted(ctx, uuid.New())
		assert.ErrorIs(t, err, ErrOutboxEntryNotFound)
	})
}

func TestPgxOutboxRepository_MarkFailed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	repo, cleanup := setupPgxOutboxRepo(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("increments retry and resets to pending", func(t *testing.T) {
		entry := NewEventOutbox("event.type", "agg", "Type", []byte(`{}`), "topic", "svc", "", "")
		_, err := repo.pool.Exec(ctx,
			`INSERT INTO event_outbox (id, event_type, aggregate_id, aggregate_type, event_payload, status, topic, created_at, retry_count, service_name, tenant_id)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			entry.ID, entry.EventType, entry.AggregateID, entry.AggregateType, entry.EventPayload,
			StatusProcessing, entry.Topic, entry.CreatedAt, 0, entry.ServiceName, "",
		)
		require.NoError(t, err)

		err = repo.MarkFailed(ctx, entry.ID, errors.New("test error"), 5)
		require.NoError(t, err)

		var status string
		var retryCount int
		err = repo.pool.QueryRow(ctx, "SELECT status, retry_count FROM event_outbox WHERE id = $1", entry.ID).Scan(&status, &retryCount)
		require.NoError(t, err)
		assert.Equal(t, StatusPending, status)
		assert.Equal(t, 1, retryCount)
	})

	t.Run("marks as failed when retries exhausted", func(t *testing.T) {
		entry := NewEventOutbox("event.type", "agg", "Type", []byte(`{}`), "topic", "svc", "", "")
		_, err := repo.pool.Exec(ctx,
			`INSERT INTO event_outbox (id, event_type, aggregate_id, aggregate_type, event_payload, status, topic, created_at, retry_count, service_name, tenant_id)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			entry.ID, entry.EventType, entry.AggregateID, entry.AggregateType, entry.EventPayload,
			StatusProcessing, entry.Topic, entry.CreatedAt, 4, entry.ServiceName, "",
		)
		require.NoError(t, err)

		err = repo.MarkFailed(ctx, entry.ID, errors.New("final error"), 5)
		require.NoError(t, err)

		var status string
		err = repo.pool.QueryRow(ctx, "SELECT status FROM event_outbox WHERE id = $1", entry.ID).Scan(&status)
		require.NoError(t, err)
		assert.Equal(t, StatusFailed, status)
	})

	t.Run("non-existent entry returns error", func(t *testing.T) {
		err := repo.MarkFailed(ctx, uuid.New(), errors.New("err"), 5)
		assert.ErrorIs(t, err, ErrOutboxEntryNotFound)
	})
}

func TestPgxOutboxRepository_GetPendingCount(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	repo, cleanup := setupPgxOutboxRepo(t)
	defer cleanup()

	ctx := context.Background()

	// Insert mixed entries.
	for i := 0; i < 3; i++ {
		entry := NewEventOutbox("event.type", "agg", "Type", []byte(`{}`), "topic", "count-svc", "", "")
		_, err := repo.pool.Exec(ctx,
			`INSERT INTO event_outbox (id, event_type, aggregate_id, aggregate_type, event_payload, status, topic, created_at, retry_count, service_name, tenant_id)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			entry.ID, entry.EventType, entry.AggregateID, entry.AggregateType, entry.EventPayload,
			StatusPending, entry.Topic, entry.CreatedAt, 0, entry.ServiceName, "",
		)
		require.NoError(t, err)
	}

	// Insert one completed entry.
	entry := NewEventOutbox("event.type", "agg", "Type", []byte(`{}`), "topic", "count-svc", "", "")
	_, err := repo.pool.Exec(ctx,
		`INSERT INTO event_outbox (id, event_type, aggregate_id, aggregate_type, event_payload, status, topic, created_at, retry_count, service_name, tenant_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		entry.ID, entry.EventType, entry.AggregateID, entry.AggregateType, entry.EventPayload,
		StatusCompleted, entry.Topic, entry.CreatedAt, 0, entry.ServiceName, "",
	)
	require.NoError(t, err)

	count, err := repo.GetPendingCount(ctx, "count-svc")
	require.NoError(t, err)
	assert.Equal(t, int64(3), count)
}

func TestPgxOutboxRepository_ResetStuckEntries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	repo, cleanup := setupPgxOutboxRepo(t)
	defer cleanup()

	ctx := context.Background()

	// Old stuck entry.
	oldEntry := NewEventOutbox("event.type", "agg-old", "Type", []byte(`{}`), "topic", "stuck-svc", "", "")
	_, err := repo.pool.Exec(ctx,
		`INSERT INTO event_outbox (id, event_type, aggregate_id, aggregate_type, event_payload, status, topic, created_at, retry_count, service_name, tenant_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		oldEntry.ID, oldEntry.EventType, oldEntry.AggregateID, oldEntry.AggregateType, oldEntry.EventPayload,
		StatusProcessing, oldEntry.Topic, time.Now().Add(-10*time.Minute), 0, oldEntry.ServiceName, "",
	)
	require.NoError(t, err)

	// Recent processing entry (should NOT be reset).
	newEntry := NewEventOutbox("event.type", "agg-new", "Type", []byte(`{}`), "topic", "stuck-svc", "", "")
	_, err = repo.pool.Exec(ctx,
		`INSERT INTO event_outbox (id, event_type, aggregate_id, aggregate_type, event_payload, status, topic, created_at, retry_count, service_name, tenant_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		newEntry.ID, newEntry.EventType, newEntry.AggregateID, newEntry.AggregateType, newEntry.EventPayload,
		StatusProcessing, newEntry.Topic, time.Now(), 0, newEntry.ServiceName, "",
	)
	require.NoError(t, err)

	count, err := repo.ResetStuckEntries(ctx, "stuck-svc", 5*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	// Verify old entry was reset.
	var oldStatus string
	err = repo.pool.QueryRow(ctx, "SELECT status FROM event_outbox WHERE id = $1", oldEntry.ID).Scan(&oldStatus)
	require.NoError(t, err)
	assert.Equal(t, StatusPending, oldStatus)

	// Verify new entry was NOT reset.
	var newStatus string
	err = repo.pool.QueryRow(ctx, "SELECT status FROM event_outbox WHERE id = $1", newEntry.ID).Scan(&newStatus)
	require.NoError(t, err)
	assert.Equal(t, StatusProcessing, newStatus)
}

func TestPgxOutboxPublisher_Publish_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := testdb.NewTestPool(t)
	ctx := context.Background()

	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS event_outbox (
			id UUID PRIMARY KEY,
			event_type VARCHAR(200) NOT NULL,
			aggregate_id VARCHAR(100) NOT NULL,
			aggregate_type VARCHAR(100) NOT NULL,
			event_payload BYTEA NOT NULL,
			correlation_id VARCHAR(100),
			causation_id VARCHAR(100),
			status VARCHAR(20) NOT NULL,
			topic VARCHAR(200) NOT NULL,
			partition_key VARCHAR(200),
			created_at TIMESTAMPTZ NOT NULL,
			processed_at TIMESTAMPTZ,
			retry_count INT NOT NULL DEFAULT 0,
			last_error TEXT,
			service_name VARCHAR(100) NOT NULL,
			tenant_id VARCHAR(100) NOT NULL DEFAULT ''
		)
	`)
	require.NoError(t, err)

	publisher := NewPgxOutboxPublisher("test-service")

	t.Run("successful publish with all fields", func(t *testing.T) {
		tenantCtx := tenant.WithTenant(ctx, "test-tenant")

		tx, err := pool.Begin(tenantCtx)
		require.NoError(t, err)

		event := timestamppb.Now()
		err = publisher.Publish(tenantCtx, tx, event, PublishConfig{
			EventType:     "test.event.v1",
			AggregateID:   "agg-1",
			AggregateType: "TestAggregate",
			Topic:         "test-topic",
			CorrelationID: "corr-1",
			CausationID:   "cause-1",
			PartitionKey:  "custom-key",
		})
		require.NoError(t, err)

		err = tx.Commit(tenantCtx)
		require.NoError(t, err)

		var count int
		err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM event_outbox WHERE aggregate_id = $1", "agg-1").Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count)
	})

	t.Run("nil event returns error", func(t *testing.T) {
		tx, err := pool.Begin(ctx)
		require.NoError(t, err)
		defer tx.Rollback(ctx)

		err = publisher.Publish(ctx, tx, nil, PublishConfig{
			EventType:     "test.event.v1",
			AggregateID:   "agg-1",
			AggregateType: "Type",
			Topic:         "topic",
		})
		assert.ErrorIs(t, err, ErrNilEvent)
	})

	t.Run("empty event type returns error", func(t *testing.T) {
		tx, err := pool.Begin(ctx)
		require.NoError(t, err)
		defer tx.Rollback(ctx)

		err = publisher.Publish(ctx, tx, timestamppb.Now(), PublishConfig{
			AggregateID:   "agg-1",
			AggregateType: "Type",
			Topic:         "topic",
		})
		assert.ErrorIs(t, err, ErrInvalidEventType)
	})

	t.Run("empty topic returns error", func(t *testing.T) {
		tx, err := pool.Begin(ctx)
		require.NoError(t, err)
		defer tx.Rollback(ctx)

		err = publisher.Publish(ctx, tx, timestamppb.Now(), PublishConfig{
			EventType:     "test.event.v1",
			AggregateID:   "agg-1",
			AggregateType: "Type",
		})
		assert.ErrorIs(t, err, ErrEmptyTopic)
	})

	t.Run("empty aggregate ID returns error", func(t *testing.T) {
		tx, err := pool.Begin(ctx)
		require.NoError(t, err)
		defer tx.Rollback(ctx)

		err = publisher.Publish(ctx, tx, timestamppb.Now(), PublishConfig{
			EventType:     "test.event.v1",
			AggregateType: "Type",
			Topic:         "topic",
		})
		assert.ErrorIs(t, err, ErrEmptyAggregateID)
	})

	t.Run("empty aggregate type returns error", func(t *testing.T) {
		tx, err := pool.Begin(ctx)
		require.NoError(t, err)
		defer tx.Rollback(ctx)

		err = publisher.Publish(ctx, tx, timestamppb.Now(), PublishConfig{
			EventType:   "test.event.v1",
			AggregateID: "agg-1",
			Topic:       "topic",
		})
		assert.ErrorIs(t, err, ErrEmptyAggregateType)
	})

	t.Run("defaults partition key to aggregate ID", func(t *testing.T) {
		tx, err := pool.Begin(ctx)
		require.NoError(t, err)

		err = publisher.Publish(ctx, tx, timestamppb.Now(), PublishConfig{
			EventType:     "test.default.v1",
			AggregateID:   "agg-default",
			AggregateType: "Type",
			Topic:         "topic",
		})
		require.NoError(t, err)

		err = tx.Commit(ctx)
		require.NoError(t, err)

		var partitionKey *string
		err = pool.QueryRow(ctx, "SELECT partition_key FROM event_outbox WHERE aggregate_id = $1", "agg-default").Scan(&partitionKey)
		require.NoError(t, err)
		require.NotNil(t, partitionKey)
		assert.Equal(t, "agg-default", *partitionKey)
	})
}

// NOTE: GORM FetchAndLockForProcessing is not tested here because GORM's Raw().Scan()
// with SELECT * has compatibility issues with CockroachDB column mapping in testcontainers.
// The pgx version of FetchAndLockForProcessing is tested above and covers the same logic.
