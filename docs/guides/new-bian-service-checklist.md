---
name: new-bian-service-checklist
description: Complete checklist for creating a new BIAN service in Meridian
triggers:
  - Creating a new BIAN service
  - Implementing a new service domain
  - Scaffolding a microservice
  - What files do I need for a new service
instructions: |
  Follow ADR-015 directory structure. Create proto, domain, adapters, service,
  observability, atlas, k8s, and Tilt integration. Use existing services
  (party, current-account) as reference patterns.
---

# New BIAN Service Checklist

This guide provides a comprehensive checklist for creating a new BIAN service domain in
Meridian. It follows the standard directory structure defined in
[ADR-015](../adr/0015-standard-service-directory-structure.md) and can be used as a Task
Master PRD for automated task generation.

## Purpose

When adding a new BIAN service domain (e.g., Party, Current Account, Payment Order), this
checklist ensures all required components are created consistently. Each numbered section
represents a discrete implementation task with clear deliverables.

## Prerequisites

Before starting, ensure you have:

- Understanding of the BIAN service domain you're implementing
- Access to existing services for reference patterns (party, current-account, financial-accounting)
- Development environment configured (Go 1.25, Tilt, Atlas, Buf, Docker)
- Testcontainers working for integration tests

## Reference Services

| Service | Best Reference For |
|---------|-------------------|
| `services/party/` | Complete minimal service (proto through deployment) |
| `services/current-account/` | Inter-service clients, complex domain logic |
| `services/financial-accounting/` | Kafka messaging, complex observability |

---

## Service Creation Tasks

### Task 1: Proto Definition

**Location**: `api/proto/meridian/{service}/v1/`

Create the Protocol Buffer definitions that define the service contract.

**Files to create:**

- `{service}.proto` - Main service definition with:
  - Entity messages (e.g., `Party`, `Account`)
  - Enum types (e.g., `PartyType`, `AccountStatus`)
  - Request/response messages for each RPC
  - Service definition with RPC methods
  - `buf.validate` annotations for request validation

**Reference**: `api/proto/meridian/party/v1/party.proto`

**Example structure:**

```protobuf
syntax = "proto3";

package meridian.{service}.v1;

import "buf/validate/validate.proto";
import "google/protobuf/timestamp.proto";

option go_package = "github.com/meridianhub/meridian/api/proto/meridian/{service}/v1;{service}v1";

// Entity message
message {Entity} {
  string id = 1;
  // ... fields with validation
}

// Service definition
service {Service}Service {
  rpc Create{Entity}(Create{Entity}Request) returns (Create{Entity}Response);
  rpc Get{Entity}(Get{Entity}Request) returns (Get{Entity}Response);
  // ... other RPCs
}
```

**Verification:**

```bash
make proto                    # Generate Go code
buf lint api/proto            # Lint proto files
go build ./...                # Verify generated code compiles
```

---

### Task 2: Domain Model with Invariants

**Location**: `services/{service}/domain/`

Create the domain model with business rules and validation.

**Files to create:**

- `{entity}.go` - Domain entity with:
  - Private fields with public getters
  - Constructor (`New{Entity}`) enforcing invariants
  - Mutation methods that validate state transitions
  - Value objects (enums, status types)
- `{entity}_test.go` - Unit tests for domain logic

**Reference**: `services/party/domain/party.go`

**Key patterns:**

```go
// Constructor with invariant enforcement
func NewParty(partyType PartyType, legalName string) (*Party, error) {
    if legalName == "" {
        return nil, ErrLegalNameRequired
    }
    // ... validation
    return &Party{
        id:        uuid.New(),
        partyType: partyType,
        legalName: legalName,
        status:    PartyStatusActive,
        version:   1,
        createdAt: time.Now(),
        updatedAt: time.Now(),
    }, nil
}

// Reconstruction for loading from database
func ReconstructParty(id uuid.UUID, /* all fields */) *Party {
    return &Party{id: id, /* ... */}
}
```

**Repository interface (port):**

```go
// Repository defines the persistence contract (port in hexagonal architecture)
type Repository interface {
    Save(ctx context.Context, entity *Entity) error
    FindByID(ctx context.Context, id uuid.UUID) (*Entity, error)
    // ... other methods
}
```

**Verification:**

```bash
go test ./services/{service}/domain/... -v
```

