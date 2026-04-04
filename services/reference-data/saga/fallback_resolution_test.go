package saga_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
	"github.com/meridianhub/meridian/services/reference-data/saga"
	pkgsaga "github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedPlatformSaga inserts a row into public.platform_saga_definition.
// Returns the UUID of the inserted platform saga.
func seedPlatformSaga(t *testing.T, pool *pgxpool.Pool, ctx context.Context, name, script string) uuid.UUID {
	t.Helper()

	id := uuid.New()

	query := `
		INSERT INTO public.platform_saga_definition (
			id, name, version, script, display_name, description
		) VALUES (
			$1, $2, '1.0.0', $3, $4, $5
		)`

	_, err := pool.Exec(ctx, query, id, name, script,
		name+" (platform)", "Platform default: "+name)
	require.NoError(t, err)

	return id
}

// seedTenantSagaWithPlatformRef inserts a saga_definition with platform_ref into a tenant schema.
// The saga has NULL script and inherits from the platform.
func seedTenantSagaWithPlatformRef(
	t *testing.T, pool *pgxpool.Pool, ctx context.Context,
	name string, version int, platformRefID uuid.UUID, status string,
) uuid.UUID {
	t.Helper()

	tenantID, _ := tenant.FromContext(ctx)
	schemaName := tenantID.SchemaName()

	id := uuid.New()
	isActive := status == "ACTIVE"

	var query string
	if isActive {
		query = `
			INSERT INTO saga_definition (
				id, name, version, script, status, is_system,
				platform_ref, created_at, updated_at, activated_at
			) VALUES (
				$1, $2, $3, NULL, $4, false,
				$5, NOW(), NOW(), NOW()
			)`
	} else {
		query = `
			INSERT INTO saga_definition (
				id, name, version, script, status, is_system,
				platform_ref, created_at, updated_at
			) VALUES (
				$1, $2, $3, NULL, $4, false,
				$5, NOW(), NOW()
			)`
	}

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s", pq.QuoteIdentifier(schemaName)))
	require.NoError(t, err)

	_, err = tx.Exec(ctx, query, id, name, version, status, platformRefID)
	require.NoError(t, err)

	require.NoError(t, tx.Commit(ctx))
	return id
}

// --- Subtask 8.1: Schema extension tests ---

func TestFallbackResolution_SchemaConstraints(t *testing.T) {
	_, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "test-fallback-schema")

	t.Run("allows NULL script when platform_ref is set", func(t *testing.T) {
		platformID := seedPlatformSaga(t, pool, ctx, "schema_test_platform", "def posting_rules(ctx): return 'platform'")
		id := seedTenantSagaWithPlatformRef(t, pool, ctx, "schema_null_script", 1, platformID, "ACTIVE")
		assert.NotEqual(t, uuid.Nil, id)
	})

	t.Run("allows saga with neither script nor platform_ref for ON DELETE SET NULL compatibility", func(t *testing.T) {
		// NOTE: The constraint allows this state because ON DELETE SET NULL on the
		// platform_ref FK can create orphaned sagas. Application logic handles validation
		// of "at least one source" during create/update operations.
		tenantID, _ := tenant.FromContext(ctx)
		schemaName := tenantID.SchemaName()

		tx, err := pool.Begin(ctx)
		require.NoError(t, err)

		_, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s", pq.QuoteIdentifier(schemaName)))
		require.NoError(t, err)

		// Insert with NULL script AND no platform_ref -- allowed at DB level
		_, err = tx.Exec(ctx, `
			INSERT INTO saga_definition (
				id, name, version, script, status, is_system,
				created_at, updated_at
			) VALUES (
				$1, 'no_source_saga', 1, NULL, 'DRAFT', false,
				NOW(), NOW()
			)`, uuid.New())

		require.NoError(t, err, "DB should allow orphaned state for ON DELETE SET NULL support")

		require.NoError(t, tx.Commit(ctx))
	})

	t.Run("rejects saga with both script and platform_ref", func(t *testing.T) {
		platformID := seedPlatformSaga(t, pool, ctx, "both_sources_test", "def posting_rules(ctx): pass")
		tenantID, _ := tenant.FromContext(ctx)
		schemaName := tenantID.SchemaName()

		tx, err := pool.Begin(ctx)
		require.NoError(t, err)

		_, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s", pq.QuoteIdentifier(schemaName)))
		require.NoError(t, err)

		// Try to insert with BOTH script AND platform_ref
		_, err = tx.Exec(ctx, `
			INSERT INTO saga_definition (
				id, name, version, script, status, is_system,
				platform_ref, created_at, updated_at
			) VALUES (
				$1, 'both_sources_saga', 1, 'def posting_rules(ctx): pass', 'DRAFT', false,
				$2, NOW(), NOW()
			)`, uuid.New(), platformID)

		require.Error(t, err, "should reject saga with both script and platform_ref")
		assert.Contains(t, err.Error(), "chk_saga_definition_script_source")

		_ = tx.Rollback(ctx)
	})
}

