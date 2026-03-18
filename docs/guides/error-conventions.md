---
name: error-conventions
description: Naming patterns for domain errors and gRPC status code mapping in Meridian services
triggers:
  - Adding errors to a domain package
  - Mapping domain errors to gRPC status codes
  - Implementing error handling in a service layer
  - Reviewing error types across services
---

# Error Conventions

This guide documents the error naming patterns used across Meridian services and explains
how domain errors map to gRPC status codes.

## Error Layers

Meridian services follow hexagonal architecture, and errors exist at two distinct layers:

| Layer | Package | Naming | Purpose |
|-------|---------|--------|---------|
| Domain | `services/{service}/domain/` | Semantic, entity-neutral | Express business rule violations |
| Persistence | `services/{service}/adapters/persistence/` | Entity-prefixed | Express storage-level failures |

Services should check domain errors in the service layer and translate them to gRPC codes.
Persistence errors leak through when the domain layer doesn't define an equivalent sentinel.

## Domain Layer Errors

Domain errors live in `domain/errors.go` or alongside the repository interface in
`domain/repository.go`. They express business rule violations in terms that the domain
understands, without leaking persistence details.

### Standard Sentinel Errors

Every service that reads or writes persistent state should define these sentinels:

```go
// domain/repository.go or domain/errors.go
var (
    // ErrNotFound is returned when a domain entity is not found.
    ErrNotFound = errors.New("entity not found")

    // ErrConflict is returned when an entity with the same unique key already exists.
    ErrConflict = errors.New("entity conflict")

    // ErrOptimisticLock is returned when a concurrent write is detected (version mismatch).
    ErrOptimisticLock = errors.New("optimistic lock failure: resource was modified")

    // ErrInvalidStatusTransition is returned when a state machine transition is invalid.
    ErrInvalidStatusTransition = errors.New("invalid status transition")
)
```

**Real examples from the codebase:**

```go
// services/reconciliation/domain/errors.go
var (
    ErrNotFound              = errors.New("entity not found")
    ErrConflict              = errors.New("entity conflict")
    ErrOptimisticLock        = errors.New("optimistic lock failure: resource was modified")
    ErrInvalidStatusTransition = errors.New("invalid status transition")
    ErrEmptyAccountID        = errors.New("account ID cannot be empty")
    ErrInvalidPeriod         = errors.New("period start must be before period end")
    ErrRunNotRunning         = errors.New("settlement run is not in running state")
)

// services/position-keeping/domain/repository.go
var (
    ErrNotFound       = errors.New("financial position log not found")
    ErrConflict       = errors.New("financial position log conflict")
    ErrOptimisticLock = errors.New("optimistic lock failure: resource was modified")
)
```

### Domain-Specific Validation Errors

For validation errors beyond the standard sentinels, use descriptive `Err` prefixed names
that are self-documenting without needing to check the entity type:

```go
var (
    ErrEmptyAccountID        = errors.New("account ID cannot be empty")
    ErrEmptyInstrumentCode   = errors.New("instrument code cannot be empty")
    ErrInvalidPeriod         = errors.New("period start must be before period end")
    ErrNegativeAmount        = errors.New("amount cannot be negative")
    ErrUnauthorized          = errors.New("unauthorized: insufficient permissions")
)
```

These errors are defined in the domain package and used in entity constructors and
mutation methods—not in the service or persistence layers.

## Persistence Layer Errors

Persistence errors live in `adapters/persistence/` and are **entity-prefixed** to avoid
ambiguity when multiple entity types share a repository file.

```go
// services/party/adapters/persistence/repository.go
var (
    ErrPartyNotFound     = errors.New("party not found")
    ErrPartyExists       = errors.New("party already exists")
    ErrVersionConflict   = errors.New("version conflict: party was modified by another transaction")
    ErrAssociationExists = errors.New("association already exists between parties")
    ErrInvalidCursor     = errors.New("invalid pagination cursor")
)
```

**Why entity-prefixed in persistence but not domain?**

- Domain errors represent business concepts (one error per concept, regardless of entity)
- Persistence files may contain multiple entity repositories—prefixing prevents shadowing
- Service code checks `persistence.ErrVersionConflict` specifically, not domain-level equivalents

## gRPC Status Code Mapping

The service layer translates domain and persistence errors into gRPC status codes. Use
`errors.Is` (not string comparison) to match sentinel errors.

### Standard Mapping Table

