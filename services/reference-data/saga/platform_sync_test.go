package saga

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestParseMetadataHeader(t *testing.T) {
	t.Run("parses all metadata fields", func(t *testing.T) {
		script := `# Saga: current_account_deposit
# Version: 1.1.0
# Previous: 1.0.0
# Changed: Added fee calculation step
# Author: Platform Team
# Date: 2026-01-26
def foo(): pass`

		meta := parseMetadataHeader(script)
		assert.Equal(t, "current_account_deposit", meta.SagaName)
		assert.Equal(t, "1.1.0", meta.Version)
		require.NotNil(t, meta.PreviousVersion)
		assert.Equal(t, "1.0.0", *meta.PreviousVersion)
		assert.Equal(t, "Added fee calculation step", meta.ChangeSummary)
		assert.Equal(t, "Platform Team", meta.Author)
		assert.Equal(t, "2026-01-26", meta.Date)
	})

	t.Run("handles missing optional fields", func(t *testing.T) {
		script := `# Saga: test_saga
# Version: 1.0.0
def foo(): pass`

		meta := parseMetadataHeader(script)
		assert.Equal(t, "test_saga", meta.SagaName)
		assert.Equal(t, "1.0.0", meta.Version)
		assert.Nil(t, meta.PreviousVersion)
		assert.Empty(t, meta.ChangeSummary)
	})

	t.Run("treats Previous: none as nil", func(t *testing.T) {
		script := `# Saga: test_saga
# Version: 1.0.0
# Previous: none
def foo(): pass`

		meta := parseMetadataHeader(script)
		assert.Nil(t, meta.PreviousVersion)
	})

	t.Run("empty script returns empty metadata", func(t *testing.T) {
		meta := parseMetadataHeader("")
		assert.Empty(t, meta.SagaName)
		assert.Nil(t, meta.PreviousVersion)
	})
}

func TestVersionFilenameRegex(t *testing.T) {
	tests := []struct {
		filename string
		expected string
	}{
		{"v1.0.0.star", "1.0.0"},
		{"v2.1.3.star", "2.1.3"},
		{"v10.20.30.star", "10.20.30"},
		{"deposit.star", ""},
		{"v1.0.star", ""},
		{"readme.md", ""},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			matches := versionFilenameRegex.FindStringSubmatch(tt.filename)
			if tt.expected == "" {
				assert.Nil(t, matches)
			} else {
				require.NotNil(t, matches)
				assert.Equal(t, tt.expected, matches[1])
			}
		})
	}
}

// setupPlatformTestDB creates a CockroachDB testcontainer with the full
// platform saga definition schema including status and previous_version columns.
func setupPlatformTestDB(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()

	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()

	// Create a unique database per test for isolation (tests write to public schema tables).
	suffix := strings.ReplaceAll(strings.ToLower(t.Name()), "/", "_")
	suffix = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return '_'
	}, suffix)
	if len(suffix) > 30 {
		suffix = suffix[:30]
	}
	dbName := fmt.Sprintf("t_%s_%s", suffix, strings.ReplaceAll(uuid.New().String(), "-", "")[:8])

	// Connect to shared CockroachDB container to create per-test database
	adminPool, err := pgxpool.New(ctx, sharedCrdbDSN)
	require.NoError(t, err)
	t.Cleanup(func() { adminPool.Close() })

	_, err = adminPool.Exec(ctx, "CREATE DATABASE "+dbName)
	require.NoError(t, err)

	// Build DSN for the per-test database
	testDSN := replaceDatabaseInDSN(sharedCrdbDSN, dbName)
	pool, err := pgxpool.New(ctx, testDSN)
	require.NoError(t, err)
	t.Cleanup(func() { pool.Close() })

	// Apply migrations in order
	migrations := []string{
		"20260125000001_platform_saga_definition.sql",
		"20260127000001_fix_platform_saga_unique_constraint.sql",
		"20260128000001_versioned_platform_sagas.sql",
		"20260128000002_versioned_platform_sagas_constraints.sql",
		"20260129000001_bitemporal_platform_sagas.sql",
		"20260129000002_bitemporal_platform_sagas_constraints.sql",
	}

	for _, migration := range migrations {
		migrationPath := filepath.Join("..", "migrations", migration)
		migrationSQL, err := os.ReadFile(migrationPath)
		require.NoError(t, err, "failed to read migration %s", migration)

		_, err = pool.Exec(ctx, string(migrationSQL))
		require.NoError(t, err, "failed to apply migration %s", migration)
	}

	return pool, func() {}
}

