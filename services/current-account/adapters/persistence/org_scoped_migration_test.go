package persistence

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/cockroachdb"
	gormpg "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestOrgScopedMigrations_CockroachDB validates that org-scoped account migrations
// run correctly on CockroachDB and that all indexes and constraints work as expected.
func TestOrgScopedMigrations_CockroachDB(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping CockroachDB migration test in short mode")
	}

	// Start CockroachDB container directly so we can get the connection string
	// for both raw SQL (migrations) and GORM (entity operations)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	crdbContainer, err := cockroachdb.Run(ctx,
		"cockroachdb/cockroach:v24.3.0",
		cockroachdb.WithDatabase("test_db"),
		cockroachdb.WithUser("root"),
		cockroachdb.WithInsecure(),
	)
	require.NoError(t, err, "Failed to start CockroachDB container")
	defer func() {
		_ = crdbContainer.Terminate(context.Background())
	}()

	connConfig, err := crdbContainer.ConnectionConfig(ctx)
	require.NoError(t, err)
	connStr := connConfig.ConnString()

	// Open raw SQL connection for migrations
	sqlDB, err := sql.Open("pgx", connStr)
	require.NoError(t, err)
	defer sqlDB.Close()

	// Apply all current-account migrations in order
	applyCurrentAccountMigrations(t, sqlDB)

	// Open GORM connection for entity operations
	gormDB, err := gorm.Open(gormpg.Open(connStr), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)

	t.Run("MigrationsAppliedSuccessfully", func(t *testing.T) {
		// Verify org_party_id column exists
		var count int64
		err := gormDB.Raw(`
			SELECT COUNT(*) FROM information_schema.columns
			WHERE table_name = 'account' AND column_name = 'org_party_id'
		`).Scan(&count).Error
		require.NoError(t, err)
		assert.Equal(t, int64(1), count, "org_party_id column should exist")
	})

	t.Run("IndexesCreated", func(t *testing.T) {
		for _, indexName := range []string{
			"idx_account_participant_syndicate",
			"idx_account_syndicate_participants",
		} {
			var count int64
			err := gormDB.Raw(`
				SELECT COUNT(*) FROM pg_indexes
				WHERE tablename = 'account' AND indexname = ?
			`, indexName).Scan(&count).Error
			require.NoError(t, err)
			assert.Equal(t, int64(1), count, "Index %s should exist", indexName)
		}
	})

	t.Run("ScopeIntegrityIndexDropped", func(t *testing.T) {
		// Migration 20260416000001 dropped idx_account_syndicate_scope_integrity to allow
		// multiple accounts per (party_id, org_party_id, instrument_code) — required by
		// utility billing patterns (e.g. separate GBP accounts for electricity and gas
		// from the same supplier).
		var count int64
		err := gormDB.Raw(`
			SELECT COUNT(*) FROM pg_indexes
			WHERE tablename = 'account' AND indexname = 'idx_account_syndicate_scope_integrity'
		`).Scan(&count).Error
		require.NoError(t, err)
		assert.Equal(t, int64(0), count, "idx_account_syndicate_scope_integrity should have been dropped")
	})

	t.Run("CanCreateOrgScopedAccount", func(t *testing.T) {
		partyID := uuid.New()
		orgPartyID := uuid.New()
		now := time.Now()

		entity := &CurrentAccountEntity{
			ID:                    uuid.New(),
			AccountID:             "ACC-ORG-001",
			AccountIdentification: "GB82WEST11111111111111",
			AccountType:           "current",
			InstrumentCode:        "GBP",
			Dimension:             "CURRENCY",
			Status:                "active",
			PartyID:               partyID,
			OrgPartyID:            &orgPartyID,
			OverdraftLimit:        0,
			CreatedAt:             now,
			UpdatedAt:             now,
			CreatedBy:             "system",
			UpdatedBy:             "system",
		}

		err := gormDB.Create(entity).Error
		require.NoError(t, err, "Should create org-scoped account")

		var retrieved CurrentAccountEntity
		err = gormDB.First(&retrieved, "id = ?", entity.ID).Error
		require.NoError(t, err)
		require.NotNil(t, retrieved.OrgPartyID)
		assert.Equal(t, orgPartyID, *retrieved.OrgPartyID)
	})

	t.Run("PersonalAccountHasNullOrgPartyID", func(t *testing.T) {
		now := time.Now()
		entity := &CurrentAccountEntity{
			ID:                    uuid.New(),
			AccountID:             "ACC-PERSONAL-001",
			AccountIdentification: "GB82WEST22222222222222",
			AccountType:           "current",
			InstrumentCode:        "GBP",
			Dimension:             "CURRENCY",
			Status:                "active",
			PartyID:               uuid.New(),
			OrgPartyID:            nil,
			OverdraftLimit:        0,
			CreatedAt:             now,
			UpdatedAt:             now,
			CreatedBy:             "system",
			UpdatedBy:             "system",
		}

		err := gormDB.Create(entity).Error
		require.NoError(t, err, "Should create personal account with NULL org_party_id")

		var retrieved CurrentAccountEntity
		err = gormDB.First(&retrieved, "id = ?", entity.ID).Error
		require.NoError(t, err)
		assert.Nil(t, retrieved.OrgPartyID, "Personal account should have NULL org_party_id")
	})

	t.Run("MultipleAccountsPerPartyOrgInstrumentAllowed", func(t *testing.T) {
		// Utility billing case: a customer has separate GBP billing accounts for electricity
		// and gas with the same supplier. After migration 20260416000001, both succeed and
		// uniqueness is preserved only by account_identification.
		partyID := uuid.New()
		orgPartyID := uuid.New()
		now := time.Now()

		entity1 := &CurrentAccountEntity{
			ID:                    uuid.New(),
			AccountID:             "ACC-UNIQ-001",
			AccountIdentification: "PPM-ELEC-DEMO-001",
			AccountType:           "current",
			InstrumentCode:        "GBP",
			Dimension:             "CURRENCY",
			Status:                "active",
			PartyID:               partyID,
			OrgPartyID:            &orgPartyID,
			OverdraftLimit:        0,
			CreatedAt:             now,
			UpdatedAt:             now,
			CreatedBy:             "system",
			UpdatedBy:             "system",
		}
		err := gormDB.Create(entity1).Error
		require.NoError(t, err, "First org-scoped GBP account should succeed")

		entity2 := &CurrentAccountEntity{
			ID:                    uuid.New(),
			AccountID:             "ACC-UNIQ-002",
			AccountIdentification: "PPM-GAS-DEMO-001",
			AccountType:           "current",
			InstrumentCode:        "GBP",
			Dimension:             "CURRENCY",
			Status:                "active",
			PartyID:               partyID,
			OrgPartyID:            &orgPartyID,
			OverdraftLimit:        0,
			CreatedAt:             now,
			UpdatedAt:             now,
			CreatedBy:             "system",
			UpdatedBy:             "system",
		}
		err = gormDB.Create(entity2).Error
		assert.NoError(t, err, "Second GBP account for same (party, org) should succeed (multi-service billing)")

		entity3 := &CurrentAccountEntity{
			ID:                    uuid.New(),
			AccountID:             "ACC-UNIQ-003",
			AccountIdentification: "PPM-EUR-DEMO-001",
			AccountType:           "current",
			InstrumentCode:        "EUR",
			Dimension:             "CURRENCY",
			Status:                "active",
			PartyID:               partyID,
			OrgPartyID:            &orgPartyID,
			OverdraftLimit:        0,
			CreatedAt:             now,
			UpdatedAt:             now,
			CreatedBy:             "system",
			UpdatedBy:             "system",
		}
		err = gormDB.Create(entity3).Error
		assert.NoError(t, err, "Different instrument_code for same (party_id, org_party_id) should succeed")

		// Account-identifier uniqueness is still enforced by idx_account_account_identification.
		dup := &CurrentAccountEntity{
			ID:                    uuid.New(),
			AccountID:             "ACC-UNIQ-004",
			AccountIdentification: "PPM-ELEC-DEMO-001",
			AccountType:           "current",
			InstrumentCode:        "GBP",
			Dimension:             "CURRENCY",
			Status:                "active",
			PartyID:               partyID,
			OrgPartyID:            &orgPartyID,
			OverdraftLimit:        0,
			CreatedAt:             now,
			UpdatedAt:             now,
			CreatedBy:             "system",
			UpdatedBy:             "system",
		}
		err = gormDB.Create(dup).Error
		assert.Error(t, err, "Duplicate account_identification must still be rejected")
	})

	t.Run("UniqueConstraintDoesNotAffectPersonalAccounts", func(t *testing.T) {
		partyID := uuid.New()
		now := time.Now()

		// Two personal accounts (NULL org_party_id) with same party and instrument_code
		// should succeed because the partial unique index only applies WHERE org_party_id IS NOT NULL
		entity1 := &CurrentAccountEntity{
			ID:                    uuid.New(),
			AccountID:             "ACC-PERS-DUP-001",
			AccountIdentification: "GB82WEST66666666666666",
			AccountType:           "current",
			InstrumentCode:        "GBP",
			Dimension:             "CURRENCY",
			Status:                "active",
			PartyID:               partyID,
			OrgPartyID:            nil,
			OverdraftLimit:        0,
			CreatedAt:             now,
			UpdatedAt:             now,
			CreatedBy:             "system",
			UpdatedBy:             "system",
		}
		err := gormDB.Create(entity1).Error
		require.NoError(t, err)

		entity2 := &CurrentAccountEntity{
			ID:                    uuid.New(),
			AccountID:             "ACC-PERS-DUP-002",
			AccountIdentification: "GB82WEST77777777777777",
			AccountType:           "savings",
			InstrumentCode:        "GBP",
			Dimension:             "CURRENCY",
			Status:                "active",
			PartyID:               partyID,
			OrgPartyID:            nil,
			OverdraftLimit:        0,
			CreatedAt:             now,
			UpdatedAt:             now,
			CreatedBy:             "system",
			UpdatedBy:             "system",
		}
		err = gormDB.Create(entity2).Error
		assert.NoError(t, err, "Multiple personal accounts with same party+instrument_code should be allowed")
	})
}

// applyCurrentAccountMigrations runs all SQL migration files for the current-account service.
func applyCurrentAccountMigrations(t *testing.T, db *sql.DB) {
	t.Helper()

	// Find project root by traversing up for go.mod
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, dir, parent, "Could not find project root (no go.mod)")
		dir = parent
	}

	migrationDir := filepath.Join(dir, "services", "current-account", "migrations")
	entries, err := os.ReadDir(migrationDir)
	require.NoError(t, err, "Failed to read migration directory")

	var migrations []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			migrations = append(migrations, filepath.Join(migrationDir, entry.Name()))
		}
	}
	sort.Strings(migrations)

	for _, path := range migrations {
		content, err := os.ReadFile(path)
		require.NoError(t, err, "Failed to read migration: %s", path)

		_, err = db.ExecContext(context.Background(), string(content))
		require.NoError(t, err, "Failed to apply migration %s", filepath.Base(path))

		t.Logf("Applied migration: %s", filepath.Base(path))
	}
}
