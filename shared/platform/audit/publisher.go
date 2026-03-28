package audit

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	auditv1 "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
	"github.com/meridianhub/meridian/shared/platform/kafka"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// Publisher errors are defined in errors.go for centralized error management.
// See: ErrPublisherDisabled, ErrEventsNotDelivered, ErrEmptyRecordID

// Publisher handles publishing audit events to Kafka.
// It provides a primary path via Kafka and a fallback path via the audit_outbox table.
type Publisher struct {
	producer   *kafka.ProtoProducer
	topic      string
	schemaName string
	mu         sync.RWMutex
	enabled    bool
}

// PublisherConfig contains configuration for creating an audit Publisher.
type PublisherConfig struct {
	// BootstrapServers is the Kafka broker addresses (e.g., "kafka:9092").
	BootstrapServers string
	// Topic is the Kafka topic for audit events (default: "audit.events.v1").
	Topic string
	// SchemaName identifies the service schema (e.g., "party", "current_account").
	SchemaName string
	// ClientID identifies the producer for logging and metrics.
	ClientID string
}

// NewPublisher creates a new audit Publisher.
// Returns nil and ErrPublisherDisabled if BootstrapServers is empty.
func NewPublisher(config PublisherConfig) (*Publisher, error) {
	if config.BootstrapServers == "" {
		return nil, ErrPublisherDisabled
	}

	if config.Topic == "" {
		config.Topic = kafka.AuditEventsTopic
	}

	producer, err := kafka.NewProtoProducer(kafka.ProducerConfig{
		BootstrapServers: config.BootstrapServers,
		ClientID:         config.ClientID,
		Acks:             "all",
		Retries:          3,
		Compression:      "snappy",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create audit producer: %w", err)
	}

	return &Publisher{
		producer:   producer,
		topic:      config.Topic,
		schemaName: config.SchemaName,
		enabled:    true,
	}, nil
}

// Enable enables Kafka publishing.
func (p *Publisher) Enable() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.enabled = true
}

// Disable disables Kafka publishing (use outbox fallback only).
func (p *Publisher) Disable() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.enabled = false
}

