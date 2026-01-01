package defaults

import (
	"log/slog"
	"os"
	"time"
)

// Environment variable names for timeout configuration.
// These can be set at runtime to override the default timeout constants.
const (
	EnvRPCTimeout            = "TIMEOUT_RPC"
	EnvHealthCheckTimeout    = "TIMEOUT_HEALTH_CHECK"
	EnvCircuitBreakerTimeout = "TIMEOUT_CIRCUIT_BREAKER"
	EnvGracefulShutdown      = "TIMEOUT_GRACEFUL_SHUTDOWN"
	EnvContextTimeout        = "TIMEOUT_CONTEXT"
	EnvRetryDelay            = "TIMEOUT_RETRY_DELAY"
	EnvMaxRetryInterval      = "TIMEOUT_MAX_RETRY_INTERVAL"
	EnvHTTPReadHeaderTimeout = "TIMEOUT_HTTP_READ_HEADER"
	EnvHTTPReadTimeout       = "TIMEOUT_HTTP_READ"
	EnvHTTPWriteTimeout      = "TIMEOUT_HTTP_WRITE"
	EnvHTTPIdleTimeout       = "TIMEOUT_HTTP_IDLE"
)

// Validation ranges for timeout values.
// These prevent misconfiguration that could cause operational issues.
var (
	// minStandardTimeout is the minimum for most timeout values (1 second).
	minStandardTimeout = 1 * time.Second

	// maxStandardTimeout is the maximum for most timeout values (5 minutes).
	maxStandardTimeout = 5 * time.Minute

	// minRetryDelay is the minimum for retry delays (10 milliseconds).
	minRetryDelay = 10 * time.Millisecond

	// maxRetryDelay is the maximum for retry delays (1 minute).
	maxRetryDelay = 1 * time.Minute
)

// GetRPCTimeout returns the RPC timeout, checking the TIMEOUT_RPC environment
// variable first. If the environment variable is not set or contains an invalid
// value, it returns DefaultRPCTimeout.
//
// Valid range: 1s to 5m
func GetRPCTimeout() time.Duration {
	return getTimeout(EnvRPCTimeout, DefaultRPCTimeout, minStandardTimeout, maxStandardTimeout)
}

// GetHealthCheckTimeout returns the health check timeout, checking the
// TIMEOUT_HEALTH_CHECK environment variable first. If the environment variable
// is not set or contains an invalid value, it returns DefaultHealthCheckTimeout.
//
// Valid range: 1s to 5m
func GetHealthCheckTimeout() time.Duration {
	return getTimeout(EnvHealthCheckTimeout, DefaultHealthCheckTimeout, minStandardTimeout, maxStandardTimeout)
}

// GetCircuitBreakerTimeout returns the circuit breaker timeout, checking the
// TIMEOUT_CIRCUIT_BREAKER environment variable first. If the environment variable
// is not set or contains an invalid value, it returns DefaultCircuitBreakerTimeout.
//
// Valid range: 1s to 5m
func GetCircuitBreakerTimeout() time.Duration {
	return getTimeout(EnvCircuitBreakerTimeout, DefaultCircuitBreakerTimeout, minStandardTimeout, maxStandardTimeout)
}

// GetGracefulShutdown returns the graceful shutdown timeout, checking the
// TIMEOUT_GRACEFUL_SHUTDOWN environment variable first. If the environment
// variable is not set or contains an invalid value, it returns DefaultGracefulShutdown.
//
// Valid range: 1s to 5m
//
// Note: If you override this, also update terminationGracePeriodSeconds in your
// Kubernetes deployment spec to match.
func GetGracefulShutdown() time.Duration {
	return getTimeout(EnvGracefulShutdown, DefaultGracefulShutdown, minStandardTimeout, maxStandardTimeout)
}

