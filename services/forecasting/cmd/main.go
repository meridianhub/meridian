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

	// Initialize database connection
	dbURL := env.GetEnvOrDefault("DATABASE_URL",
		"postgres://meridian_user@localhost:26257/meridian_forecasting?sslmode=disable")
	dbPool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("failed to create database connection pool: %w", err)
	}
	defer dbPool.Close()

	if err := dbPool.Ping(ctx); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}
	logger.Info("database connection established", "url", dbURL)

	// Create repository
	repo := persistence.NewStrategyRepository(dbPool)
	logger.Info("strategy repository initialized")

	// Initialize MDS client for observation fetching and publishing
	mdsTarget := env.GetEnvOrDefault("MDS_TARGET", "market-information:50051")
	mdsClient, mdsCleanup, err := misclient.New(ctx, misclient.Config{
		Target: mdsTarget,
	})
	if err != nil {
		return fmt.Errorf("failed to create MDS client: %w", err)
	}
	defer func() { _ = mdsCleanup() }()
	logger.Info("MDS client initialized", "target", mdsTarget)

	// Create adapters
	misAdapter := mds.NewMISAdapter(mdsClient)
	mdsPublisher := mds.NewPublisherAdapter(mdsClient)
	refDataClient := &mds.NoOpRefDataClient{}

	// Create Starlark forecast runner
	runner, err := starlark.NewForecastRunner(starlark.ForecastRunnerConfig{
		MISClient: misAdapter,
		RefData:   refDataClient,
		Logger:    logger,
	})
	if err != nil {
		return fmt.Errorf("failed to create forecast runner: %w", err)
	}

	// Create forecasting service handler
	forecastingSvc, err := handler.NewService(repo, runner, mdsPublisher, logger)
	if err != nil {
		return fmt.Errorf("failed to create forecasting service: %w", err)
	}
	logger.Info("forecasting service handler initialized")

	// Initialize Redis client for distributed locking
	redisAddr := env.GetEnvOrDefault("REDIS_ADDR", "localhost:6379")
	redisClient := redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})
	defer redisClient.Close()
	logger.Info("redis client initialized", "addr", redisAddr)

	// Create distributed lock for scheduler deduplication
	distLock := redislock.NewLock(redisClient, redislock.Config{
		KeyPrefix:  "meridian:forecasting:strategy",
		LockTTL:    5 * time.Minute,
		RenewEvery: 30 * time.Second,
	}, logger)

	// Create execution store for audit trail
	execStore, err := scheduler.NewPgExecutionStore(dbPool)
	if err != nil {
		logger.Warn("scheduler execution store unavailable, audit trail disabled", "error", err)
		execStore = nil
	}

	// Create scheduler adapters
	schedProvider := forecastscheduler.NewForecastScheduleProvider(repo)
	schedMetrics := forecastscheduler.NewMetrics()
	schedExecutor := forecastscheduler.NewForecastScheduleExecutor(
		forecastingSvc.ComputeForwardCurveInternal,
		schedMetrics,
	)

	// Create shared cron scheduler
	refreshIntervalStr := env.GetEnvOrDefault("SCHEDULER_REFRESH_INTERVAL", "60s")
	refreshInterval, err := time.ParseDuration(refreshIntervalStr)
	if err != nil {
		logger.Warn("invalid SCHEDULER_REFRESH_INTERVAL, using default 60s", "value", refreshIntervalStr, "error", err)
		refreshInterval = 60 * time.Second
	}
	cronSchedulerOpts := []scheduler.CronSchedulerOption{}
	if execStore != nil {
		cronSchedulerOpts = append(cronSchedulerOpts, scheduler.WithCronExecutionStore(execStore))
	}
	cronScheduler := scheduler.NewCronScheduler(
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
	)

	// Initialize auth interceptor
	authConfig := bootstrap.DefaultAuthConfig(logger)
	authInterceptor, err := bootstrap.NewAuthInterceptor(ctx, authConfig)
	if err != nil {
		return fmt.Errorf("failed to initialize auth: %w", err)
	}

	// Create gRPC server
	grpcServer, err := bootstrap.NewGrpcServerBuilder(tracer, logger).
		WithAuthInterceptor(authInterceptor).
		Build()
	if err != nil {
		return fmt.Errorf("failed to build grpc server: %w", err)
	}

	// Register forecasting gRPC service
	forecastingv1.RegisterForecastingServiceServer(grpcServer, forecastingSvc)

	// Register health check service
	healthServer := health.NewServer()
	healthServer.SetServingStatus("forecasting", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)

	// Register reflection service for debugging
	reflection.Register(grpcServer)

	logger.Info("gRPC services registered")

	// Get ports
	port := env.GetEnvOrDefault("GRPC_PORT", strconv.Itoa(ports.Forecasting))
	address := fmt.Sprintf(":%s", port)
	metricsPort := env.GetEnvOrDefault("METRICS_PORT", "8082")

	// Create listener
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", address)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", address, err)
	}

	serverErrors := make(chan error, 3)
	go func() {
		logger.Info("starting gRPC server", "address", address)
		if err := grpcServer.Serve(listener); err != nil {
			serverErrors <- err
		}
	}()

	// Start cron scheduler in background
	schedulerCtx, schedulerCancel := context.WithCancel(context.Background())
	go func() {
		logger.Info("starting forecasting cron scheduler")
		if err := cronScheduler.Start(schedulerCtx); err != nil {
			logger.Error("cron scheduler error", "error", err)
			serverErrors <- fmt.Errorf("cron scheduler error: %w", err)
		}
	}()

	// Start HTTP server for metrics and health
	httpMux := http.NewServeMux()
	httpMux.Handle("/metrics", promhttp.Handler())
	httpMux.HandleFunc("/health", healthHandler(grpcServer))
	httpMux.HandleFunc("/ready", healthHandler(grpcServer))

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

	// Wait for shutdown
	orchestrator := bootstrap.NewShutdownOrchestrator(grpcServer, logger)

	orchestrator.AddCleanup(func() error {
		// Stop cron scheduler
		schedulerCancel()
		cronScheduler.Stop()
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
