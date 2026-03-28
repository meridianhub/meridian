// Package main is the entry point for the Control Plane service.
// Manages manifest application, validation, and diffing.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/services/control-plane/internal/server"
	"github.com/meridianhub/meridian/services/control-plane/service"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/meridianhub/meridian/shared/platform/ports"
	"google.golang.org/grpc"
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

	logger.Info("starting control-plane service",
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

	// Initialize OpenTelemetry tracer
	tracer, err := bootstrap.NewTracer(ctx, bootstrap.TracerConfig{
		ServiceName:    "control-plane-service",
		ServiceVersion: Version,
		Logger:         logger,
	})
	if err != nil {
		return fmt.Errorf("failed to initialize tracer: %w", err)
	}
	defer bootstrap.ShutdownTracer(tracer, logger)

	// Initialize database connection
	pool, err := initDatabase(ctx, logger)
	if err != nil {
		return err
	}
	defer pool.Close()

	// Build gRPC server with auth and RBAC interceptors
	grpcServer, err := buildGRPCServer(ctx, tracer, logger)
	if err != nil {
		return err
	}

	// Register services
	if err := registerServices(grpcServer, pool, logger); err != nil {
		return err
	}

	// Start listener and wait for shutdown
	return startAndServe(grpcServer, pool, logger)
}

// initDatabase creates and validates the pgxpool database connection.
func initDatabase(ctx context.Context, logger *slog.Logger) (*pgxpool.Pool, error) {
	dbURL := env.GetEnvOrDefault("DATABASE_URL", "")
	if dbURL == "" {
		return nil, bootstrap.Permanent(ErrMissingDatabaseURL)
	}

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}
	logger.Info("database connection established")

	return pool, nil
}

// buildGRPCServer creates the gRPC server with auth and RBAC interceptors.
func buildGRPCServer(ctx context.Context, tracer *observability.Tracer, logger *slog.Logger) (*grpc.Server, error) {
	authConfig := bootstrap.DefaultAuthConfig(logger)
	authInterceptor, err := bootstrap.NewAuthInterceptor(ctx, authConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize auth: %w", err)
	}

	grpcServer, err := bootstrap.NewGrpcServerBuilder(tracer, logger).
		WithAuthInterceptor(authInterceptor).
		WithUnaryInterceptor(server.ManifestRBACUnaryInterceptor()).
		WithStreamInterceptor(server.ManifestRBACStreamInterceptor()).
		Build() //nolint:contextcheck // builder pattern; context passed via auth interceptor
	if err != nil {
		return nil, fmt.Errorf("failed to build grpc server: %w", err)
	}

	return grpcServer, nil
}

// registerServices registers gRPC services including control-plane, health, and reflection.
func registerServices(grpcServer *grpc.Server, pool *pgxpool.Pool, logger *slog.Logger) error {
	// Register ApplyManifestService.
	// HandlerDeps is nil here: this binary validates, diffs, and plans manifests
	// but defers saga execution to the unified binary which has access to downstream
	// service connections (reference_data, internal_account, operational_gateway).
	if err := service.RegisterApplyManifestService(grpcServer, service.ApplyManifestServiceConfig{
		Pool:        pool,
		Logger:      logger,
		HandlerDeps: nil,
	}); err != nil {
		return fmt.Errorf("failed to register control-plane service: %w", err)
	}

	// Register health check
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)

	// Register reflection for debugging
	reflection.Register(grpcServer)

	logger.Info("gRPC services registered")
	return nil
}

// startAndServe creates a listener, starts the gRPC server, and waits for shutdown.
func startAndServe(grpcServer *grpc.Server, pool *pgxpool.Pool, logger *slog.Logger) error {
	port := env.GetEnvOrDefault("GRPC_PORT", strconv.Itoa(ports.ControlPlane))
	address := fmt.Sprintf(":%s", port)

	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", address)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", address, err)
	}

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("starting gRPC server", "address", address)
		if err := grpcServer.Serve(listener); err != nil {
			serverErrors <- err
		}
	}()

	orchestrator := bootstrap.NewShutdownOrchestrator(grpcServer, logger)
	orchestrator.AddCleanup(func() error {
		pool.Close()
		logger.Info("database connection closed")
		return nil
	})

	return orchestrator.Wait(serverErrors)
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
