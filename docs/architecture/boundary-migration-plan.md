# BIAN Service Boundary Migration Plan

**Document Version:** 1.0
**Date:** 2025-11-19
**Status:** Active
**Related Documents:**

- [Task 14: Service Coupling Analysis](service-coupling-analysis.md)
- [Task 15: BIAN Service Boundaries](bian-service-boundaries.md)
- [Event-Driven Architecture](event-driven-architecture.md)
- [ADR-0002: Microservices Per BIAN Domain](../adr/0002-microservices-per-bian-domain.md)

## Executive Summary

### Current State Assessment

Meridian's microservices architecture demonstrates **strong BIAN boundary compliance** with **zero cross-service domain violations**. The architecture's foundation is solid, requiring only platform code organization improvements to achieve full compliance.

**Key Findings:**

- **Zero P0 violations**: No service imports another service's internal packages
- **17 P1 violations**: All platform code misplaced in `internal/platform/` instead of `pkg/platform/`
- **14 safe proto dependencies**: Current-account properly uses gRPC clients for cross-service communication
- **Independent databases**: Each service owns its schema with no cross-service database access
- **Stable architecture**: Position-keeping (I=0.00) and financial-accounting (I=0.00) are stable providers; current-account (I=1.00) is an orchestration layer with expected high instability

### Migration Goals

1. **Platform Code Migration (P1)**: Move `internal/platform/` → `pkg/platform/` (5 story points, ~1 week)
2. **Current-Account Instability Reduction (P1)**: Enhance anti-corruption layer (3 story points, optional)
3. **Documentation (P2)**: Create service dependency documentation (2 story points)
4. **CI Gates (P2)**: Implement automated coupling checks (3 story points)

**Total Effort:** 13 story points (2-3 weeks for one developer)

### Timeline Estimate

- **Week 1**: Platform code migration (P1-1) + CI gates setup (P2-2)
- **Week 2**: Documentation (P2-1) + testing/validation
- **Week 3**: Anti-corruption layer enhancement (P1-2, optional architectural improvement)

### Risk Assessment

*### Overall Risk: LOW*

- All migrations are mechanical refactoring with minimal business logic changes
- Existing test suite ensures no regressions
- No customer-facing changes required
- Platform code migration is automated via find/replace
- Services already respect BIAN boundaries (zero domain violations)

---

## Boundary Validation Results

### Compliant Patterns (What's Working Well)

#### ✅ Zero Cross-Service Internal Imports (P0 Compliance)

**Status:** PASS
**Evidence:** Coupling analysis detected zero violations
**Impact:** Services properly respect BIAN domain boundaries

All services follow the strict rule: **NEVER import `internal/<other-service>/` packages**.

```bash
# Verification
./scripts/analyze-coupling.sh | jq '.violations[] | select(.type == "cross-service-internal-import")'
# Result: (empty - no violations)
```go

**Files Analyzed:**

- `internal/current-account/`: 15 files
- `internal/position-keeping/`: 12 files
- `internal/financial-accounting/`: 18 files

**Compliance Rate:** 100% (0 violations / 45 files)

#### ✅ Proto-Only Communication (P0 Compliance)

**Status:** PASS
**Evidence:** 14 safe proto imports for gRPC clients
**Impact:** Services communicate via versioned, type-safe contracts

**Current-Account Dependencies:**

- `meridian/position_keeping/v1/position_keeping.proto` (7 imports)
- `meridian/financial_accounting/v1/financial_accounting.proto` (7 imports)

**Implementation Files:**

- `internal/current-account/clients/positionkeeping_client.go`
- `internal/current-account/clients/financialaccounting_client.go`
- `internal/current-account/clients/resilient_client.go` (circuit breaker pattern)

**Proto Breaking Change Detection:**

```bash
buf breaking --against '.git#branch=develop'
# Status: Passing (no breaking changes)
```protobuf

#### ✅ Independent Database Schemas (P0 Compliance)

**Status:** PASS
**Evidence:** No cross-service database access detected
**Impact:** Services can deploy and scale independently

**Schema Ownership:**

| Service | Schema | Tables | Migration Tool |
|---------|--------|--------|----------------|
| current-account | `current_account_audit` | `audit_log`, `audit_outbox` | Atlas |
| position-keeping | `position_keeping` | `transaction_logs`, `lineage`, `audit_trails`, `status_tracking` | Atlas |
| financial-accounting | `financial_accounting` | `booking_logs`, `ledger_postings`, `chart_of_accounts` | Atlas |

**Verification:**

```bash
# Check for cross-service database queries (none found)
rg "FROM (position_keeping|financial_accounting|current_account_audit)\." \
   --glob "internal/*/adapters/persistence/*.go"
# Result: (empty - no cross-schema queries)
```go

#### ✅ Kafka Event-Driven Communication (P0 Compliance)

**Status:** PASS
**Evidence:** 51 event publisher usages, proper consumer patterns
**Impact:** Asynchronous communication supports eventual consistency

**Event Publishers:**

- position-keeping: 42 event publisher usages
- financial-accounting: 9 event publisher usages

**Event Consumers:**

- financial-accounting: `DepositConsumer` implementation

**Event Schema Files:**

- `api/proto/meridian/events/v1/deposit_event.proto` (legacy)
- `api/proto/meridian/events/v1/financial_accounting_events.proto`
- `api/proto/meridian/events/v1/position_keeping_events.proto`

### Violations Requiring Migration

#### ❌ Internal Platform Usage (P1 Violations - 17 Total)

**Problem:** Services import `internal/platform/*` packages, violating Go module semantics.

**Impact:**

- Violates Go's `internal/` package convention (internal code should not be shared)
- Prevents platform code reuse by external tools
- Creates confusion about which code is internal vs shared

**Breakdown by Package:**

| Package | Current Location | Should Be | Files Affected | Services Using |
|---------|------------------|-----------|----------------|----------------|
| observability | `internal/platform/observability` | `pkg/platform/observability` | 7 | All services |
| kafka | `internal/platform/kafka` | `pkg/platform/kafka` | 3 | position-keeping, financial-accounting |
| testdb | `internal/platform/testdb` | `pkg/platform/testdb` | 5 | current-account, financial-accounting |
| auth | `internal/platform/auth` | `pkg/platform/auth` | 1 | position-keeping |

**Affected Files (17 total):**

**Observability (7 files):**

```text
cmd/current-account/main.go:18
cmd/financial-accounting/main.go:17
internal/current-account/service/grpc_service.go:22
internal/current-account/clients/financialaccounting_client.go:10
internal/current-account/clients/positionkeeping_client.go:10
internal/current-account/clients/financialaccounting_client_test.go:8
internal/current-account/clients/positionkeeping_client_test.go:8
internal/position-keeping/app/container.go:10
```text

**Kafka (3 files):**

```text
internal/position-keeping/adapters/messaging/kafka_event_publisher_test.go:9
internal/financial-accounting/adapters/messaging/deposit_consumer.go:12
internal/financial-accounting/adapters/messaging/deposit_consumer_test.go:12
```text

**TestDB (5 files):**

```text
internal/current-account/adapters/persistence/repository_test.go:10
internal/current-account/service/grpc_service_test.go:12
internal/financial-accounting/adapters/persistence/repository_test.go:12
internal/financial-accounting/service/posting_service_test.go:11
internal/financial-accounting/adapters/messaging/deposit_consumer_test.go:13
```text

