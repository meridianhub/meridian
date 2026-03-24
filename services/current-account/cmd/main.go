// Package main is the entry point for the CurrentAccount service.
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
	"time"

	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	currentaccountclient "github.com/meridianhub/meridian/services/current-account/client"
	"github.com/meridianhub/meridian/services/current-account/config"
	caobservability "github.com/meridianhub/meridian/services/current-account/observability"
	"github.com/meridianhub/meridian/services/current-account/service"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/kafka"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/meridianhub/meridian/shared/platform/ports"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"gorm.io/gorm"
)

// Build information set via ldflags during compilation
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// Static errors for production environment requirements
var (
	ErrRedisRequiredInProduction = errors.New("redis required for idempotency in production environment")
)

func main() {
	// Initialize structured logging with configurable log level
	// Note: bootstrap.NewLogger hardcodes INFO level, so we create logger manually for LOG_LEVEL support
	logLevel := parseLogLevel(os.Getenv("LOG_LEVEL"))
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))
	slog.SetDefault(logger)

	logger.Info("starting current-account service",
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
	// Use a run-scoped context so lazy resolution goroutines stop when run() returns
	// (e.g., during RunWithRetry retries or shutdown).
	ctx, runCancel := context.WithCancel(context.Background())
	defer runCancel()

	// Initialize OpenTelemetry tracer
	tracer, err := bootstrap.NewTracer(ctx, bootstrap.TracerConfig{
		ServiceName:    "current-account-service",
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

	logger.Info("database connection established")

	// Create repositories
	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)
	outboxRepo := events.NewPostgresOutboxRepository(db)

	// Create Redis client and idempotency service.
	// In production: fail fast if Redis is unavailable (idempotency is critical).
	// In non-production: use NoopService for graceful degradation with metrics.
	var idempotencyService idempotency.Service
	redisConfig := bootstrap.DefaultRedisConfig()
	redisConfig.Logger = logger
	redisClient, redisErr := bootstrap.NewRedisClient(ctx, redisConfig)
	if redisErr != nil {
		if env.IsProduction() {
			logger.Error("CRITICAL: Redis unavailable in production - failing fast", "error", redisErr)
			return bootstrap.Permanent(fmt.Errorf("%w: %w", ErrRedisRequiredInProduction, redisErr))
		}
		logger.Warn("Redis not available at startup, using noop idempotency service - DEVELOPMENT ONLY",
			"error", redisErr,
			"environment", os.Getenv("ENVIRONMENT"))
		idempotencyService = idempotency.NewNoopService(logger)
		caobservability.SetNoopIdempotencyActive(true)
		caobservability.RecordServiceDegradation(caobservability.ComponentIdempotency, caobservability.DegradationReasonStartupFallback)
	} else {
		idempotencyService = idempotency.NewRedisService(redisClient)
		caobservability.SetNoopIdempotencyActive(false)
		logger.Info("idempotency service initialized with Redis")
		defer func() {
			if err := redisClient.Close(); err != nil {
				logger.Error("failed to close Redis client", "error", err)
			}
		}()
	}

	// Get Kubernetes namespace from environment (defaults to "default")
	namespace := env.GetEnvOrDefault("K8S_NAMESPACE", "default")

	logger.Info("service boundary mode: standalone (cross-service calls delegated to saga layer)",
		"namespace", namespace)

	// Create service with external clients and capture the clients for health checking
	currentAccountService, svcClients, err := createServiceWithClients(
		repo,
		lienRepo,
		withdrawalRepo,
		outboxRepo,
		db,
		namespace,
		logger,
		tracer,
		idempotencyService,
	)
	if err != nil {
		return fmt.Errorf("failed to create service: %w", err)
	}

	logger.Info("service initialized with external clients")

	// Initialize Kafka producer lazily for outbox worker (optional - graceful degradation).
	// Channels communicate the outbox stop function and Kafka cleanup function back to the
	// main goroutine to avoid data races.
	outboxStopCh := make(chan func(), 1)
	kafkaCleanupCh := make(chan func(), 1)
	kafkaBootstrapServers := env.GetEnvOrDefault("KAFKA_BOOTSTRAP_SERVERS", "")
	if kafkaBootstrapServers != "" {
		lazyKafka := bootstrap.NewLazyClient(ctx, "kafka-producer",
			func(_ context.Context) (*kafka.ProtoProducer, func(), error) {
				producer, err := kafka.NewProtoProducer(kafka.ProducerConfig{
					BootstrapServers: kafkaBootstrapServers,
					ClientID:         "current-account-outbox-worker",
					Acks:             "all",
					Retries:          5,
					Compression:      "snappy",
				})
				if err != nil {
					return nil, nil, err
				}
				return producer, func() { //nolint:contextcheck // FlushWithTimeout manages its own timeout
					logger.Info("flushing and closing Kafka producer...")
					if remaining := producer.FlushWithTimeout(5000); remaining > 0 {
						logger.Warn("some Kafka messages not delivered before close", "remaining", remaining)
					}
					producer.Close()
					logger.Info("Kafka producer closed")
				}, nil
			},
			bootstrap.WithLazyLogger(logger),
			bootstrap.WithLazyOnCleanup(func(cleanup func()) {
				kafkaCleanupCh <- cleanup
			}),
		)

		// Start outbox worker that will use the lazy Kafka producer once available.
		// Sends the stop function back via channel once the worker is running.
		go func() {
			for {
				producer, err := lazyKafka.Get()
				if err == nil {
					w := events.NewWorker(
						outboxRepo,
						producer,
						events.DefaultWorkerConfig("current-account"),
						logger.With("component", "outbox_worker"),
					)
					w.Start(ctx)
					logger.Info("outbox worker started",
						"kafka_brokers", kafkaBootstrapServers)
					outboxStopCh <- w.Stop
					return
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(1 * time.Second):
				}
			}
		}()
	} else {
		logger.Warn("KAFKA_BOOTSTRAP_SERVERS not configured, outbox pattern disabled")
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

	// Register services
	pb.RegisterCurrentAccountServiceServer(grpcServer, currentAccountService)

	// Register health check service with database dependency checking.
	// Cross-service health checks (position-keeping, financial-accounting) are not
	// wired here - each service is independently deployable.
	healthChecker, err := service.NewHealthChecker(service.HealthCheckerConfig{
		Repository:   repo,
		Logger:       logger,
		ServiceName:  "current-account",
		CheckTimeout: defaults.DefaultHealthCheckTimeout,
	})
	if err != nil {
		return fmt.Errorf("failed to create health checker: %w", err)
	}
	grpc_health_v1.RegisterHealthServer(grpcServer, healthChecker)

	// Register reflection service for debugging
	reflection.Register(grpcServer)

	logger.Info("gRPC services registered")

	// Get port from environment
	port := env.GetEnvOrDefault("GRPC_PORT", strconv.Itoa(ports.CurrentAccount))
	address := fmt.Sprintf(":%s", port)

	// Create listener
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", address)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", address, err)
	}

	// Start gRPC server in background
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("starting gRPC server", "address", address)
		if err := grpcServer.Serve(listener); err != nil {
			serverErrors <- err
		}
	}()

	// Wait for shutdown signal and orchestrate graceful shutdown
	orchestrator := bootstrap.NewShutdownOrchestrator(grpcServer, logger)

	// Register outbox worker and Kafka producer cleanup (LIFO: producer closes after worker stops).
	// The stop/cleanup functions arrive via channels once the background goroutine resolves Kafka.
	orchestrator.AddCleanup(func() error {
		select {
		case cleanupFn := <-kafkaCleanupCh:
			cleanupFn()
		default:
			// Kafka never resolved - nothing to close
		}
		return nil
	})
	orchestrator.AddCleanup(func() error {
		select {
		case stopFn := <-outboxStopCh:
			logger.Info("stopping outbox worker")
			stopFn()
			logger.Info("outbox worker stopped")
		default:
			// Worker never started (Kafka not resolved yet) - nothing to stop
		}
		return nil
	})

	// Register cleanup functions (LIFO order - external clients, then Redis, then database)
	// Register external client cleanup functions first (they get called last in LIFO)
	for _, cleanup := range svcClients.cleanupFuncs {
		fn := cleanup // capture for closure
		orchestrator.AddCleanup(func() error {
			fn()
			return nil
		})
	}

	// No additional Redis cleanup needed here — Redis client is closed via defer above when available
	orchestrator.AddCleanup(func() error {
		bootstrap.CloseDatabase(db, logger)
		return nil
	})

	// Cancel run-scoped context last (runs first in LIFO) to stop lazy resolution goroutines
	// before other cleanup proceeds. This prevents orphan goroutines across RunWithRetry retries.
	orchestrator.AddCleanup(func() error {
		runCancel()
		return nil
	})

	return orchestrator.Wait(serverErrors)
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

