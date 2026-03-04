// Package dispatch provides shared dispatch infrastructure for gateway services.
//
// This package contains reusable building blocks for services that dispatch
// instructions to external providers:
//
//   - Circuit breaker state machine (CircuitBreaker) for per-connection fault isolation
//   - Exponential backoff with optional jitter (CalculateNextRetry, CalculateNextRetryWithJitter)
//   - Domain types shared across gateways (CircuitState, HealthStatus, RetryPolicy, InstructionStatus)
//   - Generic interfaces (Dispatcher, DispatchableInstruction, InstructionFetcher, InstructionPersister)
//   - Poll-dispatch worker loop (Worker) parameterized by instruction type
//
// The types in this package complement (not replace) the generic HTTP client
// resilience patterns in shared/pkg/clients. Those handle transport-level retry
// and circuit breaking for HTTP calls. This package handles domain-level dispatch
// patterns: instruction lifecycle management, per-connection circuit breaking with
// explicit state tracking, and batch-oriented worker loops.
package dispatch
