// Package service implements gRPC handlers for the identity and access management domain.
package service

import (
	"context"
	"errors"
	"log/slog"
	"os"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/identity/v1"
	"github.com/meridianhub/meridian/services/identity/adapters/persistence"
	"github.com/meridianhub/meridian/services/identity/domain"
	"github.com/meridianhub/meridian/shared/pkg/credentials"
	"github.com/meridianhub/meridian/shared/pkg/tokens"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

// Service errors
var (
	ErrRepositoryNil = errors.New("repository cannot be nil")
)

// Service implements the IdentityService gRPC service.
type Service struct {
	pb.UnimplementedIdentityServiceServer
	repo   domain.Repository
	logger *slog.Logger
}

// NewService creates a new identity service with the required repository dependency.
func NewService(repo domain.Repository, logger *slog.Logger) (*Service, error) {
	if repo == nil {
		return nil, ErrRepositoryNil
	}
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}
	return &Service{
		repo:   repo,
		logger: logger,
	}, nil
}

// --- Identity CRUD ---

// CreateIdentity creates a new identity in PENDING_INVITE status.
func (s *Service) CreateIdentity(ctx context.Context, req *pb.CreateIdentityRequest) (*pb.CreateIdentityResponse, error) {
	identity, err := domain.NewIdentity(req.GetEmail())
	if err != nil {
		s.logger.ErrorContext(ctx, "invalid email for identity creation",
			"email", req.GetEmail(),
			"error", err)
		return nil, status.Errorf(codes.InvalidArgument, "invalid email: %v", err)
	}

	if err := s.repo.Save(ctx, identity); err != nil {
		if errors.Is(err, domain.ErrEmailAlreadyExists) {
			return nil, status.Errorf(codes.AlreadyExists, "email already exists: %s", req.GetEmail())
		}
		s.logger.ErrorContext(ctx, "failed to save identity",
			"email", req.GetEmail(),
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to create identity")
	}

	return &pb.CreateIdentityResponse{
		Identity: identityToProto(identity),
	}, nil
}

// RetrieveIdentity retrieves an identity by ID.
func (s *Service) RetrieveIdentity(ctx context.Context, req *pb.RetrieveIdentityRequest) (*pb.RetrieveIdentityResponse, error) {
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid identity ID: %v", err)
	}

	identity, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, mapDomainError(err, "identity")
	}

	return &pb.RetrieveIdentityResponse{
		Identity: identityToProto(identity),
	}, nil
}

// UpdateIdentity updates mutable fields on an existing identity with optimistic locking.
func (s *Service) UpdateIdentity(ctx context.Context, req *pb.UpdateIdentityRequest) (*pb.UpdateIdentityResponse, error) {
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid identity ID: %v", err)
	}

	identity, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, mapDomainError(err, "identity")
	}

	if identity.Version() != int64(req.GetVersion()) {
		return nil, status.Errorf(codes.Aborted, "version conflict: expected %d, got %d", identity.Version(), req.GetVersion())
	}

	// No mutable fields to update beyond email at this point.
	// The proto defines email as the only updatable field; since the domain
	// model treats email as immutable (set at creation), we return the
	// current identity unchanged when no email update is requested.
	// Future fields can be added here.

	if err := s.repo.Save(ctx, identity); err != nil {
		if errors.Is(err, persistence.ErrVersionConflict) {
			return nil, status.Errorf(codes.Aborted, "version conflict: identity was modified by another transaction")
		}
		s.logger.ErrorContext(ctx, "failed to save identity",
			"identity_id", id,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to update identity")
	}

	return &pb.UpdateIdentityResponse{
		Identity: identityToProto(identity),
	}, nil
}

