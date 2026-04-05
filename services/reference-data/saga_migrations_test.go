package migrations_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// sagaTestContainer holds the test database container and connection pool for saga tests
type sagaTestContainer struct {
	container *postgres.PostgresContainer
	pool      *pgxpool.Pool
}

// setupSagaTestContainer creates a PostgreSQL testcontainer with all migrations applied
func setupSagaTestContainer(t *testing.T) *sagaTestContainer {
	t.Helper()

	ctx := context.Background()

	// Create PostgreSQL container
	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("test_reference_data"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second)),
	)
	require.NoError(t, err)

	// Get connection string
	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	// Create connection pool
	poolConfig, err := pgxpool.ParseConfig(connStr)
	require.NoError(t, err)

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	require.NoError(t, err)

	// Apply all migrations in order
	applySagaMigrations(t, pool)

	return &sagaTestContainer{
		container: pgContainer,
		pool:      pool,
	}
}

// cleanup closes the pool and terminates the container
func (tc *sagaTestContainer) cleanup(t *testing.T) {
	t.Helper()
	ctx := context.Background()

	if tc.pool != nil {
		tc.pool.Close()
	}

	if tc.container != nil {
		require.NoError(t, tc.container.Terminate(ctx))
	}
}

// applySagaMigrations reads and executes all migration SQL files in order
func applySagaMigrations(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()

	// Apply migrations in order
	migrations := []string{
		"20260124000001_saga_definitions.sql",
		"20260124000002_saga_references.sql",
		"20260125000001_platform_saga_definition.sql",
		"20260125000002_extend_saga_definition_platform_ref.sql",
		"20260125000003_platform_ref_index.sql",
		"20260127000001_fix_platform_saga_unique_constraint.sql",
	}

	for _, migration := range migrations {
		migrationPath := filepath.Join("migrations", migration)
		migrationSQL, err := os.ReadFile(migrationPath)
		require.NoError(t, err, "failed to read migration file: %s", migration)

		// CockroachDB-specific DDL needs adaptation for PostgreSQL test containers.
		sql := strings.ReplaceAll(string(migrationSQL),
			`DROP INDEX IF EXISTS "public"."uq_platform_saga_definition_name" CASCADE`,
			`ALTER TABLE "public"."platform_saga_definition" DROP CONSTRAINT IF EXISTS "uq_platform_saga_definition_name"`,
		)

		_, err = pool.Exec(ctx, sql)
		require.NoError(t, err, "failed to apply migration: %s", migration)
	}
}