---

### Task 3: GORM Persistence Layer

**Location**: `services/{service}/adapters/persistence/`

Implement the repository using GORM.

**Files to create:**

- `{entity}_entity.go` - Database entity with GORM tags
- `repository.go` - Repository implementation with:
  - Optimistic locking via version field
  - Soft delete support
  - Context-based audit fields
- `repository_test.go` - Integration tests with Testcontainers

**Reference**: `services/party/adapters/persistence/`

**Entity example:**

```go
type {Entity}Entity struct {
    ID        uuid.UUID      `gorm:"type:uuid;primaryKey"`
    // ... fields matching schema
    Version   int64          `gorm:"not null;default:1"`
    CreatedAt time.Time      `gorm:"not null"`
    UpdatedAt time.Time      `gorm:"not null"`
    DeletedAt gorm.DeletedAt `gorm:"index"`
    CreatedBy string         `gorm:"not null"`
    UpdatedBy string         `gorm:"not null"`
}

// TableName uses singular unqualified name to allow PostgreSQL search_path routing.
func ({Entity}Entity) TableName() string {
    return "{entity}"  // singular, unqualified (e.g., "party", "account")
}
```

**Optimistic locking pattern:**

```go
// Check version matches before update
expectedDBVersion := entity.Version - 1
result := r.db.Model(&Entity{}).
    Where("id = ? AND version = ?", id, expectedDBVersion).
    Updates(/* ... */)

if result.RowsAffected == 0 {
    return ErrVersionConflict
}
```

**Verification:**

```bash
go test ./services/{service}/adapters/persistence/... -v
```

---

### Task 4: Atlas Migration Configuration

**Location**: `services/{service}/atlas/`

Configure Atlas for schema management.

**Files to create:**

- `atlas.hcl` - Atlas configuration with:
  - Schema name
  - Migration directory
  - Environment configurations (local, ci, production)
  - External schema loader

**Reference**: `services/current-account/atlas/atlas.hcl`

**Example configuration:**

```hcl
data "external_schema" "{service}" {
  program = ["./utilities/atlas-loader", "--schema={service}"]
}

env "local" {
  src = data.external_schema.{service}.url
  dev = "docker://postgres/16/dev?search_path=public"
  migration {
    dir = "file://services/{service}/migrations"
  }
  format {
    migrate {
      diff = "{{ sql . \"  \" }}"
    }
  }
}

env "ci" {
  src = data.external_schema.{service}.url
  dev = "docker://postgres/16/dev?search_path=public"
  migration {
    dir    = "file://services/{service}/migrations"
    format = atlas
  }
  lint {
    destructive {
      error = true
    }
    data_depend {
      error = true
    }
  }
}
```

**Verification:**

```bash
atlas migrate status --env local --config file://services/{service}/atlas/atlas.hcl
```

---

### Task 5: Database Setup (Database-Per-Service Architecture)

**Location**: CockroachDB administration

Set up the dedicated database for the new service following the database-per-service architecture.

> **Note**: Meridian uses CockroachDB (port 26257) which implements the PostgreSQL wire protocol.
> This means `postgres://` connection strings and `psql` CLI tools work, but the underlying
> database is CockroachDB.

**Steps:**

#### Step 5.1: Create Service Database

```sql
-- Connect as admin user
CREATE DATABASE meridian_{service};
```

#### Step 5.2: Create Service User

```sql
-- Create dedicated user with restricted access
CREATE USER {service}_svc WITH PASSWORD '<secure-password>';

-- Grant access only to this service's database
GRANT ALL ON DATABASE meridian_{service} TO {service}_svc;
```

#### Step 5.3: Verify Isolation

```sql
-- As {service}_svc, verify cannot access other databases
\c meridian_party
-- Should fail: permission denied
```

**Table naming convention:**

Use **singular, unqualified** table names:

```go
// Entity TableName() method - REQUIRED
func (EntityName) TableName() string {
    return "entity_name"  // singular, no schema prefix
}
```

**Why singular**: Natural SQL syntax (`FROM account` not `FROM accounts`)
**Why unqualified**: Allows `search_path` to route queries to tenant schemas

**Reference**: [Database-Per-Service Migration Runbook](../runbooks/database-per-service-migration.md)

**Verification:**

