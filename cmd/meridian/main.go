// Package main is the entry point for the Meridian unified binary.
//
// It wires all 11 Meridian services into a single Go process with a shared gRPC
// server and gateway HTTP server. Services are initialized in tier dependency order:
//
//   - Tier 0 (no deps): party, reference-data, market-information, tenant, internal-account
//   - Tier 1 (Tier 0 deps): financial-accounting, position-keeping, forecasting
//   - Tier 2 (Tier 1 deps): current-account
//   - Tier 3 (Tier 2 deps): payment-order, reconciliation
//
// Flags:
//
//	--migrate       Apply all embedded SQL migrations and exit
//	--bootstrap     Provision master tenant schemas and validate platform manifest, then exit
//	--grpc-port     gRPC listen port (default 50051)
//	--http-port     Gateway HTTP listen port (default 8090)
//	--database-url  Superuser DSN for migrations (or DATABASE_URL env var)
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	// Proto registrations
	auditv1 "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	forecastingv1 "github.com/meridianhub/meridian/api/proto/meridian/forecasting/v1"
	internalaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	paymentorderv1 "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	reconciliationv1 "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	sagav1 "github.com/meridianhub/meridian/api/proto/meridian/saga/v1"
	tenantv1 "github.com/meridianhub/meridian/api/proto/meridian/tenant/v1"

	// Service packages
	auditservice "github.com/meridianhub/meridian/services/audit-worker/service"
	controlplaneservice "github.com/meridianhub/meridian/services/control-plane/service"
	currentaccountpersistence "github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	currentaccountservice "github.com/meridianhub/meridian/services/current-account/service"
	financialaccountingpersistence "github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
	faclient "github.com/meridianhub/meridian/services/financial-accounting/client"
	financialaccountingservice "github.com/meridianhub/meridian/services/financial-accounting/service"
	forecastingmds "github.com/meridianhub/meridian/services/forecasting/adapters/mds"
	forecastingpersistence "github.com/meridianhub/meridian/services/forecasting/adapters/persistence"
	forecastinghandler "github.com/meridianhub/meridian/services/forecasting/handler"
	forecastingstarlark "github.com/meridianhub/meridian/services/forecasting/starlark"
	internalaccountpersistence "github.com/meridianhub/meridian/services/internal-account/adapters/persistence"
	internalaccountservice "github.com/meridianhub/meridian/services/internal-account/service"
	marketinformationpersistence "github.com/meridianhub/meridian/services/market-information/adapters/persistence"
	misclient "github.com/meridianhub/meridian/services/market-information/client"
	marketinformationservice "github.com/meridianhub/meridian/services/market-information/service"
	partypersistence "github.com/meridianhub/meridian/services/party/adapters/persistence"
	partyservice "github.com/meridianhub/meridian/services/party/service"
	paymentorderpersistence "github.com/meridianhub/meridian/services/payment-order/adapters/persistence"
	paymentorderservice "github.com/meridianhub/meridian/services/payment-order/service"
	pkpersistence "github.com/meridianhub/meridian/services/position-keeping/adapters/persistence"
	pkclient "github.com/meridianhub/meridian/services/position-keeping/client"
	pkdomain "github.com/meridianhub/meridian/services/position-keeping/domain"
	positionkeepingservice "github.com/meridianhub/meridian/services/position-keeping/service"
	reconciliationpersistence "github.com/meridianhub/meridian/services/reconciliation/adapters/persistence"
	reconciliationservice "github.com/meridianhub/meridian/services/reconciliation/service"
	refcel "github.com/meridianhub/meridian/services/reference-data/cel"
	refhandler "github.com/meridianhub/meridian/services/reference-data/handler"
	refnode "github.com/meridianhub/meridian/services/reference-data/node"
	refregistry "github.com/meridianhub/meridian/services/reference-data/registry"
	refsaga "github.com/meridianhub/meridian/services/reference-data/saga"
	tenantpersistence "github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	tenantservice "github.com/meridianhub/meridian/services/tenant/service"

	// Gateway
	"github.com/meridianhub/meridian/services/gateway"

	// Shared platform
	masterbootstrap "github.com/meridianhub/meridian/internal/bootstrap"
	"github.com/meridianhub/meridian/internal/migrations"
	"github.com/meridianhub/meridian/services"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/events"
	platformgateway "github.com/meridianhub/meridian/shared/platform/gateway"
	"github.com/meridianhub/meridian/shared/platform/observability"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"gorm.io/gorm"
)

