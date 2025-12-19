// Package main is the entry point for the PaymentOrder service.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	pb "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	"github.com/meridianhub/meridian/services/payment-order/adapters/gateway"
	webhookhttp "github.com/meridianhub/meridian/services/payment-order/adapters/http"
	"github.com/meridianhub/meridian/services/payment-order/adapters/persistence"
	payclients "github.com/meridianhub/meridian/services/payment-order/clients"
	"github.com/meridianhub/meridian/services/payment-order/config"
	"github.com/meridianhub/meridian/services/payment-order/service"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/pkg/interceptors"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/kafka"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Build information set via ldflags during compilation.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// ErrMissingHMACSecret is returned when the WEBHOOK_HMAC_SECRET environment variable is not set.
var ErrMissingHMACSecret = errors.New("WEBHOOK_HMAC_SECRET environment variable is required")

func main() {
	// Initialize structured logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	logger.Info("starting payment-order service",
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
	tracerConfig, err := observability.DefaultConfig()
	if err != nil {
		return fmt.Errorf("failed to load tracer config: %w", err)
	}

	tracerConfig = tracerConfig.
		WithServiceName("payment-order-service").
		WithServiceVersion(Version)

	tracer, err := observability.NewTracer(ctx, tracerConfig)
	if err != nil {
		return fmt.Errorf("failed to initialize tracer: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tracer.Shutdown(shutdownCtx); err != nil {
			logger.Error("failed to shutdown tracer", "error", err)
		}
	}()

	logger.Info("tracer initialized",
		"service_name", tracerConfig.ServiceName,
		"environment", tracerConfig.Environment)

	// Initialize database connection
	db, err := initDatabase(logger)
	if err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}
	defer closeDatabase(db, logger)
	logger.Info("database connection established")

	// Create repository
	repo := persistence.NewPaymentOrderRepository(db)

	// Get Kubernetes namespace from environment
	namespace := getEnvOrDefault("K8S_NAMESPACE", "default")

	// Create external clients
	currentAccountClient, caCleanup, err := createCurrentAccountClient(namespace, logger)
	if err != nil {
		return fmt.Errorf("failed to create current account client: %w", err)
	}
	defer caCleanup()

	financialAccountingClient, faCleanup, err := createFinancialAccountingClient(namespace, logger)
	if err != nil {
		return fmt.Errorf("failed to create financial accounting client: %w", err)
	}
	defer faCleanup()

	// Create payment gateway
	paymentGateway := createPaymentGateway(logger)

	// Load gateway account configuration
	gatewayAccountConfig, err := createGatewayAccountConfig(logger)
	if err != nil {
		return fmt.Errorf("failed to load gateway account config: %w", err)
	}

	// Create Kafka producer
	kafkaProducer, err := createKafkaProducer(logger)
	if err != nil {
		return fmt.Errorf("failed to create Kafka producer: %w", err)
	}
	defer kafkaProducer.Close()

	// Create Redis client and idempotency service
	redisClient, err := createRedisClient(logger)
	if err != nil {
		return fmt.Errorf("failed to create Redis client: %w", err)
	}
	defer func() {
		if err := redisClient.Close(); err != nil {
			logger.Error("failed to close Redis client", "error", err)
		}
	}()
	idempotencyService := idempotency.NewRedisService(redisClient)

	// Create payment order service
	paymentOrderService, err := service.NewServiceWithConfig(service.Config{
		Repository:                repo,
		CurrentAccountClient:      currentAccountClient,
		FinancialAccountingClient: financialAccountingClient,
		PaymentGateway:            paymentGateway,
		GatewayAccountConfig:      gatewayAccountConfig,
		KafkaPublisher:            kafkaProducer,
		IdempotencyService:        idempotencyService,
		Logger:                    logger,
		Tracer:                    tracer,
	})
	if err != nil {
		return fmt.Errorf("failed to create payment order service: %w", err)
	}

	// Initialize auth interceptor (optional - based on AUTH_ENABLED)
	authInterceptor, err := initAuth(ctx, logger)
	if err != nil {
		return fmt.Errorf("failed to initialize auth: %w", err)
	}

	// Create gRPC server with interceptor chain
	// Order: tracing → auth → recovery (recovery last to catch all panics)
	var unaryInterceptors []grpc.UnaryServerInterceptor
	var streamInterceptors []grpc.StreamServerInterceptor

	// 1. Tracing (always first for full request coverage)
	unaryInterceptors = append(unaryInterceptors, tracer.UnaryServerInterceptor())
	streamInterceptors = append(streamInterceptors, tracer.StreamServerInterceptor())

	// 2. Auth (JWT validation with JWKS - optional)
	if authInterceptor != nil {
		unaryInterceptors = append(unaryInterceptors, authInterceptor.UnaryInterceptor())
		streamInterceptors = append(streamInterceptors, authInterceptor.StreamInterceptor())
		logger.Info("auth interceptor enabled in chain")
	} else {
		// When auth is disabled, use TenantExtractionInterceptor to get tenant from header
		unaryInterceptors = append(unaryInterceptors, auth.TenantExtractionInterceptor())
		streamInterceptors = append(streamInterceptors, auth.TenantExtractionStreamInterceptor())
		logger.Info("tenant extraction interceptor enabled (auth disabled)")
	}

	// 3. Recovery (last in chain to catch all panics)
	unaryInterceptors = append(unaryInterceptors, interceptors.RecoveryUnaryInterceptor(logger))
	streamInterceptors = append(streamInterceptors, interceptors.RecoveryStreamInterceptor(logger))

	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(unaryInterceptors...),
		grpc.ChainStreamInterceptor(streamInterceptors...),
	)

	// Register gRPC services
	pb.RegisterPaymentOrderServiceServer(grpcServer, paymentOrderService)
	grpc_health_v1.RegisterHealthServer(grpcServer, &simpleHealthServer{})
	reflection.Register(grpcServer)
	logger.Info("gRPC services registered")

	// Create HTTP webhook handler
	hmacSecret := []byte(getEnvOrDefault("WEBHOOK_HMAC_SECRET", ""))
	if len(hmacSecret) == 0 {
		return ErrMissingHMACSecret
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
	httpPort := getEnvAsInt("HTTP_PORT", 8080)
	httpServer, err := webhookhttp.NewServer(webhookhttp.ServerConfig{
		Port:               httpPort,
		WebhookHandler:     webhookHandler,
		Logger:             logger,
		RateLimitPerSecond: getEnvAsFloat("HTTP_RATE_LIMIT_PER_SECOND", 100),
		RateLimitBurst:     getEnvAsInt("HTTP_RATE_LIMIT_BURST", 200),
	})
	if err != nil {
		return fmt.Errorf("failed to create HTTP server: %w", err)
	}

	// Get gRPC port
	grpcPort := getEnvOrDefault("GRPC_PORT", "50054")
	grpcAddress := fmt.Sprintf(":%s", grpcPort)

	// Create gRPC listener
	grpcListener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", grpcAddress)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", grpcAddress, err)
	}

	// Channel to collect server errors
	serverErrors := make(chan error, 2)

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

	// Wait for interrupt signal or server error
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigChan:
		logger.Info("received signal", "signal", sig)
	case err := <-serverErrors:
		return err
	}

	// Graceful shutdown
	logger.Info("shutting down servers...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Shutdown HTTP server first (stop accepting new webhooks)
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

	return nil
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
// The client is wrapped with circuit breaker and retry logic using shared/pkg/clients.
func createCurrentAccountClient(namespace string, logger *slog.Logger) (service.CurrentAccountClient, func(), error) {
	target := fmt.Sprintf("dns:///current-account.%s.svc.cluster.local:50051", namespace)
	logger.Info("connecting to current-account service", "target", target)

	conn, err := grpc.NewClient(
		target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultServiceConfig(`{"loadBalancingConfig": [{"round_robin":{}}]}`),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to current-account service: %w", err)
	}

	// Configure resilience settings from environment
	resilientConfig := sharedclients.ResilientClientConfig{
		// Circuit breaker settings
		CircuitBreakerName:     "current-account",
		CircuitBreakerTimeout:  getEnvAsDuration("CURRENT_ACCOUNT_CIRCUIT_BREAKER_TIMEOUT", 30*time.Second),
		CircuitBreakerInterval: getEnvAsDuration("CURRENT_ACCOUNT_CIRCUIT_BREAKER_INTERVAL", 60*time.Second),
		MaxRequests:            getEnvAsUint32("CURRENT_ACCOUNT_CIRCUIT_BREAKER_MAX_REQUESTS", 1),
		FailureThreshold:       getEnvAsUint32("CURRENT_ACCOUNT_CIRCUIT_BREAKER_FAILURE_THRESHOLD", 5),

		// Retry settings
		MaxRetries:          getEnvAsInt("CURRENT_ACCOUNT_MAX_RETRIES", 3),
		InitialInterval:     getEnvAsDuration("CURRENT_ACCOUNT_RETRY_INITIAL_INTERVAL", 100*time.Millisecond),
		MaxInterval:         getEnvAsDuration("CURRENT_ACCOUNT_RETRY_MAX_INTERVAL", 5*time.Second),
		Multiplier:          getEnvAsFloat("CURRENT_ACCOUNT_RETRY_MULTIPLIER", 2.0),
		RandomizationFactor: getEnvAsFloat("CURRENT_ACCOUNT_RETRY_RANDOMIZATION", 0.5),

		Logger: logger,
	}

	logger.Info("current-account client configured with resilience patterns",
		"circuit_breaker_timeout", resilientConfig.CircuitBreakerTimeout,
		"circuit_breaker_failure_threshold", resilientConfig.FailureThreshold,
		"max_retries", resilientConfig.MaxRetries,
	)

	client := payclients.NewResilientCurrentAccountClient(conn, resilientConfig)

	cleanup := func() {
		if err := client.Close(); err != nil {
			logger.Error("failed to close current-account client", "error", err)
		}
	}

	return client, cleanup, nil
}

// createFinancialAccountingClient creates the FinancialAccounting gRPC client with resilience patterns.
// The client is wrapped with circuit breaker and retry logic using shared/pkg/clients.
func createFinancialAccountingClient(namespace string, logger *slog.Logger) (service.FinancialAccountingClient, func(), error) {
	target := fmt.Sprintf("dns:///financial-accounting.%s.svc.cluster.local:50051", namespace)
	logger.Info("connecting to financial-accounting service", "target", target)

	conn, err := grpc.NewClient(
		target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultServiceConfig(`{"loadBalancingConfig": [{"round_robin":{}}]}`),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to financial-accounting service: %w", err)
	}

	// Configure resilience settings from environment
	resilientConfig := sharedclients.ResilientClientConfig{
		// Circuit breaker settings
		CircuitBreakerName:     "financial-accounting",
		CircuitBreakerTimeout:  getEnvAsDuration("FINANCIAL_ACCOUNTING_CIRCUIT_BREAKER_TIMEOUT", 30*time.Second),
		CircuitBreakerInterval: getEnvAsDuration("FINANCIAL_ACCOUNTING_CIRCUIT_BREAKER_INTERVAL", 60*time.Second),
		MaxRequests:            getEnvAsUint32("FINANCIAL_ACCOUNTING_CIRCUIT_BREAKER_MAX_REQUESTS", 1),
		FailureThreshold:       getEnvAsUint32("FINANCIAL_ACCOUNTING_CIRCUIT_BREAKER_FAILURE_THRESHOLD", 5),

		// Retry settings
		MaxRetries:          getEnvAsInt("FINANCIAL_ACCOUNTING_MAX_RETRIES", 3),
		InitialInterval:     getEnvAsDuration("FINANCIAL_ACCOUNTING_RETRY_INITIAL_INTERVAL", 100*time.Millisecond),
		MaxInterval:         getEnvAsDuration("FINANCIAL_ACCOUNTING_RETRY_MAX_INTERVAL", 5*time.Second),
		Multiplier:          getEnvAsFloat("FINANCIAL_ACCOUNTING_RETRY_MULTIPLIER", 2.0),
		RandomizationFactor: getEnvAsFloat("FINANCIAL_ACCOUNTING_RETRY_RANDOMIZATION", 0.5),

		Logger: logger,
	}

	logger.Info("financial-accounting client configured with resilience patterns",
		"circuit_breaker_timeout", resilientConfig.CircuitBreakerTimeout,
		"circuit_breaker_failure_threshold", resilientConfig.FailureThreshold,
		"max_retries", resilientConfig.MaxRetries,
	)

	client := payclients.NewResilientFinancialAccountingClient(conn, resilientConfig)

	cleanup := func() {
		if err := client.Close(); err != nil {
			logger.Error("failed to close financial-accounting client", "error", err)
		}
	}

	return client, cleanup, nil
}

// createPaymentGateway creates the payment gateway client with resilience patterns.
// The gateway is wrapped with circuit breaker, rate limiting, and retry logic.
func createPaymentGateway(logger *slog.Logger) gateway.PaymentGateway {
	gatewayURL := getEnvOrDefault("PAYMENT_GATEWAY_URL", "")

	var baseGateway gateway.PaymentGateway
	if gatewayURL == "" {
		logger.Warn("PAYMENT_GATEWAY_URL not set, using mock gateway")
		baseGateway = gateway.New(gateway.Config{UseMock: true})
	} else {
		// Note: MaxRetries is 0 because the ResilientPaymentGateway wrapper handles retries.
		// Setting retries on both layers would create nested retry behavior (3x3 = 9 attempts).
		baseGateway = gateway.New(gateway.Config{
			Timeout:    30 * time.Second,
			MaxRetries: 0,
		})
	}

	// Configure resilience settings from environment
	resilientConfig := gateway.ResilientGatewayConfig{
		// Circuit breaker settings
		CircuitBreakerName:     "payment-gateway",
		CircuitBreakerTimeout:  getEnvAsDuration("GATEWAY_CIRCUIT_BREAKER_TIMEOUT", 30*time.Second),
		CircuitBreakerInterval: getEnvAsDuration("GATEWAY_CIRCUIT_BREAKER_INTERVAL", 60*time.Second),
		MaxRequests:            getEnvAsUint32("GATEWAY_CIRCUIT_BREAKER_MAX_REQUESTS", 1),
		FailureThreshold:       getEnvAsUint32("GATEWAY_CIRCUIT_BREAKER_FAILURE_THRESHOLD", 5),

		// Rate limiting settings
		RateLimit:      getEnvAsFloat("GATEWAY_RATE_LIMIT", 100.0),
		RateLimitBurst: getEnvAsInt("GATEWAY_RATE_LIMIT_BURST", 10),

		// Retry settings
		MaxRetries:          getEnvAsInt("GATEWAY_MAX_RETRIES", 3),
		InitialInterval:     getEnvAsDuration("GATEWAY_RETRY_INITIAL_INTERVAL", 100*time.Millisecond),
		MaxInterval:         getEnvAsDuration("GATEWAY_RETRY_MAX_INTERVAL", 5*time.Second),
		Multiplier:          getEnvAsFloat("GATEWAY_RETRY_MULTIPLIER", 2.0),
		RandomizationFactor: getEnvAsFloat("GATEWAY_RETRY_RANDOMIZATION", 0.5),

		Logger: logger,
	}

	logger.Info("payment gateway configured with resilience patterns",
		"circuit_breaker_timeout", resilientConfig.CircuitBreakerTimeout,
		"circuit_breaker_failure_threshold", resilientConfig.FailureThreshold,
		"rate_limit", resilientConfig.RateLimit,
		"rate_limit_burst", resilientConfig.RateLimitBurst,
		"max_retries", resilientConfig.MaxRetries,
	)

	return gateway.NewResilientPaymentGateway(baseGateway, resilientConfig)
}

// createKafkaProducer creates the Kafka producer.
func createKafkaProducer(logger *slog.Logger) (*kafka.ProtoProducer, error) {
	brokers := getEnvOrDefault("KAFKA_BROKERS", "kafka:9092")
	logger.Info("connecting to Kafka", "brokers", brokers)
	return kafka.NewProtoProducer(kafka.ProducerConfig{
		BootstrapServers: brokers,
		ClientID:         "payment-order-service",
	})
}

// createRedisClient creates and validates a Redis client connection.
// Environment variables:
//   - REDIS_URL: Redis connection URL (default: redis://localhost:6379)
//   - REDIS_PASSWORD: Redis password (optional)
//   - REDIS_DB: Redis database number (default: 0)
//   - REDIS_POOL_SIZE: Connection pool size (default: 10)
//   - REDIS_MIN_IDLE_CONNS: Minimum idle connections (default: 2)
func createRedisClient(logger *slog.Logger) (*redis.Client, error) {
	redisURL := getEnvOrDefault("REDIS_URL", "redis://localhost:6379")
	redisPassword := getEnvOrDefault("REDIS_PASSWORD", "")
	redisDB := getEnvAsInt("REDIS_DB", 0)
	poolSize := getEnvAsInt("REDIS_POOL_SIZE", 10)
	minIdleConns := getEnvAsInt("REDIS_MIN_IDLE_CONNS", 2)

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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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

// initDatabase initializes the database connection with connection pooling.
func initDatabase(logger *slog.Logger) (*gorm.DB, error) {
	dsn := getEnvOrDefault("DATABASE_URL", "postgres://meridian_payment_order_user@cockroachdb:26257/meridian_payment_order?sslmode=disable")

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		SkipDefaultTransaction: true,
		PrepareStmt:            true,
		Logger:                 nil,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get database instance: %w", err)
	}

	maxOpenConns := getEnvAsInt("DB_MAX_OPEN_CONNS", 25)
	maxIdleConns := getEnvAsInt("DB_MAX_IDLE_CONNS", 5)
	connMaxLifetime := getEnvAsDuration("DB_CONN_MAX_LIFETIME", 5*time.Minute)
	connMaxIdleTime := getEnvAsDuration("DB_CONN_MAX_IDLE_TIME", 10*time.Minute)

	sqlDB.SetMaxOpenConns(maxOpenConns)
	sqlDB.SetMaxIdleConns(maxIdleConns)
	sqlDB.SetConnMaxLifetime(connMaxLifetime)
	sqlDB.SetConnMaxIdleTime(connMaxIdleTime)

	logger.Info("database connection pool configured",
		"max_open_conns", maxOpenConns,
		"max_idle_conns", maxIdleConns)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sqlDB.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return db, nil
}

// closeDatabase closes the database connection gracefully.
func closeDatabase(db *gorm.DB, logger *slog.Logger) {
	sqlDB, err := db.DB()
	if err != nil {
		logger.Error("failed to get database instance for closing", "error", err)
		return
	}

	if err := sqlDB.Close(); err != nil {
		logger.Error("failed to close database connection", "error", err)
	} else {
		logger.Info("database connection closed")
	}
}

// Environment variable helpers

func getEnvOrDefault(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

func getEnvAsInt(key string, defaultValue int) int {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}

	value, err := strconv.Atoi(valueStr)
	if err != nil {
		return defaultValue
	}
	return value
}

func getEnvAsUint32(key string, defaultValue uint32) uint32 {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}

	value, err := strconv.ParseUint(valueStr, 10, 32)
	if err != nil {
		return defaultValue
	}
	return uint32(value)
}

