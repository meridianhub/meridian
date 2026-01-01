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

	pb "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	"github.com/meridianhub/meridian/services/payment-order/adapters/gateway"
	webhookhttp "github.com/meridianhub/meridian/services/payment-order/adapters/http"
	"github.com/meridianhub/meridian/services/payment-order/adapters/persistence"
	payclients "github.com/meridianhub/meridian/services/payment-order/clients"
	"github.com/meridianhub/meridian/services/payment-order/config"
	"github.com/meridianhub/meridian/services/payment-order/service"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/kafka"
	"github.com/meridianhub/meridian/shared/platform/ports"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
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

	logger.Info("database connection established")

	// Create repository
	repo := persistence.NewPaymentOrderRepository(db)

	// Get Kubernetes namespace from environment
	namespace := env.GetEnvOrDefault("K8S_NAMESPACE", "default")

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
	sigChan, signalCleanup := bootstrap.SignalHandler()
	defer signalCleanup()

	select {
	case sig := <-sigChan:
		logger.Info("received signal", "signal", sig)
	case err := <-serverErrors:
		return err
	}

	// Graceful shutdown
	logger.Info("shutting down servers...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), defaults.DefaultGracefulShutdown)
	defer cancel()

	// Close database connection during shutdown
	defer bootstrap.CloseDatabase(db, logger)

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
	target := fmt.Sprintf("dns:///current-account.%s.svc.cluster.local:%d", namespace, ports.CurrentAccount)
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
	target := fmt.Sprintf("dns:///financial-accounting.%s.svc.cluster.local:%d", namespace, ports.FinancialAccounting)
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
	gatewayURL := env.GetEnvOrDefault("PAYMENT_GATEWAY_URL", "")

	var baseGateway gateway.PaymentGateway
	if gatewayURL == "" {
		logger.Warn("PAYMENT_GATEWAY_URL not set, using mock gateway")
		baseGateway = gateway.New(gateway.Config{UseMock: true})
	} else {
		// Note: MaxRetries is 0 because the ResilientPaymentGateway wrapper handles retries.
		// Setting retries on both layers would create nested retry behavior (3x3 = 9 attempts).
		baseGateway = gateway.New(gateway.Config{
			Timeout:    defaults.DefaultRPCTimeout,
			MaxRetries: 0,
		})
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

	return gateway.NewResilientPaymentGateway(baseGateway, resilientConfig)
}

// createKafkaProducer creates the Kafka producer.
func createKafkaProducer(logger *slog.Logger) (*kafka.ProtoProducer, error) {
	brokers := env.GetEnvOrDefault("KAFKA_BROKERS", "kafka:9092")
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
