package registry_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
	"github.com/meridianhub/meridian/services/reference-data/registry"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestRegistry(t *testing.T) (*registry.PostgresRegistry, *pgxpool.Pool) {
	t.Helper()

	pool := testdb.NewTestPool(t)

	reg, err := registry.NewPostgresRegistry(pool)
	require.NoError(t, err)

	return reg, pool
}

func setupTenantContext(t *testing.T, pool *pgxpool.Pool, tenantID string) context.Context {
	t.Helper()
	ctx, cleanup := testdb.SetupTenantSchemaForPgx(t, pool, tenantID, "reference-data")
	t.Cleanup(cleanup)
	return ctx
}

func seedSystemInstrument(t *testing.T, pool *pgxpool.Pool, ctx context.Context, code string) {
	t.Helper()

	tenantID, _ := tenant.FromContext(ctx)
	schemaName := tenantID.SchemaName()

	// Seed a system instrument directly via SQL (simulating provisioning)
	query := `
		INSERT INTO instrument_definition (
			id, code, version, dimension, precision, status, is_system,
			fungibility_key_expression, created_at, updated_at, activated_at
		) VALUES (
			gen_random_uuid(), $1, 1, 'MONETARY', 2, 'ACTIVE', true,
			'', NOW(), NOW(), NOW()
		)`

	// Set search_path and insert
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s, public", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err)

	_, err = tx.Exec(ctx, query, code)
	require.NoError(t, err)

	require.NoError(t, tx.Commit(ctx))
}

func TestPostgresRegistry_CreateDraft(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "test-tenant-1")

	t.Run("creates draft instrument successfully", func(t *testing.T) {
		def := &registry.InstrumentDefinition{
			Code:                 "TESTUSD",
			Version:              1,
			Dimension:            registry.DimensionMonetary,
			Precision:            2,
			ValidationExpression: "parse_int(amount) > 0",
			DisplayName:          "Test US Dollar",
			Description:          "Test currency for unit tests",
		}

		err := reg.CreateDraft(ctx, def)
		require.NoError(t, err)
		assert.NotEqual(t, uuid.Nil, def.ID)
		assert.Equal(t, registry.StatusDraft, def.Status)
		assert.False(t, def.IsSystem)
	})

	t.Run("rejects system instrument creation", func(t *testing.T) {
		def := &registry.InstrumentDefinition{
			Code:      "SYSEUR",
			Version:   1,
			Dimension: registry.DimensionMonetary,
			Precision: 2,
			IsSystem:  true,
		}

		err := reg.CreateDraft(ctx, def)
		require.ErrorIs(t, err, registry.ErrSystemInstrumentReadOnly)
	})

	t.Run("rejects invalid CEL expression", func(t *testing.T) {
		def := &registry.InstrumentDefinition{
			Code:                 "BADCEL",
			Version:              1,
			Dimension:            registry.DimensionMonetary,
			Precision:            2,
			ValidationExpression: "this is not valid CEL {{{",
		}

		err := reg.CreateDraft(ctx, def)
		require.ErrorIs(t, err, registry.ErrInvalidCEL)
	})

	t.Run("duplicate code+version is idempotent no-op", func(t *testing.T) {
		def := &registry.InstrumentDefinition{
			Code:      "DUPE",
			Version:   1,
			Dimension: registry.DimensionMonetary,
			Precision: 2,
		}

		err := reg.CreateDraft(ctx, def)
		require.NoError(t, err)

		// Create again - should be idempotent (ON CONFLICT DO NOTHING)
		def2 := &registry.InstrumentDefinition{
			Code:      "DUPE",
			Version:   1,
			Dimension: registry.DimensionMonetary,
			Precision: 2,
		}

		err = reg.CreateDraft(ctx, def2)
		require.NoError(t, err, "duplicate CreateDraft should be idempotent")
	})
}

func TestPostgresRegistry_GetDefinition(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "test-tenant-2")

	// Create a test instrument
	def := &registry.InstrumentDefinition{
		Code:        "GETTEST",
		Version:     1,
		Dimension:   registry.DimensionEnergy,
		Precision:   3,
		DisplayName: "Get Test Instrument",
	}
	require.NoError(t, reg.CreateDraft(ctx, def))

	t.Run("retrieves existing instrument", func(t *testing.T) {
		result, err := reg.GetDefinition(ctx, "GETTEST", 1)
		require.NoError(t, err)
		assert.Equal(t, "GETTEST", result.Code)
		assert.Equal(t, 1, result.Version)
		assert.Equal(t, registry.DimensionEnergy, result.Dimension)
		assert.Equal(t, 3, result.Precision)
		assert.Equal(t, registry.StatusDraft, result.Status)
	})

	t.Run("returns ErrNotFound for missing instrument", func(t *testing.T) {
		_, err := reg.GetDefinition(ctx, "NOTEXIST", 1)
		require.ErrorIs(t, err, registry.ErrNotFound)
	})
}