// replaceDatabaseInDSN swaps the database name in a PostgreSQL DSN.
func replaceDatabaseInDSN(dsn, newDB string) string {
	// DSN format: postgres://user@host:port/database?params
	// Find the last / before ? and replace the database name
	qIdx := strings.Index(dsn, "?")
	base := dsn
	query := ""
	if qIdx >= 0 {
		base = dsn[:qIdx]
		query = dsn[qIdx:]
	}
	lastSlash := strings.LastIndex(base, "/")
	if lastSlash >= 0 {
		return base[:lastSlash+1] + newDB + query
	}
	return dsn
}

func TestPlatformSync_SyncPlatformDefaults(t *testing.T) {
	pool, cleanup := setupPlatformTestDB(t)
	defer cleanup()

	ctx := context.Background()
	sync := NewPlatformSync(pool)

	t.Run("initial sync inserts all sagas with ACTIVE status", func(t *testing.T) {
		err := sync.SyncPlatformDefaults(ctx)
		require.NoError(t, err)

		// Verify all unique sagas were inserted (some may have multiple versions)
		var count int
		err = pool.QueryRow(ctx, `SELECT COUNT(DISTINCT name) FROM public.platform_saga_definition`).Scan(&count)
		require.NoError(t, err)
		defaults, defaultsErr := PlatformDefaults()
		require.NoError(t, defaultsErr)
		assert.Equal(t, len(defaults), count, "expected all platform defaults to be inserted")

		// Verify each saga has an ACTIVE version with correct fields
		for _, meta := range defaults {
			var name, displayName, description, version, status string
			err := pool.QueryRow(ctx, `
				SELECT name, version, display_name, description, status
				FROM public.platform_saga_definition
				WHERE name = $1 AND status = 'ACTIVE'
			`, meta.Name).Scan(&name, &version, &displayName, &description, &status)
			require.NoError(t, err, "saga %s should have an ACTIVE version", meta.Name)
			assert.Equal(t, meta.Name, name)
			assert.Equal(t, meta.DisplayName, displayName)
			assert.Regexp(t, `^\d+\.\d+\.\d+$`, version)
			assert.Equal(t, "ACTIVE", status)
		}
	})

	t.Run("idempotent sync - no changes when same version", func(t *testing.T) {
		// Get initial row count
		var initialCount int
		err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM public.platform_saga_definition`).Scan(&initialCount)
		require.NoError(t, err)

		// Run sync again
		err = sync.SyncPlatformDefaults(ctx)
		require.NoError(t, err)

		// Verify no new rows were added
		var currentCount int
		err = pool.QueryRow(ctx, `SELECT COUNT(*) FROM public.platform_saga_definition`).Scan(&currentCount)
		require.NoError(t, err)
		assert.Equal(t, initialCount, currentCount, "no new rows should be added on idempotent sync")
	})

	t.Run("deterministic UUIDs based on name and version", func(t *testing.T) {
		// Verify UUIDs are deterministic based on saga name and version
		uuidDefaults, uuidDefaultsErr := PlatformDefaults()
		require.NoError(t, uuidDefaultsErr)
		for _, meta := range uuidDefaults {
			// Get the version from the embedded script using the directory key
			scripts, err := GetEmbeddedScripts()
			require.NoError(t, err)
			// Use backward-compatible flat key
			script, ok := scripts[meta.Filename+".star"]
			require.True(t, ok, "expected embedded script %s.star", meta.Filename)
			require.NotEmpty(t, script)
			version := extractVersionFromScript(script)
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
		// Insert v1.0.0 directly (with status column)
		oldID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("platform.saga.test_saga.1.0.0"))
		_, err := pool.Exec(ctx, `
			INSERT INTO public.platform_saga_definition
				(id, name, version, script, status, display_name, description)
			VALUES ($1, 'test_saga', '1.0.0', 'old script v1', 'ACTIVE', 'Old Name', 'Old description')
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

func TestPlatformSync_ActiveDeprecatedLifecycle(t *testing.T) {
	pool, cleanup := setupPlatformTestDB(t)
	defer cleanup()

	ctx := context.Background()
	sync := NewPlatformSync(pool)

	t.Run("activateLatestVersions promotes highest version per saga", func(t *testing.T) {
		// Insert multiple versions of two sagas
		versions := []struct {
			name    string
			version string
		}{
			{"lifecycle_saga_a", "1.0.0"},
			{"lifecycle_saga_a", "1.1.0"},
			{"lifecycle_saga_a", "2.0.0"},
			{"lifecycle_saga_b", "1.0.0"},
			{"lifecycle_saga_b", "3.0.0"},
		}

		for _, v := range versions {
			id := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("platform.saga."+v.name+"."+v.version))
			_, err := sync.syncSaga(ctx, PlatformSagaDefinition{
				ID:      id,
				Name:    v.name,
				Version: v.version,
				Script:  "script " + v.name + " " + v.version,
			})
			require.NoError(t, err)
		}

		// Run activate
		err := sync.activateLatestVersions(ctx)
		require.NoError(t, err)

		// Verify saga_a: 2.0.0 is ACTIVE, others DEPRECATED
		var status string
		err = pool.QueryRow(ctx, `
			SELECT status FROM public.platform_saga_definition
			WHERE name = 'lifecycle_saga_a' AND version = '2.0.0'
		`).Scan(&status)
		require.NoError(t, err)
		assert.Equal(t, "ACTIVE", status)

		err = pool.QueryRow(ctx, `
			SELECT status FROM public.platform_saga_definition
			WHERE name = 'lifecycle_saga_a' AND version = '1.0.0'
		`).Scan(&status)
		require.NoError(t, err)
		assert.Equal(t, "DEPRECATED", status)

		err = pool.QueryRow(ctx, `
			SELECT status FROM public.platform_saga_definition
			WHERE name = 'lifecycle_saga_a' AND version = '1.1.0'
		`).Scan(&status)
		require.NoError(t, err)
		assert.Equal(t, "DEPRECATED", status)

		// Verify saga_b: 3.0.0 is ACTIVE
		err = pool.QueryRow(ctx, `
			SELECT status FROM public.platform_saga_definition
			WHERE name = 'lifecycle_saga_b' AND version = '3.0.0'
		`).Scan(&status)
		require.NoError(t, err)
		assert.Equal(t, "ACTIVE", status)

		err = pool.QueryRow(ctx, `
			SELECT status FROM public.platform_saga_definition
			WHERE name = 'lifecycle_saga_b' AND version = '1.0.0'
		`).Scan(&status)
		require.NoError(t, err)
		assert.Equal(t, "DEPRECATED", status)
	})

	t.Run("v1.0.0 becomes DEPRECATED when v1.1.0 is synced", func(t *testing.T) {
		// Insert v1.0.0
		v1ID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("platform.saga.version_chain.1.0.0"))
		_, err := sync.syncSaga(ctx, PlatformSagaDefinition{
			ID:      v1ID,
			Name:    "version_chain",
			Version: "1.0.0",
			Script:  "# Version: 1.0.0\nv1 content",
		})
		require.NoError(t, err)

		// Activate v1.0.0
		err = sync.activateLatestVersions(ctx)
		require.NoError(t, err)

		var status string
		err = pool.QueryRow(ctx, `
			SELECT status FROM public.platform_saga_definition
			WHERE name = 'version_chain' AND version = '1.0.0'
		`).Scan(&status)
		require.NoError(t, err)
		assert.Equal(t, "ACTIVE", status, "v1.0.0 should be ACTIVE initially")

		// Add v1.1.0 and re-activate
		prev := "1.0.0"
		v2ID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("platform.saga.version_chain.1.1.0"))
		_, err = sync.syncSaga(ctx, PlatformSagaDefinition{
			ID:              v2ID,
			Name:            "version_chain",
			Version:         "1.1.0",
			Script:          "# Version: 1.1.0\nv1.1 content",
			PreviousVersion: &prev,
		})
		require.NoError(t, err)

		err = sync.activateLatestVersions(ctx)
		require.NoError(t, err)

		// v1.0.0 should now be DEPRECATED
		err = pool.QueryRow(ctx, `
			SELECT status FROM public.platform_saga_definition
			WHERE name = 'version_chain' AND version = '1.0.0'
		`).Scan(&status)
		require.NoError(t, err)
		assert.Equal(t, "DEPRECATED", status, "v1.0.0 should be DEPRECATED after v1.1.0 sync")

		// v1.1.0 should be ACTIVE
		err = pool.QueryRow(ctx, `
			SELECT status FROM public.platform_saga_definition
			WHERE name = 'version_chain' AND version = '1.1.0'
		`).Scan(&status)
		require.NoError(t, err)
		assert.Equal(t, "ACTIVE", status, "v1.1.0 should be ACTIVE")

		// v1.0.0 script should still be accessible (immutable)
		var script string
		err = pool.QueryRow(ctx, `
			SELECT script FROM public.platform_saga_definition WHERE id = $1
		`, v1ID).Scan(&script)
		require.NoError(t, err)
		assert.Contains(t, script, "v1 content", "DEPRECATED v1.0.0 script should remain accessible")
	})

	t.Run("previous_version column stores version chain", func(t *testing.T) {
		prev := "1.0.0"
		chainID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("platform.saga.chain_test.1.1.0"))
		_, err := sync.syncSaga(ctx, PlatformSagaDefinition{
			ID:              chainID,
			Name:            "chain_test",
			Version:         "1.1.0",
			Script:          "chain script",
			PreviousVersion: &prev,
		})
		require.NoError(t, err)

		var previousVersion *string
		err = pool.QueryRow(ctx, `
			SELECT previous_version FROM public.platform_saga_definition
			WHERE name = 'chain_test' AND version = '1.1.0'
		`).Scan(&previousVersion)
		require.NoError(t, err)
		require.NotNil(t, previousVersion)
		assert.Equal(t, "1.0.0", *previousVersion)

		// v1.0.0 with no previous
		v1ID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("platform.saga.chain_test.1.0.0"))
		_, err = sync.syncSaga(ctx, PlatformSagaDefinition{
			ID:      v1ID,
			Name:    "chain_test",
			Version: "1.0.0",
			Script:  "initial script",
		})
		require.NoError(t, err)

		var prevVer *string
		err = pool.QueryRow(ctx, `
			SELECT previous_version FROM public.platform_saga_definition
			WHERE name = 'chain_test' AND version = '1.0.0'
		`).Scan(&prevVer)
		require.NoError(t, err)
		assert.Nil(t, prevVer, "v1.0.0 should have NULL previous_version")
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

		// Activate it
		err = sync.activateLatestVersions(ctx)
		require.NoError(t, err)

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

		// Re-activate (v1.1.0 becomes ACTIVE, v1.0.0 DEPRECATED)
		err = sync.activateLatestVersions(ctx)
		require.NoError(t, err)

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

		// Verify v1.0.0 is DEPRECATED but still accessible
		var status string
		err = pool.QueryRow(ctx, `
			SELECT status FROM public.platform_saga_definition WHERE id = $1
		`, pinnedVersionID).Scan(&status)
		require.NoError(t, err)
		assert.Equal(t, "DEPRECATED", status, "v1.0.0 should be DEPRECATED but still queryable")

		// Also verify that the v1.1.0 version exists separately and is ACTIVE
		var v2ScriptFromDB, v2Status string
		err = pool.QueryRow(ctx, `
			SELECT script, status FROM public.platform_saga_definition WHERE id = $1
		`, v2ID).Scan(&v2ScriptFromDB, &v2Status)
		require.NoError(t, err)
		assert.Equal(t, v2Script, v2ScriptFromDB)
		assert.Equal(t, "ACTIVE", v2Status)
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
				(name, version, script, status)
			VALUES ('invalid_version', '1.2', 'script', 'ACTIVE')
		`)
		require.Error(t, err)
		// CockroachDB uses SQLSTATE 23514 for CHECK violations but does not
		// include the constraint name in the error message like PostgreSQL.
		assert.Contains(t, err.Error(), "23514")
	})

	t.Run("rejects invalid status", func(t *testing.T) {
		_, err := pool.Exec(ctx, `
			INSERT INTO public.platform_saga_definition
				(name, version, script, status)
			VALUES ('invalid_status', '1.0.0', 'script', 'INVALID')
		`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "23514")
	})

	t.Run("rejects invalid previous_version format", func(t *testing.T) {
		_, err := pool.Exec(ctx, `
			INSERT INTO public.platform_saga_definition
				(name, version, script, status, previous_version)
			VALUES ('invalid_prev', '1.0.0', 'script', 'ACTIVE', 'not-a-semver')
		`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "23514")
	})

	t.Run("accepts valid previous_version", func(t *testing.T) {
		_, err := pool.Exec(ctx, `
			INSERT INTO public.platform_saga_definition
				(name, version, script, status, previous_version)
			VALUES ('valid_prev', '1.1.0', 'script', 'ACTIVE', '1.0.0')
		`)
		require.NoError(t, err)
	})

	t.Run("accepts NULL previous_version", func(t *testing.T) {
		_, err := pool.Exec(ctx, `
			INSERT INTO public.platform_saga_definition
				(name, version, script, status, previous_version)
			VALUES ('null_prev', '1.0.0', 'script', 'ACTIVE', NULL)
		`)
		require.NoError(t, err)
	})

	t.Run("allows same name with different versions", func(t *testing.T) {
		_, err := pool.Exec(ctx, `
			INSERT INTO public.platform_saga_definition
				(name, version, script, status)
			VALUES ('multi_version_test', '1.0.0', 'script1', 'DEPRECATED')
		`)
		require.NoError(t, err)

		_, err = pool.Exec(ctx, `
			INSERT INTO public.platform_saga_definition
				(name, version, script, status)
			VALUES ('multi_version_test', '2.0.0', 'script2', 'ACTIVE')
		`)
		require.NoError(t, err, "same name with different version should be allowed")
	})

	t.Run("rejects duplicate name and version", func(t *testing.T) {
		_, err := pool.Exec(ctx, `
			INSERT INTO public.platform_saga_definition
				(name, version, script, status)
			VALUES ('dup_name_version', '1.0.0', 'script1', 'ACTIVE')
		`)
		require.NoError(t, err)

		_, err = pool.Exec(ctx, `
			INSERT INTO public.platform_saga_definition
				(name, version, script, status)
			VALUES ('dup_name_version', '1.0.0', 'script2', 'ACTIVE')
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
				(name, version, script, status)
			VALUES ('large_script', '1.0.0', $1, 'ACTIVE')
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
				(name, version, script, status)
			VALUES ('too_large_script', '1.0.0', $1, 'ACTIVE')
		`, string(tooLargeScript))
		require.Error(t, err)
		// CockroachDB uses SQLSTATE 23514 for CHECK violations.
		assert.Contains(t, err.Error(), "23514")
	})
}

func TestPlatformSync_BitemporalTracking(t *testing.T) {
	pool, cleanup := setupPlatformTestDB(t)
	defer cleanup()

	ctx := context.Background()
	sync := NewPlatformSync(pool)

	t.Run("new versions have valid_from set and valid_to NULL", func(t *testing.T) {
		id := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("platform.saga.temporal_test.1.0.0"))
		beforeInsert := time.Now().Add(-1 * time.Second)

		_, err := sync.syncSaga(ctx, PlatformSagaDefinition{
			ID:      id,
			Name:    "temporal_test",
			Version: "1.0.0",
			Script:  "temporal script v1",
		})
		require.NoError(t, err)

		var validFrom time.Time
		var validTo *time.Time
		err = pool.QueryRow(ctx, `
			SELECT valid_from, valid_to FROM public.platform_saga_definition
			WHERE name = 'temporal_test' AND version = '1.0.0'
		`).Scan(&validFrom, &validTo)
		require.NoError(t, err)

		assert.True(t, validFrom.After(beforeInsert), "valid_from should be set to approximately now")
		assert.Nil(t, validTo, "valid_to should be NULL for newly inserted version")
	})

	t.Run("deprecated version gets valid_to set during activation", func(t *testing.T) {
		// Insert v1.0.0 and activate it
		v1ID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("platform.saga.temporal_deprecation.1.0.0"))
		_, err := sync.syncSaga(ctx, PlatformSagaDefinition{
			ID:      v1ID,
			Name:    "temporal_deprecation",
			Version: "1.0.0",
			Script:  "v1 script",
		})
		require.NoError(t, err)
		err = sync.activateLatestVersions(ctx)
		require.NoError(t, err)

		// Verify v1.0.0 is ACTIVE with valid_to NULL
		var v1ValidTo *time.Time
		err = pool.QueryRow(ctx, `
			SELECT valid_to FROM public.platform_saga_definition
			WHERE name = 'temporal_deprecation' AND version = '1.0.0'
		`).Scan(&v1ValidTo)
		require.NoError(t, err)
		assert.Nil(t, v1ValidTo, "active version should have NULL valid_to")

		// Insert v2.0.0 and activate
		v2ID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("platform.saga.temporal_deprecation.2.0.0"))
		_, err = sync.syncSaga(ctx, PlatformSagaDefinition{
			ID:      v2ID,
			Name:    "temporal_deprecation",
			Version: "2.0.0",
			Script:  "v2 script",
		})
		require.NoError(t, err)
		err = sync.activateLatestVersions(ctx)
		require.NoError(t, err)

		// v1.0.0 should now have valid_to set (deprecated)
		err = pool.QueryRow(ctx, `
			SELECT valid_to FROM public.platform_saga_definition
			WHERE name = 'temporal_deprecation' AND version = '1.0.0'
		`).Scan(&v1ValidTo)
		require.NoError(t, err)
		require.NotNil(t, v1ValidTo, "deprecated version should have valid_to set")

		// v2.0.0 should have valid_to NULL (active)
		var v2ValidTo *time.Time
		err = pool.QueryRow(ctx, `
			SELECT valid_to FROM public.platform_saga_definition
			WHERE name = 'temporal_deprecation' AND version = '2.0.0'
		`).Scan(&v2ValidTo)
		require.NoError(t, err)
		assert.Nil(t, v2ValidTo, "active version should have NULL valid_to")
	})

	t.Run("valid_to is preserved for already-deprecated versions", func(t *testing.T) {
		// Insert three versions: v1, v2, v3
		for _, v := range []string{"1.0.0", "2.0.0"} {
			id := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("platform.saga.preserve_valid_to."+v))
			_, err := sync.syncSaga(ctx, PlatformSagaDefinition{
				ID: id, Name: "preserve_valid_to", Version: v,
				Script: "script " + v,
			})
			require.NoError(t, err)
		}

		// Activate: v2.0.0 active, v1.0.0 deprecated
		err := sync.activateLatestVersions(ctx)
		require.NoError(t, err)

		// Record v1.0.0's valid_to timestamp
		var v1ValidToOriginal *time.Time
		err = pool.QueryRow(ctx, `
			SELECT valid_to FROM public.platform_saga_definition
			WHERE name = 'preserve_valid_to' AND version = '1.0.0'
		`).Scan(&v1ValidToOriginal)
		require.NoError(t, err)
		require.NotNil(t, v1ValidToOriginal)

		// Insert v3.0.0 and re-activate
		v3ID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("platform.saga.preserve_valid_to.3.0.0"))
		_, err = sync.syncSaga(ctx, PlatformSagaDefinition{
			ID: v3ID, Name: "preserve_valid_to", Version: "3.0.0",
			Script: "script 3.0.0",
		})
		require.NoError(t, err)
		err = sync.activateLatestVersions(ctx)
		require.NoError(t, err)

		// v1.0.0's valid_to should be unchanged (preserved, not overwritten)
		var v1ValidToAfter *time.Time
		err = pool.QueryRow(ctx, `
			SELECT valid_to FROM public.platform_saga_definition
			WHERE name = 'preserve_valid_to' AND version = '1.0.0'
		`).Scan(&v1ValidToAfter)
		require.NoError(t, err)
		require.NotNil(t, v1ValidToAfter)
		assert.True(t, v1ValidToOriginal.Equal(*v1ValidToAfter),
			"v1.0.0 valid_to should be preserved, not overwritten on re-activation")
	})

	t.Run("validity range constraint rejects valid_to before valid_from", func(t *testing.T) {
		past := time.Now().Add(-24 * time.Hour)
		future := time.Now().Add(24 * time.Hour)

		// valid_to < valid_from should fail the CHECK constraint
		_, err := pool.Exec(ctx, `
			INSERT INTO public.platform_saga_definition
				(name, version, script, status, valid_from, valid_to)
			VALUES ('constraint_test', '1.0.0', 'script', 'ACTIVE', $1, $2)
		`, future, past)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "23514", "should violate CHECK constraint")
	})

	t.Run("validity range constraint allows valid_to after valid_from", func(t *testing.T) {
		past := time.Now().Add(-24 * time.Hour)
		future := time.Now().Add(24 * time.Hour)

		_, err := pool.Exec(ctx, `
			INSERT INTO public.platform_saga_definition
				(name, version, script, status, valid_from, valid_to)
			VALUES ('constraint_ok', '1.0.0', 'script', 'ACTIVE', $1, $2)
		`, past, future)
		require.NoError(t, err)
	})

	t.Run("validity range constraint allows NULL valid_to", func(t *testing.T) {
		_, err := pool.Exec(ctx, `
			INSERT INTO public.platform_saga_definition
				(name, version, script, status, valid_from, valid_to)
			VALUES ('constraint_null', '1.0.0', 'script', 'ACTIVE', now(), NULL)
		`)
		require.NoError(t, err)
	})

	t.Run("validity range constraint rejects equal valid_from and valid_to", func(t *testing.T) {
		now := time.Now()
		_, err := pool.Exec(ctx, `
			INSERT INTO public.platform_saga_definition
				(name, version, script, status, valid_from, valid_to)
			VALUES ('constraint_equal', '1.0.0', 'script', 'ACTIVE', $1, $2)
		`, now, now)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "23514", "equal valid_from/valid_to should violate CHECK (strict >)")
	})
}

func TestGetPlatformSagaAtTime(t *testing.T) {
	pool, cleanup := setupPlatformTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create a registry for the GetPlatformSagaAtTime method
	registry := NewPostgresRegistry(pool, nil)

	// Set up timeline:
	// t0: v1.0.0 inserted (valid_from=t0, valid_to=t1)
	// t1: v2.0.0 inserted (valid_from=t1, valid_to=NULL) -- currently active
	t0 := time.Now().Add(-2 * time.Hour)
	t1 := time.Now().Add(-1 * time.Hour)

	// Insert v1.0.0: was active from t0 to t1
	v1ID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("platform.saga.at_time_test.1.0.0"))
	_, err := pool.Exec(ctx, `
		INSERT INTO public.platform_saga_definition
			(id, name, version, script, status, valid_from, valid_to, display_name, description)
		VALUES ($1, 'at_time_test', '1.0.0', 'v1 script content', 'DEPRECATED', $2, $3, 'At Time Test', 'Version 1')
	`, v1ID, t0, t1)
	require.NoError(t, err)

	// Insert v2.0.0: active from t1 onwards
	v2ID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("platform.saga.at_time_test.2.0.0"))
	_, err = pool.Exec(ctx, `
		INSERT INTO public.platform_saga_definition
			(id, name, version, script, status, valid_from, valid_to, display_name, description)
		VALUES ($1, 'at_time_test', '2.0.0', 'v2 script content', 'ACTIVE', $2, NULL, 'At Time Test', 'Version 2')
	`, v2ID, t1)
	require.NoError(t, err)

	t.Run("resolves v1 when querying between t0 and t1", func(t *testing.T) {
		queryTime := t0.Add(30 * time.Minute) // midpoint between t0 and t1
		result, err := registry.GetPlatformSagaAtTime(ctx, "at_time_test", queryTime)
		require.NoError(t, err)
		assert.Equal(t, v1ID, result.ID)
		assert.Equal(t, "1.0.0", result.Version)
		assert.Equal(t, "v1 script content", result.Script)
		assert.Equal(t, "At Time Test", result.DisplayName)
	})

	t.Run("resolves v2 when querying after t1", func(t *testing.T) {
		queryTime := t1.Add(30 * time.Minute)
		result, err := registry.GetPlatformSagaAtTime(ctx, "at_time_test", queryTime)
		require.NoError(t, err)
		assert.Equal(t, v2ID, result.ID)
		assert.Equal(t, "2.0.0", result.Version)
		assert.Equal(t, "v2 script content", result.Script)
	})

	t.Run("resolves v1 exactly at valid_from boundary", func(t *testing.T) {
		result, err := registry.GetPlatformSagaAtTime(ctx, "at_time_test", t0)
		require.NoError(t, err)
		assert.Equal(t, v1ID, result.ID, "exactly at valid_from should match (using <=)")
	})

	t.Run("resolves v2 exactly at t1 boundary", func(t *testing.T) {
		// At t1: v1.0.0 has valid_to=t1 (exclusive), v2.0.0 has valid_from=t1 (inclusive)
		result, err := registry.GetPlatformSagaAtTime(ctx, "at_time_test", t1)
		require.NoError(t, err)
		assert.Equal(t, v2ID, result.ID,
			"at the transition boundary, the new version should be resolved (v1.valid_to=t1 is exclusive, v2.valid_from=t1 is inclusive)")
	})

	t.Run("returns not found before first version", func(t *testing.T) {
		queryTime := t0.Add(-1 * time.Hour) // before v1.0.0 existed
		_, err := registry.GetPlatformSagaAtTime(ctx, "at_time_test", queryTime)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrPlatformDefinitionNotFound)
	})

	t.Run("returns not found for non-existent saga name", func(t *testing.T) {
		_, err := registry.GetPlatformSagaAtTime(ctx, "nonexistent_saga", time.Now())
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrPlatformDefinitionNotFound)
	})

	t.Run("resolves current time for currently active version", func(t *testing.T) {
		result, err := registry.GetPlatformSagaAtTime(ctx, "at_time_test", time.Now())
		require.NoError(t, err)
		assert.Equal(t, v2ID, result.ID, "current time should resolve to the active version with NULL valid_to")
		assert.Nil(t, result.ValidTo, "active version should have nil ValidTo")
		assert.False(t, result.ValidFrom.IsZero(), "ValidFrom should be populated")
	})

	t.Run("populates temporal fields on returned struct", func(t *testing.T) {
		queryTime := t0.Add(30 * time.Minute)
		result, err := registry.GetPlatformSagaAtTime(ctx, "at_time_test", queryTime)
		require.NoError(t, err)

		assert.False(t, result.ValidFrom.IsZero(), "ValidFrom should be populated")
		require.NotNil(t, result.ValidTo, "ValidTo should be populated for deprecated version")
		assert.True(t, result.ValidFrom.Before(*result.ValidTo),
			"ValidFrom should be before ValidTo")
	})
}

func TestGetPlatformSagaByID_IncludesTemporalFields(t *testing.T) {
	pool, cleanup := setupPlatformTestDB(t)
	defer cleanup()

	ctx := context.Background()
	registry := NewPostgresRegistry(pool, nil)

	past := time.Now().Add(-1 * time.Hour)
	id := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("platform.saga.byid_temporal.1.0.0"))
	_, err := pool.Exec(ctx, `
		INSERT INTO public.platform_saga_definition
			(id, name, version, script, status, valid_from, valid_to)
		VALUES ($1, 'byid_temporal', '1.0.0', 'script', 'DEPRECATED', $2, $3)
	`, id, past, time.Now())
	require.NoError(t, err)

	result, err := registry.GetPlatformSagaByID(ctx, id)
	require.NoError(t, err)
	assert.False(t, result.ValidFrom.IsZero(), "GetPlatformSagaByID should populate ValidFrom")
	assert.NotNil(t, result.ValidTo, "GetPlatformSagaByID should populate ValidTo for deprecated versions")
}

func TestGetPlatformSagaByName_IncludesTemporalFields(t *testing.T) {
	pool, cleanup := setupPlatformTestDB(t)
	defer cleanup()

	ctx := context.Background()
	registry := NewPostgresRegistry(pool, nil)

	id := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("platform.saga.byname_temporal.1.0.0"))
	_, err := pool.Exec(ctx, `
		INSERT INTO public.platform_saga_definition
			(id, name, version, script, status, valid_from, valid_to)
		VALUES ($1, 'byname_temporal', '1.0.0', 'script', 'ACTIVE', now(), NULL)
	`, id)
	require.NoError(t, err)

	result, err := registry.GetPlatformSagaByName(ctx, "byname_temporal")
	require.NoError(t, err)
	assert.False(t, result.ValidFrom.IsZero(), "GetPlatformSagaByName should populate ValidFrom")
	assert.Nil(t, result.ValidTo, "active version should have nil ValidTo")
}

func TestPlatformSync_EmbeddedMetadataHeaders(t *testing.T) {
	scripts, err := GetEmbeddedScripts()
	require.NoError(t, err)

	// Check versioned files have proper metadata headers
	versionedKeys := []string{
		"deposit/v1.0.0.star",
		"withdrawal/v1.0.0.star",
		"payment_execution/v1.0.0.star",
		"stripe_payment/v1.0.0.star",
	}

	for _, key := range versionedKeys {
		t.Run(key, func(t *testing.T) {
			script, ok := scripts[key]
			require.True(t, ok, "script %s should exist", key)

			meta := parseMetadataHeader(script)
			assert.NotEmpty(t, meta.SagaName, "should have Saga: header")
			assert.NotEmpty(t, meta.Version, "should have Version: header")
			assert.NotEmpty(t, meta.ChangeSummary, "should have Changed: header")
			assert.NotEmpty(t, meta.Author, "should have Author: header")
			assert.NotEmpty(t, meta.Date, "should have Date: header")
		})
	}
}