**Auth (1 file):**

```text
internal/position-keeping/app/container.go:9
```text

**Current Import Pattern (WRONG):**

```go
import "github.com/meridianhub/meridian/internal/platform/observability"
import "github.com/meridianhub/meridian/internal/platform/kafka"
import "github.com/meridianhub/meridian/internal/platform/testdb"
import "github.com/meridianhub/meridian/internal/platform/auth"
```text

**Target Import Pattern (CORRECT):**

```go
import "github.com/meridianhub/meridian/pkg/platform/observability"
import "github.com/meridianhub/meridian/pkg/platform/kafka"
import "github.com/meridianhub/meridian/pkg/platform/testdb"
import "github.com/meridianhub/meridian/pkg/platform/auth"
```sql

---

## Migration Phases

### Phase 1: Platform Code Migration (Priority: P1, High Risk Mitigation)

**Duration:** Week 1 (5 story points)
**Risk:** Low (mechanical refactoring, fully automated)
**Dependencies:** None
**Validation:** Full test suite + coupling analysis

#### Step 1.1: Create pkg/platform Structure (1 story point, 1 hour)

**Actions:**

```bash
cd ~/dev/github.com/meridianhub/meridian/meridian-main

# Create target directory structure
mkdir -p pkg/platform/{observability,kafka,testdb,auth}

# Verify structure created
ls -la pkg/platform/
```text

**Validation:**

```bash
# Ensure directories exist
test -d pkg/platform/observability && \
test -d pkg/platform/kafka && \
test -d pkg/platform/testdb && \
test -d pkg/platform/auth && \
echo "✅ Directory structure created successfully"
```text

#### Step 1.2: Move Platform Packages (2 story points, 4 hours)

**Actions:**

```bash
cd ~/dev/github.com/meridianhub/meridian/meridian-main

# Move packages with git mv to preserve history
git mv internal/platform/observability pkg/platform/observability
git mv internal/platform/kafka pkg/platform/kafka
git mv internal/platform/testdb pkg/platform/testdb
git mv internal/platform/auth pkg/platform/auth

# Remove empty internal/platform directory
rmdir internal/platform

# Verify moves
ls -la pkg/platform/
```text

**Validation:**

```bash
# Verify packages moved and git history preserved
git log --follow pkg/platform/observability/ | head -5
git log --follow pkg/platform/kafka/ | head -5
git log --follow pkg/platform/testdb/ | head -5
git log --follow pkg/platform/auth/ | head -5
```sql

#### Step 1.3: Update Import Paths (2 story points, 4 hours)

**Automated Find/Replace:**

```bash
cd ~/dev/github.com/meridianhub/meridian/meridian-main

# Update import paths in all Go files
find ./cmd ./internal -name "*.go" -type f -exec sed -i '' \
  's|github.com/meridianhub/meridian/internal/platform|github.com/meridianhub/meridian/pkg/platform|g' {} \;

# Update go.mod if necessary (usually automatic)
go mod tidy
```text

**Manual Verification (Spot Check):**

```bash
# Verify observability imports updated
rg "pkg/platform/observability" cmd/current-account/main.go
rg "pkg/platform/observability" internal/current-account/service/grpc_service.go

# Verify kafka imports updated
rg "pkg/platform/kafka" internal/financial-accounting/adapters/messaging/

# Verify testdb imports updated
rg "pkg/platform/testdb" internal/current-account/adapters/persistence/

# Verify auth imports updated
rg "pkg/platform/auth" internal/position-keeping/app/container.go
```text

**Comprehensive Validation:**

```bash
# Ensure NO remaining internal/platform imports
rg "internal/platform" --type go ./cmd ./internal
# Expected result: (empty)

# Ensure all pkg/platform imports present
rg "pkg/platform" --type go ./cmd ./internal | wc -l
# Expected result: 17 (matching original violation count)
```text

#### Step 1.4: Verify with Tests (1 story point, 2 hours)

**Run Full Test Suite:**

```bash
cd ~/dev/github.com/meridianhub/meridian/meridian-main

# Run all unit tests
make test
# Expected: All tests pass

# Run service-specific tests
cd cmd/current-account && go test ./...
cd cmd/position-keeping && go test ./...
cd cmd/financial-accounting && go test ./...

# Run integration tests
make test-integration
```text

**Run Coupling Analysis:**

```bash
./scripts/analyze-coupling.sh > docs/architecture/coupling-metrics-post-migration.json

# Verify zero internal/platform violations
jq '.violations[] | select(.type == "internal-platform-import")' \
   docs/architecture/coupling-metrics-post-migration.json
# Expected result: (empty)
```text

**Build All Services:**

```bash
# Ensure services compile
make build

# Verify binaries created
ls -lh bin/current-account
ls -lh bin/position-keeping
ls -lh bin/financial-accounting
```text

#### Step 1.5: Commit and Push (included in Step 1.4 time)

**Git Workflow:**

```bash
# Stage all changes
git add .

# Commit with clear message
git commit -m "$(cat <<'EOF'
refactor: Migrate platform code from internal/ to pkg/

Moves shared platform utilities from internal/platform/ to pkg/platform/
to follow Go module semantics. The internal/ directory is semantically
private and should not be imported across services, while pkg/ is the
correct location for shared, reusable libraries.

Changes:
- Move internal/platform/observability → pkg/platform/observability (7 files)
- Move internal/platform/kafka → pkg/platform/kafka (3 files)
- Move internal/platform/testdb → pkg/platform/testdb (5 files)
- Move internal/platform/auth → pkg/platform/auth (1 file)
- Update import paths across all services (17 files total)

This resolves all 17 P1-1 violations identified in coupling analysis.

Validation:
- All tests pass (unit + integration)
- Coupling analysis shows zero internal/platform violations
- All services build successfully
- Git history preserved via git mv

Related: Task 15, Subtask 15.5 - Boundary Migration Plan
EOF
)"

# Push to remote branch
git push origin 15-bian-service-boundaries
```text

**Validation:**

```bash
# Verify commit includes all changes
git show --stat HEAD

# Verify git history preserved
git log --follow --oneline pkg/platform/observability/ | head -10
```sql

### Phase 2: Documentation and CI Gates (Priority: P2, Medium Effort)

**Duration:** Week 1-2 (5 story points)
**Risk:** None (documentation and automation)
**Dependencies:** Phase 1 (platform migration) must complete first

#### Step 2.1: Create Service Dependency Documentation (2 story points, 4 hours)

**Create DEPENDENCIES.md for CurrentAccount:**

