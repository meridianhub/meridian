package service

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/identity/v1"
	"github.com/meridianhub/meridian/services/identity/adapters/persistence"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const integrationTestTenantID = "identity_integration_test_tenant"

// setupIdentityIntegrationTest creates a CockroachDB testcontainer with the
// identity schema and returns a configured Service for integration testing.
func setupIdentityIntegrationTest(t *testing.T) (*Service, *gorm.DB, context.Context, func()) {
	t.Helper()

	db, cleanup := testdb.SetupCockroachDB(t, nil)

	tid := tenant.TenantID(integrationTestTenantID)
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

	ctx := tenant.WithTenant(context.Background(), tid)

	repo := persistence.NewRepository(db)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	svc, err := NewService(repo, logger)
	require.NoError(t, err)

	return svc, db, ctx, cleanup
}

// contextWithIntegrationAuth injects a caller identity and roles into a context.
func contextWithIntegrationAuth(ctx context.Context, callerID uuid.UUID, roles []string) context.Context {
	ctx = context.WithValue(ctx, auth.UserIDContextKey, callerID.String())
	ctx = context.WithValue(ctx, auth.RolesContextKey, roles)
	return ctx
}

// --- CreateIdentity ---

// TestIntegration_CreateIdentity verifies that a new identity is persisted in the DB.
func TestIntegration_CreateIdentity(t *testing.T) {
	svc, _, ctx, cleanup := setupIdentityIntegrationTest(t)
	defer cleanup()

	resp, err := svc.CreateIdentity(ctx, &pb.CreateIdentityRequest{
		Email: "create@integration.test",
	})

	require.NoError(t, err)
	assert.NotEmpty(t, resp.Identity.Id)
	assert.Equal(t, "create@integration.test", resp.Identity.Email)
	assert.Equal(t, pb.IdentityStatus_IDENTITY_STATUS_PENDING_INVITE, resp.Identity.Status)

	// Verify persisted in DB by retrieving
	retrieved, err := svc.RetrieveIdentity(ctx, &pb.RetrieveIdentityRequest{
		Id: resp.Identity.Id,
	})
	require.NoError(t, err)
	assert.Equal(t, resp.Identity.Id, retrieved.Identity.Id)
	assert.Equal(t, "create@integration.test", retrieved.Identity.Email)
}