| Error | gRPC Code | HTTP Equivalent | Notes |
|-------|-----------|-----------------|-------|
| `domain.ErrNotFound` | `codes.NotFound` | 404 | Entity doesn't exist |
| `persistence.ErrNotFound` | `codes.NotFound` | 404 | Row missing in DB |
| `domain.ErrConflict` / `persistence.ErrAlreadyExists` | `codes.AlreadyExists` | 409 | Duplicate unique key |
| `persistence.ErrVersionConflict` | `codes.Aborted` | 409 | Concurrent modification—retry |
| `domain.ErrInvalidStatusTransition` | `codes.FailedPrecondition` | 422 | State machine violation |
| `domain.ErrUnauthorized` | `codes.PermissionDenied` | 403 | Authorization failed |
| Validation failures (input) | `codes.InvalidArgument` | 400 | Caller error, don't retry |
| All other errors | `codes.Internal` | 500 | Log and don't leak details |

### Implementation Pattern

```go
func (s *Service) ExecuteOperation(
    ctx context.Context,
    req *pb.OperationRequest,
) (*pb.OperationResponse, error) {
    entity, err := s.repo.FindByID(ctx, id)
    if err != nil {
        switch {
        case errors.Is(err, domain.ErrNotFound):
            return nil, status.Errorf(codes.NotFound, "entity %s not found", id)
        default:
            s.logger.Error("failed to find entity", "error", err, "id", id)
            return nil, status.Error(codes.Internal, "internal error")
        }
    }

    if err := entity.Transition(newState); err != nil {
        switch {
        case errors.Is(err, domain.ErrInvalidStatusTransition):
            return nil, status.Errorf(codes.FailedPrecondition, "invalid transition: %v", err)
        default:
            return nil, status.Error(codes.Internal, "internal error")
        }
    }

    if err := s.repo.Update(ctx, entity); err != nil {
        switch {
        case errors.Is(err, persistence.ErrVersionConflict):
            return nil, status.Error(codes.Aborted, "concurrent modification detected, please retry")
        default:
            s.logger.Error("failed to update entity", "error", err)
            return nil, status.Error(codes.Internal, "internal error")
        }
    }

    return &pb.OperationResponse{...}, nil
}
```

**Real example from `services/tenant/service/grpc_service.go`:**

```go
// errors.Is chain for update operation
if errors.Is(err, persistence.ErrVersionConflict) {
    return nil, status.Errorf(codes.Aborted, "concurrent modification detected, please retry")
}
if errors.Is(err, domain.ErrNotFound) {
    return nil, status.Errorf(codes.NotFound, "tenant %s not found", req.TenantId)
}
return nil, status.Errorf(codes.Internal, "failed to update tenant status")
```

### Error Message Guidelines

- **`codes.NotFound`**: Include the entity type and identifier: `"tenant %s not found"`
- **`codes.InvalidArgument`**: Include what was invalid and why: `"invalid tenant ID: %v"`
- **`codes.AlreadyExists`**: Include the conflicting value: `"slug %s is already taken"`
- **`codes.Aborted`**: Use a consistent message callers can check: `"concurrent modification detected, please retry"`
- **`codes.Internal`**: Never leak internal details—log the actual error, return a generic message

### What NOT to Do

```go
// DON'T: Compare error strings
if err.Error() == "entity not found" { ... }

// DON'T: Use codes.Unknown for known conditions
return nil, status.Error(codes.Unknown, err.Error())

// DON'T: Leak internal error details to callers
return nil, status.Errorf(codes.Internal, "SQL error: %v", err)

// DON'T: Use codes.Internal for caller errors (validation failures)
return nil, status.Error(codes.Internal, "amount is required")
// Should be codes.InvalidArgument
```

## Shared Package Errors

Packages in `shared/` define their own sentinels. Service code typically wraps or re-exports
these as needed:

```go
// shared/platform/quantity
var ErrInstrumentMismatch = errors.New("instrument mismatch: ...")
var ErrDivisionByZero     = errors.New("division by zero")

// shared/pkg/money
var ErrInvalidCurrency  = errors.New("invalid currency")
var ErrCurrencyMismatch = quantity.ErrInstrumentMismatch  // aliased
var ErrAmountOverflow   = errors.New("amount overflow")

// shared/pkg/amount
var ErrInvalidDimension = errors.New("invalid dimension")
```

When quantity/money/amount errors bubble up through a service, map them to
`codes.InvalidArgument` if they result from bad input, or `codes.Internal` if they
indicate a programming error.

## Organizing Error Files

| Scenario | File | Pattern |
|----------|------|---------|
| Few errors, simple service | `domain/repository.go` | Group with the interface they apply to |
| Many errors, complex service | `domain/errors.go` | Dedicated file, grouped by category |
| Persistence-level errors | `adapters/persistence/repository.go` | Top of file, entity-prefixed |
| Service-level construction errors | `service/grpc_service.go` | Inline `var` block, self-descriptive |
