// Package main is the entry point for the FinancialAccounting service.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	"github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
	"github.com/meridianhub/meridian/services/financial-accounting/config"
	serviceobs "github.com/meridianhub/meridian/services/financial-accounting/observability"
	"github.com/meridianhub/meridian/services/financial-accounting/service"
	"github.com/meridianhub/meridian/services/financial-accounting/worker"
	ibaclient "github.com/meridianhub/meridian/services/internal-account/client"
	"github.com/meridianhub/meridian/services/reference-data/cache"
	refclient "github.com/meridianhub/meridian/services/reference-data/client"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/kafka"
	"github.com/meridianhub/meridian/shared/platform/ports"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

// Build information set via ldflags during compilation
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// Package-level variables for lifecycle management
var (
	outboxWorker             *events.Worker
	idempotencyCleanupWorker *worker.IdempotencyCleanupWorker
)

// Static errors for configuration validation
var (
	ErrBankCashAccountIDRequired = errors.New("BANK_CASH_ACCOUNT_ID environment variable is required")
	ErrBankCashAccountIDInvalid  = errors.New("BANK_CASH_ACCOUNT_ID must be a valid UUID")
)

// Static errors for production environment requirements
var (
	ErrRedisRequiredInProduction = errors.New("redis required for idempotency in production environment")
	ErrKafkaRequiredInProduction = errors.New("kafka required for event publishing in production environment")
)