// Version information injected at build time via ldflags.
// See Dockerfile: -X main.Version=... -X main.Commit=... -X main.BuildDate=...
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

func main() {
	migrate := flag.Bool("migrate", false, "Apply all embedded SQL migrations to CockroachDB and exit")
	bootstrapFlag := flag.Bool("bootstrap", false, "Provision master tenant schemas and validate platform manifest, then exit")
	databaseURL := flag.String("database-url", "", "Superuser DSN for CockroachDB (e.g., postgres://root@localhost:26257/defaultdb?sslmode=disable)")
	grpcPort := flag.Int("grpc-port", 50051, "gRPC server listen port")
	httpPort := flag.Int("http-port", 8090, "Gateway HTTP server listen port")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	// Set development defaults: disable auth and billing
	setDevDefaults()

	if *migrate {
		dsn := *databaseURL
		if dsn == "" {
			dsn = os.Getenv("DATABASE_URL")
		}
		if dsn == "" {
			fmt.Fprintln(os.Stderr, "error: --database-url flag or DATABASE_URL environment variable required for --migrate")
			os.Exit(1)
		}

		ctx := context.Background()
		if err := migrations.RunMigrations(ctx, services.MigrationFS, dsn, logger); err != nil {
			logger.Error("migration failed", "error", err)
			os.Exit(1)
		}

		logger.Info("all migrations applied successfully")
		return
	}

	if *bootstrapFlag {
		dsn := *databaseURL
		if dsn == "" {
			dsn = os.Getenv("DATABASE_URL")
		}
		if dsn == "" {
			fmt.Fprintln(os.Stderr, "error: --database-url flag or DATABASE_URL environment variable required for --bootstrap")
			os.Exit(1)
		}
		if err := runBootstrap(dsn, logger); err != nil {
			logger.Error("bootstrap failed", "error", err)
			os.Exit(1)
		}
		logger.Info("bootstrap completed successfully")
		return
	}

	if err := run(logger, *grpcPort, *httpPort); err != nil {
		logger.Error("fatal error", "error", err)
		os.Exit(1)
	}
}

// setDevDefaults sets environment variable defaults appropriate for unified local development.
// These can be overridden by setting the variables before starting the binary.
func setDevDefaults() {
	defaults := map[string]string{
		"AUTH_MODE":       "disabled",
		"BILLING_ENABLED": "false",
		"ENVIRONMENT":     "development",
		"REDIS_ENABLED":   "false",
		"KAFKA_ENABLED":   "false",
	}
	for k, v := range defaults {
		if os.Getenv(k) == "" {
			_ = os.Setenv(k, v)
		}
	}
}

func connectPgxPool(ctx context.Context, dsn string, logger *slog.Logger) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("pgxpool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pgxpool ping: %w", err)
	}
	logger.Info("pgxpool connection established")
	return pool, nil
}

