package service

import (
	"context"
	"fmt"
	"log/slog"
	"testing"

	auditv1 "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// migrationDDL reproduces the audit tables as they exist after applying
// 20251217000001_audit_system.sql + 20260323000001_align_audit_schema.sql.
// Tests verify that the application code works against this schema.
const migrationDDL = `
CREATE TABLE IF NOT EXISTS %q.audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    table_name VARCHAR(100) NOT NULL,
    operation VARCHAR(20) NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE', 'INITIAL_IMPORT')),
    record_id VARCHAR(100) NOT NULL,
    changed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ DEFAULT now(),
    changed_by VARCHAR(100),
    old_values JSONB,
    new_values JSONB,
    transaction_id VARCHAR(100),
    client_ip VARCHAR(45),
    user_agent TEXT,
    event_id VARCHAR(100),
    schema_name VARCHAR(100),
    correlation_id VARCHAR(255),
    causation_id VARCHAR(255),
    idempotency_key VARCHAR(255),
    UNIQUE (event_id)
);

CREATE TABLE IF NOT EXISTS %[1]q.audit_outbox (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    table_name VARCHAR(100) NOT NULL,
    operation VARCHAR(20) NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE', 'INITIAL_IMPORT')),
    record_id VARCHAR(100) NOT NULL,
    old_values JSONB,
    new_values JSONB,
    status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    retry_count INT NOT NULL DEFAULT 0,
    last_error TEXT,
    changed_by VARCHAR(100),
    transaction_id VARCHAR(100),
    client_ip VARCHAR(45),
    user_agent TEXT
);
`

// setupMigrationSchema creates audit tables using the exact DDL from the actual migrations,
// NOT the idealized schema that other tests use. This catches drift between migrations and code.
func setupMigrationSchema(t *testing.T) (context.Context, *gorm.DB, string) {
	t.Helper()

	db, cleanup := testdb.SetupCockroachDB(t, nil)
	t.Cleanup(cleanup)

	tenantID, err := tenant.NewTenantID("migration_test")
	require.NoError(t, err)

	schema := tenantID.SchemaName()
	require.NoError(t, db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %q", schema)).Error)
	require.NoError(t, db.Exec(fmt.Sprintf(migrationDDL, schema)).Error)

	ctx := tenant.WithTenant(context.Background(), tenantID)
	return ctx, db, schema
}

// TestListAuditEntries_AgainstMigrationSchema verifies that the AuditService query
// works against the audit_log table as created by the actual migration DDL.
//
// The migration creates: id, table_name, operation, record_id, changed_at, changed_by, ...
// The service queries:   id, event_id, table_name, operation, record_id, created_at, changed_by, ...
//
// This test will fail if the migration schema diverges from what the code expects.
func TestListAuditEntries_AgainstMigrationSchema(t *testing.T) {
	ctx, db, schema := setupMigrationSchema(t)

	// Insert a row using the migration's actual column names
	require.NoError(t, db.Exec(fmt.Sprintf(`
		INSERT INTO %q.audit_log (table_name, operation, record_id, changed_by, changed_at)
		VALUES ('parties', 'INSERT', gen_random_uuid(), 'test_user', now())
	`, schema)).Error)

	svc, err := NewAuditService(db, slog.Default())
	require.NoError(t, err)

	// The service should be able to query this row
	resp, err := svc.ListAuditEntries(ctx, &auditv1.ListAuditEntriesRequest{})
	require.NoError(t, err, "ListAuditEntries should succeed against migration-created schema")
	assert.Len(t, resp.Entries, 1)
}

// TestAuditLogGORMModel_WritesToMigrationSchema verifies that the GORM AuditLog model
// can write to the audit_log table as created by the actual migration DDL.
//
// The GORM model maps CreatedAt to "created_at", but the migration column is "changed_at".
// The GORM model defines record_id as varchar(50), but the migration uses UUID type.
func TestAuditLogGORMModel_WritesToMigrationSchema(t *testing.T) {
	_, db, schema := setupMigrationSchema(t)

	// Try to write via the GORM AuditLog model (same as worker.insertAuditLog does)
	entry := &audit.AuditLog{
		Table:     "parties",
		Operation: "INSERT",
		RecordID:  "d1c2e3f4-5678-9abc-def0-1234567890ab",
	}
	changedBy := "test_user"
	entry.ChangedBy = &changedBy

	err := db.Table(fmt.Sprintf("%q.audit_log", schema)).Create(entry).Error
	require.NoError(t, err, "GORM AuditLog model should write to migration-created audit_log table")

	// Verify it was written
	var count int64
	require.NoError(t, db.Raw(fmt.Sprintf("SELECT count(*) FROM %q.audit_log", schema)).Scan(&count).Error)
	assert.Equal(t, int64(1), count)
}

// TestAuditOutbox_AcceptsStringRecordIDs verifies that the audit_outbox table
// accepts non-UUID record IDs (e.g. "IBA-xxx"), since the GORM model defines
// record_id as varchar(50) but the migration DDL uses UUID type.
func TestAuditOutbox_AcceptsStringRecordIDs(t *testing.T) {
	_, db, schema := setupMigrationSchema(t)

	// Internal bank accounts use string IDs like "IBA-<uuid>"
	// This is what GORM hooks actually write via publishToKafkaWithFallback
	entry := &audit.AuditOutbox{
		Table:     "internal_accounts",
		Operation: "INSERT",
		RecordID:  "IBA-546d2339-cf23-4c77-a317-a4cf4f300472",
		Status:    "pending",
	}
	changedBy := "system"
	entry.ChangedBy = &changedBy

	err := db.Table(fmt.Sprintf("%q.audit_outbox", schema)).Create(entry).Error
	require.NoError(t, err, "audit_outbox should accept string record IDs (migration uses UUID type)")
}
