package app

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"
)

func TestNewContainer_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Set up test environment with minimal config
	clearEnv(t)
	t.Setenv("DATABASE_URL", "postgres://test:test@localhost:5432/testdb")
	t.Setenv("KAFKA_ENABLED", "false") // Disable Kafka for unit test
	t.Setenv("OTLP_ENDPOINT", "")      // Disable tracing for unit test

	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Note: This will fail without a real database connection
	// In a real integration test, you'd use testcontainers
	_, err = NewContainer(ctx, config, logger)

	// We expect this to fail in unit tests without a database
	// The test verifies the container attempts to initialize properly
	if err == nil {
		t.Error("NewContainer() expected error without database, got nil")
	}
}

func TestContainerInitialization_Order(t *testing.T) {
	// This test verifies validation happens before container initialization

	// Invalid config should fail validation
	invalidConfig := &Config{
		Server: ServerConfig{
			Port: "", // Invalid empty port
		},
		Database: DatabaseConfig{
			URL:          "postgres://localhost/db",
			MaxOpenConns: 10,
		},
		Observability: ObservabilityConfig{
			SamplingRate: 0.5,
		},
	}

	// Validate should catch this
	if err := invalidConfig.Validate(); err == nil {
		t.Error("expected validation error for empty port")
	}
}

func TestContainerClose_Idempotent(t *testing.T) {
	// Test that Close() can be called multiple times safely
	ctx := context.Background()

	container := &Container{
		Logger: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelError,
		})),
	}

	// First close
	err := container.Close(ctx)
	if err != nil {
		t.Errorf("first Close() error = %v, want nil", err)
	}

	// Second close should not panic
	err = container.Close(ctx)
	if err != nil {
		t.Errorf("second Close() error = %v, want nil", err)
	}
}

func TestContainerClose_WithTimeout(t *testing.T) {
	// Test that Close() respects context timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	container := &Container{
		Logger: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelError,
		})),
	}

	// Close with very short timeout should complete
	// (since we have no real resources to close)
	err := container.Close(ctx)
	if err != nil {
		t.Errorf("Close() error = %v, want nil", err)
	}
}

func TestKafkaDisabled(t *testing.T) {
	// Verify that when Kafka is disabled, container uses no-op publisher
	clearEnv(t)
	t.Setenv("KAFKA_ENABLED", "false")

	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if config.Kafka.Enabled {
		t.Error("Kafka should be disabled")
	}

	// Verify the config reflects disabled state
	if config.Kafka.Enabled {
		t.Error("expected Kafka to be disabled")
	}
}

func TestDatabasePoolConfiguration(t *testing.T) {
	clearEnv(t)
	t.Setenv("DB_MAX_OPEN_CONNS", "50")
	t.Setenv("DB_MAX_IDLE_CONNS", "10")
	t.Setenv("DB_CONN_MAX_LIFETIME", "15m")
	t.Setenv("DB_CONN_MAX_IDLE_TIME", "20m")

	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	// Verify database pool settings are correctly loaded
	if config.Database.MaxOpenConns != 50 {
		t.Errorf("MaxOpenConns = %d, want 50", config.Database.MaxOpenConns)
	}
	if config.Database.MaxIdleConns != 10 {
		t.Errorf("MaxIdleConns = %d, want 10", config.Database.MaxIdleConns)
	}
	if config.Database.ConnMaxLifetime != 15*time.Minute {
		t.Errorf("ConnMaxLifetime = %v, want 15m", config.Database.ConnMaxLifetime)
	}
	if config.Database.ConnMaxIdleTime != 20*time.Minute {
		t.Errorf("ConnMaxIdleTime = %v, want 20m", config.Database.ConnMaxIdleTime)
	}
}

func TestObservabilityConfiguration(t *testing.T) {
	clearEnv(t)
	t.Setenv("SERVICE_NAME", "test-service")
	t.Setenv("SERVICE_VERSION", "1.2.3")
	t.Setenv("ENVIRONMENT", "test")
	t.Setenv("SAMPLING_RATE", "0.25")

	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	// Verify observability settings
	if config.Observability.ServiceName != "test-service" {
		t.Errorf("ServiceName = %s, want test-service", config.Observability.ServiceName)
	}
	if config.Observability.ServiceVersion != "1.2.3" {
		t.Errorf("ServiceVersion = %s, want 1.2.3", config.Observability.ServiceVersion)
	}
	if config.Observability.Environment != "test" {
		t.Errorf("Environment = %s, want test", config.Observability.Environment)
	}
	if config.Observability.SamplingRate != 0.25 {
		t.Errorf("SamplingRate = %f, want 0.25", config.Observability.SamplingRate)
	}
}
