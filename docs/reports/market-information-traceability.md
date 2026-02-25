# Market Information Management - Requirements Traceability Matrix

**Date:** 2026-01-20
**PRD Version:** 1.3
**Implementation Tag:** market-information-management

## Overview

This document provides a complete mapping of PRD requirements to their implementation locations,
identifies coverage status, and documents any gaps or edge cases requiring attention.

---

## Functional Requirements Traceability

### FR-1: Dataset Definition Registry

| Requirement ID | Description | Status | Implementation Location | Notes |
|----------------|-------------|--------|-------------------------|-------|
| FR-1.1 | Maintain registry with tenant isolation (schema-per-tenant) | IMPLEMENTED | `adapters/persistence/dataset_repository.go` | Uses tenant context for schema selection |
| FR-1.2 | Unique dataset code within schema | IMPLEMENTED | `domain/dataset.go`, `migrations/20260116000001_initial.sql` | UNIQUE(code, version) constraint |
| FR-1.3 | Support categories: PRICING, CONTEXTUAL | IMPLEMENTED | `domain/data_category.go` | Enum with validation |
| FR-1.4 | CEL validation expressions | IMPLEMENTED | `domain/dataset.go`, `service/cel_validator.go` | Full CEL support with decimal() function |
| FR-1.5 | CEL resolution key expressions | IMPLEMENTED | `service/cel_validator.go:CompileResolutionKey()` | Computes unique keys for temporal queries |
| FR-1.6 | Dataset versioning | IMPLEMENTED | `domain/dataset.go:version` | Optimistic locking with version increment |
| FR-1.7 | System dataset seeding during provisioning | IMPLEMENTED | `migrations/20260116000001_initial.sql` | FX_RATE, ENERGY_SPOT, ENERGY_TARIFF, CARBON_PRICE, WEATHER_TEMP |

### FR-2: Observation Ingestion

| Requirement ID | Description | Status | Implementation Location | Notes |
|----------------|-------------|--------|-------------------------|-------|
| FR-2.1 | Accept observations referencing dataset code/version | IMPLEMENTED | `service/observation_service.go:RecordObservation()` | Validates against ACTIVE dataset |
| FR-2.2 | CEL validation against dataset expression | IMPLEMENTED | `service/observation_service.go:validateObservation()` | Uses compiled CEL programs |
| FR-2.3 | Temporal bounds (observed_at, valid_from, valid_to) | IMPLEMENTED | `domain/observation.go` | Full bi-temporal support |
| FR-2.4 | Source attribution (source_id) | IMPLEMENTED | `domain/observation.go:sourceID` | References DataSource entity |
| FR-2.5 | Quality level support | IMPLEMENTED | `domain/quality_level.go` | ESTIMATE, ACTUAL, VERIFIED |
| FR-2.6 | Reject failed CEL validation | IMPLEMENTED | `service/observation_service.go:validateObservation()` | Returns INVALID_ARGUMENT gRPC error |
| FR-2.7 | Batch ingestion | IMPLEMENTED | `service/observation_service.go:RecordObservationBatch()` | Parallel validation with 50 worker limit |
| FR-2.8 | Publish domain event on ACTUAL/VERIFIED | IMPLEMENTED | `service/observation_service.go:shouldPublishObservationEvent()` | Kafka topic: meridian.market_information.v1.ObservationRecorded |

### FR-3: Temporal Queries (Bi-Temporal)

| Requirement ID | Description | Status | Implementation Location | Notes |
|----------------|-------------|--------|-------------------------|-------|
| FR-3.1 | Point-in-time queries | IMPLEMENTED | `adapters/persistence/observation_repository.go:RetrieveObservation()` | Uses knowledgeBaseTime parameter |
| FR-3.2 | Effective date queries | IMPLEMENTED | `adapters/persistence/observation_repository.go:Query()` | Filters by valid_from/valid_to |
| FR-3.3 | Resolution key for temporal lookup | IMPLEMENTED | `service/observation_service.go:computeResolutionKey()` | CEL-based key generation |
| FR-3.4 | Return highest-quality observation | IMPLEMENTED | `adapters/persistence/observation_repository.go:queryObservationInSchema()` | ORDER BY quality DESC, observed_at DESC, trust_level DESC, created_at DESC |
| FR-3.5 | Historical range queries for analytics | IMPLEMENTED | `service/observation_service.go:ListObservations()` | Supports from_time/to_time filters |
| FR-3.6 | Bi-temporal queries via knowledge_base_time | IMPLEMENTED | `adapters/persistence/observation_repository.go:RetrieveObservation()` | Full time-travel support |
| FR-3.7 | Exclude observations with created_at > knowledge_base_time | IMPLEMENTED | SQL: `AND o.created_at <= $3` | Correct bi-temporal filtering |

### FR-4: Data Source Configuration

