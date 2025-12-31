package defaults

import "time"

// RPC and Context Timeouts
//
// These timeouts control inter-service communication and context deadlines.
// They are designed to fail fast enough to prevent cascading failures while
// allowing sufficient time for normal operations.
const (
	// DefaultRPCTimeout is the timeout for gRPC and inter-service calls.
	//
	// This value (30s) balances responsiveness with reliability:
	//   - Long enough for most database queries and processing
	//   - Short enough to fail fast during service degradation
	//   - Matches typical load balancer and ingress timeout defaults
	//
	// Override for:
	//   - Batch operations: Use 2-5 minutes for bulk imports/exports
	//   - Real-time user requests: Use 5-10 seconds for snappy UI
	//   - Cross-region calls: Consider network latency overhead
	DefaultRPCTimeout = 30 * time.Second

	// DefaultContextTimeout is the generic context timeout for operations
	// that don't have a more specific timeout constant.
	//
	// This should be used as a fallback when no specific timeout category applies.
	// Prefer using the more specific constants (DefaultRPCTimeout, etc.) when
	// the operation type is clear.
	//
	// Override for:
	//   - Long-running computations: Increase as needed
	//   - Quick local operations: Decrease for faster failure detection
	DefaultContextTimeout = 30 * time.Second
)

// Health Check Timeouts
//
// Health checks need to respond quickly to avoid false positives during
// liveness and readiness probes. Kubernetes and load balancers typically
// expect sub-second to few-second response times.
const (
	// DefaultHealthCheckTimeout is the timeout for health check probe responses.
	//
	// This value (5s) is intentionally short:
	//   - Kubernetes default livenessProbe timeout is 1s (configurable)
	//   - AWS ALB health check timeout default is 5s
	//   - Long health checks can cause unnecessary pod restarts
	//
	// Override for:
	//   - Deep health checks: Increase if checking external dependencies
	//   - Startup probes: Use longer timeouts during initialization
	//   - Never: For basic liveness checks (keep them fast)
	DefaultHealthCheckTimeout = 5 * time.Second
)

// Circuit Breaker Timeouts
//
// Circuit breakers prevent cascading failures by temporarily stopping calls
// to failing services. These timeouts control circuit breaker behavior for
// state transitions and counting intervals.
const (
	// DefaultCircuitBreakerOpenTimeout is how long a circuit breaker stays open
	// before transitioning to half-open state to attempt recovery.
	//
	// This value (30s) provides a balance:
	//   - Long enough for transient failures to resolve (network blips, restarts)
	//   - Short enough to attempt recovery reasonably quickly
	//   - Shorter than the interval to allow faster recovery detection
	//
	// Override for:
	//   - External services with slow recovery: Increase to 60-120 seconds
	//   - Internal services with fast failover: Decrease to 15 seconds
	//   - Critical paths: Consider shorter timeouts with more retry attempts
	DefaultCircuitBreakerOpenTimeout = 30 * time.Second

	// DefaultCircuitBreakerInterval is the cyclic period for clearing failure counts
	// when the circuit breaker is in closed state.
	//
	// This value (60s) provides a balance:
	//   - Long enough to accumulate meaningful failure statistics
	//   - Short enough to recover from temporary error spikes
	//   - Matches common service restart and scaling times
	//
	// Override for:
	//   - High-throughput services: Decrease to 30s for faster response
	//   - Low-traffic services: Increase to 120s to accumulate enough data
	DefaultCircuitBreakerInterval = 60 * time.Second

	// DefaultCircuitBreakerTimeout is an alias for DefaultCircuitBreakerInterval
	// for backward compatibility.
	//
	// Deprecated: Use DefaultCircuitBreakerInterval for count clearing intervals,
	// or DefaultCircuitBreakerOpenTimeout for open-to-half-open transitions.
	DefaultCircuitBreakerTimeout = DefaultCircuitBreakerInterval
)

