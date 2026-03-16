package events

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/twmb/franz-go/pkg/kgo"
)

// Worker errors.
var (
	// ErrWorkerShutdown is returned when the worker is shutting down.
	ErrWorkerShutdown = errors.New("worker is shutting down")

	// ErrBatchProcessingFailed is returned when batch processing completes with failures.
	ErrBatchProcessingFailed = errors.New("batch processing completed with failures")

	// ErrPublishFailed is returned when event publishing fails.
	ErrPublishFailed = errors.New("event publish failed")

	// ErrPublishTimeout is returned when publishing times out waiting for confirmation.
	ErrPublishTimeout = errors.New("publish timeout")
)

// Default worker configuration values.
const (
	defaultBatchSize        = 100
	defaultPollInterval     = defaults.DefaultHealthCheckTimeout
	defaultMaxRetries       = 5
	defaultProcessingAge    = 5 * time.Minute
	defaultPublishTimeoutMs = 5000
)

// KafkaPublisher defines the interface for publishing records to Kafka.
// This allows the worker to be tested without a real Kafka connection.
type KafkaPublisher interface {
	// ProduceRecord sends a record to Kafka synchronously with delivery confirmation.
	ProduceRecord(ctx context.Context, record *kgo.Record) error
	// Flush waits for all outstanding messages to be delivered.
	Flush(ctx context.Context) error
	// FlushWithTimeout waits for outstanding messages with a timeout.
	// Returns the number of messages still in flight (0 = all delivered).
	FlushWithTimeout(timeoutMs int) int
	// Close closes the producer.
	Close()
}

// WorkerRepository defines the interface for repository operations used by the Worker.
// This is a subset of OutboxRepository that excludes Insert, allowing both GORM-based
// (PostgresOutboxRepository) and pgx-based (PgxOutboxRepository) implementations to be used.
type WorkerRepository interface {
	// FetchAndLockForProcessing atomically fetches pending entries and marks them as processing.
	FetchAndLockForProcessing(ctx context.Context, serviceName string, limit int) ([]EventOutbox, error)

	// MarkCompleted marks an entry as successfully processed.
	MarkCompleted(ctx context.Context, id uuid.UUID) error

	// MarkFailed increments retry count and updates error message.
	MarkFailed(ctx context.Context, id uuid.UUID, err error, maxRetries int) error

	// GetPendingCount returns the number of pending entries for observability.
	GetPendingCount(ctx context.Context, serviceName string) (int64, error)

	// ResetStuckEntries resets entries stuck in 'processing' state for too long.
	ResetStuckEntries(ctx context.Context, serviceName string, olderThan time.Duration) (int64, error)
}

// WorkerConfig contains configuration for the event outbox worker.
type WorkerConfig struct {
	// ServiceName identifies this service for filtering outbox entries.
	ServiceName string

	// BatchSize is the maximum number of entries to process in one batch.
	// Default: 100
	BatchSize int

	// PollInterval is how often to poll for new entries.
	// Default: 5 seconds
	PollInterval time.Duration

	// MaxRetries is the maximum number of retry attempts before moving to failed state.
	// Default: 5
	MaxRetries int

	// ProcessingAge is the duration after which 'processing' entries are considered stuck.
	// Default: 5 minutes
	ProcessingAge time.Duration

	// PublishTimeoutMs is the Kafka publish timeout in milliseconds.
	// Default: 5000
	PublishTimeoutMs int
}

// DefaultWorkerConfig returns a WorkerConfig with sensible defaults.
func DefaultWorkerConfig(serviceName string) WorkerConfig {
	return WorkerConfig{
		ServiceName:      serviceName,
		BatchSize:        defaultBatchSize,
		PollInterval:     defaultPollInterval,
		MaxRetries:       defaultMaxRetries,
		ProcessingAge:    defaultProcessingAge,
		PublishTimeoutMs: defaultPublishTimeoutMs,
	}
}

// Worker is a background processor that publishes events from the outbox to Kafka.
// It implements graceful shutdown and handles retries with exponential backoff.
type Worker struct {
	repository   WorkerRepository
	publisher    KafkaPublisher
	config       WorkerConfig
	logger       *slog.Logger
	shutdown     chan struct{}
	shutdownOnce sync.Once
	wg           sync.WaitGroup
}