```bash
# Verify database exists and user can connect (CockroachDB on port 26257)
psql -h localhost -p 26257 -U {service}_svc -d meridian_{service} -c "SELECT current_database();"
```

---

### Task 6: Generate Initial Schema Migration

**Location**: `services/{service}/migrations/`

Generate and apply the initial database migration.

**Commands:**

```bash
# Generate migration from GORM entities
atlas migrate diff initial \
  --env local \
  --config file://services/{service}/atlas/atlas.hcl

# Review generated SQL in services/{service}/migrations/
# Verify: tables, constraints, indexes, enums

# Apply migration locally (uses credentials from Task 5)
atlas migrate apply \
  --env local \
  --config file://services/{service}/atlas/atlas.hcl \
  --url "postgres://{service}_svc@localhost:26257/meridian_{service}?sslmode=disable"
```

**Migration file naming**: `YYYYMMDDHHMMSS_description.sql`

**Verification:**

```bash
# Query database to confirm schema (uses credentials from Task 5)
psql -h localhost -p 26257 -U {service}_svc -d meridian_{service} -c "\dt"
```

---

### Task 7: gRPC Service Handler

**Location**: `services/{service}/service/`

Implement the gRPC service.

**Files to create:**

- `grpc_service.go` - Service implementation with:
  - Embedded `pb.Unimplemented{Service}Server`
  - Repository and logger injection
  - RPC method implementations
  - Request validation, domain operations, error mapping
- `mappers.go` - Proto to domain conversions
- `grpc_service_test.go` - Unit tests with mock repository
- `grpc_service_integration_test.go` - Integration tests

**Reference**: `services/party/service/grpc_service.go`

**Service structure:**

```go
type Service struct {
    pb.Unimplemented{Service}Server
    repo   domain.Repository
    logger *slog.Logger
}

func NewService(repo domain.Repository, logger *slog.Logger) (*Service, error) {
    if repo == nil {
        return nil, errors.New("repository is required")
    }
    return &Service{repo: repo, logger: logger}, nil
}

func (s *Service) Create{Entity}(
    ctx context.Context,
    req *pb.Create{Entity}Request,
) (*pb.Create{Entity}Response, error) {
    // 1. Validate request (protovalidate handles this via interceptor)
    // 2. Map to domain
    // 3. Execute domain operation
    // 4. Persist via repository
    // 5. Map to response
    // 6. Return or handle errors with appropriate gRPC status codes
}
```

**Error mapping:**

```go
switch {
case errors.Is(err, domain.ErrNotFound):
    return nil, status.Error(codes.NotFound, "entity not found")
case errors.Is(err, persistence.ErrVersionConflict):
    return nil, status.Error(codes.Aborted, "concurrent modification detected")
default:
    return nil, status.Error(codes.Internal, "internal error")
}
```

**Verification:**

```bash
go test ./services/{service}/service/... -v
```

---

### Task 8: Health Check Implementation

**Location**: `services/{service}/service/health.go`

Implement gRPC health checks.

**Files to create:**

- `health.go` - Health checker with:
  - `grpc_health_v1.HealthServer` implementation
  - Database health checker
  - Health aggregator using `shared/pkg/health`

**Reference**: `services/party/service/health.go`

**Structure:**

```go
type HealthChecker struct {
    grpc_health_v1.UnimplementedHealthServer
    repo        *persistence.Repository
    logger      *slog.Logger
    aggregator  *health.Aggregator
    serviceName string
}

// DatabaseHealthChecker implements health.Checker
type DatabaseHealthChecker struct {
    repo    *persistence.Repository
    timeout time.Duration
}

func (d *DatabaseHealthChecker) Name() string { return "database" }

func (d *DatabaseHealthChecker) Check(ctx context.Context) health.ComponentResult {
    err := d.repo.Ping(ctx)
    // Return appropriate status based on result
}
```

**Verification:**

```bash
# Manual test after service is running
grpcurl -plaintext localhost:50055 grpc.health.v1.Health/Check
```

---

### Task 9: Service Entry Point

**Location**: `services/{service}/cmd/`

Create the application entry point.

**Files to create:**

- `main.go` - Entry point with:
  - Structured logging (slog JSON handler)
  - Environment variable configuration
  - Database connection with GORM
  - OpenTelemetry tracer initialization
  - gRPC server with interceptors
  - Health service registration
  - Graceful shutdown handling

