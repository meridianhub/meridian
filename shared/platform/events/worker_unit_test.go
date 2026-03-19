package events

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
)

func TestNewWorker_AppliesDefaults(t *testing.T) {
	repo := newMockOutboxRepository()
	publisher := newMockKafkaPublisher()

	// All zero/negative values should get defaults applied.
	config := WorkerConfig{
		ServiceName: "test-service",
	}

	worker := NewWorker(repo, publisher, config, nil)

	assert.Equal(t, defaultBatchSize, worker.config.BatchSize)
	assert.Equal(t, defaultPollInterval, worker.config.PollInterval)
	assert.Equal(t, defaultMaxRetries, worker.config.MaxRetries)
	assert.Equal(t, defaultProcessingAge, worker.config.ProcessingAge)
	assert.Equal(t, defaultPublishTimeoutMs, worker.config.PublishTimeoutMs)
}

func TestNewWorker_PreservesCustomConfig(t *testing.T) {
	repo := newMockOutboxRepository()
	publisher := newMockKafkaPublisher()

	config := WorkerConfig{
		ServiceName:      "test-service",
		BatchSize:        50,
		PollInterval:     10 * time.Second,
		MaxRetries:       3,
		ProcessingAge:    2 * time.Minute,
		PublishTimeoutMs: 3000,
	}

	worker := NewWorker(repo, publisher, config, nil)

	assert.Equal(t, 50, worker.config.BatchSize)
	assert.Equal(t, 10*time.Second, worker.config.PollInterval)
	assert.Equal(t, 3, worker.config.MaxRetries)
	assert.Equal(t, 2*time.Minute, worker.config.ProcessingAge)
	assert.Equal(t, 3000, worker.config.PublishTimeoutMs)
}

func TestDefaultWorkerConfig(t *testing.T) {
	config := DefaultWorkerConfig("my-service")

	assert.Equal(t, "my-service", config.ServiceName)
	assert.Equal(t, defaultBatchSize, config.BatchSize)
	assert.Equal(t, defaultPollInterval, config.PollInterval)
	assert.Equal(t, defaultMaxRetries, config.MaxRetries)
	assert.Equal(t, defaultProcessingAge, config.ProcessingAge)
	assert.Equal(t, defaultPublishTimeoutMs, config.PublishTimeoutMs)
}

func TestWorker_ResetStuckEntries_RecordsMetric(t *testing.T) {
	repo := newMockOutboxRepository()
	publisher := newMockKafkaPublisher()

	config := DefaultWorkerConfig("test-service")
	config.ProcessingAge = 1 * time.Millisecond // Very short for testing

	worker := NewWorker(repo, publisher, config, nil)

	// Add an entry that's "stuck" in processing state with old created_at.
	entry := NewEventOutbox("event.type", "agg-1", "Type", []byte(`{}`), "topic", "test-service", "", "")
	entry.Status = StatusProcessing
	entry.CreatedAt = time.Now().Add(-10 * time.Minute)
	repo.addEntry(entry)

	// Call resetStuckEntries directly.
	err := worker.resetStuckEntries(context.Background())
	require.NoError(t, err)

	// Verify the entry was reset to pending.
	e := repo.getEntry(entry.ID)
	assert.Equal(t, StatusPending, e.Status)
}

func TestWorker_ResetStuckEntries_NoStuckEntries(t *testing.T) {
	repo := newMockOutboxRepository()
	publisher := newMockKafkaPublisher()

	config := DefaultWorkerConfig("test-service")
	worker := NewWorker(repo, publisher, config, nil)

	// No stuck entries - should succeed without error.
	err := worker.resetStuckEntries(context.Background())
	require.NoError(t, err)
}

