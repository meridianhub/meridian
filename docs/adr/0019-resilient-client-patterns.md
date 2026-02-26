---
name: adr-019-resilient-client-patterns
description: Shared resilient client patterns with circuit breaker, retry, and saga orchestration for fault-tolerant inter-service communication
triggers:

  - Implementing gRPC client for inter-service communication
  - Adding circuit breaker or retry logic to service clients
  - Orchestrating multi-service transactions with compensation
  - Designing fault-tolerant microservice interactions
  - Handling transient failures in distributed systems

instructions: |
  Use shared/pkg/clients patterns for all inter-service gRPC communication.
  ResilientClient combines circuit breaker + retry for idempotent operations.
  Use ExecuteWithResilienceNoRetry for non-idempotent writes.
  Saga orchestration for multi-step distributed transactions with compensation.
---

# 19. Resilient Client Patterns for Inter-Service Communication

Date: 2025-12-17

## Status

Accepted

## Context

The Meridian platform uses a microservices architecture (see [ADR-002](./0002-microservices-per-bian-domain.md)) where services communicate via gRPC. This architecture requires fault-tolerant inter-service calls to handle:

* **Network failures**: Transient connectivity issues between pods
* **Service degradation**: Downstream services under load or partially failing
* **Partial failures**: Some operations succeeding while others fail in distributed transactions
* **Cascading failures**: One failing service overwhelming dependent services

Without resilience patterns, a single failing service can cascade failures across the entire platform, violating the high-availability requirements for financial services.

The team identified code duplication across services: each service implementing similar circuit breaker, retry, and error handling logic (~200 lines per service).

## Decision Drivers

* **Prevent cascading failures** across BIAN service boundaries
* **Handle transient failures** without manual intervention
* **Support distributed transactions** (e.g., CurrentAccount -> PositionKeeping -> FinancialAccounting)
* **Reduce boilerplate**: Avoid duplicating ~200 lines of resilience code per service
* **Standardize patterns** across all services for consistency
* **Observability**: Centralised logging for circuit breaker state changes and retries
* **Idempotency awareness**: Distinguish between operations safe to retry and those that are not

## Considered Options

1. **Shared resilient client patterns** in `shared/pkg/clients`
2. **Service mesh** (Istio/Linkerd) for infrastructure-level resilience
3. **Per-service custom implementations**
4. **No resilience patterns** (rely on Kubernetes restarts)

## Decision Outcome

Chosen option: **"Shared resilient client patterns in `shared/pkg/clients`"**, because:

* Provides application-level control over retry logic (service mesh cannot handle idempotency)
* Lower operational complexity than service mesh for current 6-service architecture
* Reusable across all services with zero duplication
* Type-safe Go generics (`ExecuteWithResilience[T any]`) for any gRPC response type
* Well-tested libraries: `gobreaker/v2` (circuit breaker), `backoff/v4` (retry)

### Positive Consequences

* **Zero code duplication**: All services share the same resilience implementation
* **Fast iteration**: Edit resilience logic once, all services benefit
* **Type safety**: Generic functions prevent runtime type assertion errors
* **Comprehensive testing**: >90% coverage in `shared/pkg/clients/`
* **Explicit configuration**: Each client can tune thresholds independently
* **Observability**: Circuit breaker state changes logged for debugging

### Negative Consequences

* Requires developers to understand circuit breaker and retry patterns
* No automatic retry for HTTP endpoints (gRPC-focused)
* Additional import dependency for all services

## Pros and Cons of the Options

### Option 1: Shared Resilient Client Patterns (Chosen)

Centralised implementation in `shared/pkg/clients/` with:
* Circuit breaker wrapping `sony/gobreaker/v2`
* Retry with exponential backoff using `cenkalti/backoff/v4`
* Resilient client combining both patterns
* Saga orchestration for distributed transactions

* Good, because application-level control distinguishes idempotent vs non-idempotent operations
* Good, because zero code duplication across services
* Good, because type-safe generics for any response type
* Good, because comprehensive test coverage (>90%)
* Good, because explicit per-client configuration
* Bad, because requires developers to understand circuit breaker and retry patterns
* Bad, because no automatic retry for HTTP endpoints (gRPC-focused)

