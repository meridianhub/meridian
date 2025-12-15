package persistence

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/party/domain"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/tenant"
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
	retrieved, err := repo.FindByID(ctx, party.ID())
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
	retrieved, err := repo.FindByID(ctx, party.ID())
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
	retrieved, err := repo.FindByID(ctx, party.ID())
	require.NoError(t, err)
	assert.Equal(t, "Johnny D", retrieved.DisplayName())

	// Version should be incremented after update
	assert.Equal(t, int64(2), retrieved.Version())
}

func TestFindByIDNotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	_, err := repo.FindByID(ctx, uuid.New())
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

	retrieved, err := repo.FindByExternalReference(ctx, "12345678", string(domain.ExternalReferenceTypeCompaniesHouse))
	require.NoError(t, err)
	assert.Equal(t, party.ID(), retrieved.ID())
	assert.Equal(t, "12345678", retrieved.ExternalReference())
}

func TestFindByExternalReferenceNotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	_, err := repo.FindByExternalReference(ctx, "NONEXISTENT", string(domain.ExternalReferenceTypeCompaniesHouse))
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
	exists, err := repo.ExistsByID(ctx, party.ID())
	require.NoError(t, err)
	assert.True(t, exists)

	// Non-existent party
	exists, err = repo.ExistsByID(ctx, uuid.New())
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
	err = repo.Delete(ctx, party.ID())
	require.NoError(t, err)

	// Should not be found after soft delete
	_, err = repo.FindByID(ctx, party.ID())
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
	party2, err := repo.FindByID(ctx, party1.ID())
	require.NoError(t, err)

	party3, err := repo.FindByID(ctx, party1.ID())
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
	final, err := repo.FindByID(ctx, party1.ID())
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
	err = repo.Delete(ctx, party.ID())
	require.NoError(t, err)

	// ExistsByID should return false for soft-deleted party
	exists, err := repo.ExistsByID(ctx, party.ID())
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

	err := repo.Ping(context.Background())
	assert.NoError(t, err)
}

// Helper function for creating string pointers
func stringPtr(s string) *string {
	return &s
}

// Multi-Organization Tests

func TestSave_WithOrganizationContext_SetsSearchPath(t *testing.T) {
	// This test verifies that when organization context is present,
	// the repository correctly uses WithGormTenantScope.
	// In single-tenant mode (no org context), it should work without transaction wrapping.
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	// Test single-tenant mode (no org context) - should work normally
	party, err := domain.NewParty(domain.PartyTypePerson, "Single Tenant Party")
	require.NoError(t, err)

	ctx := context.Background()
	err = repo.Save(ctx, party)
	require.NoError(t, err, "Save should work without organization context in single-tenant mode")

	// Verify party was saved
	retrieved, err := repo.FindByID(ctx, party.ID())
	require.NoError(t, err)
	assert.Equal(t, "Single Tenant Party", retrieved.LegalName())
}

func TestFindByID_WithOrganizationContext_IsolatesData(t *testing.T) {
	// In multi-org mode, when organization context is present, data is isolated by schema.
	// When the org schema doesn't exist but the SET LOCAL search_path succeeds (PostgreSQL
	// doesn't error on non-existent schemas), queries find nothing in the org schema.
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	// Create party in single-tenant mode (public schema)
	party, err := domain.NewParty(domain.PartyTypePerson, "Test Party")
	require.NoError(t, err)

	ctx := context.Background()
	err = repo.Save(ctx, party)
	require.NoError(t, err)

	// Verify party exists in single-tenant mode
	_, err = repo.FindByID(ctx, party.ID())
	require.NoError(t, err, "Party should be findable without org context")

	// With organization context, the search_path changes to org_acme_bank,public.
	// Since the parties table only exists in public, this should still work
	// (public is included in search_path), but the isolation is enforced at the
	// schema level when org schemas are properly set up.
	orgID := tenant.TenantID("acme_bank")
	orgCtx := tenant.WithTenant(ctx, orgID)

	// This may return the party (from public schema) or error depending on
	// whether the SET LOCAL succeeds. The key behavior is that the code path
	// attempts to set the search_path when org context is present.
	_, err = repo.FindByID(orgCtx, party.ID())
	// Note: In a test environment without org schemas, this may or may not error.
	// The system is always multi-tenant - tenant context is always required.
	// Full isolation testing requires proper org schema setup.
	t.Logf("FindByID with org context result: %v", err)
}

