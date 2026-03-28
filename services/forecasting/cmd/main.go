// Package main is the entry point for the Forecasting service.
// Manages forecasting strategies that generate forward curves from market data.
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

	"github.com/jackc/pgx/v5/pgxpool"
	forecastingv1 "github.com/meridianhub/meridian/api/proto/meridian/forecasting/v1"
	"github.com/meridianhub/meridian/services/forecasting/adapters/mds"
	"github.com/meridianhub/meridian/services/forecasting/adapters/persistence"
	"github.com/meridianhub/meridian/services/forecasting/handler"
	forecastscheduler "github.com/meridianhub/meridian/services/forecasting/scheduler"
	"github.com/meridianhub/meridian/services/forecasting/starlark"
	misclient "github.com/meridianhub/meridian/services/market-information/client"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/meridianhub/meridian/shared/platform/ports"
	"github.com/meridianhub/meridian/shared/platform/redislock"
	"github.com/meridianhub/meridian/shared/platform/scheduler"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
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

	logger.Info("starting forecasting service",
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

// forecastingDeps holds initialized dependencies for the forecasting service.
type forecastingDeps struct {
	dbPool      *pgxpool.Pool
	redisClient *redis.Client
	mdsCleanup  func() error
	svc         *handler.Service
	scheduler   *scheduler.CronScheduler
}

func run(logger *slog.Logger) error {
	ctx := context.Background()

	// Initialize OpenTelemetry tracer
	tracer, err := bootstrap.NewTracer(ctx, bootstrap.TracerConfig{
		ServiceName:    "forecasting-service",
		ServiceVersion: Version,
		Logger:         logger,
	})
	if err != nil {
		return fmt.Errorf("failed to initialize tracer: %w", err)
	}
	defer bootstrap.ShutdownTracer(tracer, logger)

	// Initialize dependencies
	deps, err := initDependencies(ctx, logger)
	if err != nil {
		return err
	}
	defer deps.dbPool.Close()
	defer func() { _ = deps.redisClient.Close() }()
	defer func() { _ = deps.mdsCleanup() }()

	// Create gRPC server, register services, and start listening
	grpcServer, listener, err := setupGRPCServer(ctx, tracer, logger, deps.svc)
	if err != nil {
		return err
	}

	// Start all servers and scheduler, then await shutdown
	return startAndAwaitShutdown(grpcServer, listener, deps, logger)
}

// initDependencies creates the database pool, MDS client, forecast runner, service handler,
// and cron scheduler with all their sub-dependencies.
func initDependencies(ctx context.Context, logger *slog.Logger) (*forecastingDeps, error) {
	dbPool, err := initDatabasePool(ctx, logger)
	if err != nil {
		return nil, err
	}
	repo := persistence.NewStrategyRepository(dbPool)
	logger.Info("strategy repository initialized")

	mdsTarget := env.GetEnvOrDefault("MDS_TARGET", "market-information:50051")
	mdsClient, mdsCleanup, err := misclient.New(ctx, misclient.Config{Target: mdsTarget})
	if err != nil {
		dbPool.Close()
		return nil, fmt.Errorf("failed to create MDS client: %w", err)
	}
	logger.Info("MDS client initialized", "target", mdsTarget)

	forecastingSvc, err := createForecastingService(repo, mdsClient, logger)
	if err != nil {
		_ = mdsCleanup()
		dbPool.Close()
		return nil, err
	}

	cronScheduler, redisClient, err := createScheduler(dbPool, repo, forecastingSvc, logger) //nolint:contextcheck // NewPgExecutionStore manages its own context
	if err != nil {
		_ = mdsCleanup()
		dbPool.Close()
		return nil, err
	}

	return &forecastingDeps{
		dbPool:      dbPool,
		redisClient: redisClient,
		mdsCleanup:  mdsCleanup,
		svc:         forecastingSvc,
		scheduler:   cronScheduler,
	}, nil
}

// initDatabasePool creates and validates a pgxpool connection for the forecasting service.
func initDatabasePool(ctx context.Context, logger *slog.Logger) (*pgxpool.Pool, error) {
	dbURL := env.GetEnvOrDefault("DATABASE_URL",
		"postgres://meridian_user@localhost:26257/meridian_forecasting?sslmode=disable")
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

// createForecastingService builds the forecast runner and service handler from MDS client adapters.
func createForecastingService(repo *persistence.StrategyRepository, mdsClient *misclient.Client, logger *slog.Logger) (*handler.Service, error) {
	misAdapter := mds.NewMISAdapter(mdsClient)
	mdsPublisher := mds.NewPublisherAdapter(mdsClient)

	runner, err := starlark.NewForecastRunner(starlark.ForecastRunnerConfig{
		MISClient: misAdapter,
		RefData:   &mds.NoOpRefDataClient{},
		Logger:    logger,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create forecast runner: %w", err)
	}

	svc, err := handler.NewService(repo, runner, mdsPublisher, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create forecasting service: %w", err)
	}
	logger.Info("forecasting service handler initialized")
	return svc, nil
}

// createScheduler builds the cron scheduler with Redis distributed locking and execution store.
func createScheduler(dbPool *pgxpool.Pool, repo *persistence.StrategyRepository, forecastingSvc *handler.Service, logger *slog.Logger) (*scheduler.CronScheduler, *redis.Client, error) {
	redisAddr := env.GetEnvOrDefault("REDIS_ADDR", "localhost:6379")
	redisClient := redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})
	logger.Info("redis client initialized", "addr", redisAddr)

	distLock := redislock.NewLock(redisClient, redislock.Config{
		KeyPrefix:  "meridian:forecasting:strategy",
		LockTTL:    5 * time.Minute,
		RenewEvery: 30 * time.Second,
	}, logger)

	execStore, err := scheduler.NewPgExecutionStore(dbPool)
	if err != nil {
		logger.Warn("scheduler execution store unavailable, audit trail disabled", "error", err)
		execStore = nil
	}

	schedProvider := forecastscheduler.NewForecastScheduleProvider(repo)
	schedMetrics := forecastscheduler.NewMetrics()
	schedExecutor := forecastscheduler.NewForecastScheduleExecutor(
		forecastingSvc.ComputeForwardCurveInternal,
		schedMetrics,
	)

	refreshIntervalStr := env.GetEnvOrDefault("SCHEDULER_REFRESH_INTERVAL", "60s")
	refreshInterval, err := time.ParseDuration(refreshIntervalStr)
	if err != nil {
		logger.Warn("invalid SCHEDULER_REFRESH_INTERVAL, using default 60s", "value", refreshIntervalStr, "error", err)
		refreshInterval = 60 * time.Second
	}

	var cronSchedulerOpts []scheduler.CronSchedulerOption
	if execStore != nil {
		cronSchedulerOpts = append(cronSchedulerOpts, scheduler.WithCronExecutionStore(execStore))
	}

	return scheduler.NewCronScheduler(
		schedProvider,
		schedExecutor,
		distLock,
		scheduler.CronSchedulerConfig{
			Name:             "forecasting",
			RefreshInterval:  refreshInterval,
			ShutdownTimeout:  5 * time.Minute,
			ExecutionTimeout: 10 * time.Minute,
			MaxCatchUpAge:    time.Hour,
		},
		logger,
		cronSchedulerOpts...,
	), redisClient, nil
}

// startAndAwaitShutdown starts the gRPC server, cron scheduler, and HTTP server,
// then waits for a shutdown signal and orchestrates graceful shutdown.
func startAndAwaitShutdown(grpcServer *grpc.Server, listener net.Listener, deps *forecastingDeps, logger *slog.Logger) error {
	serverErrors := make(chan error, 3)
	go func() {
		logger.Info("starting gRPC server", "address", listener.Addr().String())
		if err := grpcServer.Serve(listener); err != nil {
			serverErrors <- err
		}
	}()

	schedulerCtx, schedulerCancel := context.WithCancel(context.Background())
	go func() {
		logger.Info("starting forecasting cron scheduler")
		if err := deps.scheduler.Start(schedulerCtx); err != nil {
			logger.Error("cron scheduler error", "error", err)
			serverErrors <- fmt.Errorf("cron scheduler error: %w", err)
		}
	}()

	metricsPort := env.GetEnvOrDefault("METRICS_PORT", "8082")
	httpServer := startHTTPServer(metricsPort, logger, serverErrors)

	orchestrator := bootstrap.NewShutdownOrchestrator(grpcServer, logger)
	orchestrator.AddCleanup(func() error {
		schedulerCancel()
		deps.scheduler.Stop()
		logger.Info("cron scheduler stopped")
		return nil
	})
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

// setupGRPCServer creates the gRPC server, registers services, and creates the TCP listener.
func setupGRPCServer(ctx context.Context, tracer *observability.Tracer, logger *slog.Logger, forecastingSvc *handler.Service) (*grpc.Server, net.Listener, error) {
	authConfig := bootstrap.DefaultAuthConfig(logger)
	authInterceptor, err := bootstrap.NewAuthInterceptor(ctx, authConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize auth: %w", err)
	}

	grpcServer, err := bootstrap.NewGrpcServerBuilder(tracer, logger).
		WithAuthInterceptor(authInterceptor).
		Build() //nolint:contextcheck // gRPC interceptors manage their own contexts
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build grpc server: %w", err)
	}

	forecastingv1.RegisterForecastingServiceServer(grpcServer, forecastingSvc)

	healthServer := health.NewServer()
	healthServer.SetServingStatus("forecasting", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	reflection.Register(grpcServer)

	logger.Info("gRPC services registered")

	port := env.GetEnvOrDefault("GRPC_PORT", strconv.Itoa(ports.Forecasting))
	address := fmt.Sprintf(":%s", port)
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", address) //nolint:contextcheck // listener intentionally outlives request contexts
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
