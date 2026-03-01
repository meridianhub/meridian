package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/services/position-keeping/adapters/messaging"
	"github.com/meridianhub/meridian/services/position-keeping/adapters/persistence"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/kafka"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/redis/go-redis/v9"
)

// Container holds all application dependencies
type Container struct {
	Config *Config
	Logger *slog.Logger

	// Infrastructure
	DBPool          *pgxpool.Pool
	RedisClient     *redis.Client
	Tracer          *observability.Tracer
	AuthInterceptor *auth.Interceptor
	kafkaProducer   *kafka.ProtoProducer // internal, for cleanup
	auditPublisher  *audit.Publisher     // internal, for cleanup

	// Adapters
	EventPublisher  domain.EventPublisher
	OutboxPublisher *messaging.OutboxEventPublisher

	// Repository
	PositionLogRepository domain.FinancialPositionLogRepository
	MeasurementRepository domain.MeasurementRepository

	// Event Outbox
	OutboxRepository *events.PgxOutboxRepository
}

// NewContainer creates and initializes a new dependency injection container
func NewContainer(ctx context.Context, config *Config, logger *slog.Logger) (*Container, error) {
	container := &Container{
		Config: config,
		Logger: logger,
	}

	// Initialize dependencies in order
	if err := container.initializeTracer(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize tracer: %w", err)
	}

	if err := container.initializeAuth(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize auth: %w", err)
	}

	if err := container.initializeDatabase(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	// Redis is optional - log warning and continue if unavailable.
	// Callers should check RedisClient for nil before use.
	if err := container.initializeRedis(ctx); err != nil {
		logger.Warn("Redis not available at startup, features requiring Redis will be disabled until it connects",
			"error", err)
	}

	container.initializeAuditPublisher()

	container.initializeEventPublisher()

	container.initializeRepositories()

	container.initializeOutboxRepository()

	container.initializeOutboxPublisher()

	logger.Info("dependency container initialized successfully")

	return container, nil
}

// initializeTracer initializes the OpenTelemetry tracer
func (c *Container) initializeTracer(ctx context.Context) error {
	// If OTLP endpoint is empty, tracing is disabled
	if c.Config.Observability.OTLPEndpoint == "" {
		c.Logger.Info("tracing disabled (no OTLP endpoint configured)")
		return nil
	}

	tracerConfig, err := observability.DefaultConfig()
	if err != nil {
		return fmt.Errorf("failed to create tracer config: %w", err)
	}

	// Override with our configuration
	tracerConfig = tracerConfig.
		WithServiceName(c.Config.Observability.ServiceName).
		WithServiceVersion(c.Config.Observability.ServiceVersion).
		WithEnvironment(c.Config.Observability.Environment).
		WithOTLPEndpoint(c.Config.Observability.OTLPEndpoint).
		WithSamplingRate(c.Config.Observability.SamplingRate)

	tracer, err := observability.NewTracer(ctx, tracerConfig)
	if err != nil {
		return fmt.Errorf("failed to create tracer: %w", err)
	}

	c.Tracer = tracer
	c.Logger.Info("tracer initialized",
		"service_name", tracerConfig.ServiceName,
		"environment", tracerConfig.Environment,
		"sampling_rate", tracerConfig.SamplingRate,
		"otlp_configured", tracerConfig.OTLPEndpoint != "")

	return nil
}

// initializeAuth initializes the JWT authentication interceptor
func (c *Container) initializeAuth(ctx context.Context) error {
	// If auth is disabled, skip initialization
	if !c.Config.Auth.Enabled {
		c.Logger.Info("auth disabled (set AUTH_ENABLED=true to enable)")
		return nil
	}

	// Create JWKS provider
	jwksConfig := &auth.JWKSProviderConfig{
		URL:        c.Config.Auth.JWKSURL,
		CacheTTL:   c.Config.Auth.JWKSCacheTTL,
		RefreshTTL: c.Config.Auth.JWKSRefreshTTL,
	}

	provider, err := auth.NewJWKSProvider(ctx, jwksConfig)
	if err != nil {
		return fmt.Errorf("failed to create JWKS provider: %w", err)
	}

	// Create JWT validator
	validator, err := auth.NewJWTValidatorWithJWKS(provider)
	if err != nil {
		return fmt.Errorf("failed to create JWT validator: %w", err)
	}

	// Create interceptor with bypass methods for health checks and reflection
	interceptorConfig := &auth.InterceptorConfig{
		JWKSValidator: validator,
		BypassMethods: []string{
			"/grpc.health.v1.Health/Check",
			"/grpc.health.v1.Health/Watch",
			"/grpc.reflection.v1alpha.ServerReflection/ServerReflectionInfo",
		},
	}

	interceptor, err := auth.NewAuthInterceptor(interceptorConfig)
	if err != nil {
		return fmt.Errorf("failed to create auth interceptor: %w", err)
	}

	c.AuthInterceptor = interceptor
	c.Logger.Info("auth interceptor initialized",
		"jwks_url", jwksConfig.URL,
		"cache_ttl", jwksConfig.CacheTTL,
		"refresh_ttl", jwksConfig.RefreshTTL,
		"bypass_methods", len(interceptorConfig.BypassMethods))

	return nil
}

// initializeDatabase initializes the database connection pool
func (c *Container) initializeDatabase(ctx context.Context) error {
	poolConfig, err := pgxpool.ParseConfig(c.Config.Database.URL)
	if err != nil {
		return fmt.Errorf("failed to parse database URL: %w", err)
	}

	// Configure connection pool with explicit bounds checking for CodeQL
	maxConns := c.Config.Database.MaxOpenConns
	minConns := c.Config.Database.MaxIdleConns
	if maxConns > 0 && maxConns <= 2147483647 {
		poolConfig.MaxConns = int32(maxConns)
	}
	if minConns >= 0 && minConns <= 2147483647 {
		poolConfig.MinConns = int32(minConns)
	}
	poolConfig.MaxConnLifetime = c.Config.Database.ConnMaxLifetime
	poolConfig.MaxConnIdleTime = c.Config.Database.ConnMaxIdleTime
	poolConfig.HealthCheckPeriod = c.Config.Database.HealthCheckInterval

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return fmt.Errorf("failed to create database pool: %w", err)
	}

	// Verify connection
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return fmt.Errorf("failed to ping database: %w", err)
	}

	c.DBPool = pool
	c.Logger.Info("database pool initialized",
		"max_conns", poolConfig.MaxConns,
		"min_conns", poolConfig.MinConns,
		"max_lifetime", poolConfig.MaxConnLifetime,
		"max_idle_time", poolConfig.MaxConnIdleTime)

	return nil
}

