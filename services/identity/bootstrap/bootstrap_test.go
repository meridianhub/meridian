package bootstrap_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/meridianhub/meridian/services/identity/adapters/persistence"
	"github.com/meridianhub/meridian/services/identity/bootstrap"
	"github.com/meridianhub/meridian/services/identity/domain"
	"github.com/meridianhub/meridian/shared/pkg/credentials"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupMasterTenantDB creates a CockroachDB testcontainer and provisions the
// org_meridian_master schema with identity tables.
func setupMasterTenantDB(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	db, cleanup := testdb.SetupCockroachDB(t, nil)
	setupTenantSchema(t, db, tenant.MustNewTenantID(bootstrap.MasterTenantID))
	return db, cleanup
}

// masterCtx returns a context scoped to the meridian_master tenant.
func masterCtx() context.Context {
	return tenant.WithTenant(context.Background(), tenant.MustNewTenantID(bootstrap.MasterTenantID))
}

// setupTenantSchema creates identity tables in a tenant schema.
func setupTenantSchema(t *testing.T, db *gorm.DB, tid tenant.TenantID) {
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
}

// TestBootstrapPlatformAdmin_CreatesAdmin verifies that the platform admin is created
// when the environment variables are set and no admin exists yet.
func TestBootstrapPlatformAdmin_CreatesAdmin(t *testing.T) {
	db, cleanup := setupMasterTenantDB(t)
	defer cleanup()

	t.Setenv("PLATFORM_ADMIN_EMAIL", "admin@example.com")
	t.Setenv("PLATFORM_ADMIN_PASSWORD", "SecurePassword1!")

	repo := persistence.NewRepository(db)
	err := bootstrap.Run(context.Background(), repo)
	require.NoError(t, err)

	identity, err := repo.FindByEmail(masterCtx(), "admin@example.com")
	require.NoError(t, err)
	assert.Equal(t, domain.IdentityStatusActive, identity.Status())
}

// TestBootstrapPlatformAdmin_Idempotent verifies that calling bootstrap twice does not
// create a duplicate admin or return an error.
func TestBootstrapPlatformAdmin_Idempotent(t *testing.T) {
	db, cleanup := setupMasterTenantDB(t)
	defer cleanup()

	t.Setenv("PLATFORM_ADMIN_EMAIL", "admin@example.com")
	t.Setenv("PLATFORM_ADMIN_PASSWORD", "SecurePassword1!")

	repo := persistence.NewRepository(db)

	require.NoError(t, bootstrap.Run(context.Background(), repo))
	// Second call must not fail.
	require.NoError(t, bootstrap.Run(context.Background(), repo))

	// Exactly one identity should exist.
	identities, err := repo.ListByTenant(masterCtx())
	require.NoError(t, err)
	assert.Len(t, identities, 1)
}

// TestBootstrapPlatformAdmin_SkipsWhenEnvVarsEmpty verifies that bootstrap is skipped
// when the required environment variables are not set.
func TestBootstrapPlatformAdmin_SkipsWhenEnvVarsEmpty(t *testing.T) {
	db, cleanup := setupMasterTenantDB(t)
	defer cleanup()

	t.Setenv("PLATFORM_ADMIN_EMAIL", "")
	t.Setenv("PLATFORM_ADMIN_PASSWORD", "")

	repo := persistence.NewRepository(db)
	require.NoError(t, bootstrap.Run(context.Background(), repo))

	identities, err := repo.ListByTenant(masterCtx())
	require.NoError(t, err)
	assert.Empty(t, identities)
}

// TestBootstrapPlatformAdmin_SkipsWhenOnlyEmailSet verifies that bootstrap is skipped
// when only one of the two required env vars is set.
func TestBootstrapPlatformAdmin_SkipsWhenOnlyEmailSet(t *testing.T) {
	db, cleanup := setupMasterTenantDB(t)
	defer cleanup()

	t.Setenv("PLATFORM_ADMIN_EMAIL", "admin@example.com")
	t.Setenv("PLATFORM_ADMIN_PASSWORD", "")

	repo := persistence.NewRepository(db)
	require.NoError(t, bootstrap.Run(context.Background(), repo))

	identities, err := repo.ListByTenant(masterCtx())
	require.NoError(t, err)
	assert.Empty(t, identities)
}

