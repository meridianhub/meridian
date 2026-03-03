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

	masterTenantID := tenant.MustNewTenantID(bootstrap.MasterTenantID)
	setupTenantSchema(t, db, masterTenantID)

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

	err := bootstrap.Run(context.Background(), db)
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
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

	err := bootstrap.Run(context.Background(), db)
	require.NoError(t, err)

	// Second call must not fail.
	err = bootstrap.Run(context.Background(), db)
	require.NoError(t, err)

	// Exactly one identity should exist.
	repo := persistence.NewRepository(db)
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

	err := bootstrap.Run(context.Background(), db)
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
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

	err := bootstrap.Run(context.Background(), db)
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
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

	err := bootstrap.Run(context.Background(), db)
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	identity, err := repo.FindByEmail(masterCtx(), "admin@example.com")
	require.NoError(t, err)

	// The stored hash must not equal the plaintext.
	assert.NotEqual(t, plaintext, identity.PasswordHash())

	// The hash must validate correctly against the plaintext.
	err = credentials.ValidatePassword(plaintext, identity.PasswordHash())
	require.NoError(t, err)
}

// TestBootstrapPlatformAdmin_RolesAssigned verifies that the expected roles are assigned
// to the bootstrapped platform admin.
func TestBootstrapPlatformAdmin_RolesAssigned(t *testing.T) {
	db, cleanup := setupMasterTenantDB(t)
	defer cleanup()

	t.Setenv("PLATFORM_ADMIN_EMAIL", "admin@example.com")
	t.Setenv("PLATFORM_ADMIN_PASSWORD", "SecurePassword1!")

	err := bootstrap.Run(context.Background(), db)
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
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
// exists but is missing roles, subsequent bootstrap calls add the missing roles.
func TestBootstrapPlatformAdmin_ReconcilesMissingRoles(t *testing.T) {
	db, cleanup := setupMasterTenantDB(t)
	defer cleanup()

	t.Setenv("PLATFORM_ADMIN_EMAIL", "admin@example.com")
	t.Setenv("PLATFORM_ADMIN_PASSWORD", "SecurePassword1!")

	// First call: creates admin with all roles.
	err := bootstrap.Run(context.Background(), db)
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	identity, err := repo.FindByEmail(masterCtx(), "admin@example.com")
	require.NoError(t, err)

	// Manually revoke one role to simulate a partial state.
	assignments, err := repo.FindRoleAssignments(masterCtx(), identity.ID())
	require.NoError(t, err)
	require.NotEmpty(t, assignments)

	// Revoke the first role.
	require.NoError(t, assignments[0].Revoke(identity.ID()))
	require.NoError(t, repo.SaveRoleAssignment(masterCtx(), assignments[0]))

	// Second call: should reconcile and restore the missing role.
	err = bootstrap.Run(context.Background(), db)
	require.NoError(t, err)

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
