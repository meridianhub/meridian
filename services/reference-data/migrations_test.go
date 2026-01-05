package migrations_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// testContainer holds the test database container and connection pool
type testContainer struct {
	container *postgres.PostgresContainer
	pool      *pgxpool.Pool
}

// setupTestContainer creates a PostgreSQL testcontainer with the migration applied
func setupTestContainer(t *testing.T) *testContainer {
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

	// Apply migration
	applyMigration(t, pool)

	return &testContainer{
		container: pgContainer,
		pool:      pool,
	}
}

// cleanup closes the pool and terminates the container
func (tc *testContainer) cleanup(t *testing.T) {
	t.Helper()
	ctx := context.Background()

	if tc.pool != nil {
		tc.pool.Close()
	}

	if tc.container != nil {
		require.NoError(t, tc.container.Terminate(ctx))
	}
}

// applyMigration reads and executes the migration SQL file
func applyMigration(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()

	// Read migration file
	migrationPath := filepath.Join("migrations", "20260104000001_initial.sql")
	migrationSQL, err := os.ReadFile(migrationPath)
	require.NoError(t, err, "failed to read migration file")

	// Execute migration
	_, err = pool.Exec(ctx, string(migrationSQL))
	require.NoError(t, err, "failed to apply migration")
}

// insertInstrument is a helper to insert an instrument definition for testing
func insertInstrument(ctx context.Context, t *testing.T, pool *pgxpool.Pool, code string, version int, dimension string, precision int, status string) uuid.UUID {
	t.Helper()

	id := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO instrument_definition (id, code, version, dimension, precision, status, fungibility_key_expression)
		VALUES ($1, $2, $3, $4, $5, $6, '')
	`, id, code, version, dimension, precision, status)
	require.NoError(t, err)

	return id
}

// insertInstrumentWithExpressions inserts an instrument with validation expressions
func insertInstrumentWithExpressions(ctx context.Context, t *testing.T, pool *pgxpool.Pool, code string, version int, dimension string, precision int, status string, validationExpr, fungibilityExpr, errorMsgExpr string) uuid.UUID {
	t.Helper()

	id := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO instrument_definition (id, code, version, dimension, precision, status, validation_expression, fungibility_key_expression, error_message_expression)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, id, code, version, dimension, precision, status, validationExpr, fungibilityExpr, errorMsgExpr)
	require.NoError(t, err)

	return id
}

func TestMigration_AppliesCleanly(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	// Verify table exists with expected columns
	var tableName string
	err := tc.pool.QueryRow(ctx, `
		SELECT table_name
		FROM information_schema.tables
		WHERE table_name = 'instrument_definition'
	`).Scan(&tableName)
	require.NoError(t, err)
	assert.Equal(t, "instrument_definition", tableName)

	// Verify all expected columns exist
	expectedColumns := []string{
		"id", "code", "version", "dimension", "precision", "status",
		"validation_expression", "fungibility_key_expression", "error_message_expression",
		"attribute_schema", "display_name", "description",
		"created_at", "activated_at", "deprecated_at",
	}

	rows, err := tc.pool.Query(ctx, `
		SELECT column_name
		FROM information_schema.columns
		WHERE table_name = 'instrument_definition'
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

	// Verify trigger function exists
	var triggerFuncName string
	err = tc.pool.QueryRow(ctx, `
		SELECT routine_name
		FROM information_schema.routines
		WHERE routine_name = 'enforce_instrument_lifecycle'
	`).Scan(&triggerFuncName)
	require.NoError(t, err)
	assert.Equal(t, "enforce_instrument_lifecycle", triggerFuncName)

	// Verify trigger exists
	var triggerName string
	err = tc.pool.QueryRow(ctx, `
		SELECT trigger_name
		FROM information_schema.triggers
		WHERE trigger_name = 'trg_enforce_instrument_lifecycle'
	`).Scan(&triggerName)
	require.NoError(t, err)
	assert.Equal(t, "trg_enforce_instrument_lifecycle", triggerName)
}

func TestMigration_UniqueConstraint(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	// Insert first instrument
	insertInstrument(ctx, t, tc.pool, "GBP", 1, "MONETARY", 2, "DRAFT")

	// Attempt to insert duplicate code+version - should fail
	_, err := tc.pool.Exec(ctx, `
		INSERT INTO instrument_definition (id, code, version, dimension, precision, status, fungibility_key_expression)
		VALUES ($1, 'GBP', 1, 'MONETARY', 2, 'DRAFT', '')
	`, uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "uq_instrument_definition_code_version")

	// Insert same code with different version - should succeed
	_, err = tc.pool.Exec(ctx, `
		INSERT INTO instrument_definition (id, code, version, dimension, precision, status, fungibility_key_expression)
		VALUES ($1, 'GBP', 2, 'MONETARY', 2, 'DRAFT', '')
	`, uuid.New())
	require.NoError(t, err)

	// Insert different code with same version - should succeed
	_, err = tc.pool.Exec(ctx, `
		INSERT INTO instrument_definition (id, code, version, dimension, precision, status, fungibility_key_expression)
		VALUES ($1, 'USD', 1, 'MONETARY', 2, 'DRAFT', '')
	`, uuid.New())
	require.NoError(t, err)
}

