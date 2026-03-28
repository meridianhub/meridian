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

	// Validate slug if provided
	if err := s.validateSlugAvailability(ctx, req.Slug); err != nil {
		return nil, err
	}

	// Build domain tenant
	tenant := s.buildTenantFromRequest(tenantID, req)

	// Register Party in BIAN Party Reference Data Directory (if client configured)
	if err := s.registerPartyForTenant(ctx, req, tenant); err != nil {
		return nil, err
	}

	// Persist tenant with initial status (provisioning or active)
	if err := s.persistNewTenant(ctx, req, tenant); err != nil {
		return nil, err
	}

	s.logger.Info("tenant created",
		"tenant_id", tenant.ID.String(),
		"display_name", tenant.DisplayName,
		"settlement_asset", tenant.SettlementAsset,
		"status", tenant.Status,
		"party_id", tenant.PartyID)

	// Best-effort post-creation tasks (cache, provisioning status)
	s.postTenantCreation(ctx, tenantID, tenant)

	return &pb.InitiateTenantResponse{
		Tenant:           s.toProto(tenant),
		ProvisioningHint: provisioningHintFromStatus(tenant.Status),
	}, nil
}

// validateSlugAvailability validates slug format and checks availability.
// Returns nil if slug is empty (not provided).
func (s *Service) validateSlugAvailability(ctx context.Context, slug string) error {
	if slug == "" {
		return nil
	}
	if err := domain.ValidateSlug(slug); err != nil {
		return status.Errorf(codes.InvalidArgument, "invalid slug: %v", err)
	}
	available, err := s.repo.IsSlugAvailable(ctx, slug)
	if err != nil {
		s.logger.Error("failed to check slug availability",
			"slug", slug,
			"error", err)
		return status.Errorf(codes.Internal, "failed to check slug availability")
	}
	if !available {
		return status.Errorf(codes.AlreadyExists, "slug %s is already taken", slug)
	}
	return nil
}

