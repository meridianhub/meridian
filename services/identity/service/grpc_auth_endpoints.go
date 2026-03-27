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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

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

	return &pb.AuthenticateResponse{
		Identity:      identityToProto(identity),
		Authenticated: true,
	}, nil
}

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

	if err := s.repo.SaveIdentityWithInvitation(ctx, identity, invitation); err != nil {
		s.logger.ErrorContext(ctx, "failed to save identity and invitation after password set",
			"identity_id", identity.ID(),
			"invitation_id", invitation.ID(),
			"error", err)
		return nil, mapDomainError(err, "identity")
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
// Returns success with no token when the email is not found (prevents enumeration).
// Returns an error for unexpected repository failures.
func (s *Service) RequestPasswordReset(ctx context.Context, req *pb.RequestPasswordResetRequest) (*pb.RequestPasswordResetResponse, error) {
	identity, findErr := s.repo.FindByEmail(ctx, req.GetEmail())
	if findErr != nil {
		if errors.Is(findErr, domain.ErrIdentityNotFound) {
			// Return success without a token to prevent email enumeration.
			return &pb.RequestPasswordResetResponse{Email: req.GetEmail()}, nil
		}
		s.logger.ErrorContext(ctx, "unexpected error during password reset lookup",
			"error", findErr)
		return nil, status.Errorf(codes.Internal, "failed to initiate password reset")
	}

	plaintext, invitation, tokenErr := s.createResetInvitation(ctx, identity.ID())
	if tokenErr != nil {
		s.logger.ErrorContext(ctx, "failed to create password reset token",
			"identity_id", identity.ID(),
			"error", tokenErr)
		return nil, status.Errorf(codes.Internal, "failed to create reset token")
	}

	if saveErr := s.repo.SaveInvitation(ctx, invitation); saveErr != nil {
		s.logger.ErrorContext(ctx, "failed to save password reset invitation",
			"identity_id", identity.ID(),
			"error", saveErr)
		return nil, status.Errorf(codes.Internal, "failed to save reset token")
	}

	s.logger.DebugContext(ctx, "password reset token generated",
		"identity_id", identity.ID())

	return &pb.RequestPasswordResetResponse{
		Email:      req.GetEmail(),
		ResetToken: plaintext,
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

	if err := s.repo.SaveIdentityWithInvitation(ctx, identity, invitation); err != nil {
		s.logger.ErrorContext(ctx, "failed to save identity and invitation after password reset",
			"identity_id", identity.ID(),
			"invitation_id", invitation.ID(),
			"error", err)
		return nil, mapDomainError(err, "identity")
	}

	return &pb.CompletePasswordResetResponse{
		IdentityId: identity.ID().String(),
	}, nil
}