**Reference**: `services/party/cmd/main.go`

**Key components:**

```go
func main() {
    // 1. Initialize logging
    logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
        Level: parseLogLevel(os.Getenv("LOG_LEVEL")),
    }))

    // 2. Run service
    if err := run(logger); err != nil {
        logger.Error("service failed", "error", err)
        os.Exit(1)
    }
}

func run(logger *slog.Logger) error {
    // 1. Initialize tracer
    // 2. Initialize database
    // 3. Create repository
    // 4. Create service
    // 5. Create gRPC server with interceptors
    // 6. Register services (main + health + reflection)
    // 7. Start server
    // 8. Wait for shutdown signal
    // 9. Graceful shutdown
}
```

**Environment variables:**

| Variable | Description | Default |
|----------|-------------|---------|
| `GRPC_PORT` | gRPC server port | `50055` |
| `DATABASE_URL` | PostgreSQL connection string | (required) |
| `LOG_LEVEL` | Logging level | `info` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OpenTelemetry endpoint | (optional) |

**Verification:**

```bash
go run services/{service}/cmd/main.go
# In another terminal:
grpcurl -plaintext localhost:50055 list
```

---

### Task 10: Dockerfile

**Location**: `services/{service}/cmd/Dockerfile`

Create a multi-stage Docker build.

**Reference**: `services/current-account/cmd/Dockerfile`

**Structure:**

```dockerfile
# Stage 1: Build
FROM golang:1.25.7-bookworm AS builder

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-X main.Version=${VERSION} -X main.Commit=${COMMIT} -X main.BuildDate=${BUILD_DATE}" \
    -o /{service} ./services/{service}/cmd/

# Stage 2: Runtime
FROM gcr.io/distroless/static:nonroot

COPY --from=builder /{service} /
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

USER 65532:65532
EXPOSE 50055

ENTRYPOINT ["/{service}"]
```

**Verification:**

```bash
docker build -t {service}:dev -f services/{service}/cmd/Dockerfile .
docker run --rm -e DATABASE_URL="postgres://..." {service}:dev
```

---

### Task 11: Kubernetes Manifests

**Location**: `services/{service}/k8s/`

Create Kubernetes deployment manifests.

**Files to create:**

- `configmap.yaml` - Non-sensitive configuration
- `secret.yaml` - Sensitive configuration (DATABASE_URL)
- `deployment.yaml` - Pod specification
- `service.yaml` - ClusterIP service

**Reference**: `services/current-account/k8s/`

**Deployment key sections:**

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {service}
  labels:
    app: {service}
    component: microservice
    tier: backend
spec:
  replicas: 2
  selector:
    matchLabels:
      app: {service}
  template:
    spec:
      containers:
      - name: {service}
        image: {service}:latest
        ports:
        - name: grpc
          containerPort: 50055
        envFrom:
        - configMapRef:
            name: {service}-config
        env:
        - name: DATABASE_URL
          valueFrom:
            secretKeyRef:
              name: {service}-secrets
              key: DATABASE_URL
        resources:
          requests:
            cpu: 50m
            memory: 64Mi
          limits:
            cpu: 200m
            memory: 256Mi
        livenessProbe:
          grpc:
            port: 50055
          initialDelaySeconds: 10
        readinessProbe:
          grpc:
            port: 50055
          initialDelaySeconds: 5
        securityContext:
          runAsNonRoot: true
          readOnlyRootFilesystem: true
          allowPrivilegeEscalation: false
```

**Verification:**

```bash
kubectl apply --dry-run=client -f services/{service}/k8s/
```

---

### Task 12: Tilt Integration

**Location**: `Tiltfile` (root)

Add the service to local development environment.

**Add to Tiltfile:**

```python
# {Service} service
docker_build(
    '{service}',
    '.',
    dockerfile='services/{service}/cmd/Dockerfile',
    build_args={
        'VERSION': 'dev',
        'COMMIT': local('git rev-parse --short HEAD'),
        'BUILD_DATE': local('date -u +%Y-%m-%dT%H:%M:%SZ'),
    },
)

k8s_yaml([
    'services/{service}/k8s/configmap.yaml',
    'services/{service}/k8s/secret.yaml',
    'services/{service}/k8s/deployment.yaml',
    'services/{service}/k8s/service.yaml',
])

