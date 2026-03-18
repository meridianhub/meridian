package persistence

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/meridianhub/meridian/services/identity/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const (
	testTenantID   = "test_tenant"
	secondTenantID = "test_tenant_b"
)

var identityModels = []interface{}{
	&IdentityEntity{},
	&RoleAssignmentEntity{},
	&InvitationEntity{},
}

func setupTestDB(t *testing.T) (*gorm.DB, context.Context, func()) {
	t.Helper()
	return testdb.SetupTestDB(t,
		testdb.WithModels(identityModels...),
		testdb.WithTenant(testTenantID),
	)
}

// setupTenantSchema creates an additional tenant schema with the identity tables.
// Used for multi-tenant isolation tests. Session affinity for SET search_path is
// guaranteed because SetupTestDB pins the connection pool to MaxOpenConns(1).
func setupTenantSchema(t *testing.T, db *gorm.DB, tid tenant.TenantID) context.Context {
	t.Helper()
	schemaName := tid.SchemaName()

	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Temporarily switch search_path to new tenant schema for AutoMigrate
	err = db.Exec(fmt.Sprintf("SET search_path TO %s, public", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	err = db.AutoMigrate(identityModels...)
	require.NoError(t, err)

	// Restore search_path to the primary tenant schema
	primarySchema := tenant.TenantID(testTenantID).SchemaName()
	err = db.Exec(fmt.Sprintf("SET search_path TO %s, public", pq.QuoteIdentifier(primarySchema))).Error
	require.NoError(t, err)

	return tenant.WithTenant(context.Background(), tid)
}

// TestSaveNewIdentity verifies basic create + retrieve round-trip.
func TestSaveNewIdentity(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	identity, err := domain.NewIdentity("alice@example.com")
	require.NoError(t, err)

	err = repo.Save(ctx, identity)
	require.NoError(t, err)

	retrieved, err := repo.FindByID(ctx, identity.ID())
	require.NoError(t, err)
	assert.Equal(t, identity.ID(), retrieved.ID())
	assert.Equal(t, "alice@example.com", retrieved.Email())
	assert.Equal(t, domain.IdentityStatusPendingInvite, retrieved.Status())
	assert.Equal(t, int64(1), retrieved.Version())
}

// TestSaveIdentity_UpdateWithOptimisticLocking verifies that an updated identity
// increments the version and persists field changes.
func TestSaveIdentity_UpdateWithOptimisticLocking(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	identity, err := domain.NewIdentity("bob@example.com")
	require.NoError(t, err)

	err = repo.Save(ctx, identity)
	require.NoError(t, err)

	// Mutate: activate transitions status and increments version
	err = identity.Activate()
	require.NoError(t, err)

	err = repo.Save(ctx, identity)
	require.NoError(t, err)

	retrieved, err := repo.FindByID(ctx, identity.ID())
	require.NoError(t, err)
	assert.Equal(t, domain.IdentityStatusActive, retrieved.Status())
	assert.Equal(t, int64(2), retrieved.Version())
}

// TestSaveIdentity_VersionConflict verifies that concurrent saves on stale version return ErrVersionConflict.
func TestSaveIdentity_VersionConflict(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	identity, err := domain.NewIdentity("carol@example.com")
	require.NoError(t, err)

	err = repo.Save(ctx, identity)
	require.NoError(t, err)

	// Load the same identity into two separate instances
	v1, err := repo.FindByID(ctx, identity.ID())
	require.NoError(t, err)
	v2, err := repo.FindByID(ctx, identity.ID())
	require.NoError(t, err)

	// First save succeeds
	require.NoError(t, v1.Activate())
	err = repo.Save(ctx, v1)
	require.NoError(t, err)

	// Second save on stale version fails
	require.NoError(t, v2.Activate())
	err = repo.Save(ctx, v2)
	assert.ErrorIs(t, err, ErrVersionConflict)
}

// TestSaveIdentity_DuplicateEmail verifies that inserting a duplicate email returns ErrEmailAlreadyExists.
func TestSaveIdentity_DuplicateEmail(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	first, err := domain.NewIdentity("dave@example.com")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, first))

	second, err := domain.NewIdentity("dave@example.com")
	require.NoError(t, err)
	err = repo.Save(ctx, second)
	assert.ErrorIs(t, err, domain.ErrEmailAlreadyExists)
}

// TestFindByID_NotFound verifies that a missing identity returns ErrIdentityNotFound.
func TestFindByID_NotFound(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	_, err := repo.FindByID(ctx, uuid.New())
	assert.ErrorIs(t, err, domain.ErrIdentityNotFound)
}

// TestFindByEmail verifies lookup by email address.
func TestFindByEmail(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	identity, err := domain.NewIdentity("eve@example.com")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, identity))

	retrieved, err := repo.FindByEmail(ctx, "eve@example.com")
	require.NoError(t, err)
	assert.Equal(t, identity.ID(), retrieved.ID())
}

