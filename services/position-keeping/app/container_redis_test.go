package app

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"
)

func TestContainer_InitializeRedis_Disabled(t *testing.T) {
	// Test that Redis initialization is skipped when disabled
	config := &Config{
		Redis: RedisConfig{
			Enabled: false,
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))

	container := &Container{
		Config: config,
		Logger: logger,
	}

	ctx := context.Background()
	err := container.initializeRedis(ctx)
	if err != nil {
		t.Errorf("initializeRedis() with disabled Redis returned error: %v", err)
	}

	if container.RedisClient != nil {
		t.Error("RedisClient should be nil when Redis is disabled")
	}
}

func TestContainer_InitializeRedis_InvalidAddress(t *testing.T) {
	// Test that initialization fails gracefully with invalid address
	config := &Config{
		Redis: RedisConfig{
			Enabled:  true,
			Address:  "invalid:address:9999",
			Password: "",
			DB:       0,
			PoolSize: 10,
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))

	container := &Container{
		Config: config,
		Logger: logger,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := container.initializeRedis(ctx)

	// Should return an error when connection fails
	if err == nil {
		t.Error("initializeRedis() with invalid address should return error")
	}

	// Client should not be set if initialization failed
	if container.RedisClient != nil {
		t.Error("RedisClient should be nil when initialization fails")
	}
}

func TestContainer_Close_WithRedis(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Test that Close() properly cleans up Redis client
	config := &Config{
		Database: DatabaseConfig{
			URL:                 "postgres://test:test@localhost:5432/testdb",
			MaxOpenConns:        5,
			MaxIdleConns:        2,
			HealthCheckInterval: 30 * time.Second, // Required for pgxpool
		},
		Redis: RedisConfig{
			Enabled:  true,
			Address:  "localhost:6379",
			Password: "",
			DB:       0,
			PoolSize: 10,
		},
		Observability: ObservabilityConfig{
			OTLPEndpoint: "", // Tracing disabled
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Try to create container (may fail without services, which is ok for this test)
	container, err := NewContainer(ctx, config, logger)
	if err != nil {
		// Expected in test environment - skip test
		t.Skip("skipping test: cannot create container in test environment")
	}

	// Verify Redis client was created (if we got this far)
	if container.RedisClient == nil {
		t.Error("RedisClient should not be nil after successful initialization")
	}

	// Close container
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer shutdownCancel()

	err = container.Close(shutdownCtx)
	// Close should succeed or return specific errors
	if err != nil {
		t.Logf("Close() returned error (may be expected): %v", err)
	}
}

func TestContainer_Close_WithoutRedis(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Test that Close() works when Redis is not enabled
	config := &Config{
		Database: DatabaseConfig{
			URL:                 "postgres://test:test@localhost:5432/testdb",
			MaxOpenConns:        5,
			MaxIdleConns:        2,
			HealthCheckInterval: 30 * time.Second, // Required for pgxpool
		},
		Redis: RedisConfig{
			Enabled: false, // Redis disabled
		},
		Observability: ObservabilityConfig{
			OTLPEndpoint: "", // Tracing disabled
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Try to create container
	container, err := NewContainer(ctx, config, logger)
	if err != nil {
		t.Skip("skipping test: cannot create container in test environment")
	}

	// Verify Redis client was NOT created
	if container.RedisClient != nil {
		t.Error("RedisClient should be nil when Redis is disabled")
	}

	// Close container - should not error even without Redis
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer shutdownCancel()

	err = container.Close(shutdownCtx)
	if err != nil {
		t.Logf("Close() returned error (may be expected): %v", err)
	}
}
