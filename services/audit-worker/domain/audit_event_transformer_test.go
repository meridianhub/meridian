package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
	auditv1 "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestNewAuditEventTransformer(t *testing.T) {
	tenantID := uuid.New()
	accountID := uuid.New()

	mapping := map[uuid.UUID]uuid.UUID{
		tenantID: accountID,
	}

	transformer := NewAuditEventTransformer(mapping)

	assert.NotNil(t, transformer)
	assert.Equal(t, 60, transformer.defaultQualityScore)
	assert.Equal(t, mapping, transformer.tenantAccountMap)
}

func TestAuditEventTransformer_Transform(t *testing.T) {
	tenantID := uuid.New()
	accountID := uuid.New()

	mapping := map[uuid.UUID]uuid.UUID{
		tenantID: accountID,
	}

	transformer := NewAuditEventTransformer(mapping)

	tests := []struct {
		name     string
		event    *auditv1.AuditEvent
		wantErr  error
		validate func(t *testing.T, m *Measurement)
	}{
		{
			name: "valid audit event transforms successfully",
			event: &auditv1.AuditEvent{
				EventId:    uuid.New().String(),
				TableName:  "accounts",
				Operation:  auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
				RecordId:   uuid.New().String(),
				SchemaName: "current_account",
				Timestamp:  timestamppb.New(time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)),
				Metadata: map[string]string{
					"tenant_id": tenantID.String(),
				},
			},
			wantErr: nil,
			validate: func(t *testing.T, m *Measurement) {
				assert.Equal(t, accountID, m.AccountID)
				assert.Equal(t, "MERIDIAN-CURRENT-ACCOUNT-OPS", m.AssetCode)
				assert.True(t, m.Quantity.Equal(decimal.NewFromInt(1)))
				assert.True(t, m.Period.IsInstant())
				assert.Equal(t, "AUDIT_STREAM", m.Source)
				assert.Equal(t, 60, m.QualityScore)
				assert.True(t, m.IsCurrent())
				assert.False(t, m.IsLocked())

				// Validate attributes
				assert.Equal(t, "current_account", m.Attributes["service"])
				assert.Equal(t, "INSERT", m.Attributes["operation"])
				assert.Equal(t, "accounts", m.Attributes["table"])
			},
		},
		{
			name:    "nil event returns error",
			event:   nil,
			wantErr: ErrInvalidAuditEvent,
		},
		{
			name: "missing timestamp returns error",
			event: &auditv1.AuditEvent{
				EventId:    uuid.New().String(),
				TableName:  "accounts",
				Operation:  auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
				SchemaName: "current_account",
				Timestamp:  nil,
				Metadata: map[string]string{
					"tenant_id": tenantID.String(),
				},
			},
			wantErr: ErrInvalidAuditEvent,
		},
		{
			name: "missing schema_name returns error",
			event: &auditv1.AuditEvent{
				EventId:    uuid.New().String(),
				TableName:  "accounts",
				Operation:  auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
				SchemaName: "",
				Timestamp:  timestamppb.New(time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)),
				Metadata: map[string]string{
					"tenant_id": tenantID.String(),
				},
			},
			wantErr: ErrInvalidAuditEvent,
		},
		{
			name: "missing tenant_id in metadata returns error",
			event: &auditv1.AuditEvent{
				EventId:    uuid.New().String(),
				TableName:  "accounts",
				Operation:  auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
				SchemaName: "current_account",
				Timestamp:  timestamppb.New(time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)),
				Metadata:   map[string]string{},
			},
			wantErr: ErrInvalidAuditEvent,
		},
		{
			name: "invalid tenant_id UUID returns error",
			event: &auditv1.AuditEvent{
				EventId:    uuid.New().String(),
				TableName:  "accounts",
				Operation:  auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
				SchemaName: "current_account",
				Timestamp:  timestamppb.New(time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)),
				Metadata: map[string]string{
					"tenant_id": "not-a-uuid",
				},
			},
			wantErr: ErrInvalidTenantID,
		},
		{
			name: "unmapped tenant_id returns error",
			event: &auditv1.AuditEvent{
				EventId:    uuid.New().String(),
				TableName:  "accounts",
				Operation:  auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
				SchemaName: "current_account",
				Timestamp:  timestamppb.New(time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)),
				Metadata: map[string]string{
					"tenant_id": uuid.New().String(), // Different tenant
				},
			},
			wantErr: ErrTenantNotMapped,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			measurement, err := transformer.Transform(tt.event)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, measurement)

			if tt.validate != nil {
				tt.validate(t, measurement)
			}
		})
	}
}