// initializeRedis initializes the Redis client
func (c *Container) initializeRedis(ctx context.Context) error {
	// If Redis is not enabled, skip initialization
	if !c.Config.Redis.Enabled {
		c.Logger.Info("redis disabled, idempotency features will use nil service")
		return nil
	}

	// Create Redis client
	client := redis.NewClient(&redis.Options{
		Addr:            c.Config.Redis.Address,
		Password:        c.Config.Redis.Password,
		DB:              c.Config.Redis.DB,
		PoolSize:        c.Config.Redis.PoolSize,
		ConnMaxIdleTime: c.Config.Redis.ConnMaxIdleTime,
	})

	// Verify connection
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return fmt.Errorf("failed to ping redis: %w", err)
	}

	c.RedisClient = client
	c.Logger.Info("redis client initialized",
		"address", c.Config.Redis.Address,
		"db", c.Config.Redis.DB,
		"pool_size", c.Config.Redis.PoolSize)

	return nil
}

// initializeAuditPublisher initializes the audit publisher for Kafka-based audit events.
// Sets the global schema name and publisher for GORM hook integration.
func (c *Container) initializeAuditPublisher() {
	// Always set schema name for audit events (used for outbox fallback routing)
	audit.SetSchemaName("position_keeping")

	if !c.Config.Kafka.Enabled {
		c.Logger.Info("audit publisher disabled (kafka disabled), using outbox fallback only")
		return
	}

	// Create audit publisher
	publisher, err := audit.NewPublisher(audit.PublisherConfig{
		BootstrapServers: strings.Join(c.Config.Kafka.Brokers, ","),
		Topic:            kafka.AuditEventsTopic,
		SchemaName:       "position_keeping",
		ClientID:         "position-keeping-audit",
	})
	if err != nil {
		c.Logger.Info("audit publisher not created, using outbox fallback only",
			"reason", err.Error())
		return
	}

	// Set global publisher for GORM hook integration
	audit.SetGlobalPublisher(publisher)
	c.auditPublisher = publisher

	c.Logger.Info("audit publisher initialized",
		"topic", kafka.AuditEventsTopic,
		"schema", "position_keeping")
}

// initializeEventPublisher initializes Kafka event publisher or no-op publisher
func (c *Container) initializeEventPublisher() {
	if !c.Config.Kafka.Enabled {
		c.Logger.Info("kafka disabled, using no-op event publisher")
		c.EventPublisher = domain.NewNoOpEventPublisher()
		return
	}

	// Create Kafka producer with tenant header support
	producerConfig := kafka.ProducerConfig{
		BootstrapServers: strings.Join(c.Config.Kafka.Brokers, ","),
		ClientID:         "position-keeping-service",
		Acks:             "all",    // Wait for full replication
		Retries:          3,        // Retry failed sends
		Compression:      "snappy", // Efficient compression
	}

	kafkaProducer, err := kafka.NewProtoProducer(producerConfig)
	if err != nil {
		c.Logger.Error("failed to create kafka producer, using no-op publisher", "error", err)
		c.EventPublisher = domain.NewNoOpEventPublisher()
		return
	}

	// Wrap with Position-Keeping event publisher adapter
	topicConfig := messaging.DefaultTopicConfig()
	eventPublisher, err := messaging.NewKafkaEventPublisher(kafkaProducer, topicConfig)
	if err != nil {
		c.Logger.Error("failed to create event publisher, using no-op publisher", "error", err)
		kafkaProducer.Close()
		c.EventPublisher = domain.NewNoOpEventPublisher()
		return
	}

	c.kafkaProducer = kafkaProducer
	c.EventPublisher = eventPublisher
	c.Logger.Info("kafka event publisher initialized",
		"brokers", c.Config.Kafka.Brokers,
		"client_id", producerConfig.ClientID)
}

