package saga

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/cockroachdb"
)

func TestExtractVersionFromScript(t *testing.T) {
	tests := []struct {
		name     string
		script   string
		expected string
	}{
		{
			name:     "version at start of file",
			script:   "# Version: 1.2.3\n# Comment\ndef foo(): pass",
			expected: "1.2.3",
		},
		{
			name:     "version with leading whitespace in comment",
			script:   "#   Version:   2.0.0  \ncode here",
			expected: "2.0.0",
		},
		{
			name:     "version in middle of file",
			script:   "# Header\n# Version: 3.1.4\n# Footer",
			expected: "3.1.4",
		},
		{
			name:     "no version comment",
			script:   "# Just a comment\ndef foo(): pass",
			expected: "",
		},
		{
			name:     "invalid version format",
			script:   "# Version: 1.2\n# Missing patch",
			expected: "",
		},
		{
			name:     "version with text after",
			script:   "# Version: 1.0.0 - initial release\ndef foo(): pass",
			expected: "",
		},
		{
			name:     "multiple version comments (takes first)",
			script:   "# Version: 1.0.0\n# Version: 2.0.0\n",
			expected: "1.0.0",
		},
		{
			name:     "zero version",
			script:   "# Version: 0.0.0\ncode",
			expected: "0.0.0",
		},
		{
			name:     "large version numbers",
			script:   "# Version: 123.456.789\ncode",
			expected: "123.456.789",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractVersionFromScript(tt.script)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHumanizeName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "single word",
			input:    "withdrawal",
			expected: "Withdrawal",
		},
		{
			name:     "two words",
			input:    "current_account",
			expected: "Current Account",
		},
		{
			name:     "three words",
			input:    "current_account_withdrawal",
			expected: "Current Account Withdrawal",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "already capitalized",
			input:    "Already_Capitalized",
			expected: "Already Capitalized",
		},
		{
			name:     "all uppercase",
			input:    "ALL_UPPERCASE",
			expected: "ALL UPPERCASE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := humanizeName(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestEmbeddedScriptsHaveVersionComments(t *testing.T) {
	scripts, err := GetEmbeddedScripts()
	require.NoError(t, err)
	require.NotEmpty(t, scripts, "should have embedded scripts")

	for filename, script := range scripts {
		t.Run(filename, func(t *testing.T) {
			version := extractVersionFromScript(script)
			assert.NotEmpty(t, version,
				"embedded script %s should have a Version: comment", filename)
			assert.Regexp(t, `^\d+\.\d+\.\d+$`, version,
				"version should be valid semver format X.Y.Z")
		})
	}
}

// setupPlatformTestDB creates a CockroachDB testcontainer with both the
// platform saga definition migration and the unique constraint fix applied.
func setupPlatformTestDB(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()

	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()

	// Start CockroachDB container in insecure mode to avoid TLS connection issues.
	// The module's default wait strategy properly waits for database readiness.
	container, err := cockroachdb.Run(ctx,
		"cockroachdb/cockroach:v24.3.0",
		cockroachdb.WithDatabase("test_platform_sync"),
		cockroachdb.WithInsecure(),
	)
	require.NoError(t, err)

	// Use ConnectionConfig to get a proper connection string.
	// ConnectionString() returns a registered pgx config reference that pgxpool cannot parse.
	connConfig, err := container.ConnectionConfig(ctx)
	require.NoError(t, err)

	pool, err := pgxpool.New(ctx, connConfig.ConnString())
	require.NoError(t, err)

	// Apply platform saga definition migration (creates table with UNIQUE(name))
	migrationPath := filepath.Join("..", "migrations", "20260125000001_platform_saga_definition.sql")
	migrationSQL, err := os.ReadFile(migrationPath)
	require.NoError(t, err, "failed to read platform saga definition migration")

	_, err = pool.Exec(ctx, string(migrationSQL))
	require.NoError(t, err, "failed to apply platform saga definition migration")

	// Apply unique constraint fix migration (UNIQUE(name) -> UNIQUE(name, version))
	fixMigrationPath := filepath.Join("..", "migrations", "20260127000001_fix_platform_saga_unique_constraint.sql")
	fixMigrationSQL, err := os.ReadFile(fixMigrationPath)
	require.NoError(t, err, "failed to read unique constraint fix migration")

	_, err = pool.Exec(ctx, string(fixMigrationSQL))
	require.NoError(t, err, "failed to apply unique constraint fix migration")

	cleanup := func() {
		pool.Close()
		_ = container.Terminate(ctx)
	}

	return pool, cleanup
}

func TestPlatformSync_SyncPlatformDefaults(t *testing.T) {
	pool, cleanup := setupPlatformTestDB(t)
	defer cleanup()

	ctx := context.Background()
	sync := NewPlatformSync(pool)

	t.Run("initial sync inserts all sagas", func(t *testing.T) {
		err := sync.SyncPlatformDefaults(ctx)
		require.NoError(t, err)

		// Verify sagas were inserted
		var count int
		err = pool.QueryRow(ctx, `SELECT COUNT(*) FROM public.platform_saga_definition`).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, len(PlatformDefaults()), count, "expected all platform defaults to be inserted")

		// Verify each saga has correct fields
		for _, meta := range PlatformDefaults() {
			var name, displayName, description string
			var version string
			err := pool.QueryRow(ctx, `
				SELECT name, version, display_name, description
				FROM public.platform_saga_definition
				WHERE name = $1
			`, meta.Name).Scan(&name, &version, &displayName, &description)
			require.NoError(t, err, "saga %s should exist", meta.Name)
			assert.Equal(t, meta.Name, name)
			assert.Equal(t, meta.DisplayName, displayName)
			assert.Equal(t, meta.Description, description)
			// Version should be either from script or default 1.0.0
			assert.Regexp(t, `^\d+\.\d+\.\d+$`, version)
		}
	})

	t.Run("idempotent sync - no changes when same version", func(t *testing.T) {
		// Get initial row count
		var initialCount int
		err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM public.platform_saga_definition`).Scan(&initialCount)
		require.NoError(t, err)

		// Get initial updated_at timestamps
		type sagaTimestamp struct {
			name      string
			version   string
			updatedAt time.Time
		}
		var initialTimestamps []sagaTimestamp

		rows, err := pool.Query(ctx, `SELECT name, version, updated_at FROM public.platform_saga_definition`)
		require.NoError(t, err)
		defer rows.Close()

		for rows.Next() {
			var ts sagaTimestamp
			err := rows.Scan(&ts.name, &ts.version, &ts.updatedAt)
			require.NoError(t, err)
			initialTimestamps = append(initialTimestamps, ts)
		}
		require.NoError(t, rows.Err())

		// Wait a bit to ensure timestamp difference would be visible
		time.Sleep(10 * time.Millisecond)

		// Run sync again
		err = sync.SyncPlatformDefaults(ctx)
		require.NoError(t, err)

		// Verify no new rows were added
		var currentCount int
		err = pool.QueryRow(ctx, `SELECT COUNT(*) FROM public.platform_saga_definition`).Scan(&currentCount)
		require.NoError(t, err)
		assert.Equal(t, initialCount, currentCount, "no new rows should be added on idempotent sync")

		// Verify timestamps haven't changed
		for _, initial := range initialTimestamps {
			var currentUpdatedAt time.Time
			err := pool.QueryRow(ctx, `
				SELECT updated_at FROM public.platform_saga_definition
				WHERE name = $1 AND version = $2
			`, initial.name, initial.version).Scan(&currentUpdatedAt)
			require.NoError(t, err)
			require.True(t, currentUpdatedAt.Equal(initial.updatedAt),
				"updated_at should not change for %s@%s when version is same", initial.name, initial.version)
		}
	})

	t.Run("deterministic UUIDs based on name and version", func(t *testing.T) {
		// Verify UUIDs are deterministic based on saga name and version
		for _, meta := range PlatformDefaults() {
			// Get the version from the embedded script
			script, err := GetEmbeddedScripts()
			require.NoError(t, err)
			version := extractVersionFromScript(script[meta.Filename])
			if version == "" {
				version = "1.0.0"
			}

			expectedID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("platform.saga."+meta.Name+"."+version))

			var actualID uuid.UUID
			err = pool.QueryRow(ctx, `
				SELECT id FROM public.platform_saga_definition
				WHERE name = $1 AND version = $2
			`, meta.Name, version).Scan(&actualID)
			require.NoError(t, err)
			assert.Equal(t, expectedID, actualID,
				"UUID for %s@%s should be deterministic", meta.Name, version)
		}
	})
}

func TestPlatformSync_InsertOnlyBehavior(t *testing.T) {
	pool, cleanup := setupPlatformTestDB(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("inserts new version as separate row", func(t *testing.T) {
		// Insert v1.0.0
		oldID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("platform.saga.test_saga.1.0.0"))
		_, err := pool.Exec(ctx, `
			INSERT INTO public.platform_saga_definition
				(id, name, version, script, display_name, description)
			VALUES ($1, 'test_saga', '1.0.0', 'old script v1', 'Old Name', 'Old description')
		`, oldID)
		require.NoError(t, err)

		// Sync v2.0.0 via syncSaga
		sync := NewPlatformSync(pool)
		newID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("platform.saga.test_saga.2.0.0"))
		synced, err := sync.syncSaga(ctx, PlatformSagaDefinition{
			ID:          newID,
			Name:        "test_saga",
			Version:     "2.0.0",
			Script:      "new script v2",
			DisplayName: "New Name",
			Description: "New description",
		})
		require.NoError(t, err)
		assert.True(t, synced, "new version should be inserted")

		// Verify both versions exist
		var count int
		err = pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM public.platform_saga_definition WHERE name = 'test_saga'
		`).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 2, count, "both v1 and v2 should exist")

		// Verify old version is untouched
		var oldScript string
		err = pool.QueryRow(ctx, `
			SELECT script FROM public.platform_saga_definition
			WHERE name = 'test_saga' AND version = '1.0.0'
		`).Scan(&oldScript)
		require.NoError(t, err)
		assert.Equal(t, "old script v1", oldScript, "old version script should be unchanged")

		// Verify new version has new data
		var newScript string
		err = pool.QueryRow(ctx, `
			SELECT script FROM public.platform_saga_definition
			WHERE name = 'test_saga' AND version = '2.0.0'
		`).Scan(&newScript)
		require.NoError(t, err)
		assert.Equal(t, "new script v2", newScript, "new version should have new script")
	})

	t.Run("insert same version twice is idempotent", func(t *testing.T) {
		sync := NewPlatformSync(pool)
		id := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("platform.saga.idempotent_saga.1.0.0"))

		saga := PlatformSagaDefinition{
			ID:          id,
			Name:        "idempotent_saga",
			Version:     "1.0.0",
			Script:      "idempotent script",
			DisplayName: "Idempotent",
			Description: "Test idempotency",
		}

		// First insert
		synced, err := sync.syncSaga(ctx, saga)
		require.NoError(t, err)
		assert.True(t, synced, "first insert should succeed")

		// Second insert of same (name, version) - should be skipped
		synced, err = sync.syncSaga(ctx, saga)
		require.NoError(t, err)
		assert.False(t, synced, "duplicate (name, version) should be skipped")

		// Verify only one row exists
		var count int
		err = pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM public.platform_saga_definition
			WHERE name = 'idempotent_saga' AND version = '1.0.0'
		`).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count, "exactly one row should exist for same (name, version)")
	})

	t.Run("multiple versions of same saga coexist in database", func(t *testing.T) {
		sync := NewPlatformSync(pool)
		versions := []string{"1.0.0", "1.1.0", "2.0.0"}

		for _, v := range versions {
			id := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("platform.saga.multi_version."+v))
			synced, err := sync.syncSaga(ctx, PlatformSagaDefinition{
				ID:          id,
				Name:        "multi_version",
				Version:     v,
				Script:      "script for " + v,
				DisplayName: "Multi Version",
				Description: "Version " + v,
			})
			require.NoError(t, err)
			assert.True(t, synced, "version %s should be inserted", v)
		}

		// All three versions should exist
		var count int
		err := pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM public.platform_saga_definition
			WHERE name = 'multi_version'
		`).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 3, count, "all three versions should coexist")

		// Each version should have its own script
		for _, v := range versions {
			var script string
			err := pool.QueryRow(ctx, `
				SELECT script FROM public.platform_saga_definition
				WHERE name = 'multi_version' AND version = $1
			`, v).Scan(&script)
			require.NoError(t, err)
			assert.Equal(t, "script for "+v, script, "version %s should have correct script", v)
		}
	})

	t.Run("old versions remain accessible after inserting new version", func(t *testing.T) {
		sync := NewPlatformSync(pool)

		// Insert v1.0.0
		v1ID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("platform.saga.retained_saga.1.0.0"))
		synced, err := sync.syncSaga(ctx, PlatformSagaDefinition{
			ID:          v1ID,
			Name:        "retained_saga",
			Version:     "1.0.0",
			Script:      "original script content",
			DisplayName: "Retained Saga",
			Description: "Original version",
		})
		require.NoError(t, err)
		assert.True(t, synced)

		// Record the v1.0.0 data
		var v1Script string
		var v1UpdatedAt time.Time
		err = pool.QueryRow(ctx, `
			SELECT script, updated_at FROM public.platform_saga_definition
			WHERE id = $1
		`, v1ID).Scan(&v1Script, &v1UpdatedAt)
		require.NoError(t, err)
		assert.Equal(t, "original script content", v1Script)

		// Insert v2.0.0
		v2ID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("platform.saga.retained_saga.2.0.0"))
		synced, err = sync.syncSaga(ctx, PlatformSagaDefinition{
			ID:          v2ID,
			Name:        "retained_saga",
			Version:     "2.0.0",
			Script:      "updated script content",
			DisplayName: "Retained Saga",
			Description: "Updated version",
		})
		require.NoError(t, err)
		assert.True(t, synced)

		// Verify v1.0.0 is completely unchanged (script, timestamps)
		var v1ScriptAfter string
		var v1UpdatedAtAfter time.Time
		err = pool.QueryRow(ctx, `
			SELECT script, updated_at FROM public.platform_saga_definition
			WHERE id = $1
		`, v1ID).Scan(&v1ScriptAfter, &v1UpdatedAtAfter)
		require.NoError(t, err)
		assert.Equal(t, "original script content", v1ScriptAfter, "v1 script must be unchanged")
		assert.True(t, v1UpdatedAtAfter.Equal(v1UpdatedAt), "v1 updated_at must be unchanged")

		// Verify both are independently accessible by ID
		var v2Script string
		err = pool.QueryRow(ctx, `
			SELECT script FROM public.platform_saga_definition WHERE id = $1
		`, v2ID).Scan(&v2Script)
		require.NoError(t, err)
		assert.Equal(t, "updated script content", v2Script)
	})
}

