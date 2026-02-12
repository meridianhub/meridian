package persistence_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reconciliation/adapters/persistence"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	if os.Getenv("INTEGRATION_TEST") == "" && testing.Short() {
		t.Skip("Skipping integration test (set INTEGRATION_TEST=1 or remove -short)")
	}

	db, cleanup := testdb.SetupCockroachDB(t, nil)

	// Create tenant schema and tables
	tid := tenant.TenantID("test-tenant-01")
	schemaName := tid.SchemaName()
	quoted := fmt.Sprintf("%q", schemaName)

	err := db.Exec("CREATE SCHEMA IF NOT EXISTS " + quoted).Error
	require.NoError(t, err)

	// Run migrations in the tenant schema
	migrationSQL := `
		SET search_path TO ` + quoted + `, public;

		CREATE TABLE IF NOT EXISTS "settlement_run" (
			"id" uuid NOT NULL DEFAULT gen_random_uuid(),
			"created_at" timestamptz NOT NULL DEFAULT now(),
			"updated_at" timestamptz NOT NULL DEFAULT now(),
			"run_id" uuid NOT NULL,
			"account_id" character varying(34) NOT NULL,
			"scope" character varying(20) NOT NULL DEFAULT 'ACCOUNT',
			"settlement_type" character varying(20) NOT NULL DEFAULT 'DAILY',
			"status" character varying(20) NOT NULL DEFAULT 'PENDING',
			"period_start" timestamptz NOT NULL,
			"period_end" timestamptz NOT NULL,
			"initiated_by" character varying(100) NOT NULL,
			"completed_at" timestamptz NULL,
			"variance_count" integer NOT NULL DEFAULT 0,
			"failure_reason" text NULL,
			"attributes" jsonb NULL,
			"version" bigint NOT NULL DEFAULT 1,
			PRIMARY KEY ("id")
		);
		CREATE UNIQUE INDEX IF NOT EXISTS "idx_sr_run_id" ON "settlement_run" ("run_id");

		CREATE TABLE IF NOT EXISTS "settlement_snapshot" (
			"id" uuid NOT NULL DEFAULT gen_random_uuid(),
			"created_at" timestamptz NOT NULL DEFAULT now(),
			"snapshot_id" uuid NOT NULL,
			"run_id" uuid NOT NULL REFERENCES "settlement_run" ("id") ON DELETE CASCADE,
			"account_id" character varying(34) NOT NULL,
			"instrument_code" character varying(20) NOT NULL,
			"expected_balance" decimal(38, 18) NOT NULL,
			"actual_balance" decimal(38, 18) NOT NULL,
			"variance_amount" decimal(38, 18) NOT NULL,
			"source_system" character varying(100) NOT NULL,
			"attributes" jsonb NULL,
			"captured_at" timestamptz NOT NULL,
			PRIMARY KEY ("id")
		);
		CREATE UNIQUE INDEX IF NOT EXISTS "idx_ss_snap_id" ON "settlement_snapshot" ("snapshot_id");

		CREATE TABLE IF NOT EXISTS "variance" (
			"id" uuid NOT NULL DEFAULT gen_random_uuid(),
			"created_at" timestamptz NOT NULL DEFAULT now(),
			"updated_at" timestamptz NOT NULL DEFAULT now(),
			"variance_id" uuid NOT NULL,
			"run_id" uuid NOT NULL REFERENCES "settlement_run" ("id") ON DELETE CASCADE,
			"snapshot_id" uuid NOT NULL REFERENCES "settlement_snapshot" ("id") ON DELETE CASCADE,
			"account_id" character varying(34) NOT NULL,
			"instrument_code" character varying(20) NOT NULL,
			"expected_amount" decimal(38, 18) NOT NULL,
			"actual_amount" decimal(38, 18) NOT NULL,
			"variance_amount" decimal(38, 18) NOT NULL,
			"value_delta" decimal(38, 18) NOT NULL DEFAULT 0,
			"currency" character varying(10) NOT NULL DEFAULT '',
			"reason" character varying(30) NOT NULL,
			"status" character varying(20) NOT NULL DEFAULT 'OPEN',
			"resolution_note" text NULL,
			"resolved_by" character varying(100) NULL,
			"resolved_at" timestamptz NULL,
			"attributes" jsonb NULL,
			PRIMARY KEY ("id")
		);
		CREATE UNIQUE INDEX IF NOT EXISTS "idx_v_var_id" ON "variance" ("variance_id");

		CREATE TABLE IF NOT EXISTS "dispute" (
			"id" uuid NOT NULL DEFAULT gen_random_uuid(),
			"created_at" timestamptz NOT NULL DEFAULT now(),
			"updated_at" timestamptz NOT NULL DEFAULT now(),
			"dispute_id" uuid NOT NULL,
			"variance_id" uuid NOT NULL REFERENCES "variance" ("id") ON DELETE CASCADE,
			"run_id" uuid NOT NULL REFERENCES "settlement_run" ("id") ON DELETE CASCADE,
			"account_id" character varying(34) NOT NULL,
			"status" character varying(20) NOT NULL DEFAULT 'OPEN',
			"reason" text NOT NULL,
			"resolution" text NULL,
			"raised_by" character varying(100) NOT NULL,
			"resolved_by" character varying(100) NULL,
			"resolved_at" timestamptz NULL,
			"attributes" jsonb NULL,
			PRIMARY KEY ("id")
		);
		CREATE UNIQUE INDEX IF NOT EXISTS "idx_d_disp_id" ON "dispute" ("dispute_id");

		CREATE TABLE IF NOT EXISTS "balance_assertion" (
			"id" uuid NOT NULL DEFAULT gen_random_uuid(),
			"created_at" timestamptz NOT NULL DEFAULT now(),
			"updated_at" timestamptz NOT NULL DEFAULT now(),
			"assertion_id" uuid NOT NULL,
			"run_id" uuid NULL,
			"account_id" character varying(34) NOT NULL,
			"instrument_code" character varying(20) NOT NULL,
			"expression" text NOT NULL,
			"expected_balance" decimal(38, 18) NOT NULL,
			"actual_balance" decimal(38, 18) NOT NULL DEFAULT 0,
			"status" character varying(20) NOT NULL DEFAULT 'PENDING',
			"failure_reason" text NULL,
			"override_reason" text NULL,
			"attributes" jsonb NULL,
			"metadata" jsonb NULL,
			"asserted_at" timestamptz NULL,
			"version" bigint NOT NULL DEFAULT 1,
			PRIMARY KEY ("id")
		);
		CREATE UNIQUE INDEX IF NOT EXISTS "idx_ba_assertion_id" ON "balance_assertion" ("assertion_id");
		CREATE INDEX IF NOT EXISTS "idx_ba_run_id" ON "balance_assertion" ("run_id");
		CREATE INDEX IF NOT EXISTS "idx_ba_account_id" ON "balance_assertion" ("account_id");
		CREATE INDEX IF NOT EXISTS "idx_ba_instrument_code" ON "balance_assertion" ("instrument_code");
		CREATE INDEX IF NOT EXISTS "idx_ba_status" ON "balance_assertion" ("status");

		SET search_path TO public;
	`
	err = db.Exec(migrationSQL).Error
	require.NoError(t, err)

	return db, cleanup
}

