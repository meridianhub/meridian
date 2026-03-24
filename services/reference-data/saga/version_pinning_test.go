package saga_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reference-data/saga"
	pkgsaga "github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// ComputeScriptHash / VerifyScriptHash (pure unit tests - no DB)
// ---------------------------------------------------------------------------

func TestComputeScriptHash(t *testing.T) {
	t.Run("deterministic", func(t *testing.T) {
		h1 := saga.ComputeScriptHash("def posting_rules(ctx): pass")
		h2 := saga.ComputeScriptHash("def posting_rules(ctx): pass")
		assert.Equal(t, h1, h2)
	})

	t.Run("different inputs produce different hashes", func(t *testing.T) {
		h1 := saga.ComputeScriptHash("script_a")
		h2 := saga.ComputeScriptHash("script_b")
		assert.NotEqual(t, h1, h2)
	})

	t.Run("empty string returns 64-char hex", func(t *testing.T) {
		h := saga.ComputeScriptHash("")
		assert.Len(t, h, 64)
	})

	t.Run("returns lowercase hex only", func(t *testing.T) {
		h := saga.ComputeScriptHash("some script content")
		assert.Len(t, h, 64)
		for _, c := range h {
			assert.True(t, (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'),
				"hash must be lowercase hex, got %c", c)
		}
	})
}

func TestVerifyScriptHash(t *testing.T) {
	t.Run("matching hash returns nil", func(t *testing.T) {
		script := "def posting_rules(ctx): return []"
		hash := saga.ComputeScriptHash(script)
		assert.NoError(t, saga.VerifyScriptHash(script, hash))
	})

	t.Run("mismatched hash returns ErrScriptHashMismatch", func(t *testing.T) {
		script := "def posting_rules(ctx): return []"
		wrongHash := saga.ComputeScriptHash("different script")
		err := saga.VerifyScriptHash(script, wrongHash)
		require.Error(t, err)
		assert.ErrorIs(t, err, saga.ErrScriptHashMismatch)
	})
}

// ---------------------------------------------------------------------------
// VersionPinning.PinVersion (integration - requires DB)
// ---------------------------------------------------------------------------

func TestVersionPinning_PinVersion(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "vp-pin-tenant")

	script := "def posting_rules(ctx):\n    return []"
	def := &saga.Definition{
		Name:    "payment.process",
		Version: 1,
		Script:  script,
	}
	require.NoError(t, reg.CreateDraft(ctx, def))
	require.NoError(t, reg.ActivateSaga(ctx, def.ID))

	vp := saga.NewVersionPinning(reg)

	t.Run("pins tenant script and records hash", func(t *testing.T) {
		instance := &pkgsaga.SagaInstance{ID: uuid.New()}
		resolved, err := vp.PinVersion(ctx, instance, "payment.process")
		require.NoError(t, err)
		assert.NotNil(t, resolved)
		assert.Equal(t, def.ID, instance.SagaDefinitionID)
		assert.NotEmpty(t, instance.ScriptHashAtStart)
		// Tenant-only saga: no platform version to pin.
		assert.Nil(t, instance.PlatformSagaVersionID)
	})

	t.Run("hash at start matches actual script", func(t *testing.T) {
		instance := &pkgsaga.SagaInstance{ID: uuid.New()}
		_, err := vp.PinVersion(ctx, instance, "payment.process")
		require.NoError(t, err)
		assert.Equal(t, saga.ComputeScriptHash(script), instance.ScriptHashAtStart)
	})

	t.Run("unknown saga name returns error", func(t *testing.T) {
		instance := &pkgsaga.SagaInstance{ID: uuid.New()}
		_, err := vp.PinVersion(ctx, instance, "nonexistent.saga")
		require.Error(t, err)
	})
}

// ---------------------------------------------------------------------------
// VersionPinning.ResolveForReplay (integration - requires DB)
// ---------------------------------------------------------------------------

func TestVersionPinning_ResolveForReplay(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "vp-replay-tenant")

	script := "def posting_rules(ctx):\n    return []"
	def := &saga.Definition{
		Name:    "deposit.process",
		Version: 1,
		Script:  script,
	}
	require.NoError(t, reg.CreateDraft(ctx, def))
	require.NoError(t, reg.ActivateSaga(ctx, def.ID))

	vp := saga.NewVersionPinning(reg)

	t.Run("resolves correct script for tenant saga", func(t *testing.T) {
		instance := &pkgsaga.SagaInstance{ID: uuid.New()}
		_, err := vp.PinVersion(ctx, instance, "deposit.process")
		require.NoError(t, err)

		resolvedScript, err := vp.ResolveForReplay(ctx, instance)
		require.NoError(t, err)
		assert.Equal(t, script, resolvedScript)
	})

	t.Run("corrupted hash returns ErrScriptHashMismatch", func(t *testing.T) {
		instance := &pkgsaga.SagaInstance{
			ID:                    uuid.New(),
			SagaDefinitionID:      def.ID,
			ScriptHashAtStart:     "0000000000000000000000000000000000000000000000000000000000000000",
			PlatformSagaVersionID: nil,
		}

		_, err := vp.ResolveForReplay(ctx, instance)
		require.Error(t, err)
		assert.ErrorIs(t, err, saga.ErrScriptHashMismatch)
	})
}

func TestVersionPinning_ResolveForReplay_PlatformVersion(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "vp-replay-platform")

	platformScript := "def posting_rules(ctx):\n    pass"
	platformID := seedPlatformSaga(t, pool, ctx, "platform.transfer", platformScript)

	tenantDefID := seedTenantSagaWithPlatformRef(t, pool, ctx, "platform.transfer", 1, platformID, "ACTIVE")
	vp := saga.NewVersionPinning(reg)

	t.Run("resolves pinned platform script", func(t *testing.T) {
		instance := &pkgsaga.SagaInstance{
			ID:                    uuid.New(),
			SagaDefinitionID:      tenantDefID,
			PlatformSagaVersionID: &platformID,
			ScriptHashAtStart:     saga.ComputeScriptHash(platformScript),
		}

		resolvedScript, err := vp.ResolveForReplay(ctx, instance)
		require.NoError(t, err)
		assert.Equal(t, platformScript, resolvedScript)
	})
}

// ---------------------------------------------------------------------------
// VersionPinning.GetResolvedDefinition (integration - requires DB)
// ---------------------------------------------------------------------------

func TestVersionPinning_GetResolvedDefinition(t *testing.T) {
	reg, pool := setupTestRegistry(t)
	ctx := setupTenantContext(t, pool, "vp-get-resolved")

	def := &saga.Definition{
		Name:    "refund.process",
		Version: 1,
		Script:  "def posting_rules(ctx):\n    return []",
	}
	require.NoError(t, reg.CreateDraft(ctx, def))
	require.NoError(t, reg.ActivateSaga(ctx, def.ID))

	vp := saga.NewVersionPinning(reg)

	t.Run("returns definition with resolved script", func(t *testing.T) {
		resolved, err := vp.GetResolvedDefinition(ctx, def.ID)
		require.NoError(t, err)
		assert.Equal(t, def.ID, resolved.ID)
		assert.NotEmpty(t, resolved.ResolvedScript)
	})

	t.Run("unknown ID returns ErrNotFound", func(t *testing.T) {
		_, err := vp.GetResolvedDefinition(ctx, uuid.New())
		require.ErrorIs(t, err, saga.ErrNotFound)
	})
}
