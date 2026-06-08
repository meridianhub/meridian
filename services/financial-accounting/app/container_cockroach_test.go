package app

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/shared/platform/testdb"
)

// configureContainerEnv sets the environment toggles that steer NewContainer onto
// the fast, network-free initialization paths suitable for an in-process smoke test:
//   - DATABASE_URL points at the CockroachDB testcontainer.
//   - ENVIRONMENT=development enables degraded fallbacks instead of failing fast.
//   - KAFKA_BOOTSTRAP_SERVERS empty -> noop event publisher, no audit Kafka publisher.
//   - REDIS_URL unreachable + cleanup disabled -> noop idempotency, no cleanup worker.
//   - AUTH_ENABLED=false -> nil auth interceptor (no JWKS fetch).
//   - OTEL_TRACES_ENABLED=false -> no OTLP exporter connection.
//   - BANK_CASH_ACCOUNT_ID valid UUID -> posting service constructs successfully.
func configureContainerEnv(t *testing.T, dbURL string) {
	t.Helper()

	t.Setenv("DATABASE_URL", dbURL)
	t.Setenv("ENVIRONMENT", "development")
	t.Setenv("KAFKA_BOOTSTRAP_SERVERS", "")
	t.Setenv("REDIS_URL", "redis://localhost:1") // unreachable port -> noop fallback
	t.Setenv("IDEMPOTENCY_CLEANUP_ENABLED", "false")
	t.Setenv("AUTH_ENABLED", "false")
	t.Setenv("OTEL_TRACES_ENABLED", "false")
	t.Setenv("REFERENCE_DATA_SERVICE_URL", "")
	t.Setenv("BANK_CASH_ACCOUNT_ID", uuid.New().String())
}

// newRealContainer starts a CockroachDB testcontainer, configures the environment,
// and constructs a real Container via NewContainer.
func newRealContainer(t *testing.T) *Container {
	t.Helper()

	container, cleanup := testdb.StartCockroachContainer(t, "financial_accounting_test_db")
	t.Cleanup(cleanup)

	dbURL := testdb.CockroachDSN(t, container)
	configureContainerEnv(t, dbURL)

	c, err := NewContainer(context.Background(), testLogger(), "test")
	require.NoError(t, err, "NewContainer() should succeed against the CockroachDB testcontainer")
	require.NotNil(t, c)

	return c
}

// TestNewContainer_RealDB exercises full container construction against a real
// CockroachDB instance, asserting that the key dependencies were wired up.
func TestNewContainer_RealDB(t *testing.T) {
	c := newRealContainer(t)
	defer c.Close()

	assert.NotNil(t, c.Logger, "Logger should be set")
	assert.NotNil(t, c.Tracer, "Tracer should be initialized")
	assert.NotNil(t, c.DB, "DB should be connected")
	assert.NotNil(t, c.LedgerRepo, "LedgerRepo should be initialized")
	assert.NotNil(t, c.OutboxRepo, "OutboxRepo should be initialized")
	assert.NotNil(t, c.OutboxPublisher, "OutboxPublisher should be initialized")
	assert.NotNil(t, c.EventPublisher, "EventPublisher should be initialized")
	assert.NotNil(t, c.PostingService, "PostingService should be created")
	assert.NotNil(t, c.IdempotencyService, "IdempotencyService should be set")

	// Development env with empty Kafka / unreachable Redis must take the noop paths.
	assert.True(t, c.UsingNoopEventPublisher, "should use noop event publisher when Kafka unset")
	assert.True(t, c.UsingNoopIdempotency, "should use noop idempotency when Redis unreachable")

	// AUTH_ENABLED=false yields no interceptor.
	assert.Nil(t, c.AuthInterceptor, "AuthInterceptor should be nil when auth disabled")
}

// TestContainer_Close_Idempotent_RealDB verifies that closing a fully-constructed
// container twice is safe (no error, no panic).
func TestContainer_Close_Idempotent_RealDB(t *testing.T) {
	c := newRealContainer(t)

	assert.NotPanics(t, func() {
		c.Close()
		c.Close()
	}, "Close should be idempotent on a real container")
}
