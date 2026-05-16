package applier

import (
	"context"
	"testing"

	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestManifestExecutor_PinSagaDefinition verifies that the executor writes a
// saga_definitions row when it pins the resolved apply_manifest script. This is
// the durable-resume parity guarantee: future calls to FindByID can return the
// exact script that was used to start the apply.
func TestManifestExecutor_PinSagaDefinition(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pool := testdb.NewTestPool(t, testdb.WithMigrations("control-plane"))
	ctx := context.Background()

	executor := NewManifestExecutor(ManifestExecutorConfig{Pool: pool})

	const (
		name    = "apply_manifest"
		version = "1.5.0"
		script  = "def main(): return {'ok': True}"
	)

	executor.pinSagaDefinition(ctx, name, version, script)

	// Verify the row exists by reading directly from the DB. Using FindOrCreate
	// here would mask a pinning regression: it would insert the row itself if
	// pinSagaDefinition had failed, and the assertions would still pass.
	var (
		storedScript     string
		storedScriptHash string
	)
	err := pool.QueryRow(ctx,
		`SELECT script, script_hash FROM saga_definitions WHERE name = $1 AND version = $2`,
		name, version,
	).Scan(&storedScript, &storedScriptHash)
	require.NoError(t, err, "pinSagaDefinition must have written a saga_definitions row")
	assert.Equal(t, script, storedScript)
	assert.Equal(t, saga.ComputeSagaDefinitionScriptHash(script), storedScriptHash)
}

// TestManifestExecutor_PinSagaDefinition_Idempotent verifies that pinning the
// same (name, version, script) twice does not create duplicate rows or error.
func TestManifestExecutor_PinSagaDefinition_Idempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pool := testdb.NewTestPool(t, testdb.WithMigrations("control-plane"))
	ctx := context.Background()

	executor := NewManifestExecutor(ManifestExecutorConfig{Pool: pool})

	const (
		name    = "apply_manifest"
		version = "2.0.0"
		script  = "def main(): return None"
	)

	executor.pinSagaDefinition(ctx, name, version, script)
	executor.pinSagaDefinition(ctx, name, version, script)

	// Count rows for (name, version) - must be exactly one.
	var count int
	err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM saga_definitions WHERE name = $1 AND version = $2`,
		name, version,
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

// TestManifestExecutor_PinSagaDefinition_NilRepoIsNoOp confirms that an executor
// constructed without a pgxpool (e.g. unit tests) skips pinning silently rather
// than panicking.
func TestManifestExecutor_PinSagaDefinition_NilRepoIsNoOp(_ *testing.T) {
	executor := NewManifestExecutor(ManifestExecutorConfig{})
	// sagaDefRepo is nil when Pool is nil; pinSagaDefinition must tolerate this.
	executor.pinSagaDefinition(context.Background(), "any", "1.0.0", "def main(): pass")
}