// NewWorker creates a new event outbox worker.
//
// Parameters:
//   - repository: The outbox repository for database operations
//   - publisher: Kafka publisher for sending events
//   - config: Worker configuration
//   - logger: Structured logger (uses slog.Default() if nil)
//
// Returns a configured Worker ready to start processing.
func NewWorker(
	repository WorkerRepository,
	publisher KafkaPublisher,
	config WorkerConfig,
	logger *slog.Logger,
) *Worker {
	if logger == nil {
		logger = slog.Default()
	}

	// Apply defaults
	if config.BatchSize <= 0 {
		config.BatchSize = defaultBatchSize
	}
	if config.PollInterval <= 0 {
		config.PollInterval = defaultPollInterval
	}
	if config.MaxRetries <= 0 {
		config.MaxRetries = defaultMaxRetries
	}
	if config.ProcessingAge <= 0 {
		config.ProcessingAge = defaultProcessingAge
	}
	if config.PublishTimeoutMs <= 0 {
		config.PublishTimeoutMs = defaultPublishTimeoutMs
	}

	return &Worker{
		repository: repository,
		publisher:  publisher,
		config:     config,
		logger:     logger.With("service", config.ServiceName),
		shutdown:   make(chan struct{}),
	}
}

// Start begins background processing of event outbox entries.
// This method spawns a goroutine and returns immediately.
// The worker will continue processing until Stop() is called or the context is cancelled.
func (w *Worker) Start(ctx context.Context) {
	w.wg.Add(1)
	go w.run(ctx)

	w.logger.Info("event outbox worker started",
		"batch_size", w.config.BatchSize,
		"poll_interval", w.config.PollInterval,
		"max_retries", w.config.MaxRetries)
}

// Stop initiates graceful shutdown of the worker.
// It signals the worker to stop, waits for the current batch to complete,
// and flushes any remaining Kafka messages.
// Safe to call multiple times - subsequent calls will block until shutdown completes.
func (w *Worker) Stop() {
	w.shutdownOnce.Do(func() {
		w.logger.Info("event outbox worker stopping")
		close(w.shutdown)
	})
	w.wg.Wait()

	// Flush any remaining messages to Kafka
	if remaining := w.publisher.FlushWithTimeout(w.config.PublishTimeoutMs); remaining > 0 {
		w.logger.Warn("unflushed Kafka messages on shutdown", "count", remaining)
	}

	w.logger.Info("event outbox worker stopped")
}

// run is the main processing loop that polls the outbox and processes batches.
func (w *Worker) run(ctx context.Context) {
	defer w.wg.Done()

	ticker := time.NewTicker(w.config.PollInterval)
	defer ticker.Stop()

	w.logger.Info("event outbox worker processing loop started")

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("event outbox worker context cancelled")
			return
		case <-w.shutdown:
			w.logger.Info("event outbox worker shutdown signal received")
			return
		case <-ticker.C:
			// Reset stuck entries before processing
			if err := w.resetStuckEntries(ctx); err != nil {
				w.logger.Error("failed to reset stuck entries", "error", err)
			}

			// Process batch
			if err := w.processBatch(ctx); err != nil {
				w.logger.Error("batch processing failed", "error", err)
			}
		}
	}
}

// resetStuckEntries resets entries that have been in 'processing' state for too long.
func (w *Worker) resetStuckEntries(ctx context.Context) error {
	count, err := w.repository.ResetStuckEntries(ctx, w.config.ServiceName, w.config.ProcessingAge)
	if err != nil {
		return fmt.Errorf("failed to reset stuck entries: %w", err)
	}

	if count > 0 {
		w.logger.Warn("reset stuck processing entries",
			"count", count,
			"threshold", w.config.ProcessingAge)
		RecordStuckEntriesReset(w.config.ServiceName, int(count))
	}

	return nil
}

// processBatch fetches and processes a batch of pending event outbox entries.
func (w *Worker) processBatch(ctx context.Context) error {
	start := time.Now()
	defer func() {
		RecordProcessingDuration(w.config.ServiceName, time.Since(start).Seconds())
	}()

	// Fetch and atomically lock pending entries for processing.
	// Uses FOR UPDATE SKIP LOCKED to prevent race conditions in multi-worker deployments.
	entries, err := w.repository.FetchAndLockForProcessing(ctx, w.config.ServiceName, w.config.BatchSize)
	if err != nil {
		return fmt.Errorf("failed to fetch and lock entries: %w", err)
	}

	// Record outbox depth
	pendingCount, countErr := w.repository.GetPendingCount(ctx, w.config.ServiceName)
	if countErr == nil {
		RecordOutboxDepth(w.config.ServiceName, int(pendingCount))
	}

	if len(entries) == 0 {
		return nil
	}

	w.logger.Info("processing event outbox batch", "count", len(entries))

	// Process entries sequentially to maintain order
	var processedCount, failedCount int
	for i := range entries {
		entry := &entries[i]

		if err := w.processEntry(ctx, entry); err != nil {
			w.logger.Error("failed to process event outbox entry",
				"entry_id", entry.ID,
				"event_type", entry.EventType,
				"aggregate_id", entry.AggregateID,
				"retry_count", entry.RetryCount,
				"error", err)
			failedCount++
		} else {
			processedCount++
		}
	}

	w.logger.Info("batch processing completed",
		"processed", processedCount,
		"failed", failedCount,
		"total", len(entries))

	if failedCount > 0 {
		return fmt.Errorf("%w: %d failures out of %d entries", ErrBatchProcessingFailed, failedCount, len(entries))
	}

	return nil
}

