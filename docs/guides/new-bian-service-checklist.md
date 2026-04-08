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

## Convention Guides

These guides document patterns you will apply throughout service creation. Read them before
starting, and reference them when working on the relevant tasks.

| Guide | Apply When |
|-------|------------|
| [Error Conventions](error-conventions.md) | Task 2 (domain errors), Task 7 (gRPC error mapping) |
| [Repository Conventions](repository-conventions.md) | Task 2 (repository interface), Task 3 (GORM implementation) |
| [Value Types](value-types.md) | Task 2 (domain model fields), Task 3 (persistence mapping) |

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

**Convention references**:

- [Error Conventions](error-conventions.md) — sentinel error naming and where to define them
- [Repository Conventions](repository-conventions.md) — interface location, method naming, error documentation
- [Value Types](value-types.md) — choosing between Money, Asset, and Amount for domain fields

**Files to create:**

- `{entity}.go` - Domain entity with:
  - Private fields with public getters
  - Constructor (`New{Entity}`) enforcing invariants
  - Mutation methods that validate state transitions
  - Value objects (enums, status types)
- `errors.go` or `repository.go` - Sentinel errors (see [Error Conventions](error-conventions.md))
- `repository.go` - Repository interface (port) with documented error contracts
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

**Convention references**:

- [Repository Conventions](repository-conventions.md) — entity-prefixed errors, TableName, optimistic locking, tenant scoping
- [Value Types](value-types.md) — mapping Qty/Money/Amount to/from persistence columns

**Files to create:**

- `{entity}_entity.go` - Database entity with GORM tags
- `repository.go` - Repository implementation with:
  - Compile-time interface assertion (`var _ domain.{Entity}Repository = (*{Entity}Repository)(nil)`)
  - Entity-prefixed sentinel errors (see [Error Conventions](error-conventions.md))
  - Optimistic locking via version field
  - Tenant scoping via `db.WithGormTenantScope`
  - Soft delete support (where applicable)
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

**Reference**: [Data Model Reference](../architecture/data-model.md)

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

**Convention reference**: [Error Conventions](error-conventions.md) — gRPC status code mapping table and error message guidelines.

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
FROM golang:1.26.2-bookworm AS builder

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

    db, cleanup := testdb.SetupCockroachDB(t, []interface{}{&persistence.{Entity}Entity{}})
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

### Scheduler Integration (Optional)

**Applies when**: The service runs periodic background work (cron-based scheduling, polling
workers, retry loops, or any recurring task).

**Packages**: `shared/platform/scheduler`, `shared/platform/redislock`

#### Option A: Cron Scheduler (time-based recurring jobs)

Use `scheduler.CronScheduler` when the service needs to execute jobs on a cron schedule
(e.g., reconciliation runs, forecast generation, billing cycles).

**1. Add `scheduler_execution` migration**

Copy the schema from an existing service migration. Use the shared column names exactly:

```sql
-- Reference: services/forecasting/migrations/20260213000001_scheduler_execution.sql
CREATE TABLE "scheduler_execution" (
    "id" uuid NOT NULL DEFAULT gen_random_uuid(),
    "scheduler_name" character varying(100) NOT NULL,
    "schedule_id" character varying(200) NOT NULL,
    "scheduled_at" timestamptz NOT NULL,
    "executed_at" timestamptz NULL,
    "completed_at" timestamptz NULL,
    "status" character varying(20) NOT NULL DEFAULT 'TRIGGERED',
    "result_ref" character varying(200) NULL,
    "error_message" text NULL,
    "created_at" timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY ("id")
);

ALTER TABLE "scheduler_execution"
    ADD CONSTRAINT "chk_scheduler_execution_status"
    CHECK ("status" IN ('TRIGGERED', 'COMPLETED', 'FAILED', 'MISSED', 'SKIPPED'));

CREATE INDEX "idx_scheduler_execution_name_schedule"
    ON "scheduler_execution" ("scheduler_name", "schedule_id");
CREATE INDEX "idx_scheduler_execution_scheduled_at"
    ON "scheduler_execution" ("scheduled_at" DESC);
CREATE INDEX "idx_scheduler_execution_status"
    ON "scheduler_execution" ("status");
CREATE INDEX "idx_scheduler_execution_name_at"
    ON "scheduler_execution" ("scheduler_name", "scheduled_at" DESC);
```

