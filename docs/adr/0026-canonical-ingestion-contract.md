---
name: adr-026-canonical-ingestion-contract
description: External systems structure data; Meridian validates and stores
triggers:
  - Designing data ingestion from external sources
  - Building adapters for market data, IoT, or third-party feeds
  - Deciding where ETL logic should live
  - Implementing validation at service boundaries
instructions: |
  Meridian services accept ONLY pre-structured data conforming to their Protobuf schemas.
  All extraction, transformation, and normalization is the caller's responsibility.
  CEL validation at the boundary enforces the contract - invalid data is rejected, not fixed.
  External adapters are reference utilities, not core service features.
---

# 26. Canonical Ingestion Contract

Date: 2026-01-17

## Status

Accepted

## Context

Meridian needs to ingest data from diverse external sources: market data providers (ECB, Bloomberg),
IoT devices (smart meters), weather APIs, and tenant-specific systems. Each source has different:

- **Protocols**: REST, WebSocket, TCP, file drops
- **Formats**: XML, JSON, CSV, binary
- **Schedules**: Real-time streaming, daily batches, on-demand
- **Error modes**: Rate limits, connection drops, schema changes

The question is: **Where should the "messy ETL" logic live?**

BIAN's Service Domain encapsulation principle provides guidance:

> "Because the Service Domain handles all activities for the complete life cycle it internalizes
> or encapsulates away much of the more complex processing logic."

However, this refers to **domain-specific** processing, not universal data integration middleware.
Market Information Management's domain is storing and querying market observations with bi-temporal
integrity - not parsing arbitrary external formats.

## Decision Drivers

* **Separation of Concerns**: Domain services should focus on their core capability, not protocol
  translation
* **Maintainability**: Source-specific adapters change frequently (API versions, format changes);
  core services should remain stable
* **Testability**: CEL validation at the boundary provides a clear contract that's easy to test
* **Flexibility**: Tenants should be able to build their own adapters without modifying Meridian
* **Security**: Rejecting malformed data at the boundary prevents garbage-in scenarios
* **BIAN Alignment**: Follows the atomic service design principle where each domain handles one
  type of asset/pattern

## Considered Options

1. **Universal Ingestion Middleware**: Build a separate ETL service that handles all source
   connectivity, extraction, and transformation
2. **Source-Specific Adapters in Core Services**: Embed Bloomberg, ECB, smart meter adapters
   directly in each consuming service
3. **Canonical Ingestion Contract**: Define a strict boundary - services accept only pre-structured
   Protobuf; adapters are external utilities

## Decision Outcome

Chosen option: **"Canonical Ingestion Contract"**, because it provides the cleanest separation of
concerns while maintaining data integrity through CEL validation at the boundary.

### The Contract