```bash
cat > cmd/current-account/DEPENDENCIES.md <<'EOF'
# CurrentAccount Service Dependencies

## Upstream Services (What We Call)

### PositionKeeping Service
- **Protocol:** gRPC (synchronous)
- **Proto:** `api/proto/meridian/position_keeping/v1/position_keeping.proto`
- **Usage:** Balance queries, transaction history retrieval for account operations
- **RPCs Called:**
  - `RetrieveFinancialPositionLog` - Fetch individual transaction logs
  - `ListFinancialPositionLogs` - Query transaction history with filtering
- **Files:**
  - `internal/current-account/clients/positionkeeping_client.go`
- **Resilience:** Circuit breaker in `internal/current-account/clients/resilient_client.go`

### FinancialAccounting Service
- **Protocol:** gRPC (synchronous)
- **Proto:** `api/proto/meridian/financial_accounting/v1/financial_accounting.proto`
- **Usage:** Ledger posting creation for deposit transactions
- **RPCs Called:**
  - `InitiateFinancialBookingLog` - Create new booking logs
  - `CaptureLedgerPosting` - Record debit/credit postings
  - `RetrieveFinancialBookingLog` - Check posting status
- **Files:**
  - `internal/current-account/clients/financialaccounting_client.go`
- **Resilience:** Circuit breaker in `internal/current-account/clients/resilient_client.go`

## Downstream Services (Who Calls Us)

None - CurrentAccount is an orchestration service with no direct dependents.

## Event Publications (Kafka)

- `TransactionInitiatedEvent` (topic: `current-account.transaction-initiated.v1`)
- `TransactionCompletedEvent` (topic: `current-account.transaction-completed.v1`)
- `AccountCreatedEvent` (topic: `current-account.account-created.v1`)
- `AccountStatusChangedEvent` (topic: `current-account.account-status-changed.v1`)

## Event Subscriptions (Kafka)

None - CurrentAccount operates synchronously via gRPC and publishes events asynchronously.

## Platform Dependencies

- `pkg/platform/observability` - OpenTelemetry tracing, logging, metrics
- `pkg/platform/testdb` - Testcontainers integration for tests

## Coupling Metrics

- **Afferent Coupling (Ca):** 0 (no services depend on current-account)
- **Efferent Coupling (Ce):** 2 (depends on position-keeping and financial-accounting)
- **Instability (I):** 1.00 (fully dependent orchestration layer - architecturally appropriate)
- **Assessment:** Orchestration layer pattern - high instability is expected

## Architectural Notes

CurrentAccount acts as an orchestration layer coordinating operations across
position-keeping and financial-accounting. High instability (I=1.00) is
architecturally appropriate for this role, as it:
- Shields clients from multi-service complexity
- Provides a unified API for account operations
- Adapts to changes in downstream service contracts

Mitigation strategies:
- Anti-corruption layer pattern insulates from proto changes
- Circuit breakers prevent cascade failures
- Comprehensive integration testing validates orchestration logic
EOF
```sql

**Create DEPENDENCIES.md for PositionKeeping:**

```bash
cat > cmd/position-keeping/DEPENDENCIES.md <<'EOF'
# PositionKeeping Service Dependencies

## Upstream Services (What We Call)

None - PositionKeeping is a stable provider service with zero efferent coupling (Ce=0).

## Downstream Services (Who Calls Us)

### CurrentAccount Service
- **Protocol:** gRPC (synchronous)
- **Usage:** Transaction history queries, balance retrieval
- **RPCs Exposed:**
  - `InitiateFinancialPositionLog` - Create new position logs
  - `UpdateFinancialPositionLog` - Update existing logs
  - `RetrieveFinancialPositionLog` - Fetch individual logs
  - `ListFinancialPositionLogs` - Query logs with filtering
  - `BulkImportTransactions` - Import transaction batches

## Event Publications (Kafka)

- `TransactionCapturedEvent` (topic: `position-keeping.transaction-captured.v1`)
- `TransactionPostedEvent` (topic: `position-keeping.transaction-posted.v1`)
- `TransactionReconciledEvent` (topic: `position-keeping.transaction-reconciled.v1`)
- `BulkTransactionCapturedEvent` (topic: `position-keeping.bulk-transaction-captured.v1`)

**Publisher Implementation:** `internal/position-keeping/adapters/messaging/kafka_event_publisher.go`
**Domain Interface:** `internal/position-keeping/domain/event_publisher.go`
**Usage:** 42 event publisher usages detected in coupling analysis

## Event Subscriptions (Kafka)

Future: May consume `TransactionInitiatedEvent` from CurrentAccount for asynchronous processing.

## Platform Dependencies

- `pkg/platform/observability` - OpenTelemetry tracing, logging, metrics
- `pkg/platform/kafka` - Kafka event publishing infrastructure
- `pkg/platform/auth` - JWT validation and authorization

## Coupling Metrics

- **Afferent Coupling (Ca):** 1 (current-account depends on this service)
- **Efferent Coupling (Ce):** 0 (no dependencies on other domain services)
- **Instability (I):** 0.00 (fully stable provider service)
- **Assessment:** Stable foundation service - foundational layer

## Architectural Notes

PositionKeeping is a stable foundation for transaction tracking with:
- No outbound dependencies on other BIAN domains
- Single consumer (current-account) with well-defined proto contract
- Low risk of cascading changes
- Appropriate role as a data persistence and audit service

Stability benefits:
- Changes isolated to this service only
- Proto contract evolution is controlled via buf breaking
- Event schema managed via ADR-0004
EOF
```sql

**Create DEPENDENCIES.md for FinancialAccounting:**

```bash
cat > cmd/financial-accounting/DEPENDENCIES.md <<'EOF'
# FinancialAccounting Service Dependencies

## Upstream Services (What We Call)

None - FinancialAccounting has zero synchronous dependencies on other BIAN domain services.

## Downstream Services (Who Calls Us)

### CurrentAccount Service
- **Protocol:** gRPC (synchronous)
- **Usage:** Ledger posting creation, booking log management
- **RPCs Exposed:**
  - `InitiateFinancialBookingLog` - Create new booking logs
  - `UpdateFinancialBookingLog` - Update booking log status/rules
  - `RetrieveFinancialBookingLog` - Fetch booking log details
  - `ListFinancialBookingLogs` - Query booking logs with filtering
  - `CaptureLedgerPosting` - Create debit/credit postings
  - `UpdateLedgerPosting` - Update posting status/result
  - `RetrieveLedgerPosting` - Fetch posting details
  - `ListLedgerPostings` - Query postings with filtering

## Event Publications (Kafka)

- `FinancialBookingLogInitiatedEvent` (topic: `financial-accounting.booking-log-initiated.v1`)
- `FinancialBookingLogPostedEvent` (topic: `financial-accounting.booking-log-posted.v1`)
- `LedgerPostingCapturedEvent` (topic: `financial-accounting.ledger-posting-captured.v1`)
- `LedgerPostingPostedEvent` (topic: `financial-accounting.ledger-posting-posted.v1`)

**Publisher Implementation:** Kafka event publisher adapter
**Usage:** 9 event publisher usages detected in coupling analysis

## Event Subscriptions (Kafka)

### DepositEvent (Legacy)
- **Topic:** `current-account.deposit.v1`
- **Proto:** `api/proto/meridian/events/v1/deposit_event.proto`
- **Consumer:** `internal/financial-accounting/adapters/messaging/deposit_consumer.go`
- **Usage:** Asynchronously creates booking logs and postings for deposits

**Note:** DepositEvent is deprecated in favor of `TransactionInitiatedEvent` with `transaction_type = DEPOSIT`.

## Platform Dependencies

- `pkg/platform/observability` - OpenTelemetry tracing, logging, metrics
- `pkg/platform/kafka` - Kafka consumer and producer infrastructure
- `pkg/platform/testdb` - Testcontainers integration for tests

## Coupling Metrics

- **Afferent Coupling (Ca):** 1 (current-account depends on this service)
- **Efferent Coupling (Ce):** 0 (no dependencies on other domain services)
- **Instability (I):** 0.00 (fully stable provider service)
- **Assessment:** Stable foundation service - foundational layer

## Architectural Notes

FinancialAccounting is a stable foundation for financial operations with:
- No outbound synchronous dependencies on other BIAN domains
- Single gRPC consumer (current-account) with well-defined proto contract
- Event-driven architecture for asynchronous processing
- Low risk of cascading changes

Stability benefits:
- Changes isolated to accounting logic
- Proto contract evolution is controlled via buf breaking
- Event schema managed via ADR-0004
- Decoupled from other services via async events
EOF
```text

