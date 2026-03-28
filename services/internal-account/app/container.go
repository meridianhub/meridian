// Package app provides application configuration and dependency injection for the internal-account service.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"gorm.io/gorm"

	"github.com/meridianhub/meridian/services/internal-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/internal-account/service"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/kafka"
	"github.com/meridianhub/meridian/shared/platform/observability"
)

// Container holds all application dependencies for the internal-account service.
type Container struct {
	Logger *slog.Logger

	// Infrastructure
	Tracer *observability.Tracer
	DB     *gorm.DB

	// Auth
	AuthInterceptor *auth.Interceptor

	// Repositories
	Repo *persistence.Repository

	// Messaging
	OutboxRepo      *events.PostgresOutboxRepository
	OutboxPublisher *events.OutboxPublisher
	OutboxWorker    *events.Worker
	kafkaProducer   *kafka.ProtoProducer

	// Service
	Service *service.Service

	// Internal config
	BootstrapServers string
}

// NewContainer creates and initializes a new dependency injection container.
func NewContainer(ctx context.Context, logger *slog.Logger, version string) (*Container, error) {
	c := &Container{
		Logger: logger,
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

	c.initRepositories()

	if err := c.initService(); err != nil {
		return nil, err
	}

	if err := c.initKafka(ctx); err != nil {
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
		ServiceName:    "internal-account-service",
		ServiceVersion: version,
		Logger:         c.Logger,
	})
	if err != nil {
		return fmt.Errorf("failed to initialize tracer: %w", err)
	}
	c.Tracer = tracer
	return nil
}

// initDatabase initializes the GORM database connection.
func (c *Container) initDatabase(ctx context.Context) error {
	dbConfig := bootstrap.DefaultDatabaseConfig()
	dbConfig.Logger = c.Logger
	db, err := bootstrap.NewDatabase(ctx, dbConfig)
	if err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}
	c.DB = db
	c.Logger.Info("database connection established")
	return nil
}

// initRepositories creates persistence repositories and the outbox publisher.
func (c *Container) initRepositories() {
	c.Repo = persistence.NewRepository(c.DB)
	c.OutboxRepo = events.NewPostgresOutboxRepository(c.DB)
	c.OutboxPublisher = events.NewOutboxPublisher("internal-account")
	c.Logger.Info("repositories initialized")
}

// initService creates the internal-account service and wires the outbox publisher.
func (c *Container) initService() error {
	svc, err := service.NewServiceWithClients(
		c.Repo,
		nil, // posKeepingClient - delegated to saga layer
		nil, // referenceDataClient - not wired yet
		c.Logger,
		c.Tracer,
	)
	if err != nil {
		return fmt.Errorf("failed to create service: %w", err)
	}

	svc.SetOutboxPublisher(c.OutboxPublisher, c.DB)
	c.Service = svc

	c.Logger.Info("service initialized")
	return nil
}

// initKafka initializes the Kafka producer and outbox worker (optional - depends on KAFKA_BOOTSTRAP_SERVERS).
func (c *Container) initKafka(ctx context.Context) error {
	c.BootstrapServers = env.GetEnvOrDefault("KAFKA_BOOTSTRAP_SERVERS", "")
	if c.BootstrapServers == "" {
		c.Logger.Warn("KAFKA_BOOTSTRAP_SERVERS not configured, outbox worker disabled - events will be persisted but not published",
			"environment", os.Getenv("ENVIRONMENT"))
		return nil
	}

	producer, err := kafka.NewProtoProducer(kafka.ProducerConfig{
		BootstrapServers: c.BootstrapServers,
		ClientID:         "internal-account-outbox-worker",
		Acks:             "all",
		Retries:          3,
		Compression:      "snappy",
	})
	if err != nil {
		c.Logger.Warn("failed to create Kafka producer for outbox worker - events will be persisted but not published",
			"error", err)
		return nil
	}

	c.kafkaProducer = producer

	workerConfig := events.DefaultWorkerConfig("internal-account")
	c.OutboxWorker = events.NewWorker(c.OutboxRepo, c.kafkaProducer, workerConfig, c.Logger)
	c.OutboxWorker.Start(ctx)

	c.Logger.Info("outbox worker started",
		"bootstrap_servers", c.BootstrapServers,
		"batch_size", workerConfig.BatchSize,
		"poll_interval", workerConfig.PollInterval)
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

	// Close database
	bootstrap.CloseDatabase(c.DB, c.Logger)

	// Shutdown tracer
	bootstrap.ShutdownTracer(c.Tracer, c.Logger)

	c.Logger.Info("container resources closed")
}