### Option 2: Service Mesh (Istio/Linkerd)

Infrastructure-level resilience via sidecar proxies.

* Good, because infrastructure-level resilience requires no code changes
* Good, because unified observability (metrics, tracing)
* Bad, because operational complexity (sidecar proxies, control plane)
* Bad, because cannot distinguish idempotent vs non-idempotent operations
* Bad, because no saga orchestration support
* Bad, because overkill for 6-service architecture

### Option 3: Per-Service Custom Implementations

Each service implements its own resilience patterns.

* Good, because tailored to each service's specific needs
* Bad, because code duplication (~200 lines per service x 6 services = 1200 LOC)
* Bad, because inconsistent behaviour across services
* Bad, because harder to maintain and test

### Option 4: No Resilience Patterns

Rely on Kubernetes pod restarts and gRPC deadline propagation.

* Good, because simplest implementation
* Bad, because cascading failures when services degrade
* Bad, because no retry logic for transient failures
* Bad, because unacceptable for financial services (high availability requirement)

## Implementation Patterns

### Pattern 1: Circuit Breaker

File: `shared/pkg/clients/circuitbreaker.go`

The circuit breaker prevents cascading failures by failing fast when a downstream service is unhealthy.

```go
import "github.com/meridianhub/meridian/shared/pkg/clients"

// Create circuit breaker with default configuration
config := clients.DefaultCircuitBreakerConfig("financial-accounting")
cb := clients.NewCircuitBreaker(config, logger)

// Execute gRPC call through circuit breaker
result, err := cb.Execute(ctx, func() (any, error) {
    return grpcClient.CapturePosting(ctx, req)
})
```

**State transitions:**
* **Closed** (normal): Requests pass through, failures counted
* **Open** (failing): Requests fail fast without calling downstream
* **Half-Open** (testing): Limited requests allowed to test recovery

**Default configuration:**
* Opens after 5 consecutive failures
* Stays open for 30 seconds
* Allows 1 test request in half-open state

### Pattern 2: Retry with Exponential Backoff

File: `shared/pkg/clients/retry.go`

Retry handles transient failures with exponential backoff and jitter.

```go
config := clients.RetryConfig{
    MaxRetries:          3,
    InitialInterval:     100 * time.Millisecond,
    MaxInterval:         5 * time.Second,
    Multiplier:          2.0,
    RandomizationFactor: 0.5,  // +/-50% jitter to prevent thundering herd
}

err := clients.Retry(ctx, config, func() error {
    _, err := grpcClient.RecordTransaction(ctx, req)
    return err
})
```

**Retryable gRPC status codes:**
* `Unavailable` - Service temporarily unavailable
* `DeadlineExceeded` - Request timeout
* `ResourceExhausted` - Rate limiting
* `Internal` - Transient internal errors

**Non-retryable status codes:**
* `InvalidArgument` - Client error, won't succeed on retry
* `NotFound` - Resource doesn't exist
* `PermissionDenied` - Authorisation failure
* `Unauthenticated` - Authentication failure

### Pattern 3: Resilient Client (Recommended)

File: `shared/pkg/clients/resilient.go`

Combines circuit breaker and retry in a single wrapper. **Use this for most cases.**

```go
// Create resilient client with default configuration
config := clients.DefaultResilientClientConfig("position-keeping")
resilientClient := clients.NewResilientClient(config)

// For idempotent operations (GET, PUT with idempotency key)
resp, err := clients.ExecuteWithResilience(
    ctx,
    resilientClient,
    "RetrievePositionLog",
    func() (*pb.RetrievePositionLogResponse, error) {
        return grpcClient.RetrievePositionLog(ctx, req)
    },
)

// For non-idempotent operations (POST without idempotency key)
resp, err := clients.ExecuteWithResilienceNoRetry(
    ctx,
    resilientClient,
    "CreateAccount",
    func() (*pb.CreateAccountResponse, error) {
        return grpcClient.CreateAccount(ctx, req)
    },
)
```

### Pattern 4: Saga Orchestration

File: `shared/pkg/clients/saga.go`

For multi-service transactions requiring compensation on failure.