**Validation:**

```bash
# Verify files created
ls -la cmd/*/DEPENDENCIES.md

# Check content formatting
head -20 cmd/current-account/DEPENDENCIES.md
head -20 cmd/position-keeping/DEPENDENCIES.md
head -20 cmd/financial-accounting/DEPENDENCIES.md
```sql

#### Step 2.2: Implement CI Coupling Gates (3 story points, 6 hours)

**Create GitHub Actions Workflow:**

```bash
cat > .github/workflows/coupling-check.yml <<'EOF'
name: Service Coupling Analysis

on:
  pull_request:
    paths:
      - 'cmd/**'
      - 'internal/**'
      - 'api/proto/**'
      - 'pkg/platform/**'

jobs:
  coupling-check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0  # Fetch all history for baseline comparison

      - name: Set up Go
        uses: actions/setup-go@v4
        with:
          go-version: '1.21'

      - name: Install dependencies
        run: |
          go install github.com/google/go-safeweb/cmd/go-safeweb@latest
          sudo apt-get update
          sudo apt-get install -y jq

      - name: Run coupling analysis
        run: |
          ./scripts/analyze-coupling.sh > coupling-report.json
          cat coupling-report.json

      - name: Check for P0 violations (cross-service internal imports)
        run: |
          P0_COUNT=$(jq '.violations[] | select(.type == "cross-service-internal-import") | .type' coupling-report.json | wc -l)
          if [ "$P0_COUNT" -gt 0 ]; then
            echo "❌ ERROR: Found $P0_COUNT P0 violations (cross-service internal imports)"
            jq '.violations[] | select(.type == "cross-service-internal-import")' coupling-report.json
            exit 1
          fi
          echo "✅ No P0 violations detected"

      - name: Check for P1 violations (internal/platform imports)
        run: |
          P1_COUNT=$(jq '.violations[] | select(.type == "internal-platform-import") | .type' coupling-report.json | wc -l)
          if [ "$P1_COUNT" -gt 0 ]; then
            echo "⚠️  WARNING: Found $P1_COUNT P1 violations (internal/platform imports)"
            jq '.violations[] | select(.type == "internal-platform-import")' coupling-report.json
            echo "::warning::$P1_COUNT platform code violations detected - should use pkg/platform instead"
            # Don't fail build for P1 violations (warnings only)
          else
            echo "✅ No P1 violations detected"
          fi

      - name: Upload coupling metrics
        uses: actions/upload-artifact@v3
        with:
          name: coupling-metrics
          path: coupling-report.json
          retention-days: 90

      - name: Generate coupling summary
        run: |
          echo "## Coupling Analysis Summary" >> $GITHUB_STEP_SUMMARY
          echo "" >> $GITHUB_STEP_SUMMARY
          echo "**Total Violations:** $(jq '.violations | length' coupling-report.json)" >> $GITHUB_STEP_SUMMARY
          echo "**P0 (Critical):** $(jq '.violations[] | select(.type == "cross-service-internal-import") | .type' coupling-report.json | wc -l)" >> $GITHUB_STEP_SUMMARY
          echo "**P1 (High):** $(jq '.violations[] | select(.type == "internal-platform-import") | .type' coupling-report.json | wc -l)" >> $GITHUB_STEP_SUMMARY
          echo "" >> $GITHUB_STEP_SUMMARY
          echo "### Instability Metrics" >> $GITHUB_STEP_SUMMARY
          jq -r '.services | to_entries[] | "- **\(.key)**: I=\(.value.instability) (\(.value.assessment))"' coupling-report.json >> $GITHUB_STEP_SUMMARY
EOF
```sql

**Create Pre-Commit Hook:**

```bash
cat > .git/hooks/pre-commit <<'EOF'
#!/bin/bash

echo "Running coupling analysis..."
./scripts/analyze-coupling.sh > /tmp/coupling-report.json

P0_COUNT=$(jq '.violations[] | select(.type == "cross-service-internal-import") | .type' /tmp/coupling-report.json | wc -l)
P1_COUNT=$(jq '.violations[] | select(.type == "internal-platform-import") | .type' /tmp/coupling-report.json | wc -l)

if [ "$P0_COUNT" -gt 0 ]; then
  echo "❌ COMMIT BLOCKED: $P0_COUNT P0 violations (cross-service internal imports)"
  jq '.violations[] | select(.type == "cross-service-internal-import")' /tmp/coupling-report.json
  exit 1
fi

if [ "$P1_COUNT" -gt 0 ]; then
  echo "⚠️  WARNING: $P1_COUNT P1 violations (internal/platform imports)"
  jq '.violations[] | select(.type == "internal-platform-import")' /tmp/coupling-report.json
  echo "Run './scripts/analyze-coupling.sh' for details"
  # Non-blocking warning (don't exit 1)
fi

echo "✅ Coupling analysis passed"
exit 0
EOF

chmod +x .git/hooks/pre-commit
```text

**Test CI Workflow Locally:**

```bash
# Simulate CI run
./scripts/analyze-coupling.sh > coupling-report.json

# Verify JSON structure
jq '.' coupling-report.json

# Test violation checks
P0_COUNT=$(jq '.violations[] | select(.type == "cross-service-internal-import") | .type' coupling-report.json | wc -l)
P1_COUNT=$(jq '.violations[] | select(.type == "internal-platform-import") | .type' coupling-report.json | wc -l)

echo "P0 violations: $P0_COUNT (expected: 0)"
echo "P1 violations: $P1_COUNT (expected: 0 after Phase 1)"
```sql

### Phase 3: Anti-Corruption Layer Enhancement (Priority: P1-2, Optional)

**Duration:** Week 3 (3 story points)
**Risk:** Medium (requires careful testing of service interactions)
**Dependencies:** None (orthogonal to Phase 1 and 2)

**Note:** This phase is **OPTIONAL** and focuses on architectural improvement rather than compliance. Current-account's high instability (I=1.00) is expected for an orchestration layer.

#### Approach: Enhance Anti-Corruption Layer

**Goal:** Insulate current-account from proto changes in downstream services

**Implementation:**

```go
// internal/current-account/domain/ports/position_reader.go
package ports

// PositionReader defines domain-level interface (not proto-coupled)
type PositionReader interface {
    GetBalance(ctx context.Context, accountID string) (Balance, error)
    GetTransactionHistory(ctx context.Context, accountID string, opts QueryOptions) ([]Transaction, error)
}

// Balance is a domain entity (not proto)
type Balance struct {
    CurrentBalance  Money
    AvailableBalance Money
    LastUpdated     time.Time
}

// Transaction is a domain entity (not proto)
type Transaction struct {
    ID          string
    AccountID   string
    Amount      Money
    Direction   Direction
    Description string
    Timestamp   time.Time
}
```text

