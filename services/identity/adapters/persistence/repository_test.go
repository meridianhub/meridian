package persistence

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
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

func setupTestDB(t *testing.T) (*gorm.DB, context.Context, func()) {
	t.Helper()
	db, cleanup := testdb.SetupCockroachDB(t, nil)

	tid := tenant.TenantID(testTenantID)
	ctx := setupTenantSchema(t, db, tid)

	return db, ctx, cleanup
}

// setupTenantSchema creates the identity tables in a tenant schema and returns a context with tenant.
func setupTenantSchema(t *testing.T, db *gorm.DB, tid tenant.TenantID) context.Context {
	t.Helper()
	schemaName := tid.SchemaName()

	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %q", schemaName)).Error
	require.NoError(t, err)

	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q.identity (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		email VARCHAR(255) NOT NULL,
		status VARCHAR(30) NOT NULL DEFAULT 'PENDING_INVITE',
		password_hash VARCHAR(255) NOT NULL DEFAULT '',
		external_idp VARCHAR(100) NOT NULL DEFAULT '',
		external_sub VARCHAR(255) NOT NULL DEFAULT '',
		failed_attempts INT NOT NULL DEFAULT 0,
		version BIGINT NOT NULL DEFAULT 1,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
		deleted_at TIMESTAMP WITH TIME ZONE,
		UNIQUE (email) WHERE deleted_at IS NULL
	)`, schemaName)).Error
	require.NoError(t, err)

	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q.role_assignment (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		identity_id UUID NOT NULL,
		granted_by UUID NOT NULL,
		role VARCHAR(50) NOT NULL,
		expires_at TIMESTAMP WITH TIME ZONE,
		revoked_at TIMESTAMP WITH TIME ZONE,
		revoked_by UUID,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
	)`, schemaName)).Error
	require.NoError(t, err)

	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q.invitation (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		identity_id UUID NOT NULL,
		invited_by UUID NOT NULL,
		token_hash VARCHAR(64) NOT NULL UNIQUE,
		expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
		status VARCHAR(20) NOT NULL DEFAULT 'PENDING',
		created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
	)`, schemaName)).Error
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
