//go:build integration

// Package e2e provides end-to-end integration tests for the universal asset system.
// These tests verify the full workflow from instrument creation to position aggregation,
// spanning the reference-data and position-keeping services.
package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/meridianhub/meridian/services/reference-data/cache"
	refcel "github.com/meridianhub/meridian/services/reference-data/cel"
	"github.com/meridianhub/meridian/services/reference-data/registry"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
)

// ============================================================================
// Test Infrastructure
// ============================================================================

// setupE2ETestPool creates a shared PostgreSQL testcontainer for E2E tests.
// This pool is shared across reference-data and position-keeping schemas.
func setupE2ETestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("e2e_test_db"),
		postgres.WithUsername("test_user"),
		postgres.WithPassword("test_password"),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
				wait.ForListeningPort("5432/tcp"),
			).WithDeadline(60*time.Second)),
	)
	require.NoError(t, err, "Failed to start PostgreSQL container")

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_ = pgContainer.Terminate(cleanupCtx)
	})

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "Failed to get connection string")

	pool, err := pgxpool.New(ctx, connStr)
	require.NoError(t, err, "Failed to create connection pool")

	t.Cleanup(func() {
		pool.Close()
	})

	return pool
}

// setupTenantWithBothSchemas creates a tenant schema with both reference-data and position-keeping tables.
func setupTenantWithBothSchemas(t *testing.T, pool *pgxpool.Pool, tenantID string) context.Context {
	t.Helper()

	tid := tenant.TenantID(tenantID)
	schemaName := tid.SchemaName()

	ctx := context.Background()

	// Create the tenant schema
	_, err := pool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err, "Failed to create tenant schema %s", schemaName)

	// Apply reference-data schema
	applyReferenceDataSchema(t, pool, schemaName)

	// Apply position-keeping schema
	applyPositionKeepingSchema(t, pool, schemaName)

	// Create context with tenant
	tenantCtx := tenant.WithTenant(context.Background(), tid)

	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = pool.Exec(cleanupCtx, fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", pq.QuoteIdentifier(schemaName)))
	})

	return tenantCtx
}

// applyReferenceDataSchema creates the instrument_definition table in the tenant schema.
func applyReferenceDataSchema(t *testing.T, pool *pgxpool.Pool, schemaName string) {
	t.Helper()
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, fmt.Sprintf("SET search_path TO %s, public", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err)

	// Create instrument_definition table
	_, err = tx.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS instrument_definition (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			code character varying(32) NOT NULL,
			version integer NOT NULL DEFAULT 1,
			dimension character varying(32) NOT NULL,
			precision integer NOT NULL DEFAULT 2,
			status character varying(20) NOT NULL DEFAULT 'DRAFT',
			is_system boolean NOT NULL DEFAULT false,
			validation_expression text,
			fungibility_key_expression text NOT NULL DEFAULT '',
			error_message_expression text,
			attribute_schema jsonb,
			display_name character varying(255),
			description text,
			created_at timestamptz NOT NULL DEFAULT now(),
			updated_at timestamptz NOT NULL DEFAULT now(),
			activated_at timestamptz,
			deprecated_at timestamptz,
			successor_id uuid,
			PRIMARY KEY (id),
			UNIQUE (code, version)
		)
	`)
	require.NoError(t, err, "Failed to create instrument_definition table")

	// Create indexes
	_, err = tx.Exec(ctx, `
		CREATE INDEX IF NOT EXISTS idx_instrument_definition_code ON instrument_definition (code);
		CREATE INDEX IF NOT EXISTS idx_instrument_definition_status ON instrument_definition (status);
		CREATE INDEX IF NOT EXISTS idx_instrument_definition_is_system ON instrument_definition (is_system)
	`)
	require.NoError(t, err, "Failed to create indexes")

	// Create trigger function for successor validation
	_, err = tx.Exec(ctx, `
		CREATE OR REPLACE FUNCTION validate_successor()
		RETURNS TRIGGER AS $$
		DECLARE
			successor_record RECORD;
			deprecated_record RECORD;
		BEGIN
			-- Skip validation if successor_id is NULL
			IF NEW.successor_id IS NULL THEN
				RETURN NEW;
			END IF;

			-- Check if trying to set self as successor
			IF NEW.id = NEW.successor_id THEN
				RAISE EXCEPTION 'successor instrument cannot be self-referential';
			END IF;

			-- Fetch the successor record
			SELECT id, status, dimension INTO successor_record
			FROM instrument_definition
			WHERE id = NEW.successor_id;

			IF successor_record IS NULL THEN
				RAISE EXCEPTION 'successor instrument does not exist';
			END IF;

			IF successor_record.status != 'ACTIVE' THEN
				RAISE EXCEPTION 'successor instrument must be ACTIVE';
			END IF;

			-- Check dimension matches
			IF successor_record.dimension != NEW.dimension THEN
				RAISE EXCEPTION 'successor instrument must have same dimension';
			END IF;

			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql
	`)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
		DROP TRIGGER IF EXISTS validate_successor_trigger ON instrument_definition;
		CREATE TRIGGER validate_successor_trigger
			BEFORE INSERT OR UPDATE OF successor_id ON instrument_definition
			FOR EACH ROW
			EXECUTE FUNCTION validate_successor()
	`)
	require.NoError(t, err)

	// Create write-once trigger for successor_id
	_, err = tx.Exec(ctx, `
		CREATE OR REPLACE FUNCTION successor_id_write_once()
		RETURNS TRIGGER AS $$
		BEGIN
			IF OLD.successor_id IS NOT NULL AND NEW.successor_id IS DISTINCT FROM OLD.successor_id THEN
				RAISE EXCEPTION 'successor_id is write-once and cannot be changed';
			END IF;
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql
	`)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
		DROP TRIGGER IF EXISTS successor_id_write_once_trigger ON instrument_definition;
		CREATE TRIGGER successor_id_write_once_trigger
			BEFORE UPDATE ON instrument_definition
			FOR EACH ROW
			EXECUTE FUNCTION successor_id_write_once()
	`)
	require.NoError(t, err)

	require.NoError(t, tx.Commit(ctx))
}

// applyPositionKeepingSchema creates the position table in the tenant schema.
func applyPositionKeepingSchema(t *testing.T, pool *pgxpool.Pool, schemaName string) {
	t.Helper()
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, fmt.Sprintf("SET search_path TO %s, public", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err)

	// Create position table (append-only)
	_, err = tx.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS position (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			created_at timestamptz NOT NULL DEFAULT now(),
			created_by character varying(100) NOT NULL,
			deleted_at timestamptz NULL,
			account_id character varying(34) NOT NULL,
			instrument_code character varying(32) NOT NULL,
			bucket_key character varying(256) NOT NULL,
			amount decimal(38, 18) NOT NULL,
			dimension character varying(32) NOT NULL DEFAULT 'Monetary',
			attributes jsonb NULL,
			reference_id uuid NULL,
			PRIMARY KEY (id),
			CONSTRAINT position_dimension_check CHECK (dimension IN ('Monetary', 'Energy', 'Compute', 'Carbon', 'Time', 'Physical', 'Custom'))
		)
	`)
	require.NoError(t, err, "Failed to create position table")

	// Create indexes
	_, err = tx.Exec(ctx, `
		CREATE INDEX IF NOT EXISTS idx_position_account_id ON position (account_id);
		CREATE INDEX IF NOT EXISTS idx_position_aggregation ON position (account_id, instrument_code, bucket_key);
		CREATE INDEX IF NOT EXISTS idx_position_deleted_at ON position (deleted_at);
		CREATE INDEX IF NOT EXISTS idx_position_active ON position (account_id, instrument_code, bucket_key)
			WHERE deleted_at IS NULL;
		CREATE INDEX IF NOT EXISTS idx_position_reference_id ON position (reference_id);
		CREATE INDEX IF NOT EXISTS idx_position_created_at ON position (created_at)
	`)
	require.NoError(t, err, "Failed to create position indexes")

	// Create append-only trigger function
	_, err = tx.Exec(ctx, `
		CREATE OR REPLACE FUNCTION positions_append_only()
		RETURNS TRIGGER AS $$
		BEGIN
			IF OLD.amount IS DISTINCT FROM NEW.amount THEN
				RAISE EXCEPTION 'positions table is append-only - UPDATE on amount column is forbidden'
					USING ERRCODE = 'P0001';
			END IF;
			IF OLD.account_id IS DISTINCT FROM NEW.account_id THEN
				RAISE EXCEPTION 'positions table is append-only - UPDATE on account_id column is forbidden'
					USING ERRCODE = 'P0001';
			END IF;
			IF OLD.instrument_code IS DISTINCT FROM NEW.instrument_code THEN
				RAISE EXCEPTION 'positions table is append-only - UPDATE on instrument_code column is forbidden'
					USING ERRCODE = 'P0001';
			END IF;
			IF OLD.bucket_key IS DISTINCT FROM NEW.bucket_key THEN
				RAISE EXCEPTION 'positions table is append-only - UPDATE on bucket_key column is forbidden'
					USING ERRCODE = 'P0001';
			END IF;
			IF OLD.reference_id IS DISTINCT FROM NEW.reference_id THEN
				RAISE EXCEPTION 'positions table is append-only - UPDATE on reference_id column is forbidden'
					USING ERRCODE = 'P0001';
			END IF;
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql
	`)
	require.NoError(t, err, "Failed to create append-only trigger function")

	_, err = tx.Exec(ctx, `
		DROP TRIGGER IF EXISTS positions_append_only ON position;
		CREATE TRIGGER positions_append_only
			BEFORE UPDATE ON position
			FOR EACH ROW
			EXECUTE FUNCTION positions_append_only()
	`)
	require.NoError(t, err, "Failed to create append-only trigger")

	require.NoError(t, tx.Commit(ctx))
}

