---
name: adr-002-microservices-per-bian-domain
description: One microservice per BIAN domain for independent scaling, deployment, and failure isolation
triggers:

  - Designing service boundaries
  - Deciding between microservices vs monolith
  - Planning service deployment architecture
  - Discussing BIAN domain implementation

instructions: |
  Create one service per BIAN domain (FinancialAccounting, PositionKeeping, CurrentAccount).
  Each service independently deployable with own database. Use gRPC for sync communication,
  Kafka for async events. Services are "lego blocks" for composability.
---

# 2. Microservices Architecture with One Service per BIAN Domain

Date: 2025-10-25

## Status

Accepted

Amended: 2025-11-19 - Added saga orchestration pattern and service coupling enforcement rules

## Context

Meridian implements multiple BIAN (Banking Industry Architecture Network) service domains: FinancialAccounting,
PositionKeeping, and CurrentAccount. We need to decide whether to build a modular monolith with all domains in one
deployable or separate microservices with one service per BIAN domain.

BIAN service domains already define clear bounded contexts with well-defined interfaces, making them natural candidates
for service boundaries.

## Decision Drivers

* BIAN domains have distinct scaling requirements (CurrentAccount serves high-volume customer operations,
FinancialAccounting handles periodic ledger posting)
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
* Failure in one domain (e.g., financial accounting) does not impact critical operations (e.g., transaction logging)
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

One deployable service for each BIAN domain: financial-accounting-service, position-keeping-service,
current-account-service, etc.

* Good, because BIAN domains already define bounded contexts with clear interfaces
* Good, because enables independent scaling (CurrentAccount may need 10x instances vs FinancialAccounting)
* Good, because failure isolation prevents cascading failures
* Good, because teams can own and deploy domains independently
* Good, because aligns with "lego block" composability vision
* Bad, because distributed transactions require Saga pattern
* Bad, because operational overhead of multiple services
* Bad, because network latency between services

### Modular Monolith - Single Deployable