// buildTenantFromRequest creates a domain tenant from the initiate request.
func (s *Service) buildTenantFromRequest(tenantID tenant.TenantID, req *pb.InitiateTenantRequest) *domain.Tenant {
	var metadata map[string]interface{}
	if req.Metadata != nil {
		metadata = req.Metadata.AsMap()
	}

	initialStatus := domain.StatusActive
	if s.provisioner != nil {
		initialStatus = domain.StatusProvisioningPending
	}

	return &domain.Tenant{
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
}

// registerPartyForTenant registers a corresponding Party in the BIAN Party Reference Data Directory.
//
// Note: If Party registration succeeds but tenant creation fails, an orphaned Party record
// may exist. This is acceptable eventual consistency - orphaned parties can be cleaned up
// operationally via the Party service, and the ExternalReference field allows correlation.
func (s *Service) registerPartyForTenant(ctx context.Context, req *pb.InitiateTenantRequest, t *domain.Tenant) error {
	if s.partyClient == nil {
		s.logger.Debug("party client not configured - skipping party registration",
			"tenant_id", req.TenantId)
		return nil
	}
	party, err := s.partyClient.RegisterParty(ctx, &partyv1.RegisterPartyRequest{
		PartyType:         partyv1.PartyType_PARTY_TYPE_ORGANIZATION,
		LegalName:         req.DisplayName,
		DisplayName:       req.DisplayName,
		ExternalReference: req.TenantId,
	})
	if err != nil {
		s.logger.Error("failed to register party for tenant",
			"tenant_id", req.TenantId,
			"error", err)
		return status.Errorf(codes.Internal, "failed to register party for tenant: %v", err)
	}
	t.PartyID = party.PartyId
	s.logger.Info("registered party for tenant",
		"tenant_id", req.TenantId,
		"party_id", t.PartyID)
	return nil
}

// persistNewTenant saves the tenant to the repository and maps persistence errors to gRPC codes.
func (s *Service) persistNewTenant(ctx context.Context, req *pb.InitiateTenantRequest, t *domain.Tenant) error {
	if err := s.repo.Create(ctx, t); err != nil {
		if errors.Is(err, persistence.ErrTenantExists) {
			return status.Errorf(codes.AlreadyExists, "tenant %s already exists", req.TenantId)
		}
		if errors.Is(err, persistence.ErrSubdomainTaken) {
			return status.Errorf(codes.AlreadyExists, "subdomain %s is already taken", req.Subdomain)
		}
		if errors.Is(err, persistence.ErrSlugTaken) {
			return status.Errorf(codes.AlreadyExists, "slug %s is already taken", req.Slug)
		}
		s.logger.Error("failed to create tenant",
			"tenant_id", req.TenantId,
			"error", err)
		return status.Errorf(codes.Internal, "failed to create tenant")
	}
	return nil
}

// postTenantCreation handles best-effort tasks after tenant creation:
// slug cache population and provisioning status initialization.
func (s *Service) postTenantCreation(ctx context.Context, tenantID tenant.TenantID, t *domain.Tenant) {
	// Pre-populate slug cache (best-effort)
	if s.slugCache != nil && t.Slug != "" {
		if err := s.slugCache.Set(ctx, t.Slug, t.ID); err != nil {
			s.logger.Warn("failed to pre-populate slug cache for new tenant",
				"tenant_id", t.ID.String(),
				"slug", t.Slug,
				"error", err)
		}
	}

	// Initialize provisioning status records (non-blocking, best-effort)
	if s.provisioner != nil {
		s.logger.Info("tenant created with provisioning_pending status - worker will handle provisioning",
			"tenant_id", t.ID.String())
		if err := s.createProvisioningStatusRecords(ctx, tenantID); err != nil {
			s.logger.Warn("failed to create provisioning status records - worker will handle",
				"tenant_id", tenantID.String(),
				"error", err)
		}
	}
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
		if tenant, ok := s.lookupSlugInCache(ctx, slug); ok {
			return tenant, nil
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
	s.populateSlugCache(ctx, slug, tenant)

	return tenant, nil
}

// lookupSlugInCache attempts cache-first tenant resolution by slug.
// Returns the tenant and true on cache hit, or nil and false on miss/error.
// Handles stale cache invalidation when the cached tenant ID no longer exists.
func (s *Service) lookupSlugInCache(ctx context.Context, slug string) (*domain.Tenant, bool) {
	cachedTenantID, err := s.slugCache.Get(ctx, slug)
	if err != nil {
		s.logger.Warn("slug cache read failed, falling back to database",
			"slug", slug,
			"error", err)
		return nil, false
	}
	if cachedTenantID == "" {
		return nil, false
	}

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
			return nil, false
		}
		// DB error on cache hit - log but fall through to DB lookup
		s.logger.Error("failed to retrieve tenant by cached ID",
			"slug", slug,
			"tenant_id", cachedTenantID,
			"error", err)
		return nil, false
	}

	s.logger.Debug("slug cache hit",
		"slug", slug,
		"tenant_id", cachedTenantID)
	return tenant, true
}

// populateSlugCache writes a slug-to-tenant mapping into the cache (best-effort).
func (s *Service) populateSlugCache(ctx context.Context, slug string, tenant *domain.Tenant) {
	if s.slugCache == nil {
		return
	}
	if err := s.slugCache.Set(ctx, slug, tenant.ID); err != nil {
		s.logger.Error("failed to populate slug cache after DB lookup",
			"slug", slug,
			"tenant_id", tenant.ID,
			"error", err)
	} else {
		s.logger.Debug("populated slug cache after DB lookup",
			"slug", slug,
			"tenant_id", tenant.ID)
	}
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

	// Load current tenant and validate the status transition
	currentTenant, err := s.validateStatusTransition(ctx, tenantID, domainStatus)
	if err != nil {
		return nil, err
	}

	// Perform the status update
	updatedTenant, err := s.executeStatusUpdate(ctx, req, tenantID, domainStatus, currentTenant)
	if err != nil {
		return nil, err
	}

	s.logger.Info("tenant status updated",
		"tenant_id", updatedTenant.ID.String(),
		"old_status", currentTenant.Status,
		"new_status", updatedTenant.Status)

	// Invalidate cache on deprovisioning (tenant becoming inactive)
	s.invalidateCacheOnDeprovision(ctx, updatedTenant, currentTenant)

	return &pb.UpdateTenantStatusResponse{
		Tenant: s.toProto(updatedTenant),
	}, nil
}

