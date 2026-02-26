# Audit System End-to-End Integration Tests

This directory contains comprehensive end-to-end integration tests for the multi-service audit system. These tests
validate the complete audit flow from service operations to tenant audit_log tables with per-service bounded context
enforcement.

## Overview

The audit system is designed with the following architecture:

- **Per-Service Kafka Topics**: Each service publishes audit events to its own topic (e.g., `audit.events.current-account`)
- **Per-Service Audit Consumers**: Each service has a dedicated audit consumer deployment that reads from its topic
- **Per-Service Databases**: Each consumer writes to its service's database (e.g., `meridian_current_account`)
- **Tenant Isolation**: Within each database, tenant data is isolated using PostgreSQL schemas (e.g., `org_tenant_a`)
- **Bounded Contexts**: Each consumer can only write to its service's database, enforcing domain boundaries

## Test Infrastructure

### Test Containers

The E2E tests use testcontainers to spin up real infrastructure:

- **PostgreSQL**: 3 separate databases (one per service: current-account, financial-accounting, position-keeping)
- **No Kafka**: Tests focus on the TenantAuditWriter component directly to avoid Kafka complexity

### Test Approach

Rather than testing the full Kafka consumer flow (which would require complex testcontainer setup), these tests:

1. Create separate PostgreSQL databases for each service
2. Instantiate `TenantAuditWriter` for each database
3. Directly call `WriteAuditEvent` with tenant context
4. Verify writes land in correct tenant schemas in correct service databases

This approach validates the core functionality:

- Multi-tenant writes across services
- Bounded context enforcement (each writer bound to its database)
- Tenant isolation within each database
- Independent failure scenarios
- Audit trail completeness across services

## Test Scenarios

### 1. Multi-Service, Multi-Tenant Writes

**File**: `audit_writer_e2e_test.go` - `TestMultiServiceMultiTenantWrites`

Validates that:

- Tenant A can write to all 3 services and data is isolated
- Tenant B can write to all 3 services and data is isolated
- Each tenant's data is isolated from other tenants within each service database

**Assertions**:

- 6 total writes (2 tenants × 3 services) land in correct locations
- Tenant A sees only its own events in each service
- Tenant B sees only its own events in each service

### 2. Bounded Context Enforcement

**File**: `audit_writer_e2e_test.go` - `TestBoundedContextEnforcement`

Validates that:

- Each `TenantAuditWriter` is bound to exactly one database
- Current-account writer writes to `meridian_current_account` only
- Financial-accounting writer writes to `meridian_financial_accounting` only
- Position-keeping writer writes to `meridian_position_keeping` only

**Assertions**:

- Each writer uses separate database connection pools
- Events written to current-account do not appear in financial-accounting or position-keeping
- Database-per-service rule (ADR-0002) is enforced at the audit layer

### 3. Independent Failure Scenarios

**File**: `audit_writer_e2e_test.go` - `TestIndependentFailureScenarios`

Validates that:

- Failure in one service (e.g., missing tenant schema) doesn't block other services
- Current-account continues writing if financial-accounting fails
- Position-keeping continues writing if financial-accounting fails

**Assertions**:

- Write to current-account succeeds
- Write to financial-accounting fails (schema doesn't exist)
- Write to position-keeping succeeds
- Successful services have expected audit entries

### 4. Audit Trail Completeness

**File**: `audit_writer_e2e_test.go` - `TestAuditTrailCompleteness`

Validates that:

- A cross-service transaction (same transaction ID) creates audit trail across all services
- Each service's audit_log contains its portion of the transaction
- Querying all services provides complete audit trail for the tenant

**Assertions**:

- 3 audit entries created (one per service)
- All entries share the same transaction ID
- Simulated cross-service query returns complete trail

### 5. Idempotency

**File**: `audit_writer_e2e_test.go` - `TestIdempotency`

Validates that:

- Writing the same event multiple times (by event_id) is idempotent
- Duplicate writes don't create additional audit log entries

**Assertions**:

- 3 writes of the same event result in 1 audit log entry
- No errors on duplicate writes (idempotent behaviour)

## Running the Tests

### Prerequisites

- Docker (for testcontainers)
- Go 1.21+

### Run All E2E Tests

```bash
go test -v -tags=integration ./tests/audit-e2e/... -timeout 10m
```

### Run Specific Test

```bash
go test -v -tags=integration ./tests/audit-e2e/... -run TestMultiServiceMultiTenantWrites -timeout 5m
```

### Skip in Short Mode

E2E tests are automatically skipped when running with `-short`:

```bash
go test -v -short ./tests/audit-e2e/...  # Skips E2E tests
```

## Test Duration

- **Setup Time**: ~30-60 seconds (starting 3 PostgreSQL containers)
- **Test Execution**: ~5-10 seconds per scenario
- **Total Runtime**: ~2-3 minutes for full suite

## Troubleshooting

### Docker Issues

If tests fail with container startup errors:

```bash
# Check Docker is running
docker ps

# Clean up stale containers
docker rm -f $(docker ps -aq)
```

### Database Connection Timeouts

If PostgreSQL containers take too long to start:

- Increase timeout in test setup (currently 180 seconds)
- Check Docker resource limits (CPU, memory)

### Test Flakiness

If tests occasionally fail:

- Look for timing issues (waits between operations)
- Check testcontainers logs for container startup failures
- Verify no port conflicts on host machine

## Design Rationale

### Why Not Test Full Kafka Flow?

Full Kafka consumer testing would require:

- Kafka testcontainer (adds 30+ seconds startup time)
- Mock Kafka producers
- Message header injection
- Consumer group coordination
- Significantly more complex test infrastructure

The current approach:

- Tests the critical path (TenantAuditWriter)
- Validates bounded context and tenant isolation
- Runs faster and more reliably
- Easier to debug when tests fail

Unit tests for `KafkaConsumer` already validate Kafka-specific logic.

### Why 3 Separate PostgreSQL Containers?

Each service must have its own database to enforce bounded contexts. This is a core architecture principle (ADR-0002).
Using 3 separate containers:

- Proves bounded context enforcement at the infrastructure level
- Validates that audit consumers cannot accidentally write cross-database
- Simulates production deployment (3 separate RDS instances)

## Coverage

These E2E tests provide:

- ✅ Multi-tenant data isolation within each service
- ✅ Bounded context enforcement (database-per-service)
- ✅ Independent failure handling
- ✅ Audit trail completeness across services
- ✅ Idempotency guarantees
- ✅ Cross-service transaction correlation

Not covered (handled by unit tests):

- Kafka message deserialisation
- Message header extraction
- Consumer group coordination
- DLQ routing
- Metrics and observability

## Future Enhancements

Potential additions to E2E suite:

1. **Load Testing**: Concurrent writes from multiple goroutines
2. **Kafka Integration**: Optional full flow test (requires Kafka container)
3. **Performance Benchmarks**: Measure write throughput per service
4. **Failure Injection**: Simulate database failures, network issues
5. **Audit Query API**: Test gRPC endpoints for querying audit logs

## Related Documentation

- [ADR-0002: Microservices per BIAN Domain](../../docs/adr/0002-microservices-per-bian-domain.md) -
  Database-per-service architecture
- [ADR-0020: Per-Service Audit Workers](../../docs/adr/0020-per-service-audit-workers.md) - Audit consumer architecture
- [Audit Consumer README](../../cmd/audit-consumer/README.md) - Deployment and configuration
- [Audit Writer Integration Tests][writer-tests] - Unit-level integration tests

[writer-tests]: ../../services/audit-worker/adapters/persistence/tenant_audit_writer_integration_test.go
