package events

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
	"gorm.io/gorm"
)

// Test errors for worker tests.
var (
	errDeliveryFailed   = errors.New("delivery failed")
	errPermanentFailure = errors.New("permanent failure")
	errProduceFailed    = errors.New("produce failed")
)

// mockKafkaPublisher is a mock implementation of KafkaPublisher for testing.
type mockKafkaPublisher struct {
	mu           sync.Mutex
	records      []*kgo.Record
	produceError error
	flushError   error
	closed       bool
}

func newMockKafkaPublisher() *mockKafkaPublisher {
	return &mockKafkaPublisher{
		records: make([]*kgo.Record, 0),
	}
}

func (m *mockKafkaPublisher) ProduceRecord(_ context.Context, record *kgo.Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.produceError != nil {
		return m.produceError
	}

	m.records = append(m.records, record)
	return nil
}

func (m *mockKafkaPublisher) Flush(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.flushError
}

func (m *mockKafkaPublisher) FlushWithTimeout(_ int) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.flushError != nil {
		return 1
	}
	return 0
}

func (m *mockKafkaPublisher) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
}

func (m *mockKafkaPublisher) getRecords() []*kgo.Record {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]*kgo.Record, len(m.records))
	copy(result, m.records)
	return result
}

func (m *mockKafkaPublisher) setProduceError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.produceError = err
}

// mockOutboxRepository is an in-memory implementation for testing.
type mockOutboxRepository struct {
	mu       sync.Mutex
	entries  map[uuid.UUID]*EventOutbox
	fetchErr error
}

func newMockOutboxRepository() *mockOutboxRepository {
	return &mockOutboxRepository{
		entries: make(map[uuid.UUID]*EventOutbox),
	}
}

func (r *mockOutboxRepository) Insert(_ context.Context, _ *gorm.DB, entry *EventOutbox) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if entry.ID == uuid.Nil {
		entry.ID = uuid.New()
	}
	if entry.Status == "" {
		entry.Status = StatusPending
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now()
	}

	r.entries[entry.ID] = entry
	return nil
}

func (r *mockOutboxRepository) FetchUnprocessed(_ context.Context, serviceName string, limit int) ([]EventOutbox, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.fetchErr != nil {
		return nil, r.fetchErr
	}

	var result []EventOutbox
	for _, entry := range r.entries {
		if entry.Status == StatusPending && entry.ServiceName == serviceName {
			result = append(result, *entry)
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (r *mockOutboxRepository) FetchAndLockForProcessing(ctx context.Context, serviceName string, limit int) ([]EventOutbox, error) {
	// For tests, this is equivalent to FetchUnprocessed + MarkProcessing
	entries, err := r.FetchUnprocessed(ctx, serviceName, limit)
	if err != nil {
		return nil, err
	}

	// Mark them as processing
	ids := make([]uuid.UUID, len(entries))
	for i, entry := range entries {
		ids[i] = entry.ID
	}
	_, _ = r.MarkProcessing(ctx, ids)

	return entries, nil
}

func (r *mockOutboxRepository) MarkProcessing(_ context.Context, ids []uuid.UUID) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var count int64
	for _, id := range ids {
		if entry, ok := r.entries[id]; ok && entry.Status == StatusPending {
			entry.Status = StatusProcessing
			count++
		}
	}
	return count, nil
}

func (r *mockOutboxRepository) MarkCompleted(_ context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.entries[id]
	if !ok {
		return ErrOutboxEntryNotFound
	}

	now := time.Now()
	entry.Status = StatusCompleted
	entry.ProcessedAt = &now
	return nil
}

func (r *mockOutboxRepository) MarkFailed(_ context.Context, id uuid.UUID, err error, maxRetries int) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.entries[id]
	if !ok {
		return ErrOutboxEntryNotFound
	}

	entry.RetryCount++
	errMsg := err.Error()
	entry.LastError = &errMsg

	if entry.RetryCount >= maxRetries {
		entry.Status = StatusFailed
	} else {
		entry.Status = StatusPending
	}
	return nil
}

func (r *mockOutboxRepository) GetPendingCount(_ context.Context, serviceName string) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var count int64
	for _, entry := range r.entries {
		if entry.Status == StatusPending && entry.ServiceName == serviceName {
			count++
		}
	}
	return count, nil
}

func (r *mockOutboxRepository) ResetStuckEntries(_ context.Context, serviceName string, olderThan time.Duration) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	threshold := time.Now().Add(-olderThan)
	var count int64
	for _, entry := range r.entries {
		if entry.Status == StatusProcessing && entry.ServiceName == serviceName && entry.CreatedAt.Before(threshold) {
			entry.Status = StatusPending
			count++
		}
	}
	return count, nil
}