func run(logger *slog.Logger, grpcPort, httpPort int) error {
	ctx := context.Background()

	logger.Info("starting meridian unified binary",
		"grpc_port", grpcPort,
		"http_port", httpPort)

	// ─── Shared Infrastructure ───────────────────────────────────────────

	// Per-service database connections derived from a base DSN.
	// Each service connects to its own database as defined by migrations.ServiceDatabases.
	baseDSN := env.GetEnvOrDefault("DATABASE_URL",
		"postgres://root@localhost:26257/defaultdb?sslmode=disable")
	conns, err := newServiceConns(ctx, baseDSN, logger)
	if err != nil {
		return fmt.Errorf("service connections: %w", err)
	}
	defer conns.closeAll(logger)

	// No-op tracer (tracing disabled in unified dev mode)
	tracer, err := observability.NewTracer(ctx, observability.TracerConfig{
		ServiceName:  "meridian-unified",
		OTLPEndpoint: "localhost:4317",
		Enabled:      false,
	})
	if err != nil {
		return fmt.Errorf("tracer: %w", err)
	}

	// Shared gRPC server (no auth for unified dev mode)
	grpcServer := bootstrap.NewGrpcServerBuilder(tracer, logger).Build()

	// Idempotency service backed by the platform pgxpool (no Redis required in dev)
	idempotencySvc := idempotency.NewPostgresService(conns.pgxPool("control-plane"))
	if err := idempotencySvc.EnsureTable(ctx); err != nil {
		return fmt.Errorf("idempotency table: %w", err)
	}

	// Noop event publishers for FA and PK (no Kafka in dev)
	faEventPublisher := &noopFAPublisher{}
	pkEventPublisher := pkdomain.NewNoOpEventPublisher()

	// Outbox publisher and repo (writes to FA database, no Kafka worker in dev)
	outboxPublisher := events.NewOutboxPublisher("unified")
	outboxRepo := events.NewPostgresOutboxRepository(conns.gormDB("financial-accounting"))

	// Loopback gRPC clients for inter-service communication within the unified binary.
	// grpc.NewClient is lazy — connects on first RPC, after the server is listening.
	loopback, err := newLoopbackClients(ctx, grpcPort)
	if err != nil {
		return fmt.Errorf("loopback clients: %w", err)
	}
	defer loopback.closeAll()

	// ─── Register All Services ──────────────────────────────────────────

	if err := registerServices(grpcServer, conns, idempotencySvc, faEventPublisher, pkEventPublisher, outboxPublisher, outboxRepo, loopback, tracer, logger); err != nil {
		return err
	}

	// ─── Start gRPC Server ───────────────────────────────────────────────

	grpcAddr := fmt.Sprintf(":%d", grpcPort)
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", grpcAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", grpcAddr, err)
	}

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("gRPC server starting", "address", grpcAddr)
		if err := grpcServer.Serve(listener); err != nil {
			serverErrors <- err
		}
	}()

	// ─── Start Gateway HTTP Server ───────────────────────────────────────

	platformDSN := replaceDSNDatabase(baseDSN, "meridian_platform")
	gwServer, err := wireGateway(grpcPort, httpPort, platformDSN, conns.gormDB("tenant"), logger)
	if err != nil {
		return fmt.Errorf("gateway init: %w", err)
	}

	gatewayErrors := make(chan error, 1)
	go func() {
		if err := gwServer.Start(context.Background()); err != nil {
			gatewayErrors <- err
		}
	}()

	// ─── Graceful Shutdown ───────────────────────────────────────────────

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		logger.Info("received signal, shutting down", "signal", sig)
	case err := <-serverErrors:
		return fmt.Errorf("gRPC server: %w", err)
	case err := <-gatewayErrors:
		return fmt.Errorf("gateway server: %w", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := gwServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("gateway shutdown error", "error", err)
	}

	grpcServer.GracefulStop()
	logger.Info("servers stopped")

	return nil
}

