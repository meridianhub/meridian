package persistence_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	auditv1 "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
	"github.com/meridianhub/meridian/services/audit-worker/adapters/persistence"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// setupCockroachDBWithSchema creates a CockroachDB test instance and a tenant schema with audit_log.
func setupCockroachDBWithSchema(t *testing.T, tenantID tenant.TenantID) *gorm.DB {
	t.Helper()

	db, cleanup := testdb.SetupCockroachDB(t, nil)
	t.Cleanup(cleanup)

	schema := tenantID.SchemaName()
	require.NoError(t, db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %q", schema)).Error)
	require.NoError(t, db.Exec(fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %q.audit_log (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			event_id VARCHAR(100) UNIQUE NOT NULL,
			table_name VARCHAR(100) NOT NULL,
			operation VARCHAR(20) NOT NULL,
			record_id VARCHAR(100) NOT NULL,
			old_values TEXT,
			new_values TEXT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			schema_name VARCHAR(100),
			changed_by VARCHAR(100),
			transaction_id VARCHAR(100),
			client_ip VARCHAR(45),
			user_agent TEXT,
			correlation_id VARCHAR(255),
			causation_id VARCHAR(255),
			idempotency_key VARCHAR(255)
		)
	`, schema)).Error)

	return db
}

func TestTenantAuditWriter_WriteAuditEvent_HappyPath(t *testing.T) {
	tenantID, err := tenant.NewTenantID("aw_happy")
	require.NoError(t, err)

	db := setupCockroachDBWithSchema(t, tenantID)

	writer, err := persistence.NewTenantAuditWriter(db)
	require.NoError(t, err)

	now := time.Now().UTC().Truncate(time.Millisecond)
	event := &auditv1.AuditEvent{
		EventId:        "evt_happy_001",
		TableName:      "accounts",
		Operation:      auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
		RecordId:       "acc_123",
		NewValues:      `{"balance": "500.00"}`,
		OldValues:      "",
		SchemaName:     "current_account",
		ChangedBy:      "alice",
		TransactionId:  "txn_abc",
		ClientIp:       "10.0.0.1",
		UserAgent:      "test-agent/1.0",
		CorrelationId:  "corr_xyz",
		CausationId:    "cause_123",
		IdempotencyKey: "idem_abc",
		Timestamp:      timestamppb.New(now),
	}

	ctx := tenant.WithTenant(context.Background(), tenantID)
	err = writer.WriteAuditEvent(ctx, event)
	require.NoError(t, err)

	// Verify record was written
	var count int64
	schema := tenantID.SchemaName()
	require.NoError(t, db.Raw(
		fmt.Sprintf("SELECT COUNT(*) FROM %q.audit_log WHERE event_id = ?", schema),
		event.EventId,
	).Scan(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestTenantAuditWriter_WriteAuditEvent_NilTimestamp(t *testing.T) {
	tenantID, err := tenant.NewTenantID("aw_nil_ts")
	require.NoError(t, err)

	db := setupCockroachDBWithSchema(t, tenantID)

	writer, err := persistence.NewTenantAuditWriter(db)
	require.NoError(t, err)

	event := &auditv1.AuditEvent{
		EventId:   "evt_nil_ts",
		TableName: "parties",
		Operation: auditv1.AuditOperation_AUDIT_OPERATION_UPDATE,
		RecordId:  "party_001",
		Timestamp: nil, // nil timestamp — should default to time.Now()
	}

	ctx := tenant.WithTenant(context.Background(), tenantID)
	err = writer.WriteAuditEvent(ctx, event)
	require.NoError(t, err)

	var count int64
	schema := tenantID.SchemaName()
	require.NoError(t, db.Raw(
		fmt.Sprintf("SELECT COUNT(*) FROM %q.audit_log WHERE event_id = ?", schema),
		event.EventId,
	).Scan(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestTenantAuditWriter_WriteAuditEvent_DeleteOperation(t *testing.T) {
	tenantID, err := tenant.NewTenantID("aw_delete")
	require.NoError(t, err)

	db := setupCockroachDBWithSchema(t, tenantID)

	writer, err := persistence.NewTenantAuditWriter(db)
	require.NoError(t, err)

	event := &auditv1.AuditEvent{
		EventId:   "evt_delete_001",
		TableName: "sessions",
		Operation: auditv1.AuditOperation_AUDIT_OPERATION_DELETE,
		RecordId:  "sess_999",
		OldValues: `{"user_id":"usr_123"}`,
		Timestamp: timestamppb.New(time.Now().UTC()),
	}

	ctx := tenant.WithTenant(context.Background(), tenantID)
	err = writer.WriteAuditEvent(ctx, event)
	require.NoError(t, err)
}

func TestTenantAuditWriter_WriteAuditEvent_InitialImport(t *testing.T) {
	tenantID, err := tenant.NewTenantID("aw_import")
	require.NoError(t, err)

	db := setupCockroachDBWithSchema(t, tenantID)

	writer, err := persistence.NewTenantAuditWriter(db)
	require.NoError(t, err)

	event := &auditv1.AuditEvent{
		EventId:   "evt_import_001",
		TableName: "positions",
		Operation: auditv1.AuditOperation_AUDIT_OPERATION_INITIAL_IMPORT,
		RecordId:  "pos_001",
		NewValues: `{"amount":"1000.00"}`,
		Timestamp: timestamppb.New(time.Now().UTC()),
	}

	ctx := tenant.WithTenant(context.Background(), tenantID)
	err = writer.WriteAuditEvent(ctx, event)
	require.NoError(t, err)
}

func TestTenantAuditWriter_WriteAuditEvent_IdempotentDuplicate(t *testing.T) {
	tenantID, err := tenant.NewTenantID("aw_idem")
	require.NoError(t, err)

	db := setupCockroachDBWithSchema(t, tenantID)

	writer, err := persistence.NewTenantAuditWriter(db)
	require.NoError(t, err)

	event := &auditv1.AuditEvent{
		EventId:   "evt_idem_001",
		TableName: "accounts",
		Operation: auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
		RecordId:  "acc_001",
		Timestamp: timestamppb.New(time.Now().UTC()),
	}

	ctx := tenant.WithTenant(context.Background(), tenantID)

	// Write twice — both should succeed (idempotent)
	err = writer.WriteAuditEvent(ctx, event)
	require.NoError(t, err)

	err = writer.WriteAuditEvent(ctx, event)
	require.NoError(t, err)

	// Only one row should exist
	var count int64
	schema := tenantID.SchemaName()
	require.NoError(t, db.Raw(
		fmt.Sprintf("SELECT COUNT(*) FROM %q.audit_log WHERE event_id = ?", schema),
		event.EventId,
	).Scan(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestTenantAuditWriter_WriteAuditEvent_CancelledContext(t *testing.T) {
	tenantID, err := tenant.NewTenantID("aw_cancel")
	require.NoError(t, err)

	db := setupCockroachDBWithSchema(t, tenantID)

	writer, err := persistence.NewTenantAuditWriter(db)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	ctx = tenant.WithTenant(ctx, tenantID)
	cancel() // already cancelled

	event := &auditv1.AuditEvent{
		EventId:   "evt_cancel_001",
		TableName: "accounts",
		Operation: auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
		RecordId:  "acc_001",
		Timestamp: timestamppb.New(time.Now().UTC()),
	}

	err = writer.WriteAuditEvent(ctx, event)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "context")
}
