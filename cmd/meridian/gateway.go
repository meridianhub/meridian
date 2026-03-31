package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	tenantv1 "github.com/meridianhub/meridian/api/proto/meridian/tenant/v1"
	gateway "github.com/meridianhub/meridian/services/api-gateway"
	"github.com/meridianhub/meridian/services/api-gateway/eventstream"
	"github.com/meridianhub/meridian/services/api-gateway/eventstream/adapters"
	identitypersistence "github.com/meridianhub/meridian/services/identity/adapters/persistence"
	tenantpersistence "github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	"github.com/meridianhub/meridian/shared/pkg/email"

	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/env"
	platformgateway "github.com/meridianhub/meridian/shared/platform/gateway"

	"google.golang.org/grpc"
	"gorm.io/gorm"
)

// serviceNames lists the fully-qualified gRPC service names to register with the
// Vanguard REST↔gRPC transcoder in the unified development binary.
//
// All services run in the same process and share a single loopback gRPC address.
// Per-service entries allow the transcoder to be selective: services that share
// conflicting HTTP path patterns in their proto annotations cannot be registered
// together in the same Vanguard instance.
//
// HTTP-route conflict: InternalAccountService and CurrentAccountService both
// define REST routes for lien operations (/v1/liens/*). InternalAccountService
// is registered here as the canonical REST owner; CurrentAccountService is still
// reachable via native gRPC or Connect protocol.
var serviceNames = []string{
	"meridian.party.v1.PartyService",
	"meridian.reference_data.v1.ReferenceDataService",
	"meridian.reference_data.v1.NodeService",
	"meridian.market_information.v1.MarketInformationService",
	"meridian.tenant.v1.TenantService",
	"meridian.internal_account.v1.InternalAccountService",
	"meridian.financial_accounting.v1.FinancialAccountingService",
	"meridian.position_keeping.v1.PositionKeepingService",
	"meridian.forecasting.v1.ForecastingService",
	// CurrentAccountService shares REST routes (/v1/liens/*) with InternalAccountService.
	// Vanguard resolves the conflict by routing REST lien requests to whichever service
	// was registered first (InternalAccountService above). Connect protocol paths
	// (/{package}.{Service}/{Method}) are unique per service and never conflict.
	"meridian.current_account.v1.CurrentAccountService",
	"meridian.payment_order.v1.PaymentOrderService",
	"meridian.reconciliation.v1.AccountReconciliationService",
	"meridian.saga.v1.SagaRegistryService",
	// NOTE: SagaAdminService is not yet wired in wire_services.go;
	// add it here once AdminHandler is registered with the gRPC server.
	"meridian.control_plane.v1.ApplyManifestService",
	"meridian.control_plane.v1.ManifestHistoryService",
	"meridian.reference_data.v1.AccountTypeRegistryService",
	"meridian.mapping.v1.MappingService",
	"meridian.audit.v1.AuditService",
	"meridian.identity.v1.IdentityService",
}

