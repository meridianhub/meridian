// Package app provides application configuration and dependency injection for the market-information service.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"gorm.io/gorm"

	"github.com/meridianhub/meridian/services/market-information/adapters/persistence"
	"github.com/meridianhub/meridian/services/market-information/config"
	"github.com/meridianhub/meridian/services/market-information/service"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/kafka"
	"github.com/meridianhub/meridian/shared/platform/observability"
)

// Container holds all application dependencies for the market-information service.
type Container struct {
	Logger *slog.Logger
	Config config.Config

	// Infrastructure
	Tracer *observability.Tracer
	DBPool *pgxpool.Pool
	GormDB *gorm.DB

	// Auth
	AuthInterceptor *auth.Interceptor

	// Repositories
	Repos *persistence.Repositories

	// Messaging
	OutboxRepo      *events.PostgresOutboxRepository
	OutboxPublisher *events.OutboxPublisher
	OutboxWorker    *events.Worker
	kafkaProducer   *kafka.ProtoProducer

	// Domain
	EventPublisher          *service.OutboxEventPublisher
	MarketInformationServer *service.Server
}

// NewContainer creates and initializes a new dependency injection container.
func NewContainer(ctx context.Context, logger *slog.Logger, version string) (*Container, error) {
	c := &Container{
		Logger: logger,
		Config: config.LoadConfig(),
	}

	// If initialization fails partway, close already-initialized resources.
	succeeded := false
	defer func() { //nolint:contextcheck // Close manages its own shutdown contexts
		if !succeeded {
			c.Close()
		}
	}()

	if err := c.initTracer(ctx, version); err != nil {
		return nil, err
	}

	if err := c.initDatabase(ctx); err != nil {
		return nil, err
	}

	if err := c.initKafka(ctx); err != nil {
		return nil, err
	}

	c.initRepositories()

	if err := c.initService(); err != nil {
		return nil, err
	}

	if err := c.initAuth(ctx); err != nil {
		return nil, err
	}

	succeeded = true
	logger.Info("dependency container initialized successfully")
	return c, nil
}

// initTracer initializes the OpenTelemetry tracer.
func (c *Container) initTracer(ctx context.Context, version string) error {
	tracer, err := bootstrap.NewTracer(ctx, bootstrap.TracerConfig{
		ServiceName:    "market-information-service",
		ServiceVersion: version,
		Logger:         c.Logger,
	})
	if err != nil {
		return fmt.Errorf("failed to initialize tracer: %w", err)
	}
	c.Tracer = tracer
	return nil
}

// initDatabase initializes both the pgxpool connection (for domain persistence)
// and the GORM connection (for outbox pattern).
func (c *Container) initDatabase(ctx context.Context) error {
	dbURL := env.GetEnvOrDefault("DATABASE_URL", "postgres://meridian_user@localhost:26257/meridian?sslmode=disable")

	// pgxpool for domain persistence
	dbPool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("failed to create database connection pool: %w", err)
	}

	if err := dbPool.Ping(ctx); err != nil {
		dbPool.Close()
		return fmt.Errorf("failed to ping database: %w", err)
	}
	c.DBPool = dbPool
	c.Logger.Info("database connection established")

	// GORM for outbox pattern
	gormDBConfig := bootstrap.DefaultDatabaseConfig()
	gormDBConfig.DSN = dbURL
	gormDBConfig.Logger = c.Logger
	gormDB, err := bootstrap.NewDatabase(ctx, gormDBConfig)
	if err != nil {
		return fmt.Errorf("failed to initialize GORM database for outbox: %w", err)
	}
	c.GormDB = gormDB

	return nil
}

// initKafka initializes the outbox repository, publisher, Kafka producer, and outbox worker.
func (c *Container) initKafka(ctx context.Context) error {
	c.OutboxRepo = events.NewPostgresOutboxRepository(c.GormDB)
	c.OutboxPublisher = events.NewOutboxPublisher("market-information")

	bootstrapServers := env.GetEnvOrDefault("KAFKA_BOOTSTRAP_SERVERS", "")
	if bootstrapServers == "" {
		c.Logger.Warn("outbox worker disabled - KAFKA_BOOTSTRAP_SERVERS not set")
		return nil
	}

	producer, err := kafka.NewProtoProducer(kafka.ProducerConfig{
		BootstrapServers: bootstrapServers,
		ClientID:         "market-information-outbox-worker",
		Acks:             "all",
		Retries:          3,
		Compression:      "snappy",
	})
	if err != nil {
		c.Logger.Warn("failed to create Kafka producer for outbox worker",
			"error", err,
			"environment", os.Getenv("ENVIRONMENT"))
		return nil
	}

	c.kafkaProducer = producer

	workerConfig := events.DefaultWorkerConfig("market-information")
	c.OutboxWorker = events.NewWorker(c.OutboxRepo, c.kafkaProducer, workerConfig, c.Logger)
	c.OutboxWorker.Start(ctx)
	c.Logger.Info("outbox worker started",
		"bootstrap_servers", bootstrapServers,
		"batch_size", workerConfig.BatchSize,
		"poll_interval", workerConfig.PollInterval)

	return nil
}

// initRepositories creates the persistence repositories.
func (c *Container) initRepositories() {
	masterTenantID := env.GetEnvOrDefault("MASTER_TENANT_ID", "meridian_master")
	c.Repos = persistence.NewRepositories(c.DBPool, masterTenantID)
	c.Logger.Info("repositories initialized", "master_tenant_id", masterTenantID)
}

// initService creates the outbox event publisher and market information server.
func (c *Container) initService() error {
	c.EventPublisher = service.NewOutboxEventPublisher(c.GormDB, c.OutboxPublisher)

	server, err := service.NewServer(
		c.Repos.DataSet,
		c.Repos.Observation,
		c.Repos.Source,
		service.WithEventPublisher(c.EventPublisher),
		service.WithLogger(c.Logger.With("component", "market_information_server")),
	)
	if err != nil {
		return fmt.Errorf("failed to create market information server: %w", err)
	}
	c.MarketInformationServer = server
	c.Logger.Info("market information server created")

	return nil
}

// initAuth initializes the auth interceptor.
func (c *Container) initAuth(ctx context.Context) error {
	authConfig := bootstrap.DefaultAuthConfig(c.Logger)
	interceptor, err := bootstrap.NewAuthInterceptor(ctx, authConfig)
	if err != nil {
		return fmt.Errorf("failed to initialize auth: %w", err)
	}
	c.AuthInterceptor = interceptor
	return nil
}

// Close gracefully shuts down all container resources.
func (c *Container) Close() {
	c.Logger.Info("closing container resources...")

	// Stop outbox worker
	if c.OutboxWorker != nil {
		c.Logger.Info("stopping outbox worker...")
		c.OutboxWorker.Stop()
		c.Logger.Info("outbox worker stopped")
	}

	// Close Kafka producer
	if c.kafkaProducer != nil {
		c.Logger.Info("flushing and closing Kafka producer...")
		if remaining := c.kafkaProducer.FlushWithTimeout(5000); remaining > 0 {
			c.Logger.Warn("some messages not delivered before close", "remaining", remaining)
		}
		c.kafkaProducer.Close()
		c.Logger.Info("Kafka producer closed")
	}

	// Close GORM database
	bootstrap.CloseDatabase(c.GormDB, c.Logger)

	// Close pgxpool
	if c.DBPool != nil {
		c.DBPool.Close()
		c.Logger.Info("database connection pool closed")
	}

	// Shutdown tracer
	bootstrap.ShutdownTracer(c.Tracer, c.Logger)

	c.Logger.Info("container resources closed")
}
