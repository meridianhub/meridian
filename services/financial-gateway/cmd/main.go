// Package main is the entry point for the financial-gateway standalone binary.
//
// It wires all financial-gateway components: gRPC service, Stripe adapter,
// platform bootstrap, and health checks.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"

	financialgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_gateway/v1"
	"github.com/meridianhub/meridian/services/financial-gateway/config"
	"github.com/meridianhub/meridian/services/financial-gateway/service"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
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

	logger.Info("starting financial-gateway service",
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
	ctx, runCancel := context.WithCancel(context.Background())
	defer runCancel()

	cfg := config.LoadConfig()

	// Initialize OpenTelemetry tracer.
	tracer, err := bootstrap.NewTracer(ctx, bootstrap.TracerConfig{
		ServiceName:    "financial-gateway-service",
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

	// Initialize and register FinancialGatewayService.
	svcCfg := service.Config{
		Logger: logger,
	}

	gatewaySvc, err := service.NewFinancialGatewayService(svcCfg)
	if err != nil {
		return fmt.Errorf("failed to create financial gateway service: %w", err)
	}
	financialgatewayv1.RegisterFinancialGatewayServiceServer(grpcServer, gatewaySvc)

	// Register health check.
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)

	// Register reflection for debugging.
	reflection.Register(grpcServer)

	logger.Info("gRPC services registered")

	// Create listener before serving to fail fast if the port is unavailable.
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
		runCancel()
		return nil
	})

	return orchestrator.Wait(serverErrors)
}

// parseLogLevel converts a string log level to slog.Level.
func parseLogLevel(levelStr string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(levelStr)) {
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