// insertSaga is a helper to insert a saga definition for testing
func insertSaga(ctx context.Context, t *testing.T, pool *pgxpool.Pool, name string, version int, script string, status string) uuid.UUID {
	t.Helper()

	id := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO saga_definition (id, name, version, script, status)
		VALUES ($1, $2, $3, $4, $5)
	`, id, name, version, script, status)
	require.NoError(t, err)

	return id
}

// insertSagaWithExpressions inserts a saga with preconditions expression
func insertSagaWithExpressions(ctx context.Context, t *testing.T, pool *pgxpool.Pool, name string, version int, script string, status string, preconditions string) uuid.UUID {
	t.Helper()

	id := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO saga_definition (id, name, version, script, status, preconditions_expression)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, id, name, version, script, status, preconditions)
	require.NoError(t, err)

	return id
}

func TestSagaMigration_AppliesCleanly(t *testing.T) {
	tc := setupSagaTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	// Verify saga_definition table exists
	var tableName string
	err := tc.pool.QueryRow(ctx, `
		SELECT table_name
		FROM information_schema.tables
		WHERE table_name = 'saga_definition'
	`).Scan(&tableName)
	require.NoError(t, err)
	assert.Equal(t, "saga_definition", tableName)

	// Verify all expected columns exist
	expectedColumns := []string{
		"id", "name", "version", "script", "status", "is_system",
		"preconditions_expression", "display_name", "description",
		"created_at", "updated_at", "activated_at", "deprecated_at", "successor_id",
		"platform_ref", "override_reason", "platform_version_at_override",
	}

	rows, err := tc.pool.Query(ctx, `
		SELECT column_name
		FROM information_schema.columns
		WHERE table_name = 'saga_definition'
	`)
	require.NoError(t, err)
	defer rows.Close()

	var columns []string
	for rows.Next() {
		var col string
		require.NoError(t, rows.Scan(&col))
		columns = append(columns, col)
	}

	for _, expected := range expectedColumns {
		assert.Contains(t, columns, expected, "missing column: %s", expected)
	}

	// NOTE: Lifecycle triggers removed for CockroachDB compatibility.
	// Enforcement now handled at Go application layer.
}

func TestSagaMigration_ReferenceTableAppliesCleanly(t *testing.T) {
	tc := setupSagaTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	// Verify saga_reference table exists
	var tableName string
	err := tc.pool.QueryRow(ctx, `
		SELECT table_name
		FROM information_schema.tables
		WHERE table_name = 'saga_reference'
	`).Scan(&tableName)
	require.NoError(t, err)
	assert.Equal(t, "saga_reference", tableName)

	// Verify all expected columns exist
	expectedColumns := []string{
		"saga_definition_id", "reference_type", "reference_key",
		"instrument_code", "attribute_key", "line_number", "extracted_at",
	}

	rows, err := tc.pool.Query(ctx, `
		SELECT column_name
		FROM information_schema.columns
		WHERE table_name = 'saga_reference'
	`)
	require.NoError(t, err)
	defer rows.Close()

	var columns []string
	for rows.Next() {
		var col string
		require.NoError(t, rows.Scan(&col))
		columns = append(columns, col)
	}

	for _, expected := range expectedColumns {
		assert.Contains(t, columns, expected, "missing column: %s", expected)
	}
}

func TestSagaMigration_UniqueConstraint(t *testing.T) {
	tc := setupSagaTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	// Insert first saga
	insertSaga(ctx, t, tc.pool, "withdrawal", 1, "def posting_rules(ctx): pass", "DRAFT")

	// Attempt to insert duplicate name+version - should fail
	_, err := tc.pool.Exec(ctx, `
		INSERT INTO saga_definition (id, name, version, script, status)
		VALUES ($1, 'withdrawal', 1, 'def posting_rules(ctx): pass', 'DRAFT')
	`, uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "uq_saga_definition_name_version")

	// Insert same name with different version - should succeed
	_, err = tc.pool.Exec(ctx, `
		INSERT INTO saga_definition (id, name, version, script, status)
		VALUES ($1, 'withdrawal', 2, 'def posting_rules(ctx): pass', 'DRAFT')
	`, uuid.New())
	require.NoError(t, err)

	// Insert different name with same version - should succeed
	_, err = tc.pool.Exec(ctx, `
		INSERT INTO saga_definition (id, name, version, script, status)
		VALUES ($1, 'deposit', 1, 'def posting_rules(ctx): pass', 'DRAFT')
	`, uuid.New())
	require.NoError(t, err)
}

func TestSagaMigration_CheckConstraints(t *testing.T) {
	tc := setupSagaTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	t.Run("status constraint accepts valid values", func(t *testing.T) {
		validStatuses := []string{"DRAFT", "ACTIVE", "DEPRECATED"}
		for i, status := range validStatuses {
			name := "TEST_STATUS_" + status
			_, err := tc.pool.Exec(ctx, `
				INSERT INTO saga_definition (id, name, version, script, status)
				VALUES ($1, $2, $3, 'def posting_rules(ctx): pass', $4)
			`, uuid.New(), name, i+1, status)
			require.NoError(t, err, "status %s should be valid", status)
		}
	})

	t.Run("status constraint rejects invalid values", func(t *testing.T) {
		invalidStatuses := []string{"INVALID", "draft", "Active", "", "PENDING"}
		for _, status := range invalidStatuses {
			_, err := tc.pool.Exec(ctx, `
				INSERT INTO saga_definition (id, name, version, script, status)
				VALUES ($1, 'INVALID_STATUS', 1, 'def posting_rules(ctx): pass', $2)
			`, uuid.New(), status)
			require.Error(t, err, "status %s should be rejected", status)
			assert.Contains(t, err.Error(), "chk_saga_definition_status")
		}
	})

	t.Run("script length constraint accepts up to 64KB", func(t *testing.T) {
		// Exactly 65536 bytes should be accepted
		validScript := strings.Repeat("x", 65536)
		_, err := tc.pool.Exec(ctx, `
			INSERT INTO saga_definition (id, name, version, script, status)
			VALUES ($1, 'VALID_SCRIPT', 1, $2, 'DRAFT')
		`, uuid.New(), validScript)
		require.NoError(t, err)
	})

	t.Run("script length constraint rejects over 64KB", func(t *testing.T) {
		// 65537 bytes should be rejected
		invalidScript := strings.Repeat("x", 65537)
		_, err := tc.pool.Exec(ctx, `
			INSERT INTO saga_definition (id, name, version, script, status)
			VALUES ($1, 'INVALID_SCRIPT', 1, $2, 'DRAFT')
		`, uuid.New(), invalidScript)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "chk_saga_definition_script_length")
	})

	t.Run("preconditions_expression length constraint accepts up to 4KB", func(t *testing.T) {
		// Exactly 4096 bytes should be accepted
		validExpr := strings.Repeat("x", 4096)
		_, err := tc.pool.Exec(ctx, `
			INSERT INTO saga_definition (id, name, version, script, status, preconditions_expression)
			VALUES ($1, 'VALID_PRECOND', 1, 'def posting_rules(ctx): pass', 'DRAFT', $2)
		`, uuid.New(), validExpr)
		require.NoError(t, err)

		// NULL should be accepted
		_, err = tc.pool.Exec(ctx, `
			INSERT INTO saga_definition (id, name, version, script, status, preconditions_expression)
			VALUES ($1, 'NULL_PRECOND', 1, 'def posting_rules(ctx): pass', 'DRAFT', NULL)
		`, uuid.New())
		require.NoError(t, err)
	})

	t.Run("preconditions_expression length constraint rejects over 4KB", func(t *testing.T) {
		// 4097 bytes should be rejected
		invalidExpr := strings.Repeat("x", 4097)
		_, err := tc.pool.Exec(ctx, `
			INSERT INTO saga_definition (id, name, version, script, status, preconditions_expression)
			VALUES ($1, 'INVALID_PRECOND', 1, 'def posting_rules(ctx): pass', 'DRAFT', $2)
		`, uuid.New(), invalidExpr)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "chk_saga_definition_preconditions_length")
	})
}

func TestSagaMigration_ReferenceTypeConstraint(t *testing.T) {
	tc := setupSagaTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	// Create a saga to reference
	sagaID := insertSaga(ctx, t, tc.pool, "test_saga", 1, "def posting_rules(ctx): pass", "DRAFT")

	t.Run("reference_type constraint accepts valid values", func(t *testing.T) {
		validTypes := []string{"step_handler", "instrument", "account", "saga", "attribute"}
		for i, refType := range validTypes {
			_, err := tc.pool.Exec(ctx, `
				INSERT INTO saga_reference (saga_definition_id, reference_type, reference_key)
				VALUES ($1, $2, $3)
			`, sagaID, refType, "key_"+string(rune('A'+i)))
			require.NoError(t, err, "reference_type %s should be valid", refType)
		}
	})

	t.Run("reference_type constraint rejects invalid values", func(t *testing.T) {
		invalidTypes := []string{"INVALID", "handler", "Step_Handler", "", "other"}
		for _, refType := range invalidTypes {
			_, err := tc.pool.Exec(ctx, `
				INSERT INTO saga_reference (saga_definition_id, reference_type, reference_key)
				VALUES ($1, $2, 'some_key')
			`, sagaID, refType)
			require.Error(t, err, "reference_type %s should be rejected", refType)
			assert.Contains(t, err.Error(), "chk_saga_reference_type")
		}
	})
}

func TestSagaMigration_ReferenceCascadeDelete(t *testing.T) {
	tc := setupSagaTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	// Create a saga
	sagaID := insertSaga(ctx, t, tc.pool, "cascade_test", 1, "def posting_rules(ctx): pass", "DRAFT")

	// Insert references
	_, err := tc.pool.Exec(ctx, `
		INSERT INTO saga_reference (saga_definition_id, reference_type, reference_key)
		VALUES ($1, 'step_handler', 'position_keeping.initiate_log'),
		       ($1, 'instrument', 'KWH'),
		       ($1, 'account', 'clearing_GBP')
	`, sagaID)
	require.NoError(t, err)

	// Verify references exist
	var count int
	err = tc.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM saga_reference WHERE saga_definition_id = $1
	`, sagaID).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 3, count)

	// Delete the saga
	_, err = tc.pool.Exec(ctx, `DELETE FROM saga_definition WHERE id = $1`, sagaID)
	require.NoError(t, err)

	// Verify references are deleted (CASCADE)
	err = tc.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM saga_reference WHERE saga_definition_id = $1
	`, sagaID).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count, "references should be deleted on cascade")
}

func TestSagaMigration_LifecycleTrigger_DraftAllowsEdits(t *testing.T) {
	tc := setupSagaTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	// Insert a DRAFT saga with preconditions
	id := insertSagaWithExpressions(ctx, t, tc.pool, "DRAFT_EDITABLE", 1, "original_script", "DRAFT", "original_precondition")

	// All fields should be editable in DRAFT status
	editTests := []struct {
		name  string
		query string
		args  []interface{}
	}{
		{
			name:  "update script",
			query: `UPDATE saga_definition SET script = $1 WHERE id = $2`,
			args:  []interface{}{"new_script", id},
		},
		{
			name:  "update preconditions_expression",
			query: `UPDATE saga_definition SET preconditions_expression = $1 WHERE id = $2`,
			args:  []interface{}{"new_precondition", id},
		},
		{
			name:  "update display_name",
			query: `UPDATE saga_definition SET display_name = $1 WHERE id = $2`,
			args:  []interface{}{"New Display Name", id},
		},
		{
			name:  "update description",
			query: `UPDATE saga_definition SET description = $1 WHERE id = $2`,
			args:  []interface{}{"New Description", id},
		},
		{
			name:  "update is_system",
			query: `UPDATE saga_definition SET is_system = $1 WHERE id = $2`,
			args:  []interface{}{true, id},
		},
	}

	for _, tt := range editTests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tc.pool.Exec(ctx, tt.query, tt.args...)
			require.NoError(t, err, "DRAFT saga should allow editing %s", tt.name)
		})
	}
}

// NOTE: TestSagaMigration_LifecycleTrigger_ActiveBlocksScriptChanges removed.
// Script immutability is now enforced at the Go application layer.

// NOTE: TestSagaMigration_LifecycleTrigger_StatusTransitions removed.
// Status transition enforcement is now handled at the Go application layer.

func TestSagaMigration_TimestampDefaults(t *testing.T) {
	tc := setupSagaTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	t.Run("created_at defaults to now on insert", func(t *testing.T) {
		id := insertSaga(ctx, t, tc.pool, "CREATED_TIMESTAMP", 1, "def posting_rules(ctx): pass", "DRAFT")

		var createdAt time.Time
		err := tc.pool.QueryRow(ctx, `SELECT created_at FROM saga_definition WHERE id = $1`, id).Scan(&createdAt)
		require.NoError(t, err)
		assert.False(t, createdAt.IsZero(), "created_at should be populated on insert")
	})

	// NOTE: activated_at, deprecated_at, updated_at, successor validation, and
	// write-once semantics are now handled at the Go application layer.
}

func TestSagaMigration_Indexes(t *testing.T) {
	tc := setupSagaTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	t.Run("verify expected indexes exist", func(t *testing.T) {
		expectedIndexes := []string{
			"idx_saga_definition_name_active",
			"idx_saga_definition_lookup",
			"idx_saga_definition_temporal",
			"idx_saga_definition_successor_id",
			"idx_saga_reference_by_target",
			"idx_saga_reference_by_saga",
			"idx_saga_reference_attribute",
		}

		for _, indexName := range expectedIndexes {
			var exists bool
			err := tc.pool.QueryRow(ctx, `
				SELECT EXISTS(
					SELECT 1 FROM pg_indexes
					WHERE indexname = $1
				)
			`, indexName).Scan(&exists)
			require.NoError(t, err)
			assert.True(t, exists, "index %s should exist", indexName)
		}
	})

	t.Run("verify unique constraint index exists", func(t *testing.T) {
		var exists bool
		err := tc.pool.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM pg_indexes
				WHERE tablename = 'saga_definition'
				AND indexname = 'uq_saga_definition_name_version'
			)
		`).Scan(&exists)
		require.NoError(t, err)
		assert.True(t, exists, "unique constraint index should exist")
	})
}

