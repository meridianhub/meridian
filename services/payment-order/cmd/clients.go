package main

import (
	"context"
	"fmt"
	"log/slog"

	stripego "github.com/stripe/stripe-go/v82"

	"github.com/meridianhub/meridian/services/payment-order/adapters/gateway"
	stripegateway "github.com/meridianhub/meridian/services/payment-order/adapters/gateway/stripe"
	"github.com/meridianhub/meridian/services/payment-order/config"
	"github.com/meridianhub/meridian/services/payment-order/service"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/meridianhub/meridian/shared/platform/ports"
	"github.com/redis/go-redis/v9"

	// Service-owned clients (standardized client packages from each service)
	currentaccountclient "github.com/meridianhub/meridian/services/current-account/client"
	financialaccountingclient "github.com/meridianhub/meridian/services/financial-accounting/client"
	financialgatewayclient "github.com/meridianhub/meridian/services/financial-gateway/client"
	internalaccountclient "github.com/meridianhub/meridian/services/internal-account/client"
)

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
func createFinancialAccountingClient(ctx context.Context, namespace string, logger *slog.Logger, tracer *observability.Tracer) (service.FinancialAccountingClient, func(), error) {
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
	client, cleanup, err := financialaccountingclient.New(ctx, financialaccountingclient.Config{
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