// ListIdentities returns a paginated list of identities within the tenant.
func (s *Service) ListIdentities(ctx context.Context, _ *pb.ListIdentitiesRequest) (*pb.ListIdentitiesResponse, error) {
	identities, err := s.repo.ListByTenant(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to list identities", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to list identities")
	}

	pbIdentities := make([]*pb.Identity, 0, len(identities))
	for _, ident := range identities {
		pbIdentities = append(pbIdentities, identityToProto(ident))
	}

	return &pb.ListIdentitiesResponse{
		Identities: pbIdentities,
		TotalCount: int32(len(pbIdentities)),
	}, nil
}

// --- Authentication ---

// Authenticate validates credentials and returns the authenticated identity.
// Called by the Dex gRPC connector during the authentication flow.
func (s *Service) Authenticate(ctx context.Context, req *pb.AuthenticateRequest) (*pb.AuthenticateResponse, error) {
	identity, err := s.repo.FindByEmail(ctx, req.GetEmail())
	if err != nil {
		if errors.Is(err, domain.ErrIdentityNotFound) {
			return &pb.AuthenticateResponse{
				Authenticated: false,
				FailureReason: pb.AuthenticationFailureReason_AUTHENTICATION_FAILURE_REASON_INVALID_CREDENTIALS,
			}, nil
		}
		s.logger.ErrorContext(ctx, "failed to find identity by email",
			"email", req.GetEmail(),
			"error", err)
		return nil, status.Errorf(codes.Internal, "authentication failed")
	}

	if identity.IsLocked() {
		return &pb.AuthenticateResponse{
			Authenticated: false,
			FailureReason: pb.AuthenticationFailureReason_AUTHENTICATION_FAILURE_REASON_ACCOUNT_LOCKED,
		}, nil
	}

	if identity.Status() != domain.IdentityStatusActive {
		return &pb.AuthenticateResponse{
			Authenticated: false,
			FailureReason: pb.AuthenticationFailureReason_AUTHENTICATION_FAILURE_REASON_ACCOUNT_NOT_ACTIVE,
		}, nil
	}

	if err := credentials.ValidatePassword(req.GetPassword(), identity.PasswordHash()); err != nil {
		_ = identity.RecordLoginAttempt(false)
		if saveErr := s.repo.Save(ctx, identity); saveErr != nil {
			s.logger.ErrorContext(ctx, "failed to save failed login attempt",
				"identity_id", identity.ID(),
				"error", saveErr)
		}
		return &pb.AuthenticateResponse{
			Authenticated: false,
			FailureReason: pb.AuthenticationFailureReason_AUTHENTICATION_FAILURE_REASON_INVALID_CREDENTIALS,
		}, nil
	}

	_ = identity.RecordLoginAttempt(true)
	if saveErr := s.repo.Save(ctx, identity); saveErr != nil {
		s.logger.ErrorContext(ctx, "failed to save successful login attempt",
			"identity_id", identity.ID(),
			"error", saveErr)
	}

	roles, err := s.repo.FindRoleAssignments(ctx, identity.ID())
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to fetch role assignments for authenticated identity",
			"identity_id", identity.ID(),
			"error", err)
	}

	pbIdentity := identityToProto(identity)
	_ = roles // roles are available for future use in claims; for now we return the identity

	return &pb.AuthenticateResponse{
		Identity:      pbIdentity,
		Authenticated: true,
	}, nil
}

// --- Password Management ---