```go
// internal/current-account/adapters/clients/position_keeping_adapter.go
package clients

// PositionKeepingAdapter implements ports.PositionReader
type PositionKeepingAdapter struct {
    client pb.PositionKeepingServiceClient
}

func (a *PositionKeepingAdapter) GetBalance(ctx context.Context, accountID string) (domain.Balance, error) {
    // Translate domain request → proto → domain response
    resp, err := a.client.ListFinancialPositionLogs(ctx, &pb.ListRequest{
        AccountId: accountID,
        Pagination: &common.Pagination{PageSize: 1},
    })
    if err != nil {
        return domain.Balance{}, fmt.Errorf("fetch position logs: %w", err)
    }

    // Convert proto → domain
    if len(resp.Logs) == 0 {
        return domain.Balance{}, fmt.Errorf("no position logs found for account %s", accountID)
    }

    log := resp.Logs[0]
    return domain.Balance{
        CurrentBalance:   protoMoneyToDomain(log.CurrentBalance),
        AvailableBalance: protoMoneyToDomain(log.AvailableBalance),
        LastUpdated:      log.UpdatedAt.AsTime(),
    }, nil
}
```sql

**Benefits:**

- Insulates current-account from proto changes in position-keeping
- Domain layer remains stable even if proto definitions change
- Clear separation between domain logic and integration concerns

**Testing:**

```go
// internal/current-account/service/account_service_test.go

// Mock PositionReader (domain interface) instead of gRPC client
type mockPositionReader struct {
    balance domain.Balance
    err     error
}

func (m *mockPositionReader) GetBalance(ctx context.Context, accountID string) (domain.Balance, error) {
    return m.balance, m.err
}

func TestAccountService_GetBalance(t *testing.T) {
    // Test with mock (no proto dependencies)
    mockReader := &mockPositionReader{
        balance: domain.Balance{
            CurrentBalance: domain.Money{Amount: 100, Currency: "USD"},
        },
    }

    service := NewAccountService(mockReader)
    balance, err := service.GetBalance(ctx, "account-123")

    require.NoError(t, err)
    assert.Equal(t, 100, balance.CurrentBalance.Amount)
}
```text

---

## Validation Criteria

### Automated Validation

#### 1. Coupling Analysis Passes

**Command:**

```bash
./scripts/analyze-coupling.sh > coupling-metrics-final.json

# Verify zero P0 violations
jq '.violations[] | select(.type == "cross-service-internal-import") | .type' coupling-metrics-final.json | wc -l
# Expected: 0

# Verify zero P1 violations (after Phase 1)
jq '.violations[] | select(.type == "internal-platform-import") | .type' coupling-metrics-final.json | wc -l
# Expected: 0
```protobuf

**Success Criteria:**

- Zero P0 violations (cross-service internal imports)
- Zero P1 violations (internal/platform imports)
- 14 safe proto imports (current-account → position-keeping, financial-accounting)

#### 2. All Tests Pass

**Command:**

```bash
# Unit tests
make test

# Integration tests
make test-integration

# Service-specific tests
cd cmd/current-account && go test -v ./...
cd cmd/position-keeping && go test -v ./...
cd cmd/financial-accounting && go test -v ./...
```text

**Success Criteria:**

- All unit tests pass (100% success rate)
- All integration tests pass
- No new test failures introduced
- Code coverage maintained or improved

#### 3. Services Build Successfully

**Command:**

```bash
# Build all services
make build

# Verify binaries created
ls -lh bin/current-account
ls -lh bin/position-keeping
ls -lh bin/financial-accounting

# Test service startup
./bin/current-account --help
./bin/position-keeping --help
./bin/financial-accounting --help
```text

**Success Criteria:**

- All services compile without errors
- Binaries are executable
- No runtime errors on startup

#### 4. Proto Breaking Change Detection

**Command:**

```bash
# Verify no breaking changes in proto definitions
buf breaking --against '.git#branch=develop'
```text

**Success Criteria:**

- No breaking changes detected
- All proto definitions backward compatible
- Event schemas follow ADR-0004 evolution rules

### Manual Validation

#### 1. Import Path Verification

**Check:**

```bash
# Verify NO internal/platform imports remain
rg "internal/platform" --type go ./cmd ./internal
# Expected: (empty)

# Verify ALL pkg/platform imports present
rg "pkg/platform/observability" --type go ./cmd ./internal | wc -l
rg "pkg/platform/kafka" --type go ./cmd ./internal | wc -l
rg "pkg/platform/testdb" --type go ./cmd ./internal | wc -l
rg "pkg/platform/auth" --type go ./cmd ./internal | wc -l
```go

**Success Criteria:**

- Zero `internal/platform` imports
- 7 `pkg/platform/observability` imports (3 services × cmd + internal)
- 3 `pkg/platform/kafka` imports (position-keeping, financial-accounting)
- 5 `pkg/platform/testdb` imports (test files)
- 1 `pkg/platform/auth` import (position-keeping)

#### 2. Service Dependency Documentation

**Check:**

```bash
# Verify DEPENDENCIES.md files exist
ls -la cmd/current-account/DEPENDENCIES.md
ls -la cmd/position-keeping/DEPENDENCIES.md
ls -la cmd/financial-accounting/DEPENDENCIES.md

# Verify content completeness
grep "Upstream Services" cmd/*/DEPENDENCIES.md
grep "Downstream Services" cmd/*/DEPENDENCIES.md
grep "Event Publications" cmd/*/DEPENDENCIES.md
grep "Coupling Metrics" cmd/*/DEPENDENCIES.md
```text

**Success Criteria:**

- All three services have DEPENDENCIES.md
- Each file documents upstream/downstream dependencies
- Event publications/subscriptions listed
- Coupling metrics included (Ca, Ce, I, assessment)

#### 3. CI Gates Functional

**Check:**

```bash
# Verify GitHub Actions workflow exists
cat .github/workflows/coupling-check.yml

# Verify pre-commit hook exists and is executable
ls -la .git/hooks/pre-commit
file .git/hooks/pre-commit  # Should show: executable

# Test pre-commit hook locally
./git/hooks/pre-commit
# Expected: "✅ Coupling analysis passed"
```protobuf

**Success Criteria:**

- coupling-check.yml workflow exists
- Workflow triggers on PR for relevant paths
- Pre-commit hook is executable
- Hook runs coupling analysis and blocks P0 violations

---

## Rollback Plan

### Rollback Scenario 1: Platform Migration Causes Test Failures

**Detection:**

- Test suite fails after import path updates
- Services fail to build
- Runtime errors on service startup

**Rollback Steps:**

```bash
cd ~/dev/github.com/meridianhub/meridian/meridian-main

# Option A: Revert single commit (if all changes in one commit)
git revert HEAD
git push origin 15-bian-service-boundaries

# Option B: Hard reset to previous commit (if multiple commits)
git log --oneline -10  # Find commit hash before migration
git reset --hard <previous-commit-hash>
git push origin 15-bian-service-boundaries --force

# Option C: Cherry-pick revert (if changes mixed with other work)
git revert <migration-commit-hash>
git push origin 15-bian-service-boundaries
```text

**Validation:**

```bash
# Verify services build
make build

# Verify tests pass
make test

# Verify imports restored
rg "internal/platform" --type go ./cmd ./internal | wc -l
# Expected: 17 (original violation count)
```sql

