package events

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// Test errors for outbox tests.
var (
	errIntentionalRollback = errors.New("intentional rollback")
	errTestError           = errors.New("test error")
	errFinalError          = errors.New("final error")
	errGeneric             = errors.New("generic error")
)

// setupTestDB creates an in-memory SQLite database for testing.
func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	// Auto-migrate the schema
	err = db.AutoMigrate(&EventOutbox{})
	require.NoError(t, err)

	return db
}

func TestNewEventOutbox(t *testing.T) {
	entry := NewEventOutbox(
		"test.event.v1",
		"aggregate-123",
		"TestAggregate",
		[]byte(`{"key":"value"}`),
		"test-topic",
		"test-service",
		"correlation-456",
		"tenant-1",
	)

	assert.NotEqual(t, uuid.Nil, entry.ID)
	assert.Equal(t, "test.event.v1", entry.EventType)
	assert.Equal(t, "aggregate-123", entry.AggregateID)
	assert.Equal(t, "TestAggregate", entry.AggregateType)
	assert.Equal(t, []byte(`{"key":"value"}`), entry.EventPayload)
	assert.Equal(t, "test-topic", entry.Topic)
	assert.Equal(t, "test-service", entry.ServiceName)
	assert.Equal(t, "correlation-456", entry.CorrelationID)
	assert.Equal(t, "tenant-1", entry.TenantID)
	assert.Equal(t, StatusPending, entry.Status)
	assert.Equal(t, "aggregate-123", entry.PartitionKey) // Default to aggregate ID
	assert.False(t, entry.CreatedAt.IsZero())
}

