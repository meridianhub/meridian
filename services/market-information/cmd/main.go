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

	"github.com/meridianhub/meridian/services/market-information/adapters/external/ecb"
	"github.com/meridianhub/meridian/services/market-information/config"
	"github.com/meridianhub/meridian/services/market-information/service"
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

	logger.Info("starting market-information service",
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

	// Load service configuration
	cfg := config.LoadConfig()

	// Initialize OpenTelemetry tracer
	tracer, err := bootstrap.NewTracer(ctx, bootstrap.TracerConfig{
		ServiceName:    "market-information-service",
		ServiceVersion: Version,
		Logger:         logger,
	})
	if err != nil {
		return fmt.Errorf("failed to initialize tracer: %w", err)
	}
	defer bootstrap.ShutdownTracer(tracer, logger)

	// TODO: Wire database into repository once persistence layer is implemented (Task 3)
	// Currently scaffolding without database to avoid holding idle connections.
	// Uncomment when ready:
	// dbConfig := bootstrap.DefaultDatabaseConfig()
	// dbConfig.Logger = logger
	// db, err := bootstrap.NewDatabase(ctx, dbConfig)
	// if err != nil {
	// 	return fmt.Errorf("failed to initialize database: %w", err)
	// }
	// logger.Info("database connection established")

	// Initialize auth interceptor (optional - based on AUTH_ENABLED)
	authConfig := bootstrap.DefaultAuthConfig(logger)
	authInterceptor, err := bootstrap.NewAuthInterceptor(ctx, authConfig)
	if err != nil {
		return fmt.Errorf("failed to initialize auth: %w", err)
	}

	// Create gRPC server with interceptor chain
	grpcServer := bootstrap.NewGrpcServerBuilder(tracer, logger).
		WithAuthInterceptor(authInterceptor).
		Build()

	// TODO: Register Market Information service when proto definitions are available
	// pb.RegisterMarketInformationServiceServer(grpcServer, marketInformationService)

	// Register health check service
	healthChecker := service.NewHealthChecker(service.HealthCheckerConfig{
		Logger:        logger,
		ServiceName:   "market-information",
		CheckTimeout:  defaults.DefaultHealthCheckTimeout,
		ServiceConfig: cfg,
	})
	grpc_health_v1.RegisterHealthServer(grpcServer, healthChecker)

	// Register reflection service for debugging
	reflection.Register(grpcServer)

	logger.Info("gRPC services registered")

	// Get ports from environment
	port := env.GetEnvOrDefault("GRPC_PORT", strconv.Itoa(ports.MarketInformation))
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
		// Readiness endpoint - checks all dependencies
		resp, err := healthChecker.Check(r.Context(), &grpc_health_v1.HealthCheckRequest{})
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

	// Initialize ECB adapter worker (if enabled)
	// Note: This requires the Market Information service to be fully wired up.
	// The ECB worker calls RecordObservation on the service to ingest FX rates.
	if cfg.ECB.Enabled {
		// TODO: Enable ECB worker once marketInformationServer is available
		// The ECB worker needs a MarketInformationClient interface which the Server implements.
		// Once the Server is created above, uncomment and wire up:
		//
		// ecbClient := ecb.NewClient(ecb.Config{
		// 	Endpoint: cfg.ECB.Endpoint,
		// 	Timeout:  cfg.ECB.Timeout,
		// }, ecb.WithLogger(logger))
		//
		// ecbWorker := ecb.NewWorker(
		// 	ecbClient,
		// 	marketInformationServer, // Server implements MarketInformationClient interface
		// 	ecb.WorkerConfig{
		// 		DatasetCode:   cfg.ECB.DatasetCode,
		// 		SourceCode:    cfg.ECB.SourceCode,
		// 		FetchInterval: cfg.ECB.Interval,
		// 		MaxRetries:    cfg.ECB.MaxRetries,
		// 	},
		// 	logger,
		// )
		//
		// ecbWorker.Start(ctx)
		//
		// // Register cleanup to stop ECB worker before other services
		// orchestrator.AddCleanup(func() error {
		// 	ecbWorker.Stop()
		// 	return nil
		// })
		//
		// logger.Info("ECB adapter initialized",
		// 	"interval", cfg.ECB.Interval,
		// 	"source_code", cfg.ECB.SourceCode,
		// 	"dataset_code", cfg.ECB.DatasetCode)

		logger.Warn("ECB adapter enabled but Market Information service not yet wired up",
			"ecb_enabled", cfg.ECB.Enabled)

		// Suppress unused import warning - remove this block when ECB worker is wired up
		_ = ecb.NewClient
	}

	// Register cleanup functions (LIFO order - HTTP server first, then database)
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

	// TODO: Re-enable database cleanup when persistence layer is implemented
	// orchestrator.AddCleanup(func() error {
	// 	bootstrap.CloseDatabase(db, logger)
	// 	return nil
	// })

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
