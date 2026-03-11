//go:build integration

package connector_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/identity/adapters/persistence"
	"github.com/meridianhub/meridian/services/identity/connector"
	"github.com/meridianhub/meridian/services/identity/domain"
	"github.com/meridianhub/meridian/shared/pkg/credentials"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// --- Infrastructure helpers ---

const (
	tenantAlpha = "tenant_alpha"
	tenantBravo = "tenant_bravo"
	sharedEmail = "shared@example.com"
	alphaPass   = "AlphaPass123!"
	bravoPass   = "BravoPass456!"
)

// multiTenantInfra holds the test infrastructure for multi-tenant connector tests.
type multiTenantInfra struct {
	db   *gorm.DB
	repo *persistence.Repository
	conn *connector.Connector
	ctxA context.Context // tenant_alpha scoped context
	ctxB context.Context // tenant_bravo scoped context
}

// setupMultiTenantInfra creates a CockroachDB testcontainer with two tenant schemas
// and an identity connector backed by the real repository.
func setupMultiTenantInfra(t *testing.T) *multiTenantInfra {
	t.Helper()

	db, cleanup := testdb.SetupCockroachDB(t, nil)
	t.Cleanup(cleanup)

	// Limit connection pool for test stability.
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	infra := &multiTenantInfra{db: db}

	infra.ctxA = setupIdentitySchema(t, db, tenantAlpha)
	infra.ctxB = setupIdentitySchema(t, db, tenantBravo)

	infra.repo = persistence.NewRepository(db)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	infra.conn, err = connector.New(infra.repo, logger)
	require.NoError(t, err)

	return infra
}

// setupIdentitySchema creates the identity tables in a tenant schema and returns a tenant-scoped context.
func setupIdentitySchema(t *testing.T, db *gorm.DB, tenantID string) context.Context {
	t.Helper()

	tid := tenant.TenantID(tenantID)
	schemaName := tid.SchemaName()

	ddls := []string{
		`CREATE SCHEMA IF NOT EXISTS %q`,
		`CREATE TABLE IF NOT EXISTS %q.identity (
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
		)`,
		`CREATE TABLE IF NOT EXISTS %q.role_assignment (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			identity_id UUID NOT NULL,
			granted_by UUID NOT NULL,
			role VARCHAR(50) NOT NULL,
			expires_at TIMESTAMP WITH TIME ZONE,
			revoked_at TIMESTAMP WITH TIME ZONE,
			revoked_by UUID,
			created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
			updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS %q.invitation (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			identity_id UUID NOT NULL,
			invited_by UUID NOT NULL,
			token_hash VARCHAR(64) NOT NULL UNIQUE,
			expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
			status VARCHAR(20) NOT NULL DEFAULT 'PENDING',
			created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
			updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
		)`,
	}

	for _, ddl := range ddls {
		err := db.Exec(fmt.Sprintf(ddl, schemaName)).Error
		require.NoError(t, err)
	}

	return tenant.WithTenant(context.Background(), tid)
}

// createActiveIdentity creates and persists an active identity with a password in the given tenant context.
func (infra *multiTenantInfra) createActiveIdentity(t *testing.T, ctx context.Context, email, password string) *domain.Identity {
	t.Helper()

	id, err := domain.NewIdentity(email)
	require.NoError(t, err)

	hash, err := credentials.HashPassword(password)
	require.NoError(t, err)
	require.NoError(t, id.SetPassword(hash))
	require.NoError(t, id.Activate())

	err = infra.repo.Save(ctx, id)
	require.NoError(t, err)

	return id
}

// =============================================================================
// Test 1: Same email, different tenants produce different identities
// =============================================================================

func TestMultiTenant_SameEmail_DifferentIdentities(t *testing.T) {
	infra := setupMultiTenantInfra(t)

	// Create the same email in both tenants with different passwords.
	idA := infra.createActiveIdentity(t, infra.ctxA, sharedEmail, alphaPass)
	idB := infra.createActiveIdentity(t, infra.ctxB, sharedEmail, bravoPass)

	// The two identities must have different UUIDs (different rows in different schemas).
	assert.NotEqual(t, idA.ID(), idB.ID(), "same email in different tenants must produce distinct identity IDs")

	// Login in tenant A returns tenant A's identity.
	gotA, validA, err := infra.conn.Login(infra.ctxA, nil, sharedEmail, alphaPass)
	require.NoError(t, err)
	assert.True(t, validA, "tenant A login with correct password should succeed")
	assert.Equal(t, idA.ID().String(), gotA.UserID)
	assert.Equal(t, sharedEmail, gotA.Email)

	// Login in tenant B returns tenant B's identity.
	gotB, validB, err := infra.conn.Login(infra.ctxB, nil, sharedEmail, bravoPass)
	require.NoError(t, err)
	assert.True(t, validB, "tenant B login with correct password should succeed")
	assert.Equal(t, idB.ID().String(), gotB.UserID)
	assert.Equal(t, sharedEmail, gotB.Email)

	// Verify the returned user IDs differ.
	assert.NotEqual(t, gotA.UserID, gotB.UserID, "login results must reference different identities per tenant")
}

