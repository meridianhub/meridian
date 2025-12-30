// Package main is the entry point for the Tenant service.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"time"

	pb "github.com/meridianhub/meridian/api/proto/meridian/tenant/v1"
	"github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	"github.com/meridianhub/meridian/services/tenant/clients"
	"github.com/meridianhub/meridian/services/tenant/provisioner"
	"github.com/meridianhub/meridian/services/tenant/service"
	"github.com/meridianhub/meridian/services/tenant/worker"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/env"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

// Build information set via ldflags during compilation.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// envValueTrue is the string value for enabled environment variables.
const envValueTrue = "true"

// Errors returned during configuration and startup.
var (
	ErrInvalidPollInterval    = fmt.Errorf("poll interval must be >= 1s")
	ErrInvalidMaxRetries      = fmt.Errorf("max retries must be >= 0 and <= 20")
	ErrInvalidRetryBaseDelay  = fmt.Errorf("retry base delay must be > 0")
	ErrInvalidRetryMaxDelay   = fmt.Errorf("retry max delay must be > 0")
	ErrInvalidRetryDelayRange = fmt.Errorf("retry base delay must be < retry max delay")
	ErrInvalidMaxConcurrent   = fmt.Errorf("max concurrent must be >= 1 and <= 100")
)

// WorkerConfig holds configuration for the provisioning worker behavior.
type WorkerConfig struct {
	PollInterval   time.Duration
	MaxRetries     int
	RetryBaseDelay time.Duration
	RetryMaxDelay  time.Duration
	MaxConcurrent  int
}

// Validate checks if the WorkerConfig has valid values.
// Returns an error if any configuration value is invalid.
func (c WorkerConfig) Validate() error {
	if c.PollInterval < 1*time.Second {
		return fmt.Errorf("%w: got %s", ErrInvalidPollInterval, c.PollInterval)
	}
	if c.MaxRetries < 0 || c.MaxRetries > 20 {
		return fmt.Errorf("%w: got %d", ErrInvalidMaxRetries, c.MaxRetries)
	}
	if c.RetryBaseDelay <= 0 {
		return fmt.Errorf("%w: got %s", ErrInvalidRetryBaseDelay, c.RetryBaseDelay)
	}
	if c.RetryMaxDelay <= 0 {
		return fmt.Errorf("%w: got %s", ErrInvalidRetryMaxDelay, c.RetryMaxDelay)
	}
	if c.RetryBaseDelay >= c.RetryMaxDelay {
		return fmt.Errorf("%w: base=%s, max=%s", ErrInvalidRetryDelayRange, c.RetryBaseDelay, c.RetryMaxDelay)
	}
	if c.MaxConcurrent < 1 || c.MaxConcurrent > 100 {
		return fmt.Errorf("%w: got %d", ErrInvalidMaxConcurrent, c.MaxConcurrent)
	}
	return nil
}

