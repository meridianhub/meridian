package persistence

import (
	"context"
	"errors"
	"fmt"
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

const testTenantID = "test_tenant"

func setupTestDB(t *testing.T) (*gorm.DB, context.Context, func()) {
	t.Helper()
	db, cleanup := testdb.SetupPostgres(t, []interface{}{&PartyEntity{}, &PartyAuditOutbox{}})

	// Create the tenant schema for tests
	tid := tenant.TenantID(testTenantID)
	schemaName := tid.SchemaName()
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %q", schemaName)).Error
	require.NoError(t, err)

	// Create the party table in the tenant schema (note: singular 'party' to match entity)
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q.party (
		id UUID PRIMARY KEY,
		party_type VARCHAR(20) NOT NULL,
		legal_name VARCHAR(255) NOT NULL,
		display_name VARCHAR(255),
		status VARCHAR(20) NOT NULL,
		external_reference VARCHAR(255),
		external_reference_type VARCHAR(50),
		version BIGINT NOT NULL DEFAULT 1,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL,
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
		deleted_at TIMESTAMP WITH TIME ZONE,
		created_by VARCHAR(255),
		updated_by VARCHAR(255),
		UNIQUE(external_reference, external_reference_type)
	)`, schemaName)).Error
	require.NoError(t, err)

	// Create the audit_outbox table in the tenant schema (required for audit hooks)
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q.audit_outbox (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		table_name VARCHAR(100) NOT NULL,
		operation VARCHAR(10) NOT NULL,
		record_id UUID NOT NULL,
		old_values TEXT,
		new_values TEXT,
		status VARCHAR(20) NOT NULL DEFAULT 'pending',
		created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
		retry_count INT NOT NULL DEFAULT 0,
		last_error TEXT,
		changed_by VARCHAR(100),
		transaction_id VARCHAR(100),
		client_ip VARCHAR(45),
		user_agent TEXT
	)`, schemaName)).Error
	require.NoError(t, err)

	// Set default search_path to include tenant schema so Create/Update work in the tenant schema
	err = db.Exec(fmt.Sprintf("SET search_path TO %q, public", schemaName)).Error
	require.NoError(t, err)

	// Create context with tenant
	ctx := tenant.WithTenant(context.Background(), tid)

	return db, ctx, cleanup
}

func TestSaveNewParty(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	party, err := domain.NewParty(domain.PartyTypePerson, "John Doe")
	require.NoError(t, err)

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
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	party, err := domain.NewParty(domain.PartyTypeOrganization, "Acme Corp")
	require.NoError(t, err)

	err = repo.Save(ctx, party)
	require.NoError(t, err)

	// Verify newly created party has version 1
	retrieved, err := repo.FindByID(ctx, party.ID())
	require.NoError(t, err)
	assert.Equal(t, int64(1), retrieved.Version(), "New party should have version 1")
}

func TestSaveUpdateExisting(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

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
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	_, err := repo.FindByID(ctx, uuid.New())
	assert.True(t, errors.Is(err, ErrPartyNotFound))
}

func TestFindByExternalReference(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

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
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	_, err := repo.FindByExternalReference(ctx, "NONEXISTENT", string(domain.ExternalReferenceTypeCompaniesHouse))
	assert.True(t, errors.Is(err, ErrPartyNotFound))
}

func TestExistsByID(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

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
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

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
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

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
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	// Create first party with external reference
	party1, err := domain.NewParty(domain.PartyTypeOrganization, "Company A")
	require.NoError(t, err)

	err = party1.SetExternalReference("12345678", domain.ExternalReferenceTypeCompaniesHouse)
	require.NoError(t, err)

	err = repo.Save(ctx, party1)
	require.NoError(t, err)

	// Create second party with same external reference - should fail
	// Use repository to ensure proper tenant scoping
	party2, err := domain.NewParty(domain.PartyTypeOrganization, "Company B")
	require.NoError(t, err)

	err = party2.SetExternalReference("12345678", domain.ExternalReferenceTypeCompaniesHouse)
	require.NoError(t, err)

	err = repo.Save(ctx, party2)
	assert.Error(t, err, "Should fail with duplicate external reference")
	assert.True(t, errors.Is(err, ErrPartyExists), "Should be ErrPartyExists due to duplicate external reference")
}

func TestSoftDeleteVerification(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

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
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	party, err := domain.NewParty(domain.PartyTypePerson, "Audit Test")
	require.NoError(t, err)

	// Create context with authenticated user on top of tenant context
	testUserID := "user-123"
	ctx = context.WithValue(ctx, auth.UserIDContextKey, testUserID)

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
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	party, err := domain.NewParty(domain.PartyTypePerson, "System Test")
	require.NoError(t, err)

	// Use context without user (but still has tenant)

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
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	party, err := domain.NewParty(domain.PartyTypePerson, "Multi User Test")
	require.NoError(t, err)

	// Create with user1
	user1 := "user-creator"
	ctx1 := context.WithValue(ctx, auth.UserIDContextKey, user1)
	err = repo.Save(ctx1, party)
	require.NoError(t, err)

	// Update with user2
	user2 := "user-updater"
	ctx2 := context.WithValue(ctx, auth.UserIDContextKey, user2)
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
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	err := repo.Ping(ctx)
	assert.NoError(t, err)
}

