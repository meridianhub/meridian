package accounttype_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
	"github.com/meridianhub/meridian/services/reference-data/accounttype"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestAccountTypeRegistry(t *testing.T) (*accounttype.PostgresRegistry, *pgxpool.Pool) {
	t.Helper()

	pool := testdb.NewTestPool(t)

	reg, err := accounttype.NewPostgresRegistry(pool)
	require.NoError(t, err)

	return reg, pool
}

func setupAccountTypeTenantContext(t *testing.T, pool *pgxpool.Pool, tenantID string) context.Context {
	t.Helper()
	ctx, cleanup := testdb.SetupTenantSchemaForPgx(t, pool, tenantID, "reference-data")
	t.Cleanup(cleanup)
	return ctx
}

// seedInstrument seeds an instrument into the tenant schema for activation pre-checks.
func seedInstrument(t *testing.T, pool *pgxpool.Pool, ctx context.Context, code string) {
	t.Helper()
	tenantID, _ := tenant.FromContext(ctx)
	schemaName := tenantID.SchemaName()

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err)

	query := `
		INSERT INTO instrument_definition (
			id, code, version, dimension, precision, status, is_system,
			fungibility_key_expression, created_at, updated_at, activated_at
		) VALUES (
			gen_random_uuid(), $1, 1, 'MONETARY', 2, 'ACTIVE', true,
			'', NOW(), NOW(), NOW()
		) ON CONFLICT DO NOTHING`

	_, err = tx.Exec(ctx, query, code)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
}

func newTestDefinition(code, instrumentCode string) *accounttype.Definition {
	def, _ := accounttype.NewDefinition(accounttype.NewDefinitionParams{
		Code:            code,
		DisplayName:     fmt.Sprintf("Test %s", code),
		Description:     fmt.Sprintf("Test account type %s", code),
		NormalBalance:   "CREDIT",
		BehaviorClass:   "CUSTOMER",
		InstrumentCode:  instrumentCode,
		AttributeSchema: json.RawMessage(`{}`),
		Attributes:      map[string]any{},
	})
	return def
}

func TestPostgresAccountTypeRegistry_CreateDraft(t *testing.T) {
	reg, pool := setupTestAccountTypeRegistry(t)
	ctx := setupAccountTypeTenantContext(t, pool, "test-tenant-at-create")

	t.Run("creates draft definition successfully", func(t *testing.T) {
		def := newTestDefinition("TEST_CREATE", "GBP")

		err := reg.CreateDraft(ctx, def)
		require.NoError(t, err)
		assert.NotEqual(t, uuid.Nil, def.ID)
		assert.Equal(t, accounttype.StatusDraft, def.Status)
	})

	t.Run("idempotent create returns existing definition", func(t *testing.T) {
		def1 := newTestDefinition("IDEMPOTENT_CREATE", "GBP")
		err := reg.CreateDraft(ctx, def1)
		require.NoError(t, err)
		originalID := def1.ID

		// Create again with same code+version
		def2 := newTestDefinition("IDEMPOTENT_CREATE", "GBP")
		err = reg.CreateDraft(ctx, def2)
		require.NoError(t, err)
		// Should have the same ID from the original insert
		assert.Equal(t, originalID, def2.ID)
	})

	t.Run("rejects invalid CEL expression", func(t *testing.T) {
		def := newTestDefinition("BAD_CEL_AT", "GBP")
		def.ValidationCEL = "this is not valid CEL {{{"

		err := reg.CreateDraft(ctx, def)
		require.ErrorIs(t, err, accounttype.ErrInvalidCEL)
	})
}

func TestPostgresAccountTypeRegistry_GetDefinition(t *testing.T) {
	reg, pool := setupTestAccountTypeRegistry(t)
	ctx := setupAccountTypeTenantContext(t, pool, "test-tenant-at-get")

	def := newTestDefinition("GET_TEST", "GBP")
	require.NoError(t, reg.CreateDraft(ctx, def))

	t.Run("retrieves existing definition", func(t *testing.T) {
		result, err := reg.GetDefinition(ctx, "GET_TEST", 1)
		require.NoError(t, err)
		assert.Equal(t, "GET_TEST", result.Code)
		assert.Equal(t, 1, result.Version)
		assert.Equal(t, accounttype.BehaviorClassCustomer, result.BehaviorClass)
		assert.Equal(t, accounttype.NormalBalanceCredit, result.NormalBalance)
		assert.Equal(t, accounttype.StatusDraft, result.Status)
	})

	t.Run("returns ErrNotFound for missing definition", func(t *testing.T) {
		_, err := reg.GetDefinition(ctx, "NOTEXIST", 1)
		require.ErrorIs(t, err, accounttype.ErrNotFound)
	})
}

