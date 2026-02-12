// Package main is the entry point for the Reference Data service.
// Manages instrument definitions, saga definitions, and hierarchical reference data nodes.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	sagav1 "github.com/meridianhub/meridian/api/proto/meridian/saga/v1"
	refcel "github.com/meridianhub/meridian/services/reference-data/cel"
	"github.com/meridianhub/meridian/services/reference-data/handler"
	"github.com/meridianhub/meridian/services/reference-data/node"
	"github.com/meridianhub/meridian/services/reference-data/registry"
	"github.com/meridianhub/meridian/services/reference-data/saga"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/ports"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
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
	logLevel := parseLogLevel(os.Getenv("LOG_LEVEL"))
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))
	slog.SetDefault(logger)

	logger.Info("starting reference-data service",
		"version", Version,
		"commit", Commit,
		"build_date", BuildDate)

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
		ServiceName:    "reference-data-service",
		ServiceVersion: Version,
		Logger:         logger,
	})
	if err != nil {
		return fmt.Errorf("failed to initialize tracer: %w", err)
	}
	defer bootstrap.ShutdownTracer(tracer, logger)

	// Initialize database connection
	dbURL := env.GetEnvOrDefault("DATABASE_URL",
		"postgres://meridian_reference_data_user@localhost:26257/meridian_reference_data?sslmode=disable")
	dbPool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("failed to create database connection pool: %w", err)
	}
	defer dbPool.Close()

	if err := dbPool.Ping(ctx); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}
	logger.Info("database connection established")

	// Initialize CEL compiler for instrument validation expressions
	compiler, err := refcel.NewCompiler()
	if err != nil {
		return fmt.Errorf("failed to create CEL compiler: %w", err)
	}
	logger.Info("CEL compiler initialized")

	// Create instrument registry
	instrumentRegistry, err := registry.NewPostgresRegistry(dbPool)
	if err != nil {
		return fmt.Errorf("failed to create instrument registry: %w", err)
	}
	logger.Info("instrument registry initialized")

	// Create Reference Data gRPC service handler
	refDataService, err := handler.NewService(instrumentRegistry, compiler, logger)
	if err != nil {
		return fmt.Errorf("failed to create reference data service: %w", err)
	}

	// Create Node service handler
	nodeRepo := node.NewPostgresRepository(dbPool)
	nodeService, err := handler.NewNodeService(nodeRepo, logger)
	if err != nil {
		return fmt.Errorf("failed to create node service: %w", err)
	}

	// Create Saga Registry handler
	sagaRegistry := saga.NewPostgresRegistry(dbPool, nil)
	sagaHandler := saga.NewRegistryHandler(sagaRegistry, nil, nil, logger)

	// Initialize auth interceptor
	authConfig := bootstrap.DefaultAuthConfig(logger)
	authInterceptor, err := bootstrap.NewAuthInterceptor(ctx, authConfig)
	if err != nil {
		return fmt.Errorf("failed to initialize auth: %w", err)
	}

	// Create gRPC server
	grpcServer := bootstrap.NewGrpcServerBuilder(tracer, logger).
		WithAuthInterceptor(authInterceptor).
		Build()

	// Register gRPC services
	referencedatav1.RegisterReferenceDataServiceServer(grpcServer, refDataService)
	referencedatav1.RegisterNodeServiceServer(grpcServer, nodeService)
	sagav1.RegisterSagaRegistryServiceServer(grpcServer, sagaHandler)

	// Register health check service
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	healthServer.SetServingStatus("reference-data", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)

	// Register reflection service for debugging
	reflection.Register(grpcServer)

	logger.Info("gRPC services registered")

	// Get ports
	port := env.GetEnvOrDefault("GRPC_PORT", strconv.Itoa(ports.ReferenceData))
	address := fmt.Sprintf(":%s", port)
	metricsPort := env.GetEnvOrDefault("METRICS_PORT", "8082")

	// Create listener
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", address)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", address, err)
	}

	serverErrors := make(chan error, 2)
	go func() {
		logger.Info("starting gRPC server", "address", address)
		if err := grpcServer.Serve(listener); err != nil {
			serverErrors <- err
		}
	}()

	// Start HTTP server for metrics and health
	httpMux := http.NewServeMux()
	httpMux.Handle("/metrics", promhttp.Handler())
	httpMux.HandleFunc("/health", healthHandler(grpcServer))
	httpMux.HandleFunc("/ready", healthHandler(grpcServer))

	httpServer := &http.Server{
		Addr:              fmt.Sprintf(":%s", metricsPort),
		Handler:           httpMux,
		ReadHeaderTimeout: defaults.DefaultHTTPReadHeaderTimeout,
		WriteTimeout:      defaults.DefaultHTTPWriteTimeout,
	}

	go func() {
		logger.Info("starting HTTP server for metrics", "address", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server error", "error", err)
			serverErrors <- fmt.Errorf("HTTP server error: %w", err)
		}
	}()

	// Wait for shutdown
	orchestrator := bootstrap.NewShutdownOrchestrator(grpcServer, logger)

	orchestrator.AddCleanup(func() error {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), defaults.DefaultGracefulShutdown)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("HTTP server shutdown error", "error", err)
			return err
		}
		logger.Info("HTTP server stopped")
		return nil
	})

	return orchestrator.Wait(serverErrors)
}

func healthHandler(_ *grpc.Server) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("SERVING")); err != nil {
			slog.Warn("failed to write health response", "error", err)
		}
	}
}

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