func TestPostgresOutboxRepository_Insert(t *testing.T) {
	db := setupTestDB(t)
	repo := NewPostgresOutboxRepository(db)
	ctx := context.Background()

	t.Run("successful insert within transaction", func(t *testing.T) {
		entry := NewEventOutbox(
			"test.event.v1",
			"agg-1",
			"TestAggregate",
			[]byte(`{"data":"test"}`),
			"test-topic",
			"test-service",
			"corr-1",
			"tenant-1",
		)

		err := db.Transaction(func(tx *gorm.DB) error {
			return repo.Insert(ctx, tx, entry)
		})

		require.NoError(t, err)

		// Verify entry was persisted
		var fetched EventOutbox
		err = db.First(&fetched, "id = ?", entry.ID).Error
		require.NoError(t, err)
		assert.Equal(t, entry.EventType, fetched.EventType)
		assert.Equal(t, entry.AggregateID, fetched.AggregateID)
		assert.Equal(t, StatusPending, fetched.Status)
	})

	t.Run("rollback on transaction failure", func(t *testing.T) {
		entry := NewEventOutbox(
			"test.event.v1",
			"agg-rollback",
			"TestAggregate",
			[]byte(`{"data":"test"}`),
			"test-topic",
			"test-service",
			"corr-rollback",
			"tenant-1",
		)

		err := db.Transaction(func(tx *gorm.DB) error {
			if err := repo.Insert(ctx, tx, entry); err != nil {
				return err
			}
			// Force rollback
			return errIntentionalRollback
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "intentional rollback")

		// Verify entry was NOT persisted
		var count int64
		db.Model(&EventOutbox{}).Where("id = ?", entry.ID).Count(&count)
		assert.Equal(t, int64(0), count)
	})

	t.Run("nil transaction returns error", func(t *testing.T) {
		entry := NewEventOutbox(
			"test.event.v1",
			"agg-nil",
			"TestAggregate",
			[]byte(`{}`),
			"test-topic",
			"test-service",
			"",
			"tenant-1",
		)

		err := repo.Insert(ctx, nil, entry)
		assert.ErrorIs(t, err, ErrNilTransaction)
	})
}

func TestPostgresOutboxRepository_FetchUnprocessed(t *testing.T) {
	db := setupTestDB(t)
	repo := NewPostgresOutboxRepository(db)
	ctx := context.Background()

	// Create test entries with different services and statuses
	entries := []EventOutbox{
		*NewEventOutbox("event.type.1", "agg-1", "Type", []byte(`{}`), "topic", "service-a", "", ""),
		*NewEventOutbox("event.type.2", "agg-2", "Type", []byte(`{}`), "topic", "service-a", "", ""),
		*NewEventOutbox("event.type.3", "agg-3", "Type", []byte(`{}`), "topic", "service-b", "", ""),
	}

	// Insert entries
	for i := range entries {
		err := db.Create(&entries[i]).Error
		require.NoError(t, err)
		//nolint:forbidigo // Intentional: ensures distinct created_at timestamps for ordering tests
		time.Sleep(10 * time.Millisecond)
	}

	// Mark one as processing
	db.Model(&EventOutbox{}).Where("id = ?", entries[1].ID).Update("status", StatusProcessing)

	t.Run("fetches only pending entries for service", func(t *testing.T) {
		fetched, err := repo.FetchUnprocessed(ctx, "service-a", 10)
		require.NoError(t, err)
		assert.Len(t, fetched, 1) // Only one pending for service-a
		assert.Equal(t, entries[0].ID, fetched[0].ID)
	})

	t.Run("respects limit", func(t *testing.T) {
		// Add more entries
		for i := 0; i < 5; i++ {
			entry := NewEventOutbox("event.type.batch", "agg-batch", "Type", []byte(`{}`), "topic", "service-c", "", "")
			db.Create(entry)
		}

		fetched, err := repo.FetchUnprocessed(ctx, "service-c", 3)
		require.NoError(t, err)
		assert.Len(t, fetched, 3)
	})

	t.Run("orders by created_at", func(t *testing.T) {
		fetched, err := repo.FetchUnprocessed(ctx, "service-c", 10)
		require.NoError(t, err)

		for i := 1; i < len(fetched); i++ {
			assert.True(t, fetched[i-1].CreatedAt.Before(fetched[i].CreatedAt) ||
				fetched[i-1].CreatedAt.Equal(fetched[i].CreatedAt))
		}
	})
}

func TestPostgresOutboxRepository_MarkProcessing(t *testing.T) {
	db := setupTestDB(t)
	repo := NewPostgresOutboxRepository(db)
	ctx := context.Background()

	// Create test entries
	entry1 := NewEventOutbox("event.1", "agg-1", "Type", []byte(`{}`), "topic", "service", "", "")
	entry2 := NewEventOutbox("event.2", "agg-2", "Type", []byte(`{}`), "topic", "service", "", "")
	entry3 := NewEventOutbox("event.3", "agg-3", "Type", []byte(`{}`), "topic", "service", "", "")

	db.Create(entry1)
	db.Create(entry2)
	db.Create(entry3)

	// Mark entry3 as already processing
	db.Model(&EventOutbox{}).Where("id = ?", entry3.ID).Update("status", StatusProcessing)

	t.Run("marks pending entries as processing", func(t *testing.T) {
		ids := []uuid.UUID{entry1.ID, entry2.ID, entry3.ID}
		count, err := repo.MarkProcessing(ctx, ids)

		require.NoError(t, err)
		assert.Equal(t, int64(2), count) // Only 2 were pending

		var e1, e2, e3 EventOutbox
		db.First(&e1, "id = ?", entry1.ID)
		db.First(&e2, "id = ?", entry2.ID)
		db.First(&e3, "id = ?", entry3.ID)

		assert.Equal(t, StatusProcessing, e1.Status)
		assert.Equal(t, StatusProcessing, e2.Status)
		assert.Equal(t, StatusProcessing, e3.Status)
	})

	t.Run("empty ids returns zero", func(t *testing.T) {
		count, err := repo.MarkProcessing(ctx, []uuid.UUID{})
		require.NoError(t, err)
		assert.Equal(t, int64(0), count)
	})
}

func TestPostgresOutboxRepository_MarkCompleted(t *testing.T) {
	db := setupTestDB(t)
	repo := NewPostgresOutboxRepository(db)
	ctx := context.Background()

	entry := NewEventOutbox("event.1", "agg-1", "Type", []byte(`{}`), "topic", "service", "", "")
	db.Create(entry)

	t.Run("marks entry as completed with timestamp", func(t *testing.T) {
		before := time.Now()
		err := repo.MarkCompleted(ctx, entry.ID)
		after := time.Now()

		require.NoError(t, err)

		var fetched EventOutbox
		db.First(&fetched, "id = ?", entry.ID)

		assert.Equal(t, StatusCompleted, fetched.Status)
		assert.NotNil(t, fetched.ProcessedAt)
		assert.True(t, fetched.ProcessedAt.After(before) || fetched.ProcessedAt.Equal(before))
		assert.True(t, fetched.ProcessedAt.Before(after) || fetched.ProcessedAt.Equal(after))
	})

	t.Run("returns error for non-existent entry", func(t *testing.T) {
		err := repo.MarkCompleted(ctx, uuid.New())
		assert.ErrorIs(t, err, ErrOutboxEntryNotFound)
	})
}

func TestPostgresOutboxRepository_MarkFailed(t *testing.T) {
	db := setupTestDB(t)
	repo := NewPostgresOutboxRepository(db)
	ctx := context.Background()

	t.Run("increments retry count and resets to pending", func(t *testing.T) {
		entry := NewEventOutbox("event.1", "agg-1", "Type", []byte(`{}`), "topic", "service", "", "")
		db.Create(entry)

		err := repo.MarkFailed(ctx, entry.ID, errTestError, 5)

		require.NoError(t, err)

		var fetched EventOutbox
		db.First(&fetched, "id = ?", entry.ID)

		assert.Equal(t, StatusPending, fetched.Status) // Reset for retry
		assert.Equal(t, 1, fetched.RetryCount)
		assert.NotNil(t, fetched.LastError)
		assert.Equal(t, "test error", *fetched.LastError)
	})

	t.Run("marks as failed when retries exhausted", func(t *testing.T) {
		entry := NewEventOutbox("event.2", "agg-2", "Type", []byte(`{}`), "topic", "service", "", "")
		entry.RetryCount = 4 // Already tried 4 times
		db.Create(entry)

		err := repo.MarkFailed(ctx, entry.ID, errFinalError, 5) // Max 5 retries

		require.NoError(t, err)

		var fetched EventOutbox
		db.First(&fetched, "id = ?", entry.ID)

		assert.Equal(t, StatusFailed, fetched.Status) // Now failed
		assert.Equal(t, 5, fetched.RetryCount)
	})

	t.Run("returns error for non-existent entry", func(t *testing.T) {
		err := repo.MarkFailed(ctx, uuid.New(), errGeneric, 5)
		assert.ErrorIs(t, err, ErrOutboxEntryNotFound)
	})
}

func TestPostgresOutboxRepository_GetPendingCount(t *testing.T) {
	db := setupTestDB(t)
	repo := NewPostgresOutboxRepository(db)
	ctx := context.Background()

	// Create entries with different services and statuses
	for i := 0; i < 5; i++ {
		entry := NewEventOutbox("event", "agg", "Type", []byte(`{}`), "topic", "service-count", "", "")
		db.Create(entry)
	}

	// Mark some as different statuses
	var entries []EventOutbox
	db.Where("service_name = ?", "service-count").Find(&entries)

	db.Model(&EventOutbox{}).Where("id = ?", entries[0].ID).Update("status", StatusProcessing)
	db.Model(&EventOutbox{}).Where("id = ?", entries[1].ID).Update("status", StatusCompleted)

	count, err := repo.GetPendingCount(ctx, "service-count")
	require.NoError(t, err)
	assert.Equal(t, int64(3), count) // 5 - 2 non-pending = 3
}

func TestPostgresOutboxRepository_ResetStuckEntries(t *testing.T) {
	db := setupTestDB(t)
	repo := NewPostgresOutboxRepository(db)
	ctx := context.Background()

	// Create entries
	oldEntry := NewEventOutbox("event.old", "agg-old", "Type", []byte(`{}`), "topic", "service-stuck", "", "")
	oldEntry.Status = StatusProcessing
	oldEntry.CreatedAt = time.Now().Add(-10 * time.Minute) // 10 minutes ago
	db.Create(oldEntry)

	newEntry := NewEventOutbox("event.new", "agg-new", "Type", []byte(`{}`), "topic", "service-stuck", "", "")
	newEntry.Status = StatusProcessing
	newEntry.CreatedAt = time.Now() // Just now
	db.Create(newEntry)

	// Reset entries older than 5 minutes
	count, err := repo.ResetStuckEntries(ctx, "service-stuck", 5*time.Minute)

	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	var oldFetched, newFetched EventOutbox
	db.First(&oldFetched, "id = ?", oldEntry.ID)
	db.First(&newFetched, "id = ?", newEntry.ID)

	assert.Equal(t, StatusPending, oldFetched.Status)    // Reset
	assert.Equal(t, StatusProcessing, newFetched.Status) // Not reset
}
