---
name: adr-0031-getbalance-nil-guard-retention
description: Retain the nil guard on PositionKeepingClient in GetBalance for safety and testability
triggers:
  - Reviewing defensive nil checks on service dependencies in GetBalance
  - Evaluating whether to remove nil guards after constructor validation is added
  - Adding new constructors that may omit optional dependencies
instructions: |
  Keep the nil guard on positionKeepingClient in GetBalance (server.go). The guard
  returns codes.Unimplemented with a clear error message rather than panicking on a nil
  pointer dereference. This is intentional because multiple constructors exist that do
  not require a PK client, and the guard has zero performance cost.
---

# 31. Retain nil guard on PositionKeepingClient in GetBalance

Date: 2026-02-13

## Status

Accepted

## Context

The `GetBalance` method in `services/internal-account/service/server.go` includes a nil check on the `positionKeepingClient` field before calling Position Keeping:

```go
if s.positionKeepingClient == nil {
    operationStatus = operationStatusFailed
    return nil, status.Error(codes.Unimplemented, "position keeping service not configured")
}
```

During review of the account service wiring, the question was raised whether this guard is redundant given that production constructors (`NewServiceWithClients`, `NewServiceFull`) always receive a PK client. The concern was that nil guards on injected dependencies can mask configuration errors.

## Decision Drivers

* Multiple constructors exist with different dependency requirements
* Test code regularly uses `NewService(repo)` without a PK client
* A nil pointer panic produces an unhelpful stack trace compared to a gRPC status error
* The guard has zero runtime cost (single pointer comparison)

## Considered Options

1. Remove the nil guard (rely on constructor validation)
2. Keep the nil guard (current behavior)

## Decision Outcome

Chosen option: "Keep the nil guard", because it provides safety across all constructors with no cost.

### Positive Consequences

* Tests using `NewService(repo)` receive a clear `Unimplemented` error instead of a nil pointer panic when GetBalance is called
* Future constructors or configurations that omit PK client are handled gracefully
* The error message is actionable: callers know PK is not configured rather than debugging a nil dereference
* Zero performance impact: a single pointer comparison in a method that makes a network call

### Negative Consequences

* The guard could mask a misconfigured production deployment where PK client was accidentally nil. This risk is mitigated by the health check system, which reports PK connectivity status independently.

## Pros and Cons of the Options

### Remove the nil guard

Rely on constructor validation to ensure PK client is always non-nil.

* Good, because it reduces code in the hot path
* Bad, because `NewService` and `NewServiceWithValuationFeatures` constructors legitimately create services without a PK client
* Bad, because a nil pointer panic in production produces an opaque error with no gRPC status code

### Keep the nil guard

Retain the explicit nil check with a descriptive error.

* Good, because it supports all existing constructors safely
* Good, because the error message tells callers exactly what is wrong
* Good, because it costs nothing at runtime
* Good, because it provides a defensive layer independent of constructor logic

## Links

* [ADR-0023: Balance Delegation to Position Keeping](0023-balance-delegation-to-position-keeping.md)
* [ADR-0024: Internal Account Service](0024-internal-account-service.md)
* Related code: `services/internal-account/service/server.go` (GetBalance method)

## Notes

Re-evaluate if the service is refactored to a single constructor that requires all dependencies. If all constructors mandate a non-nil PK client, the guard becomes truly redundant and can be removed.