// TestFindByEmail_NotFound verifies that a missing email returns ErrIdentityNotFound.
func TestFindByEmail_NotFound(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	_, err := repo.FindByEmail(ctx, "nobody@example.com")
	assert.ErrorIs(t, err, domain.ErrIdentityNotFound)
}

// TestListByTenant verifies that all tenant identities are returned.
func TestListByTenant(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	for _, email := range []string{"a@example.com", "b@example.com", "c@example.com"} {
		identity, err := domain.NewIdentity(email)
		require.NoError(t, err)
		require.NoError(t, repo.Save(ctx, identity))
	}

	all, err := repo.ListByTenant(ctx)
	require.NoError(t, err)
	assert.Len(t, all, 3)
}

// TestCrossTenantIsolation verifies that identities in one tenant are not visible to another.
func TestCrossTenantIsolation(t *testing.T) {
	db, _, cleanup := setupTestDB(t)
	defer cleanup()

	tidA := tenant.TenantID(testTenantID)
	tidB := tenant.TenantID(secondTenantID)

	ctxA := tenant.WithTenant(context.Background(), tidA)
	ctxB := setupTenantSchema(t, db, tidB)

	repoA := NewRepository(db)
	repoB := NewRepository(db)

	// Save identity in tenant A
	identityA, err := domain.NewIdentity("tenant-a@example.com")
	require.NoError(t, err)
	require.NoError(t, repoA.Save(ctxA, identityA))

	// Tenant B should not see tenant A's identity
	_, err = repoB.FindByID(ctxB, identityA.ID())
	assert.ErrorIs(t, err, domain.ErrIdentityNotFound)

	listB, err := repoB.ListByTenant(ctxB)
	require.NoError(t, err)
	assert.Empty(t, listB)
}

// TestSaveRoleAssignment_CreateAndRetrieve verifies basic role assignment persistence.
func TestSaveRoleAssignment_CreateAndRetrieve(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	// Create an identity first
	identity, err := domain.NewIdentity("frank@example.com")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, identity))

	granterID := uuid.New()
	assignment, err := domain.NewRoleAssignment(identity.ID(), granterID, string(domain.RolePlatform), string(domain.RoleAdmin))
	require.NoError(t, err)

	err = repo.SaveRoleAssignment(ctx, assignment)
	require.NoError(t, err)

	assignments, err := repo.FindRoleAssignments(ctx, identity.ID())
	require.NoError(t, err)
	require.Len(t, assignments, 1)
	assert.Equal(t, assignment.ID(), assignments[0].ID())
	assert.Equal(t, domain.RoleAdmin, assignments[0].Role())
	assert.Nil(t, assignments[0].RevokedAt())
}

// TestSaveRoleAssignment_Revoke verifies that revoking a role assignment is persisted.
func TestSaveRoleAssignment_Revoke(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	identity, err := domain.NewIdentity("grace@example.com")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, identity))

	granterID := uuid.New()
	assignment, err := domain.NewRoleAssignment(identity.ID(), granterID, string(domain.RolePlatform), string(domain.RoleViewer))
	require.NoError(t, err)
	require.NoError(t, repo.SaveRoleAssignment(ctx, assignment))

	// Revoke the assignment
	revokerID := uuid.New()
	require.NoError(t, assignment.Revoke(revokerID))
	require.NoError(t, repo.SaveRoleAssignment(ctx, assignment))

	assignments, err := repo.FindRoleAssignments(ctx, identity.ID())
	require.NoError(t, err)
	require.Len(t, assignments, 1)
	assert.NotNil(t, assignments[0].RevokedAt())
	assert.Equal(t, revokerID, *assignments[0].RevokedBy())
}

// TestSaveInvitation_CreateAndRetrieveByTokenHash verifies invitation persistence.
func TestSaveInvitation_CreateAndRetrieveByTokenHash(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	identity, err := domain.NewIdentity("henry@example.com")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, identity))

	inviterID := uuid.New()
	invitation, _, err := domain.NewInvitation(identity.ID(), inviterID)
	require.NoError(t, err)

	err = repo.SaveInvitation(ctx, invitation)
	require.NoError(t, err)

	retrieved, err := repo.FindInvitationByTokenHash(ctx, invitation.TokenHash())
	require.NoError(t, err)
	assert.Equal(t, invitation.ID(), retrieved.ID())
	assert.Equal(t, domain.InvitationStatusPending, retrieved.Status())
}

// TestFindInvitationByTokenHash_NotFound verifies that a missing token returns ErrInvitationNotFound.
func TestFindInvitationByTokenHash_NotFound(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	_, err := repo.FindInvitationByTokenHash(ctx, "nonexistent-hash")
	assert.ErrorIs(t, err, domain.ErrInvitationNotFound)
}

