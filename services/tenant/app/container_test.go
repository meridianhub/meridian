package app

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// --- Close tests ---

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

func TestContainer_Close_WithCancelRegistry(t *testing.T) {
	called := false
	_, cancel := context.WithCancel(context.Background())
	// Wrap the real cancel so we can observe the call.
	c := &Container{
		Logger: testLogger(),
		cancelRegistry: func() {
			called = true
			cancel() // still call real cancel to avoid leak
		},
	}

	c.Close()
	assert.True(t, called, "cancelRegistry should have been called")
}

func TestContainer_Close_WithCleanups(t *testing.T) {
	var order []int
	c := &Container{
		Logger: testLogger(),
		cleanups: []func(){
			func() { order = append(order, 1) },
			func() { order = append(order, 2) },
			func() { order = append(order, 3) },
		},
	}

	c.Close()
	assert.Equal(t, []int{3, 2, 1}, order, "cleanups should run in reverse order")
}

// --- init* disabled/nil tests ---

func TestContainer_initRepositories_WithNilDB(t *testing.T) {
	c := &Container{Logger: testLogger(), DB: nil}
	// initRepositories calls persistence.NewRepository(nil) which should still
	// return a non-nil Repository struct (it wraps the DB pointer).
	c.initRepositories()
	assert.NotNil(t, c.Repo)
}

func TestContainer_initSchemaProvisioner_Disabled(t *testing.T) {
	// SCHEMA_PROVISIONING_ENABLED defaults to "false" when unset.
	t.Setenv("SCHEMA_PROVISIONING_ENABLED", "false")
	c := &Container{Logger: testLogger()}

	err := c.initSchemaProvisioner()
	require.NoError(t, err)
	assert.Nil(t, c.SchemaProvisioner)
}

func TestContainer_initPartyClient_Disabled(t *testing.T) {
	t.Setenv("PARTY_SERVICE_ENABLED", "false")
	c := &Container{Logger: testLogger()}

	err := c.initPartyClient(context.Background())
	require.NoError(t, err)
	assert.Nil(t, c.PartyClient)
}

func TestContainer_initRedis_Disabled(t *testing.T) {
	t.Setenv("REDIS_ENABLED", "false")
	c := &Container{Logger: testLogger()}

	err := c.initRedis(context.Background())
	require.NoError(t, err)
	assert.Nil(t, c.RedisClient)
	assert.Nil(t, c.SlugCache)
}

func TestContainer_initProvisioningWorker_NoProvisioner(t *testing.T) {
	c := &Container{Logger: testLogger(), SchemaProvisioner: nil}

	err := c.initProvisioningWorker()
	require.NoError(t, err)
	assert.Nil(t, c.ProvisioningWorker)
}

func TestContainer_startProvisioningWorker_NilWorker(t *testing.T) {
	c := &Container{Logger: testLogger(), ProvisioningWorker: nil}
	assert.NotPanics(t, func() { c.startProvisioningWorker(context.Background()) })
}

func TestContainer_initAuth_Disabled(t *testing.T) {
	t.Setenv("AUTH_ENABLED", "false")
	c := &Container{Logger: testLogger()}

	err := c.initAuth(context.Background())
	require.NoError(t, err)
	assert.Nil(t, c.AuthInterceptor)
}

func TestContainer_initTracer_Disabled(t *testing.T) {
	t.Setenv("OTEL_TRACES_ENABLED", "false")
	c := &Container{Logger: testLogger()}

	err := c.initTracer(context.Background(), "test-version")
	require.NoError(t, err)
	// Tracer is set even when disabled (it's a no-op tracer).
	assert.NotNil(t, c.Tracer)
}

// --- loadWorkerConfig tests ---

func TestLoadWorkerConfig_Defaults(t *testing.T) {
	// Clear any env vars that could influence the result.
	t.Setenv("PROVISIONING_WORKER_POLL_INTERVAL", "")
	t.Setenv("PROVISIONING_MAX_RETRIES", "")
	t.Setenv("PROVISIONING_RETRY_BASE_DELAY", "")
	t.Setenv("PROVISIONING_RETRY_MAX_DELAY", "")
	t.Setenv("PROVISIONING_MAX_CONCURRENT", "")

	cfg, err := loadWorkerConfig()
	require.NoError(t, err)

	assert.Equal(t, 10*time.Second, cfg.PollInterval)
	assert.Equal(t, 5, cfg.MaxRetries)
	assert.Equal(t, 2*time.Second, cfg.RetryBaseDelay)
	assert.Equal(t, 5*time.Second, cfg.RetryMaxDelay) // defaults.DefaultMaxRetryInterval
	assert.Equal(t, 5, cfg.MaxConcurrent)
}

func TestLoadWorkerConfig_InvalidPollInterval(t *testing.T) {
	t.Setenv("PROVISIONING_WORKER_POLL_INTERVAL", "100ms")

	_, err := loadWorkerConfig()
	require.Error(t, err)
	assert.ErrorIs(t, err, errInvalidPollInterval)
}

func TestLoadWorkerConfig_InvalidMaxRetries(t *testing.T) {
	t.Setenv("PROVISIONING_MAX_RETRIES", "25")

	_, err := loadWorkerConfig()
	require.Error(t, err)
	assert.ErrorIs(t, err, errInvalidMaxRetries)
}

func TestLoadWorkerConfig_InvalidRetryDelayRange(t *testing.T) {
	t.Setenv("PROVISIONING_RETRY_BASE_DELAY", "10s")
	t.Setenv("PROVISIONING_RETRY_MAX_DELAY", "5s")

	_, err := loadWorkerConfig()
	require.Error(t, err)
	assert.ErrorIs(t, err, errInvalidRetryDelayRange)
}
