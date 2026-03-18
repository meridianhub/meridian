package app

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/meridianhub/meridian/shared/platform/testdb"
)

// setupCockroachDBURL returns a real CockroachDB connection URL for testing.
func setupCockroachDBURL(t *testing.T) string {
	t.Helper()

	container, cleanup := testdb.StartCockroachContainer(t, "app_test_db")
	t.Cleanup(cleanup)

	return testdb.CockroachDSN(t, container)
}

func TestNewContainer_RealDB(t *testing.T) {
	dbURL := setupCockroachDBURL(t)

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	config := &Config{
		Service: ServiceConfig{
			Name:                    "test-service",
			Port:                    "8080",
			GracefulShutdownTimeout: 30 * time.Second,
		},
		Database: DatabaseConfig{
			URL:               dbURL,
			MaxOpenConns:      5,
			MaxIdleConns:      2,
			ConnMaxLifetime:   5 * time.Minute,
			ConnMaxIdleTime:   10 * time.Minute,
			PoolStatsInterval: 10 * time.Second,
		},
		Kafka: KafkaConfig{
			BootstrapServers: "localhost:9092", // fake — consumer creation is lazy
			Topic:            "audit.events.test.v1",
			GroupID:          "test-consumer-group",
			ClientID:         "test-consumer",
			HandlerTimeout:   30 * time.Second,
			MaxRetries:       3,
		},
	}

	ctx := context.Background()
	container, err := NewContainer(ctx, config, logger)
	if err != nil {
		t.Fatalf("NewContainer() unexpected error: %v", err)
	}
	defer func() {
		_ = container.Close(ctx)
	}()

	if container.DB == nil {
		t.Error("container.DB should not be nil")
	}
	if container.AuditConsumer == nil {
		t.Error("container.AuditConsumer should not be nil")
	}
	if container.HealthChecker == nil {
		t.Error("container.HealthChecker should not be nil")
	}
}

func TestNewContainer_InvalidDBURL(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	config := &Config{
		Service: ServiceConfig{
			Name:                    "test-service",
			Port:                    "8080",
			GracefulShutdownTimeout: 30 * time.Second,
		},
		Database: DatabaseConfig{
			URL:             "postgres://invalid:5432/nonexistent_db_xyz_abc",
			MaxOpenConns:    5,
			MaxIdleConns:    2,
			ConnMaxLifetime: 5 * time.Minute,
			ConnMaxIdleTime: 10 * time.Minute,
		},
		Kafka: KafkaConfig{
			BootstrapServers: "localhost:9092",
			Topic:            "audit.events.test.v1",
			GroupID:          "test-group",
			ClientID:         "test-client",
			HandlerTimeout:   30 * time.Second,
			MaxRetries:       3,
		},
	}

	ctx := context.Background()
	container, err := NewContainer(ctx, config, logger)
	if err == nil {
		_ = container.Close(ctx)
		t.Fatal("NewContainer() expected error for invalid DB URL, got nil")
	}
}

func TestContainer_Close_WithDB(t *testing.T) {
	dbURL := setupCockroachDBURL(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	config := &Config{
		Service: ServiceConfig{
			Name:                    "test-svc",
			Port:                    "8080",
			GracefulShutdownTimeout: 30 * time.Second,
		},
		Database: DatabaseConfig{
			URL:               dbURL,
			MaxOpenConns:      5,
			MaxIdleConns:      2,
			ConnMaxLifetime:   5 * time.Minute,
			ConnMaxIdleTime:   10 * time.Minute,
			PoolStatsInterval: 10 * time.Second,
		},
		Kafka: KafkaConfig{
			BootstrapServers: "localhost:9092",
			Topic:            "audit.events.test.v1",
			GroupID:          "test-group",
			ClientID:         "test-client",
			HandlerTimeout:   30 * time.Second,
			MaxRetries:       3,
		},
	}

	ctx := context.Background()
	c, err := NewContainer(ctx, config, logger)
	if err != nil {
		t.Fatalf("NewContainer() error: %v", err)
	}

	if closeErr := c.Close(ctx); closeErr != nil {
		t.Errorf("Close() error: %v", closeErr)
	}

	// Idempotent close
	if closeErr := c.Close(ctx); closeErr != nil {
		t.Errorf("second Close() error: %v", closeErr)
	}
}

func TestDBWrapper_PingAndStats(t *testing.T) {
	dbURL := setupCockroachDBURL(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	config := &Config{
		Service: ServiceConfig{
			Name:                    "test-svc",
			Port:                    "8080",
			GracefulShutdownTimeout: 30 * time.Second,
		},
		Database: DatabaseConfig{
			URL:               dbURL,
			MaxOpenConns:      5,
			MaxIdleConns:      2,
			ConnMaxLifetime:   5 * time.Minute,
			ConnMaxIdleTime:   10 * time.Minute,
			PoolStatsInterval: 10 * time.Second,
		},
		Kafka: KafkaConfig{
			BootstrapServers: "localhost:9092",
			Topic:            "audit.events.test.v1",
			GroupID:          "test-group",
			ClientID:         "test-client",
			HandlerTimeout:   30 * time.Second,
			MaxRetries:       3,
		},
	}

	ctx := context.Background()
	c, err := NewContainer(ctx, config, logger)
	if err != nil {
		t.Fatalf("NewContainer() error: %v", err)
	}
	defer func() { _ = c.Close(ctx) }()

	// Test Ping via dbWrapper
	if err := c.dbWrapper.Ping(ctx); err != nil {
		t.Errorf("dbWrapper.Ping() error: %v", err)
	}

	// Test Stats via dbWrapper
	stats := c.dbWrapper.Stats()
	if stats.MaxOpenConnections != 5 {
		t.Errorf("Stats().MaxOpenConnections = %d, want 5", stats.MaxOpenConnections)
	}
}

func TestCollectDBPoolStats_TickerFires(t *testing.T) {
	dbURL := setupCockroachDBURL(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	config := &Config{
		Service: ServiceConfig{
			Name:                    "test-svc-ticker",
			Port:                    "8080",
			GracefulShutdownTimeout: 30 * time.Second,
		},
		Database: DatabaseConfig{
			URL:               dbURL,
			MaxOpenConns:      5,
			MaxIdleConns:      2,
			ConnMaxLifetime:   5 * time.Minute,
			ConnMaxIdleTime:   10 * time.Minute,
			PoolStatsInterval: 10 * time.Millisecond, // Very short to trigger ticker path
		},
		Kafka: KafkaConfig{
			BootstrapServers: "localhost:9092",
			Topic:            "audit.events.test.v1",
			GroupID:          "test-group-ticker",
			ClientID:         "test-client-ticker",
			HandlerTimeout:   30 * time.Second,
			MaxRetries:       3,
		},
	}

	ctx := context.Background()
	start := time.Now()
	c, err := NewContainer(ctx, config, logger)
	if err != nil {
		t.Fatalf("NewContainer() error: %v", err)
	}

	// Allow ticker to fire at least a couple times before closing.
	// Poll until enough time has passed for the 10ms ticker to fire multiple times.
	_ = await.New().
		AtMost(500 * time.Millisecond).
		PollInterval(5 * time.Millisecond).
		Until(func() bool {
			return time.Since(start) >= 50*time.Millisecond
		})

	if closeErr := c.Close(ctx); closeErr != nil {
		t.Errorf("Close() error: %v", closeErr)
	}
}