func tenantCtx() context.Context {
	tid := tenant.TenantID("test-tenant-01")
	return tenant.WithTenant(context.Background(), tid)
}

// seedRunAndVariance creates prerequisite settlement_run, snapshot, and variance records.
func seedRunAndVariance(t *testing.T, db *gorm.DB, runID, snapshotID, varianceID uuid.UUID) {
	t.Helper()
	ctx := tenantCtx()
	tid := tenant.TenantID("test-tenant-01")
	quoted := fmt.Sprintf("%q", tid.SchemaName())

	// Insert settlement_run
	err := db.WithContext(ctx).Exec(
		fmt.Sprintf(`INSERT INTO %s."settlement_run" (run_id, account_id, scope, settlement_type, period_start, period_end, initiated_by) VALUES (?, 'ACC-001', 'ACCOUNT', 'DAILY', NOW() - INTERVAL '1 day', NOW(), 'system')`, quoted),
		runID,
	).Error
	require.NoError(t, err)

	// Insert settlement_snapshot
	err = db.WithContext(ctx).Exec(
		fmt.Sprintf(`INSERT INTO %s."settlement_snapshot" (snapshot_id, run_id, account_id, instrument_code, expected_balance, actual_balance, variance_amount, source_system, captured_at) SELECT ?, sr.id, 'ACC-001', 'GBP', 100.00, 90.00, -10.00, 'test', NOW() FROM %s."settlement_run" sr WHERE sr.run_id = ?`, quoted, quoted),
		snapshotID, runID,
	).Error
	require.NoError(t, err)

	// Insert variance
	err = db.WithContext(ctx).Exec(
		fmt.Sprintf(`INSERT INTO %s."variance" (variance_id, run_id, snapshot_id, account_id, instrument_code, expected_amount, actual_amount, variance_amount, reason) SELECT ?, sr.id, ss.id, 'ACC-001', 'GBP', 100.00, 90.00, -10.00, 'AMOUNT_MISMATCH' FROM %s."settlement_run" sr JOIN %s."settlement_snapshot" ss ON ss.run_id = sr.id WHERE sr.run_id = ? AND ss.snapshot_id = ?`, quoted, quoted, quoted),
		varianceID, runID, snapshotID,
	).Error
	require.NoError(t, err)
}

