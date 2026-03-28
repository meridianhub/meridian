// Package app provides application configuration and dependency injection for the reconciliation service.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"gorm.io/gorm"

	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	reconciliationv1 "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"github.com/meridianhub/meridian/services/reconciliation/adapters/messaging"
	"github.com/meridianhub/meridian/services/reconciliation/adapters/persistence"
	"github.com/meridianhub/meridian/services/reconciliation/config"
	"github.com/meridianhub/meridian/services/reconciliation/service"
	"github.com/meridianhub/meridian/services/reconciliation/worker"
	"github.com/meridianhub/meridian/shared/pkg/valuation"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/kafka"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/meridianhub/meridian/shared/platform/redislock"
	"github.com/meridianhub/meridian/shared/platform/scheduler"
)

// Container holds all application dependencies for the reconciliation service.
type Container struct {
	Config *config.Config
	Logger *slog.Logger

	// Infrastructure
	Tracer      *observability.Tracer
	DB          *gorm.DB
	RedisClient *redis.Client

	// Auth
	AuthInterceptor *auth.Interceptor

	// Messaging
	OutboxRepo      *events.PostgresOutboxRepository
	OutboxPublisher *events.OutboxPublisher
	OutboxWorker    *events.Worker
	kafkaProducer   *kafka.ProtoProducer

	// Event publisher (outbox-based)
	EventPublisher *messaging.OutboxEventPublisher

	// Repositories
	RunRepo       *persistence.SettlementRunRepository
	SnapshotRepo  *persistence.SettlementSnapshotRepository
	VarianceRepo  *persistence.VarianceRepository
	DisputeRepo   *persistence.DisputeRepository
	AssertionRepo *persistence.BalanceAssertionRepository
	TrendRepo     *persistence.ImbalanceTrendRepository

	// Service
	ReconciliationService *service.AccountReconciliationService

	// Scheduler (may be nil if disabled or Redis unavailable)
	CronScheduler *scheduler.CronScheduler

	// Cleanup functions for gRPC connections and pgx pools
	cleanups []func()
}

// NewContainer creates and initializes a new dependency injection container.
func NewContainer(ctx context.Context, cfg *config.Config, logger *slog.Logger, version string) (*Container, error) {
	c := &Container{
		Config: cfg,
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

	if err := c.initKafka(ctx); err != nil {
		return nil, err
	}

	c.initRepositories()

	serviceOpts, err := c.initServiceDeps()
	if err != nil {
		return nil, err
	}

	c.initService(serviceOpts)

	if err := c.initRedis(ctx); err != nil {
		return nil, err
	}

	c.initScheduler(ctx)

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
		ServiceName:    c.Config.Observability.ServiceName,
		ServiceVersion: version,
		Logger:         c.Logger,
	})
	if err != nil {
		return fmt.Errorf("failed to initialize tracer: %w", err)
	}
	c.Tracer = tracer
	return nil
}

// initDatabase initializes the GORM database connection with reconciliation-specific pool settings.
func (c *Container) initDatabase(ctx context.Context) error {
	dbConfig := bootstrap.DefaultDatabaseConfig()
	dbConfig.DSN = c.Config.Database.URL
	dbConfig.MaxOpenConns = c.Config.Database.MaxOpenConns
	dbConfig.MaxIdleConns = c.Config.Database.MaxIdleConns
	dbConfig.ConnMaxLifetime = c.Config.Database.ConnMaxLifetime
	dbConfig.ConnMaxIdleTime = c.Config.Database.ConnMaxIdleTime
	dbConfig.Logger = c.Logger

	db, err := bootstrap.NewDatabase(ctx, dbConfig)
	if err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}
	c.DB = db
	c.Logger.Info("database connection established")
	return nil
}

// initKafka initializes the outbox publisher, Kafka producer, and outbox worker.
func (c *Container) initKafka(ctx context.Context) error {
	c.OutboxRepo = events.NewPostgresOutboxRepository(c.DB)
	c.OutboxPublisher = events.NewOutboxPublisher("reconciliation")

	bootstrapServers := env.GetEnvOrDefault("KAFKA_BOOTSTRAP_SERVERS", "")
	if bootstrapServers == "" && c.Config.Kafka.Enabled {
		bootstrapServers = c.Config.Kafka.Brokers
	}

	if bootstrapServers != "" {
		producer, kafkaErr := kafka.NewProtoProducer(kafka.ProducerConfig{
			BootstrapServers: bootstrapServers,
			ClientID:         "reconciliation-outbox-worker",
			Acks:             "all",
			Retries:          3,
			Compression:      "snappy",
		})
		if kafkaErr != nil {
			c.Logger.Warn("failed to create Kafka producer for outbox worker",
				"error", kafkaErr)
		} else {
			c.kafkaProducer = producer
			workerConfig := events.DefaultWorkerConfig("reconciliation")
			c.OutboxWorker = events.NewWorker(c.OutboxRepo, c.kafkaProducer, workerConfig, c.Logger)
			c.OutboxWorker.Start(ctx)
			c.Logger.Info("outbox worker started",
				"bootstrap_servers", bootstrapServers)
		}
	} else {
		c.Logger.Warn("outbox worker disabled - KAFKA_BOOTSTRAP_SERVERS not set")
	}

	// Create outbox-based event publisher
	c.EventPublisher = messaging.NewOutboxEventPublisher(c.DB, c.OutboxPublisher)
	return nil
}

