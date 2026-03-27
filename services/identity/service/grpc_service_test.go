package service

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/identity/v1"
	"github.com/meridianhub/meridian/services/identity/domain"
	"github.com/meridianhub/meridian/shared/pkg/credentials"
	"github.com/meridianhub/meridian/shared/pkg/email"
	"github.com/meridianhub/meridian/shared/pkg/tokens"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

var svcTestTID = tenant.MustNewTenantID("test_tenant")

// --- Mock Repository ---

type mockRepository struct {
	identities   map[uuid.UUID]*domain.Identity
	identByEmail map[string]*domain.Identity
	roles        map[uuid.UUID][]*domain.RoleAssignment
	invitations  map[string]*domain.Invitation // keyed by token hash

	saveErr           error
	findByIDErr       error
	findByEmailErr    error
	listByTenantErr   error
	saveRoleErr       error
	findRolesErr      error
	saveInvitationErr error
	findInvitationErr error
}

func newMockRepository() *mockRepository {
	return &mockRepository{
		identities:   make(map[uuid.UUID]*domain.Identity),
		identByEmail: make(map[string]*domain.Identity),
		roles:        make(map[uuid.UUID][]*domain.RoleAssignment),
		invitations:  make(map[string]*domain.Invitation),
	}
}

func (m *mockRepository) Save(_ context.Context, identity *domain.Identity) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	m.identities[identity.ID()] = identity
	m.identByEmail[identity.Email()] = identity
	return nil
}

func (m *mockRepository) FindByID(_ context.Context, id uuid.UUID) (*domain.Identity, error) {
	if m.findByIDErr != nil {
		return nil, m.findByIDErr
	}
	identity, ok := m.identities[id]
	if !ok {
		return nil, domain.ErrIdentityNotFound
	}
	return identity, nil
}

func (m *mockRepository) FindByEmail(_ context.Context, email string) (*domain.Identity, error) {
	if m.findByEmailErr != nil {
		return nil, m.findByEmailErr
	}
	identity, ok := m.identByEmail[email]
	if !ok {
		return nil, domain.ErrIdentityNotFound
	}
	return identity, nil
}

func (m *mockRepository) ListByTenant(_ context.Context) ([]*domain.Identity, error) {
	if m.listByTenantErr != nil {
		return nil, m.listByTenantErr
	}
	result := make([]*domain.Identity, 0, len(m.identities))
	for _, v := range m.identities {
		result = append(result, v)
	}
	return result, nil
}

func (m *mockRepository) SaveRoleAssignment(_ context.Context, assignment *domain.RoleAssignment) error {
	if m.saveRoleErr != nil {
		return m.saveRoleErr
	}
	m.roles[assignment.IdentityID()] = append(m.roles[assignment.IdentityID()], assignment)
	return nil
}

func (m *mockRepository) FindRoleAssignments(_ context.Context, identityID uuid.UUID) ([]*domain.RoleAssignment, error) {
	if m.findRolesErr != nil {
		return nil, m.findRolesErr
	}
	return m.roles[identityID], nil
}

func (m *mockRepository) SaveIdentityWithInvitation(_ context.Context, identity *domain.Identity, invitation *domain.Invitation) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	if m.saveInvitationErr != nil {
		return m.saveInvitationErr
	}
	m.identities[identity.ID()] = identity
	m.identByEmail[identity.Email()] = identity
	m.invitations[invitation.TokenHash()] = invitation
	return nil
}

func (m *mockRepository) SaveIdentityWithRoles(_ context.Context, identity *domain.Identity, roles []*domain.RoleAssignment) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	m.identities[identity.ID()] = identity
	m.identByEmail[identity.Email()] = identity
	for _, ra := range roles {
		m.roles[ra.IdentityID()] = append(m.roles[ra.IdentityID()], ra)
	}
	return nil
}

func (m *mockRepository) SaveRoleAssignments(_ context.Context, assignments []*domain.RoleAssignment) error {
	if m.saveRoleErr != nil {
		return m.saveRoleErr
	}
	for _, ra := range assignments {
		m.roles[ra.IdentityID()] = append(m.roles[ra.IdentityID()], ra)
	}
	return nil
}

func (m *mockRepository) SaveInvitation(_ context.Context, invitation *domain.Invitation) error {
	if m.saveInvitationErr != nil {
		return m.saveInvitationErr
	}
	m.invitations[invitation.TokenHash()] = invitation
	return nil
}

func (m *mockRepository) FindInvitationByTokenHash(_ context.Context, tokenHash string) (*domain.Invitation, error) {
	if m.findInvitationErr != nil {
		return nil, m.findInvitationErr
	}
	inv, ok := m.invitations[tokenHash]
	if !ok {
		return nil, domain.ErrInvitationNotFound
	}
	return inv, nil
}

func (m *mockRepository) SaveVerificationToken(_ context.Context, _ *domain.VerificationToken) error {
	return nil
}

func (m *mockRepository) FindVerificationTokenByHash(_ context.Context, _ string) (*domain.VerificationToken, error) {
	return nil, domain.ErrVerificationTokenNotFound
}

func (m *mockRepository) CountVerificationTokensInWindow(_ context.Context, _ uuid.UUID, _ time.Duration) (int, error) {
	return 0, nil
}

func (m *mockRepository) SavePasswordResetToken(_ context.Context, _ *domain.PasswordResetToken) error {
	return nil
}

func (m *mockRepository) FindPasswordResetTokenByHash(_ context.Context, _ string) (*domain.PasswordResetToken, error) {
	return nil, domain.ErrPasswordResetTokenNotFound
}

func (m *mockRepository) CountPasswordResetTokensInWindow(_ context.Context, _ uuid.UUID, _ time.Duration) (int, error) {
	return 0, nil
}

func (m *mockRepository) MarkPasswordResetTokensConsumedForIdentity(_ context.Context, _ uuid.UUID) error {
	return nil
}

// addIdentity is a test helper to insert an identity directly into the mock.
func (m *mockRepository) addIdentity(identity *domain.Identity) {
	m.identities[identity.ID()] = identity
	m.identByEmail[identity.Email()] = identity
}

// addInvitation is a test helper to insert an invitation directly into the mock.
func (m *mockRepository) addInvitation(inv *domain.Invitation) {
	m.invitations[inv.TokenHash()] = inv
}

// --- Test Helpers ---

func newTestService(t *testing.T) (*Service, *mockRepository) {
	t.Helper()
	repo := newMockRepository()
	svc, err := NewService(repo, slog.Default())
	require.NoError(t, err)
	return svc, repo
}

func contextWithAuth(callerID uuid.UUID, roles []string) context.Context {
	ctx := tenant.WithTenant(context.Background(), svcTestTID)
	ctx = context.WithValue(ctx, auth.UserIDContextKey, callerID.String())
	ctx = context.WithValue(ctx, auth.RolesContextKey, roles)
	return ctx
}

func makeActiveIdentity(t *testing.T, email, password string) *domain.Identity {
	t.Helper()
	identity, err := domain.NewIdentity(svcTestTID, email)
	require.NoError(t, err)

	hash, err := credentials.HashPassword(password)
	require.NoError(t, err)
	require.NoError(t, identity.SetPassword(hash))
	require.NoError(t, identity.Activate())
	return identity
}

// --- NewService Tests ---

func TestNewService_NilRepository(t *testing.T) {
	_, err := NewService(nil, nil)
	assert.ErrorIs(t, err, ErrRepositoryNil)
}

