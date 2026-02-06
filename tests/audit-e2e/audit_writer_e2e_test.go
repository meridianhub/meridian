//go:build integration
// +build integration

// Package audit_e2e provides end-to-end integration tests for the multi-service audit system.
// These tests validate the TenantAuditWriter component writing to tenant audit_log tables
// across multiple service databases, verifying bounded context enforcement and tenant isolation.
package audit_e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	auditv1 "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
	"github.com/meridianhub/meridian/services/audit-worker/adapters/persistence"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/protobuf/types/known/timestamppb"
	gormpg "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// =============================================================================
// Test Infrastructure
// =============================================================================

// serviceDB holds a PostgreSQL database for a service
type serviceDB struct {
	container *postgres.PostgresContainer
	db        *gorm.DB
	writer    *persistence.TenantAuditWriter
	name      string
}

// testInfra holds all test databases
type testInfra struct {
	currentAccount      *serviceDB
	financialAccounting *serviceDB
	positionKeeping     *serviceDB
}

// setupTestInfra creates PostgreSQL databases for each service
func setupTestInfra(t *testing.T) *testInfra {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	infra := &testInfra{
		currentAccount:      setupServiceDB(ctx, t, "meridian_current_account"),
		financialAccounting: setupServiceDB(ctx, t, "meridian_financial_accounting"),
		positionKeeping:     setupServiceDB(ctx, t, "meridian_position_keeping"),
	}

	// Register cleanup
	t.Cleanup(func() {
		infra.cleanup()
	})

	return infra
}

// setupServiceDB creates a PostgreSQL database for a service
func setupServiceDB(ctx context.Context, t *testing.T, dbName string) *serviceDB {
	t.Helper()

	pgContainer, err := postgres.Run(ctx,
		"postgres:15-alpine",
		postgres.WithDatabase(dbName),
		postgres.WithUsername("test_user"),
		postgres.WithPassword("test_password"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	require.NoError(t, err, "failed to start postgres container for %s", dbName)

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "failed to get connection string for %s", dbName)

	db, err := gorm.Open(gormpg.Open(connStr), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err, "failed to connect to database %s", dbName)

	writer, err := persistence.NewTenantAuditWriter(db)
	require.NoError(t, err, "failed to create writer for %s", dbName)

	return &serviceDB{
		container: pgContainer,
		db:        db,
		writer:    writer,
		name:      dbName,
	}
}

// cleanup releases all test infrastructure
func (infra *testInfra) cleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for _, svc := range []*serviceDB{infra.currentAccount, infra.financialAccounting, infra.positionKeeping} {
		if svc != nil {
			if svc.db != nil {
				sqlDB, _ := svc.db.DB()
				if sqlDB != nil {
					_ = sqlDB.Close()
				}
			}
			if svc.container != nil {
				_ = svc.container.Terminate(ctx)
			}
		}
	}
}