// serviceClients holds the clients created by createServiceWithClients.
// Cross-service clients (party, position-keeping, financial-accounting, internal-account)
// are NOT wired here - they belong in the saga orchestration layer.
type serviceClients struct {
	// HandlerRegistry for Starlark saga execution with service client handlers.
	// This registry contains handlers that call real gRPC services (not mocks).
	// See PRD: docs/prd/starlark-service-bindings.md
	handlerRegistry *saga.HandlerRegistry
	// Cleanup functions for graceful shutdown
	cleanupFuncs []func()
}

// createServiceWithClients creates the service and returns it along with any self-referential
// clients needed for Starlark saga handler registration.
//
// Cross-service clients (party, position-keeping, financial-accounting, internal-account,
// reference-data) are NOT created here. Cross-service coordination belongs in the saga
// orchestration layer, not in individual service binaries.
func createServiceWithClients(
	repo *persistence.Repository,
	lienRepo *persistence.LienRepository,
	withdrawalRepo *persistence.WithdrawalRepository,
	outboxRepo events.OutboxRepository,
	db *gorm.DB,
	namespace string,
	logger *slog.Logger,
	tracer *observability.Tracer,
	idempotencyService idempotency.Service,
) (*service.Service, *serviceClients, error) {
	// Load account configuration for clearing accounts (enables double-entry bookkeeping).
	// If not configured, the service operates in single-entry mode without clearing account postings.
	accountConfig, cfgErr := config.LoadAccountConfig()
	if cfgErr != nil {
		logger.Warn("account configuration not loaded, operating in single-entry mode",
			"error", cfgErr)
		accountConfig = nil
	} else {
		logger.Info("account configuration loaded",
			"deposit_clearing_account_id", accountConfig.DepositClearingAccountID)
	}

	// Track cleanup functions for graceful shutdown
	var cleanupFuncs []func()

	// Create service without cross-service clients.
	// Cross-service operations (position logging, ledger posting, party validation,
	// clearing account resolution) are handled by the saga orchestration layer.
	svc, err := service.NewServiceWithExistingClients(
		repo,
		lienRepo,
		withdrawalRepo,
		outboxRepo, // Outbox repository for reliable event delivery
		db,         // Database connection for transaction management
		nil,        // posKeepingClient - delegated to saga layer
		nil,        // finAcctClient - delegated to saga layer
		nil,        // partyClient - delegated to saga layer
		accountConfig,
		idempotencyService,
		logger,
		tracer,
		nil, // accountResolver - delegated to saga layer
		nil, // fungibilityValidator - delegated to saga layer
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create service: %w", err)
	}

	// Create Starlark handler registry with self-referential service client handlers.
	// Cross-service handlers (position-keeping, financial-accounting) are registered
	// at the saga orchestration layer, not here.
	handlerRegistry := saga.NewHandlerRegistry()

	// Register current-account handlers (for self-referential operations in sagas)
	currentAcctClient, currentAcctCleanup, err := currentaccountclient.New(currentaccountclient.Config{
		ServiceName: currentaccountclient.ServiceName,
		Namespace:   namespace,
		Port:        ports.CurrentAccount,
		Timeout:     defaults.DefaultRPCTimeout,
		Tracer:      tracer,
	})
	if err != nil {
		logger.Warn("failed to create current-account client for Starlark handlers",
			"error", err)
	} else {
		cleanupFuncs = append(cleanupFuncs, currentAcctCleanup)
		if regErr := currentaccountclient.RegisterStarlarkHandlers(handlerRegistry, currentAcctClient); regErr != nil {
			logger.Warn("failed to register current-account handlers", "error", regErr)
		}
	}

	logger.Info("Starlark handler registry initialized",
		"registered_handlers", len(handlerRegistry.List()))

	svcClients := &serviceClients{
		handlerRegistry: handlerRegistry,
		cleanupFuncs:    cleanupFuncs,
	}

	return svc, svcClients, nil
}