// SetPassword sets the initial password for an identity using an invitation token.
func (s *Service) SetPassword(ctx context.Context, req *pb.SetPasswordRequest) (*pb.SetPasswordResponse, error) {
	if err := credentials.ValidatePasswordPolicy(req.GetPassword()); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "password policy violation: %v", err)
	}

	tokenHash := tokens.HashToken(req.GetToken())
	invitation, err := s.repo.FindInvitationByTokenHash(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, domain.ErrInvitationNotFound) {
			return nil, status.Errorf(codes.NotFound, "invalid or expired token")
		}
		s.logger.ErrorContext(ctx, "failed to find invitation by token", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to validate token")
	}

	if err := invitation.Accept(); err != nil {
		if errors.Is(err, domain.ErrInvitationExpired) {
			return nil, status.Errorf(codes.FailedPrecondition, "invitation has expired")
		}
		if errors.Is(err, domain.ErrInvitationAlreadyAccepted) {
			return nil, status.Errorf(codes.FailedPrecondition, "invitation has already been accepted")
		}
		return nil, status.Errorf(codes.Internal, "failed to accept invitation")
	}

	identity, err := s.repo.FindByID(ctx, invitation.IdentityID())
	if err != nil {
		return nil, mapDomainError(err, "identity")
	}

	hash, err := credentials.HashPassword(req.GetPassword())
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to hash password", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to set password")
	}

	if err := identity.SetPassword(hash); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to set password on identity")
	}

	if err := identity.Activate(); err != nil {
		s.logger.ErrorContext(ctx, "failed to activate identity",
			"identity_id", identity.ID(),
			"error", err)
		return nil, status.Errorf(codes.FailedPrecondition, "cannot activate identity: %v", err)
	}

	if err := s.repo.Save(ctx, identity); err != nil {
		s.logger.ErrorContext(ctx, "failed to save identity after password set",
			"identity_id", identity.ID(),
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to save identity")
	}

	if err := s.repo.SaveInvitation(ctx, invitation); err != nil {
		s.logger.ErrorContext(ctx, "failed to save invitation after acceptance",
			"invitation_id", invitation.ID(),
			"error", err)
	}

	return &pb.SetPasswordResponse{
		IdentityId: identity.ID().String(),
	}, nil
}

// ChangePassword changes the password for the authenticated identity.
func (s *Service) ChangePassword(ctx context.Context, req *pb.ChangePasswordRequest) (*pb.ChangePasswordResponse, error) {
	callerID, ok := auth.GetUserIDFromContext(ctx)
	if !ok {
		return nil, status.Errorf(codes.Unauthenticated, "missing authentication context")
	}

	id, err := uuid.Parse(callerID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "invalid caller identity ID")
	}

	identity, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, mapDomainError(err, "identity")
	}

	if err := credentials.ValidatePassword(req.GetCurrentPassword(), identity.PasswordHash()); err != nil {
		return nil, status.Errorf(codes.PermissionDenied, "current password is incorrect")
	}

	if err := credentials.ValidatePasswordPolicy(req.GetNewPassword()); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "password policy violation: %v", err)
	}

	hash, err := credentials.HashPassword(req.GetNewPassword())
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to hash new password", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to change password")
	}

	if err := identity.SetPassword(hash); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to set password on identity")
	}

	if err := s.repo.Save(ctx, identity); err != nil {
		s.logger.ErrorContext(ctx, "failed to save identity after password change",
			"identity_id", identity.ID(),
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to save identity")
	}

	return &pb.ChangePasswordResponse{
		IdentityId: identity.ID().String(),
	}, nil
}

// RequestPasswordReset initiates the password reset flow by generating a reset token.
// For security, always returns success even if the email is not found.
func (s *Service) RequestPasswordReset(ctx context.Context, req *pb.RequestPasswordResetRequest) (*pb.RequestPasswordResetResponse, error) {
	// Attempt to find identity. If not found, return success to prevent email enumeration.
	identity, findErr := s.repo.FindByEmail(ctx, req.GetEmail())
	if findErr == nil {
		_, invitation, tokenErr := s.createResetInvitation(ctx, identity.ID())
		if tokenErr != nil {
			s.logger.ErrorContext(ctx, "failed to create password reset token",
				"identity_id", identity.ID(),
				"error", tokenErr)
		} else if saveErr := s.repo.SaveInvitation(ctx, invitation); saveErr != nil {
			s.logger.ErrorContext(ctx, "failed to save password reset invitation",
				"identity_id", identity.ID(),
				"error", saveErr)
		} else {
			s.logger.DebugContext(ctx, "password reset token generated",
				"identity_id", identity.ID())
		}
	}

	return &pb.RequestPasswordResetResponse{
		Email: req.GetEmail(),
	}, nil
}

