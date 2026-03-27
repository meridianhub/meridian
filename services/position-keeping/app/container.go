package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	internalaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"github.com/meridianhub/meridian/services/position-keeping/adapters/messaging"
	"github.com/meridianhub/meridian/services/position-keeping/adapters/persistence"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/services/position-keeping/observability"
	"github.com/meridianhub/meridian/services/position-keeping/service"
	"github.com/meridianhub/meridian/services/position-keeping/worker"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/pkg/refdata"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/kafka"
	pkobservability "github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// ErrRedisRequiredInProduction is returned when Redis is unavailable in production environments.
var ErrRedisRequiredInProduction = errors.New("redis required for idempotency in production environment")

// Container holds all application dependencies
type Container struct {
	Config *Config
	Logger *slog.Logger

	// Infrastructure
	DBPool          *pgxpool.Pool
	RedisClient     *redis.Client
	Tracer          *pkobservability.Tracer
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

	// Idempotency
	IdempotencyService idempotency.Service

	// Account Validation
	ServiceOpts []service.Option

	// Compaction Worker
	CompactionWorker *worker.CompactionWorker

	// gRPC connections for cleanup
	grpcConns []*grpc.ClientConn
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

	if err := container.initializeIdempotency(); err != nil {
		return nil, err
	}

	if err := container.initializeAccountValidation(); err != nil { //nolint:contextcheck // instrument resolver manages its own gRPC connections
		return nil, err
	}

	container.initializeCompactionWorker()

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

	tracerConfig, err := pkobservability.DefaultConfig()
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

	tracer, err := pkobservability.NewTracer(ctx, tracerConfig)
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

// initializeIdempotency creates the idempotency service.
// In production, Redis is required (fails fast). In non-production, falls back to NoopService.
func (c *Container) initializeIdempotency() error {
	if c.RedisClient != nil {
		c.IdempotencyService = idempotency.NewRedisService(c.RedisClient)
		observability.SetNoopIdempotencyActive(false)
		c.Logger.Info("idempotency service enabled with Redis")
		return nil
	}

	if c.Config.Redis.Enabled {
		// Redis is configured but was not available at container startup
		if env.IsProduction() {
			c.Logger.Error("CRITICAL: Redis unavailable in production - failing fast",
				"environment", os.Getenv("ENVIRONMENT"))
			return ErrRedisRequiredInProduction
		}
		c.Logger.Warn("Redis not available at startup, using noop idempotency service - DEVELOPMENT ONLY",
			"environment", os.Getenv("ENVIRONMENT"))
	} else {
		c.Logger.Warn("Redis not configured, using noop idempotency service - DEVELOPMENT ONLY",
			"environment", os.Getenv("ENVIRONMENT"))
	}

	c.IdempotencyService = idempotency.NewNoopService(c.Logger)
	observability.SetNoopIdempotencyActive(true)
	observability.RecordServiceDegradation(observability.ComponentIdempotency, observability.DegradationReasonStartupFallback)
	return nil
}

// initializeAccountValidation creates account validators, appending service options to ServiceOpts.
func (c *Container) initializeAccountValidation() error {
	if !c.Config.AccountValidation.Enabled {
		c.Logger.Info("account validation disabled")
		return c.initializeInstrumentResolver()
	}

	currentAccountValidator, err := c.createCurrentAccountValidator()
	if err != nil {
		return err
	}

	internalAccountValidator, err := c.createInternalAccountValidator()
	if err != nil {
		return err
	}

	compositeValidator, compositeErr := service.NewCompositeAccountValidator(service.CompositeAccountValidatorConfig{
		CurrentAccountValidator:  currentAccountValidator,
		InternalAccountValidator: internalAccountValidator,
		Logger:                   c.Logger,
	})
	if compositeErr != nil {
		return fmt.Errorf("failed to create composite account validator: %w", compositeErr)
	}

	c.ServiceOpts = append(c.ServiceOpts,
		service.WithAccountValidator(compositeValidator),
		service.WithAccountValidationEnabled(true),
	)

	c.Logger.Info("account validation enabled",
		"current_account_url", c.Config.AccountValidation.CurrentAccountServiceURL,
		"internal_account_url", c.Config.AccountValidation.InternalAccountServiceURL,
		"cache_ttl", c.Config.AccountValidation.CacheTTL)

	return c.initializeInstrumentResolver()
}

// createCurrentAccountValidator creates the Current Account validator if URL is configured.
func (c *Container) createCurrentAccountValidator() (*service.CurrentAccountValidator, error) {
	if c.Config.AccountValidation.CurrentAccountServiceURL == "" {
		return nil, nil //nolint:nilnil // nil validator is valid when URL not configured
	}

	conn, connErr := grpc.NewClient(
		c.Config.AccountValidation.CurrentAccountServiceURL,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if connErr != nil {
		return nil, fmt.Errorf("failed to create current account client at %s: %w",
			c.Config.AccountValidation.CurrentAccountServiceURL, connErr)
	}
	c.grpcConns = append(c.grpcConns, conn)

	client := currentaccountv1.NewCurrentAccountServiceClient(conn)
	validator, validatorErr := service.NewCurrentAccountValidator(service.CurrentAccountValidatorConfig{
		Client:        &CurrentAccountClientAdapter{Client: client},
		Logger:        c.Logger,
		CacheTTL:      c.Config.AccountValidation.CacheTTL,
		LookupTimeout: c.Config.AccountValidation.ConnectionTimeout,
	})
	if validatorErr != nil {
		return nil, fmt.Errorf("failed to create current account validator: %w", validatorErr)
	}
	c.Logger.Info("current account validator configured",
		"url", c.Config.AccountValidation.CurrentAccountServiceURL)
	return validator, nil
}

// createInternalAccountValidator creates the Internal Account validator if URL is configured.
func (c *Container) createInternalAccountValidator() (*service.InternalAccountValidator, error) {
	if c.Config.AccountValidation.InternalAccountServiceURL == "" {
		return nil, nil //nolint:nilnil // nil validator is valid when URL not configured
	}

	conn, connErr := grpc.NewClient(
		c.Config.AccountValidation.InternalAccountServiceURL,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if connErr != nil {
		return nil, fmt.Errorf("failed to create internal account client at %s: %w",
			c.Config.AccountValidation.InternalAccountServiceURL, connErr)
	}
	c.grpcConns = append(c.grpcConns, conn)

	client := internalaccountv1.NewInternalAccountServiceClient(conn)
	validator, validatorErr := service.NewInternalAccountValidator(service.InternalAccountValidatorConfig{
		Client:        &InternalAccountClientAdapter{Client: client},
		Logger:        c.Logger,
		CacheTTL:      c.Config.AccountValidation.CacheTTL,
		LookupTimeout: c.Config.AccountValidation.ConnectionTimeout,
	})
	if validatorErr != nil {
		return nil, fmt.Errorf("failed to create internal account validator: %w", validatorErr)
	}
	c.Logger.Info("internal account validator configured",
		"url", c.Config.AccountValidation.InternalAccountServiceURL)
	return validator, nil
}

// initializeInstrumentResolver initializes the Reference Data InstrumentResolver (optional).
func (c *Container) initializeInstrumentResolver() error {
	if c.Config.ReferenceData.ServiceURL == "" {
		c.Logger.Info("instrument resolver disabled (REFERENCE_DATA_SERVICE_URL not configured)")
		return nil
	}

	conn, connErr := grpc.NewClient(
		c.Config.ReferenceData.ServiceURL,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if connErr != nil {
		return fmt.Errorf("failed to create reference data client at %s: %w",
			c.Config.ReferenceData.ServiceURL, connErr)
	}
	c.grpcConns = append(c.grpcConns, conn)

	refDataClient := referencedatav1.NewReferenceDataServiceClient(conn)
	dataSource := refdata.NewGRPCDataSource(refDataClient)
	cachedResolver := refdata.NewCachedResolver(dataSource, refdata.CachedResolverConfig{
		Logger: c.Logger,
	})

	ctx := context.Background()
	if preloadErr := cachedResolver.Preload(ctx); preloadErr != nil {
		c.Logger.Warn("failed to preload instrument cache from reference data, will resolve on demand",
			"error", preloadErr)
	}

	c.ServiceOpts = append(c.ServiceOpts, service.WithInstrumentResolver(cachedResolver))
	c.Logger.Info("instrument resolver configured",
		"url", c.Config.ReferenceData.ServiceURL)
	return nil
}

// initializeCompactionWorker creates the background compaction worker (if enabled).
func (c *Container) initializeCompactionWorker() {
	if !c.Config.Compaction.Enabled {
		c.Logger.Info("compaction worker disabled")
		return
	}

	compactionConfig := worker.CompactionConfig{
		RunInterval:       c.Config.Compaction.RunInterval,
		FragmentThreshold: c.Config.Compaction.FragmentThreshold,
		BatchSize:         c.Config.Compaction.BatchSize,
	}
	compactionWorker, workerErr := worker.NewCompactionWorker(c.DBPool, compactionConfig, c.Logger)
	if workerErr != nil {
		c.Logger.Error("failed to create compaction worker", "error", workerErr)
		return
	}
	c.CompactionWorker = compactionWorker
	c.Logger.Info("compaction worker enabled",
		"run_interval", c.Config.Compaction.RunInterval,
		"fragment_threshold", c.Config.Compaction.FragmentThreshold,
		"batch_size", c.Config.Compaction.BatchSize)
}

// CurrentAccountClientAdapter adapts the generated gRPC client to the service.CurrentAccountClient interface.
type CurrentAccountClientAdapter struct {
	Client currentaccountv1.CurrentAccountServiceClient
}

// RetrieveCurrentAccount implements service.CurrentAccountClient by delegating to the generated client.
func (a *CurrentAccountClientAdapter) RetrieveCurrentAccount(ctx context.Context, req *currentaccountv1.RetrieveCurrentAccountRequest) (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
	return a.Client.RetrieveCurrentAccount(ctx, req)
}

// InternalAccountClientAdapter adapts the generated gRPC client to the service.InternalAccountClient interface.
type InternalAccountClientAdapter struct {
	Client internalaccountv1.InternalAccountServiceClient
}

// RetrieveInternalAccount implements service.InternalAccountClient by delegating to the generated client.
func (a *InternalAccountClientAdapter) RetrieveInternalAccount(ctx context.Context, req *internalaccountv1.RetrieveInternalAccountRequest) (*internalaccountv1.RetrieveInternalAccountResponse, error) {
	return a.Client.RetrieveInternalAccount(ctx, req)
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

	// Close gRPC connections (account validators, reference data)
	for _, conn := range c.grpcConns {
		if err := conn.Close(); err != nil {
			c.Logger.Error("failed to close gRPC connection", "error", err)
			errs = append(errs, fmt.Errorf("grpc connection close: %w", err))
		}
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
