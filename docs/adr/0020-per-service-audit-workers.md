---
name: adr-020-per-service-audit-workers
description: Each microservice runs its own embedded audit worker to maintain bounded context isolation
triggers:

  - Designing audit worker deployment strategy
  - Discussing audit processing architecture
  - Planning multi-service audit infrastructure
  - Evaluating cross-service database access patterns

instructions: |
  Embed audit workers as background goroutines within each service, not as a centralized service.
  Each service processes its own audit_outbox table using the shared/platform/audit package.
  Pass the service's schema name to NewAuditWorker for proper table targeting.
---

# 20. Per-Service Audit Workers

Date: 2025-12-18

## Status

Superseded

**Note:** This ADR proposed per-service embedded workers. The actual implementation (2025-12-24) uses a dual-path approach:
- **Primary path**: Kafka audit consumers (one deployment per service, deployed separately)
- **Fallback path**: Centralized audit-worker service processes outbox when Kafka unavailable

See implementation details in ADR-0009 and `/services/README.md`.

**Disposition verified 2026-03-06**: SUPERSEDED status is accurate. The per-service embedded worker
approach described here was not adopted. The implementation phases (Refactor Worker, Embed Workers,
Deprecate Centralized) are all CANCELLED in favor of the Kafka-based dual-path architecture.

## Context

ADR-0009 established the transactional outbox pattern for application-level audit logging. The current implementation (`services/audit-worker/cmd/main.go`) deploys a centralized audit-worker service that connects to a single database and processes audit_outbox entries.

As the platform grows to 6 services with separate database schemas (current-account, position-keeping, financial-accounting, party, payment-order, tenant), we must decide how to scale audit processing:

1. **Centralized approach**: Extend the existing audit-worker to poll 6 different service schemas
2. **Per-service approach**: Embed audit workers as background goroutines within each service

This decision has significant implications for bounded context isolation, service autonomy, and operational complexity.

## Decision Drivers

* **ADR-0002 Compliance**: Microservices per BIAN domain requires database-per-service and no cross-service database access
* **Service Coupling Analysis**: Zero cross-service database access violations is a key compliance criterion
* **Service Autonomy**: Services should not depend on external workers for core audit functionality
* **Operational Simplicity**: Minimize credentials management and deployment coupling
* **Performance**: Each service handles its own audit volume without contention

## Considered Options

1. Centralized audit-worker service polling multiple schemas
2. Per-service embedded audit workers
3. Hybrid approach with centralized worker for high-volume services

## Decision Outcome

Chosen option: "Per-service embedded audit workers", because it maintains bounded context isolation, eliminates cross-service database access, and aligns with ADR-0002's microservices principles.

### Positive Consequences

* Each service owns its complete audit lifecycle (write to outbox, process to audit_log)
* No cross-service database credentials required
* Service autonomy: audit processing continues independently if other services fail
* Independent deployment: audit logic changes deploy with the owning service
* Simpler monitoring: per-service metrics naturally scoped
* Aligns with service coupling analysis findings (zero database access violations)

### Negative Consequences

* Duplicated worker goroutines across 6 services (minimal overhead ~1MB per worker)
* No single dashboard for aggregate audit health (requires metric aggregation)
* Worker startup code added to each service's main.go

## Pros and Cons of the Options

### Centralized Audit-Worker (Current Pattern Extended)

A single audit-worker service connects to all 6 service databases and polls their audit_outbox tables.

* Good, because single deployment for audit processing logic
* Good, because centralized monitoring (one place to check audit lag)
* Good, because no changes to existing service binaries
* Bad, because **violates bounded context isolation** (cross-service database access)
* Bad, because **operational coupling** (all services depend on audit-worker availability)
* Bad, because **credentials complexity** (audit-worker needs 6 database connection strings)
* Bad, because **deployment coupling** (audit logic changes require audit-worker deployment)
* Bad, because **contradicts ADR-0002 Rule 4** (database-per-service)
* Bad, because creates a single point of failure for audit processing across all services

### Per-Service Embedded Audit Workers (Selected)

Each service runs its own background goroutine processing its local audit_outbox table, using the shared `shared/platform/audit` package.

```go
// Example: services/party/cmd/main.go
auditWorker := audit.NewAuditWorker(db, "party_audit", logger)
workerCtx, workerCancel := context.WithCancel(ctx)
auditWorker.Start(workerCtx)
// Worker processes only party_audit.audit_outbox
```

* Good, because **maintains bounded context isolation** (no cross-service database access)
* Good, because **service autonomy** (each service controls its audit processing lifecycle)
* Good, because **independent deployment** (audit logic changes deploy with owning service)
* Good, because **simpler credentials** (each service already has its own database connection)
* Good, because **aligns with ADR-0002** (microservices per BIAN domain)
* Good, because **failure isolation** (one service's audit issues don't affect others)
* Bad, because duplicated worker goroutines (minimal memory overhead ~1MB each)
* Bad, because requires metric aggregation for centralized monitoring
* Bad, because worker startup code duplicated across 6 service main.go files