// initializeRepositories initializes domain repositories
func (c *Container) initializeRepositories() {
	// Create PostgreSQL repositories
	c.PositionLogRepository = persistence.NewPostgresRepository(c.DBPool)
	c.MeasurementRepository = persistence.NewMeasurementRepository(c.DBPool)

	c.Logger.Info("repositories initialized")
}

// initializeOutboxRepository initializes the event outbox repository for transactional publishing.
// The repository is always initialized regardless of Kafka availability because:
// 1. Domain services use it transactionally to persist events alongside state changes
// 2. When Kafka is disabled, events remain in the outbox until Kafka becomes available
// 3. This enables graceful degradation - the system continues operating without message broker
func (c *Container) initializeOutboxRepository() {
	// Consider exposing outbox depth as a health check metric to alert operators
	// when the outbox is backing up (e.g., Kafka unavailable).
	c.OutboxRepository = events.NewPgxOutboxRepository(c.DBPool)
	c.Logger.Info("event outbox repository initialized")
}

// initializeOutboxPublisher initializes the OutboxEventPublisher that writes domain events
// to the transactional outbox table atomically with business operations.
func (c *Container) initializeOutboxPublisher() {
	publisher, err := messaging.NewOutboxEventPublisher(c.OutboxRepository)
	if err != nil {
		// NewOutboxEventPublisher only fails if OutboxRepository is nil, which cannot
		// happen here since initializeOutboxRepository always sets it.
		c.Logger.Error("failed to create outbox event publisher", "error", err)
		return
	}
	c.OutboxPublisher = publisher
	c.Logger.Info("outbox event publisher initialized")
}

// KafkaProducer returns the Kafka producer for use by components that need
// direct Kafka access (e.g., the event outbox worker). Returns nil if Kafka is disabled.
func (c *Container) KafkaProducer() *kafka.ProtoProducer {
	return c.kafkaProducer
}

// Close gracefully closes all resources in the container
func (c *Container) Close(ctx context.Context) error {
	c.Logger.Info("closing container resources...")

	var errs []error

	// Close audit publisher first (flush audit events before closing other resources)
	if c.auditPublisher != nil {
		//nolint:contextcheck // Publisher.Close uses FlushWithTimeout which manages its own timeout
		if err := c.auditPublisher.Close(); err != nil {
			c.Logger.Error("failed to close audit publisher", "error", err)
			errs = append(errs, fmt.Errorf("audit publisher close: %w", err))
		} else {
			c.Logger.Info("audit publisher closed")
		}
		// Clear global publisher to prevent use after close
		audit.SetGlobalPublisher(nil)
	}

	// Close Kafka producer (flush outstanding messages first)
	if c.kafkaProducer != nil {
		//nolint:contextcheck // FlushWithTimeout creates its own timeout context from milliseconds
		remaining := c.kafkaProducer.FlushWithTimeout(5000) // 5 second timeout
		if remaining > 0 {
			c.Logger.Warn("kafka producer flush incomplete", "remaining_messages", remaining)
		}
		c.kafkaProducer.Close()
		c.Logger.Info("kafka producer closed")
	}

	// Close database pool
	if c.DBPool != nil {
		c.DBPool.Close()
		c.Logger.Info("database pool closed")
	}

	// Close Redis client
	if c.RedisClient != nil {
		if err := c.RedisClient.Close(); err != nil {
			c.Logger.Error("failed to close redis client", "error", err)
			errs = append(errs, fmt.Errorf("redis close: %w", err))
		} else {
			c.Logger.Info("redis client closed")
		}
	}

	// Shutdown tracer
	if c.Tracer != nil {
		if err := c.Tracer.Shutdown(ctx); err != nil {
			c.Logger.Error("failed to shutdown tracer", "error", err)
			errs = append(errs, fmt.Errorf("tracer shutdown: %w", err))
		} else {
			c.Logger.Info("tracer shutdown complete")
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("%w: %v", ErrContainerCloseFailures, errs)
	}

	c.Logger.Info("container resources closed successfully")
	return nil
}
