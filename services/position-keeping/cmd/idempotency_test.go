package main

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/position-keeping/app"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
)

func TestIdempotencyService_WiringWithRedis(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Test that idempotency service is wired when Redis is enabled
	config := &app.Config{
		Database: app.DatabaseConfig{
			URL:                 "postgres://test:test@localhost:5432/testdb",
			MaxOpenConns:        5,
			MaxIdleConns:        2,
			HealthCheckInterval: 30 * time.Second,
		},
		Redis: app.RedisConfig{
			Enabled:  true,
			Address:  "localhost:6379",
			Password: "",
			DB:       0,
			PoolSize: 10,
		},
		Observability: app.ObservabilityConfig{
			OTLPEndpoint: "", // Tracing disabled
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Try to create container
	container, err := app.NewContainer(ctx, config, logger)
	if err != nil {
		t.Skip("skipping test: cannot create container (Redis or DB unavailable)")
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		_ = container.Close(shutdownCtx)
	}()

	// Wire up idempotency service (same logic as main.go)
	var idempotencySvc idempotency.Service
	if container.RedisClient != nil {
		idempotencySvc = idempotency.NewRedisService(container.RedisClient)
	}

	// Verify service was wired
	if idempotencySvc == nil {
		t.Error("idempotency service should not be nil when Redis is available")
	}

	// Verify it's the correct type
	if _, ok := idempotencySvc.(*idempotency.RedisService); !ok {
		t.Errorf("idempotency service should be *idempotency.RedisService, got %T", idempotencySvc)
	}
}

func TestIdempotencyService_WiringWithoutRedis(t *testing.T) {
	// Test that idempotency service is nil when Redis is disabled
	config := &app.Config{
		Database: app.DatabaseConfig{
			URL:                 "postgres://test:test@localhost:5432/testdb",
			MaxOpenConns:        5,
			MaxIdleConns:        2,
			HealthCheckInterval: 30 * time.Second,
		},
		Redis: app.RedisConfig{
			Enabled: false, // Redis disabled
		},
		Observability: app.ObservabilityConfig{
			OTLPEndpoint: "", // Tracing disabled
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Try to create container
	container, err := app.NewContainer(ctx, config, logger)
	if err != nil {
		t.Skip("skipping test: cannot create container (DB unavailable)")
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		_ = container.Close(shutdownCtx)
	}()

	// Wire up idempotency service (same logic as main.go)
	var idempotencySvc idempotency.Service
	if container.RedisClient != nil {
		idempotencySvc = idempotency.NewRedisService(container.RedisClient)
	}

	// Verify service is nil when Redis is disabled
	if idempotencySvc != nil {
		t.Error("idempotency service should be nil when Redis is disabled")
	}

	// Verify Redis client is nil
	if container.RedisClient != nil {
		t.Error("container.RedisClient should be nil when Redis is disabled")
	}
}
