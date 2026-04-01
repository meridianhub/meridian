package saga_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
	"github.com/meridianhub/meridian/services/reference-data/saga"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestRegistry(t *testing.T) (*saga.PostgresRegistry, *pgxpool.Pool) {
	t.Helper()

	// Use the shared PostgreSQL container started in TestMain (testmain_test.go).
	// Create a fresh pool from the shared connection string so each test
	// gets its own pool lifecycle while reusing the same container.
	pool, err := pgxpool.New(context.Background(), saga.SharedPgConnStr)
	require.NoError(t, err)
	t.Cleanup(func() { pool.Close() })

	reg := saga.NewPostgresRegistry(pool, nil)

	return reg, pool
}

func setupTenantContext(t *testing.T, pool *pgxpool.Pool, tenantID string) context.Context {
	t.Helper()
	ctx, cleanup := testdb.SetupTenantSchemaForPgx(t, pool, tenantID, "reference-data")
	t.Cleanup(cleanup)
	return ctx
}

func seedSystemSaga(t *testing.T, pool *pgxpool.Pool, ctx context.Context, name string) uuid.UUID {
	t.Helper()

	tenantID, _ := tenant.FromContext(ctx)
	schemaName := tenantID.SchemaName()

	id := uuid.New()

	// Seed a system saga directly via SQL (simulating provisioning)
	query := `
		INSERT INTO saga_definition (
			id, name, version, script, status, is_system,
			created_at, updated_at, activated_at
		) VALUES (
			$1, $2, 1, 'def posting_rules(ctx): pass', 'ACTIVE', true,
			NOW(), NOW(), NOW()
		)`

	// Set search_path and insert
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s, public", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err)

	_, err = tx.Exec(ctx, query, id, name)
	require.NoError(t, err)

	require.NoError(t, tx.Commit(ctx))

	return id
}

func TestPostgresRegistry_CreateDraft(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "test-tenant-1")

	t.Run("creates draft saga successfully", func(t *testing.T) {
		def := &saga.Definition{
			Name:        "withdrawal",
			Version:     1,
			Script:      "def posting_rules(ctx):\n    return []",
			DisplayName: "Withdrawal Saga",
			Description: "Handles withdrawal workflow",
		}

		err := reg.CreateDraft(ctx, def)
		require.NoError(t, err)
		assert.NotEqual(t, uuid.Nil, def.ID)
		assert.Equal(t, saga.StatusDraft, def.Status)
		assert.False(t, def.IsSystem)
	})

	t.Run("rejects system saga creation", func(t *testing.T) {
		def := &saga.Definition{
			Name:     "system_saga",
			Version:  1,
			Script:   "def posting_rules(ctx): pass",
			IsSystem: true,
		}

		err := reg.CreateDraft(ctx, def)
		require.ErrorIs(t, err, saga.ErrSystemSagaReadOnly)
	})

	t.Run("rejects duplicate name+version", func(t *testing.T) {
		def := &saga.Definition{
			Name:    "duplicate_test",
			Version: 1,
			Script:  "def posting_rules(ctx): pass",
		}

		err := reg.CreateDraft(ctx, def)
		require.NoError(t, err)

		// Try to create again
		def2 := &saga.Definition{
			Name:    "duplicate_test",
			Version: 1,
			Script:  "def posting_rules(ctx): pass",
		}

		err = reg.CreateDraft(ctx, def2)
		require.ErrorIs(t, err, saga.ErrAlreadyExists)
	})

	t.Run("allows same name with different version", func(t *testing.T) {
		def1 := &saga.Definition{
			Name:    "versioned_saga",
			Version: 1,
			Script:  "def posting_rules(ctx): pass",
		}
		require.NoError(t, reg.CreateDraft(ctx, def1))

		def2 := &saga.Definition{
			Name:    "versioned_saga",
			Version: 2,
			Script:  "def posting_rules(ctx): pass",
		}
		err := reg.CreateDraft(ctx, def2)
		require.NoError(t, err)
	})
}