func (r *mockOutboxRepository) addEntry(entry *EventOutbox) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[entry.ID] = entry
}

func (r *mockOutboxRepository) getEntry(id uuid.UUID) *EventOutbox {
	r.mu.Lock()
	defer r.mu.Unlock()
	if entry, ok := r.entries[id]; ok {
		// Return a copy to avoid data races when test reads while worker writes
		entryCopy := *entry
		return &entryCopy
	}
	return nil
}

func TestWorker_ProcessBatch_Success(t *testing.T) {
	repo := newMockOutboxRepository()
	publisher := newMockKafkaPublisher()

	config := DefaultWorkerConfig("test-service")
	config.PollInterval = 50 * time.Millisecond
	config.PublishTimeoutMs = 1000

	worker := NewWorker(repo, publisher, config, nil)

	// Add test entries
	entry1 := NewEventOutbox("event.type.1", "agg-1", "Aggregate", []byte(`{"test":1}`), "test-topic", "test-service", "corr-1", "tenant-1")
	entry2 := NewEventOutbox("event.type.2", "agg-2", "Aggregate", []byte(`{"test":2}`), "test-topic", "test-service", "corr-2", "tenant-1")
	repo.addEntry(entry1)
	repo.addEntry(entry2)

	ctx, cancel := context.WithCancel(context.Background())
	worker.Start(ctx)

	// Wait for entries to be processed
	err := await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			e1 := repo.getEntry(entry1.ID)
			e2 := repo.getEntry(entry2.ID)
			return e1 != nil && e1.Status == StatusCompleted &&
				e2 != nil && e2.Status == StatusCompleted
		})

	require.NoError(t, err, "entries should be processed")

	cancel()
	worker.Stop()

	// Verify records were published
	records := publisher.getRecords()
	assert.Len(t, records, 2)
}

func TestWorker_ProcessBatch_RetryOnFailure(t *testing.T) {
	repo := newMockOutboxRepository()
	publisher := newMockKafkaPublisher()

	config := DefaultWorkerConfig("test-service")
	config.PollInterval = 50 * time.Millisecond
	config.MaxRetries = 5 // More retries so we have time to clear the error
	config.PublishTimeoutMs = 500

	worker := NewWorker(repo, publisher, config, nil)

	// Add test entry
	entry := NewEventOutbox("event.type.1", "agg-1", "Aggregate", []byte(`{}`), "test-topic", "test-service", "", "tenant-1")
	repo.addEntry(entry)

	// Set produce to fail initially
	publisher.setProduceError(errDeliveryFailed)

	ctx, cancel := context.WithCancel(context.Background())
	worker.Start(ctx)

	// Wait for at least one retry
	err := await.New().
		AtMost(2 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			e := repo.getEntry(entry.ID)
			return e != nil && e.RetryCount >= 1
		})

	require.NoError(t, err, "first retry should occur")

	// Clear the error - next attempt should succeed
	publisher.setProduceError(nil)

	// Wait for success
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			e := repo.getEntry(entry.ID)
			return e != nil && e.Status == StatusCompleted
		})

	require.NoError(t, err, "entry should eventually succeed")

	cancel()
	worker.Stop()
}

func TestWorker_ProcessBatch_DLQAfterMaxRetries(t *testing.T) {
	repo := newMockOutboxRepository()
	publisher := newMockKafkaPublisher()

	config := DefaultWorkerConfig("test-service")
	config.PollInterval = 50 * time.Millisecond
	config.MaxRetries = 2
	config.PublishTimeoutMs = 500

	worker := NewWorker(repo, publisher, config, nil)

	// Add test entry
	entry := NewEventOutbox("event.type.1", "agg-1", "Aggregate", []byte(`{}`), "test-topic", "test-service", "", "tenant-1")
	repo.addEntry(entry)

	// Make produce always fail
	publisher.setProduceError(errPermanentFailure)

	ctx, cancel := context.WithCancel(context.Background())
	worker.Start(ctx)

	// Wait for entry to be moved to failed status
	err := await.New().
		AtMost(5 * time.Second).
		PollInterval(100 * time.Millisecond).
		Until(func() bool {
			e := repo.getEntry(entry.ID)
			return e != nil && e.Status == StatusFailed
		})

	require.NoError(t, err, "entry should be marked as failed after max retries")

	cancel()
	worker.Stop()

	finalEntry := repo.getEntry(entry.ID)
	assert.Equal(t, StatusFailed, finalEntry.Status)
	assert.GreaterOrEqual(t, finalEntry.RetryCount, config.MaxRetries)
}