// --- Subtask 8.2: COALESCE fallback resolution tests ---

func TestFallbackResolution_GetActive_PlatformFallback(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "test-fallback-getactive")

	t.Run("resolves script via platform fallback when tenant has platform_ref", func(t *testing.T) {
		platformScript := "def posting_rules(ctx):\n    return ['platform_step1', 'platform_step2']"
		platformID := seedPlatformSaga(t, pool, ctx, "fb_platform_test", platformScript)

		// Create tenant saga with platform_ref (no custom script)
		seedTenantSagaWithPlatformRef(t, pool, ctx, "fb_platform_test", 1, platformID, "ACTIVE")

		// GetActive should resolve using platform script via COALESCE
		result, err := reg.GetActive(ctx, "fb_platform_test")
		require.NoError(t, err)
		assert.Equal(t, platformScript, result.ResolvedScript,
			"ResolvedScript should contain platform script via COALESCE")
		assert.True(t, result.UsedPlatformFallback,
			"UsedPlatformFallback should be true")
		assert.Equal(t, "", result.Script,
			"Script (tenant's own) should be empty")
		assert.NotNil(t, result.PlatformRef,
			"PlatformRef should be set")
		assert.Equal(t, platformID, *result.PlatformRef)
	})

	t.Run("returns tenant override script when tenant has custom script", func(t *testing.T) {
		tenantScript := "def posting_rules(ctx):\n    return ['tenant_custom_step']"

		// Create tenant saga with custom script (no platform_ref)
		tenantDef := &saga.Definition{
			Name:    "fb_tenant_override",
			Version: 1,
			Script:  tenantScript,
		}
		require.NoError(t, reg.CreateDraft(ctx, tenantDef))
		require.NoError(t, reg.ActivateSaga(ctx, tenantDef.ID))

		result, err := reg.GetActive(ctx, "fb_tenant_override")
		require.NoError(t, err)
		assert.Equal(t, tenantScript, result.ResolvedScript)
		assert.False(t, result.UsedPlatformFallback,
			"UsedPlatformFallback should be false for custom scripts")
		assert.Nil(t, result.PlatformRef,
			"PlatformRef should be nil for custom scripts")
	})

	t.Run("tenant override takes precedence over system saga", func(t *testing.T) {
		// Seed platform definition
		platformScript := "def posting_rules(ctx): return 'system'"
		platformID := seedPlatformSaga(t, pool, ctx, "fb_precedence", platformScript)

		// Seed system saga (is_system=true, has script)
		seedSystemSaga(t, pool, ctx, "fb_precedence")

		// Create tenant override with platform_ref (should be chosen over system saga)
		seedTenantSagaWithPlatformRef(t, pool, ctx, "fb_precedence", 2, platformID, "ACTIVE")

		result, err := reg.GetActive(ctx, "fb_precedence")
		require.NoError(t, err)
		assert.False(t, result.IsSystem,
			"tenant override should be returned, not system saga")
		assert.True(t, result.UsedPlatformFallback)
	})

	t.Run("returns platform default when no tenant override exists", func(t *testing.T) {
		// Only seed system saga - no tenant override
		seedSystemSaga(t, pool, ctx, "fb_nooverride")

		result, err := reg.GetActive(ctx, "fb_nooverride")
		require.NoError(t, err)
		assert.True(t, result.IsSystem)
	})
}