// TestBootstrapPlatformAdmin_PasswordIsHashed verifies that the stored password is
// a bcrypt hash and not the plaintext password.
func TestBootstrapPlatformAdmin_PasswordIsHashed(t *testing.T) {
	db, cleanup := setupMasterTenantDB(t)
	defer cleanup()

	const plaintext = "SecurePassword1!"
	t.Setenv("PLATFORM_ADMIN_EMAIL", "admin@example.com")
	t.Setenv("PLATFORM_ADMIN_PASSWORD", plaintext)

	repo := persistence.NewRepository(db)
	require.NoError(t, bootstrap.Run(context.Background(), repo))

	identity, err := repo.FindByEmail(masterCtx(), "admin@example.com")
	require.NoError(t, err)

	// The stored hash must not equal the plaintext.
	assert.NotEqual(t, plaintext, identity.PasswordHash())

	// The hash must validate correctly against the plaintext.
	require.NoError(t, credentials.ValidatePassword(plaintext, identity.PasswordHash()))
}

// TestBootstrapPlatformAdmin_RolesAssigned verifies that the expected roles are assigned
// to the bootstrapped platform admin.
func TestBootstrapPlatformAdmin_RolesAssigned(t *testing.T) {
	db, cleanup := setupMasterTenantDB(t)
	defer cleanup()

	t.Setenv("PLATFORM_ADMIN_EMAIL", "admin@example.com")
	t.Setenv("PLATFORM_ADMIN_PASSWORD", "SecurePassword1!")

	repo := persistence.NewRepository(db)
	require.NoError(t, bootstrap.Run(context.Background(), repo))

	identity, err := repo.FindByEmail(masterCtx(), "admin@example.com")
	require.NoError(t, err)

	assignments, err := repo.FindRoleAssignments(masterCtx(), identity.ID())
	require.NoError(t, err)
	require.Len(t, assignments, 3)

	roleStrings := make([]string, 0, len(assignments))
	for _, ra := range assignments {
		roleStrings = append(roleStrings, string(ra.Role()))
	}
	assert.Contains(t, roleStrings, "platform-admin")
	assert.Contains(t, roleStrings, "super-admin")
	assert.Contains(t, roleStrings, "tenant-owner")
}

// TestBootstrapPlatformAdmin_ReconcilesMissingRoles verifies that when an admin already
// exists but is missing roles, subsequent bootstrap calls add the missing roles atomically.
func TestBootstrapPlatformAdmin_ReconcilesMissingRoles(t *testing.T) {
	db, cleanup := setupMasterTenantDB(t)
	defer cleanup()

	t.Setenv("PLATFORM_ADMIN_EMAIL", "admin@example.com")
	t.Setenv("PLATFORM_ADMIN_PASSWORD", "SecurePassword1!")

	repo := persistence.NewRepository(db)

	// First call: creates admin with all roles.
	require.NoError(t, bootstrap.Run(context.Background(), repo))

	identity, err := repo.FindByEmail(masterCtx(), "admin@example.com")
	require.NoError(t, err)

	// Manually revoke one role to simulate a partial state.
	assignments, err := repo.FindRoleAssignments(masterCtx(), identity.ID())
	require.NoError(t, err)
	require.NotEmpty(t, assignments)

	require.NoError(t, assignments[0].Revoke(identity.ID()))
	require.NoError(t, repo.SaveRoleAssignment(masterCtx(), assignments[0]))

	// Second call: should reconcile and restore the missing role.
	require.NoError(t, bootstrap.Run(context.Background(), repo))

	// All 3 roles must be active after reconciliation.
	all, err := repo.FindRoleAssignments(masterCtx(), identity.ID())
	require.NoError(t, err)

	activeRoles := make([]string, 0)
	for _, ra := range all {
		if ra.IsActive() {
			activeRoles = append(activeRoles, string(ra.Role()))
		}
	}
	assert.Contains(t, activeRoles, "platform-admin")
	assert.Contains(t, activeRoles, "super-admin")
	assert.Contains(t, activeRoles, "tenant-owner")
}

// --- Tests for ProvisionAdminForTenant ---

