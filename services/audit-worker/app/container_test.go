package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"
)

func TestNewContainer(t *testing.T) {
	// Skip integration tests in short mode
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Require explicit opt-in for integration tests
	if os.Getenv("INTEGRATION_TESTS") != "1" {
		t.Skip("INTEGRATION_TESTS=1 not set, skipping integration test")
	}

	// Check if DATABASE_URL is set for integration testing
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set, skipping integration test")
	}

	// Check if Kafka is available for integration testing
	kafkaServers := os.Getenv("KAFKA_BOOTSTRAP_SERVERS")
	if kafkaServers == "" {
		t.Skip("KAFKA_BOOTSTRAP_SERVERS not set, skipping integration test")
	}

	tests := []struct {
		name    string
		config  *Config
		wantErr bool
	}{
		{
			name: "valid configuration with real database and kafka",
			config: &Config{
				Service: ServiceConfig{
					Name:                    "test-service",
					Port:                    "8080",
					GracefulShutdownTimeout: 30 * time.Second,
				},
				Database: DatabaseConfig{
					URL:             dbURL,
					MaxOpenConns:    5,
					MaxIdleConns:    2,
					ConnMaxLifetime: 5 * time.Minute,
					ConnMaxIdleTime: 10 * time.Minute,
				},
				Kafka: KafkaConfig{
					BootstrapServers: kafkaServers,
					Topic:            "audit.events.test.v1",
					GroupID:          "test-consumer-group",
					ClientID:         "test-consumer",
					HandlerTimeout:   30 * time.Second,
					MaxRetries:       3,
				},
			},
			wantErr: false,
		},
		{
			name: "invalid database URL",
			config: &Config{
				Service: ServiceConfig{
					Name:                    "test-service",
					Port:                    "8080",
					GracefulShutdownTimeout: 30 * time.Second,
				},
				Database: DatabaseConfig{
					URL:             "invalid://connection/string",
					MaxOpenConns:    5,
					MaxIdleConns:    2,
					ConnMaxLifetime: 5 * time.Minute,
					ConnMaxIdleTime: 10 * time.Minute,
				},
				Kafka: KafkaConfig{
					BootstrapServers: kafkaServers,
					Topic:            "audit.events.test.v1",
					GroupID:          "test-consumer-group",
					ClientID:         "test-consumer",
					HandlerTimeout:   30 * time.Second,
					MaxRetries:       3,
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
				Level: slog.LevelInfo,
			}))

			ctx := context.Background()
			container, err := NewContainer(ctx, tt.config, logger)

			if (err != nil) != tt.wantErr {
				t.Errorf("NewContainer() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			// Clean up if container was created
			if container != nil {
				defer func() {
					if closeErr := container.Close(ctx); closeErr != nil {
						t.Logf("Failed to close container: %v", closeErr)
					}
				}()

				// Verify components were initialized
				if container.DB == nil {
					t.Error("NewContainer() DB is nil")
				}
				if container.AuditConsumer == nil {
					t.Error("NewContainer() AuditConsumer is nil")
				}
			}
		})
	}
}