func TestFallbackResolution_GetByID_PlatformFallback(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "test-fallback-getbyid")

	t.Run("GetByID resolves platform script via COALESCE", func(t *testing.T) {
		platformScript := "def posting_rules(ctx): return 'from_platform'"
		platformID := seedPlatformSaga(t, pool, ctx, "getbyid_platform", platformScript)

		sagaID := seedTenantSagaWithPlatformRef(t, pool, ctx, "getbyid_platform", 1, platformID, "ACTIVE")

		result, err := reg.GetByID(ctx, sagaID)
		require.NoError(t, err)
		assert.Equal(t, platformScript, result.ResolvedScript)
		assert.True(t, result.UsedPlatformFallback)
	})
}

func TestFallbackResolution_GetDefinition_PlatformFallback(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "test-fallback-getdef")

	t.Run("GetDefinition resolves platform script via COALESCE", func(t *testing.T) {
		platformScript := "def posting_rules(ctx): return 'getdef_platform'"
		platformID := seedPlatformSaga(t, pool, ctx, "getdef_test", platformScript)

		seedTenantSagaWithPlatformRef(t, pool, ctx, "getdef_test", 1, platformID, "DRAFT")

		result, err := reg.GetDefinition(ctx, "getdef_test", 1)
		require.NoError(t, err)
		assert.Equal(t, platformScript, result.ResolvedScript)
		assert.True(t, result.UsedPlatformFallback)
	})
}

func TestFallbackResolution_ListByStatus_PlatformFallback(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "test-fallback-list")

	platformScript := "def posting_rules(ctx): return 'list_platform'"
	platformID := seedPlatformSaga(t, pool, ctx, "list_fb_test", platformScript)

	// Create saga with platform_ref
	seedTenantSagaWithPlatformRef(t, pool, ctx, "list_fb_test", 1, platformID, "ACTIVE")

	// Also create a regular saga
	regularDef := &saga.Definition{
		Name:    "list_regular",
		Version: 1,
		Script:  "def posting_rules(ctx): return 'regular'",
	}
	require.NoError(t, reg.CreateDraft(ctx, regularDef))
	require.NoError(t, reg.ActivateSaga(ctx, regularDef.ID))

	t.Run("ListByStatus includes resolved platform scripts", func(t *testing.T) {
		results, err := reg.ListByStatus(ctx, saga.StatusActive)
		require.NoError(t, err)
		require.True(t, len(results) >= 2)

		var platformRefSaga, regularSaga *saga.Definition
		for _, r := range results {
			if r.Name == "list_fb_test" {
				platformRefSaga = r
			}
			if r.Name == "list_regular" {
				regularSaga = r
			}
		}

		require.NotNil(t, platformRefSaga, "should find platform-ref saga")
		assert.Equal(t, platformScript, platformRefSaga.ResolvedScript)
		assert.True(t, platformRefSaga.UsedPlatformFallback)

		require.NotNil(t, regularSaga, "should find regular saga")
		assert.Equal(t, "def posting_rules(ctx): return 'regular'", regularSaga.ResolvedScript)
		assert.False(t, regularSaga.UsedPlatformFallback)
	})
}

// --- Subtask 8.2 negative: platform_ref pointing to deleted platform saga ---

func TestFallbackResolution_DeletedPlatformSaga(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "test-fallback-deleted")

	t.Run("GetActive returns empty resolved script when platform deleted", func(t *testing.T) {
		// Create and immediately delete a platform saga
		platformID := seedPlatformSaga(t, pool, ctx, "deleted_platform_test", "def posting_rules(ctx): pass")

		// Create tenant saga pointing to it
		seedTenantSagaWithPlatformRef(t, pool, ctx, "deleted_platform_test", 1, platformID, "ACTIVE")

		// Delete the platform saga
		_, err := pool.Exec(ctx,
			"DELETE FROM public.platform_saga_definition WHERE id = $1", platformID)
		require.NoError(t, err)

		// GetActive should return empty resolved script (LEFT JOIN produces NULL)
		result, err := reg.GetActive(ctx, "deleted_platform_test")
		require.NoError(t, err)
		assert.Equal(t, "", result.ResolvedScript,
			"resolved script should be empty when platform definition is deleted")
		assert.False(t, result.UsedPlatformFallback,
			"platform fallback flag should be false when platform is missing")
	})
}

// --- Subtask 8.3 + 8.4: Bi-temporal pinning tests ---

