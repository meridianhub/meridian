package adapters_test

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/api-gateway/eventstream"
	"github.com/meridianhub/meridian/services/api-gateway/eventstream/adapters"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func newTestLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// setupTestDB starts a CockroachDB testcontainer with the event_outbox schema.
func setupTestDB(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	return testdb.SetupCockroachDB(t, []interface{}{&events.EventOutbox{}})
}

// insertOutboxEntry inserts an EventOutbox row directly for test setup.
func insertOutboxEntry(t *testing.T, db *gorm.DB, entry events.EventOutbox) {
	t.Helper()
	if entry.ID == uuid.Nil {
		entry.ID = uuid.New()
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	require.NoError(t, db.Create(&entry).Error)
}

func newCompletedEntry(serviceName, eventType, topic string) events.EventOutbox {
	return events.EventOutbox{
		ID:            uuid.New(),
		EventType:     eventType,
		AggregateID:   uuid.New().String(),
		AggregateType: "TestAggregate",
		EventPayload:  []byte(`{"test":"payload"}`),
		Topic:         topic,
		ServiceName:   serviceName,
		Status:        events.StatusCompleted,
		CreatedAt:     time.Now().UTC(),
	}
}

// --- outboxToDomainEvent conversion ---

func TestOutboxToDomainEvent_FieldMapping(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	payload := []byte(`{"amount":100}`)
	entry := events.EventOutbox{
		ID:            uuid.New(),
		EventType:     "payment_order.reserved.v1",
		AggregateID:   "agg-123",
		AggregateType: "PaymentOrder",
		EventPayload:  payload,
		Topic:         "payment-order.reserved.v1",
		ServiceName:   "payment-order",
		CorrelationID: "corr-456",
		CausationID:   "cause-789",
		Status:        events.StatusCompleted,
		CreatedAt:     time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC),
	}
	insertOutboxEntry(t, db, entry)

	source := adapters.NewOutboxEventSource(db, 50*time.Millisecond, newTestLogger(t))

	var received []eventstream.DomainEvent
	var mu sync.Mutex

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	go func() {
		_ = source.Start(ctx, func(_ context.Context, ev eventstream.DomainEvent) error {
			mu.Lock()
			received = append(received, ev)
			mu.Unlock()
			cancel() // stop after first event
			return nil
		})
	}()

	<-ctx.Done()
	mu.Lock()
	defer mu.Unlock()

	require.Len(t, received, 1)
	got := received[0]

	assert.Equal(t, entry.ID.String(), got.EventID)
	assert.Equal(t, "payment_order.reserved.v1", got.EventType)
	assert.Equal(t, "payment-order.reserved.v1", got.Topic)
	assert.Equal(t, "payment-order.reserved", got.Channel) // .v1 stripped
	assert.Equal(t, "agg-123", got.AggregateID)
	assert.Equal(t, "PaymentOrder", got.AggregateType)
	assert.Equal(t, "corr-456", got.CorrelationID)
	assert.Equal(t, "cause-789", got.CausationID)
	assert.Equal(t, payload, got.Payload)
	assert.Equal(t, entry.CreatedAt.UTC(), got.Timestamp)
	assert.Empty(t, got.TenantID, "TenantID is not stored in EventOutbox")
}

func TestOutboxToDomainEvent_ChannelDerivation(t *testing.T) {
	tests := []struct {
		topic           string
		expectedChannel string
	}{
		{"payment-order.reserved.v1", "payment-order.reserved"},
		{"position-keeping.transaction-captured.v1", "position-keeping.transaction-captured"},
		{"audit.events.current-account.v1", "audit.events.current-account"},
		{"no-version-suffix", "no-version-suffix"},
	}

	for _, tc := range tests {
		t.Run(tc.topic, func(t *testing.T) {
			entry := newCompletedEntry("svc", "evt.v1", tc.topic)
			db2, cleanup2 := setupTestDB(t)
			defer cleanup2()
			insertOutboxEntry(t, db2, entry)

			source := adapters.NewOutboxEventSource(db2, 50*time.Millisecond, newTestLogger(t))

			var got eventstream.DomainEvent
			var once sync.Once
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			go func() {
				_ = source.Start(ctx, func(_ context.Context, ev eventstream.DomainEvent) error {
					once.Do(func() { got = ev; cancel() })
					return nil
				})
			}()
			<-ctx.Done()
			assert.Equal(t, tc.expectedChannel, got.Channel)
		})
	}
}