// seedSystemInstrument seeds a system instrument directly via SQL (simulating provisioning).
func seedSystemInstrument(t *testing.T, pool *pgxpool.Pool, ctx context.Context, code string, dimension string, precision int) {
	t.Helper()

	tenantID, _ := tenant.FromContext(ctx)
	schemaName := tenantID.SchemaName()

	query := `
		INSERT INTO instrument_definition (
			id, code, version, dimension, precision, status, is_system,
			fungibility_key_expression, created_at, updated_at, activated_at
		) VALUES (
			gen_random_uuid(), $1, 1, $2, $3, 'ACTIVE', true,
			'', NOW(), NOW(), NOW()
		)`

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s, public", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err)

	_, err = tx.Exec(ctx, query, code, dimension, precision)
	require.NoError(t, err)

	require.NoError(t, tx.Commit(ctx))
}

// insertPosition inserts a position record directly via SQL.
func insertPosition(t *testing.T, pool *pgxpool.Pool, ctx context.Context, accountID, instrumentCode, bucketKey string, amount decimal.Decimal, dimension string, attributes map[string]string) uuid.UUID {
	t.Helper()

	tenantID, _ := tenant.FromContext(ctx)
	schemaName := tenantID.SchemaName()

	id := uuid.New()

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s, public", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err)

	var attrsJSON interface{}
	if attributes != nil {
		attrsJSON = attributes
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO position (id, created_at, created_by, account_id, instrument_code, bucket_key, amount, dimension, attributes)
		VALUES ($1, NOW(), 'test-system', $2, $3, $4, $5, $6, $7)`,
		id, accountID, instrumentCode, bucketKey, amount, dimension, attrsJSON,
	)
	require.NoError(t, err)

	require.NoError(t, tx.Commit(ctx))
	return id
}

// getAggregatedPosition retrieves an aggregated position via SQL.
func getAggregatedPosition(t *testing.T, pool *pgxpool.Pool, ctx context.Context, accountID, instrumentCode, bucketKey string) (decimal.Decimal, int64) {
	t.Helper()

	tenantID, _ := tenant.FromContext(ctx)
	schemaName := tenantID.SchemaName()

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s, public", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err)

	var totalAmount decimal.Decimal
	var recordCount int64

	err = tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0), COUNT(*)
		FROM position
		WHERE account_id = $1 AND instrument_code = $2 AND bucket_key = $3 AND deleted_at IS NULL
		GROUP BY account_id, instrument_code, bucket_key`,
		accountID, instrumentCode, bucketKey,
	).Scan(&totalAmount, &recordCount)
	if err != nil {
		// No rows found
		return decimal.Zero, 0
	}

	require.NoError(t, tx.Commit(ctx))
	return totalAmount, recordCount
}

// ============================================================================
// E2E Test: Full Instrument to Position Workflow
// ============================================================================

