package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/shared/platform/testdb"
)

// realDBConfig builds a config backed by a real CockroachDB testcontainer DSN,
// with all optional subsystems (Kafka, Redis, scheduler, upstream gRPC deps)
// disabled so NewContainer exercises the full happy-path construction without
// requiring external services.
func realDBConfig(t *testing.T) string {
	t.Helper()

	container, cleanup := testdb.StartCockroachContainer(t, "reconciliation_test_db")
	t.Cleanup(cleanup)

	return testdb.CockroachDSN(t, container)
}

// disableExternalToggles sets the env toggles that route NewContainer down its
// no-op / fast paths: tracer no-op, auth disabled, no Kafka brokers.
func disableExternalToggles(t *testing.T) {
	t.Helper()
	t.Setenv("OTEL_TRACES_ENABLED", "false")
	t.Setenv("AUTH_ENABLED", "false")
	t.Setenv("KAFKA_BOOTSTRAP_SERVERS", "")
}

func TestNewContainer_RealDB(t *testing.T) {
	disableExternalToggles(t)

	cfg := testConfig()
	cfg.Database.URL = realDBConfig(t)
	// Keep optional subsystems off so construction stays self-contained.
	cfg.Kafka.Enabled = false
	cfg.Redis.Enabled = false
	cfg.Scheduler.Enabled = false

	ctx := context.Background()
	c, err := NewContainer(ctx, cfg, testLogger(), "test-version")
	require.NoError(t, err)
	require.NotNil(t, c)
	defer c.Close()

	// Core infrastructure built against the real DB.
	assert.NotNil(t, c.DB, "DB should be connected")
	assert.NotNil(t, c.Tracer, "Tracer should be non-nil (no-op when disabled)")

	// Messaging (outbox-based, always wired).
	assert.NotNil(t, c.OutboxRepo)
	assert.NotNil(t, c.OutboxPublisher)
	assert.NotNil(t, c.EventPublisher)

	// Repositories.
	assert.NotNil(t, c.RunRepo)
	assert.NotNil(t, c.SnapshotRepo)
	assert.NotNil(t, c.VarianceRepo)
	assert.NotNil(t, c.DisputeRepo)
	assert.NotNil(t, c.AssertionRepo)
	assert.NotNil(t, c.TrendRepo)

	// Service.
	assert.NotNil(t, c.ReconciliationService)

	// Disabled optional subsystems remain nil.
	assert.Nil(t, c.RedisClient, "Redis disabled")
	assert.Nil(t, c.CronScheduler, "scheduler disabled")
	assert.Nil(t, c.AuthInterceptor, "auth disabled")
	assert.Nil(t, c.kafkaProducer, "no Kafka brokers configured")
}

func TestContainer_Close_Idempotent_RealDB(t *testing.T) {
	disableExternalToggles(t)

	cfg := testConfig()
	cfg.Database.URL = realDBConfig(t)
	cfg.Kafka.Enabled = false
	cfg.Redis.Enabled = false
	cfg.Scheduler.Enabled = false

	ctx := context.Background()
	c, err := NewContainer(ctx, cfg, testLogger(), "test-version")
	require.NoError(t, err)
	require.NotNil(t, c)

	assert.NotPanics(t, func() {
		c.Close()
		c.Close()
	}, "Close should be safe to call multiple times")
}
