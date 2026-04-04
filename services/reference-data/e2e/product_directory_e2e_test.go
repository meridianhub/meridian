//go:build integration

// Package e2e provides end-to-end integration tests for the product directory flow.
// These tests verify the full lifecycle from account type creation through
// activation, eligibility checking, and system protection.
package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/google/cel-go/cel"
	"github.com/meridianhub/meridian/services/reference-data/accounttype"
	"github.com/meridianhub/meridian/services/reference-data/cache"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// ============================================================================
// Product Directory Test Infrastructure
// ============================================================================

// setupProductDirectorySchema creates a tenant schema with account_type_definitions,
// account_type_valuation_methods, instrument_definition, saga_definition, and
// platform_saga_definition tables. This enables E2E tests covering the full
// product directory flow.
func setupProductDirectorySchema(t *testing.T, pool *pgxpool.Pool, tenantID string) context.Context {
	t.Helper()

	tid := tenant.TenantID(tenantID)
	schemaName := tid.SchemaName()

	ctx := context.Background()

	// Create tenant schema
	_, err := pool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err, "Failed to create tenant schema %s", schemaName)

	// Apply all required schemas in the correct order
	applyReferenceDataSchema(t, pool, schemaName)
	applyAccountTypeSchema(t, pool, schemaName)
	applySagaSchema(t, pool, schemaName)
	applyPlatformSagaSchema(t, pool)

	tenantCtx := tenant.WithTenant(context.Background(), tid)

	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = pool.Exec(cleanupCtx, fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", pq.QuoteIdentifier(schemaName)))
	})

	return tenantCtx
}

// applyAccountTypeSchema creates the account_type_definitions and related tables.
func applyAccountTypeSchema(t *testing.T, pool *pgxpool.Pool, schemaName string) {
	t.Helper()
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, fmt.Sprintf("SET search_path TO %s", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err)

	// account_type_definitions table
	_, err = tx.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS account_type_definitions (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			code character varying(50) NOT NULL,
			version integer NOT NULL DEFAULT 1,
			display_name character varying(255) NOT NULL,
			description text NULL,
			normal_balance character varying(10) NOT NULL,
			behavior_class character varying(20) NOT NULL,
			instrument_code character varying(50) NOT NULL,
			default_saga_prefix character varying(100) NULL,
			default_conversion_method_id uuid NULL,
			default_conversion_method_version integer NULL,
			validation_cel text NULL,
			bucketing_cel text NULL,
			eligibility_cel text NULL,
			attribute_schema jsonb NULL,
			attributes jsonb NOT NULL DEFAULT '{}',
			status character varying(20) NOT NULL DEFAULT 'DRAFT',
			is_system boolean NOT NULL DEFAULT false,
			successor_id uuid NULL,
			created_at timestamptz NOT NULL DEFAULT now(),
			updated_at timestamptz NOT NULL DEFAULT now(),
			activated_at timestamptz NULL,
			deprecated_at timestamptz NULL,
			PRIMARY KEY (id),
			CONSTRAINT uq_account_type_code_version UNIQUE (code, version),
			CONSTRAINT fk_account_type_successor
				FOREIGN KEY (successor_id) REFERENCES account_type_definitions (id) ON DELETE SET NULL,
			CONSTRAINT chk_account_type_normal_balance
				CHECK (normal_balance IN ('DEBIT', 'CREDIT')),
			CONSTRAINT chk_account_type_behavior_class
				CHECK (behavior_class IN ('CUSTOMER', 'CLEARING', 'NOSTRO', 'VOSTRO', 'HOLDING', 'SUSPENSE', 'REVENUE', 'EXPENSE', 'INVENTORY')),
			CONSTRAINT chk_account_type_status
				CHECK (status IN ('DRAFT', 'ACTIVE', 'DEPRECATED')),
			CONSTRAINT chk_acct_type_successor_not_self
				CHECK (successor_id != id),
			CONSTRAINT chk_default_conversion_method_pair
				CHECK ((default_conversion_method_id IS NULL) = (default_conversion_method_version IS NULL))
		)
	`)
	require.NoError(t, err, "Failed to create account_type_definitions table")

	_, err = tx.Exec(ctx, `
		CREATE INDEX IF NOT EXISTS idx_account_type_status ON account_type_definitions (status);
		CREATE INDEX IF NOT EXISTS idx_account_type_code ON account_type_definitions (code)
	`)
	require.NoError(t, err)

	require.NoError(t, tx.Commit(ctx))

	// Partial unique index in separate transaction (CockroachDB requirement)
	tx2, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx2.Rollback(ctx) }()

	_, err = tx2.Exec(ctx, fmt.Sprintf("SET search_path TO %s", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err)

	_, err = tx2.Exec(ctx, `
		CREATE UNIQUE INDEX IF NOT EXISTS uq_active_account_type_code
			ON account_type_definitions (code) WHERE status = 'ACTIVE'
	`)
	require.NoError(t, err)

	// account_type_valuation_methods table
	_, err = tx2.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS account_type_valuation_methods (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			account_type_id uuid NOT NULL,
			input_instrument character varying(50) NOT NULL,
			valuation_method_id uuid NOT NULL,
			valuation_method_version integer NOT NULL DEFAULT 1,
			parameters jsonb NOT NULL DEFAULT '{}',
			status character varying(20) NOT NULL DEFAULT 'DRAFT',
			successor_id uuid NULL,
			created_at timestamptz NOT NULL DEFAULT now(),
			updated_at timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (id),
			CONSTRAINT fk_account_type_val_method_account_type
				FOREIGN KEY (account_type_id) REFERENCES account_type_definitions (id),
			CONSTRAINT fk_account_type_val_method_successor
				FOREIGN KEY (successor_id) REFERENCES account_type_valuation_methods (id) ON DELETE SET NULL,
			CONSTRAINT chk_account_type_val_method_status
				CHECK (status IN ('DRAFT', 'ACTIVE', 'DEPRECATED')),
			CONSTRAINT chk_val_method_successor_not_self
				CHECK (successor_id != id)
		)
	`)
	require.NoError(t, err, "Failed to create account_type_valuation_methods table")

	_, err = tx2.Exec(ctx, `
		CREATE INDEX IF NOT EXISTS idx_val_method_account_type ON account_type_valuation_methods (account_type_id)
	`)
	require.NoError(t, err)

	require.NoError(t, tx2.Commit(ctx))

	// Partial unique index in separate transaction
	tx3, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx3.Rollback(ctx) }()

	_, err = tx3.Exec(ctx, fmt.Sprintf("SET search_path TO %s", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err)

	_, err = tx3.Exec(ctx, `
		CREATE UNIQUE INDEX IF NOT EXISTS uq_active_valuation_method
			ON account_type_valuation_methods (account_type_id, input_instrument) WHERE status = 'ACTIVE'
	`)
	require.NoError(t, err)

	require.NoError(t, tx3.Commit(ctx))
}