// initRepositories creates all persistence repositories.
func (c *Container) initRepositories() {
	c.RunRepo = persistence.NewSettlementRunRepository(c.DB)
	c.SnapshotRepo = persistence.NewSettlementSnapshotRepository(c.DB)
	c.VarianceRepo = persistence.NewVarianceRepository(c.DB)
	c.DisputeRepo = persistence.NewDisputeRepository(c.DB)
	c.AssertionRepo = persistence.NewBalanceAssertionRepository(c.DB)
	c.TrendRepo = persistence.NewImbalanceTrendRepository(c.DB)
	c.Logger.Info("repositories initialized")
}

// initServiceDeps wires the optional service dependencies (snapshot capturer, balance assertor,
// variance detector, variance valuator, account party resolver) and returns the service options.
func (c *Container) initServiceDeps() ([]service.Option, error) {
	serviceOpts := []service.Option{
		service.WithSettlementRunRepository(c.RunRepo),
		service.WithDisputeRepository(c.DisputeRepo),
		service.WithDisputeListRepository(c.DisputeRepo),
		service.WithAssertionListRepository(c.AssertionRepo),
		service.WithVarianceRepository(c.VarianceRepo),
		service.WithVarianceListRepository(c.VarianceRepo),
		service.WithEventPublisher(c.EventPublisher),
		service.WithLogger(c.Logger),
	}

	// Wire SnapshotCapturer and BalanceAssertor if Position Keeping URL is configured
	if c.Config.Services.PositionKeepingURL != "" {
		pkConn, connErr := grpc.NewClient(
			c.Config.Services.PositionKeepingURL,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if connErr != nil {
			return nil, fmt.Errorf("failed to create position keeping client at %s: %w",
				c.Config.Services.PositionKeepingURL, connErr)
		}
		c.cleanups = append(c.cleanups, func() {
			if err := pkConn.Close(); err != nil {
				c.Logger.Error("failed to close position keeping connection", "error", err)
			}
		})

		pkClient := positionkeepingv1.NewPositionKeepingServiceClient(pkConn)
		provider := service.NewPKPositionProvider(pkClient)
		capturer := service.NewSnapshotCapturer(provider, c.RunRepo, c.SnapshotRepo)
		serviceOpts = append(serviceOpts,
			service.WithSnapshotCapturer(capturer.CaptureSnapshots),
		)
		c.Logger.Info("snapshot capturer configured",
			"position_keeping_url", c.Config.Services.PositionKeepingURL)

		// Wire BalanceAssertor (requires PK client for position summaries)
		balancePKClient := service.NewGrpcPositionKeepingClient(pkClient)
		assertor := service.NewBalanceAssertor(c.AssertionRepo, c.TrendRepo, balancePKClient, nil, nil, c.Logger)
		serviceOpts = append(serviceOpts,
			service.WithBalanceAssertor(assertor),
		)
		c.Logger.Info("balance assertor configured")
	} else {
		c.Logger.Warn("snapshot capturer not configured: POSITION_KEEPING_URL not set")
		c.Logger.Warn("balance assertor not configured: position keeping client unavailable")
	}

	// Wire VarianceDetector (depends on repos only, always available)
	detector := service.NewVarianceDetector(c.RunRepo, c.SnapshotRepo, c.VarianceRepo)
	serviceOpts = append(serviceOpts,
		service.WithVarianceDetector(detector.DetectVariances),
	)

	// Wire VarianceValuator with valuation engine and reference data provider
	valuationEngine, refDataProvider := c.buildValuationComponents()

	// Wire AccountPartyResolver if Current Account URL is configured
	var partyResolver service.AccountPartyResolver
	if c.Config.Services.CurrentAccountURL != "" {
		caConn, caErr := grpc.NewClient(
			c.Config.Services.CurrentAccountURL,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if caErr != nil {
			c.Logger.Warn("failed to create current account gRPC client, party resolution will fall back to account ID",
				"error", caErr)
		} else {
			c.cleanups = append(c.cleanups, func() {
				if err := caConn.Close(); err != nil {
					c.Logger.Error("failed to close current account connection", "error", err)
				}
			})
			caClient := currentaccountv1.NewCurrentAccountServiceClient(caConn)
			partyResolver = service.NewGRPCAccountPartyResolver(caClient)
			c.Logger.Info("current account party resolver configured",
				"current_account_url", c.Config.Services.CurrentAccountURL)
		}
	}

	valuator := service.NewVarianceValuator(valuationEngine, refDataProvider, partyResolver, c.VarianceRepo, c.RunRepo)
	serviceOpts = append(serviceOpts,
		service.WithVarianceValuator(valuator.ValueVariances),
	)

	return serviceOpts, nil
}

// buildValuationComponents creates the valuation engine and reference data provider.
// When the Reference Data gRPC URL is configured, it creates a gRPC client for instrument
// lookups. Otherwise, it falls back to the identity conversion method with default materiality
// thresholds.
func (c *Container) buildValuationComponents() (valuation.Engine, service.ReferenceDataProvider) {
	// Create valuation engine runtime components
	policyRT, err := valuation.NewPolicyRuntime()
	if err != nil {
		c.Logger.Warn("failed to create CEL policy runtime, using identity method resolver", "error", err)
	}

	starlarkRT := valuation.NewStarlarkRuntime(valuation.StarlarkRuntimeConfig{
		PolicyRuntime: policyRT,
	})

	cache := valuation.NewInMemoryCache(valuation.InMemoryCacheConfig{})

	// Use identity method resolver as the base
	methodResolver := valuation.NewIdentityMethodResolver()

	engine := valuation.NewEngine(valuation.Config{
		StarlarkRuntime: starlarkRT,
		PolicyRuntime:   policyRT,
		Cache:           cache,
	}, methodResolver)

	adaptedEngine := service.NewValuationEngineAdapter(engine, c.Logger)

	// Build reference data provider with gRPC clients if available
	providerCfg := service.GRPCReferenceDataProviderConfig{
		DefaultMethodID: uuid.MustParse(valuation.IdentityMethodID),
		Logger:          c.Logger,
	}

	if c.Config.Services.ReferenceDataURL != "" {
		refDataConn, connErr := grpc.NewClient(
			c.Config.Services.ReferenceDataURL,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if connErr != nil {
			c.Logger.Warn("failed to create reference data gRPC client", "error", connErr)
		} else {
			c.cleanups = append(c.cleanups, func() {
				if err := refDataConn.Close(); err != nil {
					c.Logger.Error("failed to close reference data connection", "error", err)
				}
			})
			providerCfg.InstrumentClient = referencedatav1.NewReferenceDataServiceClient(refDataConn)
			c.Logger.Info("reference data gRPC client configured for instrument lookups",
				"url", c.Config.Services.ReferenceDataURL)
		}
	} else {
		c.Logger.Info("reference data gRPC not configured, using default valuation method and materiality threshold")
	}

	refDataProvider := service.NewGRPCReferenceDataProvider(providerCfg)

	c.Logger.Info("variance valuator configured",
		"engine", "starlark+cel",
		"method_resolver", "identity (fallback)",
		"reference_data_url", c.Config.Services.ReferenceDataURL,
	)

	return adaptedEngine, refDataProvider
}

// initService creates the AccountReconciliationService from options.
func (c *Container) initService(opts []service.Option) {
	c.ReconciliationService = service.NewAccountReconciliationService(opts...)
	c.Logger.Info("reconciliation service initialized")
}

// initRedis initializes the Redis client (optional, needed for scheduler leader election).
func (c *Container) initRedis(ctx context.Context) error {
	if !c.Config.Redis.Enabled || c.Config.Redis.URL == "" {
		c.Logger.Info("Redis disabled or not configured")
		return nil
	}

	redisCfg := bootstrap.RedisConfig{
		URL:    c.Config.Redis.URL,
		Logger: c.Logger,
	}
	redisClient, redisErr := bootstrap.NewRedisClient(ctx, redisCfg)
	if redisErr != nil {
		c.Logger.Warn("Redis connection failed, scheduler and Redis health check disabled",
			"error", redisErr)
		return nil
	}

	c.RedisClient = redisClient
	c.Logger.Info("Redis client connected")
	return nil
}

// initScheduler creates and configures the CronScheduler with all its dependencies.
// The scheduler is only created if enabled in config and Redis is available.
func (c *Container) initScheduler(ctx context.Context) {
	if !c.Config.Scheduler.Enabled {
		c.Logger.Info("settlement scheduler disabled")
		return
	}

	if c.RedisClient == nil {
		c.Logger.Warn("scheduler disabled: Redis not configured (required for distributed locking)")
		return
	}

	// Create distributed lock
	distLock := redislock.NewLock(c.RedisClient, redislock.Config{
		KeyPrefix:  "meridian:reconciliation:scheduler",
		LockTTL:    c.Config.Scheduler.LeaderLockTTL,
		RenewEvery: c.Config.Scheduler.LeaderRenewInterval,
	}, c.Logger)

	// Create execution store (requires pgxpool for direct pgx queries)
	pool, err := pgxpool.New(ctx, c.Config.Database.URL)
	if err != nil {
		c.Logger.Warn("scheduler disabled: failed to create pgx pool for execution store",
			"error", err)
		return
	}
	c.cleanups = append(c.cleanups, func() {
		pool.Close()
	})

	executionStore, err := scheduler.NewPgExecutionStore(pool) //nolint:contextcheck // NewPgExecutionStore creates its own context for schema validation
	if err != nil {
		c.Logger.Warn("scheduler disabled: execution store validation failed",
			"error", err)
		return
	}

	// Create Reference Data client (stub until proto available)
	refDataClient := worker.NewStubReferenceDataClient(c.Logger)

	// Create reconciliation self-client (loopback to this service)
	reconConn, err := grpc.NewClient(
		fmt.Sprintf("localhost:%s", c.Config.Server.Port),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		c.Logger.Warn("scheduler disabled: failed to create reconciliation self-client",
			"error", err)
		return
	}
	c.cleanups = append(c.cleanups, func() {
		if err := reconConn.Close(); err != nil {
			c.Logger.Error("failed to close reconciliation self-client connection", "error", err)
		}
	})

	reconGrpcClient := reconciliationv1.NewAccountReconciliationServiceClient(reconConn)
	reconClient := worker.NewGrpcReconciliationClient(reconGrpcClient)

	// Create adapter types
	provider := worker.NewSettlementScheduleProvider(refDataClient)
	executor := worker.NewSettlementExecutor(reconClient, nil, c.Logger)

	// Create shared cron scheduler
	c.CronScheduler = scheduler.NewCronScheduler(
		provider,
		executor,
		distLock,
		scheduler.CronSchedulerConfig{
			Name:             "reconciliation",
			RefreshInterval:  c.Config.Scheduler.PollInterval,
			ShutdownTimeout:  c.Config.Scheduler.ShutdownTimeout,
			ExecutionTimeout: 10 * time.Minute,
			MaxCatchUpAge:    24 * time.Hour,
		},
		c.Logger,
		scheduler.WithCronExecutionStore(executionStore),
	)

	c.Logger.Info("settlement scheduler configured",
		"poll_interval", c.Config.Scheduler.PollInterval,
		"leader_lock_ttl", c.Config.Scheduler.LeaderLockTTL,
		"shutdown_timeout", c.Config.Scheduler.ShutdownTimeout)
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

// WireScheduler creates and configures a CronScheduler with all its dependencies.
// This is exported for use in tests. Returns nil if any required dependency is not available.
func WireScheduler(ctx context.Context, cfg *config.Config, redisClient *redis.Client, logger *slog.Logger) *scheduler.CronScheduler {
	c := &Container{
		Config:      cfg,
		Logger:      logger,
		RedisClient: redisClient,
	}
	c.initScheduler(ctx)
	return c.CronScheduler
}

// BuildValuationComponents creates the real valuation engine and reference data provider.
// This is exported for use in tests.
func BuildValuationComponents(cfg *config.Config, logger *slog.Logger) (valuation.Engine, service.ReferenceDataProvider, *grpc.ClientConn) {
	c := &Container{
		Config: cfg,
		Logger: logger,
	}
	engine, provider := c.buildValuationComponents()

	// Extract connection from cleanups if one was created
	var conn *grpc.ClientConn
	if len(c.cleanups) > 0 {
		// The last cleanup is the reference data connection closer
		// Return the connection for the caller to manage
		// We need to re-create it since the cleanup holds a closure
		if cfg.Services.ReferenceDataURL != "" {
			var connErr error
			conn, connErr = grpc.NewClient(
				cfg.Services.ReferenceDataURL,
				grpc.WithTransportCredentials(insecure.NewCredentials()),
			)
			if connErr != nil {
				logger.Warn("failed to create reference data gRPC client for test", "error", connErr)
			}
		}
	}
	return engine, provider, conn
}

// Close gracefully shuts down all container resources.
func (c *Container) Close() {
	c.Logger.Info("closing container resources...")

	// Stop scheduler first (it makes gRPC calls to self)
	if c.CronScheduler != nil {
		c.Logger.Info("stopping settlement scheduler...")
		c.CronScheduler.Stop()
		c.Logger.Info("settlement scheduler stopped")
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

	// Run cleanup functions in reverse order (gRPC connections, pgx pools, etc.)
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
