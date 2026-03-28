package app

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestContainer_Close_NilResources(t *testing.T) {
	c := &Container{Logger: testLogger()}
	assert.NotPanics(t, func() { c.Close() })
}

func TestContainer_Close_Idempotent(t *testing.T) {
	c := &Container{Logger: testLogger()}
	assert.NotPanics(t, func() {
		c.Close()
		c.Close()
	})
}

func TestContainer_Close_WithCleanups(t *testing.T) {
	c := &Container{Logger: testLogger()}

	var order []int
	c.cleanups = append(c.cleanups, func() { order = append(order, 1) })
	c.cleanups = append(c.cleanups, func() { order = append(order, 2) })
	c.cleanups = append(c.cleanups, func() { order = append(order, 3) })

	c.Close()

	require.Equal(t, []int{3, 2, 1}, order, "cleanups should be called in reverse order")
}

func TestContainer_initRepositories(t *testing.T) {
	c := &Container{Logger: testLogger(), DB: nil}
	c.initRepositories()
	assert.NotNil(t, c.LedgerRepo, "LedgerRepo should be non-nil even with nil DB")
}

func TestContainer_initKafkaProducer_NoServers_NonProduction(t *testing.T) {
	t.Setenv("KAFKA_BOOTSTRAP_SERVERS", "")
	t.Setenv("ENVIRONMENT", "development")

	c := &Container{Logger: testLogger()}
	err := c.initKafkaProducer()

	require.NoError(t, err)
	assert.True(t, c.UsingNoopEventPublisher, "should fall back to noop when Kafka unavailable in non-production")
}

func TestContainer_initEventPublisher_NoopMode(t *testing.T) {
	t.Setenv("ENVIRONMENT", "development")

	c := &Container{Logger: testLogger(), UsingNoopEventPublisher: true}
	c.initEventPublisher()

	assert.NotNil(t, c.EventPublisher)
}

func TestContainer_initEventPublisher_NormalMode(t *testing.T) {
	t.Setenv("ENVIRONMENT", "development")

	c := &Container{Logger: testLogger(), UsingNoopEventPublisher: false}
	c.initEventPublisher()

	assert.NotNil(t, c.EventPublisher)
}

func TestContainer_initBankCashAccount_Missing(t *testing.T) {
	t.Setenv("BANK_CASH_ACCOUNT_ID", "")

	c := &Container{Logger: testLogger()}
	err := c.initBankCashAccount()

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrBankCashAccountIDRequired), "error should wrap ErrBankCashAccountIDRequired")
}

func TestContainer_initBankCashAccount_InvalidUUID(t *testing.T) {
	t.Setenv("BANK_CASH_ACCOUNT_ID", "not-a-uuid")

	c := &Container{Logger: testLogger()}
	err := c.initBankCashAccount()

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrBankCashAccountIDInvalid), "error should wrap ErrBankCashAccountIDInvalid")
}

func TestContainer_initBankCashAccount_Valid(t *testing.T) {
	validUUID := uuid.New().String()
	t.Setenv("BANK_CASH_ACCOUNT_ID", validUUID)

	c := &Container{Logger: testLogger()}
	c.initRepositories()

	err := c.initBankCashAccount()

	require.NoError(t, err)
	assert.NotNil(t, c.PostingService, "PostingService should be created with valid UUID")
}

func TestContainer_initAuth_Disabled(t *testing.T) {
	t.Setenv("AUTH_ENABLED", "false")

	c := &Container{Logger: testLogger()}
	err := c.initAuth(context.Background())

	require.NoError(t, err)
	assert.Nil(t, c.AuthInterceptor, "AuthInterceptor should be nil when auth is disabled")
}

func TestContainer_initRedis_FallbackToNoop(t *testing.T) {
	t.Setenv("ENVIRONMENT", "development")
	t.Setenv("REDIS_URL", "redis://localhost:1") // unreachable port
	t.Setenv("IDEMPOTENCY_CLEANUP_ENABLED", "false")

	c := &Container{Logger: testLogger()}
	err := c.initRedis(context.Background())

	require.NoError(t, err)
	assert.True(t, c.UsingNoopIdempotency, "should fall back to noop idempotency in non-production")
	assert.NotNil(t, c.IdempotencyService, "IdempotencyService should be set to noop")
}

func TestContainer_initAuditPublisher_NoKafka(t *testing.T) {
	t.Setenv("KAFKA_BOOTSTRAP_SERVERS", "")

	c := &Container{Logger: testLogger()}
	assert.NotPanics(t, func() { c.initAuditPublisher() })
	assert.Nil(t, c.auditPublisher, "auditPublisher should be nil when Kafka not configured")
}

func TestNoopEventPublisher(t *testing.T) {
	p := &NoopEventPublisher{}

	assert.NoError(t, p.Publish(context.Background(), nil))
	assert.NoError(t, p.PublishBatch(context.Background(), nil))
}