func TestWorker_GracefulShutdown(t *testing.T) {
	repo := newMockOutboxRepository()
	publisher := newMockKafkaPublisher()

	config := DefaultWorkerConfig("test-service")
	config.PollInterval = 100 * time.Millisecond

	worker := NewWorker(repo, publisher, config, nil)

	ctx := context.Background()
	worker.Start(ctx)

	// Intentional sleep: Give worker time to start its run loop.
	// The worker doesn't expose a "started" state we can poll.
	time.Sleep(50 * time.Millisecond)

	// Stop should complete without hanging
	done := make(chan struct{})
	go func() {
		worker.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(2 * time.Second):
		t.Fatal("worker.Stop() timed out")
	}
}

func TestWorker_ContextCancellation(t *testing.T) {
	repo := newMockOutboxRepository()
	publisher := newMockKafkaPublisher()

	config := DefaultWorkerConfig("test-service")
	config.PollInterval = 100 * time.Millisecond

	worker := NewWorker(repo, publisher, config, nil)

	ctx, cancel := context.WithCancel(context.Background())
	worker.Start(ctx)

	// Intentional sleep: Give worker time to start its run loop.
	// The worker doesn't expose a "started" state we can poll.
	time.Sleep(50 * time.Millisecond)

	// Cancel context
	cancel()

	// Worker should stop on its own
	done := make(chan struct{})
	go func() {
		worker.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop after context cancellation")
	}
}

func TestWorker_PublishWithHeaders(t *testing.T) {
	repo := newMockOutboxRepository()
	publisher := newMockKafkaPublisher()

	config := DefaultWorkerConfig("test-service")
	config.PollInterval = 50 * time.Millisecond
	config.PublishTimeoutMs = 1000

	worker := NewWorker(repo, publisher, config, nil)

	// Add test entry with correlation and causation IDs
	entry := NewEventOutbox(
		"event.type.1",
		"agg-1",
		"TestAggregate",
		[]byte(`{"data":"test"}`),
		"test-topic",
		"test-service",
		"correlation-123",
		"tenant-1",
	)
	entry.CausationID = "causation-456"
	repo.addEntry(entry)

	ctx, cancel := context.WithCancel(context.Background())
	worker.Start(ctx)

	// Wait for entry to be processed
	err := await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			e := repo.getEntry(entry.ID)
			return e != nil && e.Status == StatusCompleted
		})

	require.NoError(t, err)

	cancel()
	worker.Stop()

	// Verify headers were set correctly
	records := publisher.getRecords()
	require.Len(t, records, 1)

	record := records[0]
	headerMap := make(map[string]string)
	for _, h := range record.Headers {
		headerMap[h.Key] = string(h.Value)
	}

	assert.NotEmpty(t, headerMap["event_id"], "event_id header should be set")
	assert.Equal(t, "event.type.1", headerMap["event_type"])
	assert.Equal(t, "TestAggregate", headerMap["aggregate_type"])
	assert.Equal(t, "agg-1", headerMap["aggregate_id"])
	assert.Equal(t, "correlation-123", headerMap["correlation_id"])
	assert.Equal(t, "causation-456", headerMap["causation_id"])
	assert.Equal(t, "tenant-1", headerMap["X-Tenant-ID"])
}

func TestWorker_ProduceError(t *testing.T) {
	repo := newMockOutboxRepository()
	publisher := newMockKafkaPublisher()

	// Make produce fail
	publisher.setProduceError(errProduceFailed)

	config := DefaultWorkerConfig("test-service")
	config.PollInterval = 50 * time.Millisecond
	config.MaxRetries = 2

	worker := NewWorker(repo, publisher, config, nil)

	entry := NewEventOutbox("event.type.1", "agg-1", "Aggregate", []byte(`{}`), "test-topic", "test-service", "", "tenant-1")
	repo.addEntry(entry)

	ctx, cancel := context.WithCancel(context.Background())
	worker.Start(ctx)

	// Wait for retries
	err := await.New().
		AtMost(5 * time.Second).
		PollInterval(100 * time.Millisecond).
		Until(func() bool {
			e := repo.getEntry(entry.ID)
			return e != nil && e.Status == StatusFailed
		})

	require.NoError(t, err)

	cancel()
	worker.Stop()
}