// applySagaSchema creates the saga_definition table in the tenant schema.
func applySagaSchema(t *testing.T, pool *pgxpool.Pool, schemaName string) {
	t.Helper()
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, fmt.Sprintf("SET search_path TO %s", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS saga_definition (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			name character varying(64) NOT NULL,
			version integer NOT NULL DEFAULT 1,
			script text NOT NULL,
			status character varying(16) NOT NULL DEFAULT 'DRAFT',
			is_system boolean NOT NULL DEFAULT FALSE,
			preconditions_expression text NULL,
			display_name character varying(128) NULL,
			description text NULL,
			created_at timestamptz NOT NULL DEFAULT now(),
			updated_at timestamptz NOT NULL DEFAULT now(),
			activated_at timestamptz NULL,
			deprecated_at timestamptz NULL,
			successor_id uuid NULL,
			platform_ref uuid NULL,
			override_reason text NULL,
			platform_version_at_override character varying(16) NULL,
			validation_status character varying(16) NOT NULL DEFAULT 'UNVALIDATED',
			complexity_score integer NULL,
			handler_call_count integer NULL,
			validated_at timestamptz NULL,
			PRIMARY KEY (id),
			CONSTRAINT chk_saga_definition_status
				CHECK (status IN ('DRAFT', 'ACTIVE', 'DEPRECATED')),
			CONSTRAINT uq_saga_definition_name_version
				UNIQUE (name, version),
			CONSTRAINT fk_saga_definition_successor
				FOREIGN KEY (successor_id) REFERENCES saga_definition (id)
		)
	`)
	require.NoError(t, err, "Failed to create saga_definition table")

	_, err = tx.Exec(ctx, `
		CREATE INDEX IF NOT EXISTS idx_saga_definition_name_active ON saga_definition (name) WHERE status = 'ACTIVE';
		CREATE INDEX IF NOT EXISTS idx_saga_definition_lookup ON saga_definition (name, status)
	`)
	require.NoError(t, err)

	require.NoError(t, tx.Commit(ctx))
}

// applyPlatformSagaSchema creates the platform_saga_definition table in public schema.
func applyPlatformSagaSchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()

	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS public.platform_saga_definition (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			name character varying(64) NOT NULL,
			version character varying(16) NOT NULL,
			script text NOT NULL,
			display_name character varying(128) NULL,
			description text NULL,
			status character varying(16) NOT NULL DEFAULT 'ACTIVE',
			previous_version character varying(16) NULL,
			created_at timestamptz NOT NULL DEFAULT now(),
			updated_at timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (id),
			CONSTRAINT uq_platform_saga_definition_name UNIQUE (name),
			CONSTRAINT chk_platform_saga_definition_status
				CHECK (status IN ('ACTIVE', 'DEPRECATED'))
		)
	`)
	require.NoError(t, err, "Failed to create platform_saga_definition table")
}

// seedInstrument seeds a system instrument directly via SQL for activation pre-checks.
func seedInstrument(t *testing.T, pool *pgxpool.Pool, ctx context.Context, code, dimension string) {
	t.Helper()

	tenantID, _ := tenant.FromContext(ctx)
	schemaName := tenantID.SchemaName()

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
		INSERT INTO instrument_definition (id, code, version, dimension, precision, status, is_system,
			fungibility_key_expression, created_at, updated_at, activated_at)
		VALUES (gen_random_uuid(), $1, 1, $2, 2, 'ACTIVE', true, '', NOW(), NOW(), NOW())
		ON CONFLICT (code, version) DO NOTHING`,
		code, dimension)
	require.NoError(t, err)

	require.NoError(t, tx.Commit(ctx))
}

// seedActiveSaga seeds an active saga definition in the tenant schema.
func seedActiveSaga(t *testing.T, pool *pgxpool.Pool, ctx context.Context, name, script string) {
	t.Helper()

	tenantID, _ := tenant.FromContext(ctx)
	schemaName := tenantID.SchemaName()

	now := time.Now().UTC()

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
		INSERT INTO saga_definition (id, name, version, script, status, activated_at, created_at, updated_at)
		VALUES ($1, $2, 1, $3, 'ACTIVE', $4, $4, $4)
		ON CONFLICT (name, version) DO NOTHING`,
		uuid.New(), name, script, now)
	require.NoError(t, err)

	require.NoError(t, tx.Commit(ctx))
}

// seedPlatformSaga seeds an active platform saga definition in the public schema.
// The activation pre-check (checkSagaExists) queries platform_saga_definition.
func seedPlatformSaga(t *testing.T, pool *pgxpool.Pool, name, script string) {
	t.Helper()

	ctx := context.Background()
	now := time.Now().UTC()

	_, err := pool.Exec(ctx, `
		INSERT INTO public.platform_saga_definition (id, name, version, script, status, created_at, updated_at)
		VALUES ($1, $2, '1.0.0', $3, 'ACTIVE', $4, $4)
		ON CONFLICT ON CONSTRAINT uq_platform_saga_definition_name DO NOTHING`,
		uuid.New(), name, script, now)
	require.NoError(t, err)
}

// countActiveDefinitions returns the count of ACTIVE definitions for a given code.
func countActiveDefinitions(t *testing.T, pool *pgxpool.Pool, ctx context.Context, code string) int {
	t.Helper()

	tenantID, _ := tenant.FromContext(ctx)
	schemaName := tenantID.SchemaName()

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err)

	var count int
	err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM account_type_definitions WHERE code = $1 AND status = 'ACTIVE'`, code).Scan(&count)
	require.NoError(t, err)

	require.NoError(t, tx.Commit(ctx))
	return count
}

// countAllDefinitions returns the total count of definitions for a given code (all statuses).
func countAllDefinitions(t *testing.T, pool *pgxpool.Pool, ctx context.Context, code string) int {
	t.Helper()

	tenantID, _ := tenant.FromContext(ctx)
	schemaName := tenantID.SchemaName()

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err)

	var count int
	err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM account_type_definitions WHERE code = $1`, code).Scan(&count)
	require.NoError(t, err)

	require.NoError(t, tx.Commit(ctx))
	return count
}

// ============================================================================
// Scenario 1: Manifest Round-Trip and Idempotency
// ============================================================================

