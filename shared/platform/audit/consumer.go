package audit

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	auditv1 "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
	"github.com/meridianhub/meridian/shared/platform/defaults"
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

// quoteIdentifier safely quotes a PostgreSQL identifier using double quotes.
// This provides defense-in-depth alongside schema validation.
func quoteIdentifier(name string) string {
	// PostgreSQL identifiers: escape double quotes by doubling them
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
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
	// Topic is the Kafka topic for audit events (default: "audit.events.v1").
	Topic string
	// DLQTopicSuffix is appended to topic name for DLQ (default: ".dlq" -> "audit.events.v1.dlq").
	DLQTopicSuffix string
	// DB is the GORM database connection for writing to audit_log.
	DB *gorm.DB
	// HandlerTimeout is the maximum duration for processing a single message.
	HandlerTimeout time.Duration
	// MaxRetries is the maximum number of retry attempts before sending to DLQ.
	MaxRetries int
}

// applyDefaults fills zero-valued fields with sensible defaults.
func (c *ConsumerConfig) applyDefaults() {
	if c.GroupID == "" {
		c.GroupID = kafka.AuditConsumerGroup
	}
	if c.Topic == "" {
		c.Topic = kafka.AuditEventsTopic
	}
	if c.DLQTopicSuffix == "" {
		c.DLQTopicSuffix = ".dlq" // Results in "audit.events.v1.dlq"
	}
	if c.HandlerTimeout == 0 {
		c.HandlerTimeout = defaults.DefaultRPCTimeout
	}
	if c.MaxRetries == 0 {
		c.MaxRetries = 3
	}
}

// NewConsumer creates a new audit Consumer.
func NewConsumer(config ConsumerConfig) (*Consumer, error) {
	if config.BootstrapServers == "" {
		return nil, ErrEmptyBootstrapServers
	}
	if config.DB == nil {
		return nil, ErrNilDatabase
	}

	config.applyDefaults()

	ctx, cancel := context.WithCancel(context.Background())

	c := &Consumer{
		db:     config.DB,
		ctx:    ctx,
		cancel: cancel,
	}

	dlqProducer, err := buildDLQProducer(config)
	if err != nil {
		cancel()
		return nil, err
	}
	c.dlqProducer = dlqProducer

	consumer, err := buildKafkaConsumer(config, dlqProducer, c.handleMessage)
	if err != nil {
		dlqProducer.Close()
		cancel()
		return nil, err
	}
	c.consumer = consumer

	return c, nil
}

// buildDLQProducer creates a DLQ producer for the audit consumer.
func buildDLQProducer(config ConsumerConfig) (*kafka.DLQProducer, error) {
	producer, err := kafka.NewProtoProducer(kafka.ProducerConfig{
		BootstrapServers: config.BootstrapServers,
		ClientID:         config.ClientID + "-dlq",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create DLQ producer: %w", err)
	}

	dlqConfig := kafka.DLQConfig{
		DLQTopicSuffix:    config.DLQTopicSuffix,
		MaxRetries:        int32(config.MaxRetries),
		RetryBackoffMs:    1000,
		BackoffMultiplier: 2.0,
		ConsumerGroupID:   config.GroupID,
	}
	dlqProducer, err := kafka.NewDLQProducer(producer, dlqConfig)
	if err != nil {
		producer.Close()
		return nil, fmt.Errorf("failed to create DLQ producer: %w", err)
	}

	return dlqProducer, nil
}

// buildKafkaConsumer creates the underlying Kafka consumer with DLQ support.
func buildKafkaConsumer(config ConsumerConfig, dlqProducer *kafka.DLQProducer, handler func(context.Context, []byte, proto.Message) error) (*kafka.ProtoConsumer, error) {
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
				DLQTopicSuffix:    config.DLQTopicSuffix,
				MaxRetries:        int32(config.MaxRetries),
				RetryBackoffMs:    1000,
				BackoffMultiplier: 2.0,
			},
		},
		func() proto.Message { return &auditv1.AuditEvent{} },
		handler,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kafka consumer: %w", err)
	}
	return consumer, nil
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
			RecordKafkaFallback("unknown", "subscription_error")
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

	operation := protoToOperation(event.Operation)
	if operation == "" {
		return fmt.Errorf("%w: %v", ErrInvalidOperation, event.Operation)
	}

	schema := event.SchemaName
	if schema == "" {
		schema = "unknown"
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	if event.SchemaName != "" && !isValidSchemaName(event.SchemaName) {
		RecordKafkaConsumed(schema, operation, "failure")
		return fmt.Errorf("%w: %s", ErrInvalidSchemaName, event.SchemaName)
	}

	auditLog := buildAuditLogFromEvent(event, operation)

	err := c.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if event.SchemaName != "" {
			if err := tx.Exec(fmt.Sprintf("SET LOCAL search_path TO %s", quoteIdentifier(event.SchemaName))).Error; err != nil {
				return fmt.Errorf("failed to set search_path: %w", err)
			}
		}
		return tx.Create(&auditLog).Error
	})
	if err != nil {
		RecordKafkaConsumed(schema, operation, "failure")
		RecordKafkaConsumeDuration(time.Since(startTime).Seconds())
		return fmt.Errorf("failed to insert audit log: %w", err)
	}

	RecordKafkaConsumed(schema, operation, "success")
	RecordKafkaConsumeDuration(time.Since(startTime).Seconds())
	RecordEntryAge(time.Since(auditLog.CreatedAt).Seconds())

	log.Printf("DEBUG: Processed audit event: table=%s operation=%s record=%s",
		event.TableName, operation, event.RecordId)

	return nil
}

