// Package main is the entry point for the Reference Data service.
// Manages instrument definitions, reference data nodes, and saga definitions.
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
	mappingv1 "github.com/meridianhub/meridian/api/proto/meridian/mapping/v1"
	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	sagav1 "github.com/meridianhub/meridian/api/proto/meridian/saga/v1"
	"github.com/meridianhub/meridian/services/reference-data/accounttype"
	refcel "github.com/meridianhub/meridian/services/reference-data/cel"
	"github.com/meridianhub/meridian/services/reference-data/handler"
	"github.com/meridianhub/meridian/services/reference-data/mapping"
	"github.com/meridianhub/meridian/services/reference-data/node"
	"github.com/meridianhub/meridian/services/reference-data/registry"
	"github.com/meridianhub/meridian/services/reference-data/saga"
	sharedcel "github.com/meridianhub/meridian/shared/pkg/cel"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/observability"
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
		ServiceName:    "reference-data-service",
		ServiceVersion: Version,
		Logger:         logger,
	})
	if err != nil {
		return fmt.Errorf("failed to initialize tracer: %w", err)
	}
	defer bootstrap.ShutdownTracer(tracer, logger)

	// Initialize database connection
	dbPool, err := initDatabase(ctx, logger)
	if err != nil {
		return err
	}
	defer dbPool.Close()

	// Create service handlers
	svcs, err := initServices(dbPool, logger)
	if err != nil {
		return err
	}

	// Create gRPC server, register services, and start listening
	grpcServer, listener, err := setupGRPCServer(ctx, tracer, logger, svcs)
	if err != nil {
		return err
	}

	serverErrors := make(chan error, 2)
	go func() {
		logger.Info("starting gRPC server", "address", listener.Addr().String())
		if err := grpcServer.Serve(listener); err != nil {
			serverErrors <- err
		}
	}()

	// Start HTTP server for metrics and health
	metricsPort := env.GetEnvOrDefault("METRICS_PORT", "8082")
	httpServer := startHTTPServer(metricsPort, logger, serverErrors)

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

// refDataServices bundles all gRPC service handlers for reference-data.
type refDataServices struct {
	refData     referencedatav1.ReferenceDataServiceServer
	node        referencedatav1.NodeServiceServer
	accountType referencedatav1.AccountTypeRegistryServiceServer
	saga        sagav1.SagaRegistryServiceServer
	mapping     mappingv1.MappingServiceServer
}

// initDatabase creates and validates a pgxpool connection.
func initDatabase(ctx context.Context, logger *slog.Logger) (*pgxpool.Pool, error) {
	dbURL := env.GetEnvOrDefault("DATABASE_URL",
		"postgres://meridian_reference_data_user@localhost:26257/meridian_reference_data?sslmode=disable")
	dbPool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return nil, fmt.Errorf("failed to create database connection pool: %w", err)
	}
	if err := dbPool.Ping(ctx); err != nil {
		dbPool.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}
	logger.Info("database connection established", "url", dbURL)
	return dbPool, nil
}

// initServices creates all repositories and gRPC service handlers.
func initServices(dbPool *pgxpool.Pool, logger *slog.Logger) (*refDataServices, error) {
	instrumentRegistry, err := registry.NewPostgresRegistry(dbPool)
	if err != nil {
		return nil, fmt.Errorf("failed to create instrument registry: %w", err)
	}
	logger.Info("instrument registry initialized")

	compiler, err := refcel.NewCompiler()
	if err != nil {
		return nil, fmt.Errorf("failed to create CEL compiler: %w", err)
	}
	logger.Info("CEL compiler initialized")

	nodeRepo := node.NewPostgresRepository(dbPool)
	logger.Info("node repository initialized")

	sagaRegistry := saga.NewPostgresRegistry(dbPool, nil)
	logger.Info("saga registry initialized")

	accountTypeRegistry, err := accounttype.NewPostgresRegistry(dbPool)
	if err != nil {
		return nil, fmt.Errorf("failed to create account type registry: %w", err)
	}
	logger.Info("account type registry initialized")

	mappingRepo := mapping.NewPostgresRepository(dbPool)
	mappingCELCompiler, err := sharedcel.NewCompiler()
	if err != nil {
		return nil, fmt.Errorf("failed to create mapping CEL compiler: %w", err)
	}
	mappingValidator, err := mapping.NewValidator(mappingCELCompiler)
	if err != nil {
		return nil, fmt.Errorf("failed to create mapping validator: %w", err)
	}
	logger.Info("mapping repository and validator initialized")

	refDataSvc, err := handler.NewService(instrumentRegistry, compiler, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create reference data service: %w", err)
	}
	nodeSvc, err := handler.NewNodeService(nodeRepo, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create node service: %w", err)
	}
	sagaSvc := saga.NewRegistryHandler(sagaRegistry, nil, nil, logger)
	accountTypeSvc, err := handler.NewAccountTypeService(accountTypeRegistry, instrumentRegistry, compiler, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create account type service: %w", err)
	}
	mappingSvc, err := handler.NewMappingService(mappingRepo, mappingValidator, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create mapping service: %w", err)
	}

	logger.Info("gRPC service handlers initialized")

	return &refDataServices{
		refData:     refDataSvc,
		node:        nodeSvc,
		accountType: accountTypeSvc,
		saga:        sagaSvc,
		mapping:     mappingSvc,
	}, nil
}

// setupGRPCServer creates the gRPC server, registers all services, and creates the TCP listener.
func setupGRPCServer(ctx context.Context, tracer *observability.Tracer, logger *slog.Logger, svcs *refDataServices) (*grpc.Server, net.Listener, error) {
	authConfig := bootstrap.DefaultAuthConfig(logger)
	authInterceptor, err := bootstrap.NewAuthInterceptor(ctx, authConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize auth: %w", err)
	}

	grpcServer, err := bootstrap.NewGrpcServerBuilder(tracer, logger).
		WithAuthInterceptor(authInterceptor).
		Build()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build grpc server: %w", err)
	}

	referencedatav1.RegisterReferenceDataServiceServer(grpcServer, svcs.refData)
	referencedatav1.RegisterNodeServiceServer(grpcServer, svcs.node)
	referencedatav1.RegisterAccountTypeRegistryServiceServer(grpcServer, svcs.accountType)
	sagav1.RegisterSagaRegistryServiceServer(grpcServer, svcs.saga)
	mappingv1.RegisterMappingServiceServer(grpcServer, svcs.mapping)

	healthServer := health.NewServer()
	healthServer.SetServingStatus("reference-data", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	reflection.Register(grpcServer)

	logger.Info("gRPC services registered")

	port := env.GetEnvOrDefault("GRPC_PORT", strconv.Itoa(ports.ReferenceData))
	address := fmt.Sprintf(":%s", port)
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", address)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to listen on %s: %w", address, err)
	}

	return grpcServer, listener, nil
}

// startHTTPServer creates and starts the HTTP server for metrics and health endpoints.
func startHTTPServer(metricsPort string, logger *slog.Logger, serverErrors chan error) *http.Server {
	httpMux := http.NewServeMux()
	httpMux.Handle("/metrics", promhttp.Handler())
	httpMux.HandleFunc("/health", healthHandler(nil))
	httpMux.HandleFunc("/ready", healthHandler(nil))

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

	return httpServer
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
