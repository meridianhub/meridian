// Package main is the entry point for the Market Information service.
// BIAN Service Domain: Market Information Management
// Manages price benchmarks, market data feeds, and reference prices.
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

	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	"github.com/meridianhub/meridian/services/market-information/adapters/external/ecb"
	"github.com/meridianhub/meridian/services/market-information/app"
	"github.com/meridianhub/meridian/services/market-information/service"
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
	logLevel := parseLogLevel(os.Getenv("LOG_LEVEL"))
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))
	slog.SetDefault(logger)

	logger.Info("starting market-information service",
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

	// Create and register gRPC services
	grpcServer, healthChecker, err := buildGRPCServer(container, logger)
	if err != nil {
		return err
	}

	// Start gRPC listener
	port := env.GetEnvOrDefault("GRPC_PORT", strconv.Itoa(ports.MarketInformation))
	address := fmt.Sprintf(":%s", port)
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", address)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", address, err)
	}

	// Start gRPC server in background
	serverErrors := make(chan error, 2)
	go func() {
		logger.Info("starting gRPC server", "address", address)
		if err := grpcServer.Serve(listener); err != nil {
			serverErrors <- err
		}
	}()

	// Start HTTP server for metrics and health
	metricsPort := env.GetEnvOrDefault("METRICS_PORT", "8082")
	httpServer := buildHTTPServer(healthChecker, metricsPort, logger)

	go func() {
		logger.Info("starting HTTP server for metrics", "address", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server error", "error", err)
			serverErrors <- fmt.Errorf("HTTP server error: %w", err)
		}
	}()

	// Orchestrate shutdown
	orchestrator := bootstrap.NewShutdownOrchestrator(grpcServer, logger)
	initECBWorker(ctx, container, orchestrator, logger)
	registerHTTPShutdown(orchestrator, httpServer, logger)

	return orchestrator.Wait(serverErrors)
}

// buildGRPCServer creates the gRPC server and registers all services (market information, health, reflection).
func buildGRPCServer(container *app.Container, logger *slog.Logger) (*grpc.Server, *service.HealthChecker, error) {
	grpcServer, err := bootstrap.NewGrpcServerBuilder(container.Tracer, logger).
		WithAuthInterceptor(container.AuthInterceptor).
		Build()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build grpc server: %w", err)
	}

	marketinformationv1.RegisterMarketInformationServiceServer(grpcServer, container.MarketInformationServer)

	healthChecker := service.NewHealthChecker(service.HealthCheckerConfig{
		Logger:        logger,
		ServiceName:   "market-information",
		CheckTimeout:  defaults.DefaultHealthCheckTimeout,
		ServiceConfig: container.Config,
	})
	grpc_health_v1.RegisterHealthServer(grpcServer, healthChecker)

	reflection.Register(grpcServer)

	logger.Info("gRPC services registered")
	return grpcServer, healthChecker, nil
}

// buildHTTPServer creates the HTTP server with metrics, health, and readiness endpoints.
func buildHTTPServer(healthChecker *service.HealthChecker, metricsPort string, logger *slog.Logger) *http.Server {
	httpMux := http.NewServeMux()
	httpMux.Handle("/metrics", promhttp.Handler())
	httpMux.HandleFunc("/health", buildHealthHandler(healthChecker, logger, "SERVING", "NOT_SERVING"))
	httpMux.HandleFunc("/ready", buildHealthHandler(healthChecker, logger, "READY", "NOT_READY"))

	return &http.Server{
		Addr:              fmt.Sprintf(":%s", metricsPort),
		Handler:           httpMux,
		ReadHeaderTimeout: defaults.DefaultHTTPReadHeaderTimeout,
		WriteTimeout:      defaults.DefaultHTTPWriteTimeout,
	}
}

// buildHealthHandler returns an HTTP handler that checks gRPC health and writes the appropriate response.
func buildHealthHandler(healthChecker *service.HealthChecker, logger *slog.Logger, servingMsg, notServingMsg string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp, err := healthChecker.Check(r.Context(), &grpc_health_v1.HealthCheckRequest{})
		if err != nil || resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
			w.WriteHeader(http.StatusServiceUnavailable)
			if _, writeErr := w.Write([]byte(notServingMsg)); writeErr != nil {
				logger.Warn("failed to write health response",
					"error", writeErr,
					"endpoint", r.URL.Path,
					"remote_addr", r.RemoteAddr)
			}
			return
		}
		w.WriteHeader(http.StatusOK)
		if _, writeErr := w.Write([]byte(servingMsg)); writeErr != nil {
			logger.Warn("failed to write health response",
				"error", writeErr,
				"endpoint", r.URL.Path,
				"remote_addr", r.RemoteAddr)
		}
	}
}

// initECBWorker initializes and starts the ECB ingestion worker if enabled in config.
func initECBWorker(ctx context.Context, container *app.Container, orchestrator *bootstrap.ShutdownOrchestrator, logger *slog.Logger) {
	cfg := container.Config
	if !cfg.ECB.Enabled {
		return
	}

	ecbClient := ecb.NewClient(ecb.Config{
		Endpoint: cfg.ECB.Endpoint,
		Timeout:  cfg.ECB.Timeout,
	}, ecb.WithLogger(logger))

	ecbWorker := ecb.NewWorker(
		ecbClient,
		container.MarketInformationServer,
		ecb.WorkerConfig{
			DatasetCode:   cfg.ECB.DatasetCode,
			SourceCode:    cfg.ECB.SourceCode,
			FetchInterval: cfg.ECB.Interval,
			MaxRetries:    cfg.ECB.MaxRetries,
		},
		logger,
	)

	ecbWorker.Start(ctx)

	orchestrator.AddCleanup(func() error {
		ecbWorker.Stop()
		return nil
	})

	logger.Info("ECB adapter initialized",
		"interval", cfg.ECB.Interval,
		"source_code", cfg.ECB.SourceCode,
		"dataset_code", cfg.ECB.DatasetCode)
}

// registerHTTPShutdown registers the HTTP server shutdown as a cleanup function.
func registerHTTPShutdown(orchestrator *bootstrap.ShutdownOrchestrator, httpServer *http.Server, logger *slog.Logger) {
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