func TestPostgresRegistry_GetByID(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "test-tenant-2")

	// Create a test saga
	def := &saga.Definition{
		Name:        "get_by_id_test",
		Version:     1,
		Script:      "def posting_rules(ctx): pass",
		DisplayName: "Get By ID Test",
	}
	require.NoError(t, reg.CreateDraft(ctx, def))

	t.Run("retrieves existing saga by ID", func(t *testing.T) {
		result, err := reg.GetByID(ctx, def.ID)
		require.NoError(t, err)
		assert.Equal(t, def.ID, result.ID)
		assert.Equal(t, "get_by_id_test", result.Name)
		assert.Equal(t, 1, result.Version)
		assert.Equal(t, saga.StatusDraft, result.Status)
	})

	t.Run("returns ErrNotFound for missing ID", func(t *testing.T) {
		_, err := reg.GetByID(ctx, uuid.New())
		require.ErrorIs(t, err, saga.ErrNotFound)
	})
}

func TestPostgresRegistry_GetDefinition(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "test-tenant-3")

	// Create a test saga
	def := &saga.Definition{
		Name:        "get_def_test",
		Version:     1,
		Script:      "def posting_rules(ctx): pass",
		DisplayName: "Get Definition Test",
	}
	require.NoError(t, reg.CreateDraft(ctx, def))

	t.Run("retrieves existing saga", func(t *testing.T) {
		result, err := reg.GetDefinition(ctx, "get_def_test", 1)
		require.NoError(t, err)
		assert.Equal(t, "get_def_test", result.Name)
		assert.Equal(t, 1, result.Version)
		assert.Equal(t, saga.StatusDraft, result.Status)
	})

	t.Run("returns ErrNotFound for missing saga", func(t *testing.T) {
		_, err := reg.GetDefinition(ctx, "nonexistent", 1)
		require.ErrorIs(t, err, saga.ErrNotFound)
	})
}

func TestPostgresRegistry_GetActive_TenantResolution(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "test-tenant-4")

	t.Run("returns tenant override over platform default", func(t *testing.T) {
		// Seed a platform default (system saga)
		seedSystemSaga(t, pool, ctx, "transfer")

		// Create a tenant override with version 2 (since system saga has version 1)
		tenantDef := &saga.Definition{
			Name:        "transfer",
			Version:     2,
			Script:      "def posting_rules(ctx): return 'tenant_override'",
			DisplayName: "Tenant Transfer Override",
		}
		require.NoError(t, reg.CreateDraft(ctx, tenantDef))
		require.NoError(t, reg.ActivateSaga(ctx, tenantDef.ID))

		// GetActive should return tenant override
		result, err := reg.GetActive(ctx, "transfer")
		require.NoError(t, err)
		assert.False(t, result.IsSystem, "should return tenant override, not system saga")
		assert.Equal(t, "Tenant Transfer Override", result.DisplayName)
	})

	t.Run("returns platform default when no tenant override", func(t *testing.T) {
		// Seed a platform default
		seedSystemSaga(t, pool, ctx, "deposit")

		// No tenant override created

		// GetActive should return platform default
		result, err := reg.GetActive(ctx, "deposit")
		require.NoError(t, err)
		assert.True(t, result.IsSystem, "should return system saga")
	})

	t.Run("returns highest version tenant override", func(t *testing.T) {
		// Create v1 and activate
		v1 := &saga.Definition{
			Name:    "versioned_active",
			Version: 1,
			Script:  "def posting_rules(ctx): return 'v1'",
		}
		require.NoError(t, reg.CreateDraft(ctx, v1))
		require.NoError(t, reg.ActivateSaga(ctx, v1.ID))

		// Create v2 and activate
		v2 := &saga.Definition{
			Name:    "versioned_active",
			Version: 2,
			Script:  "def posting_rules(ctx): return 'v2'",
		}
		require.NoError(t, reg.CreateDraft(ctx, v2))
		require.NoError(t, reg.ActivateSaga(ctx, v2.ID))

		// GetActive should return v2
		result, err := reg.GetActive(ctx, "versioned_active")
		require.NoError(t, err)
		assert.Equal(t, 2, result.Version)
	})

	t.Run("returns ErrNotFound when no active version exists", func(t *testing.T) {
		// Create draft but don't activate
		draft := &saga.Definition{
			Name:    "draft_only",
			Version: 1,
			Script:  "def posting_rules(ctx): pass",
		}
		require.NoError(t, reg.CreateDraft(ctx, draft))

		_, err := reg.GetActive(ctx, "draft_only")
		require.ErrorIs(t, err, saga.ErrNotFound)
	})
}

