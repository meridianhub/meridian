// Package app provides application configuration and dependency injection for the financial-accounting service.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
	"github.com/meridianhub/meridian/services/financial-accounting/config"
	serviceobs "github.com/meridianhub/meridian/services/financial-accounting/observability"
	"github.com/meridianhub/meridian/services/financial-accounting/service"
	"github.com/meridianhub/meridian/services/financial-accounting/worker"
	refcache "github.com/meridianhub/meridian/services/reference-data/cache"
	refclient "github.com/meridianhub/meridian/services/reference-data/client"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/kafka"
	"github.com/meridianhub/meridian/shared/platform/observability"
)

// ErrRedisRequiredInProduction is returned when Redis is unavailable in production environments.
var ErrRedisRequiredInProduction = errors.New("redis required for idempotency in production environment")

// ErrKafkaRequiredInProduction is returned when Kafka is unavailable in production environments.
var ErrKafkaRequiredInProduction = errors.New("kafka required for event publishing in production environment")

// ErrBankCashAccountIDRequired is returned when the BANK_CASH_ACCOUNT_ID environment variable is not set.
var ErrBankCashAccountIDRequired = errors.New("BANK_CASH_ACCOUNT_ID environment variable is required")

// ErrBankCashAccountIDInvalid is returned when the BANK_CASH_ACCOUNT_ID is not a valid UUID.
var ErrBankCashAccountIDInvalid = errors.New("BANK_CASH_ACCOUNT_ID must be a valid UUID")

// Container holds all application dependencies for the financial-accounting service.
type Container struct {
	Logger *slog.Logger

	// Infrastructure
	Tracer      *observability.Tracer
	DB          *gorm.DB
	RedisClient *redis.Client

	// Repositories
	LedgerRepo *persistence.LedgerRepository

	// Messaging
	OutboxRepo      *events.PostgresOutboxRepository
	OutboxPublisher *events.OutboxPublisher
	EventPublisher  service.EventPublisher
	kafkaProducer   *kafka.ProtoProducer
	OutboxWorker    *events.Worker

	// Idempotency
	IdempotencyService idempotency.Service
	RedisSvc           *idempotency.RedisService // kept for cleanup worker

	// Cleanup worker
	IdempotencyCleanupWorker *worker.IdempotencyCleanupWorker

	// Service options (registry, etc.)
	ServiceOpts []service.Option

	// Auth
	AuthInterceptor *auth.Interceptor

	// Degradation flags for health checker
	UsingNoopIdempotency    bool
	UsingNoopEventPublisher bool

	// Posting service (created during init)
	PostingService *service.PostingService

	// Audit publisher for cleanup
	auditPublisher *audit.Publisher

	// Cleanup functions
	cleanups []func()
}

// NewContainer creates and initializes a new dependency injection container.
func NewContainer(ctx context.Context, logger *slog.Logger, version string) (_ *Container, err error) {
	c := &Container{
		Logger: logger,
	}

	// If initialization fails partway, close already-initialized resources.
	defer func() {
		if err != nil {
			c.Close()
		}
	}()

	if err = c.initTracer(ctx, version); err != nil {
		return nil, err
	}

	if err = c.initDatabase(ctx); err != nil {
		return nil, err
	}

	c.initAuditPublisher()
	c.initRepositories()

	if err = c.initKafka(ctx); err != nil {
		return nil, err
	}

	if err = c.initBankCashAccount(); err != nil {
		return nil, err
	}

	if err = c.initRedis(ctx); err != nil {
		return nil, err
	}

	c.initReferenceDataClient(ctx)

	if err = c.initAuth(ctx); err != nil {
		return nil, err
	}

	logger.Info("dependency container initialized successfully")
	return c, nil
}