**2. Implement `scheduler.ScheduleProvider`**

```go
type ScheduleProvider interface {
    ListSchedules(ctx context.Context) ([]scheduler.Schedule, error)
}
```

Returns the active schedules for this service. Each `Schedule` has `ID`, `CronExpr`,
`TenantID`, and optional `Metadata`.

**3. Implement `scheduler.Executor`**

```go
type Executor interface {
    Execute(ctx context.Context, schedule scheduler.Schedule) error
}
```

Runs the business logic when a schedule fires.

**4. Wire in `cmd/main.go`**

```go
import (
    "github.com/meridianhub/meridian/shared/platform/redislock"
    "github.com/meridianhub/meridian/shared/platform/scheduler"
)

// Create execution store (returns error if table missing — handle it)
execStore, err := scheduler.NewPgExecutionStore(pool)
if err != nil {
    return fmt.Errorf("scheduler execution store: %w", err)
}

// Create distributed lock
lock := redislock.NewLock(redisClient, redislock.Config{
    KeyPrefix: "{service}-scheduler",
}, logger)

// Create and start scheduler
cronScheduler := scheduler.NewCronScheduler(
    provider,
    executor,
    lock,
    scheduler.CronSchedulerConfig{
        Name:             "{service}-scheduler",
        RefreshInterval:  60 * time.Second,
        ShutdownTimeout:  30 * time.Second,
        ExecutionTimeout: 5 * time.Minute,
        MaxCatchUpAge:    time.Hour,
    },
    logger,
    scheduler.WithCronExecutionStore(execStore),
)

go func() {
    if err := cronScheduler.Start(ctx); err != nil {
        logger.Error("scheduler failed", "error", err)
    }
}()
// In shutdown: cronScheduler.Stop()
```

**5. Add `'redis'` to Tiltfile `resource_deps`**

```python
k8s_resource(
    '{service}',
    port_forwards=['50055:50055'],
    resource_deps=['cockroachdb', 'redis'],
    labels=['services'],
)
```

**Reference services**: `services/reconciliation/`, `services/forecasting/`,
`services/payment-order/worker/billing_scheduler.go`

#### Option B: Polling Worker (non-cron background work)

Use `scheduler.WorkerLifecycle` for workers that poll for work on a fixed interval rather
than running on a cron schedule (e.g., retry queues, orphan detection, outbox processing).

```go
import (
    "github.com/meridianhub/meridian/shared/platform/redislock"
    "github.com/meridianhub/meridian/shared/platform/scheduler"
)

type MyWorker struct {
    lifecycle *scheduler.WorkerLifecycle
    lock      *redislock.Lock
    // ... other dependencies
}

func NewMyWorker(redisClient *redis.Client, logger *slog.Logger) *MyWorker {
    return &MyWorker{
        lifecycle: scheduler.NewWorkerLifecycle(logger),
        lock: redislock.NewLock(redisClient, redislock.Config{
            KeyPrefix: "{service}-worker",
        }, logger),
    }
}

func (w *MyWorker) Start(ctx context.Context) error {
    return w.lifecycle.Start(ctx, func(ctx context.Context) error {
        ticker := time.NewTicker(pollInterval)
        defer ticker.Stop()
        for {
            select {
            case <-ctx.Done():
                return nil
            case <-ticker.C:
                w.processWork(ctx)
            }
        }
    })
}

func (w *MyWorker) Stop() {
    w.lifecycle.Stop(30 * time.Second)
    w.lock.ReleaseAll(context.Background())
}
```

**Reference**: `services/payment-order/worker/dunning_worker.go`

#### Redis Key Naming Convention (tenant isolation)

All Redis keys MUST be scoped by tenant to ensure data isolation:

| Key Type | Format | Example |
|----------|--------|---------|
| Distributed lock | `{prefix}:{tenantID}:{resourceID}` | `billing-scheduler:tenant_abc:sched_001` |
| Sorted set | `{purpose}:{tenantID}` | `dunning:retries:tenant_abc` |

Never use global (non-tenant-scoped) Redis keys.

---

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

### Codecov Component Registration

**Applies when**: Any new `services/<name>/` directory or `frontend/src/features/<name>/`
directory is created.

Register a Codecov component so coverage for the new service is tracked independently.
The `default_rules` in `codecov.yml` automatically apply an 80% target to all registered
components — no additional target configuration is needed.

Add the component definition to `codecov.yml` under
`component_management.individual_components`:

**Backend service:**

```yaml
- component_id: <service-name>
  name: <Service Name>
  paths:
    - services/<service-name>/**
```

**Frontend feature:**

```yaml
- component_id: fe-<feature-name>
  name: Frontend - <Feature Name>
  paths:
    - frontend/src/features/<feature-name>/**
```

**Reference PRs**: #1815 (backend components), #1820 (frontend components)

---

### Idempotency Service Wiring

**Applies when**: The service accepts mutation requests that must not be executed more than
once if the client retries (payment operations, ledger entries, state-changing RPCs).

#### Overview

The `shared/pkg/idempotency/` package provides distributed idempotency and locking. It has
three concrete adapters:

| Adapter | Type | Use Case |
|---------|------|----------|
| `RedisService` | `CleanableService` | Production — distributed, supports cleanup worker |
| `PostgresService` | `Service` | Unified binary / local dev — no Redis required |
| `NoopService` | `Service` | Non-production fallback when Redis unavailable at startup |

All adapters satisfy the `idempotency.Service` interface (`Checker` + `Locker`). Service
constructors accept `idempotency.Service`, so the adapter is injected at startup without
changing domain code.

Higher-level usage is via `idempotency.Executor`, which wraps your business logic with
atomic check-mark-execute-store semantics.

#### Standard Wiring Pattern (production fail-fast with non-production fallback)

This is the gold-standard pattern from `services/financial-accounting/cmd/main.go`:

```go
import (
    "github.com/meridianhub/meridian/shared/pkg/idempotency"
    "github.com/meridianhub/meridian/shared/platform/env"
    "github.com/redis/go-redis/v9"
)

var idempotencySvc idempotency.Service
var redisSvc *idempotency.RedisService // Keep reference for cleanup worker

redisClient, err := createRedisClient(logger)
if err != nil {
    if env.IsProduction() {
        // Fail fast — idempotency is a hard requirement in production
        logger.Error("CRITICAL: Redis unavailable in production - failing fast", "error", err)
        return bootstrap.Permanent(fmt.Errorf("%w: %w", ErrRedisRequiredInProduction, err))
    }
    // Non-production: degrade gracefully and record metrics
    logger.Warn("Redis not available, using noop idempotency service - DEVELOPMENT ONLY",
        "error", err,
        "environment", os.Getenv("ENVIRONMENT"))
    idempotencySvc = idempotency.NewNoopService(logger)
    serviceobs.SetNoopIdempotencyActive(true)
    serviceobs.RecordServiceDegradation(serviceobs.ComponentIdempotency, serviceobs.DegradationReasonStartupFallback)
} else {
    redisSvc = idempotency.NewRedisService(redisClient)
    idempotencySvc = redisSvc
    serviceobs.SetNoopIdempotencyActive(false)
    logger.Info("idempotency service initialized with Redis")
    defer func() {
        if err := redisClient.Close(); err != nil {
            logger.Error("failed to close Redis client", "error", err)
        }
    }()
}
```

**Key rules:**

- `env.IsProduction()` determines fail-fast vs. graceful degradation. Never skip this check.
- Record degradation metrics before returning from the error branch. This ensures the
  health check reports degraded state and on-call alerts fire.