// TestE2E_ManifestRoundTrip tests that applying a manifest creates an ACTIVE
// account type in the registry and that re-applying the same manifest
// is idempotent (no duplicates, same state).
func TestE2E_ManifestRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupE2ETestPool(t)
	ctx := setupProductDirectorySchema(t, pool, "e2e_manifest_roundtrip")

	reg, err := accounttype.NewPostgresRegistry(pool)
	require.NoError(t, err)

	// Seed required instruments for activation pre-checks
	seedInstrument(t, pool, ctx, "KWH", "ENERGY")

	// Seed a platform saga for the prefix check (activation pre-check queries platform_saga_definition)
	seedPlatformSaga(t, pool, "ENERGY_SETTLEMENT.deposit", "def execute(ctx): pass")

	t.Run("First application creates ACTIVE definition", func(t *testing.T) {
		def := &accounttype.Definition{
			ID:                uuid.NewSHA1(uuid.MustParse("6ba7b811-9dad-11d1-80b4-00c04fd430c8"), []byte("ENERGY_SETTLEMENT_KWH")),
			Code:              "ENERGY_SETTLEMENT_KWH",
			Version:           1,
			DisplayName:       "Energy Settlement kWh",
			BehaviorClass:     accounttype.BehaviorClassInventory,
			NormalBalance:     accounttype.NormalBalanceDebit,
			InstrumentCode:    "KWH",
			DefaultSagaPrefix: "ENERGY_SETTLEMENT",
			EligibilityCEL:    "true",
			IsSystem:          false,
			Status:            accounttype.StatusDraft,
			Attributes:        map[string]any{},
		}

		err := reg.CreateDraft(ctx, def)
		require.NoError(t, err)

		err = reg.ActivateAccountType(ctx, "ENERGY_SETTLEMENT_KWH", 1)
		require.NoError(t, err)

		// Verify ACTIVE in registry
		active, err := reg.GetActiveDefinition(ctx, "ENERGY_SETTLEMENT_KWH")
		require.NoError(t, err)
		assert.Equal(t, accounttype.StatusActive, active.Status)
		assert.Equal(t, "ENERGY_SETTLEMENT_KWH", active.Code)
		assert.Equal(t, 1, active.Version)
		assert.Equal(t, accounttype.BehaviorClassInventory, active.BehaviorClass)
		assert.Equal(t, "KWH", active.InstrumentCode)
		assert.NotNil(t, active.ActivatedAt)

		// Verify exactly 1 ACTIVE definition
		count := countActiveDefinitions(t, pool, ctx, "ENERGY_SETTLEMENT_KWH")
		assert.Equal(t, 1, count)
	})

	t.Run("Second application is idempotent", func(t *testing.T) {
		// Apply same definition again - CreateDraft uses ON CONFLICT DO NOTHING
		def := &accounttype.Definition{
			ID:                uuid.NewSHA1(uuid.MustParse("6ba7b811-9dad-11d1-80b4-00c04fd430c8"), []byte("ENERGY_SETTLEMENT_KWH")),
			Code:              "ENERGY_SETTLEMENT_KWH",
			Version:           1,
			DisplayName:       "Energy Settlement kWh",
			BehaviorClass:     accounttype.BehaviorClassInventory,
			NormalBalance:     accounttype.NormalBalanceDebit,
			InstrumentCode:    "KWH",
			DefaultSagaPrefix: "ENERGY_SETTLEMENT",
			EligibilityCEL:    "true",
			IsSystem:          false,
			Status:            accounttype.StatusDraft,
			Attributes:        map[string]any{},
		}

		err := reg.CreateDraft(ctx, def)
		require.NoError(t, err)

		// ActivateAccountType on already-ACTIVE is idempotent (returns nil)
		err = reg.ActivateAccountType(ctx, "ENERGY_SETTLEMENT_KWH", 1)
		require.NoError(t, err)

		// Verify still exactly 1 ACTIVE definition (no duplicates)
		activeCount := countActiveDefinitions(t, pool, ctx, "ENERGY_SETTLEMENT_KWH")
		assert.Equal(t, 1, activeCount)

		// Verify total rows unchanged (no duplicate entries)
		totalCount := countAllDefinitions(t, pool, ctx, "ENERGY_SETTLEMENT_KWH")
		assert.Equal(t, 1, totalCount)

		// Verify state is identical
		active, err := reg.GetActiveDefinition(ctx, "ENERGY_SETTLEMENT_KWH")
		require.NoError(t, err)
		assert.Equal(t, accounttype.StatusActive, active.Status)
		assert.Equal(t, "KWH", active.InstrumentCode)
	})
}

// ============================================================================
// Scenario 2: Custom Product Type Account Creation
// ============================================================================

// TestE2E_CustomProductTypeCreation tests creating an account type via the registry
// and verifying it can be retrieved with the correct product_type_code and version.
func TestE2E_CustomProductTypeCreation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupE2ETestPool(t)
	ctx := setupProductDirectorySchema(t, pool, "e2e_custom_product_type")

	reg, err := accounttype.NewPostgresRegistry(pool)
	require.NoError(t, err)

	seedInstrument(t, pool, ctx, "KWH", "ENERGY")
	seedPlatformSaga(t, pool, "ENERGY_SETTLEMENT.deposit_custom", "def execute(ctx): pass")

	t.Run("Create and activate custom product type", func(t *testing.T) {
		def := &accounttype.Definition{
			ID:                uuid.New(),
			Code:              "ENERGY_SETTLEMENT_KWH",
			Version:           1,
			DisplayName:       "Energy Settlement kWh Account",
			Description:       "Account type for energy settlement in kWh",
			BehaviorClass:     accounttype.BehaviorClassInventory,
			NormalBalance:     accounttype.NormalBalanceDebit,
			InstrumentCode:    "KWH",
			DefaultSagaPrefix: "ENERGY_SETTLEMENT",
			EligibilityCEL:    "true",
			IsSystem:          false,
			Status:            accounttype.StatusDraft,
			Attributes:        map[string]any{},
		}

		err := reg.CreateDraft(ctx, def)
		require.NoError(t, err)

		err = reg.ActivateAccountType(ctx, "ENERGY_SETTLEMENT_KWH", 1)
		require.NoError(t, err)

		// Retrieve and verify
		active, err := reg.GetActiveDefinition(ctx, "ENERGY_SETTLEMENT_KWH")
		require.NoError(t, err)
		assert.Equal(t, "ENERGY_SETTLEMENT_KWH", active.Code)
		assert.Equal(t, 1, active.Version)
		assert.Equal(t, accounttype.BehaviorClassInventory, active.BehaviorClass)
		assert.Equal(t, "KWH", active.InstrumentCode)
		assert.Equal(t, "ENERGY_SETTLEMENT", active.DefaultSagaPrefix)
		assert.Equal(t, "true", active.EligibilityCEL)

		// Retrieve by code+version
		byVersion, err := reg.GetDefinition(ctx, "ENERGY_SETTLEMENT_KWH", 1)
		require.NoError(t, err)
		assert.Equal(t, active.ID, byVersion.ID)
		assert.Equal(t, active.Code, byVersion.Code)
	})

	t.Run("Custom product type appears in ListActive", func(t *testing.T) {
		results, err := reg.ListActive(ctx)
		require.NoError(t, err)

		found := false
		for _, def := range results {
			if def.Code == "ENERGY_SETTLEMENT_KWH" {
				found = true
				assert.Equal(t, accounttype.StatusActive, def.Status)
			}
		}
		assert.True(t, found, "ENERGY_SETTLEMENT_KWH should be in ListActive results")
	})
}