// --- High-water mark deduplication ---

func TestOutboxSource_HighWaterMark_PreventsDuplicateDelivery(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	entry := newCompletedEntry("position-keeping", "pk.transaction-captured.v1", "position-keeping.transaction-captured.v1")
	insertOutboxEntry(t, db, entry)

	source := adapters.NewOutboxEventSource(db, 50*time.Millisecond, newTestLogger(t))

	var mu sync.Mutex
	var count int

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_ = source.Start(ctx, func(_ context.Context, _ eventstream.DomainEvent) error {
		mu.Lock()
		count++
		mu.Unlock()
		return nil
	})

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, count, "each outbox entry should be delivered exactly once")
}

func TestOutboxSource_HighWaterMark_PerServiceTracking(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Two entries from different services inserted at slightly different times.
	base := time.Now().UTC().Add(-time.Second) // ensure they're in the past
	entry1 := newCompletedEntry("service-a", "svc-a.event.v1", "service-a.event.v1")
	entry1.CreatedAt = base
	entry2 := newCompletedEntry("service-b", "svc-b.event.v1", "service-b.event.v1")
	entry2.CreatedAt = base.Add(10 * time.Millisecond)

	insertOutboxEntry(t, db, entry1)
	insertOutboxEntry(t, db, entry2)

	source := adapters.NewOutboxEventSource(db, 50*time.Millisecond, newTestLogger(t))

	var mu sync.Mutex
	received := make(map[string]int)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_ = source.Start(ctx, func(_ context.Context, ev eventstream.DomainEvent) error {
		mu.Lock()
		received[ev.EventType]++
		mu.Unlock()
		return nil
	})

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, received["svc-a.event.v1"], "service-a event delivered once")
	assert.Equal(t, 1, received["svc-b.event.v1"], "service-b event delivered once")
}

func TestOutboxSource_HighWaterMark_SameTimestampNoDuplicates(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Insert 3 entries with the same created_at timestamp, simulating a bulk insert.
	sameTime := time.Now().UTC().Add(-time.Second).Truncate(time.Millisecond)
	for i := 0; i < 3; i++ {
		e := newCompletedEntry("svc", "same-ts.event.v1", "svc.same-ts.v1")
		e.CreatedAt = sameTime
		insertOutboxEntry(t, db, e)
	}

	source := adapters.NewOutboxEventSource(db, 50*time.Millisecond, newTestLogger(t))

	var mu sync.Mutex
	var count int

	// Run for enough time to allow multiple polls.
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()

	_ = source.Start(ctx, func(_ context.Context, _ eventstream.DomainEvent) error {
		mu.Lock()
		count++
		mu.Unlock()
		return nil
	})

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 3, count, "all 3 same-timestamp entries should be delivered exactly once")
}

// --- Batch size limiting ---

func TestOutboxSource_BatchSize_LimitsEntriesPerPoll(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Insert 10 entries.
	base := time.Now().UTC().Add(-10 * time.Second)
	for i := 0; i < 10; i++ {
		entry := newCompletedEntry("svc", "evt.v1", "svc.event.v1")
		entry.CreatedAt = base.Add(time.Duration(i) * time.Millisecond)
		insertOutboxEntry(t, db, entry)
	}

	var mu sync.Mutex
	var count int

	// Run a single poll by starting with a very short-lived context that expires
	// after the first tick fires.
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	// Use a shorter poll interval so the first tick fires within the timeout.
	sourceShort := adapters.NewOutboxEventSource(db, 50*time.Millisecond, newTestLogger(t)).
		WithBatchSize(3)

	_ = sourceShort.Start(ctx, func(_ context.Context, _ eventstream.DomainEvent) error {
		mu.Lock()
		count++
		mu.Unlock()
		return nil
	})

	mu.Lock()
	defer mu.Unlock()
	// After one poll cycle with batch 3, we expect exactly 3.
	// The context expires after 150ms; with 50ms poll interval there may be up to 2 polls.
	// We assert at most 6 (2 polls × 3) and at least 3.
	assert.GreaterOrEqual(t, count, 3, "at least one batch of 3 should be delivered")
	assert.LessOrEqual(t, count, 6, "at most 2 batch polls in 150ms window")
}

