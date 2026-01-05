package shared

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	auditv1 "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// OperationInitialImport is the string identifier for initial import operations.
// This maps to AUDIT_OPERATION_INITIAL_IMPORT in the protobuf enum.
const OperationInitialImport = "INITIAL_IMPORT"

// Audit integration errors.
var (
	// ErrAuditPublishFailed is returned when publishing an audit event fails.
	ErrAuditPublishFailed = errors.New("failed to publish audit event")
)

// ImportAuditLogger provides audit logging for bulk import operations.
// It creates audit events for position imports with proper metadata
// and correlation tracking.
type ImportAuditLogger struct {
	publisher  *audit.Publisher
	schemaName string

	// Correlation tracking
	importID      string
	correlationID string

	// Statistics
	eventsPublished int
}

// ImportAuditLoggerConfig contains configuration for creating an ImportAuditLogger.
type ImportAuditLoggerConfig struct {
	// Publisher is the audit event publisher (optional - if nil, logging is a no-op).
	Publisher *audit.Publisher

	// SchemaName identifies the tenant schema (e.g., "org_acme_bank").
	SchemaName string

	// ImportID is a unique identifier for this import session.
	// If empty, a new UUID will be generated.
	ImportID string

	// CorrelationID links related audit events for distributed tracing.
	// If empty, the ImportID will be used.
	CorrelationID string
}

// NewImportAuditLogger creates a new audit logger for import operations.
// If the publisher is nil, all logging operations become no-ops.
func NewImportAuditLogger(config ImportAuditLoggerConfig) *ImportAuditLogger {
	importID := config.ImportID
	if importID == "" {
		importID = uuid.New().String()
	}

	correlationID := config.CorrelationID
	if correlationID == "" {
		correlationID = importID
	}

	return &ImportAuditLogger{
		publisher:     config.Publisher,
		schemaName:    config.SchemaName,
		importID:      importID,
		correlationID: correlationID,
	}
}

// LogBatchImport logs an audit event for a batch of imported positions.
// This creates a single audit event summarizing the batch, rather than
// one event per position, to avoid overwhelming the audit system during
// large imports.
//
// Parameters:
//   - ctx: Context for cancellation and deadline control
//   - batchNumber: The sequence number of this batch
//   - positionCount: Number of positions in this batch
//   - accountID: The account these positions belong to
//   - instrumentCode: The instrument type being imported
//   - changedBy: The user/system performing the import
//
// Returns nil if the publisher is nil or disabled.
func (l *ImportAuditLogger) LogBatchImport(
	ctx context.Context,
	batchNumber int,
	positionCount int,
	accountID string,
	instrumentCode string,
	changedBy string,
) error {
	if l.publisher == nil || !l.publisher.IsEnabled() {
		return nil
	}

	// Create batch summary as the "new_values"
	batchSummary := map[string]any{
		"batch_number":    batchNumber,
		"position_count":  positionCount,
		"account_id":      accountID,
		"instrument_code": instrumentCode,
		"import_id":       l.importID,
	}

	newValues, err := json.Marshal(batchSummary)
	if err != nil {
		return fmt.Errorf("failed to marshal batch summary: %w", err)
	}

	event := &auditv1.AuditEvent{
		EventId:       uuid.New().String(),
		TableName:     "position",
		Operation:     auditv1.AuditOperation_AUDIT_OPERATION_INITIAL_IMPORT,
		RecordId:      fmt.Sprintf("import:%s:batch:%d", l.importID, batchNumber),
		OldValues:     "", // No old values for import
		NewValues:     string(newValues),
		ChangedBy:     changedBy,
		SchemaName:    l.schemaName,
		CorrelationId: l.correlationID,
		Timestamp:     timestamppb.Now(),
		Metadata: map[string]string{
			"import_id":       l.importID,
			"batch_number":    fmt.Sprintf("%d", batchNumber),
			"position_count":  fmt.Sprintf("%d", positionCount),
			"account_id":      accountID,
			"instrument_code": instrumentCode,
		},
	}

	if err := l.publisher.Publish(ctx, event); err != nil {
		return errors.Join(ErrAuditPublishFailed, err)
	}

	l.eventsPublished++
	return nil
}

// LogImportComplete logs a summary audit event when the import is finished.
// This provides a single record showing the total import statistics.
func (l *ImportAuditLogger) LogImportComplete(
	ctx context.Context,
	totalPositions int,
	totalBatches int,
	duration time.Duration,
	changedBy string,
) error {
	if l.publisher == nil || !l.publisher.IsEnabled() {
		return nil
	}

	completeSummary := map[string]any{
		"import_id":        l.importID,
		"total_positions":  totalPositions,
		"total_batches":    totalBatches,
		"duration_seconds": duration.Seconds(),
		"status":           "completed",
	}

	newValues, err := json.Marshal(completeSummary)
	if err != nil {
		return fmt.Errorf("failed to marshal completion summary: %w", err)
	}

	event := &auditv1.AuditEvent{
		EventId:       uuid.New().String(),
		TableName:     "position",
		Operation:     auditv1.AuditOperation_AUDIT_OPERATION_INITIAL_IMPORT,
		RecordId:      fmt.Sprintf("import:%s:complete", l.importID),
		OldValues:     "",
		NewValues:     string(newValues),
		ChangedBy:     changedBy,
		SchemaName:    l.schemaName,
		CorrelationId: l.correlationID,
		Timestamp:     timestamppb.Now(),
		Metadata: map[string]string{
			"import_id":       l.importID,
			"status":          "completed",
			"total_positions": fmt.Sprintf("%d", totalPositions),
			"total_batches":   fmt.Sprintf("%d", totalBatches),
		},
	}

	if err := l.publisher.Publish(ctx, event); err != nil {
		return errors.Join(ErrAuditPublishFailed, err)
	}

	l.eventsPublished++
	return nil
}

// ImportID returns the unique identifier for this import session.
func (l *ImportAuditLogger) ImportID() string {
	return l.importID
}

// EventsPublished returns the number of audit events published so far.
func (l *ImportAuditLogger) EventsPublished() int {
	return l.eventsPublished
}

// NoOpAuditLogger returns an ImportAuditLogger that does nothing.
// This is useful for dry-run mode where no audit trail is needed.
func NoOpAuditLogger() *ImportAuditLogger {
	return &ImportAuditLogger{
		publisher:     nil,
		schemaName:    "",
		importID:      uuid.New().String(),
		correlationID: "",
	}
}