// buildAuditLogFromEvent converts a protobuf AuditEvent into an AuditLog database record.
func buildAuditLogFromEvent(event *auditv1.AuditEvent, operation string) AuditLog {
	var createdAt time.Time
	if event.Timestamp != nil {
		createdAt = event.Timestamp.AsTime()
	} else {
		createdAt = time.Now()
	}

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

	return auditLog
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
	case auditv1.AuditOperation_AUDIT_OPERATION_INITIAL_IMPORT:
		return "INITIAL_IMPORT"
	case auditv1.AuditOperation_AUDIT_OPERATION_UNSPECIFIED:
		return ""
	}
	return ""
}

// processOutboxEntry processes a single outbox entry within a transaction.
// Returns nil on success, gorm.ErrRecordNotFound if entry was already processed,
// or other error on failure.
func processOutboxEntry(ctx context.Context, tx *gorm.DB, entryID uuid.UUID) error {
	// Check for context cancellation
	if err := ctx.Err(); err != nil {
		return err
	}

	// Lock the specific entry to ensure it's still pending
	var lockedEntry AuditOutbox
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
		Where("id = ? AND status = ?", entryID, "pending").
		First(&lockedEntry).Error; err != nil {
		return err
	}

	// Mark as processing
	if err := tx.WithContext(ctx).Model(&lockedEntry).Update("status", "processing").Error; err != nil {
		return err
	}

	// Create audit log entry
	auditLog := AuditLog{
		ID:            uuid.New(),
		Table:         lockedEntry.Table,
		Operation:     lockedEntry.Operation,
		RecordID:      lockedEntry.RecordID,
		OldValues:     lockedEntry.OldValues,
		NewValues:     lockedEntry.NewValues,
		CreatedAt:     lockedEntry.CreatedAt,
		ChangedBy:     lockedEntry.ChangedBy,
		TransactionID: lockedEntry.TransactionID,
		ClientIP:      lockedEntry.ClientIP,
		UserAgent:     lockedEntry.UserAgent,
	}

	// Insert into audit_log
	if err := tx.WithContext(ctx).Create(&auditLog).Error; err != nil {
		return err
	}

	// Mark as completed
	return tx.WithContext(ctx).Model(&lockedEntry).Update("status", "completed").Error
}

// ProcessOutboxFallback processes any pending entries in the audit_outbox table
// that were written as a fallback when Kafka publishing failed.
// This ensures no audit records are lost.
// Each entry is processed in its own transaction with FOR UPDATE locking to prevent
// race conditions when multiple workers run concurrently.
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
		// Set search_path for session (not LOCAL since we need it across multiple operations)
		// This affects the session until changed or connection returned to pool
		db = db.Exec(fmt.Sprintf("SET search_path TO %s", quoteIdentifier(schema)))
		if db.Error != nil {
			return 0, fmt.Errorf("failed to set search_path: %w", db.Error)
		}
	}

	// Fetch pending entry IDs without locking (just for iteration)
	var entryIDs []uuid.UUID
	if err := db.WithContext(ctx).
		Model(&AuditOutbox{}).
		Where("status = ?", "pending").
		Order("created_at ASC").
		Limit(batchSize).
		Pluck("id", &entryIDs).Error; err != nil {
		return 0, fmt.Errorf("failed to fetch pending outbox entries: %w", err)
	}

	if len(entryIDs) == 0 {
		return 0, nil
	}

	processed := 0
	for _, entryID := range entryIDs {
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return processOutboxEntry(ctx, tx, entryID)
		})
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue // Entry was already processed
			}
			// Mark as failed outside transaction
			errMsg := err.Error()
			if updateErr := db.WithContext(ctx).Model(&AuditOutbox{}).
				Where("id = ?", entryID).
				Updates(map[string]interface{}{
					"status":      "failed",
					"last_error":  errMsg,
					"retry_count": gorm.Expr("retry_count + 1"),
				}).Error; updateErr != nil {
				log.Printf("ERROR: Failed to mark outbox entry as failed: %v", updateErr)
			}
			log.Printf("ERROR: Failed to process outbox entry: %v", err)
			continue
		}
		processed++
	}

	return processed, nil
}