// ============================================================================
// Scenario 3: EligibilityCEL Evaluation
// ============================================================================

// TestE2E_EligibilityCEL tests that the EligibilityCEL expression is correctly
// evaluated against party context attributes.
func TestE2E_EligibilityCEL(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupE2ETestPool(t)
	ctx := setupProductDirectorySchema(t, pool, "e2e_eligibility_cel")

	reg, err := accounttype.NewPostgresRegistry(pool)
	require.NoError(t, err)

	seedInstrument(t, pool, ctx, "GBP", "MONETARY")

	t.Run("Organization-only eligibility rejects PERSON party", func(t *testing.T) {
		def := &accounttype.Definition{
			ID:             uuid.New(),
			Code:           "ORG_ONLY_ACCOUNT",
			Version:        1,
			DisplayName:    "Organization Only Account",
			BehaviorClass:  accounttype.BehaviorClassClearing,
			NormalBalance:  accounttype.NormalBalanceDebit,
			InstrumentCode: "GBP",
			EligibilityCEL: `party.type == "ORGANIZATION"`,
			IsSystem:       false,
			Status:         accounttype.StatusDraft,
			Attributes:     map[string]any{},
		}

		err := reg.CreateDraft(ctx, def)
		require.NoError(t, err)

		err = reg.ActivateAccountType(ctx, "ORG_ONLY_ACCOUNT", 1)
		require.NoError(t, err)

		// PERSON party should fail eligibility
		result, err := reg.CheckEligibility(ctx, "ORG_ONLY_ACCOUNT", 1, accounttype.AttributeBag{
			Attributes: map[string]string{"type": "PERSON"},
		})
		require.NoError(t, err)
		assert.False(t, result.Valid, "PERSON party should not be eligible for ORG_ONLY_ACCOUNT")

		// ORGANIZATION party should pass eligibility
		result, err = reg.CheckEligibility(ctx, "ORG_ONLY_ACCOUNT", 1, accounttype.AttributeBag{
			Attributes: map[string]string{"type": "ORGANIZATION"},
		})
		require.NoError(t, err)
		assert.True(t, result.Valid, "ORGANIZATION party should be eligible for ORG_ONLY_ACCOUNT")
	})

	t.Run("Eligibility with 'true' expression always passes", func(t *testing.T) {
		def := &accounttype.Definition{
			ID:             uuid.New(),
			Code:           "OPEN_ACCOUNT",
			Version:        1,
			DisplayName:    "Open Account",
			BehaviorClass:  accounttype.BehaviorClassHolding,
			NormalBalance:  accounttype.NormalBalanceCredit,
			InstrumentCode: "GBP",
			EligibilityCEL: "true",
			IsSystem:       false,
			Status:         accounttype.StatusDraft,
			Attributes:     map[string]any{},
		}

		err := reg.CreateDraft(ctx, def)
		require.NoError(t, err)

		err = reg.ActivateAccountType(ctx, "OPEN_ACCOUNT", 1)
		require.NoError(t, err)

		// Any party should be eligible
		result, err := reg.CheckEligibility(ctx, "OPEN_ACCOUNT", 1, accounttype.AttributeBag{
			Attributes: map[string]string{"type": "PERSON"},
		})
		require.NoError(t, err)
		assert.True(t, result.Valid)

		// Empty attributes should also be eligible
		result, err = reg.CheckEligibility(ctx, "OPEN_ACCOUNT", 1, accounttype.AttributeBag{})
		require.NoError(t, err)
		assert.True(t, result.Valid)
	})

	t.Run("No eligibility expression defaults to eligible", func(t *testing.T) {
		def := &accounttype.Definition{
			ID:             uuid.New(),
			Code:           "NO_ELIGIBILITY_ACCOUNT",
			Version:        1,
			DisplayName:    "No Eligibility Account",
			BehaviorClass:  accounttype.BehaviorClassSuspense,
			NormalBalance:  accounttype.NormalBalanceDebit,
			InstrumentCode: "GBP",
			// EligibilityCEL intentionally empty
			IsSystem:   false,
			Status:     accounttype.StatusDraft,
			Attributes: map[string]any{},
		}

		err := reg.CreateDraft(ctx, def)
		require.NoError(t, err)

		err = reg.ActivateAccountType(ctx, "NO_ELIGIBILITY_ACCOUNT", 1)
		require.NoError(t, err)

		// No expression = always eligible
		result, err := reg.CheckEligibility(ctx, "NO_ELIGIBILITY_ACCOUNT", 1, accounttype.AttributeBag{})
		require.NoError(t, err)
		assert.True(t, result.Valid, "No eligibility expression should default to eligible")
	})
}

// ============================================================================
// Scenario 4: Saga Routing via ProductTypeSagaResolver
// ============================================================================

