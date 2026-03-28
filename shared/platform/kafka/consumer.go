package kafka

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
)

// MessageHandler is called for each message received from Kafka.
// The handler should return an error if the message cannot be processed.
// Errors are logged but do not stop the consumer - implement dead letter queue
// or retry logic in production for better error handling.
//
// Parameters:
// - ctx: Context with timeout (default 30s) for processing the message
// - key: Kafka message key as bytes (may be nil)
// - msg: Deserialized protobuf message
//
// Returns an error if message processing fails.
type MessageHandler func(ctx context.Context, key []byte, msg proto.Message) error

// ProtoConsumer handles consuming Protocol Buffer messages from Kafka topics.
// It provides automatic deserialization, error recovery, and graceful shutdown.
// The consumer runs in a blocking loop until Stop() is called or an unrecoverable error occurs.
type ProtoConsumer struct {
	// client is the underlying franz-go client instance
	client *kgo.Client
	// msgFactory creates new instances of the protobuf message type for deserialization
	msgFactory func() proto.Message
	// handler processes each consumed message
	handler MessageHandler
	// pollTimeout is the duration to wait for messages before checking shutdown signal
	pollTimeout time.Duration
	// handlerTimeout is the maximum duration for processing a single message
	handlerTimeout time.Duration
	// dlqProducer handles failed messages (optional, may be nil)
	dlqProducer *DLQProducer
	// dlqConfig contains DLQ behavior configuration
	dlqConfig *DLQConfig
	// wg tracks the Subscribe goroutine for graceful shutdown
	wg sync.WaitGroup
	// ctx provides cancellation signal for graceful shutdown
	ctx context.Context
	// cancel triggers shutdown of the consumer loop
	cancel context.CancelFunc
}

// ConsumerConfig contains configuration for creating a Kafka consumer.
// All fields except EnableAutoCommit have defaults applied if empty.
type ConsumerConfig struct {
	// BootstrapServers is the comma-separated list of Kafka broker addresses (required).
	BootstrapServers string
	// GroupID identifies the consumer group for coordinated consumption (required).
	GroupID string
	// ClientID identifies the consumer for logging and metrics (optional).
	ClientID string
	// AutoOffsetReset determines where to start consuming if no offset exists:
	// "earliest" (default) starts from beginning, "latest" starts from end.
	AutoOffsetReset string
	// EnableAutoCommit when true enables automatic offset commits, when false
	// offsets are committed manually after successful message processing.
	EnableAutoCommit bool
	// PollTimeout is the duration to wait for new messages before checking shutdown signal.
	// Default: 100ms. Lower values improve shutdown responsiveness, higher values reduce CPU.
	PollTimeout time.Duration
	// HandlerTimeout is the maximum duration for processing a single message.
	// Default: 30s. Handlers exceeding this timeout will be cancelled.
	HandlerTimeout time.Duration
	// DLQProducer is an optional dead letter queue producer for failed messages.
	// If nil, DLQ functionality is disabled and errors are only logged.
	DLQProducer *DLQProducer
	// DLQConfig contains DLQ behavior configuration (retry count, backoff, etc.).
	// Only used if DLQProducer is not nil.
	DLQConfig *DLQConfig
}

var (
	// ErrEmptyGroupID is returned when group ID is empty.
	ErrEmptyGroupID = errors.New("group ID cannot be empty")
	// ErrNilMsgFactory is returned when message factory is nil.
	ErrNilMsgFactory = errors.New("message factory cannot be nil")
	// ErrNilHandler is returned when message handler is nil.
	ErrNilHandler = errors.New("message handler cannot be nil")
	// ErrEmptyTopics is returned when topics list is empty.
	ErrEmptyTopics = errors.New("topics cannot be empty")
	// ErrMissingTenantHeader is returned when the x-tenant-id header is missing from a Kafka message.
	ErrMissingTenantHeader = errors.New("missing x-tenant-id header")
)

