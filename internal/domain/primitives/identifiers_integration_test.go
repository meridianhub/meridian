//go:build integration

package primitives

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/internal/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestEntity is a test model that uses our ID types for persistence testing.
type TestEntity struct {
	ID         uuid.UUID  `gorm:"type:uuid;primaryKey"`
	AccountID  AccountID  `gorm:"type:uuid;not null"`
	CustomerID CustomerID `gorm:"type:uuid;not null"`
	LedgerID   LedgerID   `gorm:"type:uuid"`
	Name       string     `gorm:"type:varchar(100)"`
}

// TableName returns the table name for the test entity.
func (TestEntity) TableName() string {
	return "test_identifiers"
}

func setupIntegrationTestDB(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	return testdb.SetupPostgres(t, []interface{}{&TestEntity{}})
}

func TestIdentifiers_PostgresUUIDColumn_Integration(t *testing.T) {
	db, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	t.Run("insert and retrieve entity with ID types", func(t *testing.T) {
		entity := TestEntity{
			ID:         uuid.New(),
			AccountID:  AccountID("550e8400-e29b-41d4-a716-446655440000"),
			CustomerID: CustomerID("f47ac10b-58cc-4372-a567-0e02b2c3d479"),
			LedgerID:   LedgerID("6ba7b810-9dad-41d1-80b4-00c04fd430c8"),
			Name:       "Test Entity",
		}

		// Insert
		err := db.Create(&entity).Error
		require.NoError(t, err, "Failed to insert entity with ID types")

		// Retrieve
		var retrieved TestEntity
		err = db.First(&retrieved, "id = ?", entity.ID).Error
		require.NoError(t, err, "Failed to retrieve entity")

		// Verify ID types are preserved
		assert.Equal(t, entity.AccountID, retrieved.AccountID)
		assert.Equal(t, entity.CustomerID, retrieved.CustomerID)
		assert.Equal(t, entity.LedgerID, retrieved.LedgerID)
		assert.Equal(t, entity.Name, retrieved.Name)
	})

	t.Run("query by ID type", func(t *testing.T) {
		// Use unique IDs for this test to avoid collision with other subtests
		uniqueAccountID := AccountID("aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa")
		uniqueCustomerID := CustomerID("bbbbbbbb-bbbb-4bbb-bbbb-bbbbbbbbbbbb")

		entity := TestEntity{
			ID:         uuid.New(),
			AccountID:  uniqueAccountID,
			CustomerID: uniqueCustomerID,
			LedgerID:   LedgerID("cccccccc-cccc-4ccc-accc-cccccccccccc"),
			Name:       "Query Test Entity",
		}

		err := db.Create(&entity).Error
		require.NoError(t, err)

		// Query using AccountID - use the entity's primary key for precise matching
		var found TestEntity
		err = db.Where("id = ?", entity.ID).First(&found).Error
		require.NoError(t, err, "Failed to query by primary key")
		assert.Equal(t, entity.Name, found.Name)
		assert.Equal(t, uniqueAccountID, found.AccountID)

		// Query using unique AccountID
		var foundByAccount TestEntity
		err = db.Where("account_id = ?", uniqueAccountID).First(&foundByAccount).Error
		require.NoError(t, err, "Failed to query by AccountID")
		assert.Equal(t, entity.Name, foundByAccount.Name)
	})

	t.Run("update entity with ID types", func(t *testing.T) {
		entity := TestEntity{
			ID:         uuid.New(),
			AccountID:  AccountID("dddddddd-dddd-4ddd-addd-dddddddddddd"),
			CustomerID: CustomerID("eeeeeeee-eeee-4eee-aeee-eeeeeeeeeeee"),
			LedgerID:   LedgerID("ffffffff-ffff-4fff-afff-ffffffffffff"),
			Name:       "Original Name",
		}

		err := db.Create(&entity).Error
		require.NoError(t, err)

		// Update
		newLedgerID := LedgerID("a1b2c3d4-e5f6-4789-abcd-ef1234567890")
		err = db.Model(&entity).Update("ledger_id", newLedgerID).Error
		require.NoError(t, err)

		// Verify update
		var updated TestEntity
		err = db.First(&updated, "id = ?", entity.ID).Error
		require.NoError(t, err)
		assert.Equal(t, newLedgerID, updated.LedgerID)
	})

	t.Run("PostgreSQL normalizes UUID case to lowercase", func(t *testing.T) {
		// PostgreSQL UUID columns store as lowercase
		uppercaseID := "550E8400-E29B-41D4-A716-446655440001"
		entity := TestEntity{
			ID:         uuid.New(),
			AccountID:  AccountID(uppercaseID),
			CustomerID: CustomerID("11111111-1111-4111-a111-111111111111"),
			LedgerID:   LedgerID("22222222-2222-4222-a222-222222222222"),
			Name:       "Case Test Entity",
		}

		err := db.Create(&entity).Error
		require.NoError(t, err)

		var retrieved TestEntity
		err = db.First(&retrieved, "id = ?", entity.ID).Error
		require.NoError(t, err)

		// PostgreSQL normalizes UUIDs to lowercase
		expectedLowercase := strings.ToLower(uppercaseID)
		assert.Equal(t, AccountID(expectedLowercase), retrieved.AccountID)
	})

	t.Run("multiple entities with different IDs", func(t *testing.T) {
		entities := []TestEntity{
			{
				ID:         uuid.New(),
				AccountID:  AccountID("11111111-1111-4111-a111-111111111111"),
				CustomerID: CustomerID("22222222-2222-4222-a222-222222222222"),
				LedgerID:   LedgerID("33333333-3333-4333-a333-333333333333"),
				Name:       "Multi Entity 1",
			},
			{
				ID:         uuid.New(),
				AccountID:  AccountID("44444444-4444-4444-a444-444444444444"),
				CustomerID: CustomerID("55555555-5555-4555-a555-555555555555"),
				LedgerID:   LedgerID("66666666-6666-4666-a666-666666666666"),
				Name:       "Multi Entity 2",
			},
		}

		for _, e := range entities {
			err := db.Create(&e).Error
			require.NoError(t, err)
		}

		// Query specific entities by their unique AccountIDs
		var found1, found2 TestEntity
		err := db.Where("account_id = ?", entities[0].AccountID).First(&found1).Error
		require.NoError(t, err)
		assert.Equal(t, "Multi Entity 1", found1.Name)

		err = db.Where("account_id = ?", entities[1].AccountID).First(&found2).Error
		require.NoError(t, err)
		assert.Equal(t, "Multi Entity 2", found2.Name)
	})
}

func TestIdentifiers_TypeSafety_PostgresIntegration(t *testing.T) {
	db, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	t.Run("same UUID string results in different types", func(t *testing.T) {
		sameUUID := "550e8400-e29b-41d4-a716-446655440000"

		entity := TestEntity{
			ID:         uuid.New(),
			AccountID:  AccountID(sameUUID),
			CustomerID: CustomerID(sameUUID), // Same UUID, different type
			LedgerID:   LedgerID(sameUUID),   // Same UUID, different type
			Name:       "Same UUID Test",
		}

		err := db.Create(&entity).Error
		require.NoError(t, err)

		var retrieved TestEntity
		err = db.First(&retrieved, "id = ?", entity.ID).Error
		require.NoError(t, err)

		// All IDs have the same string value
		assert.Equal(t, retrieved.AccountID.String(), retrieved.CustomerID.String())
		assert.Equal(t, retrieved.AccountID.String(), retrieved.LedgerID.String())

		// But they are different Go types (compile-time safety)
		// This line would not compile: var _ AccountID = retrieved.CustomerID
	})
}