// TestE2E_InstrumentToPositionWorkflow tests the complete flow from instrument
// creation through position aggregation with CEL validation.
func TestE2E_InstrumentToPositionWorkflow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupE2ETestPool(t)
	ctx := setupTenantWithBothSchemas(t, pool, "e2e_workflow_tenant")

	reg, err := registry.NewPostgresRegistry(pool)
	require.NoError(t, err)

	t.Run("1. Create custom instrument with CEL validation", func(t *testing.T) {
		// Create a custom energy instrument with validation requiring positive amounts
		// and a source_type attribute
		def := &registry.InstrumentDefinition{
			Code:      "SOLAR_KWH",
			Version:   1,
			Dimension: registry.DimensionEnergy,
			Precision: 4,
			// Validation: amount must be positive and source must be specified
			ValidationExpression:   `parse_int(amount) >= 0 && has(attributes.source_type)`,
			ErrorMessageExpression: `"Invalid solar energy measurement: amount=" + amount + ", source=" + attributes.source_type`,
			DisplayName:            "Solar Kilowatt Hour",
			Description:            "Energy generated from solar panels",
		}

		err := reg.CreateDraft(ctx, def)
		require.NoError(t, err)
		assert.NotEqual(t, uuid.Nil, def.ID)
		assert.Equal(t, registry.StatusDraft, def.Status)
	})

	t.Run("2. Activate instrument", func(t *testing.T) {
		err := reg.ActivateInstrument(ctx, "SOLAR_KWH", 1)
		require.NoError(t, err)

		// Verify it's now ACTIVE
		def, err := reg.GetDefinition(ctx, "SOLAR_KWH", 1)
		require.NoError(t, err)
		assert.Equal(t, registry.StatusActive, def.Status)
		assert.NotNil(t, def.ActivatedAt)
	})

	t.Run("3. Create positions with valid attributes", func(t *testing.T) {
		// Insert multiple positions for the activated instrument
		positions := []struct {
			accountID string
			bucketKey string
			amount    float64
			attrs     map[string]string
		}{
			{"METER-001", "2024-01-01", 1500.5, map[string]string{"source_type": "rooftop"}},
			{"METER-001", "2024-01-01", 250.25, map[string]string{"source_type": "rooftop"}},
			{"METER-001", "2024-01-02", 1750.0, map[string]string{"source_type": "ground"}},
			{"METER-002", "2024-01-01", 3000.0, map[string]string{"source_type": "commercial"}},
		}

		for _, pos := range positions {
			insertPosition(t, pool, ctx,
				pos.accountID, "SOLAR_KWH", pos.bucketKey,
				decimal.NewFromFloat(pos.amount), "Energy", pos.attrs,
			)
		}

		// Verify METER-001 aggregation for 2024-01-01
		total, count := getAggregatedPosition(t, pool, ctx, "METER-001", "SOLAR_KWH", "2024-01-01")
		assert.True(t, decimal.NewFromFloat(1750.75).Equal(total), "Expected 1500.5 + 250.25 = 1750.75")
		assert.Equal(t, int64(2), count)

		// Verify METER-001 aggregation for 2024-01-02
		total, count = getAggregatedPosition(t, pool, ctx, "METER-001", "SOLAR_KWH", "2024-01-02")
		assert.True(t, decimal.NewFromFloat(1750.0).Equal(total))
		assert.Equal(t, int64(1), count)
	})

	t.Run("4. Verify attribute validation rejects invalid data", func(t *testing.T) {
		def, err := reg.GetDefinition(ctx, "SOLAR_KWH", 1)
		require.NoError(t, err)

		// Valid case - has source_type and positive amount
		result, err := reg.ValidateAttributes(ctx, "SOLAR_KWH", 1, registry.AttributeBag{
			Amount:     "100",
			Attributes: map[string]string{"source_type": "rooftop"},
		})
		require.NoError(t, err)
		assert.True(t, result.Valid)

		// Invalid case - negative amount
		result, err = reg.ValidateAttributes(ctx, "SOLAR_KWH", 1, registry.AttributeBag{
			Amount:     "-50",
			Attributes: map[string]string{"source_type": "rooftop"},
		})
		require.NoError(t, err)
		assert.False(t, result.Valid)

		// Invalid case - missing source_type
		result, err = reg.ValidateAttributes(ctx, "SOLAR_KWH", 1, registry.AttributeBag{
			Amount:     "100",
			Attributes: map[string]string{}, // No source_type
		})
		require.NoError(t, err)
		assert.False(t, result.Valid)

		_ = def // Used for context
	})
}

// ============================================================================
// E2E Test: Tenant Isolation
// ============================================================================

// TestE2E_TenantIsolation verifies that instruments created by one tenant
// are not visible to another tenant.
func TestE2E_TenantIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupE2ETestPool(t)
	ctxTenantA := setupTenantWithBothSchemas(t, pool, "tenant_iso_a")
	ctxTenantB := setupTenantWithBothSchemas(t, pool, "tenant_iso_b")

	reg, err := registry.NewPostgresRegistry(pool)
	require.NoError(t, err)

	t.Run("Tenant A creates instrument", func(t *testing.T) {
		def := &registry.InstrumentDefinition{
			Code:        "PRIVATE_TOKEN",
			Version:     1,
			Dimension:   registry.DimensionQuantity,
			Precision:   0,
			DisplayName: "Tenant A Private Token",
		}

		err := reg.CreateDraft(ctxTenantA, def)
		require.NoError(t, err)
		err = reg.ActivateInstrument(ctxTenantA, "PRIVATE_TOKEN", 1)
		require.NoError(t, err)
	})

	t.Run("Tenant B cannot see Tenant A's instrument", func(t *testing.T) {
		_, err := reg.GetDefinition(ctxTenantB, "PRIVATE_TOKEN", 1)
		require.ErrorIs(t, err, registry.ErrNotFound)

		_, err = reg.GetActiveDefinition(ctxTenantB, "PRIVATE_TOKEN")
		require.ErrorIs(t, err, registry.ErrNotFound)
	})

	t.Run("Both tenants can create same code independently", func(t *testing.T) {
		// Tenant B creates its own PRIVATE_TOKEN
		defB := &registry.InstrumentDefinition{
			Code:        "PRIVATE_TOKEN",
			Version:     1,
			Dimension:   registry.DimensionMonetary, // Different dimension
			Precision:   2,
			DisplayName: "Tenant B Private Token",
		}

		err := reg.CreateDraft(ctxTenantB, defB)
		require.NoError(t, err)
		err = reg.ActivateInstrument(ctxTenantB, "PRIVATE_TOKEN", 1)
		require.NoError(t, err)

		// Verify both exist independently with different dimensions
		resultA, err := reg.GetActiveDefinition(ctxTenantA, "PRIVATE_TOKEN")
		require.NoError(t, err)
		assert.Equal(t, registry.DimensionQuantity, resultA.Dimension)

		resultB, err := reg.GetActiveDefinition(ctxTenantB, "PRIVATE_TOKEN")
		require.NoError(t, err)
		assert.Equal(t, registry.DimensionMonetary, resultB.Dimension)
	})

	t.Run("Position isolation - Tenant A cannot see Tenant B positions", func(t *testing.T) {
		// Tenant A creates position
		insertPosition(t, pool, ctxTenantA, "ACC-A", "PRIVATE_TOKEN", "bucket-a",
			decimal.NewFromFloat(100.0), "Custom", nil)

		// Tenant B creates position with same account ID
		insertPosition(t, pool, ctxTenantB, "ACC-A", "PRIVATE_TOKEN", "bucket-a",
			decimal.NewFromFloat(500.0), "Monetary", nil)

		// Verify isolation
		totalA, countA := getAggregatedPosition(t, pool, ctxTenantA, "ACC-A", "PRIVATE_TOKEN", "bucket-a")
		assert.True(t, decimal.NewFromFloat(100.0).Equal(totalA))
		assert.Equal(t, int64(1), countA)

		totalB, countB := getAggregatedPosition(t, pool, ctxTenantB, "ACC-A", "PRIVATE_TOKEN", "bucket-a")
		assert.True(t, decimal.NewFromFloat(500.0).Equal(totalB))
		assert.Equal(t, int64(1), countB)
	})
}