| Requirement ID | Description | Status | Implementation Location | Notes |
|----------------|-------------|--------|-------------------------|-------|
| FR-4.1 | Maintain registry of data sources | IMPLEMENTED | `adapters/persistence/source_repository.go` | CRUD operations |
| FR-4.2 | Trust levels for quality ladder | IMPLEMENTED | `domain/data_source.go:trustLevel` | 0-100 integer range |
| FR-4.3 | Tenants can add custom sources | IMPLEMENTED | `service/source_service.go:RegisterSource()` | Tenant-scoped |
| FR-4.4 | Override platform source priority | PARTIAL | See gaps section | Trust level mechanism exists but override pattern not explicit |
| FR-4.5 | Source-specific ingestion credentials | NOT STARTED | N/A | P2 priority - deferred to future |

### FR-5: Quality Ladder and Knowledge Lineage

| Requirement ID | Description | Status | Implementation Location | Notes |
|----------------|-------------|--------|-------------------------|-------|
| FR-5.1 | Quality levels per ADR-0017 | IMPLEMENTED | `domain/quality_level.go` | ESTIMATE=1, ACTUAL=2, VERIFIED=3 |
| FR-5.2 | ESTIMATE < ACTUAL < VERIFIED precedence | IMPLEMENTED | SQL ORDER BY and supersession logic | Integer ordering enables DB-level sort |
| FR-5.3 | Higher quality supersedes lower | IMPLEMENTED | `adapters/persistence/observation_repository.go:Record()` | Automatic supersession on insert |
| FR-5.4 | Quality transition tracking for audit | IMPLEMENTED | Supersession chain via superseded_by | Full lineage preserved |
| FR-5.5 | Corrections mark old as superseded | IMPLEMENTED | `adapters/persistence/observation_repository.go:Record()` | UPDATE superseded_by query |
| FR-5.6 | Track causation_id for lineage | IMPLEMENTED | `domain/observation.go:causationID` | Links to upstream events |
| FR-5.7 | Traversable supersession chain | IMPLEMENTED | `superseded_by` FK with index | idx_observation_superseded index |

### FR-6: BIAN Control Record Operations

| Requirement ID | Description | Status | Implementation Location | Notes |
|----------------|-------------|--------|-------------------------|-------|
| FR-6.1 | RegisterDataSet | IMPLEMENTED | `service/dataset_service.go:RegisterDataSet()` | Creates DRAFT status |
| FR-6.2 | UpdateDataSet | IMPLEMENTED | `service/dataset_service.go:UpdateDataSet()` | Only DRAFT allowed |
| FR-6.3 | ActivateDataSet | IMPLEMENTED | `service/dataset_service.go:ActivateDataSet()` | DRAFT -> ACTIVE transition |
| FR-6.4 | DeprecateDataSet | IMPLEMENTED | `service/dataset_service.go:DeprecateDataSet()` | ACTIVE -> DEPRECATED |
| FR-6.5 | RetrieveDataSet | IMPLEMENTED | `service/dataset_service.go:RetrieveDataSet()` | By code and version |
| FR-6.6 | ListDataSets | IMPLEMENTED | `service/dataset_service.go:ListDataSets()` | With filters |
| FR-6.7 | RecordObservation | IMPLEMENTED | `service/observation_service.go:RecordObservation()` | Full validation |
| FR-6.8 | RetrieveObservation | IMPLEMENTED | `service/observation_service.go:RetrieveObservation()` | Bi-temporal query |
| FR-6.9 | RegisterDataSource | IMPLEMENTED | `service/source_service.go:RegisterSource()` | With trust level |
| FR-6.10 | EvaluateDataSet (CEL playground) | NOT IMPLEMENTED | See gaps section | P1 - deferred |

---

## Non-Functional Requirements Traceability

### NFR-1: Performance

| Requirement ID | Target | Status | Validation Method | Notes |
|----------------|--------|--------|-------------------|-------|
| NFR-1.1 | Point-in-time query < 10ms p99 | NEEDS VALIDATION | NFR benchmark tests (to be created) | Bi-temporal index optimized |
| NFR-1.2 | Observation ingestion < 50ms p99 | NEEDS VALIDATION | NFR benchmark tests (to be created) | Single insert with supersession |
| NFR-1.3 | Batch ingestion 1,000 obs/sec | NEEDS VALIDATION | NFR benchmark tests (to be created) | Parallel validation with 50 workers |

### NFR-2: Reliability

| Requirement ID | Target | Status | Validation Method | Notes |
|----------------|--------|--------|-------------------|-------|
| NFR-2.1 | Service availability 99.9% | INFRASTRUCTURE | Kubernetes deployment | Covered by platform SLO |
| NFR-2.2 | Data durability 99.999999% | INFRASTRUCTURE | CockroachDB replication | Database-level guarantee |

