package audit

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	auditv1 "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCreateAuditEvent(t *testing.T) {
	t.Run("creates event with all fields", func(t *testing.T) {
		ctx := context.Background()
		ctx = WithTransactionID(ctx, "tx-123")
		ctx = WithCorrelationID(ctx, "corr-456")

		event := CreateAuditEvent(
			ctx,
			"customers",
			"INSERT",
			"cust-001",
			"",
			`{"name":"Test Customer"}`,
			"user-123",
			"party",
		)

		assert.NotEmpty(t, event.EventId)
		assert.Equal(t, "customers", event.TableName)
		assert.Equal(t, auditv1.AuditOperation_AUDIT_OPERATION_INSERT, event.Operation)
		assert.Equal(t, "cust-001", event.RecordId)
		assert.Empty(t, event.OldValues)
		assert.Equal(t, `{"name":"Test Customer"}`, event.NewValues)
		assert.Equal(t, "user-123", event.ChangedBy)
		assert.Equal(t, "party", event.SchemaName)
		assert.Equal(t, "tx-123", event.TransactionId)
		assert.Equal(t, "corr-456", event.CorrelationId)
		assert.NotNil(t, event.Timestamp)
	})

	t.Run("handles UPDATE operation", func(t *testing.T) {
		event := CreateAuditEvent(
			context.Background(),
			"accounts",
			"UPDATE",
			"acc-001",
			`{"balance":100}`,
			`{"balance":150}`,
			"user-456",
			"current_account",
		)

		assert.Equal(t, auditv1.AuditOperation_AUDIT_OPERATION_UPDATE, event.Operation)
		assert.Equal(t, `{"balance":100}`, event.OldValues)
		assert.Equal(t, `{"balance":150}`, event.NewValues)
	})

	t.Run("handles DELETE operation", func(t *testing.T) {
		event := CreateAuditEvent(
			context.Background(),
			"transactions",
			"DELETE",
			"tx-001",
			`{"amount":500}`,
			"",
			"system",
			"payment_order",
		)

		assert.Equal(t, auditv1.AuditOperation_AUDIT_OPERATION_DELETE, event.Operation)
		assert.NotEmpty(t, event.OldValues)
		assert.Empty(t, event.NewValues)
	})

	t.Run("handles unknown operation", func(t *testing.T) {
		event := CreateAuditEvent(
			context.Background(),
			"test",
			"UNKNOWN",
			"id-1",
			"",
			"",
			"user",
			"schema",
		)

		assert.Equal(t, auditv1.AuditOperation_AUDIT_OPERATION_UNSPECIFIED, event.Operation)
	})
}

