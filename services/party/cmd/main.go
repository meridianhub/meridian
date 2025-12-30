// Package main is the entry point for the Party service.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"time"

	pb "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/meridianhub/meridian/services/party/adapters/persistence"
	"github.com/meridianhub/meridian/services/party/service"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/env"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

// Build information set via ldflags during compilation
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

func main() {
	// Initialize structured logging with configurable log level
	// Note: bootstrap.NewLogger hardcodes INFO level, so we create logger manually for LOG_LEVEL support
	logLevel := parseLogLevel(os.Getenv("LOG_LEVEL"))
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))
	slog.SetDefault(logger)

	logger.Info("starting party service",
		"version", Version,
		"commit", Commit,
		"build_date", BuildDate)

	// Run the service
	if err := run(logger); err != nil {
		logger.Error("service failed", "error", err)
		os.Exit(1)
	}

	logger.Info("service stopped gracefully")
}

func run(logger *slog.Logger) error {
	ctx := context.Background()

	// Initialize OpenTelemetry tracer
	tracer, err := bootstrap.NewTracer(ctx, bootstrap.TracerConfig{
		ServiceName:    "party-service",
		ServiceVersion: Version,
		Logger:         logger,
	})
	if err != nil {
		return fmt.Errorf("failed to initialize tracer: %w", err)
	}
	defer bootstrap.ShutdownTracer(tracer, logger)

	// Initialize database connection
	dbConfig := bootstrap.DefaultDatabaseConfig()
	dbConfig.Logger = logger
	db, err := bootstrap.NewDatabase(ctx, dbConfig)
	if err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}

	logger.Info("database connection established")

	// Create repository
	repo := persistence.NewRepository(db)

	// Create party service
	partyService, err := service.NewService(repo, logger)
	if err != nil {
		return fmt.Errorf("failed to create party service: %w", err)
	}

	logger.Info("party service initialized")

	// Initialize auth interceptor (optional - based on AUTH_ENABLED)
	authConfig := bootstrap.DefaultAuthConfig(logger)
	authInterceptor, err := bootstrap.NewAuthInterceptor(ctx, authConfig)
	if err != nil {
		return fmt.Errorf("failed to initialize auth: %w", err)
	}

	// Create gRPC server with interceptor chain
	// Order is handled by bootstrap: tracing -> auth -> recovery
	grpcServer := bootstrap.NewGrpcServerBuilder(tracer, logger).
		WithAuthInterceptor(authInterceptor).
		Build()

	// Register services
	pb.RegisterPartyServiceServer(grpcServer, partyService)

	// Register health check service
	healthChecker := service.NewHealthChecker(service.HealthCheckerConfig{
		Repository:   repo,
		Logger:       logger,
		ServiceName:  "party",
		CheckTimeout: 5 * time.Second,
	})
	grpc_health_v1.RegisterHealthServer(grpcServer, healthChecker)

	// Register reflection service for debugging
	reflection.Register(grpcServer)

	logger.Info("gRPC services registered")

	// Get port from environment (default 50055 per task spec)
	port := env.GetEnvOrDefault("GRPC_PORT", "50055")
	address := fmt.Sprintf(":%s", port)

	// Create listener
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", address)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", address, err)
	}

	// Start gRPC server in background
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("starting gRPC server", "address", address)
		if err := grpcServer.Serve(listener); err != nil {
			serverErrors <- err
		}
	}()

	// Wait for shutdown signal and orchestrate graceful shutdown
	orchestrator := bootstrap.NewShutdownOrchestrator(grpcServer, logger)
	orchestrator.AddCleanup(func() error {
		bootstrap.CloseDatabase(db, logger)
		return nil
	})

	return orchestrator.Wait(serverErrors)
}

// parseLogLevel converts a string log level to slog.Level.
// Supports: debug, info, warn, error (case-insensitive). Defaults to info.
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
