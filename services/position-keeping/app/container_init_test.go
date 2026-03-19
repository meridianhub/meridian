package app

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))
}

func TestContainer_InitializeTracer_Disabled(t *testing.T) {
	c := &Container{
		Config: &Config{
			Observability: ObservabilityConfig{
				OTLPEndpoint: "",
			},
		},
		Logger: testLogger(),
	}

	err := c.initializeTracer(context.Background())
	require.NoError(t, err)
	assert.Nil(t, c.Tracer)
}

func TestContainer_InitializeAuth_Disabled(t *testing.T) {
	c := &Container{
		Config: &Config{
			Auth: AuthConfig{
				Enabled: false,
			},
		},
		Logger: testLogger(),
	}

	err := c.initializeAuth(context.Background())
	require.NoError(t, err)
	assert.Nil(t, c.AuthInterceptor)
}

func TestContainer_InitializeAuth_Enabled_InvalidJWKS(t *testing.T) {
	c := &Container{
		Config: &Config{
			Auth: AuthConfig{
				Enabled: true,
				JWKSURL: "", // empty URL will fail
			},
		},
		Logger: testLogger(),
	}

	err := c.initializeAuth(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "JWKS provider")
	assert.Nil(t, c.AuthInterceptor)
}

func TestContainer_InitializeTracer_InvalidEndpoint(t *testing.T) {
	c := &Container{
		Config: &Config{
			Observability: ObservabilityConfig{
				OTLPEndpoint:   "http://invalid-otlp:4317",
				ServiceName:    "test-service",
				ServiceVersion: "0.0.1",
				Environment:    "test",
				SamplingRate:   1.0,
			},
		},
		Logger: testLogger(),
	}

	// Tracer initialization may succeed even with invalid endpoint
	// (connection is lazy). This exercises the non-empty OTLP path.
	err := c.initializeTracer(context.Background())
	if err != nil {
		assert.Contains(t, err.Error(), "tracer")
	} else {
		assert.NotNil(t, c.Tracer)
		if c.Tracer != nil {
			_ = c.Tracer.Shutdown(context.Background())
		}
	}
}

func TestContainer_InitializeEventPublisher_KafkaEnabled_InvalidBrokers(t *testing.T) {
	c := &Container{
		Config: &Config{
			Kafka: KafkaConfig{
				Enabled: true,
				Brokers: []string{"invalid-broker:9999"},
			},
		},
		Logger: testLogger(),
	}

	c.initializeEventPublisher()
	// Should fallback to NoOp on producer creation failure
	require.NotNil(t, c.EventPublisher)
	_, isNoOp := c.EventPublisher.(*domain.NoOpEventPublisher)
	assert.True(t, isNoOp, "expected NoOp publisher when kafka broker is invalid")
}

func TestContainer_InitializeAuditPublisher_KafkaDisabled(t *testing.T) {
	c := &Container{
		Config: &Config{
			Kafka: KafkaConfig{
				Enabled: false,
			},
		},
		Logger: testLogger(),
	}

	c.initializeAuditPublisher()
	assert.Nil(t, c.auditPublisher)
}

func TestContainer_InitializeAuditPublisher_KafkaEnabled_InvalidBrokers(t *testing.T) {
	c := &Container{
		Config: &Config{
			Kafka: KafkaConfig{
				Enabled: true,
				Brokers: []string{"invalid-broker:9999"},
			},
		},
		Logger: testLogger(),
	}

	c.initializeAuditPublisher()
	// Invalid broker may or may not fail — audit publisher degrades gracefully
	// We just verify initialization doesn't panic
	t.Log("audit publisher initialized without panic")
}

func TestContainer_InitializeEventPublisher_KafkaDisabled(t *testing.T) {
	c := &Container{
		Config: &Config{
			Kafka: KafkaConfig{
				Enabled: false,
			},
		},
		Logger: testLogger(),
	}

	c.initializeEventPublisher()
	assert.NotNil(t, c.EventPublisher)
	_, ok := c.EventPublisher.(*domain.NoOpEventPublisher)
	assert.True(t, ok)
}

func TestContainer_InitializeRepositories_WithNilPool(t *testing.T) {
	// initializeRepositories just wraps the pool — nil pool is valid at construction time
	c := &Container{
		Config: &Config{},
		Logger: testLogger(),
		DBPool: nil,
	}

	c.initializeRepositories()
	assert.NotNil(t, c.PositionLogRepository)
	assert.NotNil(t, c.MeasurementRepository)
}

