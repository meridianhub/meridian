// Package app provides application configuration and dependency injection for the payment-order service.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/meridianhub/meridian/services/payment-order/adapters/gateway"
	pomessaging "github.com/meridianhub/meridian/services/payment-order/adapters/messaging"
	"github.com/meridianhub/meridian/services/payment-order/adapters/persistence"
	"github.com/meridianhub/meridian/services/payment-order/config"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	poobservability "github.com/meridianhub/meridian/services/payment-order/observability"
	"github.com/meridianhub/meridian/services/payment-order/service"
	"github.com/meridianhub/meridian/services/payment-order/worker"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/kafka"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/meridianhub/meridian/shared/platform/ports"
	"github.com/meridianhub/meridian/shared/platform/redislock"
	"github.com/meridianhub/meridian/shared/platform/scheduler"

	currentaccountclient "github.com/meridianhub/meridian/services/current-account/client"
	financialaccountingclient "github.com/meridianhub/meridian/services/financial-accounting/client"
	financialgatewayclient "github.com/meridianhub/meridian/services/financial-gateway/client"
	partyclient "github.com/meridianhub/meridian/services/party/client"
	poclients "github.com/meridianhub/meridian/services/payment-order/adapters/clients"
	positionkeepingclient "github.com/meridianhub/meridian/services/position-keeping/client"
	referencedataclient "github.com/meridianhub/meridian/services/reference-data/client"
)

// ErrContainerCloseFailures is returned when one or more resources fail to close.
var ErrContainerCloseFailures = errors.New("container close encountered failures")

// ErrMissingHMACSecret is returned when the WEBHOOK_HMAC_SECRET environment variable is not set.
var ErrMissingHMACSecret = errors.New("WEBHOOK_HMAC_SECRET environment variable is required")

// Container holds all application dependencies for the payment-order service.
// It manages the lifecycle of infrastructure, adapters, and background workers,
// providing a clean separation between wiring and business logic.
type Container struct {
	Config *config.ServiceConfig
	Logger *slog.Logger

	// Infrastructure
	Tracer          *observability.Tracer
	DB              *gorm.DB
	RedisClient     *redis.Client
	AuthInterceptor *auth.Interceptor

	// Repositories
	PaymentOrderRepo  *persistence.PaymentOrderRepository
	BillingRepo       *persistence.BillingRepositoryImpl
	SagaExecutionRepo *persistence.SagaExecutionRepository

	// External clients
	CurrentAccountClient      service.CurrentAccountClient
	FinancialAccountingClient service.FinancialAccountingClient
	InternalAccountClient     service.InternalAccountClient // nil when internal clearing disabled
	ReferenceDataClient       service.ReferenceDataClient   // nil when reference-data unavailable
	InternalClearingEnabled   bool

	// Payment gateway
	PaymentGateway       gateway.PaymentGateway
	GatewayAccountConfig *config.GatewayAccountConfig

	// Messaging
	OutboxRepo      *events.PostgresOutboxRepository
	OutboxPublisher *events.OutboxPublisher
	EventPublisher  *pomessaging.OutboxPublisher
	kafkaProducer   *kafka.ProtoProducer
	OutboxWorker    *events.Worker

	// Idempotency
	IdempotencyService idempotency.Service

	// Starlark
	HandlerRegistry *saga.HandlerRegistry

	// Billing workers
	BillingCronScheduler *scheduler.CronScheduler
	DunningWorker        *worker.DunningWorker

	// BootstrapServers is the Kafka bootstrap servers string, exposed so that
	// run() can decide whether to create the payment event consumer.
	BootstrapServers string

	// pgxPool is held for billing scheduler execution store cleanup.
	pgxPool *pgxpool.Pool

	// cleanups accumulates cleanup functions from external client connections.
	cleanups []func()
}

