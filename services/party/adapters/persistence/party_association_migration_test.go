package persistence

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupPartyAssociationTestDB creates a PostgreSQL testcontainer and applies
// all party service migrations in order. This tests the actual migration SQL
// rather than relying on GORM AutoMigrate.
func setupPartyAssociationTestDB(t *testing.T) (*gorm.DB, func()) {
	t.Helper()

	db, cleanup := testdb.SetupPostgres(t, nil)

	// Find and apply all party service migrations in order
	var migrationsDir string
	possiblePaths := []string{
		filepath.Join("services", "party", "migrations"),
		filepath.Join("..", "..", "migrations"),
		filepath.Join("..", "..", "services", "party", "migrations"),
	}
	for _, path := range possiblePaths {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			migrationsDir = path
			break
		}
	}
	require.NotEmpty(t, migrationsDir, "Could not find party migrations directory")

	entries, err := os.ReadDir(migrationsDir)
	require.NoError(t, err)

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		content, err := os.ReadFile(filepath.Join(migrationsDir, entry.Name()))
		require.NoError(t, err, "Failed to read migration %s", entry.Name())

		err = db.Exec(string(content)).Error
		require.NoError(t, err, "Failed to apply migration %s", entry.Name())
	}

	return db, cleanup
}

func TestPartyAssociationMigrations_ApplySuccessfully(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupPartyAssociationTestDB(t)
	defer cleanup()

	// Verify new columns exist by querying information_schema
	var columns []struct {
		ColumnName string
		DataType   string
	}
	err := db.Raw(`
		SELECT column_name, data_type
		FROM information_schema.columns
		WHERE table_name = 'party_association'
		AND column_name IN ('metadata', 'status', 'effective_from', 'effective_to')
		ORDER BY column_name
	`).Scan(&columns).Error
	require.NoError(t, err)
	require.Len(t, columns, 4, "Expected 4 new columns on party_association")

	columnNames := make([]string, len(columns))
	for i, c := range columns {
		columnNames[i] = c.ColumnName
	}
	assert.Contains(t, columnNames, "metadata")
	assert.Contains(t, columnNames, "status")
	assert.Contains(t, columnNames, "effective_from")
	assert.Contains(t, columnNames, "effective_to")
}

func TestPartyAssociationMigrations_InsertWithValidMetadata(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupPartyAssociationTestDB(t)
	defer cleanup()

	partyID1 := createPartyForAssociationTest(t, db, "Party A")
	partyID2 := createPartyForAssociationTest(t, db, "Party B")

	err := db.Exec(`
		INSERT INTO party_association (party_id, related_party_id, relationship_type, metadata, status, effective_from)
		VALUES ($1, $2, 'SYNDICATE_MEMBER', '{"allocation_share": 0.25, "role": "lead_arranger"}'::jsonb, 'ACTIVE', now())
	`, partyID1, partyID2).Error
	require.NoError(t, err)

	var result struct {
		Metadata      string
		Status        string
		EffectiveFrom time.Time
	}
	err = db.Raw(`
		SELECT metadata::text, status, effective_from
		FROM party_association
		WHERE party_id = $1 AND related_party_id = $2
	`, partyID1, partyID2).Scan(&result).Error
	require.NoError(t, err)
	assert.Equal(t, "ACTIVE", result.Status)
	assert.Contains(t, result.Metadata, "allocation_share")
	assert.False(t, result.EffectiveFrom.IsZero())
}

func TestPartyAssociationMigrations_InvalidStatusFailsConstraint(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupPartyAssociationTestDB(t)
	defer cleanup()

	partyID1 := createPartyForAssociationTest(t, db, "Party C")
	partyID2 := createPartyForAssociationTest(t, db, "Party D")

	err := db.Exec(`
		INSERT INTO party_association (party_id, related_party_id, relationship_type, status)
		VALUES ($1, $2, 'PARTNER', 'INVALID_STATUS')
	`, partyID1, partyID2).Error
	require.Error(t, err, "Expected constraint violation for invalid status")
	assert.Contains(t, err.Error(), "chk_party_association_status")
}

