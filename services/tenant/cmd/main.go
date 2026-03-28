// Package main is the entry point for the Tenant service.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	pb "github.com/meridianhub/meridian/api/proto/meridian/tenant/v1"
	"github.com/meridianhub/meridian/services/tenant/app"
	"github.com/meridianhub/meridian/services/tenant/service"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/ports"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

// Build information set via ldflags during compilation.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// Errors returned during configuration and startup.
var (
	ErrInvalidPollInterval    = errors.New("poll interval must be >= 1s")
	ErrInvalidMaxRetries      = errors.New("max retries must be >= 0 and <= 20")
	ErrInvalidRetryBaseDelay  = errors.New("retry base delay must be > 0")
	ErrInvalidRetryMaxDelay   = errors.New("retry max delay must be > 0")
	ErrInvalidRetryDelayRange = errors.New("retry base delay must be < retry max delay")
	ErrInvalidMaxConcurrent   = errors.New("max concurrent must be >= 1 and <= 100")
)

// WorkerConfig holds configuration for the provisioning worker behavior.
type WorkerConfig struct {
	PollInterval   time.Duration
	MaxRetries     int
	RetryBaseDelay time.Duration
	RetryMaxDelay  time.Duration
	MaxConcurrent  int
}

// Validate checks if the WorkerConfig has valid values.
// Returns an error if any configuration value is invalid.
func (c WorkerConfig) Validate() error {
	if c.PollInterval < 1*time.Second {
		return fmt.Errorf("%w: got %s", ErrInvalidPollInterval, c.PollInterval)
	}
	if c.MaxRetries < 0 || c.MaxRetries > 20 {
		return fmt.Errorf("%w: got %d", ErrInvalidMaxRetries, c.MaxRetries)
	}
	if c.RetryBaseDelay <= 0 {
		return fmt.Errorf("%w: got %s", ErrInvalidRetryBaseDelay, c.RetryBaseDelay)
	}
	if c.RetryMaxDelay <= 0 {
		return fmt.Errorf("%w: got %s", ErrInvalidRetryMaxDelay, c.RetryMaxDelay)
	}
	if c.RetryBaseDelay >= c.RetryMaxDelay {
		return fmt.Errorf("%w: base=%s, max=%s", ErrInvalidRetryDelayRange, c.RetryBaseDelay, c.RetryMaxDelay)
	}
	if c.MaxConcurrent < 1 || c.MaxConcurrent > 100 {
		return fmt.Errorf("%w: got %d", ErrInvalidMaxConcurrent, c.MaxConcurrent)
	}
	return nil
}

func main() {
	// Initialize structured logging with configurable log level
	// Note: bootstrap.NewLogger hardcodes INFO level, so we create logger manually for LOG_LEVEL support
	logLevel := parseLogLevel(os.Getenv("LOG_LEVEL"))
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))
	slog.SetDefault(logger)

	logger.Info("starting tenant service",
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

	// Initialize dependency container (tracer, database, provisioner, party client, Redis, worker, auth)
	container, err := app.NewContainer(ctx, logger, Version)
	if err != nil {
		return fmt.Errorf("failed to initialize container: %w", err)
	}
	defer container.Close()

	// Create gRPC server with interceptor chain
	// Order is handled by bootstrap: tracing -> platform auth -> platform admin -> recovery
	// Note: WithPlatformAdmin() adds PlatformAdminInterceptor for platform-layer services
	grpcServer, err := bootstrap.NewGrpcServerBuilder(container.Tracer, logger).
		WithAuthInterceptor(container.AuthInterceptor).
		WithPlatformAdmin().
		Build()
	if err != nil {
		return fmt.Errorf("failed to build grpc server: %w", err)
	}

	// Register services
	pb.RegisterTenantServiceServer(grpcServer, container.TenantService)

	// Register health check service
	healthChecker := service.NewHealthChecker(service.HealthCheckerConfig{
		Repository:  container.Repo,
		Logger:      logger,
		ServiceName: "tenant",
		Timeout:     defaults.DefaultHealthCheckTimeout,
	})
	grpc_health_v1.RegisterHealthServer(grpcServer, healthChecker)

	// Register reflection service for debugging
	reflection.Register(grpcServer)

	logger.Info("gRPC services registered")

	// Get port from environment
	port := env.GetEnvOrDefault("GRPC_PORT", strconv.Itoa(ports.Tenant))
	address := fmt.Sprintf(":%s", port)

	// Create listener
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", address)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", address, err)
	}

	// Start gRPC server in background
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("starting gRPC server", "address", address)
		if err := grpcServer.Serve(listener); err != nil {
			serverErrors <- err
		}
	}()

	// Wait for shutdown signal and orchestrate graceful shutdown.
	// container.Close() (deferred above) handles all resource cleanup:
	// provisioning worker, provisioner connections, party client, Redis, database, tracer.
	orchestrator := bootstrap.NewShutdownOrchestrator(grpcServer, logger)

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

// loadWorkerConfig loads worker configuration from environment variables with defaults.
// It validates the configuration and returns an error if any value is invalid.
func loadWorkerConfig() (WorkerConfig, error) {
	config := WorkerConfig{
		PollInterval:   env.GetEnvAsDuration("PROVISIONING_WORKER_POLL_INTERVAL", 10*time.Second),
		MaxRetries:     env.GetEnvAsInt("PROVISIONING_MAX_RETRIES", 5),
		RetryBaseDelay: env.GetEnvAsDuration("PROVISIONING_RETRY_BASE_DELAY", 2*time.Second),
		RetryMaxDelay:  env.GetEnvAsDuration("PROVISIONING_RETRY_MAX_DELAY", defaults.DefaultMaxRetryInterval),
		MaxConcurrent:  env.GetEnvAsInt("PROVISIONING_MAX_CONCURRENT", 5),
	}

	// Validate configuration
	if err := config.Validate(); err != nil {
		return WorkerConfig{}, fmt.Errorf("invalid worker configuration: %w", err)
	}

	// Log loaded configuration with sources
	slog.Info("worker configuration loaded",
		"poll_interval", config.PollInterval,
		"poll_interval_source", getConfigSource("PROVISIONING_WORKER_POLL_INTERVAL"),
		"max_retries", config.MaxRetries,
		"max_retries_source", getConfigSource("PROVISIONING_MAX_RETRIES"),
		"retry_base_delay", config.RetryBaseDelay,
		"retry_base_delay_source", getConfigSource("PROVISIONING_RETRY_BASE_DELAY"),
		"retry_max_delay", config.RetryMaxDelay,
		"retry_max_delay_source", getConfigSource("PROVISIONING_RETRY_MAX_DELAY"),
		"max_concurrent", config.MaxConcurrent,
		"max_concurrent_source", getConfigSource("PROVISIONING_MAX_CONCURRENT"))

	return config, nil
}

// getConfigSource returns "env" if the environment variable is set, "default" otherwise.
func getConfigSource(key string) string {
	if os.Getenv(key) != "" {
		return "env"
	}
	return "default"
}