// validateStatusTransition loads the current tenant and validates the requested status transition.
func (s *Service) validateStatusTransition(ctx context.Context, tenantID tenant.TenantID, domainStatus domain.Status) (*domain.Tenant, error) {
	currentTenant, err := s.repo.GetByID(ctx, tenantID)
	if err != nil {
		if errors.Is(err, persistence.ErrTenantNotFound) {
			return nil, status.Errorf(codes.NotFound, "tenant %s not found", tenantID)
		}
		s.logger.Error("failed to get tenant for status update",
			"tenant_id", tenantID,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to update tenant status")
	}

	if !currentTenant.CanTransitionTo(domainStatus) {
		return nil, status.Errorf(codes.FailedPrecondition,
			"invalid status transition from %s to %s", currentTenant.Status, domainStatus)
	}
	return currentTenant, nil
}

// executeStatusUpdate persists the status change and maps persistence errors to gRPC codes.
func (s *Service) executeStatusUpdate(ctx context.Context, req *pb.UpdateTenantStatusRequest, tenantID tenant.TenantID, domainStatus domain.Status, current *domain.Tenant) (*domain.Tenant, error) {
	updated, err := s.repo.UpdateStatus(ctx, tenantID, domainStatus, current.Version)
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
	return updated, nil
}

// invalidateCacheOnDeprovision removes the slug cache entry when a tenant is deprovisioned.
func (s *Service) invalidateCacheOnDeprovision(ctx context.Context, updated *domain.Tenant, previous *domain.Tenant) {
	if s.slugCache == nil || updated.Status != domain.StatusDeprovisioned || previous.Slug == "" {
		return
	}
	if err := s.slugCache.Invalidate(ctx, previous.Slug); err != nil {
		s.logger.Error("failed to invalidate slug cache after deprovisioning",
			"tenant_id", updated.ID.String(),
			"slug", previous.Slug,
			"error", err)
	} else {
		s.logger.Debug("invalidated slug cache after deprovisioning",
			"tenant_id", updated.ID.String(),
			"slug", previous.Slug)
	}
}

// ListTenants returns all tenants with optional status filter (BIAN: Control).
// When auth is enabled, non-admin users see only their own tenant.
// Platform admins and super admins see all tenants.
func (s *Service) ListTenants(ctx context.Context, req *pb.ListTenantsRequest) (*pb.ListTenantsResponse, error) {
	// RBAC: When claims are present (auth interceptor is active), restrict non-admin
	// users to their own tenant. When no claims are in context (auth disabled or
	// internal calls), fall through to full list for backwards compatibility.
	// This aligns with ReconcileMigrations and GetTenantProvisioningStatus patterns.
	if resp, handled := s.listTenantsForNonAdmin(ctx); handled {
		return resp, nil
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
	for _, t := range tenants {
		protoTenants = append(protoTenants, s.toProto(t))
	}

	return &pb.ListTenantsResponse{
		Tenants:       protoTenants,
		NextPageToken: nextPageToken,
	}, nil
}

// listTenantsForNonAdmin checks RBAC and returns a single-tenant response for non-admin users.
// Returns (response, true) if the request was handled (non-admin user), or (nil, false) to
// fall through to the full admin list path.
func (s *Service) listTenantsForNonAdmin(ctx context.Context) (*pb.ListTenantsResponse, bool) {
	claims, ok := auth.GetClaimsFromContext(ctx)
	if !ok {
		return nil, false
	}
	if auth.HasAnyRole(claims, auth.RolePlatformAdmin, auth.RoleSuperAdmin) {
		return nil, false
	}

	// Non-admin: return only the user's own tenant
	tenantID, err := claims.GetTenantID()
	if err != nil {
		s.logger.Warn("ListTenants: non-admin user without tenant claim",
			"user_id", claims.EffectiveUserID(),
			"error", err)
		return &pb.ListTenantsResponse{}, true
	}

	t, err := s.repo.GetByID(ctx, tenantID)
	if err != nil {
		if errors.Is(err, persistence.ErrTenantNotFound) {
			return &pb.ListTenantsResponse{}, true
		}
		s.logger.Error("failed to retrieve tenant for non-admin user",
			"tenant_id", tenantID.String(),
			"error", err)
		return &pb.ListTenantsResponse{}, true
	}

	return &pb.ListTenantsResponse{
		Tenants: []*pb.Tenant{s.toProto(t)},
	}, true
}