func TestPostgresRegistry_ListByStatus(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "test-tenant-5")

	// Seed a system saga
	seedSystemSaga(t, pool, ctx, "system_list_test")

	// Create and activate a tenant saga
	def := &saga.Definition{
		Name:    "tenant_list_test",
		Version: 1,
		Script:  "def posting_rules(ctx): pass",
	}
	require.NoError(t, reg.CreateDraft(ctx, def))
	require.NoError(t, reg.ActivateSaga(ctx, def.ID))

	t.Run("returns both system and tenant sagas", func(t *testing.T) {
		results, err := reg.ListByStatus(ctx, saga.StatusActive)
		require.NoError(t, err)

		names := make(map[string]bool)
		for _, r := range results {
			names[r.Name] = true
		}

		assert.True(t, names["system_list_test"], "expected system saga")
		assert.True(t, names["tenant_list_test"], "expected tenant saga")
	})

	t.Run("filters by status", func(t *testing.T) {
		// Create a draft saga
		draft := &saga.Definition{
			Name:    "draft_list_test",
			Version: 1,
			Script:  "def posting_rules(ctx): pass",
		}
		require.NoError(t, reg.CreateDraft(ctx, draft))

		// List DRAFT only
		drafts, err := reg.ListByStatus(ctx, saga.StatusDraft)
		require.NoError(t, err)

		for _, d := range drafts {
			assert.Equal(t, saga.StatusDraft, d.Status)
		}
	})
}

func TestPostgresRegistry_SystemSagaProtection(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "test-tenant-6")

	// Seed system sagas
	systemID := seedSystemSaga(t, pool, ctx, "protected_saga")

	t.Run("CreateDraft rejects is_system=true", func(t *testing.T) {
		def := &saga.Definition{
			Name:     "fake_system",
			Version:  1,
			Script:   "def posting_rules(ctx): pass",
			IsSystem: true,
		}
		err := reg.CreateDraft(ctx, def)
		require.ErrorIs(t, err, saga.ErrSystemSagaReadOnly)
	})

	t.Run("UpdateDefinition rejects system saga", func(t *testing.T) {
		updates := &saga.Definition{
			DisplayName: "Modified System Saga",
		}
		err := reg.UpdateDefinition(ctx, systemID, updates)
		require.ErrorIs(t, err, saga.ErrSystemSagaReadOnly)
	})

	t.Run("ActivateSaga rejects system saga", func(t *testing.T) {
		err := reg.ActivateSaga(ctx, systemID)
		require.ErrorIs(t, err, saga.ErrSystemSagaReadOnly)
	})

	t.Run("DeprecateSaga rejects system saga", func(t *testing.T) {
		err := reg.DeprecateSaga(ctx, systemID, nil)
		require.ErrorIs(t, err, saga.ErrSystemSagaReadOnly)
	})

	t.Run("GetByID still works for system sagas", func(t *testing.T) {
		def, err := reg.GetByID(ctx, systemID)
		require.NoError(t, err)
		assert.Equal(t, "protected_saga", def.Name)
		assert.True(t, def.IsSystem)
		assert.Equal(t, saga.StatusActive, def.Status)
	})
}