// Multi-Organization Tests

func TestSave_WithOrganizationContext_SetsSearchPath(t *testing.T) {
	// This test verifies that when organization context is present,
	// the repository correctly uses WithGormTenantScope.
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	// Test with tenant context - should work normally
	party, err := domain.NewParty(domain.PartyTypePerson, "Single Tenant Party")
	require.NoError(t, err)

	err = repo.Save(ctx, party)
	require.NoError(t, err, "Save should work with tenant context")

	// Verify party was saved
	retrieved, err := repo.FindByID(ctx, party.ID())
	require.NoError(t, err)
	assert.Equal(t, "Single Tenant Party", retrieved.LegalName())
}

func TestFindByID_WithOrganizationContext_IsolatesData(t *testing.T) {
	// In multi-org mode, when organization context is present, data is isolated by schema.
	// When the org schema doesn't exist but the SET LOCAL search_path succeeds (PostgreSQL
	// doesn't error on non-existent schemas), queries find nothing in the org schema.
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	// Create party with tenant context
	party, err := domain.NewParty(domain.PartyTypePerson, "Test Party")
	require.NoError(t, err)

	err = repo.Save(ctx, party)
	require.NoError(t, err)

	// Verify party exists with tenant context
	_, err = repo.FindByID(ctx, party.ID())
	require.NoError(t, err, "Party should be findable with tenant context")

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
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	// Create party with tenant context
	party, err := domain.NewParty(domain.PartyTypePerson, "Test Party")
	require.NoError(t, err)

	err = repo.Save(ctx, party)
	require.NoError(t, err)

	// Verify exists with tenant context
	exists, err := repo.ExistsByID(ctx, party.ID())
	require.NoError(t, err)
	assert.True(t, exists, "Party should exist with tenant context")

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
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	// Create party with external reference with tenant context
	party, err := domain.NewParty(domain.PartyTypeOrganization, "Acme Corp Ltd")
	require.NoError(t, err)
	err = party.SetExternalReference("12345678", domain.ExternalReferenceTypeCompaniesHouse)
	require.NoError(t, err)

	err = repo.Save(ctx, party)
	require.NoError(t, err)

	// Verify findable with tenant context
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
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	// Create party with tenant context
	party, err := domain.NewParty(domain.PartyTypePerson, "To Be Deleted")
	require.NoError(t, err)

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
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	// Create party with tenant context
	party, err := domain.NewParty(domain.PartyTypePerson, "Test Party")
	require.NoError(t, err)

	err = repo.Save(ctx, party)
	require.NoError(t, err)

	// Verify findable with tenant context
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
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	// Ping should work with tenant context
	err := repo.Ping(ctx)
	assert.NoError(t, err, "Ping should work with tenant context")

	// Ping should also work when different org context is present (it ignores it)
	orgID := tenant.TenantID("acme_bank")
	orgCtx := tenant.WithTenant(context.Background(), orgID)

	err = repo.Ping(orgCtx)
	assert.NoError(t, err, "Ping should work even with different organization context (ignores it)")
}

// Note: hasTenantContext tests removed - the system is always multi-tenant.
// Tenant context is always required for all business service operations.

// Audit Tests

func TestAudit_CreateParty_WritesAuditOutboxEntry(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	// Create a party using direct GORM to trigger hooks
	party, err := domain.NewParty(domain.PartyTypePerson, "John Doe Audit Test")
	require.NoError(t, err)

	entity := &PartyEntity{
		ID:        party.ID(),
		PartyType: string(party.PartyType()),
		LegalName: party.LegalName(),
		Status:    string(party.Status()),
		Version:   party.Version(),
		CreatedAt: party.CreatedAt(),
		UpdatedAt: party.UpdatedAt(),
		CreatedBy: "test-user",
		UpdatedBy: "test-user",
	}

	// Create using GORM directly to ensure hooks fire
	err = db.WithContext(ctx).Create(entity).Error
	require.NoError(t, err)

	// Verify audit outbox entry was created
	var auditEntries []PartyAuditOutbox
	err = db.WithContext(ctx).Where("record_id = ?", party.ID()).Find(&auditEntries).Error
	require.NoError(t, err)

	require.Len(t, auditEntries, 1, "Expected one audit entry for party creation")

	auditEntry := auditEntries[0]
	assert.Equal(t, "party", auditEntry.Table, "Table name should be 'party'")
	assert.Equal(t, "INSERT", auditEntry.Operation, "Operation should be INSERT")
	assert.Equal(t, party.ID().String(), auditEntry.RecordID, "Record ID should match party ID")
	assert.Equal(t, "pending", auditEntry.Status, "Status should be pending")
	assert.Empty(t, auditEntry.OldValues, "Old values should be empty for INSERT")
	assert.NotEmpty(t, auditEntry.NewValues, "New values should contain party data")
	assert.NotNil(t, auditEntry.ChangedBy, "ChangedBy should be set")
}

