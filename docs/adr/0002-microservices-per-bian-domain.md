# 2. Microservices Architecture with One Service per BIAN Domain

Date: 2025-10-25

## Status

Accepted

## Context

Meridian implements multiple BIAN (Banking Industry Architecture Network) service domains: FinancialAccounting, PositionKeeping, CurrentAccount, and AccountReconciliation. We need to decide whether to build a modular monolith with all domains in one deployable or separate microservices with one service per BIAN domain.

BIAN service domains already define clear bounded contexts with well-defined interfaces, making them natural candidates for service boundaries.

## Decision Drivers

* BIAN domains have distinct scaling requirements (CurrentAccount serves high-volume customer operations, FinancialAccounting handles periodic ledger posting)
* Failure isolation is critical for financial services (one domain failing should not cascade)
* Independent deployment cycles per domain enable faster iteration
* Team ownership can align with BIAN domain boundaries
* Financial services benefit from explicit service boundaries for audit and compliance
* Need for "lego block" composability - services should be independently deployable and replaceable

## Considered Options

1. Microservices - One service per BIAN domain
2. Modular Monolith - All domains in single deployable with internal module boundaries
3. Hybrid - Core domains (GL, Transaction Log) in monolith, customer-facing domains as services

## Decision Outcome

Chosen option: "Microservices - One service per BIAN domain", because:

* BIAN domains map perfectly to microservice boundaries (bounded contexts already defined)
* Enables independent scaling, deployment, and failure isolation per domain
* Aligns with "lego block" composability vision
* Easier to start with proper boundaries than retrofit distributed transactions later
* Financial services architecture benefits from explicit service isolation for compliance

### Positive Consequences

* Each BIAN domain can scale independently based on load
* Failure in one domain (e.g., reconciliation) does not impact critical operations (e.g., transaction logging)
* Teams can own and deploy individual domains independently
* Technology choices can vary per service if needed (though we'll standardize on Go/gRPC initially)
* Clear audit boundaries aligned with BIAN specification
* Services are composable "lego blocks" that can be deployed in different configurations

### Negative Consequences

* Increased operational complexity (6+ services to deploy and monitor)
* Distributed transactions require Saga pattern or 2PC where needed
* Network latency between services (though all communication is gRPC)
* Service mesh or API gateway required for cross-cutting concerns
* More complex local development setup (mitigated by Tilt)

## Pros and Cons of the Options

### Microservices - One Service per BIAN Domain

One deployable service for each BIAN domain: financial-accounting-service, position-keeping-service, current-account-service, etc.

* Good, because BIAN domains already define bounded contexts with clear interfaces
* Good, because enables independent scaling (CurrentAccount may need 10x instances vs FinancialAccounting)
* Good, because failure isolation prevents cascading failures
* Good, because teams can own and deploy domains independently
* Good, because aligns with "lego block" composability vision
* Bad, because distributed transactions require Saga pattern
* Bad, because operational overhead of multiple services
* Bad, because network latency between services

### Modular Monolith - Single Deployable

All BIAN domains in one binary with internal module boundaries (internal/financial-accounting/, internal/position-keeping/, etc.)

* Good, because simpler deployment (one binary)
* Good, because ACID transactions across all domains
* Good, because lower operational complexity
* Good, because can extract to microservices later
* Bad, because all domains scale together (cannot scale CurrentAccount independently)
* Bad, because deployment coupling (change in one domain requires redeploying all)
* Bad, because failure in one domain can impact entire system
* Bad, because harder to retrofit distributed transactions if extracted later
* Bad, because does not align with "lego block" composability vision

### Hybrid - Core Monolith with Customer-Facing Services

Core domains (FinancialAccounting, PositionKeeping) in monolith, customer-facing domains (CurrentAccount) as services.

* Good, because reduces number of services
* Good, because ACID transactions for core ledger operations
* Bad, because creates arbitrary boundary (BIAN domains are the natural boundary)
* Bad, because still requires distributed transaction patterns
* Bad, because unclear which domains belong where
* Bad, because does not leverage BIAN's pre-defined service boundaries

## Links

* [BIAN Service Landscape](https://bian.org/servicelandscape/)
* [BIAN Semantic APIs](../../../bian/bian-public-main/release13.0.0/semantic-apis/)
* [GitHub Issue #1: Infrastructure](https://github.com/bjcoombs/meridian/issues/1)
* [GitHub Issue #3: Platform Services](https://github.com/bjcoombs/meridian/issues/3)

## Notes

### Service Structure

Each BIAN domain service will follow this structure:

```
services/
├── financial-accounting-service/
│   ├── cmd/server/main.go
│   ├── internal/
│   │   ├── domain/          # BIAN domain model
│   │   ├── repository/      # Database persistence
│   │   ├── grpc/           # gRPC service implementation
│   │   └── kafka/          # Event publishing
│   ├── migrations/          # Flyway database migrations
│   ├── Dockerfile
│   └── go.mod
├── position-keeping-service/
│   └── ...
└── current-account-service/
    └── ...
```

### Shared Platform Services

Common platform services (database, Kafka, auth, observability) will be in:

```
platform/
├── database/        # Connection pooling, transaction management
├── kafka/           # Producer/consumer utilities, schema registry client
├── auth/            # JWT validation, authorization
├── observability/   # OpenTelemetry, logging, metrics
└── idempotency/     # Redis-based idempotency keys
```

### Inter-Service Communication

* Synchronous: gRPC with Protobuf (leveraging existing API contracts)
* Asynchronous: Kafka events with Schema Registry validation
* Service discovery: Kubernetes DNS
* Load balancing: Kubernetes Service resources + gRPC client-side load balancing

### Future Considerations

* Consider service mesh (Istio, Linkerd) when cross-cutting concerns grow
* May need API gateway for external clients (Kong, Ambassador)
* Watch for chatty inter-service communication patterns
* Re-evaluate if distributed transaction complexity becomes unmanageable