// NewContainer creates and initializes a new dependency injection container.
// It initializes infrastructure, repositories, external clients, messaging,
// and background workers in dependency order. The caller is responsible for
// calling Close when the container is no longer needed.
func NewContainer(ctx context.Context, cfg *config.ServiceConfig, logger *slog.Logger, version string) (_ *Container, err error) {
	c := &Container{
		Config: cfg,
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

	c.initRepositories()

	if err = c.initExternalClients(ctx); err != nil {
		return nil, err
	}

	if err = c.initPaymentGateway(); err != nil { //nolint:contextcheck // gateway client.New manages its own connection
		return nil, err
	}

	c.initKafka(ctx)

	if err = c.initRedis(ctx); err != nil {
		return nil, err
	}

	c.initHandlerRegistry(ctx)

	if err = c.initBillingWorkers(ctx); err != nil {
		return nil, err
	}

	if err = c.initAuth(ctx); err != nil {
		return nil, err
	}

	logger.Info("dependency container initialized successfully")
	return c, nil
}

// initTracer initializes the OpenTelemetry tracer.
func (c *Container) initTracer(ctx context.Context, version string) error {
	tracer, err := bootstrap.NewTracer(ctx, bootstrap.TracerConfig{
		ServiceName:    "payment-order-service",
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
	c.PaymentOrderRepo = persistence.NewPaymentOrderRepository(c.DB)
	c.BillingRepo = persistence.NewBillingRepository(c.DB)
	c.SagaExecutionRepo = persistence.NewSagaExecutionRepository(c.DB)

	c.Logger.Info("repositories initialized")
}

// initExternalClients creates all external gRPC clients.
// Cleanup functions are accumulated and called during Close.
func (c *Container) initExternalClients(ctx context.Context) error {
	namespace := env.GetEnvOrDefault("K8S_NAMESPACE", "default")

	// Current Account client (required)
	caClient, caCleanup, err := c.createCurrentAccountClient(namespace) //nolint:contextcheck // client.New manages its own connection
	if err != nil {
		return fmt.Errorf("failed to create current account client: %w", err)
	}
	c.CurrentAccountClient = caClient
	c.cleanups = append(c.cleanups, caCleanup)

	// Financial Accounting client (required)
	faClient, faCleanup, err := c.createFinancialAccountingClient(ctx, namespace)
	if err != nil {
		return fmt.Errorf("failed to create financial accounting client: %w", err)
	}
	c.FinancialAccountingClient = faClient
	c.cleanups = append(c.cleanups, faCleanup)

	// Internal Account client (optional - only when INTERNAL_CLEARING_ENABLED)
	c.InternalClearingEnabled = env.GetEnvAsBool("INTERNAL_CLEARING_ENABLED", false)
	if c.InternalClearingEnabled {
		iaClient, iaCleanup, err := c.createInternalAccountClient(namespace) //nolint:contextcheck // client.New manages its own connection
		if err != nil {
			return fmt.Errorf("failed to create internal account client: %w", err)
		}
		c.InternalAccountClient = iaClient
		c.cleanups = append(c.cleanups, iaCleanup)
		c.Logger.Info("internal clearing enabled - internal account client configured")
	} else {
		c.Logger.Info("internal clearing disabled - gateway-only mode")
	}

	// Reference Data client (optional - for saga definitions and instrument lookups)
	refDataClient, refDataCleanup, err := referencedataclient.New(ctx, referencedataclient.Config{
		ServiceName: referencedataclient.ServiceName,
		Namespace:   namespace,
		Port:        ports.ReferenceData,
		Timeout:     defaults.DefaultRPCTimeout,
		Tracer:      c.Tracer,
	})
	if err != nil {
		c.Logger.Warn("reference-data client unavailable, saga definitions will not be fetched",
			"error", err)
	} else {
		c.cleanups = append(c.cleanups, func() {
			if err := refDataCleanup(); err != nil {
				c.Logger.Error("failed to close reference-data client", "error", err)
			}
		})
		// Wrap the reference-data client to implement service.ReferenceDataClient interface
		c.ReferenceDataClient = poclients.NewReferenceDataClient(refDataClient.Conn())
		c.Logger.Info("reference-data client connected", "port", ports.ReferenceData)
	}

	return nil
}

// initPaymentGateway creates the payment gateway with resilience patterns.
func (c *Container) initPaymentGateway() error {
	gw, gwCleanup, err := createPaymentGateway(*c.Config, c.Logger)
	if err != nil {
		return fmt.Errorf("failed to create payment gateway: %w", err)
	}
	c.PaymentGateway = gw
	c.cleanups = append(c.cleanups, gwCleanup)
	c.Logger.Info("payment gateway initialized")

	// Load gateway account configuration
	gaCfg, err := createGatewayAccountConfig(c.Logger)
	if err != nil {
		return fmt.Errorf("failed to load gateway account config: %w", err)
	}
	c.GatewayAccountConfig = gaCfg

	return nil
}

// initKafka initializes the outbox publisher, Kafka producer, and outbox worker.
// Kafka is optional - the service operates without it (events accumulate in the outbox).
func (c *Container) initKafka(ctx context.Context) {
	c.OutboxRepo = events.NewPostgresOutboxRepository(c.DB)
	c.OutboxPublisher = events.NewOutboxPublisher("payment-order")

	c.BootstrapServers = env.GetEnvOrDefault("KAFKA_BOOTSTRAP_SERVERS", "")
	if c.BootstrapServers == "" {
		// Fall back to legacy KAFKA_BROKERS env var
		c.BootstrapServers = env.GetEnvOrDefault("KAFKA_BROKERS", "")
	}

	if c.BootstrapServers != "" {
		producer, kafkaErr := kafka.NewProtoProducer(kafka.ProducerConfig{
			BootstrapServers: c.BootstrapServers,
			ClientID:         "payment-order-outbox-worker",
			Acks:             "all",
			Retries:          3,
			Compression:      "snappy",
		})
		if kafkaErr != nil {
			c.Logger.Warn("failed to create Kafka producer for outbox worker",
				"error", kafkaErr)
		} else {
			c.kafkaProducer = producer
			workerConfig := events.DefaultWorkerConfig("payment-order")
			c.OutboxWorker = events.NewWorker(c.OutboxRepo, c.kafkaProducer, workerConfig, c.Logger)
			c.OutboxWorker.Start(ctx)
			c.Logger.Info("outbox worker started",
				"bootstrap_servers", c.BootstrapServers)
		}
	} else {
		c.Logger.Warn("outbox worker disabled - KAFKA_BOOTSTRAP_SERVERS not set (events will accumulate in outbox)")
	}

	// Create outbox-based event publisher (replaces direct Kafka producer)
	c.EventPublisher = pomessaging.NewOutboxPublisher(c.DB, c.OutboxPublisher)
}

// initRedis initializes the Redis client and idempotency service.
// In production, Redis is required (fails fast). In non-production, falls back to NoopService.
func (c *Container) initRedis(_ context.Context) error {
	var redisErr error
	c.RedisClient, redisErr = createRedisClient(c.Logger) //nolint:contextcheck // createRedisClient manages its own timeout context
	if redisErr != nil {
		if env.IsProduction() {
			c.Logger.Error("CRITICAL: Redis unavailable in production - failing fast", "error", redisErr)
			return bootstrap.Permanent(fmt.Errorf("%w: %w", ErrRedisRequiredInProduction, redisErr))
		}
		c.Logger.Warn("Redis not available at startup, using noop idempotency service - DEVELOPMENT ONLY",
			"error", redisErr,
			"environment", os.Getenv("ENVIRONMENT"))
		c.IdempotencyService = idempotency.NewNoopService(c.Logger)
		poobservability.SetNoopIdempotencyActive(true)
		poobservability.RecordServiceDegradation(poobservability.ComponentIdempotency, poobservability.DegradationReasonStartupFallback)
	} else {
		c.IdempotencyService = idempotency.NewRedisService(c.RedisClient)
		poobservability.SetNoopIdempotencyActive(false)
		c.Logger.Info("idempotency service initialized with Redis")
	}
	return nil
}

// initHandlerRegistry creates the Starlark handler registry and registers handlers
// from all available service clients.
func (c *Container) initHandlerRegistry(ctx context.Context) {
	c.HandlerRegistry = saga.NewHandlerRegistry()

	// Register handlers from existing clients (already initialized)
	c.registerExistingClientHandlers() //nolint:contextcheck // handler registration callbacks receive context at invocation time

	// Register handlers from Starlark-only clients (created here)
	c.registerStarlarkOnlyClientHandlers(ctx)

	c.Logger.Info("Starlark handler registry initialized",
		"registered_handlers", len(c.HandlerRegistry.List()))
}

// registerExistingClientHandlers registers Starlark handlers from clients already in the container.
func (c *Container) registerExistingClientHandlers() {
	if caClient, ok := c.CurrentAccountClient.(*currentaccountclient.Client); ok {
		if err := currentaccountclient.RegisterStarlarkHandlers(c.HandlerRegistry, caClient); err != nil {
			c.Logger.Warn("failed to register current-account handlers", "error", err)
		}
	} else {
		c.Logger.Warn("current-account client type assertion failed, Starlark handlers not registered")
	}

	if faClient, ok := c.FinancialAccountingClient.(*financialaccountingclient.Client); ok {
		if err := financialaccountingclient.RegisterStarlarkHandlers(c.HandlerRegistry, faClient); err != nil {
			c.Logger.Warn("failed to register financial-accounting handlers", "error", err)
		}
	} else {
		c.Logger.Warn("financial-accounting client type assertion failed, Starlark handlers not registered")
	}
}

// registerStarlarkOnlyClientHandlers creates and registers Starlark handlers for clients
// that are only needed for handler registration (position-keeping, party, financial-gateway).
func (c *Container) registerStarlarkOnlyClientHandlers(ctx context.Context) {
	namespace := env.GetEnvOrDefault("K8S_NAMESPACE", "default")

	// Position-keeping handlers (optional)
	posKeepingClient, posKeepingCleanup, err := positionkeepingclient.New(ctx, positionkeepingclient.Config{
		ServiceName: positionkeepingclient.ServiceName,
		Namespace:   namespace,
		Port:        ports.PositionKeeping,
		Timeout:     defaults.DefaultRPCTimeout,
		Tracer:      c.Tracer,
	})
	if err != nil {
		c.Logger.Warn("position-keeping client unavailable, Starlark handlers not registered", "error", err)
	} else {
		c.cleanups = append(c.cleanups, posKeepingCleanup)
		if err := positionkeepingclient.RegisterStarlarkHandlers(c.HandlerRegistry, posKeepingClient); err != nil { //nolint:contextcheck // handler callbacks receive context at invocation time
			c.Logger.Warn("failed to register position-keeping handlers", "error", err)
		}
	}

	// Party handlers (optional)
	partyClient, partyCleanup, err := partyclient.New(ctx, partyclient.Config{
		ServiceName: partyclient.ServiceName,
		Namespace:   namespace,
		Port:        ports.Party,
		Timeout:     defaults.DefaultRPCTimeout,
		Tracer:      c.Tracer,
	})
	if err != nil {
		c.Logger.Warn("party client unavailable, Starlark party handlers not registered", "error", err)
	} else {
		c.cleanups = append(c.cleanups, partyCleanup)
		if err := partyclient.RegisterStarlarkHandlers(c.HandlerRegistry, partyClient); err != nil { //nolint:contextcheck // handler callbacks receive context at invocation time
			c.Logger.Warn("failed to register party handlers", "error", err)
		}
	}

	// Financial-gateway handlers (optional)
	fgClient, fgCleanup, err := financialgatewayclient.New(financialgatewayclient.Config{ //nolint:contextcheck // client.New manages its own connection
		ServiceName: financialgatewayclient.ServiceName,
		Namespace:   namespace,
		Port:        financialgatewayclient.DefaultPort,
		Timeout:     defaults.DefaultRPCTimeout,
		Tracer:      c.Tracer,
	})
	if err != nil {
		c.Logger.Warn("financial-gateway client unavailable, Starlark financial_gateway handlers not registered", "error", err)
	} else {
		c.cleanups = append(c.cleanups, fgCleanup)
		if err := financialgatewayclient.RegisterStarlarkHandlers(c.HandlerRegistry, fgClient); err != nil { //nolint:contextcheck // handler callbacks receive context at invocation time
			c.Logger.Warn("failed to register financial-gateway handlers", "error", err)
		}
	}
}

// initBillingWorkers creates the billing scheduler and dunning worker (feature-flagged).
func (c *Container) initBillingWorkers(ctx context.Context) error {
	if !c.Config.BillingEnabled {
		c.Logger.Info("billing workers disabled (BILLING_ENABLED=false)")
		return nil
	}

	if c.RedisClient == nil {
		c.Logger.Warn("billing workers disabled - Redis unavailable (DEVELOPMENT ONLY)")
		return nil
	}

	// Create metrics once and share between billing scheduler and dunning worker
	// to avoid duplicate prometheus.MustRegister panics via promauto.
	billingMetrics := worker.NewBillingMetrics()

	if err := c.initBillingScheduler(ctx, billingMetrics); err != nil {
		return err
	}

	return c.initDunningWorker(billingMetrics)
}

// initBillingScheduler creates the billing cron scheduler with execution store and distributed lock.
func (c *Container) initBillingScheduler(ctx context.Context, billingMetrics *worker.BillingMetrics) error {
	tenantID := env.GetEnvOrDefault("BILLING_TENANT_ID", "default")

	pgxPool, execStore, err := c.createSchedulerExecutionStore(ctx)
	if err != nil {
		return err
	}
	c.pgxPool = pgxPool

	billingLock := redislock.NewLock(c.RedisClient, redislock.Config{
		KeyPrefix:  "meridian:billing:scheduler",
		LockTTL:    5 * time.Minute,
		RenewEvery: 30 * time.Second,
	}, c.Logger)

	billingExecutor := worker.NewBillingExecutor(
		c.BillingRepo,
		c.RedisClient,
		billingMetrics,
		worker.BillingExecutorConfig{ShadowMode: c.Config.BillingShadowMode},
		c.Logger,
	)

	cronOpts := []scheduler.CronSchedulerOption{}
	if execStore != nil {
		cronOpts = append(cronOpts, scheduler.WithCronExecutionStore(execStore))
	}

	c.BillingCronScheduler = scheduler.NewCronScheduler(
		worker.NewBillingScheduleProvider(tenantID, c.Config.BillingCronSchedule),
		billingExecutor,
		billingLock,
		scheduler.CronSchedulerConfig{
			Name:             "billing-scheduler",
			RefreshInterval:  60 * time.Second,
			ShutdownTimeout:  30 * time.Second,
			ExecutionTimeout: 10 * time.Minute,
			MaxCatchUpAge:    time.Hour,
		},
		c.Logger,
		cronOpts...,
	)

	c.Logger.Info("billing scheduler configured",
		"cron_schedule", c.Config.BillingCronSchedule,
		"shadow_mode", c.Config.BillingShadowMode,
		"tenant_id", tenantID)

	return nil
}

// createSchedulerExecutionStore creates the pgxpool and execution store for audit trail.
func (c *Container) createSchedulerExecutionStore(ctx context.Context) (*pgxpool.Pool, *scheduler.PgExecutionStore, error) {
	databaseURL := env.GetEnvOrDefault("DATABASE_URL", "postgres://meridian_user@localhost:26257/meridian?sslmode=disable")
	pgxPool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create pgxpool for scheduler execution store: %w", err)
	}

	execStore, err := scheduler.NewPgExecutionStore(pgxPool) //nolint:contextcheck // NewPgExecutionStore validates table existence without context
	if err != nil {
		// Only suppress missing-table errors (PostgreSQL/CockroachDB code 42P01).
		// Other errors (permissions, connectivity) should propagate.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42P01" {
			c.Logger.Warn("scheduler_execution table not found, audit trail disabled", "error", err)
			return pgxPool, nil, nil
		}
		return nil, nil, fmt.Errorf("failed to initialize scheduler execution store: %w", err)
	}
	return pgxPool, execStore, nil
}

// initDunningWorker creates the dunning escalation worker.
func (c *Container) initDunningWorker(billingMetrics *worker.BillingMetrics) error {
	// DunningCallback is a no-op for now; saga runner integration comes in Task 5
	dunningCallback := func(_ context.Context, run *domain.BillingRun) error {
		c.Logger.Info("dunning escalation triggered (no-op: saga runner not wired)",
			"billing_run_id", run.ID,
			"dunning_level", run.DunningLevel)
		return nil
	}

	dunningWorker, err := worker.NewDunningWorker(
		c.BillingRepo,
		c.RedisClient,
		worker.DunningWorkerConfig{
			PollInterval: c.Config.DunningPollInterval,
		},
		dunningCallback,
		c.Logger,
		billingMetrics,
	)
	if err != nil {
		return fmt.Errorf("failed to create dunning worker: %w", err)
	}
	c.DunningWorker = dunningWorker

	c.Logger.Info("dunning worker configured",
		"dunning_poll_interval", c.Config.DunningPollInterval)

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

	// Stop outbox worker
	if c.OutboxWorker != nil {
		c.OutboxWorker.Stop()
		c.Logger.Info("outbox worker stopped")
	}

	// Close Kafka producer
	if c.kafkaProducer != nil {
		c.kafkaProducer.Close()
		c.Logger.Info("kafka producer closed")
	}

	// Close pgxpool (billing execution store)
	if c.pgxPool != nil {
		c.pgxPool.Close()
		c.Logger.Info("pgx pool closed")
	}

	// Run client cleanups in reverse order
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

// ErrRedisRequiredInProduction is returned when Redis is unavailable in production environments.
var ErrRedisRequiredInProduction = errors.New("redis required for idempotency in production environment")