**Root Cause Analysis:**
After rollback, investigate:

1. Which test failed and why?
2. Was the import path update incomplete?
3. Did we miss updating go.mod?
4. Are there circular dependencies?

**Re-attempt Strategy:**

- Fix identified issues
- Re-run migration on smaller scope (one package at a time)
- Add additional validation steps

### Rollback Scenario 2: CI Gates Block Valid Changes

**Detection:**

- Pre-commit hook blocks legitimate changes
- GitHub Actions workflow fails incorrectly
- False positives in coupling analysis

**Rollback Steps:**

```bash
# Temporarily disable pre-commit hook
mv .git/hooks/pre-commit .git/hooks/pre-commit.disabled

# OR bypass hook for single commit
git commit --no-verify -m "Emergency fix"

# Disable GitHub Actions workflow temporarily
git mv .github/workflows/coupling-check.yml .github/workflows/coupling-check.yml.disabled
git commit -m "Temporarily disable coupling check workflow"
git push origin 15-bian-service-boundaries
```sql

**Root Cause Analysis:**

1. Review coupling analysis script for bugs
2. Check if script is producing false positives
3. Verify jq queries are correct
4. Test script on sample codebase

**Fix Strategy:**

- Update coupling analysis script to fix false positives
- Add exclusion patterns for legitimate patterns
- Re-enable CI gates after validation

### Rollback Scenario 3: Anti-Corruption Layer Breaks Service Interactions

**Detection:**

- Integration tests fail
- gRPC calls fail at runtime
- Data transformation errors

**Rollback Steps:**

```bash
# Revert anti-corruption layer changes only
git revert <acl-commit-hash>
git push origin 15-bian-service-boundaries

# Verify services work with original gRPC clients
make test-integration
```sql

**Root Cause Analysis:**

1. Which adapter failed (PositionKeepingAdapter or FinancialAccountingAdapter)?
2. Was proto → domain conversion correct?
3. Did we handle all error cases?
4. Are there missing fields in domain entities?

**Fix Strategy:**

- Fix transformation logic
- Add more comprehensive unit tests for adapters
- Add integration tests for adapter layer
- Re-attempt with smaller scope (one adapter at a time)

---

## Proto Alignment Validation

### Current Account Proto Validation

**Proto File:** `api/proto/meridian/current_account/v1/current_account.proto`

**Boundary Compliance Check:**

| Entity/RPC | Ownership | Compliant? | Notes |
|------------|-----------|------------|-------|
| `CurrentAccountFacility` | CurrentAccount | ✅ Yes | Core domain entity |
| `AccountBalance` | CurrentAccount | ✅ Yes | Aggregated from PositionKeeping via gRPC |
| `OverdraftConfiguration` | CurrentAccount | ✅ Yes | CurrentAccount configuration |
| `AccountTransaction` | CurrentAccount | ✅ Yes | Client-facing view |
| `InitiateCurrentAccount` RPC | CurrentAccount | ✅ Yes | BIAN Initiate pattern |
| `ExecuteDeposit` RPC | CurrentAccount | ✅ Yes | BIAN Execute behavior qualifier |
| `RetrieveCurrentAccount` RPC | CurrentAccount | ✅ Yes | BIAN Retrieve pattern |

**Violations:** None

**Validation:**

```bash
# Verify current-account proto imports no other service protos
rg "import.*position_keeping" api/proto/meridian/current_account/v1/current_account.proto
rg "import.*financial_accounting" api/proto/meridian/current_account/v1/current_account.proto
# Expected: (empty - no cross-service proto imports in entity definitions)

# Verify only common types imported
rg "import.*common" api/proto/meridian/current_account/v1/current_account.proto
# Expected: meridian/common/v1/types.proto (allowed)
```sql

### Position Keeping Proto Validation

**Proto File:** `api/proto/meridian/position_keeping/v1/position_keeping.proto`

**Boundary Compliance Check:**

| Entity/RPC | Ownership | Compliant? | Notes |
|------------|-----------|------------|-------|
| `FinancialPositionLog` | PositionKeeping | ✅ Yes | Aggregate root |
| `TransactionLogEntry` | PositionKeeping | ✅ Yes | Transaction records |
| `TransactionLineage` | PositionKeeping | ✅ Yes | Lineage tracking |
| `AuditTrailEntry` | PositionKeeping | ✅ Yes | Compliance audit |
| `StatusTracking` | PositionKeeping | ✅ Yes | Lifecycle status |
| `InitiateFinancialPositionLog` RPC | PositionKeeping | ✅ Yes | BIAN Initiate pattern |
| `InitiateFinancialPositionLogBatch` RPC | PositionKeeping | ✅ Yes | Batch creation (1-10,000 logs) |
| `UpdateFinancialPositionLog` RPC | PositionKeeping | ✅ Yes | BIAN Update pattern |
| `BulkImportTransactions` RPC | PositionKeeping | ✅ Yes | High-volume ingestion |
| `ListFinancialPositionLogs` RPC | PositionKeeping | ✅ Yes | Query with filtering |

**Violations:** None

**Validation:**

```bash
# Verify position-keeping proto imports no other service protos
rg "import.*current_account" api/proto/meridian/position_keeping/v1/position_keeping.proto
rg "import.*financial_accounting" api/proto/meridian/position_keeping/v1/position_keeping.proto
# Expected: (empty - no cross-service proto imports)

# Verify only common types imported
rg "import.*common" api/proto/meridian/position_keeping/v1/position_keeping.proto
# Expected: meridian/common/v1/types.proto (allowed)
```sql

### Financial Accounting Proto Validation

**Proto File:** `api/proto/meridian/financial_accounting/v1/financial_accounting.proto`

**Boundary Compliance Check:**

| Entity/RPC | Ownership | Compliant? | Notes |
|------------|-----------|------------|-------|
| `FinancialBookingLog` | FinancialAccounting | ✅ Yes | Aggregate root |
| `LedgerPosting` | FinancialAccounting | ✅ Yes | Debit/credit postings |
| `InitiateFinancialBookingLog` RPC | FinancialAccounting | ✅ Yes | BIAN Initiate pattern |
| `UpdateFinancialBookingLog` RPC | FinancialAccounting | ✅ Yes | BIAN Update pattern |
| `CaptureLedgerPosting` RPC | FinancialAccounting | ✅ Yes | BIAN Capture behavior qualifier |
| `UpdateLedgerPosting` RPC | FinancialAccounting | ✅ Yes | Update posting status/result |
| `ListFinancialBookingLogs` RPC | FinancialAccounting | ✅ Yes | Query with filtering |
| `ListLedgerPostings` RPC | FinancialAccounting | ✅ Yes | Query with filtering |

**Violations:** None

**Notes:**

- Double-entry balance validation occurs at **service layer** (not proto layer)
- Individual postings created separately (not balanced pairs)
- Booking log can only transition to POSTED status when balanced

**Validation:**

```bash
# Verify financial-accounting proto imports no other service protos
rg "import.*current_account" api/proto/meridian/financial_accounting/v1/financial_accounting.proto
rg "import.*position_keeping" api/proto/meridian/financial_accounting/v1/financial_accounting.proto
# Expected: (empty - no cross-service proto imports)

# Verify only common types and google types imported
rg "import" api/proto/meridian/financial_accounting/v1/financial_accounting.proto
# Expected:
# - meridian/common/v1/types.proto (allowed)
# - google/protobuf/timestamp.proto (allowed)
# - google/type/money.proto (allowed)
```sql