func TestPostgresRegistry_LifecycleTransitions(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "test-tenant-7")

	t.Run("DRAFT to ACTIVE succeeds", func(t *testing.T) {
		def := &saga.Definition{
			Name:    "lifecycle1",
			Version: 1,
			Script:  "def posting_rules(ctx): pass",
		}
		require.NoError(t, reg.CreateDraft(ctx, def))

		err := reg.ActivateSaga(ctx, def.ID)
		require.NoError(t, err)

		// Verify status changed
		result, err := reg.GetByID(ctx, def.ID)
		require.NoError(t, err)
		assert.Equal(t, saga.StatusActive, result.Status)
		assert.NotNil(t, result.ActivatedAt)
	})

	t.Run("ACTIVE to DEPRECATED succeeds", func(t *testing.T) {
		def := &saga.Definition{
			Name:    "lifecycle2",
			Version: 1,
			Script:  "def posting_rules(ctx): pass",
		}
		require.NoError(t, reg.CreateDraft(ctx, def))
		require.NoError(t, reg.ActivateSaga(ctx, def.ID))

		err := reg.DeprecateSaga(ctx, def.ID, nil)
		require.NoError(t, err)

		// Verify status changed
		result, err := reg.GetByID(ctx, def.ID)
		require.NoError(t, err)
		assert.Equal(t, saga.StatusDeprecated, result.Status)
		assert.NotNil(t, result.DeprecatedAt)
	})

	t.Run("ACTIVE to ACTIVE fails", func(t *testing.T) {
		def := &saga.Definition{
			Name:    "lifecycle3",
			Version: 1,
			Script:  "def posting_rules(ctx): pass",
		}
		require.NoError(t, reg.CreateDraft(ctx, def))
		require.NoError(t, reg.ActivateSaga(ctx, def.ID))

		err := reg.ActivateSaga(ctx, def.ID)
		require.ErrorIs(t, err, saga.ErrNotDraft)
	})

	t.Run("DRAFT to DEPRECATED fails", func(t *testing.T) {
		def := &saga.Definition{
			Name:    "lifecycle4",
			Version: 1,
			Script:  "def posting_rules(ctx): pass",
		}
		require.NoError(t, reg.CreateDraft(ctx, def))

		err := reg.DeprecateSaga(ctx, def.ID, nil)
		require.ErrorIs(t, err, saga.ErrNotActive)
	})
}

func TestPostgresRegistry_UpdateDefinition(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "test-tenant-8")

	t.Run("updates draft saga successfully", func(t *testing.T) {
		def := &saga.Definition{
			Name:    "update1",
			Version: 1,
			Script:  "def posting_rules(ctx): pass",
		}
		require.NoError(t, reg.CreateDraft(ctx, def))

		updates := &saga.Definition{
			Script:                  "def posting_rules(ctx): return []",
			DisplayName:             "Updated Display Name",
			Description:             "Updated Description",
			PreconditionsExpression: "true",
		}
		err := reg.UpdateDefinition(ctx, def.ID, updates)
		require.NoError(t, err)

		// Verify updates
		result, err := reg.GetByID(ctx, def.ID)
		require.NoError(t, err)
		assert.Equal(t, "Updated Display Name", result.DisplayName)
		assert.Equal(t, "Updated Description", result.Description)
		assert.Equal(t, "true", result.PreconditionsExpression)
		assert.Contains(t, result.Script, "return []")
	})

	t.Run("rejects update on active saga", func(t *testing.T) {
		def := &saga.Definition{
			Name:    "update2",
			Version: 1,
			Script:  "def posting_rules(ctx): pass",
		}
		require.NoError(t, reg.CreateDraft(ctx, def))
		require.NoError(t, reg.ActivateSaga(ctx, def.ID))

		updates := &saga.Definition{
			DisplayName: "Should Fail",
		}
		err := reg.UpdateDefinition(ctx, def.ID, updates)
		require.ErrorIs(t, err, saga.ErrNotDraft)
	})

	t.Run("returns ErrNotFound for missing saga", func(t *testing.T) {
		updates := &saga.Definition{
			DisplayName: "Does not exist",
		}
		err := reg.UpdateDefinition(ctx, uuid.New(), updates)
		require.ErrorIs(t, err, saga.ErrNotFound)
	})
}

