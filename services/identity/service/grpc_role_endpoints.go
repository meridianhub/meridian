package service

import (
	"context"
	"errors"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/identity/v1"
	"github.com/meridianhub/meridian/services/identity/domain"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

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

	tenantID, err := tenant.RequireFromContext(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "missing tenant context")
	}

	granterRole := s.getCallerHighestRole(ctx)
	targetRole := protoRoleToDomain(req.GetRole())

	assignment, err := domain.NewRoleAssignment(tenantID, identityID, granterID, granterRole, targetRole)
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
	revokerID, identityID, assignmentID, err := validateRevokeRoleRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	target, err := s.findRoleAssignment(ctx, identityID, assignmentID)
	if err != nil {
		return nil, err
	}

	// Enforce role hierarchy: revoker must hold a higher privilege than the target role.
	revokerRole := s.getCallerHighestRole(ctx)
	if !domain.CanGrant(revokerRole, string(target.Role())) {
		return nil, status.Errorf(codes.PermissionDenied, "insufficient permissions to revoke this role")
	}

	if err := target.Revoke(revokerID); err != nil {
		if errors.Is(err, domain.ErrRoleAlreadyRevoked) {
			return nil, status.Errorf(codes.FailedPrecondition, "role assignment has already been revoked")
		}
		return nil, status.Errorf(codes.Internal, "failed to revoke role")
	}

	if err := s.repo.SaveRoleAssignment(ctx, target); err != nil {
		s.logger.ErrorContext(ctx, "failed to save revoked role assignment",
			"assignment_id", assignmentID, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to save role revocation")
	}

	return &pb.RevokeRoleResponse{
		RoleAssignment: roleAssignmentToProto(target),
	}, nil
}

// validateRevokeRoleRequest parses and validates the IDs from the revoke role request.
func validateRevokeRoleRequest(ctx context.Context, req *pb.RevokeRoleRequest) (uuid.UUID, uuid.UUID, uuid.UUID, error) {
	callerID, ok := auth.GetUserIDFromContext(ctx)
	if !ok {
		return uuid.Nil, uuid.Nil, uuid.Nil, status.Errorf(codes.Unauthenticated, "missing authentication context")
	}
	revokerID, err := uuid.Parse(callerID)
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, status.Errorf(codes.Internal, "invalid caller identity ID")
	}
	identityID, err := uuid.Parse(req.GetIdentityId())
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, status.Errorf(codes.InvalidArgument, "invalid identity ID: %v", err)
	}
	assignmentID, err := uuid.Parse(req.GetRoleAssignmentId())
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, status.Errorf(codes.InvalidArgument, "invalid role assignment ID: %v", err)
	}
	return revokerID, identityID, assignmentID, nil
}

// findRoleAssignment looks up a specific role assignment by identity and assignment ID.
func (s *Service) findRoleAssignment(ctx context.Context, identityID, assignmentID uuid.UUID) (*domain.RoleAssignment, error) {
	assignments, err := s.repo.FindRoleAssignments(ctx, identityID)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to find role assignments",
			"identity_id", identityID, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to find role assignments")
	}

	for _, a := range assignments {
		if a.ID() == assignmentID {
			return a, nil
		}
	}

	return nil, status.Errorf(codes.NotFound, "role assignment not found")
}

// ListRoleAssignments lists the role assignments for an identity.
func (s *Service) ListRoleAssignments(ctx context.Context, req *pb.ListRoleAssignmentsRequest) (*pb.ListRoleAssignmentsResponse, error) {
	if _, ok := auth.GetUserIDFromContext(ctx); !ok {
		return nil, status.Errorf(codes.Unauthenticated, "missing authentication context")
	}

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