### Event Proto Validation

**Event Files:**

- `api/proto/meridian/events/v1/deposit_event.proto` (legacy)
- `api/proto/meridian/events/v1/financial_accounting_events.proto`
- `api/proto/meridian/events/v1/position_keeping_events.proto`

**Boundary Compliance Check:**

| Event | Publisher | Consumers | Compliant? | Notes |
|-------|-----------|-----------|------------|-------|
| `DepositEvent` | CurrentAccount | FinancialAccounting | ⚠️ Legacy | Being replaced by `TransactionInitiatedEvent` |
| `FinancialBookingLogInitiatedEvent` | FinancialAccounting | Audit, Reporting | ✅ Yes | Booking log created |
| `LedgerPostingCapturedEvent` | FinancialAccounting | Trial-balance, Account-summary | ✅ Yes | Posting created |
| `TransactionCapturedEvent` | PositionKeeping | Reconciliation, Reporting | ✅ Yes | Transaction log created |
| `TransactionPostedEvent` | PositionKeeping | Reporting, Analytics | ✅ Yes | Transaction finalized |

**Recommendations:**

1. **Deprecate DepositEvent**: Migrate to `TransactionInitiatedEvent` with `transaction_type = DEPOSIT`
2. **Create CurrentAccount Events**: Define `current_account_events.proto` with:
   - `AccountCreatedEvent`
   - `AccountStatusChangedEvent`
   - `TransactionInitiatedEvent`
   - `TransactionCompletedEvent`
3. **Event Schema Versioning**: Follow ADR-0004 for breaking change detection

**Validation:**

```bash
# Verify event protos exist
ls -la api/proto/meridian/events/v1/

# Check event imports (should only import common types)
rg "import" api/proto/meridian/events/v1/*.proto
# Expected: Only common types and google types

# Verify event schemas with buf
buf lint api/proto/meridian/events/
buf breaking --against '.git#branch=develop'
```text

---

## Test Strategy

### 1. Unit Tests (Boundary Validation)

**Test Platform Migration:**

```go
// pkg/platform/observability/observability_test.go

func TestObservabilityImport(t *testing.T) {
    // Verify package can be imported from pkg/platform
    logger := observability.NewLogger("test-service")
    assert.NotNil(t, logger)

    tracer := observability.NewTracer("test-service")
    assert.NotNil(t, tracer)
}
```text

**Test Service Independence:**

```go
// cmd/current-account/service_test.go

func TestCurrentAccountServiceImports(t *testing.T) {
    // Verify no internal imports from other services
    source, err := os.ReadFile("internal/current-account/service/account_service.go")
    require.NoError(t, err)

    // Check for forbidden imports
    assert.NotContains(t, string(source), "internal/position-keeping")
    assert.NotContains(t, string(source), "internal/financial-accounting")

    // Check for correct platform imports
    assert.Contains(t, string(source), "pkg/platform/observability")
}
```text

### 2. Integration Tests (Cross-Service Flows)

**Test CurrentAccount → PositionKeeping Flow:**

```go
// internal/current-account/service/account_service_integration_test.go

func TestExecuteDeposit_UpdatesPositionKeeping(t *testing.T) {
    // Start testcontainers for current-account and position-keeping
    caContainer := testcontainers.StartCurrentAccount(t)
    pkContainer := testcontainers.StartPositionKeeping(t)
    defer caContainer.Terminate(t)
    defer pkContainer.Terminate(t)

    // Execute deposit via current-account
    caClient := pb.NewCurrentAccountServiceClient(caContainer.GRPCConn())
    resp, err := caClient.ExecuteDeposit(ctx, &pb.ExecuteDepositRequest{
        AccountId: "account-123",
        Amount:    &common.MoneyAmount{Currency: common.Currency_CURRENCY_USD, Amount: 100},
    })
    require.NoError(t, err)
    assert.Equal(t, pb.TransactionStatus_TRANSACTION_STATUS_COMPLETED, resp.Status)

    // Verify position-keeping received transaction
    pkClient := pkpb.NewPositionKeepingServiceClient(pkContainer.GRPCConn())
    logsResp, err := pkClient.ListFinancialPositionLogs(ctx, &pkpb.ListRequest{
        AccountId: "account-123",
    })
    require.NoError(t, err)
    assert.Len(t, logsResp.Logs, 1)
    assert.Equal(t, int64(100), logsResp.Logs[0].TransactionLogEntries[0].Amount.Amount)
}
```text

**Test CurrentAccount → FinancialAccounting Flow:**

```go
// internal/current-account/service/account_service_integration_test.go

func TestExecuteDeposit_CreatesBookingLog(t *testing.T) {
    // Start testcontainers
    caContainer := testcontainers.StartCurrentAccount(t)
    faContainer := testcontainers.StartFinancialAccounting(t)
    defer caContainer.Terminate(t)
    defer faContainer.Terminate(t)

    // Execute deposit
    caClient := pb.NewCurrentAccountServiceClient(caContainer.GRPCConn())
    resp, err := caClient.ExecuteDeposit(ctx, &pb.ExecuteDepositRequest{
        AccountId: "account-123",
        Amount:    &common.MoneyAmount{Currency: common.Currency_CURRENCY_USD, Amount: 100},
    })
    require.NoError(t, err)

    // Verify financial-accounting created booking log
    faClient := fapb.NewFinancialAccountingServiceClient(faContainer.GRPCConn())
    postingsResp, err := faClient.ListLedgerPostings(ctx, &fapb.ListLedgerPostingsRequest{
        AccountId: "account-123",
    })
    require.NoError(t, err)
    assert.Len(t, postingsResp.LedgerPostings, 2)  // Debit + Credit

    // Verify double-entry balance
    debitTotal := int64(0)
    creditTotal := int64(0)
    for _, posting := range postingsResp.LedgerPostings {
        if posting.PostingDirection == common.PostingDirection_POSTING_DIRECTION_DEBIT {
            debitTotal += posting.PostingAmount.Units
        } else {
            creditTotal += posting.PostingAmount.Units
        }
    }
    assert.Equal(t, debitTotal, creditTotal, "Double-entry bookkeeping should balance")
}
```text

### 3. Contract Tests (Proto Compatibility)

**Test Proto Breaking Changes:**

```bash
# Add to CI workflow
buf breaking --against '.git#branch=develop,subdir=api/proto'

# Expected: Pass (no breaking changes)
```text

**Test Event Schema Compatibility:**

```go
// api/proto/meridian/events/v1/events_compatibility_test.go

func TestDepositEvent_BackwardCompatibility(t *testing.T) {
    // Create old-style DepositEvent
    oldEvent := &eventsv1.DepositEvent{
        AccountId: "account-123",
        Amount:    &money.Money{CurrencyCode: "USD", Units: 100},
    }

    // Serialize
    bytes, err := proto.Marshal(oldEvent)
    require.NoError(t, err)

    // Deserialize (should work with new code)
    newEvent := &eventsv1.DepositEvent{}
    err = proto.Unmarshal(bytes, newEvent)
    require.NoError(t, err)

    // Verify fields match
    assert.Equal(t, "account-123", newEvent.AccountId)
    assert.Equal(t, int64(100), newEvent.Amount.Units)
}
```text