// createResetInvitation creates a new invitation token for password reset purposes.
func (s *Service) createResetInvitation(ctx context.Context, identityID uuid.UUID) (string, *domain.Invitation, error) {
	_ = ctx
	invitation, plaintext, err := domain.NewInvitation(identityID, identityID)
	if err != nil {
		return "", nil, err
	}
	return plaintext, invitation, nil
}

// CompletePasswordReset validates the reset token and stores the new password.
func (s *Service) CompletePasswordReset(ctx context.Context, req *pb.CompletePasswordResetRequest) (*pb.CompletePasswordResetResponse, error) {
	if err := credentials.ValidatePasswordPolicy(req.GetNewPassword()); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "password policy violation: %v", err)
	}

	tokenHash := tokens.HashToken(req.GetResetToken())
	invitation, err := s.repo.FindInvitationByTokenHash(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, domain.ErrInvitationNotFound) {
			return nil, status.Errorf(codes.NotFound, "invalid or expired reset token")
		}
		s.logger.ErrorContext(ctx, "failed to find reset invitation", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to validate reset token")
	}

	if err := invitation.Accept(); err != nil {
		if errors.Is(err, domain.ErrInvitationExpired) {
			return nil, status.Errorf(codes.FailedPrecondition, "reset token has expired")
		}
		if errors.Is(err, domain.ErrInvitationAlreadyAccepted) {
			return nil, status.Errorf(codes.FailedPrecondition, "reset token has already been used")
		}
		return nil, status.Errorf(codes.Internal, "failed to process reset token")
	}

	identity, err := s.repo.FindByID(ctx, invitation.IdentityID())
	if err != nil {
		return nil, mapDomainError(err, "identity")
	}

	hash, err := credentials.HashPassword(req.GetNewPassword())
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to hash new password", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to reset password")
	}

	if err := identity.SetPassword(hash); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to set password on identity")
	}

	if err := s.repo.Save(ctx, identity); err != nil {
		s.logger.ErrorContext(ctx, "failed to save identity after password reset",
			"identity_id", identity.ID(),
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to save identity")
	}

	if err := s.repo.SaveInvitation(ctx, invitation); err != nil {
		s.logger.ErrorContext(ctx, "failed to save invitation after reset completion",
			"invitation_id", invitation.ID(),
			"error", err)
	}

	return &pb.CompletePasswordResetResponse{
		IdentityId: identity.ID().String(),
	}, nil
}

// --- Role Management ---

// GrantRole assigns a role to an identity, enforcing the role hierarchy.
func (s *Service) GrantRole(ctx context.Context, req *pb.GrantRoleRequest) (*pb.GrantRoleResponse, error) {
	callerID, ok := auth.GetUserIDFromContext(ctx)
	if !ok {
		return nil, status.Errorf(codes.Unauthenticated, "missing authentication context")
	}

	granterID, err := uuid.Parse(callerID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "invalid caller identity ID")
	}

	identityID, err := uuid.Parse(req.GetIdentityId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid identity ID: %v", err)
	}

	// Verify the target identity exists.
	if _, err := s.repo.FindByID(ctx, identityID); err != nil {
		return nil, mapDomainError(err, "identity")
	}

	granterRole := s.getCallerHighestRole(ctx)
	targetRole := protoRoleToDomain(req.GetRole())

	assignment, err := domain.NewRoleAssignment(identityID, granterID, granterRole, targetRole)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidRole) {
			return nil, status.Errorf(codes.InvalidArgument, "invalid role: %v", err)
		}
		if errors.Is(err, domain.ErrInsufficientRolePermissions) {
			return nil, status.Errorf(codes.PermissionDenied, "insufficient permissions to grant this role")
		}
		return nil, status.Errorf(codes.Internal, "failed to create role assignment")
	}

	if err := s.repo.SaveRoleAssignment(ctx, assignment); err != nil {
		s.logger.ErrorContext(ctx, "failed to save role assignment",
			"identity_id", identityID,
			"role", targetRole,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to grant role")
	}

	return &pb.GrantRoleResponse{
		RoleAssignment: roleAssignmentToProto(assignment),
	}, nil
}