k8s_resource(
    '{service}',
    port_forwards=['50055:50055'],
    resource_deps=['cockroachdb'],
    labels=['services'],
)
```

**Verification:**

```bash
tilt up
# Verify service appears in Tilt UI and health checks pass
```

---

### Task 13: Integration Tests

**Location**: `services/{service}/service/` or `tests/`

Create comprehensive integration tests.

**Test coverage:**

- Full gRPC request/response flows
- Error conditions (validation, not found, conflicts)
- Database interactions (CRUD, transactions, optimistic locking)
- Health check endpoints

**Reference**: `services/party/service/grpc_service_integration_test.go`

**Test setup pattern:**

```go
func TestIntegration(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping integration test")
    }

    db, cleanup := testdb.SetupPostgres(t, []interface{}{&persistence.{Entity}Entity{}})
    defer cleanup()

    repo := persistence.NewRepository(db)
    svc, _ := service.NewService(repo, slog.Default())

    // Test cases...
}
```

**Verification:**

```bash
go test ./services/{service}/... -v -count=1
```

---

## Additional Considerations

### Inter-Service Clients (Optional)

**Location**: `services/{service}/client/`

If your service needs to call other services:

- Create service-owned gRPC client using `shared/pkg/clients` for resilience
- Reference: `docs/guides/circuit-breaker-usage.md`
- Reference: `services/position-keeping/client/client.go` (canonical example)
- Reference: `shared/pkg/clients/doc.go` for migration guidance

### Event Publishing (Optional)

**Location**: `services/{service}/adapters/messaging/`

For Kafka integration:

- Follow [ADR-004](../adr/0004-event-schema-evolution.md) for event schema evolution
- Reference: `services/financial-accounting/adapters/messaging/`

### Multi-Tenancy / Organization Scoping

Use `shared/platform/organization` context patterns for tenant isolation.

### Audit Logging

Follow [ADR-009](../adr/0009-application-level-audit-logging.md) for application-level
audit logging using `shared/platform/audit`.

---

## Task 14: Starlark & CEL Integration (Conditional)

**Applies when**: The service has tenant-definable business logic (pricing rules,
validation policies, workflow orchestration, conditional routing).

**Decision framework**: If a tenant might reasonably want to customise behaviour
without redeploying, the service needs Starlark, CEL, or both.

### When to Use What

| Mechanism | Use Case | Execution | Examples |
|-----------|----------|-----------|---------|
| **CEL** | Validation rules, conditional expressions, pricing formulas | < 10ms, no loops, pure functions | `amount > 0 && amount <= account.credit_limit` |
| **Starlark** | Multi-step workflows, orchestration, compensation logic | < 5s, finite loops only, deterministic | Saga scripts, settlement flows |
| **Both** | Starlark for workflow, CEL for hot-path decisions within it | Starlark calls `cel_eval()` inline | Saga with conditional pricing |

### Existing Patterns as Reference

Meridian has two established Starlark/CEL integration patterns. Each service context
gets a **different set of built-ins** based on its security requirements:

#### Saga Pattern (Payment Order, Current Account)

**Built-ins available:**

- `Decimal()`, `posting()`, `saga()`, `step()` - Core DSL
- `resolve_account()`, `resolve_instrument()` - Reference data lookups
- `invoke_saga()` - Child saga invocation with scope inheritance
- `cel_eval()` - Inline CEL evaluation
- `log()`, `fail()` - Observability and control flow
- Service modules (auto-generated from `handlers.yaml`)

**Blocked:** `load()`, `time.now()`, `random()`, `exec()`, `open()`, `http`

**Reference**: `shared/pkg/saga/builtins.go`

#### Valuation Pattern (Valuation Engine)

**Built-ins available:**

- `Decimal()`, `cel_eval()` - Financial calculations
- Market data lookups - Read-only price/rate access
- Mathematical functions - No side effects

**Not available:** `posting()`, `resolve_account()`, `invoke_saga()` - Valuation
scripts must not mutate ledger state or invoke workflows.

**Reference**: `services/valuation/` and valuation PRD

#### Choosing Built-ins for a New Service

When adding Starlark support to a new service, explicitly decide which built-ins
to expose. The principle is **least privilege by default**:

```go
// In your service's handler registration
func RegisterHandlers(registry *saga.HandlerRegistry) {
    // Only expose what this service context needs
    registry.RegisterBuiltin("Decimal", saga.DecimalBuiltin)
    registry.RegisterBuiltin("cel_eval", saga.CELEvalBuiltin)

    // Service-specific handlers from handlers.yaml
    registry.RegisterServiceModule("my_service", myHandlers)

    // DO NOT register invoke_saga if this context shouldn't
    // orchestrate workflows
    // DO NOT register posting() if this context shouldn't
    // write to the ledger
}
```

**Security checklist for new Starlark contexts:**

- [ ] Can scripts in this context mutate ledger state? If no, omit `posting()`
- [ ] Can scripts orchestrate child workflows? If no, omit `invoke_saga()`
- [ ] Can scripts resolve accounts/instruments? If no, omit `resolve_*`
- [ ] Does the Conservation of Dimension rule apply? (Physics instruments
  cannot produce themselves - `shared/pkg/saga/handlers.go`)
- [ ] Are scripts tenant-scoped via `PartyScope`?

### Adding Service Bindings (handlers.yaml)

If your service exposes handlers callable from Starlark scripts:

1. Add handler definitions to `shared/pkg/saga/schema/handlers.yaml`
2. Implement handler functions following the pattern in
   `docs/guides/adding-starlark-service-bindings.md`
3. Register handlers with the saga engine
4. Auto-generated service modules provide type-safe Starlark clients

**Handler definition example:**

```yaml
handlers:
  {service}.{operation}:
    description: "What this handler does"
    category: ingestion | valuation | settlement
    params:
      account_id:
        type: string
        required: true
      amount:
        type: Decimal
        required: true
    compensate: {service}.{reverse_operation}
