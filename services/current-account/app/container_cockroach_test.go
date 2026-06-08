package app

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/shared/platform/testdb"
)

// repoRoot returns the absolute path to the module root, derived from this test
// file's location (services/current-account/app/), so that saga asset loading
// works regardless of the test's working directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller should resolve this test file's path")
	// thisFile = <root>/services/current-account/app/container_cockroach_test.go
	// filepath.Dir(thisFile) = <root>/services/current-account/app
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
}

// setupContainerEnv configures the environment so that NewContainer constructs
// successfully against a real CockroachDB testcontainer while taking the fast,
// no-op paths for everything else (Redis fallback, auth disabled, tracing
// disabled, no Kafka). It returns the CockroachDB DSN that was wired into
// DATABASE_URL.
func setupContainerEnv(t *testing.T) string {
	t.Helper()

	container, cleanup := testdb.StartCockroachContainer(t, "current_account_test_db")
	t.Cleanup(cleanup)
	dbURL := testdb.CockroachDSN(t, container)

	t.Setenv("DATABASE_URL", dbURL)
	// Non-production so Redis failure falls back to the noop idempotency service.
	t.Setenv("ENVIRONMENT", "development")
	// Unreachable Redis port forces the noop fallback path quickly.
	t.Setenv("REDIS_URL", "redis://localhost:1")
	// Disable auth and tracing for a fast, dependency-free construction.
	t.Setenv("AUTH_ENABLED", "false")
	t.Setenv("OTEL_TRACES_ENABLED", "false")
	// No Kafka so run() would skip the outbox worker / consumer.
	t.Setenv("KAFKA_BOOTSTRAP_SERVERS", "")
	// Point saga asset loading at the repo root so default deposit/withdrawal
	// scripts resolve during service construction.
	t.Setenv("SAGA_ASSET_DIR", repoRoot(t))

	return dbURL
}

func TestNewContainer_RealDB(t *testing.T) {
	setupContainerEnv(t)

	ctx := context.Background()
	c, err := NewContainer(ctx, testLogger(), "test-version")
	require.NoError(t, err, "NewContainer should construct against a real CockroachDB")
	require.NotNil(t, c)
	defer c.Close()

	assert.NotNil(t, c.DB, "DB should be initialized")
	assert.NotNil(t, c.AccountRepo, "AccountRepo should be initialized")
	assert.NotNil(t, c.LienRepo, "LienRepo should be initialized")
	assert.NotNil(t, c.WithdrawalRepo, "WithdrawalRepo should be initialized")
	assert.NotNil(t, c.OutboxRepo, "OutboxRepo should be initialized")
	assert.NotNil(t, c.IdempotencyService, "IdempotencyService should be initialized")
	assert.NotNil(t, c.HandlerRegistry, "HandlerRegistry should be initialized")
	assert.NotNil(t, c.Service, "Service should be initialized")

	// Redis is unreachable in this setup, so we expect the noop fallback.
	assert.True(t, c.UsingNoopIdempotency, "should fall back to noop idempotency")
	assert.Nil(t, c.RedisClient, "RedisClient should be nil when using noop fallback")

	// Auth disabled, so no interceptor.
	assert.Nil(t, c.AuthInterceptor, "AuthInterceptor should be nil when auth is disabled")
}

func TestContainer_Close_Idempotent_RealDB(t *testing.T) {
	setupContainerEnv(t)

	ctx := context.Background()
	c, err := NewContainer(ctx, testLogger(), "test-version")
	require.NoError(t, err)
	require.NotNil(t, c)

	assert.NotPanics(t, func() {
		c.Close()
		c.Close()
	}, "Close should be idempotent and not panic when called twice")
}