### 4. Boundary Enforcement Tests

**Test Coupling Analysis Script:**

```bash
# Create test codebase with violations
mkdir -p /tmp/test-coupling/internal/service-a
mkdir -p /tmp/test-coupling/internal/service-b

# Add cross-service import (violation)
echo 'import "github.com/test/internal/service-a/domain"' > /tmp/test-coupling/internal/service-b/client.go

# Run coupling analysis
./scripts/analyze-coupling.sh /tmp/test-coupling > /tmp/coupling-test.json

# Verify violation detected
VIOLATIONS=$(jq '.violations[] | select(.type == "cross-service-internal-import") | .type' /tmp/coupling-test.json | wc -l)
assert [ "$VIOLATIONS" -eq 1 ]
```text

**Test Pre-Commit Hook:**

```bash
# Add violation to codebase
echo 'import "github.com/meridianhub/meridian/internal/position-keeping/domain"' >> internal/current-account/test_violation.go

# Attempt commit (should fail)
git add internal/current-account/test_violation.go
git commit -m "Test violation"
# Expected: "❌ COMMIT BLOCKED: 1 P0 violations"

# Remove violation
rm internal/current-account/test_violation.go
```sql

---

## Success Criteria

### Phase 1 Success Criteria (Platform Migration)

- [ ] All platform packages moved from `internal/platform/` to `pkg/platform/`
- [ ] All 17 import paths updated (observability, kafka, testdb, auth)
- [ ] Zero remaining `internal/platform` imports
- [ ] All unit tests pass (no regressions)
- [ ] All integration tests pass
- [ ] All services build successfully
- [ ] Coupling analysis shows zero P1 violations
- [ ] Git history preserved for moved packages

### Phase 2 Success Criteria (Documentation & CI)

- [ ] DEPENDENCIES.md created for all three services
- [ ] Each DEPENDENCIES.md includes upstream/downstream services, events, coupling metrics
- [ ] GitHub Actions workflow `coupling-check.yml` created
- [ ] Pre-commit hook created and executable
- [ ] CI workflow blocks P0 violations (cross-service imports)
- [ ] CI workflow warns on P1 violations (platform imports)
- [ ] Coupling metrics uploaded as artifacts
- [ ] Pre-commit hook tested locally

### Phase 3 Success Criteria (Anti-Corruption Layer - Optional)

- [ ] Domain interfaces defined for PositionReader and JournalWriter
- [ ] Adapters implemented for position-keeping and financial-accounting clients
- [ ] Current-account domain layer decoupled from proto definitions
- [ ] Unit tests use domain mocks (no proto dependencies)
- [ ] Integration tests pass with adapter layer
- [ ] No regressions in existing functionality

### Overall Project Success Criteria

- [ ] Zero P0 violations (cross-service internal imports)
- [ ] Zero P1 violations (internal/platform imports)
- [ ] All proto definitions align with service boundaries
- [ ] All services respect BIAN domain boundaries
- [ ] Event-driven architecture follows ADR-0004
- [ ] Documentation is complete and accurate
- [ ] CI gates prevent future boundary violations
- [ ] All tests pass (unit + integration + contract)
- [ ] Services deploy successfully to all environments

---

## Monitoring and Metrics

### Coupling Metrics Dashboard (Grafana)

**Metrics to Track:**

```promql
# Instability metrics (from coupling analysis)
coupling_instability{service="current-account"}
coupling_instability{service="position-keeping"}
coupling_instability{service="financial-accounting"}

# Violation counts
coupling_violations_total{priority="P0"}
coupling_violations_total{priority="P1"}

# Service dependencies
coupling_afferent_coupling{service="current-account"}
coupling_efferent_coupling{service="current-account"}
```text

**Alerts:**

```yaml
# Alert on new P0 violations
- alert: ServiceBoundaryViolation
  expr: coupling_violations_total{priority="P0"} > 0
  for: 1m
  labels:
    severity: critical
  annotations:
    summary: "Service boundary violation detected"
    description: "{{ $value }} P0 violations detected (cross-service internal imports)"

# Alert on instability increase
- alert: ServiceInstabilityIncreased
  expr: delta(coupling_instability{service="position-keeping"}[1h]) > 0.1
  for: 5m
  labels:
    severity: warning
  annotations:
    summary: "Service instability increased"
    description: "{{ $labels.service }} instability increased by {{ $value }}"
```sql

### Continuous Monitoring Schedule

**Daily:**

- Run coupling analysis in CI for all PRs
- Review coupling metrics dashboard
- Alert on P0 violations

**Weekly:**

- Review coupling trends (instability, afferent/efferent coupling)
- Update coupling metrics documentation
- Identify new hotspots or violations

**Monthly:**

- Comprehensive boundary review
- Update migration plan if needed
- Review and improve CI gates

**Quarterly:**

- Architecture review with stakeholders
- Reassess BIAN boundary alignment
- Update ADRs based on learnings

---

## Appendix: Detailed Command Reference

### Coupling Analysis Commands

```bash
# Run full coupling analysis
./scripts/analyze-coupling.sh > coupling-report.json

# Check P0 violations (critical)
jq '.violations[] | select(.type == "cross-service-internal-import")' coupling-report.json

# Check P1 violations (high)
jq '.violations[] | select(.type == "internal-platform-import")' coupling-report.json

# View instability metrics
jq '.services | to_entries[] | "\(.key): I=\(.value.instability) (\(.value.assessment))"' coupling-report.json

# Export metrics to CSV
jq -r '.services | to_entries[] | [.key, .value.afferent_coupling, .value.efferent_coupling, .value.instability] | @csv' coupling-report.json > coupling-metrics.csv
```text

### Import Path Migration Commands

```bash
# Find all internal/platform imports
rg "internal/platform" --type go ./cmd ./internal -l

# Replace internal/platform with pkg/platform
find ./cmd ./internal -name "*.go" -type f -exec sed -i '' \
  's|github.com/meridianhub/meridian/internal/platform|github.com/meridianhub/meridian/pkg/platform|g' {} \;

# Verify replacements
rg "pkg/platform" --type go ./cmd ./internal -c
```text

### Proto Validation Commands

```bash
# Lint proto files
buf lint api/proto/

# Check for breaking changes
buf breaking --against '.git#branch=develop'

# Generate Go code from protos
buf generate

# Verify proto imports
rg "import.*proto.*meridian" api/proto/meridian/current_account/v1/current_account.proto
```text

### Test Execution Commands

```bash
# Run all unit tests
make test

# Run specific service tests
go test ./internal/current-account/...
go test ./internal/position-keeping/...
go test ./internal/financial-accounting/...

# Run with coverage
go test -cover ./...
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# Run integration tests
make test-integration

# Run tests with race detector
go test -race ./...
```text

### Build and Deploy Commands

```bash
# Build all services
make build

# Build specific service
go build -o bin/current-account ./cmd/current-account

# Run services locally
./bin/current-account
./bin/position-keeping
./bin/financial-accounting

# Deploy to Kubernetes (example)
kubectl apply -f k8s/current-account/deployment.yaml
kubectl apply -f k8s/position-keeping/deployment.yaml
kubectl apply -f k8s/financial-accounting/deployment.yaml
```text

---

**Document Version:** 1.0
**Last Updated:** 2025-11-19
**Next Review:** 2026-02-19 (quarterly)