// wireGateway creates the gateway HTTP server with the Vanguard transcoder
// routing REST/JSON, Connect, and gRPC-Web requests to the shared gRPC server
// running on grpcPort.
func wireGateway(grpcPort, httpPort int, databaseURL string, tenantDB *gorm.DB, localSigner *platformauth.JWTSigner, logger *slog.Logger, extraOpts ...gateway.ServerOption) (*gateway.Server, error) {
	grpcTarget := fmt.Sprintf("localhost:%d", grpcPort)

	authConfig := gateway.LoadAuthConfig()

	baseDomain := env.GetEnvOrDefault("BASE_DOMAIN", "localhost")
	localDevMode := env.GetEnvAsBool("LOCAL_DEV_MODE", false)

	config := &gateway.Config{
		Port:         httpPort,
		BaseDomain:   baseDomain,
		LocalDevMode: localDevMode,
		DatabaseURL:  databaseURL,
		Auth:         authConfig,
	}

	// Build per-service backends pointing at the shared loopback gRPC server.
	backends := make([]gateway.ServiceBackend, 0, len(serviceNames))
	for _, name := range serviceNames {
		backends = append(backends, gateway.ServiceBackend{
			ServiceName: name,
			BackendAddr: grpcTarget,
		})
	}

	// Build the Vanguard transcoder from the embedded FileDescriptorSet.
	// On failure, log a warning and fall back to the placeholder handler —
	// this keeps the health endpoints alive even if descriptors are stale.
	var opts []gateway.ServerOption
	transcoder, err := gateway.NewTranscoder(GetProtoDescriptors(), backends)
	if err != nil {
		logger.Warn("failed to build Vanguard transcoder; API routes will return 503",
			"error", err)
	} else {
		opts = append(opts, gateway.WithTranscoder(transcoder))
	}

	// Wire auth middleware if enabled — fail fast if misconfigured.
	// Pass the local signer so the composite validator trusts both
	// Meridian-issued (BFF) and Dex-issued (SSO) tokens.
	if authConfig.Enabled {
		authMiddleware, err := gateway.BuildAuthMiddleware(authConfig, logger, localSigner)
		if err != nil {
			return nil, fmt.Errorf("failed to build auth middleware: %w", err)
		}
		opts = append(opts, gateway.WithAuthMiddleware(authMiddleware))
	}

	opts = append(opts, gateway.WithVersionInfo(&gateway.VersionInfo{
		Version:   Version,
		Commit:    Commit,
		BuildDate: BuildDate,
	}))

	// Wire tenant resolver middleware — resolves tenant from X-Tenant-Slug header
	// (LOCAL_DEV_MODE) or subdomain-based resolution.
	slugCache := platformgateway.NewInMemorySlugCache()
	tenantRepo := tenantpersistence.NewRepository(tenantDB)
	tenantResolver, err := platformgateway.NewTenantResolverMiddleware(slugCache, tenantRepo, baseDomain, logger, localDevMode)
	if err != nil {
		return nil, fmt.Errorf("failed to create tenant resolver: %w", err)
	}

	// Wire public tenant info endpoint for login page branding.
	tenantInfoHandler := gateway.NewTenantInfoHandler(logger)
	opts = append(opts, gateway.WithTenantInfoHandler(tenantInfoHandler))

	// Append caller-provided options (e.g., event stream handler).
	opts = append(opts, extraOpts...)

	return gateway.NewServer(config, logger, tenantResolver, opts...), nil
}

// ─── Event Stream Wiring ─────────────────────────────────────────────────────

// startEventRouter launches the event router in a background goroutine and
// returns a cancel function. If router is nil the returned cancel is a no-op.
func startEventRouter(ctx context.Context, router *eventstream.Router, logger *slog.Logger) context.CancelFunc {
	if router == nil {
		return func() {}
	}
	routerCtx, cancel := context.WithCancel(ctx)
	go func() {
		if err := router.Start(routerCtx); err != nil {
			logger.Error("event router error", "error", err)
		}
	}()
	logger.Info("event router started")
	return cancel
}

// wireEventStream conditionally builds event stream components and returns the
// Router (for lifecycle management) and any gateway ServerOptions that should be
// applied. When EVENT_STREAM_ENABLED is false or unset, both return values are
// nil/empty. Set EVENT_STREAM_ENABLED=true to enable.
func wireEventStream(faDB *gorm.DB, logger *slog.Logger) (*eventstream.Router, []gateway.ServerOption) {
	if !env.GetEnvAsBool("EVENT_STREAM_ENABLED", false) {
		return nil, nil
	}
	router, wsHandler := buildUnifiedEventStreamComponents(faDB, logger)
	logger.Info("event stream components initialized (outbox source, local fan-out)")
	return router, []gateway.ServerOption{gateway.WithEventStreamHandler(wsHandler)}
}