// RevokeRole revokes a role assignment from an identity.
func (s *Service) RevokeRole(ctx context.Context, req *pb.RevokeRoleRequest) (*pb.RevokeRoleResponse, error) {
	callerID, ok := auth.GetUserIDFromContext(ctx)
	if !ok {
		return nil, status.Errorf(codes.Unauthenticated, "missing authentication context")
	}

	revokerID, err := uuid.Parse(callerID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "invalid caller identity ID")
	}

	identityID, err := uuid.Parse(req.GetIdentityId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid identity ID: %v", err)
	}

	assignmentID, err := uuid.Parse(req.GetRoleAssignmentId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid role assignment ID: %v", err)
	}

	assignments, err := s.repo.FindRoleAssignments(ctx, identityID)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to find role assignments",
			"identity_id", identityID,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to find role assignments")
	}

	var target *domain.RoleAssignment
	for _, a := range assignments {
		if a.ID() == assignmentID {
			target = a
			break
		}
	}

	if target == nil {
		return nil, status.Errorf(codes.NotFound, "role assignment not found")
	}

	if err := target.Revoke(revokerID); err != nil {
		if errors.Is(err, domain.ErrRoleAlreadyRevoked) {
			return nil, status.Errorf(codes.FailedPrecondition, "role assignment has already been revoked")
		}
		return nil, status.Errorf(codes.Internal, "failed to revoke role")
	}

	if err := s.repo.SaveRoleAssignment(ctx, target); err != nil {
		s.logger.ErrorContext(ctx, "failed to save revoked role assignment",
			"assignment_id", assignmentID,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to save role revocation")
	}

	return &pb.RevokeRoleResponse{
		RoleAssignment: roleAssignmentToProto(target),
	}, nil
}

// ListRoleAssignments lists the role assignments for an identity.
func (s *Service) ListRoleAssignments(ctx context.Context, req *pb.ListRoleAssignmentsRequest) (*pb.ListRoleAssignmentsResponse, error) {
	identityID, err := uuid.Parse(req.GetIdentityId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid identity ID: %v", err)
	}

	assignments, err := s.repo.FindRoleAssignments(ctx, identityID)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to list role assignments",
			"identity_id", identityID,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to list role assignments")
	}

	pbAssignments := make([]*pb.RoleAssignment, 0, len(assignments))
	for _, a := range assignments {
		if !req.GetIncludeRevoked() && a.RevokedAt() != nil {
			continue
		}
		pbAssignments = append(pbAssignments, roleAssignmentToProto(a))
	}

	return &pb.ListRoleAssignmentsResponse{
		RoleAssignments: pbAssignments,
	}, nil
}

// --- Invitation Management ---

// InviteUser creates an Identity in PENDING_INVITE status and an Invitation record.
func (s *Service) InviteUser(ctx context.Context, req *pb.InviteUserRequest) (*pb.InviteUserResponse, error) {
	callerID, ok := auth.GetUserIDFromContext(ctx)
	if !ok {
		return nil, status.Errorf(codes.Unauthenticated, "missing authentication context")
	}

	inviterID, err := uuid.Parse(callerID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "invalid caller identity ID")
	}

	identity, err := domain.NewIdentity(req.GetEmail())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid email: %v", err)
	}

	if err := s.repo.Save(ctx, identity); err != nil {
		if errors.Is(err, domain.ErrEmailAlreadyExists) {
			return nil, status.Errorf(codes.AlreadyExists, "email already exists: %s", req.GetEmail())
		}
		s.logger.ErrorContext(ctx, "failed to save invited identity",
			"email", req.GetEmail(),
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to create identity")
	}

	invitation, _, err := domain.NewInvitation(identity.ID(), inviterID)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to create invitation",
			"identity_id", identity.ID(),
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to create invitation")
	}

	if err := s.repo.SaveInvitation(ctx, invitation); err != nil {
		s.logger.ErrorContext(ctx, "failed to save invitation",
			"identity_id", identity.ID(),
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to save invitation")
	}

	// Grant the initial role if specified.
	if req.GetRole() != pb.Role_ROLE_UNSPECIFIED {
		granterRole := s.getCallerHighestRole(ctx)
		targetRole := protoRoleToDomain(req.GetRole())
		assignment, roleErr := domain.NewRoleAssignment(identity.ID(), inviterID, granterRole, targetRole)
		if roleErr != nil {
			s.logger.WarnContext(ctx, "failed to grant initial role during invitation",
				"identity_id", identity.ID(),
				"role", targetRole,
				"error", roleErr)
		} else {
			if saveErr := s.repo.SaveRoleAssignment(ctx, assignment); saveErr != nil {
				s.logger.WarnContext(ctx, "failed to save initial role assignment",
					"identity_id", identity.ID(),
					"error", saveErr)
			}
		}
	}

	return &pb.InviteUserResponse{
		Invitation: invitationToProto(invitation),
		Identity:   identityToProto(identity),
	}, nil
}