func main() {
	// Initialize structured logging with configurable log level
	// Note: bootstrap.NewLogger hardcodes INFO level, so we create logger manually for LOG_LEVEL support
	logLevel := parseLogLevel(os.Getenv("LOG_LEVEL"))
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))
	slog.SetDefault(logger)

	logger.Info("starting tenant service",
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
		ServiceName:    "tenant-service",
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

	// Create repository
	repo := persistence.NewRepository(db)

	// Initialize schema provisioner (optional - skipped if SCHEMA_PROVISIONING_ENABLED is not "true")
	var schemaProvisioner provisioner.SchemaProvisioner
	provisioningEnabled := env.GetEnvOrDefault("SCHEMA_PROVISIONING_ENABLED", "false")
	if provisioningEnabled == envValueTrue {
		config := provisioner.DefaultConfig()

		// Pass platform database connection (for tenant_provisioning table).
		// The provisioner will also connect to each service's database for schema creation.
		prov, err := provisioner.NewPostgresProvisioner(db, config)
		if err != nil {
			return fmt.Errorf("failed to create schema provisioner: %w", err)
		}
		schemaProvisioner = prov

		// Clean up service database connections on shutdown
		defer func() {
			if err := prov.Close(); err != nil {
				logger.Error("failed to close provisioner connections", "error", err)
			}
		}()

		logger.Info("schema provisioner initialized",
			"services", len(config.Services),
			"provisioning_timeout", config.ProvisioningTimeout)
	} else {
		logger.Warn("schema provisioning not enabled - tenant creation will not provision schemas",
			"hint", "set SCHEMA_PROVISIONING_ENABLED=true to enable schema provisioning")
	}

	// Initialize Party client (optional - skipped if PARTY_SERVICE_ENABLED is not "true")
	var partyClient clients.PartyClient
	namespace := env.GetEnvOrDefault("K8S_NAMESPACE", "default")
	partyEnabled := env.GetEnvOrDefault("PARTY_SERVICE_ENABLED", envValueTrue) == envValueTrue
	if partyEnabled {
		pc, err := clients.NewPartyClient(&sharedclients.PartyClientConfig{
			ServiceName: "party",
			Namespace:   namespace,
			Port:        50055,
			Timeout:     30 * time.Second,
			Tracer:      tracer,
		})
		if err != nil {
			return fmt.Errorf("failed to create party client: %w", err)
		}
		partyClient = pc
		defer func() {
			if err := pc.Close(); err != nil {
				logger.Error("failed to close party client", "error", err)
			}
		}()
		logger.Info("party client initialized",
			"service_name", "party",
			"namespace", namespace,
			"port", 50055)
	} else {
		logger.Warn("party client not configured - tenant creation will not register parties",
			"hint", "set PARTY_SERVICE_ENABLED=true to enable party registration")
	}

	// Initialize Redis client and slug cache (optional - skipped if REDIS_ENABLED is not "true")
	var slugCache *service.SlugCache
	redisEnabled := env.GetEnvOrDefault("REDIS_ENABLED", envValueTrue) == envValueTrue
	if redisEnabled {
		redisConfig := bootstrap.DefaultRedisConfig()
		redisConfig.Logger = logger
		redisClient, err := bootstrap.NewRedisClient(ctx, redisConfig)
		if err != nil {
			return fmt.Errorf("failed to create Redis client: %w", err)
		}
		defer func() {
			if err := redisClient.Close(); err != nil {
				logger.Error("failed to close Redis client", "error", err)
			}
		}()

		slugCache = service.NewSlugCache(redisClient)
		logger.Info("slug cache initialized with Redis backend")
	} else {
		logger.Warn("Redis not enabled - slug caching disabled",
			"hint", "set REDIS_ENABLED=true to enable slug caching")
	}

	// Create gRPC service
	tenantService := service.NewService(repo, schemaProvisioner, partyClient, slugCache, logger)

	// Create cached registry for validation middleware
	cachedRegistry := service.NewCachedRegistry(repo, service.CachedRegistryConfig{
		RefreshInterval: 60 * time.Second,
		Logger:          logger,
	})
	cachedRegistry.Start(ctx)

	logger.Info("cached tenant registry started",
		"refresh_interval", "60s")

	// Initialize provisioning worker (only if schema provisioning is enabled)
	var provisioningWorker *worker.ProvisioningWorker
	if provisioningEnabled == envValueTrue && schemaProvisioner != nil {
		config, err := loadWorkerConfig()
		if err != nil {
			return fmt.Errorf("failed to load worker configuration: %w", err)
		}

		provisioningWorker, err = worker.NewProvisioningWorker(
			repo,
			schemaProvisioner,
			worker.Config{
				PollInterval:   config.PollInterval,
				MaxRetries:     config.MaxRetries,
				RetryBaseDelay: config.RetryBaseDelay,
				RetryMaxDelay:  config.RetryMaxDelay,
				MaxConcurrent:  config.MaxConcurrent,
			},
			logger,
		)
		if err != nil {
			return fmt.Errorf("failed to create provisioning worker: %w", err)
		}

		// Start worker in background goroutine
		go provisioningWorker.Start(ctx)

		logger.Info("provisioning worker started")
	} else {
		logger.Info("provisioning worker disabled",
			"hint", "set SCHEMA_PROVISIONING_ENABLED=true to enable background provisioning")
	}

	// Initialize auth interceptor (optional - based on AUTH_ENABLED)
	authConfig := bootstrap.DefaultAuthConfig(logger)
	authInterceptor, err := bootstrap.NewAuthInterceptor(ctx, authConfig)
	if err != nil {
		return fmt.Errorf("failed to initialize auth: %w", err)
	}

	// Create gRPC server with interceptor chain
	// Order is handled by bootstrap: tracing -> platform auth -> platform admin -> recovery
	// Note: WithPlatformAdmin() adds PlatformAdminInterceptor for platform-layer services
	grpcServer := bootstrap.NewGrpcServerBuilder(tracer, logger).
		WithAuthInterceptor(authInterceptor).
		WithPlatformAdmin().
		Build()

	// Register services
	pb.RegisterTenantServiceServer(grpcServer, tenantService)

	// Register health check service
	healthChecker := service.NewHealthChecker(service.HealthCheckerConfig{
		Repository:  repo,
		Logger:      logger,
		ServiceName: "tenant",
		Timeout:     5 * time.Second,
	})
	grpc_health_v1.RegisterHealthServer(grpcServer, healthChecker)

	// Register reflection service for debugging
	reflection.Register(grpcServer)

	logger.Info("gRPC services registered")

	// Get port from environment
	port := env.GetEnvOrDefault("GRPC_PORT", "50056")
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

	// Add cleanup functions in reverse order of initialization (LIFO)
	// Stop provisioning worker first
	if provisioningWorker != nil {
		orchestrator.AddCleanup(func() error {
			logger.Info("stopping provisioning worker...")
			provisioningWorker.Stop()
			logger.Info("provisioning worker stopped")
			return nil
		})
	}

	// Close database connection
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

// loadWorkerConfig loads worker configuration from environment variables with defaults.
// It validates the configuration and returns an error if any value is invalid.
func loadWorkerConfig() (WorkerConfig, error) {
	config := WorkerConfig{
		PollInterval:   env.GetEnvAsDuration("PROVISIONING_WORKER_POLL_INTERVAL", 10*time.Second),
		MaxRetries:     env.GetEnvAsInt("PROVISIONING_MAX_RETRIES", 5),
		RetryBaseDelay: env.GetEnvAsDuration("PROVISIONING_RETRY_BASE_DELAY", 2*time.Second),
		RetryMaxDelay:  env.GetEnvAsDuration("PROVISIONING_RETRY_MAX_DELAY", 30*time.Second),
		MaxConcurrent:  env.GetEnvAsInt("PROVISIONING_MAX_CONCURRENT", 5),
	}

	// Validate configuration
	if err := config.Validate(); err != nil {
		return WorkerConfig{}, fmt.Errorf("invalid worker configuration: %w", err)
	}

	// Log loaded configuration with sources
	slog.Info("worker configuration loaded",
		"poll_interval", config.PollInterval,
		"poll_interval_source", getConfigSource("PROVISIONING_WORKER_POLL_INTERVAL"),
		"max_retries", config.MaxRetries,
		"max_retries_source", getConfigSource("PROVISIONING_MAX_RETRIES"),
		"retry_base_delay", config.RetryBaseDelay,
		"retry_base_delay_source", getConfigSource("PROVISIONING_RETRY_BASE_DELAY"),
		"retry_max_delay", config.RetryMaxDelay,
		"retry_max_delay_source", getConfigSource("PROVISIONING_RETRY_MAX_DELAY"),
		"max_concurrent", config.MaxConcurrent,
		"max_concurrent_source", getConfigSource("PROVISIONING_MAX_CONCURRENT"))

	return config, nil
}

// getConfigSource returns "env" if the environment variable is set, "default" otherwise.
func getConfigSource(key string) string {
	if os.Getenv(key) != "" {
		return "env"
	}
	return "default"
}
