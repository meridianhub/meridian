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
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/observability"

	pkdomain "github.com/meridianhub/meridian/services/position-keeping/domain"
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

	// Shared gRPC server (no auth for unified dev mode — auth handled at HTTP gateway)
	grpcServer, err := bootstrap.NewGrpcServerBuilder(tracer, logger).
		WithoutAuth().
		Build()
	if err != nil {
		return fmt.Errorf("failed to build grpc server: %w", err)
	}

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

	// Service-to-service auth credentials (opt-in via SERVICE_AUTH_ENABLED=true).
	svcAuthCfg := platformauth.NewServiceAuthConfigFromEnv()
	svcCreds, err := svcAuthCfg.NewCredentials()
	if err != nil {
		return fmt.Errorf("service auth credentials: %w", err)
	}

	// Loopback gRPC clients for inter-service communication within the unified binary.
	// grpc.NewClient is lazy — connects on first RPC, after the server is listening.
	loopback, err := newLoopbackClients(ctx, grpcPort, svcCreds)
	if err != nil {
		return fmt.Errorf("loopback clients: %w", err)
	}
	defer loopback.closeAll()

	// ─── Register All Services ──────────────────────────────────────────

	if err := registerServices(ctx, grpcServer, conns, idempotencySvc, faEventPublisher, pkEventPublisher, outboxPublisher, outboxRepo, loopback, tracer, logger); err != nil {
		return err
	}

	// ─── Provisioning Worker (optional) ─────────────────────────────────

	provisioningWorker, provisionerCleanup, err := startProvisioningWorker(ctx, baseDSN, conns.gormDB("tenant"), conns.gormDB("identity"), logger)
	if err != nil {
		return fmt.Errorf("provisioning worker: %w", err)
	}
	if provisionerCleanup != nil {
		defer provisionerCleanup()
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

	platformDSN, err := replaceDSNDatabase(baseDSN, "meridian_platform")
	if err != nil {
		return fmt.Errorf("platform DSN: %w", err)
	}

	eventRouter, extraGWOpts := wireEventStream(conns.gormDB("financial-accounting"), logger)

	// Embedded Dex OIDC server (opt-in via DEX_ISSUER).
	dexOpt, err := wireEmbeddedDex(ctx, conns.gormDB("identity"), logger)
	if err != nil {
		return fmt.Errorf("embedded dex: %w", err)
	}
	extraGWOpts = append(extraGWOpts, dexOpt)

	// Wire BFF auth handler: Meridian-signed JWTs for password login.
	// The identity connector validates credentials directly against the identity domain.
	bffSigner, bffAuthOpts := wireBFFAuth(conns.gormDB("identity"), logger)
	extraGWOpts = append(extraGWOpts, bffAuthOpts...)

	gwServer, err := wireGateway(grpcPort, httpPort, platformDSN, conns.gormDB("tenant"), bffSigner, logger, extraGWOpts...)
	if err != nil {
		return fmt.Errorf("gateway init: %w", err)
	}

	gatewayErrors := make(chan error, 1)

	// Start event router in background (consumes from EventSource and publishes to FanOut).
	routerCancel := startEventRouter(ctx, eventRouter, logger)
	defer routerCancel()

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

	// Stop provisioning worker first (drains in-flight work)
	if provisioningWorker != nil {
		provisioningWorker.Stop()
		logger.Info("provisioning worker stopped")
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

// ─── Bootstrap ──────────────────────────────────────────────────────────────

// runBootstrap provisions master tenant schemas and validates the platform manifest.
// It establishes database connections, runs the bootstrap process, and exits.
func runBootstrap(baseDSN string, logger *slog.Logger) error {
	ctx := context.Background()

	// Both tenant and control-plane share meridian_platform database.
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