// TestSaveInvitation_Accept verifies that accepting an invitation persists the status change.
func TestSaveInvitation_Accept(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	identity, err := domain.NewIdentity("iris@example.com")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, identity))

	inviterID := uuid.New()
	invitation, _, err := domain.NewInvitation(identity.ID(), inviterID)
	require.NoError(t, err)
	require.NoError(t, repo.SaveInvitation(ctx, invitation))

	require.NoError(t, invitation.Accept())
	require.NoError(t, repo.SaveInvitation(ctx, invitation))

	retrieved, err := repo.FindInvitationByTokenHash(ctx, invitation.TokenHash())
	require.NoError(t, err)
	assert.Equal(t, domain.InvitationStatusAccepted, retrieved.Status())
}

// --- SaveIdentityWithInvitation Tests ---

// TestSaveIdentityWithInvitation_NewIdentityAndInvitation verifies atomic creation of
// both an identity and an invitation in a single transaction.
func TestSaveIdentityWithInvitation_NewIdentityAndInvitation(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	identity, err := domain.NewIdentity("invite-new@example.com")
	require.NoError(t, err)

	inviterID := uuid.New()
	invitation, _, err := domain.NewInvitation(identity.ID(), inviterID)
	require.NoError(t, err)

	err = repo.SaveIdentityWithInvitation(ctx, identity, invitation)
	require.NoError(t, err)

	// Verify identity was persisted
	retrieved, err := repo.FindByID(ctx, identity.ID())
	require.NoError(t, err)
	assert.Equal(t, "invite-new@example.com", retrieved.Email())

	// Verify invitation was persisted
	retrievedInv, err := repo.FindInvitationByTokenHash(ctx, invitation.TokenHash())
	require.NoError(t, err)
	assert.Equal(t, domain.InvitationStatusPending, retrievedInv.Status())
}

// TestSaveIdentityWithInvitation_ExistingIdentityUpdated verifies that calling
// SaveIdentityWithInvitation on an existing identity applies optimistic locking.
func TestSaveIdentityWithInvitation_ExistingIdentityUpdated(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	// Create identity first
	identity, err := domain.NewIdentity("invite-update@example.com")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, identity))

	// Activate identity (simulating password set flow)
	require.NoError(t, identity.Activate())

	inviterID := uuid.New()
	invitation, _, err := domain.NewInvitation(identity.ID(), inviterID)
	require.NoError(t, err)

	err = repo.SaveIdentityWithInvitation(ctx, identity, invitation)
	require.NoError(t, err)

	// Verify identity was updated
	retrieved, err := repo.FindByID(ctx, identity.ID())
	require.NoError(t, err)
	assert.Equal(t, domain.IdentityStatusActive, retrieved.Status())
	assert.Equal(t, int64(2), retrieved.Version())
}

// TestSaveIdentityWithInvitation_DuplicateEmail verifies that creating a duplicate
// email identity returns ErrEmailAlreadyExists.
func TestSaveIdentityWithInvitation_DuplicateEmail(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	// Create first identity
	first, err := domain.NewIdentity("invite-dup@example.com")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, first))

	// Try to create another identity with the same email
	second, err := domain.NewIdentity("invite-dup@example.com")
	require.NoError(t, err)

	inviterID := uuid.New()
	invitation, _, err := domain.NewInvitation(second.ID(), inviterID)
	require.NoError(t, err)

	err = repo.SaveIdentityWithInvitation(ctx, second, invitation)
	assert.ErrorIs(t, err, domain.ErrEmailAlreadyExists)
}

// TestSaveIdentityWithInvitation_ExistingInvitationUpdated verifies that calling
// SaveIdentityWithInvitation with an existing invitation updates the invitation.
func TestSaveIdentityWithInvitation_ExistingInvitationUpdated(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	identity, err := domain.NewIdentity("invite-existinginv@example.com")
	require.NoError(t, err)

	inviterID := uuid.New()
	invitation, _, err := domain.NewInvitation(identity.ID(), inviterID)
	require.NoError(t, err)

	// Save identity and invitation the first time
	err = repo.SaveIdentityWithInvitation(ctx, identity, invitation)
	require.NoError(t, err)

	// Accept the invitation
	require.NoError(t, invitation.Accept())

	// Activate the identity
	require.NoError(t, identity.Activate())

	// Save again — invitation update path, identity update path
	err = repo.SaveIdentityWithInvitation(ctx, identity, invitation)
	require.NoError(t, err)

	// Verify invitation was updated
	retrievedInv, err := repo.FindInvitationByTokenHash(ctx, invitation.TokenHash())
	require.NoError(t, err)
	assert.Equal(t, domain.InvitationStatusAccepted, retrievedInv.Status())
}