func TestSagaMigration_SchemaIsolation(t *testing.T) {
	// This test creates multiple schemas to simulate tenant isolation
	tc := setupSagaTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	// Create tenant schemas and apply migrations
	tenants := []string{"tenant_alpha", "tenant_beta", "tenant_gamma"}

	for _, tenant := range tenants {
		quoted := pq.QuoteIdentifier(tenant)
		// Create schema for tenant
		_, err := tc.pool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", quoted))
		require.NoError(t, err)

		// Apply saga migrations to this tenant's schema
		migrations := []string{
			"20260124000001_saga_definitions.sql",
			"20260124000002_saga_references.sql",
		}

		for _, migration := range migrations {
			migrationPath := filepath.Join("migrations", migration)
			migrationSQL, err := os.ReadFile(migrationPath)
			require.NoError(t, err)

			// Set search_path to tenant schema and apply migration
			_, err = tc.pool.Exec(ctx, fmt.Sprintf("SET search_path TO %s", quoted))
			require.NoError(t, err)
			_, err = tc.pool.Exec(ctx, string(migrationSQL))
			require.NoError(t, err)
		}
	}

	t.Run("data inserted in one tenant is not visible in another", func(t *testing.T) {
		// Insert data into tenant_alpha
		_, err := tc.pool.Exec(ctx, `SET search_path TO tenant_alpha`)
		require.NoError(t, err)
		_, err = tc.pool.Exec(ctx, `
			INSERT INTO saga_definition (id, name, version, script, status)
			VALUES ($1, 'ALPHA_ONLY', 1, 'def posting_rules(ctx): pass', 'DRAFT')
		`, uuid.New())
		require.NoError(t, err)

		// Verify it exists in tenant_alpha
		var count int
		err = tc.pool.QueryRow(ctx, `SELECT COUNT(*) FROM saga_definition WHERE name = 'ALPHA_ONLY'`).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count)

		// Verify it does NOT exist in tenant_beta
		_, err = tc.pool.Exec(ctx, `SET search_path TO tenant_beta`)
		require.NoError(t, err)
		err = tc.pool.QueryRow(ctx, `SELECT COUNT(*) FROM saga_definition WHERE name = 'ALPHA_ONLY'`).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 0, count)

		// Verify it does NOT exist in tenant_gamma
		_, err = tc.pool.Exec(ctx, `SET search_path TO tenant_gamma`)
		require.NoError(t, err)
		err = tc.pool.QueryRow(ctx, `SELECT COUNT(*) FROM saga_definition WHERE name = 'ALPHA_ONLY'`).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 0, count)
	})

	t.Run("same name+version can exist in different tenants", func(t *testing.T) {
		// Insert same name+version in each tenant
		for _, tenant := range tenants {
			_, err := tc.pool.Exec(ctx, `SET search_path TO `+tenant)
			require.NoError(t, err)
			_, err = tc.pool.Exec(ctx, `
				INSERT INTO saga_definition (id, name, version, script, status)
				VALUES ($1, 'SHARED_NAME', 1, 'def posting_rules(ctx): pass', 'DRAFT')
			`, uuid.New())
			require.NoError(t, err, "tenant %s should allow inserting SHARED_NAME", tenant)
		}

		// Verify each tenant has exactly one record
		for _, tenant := range tenants {
			_, err := tc.pool.Exec(ctx, `SET search_path TO `+tenant)
			require.NoError(t, err)

			var count int
			err = tc.pool.QueryRow(ctx, `SELECT COUNT(*) FROM saga_definition WHERE name = 'SHARED_NAME'`).Scan(&count)
			require.NoError(t, err)
			assert.Equal(t, 1, count, "tenant %s should have exactly one SHARED_NAME", tenant)
		}
	})

	// NOTE: "trigger functions work independently per tenant" subtest removed.
	// Lifecycle enforcement is now at the Go application layer.
}

