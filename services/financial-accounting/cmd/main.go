// Package main is the entry point for the FinancialAccounting service.
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
	"time"

	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	"github.com/meridianhub/meridian/services/financial-accounting/app"
	serviceobs "github.com/meridianhub/meridian/services/financial-accounting/observability"
	"github.com/meridianhub/meridian/services/financial-accounting/service"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/ports"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
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

	logger.Info("starting financial-accounting service",
		"version", Version,
		"commit", Commit,
		"build_date", BuildDate)

	// Log environment for operational visibility
	environment := env.GetEnvOrDefault("ENVIRONMENT", "production")
	logger.Info("service environment configured", "environment", environment)

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

	// Initialize dependency container
	container, err := app.NewContainer(ctx, logger, Version)
	if err != nil {
		return err
	}
	defer container.Close()

	// Create Financial Accounting service
	financialAccountingSvc, err := service.NewFinancialAccountingService(
		container.LedgerRepo,
		container.EventPublisher,
		container.IdempotencyService,
		container.OutboxPublisher,
		container.OutboxRepo,
		container.ServiceOpts...,
	)
	if err != nil {
		return fmt.Errorf("failed to create financial accounting service: %w", err)
	}

	logger.Info("financial accounting service initialized")
	_ = container.PostingService // Available for internal use

	// Create gRPC server, register services, and start listening
	grpcServer, healthChecker, listener, err := setupGRPCServer(container, financialAccountingSvc, logger)
	if err != nil {
		return err
	}

	// Start gRPC server in background
	serverErrors := make(chan error, 2)
	go func() {
		logger.Info("starting gRPC server", "address", listener.Addr().String())
		if err := grpcServer.Serve(listener); err != nil {
			serverErrors <- err
		}
	}()

	// Start HTTP server for metrics and health
	metricsPort := env.GetEnvOrDefault("METRICS_PORT", "8082")
	httpServer := startHTTPServer(metricsPort, healthChecker, logger, serverErrors)

	return awaitAndShutdown(grpcServer, httpServer, serverErrors, logger)
}

// setupGRPCServer creates the gRPC server, registers all services, and creates the TCP listener.
func setupGRPCServer(container *app.Container, financialAccountingSvc financialaccountingv1.FinancialAccountingServiceServer, logger *slog.Logger) (*grpc.Server, *serviceobs.HealthChecker, net.Listener, error) {
	grpcServer, err := bootstrap.NewGrpcServerBuilder(container.Tracer, logger).
		WithAuthInterceptor(container.AuthInterceptor).
		Build()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to build grpc server: %w", err)
	}

	financialaccountingv1.RegisterFinancialAccountingServiceServer(grpcServer, financialAccountingSvc)

	healthChecker, err := serviceobs.NewHealthChecker(serviceobs.HealthCheckerConfig{
		DB:                   container.DB,
		Logger:               logger,
		ServiceName:          "financial-accounting",
		CheckTimeout:         defaults.DefaultHealthCheckTimeout,
		UsingNoopIdempotency: container.UsingNoopIdempotency,
		UsingNoopEvents:      container.UsingNoopEventPublisher,
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create health checker: %w", err)
	}
	grpc_health_v1.RegisterHealthServer(grpcServer, healthChecker)
	reflection.Register(grpcServer)

	logger.Info("gRPC services registered")

	port := env.GetEnvOrDefault("GRPC_PORT", strconv.Itoa(ports.FinancialAccounting))
	address := fmt.Sprintf(":%s", port)
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", address)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to listen on %s: %w", address, err)
	}

	return grpcServer, healthChecker, listener, nil
}

// startHTTPServer creates and starts the HTTP server for metrics and health endpoints.
func startHTTPServer(metricsPort string, healthChecker *serviceobs.HealthChecker, logger *slog.Logger, serverErrors chan error) *http.Server {
	httpMux := http.NewServeMux()
	httpMux.Handle("/metrics", promhttp.Handler())
	httpMux.HandleFunc("/health", newHealthHandler(healthChecker, logger))
	httpMux.HandleFunc("/ready", newReadyHandler(healthChecker, logger))

	httpServer := &http.Server{
		Addr:              fmt.Sprintf(":%s", metricsPort),
		Handler:           httpMux,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      30 * time.Second,
	}

	go func() {
		logger.Info("starting HTTP server for metrics", "address", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server error", "error", err)
			serverErrors <- fmt.Errorf("HTTP server error: %w", err)
		}
	}()

	return httpServer
}

// newHealthHandler returns an HTTP handler that checks overall service health.
func newHealthHandler(checker *serviceobs.HealthChecker, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp, err := checker.Check(r.Context(), &grpc_health_v1.HealthCheckRequest{})
		if err != nil || resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
			w.WriteHeader(http.StatusServiceUnavailable)
			if _, err := w.Write([]byte("NOT_SERVING")); err != nil {
				logger.Warn("failed to write health response",
					"error", err,
					"endpoint", r.URL.Path,
					"remote_addr", r.RemoteAddr)
			}
			return
		}
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("SERVING")); err != nil {
			logger.Warn("failed to write health response",
				"error", err,
				"endpoint", r.URL.Path,
				"remote_addr", r.RemoteAddr)
		}
	}
}

// newReadyHandler returns an HTTP handler that checks database readiness.
func newReadyHandler(checker *serviceobs.HealthChecker, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp, err := checker.Check(r.Context(), &grpc_health_v1.HealthCheckRequest{Service: "database"})
		if err != nil || resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
			w.WriteHeader(http.StatusServiceUnavailable)
			if _, err := w.Write([]byte("NOT_READY")); err != nil {
				logger.Warn("failed to write readiness response",
					"error", err,
					"endpoint", r.URL.Path,
					"remote_addr", r.RemoteAddr)
			}
			return
		}
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("READY")); err != nil {
			logger.Warn("failed to write readiness response",
				"error", err,
				"endpoint", r.URL.Path,
				"remote_addr", r.RemoteAddr)
		}
	}
}

// awaitAndShutdown waits for a shutdown signal or server error, then gracefully stops servers.
func awaitAndShutdown(grpcServer *grpc.Server, httpServer *http.Server, serverErrors chan error, logger *slog.Logger) error {
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

	logger.Info("shutting down server...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), defaults.DefaultRPCTimeout)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP server shutdown error", "error", err)
	} else {
		logger.Info("HTTP server stopped")
	}

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

	return runErr
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
