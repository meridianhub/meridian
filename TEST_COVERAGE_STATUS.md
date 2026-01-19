# Test Coverage Status - Market Information Service

## Current Status (2026-01-19)

### Overall Progress

- **Starting Coverage**: 17.8%
- **Current Coverage**: 63.1%
- **Target Coverage**: 80%+
- **Progress**: +45.3 percentage points

### Package-by-Package Status

| Package | Starting | Current | Target | Status |
|---------|----------|---------|--------|--------|
| service | 17.8% | 63.1% | 80% | 🟡 In Progress (+45.3pp) |
| adapters/external/ecb | 94.3% | 94.3% | 80% | ✅ Complete |
| config | 100.0% | 100.0% | 80% | ✅ Complete |
| domain | 100.0% | 100.0% | 80% | ✅ Complete |
| observability | 77.3% | 77.3% | 80% | 🟡 Needs 2.7% |
| adapters/persistence | 77.5% | 77.5% | 80% | 🟡 Needs 2.5% |
| client | 62.8% | 62.8% | 80% | 🔴 Needs 17.2% |
| cmd | 0.0% | 0.0% | 80% | 🔴 Needs 80% |

## Completed Work

### ✅ dataset_service_test.go

**File**: `services/market-information/service/dataset_service_test.go`
**Lines**: 693 lines of comprehensive tests
**Coverage Impact**: +16.6% service coverage

**Tests Implemented**:

- `TestRegisterDataSet_Success` - Happy path dataset registration
- `TestRegisterDataSet_Errors` - Error cases (duplicate code, invalid category, missing fields)
- `TestUpdateDataSet_Success` - Dataset updates (description, validation expressions)
- `TestUpdateDataSet_Errors` - Error cases (not found, non-draft status)
- `TestActivateDataSet_Success` - Dataset activation (DRAFT → ACTIVE)
- `TestActivateDataSet_Errors` - Error cases (not found, already active)
- `TestDeprecateDataSet_Success` - Dataset deprecation (ACTIVE → DEPRECATED)
- `TestDeprecateDataSet_Errors` - Error cases (not found)
- `TestRetrieveDataSet_Success` - Retrieval by code and version
- `TestRetrieveDataSet_Errors` - Error cases (not found)
- `TestListDataSets_Success` - Listing with filters (status, category, pagination)
- `TestDataSetStatusTransitions` - State machine validation

### ✅ observation_service_test.go

**File**: `services/market-information/service/observation_service_test.go`
**Lines**: ~996 lines of comprehensive tests
**Coverage Impact**: +14.7% service coverage

**Tests Implemented**:

- `TestRecordObservation_Success` - Basic observation recording
- `TestRecordObservation_Errors` - Error validation (missing fields, invalid source/dataset)
- `TestRecordObservation_CELValidation` - CEL expression validation
- `TestRetrieveObservation_Success` - Observation retrieval by ID
- `TestRetrieveObservation_Errors` - Error cases (not found)
- `TestListObservations_Success` - Observation listing with filters
- `TestListObservations_Pagination` - Pagination handling
- `TestRecordObservationBatch_Success` - Batch observation recording

### ✅ source_service_test.go

**File**: `services/market-information/service/source_service_test.go`
**Lines**: ~383 lines of comprehensive tests
**Coverage Impact**: +7.2% service coverage

**Tests Implemented**:

- `TestRegisterDataSource_Success` - Happy path source registration
- `TestRegisterDataSource_UpsertBehavior` - Upsert on duplicate code
- `TestRegisterDataSource_Errors` - Error cases (missing fields, trust level validation)
- `TestUpdateDataSource_Success` - Source updates (name, trust level)
- `TestUpdateDataSource_Errors` - Error cases (not found)
- `TestDeactivateDataSource_Success` - Source deactivation (soft-delete)
- `TestDeactivateDataSource_Errors` - Error cases (not found, already inactive)
- `TestListDataSources_Success` - Source listing with trust level ordering
- `TestTrustLevelRangeValidation` - Trust level boundary tests (0-100)

### ✅ Documentation Tasks