func TestSagaMigration_PlatformRefExtension_ColumnsExist(t *testing.T) {
	tc := setupSagaTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	// Verify new columns exist
	expectedColumns := []string{
		"platform_ref",
		"override_reason",
		"platform_version_at_override",
	}

	rows, err := tc.pool.Query(ctx, `
		SELECT column_name
		FROM information_schema.columns
		WHERE table_name = 'saga_definition'
	`)
	require.NoError(t, err)
	defer rows.Close()

	var columns []string
	for rows.Next() {
		var col string
		require.NoError(t, rows.Scan(&col))
		columns = append(columns, col)
	}

	for _, expected := range expectedColumns {
		assert.Contains(t, columns, expected, "missing column: %s", expected)
	}
}

func TestSagaMigration_PlatformRefExtension_ForeignKey(t *testing.T) {
	tc := setupSagaTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	// Create a platform saga definition
	platformID := uuid.New()
	_, err := tc.pool.Exec(ctx, `
		INSERT INTO public.platform_saga_definition (id, name, version, script)
		VALUES ($1, 'platform_withdrawal', '1.0.0', 'def posting_rules(ctx): pass')
	`, platformID)
	require.NoError(t, err)

	// Create a tenant saga referencing the platform saga
	tenantID := uuid.New()
	_, err = tc.pool.Exec(ctx, `
		INSERT INTO saga_definition (id, name, version, script, status, platform_ref)
		VALUES ($1, 'tenant_withdrawal', 1, '', 'DRAFT', $2)
	`, tenantID, platformID)
	require.NoError(t, err)

	// Verify the reference was stored
	var storedPlatformRef uuid.UUID
	err = tc.pool.QueryRow(ctx, `
		SELECT platform_ref FROM saga_definition WHERE id = $1
	`, tenantID).Scan(&storedPlatformRef)
	require.NoError(t, err)
	assert.Equal(t, platformID, storedPlatformRef)
}