func main() {
	// Initialize structured logging with configurable log level
	// Note: bootstrap.NewLogger hardcodes INFO level, so we create logger manually for LOG_LEVEL support
	logLevel := parseLogLevel(os.Getenv("LOG_LEVEL"))
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))
	slog.SetDefault(logger)

	logger.Info("starting financial-accounting service",
		"version", Version,
		"commit", Commit,
		"build_date", BuildDate)

	// Log environment for operational visibility
	environment := env.GetEnvOrDefault("ENVIRONMENT", "production")
	logger.Info("service environment configured", "environment", environment)

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

	// Initialize OpenTelemetry tracer
	tracer, err := bootstrap.NewTracer(ctx, bootstrap.TracerConfig{
		ServiceName:    "financial-accounting-service",
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

	// Initialize audit system with Kafka publisher
	auditPublisher, err := initAuditPublisher(logger)
	if err != nil {
		// Non-fatal: audit will use outbox fallback
		logger.Warn("failed to initialize audit Kafka publisher, using outbox fallback",
			"error", err)
	}
	if auditPublisher != nil {
		defer func() {
			if err := auditPublisher.Close(); err != nil {
				logger.Error("failed to close audit publisher", "error", err)
			}
		}()
	}

	// Initialize outbox repository and worker for transactional event publishing.
	// The outbox pattern ensures at-least-once delivery of events by storing them
	// in the database first and then publishing asynchronously via Kafka.
	outboxRepo := events.NewPostgresOutboxRepository(db)
	outboxPublisher := events.NewOutboxPublisher("financial-accounting")

	// Initialize Kafka producer for outbox worker (optional - depends on KAFKA_BOOTSTRAP_SERVERS)
	var kafkaProducer *kafka.ProtoProducer
	var usingNoopEventPublisher bool
	bootstrapServers := env.GetEnvOrDefault("KAFKA_BOOTSTRAP_SERVERS", "")
	if bootstrapServers != "" {
		producer, err := kafka.NewProtoProducer(kafka.ProducerConfig{
			BootstrapServers: bootstrapServers,
			ClientID:         "financial-accounting-outbox-worker",
			Acks:             "all",
			Retries:          3,
			Compression:      "snappy",
		})
		if err != nil {
			// Fail fast in production - Kafka is required for event publishing
			if env.IsProduction() {
				logger.Error("CRITICAL: Failed to create Kafka producer in production - failing fast",
					"error", err)
				return bootstrap.Permanent(fmt.Errorf("%w: %w", ErrKafkaRequiredInProduction, err))
			}
			logger.Warn("failed to create Kafka producer for outbox worker - DEVELOPMENT ONLY",
				"error", err,
				"environment", os.Getenv("ENVIRONMENT"))
			usingNoopEventPublisher = true
		} else {
			kafkaProducer = producer
			logger.Info("Kafka producer initialized for outbox worker",
				"bootstrap_servers", bootstrapServers)
		}
	} else {
		// Fail fast in production - Kafka is required for event publishing
		if env.IsProduction() {
			logger.Error("CRITICAL: Kafka unavailable in production - failing fast",
				"reason", "KAFKA_BOOTSTRAP_SERVERS not set")
			return bootstrap.Permanent(ErrKafkaRequiredInProduction)
		}
		logger.Warn("outbox worker disabled - DEVELOPMENT ONLY",
			"reason", "KAFKA_BOOTSTRAP_SERVERS not set",
			"environment", os.Getenv("ENVIRONMENT"))
		usingNoopEventPublisher = true
	}

	// Start outbox worker if Kafka producer is available
	if kafkaProducer != nil {
		workerConfig := events.DefaultWorkerConfig("financial-accounting")
		outboxWorker = events.NewWorker(outboxRepo, kafkaProducer, workerConfig, logger)
		outboxWorker.Start(ctx)
		// Ensure worker is stopped if run() returns early (e.g., init failure after this point).
		// The shutdown block also calls Stop(), but calling it twice is safe.
		defer outboxWorker.Stop()
		logger.Info("outbox worker started",
			"batch_size", workerConfig.BatchSize,
			"poll_interval", workerConfig.PollInterval)
	}

	// Validate bank cash account ID is configured (permanent config error)
	bankCashAccountID := env.GetEnvOrDefault("BANK_CASH_ACCOUNT_ID", "")
	if bankCashAccountID == "" {
		return bootstrap.Permanent(ErrBankCashAccountIDRequired)
	}

	// Validate UUID format (permanent config error)
	if _, err := uuid.Parse(bankCashAccountID); err != nil {
		return bootstrap.Permanent(fmt.Errorf("%w: %w", ErrBankCashAccountIDInvalid, err))
	}

	logger.Info("bank cash account configured",
		"account_id", bankCashAccountID)

	// Create ledger repository
	ledgerRepo := persistence.NewLedgerRepository(db)

	// Initialize Internal Account client (optional - for dynamic clearing account lookup)
	var accountResolver *service.AccountResolver
	ibaServiceURL := env.GetEnvOrDefault("INTERNAL_ACCOUNT_SERVICE_URL",
		env.GetEnvOrDefault("INTERNAL_BANK_ACCOUNT_SERVICE_URL", ""))
	if ibaServiceURL != "" {
		ibaClient, ibaCleanup, err := ibaclient.New(ibaclient.Config{
			Target:  ibaServiceURL,
			Timeout: service.DefaultLookupTimeout,
		})
		if err != nil {
			// Non-fatal: fall back to static config
			logger.Warn("failed to create Internal Account client, using static clearing account",
				"error", err,
				"service_url", ibaServiceURL)
		} else {
			defer ibaCleanup()

			resolver, err := service.NewAccountResolver(service.AccountResolverConfig{
				Client: ibaClient,
				Logger: logger,
			})
			if err != nil {
				logger.Warn("failed to create AccountResolver, using static clearing account",
					"error", err)
			} else {
				accountResolver = resolver
				logger.Info("dynamic clearing account resolution enabled",
					"service_url", ibaServiceURL)
			}
		}
	} else {
		logger.Info("dynamic clearing account resolution disabled (INTERNAL_ACCOUNT_SERVICE_URL not set)")
	}

	// Create posting service with optional AccountResolver
	postingService := service.NewPostingServiceWithConfig(service.PostingServiceConfig{
		Repo:              ledgerRepo,
		BankCashAccountID: bankCashAccountID,
		AccountResolver:   accountResolver,
		Logger:            logger,
	})

	// Create Redis client and idempotency service.
	// In production: fail fast if Redis is unavailable (idempotency is critical).
	// In non-production: use NoopService for graceful degradation with metrics.
	var idempotencySvc idempotency.Service
	var redisSvc *idempotency.RedisService // Keep reference for cleanup worker
	var usingNoopIdempotency bool
	redisClient, err := createRedisClient(logger)
	if err != nil {
		if env.IsProduction() {
			logger.Error("CRITICAL: Redis unavailable in production - failing fast",
				"error", err)
			return bootstrap.Permanent(fmt.Errorf("%w: %w", ErrRedisRequiredInProduction, err))
		}
		logger.Warn("Redis not available at startup, using noop idempotency service - DEVELOPMENT ONLY",
			"error", err,
			"environment", os.Getenv("ENVIRONMENT"))
		idempotencySvc = idempotency.NewNoopService(logger)
		usingNoopIdempotency = true
		serviceobs.SetNoopIdempotencyActive(true)
		serviceobs.RecordServiceDegradation(serviceobs.ComponentIdempotency, serviceobs.DegradationReasonStartupFallback)
	} else {
		redisSvc = idempotency.NewRedisService(redisClient)
		idempotencySvc = redisSvc
		serviceobs.SetNoopIdempotencyActive(false)
		logger.Info("idempotency service initialized with Redis")
		defer func() {
			if err := redisClient.Close(); err != nil {
				logger.Error("failed to close Redis client", "error", err)
			}
		}()
	}

	// Initialize idempotency cleanup worker (only if Redis is available)
	cleanupConfig := config.LoadIdempotencyCleanupConfig()
	if redisSvc != nil && cleanupConfig.Enabled {
		var workerErr error
		idempotencyCleanupWorker, workerErr = worker.NewIdempotencyCleanupWorker(
			redisSvc,
			cleanupConfig,
			logger,
		)
		if workerErr != nil {
			logger.Error("failed to create idempotency cleanup worker", "error", workerErr)
		} else {
			// Start cleanup worker in background
			go func() {
				if err := idempotencyCleanupWorker.Start(ctx); err != nil {
					logger.Error("idempotency cleanup worker error", "error", err)
				}
			}()
			logger.Info("idempotency cleanup worker started",
				"stale_threshold", cleanupConfig.StaleThreshold,
				"run_interval", cleanupConfig.RunInterval,
				"batch_size", cleanupConfig.BatchSize)
		}
	} else if !cleanupConfig.Enabled {
		logger.Info("idempotency cleanup worker disabled by configuration")
	}

	// Create event publisher
	// Note: The primary event publishing mechanism is the outbox pattern (transactional).
	// This direct publisher is for optional synchronous publishing scenarios.
	eventPublisher := &noopEventPublisher{}
	if usingNoopEventPublisher {
		logger.Warn("using noop event publisher - DEVELOPMENT ONLY",
			"environment", os.Getenv("ENVIRONMENT"))
		serviceobs.SetNoopEventPublisherActive(true)
		serviceobs.RecordServiceDegradation(serviceobs.ComponentEventPublisher, serviceobs.DegradationReasonStartupFallback)
	} else {
		serviceobs.SetNoopEventPublisherActive(false)
		logger.Info("event publisher initialized (noop mode for direct publishing, outbox handles primary events)")
	}

	// Initialize reference-data client for fungibility validation (optional)
	var registryOption service.Option
	referenceDataURL := env.GetEnvOrDefault("REFERENCE_DATA_SERVICE_URL", "")
	if referenceDataURL != "" {
		refClient, refCleanup, refErr := refclient.New(ctx, refclient.Config{
			Target: referenceDataURL,
		})
		if refErr != nil {
			// Non-fatal: fungibility validation will be skipped
			logger.Warn("failed to create reference-data client, fungibility validation disabled",
				"error", refErr,
				"service_url", referenceDataURL)
		} else {
			defer func() {
				if err := refCleanup(); err != nil {
					logger.Error("failed to close reference-data client", "error", err)
				}
			}()

			// Create adapter to translate between reference-data client and service interface
			registryAdapter := service.NewReferenceDataRegistryAdapter(&referenceDataClientAdapter{client: refClient})
			registryOption = service.WithRegistry(registryAdapter)
			logger.Info("fungibility validation enabled",
				"reference_data_service", referenceDataURL)
		}
	} else {
		logger.Info("fungibility validation disabled (REFERENCE_DATA_SERVICE_URL not set)")
	}

	// Create Financial Accounting service
	var svcOpts []service.Option
	if registryOption != nil {
		svcOpts = append(svcOpts, registryOption)
	}
	financialAccountingSvc, err := service.NewFinancialAccountingService(
		ledgerRepo,
		eventPublisher,
		idempotencySvc,
		outboxPublisher,
		outboxRepo,
		svcOpts...,
	)
	if err != nil {
		return fmt.Errorf("failed to create financial accounting service: %w", err)
	}

	logger.Info("financial accounting service initialized")
	_ = postingService // Available for internal use

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

	// Register Financial Accounting gRPC service
	financialaccountingv1.RegisterFinancialAccountingServiceServer(grpcServer, financialAccountingSvc)

	// Register health check service with database connectivity check
	healthChecker, err := serviceobs.NewHealthChecker(serviceobs.HealthCheckerConfig{
		DB:                   db,
		Logger:               logger,
		ServiceName:          "financial-accounting",
		CheckTimeout:         defaults.DefaultHealthCheckTimeout,
		UsingNoopIdempotency: usingNoopIdempotency,
		UsingNoopEvents:      usingNoopEventPublisher,
	})
	if err != nil {
		return fmt.Errorf("failed to create health checker: %w", err)
	}
	grpc_health_v1.RegisterHealthServer(grpcServer, healthChecker)

	// Register reflection service for debugging
	reflection.Register(grpcServer)

	logger.Info("gRPC services registered")

	// Get ports from environment
	port := env.GetEnvOrDefault("GRPC_PORT", strconv.Itoa(ports.FinancialAccounting))
	address := fmt.Sprintf(":%s", port)
	metricsPort := env.GetEnvOrDefault("METRICS_PORT", "8082")

	// Create listener
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", address)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", address, err)
	}

	// Start gRPC server in background
	// Buffer size must match number of goroutines writing to this channel
	// to prevent deadlock on simultaneous failures (gRPC + HTTP = 2)
	serverErrors := make(chan error, 2)
	go func() {
		logger.Info("starting gRPC server", "address", address)
		if err := grpcServer.Serve(listener); err != nil {
			serverErrors <- err
		}
	}()

	// Start HTTP server for metrics and health endpoints
	httpMux := http.NewServeMux()
	httpMux.Handle("/metrics", promhttp.Handler())
	httpMux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		// Simple health endpoint for HTTP probes
		resp, err := healthChecker.Check(r.Context(), &grpc_health_v1.HealthCheckRequest{})
		if err != nil || resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
			w.WriteHeader(http.StatusServiceUnavailable)
			if _, err := w.Write([]byte("NOT_SERVING")); err != nil {
				logger.Warn("failed to write health response",
					"error", err,
					"endpoint", r.URL.Path,
					"remote_addr", r.RemoteAddr)
			}
			return
		}
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("SERVING")); err != nil {
			logger.Warn("failed to write health response",
				"error", err,
				"endpoint", r.URL.Path,
				"remote_addr", r.RemoteAddr)
		}
	})
	httpMux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		// Readiness endpoint - checks database connectivity
		resp, err := healthChecker.Check(r.Context(), &grpc_health_v1.HealthCheckRequest{Service: "database"})
		if err != nil || resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
			w.WriteHeader(http.StatusServiceUnavailable)
			if _, err := w.Write([]byte("NOT_READY")); err != nil {
				logger.Warn("failed to write readiness response",
					"error", err,
					"endpoint", r.URL.Path,
					"remote_addr", r.RemoteAddr)
			}
			return
		}
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("READY")); err != nil {
			logger.Warn("failed to write readiness response",
				"error", err,
				"endpoint", r.URL.Path,
				"remote_addr", r.RemoteAddr)
		}
	})

	httpServer := &http.Server{
		Addr:              fmt.Sprintf(":%s", metricsPort),
		Handler:           httpMux,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      30 * time.Second,
	}

	go func() {
		logger.Info("starting HTTP server for metrics", "address", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server error", "error", err)
			serverErrors <- fmt.Errorf("HTTP server error: %w", err)
		}
	}()

	// Wait for interrupt signal or server error
	sigChan, signalCleanup := bootstrap.SignalHandler()
	defer signalCleanup()

	var runErr error
	select {
	case sig := <-sigChan:
		logger.Info("received signal", "signal", sig)
	case err := <-serverErrors:
		logger.Error("server error", "error", err)
		runErr = fmt.Errorf("server error: %w", err)
	}

	// Graceful shutdown (runs for both signal and server error paths)
	logger.Info("shutting down server...")

	// Create shutdown context with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), defaults.DefaultRPCTimeout)
	defer cancel()

	// Close database connection during shutdown
	defer bootstrap.CloseDatabase(db, logger)

	// Stop idempotency cleanup worker
	if idempotencyCleanupWorker != nil {
		logger.Info("stopping idempotency cleanup worker...")
		idempotencyCleanupWorker.Stop()
		logger.Info("idempotency cleanup worker stopped")
	}

	// Stop outbox worker first (stop processing before closing Kafka producer)
	if outboxWorker != nil {
		logger.Info("stopping outbox worker...")
		outboxWorker.Stop()
		logger.Info("outbox worker stopped")
	}

	// Close Kafka producer after outbox worker stops
	if kafkaProducer != nil {
		logger.Info("flushing and closing Kafka producer...")
		// FlushWithTimeout ensures pending messages are delivered before closing
		if remaining := kafkaProducer.FlushWithTimeout(5000); remaining > 0 {
			logger.Warn("some messages not delivered before close", "remaining", remaining)
		}
		kafkaProducer.Close()
		logger.Info("Kafka producer closed")
	}

	// Shutdown HTTP server (faster, allows metrics scraping during gRPC drain)
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP server shutdown error", "error", err)
	} else {
		logger.Info("HTTP server stopped")
	}

	// Gracefully stop gRPC server
	stopped := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(stopped)
	}()

	// Wait for graceful stop or timeout
	select {
	case <-stopped:
		logger.Info("gRPC server stopped gracefully")
	case <-shutdownCtx.Done():
		logger.Warn("graceful shutdown timeout, forcing stop")
		grpcServer.Stop()
	}

	return runErr
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
		// Close client to release resources before returning error
		_ = client.Close()
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

