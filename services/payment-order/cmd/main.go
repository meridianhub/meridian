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
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	pb "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	webhookhttp "github.com/meridianhub/meridian/services/payment-order/adapters/http"
	pomessaging "github.com/meridianhub/meridian/services/payment-order/adapters/messaging"
	"github.com/meridianhub/meridian/services/payment-order/adapters/persistence"
	"github.com/meridianhub/meridian/services/payment-order/config"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	poobservability "github.com/meridianhub/meridian/services/payment-order/observability"
	"github.com/meridianhub/meridian/services/payment-order/service"
	"github.com/meridianhub/meridian/services/payment-order/worker"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/kafka"
	"github.com/meridianhub/meridian/shared/platform/ports"
	"github.com/meridianhub/meridian/shared/platform/redislock"
	"github.com/meridianhub/meridian/shared/platform/scheduler"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	// Service-owned clients (standardized client packages from each service)
	currentaccountclient "github.com/meridianhub/meridian/services/current-account/client"
	financialaccountingclient "github.com/meridianhub/meridian/services/financial-accounting/client"
	financialgatewayclient "github.com/meridianhub/meridian/services/financial-gateway/client"
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
		// BLOCKED: Cannot wire InvoiceGenerator or PaymentInitiator yet.
		//
		// InvoiceGenerator requires a worker.PositionKeepingClient adapter that wraps the gRPC
		// positionkeepingclient.Client with simplified signatures (GetAccountBalance returns
		// (int64, string, error) vs the proto request/response pattern). This adapter does not
		// exist yet.
		//
		// PaymentInitiator requires a worker.SagaClient (StartSaga/GetSagaStatus), which depends
		// on the Starlark saga runner being integrated into payment-order. The saga runtime exists
		// in shared/pkg/saga but is not wired into this service's main.go.
		//
		// Prerequisites:
		//   1. Create posKeepingClient adapter implementing worker.PositionKeepingClient
		//   2. Wire saga runner into payment-order and create SagaClient adapter
		//   3. Then: billingExecutor.WithInvoiceGenerator(invoiceGen).WithPaymentInitiator(paymentInit)

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
	grpcServer, err := bootstrap.NewGrpcServerBuilder(tracer, logger).
		WithAuthInterceptor(authInterceptor).
		Build()
	if err != nil {
		return fmt.Errorf("failed to build grpc server: %w", err)
	}

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