// ============================================================================
// E2E Test: System Tenant Fallback
// ============================================================================

// TestE2E_SystemTenantFallback verifies that tenants can use system instruments
// (like USD) without defining them, and cannot modify system instruments.
func TestE2E_SystemTenantFallback(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupE2ETestPool(t)
	ctx := setupTenantWithBothSchemas(t, pool, "system_fallback_tenant")

	reg, err := registry.NewPostgresRegistry(pool)
	require.NoError(t, err)

	// Seed system instruments (simulating tenant provisioning)
	seedSystemInstrument(t, pool, ctx, "USD", "MONETARY", 2)
	seedSystemInstrument(t, pool, ctx, "EUR", "MONETARY", 2)
	seedSystemInstrument(t, pool, ctx, "GBP", "MONETARY", 2)

	t.Run("Tenant can use USD without defining it", func(t *testing.T) {
		// Get the system USD instrument
		def, err := reg.GetActiveDefinition(ctx, "USD")
		require.NoError(t, err)
		assert.Equal(t, "USD", def.Code)
		assert.True(t, def.IsSystem)
		assert.Equal(t, registry.StatusActive, def.Status)

		// Create positions using the system instrument
		insertPosition(t, pool, ctx, "ACC-SYS-TEST", "USD", "checking",
			decimal.NewFromFloat(1000.50), "Monetary", nil)
		insertPosition(t, pool, ctx, "ACC-SYS-TEST", "USD", "checking",
			decimal.NewFromFloat(500.25), "Monetary", nil)

		total, count := getAggregatedPosition(t, pool, ctx, "ACC-SYS-TEST", "USD", "checking")
		assert.True(t, decimal.NewFromFloat(1500.75).Equal(total))
		assert.Equal(t, int64(2), count)
	})

	t.Run("Tenant cannot modify system instrument", func(t *testing.T) {
		// Try to update USD
		updates := &registry.InstrumentDefinition{
			DisplayName: "Modified USD",
		}
		err := reg.UpdateDefinition(ctx, "USD", 1, updates)
		require.ErrorIs(t, err, registry.ErrSystemInstrumentReadOnly)
	})

	t.Run("Tenant cannot activate system instrument", func(t *testing.T) {
		err := reg.ActivateInstrument(ctx, "EUR", 1)
		require.ErrorIs(t, err, registry.ErrSystemInstrumentReadOnly)
	})

	t.Run("Tenant cannot deprecate system instrument", func(t *testing.T) {
		err := reg.DeprecateInstrument(ctx, "GBP", 1, nil)
		require.ErrorIs(t, err, registry.ErrSystemInstrumentReadOnly)
	})

	t.Run("ListActive returns both system and tenant instruments", func(t *testing.T) {
		// Create a tenant-specific instrument
		def := &registry.InstrumentDefinition{
			Code:      "TENANT_COIN",
			Version:   1,
			Dimension: registry.DimensionQuantity,
			Precision: 0,
		}
		require.NoError(t, reg.CreateDraft(ctx, def))
		require.NoError(t, reg.ActivateInstrument(ctx, "TENANT_COIN", 1))

		// List all active instruments
		results, err := reg.ListActive(ctx)
		require.NoError(t, err)

		codes := make(map[string]bool)
		for _, r := range results {
			codes[r.Code] = true
		}

		// Should have both system and tenant instruments
		assert.True(t, codes["USD"], "expected system USD")
		assert.True(t, codes["EUR"], "expected system EUR")
		assert.True(t, codes["GBP"], "expected system GBP")
		assert.True(t, codes["TENANT_COIN"], "expected tenant TENANT_COIN")
	})
}

// ============================================================================
// E2E Test: Bucket Aggregation
// ============================================================================

// TestE2E_BucketAggregation verifies that positions are correctly aggregated
// across multiple buckets using SQL GROUP BY.
func TestE2E_BucketAggregation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupE2ETestPool(t)
	ctx := setupTenantWithBothSchemas(t, pool, "bucket_agg_tenant")

	reg, err := registry.NewPostgresRegistry(pool)
	require.NoError(t, err)

	// Create and activate a compute instrument
	def := &registry.InstrumentDefinition{
		Code:        "GPU_HOUR",
		Version:     1,
		Dimension:   registry.DimensionCompute,
		Precision:   2,
		DisplayName: "GPU Compute Hour",
	}
	require.NoError(t, reg.CreateDraft(ctx, def))
	require.NoError(t, reg.ActivateInstrument(ctx, "GPU_HOUR", 1))

	t.Run("Insert positions across multiple buckets", func(t *testing.T) {
		// Insert positions across 3 different GPU instance types (buckets)
		buckets := []struct {
			key     string
			amounts []float64
		}{
			{"gpu-a100", []float64{10.5, 20.25, 5.0}},     // Total: 35.75, Count: 3
			{"gpu-v100", []float64{100.0}},                // Total: 100.0, Count: 1
			{"gpu-t4", []float64{15.0, 15.0, 15.0, 15.0}}, // Total: 60.0, Count: 4
		}

		for _, bucket := range buckets {
			for _, amt := range bucket.amounts {
				insertPosition(t, pool, ctx, "CLUSTER-01", "GPU_HOUR", bucket.key,
					decimal.NewFromFloat(amt), "Compute",
					map[string]string{"instance_type": bucket.key},
				)
			}
		}
	})

	t.Run("Verify aggregation per bucket", func(t *testing.T) {
		// Verify gpu-a100 bucket
		total, count := getAggregatedPosition(t, pool, ctx, "CLUSTER-01", "GPU_HOUR", "gpu-a100")
		assert.True(t, decimal.NewFromFloat(35.75).Equal(total), "gpu-a100 total")
		assert.Equal(t, int64(3), count, "gpu-a100 count")

		// Verify gpu-v100 bucket
		total, count = getAggregatedPosition(t, pool, ctx, "CLUSTER-01", "GPU_HOUR", "gpu-v100")
		assert.True(t, decimal.NewFromFloat(100.0).Equal(total), "gpu-v100 total")
		assert.Equal(t, int64(1), count, "gpu-v100 count")

		// Verify gpu-t4 bucket
		total, count = getAggregatedPosition(t, pool, ctx, "CLUSTER-01", "GPU_HOUR", "gpu-t4")
		assert.True(t, decimal.NewFromFloat(60.0).Equal(total), "gpu-t4 total")
		assert.Equal(t, int64(4), count, "gpu-t4 count")
	})

	t.Run("Verify account-wide aggregation returns all buckets", func(t *testing.T) {
		tenantID, _ := tenant.FromContext(ctx)
		schemaName := tenantID.SchemaName()

		tx, err := pool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()

		_, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s, public", pq.QuoteIdentifier(schemaName)))
		require.NoError(t, err)

		// Query all aggregated positions for the account/instrument
		rows, err := tx.Query(ctx, `
			SELECT bucket_key, SUM(amount) as total, COUNT(*) as cnt
			FROM position
			WHERE account_id = $1 AND instrument_code = $2 AND deleted_at IS NULL
			GROUP BY bucket_key
			ORDER BY bucket_key`,
			"CLUSTER-01", "GPU_HOUR",
		)
		require.NoError(t, err)
		defer rows.Close()

		aggregates := make(map[string]decimal.Decimal)
		for rows.Next() {
			var bucketKey string
			var total decimal.Decimal
			var cnt int64
			require.NoError(t, rows.Scan(&bucketKey, &total, &cnt))
			aggregates[bucketKey] = total
		}

		require.NoError(t, tx.Commit(ctx))

		// Verify all buckets are present
		assert.Len(t, aggregates, 3)
		assert.True(t, aggregates["gpu-a100"].Equal(decimal.NewFromFloat(35.75)))
		assert.True(t, aggregates["gpu-v100"].Equal(decimal.NewFromFloat(100.0)))
		assert.True(t, aggregates["gpu-t4"].Equal(decimal.NewFromFloat(60.0)))
	})
}