// TestProvisionAdminForTenant_CreatesAdminInTenantSchema verifies that calling
// ProvisionAdminForTenant provisions the platform admin in a non-master tenant schema.
func TestProvisionAdminForTenant_CreatesAdminInTenantSchema(t *testing.T) {
	db, cleanup := testdb.SetupCockroachDB(t, nil)
	defer cleanup()

	tenantTID := tenant.MustNewTenantID("acme_corp")
	setupTenantSchema(t, db, tenantTID)

	t.Setenv("PLATFORM_ADMIN_EMAIL", "admin@example.com")
	t.Setenv("PLATFORM_ADMIN_PASSWORD", "SecurePassword1!")

	repo := persistence.NewRepository(db)
	err := bootstrap.ProvisionAdminForTenant(context.Background(), repo, tenantTID)
	require.NoError(t, err)

	tenantCtx := tenant.WithTenant(context.Background(), tenantTID)
	identity, err := repo.FindByEmail(tenantCtx, "admin@example.com")
	require.NoError(t, err)
	assert.Equal(t, domain.IdentityStatusActive, identity.Status())

	assignments, err := repo.FindRoleAssignments(tenantCtx, identity.ID())
	require.NoError(t, err)
	assert.Len(t, assignments, 3)
}

// TestProvisionAdminForTenant_Idempotent verifies that calling the function twice
// for the same tenant does not create duplicates.
func TestProvisionAdminForTenant_Idempotent(t *testing.T) {
	db, cleanup := testdb.SetupCockroachDB(t, nil)
	defer cleanup()

	tenantTID := tenant.MustNewTenantID("acme_corp")
	setupTenantSchema(t, db, tenantTID)

	t.Setenv("PLATFORM_ADMIN_EMAIL", "admin@example.com")
	t.Setenv("PLATFORM_ADMIN_PASSWORD", "SecurePassword1!")

	repo := persistence.NewRepository(db)
	require.NoError(t, bootstrap.ProvisionAdminForTenant(context.Background(), repo, tenantTID))
	require.NoError(t, bootstrap.ProvisionAdminForTenant(context.Background(), repo, tenantTID))

	tenantCtx := tenant.WithTenant(context.Background(), tenantTID)
	identities, err := repo.ListByTenant(tenantCtx)
	require.NoError(t, err)
	assert.Len(t, identities, 1)
}

// TestProvisionAdminForTenant_SkipsWhenEnvVarsEmpty verifies that provisioning
// is skipped when platform admin credentials are not configured.
func TestProvisionAdminForTenant_SkipsWhenEnvVarsEmpty(t *testing.T) {
	db, cleanup := testdb.SetupCockroachDB(t, nil)
	defer cleanup()

	tenantTID := tenant.MustNewTenantID("acme_corp")
	setupTenantSchema(t, db, tenantTID)

	t.Setenv("PLATFORM_ADMIN_EMAIL", "")
	t.Setenv("PLATFORM_ADMIN_PASSWORD", "")

	repo := persistence.NewRepository(db)
	require.NoError(t, bootstrap.ProvisionAdminForTenant(context.Background(), repo, tenantTID))

	tenantCtx := tenant.WithTenant(context.Background(), tenantTID)
	identities, err := repo.ListByTenant(tenantCtx)
	require.NoError(t, err)
	assert.Empty(t, identities)
}

// TestProvisionAdminForTenant_NilRepo verifies that a nil repository returns an error.
func TestProvisionAdminForTenant_NilRepo(t *testing.T) {
	tenantTID := tenant.MustNewTenantID("acme_corp")
	err := bootstrap.ProvisionAdminForTenant(context.Background(), nil, tenantTID)
	assert.ErrorIs(t, err, bootstrap.ErrNilRepository)
}

// TestAsPostProvisioningHook_InvokesProvisionAdmin verifies that the hook
// returned by AsPostProvisioningHook correctly provisions the admin.
func TestAsPostProvisioningHook_InvokesProvisionAdmin(t *testing.T) {
	db, cleanup := testdb.SetupCockroachDB(t, nil)
	defer cleanup()

	tenantTID := tenant.MustNewTenantID("beta_corp")
	setupTenantSchema(t, db, tenantTID)

	t.Setenv("PLATFORM_ADMIN_EMAIL", "admin@example.com")
	t.Setenv("PLATFORM_ADMIN_PASSWORD", "SecurePassword1!")

	repo := persistence.NewRepository(db)
	hook := bootstrap.AsPostProvisioningHook(repo)
	require.NoError(t, hook(context.Background(), tenantTID))

	tenantCtx := tenant.WithTenant(context.Background(), tenantTID)
	identity, err := repo.FindByEmail(tenantCtx, "admin@example.com")
	require.NoError(t, err)
	assert.Equal(t, domain.IdentityStatusActive, identity.Status())
}
