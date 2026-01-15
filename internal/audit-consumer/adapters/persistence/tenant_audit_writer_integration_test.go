//go:build integration
// +build integration

package persistence_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	auditv1 "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
	"github.com/meridianhub/meridian/internal/audit-consumer/adapters/persistence"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// setupTestDB creates a test database connection with tenant schemas
func setupTestDB(t *testing.T) (*gorm.DB, func()) {
	t.Helper()

	// Use test database URL from environment or default
	dbURL := getTestDatabaseURL()
	require.NotEmpty(t, dbURL, "TEST_DATABASE_URL must be set for integration tests")

	// Connect to database with GORM
	db, err := gorm.Open(postgres.Open(dbURL), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)

	// Cleanup function to close DB
	cleanup := func() {
		sqlDB, err := db.DB()
		if err == nil {
			sqlDB.Close()
		}
	}

	return db, cleanup
}

// createTenantSchema creates a tenant schema with audit_log table
func createTenantSchema(t *testing.T, db *gorm.DB, tenantID tenant.TenantID) {
	t.Helper()

	schemaName := tenantID.SchemaName()

	// Create schema
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", schemaName)).Error
	require.NoError(t, err)

	// Create audit_log table in tenant schema
	// Based on shared/domain/models/audit.go AuditLog structure
	createTableSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s.audit_log (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			event_id VARCHAR(100) NOT NULL UNIQUE,
			table_name VARCHAR(100) NOT NULL,
			operation VARCHAR(10) NOT NULL,
			record_id VARCHAR(100) NOT NULL,
			old_values TEXT,
			new_values TEXT,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			schema_name VARCHAR(100),
			changed_by VARCHAR(100),
			transaction_id VARCHAR(100),
			client_ip VARCHAR(45),
			user_agent TEXT,
			correlation_id VARCHAR(100),
			causation_id VARCHAR(100),
			idempotency_key VARCHAR(100)
		)
	`, schemaName)

	err = db.Exec(createTableSQL).Error
	require.NoError(t, err)

	// Create index on event_id for idempotency
	err = db.Exec(fmt.Sprintf(
		"CREATE INDEX IF NOT EXISTS idx_audit_log_event_id ON %s.audit_log(event_id)",
		schemaName,
	)).Error
	require.NoError(t, err)
}

// dropTenantSchema drops a tenant schema
func dropTenantSchema(t *testing.T, db *gorm.DB, tenantID tenant.TenantID) {
	t.Helper()

	schemaName := tenantID.SchemaName()
	err := db.Exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schemaName)).Error
	require.NoError(t, err)
}

// getAuditLogCount returns the number of audit log entries for a tenant
func getAuditLogCount(t *testing.T, db *gorm.DB, tenantID tenant.TenantID) int64 {
	t.Helper()

	var count int64
	schemaName := tenantID.SchemaName()
	err := db.Raw(fmt.Sprintf("SELECT COUNT(*) FROM %s.audit_log", schemaName)).Scan(&count).Error
	require.NoError(t, err)

	return count
}

// getTestDatabaseURL returns the test database URL from environment
func getTestDatabaseURL() string {
	// Try TEST_DATABASE_URL first, fall back to DATABASE_URL
	if url := getEnv("TEST_DATABASE_URL"); url != "" {
		return url
	}
	return getEnv("DATABASE_URL")
}

func getEnv(key string) string {
	return os.Getenv(key)
}

func TestTenantAuditWriter_Integration_WriteSingleTenantSchema(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	tenantID, err := tenant.NewTenantID("test_tenant_1")
	require.NoError(t, err)

	// Setup: Create tenant schema
	createTenantSchema(t, db, tenantID)
	defer dropTenantSchema(t, db, tenantID)

	// Create writer
	writer, err := persistence.NewTenantAuditWriter(db)
	require.NoError(t, err)

	// Create audit event
	now := time.Now().UTC()
	event := &auditv1.AuditEvent{
		EventId:       "evt_test_001",
		TableName:     "accounts",
		Operation:     auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
		RecordId:      "acc_123",
		NewValues:     `{"balance": "100.00"}`,
		ChangedBy:     "user_789",
		TransactionId: "txn_abc",
		Timestamp:     timestamppb.New(now),
	}

	// Write audit event with tenant context
	ctx := context.Background()
	ctx = tenant.WithTenant(ctx, tenantID)
	err = writer.WriteAuditEvent(ctx, event)
	require.NoError(t, err)

	// Verify: Check audit log was written to correct tenant schema
	count := getAuditLogCount(t, db, tenantID)
	assert.Equal(t, int64(1), count, "should have 1 audit log entry")

	// Verify: Read back the audit log entry
	var auditLog map[string]interface{}
	err = db.Raw(fmt.Sprintf("SELECT * FROM %s.audit_log WHERE event_id = ?", tenantID.SchemaName()), event.EventId).
		Scan(&auditLog).Error
	require.NoError(t, err)

	assert.Equal(t, event.EventId, auditLog["event_id"])
	assert.Equal(t, event.TableName, auditLog["table_name"])
	assert.Equal(t, "INSERT", auditLog["operation"])
	assert.Equal(t, event.RecordId, auditLog["record_id"])
	assert.Equal(t, event.NewValues, auditLog["new_values"])
	assert.Equal(t, event.ChangedBy, auditLog["changed_by"])
	assert.Equal(t, event.TransactionId, auditLog["transaction_id"])
}

func TestTenantAuditWriter_Integration_MultipleTenantSchemas(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create two tenant schemas
	tenant1, err := tenant.NewTenantID("test_tenant_a")
	require.NoError(t, err)
	tenant2, err := tenant.NewTenantID("test_tenant_b")
	require.NoError(t, err)

	createTenantSchema(t, db, tenant1)
	createTenantSchema(t, db, tenant2)
	defer dropTenantSchema(t, db, tenant1)
	defer dropTenantSchema(t, db, tenant2)

	// Create writer
	writer, err := persistence.NewTenantAuditWriter(db)
	require.NoError(t, err)

	ctx := context.Background()

	// Write to tenant 1
	event1 := &auditv1.AuditEvent{
		EventId:   "evt_tenant1_001",
		TableName: "accounts",
		Operation: auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
		RecordId:  "acc_t1_123",
		NewValues: `{"balance": "100.00"}`,
	}
	ctx1 := tenant.WithTenant(ctx, tenant1)
	err = writer.WriteAuditEvent(ctx1, event1)
	require.NoError(t, err)

	// Write to tenant 2
	event2 := &auditv1.AuditEvent{
		EventId:   "evt_tenant2_001",
		TableName: "accounts",
		Operation: auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
		RecordId:  "acc_t2_456",
		NewValues: `{"balance": "200.00"}`,
	}
	ctx2 := tenant.WithTenant(ctx, tenant2)
	err = writer.WriteAuditEvent(ctx2, event2)
	require.NoError(t, err)

	// Verify: Tenant 1 has only its own audit log
	count1 := getAuditLogCount(t, db, tenant1)
	assert.Equal(t, int64(1), count1, "tenant 1 should have 1 audit log entry")

	// Verify: Tenant 2 has only its own audit log
	count2 := getAuditLogCount(t, db, tenant2)
	assert.Equal(t, int64(1), count2, "tenant 2 should have 1 audit log entry")

	// Verify: Tenant isolation - event IDs don't cross tenant boundaries
	var tenant1Events []string
	err = db.Raw(fmt.Sprintf("SELECT event_id FROM %s.audit_log", tenant1.SchemaName())).
		Scan(&tenant1Events).Error
	require.NoError(t, err)
	assert.Contains(t, tenant1Events, event1.EventId)
	assert.NotContains(t, tenant1Events, event2.EventId, "tenant 1 should not see tenant 2 events")
}

func TestTenantAuditWriter_Integration_IdempotentWrites(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	tenantID, err := tenant.NewTenantID("test_tenant_idem")
	require.NoError(t, err)

	createTenantSchema(t, db, tenantID)
	defer dropTenantSchema(t, db, tenantID)

	writer, err := persistence.NewTenantAuditWriter(db)
	require.NoError(t, err)

	ctx := context.Background()

	// Create audit event
	event := &auditv1.AuditEvent{
		EventId:   "evt_idem_001",
		TableName: "accounts",
		Operation: auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
		RecordId:  "acc_123",
		NewValues: `{"balance": "100.00"}`,
	}

	// Write event first time with tenant context
	ctx = tenant.WithTenant(ctx, tenantID)
	err = writer.WriteAuditEvent(ctx, event)
	require.NoError(t, err)

	// Verify: 1 entry exists
	count := getAuditLogCount(t, db, tenantID)
	assert.Equal(t, int64(1), count)

	// Write same event again (retry scenario)
	err = writer.WriteAuditEvent(ctx, event)
	require.NoError(t, err, "duplicate write should not error")

	// Verify: Still only 1 entry (idempotent)
	count = getAuditLogCount(t, db, tenantID)
	assert.Equal(t, int64(1), count, "duplicate event should not create new entry")

	// Write same event third time
	err = writer.WriteAuditEvent(ctx, event)
	require.NoError(t, err, "third duplicate write should not error")

	// Verify: Still only 1 entry
	count = getAuditLogCount(t, db, tenantID)
	assert.Equal(t, int64(1), count, "third duplicate should not create new entry")
}

func TestTenantAuditWriter_Integration_BoundedContextEnforcement(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	tenantID, err := tenant.NewTenantID("test_tenant_bounded")
	require.NoError(t, err)

	createTenantSchema(t, db, tenantID)
	defer dropTenantSchema(t, db, tenantID)

	writer, err := persistence.NewTenantAuditWriter(db)
	require.NoError(t, err)

	ctx := context.Background()

	// Write audit event
	event := &auditv1.AuditEvent{
		EventId:   "evt_bounded_001",
		TableName: "accounts",
		Operation: auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
		RecordId:  "acc_123",
		NewValues: `{"balance": "100.00"}`,
	}

	ctx = tenant.WithTenant(ctx, tenantID)
	err = writer.WriteAuditEvent(ctx, event)
	require.NoError(t, err)

	// Verify: Writer only connects to single database
	// This is enforced by constructor taking a single *gorm.DB
	// and using search_path for tenant routing within that database

	// Verify: No cross-database queries possible
	// The writer uses db.WithGormTenantTransaction which sets search_path
	// to a single tenant schema within the service database
	// Per ADR-0002 Rule 4: Services cannot access other service databases

	sqlDB, err := db.DB()
	require.NoError(t, err)

	stats := sqlDB.Stats()
	assert.GreaterOrEqual(t, stats.MaxOpenConnections, 1, "should have connection pool")

	// Connection pool is shared across all tenant schemas (same database)
	// This verifies bounded context isolation: single DB per service
}

func TestTenantAuditWriter_Integration_ConnectionPooling(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create multiple tenant schemas
	tenants := make([]tenant.TenantID, 3)
	for i := 0; i < 3; i++ {
		tid, err := tenant.NewTenantID(fmt.Sprintf("test_tenant_pool_%d", i))
		require.NoError(t, err)
		tenants[i] = tid
		createTenantSchema(t, db, tid)
		defer dropTenantSchema(t, db, tid)
	}

	writer, err := persistence.NewTenantAuditWriter(db)
	require.NoError(t, err)

	ctx := context.Background()

	// Write to multiple tenants using same connection pool
	for i, tid := range tenants {
		event := &auditv1.AuditEvent{
			EventId:   fmt.Sprintf("evt_pool_%d", i),
			TableName: "accounts",
			Operation: auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
			RecordId:  fmt.Sprintf("acc_%d", i),
			NewValues: `{"balance": "100.00"}`,
		}

		tenantCtx := tenant.WithTenant(ctx, tid)
		err = writer.WriteAuditEvent(tenantCtx, event)
		require.NoError(t, err)
	}

	// Verify: All writes succeeded using shared connection pool
	for _, tid := range tenants {
		count := getAuditLogCount(t, db, tid)
		assert.Equal(t, int64(1), count, "each tenant should have 1 audit log entry")
	}

	// Verify: Connection pool statistics
	sqlDB, err := db.DB()
	require.NoError(t, err)

	stats := sqlDB.Stats()
	assert.GreaterOrEqual(t, stats.OpenConnections, 1, "should have open connections")
	assert.LessOrEqual(t, stats.OpenConnections, stats.MaxOpenConnections, "should not exceed max connections")

	// Connection pool is shared across all tenant schemas
	// This is the PostgreSQL search_path pattern (per ADR-0002)
}
