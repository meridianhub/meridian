package events

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
)

// Worker errors.
var (
	// ErrWorkerShutdown is returned when the worker is shutting down.
	ErrWorkerShutdown = errors.New("worker is shutting down")

	// ErrBatchProcessingFailed is returned when batch processing completes with failures.
	ErrBatchProcessingFailed = errors.New("batch processing completed with failures")

	// ErrPublishFailed is returned when event publishing fails.
	ErrPublishFailed = errors.New("event publish failed")

	// ErrUnexpectedDeliveryEvent is returned when the delivery channel receives an unexpected event type.
	ErrUnexpectedDeliveryEvent = errors.New("unexpected event type from delivery channel")

	// ErrPublishTimeout is returned when publishing times out waiting for confirmation.
	ErrPublishTimeout = errors.New("publish timeout")
)

// Default worker configuration values.
const (
	defaultBatchSize        = 100
	defaultPollInterval     = 5 * time.Second
	defaultMaxRetries       = 5
	defaultProcessingAge    = 5 * time.Minute
	defaultPublishTimeoutMs = 5000
)

// KafkaPublisher defines the interface for publishing raw messages to Kafka.
// This allows the worker to be tested without a real Kafka connection.
type KafkaPublisher interface {
	// Produce sends a message to Kafka asynchronously with delivery report.
	Produce(msg *kafka.Message, deliveryChan chan kafka.Event) error
	// Flush waits for all outstanding messages to be delivered.
	Flush(timeoutMs int) int
	// Close closes the producer.
	Close()
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
	repository   OutboxRepository
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
	repository OutboxRepository,
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
	if remaining := w.publisher.Flush(w.config.PublishTimeoutMs); remaining > 0 {
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
	// Build Kafka headers
	headers := []kafka.Header{
		{Key: "event_id", Value: []byte(entry.ID.String())},
		{Key: "event_type", Value: []byte(entry.EventType)},
		{Key: "aggregate_type", Value: []byte(entry.AggregateType)},
		{Key: "aggregate_id", Value: []byte(entry.AggregateID)},
	}

	if entry.CorrelationID != "" {
		headers = append(headers, kafka.Header{
			Key:   "correlation_id",
			Value: []byte(entry.CorrelationID),
		})
	}
	if entry.CausationID != "" {
		headers = append(headers, kafka.Header{
			Key:   "causation_id",
			Value: []byte(entry.CausationID),
		})
	}

	// Create Kafka message
	topic := entry.Topic
	msg := &kafka.Message{
		TopicPartition: kafka.TopicPartition{
			Topic:     &topic,
			Partition: kafka.PartitionAny,
		},
		Key:       []byte(entry.PartitionKey),
		Value:     entry.EventPayload,
		Headers:   headers,
		Timestamp: entry.CreatedAt,
	}

	// Publish with delivery confirmation
	deliveryChan := make(chan kafka.Event, 1)
	if err := w.publisher.Produce(msg, deliveryChan); err != nil {
		RecordPublished(w.config.ServiceName, entry.EventType, "failure")
		return fmt.Errorf("failed to produce message: %w", err)
	}

	// Wait for delivery confirmation with timeout
	select {
	case e := <-deliveryChan:
		m, ok := e.(*kafka.Message)
		if !ok {
			RecordPublished(w.config.ServiceName, entry.EventType, "failure")
			return fmt.Errorf("%w: got %T", ErrUnexpectedDeliveryEvent, e)
		}
		if m.TopicPartition.Error != nil {
			RecordPublished(w.config.ServiceName, entry.EventType, "failure")
			return fmt.Errorf("delivery failed: %w", m.TopicPartition.Error)
		}
		return nil
	case <-time.After(time.Duration(w.config.PublishTimeoutMs) * time.Millisecond):
		RecordPublished(w.config.ServiceName, entry.EventType, "timeout")
		return fmt.Errorf("%w after %dms", ErrPublishTimeout, w.config.PublishTimeoutMs)
	case <-ctx.Done():
		return fmt.Errorf("context cancelled: %w", ctx.Err())
	}
}