// AcceptInvitation accepts a pending invitation and activates the identity.
func (s *Service) AcceptInvitation(ctx context.Context, req *pb.AcceptInvitationRequest) (*pb.AcceptInvitationResponse, error) {
	if err := credentials.ValidatePasswordPolicy(req.GetPassword()); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "password policy violation: %v", err)
	}

	tokenHash := tokens.HashToken(req.GetToken())
	invitation, err := s.repo.FindInvitationByTokenHash(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, domain.ErrInvitationNotFound) {
			return nil, status.Errorf(codes.NotFound, "invalid or expired invitation token")
		}
		s.logger.ErrorContext(ctx, "failed to find invitation by token", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to validate invitation token")
	}

	if err := invitation.Accept(); err != nil {
		if errors.Is(err, domain.ErrInvitationExpired) {
			return nil, status.Errorf(codes.FailedPrecondition, "invitation has expired")
		}
		if errors.Is(err, domain.ErrInvitationAlreadyAccepted) {
			return nil, status.Errorf(codes.FailedPrecondition, "invitation has already been accepted")
		}
		return nil, status.Errorf(codes.Internal, "failed to accept invitation")
	}

	identity, err := s.repo.FindByID(ctx, invitation.IdentityID())
	if err != nil {
		return nil, mapDomainError(err, "identity")
	}

	hash, err := credentials.HashPassword(req.GetPassword())
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to hash password", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to set password")
	}

	if err := identity.SetPassword(hash); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to set password on identity")
	}

	if err := identity.Activate(); err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "cannot activate identity: %v", err)
	}

	if err := s.repo.Save(ctx, identity); err != nil {
		s.logger.ErrorContext(ctx, "failed to save identity after invitation acceptance",
			"identity_id", identity.ID(),
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to save identity")
	}

	if err := s.repo.SaveInvitation(ctx, invitation); err != nil {
		s.logger.ErrorContext(ctx, "failed to save accepted invitation",
			"invitation_id", invitation.ID(),
			"error", err)
	}

	return &pb.AcceptInvitationResponse{
		Identity: identityToProto(identity),
	}, nil
}

// --- Lifecycle Management ---

// SuspendIdentity suspends an active identity.
func (s *Service) SuspendIdentity(ctx context.Context, req *pb.SuspendIdentityRequest) (*pb.SuspendIdentityResponse, error) {
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid identity ID: %v", err)
	}

	identity, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, mapDomainError(err, "identity")
	}

	if err := identity.Suspend(); err != nil {
		if errors.Is(err, domain.ErrInvalidStatusTransition) {
			return nil, status.Errorf(codes.FailedPrecondition, "cannot suspend identity in %s status", identity.Status())
		}
		return nil, status.Errorf(codes.Internal, "failed to suspend identity")
	}

	if err := s.repo.Save(ctx, identity); err != nil {
		s.logger.ErrorContext(ctx, "failed to save suspended identity",
			"identity_id", id,
			"reason", req.GetReason(),
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to save identity")
	}

	s.logger.InfoContext(ctx, "identity suspended",
		"identity_id", id,
		"reason", req.GetReason())

	return &pb.SuspendIdentityResponse{
		Identity: identityToProto(identity),
	}, nil
}

