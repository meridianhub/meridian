// Package messaging provides Kafka consumer adapters for audit event processing.
package messaging

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	auditv1 "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
	"github.com/meridianhub/meridian/internal/audit-consumer/observability"
	"github.com/meridianhub/meridian/shared/platform/kafka"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
)

var (
	// ErrEmptyBootstrapServers is returned when bootstrap servers configuration is empty.
	ErrEmptyBootstrapServers = errors.New("bootstrap servers cannot be empty")
	// ErrEmptyTopic is returned when topic configuration is empty.
	ErrEmptyTopic = errors.New("topic cannot be empty")
	// ErrNilDatabase is returned when database connection is nil.
	ErrNilDatabase = errors.New("database connection cannot be nil")
	// ErrUnexpectedMessageType is returned when the message type is not AuditEvent.
	ErrUnexpectedMessageType = errors.New("unexpected message type")
	// ErrMissingTenantContext is returned when the tenant context is missing.
	ErrMissingTenantContext = errors.New("missing tenant context")
	// ErrInvalidOperation is returned when the operation is invalid.
	ErrInvalidOperation = errors.New("invalid operation")
	// ErrMaxRetriesOutOfRange is returned when MaxRetries exceeds int32 bounds.
	ErrMaxRetriesOutOfRange = errors.New("MaxRetries must be between 0 and 2147483647")
)

// AuditConsumer consumes AuditEvent messages from a single Kafka topic and writes them
// to the tenant-scoped audit_log table. Each deployment processes events for one service.
type AuditConsumer struct {
	consumer    *kafka.ProtoConsumer
	db          *gorm.DB
	dlqProducer *kafka.DLQProducer
	mu          sync.RWMutex
	running     bool
}

// ConsumerConfig contains configuration for creating an audit Kafka consumer.
type ConsumerConfig struct {
	// BootstrapServers is the Kafka broker addresses (e.g., "kafka:9092").
	BootstrapServers string
	// Topic is the Kafka topic to consume audit events from (e.g., "audit.events.current-account").
	Topic string
	// GroupID is the consumer group ID (e.g., "audit-consumer-current-account").
	GroupID string
	// ClientID identifies the consumer for logging and metrics.
	ClientID string
	// DB is the GORM database connection for writing to audit_log.
	DB *gorm.DB
	// HandlerTimeout is the maximum duration for processing a single message.
	HandlerTimeout time.Duration
	// MaxRetries is the maximum number of retry attempts before sending to DLQ.
	MaxRetries int
}

