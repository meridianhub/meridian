package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/services/position-keeping/adapters/persistence"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/shared/platform/auth"
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

	// Adapters
	EventPublisher domain.EventPublisher

	// Repository
	PositionLogRepository domain.FinancialPositionLogRepository
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

	if err := container.initializeRedis(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize redis: %w", err)
	}

	container.initializeEventPublisher()

	container.initializeRepositories()

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

	// Configure connection pool
	// #nosec G115 -- overflow validated in config.Validate()
	poolConfig.MaxConns = int32(c.Config.Database.MaxOpenConns)
	// #nosec G115 -- overflow validated in config.Validate()
	poolConfig.MinConns = int32(c.Config.Database.MaxIdleConns)
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

// initializeEventPublisher initializes Kafka event publisher or no-op publisher
func (c *Container) initializeEventPublisher() {
	if !c.Config.Kafka.Enabled {
		c.Logger.Info("kafka disabled, using no-op event publisher")
		c.EventPublisher = domain.NewNoOpEventPublisher()
		return
	}

	// For now, use no-op publisher until Kafka integration is fully wired
	// TODO: Implement Kafka producer initialization when Kafka config is ready
	c.Logger.Info("kafka producer not yet implemented, using no-op event publisher")
	c.EventPublisher = domain.NewNoOpEventPublisher()
}

// initializeRepositories initializes domain repositories
func (c *Container) initializeRepositories() {
	// Create PostgreSQL repository
	c.PositionLogRepository = persistence.NewPostgresRepository(c.DBPool)

	c.Logger.Info("repositories initialized")
}

// Close gracefully closes all resources in the container
func (c *Container) Close(ctx context.Context) error {
	c.Logger.Info("closing container resources...")

	var errs []error

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