// Lifecycle Timeouts
//
// These timeouts control service startup and shutdown behavior, ensuring
// graceful handling of in-flight requests and proper resource cleanup.
const (
	// DefaultGracefulShutdown is the maximum time to wait during graceful shutdown.
	//
	// This value (30s) coordinates with Kubernetes:
	//   - Kubernetes default terminationGracePeriodSeconds is 30s
	//   - Allows time to complete in-flight requests
	//   - Allows time to drain connections and close resources
	//
	// Override for:
	//   - Long-running request handlers: Increase to match max request time
	//   - Stateless services: Can decrease to 10-15 seconds
	//   - Services with external dependencies: Match dependency shutdown time
	//
	// Important: If you increase this, also update terminationGracePeriodSeconds
	// in your Kubernetes deployment spec to match.
	DefaultGracefulShutdown = 30 * time.Second
)

// HTTP Server Timeouts
//
// These timeouts control HTTP server behavior for request handling and
// connection management. They protect against slow clients, resource exhaustion,
// and denial-of-service attacks.
const (
	// DefaultHTTPReadHeaderTimeout is the maximum time to read request headers.
	//
	// This value (10s) protects against slowloris attacks:
	//   - Short enough to prevent connection exhaustion
	//   - Long enough for legitimate slow networks
	//
	// Override for:
	//   - High-security environments: Decrease to 5s
	//   - Slow networks: Increase cautiously (max 30s)
	DefaultHTTPReadHeaderTimeout = 10 * time.Second

	// DefaultHTTPReadTimeout is the maximum time to read the entire request.
	//
	// This value (30s) accommodates various request sizes:
	//   - Long enough for large file uploads
	//   - Short enough to free resources from slow clients
	//
	// Override for:
	//   - File upload endpoints: Increase to minutes
	//   - API endpoints: Can decrease to 10-15s
	DefaultHTTPReadTimeout = 30 * time.Second

	// DefaultHTTPWriteTimeout is the maximum time to write the response.
	//
	// This value (30s) handles typical response times:
	//   - Long enough for complex queries and data aggregation
	//   - Short enough to detect unresponsive clients
	//
	// Override for:
	//   - Streaming endpoints: Increase significantly
	//   - Simple JSON APIs: Can decrease to 10-15s
	DefaultHTTPWriteTimeout = 30 * time.Second

	// DefaultHTTPIdleTimeout is the maximum time for keep-alive connections.
	//
	// This value (60s) balances connection reuse with resource efficiency:
	//   - Long enough to benefit from HTTP keep-alive
	//   - Short enough to free unused connections
	//
	// Override for:
	//   - High-traffic services: Can decrease to 30s
	//   - Low-traffic services: Can increase to 120s
	DefaultHTTPIdleTimeout = 60 * time.Second
)

// Retry Timings
//
// These timings control retry behavior for transient failures. They work
// together with retry count limits (not defined here) to implement backoff.
const (
	// DefaultRetryDelay is the initial delay between retry attempts.
	//
	// This value (100ms) is the starting point for exponential backoff:
	//   - Retry 1: 100ms
	//   - Retry 2: 200ms (with 2x backoff)
	//   - Retry 3: 400ms (with 2x backoff)
	//   - etc.
	//
	// The delay is short enough to quickly recover from transient failures
	// while providing enough spacing to avoid thundering herd effects.
	//
	// Override for:
	//   - Rate-limited APIs: Match rate limit window (e.g., 1s)
	//   - Database reconnection: Use 500ms-1s for connection pool recovery
	//   - External APIs: Match provider recommendations
	DefaultRetryDelay = 100 * time.Millisecond

	// DefaultMaxRetryInterval is the maximum delay between retry attempts.
	//
	// This value (5s) caps exponential backoff growth:
	//   - Prevents excessive delays after many retries
	//   - Still allows time for transient issues to resolve
	//
	// Override for:
	//   - Rate-limited APIs: Match rate limit window
	//   - External services: Increase to 30s for slow recovery
	DefaultMaxRetryInterval = 5 * time.Second
)