func TestMigration_CheckConstraints(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	t.Run("precision constraint accepts valid values 0-18", func(t *testing.T) {
		validPrecisions := []int{0, 1, 8, 9, 17, 18}
		for _, precision := range validPrecisions {
			code := "TEST_PRECISION_" + string(rune('A'+precision))
			_, err := tc.pool.Exec(ctx, `
				INSERT INTO instrument_definition (id, code, version, dimension, precision, status, fungibility_key_expression)
				VALUES ($1, $2, 1, 'MONETARY', $3, 'DRAFT', '')
			`, uuid.New(), code, precision)
			require.NoError(t, err, "precision %d should be valid", precision)
		}
	})

	t.Run("precision constraint rejects invalid values", func(t *testing.T) {
		invalidPrecisions := []int{-1, -10, 19, 100}
		for _, precision := range invalidPrecisions {
			_, err := tc.pool.Exec(ctx, `
				INSERT INTO instrument_definition (id, code, version, dimension, precision, status, fungibility_key_expression)
				VALUES ($1, 'INVALID_PRECISION', 1, 'MONETARY', $2, 'DRAFT', '')
			`, uuid.New(), precision)
			require.Error(t, err, "precision %d should be rejected", precision)
			assert.Contains(t, err.Error(), "chk_instrument_definition_precision")
		}
	})

	t.Run("dimension constraint accepts valid values", func(t *testing.T) {
		validDimensions := []string{"MONETARY", "ENERGY", "QUANTITY", "COMPUTE", "TIME", "MASS", "VOLUME"}
		for i, dimension := range validDimensions {
			code := "TEST_DIM_" + dimension
			_, err := tc.pool.Exec(ctx, `
				INSERT INTO instrument_definition (id, code, version, dimension, precision, status, fungibility_key_expression)
				VALUES ($1, $2, $3, $4, 2, 'DRAFT', '')
			`, uuid.New(), code, i+1, dimension)
			require.NoError(t, err, "dimension %s should be valid", dimension)
		}
	})

	t.Run("dimension constraint rejects invalid values", func(t *testing.T) {
		invalidDimensions := []string{"INVALID", "monetary", "Money", "", "UNKNOWN"}
		for _, dimension := range invalidDimensions {
			_, err := tc.pool.Exec(ctx, `
				INSERT INTO instrument_definition (id, code, version, dimension, precision, status, fungibility_key_expression)
				VALUES ($1, 'INVALID_DIM', 1, $2, 2, 'DRAFT', '')
			`, uuid.New(), dimension)
			require.Error(t, err, "dimension %s should be rejected", dimension)
			assert.Contains(t, err.Error(), "chk_instrument_definition_dimension")
		}
	})

	t.Run("status constraint accepts valid values", func(t *testing.T) {
		validStatuses := []string{"DRAFT", "ACTIVE", "DEPRECATED"}
		for i, status := range validStatuses {
			code := "TEST_STATUS_" + status
			_, err := tc.pool.Exec(ctx, `
				INSERT INTO instrument_definition (id, code, version, dimension, precision, status, fungibility_key_expression)
				VALUES ($1, $2, $3, 'MONETARY', 2, $4, '')
			`, uuid.New(), code, i+1, status)
			require.NoError(t, err, "status %s should be valid", status)
		}
	})

	t.Run("status constraint rejects invalid values", func(t *testing.T) {
		invalidStatuses := []string{"INVALID", "draft", "Active", "", "PENDING"}
		for _, status := range invalidStatuses {
			_, err := tc.pool.Exec(ctx, `
				INSERT INTO instrument_definition (id, code, version, dimension, precision, status, fungibility_key_expression)
				VALUES ($1, 'INVALID_STATUS', 1, 'MONETARY', 2, $2, '')
			`, uuid.New(), status)
			require.Error(t, err, "status %s should be rejected", status)
			assert.Contains(t, err.Error(), "chk_instrument_definition_status")
		}
	})

	t.Run("validation_expression length constraint accepts up to 4KB", func(t *testing.T) {
		// Exactly 4096 bytes should be accepted
		validExpr := strings.Repeat("x", 4096)
		_, err := tc.pool.Exec(ctx, `
			INSERT INTO instrument_definition (id, code, version, dimension, precision, status, validation_expression, fungibility_key_expression)
			VALUES ($1, 'VALID_EXPR', 1, 'MONETARY', 2, 'DRAFT', $2, '')
		`, uuid.New(), validExpr)
		require.NoError(t, err)

		// NULL should be accepted
		_, err = tc.pool.Exec(ctx, `
			INSERT INTO instrument_definition (id, code, version, dimension, precision, status, validation_expression, fungibility_key_expression)
			VALUES ($1, 'NULL_EXPR', 1, 'MONETARY', 2, 'DRAFT', NULL, '')
		`, uuid.New())
		require.NoError(t, err)
	})

	t.Run("validation_expression length constraint rejects over 4KB", func(t *testing.T) {
		// 4097 bytes should be rejected
		invalidExpr := strings.Repeat("x", 4097)
		_, err := tc.pool.Exec(ctx, `
			INSERT INTO instrument_definition (id, code, version, dimension, precision, status, validation_expression, fungibility_key_expression)
			VALUES ($1, 'INVALID_EXPR', 1, 'MONETARY', 2, 'DRAFT', $2, '')
		`, uuid.New(), invalidExpr)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "chk_instrument_definition_validation_expression_length")
	})

	t.Run("fungibility_key_expression length constraint accepts up to 4KB", func(t *testing.T) {
		// Exactly 4096 bytes should be accepted
		validExpr := strings.Repeat("x", 4096)
		_, err := tc.pool.Exec(ctx, `
			INSERT INTO instrument_definition (id, code, version, dimension, precision, status, fungibility_key_expression)
			VALUES ($1, 'VALID_FUNG_EXPR', 1, 'MONETARY', 2, 'DRAFT', $2)
		`, uuid.New(), validExpr)
		require.NoError(t, err)
	})

	t.Run("fungibility_key_expression length constraint rejects over 4KB", func(t *testing.T) {
		// 4097 bytes should be rejected
		invalidExpr := strings.Repeat("x", 4097)
		_, err := tc.pool.Exec(ctx, `
			INSERT INTO instrument_definition (id, code, version, dimension, precision, status, fungibility_key_expression)
			VALUES ($1, 'INVALID_FUNG_EXPR', 1, 'MONETARY', 2, 'DRAFT', $2)
		`, uuid.New(), invalidExpr)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "chk_instrument_definition_fungibility_expression_length")
	})

	t.Run("error_message_expression length constraint accepts up to 4KB", func(t *testing.T) {
		// Exactly 4096 bytes should be accepted
		validExpr := strings.Repeat("x", 4096)
		_, err := tc.pool.Exec(ctx, `
			INSERT INTO instrument_definition (id, code, version, dimension, precision, status, fungibility_key_expression, error_message_expression)
			VALUES ($1, 'VALID_ERR_EXPR', 1, 'MONETARY', 2, 'DRAFT', '', $2)
		`, uuid.New(), validExpr)
		require.NoError(t, err)

		// NULL should be accepted
		_, err = tc.pool.Exec(ctx, `
			INSERT INTO instrument_definition (id, code, version, dimension, precision, status, fungibility_key_expression, error_message_expression)
			VALUES ($1, 'NULL_ERR_EXPR', 1, 'MONETARY', 2, 'DRAFT', '', NULL)
		`, uuid.New())
		require.NoError(t, err)
	})

	t.Run("error_message_expression length constraint rejects over 4KB", func(t *testing.T) {
		// 4097 bytes should be rejected
		invalidExpr := strings.Repeat("x", 4097)
		_, err := tc.pool.Exec(ctx, `
			INSERT INTO instrument_definition (id, code, version, dimension, precision, status, fungibility_key_expression, error_message_expression)
			VALUES ($1, 'INVALID_ERR_EXPR', 1, 'MONETARY', 2, 'DRAFT', '', $2)
		`, uuid.New(), invalidExpr)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "chk_instrument_definition_error_message_length")
	})
}