func TestPostgresAccountTypeRegistry_UpdateDefinition(t *testing.T) {
	reg, pool := setupTestAccountTypeRegistry(t)
	ctx := setupAccountTypeTenantContext(t, pool, "test-tenant-at-update")

	t.Run("updates draft definition successfully", func(t *testing.T) {
		def := newTestDefinition("UPDATE_OK", "GBP")
		require.NoError(t, reg.CreateDraft(ctx, def))

		updates := &accounttype.Definition{
			DisplayName:   "Updated Display Name",
			Description:   "Updated Description",
			ValidationCEL: "true",
		}
		err := reg.UpdateDefinition(ctx, "UPDATE_OK", 1, updates)
		require.NoError(t, err)

		result, err := reg.GetDefinition(ctx, "UPDATE_OK", 1)
		require.NoError(t, err)
		assert.Equal(t, "Updated Display Name", result.DisplayName)
		assert.Equal(t, "Updated Description", result.Description)
		assert.Equal(t, "true", result.ValidationCEL)
	})

	t.Run("allows successive updates without optimistic lock conflict", func(t *testing.T) {
		def := newTestDefinition("OPT_LOCK", "GBP")
		require.NoError(t, reg.CreateDraft(ctx, def))

		// First update succeeds
		updates1 := &accounttype.Definition{DisplayName: "First Update"}
		err := reg.UpdateDefinition(ctx, "OPT_LOCK", 1, updates1)
		require.NoError(t, err)

		// Second update with stale updated_at should succeed (re-reads in same txn)
		updates2 := &accounttype.Definition{DisplayName: "Second Update"}
		err = reg.UpdateDefinition(ctx, "OPT_LOCK", 1, updates2)
		require.NoError(t, err)

		result, err := reg.GetDefinition(ctx, "OPT_LOCK", 1)
		require.NoError(t, err)
		assert.Equal(t, "Second Update", result.DisplayName)
	})

	t.Run("returns ErrNotDraft when status is ACTIVE", func(t *testing.T) {
		def := newTestDefinition("UPDATE_ACTIVE", "GBP")
		require.NoError(t, reg.CreateDraft(ctx, def))

		// Seed instrument and activate
		seedInstrument(t, pool, ctx, "GBP")
		require.NoError(t, reg.ActivateAccountType(ctx, "UPDATE_ACTIVE", 1))

		updates := &accounttype.Definition{DisplayName: "Should Fail"}
		err := reg.UpdateDefinition(ctx, "UPDATE_ACTIVE", 1, updates)
		require.ErrorIs(t, err, accounttype.ErrNotDraft)
	})

	t.Run("returns ErrFieldImmutable for BehaviorClass change", func(t *testing.T) {
		def := newTestDefinition("IMMUTABLE_BC", "GBP")
		require.NoError(t, reg.CreateDraft(ctx, def))

		updates := &accounttype.Definition{BehaviorClass: accounttype.BehaviorClassClearing}
		err := reg.UpdateDefinition(ctx, "IMMUTABLE_BC", 1, updates)
		require.ErrorIs(t, err, accounttype.ErrFieldImmutable)
	})

	t.Run("returns ErrNotFound for missing definition", func(t *testing.T) {
		updates := &accounttype.Definition{DisplayName: "Nope"}
		err := reg.UpdateDefinition(ctx, "NOTEXIST", 1, updates)
		require.ErrorIs(t, err, accounttype.ErrNotFound)
	})
}

func TestPostgresAccountTypeRegistry_ActivateAccountType(t *testing.T) {
	reg, pool := setupTestAccountTypeRegistry(t)
	ctx := setupAccountTypeTenantContext(t, pool, "test-tenant-at-activate")

	// Seed instrument for activation checks
	seedInstrument(t, pool, ctx, "GBP")

	t.Run("activates draft definition successfully", func(t *testing.T) {
		def := newTestDefinition("ACTIVATE_OK", "GBP")
		require.NoError(t, reg.CreateDraft(ctx, def))

		err := reg.ActivateAccountType(ctx, "ACTIVATE_OK", 1)
		require.NoError(t, err)

		result, err := reg.GetDefinition(ctx, "ACTIVATE_OK", 1)
		require.NoError(t, err)
		assert.Equal(t, accounttype.StatusActive, result.Status)
		assert.NotNil(t, result.ActivatedAt)
	})

	t.Run("idempotent: activating already-ACTIVE returns nil", func(t *testing.T) {
		def := newTestDefinition("ACTIVATE_IDEMPOTENT", "GBP")
		require.NoError(t, reg.CreateDraft(ctx, def))
		require.NoError(t, reg.ActivateAccountType(ctx, "ACTIVATE_IDEMPOTENT", 1))

		// Call again - should be no-op
		err := reg.ActivateAccountType(ctx, "ACTIVATE_IDEMPOTENT", 1)
		require.NoError(t, err)
	})

	t.Run("fails with invalid instrument code", func(t *testing.T) {
		def := newTestDefinition("BAD_INSTRUMENT", "NONEXISTENT")
		require.NoError(t, reg.CreateDraft(ctx, def))

		err := reg.ActivateAccountType(ctx, "BAD_INSTRUMENT", 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "instrument")
	})

	t.Run("reactivates DEPRECATED definition", func(t *testing.T) {
		def := newTestDefinition("ACTIVATE_DEP", "GBP")
		require.NoError(t, reg.CreateDraft(ctx, def))
		require.NoError(t, reg.ActivateAccountType(ctx, "ACTIVATE_DEP", 1))
		require.NoError(t, reg.DeprecateAccountType(ctx, "ACTIVATE_DEP", 1, nil))

		err := reg.ActivateAccountType(ctx, "ACTIVATE_DEP", 1)
		require.NoError(t, err)
	})
}