All BIAN domains in one binary with internal module boundaries (internal/financial-accounting/,
internal/position-keeping/, etc.)

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
* [GitHub Issue #1: Infrastructure](https://github.com/meridianhub/meridian/issues/1)
* [GitHub Issue #3: Platform Services](https://github.com/meridianhub/meridian/issues/3)

## Notes

### Service Structure

Each BIAN domain service will follow this structure:

```text
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

```text
platform/
├── database/        # Connection pooling, transaction management
├── kafka/           # Producer/consumer utilities with protobuf serialisation
├── auth/            # JWT validation, authorisation
├── observability/   # OpenTelemetry, logging, metrics
└── idempotency/     # Redis-based idempotency keys
```

### Inter-Service Communication

* Synchronous: gRPC with Protobuf (leveraging existing API contracts)
* Asynchronous: Kafka events with protobuf serialisation (validated via `buf breaking` in CI)
* Service discovery: Kubernetes DNS
* Load balancing: Kubernetes Service resources + gRPC client-side load balancing

### Future Considerations

* Consider service mesh (Istio, Linkerd) when cross-cutting concerns grow
* May need API gateway for external clients (Kong, Ambassador)
* Watch for chatty inter-service communication patterns
* Re-evaluate if distributed transaction complexity becomes unmanageable

## Amendment: Saga Orchestration Pattern (2025-11-19)

### Context

The original ADR mentioned "distributed transactions require Saga pattern or 2PC" but didn't specify which approach. After implementing service boundaries and analyzing transaction flows, we need to formalize the distributed transaction pattern.

**Key observations:**

* CurrentAccount naturally coordinates transactions across FinancialAccounting and PositionKeeping
* Transactions require multi-step workflows with compensation (deposit → position update → ledger posting)
* Business logic for coordination belongs in the domain service, not infrastructure
* BIAN service domains have clear orchestration patterns (e.g., CurrentAccount.ExecuteDeposit coordinates multiple services)

### Decision

Use **orchestration-based saga pattern** with CurrentAccount as the orchestrator for multi-service transactions.

**Pattern:**

```text
CurrentAccount (Orchestrator)
  ├─> PositionKeeping.RecordTransaction (step 1)
  ├─> FinancialAccounting.CapturePosting (step 2)
  └─> Publish AccountTransactionCompletedEvent (step 3)

On failure at any step:
  └─> Execute compensation (e.g., ReverseTransaction)
```

**Rationale:**

* **Explicit control flow**: Orchestrator explicitly calls each service step-by-step
* **Business logic visibility**: Saga logic lives in CurrentAccount domain code, not infrastructure
* **Debugging**: Clear call stack for transaction flow
* **BIAN alignment**: BIAN's "Execute" behaviour qualifiers map naturally to orchestration
* **Error handling**: Centralised compensation logic in orchestrator

### Alternatives Considered

#### 1. Choreography-Based Saga

Services react to events without central coordinator:

```text
CurrentAccount publishes → TransactionInitiatedEvent
PositionKeeping consumes → Updates position → Publishes PositionUpdatedEvent
FinancialAccounting consumes → Posts ledger → Publishes PostingCompletedEvent
```

**Pros:**

* Loose coupling (no direct service dependencies)
* Services independently scalable and deployable

**Cons:**

* **Implicit control flow**: Transaction logic scattered across event handlers
* **Debugging complexity**: Hard to trace transaction flow across events
* **Compensation complexity**: Compensating transactions require complex event choreography
* **BIAN mismatch**: BIAN's orchestration patterns don't map to choreography well

**Why rejected:** Debugging and maintaining distributed transaction flows is significantly harder with choreography. The loose coupling benefit doesn't outweigh the operational complexity for our 3-service architecture.

#### 2. Two-Phase Commit (2PC)

Use distributed transaction coordinator (XA transactions):

**Pros:**

* ACID guarantees across services
* Simplified application code

**Cons:**

* **Blocking protocol**: Coordinator failure blocks all participants
* **Database coupling**: Requires XA-compatible databases
* **Performance**: Significantly slower than saga patterns
* **Availability impact**: Reduces system availability (CAP theorem)

**Why rejected:** 2PC's blocking nature and availability impact are unacceptable for financial transaction processing. Saga patterns provide better availability with acceptable consistency guarantees.

### Implementation Guidelines

**Orchestrator responsibilities:**

1. Execute saga steps sequentially
2. Handle partial failures with compensation
3. Publish domain events after successful completion
4. Maintain idempotency (retry safety)

**Example: Deposit Transaction Saga**

```go
func (s *CurrentAccountService) ExecuteDeposit(ctx context.Context, req *pb.ExecuteDepositRequest) error {
    // Step 1: Record position
    positionResp, err := s.positionKeepingClient.RecordTransaction(ctx, &pkpb.RecordTransactionRequest{
        AccountId: req.AccountId,
        Amount:    req.Amount,
        Type:      "DEPOSIT",
    })
    if err != nil {
        return fmt.Errorf("position keeping failed: %w", err)
    }

    // Step 2: Post to ledger
    _, err = s.financialAccountingClient.CapturePosting(ctx, &fapb.CapturePostingRequest{
        AccountId:     req.AccountId,
        Amount:        req.Amount,
        TransactionId: positionResp.TransactionId,
    })
    if err != nil {
        // Compensate: Reverse position
        s.positionKeepingClient.ReverseTransaction(ctx, &pkpb.ReverseTransactionRequest{
            TransactionId: positionResp.TransactionId,
        })
        return fmt.Errorf("ledger posting failed: %w", err)
    }

    // Step 3: Publish completion event
    s.eventPublisher.Publish(ctx, &events.AccountTransactionCompletedEvent{
        AccountId:     req.AccountId,
        TransactionId: positionResp.TransactionId,
        Amount:        req.Amount,
    })

    return nil
}
```

**Compensation strategies:**

* **Semantic compensation**: Business-level reversal (e.g., ReverseTransaction)
* **Idempotency**: All operations must be retry-safe
* **Timeout handling**: Circuit breakers for downstream services
* **Audit trail**: Log all saga steps for debugging

### Consequences

**Positive:**

* ✅ Clear ownership: CurrentAccount owns transaction coordination logic
* ✅ Debuggability: Single call stack for transaction flow
* ✅ BIAN alignment: Maps naturally to BIAN's orchestration patterns
* ✅ Testability: Orchestrator logic is unit-testable
* ✅ Monitoring: Centralised metrics for transaction success/failure

**Negative:**

* ❌ Service coupling: CurrentAccount depends on FA and PK gRPC clients
* ❌ Single point of failure: Orchestrator failure blocks transactions
* ❌ Scaling: Orchestrator can become bottleneck

**Mitigations:**

* **Coupling**: Acceptable for 3-service architecture; use proto contracts
* **Availability**: Deploy CurrentAccount with high availability (3+ replicas)
* **Scaling**: Orchestration is CPU-light; horizontal scaling is straightforward

### Related Patterns

* **Outbox Pattern**: Ensure reliable event publishing after saga completion (see ADR-004 Amendment)
* **Circuit Breaker**: Prevent cascading failures in orchestrator
* **Idempotency**: All saga steps must be retry-safe

### References

* [Service Boundaries Documentation](../architecture/bian-service-boundaries.md)
* [CurrentAccount API Contract](../architecture/api-contracts/current-account-contract.md#saga-orchestration)
* [Event-Driven Architecture](../architecture/event-driven-architecture.md)

---

## Amendment: Service Coupling Enforcement Rules (2025-11-19)

### Context

While ADR-002 established microservices per BIAN domain, it didn't specify how to **enforce** service boundaries. After auditing the codebase (Task 14), we formalized 5 concrete rules to maintain proper service coupling.

**Audit findings (2025-11-19):**

* ✅ 0 P0 violations: No cross-service internal imports
* ❌ 17 P1 violations: Platform code in `internal/platform/` should be in `pkg/platform/`
* ✅ Proto-only inter-service dependencies (14 safe imports)

### Decision

Enforce service boundaries with **5 explicit dependency rules** validated through linting, CI, and code review.

#### Rule 1: Proto-Only Inter-Service Communication

**Rule:** Services MUST communicate only via gRPC (proto contracts) or Kafka events. Direct Go package imports across services are forbidden.

**Allowed:**

```go
import "github.com/meridianhub/meridian/api/proto/financial_accounting/v1"
```

**Forbidden:**

```go
import "github.com/meridianhub/meridian/internal/financial-accounting/domain"
```

**Enforcement:**

* Linter: Custom rule to detect `internal/<other-service>/` imports
* CI: Automated coupling analysis (see `scripts/analyze-coupling.sh`)
* Code review: Reject PRs with cross-service internal imports

**Rationale:** Proto contracts are the public API. Internal packages can change without breaking other services.

#### Rule 2: Platform Code in pkg/, Not internal/

**Rule:** Shared platform utilities (observability, Kafka, database) MUST be in `pkg/platform/`, not `internal/platform/`.

**Allowed:**

```go
import "github.com/meridianhub/meridian/pkg/platform/observability"
```

**Forbidden:**

```go
import "github.com/meridianhub/meridian/internal/platform/observability"
```

**Enforcement:**

* Migration: Move `internal/platform/` → `pkg/platform/` (see [Boundary Migration Plan](../architecture/boundary-migration-plan.md))
* Linter: Warn on `internal/platform/` imports from services
* CI: Track platform coupling metrics

**Rationale:** `internal/` signals "private to this service." Platform code shared across services must be in `pkg/`.

#### Rule 3: Own Your Domain Entities

**Rule:** Each service MUST own its domain entities. No shared domain models across services.

**Entity ownership matrix:**

* CurrentAccount owns: Account, Transaction (orchestration)
* FinancialAccounting owns: FinancialBookingLog, LedgerPosting, ChartOfAccounts
* PositionKeeping owns: PositionLog, CashPosition, SecurityPosition

**Forbidden:**

```go
// In FinancialAccounting service
import "github.com/meridianhub/meridian/internal/current-account/domain"

func PostToLedger(account *domain.Account) { ... } // ❌ Using another service's domain model
```

**Allowed:**

```go
// Use proto messages to exchange data
func PostToLedger(accountId string, amount decimal.Decimal) { ... } // ✅ Primitive types
```

**Enforcement:**

* Code review: Reject shared domain model imports
* Architecture documentation: 19-entity ownership matrix (see [Service Boundaries](../architecture/bian-service-boundaries.md#entity-ownership-matrix))

**Rationale:** BIAN domains are bounded contexts. Each service's domain model evolves independently.

#### Rule 4: Database-Per-Service

**Rule:** Each service MUST have its own database schema. No shared tables across services.

**Schema ownership:**

* `financial_accounting` database: Tables owned by FinancialAccounting service
* `position_keeping` database: Tables owned by PositionKeeping service
* `current_account` database: Tables owned by CurrentAccount service

**Forbidden:**

```sql
-- In FinancialAccounting service migrations
SELECT * FROM position_keeping.position_log; -- ❌ Cross-database query
```

**Enforcement:**

* Database migrations: Each service has its own migration directory
* Schema review: Reject migrations that reference other service schemas
* Connection strings: Services only have credentials for their own database

**Rationale:** Database-per-service enables independent scaling, deployment, and schema evolution.

#### Rule 5: Events for Async, gRPC for Sync

**Rule:** Use Kafka events for asynchronous coordination, gRPC for synchronous request/response.

**Async (Kafka):**

* State change notifications (AccountCreatedEvent, TransactionCompletedEvent)
* Fire-and-forget operations
* Event-driven workflows

**Sync (gRPC):**

* Read operations (GetAccount, RetrievePosting)
* Orchestrated transactions (ExecuteDeposit → RecordTransaction → CapturePosting)
* Request/response with immediate result

**Anti-pattern:**

```go
// ❌ Using events for synchronous orchestration
publisher.Publish(TransactionInitiatedEvent)
// ... wait for PositionUpdatedEvent ...  // Race condition!
// ... wait for PostingCompletedEvent ... // Complex choreography
```

**Correct pattern:**

```go
// ✅ Use gRPC for orchestrated saga
positionResp, err := positionClient.RecordTransaction(ctx, req)
postingResp, err := accountingClient.CapturePosting(ctx, req)
```

**Enforcement:**

* Architecture review: Ensure pattern fits use case (sync vs async)
* Code review: Check for event-based request/response anti-patterns

**Rationale:** gRPC provides strong contracts and immediate feedback for orchestration. Events are for eventual consistency and decoupling.

### Validation Tooling

#### scripts/analyze-coupling.sh

Automated coupling analysis:

```bash
./scripts/analyze-coupling.sh > coupling-report.json
```

**Detects:**

* Cross-service internal imports (P0 violations)
* Internal platform imports (P1 violations)
* Proto dependencies (safe)
* gRPC client instantiation
* Kafka event patterns

#### scripts/calculate-coupling-metrics.sh

Calculates coupling metrics:

```bash
./scripts/calculate-coupling-metrics.sh
```

**Metrics:**

* Afferent Coupling (Ca): Services depending on this service
* Efferent Coupling (Ce): Services this service depends on
* Instability (I): Ce / (Ca + Ce) - measures resistance to change
* Assessment: stable, acceptable, too-dependent, too-rigid

**Current metrics (2025-11-19):**

* CurrentAccount: I=1.0 (too-dependent) - orchestrator pattern, acceptable
* FinancialAccounting: I=0.0 (stable) - pure provider
* PositionKeeping: I=0.0 (stable) - pure provider

### Consequences

**Positive:**

* ✅ Explicit rules make boundaries enforceable
* ✅ Automated validation catches violations in CI
* ✅ Coupling metrics track architectural health over time
* ✅ Clear ownership matrix prevents domain model conflicts

**Negative:**

* ❌ Additional CI overhead for coupling analysis (~30s)
* ❌ Platform code migration required (17 files, 5 story points)

**Mitigations:**

* **CI performance**: Cache coupling analysis results
* **Migration**: Phased approach over 2-3 weeks (see [Migration Plan](../architecture/boundary-migration-plan.md))

### References

* [Service Coupling Analysis](../architecture/service-coupling-analysis.md)
* [BIAN Service Boundaries](../architecture/bian-service-boundaries.md)
* [Boundary Migration Plan](../architecture/boundary-migration-plan.md)
* Coupling analysis scripts: `scripts/analyze-coupling.sh`, `scripts/calculate-coupling-metrics.sh`

---

## Amendment: Database Architecture Implementation (2025-12-16)

### Context

ADR-002 specified database-per-service in Rule 4, but didn't detail the actual database architecture. After implementing database migrations (Task Master: database-per-service), we formalized the production database structure.

### Decision

Each microservice has its own CockroachDB database with tenant isolation via schemas:

**Database naming:**

| Service | Database |
|---------|----------|
| Tenant Service | `meridian_platform` |
| Current Account | `meridian_current_account` |
| Financial Accounting | `meridian_financial_accounting` |
| Position Keeping | `meridian_position_keeping` |
| Payment Order | `meridian_payment_order` |
| Party | `meridian_party` |

### Multi-Tenancy Architecture

**Schema-per-tenant within each service database:**

```text
Database: meridian_current_account
  └── Schema: org_acme_bank        (tenant-specific)
       └── Tables: account, lien, audit_log
  └── Schema: org_demo_corp        (tenant-specific)
       └── Tables: account, lien, audit_log

Database: meridian_party
  └── Schema: org_acme_bank
       └── Tables: party
  └── Schema: org_demo_corp
       └── Tables: party
```

**Tenant routing via `search_path`:**

```go
// Connection URL includes tenant schema
connStr := fmt.Sprintf(
    "postgres://%s:%s@%s/%s?search_path=%s",
    user, password, host, database, tenantSchema,
)
// Queries use unqualified table names
db.Query("SELECT * FROM account WHERE id = $1", accountID)
// PostgreSQL resolves via search_path: org_acme_bank.account
```

### Table Naming Conventions

**Singular nouns, unqualified:**

| Pattern | Example | Rationale |
|---------|---------|-----------|
| Singular | `account` (not `accounts`) | Natural in queries: `SELECT * FROM account` |
| Unqualified | No schema prefix | Enables transparent tenant routing |
| Snake_case | `payment_order`, `audit_trail_entry` | Consistent with SQL conventions |

**Compound naming follows `<context>_<entity>`:**

- `payment_order` - an order for payment
- `ledger_posting` - a posting to a ledger
- `financial_booking_log` - a log entry for financial bookings

### Database Access Control

**Principle of least privilege:**

```sql
-- Each service has dedicated database user
CREATE USER current_account_svc WITH PASSWORD '...';

-- User only has access to its own database
GRANT ALL ON DATABASE meridian_current_account TO current_account_svc;

-- No cross-database access (CockroachDB enforces this)
-- current_account_svc CANNOT access meridian_party
```

**Cross-service data access:**

- **Allowed**: gRPC calls between services
- **Forbidden**: SQL queries to other service databases

### Migration Strategy

See [Database-Per-Service Migration Runbook](../runbooks/database-per-service-migration.md) for:

- Step-by-step migration guide
- Rollback procedures
- Verification checklists
- Lessons learned

### Consequences

**Positive:**

* ✅ True database-level isolation (not just schema)
* ✅ Independent scaling per service database
* ✅ Service failure cannot corrupt other service data
* ✅ Clear audit boundaries per BIAN domain
* ✅ Simplified backup/restore per service

**Negative:**

* ❌ More databases to manage (6 instead of 1)
* ❌ Cannot JOIN across services (must use gRPC)
* ❌ Distributed transactions require saga pattern

### References

* [ADR-003: Database Schema Migrations](./0003-database-schema-migrations.md)
* [Migration Runbook](../runbooks/database-per-service-migration.md)