func TestMigration_LifecycleTrigger_DraftAllowsEdits(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	// Insert a DRAFT instrument with expressions
	id := insertInstrumentWithExpressions(ctx, t, tc.pool, "DRAFT_EDITABLE", 1, "MONETARY", 2, "DRAFT",
		"original_validation", "original_fungibility", "original_error")

	// All fields should be editable in DRAFT status
	editTests := []struct {
		name  string
		query string
		args  []interface{}
	}{
		{
			name:  "update validation_expression",
			query: `UPDATE instrument_definition SET validation_expression = $1 WHERE id = $2`,
			args:  []interface{}{"new_validation", id},
		},
		{
			name:  "update fungibility_key_expression",
			query: `UPDATE instrument_definition SET fungibility_key_expression = $1 WHERE id = $2`,
			args:  []interface{}{"new_fungibility", id},
		},
		{
			name:  "update error_message_expression",
			query: `UPDATE instrument_definition SET error_message_expression = $1 WHERE id = $2`,
			args:  []interface{}{"new_error", id},
		},
		{
			name:  "update display_name",
			query: `UPDATE instrument_definition SET display_name = $1 WHERE id = $2`,
			args:  []interface{}{"New Display Name", id},
		},
		{
			name:  "update description",
			query: `UPDATE instrument_definition SET description = $1 WHERE id = $2`,
			args:  []interface{}{"New Description", id},
		},
		{
			name:  "update precision",
			query: `UPDATE instrument_definition SET precision = $1 WHERE id = $2`,
			args:  []interface{}{4, id},
		},
	}

	for _, tt := range editTests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tc.pool.Exec(ctx, tt.query, tt.args...)
			require.NoError(t, err, "DRAFT instrument should allow editing %s", tt.name)
		})
	}
}