func TestPartyAssociationMigrations_EffectiveToBeforeEffectiveFromFailsConstraint(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupPartyAssociationTestDB(t)
	defer cleanup()

	partyID1 := createPartyForAssociationTest(t, db, "Party E")
	partyID2 := createPartyForAssociationTest(t, db, "Party F")

	err := db.Exec(`
		INSERT INTO party_association (party_id, related_party_id, relationship_type, effective_from, effective_to)
		VALUES ($1, $2, 'SUBSIDIARY', '2026-06-01T00:00:00Z', '2026-01-01T00:00:00Z')
	`, partyID1, partyID2).Error
	require.Error(t, err, "Expected constraint violation for effective_to < effective_from")
	assert.Contains(t, err.Error(), "chk_party_association_validity_range")
}

func TestPartyAssociationMigrations_JSONBQueryOnMetadata(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupPartyAssociationTestDB(t)
	defer cleanup()

	partyID1 := createPartyForAssociationTest(t, db, "Party G")
	partyID2 := createPartyForAssociationTest(t, db, "Party H")
	partyID3 := createPartyForAssociationTest(t, db, "Party I")

	err := db.Exec(`
		INSERT INTO party_association (party_id, related_party_id, relationship_type, metadata)
		VALUES ($1, $2, 'SYNDICATE_MEMBER', '{"allocation_share": 0.75, "role": "lead_arranger"}'::jsonb)
	`, partyID1, partyID2).Error
	require.NoError(t, err)

	err = db.Exec(`
		INSERT INTO party_association (party_id, related_party_id, relationship_type, metadata)
		VALUES ($1, $2, 'SYNDICATE_MEMBER', '{"allocation_share": 0.25, "role": "participant"}'::jsonb)
	`, partyID1, partyID3).Error
	require.NoError(t, err)

	// Query using JSONB accessor operator
	var count int64
	err = db.Raw(`
		SELECT COUNT(*)
		FROM party_association
		WHERE metadata->>'role' = 'lead_arranger'
	`).Scan(&count).Error
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	// Query using JSONB containment operator
	err = db.Raw(`
		SELECT COUNT(*)
		FROM party_association
		WHERE metadata @> '{"role": "participant"}'::jsonb
	`).Scan(&count).Error
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

func TestPartyAssociationMigrations_ValidStatusTransitions(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupPartyAssociationTestDB(t)
	defer cleanup()

	partyID1 := createPartyForAssociationTest(t, db, "Party J")
	partyID2 := createPartyForAssociationTest(t, db, "Party K")

	// Insert with ACTIVE status (default)
	err := db.Exec(`
		INSERT INTO party_association (party_id, related_party_id, relationship_type)
		VALUES ($1, $2, 'PARTNER')
	`, partyID1, partyID2).Error
	require.NoError(t, err)

	// Update to SUSPENDED
	err = db.Exec(`
		UPDATE party_association
		SET status = 'SUSPENDED'
		WHERE party_id = $1 AND related_party_id = $2
	`, partyID1, partyID2).Error
	require.NoError(t, err)

	// Update to TERMINATED with effective_to
	err = db.Exec(`
		UPDATE party_association
		SET status = 'TERMINATED', effective_to = now()
		WHERE party_id = $1 AND related_party_id = $2
	`, partyID1, partyID2).Error
	require.NoError(t, err)

	var result struct {
		Status      string
		EffectiveTo *time.Time
	}
	err = db.Raw(`
		SELECT status, effective_to
		FROM party_association
		WHERE party_id = $1 AND related_party_id = $2
	`, partyID1, partyID2).Scan(&result).Error
	require.NoError(t, err)
	assert.Equal(t, "TERMINATED", result.Status)
	assert.NotNil(t, result.EffectiveTo)
}

// createPartyForAssociationTest inserts a minimal party record and returns its UUID as a string.
func createPartyForAssociationTest(t *testing.T, db *gorm.DB, legalName string) string {
	t.Helper()
	var partyID string
	err := db.Raw(`
		INSERT INTO party (party_type, legal_name, status, created_by, updated_by)
		VALUES ('ORGANIZATION', $1, 'ACTIVE', 'test', 'test')
		RETURNING id::text
	`, legalName).Scan(&partyID).Error
	require.NoError(t, err)
	return partyID
}