func TestFallbackResolution_ScriptHash(t *testing.T) {
	t.Run("ComputeScriptHash produces consistent SHA-256", func(t *testing.T) {
		script := "def posting_rules(ctx): return []"
		hash1 := saga.ComputeScriptHash(script)
		hash2 := saga.ComputeScriptHash(script)
		assert.Equal(t, hash1, hash2, "same script should produce same hash")
		assert.Len(t, hash1, 64, "SHA-256 hex hash should be 64 chars")
	})

	t.Run("different scripts produce different hashes", func(t *testing.T) {
		hash1 := saga.ComputeScriptHash("def posting_rules(ctx): return 'v1'")
		hash2 := saga.ComputeScriptHash("def posting_rules(ctx): return 'v2'")
		assert.NotEqual(t, hash1, hash2)
	})

	t.Run("VerifyScriptHash succeeds with matching hash", func(t *testing.T) {
		script := "def posting_rules(ctx): return []"
		hash := saga.ComputeScriptHash(script)
		err := saga.VerifyScriptHash(script, hash)
		assert.NoError(t, err)
	})

	t.Run("VerifyScriptHash fails with mismatched hash", func(t *testing.T) {
		script := "def posting_rules(ctx): return []"
		err := saga.VerifyScriptHash(script, "deadbeefdeadbeefdeadbeefdeadbeef")
		require.Error(t, err)
		assert.ErrorIs(t, err, saga.ErrScriptHashMismatch)
	})
}

func TestFallbackResolution_VersionPinning(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "test-version-pinning")

	vp := saga.NewVersionPinning(reg)

	t.Run("PinVersion pins platform version on instance", func(t *testing.T) {
		platformScript := "def posting_rules(ctx): return 'pinned'"
		platformID := seedPlatformSaga(t, pool, ctx, "pin_test", platformScript)
		seedTenantSagaWithPlatformRef(t, pool, ctx, "pin_test", 1, platformID, "ACTIVE")

		// Create a new saga instance
		instance := &pkgsaga.SagaInstance{}

		def, err := vp.PinVersion(ctx, instance, "pin_test")
		require.NoError(t, err)

		assert.Equal(t, "pin_test", def.Name)
		assert.Equal(t, platformScript, def.ResolvedScript)
		assert.NotNil(t, instance.PlatformSagaVersionID,
			"PlatformSagaVersionID should be set for platform-ref sagas")
		assert.Equal(t, platformID, *instance.PlatformSagaVersionID)
		assert.NotEmpty(t, instance.ScriptHashAtStart)
		assert.Equal(t, saga.ComputeScriptHash(platformScript), instance.ScriptHashAtStart)
	})

	t.Run("PinVersion does not pin platform version for custom scripts", func(t *testing.T) {
		tenantScript := "def posting_rules(ctx): return 'custom'"
		tenantDef := &saga.Definition{
			Name:    "pin_custom_test",
			Version: 1,
			Script:  tenantScript,
		}
		require.NoError(t, reg.CreateDraft(ctx, tenantDef))
		require.NoError(t, reg.ActivateSaga(ctx, tenantDef.ID))

		instance := &pkgsaga.SagaInstance{}

		def, err := vp.PinVersion(ctx, instance, "pin_custom_test")
		require.NoError(t, err)

		assert.Equal(t, tenantScript, def.ResolvedScript)
		assert.Nil(t, instance.PlatformSagaVersionID,
			"PlatformSagaVersionID should be nil for custom scripts")
		assert.NotEmpty(t, instance.ScriptHashAtStart)
		assert.Equal(t, saga.ComputeScriptHash(tenantScript), instance.ScriptHashAtStart)
	})

	t.Run("PinVersion returns error for nonexistent saga", func(t *testing.T) {
		instance := &pkgsaga.SagaInstance{}
		_, err := vp.PinVersion(ctx, instance, "nonexistent_saga")
		require.Error(t, err)
	})
}

// --- Subtask 8.5: Replay with pinned version resolution ---