func TestPostgresRegistry_TenantIsolation(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx1 := setupTenantContext(t, pool, "tenant-iso-1")
	ctx2 := setupTenantContext(t, pool, "tenant-iso-2")

	// Create saga in tenant 1
	def1 := &saga.Definition{
		Name:    "isolated_saga",
		Version: 1,
		Script:  "def posting_rules(ctx): pass",
	}
	require.NoError(t, reg.CreateDraft(ctx1, def1))

	t.Run("tenant 1 can see its saga", func(t *testing.T) {
		result, err := reg.GetDefinition(ctx1, "isolated_saga", 1)
		require.NoError(t, err)
		assert.Equal(t, "isolated_saga", result.Name)
	})

	t.Run("tenant 2 cannot see tenant 1's saga", func(t *testing.T) {
		_, err := reg.GetDefinition(ctx2, "isolated_saga", 1)
		require.ErrorIs(t, err, saga.ErrNotFound)
	})

	t.Run("tenants can have same name independently", func(t *testing.T) {
		def2 := &saga.Definition{
			Name:        "isolated_saga",
			Version:     1,
			Script:      "def posting_rules(ctx): return 'tenant2'",
			DisplayName: "Tenant 2 version",
		}
		require.NoError(t, reg.CreateDraft(ctx2, def2))

		// Verify both exist independently
		result1, err := reg.GetDefinition(ctx1, "isolated_saga", 1)
		require.NoError(t, err)
		assert.Equal(t, "", result1.DisplayName)

		result2, err := reg.GetDefinition(ctx2, "isolated_saga", 1)
		require.NoError(t, err)
		assert.Equal(t, "Tenant 2 version", result2.DisplayName)
	})
}

func TestPostgresRegistry_DeprecateWithSuccessor(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "test-tenant-successor")

	t.Run("deprecate with valid successor succeeds", func(t *testing.T) {
		// Create and activate the old saga
		oldDef := &saga.Definition{
			Name:    "successor_old",
			Version: 1,
			Script:  "def posting_rules(ctx): pass",
		}
		require.NoError(t, reg.CreateDraft(ctx, oldDef))
		require.NoError(t, reg.ActivateSaga(ctx, oldDef.ID))

		// Create and activate the successor saga (same name, higher version)
		newDef := &saga.Definition{
			Name:    "successor_old",
			Version: 2,
			Script:  "def posting_rules(ctx): return []",
		}
		require.NoError(t, reg.CreateDraft(ctx, newDef))
		require.NoError(t, reg.ActivateSaga(ctx, newDef.ID))

		// Deprecate old with successor
		err := reg.DeprecateSaga(ctx, oldDef.ID, &newDef.ID)
		require.NoError(t, err)

		// Verify successor was set
		result, err := reg.GetByID(ctx, oldDef.ID)
		require.NoError(t, err)
		assert.Equal(t, saga.StatusDeprecated, result.Status)
		assert.NotNil(t, result.SuccessorID)
		assert.Equal(t, newDef.ID, *result.SuccessorID)
	})

	t.Run("deprecate with non-existent successor fails", func(t *testing.T) {
		def := &saga.Definition{
			Name:    "no_successor",
			Version: 1,
			Script:  "def posting_rules(ctx): pass",
		}
		require.NoError(t, reg.CreateDraft(ctx, def))
		require.NoError(t, reg.ActivateSaga(ctx, def.ID))

		// Try to deprecate with non-existent successor
		fakeID := uuid.New()
		err := reg.DeprecateSaga(ctx, def.ID, &fakeID)
		require.ErrorIs(t, err, saga.ErrSuccessorInvalid)
	})

	t.Run("deprecate with DRAFT successor fails", func(t *testing.T) {
		def := &saga.Definition{
			Name:    "draft_succ1",
			Version: 1,
			Script:  "def posting_rules(ctx): pass",
		}
		require.NoError(t, reg.CreateDraft(ctx, def))
		require.NoError(t, reg.ActivateSaga(ctx, def.ID))

		// Create successor but keep in DRAFT
		successor := &saga.Definition{
			Name:    "draft_succ1",
			Version: 2,
			Script:  "def posting_rules(ctx): pass",
		}
		require.NoError(t, reg.CreateDraft(ctx, successor))
		// NOT activated - still DRAFT

		err := reg.DeprecateSaga(ctx, def.ID, &successor.ID)
		require.ErrorIs(t, err, saga.ErrSuccessorInvalid)
	})

	t.Run("deprecate with different name successor fails", func(t *testing.T) {
		def := &saga.Definition{
			Name:    "name_test1",
			Version: 1,
			Script:  "def posting_rules(ctx): pass",
		}
		require.NoError(t, reg.CreateDraft(ctx, def))
		require.NoError(t, reg.ActivateSaga(ctx, def.ID))

		// Create successor with different name
		successor := &saga.Definition{
			Name:    "name_test2", // Different name!
			Version: 1,
			Script:  "def posting_rules(ctx): pass",
		}
		require.NoError(t, reg.CreateDraft(ctx, successor))
		require.NoError(t, reg.ActivateSaga(ctx, successor.ID))

		err := reg.DeprecateSaga(ctx, def.ID, &successor.ID)
		require.ErrorIs(t, err, saga.ErrSuccessorInvalid)
	})

	t.Run("deprecate with self as successor fails", func(t *testing.T) {
		def := &saga.Definition{
			Name:    "self_ref",
			Version: 1,
			Script:  "def posting_rules(ctx): pass",
		}
		require.NoError(t, reg.CreateDraft(ctx, def))
		require.NoError(t, reg.ActivateSaga(ctx, def.ID))

		// Try to set self as successor
		err := reg.DeprecateSaga(ctx, def.ID, &def.ID)
		require.ErrorIs(t, err, saga.ErrSuccessorInvalid)
	})
}