func getEnvAsFloat(key string, defaultValue float64) float64 {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}

	value, err := strconv.ParseFloat(valueStr, 64)
	if err != nil {
		return defaultValue
	}
	return value
}

func getEnvAsDuration(key string, defaultValue time.Duration) time.Duration {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}

	value, err := time.ParseDuration(valueStr)
	if err != nil {
		return defaultValue
	}
	return value
}

// getEnvAsBool returns the environment variable value as bool or default
func getEnvAsBool(key string, defaultValue bool) bool {
	valueStr := strings.TrimSpace(os.Getenv(key))
	if valueStr == "" {
		return defaultValue
	}

	switch strings.ToLower(valueStr) {
	case "true", "1", "yes":
		return true
	case "false", "0", "no":
		return false
	default:
		return defaultValue
	}
}

// initAuth initializes the JWT authentication interceptor if enabled.
// Returns nil if AUTH_ENABLED is false (default), allowing unauthenticated requests.
//
// Environment variables:
//   - AUTH_ENABLED: Set to "true" to enable JWT authentication (default: false)
//   - JWKS_URL: JWKS endpoint URL for JWT validation (required when enabled)
//   - JWKS_CACHE_TTL: How long to cache JWKS keys (default: 1h)
//   - JWKS_REFRESH_TTL: Background refresh interval for JWKS (default: 30m)
//   - JWKS_HTTP_TIMEOUT: HTTP client timeout for JWKS fetch (default: 10s)
//
// Note: The system is always multi-tenant. JWT tokens MUST include tenant_id claim.
// The JWKS provider starts a background refresh goroutine. This follows the
// existing pattern in other services (e.g., position-keeping) where the provider
// is not explicitly closed during shutdown, relying on process termination.
func initAuth(ctx context.Context, logger *slog.Logger) (*auth.Interceptor, error) {
	enabled := getEnvAsBool("AUTH_ENABLED", false)
	if !enabled {
		logger.Info("auth disabled (set AUTH_ENABLED=true to enable)")
		return nil, nil //nolint:nilnil // Disabled mode intentionally returns no interceptor and no error
	}

	// Load JWKS configuration
	jwksURL := getEnvOrDefault("JWKS_URL", "http://localhost:18080/realms/meridian/protocol/openid-connect/certs")
	cacheTTL := getEnvAsDuration("JWKS_CACHE_TTL", 1*time.Hour)
	refreshTTL := getEnvAsDuration("JWKS_REFRESH_TTL", 30*time.Minute)

	// Create JWKS provider with HTTP client
	httpTimeout := getEnvAsDuration("JWKS_HTTP_TIMEOUT", 10*time.Second)
	httpClient := &http.Client{
		Timeout: httpTimeout,
	}
	jwksConfig := &auth.JWKSProviderConfig{
		URL:        jwksURL,
		Client:     httpClient,
		CacheTTL:   cacheTTL,
		RefreshTTL: refreshTTL,
	}

	provider, err := auth.NewJWKSProvider(ctx, jwksConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create JWKS provider: %w", err)
	}

	// Create JWT validator
	validator, err := auth.NewJWTValidatorWithJWKS(provider)
	if err != nil {
		return nil, fmt.Errorf("failed to create JWT validator: %w", err)
	}

	// Create interceptor with bypass methods for health checks and reflection
	interceptorConfig := &auth.InterceptorConfig{
		JWKSValidator: validator,
		BypassMethods: []string{
			"/grpc.health.v1.Health/Check",
			"/grpc.health.v1.Health/Watch",
			"/grpc.reflection.v1alpha.ServerReflection/ServerReflectionInfo",
			"/grpc.reflection.v1.ServerReflection/ServerReflectionInfo",
		},
	}

	interceptor, err := auth.NewAuthInterceptor(interceptorConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create auth interceptor: %w", err)
	}

	logger.Debug("auth interceptor initialized",
		"jwks_url", jwksURL,
		"cache_ttl", cacheTTL,
		"refresh_ttl", refreshTTL,
		"http_timeout", httpTimeout,
		"bypass_methods", len(interceptorConfig.BypassMethods))

	return interceptor, nil
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
