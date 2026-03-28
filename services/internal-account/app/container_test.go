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

	// Close on a container with only a logger should not panic.
	assert.NotPanics(t, func() {
		c.Close()
	})
}

func TestContainer_Close_Idempotent(t *testing.T) {
	c := &Container{
		Logger: testLogger(),
	}

	// Calling Close twice should not panic.
	assert.NotPanics(t, func() {
		c.Close()
		c.Close()
	})
}

func TestContainer_initRepositories_WithNilDB(t *testing.T) {
	c := &Container{
		Logger: testLogger(),
		DB:     nil,
	}

	c.initRepositories()

	// Repos wrap the DB reference at construction time; a nil DB is valid
	// because actual queries happen later at request time.
	assert.NotNil(t, c.Repo, "Repo should be created even with nil DB")
	assert.NotNil(t, c.OutboxRepo, "OutboxRepo should be created even with nil DB")
	assert.NotNil(t, c.OutboxPublisher, "OutboxPublisher should be created even with nil DB")
}

func TestContainer_initKafka_NoBootstrapServers(t *testing.T) {
	t.Setenv("KAFKA_BOOTSTRAP_SERVERS", "")
	t.Setenv("ENVIRONMENT", "development")

	c := &Container{
		Logger: testLogger(),
	}

	err := c.initKafka(context.Background())

	require.NoError(t, err)
	assert.Nil(t, c.kafkaProducer, "kafkaProducer should be nil when KAFKA_BOOTSTRAP_SERVERS is empty")
	assert.Nil(t, c.OutboxWorker, "OutboxWorker should be nil when Kafka is not configured")
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

func TestContainer_initTracer_DisabledViaEnv(t *testing.T) {
	t.Setenv("OTEL_TRACES_ENABLED", "false")

	c := &Container{
		Logger: testLogger(),
	}

	err := c.initTracer(context.Background(), "test-version")

	require.NoError(t, err)
}

func TestContainer_initService_WithNilClients(t *testing.T) {
	c := &Container{
		Logger: testLogger(),
		DB:     nil,
	}

	// initRepositories creates repos wrapping nil DB - valid at construction time.
	c.initRepositories()

	// initService creates the service with nil position-keeping and reference-data clients.
	err := c.initService()

	require.NoError(t, err)
	assert.NotNil(t, c.Service, "Service should be created with nil external clients")
}
