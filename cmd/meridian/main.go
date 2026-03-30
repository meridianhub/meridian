// Package main is the entry point for the Meridian unified binary.
//
// It wires all Meridian services into a single Go process with a shared gRPC
// server and gateway HTTP server. Services are initialized in tier dependency order:
//
//   - Tier 0 (no deps): party, reference-data, market-information, tenant, internal-account, identity
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
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	masterbootstrap "github.com/meridianhub/meridian/internal/bootstrap"
	"github.com/meridianhub/meridian/internal/migrations"
	"github.com/meridianhub/meridian/services"
	gateway "github.com/meridianhub/meridian/services/api-gateway"
	tenantprovisioner "github.com/meridianhub/meridian/services/tenant/provisioner"
	tenantworker "github.com/meridianhub/meridian/services/tenant/worker"
	"github.com/meridianhub/meridian/shared/pkg/dispatch"
	"github.com/meridianhub/meridian/shared/pkg/email"
	emailworker "github.com/meridianhub/meridian/shared/pkg/email/worker"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/audit"
	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/observability"

	pkdomain "github.com/meridianhub/meridian/services/position-keeping/domain"

	"google.golang.org/grpc"
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
		"EMAIL_MODE":      "disabled",
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

// unifiedInfra holds shared infrastructure components initialized during startup.
type unifiedInfra struct {
	baseDSN         string
	conns           *serviceConns
	tracer          *observability.Tracer
	grpcServer      *grpc.Server
	idempotencySvc  *idempotency.PostgresService
	faEventPub      *noopFAPublisher
	pkEventPub      pkdomain.EventPublisher
	outboxPublisher *events.OutboxPublisher
	outboxRepo      *events.PostgresOutboxRepository
	loopback        *loopbackClients
	provIface       tenantprovisioner.SchemaProvisioner
	schemaProv      *tenantprovisioner.PostgresProvisioner
}

func run(logger *slog.Logger, grpcPort, httpPort int) error {
	ctx := context.Background()
	logger.Info("starting meridian unified binary", "grpc_port", grpcPort, "http_port", httpPort)

	// Initialize shared infrastructure
	infra, err := initInfrastructure(ctx, grpcPort, logger)
	if err != nil {
		return err
	}
	defer infra.conns.closeAll(logger)
	defer infra.loopback.closeAll()

	// Register all services on the shared gRPC server
	auditWorker, err := registerServices(ctx, infra.grpcServer, infra.conns, infra.idempotencySvc, infra.faEventPub, infra.pkEventPub, infra.outboxPublisher, infra.outboxRepo, infra.loopback, infra.provIface, infra.tracer, logger)
	if err != nil {
		return err
	}

	// Start workers
	provisioningWorker, provisionerCleanup, err := startProvisioningWorker(ctx, infra.schemaProv, infra.conns.gormDB("tenant"), infra.conns.gormDB("identity"), logger)
	if err != nil {
		return fmt.Errorf("provisioning worker: %w", err)
	}
	if provisionerCleanup != nil {
		defer provisionerCleanup()
	}
	emailWorker := startEmailWorker(ctx, infra.conns.gormDB("payment-order"), logger)

	// Start gRPC server
	grpcAddr := fmt.Sprintf(":%d", grpcPort)
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", grpcAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", grpcAddr, err)
	}
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("gRPC server starting", "address", grpcAddr)
		if err := infra.grpcServer.Serve(listener); err != nil {
			serverErrors <- err
		}
	}()

	// Start gateway HTTP server
	gwServer, routerCancel, err := setupAndStartGateway(ctx, infra, grpcPort, httpPort, logger)
	if err != nil {
		return err
	}
	defer routerCancel()
	gatewayErrors := make(chan error, 1)
	go func() {
		if err := gwServer.Start(context.Background()); err != nil {
			gatewayErrors <- err
		}
	}()

	return awaitAndShutdown(infra.grpcServer, gwServer, auditWorker, provisioningWorker, emailWorker, serverErrors, gatewayErrors, logger)
}