// registerServices wires all 11 gRPC services into the shared server in tier dependency order,
// then enables health checking and reflection.
func registerServices(
	grpcServer *grpc.Server,
	conns *serviceConns,
	idempotencySvc idempotency.Service,
	faEventPublisher financialaccountingservice.EventPublisher,
	pkEventPublisher pkdomain.EventPublisher,
	outboxPublisher *events.OutboxPublisher,
	outboxRepo *events.PostgresOutboxRepository,
	loopback *loopbackClients,
	tracer *observability.Tracer,
	logger *slog.Logger,
) error {
	// Tier 0: No gRPC dependencies
	for _, wire := range []struct {
		name string
		fn   func() error
	}{
		{"party", func() error { return wireParty(grpcServer, conns.gormDB("party"), logger) }},
		{"reference-data", func() error { return wireReferenceData(grpcServer, conns.pgxPool("reference-data"), logger) }},
		{"market-information", func() error { return wireMarketInformation(grpcServer, conns.pgxPool("market-information"), logger) }},
		{"tenant", func() error { return wireTenant(grpcServer, conns.gormDB("tenant"), logger) }},
		{"internal-account", func() error {
			return wireInternalAccount(grpcServer, conns.gormDB("internal-account"), logger)
		}},
		{"control-plane", func() error { return wireControlPlane(grpcServer, conns.pgxPool("control-plane"), logger) }},
		{"audit", func() error { return wireAudit(grpcServer, conns.gormDB("tenant"), logger) }}, // audit uses platform DB
	} {
		if err := wire.fn(); err != nil {
			return fmt.Errorf("%s: %w", wire.name, err)
		}
	}
	logger.Info("Tier 0 services registered")

	// Tier 1: Depend on Tier 0 via loopback
	for _, wire := range []struct {
		name string
		fn   func() error
	}{
		{"financial-accounting", func() error {
			return wireFinancialAccounting(grpcServer, conns.gormDB("financial-accounting"), idempotencySvc, faEventPublisher, outboxPublisher, outboxRepo, logger)
		}},
		{"position-keeping", func() error {
			return wirePositionKeeping(grpcServer, conns.pgxPool("position-keeping"), idempotencySvc, pkEventPublisher, logger)
		}},
		{"forecasting", func() error { return wireForecasting(grpcServer, conns.pgxPool("forecasting"), loopback.mds, logger) }},
	} {
		if err := wire.fn(); err != nil {
			return fmt.Errorf("%s: %w", wire.name, err)
		}
	}
	logger.Info("Tier 1 services registered")

	// Tier 2: Depend on Tier 1 (current-account needs PK, FA loopback clients for saga orchestration)
	caOutboxRepo := events.NewPostgresOutboxRepository(conns.gormDB("current-account"))
	if err := wireCurrentAccount(grpcServer, conns.gormDB("current-account"), loopback.pk, loopback.fa, idempotencySvc, caOutboxRepo, tracer, logger); err != nil {
		return fmt.Errorf("current-account: %w", err)
	}
	logger.Info("Tier 2 services registered")

	// Tier 3: Depend on Tier 2
	for _, wire := range []struct {
		name string
		fn   func() error
	}{
		{"payment-order", func() error {
			return wirePaymentOrder(grpcServer, conns.gormDB("payment-order"), idempotencySvc, logger)
		}},
		{"reconciliation", func() error { return wireReconciliation(grpcServer, conns.gormDB("reconciliation"), logger) }},
	} {
		if err := wire.fn(); err != nil {
			return fmt.Errorf("%s: %w", wire.name, err)
		}
	}
	logger.Info("Tier 3 services registered")

	// Health + Reflection
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	reflection.Register(grpcServer)

	return nil
}

// ─── Tier 0 Wiring ──────────────────────────────────────────────────────────

func wireParty(server *grpc.Server, db *gorm.DB, logger *slog.Logger) error {
	repo := partypersistence.NewRepository(db)
	svc, err := partyservice.NewService(repo, logger)
	if err != nil {
		return err
	}
	partyv1.RegisterPartyServiceServer(server, svc)
	logger.Info("registered party service")
	return nil
}

func wireReferenceData(server *grpc.Server, pool *pgxpool.Pool, logger *slog.Logger) error {
	instrumentRegistry, err := refregistry.NewPostgresRegistry(pool)
	if err != nil {
		return fmt.Errorf("instrument registry: %w", err)
	}

	compiler, err := refcel.NewCompiler()
	if err != nil {
		return fmt.Errorf("CEL compiler: %w", err)
	}

	refDataSvc, err := refhandler.NewService(instrumentRegistry, compiler, logger)
	if err != nil {
		return fmt.Errorf("ref data service: %w", err)
	}

	nodeRepo := refnode.NewPostgresRepository(pool)
	nodeSvc, err := refhandler.NewNodeService(nodeRepo, logger)
	if err != nil {
		return fmt.Errorf("node service: %w", err)
	}

	sagaRegistry := refsaga.NewPostgresRegistry(pool, nil)
	sagaSvc := refsaga.NewRegistryHandler(sagaRegistry, nil, nil, logger)

	referencedatav1.RegisterReferenceDataServiceServer(server, refDataSvc)
	referencedatav1.RegisterNodeServiceServer(server, nodeSvc)
	sagav1.RegisterSagaRegistryServiceServer(server, sagaSvc)
	logger.Info("registered reference-data service")
	return nil
}

