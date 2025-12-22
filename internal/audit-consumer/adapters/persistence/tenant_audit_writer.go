// Package persistence provides database adapters for audit log persistence.
package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	auditv1 "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
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
//   - tenant_id is missing from context
//   - operation type is invalid
//   - database write fails (excluding duplicate key constraint violations)
func (w *TenantAuditWriter) WriteAuditEvent(ctx context.Context, event *auditv1.AuditEvent) error {
	// Extract tenant ID from context (already injected by Kafka consumer from x-tenant-id header)
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return ErrMissingTenantContext
	}

	// Convert protobuf operation to string
	operation := protoToOperation(event.Operation)
	if operation == "" {
		return fmt.Errorf("%w: %v", ErrInvalidOperation, event.Operation)
	}

	// Handle potentially nil timestamp
	var createdAt time.Time
	if event.Timestamp != nil {
		createdAt = event.Timestamp.AsTime()
	} else {
		createdAt = time.Now()
	}

	// Build audit log entry map
	auditLog := buildAuditLogMap(event, operation, createdAt)

	// Note: tenant ID is already in context (from Kafka message header via ProtoConsumer)
	// No need to inject it again - it's used by db.WithGormTenantTransaction

	// Write to audit_log table within a transaction with tenant schema scope
	// Uses db.WithGormTenantTransaction to:
	// 1. Create a transaction
	// 2. Set search_path to org_{tenant_id}, public
	// 3. Execute the write
	// 4. Commit or rollback automatically
	err := db.WithGormTenantTransaction(ctx, w.db, func(tx *gorm.DB) error {
		// Idempotent insert using ON CONFLICT DO NOTHING
		// This handles retry scenarios where the same event_id is processed twice
		// Note: Using raw SQL with ON CONFLICT since GORM's Clauses API may not fully support it
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

		// If RowsAffected is 0, it means the event_id already exists (duplicate)
		// This is not an error - it's expected in retry scenarios
		if result.RowsAffected == 0 {
			// Silently ignore duplicates - this is idempotent behavior
			return nil
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to write audit event for tenant %s: %w", tenantID, err)
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