func TestNewService_NilLogger(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewService(repo, nil)
	require.NoError(t, err)
	assert.NotNil(t, svc.logger)
}

// --- CreateIdentity Tests ---

func TestCreateIdentity_Success(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	resp, err := svc.CreateIdentity(ctx, &pb.CreateIdentityRequest{
		Email: "test@example.com",
	})

	require.NoError(t, err)
	assert.NotEmpty(t, resp.Identity.Id)
	assert.Equal(t, "test@example.com", resp.Identity.Email)
	assert.Equal(t, pb.IdentityStatus_IDENTITY_STATUS_PENDING_INVITE, resp.Identity.Status)
	assert.Len(t, repo.identities, 1)
}

func TestCreateIdentity_InvalidEmail(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	_, err := svc.CreateIdentity(ctx, &pb.CreateIdentityRequest{
		Email: "not-an-email",
	})

	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestCreateIdentity_DuplicateEmail(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)
	repo.saveErr = domain.ErrEmailAlreadyExists

	_, err := svc.CreateIdentity(ctx, &pb.CreateIdentityRequest{
		Email: "test@example.com",
	})

	require.Error(t, err)
	assert.Equal(t, codes.AlreadyExists, status.Code(err))
}

// --- RetrieveIdentity Tests ---

func TestRetrieveIdentity_Success(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	identity, err := domain.NewIdentity(svcTestTID, "test@example.com")
	require.NoError(t, err)
	repo.addIdentity(identity)

	resp, err := svc.RetrieveIdentity(ctx, &pb.RetrieveIdentityRequest{
		Id: identity.ID().String(),
	})

	require.NoError(t, err)
	assert.Equal(t, identity.ID().String(), resp.Identity.Id)
	assert.Equal(t, "test@example.com", resp.Identity.Email)
}