```

**Handler categories determine security boundaries:**

- **ingestion** - Imports external data (read-write to own tables only)
- **valuation** - Computes derived values (read-only, no side effects)
- **settlement** - Executes financial operations (full ledger access)

---

## Task 15: Control Plane Manifest Registration

**Applies when**: The service is tenant-configurable and needs to be provisioned
as part of the manifest apply workflow.

The control plane uses a Kubernetes-style **declare-diff-plan-apply** pipeline.
When a tenant applies a manifest, the control plane:

1. **Validates** the manifest (proto constraints, CEL compilation, Starlark syntax)
2. **Diffs** against last-applied manifest
3. **Plans** an ordered sequence of gRPC calls
4. **Applies** each call in phase order with idempotency keys

### What to Add

#### Step 15.1: Manifest Proto

Add your service's configuration to `api/proto/meridian/control_plane/v1/manifest.proto`:

```protobuf
// Add message for your service's configuration
message {Service}Config {
  // Define tenant-configurable fields
  repeated {Service}Rule rules = 1;
  // CEL expressions, Starlark scripts, thresholds, etc.
}

// Add field to Manifest message
message Manifest {
  // ... existing fields ...
  // Check current highest field number in manifest.proto and use next
  {Service}Config {service}_config = N;
}
```

#### Step 15.2: Validator

Extend `services/control-plane/internal/validator/manifest_validator.go`:

```go
func (v *ManifestValidator) validate{Service}(
    manifest *controlplanev1.Manifest,
    result *ValidationResult,
) {
    cfg := manifest.Get{Service}Config()
    if cfg == nil {
        return  // Optional section, skip if absent
    }
    // Validate CEL expressions compile
    // Validate Starlark scripts parse
    // Check references to instruments/account types exist in manifest
}
```

#### Step 15.3: Differ

Add resource type and diff method to
`services/control-plane/internal/differ/manifest_differ.go`:

```go
const Resource{Service}Config differ.ResourceType = "{service}_config"

func (d *ManifestDiffer) diff{Service}(
    lastApplied, newManifest *controlplanev1.Manifest,
    plan *DiffPlan,
) {
    // Compare old vs new config, emit CREATE/UPDATE/DELETE actions
}
```

#### Step 15.4: Planner

Map to execution phases in
`services/control-plane/internal/planner/types.go`:

```go
// Add phase constant - order determines execution sequence
// Existing phases (from planner/types.go):
//   PhaseInstruments    Phase = 1
//   PhaseAccountTypes   Phase = 2
//   PhaseValuationRules Phase = 3
//   PhaseSagas          Phase = 4
//   PhaseSeedData       Phase = 5
const Phase{Service} Phase = 6  // Next available; adjust based on dependencies