func TestPostgresRegistry_SuccessorWriteOnce(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "test-tenant-writeonce")

	// Create old saga and two potential successors
	oldDef := &saga.Definition{
		Name:    "writeonce_old",
		Version: 1,
		Script:  "def posting_rules(ctx): pass",
	}
	require.NoError(t, reg.CreateDraft(ctx, oldDef))
	require.NoError(t, reg.ActivateSaga(ctx, oldDef.ID))

	successor1 := &saga.Definition{
		Name:    "writeonce_old",
		Version: 2,
		Script:  "def posting_rules(ctx): pass",
	}
	require.NoError(t, reg.CreateDraft(ctx, successor1))
	require.NoError(t, reg.ActivateSaga(ctx, successor1.ID))

	successor2 := &saga.Definition{
		Name:    "writeonce_old",
		Version: 3,
		Script:  "def posting_rules(ctx): pass",
	}
	require.NoError(t, reg.CreateDraft(ctx, successor2))
	require.NoError(t, reg.ActivateSaga(ctx, successor2.ID))

	t.Run("cannot change successor_id once set", func(t *testing.T) {
		// Deprecate with first successor
		err := reg.DeprecateSaga(ctx, oldDef.ID, &successor1.ID)
		require.NoError(t, err)

		// Verify successor was set
		result, err := reg.GetByID(ctx, oldDef.ID)
		require.NoError(t, err)
		assert.Equal(t, successor1.ID, *result.SuccessorID)

		// Write-once is architecturally enforced: DeprecateSaga requires
		// status=ACTIVE, and after deprecation the status is DEPRECATED, so a
		// second call to DeprecateSaga will fail with ErrNotActive.
		err = reg.DeprecateSaga(ctx, oldDef.ID, &successor2.ID)
		require.ErrorIs(t, err, saga.ErrNotActive)
	})
}

func TestPostgresRegistry_ImmutableScriptOnActive(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "test-tenant-immutable")

	// Create and activate a saga
	def := &saga.Definition{
		Name:                    "immutable_test",
		Version:                 1,
		Script:                  "def posting_rules(ctx): pass",
		PreconditionsExpression: "true",
	}
	require.NoError(t, reg.CreateDraft(ctx, def))
	require.NoError(t, reg.ActivateSaga(ctx, def.ID))

	// Go-layer enforcement: UpdateDefinition rejects non-DRAFT sagas,
	// preventing script/preconditions modification on ACTIVE sagas.
	// (CockroachDB does not support PL/pgSQL triggers for DB-level enforcement.)

	t.Run("script cannot be modified on ACTIVE saga", func(t *testing.T) {
		updates := &saga.Definition{
			Script: "def posting_rules(ctx): return modified",
		}
		err := reg.UpdateDefinition(ctx, def.ID, updates)
		require.ErrorIs(t, err, saga.ErrNotDraft)
	})

	t.Run("preconditions cannot be modified on ACTIVE saga", func(t *testing.T) {
		updates := &saga.Definition{
			PreconditionsExpression: "false",
		}
		err := reg.UpdateDefinition(ctx, def.ID, updates)
		require.ErrorIs(t, err, saga.ErrNotDraft)
	})
}