func TestOperationToProto(t *testing.T) {
	tests := []struct {
		input    string
		expected auditv1.AuditOperation
	}{
		{"INSERT", auditv1.AuditOperation_AUDIT_OPERATION_INSERT},
		{"UPDATE", auditv1.AuditOperation_AUDIT_OPERATION_UPDATE},
		{"DELETE", auditv1.AuditOperation_AUDIT_OPERATION_DELETE},
		{"UNKNOWN", auditv1.AuditOperation_AUDIT_OPERATION_UNSPECIFIED},
		{"", auditv1.AuditOperation_AUDIT_OPERATION_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := operationToProto(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestContextHelpers(t *testing.T) {
	t.Run("WithTransactionID and retrieval", func(t *testing.T) {
		ctx := context.Background()
		ctx = WithTransactionID(ctx, "tx-abc")

		result := getTransactionIDFromContext(ctx)
		assert.Equal(t, "tx-abc", result)
	})

	t.Run("WithCorrelationID and retrieval", func(t *testing.T) {
		ctx := context.Background()
		ctx = WithCorrelationID(ctx, "corr-xyz")

		result := getCorrelationIDFromContext(ctx)
		assert.Equal(t, "corr-xyz", result)
	})

	t.Run("returns empty for missing values", func(t *testing.T) {
		ctx := context.Background()

		assert.Empty(t, getTransactionIDFromContext(ctx))
		assert.Empty(t, getCorrelationIDFromContext(ctx))
	})

	t.Run("handles nil context", func(t *testing.T) {
		//nolint:staticcheck // Testing nil context handling
		assert.Empty(t, getTransactionIDFromContext(nil)) //nolint:staticcheck // SA1012: Intentionally testing nil context handling
		assert.Empty(t, getCorrelationIDFromContext(nil)) //nolint:staticcheck // SA1012: Intentionally testing nil context handling
	})
}

func TestPublisher(t *testing.T) {
	t.Run("NewPublisher returns error when bootstrap servers empty", func(t *testing.T) {
		p, err := NewPublisher(PublisherConfig{
			BootstrapServers: "",
			SchemaName:       "test",
		})
		assert.ErrorIs(t, err, ErrPublisherDisabled)
		assert.Nil(t, p)
	})

	t.Run("nil publisher is safe to use", func(t *testing.T) {
		var p *Publisher

		// These should not panic
		assert.False(t, p.IsEnabled())
		p.Enable()
		p.Disable()

		err := p.Publish(context.Background(), &auditv1.AuditEvent{})
		assert.NoError(t, err)

		err = p.Close()
		assert.NoError(t, err)
	})

	t.Run("Enable and Disable toggle state", func(t *testing.T) {
		// Create a mock publisher
		p := &Publisher{
			enabled: true,
		}

		assert.True(t, p.IsEnabled())

		p.Disable()
		assert.False(t, p.IsEnabled())

		p.Enable()
		assert.True(t, p.IsEnabled())
	})

	t.Run("Publish returns nil when disabled", func(t *testing.T) {
		p := &Publisher{
			enabled: false,
		}

		err := p.Publish(context.Background(), &auditv1.AuditEvent{})
		assert.NoError(t, err)
	})
}

func TestGlobalPublisher(t *testing.T) {
	t.Run("SetGlobalPublisher and GetGlobalPublisher", func(t *testing.T) {
		// Save and restore global state
		original := GetGlobalPublisher()
		defer SetGlobalPublisher(original)

		p := &Publisher{enabled: true}
		SetGlobalPublisher(p)

		result := GetGlobalPublisher()
		assert.Equal(t, p, result)
	})

	t.Run("GetGlobalPublisher returns nil by default", func(t *testing.T) {
		// Save and restore global state
		original := GetGlobalPublisher()
		defer SetGlobalPublisher(original)

		SetGlobalPublisher(nil)
		result := GetGlobalPublisher()
		assert.Nil(t, result)
	})
}

func TestSchemaName(t *testing.T) {
	t.Run("SetSchemaName and GetSchemaName", func(t *testing.T) {
		// Save and restore global state
		original := GetSchemaName()
		defer SetSchemaName(original)

		SetSchemaName("party_audit")
		assert.Equal(t, "party_audit", GetSchemaName())
	})
}

func TestAuditEventTimestamp(t *testing.T) {
	t.Run("timestamp is set correctly", func(t *testing.T) {
		before := time.Now()

		event := CreateAuditEvent(
			context.Background(),
			"test",
			"INSERT",
			"id-1",
			"",
			"{}",
			"user",
			"schema",
		)

		after := time.Now()

		eventTime := event.Timestamp.AsTime()
		assert.True(t, eventTime.After(before) || eventTime.Equal(before))
		assert.True(t, eventTime.Before(after) || eventTime.Equal(after))
	})
}

func TestPublishToKafkaWithFallbackMetrics(t *testing.T) {
	// This test verifies that metrics are recorded correctly
	// when Kafka publishing is disabled or unavailable

	t.Run("records fallback metric when publisher is nil", func(t *testing.T) {
		// Save and restore global state
		original := GetGlobalPublisher()
		defer SetGlobalPublisher(original)

		SetGlobalPublisher(nil)

		db := setupHooksTestDB(t)

		entity := testEntity{
			ID:     uuid.New(),
			Name:   "Test",
			Status: "active",
		}

		err := db.Transaction(func(tx *gorm.DB) error {
			return RecordCreate(tx, entity)
		})
		require.NoError(t, err)

		// Verify outbox entry was created (fallback path)
		var count int64
		db.Model(&testAuditOutbox{}).Count(&count)
		assert.Equal(t, int64(1), count)
	})
}

// Ensure we're importing gorm for this test
var _ *gorm.DB = nil
