package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	gateway "github.com/meridianhub/meridian/services/api-gateway"
	"github.com/meridianhub/meridian/services/api-gateway/eventstream"
	"github.com/meridianhub/meridian/services/api-gateway/eventstream/adapters"
	tenantpersistence "github.com/meridianhub/meridian/services/tenant/adapters/persistence"

	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/env"
	platformgateway "github.com/meridianhub/meridian/shared/platform/gateway"

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
	"meridian.saga.v1.SagaAdminService",
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