**ADR-0027**: `docs/adr/0027-market-information-management.md`

- Bi-temporal data model design decisions
- Quality ladder and supersession rules
- CEL validation approach
- Knowledge lineage for audit compliance

**Operational Runbook**: `docs/runbooks/market-information-operations.md`

- Service overview and key concepts
- Common operations (dataset, source, observation management)
- Troubleshooting guides
- Database operations and monitoring
- Disaster recovery procedures

**Architecture Update**: `docs/architecture/bian-service-boundaries.md`

- Added Market Information Management service domain
- Data ownership boundaries
- RPC and event patterns
- BIAN mapping

### ✅ Bug Fixes

#### Bug 1: Mapper not setting isActive

- **Issue**: Data sources loaded from database were marked as inactive
- **Root Cause**: `EntityToDataSource` mapper was not setting `isActive=true`
- **Fix**: Added `WithIsActive(true)` to the mapper builder chain in `mappers.go`

#### Bug 2: DeactivateDataSource not persisting deactivation

- **Issue**: `DeactivateDataSource` set `isActive=false` on domain object but `Save()` didn't persist it
- **Root Cause**: Repository had no `Delete` method; `Save()` only updates name/description/trust_level
- **Fix**:
  - Added `Delete(ctx, code)` method to `SourceRepository` interface
  - Implemented soft-delete: `UPDATE ... SET deleted_at = NOW() WHERE code = $1`
  - Updated `DeactivateDataSource` to call `Delete()` instead of `Save()`
  - Now deactivated sources are properly excluded from queries

> **Note**: The database uses soft-delete pattern (`deleted_at` column). Sources with
> `deleted_at IS NOT NULL` are excluded from `FindByCode`, `FindByID`, and `List` queries.

## Remaining Work (16.9% to reach 80%)

### High Priority (Service Package)

#### 1. event_publisher_test.go

**Estimate**: 2 hours
**Coverage Impact**: +5-8%

**Tests Needed**:

- `NewKafkaObservationPublisher` - Initialization
- `PublishObservationRecorded` - Event publishing
- `Close` and `FlushWithTimeout` - Cleanup
- Mock Kafka producer for testing

#### 2. server_test.go

**Estimate**: 1 hour
**Coverage Impact**: +3-5%

**Tests Needed**:

- `NewServer` - Initialization with required dependencies
- `NewServer` errors - Nil repository validation
- Option functions (`WithCelValidator`, `WithEventPublisher`, `WithLogger`)

#### 3. health_test.go (Update)

**Estimate**: 30 minutes
**Coverage Impact**: +1-2%

**Tests Needed**:

- Add `Watch` method test (streaming health check)

### Medium Priority (Near-80% Packages)

#### 4. observability Package (77.3% → 80%)

**Estimate**: 1 hour
**Coverage Impact**: +2.7%

#### 5. persistence Package (77.5% → 80%)

**Estimate**: 1 hour
**Coverage Impact**: +2.5%

### Task Master Subtasks Status

| Subtask | Description | Status |
|---------|-------------|--------|
| 10.1 | Achieve 80%+ Unit Test Coverage | 🟡 In Progress (63.1%) |
| 10.2 | End-to-End Service Lifecycle Tests | 📋 Pending |
| 10.3 | Benchmark and Fuzz Tests | 📋 Pending |
| 10.4 | ADR and Architecture Documentation | ✅ Complete |
| 10.5 | Operational Runbook | ✅ Complete |

## Test Execution

### Running Tests

```bash
# Service package only
go test ./services/market-information/service

# With coverage
go test -cover ./services/market-information/service

# Detailed coverage report
go test -coverprofile=/tmp/coverage.out ./services/market-information/service
go tool cover -html=/tmp/coverage.out

# All packages
go test -cover ./services/market-information/...
```

### Coverage Verification

```bash
# Check which functions lack coverage
go test -coverprofile=/tmp/coverage.out ./services/market-information/service
go tool cover -func=/tmp/coverage.out | grep "0.0%"
```

---

**Last Updated**: 2026-01-19
**Status**: Service package at 63.1% coverage (+45.3pp from baseline), documentation complete
