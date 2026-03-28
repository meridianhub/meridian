// Package persistence provides database adapters for audit log persistence.
package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	auditv1 "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
	"github.com/meridianhub/meridian/services/audit-worker/domain"
	"github.com/meridianhub/meridian/shared/platform/db"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"gorm.io/gorm"
)

var (
	// ErrNilDatabase is returned when database connection is nil.
	ErrNilDatabase = errors.New("database connection cannot be nil")
	// ErrMissingTenantContext is returned when tenant ID is missing from context.
	ErrMissingTenantContext = errors.New("missing tenant context")
	// ErrInvalidOperation is returned when the operation is invalid.
	ErrInvalidOperation = errors.New("invalid operation")
)

// TenantAuditWriter writes audit events to tenant-scoped audit_log tables.
// It routes events to org_{tenant_id}.audit_log within the service's database,
// maintaining ADR-0002 bounded context isolation (single database per service).
//
// The writer uses PostgreSQL search_path pattern for tenant schema routing
// and maintains connection pooling per tenant schema for efficiency.
type TenantAuditWriter struct {
	db *gorm.DB
}

// NewTenantAuditWriter creates a new tenant audit writer.
// The database connection should be configured with appropriate connection pool settings.
//
// Returns ErrNilDatabase if db is nil.
func NewTenantAuditWriter(db *gorm.DB) (*TenantAuditWriter, error) {
	if db == nil {
		return nil, ErrNilDatabase
	}

	return &TenantAuditWriter{
		db: db,
	}, nil
}

// WriteAuditEvent writes an audit event to the tenant-scoped audit_log table.
// It extracts the tenant_id from the context (set by Kafka consumer from x-tenant-id header),
// sets the search_path to the tenant schema, and writes the audit log entry using an
// idempotent insert.
//
// The write is idempotent based on event_id - duplicate events are silently ignored
// using ON CONFLICT DO NOTHING to handle retry scenarios.
//
// Parameters:
//   - ctx: Context containing tenant ID (from Kafka message header)
//   - event: The AuditEvent protobuf message containing the audit data
//
// Returns an error if:
//   - context is already cancelled
//   - tenant_id is missing from context
//   - operation type is invalid
//   - database write fails (excluding duplicate key constraint violations)
func (w *TenantAuditWriter) WriteAuditEvent(ctx context.Context, event *auditv1.AuditEvent) error {
	// Check if context is already cancelled before expensive operations
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context cancelled before write: %w", err)
	}

	tenantID, operation, createdAt, err := validateAuditEvent(ctx, event)
	if err != nil {
		return err
	}

	// Build audit log entry map
	auditLog := buildAuditLogMap(event, operation, createdAt)

	// Write to audit_log table within a transaction with tenant schema scope
	err = db.WithGormTenantTransaction(ctx, w.db, func(tx *gorm.DB) error {
		return execIdempotentAuditInsert(tx, auditLog)
	})
	if err != nil {
		return fmt.Errorf("failed to write audit event for tenant %s: %w", tenantID, err)
	}

	return nil
}

// validateAuditEvent extracts and validates tenant ID and operation from context and event.
func validateAuditEvent(ctx context.Context, event *auditv1.AuditEvent) (tenant.TenantID, string, time.Time, error) {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return "", "", time.Time{}, ErrMissingTenantContext
	}

	operation := domain.ProtoToOperation(event.Operation)
	if operation == "" {
		return "", "", time.Time{}, fmt.Errorf("%w: %v", ErrInvalidOperation, event.Operation)
	}

	var createdAt time.Time
	if event.Timestamp != nil {
		createdAt = event.Timestamp.AsTime()
	} else {
		createdAt = time.Now()
	}

	return tenantID, operation, createdAt, nil
}

// execIdempotentAuditInsert performs an idempotent INSERT into audit_log using ON CONFLICT DO NOTHING.
func execIdempotentAuditInsert(tx *gorm.DB, auditLog map[string]interface{}) error {
	query := `
		INSERT INTO audit_log (
			event_id, table_name, operation, record_id, old_values, new_values,
			created_at, schema_name, changed_by, transaction_id, client_ip, user_agent,
			correlation_id, causation_id, idempotency_key
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (event_id) DO NOTHING
	`

	result := tx.Exec(query,
		auditLog["event_id"],
		auditLog["table_name"],
		auditLog["operation"],
		auditLog["record_id"],
		auditLog["old_values"],
		auditLog["new_values"],
		auditLog["created_at"],
		auditLog["schema_name"],
		auditLog["changed_by"],
		auditLog["transaction_id"],
		auditLog["client_ip"],
		auditLog["user_agent"],
		auditLog["correlation_id"],
		auditLog["causation_id"],
		auditLog["idempotency_key"],
	)

	if result.Error != nil {
		return fmt.Errorf("failed to insert audit log: %w", result.Error)
	}

	return nil
}

// buildAuditLogMap creates the audit log entry map from the event.
// Using map[string]interface{} for flexibility with optional fields.
func buildAuditLogMap(event *auditv1.AuditEvent, operation string, createdAt time.Time) map[string]interface{} {
	auditLog := map[string]interface{}{
		"event_id":   event.EventId,
		"table_name": event.TableName,
		"operation":  operation,
		"record_id":  event.RecordId,
		"created_at": createdAt,
	}

	// Add optional string fields only if non-empty
	addOptionalField := func(key, value string) {
		if value != "" {
			auditLog[key] = value
		}
	}

	addOptionalField("old_values", event.OldValues)
	addOptionalField("new_values", event.NewValues)
	addOptionalField("schema_name", event.SchemaName)
	addOptionalField("changed_by", event.ChangedBy)
	addOptionalField("transaction_id", event.TransactionId)
	addOptionalField("client_ip", event.ClientIp)
	addOptionalField("user_agent", event.UserAgent)
	addOptionalField("correlation_id", event.CorrelationId)
	addOptionalField("causation_id", event.CausationId)
	addOptionalField("idempotency_key", event.IdempotencyKey)

	return auditLog
}