func TestFallbackResolution_ResolveForReplay(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "test-replay-resolve")

	vp := saga.NewVersionPinning(reg)

	t.Run("replay resolves pinned platform version", func(t *testing.T) {
		platformScript := "def posting_rules(ctx): return 'pinned_v1'"
		platformID := seedPlatformSaga(t, pool, ctx, "replay_pin_test", platformScript)
		sagaID := seedTenantSagaWithPlatformRef(t, pool, ctx, "replay_pin_test", 1, platformID, "ACTIVE")

		// Simulate a pinned instance
		instance := &pkgsaga.SagaInstance{
			ID:                    uuid.New(),
			SagaDefinitionID:      sagaID,
			PlatformSagaVersionID: &platformID,
			ScriptHashAtStart:     saga.ComputeScriptHash(platformScript),
		}

		script, err := vp.ResolveForReplay(ctx, instance)
		require.NoError(t, err)
		assert.Equal(t, platformScript, script)
	})

	t.Run("replay resolves custom script without platform pinning", func(t *testing.T) {
		tenantScript := "def posting_rules(ctx): return 'custom_replay'"
		tenantDef := &saga.Definition{
			Name:    "replay_custom_test",
			Version: 1,
			Script:  tenantScript,
		}
		require.NoError(t, reg.CreateDraft(ctx, tenantDef))
		require.NoError(t, reg.ActivateSaga(ctx, tenantDef.ID))

		instance := &pkgsaga.SagaInstance{
			ID:                uuid.New(),
			SagaDefinitionID:  tenantDef.ID,
			ScriptHashAtStart: saga.ComputeScriptHash(tenantScript),
		}

		script, err := vp.ResolveForReplay(ctx, instance)
		require.NoError(t, err)
		assert.Equal(t, tenantScript, script)
	})

	t.Run("in-flight saga uses OLD platform version after update", func(t *testing.T) {
		// Original platform saga
		originalScript := "def posting_rules(ctx): return 'original'"
		platformID := seedPlatformSaga(t, pool, ctx, "replay_update_test", originalScript)
		sagaID := seedTenantSagaWithPlatformRef(t, pool, ctx, "replay_update_test", 1, platformID, "ACTIVE")

		// Pin version on instance (using original script)
		instance := &pkgsaga.SagaInstance{
			ID:                    uuid.New(),
			SagaDefinitionID:      sagaID,
			PlatformSagaVersionID: &platformID,
			ScriptHashAtStart:     saga.ComputeScriptHash(originalScript),
		}

		// Update the platform saga script (simulates platform update)
		_, err := pool.Exec(ctx,
			"UPDATE public.platform_saga_definition SET script = $1 WHERE id = $2",
			"def posting_rules(ctx): return 'updated'", platformID)
		require.NoError(t, err)

		// Replay should detect hash mismatch because the platform script changed
		_, err = vp.ResolveForReplay(ctx, instance)
		require.Error(t, err, "replay should fail when platform script changes")
		assert.ErrorIs(t, err, saga.ErrScriptHashMismatch)
	})

	t.Run("new instance uses NEW platform version after update", func(t *testing.T) {
		// Create new platform saga
		newScript := "def posting_rules(ctx): return 'new_version'"
		platformID := seedPlatformSaga(t, pool, ctx, "replay_newversion_test", newScript)
		seedTenantSagaWithPlatformRef(t, pool, ctx, "replay_newversion_test", 1, platformID, "ACTIVE")

		// Pin version on NEW instance (should use current script)
		instance := &pkgsaga.SagaInstance{}
		def, err := vp.PinVersion(ctx, instance, "replay_newversion_test")
		require.NoError(t, err)
		assert.Equal(t, newScript, def.ResolvedScript)

		// Replay should succeed since hash matches
		script, err := vp.ResolveForReplay(ctx, instance)
		require.NoError(t, err)
		assert.Equal(t, newScript, script)
	})
}

// --- Subtask 8.5 negative: deleted platform version ---

