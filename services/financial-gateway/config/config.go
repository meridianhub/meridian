// Package config provides configuration loading for the financial-gateway service.
package config

import (
	"strconv"
	"time"

	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/ports"
)

// Config holds the financial-gateway service configuration.
type Config struct {
	// GRPCPort is the gRPC listen port.
	GRPCPort string

	// DatabaseURL is the CockroachDB/PostgreSQL connection string.
	DatabaseURL string

	// LogLevel controls the log verbosity (debug, info, warn, error).
	LogLevel string

	// StripeSecretKey is the Stripe API secret key for payment dispatch.
	StripeSecretKey string

	// CircuitBreaker configures per-connection circuit breaker behavior.
	CircuitBreaker CircuitBreakerConfig

	// RateLimit configures the inbound request rate limiter.
	RateLimit RateLimitConfig
}

// CircuitBreakerConfig configures the circuit breaker for external provider calls.
type CircuitBreakerConfig struct {
	// Timeout is the duration the circuit stays open before transitioning to half-open.
	Timeout time.Duration

	// FailureThreshold is the number of consecutive failures required to trip the circuit.
	FailureThreshold int
}

// RateLimitConfig configures inbound request rate limiting.
type RateLimitConfig struct {
	// RPS is the maximum sustained request rate (requests per second).
	RPS float64

	// Burst is the maximum burst size above the sustained rate.
	Burst int
}

// LoadConfig loads configuration from environment variables with sensible defaults.
func LoadConfig() Config {
	return Config{
		GRPCPort:        env.GetEnvOrDefault("GRPC_PORT", strconv.Itoa(ports.FinancialGateway)),
		DatabaseURL:     env.GetEnvOrDefault("DATABASE_URL", ""),
		LogLevel:        env.GetEnvOrDefault("LOG_LEVEL", "info"),
		StripeSecretKey: env.GetEnvOrDefault("STRIPE_SECRET_KEY", ""),
		CircuitBreaker: CircuitBreakerConfig{
			Timeout:          env.GetEnvAsDuration("CIRCUIT_BREAKER_TIMEOUT", 30*time.Second),
			FailureThreshold: env.GetEnvAsInt("CIRCUIT_BREAKER_FAILURES", 5),
		},
		RateLimit: RateLimitConfig{
			RPS:   env.GetEnvAsFloat("RATE_LIMIT_RPS", 100),
			Burst: env.GetEnvAsInt("RATE_LIMIT_BURST", 10),
		},
	}
}