func wireMarketInformation(server *grpc.Server, pool *pgxpool.Pool, logger *slog.Logger) error {
	masterTenantID := env.GetEnvOrDefault("MASTER_TENANT_ID", "meridian_master")
	repos := marketinformationpersistence.NewRepositories(pool, masterTenantID)

	miServer, err := marketinformationservice.NewServer(
		repos.DataSet,
		repos.Observation,
		repos.Source,
		marketinformationservice.WithLogger(logger),
	)
	if err != nil {
		return err
	}

	marketinformationv1.RegisterMarketInformationServiceServer(server, miServer)
	logger.Info("registered market-information service")
	return nil
}

func wireTenant(server *grpc.Server, db *gorm.DB, logger *slog.Logger) error {
	repo := tenantpersistence.NewRepository(db)
	svc := tenantservice.NewService(repo, nil, nil, nil, logger)
	tenantv1.RegisterTenantServiceServer(server, svc)
	logger.Info("registered tenant service")
	return nil
}

func wireInternalAccount(server *grpc.Server, db *gorm.DB, logger *slog.Logger) error {
	repo := internalaccountpersistence.NewRepository(db)
	svc, err := internalaccountservice.NewService(repo)
	if err != nil {
		return err
	}
	internalaccountv1.RegisterInternalAccountServiceServer(server, svc)
	logger.Info("registered internal-account service")
	return nil
}

// ─── Tier 1 Wiring ──────────────────────────────────────────────────────────

func wireFinancialAccounting(
	server *grpc.Server,
	db *gorm.DB,
	idempotencySvc idempotency.Service,
	eventPub financialaccountingservice.EventPublisher,
	outboxPub *events.OutboxPublisher,
	outboxRepo *events.PostgresOutboxRepository,
	logger *slog.Logger,
) error {
	svc, err := financialaccountingservice.NewFinancialAccountingService(
		financialaccountingpersistence.NewLedgerRepository(db),
		eventPub,
		idempotencySvc,
		outboxPub,
		outboxRepo,
	)
	if err != nil {
		return err
	}

	financialaccountingv1.RegisterFinancialAccountingServiceServer(server, svc)
	logger.Info("registered financial-accounting service")
	return nil
}

func wirePositionKeeping(
	server *grpc.Server,
	pool *pgxpool.Pool,
	idempotencySvc idempotency.Service,
	eventPub pkdomain.EventPublisher,
	logger *slog.Logger,
) error {
	svc, err := positionkeepingservice.NewPositionKeepingService(
		pkpersistence.NewPostgresRepository(pool),
		pkpersistence.NewMeasurementRepository(pool),
		eventPub,
		idempotencySvc,
	)
	if err != nil {
		return err
	}

	positionkeepingv1.RegisterPositionKeepingServiceServer(server, svc)
	logger.Info("registered position-keeping service")
	return nil
}

func wireForecasting(server *grpc.Server, pool *pgxpool.Pool, mdsClient *misclient.Client, logger *slog.Logger) error {
	repo := forecastingpersistence.NewStrategyRepository(pool)

	misAdapter := forecastingmds.NewMISAdapter(mdsClient)
	mdsPublisher := forecastingmds.NewPublisherAdapter(mdsClient)

	runner, err := forecastingstarlark.NewForecastRunner(forecastingstarlark.ForecastRunnerConfig{
		MISClient: misAdapter,
		RefData:   &forecastingmds.NoOpRefDataClient{},
		Logger:    logger,
	})
	if err != nil {
		return fmt.Errorf("forecast runner: %w", err)
	}

	svc, err := forecastinghandler.NewService(repo, runner, mdsPublisher, logger)
	if err != nil {
		return err
	}

	forecastingv1.RegisterForecastingServiceServer(server, svc)
	logger.Info("registered forecasting service")
	return nil
}

// ─── Tier 2 Wiring ──────────────────────────────────────────────────────────

func wireCurrentAccount(
	server *grpc.Server,
	db *gorm.DB,
	pkLoopback *pkclient.Client,
	faLoopback *faclient.Client,
	idempotencySvc idempotency.Service,
	outboxRepo *events.PostgresOutboxRepository,
	tracer *observability.Tracer,
	logger *slog.Logger,
) error {
	repo := currentaccountpersistence.NewRepository(db)
	lienRepo := currentaccountpersistence.NewLienRepository(db)
	withdrawalRepo := currentaccountpersistence.NewWithdrawalRepository(db)

	svc, err := currentaccountservice.NewServiceWithExistingClients(
		repo, lienRepo, withdrawalRepo,
		outboxRepo, db,
		pkLoopback, faLoopback,
		nil, // partyClient — party validation not needed for unified binary
		nil, // accountConfig — no static clearing account config needed
		idempotencySvc, logger, tracer,
		nil, // accountResolver — no dynamic clearing account resolution
		nil, // fungibilityValidator — not needed for demo
	)
	if err != nil {
		return err
	}

	currentaccountv1.RegisterCurrentAccountServiceServer(server, svc)
	logger.Info("registered current-account service")
	return nil
}

