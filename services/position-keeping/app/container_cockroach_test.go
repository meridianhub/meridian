package app

import (
	"context"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cockroachDBURL spins up a CockroachDB testcontainer and returns its DSN.
// Cleanup is registered via t.Cleanup so the container is terminated automatically.
func cockroachDBURL(t *testing.T) string {
	t.Helper()

	container, cleanup := testdb.StartCockroachContainer(t, "position_keeping_test_db")
	t.Cleanup(cleanup)

	return testdb.CockroachDSN(t, container)
}

// realDBConfig builds a Config wired to a real CockroachDB DSN with all optional
// subsystems (Kafka, Redis, auth, tracing, account validation, compaction) on
// fast no-op paths so full container construction succeeds.
func realDBConfig(dbURL string) *Config {
	return &Config{
		Server: ServerConfig{
			Port:                    "50051",
			GracefulShutdownTimeout: 10 * time.Second,
		},
		Database: DatabaseConfig{
			URL:                 dbURL,
			MaxOpenConns:        5,
			MaxIdleConns:        2,
			ConnMaxLifetime:     5 * time.Minute,
			ConnMaxIdleTime:     1 * time.Minute,
			HealthCheckInterval: 30 * time.Second,
		},
		Kafka: KafkaConfig{
			Enabled: false, // no-op event publisher, no broker connection
		},
		Redis: RedisConfig{
			Enabled: false, // noop idempotency service fallback
		},
		Auth: AuthConfig{
			Enabled: false, // skip JWKS provider / interceptor
		},
		Observability: ObservabilityConfig{
			OTLPEndpoint: "", // tracing disabled
			SamplingRate: 1.0,
		},
		Compaction: CompactionConfig{
			Enabled: false, // skip compaction worker
		},
		AccountValidation: AccountValidationConfig{
			Enabled: false, // no gRPC account-service connections
		},
		ReferenceData: ReferenceDataConfig{
			ServiceURL: "", // instrument resolver disabled
		},
	}
}

// TestNewContainer_RealDB performs full container construction against a real
// CockroachDB instance and verifies the key wired dependencies are non-nil.
func TestNewContainer_RealDB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dbURL := cockroachDBURL(t)
	config := realDBConfig(dbURL)

	ctx := context.Background()
	container, err := NewContainer(ctx, config, testLogger())
	require.NoError(t, err)
	require.NotNil(t, container)
	defer func() {
		_ = container.Close(ctx)
	}()

	// Infrastructure
	assert.NotNil(t, container.DBPool, "DBPool should be initialized")

	// Repositories
	assert.NotNil(t, container.PositionLogRepository, "PositionLogRepository should be initialized")
	assert.NotNil(t, container.MeasurementRepository, "MeasurementRepository should be initialized")

	// Event publishing / outbox
	assert.NotNil(t, container.EventPublisher, "EventPublisher should be initialized")
	assert.NotNil(t, container.OutboxRepository, "OutboxRepository should be initialized")
	assert.NotNil(t, container.OutboxPublisher, "OutboxPublisher should be initialized")

	// Idempotency falls back to noop service when Redis is disabled
	assert.NotNil(t, container.IdempotencyService, "IdempotencyService should be initialized")

	// Optional subsystems are disabled in this config
	assert.Nil(t, container.AuthInterceptor, "AuthInterceptor should be nil when auth disabled")
	assert.Nil(t, container.Tracer, "Tracer should be nil when tracing disabled")
	assert.Nil(t, container.RedisClient, "RedisClient should be nil when Redis disabled")
}

// TestNewContainer_RealDB_AllSubsystemsEnabled constructs the container with the
// account-validation, reference-data, and compaction subsystems enabled. The gRPC
// account/reference-data clients connect lazily, so construction succeeds against
// unreachable URLs while exercising the validator/resolver/compaction-worker wiring.
func TestNewContainer_RealDB_AllSubsystemsEnabled(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dbURL := cockroachDBURL(t)
	config := realDBConfig(dbURL)

	// Enable account validation with both account-service URLs (lazy gRPC clients).
	config.AccountValidation = AccountValidationConfig{
		Enabled:                   true,
		CurrentAccountServiceURL:  "localhost:59001",
		InternalAccountServiceURL: "localhost:59002",
		CacheTTL:                  1 * time.Minute,
		ConnectionTimeout:         5 * time.Second,
	}
	// Enable the reference-data instrument resolver (lazy gRPC client, preload best-effort).
	config.ReferenceData = ReferenceDataConfig{
		ServiceURL: "localhost:59003",
	}
	// Enable the background compaction worker.
	config.Compaction = CompactionConfig{
		Enabled:           true,
		RunInterval:       1 * time.Minute,
		FragmentThreshold: 100,
		BatchSize:         50,
	}

	ctx := context.Background()
	container, err := NewContainer(ctx, config, testLogger())
	require.NoError(t, err)
	require.NotNil(t, container)
	defer func() {
		_ = container.Close(ctx)
	}()

	assert.NotNil(t, container.DBPool, "DBPool should be initialized")
	assert.NotNil(t, container.CompactionWorker, "CompactionWorker should be initialized when enabled")
	// Account validation + reference-data wiring appends service options.
	assert.NotEmpty(t, container.ServiceOpts, "ServiceOpts should be populated when validation/resolver enabled")
}

// TestContainer_Close_Idempotent_RealDB verifies that Close can be safely called
// multiple times on a fully constructed container without error or panic.
func TestContainer_Close_Idempotent_RealDB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dbURL := cockroachDBURL(t)
	config := realDBConfig(dbURL)

	ctx := context.Background()
	container, err := NewContainer(ctx, config, testLogger())
	require.NoError(t, err)
	require.NotNil(t, container)

	// First close releases the DB pool and other resources.
	require.NoError(t, container.Close(ctx), "first Close() should not error")

	// Second close must be a safe no-op (all resources already released).
	require.NoError(t, container.Close(ctx), "second Close() should not error")
}
