package service

import (
	"context"
	"errors"

	pb "github.com/meridianhub/meridian/api/proto/meridian/tenant/v1"
	"github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ReconcileMigrations applies new migrations to existing tenant schemas.
// When services add new migrations after tenants are created, existing tenant
// schemas may be missing these migrations. This operation detects and applies
// new migrations to bring tenant schemas up to date.
//
// This endpoint requires platform-admin or super-admin role authorization.
// Migration reconciliation is a platform-layer operation that affects all tenants
// globally, equivalent to DBA-level privileges.
func (s *Service) ReconcileMigrations(ctx context.Context, req *pb.ReconcileMigrationsRequest) (*pb.ReconcileMigrationsResponse, error) {
	// Authorization check - must be performed before any business logic.
	// ReconcileMigrations is a platform-layer operation requiring elevated privileges.
	claims, ok := auth.GetClaimsFromContext(ctx)
	if !ok {
		s.logger.Warn("migration reconciliation attempted without authentication claims")
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}

	if !claims.HasRole(auth.RolePlatformAdmin.String()) && !claims.HasRole(auth.RoleSuperAdmin.String()) {
		s.logger.Warn("unauthorized migration reconciliation attempt",
			"user_id", claims.UserID,
			"roles", claims.Roles)
		return nil, status.Error(codes.PermissionDenied, "platform-admin or super-admin role required for migration operations")
	}

	s.logger.Info("migration reconciliation authorized",
		"user_id", claims.UserID,
		"roles", claims.Roles)

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

// createProvisioningStatusRecords initializes tracking records for each schema that needs provisioning.
// This is a best-effort operation - failure is logged but does not fail the tenant creation.
// The async worker will handle provisioning regardless of whether these records exist.
func (s *Service) createProvisioningStatusRecords(ctx context.Context, tenantID tenant.TenantID) error {
	// Get the list of required schemas from the provisioner
	schemas := s.provisioner.GetRequiredSchemas()
	if len(schemas) == 0 {
		s.logger.Debug("no schemas require provisioning",
			"tenant_id", tenantID.String())
		return nil
	}

	s.logger.Debug("creating provisioning status records",
		"tenant_id", tenantID.String(),
		"schema_count", len(schemas))

	// Initialize provisioning status record with 'pending' state
	// This creates the tenant_provisioning record with all service_schemas entries
	if err := s.provisioner.InitializeProvisioningStatus(ctx, tenantID); err != nil {
		s.logger.Error("failed to initialize provisioning status",
			"tenant_id", tenantID.String(),
			"error", err)
		return err
	}

	s.logger.Debug("provisioning status records initialized",
		"tenant_id", tenantID.String(),
		"schemas", schemas)

	return nil
}

// GetTenantProvisioningStatus retrieves detailed provisioning status for a tenant.
// Returns per-service provisioning progress including migration versions and error details.
//
// This endpoint requires either:
// - Authenticated user with tenant_id claim matching the requested tenant (tenant isolation)
// - OR platform-admin or super-admin role (cross-tenant access)
func (s *Service) GetTenantProvisioningStatus(ctx context.Context, req *pb.GetTenantProvisioningStatusRequest) (*pb.GetTenantProvisioningStatusResponse, error) {
	s.logger.Debug("getting tenant provisioning status",
		"tenant_id", req.TenantId)

	// Authorization check - when auth middleware is configured, enforce tenant isolation.
	// When no claims are present (e.g., unified binary without auth middleware), skip
	// authorization consistent with other tenant endpoints like RetrieveTenant.
	claims, ok := auth.GetClaimsFromContext(ctx)
	if ok {
		// Check authorization: either tenant-scoped access OR platform admin role
		hasAdminRole := claims.HasRole(auth.RolePlatformAdmin.String()) || claims.HasRole(auth.RoleSuperAdmin.String())
		hasTenantAccess := claims.HasTenantID() && claims.TenantID == req.TenantId

		if !hasAdminRole && !hasTenantAccess {
			s.logger.Warn("unauthorized provisioning status query attempt",
				"user_id", claims.UserID,
				"requested_tenant", req.TenantId,
				"user_tenant", claims.TenantID,
				"roles", claims.Roles)
			return nil, status.Error(codes.PermissionDenied, "access denied: must be tenant owner or platform administrator")
		}

		s.logger.Debug("provisioning status query authorized",
			"user_id", claims.UserID,
			"tenant_id", req.TenantId,
			"admin_access", hasAdminRole)
	}

	// Validate tenant ID
	tenantID, err := tenant.NewTenantID(req.TenantId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid tenant ID: %v", err)
	}

	// Retrieve tenant to get overall status
	tenant, err := s.repo.GetByID(ctx, tenantID)
	if err != nil {
		if errors.Is(err, persistence.ErrTenantNotFound) {
			return nil, status.Errorf(codes.NotFound, "tenant %s not found", req.TenantId)
		}
		s.logger.Error("failed to retrieve tenant for provisioning status",
			"tenant_id", req.TenantId,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to retrieve tenant")
	}

	// Query tenant_provisioning_status table for all service records
	provisioningStatuses, err := s.repo.FindProvisioningStatusByTenantID(ctx, req.TenantId)
	if err != nil {
		s.logger.Error("failed to retrieve provisioning status records",
			"tenant_id", req.TenantId,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to retrieve provisioning status")
	}

	// Build ServiceProvisioningStatus slice from database results
	serviceStatuses := make([]*pb.ServiceProvisioningStatus, 0, len(provisioningStatuses))
	for _, ps := range provisioningStatuses {
		serviceStatus := &pb.ServiceProvisioningStatus{
			ServiceName:      ps.ServiceName,
			Status:           s.toProtoServiceStatus(ps.Status),
			MigrationVersion: ps.MigrationVersion,
		}

		// Set optional error_message
		if ps.ErrorMessage != nil {
			serviceStatus.ErrorMessage = *ps.ErrorMessage
		}

		// Set optional timestamps
		if ps.StartedAt != nil {
			serviceStatus.StartedAt = timestamppb.New(*ps.StartedAt)
		}
		if ps.CompletedAt != nil {
			serviceStatus.CompletedAt = timestamppb.New(*ps.CompletedAt)
		}

		serviceStatuses = append(serviceStatuses, serviceStatus)
	}

	s.logger.Debug("tenant provisioning status retrieved",
		"tenant_id", req.TenantId,
		"overall_status", tenant.Status,
		"service_count", len(serviceStatuses))

	// Construct response
	return &pb.GetTenantProvisioningStatusResponse{
		TenantId:      req.TenantId,
		OverallStatus: s.toProtoStatus(tenant.Status),
		Services:      serviceStatuses,
		ErrorMessage:  tenant.ErrorMessage,
	}, nil
}
