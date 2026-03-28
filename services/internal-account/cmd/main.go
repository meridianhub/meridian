// Package main is the entry point for the InternalAccount service.
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

	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	"github.com/meridianhub/meridian/services/internal-account/app"
	"github.com/meridianhub/meridian/services/internal-account/service"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/ports"
	"github.com/prometheus/client_golang/prometheus/promhttp"
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
	logLevel := parseLogLevel(os.Getenv("LOG_LEVEL"))
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))
	slog.SetDefault(logger)

	logger.Info("starting internal-account service",
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

	// Initialize dependency container
	container, err := app.NewContainer(ctx, logger, Version)
	if err != nil {
		return err
	}
	defer container.Close()

	// Create gRPC server with interceptor chain
	// Order is handled by bootstrap: tracing -> auth -> recovery
	grpcServer, err := bootstrap.NewGrpcServerBuilder(container.Tracer, logger).
		WithAuthInterceptor(container.AuthInterceptor).
		Build()
	if err != nil {
		return fmt.Errorf("failed to build grpc server: %w", err)
	}

	// Register services
	pb.RegisterInternalAccountServiceServer(grpcServer, container.Service)

	// Register health check service with database dependency checking
	healthChecker, err := service.NewHealthChecker(service.HealthCheckerConfig{
		Repository:   container.Repo,
		Logger:       logger,
		ServiceName:  "internal-account",
		CheckTimeout: defaults.DefaultHealthCheckTimeout,
	})
	if err != nil {
		return fmt.Errorf("failed to create health checker: %w", err)
	}
	grpc_health_v1.RegisterHealthServer(grpcServer, healthChecker)

	// Register reflection service for debugging
	reflection.Register(grpcServer)

	logger.Info("gRPC services registered")

	// Get ports from environment
	port := env.GetEnvOrDefault("GRPC_PORT", strconv.Itoa(ports.InternalAccount))
	address := fmt.Sprintf(":%s", port)
	metricsPort := env.GetEnvOrDefault("METRICS_PORT", "8082")

	// Create listener
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", address)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", address, err)
	}

	// Start gRPC server in background
	// Buffer size must match number of goroutines writing to this channel
	// to prevent deadlock on simultaneous failures (gRPC + HTTP = 2)
	serverErrors := make(chan error, 2)
	go func() {
		logger.Info("starting gRPC server", "address", address)
		if err := grpcServer.Serve(listener); err != nil {
			serverErrors <- err
		}
	}()

	// Start HTTP server for metrics and health endpoints
	httpMux := http.NewServeMux()
	httpMux.Handle("/metrics", promhttp.Handler())
	httpMux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		// Simple health endpoint for HTTP probes
		resp, err := healthChecker.Check(r.Context(), &grpc_health_v1.HealthCheckRequest{})
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
	})
	httpMux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		// Readiness endpoint - checks database connectivity
		resp, err := healthChecker.Check(r.Context(), &grpc_health_v1.HealthCheckRequest{Service: "database"})
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
	})

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

	// Wait for shutdown signal and orchestrate graceful shutdown
	orchestrator := bootstrap.NewShutdownOrchestrator(grpcServer, logger)

	// Register cleanup functions (LIFO order - HTTP server first, database etc handled by container.Close())
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
