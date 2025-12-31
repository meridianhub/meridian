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
// # Categories
//
// The constants are organized into categories:
//   - RPC timeouts: For inter-service communication
//   - Health check timeouts: For probe responsiveness
//   - Circuit breaker timeouts: For failure recovery
//   - Lifecycle timeouts: For graceful shutdown and startup
//   - Retry timings: For backoff and retry logic
package defaults
