package domain_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	auditv1 "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
	auditdomain "github.com/meridianhub/meridian/services/audit-worker/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// newEventRouterTransformer creates an AuditEventTransformer configured for event-router tests.
// Uses tenant-zero as both tenant and billing account (system tenant billing).
func newEventRouterTransformer() *auditdomain.AuditEventTransformer {
	tenantZeroID := uuid.MustParse("00000000-0000-0000-0000-000000000000")
	tenantTestID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	tenantAccountMap := map[uuid.UUID]uuid.UUID{
		tenantZeroID: tenantZeroID,
		tenantTestID: tenantTestID,
	}
	return auditdomain.NewAuditEventTransformer(tenantAccountMap)
}

// newAuditEventWithTenant constructs a valid AuditEvent for the given tenant ID.
func newAuditEventWithTenant(schemaName, tenantID string, op auditv1.AuditOperation) *auditv1.AuditEvent {
	return &auditv1.AuditEvent{
		EventId:       uuid.New().String(),
		SchemaName:    schemaName,
		TableName:     schemaName,
		Operation:     op,
		RecordId:      "rec-" + uuid.New().String()[:8],
		ChangedBy:     "test-user",
		CorrelationId: uuid.New().String(),
		Timestamp:     timestamppb.Now(),
		Metadata: map[string]string{
			"tenant_id": tenantID,
		},
	}
}

// TestAuditEventTransformer_Transform_KnownTenant verifies transformation for a registered tenant.
func TestAuditEventTransformer_Transform_KnownTenant(t *testing.T) {
	transformer := newEventRouterTransformer()

	event := newAuditEventWithTenant(
		"current_account",
		"00000000-0000-0000-0000-000000000000",
		auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
	)

	measurement, err := transformer.Transform(event)

	require.NoError(t, err)
	require.NotNil(t, measurement, "expected measurement for known tenant")
	assert.Equal(t, "MERIDIAN-CURRENT-ACCOUNT-OPS", measurement.AssetCode)
	assert.Equal(t, "AUDIT_STREAM", measurement.Source)
	assert.Equal(t, "current_account", measurement.Attributes["service"])
	assert.Equal(t, "INSERT", measurement.Attributes["operation"])
}

// TestAuditEventTransformer_Transform_UnknownTenant verifies that unknown tenants
// produce an error (not in the account mapping).
func TestAuditEventTransformer_Transform_UnknownTenant(t *testing.T) {
	transformer := newEventRouterTransformer()

	event := newAuditEventWithTenant(
		"current_account",
		"ffffffff-ffff-ffff-ffff-ffffffffffff", // not in the transformer map
		auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
	)

	measurement, err := transformer.Transform(event)

	// Unknown tenant returns an error — caller must handle filtering
	require.Error(t, err)
	assert.Nil(t, measurement)
	assert.Contains(t, err.Error(), "tenant not found")
}

// TestAuditEventTransformer_Transform_Metadata verifies timestamp and ID fields are populated.
func TestAuditEventTransformer_Transform_Metadata(t *testing.T) {
	transformer := newEventRouterTransformer()

	tenantID := "00000000-0000-0000-0000-000000000000"
	event := newAuditEventWithTenant("payment_order", tenantID, auditv1.AuditOperation_AUDIT_OPERATION_UPDATE)

	before := time.Now()
	measurement, err := transformer.Transform(event)
	after := time.Now()

	require.NoError(t, err)
	require.NotNil(t, measurement)

	// Measurement ID is a UUID
	assert.NotEqual(t, uuid.Nil, measurement.ID)

	// AccountID maps to tenant billing account
	expectedAccountID := uuid.MustParse(tenantID)
	assert.Equal(t, expectedAccountID, measurement.AccountID)

	// Period timestamps are within the test window
	assert.True(t, !measurement.Period.Start.Before(before.Add(-time.Second)))
	assert.True(t, !measurement.Period.Start.After(after.Add(time.Second)))
}

// TestAuditEventTransformer_Transform_EventTypes verifies INSERT, UPDATE, DELETE all produce measurements.
func TestAuditEventTransformer_Transform_EventTypes(t *testing.T) {
	transformer := newEventRouterTransformer()
	tenantID := "00000000-0000-0000-0000-000000000000"

	tests := []struct {
		name      string
		operation auditv1.AuditOperation
		wantOp    string
	}{
		{"insert", auditv1.AuditOperation_AUDIT_OPERATION_INSERT, "INSERT"},
		{"update", auditv1.AuditOperation_AUDIT_OPERATION_UPDATE, "UPDATE"},
		{"delete", auditv1.AuditOperation_AUDIT_OPERATION_DELETE, "DELETE"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := newAuditEventWithTenant("current_account", tenantID, tt.operation)

			measurement, err := transformer.Transform(event)

			require.NoError(t, err)
			require.NotNil(t, measurement)
			assert.Equal(t, tt.wantOp, measurement.Attributes["operation"])
		})
	}
}

// TestAuditEventTransformer_Transform_MultipleServices verifies different schema names
// produce correctly labelled measurements.
func TestAuditEventTransformer_Transform_MultipleServices(t *testing.T) {
	transformer := newEventRouterTransformer()
	tenantID := "00000000-0000-0000-0000-000000000000"

	services := []struct {
		schemaName      string
		wantAssetCode   string
	}{
		{"current_account", "MERIDIAN-CURRENT-ACCOUNT-OPS"},
		{"payment_order", "MERIDIAN-PAYMENT-ORDER-OPS"},
		{"financial_accounting", "MERIDIAN-FINANCIAL-ACCOUNTING-OPS"},
	}

	for _, svc := range services {
		t.Run(svc.schemaName, func(t *testing.T) {
			event := newAuditEventWithTenant(svc.schemaName, tenantID, auditv1.AuditOperation_AUDIT_OPERATION_INSERT)

			measurement, err := transformer.Transform(event)

			require.NoError(t, err)
			require.NotNil(t, measurement)
			assert.Equal(t, svc.wantAssetCode, measurement.AssetCode)
			assert.Equal(t, svc.schemaName, measurement.Attributes["service"])
		})
	}
}

// TestAuditEventTransformer_Transform_QualityScore verifies quality score is set on measurements.
func TestAuditEventTransformer_Transform_QualityScore(t *testing.T) {
	transformer := newEventRouterTransformer()
	tenantID := "00000000-0000-0000-0000-000000000000"

	event := newAuditEventWithTenant("current_account", tenantID, auditv1.AuditOperation_AUDIT_OPERATION_INSERT)

	measurement, err := transformer.Transform(event)

	require.NoError(t, err)
	require.NotNil(t, measurement)
	// Quality score should be set (non-zero for actual measurements)
	assert.Positive(t, measurement.QualityScore)
}

// TestAuditEventTransformer_Transform_SecondTenant verifies a second registered tenant
// also produces measurements.
func TestAuditEventTransformer_Transform_SecondTenant(t *testing.T) {
	transformer := newEventRouterTransformer()

	event := newAuditEventWithTenant(
		"current_account",
		"11111111-1111-1111-1111-111111111111",
		auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
	)

	measurement, err := transformer.Transform(event)

	require.NoError(t, err)
	require.NotNil(t, measurement)
	assert.Equal(t, uuid.MustParse("11111111-1111-1111-1111-111111111111"), measurement.AccountID)
}
