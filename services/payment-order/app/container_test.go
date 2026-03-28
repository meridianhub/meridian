package app

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/payment-order/config"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func testServiceConfig() *config.ServiceConfig {
	return &config.ServiceConfig{
		PaymentGatewayProvider: "mock",
	}
}

func TestContainer_Close_NilResources(t *testing.T) {
	c := &Container{
		Config: testServiceConfig(),
		Logger: testLogger(),
	}

	assert.NotPanics(t, func() {
		c.Close()
	})
}

func TestContainer_Close_Idempotent(t *testing.T) {
	c := &Container{
		Config: testServiceConfig(),
		Logger: testLogger(),
	}

	assert.NotPanics(t, func() {
		c.Close()
		c.Close()
	})
}

func TestContainer_Close_WithCleanups(t *testing.T) {
	c := &Container{
		Config: testServiceConfig(),
		Logger: testLogger(),
	}

	var order []int
	c.cleanups = append(c.cleanups, func() { order = append(order, 1) })
	c.cleanups = append(c.cleanups, func() { order = append(order, 2) })
	c.cleanups = append(c.cleanups, func() { order = append(order, 3) })

	c.Close()

	require.Len(t, order, 3)
	assert.Equal(t, []int{3, 2, 1}, order, "cleanups should execute in reverse order")
}

func TestContainer_initRepositories_WithNilDB(t *testing.T) {
	c := &Container{
		Config: testServiceConfig(),
		Logger: testLogger(),
		DB:     nil,
	}

	assert.NotPanics(t, func() {
		c.initRepositories()
	})

	assert.NotNil(t, c.PaymentOrderRepo)
	assert.NotNil(t, c.BillingRepo)
	assert.NotNil(t, c.SagaExecutionRepo)
}

func TestContainer_initKafka_NoBootstrapServers(t *testing.T) {
	t.Setenv("KAFKA_BOOTSTRAP_SERVERS", "")
	t.Setenv("KAFKA_BROKERS", "")
	t.Setenv("ENVIRONMENT", "development")

	c := &Container{
		Config: testServiceConfig(),
		Logger: testLogger(),
	}

	c.initKafka(context.Background())

	assert.Nil(t, c.kafkaProducer)
	assert.Empty(t, c.BootstrapServers)
	// OutboxRepo and OutboxPublisher should still be created
	assert.NotNil(t, c.OutboxRepo)
	assert.NotNil(t, c.OutboxPublisher)
	assert.NotNil(t, c.EventPublisher)
}

func TestContainer_initRedis_FallbackToNoop(t *testing.T) {
	t.Setenv("ENVIRONMENT", "development")
	t.Setenv("REDIS_URL", "redis://localhost:1") // unreachable port

	c := &Container{
		Config: testServiceConfig(),
		Logger: testLogger(),
	}

	err := c.initRedis(context.Background())

	require.NoError(t, err)
	assert.NotNil(t, c.IdempotencyService, "should fall back to noop idempotency service")
	assert.Nil(t, c.RedisClient, "RedisClient should be nil when connection fails")
}

func TestContainer_initAuth_Disabled(t *testing.T) {
	t.Setenv("AUTH_ENABLED", "false")

	c := &Container{
		Config: testServiceConfig(),
		Logger: testLogger(),
	}

	err := c.initAuth(context.Background())

	require.NoError(t, err)
	assert.Nil(t, c.AuthInterceptor, "AuthInterceptor should be nil when auth disabled")
}

func TestContainer_initTracer_Disabled(t *testing.T) {
	t.Setenv("OTEL_TRACES_ENABLED", "false")

	c := &Container{
		Config: testServiceConfig(),
		Logger: testLogger(),
	}

	err := c.initTracer(context.Background(), "test-version")

	require.NoError(t, err)
	assert.NotNil(t, c.Tracer, "tracer should be created even when disabled (noop)")
}

func TestContainer_initPaymentGateway_Mock(t *testing.T) {
	// initPaymentGateway also calls createGatewayAccountConfig which needs env vars
	t.Setenv("GATEWAY_MOCK_ACCOUNT_ID", "00000000-0000-0000-0000-000000000001")
	t.Setenv("GATEWAY_MOCK_ACCOUNT_TYPE", "NOSTRO")

	c := &Container{
		Config: testServiceConfig(),
		Logger: testLogger(),
	}

	err := c.initPaymentGateway()

	require.NoError(t, err)
	assert.NotNil(t, c.PaymentGateway, "PaymentGateway should be non-nil for mock provider")
}

func TestContainer_initBillingWorkers_Disabled(t *testing.T) {
	c := &Container{
		Config: &config.ServiceConfig{
			PaymentGatewayProvider: "mock",
			BillingEnabled:         false,
		},
		Logger: testLogger(),
	}

	err := c.initBillingWorkers(context.Background())

	require.NoError(t, err)
	assert.Nil(t, c.BillingCronScheduler, "BillingCronScheduler should be nil when billing disabled")
	assert.Nil(t, c.DunningWorker, "DunningWorker should be nil when billing disabled")
}

func TestContainer_initHandlerRegistry(t *testing.T) {
	c := &Container{
		Config: testServiceConfig(),
		Logger: testLogger(),
	}

	assert.NotPanics(t, func() {
		c.initHandlerRegistry(context.Background())
	})

	assert.NotNil(t, c.HandlerRegistry, "HandlerRegistry should be non-nil after init")
}
