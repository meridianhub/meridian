// Package service implements the TenantService gRPC server.
package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/tenant/v1"
	"github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	"github.com/meridianhub/meridian/services/tenant/clients"
	"github.com/meridianhub/meridian/services/tenant/domain"
	"github.com/meridianhub/meridian/services/tenant/provisioner"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ErrUnknownStatus indicates an unspecified or unknown tenant status was provided.
var ErrUnknownStatus = errors.New("unspecified or unknown tenant status")

// Service implements the TenantService gRPC server.
type Service struct {
	pb.UnimplementedTenantServiceServer
	repo        *persistence.Repository
	provisioner provisioner.SchemaProvisioner
	partyClient clients.PartyClient
	logger      *slog.Logger
}

// NewService creates a new TenantService.
// The provisioner parameter is optional; if nil, schema provisioning is skipped during tenant creation.
// The partyClient parameter is optional; if nil, party registration is skipped during tenant creation.
func NewService(repo *persistence.Repository, prov provisioner.SchemaProvisioner, partyClient clients.PartyClient, logger *slog.Logger) *Service {
	return &Service{
		repo:        repo,
		provisioner: prov,
		partyClient: partyClient,
		logger:      logger,
	}
}

// InitiateTenant creates a new tenant in the platform registry (BIAN: Initiate).
// If a provisioner is configured, this also provisions schemas for the tenant.
// If a Party client is configured, this also registers a corresponding Party in the
// BIAN Party Reference Data Directory, establishing the link between platform
// infrastructure (Tenant) and BIAN domain entities (Party.Organization).
func (s *Service) InitiateTenant(ctx context.Context, req *pb.InitiateTenantRequest) (*pb.InitiateTenantResponse, error) {
	// Validate and create tenant ID
	tenantID, err := tenant.NewTenantID(req.TenantId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid tenant ID: %v", err)
	}

	// Convert metadata from protobuf Struct
	var metadata map[string]interface{}
	if req.Metadata != nil {
		metadata = req.Metadata.AsMap()
	}

	// Determine initial status based on whether provisioning is configured
	initialStatus := domain.StatusActive
	if s.provisioner != nil {
		initialStatus = domain.StatusProvisioning
	}

	// Create domain tenant
	tenant := &domain.Tenant{
		ID:              tenantID,
		DisplayName:     req.DisplayName,
		SettlementAsset: req.SettlementAsset,
		Subdomain:       req.Subdomain,
		Status:          initialStatus,
		CreatedAt:       time.Now(),
		Metadata:        metadata,
		Version:         1,
	}

	// Register corresponding Party in BIAN Party Reference Data Directory (if client configured).
	//
	// Note: If Party registration succeeds but tenant creation fails, an orphaned Party record
	// may exist. This is acceptable eventual consistency - orphaned parties can be cleaned up
	// operationally via the Party service, and the ExternalReference field allows correlation.
	// A saga pattern with compensation could be added if stricter atomicity is required.
	if s.partyClient != nil {
		party, err := s.partyClient.RegisterParty(ctx, &partyv1.RegisterPartyRequest{
			PartyType:         partyv1.PartyType_PARTY_TYPE_ORGANIZATION,
			LegalName:         req.DisplayName,
			DisplayName:       req.DisplayName,
			ExternalReference: req.TenantId, // Bidirectional link: Party -> Tenant
		})
		if err != nil {
			s.logger.Error("failed to register party for tenant",
				"tenant_id", req.TenantId,
				"error", err)
			return nil, status.Errorf(codes.Internal, "failed to register party for tenant: %v", err)
		}
		tenant.PartyID = party.PartyId
		s.logger.Info("registered party for tenant",
			"tenant_id", req.TenantId,
			"party_id", tenant.PartyID)
	} else {
		s.logger.Debug("party client not configured - skipping party registration",
			"tenant_id", req.TenantId)
	}

	// Persist tenant with initial status (provisioning or active)
	if err := s.repo.Create(ctx, tenant); err != nil {
		if errors.Is(err, persistence.ErrTenantExists) {
			return nil, status.Errorf(codes.AlreadyExists, "tenant %s already exists", req.TenantId)
		}
		if errors.Is(err, persistence.ErrSubdomainTaken) {
			return nil, status.Errorf(codes.AlreadyExists, "subdomain %s is already taken", req.Subdomain)
		}
		s.logger.Error("failed to create tenant",
			"tenant_id", req.TenantId,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to create tenant")
	}

	s.logger.Info("tenant created",
		"tenant_id", tenant.ID.String(),
		"display_name", tenant.DisplayName,
		"settlement_asset", tenant.SettlementAsset,
		"status", tenant.Status,
		"party_id", tenant.PartyID)

	// Provision schemas if provisioner is configured
	if s.provisioner != nil {
		s.logger.Info("provisioning schemas for tenant",
			"tenant_id", tenant.ID.String())

		provErr := s.provisioner.ProvisionSchemas(ctx, tenant.ID)
		if provErr != nil {
			// Mark tenant as provisioning_failed
			s.logger.Error("schema provisioning failed",
				"tenant_id", tenant.ID.String(),
				"error", provErr)

			_, updateErr := s.repo.UpdateStatusWithError(ctx, tenant.ID, domain.StatusProvisioningFailed, provErr.Error(), tenant.Version)
			if updateErr != nil {
				s.logger.Error("failed to update tenant status after provisioning failure",
					"tenant_id", tenant.ID.String(),
					"error", updateErr)
			}

			// Update local tenant state for response
			tenant.Status = domain.StatusProvisioningFailed
			tenant.ErrorMessage = provErr.Error()
			tenant.Version++

			return nil, status.Errorf(codes.Internal, "schema provisioning failed: %v", provErr)
		}

		// Activate tenant after successful provisioning
		updatedTenant, updateErr := s.repo.UpdateStatus(ctx, tenant.ID, domain.StatusActive, tenant.Version)
		if updateErr != nil {
			s.logger.Error("failed to activate tenant after provisioning",
				"tenant_id", tenant.ID.String(),
				"error", updateErr)
			return nil, status.Errorf(codes.Internal, "failed to activate tenant after provisioning")
		}

		tenant = updatedTenant
		s.logger.Info("tenant provisioned and activated",
			"tenant_id", tenant.ID.String(),
			"status", tenant.Status)
	}

	return &pb.InitiateTenantResponse{
		Tenant: s.toProto(tenant),
	}, nil
}

// RetrieveTenant gets tenant details by ID (BIAN: Retrieve).
func (s *Service) RetrieveTenant(ctx context.Context, req *pb.RetrieveTenantRequest) (*pb.RetrieveTenantResponse, error) {
	// Validate tenant ID
	tenantID, err := tenant.NewTenantID(req.TenantId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid tenant ID: %v", err)
	}

	// Retrieve tenant
	tenant, err := s.repo.GetByID(ctx, tenantID)
	if err != nil {
		if errors.Is(err, persistence.ErrTenantNotFound) {
			return nil, status.Errorf(codes.NotFound, "tenant %s not found", req.TenantId)
		}
		s.logger.Error("failed to retrieve tenant",
			"tenant_id", req.TenantId,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to retrieve tenant")
	}

	return &pb.RetrieveTenantResponse{
		Tenant: s.toProto(tenant),
	}, nil
}

// UpdateTenantStatus changes the lifecycle status of a tenant (BIAN: Update).
func (s *Service) UpdateTenantStatus(ctx context.Context, req *pb.UpdateTenantStatusRequest) (*pb.UpdateTenantStatusResponse, error) {
	// Validate tenant ID
	tenantID, err := tenant.NewTenantID(req.TenantId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid tenant ID: %v", err)
	}

	// Convert proto status to domain status
	domainStatus, err := s.toDomainStatus(req.Status)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid status: %v", err)
	}

	// Get current tenant to get version and validate transition
	currentTenant, err := s.repo.GetByID(ctx, tenantID)
	if err != nil {
		if errors.Is(err, persistence.ErrTenantNotFound) {
			return nil, status.Errorf(codes.NotFound, "tenant %s not found", req.TenantId)
		}
		s.logger.Error("failed to get tenant for status update",
			"tenant_id", req.TenantId,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to update tenant status")
	}

	// Validate status transition
	if !currentTenant.CanTransitionTo(domainStatus) {
		return nil, status.Errorf(codes.FailedPrecondition,
			"invalid status transition from %s to %s", currentTenant.Status, domainStatus)
	}

	// Update status
	tenant, err := s.repo.UpdateStatus(ctx, tenantID, domainStatus, currentTenant.Version)
	if err != nil {
		if errors.Is(err, persistence.ErrTenantNotFound) {
			return nil, status.Errorf(codes.NotFound, "tenant %s not found", req.TenantId)
		}
		if errors.Is(err, persistence.ErrVersionConflict) {
			return nil, status.Errorf(codes.Aborted, "concurrent modification detected, please retry")
		}
		s.logger.Error("failed to update tenant status",
			"tenant_id", req.TenantId,
			"status", req.Status,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to update tenant status")
	}

	s.logger.Info("tenant status updated",
		"tenant_id", tenant.ID.String(),
		"old_status", currentTenant.Status,
		"new_status", tenant.Status)

	return &pb.UpdateTenantStatusResponse{
		Tenant: s.toProto(tenant),
	}, nil
}

// ListTenants returns all tenants with optional status filter (BIAN: Control).
func (s *Service) ListTenants(ctx context.Context, req *pb.ListTenantsRequest) (*pb.ListTenantsResponse, error) {
	// Convert proto status filter to domain status
	var statusFilter *domain.Status
	if req.StatusFilter != pb.TenantStatus_TENANT_STATUS_UNSPECIFIED {
		domainStatus, err := s.toDomainStatus(req.StatusFilter)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid status filter: %v", err)
		}
		statusFilter = &domainStatus
	}

	// List tenants
	tenants, nextPageToken, err := s.repo.List(ctx, statusFilter, int(req.PageSize), req.PageToken)
	if err != nil {
		s.logger.Error("failed to list tenants",
			"status_filter", req.StatusFilter,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to list tenants")
	}

	// Convert to proto
	protoTenants := make([]*pb.Tenant, 0, len(tenants))
	for _, tenant := range tenants {
		protoTenants = append(protoTenants, s.toProto(tenant))
	}

	return &pb.ListTenantsResponse{
		Tenants:       protoTenants,
		NextPageToken: nextPageToken,
	}, nil
}

// toProto converts a domain tenant to protobuf.
func (s *Service) toProto(tenant *domain.Tenant) *pb.Tenant {
	proto := &pb.Tenant{
		TenantId:        tenant.ID.String(),
		DisplayName:     tenant.DisplayName,
		SettlementAsset: tenant.SettlementAsset,
		Subdomain:       tenant.Subdomain,
		Status:          s.toProtoStatus(tenant.Status),
		CreatedAt:       timestamppb.New(tenant.CreatedAt),
		Version:         int32(tenant.Version),
		PartyId:         tenant.PartyID,
		ErrorMessage:    tenant.ErrorMessage,
	}

	if tenant.DeprovisionedAt != nil {
		proto.DeprovisionedAt = timestamppb.New(*tenant.DeprovisionedAt)
	}

	if tenant.Metadata != nil {
		if metadata, err := structpb.NewStruct(tenant.Metadata); err == nil {
			proto.Metadata = metadata
		}
	}

	return proto
}

// toProtoStatus converts domain status to protobuf status.
func (s *Service) toProtoStatus(status domain.Status) pb.TenantStatus {
	switch status {
	case domain.StatusProvisioning:
		return pb.TenantStatus_TENANT_STATUS_PROVISIONING
	case domain.StatusProvisioningFailed:
		return pb.TenantStatus_TENANT_STATUS_PROVISIONING_FAILED
	case domain.StatusActive:
		return pb.TenantStatus_TENANT_STATUS_ACTIVE
	case domain.StatusSuspended:
		return pb.TenantStatus_TENANT_STATUS_SUSPENDED
	case domain.StatusDeprovisioned:
		return pb.TenantStatus_TENANT_STATUS_DEPROVISIONED
	default:
		return pb.TenantStatus_TENANT_STATUS_UNSPECIFIED
	}
}

// toDomainStatus converts protobuf status to domain status.
func (s *Service) toDomainStatus(status pb.TenantStatus) (domain.Status, error) {
	switch status {
	case pb.TenantStatus_TENANT_STATUS_PROVISIONING:
		return domain.StatusProvisioning, nil
	case pb.TenantStatus_TENANT_STATUS_PROVISIONING_FAILED:
		return domain.StatusProvisioningFailed, nil
	case pb.TenantStatus_TENANT_STATUS_ACTIVE:
		return domain.StatusActive, nil
	case pb.TenantStatus_TENANT_STATUS_SUSPENDED:
		return domain.StatusSuspended, nil
	case pb.TenantStatus_TENANT_STATUS_DEPROVISIONED:
		return domain.StatusDeprovisioned, nil
	case pb.TenantStatus_TENANT_STATUS_UNSPECIFIED:
		return "", ErrUnknownStatus
	default:
		return "", ErrUnknownStatus
	}
}

// ReconcileMigrations applies new migrations to existing tenant schemas.
// When services add new migrations after tenants are created, existing tenant
// schemas may be missing these migrations. This operation detects and applies
// new migrations to bring tenant schemas up to date.
func (s *Service) ReconcileMigrations(ctx context.Context, req *pb.ReconcileMigrationsRequest) (*pb.ReconcileMigrationsResponse, error) {
	if s.provisioner == nil {
		return nil, status.Error(codes.FailedPrecondition, "schema provisioning not enabled")
	}

	// Parse tenant ID if provided
	var tenantID *tenant.TenantID
	if req.TenantId != "" {
		tid, err := tenant.NewTenantID(req.TenantId)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid tenant_id: %v", err)
		}
		tenantID = &tid
	}

	// Perform reconciliation
	reconciledCount, errs := s.provisioner.ReconcileMigrations(ctx, tenantID)

	s.logger.Info("migration reconciliation completed",
		"tenant_id", req.TenantId,
		"reconciled_count", reconciledCount,
		"error_count", len(errs))

	return &pb.ReconcileMigrationsResponse{
		ReconciledCount: int32(reconciledCount),
		Errors:          errs,
	}, nil
}
