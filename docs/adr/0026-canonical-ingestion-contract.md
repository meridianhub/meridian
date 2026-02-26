---
name: adr-026-canonical-ingestion-contract
description: Keep ETL off critical path; Meridian validates and stores structured data at 100k TPS
triggers:
  - Designing data ingestion from external sources
  - Building adapters for market data, IoT, or third-party feeds
  - Deciding where ETL logic should live
  - Performance concerns about ingestion overhead
  - Scaling decisions for high-throughput data pipelines
instructions: |
  Meridian is a high-performance persistence layer (100k TPS target). Keep messy ETL OFF the
  critical path. Services accept ONLY pre-structured Protobuf - all extraction, transformation,
  and normalisation is the caller's responsibility. This extends hexagonal architecture: Meridian
  defines the Port (structured schema), external systems implement the Adapter (messy translation).
  CEL validation at the boundary enforces the contract. Adapters scale independently from core.
---

# 26. Canonical Ingestion Contract

Date: 2026-01-17

## Status

Accepted

## Context

**Meridian is a high-performance persistence layer for structured financial data.** The design
target is **100,000 transactions per second** - writes to the database and reads from the database
must be blazing fast.

The critical path is simple: **Structured Data → Validation → Storage → Query**

External data sources (market data providers, IoT devices, weather APIs) introduce complexity
that is fundamentally incompatible with this performance goal:

| External Source Characteristic | Impact on Critical Path |
|-------------------------------|------------------------|
| Variable protocols (REST, WebSocket, TCP) | Connection management overhead |
| Diverse formats (XML, JSON, CSV, binary) | Parsing and transformation CPU cost |
| Unpredictable latency (rate limits, timeouts) | Blocking or queueing delays |
| Schema drift (API version changes) | Runtime adaptation logic |
| Scaling requirements (burst handling) | Resource contention with core |

**The question is: Where should the "messy ETL" logic live?**

The answer is driven by a fundamental architectural principle: **Keep slow, unpredictable work
OFF the critical path.** Data ingestion from external sources is inherently slow and variable.
Scaling up or scaling down to handle ingestion load is a separate concern from scaling the
persistence layer.

This extends the **Hexagonal Architecture** (Ports and Adapters) pattern:
- **Meridian defines the Port** (the structured Protobuf schema)
- **External systems implement the Adapter** (the messy translation logic)
- **We are responsible for the structured database, not for adapting the messy external world**

## Decision Drivers

* **Performance (PRIMARY)**: The critical path (validate → store → query) must achieve 100k TPS.
  ETL logic in the critical path would destroy this target.
* **Predictable Latency**: Database writes should have consistent, low latency. External source
  variability (rate limits, connection drops, slow APIs) must not affect core service SLAs.
* **Independent Scaling**: Adapters may need to scale differently than the core. A burst of
  weather data shouldn't compete for resources with payment processing.
* **Hexagonal Architecture**: Meridian defines structured ports; adapters are external. This is
  the classic ports-and-adapters pattern applied to data ingestion.
* **Operational Isolation**: Adapter failures (Bloomberg API down) should not crash or degrade
  core services. Blast radius containment.
* **Tenant Flexibility**: Tenants can build, scale, and operate their own adapters without
  touching Meridian core.

## Considered Options

1. **Universal Ingestion Middleware**: Build a separate ETL service that handles all source
   connectivity, extraction, and transformation
2. **Source-Specific Adapters in Core Services**: Embed Bloomberg, ECB, smart meter adapters
   directly in each consuming service
3. **Canonical Ingestion Contract**: Define a strict boundary - services accept only pre-structured
   Protobuf; adapters are external utilities

## Decision Outcome

Chosen option: **"Canonical Ingestion Contract"**, because it keeps the messy, slow, unpredictable
ETL work OFF the critical path, enabling Meridian to achieve its 100k TPS target while maintaining
data integrity through CEL validation at the boundary.

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
│  • Normalisation                   │   • Bi-Temporal Storage                │
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

* **100k TPS Achievable**: Critical path contains only validation and storage - no ETL overhead
* **Predictable Latency**: Database operations have consistent performance; external variability
  is isolated in adapters
* **Independent Scaling**: Adapters scale separately from core; burst ingestion doesn't starve
  transaction processing
* **Operational Isolation**: Adapter failures don't crash core services; blast radius contained
* **Clean Domain Services**: Core services remain focused on their BIAN responsibility
* **Testable Boundaries**: CEL validation provides deterministic, testable contract enforcement
* **Tenant Flexibility**: Tenants can build custom adapters without modifying Meridian

### Negative Consequences

* **More Components**: Requires separate adapter deployment/maintenance for each external source
* **Adapter Scaling Responsibility**: Tenants must scale their own adapters for burst handling
* **Duplication Risk**: Without good reference implementations, teams might reinvent adapters
* **Operational Complexity**: Adapters need their own monitoring, logging, and error handling

## Pros and Cons of the Options

### Option 1: Universal Ingestion Middleware

Build a centralised ETL service that handles all external connectivity and transformation.

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
* [PRD: Market Information Management](../prd/004-market-information-management.md) -
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