// initInfrastructure creates all shared infrastructure: database connections, tracer,
// gRPC server, idempotency service, event publishers, loopback clients, and schema provisioner.
func initInfrastructure(ctx context.Context, grpcPort int, logger *slog.Logger) (*unifiedInfra, error) {
	baseDSN := env.GetEnvOrDefault("DATABASE_URL",
		"postgres://root@localhost:26257/defaultdb?sslmode=disable")
	conns, err := newServiceConns(ctx, baseDSN, logger)
	if err != nil {
		return nil, fmt.Errorf("service connections: %w", err)
	}

	tracer, err := observability.NewTracer(ctx, observability.TracerConfig{
		ServiceName:  "meridian-unified",
		OTLPEndpoint: "localhost:4317",
		Enabled:      false,
	})
	if err != nil {
		conns.closeAll(logger)
		return nil, fmt.Errorf("tracer: %w", err)
	}

	grpcServer, err := bootstrap.NewGrpcServerBuilder(tracer, logger).
		WithoutAuth().
		Build() //nolint:contextcheck // gRPC interceptors manage their own contexts
	if err != nil {
		conns.closeAll(logger)
		return nil, fmt.Errorf("failed to build grpc server: %w", err)
	}

	idempotencySvc := idempotency.NewPostgresService(conns.pgxPool("control-plane"))
	if err := idempotencySvc.EnsureTable(ctx); err != nil {
		conns.closeAll(logger)
		return nil, fmt.Errorf("idempotency table: %w", err)
	}

	infra := &unifiedInfra{
		baseDSN:         baseDSN,
		conns:           conns,
		tracer:          tracer,
		grpcServer:      grpcServer,
		idempotencySvc:  idempotencySvc,
		faEventPub:      &noopFAPublisher{},
		pkEventPub:      pkdomain.NewNoOpEventPublisher(),
		outboxPublisher: events.NewOutboxPublisher("unified"),
		outboxRepo:      events.NewPostgresOutboxRepository(conns.gormDB("financial-accounting")),
	}

	if err := initLoopbackAndProvisioner(ctx, infra, grpcPort, logger); err != nil {
		return nil, err
	}
	return infra, nil
}

// initLoopbackAndProvisioner creates loopback clients and schema provisioner for the unified infra.
func initLoopbackAndProvisioner(ctx context.Context, infra *unifiedInfra, grpcPort int, logger *slog.Logger) error {
	svcAuthCfg := platformauth.NewServiceAuthConfigFromEnv()
	svcCreds, err := svcAuthCfg.NewCredentials()
	if err != nil {
		infra.conns.closeAll(logger)
		return fmt.Errorf("service auth credentials: %w", err)
	}

	loopback, err := newLoopbackClients(ctx, grpcPort, svcCreds)
	if err != nil {
		infra.conns.closeAll(logger)
		return fmt.Errorf("loopback clients: %w", err)
	}
	infra.loopback = loopback

	schemaProvisioner, err := createSchemaProvisioner(infra.baseDSN, infra.conns.gormDB("tenant"), logger)
	if err != nil {
		loopback.closeAll()
		infra.conns.closeAll(logger)
		return fmt.Errorf("schema provisioner: %w", err)
	}
	infra.schemaProv = schemaProvisioner
	if schemaProvisioner != nil {
		infra.provIface = schemaProvisioner
	}
	return nil
}

// setupAndStartGateway wires all gateway HTTP handlers and creates the gateway server.
func setupAndStartGateway(ctx context.Context, infra *unifiedInfra, grpcPort, httpPort int, logger *slog.Logger) (*gateway.Server, context.CancelFunc, error) {
	platformDSN, err := replaceDSNDatabase(infra.baseDSN, "meridian_platform")
	if err != nil {
		return nil, nil, fmt.Errorf("platform DSN: %w", err)
	}

	eventRouter, extraGWOpts := wireEventStream(infra.conns.gormDB("financial-accounting"), logger)

	dexOpt, err := wireEmbeddedDex(ctx, infra.conns.gormDB("identity"), logger)
	if err != nil {
		return nil, nil, fmt.Errorf("embedded dex: %w", err)
	}
	extraGWOpts = append(extraGWOpts, dexOpt)

	bffSigner, bffAuthOpts := wireBFFAuth(infra.conns.gormDB("identity"), logger)
	extraGWOpts = append(extraGWOpts, bffAuthOpts...)

	baseDomain := env.GetEnvOrDefault("BASE_DOMAIN", "localhost")
	emailOutboxRepo := email.NewPostgresOutboxRepository(infra.conns.gormDB("payment-order"))
	if regOpt := wireRegistration(infra.conns.gormDB("identity"), infra.conns.gormDB("tenant"), infra.loopback.rawConn, baseDomain, emailOutboxRepo, logger); regOpt != nil {
		extraGWOpts = append(extraGWOpts, regOpt)
	}
	if verifyOpt := wireVerification(infra.conns.gormDB("identity"), emailOutboxRepo, baseDomain, logger); verifyOpt != nil {
		extraGWOpts = append(extraGWOpts, verifyOpt)
	}
	if resetOpt := wirePasswordReset(infra.conns.gormDB("identity"), emailOutboxRepo, baseDomain, logger); resetOpt != nil {
		extraGWOpts = append(extraGWOpts, resetOpt)
	}
	if webhookOpt := wireResendWebhook(infra.conns.gormDB("payment-order"), logger); webhookOpt != nil {
		extraGWOpts = append(extraGWOpts, webhookOpt)
	}
	if adminOpt := wireAdminHandler(infra.conns.gormDB("identity"), logger); adminOpt != nil {
		extraGWOpts = append(extraGWOpts, adminOpt)
	}

	gwServer, err := wireGateway(grpcPort, httpPort, platformDSN, infra.conns.gormDB("tenant"), bffSigner, logger, extraGWOpts...) //nolint:contextcheck // BuildAuthMiddleware manages its own context
	if err != nil {
		return nil, nil, fmt.Errorf("gateway init: %w", err)
	}

	routerCancel := startEventRouter(ctx, eventRouter, logger)

	return gwServer, routerCancel, nil
}