// processEntry processes a single event outbox entry.
func (w *Worker) processEntry(ctx context.Context, entry *EventOutbox) error {
	// Record entry age
	entryAge := time.Since(entry.CreatedAt).Seconds()
	RecordEntryAge(w.config.ServiceName, entryAge)

	// Publish to Kafka
	err := w.publishToKafka(ctx, entry)
	if err != nil {
		// Mark as failed and potentially retry
		if markErr := w.repository.MarkFailed(ctx, entry.ID, err, w.config.MaxRetries); markErr != nil {
			w.logger.Error("failed to mark entry as failed",
				"entry_id", entry.ID,
				"error", markErr)
		}

		// Check if retries exhausted.
		// NOTE: entry.RetryCount is the in-memory value before MarkFailed's atomic increment.
		// This is intentional - we use +1 to predict the new count for logging purposes.
		// The actual DB count is authoritative and was atomically incremented in MarkFailed.
		if entry.RetryCount+1 >= w.config.MaxRetries {
			RecordDLQEntry(w.config.ServiceName, entry.EventType)
			w.logger.Error("event moved to DLQ (retries exhausted)",
				"entry_id", entry.ID,
				"event_type", entry.EventType,
				"aggregate_id", entry.AggregateID,
				"retry_count", entry.RetryCount+1,
				"max_retries", w.config.MaxRetries,
				"error", err)
		} else {
			// Record retry metric when the entry will be retried
			RecordRetry(w.config.ServiceName, entry.EventType)
		}

		return fmt.Errorf("publish failed for entry %s: %w", entry.ID, err)
	}

	// Mark as completed
	if err := w.repository.MarkCompleted(ctx, entry.ID); err != nil {
		return fmt.Errorf("failed to mark entry as completed: %w", err)
	}

	RecordPublished(w.config.ServiceName, entry.EventType, "success")

	w.logger.Debug("event published successfully",
		"entry_id", entry.ID,
		"event_type", entry.EventType,
		"aggregate_id", entry.AggregateID,
		"topic", entry.Topic)

	return nil
}

// publishToKafka publishes an event to Kafka and waits for delivery confirmation.
func (w *Worker) publishToKafka(ctx context.Context, entry *EventOutbox) error {
	// Build Kafka record headers
	headers := []kgo.RecordHeader{
		{Key: "event_id", Value: []byte(entry.ID.String())},
		{Key: "event_type", Value: []byte(entry.EventType)},
		{Key: "aggregate_type", Value: []byte(entry.AggregateType)},
		{Key: "aggregate_id", Value: []byte(entry.AggregateID)},
	}

	if entry.CorrelationID != "" {
		headers = append(headers, kgo.RecordHeader{
			Key:   "correlation_id",
			Value: []byte(entry.CorrelationID),
		})
	}
	if entry.CausationID != "" {
		headers = append(headers, kgo.RecordHeader{
			Key:   "causation_id",
			Value: []byte(entry.CausationID),
		})
	}
	if entry.TenantID != "" {
		headers = append(headers, kgo.RecordHeader{
			Key:   "X-Tenant-ID",
			Value: []byte(entry.TenantID),
		})
	}

	// Create Kafka record
	record := &kgo.Record{
		Topic:     entry.Topic,
		Key:       []byte(entry.PartitionKey),
		Value:     entry.EventPayload,
		Headers:   headers,
		Timestamp: entry.CreatedAt,
	}

	// Create context with timeout for publishing
	publishCtx, cancel := context.WithTimeout(ctx, time.Duration(w.config.PublishTimeoutMs)*time.Millisecond)
	defer cancel()

	// Publish synchronously with delivery confirmation
	if err := w.publisher.ProduceRecord(publishCtx, record); err != nil {
		RecordPublished(w.config.ServiceName, entry.EventType, "failure")
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("%w: after %dms", ErrPublishTimeout, w.config.PublishTimeoutMs)
		}
		return fmt.Errorf("failed to produce message: %w", err)
	}

	return nil
}