func TestMigration_LifecycleTrigger_ActiveBlocksExpressionChanges(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	// Insert ACTIVE instruments for testing immutable fields
	blockedExpressions := []struct {
		name       string
		column     string
		initialVal string
		newVal     string
	}{
		{
			name:       "validation_expression",
			column:     "validation_expression",
			initialVal: "original_validation",
			newVal:     "modified_validation",
		},
		{
			name:       "fungibility_key_expression",
			column:     "fungibility_key_expression",
			initialVal: "original_fungibility",
			newVal:     "modified_fungibility",
		},
		{
			name:       "error_message_expression",
			column:     "error_message_expression",
			initialVal: "original_error",
			newVal:     "modified_error",
		},
	}

	for i, tt := range blockedExpressions {
		t.Run("ACTIVE blocks "+tt.name, func(t *testing.T) {
			// Insert ACTIVE instrument
			id := uuid.New()
			code := "ACTIVE_BLOCKED_" + string(rune('A'+i))
			_, err := tc.pool.Exec(ctx, `
				INSERT INTO instrument_definition (id, code, version, dimension, precision, status, validation_expression, fungibility_key_expression, error_message_expression, activated_at)
				VALUES ($1, $2, 1, 'MONETARY', 2, 'ACTIVE', $3, $4, $5, NOW())
			`, id, code, "orig_val", "orig_fung", "orig_err")
			require.NoError(t, err)

			// Attempt to modify expression - should fail
			_, err = tc.pool.Exec(ctx, `UPDATE instrument_definition SET `+tt.column+` = $1 WHERE id = $2`, tt.newVal, id)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "Cannot modify "+tt.column)
		})
	}

	// Test that non-expression fields CAN be modified on ACTIVE instruments
	t.Run("ACTIVE allows non-expression edits", func(t *testing.T) {
		id := insertInstrument(ctx, t, tc.pool, "ACTIVE_EDITABLE", 1, "MONETARY", 2, "DRAFT")

		// Activate the instrument
		_, err := tc.pool.Exec(ctx, `UPDATE instrument_definition SET status = 'ACTIVE' WHERE id = $1`, id)
		require.NoError(t, err)

		// These should succeed
		_, err = tc.pool.Exec(ctx, `UPDATE instrument_definition SET display_name = 'Updated' WHERE id = $1`, id)
		require.NoError(t, err)

		_, err = tc.pool.Exec(ctx, `UPDATE instrument_definition SET description = 'Updated description' WHERE id = $1`, id)
		require.NoError(t, err)
	})
}