func TestSagaMigration_PlatformRefExtension_ForeignKeyOnDeleteSetNull(t *testing.T) {
	tc := setupSagaTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	// Create a platform saga
	platformID := uuid.New()
	_, err := tc.pool.Exec(ctx, `
		INSERT INTO public.platform_saga_definition (id, name, version, script)
		VALUES ($1, 'platform_delete_test', '1.0.0', 'def posting_rules(ctx): pass')
	`, platformID)
	require.NoError(t, err)

	// Create a tenant saga referencing it
	tenantID := uuid.New()
	_, err = tc.pool.Exec(ctx, `
		INSERT INTO saga_definition (id, name, version, script, status, platform_ref)
		VALUES ($1, 'tenant_delete_test', 1, '', 'DRAFT', $2)
	`, tenantID, platformID)
	require.NoError(t, err)

	// Delete the platform saga
	_, err = tc.pool.Exec(ctx, `DELETE FROM public.platform_saga_definition WHERE id = $1`, platformID)
	require.NoError(t, err)

	// Verify the tenant saga's platform_ref is now NULL (ON DELETE SET NULL)
	var platformRef *uuid.UUID
	var script string
	err = tc.pool.QueryRow(ctx, `
		SELECT platform_ref, script FROM saga_definition WHERE id = $1
	`, tenantID).Scan(&platformRef, &script)
	require.NoError(t, err)
	assert.Nil(t, platformRef, "platform_ref should be NULL after platform saga deletion")
	assert.Equal(t, "", script, "script should still be empty")

	// This creates an "orphaned" saga state (no platform_ref, no script)
	// Application logic should handle these cases by either:
	// 1. Providing a custom script to make it valid
	// 2. Deleting the orphaned saga
	// The CHECK constraint allows this state because FK cascade creates it automatically
}