func TestPostgresRegistry_GetActiveDefinition(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "test-tenant-3")

	// Create and activate an instrument
	def := &registry.InstrumentDefinition{
		Code:      "ACTIVETEST",
		Version:   1,
		Dimension: registry.DimensionMonetary,
		Precision: 2,
	}
	require.NoError(t, reg.CreateDraft(ctx, def))
	require.NoError(t, reg.ActivateInstrument(ctx, "ACTIVETEST", 1))

	t.Run("retrieves active instrument", func(t *testing.T) {
		result, err := reg.GetActiveDefinition(ctx, "ACTIVETEST")
		require.NoError(t, err)
		assert.Equal(t, "ACTIVETEST", result.Code)
		assert.Equal(t, registry.StatusActive, result.Status)
	})

	t.Run("returns highest active version", func(t *testing.T) {
		// Create version 2 and activate it
		def2 := &registry.InstrumentDefinition{
			Code:      "ACTIVETEST",
			Version:   2,
			Dimension: registry.DimensionMonetary,
			Precision: 2,
		}
		require.NoError(t, reg.CreateDraft(ctx, def2))
		require.NoError(t, reg.ActivateInstrument(ctx, "ACTIVETEST", 2))

		result, err := reg.GetActiveDefinition(ctx, "ACTIVETEST")
		require.NoError(t, err)
		assert.Equal(t, 2, result.Version)
	})

	t.Run("returns ErrNotFound for non-active instrument", func(t *testing.T) {
		// Create a draft instrument
		draft := &registry.InstrumentDefinition{
			Code:      "DRAFTONLY",
			Version:   1,
			Dimension: registry.DimensionMonetary,
			Precision: 2,
		}
		require.NoError(t, reg.CreateDraft(ctx, draft))

		_, err := reg.GetActiveDefinition(ctx, "DRAFTONLY")
		require.ErrorIs(t, err, registry.ErrNotFound)
	})
}

func TestPostgresRegistry_ListActive(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "test-tenant-4")

	// Seed a system instrument
	seedSystemInstrument(t, pool, ctx, "USD")

	// Create and activate a tenant instrument
	def := &registry.InstrumentDefinition{
		Code:      "TENANTCOIN",
		Version:   1,
		Dimension: registry.DimensionQuantity,
		Precision: 0,
	}
	require.NoError(t, reg.CreateDraft(ctx, def))
	require.NoError(t, reg.ActivateInstrument(ctx, "TENANTCOIN", 1))

	t.Run("returns both system and tenant instruments", func(t *testing.T) {
		results, err := reg.ListActive(ctx)
		require.NoError(t, err)

		// Should have at least the system USD and tenant TENANTCOIN
		codes := make(map[string]bool)
		for _, r := range results {
			codes[r.Code] = true
		}

		assert.True(t, codes["USD"], "expected system instrument USD")
		assert.True(t, codes["TENANTCOIN"], "expected tenant instrument TENANTCOIN")
	})
}