// NewProtoConsumer creates a new Kafka consumer for protobuf messages.
// The consumer requires a message factory to create typed protobuf instances for deserialization,
// and a handler to process each consumed message.
//
// Parameters:
// - config: Consumer configuration (BootstrapServers and GroupID are required)
// - msgFactory: Function that creates a new instance of the proto message type to deserialize into
// - handler: Function called for each consumed message
//
// Returns an error if:
// - BootstrapServers is empty
// - GroupID is empty
// - msgFactory is nil
// - handler is nil
// - underlying Kafka consumer fails to initialize
func NewProtoConsumer(config ConsumerConfig, msgFactory func() proto.Message, handler MessageHandler) (*ProtoConsumer, error) {
	if err := validateConsumerConfig(config, msgFactory, handler); err != nil {
		return nil, err
	}

	applyConsumerDefaults(&config)

	opts := buildConsumerOpts(config)

	client, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create kafka consumer: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &ProtoConsumer{
		client:         client,
		msgFactory:     msgFactory,
		handler:        handler,
		pollTimeout:    config.PollTimeout,
		handlerTimeout: config.HandlerTimeout,
		dlqProducer:    config.DLQProducer,
		dlqConfig:      config.DLQConfig,
		ctx:            ctx,
		cancel:         cancel,
	}, nil
}

// validateConsumerConfig checks required consumer configuration parameters.
func validateConsumerConfig(config ConsumerConfig, msgFactory func() proto.Message, handler MessageHandler) error {
	if config.BootstrapServers == "" {
		return ErrEmptyBootstrapServers
	}
	if config.GroupID == "" {
		return ErrEmptyGroupID
	}
	if msgFactory == nil {
		return ErrNilMsgFactory
	}
	if handler == nil {
		return ErrNilHandler
	}
	return nil
}

// applyConsumerDefaults fills zero-valued fields with sensible defaults.
func applyConsumerDefaults(config *ConsumerConfig) {
	if config.AutoOffsetReset == "" {
		config.AutoOffsetReset = "earliest"
	}
	if config.PollTimeout == 0 {
		config.PollTimeout = defaults.DefaultRetryDelay
	}
	if config.HandlerTimeout == 0 {
		config.HandlerTimeout = defaults.DefaultRPCTimeout
	}
}

