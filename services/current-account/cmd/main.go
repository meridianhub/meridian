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
	finacctclient "github.com/meridianhub/meridian/services/financial-accounting/client"
	internalaccountclient "github.com/meridianhub/meridian/services/internal-account/client"
	partyclient "github.com/meridianhub/meridian/services/party/client"
	poskeepingclient "github.com/meridianhub/meridian/services/position-keeping/client"
	refdataclient "github.com/meridianhub/meridian/services/reference-data/client"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/kafka"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/meridianhub/meridian/shared/platform/ports"
	"github.com/sony/gobreaker/v2"
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

	logger.Info("external service configuration",
		"position_keeping", fmt.Sprintf("position-keeping.%s.svc.cluster.local:%d", namespace, ports.PositionKeeping),
		"financial_accounting", fmt.Sprintf("financial-accounting.%s.svc.cluster.local:%d", namespace, ports.FinancialAccounting),
		"load_balancing", "DNS-based round_robin")

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
	grpcServer := bootstrap.NewGrpcServerBuilder(tracer, logger).
		WithAuthInterceptor(authInterceptor).
		Build()

	// Register services
	pb.RegisterCurrentAccountServiceServer(grpcServer, currentAccountService)

	// Register health check service with dependency checking
	// Health clients bypass the circuit breaker used for business operations
	healthChecker, err := service.NewHealthChecker(service.HealthCheckerConfig{
		Repository:                      repo,
		PositionKeepingClient:           svcClients.positionKeeping,
		PositionKeepingHealthClient:     svcClients.positionKeepingHealth,
		FinancialAccountingClient:       svcClients.financialAccounting,
		FinancialAccountingHealthClient: svcClients.financialAccountingHealth,
		Logger:                          logger,
		ServiceName:                     "current-account",
		CheckTimeout:                    defaults.DefaultHealthCheckTimeout,
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
type serviceClients struct {
	positionKeeping     service.PositionKeepingClient
	financialAccounting service.FinancialAccountingClient
	party               service.PartyClient
	internalAccount     service.InternalAccountClient
	accountResolver     *service.AccountResolver
	// Health clients bypass the circuit breaker for health checks
	positionKeepingHealth     grpc_health_v1.HealthClient
	financialAccountingHealth grpc_health_v1.HealthClient
	// HandlerRegistry for Starlark saga execution with service client handlers.
	// This registry contains handlers that call real gRPC services (not mocks).
	// See PRD: docs/prd/starlark-service-bindings.md
	handlerRegistry *saga.HandlerRegistry
	// Cleanup functions for graceful shutdown
	cleanupFuncs []func()
}

// createServiceWithClients creates the service and returns it along with the external clients
// for use in health checking. This approach creates the clients once and shares them between
// the service and health checker to avoid duplicate connections.
//
// Uses the new service-owned client packages with built-in resilience patterns:
//   - services/position-keeping/client
//   - services/financial-accounting/client
//   - services/party/client
//
// Each client is configured with DNS-based client-side load balancing and optional
// circuit breaker + retry resilience.
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

	// Create Position Keeping client using service-owned client package
	// The new client has built-in resilience patterns (circuit breaker + retry)
	posKeepingClient, posKeepingCleanup, err := poskeepingclient.New(poskeepingclient.Config{
		ServiceName: poskeepingclient.ServiceName,
		Namespace:   namespace,
		Port:        ports.PositionKeeping,
		Timeout:     defaults.DefaultRPCTimeout,
		Tracer:      tracer,
		Resilience: &sharedclients.ResilientClientConfig{
			Logger:             logger,
			CircuitBreakerName: "position-keeping",
			OnStateChange:      makeCircuitBreakerCallback(),
		},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create position keeping client: %w", err)
	}
	cleanupFuncs = append(cleanupFuncs, posKeepingCleanup)

	// Create Financial Accounting client using service-owned client package
	finAcctClient, finAcctCleanup, err := finacctclient.New(finacctclient.Config{
		ServiceName: finacctclient.ServiceName,
		Namespace:   namespace,
		Port:        ports.FinancialAccounting,
		Timeout:     defaults.DefaultRPCTimeout,
		Tracer:      tracer,
		Resilience: &sharedclients.ResilientClientConfig{
			Logger:             logger,
			CircuitBreakerName: "financial-accounting",
			OnStateChange:      makeCircuitBreakerCallback(),
		},
	})
	if err != nil {
		// Cleanup already created clients before returning
		for _, cleanup := range cleanupFuncs {
			cleanup()
		}
		return nil, nil, fmt.Errorf("failed to create financial accounting client: %w", err)
	}
	cleanupFuncs = append(cleanupFuncs, finAcctCleanup)

	// Create Party client using service-owned client package
	// PartyClient requires a wrapper for ValidateParty/GetParty methods
	partyBaseClient, partyCleanup, err := partyclient.New(partyclient.Config{
		ServiceName: partyclient.ServiceName,
		Namespace:   namespace,
		Port:        ports.Party,
		Timeout:     defaults.DefaultRPCTimeout,
		Tracer:      tracer,
		Resilience: &sharedclients.ResilientClientConfig{
			Logger:             logger,
			CircuitBreakerName: "party",
			OnStateChange:      makeCircuitBreakerCallback(),
		},
	})
	if err != nil {
		// Cleanup already created clients before returning
		for _, cleanup := range cleanupFuncs {
			cleanup()
		}
		return nil, nil, fmt.Errorf("failed to create party client: %w", err)
	}
	cleanupFuncs = append(cleanupFuncs, partyCleanup)

	// Wrap the party client with CurrentAccount-specific methods (ValidateParty, GetParty)
	partyClientWrapper := NewPartyClientWrapper(partyBaseClient)

	// Create Internal Account client for dynamic clearing account resolution.
	// This is optional - if it fails, the service will fall back to static config.
	var internalAccountClient *internalaccountclient.Client
	var accountResolver *service.AccountResolver

	ibaClient, ibaCleanup, err := internalaccountclient.New(internalaccountclient.Config{
		ServiceName: internalaccountclient.ServiceName,
		Namespace:   namespace,
		Port:        ports.InternalAccount,
		Timeout:     defaults.DefaultRPCTimeout,
		Tracer:      tracer,
		Resilience: &sharedclients.ResilientClientConfig{
			Logger:             logger,
			CircuitBreakerName: "internal-account",
			OnStateChange:      makeCircuitBreakerCallback(),
		},
	})
	if err != nil {
		logger.Warn("internal account client not available, clearing accounts will use static config",
			"error", err)
	} else {
		internalAccountClient = ibaClient
		cleanupFuncs = append(cleanupFuncs, ibaCleanup)

		// Create AccountResolver with the client
		accountResolver, err = service.NewAccountResolver(service.AccountResolverConfig{
			Client:   internalAccountClient,
			Logger:   logger,
			CacheTTL: service.DefaultCacheTTL,
		})
		if err != nil {
			logger.Warn("failed to create account resolver, clearing accounts will use static config",
				"error", err)
			accountResolver = nil
		} else {
			logger.Info("dynamic clearing account resolution enabled via Internal Account service")
		}
	}

	// Create Reference Data client for instrument lookup (required for fungibility validation).
	// This is optional - if it fails, fungibility validation is disabled.
	var fungibilityValidator *service.FungibilityValidator

	refDataClient, refDataCleanup, err := refdataclient.New(context.Background(), refdataclient.Config{
		ServiceName: refdataclient.ServiceName,
		Namespace:   namespace,
		Port:        ports.ReferenceData,
		Timeout:     defaults.DefaultRPCTimeout,
		Tracer:      tracer,
		Resilience: &sharedclients.ResilientClientConfig{
			Logger:             logger,
			CircuitBreakerName: "reference-data",
			OnStateChange:      makeCircuitBreakerCallback(),
		},
	})
	if err != nil {
		logger.Warn("reference data client not available, fungibility validation disabled",
			"error", err)
	} else {
		// Wrap the cleanup function to match expected signature (func() instead of func() error)
		cleanupFuncs = append(cleanupFuncs, func() {
			if closeErr := refDataCleanup(); closeErr != nil {
				logger.Error("failed to close reference data client", "error", closeErr)
			}
		})
		fungibilityValidator = service.NewFungibilityValidator(refDataClient)
		logger.Info("fungibility validation enabled via Reference Data service")
	}

	// Create service with the pre-created clients
	// The new service-owned clients implement the same interfaces as defined in
	// services/current-account/service/client_interfaces.go
	svc, err := service.NewServiceWithExistingClients(
		repo,
		lienRepo,
		withdrawalRepo,
		outboxRepo,         // Outbox repository for reliable event delivery
		db,                 // Database connection for transaction management
		posKeepingClient,   // *poskeepingclient.Client implements service.PositionKeepingClient
		finAcctClient,      // *finacctclient.Client implements service.FinancialAccountingClient
		partyClientWrapper, // *PartyClientWrapper implements service.PartyClient
		accountConfig,
		idempotencyService,
		logger,
		tracer,
		accountResolver,      // Optional: dynamic clearing account resolution
		fungibilityValidator, // Optional: validates fungibility for non-fungible instruments
	)
	if err != nil {
		// Cleanup all clients before returning
		for _, cleanup := range cleanupFuncs {
			cleanup()
		}
		return nil, nil, fmt.Errorf("failed to create service with existing clients: %w", err)
	}

	// Create Starlark handler registry with service client handlers.
	// This enables saga scripts to call real services (not mocks).
	// See PRD: docs/prd/starlark-service-bindings.md
	handlerRegistry := saga.NewHandlerRegistry()

	// Register handlers from service clients.
	// Each RegisterStarlarkHandlers function adapts Starlark params (map[string]any)
	// to gRPC client calls, propagating saga metadata (idempotency, correlation, etc.)
	if err := poskeepingclient.RegisterStarlarkHandlers(handlerRegistry, posKeepingClient); err != nil {
		for _, cleanup := range cleanupFuncs {
			cleanup()
		}
		return nil, nil, fmt.Errorf("failed to register position-keeping handlers: %w", err)
	}

	if err := finacctclient.RegisterStarlarkHandlers(handlerRegistry, finAcctClient); err != nil {
		for _, cleanup := range cleanupFuncs {
			cleanup()
		}
		return nil, nil, fmt.Errorf("failed to register financial-accounting handlers: %w", err)
	}

	// Register current-account handlers (for self-referential operations in sagas)
	// Note: We need a current-account client that connects to this service's gRPC endpoint.
	// For now, we create one using the same namespace/port configuration.
	currentAcctClient, currentAcctCleanup, err := currentaccountclient.New(currentaccountclient.Config{
		ServiceName: currentaccountclient.ServiceName,
		Namespace:   namespace,
		Port:        ports.CurrentAccount,
		Timeout:     defaults.DefaultRPCTimeout,
		Tracer:      tracer,
		// No resilience for self-calls - we want fast failure for local issues
	})
	if err != nil {
		// Current-account handlers are optional - service can work without them
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

	// Create health clients from the underlying gRPC connections
	// These bypass the circuit breaker to avoid health checks tripping business operation circuit breakers
	svcClients := &serviceClients{
		positionKeeping:           posKeepingClient,
		financialAccounting:       finAcctClient,
		party:                     partyClientWrapper,
		internalAccount:           internalAccountClient,
		accountResolver:           accountResolver,
		positionKeepingHealth:     grpc_health_v1.NewHealthClient(posKeepingClient.Conn()),
		financialAccountingHealth: grpc_health_v1.NewHealthClient(finAcctClient.Conn()),
		handlerRegistry:           handlerRegistry,
		cleanupFuncs:              cleanupFuncs,
	}

	return svc, svcClients, nil
}

// PartyClientWrapper is defined in party_wrapper.go

// makeCircuitBreakerCallback creates a circuit breaker state change callback
// that records metrics for the given service name.
func makeCircuitBreakerCallback() func(string, gobreaker.State, gobreaker.State) {
	return func(name string, from gobreaker.State, to gobreaker.State) {
		caobservability.RecordCircuitBreakerState(name, gobreakerStateToMetricState(to))
		caobservability.RecordCircuitBreakerStateChange(name, from.String(), to.String())
	}
}

// gobreakerStateToMetricState converts a gobreaker.State to the observability CircuitBreakerState
// for Prometheus metrics recording.
func gobreakerStateToMetricState(state gobreaker.State) caobservability.CircuitBreakerState {
	switch state {
	case gobreaker.StateClosed:
		return caobservability.CircuitBreakerStateClosed
	case gobreaker.StateHalfOpen:
		return caobservability.CircuitBreakerStateHalfOpen
	case gobreaker.StateOpen:
		return caobservability.CircuitBreakerStateOpen
	default:
		return caobservability.CircuitBreakerStateClosed
	}
}