func TestFallbackResolution_ResolveForReplay_Negative(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "test-replay-negative")

	vp := saga.NewVersionPinning(reg)

	t.Run("replay fails when pinned platform version is deleted", func(t *testing.T) {
		platformScript := "def posting_rules(ctx): return 'soon_deleted'"
		platformID := seedPlatformSaga(t, pool, ctx, "replay_deleted_test", platformScript)
		sagaID := seedTenantSagaWithPlatformRef(t, pool, ctx, "replay_deleted_test", 1, platformID, "ACTIVE")

		instance := &pkgsaga.SagaInstance{
			ID:                    uuid.New(),
			SagaDefinitionID:      sagaID,
			PlatformSagaVersionID: &platformID,
			ScriptHashAtStart:     saga.ComputeScriptHash(platformScript),
		}

		// Delete the platform saga (ON DELETE SET NULL will null out saga_definition.platform_ref)
		_, err := pool.Exec(ctx,
			"DELETE FROM public.platform_saga_definition WHERE id = $1", platformID)
		require.NoError(t, err)

		// Replay should fail because the saga instance still references the platform version
		// via PlatformSagaVersionID (which is NOT affected by ON DELETE SET NULL on saga_definition.platform_ref)
		_, err = vp.ResolveForReplay(ctx, instance)
		require.Error(t, err, "replay should fail when pinned platform version is deleted")
		assert.ErrorIs(t, err, saga.ErrPinnedVersionNotFound)
	})

	t.Run("replay fails with corrupted script hash", func(t *testing.T) {
		platformScript := "def posting_rules(ctx): return 'original'"
		platformID := seedPlatformSaga(t, pool, ctx, "replay_corrupt_test", platformScript)
		sagaID := seedTenantSagaWithPlatformRef(t, pool, ctx, "replay_corrupt_test", 1, platformID, "ACTIVE")

		instance := &pkgsaga.SagaInstance{
			ID:                    uuid.New(),
			SagaDefinitionID:      sagaID,
			PlatformSagaVersionID: &platformID,
			ScriptHashAtStart:     "deadbeefdeadbeefdeadbeefdeadbeef", // deliberately wrong
		}

		_, err := vp.ResolveForReplay(ctx, instance)
		require.Error(t, err, "replay should fail with corrupted hash")
		assert.ErrorIs(t, err, saga.ErrScriptHashMismatch)
	})
}

// --- Subtask 8.2: CreateDraft with platform_ref ---

func TestFallbackResolution_CreateDraft_PlatformRef(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "test-create-platformref")

	t.Run("creates draft with platform_ref and no script", func(t *testing.T) {
		platformID := seedPlatformSaga(t, pool, ctx, "create_pref_test", "def posting_rules(ctx): pass")

		def := &saga.Definition{
			Name:        "draft_with_pref",
			Version:     1,
			PlatformRef: &platformID,
		}

		err := reg.CreateDraft(ctx, def)
		require.NoError(t, err)

		// Verify via GetByID
		result, err := reg.GetByID(ctx, def.ID)
		require.NoError(t, err)
		assert.NotNil(t, result.PlatformRef)
		assert.Equal(t, platformID, *result.PlatformRef)
		assert.Equal(t, "def posting_rules(ctx): pass", result.ResolvedScript)
		assert.True(t, result.UsedPlatformFallback)
	})
}

// --- Subtask 8.2: GetPlatformSagaByID and GetPlatformSagaByName ---

func TestFallbackResolution_GetPlatformSaga(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	// Apply public schema migrations to create platform_saga_definition table
	ctx := setupTenantContext(t, pool, "test-platform-saga-ctx")

	t.Run("GetPlatformSagaByID returns platform definition", func(t *testing.T) {
		id := seedPlatformSaga(t, pool, ctx, "get_platform_by_id", "def posting_rules(ctx): pass")

		result, err := reg.GetPlatformSagaByID(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, id, result.ID)
		assert.Equal(t, "get_platform_by_id", result.Name)
		assert.Equal(t, "def posting_rules(ctx): pass", result.Script)
	})

	t.Run("GetPlatformSagaByID returns error for missing ID", func(t *testing.T) {
		_, err := reg.GetPlatformSagaByID(ctx, uuid.New())
		require.ErrorIs(t, err, saga.ErrPlatformDefinitionNotFound)
	})

	t.Run("GetPlatformSagaByName returns platform definition", func(t *testing.T) {
		seedPlatformSaga(t, pool, ctx, "get_platform_by_name", "def posting_rules(ctx): return 'named'")

		result, err := reg.GetPlatformSagaByName(ctx, "get_platform_by_name")
		require.NoError(t, err)
		assert.Equal(t, "get_platform_by_name", result.Name)
	})

	t.Run("GetPlatformSagaByName returns error for missing name", func(t *testing.T) {
		_, err := reg.GetPlatformSagaByName(ctx, "nonexistent_platform")
		require.ErrorIs(t, err, saga.ErrPlatformDefinitionNotFound)
	})
}