// --- Polling only completed entries ---

func TestOutboxSource_OnlyDeliversCompletedEntries(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	completed := newCompletedEntry("svc", "completed.event.v1", "svc.completed.v1")
	pending := newCompletedEntry("svc", "pending.event.v1", "svc.pending.v1")
	pending.Status = events.StatusPending
	processing := newCompletedEntry("svc", "processing.event.v1", "svc.processing.v1")
	processing.Status = events.StatusProcessing
	failed := newCompletedEntry("svc", "failed.event.v1", "svc.failed.v1")
	failed.Status = events.StatusFailed

	insertOutboxEntry(t, db, completed)
	insertOutboxEntry(t, db, pending)
	insertOutboxEntry(t, db, processing)
	insertOutboxEntry(t, db, failed)

	source := adapters.NewOutboxEventSource(db, 50*time.Millisecond, newTestLogger(t))

	var mu sync.Mutex
	var received []string

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	_ = source.Start(ctx, func(_ context.Context, ev eventstream.DomainEvent) error {
		mu.Lock()
		received = append(received, ev.EventType)
		mu.Unlock()
		return nil
	})

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"completed.event.v1"}, received,
		"only completed outbox entries should be delivered")
}

// --- Start returns nil on context cancellation ---

func TestOutboxSource_Start_ReturnsNilOnContextCancel(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	source := adapters.NewOutboxEventSource(db, 50*time.Millisecond, newTestLogger(t))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := source.Start(ctx, func(_ context.Context, _ eventstream.DomainEvent) error {
		return nil
	})
	assert.NoError(t, err)
}

// --- New entries are picked up after high-water mark advance ---

func TestOutboxSource_PicksUpNewEntriesAfterFirstPoll(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Insert first entry.
	entry1 := newCompletedEntry("svc", "first.event.v1", "svc.first.v1")
	entry1.CreatedAt = time.Now().UTC().Add(-100 * time.Millisecond)
	insertOutboxEntry(t, db, entry1)

	source := adapters.NewOutboxEventSource(db, 50*time.Millisecond, newTestLogger(t))

	var mu sync.Mutex
	var received []string
	countReceived := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(received)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = source.Start(ctx, func(_ context.Context, ev eventstream.DomainEvent) error {
			mu.Lock()
			received = append(received, ev.EventType)
			mu.Unlock()
			return nil
		})
	}()

	// Wait until the first entry is delivered, then insert a second entry.
	err := await.New().AtMost(5 * time.Second).PollInterval(20 * time.Millisecond).
		Until(func() bool { return countReceived() >= 1 })
	require.NoError(t, err, "first entry should have been delivered")

	entry2 := newCompletedEntry("svc", "second.event.v1", "svc.second.v1")
	entry2.CreatedAt = time.Now().UTC()
	insertOutboxEntry(t, db, entry2)

	// Wait until the second entry is also delivered.
	err = await.New().AtMost(5 * time.Second).PollInterval(20 * time.Millisecond).
		Until(func() bool { return countReceived() >= 2 })
	require.NoError(t, err, "second entry should have been delivered")
	cancel()

	mu.Lock()
	defer mu.Unlock()
	assert.Contains(t, received, "first.event.v1")
	assert.Contains(t, received, "second.event.v1")
	assert.Equal(t, 2, len(received), "should receive exactly 2 distinct events")
}