// noopEventPublisher provides a no-operation implementation of service.EventPublisher.
// This allows the service to start without Kafka for development and testing.
// In production, use messaging.NewKafkaEventPublisher for proper event publishing.
type noopEventPublisher struct{}

func (p *noopEventPublisher) Publish(_ context.Context, _ service.DomainEvent) error {
	return nil
}

func (p *noopEventPublisher) PublishBatch(_ context.Context, _ []service.DomainEvent) error {
	return nil
}

// initAuditPublisher initializes the Kafka-based audit publisher.
// Returns nil if Kafka is not configured (KAFKA_BOOTSTRAP_SERVERS is empty),
// which causes the audit system to use outbox fallback only.
//
// Environment variables:
//   - KAFKA_BOOTSTRAP_SERVERS: Kafka broker addresses (e.g., "kafka:9092")
//   - KAFKA_AUDIT_TOPIC: Topic for audit events (default: "audit.events.v1")
func initAuditPublisher(logger *slog.Logger) (*audit.Publisher, error) {
	// Set schema name for audit events
	audit.SetSchemaName("financial_accounting")

	bootstrapServers := env.GetEnvOrDefault("KAFKA_BOOTSTRAP_SERVERS", "")
	if bootstrapServers == "" {
		logger.Info("audit Kafka publisher disabled: KAFKA_BOOTSTRAP_SERVERS not set")
		return nil, nil //nolint:nilnil // Intentionally returns nil when Kafka is not configured
	}

	topic := env.GetEnvOrDefault("KAFKA_AUDIT_TOPIC", kafka.AuditEventsTopic)

	config := audit.PublisherConfig{
		BootstrapServers: bootstrapServers,
		Topic:            topic,
		SchemaName:       "financial_accounting",
		ClientID:         "financial-accounting-audit-publisher",
	}

	publisher, err := audit.NewPublisher(config)
	if err != nil {
		if errors.Is(err, audit.ErrPublisherDisabled) {
			logger.Info("audit Kafka publisher disabled",
				"reason", err.Error())
			return nil, nil //nolint:nilnil // Disabled mode returns no publisher
		}
		return nil, fmt.Errorf("failed to create audit publisher: %w", err)
	}

	// Set global publisher for GORM hooks integration
	audit.SetGlobalPublisher(publisher)

	logger.Info("audit Kafka publisher initialized",
		"bootstrap_servers", bootstrapServers,
		"topic", topic,
		"schema", "financial_accounting")

	return publisher, nil
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

// referenceDataClientAdapter adapts refclient.Client to the service.ReferenceDataClient interface.
// This is necessary because the service layer defines its own interface to avoid direct
// dependency on the reference-data cache types.
type referenceDataClientAdapter struct {
	client *refclient.Client
}

// GetInstrument retrieves an instrument from the reference-data service's tiered cache.
func (a *referenceDataClientAdapter) GetInstrument(ctx context.Context, code string, version int) (service.CachedInstrumentResult, error) {
	cached, err := a.client.GetInstrument(ctx, code, version)
	if err != nil {
		return nil, err
	}
	return &cachedInstrumentResultAdapter{cached: cached}, nil
}

// cachedInstrumentResultAdapter adapts cache.CachedInstrument to service.CachedInstrumentResult.
type cachedInstrumentResultAdapter struct {
	cached *cache.CachedInstrument
}

// GetBucketKeyProgram returns the CEL program for fungibility key evaluation.
func (a *cachedInstrumentResultAdapter) GetBucketKeyProgram() interface{} {
	return a.cached.BucketKeyProgram
}