func TestMigration_LifecycleTrigger_StatusTransitions(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	t.Run("DRAFT to ACTIVE allowed", func(t *testing.T) {
		id := insertInstrument(ctx, t, tc.pool, "DRAFT_TO_ACTIVE", 1, "MONETARY", 2, "DRAFT")

		_, err := tc.pool.Exec(ctx, `UPDATE instrument_definition SET status = 'ACTIVE' WHERE id = $1`, id)
		require.NoError(t, err)

		var status string
		err = tc.pool.QueryRow(ctx, `SELECT status FROM instrument_definition WHERE id = $1`, id).Scan(&status)
		require.NoError(t, err)
		assert.Equal(t, "ACTIVE", status)
	})

	t.Run("DRAFT to DEPRECATED allowed", func(t *testing.T) {
		id := insertInstrument(ctx, t, tc.pool, "DRAFT_TO_DEPRECATED", 1, "MONETARY", 2, "DRAFT")

		_, err := tc.pool.Exec(ctx, `UPDATE instrument_definition SET status = 'DEPRECATED' WHERE id = $1`, id)
		require.NoError(t, err)

		var status string
		err = tc.pool.QueryRow(ctx, `SELECT status FROM instrument_definition WHERE id = $1`, id).Scan(&status)
		require.NoError(t, err)
		assert.Equal(t, "DEPRECATED", status)
	})

	t.Run("ACTIVE to DEPRECATED allowed", func(t *testing.T) {
		id := insertInstrument(ctx, t, tc.pool, "ACTIVE_TO_DEPRECATED", 1, "MONETARY", 2, "DRAFT")

		// First activate
		_, err := tc.pool.Exec(ctx, `UPDATE instrument_definition SET status = 'ACTIVE' WHERE id = $1`, id)
		require.NoError(t, err)

		// Then deprecate
		_, err = tc.pool.Exec(ctx, `UPDATE instrument_definition SET status = 'DEPRECATED' WHERE id = $1`, id)
		require.NoError(t, err)

		var status string
		err = tc.pool.QueryRow(ctx, `SELECT status FROM instrument_definition WHERE id = $1`, id).Scan(&status)
		require.NoError(t, err)
		assert.Equal(t, "DEPRECATED", status)
	})

	t.Run("ACTIVE to DRAFT blocked", func(t *testing.T) {
		id := insertInstrument(ctx, t, tc.pool, "ACTIVE_TO_DRAFT", 1, "MONETARY", 2, "DRAFT")

		// Activate
		_, err := tc.pool.Exec(ctx, `UPDATE instrument_definition SET status = 'ACTIVE' WHERE id = $1`, id)
		require.NoError(t, err)

		// Attempt to go back to DRAFT - should fail
		_, err = tc.pool.Exec(ctx, `UPDATE instrument_definition SET status = 'DRAFT' WHERE id = $1`, id)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Cannot transition from ACTIVE back to DRAFT")
	})

	t.Run("DEPRECATED to DRAFT blocked", func(t *testing.T) {
		id := insertInstrument(ctx, t, tc.pool, "DEPRECATED_TO_DRAFT", 1, "MONETARY", 2, "DRAFT")

		// Activate then deprecate
		_, err := tc.pool.Exec(ctx, `UPDATE instrument_definition SET status = 'ACTIVE' WHERE id = $1`, id)
		require.NoError(t, err)
		_, err = tc.pool.Exec(ctx, `UPDATE instrument_definition SET status = 'DEPRECATED' WHERE id = $1`, id)
		require.NoError(t, err)

		// Attempt to go back to DRAFT - should fail
		_, err = tc.pool.Exec(ctx, `UPDATE instrument_definition SET status = 'DRAFT' WHERE id = $1`, id)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Cannot transition from DEPRECATED")
	})

	t.Run("DEPRECATED to ACTIVE blocked", func(t *testing.T) {
		id := insertInstrument(ctx, t, tc.pool, "DEPRECATED_TO_ACTIVE", 1, "MONETARY", 2, "DRAFT")

		// Activate then deprecate
		_, err := tc.pool.Exec(ctx, `UPDATE instrument_definition SET status = 'ACTIVE' WHERE id = $1`, id)
		require.NoError(t, err)
		_, err = tc.pool.Exec(ctx, `UPDATE instrument_definition SET status = 'DEPRECATED' WHERE id = $1`, id)
		require.NoError(t, err)

		// Attempt to go to ACTIVE - should fail
		_, err = tc.pool.Exec(ctx, `UPDATE instrument_definition SET status = 'ACTIVE' WHERE id = $1`, id)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Cannot transition from DEPRECATED")
	})
}