func TestContainer_InitializeOutboxRepository_WithNilPool(t *testing.T) {
	c := &Container{
		Config: &Config{},
		Logger: testLogger(),
		DBPool: nil,
	}

	c.initializeOutboxRepository()
	assert.NotNil(t, c.OutboxRepository)
}

func TestContainer_InitializeOutboxPublisher_WithRepo(t *testing.T) {
	c := &Container{
		Config:           &Config{},
		Logger:           testLogger(),
		OutboxRepository: events.NewPgxOutboxRepository(nil),
	}

	c.initializeOutboxPublisher()
	assert.NotNil(t, c.OutboxPublisher)
}

func TestContainer_InitializeOutboxPublisher_NilRepo(t *testing.T) {
	c := &Container{
		Config:           &Config{},
		Logger:           testLogger(),
		OutboxRepository: nil,
	}

	c.initializeOutboxPublisher()
	assert.Nil(t, c.OutboxPublisher)
}

func TestContainer_KafkaProducer_Nil(t *testing.T) {
	c := &Container{}
	assert.Nil(t, c.KafkaProducer())
}

func TestContainer_Close_WithAllNilResources(t *testing.T) {
	c := &Container{
		Logger: testLogger(),
	}
	err := c.Close(context.Background())
	assert.NoError(t, err)
}

func TestContainer_NewContainer_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()

	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("test_pk_app"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second)),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, pgContainer.Terminate(ctx))
	})

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	// Verify pool works before handing to container
	pool, err := pgxpool.New(ctx, connStr)
	require.NoError(t, err)
	pool.Close()

	config := &Config{
		Server: ServerConfig{
			Port:                    "50051",
			GracefulShutdownTimeout: 10 * time.Second,
		},
		Database: DatabaseConfig{
			URL:                 connStr,
			MaxOpenConns:        5,
			MaxIdleConns:        2,
			ConnMaxLifetime:     5 * time.Minute,
			ConnMaxIdleTime:     1 * time.Minute,
			HealthCheckInterval: 30 * time.Second,
		},
		Kafka: KafkaConfig{
			Enabled: false,
		},
		Redis: RedisConfig{
			Enabled: false,
		},
		Auth: AuthConfig{
			Enabled: false,
		},
		Observability: ObservabilityConfig{
			OTLPEndpoint: "",
			SamplingRate: 1.0,
		},
	}

	container, err := NewContainer(ctx, config, testLogger())
	require.NoError(t, err)
	require.NotNil(t, container)

	assert.NotNil(t, container.DBPool)
	assert.NotNil(t, container.PositionLogRepository)
	assert.NotNil(t, container.MeasurementRepository)
	assert.NotNil(t, container.EventPublisher)
	assert.NotNil(t, container.OutboxRepository)
	assert.Nil(t, container.AuthInterceptor)
	assert.Nil(t, container.Tracer)
	assert.Nil(t, container.RedisClient)

	err = container.Close(context.Background())
	require.NoError(t, err)
}

func TestContainer_Close_WithDBPool(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()

	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("test_close"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second)),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = pgContainer.Terminate(ctx)
	})

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	pool, err := pgxpool.New(ctx, connStr)
	require.NoError(t, err)

	c := &Container{
		Logger: testLogger(),
		DBPool: pool,
	}

	err = c.Close(context.Background())
	assert.NoError(t, err)
}

func TestContainer_InitializeDatabase_InvalidURL(t *testing.T) {
	c := &Container{
		Config: &Config{
			Database: DatabaseConfig{
				URL: "not-a-valid-url",
			},
		},
		Logger: testLogger(),
	}

	err := c.initializeDatabase(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse database URL")
}

func TestContainer_InitializeDatabase_UnreachableHost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	c := &Container{
		Config: &Config{
			Database: DatabaseConfig{
				URL:                 "postgres://test:test@198.51.100.1:5432/testdb?connect_timeout=1",
				MaxOpenConns:        5,
				MaxIdleConns:        2,
				ConnMaxLifetime:     5 * time.Minute,
				ConnMaxIdleTime:     1 * time.Minute,
				HealthCheckInterval: 30 * time.Second,
			},
		},
		Logger: testLogger(),
	}

	err := c.initializeDatabase(ctx)
	assert.Error(t, err)
	// Should fail at pool creation or ping
}
