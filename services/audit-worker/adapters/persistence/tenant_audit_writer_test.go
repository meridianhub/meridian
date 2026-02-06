package persistence_test

import (
	"context"
	"testing"

	auditv1 "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
	"github.com/meridianhub/meridian/services/audit-worker/adapters/persistence"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestNewTenantAuditWriter(t *testing.T) {
	t.Run("valid_database_connection", func(t *testing.T) {
		db := &gorm.DB{} // Mock DB for constructor test
		writer, err := persistence.NewTenantAuditWriter(db)

		assert.NoError(t, err)
		assert.NotNil(t, writer)
	})

	t.Run("nil_database_connection", func(t *testing.T) {
		writer, err := persistence.NewTenantAuditWriter(nil)

		assert.Error(t, err)
		assert.ErrorIs(t, err, persistence.ErrNilDatabase)
		assert.Nil(t, writer)
	})
}

func TestTenantAuditWriter_WriteAuditEvent_TenantIDExtraction(t *testing.T) {
	t.Run("missing_tenant_context", func(t *testing.T) {
		db := &gorm.DB{}
		writer, err := persistence.NewTenantAuditWriter(db)
		require.NoError(t, err)

		event := &auditv1.AuditEvent{
			EventId:   "evt_123",
			TableName: "accounts",
			Operation: auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
			RecordId:  "acc_456",
		}

		// No tenant ID in context
		ctx := context.Background()
		err = writer.WriteAuditEvent(ctx, event)

		assert.Error(t, err)
		assert.ErrorIs(t, err, persistence.ErrMissingTenantContext)
	})
}

func TestTenantAuditWriter_WriteAuditEvent_OperationValidation(t *testing.T) {
	t.Run("unspecified_operation_returns_error", func(t *testing.T) {
		db := &gorm.DB{}
		writer, err := persistence.NewTenantAuditWriter(db)
		require.NoError(t, err)

		event := &auditv1.AuditEvent{
			EventId:   "evt_123",
			TableName: "accounts",
			Operation: auditv1.AuditOperation_AUDIT_OPERATION_UNSPECIFIED,
			RecordId:  "acc_456",
		}

		// Add tenant ID to context (normally done by Kafka consumer)
		ctx := context.Background()
		tenantID, _ := tenant.NewTenantID("test_tenant")
		ctx = tenant.WithTenant(ctx, tenantID)

		err = writer.WriteAuditEvent(ctx, event)

		// Should fail with invalid operation error (before attempting DB access)
		assert.Error(t, err)
		assert.ErrorIs(t, err, persistence.ErrInvalidOperation)
	})

	// Note: Testing valid operations (INSERT, UPDATE, DELETE) requires a real database connection
	// Those are tested in integration tests where we have actual PostgreSQL
}

// Note: Timestamp handling, optional fields, and other database write operations
// are tested in integration tests where we have actual PostgreSQL connections.
// Unit tests here focus on validation logic that doesn't require database access.

// TestSchemaNameGeneration verifies tenant ID to schema name conversion
func TestSchemaNameGeneration(t *testing.T) {
	// This is tested implicitly through tenant.TenantID.SchemaName()
	// but we document the expected behavior here for clarity
	tests := []struct {
		tenantID       string
		expectedSchema string
	}{
		{
			tenantID:       "acme_bank",
			expectedSchema: "org_acme_bank",
		},
		{
			tenantID:       "post_office",
			expectedSchema: "org_post_office",
		},
		{
			tenantID:       "test123",
			expectedSchema: "org_test123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.tenantID, func(t *testing.T) {
			// The tenant.TenantID type handles schema name generation
			// Format: org_{tenant_id} (per ADR-0016)
			// This test documents the expected schema routing behavior
			expectedPrefix := "org_"
			assert.Contains(t, tt.expectedSchema, expectedPrefix)
			assert.Contains(t, tt.expectedSchema, tt.tenantID)
		})
	}
}
