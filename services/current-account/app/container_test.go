package app

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestContainer_Close_NilResources(t *testing.T) {
	c := &Container{
		Logger: testLogger(),
	}

	assert.NotPanics(t, func() {
		c.Close()
	})
}

func TestContainer_Close_Idempotent(t *testing.T) {
	c := &Container{
		Logger: testLogger(),
	}

	assert.NotPanics(t, func() {
		c.Close()
		c.Close()
	})
}

func TestContainer_Close_WithCleanups(t *testing.T) {
	var callOrder []int

	c := &Container{
		Logger: testLogger(),
		cleanups: []func(){
			func() { callOrder = append(callOrder, 1) },
			func() { callOrder = append(callOrder, 2) },
			func() { callOrder = append(callOrder, 3) },
		},
	}

	c.Close()

	require.Len(t, callOrder, 3)
	assert.Equal(t, []int{3, 2, 1}, callOrder, "cleanups should run in reverse order")
}

func TestContainer_initRepositories_WithNilDB(t *testing.T) {
	c := &Container{
		Logger: testLogger(),
		DB:     nil,
	}

	c.initRepositories()

	assert.NotNil(t, c.AccountRepo, "AccountRepo should be non-nil even with nil DB")
	assert.NotNil(t, c.LienRepo, "LienRepo should be non-nil even with nil DB")
	assert.NotNil(t, c.WithdrawalRepo, "WithdrawalRepo should be non-nil even with nil DB")
	assert.NotNil(t, c.OutboxRepo, "OutboxRepo should be non-nil even with nil DB")
}

func TestContainer_initRedis_FallbackToNoop(t *testing.T) {
	t.Setenv("REDIS_URL", "redis://localhost:1") // unreachable port
	t.Setenv("ENVIRONMENT", "development")

	c := &Container{
		Logger: testLogger(),
	}

	err := c.initRedis(context.Background())

	require.NoError(t, err)
	assert.True(t, c.UsingNoopIdempotency, "should fall back to noop idempotency")
	assert.NotNil(t, c.IdempotencyService, "IdempotencyService should be non-nil (noop)")
	assert.Nil(t, c.RedisClient, "RedisClient should be nil when using noop fallback")
}

func TestContainer_initAuth_Disabled(t *testing.T) {
	t.Setenv("AUTH_ENABLED", "false")

	c := &Container{
		Logger: testLogger(),
	}

	err := c.initAuth(context.Background())

	require.NoError(t, err)
	assert.Nil(t, c.AuthInterceptor, "AuthInterceptor should be nil when auth is disabled")
}

func TestContainer_initTracer_Disabled(t *testing.T) {
	t.Setenv("OTEL_TRACES_ENABLED", "false")

	c := &Container{
		Logger: testLogger(),
	}

	err := c.initTracer(context.Background(), "test-version")

	require.NoError(t, err)
}
