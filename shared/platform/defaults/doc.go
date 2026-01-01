// Package defaults provides centralized timeout and timing constants for the
// Meridian platform. These defaults are used across services to ensure consistent
// behavior for common operations like RPC calls, health checks, and graceful shutdown.
//
// # Design Philosophy
//
// Timeout constants are centralized here rather than defined inline for several reasons:
//   - Consistency: All services use the same values by default
//   - Visibility: Easy to audit and adjust platform-wide timing behavior
//   - Tunability: Services can override when needed (e.g., long-running operations)
//   - Testing: Tests can reason about expected timeout behavior
//
// # When to Override
//
// These are sensible defaults, not mandates. Override when:
//   - Your operation genuinely needs more time (batch processing, large data transfer)
//   - Your operation should fail faster (user-facing latency-sensitive paths)
//   - External SLAs require specific timeout values
//   - Testing requires deterministic timing
//
// # Example Usage
//
//	import "github.com/meridianhub/meridian/shared/platform/defaults"
//
//	// Use defaults directly
//	ctx, cancel := context.WithTimeout(ctx, defaults.DefaultRPCTimeout)
//	defer cancel()
//
//	// Override for specific needs
//	longRunningCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
//	defer cancel()
//
//	// Use Get* functions for environment-configurable timeouts
//	ctx, cancel := context.WithTimeout(ctx, defaults.GetRPCTimeout())
//	defer cancel()
//
// # Categories
//
// The constants are organized into categories:
//   - RPC timeouts: For inter-service communication
//   - Health check timeouts: For probe responsiveness
//   - Circuit breaker timeouts: For failure recovery
//   - Lifecycle timeouts: For graceful shutdown and startup
//   - HTTP server timeouts: For request handling and connection management
//   - Retry timings: For backoff and retry logic
//
// # Environment Variable Overrides
//
// Each timeout has a corresponding Get* function that checks for an environment
// variable override before returning the default value. This allows runtime
// configuration without code changes.
//
// Supported environment variables:
//
//	TIMEOUT_RPC                - Override DefaultRPCTimeout (valid: 1s-5m)
//	TIMEOUT_HEALTH_CHECK       - Override DefaultHealthCheckTimeout (valid: 1s-5m)
//	TIMEOUT_CIRCUIT_BREAKER    - Override DefaultCircuitBreakerTimeout (valid: 1s-5m)
//	TIMEOUT_GRACEFUL_SHUTDOWN  - Override DefaultGracefulShutdown (valid: 1s-5m)
//	TIMEOUT_CONTEXT            - Override DefaultContextTimeout (valid: 1s-5m)
//	TIMEOUT_RETRY_DELAY        - Override DefaultRetryDelay (valid: 10ms-1m)
//	TIMEOUT_MAX_RETRY_INTERVAL - Override DefaultMaxRetryInterval (valid: 10ms-1m)
//	TIMEOUT_HTTP_READ_HEADER   - Override DefaultHTTPReadHeaderTimeout (valid: 1s-5m)
//	TIMEOUT_HTTP_READ          - Override DefaultHTTPReadTimeout (valid: 1s-5m)
//	TIMEOUT_HTTP_WRITE         - Override DefaultHTTPWriteTimeout (valid: 1s-5m)
//	TIMEOUT_HTTP_IDLE          - Override DefaultHTTPIdleTimeout (valid: 1s-5m)
//
// Values must be valid Go duration strings (e.g., "30s", "2m", "500ms").
// Invalid or out-of-range values are logged and the default is used instead.
//
// # Example Environment Override
//
//	# In Kubernetes deployment or docker-compose
//	env:
//	  - name: TIMEOUT_RPC
//	    value: "60s"
//	  - name: TIMEOUT_GRACEFUL_SHUTDOWN
//	    value: "45s"
//
//	# In shell
//	export TIMEOUT_RPC=60s
//	export TIMEOUT_GRACEFUL_SHUTDOWN=45s
package defaults