- Keep the `*idempotency.RedisService` reference separate from the `idempotency.Service`
  interface reference. The cleanup worker requires `idempotency.Cleaner`, which only
  `RedisService` implements.

#### Adapter Selection Table

| Scenario | Adapter | Why |
|----------|---------|-----|
| Production Kubernetes deployment | `RedisService` | Distributed, TTL-based, supports cleanup |
| Unified binary (`cmd/meridian/`) | `PostgresService` | No Redis dependency in dev |
| Production Redis unavailable at startup | Fail fast with `bootstrap.Permanent` | Prevent silent data corruption |
| Non-production Redis unavailable | `NoopService` | Allow dev iteration without full stack |
| Integration tests | `NoopService` | Simplify test setup; use DB constraints for correctness |

#### Cleanup Worker Wiring

When using `RedisService`, wire the `IdempotencyCleanupWorker` to prevent PENDING keys from
being stuck indefinitely after a service crash:

```go
import (
    "{service}-config" // service-specific config package
    "github.com/meridianhub/meridian/services/{service}/worker"
)

// Wire cleanup worker only when Redis is available
cleanupConfig := config.LoadIdempotencyCleanupConfig()
if redisSvc != nil && cleanupConfig.Enabled {
    cleanupWorker, err := worker.NewIdempotencyCleanupWorker(
        redisSvc,
        cleanupConfig,
        logger,
    )
    if err != nil {
        logger.Error("failed to create idempotency cleanup worker", "error", err)
    } else {
        go func() {
            if err := cleanupWorker.Start(ctx); err != nil {
                logger.Error("idempotency cleanup worker error", "error", err)
            }
        }()
    }

    // Stop worker during graceful shutdown
    defer cleanupWorker.Stop()
}
```

The cleanup worker requires:

1. The `IdempotencyCleanupConfig` loaded from environment — see
   `services/financial-accounting/config/` for a reference implementation.
2. The `redisSvc` reference (not the `idempotency.Service` interface), because cleanup
   requires `idempotency.Cleaner` which is only implemented by `RedisService`.
3. The worker must be stopped before the Redis client is closed during shutdown.

**Reference**: `services/financial-accounting/worker/idempotency_cleanup_worker.go`

#### Domain-Level Idempotency

Idempotency operates at two levels and both are needed:

| Level | Mechanism | Purpose |
|-------|-----------|---------|
| Redis / Postgres idempotency service | `idempotency.Key` with namespace + operation + entity | Prevent duplicate RPC execution across service restarts |
| Database unique constraint | `UNIQUE (tenant_id, idempotency_key)` on the domain table | Prevent duplicate writes if the idempotency service is bypassed |

Add a database-level unique constraint to the migration as a safety net even when the
idempotency service is active.

#### Anti-Patterns to Avoid

These are the inconsistencies the consistency refactor fixed. Do not reintroduce them:

- **Do not** use `NoopService` in production paths without recording degradation metrics.
  Silent noop use allows duplicate requests to corrupt ledger state.
- **Do not** skip `env.IsProduction()` and always use noop — this defeats the purpose of
  the idempotency service.
- **Do not** pass the `idempotency.Service` interface to the cleanup worker constructor.
  The worker requires `idempotency.Cleaner`, which is only available on `*RedisService`.
- **Do not** start the cleanup worker when `redisSvc == nil`. The noop path never sets
  `redisSvc`, so guard on `redisSvc != nil` (not `idempotencySvc != nil`).
- **Do not** omit the `defer redisClient.Close()` — leaked connections cause pool exhaustion
  under load.

**Reference**: `services/financial-accounting/cmd/main.go` (gold-standard pattern)

---

### Unified Binary Registration

**Applies when**: The service should be included in `cmd/meridian/`, the single-binary
entry point used for local development and single-node deployments.

#### What `cmd/meridian/` Is

`cmd/meridian/` is a single Go binary that wires all Meridian services into one process,
sharing a single gRPC server, two database connections (a `*gorm.DB` for ORM-based services
and a `*pgxpool.Pool` for pgx-based services), and a single Postgres-backed idempotency
service (no Redis required).