// ReactivateIdentity reactivates a suspended identity.
func (s *Service) ReactivateIdentity(ctx context.Context, req *pb.ReactivateIdentityRequest) (*pb.ReactivateIdentityResponse, error) {
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid identity ID: %v", err)
	}

	identity, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, mapDomainError(err, "identity")
	}

	if err := identity.Activate(); err != nil {
		if errors.Is(err, domain.ErrInvalidStatusTransition) {
			return nil, status.Errorf(codes.FailedPrecondition, "cannot reactivate identity in %s status", identity.Status())
		}
		return nil, status.Errorf(codes.Internal, "failed to reactivate identity")
	}

	if err := s.repo.Save(ctx, identity); err != nil {
		s.logger.ErrorContext(ctx, "failed to save reactivated identity",
			"identity_id", id,
			"reason", req.GetReason(),
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to save identity")
	}

	s.logger.InfoContext(ctx, "identity reactivated",
		"identity_id", id,
		"reason", req.GetReason())

	return &pb.ReactivateIdentityResponse{
		Identity: identityToProto(identity),
	}, nil
}

// --- Health Check ---

// Check implements grpc_health_v1.HealthServer.
func (s *Service) Check(_ context.Context, _ *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	return &grpc_health_v1.HealthCheckResponse{
		Status: grpc_health_v1.HealthCheckResponse_SERVING,
	}, nil
}

// Watch implements grpc_health_v1.HealthServer (streaming, not supported).
func (s *Service) Watch(_ *grpc_health_v1.HealthCheckRequest, _ grpc_health_v1.Health_WatchServer) error {
	return status.Errorf(codes.Unimplemented, "watch is not supported")
}

// --- Helpers ---

// getCallerHighestRole extracts the caller's highest role from context.
// Returns empty string if no roles are found.
func (s *Service) getCallerHighestRole(ctx context.Context) string {
	roles, ok := auth.GetRolesFromContext(ctx)
	if !ok || len(roles) == 0 {
		return ""
	}
	// Return the last role, which by convention is the highest in the list.
	// The auth interceptor orders roles by privilege level.
	highest := roles[0]
	for _, r := range roles[1:] {
		if domain.CanGrant(r, highest) {
			highest = r
		}
	}
	return highest
}

// mapDomainError maps domain-layer errors to gRPC status errors.
func mapDomainError(err error, entity string) error {
	switch {
	case errors.Is(err, domain.ErrIdentityNotFound):
		return status.Errorf(codes.NotFound, "%s not found", entity)
	case errors.Is(err, domain.ErrEmailAlreadyExists):
		return status.Errorf(codes.AlreadyExists, "email already exists")
	case errors.Is(err, domain.ErrAccountLocked):
		return status.Errorf(codes.FailedPrecondition, "account is locked")
	case errors.Is(err, domain.ErrInvalidStatusTransition):
		return status.Errorf(codes.FailedPrecondition, "invalid status transition")
	case errors.Is(err, domain.ErrInvitationNotFound):
		return status.Errorf(codes.NotFound, "invitation not found")
	case errors.Is(err, domain.ErrInvitationExpired):
		return status.Errorf(codes.FailedPrecondition, "invitation has expired")
	case errors.Is(err, domain.ErrInvitationAlreadyAccepted):
		return status.Errorf(codes.FailedPrecondition, "invitation has already been accepted")
	case errors.Is(err, persistence.ErrVersionConflict):
		return status.Errorf(codes.Aborted, "version conflict: resource was modified by another transaction")
	default:
		return status.Errorf(codes.Internal, "internal error")
	}
}
