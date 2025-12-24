package domain

import (
	"testing"
	"time"

	auditv1 "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestNewAuditEventTransformer(t *testing.T) {
	transformer := NewAuditEventTransformer()
	assert.NotNil(t, transformer)
}

func TestTransform_NilEvent(t *testing.T) {
	transformer := NewAuditEventTransformer()

	measurement, err := transformer.Transform(nil)

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidAuditEvent)
	assert.Nil(t, measurement)
}

func TestTransform_ValidEvent_WithTenantInMetadata(t *testing.T) {
	transformer := NewAuditEventTransformer()

	event := &auditv1.AuditEvent{
		EventId:       "evt-001",
		SchemaName:    "current_account",
		TableName:     "current_account",
		Operation:     auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
		RecordId:      "rec-123",
		CorrelationId: "corr-456",
		Timestamp:     timestamppb.Now(),
		Metadata: map[string]string{
			"tenant_id": "tenant-abc",
		},
	}

	measurement, err := transformer.Transform(event)

	require.NoError(t, err)
	require.NotNil(t, measurement)
	assert.Equal(t, "tenant-abc", measurement.TenantID)
	assert.Equal(t, "current_account", measurement.ServiceName)
	assert.Equal(t, "AUDIT_OPERATION_INSERT", measurement.OperationType)
	assert.Equal(t, int64(1), measurement.Quantity)
	assert.Equal(t, "operation", measurement.UnitOfMeasure)
	assert.Equal(t, "corr-456", measurement.CorrelationID)
	assert.WithinDuration(t, time.Now(), measurement.Timestamp, 2*time.Second)
}

func TestTransform_ValidEvent_WithoutTenantInMetadata(t *testing.T) {
	transformer := NewAuditEventTransformer()

	event := &auditv1.AuditEvent{
		EventId:       "evt-002",
		SchemaName:    "financial_accounting",
		TableName:     "ledger_posting",
		Operation:     auditv1.AuditOperation_AUDIT_OPERATION_UPDATE,
		RecordId:      "rec-456",
		CorrelationId: "corr-789",
		Timestamp:     timestamppb.Now(),
		Metadata:      nil, // No metadata
	}

	measurement, err := transformer.Transform(event)

	require.NoError(t, err)
	require.NotNil(t, measurement)
	assert.Equal(t, "unknown", measurement.TenantID)
	assert.Equal(t, "financial_accounting", measurement.ServiceName)
	assert.Equal(t, "AUDIT_OPERATION_UPDATE", measurement.OperationType)
}

func TestTransform_ValidEvent_EmptyMetadata(t *testing.T) {
	transformer := NewAuditEventTransformer()

	event := &auditv1.AuditEvent{
		EventId:       "evt-003",
		SchemaName:    "position_keeping",
		TableName:     "financial_position_log",
		Operation:     auditv1.AuditOperation_AUDIT_OPERATION_DELETE,
		RecordId:      "rec-789",
		CorrelationId: "corr-012",
		Timestamp:     timestamppb.Now(),
		Metadata:      map[string]string{}, // Empty metadata
	}

	measurement, err := transformer.Transform(event)

	require.NoError(t, err)
	require.NotNil(t, measurement)
	assert.Equal(t, "unknown", measurement.TenantID)
}

// TestTransform_AllServiceTypes tests transformation for all 6 service types
func TestTransform_AllServiceTypes(t *testing.T) {
	tests := []struct {
		name       string
		schemaName string
		tableName  string
	}{
		{
			name:       "current-account service",
			schemaName: "current_account",
			tableName:  "current_account",
		},
		{
			name:       "financial-accounting service",
			schemaName: "financial_accounting",
			tableName:  "ledger_posting",
		},
		{
			name:       "position-keeping service",
			schemaName: "position_keeping",
			tableName:  "financial_position_log",
		},
		{
			name:       "party service",
			schemaName: "party",
			tableName:  "party",
		},
		{
			name:       "payment-order service",
			schemaName: "payment_order",
			tableName:  "payment_order",
		},
		{
			name:       "tenant service",
			schemaName: "tenant",
			tableName:  "tenant",
		},
	}

	transformer := NewAuditEventTransformer()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := &auditv1.AuditEvent{
				EventId:       "evt-" + tt.schemaName,
				SchemaName:    tt.schemaName,
				TableName:     tt.tableName,
				Operation:     auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
				RecordId:      "rec-123",
				CorrelationId: "corr-456",
				Timestamp:     timestamppb.Now(),
				Metadata: map[string]string{
					"tenant_id": "tenant-test",
				},
			}

			measurement, err := transformer.Transform(event)

			require.NoError(t, err)
			require.NotNil(t, measurement)
			assert.Equal(t, tt.schemaName, measurement.ServiceName)
			assert.Equal(t, "tenant-test", measurement.TenantID)
		})
	}
}

