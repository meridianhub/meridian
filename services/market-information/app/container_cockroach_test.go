package app

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/shared/platform/testdb"
)

// setupCockroachDBURL spins up a real CockroachDB testcontainer and returns its
// connection URL. Cleanup is registered with t.Cleanup.
func setupCockroachDBURL(t *testing.T) string {
	t.Helper()

	container, cleanup := testdb.StartCockroachContainer(t, "market_information_test_db")
	t.Cleanup(cleanup)

	return testdb.CockroachDSN(t, container)
}

// testContainerEnv configures the env so NewContainer takes the fast,
// dependency-free paths: real DB via DATABASE_URL, no Kafka producer/worker,
// auth disabled, and tracing disabled (noop).
func testContainerEnv(t *testing.T, dbURL string) {
	t.Helper()

	t.Setenv("DATABASE_URL", dbURL)
	t.Setenv("KAFKA_BOOTSTRAP_SERVERS", "")
	t.Setenv("AUTH_ENABLED", "false")
	t.Setenv("OTEL_TRACES_ENABLED", "false")
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
}

// TestNewContainer_RealDB constructs the full container against a real
// CockroachDB instance and asserts the key dependency fields are wired up.
func TestNewContainer_RealDB(t *testing.T) {
	dbURL := setupCockroachDBURL(t)
	testContainerEnv(t, dbURL)

	ctx := context.Background()
	c, err := NewContainer(ctx, testLogger(), "test-version")
	require.NoError(t, err)
	require.NotNil(t, c)
	defer c.Close()

	// Infrastructure
	assert.NotNil(t, c.Tracer, "Tracer should be non-nil (noop when disabled)")
	assert.NotNil(t, c.DBPool, "DBPool should be non-nil")
	assert.NotNil(t, c.GormDB, "GormDB should be non-nil")

	// Repositories
	assert.NotNil(t, c.Repos, "Repos should be non-nil")

	// Messaging (outbox repo/publisher created even without Kafka)
	assert.NotNil(t, c.OutboxRepo, "OutboxRepo should be non-nil")
	assert.NotNil(t, c.OutboxPublisher, "OutboxPublisher should be non-nil")

	// Domain
	assert.NotNil(t, c.EventPublisher, "EventPublisher should be non-nil")
	assert.NotNil(t, c.MarketInformationServer, "MarketInformationServer should be non-nil")

	// With KAFKA_BOOTSTRAP_SERVERS empty, no producer/worker should be created.
	assert.Nil(t, c.OutboxWorker, "OutboxWorker should be nil when Kafka is disabled")
}

// TestNewContainer_RealDB_WithKafka constructs the container with a (fake)
// Kafka bootstrap server set. franz-go connects lazily, so the producer object
// is created without dialing, which exercises the Kafka-enabled branches of
// initKafka and Close (producer flush/close, worker start/stop).
func TestNewContainer_RealDB_WithKafka(t *testing.T) {
	dbURL := setupCockroachDBURL(t)
	t.Setenv("DATABASE_URL", dbURL)
	t.Setenv("KAFKA_BOOTSTRAP_SERVERS", "localhost:9092") // fake; franz-go dials lazily
	t.Setenv("AUTH_ENABLED", "false")
	t.Setenv("OTEL_TRACES_ENABLED", "false")

	ctx := context.Background()
	c, err := NewContainer(ctx, testLogger(), "test-version")
	require.NoError(t, err)
	require.NotNil(t, c)

	assert.NotNil(t, c.OutboxWorker, "OutboxWorker should be created when Kafka is enabled")

	assert.NotPanics(t, func() {
		c.Close()
	}, "Close should flush/close the Kafka producer and stop the worker without panicking")
}

// TestContainer_Close_Idempotent_RealDB verifies Close can be invoked multiple
// times without panicking after a real construction.
func TestContainer_Close_Idempotent_RealDB(t *testing.T) {
	dbURL := setupCockroachDBURL(t)
	testContainerEnv(t, dbURL)

	ctx := context.Background()
	c, err := NewContainer(ctx, testLogger(), "test-version")
	require.NoError(t, err)
	require.NotNil(t, c)

	assert.NotPanics(t, func() {
		c.Close()
		c.Close()
	}, "Close should be idempotent and not panic on repeated calls")
}