func TestWorker_ResetStuckEntries_RepoError(t *testing.T) {
	repo := &errorOutboxRepository{
		resetErr: errors.New("db connection lost"),
	}
	publisher := newMockKafkaPublisher()

	config := DefaultWorkerConfig("test-service")
	worker := NewWorker(repo, publisher, config, nil)

	err := worker.resetStuckEntries(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to reset stuck entries")
}

func TestWorker_ProcessBatch_FetchError(t *testing.T) {
	repo := &errorOutboxRepository{
		fetchErr: errors.New("database unavailable"),
	}
	publisher := newMockKafkaPublisher()

	config := DefaultWorkerConfig("test-service")
	worker := NewWorker(repo, publisher, config, nil)

	err := worker.processBatch(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to fetch and lock entries")
}

func TestWorker_ProcessBatch_EmptyBatch(t *testing.T) {
	repo := newMockOutboxRepository()
	publisher := newMockKafkaPublisher()

	config := DefaultWorkerConfig("test-service")
	worker := NewWorker(repo, publisher, config, nil)

	// No entries - should return nil.
	err := worker.processBatch(context.Background())
	assert.NoError(t, err)
}

func TestWorker_ProcessEntry_MarkCompletedError(t *testing.T) {
	repo := &errorOutboxRepository{
		markCompletedErr: errors.New("mark completed failed"),
	}
	publisher := newMockKafkaPublisher()

	config := DefaultWorkerConfig("test-service")
	worker := NewWorker(repo, publisher, config, nil)

	entry := NewEventOutbox("event.type", "agg-1", "Type", []byte(`{}`), "topic", "test-service", "", "")

	err := worker.processEntry(context.Background(), entry)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to mark entry as completed")
}

func TestWorker_ProcessEntry_MarkFailedError(t *testing.T) {
	publisher := newMockKafkaPublisher()
	publisher.setProduceError(errors.New("kafka down"))

	repo := &errorOutboxRepository{
		markFailedErr: errors.New("db error on mark failed"),
	}

	config := DefaultWorkerConfig("test-service")
	worker := NewWorker(repo, publisher, config, nil)

	entry := NewEventOutbox("event.type", "agg-1", "Type", []byte(`{}`), "topic", "test-service", "", "")

	err := worker.processEntry(context.Background(), entry)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "publish failed")
}

func TestWorker_PublishToKafka_Timeout(t *testing.T) {
	publisher := &timeoutKafkaPublisher{}

	repo := newMockOutboxRepository()

	config := DefaultWorkerConfig("test-service")
	config.PublishTimeoutMs = 50 // Short timeout
	worker := NewWorker(repo, publisher, config, nil)

	entry := NewEventOutbox("event.type", "agg-1", "Type", []byte(`{}`), "topic", "test-service", "", "")

	err := worker.publishToKafka(context.Background(), entry)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrPublishTimeout)
}

func TestWorker_PublishToKafka_HeadersWithoutOptionalFields(t *testing.T) {
	publisher := newMockKafkaPublisher()
	repo := newMockOutboxRepository()

	config := DefaultWorkerConfig("test-service")
	worker := NewWorker(repo, publisher, config, nil)

	// Entry without correlation, causation, or tenant.
	entry := NewEventOutbox("event.type", "agg-1", "Type", []byte(`{}`), "topic", "test-service", "", "")

	err := worker.publishToKafka(context.Background(), entry)
	require.NoError(t, err)

	records := publisher.getRecords()
	require.Len(t, records, 1)

	// Verify only required headers are present (no correlation, causation, tenant).
	headerMap := make(map[string]string)
	for _, h := range records[0].Headers {
		headerMap[h.Key] = string(h.Value)
	}

	assert.NotEmpty(t, headerMap["event_id"])
	assert.Equal(t, "event.type", headerMap["event_type"])
	assert.Equal(t, "Type", headerMap["aggregate_type"])
	assert.Equal(t, "agg-1", headerMap["aggregate_id"])

	_, hasCorrelation := headerMap["correlation_id"]
	_, hasCausation := headerMap["causation_id"]
	_, hasTenant := headerMap["X-Tenant-ID"]
	assert.False(t, hasCorrelation, "should not have correlation_id header")
	assert.False(t, hasCausation, "should not have causation_id header")
	assert.False(t, hasTenant, "should not have X-Tenant-ID header")
}

