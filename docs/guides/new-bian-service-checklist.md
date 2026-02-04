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
FROM golang:1.25.7-alpine AS builder

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

Follow [ADR-009](../adr/0009-application-level-audit-logging.md) for application-level audit logging using `shared/platform/audit`.

---

## Using as Task Master PRD

This checklist can be used to generate Task Master tasks:

1. Each numbered task (1-13) becomes a Task Master task
2. Dependencies follow the logical order:
   - Task 2 depends on Task 1 (domain needs proto types)
   - Task 3 depends on Task 2 (persistence needs domain)
   - Task 5 depends on Task 4 (database setup needs atlas config)
   - Task 6 depends on Tasks 4, 5 (migration needs atlas and database)
   - Task 7 depends on Tasks 2, 3 (service needs domain and persistence)
   - Tasks 9-12 depend on Task 7 (deployment needs service)
   - Task 13 depends on all previous tasks

3. To generate tasks for a new service:

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
- [Circuit Breaker Usage Guide](circuit-breaker-usage.md)
- [Testcontainers Usage Guide](testcontainers-usage.md)
- [Database-Per-Service Migration Runbook](../runbooks/database-per-service-migration.md)
