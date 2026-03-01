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

	"github.com/jackc/pgx/v5/pgxpool"
	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	"github.com/meridianhub/meridian/services/market-information/adapters/external/ecb"
	"github.com/meridianhub/meridian/services/market-information/adapters/persistence"
	"github.com/meridianhub/meridian/services/market-information/config"
	"github.com/meridianhub/meridian/services/market-information/service"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/kafka"
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

	// Initialize database connection using pgxpool
	dbURL := env.GetEnvOrDefault("DATABASE_URL", "postgres://meridian_user@localhost:26257/meridian?sslmode=disable")
	dbPool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("failed to create database connection pool: %w", err)
	}
	defer dbPool.Close()

	// Verify database connectivity
	if err := dbPool.Ping(ctx); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}
	logger.Info("database connection established", "url", dbURL)

	// Initialize GORM database connection for outbox pattern.
	// The existing pgxpool connection is retained for domain persistence operations.
	gormDBConfig := bootstrap.DefaultDatabaseConfig()
	gormDBConfig.Logger = logger
	gormDB, err := bootstrap.NewDatabase(ctx, gormDBConfig)
	if err != nil {
		return fmt.Errorf("failed to initialize GORM database for outbox: %w", err)
	}
	defer bootstrap.CloseDatabase(gormDB, logger)

	// Initialize outbox publisher and worker for transactional event publishing
	outboxRepo := events.NewPostgresOutboxRepository(gormDB)
	outboxPublisher := events.NewOutboxPublisher("market-information")

	var outboxWorker *events.Worker
	var kafkaProducer *kafka.ProtoProducer
	bootstrapServers := env.GetEnvOrDefault("KAFKA_BOOTSTRAP_SERVERS", "")
	if bootstrapServers != "" {
		producer, kafkaErr := kafka.NewProtoProducer(kafka.ProducerConfig{
			BootstrapServers: bootstrapServers,
			ClientID:         "market-information-outbox-worker",
			Acks:             "all",
			Retries:          3,
			Compression:      "snappy",
		})
		if kafkaErr != nil {
			logger.Warn("failed to create Kafka producer for outbox worker",
				"error", kafkaErr)
		} else {
			kafkaProducer = producer
			defer kafkaProducer.Close()
			workerConfig := events.DefaultWorkerConfig("market-information")
			outboxWorker = events.NewWorker(outboxRepo, kafkaProducer, workerConfig, logger)
			outboxWorker.Start(ctx)
			defer outboxWorker.Stop()
			logger.Info("outbox worker started",
				"bootstrap_servers", bootstrapServers)
		}
	} else {
		logger.Warn("outbox worker disabled - KAFKA_BOOTSTRAP_SERVERS not set")
	}

	// Create outbox-based event publisher (replaces KafkaObservationPublisher)
	outboxEventPublisher := service.NewOutboxEventPublisher(gormDB, outboxPublisher)

	// Create repositories for persistence
	masterTenantID := env.GetEnvOrDefault("MASTER_TENANT_ID", "meridian_master")
	repos := persistence.NewRepositories(dbPool, masterTenantID)
	logger.Info("repositories initialized", "master_tenant_id", masterTenantID)

	// Create Market Information service server
	marketInformationServer, err := service.NewServer(
		repos.DataSet,
		repos.Observation,
		repos.Source,
		service.WithEventPublisher(outboxEventPublisher),
		service.WithLogger(logger.With("component", "market_information_server")),
	)
	if err != nil {
		return fmt.Errorf("failed to create market information server: %w", err)
	}
	logger.Info("market information server created")

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

	// Register Market Information service
	marketinformationv1.RegisterMarketInformationServiceServer(grpcServer, marketInformationServer)

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

	// Outbox worker and Kafka producer are cleaned up via defer.

	// Initialize ECB adapter worker (if enabled)
	// Note: The ECB worker calls RecordObservation on the Server to ingest FX rates.
	if cfg.ECB.Enabled {
		ecbClient := ecb.NewClient(ecb.Config{
			Endpoint: cfg.ECB.Endpoint,
			Timeout:  cfg.ECB.Timeout,
		}, ecb.WithLogger(logger))

		ecbWorker := ecb.NewWorker(
			ecbClient,
			marketInformationServer, // Server implements MarketInformationClient interface
			ecb.WorkerConfig{
				DatasetCode:   cfg.ECB.DatasetCode,
				SourceCode:    cfg.ECB.SourceCode,
				FetchInterval: cfg.ECB.Interval,
				MaxRetries:    cfg.ECB.MaxRetries,
			},
			logger,
		)

		ecbWorker.Start(ctx)

		// Register cleanup to stop ECB worker before other services
		orchestrator.AddCleanup(func() error {
			ecbWorker.Stop()
			return nil
		})

		logger.Info("ECB adapter initialized",
			"interval", cfg.ECB.Interval,
			"source_code", cfg.ECB.SourceCode,
			"dataset_code", cfg.ECB.DatasetCode)
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

	// Note: Database pool cleanup is handled via defer dbPool.Close() above
	// This ensures the pool is closed even if orchestrator.Wait returns early

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