It is the recommended way to run Meridian locally and in single-node evaluation deployments.
Production deployments run each service as an independent binary.

Services are initialized in tier dependency order to avoid circular dependency issues with
loopback gRPC calls:

| Tier | Services |
|------|----------|
| 0 | party, reference-data, market-information, tenant, internal-account |
| 1 | financial-accounting, position-keeping, forecasting |
| 2 | current-account |
| 3 | payment-order, reconciliation |

#### How to Register a New Service

##### Step 1: Add proto import

In `cmd/meridian/main.go`, add the proto package import alongside existing ones:

```go
import (
    // ... existing proto registrations ...
    yourservicev1 "github.com/meridianhub/meridian/api/proto/meridian/your_service/v1"
)
```

##### Step 2: Add service package imports

```go
import (
    // ... existing service packages ...
    yourservicepersistence "github.com/meridianhub/meridian/services/your-service/adapters/persistence"
    yourserviceservice     "github.com/meridianhub/meridian/services/your-service/service"
)
```

##### Step 3: Add a wire function

Both database connections are created in `run()` before `registerServices` is called.
Each wire function has a fixed signature that accepts whichever client the service
requires. Use `*gorm.DB` for GORM-based persistence (party, financial-accounting,
current-account, payment-order, reconciliation, tenant, internal-account) or
`*pgxpool.Pool` for services that use direct pgx access (reference-data,
market-information, forecasting, position-keeping).

GORM example:

```go
func wireYourService(
    server *grpc.Server,
    db *gorm.DB,
    idempotencySvc idempotency.Service, // omit if service does not use idempotency
    logger *slog.Logger,
) error {
    repo := yourservicepersistence.NewRepository(db)
    svc, err := yourserviceservice.NewService(repo, idempotencySvc)
    if err != nil {
        return err
    }
    yourservicev1.RegisterYourServiceServer(server, svc)
    logger.Info("registered your-service service")
    return nil
}
```

pgxpool example (if your service uses direct pgx):

```go
func wireYourService(
    server *grpc.Server,
    pool *pgxpool.Pool,
    logger *slog.Logger,
) error {
    repo := yourservicepersistence.NewRepository(pool)
    svc, err := yourserviceservice.NewService(repo, logger)
    if err != nil {
        return err
    }
    yourservicev1.RegisterYourServiceServer(server, svc)
    logger.Info("registered your-service service")
    return nil
}
```

##### Step 4: Add the wire call in `registerServices`

Place the call in the correct tier block based on service dependencies:

```go
// In the appropriate tier block:
{"your-service", func() error {
    return wireYourService(grpcServer, db, idempotencySvc, logger)
}},
```

##### Step 5: Add gateway route

In the `wireGateway` function, add a backend route for the service:

```go
Backends: []gateway.BackendRoute{
    // ... existing routes ...
    {Prefix: "/v1/your-service", Target: grpcTarget},
},
```

##### Step 6: Add migration embed

If the service has its own migration files, embed them into the unified migration runner.
See `internal/migrations/` and `services/services.go` for the migration embed pattern used
by existing services.

#### Environment Variable Conventions

The unified binary sets these development defaults via `setDevDefaults()`. Services that
need additional environment variables should document them in their own `cmd/main.go` and
ensure they have safe defaults for the unified binary context:

| Variable | Unified binary default | Description |
|----------|----------------------|-------------|
| `AUTH_MODE` | `disabled` | Authentication mode |
| `BILLING_ENABLED` | `false` | Billing subsystem toggle |
| `ENVIRONMENT` | `development` | Used by `env.IsProduction()` |
| `REDIS_ENABLED` | `false` | Redis availability hint |
| `KAFKA_ENABLED` | `false` | Kafka availability hint |

Services that use `env.IsProduction()` for fail-fast decisions will automatically use the
noop/degraded path in the unified binary because `ENVIRONMENT=development`.

**Reference**: `cmd/meridian/main.go`

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
- [Data Model Reference](../architecture/data-model.md)