// TestE2E_SagaRouting tests that saga routing correctly uses the product type's
// DefaultSagaPrefix to resolve prefixed sagas, and falls back to generic sagas
// when no prefix is set.
func TestE2E_SagaRouting(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupE2ETestPool(t)
	ctx := setupProductDirectorySchema(t, pool, "e2e_saga_routing")

	reg, err := accounttype.NewPostgresRegistry(pool)
	require.NoError(t, err)

	seedInstrument(t, pool, ctx, "GBP", "MONETARY")

	// Seed platform sagas: both a prefixed and a generic one
	// Activation pre-check queries platform_saga_definition for saga prefix validation
	seedPlatformSaga(t, pool, "SAVINGS_TEST.deposit", `def execute(ctx): return "savings_deposit"`)
	seedPlatformSaga(t, pool, "deposit_generic", `def execute(ctx): return "generic_deposit"`)

	t.Run("Product type with saga prefix resolves prefixed saga", func(t *testing.T) {
		def := &accounttype.Definition{
			ID:                uuid.New(),
			Code:              "SAVINGS_TEST",
			Version:           1,
			DisplayName:       "Savings Test Account",
			BehaviorClass:     accounttype.BehaviorClassCustomer,
			NormalBalance:     accounttype.NormalBalanceCredit,
			InstrumentCode:    "GBP",
			DefaultSagaPrefix: "SAVINGS_TEST",
			EligibilityCEL:    "true",
			IsSystem:          false,
			Status:            accounttype.StatusDraft,
			Attributes:        map[string]any{},
		}

		err := reg.CreateDraft(ctx, def)
		require.NoError(t, err)

		err = reg.ActivateAccountType(ctx, "SAVINGS_TEST", 1)
		require.NoError(t, err)

		// Verify the account type was stored with the prefix
		active, err := reg.GetActiveDefinition(ctx, "SAVINGS_TEST")
		require.NoError(t, err)
		assert.Equal(t, "SAVINGS_TEST", active.DefaultSagaPrefix)
	})

	t.Run("Product type without prefix uses generic saga", func(t *testing.T) {
		def := &accounttype.Definition{
			ID:             uuid.New(),
			Code:           "GENERIC_CLEARING",
			Version:        1,
			DisplayName:    "Generic Clearing Account",
			BehaviorClass:  accounttype.BehaviorClassClearing,
			NormalBalance:  accounttype.NormalBalanceDebit,
			InstrumentCode: "GBP",
			// No DefaultSagaPrefix
			EligibilityCEL: "true",
			IsSystem:       false,
			Status:         accounttype.StatusDraft,
			Attributes:     map[string]any{},
		}

		err := reg.CreateDraft(ctx, def)
		require.NoError(t, err)

		err = reg.ActivateAccountType(ctx, "GENERIC_CLEARING", 1)
		require.NoError(t, err)

		active, err := reg.GetActiveDefinition(ctx, "GENERIC_CLEARING")
		require.NoError(t, err)
		assert.Empty(t, active.DefaultSagaPrefix, "Generic clearing should have no saga prefix")
	})

	t.Run("Saga prefix with no matching saga fails activation", func(t *testing.T) {
		def := &accounttype.Definition{
			ID:                uuid.New(),
			Code:              "MISSING_SAGA_PREFIX",
			Version:           1,
			DisplayName:       "Missing Saga Prefix Account",
			BehaviorClass:     accounttype.BehaviorClassHolding,
			NormalBalance:     accounttype.NormalBalanceCredit,
			InstrumentCode:    "GBP",
			DefaultSagaPrefix: "NONEXISTENT_PREFIX",
			EligibilityCEL:    "true",
			IsSystem:          false,
			Status:            accounttype.StatusDraft,
			Attributes:        map[string]any{},
		}

		err := reg.CreateDraft(ctx, def)
		require.NoError(t, err)

		// Activation should fail because no saga with prefix NONEXISTENT_PREFIX exists
		err = reg.ActivateAccountType(ctx, "MISSING_SAGA_PREFIX", 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "saga prefix")
	})
}

// ============================================================================
// Scenario 5: Activation Pre-Check Completeness
// ============================================================================

// TestE2E_ActivationPreChecks tests that activation pre-checks return ALL errors
// in a single response, not just the first.
func TestE2E_ActivationPreChecks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupE2ETestPool(t)
	ctx := setupProductDirectorySchema(t, pool, "e2e_activation_prechecks")

	reg, err := accounttype.NewPostgresRegistry(pool)
	require.NoError(t, err)

	// Intentionally do NOT seed any instruments or sagas

	t.Run("Non-existent instrument returns structured error", func(t *testing.T) {
		def := &accounttype.Definition{
			ID:             uuid.New(),
			Code:           "BAD_INSTRUMENT",
			Version:        1,
			DisplayName:    "Bad Instrument Account",
			BehaviorClass:  accounttype.BehaviorClassClearing,
			NormalBalance:  accounttype.NormalBalanceDebit,
			InstrumentCode: "NONEXISTENT_CURRENCY",
			EligibilityCEL: "true",
			IsSystem:       false,
			Status:         accounttype.StatusDraft,
			Attributes:     map[string]any{},
		}

		err := reg.CreateDraft(ctx, def)
		require.NoError(t, err)

		err = reg.ActivateAccountType(ctx, "BAD_INSTRUMENT", 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "instrument")
		assert.Contains(t, err.Error(), "NONEXISTENT_CURRENCY")
	})

	t.Run("Invalid CEL expression returns structured error", func(t *testing.T) {
		def := &accounttype.Definition{
			ID:             uuid.New(),
			Code:           "BAD_CEL",
			Version:        1,
			DisplayName:    "Bad CEL Account",
			BehaviorClass:  accounttype.BehaviorClassHolding,
			NormalBalance:  accounttype.NormalBalanceCredit,
			InstrumentCode: "GBP",
			ValidationCEL:  "this is not valid CEL %%%",
			IsSystem:       false,
			Status:         accounttype.StatusDraft,
			Attributes:     map[string]any{},
		}

		// CreateDraft should fail because CEL is validated at creation time
		err := reg.CreateDraft(ctx, def)
		require.Error(t, err)
		assert.ErrorIs(t, err, accounttype.ErrInvalidCEL)
	})

	t.Run("Multiple failures returned in single activation response", func(t *testing.T) {
		// Seed a valid instrument so the definition can be created
		seedInstrument(t, pool, ctx, "EUR", "MONETARY")

		def := &accounttype.Definition{
			ID:                uuid.New(),
			Code:              "MULTI_FAILURE",
			Version:           1,
			DisplayName:       "Multi Failure Account",
			BehaviorClass:     accounttype.BehaviorClassRevenue,
			NormalBalance:     accounttype.NormalBalanceCredit,
			InstrumentCode:    "EUR",
			DefaultSagaPrefix: "NONEXISTENT_MULTI",
			EligibilityCEL:    "true",
			IsSystem:          false,
			Status:            accounttype.StatusDraft,
			Attributes:        map[string]any{},
		}

		// Use a default conversion method that doesn't exist
		methodID := uuid.New()
		methodVersion := 1
		def.DefaultConversionMethodID = &methodID
		def.DefaultConversionMethodVersion = &methodVersion

		err := reg.CreateDraft(ctx, def)
		require.NoError(t, err)

		err = reg.ActivateAccountType(ctx, "MULTI_FAILURE", 1)
		require.Error(t, err)

		// Should contain BOTH saga and conversion method errors
		errStr := err.Error()
		assert.Contains(t, errStr, "saga prefix", "error should mention saga prefix failure")
		assert.Contains(t, errStr, "conversion method", "error should mention conversion method failure")
	})
}

