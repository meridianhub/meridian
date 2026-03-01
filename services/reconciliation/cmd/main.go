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

	"github.com/jackc/pgx/v5/pgxpool"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	reconciliationv1 "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	"github.com/meridianhub/meridian/services/reconciliation/adapters/messaging"
	"github.com/meridianhub/meridian/services/reconciliation/adapters/persistence"
	"github.com/meridianhub/meridian/services/reconciliation/config"
	"github.com/meridianhub/meridian/services/reconciliation/observability"
	"github.com/meridianhub/meridian/services/reconciliation/service"
	"github.com/meridianhub/meridian/services/reconciliation/worker"
	"github.com/meridianhub/meridian/shared/pkg/health"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/kafka"
	"github.com/meridianhub/meridian/shared/platform/redislock"
	"github.com/meridianhub/meridian/shared/platform/scheduler"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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

	// Initialize OpenTelemetry tracer
	tracer, err := bootstrap.NewTracer(ctx, bootstrap.TracerConfig{
		ServiceName:    cfg.Observability.ServiceName,
		ServiceVersion: Version,
		Logger:         logger,
	})
	if err != nil {
		return fmt.Errorf("failed to initialize tracer: %w", err)
	}
	defer bootstrap.ShutdownTracer(tracer, logger)

	// Initialize database connection
	dbConfig := bootstrap.DefaultDatabaseConfig()
	dbConfig.DSN = cfg.Database.URL
	dbConfig.MaxOpenConns = cfg.Database.MaxOpenConns
	dbConfig.MaxIdleConns = cfg.Database.MaxIdleConns
	dbConfig.ConnMaxLifetime = cfg.Database.ConnMaxLifetime
	dbConfig.ConnMaxIdleTime = cfg.Database.ConnMaxIdleTime
	dbConfig.Logger = logger

	db, err := bootstrap.NewDatabase(ctx, dbConfig)
	if err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}
	defer bootstrap.CloseDatabase(db, logger)

	logger.Info("database connection established")

	// Initialize outbox publisher and worker for transactional event publishing
	outboxRepo := events.NewPostgresOutboxRepository(db)
	outboxPublisher := events.NewOutboxPublisher("reconciliation")

	var outboxWorker *events.Worker
	var kafkaProducer *kafka.ProtoProducer
	bootstrapServers := env.GetEnvOrDefault("KAFKA_BOOTSTRAP_SERVERS", "")
	if bootstrapServers == "" && cfg.Kafka.Enabled {
		bootstrapServers = cfg.Kafka.Brokers
	}
	if bootstrapServers != "" {
		producer, kafkaErr := kafka.NewProtoProducer(kafka.ProducerConfig{
			BootstrapServers: bootstrapServers,
			ClientID:         "reconciliation-outbox-worker",
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
			workerConfig := events.DefaultWorkerConfig("reconciliation")
			outboxWorker = events.NewWorker(outboxRepo, kafkaProducer, workerConfig, logger)
			outboxWorker.Start(ctx)
			defer outboxWorker.Stop()
			logger.Info("outbox worker started",
				"bootstrap_servers", bootstrapServers)
		}
	} else {
		logger.Warn("outbox worker disabled - KAFKA_BOOTSTRAP_SERVERS not set")
	}

	// Create outbox-based event publisher (replaces KafkaPublisher/NoopPublisher)
	eventPublisher := messaging.NewOutboxEventPublisher(db, outboxPublisher)

	// Instantiate persistence repositories
	runRepo := persistence.NewSettlementRunRepository(db)
	snapshotRepo := persistence.NewSettlementSnapshotRepository(db)
	varianceRepo := persistence.NewVarianceRepository(db)
	disputeRepo := persistence.NewDisputeRepository(db)
	assertionRepo := persistence.NewBalanceAssertionRepository(db)
	trendRepo := persistence.NewImbalanceTrendRepository(db)

	// Build service options with repositories (always available)
	serviceOpts := []service.Option{
		service.WithSettlementRunRepository(runRepo),
		service.WithDisputeRepository(disputeRepo),
		service.WithDisputeListRepository(disputeRepo),
		service.WithAssertionListRepository(assertionRepo),
		service.WithVarianceRepository(varianceRepo),
		service.WithVarianceListRepository(varianceRepo),
		service.WithEventPublisher(eventPublisher),
		service.WithLogger(logger),
	}

	// Wire SnapshotCapturer if Position Keeping URL is configured
	var pkConn *grpc.ClientConn
	if cfg.Services.PositionKeepingURL != "" {
		var connErr error
		pkConn, connErr = grpc.NewClient(
			cfg.Services.PositionKeepingURL,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if connErr != nil {
			return fmt.Errorf("failed to create position keeping client at %s: %w",
				cfg.Services.PositionKeepingURL, connErr)
		}

		pkClient := positionkeepingv1.NewPositionKeepingServiceClient(pkConn)
		provider := service.NewPKPositionProvider(pkClient)
		capturer := service.NewSnapshotCapturer(provider, runRepo, snapshotRepo)
		serviceOpts = append(serviceOpts,
			service.WithSnapshotCapturer(capturer.CaptureSnapshots),
		)

		logger.Info("snapshot capturer configured",
			"position_keeping_url", cfg.Services.PositionKeepingURL)

		// Wire BalanceAssertor (requires PK client for position summaries)
		balancePKClient := service.NewGrpcPositionKeepingClient(pkClient)
		assertor := service.NewBalanceAssertor(assertionRepo, trendRepo, balancePKClient, nil, nil, logger)
		serviceOpts = append(serviceOpts,
			service.WithBalanceAssertor(assertor),
		)
		logger.Info("balance assertor configured")
	} else {
		logger.Warn("snapshot capturer not configured: POSITION_KEEPING_URL not set")
		logger.Warn("balance assertor not configured: position keeping client unavailable")
	}
	defer func() {
		if pkConn != nil {
			if err := pkConn.Close(); err != nil {
				logger.Error("failed to close position keeping connection", "error", err)
			}
		}
	}()

	// Wire VarianceDetector (depends on repos only, always available)
	detector := service.NewVarianceDetector(runRepo, snapshotRepo, varianceRepo)
	serviceOpts = append(serviceOpts,
		service.WithVarianceDetector(detector.DetectVariances),
	)

	// Wire VarianceValuator with stub engine (temporary until valuation service ready)
	// TODO: Replace stub engine with shared/pkg/valuation concrete Engine when available
	// TODO: Replace stub ref data with gRPC client to Reference Data service
	stubEngine := service.NewStubValuationEngine()
	stubRefData := service.NewStubReferenceDataProvider()
	valuator := service.NewVarianceValuator(stubEngine, stubRefData, varianceRepo, runRepo)
	serviceOpts = append(serviceOpts,
		service.WithVarianceValuator(valuator.ValueVariances),
	)
	logger.Info("variance valuator configured (using stub engine)",
		"note", "identity valuation until shared/pkg/valuation implementation available")

	// Create AccountReconciliationService
	reconciliationSvc := service.NewAccountReconciliationService(serviceOpts...)

	// Initialize Redis client (optional, needed for scheduler leader election)
	var redisClient *redis.Client
	if cfg.Redis.Enabled && cfg.Redis.URL != "" {
		redisCfg := bootstrap.RedisConfig{
			URL:    cfg.Redis.URL,
			Logger: logger,
		}
		var redisErr error
		redisClient, redisErr = bootstrap.NewRedisClient(ctx, redisCfg)
		if redisErr != nil {
			logger.Warn("Redis connection failed, scheduler and Redis health check disabled",
				"error", redisErr)
		} else {
			logger.Info("Redis client connected")
		}
	}
	defer func() {
		if redisClient != nil {
			if err := redisClient.Close(); err != nil {
				logger.Error("failed to close Redis client", "error", err)
			}
		}
	}()

	// Wire settlement scheduler (optional, depends on Redis + config)
	var cronScheduler *scheduler.CronScheduler
	if cfg.Scheduler.Enabled {
		cronScheduler = wireScheduler(ctx, cfg, redisClient, logger)
	} else {
		logger.Info("settlement scheduler disabled")
	}

	// Initialize auth interceptor
	authConfig := bootstrap.DefaultAuthConfig(logger)
	authInterceptor, err := bootstrap.NewAuthInterceptor(ctx, authConfig)
	if err != nil {
		return fmt.Errorf("failed to initialize auth: %w", err)
	}

	// Create gRPC server with interceptor chain
	grpcServer := bootstrap.NewGrpcServerBuilder(tracer, logger).
		WithAuthInterceptor(authInterceptor).
		Build()

	// Register AccountReconciliationService
	reconciliationv1.RegisterAccountReconciliationServiceServer(grpcServer, reconciliationSvc)

	// Register health check service with available checkers
	healthCheckers := []health.Checker{
		observability.NewDatabaseChecker(db),
	}
	if redisClient != nil {
		healthCheckers = append(healthCheckers, observability.NewRedisChecker(redisClient))
	}
	healthAggregator := health.NewAggregator(healthCheckers)
	healthServer := newHealthServer(healthAggregator, logger)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)

	// Register reflection service for debugging
	reflection.Register(grpcServer)

	logger.Info("gRPC services registered",
		"services", []string{"AccountReconciliationService", "Health", "Reflection"})

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
	if cronScheduler != nil {
		go func() {
			if err := cronScheduler.Start(schedulerCtx); err != nil {
				logger.Error("scheduler stopped with error", "error", err)
			}
		}()
	}

	// Start HTTP server for metrics and health endpoints
	httpMux := http.NewServeMux()
	httpMux.Handle("/metrics", promhttp.Handler())

	healthHandler := health.NewHTTPHandler(healthAggregator)
	healthHandler.RegisterHandlers(httpMux)

	httpServer := &http.Server{
		Addr:              fmt.Sprintf(":%s", cfg.Observability.MetricsPort),
		Handler:           httpMux,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("HTTP server shutdown error", "error", err)
		} else {
			logger.Info("HTTP server stopped")
		}
	}()

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

	// Graceful shutdown (runs for both signal and server error paths)
	logger.Info("shutting down servers...")

	// Stop scheduler first (it makes gRPC calls to self)
	if cronScheduler != nil {
		logger.Info("stopping settlement scheduler...")
		schedulerCancel()
		cronScheduler.Stop()
		logger.Info("settlement scheduler stopped")
	}

	// Outbox worker and Kafka producer are cleaned up via defer.

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.GracefulShutdownTimeout)
	defer cancel()

	// Gracefully stop gRPC server
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