func TestAuditEventTransformer_DeriveAssetCode(t *testing.T) {
	transformer := NewAuditEventTransformer(nil)

	tests := []struct {
		schemaName string
		want       string
	}{
		{
			schemaName: "current_account",
			want:       "MERIDIAN-CURRENT-ACCOUNT-OPS",
		},
		{
			schemaName: "position_keeping",
			want:       "MERIDIAN-POSITION-KEEPING-OPS",
		},
		{
			schemaName: "financial_accounting",
			want:       "MERIDIAN-FINANCIAL-ACCOUNTING-OPS",
		},
		{
			schemaName: "payment_order",
			want:       "MERIDIAN-PAYMENT-ORDER-OPS",
		},
		{
			schemaName: "party",
			want:       "MERIDIAN-PARTY-OPS",
		},
	}

	for _, tt := range tests {
		t.Run(tt.schemaName, func(t *testing.T) {
			got := transformer.deriveAssetCode(tt.schemaName)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestAuditEventTransformer_BuildAttributes(t *testing.T) {
	transformer := NewAuditEventTransformer(nil)

	tests := []struct {
		name  string
		event *auditv1.AuditEvent
		want  map[string]string
	}{
		{
			name: "all attributes present",
			event: &auditv1.AuditEvent{
				SchemaName: "current_account",
				TableName:  "accounts",
				Operation:  auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
			},
			want: map[string]string{
				"service":   "current_account",
				"operation": "INSERT",
				"table":     "accounts",
			},
		},
		{
			name: "UPDATE operation",
			event: &auditv1.AuditEvent{
				SchemaName: "position_keeping",
				TableName:  "transactions",
				Operation:  auditv1.AuditOperation_AUDIT_OPERATION_UPDATE,
			},
			want: map[string]string{
				"service":   "position_keeping",
				"operation": "UPDATE",
				"table":     "transactions",
			},
		},
		{
			name: "DELETE operation",
			event: &auditv1.AuditEvent{
				SchemaName: "party",
				TableName:  "customers",
				Operation:  auditv1.AuditOperation_AUDIT_OPERATION_DELETE,
			},
			want: map[string]string{
				"service":   "party",
				"operation": "DELETE",
				"table":     "customers",
			},
		},
		{
			name: "UNSPECIFIED operation omitted",
			event: &auditv1.AuditEvent{
				SchemaName: "current_account",
				TableName:  "accounts",
				Operation:  auditv1.AuditOperation_AUDIT_OPERATION_UNSPECIFIED,
			},
			want: map[string]string{
				"service": "current_account",
				"table":   "accounts",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := transformer.buildAttributes(tt.event)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestAuditEventTransformer_SetQualityScore(t *testing.T) {
	transformer := NewAuditEventTransformer(nil)

	assert.Equal(t, 60, transformer.defaultQualityScore)

	transformer.SetQualityScore(75)
	assert.Equal(t, 75, transformer.defaultQualityScore)
}

func TestAuditEventTransformer_Integration(t *testing.T) {
	// Integration test: full transformation pipeline
	tenantID := uuid.New()
	accountID := uuid.New()

	mapping := map[uuid.UUID]uuid.UUID{
		tenantID: accountID,
	}

	transformer := NewAuditEventTransformer(mapping)
	transformer.SetQualityScore(70) // Override quality score

	event := &auditv1.AuditEvent{
		EventId:       uuid.New().String(),
		TableName:     "financial_positions",
		Operation:     auditv1.AuditOperation_AUDIT_OPERATION_UPDATE,
		RecordId:      uuid.New().String(),
		SchemaName:    "position_keeping",
		Timestamp:     timestamppb.New(time.Date(2025, 12, 24, 10, 30, 0, 0, time.UTC)),
		ChangedBy:     "system",
		TransactionId: uuid.New().String(),
		Metadata: map[string]string{
			"tenant_id": tenantID.String(),
		},
	}

	measurement, err := transformer.Transform(event)
	require.NoError(t, err)
	require.NotNil(t, measurement)

	// Verify all fields
	assert.NotEqual(t, uuid.Nil, measurement.ID)
	assert.Equal(t, accountID, measurement.AccountID)
	assert.Equal(t, "MERIDIAN-POSITION-KEEPING-OPS", measurement.AssetCode)
	assert.True(t, measurement.Quantity.Equal(decimal.NewFromInt(1)))
	assert.Equal(t, "AUDIT_STREAM", measurement.Source)
	assert.Equal(t, 70, measurement.QualityScore)

	// Verify period is instant at the correct timestamp
	expectedTime := time.Date(2025, 12, 24, 10, 30, 0, 0, time.UTC)
	assert.True(t, measurement.Period.IsInstant())
	assert.Equal(t, expectedTime, measurement.Period.Start)
	assert.Equal(t, expectedTime, measurement.Period.End)

	// Verify attributes
	assert.Equal(t, "position_keeping", measurement.Attributes["service"])
	assert.Equal(t, "UPDATE", measurement.Attributes["operation"])
	assert.Equal(t, "financial_positions", measurement.Attributes["table"])

	// Verify lifecycle fields
	assert.Nil(t, measurement.SupersededBy)
	assert.Empty(t, measurement.SettlementRun)
	assert.Nil(t, measurement.LockedAt)
	assert.True(t, measurement.IsCurrent())
	assert.False(t, measurement.IsLocked())

	// Verify ReceivedAt is recent (within last second)
	assert.WithinDuration(t, time.Now().UTC(), measurement.ReceivedAt, time.Second)
}
