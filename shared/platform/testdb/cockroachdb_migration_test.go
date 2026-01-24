package testdb_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/require"
)

// TestAtlasMigrations_CockroachDBCompatibility validates all service migrations
// against CockroachDB to catch compatibility issues early.
//
// Known CockroachDB limitations this test catches:
//   - ALTER COLUMN TYPE in transactions (error: cannot modify column type within a transaction)
//   - PL/pgSQL triggers (limited support, may fail on complex triggers)
//   - Range types like TSTZRANGE, DATERANGE (not supported, see cockroachdb/cockroach#27791)
//   - Some window functions or CTEs may behave differently
//
// When migrations fail:
//  1. Check the error message for CockroachDB-specific limitations
//  2. Consult docs/adr/0003-database-schema-migrations.md for compatibility guidelines
//  3. Consider using explicit ADD/DROP COLUMN instead of ALTER COLUMN TYPE
//  4. For triggers, keep logic simple or move to application layer
func TestAtlasMigrations_CockroachDBCompatibility(t *testing.T) {
	// SKIP: Atlas CLI v0.35+ no longer supports postgres:// scheme for CockroachDB.
	// It now requires crdb:// scheme which needs Atlas Cloud login (paid feature).
	// See: https://atlasgo.io/guides/drivers/cockroachdb
	// TODO: Either pin Atlas version < 0.35 or configure Atlas Cloud credentials in CI.
	t.Skip("CockroachDB migration test disabled: Atlas CLI requires crdb:// scheme with Atlas Cloud login")

	if testing.Short() {
		t.Skip("Skipping CockroachDB migration test in short mode")
	}

	// Check if atlas CLI is available
	if _, err := exec.LookPath("atlas"); err != nil {
		t.Skip("Atlas CLI not found, skipping migration test")
	}

	// Get project root (shared/platform/testdb -> project root)
	projectRoot := findProjectRoot(t)

	// Services with Atlas migrations
	services := []string{
		"current-account",
		"financial-accounting",
		"internal-bank-account",
		"market-information",
		"party",
		"payment-order",
		"position-keeping",
	}

	for _, service := range services {
		service := service
		t.Run(service, func(t *testing.T) {
			t.Parallel()

			// Setup CockroachDB container with connection info for Atlas CLI
			info, cleanup := testdb.SetupCockroachDBWithInfo(t)
			defer cleanup()

			// Construct paths
			configPath := filepath.Join(projectRoot, "services", service, "atlas", "atlas.hcl")
			migrationsDir := filepath.Join(projectRoot, "services", service, "migrations")

			// Verify migration directory exists
			if _, err := os.Stat(migrationsDir); os.IsNotExist(err) {
				t.Skipf("No migrations directory for %s", service)
			}

			// Verify atlas config exists
			if _, err := os.Stat(configPath); os.IsNotExist(err) {
				t.Skipf("No atlas config for %s", service)
			}

			// Run atlas migrate apply with CockroachDB
			// Use --tx-mode none because CockroachDB has limitations with transactional DDL
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()

			applyCmd := exec.CommandContext(ctx, "atlas", "migrate", "apply",
				"--dir", "file://"+migrationsDir,
				"--url", info.ConnStr,
				"--tx-mode", "none",
				"--allow-dirty",
			)
			applyCmd.Dir = projectRoot

			applyOut, err := applyCmd.CombinedOutput()
			if err != nil {
				t.Logf("atlas migrate apply output:\n%s", applyOut)

				// Provide helpful error messages for common CockroachDB issues
				outputStr := string(applyOut)
				if strings.Contains(outputStr, "cannot modify column type") {
					t.Errorf("CockroachDB compatibility issue in %s: ALTER COLUMN TYPE not supported in transactions. Use explicit ADD/DROP COLUMN pattern instead.", service)
				} else if strings.Contains(outputStr, "unknown function") && strings.Contains(outputStr, "tstzrange") {
					t.Errorf("CockroachDB compatibility issue in %s: Range types (TSTZRANGE) not supported. Use explicit start_time/end_time columns.", service)
				} else if strings.Contains(outputStr, "syntax error") && strings.Contains(outputStr, "LANGUAGE plpgsql") {
					t.Errorf("CockroachDB compatibility issue in %s: Complex PL/pgSQL trigger not supported. Simplify trigger or use application logic.", service)
				} else {
					t.Errorf("atlas migrate apply failed for %s: %v", service, err)
				}
				return
			}

			t.Logf("✓ %s migrations compatible with CockroachDB", service)
		})
	}
}

// TestSetupCockroachDB verifies the CockroachDB testcontainer setup works correctly.
func TestSetupCockroachDB(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping CockroachDB container test in short mode")
	}

	db, cleanup := testdb.SetupCockroachDB(t, nil)
	defer cleanup()

	// Verify connection works
	var version string
	err := db.Raw("SELECT version()").Scan(&version).Error
	require.NoError(t, err)
	require.Contains(t, version, "CockroachDB")

	t.Logf("Connected to: %s", version)
}

// TestSetupCockroachDB_SchemaCreation verifies schema and table creation.
func TestSetupCockroachDB_SchemaCreation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping CockroachDB schema test in short mode")
	}

	db, cleanup := testdb.SetupCockroachDB(t, nil)
	defer cleanup()

	// Create test schema
	err := db.Exec("CREATE SCHEMA IF NOT EXISTS test_schema").Error
	require.NoError(t, err)

	// Create test table
	err = db.Exec("CREATE TABLE test_schema.test_table (id INT PRIMARY KEY, name STRING)").Error
	require.NoError(t, err)

	// Verify table exists
	var count int
	err = db.Raw("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'test_schema' AND table_name = 'test_table'").Scan(&count).Error
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

// TestSetupCockroachDBWithInfo verifies the connection info helper.
func TestSetupCockroachDBWithInfo(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping CockroachDB info test in short mode")
	}

	info, cleanup := testdb.SetupCockroachDBWithInfo(t)
	defer cleanup()

	require.NotEmpty(t, info.Host)
	require.NotEmpty(t, info.Port)
	require.Equal(t, "test_db", info.Database)
	require.Equal(t, "root", info.User)
	require.NotEmpty(t, info.ConnStr)
	require.Contains(t, info.ConnStr, "test_db")
}

// findProjectRoot walks up from current directory to find go.mod
func findProjectRoot(t *testing.T) string {
	t.Helper()

	// Start from test file location
	dir, err := os.Getwd()
	require.NoError(t, err)

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("Could not find project root (no go.mod found)")
		}
		dir = parent
	}
}
