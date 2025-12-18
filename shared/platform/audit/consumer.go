package audit

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"sync"
	"time"

	"github.com/google/uuid"
	auditv1 "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
	"github.com/meridianhub/meridian/shared/platform/kafka"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Errors returned by the audit Consumer.
var (
	// ErrEmptyBootstrapServers is returned when bootstrap servers configuration is empty.
	ErrEmptyBootstrapServers = errors.New("bootstrap servers cannot be empty")
	// ErrNilDatabase is returned when database connection is nil.
	ErrNilDatabase = errors.New("database connection cannot be nil")
	// ErrUnexpectedMessageType is returned when the message type is not AuditEvent.
	ErrUnexpectedMessageType = errors.New("unexpected message type")
	// ErrInvalidOperation is returned when the operation in the event is invalid.
	ErrInvalidOperation = errors.New("invalid operation")
	// ErrInvalidSchemaName is returned when the schema name contains invalid characters.
	ErrInvalidSchemaName = errors.New("invalid schema name")
)

// schemaNamePattern validates PostgreSQL schema names to prevent SQL injection.
// Schema names must start with a letter or underscore, followed by letters, digits, or underscores.
var schemaNamePattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// isValidSchemaName checks if a schema name is safe for use in SQL statements.
func isValidSchemaName(name string) bool {
	return schemaNamePattern.MatchString(name) && len(name) <= 63 // PostgreSQL max identifier length
}

// Consumer processes audit events from Kafka and writes them to the audit_log table.
// It provides at-least-once processing guarantees with DLQ support for failed messages.
type Consumer struct {
	consumer    *kafka.ProtoConsumer
	db          *gorm.DB
	dlqProducer *kafka.DLQProducer

	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
}

// ConsumerConfig contains configuration for creating an audit Consumer.
type ConsumerConfig struct {
	// BootstrapServers is the Kafka broker addresses (e.g., "kafka:9092").
	BootstrapServers string
	// GroupID is the consumer group ID (default: "audit-consumer-group").
	GroupID string
	// ClientID identifies the consumer for logging and metrics.
	ClientID string
	// Topic is the Kafka topic for audit events (default: "audit.events").
	Topic string
	// DLQTopic is the dead letter queue topic (default: "audit.events.dlq").
	DLQTopic string
	// DB is the GORM database connection for writing to audit_log.
	DB *gorm.DB
	// HandlerTimeout is the maximum duration for processing a single message.
	HandlerTimeout time.Duration
	// MaxRetries is the maximum number of retry attempts before sending to DLQ.
	MaxRetries int
}

// NewConsumer creates a new audit Consumer.
func NewConsumer(config ConsumerConfig) (*Consumer, error) {
	if config.BootstrapServers == "" {
		return nil, ErrEmptyBootstrapServers
	}
	if config.DB == nil {
		return nil, ErrNilDatabase
	}

	// Apply defaults
	if config.GroupID == "" {
		config.GroupID = kafka.AuditConsumerGroup
	}
	if config.Topic == "" {
		config.Topic = kafka.AuditEventsTopic
	}
	if config.DLQTopic == "" {
		config.DLQTopic = kafka.AuditEventsDLQTopic
	}
	if config.HandlerTimeout == 0 {
		config.HandlerTimeout = 30 * time.Second
	}
	if config.MaxRetries == 0 {
		config.MaxRetries = 3
	}

	ctx, cancel := context.WithCancel(context.Background())

	c := &Consumer{
		db:     config.DB,
		ctx:    ctx,
		cancel: cancel,
	}

	// Create producer for DLQ
	producer, err := kafka.NewProtoProducer(kafka.ProducerConfig{
		BootstrapServers: config.BootstrapServers,
		ClientID:         config.ClientID + "-dlq",
	})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create DLQ producer: %w", err)
	}

	// Create DLQ producer wrapper
	dlqConfig := kafka.DLQConfig{
		DLQTopicSuffix:    "", // We use explicit topic name
		MaxRetries:        int32(config.MaxRetries),
		RetryBackoffMs:    1000,
		BackoffMultiplier: 2.0,
		ConsumerGroupID:   config.GroupID,
	}
	dlqProducer, err := kafka.NewDLQProducer(producer, dlqConfig)
	if err != nil {
		producer.Close()
		cancel()
		return nil, fmt.Errorf("failed to create DLQ producer: %w", err)
	}
	c.dlqProducer = dlqProducer

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
				MaxRetries:        int32(config.MaxRetries),
				RetryBackoffMs:    1000,
				BackoffMultiplier: 2.0,
			},
		},
		func() proto.Message { return &auditv1.AuditEvent{} },
		c.handleMessage,
	)
	if err != nil {
		dlqProducer.Close()
		cancel()
		return nil, fmt.Errorf("failed to create Kafka consumer: %w", err)
	}
	c.consumer = consumer

	return c, nil
}

// Start begins consuming audit events from Kafka.
// This method is non-blocking; it starts a goroutine that runs until Stop is called.
func (c *Consumer) Start(topic string) error {
	if topic == "" {
		topic = kafka.AuditEventsTopic
	}

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		if err := c.consumer.Subscribe([]string{topic}); err != nil {
			// Check if this is a shutdown error
			if c.ctx.Err() != nil {
				log.Printf("INFO: Audit consumer stopped gracefully")
				return
			}
			log.Printf("ERROR: Audit consumer subscription error: %v", err)
		}
	}()

	log.Printf("INFO: Audit consumer started, subscribing to topic: %s", topic)
	return nil
}

// Stop gracefully stops the consumer.
func (c *Consumer) Stop() {
	log.Printf("INFO: Stopping audit consumer...")
	c.cancel()
	if c.consumer != nil {
		c.consumer.Stop()
	}
	c.wg.Wait()
	log.Printf("INFO: Audit consumer stopped")
}

