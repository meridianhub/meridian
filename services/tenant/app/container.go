// Package app provides application configuration and dependency injection for the tenant service.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	"github.com/meridianhub/meridian/services/tenant/provisioner"
	"github.com/meridianhub/meridian/services/tenant/service"
	"github.com/meridianhub/meridian/services/tenant/worker"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/meridianhub/meridian/shared/platform/ports"

	// Service-owned client (standardized client package from party service)
	partyclient "github.com/meridianhub/meridian/services/party/client"
)

// envValueTrue is the string value for enabled environment variables.
const envValueTrue = "true"

// Sentinel errors for worker configuration validation.
var (
	errInvalidPollInterval    = errors.New("poll interval must be >= 1s")
	errInvalidMaxRetries      = errors.New("max retries must be >= 0 and <= 20")
	errInvalidRetryBaseDelay  = errors.New("retry base delay must be > 0")
	errInvalidRetryMaxDelay   = errors.New("retry max delay must be > 0")
	errInvalidRetryDelayRange = errors.New("retry base delay must be < retry max delay")
	errInvalidMaxConcurrent   = errors.New("max concurrent must be >= 1 and <= 100")
)

// Container holds all application dependencies for the tenant service.
type Container struct {
	Logger *slog.Logger

	// Infrastructure
	Tracer      *observability.Tracer
	DB          *gorm.DB
	RedisClient *redis.Client

	// Auth
	AuthInterceptor  *auth.Interceptor
	UsePlatformAdmin bool

	// Repositories
	Repo *persistence.Repository

	// Schema provisioning
	SchemaProvisioner provisioner.SchemaProvisioner

	// Party client
	PartyClient service.PartyClient

	// Caching
	SlugCache *service.SlugCache

	// Service layer
	TenantService  *service.Service
	CachedRegistry *service.CachedRegistry

	// Workers
	ProvisioningWorker *worker.ProvisioningWorker

	// Internal lifecycle
	cancelRegistry context.CancelFunc
	cleanups       []func()
}