func TestDisputeRepository_Create(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewDisputeRepository(db)
	ctx := tenantCtx()

	runID := uuid.New()
	snapshotID := uuid.New()
	varianceID := uuid.New()
	seedRunAndVariance(t, db, runID, snapshotID, varianceID)

	dispute, err := domain.NewDispute(varianceID, runID, "ACC-001", "Amount mismatch", "user-1")
	require.NoError(t, err)

	err = repo.Create(ctx, dispute)
	require.NoError(t, err)

	// Verify retrieval
	found, err := repo.FindByID(ctx, dispute.DisputeID)
	require.NoError(t, err)
	assert.Equal(t, dispute.DisputeID, found.DisputeID)
	assert.Equal(t, "ACC-001", found.AccountID)
	assert.Equal(t, domain.DisputeStatusOpen, found.Status)
	assert.Equal(t, "Amount mismatch", found.Reason)
	assert.Equal(t, "user-1", found.RaisedBy)
}

func TestDisputeRepository_Update(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewDisputeRepository(db)
	ctx := tenantCtx()

	runID := uuid.New()
	snapshotID := uuid.New()
	varianceID := uuid.New()
	seedRunAndVariance(t, db, runID, snapshotID, varianceID)

	dispute, err := domain.NewDispute(varianceID, runID, "ACC-001", "Reason", "user-1")
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, dispute))

	// Resolve the dispute
	require.NoError(t, dispute.Resolve("Fixed", "admin"))
	err = repo.Update(ctx, dispute)
	require.NoError(t, err)

	// Verify update persisted
	found, err := repo.FindByID(ctx, dispute.DisputeID)
	require.NoError(t, err)
	assert.Equal(t, domain.DisputeStatusResolved, found.Status)
	assert.Equal(t, "Fixed", found.Resolution)
	assert.Equal(t, "admin", found.ResolvedBy)
	assert.NotNil(t, found.ResolvedAt)
}

func TestDisputeRepository_FindByVarianceID(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewDisputeRepository(db)
	ctx := tenantCtx()

	runID := uuid.New()
	snapshotID := uuid.New()
	varianceID := uuid.New()
	seedRunAndVariance(t, db, runID, snapshotID, varianceID)

	// Create two disputes for the same variance
	d1, err := domain.NewDispute(varianceID, runID, "ACC-001", "First dispute", "user-1")
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, d1))

	d2, err := domain.NewDispute(varianceID, runID, "ACC-001", "Second dispute", "user-2")
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, d2))

	found, err := repo.FindByVarianceID(ctx, varianceID)
	require.NoError(t, err)
	assert.Len(t, found, 2)
}

func TestDisputeRepository_List(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewDisputeRepository(db)
	ctx := tenantCtx()

	runID := uuid.New()
	snapshotID := uuid.New()
	varianceID := uuid.New()
	seedRunAndVariance(t, db, runID, snapshotID, varianceID)

	d1, err := domain.NewDispute(varianceID, runID, "ACC-001", "Reason 1", "user-1")
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, d1))

	d2, err := domain.NewDispute(varianceID, runID, "ACC-002", "Reason 2", "user-2")
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, d2))

	t.Run("list all", func(t *testing.T) {
		found, err := repo.List(ctx, domain.DisputeFilter{})
		require.NoError(t, err)
		assert.Len(t, found, 2)
	})

	t.Run("filter by account", func(t *testing.T) {
		accountID := "ACC-001"
		found, err := repo.List(ctx, domain.DisputeFilter{AccountID: &accountID})
		require.NoError(t, err)
		assert.Len(t, found, 1)
		assert.Equal(t, "ACC-001", found[0].AccountID)
	})

	t.Run("filter by status", func(t *testing.T) {
		s := domain.DisputeStatusOpen
		found, err := repo.List(ctx, domain.DisputeFilter{Status: &s})
		require.NoError(t, err)
		assert.Len(t, found, 2)
	})
}

func TestDisputeRepository_NotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewDisputeRepository(db)
	ctx := tenantCtx()

	_, err := repo.FindByID(ctx, uuid.New())
	assert.ErrorIs(t, err, domain.ErrNotFound)
}