// Close stops the consumer and releases resources.
func (c *Consumer) Close() error {
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

// handleMessage processes an audit event and writes it to the audit_log table.
func (c *Consumer) handleMessage(ctx context.Context, _ []byte, msg proto.Message) error {
	startTime := time.Now()

	event, ok := msg.(*auditv1.AuditEvent)
	if !ok {
		return fmt.Errorf("%w: %T", ErrUnexpectedMessageType, msg)
	}

	// Convert protobuf operation to string
	operation := protoToOperation(event.Operation)
	if operation == "" {
		return fmt.Errorf("%w: %v", ErrInvalidOperation, event.Operation)
	}

	schema := event.SchemaName
	if schema == "" {
		schema = "unknown"
	}

	// Handle potentially nil timestamp
	var createdAt time.Time
	if event.Timestamp != nil {
		createdAt = event.Timestamp.AsTime()
	} else {
		createdAt = time.Now()
	}

	// Create audit log entry
	auditLog := AuditLog{
		ID:        uuid.New(),
		Table:     event.TableName,
		Operation: operation,
		RecordID:  event.RecordId,
		OldValues: event.OldValues,
		NewValues: event.NewValues,
		CreatedAt: createdAt,
	}

	if event.ChangedBy != "" {
		auditLog.ChangedBy = &event.ChangedBy
	}
	if event.TransactionId != "" {
		auditLog.TransactionID = &event.TransactionId
	}
	if event.ClientIp != "" {
		auditLog.ClientIP = &event.ClientIp
	}
	if event.UserAgent != "" {
		auditLog.UserAgent = &event.UserAgent
	}

	// Write to audit_log using the schema from the event
	db := c.db
	if event.SchemaName != "" {
		// Validate schema name to prevent SQL injection
		if !isValidSchemaName(event.SchemaName) {
			RecordKafkaConsumed(schema, operation, "failure")
			return fmt.Errorf("%w: %s", ErrInvalidSchemaName, event.SchemaName)
		}
		// Set search_path to the service schema for routing
		db = db.Exec(fmt.Sprintf("SET LOCAL search_path TO %s", event.SchemaName))
	}

	if err := db.WithContext(ctx).Create(&auditLog).Error; err != nil {
		RecordKafkaConsumed(schema, operation, "failure")
		RecordKafkaConsumeDuration(time.Since(startTime).Seconds())
		return fmt.Errorf("failed to insert audit log: %w", err)
	}

	// Record successful consumption
	RecordKafkaConsumed(schema, operation, "success")
	RecordKafkaConsumeDuration(time.Since(startTime).Seconds())

	// Record event age (time from creation to processing)
	eventAge := time.Since(createdAt).Seconds()
	RecordEntryAge(eventAge)

	log.Printf("DEBUG: Processed audit event: table=%s operation=%s record=%s",
		event.TableName, operation, event.RecordId)

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

// ProcessOutboxFallback processes any pending entries in the audit_outbox table
// that were written as a fallback when Kafka publishing failed.
// This ensures no audit records are lost.
// Uses SELECT FOR UPDATE SKIP LOCKED to prevent race conditions when multiple workers run concurrently.
func (c *Consumer) ProcessOutboxFallback(ctx context.Context, schema string, batchSize int) (int, error) {
	if batchSize <= 0 {
		batchSize = 100
	}

	db := c.db
	if schema != "" {
		// Validate schema name to prevent SQL injection
		if !isValidSchemaName(schema) {
			return 0, fmt.Errorf("%w: %s", ErrInvalidSchemaName, schema)
		}
		db = db.Exec(fmt.Sprintf("SET LOCAL search_path TO %s", schema))
	}

	// Fetch and lock pending entries atomically using FOR UPDATE SKIP LOCKED
	// This prevents race conditions when multiple workers run concurrently
	var entries []AuditOutbox
	if err := db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
		Where("status = ?", "pending").
		Order("created_at ASC").
		Limit(batchSize).
		Find(&entries).Error; err != nil {
		return 0, fmt.Errorf("failed to fetch pending outbox entries: %w", err)
	}

	if len(entries) == 0 {
		return 0, nil
	}

	processed := 0
	for _, entry := range entries {
		// Mark as processing
		if err := db.WithContext(ctx).
			Model(&entry).
			Update("status", "processing").Error; err != nil {
			log.Printf("WARN: Failed to mark outbox entry as processing: %v", err)
			continue
		}

		// Create audit log entry
		auditLog := AuditLog{
			ID:            uuid.New(),
			Table:         entry.Table,
			Operation:     entry.Operation,
			RecordID:      entry.RecordID,
			OldValues:     entry.OldValues,
			NewValues:     entry.NewValues,
			CreatedAt:     entry.CreatedAt,
			ChangedBy:     entry.ChangedBy,
			TransactionID: entry.TransactionID,
			ClientIP:      entry.ClientIP,
			UserAgent:     entry.UserAgent,
		}

		// Insert into audit_log
		if err := db.WithContext(ctx).Create(&auditLog).Error; err != nil {
			// Mark as failed
			errMsg := err.Error()
			db.WithContext(ctx).Model(&entry).Updates(map[string]interface{}{
				"status":      "failed",
				"last_error":  errMsg,
				"retry_count": gorm.Expr("retry_count + 1"),
			})
			log.Printf("ERROR: Failed to insert audit log from outbox: %v", err)
			continue
		}

		// Mark as completed
		if err := db.WithContext(ctx).
			Model(&entry).
			Update("status", "completed").Error; err != nil {
			log.Printf("WARN: Failed to mark outbox entry as completed: %v", err)
		}

		processed++
	}

	return processed, nil
}