// ─── Tier 3 Wiring ──────────────────────────────────────────────────────────

func wirePaymentOrder(
	server *grpc.Server,
	db *gorm.DB,
	idempotencySvc idempotency.Service,
	logger *slog.Logger,
) error {
	repo := paymentorderpersistence.NewPaymentOrderRepository(db)
	svc, err := paymentorderservice.NewService(repo, idempotencySvc)
	if err != nil {
		return err
	}

	paymentorderv1.RegisterPaymentOrderServiceServer(server, svc)
	logger.Info("registered payment-order service")
	return nil
}

func wireReconciliation(
	server *grpc.Server,
	db *gorm.DB,
	logger *slog.Logger,
) error {
	svc := reconciliationservice.NewAccountReconciliationService(
		reconciliationservice.WithSettlementRunRepository(
			reconciliationpersistence.NewSettlementRunRepository(db),
		),
		reconciliationservice.WithDisputeRepository(
			reconciliationpersistence.NewDisputeRepository(db),
		),
		reconciliationservice.WithVarianceRepository(
			reconciliationpersistence.NewVarianceRepository(db),
		),
		reconciliationservice.WithVarianceListRepository(
			reconciliationpersistence.NewVarianceRepository(db),
		),
		reconciliationservice.WithLogger(logger),
	)

	reconciliationv1.RegisterAccountReconciliationServiceServer(server, svc)
	logger.Info("registered reconciliation service")
	return nil
}

// ─── Audit Wiring ────────────────────────────────────────────────────────────

func wireAudit(server *grpc.Server, db *gorm.DB, logger *slog.Logger) error {
	svc, err := auditservice.NewAuditService(db, logger)
	if err != nil {
		return err
	}
	auditv1.RegisterAuditServiceServer(server, svc)
	logger.Info("registered audit service")
	return nil
}

// ─── Control Plane Wiring ────────────────────────────────────────────────────

func wireControlPlane(server *grpc.Server, pool *pgxpool.Pool, logger *slog.Logger) error {
	// Register ApplyManifestService without executor for now.
	// The executor requires gRPC client adapters for downstream services
	// (reference-data, internal-account) which will be wired in a follow-up.
	if err := controlplaneservice.RegisterApplyManifestService(server, pool, nil, logger); err != nil {
		return err
	}
	logger.Info("registered control-plane service (ApplyManifestService)")
	return nil
}

// ─── Bootstrap ──────────────────────────────────────────────────────────────

// runBootstrap provisions master tenant schemas and validates the platform manifest.
// It establishes database connections, runs the bootstrap process, and exits.
func runBootstrap(baseDSN string, logger *slog.Logger) error {
	ctx := context.Background()

	// Both tenant and control-plane share meridian_platform database.
	platformDSN := replaceDSNDatabase(baseDSN, "meridian_platform")
	cfg := bootstrap.DatabaseConfig{
		DSN:             platformDSN,
		MaxOpenConns:    5,
		MaxIdleConns:    2,
		ConnMaxLifetime: 5 * time.Minute,
		ConnMaxIdleTime: 10 * time.Minute,
		Logger:          logger,
	}
	platformDB, err := bootstrap.NewDatabase(ctx, cfg)
	if err != nil {
		return fmt.Errorf("platform database: %w", err)
	}
	defer bootstrap.CloseDatabase(platformDB, logger)

	// Control-plane also uses meridian_platform (see internal/migrations/runner.go)
	platformPool, err := connectPgxPool(ctx, platformDSN, logger)
	if err != nil {
		return fmt.Errorf("platform pgxpool: %w", err)
	}
	defer platformPool.Close()

	return masterbootstrap.Run(ctx, masterbootstrap.Config{
		PlatformDB:       platformDB,
		ControlPlanePool: platformPool,
		Logger:           logger,
	})
}