// initTracer initializes the OpenTelemetry tracer.
func (c *Container) initTracer(ctx context.Context, version string) error {
	tracer, err := bootstrap.NewTracer(ctx, bootstrap.TracerConfig{
		ServiceName:    "financial-accounting-service",
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

// initAuditPublisher initializes the Kafka-based audit publisher.
func (c *Container) initAuditPublisher() {
	audit.SetSchemaName("financial_accounting")

	bootstrapServers := env.GetEnvOrDefault("KAFKA_BOOTSTRAP_SERVERS", "")
	if bootstrapServers == "" {
		c.Logger.Info("audit Kafka publisher disabled: KAFKA_BOOTSTRAP_SERVERS not set")
		return
	}

	topic := env.GetEnvOrDefault("KAFKA_AUDIT_TOPIC", kafka.AuditEventsTopic)

	publisher, err := audit.NewPublisher(audit.PublisherConfig{
		BootstrapServers: bootstrapServers,
		Topic:            topic,
		SchemaName:       "financial_accounting",
		ClientID:         "financial-accounting-audit-publisher",
	})
	if err != nil {
		if errors.Is(err, audit.ErrPublisherDisabled) {
			c.Logger.Info("audit Kafka publisher disabled",
				"reason", err.Error())
			return
		}
		c.Logger.Warn("failed to initialize audit Kafka publisher, using outbox fallback",
			"error", err)
		return
	}

	audit.SetGlobalPublisher(publisher)
	c.auditPublisher = publisher

	c.Logger.Info("audit Kafka publisher initialized",
		"bootstrap_servers", bootstrapServers,
		"topic", topic,
		"schema", "financial_accounting")
}

// initRepositories creates persistence repositories.
func (c *Container) initRepositories() {
	c.LedgerRepo = persistence.NewLedgerRepository(c.DB)
	c.Logger.Info("repositories initialized")
}

// initKafka initializes the outbox publisher, Kafka producer, and outbox worker.
func (c *Container) initKafka(ctx context.Context) error {
	c.OutboxRepo = events.NewPostgresOutboxRepository(c.DB)
	c.OutboxPublisher = events.NewOutboxPublisher("financial-accounting")

	if err := c.initKafkaProducer(); err != nil {
		return err
	}

	// Start outbox worker if Kafka producer is available
	if c.kafkaProducer != nil {
		workerConfig := events.DefaultWorkerConfig("financial-accounting")
		c.OutboxWorker = events.NewWorker(c.OutboxRepo, c.kafkaProducer, workerConfig, c.Logger)
		c.OutboxWorker.Start(ctx)
		c.Logger.Info("outbox worker started",
			"batch_size", workerConfig.BatchSize,
			"poll_interval", workerConfig.PollInterval)
	}

	c.initEventPublisher()
	return nil
}

// initKafkaProducer creates the Kafka producer or falls back to noop in development.
func (c *Container) initKafkaProducer() error {
	bootstrapServers := env.GetEnvOrDefault("KAFKA_BOOTSTRAP_SERVERS", "")
	if bootstrapServers == "" {
		if env.IsProduction() {
			c.Logger.Error("CRITICAL: Kafka unavailable in production - failing fast",
				"reason", "KAFKA_BOOTSTRAP_SERVERS not set")
			return bootstrap.Permanent(ErrKafkaRequiredInProduction)
		}
		c.Logger.Warn("outbox worker disabled - DEVELOPMENT ONLY",
			"reason", "KAFKA_BOOTSTRAP_SERVERS not set",
			"environment", os.Getenv("ENVIRONMENT"))
		c.UsingNoopEventPublisher = true
		return nil
	}

	producer, err := kafka.NewProtoProducer(kafka.ProducerConfig{
		BootstrapServers: bootstrapServers,
		ClientID:         "financial-accounting-outbox-worker",
		Acks:             "all",
		Retries:          3,
		Compression:      "snappy",
	})
	if err != nil {
		if env.IsProduction() {
			c.Logger.Error("CRITICAL: Failed to create Kafka producer in production - failing fast",
				"error", err)
			return bootstrap.Permanent(fmt.Errorf("%w: %w", ErrKafkaRequiredInProduction, err))
		}
		c.Logger.Warn("failed to create Kafka producer for outbox worker - DEVELOPMENT ONLY",
			"error", err,
			"environment", os.Getenv("ENVIRONMENT"))
		c.UsingNoopEventPublisher = true
		return nil
	}

	c.kafkaProducer = producer
	c.Logger.Info("Kafka producer initialized for outbox worker",
		"bootstrap_servers", bootstrapServers)
	return nil
}

// initEventPublisher configures the event publisher based on Kafka availability.
func (c *Container) initEventPublisher() {
	c.EventPublisher = &NoopEventPublisher{}
	if c.UsingNoopEventPublisher {
		c.Logger.Warn("using noop event publisher - DEVELOPMENT ONLY",
			"environment", os.Getenv("ENVIRONMENT"))
		serviceobs.SetNoopEventPublisherActive(true)
		serviceobs.RecordServiceDegradation(serviceobs.ComponentEventPublisher, serviceobs.DegradationReasonStartupFallback)
	} else {
		serviceobs.SetNoopEventPublisherActive(false)
		c.Logger.Info("event publisher initialized (noop mode for direct publishing, outbox handles primary events)")
	}
}

// initBankCashAccount validates the bank cash account ID and creates the posting service.
func (c *Container) initBankCashAccount() error {
	bankCashAccountID := env.GetEnvOrDefault("BANK_CASH_ACCOUNT_ID", "")
	if bankCashAccountID == "" {
		return bootstrap.Permanent(ErrBankCashAccountIDRequired)
	}

	// Validate UUID format (permanent config error)
	if _, err := uuid.Parse(bankCashAccountID); err != nil {
		return bootstrap.Permanent(fmt.Errorf("%w: %w", ErrBankCashAccountIDInvalid, err))
	}

	c.Logger.Info("bank cash account configured",
		"account_id", bankCashAccountID)

	// Create posting service with static config
	c.PostingService = service.NewPostingServiceWithConfig(service.PostingServiceConfig{
		Repo:              c.LedgerRepo,
		BankCashAccountID: bankCashAccountID,
		AccountResolver:   nil, // Delegated to saga layer
		Logger:            c.Logger,
	})

	return nil
}

// initRedis initializes the Redis client and idempotency service.
func (c *Container) initRedis(_ context.Context) error {
	redisClient, err := CreateRedisClient(c.Logger) //nolint:contextcheck // CreateRedisClient manages its own timeout context
	if err != nil {
		if env.IsProduction() {
			c.Logger.Error("CRITICAL: Redis unavailable in production - failing fast",
				"error", err)
			return bootstrap.Permanent(fmt.Errorf("%w: %w", ErrRedisRequiredInProduction, err))
		}
		c.Logger.Warn("Redis not available at startup, using noop idempotency service - DEVELOPMENT ONLY",
			"error", err,
			"environment", os.Getenv("ENVIRONMENT"))
		c.IdempotencyService = idempotency.NewNoopService(c.Logger)
		c.UsingNoopIdempotency = true
		serviceobs.SetNoopIdempotencyActive(true)
		serviceobs.RecordServiceDegradation(serviceobs.ComponentIdempotency, serviceobs.DegradationReasonStartupFallback)
	} else {
		c.RedisClient = redisClient
		c.RedisSvc = idempotency.NewRedisService(redisClient)
		c.IdempotencyService = c.RedisSvc
		serviceobs.SetNoopIdempotencyActive(false)
		c.Logger.Info("idempotency service initialized with Redis")
	}

	// Initialize idempotency cleanup worker (only if Redis is available)
	cleanupConfig := config.LoadIdempotencyCleanupConfig()
	if c.RedisSvc != nil && cleanupConfig.Enabled {
		cleanupWorker, workerErr := worker.NewIdempotencyCleanupWorker(
			c.RedisSvc,
			cleanupConfig,
			c.Logger,
		)
		if workerErr != nil {
			c.Logger.Error("failed to create idempotency cleanup worker", "error", workerErr)
		} else {
			c.IdempotencyCleanupWorker = cleanupWorker
			// Start cleanup worker in background
			go func() { //nolint:contextcheck // Worker needs independent context for lifecycle management
				if err := cleanupWorker.Start(context.Background()); err != nil {
					c.Logger.Error("idempotency cleanup worker error", "error", err)
				}
			}()
			c.Logger.Info("idempotency cleanup worker started",
				"stale_threshold", cleanupConfig.StaleThreshold,
				"run_interval", cleanupConfig.RunInterval,
				"batch_size", cleanupConfig.BatchSize)
		}
	} else if !cleanupConfig.Enabled {
		c.Logger.Info("idempotency cleanup worker disabled by configuration")
	}

	return nil
}

// initReferenceDataClient initializes the reference-data client for fungibility validation (optional).
func (c *Container) initReferenceDataClient(ctx context.Context) {
	referenceDataURL := env.GetEnvOrDefault("REFERENCE_DATA_SERVICE_URL", "")
	if referenceDataURL == "" {
		c.Logger.Info("fungibility validation disabled (REFERENCE_DATA_SERVICE_URL not set)")
		return
	}

	refClient, refCleanup, refErr := refclient.New(ctx, refclient.Config{
		Target: referenceDataURL,
	})
	if refErr != nil {
		c.Logger.Warn("failed to create reference-data client, fungibility validation disabled",
			"error", refErr,
			"service_url", referenceDataURL)
		return
	}
	c.cleanups = append(c.cleanups, func() {
		if err := refCleanup(); err != nil {
			c.Logger.Error("failed to close reference-data client", "error", err)
		}
	})

	registryAdapter := service.NewReferenceDataRegistryAdapter(&ReferenceDataClientAdapter{Client: refClient})
	c.ServiceOpts = append(c.ServiceOpts, service.WithRegistry(registryAdapter))
	c.Logger.Info("fungibility validation enabled",
		"reference_data_service", referenceDataURL)
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

	// Stop idempotency cleanup worker
	if c.IdempotencyCleanupWorker != nil {
		c.Logger.Info("stopping idempotency cleanup worker...")
		c.IdempotencyCleanupWorker.Stop()
		c.Logger.Info("idempotency cleanup worker stopped")
	}

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

	// Close audit publisher
	if c.auditPublisher != nil {
		if err := c.auditPublisher.Close(); err != nil {
			c.Logger.Error("failed to close audit publisher", "error", err)
		}
	}

	// Run cleanup functions (reference data client, etc.)
	for i := len(c.cleanups) - 1; i >= 0; i-- {
		c.cleanups[i]()
	}

	// Close Redis client
	if c.RedisClient != nil {
		if err := c.RedisClient.Close(); err != nil {
			c.Logger.Error("failed to close Redis client", "error", err)
		}
	}

	// Close database
	bootstrap.CloseDatabase(c.DB, c.Logger)

	// Shutdown tracer
	bootstrap.ShutdownTracer(c.Tracer, c.Logger)

	c.Logger.Info("container resources closed")
}

// CreateRedisClient creates and validates a Redis client connection.
func CreateRedisClient(logger *slog.Logger) (*redis.Client, error) {
	redisURL := env.GetEnvOrDefault("REDIS_URL", "redis://localhost:6379")
	redisPassword := env.GetEnvOrDefault("REDIS_PASSWORD", "")
	redisDB := env.GetEnvAsInt("REDIS_DB", 0)
	poolSize := env.GetEnvAsInt("REDIS_POOL_SIZE", 10)
	minIdleConns := env.GetEnvAsInt("REDIS_MIN_IDLE_CONNS", 2)

	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("invalid REDIS_URL: %w", err)
	}

	if redisPassword != "" {
		opt.Password = redisPassword
	}
	opt.DB = redisDB
	opt.PoolSize = poolSize
	opt.MinIdleConns = minIdleConns

	client := redis.NewClient(opt)

	ctx, cancel := context.WithTimeout(context.Background(), defaults.DefaultHealthCheckTimeout)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("failed to ping Redis: %w", err)
	}

	logger.Info("Redis client connected",
		"addr", opt.Addr,
		"db", redisDB,
		"pool_size", poolSize,
		"min_idle_conns", minIdleConns)

	return client, nil
}

// NoopEventPublisher provides a no-operation implementation of service.EventPublisher.
type NoopEventPublisher struct{}

// Publish is a no-op implementation of service.EventPublisher.Publish.
func (p *NoopEventPublisher) Publish(_ context.Context, _ service.DomainEvent) error {
	return nil
}

// PublishBatch is a no-op implementation of service.EventPublisher.PublishBatch.
func (p *NoopEventPublisher) PublishBatch(_ context.Context, _ []service.DomainEvent) error {
	return nil
}

// ReferenceDataClientAdapter adapts refclient.Client to the service.ReferenceDataClient interface.
type ReferenceDataClientAdapter struct {
	Client *refclient.Client
}

// GetInstrument retrieves an instrument from the reference-data service's tiered cache.
func (a *ReferenceDataClientAdapter) GetInstrument(ctx context.Context, code string, version int) (service.CachedInstrumentResult, error) {
	cached, err := a.Client.GetInstrument(ctx, code, version)
	if err != nil {
		return nil, err
	}
	return &CachedInstrumentResultAdapter{Cached: cached}, nil
}

// CachedInstrumentResultAdapter adapts cache.CachedInstrument to service.CachedInstrumentResult.
type CachedInstrumentResultAdapter struct {
	Cached *refcache.CachedInstrument
}

// GetBucketKeyProgram returns the CEL program for fungibility key evaluation.
func (a *CachedInstrumentResultAdapter) GetBucketKeyProgram() interface{} {
	return a.Cached.BucketKeyProgram
}
