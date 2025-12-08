package persistence

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/party/domain"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	return testdb.SetupPostgres(t, []interface{}{&PartyEntity{}})
}

func TestSaveNewParty(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	party, err := domain.NewParty(domain.PartyTypePerson, "John Doe")
	require.NoError(t, err)

	ctx := context.Background()
	err = repo.Save(ctx, party)
	require.NoError(t, err)

	// Verify party was saved
	retrieved, err := repo.FindByID(party.ID())
	require.NoError(t, err)
	assert.Equal(t, party.ID(), retrieved.ID())
	assert.Equal(t, "John Doe", retrieved.LegalName())
	assert.Equal(t, domain.PartyTypePerson, retrieved.PartyType())
	assert.Equal(t, domain.PartyStatusActive, retrieved.Status())
}

func TestSaveNewParty_InitialVersion(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	party, err := domain.NewParty(domain.PartyTypeOrganization, "Acme Corp")
	require.NoError(t, err)

	ctx := context.Background()
	err = repo.Save(ctx, party)
	require.NoError(t, err)

	// Verify newly created party has version 1
	retrieved, err := repo.FindByID(party.ID())
	require.NoError(t, err)
	assert.Equal(t, int64(1), retrieved.Version(), "New party should have version 1")
}

func TestSaveUpdateExisting(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	party, err := domain.NewParty(domain.PartyTypePerson, "John Doe")
	require.NoError(t, err)

	// Save initial
	err = repo.Save(ctx, party)
	require.NoError(t, err)

	// Modify and save again
	err = party.SetDisplayName("Johnny D")
	require.NoError(t, err)

	err = repo.Save(ctx, party)
	require.NoError(t, err)

	// Verify display name was updated
	retrieved, err := repo.FindByID(party.ID())
	require.NoError(t, err)
	assert.Equal(t, "Johnny D", retrieved.DisplayName())

	// Version should be incremented after update
	assert.Equal(t, int64(2), retrieved.Version())
}

func TestFindByIDNotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	_, err := repo.FindByID(uuid.New())
	assert.True(t, errors.Is(err, ErrPartyNotFound))
}

func TestFindByExternalReference(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	party, err := domain.NewParty(domain.PartyTypeOrganization, "Acme Corp Ltd")
	require.NoError(t, err)

	err = party.SetExternalReference("12345678", domain.ExternalReferenceTypeCompaniesHouse)
	require.NoError(t, err)

	err = repo.Save(ctx, party)
	require.NoError(t, err)

	retrieved, err := repo.FindByExternalReference("12345678", string(domain.ExternalReferenceTypeCompaniesHouse))
	require.NoError(t, err)
	assert.Equal(t, party.ID(), retrieved.ID())
	assert.Equal(t, "12345678", retrieved.ExternalReference())
}

func TestFindByExternalReferenceNotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	_, err := repo.FindByExternalReference("NONEXISTENT", string(domain.ExternalReferenceTypeCompaniesHouse))
	assert.True(t, errors.Is(err, ErrPartyNotFound))
}

