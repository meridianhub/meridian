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

// CreateIdentity creates a new identity in PENDING_INVITE status.
func (s *Service) CreateIdentity(ctx context.Context, req *pb.CreateIdentityRequest) (*pb.CreateIdentityResponse, error) {
	tenantID, err := tenant.RequireFromContext(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "missing tenant context")
	}

	identity, err := domain.NewIdentity(tenantID, req.GetEmail())
	if err != nil {
		s.logger.ErrorContext(ctx, "invalid email for identity creation",
			"error", err)
		return nil, status.Errorf(codes.InvalidArgument, "invalid email: %v", err)
	}

	if err := s.repo.Save(ctx, identity); err != nil {
		if errors.Is(err, domain.ErrEmailAlreadyExists) {
			return nil, status.Errorf(codes.AlreadyExists, "email already registered")
		}
		s.logger.ErrorContext(ctx, "failed to save identity",
			"identity_id", identity.ID(),
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

	// The proto allows email to be sent, but the domain treats email as
	// immutable (set at creation). Reject explicit email change requests.
	if req.GetEmail() != "" && req.GetEmail() != identity.Email() {
		return nil, status.Errorf(codes.FailedPrecondition, "email is immutable and cannot be changed")
	}

	return &pb.UpdateIdentityResponse{
		Identity: identityToProto(identity),
	}, nil
}

// ListIdentities returns all identities within the tenant.
// Pagination and status filtering are not yet implemented.
func (s *Service) ListIdentities(ctx context.Context, req *pb.ListIdentitiesRequest) (*pb.ListIdentitiesResponse, error) {
	tenantID, hasTenant := tenant.FromContext(ctx)
	s.logger.DebugContext(ctx, "ListIdentities called",
		"tenant_id", tenantID,
		"has_tenant_context", hasTenant,
		"page_size", req.GetPageSize(),
		"page_token", req.GetPageToken(),
		"status_filter", req.GetStatusFilter().String())

	if req.GetPageSize() > 0 || req.GetPageToken() != "" || req.GetStatusFilter() != pb.IdentityStatus_IDENTITY_STATUS_UNSPECIFIED {
		return nil, status.Errorf(codes.Unimplemented, "pagination and status filtering are not yet supported")
	}

	identities, err := s.repo.ListByTenant(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to list identities",
			"tenant_id", tenantID,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to list identities")
	}

	pbIdentities := make([]*pb.Identity, 0, len(identities))
	for _, ident := range identities {
		pbIdentities = append(pbIdentities, identityToProto(ident))
	}

	s.logger.DebugContext(ctx, "ListIdentities completed",
		"tenant_id", tenantID,
		"result_count", len(pbIdentities))

	return &pb.ListIdentitiesResponse{
		Identities: pbIdentities,
		TotalCount: int32(len(pbIdentities)),
	}, nil
}

// SuspendIdentity suspends an active identity.
func (s *Service) SuspendIdentity(ctx context.Context, req *pb.SuspendIdentityRequest) (*pb.SuspendIdentityResponse, error) {
	if _, ok := auth.GetUserIDFromContext(ctx); !ok {
		return nil, status.Errorf(codes.Unauthenticated, "missing authentication context")
	}

	callerRole := s.getCallerHighestRole(ctx)
	if !domain.CanGrant(callerRole, string(domain.RoleViewer)) {
		return nil, status.Errorf(codes.PermissionDenied, "insufficient permissions to suspend identities")
	}

	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid identity ID: %v", err)
	}

	identity, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, mapDomainError(err, "identity")
	}

	// Verify caller outranks the target identity's highest role.
	if err := s.verifyCallerOutranksTarget(ctx, id, callerRole); err != nil {
		return nil, err
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
	if _, ok := auth.GetUserIDFromContext(ctx); !ok {
		return nil, status.Errorf(codes.Unauthenticated, "missing authentication context")
	}

	callerRole := s.getCallerHighestRole(ctx)
	if !domain.CanGrant(callerRole, string(domain.RoleViewer)) {
		return nil, status.Errorf(codes.PermissionDenied, "insufficient permissions to reactivate identities")
	}

	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid identity ID: %v", err)
	}

	identity, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, mapDomainError(err, "identity")
	}

	// Verify caller outranks the target identity's highest role.
	if err := s.verifyCallerOutranksTarget(ctx, id, callerRole); err != nil {
		return nil, err
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