```text
┌─────────────────────────────────────────────────────────────────────────────┐
│                         CANONICAL INGESTION CONTRACT                        │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  External World                    │  Meridian Core                         │
│  ─────────────                     │  ────────────                          │
│                                    │                                        │
│  ┌─────────────────┐               │   ┌───────────────────────┐            │
│  │ External Source │               │   │ DataSetDefinition     │            │
│  │ (Bloomberg/ECB/ │               │   │ ─────────────────     │            │
│  │  Smart Meter)   │               │   │ validation_expr (CEL) │            │
│  └────────┬────────┘               │   └───────────┬───────────┘            │
│           │ Raw Data               │               │                        │
│           ▼                        │               ▼                        │
│  ┌─────────────────┐               │   ┌───────────────────────┐            │
│  │ External        │  Structured   │   │ CEL Validator         │            │
│  │ Adapter         │  Protobuf     │   │ ─────────────         │            │
│  │ ─────────────── ├───────────────┼──►│ PASS → Store          │            │
│  │ YOUR CODE       │  (gRPC call)  │   │ FAIL → INVALID_ARG    │            │
│  └─────────────────┘               │   └───────────────────────┘            │
│                                    │                                        │
│  Responsibility:                   │   Responsibility:                      │
│  • Connectivity                    │   • Schema Definition                  │
│  • Extraction                      │   • Contract Enforcement               │
│  • Normalization                   │   • Bi-Temporal Storage                │
│  • Scheduling                      │   • Quality Resolution                 │
│  • Error Recovery                  │   • Knowledge Lineage                  │
│                                    │                                        │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Boundary Rules

1. **No Protocol Adapters in Core**: Services SHALL NOT contain source-specific logic (no code
   for Bloomberg WebSocket, smart meter protocols, or weather APIs inside domain services)

2. **Validation-on-Arrival**: Every observation MUST be validated against the `DataSetDefinition`
   CEL expressions. Invalid data is rejected with `INVALID_ARGUMENT`, not silently fixed

3. **Caller Responsibility**: It is the responsibility of the *caller* (external adapter) to
   structure data into the canonical Protobuf format before calling

4. **No Implicit Transformation**: If data doesn't match the schema, the service does not attempt
   to fix it. The "messy ETL" stays on the external side of the boundary

### Positive Consequences

* **Clean Domain Services**: Core services remain focused on their BIAN responsibility
* **Stable APIs**: Service contracts don't change when external sources change their formats
* **Testable Boundaries**: CEL validation provides deterministic, testable contract enforcement
* **Tenant Flexibility**: Tenants can build custom adapters without modifying Meridian
* **Security**: Malformed data is rejected at the boundary, preventing data quality issues
* **Reusability**: Reference adapters can be shared across deployments

### Negative Consequences

* **More Components**: Requires separate adapter deployment/maintenance for each external source
* **Duplication Risk**: Without good reference implementations, teams might reinvent adapters
* **Operational Complexity**: Adapters need their own monitoring, logging, and error handling

## Pros and Cons of the Options

### Option 1: Universal Ingestion Middleware

Build a centralized ETL service that handles all external connectivity and transformation.

* Good, because it centralizes integration logic
* Good, because it provides a single point for monitoring
* Bad, because it becomes a bottleneck and single point of failure
* Bad, because it requires deep knowledge of every possible source format
* Bad, because it creates tight coupling between unrelated data sources
* Bad, because schema changes in one source could affect others

### Option 2: Source-Specific Adapters in Core Services

Embed adapters (Bloomberg, ECB, etc.) directly in consuming services.

* Good, because it's simple for small numbers of sources
* Bad, because it pollutes domain services with integration concerns
* Bad, because adapter bugs could crash core services
* Bad, because it requires service redeployment when source formats change
* Bad, because it creates dependency on external libraries (Bloomberg SDK, etc.) in core services
* Bad, because it violates BIAN's atomic service design principle

### Option 3: Canonical Ingestion Contract (Chosen)

Define a strict boundary with CEL validation; adapters are external utilities.

* Good, because it cleanly separates domain logic from integration logic
* Good, because adapters can be updated independently of core services
* Good, because CEL validation provides auditable contract enforcement
* Good, because tenants can build custom adapters for proprietary sources
* Good, because it aligns with ADR-0005 (Adapter Pattern)
* Neutral, because it requires more initial setup for reference adapters

## Implementation Guidelines

### Reference Adapter Location

External adapters are placed in `cmd/` as standalone utilities, not in `services/`:

```text
cmd/
├── market-data-tool/           # CLI for bulk imports
│   ├── cmd/
│   │   ├── import.go
│   │   ├── validate.go
│   │   └── schema.go
│   └── adapters/
│       ├── ecb/                # ECB daily rates adapter
│       └── generic/            # CSV/JSON generic adapter
└── ...
```

### CEL Validation as Gatekeeper

The CEL validator in each service is the **Compliance Auditor**:

```go
// In the gRPC handler
func (s *Server) RecordObservation(ctx context.Context, req *pb.RecordObservationRequest) (*pb.RecordObservationResponse, error) {
    // Load dataset definition
    dataset, err := s.repo.GetDataSet(ctx, req.DatasetCode)
    if err != nil {
        return nil, status.Error(codes.NotFound, "dataset not found")
    }

    // Validate against CEL expression - THIS IS THE BOUNDARY
    if err := s.validator.Validate(dataset.ValidationExpression, req); err != nil {
        return nil, status.Errorf(codes.InvalidArgument,
            "validation failed: %s (expression: %s)",
            err.Error(), dataset.ValidationExpression)
    }

    // Only after validation passes do we store
    // ...
}
```

### Error Messages Must Be Helpful

When validation fails, the error should help the adapter developer fix their code:

```text
INVALID_ARGUMENT: validation failed: attribute 'tou_period' is required
  Expression: has(tou_period) && tou_period in ['PEAK', 'OFF_PEAK', 'SHOULDER']
  Provided context: {base_code: "USD", quote_code: "EUR"}
  Missing: tou_period
```

## Links

* [ADR-0005 Adapter Pattern Layer Translation](0005-adapter-pattern-layer-translation.md) -
  Establishes the Port/Adapter pattern this decision extends
* [PRD: Market Information Management](../prd/market-information-management.md) -
  Service Boundaries section defines the specific application
* [BIAN Semantic API Practitioner Guide V8.1](https://bian.org/wp-content/uploads/2024/12/BIAN-Semantic-API-Pactitioner-Guide-V8.1-FINAL.pdf) -
  Service Domain encapsulation principle

## Notes

### When to Reconsider

This decision should be reconsidered if:

* Meridian becomes a general-purpose data integration platform (scope change)
* A specific source requires sub-millisecond latency that adapter overhead can't meet
* Regulatory requirements mandate specific connectivity patterns within the core service

### Applicability Beyond Market Information

This pattern applies to **all** Meridian services accepting external data:

| Service | Canonical Input | External Adapters |
|---------|-----------------|-------------------|
| Market Information | `MarketPriceObservation` | ECB, Bloomberg, weather APIs |
| Position Keeping | `PositionMeasurement` | Smart meters, IoT devices |
| Reference Data | `InstrumentDefinition` | Regulatory feeds, tenant uploads |

The principle remains constant: **Meridian provides the Universal Schema; the external world
provides the Translation.**