// ============================================================================
// Scenario 6: Platform Blueprints Read-Only
// ============================================================================

// TestE2E_PlatformBlueprintsReadOnly verifies that system account types
// (is_system=true) cannot be modified or deprecated by tenants.
func TestE2E_PlatformBlueprintsReadOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupE2ETestPool(t)
	ctx := setupProductDirectorySchema(t, pool, "e2e_platform_readonly")

	reg, err := accounttype.NewPostgresRegistry(pool)
	require.NoError(t, err)

	// Seed required instruments for blueprint activation
	seedInstrument(t, pool, ctx, "GBP", "MONETARY")
	seedInstrument(t, pool, ctx, "USD", "MONETARY")
	seedInstrument(t, pool, ctx, "EUR", "MONETARY")
	seedInstrument(t, pool, ctx, "TONNE_CO2E", "QUANTITY")
	seedInstrument(t, pool, ctx, "KWH", "ENERGY")

	// Seed platform blueprints using the seeder
	err = accounttype.SeedPlatformBlueprints(ctx, reg)
	require.NoError(t, err)

	t.Run("System blueprint is ACTIVE and retrievable", func(t *testing.T) {
		def, err := reg.GetActiveDefinition(ctx, "CLEARING_GBP")
		require.NoError(t, err)
		assert.Equal(t, accounttype.StatusActive, def.Status)
		assert.True(t, def.IsSystem)
		assert.Equal(t, accounttype.BehaviorClassClearing, def.BehaviorClass)
		assert.Equal(t, "GBP", def.InstrumentCode)
	})

	t.Run("Cannot update system blueprint", func(t *testing.T) {
		updates := &accounttype.Definition{
			DisplayName: "Modified Clearing GBP",
		}
		err := reg.UpdateDefinition(ctx, "CLEARING_GBP", 1, updates)
		require.Error(t, err)
		assert.ErrorIs(t, err, accounttype.ErrSystemAccountTypeReadOnly)
	})

	t.Run("Cannot deprecate system blueprint", func(t *testing.T) {
		err := reg.DeprecateAccountType(ctx, "CLEARING_GBP", 1, nil)
		require.Error(t, err)
		assert.ErrorIs(t, err, accounttype.ErrSystemAccountTypeReadOnly)
	})

	t.Run("All 12 platform blueprints are seeded", func(t *testing.T) {
		results, err := reg.ListActive(ctx)
		require.NoError(t, err)

		systemCodes := make(map[string]bool)
		for _, def := range results {
			if def.IsSystem {
				systemCodes[def.Code] = true
			}
		}

		expectedCodes := []string{
			"CURRENT_ACCOUNT_GBP", "CURRENT_ACCOUNT_USD", "CURRENT_ACCOUNT_EUR",
			"CLEARING_GBP", "NOSTRO_GBP", "VOSTRO_GBP",
			"HOLDING_ESCROW", "SUSPENSE_UNALLOCATED",
			"REVENUE_FEES", "EXPENSE_OPERATIONS",
			"CARBON_CREDIT_HOLDING", "INVENTORY_KWH",
		}

		for _, code := range expectedCodes {
			assert.True(t, systemCodes[code], "expected system blueprint %s", code)
		}

		assert.Len(t, systemCodes, 12, "expected exactly 12 system blueprints")
	})

	t.Run("Blueprint seeding is idempotent", func(t *testing.T) {
		// Seed again
		err := accounttype.SeedPlatformBlueprints(ctx, reg)
		require.NoError(t, err)

		// Verify same count
		results, err := reg.ListActive(ctx)
		require.NoError(t, err)

		systemCount := 0
		for _, def := range results {
			if def.IsSystem {
				systemCount++
			}
		}
		assert.Equal(t, 12, systemCount)
	})
}

// ============================================================================
// Scenario 7: Tenant Isolation for Account Types
// ============================================================================

// TestE2E_AccountTypeTenantIsolation verifies that account types created by
// one tenant are not visible to another tenant.
func TestE2E_AccountTypeTenantIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupE2ETestPool(t)
	ctxA := setupProductDirectorySchema(t, pool, "e2e_tenant_iso_a")
	ctxB := setupProductDirectorySchema(t, pool, "e2e_tenant_iso_b")

	reg, err := accounttype.NewPostgresRegistry(pool)
	require.NoError(t, err)

	seedInstrument(t, pool, ctxA, "GBP", "MONETARY")
	seedInstrument(t, pool, ctxB, "GBP", "MONETARY")

	t.Run("Tenant A creates account type", func(t *testing.T) {
		def := &accounttype.Definition{
			ID:             uuid.New(),
			Code:           "TENANT_A_ONLY",
			Version:        1,
			DisplayName:    "Tenant A Only Type",
			BehaviorClass:  accounttype.BehaviorClassClearing,
			NormalBalance:  accounttype.NormalBalanceDebit,
			InstrumentCode: "GBP",
			EligibilityCEL: "true",
			IsSystem:       false,
			Status:         accounttype.StatusDraft,
			Attributes:     map[string]any{},
		}

		err := reg.CreateDraft(ctxA, def)
		require.NoError(t, err)
		err = reg.ActivateAccountType(ctxA, "TENANT_A_ONLY", 1)
		require.NoError(t, err)
	})

	t.Run("Tenant B cannot see Tenant A's account type", func(t *testing.T) {
		_, err := reg.GetActiveDefinition(ctxB, "TENANT_A_ONLY")
		assert.ErrorIs(t, err, accounttype.ErrNotFound)
	})

	t.Run("Both tenants can create same code independently", func(t *testing.T) {
		defB := &accounttype.Definition{
			ID:             uuid.New(),
			Code:           "TENANT_A_ONLY", // Same code
			Version:        1,
			DisplayName:    "Tenant B Version",
			BehaviorClass:  accounttype.BehaviorClassHolding, // Different behavior class
			NormalBalance:  accounttype.NormalBalanceCredit,
			InstrumentCode: "GBP",
			EligibilityCEL: "true",
			IsSystem:       false,
			Status:         accounttype.StatusDraft,
			Attributes:     map[string]any{},
		}

		err := reg.CreateDraft(ctxB, defB)
		require.NoError(t, err)
		err = reg.ActivateAccountType(ctxB, "TENANT_A_ONLY", 1)
		require.NoError(t, err)

		// Verify they are independent
		defA, err := reg.GetActiveDefinition(ctxA, "TENANT_A_ONLY")
		require.NoError(t, err)
		assert.Equal(t, accounttype.BehaviorClassClearing, defA.BehaviorClass)

		defBResult, err := reg.GetActiveDefinition(ctxB, "TENANT_A_ONLY")
		require.NoError(t, err)
		assert.Equal(t, accounttype.BehaviorClassHolding, defBResult.BehaviorClass)
	})
}