func TestPostgresRegistry_ListByStatus(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "test-tenant-liststatus")

	// Seed a system instrument (ACTIVE)
	seedSystemInstrument(t, pool, ctx, "USD")

	// Create DRAFT instruments
	draft1 := &registry.InstrumentDefinition{
		Code:      "DRAFT1",
		Version:   1,
		Dimension: registry.DimensionMonetary,
		Precision: 2,
	}
	require.NoError(t, reg.CreateDraft(ctx, draft1))

	draft2 := &registry.InstrumentDefinition{
		Code:      "DRAFT2",
		Version:   1,
		Dimension: registry.DimensionEnergy,
		Precision: 3,
	}
	require.NoError(t, reg.CreateDraft(ctx, draft2))

	// Create and activate a tenant instrument
	active := &registry.InstrumentDefinition{
		Code:      "TENANTACTIVE",
		Version:   1,
		Dimension: registry.DimensionQuantity,
		Precision: 0,
	}
	require.NoError(t, reg.CreateDraft(ctx, active))
	require.NoError(t, reg.ActivateInstrument(ctx, "TENANTACTIVE", 1))

	// Create and deprecate an instrument
	dep := &registry.InstrumentDefinition{
		Code:      "DEPRECATED1",
		Version:   1,
		Dimension: registry.DimensionMonetary,
		Precision: 2,
	}
	require.NoError(t, reg.CreateDraft(ctx, dep))
	require.NoError(t, reg.ActivateInstrument(ctx, "DEPRECATED1", 1))
	require.NoError(t, reg.DeprecateInstrument(ctx, "DEPRECATED1", 1, nil))

	t.Run("returns only DRAFT instruments", func(t *testing.T) {
		results, err := reg.ListByStatus(ctx, registry.StatusDraft)
		require.NoError(t, err)

		codes := make(map[string]bool)
		for _, r := range results {
			codes[r.Code] = true
			assert.Equal(t, registry.StatusDraft, r.Status)
		}
		assert.True(t, codes["DRAFT1"])
		assert.True(t, codes["DRAFT2"])
		assert.Len(t, results, 2)
	})

	t.Run("returns only ACTIVE instruments", func(t *testing.T) {
		results, err := reg.ListByStatus(ctx, registry.StatusActive)
		require.NoError(t, err)

		codes := make(map[string]bool)
		for _, r := range results {
			codes[r.Code] = true
			assert.Equal(t, registry.StatusActive, r.Status)
		}
		assert.True(t, codes["USD"])
		assert.True(t, codes["TENANTACTIVE"])
		assert.Len(t, results, 2)
	})

	t.Run("returns only DEPRECATED instruments", func(t *testing.T) {
		results, err := reg.ListByStatus(ctx, registry.StatusDeprecated)
		require.NoError(t, err)

		codes := make(map[string]bool)
		for _, r := range results {
			codes[r.Code] = true
			assert.Equal(t, registry.StatusDeprecated, r.Status)
		}
		assert.True(t, codes["DEPRECATED1"])
		assert.Len(t, results, 1)
	})

	t.Run("returns all instruments when status is empty", func(t *testing.T) {
		results, err := reg.ListByStatus(ctx, "")
		require.NoError(t, err)

		// Should include: USD (system), DRAFT1, DRAFT2, TENANTACTIVE, DEPRECATED1
		assert.Len(t, results, 5)
	})
}

func TestPostgresRegistry_SystemInstrumentProtection(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "test-tenant-5")

	// Seed system instruments
	seedSystemInstrument(t, pool, ctx, "USD")
	seedSystemInstrument(t, pool, ctx, "EUR")
	seedSystemInstrument(t, pool, ctx, "GBP")

	t.Run("CreateDraft rejects is_system=true", func(t *testing.T) {
		def := &registry.InstrumentDefinition{
			Code:      "FAKESYS",
			Version:   1,
			Dimension: registry.DimensionMonetary,
			Precision: 2,
			IsSystem:  true,
		}
		err := reg.CreateDraft(ctx, def)
		require.ErrorIs(t, err, registry.ErrSystemInstrumentReadOnly)
	})

	t.Run("UpdateDefinition rejects system instrument", func(t *testing.T) {
		updates := &registry.InstrumentDefinition{
			DisplayName: "Modified USD",
		}
		err := reg.UpdateDefinition(ctx, "USD", 1, updates)
		require.ErrorIs(t, err, registry.ErrSystemInstrumentReadOnly)
	})

	t.Run("ActivateInstrument rejects system instrument", func(t *testing.T) {
		err := reg.ActivateInstrument(ctx, "EUR", 1)
		require.ErrorIs(t, err, registry.ErrSystemInstrumentReadOnly)
	})

	t.Run("DeprecateInstrument rejects system instrument", func(t *testing.T) {
		err := reg.DeprecateInstrument(ctx, "GBP", 1, nil)
		require.ErrorIs(t, err, registry.ErrSystemInstrumentReadOnly)
	})

	t.Run("GetDefinition still works for system instruments", func(t *testing.T) {
		def, err := reg.GetDefinition(ctx, "USD", 1)
		require.NoError(t, err)
		assert.Equal(t, "USD", def.Code)
		assert.True(t, def.IsSystem)
		assert.Equal(t, registry.StatusActive, def.Status)
	})
}