func TestSagaMigration_PlatformRefExtension_MutualExclusivity(t *testing.T) {
	tc := setupSagaTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	// Create a platform saga for testing
	platformID := uuid.New()
	_, err := tc.pool.Exec(ctx, `
		INSERT INTO public.platform_saga_definition (id, name, version, script)
		VALUES ($1, 'platform_mutual_test', '1.0.0', 'def posting_rules(ctx): pass')
	`, platformID)
	require.NoError(t, err)

	t.Run("accepts platform_ref with empty script", func(t *testing.T) {
		id := uuid.New()
		_, err := tc.pool.Exec(ctx, `
			INSERT INTO saga_definition (id, name, version, script, status, platform_ref)
			VALUES ($1, 'valid_platform_ref', 1, '', 'DRAFT', $2)
		`, id, platformID)
		require.NoError(t, err)
	})

	t.Run("accepts custom script with NULL platform_ref", func(t *testing.T) {
		id := uuid.New()
		_, err := tc.pool.Exec(ctx, `
			INSERT INTO saga_definition (id, name, version, script, status, platform_ref)
			VALUES ($1, 'valid_custom_script', 1, 'def posting_rules(ctx): pass', 'DRAFT', NULL)
		`, id)
		require.NoError(t, err)
	})

	t.Run("rejects both platform_ref and script set", func(t *testing.T) {
		id := uuid.New()
		_, err := tc.pool.Exec(ctx, `
			INSERT INTO saga_definition (id, name, version, script, status, platform_ref)
			VALUES ($1, 'invalid_both_set', 1, 'def posting_rules(ctx): pass', 'DRAFT', $2)
		`, id, platformID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "chk_saga_definition_platform_or_custom")
	})

	t.Run("allows orphaned state (neither platform_ref nor script)", func(t *testing.T) {
		// This state is allowed because ON DELETE SET NULL can create it
		// Application logic should detect and handle orphaned sagas
		id := uuid.New()
		_, err := tc.pool.Exec(ctx, `
			INSERT INTO saga_definition (id, name, version, script, status, platform_ref)
			VALUES ($1, 'orphaned_saga', 1, '', 'DRAFT', NULL)
		`, id)
		require.NoError(t, err, "orphaned state should be allowed for FK cascade compatibility")
	})
}