// buildUnifiedEventStreamComponents creates event stream components for the unified binary.
// Uses OutboxEventSource (polls shared outbox table) and LocalFanOut (in-process channels).
// This matches dev/CI mode from the standalone gateway.
//
// The db parameter should be the financial-accounting GORM connection where
// outbox events are written via OutboxPublisher. The connection is shared with
// the service — no separate cleanup is needed.
func buildUnifiedEventStreamComponents(db *gorm.DB, logger *slog.Logger) (*eventstream.Router, *eventstream.Handler) {
	pollInterval := env.GetEnvAsDuration("OUTBOX_POLL_INTERVAL", 500*time.Millisecond)
	source := adapters.NewOutboxEventSource(db, pollInterval, logger)

	bufferSize := env.GetEnvAsInt("EVENT_STREAM_BUFFER_SIZE", 256)
	if bufferSize < 0 {
		bufferSize = 256
	}
	fanOut := adapters.NewLocalFanOut(bufferSize)

	router := eventstream.NewRouter(source, fanOut)
	handler := eventstream.NewHandler(router, logger)

	return router, handler
}

// ─── Registration Wiring ────────────────────────────────────────────────────

// errNilTenantResponse is returned when InitiateTenant succeeds but returns a nil tenant.
var errNilTenantResponse = fmt.Errorf("InitiateTenant returned nil tenant")

// loopbackTenantCreator adapts the tenant gRPC service client to the
// gateway.TenantCreator interface used by RegistrationHandler.
type loopbackTenantCreator struct {
	client     tenantv1.TenantServiceClient
	baseDomain string
	logger     *slog.Logger
}

func (a *loopbackTenantCreator) CreateTenant(ctx context.Context, tenantID, slug, displayName string) (string, error) {
	subdomain := slug
	if a.baseDomain != "" {
		subdomain = slug + "." + a.baseDomain
	}

	resp, err := a.client.InitiateTenant(ctx, &tenantv1.InitiateTenantRequest{
		TenantId:        tenantID,
		DisplayName:     displayName,
		Slug:            slug,
		Subdomain:       subdomain,
		SettlementAsset: "USD",
	})
	if err != nil {
		return "", err
	}
	if resp.Tenant == nil {
		return "", errNilTenantResponse
	}
	return resp.Tenant.TenantId, nil
}

func (a *loopbackTenantCreator) DeleteTenant(ctx context.Context, tenantID string) error {
	_, err := a.client.UpdateTenantStatus(ctx, &tenantv1.UpdateTenantStatusRequest{
		TenantId: tenantID,
		Status:   tenantv1.TenantStatus_TENANT_STATUS_DEPROVISIONED,
	})
	return err
}

// loopbackSlugChecker adapts the tenant persistence repository to the
// gateway.SlugChecker interface used by RegistrationHandler.
type loopbackSlugChecker struct {
	repo *tenantpersistence.Repository
}

func (c *loopbackSlugChecker) IsSlugAvailable(ctx context.Context, slug string) (bool, error) {
	return c.repo.IsSlugAvailable(ctx, slug)
}

// wireResendWebhook creates the Resend delivery status webhook handler and returns
// a ServerOption. Returns nil if RESEND_WEBHOOK_SECRET is not set (webhook disabled).
func wireResendWebhook(paymentOrderDB *gorm.DB, logger *slog.Logger) gateway.ServerOption {
	secret := env.GetEnvOrDefault("RESEND_WEBHOOK_SECRET", "")
	if secret == "" {
		logger.Info("resend webhook disabled: RESEND_WEBHOOK_SECRET not set")
		return nil
	}

	auditRepo := email.NewPostgresAuditRepository(paymentOrderDB)
	suppressionRepo := email.NewPostgresSuppressionRepository(paymentOrderDB)
	recorder := email.NewDeliveryStatusRecorder(auditRepo, suppressionRepo, email.NewMetrics(), logger)
	handler := gateway.NewResendWebhookHandler(recorder, secret, logger)
	logger.Info("resend webhook handler initialized")
	return gateway.WithResendWebhookHandler(handler)
}

