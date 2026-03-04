// Package main is the entry point for the PaymentOrder service.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	stripego "github.com/stripe/stripe-go/v82"

	"github.com/jackc/pgx/v5/pgxpool"
	pb "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	"github.com/meridianhub/meridian/services/payment-order/adapters/gateway"
	stripegateway "github.com/meridianhub/meridian/services/payment-order/adapters/gateway/stripe"
	webhookhttp "github.com/meridianhub/meridian/services/payment-order/adapters/http"
	pomessaging "github.com/meridianhub/meridian/services/payment-order/adapters/messaging"
	"github.com/meridianhub/meridian/services/payment-order/adapters/persistence"
	"github.com/meridianhub/meridian/services/payment-order/config"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	poobservability "github.com/meridianhub/meridian/services/payment-order/observability"
	"github.com/meridianhub/meridian/services/payment-order/service"
	"github.com/meridianhub/meridian/services/payment-order/worker"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/kafka"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/meridianhub/meridian/shared/platform/ports"
	"github.com/meridianhub/meridian/shared/platform/redislock"
	"github.com/meridianhub/meridian/shared/platform/scheduler"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	// Service-owned clients (standardized client packages from each service)
	currentaccountclient "github.com/meridianhub/meridian/services/current-account/client"
	financialaccountingclient "github.com/meridianhub/meridian/services/financial-accounting/client"
	financialgatewayclient "github.com/meridianhub/meridian/services/financial-gateway/client"
	internalaccountclient "github.com/meridianhub/meridian/services/internal-account/client"
	partyclient "github.com/meridianhub/meridian/services/party/client"
	positionkeepingclient "github.com/meridianhub/meridian/services/position-keeping/client"

	// Reference data client for saga definitions and instrument lookups
	poclients "github.com/meridianhub/meridian/services/payment-order/adapters/clients"
	referencedataclient "github.com/meridianhub/meridian/services/reference-data/client"

	"github.com/meridianhub/meridian/shared/pkg/saga"
)

// Build information set via ldflags during compilation.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// ErrMissingHMACSecret is returned when the WEBHOOK_HMAC_SECRET environment variable is not set.
var ErrMissingHMACSecret = errors.New("WEBHOOK_HMAC_SECRET environment variable is required")

// ErrRedisRequiredInProduction is returned when Redis is unavailable in production environments.
var ErrRedisRequiredInProduction = errors.New("redis required for idempotency in production environment")

func main() {
	// Initialize structured logging with configurable log level
	// Note: bootstrap.NewLogger hardcodes INFO level, so we create logger manually for LOG_LEVEL support
	logLevel := parseLogLevel(os.Getenv("LOG_LEVEL"))
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))
	slog.SetDefault(logger)

	logger.Info("starting payment-order service",
		"version", Version,
		"commit", Commit,
		"build_date", BuildDate)

	// Run the service with retry for transient startup errors
	if err := bootstrap.RunWithRetry(
		func() error { return run(logger) },
		bootstrap.WithRetryLogger(logger),
	); err != nil {
		logger.Error("service failed to start", "error", err)
		os.Exit(1)
	}

	logger.Info("service stopped gracefully")
}