// wireScheduler creates and configures the CronScheduler with all its dependencies.
// Returns nil if any required dependency is not available.
func wireScheduler(ctx context.Context, cfg *config.Config, redisClient *redis.Client, logger *slog.Logger) *scheduler.CronScheduler {
	if redisClient == nil {
		logger.Warn("scheduler disabled: Redis not configured (required for distributed locking)")
		return nil
	}

	// Create distributed lock (replaces custom RedisLeaderElector)
	distLock := redislock.NewLock(redisClient, redislock.Config{
		KeyPrefix:  "meridian:reconciliation:scheduler",
		LockTTL:    cfg.Scheduler.LeaderLockTTL,
		RenewEvery: cfg.Scheduler.LeaderRenewInterval,
	}, logger)

	// Create execution store (requires pgxpool for direct pgx queries)
	pool, err := pgxpool.New(ctx, cfg.Database.URL)
	if err != nil {
		logger.Warn("scheduler disabled: failed to create pgx pool for execution store",
			"error", err)
		return nil
	}
	executionStore, err := scheduler.NewPgExecutionStore(pool) //nolint:contextcheck // NewPgExecutionStore creates its own context for schema validation
	if err != nil {
		logger.Warn("scheduler disabled: execution store validation failed",
			"error", err)
		pool.Close()
		return nil
	}

	// Create Reference Data client (stub until proto available)
	refDataClient := worker.NewStubReferenceDataClient(logger)

	// Create reconciliation self-client (loopback to this service)
	reconConn, err := grpc.NewClient(
		fmt.Sprintf("localhost:%s", cfg.Server.Port),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		logger.Warn("scheduler disabled: failed to create reconciliation self-client",
			"error", err)
		pool.Close()
		return nil
	}
	reconGrpcClient := reconciliationv1.NewAccountReconciliationServiceClient(reconConn)
	reconClient := worker.NewGrpcReconciliationClient(reconGrpcClient)

	// Create adapter types
	provider := worker.NewSettlementScheduleProvider(refDataClient)
	executor := worker.NewSettlementExecutor(reconClient, nil, logger)

	// Create shared cron scheduler
	cronScheduler := scheduler.NewCronScheduler(
		provider,
		executor,
		distLock,
		scheduler.CronSchedulerConfig{
			Name:             "reconciliation",
			RefreshInterval:  cfg.Scheduler.PollInterval,
			ShutdownTimeout:  cfg.Scheduler.ShutdownTimeout,
			ExecutionTimeout: 10 * time.Minute,
			MaxCatchUpAge:    24 * time.Hour,
		},
		logger,
		scheduler.WithCronExecutionStore(executionStore),
	)

	logger.Info("settlement scheduler configured",
		"poll_interval", cfg.Scheduler.PollInterval,
		"leader_lock_ttl", cfg.Scheduler.LeaderLockTTL,
		"shutdown_timeout", cfg.Scheduler.ShutdownTimeout)

	return cronScheduler
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
