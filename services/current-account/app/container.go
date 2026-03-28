// Package app provides application configuration and dependency injection for the current-account service.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	currentaccountclient "github.com/meridianhub/meridian/services/current-account/client"
	"github.com/meridianhub/meridian/services/current-account/config"
	caobservability "github.com/meridianhub/meridian/services/current-account/observability"
	"github.com/meridianhub/meridian/services/current-account/service"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/meridianhub/meridian/shared/platform/ports"
)

// ErrContainerCloseFailures is returned when one or more resources fail to close.
var ErrContainerCloseFailures = errors.New("container close encountered failures")

// ErrRedisRequiredInProduction is returned when Redis is unavailable in production environments.
var ErrRedisRequiredInProduction = errors.New("redis required for idempotency in production environment")

// Container holds all application dependencies for the current-account service.
// It manages the lifecycle of infrastructure, repositories, external clients, and
// background workers, providing a clean separation between wiring and business logic.
type Container struct {
	Logger *slog.Logger

	// Infrastructure
	Tracer          *observability.Tracer
	DB              *gorm.DB
	RedisClient     *redis.Client
	AuthInterceptor *auth.Interceptor

	// Repositories
	AccountRepo    *persistence.Repository
	LienRepo       *persistence.LienRepository
	WithdrawalRepo *persistence.WithdrawalRepository
	OutboxRepo     *events.PostgresOutboxRepository

	// Idempotency
	IdempotencyService idempotency.Service

	// Starlark
	HandlerRegistry *saga.HandlerRegistry

	// Service
	Service *service.Service

	// BootstrapServers is the Kafka bootstrap servers string, exposed so that
	// run() can decide whether to create the outbox worker and event consumer.
	BootstrapServers string

	// UsingNoopIdempotency tracks whether the service is running with degraded
	// idempotency (noop) for health reporting.
	UsingNoopIdempotency bool

	// cleanups accumulates cleanup functions from external client connections.
	cleanups []func()
}

// NewContainer creates and initializes a new dependency injection container.
// It initializes infrastructure, repositories, external clients, and auth in
// dependency order. The caller is responsible for calling Close when the
// container is no longer needed.
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

	if err := c.initRedis(ctx); err != nil {
		return nil, err
	}

	if err := c.initServiceClients(ctx); err != nil {
		return nil, err
	}

	if err := c.initAuth(ctx); err != nil {
		return nil, err
	}

	c.BootstrapServers = env.GetEnvOrDefault("KAFKA_BOOTSTRAP_SERVERS", "")

	succeeded = true
	logger.Info("dependency container initialized successfully")
	return c, nil
}