func TestPostgresRegistry_LifecycleTransitions(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "test-tenant-6")

	t.Run("DRAFT to ACTIVE succeeds", func(t *testing.T) {
		def := &registry.InstrumentDefinition{
			Code:      "LIFECYCLE1",
			Version:   1,
			Dimension: registry.DimensionMonetary,
			Precision: 2,
		}
		require.NoError(t, reg.CreateDraft(ctx, def))

		err := reg.ActivateInstrument(ctx, "LIFECYCLE1", 1)
		require.NoError(t, err)

		// Verify status changed
		result, err := reg.GetDefinition(ctx, "LIFECYCLE1", 1)
		require.NoError(t, err)
		assert.Equal(t, registry.StatusActive, result.Status)
		assert.NotNil(t, result.ActivatedAt)
	})

	t.Run("ACTIVE to DEPRECATED succeeds", func(t *testing.T) {
		def := &registry.InstrumentDefinition{
			Code:      "LIFECYCLE2",
			Version:   1,
			Dimension: registry.DimensionMonetary,
			Precision: 2,
		}
		require.NoError(t, reg.CreateDraft(ctx, def))
		require.NoError(t, reg.ActivateInstrument(ctx, "LIFECYCLE2", 1))

		err := reg.DeprecateInstrument(ctx, "LIFECYCLE2", 1, nil)
		require.NoError(t, err)

		// Verify status changed
		result, err := reg.GetDefinition(ctx, "LIFECYCLE2", 1)
		require.NoError(t, err)
		assert.Equal(t, registry.StatusDeprecated, result.Status)
		assert.NotNil(t, result.DeprecatedAt)
	})

	t.Run("ACTIVE to ACTIVE is idempotent no-op", func(t *testing.T) {
		def := &registry.InstrumentDefinition{
			Code:      "LIFECYCLE3",
			Version:   1,
			Dimension: registry.DimensionMonetary,
			Precision: 2,
		}
		require.NoError(t, reg.CreateDraft(ctx, def))
		require.NoError(t, reg.ActivateInstrument(ctx, "LIFECYCLE3", 1))

		err := reg.ActivateInstrument(ctx, "LIFECYCLE3", 1)
		require.NoError(t, err, "activating already-active instrument should be idempotent")
	})

	t.Run("DRAFT to DEPRECATED fails", func(t *testing.T) {
		def := &registry.InstrumentDefinition{
			Code:      "LIFECYCLE4",
			Version:   1,
			Dimension: registry.DimensionMonetary,
			Precision: 2,
		}
		require.NoError(t, reg.CreateDraft(ctx, def))

		err := reg.DeprecateInstrument(ctx, "LIFECYCLE4", 1, nil)
		require.ErrorIs(t, err, registry.ErrNotActive)
	})
}

func TestPostgresRegistry_UpdateDefinition(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "test-tenant-7")

	t.Run("updates draft instrument successfully", func(t *testing.T) {
		def := &registry.InstrumentDefinition{
			Code:      "UPDATE1",
			Version:   1,
			Dimension: registry.DimensionMonetary,
			Precision: 2,
		}
		require.NoError(t, reg.CreateDraft(ctx, def))

		updates := &registry.InstrumentDefinition{
			DisplayName:          "Updated Display Name",
			Description:          "Updated Description",
			ValidationExpression: "true",
		}
		err := reg.UpdateDefinition(ctx, "UPDATE1", 1, updates)
		require.NoError(t, err)

		// Verify updates
		result, err := reg.GetDefinition(ctx, "UPDATE1", 1)
		require.NoError(t, err)
		assert.Equal(t, "Updated Display Name", result.DisplayName)
		assert.Equal(t, "Updated Description", result.Description)
		assert.Equal(t, "true", result.ValidationExpression)
	})

	t.Run("rejects update on active instrument", func(t *testing.T) {
		def := &registry.InstrumentDefinition{
			Code:      "UPDATE2",
			Version:   1,
			Dimension: registry.DimensionMonetary,
			Precision: 2,
		}
		require.NoError(t, reg.CreateDraft(ctx, def))
		require.NoError(t, reg.ActivateInstrument(ctx, "UPDATE2", 1))

		updates := &registry.InstrumentDefinition{
			DisplayName: "Should Fail",
		}
		err := reg.UpdateDefinition(ctx, "UPDATE2", 1, updates)
		require.ErrorIs(t, err, registry.ErrNotDraft)
	})

	t.Run("returns ErrNotFound for missing instrument", func(t *testing.T) {
		updates := &registry.InstrumentDefinition{
			DisplayName: "Does not exist",
		}
		err := reg.UpdateDefinition(ctx, "NOTEXIST", 1, updates)
		require.ErrorIs(t, err, registry.ErrNotFound)
	})
}

