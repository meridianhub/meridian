// Package main is the entry point for the Gateway service.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/meridianhub/meridian/services/gateway"
	gwhealth "github.com/meridianhub/meridian/services/gateway/health"
	"github.com/meridianhub/meridian/shared/pkg/health"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/db"
	"github.com/redis/go-redis/v9"
)

// Build information set via ldflags during compilation.
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

	logger.Info("starting gateway service",
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
	// Load configuration (permanent error if invalid)
	config, err := gateway.LoadConfig()
	if err != nil {
		return bootstrap.Permanent(fmt.Errorf("failed to load configuration: %w", err))
	}

	// Production safety check: LOCAL_DEV_MODE must not be enabled in production namespaces
	namespace := os.Getenv("POD_NAMESPACE")
	if err := config.ValidateForNamespace(namespace); err != nil {
		return bootstrap.Permanent(err)
	}

	logger.Info("configuration loaded",
		"port", config.Port,
		"base_domain", config.BaseDomain,
		"local_dev_mode", config.LocalDevMode,
		"namespace", namespace,
		"redis_enabled", config.RedisURL != "",
		"backend_routes", len(config.Backends))

	// Initialize database pool for tenant resolution and health checks
	dbPool, err := db.NewPostgresPool(context.Background(), db.DefaultConfig(config.DatabaseURL))
	if err != nil {
		return fmt.Errorf("failed to create database pool: %w", err)
	}
	defer func() { _ = dbPool.Close() }()

	logger.Info("database pool initialized")

	// Build health checkers list
	checkers := []health.Checker{
		health.NewDatabaseChecker(dbPool), // Critical dependency
	}

	// Initialize Redis client if configured (optional dependency)
	var redisClient *redis.Client
	if config.RedisURL != "" {
		redisClient = redis.NewClient(&redis.Options{
			Addr: config.RedisURL,
		})
		defer func() { _ = redisClient.Close() }()

		// Verify Redis connection (log warning on failure, don't fail startup)
		if err := redisClient.Ping(context.Background()).Err(); err != nil {
			logger.Warn("redis connection failed (will operate in degraded mode)", "error", err)
		} else {
			logger.Info("redis client initialized")
			checkers = append(checkers, health.NewRedisChecker(redisClient))
		}
	}

	// Create health checker with all components
	healthChecker := gwhealth.NewGatewayHealthChecker(gwhealth.Config{
		Checkers:     checkers,
		CheckTimeout: 5 * time.Second,
	})

	// Create server with health checker
	// Note: Tenant resolver will be initialized in a future task when database connection is available.
	// For now, pass nil to allow the server to start without tenant resolution.
	// Health endpoints will work regardless of tenant resolver configuration.
	server := gateway.NewServer(config, logger, nil, gateway.WithHealthChecker(healthChecker))

	// Start server in background
	serverErrors := make(chan error, 1)
	go func() {
		if err := server.Start(context.Background()); err != nil {
			serverErrors <- err
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
	logger.Info("initiating graceful shutdown...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown error: %w", err)
	}

	return nil
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