// ─── Gateway Wiring ──────────────────────────────────────────────────────────

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
	// CurrentAccountService excluded from Vanguard: its REST routes (/v1/liens/*)
	// conflict with InternalAccountService. Still reachable via Connect protocol
	// (/{package}.{Service}/{Method} paths don't conflict).
	"meridian.payment_order.v1.PaymentOrderService",
	"meridian.reconciliation.v1.AccountReconciliationService",
	"meridian.saga.v1.SagaRegistryService",
	"meridian.saga.v1.SagaAdminService",
	"meridian.control_plane.v1.ApplyManifestService",
	"meridian.control_plane.v1.ManifestHistoryService",
	"meridian.reference_data.v1.AccountTypeRegistryService",
	"meridian.mapping.v1.MappingService",
	"meridian.audit.v1.AuditService",
}

// wireGateway creates the gateway HTTP server with the Vanguard transcoder
// routing REST/JSON, Connect, and gRPC-Web requests to the shared gRPC server
// running on grpcPort.
func wireGateway(grpcPort, httpPort int, databaseURL string, tenantDB *gorm.DB, logger *slog.Logger) (*gateway.Server, error) {
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
		logger.Warn("failed to build Vanguard transcoder; API routes will return 501",
			"error", err)
	} else {
		opts = append(opts, gateway.WithTranscoder(transcoder))
	}

	// Wire auth middleware if enabled — fail fast if misconfigured
	if authConfig.Enabled {
		authMiddleware, err := gateway.BuildAuthMiddleware(authConfig, logger)
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

	return gateway.NewServer(config, logger, tenantResolver, opts...), nil
}

// ─── Per-Service Database Connections ────────────────────────────────────────

// replaceDSNDatabase replaces the database name in a PostgreSQL/CockroachDB DSN URL.
func replaceDSNDatabase(baseDSN, database string) string {
	parsed, err := url.Parse(baseDSN)
	if err != nil {
		return baseDSN
	}
	parsed.Path = "/" + database
	return parsed.String()
}

// serviceConns holds per-database connections for the unified binary.
// Services sharing the same database (e.g., tenant and control-plane both use
// meridian_platform) share the same connection object.
type serviceConns struct {
	gormDBs  map[string]*gorm.DB
	pgxPools map[string]*pgxpool.Pool
}

// gormDB returns the GORM connection for the given service's database.
// Panics with a descriptive message if serviceName is not in ServiceDatabases.
func (c *serviceConns) gormDB(serviceName string) *gorm.DB {
	sdb, ok := migrations.ServiceDatabases[serviceName]
	if !ok {
		panic(fmt.Sprintf("unknown service %q: not found in ServiceDatabases", serviceName))
	}
	return c.gormDBs[sdb.Database]
}

// pgxPool returns the pgxpool connection for the given service's database.
// Panics with a descriptive message if serviceName is not in ServiceDatabases.
func (c *serviceConns) pgxPool(serviceName string) *pgxpool.Pool {
	sdb, ok := migrations.ServiceDatabases[serviceName]
	if !ok {
		panic(fmt.Sprintf("unknown service %q: not found in ServiceDatabases", serviceName))
	}
	return c.pgxPools[sdb.Database]
}

// closeAll closes all database connections.
func (c *serviceConns) closeAll(logger *slog.Logger) {
	for name, db := range c.gormDBs {
		bootstrap.CloseDatabase(db, logger)
		logger.Info("closed GORM connection", "database", name)
	}
	for name, pool := range c.pgxPools {
		pool.Close()
		logger.Info("closed pgxpool connection", "database", name)
	}
}

