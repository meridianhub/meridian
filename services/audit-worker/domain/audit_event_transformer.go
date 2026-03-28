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
	if err := validateAuditEventFields(event); err != nil {
		return nil, err
	}

	tenantID, accountID, err := t.resolveAccount(event)
	if err != nil {
		return nil, err
	}
	_ = tenantID // used only for account resolution

	// Derive asset code from service name
	assetCode := t.deriveAssetCode(event.SchemaName)

	// Create instant Period from event timestamp
	timestamp := event.Timestamp.AsTime().UTC()
	period, err := Instant(timestamp)
	if err != nil {
		return nil, fmt.Errorf("failed to create period: %w", err)
	}

	// Create measurement
	measurement := &Measurement{
		ID:            uuid.New(),
		AccountID:     accountID,
		AssetCode:     assetCode,
		Quantity:      decimal.NewFromInt(1),
		Period:        period,
		Attributes:    t.buildAttributes(event),
		Source:        "AUDIT_STREAM",
		QualityScore:  t.defaultQualityScore,
		ReceivedAt:    time.Now().UTC(),
		SupersededBy:  nil,
		SettlementRun: "",  // Managed by settlement engine
		LockedAt:      nil, // Managed by settlement engine
	}

	return measurement, nil
}

// validateAuditEventFields checks that the event and its required fields are present.
func validateAuditEventFields(event *auditv1.AuditEvent) error {
	if event == nil {
		return fmt.Errorf("%w: event is nil", ErrInvalidAuditEvent)
	}
	if event.Timestamp == nil {
		return fmt.Errorf("%w: timestamp is required", ErrInvalidAuditEvent)
	}
	if event.SchemaName == "" {
		return fmt.Errorf("%w: schema_name is required", ErrInvalidAuditEvent)
	}
	return nil
}

// resolveAccount extracts the tenant ID from event metadata and maps it to a utilization account.
func (t *AuditEventTransformer) resolveAccount(event *auditv1.AuditEvent) (uuid.UUID, uuid.UUID, error) {
	tenantIDStr, ok := event.Metadata["tenant_id"]
	if !ok {
		return uuid.Nil, uuid.Nil, fmt.Errorf("%w: tenant_id not found in metadata", ErrInvalidAuditEvent)
	}

	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("%w: %w", ErrInvalidTenantID, err)
	}

	accountID, ok := t.tenantAccountMap[tenantID]
	if !ok {
		return uuid.Nil, uuid.Nil, fmt.Errorf("%w: %s", ErrTenantNotMapped, tenantID)
	}

	return tenantID, accountID, nil
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