func TestPostgresRegistry_ValidateAttributes(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "test-tenant-8")

	t.Run("validates with CEL expression", func(t *testing.T) {
		def := &registry.InstrumentDefinition{
			Code:                   "VALIDATE1",
			Version:                1,
			Dimension:              registry.DimensionMonetary,
			Precision:              2,
			ValidationExpression:   `parse_int(amount) > 0`,
			ErrorMessageExpression: `"Amount must be positive, got: " + amount`,
		}
		require.NoError(t, reg.CreateDraft(ctx, def))
		require.NoError(t, reg.ActivateInstrument(ctx, "VALIDATE1", 1))

		// Valid case
		result, err := reg.ValidateAttributes(ctx, "VALIDATE1", 1, registry.AttributeBag{
			Amount: "100",
		})
		require.NoError(t, err)
		assert.True(t, result.Valid)

		// Invalid case
		result, err = reg.ValidateAttributes(ctx, "VALIDATE1", 1, registry.AttributeBag{
			Amount: "-50",
		})
		require.NoError(t, err)
		assert.False(t, result.Valid)
		assert.Contains(t, result.ErrorMessage, "Amount must be positive")
	})

	t.Run("validates with attributes", func(t *testing.T) {
		def := &registry.InstrumentDefinition{
			Code:                 "VALIDATE2",
			Version:              1,
			Dimension:            registry.DimensionEnergy,
			Precision:            3,
			ValidationExpression: `attributes["source_type"] == "renewable"`,
		}
		require.NoError(t, reg.CreateDraft(ctx, def))
		require.NoError(t, reg.ActivateInstrument(ctx, "VALIDATE2", 1))

		// Valid case
		result, err := reg.ValidateAttributes(ctx, "VALIDATE2", 1, registry.AttributeBag{
			Attributes: map[string]string{"source_type": "renewable"},
		})
		require.NoError(t, err)
		assert.True(t, result.Valid)

		// Invalid case
		result, err = reg.ValidateAttributes(ctx, "VALIDATE2", 1, registry.AttributeBag{
			Attributes: map[string]string{"source_type": "fossil"},
		})
		require.NoError(t, err)
		assert.False(t, result.Valid)
	})

	t.Run("returns valid when no validation expression", func(t *testing.T) {
		def := &registry.InstrumentDefinition{
			Code:      "VALIDATE3",
			Version:   1,
			Dimension: registry.DimensionQuantity,
			Precision: 0,
			// No validation expression
		}
		require.NoError(t, reg.CreateDraft(ctx, def))
		require.NoError(t, reg.ActivateInstrument(ctx, "VALIDATE3", 1))

		result, err := reg.ValidateAttributes(ctx, "VALIDATE3", 1, registry.AttributeBag{
			Amount: "anything",
		})
		require.NoError(t, err)
		assert.True(t, result.Valid)
	})
}

func TestPostgresRegistry_CELCompilationAtCreation(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "test-tenant-9")

	testCases := []struct {
		name       string
		expression string
		valid      bool
	}{
		{
			name:       "valid boolean expression",
			expression: "parse_int(amount) > 0",
			valid:      true,
		},
		{
			name:       "valid attribute check",
			expression: `attributes["key"] == "value"`,
			valid:      true,
		},
		{
			name:       "valid timestamp check",
			expression: `valid_from < valid_to`,
			valid:      true,
		},
		{
			name:       "invalid syntax",
			expression: "{{{{invalid syntax",
			valid:      false,
		},
		{
			name:       "undefined variable",
			expression: "undefined_var > 0",
			valid:      false,
		},
		{
			name:       "type mismatch",
			expression: `amount + attributes`, // can't add string and map
			valid:      false,
		},
	}

	for i, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			def := &registry.InstrumentDefinition{
				Code:                 fmt.Sprintf("CEL%d", i),
				Version:              1,
				Dimension:            registry.DimensionMonetary,
				Precision:            2,
				ValidationExpression: tc.expression,
			}

			err := reg.CreateDraft(ctx, def)
			if tc.valid {
				assert.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, registry.ErrInvalidCEL)
			}
		})
	}
}

func TestPostgresRegistry_TenantIsolation(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx1 := setupTenantContext(t, pool, "tenant-iso-1")
	ctx2 := setupTenantContext(t, pool, "tenant-iso-2")

	// Create instrument in tenant 1
	def1 := &registry.InstrumentDefinition{
		Code:      "ISOLATED",
		Version:   1,
		Dimension: registry.DimensionMonetary,
		Precision: 2,
	}
	require.NoError(t, reg.CreateDraft(ctx1, def1))

	t.Run("tenant 1 can see its instrument", func(t *testing.T) {
		result, err := reg.GetDefinition(ctx1, "ISOLATED", 1)
		require.NoError(t, err)
		assert.Equal(t, "ISOLATED", result.Code)
	})

	t.Run("tenant 2 cannot see tenant 1's instrument", func(t *testing.T) {
		_, err := reg.GetDefinition(ctx2, "ISOLATED", 1)
		require.ErrorIs(t, err, registry.ErrNotFound)
	})

	t.Run("tenants can have same code independently", func(t *testing.T) {
		def2 := &registry.InstrumentDefinition{
			Code:        "ISOLATED",
			Version:     1,
			Dimension:   registry.DimensionEnergy, // Different dimension
			Precision:   3,
			DisplayName: "Tenant 2 version",
		}
		require.NoError(t, reg.CreateDraft(ctx2, def2))

		// Verify both exist independently
		result1, err := reg.GetDefinition(ctx1, "ISOLATED", 1)
		require.NoError(t, err)
		assert.Equal(t, registry.DimensionMonetary, result1.Dimension)

		result2, err := reg.GetDefinition(ctx2, "ISOLATED", 1)
		require.NoError(t, err)
		assert.Equal(t, registry.DimensionEnergy, result2.Dimension)
		assert.Equal(t, "Tenant 2 version", result2.DisplayName)
	})
}