// Add gRPC method mappings
const Method{Service}Configure GRPCMethod = (
    "meridian.{service}.v1.{Service}Service/Configure"
)
```

**Phase ordering rule**: Phases execute in numeric order. If your service depends
on instruments (phase 1) or account types (phase 2), use a higher phase number.
Most new services slot in at phase 6+.

### Checklist

- [ ] Proto: Add config message to `manifest.proto`
- [ ] Validator: Add `validate{Service}()` method
- [ ] Differ: Add resource type constant and `diff{Service}()` method
- [ ] Planner: Add phase constant, gRPC method mapping, wire into
  `phaseForResource()` and `grpcMethodMap`
- [ ] Tests: Unit tests for validator, differ, planner with new resource type
- [ ] Service: Implement the `Configure` RPC that the applier will call

**Reference files:**

- `services/control-plane/internal/validator/manifest_validator.go`
- `services/control-plane/internal/differ/manifest_differ.go`
- `services/control-plane/internal/planner/types.go`
- `services/control-plane/internal/planner/manifest_planner.go`

---

## Task 16: End-to-End Test Blueprint

**Applies to**: All new services. E2E tests verify cross-service state consistency
and are required before a service is considered production-ready.

### Test Architecture

Meridian e2e tests use **database-per-service testcontainers** to verify state
changes across bounded contexts. Each service gets its own CockroachDB instance,
matching the production database-per-service architecture.

### Required: Cross-Service E2E Test

**Location**: Two patterns exist depending on scope:

- `tests/{service}-e2e/` - Cross-service tests (multiple service databases,
  verifies interactions between bounded contexts)
- `services/{service}/e2e/` - Service-scoped tests (single service, verifies
  internal saga compensation and schema isolation)

Use `tests/` for new services that interact with other services. Use
`services/{service}/e2e/` when testing complex internal flows (e.g., saga
compensation) that don't require standing up other services.

Create an e2e test that exercises the service's interactions with its
dependencies using real gRPC calls where possible.

**Infrastructure setup pattern:**

```go
type e2eTestInfra struct {
    {service}DB          *gorm.DB
    // Add a database for each service this service interacts with
    positionKeepingDB    *gorm.DB
    financialAccountingDB *gorm.DB
    // gRPC servers for real inter-service calls
    {service}Server      *grpc.Server
}

func setupE2EInfra(t *testing.T) *e2eTestInfra {
    infra := &e2eTestInfra{}

    // Each service gets its own CockroachDB testcontainer
    // Always capture and defer the cleanup function
    var cleanup func()
    infra.{service}DB, cleanup = testdb.SetupCockroachDB(t, nil)
    t.Cleanup(cleanup)
    infra.positionKeepingDB, cleanup = testdb.SetupCockroachDB(t, nil)
    t.Cleanup(cleanup)

    return infra
}
```

**Reference**: `tests/clearinge2e/` (deposit/withdrawal flows across 4 services)

### Preferred: Real gRPC Between Services

Where feasible, prefer standing up real gRPC servers in tests rather than only
verifying database state. This catches serialisation issues, interceptor bugs,
and context propagation failures that database-only tests miss.

```go
func setupGRPCServer(t *testing.T, db *gorm.DB) (
    *grpc.Server, string, // addr
) {
    repo := persistence.NewRepository(db)
    svc, _ := service.NewService(repo, slog.Default())

    lis, _ := net.Listen("tcp", "localhost:0")
    srv := grpc.NewServer(
        // Use the same interceptors as production
        grpc.ChainUnaryInterceptor(
            tenant.UnaryServerInterceptor(),
            observability.UnaryServerInterceptor(tracer),
        ),
    )
    pb.Register{Service}Server(srv, svc)
    go srv.Serve(lis)

    t.Cleanup(func() { srv.GracefulStop() })
    return srv, lis.Addr().String()
}
```

**Use real gRPC calls for:**

- Inter-service calls (service A calling service B)
- Tenant context propagation (verify interceptors work end-to-end)
- Error code mapping (gRPC status codes)
- Idempotency key propagation

**Use direct database verification for:**

- Final state assertions (query each service's DB independently)
- Balanced ledger checks (sum across databases = 0)
- Audit trail verification (events in correct service DB)

### Multi-Tenant Isolation Test

Every e2e suite must verify tenant isolation:

```go
func TestTenantIsolation(t *testing.T) {
    infra := setupE2EInfra(t)

    // Create schemas for two tenants in each service DB
    ctxA, tenantA := setupTestTenant(t, infra, "tenant_a")
    ctxB, tenantB := setupTestTenant(t, infra, "tenant_b")

    // Create data as tenant A
    createEntity(t, ctxA, infra.{service}DB, tenantA.SchemaName(), ...)

    // Verify tenant B cannot see tenant A's data
    entities := listEntities(t, ctxB, infra.{service}DB, tenantB.SchemaName())
    assert.Empty(t, entities, "tenant B must not see tenant A data")
}
```

### Test Coverage Requirements

| Category | Required | Pattern |
|----------|----------|---------|
| Happy path flow | Yes | Full request-to-state-change cycle via gRPC |
| Error conditions | Yes | Validation failures, not found, version conflicts |
| Multi-tenant isolation | Yes | Two tenants, verify data isolation |
| Balanced ledger (if applicable) | Yes | Sum of debits = sum of credits across services |
| Compensation (if saga-backed) | Yes | Force failure mid-flow, verify clean reversal |
| Idempotency | Yes | Same request twice produces same result once |
| Concurrent access | Recommended | Optimistic locking, parallel writes |

### Build Tags and Running

```go
//go:build integration

