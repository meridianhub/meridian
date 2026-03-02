// Package main is the entry point for the operational-gateway standalone binary.
//
// It wires all operational-gateway components: gRPC services, background workers,
// repositories, and event publishing. The binary can be run standalone or integrated
// into the unified Meridian binary.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"

	opgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/operational_gateway/v1"
	"github.com/meridianhub/meridian/services/operational-gateway/adapters/httpadapter"
	"github.com/meridianhub/meridian/services/operational-gateway/adapters/messaging"
	"github.com/meridianhub/meridian/services/operational-gateway/adapters/passthrough"
	"github.com/meridianhub/meridian/services/operational-gateway/adapters/persistence"
	"github.com/meridianhub/meridian/services/operational-gateway/adapters/secrets"
	"github.com/meridianhub/meridian/services/operational-gateway/config"
	"github.com/meridianhub/meridian/services/operational-gateway/service"
	"github.com/meridianhub/meridian/services/operational-gateway/worker"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/events"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

// ErrMissingDatabaseURL is returned when the DATABASE_URL environment variable is not set.
var ErrMissingDatabaseURL = errors.New("DATABASE_URL is required")

// Build information set via ldflags during compilation.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

func main() {
	logLevel := parseLogLevel(os.Getenv("LOG_LEVEL"))
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))
	slog.SetDefault(logger)

	logger.Info("starting operational-gateway service",
		"version", Version,
		"commit", Commit,
		"build_date", BuildDate)

	if err := bootstrap.RunWithRetry(
		func() error { return run(logger) },
		bootstrap.WithRetryLogger(logger),
	); err != nil {
		logger.Error("service failed to start", "error", err)
		os.Exit(1)
	}

	logger.Info("service stopped gracefully")
}

func run(logger *slog.Logger) error {
	ctx := context.Background()
	cfg := config.LoadConfig()

	// Initialize OpenTelemetry tracer.
	tracer, err := bootstrap.NewTracer(ctx, bootstrap.TracerConfig{
		ServiceName:    "operational-gateway-service",
		ServiceVersion: Version,
		Logger:         logger,
	})
	if err != nil {
		return fmt.Errorf("failed to initialize tracer: %w", err)
	}
	defer bootstrap.ShutdownTracer(tracer, logger)

	// Initialize database connection.
	if cfg.DatabaseURL == "" {
		return bootstrap.Permanent(ErrMissingDatabaseURL)
	}

	dbCfg := bootstrap.DefaultDatabaseConfig()
	dbCfg.DSN = cfg.DatabaseURL
	dbCfg.Logger = logger

	db, err := bootstrap.NewDatabase(ctx, dbCfg)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer bootstrap.CloseDatabase(db, logger)

	logger.Info("database connection established")

	// Initialize repositories.
	instructionRepo := persistence.NewInstructionRepository(db)
	connectionRepo := persistence.NewConnectionRepository(db)
	routeRepo := persistence.NewRouteRepository(db)

	// Initialize event publishing (outbox pattern).
	outboxPublisher := events.NewOutboxPublisher("operational-gateway")
	eventPublisher := messaging.NewInstructionEventPublisher(outboxPublisher)

	// Initialize auth interceptor.
	authConfig := bootstrap.DefaultAuthConfig(logger)
	authInterceptor, err := bootstrap.NewAuthInterceptor(ctx, authConfig)
	if err != nil {
		return fmt.Errorf("failed to initialize auth: %w", err)
	}

	// Create gRPC server.
	grpcServer := bootstrap.NewGrpcServerBuilder(tracer, logger).
		WithAuthInterceptor(authInterceptor).
		Build()

	// Initialize and register OperationalGatewayService.
	gatewaySvc, err := service.NewOperationalGatewayService(instructionRepo, connectionRepo, logger)
	if err != nil {
		return fmt.Errorf("failed to create gateway service: %w", err)
	}
	gatewaySvc.WithEventPublishing(db, instructionRepo, eventPublisher)
	opgatewayv1.RegisterOperationalGatewayServiceServer(grpcServer, gatewaySvc)

	// Initialize and register ProviderConnectionService.
	connectionSvc, err := service.NewProviderConnectionService(connectionRepo, instructionRepo, logger)
	if err != nil {
		return fmt.Errorf("failed to create connection service: %w", err)
	}
	opgatewayv1.RegisterProviderConnectionServiceServer(grpcServer, connectionSvc)

	// Initialize and register InstructionRouteService.
	routeSvc, err := service.NewInstructionRouteService(routeRepo, connectionRepo, logger)
	if err != nil {
		return fmt.Errorf("failed to create route service: %w", err)
	}
	opgatewayv1.RegisterInstructionRouteServiceServer(grpcServer, routeSvc)

	// Register health check.
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)

	// Register reflection for debugging.
	reflection.Register(grpcServer)

	logger.Info("gRPC services registered")

	// Initialize dispatch worker dependencies.
	routeResolver := persistence.NewDBRouteResolver(routeRepo)
	secretStore := secrets.NewEnvSecretStore(&identitySlugResolver{})
	transformer := passthrough.NewTransformer()
	dispatcher := httpadapter.NewHTTPDispatcher(secretStore, transformer, logger)

	dispatchWorker := worker.NewDispatchWorker(
		instructionRepo,
		connectionRepo,
		routeResolver,
		dispatcher,
		worker.DispatchWorkerConfig{
			BatchSize:    cfg.DispatchWorker.BatchSize,
			PollInterval: cfg.DispatchWorker.PollInterval,
		},
		logger,
	)

	// Initialize expiry worker.
	expiryWorker := worker.NewExpiryWorker(
		instructionRepo,
		worker.ExpiryWorkerConfig{
			ScanInterval: cfg.ExpiryWorker.ScanInterval,
			BatchSize:    cfg.ExpiryWorker.BatchSize,
		},
		logger,
	)

	// Start background workers.
	dispatchWorker.Start(ctx)
	expiryWorker.Start(ctx)

	// Create listener.
	address := fmt.Sprintf(":%s", cfg.GRPCPort)
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", address, err)
	}

	// Start gRPC server in background.
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("starting gRPC server", "address", address)
		if err := grpcServer.Serve(listener); err != nil {
			serverErrors <- err
		}
	}()

	// Wait for shutdown signal.
	orchestrator := bootstrap.NewShutdownOrchestrator(grpcServer, logger)

	orchestrator.AddCleanup(func() error {
		dispatchWorker.Stop()
		return nil
	})

	orchestrator.AddCleanup(func() error {
		expiryWorker.Stop()
		return nil
	})

	orchestrator.AddCleanup(func() error {
		bootstrap.CloseDatabase(db, logger)
		return nil
	})

	return orchestrator.Wait(serverErrors)
}

// identitySlugResolver is a TenantSlugResolver that returns the tenant ID as-is.
// This is suitable for environments where tenant IDs are already usable as slugs
// or where the env-based secret naming convention uses tenant IDs directly.
type identitySlugResolver struct{}

func (r *identitySlugResolver) GetSlug(_ context.Context, tenantID string) (string, error) {
	return tenantID, nil
}

// parseLogLevel converts a string log level to slog.Level.
func parseLogLevel(levelStr string) slog.Level {
	switch strings.ToLower(levelStr) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