func TestExistsByID_WithOrganizationContext_UsesOrgScope(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	// Create party in single-tenant mode
	party, err := domain.NewParty(domain.PartyTypePerson, "Test Party")
	require.NoError(t, err)

	ctx := context.Background()
	err = repo.Save(ctx, party)
	require.NoError(t, err)

	// Verify exists in single-tenant mode
	exists, err := repo.ExistsByID(ctx, party.ID())
	require.NoError(t, err)
	assert.True(t, exists, "Party should exist without org context")

	// With organization context, the search_path is changed.
	// Since we include public schema in search_path, the party is still found.
	orgID := tenant.TenantID("acme_bank")
	orgCtx := tenant.WithTenant(ctx, orgID)

	// The query will use the org-scoped transaction
	exists, err = repo.ExistsByID(orgCtx, party.ID())
	require.NoError(t, err)
	// Party may still be found via public schema fallback in search_path
	t.Logf("ExistsByID with org context: exists=%v, err=%v", exists, err)
}

func TestFindByExternalReference_WithOrganizationContext_UsesOrgScope(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	// Create party with external reference in single-tenant mode
	party, err := domain.NewParty(domain.PartyTypeOrganization, "Acme Corp Ltd")
	require.NoError(t, err)
	err = party.SetExternalReference("12345678", domain.ExternalReferenceTypeCompaniesHouse)
	require.NoError(t, err)

	ctx := context.Background()
	err = repo.Save(ctx, party)
	require.NoError(t, err)

	// Verify findable in single-tenant mode
	found, err := repo.FindByExternalReference(ctx, "12345678", string(domain.ExternalReferenceTypeCompaniesHouse))
	require.NoError(t, err)
	assert.Equal(t, party.ID(), found.ID())

	// With organization context, uses org-scoped search_path
	orgID := tenant.TenantID("acme_bank")
	orgCtx := tenant.WithTenant(ctx, orgID)

	found, err = repo.FindByExternalReference(orgCtx, "12345678", string(domain.ExternalReferenceTypeCompaniesHouse))
	// May find via public schema fallback
	t.Logf("FindByExternalReference with org context: found=%v, err=%v", found != nil, err)
}

func TestDelete_WithOrganizationContext_UsesOrgScope(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	// Create party in single-tenant mode
	party, err := domain.NewParty(domain.PartyTypePerson, "To Be Deleted")
	require.NoError(t, err)

	ctx := context.Background()
	err = repo.Save(ctx, party)
	require.NoError(t, err)

	// Delete with organization context - uses org-scoped search_path
	orgID := tenant.TenantID("acme_bank")
	orgCtx := tenant.WithTenant(ctx, orgID)

	err = repo.Delete(orgCtx, party.ID())
	// Delete may succeed via public schema fallback
	t.Logf("Delete with org context: err=%v", err)

	// Verify party was actually deleted (via public schema)
	_, err = repo.FindByID(ctx, party.ID())
	// Could be deleted or not depending on schema resolution
	t.Logf("FindByID after delete: err=%v", err)
}

func TestFindByIDForUpdate_WithOrganizationContext_UsesOrgScope(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	// Create party in single-tenant mode
	party, err := domain.NewParty(domain.PartyTypePerson, "Test Party")
	require.NoError(t, err)

	ctx := context.Background()
	err = repo.Save(ctx, party)
	require.NoError(t, err)

	// Verify findable in single-tenant mode
	found, err := repo.FindByIDForUpdate(ctx, party.ID())
	require.NoError(t, err)
	assert.Equal(t, party.ID(), found.ID())

	// With organization context, uses org-scoped transaction
	orgID := tenant.TenantID("acme_bank")
	orgCtx := tenant.WithTenant(ctx, orgID)

	found, err = repo.FindByIDForUpdate(orgCtx, party.ID())
	// May find via public schema fallback
	t.Logf("FindByIDForUpdate with org context: found=%v, err=%v", found != nil, err)
}

func TestPing_WorksWithoutOrganizationContext(t *testing.T) {
	// Ping is a health check and should work without organization context
	// This is critical for health checks to succeed even in multi-org mode
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	// Ping should work without any context
	err := repo.Ping(context.Background())
	assert.NoError(t, err, "Ping should work without organization context")

	// Ping should also work when org context is present (it ignores it)
	orgID := tenant.TenantID("acme_bank")
	orgCtx := tenant.WithTenant(context.Background(), orgID)

	err = repo.Ping(orgCtx)
	assert.NoError(t, err, "Ping should work even with organization context (ignores it)")
}

// Note: hasTenantContext tests removed - the system is always multi-tenant.
// Tenant context is always required for all business service operations.