func TestRetrieveIdentity_NotFound(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	_, err := svc.RetrieveIdentity(ctx, &pb.RetrieveIdentityRequest{
		Id: uuid.New().String(),
	})

	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestRetrieveIdentity_InvalidID(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	_, err := svc.RetrieveIdentity(ctx, &pb.RetrieveIdentityRequest{
		Id: "not-a-uuid",
	})

	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

// --- UpdateIdentity Tests ---

func TestUpdateIdentity_Success(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	identity, err := domain.NewIdentity(svcTestTID, "test@example.com")
	require.NoError(t, err)
	repo.addIdentity(identity)

	resp, err := svc.UpdateIdentity(ctx, &pb.UpdateIdentityRequest{
		Id:      identity.ID().String(),
		Version: int32(identity.Version()),
	})

	require.NoError(t, err)
	assert.Equal(t, identity.ID().String(), resp.Identity.Id)
}

func TestUpdateIdentity_VersionConflict(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	identity, err := domain.NewIdentity(svcTestTID, "test@example.com")
	require.NoError(t, err)
	repo.addIdentity(identity)

	_, err = svc.UpdateIdentity(ctx, &pb.UpdateIdentityRequest{
		Id:      identity.ID().String(),
		Version: 999,
	})

	require.Error(t, err)
	assert.Equal(t, codes.Aborted, status.Code(err))
}

// --- ListIdentities Tests ---

func TestListIdentities_Success(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	identity1, err := domain.NewIdentity(svcTestTID, "user1@example.com")
	require.NoError(t, err)
	identity2, err := domain.NewIdentity(svcTestTID, "user2@example.com")
	require.NoError(t, err)
	repo.addIdentity(identity1)
	repo.addIdentity(identity2)

	resp, err := svc.ListIdentities(ctx, &pb.ListIdentitiesRequest{})

	require.NoError(t, err)
	assert.Len(t, resp.Identities, 2)
	assert.Equal(t, int32(2), resp.TotalCount)
}

func TestListIdentities_Empty(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	resp, err := svc.ListIdentities(ctx, &pb.ListIdentitiesRequest{})

	require.NoError(t, err)
	assert.Empty(t, resp.Identities)
	assert.Equal(t, int32(0), resp.TotalCount)
}

// --- Authenticate Tests ---

func TestAuthenticate_Success(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	password := "SecurePass123!"
	identity := makeActiveIdentity(t, "test@example.com", password)
	repo.addIdentity(identity)

	resp, err := svc.Authenticate(ctx, &pb.AuthenticateRequest{
		Email:    "test@example.com",
		Password: password,
	})

	require.NoError(t, err)
	assert.True(t, resp.Authenticated)
	assert.NotNil(t, resp.Identity)
	assert.Equal(t, identity.ID().String(), resp.Identity.Id)
}

func TestAuthenticate_WrongPassword(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	identity := makeActiveIdentity(t, "test@example.com", "SecurePass123!")
	repo.addIdentity(identity)

	resp, err := svc.Authenticate(ctx, &pb.AuthenticateRequest{
		Email:    "test@example.com",
		Password: "WrongPassword1!",
	})

	require.NoError(t, err)
	assert.False(t, resp.Authenticated)
	assert.Equal(t, pb.AuthenticationFailureReason_AUTHENTICATION_FAILURE_REASON_INVALID_CREDENTIALS, resp.FailureReason)
}

func TestAuthenticate_UserNotFound(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	resp, err := svc.Authenticate(ctx, &pb.AuthenticateRequest{
		Email:    "missing@example.com",
		Password: "anything",
	})

	require.NoError(t, err)
	assert.False(t, resp.Authenticated)
	assert.Equal(t, pb.AuthenticationFailureReason_AUTHENTICATION_FAILURE_REASON_INVALID_CREDENTIALS, resp.FailureReason)
}

func TestAuthenticate_AccountLocked(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	identity := makeActiveIdentity(t, "test@example.com", "SecurePass123!")
	// Simulate lockout by recording 5 failed attempts
	for i := 0; i < 5; i++ {
		_ = identity.RecordLoginAttempt(false)
	}
	repo.addIdentity(identity)

	resp, err := svc.Authenticate(ctx, &pb.AuthenticateRequest{
		Email:    "test@example.com",
		Password: "SecurePass123!",
	})

	require.NoError(t, err)
	assert.False(t, resp.Authenticated)
	assert.Equal(t, pb.AuthenticationFailureReason_AUTHENTICATION_FAILURE_REASON_ACCOUNT_LOCKED, resp.FailureReason)
}

func TestAuthenticate_AccountSuspended(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	identity := makeActiveIdentity(t, "test@example.com", "SecurePass123!")
	require.NoError(t, identity.Suspend())
	repo.addIdentity(identity)

	resp, err := svc.Authenticate(ctx, &pb.AuthenticateRequest{
		Email:    "test@example.com",
		Password: "SecurePass123!",
	})

	require.NoError(t, err)
	assert.False(t, resp.Authenticated)
	assert.Equal(t, pb.AuthenticationFailureReason_AUTHENTICATION_FAILURE_REASON_ACCOUNT_NOT_ACTIVE, resp.FailureReason)
}

func TestAuthenticate_FailedAttemptsIncrement(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	identity := makeActiveIdentity(t, "test@example.com", "SecurePass123!")
	repo.addIdentity(identity)

	_, err := svc.Authenticate(ctx, &pb.AuthenticateRequest{
		Email:    "test@example.com",
		Password: "WrongPassword1!",
	})
	require.NoError(t, err)

	// Verify failed attempts incremented
	stored := repo.identities[identity.ID()]
	assert.Equal(t, 1, stored.FailedAttempts())
}

// --- SetPassword Tests ---

func TestSetPassword_Success(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	identity, err := domain.NewIdentity(svcTestTID, "test@example.com")
	require.NoError(t, err)
	repo.addIdentity(identity)

	inv, plaintext, err := domain.NewInvitation(identity.ID(), uuid.New())
	require.NoError(t, err)
	repo.addInvitation(inv)

	resp, err := svc.SetPassword(ctx, &pb.SetPasswordRequest{
		Token:    plaintext,
		Password: "NewSecurePass1!",
	})

	require.NoError(t, err)
	assert.Equal(t, identity.ID().String(), resp.IdentityId)

	// Verify identity is now active
	stored := repo.identities[identity.ID()]
	assert.Equal(t, domain.IdentityStatusActive, stored.Status())
	assert.NotEmpty(t, stored.PasswordHash())
}

func TestSetPassword_InvalidToken(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	_, err := svc.SetPassword(ctx, &pb.SetPasswordRequest{
		Token:    "invalid-token",
		Password: "NewSecurePass1!",
	})

	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestSetPassword_WeakPassword(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	_, err := svc.SetPassword(ctx, &pb.SetPasswordRequest{
		Token:    "any-token",
		Password: "weak",
	})

	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

// --- ChangePassword Tests ---

func TestChangePassword_Success(t *testing.T) {
	svc, repo := newTestService(t)

	password := "OldSecurePass1!"
	identity := makeActiveIdentity(t, "test@example.com", password)
	repo.addIdentity(identity)

	ctx := contextWithAuth(identity.ID(), []string{"ADMIN"})

	resp, err := svc.ChangePassword(ctx, &pb.ChangePasswordRequest{
		CurrentPassword: password,
		NewPassword:     "NewSecurePass1!",
	})

	require.NoError(t, err)
	assert.Equal(t, identity.ID().String(), resp.IdentityId)
}

func TestChangePassword_WrongCurrentPassword(t *testing.T) {
	svc, repo := newTestService(t)

	identity := makeActiveIdentity(t, "test@example.com", "OldSecurePass1!")
	repo.addIdentity(identity)

	ctx := contextWithAuth(identity.ID(), []string{"ADMIN"})

	_, err := svc.ChangePassword(ctx, &pb.ChangePasswordRequest{
		CurrentPassword: "WrongPassword1!",
		NewPassword:     "NewSecurePass1!",
	})

	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestChangePassword_NoAuthContext(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	_, err := svc.ChangePassword(ctx, &pb.ChangePasswordRequest{
		CurrentPassword: "old",
		NewPassword:     "NewSecurePass1!",
	})

	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

// --- RequestPasswordReset Tests ---

func TestRequestPasswordReset_Success(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	identity := makeActiveIdentity(t, "test@example.com", "SecurePass123!")
	repo.addIdentity(identity)

	resp, err := svc.RequestPasswordReset(ctx, &pb.RequestPasswordResetRequest{
		Email: "test@example.com",
	})

	require.NoError(t, err)
	assert.Equal(t, "test@example.com", resp.Email)
}

func TestRequestPasswordReset_UnknownEmail(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	// Should still return success to prevent email enumeration
	resp, err := svc.RequestPasswordReset(ctx, &pb.RequestPasswordResetRequest{
		Email: "unknown@example.com",
	})

	require.NoError(t, err)
	assert.Equal(t, "unknown@example.com", resp.Email)
}

// --- CompletePasswordReset Tests ---

func TestCompletePasswordReset_Success(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	identity := makeActiveIdentity(t, "test@example.com", "OldPassword123!")
	repo.addIdentity(identity)

	inv, plaintext, err := domain.NewInvitation(identity.ID(), identity.ID())
	require.NoError(t, err)
	repo.addInvitation(inv)

	resp, err := svc.CompletePasswordReset(ctx, &pb.CompletePasswordResetRequest{
		ResetToken:  plaintext,
		NewPassword: "NewSecurePass1!",
	})

	require.NoError(t, err)
	assert.Equal(t, identity.ID().String(), resp.IdentityId)
}

func TestCompletePasswordReset_InvalidToken(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	_, err := svc.CompletePasswordReset(ctx, &pb.CompletePasswordResetRequest{
		ResetToken:  "bad-token",
		NewPassword: "NewSecurePass1!",
	})

	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestCompletePasswordReset_ExpiredToken(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	identity := makeActiveIdentity(t, "test@example.com", "OldPassword123!")
	repo.addIdentity(identity)

	// Create an already-expired invitation
	expiredInv := domain.ReconstructInvitation(
		uuid.New(),
		identity.ID(),
		identity.ID(),
		tokens.HashToken("expired-token"),
		time.Now().Add(-1*time.Hour), // expired 1 hour ago
		domain.InvitationStatusPending,
		time.Now().Add(-2*time.Hour),
		time.Now().Add(-2*time.Hour),
	)
	repo.addInvitation(expiredInv)

	_, err := svc.CompletePasswordReset(ctx, &pb.CompletePasswordResetRequest{
		ResetToken:  "expired-token",
		NewPassword: "NewSecurePass1!",
	})

	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

// --- GrantRole Tests ---

func TestGrantRole_Success(t *testing.T) {
	svc, repo := newTestService(t)

	granterID := uuid.New()
	targetIdentity, err := domain.NewIdentity(svcTestTID, "target@example.com")
	require.NoError(t, err)
	repo.addIdentity(targetIdentity)

	// ADMIN (level 3) can grant OPERATOR (level 2)
	ctx := contextWithAuth(granterID, []string{"ADMIN"})

	resp, err := svc.GrantRole(ctx, &pb.GrantRoleRequest{
		IdentityId: targetIdentity.ID().String(),
		Role:       pb.Role_ROLE_OPERATOR,
	})

	require.NoError(t, err)
	assert.NotNil(t, resp.RoleAssignment)
	assert.Equal(t, targetIdentity.ID().String(), resp.RoleAssignment.IdentityId)
	assert.Equal(t, pb.Role_ROLE_OPERATOR, resp.RoleAssignment.Role)
}

func TestGrantRole_InsufficientPermissions(t *testing.T) {
	svc, repo := newTestService(t)

	granterID := uuid.New()
	targetIdentity, err := domain.NewIdentity(svcTestTID, "target@example.com")
	require.NoError(t, err)
	repo.addIdentity(targetIdentity)

	// OPERATOR (level 2) cannot grant ADMIN (level 3)
	ctx := contextWithAuth(granterID, []string{"OPERATOR"})

	_, err = svc.GrantRole(ctx, &pb.GrantRoleRequest{
		IdentityId: targetIdentity.ID().String(),
		Role:       pb.Role_ROLE_ADMIN,
	})

	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestGrantRole_IdentityNotFound(t *testing.T) {
	svc, _ := newTestService(t)

	ctx := contextWithAuth(uuid.New(), []string{"ADMIN"})

	_, err := svc.GrantRole(ctx, &pb.GrantRoleRequest{
		IdentityId: uuid.New().String(),
		Role:       pb.Role_ROLE_OPERATOR,
	})

	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestGrantRole_NoAuthContext(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	_, err := svc.GrantRole(ctx, &pb.GrantRoleRequest{
		IdentityId: uuid.New().String(),
		Role:       pb.Role_ROLE_OPERATOR,
	})

	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

// --- RevokeRole Tests ---

func TestRevokeRole_Success(t *testing.T) {
	svc, repo := newTestService(t)

	granterID := uuid.New()
	targetIdentity, err := domain.NewIdentity(svcTestTID, "target@example.com")
	require.NoError(t, err)
	repo.addIdentity(targetIdentity)

	assignment, err := domain.NewRoleAssignment(svcTestTID, targetIdentity.ID(), granterID, "ADMIN", "OPERATOR")
	require.NoError(t, err)
	repo.roles[targetIdentity.ID()] = []*domain.RoleAssignment{assignment}

	revokerID := uuid.New()
	ctx := contextWithAuth(revokerID, []string{"ADMIN"})

	resp, err := svc.RevokeRole(ctx, &pb.RevokeRoleRequest{
		IdentityId:       targetIdentity.ID().String(),
		RoleAssignmentId: assignment.ID().String(),
	})

	require.NoError(t, err)
	assert.True(t, resp.RoleAssignment.Revoked)
	assert.NotNil(t, resp.RoleAssignment.RevokedAt)
}

func TestRevokeRole_AssignmentNotFound(t *testing.T) {
	svc, repo := newTestService(t)

	targetIdentity, err := domain.NewIdentity(svcTestTID, "target@example.com")
	require.NoError(t, err)
	repo.addIdentity(targetIdentity)

	ctx := contextWithAuth(uuid.New(), []string{"ADMIN"})

	_, err = svc.RevokeRole(ctx, &pb.RevokeRoleRequest{
		IdentityId:       targetIdentity.ID().String(),
		RoleAssignmentId: uuid.New().String(),
	})

	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestRevokeRole_InsufficientPermissions(t *testing.T) {
	svc, repo := newTestService(t)

	granterID := uuid.New()
	targetIdentity, err := domain.NewIdentity(svcTestTID, "target@example.com")
	require.NoError(t, err)
	repo.addIdentity(targetIdentity)

	// Assignment grants OPERATOR role
	assignment, err := domain.NewRoleAssignment(svcTestTID, targetIdentity.ID(), granterID, "ADMIN", "OPERATOR")
	require.NoError(t, err)
	repo.roles[targetIdentity.ID()] = []*domain.RoleAssignment{assignment}

	// VIEWER (level 1) cannot revoke OPERATOR (level 2)
	revokerID := uuid.New()
	ctx := contextWithAuth(revokerID, []string{"VIEWER"})

	_, err = svc.RevokeRole(ctx, &pb.RevokeRoleRequest{
		IdentityId:       targetIdentity.ID().String(),
		RoleAssignmentId: assignment.ID().String(),
	})

	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestRevokeRole_AlreadyRevoked(t *testing.T) {
	svc, repo := newTestService(t)

	granterID := uuid.New()
	targetIdentity, err := domain.NewIdentity(svcTestTID, "target@example.com")
	require.NoError(t, err)
	repo.addIdentity(targetIdentity)

	assignment, err := domain.NewRoleAssignment(svcTestTID, targetIdentity.ID(), granterID, "ADMIN", "OPERATOR")
	require.NoError(t, err)
	require.NoError(t, assignment.Revoke(granterID)) // already revoked
	repo.roles[targetIdentity.ID()] = []*domain.RoleAssignment{assignment}

	ctx := contextWithAuth(uuid.New(), []string{"ADMIN"})

	_, err = svc.RevokeRole(ctx, &pb.RevokeRoleRequest{
		IdentityId:       targetIdentity.ID().String(),
		RoleAssignmentId: assignment.ID().String(),
	})

	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

// --- ListRoleAssignments Tests ---

func TestListRoleAssignments_Success(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	targetIdentity, err := domain.NewIdentity(svcTestTID, "target@example.com")
	require.NoError(t, err)
	repo.addIdentity(targetIdentity)

	assignment1, err := domain.NewRoleAssignment(svcTestTID, targetIdentity.ID(), uuid.New(), "ADMIN", "OPERATOR")
	require.NoError(t, err)
	assignment2, err := domain.NewRoleAssignment(svcTestTID, targetIdentity.ID(), uuid.New(), "ADMIN", "VIEWER")
	require.NoError(t, err)
	repo.roles[targetIdentity.ID()] = []*domain.RoleAssignment{assignment1, assignment2}

	resp, err := svc.ListRoleAssignments(ctx, &pb.ListRoleAssignmentsRequest{
		IdentityId: targetIdentity.ID().String(),
	})

	require.NoError(t, err)
	assert.Len(t, resp.RoleAssignments, 2)
}

func TestListRoleAssignments_ExcludeRevoked(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	targetIdentity, err := domain.NewIdentity(svcTestTID, "target@example.com")
	require.NoError(t, err)
	repo.addIdentity(targetIdentity)

	active, err := domain.NewRoleAssignment(svcTestTID, targetIdentity.ID(), uuid.New(), "ADMIN", "OPERATOR")
	require.NoError(t, err)
	revoked, err := domain.NewRoleAssignment(svcTestTID, targetIdentity.ID(), uuid.New(), "ADMIN", "VIEWER")
	require.NoError(t, err)
	require.NoError(t, revoked.Revoke(uuid.New()))
	repo.roles[targetIdentity.ID()] = []*domain.RoleAssignment{active, revoked}

	resp, err := svc.ListRoleAssignments(ctx, &pb.ListRoleAssignmentsRequest{
		IdentityId:     targetIdentity.ID().String(),
		IncludeRevoked: false,
	})

	require.NoError(t, err)
	assert.Len(t, resp.RoleAssignments, 1)
}

func TestListRoleAssignments_IncludeRevoked(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	targetIdentity, err := domain.NewIdentity(svcTestTID, "target@example.com")
	require.NoError(t, err)
	repo.addIdentity(targetIdentity)

	active, err := domain.NewRoleAssignment(svcTestTID, targetIdentity.ID(), uuid.New(), "ADMIN", "OPERATOR")
	require.NoError(t, err)
	revoked, err := domain.NewRoleAssignment(svcTestTID, targetIdentity.ID(), uuid.New(), "ADMIN", "VIEWER")
	require.NoError(t, err)
	require.NoError(t, revoked.Revoke(uuid.New()))
	repo.roles[targetIdentity.ID()] = []*domain.RoleAssignment{active, revoked}

	resp, err := svc.ListRoleAssignments(ctx, &pb.ListRoleAssignmentsRequest{
		IdentityId:     targetIdentity.ID().String(),
		IncludeRevoked: true,
	})

	require.NoError(t, err)
	assert.Len(t, resp.RoleAssignments, 2)
}

// --- InviteUser Tests ---

func TestInviteUser_Success(t *testing.T) {
	svc, repo := newTestService(t)

	inviterID := uuid.New()
	ctx := contextWithAuth(inviterID, []string{"ADMIN"})

	resp, err := svc.InviteUser(ctx, &pb.InviteUserRequest{
		Email: "newuser@example.com",
		Role:  pb.Role_ROLE_OPERATOR,
	})

	require.NoError(t, err)
	assert.NotNil(t, resp.Identity)
	assert.NotNil(t, resp.Invitation)
	assert.Equal(t, "newuser@example.com", resp.Identity.Email)
	assert.Equal(t, pb.IdentityStatus_IDENTITY_STATUS_PENDING_INVITE, resp.Identity.Status)
	assert.Len(t, repo.identities, 1)
	assert.Len(t, repo.invitations, 1)
}

func TestInviteUser_DuplicateEmail(t *testing.T) {
	svc, repo := newTestService(t)
	repo.saveErr = domain.ErrEmailAlreadyExists

	ctx := contextWithAuth(uuid.New(), []string{"ADMIN"})

	_, err := svc.InviteUser(ctx, &pb.InviteUserRequest{
		Email: "existing@example.com",
		Role:  pb.Role_ROLE_OPERATOR,
	})

	require.Error(t, err)
	assert.Equal(t, codes.AlreadyExists, status.Code(err))
}

func TestInviteUser_NoAuthContext(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	_, err := svc.InviteUser(ctx, &pb.InviteUserRequest{
		Email: "newuser@example.com",
		Role:  pb.Role_ROLE_OPERATOR,
	})

	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

// --- AcceptInvitation Tests ---

func TestAcceptInvitation_Success(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	identity, err := domain.NewIdentity(svcTestTID, "invited@example.com")
	require.NoError(t, err)
	repo.addIdentity(identity)

	inv, plaintext, err := domain.NewInvitation(identity.ID(), uuid.New())
	require.NoError(t, err)
	repo.addInvitation(inv)

	resp, err := svc.AcceptInvitation(ctx, &pb.AcceptInvitationRequest{
		Token:    plaintext,
		Password: "SecureNewPass1!",
	})

	require.NoError(t, err)
	assert.NotNil(t, resp.Identity)
	assert.Equal(t, pb.IdentityStatus_IDENTITY_STATUS_ACTIVE, resp.Identity.Status)
}

func TestAcceptInvitation_InvalidToken(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	_, err := svc.AcceptInvitation(ctx, &pb.AcceptInvitationRequest{
		Token:    "bad-token",
		Password: "SecureNewPass1!",
	})

	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestAcceptInvitation_WeakPassword(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	_, err := svc.AcceptInvitation(ctx, &pb.AcceptInvitationRequest{
		Token:    "any-token",
		Password: "short",
	})

	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestAcceptInvitation_ExpiredInvitation(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	identity, err := domain.NewIdentity(svcTestTID, "invited@example.com")
	require.NoError(t, err)
	repo.addIdentity(identity)

	expiredInv := domain.ReconstructInvitation(
		uuid.New(),
		identity.ID(),
		uuid.New(),
		tokens.HashToken("expired-token"),
		time.Now().Add(-1*time.Hour),
		domain.InvitationStatusPending,
		time.Now().Add(-2*time.Hour),
		time.Now().Add(-2*time.Hour),
	)
	repo.addInvitation(expiredInv)

	_, err = svc.AcceptInvitation(ctx, &pb.AcceptInvitationRequest{
		Token:    "expired-token",
		Password: "SecureNewPass1!",
	})

	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

// --- SuspendIdentity Tests ---

func TestSuspendIdentity_Success(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := contextWithAuth(uuid.New(), []string{"ADMIN"})

	identity := makeActiveIdentity(t, "test@example.com", "SecurePass123!")
	repo.addIdentity(identity)

	resp, err := svc.SuspendIdentity(ctx, &pb.SuspendIdentityRequest{
		Id:     identity.ID().String(),
		Reason: "policy violation",
	})

	require.NoError(t, err)
	assert.Equal(t, pb.IdentityStatus_IDENTITY_STATUS_SUSPENDED, resp.Identity.Status)
}

func TestSuspendIdentity_NoAuthContext(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	_, err := svc.SuspendIdentity(ctx, &pb.SuspendIdentityRequest{
		Id:     uuid.New().String(),
		Reason: "test",
	})

	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestSuspendIdentity_InsufficientPermissions(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := contextWithAuth(uuid.New(), []string{"VIEWER"})

	identity := makeActiveIdentity(t, "test@example.com", "SecurePass123!")
	repo.addIdentity(identity)

	_, err := svc.SuspendIdentity(ctx, &pb.SuspendIdentityRequest{
		Id:     identity.ID().String(),
		Reason: "test",
	})

	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestSuspendIdentity_NotActive(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := contextWithAuth(uuid.New(), []string{"ADMIN"})

	identity, err := domain.NewIdentity(svcTestTID, "test@example.com")
	require.NoError(t, err)
	// identity is in PENDING_INVITE status, cannot be suspended
	repo.addIdentity(identity)

	_, err = svc.SuspendIdentity(ctx, &pb.SuspendIdentityRequest{
		Id:     identity.ID().String(),
		Reason: "test",
	})

	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestSuspendIdentity_NotFound(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := contextWithAuth(uuid.New(), []string{"ADMIN"})

	_, err := svc.SuspendIdentity(ctx, &pb.SuspendIdentityRequest{
		Id:     uuid.New().String(),
		Reason: "test",
	})

	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestSuspendIdentity_CallerOutrankedByTarget(t *testing.T) {
	svc, repo := newTestService(t)
	callerID := uuid.New()
	ctx := contextWithAuth(callerID, []string{"ADMIN"})

	identity := makeActiveIdentity(t, "owner@example.com", "SecurePass123!")
	repo.addIdentity(identity)

	// Target holds TENANT_OWNER — outranks the caller's ADMIN role.
	ownerAssignment := domain.ReconstructRoleAssignment(
		uuid.New(), svcTestTID, identity.ID(), uuid.New(), domain.RoleTenantOwner,
		nil, nil, nil, time.Now(), time.Now(),
	)
	repo.roles[identity.ID()] = []*domain.RoleAssignment{ownerAssignment}

	_, err := svc.SuspendIdentity(ctx, &pb.SuspendIdentityRequest{
		Id:     identity.ID().String(),
		Reason: "test",
	})

	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

// --- ReactivateIdentity Tests ---

func TestReactivateIdentity_Success(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := contextWithAuth(uuid.New(), []string{"ADMIN"})

	identity := makeActiveIdentity(t, "test@example.com", "SecurePass123!")
	require.NoError(t, identity.Suspend())
	repo.addIdentity(identity)

	resp, err := svc.ReactivateIdentity(ctx, &pb.ReactivateIdentityRequest{
		Id:     identity.ID().String(),
		Reason: "suspension lifted",
	})

	require.NoError(t, err)
	assert.Equal(t, pb.IdentityStatus_IDENTITY_STATUS_ACTIVE, resp.Identity.Status)
}

func TestReactivateIdentity_NoAuthContext(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	_, err := svc.ReactivateIdentity(ctx, &pb.ReactivateIdentityRequest{
		Id:     uuid.New().String(),
		Reason: "test",
	})

	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestReactivateIdentity_InsufficientPermissions(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := contextWithAuth(uuid.New(), []string{"OPERATOR"})

	identity := makeActiveIdentity(t, "test@example.com", "SecurePass123!")
	require.NoError(t, identity.Suspend())
	repo.addIdentity(identity)

	_, err := svc.ReactivateIdentity(ctx, &pb.ReactivateIdentityRequest{
		Id:     identity.ID().String(),
		Reason: "test",
	})

	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestReactivateIdentity_NotSuspended(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := contextWithAuth(uuid.New(), []string{"ADMIN"})

	identity := makeActiveIdentity(t, "test@example.com", "SecurePass123!")
	// Lock the identity
	require.NoError(t, identity.Lock())
	repo.addIdentity(identity)

	_, err := svc.ReactivateIdentity(ctx, &pb.ReactivateIdentityRequest{
		Id:     identity.ID().String(),
		Reason: "test",
	})

	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestReactivateIdentity_CallerOutrankedByTarget(t *testing.T) {
	svc, repo := newTestService(t)
	callerID := uuid.New()
	ctx := contextWithAuth(callerID, []string{"ADMIN"})

	identity := makeActiveIdentity(t, "owner@example.com", "SecurePass123!")
	require.NoError(t, identity.Suspend())
	repo.addIdentity(identity)

	// Target holds TENANT_OWNER — outranks the caller's ADMIN role.
	ownerAssignment := domain.ReconstructRoleAssignment(
		uuid.New(), svcTestTID, identity.ID(), uuid.New(), domain.RoleTenantOwner,
		nil, nil, nil, time.Now(), time.Now(),
	)
	repo.roles[identity.ID()] = []*domain.RoleAssignment{ownerAssignment}

	_, err := svc.ReactivateIdentity(ctx, &pb.ReactivateIdentityRequest{
		Id:     identity.ID().String(),
		Reason: "test",
	})

	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

// --- Health Check Tests ---

func TestHealthCheck(t *testing.T) {
	svc, _ := newTestService(t)

	resp, err := svc.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{})

	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)
}

func TestHealthWatch_Unimplemented(t *testing.T) {
	svc, _ := newTestService(t)

	err := svc.Watch(&grpc_health_v1.HealthCheckRequest{}, nil)

	require.Error(t, err)
	assert.Equal(t, codes.Unimplemented, status.Code(err))
}

// --- Mapper Tests ---

func TestDomainStatusToProto(t *testing.T) {
	tests := []struct {
		domain domain.IdentityStatus
		proto  pb.IdentityStatus
	}{
		{domain.IdentityStatusPendingInvite, pb.IdentityStatus_IDENTITY_STATUS_PENDING_INVITE},
		{domain.IdentityStatusActive, pb.IdentityStatus_IDENTITY_STATUS_ACTIVE},
		{domain.IdentityStatusSuspended, pb.IdentityStatus_IDENTITY_STATUS_SUSPENDED},
		{domain.IdentityStatusLocked, pb.IdentityStatus_IDENTITY_STATUS_LOCKED},
		{"UNKNOWN", pb.IdentityStatus_IDENTITY_STATUS_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(string(tt.domain), func(t *testing.T) {
			assert.Equal(t, tt.proto, domainStatusToProto(tt.domain))
		})
	}
}

func TestProtoRoleToDomain(t *testing.T) {
	tests := []struct {
		proto  pb.Role
		domain string
	}{
		{pb.Role_ROLE_ADMIN, "ADMIN"},
		{pb.Role_ROLE_OPERATOR, "OPERATOR"},
		{pb.Role_ROLE_AUDITOR, "VIEWER"},
		{pb.Role_ROLE_TENANT_OWNER, "TENANT_OWNER"},
		{pb.Role_ROLE_PLATFORM_ADMIN, "PLATFORM"},
		{pb.Role_ROLE_SUPER_ADMIN, "PLATFORM"},
		{pb.Role_ROLE_UNSPECIFIED, ""},
	}

	for _, tt := range tests {
		t.Run(tt.proto.String(), func(t *testing.T) {
			assert.Equal(t, tt.domain, protoRoleToDomain(tt.proto))
		})
	}
}

func TestIdentityToProto(t *testing.T) {
	identity := domain.ReconstructIdentity(
		uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		svcTestTID,
		"test@example.com",
		domain.IdentityStatusActive,
		"hash",
		"google",
		"sub-123",
		2,
		time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
		5,
	)

	pbIdentity := identityToProto(identity)

	assert.Equal(t, "00000000-0000-0000-0000-000000000001", pbIdentity.Id)
	assert.Equal(t, "test@example.com", pbIdentity.Email)
	assert.Equal(t, pb.IdentityStatus_IDENTITY_STATUS_ACTIVE, pbIdentity.Status)
	assert.Equal(t, int32(2), pbIdentity.FailedAttempts)
	assert.Equal(t, int32(5), pbIdentity.Version)
	assert.Equal(t, "google", pbIdentity.ExternalIdp)
	assert.Equal(t, "sub-123", pbIdentity.ExternalIdpSub)
}

func TestIdentityToProto_Nil(t *testing.T) {
	assert.Nil(t, identityToProto(nil))
}

func TestRoleAssignmentToProto(t *testing.T) {
	assignment, err := domain.NewRoleAssignment(svcTestTID, uuid.New(), uuid.New(), "ADMIN", "OPERATOR")
	require.NoError(t, err)

	pb := roleAssignmentToProto(assignment)

	assert.Equal(t, assignment.ID().String(), pb.Id)
	assert.Equal(t, assignment.IdentityID().String(), pb.IdentityId)
	assert.False(t, pb.Revoked)
}

func TestRoleAssignmentToProto_Nil(t *testing.T) {
	assert.Nil(t, roleAssignmentToProto(nil))
}

func TestInvitationToProto_Nil(t *testing.T) {
	assert.Nil(t, invitationToProto(nil))
}

func TestInvitationToProto_AcceptedInvitation(t *testing.T) {
	identity, err := domain.NewIdentity(svcTestTID, "inv-proto@example.com")
	require.NoError(t, err)
	inv, _, err := domain.NewInvitation(identity.ID(), uuid.New())
	require.NoError(t, err)

	// Accept the invitation to cover the AcceptedAt path
	require.NoError(t, inv.Accept())

	proto := invitationToProto(inv)
	assert.NotNil(t, proto)
	assert.NotNil(t, proto.AcceptedAt)
}

func TestInvitationToProto_PendingInvitation(t *testing.T) {
	identity, err := domain.NewIdentity(svcTestTID, "inv-pending-proto@example.com")
	require.NoError(t, err)
	inv, _, err := domain.NewInvitation(identity.ID(), uuid.New())
	require.NoError(t, err)

	proto := invitationToProto(inv)
	assert.NotNil(t, proto)
	assert.Nil(t, proto.AcceptedAt)
}

// --- Additional coverage tests ---

func TestCreateIdentity_InternalError(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)
	repo.saveErr = errors.New("db unavailable")

	_, err := svc.CreateIdentity(ctx, &pb.CreateIdentityRequest{
		Email: "internal@example.com",
	})

	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestUpdateIdentity_NotFound(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	_, err := svc.UpdateIdentity(ctx, &pb.UpdateIdentityRequest{
		Id:      uuid.New().String(),
		Version: 1,
	})

	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestUpdateIdentity_InvalidID(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	_, err := svc.UpdateIdentity(ctx, &pb.UpdateIdentityRequest{
		Id: "not-a-uuid",
	})

	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestUpdateIdentity_EmailChangeRejected(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	identity, err := domain.NewIdentity(svcTestTID, "immutable@example.com")
	require.NoError(t, err)
	repo.addIdentity(identity)

	_, err = svc.UpdateIdentity(ctx, &pb.UpdateIdentityRequest{
		Id:      identity.ID().String(),
		Version: int32(identity.Version()),
		Email:   "changed@example.com",
	})

	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	assert.Contains(t, err.Error(), "immutable")
}

func TestListIdentities_PaginationUnsupported(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	_, err := svc.ListIdentities(ctx, &pb.ListIdentitiesRequest{
		PageSize: 10,
	})

	require.Error(t, err)
	assert.Equal(t, codes.Unimplemented, status.Code(err))
}

func TestListIdentities_InternalError(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)
	repo.listByTenantErr = errors.New("db down")

	_, err := svc.ListIdentities(ctx, &pb.ListIdentitiesRequest{})

	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestAuthenticate_InternalError(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)
	repo.findByEmailErr = errors.New("db down")

	_, err := svc.Authenticate(ctx, &pb.AuthenticateRequest{
		Email:    "err@example.com",
		Password: "anything",
	})

	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestAuthenticate_PendingInviteAccount(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	identity, err := domain.NewIdentity(svcTestTID, "pending@example.com")
	require.NoError(t, err)
	repo.addIdentity(identity)

	resp, err := svc.Authenticate(ctx, &pb.AuthenticateRequest{
		Email:    "pending@example.com",
		Password: "anything",
	})

	require.NoError(t, err)
	assert.False(t, resp.Authenticated)
	assert.Equal(t, pb.AuthenticationFailureReason_AUTHENTICATION_FAILURE_REASON_ACCOUNT_NOT_ACTIVE, resp.FailureReason)
}

func TestSetPassword_ExpiredInvitation(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	identity, err := domain.NewIdentity(svcTestTID, "expired-setpw@example.com")
	require.NoError(t, err)
	repo.addIdentity(identity)

	expiredInv := domain.ReconstructInvitation(
		uuid.New(),
		identity.ID(),
		uuid.New(),
		tokens.HashToken("expired-setpw-token"),
		time.Now().Add(-1*time.Hour),
		domain.InvitationStatusPending,
		time.Now().Add(-2*time.Hour),
		time.Now().Add(-2*time.Hour),
	)
	repo.addInvitation(expiredInv)

	_, err = svc.SetPassword(ctx, &pb.SetPasswordRequest{
		Token:    "expired-setpw-token",
		Password: "NewSecurePass1!",
	})

	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestSetPassword_AlreadyAcceptedInvitation(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	identity, err := domain.NewIdentity(svcTestTID, "already-accepted@example.com")
	require.NoError(t, err)
	repo.addIdentity(identity)

	inv, plaintext, err := domain.NewInvitation(identity.ID(), uuid.New())
	require.NoError(t, err)
	require.NoError(t, inv.Accept())
	repo.addInvitation(inv)

	_, err = svc.SetPassword(ctx, &pb.SetPasswordRequest{
		Token:    plaintext,
		Password: "NewSecurePass1!",
	})

	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestSetPassword_InternalFindInvitationError(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)
	repo.findInvitationErr = errors.New("db down")

	_, err := svc.SetPassword(ctx, &pb.SetPasswordRequest{
		Token:    "some-token",
		Password: "NewSecurePass1!",
	})

	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestChangePassword_WeakNewPassword(t *testing.T) {
	svc, repo := newTestService(t)

	password := "OldSecurePass1!"
	identity := makeActiveIdentity(t, "changepw-weak@example.com", password)
	repo.addIdentity(identity)

	ctx := contextWithAuth(identity.ID(), []string{"ADMIN"})

	_, err := svc.ChangePassword(ctx, &pb.ChangePasswordRequest{
		CurrentPassword: password,
		NewPassword:     "weak",
	})

	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestRequestPasswordReset_InternalError(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)
	repo.findByEmailErr = errors.New("db down")

	_, err := svc.RequestPasswordReset(ctx, &pb.RequestPasswordResetRequest{
		Email: "err@example.com",
	})

	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestRequestPasswordReset_SaveInvitationError(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	identity := makeActiveIdentity(t, "reset-save-err@example.com", "SecurePass123!")
	repo.addIdentity(identity)
	repo.saveInvitationErr = errors.New("db write error")

	_, err := svc.RequestPasswordReset(ctx, &pb.RequestPasswordResetRequest{
		Email: "reset-save-err@example.com",
	})

	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestCompletePasswordReset_WeakPassword(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	_, err := svc.CompletePasswordReset(ctx, &pb.CompletePasswordResetRequest{
		ResetToken:  "any-token",
		NewPassword: "weak",
	})

	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestCompletePasswordReset_AlreadyUsedToken(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	identity := makeActiveIdentity(t, "reset-used@example.com", "OldPassword123!")
	repo.addIdentity(identity)

	inv, plaintext, err := domain.NewInvitation(identity.ID(), identity.ID())
	require.NoError(t, err)
	require.NoError(t, inv.Accept())
	repo.addInvitation(inv)

	_, err = svc.CompletePasswordReset(ctx, &pb.CompletePasswordResetRequest{
		ResetToken:  plaintext,
		NewPassword: "NewSecurePass1!",
	})

	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestGrantRole_InvalidIdentityID(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := contextWithAuth(uuid.New(), []string{"ADMIN"})

	_, err := svc.GrantRole(ctx, &pb.GrantRoleRequest{
		IdentityId: "not-a-uuid",
		Role:       pb.Role_ROLE_OPERATOR,
	})

	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGrantRole_SaveError(t *testing.T) {
	svc, repo := newTestService(t)

	targetIdentity, err := domain.NewIdentity(svcTestTID, "saveerr@example.com")
	require.NoError(t, err)
	repo.addIdentity(targetIdentity)
	repo.saveRoleErr = errors.New("db write error")

	ctx := contextWithAuth(uuid.New(), []string{"ADMIN"})

	_, err = svc.GrantRole(ctx, &pb.GrantRoleRequest{
		IdentityId: targetIdentity.ID().String(),
		Role:       pb.Role_ROLE_OPERATOR,
	})

	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestRevokeRole_NoAuthContext(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	_, err := svc.RevokeRole(ctx, &pb.RevokeRoleRequest{
		IdentityId:       uuid.New().String(),
		RoleAssignmentId: uuid.New().String(),
	})

	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestRevokeRole_InvalidIdentityID(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := contextWithAuth(uuid.New(), []string{"ADMIN"})

	_, err := svc.RevokeRole(ctx, &pb.RevokeRoleRequest{
		IdentityId:       "not-a-uuid",
		RoleAssignmentId: uuid.New().String(),
	})

	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestRevokeRole_InvalidAssignmentID(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := contextWithAuth(uuid.New(), []string{"ADMIN"})

	_, err := svc.RevokeRole(ctx, &pb.RevokeRoleRequest{
		IdentityId:       uuid.New().String(),
		RoleAssignmentId: "not-a-uuid",
	})

	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestListRoleAssignments_InvalidIdentityID(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	_, err := svc.ListRoleAssignments(ctx, &pb.ListRoleAssignmentsRequest{
		IdentityId: "not-a-uuid",
	})

	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestListRoleAssignments_InternalError(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)
	repo.findRolesErr = errors.New("db down")

	_, err := svc.ListRoleAssignments(ctx, &pb.ListRoleAssignmentsRequest{
		IdentityId: uuid.New().String(),
	})

	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestInviteUser_InvalidEmail(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := contextWithAuth(uuid.New(), []string{"ADMIN"})

	_, err := svc.InviteUser(ctx, &pb.InviteUserRequest{
		Email: "not-an-email",
	})

	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestInviteUser_NoRole(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := contextWithAuth(uuid.New(), []string{"ADMIN"})

	resp, err := svc.InviteUser(ctx, &pb.InviteUserRequest{
		Email: "norole@example.com",
		Role:  pb.Role_ROLE_UNSPECIFIED,
	})

	require.NoError(t, err)
	assert.NotNil(t, resp.Identity)
	assert.NotNil(t, resp.Invitation)
	// No role assignment should have been created
	assert.Empty(t, repo.roles)
}

func TestSuspendIdentity_InvalidID(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := contextWithAuth(uuid.New(), []string{"ADMIN"})

	_, err := svc.SuspendIdentity(ctx, &pb.SuspendIdentityRequest{
		Id:     "not-a-uuid",
		Reason: "test",
	})

	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestReactivateIdentity_InvalidID(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := contextWithAuth(uuid.New(), []string{"ADMIN"})

	_, err := svc.ReactivateIdentity(ctx, &pb.ReactivateIdentityRequest{
		Id:     "not-a-uuid",
		Reason: "test",
	})

	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestReactivateIdentity_NotFound(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := contextWithAuth(uuid.New(), []string{"ADMIN"})

	_, err := svc.ReactivateIdentity(ctx, &pb.ReactivateIdentityRequest{
		Id:     uuid.New().String(),
		Reason: "test",
	})

	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestDomainRoleToProto(t *testing.T) {
	tests := []struct {
		domain domain.Role
		proto  pb.Role
	}{
		{domain.RoleViewer, pb.Role_ROLE_AUDITOR},
		{domain.RoleOperator, pb.Role_ROLE_OPERATOR},
		{domain.RoleAdmin, pb.Role_ROLE_ADMIN},
		{domain.RoleTenantOwner, pb.Role_ROLE_TENANT_OWNER},
		{domain.RolePlatform, pb.Role_ROLE_PLATFORM_ADMIN},
		{"UNKNOWN_ROLE", pb.Role_ROLE_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(string(tt.domain), func(t *testing.T) {
			assert.Equal(t, tt.proto, domainRoleToProto(tt.domain))
		})
	}
}

func TestMapDomainError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected codes.Code
	}{
		{"identity not found", domain.ErrIdentityNotFound, codes.NotFound},
		{"email already exists", domain.ErrEmailAlreadyExists, codes.AlreadyExists},
		{"account locked", domain.ErrAccountLocked, codes.FailedPrecondition},
		{"invalid status transition", domain.ErrInvalidStatusTransition, codes.FailedPrecondition},
		{"invitation not found", domain.ErrInvitationNotFound, codes.NotFound},
		{"invitation expired", domain.ErrInvitationExpired, codes.FailedPrecondition},
		{"invitation already accepted", domain.ErrInvitationAlreadyAccepted, codes.FailedPrecondition},
		{"version conflict", domain.ErrVersionConflict, codes.Aborted},
		{"unknown error", errors.New("unknown"), codes.Internal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := mapDomainError(tt.err, "test")
			assert.Equal(t, tt.expected, status.Code(err))
		})
	}
}

func TestGetCallerHighestRole_NoRoles(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := tenant.WithTenant(context.Background(), svcTestTID)

	role := svc.getCallerHighestRole(ctx)
	assert.Empty(t, role)
}

func TestGetCallerHighestRole_MultipleRoles(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := contextWithAuth(uuid.New(), []string{"VIEWER", "ADMIN", "OPERATOR"})

	role := svc.getCallerHighestRole(ctx)
	assert.Equal(t, "ADMIN", role)
}

func TestGetCallerHighestRole_SingleRole(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := contextWithAuth(uuid.New(), []string{"OPERATOR"})

	role := svc.getCallerHighestRole(ctx)
	assert.Equal(t, "OPERATOR", role)
}

func TestRoleAssignmentToProto_WithExpiry(t *testing.T) {
	expiry := time.Now().Add(24 * time.Hour)
	assignment := domain.ReconstructRoleAssignment(
		uuid.New(), svcTestTID, uuid.New(), uuid.New(),
		domain.RoleOperator, &expiry, nil, nil,
		time.Now(), time.Now(),
	)

	proto := roleAssignmentToProto(assignment)
	assert.NotNil(t, proto.ExpiresAt)
}

func TestRoleAssignmentToProto_WithRevocation(t *testing.T) {
	revokedAt := time.Now()
	revokedBy := uuid.New()
	assignment := domain.ReconstructRoleAssignment(
		uuid.New(), svcTestTID, uuid.New(), uuid.New(),
		domain.RoleOperator, nil, &revokedAt, &revokedBy,
		time.Now(), time.Now(),
	)

	proto := roleAssignmentToProto(assignment)
	assert.True(t, proto.Revoked)
	assert.NotNil(t, proto.RevokedAt)
	assert.Equal(t, revokedBy.String(), proto.RevokedBy)
}

// --- mockOutbox ---

type mockOutbox struct {
	entries  []*email.OutboxEntry
	enqueueErr error
}

func (m *mockOutbox) Enqueue(_ context.Context, entry *email.OutboxEntry) error {
	if m.enqueueErr != nil {
		return m.enqueueErr
	}
	m.entries = append(m.entries, entry)
	return nil
}

func newTestServiceWithOutbox(t *testing.T) (*Service, *mockRepository, *mockOutbox) {
	t.Helper()
	repo := newMockRepository()
	outbox := &mockOutbox{}
	svc, err := NewService(repo, slog.Default(), WithEmailOutbox(outbox), WithBaseURL("https://app.example.com"))
	require.NoError(t, err)
	return svc, repo, outbox
}

// --- InviteUser email outbox tests ---

func TestInviteUser_QueuesInvitationEmail(t *testing.T) {
	svc, repo, outbox := newTestServiceWithOutbox(t)

	inviterID := uuid.New()
	inviter := domain.ReconstructIdentity(
		inviterID, svcTestTID, "inviter@example.com",
		domain.IdentityStatusActive, "", "", "", 0,
		time.Now(), time.Now(), 0,
	)
	repo.addIdentity(inviter)

	ctx := contextWithAuth(inviterID, []string{"ADMIN"})
	resp, err := svc.InviteUser(ctx, &pb.InviteUserRequest{
		Email: "invited@example.com",
		Role:  pb.Role_ROLE_OPERATOR,
	})

	require.NoError(t, err)
	assert.NotNil(t, resp.Invitation)
	require.Len(t, outbox.entries, 1)

	entry := outbox.entries[0]
	assert.Equal(t, "invite-user", entry.TemplateName)
	assert.Equal(t, []string{"invited@example.com"}, entry.ToAddresses)
	assert.Equal(t, "inviter@example.com", entry.TemplateData["InviterEmail"])
	assert.NotEmpty(t, entry.TemplateData["AcceptLink"])
	assert.Contains(t, entry.TemplateData["AcceptLink"].(string), resp.InvitationToken)
	assert.Equal(t, string(svcTestTID), entry.TemplateData["TenantName"])
}

func TestInviteUser_NilOutbox_NoEmailQueued(t *testing.T) {
	// No WithEmailOutbox option - outbox is nil, no panic, invitation still created.
	svc, repo := newTestService(t)

	inviterID := uuid.New()
	ctx := contextWithAuth(inviterID, []string{"ADMIN"})
	resp, err := svc.InviteUser(ctx, &pb.InviteUserRequest{
		Email: "invited@example.com",
		Role:  pb.Role_ROLE_OPERATOR,
	})

	require.NoError(t, err)
	assert.NotNil(t, resp.Invitation)
	assert.Len(t, repo.invitations, 1)
}

func TestInviteUser_OutboxError_InvitationStillSucceeds(t *testing.T) {
	// Outbox enqueue failure must not fail the RPC - email is best-effort.
	svc, repo, outbox := newTestServiceWithOutbox(t)
	outbox.enqueueErr = errors.New("db unavailable")

	inviterID := uuid.New()
	ctx := contextWithAuth(inviterID, []string{"ADMIN"})
	resp, err := svc.InviteUser(ctx, &pb.InviteUserRequest{
		Email: "invited@example.com",
		Role:  pb.Role_ROLE_OPERATOR,
	})

	require.NoError(t, err)
	assert.NotNil(t, resp.Invitation)
	assert.Len(t, repo.invitations, 1)
}