func run(logger *slog.Logger) error {
	ctx := context.Background()

	// Load and validate service configuration early (permanent error if invalid)
	svcConfig := config.LoadServiceConfig()
	if err := svcConfig.Validate(); err != nil {
		return bootstrap.Permanent(fmt.Errorf("invalid service configuration: %w", err))
	}
	svcConfig.LogValues(logger)

	// Initialize OpenTelemetry tracer
	tracer, err := bootstrap.NewTracer(ctx, bootstrap.TracerConfig{
		ServiceName:    "payment-order-service",
		ServiceVersion: Version,
		Logger:         logger,
	})
	if err != nil {
		return fmt.Errorf("failed to initialize tracer: %w", err)
	}
	defer bootstrap.ShutdownTracer(tracer, logger)

	// Initialize database connection
	dbConfig := bootstrap.DefaultDatabaseConfig()
	dbConfig.Logger = logger
	db, err := bootstrap.NewDatabase(ctx, dbConfig)
	if err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}
	defer bootstrap.CloseDatabase(db, logger)

	logger.Info("database connection established")

	// Create repository
	repo := persistence.NewPaymentOrderRepository(db)

	// Get Kubernetes namespace from environment
	namespace := env.GetEnvOrDefault("K8S_NAMESPACE", "default")

	// Create external clients using service-owned client packages
	currentAccountClient, caCleanup, err := createCurrentAccountClient(namespace, logger, tracer)
	if err != nil {
		return fmt.Errorf("failed to create current account client: %w", err)
	}
	defer caCleanup()

	financialAccountingClient, faCleanup, err := createFinancialAccountingClient(namespace, logger, tracer)
	if err != nil {
		return fmt.Errorf("failed to create financial accounting client: %w", err)
	}
	defer faCleanup()

	// Create internal account client (optional - for internal clearing operations)
	// This client is only created if INTERNAL_CLEARING_ENABLED is true
	internalClearingEnabled := env.GetEnvAsBool("INTERNAL_CLEARING_ENABLED", false)
	var internalAccountClient service.InternalAccountClient
	var ibaCleanup func()
	if internalClearingEnabled {
		internalAccountClient, ibaCleanup, err = createInternalAccountClient(namespace, logger, tracer)
		if err != nil {
			return fmt.Errorf("failed to create internal account client: %w", err)
		}
		defer ibaCleanup()
		logger.Info("internal clearing enabled - internal account client configured")
	} else {
		logger.Info("internal clearing disabled - gateway-only mode")
	}

	// Create payment gateway
	paymentGateway, gatewayCleanup, err := createPaymentGateway(svcConfig, logger)
	if err != nil {
		return fmt.Errorf("failed to create payment gateway: %w", err)
	}
	defer gatewayCleanup()
	logger.Info("payment gateway initialized")

	// Load gateway account configuration
	gatewayAccountConfig, err := createGatewayAccountConfig(logger)
	if err != nil {
		return fmt.Errorf("failed to load gateway account config: %w", err)
	}

	// Initialize outbox publisher and worker for transactional event publishing.
	// Events are written to the outbox table within the same DB transaction as
	// domain operations, then published to Kafka asynchronously by the outbox worker.
	outboxRepo := events.NewPostgresOutboxRepository(db)
	outboxPublisher := events.NewOutboxPublisher("payment-order")

	var outboxWorker *events.Worker
	var kafkaProducer *kafka.ProtoProducer
	bootstrapServers := env.GetEnvOrDefault("KAFKA_BOOTSTRAP_SERVERS", "")
	if bootstrapServers == "" {
		// Fall back to legacy KAFKA_BROKERS env var
		bootstrapServers = env.GetEnvOrDefault("KAFKA_BROKERS", "")
	}
	if bootstrapServers != "" {
		producer, kafkaErr := kafka.NewProtoProducer(kafka.ProducerConfig{
			BootstrapServers: bootstrapServers,
			ClientID:         "payment-order-outbox-worker",
			Acks:             "all",
			Retries:          3,
			Compression:      "snappy",
		})
		if kafkaErr != nil {
			logger.Warn("failed to create Kafka producer for outbox worker",
				"error", kafkaErr)
		} else {
			kafkaProducer = producer
			defer kafkaProducer.Close()
			workerConfig := events.DefaultWorkerConfig("payment-order")
			outboxWorker = events.NewWorker(outboxRepo, kafkaProducer, workerConfig, logger)
			outboxWorker.Start(ctx)
			defer outboxWorker.Stop()
			logger.Info("outbox worker started",
				"bootstrap_servers", bootstrapServers)
		}
	} else {
		logger.Warn("outbox worker disabled - KAFKA_BOOTSTRAP_SERVERS not set (events will accumulate in outbox)")
	}

	// Create outbox-based event publisher (replaces direct Kafka producer)
	eventPublisher := pomessaging.NewOutboxPublisher(db, outboxPublisher)

	// Create Redis client and idempotency service.
	// In production: fail fast if Redis is unavailable (idempotency is critical).
	// In non-production: use NoopService for graceful degradation with metrics.
	var idempotencyService idempotency.Service
	redisClient, redisErr := createRedisClient(logger)
	if redisErr != nil {
		if env.IsProduction() {
			logger.Error("CRITICAL: Redis unavailable in production - failing fast", "error", redisErr)
			return bootstrap.Permanent(fmt.Errorf("%w: %w", ErrRedisRequiredInProduction, redisErr))
		}
		logger.Warn("Redis not available at startup, using noop idempotency service - DEVELOPMENT ONLY",
			"error", redisErr,
			"environment", os.Getenv("ENVIRONMENT"))
		idempotencyService = idempotency.NewNoopService(logger)
		poobservability.SetNoopIdempotencyActive(true)
		poobservability.RecordServiceDegradation(poobservability.ComponentIdempotency, poobservability.DegradationReasonStartupFallback)
	} else {
		idempotencyService = idempotency.NewRedisService(redisClient)
		poobservability.SetNoopIdempotencyActive(false)
		logger.Info("idempotency service initialized with Redis")
		defer func() {
			if err := redisClient.Close(); err != nil {
				logger.Error("failed to close Redis client", "error", err)
			}
		}()
	}

	// Create Starlark handler registry with service client handlers.
	// This enables saga scripts to call real services (not mocks).
	// See PRD: docs/prd/starlark-service-bindings.md
	handlerRegistry := saga.NewHandlerRegistry()

	// Create position-keeping client for Starlark handlers.
	// Note: payment-order needs position-keeping for payment execution sagas.
	// This is optional during migration - service can start without it until Starlark is active.
	posKeepingClient, posKeepingCleanup, err := positionkeepingclient.New(positionkeepingclient.Config{
		ServiceName: positionkeepingclient.ServiceName,
		Namespace:   namespace,
		Port:        ports.PositionKeeping,
		Timeout:     defaults.DefaultRPCTimeout,
		Tracer:      tracer,
		// No circuit breaker for saga handlers - saga has its own retry logic
	})
	if err != nil {
		// Downgrade to warning - Starlark runtime isn't wired yet, service can operate without handlers
		logger.Warn("position-keeping client unavailable, Starlark handlers not registered",
			"error", err)
	} else {
		defer posKeepingCleanup()
	}

	// Register handlers from service clients.
	// Each RegisterStarlarkHandlers function adapts Starlark params (map[string]any)
	// to gRPC client calls, propagating saga metadata (idempotency, correlation, etc.)
	//
	// Note: Type assertions are required because the createXxxClient functions return
	// interface types (service.XxxClient) but RegisterStarlarkHandlers needs the
	// concrete *Client type to access the gRPC connection.
	//
	// Handler registration failures are warnings (not errors) since Starlark runner
	// isn't wired yet - service can operate without handlers during migration.
	if caClient, ok := currentAccountClient.(*currentaccountclient.Client); ok {
		if err := currentaccountclient.RegisterStarlarkHandlers(handlerRegistry, caClient); err != nil {
			logger.Warn("failed to register current-account handlers", "error", err)
		}
	} else {
		logger.Warn("current-account client type assertion failed, Starlark handlers not registered")
	}

	if faClient, ok := financialAccountingClient.(*financialaccountingclient.Client); ok {
		if err := financialaccountingclient.RegisterStarlarkHandlers(handlerRegistry, faClient); err != nil {
			logger.Warn("failed to register financial-accounting handlers", "error", err)
		}
	} else {
		logger.Warn("financial-accounting client type assertion failed, Starlark handlers not registered")
	}

	if posKeepingClient != nil {
		if err := positionkeepingclient.RegisterStarlarkHandlers(handlerRegistry, posKeepingClient); err != nil {
			logger.Warn("failed to register position-keeping handlers", "error", err)
		}
	}

	// Create Party client for Starlark handlers (party.get_default_payment_method).
	partyClient, partyCleanup, err := partyclient.New(partyclient.Config{
		ServiceName: partyclient.ServiceName,
		Namespace:   namespace,
		Port:        ports.Party,
		Timeout:     defaults.DefaultRPCTimeout,
		Tracer:      tracer,
	})
	if err != nil {
		logger.Warn("party client unavailable, Starlark party handlers not registered",
			"error", err)
	} else {
		defer partyCleanup()
		if err := partyclient.RegisterStarlarkHandlers(handlerRegistry, partyClient); err != nil {
			logger.Warn("failed to register party handlers", "error", err)
		}
	}

	// Create Financial Gateway client for Starlark handlers (financial_gateway.dispatch_payment).
	fgClient, fgCleanup, err := financialgatewayclient.New(financialgatewayclient.Config{
		ServiceName: financialgatewayclient.ServiceName,
		Namespace:   namespace,
		Port:        financialgatewayclient.DefaultPort,
		Timeout:     defaults.DefaultRPCTimeout,
		Tracer:      tracer,
	})
	if err != nil {
		logger.Warn("financial-gateway client unavailable, Starlark financial_gateway handlers not registered",
			"error", err)
	} else {
		defer fgCleanup()
		if err := financialgatewayclient.RegisterStarlarkHandlers(handlerRegistry, fgClient); err != nil {
			logger.Warn("failed to register financial-gateway handlers", "error", err)
		}
	}

	// Create Reference Data client for saga definitions and instrument lookups.
	// Uses the shared gRPC connection via ReferenceDataClientWrapper which implements
	// service.ReferenceDataClient (GetSaga + RetrieveInstrument).
	refDataClient, refDataCleanup, err := referencedataclient.New(ctx, referencedataclient.Config{
		ServiceName: referencedataclient.ServiceName,
		Namespace:   namespace,
		Port:        ports.ReferenceData,
		Timeout:     defaults.DefaultRPCTimeout,
		Tracer:      tracer,
	})
	var referenceDataClient service.ReferenceDataClient
	if err != nil {
		logger.Warn("reference-data client unavailable, saga definitions will not be fetched",
			"error", err)
	} else {
		defer func() {
			if err := refDataCleanup(); err != nil {
				logger.Error("failed to close reference-data client", "error", err)
			}
		}()
		// Wrap the reference-data client to implement service.ReferenceDataClient interface
		referenceDataClient = poclients.NewReferenceDataClient(refDataClient.Conn())
		// Register reference-data Starlark handlers if needed in the future
		logger.Info("reference-data client connected", "port", ports.ReferenceData)
	}

	logger.Info("Starlark handler registry initialized",
		"registered_handlers", len(handlerRegistry.List()))

	// Start billing background workers (feature-flagged)
	var billingCronScheduler *scheduler.CronScheduler
	var dunningWorker *worker.DunningWorker

	if svcConfig.BillingEnabled && redisClient != nil {
		billingRepo := persistence.NewBillingRepository(db)
		billingMetrics := worker.NewBillingMetrics()

		tenantID := env.GetEnvOrDefault("BILLING_TENANT_ID", "default")

		// Create pgxpool connection for scheduler execution store (audit trail)
		databaseURL := env.GetEnvOrDefault("DATABASE_URL", "postgres://meridian_user@localhost:26257/meridian?sslmode=disable")
		pgxPool, err := pgxpool.New(ctx, databaseURL)
		if err != nil {
			return fmt.Errorf("failed to create pgxpool for scheduler execution store: %w", err)
		}
		defer pgxPool.Close()

		execStore, err := scheduler.NewPgExecutionStore(pgxPool)
		if err != nil {
			logger.Warn("scheduler_execution table not found, audit trail disabled", "error", err)
		}

		// Create distributed lock for billing scheduler
		billingLock := redislock.NewLock(redisClient, redislock.Config{
			KeyPrefix:  "meridian:billing:scheduler",
			LockTTL:    5 * time.Minute,
			RenewEvery: 30 * time.Second,
		}, logger)

		// Create billing executor adapter (preserves shadow mode and Redis idempotency).
		// InvoiceGenerator and PaymentInitiator are nil until the position-keeping
		// client and saga runner are wired (same as pre-migration behavior).
		billingExecutor := worker.NewBillingExecutor(
			billingRepo,
			redisClient,
			billingMetrics,
			worker.BillingExecutorConfig{ShadowMode: svcConfig.BillingShadowMode},
			logger,
		)
		// TODO: Wire once position-keeping client is available:
		// billingExecutor.WithInvoiceGenerator(invoiceGen).WithPaymentInitiator(paymentInit)

		// Create schedule provider (static single-tenant schedule)
		billingProvider := worker.NewBillingScheduleProvider(tenantID, svcConfig.BillingCronSchedule)

		// Build CronScheduler with optional execution store
		cronOpts := []scheduler.CronSchedulerOption{}
		if execStore != nil {
			cronOpts = append(cronOpts, scheduler.WithCronExecutionStore(execStore))
		}

		billingCronScheduler = scheduler.NewCronScheduler(
			billingProvider,
			billingExecutor,
			billingLock,
			scheduler.CronSchedulerConfig{
				Name:             "billing-scheduler",
				RefreshInterval:  60 * time.Second,
				ShutdownTimeout:  30 * time.Second,
				ExecutionTimeout: 10 * time.Minute,
				MaxCatchUpAge:    time.Hour,
			},
			logger,
			cronOpts...,
		)

		// DunningCallback is a no-op for now; saga runner integration comes in Task 5
		dunningCallback := func(_ context.Context, run *domain.BillingRun) error {
			logger.Info("dunning escalation triggered (no-op: saga runner not wired)",
				"billing_run_id", run.ID,
				"dunning_level", run.DunningLevel)
			return nil
		}

		dunningWorker, err = worker.NewDunningWorker(
			billingRepo,
			redisClient,
			worker.DunningWorkerConfig{
				PollInterval: svcConfig.DunningPollInterval,
			},
			dunningCallback,
			logger,
			billingMetrics,
		)
		if err != nil {
			return fmt.Errorf("failed to create dunning worker: %w", err)
		}

		logger.Info("billing workers configured",
			"cron_schedule", svcConfig.BillingCronSchedule,
			"shadow_mode", svcConfig.BillingShadowMode,
			"dunning_poll_interval", svcConfig.DunningPollInterval,
			"tenant_id", tenantID)
	} else if svcConfig.BillingEnabled && redisClient == nil {
		logger.Warn("billing workers disabled — Redis unavailable (DEVELOPMENT ONLY)")
	} else {
		logger.Info("billing workers disabled (BILLING_ENABLED=false)")
	}

	// Create saga execution repository for audit logging
	sagaExecutionRepo := persistence.NewSagaExecutionRepository(db)

	logger.Info("saga orchestration configuration",
		"saga_orchestration_enabled", svcConfig.SagaOrchestrationEnabled)

	// Create payment order service
	paymentOrderService, err := service.NewServiceWithConfig(service.Config{
		Repository:                repo,
		CurrentAccountClient:      currentAccountClient,
		FinancialAccountingClient: financialAccountingClient,
		InternalAccountClient:     internalAccountClient, // May be nil if internal clearing disabled
		ReferenceDataClient:       referenceDataClient,   // May be nil if reference-data unavailable
		PaymentGateway:            paymentGateway,
		GatewayAccountConfig:      gatewayAccountConfig,
		KafkaPublisher:            eventPublisher,
		IdempotencyService:        idempotencyService,
		Logger:                    logger,
		Tracer:                    tracer,
		InternalClearingEnabled:   internalClearingEnabled,
		HandlerRegistry:           handlerRegistry,
		SagaExecutionLogger:       sagaExecutionRepo,
		SagaOrchestrationEnabled:  svcConfig.SagaOrchestrationEnabled,
	})
	if err != nil {
		return fmt.Errorf("failed to create payment order service: %w", err)
	}

	// Initialize auth interceptor (optional - based on AUTH_ENABLED)
	authConfig := bootstrap.DefaultAuthConfig(logger)
	authInterceptor, err := bootstrap.NewAuthInterceptor(ctx, authConfig)
	if err != nil {
		return fmt.Errorf("failed to initialize auth: %w", err)
	}

	// Create gRPC server with interceptor chain
	// Order is handled by bootstrap: tracing -> auth -> recovery
	grpcServer := bootstrap.NewGrpcServerBuilder(tracer, logger).
		WithAuthInterceptor(authInterceptor).
		Build()

	// Register gRPC services
	pb.RegisterPaymentOrderServiceServer(grpcServer, paymentOrderService)
	grpc_health_v1.RegisterHealthServer(grpcServer, &simpleHealthServer{})
	reflection.Register(grpcServer)
	logger.Info("gRPC services registered")

	// Create HTTP webhook handler
	hmacSecret := []byte(env.GetEnvOrDefault("WEBHOOK_HMAC_SECRET", ""))
	if len(hmacSecret) == 0 {
		return bootstrap.Permanent(ErrMissingHMACSecret)
	}

	// Create a gRPC client wrapper for the local service
	localServiceClient := &localPaymentOrderClient{service: paymentOrderService}

	webhookHandler, err := webhookhttp.NewWebhookHandler(webhookhttp.WebhookHandlerConfig{
		PaymentOrderService: localServiceClient,
		HMACSecret:          hmacSecret,
		Logger:              logger,
	})
	if err != nil {
		return fmt.Errorf("failed to create webhook handler: %w", err)
	}

	// Create HTTP server
	httpPort := env.GetEnvAsInt("HTTP_PORT", ports.Gateway)
	httpServer, err := webhookhttp.NewServer(webhookhttp.ServerConfig{
		Port:               httpPort,
		WebhookHandler:     webhookHandler,
		Logger:             logger,
		RateLimitPerSecond: env.GetEnvAsFloat("HTTP_RATE_LIMIT_PER_SECOND", 100),
		RateLimitBurst:     env.GetEnvAsInt("HTTP_RATE_LIMIT_BURST", 200),
	})
	if err != nil {
		return fmt.Errorf("failed to create HTTP server: %w", err)
	}

	// Get gRPC port
	grpcPort := env.GetEnvOrDefault("GRPC_PORT", strconv.Itoa(ports.PaymentOrder))
	grpcAddress := fmt.Sprintf(":%s", grpcPort)

	// Create gRPC listener
	grpcListener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", grpcAddress)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", grpcAddress, err)
	}

	// Construct the financial-gateway payment event consumer before starting servers
	// so that any initialization error causes an early return without leaving
	// running goroutines/listeners behind.
	//
	// Subscribes to financial-gateway.payment-captured.v1 and
	// financial-gateway.payment-failed.v1 topics and calls UpdatePaymentOrder
	// to transition payment orders to SETTLED or REJECTED.
	var paymentEventConsumer *pomessaging.PaymentEventConsumer
	if bootstrapServers != "" {
		paymentEventConsumer, err = pomessaging.NewPaymentEventConsumerWithKafka(
			kafka.ConsumerConfig{
				BootstrapServers: bootstrapServers,
				GroupID:          "payment-order-gateway-events",
				ClientID:         "payment-order-gateway-events",
				AutoOffsetReset:  "earliest",
				EnableAutoCommit: false,
			},
			localServiceClient,
			logger,
		)
		if err != nil {
			return fmt.Errorf("failed to create payment event consumer: %w", err)
		}
		defer func() {
			if err := paymentEventConsumer.Close(); err != nil {
				logger.Error("failed to close payment event consumer", "error", err)
			}
		}()
	} else {
		logger.Warn("payment event consumer disabled - KAFKA_BOOTSTRAP_SERVERS not set")
	}

	// Channel to collect server errors (gRPC + HTTP + payment event consumer).
	serverErrors := make(chan error, 3)

	// Start gRPC server in background
	go func() {
		logger.Info("starting gRPC server", "address", grpcAddress)
		if err := grpcServer.Serve(grpcListener); err != nil {
			serverErrors <- fmt.Errorf("gRPC server error: %w", err)
		}
	}()

	// Start HTTP server in background
	go func() {
		logger.Info("starting HTTP server", "port", httpPort)
		if err := httpServer.Start(); err != nil {
			serverErrors <- fmt.Errorf("HTTP server error: %w", err)
		}
	}()

	// Start payment event consumer after servers are up.
	if paymentEventConsumer != nil {
		go func() {
			if err := paymentEventConsumer.Start(
				"financial-gateway.payment-captured.v1",
				"financial-gateway.payment-failed.v1",
			); err != nil {
				logger.Error("payment event consumer error", "error", err)
				serverErrors <- fmt.Errorf("payment event consumer error: %w", err)
			}
		}()
	}

	// Start billing workers in background (if enabled)
	workerCtx, workerCancel := context.WithCancel(ctx)
	defer workerCancel()
	var billingWg sync.WaitGroup
	if svcConfig.BillingEnabled && billingCronScheduler != nil && dunningWorker != nil {
		billingWg.Add(2)
		go func() {
			defer billingWg.Done()
			if err := billingCronScheduler.Start(workerCtx); err != nil {
				logger.Error("billing scheduler error", "error", err)
			}
		}()

		go func() {
			defer billingWg.Done()
			if err := dunningWorker.Start(workerCtx); err != nil {
				logger.Error("dunning worker error", "error", err)
			}
		}()

		logger.Info("billing workers started")
	}

	// Wait for interrupt signal or server error
	sigChan, signalCleanup := bootstrap.SignalHandler()
	defer signalCleanup()

	var runErr error
	select {
	case sig := <-sigChan:
		logger.Info("received signal", "signal", sig)
	case err := <-serverErrors:
		logger.Error("server error", "error", err)
		runErr = err

		// Prefer graceful exit if a shutdown signal is already pending.
		// Without this, RunWithRetry would retry despite SIGTERM intent.
		select {
		case sig := <-sigChan:
			logger.Info("received signal during error handling, treating as shutdown", "signal", sig)
			runErr = nil
		default:
		}
	}

	// Graceful shutdown (runs for both signal and server error paths)
	logger.Info("shutting down servers...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), defaults.DefaultGracefulShutdown)
	defer cancel()

	// Stop billing workers and wait for goroutines to exit before database close.
	// Cancel the worker context first to unblock Start() select loops, then
	// call Stop() to signal internal shutdown channels and drain in-flight work.
	if svcConfig.BillingEnabled && billingCronScheduler != nil && dunningWorker != nil {
		logger.Info("stopping billing workers...")
		workerCancel()
		billingCronScheduler.Stop()
		dunningWorker.Stop()
		billingWg.Wait()
		logger.Info("billing workers stopped")
	}

	// Stop payment event consumer before closing connections
	if paymentEventConsumer != nil {
		paymentEventConsumer.Stop()
		logger.Info("payment event consumer stopped")
	}

	// Shutdown HTTP server (stop accepting new webhooks)
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("failed to shutdown HTTP server", "error", err)
	}

	// Gracefully stop gRPC server
	stopped := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(stopped)
	}()

	select {
	case <-stopped:
		logger.Info("servers stopped gracefully")
	case <-shutdownCtx.Done():
		logger.Warn("graceful shutdown timeout, forcing stop")
		grpcServer.Stop()
	}

	return runErr
}

