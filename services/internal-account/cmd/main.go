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
	"github.com/meridianhub/meridian/services/internal-account/adapters/persistence"
	ibaobservability "github.com/meridianhub/meridian/services/internal-account/observability"
	"github.com/meridianhub/meridian/services/internal-account/service"
	poskeepingclient "github.com/meridianhub/meridian/services/position-keeping/client"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/meridianhub/meridian/shared/platform/ports"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sony/gobreaker/v2"
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

	// Initialize OpenTelemetry tracer
	tracer, err := bootstrap.NewTracer(ctx, bootstrap.TracerConfig{
		ServiceName:    "internal-account-service",
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

	// Get Kubernetes namespace from environment (defaults to "default")
	namespace := env.GetEnvOrDefault("K8S_NAMESPACE", "default")

	logger.Info("external service configuration",
		"position_keeping", fmt.Sprintf("position-keeping.%s.svc.cluster.local:%d", namespace, ports.PositionKeeping),
		"load_balancing", "DNS-based round_robin")

	// Create service with external clients and capture the clients for health checking
	internalAccountService, svcClients, err := createServiceWithClients(
		repo,
		namespace,
		logger,
		tracer,
	)
	if err != nil {
		return fmt.Errorf("failed to create service: %w", err)
	}

	logger.Info("service initialized with external clients")

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
	pb.RegisterInternalAccountServiceServer(grpcServer, internalAccountService)

	// Register health check service with dependency checking
	healthChecker, err := service.NewHealthChecker(service.HealthCheckerConfig{
		Repository:                  repo,
		PositionKeepingClient:       svcClients.positionKeeping,
		PositionKeepingHealthClient: svcClients.positionKeepingHealth,
		Logger:                      logger,
		ServiceName:                 "internal-account",
		CheckTimeout:                defaults.DefaultHealthCheckTimeout,
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

	// Register cleanup functions (LIFO order - HTTP server first, then external clients, then database)
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

	for _, cleanup := range svcClients.cleanupFuncs {
		fn := cleanup // capture for closure
		orchestrator.AddCleanup(func() error {
			fn()
			return nil
		})
	}

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

// serviceClients holds the clients created by createServiceWithClients.
type serviceClients struct {
	positionKeeping service.PositionKeepingClient
	// Health client bypasses the circuit breaker for health checks
	positionKeepingHealth grpc_health_v1.HealthClient
	// Cleanup functions for graceful shutdown
	cleanupFuncs []func()
}

// createServiceWithClients creates the service and returns it along with the external clients
// for use in health checking. This approach creates the clients once and shares them between
// the service and health checker to avoid duplicate connections.
//
// Uses the service-owned client package with built-in resilience patterns:
//   - services/position-keeping/client
//
// The client is configured with DNS-based client-side load balancing and optional
// circuit breaker + retry resilience.
func createServiceWithClients(
	repo *persistence.Repository,
	namespace string,
	logger *slog.Logger,
	tracer *observability.Tracer,
) (*service.Service, *serviceClients, error) {
	// Track cleanup functions for graceful shutdown
	cleanupFuncs := make([]func(), 0, 1)

	// Create Position Keeping client using service-owned client package
	// The client has built-in resilience patterns (circuit breaker + retry)
	posKeepingClient, posKeepingCleanup, err := poskeepingclient.New(poskeepingclient.Config{
		ServiceName: poskeepingclient.ServiceName,
		Namespace:   namespace,
		Port:        ports.PositionKeeping,
		Timeout:     defaults.DefaultRPCTimeout,
		Tracer:      tracer,
		Resilience: &sharedclients.ResilientClientConfig{
			Logger:             logger,
			CircuitBreakerName: "position-keeping",
			OnStateChange:      makeCircuitBreakerCallback(),
		},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create position keeping client: %w", err)
	}
	cleanupFuncs = append(cleanupFuncs, posKeepingCleanup)

	// Create service with the pre-created client
	// The position-keeping client implements service.PositionKeepingClient interface
	svc, err := service.NewServiceWithClients(
		repo,
		posKeepingClient, // *poskeepingclient.Client implements service.PositionKeepingClient
		nil,              // referenceDataClient - not wired yet (future task)
		logger,
		tracer,
	)
	if err != nil {
		// Cleanup all clients before returning
		for _, cleanup := range cleanupFuncs {
			cleanup()
		}
		return nil, nil, fmt.Errorf("failed to create service with existing clients: %w", err)
	}

	// Create health client from the underlying gRPC connection
	// This bypasses the circuit breaker to avoid health checks tripping business operation circuit breakers
	svcClients := &serviceClients{
		positionKeeping:       posKeepingClient,
		positionKeepingHealth: grpc_health_v1.NewHealthClient(posKeepingClient.Conn()),
		cleanupFuncs:          cleanupFuncs,
	}

	return svc, svcClients, nil
}

// makeCircuitBreakerCallback creates a circuit breaker state change callback
// that records metrics for the given service name.
func makeCircuitBreakerCallback() func(string, gobreaker.State, gobreaker.State) {
	return func(name string, from gobreaker.State, to gobreaker.State) {
		ibaobservability.RecordCircuitBreakerState(name, gobreakerStateToMetricState(to))
		ibaobservability.RecordCircuitBreakerStateChange(name, from.String(), to.String())
	}
}

// gobreakerStateToMetricState converts a gobreaker.State to the observability CircuitBreakerState
// for Prometheus metrics recording.
func gobreakerStateToMetricState(state gobreaker.State) ibaobservability.CircuitBreakerState {
	switch state {
	case gobreaker.StateClosed:
		return ibaobservability.CircuitBreakerStateClosed
	case gobreaker.StateHalfOpen:
		return ibaobservability.CircuitBreakerStateHalfOpen
	case gobreaker.StateOpen:
		return ibaobservability.CircuitBreakerStateOpen
	default:
		return ibaobservability.CircuitBreakerStateClosed
	}
}