// GetContextTimeout returns the generic context timeout, checking the
// TIMEOUT_CONTEXT environment variable first. If the environment variable
// is not set or contains an invalid value, it returns DefaultContextTimeout.
//
// Valid range: 1s to 5m
func GetContextTimeout() time.Duration {
	return getTimeout(EnvContextTimeout, DefaultContextTimeout, minStandardTimeout, maxStandardTimeout)
}

// GetRetryDelay returns the initial retry delay, checking the TIMEOUT_RETRY_DELAY
// environment variable first. If the environment variable is not set or contains
// an invalid value, it returns DefaultRetryDelay.
//
// Valid range: 10ms to 1m
func GetRetryDelay() time.Duration {
	return getTimeout(EnvRetryDelay, DefaultRetryDelay, minRetryDelay, maxRetryDelay)
}

// GetMaxRetryInterval returns the maximum retry interval, checking the
// TIMEOUT_MAX_RETRY_INTERVAL environment variable first. If the environment
// variable is not set or contains an invalid value, it returns DefaultMaxRetryInterval.
//
// Valid range: 10ms to 1m
func GetMaxRetryInterval() time.Duration {
	return getTimeout(EnvMaxRetryInterval, DefaultMaxRetryInterval, minRetryDelay, maxRetryDelay)
}

// GetHTTPReadHeaderTimeout returns the HTTP read header timeout, checking the
// TIMEOUT_HTTP_READ_HEADER environment variable first. If the environment variable
// is not set or contains an invalid value, it returns DefaultHTTPReadHeaderTimeout.
//
// Valid range: 1s to 5m
func GetHTTPReadHeaderTimeout() time.Duration {
	return getTimeout(EnvHTTPReadHeaderTimeout, DefaultHTTPReadHeaderTimeout, minStandardTimeout, maxStandardTimeout)
}

// GetHTTPReadTimeout returns the HTTP read timeout, checking the TIMEOUT_HTTP_READ
// environment variable first. If the environment variable is not set or contains
// an invalid value, it returns DefaultHTTPReadTimeout.
//
// Valid range: 1s to 5m
func GetHTTPReadTimeout() time.Duration {
	return getTimeout(EnvHTTPReadTimeout, DefaultHTTPReadTimeout, minStandardTimeout, maxStandardTimeout)
}

// GetHTTPWriteTimeout returns the HTTP write timeout, checking the TIMEOUT_HTTP_WRITE
// environment variable first. If the environment variable is not set or contains
// an invalid value, it returns DefaultHTTPWriteTimeout.
//
// Valid range: 1s to 5m
func GetHTTPWriteTimeout() time.Duration {
	return getTimeout(EnvHTTPWriteTimeout, DefaultHTTPWriteTimeout, minStandardTimeout, maxStandardTimeout)
}

// GetHTTPIdleTimeout returns the HTTP idle timeout, checking the TIMEOUT_HTTP_IDLE
// environment variable first. If the environment variable is not set or contains
// an invalid value, it returns DefaultHTTPIdleTimeout.
//
// Valid range: 1s to 5m
func GetHTTPIdleTimeout() time.Duration {
	return getTimeout(EnvHTTPIdleTimeout, DefaultHTTPIdleTimeout, minStandardTimeout, maxStandardTimeout)
}

// getTimeout is the internal implementation for retrieving timeout values with
// environment variable override capability and validation.
func getTimeout(envVar string, defaultVal, minVal, maxVal time.Duration) time.Duration {
	envValue := os.Getenv(envVar)
	if envValue == "" {
		return defaultVal
	}

	parsed, err := time.ParseDuration(envValue)
	if err != nil {
		slog.Warn("invalid timeout duration in environment variable, using default",
			"env_var", envVar,
			"value", envValue,
			"error", err,
			"default", defaultVal,
		)
		return defaultVal
	}

	if parsed < minVal || parsed > maxVal {
		slog.Warn("timeout duration out of valid range, using default",
			"env_var", envVar,
			"value", parsed,
			"min", minVal,
			"max", maxVal,
			"default", defaultVal,
		)
		return defaultVal
	}

	return parsed
}