// NewAuditConsumer creates a new Kafka consumer for audit events from a single topic.
// The consumer subscribes to one topic (configured via environment variable) and writes
// audit events to tenant-scoped audit_log tables using the x-tenant-id header.
//
// Parameters:
// - config: Consumer configuration with Kafka settings and database connection
//
// Returns an error if:
// - Required configuration is missing
// - Database connection is nil
// - Kafka consumer creation fails
func NewAuditConsumer(config ConsumerConfig) (*AuditConsumer, error) {
	if config.BootstrapServers == "" {
		return nil, ErrEmptyBootstrapServers
	}
	if config.Topic == "" {
		return nil, ErrEmptyTopic
	}
	if config.DB == nil {
		return nil, ErrNilDatabase
	}

	// Apply defaults
	if config.HandlerTimeout == 0 {
		config.HandlerTimeout = 30 * time.Second
	}
	if config.MaxRetries == 0 {
		config.MaxRetries = 3
	}

	// Validate MaxRetries fits in int32
	if config.MaxRetries < 0 || config.MaxRetries > math.MaxInt32 {
		return nil, ErrMaxRetriesOutOfRange
	}

	c := &AuditConsumer{
		db: config.DB,
	}

	// Create producer for DLQ
	producer, err := kafka.NewProtoProducer(kafka.ProducerConfig{
		BootstrapServers: config.BootstrapServers,
		ClientID:         config.ClientID + "-dlq",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create DLQ producer: %w", err)
	}

	// Create DLQ producer wrapper
	// Safe conversion: validated above
	maxRetries32 := int32(config.MaxRetries)
	dlqConfig := kafka.DLQConfig{
		DLQTopicSuffix:    ".dlq",
		MaxRetries:        maxRetries32,
		RetryBackoffMs:    1000,
		BackoffMultiplier: 2.0,
		ConsumerGroupID:   config.GroupID,
	}
	dlqProducer, err := kafka.NewDLQProducer(producer, dlqConfig)
	if err != nil {
		producer.Close()
		return nil, fmt.Errorf("failed to create DLQ producer: %w", err)
	}
	c.dlqProducer = dlqProducer

	// Message factory creates new AuditEvent instances for deserialization
	msgFactory := func() proto.Message {
		return &auditv1.AuditEvent{}
	}

	// Handler processes each audit event
	handler := func(ctx context.Context, _ []byte, msg proto.Message) error {
		event, ok := msg.(*auditv1.AuditEvent)
		if !ok {
			return fmt.Errorf("%w: expected *AuditEvent, got %T", ErrUnexpectedMessageType, msg)
		}
		return c.handleAuditEvent(ctx, event)
	}

	// Create the Kafka consumer with DLQ support
	consumer, err := kafka.NewProtoConsumer(
		kafka.ConsumerConfig{
			BootstrapServers: config.BootstrapServers,
			GroupID:          config.GroupID,
			ClientID:         config.ClientID,
			AutoOffsetReset:  "earliest",
			EnableAutoCommit: false,
			HandlerTimeout:   config.HandlerTimeout,
			DLQProducer:      dlqProducer,
			DLQConfig: &kafka.DLQConfig{
				DLQTopicSuffix:    ".dlq",
				MaxRetries:        maxRetries32, // Safe conversion: validated above
				RetryBackoffMs:    1000,
				BackoffMultiplier: 2.0,
			},
		},
		msgFactory,
		handler,
	)
	if err != nil {
		dlqProducer.Close()
		return nil, fmt.Errorf("failed to create Kafka consumer: %w", err)
	}
	c.consumer = consumer

	return c, nil
}

// handleAuditEvent processes a single audit event and writes it to the audit_log table.
// The tenant ID is extracted from the Kafka message header and used to scope the database write.
func (c *AuditConsumer) handleAuditEvent(ctx context.Context, event *auditv1.AuditEvent) error {
	start := time.Now()

	// Extract tenant ID from context (already injected by ProtoConsumer)
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		observability.RecordEventFailed("unknown", "unknown", "missing_tenant_context")
		return ErrMissingTenantContext
	}

	// Convert protobuf operation to string
	operation := protoToOperation(event.Operation)
	if operation == "" {
		observability.RecordEventFailed(string(tenantID), "unknown", "invalid_operation")
		return fmt.Errorf("%w: %v", ErrInvalidOperation, event.Operation)
	}

	// Handle potentially nil timestamp
	var createdAt time.Time
	if event.Timestamp != nil {
		createdAt = event.Timestamp.AsTime()
	} else {
		createdAt = time.Now()
	}

	// Build audit log entry
	auditLog := map[string]interface{}{
		"event_id":        event.EventId,
		"table_name":      event.TableName,
		"operation":       operation,
		"record_id":       event.RecordId,
		"old_values":      event.OldValues,
		"new_values":      event.NewValues,
		"created_at":      createdAt,
		"tenant_id":       string(tenantID),
		"schema_name":     event.SchemaName,
		"changed_by":      event.ChangedBy,
		"transaction_id":  event.TransactionId,
		"client_ip":       event.ClientIp,
		"user_agent":      event.UserAgent,
		"correlation_id":  event.CorrelationId,
		"causation_id":    event.CausationId,
		"idempotency_key": event.IdempotencyKey,
	}

	// Check for context cancellation before expensive DB operation
	if err := ctx.Err(); err != nil {
		observability.RecordEventFailed(string(tenantID), operation, "context_cancelled")
		return err
	}

	// Write to audit_log table (tenant-scoped via tenant_id column)
	// GORM uses table name "audit_logs" by convention
	if err := c.db.WithContext(ctx).Table("audit_logs").Create(auditLog).Error; err != nil {
		observability.RecordEventFailed(string(tenantID), operation, "db_write_failed")
		return fmt.Errorf("failed to insert audit log: %w", err)
	}

	// Record successful processing metrics
	duration := time.Since(start)
	observability.RecordEventProcessed(string(tenantID), operation)
	observability.RecordTenantAuditWriteDuration(string(tenantID), duration)

	log.Printf("DEBUG: Processed audit event: tenant=%s table=%s operation=%s record=%s duration=%v",
		tenantID, event.TableName, operation, event.RecordId, duration)

	return nil
}

// protoToOperation converts a protobuf AuditOperation to a string.
func protoToOperation(op auditv1.AuditOperation) string {
	switch op {
	case auditv1.AuditOperation_AUDIT_OPERATION_INSERT:
		return "INSERT"
	case auditv1.AuditOperation_AUDIT_OPERATION_UPDATE:
		return "UPDATE"
	case auditv1.AuditOperation_AUDIT_OPERATION_DELETE:
		return "DELETE"
	case auditv1.AuditOperation_AUDIT_OPERATION_UNSPECIFIED:
		return ""
	}
	return ""
}

// Start begins consuming audit events from the configured topic.
// This method blocks until Stop() is called or an error occurs.
//
// Parameters:
// - topic: The Kafka topic to consume from (typically from AUDIT_TOPIC environment variable)
//
// Returns an error if subscription fails.
func (c *AuditConsumer) Start(topic string) error {
	if topic == "" {
		return ErrEmptyTopic
	}

	log.Printf("INFO: Starting audit consumer for topic: %s", topic)
	if err := c.consumer.Subscribe([]string{topic}); err != nil {
		return fmt.Errorf("failed to subscribe to topic %s: %w", topic, err)
	}

	c.mu.Lock()
	c.running = true
	c.mu.Unlock()
	observability.RecordKafkaHealth(true)

	return nil
}

// Stop gracefully stops the consumer.
// Waits for in-flight messages to complete before shutting down.
func (c *AuditConsumer) Stop() {
	log.Printf("INFO: Stopping audit consumer...")
	c.mu.Lock()
	c.running = false
	c.mu.Unlock()
	observability.RecordKafkaHealth(false)
	if c.consumer != nil {
		c.consumer.Stop()
	}
	log.Printf("INFO: Audit consumer stopped")
}

// IsRunning returns true if the consumer is currently running.
// This method is used by health checks.
func (c *AuditConsumer) IsRunning() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.running
}

// Close stops the consumer and releases resources.
// Always call this when finished to free network connections.
func (c *AuditConsumer) Close() error {
	c.Stop()

	var closeErr error
	if c.consumer != nil {
		if err := c.consumer.Close(); err != nil {
			closeErr = fmt.Errorf("consumer close error: %w", err)
		}
	}
	if c.dlqProducer != nil {
		c.dlqProducer.Close()
	}

	return closeErr
}