// buildConsumerOpts constructs franz-go client options from consumer config.
func buildConsumerOpts(config ConsumerConfig) []kgo.Opt {
	opts := []kgo.Opt{
		kgo.SeedBrokers(splitBrokers(config.BootstrapServers)...),
		kgo.ConsumerGroup(config.GroupID),
		kgo.BlockRebalanceOnPoll(),
	}

	if config.ClientID != "" {
		opts = append(opts, kgo.ClientID(config.ClientID))
	}

	switch config.AutoOffsetReset {
	case "earliest":
		opts = append(opts, kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	case "latest":
		opts = append(opts, kgo.ConsumeResetOffset(kgo.NewOffset().AtEnd()))
	default:
		opts = append(opts, kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	}

	if !config.EnableAutoCommit {
		opts = append(opts, kgo.DisableAutoCommit())
	}

	return opts
}

// splitBrokers splits comma-separated broker addresses into a slice.
func splitBrokers(brokers string) []string {
	var result []string
	for _, broker := range splitString(brokers, ',') {
		if broker != "" {
			result = append(result, broker)
		}
	}
	return result
}

// splitString splits a string by the given separator.
func splitString(s string, sep rune) []string {
	var result []string
	current := ""
	for _, c := range s {
		if c == sep {
			result = append(result, current)
			current = ""
		} else {
			current += string(c)
		}
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}

// Subscribe starts consuming from the specified topics.
// This method blocks until Stop() is called or an unrecoverable error occurs.
// The consumer will:
// - Join the consumer group
// - Poll for messages with configurable timeout
// - Deserialize protobuf messages using the factory
// - Call the handler with a 30s timeout
// - Retry failed messages with exponential backoff (if DLQ configured)
// - Send permanently failed messages to DLQ after exhausting retries
// - Commit offsets only when ALL records in a batch are processed successfully
// - Skip offset commit if any record fails (including DLQ publish failures) to ensure redelivery
// - Continue consuming even after failures (failed messages will be redelivered)
// - Exit gracefully when Stop() is called
//
// Parameters:
// - topics: List of Kafka topic names to consume from (must not be empty)
//
// Returns an error if:
// - topics list is empty
// - subscription fails
// - unrecoverable Kafka error occurs (timeouts are handled internally)
func (c *ProtoConsumer) Subscribe(topics []string) error {
	if len(topics) == 0 {
		return ErrEmptyTopics
	}

	// Add topics to consume
	c.client.AddConsumeTopics(topics...)

	// Track this goroutine for graceful shutdown
	c.wg.Add(1)
	defer c.wg.Done()

	// Start consuming loop
	for {
		select {
		case <-c.ctx.Done():
			return nil
		default:
			// Poll for messages with timeout
			pollCtx, pollCancel := context.WithTimeout(c.ctx, c.pollTimeout)
			fetches := c.client.PollFetches(pollCtx)
			pollCancel()

			// Check for errors in fetches
			if errs := fetches.Errors(); len(errs) > 0 {
				for _, err := range errs {
					// Context cancellation is expected during shutdown
					if errors.Is(err.Err, context.Canceled) || errors.Is(err.Err, context.DeadlineExceeded) {
						continue
					}
					log.Printf("WARN: Fetch error for topic=%s partition=%d: %v",
						err.Topic, err.Partition, err.Err)
				}
			}

			// Track whether any record processing failed (especially DLQ failures)
			// If any record fails, we must not commit offsets to prevent message loss
			var processingFailed bool

			// Process each record
			fetches.EachRecord(func(record *kgo.Record) {
				// Process message with retry and DLQ support
				if err := c.processMessageWithRetry(record); err != nil {
					// Mark batch as failed to prevent offset commit
					processingFailed = true
					// Log error with full context for debugging and monitoring
					log.Printf("ERROR: Failed to process message from topic=%s partition=%d offset=%d after all retries: %v",
						record.Topic,
						record.Partition,
						record.Offset,
						err)
					// Message will be redelivered on next poll since offset won't be committed
				}
			})

			// Allow rebalance to proceed now that we're done processing
			c.client.AllowRebalance()

			// Commit offsets only if all records were processed successfully
			// If any record failed (including DLQ failures), skip commit to ensure redelivery
			if !fetches.Empty() && !processingFailed {
				if err := c.client.CommitUncommittedOffsets(c.ctx); err != nil {
					log.Printf("WARN: Failed to commit offsets: %v", err)
				}
			} else if processingFailed {
				log.Printf("WARN: Skipping offset commit due to processing failures - messages will be redelivered")
			}
		}
	}
}

// ExtractTenantHeader extracts and validates the tenant ID from a Kafka record header.
// It looks for the x-tenant-id header and validates the tenant ID format.
//
// Returns:
// - The tenant ID if the header is present and valid
// - ErrMissingTenantHeader if the record is nil or the header is not present
// - tenant.ErrInvalidTenantID if the header value is invalid
func ExtractTenantHeader(record *kgo.Record) (tenant.TenantID, error) {
	if record == nil {
		return "", ErrMissingTenantHeader
	}
	for _, h := range record.Headers {
		if h.Key == tenant.TenantIDKey {
			return tenant.NewTenantID(string(h.Value))
		}
	}
	return "", ErrMissingTenantHeader
}

// processMessage deserializes and handles a Kafka record.
// This is an internal method that:
// 1. Extracts tenant ID from Kafka header
// 2. Creates a new protobuf message instance using the factory
// 3. Deserializes the Kafka message value into the proto message
// 4. Calls the handler with tenant context and configured timeout
//
// Returns an error if header extraction, deserialization, or handler execution fails.
// Note: Errors returned here bubble up through processMessageWithRetry, which handles
// DLQ routing after exhausting retries. Messages with missing/invalid tenant
// headers will eventually be sent to DLQ if configured.
func (c *ProtoConsumer) processMessage(record *kgo.Record) error {
	// Extract tenant header
	orgID, err := ExtractTenantHeader(record)
	if err != nil {
		return fmt.Errorf("failed to extract tenant header: %w", err)
	}

	// Create new proto message instance
	protoMsg := c.msgFactory()

	// Deserialize from bytes
	if err := proto.Unmarshal(record.Value, protoMsg); err != nil {
		return fmt.Errorf("failed to unmarshal protobuf message: %w", err)
	}

	// Call handler with tenant context and configured timeout
	ctx, cancel := context.WithTimeout(c.ctx, c.handlerTimeout)
	defer cancel()

	// Inject tenant context
	ctx = tenant.WithTenant(ctx, orgID)

	if err := c.handler(ctx, record.Key, protoMsg); err != nil {
		return fmt.Errorf("handler error: %w", err)
	}

	return nil
}

// processMessageWithRetry attempts to process a message with configurable retry and DLQ support.
// This method implements exponential backoff retry logic and sends failed messages to DLQ after
// exhausting all retry attempts.
//
// Behavior:
//   - If DLQ is not configured: Attempts processing once, returns error on failure
//   - If DLQ is configured: Retries up to MaxRetries times with exponential backoff,
//     sends to DLQ after exhausting retries, returns nil (message handled)
//
// Returns an error only if:
// - DLQ is not configured and processing fails
// - DLQ publishing fails (rare - indicates infrastructure issue)
func (c *ProtoConsumer) processMessageWithRetry(record *kgo.Record) error {
	if c.dlqProducer == nil || c.dlqConfig == nil {
		return c.processMessage(record)
	}

	firstFailureTime := time.Now()
	maxRetries := c.dlqConfig.MaxRetries
	if maxRetries == 0 {
		maxRetries = 3
	}

	lastErr := c.retryProcessing(record, maxRetries)
	if lastErr == nil {
		return nil
	}

	// If shutdown occurred during retry backoff, return the error directly
	// without sending to DLQ - the message will be reprocessed after restart.
	if c.ctx.Err() != nil {
		return lastErr
	}

	return c.sendToDLQ(record, lastErr, maxRetries, firstFailureTime)
}

// retryProcessing attempts to process a message up to maxRetries times with exponential backoff.
// Returns nil on success, or the last error after all retries are exhausted.
func (c *ProtoConsumer) retryProcessing(record *kgo.Record, maxRetries int32) error {
	var lastErr error
	for attempt := int32(1); attempt <= maxRetries; attempt++ {
		err := c.processMessage(record)
		if err == nil {
			return nil
		}
		lastErr = err

		log.Printf("WARN: Message processing attempt %d/%d failed for topic=%s partition=%d offset=%d: %v",
			attempt, maxRetries, record.Topic, record.Partition, record.Offset, lastErr)

		if attempt < maxRetries {
			backoff := c.dlqConfig.CalculateBackoff(attempt)
			log.Printf("INFO: Retrying after %v backoff", backoff)

			select {
			case <-time.After(backoff):
			case <-c.ctx.Done():
				return fmt.Errorf("retry cancelled due to shutdown: %w", lastErr)
			}
		}
	}
	return lastErr
}

// sendToDLQ publishes a failed record to the dead letter queue after retries are exhausted.
func (c *ProtoConsumer) sendToDLQ(record *kgo.Record, lastErr error, maxRetries int32, firstFailureTime time.Time) error {
	log.Printf("ERROR: All %d retry attempts exhausted for topic=%s partition=%d offset=%d, sending to DLQ",
		maxRetries, record.Topic, record.Partition, record.Offset)

	dlqCtx, cancel := context.WithTimeout(c.ctx, 30*time.Second)
	defer cancel()

	if err := c.dlqProducer.PublishFailedRecord(dlqCtx, record, lastErr, maxRetries, firstFailureTime); err != nil {
		log.Printf("CRITICAL: Failed to publish message to DLQ for topic=%s partition=%d offset=%d: %v",
			record.Topic, record.Partition, record.Offset, err)
		return fmt.Errorf("DLQ publishing failed: %w", err)
	}

	log.Printf("INFO: Message successfully sent to DLQ for topic=%s partition=%d offset=%d",
		record.Topic, record.Partition, record.Offset)

	return nil
}

// Stop stops the consumer gracefully.
// This triggers the Subscribe() loop to exit and waits for it to finish.
// Safe to call multiple times.
func (c *ProtoConsumer) Stop() {
	c.cancel()
	c.wg.Wait()
}

// Close closes the consumer and releases resources.
// This calls Stop() to trigger graceful shutdown, waits for Subscribe() to exit,
// then closes the underlying Kafka consumer. Always call this when finished with
// the consumer to free network connections and other system resources.
//
// Returns an error if the underlying consumer close fails.
func (c *ProtoConsumer) Close() error {
	c.Stop()
	c.client.Close()
	return nil
}