// awaitAndShutdown waits for a shutdown signal or server error, then gracefully stops all components.
func awaitAndShutdown(
	grpcServer *grpc.Server,
	gwServer *gateway.Server,
	auditWorker *audit.MultiTenantWorker,
	provisioningWorker *tenantworker.ProvisioningWorker,
	emailWorker *dispatch.Worker[*emailworker.OutboxInstruction],
	serverErrors, gatewayErrors chan error,
	logger *slog.Logger,
) error {
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

	// Stop workers first (drain in-flight work)
	if auditWorker != nil {
		auditWorker.Stop()
		logger.Info("audit worker stopped")
	}
	if provisioningWorker != nil {
		provisioningWorker.Stop()
		logger.Info("provisioning worker stopped")
	}
	if emailWorker != nil {
		emailWorker.Stop()
		logger.Info("email worker stopped")
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

// ─── Email Worker ────────────────────────────────────────────────────────────

// startEmailWorker creates and starts the email dispatch worker if email is
// configured (EMAIL_MODE is "log" or "live"). Returns the worker for graceful
// shutdown, or nil if email is disabled or misconfigured.
//
// When EMAIL_MODE is "disabled" (the dev default), the worker is not started at
// all. This prevents the NoopSender from silently consuming outbox entries and
// marking them as sent without actual delivery.
func startEmailWorker(ctx context.Context, paymentOrderDB *gorm.DB, logger *slog.Logger) *dispatch.Worker[*emailworker.OutboxInstruction] {
	mode := os.Getenv("EMAIL_MODE")
	if mode == "disabled" {
		logger.Info("email worker disabled", "email_mode", mode)
		return nil
	}

	sender, err := email.NewSenderFromEnv(logger)
	if err != nil {
		logger.Warn("email sender not configured, email worker disabled", "error", err)
		return nil
	}

	renderer, err := email.NewEmbeddedRenderer()
	if err != nil {
		logger.Error("email renderer init failed, email worker disabled", "error", err)
		return nil
	}

	outboxRepo := email.NewPostgresOutboxRepository(paymentOrderDB)
	auditRepo := email.NewPostgresAuditRepository(paymentOrderDB)
	metrics := email.NewMetrics()

	w := emailworker.NewEmailWorker(
		outboxRepo,
		auditRepo,
		renderer,
		sender,
		nil, // invoiceChecker - not wired yet (dunning validation)
		metrics,
		dispatch.WorkerConfig{
			BatchSize:    env.GetEnvAsInt("EMAIL_WORKER_BATCH_SIZE", dispatch.DefaultBatchSize),
			PollInterval: env.GetEnvAsDuration("EMAIL_WORKER_POLL_INTERVAL", 5*time.Second),
		},
		logger.With("component", "email-worker"),
	)

	w.Start(ctx)
	logger.Info("email worker started")
	return w
}

// ─── Bootstrap ──────────────────────────────────────────────────────────────

// runBootstrap provisions master tenant schemas and validates the platform manifest.
// It establishes database connections, runs the bootstrap process, and exits.
func runBootstrap(baseDSN string, logger *slog.Logger) error {
	ctx := context.Background()

	// Platform database (tenant service)
	platformDSN, err := replaceDSNDatabase(baseDSN, "meridian_platform")
	if err != nil {
		return fmt.Errorf("platform DSN: %w", err)
	}
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

	// Control-plane has its own database (meridian_control_plane)
	controlPlaneDSN, err := replaceDSNDatabase(baseDSN, "meridian_control_plane")
	if err != nil {
		return fmt.Errorf("control-plane DSN: %w", err)
	}
	controlPlanePool, err := connectPgxPool(ctx, controlPlaneDSN, logger)
	if err != nil {
		return fmt.Errorf("control-plane pgxpool: %w", err)
	}
	defer controlPlanePool.Close()

	controlPlaneCfg := bootstrap.DatabaseConfig{
		DSN:             controlPlaneDSN,
		MaxOpenConns:    5,
		MaxIdleConns:    2,
		ConnMaxLifetime: 5 * time.Minute,
		ConnMaxIdleTime: 10 * time.Minute,
		Logger:          logger,
	}
	controlPlaneDB, err := bootstrap.NewDatabase(ctx, controlPlaneCfg)
	if err != nil {
		return fmt.Errorf("control-plane database: %w", err)
	}
	defer bootstrap.CloseDatabase(controlPlaneDB, logger)

	provConfig, err := DeriveProvisionerConfig(baseDSN)
	if err != nil {
		return fmt.Errorf("provisioner config: %w", err)
	}

	return masterbootstrap.Run(ctx, masterbootstrap.Config{
		PlatformDB:        platformDB,
		ControlPlaneDB:    controlPlaneDB,
		ControlPlanePool:  controlPlanePool,
		ProvisionerConfig: provConfig,
		Logger:            logger,
	})
}