func TestExistsByID(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	party, err := domain.NewParty(domain.PartyTypePerson, "Jane Doe")
	require.NoError(t, err)

	err = repo.Save(ctx, party)
	require.NoError(t, err)

	// Existing party
	exists, err := repo.ExistsByID(party.ID())
	require.NoError(t, err)
	assert.True(t, exists)

	// Non-existent party
	exists, err = repo.ExistsByID(uuid.New())
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestDeleteParty(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	party, err := domain.NewParty(domain.PartyTypePerson, "To Be Deleted")
	require.NoError(t, err)

	err = repo.Save(ctx, party)
	require.NoError(t, err)

	// Delete party
	err = repo.Delete(party.ID())
	require.NoError(t, err)

	// Should not be found after soft delete
	_, err = repo.FindByID(party.ID())
	assert.True(t, errors.Is(err, ErrPartyNotFound))
}

func TestOptimisticLocking(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Create initial party
	party1, err := domain.NewParty(domain.PartyTypePerson, "John Doe")
	require.NoError(t, err)

	err = repo.Save(ctx, party1)
	require.NoError(t, err)

	// Load same party in two "transactions"
	party2, err := repo.FindByID(party1.ID())
	require.NoError(t, err)

	party3, err := repo.FindByID(party1.ID())
	require.NoError(t, err)

	// Both should have same version
	assert.Equal(t, party2.Version(), party3.Version())

	// First transaction modifies and saves successfully
	err = party2.SetDisplayName("John D")
	require.NoError(t, err)

	err = repo.Save(ctx, party2)
	require.NoError(t, err)

	// Second transaction tries to save with stale version
	err = party3.SetDisplayName("Johnny Doe")
	require.NoError(t, err)

	err = repo.Save(ctx, party3)
	assert.True(t, errors.Is(err, ErrVersionConflict))

	// Verify first transaction's changes persisted
	final, err := repo.FindByID(party1.ID())
	require.NoError(t, err)
	assert.Equal(t, "John D", final.DisplayName())

	// Version should be incremented
	assert.Equal(t, int64(2), final.Version())
}

func TestExternalReferenceUniqueness(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Create first party with external reference
	party1, err := domain.NewParty(domain.PartyTypeOrganization, "Company A")
	require.NoError(t, err)

	err = party1.SetExternalReference("12345678", domain.ExternalReferenceTypeCompaniesHouse)
	require.NoError(t, err)

	err = repo.Save(ctx, party1)
	require.NoError(t, err)

	// Create second party with same external reference - should fail
	// Test at the database level by creating directly with entity
	entity := &PartyEntity{
		ID:                    uuid.New(),
		PartyType:             string(domain.PartyTypeOrganization),
		LegalName:             "Company B",
		Status:                string(domain.PartyStatusActive),
		ExternalReference:     stringPtr("12345678"),
		ExternalReferenceType: stringPtr(string(domain.ExternalReferenceTypeCompaniesHouse)),
		Version:               1,
		CreatedBy:             "system",
		UpdatedBy:             "system",
	}

	err = db.Create(entity).Error
	assert.Error(t, err, "Should fail with duplicate external reference")
	assert.True(t, isDuplicateKeyError(err), "Should be a duplicate key error")
}

func TestSoftDeleteVerification(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	party, err := domain.NewParty(domain.PartyTypePerson, "Soft Delete Test")
	require.NoError(t, err)

	err = repo.Save(ctx, party)
	require.NoError(t, err)

	// Delete party
	err = repo.Delete(party.ID())
	require.NoError(t, err)

	// ExistsByID should return false for soft-deleted party
	exists, err := repo.ExistsByID(party.ID())
	require.NoError(t, err)
	assert.False(t, exists)

	// Direct database query should still find the record with deleted_at set
	var entity PartyEntity
	err = db.Unscoped().Where("id = ?", party.ID()).First(&entity).Error
	require.NoError(t, err)
	assert.NotNil(t, entity.DeletedAt, "deleted_at should be set")
}

// Mapper unit tests

func TestToDomain_NullableFields(t *testing.T) {
	entity := &PartyEntity{
		ID:                    uuid.New(),
		PartyType:             string(domain.PartyTypePerson),
		LegalName:             "Test Person",
		DisplayName:           nil,
		Status:                string(domain.PartyStatusActive),
		ExternalReference:     nil,
		ExternalReferenceType: nil,
		Version:               1,
		CreatedBy:             "system",
		UpdatedBy:             "system",
	}

	party := toDomain(entity)

	assert.Equal(t, entity.ID, party.ID())
	assert.Equal(t, domain.PartyTypePerson, party.PartyType())
	assert.Equal(t, "Test Person", party.LegalName())
	assert.Equal(t, "", party.DisplayName())
	assert.Equal(t, "", party.ExternalReference())
}

func TestToDomain_WithOptionalFields(t *testing.T) {
	displayName := "Test Display"
	extRef := "12345678"
	extRefType := string(domain.ExternalReferenceTypeCompaniesHouse)

	entity := &PartyEntity{
		ID:                    uuid.New(),
		PartyType:             string(domain.PartyTypeOrganization),
		LegalName:             "Test Corp",
		DisplayName:           &displayName,
		Status:                string(domain.PartyStatusRestricted),
		ExternalReference:     &extRef,
		ExternalReferenceType: &extRefType,
		Version:               5,
		CreatedBy:             "user-123",
		UpdatedBy:             "user-456",
	}

	party := toDomain(entity)

	assert.Equal(t, "Test Display", party.DisplayName())
	assert.Equal(t, "12345678", party.ExternalReference())
	assert.Equal(t, domain.ExternalReferenceTypeCompaniesHouse, party.ExternalReferenceType())
	assert.Equal(t, int64(5), party.Version())
}

// Audit context tests

func TestSave_PopulatesAuditFieldsFromContext(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	party, err := domain.NewParty(domain.PartyTypePerson, "Audit Test")
	require.NoError(t, err)

	// Create context with authenticated user
	testUserID := "user-123"
	ctx := context.WithValue(context.Background(), auth.UserIDContextKey, testUserID)

	// Save new party
	err = repo.Save(ctx, party)
	require.NoError(t, err)

	// Verify audit fields were set from context
	var entity PartyEntity
	err = db.Where("id = ?", party.ID()).First(&entity).Error
	require.NoError(t, err)

	assert.Equal(t, testUserID, entity.CreatedBy, "created_by should be set from context")
	assert.Equal(t, testUserID, entity.UpdatedBy, "updated_by should be set from context")
}

func TestSave_UsesSystemWhenNoUserInContext(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	party, err := domain.NewParty(domain.PartyTypePerson, "System Test")
	require.NoError(t, err)

	// Use empty context (no user)
	ctx := context.Background()

	// Save new party
	err = repo.Save(ctx, party)
	require.NoError(t, err)

	// Verify audit fields default to "system"
	var entity PartyEntity
	err = db.Where("id = ?", party.ID()).First(&entity).Error
	require.NoError(t, err)

	assert.Equal(t, "system", entity.CreatedBy, "created_by should default to 'system'")
	assert.Equal(t, "system", entity.UpdatedBy, "updated_by should default to 'system'")
}

func TestSave_UpdatePreservesCreatedByButUpdatesUpdatedBy(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	party, err := domain.NewParty(domain.PartyTypePerson, "Multi User Test")
	require.NoError(t, err)

	// Create with user1
	user1 := "user-creator"
	ctx1 := context.WithValue(context.Background(), auth.UserIDContextKey, user1)
	err = repo.Save(ctx1, party)
	require.NoError(t, err)

	// Update with user2
	user2 := "user-updater"
	ctx2 := context.WithValue(context.Background(), auth.UserIDContextKey, user2)
	err = party.SetDisplayName("Updated Name")
	require.NoError(t, err)

	err = repo.Save(ctx2, party)
	require.NoError(t, err)

	// Verify created_by preserved but updated_by changed
	var entity PartyEntity
	err = db.Where("id = ?", party.ID()).First(&entity).Error
	require.NoError(t, err)

	assert.Equal(t, user1, entity.CreatedBy, "created_by should be preserved from original creation")
	assert.Equal(t, user2, entity.UpdatedBy, "updated_by should reflect the user who made the update")
}

func TestPing(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	err := repo.Ping()
	assert.NoError(t, err)
}

// Helper function for creating string pointers
func stringPtr(s string) *string {
	return &s
}
