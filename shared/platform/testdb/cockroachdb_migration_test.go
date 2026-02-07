package testdb_test

import (
	"testing"

	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/require"
)

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