// ============================================================================
// E2E Test: Cache Invalidation
// ============================================================================

// TestE2E_CacheInvalidation verifies that the L1 cache correctly invalidates
// when instruments are updated.
func TestE2E_CacheInvalidation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupE2ETestPool(t)
	ctx := setupTenantWithBothSchemas(t, pool, "cache_inv_tenant")

	reg, err := registry.NewPostgresRegistry(pool)
	require.NoError(t, err)

	// Create the CEL compiler and L1 cache
	compiler, err := refcel.NewCompiler()
	require.NoError(t, err)

	l1Cache := cache.NewInstrumentCache(
		cache.WithCacheSize(100),
		cache.WithTTL(5*time.Minute, 0), // No jitter for deterministic testing
	)

	// Create tiered cache (no L2 for this test)
	tieredCache := cache.NewTieredInstrumentCache(l1Cache, nil, reg, compiler)

	t.Run("Cache instrument in DRAFT state", func(t *testing.T) {
		// Create a draft instrument
		def := &registry.InstrumentDefinition{
			Code:                 "CACHE_TEST",
			Version:              1,
			Dimension:            registry.DimensionMonetary,
			Precision:            2,
			ValidationExpression: "true",
			DisplayName:          "Cache Test Original",
		}
		require.NoError(t, reg.CreateDraft(ctx, def))

		// Load into cache
		cached, err := tieredCache.Get(ctx, "CACHE_TEST", 1)
		require.NoError(t, err)
		assert.Equal(t, "Cache Test Original", cached.Definition.DisplayName)

		// Verify it's in cache
		stats := tieredCache.Stats(ctx)
		assert.Equal(t, 1, stats.L1Size)
	})

	t.Run("Update instrument and verify cache invalidation", func(t *testing.T) {
		// Update the instrument
		updates := &registry.InstrumentDefinition{
			DisplayName: "Cache Test Updated",
		}
		require.NoError(t, reg.UpdateDefinition(ctx, "CACHE_TEST", 1, updates))

		// Manually invalidate cache (would normally be triggered by event)
		tieredCache.Invalidate(ctx, "CACHE_TEST", 1)

		// Use await to verify cache returns updated data
		err := await.New().AtMost(2 * time.Second).PollInterval(50 * time.Millisecond).Until(func() bool {
			cached, err := tieredCache.Get(ctx, "CACHE_TEST", 1)
			if err != nil {
				return false
			}
			return cached.Definition.DisplayName == "Cache Test Updated"
		})
		require.NoError(t, err, "cache should return updated instrument after invalidation")
	})

	t.Run("Activate instrument and verify status change in cache", func(t *testing.T) {
		// Activate the instrument
		require.NoError(t, reg.ActivateInstrument(ctx, "CACHE_TEST", 1))

		// Invalidate cache for this code (all versions)
		tieredCache.InvalidateCode(ctx, "CACHE_TEST")

		// Verify cache returns ACTIVE status
		err := await.New().AtMost(2 * time.Second).Until(func() bool {
			cached, err := tieredCache.Get(ctx, "CACHE_TEST", 1)
			if err != nil {
				return false
			}
			return cached.Definition.Status == registry.StatusActive
		})
		require.NoError(t, err, "cache should return ACTIVE status after activation")
	})

	t.Run("InvalidateAll clears entire tenant cache", func(t *testing.T) {
		// Add another instrument to cache
		def2 := &registry.InstrumentDefinition{
			Code:      "CACHE_TEST_2",
			Version:   1,
			Dimension: registry.DimensionEnergy,
			Precision: 3,
		}
		require.NoError(t, reg.CreateDraft(ctx, def2))

		_, err := tieredCache.Get(ctx, "CACHE_TEST_2", 1)
		require.NoError(t, err)

		// Verify cache has entries
		stats := tieredCache.Stats(ctx)
		assert.GreaterOrEqual(t, stats.L1Size, 2)

		// Invalidate all
		tieredCache.InvalidateAll(ctx)

		// Verify cache is empty
		stats = tieredCache.Stats(ctx)
		assert.Equal(t, 0, stats.L1Size)
	})
}

// ============================================================================
// E2E Test: Performance Baselines
// ============================================================================

