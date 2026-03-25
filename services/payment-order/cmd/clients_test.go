package main

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, nil))
}

// ---- createCurrentAccountClient ----

func TestCreateCurrentAccountClient_Success(t *testing.T) {
	client, cleanup, err := createCurrentAccountClient(
		"default",
		testLogger(),
		nil, // tracer is optional
	)
	require.NoError(t, err)
	require.NotNil(t, client)
	require.NotNil(t, cleanup)
	cleanup()
}

// ---- createFinancialAccountingClient ----

func TestCreateFinancialAccountingClient_Success(t *testing.T) {
	client, cleanup, err := createFinancialAccountingClient(
		context.Background(),
		"default",
		testLogger(),
		nil, // tracer is optional
	)
	require.NoError(t, err)
	require.NotNil(t, client)
	require.NotNil(t, cleanup)
	cleanup()
}

// ---- createInternalAccountClient ----

func TestCreateInternalAccountClient_Success(t *testing.T) {
	client, cleanup, err := createInternalAccountClient(
		"default",
		testLogger(),
		nil, // tracer is optional
	)
	require.NoError(t, err)
	require.NotNil(t, client)
	require.NotNil(t, cleanup)
	cleanup()
}

// ---- createRedisClient: valid URL with password and custom settings ----

func TestCreateRedisClient_ValidURLWithPassword(t *testing.T) {
	t.Setenv("REDIS_URL", "redis://127.0.0.1:19998") // non-existent, will fail on ping
	t.Setenv("REDIS_PASSWORD", "secret")
	t.Setenv("REDIS_DB", "1")
	t.Setenv("REDIS_POOL_SIZE", "5")
	t.Setenv("REDIS_MIN_IDLE_CONNS", "1")

	// We expect a ping failure, not a URL parse failure
	_, err := createRedisClient(testLogger())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to ping Redis")
}

func TestCreateRedisClient_CustomPoolSettings(t *testing.T) {
	t.Setenv("REDIS_URL", "redis://127.0.0.1:19997")
	t.Setenv("REDIS_POOL_SIZE", "20")
	t.Setenv("REDIS_MIN_IDLE_CONNS", "5")

	_, err := createRedisClient(testLogger())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to ping Redis")
}