// TestTransform_AllOperationTypes tests transformation for different operation types
func TestTransform_AllOperationTypes(t *testing.T) {
	tests := []struct {
		name      string
		operation auditv1.AuditOperation
		expected  string
	}{
		{
			name:      "INSERT operation",
			operation: auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
			expected:  "AUDIT_OPERATION_INSERT",
		},
		{
			name:      "UPDATE operation",
			operation: auditv1.AuditOperation_AUDIT_OPERATION_UPDATE,
			expected:  "AUDIT_OPERATION_UPDATE",
		},
		{
			name:      "DELETE operation",
			operation: auditv1.AuditOperation_AUDIT_OPERATION_DELETE,
			expected:  "AUDIT_OPERATION_DELETE",
		},
		{
			name:      "UNSPECIFIED operation",
			operation: auditv1.AuditOperation_AUDIT_OPERATION_UNSPECIFIED,
			expected:  "AUDIT_OPERATION_UNSPECIFIED",
		},
	}

	transformer := NewAuditEventTransformer()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := &auditv1.AuditEvent{
				EventId:       "evt-001",
				SchemaName:    "current_account",
				TableName:     "current_account",
				Operation:     tt.operation,
				RecordId:      "rec-123",
				CorrelationId: "corr-456",
				Timestamp:     timestamppb.Now(),
				Metadata: map[string]string{
					"tenant_id": "tenant-test",
				},
			}

			measurement, err := transformer.Transform(event)

			require.NoError(t, err)
			require.NotNil(t, measurement)
			assert.Equal(t, tt.expected, measurement.OperationType)
		})
	}
}

// TestTransform_PreservesCorrelationID tests that correlation IDs are preserved
func TestTransform_PreservesCorrelationID(t *testing.T) {
	tests := []struct {
		name          string
		correlationID string
	}{
		{
			name:          "standard UUID correlation ID",
			correlationID: "550e8400-e29b-41d4-a716-446655440000",
		},
		{
			name:          "custom correlation ID",
			correlationID: "payment-batch-2024-001",
		},
		{
			name:          "empty correlation ID",
			correlationID: "",
		},
	}

	transformer := NewAuditEventTransformer()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := &auditv1.AuditEvent{
				EventId:       "evt-001",
				SchemaName:    "current_account",
				TableName:     "current_account",
				Operation:     auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
				RecordId:      "rec-123",
				CorrelationId: tt.correlationID,
				Timestamp:     timestamppb.Now(),
				Metadata: map[string]string{
					"tenant_id": "tenant-test",
				},
			}

			measurement, err := transformer.Transform(event)

			require.NoError(t, err)
			require.NotNil(t, measurement)
			assert.Equal(t, tt.correlationID, measurement.CorrelationID)
		})
	}
}

// TestTransform_MetadataVariations tests different metadata scenarios
func TestTransform_MetadataVariations(t *testing.T) {
	tests := []struct {
		name           string
		metadata       map[string]string
		expectedTenant string
	}{
		{
			name: "tenant_id in metadata",
			metadata: map[string]string{
				"tenant_id": "tenant-123",
			},
			expectedTenant: "tenant-123",
		},
		{
			name: "tenant_id with other metadata",
			metadata: map[string]string{
				"tenant_id":      "tenant-456",
				"user_id":        "user-789",
				"request_source": "api",
			},
			expectedTenant: "tenant-456",
		},
		{
			name: "metadata without tenant_id",
			metadata: map[string]string{
				"user_id": "user-789",
			},
			expectedTenant: "unknown",
		},
		{
			name:           "nil metadata",
			metadata:       nil,
			expectedTenant: "unknown",
		},
		{
			name:           "empty metadata",
			metadata:       map[string]string{},
			expectedTenant: "unknown",
		},
	}

	transformer := NewAuditEventTransformer()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := &auditv1.AuditEvent{
				EventId:       "evt-001",
				SchemaName:    "current_account",
				TableName:     "current_account",
				Operation:     auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
				RecordId:      "rec-123",
				CorrelationId: "corr-456",
				Timestamp:     timestamppb.Now(),
				Metadata:      tt.metadata,
			}

			measurement, err := transformer.Transform(event)

			require.NoError(t, err)
			require.NotNil(t, measurement)
			assert.Equal(t, tt.expectedTenant, measurement.TenantID)
		})
	}
}