// createTenantSchema creates tenant schema with audit_log table
func createTenantSchema(t *testing.T, db *gorm.DB, tenantID tenant.TenantID) {
	t.Helper()

	schemaName := tenantID.SchemaName()

	// Create schema
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", schemaName)).Error
	require.NoError(t, err)

	// Create audit_log table
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

	// Create index on event_id
	err = db.Exec(fmt.Sprintf(
		"CREATE INDEX IF NOT EXISTS idx_audit_log_event_id ON %s.audit_log(event_id)",
		schemaName,
	)).Error
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

// getAuditLogByEventID retrieves an audit log entry by event ID
func getAuditLogByEventID(t *testing.T, db *gorm.DB, tenantID tenant.TenantID, eventID string) map[string]interface{} {
	t.Helper()

	var auditLog map[string]interface{}
	schemaName := tenantID.SchemaName()
	err := db.Raw(
		fmt.Sprintf("SELECT * FROM %s.audit_log WHERE event_id = ?", schemaName),
		eventID,
	).Scan(&auditLog).Error
	require.NoError(t, err)

	return auditLog
}

// =============================================================================
// Scenario 1: Multi-Service, Multi-Tenant Writes
// =============================================================================

func TestMultiServiceMultiTenantWrites(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	infra := setupTestInfra(t)
	ctx := context.Background()

	// Create two tenants
	tenantA, err := tenant.NewTenantID("tenant_a")
	require.NoError(t, err)
	tenantB, err := tenant.NewTenantID("tenant_b")
	require.NoError(t, err)

	// Create tenant schemas in all service databases
	createTenantSchema(t, infra.currentAccount.db, tenantA)
	createTenantSchema(t, infra.currentAccount.db, tenantB)
	createTenantSchema(t, infra.financialAccounting.db, tenantA)
	createTenantSchema(t, infra.financialAccounting.db, tenantB)
	createTenantSchema(t, infra.positionKeeping.db, tenantA)
	createTenantSchema(t, infra.positionKeeping.db, tenantB)

	t.Run("concurrent_writes_across_services_and_tenants", func(t *testing.T) {
		// Write audit events for tenant A across all services
		ctxA := tenant.WithTenant(ctx, tenantA)

		err := infra.currentAccount.writer.WriteAuditEvent(ctxA, &auditv1.AuditEvent{
			EventId:       "evt_ca_ta_001",
			TableName:     "accounts",
			Operation:     auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
			RecordId:      "acc_a_001",
			NewValues:     `{"balance": "1000.00"}`,
			ChangedBy:     "user_a",
			TransactionId: "txn_a_001",
			Timestamp:     timestamppb.Now(),
		})
		require.NoError(t, err)

		err = infra.financialAccounting.writer.WriteAuditEvent(ctxA, &auditv1.AuditEvent{
			EventId:       "evt_fa_ta_001",
			TableName:     "ledger_postings",
			Operation:     auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
			RecordId:      "lp_a_001",
			NewValues:     `{"amount": "1000.00"}`,
			ChangedBy:     "user_a",
			TransactionId: "txn_a_001",
			Timestamp:     timestamppb.Now(),
		})
		require.NoError(t, err)

		err = infra.positionKeeping.writer.WriteAuditEvent(ctxA, &auditv1.AuditEvent{
			EventId:       "evt_pk_ta_001",
			TableName:     "positions",
			Operation:     auditv1.AuditOperation_AUDIT_OPERATION_UPDATE,
			RecordId:      "pos_a_001",
			OldValues:     `{"balance": "0.00"}`,
			NewValues:     `{"balance": "1000.00"}`,
			ChangedBy:     "user_a",
			TransactionId: "txn_a_001",
			Timestamp:     timestamppb.Now(),
		})
		require.NoError(t, err)

		// Write audit events for tenant B across all services
		ctxB := tenant.WithTenant(ctx, tenantB)

		err = infra.currentAccount.writer.WriteAuditEvent(ctxB, &auditv1.AuditEvent{
			EventId:       "evt_ca_tb_001",
			TableName:     "accounts",
			Operation:     auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
			RecordId:      "acc_b_001",
			NewValues:     `{"balance": "2000.00"}`,
			ChangedBy:     "user_b",
			TransactionId: "txn_b_001",
			Timestamp:     timestamppb.Now(),
		})
		require.NoError(t, err)

		err = infra.financialAccounting.writer.WriteAuditEvent(ctxB, &auditv1.AuditEvent{
			EventId:       "evt_fa_tb_001",
			TableName:     "ledger_postings",
			Operation:     auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
			RecordId:      "lp_b_001",
			NewValues:     `{"amount": "2000.00"}`,
			ChangedBy:     "user_b",
			TransactionId: "txn_b_001",
			Timestamp:     timestamppb.Now(),
		})
		require.NoError(t, err)

		err = infra.positionKeeping.writer.WriteAuditEvent(ctxB, &auditv1.AuditEvent{
			EventId:       "evt_pk_tb_001",
			TableName:     "positions",
			Operation:     auditv1.AuditOperation_AUDIT_OPERATION_UPDATE,
			RecordId:      "pos_b_001",
			OldValues:     `{"balance": "0.00"}`,
			NewValues:     `{"balance": "2000.00"}`,
			ChangedBy:     "user_b",
			TransactionId: "txn_b_001",
			Timestamp:     timestamppb.Now(),
		})
		require.NoError(t, err)

		// Verify tenant A has 1 audit entry in each service database
		countCA_A := getAuditLogCount(t, infra.currentAccount.db, tenantA)
		assert.Equal(t, int64(1), countCA_A, "tenant A should have 1 audit log in current-account")

		countFA_A := getAuditLogCount(t, infra.financialAccounting.db, tenantA)
		assert.Equal(t, int64(1), countFA_A, "tenant A should have 1 audit log in financial-accounting")

		countPK_A := getAuditLogCount(t, infra.positionKeeping.db, tenantA)
		assert.Equal(t, int64(1), countPK_A, "tenant A should have 1 audit log in position-keeping")

		// Verify tenant B has 1 audit entry in each service database
		countCA_B := getAuditLogCount(t, infra.currentAccount.db, tenantB)
		assert.Equal(t, int64(1), countCA_B, "tenant B should have 1 audit log in current-account")

		countFA_B := getAuditLogCount(t, infra.financialAccounting.db, tenantB)
		assert.Equal(t, int64(1), countFA_B, "tenant B should have 1 audit log in financial-accounting")

		countPK_B := getAuditLogCount(t, infra.positionKeeping.db, tenantB)
		assert.Equal(t, int64(1), countPK_B, "tenant B should have 1 audit log in position-keeping")
	})

	t.Run("verify_tenant_isolation_across_services", func(t *testing.T) {
		// Verify tenant A cannot see tenant B's events in any service
		var eventIDs []string

		// Check current-account for tenant A
		err := infra.currentAccount.db.Raw(
			fmt.Sprintf("SELECT event_id FROM %s.audit_log", tenantA.SchemaName()),
		).Scan(&eventIDs).Error
		require.NoError(t, err)
		assert.Contains(t, eventIDs, "evt_ca_ta_001")
		assert.NotContains(t, eventIDs, "evt_ca_tb_001", "tenant A should not see tenant B events")

		// Check financial-accounting for tenant A
		eventIDs = nil
		err = infra.financialAccounting.db.Raw(
			fmt.Sprintf("SELECT event_id FROM %s.audit_log", tenantA.SchemaName()),
		).Scan(&eventIDs).Error
		require.NoError(t, err)
		assert.Contains(t, eventIDs, "evt_fa_ta_001")
		assert.NotContains(t, eventIDs, "evt_fa_tb_001", "tenant A should not see tenant B events")

		// Check position-keeping for tenant A
		eventIDs = nil
		err = infra.positionKeeping.db.Raw(
			fmt.Sprintf("SELECT event_id FROM %s.audit_log", tenantA.SchemaName()),
		).Scan(&eventIDs).Error
		require.NoError(t, err)
		assert.Contains(t, eventIDs, "evt_pk_ta_001")
		assert.NotContains(t, eventIDs, "evt_pk_tb_001", "tenant A should not see tenant B events")
	})
}

// =============================================================================
// Scenario 2: Bounded Context Enforcement
// =============================================================================

func TestBoundedContextEnforcement(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	infra := setupTestInfra(t)
	ctx := context.Background()

	tenantID, err := tenant.NewTenantID("tenant_bounded")
	require.NoError(t, err)

	// Create tenant schemas in all databases
	createTenantSchema(t, infra.currentAccount.db, tenantID)
	createTenantSchema(t, infra.financialAccounting.db, tenantID)
	createTenantSchema(t, infra.positionKeeping.db, tenantID)

	ctxWithTenant := tenant.WithTenant(ctx, tenantID)

	t.Run("each_writer_writes_only_to_its_service_database", func(t *testing.T) {
		// Write event to current-account
		err := infra.currentAccount.writer.WriteAuditEvent(ctxWithTenant, &auditv1.AuditEvent{
			EventId:   "evt_bounded_ca_001",
			TableName: "accounts",
			Operation: auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
			RecordId:  "acc_001",
			NewValues: `{"balance": "100.00"}`,
			Timestamp: timestamppb.Now(),
		})
		require.NoError(t, err)

		// Write event to financial-accounting
		err = infra.financialAccounting.writer.WriteAuditEvent(ctxWithTenant, &auditv1.AuditEvent{
			EventId:   "evt_bounded_fa_001",
			TableName: "ledger_postings",
			Operation: auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
			RecordId:  "lp_001",
			NewValues: `{"amount": "100.00"}`,
			Timestamp: timestamppb.Now(),
		})
		require.NoError(t, err)

		// Verify current-account database only has current-account events
		countCA := getAuditLogCount(t, infra.currentAccount.db, tenantID)
		assert.Equal(t, int64(1), countCA, "current-account DB should have exactly 1 audit log")

		auditLogCA := getAuditLogByEventID(t, infra.currentAccount.db, tenantID, "evt_bounded_ca_001")
		assert.Equal(t, "accounts", auditLogCA["table_name"])

		// Verify financial-accounting database only has financial-accounting events
		countFA := getAuditLogCount(t, infra.financialAccounting.db, tenantID)
		assert.Equal(t, int64(1), countFA, "financial-accounting DB should have exactly 1 audit log")

		auditLogFA := getAuditLogByEventID(t, infra.financialAccounting.db, tenantID, "evt_bounded_fa_001")
		assert.Equal(t, "ledger_postings", auditLogFA["table_name"])

		// Verify position-keeping database has no events
		countPK := getAuditLogCount(t, infra.positionKeeping.db, tenantID)
		assert.Equal(t, int64(0), countPK, "position-keeping DB should have 0 audit logs")
	})

	t.Run("verify_separate_database_connections", func(t *testing.T) {
		// Verify each writer is bound to its own database by checking connection pools
		sqlDB_CA, err := infra.currentAccount.db.DB()
		require.NoError(t, err)
		stats_CA := sqlDB_CA.Stats()
		assert.GreaterOrEqual(t, stats_CA.MaxOpenConnections, 1, "current-account should have connections")

		sqlDB_FA, err := infra.financialAccounting.db.DB()
		require.NoError(t, err)
		stats_FA := sqlDB_FA.Stats()
		assert.GreaterOrEqual(t, stats_FA.MaxOpenConnections, 1, "financial-accounting should have connections")

		// Separate connection pools prove bounded context isolation
		assert.NotEqual(t, sqlDB_CA, sqlDB_FA, "databases should be separate instances")
	})
}

// =============================================================================
// Scenario 3: Independent Failure Scenarios
// =============================================================================

func TestIndependentFailureScenarios(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	infra := setupTestInfra(t)
	ctx := context.Background()

	tenantID, err := tenant.NewTenantID("tenant_failure")
	require.NoError(t, err)

	// Create tenant schemas in current-account and position-keeping only
	createTenantSchema(t, infra.currentAccount.db, tenantID)
	createTenantSchema(t, infra.positionKeeping.db, tenantID)

	// Do NOT create schema in financial-accounting (simulating failure)

	ctxWithTenant := tenant.WithTenant(ctx, tenantID)

	t.Run("failure_in_one_service_does_not_block_others", func(t *testing.T) {
		// Write to current-account (should succeed)
		err := infra.currentAccount.writer.WriteAuditEvent(ctxWithTenant, &auditv1.AuditEvent{
			EventId:   "evt_fail_ca_001",
			TableName: "accounts",
			Operation: auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
			RecordId:  "acc_001",
			NewValues: `{"balance": "100.00"}`,
			Timestamp: timestamppb.Now(),
		})
		require.NoError(t, err)

		// Write to financial-accounting (will fail - schema doesn't exist)
		err = infra.financialAccounting.writer.WriteAuditEvent(ctxWithTenant, &auditv1.AuditEvent{
			EventId:   "evt_fail_fa_001",
			TableName: "ledger_postings",
			Operation: auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
			RecordId:  "lp_001",
			NewValues: `{"amount": "100.00"}`,
			Timestamp: timestamppb.Now(),
		})
		assert.Error(t, err, "write should fail when schema doesn't exist")

		// Write to position-keeping (should succeed)
		err = infra.positionKeeping.writer.WriteAuditEvent(ctxWithTenant, &auditv1.AuditEvent{
			EventId:   "evt_fail_pk_001",
			TableName: "positions",
			Operation: auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
			RecordId:  "pos_001",
			NewValues: `{"balance": "100.00"}`,
			Timestamp: timestamppb.Now(),
		})
		require.NoError(t, err)

		// Verify current-account succeeded
		countCA := getAuditLogCount(t, infra.currentAccount.db, tenantID)
		assert.Equal(t, int64(1), countCA, "current-account should have 1 audit log despite FA failure")

		// Verify position-keeping succeeded
		countPK := getAuditLogCount(t, infra.positionKeeping.db, tenantID)
		assert.Equal(t, int64(1), countPK, "position-keeping should have 1 audit log despite FA failure")
	})
}

// =============================================================================
// Scenario 4: Audit Trail Completeness
// =============================================================================

func TestAuditTrailCompleteness(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	infra := setupTestInfra(t)
	ctx := context.Background()

	tenantID, err := tenant.NewTenantID("tenant_complete")
	require.NoError(t, err)

	// Create tenant schemas in all databases
	createTenantSchema(t, infra.currentAccount.db, tenantID)
	createTenantSchema(t, infra.financialAccounting.db, tenantID)
	createTenantSchema(t, infra.positionKeeping.db, tenantID)

	ctxWithTenant := tenant.WithTenant(ctx, tenantID)

	t.Run("complete_audit_trail_across_all_services", func(t *testing.T) {
		// Simulate a cross-service transaction
		txnID := "txn_complete_001"

		err := infra.currentAccount.writer.WriteAuditEvent(ctxWithTenant, &auditv1.AuditEvent{
			EventId:       "evt_complete_ca_001",
			TableName:     "accounts",
			Operation:     auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
			RecordId:      "acc_complete_001",
			NewValues:     `{"balance": "500.00"}`,
			TransactionId: txnID,
			Timestamp:     timestamppb.Now(),
		})
		require.NoError(t, err)

		err = infra.financialAccounting.writer.WriteAuditEvent(ctxWithTenant, &auditv1.AuditEvent{
			EventId:       "evt_complete_fa_001",
			TableName:     "ledger_postings",
			Operation:     auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
			RecordId:      "lp_complete_001",
			NewValues:     `{"amount": "500.00"}`,
			TransactionId: txnID,
			Timestamp:     timestamppb.Now(),
		})
		require.NoError(t, err)

		err = infra.positionKeeping.writer.WriteAuditEvent(ctxWithTenant, &auditv1.AuditEvent{
			EventId:       "evt_complete_pk_001",
			TableName:     "positions",
			Operation:     auditv1.AuditOperation_AUDIT_OPERATION_UPDATE,
			RecordId:      "pos_complete_001",
			OldValues:     `{"balance": "0.00"}`,
			NewValues:     `{"balance": "500.00"}`,
			TransactionId: txnID,
			Timestamp:     timestamppb.Now(),
		})
		require.NoError(t, err)

		// Verify each service has the audit entry
		countCA := getAuditLogCount(t, infra.currentAccount.db, tenantID)
		assert.Equal(t, int64(1), countCA)

		countFA := getAuditLogCount(t, infra.financialAccounting.db, tenantID)
		assert.Equal(t, int64(1), countFA)

		countPK := getAuditLogCount(t, infra.positionKeeping.db, tenantID)
		assert.Equal(t, int64(1), countPK)

		// Verify transaction IDs match
		auditCA := getAuditLogByEventID(t, infra.currentAccount.db, tenantID, "evt_complete_ca_001")
		assert.Equal(t, txnID, auditCA["transaction_id"])

		auditFA := getAuditLogByEventID(t, infra.financialAccounting.db, tenantID, "evt_complete_fa_001")
		assert.Equal(t, txnID, auditFA["transaction_id"])

		auditPK := getAuditLogByEventID(t, infra.positionKeeping.db, tenantID, "evt_complete_pk_001")
		assert.Equal(t, txnID, auditPK["transaction_id"])
	})

	t.Run("query_complete_audit_trail", func(t *testing.T) {
		type AuditEntry struct {
			Service       string
			EventID       string
			TableName     string
			TransactionID string
		}

		var trail []AuditEntry

		// Query each service database
		for _, svc := range []struct {
			name string
			db   *gorm.DB
		}{
			{"current-account", infra.currentAccount.db},
			{"financial-accounting", infra.financialAccounting.db},
			{"position-keeping", infra.positionKeeping.db},
		} {
			var rows []map[string]interface{}
			err := svc.db.Raw(
				fmt.Sprintf("SELECT event_id, table_name, transaction_id FROM %s.audit_log", tenantID.SchemaName()),
			).Scan(&rows).Error
			require.NoError(t, err)

			for _, row := range rows {
				trail = append(trail, AuditEntry{
					Service:       svc.name,
					EventID:       safeString(row["event_id"]),
					TableName:     safeString(row["table_name"]),
					TransactionID: safeString(row["transaction_id"]),
				})
			}
		}

		// Verify complete trail
		assert.Len(t, trail, 3, "should have complete audit trail across 3 services")

		// Verify all entries share transaction ID
		txnID := trail[0].TransactionID
		for _, entry := range trail {
			assert.Equal(t, txnID, entry.TransactionID)
		}

		// Verify entries from all 3 services
		services := make(map[string]bool)
		for _, entry := range trail {
			services[entry.Service] = true
		}
		assert.Len(t, services, 3)
	})
}

// safeString safely converts interface to string
func safeString(v interface{}) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// =============================================================================
// Scenario 5: Idempotency
// =============================================================================

func TestIdempotency(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	infra := setupTestInfra(t)
	ctx := context.Background()

	tenantID, err := tenant.NewTenantID("tenant_idem")
	require.NoError(t, err)

	createTenantSchema(t, infra.currentAccount.db, tenantID)
	ctxWithTenant := tenant.WithTenant(ctx, tenantID)

	t.Run("duplicate_writes_are_idempotent", func(t *testing.T) {
		event := &auditv1.AuditEvent{
			EventId:   "evt_idem_001",
			TableName: "accounts",
			Operation: auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
			RecordId:  "acc_001",
			NewValues: `{"balance": "100.00"}`,
			Timestamp: timestamppb.Now(),
		}

		// Write same event 3 times
		err := infra.currentAccount.writer.WriteAuditEvent(ctxWithTenant, event)
		require.NoError(t, err)

		err = infra.currentAccount.writer.WriteAuditEvent(ctxWithTenant, event)
		require.NoError(t, err, "duplicate write should not error")

		err = infra.currentAccount.writer.WriteAuditEvent(ctxWithTenant, event)
		require.NoError(t, err, "third write should not error")

		// Verify only 1 entry exists
		count := getAuditLogCount(t, infra.currentAccount.db, tenantID)
		assert.Equal(t, int64(1), count, "idempotent writes should result in 1 entry")
	})
}