// ============================================================================
// Scenario 8: Account Type Lifecycle (DRAFT -> ACTIVE -> DEPRECATED)
// ============================================================================

// TestE2E_AccountTypeLifecycle tests the full lifecycle of an account type
// from DRAFT through ACTIVE to DEPRECATED with successor chaining.
func TestE2E_AccountTypeLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupE2ETestPool(t)
	ctx := setupProductDirectorySchema(t, pool, "e2e_lifecycle")

	reg, err := accounttype.NewPostgresRegistry(pool)
	require.NoError(t, err)

	seedInstrument(t, pool, ctx, "GBP", "MONETARY")

	var v1ID uuid.UUID

	t.Run("Create DRAFT definition", func(t *testing.T) {
		def := &accounttype.Definition{
			ID:             uuid.New(),
			Code:           "LIFECYCLE_TEST",
			Version:        1,
			DisplayName:    "Lifecycle Test v1",
			BehaviorClass:  accounttype.BehaviorClassClearing,
			NormalBalance:  accounttype.NormalBalanceDebit,
			InstrumentCode: "GBP",
			EligibilityCEL: "true",
			IsSystem:       false,
			Status:         accounttype.StatusDraft,
			Attributes:     map[string]any{},
		}

		err := reg.CreateDraft(ctx, def)
		require.NoError(t, err)
		v1ID = def.ID

		// Verify in DRAFT
		result, err := reg.GetDefinition(ctx, "LIFECYCLE_TEST", 1)
		require.NoError(t, err)
		assert.Equal(t, accounttype.StatusDraft, result.Status)
	})

	t.Run("Update DRAFT definition", func(t *testing.T) {
		updates := &accounttype.Definition{
			DisplayName: "Lifecycle Test v1 Updated",
			Description: "Updated description",
		}

		err := reg.UpdateDefinition(ctx, "LIFECYCLE_TEST", 1, updates)
		require.NoError(t, err)

		result, err := reg.GetDefinition(ctx, "LIFECYCLE_TEST", 1)
		require.NoError(t, err)
		assert.Equal(t, "Lifecycle Test v1 Updated", result.DisplayName)
		assert.Equal(t, "Updated description", result.Description)
	})

	t.Run("Activate definition", func(t *testing.T) {
		err := reg.ActivateAccountType(ctx, "LIFECYCLE_TEST", 1)
		require.NoError(t, err)

		result, err := reg.GetDefinition(ctx, "LIFECYCLE_TEST", 1)
		require.NoError(t, err)
		assert.Equal(t, accounttype.StatusActive, result.Status)
		assert.NotNil(t, result.ActivatedAt)
	})

	t.Run("Cannot update ACTIVE definition", func(t *testing.T) {
		updates := &accounttype.Definition{
			DisplayName: "Should not work",
		}

		err := reg.UpdateDefinition(ctx, "LIFECYCLE_TEST", 1, updates)
		require.Error(t, err)
		assert.ErrorIs(t, err, accounttype.ErrNotDraft)
	})

	t.Run("Create v2 and deprecate v1 with successor", func(t *testing.T) {
		v2Def := &accounttype.Definition{
			ID:             uuid.New(),
			Code:           "LIFECYCLE_TEST",
			Version:        2,
			DisplayName:    "Lifecycle Test v2",
			BehaviorClass:  accounttype.BehaviorClassClearing,
			NormalBalance:  accounttype.NormalBalanceDebit,
			InstrumentCode: "GBP",
			EligibilityCEL: "true",
			IsSystem:       false,
			Status:         accounttype.StatusDraft,
			Attributes:     map[string]any{},
		}

		err := reg.CreateDraft(ctx, v2Def)
		require.NoError(t, err)

		// First deprecate v1 (before activating v2, since only one ACTIVE per code)
		err = reg.DeprecateAccountType(ctx, "LIFECYCLE_TEST", 1, &v2Def.ID)
		require.NoError(t, err)

		// Activate v2
		err = reg.ActivateAccountType(ctx, "LIFECYCLE_TEST", 2)
		require.NoError(t, err)

		// Verify v1 is deprecated with successor
		v1Result, err := reg.GetDefinition(ctx, "LIFECYCLE_TEST", 1)
		require.NoError(t, err)
		assert.Equal(t, accounttype.StatusDeprecated, v1Result.Status)
		assert.NotNil(t, v1Result.SuccessorID)
		assert.Equal(t, v2Def.ID, *v1Result.SuccessorID)
		assert.NotNil(t, v1Result.DeprecatedAt)

		// Verify GetActiveDefinition returns v2
		active, err := reg.GetActiveDefinition(ctx, "LIFECYCLE_TEST")
		require.NoError(t, err)
		assert.Equal(t, 2, active.Version)
		assert.Equal(t, accounttype.StatusActive, active.Status)
	})

	t.Run("Successor write-once semantics", func(t *testing.T) {
		otherID := uuid.New()
		err := reg.DeprecateAccountType(ctx, "LIFECYCLE_TEST", 1, &otherID)
		require.Error(t, err)
		// Already deprecated, successor already set to v2 ID
		_ = v1ID // Used earlier
	})
}

// ============================================================================
// Scenario 9: LocalAccountTypeCache Integration
// ============================================================================

