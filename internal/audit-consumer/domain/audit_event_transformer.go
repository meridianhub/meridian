package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	auditv1 "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
	"github.com/shopspring/decimal"
)

var (
	// ErrInvalidAuditEvent indicates the audit event is missing required fields
	ErrInvalidAuditEvent = errors.New("invalid audit event")

	// ErrInvalidTenantID indicates the tenant_id cannot be parsed as UUID
	ErrInvalidTenantID = errors.New("invalid tenant_id: must be a valid UUID")

	// ErrTenantNotMapped indicates the tenant has no account mapping configured
	ErrTenantNotMapped = errors.New("tenant not found in account mapping")
)

// AuditEventTransformer converts AuditEvent proto messages into Measurement domain models
// following ADR-0017 temporal quality ladder structure for utilization metering.
type AuditEventTransformer struct {
	// tenantAccountMap maps tenant UUIDs to their utilization account IDs in tenant-zero
	// This is injected at construction time from configuration
	tenantAccountMap map[uuid.UUID]uuid.UUID

	// defaultQualityScore is used when Source Authority Registry is unavailable
	// AUDIT_STREAM is typically medium quality (50-70 range)
	defaultQualityScore int
}

// NewAuditEventTransformer creates a transformer with the given tenant-to-account mapping.
// The quality score defaults to 60 (medium quality) for AUDIT_STREAM source.
func NewAuditEventTransformer(tenantAccountMap map[uuid.UUID]uuid.UUID) *AuditEventTransformer {
	return &AuditEventTransformer{
		tenantAccountMap:    tenantAccountMap,
		defaultQualityScore: 60, // Medium quality for audit stream data
	}
}

// Transform converts an AuditEvent proto message into a Measurement domain model.
// Returns an error if the event is invalid or tenant mapping is missing.
func (t *AuditEventTransformer) Transform(event *auditv1.AuditEvent) (*Measurement, error) {
	if event == nil {
		return nil, fmt.Errorf("%w: event is nil", ErrInvalidAuditEvent)
	}

	// Validate required fields
	if event.Timestamp == nil {
		return nil, fmt.Errorf("%w: timestamp is required", ErrInvalidAuditEvent)
	}
	if event.SchemaName == "" {
		return nil, fmt.Errorf("%w: schema_name is required", ErrInvalidAuditEvent)
	}

	// Extract tenant_id from metadata
	tenantIDStr, ok := event.Metadata["tenant_id"]
	if !ok {
		return nil, fmt.Errorf("%w: tenant_id not found in metadata", ErrInvalidAuditEvent)
	}

	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidTenantID, err)
	}

	// Map tenant_id to utilization account_id
	accountID, ok := t.tenantAccountMap[tenantID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrTenantNotMapped, tenantID)
	}

	// Derive asset code from service name
	assetCode := t.deriveAssetCode(event.SchemaName)

	// Create instant Period from event timestamp
	timestamp := event.Timestamp.AsTime().UTC()
	period, err := Instant(timestamp)
	if err != nil {
		return nil, fmt.Errorf("failed to create period: %w", err)
	}

	// Set quantity to 1 for per-event counting
	quantity := decimal.NewFromInt(1)

	// Populate attributes for fungibility and reporting
	attributes := t.buildAttributes(event)

	// Create measurement
	measurement := &Measurement{
		ID:            uuid.New(),
		AccountID:     accountID,
		AssetCode:     assetCode,
		Quantity:      quantity,
		Period:        period,
		Attributes:    attributes,
		Source:        "AUDIT_STREAM",
		QualityScore:  t.defaultQualityScore,
		ReceivedAt:    time.Now().UTC(),
		SupersededBy:  nil,
		SettlementRun: "",  // Managed by settlement engine
		LockedAt:      nil, // Managed by settlement engine
	}

	return measurement, nil
}

// deriveAssetCode creates a service-specific asset code from the schema name.
// Format: "MERIDIAN-{SERVICE}-OPS" (e.g., "MERIDIAN-CURRENT-ACCOUNT-OPS")
func (t *AuditEventTransformer) deriveAssetCode(schemaName string) string {
	// Convert schema_name (e.g., "current_account") to uppercase with hyphens
	// Replace underscores with hyphens
	serviceName := strings.ToUpper(strings.ReplaceAll(schemaName, "_", "-"))
	return fmt.Sprintf("MERIDIAN-%s-OPS", serviceName)
}

// buildAttributes creates the attributes map for the measurement.
// Attributes include: service, operation, table for position aggregation.
func (t *AuditEventTransformer) buildAttributes(event *auditv1.AuditEvent) map[string]string {
	attributes := make(map[string]string)

	// Service name from schema
	attributes["service"] = event.SchemaName

	// Operation type
	operation := ProtoToOperation(event.Operation)
	if operation != "" {
		attributes["operation"] = operation
	}

	// Table name
	if event.TableName != "" {
		attributes["table"] = event.TableName
	}

	return attributes
}

// SetQualityScore allows overriding the default quality score.
// This would be called after consulting the Source Authority Registry.
func (t *AuditEventTransformer) SetQualityScore(score int) {
	t.defaultQualityScore = score
}
