package service

import (
	"context"
	"errors"
	"time"

	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/tenant/v1"
	"github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	"github.com/meridianhub/meridian/services/tenant/domain"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// InitiateTenant creates a new tenant in the platform registry (BIAN: Initiate).
// Returns immediately with 202 Accepted semantics (represented by successful response with PROVISIONING_PENDING status).
// If a provisioner is configured, tenant is created with PROVISIONING_PENDING status and schema provisioning
// happens asynchronously via background worker. If no provisioner is configured, tenant is created as ACTIVE.
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
		initialStatus = domain.StatusProvisioningPending
	}

	// Validate slug if provided
	if req.Slug != "" {
		if err := domain.ValidateSlug(req.Slug); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid slug: %v", err)
		}

		// Check slug availability
		available, err := s.repo.IsSlugAvailable(ctx, req.Slug)
		if err != nil {
			s.logger.Error("failed to check slug availability",
				"slug", req.Slug,
				"error", err)
			return nil, status.Errorf(codes.Internal, "failed to check slug availability")
		}
		if !available {
			return nil, status.Errorf(codes.AlreadyExists, "slug %s is already taken", req.Slug)
		}
	}

	// Create domain tenant
	tenant := &domain.Tenant{
		ID:              tenantID,
		Slug:            req.Slug,
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
		if errors.Is(err, persistence.ErrSlugTaken) {
			return nil, status.Errorf(codes.AlreadyExists, "slug %s is already taken", req.Slug)
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

	// Pre-populate cache for newly created tenant (best-effort)
	// This optimizes the first slug lookup after tenant creation
	if s.slugCache != nil && tenant.Slug != "" {
		if err := s.slugCache.Set(ctx, tenant.Slug, tenant.ID); err != nil {
			s.logger.Warn("failed to pre-populate slug cache for new tenant",
				"tenant_id", tenant.ID.String(),
				"slug", tenant.Slug,
				"error", err)
			// Don't fail tenant creation if cache population fails
		}
	}

	// Schema provisioning will be handled asynchronously by the worker
	if s.provisioner != nil {
		s.logger.Info("tenant created with provisioning_pending status - worker will handle provisioning",
			"tenant_id", tenant.ID.String())

		// Initialize provisioning status records (non-blocking, best-effort)
		if err := s.createProvisioningStatusRecords(ctx, tenantID); err != nil {
			s.logger.Warn("failed to create provisioning status records - worker will handle",
				"tenant_id", tenantID.String(),
				"error", err)
		}
	}

	return &pb.InitiateTenantResponse{
		Tenant:           s.toProto(tenant),
		ProvisioningHint: provisioningHintFromStatus(tenant.Status),
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

// GetBySlug retrieves a tenant by its URL-friendly slug with cache-first lookup.
// This method is used internally by middleware/routing layers for tenant resolution.
// Performance characteristics:
//   - Cache hit: ~1ms (Redis roundtrip)
//   - Cache miss: ~5-10ms (PostgreSQL query + cache population)
//   - Cache TTL: 5 minutes (configurable in SlugCache)
//
// Error handling:
//   - Cache failures are logged but don't fail the request (degrades gracefully to DB)
//   - Returns ErrTenantNotFound if slug doesn't exist in database
func (s *Service) GetBySlug(ctx context.Context, slug string) (*domain.Tenant, error) {
	// Cache-first lookup (if cache is configured)
	if s.slugCache != nil {
		cachedTenantID, err := s.slugCache.Get(ctx, slug)
		if err != nil {
			// Cache read failure - log and continue to DB lookup
			s.logger.Warn("slug cache read failed, falling back to database",
				"slug", slug,
				"error", err)
		} else if cachedTenantID != "" {
			// Cache hit - retrieve full tenant by ID
			tenant, err := s.repo.GetByID(ctx, cachedTenantID)
			if err != nil {
				if errors.Is(err, persistence.ErrTenantNotFound) {
					// Stale cache entry - invalidate and fall through to DB lookup
					s.logger.Warn("stale cache entry detected, invalidating",
						"slug", slug,
						"cached_tenant_id", cachedTenantID)
					if invErr := s.slugCache.Invalidate(ctx, slug); invErr != nil {
						s.logger.Error("failed to invalidate stale cache entry",
							"slug", slug,
							"error", invErr)
					}
				} else {
					// DB error on cache hit - return error
					s.logger.Error("failed to retrieve tenant by cached ID",
						"slug", slug,
						"tenant_id", cachedTenantID,
						"error", err)
					return nil, err
				}
			} else {
				// Cache hit with successful DB lookup
				s.logger.Debug("slug cache hit",
					"slug", slug,
					"tenant_id", cachedTenantID)
				return tenant, nil
			}
		}
	}

	// Cache miss or cache disabled - query database
	tenant, err := s.repo.GetBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, persistence.ErrTenantNotFound) {
			return nil, err
		}
		s.logger.Error("failed to retrieve tenant by slug",
			"slug", slug,
			"error", err)
		return nil, err
	}

	// Populate cache on successful DB lookup (best-effort)
	if s.slugCache != nil {
		if err := s.slugCache.Set(ctx, slug, tenant.ID); err != nil {
			s.logger.Error("failed to populate slug cache after DB lookup",
				"slug", slug,
				"tenant_id", tenant.ID,
				"error", err)
			// Don't fail the request - cache population is best-effort
		} else {
			s.logger.Debug("populated slug cache after DB lookup",
				"slug", slug,
				"tenant_id", tenant.ID)
		}
	}

	return tenant, nil
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

	// Invalidate cache on deprovisioning (tenant becoming inactive)
	// Deprovisioned tenants should not be served from cache
	if s.slugCache != nil && tenant.Status == domain.StatusDeprovisioned && currentTenant.Slug != "" {
		if err := s.slugCache.Invalidate(ctx, currentTenant.Slug); err != nil {
			s.logger.Error("failed to invalidate slug cache after deprovisioning",
				"tenant_id", tenant.ID.String(),
				"slug", currentTenant.Slug,
				"error", err)
			// Don't fail the status update if cache invalidation fails
		} else {
			s.logger.Debug("invalidated slug cache after deprovisioning",
				"tenant_id", tenant.ID.String(),
				"slug", currentTenant.Slug)
		}
	}

	return &pb.UpdateTenantStatusResponse{
		Tenant: s.toProto(tenant),
	}, nil
}

// ListTenants returns all tenants with optional status filter (BIAN: Control).
// When auth is enabled, non-admin users see only their own tenant.
// Platform admins and super admins see all tenants.
func (s *Service) ListTenants(ctx context.Context, req *pb.ListTenantsRequest) (*pb.ListTenantsResponse, error) {
	// RBAC: When claims are present (auth interceptor is active), restrict non-admin
	// users to their own tenant. When no claims are in context (auth disabled or
	// internal calls), fall through to full list for backwards compatibility.
	// This aligns with ReconcileMigrations and GetTenantProvisioningStatus patterns.
	if claims, ok := auth.GetClaimsFromContext(ctx); ok {
		if !auth.HasAnyRole(claims, auth.RolePlatformAdmin, auth.RoleSuperAdmin) {
			// Non-admin: return only the user's own tenant
			tenantID, err := claims.GetTenantID()
			if err != nil {
				s.logger.Warn("ListTenants: non-admin user without tenant claim",
					"user_id", claims.EffectiveUserID(),
					"error", err)
				return &pb.ListTenantsResponse{}, nil
			}

			t, err := s.repo.GetByID(ctx, tenantID)
			if err != nil {
				if errors.Is(err, persistence.ErrTenantNotFound) {
					return &pb.ListTenantsResponse{}, nil
				}
				s.logger.Error("failed to retrieve tenant for non-admin user",
					"tenant_id", tenantID.String(),
					"error", err)
				return nil, status.Errorf(codes.Internal, "failed to list tenants")
			}

			return &pb.ListTenantsResponse{
				Tenants: []*pb.Tenant{s.toProto(t)},
			}, nil
		}
	}

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