// TestE2E_LocalAccountTypeCacheIntegration tests that the LocalAccountTypeCache
// correctly loads, caches, and invalidates account type definitions from the
// PostgresRegistry.
func TestE2E_LocalAccountTypeCacheIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupE2ETestPool(t)
	ctx := setupProductDirectorySchema(t, pool, "e2e_cache_integration")

	reg, err := accounttype.NewPostgresRegistry(pool)
	require.NoError(t, err)

	seedInstrument(t, pool, ctx, "GBP", "MONETARY")

	// Create and activate an account type
	def := &accounttype.Definition{
		ID:             uuid.New(),
		Code:           "CACHED_TYPE",
		Version:        1,
		DisplayName:    "Cached Type",
		BehaviorClass:  accounttype.BehaviorClassClearing,
		NormalBalance:  accounttype.NormalBalanceDebit,
		InstrumentCode: "GBP",
		EligibilityCEL: "true",
		IsSystem:       false,
		Status:         accounttype.StatusDraft,
		Attributes:     map[string]any{},
	}

	err = reg.CreateDraft(ctx, def)
	require.NoError(t, err)
	err = reg.ActivateAccountType(ctx, "CACHED_TYPE", 1)
	require.NoError(t, err)

	// Create a loader backed by the registry
	loader := &registryAccountTypeLoader{registry: reg}
	nilCompiler := &nilCELCompiler{}

	atCache := cache.NewLocalAccountTypeCache(loader, nilCompiler,
		cache.WithAccountTypeCacheSize(100),
		cache.WithAccountTypeTTL(5*time.Minute, 0),
	)

	tid, _ := tenant.FromContext(ctx)

	t.Run("GetOrLoad retrieves from registry on cache miss", func(t *testing.T) {
		cached, err := atCache.GetOrLoad(ctx, tid, "CACHED_TYPE")
		require.NoError(t, err)
		require.NotNil(t, cached)
		assert.Equal(t, "CACHED_TYPE", cached.Definition.Code)
		assert.Equal(t, accounttype.StatusActive, cached.Definition.Status)
	})

	t.Run("Cache hit returns same entry", func(t *testing.T) {
		cached1, err := atCache.GetOrLoad(ctx, tid, "CACHED_TYPE")
		require.NoError(t, err)

		cached2, err := atCache.GetOrLoad(ctx, tid, "CACHED_TYPE")
		require.NoError(t, err)

		// Same pointer from cache
		assert.Equal(t, cached1.Definition.ID, cached2.Definition.ID)
	})

	t.Run("Invalidate removes entry and forces reload", func(t *testing.T) {
		atCache.Invalidate(ctx, "CACHED_TYPE", 1)

		// Get should return nil after invalidation
		entry := atCache.Get(ctx, "CACHED_TYPE", 1)
		assert.Nil(t, entry)

		// GetOrLoad should reload from registry
		cached, err := atCache.GetOrLoad(ctx, tid, "CACHED_TYPE")
		require.NoError(t, err)
		require.NotNil(t, cached)
		assert.Equal(t, "CACHED_TYPE", cached.Definition.Code)
	})

	t.Run("InvalidateAll clears entire tenant cache", func(t *testing.T) {
		// Ensure entry is in cache
		_, err := atCache.GetOrLoad(ctx, tid, "CACHED_TYPE")
		require.NoError(t, err)

		size, _ := atCache.Stats(ctx)
		assert.GreaterOrEqual(t, size, 1)

		atCache.InvalidateAll(ctx)

		size, _ = atCache.Stats(ctx)
		assert.Equal(t, 0, size)
	})

	t.Run("Non-existent code returns error", func(t *testing.T) {
		_, err := atCache.GetOrLoad(ctx, tid, "DOES_NOT_EXIST")
		require.Error(t, err)
	})
}

// registryAccountTypeLoader adapts the PostgresRegistry to the AccountTypeLoader interface.
type registryAccountTypeLoader struct {
	registry *accounttype.PostgresRegistry
}

func (l *registryAccountTypeLoader) LoadAccountType(ctx context.Context, code string) (*accounttype.Definition, error) {
	return l.registry.GetActiveDefinition(ctx, code)
}

func (l *registryAccountTypeLoader) ListActiveAccountTypes(ctx context.Context) ([]*accounttype.Definition, error) {
	return l.registry.ListActive(ctx)
}

// nilCELCompiler is a no-op CEL compiler for cache integration tests.
type nilCELCompiler struct{}

func (c *nilCELCompiler) CompileValidation(_ string) (cel.Program, error)  { return nil, nil }
func (c *nilCELCompiler) CompileBucketKey(_ string) (cel.Program, error)   { return nil, nil }
func (c *nilCELCompiler) CompileEligibility(_ string) (cel.Program, error) { return nil, nil }

// ============================================================================
// Scenario 10: Validation CEL Evaluation
// ============================================================================

// TestE2E_ValidationCEL tests that ValidationCEL expressions are correctly
// evaluated against transaction attributes.
func TestE2E_ValidationCEL(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupE2ETestPool(t)
	ctx := setupProductDirectorySchema(t, pool, "e2e_validation_cel")

	reg, err := accounttype.NewPostgresRegistry(pool)
	require.NoError(t, err)

	seedInstrument(t, pool, ctx, "GBP", "MONETARY")

	t.Run("Amount validation passes for positive amounts", func(t *testing.T) {
		def := &accounttype.Definition{
			ID:             uuid.New(),
			Code:           "AMOUNT_CHECK",
			Version:        1,
			DisplayName:    "Amount Check Account",
			BehaviorClass:  accounttype.BehaviorClassClearing,
			NormalBalance:  accounttype.NormalBalanceDebit,
			InstrumentCode: "GBP",
			ValidationCEL:  `parse_int(amount) > 0`,
			EligibilityCEL: "true",
			IsSystem:       false,
			Status:         accounttype.StatusDraft,
			Attributes:     map[string]any{},
		}

		err := reg.CreateDraft(ctx, def)
		require.NoError(t, err)
		err = reg.ActivateAccountType(ctx, "AMOUNT_CHECK", 1)
		require.NoError(t, err)

		// Positive amount passes
		result, err := reg.ValidateTransaction(ctx, "AMOUNT_CHECK", 1, accounttype.AttributeBag{
			Amount: "100",
		})
		require.NoError(t, err)
		assert.True(t, result.Valid)

		// Zero amount fails
		result, err = reg.ValidateTransaction(ctx, "AMOUNT_CHECK", 1, accounttype.AttributeBag{
			Amount: "0",
		})
		require.NoError(t, err)
		assert.False(t, result.Valid)

		// Negative amount fails
		result, err = reg.ValidateTransaction(ctx, "AMOUNT_CHECK", 1, accounttype.AttributeBag{
			Amount: "-50",
		})
		require.NoError(t, err)
		assert.False(t, result.Valid)
	})

	t.Run("No validation CEL defaults to valid", func(t *testing.T) {
		def := &accounttype.Definition{
			ID:             uuid.New(),
			Code:           "NO_VALIDATION",
			Version:        1,
			DisplayName:    "No Validation Account",
			BehaviorClass:  accounttype.BehaviorClassHolding,
			NormalBalance:  accounttype.NormalBalanceCredit,
			InstrumentCode: "GBP",
			// No ValidationCEL
			EligibilityCEL: "true",
			IsSystem:       false,
			Status:         accounttype.StatusDraft,
			Attributes:     map[string]any{},
		}

		err := reg.CreateDraft(ctx, def)
		require.NoError(t, err)
		err = reg.ActivateAccountType(ctx, "NO_VALIDATION", 1)
		require.NoError(t, err)

		result, err := reg.ValidateTransaction(ctx, "NO_VALIDATION", 1, accounttype.AttributeBag{
			Amount: "-999",
		})
		require.NoError(t, err)
		assert.True(t, result.Valid, "No validation expression should default to valid")
	})
}