func TestMigration_LifecycleTrigger_TimestampPopulation(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	t.Run("activated_at populated on DRAFT to ACTIVE transition", func(t *testing.T) {
		id := insertInstrument(ctx, t, tc.pool, "ACTIVATION_TIMESTAMP", 1, "MONETARY", 2, "DRAFT")

		// Verify activated_at is NULL initially
		var activatedAt *time.Time
		err := tc.pool.QueryRow(ctx, `SELECT activated_at FROM instrument_definition WHERE id = $1`, id).Scan(&activatedAt)
		require.NoError(t, err)
		assert.Nil(t, activatedAt)

		// Capture time before activation
		beforeActivation := time.Now().Add(-1 * time.Second)

		// Activate
		_, err = tc.pool.Exec(ctx, `UPDATE instrument_definition SET status = 'ACTIVE' WHERE id = $1`, id)
		require.NoError(t, err)

		// Verify activated_at is now set
		err = tc.pool.QueryRow(ctx, `SELECT activated_at FROM instrument_definition WHERE id = $1`, id).Scan(&activatedAt)
		require.NoError(t, err)
		require.NotNil(t, activatedAt)
		assert.True(t, activatedAt.After(beforeActivation), "activated_at should be after the activation request")
	})

	t.Run("deprecated_at populated on ACTIVE to DEPRECATED transition", func(t *testing.T) {
		id := insertInstrument(ctx, t, tc.pool, "DEPRECATION_TIMESTAMP", 1, "MONETARY", 2, "DRAFT")

		// Activate first
		_, err := tc.pool.Exec(ctx, `UPDATE instrument_definition SET status = 'ACTIVE' WHERE id = $1`, id)
		require.NoError(t, err)

		// Verify deprecated_at is NULL
		var deprecatedAt *time.Time
		err = tc.pool.QueryRow(ctx, `SELECT deprecated_at FROM instrument_definition WHERE id = $1`, id).Scan(&deprecatedAt)
		require.NoError(t, err)
		assert.Nil(t, deprecatedAt)

		// Capture time before deprecation
		beforeDeprecation := time.Now().Add(-1 * time.Second)

		// Deprecate
		_, err = tc.pool.Exec(ctx, `UPDATE instrument_definition SET status = 'DEPRECATED' WHERE id = $1`, id)
		require.NoError(t, err)

		// Verify deprecated_at is now set
		err = tc.pool.QueryRow(ctx, `SELECT deprecated_at FROM instrument_definition WHERE id = $1`, id).Scan(&deprecatedAt)
		require.NoError(t, err)
		require.NotNil(t, deprecatedAt)
		assert.True(t, deprecatedAt.After(beforeDeprecation), "deprecated_at should be after the deprecation request")
	})

	t.Run("created_at defaults to now on insert", func(t *testing.T) {
		beforeInsert := time.Now().Add(-1 * time.Second)

		id := insertInstrument(ctx, t, tc.pool, "CREATED_TIMESTAMP", 1, "MONETARY", 2, "DRAFT")

		var createdAt time.Time
		err := tc.pool.QueryRow(ctx, `SELECT created_at FROM instrument_definition WHERE id = $1`, id).Scan(&createdAt)
		require.NoError(t, err)
		assert.True(t, createdAt.After(beforeInsert), "created_at should be after the insert request")
	})

	t.Run("deprecated_at populated on DRAFT to DEPRECATED transition", func(t *testing.T) {
		id := insertInstrument(ctx, t, tc.pool, "DRAFT_DEPRECATION", 1, "MONETARY", 2, "DRAFT")

		// Verify deprecated_at is NULL initially
		var deprecatedAt *time.Time
		err := tc.pool.QueryRow(ctx, `SELECT deprecated_at FROM instrument_definition WHERE id = $1`, id).Scan(&deprecatedAt)
		require.NoError(t, err)
		assert.Nil(t, deprecatedAt)

		// Capture time before deprecation
		beforeDeprecation := time.Now().Add(-1 * time.Second)

		// Deprecate directly from DRAFT
		_, err = tc.pool.Exec(ctx, `UPDATE instrument_definition SET status = 'DEPRECATED' WHERE id = $1`, id)
		require.NoError(t, err)

		// Verify deprecated_at is now set
		err = tc.pool.QueryRow(ctx, `SELECT deprecated_at FROM instrument_definition WHERE id = $1`, id).Scan(&deprecatedAt)
		require.NoError(t, err)
		require.NotNil(t, deprecatedAt)
		assert.True(t, deprecatedAt.After(beforeDeprecation), "deprecated_at should be after the deprecation request")
	})

	t.Run("updated_at populated on any update", func(t *testing.T) {
		id := insertInstrument(ctx, t, tc.pool, "UPDATED_TIMESTAMP", 1, "MONETARY", 2, "DRAFT")

		// Get initial updated_at
		var initialUpdatedAt time.Time
		err := tc.pool.QueryRow(ctx, `SELECT updated_at FROM instrument_definition WHERE id = $1`, id).Scan(&initialUpdatedAt)
		require.NoError(t, err)

		// Wait a tiny bit to ensure time difference
		time.Sleep(10 * time.Millisecond)

		// Update display_name
		_, err = tc.pool.Exec(ctx, `UPDATE instrument_definition SET display_name = 'Updated Name' WHERE id = $1`, id)
		require.NoError(t, err)

		// Verify updated_at changed
		var newUpdatedAt time.Time
		err = tc.pool.QueryRow(ctx, `SELECT updated_at FROM instrument_definition WHERE id = $1`, id).Scan(&newUpdatedAt)
		require.NoError(t, err)
		assert.True(t, newUpdatedAt.After(initialUpdatedAt), "updated_at should be after the initial value")
	})
}