package {service}e2e
```

```bash
# Run service e2e tests
go test -v -tags=integration ./tests/{service}-e2e/... -timeout 10m

# Run all e2e tests
go test -v -tags=integration ./tests/... -timeout 15m
```

**Reference test suites:**

- `tests/clearinge2e/` - Cross-service clearing flows (4 databases, deposit/withdrawal)
- `tests/audit-e2e/` - Multi-service audit writes with tenant isolation
- `tests/reconciliation-e2e/` - Settlement lifecycle with variance detection
- `services/current-account/e2e/` - Saga compensation with multi-schema DB

---

## Using as Task Master PRD

This checklist can be used to generate Task Master tasks:

1. Tasks 1-13 are the core service scaffold (always required)
2. Tasks 14-16 are conditional based on service characteristics:
   - Task 14 (Starlark/CEL): When service has tenant-definable logic
   - Task 15 (Control Plane): When service is manifest-provisioned
   - Task 16 (E2E Tests): Always required for production readiness
3. Dependencies follow the logical order:
   - Task 2 depends on Task 1 (domain needs proto types)
   - Task 3 depends on Task 2 (persistence needs domain)
   - Task 5 depends on Task 4 (database setup needs atlas config)
   - Task 6 depends on Tasks 4, 5 (migration needs atlas and database)
   - Task 7 depends on Tasks 2, 3 (service needs domain and persistence)
   - Tasks 9-12 depend on Task 7 (deployment needs service)
   - Task 13 depends on all previous tasks
   - Task 14 depends on Tasks 1, 7 (needs proto and service handler)
   - Task 15 depends on Tasks 1, 7, 11 (needs proto, service, and k8s)
   - Task 16 depends on Tasks 1-13, plus 14 and/or 15 if applicable

4. To generate tasks for a new service:

```bash
# Copy this file, replace {service} with actual service name
# Then parse with Task Master
task-master parse-prd docs/guides/new-{service}-service-checklist.md
```

---

## Reference Links

- [ADR-002: Microservices per BIAN Domain](../adr/0002-microservices-per-bian-domain.md)
- [ADR-003: Database Schema Migrations](../adr/0003-database-schema-migrations.md)
- [ADR-004: Event Schema Evolution](../adr/0004-event-schema-evolution.md)
- [ADR-005: Adapter Pattern Layer Translation](../adr/0005-adapter-pattern-layer-translation.md)
- [ADR-006: Tilt Local Development](../adr/0006-tilt-local-development.md)
- [ADR-015: Standard Service Directory Structure](../adr/0015-standard-service-directory-structure.md)
- [Adding Starlark Service Bindings](adding-starlark-service-bindings.md)
- [Circuit Breaker Usage Guide](circuit-breaker-usage.md)
- [Testcontainers Usage Guide](testcontainers-usage.md)
- [Database-Per-Service Migration Runbook](../runbooks/database-per-service-migration.md)
