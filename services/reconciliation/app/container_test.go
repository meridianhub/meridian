package app

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/reconciliation/config"
	"github.com/meridianhub/meridian/services/reconciliation/service"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func testConfig() *config.Config {
	return &config.Config{
		Server: config.ServerConfig{Port: "50051"},
		Database: config.DatabaseConfig{
			URL:             "postgres://test:test@localhost:5432/testdb",
			MaxOpenConns:    5,
			MaxIdleConns:    2,
			ConnMaxLifetime: 5 * time.Minute,
			ConnMaxIdleTime: 1 * time.Minute,
		},
		Observability: config.ObservabilityConfig{ServiceName: "test-recon"},
	}
}

func TestContainer_Close_NilResources(t *testing.T) {
	c := &Container{
		Config: testConfig(),
		Logger: testLogger(),
	}

	assert.NotPanics(t, func() {
		c.Close()
	})
}

func TestContainer_Close_Idempotent(t *testing.T) {
	c := &Container{
		Config: testConfig(),
		Logger: testLogger(),
	}

	assert.NotPanics(t, func() {
		c.Close()
		c.Close()
	})
}

func TestContainer_Close_WithCleanups(t *testing.T) {
	c := &Container{
		Config: testConfig(),
		Logger: testLogger(),
	}

	var order []int
	c.cleanups = []func(){
		func() { order = append(order, 1) },
		func() { order = append(order, 2) },
		func() { order = append(order, 3) },
	}

	c.Close()

	require.Len(t, order, 3)
	assert.Equal(t, []int{3, 2, 1}, order, "cleanups should be called in reverse order")
}

func TestContainer_initRepositories_WithNilDB(t *testing.T) {
	c := &Container{
		Config: testConfig(),
		Logger: testLogger(),
		DB:     nil,
	}

	c.initRepositories()

	assert.NotNil(t, c.RunRepo)
	assert.NotNil(t, c.SnapshotRepo)
	assert.NotNil(t, c.VarianceRepo)
	assert.NotNil(t, c.DisputeRepo)
	assert.NotNil(t, c.AssertionRepo)
	assert.NotNil(t, c.TrendRepo)
}

func TestContainer_initKafka_NoBootstrapServers(t *testing.T) {
	t.Setenv("KAFKA_BOOTSTRAP_SERVERS", "")

	cfg := testConfig()
	cfg.Kafka.Enabled = false

	c := &Container{
		Config: cfg,
		Logger: testLogger(),
	}

	err := c.initKafka(context.Background())

	require.NoError(t, err)
	assert.Nil(t, c.kafkaProducer)
	assert.NotNil(t, c.EventPublisher, "EventPublisher should always be created (outbox-based)")
}

func TestContainer_initRedis_Disabled(t *testing.T) {
	cfg := testConfig()
	cfg.Redis.Enabled = false

	c := &Container{
		Config: cfg,
		Logger: testLogger(),
	}

	err := c.initRedis(context.Background())

	require.NoError(t, err)
	assert.Nil(t, c.RedisClient)
}

func TestContainer_initRedis_NoURL(t *testing.T) {
	cfg := testConfig()
	cfg.Redis.Enabled = true
	cfg.Redis.URL = ""

	c := &Container{
		Config: cfg,
		Logger: testLogger(),
	}

	err := c.initRedis(context.Background())

	require.NoError(t, err)
	assert.Nil(t, c.RedisClient, "RedisClient should be nil when URL is empty")
}

func TestContainer_initScheduler_Disabled(t *testing.T) {
	cfg := testConfig()
	cfg.Scheduler.Enabled = false

	c := &Container{
		Config: cfg,
		Logger: testLogger(),
	}

	c.initScheduler(context.Background())

	assert.Nil(t, c.CronScheduler)
}

func TestContainer_initScheduler_NoRedis(t *testing.T) {
	cfg := testConfig()
	cfg.Scheduler.Enabled = true

	c := &Container{
		Config:      cfg,
		Logger:      testLogger(),
		RedisClient: nil,
	}

	c.initScheduler(context.Background())

	assert.Nil(t, c.CronScheduler, "CronScheduler should be nil when RedisClient is nil")
}

func TestContainer_initAuth_Disabled(t *testing.T) {
	t.Setenv("AUTH_ENABLED", "false")

	c := &Container{
		Config: testConfig(),
		Logger: testLogger(),
	}

	err := c.initAuth(context.Background())

	require.NoError(t, err)
	assert.Nil(t, c.AuthInterceptor)
}

func TestContainer_initTracer_Disabled(t *testing.T) {
	t.Setenv("OTEL_TRACES_ENABLED", "false")

	c := &Container{
		Config: testConfig(),
		Logger: testLogger(),
	}

	err := c.initTracer(context.Background(), "test-version")

	require.NoError(t, err)
	assert.NotNil(t, c.Tracer, "Tracer should be non-nil (no-op) even when disabled")
}

func TestContainer_initServiceDeps_NoPKURL(t *testing.T) {
	cfg := testConfig()
	cfg.Services.PositionKeepingURL = ""

	c := &Container{
		Config: cfg,
		Logger: testLogger(),
	}

	// initRepositories must be called first since initServiceDeps references repos.
	c.initRepositories()

	opts, err := c.initServiceDeps()

	require.NoError(t, err)
	assert.NotEmpty(t, opts, "should return base service options even without PK URL")
}

func TestContainer_buildValuationComponents(t *testing.T) {
	cfg := testConfig()
	cfg.Services.ReferenceDataURL = ""

	c := &Container{
		Config: cfg,
		Logger: testLogger(),
	}

	// initRepositories needed by wireVarianceComponents -> NewVarianceDetector
	c.initRepositories()

	engine, provider := c.buildValuationComponents()

	assert.NotNil(t, engine, "valuation engine should be non-nil")
	assert.NotNil(t, provider, "reference data provider should be non-nil")
	assert.Nil(t, c.refDataConn, "refDataConn should be nil when ReferenceDataURL is empty")
}

func TestBuildValuationComponents_Exported(t *testing.T) {
	cfg := testConfig()
	cfg.Services.ReferenceDataURL = ""

	engine, provider, conn := BuildValuationComponents(cfg, testLogger())

	assert.NotNil(t, engine)
	assert.NotNil(t, provider)
	assert.Nil(t, conn, "conn should be nil when ReferenceDataURL is empty")
}

func TestContainer_initService(t *testing.T) {
	c := &Container{
		Config: testConfig(),
		Logger: testLogger(),
	}

	c.initRepositories()

	opts := []service.Option{
		service.WithLogger(testLogger()),
	}

	c.initService(opts)

	assert.NotNil(t, c.ReconciliationService)
}