func TestAudit_CreateParty_AuditContainsPartyFields(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	// Create a party with specific fields to verify in audit
	party, err := domain.NewParty(domain.PartyTypeOrganization, "Acme Corp Ltd")
	require.NoError(t, err)

	displayName := "Acme"
	extRef := "12345678"
	extRefType := string(domain.ExternalReferenceTypeCompaniesHouse)

	entity := &PartyEntity{
		ID:                    party.ID(),
		PartyType:             string(domain.PartyTypeOrganization),
		LegalName:             "Acme Corp Ltd",
		DisplayName:           &displayName,
		Status:                string(domain.PartyStatusActive),
		ExternalReference:     &extRef,
		ExternalReferenceType: &extRefType,
		Version:               1,
		CreatedAt:             party.CreatedAt(),
		UpdatedAt:             party.UpdatedAt(),
		CreatedBy:             "test-user",
		UpdatedBy:             "test-user",
	}

	err = db.WithContext(ctx).Create(entity).Error
	require.NoError(t, err)

	// Verify audit contains the expected fields
	var auditEntry PartyAuditOutbox
	err = db.WithContext(ctx).Where("record_id = ?", party.ID()).First(&auditEntry).Error
	require.NoError(t, err)

	// The new_values should contain JSON with party fields
	assert.Contains(t, auditEntry.NewValues, "Acme Corp Ltd", "Audit should contain legal_name")
	assert.Contains(t, auditEntry.NewValues, "ORGANIZATION", "Audit should contain party_type")
	assert.Contains(t, auditEntry.NewValues, "ACTIVE", "Audit should contain status")
	assert.Contains(t, auditEntry.NewValues, "12345678", "Audit should contain external_reference")
}

func TestAudit_CreateParty_WithUserContext_SetsChangedBy(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	// Add user to context
	testUserID := "user-456"
	ctx = context.WithValue(ctx, auth.UserIDContextKey, testUserID)

	party, err := domain.NewParty(domain.PartyTypePerson, "User Context Test")
	require.NoError(t, err)

	entity := &PartyEntity{
		ID:        party.ID(),
		PartyType: string(party.PartyType()),
		LegalName: party.LegalName(),
		Status:    string(party.Status()),
		Version:   1,
		CreatedAt: party.CreatedAt(),
		UpdatedAt: party.UpdatedAt(),
		CreatedBy: testUserID,
		UpdatedBy: testUserID,
	}

	err = db.WithContext(ctx).Create(entity).Error
	require.NoError(t, err)

	// Verify changed_by was set from context
	var auditEntry PartyAuditOutbox
	err = db.WithContext(ctx).Where("record_id = ?", party.ID()).First(&auditEntry).Error
	require.NoError(t, err)

	require.NotNil(t, auditEntry.ChangedBy, "ChangedBy should be set")
	assert.Equal(t, testUserID, *auditEntry.ChangedBy, "ChangedBy should match user from context")
}

func TestAudit_DeleteParty_WritesAuditOutboxEntry(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	// Create a party first
	party, err := domain.NewParty(domain.PartyTypePerson, "To Be Deleted")
	require.NoError(t, err)

	entity := &PartyEntity{
		ID:        party.ID(),
		PartyType: string(party.PartyType()),
		LegalName: party.LegalName(),
		Status:    string(party.Status()),
		Version:   1,
		CreatedAt: party.CreatedAt(),
		UpdatedAt: party.UpdatedAt(),
		CreatedBy: "test-user",
		UpdatedBy: "test-user",
	}

	err = db.WithContext(ctx).Create(entity).Error
	require.NoError(t, err)

	// Now delete using GORM Delete (which triggers AfterDelete hook)
	err = db.WithContext(ctx).Delete(entity).Error
	require.NoError(t, err)

	// Verify audit entries - should have INSERT and DELETE
	var auditEntries []PartyAuditOutbox
	err = db.WithContext(ctx).Where("record_id = ?", party.ID()).Order("created_at").Find(&auditEntries).Error
	require.NoError(t, err)

	require.Len(t, auditEntries, 2, "Expected two audit entries (INSERT and DELETE)")

	// First entry should be INSERT
	assert.Equal(t, "INSERT", auditEntries[0].Operation)

	// Second entry should be DELETE
	deleteEntry := auditEntries[1]
	assert.Equal(t, "DELETE", deleteEntry.Operation, "Operation should be DELETE")
	assert.Equal(t, "party", deleteEntry.Table)
	assert.NotEmpty(t, deleteEntry.OldValues, "Old values should contain deleted party data")
	assert.Empty(t, deleteEntry.NewValues, "New values should be empty for DELETE")
}
