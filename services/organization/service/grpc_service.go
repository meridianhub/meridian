// Package service implements the OrganizationService gRPC server.
package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	pb "github.com/meridianhub/meridian/api/proto/meridian/organization/v1"
	"github.com/meridianhub/meridian/services/organization/adapters/persistence"
	"github.com/meridianhub/meridian/services/organization/domain"
	"github.com/meridianhub/meridian/shared/platform/organization"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ErrUnknownStatus indicates an unspecified or unknown organization status was provided.
var ErrUnknownStatus = errors.New("unspecified or unknown organization status")

// Service implements the OrganizationService gRPC server.
type Service struct {
	pb.UnimplementedOrganizationServiceServer
	repo   *persistence.Repository
	logger *slog.Logger
}

// NewService creates a new OrganizationService.
func NewService(repo *persistence.Repository, logger *slog.Logger) *Service {
	return &Service{
		repo:   repo,
		logger: logger,
	}
}

// InitiateOrganization creates a new organization in the platform registry (BIAN: Initiate).
func (s *Service) InitiateOrganization(ctx context.Context, req *pb.InitiateOrganizationRequest) (*pb.InitiateOrganizationResponse, error) {
	// Validate and create organization ID
	orgID, err := organization.NewOrganizationID(req.OrganizationId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid organization ID: %v", err)
	}

	// Convert metadata from protobuf Struct
	var metadata map[string]interface{}
	if req.Metadata != nil {
		metadata = req.Metadata.AsMap()
	}

	// Create domain organization
	org := &domain.Organization{
		ID:              orgID,
		DisplayName:     req.DisplayName,
		SettlementAsset: req.SettlementAsset,
		Subdomain:       req.Subdomain,
		Status:          domain.StatusActive,
		CreatedAt:       time.Now(),
		Metadata:        metadata,
		Version:         1,
	}

	// Persist organization
	if err := s.repo.Create(ctx, org); err != nil {
		if errors.Is(err, persistence.ErrOrganizationExists) {
			return nil, status.Errorf(codes.AlreadyExists, "organization %s already exists", req.OrganizationId)
		}
		if errors.Is(err, persistence.ErrSubdomainTaken) {
			return nil, status.Errorf(codes.AlreadyExists, "subdomain %s is already taken", req.Subdomain)
		}
		s.logger.Error("failed to create organization",
			"organization_id", req.OrganizationId,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to create organization")
	}

	s.logger.Info("organization created",
		"organization_id", org.ID.String(),
		"display_name", org.DisplayName,
		"settlement_asset", org.SettlementAsset)

	return &pb.InitiateOrganizationResponse{
		Organization: s.toProto(org),
	}, nil
}

// RetrieveOrganization gets organization details by ID (BIAN: Retrieve).
func (s *Service) RetrieveOrganization(ctx context.Context, req *pb.RetrieveOrganizationRequest) (*pb.RetrieveOrganizationResponse, error) {
	// Validate organization ID
	orgID, err := organization.NewOrganizationID(req.OrganizationId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid organization ID: %v", err)
	}

	// Retrieve organization
	org, err := s.repo.GetByID(ctx, orgID)
	if err != nil {
		if errors.Is(err, persistence.ErrOrganizationNotFound) {
			return nil, status.Errorf(codes.NotFound, "organization %s not found", req.OrganizationId)
		}
		s.logger.Error("failed to retrieve organization",
			"organization_id", req.OrganizationId,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to retrieve organization")
	}

	return &pb.RetrieveOrganizationResponse{
		Organization: s.toProto(org),
	}, nil
}

// UpdateOrganizationStatus changes the lifecycle status of an organization (BIAN: Update).
func (s *Service) UpdateOrganizationStatus(ctx context.Context, req *pb.UpdateOrganizationStatusRequest) (*pb.UpdateOrganizationStatusResponse, error) {
	// Validate organization ID
	orgID, err := organization.NewOrganizationID(req.OrganizationId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid organization ID: %v", err)
	}

	// Convert proto status to domain status
	domainStatus, err := s.toDomainStatus(req.Status)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid status: %v", err)
	}

	// Get current organization to get version
	currentOrg, err := s.repo.GetByID(ctx, orgID)
	if err != nil {
		if errors.Is(err, persistence.ErrOrganizationNotFound) {
			return nil, status.Errorf(codes.NotFound, "organization %s not found", req.OrganizationId)
		}
		s.logger.Error("failed to get organization for status update",
			"organization_id", req.OrganizationId,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to update organization status")
	}

	// Update status
	org, err := s.repo.UpdateStatus(ctx, orgID, domainStatus, currentOrg.Version)
	if err != nil {
		if errors.Is(err, persistence.ErrOrganizationNotFound) {
			return nil, status.Errorf(codes.NotFound, "organization %s not found", req.OrganizationId)
		}
		if errors.Is(err, persistence.ErrVersionConflict) {
			return nil, status.Errorf(codes.Aborted, "concurrent modification detected, please retry")
		}
		s.logger.Error("failed to update organization status",
			"organization_id", req.OrganizationId,
			"status", req.Status,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to update organization status")
	}

	s.logger.Info("organization status updated",
		"organization_id", org.ID.String(),
		"old_status", currentOrg.Status,
		"new_status", org.Status)

	return &pb.UpdateOrganizationStatusResponse{
		Organization: s.toProto(org),
	}, nil
}

// ListOrganizations returns all organizations with optional status filter (BIAN: Control).
func (s *Service) ListOrganizations(ctx context.Context, req *pb.ListOrganizationsRequest) (*pb.ListOrganizationsResponse, error) {
	// Convert proto status filter to domain status
	var statusFilter *domain.Status
	if req.StatusFilter != pb.OrganizationStatus_ORGANIZATION_STATUS_UNSPECIFIED {
		domainStatus, err := s.toDomainStatus(req.StatusFilter)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid status filter: %v", err)
		}
		statusFilter = &domainStatus
	}

	// List organizations
	orgs, nextPageToken, err := s.repo.List(ctx, statusFilter, int(req.PageSize), req.PageToken)
	if err != nil {
		s.logger.Error("failed to list organizations",
			"status_filter", req.StatusFilter,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to list organizations")
	}

	// Convert to proto
	protoOrgs := make([]*pb.Organization, 0, len(orgs))
	for _, org := range orgs {
		protoOrgs = append(protoOrgs, s.toProto(org))
	}

	return &pb.ListOrganizationsResponse{
		Organizations: protoOrgs,
		NextPageToken: nextPageToken,
	}, nil
}

// toProto converts a domain organization to protobuf.
func (s *Service) toProto(org *domain.Organization) *pb.Organization {
	proto := &pb.Organization{
		OrganizationId:  org.ID.String(),
		DisplayName:     org.DisplayName,
		SettlementAsset: org.SettlementAsset,
		Subdomain:       org.Subdomain,
		Status:          s.toProtoStatus(org.Status),
		CreatedAt:       timestamppb.New(org.CreatedAt),
		Version:         int32(org.Version),
	}

	if org.DeprovisionedAt != nil {
		proto.DeprovisionedAt = timestamppb.New(*org.DeprovisionedAt)
	}

	if org.Metadata != nil {
		if metadata, err := structpb.NewStruct(org.Metadata); err == nil {
			proto.Metadata = metadata
		}
	}

	return proto
}

// toProtoStatus converts domain status to protobuf status.
func (s *Service) toProtoStatus(status domain.Status) pb.OrganizationStatus {
	switch status {
	case domain.StatusActive:
		return pb.OrganizationStatus_ORGANIZATION_STATUS_ACTIVE
	case domain.StatusSuspended:
		return pb.OrganizationStatus_ORGANIZATION_STATUS_SUSPENDED
	case domain.StatusDeprovisioned:
		return pb.OrganizationStatus_ORGANIZATION_STATUS_DEPROVISIONED
	default:
		return pb.OrganizationStatus_ORGANIZATION_STATUS_UNSPECIFIED
	}
}

// toDomainStatus converts protobuf status to domain status.
func (s *Service) toDomainStatus(status pb.OrganizationStatus) (domain.Status, error) {
	switch status {
	case pb.OrganizationStatus_ORGANIZATION_STATUS_ACTIVE:
		return domain.StatusActive, nil
	case pb.OrganizationStatus_ORGANIZATION_STATUS_SUSPENDED:
		return domain.StatusSuspended, nil
	case pb.OrganizationStatus_ORGANIZATION_STATUS_DEPROVISIONED:
		return domain.StatusDeprovisioned, nil
	case pb.OrganizationStatus_ORGANIZATION_STATUS_UNSPECIFIED:
		return "", ErrUnknownStatus
	default:
		return "", ErrUnknownStatus
	}
}
