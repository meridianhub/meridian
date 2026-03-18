# gRPC Handler File Splitting Convention

This guide describes when and how to split a service's `server.go` into separate endpoint files.

## Overview

Every gRPC service starts with a `server.go` file containing the struct definition, constructor,
and all RPC handler methods. As a service grows, it becomes necessary to split RPC handlers into
separate files grouped by proto service or domain noun. This guide defines when to split and what
naming to use.

## The Role of server.go

`server.go` is always the entry point for a service package. It must contain:

- The `Service` struct definition and its fields
- Interface definitions for external dependencies (repositories, clients)
- Constructor (`New*` function)
- Functional options (`With*` or `Option` types)
- Package-level constants and errors shared across all endpoint files

`server.go` should **not** grow to hold all RPC implementations once a service matures. The
`financial-accounting` service demonstrates the clean end state: `server.go` holds only struct,
interfaces, constructor, and options; all RPCs live in `grpc_*_endpoints.go` files.

## When to Split

Split RPC handlers out of `server.go` when **any** of the following conditions hold:

1. **Size** — `server.go` exceeds ~300 lines of RPC handler code (excluding struct, constructor,
   and interfaces).
2. **Multiple RPC groups** — The service implements more than one logical proto service or noun
   group (e.g., posting operations and booking operations are distinct enough to separate).
3. **Test isolation** — A group of RPCs has enough test surface that a dedicated
   `*_endpoints_test.go` improves readability.
4. **Different dependency focus** — Some RPCs use only a subset of the struct's dependencies,
   making the grouping semantically coherent.

Do **not** split merely to reduce line count if all RPCs share the same domain concern. Three
related methods in a 200-line `server.go` are easier to navigate than three files.

## Naming Convention

Files in the `service/` package follow this pattern:

```text
grpc_{noun}_endpoints.go
```

Where `{noun}` is a lowercase, underscore-separated name identifying the proto RPC group. It
typically matches the BIAN behaviour concept or proto message noun.

Examples from the codebase:

| Service | File | RPC Group |
|---|---|---|
| `current-account` | `grpc_account_endpoints.go` | Account CRUD (Initiate, List, Retrieve, Update) |
| `current-account` | `grpc_control_endpoints.go` | Control and status operations |
| `current-account` | `grpc_deposit_endpoints.go` | Deposit and withdrawal operations |
| `financial-accounting` | `grpc_posting_endpoints.go` | Ledger posting RPCs |
| `financial-accounting` | `grpc_booking_endpoints.go` | Financial booking log RPCs |
| `financial-accounting` | `grpc_control_endpoints.go` | Control plane RPCs |
| `financial-accounting` | `grpc_ledger_endpoints.go` | Ledger query RPCs |

Test files mirror the source file name:

```text
grpc_{noun}_endpoints_test.go
```

## File Structure

Each `grpc_{noun}_endpoints.go` file:

- Declares `package service` (same package as `server.go`)
- Contains only `func (s *Service)` methods for its RPC group
- May define private helper functions scoped to that group
- Should **not** re-declare types, errors, or constants already in `server.go`

```text
services/{service}/service/
├── server.go                     # Struct, constructor, interfaces, options
├── grpc_{noun}_endpoints.go      # RPC handlers for noun group
├── grpc_{noun}_endpoints_test.go
├── grpc_{other}_endpoints.go     # Additional group when needed
├── mappers.go                    # Proto ↔ domain conversions
└── errors.go                     # Sentinel errors (when numerous)
```

## The reference-data Exception

The `reference-data` service uses a `handler/` package directory instead of `service/`, with a
different split pattern:

```text
services/reference-data/handler/
├── grpc_handler.go               # Core struct, constructor, shared logic
├── account_type_handler.go       # Account type RPC methods
├── mapping_handler.go            # Mapping RPC methods
├── node_handler.go               # Node RPC methods
└── pagination.go                 # Shared pagination utilities
```

This `{noun}_handler.go` naming is specific to the `handler/` package. New services should use
the `service/` package with `grpc_{noun}_endpoints.go` naming.

## Services Eligible for Splitting

The following services have `server.go` files large enough to benefit from splitting:

| Service | server.go Lines | Status |
|---|---|---|
| `party` | 1359 | Monolithic — candidate for splitting |
| `reconciliation` | 999 | Partially split (`dispute_handler.go`, `list_handler.go`) |
| `internal-account` | 952 | Monolithic — candidate for splitting |
| `identity` | 887 | Monolithic — candidate for splitting |
| `tenant` | 724 | Monolithic — candidate for splitting |
| `current-account` | 446 | Split into 3 endpoint files |
| `financial-accounting` | 294 | Fully split — `server.go` is struct/constructor only |

Services under 300 lines (`market-information`, `audit-worker`) do not need splitting.

## Worked Example: financial-accounting

`financial-accounting` is the reference implementation. `server.go` contains only the struct, all
interface definitions, constructor, and options:

```go
// server.go
type FinancialAccountingService struct {
    repo        persistence.Repository
    idempotency idempotency.Service
    // ...
}

func NewFinancialAccountingService(repo persistence.Repository, ...) *FinancialAccountingService {
    // ...
}

func WithRegistry(registry InstrumentRegistry) Option { ... }
```

RPC methods live in focused files:

```go
// grpc_posting_endpoints.go
func (s *FinancialAccountingService) CaptureLedgerPosting(...) { ... }
func (s *FinancialAccountingService) RetrieveLedgerPosting(...) { ... }

// grpc_booking_endpoints.go
func (s *FinancialAccountingService) InitiateFinancialBookingLog(...) { ... }
func (s *FinancialAccountingService) RetrieveFinancialBookingLog(...) { ... }
```

## See Also

- [ADR-015: Standard Service Directory Structure](../adr/0015-standard-service-directory-structure.md)
  — defines where `service/` fits in the overall layout
- [New BIAN Service Checklist](new-bian-service-checklist.md) — checklist for creating a new
  service including initial file layout
