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
	"github.com/meridianhub/meridian/services/reconciliation/adapters/persistence"
	"github.com/meridianhub/meridian/services/reconciliation/config"
	"github.com/meridianhub/meridian/services/reconciliation/observability"
	"github.com/meridianhub/meridian/services/reconciliation/service"
	"github.com/meridianhub/meridian/services/reconciliation/worker"
	"github.com/meridianhub/meridian/shared/pkg/health"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
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

	if err := run(logger); err != nil {
		logger.Error("service failed", "error", err)
		os.Exit(1)
	}

	logger.Info("service stopped gracefully")
}

func run(logger *slog.Logger) error {
	ctx := context.Background()

	// Load configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
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

	// Instantiate persistence repositories
	runRepo := persistence.NewSettlementRunRepository(db)
	snapshotRepo := persistence.NewSettlementSnapshotRepository(db)
	varianceRepo := persistence.NewVarianceRepository(db)
	disputeRepo := persistence.NewDisputeRepository(db)

	// Build service options with repositories (always available)
	serviceOpts := []service.Option{
		service.WithSettlementRunRepository(runRepo),
		service.WithDisputeRepository(disputeRepo),
		service.WithVarianceRepository(varianceRepo),
		service.WithVarianceListRepository(varianceRepo),
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
	} else {
		logger.Warn("snapshot capturer not configured: POSITION_KEEPING_URL not set")
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

	// VarianceValuator requires valuation engine + reference data provider (not yet available)
	// BalanceAssertor requires assertion repo + PK client (not yet available)
	// Both will return UNIMPLEMENTED until their dependencies are wired in future tasks
	logger.Warn("variance valuator not configured: valuation engine not yet available")
	logger.Warn("balance assertor not configured: assertion repository not yet available")

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
	var scheduler *worker.SettlementScheduler
	if cfg.Scheduler.Enabled {
		scheduler = wireScheduler(ctx, cfg, redisClient, logger)
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
	if scheduler != nil {
		go func() {
			if err := scheduler.Start(schedulerCtx); err != nil {
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

	select {
	case sig := <-sigChan:
		logger.Info("received signal", "signal", sig)
	case err := <-serverErrors:
		return fmt.Errorf("server error: %w", err)
	}

	// Graceful shutdown
	logger.Info("shutting down servers...")

	// Stop scheduler first (it makes gRPC calls to self)
	if scheduler != nil {
		logger.Info("stopping settlement scheduler...")
		schedulerCancel()
		scheduler.Stop()
		logger.Info("settlement scheduler stopped")
	}

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

	return nil
}

// wireScheduler creates and configures the SettlementScheduler with all its dependencies.
// Returns nil if any required dependency is not available.
func wireScheduler(ctx context.Context, cfg *config.Config, redisClient *redis.Client, logger *slog.Logger) *worker.SettlementScheduler {
	if redisClient == nil {
		logger.Warn("scheduler disabled: Redis not configured (required for leader election)")
		return nil
	}

	// Create leader elector
	leaderElector := worker.NewRedisLeaderElector(
		redisClient,
		worker.RedisLeaderConfig{
			LockTTL:       cfg.Scheduler.LeaderLockTTL,
			RenewInterval: cfg.Scheduler.LeaderRenewInterval,
		},
		logger,
	)

	// Create execution store (requires pgxpool for direct pgx queries)
	pool, err := pgxpool.New(ctx, cfg.Database.URL)
	if err != nil {
		logger.Warn("scheduler disabled: failed to create pgx pool for execution store",
			"error", err)
		return nil
	}
	executionStore := worker.NewPgExecutionStore(pool)

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

	// Create scheduler
	schedulerCfg := worker.SchedulerConfig{
		PollInterval:    cfg.Scheduler.PollInterval,
		ShutdownTimeout: cfg.Scheduler.ShutdownTimeout,
	}

	scheduler, err := worker.NewSettlementScheduler(
		refDataClient,
		reconClient,
		leaderElector,
		schedulerCfg,
		logger,
		nil, // metrics - use default
		worker.WithExecutionStore(executionStore),
	)
	if err != nil {
		logger.Warn("scheduler disabled: failed to create scheduler",
			"error", err)
		pool.Close()
		if closeErr := reconConn.Close(); closeErr != nil {
			logger.Error("failed to close reconciliation connection", "error", closeErr)
		}
		return nil
	}

	logger.Info("settlement scheduler configured",
		"poll_interval", cfg.Scheduler.PollInterval,
		"leader_lock_ttl", cfg.Scheduler.LeaderLockTTL,
		"shutdown_timeout", cfg.Scheduler.ShutdownTimeout)

	return scheduler
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
