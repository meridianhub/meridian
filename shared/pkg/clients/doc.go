// Package clients provides shared resilience patterns for inter-service
// communication within the Meridian platform.
//
// This package offers reusable implementations of:
//   - Circuit breaker pattern (wrapping sony/gobreaker)
//   - Retry with exponential backoff and jitter
//   - Saga pattern for distributed transaction coordination
//   - Resilient client wrapper combining circuit breaker and retry
//   - Utilities for correlation ID propagation and timeout handling
//
// # Circuit Breaker
//
// The circuit breaker prevents cascading failures by failing fast when a
// downstream service is unhealthy. Use [NewCircuitBreaker] to create one:
//
//	config := clients.DefaultCircuitBreakerConfig("my-service")
//	cb := clients.NewCircuitBreaker(config, logger)
//
//	result, err := cb.Execute(ctx, func() (any, error) {
//		return myClient.Call(ctx, req)
//	})
//
// # Retry
//
// The retry logic uses exponential backoff with jitter to handle transient
// failures. Only gRPC status codes indicating transient errors are retried
// (Unavailable, DeadlineExceeded, ResourceExhausted, Internal).
//
//	config := clients.DefaultRetryConfig()
//	err := clients.Retry(ctx, config, func() error {
//		_, err := myClient.Call(ctx, req)
//		return err
//	})
//
// # Resilient Client
//
// For most use cases, [ResilientClient] combines circuit breaker and retry
// in a convenient wrapper:
//
//	config := clients.DefaultResilientClientConfig("my-service")
//	resilient := clients.NewResilientClient(config)
//
//	result, err := clients.ExecuteWithResilience(ctx, resilient, "operation", func() (*Response, error) {
//		return myClient.Call(ctx, req)
//	})
//
// For non-idempotent operations, use [ExecuteWithResilienceNoRetry]:
//
//	result, err := clients.ExecuteWithResilienceNoRetry(ctx, resilient, "create", func() (*Response, error) {
//		return myClient.Create(ctx, req)
//	})
//
// # Saga Pattern
//
// For distributed transactions requiring compensation on failure:
//
//	saga := clients.NewSagaOrchestrator(logger)
//	saga.AddStep("create-order",
//		func(ctx context.Context) error { return createOrder(ctx) },
//		func(ctx context.Context) error { return cancelOrder(ctx) },
//	)
//	saga.AddStep("reserve-inventory",
//		func(ctx context.Context) error { return reserveInventory(ctx) },
//		func(ctx context.Context) error { return releaseInventory(ctx) },
//	)
//	result := saga.Execute(ctx)
//
// # Migration from Service-Specific Clients
//
// Services that previously defined their own circuit breaker, retry, or saga
// implementations should migrate to this shared package:
//
//	// Old import (deprecated)
//	import "github.com/meridianhub/meridian/services/current-account/clients"
//
//	// New import (recommended)
//	import "github.com/meridianhub/meridian/shared/pkg/clients"
//
// The service-specific packages re-export these types with deprecation notices
// to maintain backward compatibility during migration.
package clients
