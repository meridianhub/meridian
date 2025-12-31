// Package main is the entry point for the CurrentAccount service.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"

	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/clients"
	"github.com/meridianhub/meridian/services/current-account/config"
	"github.com/meridianhub/meridian/services/current-account/service"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/meridianhub/meridian/shared/platform/ports"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

// Build information set via ldflags during compilation
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
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

	// Run the service
	if err := run(logger); err != nil {
		logger.Error("service failed", "error", err)
		os.Exit(1)
	}

	logger.Info("service stopped gracefully")
}

func run(logger *slog.Logger) error {
	ctx := context.Background()

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

	// Create Redis client and idempotency service (optional - graceful degradation)
	var idempotencyService idempotency.Service
	redisConfig := bootstrap.DefaultRedisConfig()
	redisConfig.Logger = logger
	redisClient, err := bootstrap.NewRedisClient(ctx, redisConfig)
	if err != nil {
		logger.Warn("Redis not available, idempotency protection disabled",
			"error", err)
	} else {
		idempotencyService = idempotency.NewRedisService(redisClient)
		logger.Info("idempotency protection enabled with Redis")
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
		namespace,
		logger,
		tracer,
		idempotencyService,
	)
	if err != nil {
		return fmt.Errorf("failed to create service: %w", err)
	}

	logger.Info("service initialized with external clients")

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

	// Register cleanup functions (LIFO order - Redis before database)
	if redisClient != nil {
		orchestrator.AddCleanup(func() error {
			if err := redisClient.Close(); err != nil {
				logger.Error("failed to close Redis client", "error", err)
				return err
			}
			logger.Info("Redis client closed")
			return nil
		})
	}
	orchestrator.AddCleanup(func() error {
		bootstrap.CloseDatabase(db, logger)
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
	positionKeeping     clients.PositionKeepingClient
	financialAccounting clients.FinancialAccountingClient
	// Health clients bypass the circuit breaker for health checks
	positionKeepingHealth     grpc_health_v1.HealthClient
	financialAccountingHealth grpc_health_v1.HealthClient
}

// createServiceWithClients creates the service and returns it along with the external clients
// for use in health checking. This approach creates the clients once and shares them between
// the service and health checker to avoid duplicate connections.
//
// Uses DNS-based client-side load balancing for inter-service gRPC communication.
func createServiceWithClients(
	repo *persistence.Repository,
	lienRepo *persistence.LienRepository,
	namespace string,
	logger *slog.Logger,
	tracer *observability.Tracer,
	idempotencyService idempotency.Service,
) (*service.Service, *serviceClients, error) {
	// Load account configuration for clearing accounts
	// This is optional - if not configured, deposits will use single-entry mode (backward compatible)
	accountConfig, cfgErr := config.LoadAccountConfig()
	if cfgErr != nil {
		// Log warning but continue - double-entry is optional for backward compatibility
		logger.Warn("account configuration not loaded, double-entry bookkeeping disabled",
			"error", cfgErr)
		accountConfig = nil // Explicit nil for clarity
	} else {
		logger.Info("account configuration loaded",
			"deposit_clearing_account_id", accountConfig.DepositClearingAccountID)
	}

	// Create Position Keeping client with DNS-based load balancing
	posKeepingGRPCClient, err := clients.NewPositionKeepingClient(&clients.PositionKeepingClientConfig{
		ServiceName: "position-keeping",
		Namespace:   namespace,
		Port:        ports.PositionKeeping,
		Timeout:     defaults.DefaultRPCTimeout,
		Tracer:      tracer,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create position keeping client: %w", err)
	}

	// Wrap with resilience patterns (circuit breaker + retry)
	resilientPosKeepingClient := clients.NewResilientPositionKeepingClient(
		posKeepingGRPCClient,
		sharedclients.ResilientClientConfig{
			Logger: logger,
		},
	)

	// Create Financial Accounting client with DNS-based load balancing
	finAcctGRPCClient, err := clients.NewFinancialAccountingClient(&clients.FinancialAccountingClientConfig{
		ServiceName: "financial-accounting",
		Namespace:   namespace,
		Port:        ports.FinancialAccounting,
		Timeout:     defaults.DefaultRPCTimeout,
		Tracer:      tracer,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create financial accounting client: %w", err)
	}

	// Wrap with resilience patterns (circuit breaker + retry)
	resilientFinAcctClient := clients.NewResilientFinancialAccountingClient(
		finAcctGRPCClient,
		sharedclients.ResilientClientConfig{
			Logger: logger,
		},
	)

	// Create Party client with DNS-based load balancing
	partyGRPCClient, err := clients.NewPartyClient(&sharedclients.PartyClientConfig{
		ServiceName: "party",
		Namespace:   namespace,
		Port:        ports.Party,
		Timeout:     defaults.DefaultRPCTimeout,
		Tracer:      tracer,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create party client: %w", err)
	}

	// Wrap with resilience patterns (circuit breaker + retry)
	resilientPartyClient := clients.NewResilientPartyClient(
		partyGRPCClient,
		sharedclients.ResilientClientConfig{
			Logger: logger,
		},
	)

	// Create service with the pre-created clients
	svc, err := service.NewServiceWithExistingClients(
		repo,
		lienRepo,
		resilientPosKeepingClient,
		resilientFinAcctClient,
		resilientPartyClient,
		accountConfig,
		idempotencyService,
		logger,
		tracer,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create service with existing clients: %w", err)
	}

	// Create health clients from the underlying gRPC connections
	// These bypass the circuit breaker to avoid health checks tripping business operation circuit breakers
	clients := &serviceClients{
		positionKeeping:           resilientPosKeepingClient,
		financialAccounting:       resilientFinAcctClient,
		positionKeepingHealth:     grpc_health_v1.NewHealthClient(posKeepingGRPCClient.Conn()),
		financialAccountingHealth: grpc_health_v1.NewHealthClient(finAcctGRPCClient.Conn()),
	}

	return svc, clients, nil
}