### Hybrid Approach

High-volume services (current-account, payment-order) use embedded workers; low-volume services share a centralized worker.

* Good, because reduces total worker count
* Bad, because **inconsistent architecture** (some services isolated, some not)
* Bad, because **arbitrary boundary** (difficult to justify which services share)
* Bad, because **partial ADR-0002 violation** (still has cross-service database access)
* Bad, because **complicates operational model** (two patterns to understand and maintain)

## Implementation

### Phase 1: Refactor Worker for Per-Service Use

Update `shared/platform/audit/worker.go` to accept schema parameter:

```go
// NewAuditWorker creates a worker for a specific service schema
func NewAuditWorker(db *gorm.DB, schema string, logger *slog.Logger) *Worker {
    return &Worker{
        db:     db,
        schema: schema,  // e.g., "party_audit", "current_account_audit"
        logger: logger,
        // ... existing configuration
    }
}

// Internal queries use the schema parameter
func (w *Worker) processBatch(ctx context.Context) error {
    w.db.Table(w.schema + ".audit_outbox").Where("status = ?", "pending")...
}
```

### Phase 2: Embed Workers in Services

Add worker startup to each service's `main.go`:

| Service | Schema | Worker Integration |
|---------|--------|-------------------|
| current-account | `current_account_audit` | `services/current-account/cmd/main.go` |
| position-keeping | `position_keeping_audit` | `services/position-keeping/cmd/main.go` |
| financial-accounting | `financial_accounting_audit` | `services/financial-accounting/cmd/main.go` |
| party | `party_audit` | `services/party/cmd/main.go` |
| payment-order | `payment_order_audit` | `services/payment-order/cmd/main.go` |
| tenant | `tenant_audit` | `services/tenant/cmd/main.go` |

### Phase 3: Deprecate Centralized Audit-Worker

1. Deploy per-service workers to staging
2. Monitor for 1 week to verify no audit lag or processing issues
3. Remove `services/audit-worker` service
4. Update Kubernetes manifests to remove audit-worker deployment

### Monitoring Strategy

**Per-service metrics** (automatically labeled with service name):

```
{service}_audit_worker_outbox_depth_total
{service}_audit_worker_entries_processed_total
{service}_audit_worker_entries_failed_total
{service}_audit_worker_processing_duration_seconds
```

**Aggregate Grafana dashboard**:

* Sum outbox depth across all services
* Alert: Any service's outbox depth > 1000 for > 5 minutes
* Per-service drill-down panels

## Impact on Related Work

### Task 16 (Multi-schema processing)

**Status**: Superseded by this decision

Task 16 proposed extending the centralized audit-worker to poll 6 schemas. This decision replaces that approach with per-service workers. Task 16 should be updated to "Embed audit workers in individual services" or closed as superseded.

### Task 6 (Centralized Kafka audit consumer)

**Status**: Still valid

The Kafka audit consumer remains centralized for cross-service audit aggregation and analytics. This is architecturally distinct from outbox processing:

| Component | Scope | Purpose |
|-----------|-------|---------|
| Per-service audit worker | Single service | Process outbox → audit_log |
| Kafka audit consumer | Cross-service | Aggregate audit events for analytics |

The per-service workers publish events to Kafka after processing; the centralized consumer aggregates these events.

## ADR Alignment

| ADR | Alignment |
|-----|-----------|
| ADR-0002 (Microservices Per BIAN Domain) | Maintains database independence per service |
| ADR-0002 Amendment (Service Coupling Rules) | Enforces Rule 4: Database-per-service |
| ADR-0009 (Application-Level Audit Logging) | Preserves transactional outbox pattern |
| Service Coupling Analysis | Eliminates potential cross-service database access violation |

## Links

* [ADR-0002: Microservices Per BIAN Domain](./0002-microservices-per-bian-domain.md)
* [ADR-0009: Application-Level Audit Logging](./0009-application-level-audit-logging.md)
* [Service Coupling Analysis](../architecture/service-coupling-analysis.md)
* [Shared Audit Worker Package](../../shared/platform/audit/worker.go)

## Notes

### Decision Criteria Checklist

Choose per-service workers (this decision) if:

- [x] Service autonomy and bounded context isolation are architectural priorities
- [x] Team values alignment with microservices principles
- [x] Service-specific audit processing needs may diverge in future
- [x] Operational simplicity (single database connection per service) is preferred

Choose centralized worker if:

- [ ] Operational simplicity (single deployment) outweighs architectural purity
- [ ] Team has strong operational automation for multi-service database access
- [ ] Audit requirements are guaranteed to remain identical across all services

### Future Considerations

* If audit volume grows significantly, consider dedicated audit worker pods per service (separate from main service pods)
* If audit requirements diverge per service, the per-service architecture enables independent customization
* Consider adding circuit breakers if audit processing affects service health under load
