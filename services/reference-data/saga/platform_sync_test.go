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
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/cockroachdb"
	"github.com/testcontainers/testcontainers-go/wait"
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

func TestShouldUpdateVersion(t *testing.T) {
	tests := []struct {
		name     string
		existing string
		new      string
		expected bool
		wantErr  bool
	}{
		{
			name:     "new major version",
			existing: "1.0.0",
			new:      "2.0.0",
			expected: true,
		},
		{
			name:     "new minor version",
			existing: "1.0.0",
			new:      "1.1.0",
			expected: true,
		},
		{
			name:     "new patch version",
			existing: "1.0.0",
			new:      "1.0.1",
			expected: true,
		},
		{
			name:     "same version",
			existing: "1.0.0",
			new:      "1.0.0",
			expected: false,
		},
		{
			name:     "older version",
			existing: "2.0.0",
			new:      "1.0.0",
			expected: false,
		},
		{
			name:     "older minor version",
			existing: "1.5.0",
			new:      "1.4.9",
			expected: false,
		},
		{
			name:     "invalid existing version",
			existing: "not-a-version",
			new:      "1.0.0",
			wantErr:  true,
		},
		{
			name:     "invalid new version",
			existing: "1.0.0",
			new:      "invalid",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := shouldUpdateVersion(tt.existing, tt.new)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
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

// setupPlatformTestDB creates a CockroachDB testcontainer with the platform migration applied.
func setupPlatformTestDB(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()

	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()

	// Start CockroachDB container
	container, err := cockroachdb.Run(ctx,
		"cockroachdb/cockroach:v24.3.8",
		cockroachdb.WithDatabase("test_platform_sync"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("CockroachDB node starting").
				WithStartupTimeout(60*time.Second),
		),
	)
	require.NoError(t, err)

	// Get connection string
	connStr, err := container.ConnectionString(ctx)
	require.NoError(t, err)

	// Create pool
	pool, err := pgxpool.New(ctx, connStr)
	require.NoError(t, err)

	// Apply platform saga definition migration
	migrationPath := filepath.Join("..", "migrations", "20260125000001_platform_saga_definition.sql")
	migrationSQL, err := os.ReadFile(migrationPath)
	require.NoError(t, err, "failed to read migration file")

	_, err = pool.Exec(ctx, string(migrationSQL))
	require.NoError(t, err, "failed to apply migration")

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
		// Get initial updated_at timestamps
		type sagaTimestamp struct {
			name      string
			updatedAt time.Time
		}
		var initialTimestamps []sagaTimestamp

		rows, err := pool.Query(ctx, `SELECT name, updated_at FROM public.platform_saga_definition`)
		require.NoError(t, err)
		defer rows.Close()

		for rows.Next() {
			var ts sagaTimestamp
			err := rows.Scan(&ts.name, &ts.updatedAt)
			require.NoError(t, err)
			initialTimestamps = append(initialTimestamps, ts)
		}
		require.NoError(t, rows.Err())

		// Wait a bit to ensure timestamp difference would be visible
		time.Sleep(10 * time.Millisecond)

		// Run sync again
		err = sync.SyncPlatformDefaults(ctx)
		require.NoError(t, err)

		// Verify timestamps haven't changed (compare full timestamps, not just Unix seconds)
		for _, initial := range initialTimestamps {
			var currentUpdatedAt time.Time
			err := pool.QueryRow(ctx, `
				SELECT updated_at FROM public.platform_saga_definition WHERE name = $1
			`, initial.name).Scan(&currentUpdatedAt)
			require.NoError(t, err)
			require.True(t, currentUpdatedAt.Equal(initial.updatedAt),
				"updated_at should not change for %s when version is same", initial.name)
		}
	})

	t.Run("deterministic UUIDs", func(t *testing.T) {
		// Verify UUIDs are deterministic based on saga name
		for _, meta := range PlatformDefaults() {
			expectedID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("platform.saga."+meta.Name))

			var actualID uuid.UUID
			err := pool.QueryRow(ctx, `
				SELECT id FROM public.platform_saga_definition WHERE name = $1
			`, meta.Name).Scan(&actualID)
			require.NoError(t, err)
			assert.Equal(t, expectedID, actualID,
				"UUID for %s should be deterministic", meta.Name)
		}
	})
}

func TestPlatformSync_VersionUpdate(t *testing.T) {
	pool, cleanup := setupPlatformTestDB(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("updates saga when version is newer", func(t *testing.T) {
		// Insert a saga with old version directly
		id := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("platform.saga.test_saga"))
		_, err := pool.Exec(ctx, `
			INSERT INTO public.platform_saga_definition
				(id, name, version, script, display_name, description)
			VALUES ($1, 'test_saga', '0.0.1', 'old script', 'Old Name', 'Old description')
		`, id)
		require.NoError(t, err)

		// Create sync and manually sync a newer version
		sync := NewPlatformSync(pool)
		synced, err := sync.syncSaga(ctx, PlatformSagaDefinition{
			ID:          id,
			Name:        "test_saga",
			Version:     "1.0.0",
			Script:      "new script",
			DisplayName: "New Name",
			Description: "New description",
		})
		require.NoError(t, err)
		assert.True(t, synced, "saga should be updated with newer version")

		// Verify update
		var version, script, displayName string
		err = pool.QueryRow(ctx, `
			SELECT version, script, display_name
			FROM public.platform_saga_definition
			WHERE name = 'test_saga'
		`).Scan(&version, &script, &displayName)
		require.NoError(t, err)
		assert.Equal(t, "1.0.0", version)
		assert.Equal(t, "new script", script)
		assert.Equal(t, "New Name", displayName)
	})

	t.Run("skips saga when version is older", func(t *testing.T) {
		// Insert a saga with newer version directly
		id := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("platform.saga.skip_test"))
		_, err := pool.Exec(ctx, `
			INSERT INTO public.platform_saga_definition
				(id, name, version, script, display_name, description)
			VALUES ($1, 'skip_test', '2.0.0', 'existing script', 'Existing Name', 'Existing description')
		`, id)
		require.NoError(t, err)

		// Try to sync with older version
		sync := NewPlatformSync(pool)
		synced, err := sync.syncSaga(ctx, PlatformSagaDefinition{
			ID:          id,
			Name:        "skip_test",
			Version:     "1.0.0",
			Script:      "older script",
			DisplayName: "Older Name",
			Description: "Older description",
		})
		require.NoError(t, err)
		assert.False(t, synced, "saga should not be updated with older version")

		// Verify no update
		var version, script string
		err = pool.QueryRow(ctx, `
			SELECT version, script
			FROM public.platform_saga_definition
			WHERE name = 'skip_test'
		`).Scan(&version, &script)
		require.NoError(t, err)
		assert.Equal(t, "2.0.0", version, "version should remain unchanged")
		assert.Equal(t, "existing script", script, "script should remain unchanged")
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
		assert.Contains(t, err.Error(), "chk_platform_saga_definition_version")
	})

	t.Run("rejects duplicate name", func(t *testing.T) {
		_, err := pool.Exec(ctx, `
			INSERT INTO public.platform_saga_definition
				(name, version, script)
			VALUES ('duplicate_test', '1.0.0', 'script1')
		`)
		require.NoError(t, err)

		_, err = pool.Exec(ctx, `
			INSERT INTO public.platform_saga_definition
				(name, version, script)
			VALUES ('duplicate_test', '2.0.0', 'script2')
		`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "uq_platform_saga_definition_name")
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
		assert.Contains(t, err.Error(), "chk_platform_saga_definition_script_length")
	})
}