// TestE2E_PerformanceBaselines verifies that registry lookups and cache hits
// meet performance requirements.
func TestE2E_PerformanceBaselines(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupE2ETestPool(t)
	ctx := setupTenantWithBothSchemas(t, pool, "perf_baseline_tenant")

	reg, err := registry.NewPostgresRegistry(pool)
	require.NoError(t, err)

	// Setup: Create and activate an instrument
	def := &registry.InstrumentDefinition{
		Code:                 "PERF_TEST",
		Version:              1,
		Dimension:            registry.DimensionMonetary,
		Precision:            2,
		ValidationExpression: "parse_int(amount) > 0",
	}
	require.NoError(t, reg.CreateDraft(ctx, def))
	require.NoError(t, reg.ActivateInstrument(ctx, "PERF_TEST", 1))

	t.Run("Registry lookup should complete in reasonable time", func(t *testing.T) {
		// Warm up
		_, err := reg.GetActiveDefinition(ctx, "PERF_TEST")
		require.NoError(t, err)

		// Measure average lookup time over 100 iterations
		iterations := 100
		start := time.Now()
		for i := 0; i < iterations; i++ {
			_, err := reg.GetActiveDefinition(ctx, "PERF_TEST")
			require.NoError(t, err)
		}
		elapsed := time.Since(start)
		avgTime := elapsed / time.Duration(iterations)

		t.Logf("Registry lookup average: %v over %d iterations", avgTime, iterations)

		// Should be under 50ms average (allowing for testcontainer overhead)
		assert.Less(t, avgTime.Milliseconds(), int64(50),
			"registry lookup should average under 50ms, got %v", avgTime)
	})

	t.Run("L1 cache hit should be sub-millisecond", func(t *testing.T) {
		compiler, err := refcel.NewCompiler()
		require.NoError(t, err)

		l1Cache := cache.NewInstrumentCache(
			cache.WithCacheSize(100),
			cache.WithTTL(5*time.Minute, 0),
		)

		tieredCache := cache.NewTieredInstrumentCache(l1Cache, nil, reg, compiler)

		// Prime the cache
		_, err = tieredCache.Get(ctx, "PERF_TEST", 1)
		require.NoError(t, err)

		// Measure cache hit time
		iterations := 1000
		start := time.Now()
		for i := 0; i < iterations; i++ {
			cached, err := tieredCache.Get(ctx, "PERF_TEST", 1)
			require.NoError(t, err)
			require.NotNil(t, cached)
		}
		elapsed := time.Since(start)
		avgTime := elapsed / time.Duration(iterations)

		t.Logf("L1 cache hit average: %v over %d iterations", avgTime, iterations)

		// Cache hit should be very fast (under 1ms)
		assert.Less(t, avgTime.Microseconds(), int64(1000),
			"L1 cache hit should average under 1ms, got %v", avgTime)

		// Verify stats show cache hits (iterations loop hits + priming = iterations+1 total loads,
		// but priming is L1Miss+SourceLoad, so L1Hits = iterations)
		stats := tieredCache.Stats(ctx)
		assert.GreaterOrEqual(t, stats.L1Hits, int64(iterations),
			"L1 cache should show at least %d hits, got %d", iterations, stats.L1Hits)
	})

	t.Run("Position aggregation should be efficient", func(t *testing.T) {
		// Insert 1000 positions
		for i := 0; i < 1000; i++ {
			insertPosition(t, pool, ctx, "PERF-ACC", "PERF_TEST", "default",
				decimal.NewFromFloat(1.0), "Monetary", nil)
		}

		// Measure aggregation time
		start := time.Now()
		total, count := getAggregatedPosition(t, pool, ctx, "PERF-ACC", "PERF_TEST", "default")
		elapsed := time.Since(start)

		t.Logf("Aggregation of %d positions completed in %v", count, elapsed)

		assert.True(t, decimal.NewFromFloat(1000.0).Equal(total))
		assert.Equal(t, int64(1000), count)

		// Should complete in under 100ms for 1000 positions
		assert.Less(t, elapsed.Milliseconds(), int64(100),
			"aggregation of 1000 positions should complete in under 100ms, got %v", elapsed)
	})
}

// ============================================================================
// E2E Test: Async Operations with Await
// ============================================================================

// TestE2E_AsyncOperationsWithAwait demonstrates proper use of the await package
// for testing asynchronous operations without time.Sleep.
func TestE2E_AsyncOperationsWithAwait(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupE2ETestPool(t)
	ctx := setupTenantWithBothSchemas(t, pool, "async_await_tenant")

	reg, err := registry.NewPostgresRegistry(pool)
	require.NoError(t, err)

	t.Run("Wait for instrument to become active", func(t *testing.T) {
		// Create instrument in DRAFT
		def := &registry.InstrumentDefinition{
			Code:      "ASYNC_TEST",
			Version:   1,
			Dimension: registry.DimensionQuantity,
			Precision: 0,
		}
		require.NoError(t, reg.CreateDraft(ctx, def))

		// Simulate async activation (in real scenario, this might be an event handler)
		go func() {
			time.Sleep(100 * time.Millisecond) //nolint:forbidigo // simulates async event handler delay
			_ = reg.ActivateInstrument(ctx, "ASYNC_TEST", 1)
		}()

		// Use await to poll for activation
		err := await.New().
			AtMost(5 * time.Second).
			PollInterval(50 * time.Millisecond).
			Until(func() bool {
				result, err := reg.GetDefinition(ctx, "ASYNC_TEST", 1)
				if err != nil {
					return false
				}
				return result.Status == registry.StatusActive
			})

		require.NoError(t, err, "instrument should become ACTIVE")
	})

	t.Run("Wait for position to appear", func(t *testing.T) {
		// Simulate async position creation
		go func() {
			time.Sleep(150 * time.Millisecond) //nolint:forbidigo // simulates async position creation delay
			insertPosition(t, pool, ctx, "ASYNC-ACC", "ASYNC_TEST", "bucket",
				decimal.NewFromFloat(123.45), "Custom", nil)
		}()

		// Use await to poll for position
		var finalTotal decimal.Decimal
		var finalCount int64

		err := await.New().
			AtMost(5 * time.Second).
			PollInterval(50 * time.Millisecond).
			Until(func() bool {
				total, count := getAggregatedPosition(t, pool, ctx, "ASYNC-ACC", "ASYNC_TEST", "bucket")
				if count > 0 {
					finalTotal = total
					finalCount = count
					return true
				}
				return false
			})

		require.NoError(t, err, "position should be created")
		assert.True(t, decimal.NewFromFloat(123.45).Equal(finalTotal))
		assert.Equal(t, int64(1), finalCount)
	})

	t.Run("UntilNoError for transient failures", func(t *testing.T) {
		attempts := 0

		err := await.New().
			AtMost(2 * time.Second).
			PollInterval(100 * time.Millisecond).
			UntilNoError(func() error {
				attempts++
				if attempts < 3 {
					return fmt.Errorf("simulated transient failure")
				}
				return nil
			})

		require.NoError(t, err)
		assert.GreaterOrEqual(t, attempts, 3, "should have retried at least 3 times")
	})
}

// ============================================================================
// E2E Test: Cross-Service Validation Flow
// ============================================================================