func TestPostgresRegistry_ValidateAttributesWithTimestamps(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "test-tenant-10")

	def := &registry.InstrumentDefinition{
		Code:                 "TIMETEST",
		Version:              1,
		Dimension:            registry.DimensionEnergy,
		Precision:            3,
		ValidationExpression: `valid_from < valid_to`,
	}
	require.NoError(t, reg.CreateDraft(ctx, def))
	require.NoError(t, reg.ActivateInstrument(ctx, "TIMETEST", 1))

	now := time.Now()
	later := now.Add(time.Hour)

	t.Run("valid time range passes", func(t *testing.T) {
		result, err := reg.ValidateAttributes(ctx, "TIMETEST", 1, registry.AttributeBag{
			ValidFrom: &now,
			ValidTo:   &later,
		})
		require.NoError(t, err)
		assert.True(t, result.Valid)
	})

	t.Run("invalid time range fails", func(t *testing.T) {
		result, err := reg.ValidateAttributes(ctx, "TIMETEST", 1, registry.AttributeBag{
			ValidFrom: &later,
			ValidTo:   &now, // Before ValidFrom
		})
		require.NoError(t, err)
		assert.False(t, result.Valid)
	})
}

func TestPostgresRegistry_DeprecateWithSuccessor(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "test-tenant-successor")

	t.Run("deprecate with valid successor succeeds", func(t *testing.T) {
		// Create and activate the old instrument
		oldDef := &registry.InstrumentDefinition{
			Code:      "OLD_V1",
			Version:   1,
			Dimension: registry.DimensionMonetary,
			Precision: 2,
		}
		require.NoError(t, reg.CreateDraft(ctx, oldDef))
		require.NoError(t, reg.ActivateInstrument(ctx, "OLD_V1", 1))

		// Create and activate the successor instrument
		newDef := &registry.InstrumentDefinition{
			Code:      "NEW_V2",
			Version:   1,
			Dimension: registry.DimensionMonetary, // Same dimension
			Precision: 2,
		}
		require.NoError(t, reg.CreateDraft(ctx, newDef))
		require.NoError(t, reg.ActivateInstrument(ctx, "NEW_V2", 1))

		// Deprecate old with successor
		err := reg.DeprecateInstrument(ctx, "OLD_V1", 1, &newDef.ID)
		require.NoError(t, err)

		// Verify successor was set
		result, err := reg.GetDefinition(ctx, "OLD_V1", 1)
		require.NoError(t, err)
		assert.Equal(t, registry.StatusDeprecated, result.Status)
		assert.NotNil(t, result.SuccessorID)
		assert.Equal(t, newDef.ID, *result.SuccessorID)
	})

	t.Run("deprecate with non-existent successor fails", func(t *testing.T) {
		def := &registry.InstrumentDefinition{
			Code:      "NOSUCC",
			Version:   1,
			Dimension: registry.DimensionMonetary,
			Precision: 2,
		}
		require.NoError(t, reg.CreateDraft(ctx, def))
		require.NoError(t, reg.ActivateInstrument(ctx, "NOSUCC", 1))

		// Try to deprecate with non-existent successor
		fakeID := uuid.New()
		err := reg.DeprecateInstrument(ctx, "NOSUCC", 1, &fakeID)
		require.ErrorIs(t, err, registry.ErrSuccessorInvalid)
	})

	t.Run("deprecate with DRAFT successor fails", func(t *testing.T) {
		def := &registry.InstrumentDefinition{
			Code:      "DRAFTSUCC1",
			Version:   1,
			Dimension: registry.DimensionMonetary,
			Precision: 2,
		}
		require.NoError(t, reg.CreateDraft(ctx, def))
		require.NoError(t, reg.ActivateInstrument(ctx, "DRAFTSUCC1", 1))

		// Create successor but keep in DRAFT
		successor := &registry.InstrumentDefinition{
			Code:      "DRAFTSUCC2",
			Version:   1,
			Dimension: registry.DimensionMonetary,
			Precision: 2,
		}
		require.NoError(t, reg.CreateDraft(ctx, successor))
		// NOT activated - still DRAFT

		err := reg.DeprecateInstrument(ctx, "DRAFTSUCC1", 1, &successor.ID)
		require.ErrorIs(t, err, registry.ErrSuccessorInvalid)
	})

	t.Run("deprecate with different dimension successor fails", func(t *testing.T) {
		def := &registry.InstrumentDefinition{
			Code:      "DIMTEST1",
			Version:   1,
			Dimension: registry.DimensionMonetary,
			Precision: 2,
		}
		require.NoError(t, reg.CreateDraft(ctx, def))
		require.NoError(t, reg.ActivateInstrument(ctx, "DIMTEST1", 1))

		// Create successor with different dimension
		successor := &registry.InstrumentDefinition{
			Code:      "DIMTEST2",
			Version:   1,
			Dimension: registry.DimensionEnergy, // Different dimension!
			Precision: 3,
		}
		require.NoError(t, reg.CreateDraft(ctx, successor))
		require.NoError(t, reg.ActivateInstrument(ctx, "DIMTEST2", 1))

		err := reg.DeprecateInstrument(ctx, "DIMTEST1", 1, &successor.ID)
		require.ErrorIs(t, err, registry.ErrSuccessorInvalid)
	})

	t.Run("deprecate with self as successor fails", func(t *testing.T) {
		def := &registry.InstrumentDefinition{
			Code:      "SELFREF",
			Version:   1,
			Dimension: registry.DimensionMonetary,
			Precision: 2,
		}
		require.NoError(t, reg.CreateDraft(ctx, def))
		require.NoError(t, reg.ActivateInstrument(ctx, "SELFREF", 1))

		// Try to set self as successor
		err := reg.DeprecateInstrument(ctx, "SELFREF", 1, &def.ID)
		require.ErrorIs(t, err, registry.ErrSuccessorInvalid)
	})
}