func TestWorker_ProcessEntry_RetriesExhausted(t *testing.T) {
	publisher := newMockKafkaPublisher()
	publisher.setProduceError(errors.New("permanent failure"))

	repo := newMockOutboxRepository()

	config := DefaultWorkerConfig("test-service")
	config.MaxRetries = 3
	worker := NewWorker(repo, publisher, config, nil)

	// Entry already at max retries - 1.
	entry := NewEventOutbox("event.type", "agg-1", "Type", []byte(`{}`), "topic", "test-service", "", "")
	entry.RetryCount = 2 // Next failure will exhaust retries.
	repo.addEntry(entry)

	err := worker.processEntry(context.Background(), entry)
	assert.Error(t, err)

	// Verify DLQ path was hit (entry marked failed).
	e := repo.getEntry(entry.ID)
	assert.Equal(t, StatusFailed, e.Status)
}

func TestWorker_ProcessEntry_RetryNotExhausted(t *testing.T) {
	publisher := newMockKafkaPublisher()
	publisher.setProduceError(errors.New("temporary failure"))

	repo := newMockOutboxRepository()

	config := DefaultWorkerConfig("test-service")
	config.MaxRetries = 5
	worker := NewWorker(repo, publisher, config, nil)

	// Entry with room for retries.
	entry := NewEventOutbox("event.type", "agg-1", "Type", []byte(`{}`), "topic", "test-service", "", "")
	entry.RetryCount = 1
	repo.addEntry(entry)

	err := worker.processEntry(context.Background(), entry)
	assert.Error(t, err)

	// Entry should be back to pending for retry.
	e := repo.getEntry(entry.ID)
	assert.Equal(t, StatusPending, e.Status)
}

func TestWorker_Stop_MultipleCallsSafe(t *testing.T) {
	repo := newMockOutboxRepository()
	publisher := newMockKafkaPublisher()

	config := DefaultWorkerConfig("test-service")
	config.PollInterval = 100 * time.Millisecond

	worker := NewWorker(repo, publisher, config, nil)
	worker.Start(context.Background())

	// Multiple stops should not panic.
	done := make(chan struct{})
	go func() {
		worker.Stop()
		worker.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(3 * time.Second):
		t.Fatal("double Stop() timed out")
	}
}

func TestWorker_Stop_UnflushedMessages(t *testing.T) {
	repo := newMockOutboxRepository()
	publisher := newMockKafkaPublisher()
	publisher.flushError = errors.New("flush failed") // Causes FlushWithTimeout to return >0

	config := DefaultWorkerConfig("test-service")
	config.PollInterval = 100 * time.Millisecond

	worker := NewWorker(repo, publisher, config, nil)
	worker.Start(context.Background())

	// Stop should complete even with unflushed messages (just logs warning).
	done := make(chan struct{})
	go func() {
		worker.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Success - worker stopped despite flush issues
	case <-time.After(3 * time.Second):
		t.Fatal("Stop() with unflushed messages timed out")
	}
}

// errorOutboxRepository is a mock that returns configurable errors.
type errorOutboxRepository struct {
	fetchErr         error
	resetErr         error
	markCompletedErr error
	markFailedErr    error
}

func (r *errorOutboxRepository) FetchAndLockForProcessing(_ context.Context, _ string, _ int) ([]EventOutbox, error) {
	if r.fetchErr != nil {
		return nil, r.fetchErr
	}
	return nil, nil
}

func (r *errorOutboxRepository) MarkCompleted(_ context.Context, _ uuid.UUID) error {
	return r.markCompletedErr
}

func (r *errorOutboxRepository) MarkFailed(_ context.Context, _ uuid.UUID, _ error, _ int) error {
	return r.markFailedErr
}

func (r *errorOutboxRepository) GetPendingCount(_ context.Context, _ string) (int64, error) {
	return 0, nil
}

func (r *errorOutboxRepository) ResetStuckEntries(_ context.Context, _ string, _ time.Duration) (int64, error) {
	if r.resetErr != nil {
		return 0, r.resetErr
	}
	return 0, nil
}

// timeoutKafkaPublisher simulates a producer that always times out.
type timeoutKafkaPublisher struct{}

func (t *timeoutKafkaPublisher) ProduceRecord(ctx context.Context, _ *kgo.Record) error {
	// Wait for context to expire.
	<-ctx.Done()
	return context.DeadlineExceeded
}

func (t *timeoutKafkaPublisher) Flush(_ context.Context) error { return nil }
func (t *timeoutKafkaPublisher) FlushWithTimeout(_ int) int    { return 0 }
func (t *timeoutKafkaPublisher) Close()                        {}