func TestPostgresAccountTypeRegistry_ActiveCodeUniqueConstraint(t *testing.T) {
	reg, pool := setupTestAccountTypeRegistry(t)
	ctx := setupAccountTypeTenantContext(t, pool, "test-tenant-at-unique")

	seedInstrument(t, pool, ctx, "GBP")

	// Create and activate version 1
	def1 := newTestDefinition("UNIQUE_CODE", "GBP")
	require.NoError(t, reg.CreateDraft(ctx, def1))
	require.NoError(t, reg.ActivateAccountType(ctx, "UNIQUE_CODE", 1))

	// Create version 2 with same code
	def2, err := accounttype.NewDefinition(accounttype.NewDefinitionParams{
		Code:            "UNIQUE_CODE",
		DisplayName:     "Version 2",
		NormalBalance:   "CREDIT",
		BehaviorClass:   "CUSTOMER",
		InstrumentCode:  "GBP",
		AttributeSchema: json.RawMessage(`{}`),
		Attributes:      map[string]any{},
	})
	require.NoError(t, err)
	def2.Version = 2
	require.NoError(t, reg.CreateDraft(ctx, def2))

	// Activating version 2 should fail because version 1 is already ACTIVE
	err = reg.ActivateAccountType(ctx, "UNIQUE_CODE", 2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ACTIVE")
}

func TestPostgresAccountTypeRegistry_DeprecateAccountType(t *testing.T) {
	reg, pool := setupTestAccountTypeRegistry(t)
	ctx := setupAccountTypeTenantContext(t, pool, "test-tenant-at-deprecate")

	seedInstrument(t, pool, ctx, "GBP")

	t.Run("deprecates active definition successfully", func(t *testing.T) {
		def := newTestDefinition("DEPRECATE_OK", "GBP")
		require.NoError(t, reg.CreateDraft(ctx, def))
		require.NoError(t, reg.ActivateAccountType(ctx, "DEPRECATE_OK", 1))

		err := reg.DeprecateAccountType(ctx, "DEPRECATE_OK", 1, nil)
		require.NoError(t, err)

		result, err := reg.GetDefinition(ctx, "DEPRECATE_OK", 1)
		require.NoError(t, err)
		assert.Equal(t, accounttype.StatusDeprecated, result.Status)
		assert.NotNil(t, result.DeprecatedAt)
	})

	t.Run("sets successor_id", func(t *testing.T) {
		// Create two definitions
		old := newTestDefinition("SUCC_OLD", "GBP")
		require.NoError(t, reg.CreateDraft(ctx, old))
		require.NoError(t, reg.ActivateAccountType(ctx, "SUCC_OLD", 1))

		successor := newTestDefinition("SUCC_NEW", "GBP")
		require.NoError(t, reg.CreateDraft(ctx, successor))
		require.NoError(t, reg.ActivateAccountType(ctx, "SUCC_NEW", 1))

		err := reg.DeprecateAccountType(ctx, "SUCC_OLD", 1, &successor.ID)
		require.NoError(t, err)

		result, err := reg.GetDefinition(ctx, "SUCC_OLD", 1)
		require.NoError(t, err)
		assert.NotNil(t, result.SuccessorID)
		assert.Equal(t, successor.ID, *result.SuccessorID)
	})

	t.Run("returns ErrNotActive when status is DRAFT", func(t *testing.T) {
		def := newTestDefinition("DEPRECATE_DRAFT", "GBP")
		require.NoError(t, reg.CreateDraft(ctx, def))

		err := reg.DeprecateAccountType(ctx, "DEPRECATE_DRAFT", 1, nil)
		require.ErrorIs(t, err, accounttype.ErrNotActive)
	})

	t.Run("returns ErrNotFound for missing definition", func(t *testing.T) {
		err := reg.DeprecateAccountType(ctx, "NOTEXIST", 1, nil)
		require.ErrorIs(t, err, accounttype.ErrNotFound)
	})
}

func TestPostgresAccountTypeRegistry_SuccessorWriteOnce(t *testing.T) {
	reg, pool := setupTestAccountTypeRegistry(t)
	ctx := setupAccountTypeTenantContext(t, pool, "test-tenant-at-writeonce")

	seedInstrument(t, pool, ctx, "GBP")

	// Create the old definition and two successors
	old := newTestDefinition("WO_OLD", "GBP")
	require.NoError(t, reg.CreateDraft(ctx, old))
	require.NoError(t, reg.ActivateAccountType(ctx, "WO_OLD", 1))

	succ1 := newTestDefinition("WO_SUCC1", "GBP")
	require.NoError(t, reg.CreateDraft(ctx, succ1))
	require.NoError(t, reg.ActivateAccountType(ctx, "WO_SUCC1", 1))

	// Deprecate with successor
	err := reg.DeprecateAccountType(ctx, "WO_OLD", 1, &succ1.ID)
	require.NoError(t, err)

	// Already DEPRECATED, so trying again should fail with ErrNotActive
	succ2 := newTestDefinition("WO_SUCC2", "GBP")
	require.NoError(t, reg.CreateDraft(ctx, succ2))
	require.NoError(t, reg.ActivateAccountType(ctx, "WO_SUCC2", 1))

	err = reg.DeprecateAccountType(ctx, "WO_OLD", 1, &succ2.ID)
	require.ErrorIs(t, err, accounttype.ErrNotActive)
}

func TestPostgresAccountTypeRegistry_BehaviorClassCheckConstraint(t *testing.T) {
	reg, pool := setupTestAccountTypeRegistry(t)
	ctx := setupAccountTypeTenantContext(t, pool, "test-tenant-at-chk")

	// The DB-level CHECK constraint should reject invalid behavior_class values.
	// Since NewDefinition validates at the Go level, we verify the Go-level validation.
	_, err := accounttype.NewDefinition(accounttype.NewDefinitionParams{
		Code:           "INVALID_BC",
		DisplayName:    "Invalid",
		NormalBalance:  "CREDIT",
		BehaviorClass:  "NOT_A_REAL_CLASS",
		InstrumentCode: "GBP",
	})
	require.ErrorIs(t, err, accounttype.ErrInvalidBehaviorClass)

	// Ensure a valid one works through the full stack
	def := newTestDefinition("VALID_BC", "GBP")
	err = reg.CreateDraft(ctx, def)
	require.NoError(t, err)

	// Verify round-trip
	result, err := reg.GetDefinition(ctx, "VALID_BC", 1)
	require.NoError(t, err)
	assert.Equal(t, accounttype.BehaviorClassCustomer, result.BehaviorClass)
}

func TestPostgresAccountTypeRegistry_MultiTenantIsolation(t *testing.T) {
	reg, pool := setupTestAccountTypeRegistry(t)
	ctx1 := setupAccountTypeTenantContext(t, pool, "tenant-at-iso-1")
	ctx2 := setupAccountTypeTenantContext(t, pool, "tenant-at-iso-2")

	// Create definition in tenant 1
	def1 := newTestDefinition("ISOLATED_AT", "GBP")
	require.NoError(t, reg.CreateDraft(ctx1, def1))

	t.Run("tenant 1 can see its definition", func(t *testing.T) {
		result, err := reg.GetDefinition(ctx1, "ISOLATED_AT", 1)
		require.NoError(t, err)
		assert.Equal(t, "ISOLATED_AT", result.Code)
	})

	t.Run("tenant 2 cannot see tenant 1's definition", func(t *testing.T) {
		_, err := reg.GetDefinition(ctx2, "ISOLATED_AT", 1)
		require.ErrorIs(t, err, accounttype.ErrNotFound)
	})

	t.Run("tenants can have same code independently", func(t *testing.T) {
		def2 := newTestDefinition("ISOLATED_AT", "EUR")
		require.NoError(t, reg.CreateDraft(ctx2, def2))

		result1, err := reg.GetDefinition(ctx1, "ISOLATED_AT", 1)
		require.NoError(t, err)
		assert.Equal(t, "GBP", result1.InstrumentCode)

		result2, err := reg.GetDefinition(ctx2, "ISOLATED_AT", 1)
		require.NoError(t, err)
		assert.Equal(t, "EUR", result2.InstrumentCode)
	})
}

func TestPostgresAccountTypeRegistry_GetActiveDefinition(t *testing.T) {
	reg, pool := setupTestAccountTypeRegistry(t)
	ctx := setupAccountTypeTenantContext(t, pool, "test-tenant-at-getactive")

	seedInstrument(t, pool, ctx, "GBP")

	def := newTestDefinition("GETACTIVE_AT", "GBP")
	require.NoError(t, reg.CreateDraft(ctx, def))
	require.NoError(t, reg.ActivateAccountType(ctx, "GETACTIVE_AT", 1))

	t.Run("retrieves active definition", func(t *testing.T) {
		result, err := reg.GetActiveDefinition(ctx, "GETACTIVE_AT")
		require.NoError(t, err)
		assert.Equal(t, "GETACTIVE_AT", result.Code)
		assert.Equal(t, accounttype.StatusActive, result.Status)
	})

	t.Run("returns ErrNotFound for non-active definition", func(t *testing.T) {
		draft := newTestDefinition("DRAFTONLY_AT", "GBP")
		require.NoError(t, reg.CreateDraft(ctx, draft))

		_, err := reg.GetActiveDefinition(ctx, "DRAFTONLY_AT")
		require.ErrorIs(t, err, accounttype.ErrNotFound)
	})
}

func TestPostgresAccountTypeRegistry_ListActive(t *testing.T) {
	reg, pool := setupTestAccountTypeRegistry(t)
	ctx := setupAccountTypeTenantContext(t, pool, "test-tenant-at-listactive")

	seedInstrument(t, pool, ctx, "GBP")
	seedInstrument(t, pool, ctx, "EUR")

	// Create and activate two definitions
	def1 := newTestDefinition("LISTA_1", "GBP")
	require.NoError(t, reg.CreateDraft(ctx, def1))
	require.NoError(t, reg.ActivateAccountType(ctx, "LISTA_1", 1))

	def2 := newTestDefinition("LISTA_2", "EUR")
	require.NoError(t, reg.CreateDraft(ctx, def2))
	require.NoError(t, reg.ActivateAccountType(ctx, "LISTA_2", 1))

	// Create a draft (should not appear)
	draft := newTestDefinition("LISTA_DRAFT", "GBP")
	require.NoError(t, reg.CreateDraft(ctx, draft))

	results, err := reg.ListActive(ctx)
	require.NoError(t, err)

	codes := make(map[string]bool)
	for _, r := range results {
		codes[r.Code] = true
		assert.Equal(t, accounttype.StatusActive, r.Status)
	}

	assert.True(t, codes["LISTA_1"])
	assert.True(t, codes["LISTA_2"])
	assert.False(t, codes["LISTA_DRAFT"])
}

func TestPostgresAccountTypeRegistry_ValidateTransaction(t *testing.T) {
	reg, pool := setupTestAccountTypeRegistry(t)
	ctx := setupAccountTypeTenantContext(t, pool, "test-tenant-at-validate")

	seedInstrument(t, pool, ctx, "GBP")

	t.Run("validates with CEL expression", func(t *testing.T) {
		def := newTestDefinition("VAL_CEL", "GBP")
		def.ValidationCEL = `parse_int(amount) > 0`
		require.NoError(t, reg.CreateDraft(ctx, def))
		require.NoError(t, reg.ActivateAccountType(ctx, "VAL_CEL", 1))

		// Valid case
		result, err := reg.ValidateTransaction(ctx, "VAL_CEL", 1, accounttype.AttributeBag{
			Amount: "100",
		})
		require.NoError(t, err)
		assert.True(t, result.Valid)

		// Invalid case
		result, err = reg.ValidateTransaction(ctx, "VAL_CEL", 1, accounttype.AttributeBag{
			Amount: "-50",
		})
		require.NoError(t, err)
		assert.False(t, result.Valid)
	})

	t.Run("returns valid when no validation expression", func(t *testing.T) {
		def := newTestDefinition("VAL_NONE", "GBP")
		require.NoError(t, reg.CreateDraft(ctx, def))
		require.NoError(t, reg.ActivateAccountType(ctx, "VAL_NONE", 1))

		result, err := reg.ValidateTransaction(ctx, "VAL_NONE", 1, accounttype.AttributeBag{
			Amount: "anything",
		})
		require.NoError(t, err)
		assert.True(t, result.Valid)
	})
}

func TestPostgresAccountTypeRegistry_CheckEligibility(t *testing.T) {
	reg, pool := setupTestAccountTypeRegistry(t)
	ctx := setupAccountTypeTenantContext(t, pool, "test-tenant-at-eligibility")

	seedInstrument(t, pool, ctx, "GBP")

	t.Run("checks eligibility with CEL expression", func(t *testing.T) {
		def := newTestDefinition("ELIG_CEL", "GBP")
		def.EligibilityCEL = `party["status"] == "ACTIVE"`
		require.NoError(t, reg.CreateDraft(ctx, def))
		require.NoError(t, reg.ActivateAccountType(ctx, "ELIG_CEL", 1))

		// Eligible
		result, err := reg.CheckEligibility(ctx, "ELIG_CEL", 1, accounttype.AttributeBag{
			Attributes: map[string]string{"status": "ACTIVE"},
		})
		require.NoError(t, err)
		assert.True(t, result.Valid)

		// Not eligible
		result, err = reg.CheckEligibility(ctx, "ELIG_CEL", 1, accounttype.AttributeBag{
			Attributes: map[string]string{"status": "SUSPENDED"},
		})
		require.NoError(t, err)
		assert.False(t, result.Valid)
	})

	t.Run("returns eligible when no eligibility expression", func(t *testing.T) {
		def := newTestDefinition("ELIG_NONE", "GBP")
		require.NoError(t, reg.CreateDraft(ctx, def))
		require.NoError(t, reg.ActivateAccountType(ctx, "ELIG_NONE", 1))

		result, err := reg.CheckEligibility(ctx, "ELIG_NONE", 1, accounttype.AttributeBag{})
		require.NoError(t, err)
		assert.True(t, result.Valid)
	})
}

func TestPostgresAccountTypeRegistry_GetProductFeatures(t *testing.T) {
	reg, pool := setupTestAccountTypeRegistry(t)
	ctx := setupAccountTypeTenantContext(t, pool, "test-tenant-at-features")

	def := newTestDefinition("FEATURES", "GBP")
	def.Attributes = map[string]any{
		"overdraft_limit": float64(1000),
		"interest_rate":   0.05,
	}
	require.NoError(t, reg.CreateDraft(ctx, def))

	features, err := reg.GetProductFeatures(ctx, "FEATURES", 1)
	require.NoError(t, err)
	assert.Equal(t, float64(1000), features["overdraft_limit"])
	assert.Equal(t, 0.05, features["interest_rate"])
}

func TestPostgresRegistry_GetDefinitionByID(t *testing.T) {
	reg, pool := setupTestAccountTypeRegistry(t)
	ctx := setupAccountTypeTenantContext(t, pool, "test-tenant-at-getbyid")

	t.Run("returns existing definition by ID", func(t *testing.T) {
		def := newTestDefinition("GETBYID_OK", "GBP")
		require.NoError(t, reg.CreateDraft(ctx, def))

		result, err := reg.GetDefinitionByID(ctx, def.ID)
		require.NoError(t, err)
		assert.Equal(t, def.ID, result.ID)
		assert.Equal(t, "GETBYID_OK", result.Code)
		assert.Equal(t, 1, result.Version)
		assert.Equal(t, accounttype.StatusDraft, result.Status)
	})

	t.Run("returns ErrNotFound for non-existent ID", func(t *testing.T) {
		_, err := reg.GetDefinitionByID(ctx, uuid.New())
		require.ErrorIs(t, err, accounttype.ErrNotFound)
	})
}

func TestPostgresRegistry_ActivateWithValuationMethods(t *testing.T) {
	reg, pool := setupTestAccountTypeRegistry(t)
	ctx := setupAccountTypeTenantContext(t, pool, "test-tenant-at-valmethod")

	seedInstrument(t, pool, ctx, "GBP")
	seedInstrument(t, pool, ctx, "USD")

	// Seed a valuation method so the activation pre-check succeeds
	seedValuationMethod(t, pool, ctx)

	t.Run("creates draft with valuation method templates and activates", func(t *testing.T) {
		vmID := getSeededValuationMethodID(t, pool, ctx)

		def := newTestDefinition("VM_ACTIVATE", "GBP")
		def.ValuationMethods = []accounttype.ValuationMethodTemplate{
			{
				InputInstrument:        "USD",
				ValuationMethodID:      vmID,
				ValuationMethodVersion: 1,
				Parameters:             map[string]any{"margin": 0.01},
			},
		}
		require.NoError(t, reg.CreateDraft(ctx, def))

		// Activate should succeed (valuation method and instruments are valid)
		err := reg.ActivateAccountType(ctx, "VM_ACTIVATE", 1)
		require.NoError(t, err)

		// Fetch and verify valuation methods are present and ACTIVE
		result, err := reg.GetDefinitionByID(ctx, def.ID)
		require.NoError(t, err)
		assert.Equal(t, accounttype.StatusActive, result.Status)
		require.Len(t, result.ValuationMethods, 1)
		assert.Equal(t, accounttype.StatusActive, result.ValuationMethods[0].Status)
		assert.Equal(t, "USD", result.ValuationMethods[0].InputInstrument)
	})

	t.Run("activation fails when valuation method does not exist", func(t *testing.T) {
		def := newTestDefinition("VM_BAD_METHOD", "GBP")
		def.ValuationMethods = []accounttype.ValuationMethodTemplate{
			{
				InputInstrument:        "USD",
				ValuationMethodID:      uuid.New(), // non-existent
				ValuationMethodVersion: 1,
			},
		}
		require.NoError(t, reg.CreateDraft(ctx, def))

		err := reg.ActivateAccountType(ctx, "VM_BAD_METHOD", 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "valuation method")
	})

	t.Run("activation fails when valuation method instrument is not active", func(t *testing.T) {
		vmID := getSeededValuationMethodID(t, pool, ctx)

		def := newTestDefinition("VM_BAD_INST", "GBP")
		def.ValuationMethods = []accounttype.ValuationMethodTemplate{
			{
				InputInstrument:        "NONEXISTENT_CURRENCY",
				ValuationMethodID:      vmID,
				ValuationMethodVersion: 1,
			},
		}
		require.NoError(t, reg.CreateDraft(ctx, def))

		err := reg.ActivateAccountType(ctx, "VM_BAD_INST", 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "instrument")
	})
}

func TestPostgresRegistry_SchemaValidation(t *testing.T) {
	reg, pool := setupTestAccountTypeRegistry(t)
	ctx := setupAccountTypeTenantContext(t, pool, "test-tenant-at-schema")

	seedInstrument(t, pool, ctx, "GBP")

	t.Run("valid schema and matching attributes pass activation", func(t *testing.T) {
		def := newTestDefinition("SCHEMA_OK", "GBP")
		def.AttributeSchema = json.RawMessage(`{
			"type": "object",
			"properties": {
				"tier": {"type": "string"},
				"limit": {"type": "number"}
			},
			"required": ["tier"]
		}`)
		def.Attributes = map[string]any{
			"tier":  "GOLD",
			"limit": float64(5000),
		}
		require.NoError(t, reg.CreateDraft(ctx, def))

		err := reg.ActivateAccountType(ctx, "SCHEMA_OK", 1)
		require.NoError(t, err)
	})

	t.Run("invalid JSON schema is rejected at draft creation", func(t *testing.T) {
		def := newTestDefinition("SCHEMA_BAD", "GBP")
		def.AttributeSchema = json.RawMessage(`{"type": "not-a-real-type"}`)

		err := reg.CreateDraft(ctx, def)
		require.Error(t, err)
		assert.ErrorIs(t, err, accounttype.ErrInvalidAttributeSchema)
	})

	t.Run("attributes not matching schema fail activation", func(t *testing.T) {
		def := newTestDefinition("SCHEMA_MISMATCH", "GBP")
		def.AttributeSchema = json.RawMessage(`{
			"type": "object",
			"properties": {
				"tier": {"type": "string"}
			},
			"required": ["tier"]
		}`)
		// Missing required "tier" field
		def.Attributes = map[string]any{
			"other_field": "value",
		}
		require.NoError(t, reg.CreateDraft(ctx, def))

		err := reg.ActivateAccountType(ctx, "SCHEMA_MISMATCH", 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "attribute")
	})

	t.Run("empty schema allows activation without attribute validation", func(t *testing.T) {
		def := newTestDefinition("SCHEMA_EMPTY", "GBP")
		def.AttributeSchema = json.RawMessage(`{}`)
		def.Attributes = map[string]any{"anything": "goes"}
		require.NoError(t, reg.CreateDraft(ctx, def))

		err := reg.ActivateAccountType(ctx, "SCHEMA_EMPTY", 1)
		require.NoError(t, err)
	})
}

// seedValuationMethod inserts a minimal valuation method record so activation pre-checks pass.
func seedValuationMethod(t *testing.T, pool *pgxpool.Pool, ctx context.Context) {
	t.Helper()
	tenantID, _ := tenant.FromContext(ctx)
	schemaName := tenantID.SchemaName()

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err)

	query := `
		INSERT INTO valuation_method (
			id, name, version, input_instrument, output_instrument,
			logic_script, logic_hash, lifecycle_status, is_system,
			created_at, valid_from
		) VALUES (
			gen_random_uuid(), 'TEST_VM', 1, 'USD', 'GBP',
			'return input', 'abc123', 'ACTIVE', false,
			NOW(), NOW()
		) ON CONFLICT DO NOTHING`

	_, err = tx.Exec(ctx, query)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
}