// =============================================================================
// Test 2: Correct password in wrong tenant is rejected
// =============================================================================

func TestMultiTenant_CrossTenantPassword_Rejected(t *testing.T) {
	infra := setupMultiTenantInfra(t)

	infra.createActiveIdentity(t, infra.ctxA, sharedEmail, alphaPass)
	infra.createActiveIdentity(t, infra.ctxB, sharedEmail, bravoPass)

	// Tenant A's password must not work in tenant B's context.
	_, valid, err := infra.conn.Login(infra.ctxB, nil, sharedEmail, alphaPass)
	require.NoError(t, err)
	assert.False(t, valid, "tenant A password must not authenticate in tenant B")

	// Tenant B's password must not work in tenant A's context.
	_, valid, err = infra.conn.Login(infra.ctxA, nil, sharedEmail, bravoPass)
	require.NoError(t, err)
	assert.False(t, valid, "tenant B password must not authenticate in tenant A")
}

// =============================================================================
// Test 3: Identity in tenant A is invisible to tenant B
// =============================================================================

func TestMultiTenant_IdentityNotFound_InOtherTenant(t *testing.T) {
	infra := setupMultiTenantInfra(t)

	// Create identity only in tenant A.
	infra.createActiveIdentity(t, infra.ctxA, "only-alpha@example.com", alphaPass)

	// Login attempt in tenant B returns not-found (false, no error).
	_, valid, err := infra.conn.Login(infra.ctxB, nil, "only-alpha@example.com", alphaPass)
	require.NoError(t, err)
	assert.False(t, valid, "identity from tenant A must not be visible in tenant B")
}

// =============================================================================
// Test 4: Missing tenant context returns error (not false)
// =============================================================================

func TestMultiTenant_MissingTenantContext_ReturnsError(t *testing.T) {
	infra := setupMultiTenantInfra(t)

	infra.createActiveIdentity(t, infra.ctxA, "alice@example.com", alphaPass)

	// Login with bare context (no tenant) must return an error.
	_, valid, err := infra.conn.Login(context.Background(), nil, "alice@example.com", alphaPass)
	require.Error(t, err, "login without tenant context must return an error")
	assert.False(t, valid)
}

// =============================================================================
// Test 5: Role assignments are tenant-isolated
// =============================================================================

func TestMultiTenant_RoleIsolation(t *testing.T) {
	infra := setupMultiTenantInfra(t)

	idA := infra.createActiveIdentity(t, infra.ctxA, sharedEmail, alphaPass)
	infra.createActiveIdentity(t, infra.ctxB, sharedEmail, bravoPass)

	// Grant ADMIN role to the identity in tenant A.
	granterID := uuid.New()
	assignment, err := domain.NewRoleAssignment(idA.ID(), granterID, string(domain.RolePlatform), string(domain.RoleAdmin))
	require.NoError(t, err)
	err = infra.repo.SaveRoleAssignment(infra.ctxA, assignment)
	require.NoError(t, err)

	// Login in tenant A should include ADMIN in groups.
	gotA, validA, err := infra.conn.Login(infra.ctxA, nil, sharedEmail, alphaPass)
	require.NoError(t, err)
	require.True(t, validA)
	assert.Contains(t, gotA.Groups, "ADMIN", "tenant A identity should have ADMIN role")

	// Login in tenant B should have no roles (even though same email).
	gotB, validB, err := infra.conn.Login(infra.ctxB, nil, sharedEmail, bravoPass)
	require.NoError(t, err)
	require.True(t, validB)
	assert.Empty(t, gotB.Groups, "tenant B identity should have no roles")
}

// =============================================================================
// Test 6: Account status isolation across tenants
// =============================================================================

func TestMultiTenant_AccountStatusIsolation(t *testing.T) {
	infra := setupMultiTenantInfra(t)

	// Create and activate identity in tenant A, then suspend it.
	idA := infra.createActiveIdentity(t, infra.ctxA, sharedEmail, alphaPass)
	require.NoError(t, idA.Suspend())
	require.NoError(t, infra.repo.Save(infra.ctxA, idA))

	// Create active identity in tenant B with same email.
	infra.createActiveIdentity(t, infra.ctxB, sharedEmail, bravoPass)

	// Tenant A: suspended, login rejected.
	_, valid, err := infra.conn.Login(infra.ctxA, nil, sharedEmail, alphaPass)
	require.NoError(t, err)
	assert.False(t, valid, "suspended account in tenant A should be rejected")

	// Tenant B: active, login succeeds.
	_, valid, err = infra.conn.Login(infra.ctxB, nil, sharedEmail, bravoPass)
	require.NoError(t, err)
	assert.True(t, valid, "active account in tenant B should succeed")
}