func TestPostgresRegistry_SuccessorWriteOnce(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "test-tenant-writeonce")

	// Create old instrument and two potential successors
	oldDef := &registry.InstrumentDefinition{
		Code:      "WRITEONCE_OLD",
		Version:   1,
		Dimension: registry.DimensionMonetary,
		Precision: 2,
	}
	require.NoError(t, reg.CreateDraft(ctx, oldDef))
	require.NoError(t, reg.ActivateInstrument(ctx, "WRITEONCE_OLD", 1))

	successor1 := &registry.InstrumentDefinition{
		Code:      "WRITEONCE_NEW1",
		Version:   1,
		Dimension: registry.DimensionMonetary,
		Precision: 2,
	}
	require.NoError(t, reg.CreateDraft(ctx, successor1))
	require.NoError(t, reg.ActivateInstrument(ctx, "WRITEONCE_NEW1", 1))

	t.Run("deprecate with successor succeeds first time", func(t *testing.T) {
		err := reg.DeprecateInstrument(ctx, "WRITEONCE_OLD", 1, &successor1.ID)
		require.NoError(t, err)

		result, err := reg.GetDefinition(ctx, "WRITEONCE_OLD", 1)
		require.NoError(t, err)
		assert.Equal(t, successor1.ID, *result.SuccessorID)
	})

	t.Run("deprecate already deprecated instrument fails", func(t *testing.T) {
		// The instrument is already DEPRECATED, so trying to deprecate again
		// should fail with ErrNotActive (state machine rejects DEPRECATED->DEPRECATED)
		successor2 := &registry.InstrumentDefinition{
			Code:      "WRITEONCE_NEW2",
			Version:   1,
			Dimension: registry.DimensionMonetary,
			Precision: 2,
		}
		require.NoError(t, reg.CreateDraft(ctx, successor2))
		require.NoError(t, reg.ActivateInstrument(ctx, "WRITEONCE_NEW2", 1))

		err := reg.DeprecateInstrument(ctx, "WRITEONCE_OLD", 1, &successor2.ID)
		require.ErrorIs(t, err, registry.ErrNotActive)
	})
}

func TestPostgresRegistry_StatusStateMachine(t *testing.T) {
	t.Run("valid transitions", func(t *testing.T) {
		assert.NoError(t, registry.ValidateStatusTransition(registry.StatusDraft, registry.StatusActive))
		assert.NoError(t, registry.ValidateStatusTransition(registry.StatusActive, registry.StatusDeprecated))
	})

	t.Run("invalid transitions", func(t *testing.T) {
		// ACTIVE -> DRAFT
		err := registry.ValidateStatusTransition(registry.StatusActive, registry.StatusDraft)
		require.ErrorIs(t, err, registry.ErrInvalidStateTransition)

		// DEPRECATED -> DRAFT
		err = registry.ValidateStatusTransition(registry.StatusDeprecated, registry.StatusDraft)
		require.ErrorIs(t, err, registry.ErrInvalidStateTransition)

		// DEPRECATED -> ACTIVE
		err = registry.ValidateStatusTransition(registry.StatusDeprecated, registry.StatusActive)
		require.ErrorIs(t, err, registry.ErrInvalidStateTransition)

		// DRAFT -> DEPRECATED (not allowed for instrument definitions)
		err = registry.ValidateStatusTransition(registry.StatusDraft, registry.StatusDeprecated)
		require.ErrorIs(t, err, registry.ErrInvalidStateTransition)

		// Same status
		err = registry.ValidateStatusTransition(registry.StatusActive, registry.StatusActive)
		require.ErrorIs(t, err, registry.ErrInvalidStateTransition)
	})

	t.Run("IsValid", func(t *testing.T) {
		assert.True(t, registry.StatusDraft.IsValid())
		assert.True(t, registry.StatusActive.IsValid())
		assert.True(t, registry.StatusDeprecated.IsValid())
		assert.False(t, registry.Status("UNKNOWN").IsValid())
	})

	t.Run("CanTransitionTo", func(t *testing.T) {
		assert.True(t, registry.StatusDraft.CanTransitionTo(registry.StatusActive))
		assert.True(t, registry.StatusActive.CanTransitionTo(registry.StatusDeprecated))
		assert.False(t, registry.StatusActive.CanTransitionTo(registry.StatusDraft))
		assert.False(t, registry.StatusDeprecated.CanTransitionTo(registry.StatusActive))
		assert.False(t, registry.StatusDraft.CanTransitionTo(registry.StatusDraft))
	})
}