func TestPlatformSync_ReplayDeterminism(t *testing.T) {
	pool, cleanup := setupPlatformTestDB(t)
	defer cleanup()

	ctx := context.Background()
	sync := NewPlatformSync(pool)

	t.Run("pinned version survives new platform inserts", func(t *testing.T) {
		// Simulate the production scenario:
		// 1. v1.0.0 is synced to platform table
		// 2. A saga instance starts and pins PlatformSagaVersionID = v1.0.0 row ID
		// 3. v1.1.0 is synced (new release)
		// 4. The running instance replays using the pinned v1.0.0 ID
		// 5. The v1.0.0 script must still be the original content

		v1Script := "# Version: 1.0.0\ndef posting_rules(ctx):\n    return debit('clearing', ctx.amount)"
		v1ID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("platform.saga.replay_test.1.0.0"))

		// Step 1: Sync v1.0.0
		synced, err := sync.syncSaga(ctx, PlatformSagaDefinition{
			ID:      v1ID,
			Name:    "replay_test",
			Version: "1.0.0",
			Script:  v1Script,
		})
		require.NoError(t, err)
		assert.True(t, synced)

		// Step 2: Simulate pinning - record the ID and compute script hash
		pinnedVersionID := v1ID
		pinnedHash := ComputeScriptHash(v1Script)

		// Step 3: Sync v1.1.0 (simulates a platform upgrade)
		v2Script := "# Version: 1.1.0\ndef posting_rules(ctx):\n    return debit('clearing', ctx.amount) + credit('revenue', ctx.fee)"
		v2ID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("platform.saga.replay_test.1.1.0"))

		synced, err = sync.syncSaga(ctx, PlatformSagaDefinition{
			ID:      v2ID,
			Name:    "replay_test",
			Version: "1.1.0",
			Script:  v2Script,
		})
		require.NoError(t, err)
		assert.True(t, synced)

		// Step 4: Replay using pinned version ID - load the script
		var replayScript string
		err = pool.QueryRow(ctx, `
			SELECT script FROM public.platform_saga_definition WHERE id = $1
		`, pinnedVersionID).Scan(&replayScript)
		require.NoError(t, err)

		// Step 5: Verify the replay script matches the original
		assert.Equal(t, v1Script, replayScript,
			"replayed script must match the original v1.0.0 content")

		// Verify hash still matches (deterministic replay)
		replayHash := ComputeScriptHash(replayScript)
		assert.Equal(t, pinnedHash, replayHash,
			"script hash must match the pinned hash for replay determinism")

		// Also verify that the v1.1.0 version exists separately
		var v2ScriptFromDB string
		err = pool.QueryRow(ctx, `
			SELECT script FROM public.platform_saga_definition WHERE id = $1
		`, v2ID).Scan(&v2ScriptFromDB)
		require.NoError(t, err)
		assert.Equal(t, v2Script, v2ScriptFromDB)
		assert.NotEqual(t, v1Script, v2ScriptFromDB,
			"v1.1.0 script should differ from v1.0.0")
	})
}