// TestE2E_CrossServiceValidationFlow tests the integration between reference-data
// validation rules and position-keeping data entry.
func TestE2E_CrossServiceValidationFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupE2ETestPool(t)
	ctx := setupTenantWithBothSchemas(t, pool, "cross_service_tenant")

	reg, err := registry.NewPostgresRegistry(pool)
	require.NoError(t, err)

	t.Run("Setup carbon credit instrument with complex validation", func(t *testing.T) {
		// Create a carbon credit instrument with CEL validation
		def := &registry.InstrumentDefinition{
			Code:      "VCU",
			Version:   1,
			Dimension: registry.DimensionQuantity,
			Precision: 0,
			// Validation: must have vintage year >= 2020 and project_type specified
			ValidationExpression: `
				parse_int(attributes.vintage) >= 2020 &&
				has(attributes.project_type) &&
				attributes.project_type in ["forestry", "renewable", "efficiency"]
			`,
			ErrorMessageExpression: `"Invalid VCU: vintage=" + attributes.vintage + ", type=" + attributes.project_type`,
			DisplayName:            "Verified Carbon Unit",
			Description:            "Carbon offset credit from verified projects",
		}
		require.NoError(t, reg.CreateDraft(ctx, def))
		require.NoError(t, reg.ActivateInstrument(ctx, "VCU", 1))
	})

	t.Run("Validate and create positions for valid carbon credits", func(t *testing.T) {
		validCredits := []struct {
			vintage     string
			projectType string
			amount      float64
		}{
			{"2023", "forestry", 100},
			{"2024", "renewable", 50},
			{"2020", "efficiency", 75},
		}

		for _, credit := range validCredits {
			attrs := registry.AttributeBag{
				Amount: fmt.Sprintf("%d", int(credit.amount)),
				Attributes: map[string]string{
					"vintage":      credit.vintage,
					"project_type": credit.projectType,
				},
			}

			// Validate using reference-data service
			result, err := reg.ValidateAttributes(ctx, "VCU", 1, attrs)
			require.NoError(t, err)
			require.True(t, result.Valid, "expected valid for vintage=%s, type=%s", credit.vintage, credit.projectType)

			// If valid, create position in position-keeping service
			insertPosition(t, pool, ctx, "CARBON-ACCOUNT", "VCU",
				fmt.Sprintf("vintage-%s-%s", credit.vintage, credit.projectType),
				decimal.NewFromFloat(credit.amount), "Carbon",
				map[string]string{
					"vintage":      credit.vintage,
					"project_type": credit.projectType,
				},
			)
		}

		// Verify total credits
		tenantID, _ := tenant.FromContext(ctx)
		schemaName := tenantID.SchemaName()

		tx, err := pool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()

		_, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s, public", pq.QuoteIdentifier(schemaName)))
		require.NoError(t, err)

		var totalCredits decimal.Decimal
		err = tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(amount), 0)
			FROM position
			WHERE account_id = 'CARBON-ACCOUNT' AND instrument_code = 'VCU' AND deleted_at IS NULL
		`).Scan(&totalCredits)
		require.NoError(t, err)

		require.NoError(t, tx.Commit(ctx))

		// 100 + 50 + 75 = 225
		assert.True(t, decimal.NewFromFloat(225.0).Equal(totalCredits))
	})

	t.Run("Reject positions with invalid attributes", func(t *testing.T) {
		invalidCredits := []struct {
			vintage     string
			projectType string
			reason      string
		}{
			{"2019", "forestry", "vintage too old"},
			{"2023", "mining", "invalid project type"},
			{"2024", "", "missing project type"},
		}

		for _, credit := range invalidCredits {
			attrs := registry.AttributeBag{
				Amount: "10",
				Attributes: map[string]string{
					"vintage":      credit.vintage,
					"project_type": credit.projectType,
				},
			}

			result, err := reg.ValidateAttributes(ctx, "VCU", 1, attrs)
			require.NoError(t, err)
			assert.False(t, result.Valid, "expected invalid for: %s", credit.reason)
			t.Logf("Validation rejected (%s): %s", credit.reason, result.ErrorMessage)
		}
	})
}

// ============================================================================
// E2E Test: Instrument Version Migration
// ============================================================================

// TestE2E_InstrumentVersionMigration tests creating a new version of an instrument
// and deprecating the old one with a successor reference.
func TestE2E_InstrumentVersionMigration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupE2ETestPool(t)
	ctx := setupTenantWithBothSchemas(t, pool, "version_migration_tenant")

	reg, err := registry.NewPostgresRegistry(pool)
	require.NoError(t, err)

	t.Run("Create and activate v1 instrument", func(t *testing.T) {
		v1 := &registry.InstrumentDefinition{
			Code:                 "ENERGY_TOKEN",
			Version:              1,
			Dimension:            registry.DimensionEnergy,
			Precision:            2,
			ValidationExpression: "parse_int(amount) > 0",
			DisplayName:          "Energy Token v1",
		}
		require.NoError(t, reg.CreateDraft(ctx, v1))
		require.NoError(t, reg.ActivateInstrument(ctx, "ENERGY_TOKEN", 1))

		// Create some positions
		insertPosition(t, pool, ctx, "ACC-V1", "ENERGY_TOKEN", "bucket",
			decimal.NewFromFloat(100.0), "Energy", nil)
	})

	t.Run("Create v2 with enhanced validation", func(t *testing.T) {
		v2 := &registry.InstrumentDefinition{
			Code:      "ENERGY_TOKEN",
			Version:   2,
			Dimension: registry.DimensionEnergy,
			Precision: 4, // Increased precision
			// Enhanced validation with source tracking
			ValidationExpression:   `parse_int(amount) > 0 && has(attributes.source)`,
			ErrorMessageExpression: `"v2 requires source attribute"`,
			DisplayName:            "Energy Token v2",
		}
		require.NoError(t, reg.CreateDraft(ctx, v2))
		require.NoError(t, reg.ActivateInstrument(ctx, "ENERGY_TOKEN", 2))
	})

	t.Run("Deprecate v1 with v2 as successor", func(t *testing.T) {
		// Get v2's ID
		v2Def, err := reg.GetDefinition(ctx, "ENERGY_TOKEN", 2)
		require.NoError(t, err)

		// Deprecate v1 with v2 as successor
		err = reg.DeprecateInstrument(ctx, "ENERGY_TOKEN", 1, &v2Def.ID)
		require.NoError(t, err)

		// Verify v1 is deprecated with successor
		v1Def, err := reg.GetDefinition(ctx, "ENERGY_TOKEN", 1)
		require.NoError(t, err)
		assert.Equal(t, registry.StatusDeprecated, v1Def.Status)
		assert.NotNil(t, v1Def.SuccessorID)
		assert.Equal(t, v2Def.ID, *v1Def.SuccessorID)
	})

	t.Run("GetActiveDefinition returns v2", func(t *testing.T) {
		active, err := reg.GetActiveDefinition(ctx, "ENERGY_TOKEN")
		require.NoError(t, err)
		assert.Equal(t, 2, active.Version)
		assert.Equal(t, registry.StatusActive, active.Status)
	})

	t.Run("Existing v1 positions remain valid", func(t *testing.T) {
		total, count := getAggregatedPosition(t, pool, ctx, "ACC-V1", "ENERGY_TOKEN", "bucket")
		assert.True(t, decimal.NewFromFloat(100.0).Equal(total))
		assert.Equal(t, int64(1), count)
	})

	t.Run("New positions can use v2 validation", func(t *testing.T) {
		// Validate with v2 rules (requires source)
		result, err := reg.ValidateAttributes(ctx, "ENERGY_TOKEN", 2, registry.AttributeBag{
			Amount:     "50",
			Attributes: map[string]string{"source": "solar"},
		})
		require.NoError(t, err)
		assert.True(t, result.Valid)

		// Reject without source
		result, err = reg.ValidateAttributes(ctx, "ENERGY_TOKEN", 2, registry.AttributeBag{
			Amount:     "50",
			Attributes: map[string]string{},
		})
		require.NoError(t, err)
		assert.False(t, result.Valid)
	})
}

// ============================================================================
// E2E Test: High-Volume Position Aggregation
// ============================================================================

// TestE2E_HighVolumeAggregation tests position aggregation performance at scale.
func TestE2E_HighVolumeAggregation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupE2ETestPool(t)
	ctx := setupTenantWithBothSchemas(t, pool, "high_volume_tenant")

	// Seed a system instrument for testing
	seedSystemInstrument(t, pool, ctx, "KWH", "ENERGY", 4)

	t.Run("Insert and aggregate 10,000 positions across 100 buckets", func(t *testing.T) {
		const numBuckets = 100
		const positionsPerBucket = 100 // 10,000 total

		tenantID, _ := tenant.FromContext(ctx)
		schemaName := tenantID.SchemaName()

		// Bulk insert using COPY for performance
		tx, err := pool.Begin(ctx)
		require.NoError(t, err)

		_, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s, public", pq.QuoteIdentifier(schemaName)))
		require.NoError(t, err)

		// Prepare batch insert
		stmt, err := tx.Prepare(ctx, "insert_position", `
			INSERT INTO position (id, created_at, created_by, account_id, instrument_code, bucket_key, amount, dimension)
			VALUES ($1, NOW(), 'bulk-insert', $2, $3, $4, $5, $6)
		`)
		require.NoError(t, err)

		insertStart := time.Now()
		for b := 0; b < numBuckets; b++ {
			bucketKey := fmt.Sprintf("meter-%03d", b)
			for p := 0; p < positionsPerBucket; p++ {
				_, err := tx.Exec(ctx, stmt.Name,
					uuid.New(),
					"HIGH-VOL-ACC",
					"KWH",
					bucketKey,
					decimal.NewFromFloat(1.5), // Each position = 1.5 KWH
					"Energy",
				)
				require.NoError(t, err)
			}
		}
		require.NoError(t, tx.Commit(ctx))
		insertDuration := time.Since(insertStart)

		t.Logf("Inserted %d positions in %v", numBuckets*positionsPerBucket, insertDuration)

		// Measure aggregation time
		tx, err = pool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()

		_, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s, public", pq.QuoteIdentifier(schemaName)))
		require.NoError(t, err)

		aggregateStart := time.Now()
		rows, err := tx.Query(ctx, `
			SELECT bucket_key, SUM(amount) as total, COUNT(*) as cnt
			FROM position
			WHERE account_id = 'HIGH-VOL-ACC' AND instrument_code = 'KWH' AND deleted_at IS NULL
			GROUP BY bucket_key
			ORDER BY bucket_key
		`)
		require.NoError(t, err)

		var totalSum decimal.Decimal
		bucketCount := 0
		for rows.Next() {
			var bucketKey string
			var total decimal.Decimal
			var cnt int64
			require.NoError(t, rows.Scan(&bucketKey, &total, &cnt))
			totalSum = totalSum.Add(total)
			bucketCount++
		}
		rows.Close()
		aggregateDuration := time.Since(aggregateStart)

		require.NoError(t, tx.Commit(ctx))

		t.Logf("Aggregated %d buckets in %v", bucketCount, aggregateDuration)

		// Verify results
		assert.Equal(t, numBuckets, bucketCount)
		expectedTotal := decimal.NewFromFloat(float64(numBuckets*positionsPerBucket) * 1.5) // 15,000
		assert.True(t, expectedTotal.Equal(totalSum),
			"expected total %s, got %s", expectedTotal.String(), totalSum.String())

		// Performance assertion: aggregation should complete in under 100ms
		assert.Less(t, aggregateDuration.Milliseconds(), int64(100),
			"aggregation of %d positions should complete in under 100ms, got %v",
			numBuckets*positionsPerBucket, aggregateDuration)
	})
}

// ============================================================================
// Benchmark Tests
// ============================================================================

// BenchmarkRegistryLookup benchmarks registry lookup performance.
func BenchmarkRegistryLookup(b *testing.B) {
	pool := testdb.NewTestPool(&testing.T{})
	ctx, cleanup := testdb.SetupTenantSchemaForPgx(&testing.T{}, pool, "bench_tenant", "reference-data")
	defer cleanup()

	reg, err := registry.NewPostgresRegistry(pool)
	if err != nil {
		b.Fatal(err)
	}

	def := &registry.InstrumentDefinition{
		Code:      "BENCH",
		Version:   1,
		Dimension: registry.DimensionMonetary,
		Precision: 2,
	}
	if err := reg.CreateDraft(ctx, def); err != nil {
		b.Fatal(err)
	}
	if err := reg.ActivateInstrument(ctx, "BENCH", 1); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := reg.GetActiveDefinition(ctx, "BENCH")
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkL1CacheHit benchmarks L1 cache hit performance.
func BenchmarkL1CacheHit(b *testing.B) {
	pool := testdb.NewTestPool(&testing.T{})
	ctx, cleanup := testdb.SetupTenantSchemaForPgx(&testing.T{}, pool, "bench_cache_tenant", "reference-data")
	defer cleanup()

	reg, err := registry.NewPostgresRegistry(pool)
	if err != nil {
		b.Fatal(err)
	}

	compiler, err := refcel.NewCompiler()
	if err != nil {
		b.Fatal(err)
	}

	l1Cache := cache.NewInstrumentCache(
		cache.WithCacheSize(100),
		cache.WithTTL(5*time.Minute, 0),
	)

	tieredCache := cache.NewTieredInstrumentCache(l1Cache, nil, reg, compiler)

	def := &registry.InstrumentDefinition{
		Code:      "BENCH_CACHE",
		Version:   1,
		Dimension: registry.DimensionMonetary,
		Precision: 2,
	}
	if err := reg.CreateDraft(ctx, def); err != nil {
		b.Fatal(err)
	}
	if err := reg.ActivateInstrument(ctx, "BENCH_CACHE", 1); err != nil {
		b.Fatal(err)
	}

	// Prime the cache
	_, err = tieredCache.Get(ctx, "BENCH_CACHE", 1)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := tieredCache.Get(ctx, "BENCH_CACHE", 1)
		if err != nil {
			b.Fatal(err)
		}
	}
}