func TestPostgresRegistry_DeprecatedIsTerminal(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "test-tenant-terminal")

	// Create, activate, deprecate
	def := &registry.InstrumentDefinition{
		Code:      "TERMINAL1",
		Version:   1,
		Dimension: registry.DimensionMonetary,
		Precision: 2,
	}
	require.NoError(t, reg.CreateDraft(ctx, def))
	require.NoError(t, reg.ActivateInstrument(ctx, "TERMINAL1", 1))
	require.NoError(t, reg.DeprecateInstrument(ctx, "TERMINAL1", 1, nil))

	t.Run("cannot activate deprecated instrument", func(t *testing.T) {
		err := reg.ActivateInstrument(ctx, "TERMINAL1", 1)
		require.ErrorIs(t, err, registry.ErrNotDraft)
	})

	t.Run("cannot deprecate already deprecated instrument", func(t *testing.T) {
		err := reg.DeprecateInstrument(ctx, "TERMINAL1", 1, nil)
		require.ErrorIs(t, err, registry.ErrNotActive)
	})

	t.Run("cannot update deprecated instrument", func(t *testing.T) {
		updates := &registry.InstrumentDefinition{
			DisplayName: "Should fail",
		}
		err := reg.UpdateDefinition(ctx, "TERMINAL1", 1, updates)
		require.ErrorIs(t, err, registry.ErrNotDraft)
	})
}

func TestPostgresRegistry_UpdatedAtManagement(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "test-tenant-timestamps")

	def := &registry.InstrumentDefinition{
		Code:      "TIMESTAMP1",
		Version:   1,
		Dimension: registry.DimensionMonetary,
		Precision: 2,
	}
	require.NoError(t, reg.CreateDraft(ctx, def))

	t.Run("updated_at advances on update", func(t *testing.T) {
		original, err := reg.GetDefinition(ctx, "TIMESTAMP1", 1)
		require.NoError(t, err)

		time.Sleep(10 * time.Millisecond) //nolint:forbidigo // ensures distinct timestamps between read and update

		updates := &registry.InstrumentDefinition{
			DisplayName: "Updated Name",
		}
		require.NoError(t, reg.UpdateDefinition(ctx, "TIMESTAMP1", 1, updates))

		updated, err := reg.GetDefinition(ctx, "TIMESTAMP1", 1)
		require.NoError(t, err)
		assert.True(t, updated.UpdatedAt.After(original.UpdatedAt))
	})

	t.Run("updated_at advances on activation", func(t *testing.T) {
		before, err := reg.GetDefinition(ctx, "TIMESTAMP1", 1)
		require.NoError(t, err)

		time.Sleep(10 * time.Millisecond) //nolint:forbidigo // ensures distinct timestamps between read and activation

		require.NoError(t, reg.ActivateInstrument(ctx, "TIMESTAMP1", 1))

		after, err := reg.GetDefinition(ctx, "TIMESTAMP1", 1)
		require.NoError(t, err)
		assert.True(t, after.UpdatedAt.After(before.UpdatedAt))
		assert.NotNil(t, after.ActivatedAt)
	})

	t.Run("updated_at advances on deprecation", func(t *testing.T) {
		before, err := reg.GetDefinition(ctx, "TIMESTAMP1", 1)
		require.NoError(t, err)

		time.Sleep(10 * time.Millisecond) //nolint:forbidigo // ensures distinct timestamps between read and deprecation

		require.NoError(t, reg.DeprecateInstrument(ctx, "TIMESTAMP1", 1, nil))

		after, err := reg.GetDefinition(ctx, "TIMESTAMP1", 1)
		require.NoError(t, err)
		assert.True(t, after.UpdatedAt.After(before.UpdatedAt))
		assert.NotNil(t, after.DeprecatedAt)
	})
}
