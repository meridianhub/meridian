// Package main is the entry point for the Reconciliation service.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	reconciliationv1 "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	"github.com/meridianhub/meridian/services/reconciliation/app"
	"github.com/meridianhub/meridian/services/reconciliation/config"
	"github.com/meridianhub/meridian/services/reconciliation/observability"
	"github.com/meridianhub/meridian/shared/pkg/health"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

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

	logger.Info("starting reconciliation service",
		"version", Version,
		"commit", Commit,
		"build_date", BuildDate)

	// Run the service with retry for transient startup errors
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

	// Load configuration (permanent error if invalid)
	cfg, err := config.LoadConfig()
	if err != nil {
		return bootstrap.Permanent(fmt.Errorf("failed to load configuration: %w", err))
	}

	logger.Info("configuration loaded",
		"environment", cfg.Observability.Environment,
		"grpc_port", cfg.Server.Port,
		"metrics_port", cfg.Observability.MetricsPort)

	// Initialize dependency container
	container, err := app.NewContainer(ctx, cfg, logger, Version)
	if err != nil {
		return err
	}
	defer container.Close()

	grpcServer, healthAggregator, err := buildGRPCServer(container, logger)
	if err != nil {
		return err
	}

	// Create gRPC listener
	grpcAddress := fmt.Sprintf(":%s", cfg.Server.Port)
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", grpcAddress)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", grpcAddress, err)
	}

	// Start gRPC server in background
	serverErrors := make(chan error, 2)
	go func() {
		logger.Info("starting gRPC server", "address", grpcAddress)
		if err := grpcServer.Serve(listener); err != nil {
			serverErrors <- err
		}
	}()

	// Start settlement scheduler in background (after gRPC server is listening)
	schedulerCtx, schedulerCancel := context.WithCancel(context.Background())
	defer schedulerCancel()
	if container.CronScheduler != nil {
		go func() {
			if err := container.CronScheduler.Start(schedulerCtx); err != nil {
				logger.Error("scheduler stopped with error", "error", err)
			}
		}()
	}

	httpServer := buildHTTPServer(cfg, healthAggregator)
	defer shutdownHTTPServer(httpServer, logger)

	go func() {
		logger.Info("starting HTTP server for health and metrics",
			"address", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- fmt.Errorf("HTTP server error: %w", err)
		}
	}()

	// Wait for interrupt signal or server error
	sigChan, signalCleanup := bootstrap.SignalHandler()
	defer signalCleanup()

	var runErr error
	select {
	case sig := <-sigChan:
		logger.Info("received signal", "signal", sig)
	case err := <-serverErrors:
		logger.Error("server error", "error", err)
		runErr = fmt.Errorf("server error: %w", err)
	}

	gracefulShutdown(container, grpcServer, cfg, schedulerCancel, logger)

	return runErr
}

// buildGRPCServer creates and configures the gRPC server with all service registrations.
func buildGRPCServer(container *app.Container, logger *slog.Logger) (*grpc.Server, *health.Aggregator, error) {
	grpcServer, err := bootstrap.NewGrpcServerBuilder(container.Tracer, logger).
		WithAuthInterceptor(container.AuthInterceptor).
		Build()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build grpc server: %w", err)
	}

	reconciliationv1.RegisterAccountReconciliationServiceServer(grpcServer, container.ReconciliationService)

	healthCheckers := []health.Checker{
		observability.NewDatabaseChecker(container.DB),
	}
	if container.RedisClient != nil {
		healthCheckers = append(healthCheckers, observability.NewRedisChecker(container.RedisClient))
	}
	healthAggregator := health.NewAggregator(healthCheckers)
	grpc_health_v1.RegisterHealthServer(grpcServer, newHealthServer(healthAggregator, logger))

	reflection.Register(grpcServer)

	logger.Info("gRPC services registered",
		"services", []string{"AccountReconciliationService", "Health", "Reflection"})

	return grpcServer, healthAggregator, nil
}

// buildHTTPServer creates the HTTP server for metrics and health endpoints.
func buildHTTPServer(cfg *config.Config, healthAggregator *health.Aggregator) *http.Server {
	httpMux := http.NewServeMux()
	httpMux.Handle("/metrics", promhttp.Handler())

	healthHandler := health.NewHTTPHandler(healthAggregator)
	healthHandler.RegisterHandlers(httpMux)

	return &http.Server{
		Addr:              fmt.Sprintf(":%s", cfg.Observability.MetricsPort),
		Handler:           httpMux,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
}

// shutdownHTTPServer gracefully shuts down the HTTP server.
func shutdownHTTPServer(httpServer *http.Server, logger *slog.Logger) {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP server shutdown error", "error", err)
	} else {
		logger.Info("HTTP server stopped")
	}
}

// gracefulShutdown stops the scheduler and gRPC server in order.
func gracefulShutdown(container *app.Container, grpcServer *grpc.Server, cfg *config.Config, schedulerCancel context.CancelFunc, logger *slog.Logger) {
	logger.Info("shutting down servers...")

	// Stop scheduler first (it makes gRPC calls to self)
	if container.CronScheduler != nil {
		logger.Info("stopping settlement scheduler...")
		schedulerCancel()
		container.CronScheduler.Stop()
		logger.Info("settlement scheduler stopped")
	}

	// Outbox worker and Kafka producer are cleaned up via container.Close().

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.GracefulShutdownTimeout)
	defer cancel()

	stopped := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(stopped)
	}()

	select {
	case <-stopped:
		logger.Info("gRPC server stopped gracefully")
	case <-shutdownCtx.Done():
		logger.Warn("graceful shutdown timeout, forcing stop")
		grpcServer.Stop()
	}
}

// healthServer implements the gRPC health checking protocol.
type healthServer struct {
	grpc_health_v1.UnimplementedHealthServer
	aggregator *health.Aggregator
	logger     *slog.Logger
}

func newHealthServer(aggregator *health.Aggregator, logger *slog.Logger) *healthServer {
	return &healthServer{
		aggregator: aggregator,
		logger:     logger,
	}
}

// Check performs a health check.
func (h *healthServer) Check(ctx context.Context, _ *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	report := h.aggregator.CheckAll(ctx)

	grpcStatus := grpc_health_v1.HealthCheckResponse_SERVING
	overallStatus := report.OverallStatus()
	if overallStatus == health.StatusUnhealthy || overallStatus == health.StatusDegraded {
		grpcStatus = grpc_health_v1.HealthCheckResponse_NOT_SERVING
		h.logger.Warn("health check failed",
			"status", overallStatus,
			"checked_at", report.CheckedAt)
	}

	return &grpc_health_v1.HealthCheckResponse{
		Status: grpcStatus,
	}, nil
}

// parseLogLevel converts a string log level to slog.Level.
func parseLogLevel(levelStr string) slog.Level {
	switch strings.ToLower(levelStr) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