// wireAdminHandler creates the admin identity management handler and returns
// a ServerOption to install it. Returns nil on error (graceful degradation).
func wireAdminHandler(identityDB *gorm.DB, logger *slog.Logger) gateway.ServerOption {
	identityRepo := identitypersistence.NewRepository(identityDB)
	handler, err := gateway.NewAdminHandler(identityRepo, logger)
	if err != nil {
		logger.Warn("admin handler disabled", "error", err)
		return nil
	}
	logger.Info("admin handler initialized")
	return gateway.WithAdminHandler(handler)
}

// wireRegistration creates the self-service registration handler and returns
// a ServerOption to install it. Returns nil on error (graceful degradation).
func wireRegistration(identityDB, tenantDB *gorm.DB, rawConn *grpc.ClientConn, baseDomain string, outboxRepo email.OutboxRepository, logger *slog.Logger) gateway.ServerOption {
	identityRepo := identitypersistence.NewRepository(identityDB)
	tenantClient := tenantv1.NewTenantServiceClient(rawConn)

	creator := &loopbackTenantCreator{client: tenantClient, baseDomain: baseDomain, logger: logger}
	slugChecker := &loopbackSlugChecker{repo: tenantpersistence.NewRepository(tenantDB)}

	emailVerificationRequired := env.GetEnvAsBool("EMAIL_VERIFICATION_REQUIRED", false)

	handler, err := gateway.NewRegistrationHandler(gateway.RegistrationHandlerConfig{
		TenantCreator:             creator,
		SlugChecker:               slugChecker,
		IdentityRepo:              identityRepo,
		OutboxRepo:                outboxRepo,
		BaseDomain:                baseDomain,
		EmailVerificationRequired: emailVerificationRequired,
		Logger:                    logger,
	})
	if err != nil {
		logger.Warn("self-service registration disabled", "error", err)
		return nil
	}

	logger.Info("self-service registration handler initialized",
		"base_domain", baseDomain,
		"email_verification_required", emailVerificationRequired)
	return gateway.WithRegistrationHandler(handler)
}

// wireVerification creates the email verification handler and returns
// a ServerOption to install it. Returns nil on error (graceful degradation).
func wireVerification(identityDB *gorm.DB, outboxRepo email.OutboxRepository, baseDomain string, logger *slog.Logger) gateway.ServerOption {
	identityRepo := identitypersistence.NewRepository(identityDB)

	handler, err := gateway.NewVerificationHandler(gateway.VerificationHandlerConfig{
		IdentityRepo: identityRepo,
		OutboxRepo:   outboxRepo,
		BaseDomain:   baseDomain,
		Logger:       logger,
	})
	if err != nil {
		logger.Warn("email verification handler disabled", "error", err)
		return nil
	}

	logger.Info("email verification handler initialized")
	return gateway.WithVerificationHandler(handler)
}

// wirePasswordReset creates the password reset handler and returns
// a ServerOption to install it. Returns nil on error (graceful degradation).
func wirePasswordReset(identityDB *gorm.DB, outboxRepo email.OutboxRepository, baseDomain string, logger *slog.Logger) gateway.ServerOption {
	identityRepo := identitypersistence.NewRepository(identityDB)

	handler, err := gateway.NewPasswordResetHandler(gateway.PasswordResetHandlerConfig{
		IdentityRepo: identityRepo,
		OutboxRepo:   outboxRepo,
		BaseDomain:   baseDomain,
		Logger:       logger,
	})
	if err != nil {
		logger.Warn("password reset handler disabled", "error", err)
		return nil
	}

	logger.Info("password reset handler initialized")
	return gateway.WithPasswordResetHandler(handler)
}