func TestPlatformSync_MigrationConstraints(t *testing.T) {
	pool, cleanup := setupPlatformTestDB(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("rejects invalid semver version", func(t *testing.T) {
		_, err := pool.Exec(ctx, `
			INSERT INTO public.platform_saga_definition
				(name, version, script)
			VALUES ('invalid_version', '1.2', 'script')
		`)
		require.Error(t, err)
		// CockroachDB uses SQLSTATE 23514 for CHECK violations but does not
		// include the constraint name in the error message like PostgreSQL.
		assert.Contains(t, err.Error(), "23514")
	})

	t.Run("allows same name with different versions", func(t *testing.T) {
		_, err := pool.Exec(ctx, `
			INSERT INTO public.platform_saga_definition
				(name, version, script)
			VALUES ('multi_version_test', '1.0.0', 'script1')
		`)
		require.NoError(t, err)

		_, err = pool.Exec(ctx, `
			INSERT INTO public.platform_saga_definition
				(name, version, script)
			VALUES ('multi_version_test', '2.0.0', 'script2')
		`)
		require.NoError(t, err, "same name with different version should be allowed")
	})

	t.Run("rejects duplicate name and version", func(t *testing.T) {
		_, err := pool.Exec(ctx, `
			INSERT INTO public.platform_saga_definition
				(name, version, script)
			VALUES ('dup_name_version', '1.0.0', 'script1')
		`)
		require.NoError(t, err)

		_, err = pool.Exec(ctx, `
			INSERT INTO public.platform_saga_definition
				(name, version, script)
			VALUES ('dup_name_version', '1.0.0', 'script2')
		`)
		require.Error(t, err)
		// CockroachDB uses SQLSTATE 23505 for unique constraint violations.
		assert.Contains(t, err.Error(), "23505")
	})

	t.Run("accepts script up to 64KB", func(t *testing.T) {
		largeScript := make([]byte, 65536)
		for i := range largeScript {
			largeScript[i] = 'x'
		}

		_, err := pool.Exec(ctx, `
			INSERT INTO public.platform_saga_definition
				(name, version, script)
			VALUES ('large_script', '1.0.0', $1)
		`, string(largeScript))
		require.NoError(t, err)
	})

	t.Run("rejects script over 64KB", func(t *testing.T) {
		tooLargeScript := make([]byte, 65537)
		for i := range tooLargeScript {
			tooLargeScript[i] = 'x'
		}

		_, err := pool.Exec(ctx, `
			INSERT INTO public.platform_saga_definition
				(name, version, script)
			VALUES ('too_large_script', '1.0.0', $1)
		`, string(tooLargeScript))
		require.Error(t, err)
		// CockroachDB uses SQLSTATE 23514 for CHECK violations.
		assert.Contains(t, err.Error(), "23514")
	})
}