func TestSagaMigration_PlatformRefExtension_OverrideTracking(t *testing.T) {
	tc := setupSagaTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	// Insert a custom saga with override tracking
	id := uuid.New()
	_, err := tc.pool.Exec(ctx, `
		INSERT INTO saga_definition (
			id, name, version, script, status,
			platform_ref, override_reason, platform_version_at_override
		)
		VALUES (
			$1, 'custom_override', 1, 'def posting_rules(ctx): pass', 'DRAFT',
			NULL, 'Need custom business logic for regional compliance', '1.2.3'
		)
	`, id)
	require.NoError(t, err)

	// Verify override tracking fields were stored
	var overrideReason, platformVersion string
	err = tc.pool.QueryRow(ctx, `
		SELECT override_reason, platform_version_at_override
		FROM saga_definition
		WHERE id = $1
	`, id).Scan(&overrideReason, &platformVersion)
	require.NoError(t, err)
	assert.Equal(t, "Need custom business logic for regional compliance", overrideReason)
	assert.Equal(t, "1.2.3", platformVersion)
}

func TestSagaMigration_PlatformRefExtension_Index(t *testing.T) {
	tc := setupSagaTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	// Verify the platform_ref index exists
	var exists bool
	err := tc.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM pg_indexes
			WHERE indexname = 'idx_saga_definition_platform_ref'
		)
	`).Scan(&exists)
	require.NoError(t, err)
	assert.True(t, exists, "idx_saga_definition_platform_ref index should exist")
}

func TestSagaMigration_PlatformRefExtension_ExistingDataRemainValid(t *testing.T) {
	tc := setupSagaTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	// This test verifies that existing sagas with scripts (pre-migration) remain valid
	// after the migration adds the platform_ref columns

	// The migration should allow existing rows where:
	// - platform_ref is NULL (default after ALTER TABLE ADD COLUMN)
	// - script has a value

	// Insert a saga that mimics pre-migration data (script set, platform_ref NULL)
	id := uuid.New()
	_, err := tc.pool.Exec(ctx, `
		INSERT INTO saga_definition (id, name, version, script, status)
		VALUES ($1, 'legacy_saga', 1, 'def posting_rules(ctx): pass', 'DRAFT')
	`, id)
	require.NoError(t, err)

	// Verify the saga exists and has NULL platform_ref
	var platformRef *uuid.UUID
	var script string
	err = tc.pool.QueryRow(ctx, `
		SELECT platform_ref, script FROM saga_definition WHERE id = $1
	`, id).Scan(&platformRef, &script)
	require.NoError(t, err)
	assert.Nil(t, platformRef)
	assert.NotEmpty(t, script)

	// Verify we can query and update it without issues
	_, err = tc.pool.Exec(ctx, `
		UPDATE saga_definition SET description = 'Updated legacy saga' WHERE id = $1
	`, id)
	require.NoError(t, err)
}