// initTracer initializes the OpenTelemetry tracer.
func (c *Container) initTracer(ctx context.Context, version string) error {
	tracer, err := bootstrap.NewTracer(ctx, bootstrap.TracerConfig{
		ServiceName:    "current-account-service",
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

// initRepositories creates all persistence repositories.
func (c *Container) initRepositories() {
	c.AccountRepo = persistence.NewRepository(c.DB)
	c.LienRepo = persistence.NewLienRepository(c.DB)
	c.WithdrawalRepo = persistence.NewWithdrawalRepository(c.DB)
	c.OutboxRepo = events.NewPostgresOutboxRepository(c.DB)
	c.Logger.Info("repositories initialized")
}

// initRedis initializes the Redis client and idempotency service.
// In production, Redis is required (fails fast). In non-production, falls back to NoopService.
func (c *Container) initRedis(_ context.Context) error {
	redisConfig := bootstrap.DefaultRedisConfig()
	redisConfig.Logger = c.Logger
	redisClient, redisErr := bootstrap.NewRedisClient(context.Background(), redisConfig) //nolint:contextcheck // NewRedisClient manages its own timeout context
	if redisErr != nil {
		if env.IsProduction() {
			c.Logger.Error("CRITICAL: Redis unavailable in production - failing fast",
				"error", redisErr)
			return bootstrap.Permanent(fmt.Errorf("%w: %w", ErrRedisRequiredInProduction, redisErr))
		}
		c.Logger.Warn("Redis not available at startup, using noop idempotency service - DEVELOPMENT ONLY",
			"error", redisErr,
			"environment", os.Getenv("ENVIRONMENT"))
		c.IdempotencyService = idempotency.NewNoopService(c.Logger)
		c.UsingNoopIdempotency = true
		caobservability.SetNoopIdempotencyActive(true)
		caobservability.RecordServiceDegradation(caobservability.ComponentIdempotency, caobservability.DegradationReasonStartupFallback)
	} else {
		c.RedisClient = redisClient
		c.IdempotencyService = idempotency.NewRedisService(redisClient)
		caobservability.SetNoopIdempotencyActive(false)
		c.Logger.Info("idempotency service initialized with Redis")
	}
	return nil
}

// initServiceClients creates the current-account service with its handler registry
// and self-referential Starlark client. Cross-service clients (party, position-keeping,
// financial-accounting) are NOT created here - they belong in the saga orchestration layer.
func (c *Container) initServiceClients(_ context.Context) error {
	// Load account configuration for clearing accounts (enables double-entry bookkeeping).
	// If not configured, the service operates in single-entry mode without clearing account postings.
	accountConfig, cfgErr := config.LoadAccountConfig()
	if cfgErr != nil {
		c.Logger.Warn("account configuration not loaded, operating in single-entry mode",
			"error", cfgErr)
		accountConfig = nil
	} else {
		c.Logger.Info("account configuration loaded",
			"deposit_clearing_account_id", accountConfig.DepositClearingAccountID)
	}

	// Create service without cross-service clients.
	// Cross-service operations (position logging, ledger posting, party validation,
	// clearing account resolution) are handled by the saga orchestration layer.
	svc, err := service.NewServiceWithExistingClients(
		c.AccountRepo,
		c.LienRepo,
		c.WithdrawalRepo,
		c.OutboxRepo,
		c.DB,
		nil, // posKeepingClient - delegated to saga layer
		nil, // finAcctClient - delegated to saga layer
		nil, // partyClient - delegated to saga layer
		accountConfig,
		c.IdempotencyService,
		c.Logger,
		c.Tracer,
		nil, // accountResolver - delegated to saga layer
		nil, // fungibilityValidator - delegated to saga layer
	)
	if err != nil {
		return fmt.Errorf("failed to create service: %w", err)
	}
	c.Service = svc

	// Create Starlark handler registry with self-referential service client handlers.
	// Cross-service handlers (position-keeping, financial-accounting) are registered
	// at the saga orchestration layer, not here.
	c.HandlerRegistry = saga.NewHandlerRegistry()

	namespace := env.GetEnvOrDefault("K8S_NAMESPACE", "default")

	// Register current-account handlers (for self-referential operations in sagas)
	currentAcctClient, currentAcctCleanup, clientErr := currentaccountclient.New(currentaccountclient.Config{ //nolint:contextcheck // uses background dial
		ServiceName: currentaccountclient.ServiceName,
		Namespace:   namespace,
		Port:        ports.CurrentAccount,
		Timeout:     defaults.DefaultRPCTimeout,
		Tracer:      c.Tracer,
	})
	if clientErr != nil {
		c.Logger.Warn("failed to create current-account client for Starlark handlers",
			"error", clientErr)
	} else {
		c.cleanups = append(c.cleanups, currentAcctCleanup)
		if regErr := currentaccountclient.RegisterStarlarkHandlers(c.HandlerRegistry, currentAcctClient); regErr != nil { //nolint:contextcheck // no ctx needed
			c.Logger.Warn("failed to register current-account handlers", "error", regErr)
		}
	}

	c.Logger.Info("Starlark handler registry initialized",
		"registered_handlers", len(c.HandlerRegistry.List()))

	c.Logger.Info("service boundary mode: standalone (cross-service calls delegated to saga layer)",
		"namespace", namespace)

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

// Close gracefully shuts down all container resources in reverse initialization order.
func (c *Container) Close() {
	c.Logger.Info("closing container resources...")

	// Run client cleanups in reverse order (service clients)
	for i := len(c.cleanups) - 1; i >= 0; i-- {
		c.cleanups[i]()
	}

	// Close Redis client
	if c.RedisClient != nil {
		if err := c.RedisClient.Close(); err != nil {
			c.Logger.Error("failed to close Redis client", "error", err)
		} else {
			c.Logger.Info("redis client closed")
		}
	}

	// Close database
	bootstrap.CloseDatabase(c.DB, c.Logger)

	// Shutdown tracer
	bootstrap.ShutdownTracer(c.Tracer, c.Logger)

	c.Logger.Info("container resources closed")
}
