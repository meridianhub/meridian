package persistence_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/testcontainers/testcontainers-go/modules/cockroachdb"
	gormpg "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// sharedDB holds the single CockroachDB connection shared across all tests in this package.
var sharedDB *gorm.DB

// allTables lists every table created in the tenant schema, in deletion order (children first).
var allTables = []string{
	"imbalance_trend",
	"dispute",
	"variance",
	"settlement_snapshot",
	"balance_assertion",
	"settlement_run",
}

func TestMain(m *testing.M) {
	if os.Getenv("INTEGRATION_TEST") == "" && isShortMode() {
		os.Exit(m.Run())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	crdbContainer, err := cockroachdb.Run(ctx,
		"cockroachdb/cockroach:v24.3.0",
		cockroachdb.WithDatabase("test_db"),
		cockroachdb.WithUser("root"),
		cockroachdb.WithInsecure(),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start CockroachDB container: %v\n", err)
		os.Exit(1)
	}

	connConfig, err := crdbContainer.ConnectionConfig(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get connection config: %v\n", err)
		_ = crdbContainer.Terminate(context.Background())
		os.Exit(1)
	}

	sharedDB, err = gorm.Open(gormpg.Open(connConfig.ConnString()), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to database: %v\n", err)
		_ = crdbContainer.Terminate(context.Background())
		os.Exit(1)
	}

	if err := runMigrations(sharedDB); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to run migrations: %v\n", err)
		_ = crdbContainer.Terminate(context.Background())
		os.Exit(1)
	}

	code := m.Run()

	sqlDB, _ := sharedDB.DB()
	if sqlDB != nil {
		_ = sqlDB.Close()
	}
	_ = crdbContainer.Terminate(context.Background())
	os.Exit(code)
}

func isShortMode() bool {
	for _, arg := range os.Args {
		if arg == "-test.short" || arg == "--test.short" {
			return true
		}
	}
	return false
}

// runMigrations creates the tenant schema and all tables once for the shared container.
func runMigrations(db *gorm.DB) error {
	tid := tenant.TenantID("test-tenant-01")
	schemaName := tid.SchemaName()
	quoted := fmt.Sprintf("%q", schemaName)

	migrationSQL := `
		CREATE SCHEMA IF NOT EXISTS ` + quoted + `;
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
			"last_completed_phase" character varying(30) NULL,
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
			"variance_id" uuid NOT NULL,
			"run_id" uuid NOT NULL,
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

		CREATE TABLE IF NOT EXISTS "imbalance_trend" (
			"id" uuid NOT NULL DEFAULT gen_random_uuid(),
			"created_at" timestamptz NOT NULL DEFAULT now(),
			"updated_at" timestamptz NOT NULL DEFAULT now(),
			"trend_id" uuid NOT NULL,
			"instrument_code" character varying(20) NOT NULL,
			"first_detected_at" timestamptz NOT NULL,
			"last_detected_at" timestamptz NOT NULL,
			"consecutive_days" integer NOT NULL DEFAULT 0,
			"total_occurrences" integer NOT NULL DEFAULT 0,
			"last_imbalance_amount" decimal(38, 18) NOT NULL,
			"last_assertion_id" uuid NULL,
			"resolved_at" timestamptz NULL,
			"metadata" jsonb NULL,
			PRIMARY KEY ("id")
		);
		CREATE UNIQUE INDEX IF NOT EXISTS "idx_it_trend_id" ON "imbalance_trend" ("trend_id");
		CREATE UNIQUE INDEX IF NOT EXISTS "idx_it_instrument_code" ON "imbalance_trend" ("instrument_code");

		SET search_path TO public;
	`
	return db.Exec(migrationSQL).Error
}

// truncateAllTables removes all data from every table in the tenant schema,
// preserving the schema and table structure for the next test.
func truncateAllTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	tid := tenant.TenantID("test-tenant-01")
	quoted := fmt.Sprintf("%q", tid.SchemaName())

	for _, table := range allTables {
		err := db.Exec(fmt.Sprintf(`DELETE FROM %s.%q`, quoted, table)).Error
		if err != nil {
			t.Fatalf("Failed to truncate %s: %v", table, err)
		}
	}
}