```go
saga := clients.NewSagaOrchestrator(logger)

// Each step captures its own transaction ID for compensation
var positionTxnID, postingTxnID string

// Step 1: Record position
saga.AddStep(
    "RecordPosition",
    func(ctx context.Context) error {
        resp, err := positionClient.RecordTransaction(ctx, positionReq)
        if err == nil {
            positionTxnID = resp.TransactionId  // Save for compensation
        }
        return err
    },
    func(ctx context.Context) error {
        _, err := positionClient.ReverseTransaction(ctx, &pb.ReverseRequest{
            TransactionId: positionTxnID,
        })
        return err
    },
)

// Step 2: Post to ledger
saga.AddStep(
    "PostToLedger",
    func(ctx context.Context) error {
        resp, err := accountingClient.CapturePosting(ctx, postingReq)
        if err == nil {
            postingTxnID = resp.PostingId  // Save posting ID for compensation
        }
        return err
    },
    func(ctx context.Context) error {
        _, err := accountingClient.ReversePosting(ctx, &pb.ReverseRequest{
            PostingId: postingTxnID,  // Use posting-specific ID
        })
        return err
    },
)

// Execute saga (auto-compensates on failure)
result := saga.Execute(ctx)
if !result.Success {
    return fmt.Errorf("saga failed at step %s: %w", result.FailedStep, result.Error)
}
```

## Configuration Guidelines

### Default Configuration (Recommended for Most Cases)

```go
config := clients.DefaultResilientClientConfig("service-name")
// Circuit Breaker: 5 consecutive failures -> open for 30s -> allow 1 test request
// Retry: 3 attempts, 100ms -> 200ms -> 400ms with +/-50% jitter
```

### Custom Configuration (For Specific Requirements)

```go
config := clients.ResilientClientConfig{
    // Circuit Breaker
    CircuitBreakerName:     "critical-service",
    FailureThreshold:       10,               // Higher threshold for less sensitive service
    CircuitBreakerTimeout:  60 * time.Second, // Stay open longer
    MaxRequests:            3,                // More test requests in half-open

    // Retry
    MaxRetries:             5,                // More retries for flaky service
    InitialInterval:        500 * time.Millisecond,
    MaxInterval:            30 * time.Second,
    Multiplier:             2.0,
    RandomizationFactor:    0.5,

    // Observability
    Logger:                 logger,
}
```

## When to Use Each Pattern

| Pattern | Use Case | Example |
|---------|----------|---------|
| `ExecuteWithResilience` | Idempotent operations (GET, PUT with idempotency key) | RetrieveAccount, RecordTransaction |
| `ExecuteWithResilienceNoRetry` | Non-idempotent writes | CreateAccount (no idempotency key) |
| `SagaOrchestrator` | Multi-service transactions requiring compensation | ExecuteDeposit (CurrentAccount -> PositionKeeping -> FinancialAccounting) |
| Circuit breaker only | Fire-and-forget operations | Event publishing |

## Links

* [Circuit Breaker Implementation](../../shared/pkg/clients/circuitbreaker.go)
* [Retry Implementation](../../shared/pkg/clients/retry.go)
* [Resilient Client Wrapper](../../shared/pkg/clients/resilient.go)
* [Saga Orchestration](../../shared/pkg/clients/saga.go)
* [Package Documentation](../../shared/pkg/clients/doc.go)
* [ADR-002: Microservices Architecture](./0002-microservices-per-bian-domain.md)
* [ADR-010: gRPC Client-Side Load Balancing](./0010-grpc-client-side-load-balancing.md)
* [gobreaker/v2 Documentation](https://github.com/sony/gobreaker)
* [backoff/v4 Documentation](https://github.com/cenkalti/backoff)

## Notes

### Migration Strategy for Existing Services

Services that previously defined their own resilience implementations should migrate to the shared package:

```go
// Old import (deprecated)
import "github.com/meridianhub/meridian/services/current-account/clients"

// New import (recommended)
import "github.com/meridianhub/meridian/shared/pkg/clients"
```

Service-specific packages re-export shared types with deprecation notices for backward compatibility.

### Future Considerations

* Consider service mesh if service count grows beyond 10-15
* Add distributed tracing (OpenTelemetry) for saga orchestration visibility
* Implement bulkhead pattern if resource exhaustion becomes an issue
* Add rate limiting if external API consumption patterns emerge