func TestContainer_Close(t *testing.T) {
	// Skip integration tests in short mode
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Require explicit opt-in for integration tests
	if os.Getenv("INTEGRATION_TESTS") != "1" {
		t.Skip("INTEGRATION_TESTS=1 not set, skipping integration test")
	}

	// Check if DATABASE_URL is set for integration testing
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set, skipping integration test")
	}

	// Check if Kafka is available for integration testing
	kafkaServers := os.Getenv("KAFKA_BOOTSTRAP_SERVERS")
	if kafkaServers == "" {
		t.Skip("KAFKA_BOOTSTRAP_SERVERS not set, skipping integration test")
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	config := &Config{
		Service: ServiceConfig{
			Name:                    "test-service",
			Port:                    "8080",
			GracefulShutdownTimeout: 30 * time.Second,
		},
		Database: DatabaseConfig{
			URL:             dbURL,
			MaxOpenConns:    5,
			MaxIdleConns:    2,
			ConnMaxLifetime: 5 * time.Minute,
			ConnMaxIdleTime: 10 * time.Minute,
		},
		Kafka: KafkaConfig{
			BootstrapServers: kafkaServers,
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
		t.Fatalf("Failed to create container: %v", err)
	}

	// Test that Close succeeds
	err = container.Close(ctx)
	if err != nil {
		t.Errorf("Container.Close() error = %v, want nil", err)
	}

	// Test that Close is idempotent
	err = container.Close(ctx)
	if err != nil {
		t.Errorf("Container.Close() second call error = %v, want nil", err)
	}
}

func TestContainer_DatabaseConnection(t *testing.T) {
	// Skip integration tests in short mode
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Require explicit opt-in for integration tests
	if os.Getenv("INTEGRATION_TESTS") != "1" {
		t.Skip("INTEGRATION_TESTS=1 not set, skipping integration test")
	}

	// Check if DATABASE_URL is set for integration testing
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set, skipping integration test")
	}

	// Check if Kafka is available for integration testing
	kafkaServers := os.Getenv("KAFKA_BOOTSTRAP_SERVERS")
	if kafkaServers == "" {
		t.Skip("KAFKA_BOOTSTRAP_SERVERS not set, skipping integration test")
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	config := &Config{
		Service: ServiceConfig{
			Name:                    "test-service",
			Port:                    "8080",
			GracefulShutdownTimeout: 30 * time.Second,
		},
		Database: DatabaseConfig{
			URL:             dbURL,
			MaxOpenConns:    10,
			MaxIdleConns:    3,
			ConnMaxLifetime: 5 * time.Minute,
			ConnMaxIdleTime: 10 * time.Minute,
		},
		Kafka: KafkaConfig{
			BootstrapServers: kafkaServers,
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
		t.Fatalf("Failed to create container: %v", err)
	}
	defer func() {
		if closeErr := container.Close(ctx); closeErr != nil {
			t.Logf("Failed to close container: %v", closeErr)
		}
	}()

	// Verify database connection pool settings
	sqlDB, err := container.DB.DB()
	if err != nil {
		t.Fatalf("Failed to get database instance: %v", err)
	}

	stats := sqlDB.Stats()
	if stats.MaxOpenConnections != 10 {
		t.Errorf("MaxOpenConnections = %d, want 10", stats.MaxOpenConnections)
	}

	// Test database connection with a simple query
	var result int
	err = container.DB.Raw("SELECT 1").Scan(&result).Error
	if err != nil {
		t.Errorf("Database query failed: %v", err)
	}
	if result != 1 {
		t.Errorf("Database query result = %d, want 1", result)
	}
}

func TestContainerCloseError_Error(t *testing.T) {
	err := &ContainerCloseError{
		Errors: []error{
			fmt.Errorf("error 1"),
			fmt.Errorf("error 2"),
		},
	}

	msg := err.Error()
	if msg != "errors during container close: 2 errors" {
		t.Errorf("ContainerCloseError.Error() = %q, unexpected", msg)
	}
}

func TestContainer_Close_EmptyContainer(t *testing.T) {
	// Close a container with no DB or AuditConsumer initialized.
	// This covers the nil-check branches in Close().
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	c := &Container{
		Config: &Config{
			Service: ServiceConfig{Name: "test"},
		},
		Logger:    logger,
		done:      make(chan struct{}),
		closeOnce: sync.Once{},
		// DB and AuditConsumer intentionally nil
	}

	ctx := context.Background()
	err := c.Close(ctx)
	if err != nil {
		t.Errorf("Close() on empty container returned error: %v", err)
	}
}

func TestContainer_Close_Idempotent(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	c := &Container{
		Config: &Config{
			Service: ServiceConfig{Name: "test"},
		},
		Logger:    logger,
		done:      make(chan struct{}),
		closeOnce: sync.Once{},
	}

	ctx := context.Background()
	// Should not panic on multiple calls
	_ = c.Close(ctx)
	err := c.Close(ctx)
	if err != nil {
		t.Errorf("second Close() returned error: %v", err)
	}
}
