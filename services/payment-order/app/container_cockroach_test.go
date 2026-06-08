package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/payment-order/config"
	"github.com/meridianhub/meridian/shared/platform/testdb"
)

// setupCockroachDBURL starts a CockroachDB testcontainer and returns its DSN.
// The container is torn down automatically via t.Cleanup.
func setupCockroachDBURL(t *testing.T) string {
	t.Helper()

	container, cleanup := testdb.StartCockroachContainer(t, "payment_order_test_db")
	t.Cleanup(cleanup)

	return testdb.CockroachDSN(t, container)
}

// realDBServiceConfig returns a ServiceConfig wired for the mock gateway and
// billing disabled, so NewContainer reaches success without external services.
func realDBServiceConfig() *config.ServiceConfig {
	return &config.ServiceConfig{
		PaymentGatewayProvider: "mock",
		BillingEnabled:         false,
	}
}

// setRealDBContainerEnv configures the environment so NewContainer takes the
// fast/no-op paths for every optional dependency: real CockroachDB for the DB,
// no Kafka, no auth, no tracing, noop idempotency (unreachable Redis), and the
// mock payment gateway account mapping.
func setRealDBContainerEnv(t *testing.T, dbURL string) {
	t.Helper()

	t.Setenv("DATABASE_URL", dbURL)
	t.Setenv("ENVIRONMENT", "development")
	t.Setenv("KAFKA_BOOTSTRAP_SERVERS", "")
	t.Setenv("KAFKA_BROKERS", "")
	t.Setenv("AUTH_ENABLED", "false")
	t.Setenv("OTEL_TRACES_ENABLED", "false")
	t.Setenv("REDIS_URL", "redis://localhost:1") // unreachable -> noop idempotency fallback
	t.Setenv("GATEWAY_MOCK_ACCOUNT_ID", "00000000-0000-0000-0000-000000000001")
	t.Setenv("GATEWAY_MOCK_ACCOUNT_TYPE", "NOSTRO")
}

// TestNewContainer_RealDB exercises the full NewContainer construction path
// against a real CockroachDB testcontainer, asserting key dependencies are wired.
func TestNewContainer_RealDB(t *testing.T) {
	dbURL := setupCockroachDBURL(t)
	setRealDBContainerEnv(t, dbURL)

	ctx := context.Background()
	c, err := NewContainer(ctx, realDBServiceConfig(), testLogger(), "test-version")
	require.NoError(t, err)
	require.NotNil(t, c)
	defer c.Close()

	// Infrastructure
	assert.NotNil(t, c.DB, "DB should be wired from the testcontainer")
	assert.NotNil(t, c.Tracer, "Tracer should be created (noop when tracing disabled)")

	// Repositories
	assert.NotNil(t, c.PaymentOrderRepo)
	assert.NotNil(t, c.BillingRepo)
	assert.NotNil(t, c.SagaExecutionRepo)

	// Payment gateway
	assert.NotNil(t, c.PaymentGateway, "mock payment gateway should be wired")
	assert.NotNil(t, c.GatewayAccountConfig)

	// Messaging (Kafka disabled -> outbox-only)
	assert.NotNil(t, c.OutboxRepo)
	assert.NotNil(t, c.OutboxPublisher)
	assert.NotNil(t, c.EventPublisher)
	assert.Nil(t, c.kafkaProducer, "kafka producer should be nil without bootstrap servers")
	assert.Empty(t, c.BootstrapServers)

	// Idempotency falls back to noop when Redis is unreachable in development.
	assert.NotNil(t, c.IdempotencyService)
	assert.Nil(t, c.RedisClient, "RedisClient should be nil when Redis is unreachable")

	// Starlark handler registry
	assert.NotNil(t, c.HandlerRegistry)

	// Auth disabled -> no interceptor.
	assert.Nil(t, c.AuthInterceptor, "AuthInterceptor should be nil when auth disabled")

	// Billing disabled -> no billing workers.
	assert.Nil(t, c.BillingCronScheduler)
	assert.Nil(t, c.DunningWorker)
}

// TestContainer_Close_Idempotent_RealDB verifies Close is safe to call multiple
// times after a full real-DB construction.
func TestContainer_Close_Idempotent_RealDB(t *testing.T) {
	dbURL := setupCockroachDBURL(t)
	setRealDBContainerEnv(t, dbURL)

	ctx := context.Background()
	c, err := NewContainer(ctx, realDBServiceConfig(), testLogger(), "test-version")
	require.NoError(t, err)
	require.NotNil(t, c)

	assert.NotPanics(t, func() {
		c.Close()
		c.Close()
	}, "Close should be idempotent and not panic on repeated calls")
}
