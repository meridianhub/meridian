package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/redis/go-redis/v9"
	stripego "github.com/stripe/stripe-go/v82"

	"github.com/meridianhub/meridian/services/payment-order/adapters/gateway"
	stripegateway "github.com/meridianhub/meridian/services/payment-order/adapters/gateway/stripe"
	"github.com/meridianhub/meridian/services/payment-order/config"
	"github.com/meridianhub/meridian/services/payment-order/service"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/ports"

	currentaccountclient "github.com/meridianhub/meridian/services/current-account/client"
	financialaccountingclient "github.com/meridianhub/meridian/services/financial-accounting/client"
	financialgatewayclient "github.com/meridianhub/meridian/services/financial-gateway/client"
	internalaccountclient "github.com/meridianhub/meridian/services/internal-account/client"
)

// createCurrentAccountClient creates the CurrentAccount gRPC client with resilience patterns.
func (c *Container) createCurrentAccountClient(namespace string) (service.CurrentAccountClient, func(), error) {
	c.Logger.Info("connecting to current-account service",
		"service", currentaccountclient.ServiceName,
		"namespace", namespace,
		"port", ports.CurrentAccount)

	resilientConfig := &sharedclients.ResilientClientConfig{
		CircuitBreakerName:     "current-account",
		CircuitBreakerTimeout:  env.GetEnvAsDuration("CURRENT_ACCOUNT_CIRCUIT_BREAKER_TIMEOUT", defaults.DefaultCircuitBreakerOpenTimeout),
		CircuitBreakerInterval: env.GetEnvAsDuration("CURRENT_ACCOUNT_CIRCUIT_BREAKER_INTERVAL", defaults.DefaultCircuitBreakerInterval),
		MaxRequests:            env.GetEnvAsUint32("CURRENT_ACCOUNT_CIRCUIT_BREAKER_MAX_REQUESTS", 1),
		FailureThreshold:       env.GetEnvAsUint32("CURRENT_ACCOUNT_CIRCUIT_BREAKER_FAILURE_THRESHOLD", 5),
		MaxRetries:             env.GetEnvAsInt("CURRENT_ACCOUNT_MAX_RETRIES", 3),
		InitialInterval:        env.GetEnvAsDuration("CURRENT_ACCOUNT_RETRY_INITIAL_INTERVAL", defaults.DefaultRetryDelay),
		MaxInterval:            env.GetEnvAsDuration("CURRENT_ACCOUNT_RETRY_MAX_INTERVAL", defaults.DefaultMaxRetryInterval),
		Multiplier:             env.GetEnvAsFloat("CURRENT_ACCOUNT_RETRY_MULTIPLIER", 2.0),
		RandomizationFactor:    env.GetEnvAsFloat("CURRENT_ACCOUNT_RETRY_RANDOMIZATION", 0.5),
		Logger:                 c.Logger,
	}

	c.Logger.Info("current-account client configured with resilience patterns",
		"circuit_breaker_timeout", resilientConfig.CircuitBreakerTimeout,
		"circuit_breaker_failure_threshold", resilientConfig.FailureThreshold,
		"max_retries", resilientConfig.MaxRetries,
	)

	client, cleanup, err := currentaccountclient.New(currentaccountclient.Config{
		ServiceName: currentaccountclient.ServiceName,
		Namespace:   namespace,
		Port:        ports.CurrentAccount,
		Timeout:     env.GetEnvAsDuration("CURRENT_ACCOUNT_TIMEOUT", currentaccountclient.DefaultTimeout),
		Tracer:      c.Tracer,
		Resilience:  resilientConfig,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create current-account client: %w", err)
	}

	return client, cleanup, nil
}

// createFinancialAccountingClient creates the FinancialAccounting gRPC client with resilience patterns.
func (c *Container) createFinancialAccountingClient(ctx context.Context, namespace string) (service.FinancialAccountingClient, func(), error) {
	c.Logger.Info("connecting to financial-accounting service",
		"service", financialaccountingclient.ServiceName,
		"namespace", namespace,
		"port", ports.FinancialAccounting)

	resilientConfig := &sharedclients.ResilientClientConfig{
		CircuitBreakerName:     "financial-accounting",
		CircuitBreakerTimeout:  env.GetEnvAsDuration("FINANCIAL_ACCOUNTING_CIRCUIT_BREAKER_TIMEOUT", defaults.DefaultCircuitBreakerOpenTimeout),
		CircuitBreakerInterval: env.GetEnvAsDuration("FINANCIAL_ACCOUNTING_CIRCUIT_BREAKER_INTERVAL", defaults.DefaultCircuitBreakerInterval),
		MaxRequests:            env.GetEnvAsUint32("FINANCIAL_ACCOUNTING_CIRCUIT_BREAKER_MAX_REQUESTS", 1),
		FailureThreshold:       env.GetEnvAsUint32("FINANCIAL_ACCOUNTING_CIRCUIT_BREAKER_FAILURE_THRESHOLD", 5),
		MaxRetries:             env.GetEnvAsInt("FINANCIAL_ACCOUNTING_MAX_RETRIES", 3),
		InitialInterval:        env.GetEnvAsDuration("FINANCIAL_ACCOUNTING_RETRY_INITIAL_INTERVAL", defaults.DefaultRetryDelay),
		MaxInterval:            env.GetEnvAsDuration("FINANCIAL_ACCOUNTING_RETRY_MAX_INTERVAL", defaults.DefaultMaxRetryInterval),
		Multiplier:             env.GetEnvAsFloat("FINANCIAL_ACCOUNTING_RETRY_MULTIPLIER", 2.0),
		RandomizationFactor:    env.GetEnvAsFloat("FINANCIAL_ACCOUNTING_RETRY_RANDOMIZATION", 0.5),
		Logger:                 c.Logger,
	}

	c.Logger.Info("financial-accounting client configured with resilience patterns",
		"circuit_breaker_timeout", resilientConfig.CircuitBreakerTimeout,
		"circuit_breaker_failure_threshold", resilientConfig.FailureThreshold,
		"max_retries", resilientConfig.MaxRetries,
	)

	client, cleanup, err := financialaccountingclient.New(ctx, financialaccountingclient.Config{
		ServiceName: financialaccountingclient.ServiceName,
		Namespace:   namespace,
		Port:        ports.FinancialAccounting,
		Timeout:     env.GetEnvAsDuration("FINANCIAL_ACCOUNTING_TIMEOUT", financialaccountingclient.DefaultTimeout),
		Tracer:      c.Tracer,
		Resilience:  resilientConfig,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create financial-accounting client: %w", err)
	}

	return client, cleanup, nil
}

// createInternalAccountClient creates the InternalAccount gRPC client with resilience patterns.
func (c *Container) createInternalAccountClient(namespace string) (service.InternalAccountClient, func(), error) {
	c.Logger.Info("connecting to internal-account service",
		"service", internalaccountclient.ServiceName,
		"namespace", namespace,
		"port", ports.InternalAccount)

	resilientConfig := &sharedclients.ResilientClientConfig{
		CircuitBreakerName:     "internal-account",
		CircuitBreakerTimeout:  env.GetEnvAsDuration("INTERNAL_ACCOUNT_CIRCUIT_BREAKER_TIMEOUT", defaults.DefaultCircuitBreakerOpenTimeout),
		CircuitBreakerInterval: env.GetEnvAsDuration("INTERNAL_ACCOUNT_CIRCUIT_BREAKER_INTERVAL", defaults.DefaultCircuitBreakerInterval),
		MaxRequests:            env.GetEnvAsUint32("INTERNAL_ACCOUNT_CIRCUIT_BREAKER_MAX_REQUESTS", 1),
		FailureThreshold:       env.GetEnvAsUint32("INTERNAL_ACCOUNT_CIRCUIT_BREAKER_FAILURE_THRESHOLD", 5),
		MaxRetries:             env.GetEnvAsInt("INTERNAL_ACCOUNT_MAX_RETRIES", 3),
		InitialInterval:        env.GetEnvAsDuration("INTERNAL_ACCOUNT_RETRY_INITIAL_INTERVAL", defaults.DefaultRetryDelay),
		MaxInterval:            env.GetEnvAsDuration("INTERNAL_ACCOUNT_RETRY_MAX_INTERVAL", defaults.DefaultMaxRetryInterval),
		Multiplier:             env.GetEnvAsFloat("INTERNAL_ACCOUNT_RETRY_MULTIPLIER", 2.0),
		RandomizationFactor:    env.GetEnvAsFloat("INTERNAL_ACCOUNT_RETRY_RANDOMIZATION", 0.5),
		Logger:                 c.Logger,
	}

	c.Logger.Info("internal-account client configured with resilience patterns",
		"circuit_breaker_timeout", resilientConfig.CircuitBreakerTimeout,
		"circuit_breaker_failure_threshold", resilientConfig.FailureThreshold,
		"max_retries", resilientConfig.MaxRetries,
	)

	client, cleanup, err := internalaccountclient.New(internalaccountclient.Config{
		ServiceName: internalaccountclient.ServiceName,
		Namespace:   namespace,
		Port:        ports.InternalAccount,
		Timeout:     env.GetEnvAsDuration("INTERNAL_ACCOUNT_TIMEOUT", internalaccountclient.DefaultTimeout),
		Tracer:      c.Tracer,
		Resilience:  resilientConfig,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create internal-account client: %w", err)
	}

	return client, cleanup, nil
}

// createPaymentGateway creates the payment gateway client with resilience patterns.
// Provider selection and API key validation are handled by ServiceConfig.Validate,
// so this function assumes the config is already validated.
func createPaymentGateway(svcConfig config.ServiceConfig, logger *slog.Logger) (gateway.PaymentGateway, func(), error) {
	baseGateway, cleanup, err := createBaseGateway(svcConfig, logger)
	if err != nil {
		return nil, nil, err
	}

	resilientConfig := buildGatewayResilienceConfig(logger)

	logger.Info("payment gateway configured with resilience patterns",
		"circuit_breaker_timeout", resilientConfig.CircuitBreakerTimeout,
		"circuit_breaker_failure_threshold", resilientConfig.FailureThreshold,
		"rate_limit", resilientConfig.RateLimit,
		"rate_limit_burst", resilientConfig.RateLimitBurst,
		"max_retries", resilientConfig.MaxRetries,
	)

	return gateway.NewResilientPaymentGateway(baseGateway, resilientConfig), cleanup, nil
}

// createBaseGateway creates the provider-specific base gateway implementation.
func createBaseGateway(svcConfig config.ServiceConfig, logger *slog.Logger) (gateway.PaymentGateway, func(), error) {
	cleanup := func() {}

	switch svcConfig.PaymentGatewayProvider {
	case gateway.ProviderStripe:
		client := stripego.NewClient(svcConfig.StripeAPIKey)
		gw := stripegateway.NewGatewayAdapter(
			client.V1PaymentIntents,
			stripegateway.GatewayAdapterConfig{},
			logger,
		)
		logger.Info("using stripe payment gateway")
		return gw, cleanup, nil

	case gateway.ProviderFinancialGateway:
		fgClient, fgCleanup, err := financialgatewayclient.New(financialgatewayclient.Config{
			Target: svcConfig.FinancialGatewayAddr,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create financial-gateway gRPC client: %w", err)
		}
		gw := gateway.NewFinancialGatewayClient(fgClient, logger)
		logger.Info("using financial-gateway payment gateway", "addr", svcConfig.FinancialGatewayAddr)
		return gw, fgCleanup, nil

	case gateway.ProviderMock:
		logger.Warn("using mock payment gateway")
		gw := gateway.New(gateway.Config{
			UseMock: true,
			MockConfig: gateway.MockGatewayConfig{
				DeterministicFailures: true,
			},
		})
		return gw, cleanup, nil

	default:
		return nil, nil, fmt.Errorf("%w: %q (valid: %q, %q, %q)", config.ErrInvalidGatewayProvider, svcConfig.PaymentGatewayProvider, gateway.ProviderStripe, gateway.ProviderFinancialGateway, gateway.ProviderMock)
	}
}

// buildGatewayResilienceConfig creates the resilience configuration from environment variables.
func buildGatewayResilienceConfig(logger *slog.Logger) gateway.ResilientGatewayConfig {
	return gateway.ResilientGatewayConfig{
		CircuitBreakerName:     "payment-gateway",
		CircuitBreakerTimeout:  env.GetEnvAsDuration("GATEWAY_CIRCUIT_BREAKER_TIMEOUT", defaults.DefaultCircuitBreakerOpenTimeout),
		CircuitBreakerInterval: env.GetEnvAsDuration("GATEWAY_CIRCUIT_BREAKER_INTERVAL", defaults.DefaultCircuitBreakerInterval),
		MaxRequests:            env.GetEnvAsUint32("GATEWAY_CIRCUIT_BREAKER_MAX_REQUESTS", 1),
		FailureThreshold:       env.GetEnvAsUint32("GATEWAY_CIRCUIT_BREAKER_FAILURE_THRESHOLD", 5),
		RateLimit:              env.GetEnvAsFloat("GATEWAY_RATE_LIMIT", 100.0),
		RateLimitBurst:         env.GetEnvAsInt("GATEWAY_RATE_LIMIT_BURST", 10),
		MaxRetries:             env.GetEnvAsInt("GATEWAY_MAX_RETRIES", 3),
		InitialInterval:        env.GetEnvAsDuration("GATEWAY_RETRY_INITIAL_INTERVAL", defaults.DefaultRetryDelay),
		MaxInterval:            env.GetEnvAsDuration("GATEWAY_RETRY_MAX_INTERVAL", defaults.DefaultMaxRetryInterval),
		Multiplier:             env.GetEnvAsFloat("GATEWAY_RETRY_MULTIPLIER", 2.0),
		RandomizationFactor:    env.GetEnvAsFloat("GATEWAY_RETRY_RANDOMIZATION", 0.5),
		Logger:                 logger,
	}
}

// createRedisClient creates and validates a Redis client connection.
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

	if redisPassword != "" {
		opt.Password = redisPassword
	}
	opt.DB = redisDB
	opt.PoolSize = poolSize
	opt.MinIdleConns = minIdleConns

	client := redis.NewClient(opt)

	ctx, cancel := context.WithTimeout(context.Background(), defaults.DefaultHealthCheckTimeout)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("failed to ping Redis: %w", err)
	}

	logger.Info("Redis client connected",
		"addr", opt.Addr,
		"db", redisDB,
		"pool_size", poolSize,
		"min_idle_conns", minIdleConns)

	return client, nil
}

// createGatewayAccountConfig loads the gateway-to-account mapping configuration.
func createGatewayAccountConfig(logger *slog.Logger) (*config.GatewayAccountConfig, error) {
	cfg, err := config.LoadGatewayAccountConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load gateway account config: %w", err)
	}

	logger.Info("gateway account configuration loaded",
		"gateway_count", len(cfg.Mappings))

	return cfg, nil
}