// IsEnabled returns whether Kafka publishing is enabled.
func (p *Publisher) IsEnabled() bool {
	if p == nil {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.enabled
}

// Publish sends an audit event to Kafka.
// Returns nil if Kafka publishing is disabled or if the publisher is nil.
// Returns ErrEmptyRecordID if the event has no record ID for partitioning.
func (p *Publisher) Publish(ctx context.Context, event *auditv1.AuditEvent) error {
	if p == nil || !p.IsEnabled() {
		return nil
	}

	// Validate RecordId to ensure proper partitioning
	if event.RecordId == "" {
		return ErrEmptyRecordID
	}

	// Use record_id as the partition key for locality
	return p.producer.Publish(ctx, p.topic, event.RecordId, event)
}

// Close shuts down the publisher, flushing pending messages.
func (p *Publisher) Close() error {
	if p == nil || p.producer == nil {
		return nil
	}

	// Flush outstanding messages (wait up to 5 seconds)
	remaining := p.producer.FlushWithTimeout(5000)
	p.producer.Close()

	if remaining > 0 {
		return fmt.Errorf("%w: %d pending", ErrEventsNotDelivered, remaining)
	}
	return nil
}

// CreateAuditEvent creates an AuditEvent protobuf message from audit record parameters.
func CreateAuditEvent(
	ctx context.Context,
	tableName string,
	operation string,
	recordID string,
	oldValues string,
	newValues string,
	changedBy string,
	schemaName string,
) *auditv1.AuditEvent {
	event := &auditv1.AuditEvent{
		EventId:    uuid.New().String(),
		TableName:  tableName,
		Operation:  operationToProto(operation),
		RecordId:   recordID,
		OldValues:  oldValues,
		NewValues:  newValues,
		ChangedBy:  changedBy,
		SchemaName: schemaName,
		Timestamp:  timestamppb.Now(),
	}

	// Extract transaction ID if available from context
	if ctx != nil {
		if txID := getTransactionIDFromContext(ctx); txID != "" {
			event.TransactionId = txID
		}
		if corrID := getCorrelationIDFromContext(ctx); corrID != "" {
			event.CorrelationId = corrID
		}
	}

	return event
}

// operationToProto converts a string operation to the protobuf enum.
func operationToProto(op string) auditv1.AuditOperation {
	switch op {
	case OperationInsert:
		return auditv1.AuditOperation_AUDIT_OPERATION_INSERT
	case OperationUpdate:
		return auditv1.AuditOperation_AUDIT_OPERATION_UPDATE
	case OperationDelete:
		return auditv1.AuditOperation_AUDIT_OPERATION_DELETE
	default:
		return auditv1.AuditOperation_AUDIT_OPERATION_UNSPECIFIED
	}
}

// Context key types for additional audit context.
type (
	transactionIDKey struct{}
	correlationIDKey struct{}
)

// WithTransactionID adds a transaction ID to the context for audit correlation.
func WithTransactionID(ctx context.Context, txID string) context.Context {
	return context.WithValue(ctx, transactionIDKey{}, txID)
}

// WithCorrelationID adds a correlation ID to the context for distributed tracing.
func WithCorrelationID(ctx context.Context, corrID string) context.Context {
	return context.WithValue(ctx, correlationIDKey{}, corrID)
}

func getTransactionIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v := ctx.Value(transactionIDKey{}); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getCorrelationIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v := ctx.Value(correlationIDKey{}); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// Global publisher instance for hook integration.
var (
	globalPublisher *Publisher
	globalMu        sync.RWMutex
)

// SetGlobalPublisher sets the global audit publisher for hook integration.
// This should be called during service initialization.
func SetGlobalPublisher(p *Publisher) {
	globalMu.Lock()
	defer globalMu.Unlock()
	globalPublisher = p
}

// GetGlobalPublisher returns the global audit publisher.
func GetGlobalPublisher() *Publisher {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return globalPublisher
}

// publishToKafkaWithFallback attempts to publish to Kafka, falling back to outbox on failure.
// This is the core integration point for GORM hooks.
func publishToKafkaWithFallback(
	tx *gorm.DB,
	tableName string,
	operation string,
	recordID string,
	oldJSON string,
	newJSON string,
	changedBy string,
	schemaName string,
) error {
	publisher := GetGlobalPublisher()

	if publisher != nil && publisher.IsEnabled() {
		if err := tryKafkaPublish(tx, publisher, tableName, operation, recordID, oldJSON, newJSON, changedBy, schemaName); err == nil {
			return nil
		}
	} else if publisher == nil {
		RecordKafkaFallback(schemaName, "not_configured")
	} else {
		RecordKafkaFallback(schemaName, "disabled")
	}

	return writeOutboxFallback(tx, tableName, operation, recordID, oldJSON, newJSON, changedBy)
}

// tryKafkaPublish attempts to publish an audit event to Kafka. Returns nil on success.
func tryKafkaPublish(tx *gorm.DB, publisher *Publisher, tableName, operation, recordID, oldJSON, newJSON, changedBy, schemaName string) error {
	var ctx context.Context
	if tx.Statement != nil && tx.Statement.Context != nil {
		ctx = tx.Statement.Context
	} else {
		ctx = context.Background()
	}

	event := CreateAuditEvent(ctx, tableName, operation, recordID, oldJSON, newJSON, changedBy, schemaName)

	pubCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	startTime := time.Now()
	err := publisher.Publish(pubCtx, event)
	RecordKafkaPublishDuration(time.Since(startTime).Seconds())

	if err == nil {
		RecordKafkaPublished(schemaName, operation, "success")
		return nil
	}

	log.Printf("WARN: Kafka audit publish failed, using outbox fallback: %v", err)
	RecordKafkaPublished(schemaName, operation, "failure")
	RecordKafkaFallback(schemaName, "publish_error")
	return err
}

// writeOutboxFallback writes an audit event to the outbox table as a fallback.
func writeOutboxFallback(tx *gorm.DB, tableName, operation, recordID, oldJSON, newJSON, changedBy string) error {
	outbox := AuditOutbox{
		ID:        uuid.New(),
		Table:     tableName,
		Operation: operation,
		RecordID:  recordID,
		OldValues: oldJSON,
		NewValues: newJSON,
		Status:    StatusPending,
		CreatedAt: time.Now(),
	}

	if changedBy != "" {
		outbox.ChangedBy = &changedBy
	}

	return tx.Create(&outbox).Error
}