func TestMigration_Indexes(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	t.Run("verify expected indexes exist", func(t *testing.T) {
		// Note: (code, version) index is implicit via unique constraint
		expectedIndexes := []string{
			"idx_instrument_definition_code_active",
			"idx_instrument_definition_status",
			"idx_instrument_definition_created_at",
		}

		for _, indexName := range expectedIndexes {
			var exists bool
			err := tc.pool.QueryRow(ctx, `
				SELECT EXISTS(
					SELECT 1 FROM pg_indexes
					WHERE tablename = 'instrument_definition'
					AND indexname = $1
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
				WHERE tablename = 'instrument_definition'
				AND indexname = 'uq_instrument_definition_code_version'
			)
		`).Scan(&exists)
		require.NoError(t, err)
		assert.True(t, exists, "unique constraint index should exist")
	})

	t.Run("verify code_version index used in queries", func(t *testing.T) {
		// Insert some data to make query planning realistic
		for i := 0; i < 10; i++ {
			insertInstrument(ctx, t, tc.pool, "IDX_TEST_"+string(rune('A'+i)), i+1, "MONETARY", 2, "DRAFT")
		}

		// Check query plan uses the index
		var plan string
		rows, err := tc.pool.Query(ctx, `
			EXPLAIN (FORMAT TEXT)
			SELECT * FROM instrument_definition
			WHERE code = 'IDX_TEST_A' AND version = 1
		`)
		require.NoError(t, err)
		defer rows.Close()

		for rows.Next() {
			var line string
			require.NoError(t, rows.Scan(&line))
			plan += line + "\n"
		}

		// Should use index scan (not seq scan) for this query
		assert.Contains(t, plan, "Index", "query should use index for code+version lookup")
	})

	t.Run("verify active status partial index used in queries", func(t *testing.T) {
		// Insert some ACTIVE instruments
		for i := 0; i < 10; i++ {
			id := insertInstrument(ctx, t, tc.pool, "ACTIVE_IDX_"+string(rune('A'+i)), 1, "MONETARY", 2, "DRAFT")
			_, err := tc.pool.Exec(ctx, `UPDATE instrument_definition SET status = 'ACTIVE' WHERE id = $1`, id)
			require.NoError(t, err)
		}

		// Check query plan for active instruments
		var plan string
		rows, err := tc.pool.Query(ctx, `
			EXPLAIN (FORMAT TEXT)
			SELECT * FROM instrument_definition
			WHERE code = 'ACTIVE_IDX_A' AND status = 'ACTIVE'
		`)
		require.NoError(t, err)
		defer rows.Close()

		for rows.Next() {
			var line string
			require.NoError(t, rows.Scan(&line))
			plan += line + "\n"
		}

		// The partial index should be considered
		assert.Contains(t, plan, "Index", "query should use index for active instruments")
	})
}

func TestMigration_SchemaIsolation(t *testing.T) {
	// This test creates multiple schemas to simulate tenant isolation
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	// Create tenant schemas
	tenants := []string{"tenant_alpha", "tenant_beta", "tenant_gamma"}

	for _, tenant := range tenants {
		// Create schema for tenant
		_, err := tc.pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS `+tenant)
		require.NoError(t, err)

		// Read and apply migration to this tenant's schema
		migrationPath := filepath.Join("migrations", "20260104000001_initial.sql")
		migrationSQL, err := os.ReadFile(migrationPath)
		require.NoError(t, err)

		// Set search_path to tenant schema and apply migration
		_, err = tc.pool.Exec(ctx, `SET search_path TO `+tenant)
		require.NoError(t, err)
		_, err = tc.pool.Exec(ctx, string(migrationSQL))
		require.NoError(t, err)
	}

	t.Run("data inserted in one tenant is not visible in another", func(t *testing.T) {
		// Insert data into tenant_alpha
		_, err := tc.pool.Exec(ctx, `SET search_path TO tenant_alpha`)
		require.NoError(t, err)
		_, err = tc.pool.Exec(ctx, `
			INSERT INTO instrument_definition (id, code, version, dimension, precision, status, fungibility_key_expression)
			VALUES ($1, 'ALPHA_ONLY', 1, 'MONETARY', 2, 'DRAFT', '')
		`, uuid.New())
		require.NoError(t, err)

		// Verify it exists in tenant_alpha
		var count int
		err = tc.pool.QueryRow(ctx, `SELECT COUNT(*) FROM instrument_definition WHERE code = 'ALPHA_ONLY'`).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count)

		// Verify it does NOT exist in tenant_beta
		_, err = tc.pool.Exec(ctx, `SET search_path TO tenant_beta`)
		require.NoError(t, err)
		err = tc.pool.QueryRow(ctx, `SELECT COUNT(*) FROM instrument_definition WHERE code = 'ALPHA_ONLY'`).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 0, count)

		// Verify it does NOT exist in tenant_gamma
		_, err = tc.pool.Exec(ctx, `SET search_path TO tenant_gamma`)
		require.NoError(t, err)
		err = tc.pool.QueryRow(ctx, `SELECT COUNT(*) FROM instrument_definition WHERE code = 'ALPHA_ONLY'`).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 0, count)
	})

	t.Run("same code+version can exist in different tenants", func(t *testing.T) {
		// Insert same code+version in each tenant
		for _, tenant := range tenants {
			_, err := tc.pool.Exec(ctx, `SET search_path TO `+tenant)
			require.NoError(t, err)
			_, err = tc.pool.Exec(ctx, `
				INSERT INTO instrument_definition (id, code, version, dimension, precision, status, fungibility_key_expression)
				VALUES ($1, 'SHARED_CODE', 1, 'MONETARY', 2, 'DRAFT', '')
			`, uuid.New())
			require.NoError(t, err, "tenant %s should allow inserting SHARED_CODE", tenant)
		}

		// Verify each tenant has exactly one record
		for _, tenant := range tenants {
			_, err := tc.pool.Exec(ctx, `SET search_path TO `+tenant)
			require.NoError(t, err)

			var count int
			err = tc.pool.QueryRow(ctx, `SELECT COUNT(*) FROM instrument_definition WHERE code = 'SHARED_CODE'`).Scan(&count)
			require.NoError(t, err)
			assert.Equal(t, 1, count, "tenant %s should have exactly one SHARED_CODE", tenant)
		}
	})

	t.Run("trigger functions work independently per tenant", func(t *testing.T) {
		// Activate instrument in tenant_alpha
		_, err := tc.pool.Exec(ctx, `SET search_path TO tenant_alpha`)
		require.NoError(t, err)

		var id uuid.UUID
		err = tc.pool.QueryRow(ctx, `SELECT id FROM instrument_definition WHERE code = 'SHARED_CODE'`).Scan(&id)
		require.NoError(t, err)

		_, err = tc.pool.Exec(ctx, `UPDATE instrument_definition SET status = 'ACTIVE' WHERE id = $1`, id)
		require.NoError(t, err)

		// Verify activated_at was set in tenant_alpha
		var activatedAt *time.Time
		err = tc.pool.QueryRow(ctx, `SELECT activated_at FROM instrument_definition WHERE id = $1`, id).Scan(&activatedAt)
		require.NoError(t, err)
		require.NotNil(t, activatedAt)

		// Verify tenant_beta's instrument is still DRAFT
		_, err = tc.pool.Exec(ctx, `SET search_path TO tenant_beta`)
		require.NoError(t, err)

		var status string
		err = tc.pool.QueryRow(ctx, `SELECT status FROM instrument_definition WHERE code = 'SHARED_CODE'`).Scan(&status)
		require.NoError(t, err)
		assert.Equal(t, "DRAFT", status, "tenant_beta instrument should still be DRAFT")
	})
}

// TestMigration_IdempotencyCheck verifies that the migration can be run multiple times safely
// (though in practice, migrations should only run once via Atlas versioned migrations)
func TestMigration_IdempotencyCheck(t *testing.T) {
	ctx := context.Background()

	// Create PostgreSQL container manually to avoid double-applying migration
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
	defer func() {
		require.NoError(t, pgContainer.Terminate(ctx))
	}()

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	poolConfig, err := pgxpool.ParseConfig(connStr)
	require.NoError(t, err)

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	require.NoError(t, err)
	defer pool.Close()

	// Read migration
	migrationPath := filepath.Join("migrations", "20260104000001_initial.sql")
	migrationSQL, err := os.ReadFile(migrationPath)
	require.NoError(t, err)

	// First application should succeed
	_, err = pool.Exec(ctx, string(migrationSQL))
	require.NoError(t, err)

	// Insert some data
	_, err = pool.Exec(ctx, `
		INSERT INTO instrument_definition (id, code, version, dimension, precision, status, fungibility_key_expression)
		VALUES ($1, 'TEST', 1, 'MONETARY', 2, 'DRAFT', '')
	`, uuid.New())
	require.NoError(t, err)

	// Second application should fail (table already exists)
	// This verifies that the migration is NOT idempotent, which is expected
	// for Atlas versioned migrations
	_, err = pool.Exec(ctx, string(migrationSQL))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}