### NFR-3: Scalability

| Requirement ID | Target | Status | Validation Method | Notes |
|----------------|--------|--------|-------------------|-------|
| NFR-3.1 | 10,000+ observations/dataset/day | ARCHITECTURAL | Index design review | idx_observation_resolution_bitemporal |
| NFR-3.2 | 1,000+ datasets per tenant | ARCHITECTURAL | Schema design | No artificial limits |
| NFR-3.3 | 7-year historical retention | NOT IMPLEMENTED | See gaps section | Archival policy needed |

---

## Gap Analysis

### Critical Gaps (P0)

None identified. All P0 functional requirements are implemented.

### High Priority Gaps (P1)

| Gap ID | Description | Requirement | Impact | Recommendation |
|--------|-------------|-------------|--------|----------------|
| GAP-001 | EvaluateDataSet (CEL playground) not implemented | FR-6.10 | Users cannot test CEL expressions before activation | Implement in next iteration |
| GAP-002 | NFR performance validation not automated | NFR-1.* | Cannot verify performance targets in CI | Create benchmark test suite |

### Medium Priority Gaps (P2)

| Gap ID | Description | Requirement | Impact | Recommendation |
|--------|-------------|-------------|--------|----------------|
| GAP-003 | Source-specific ingestion credentials | FR-4.5 | External sources need manual credential management | Defer to Phase 2 |
| GAP-004 | Historical data retention/archival | NFR-3.3 | Unbounded storage growth | Implement archival policy |
| GAP-005 | Cursor-based pagination | ListObservations | Offset pagination limits scalability | Replace with cursor-based |

### Edge Cases Not Covered

| Edge Case | Description | Current Behavior | Recommendation |
|-----------|-------------|------------------|----------------|
| EC-001 | Concurrent supersession race | Last write wins | Add optimistic locking on supersession |
| EC-002 | Dataset version mismatch in observations | Uses latest ACTIVE | Document expected behavior |
| EC-003 | Knowledge time in future | Query returns all known | Add validation to reject future knowledge times |
| EC-004 | Empty observation_context with CEL | CEL may fail | Add graceful handling with empty map default |

---

## Implementation Coverage Summary

| Category | Total | Implemented | Partial | Not Started | Coverage |
|----------|-------|-------------|---------|-------------|----------|
| FR-1 Dataset Registry | 7 | 7 | 0 | 0 | 100% |
| FR-2 Observation Ingestion | 8 | 8 | 0 | 0 | 100% |
| FR-3 Temporal Queries | 7 | 7 | 0 | 0 | 100% |
| FR-4 Data Source Config | 5 | 3 | 1 | 1 | 70% |
| FR-5 Quality Ladder | 7 | 7 | 0 | 0 | 100% |
| FR-6 BIAN Operations | 10 | 9 | 0 | 1 | 90% |
| **Total FR** | **44** | **41** | **1** | **2** | **93%** |
| NFR-1 Performance | 3 | 0 | 0 | 3 | 0% (needs validation) |
| NFR-2 Reliability | 2 | 2 | 0 | 0 | 100% (infrastructure) |
| NFR-3 Scalability | 3 | 2 | 0 | 1 | 67% |
| **Total NFR** | **8** | **4** | **0** | **4** | **50%** |

---

## Test Coverage Mapping

| Component | Unit Tests | Integration Tests | Coverage |
|-----------|------------|-------------------|----------|
| domain/dataset.go | dataset_test.go | dataset_repository_test.go | HIGH |
| domain/observation.go | observation_test.go | observation_repository_test.go | HIGH |
| domain/data_source.go | data_source_test.go | source_repository_test.go | HIGH |
| domain/quality_level.go | quality_level_test.go | N/A | MEDIUM |
| service/cel_validator.go | cel_validator_test.go | N/A | HIGH |
| service/dataset_service.go | dataset_service_test.go | N/A | HIGH |
| service/observation_service.go | observation_service_test.go | N/A | HIGH |
| service/source_service.go | source_service_test.go | N/A | HIGH |
| client/client.go | client_test.go | N/A | MEDIUM |

---

## Recommendations

1. **Create NFR Performance Validation Suite** (Priority: HIGH)
   - Implement benchmark tests following internal-account pattern
   - Set CI thresholds with production targets as documentation

2. **Implement CEL Playground** (Priority: MEDIUM)
   - Add EvaluateDataSet RPC for testing expressions without persistence
   - Essential for developer experience

3. **Document Edge Cases** (Priority: MEDIUM)
   - Add explicit handling for concurrent supersession
   - Validate knowledge_base_time is not in future

4. **Plan Archival Strategy** (Priority: LOW)
   - Design time-based partitioning or archival to separate storage
   - Implement retention policy configuration per dataset

---

Generated as part of Market Information Management Meta-Review (Task 11.1)
