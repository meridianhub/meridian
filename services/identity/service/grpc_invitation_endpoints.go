package service

import (
	"context"
	"errors"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/identity/v1"
	"github.com/meridianhub/meridian/services/identity/domain"
	"github.com/meridianhub/meridian/shared/pkg/credentials"
	"github.com/meridianhub/meridian/shared/pkg/tokens"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

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

	tenantID, err := tenant.RequireFromContext(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "missing tenant context")
	}

	identity, err := domain.NewIdentity(tenantID, req.GetEmail())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid email: %v", err)
	}

	invitation, plaintextToken, err := domain.NewInvitation(identity.ID(), inviterID)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to create invitation",
			"identity_id", identity.ID(),
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to create invitation")
	}

	if err := s.repo.SaveIdentityWithInvitation(ctx, identity, invitation); err != nil {
		s.logger.ErrorContext(ctx, "failed to save identity and invitation",
			"identity_id", identity.ID(),
			"invitation_id", invitation.ID(),
			"error", err)
		return nil, mapDomainError(err, "identity")
	}

	s.logger.InfoContext(ctx, "invitation created",
		"identity_id", identity.ID())

	s.queueInvitationEmail(ctx, identity, inviterID, plaintextToken, tenantID)

	// Grant the initial role if specified.
	if req.GetRole() != pb.Role_ROLE_UNSPECIFIED {
		granterRole := s.getCallerHighestRole(ctx)
		targetRole := protoRoleToDomain(req.GetRole())
		assignment, roleErr := domain.NewRoleAssignment(tenantID, identity.ID(), inviterID, granterRole, targetRole)
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
		Invitation:      invitationToProto(invitation),
		Identity:        identityToProto(identity),
		InvitationToken: plaintextToken,
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

	if err := s.repo.SaveIdentityWithInvitation(ctx, identity, invitation); err != nil {
		s.logger.ErrorContext(ctx, "failed to save identity and invitation after acceptance",
			"identity_id", identity.ID(),
			"invitation_id", invitation.ID(),
			"error", err)
		return nil, mapDomainError(err, "identity")
	}

	return &pb.AcceptInvitationResponse{
		Identity: identityToProto(identity),
	}, nil
}