// getSeededValuationMethodID retrieves the ID of the seeded valuation method.
func getSeededValuationMethodID(t *testing.T, pool *pgxpool.Pool, ctx context.Context) uuid.UUID {
	t.Helper()
	tenantID, _ := tenant.FromContext(ctx)
	schemaName := tenantID.SchemaName()

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err)

	var vmID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM valuation_method WHERE name = 'TEST_VM' AND version = 1`).Scan(&vmID)
	require.NoError(t, err)

	return vmID
}

func TestPostgresAccountTypeRegistry_LifecycleTimestamps(t *testing.T) {
	reg, pool := setupTestAccountTypeRegistry(t)
	ctx := setupAccountTypeTenantContext(t, pool, "test-tenant-at-timestamps")

	seedInstrument(t, pool, ctx, "GBP")

	def := newTestDefinition("TIMESTAMPS_AT", "GBP")
	require.NoError(t, reg.CreateDraft(ctx, def))

	t.Run("updated_at advances on update", func(t *testing.T) {
		original, err := reg.GetDefinition(ctx, "TIMESTAMPS_AT", 1)
		require.NoError(t, err)

		time.Sleep(10 * time.Millisecond) //nolint:forbidigo // ensures distinct timestamps between read and update

		updates := &accounttype.Definition{DisplayName: "Updated Name"}
		require.NoError(t, reg.UpdateDefinition(ctx, "TIMESTAMPS_AT", 1, updates))

		updated, err := reg.GetDefinition(ctx, "TIMESTAMPS_AT", 1)
		require.NoError(t, err)
		assert.True(t, updated.UpdatedAt.After(original.UpdatedAt))
	})

	t.Run("activated_at set on activation", func(t *testing.T) {
		require.NoError(t, reg.ActivateAccountType(ctx, "TIMESTAMPS_AT", 1))

		result, err := reg.GetDefinition(ctx, "TIMESTAMPS_AT", 1)
		require.NoError(t, err)
		assert.NotNil(t, result.ActivatedAt)
	})

	t.Run("deprecated_at set on deprecation", func(t *testing.T) {
		require.NoError(t, reg.DeprecateAccountType(ctx, "TIMESTAMPS_AT", 1, nil))

		result, err := reg.GetDefinition(ctx, "TIMESTAMPS_AT", 1)
		require.NoError(t, err)
		assert.NotNil(t, result.DeprecatedAt)
	})
}

func TestPostgresAccountTypeRegistry_ListAll(t *testing.T) {
	reg, pool := setupTestAccountTypeRegistry(t)
	ctx := setupAccountTypeTenantContext(t, pool, "test-tenant-at-listall")

	seedInstrument(t, pool, ctx, "GBP")
	seedInstrument(t, pool, ctx, "EUR")
	seedInstrument(t, pool, ctx, "USD")

	draft := newTestDefinition("LISTALL_DRAFT", "GBP")
	require.NoError(t, reg.CreateDraft(ctx, draft))

	active := newTestDefinition("LISTALL_ACTIVE", "EUR")
	require.NoError(t, reg.CreateDraft(ctx, active))
	require.NoError(t, reg.ActivateAccountType(ctx, "LISTALL_ACTIVE", 1))

	deprecated := newTestDefinition("LISTALL_DEPR", "USD")
	require.NoError(t, reg.CreateDraft(ctx, deprecated))
	require.NoError(t, reg.ActivateAccountType(ctx, "LISTALL_DEPR", 1))
	require.NoError(t, reg.DeprecateAccountType(ctx, "LISTALL_DEPR", 1, nil))

	t.Run("returns all statuses when no filter", func(t *testing.T) {
		results, err := reg.ListAll(ctx, nil)
		require.NoError(t, err)

		codes := make(map[string]accounttype.Status)
		for _, r := range results {
			codes[r.Code] = r.Status
		}
		assert.Equal(t, accounttype.StatusDraft, codes["LISTALL_DRAFT"])
		assert.Equal(t, accounttype.StatusActive, codes["LISTALL_ACTIVE"])
		assert.Equal(t, accounttype.StatusDeprecated, codes["LISTALL_DEPR"])
	})

	t.Run("status filter returns only matching statuses", func(t *testing.T) {
		results, err := reg.ListAll(ctx, []accounttype.Status{accounttype.StatusActive, accounttype.StatusDeprecated})
		require.NoError(t, err)

		codes := make(map[string]accounttype.Status)
		for _, r := range results {
			codes[r.Code] = r.Status
		}
		assert.Equal(t, accounttype.StatusActive, codes["LISTALL_ACTIVE"])
		assert.Equal(t, accounttype.StatusDeprecated, codes["LISTALL_DEPR"])
		_, hasDraft := codes["LISTALL_DRAFT"]
		assert.False(t, hasDraft)
	})

	t.Run("empty result for unmatched status filter", func(t *testing.T) {
		// Create a fresh tenant context with no definitions
		emptyCtx := setupAccountTypeTenantContext(t, pool, "test-tenant-at-listall-empty")
		results, err := reg.ListAll(emptyCtx, []accounttype.Status{accounttype.StatusActive})
		require.NoError(t, err)
		assert.Empty(t, results)
	})
}