// NewContainer creates and initializes a new dependency injection container.
func NewContainer(ctx context.Context, logger *slog.Logger, version string) (*Container, error) {
	c := &Container{
		Logger:           logger,
		UsePlatformAdmin: true,
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

	if err := c.initSchemaProvisioner(); err != nil {
		return nil, err
	}

	if err := c.initPartyClient(ctx); err != nil {
		return nil, err
	}

	if err := c.initRedis(ctx); err != nil {
		return nil, err
	}

	c.initService(ctx)

	if err := c.initProvisioningWorker(ctx); err != nil {
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
		ServiceName:    "tenant-service",
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

// initRepositories creates persistence repositories.
func (c *Container) initRepositories() {
	c.Repo = persistence.NewRepository(c.DB)
	c.Logger.Info("repositories initialized")
}

// initSchemaProvisioner initializes the schema provisioner if SCHEMA_PROVISIONING_ENABLED is "true".
// When disabled, tenant creation will not provision per-tenant schemas.
func (c *Container) initSchemaProvisioner() error {
	provisioningEnabled := env.GetEnvOrDefault("SCHEMA_PROVISIONING_ENABLED", "false")
	if provisioningEnabled != envValueTrue {
		c.Logger.Warn("schema provisioning not enabled - tenant creation will not provision schemas",
			"hint", "set SCHEMA_PROVISIONING_ENABLED=true to enable schema provisioning")
		return nil
	}

	config := provisioner.DefaultConfig()

	// Pass platform database connection (for tenant_provisioning table).
	// The provisioner will also connect to each service's database for schema creation.
	prov, err := provisioner.NewPostgresProvisioner(c.DB, config)
	if err != nil {
		return fmt.Errorf("failed to create schema provisioner: %w", err)
	}
	c.SchemaProvisioner = prov

	// Register cleanup for service database connections
	c.cleanups = append(c.cleanups, func() {
		if err := prov.Close(); err != nil {
			c.Logger.Error("failed to close provisioner connections", "error", err)
		}
	})

	c.Logger.Info("schema provisioner initialized",
		"services", len(config.Services),
		"provisioning_timeout", config.ProvisioningTimeout)

	return nil
}

// initPartyClient initializes the Party gRPC client with resilience patterns.
// The client is optional - when PARTY_SERVICE_ENABLED is not "true", party registration
// is skipped during tenant creation.
func (c *Container) initPartyClient(ctx context.Context) error {
	namespace := env.GetEnvOrDefault("K8S_NAMESPACE", "default")
	partyEnabled := env.GetEnvOrDefault("PARTY_SERVICE_ENABLED", envValueTrue) == envValueTrue
	if !partyEnabled {
		c.Logger.Warn("party client not configured - tenant creation will not register parties",
			"hint", "set PARTY_SERVICE_ENABLED=true to enable party registration")
		return nil
	}

	c.Logger.Info("connecting to party service",
		"service", partyclient.ServiceName,
		"namespace", namespace,
		"port", ports.Party)

	// Configure resilience settings from environment
	resilientConfig := &sharedclients.ResilientClientConfig{
		// Circuit breaker settings
		CircuitBreakerName:     "party",
		CircuitBreakerTimeout:  env.GetEnvAsDuration("PARTY_CIRCUIT_BREAKER_TIMEOUT", 30*time.Second),
		CircuitBreakerInterval: env.GetEnvAsDuration("PARTY_CIRCUIT_BREAKER_INTERVAL", 60*time.Second),
		MaxRequests:            env.GetEnvAsUint32("PARTY_CIRCUIT_BREAKER_MAX_REQUESTS", 1),
		FailureThreshold:       env.GetEnvAsUint32("PARTY_CIRCUIT_BREAKER_FAILURE_THRESHOLD", 5),

		// Retry settings
		MaxRetries:          env.GetEnvAsInt("PARTY_MAX_RETRIES", 3),
		InitialInterval:     env.GetEnvAsDuration("PARTY_RETRY_INITIAL_INTERVAL", 100*time.Millisecond),
		MaxInterval:         env.GetEnvAsDuration("PARTY_RETRY_MAX_INTERVAL", 5*time.Second),
		Multiplier:          env.GetEnvAsFloat("PARTY_RETRY_MULTIPLIER", 2.0),
		RandomizationFactor: env.GetEnvAsFloat("PARTY_RETRY_RANDOMIZATION", 0.5),

		Logger: c.Logger,
	}

	c.Logger.Info("party client configured with resilience patterns",
		"circuit_breaker_timeout", resilientConfig.CircuitBreakerTimeout,
		"circuit_breaker_failure_threshold", resilientConfig.FailureThreshold,
		"max_retries", resilientConfig.MaxRetries,
	)

	// Use the service-owned client package with DNS-based load balancing
	pc, cleanup, err := partyclient.New(ctx, partyclient.Config{
		ServiceName: partyclient.ServiceName,
		Namespace:   namespace,
		Port:        ports.Party,
		Timeout:     env.GetEnvAsDuration("PARTY_TIMEOUT", partyclient.DefaultTimeout),
		Tracer:      c.Tracer,
		Resilience:  resilientConfig,
	})
	if err != nil {
		return fmt.Errorf("failed to create party client: %w", err)
	}

	// Wrap with adapter to implement tenant-specific PartyClient interface
	adapter := service.NewPartyClientAdapter(pc, cleanup)
	c.PartyClient = adapter

	c.cleanups = append(c.cleanups, func() {
		if err := adapter.Close(); err != nil {
			c.Logger.Error("failed to close party client", "error", err)
		}
	})

	c.Logger.Info("party client initialized",
		"service_name", partyclient.ServiceName,
		"namespace", namespace,
		"port", ports.Party)

	return nil
}

// initRedis initializes the Redis client and slug cache.
// Redis is optional - when unavailable, slug caching is disabled until next restart.
// The slug cache is a performance optimization; the service operates correctly without it.
func (c *Container) initRedis(ctx context.Context) error {
	redisEnabled := env.GetEnvOrDefault("REDIS_ENABLED", envValueTrue) == envValueTrue
	if !redisEnabled {
		c.Logger.Warn("Redis not enabled - slug caching disabled",
			"hint", "set REDIS_ENABLED=true to enable slug caching")
		return nil
	}

	redisConfig := bootstrap.DefaultRedisConfig()
	redisConfig.Logger = c.Logger
	redisClient, err := bootstrap.NewRedisClient(ctx, redisConfig)
	if err != nil {
		c.Logger.Warn("Redis not available at startup, slug caching disabled",
			"error", err,
			"hint", "slug caching will be available after service restart when Redis is reachable")
		return nil
	}

	c.RedisClient = redisClient
	c.SlugCache = service.NewSlugCache(redisClient)

	c.cleanups = append(c.cleanups, func() {
		if err := redisClient.Close(); err != nil {
			c.Logger.Error("failed to close Redis client", "error", err)
		}
	})

	c.Logger.Info("slug cache initialized with Redis backend")
	return nil
}

// initService creates the tenant gRPC service and cached registry.
func (c *Container) initService(ctx context.Context) {
	c.TenantService = service.NewService(c.Repo, c.SchemaProvisioner, c.PartyClient, c.SlugCache, c.Logger)

	c.CachedRegistry = service.NewCachedRegistry(c.Repo, service.CachedRegistryConfig{
		RefreshInterval: 60 * time.Second,
		Logger:          c.Logger,
	})

	registryCtx, cancel := context.WithCancel(ctx)
	c.cancelRegistry = cancel
	c.CachedRegistry.Start(registryCtx)

	c.Logger.Info("cached tenant registry started",
		"refresh_interval", "60s")
}

// initProvisioningWorker creates and starts the provisioning worker if schema provisioning is enabled.
func (c *Container) initProvisioningWorker(ctx context.Context) error {
	if c.SchemaProvisioner == nil {
		c.Logger.Info("provisioning worker disabled",
			"hint", "set SCHEMA_PROVISIONING_ENABLED=true to enable background provisioning")
		return nil
	}

	config, err := loadWorkerConfig()
	if err != nil {
		return bootstrap.Permanent(fmt.Errorf("failed to load worker configuration: %w", err))
	}

	pw, err := worker.NewProvisioningWorker(
		c.Repo,
		c.SchemaProvisioner,
		worker.Config{
			PollInterval:   config.PollInterval,
			MaxRetries:     config.MaxRetries,
			RetryBaseDelay: config.RetryBaseDelay,
			RetryMaxDelay:  config.RetryMaxDelay,
			MaxConcurrent:  config.MaxConcurrent,
		},
		c.Logger,
	)
	if err != nil {
		return fmt.Errorf("failed to create provisioning worker: %w", err)
	}

	c.ProvisioningWorker = pw

	// Start worker in background goroutine
	go pw.Start(ctx)

	c.Logger.Info("provisioning worker started")
	return nil
}

// initAuth initializes the auth interceptor.
// The tenant service uses PlatformAdmin authorization.
func (c *Container) initAuth(ctx context.Context) error {
	authConfig := bootstrap.DefaultAuthConfig(c.Logger)
	interceptor, err := bootstrap.NewAuthInterceptor(ctx, authConfig)
	if err != nil {
		return fmt.Errorf("failed to initialize auth: %w", err)
	}
	c.AuthInterceptor = interceptor
	return nil
}

// Close gracefully shuts down all container resources in reverse initialization order.
func (c *Container) Close() {
	c.Logger.Info("closing container resources...")

	// Cancel the cached registry refresh goroutine first
	if c.cancelRegistry != nil {
		c.cancelRegistry()
	}

	// Stop provisioning worker
	if c.ProvisioningWorker != nil {
		c.Logger.Info("stopping provisioning worker...")
		c.ProvisioningWorker.Stop()
		c.Logger.Info("provisioning worker stopped")
	}

	// Run cleanup functions in reverse order (provisioner, party client, Redis)
	for i := len(c.cleanups) - 1; i >= 0; i-- {
		c.cleanups[i]()
	}

	// Close database
	bootstrap.CloseDatabase(c.DB, c.Logger)

	// Shutdown tracer
	bootstrap.ShutdownTracer(c.Tracer, c.Logger)

	c.Logger.Info("container resources closed")
}

// loadWorkerConfig loads worker configuration from environment variables with defaults.
// It validates the configuration and returns an error if any value is invalid.
func loadWorkerConfig() (workerConfig, error) {
	config := workerConfig{
		PollInterval:   env.GetEnvAsDuration("PROVISIONING_WORKER_POLL_INTERVAL", 10*time.Second),
		MaxRetries:     env.GetEnvAsInt("PROVISIONING_MAX_RETRIES", 5),
		RetryBaseDelay: env.GetEnvAsDuration("PROVISIONING_RETRY_BASE_DELAY", 2*time.Second),
		RetryMaxDelay:  env.GetEnvAsDuration("PROVISIONING_RETRY_MAX_DELAY", defaults.DefaultMaxRetryInterval),
		MaxConcurrent:  env.GetEnvAsInt("PROVISIONING_MAX_CONCURRENT", 5),
	}

	if config.PollInterval < 1*time.Second {
		return workerConfig{}, fmt.Errorf("%w: got %s", errInvalidPollInterval, config.PollInterval)
	}
	if config.MaxRetries < 0 || config.MaxRetries > 20 {
		return workerConfig{}, fmt.Errorf("%w: got %d", errInvalidMaxRetries, config.MaxRetries)
	}
	if config.RetryBaseDelay <= 0 {
		return workerConfig{}, fmt.Errorf("%w: got %s", errInvalidRetryBaseDelay, config.RetryBaseDelay)
	}
	if config.RetryMaxDelay <= 0 {
		return workerConfig{}, fmt.Errorf("%w: got %s", errInvalidRetryMaxDelay, config.RetryMaxDelay)
	}
	if config.RetryBaseDelay >= config.RetryMaxDelay {
		return workerConfig{}, fmt.Errorf("%w: base=%s, max=%s", errInvalidRetryDelayRange, config.RetryBaseDelay, config.RetryMaxDelay)
	}
	if config.MaxConcurrent < 1 || config.MaxConcurrent > 100 {
		return workerConfig{}, fmt.Errorf("%w: got %d", errInvalidMaxConcurrent, config.MaxConcurrent)
	}

	slog.Info("worker configuration loaded",
		"poll_interval", config.PollInterval,
		"max_retries", config.MaxRetries,
		"retry_base_delay", config.RetryBaseDelay,
		"retry_max_delay", config.RetryMaxDelay,
		"max_concurrent", config.MaxConcurrent)

	return config, nil
}

// workerConfig holds configuration for the provisioning worker behavior.
// This is internal to the container - the exported WorkerConfig and its validation
// remain in main.go for backward compatibility.
type workerConfig struct {
	PollInterval   time.Duration
	MaxRetries     int
	RetryBaseDelay time.Duration
	RetryMaxDelay  time.Duration
	MaxConcurrent  int
}