// newServiceConns creates per-database GORM and pgxpool connections for all services
// defined in the ServiceDatabases map. The baseDSN provides the host, port, and
// credentials; the database name is replaced for each service's target database.
func newServiceConns(ctx context.Context, baseDSN string, logger *slog.Logger) (*serviceConns, error) {
	conns := &serviceConns{
		gormDBs:  make(map[string]*gorm.DB),
		pgxPools: make(map[string]*pgxpool.Pool),
	}

	// Services requiring GORM connections.
	gormServices := []string{
		"party", "tenant", "internal-account",
		"financial-accounting", "current-account",
		"payment-order", "reconciliation",
	}

	// Services requiring pgxpool connections.
	pgxServices := []string{
		"control-plane", "reference-data", "market-information",
		"position-keeping", "forecasting",
	}

	// Create GORM connections (deduplicated by database name).
	for _, svc := range gormServices {
		sdb := migrations.ServiceDatabases[svc]
		if _, exists := conns.gormDBs[sdb.Database]; exists {
			continue
		}
		dsn := replaceDSNDatabase(baseDSN, sdb.Database)
		cfg := bootstrap.DatabaseConfig{
			DSN:             dsn,
			MaxOpenConns:    10,
			MaxIdleConns:    2,
			ConnMaxLifetime: 5 * time.Minute,
			ConnMaxIdleTime: 10 * time.Minute,
			Logger:          logger,
		}
		db, err := bootstrap.NewDatabase(ctx, cfg)
		if err != nil {
			conns.closeAll(logger)
			return nil, fmt.Errorf("gorm %s: %w", sdb.Database, err)
		}
		conns.gormDBs[sdb.Database] = db
	}

	// Create pgxpool connections (deduplicated by database name).
	for _, svc := range pgxServices {
		sdb := migrations.ServiceDatabases[svc]
		if _, exists := conns.pgxPools[sdb.Database]; exists {
			continue
		}
		dsn := replaceDSNDatabase(baseDSN, sdb.Database)
		pool, err := connectPgxPool(ctx, dsn, logger)
		if err != nil {
			conns.closeAll(logger)
			return nil, fmt.Errorf("pgxpool %s: %w", sdb.Database, err)
		}
		conns.pgxPools[sdb.Database] = pool
	}

	logger.Info("per-service database connections established",
		"gorm_databases", len(conns.gormDBs),
		"pgxpool_databases", len(conns.pgxPools))

	return conns, nil
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// noopFAPublisher is a no-op event publisher for financial-accounting (no Kafka in dev).
type noopFAPublisher struct{}

func (p *noopFAPublisher) Publish(_ context.Context, _ financialaccountingservice.DomainEvent) error {
	return nil
}

func (p *noopFAPublisher) PublishBatch(_ context.Context, _ []financialaccountingservice.DomainEvent) error {
	return nil
}

// ─── Loopback gRPC Clients ──────────────────────────────────────────────────

// loopbackClients holds gRPC clients that connect back to the same unified binary
// for inter-service communication (e.g., current-account calling position-keeping).
type loopbackClients struct {
	mds      *misclient.Client
	mdsClose func()
	pk       *pkclient.Client
	pkClose  func()
	fa       *faclient.Client
	faClose  func()
}

// newLoopbackClients creates loopback gRPC clients targeting the local gRPC server.
// grpc.NewClient is lazy — it connects on first RPC, after the server is listening.
func newLoopbackClients(ctx context.Context, grpcPort int) (*loopbackClients, error) {
	target := fmt.Sprintf("localhost:%d", grpcPort)
	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}

	mds, mdsClose, err := misclient.New(ctx, misclient.Config{Target: target, DialOptions: opts})
	if err != nil {
		return nil, fmt.Errorf("mds loopback: %w", err)
	}

	pk, pkClose, err := pkclient.New(pkclient.Config{Target: target, DialOptions: opts}) //nolint:contextcheck // pkclient.New does not accept context
	if err != nil {
		_ = mdsClose()
		return nil, fmt.Errorf("pk loopback: %w", err)
	}

	fa, faClose, err := faclient.New(faclient.Config{Target: target, DialOptions: opts}) //nolint:contextcheck // faclient.New does not accept context
	if err != nil {
		_ = mdsClose()
		pkClose()
		return nil, fmt.Errorf("fa loopback: %w", err)
	}

	return &loopbackClients{
		mds: mds, mdsClose: func() { _ = mdsClose() },
		pk: pk, pkClose: pkClose,
		fa: fa, faClose: faClose,
	}, nil
}

// closeAll closes all loopback gRPC connections.
func (l *loopbackClients) closeAll() {
	if l.mdsClose != nil {
		l.mdsClose()
	}
	if l.pkClose != nil {
		l.pkClose()
	}
	if l.faClose != nil {
		l.faClose()
	}
}