// TestIntegration_CreateIdentity_DuplicateEmail verifies that a duplicate email
// returns AlreadyExists.
func TestIntegration_CreateIdentity_DuplicateEmail(t *testing.T) {
	svc, _, ctx, cleanup := setupIdentityIntegrationTest(t)
	defer cleanup()

	_, err := svc.CreateIdentity(ctx, &pb.CreateIdentityRequest{
		Email: "duplicate@integration.test",
	})
	require.NoError(t, err)

	_, err = svc.CreateIdentity(ctx, &pb.CreateIdentityRequest{
		Email: "duplicate@integration.test",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already")
}

// --- Authenticate ---

// TestIntegration_Authenticate verifies the full authenticate flow:
// invite user → accept invitation (sets password + activates) → authenticate.
func TestIntegration_Authenticate(t *testing.T) {
	svc, _, ctx, cleanup := setupIdentityIntegrationTest(t)
	defer cleanup()

	inviterID := uuid.New()
	authCtx := contextWithIntegrationAuth(ctx, inviterID, []string{"TENANT_OWNER"})

	// Invite user
	inviteResp, err := svc.InviteUser(authCtx, &pb.InviteUserRequest{
		Email: "auth@integration.test",
	})
	require.NoError(t, err)
	plaintextToken := inviteResp.InvitationToken

	// Accept invitation with password
	_, err = svc.AcceptInvitation(ctx, &pb.AcceptInvitationRequest{
		Token:    plaintextToken,
		Password: "SecurePass123!",
	})
	require.NoError(t, err)

	// Authenticate with correct password
	authResp, err := svc.Authenticate(ctx, &pb.AuthenticateRequest{
		Email:    "auth@integration.test",
		Password: "SecurePass123!",
	})
	require.NoError(t, err)
	assert.True(t, authResp.Authenticated)
	assert.NotNil(t, authResp.Identity)
	assert.Equal(t, "auth@integration.test", authResp.Identity.Email)

	// Authenticate with wrong password
	wrongResp, err := svc.Authenticate(ctx, &pb.AuthenticateRequest{
		Email:    "auth@integration.test",
		Password: "WrongPassword1!",
	})
	require.NoError(t, err)
	assert.False(t, wrongResp.Authenticated)
	assert.Equal(t, pb.AuthenticationFailureReason_AUTHENTICATION_FAILURE_REASON_INVALID_CREDENTIALS, wrongResp.FailureReason)
}

// --- GrantRole ---

// TestIntegration_GrantRole verifies that a role can be assigned and is
// persisted so it appears in ListRoleAssignments.
func TestIntegration_GrantRole(t *testing.T) {
	svc, _, ctx, cleanup := setupIdentityIntegrationTest(t)
	defer cleanup()

	// Create target identity
	createResp, err := svc.CreateIdentity(ctx, &pb.CreateIdentityRequest{
		Email: "roleuser@integration.test",
	})
	require.NoError(t, err)
	identityID := createResp.Identity.Id

	granterID := uuid.New()
	authCtx := contextWithIntegrationAuth(ctx, granterID, []string{"ADMIN"})

	// Grant OPERATOR role
	grantResp, err := svc.GrantRole(authCtx, &pb.GrantRoleRequest{
		IdentityId: identityID,
		Role:       pb.Role_ROLE_OPERATOR,
	})
	require.NoError(t, err)
	assert.Equal(t, identityID, grantResp.RoleAssignment.IdentityId)
	assert.Equal(t, pb.Role_ROLE_OPERATOR, grantResp.RoleAssignment.Role)

	// Verify role persisted
	listResp, err := svc.ListRoleAssignments(ctx, &pb.ListRoleAssignmentsRequest{
		IdentityId: identityID,
	})
	require.NoError(t, err)
	require.Len(t, listResp.RoleAssignments, 1)
	assert.Equal(t, pb.Role_ROLE_OPERATOR, listResp.RoleAssignments[0].Role)
}

// --- InviteUser ---

// TestIntegration_InviteUser verifies that an invitation creates both an identity
// and an invitation record in the DB.
func TestIntegration_InviteUser(t *testing.T) {
	svc, _, ctx, cleanup := setupIdentityIntegrationTest(t)
	defer cleanup()

	inviterID := uuid.New()
	authCtx := contextWithIntegrationAuth(ctx, inviterID, []string{"ADMIN"})

	resp, err := svc.InviteUser(authCtx, &pb.InviteUserRequest{
		Email: "invited@integration.test",
		Role:  pb.Role_ROLE_OPERATOR,
	})

	require.NoError(t, err)
	assert.NotNil(t, resp.Identity)
	assert.NotNil(t, resp.Invitation)
	assert.NotEmpty(t, resp.InvitationToken)
	assert.Equal(t, "invited@integration.test", resp.Identity.Email)
	assert.Equal(t, pb.IdentityStatus_IDENTITY_STATUS_PENDING_INVITE, resp.Identity.Status)

	// Verify identity persisted
	retrieved, err := svc.RetrieveIdentity(ctx, &pb.RetrieveIdentityRequest{
		Id: resp.Identity.Id,
	})
	require.NoError(t, err)
	assert.Equal(t, pb.IdentityStatus_IDENTITY_STATUS_PENDING_INVITE, retrieved.Identity.Status)

	// Verify role assigned
	roles, err := svc.ListRoleAssignments(ctx, &pb.ListRoleAssignmentsRequest{
		IdentityId: resp.Identity.Id,
	})
	require.NoError(t, err)
	require.Len(t, roles.RoleAssignments, 1)
	assert.Equal(t, pb.Role_ROLE_OPERATOR, roles.RoleAssignments[0].Role)
}

// --- AcceptInvitation ---

// TestIntegration_AcceptInvitation verifies the full accept flow: invite → accept
// → identity becomes ACTIVE with password set.
func TestIntegration_AcceptInvitation(t *testing.T) {
	svc, _, ctx, cleanup := setupIdentityIntegrationTest(t)
	defer cleanup()

	inviterID := uuid.New()
	authCtx := contextWithIntegrationAuth(ctx, inviterID, []string{"ADMIN"})

	// Invite user
	inviteResp, err := svc.InviteUser(authCtx, &pb.InviteUserRequest{
		Email: "accept@integration.test",
	})
	require.NoError(t, err)
	plaintextToken := inviteResp.InvitationToken
	identityID := inviteResp.Identity.Id

	// Accept invitation
	acceptResp, err := svc.AcceptInvitation(ctx, &pb.AcceptInvitationRequest{
		Token:    plaintextToken,
		Password: "StrongPassword1!",
	})
	require.NoError(t, err)
	assert.Equal(t, pb.IdentityStatus_IDENTITY_STATUS_ACTIVE, acceptResp.Identity.Status)

	// Verify persisted status
	retrieved, err := svc.RetrieveIdentity(ctx, &pb.RetrieveIdentityRequest{
		Id: identityID,
	})
	require.NoError(t, err)
	assert.Equal(t, pb.IdentityStatus_IDENTITY_STATUS_ACTIVE, retrieved.Identity.Status)

	// Using token a second time must fail
	_, err = svc.AcceptInvitation(ctx, &pb.AcceptInvitationRequest{
		Token:    plaintextToken,
		Password: "AnotherPass1!",
	})
	require.Error(t, err)
}

// --- RequestPasswordReset ---

// TestIntegration_RequestPasswordReset verifies the full reset flow:
// create identity → accept invitation → request reset → use token → new password works.
func TestIntegration_RequestPasswordReset(t *testing.T) {
	svc, _, ctx, cleanup := setupIdentityIntegrationTest(t)
	defer cleanup()

	inviterID := uuid.New()
	authCtx := contextWithIntegrationAuth(ctx, inviterID, []string{"ADMIN"})

	// Invite and activate user
	inviteResp, err := svc.InviteUser(authCtx, &pb.InviteUserRequest{
		Email: "reset@integration.test",
	})
	require.NoError(t, err)

	_, err = svc.AcceptInvitation(ctx, &pb.AcceptInvitationRequest{
		Token:    inviteResp.InvitationToken,
		Password: "OldPassword1!",
	})
	require.NoError(t, err)

	// Request password reset
	resetResp, err := svc.RequestPasswordReset(ctx, &pb.RequestPasswordResetRequest{
		Email: "reset@integration.test",
	})
	require.NoError(t, err)
	assert.Equal(t, "reset@integration.test", resetResp.Email)
	assert.NotEmpty(t, resetResp.ResetToken)

	// Complete password reset
	_, err = svc.CompletePasswordReset(ctx, &pb.CompletePasswordResetRequest{
		ResetToken:  resetResp.ResetToken,
		NewPassword: "NewPassword1!",
	})
	require.NoError(t, err)

	// Authenticate with new password
	authResp, err := svc.Authenticate(ctx, &pb.AuthenticateRequest{
		Email:    "reset@integration.test",
		Password: "NewPassword1!",
	})
	require.NoError(t, err)
	assert.True(t, authResp.Authenticated)

	// Old password must no longer work
	oldAuthResp, err := svc.Authenticate(ctx, &pb.AuthenticateRequest{
		Email:    "reset@integration.test",
		Password: "OldPassword1!",
	})
	require.NoError(t, err)
	assert.False(t, oldAuthResp.Authenticated)
}

// --- LockAccount ---

// TestIntegration_LockAccount verifies that exceeding the failed login attempt
// threshold locks the account so subsequent logins are rejected.
func TestIntegration_LockAccount(t *testing.T) {
	svc, _, ctx, cleanup := setupIdentityIntegrationTest(t)
	defer cleanup()

	inviterID := uuid.New()
	authCtx := contextWithIntegrationAuth(ctx, inviterID, []string{"ADMIN"})

	// Invite and activate user
	inviteResp, err := svc.InviteUser(authCtx, &pb.InviteUserRequest{
		Email: "lockme@integration.test",
	})
	require.NoError(t, err)

	_, err = svc.AcceptInvitation(ctx, &pb.AcceptInvitationRequest{
		Token:    inviteResp.InvitationToken,
		Password: "SecurePass1!",
	})
	require.NoError(t, err)

	// Exhaust failed attempts (maxFailedAttempts = 5 in domain)
	for i := 0; i < 5; i++ {
		resp, err := svc.Authenticate(ctx, &pb.AuthenticateRequest{
			Email:    "lockme@integration.test",
			Password: "WrongPassword1!",
		})
		require.NoError(t, err)
		assert.False(t, resp.Authenticated)
	}

	// Account should now be locked
	lockedResp, err := svc.Authenticate(ctx, &pb.AuthenticateRequest{
		Email:    "lockme@integration.test",
		Password: "SecurePass1!",
	})
	require.NoError(t, err)
	assert.False(t, lockedResp.Authenticated)
	assert.Equal(t, pb.AuthenticationFailureReason_AUTHENTICATION_FAILURE_REASON_ACCOUNT_LOCKED, lockedResp.FailureReason)
}

// --- Full Flow ---

// TestIntegration_FullFlow exercises the complete identity lifecycle:
// create → set password → authenticate → assign roles → verify roles in list.
func TestIntegration_FullFlow(t *testing.T) {
	svc, _, ctx, cleanup := setupIdentityIntegrationTest(t)
	defer cleanup()

	ownerID := uuid.New()
	ownerCtx := contextWithIntegrationAuth(ctx, ownerID, []string{"TENANT_OWNER"})

	// Step 1: Invite user
	inviteResp, err := svc.InviteUser(ownerCtx, &pb.InviteUserRequest{
		Email: "fullflow@integration.test",
	})
	require.NoError(t, err)
	identityID := inviteResp.Identity.Id
	plaintextToken := inviteResp.InvitationToken

	// Step 2: Accept invitation (sets password + activates)
	acceptResp, err := svc.AcceptInvitation(ctx, &pb.AcceptInvitationRequest{
		Token:    plaintextToken,
		Password: "FlowPassword1!",
	})
	require.NoError(t, err)
	assert.Equal(t, pb.IdentityStatus_IDENTITY_STATUS_ACTIVE, acceptResp.Identity.Status)

	// Step 3: Authenticate
	authResp, err := svc.Authenticate(ctx, &pb.AuthenticateRequest{
		Email:    "fullflow@integration.test",
		Password: "FlowPassword1!",
	})
	require.NoError(t, err)
	assert.True(t, authResp.Authenticated)
	assert.Equal(t, identityID, authResp.Identity.Id)

	// Step 4: Assign two roles
	_, err = svc.GrantRole(ownerCtx, &pb.GrantRoleRequest{
		IdentityId: identityID,
		Role:       pb.Role_ROLE_OPERATOR,
	})
	require.NoError(t, err)

	_, err = svc.GrantRole(ownerCtx, &pb.GrantRoleRequest{
		IdentityId: identityID,
		Role:       pb.Role_ROLE_ADMIN,
	})
	require.NoError(t, err)

	// Step 5: Verify roles in list
	listResp, err := svc.ListRoleAssignments(ctx, &pb.ListRoleAssignmentsRequest{
		IdentityId: identityID,
	})
	require.NoError(t, err)
	assert.Len(t, listResp.RoleAssignments, 2)

	roles := make(map[pb.Role]bool)
	for _, ra := range listResp.RoleAssignments {
		roles[ra.Role] = true
	}
	assert.True(t, roles[pb.Role_ROLE_OPERATOR])
	assert.True(t, roles[pb.Role_ROLE_ADMIN])

	// Step 6: Verify identity appears in tenant list
	listIdentResp, err := svc.ListIdentities(ctx, &pb.ListIdentitiesRequest{})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, int(listIdentResp.TotalCount), 1)

	found := false
	for _, id := range listIdentResp.Identities {
		if id.Id == identityID {
			found = true
			break
		}
	}
	assert.True(t, found, "identity should appear in tenant list")
}
