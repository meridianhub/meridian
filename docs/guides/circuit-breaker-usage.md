# Circuit Breaker Usage Guide

This document describes how to use the circuit breaker pattern for inter-service gRPC calls in Meridian services.

**Location**: `shared/pkg/clients/` - Resilience patterns (circuit breakers, retries) live in the shared package.
Service-specific clients are in `services/{service}/client/`.

## Overview

The circuit breaker pattern protects services from cascading failures by monitoring downstream service calls and
preventing requests when a service is unhealthy. The implementation uses the sony/gobreaker library with context support
and comprehensive logging.

## Circuit Breaker States

1. **Closed**: Normal operation, all requests pass through
2. **Open**: Service is unhealthy, requests fail immediately (fast-fail)
3. **Half-Open**: Testing if service has recovered, limited requests allowed

## Basic Usage

### 1. Create a Circuit Breaker

```go
import (
    "log/slog"
    "github.com/meridianhub/meridian/shared/pkg/clients"
)

// Use default configuration
logger := slog.Default()
config := clients.DefaultCircuitBreakerConfig("financial-accounting-service")
cb := clients.NewCircuitBreaker(config, logger)
```

### 2. Execute Operations with Circuit Breaker Protection

```go
ctx := context.Background()

// Wrap your gRPC call with circuit breaker
result, err := cb.Execute(ctx, func() (any, error) {
    // Your gRPC call here
    resp, err := grpcClient.InitiateFinancialBookingLog(ctx, req)
    return resp, err
})

if err != nil {
    if errors.Is(err, gobreaker.ErrOpenState) {
        // Circuit is open - service is unhealthy
        return nil, fmt.Errorf("financial accounting service unavailable: %w", err)
    }
    return nil, fmt.Errorf("operation failed: %w", err)
}

// Type assert the result
response := result.(*financialaccountingv1.InitiateFinancialBookingLogResponse)
```

## Configuration

### Default Configuration

The `DefaultCircuitBreakerConfig` provides sensible defaults:

- **MaxRequests**: 1 (only 1 request allowed in half-open state)
- **Interval**: 60 seconds (reset failure counts after this period in closed state)
- **Timeout**: 30 seconds (duration before transitioning from open to half-open)
- **ReadyToTrip**: Trips after 5 consecutive failures

### Custom Configuration

```go
config := clients.CircuitBreakerConfig{
    Name:        "my-service",
    MaxRequests: 3,                   // Allow 3 test requests in half-open
    Interval:    2 * time.Minute,     // Reset counts every 2 minutes
    Timeout:     45 * time.Second,    // Stay open for 45 seconds
    ReadyToTrip: func(counts gobreaker.Counts) bool {
        // Custom logic: trip after 3 failures OR >50% error rate with min 10 requests
        failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
        return counts.ConsecutiveFailures >= 3 ||
               (counts.Requests >= 10 && failureRatio > 0.5)
    },
    OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
        // Custom logging or alerting
        if to == gobreaker.StateOpen {
            // Trigger alert: service is down
            alert.Send("Circuit breaker opened for " + name)
        }
    },
}

cb := clients.NewCircuitBreaker(config, logger)
```

## Integration with Existing Clients

### Wrapping gRPC Client Methods

Create a circuit breaker-aware wrapper around your gRPC client:

```go
type ResilientFinancialAccountingClient struct {
    client *FinancialAccountingGRPCClient
    cb     *clients.CircuitBreaker
}

func NewResilientFinancialAccountingClient(
    grpcClient *FinancialAccountingGRPCClient,
    logger *slog.Logger,
) *ResilientFinancialAccountingClient {
    config := clients.DefaultCircuitBreakerConfig("financial-accounting-service")
    cb := clients.NewCircuitBreaker(config, logger)

    return &ResilientFinancialAccountingClient{
        client: grpcClient,
        cb:     cb,
    }
}

func (c *ResilientFinancialAccountingClient) InitiateFinancialBookingLog(
    ctx context.Context,
    req *financialaccountingv1.InitiateFinancialBookingLogRequest,
) (*financialaccountingv1.InitiateFinancialBookingLogResponse, error) {
    result, err := c.cb.Execute(ctx, func() (any, error) {
        return c.client.InitiateFinancialBookingLog(ctx, req)
    })

    if err != nil {
        return nil, err
    }

    return result.(*financialaccountingv1.InitiateFinancialBookingLogResponse), nil
}
```

## Context Support

The circuit breaker respects context cancellation and deadlines:

```go
// Context with timeout
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

result, err := cb.Execute(ctx, func() (any, error) {
    // This will be cancelled if context timeout is reached
    return grpcClient.SomeMethod(ctx, req)
})

if err != nil {
    if errors.Is(err, context.DeadlineExceeded) {
        // Context timeout occurred
    } else if errors.Is(err, context.Canceled) {
        // Context was cancelled
    }
}
```

## Monitoring Circuit Breaker State

The circuit breaker logs state transitions automatically:

```text
level=INFO msg="circuit breaker state changed" name=financial-accounting-service from=closed to=open
level=INFO msg="circuit breaker state changed" name=financial-accounting-service from=open to=half-open
level=INFO msg="circuit breaker state changed" name=financial-accounting-service from=half-open to=closed
```

You can also query the current state:

```go
state := cb.State()
switch state {
case gobreaker.StateClosed:
    // Normal operation
case gobreaker.StateOpen:
    // Service is down
case gobreaker.StateHalfOpen:
    // Testing recovery
}
```

## Best Practices

1. **Service-Level Circuit Breakers**: Create one circuit breaker per downstream service, not per method
2. **Appropriate Thresholds**: Set `ReadyToTrip` based on your service's SLA and expected error rates
3. **Timeout Configuration**: Set `Timeout` to allow enough time for service recovery (typically 30-60 seconds)
4. **Fallback Logic**: Always provide fallback behaviour when circuit is open
5. **Monitoring**: Integrate circuit breaker state changes with your observability platform
6. **Testing**: Test circuit breaker behaviour in your integration tests with simulated failures

## Error Handling

```go
result, err := cb.Execute(ctx, func() (any, error) {
    return grpcClient.SomeMethod(ctx, req)
})

if err != nil {
    switch {
    case errors.Is(err, gobreaker.ErrOpenState):
        // Circuit is open - use cached data or return degraded response
        return getCachedData()
    case errors.Is(err, gobreaker.ErrTooManyRequests):
        // Too many requests in half-open state - retry later
        return nil, ErrServiceBusy
    case errors.Is(err, context.DeadlineExceeded):
        // Timeout occurred
        return nil, ErrTimeout
    default:
        // Other error from the underlying service
        return nil, fmt.Errorf("service error: %w", err)
    }
}
```

## Testing

The circuit breaker includes comprehensive tests covering:

- State transitions (closed → open → half-open → closed)
- Threshold-based tripping
- Context cancellation and timeout
- Concurrent execution
- Custom configuration

See `circuitbreaker_test.go` for examples.