// localPaymentOrderClient wraps the local service to implement the client interface.
type localPaymentOrderClient struct {
	service *service.Service
}

func (c *localPaymentOrderClient) UpdatePaymentOrder(ctx context.Context, req *pb.UpdatePaymentOrderRequest) (*pb.UpdatePaymentOrderResponse, error) {
	return c.service.UpdatePaymentOrder(ctx, req)
}

// simpleHealthServer implements grpc_health_v1.HealthServer with basic checks.
type simpleHealthServer struct {
	grpc_health_v1.UnimplementedHealthServer
}

func (s *simpleHealthServer) Check(_ context.Context, _ *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	return &grpc_health_v1.HealthCheckResponse{
		Status: grpc_health_v1.HealthCheckResponse_SERVING,
	}, nil
}

func (s *simpleHealthServer) Watch(_ *grpc_health_v1.HealthCheckRequest, server grpc_health_v1.Health_WatchServer) error {
	// Send initial status
	if err := server.Send(&grpc_health_v1.HealthCheckResponse{
		Status: grpc_health_v1.HealthCheckResponse_SERVING,
	}); err != nil {
		return err
	}
	// Block until context is done to keep stream open
	<-server.Context().Done()
	return server.Context().Err()
}

// createCurrentAccountClient creates the CurrentAccount gRPC client with resilience patterns.
// Uses the service-owned client package from services/current-account/client for standardized
// client creation with built-in tracing and resilience patterns.
func createCurrentAccountClient(namespace string, logger *slog.Logger, tracer *observability.Tracer) (service.CurrentAccountClient, func(), error) {
	logger.Info("connecting to current-account service",
		"service", currentaccountclient.ServiceName,
		"namespace", namespace,
		"port", ports.CurrentAccount)

	// Configure resilience settings from environment
	resilientConfig := &sharedclients.ResilientClientConfig{
		// Circuit breaker settings
		CircuitBreakerName:     "current-account",
		CircuitBreakerTimeout:  env.GetEnvAsDuration("CURRENT_ACCOUNT_CIRCUIT_BREAKER_TIMEOUT", defaults.DefaultCircuitBreakerOpenTimeout),
		CircuitBreakerInterval: env.GetEnvAsDuration("CURRENT_ACCOUNT_CIRCUIT_BREAKER_INTERVAL", defaults.DefaultCircuitBreakerInterval),
		MaxRequests:            env.GetEnvAsUint32("CURRENT_ACCOUNT_CIRCUIT_BREAKER_MAX_REQUESTS", 1),
		FailureThreshold:       env.GetEnvAsUint32("CURRENT_ACCOUNT_CIRCUIT_BREAKER_FAILURE_THRESHOLD", 5),

		// Retry settings
		MaxRetries:          env.GetEnvAsInt("CURRENT_ACCOUNT_MAX_RETRIES", 3),
		InitialInterval:     env.GetEnvAsDuration("CURRENT_ACCOUNT_RETRY_INITIAL_INTERVAL", defaults.DefaultRetryDelay),
		MaxInterval:         env.GetEnvAsDuration("CURRENT_ACCOUNT_RETRY_MAX_INTERVAL", defaults.DefaultMaxRetryInterval),
		Multiplier:          env.GetEnvAsFloat("CURRENT_ACCOUNT_RETRY_MULTIPLIER", 2.0),
		RandomizationFactor: env.GetEnvAsFloat("CURRENT_ACCOUNT_RETRY_RANDOMIZATION", 0.5),

		Logger: logger,
	}

	logger.Info("current-account client configured with resilience patterns",
		"circuit_breaker_timeout", resilientConfig.CircuitBreakerTimeout,
		"circuit_breaker_failure_threshold", resilientConfig.FailureThreshold,
		"max_retries", resilientConfig.MaxRetries,
	)

	// Use the service-owned client package with DNS-based load balancing
	client, cleanup, err := currentaccountclient.New(currentaccountclient.Config{
		ServiceName: currentaccountclient.ServiceName,
		Namespace:   namespace,
		Port:        ports.CurrentAccount,
		Timeout:     env.GetEnvAsDuration("CURRENT_ACCOUNT_TIMEOUT", currentaccountclient.DefaultTimeout),
		Tracer:      tracer,
		Resilience:  resilientConfig,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create current-account client: %w", err)
	}

	return client, cleanup, nil
}

// createFinancialAccountingClient creates the FinancialAccounting gRPC client with resilience patterns.
// Uses the service-owned client package from services/financial-accounting/client for standardized
// client creation with built-in tracing and resilience patterns.
func createFinancialAccountingClient(namespace string, logger *slog.Logger, tracer *observability.Tracer) (service.FinancialAccountingClient, func(), error) {
	logger.Info("connecting to financial-accounting service",
		"service", financialaccountingclient.ServiceName,
		"namespace", namespace,
		"port", ports.FinancialAccounting)

	// Configure resilience settings from environment
	resilientConfig := &sharedclients.ResilientClientConfig{
		// Circuit breaker settings
		CircuitBreakerName:     "financial-accounting",
		CircuitBreakerTimeout:  env.GetEnvAsDuration("FINANCIAL_ACCOUNTING_CIRCUIT_BREAKER_TIMEOUT", defaults.DefaultCircuitBreakerOpenTimeout),
		CircuitBreakerInterval: env.GetEnvAsDuration("FINANCIAL_ACCOUNTING_CIRCUIT_BREAKER_INTERVAL", defaults.DefaultCircuitBreakerInterval),
		MaxRequests:            env.GetEnvAsUint32("FINANCIAL_ACCOUNTING_CIRCUIT_BREAKER_MAX_REQUESTS", 1),
		FailureThreshold:       env.GetEnvAsUint32("FINANCIAL_ACCOUNTING_CIRCUIT_BREAKER_FAILURE_THRESHOLD", 5),

		// Retry settings
		MaxRetries:          env.GetEnvAsInt("FINANCIAL_ACCOUNTING_MAX_RETRIES", 3),
		InitialInterval:     env.GetEnvAsDuration("FINANCIAL_ACCOUNTING_RETRY_INITIAL_INTERVAL", defaults.DefaultRetryDelay),
		MaxInterval:         env.GetEnvAsDuration("FINANCIAL_ACCOUNTING_RETRY_MAX_INTERVAL", defaults.DefaultMaxRetryInterval),
		Multiplier:          env.GetEnvAsFloat("FINANCIAL_ACCOUNTING_RETRY_MULTIPLIER", 2.0),
		RandomizationFactor: env.GetEnvAsFloat("FINANCIAL_ACCOUNTING_RETRY_RANDOMIZATION", 0.5),

		Logger: logger,
	}

	logger.Info("financial-accounting client configured with resilience patterns",
		"circuit_breaker_timeout", resilientConfig.CircuitBreakerTimeout,
		"circuit_breaker_failure_threshold", resilientConfig.FailureThreshold,
		"max_retries", resilientConfig.MaxRetries,
	)

	// Use the service-owned client package with DNS-based load balancing
	client, cleanup, err := financialaccountingclient.New(financialaccountingclient.Config{
		ServiceName: financialaccountingclient.ServiceName,
		Namespace:   namespace,
		Port:        ports.FinancialAccounting,
		Timeout:     env.GetEnvAsDuration("FINANCIAL_ACCOUNTING_TIMEOUT", financialaccountingclient.DefaultTimeout),
		Tracer:      tracer,
		Resilience:  resilientConfig,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create financial-accounting client: %w", err)
	}

	return client, cleanup, nil
}

// createInternalAccountClient creates the InternalAccount gRPC client with resilience patterns.
// Uses the service-owned client package from services/internal-account/client for standardized
// client creation with built-in tracing and resilience patterns.
// This client is optional and only created when INTERNAL_CLEARING_ENABLED is true.
func createInternalAccountClient(namespace string, logger *slog.Logger, tracer *observability.Tracer) (service.InternalAccountClient, func(), error) {
	logger.Info("connecting to internal-account service",
		"service", internalaccountclient.ServiceName,
		"namespace", namespace,
		"port", ports.InternalAccount)

	// Configure resilience settings from environment
	resilientConfig := &sharedclients.ResilientClientConfig{
		// Circuit breaker settings
		CircuitBreakerName:     "internal-account",
		CircuitBreakerTimeout:  env.GetEnvAsDuration("INTERNAL_ACCOUNT_CIRCUIT_BREAKER_TIMEOUT", defaults.DefaultCircuitBreakerOpenTimeout),
		CircuitBreakerInterval: env.GetEnvAsDuration("INTERNAL_ACCOUNT_CIRCUIT_BREAKER_INTERVAL", defaults.DefaultCircuitBreakerInterval),
		MaxRequests:            env.GetEnvAsUint32("INTERNAL_ACCOUNT_CIRCUIT_BREAKER_MAX_REQUESTS", 1),
		FailureThreshold:       env.GetEnvAsUint32("INTERNAL_ACCOUNT_CIRCUIT_BREAKER_FAILURE_THRESHOLD", 5),

		// Retry settings
		MaxRetries:          env.GetEnvAsInt("INTERNAL_ACCOUNT_MAX_RETRIES", 3),
		InitialInterval:     env.GetEnvAsDuration("INTERNAL_ACCOUNT_RETRY_INITIAL_INTERVAL", defaults.DefaultRetryDelay),
		MaxInterval:         env.GetEnvAsDuration("INTERNAL_ACCOUNT_RETRY_MAX_INTERVAL", defaults.DefaultMaxRetryInterval),
		Multiplier:          env.GetEnvAsFloat("INTERNAL_ACCOUNT_RETRY_MULTIPLIER", 2.0),
		RandomizationFactor: env.GetEnvAsFloat("INTERNAL_ACCOUNT_RETRY_RANDOMIZATION", 0.5),

		Logger: logger,
	}

	logger.Info("internal-account client configured with resilience patterns",
		"circuit_breaker_timeout", resilientConfig.CircuitBreakerTimeout,
		"circuit_breaker_failure_threshold", resilientConfig.FailureThreshold,
		"max_retries", resilientConfig.MaxRetries,
	)

	// Use the service-owned client package with DNS-based load balancing
	client, cleanup, err := internalaccountclient.New(internalaccountclient.Config{
		ServiceName: internalaccountclient.ServiceName,
		Namespace:   namespace,
		Port:        ports.InternalAccount,
		Timeout:     env.GetEnvAsDuration("INTERNAL_ACCOUNT_TIMEOUT", internalaccountclient.DefaultTimeout),
		Tracer:      tracer,
		Resilience:  resilientConfig,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create internal-account client: %w", err)
	}

	return client, cleanup, nil
}

// createPaymentGateway creates the payment gateway client with resilience patterns.
// The gateway is wrapped with circuit breaker, rate limiting, and retry logic.
// Provider selection and API key validation are handled by ServiceConfig.Validate,
// so this function assumes the config is already validated.
func createPaymentGateway(svcConfig config.ServiceConfig, logger *slog.Logger) (gateway.PaymentGateway, func(), error) {
	var baseGateway gateway.PaymentGateway
	cleanup := func() {}

	switch svcConfig.PaymentGatewayProvider {
	case gateway.ProviderStripe:
		client := stripego.NewClient(svcConfig.StripeAPIKey)
		baseGateway = stripegateway.NewGatewayAdapter(
			client.V1PaymentIntents,
			stripegateway.GatewayAdapterConfig{},
			logger,
		)
		logger.Info("using stripe payment gateway")

	case gateway.ProviderFinancialGateway:
		fgClient, fgCleanup, err := financialgatewayclient.New(financialgatewayclient.Config{
			Target: svcConfig.FinancialGatewayAddr,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create financial-gateway gRPC client: %w", err)
		}
		cleanup = fgCleanup
		baseGateway = gateway.NewFinancialGatewayClient(fgClient, logger)
		logger.Info("using financial-gateway payment gateway", "addr", svcConfig.FinancialGatewayAddr)

	case gateway.ProviderMock:
		logger.Warn("using mock payment gateway")
		baseGateway = gateway.New(gateway.Config{
			UseMock: true,
			MockConfig: gateway.MockGatewayConfig{
				DeterministicFailures: true,
			},
		})

	default:
		return nil, nil, fmt.Errorf("%w: %q (valid: %q, %q, %q)", config.ErrInvalidGatewayProvider, svcConfig.PaymentGatewayProvider, gateway.ProviderStripe, gateway.ProviderFinancialGateway, gateway.ProviderMock)
	}

	// Configure resilience settings from environment
	resilientConfig := gateway.ResilientGatewayConfig{
		// Circuit breaker settings
		CircuitBreakerName:     "payment-gateway",
		CircuitBreakerTimeout:  env.GetEnvAsDuration("GATEWAY_CIRCUIT_BREAKER_TIMEOUT", defaults.DefaultCircuitBreakerOpenTimeout),
		CircuitBreakerInterval: env.GetEnvAsDuration("GATEWAY_CIRCUIT_BREAKER_INTERVAL", defaults.DefaultCircuitBreakerInterval),
		MaxRequests:            env.GetEnvAsUint32("GATEWAY_CIRCUIT_BREAKER_MAX_REQUESTS", 1),
		FailureThreshold:       env.GetEnvAsUint32("GATEWAY_CIRCUIT_BREAKER_FAILURE_THRESHOLD", 5),

		// Rate limiting settings
		RateLimit:      env.GetEnvAsFloat("GATEWAY_RATE_LIMIT", 100.0),
		RateLimitBurst: env.GetEnvAsInt("GATEWAY_RATE_LIMIT_BURST", 10),

		// Retry settings
		MaxRetries:          env.GetEnvAsInt("GATEWAY_MAX_RETRIES", 3),
		InitialInterval:     env.GetEnvAsDuration("GATEWAY_RETRY_INITIAL_INTERVAL", defaults.DefaultRetryDelay),
		MaxInterval:         env.GetEnvAsDuration("GATEWAY_RETRY_MAX_INTERVAL", defaults.DefaultMaxRetryInterval),
		Multiplier:          env.GetEnvAsFloat("GATEWAY_RETRY_MULTIPLIER", 2.0),
		RandomizationFactor: env.GetEnvAsFloat("GATEWAY_RETRY_RANDOMIZATION", 0.5),

		Logger: logger,
	}

	logger.Info("payment gateway configured with resilience patterns",
		"circuit_breaker_timeout", resilientConfig.CircuitBreakerTimeout,
		"circuit_breaker_failure_threshold", resilientConfig.FailureThreshold,
		"rate_limit", resilientConfig.RateLimit,
		"rate_limit_burst", resilientConfig.RateLimitBurst,
		"max_retries", resilientConfig.MaxRetries,
	)

	return gateway.NewResilientPaymentGateway(baseGateway, resilientConfig), cleanup, nil
}

// createRedisClient creates and validates a Redis client connection.
// Environment variables:
//   - REDIS_URL: Redis connection URL (default: redis://localhost:6379)
//   - REDIS_PASSWORD: Redis password (optional)
//   - REDIS_DB: Redis database number (default: 0)
//   - REDIS_POOL_SIZE: Connection pool size (default: 10)
//   - REDIS_MIN_IDLE_CONNS: Minimum idle connections (default: 2)
func createRedisClient(logger *slog.Logger) (*redis.Client, error) {
	redisURL := env.GetEnvOrDefault("REDIS_URL", "redis://localhost:6379")
	redisPassword := env.GetEnvOrDefault("REDIS_PASSWORD", "")
	redisDB := env.GetEnvAsInt("REDIS_DB", 0)
	poolSize := env.GetEnvAsInt("REDIS_POOL_SIZE", 10)
	minIdleConns := env.GetEnvAsInt("REDIS_MIN_IDLE_CONNS", 2)

	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("invalid REDIS_URL: %w", err)
	}

	// Override with explicit config if set
	if redisPassword != "" {
		opt.Password = redisPassword
	}
	opt.DB = redisDB
	opt.PoolSize = poolSize
	opt.MinIdleConns = minIdleConns

	client := redis.NewClient(opt)

	// Verify connection
	ctx, cancel := context.WithTimeout(context.Background(), defaults.DefaultHealthCheckTimeout)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to ping Redis: %w", err)
	}

	// Log sanitized address to avoid exposing credentials
	logger.Info("Redis client connected",
		"addr", opt.Addr,
		"db", redisDB,
		"pool_size", poolSize,
		"min_idle_conns", minIdleConns)

	return client, nil
}

// createGatewayAccountConfig loads the gateway-to-account mapping configuration.
// This configuration is required for ledger posting - it maps each payment gateway
// to its corresponding contra-account for double-entry bookkeeping.
//
// Environment variables:
//   - GATEWAY_ACCOUNT_MAPPING_FILE: Path to JSON config file (takes precedence)
//   - GATEWAY_{ID}_ACCOUNT_ID: Contra-account UUID for gateway ID
//   - GATEWAY_{ID}_ACCOUNT_TYPE: Account type (NOSTRO or ACQUIRER)
func createGatewayAccountConfig(logger *slog.Logger) (*config.GatewayAccountConfig, error) {
	cfg, err := config.LoadGatewayAccountConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load gateway account config: %w", err)
	}

	logger.Info("gateway account configuration loaded",
		"gateway_count", len(cfg.Mappings))

	return cfg, nil
}

// parseLogLevel converts a string log level to slog.Level.
// Supports: debug, info, warn, error (case-insensitive). Defaults to info.
func parseLogLevel(levelStr string) slog.Level {
	switch strings.ToLower(levelStr) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