// TestSaveIdentityWithInvitation_VersionConflict verifies that concurrent updates
// on stale versions return ErrVersionConflict.
func TestSaveIdentityWithInvitation_VersionConflict(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	identity, err := domain.NewIdentity("invite-conflict@example.com")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, identity))

	// Load two copies
	v1, err := repo.FindByID(ctx, identity.ID())
	require.NoError(t, err)
	v2, err := repo.FindByID(ctx, identity.ID())
	require.NoError(t, err)

	// First update succeeds
	require.NoError(t, v1.Activate())
	inv1, _, err := domain.NewInvitation(v1.ID(), uuid.New())
	require.NoError(t, err)
	err = repo.SaveIdentityWithInvitation(ctx, v1, inv1)
	require.NoError(t, err)

	// Second update on stale version fails
	require.NoError(t, v2.Activate())
	inv2, _, err := domain.NewInvitation(v2.ID(), uuid.New())
	require.NoError(t, err)
	err = repo.SaveIdentityWithInvitation(ctx, v2, inv2)
	assert.ErrorIs(t, err, ErrVersionConflict)
}

// --- SaveIdentityWithRoles Tests ---

// TestSaveIdentityWithRoles_Success verifies atomic creation of an identity and roles.
func TestSaveIdentityWithRoles_Success(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	identity, err := domain.NewIdentity("withroles@example.com")
	require.NoError(t, err)

	granterID := uuid.New()
	role1, err := domain.NewRoleAssignment(identity.ID(), granterID, string(domain.RolePlatform), string(domain.RoleAdmin))
	require.NoError(t, err)
	role2, err := domain.NewRoleAssignment(identity.ID(), granterID, string(domain.RolePlatform), string(domain.RoleOperator))
	require.NoError(t, err)

	err = repo.SaveIdentityWithRoles(ctx, identity, []*domain.RoleAssignment{role1, role2})
	require.NoError(t, err)

	// Verify identity persisted
	retrieved, err := repo.FindByID(ctx, identity.ID())
	require.NoError(t, err)
	assert.Equal(t, "withroles@example.com", retrieved.Email())

	// Verify roles persisted
	assignments, err := repo.FindRoleAssignments(ctx, identity.ID())
	require.NoError(t, err)
	assert.Len(t, assignments, 2)
}

// TestSaveIdentityWithRoles_DuplicateEmail verifies ErrEmailAlreadyExists.
func TestSaveIdentityWithRoles_DuplicateEmail(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	first, err := domain.NewIdentity("withroles-dup@example.com")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, first))

	second, err := domain.NewIdentity("withroles-dup@example.com")
	require.NoError(t, err)

	err = repo.SaveIdentityWithRoles(ctx, second, nil)
	assert.ErrorIs(t, err, domain.ErrEmailAlreadyExists)
}

// TestSaveIdentityWithRoles_EmptyRoles verifies that passing empty roles still persists the identity.
func TestSaveIdentityWithRoles_EmptyRoles(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	identity, err := domain.NewIdentity("withroles-empty@example.com")
	require.NoError(t, err)

	err = repo.SaveIdentityWithRoles(ctx, identity, []*domain.RoleAssignment{})
	require.NoError(t, err)

	retrieved, err := repo.FindByID(ctx, identity.ID())
	require.NoError(t, err)
	assert.Equal(t, "withroles-empty@example.com", retrieved.Email())
}

// --- SaveRoleAssignments Tests ---

// TestSaveRoleAssignments_Success verifies batch role assignment persistence.
func TestSaveRoleAssignments_Success(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	identity, err := domain.NewIdentity("batchroles@example.com")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, identity))

	granterID := uuid.New()
	role1, err := domain.NewRoleAssignment(identity.ID(), granterID, string(domain.RolePlatform), string(domain.RoleAdmin))
	require.NoError(t, err)
	role2, err := domain.NewRoleAssignment(identity.ID(), granterID, string(domain.RolePlatform), string(domain.RoleOperator))
	require.NoError(t, err)
	role3, err := domain.NewRoleAssignment(identity.ID(), granterID, string(domain.RolePlatform), string(domain.RoleViewer))
	require.NoError(t, err)

	err = repo.SaveRoleAssignments(ctx, []*domain.RoleAssignment{role1, role2, role3})
	require.NoError(t, err)

	assignments, err := repo.FindRoleAssignments(ctx, identity.ID())
	require.NoError(t, err)
	assert.Len(t, assignments, 3)
}

// TestSaveRoleAssignments_EmptySlice verifies that passing an empty slice succeeds.
func TestSaveRoleAssignments_EmptySlice(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	err := repo.SaveRoleAssignments(ctx, []*domain.RoleAssignment{})
	require.NoError(t, err)
}
