# Internal Bank Account Service - Developer Guide

This guide provides detailed instructions for developing, testing, and debugging the Internal Bank Account service.

## Table of Contents

1. [Local Development Setup](#local-development-setup)
2. [Running Tests](#running-tests)
3. [Debugging Tips](#debugging-tips)
4. [Database Operations](#database-operations)
5. [Adding New Account Types](#adding-new-account-types)
6. [Code Organization](#code-organization)
7. [Testing Standards](#testing-standards)
8. [Contributing Guidelines](#contributing-guidelines)

---

## Local Development Setup

### Prerequisites

Ensure you have the following tools installed:

| Tool | Version | Installation |
|------|---------|--------------|
| Go | 1.23+ | `brew install go` or [golang.org](https://golang.org/dl/) |
| Docker | 20.10+ | [Docker Desktop](https://www.docker.com/products/docker-desktop/) |
| buf | 1.30+ | `brew install bufbuild/buf/buf` |
| Atlas | 0.21+ | `brew install ariga/tap/atlas` |
| grpcurl | latest | `brew install grpcurl` |
| Kind + ctlptl | latest | `brew install kind ctlptl` (for Tilt) |
| Tilt | 0.33+ | `brew install tilt-dev/tap/tilt` |

Verify installation:

```bash
go version          # go version go1.23.x
docker --version    # Docker version 24.x.x
buf --version       # 1.30.x
atlas version       # atlas version v0.21.x
grpcurl --version   # grpcurl v1.x.x
```

### Environment Variables

Create a `.env` file in the repository root for local development:

```bash
# Database configuration
DATABASE_URL=postgres://postgres:postgres@localhost:5432/meridian_internal_bank_account?sslmode=disable
DB_MAX_OPEN_CONNS=25
DB_MAX_IDLE_CONNS=5

# Service addresses
POSITION_KEEPING_ADDR=localhost:50053
REFERENCE_DATA_ADDR=localhost:50055

# Logging
LOG_LEVEL=debug
LOG_FORMAT=json

# Server ports
GRPC_PORT=50057
METRICS_PORT=8082

# Authentication (optional for local dev)
AUTH_ENABLED=false

# OpenTelemetry (optional)
OTEL_SERVICE_NAME=internal-bank-account-service
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317
```

### Running Locally with Tilt (Recommended)

Tilt provides the fastest development experience with live reload:

```bash
# 1. Create local Kubernetes cluster (one-time setup)
ctlptl create cluster kind --registry=ctlptl-registry --name=kind-meridian-local

# 2. Start all services with hot reload
tilt up

# 3. Access the Tilt UI
open http://localhost:10350

# Service endpoints (auto-forwarded):
# - Internal Bank Account gRPC: localhost:50057
# - Position Keeping gRPC: localhost:50053
# - CockroachDB: localhost:26257
# - Metrics: localhost:8082
```

Tilt automatically:

- Syncs code changes (2-3 second feedback loop)
- Rebuilds and restarts services
- Manages database migrations
- Aggregates logs from all services

### Running Without Tilt

For standalone service development:

```bash
# 1. Start dependencies
docker-compose up -d postgres redis kafka

# 2. Apply database migrations
cd services/internal-bank-account
atlas migrate apply --env local

# 3. Build and run the service
go build -o internal-bank-account ./cmd
./internal-bank-account
```

Alternative: Run with `go run`:

```bash
go run ./services/internal-bank-account/cmd
```

### Verifying the Service

```bash
# Check health endpoint
curl http://localhost:8082/health

# Check readiness
curl http://localhost:8082/ready

# List gRPC methods
grpcurl -plaintext localhost:50057 list

# Create a test account
grpcurl -plaintext -d '{
  "account_code": "CLR-DEV-001",
  "name": "Development Clearing Account",
  "account_type": "INTERNAL_ACCOUNT_TYPE_CLEARING",
  "instrument_code": "USD",
  "description": "Test clearing account for development"
}' localhost:50057 meridian.internal_bank_account.v1.InternalBankAccountService/InitiateInternalBankAccount
```

---

## Running Tests

### Unit Tests

Unit tests run quickly without external dependencies:

```bash
# Run all unit tests
go test ./services/internal-bank-account/...

# Run tests with coverage
go test -coverprofile=coverage.out ./services/internal-bank-account/...
go tool cover -html=coverage.out -o coverage.html

# Run specific package tests
go test ./services/internal-bank-account/domain/...
go test ./services/internal-bank-account/service/...

# Run tests with verbose output
go test -v ./services/internal-bank-account/...

# Run a specific test
go test -v -run TestInternalBankAccount_Suspend ./services/internal-bank-account/domain/
```

### Integration Tests with Testcontainers

Integration tests use the `//go:build integration` build tag and spin up real PostgreSQL containers:

```bash
# Run integration tests (requires Docker)
go test -tags=integration ./services/internal-bank-account/...

# Run specific integration test
go test -tags=integration -v -run TestRepository ./services/internal-bank-account/adapters/persistence/

# Run E2E tests
go test -tags=integration -v ./services/internal-bank-account/e2e/
```

Integration tests automatically:

- Start PostgreSQL 16 in Docker
- Apply the schema
- Create tenant schemas for isolation
- Clean up after completion

### Coverage Requirements

Target coverage levels:

| Package | Target |
|---------|--------|
| `domain/` | 90%+ |
| `service/` | 85%+ |
| `adapters/persistence/` | 80%+ |
| `client/` | 75%+ |

Check coverage:

```bash
go test -coverprofile=coverage.out ./services/internal-bank-account/...
go tool cover -func=coverage.out | grep -E "(total|domain|service|adapters)"
```

### Using await.Until() for Async Operations

**CRITICAL**: Never use `time.Sleep()` in tests. Use the `await` package for polling:

```go
import "github.com/meridianhub/meridian/shared/platform/await"

// BAD - arbitrary sleep, flaky and slow
time.Sleep(2 * time.Second)
assert.Equal(t, "COMPLETED", order.Status)

// GOOD - polls until condition met or timeout
err := await.Until(func() bool {
    return order.Status == "COMPLETED"
})
require.NoError(t, err)

// With custom timeout and poll interval
err := await.New().
    AtMost(5 * time.Second).
    PollInterval(50 * time.Millisecond).
    Until(func() bool {
        account, _ := repo.FindByID(ctx, id)
        return account != nil && account.Status() == domain.AccountStatusActive
    })

// Wait for an operation to succeed
err := await.UntilNoError(func() error {
    return client.HealthCheck()
})
```

Default timeouts: 10s timeout, 100ms poll interval.

---

## Debugging Tips

### Common Issues and Solutions

| Issue | Cause | Solution |
|-------|-------|----------|
| `connection refused` | Service not running | Check `docker ps` and service logs |
| `tenant not found` | Missing tenant context | Ensure `x-organization` header is set |
| `permission denied` | Auth interceptor | Set `AUTH_ENABLED=false` for local dev |
| `version conflict` | Optimistic locking | Re-read entity before update |
| `constraint violation` | Duplicate account_code | Use unique codes per tenant |

### Using Delve Debugger

Start the service with delve:

```bash
# Interactive debugging
dlv debug ./services/internal-bank-account/cmd -- --log-level=debug

# Attach to running process
dlv attach $(pgrep internal-bank-account)

# Common commands in delve:
# b main.main      - Set breakpoint
# c                - Continue
# n                - Next line
# s                - Step into
# p variable       - Print variable
# bt               - Backtrace
```

VS Code launch configuration (`.vscode/launch.json`):

```json
{
  "version": "0.2.0",
  "configurations": [
    {
      "name": "Debug Internal Bank Account",
      "type": "go",
      "request": "launch",
      "mode": "debug",
      "program": "${workspaceFolder}/services/internal-bank-account/cmd",
      "env": {
        "LOG_LEVEL": "debug",
        "DATABASE_URL": "postgres://postgres:postgres@localhost:5432/meridian_internal_bank_account?sslmode=disable"
      }
    }
  ]
}
```

### Log Levels and Configuration

Set log level via environment variable:

```bash
export LOG_LEVEL=debug  # debug, info, warn, error
```

Log levels in code:

```go
import "log/slog"

logger.Debug("processing request", "account_id", accountID)
logger.Info("account created", "account_id", accountID, "type", accountType)
logger.Warn("retry attempt", "attempt", retryCount, "error", err)
logger.Error("failed to create account", "error", err)
```

### Tracing with OpenTelemetry

Enable tracing for distributed debugging:

```bash
# Start Jaeger locally
docker run -d --name jaeger \
  -p 16686:16686 \
  -p 4317:4317 \
  jaegertracing/all-in-one:latest

# Configure service
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317
export OTEL_SERVICE_NAME=internal-bank-account-service

# View traces
open http://localhost:16686
```

Trace propagation is automatic via gRPC interceptors.

---

## Database Operations

### Atlas Migrations

Generate a new migration after schema changes:

```bash
cd services/internal-bank-account

# Generate migration from schema changes
atlas migrate diff migration_name --env local

# Review generated SQL
cat migrations/*.sql

# Apply migrations locally
atlas migrate apply --env local

# Check migration status
atlas migrate status --env local
```

### Schema Inspection

```bash
# View current schema
atlas schema inspect --env local

# Compare schema with migrations
atlas schema diff --env local
```

### Manual SQL Queries for Debugging

Connect to the database:

```bash
# Via psql
psql $DATABASE_URL

# Via Docker
docker exec -it postgres psql -U postgres -d meridian_internal_bank_account
```

Useful queries:

```sql
-- List all accounts
SELECT id, account_code, name, account_type, status, instrument_code
FROM internal_bank_account
ORDER BY created_at DESC;

-- Find accounts by type
SELECT * FROM internal_bank_account
WHERE account_type = 'NOSTRO' AND status = 'ACTIVE';

-- View status history
SELECT h.*, a.account_code
FROM internal_bank_account_status_history h
JOIN internal_bank_account a ON a.account_id = h.account_id
ORDER BY h.changed_at DESC
LIMIT 20;

-- Check schema (for tenant schemas)
SELECT schemaname, tablename
FROM pg_tables
WHERE schemaname LIKE 'tenant_%';
```

---

## Adding New Account Types

Follow these steps to add a new internal account type:

### Step 1: Update Proto Definitions

Edit `api/proto/meridian/internal_bank_account/v1/internal_bank_account.proto`:

```protobuf
enum InternalAccountType {
  // ... existing types ...

  // INTERNAL_ACCOUNT_TYPE_ESCROW is for escrow account holdings.
  INTERNAL_ACCOUNT_TYPE_ESCROW = 9;
}
```

Regenerate Go code:

```bash
buf generate
```

### Step 2: Update Domain Model

Edit `services/internal-bank-account/domain/account_type.go`:

```go
const (
    // ... existing types ...
    AccountTypeEscrow AccountType = "ESCROW"
)

func (t AccountType) IsValid() bool {
    switch t {
    case AccountTypeClearing, AccountTypeNostro, /* ... */ AccountTypeEscrow:
        return true
    }
    return false
}
```

### Step 3: Update Repository Mapping

Edit `services/internal-bank-account/adapters/persistence/mappers.go`:

```go
func mapProtoAccountTypeToDomain(protoType pb.InternalAccountType) domain.AccountType {
    switch protoType {
    // ... existing cases ...
    case pb.INTERNAL_ACCOUNT_TYPE_ESCROW:
        return domain.AccountTypeEscrow
    }
}
```

### Step 4: Update Database Constraints

Create a new migration:

```bash
atlas migrate diff add_escrow_account_type --env local
```

Or manually add to migration:

```sql
ALTER TABLE internal_bank_account
DROP CONSTRAINT chk_account_type;

ALTER TABLE internal_bank_account
ADD CONSTRAINT chk_account_type CHECK (account_type IN (
  'CLEARING', 'NOSTRO', 'VOSTRO', 'HOLDING',
  'SUSPENSE', 'REVENUE', 'EXPENSE', 'INVENTORY', 'ESCROW'
));
```

### Step 5: Write Tests

Add tests for the new type:

```go
func TestAccountType_Escrow(t *testing.T) {
    account, err := domain.NewInternalBankAccount(
        "ACC-001",
        "ESCROW-USD-001",
        "USD Escrow Account",
        domain.AccountTypeEscrow,
        "USD",
        "CURRENCY",
    )
    require.NoError(t, err)
    assert.Equal(t, domain.AccountTypeEscrow, account.AccountType())
    assert.False(t, account.AccountType().RequiresCorrespondent())
}
```

### Step 6: Update Documentation

Update README.md with the new account type description.

---

## Code Organization

The service follows the hexagonal architecture pattern per ADR-0015:

```text
services/internal-bank-account/
|
+-- cmd/                        # Application entry point
|   +-- main.go                 # Service bootstrap, dependency wiring
|   +-- Dockerfile              # Container build
|
+-- domain/                     # Business logic (no external dependencies)
|   +-- internal_account.go     # Aggregate root with domain logic
|   +-- account_type.go         # Account type enumeration
|   +-- account_status.go       # Status state machine
|   +-- correspondent.go        # Correspondent bank value object
|   +-- repository.go           # Repository interface (port)
|   +-- errors.go               # Domain-specific errors
|
+-- adapters/                   # Infrastructure implementations
|   +-- persistence/            # Database adapter
|   |   +-- repository.go       # Repository implementation
|   |   +-- account_entity.go   # GORM entity
|   |   +-- mappers.go          # Entity <-> Domain mapping
|   +-- grpc/                   # External gRPC clients
|       +-- position_keeping_client.go
|
+-- service/                    # gRPC service layer
|   +-- server.go               # gRPC server implementation
|   +-- mappers.go              # Proto <-> Domain mapping
|   +-- health.go               # Health check implementation
|   +-- client_interfaces.go    # External client interfaces
|
+-- client/                     # Service-owned gRPC client
|   +-- client.go               # Client for other services to import
|
+-- observability/              # Metrics and health checks
|   +-- metrics.go              # Prometheus metrics
|   +-- health.go               # Health check logic
|
+-- provisioning/               # Account provisioning
|   +-- default_accounts.go     # Default account templates
|   +-- templates.go            # Account creation templates
|
+-- migrations/                 # SQL migration files
|   +-- *.sql                   # Atlas migrations
|
+-- atlas/                      # Atlas configuration
|   +-- atlas.hcl               # Migration settings
|
+-- e2e/                        # End-to-end tests
|   +-- e2e_test.go             # Integration test suite
|
+-- benchmarks/                 # Performance tests
    +-- performance_bench_test.go
```

### Key Principles

1. **Domain is independent**: No imports from adapters or service layers
2. **Adapters implement ports**: Repository interface defined in domain, implemented in adapters
3. **Service orchestrates**: Maps protos, calls domain, handles errors
4. **Immutable domain objects**: All domain modifications return new instances

---

## Testing Standards

### Red/Green/Refactor TDD

Follow the TDD cycle:

1. **Red**: Write a failing test first
2. **Green**: Write minimal code to pass
3. **Refactor**: Improve code while keeping tests green

```go
// 1. RED - Write failing test
func TestInternalBankAccount_CannotCloseAlreadyClosed(t *testing.T) {
    account := createClosedAccount(t)
    _, err := account.Close("duplicate closure attempt")
    require.Error(t, err)
    assert.ErrorIs(t, err, domain.ErrInvalidStateTransition)
}

// 2. GREEN - Implement minimal code
func (a InternalBankAccount) Close(reason string) (InternalBankAccount, error) {
    if a.status == AccountStatusClosed {
        return a, ErrInvalidStateTransition
    }
    // ...
}

// 3. REFACTOR - Improve (add state machine validation)
```

### Testing Both Happy and Unhappy Paths

Every feature needs both positive and negative tests:

```go
// Happy path
func TestInitiateAccount_Success(t *testing.T) {
    resp, err := service.InitiateInternalBankAccount(ctx, validRequest)
    require.NoError(t, err)
    assert.NotEmpty(t, resp.AccountId)
    assert.Equal(t, pb.INTERNAL_ACCOUNT_STATUS_ACTIVE, resp.Facility.AccountStatus)
}

// Unhappy paths
func TestInitiateAccount_MissingAccountCode(t *testing.T) {
    req := validRequest
    req.AccountCode = ""
    _, err := service.InitiateInternalBankAccount(ctx, req)
    require.Error(t, err)
    assert.Contains(t, err.Error(), "account_code")
}

func TestInitiateAccount_DuplicateCode(t *testing.T) {
    // Create first account
    _, err := service.InitiateInternalBankAccount(ctx, validRequest)
    require.NoError(t, err)

    // Attempt duplicate
    _, err = service.InitiateInternalBankAccount(ctx, validRequest)
    require.Error(t, err)
    assert.Equal(t, codes.AlreadyExists, status.Code(err))
}

func TestInitiateAccount_NostroWithoutCorrespondent(t *testing.T) {
    req := validRequest
    req.AccountType = pb.INTERNAL_ACCOUNT_TYPE_NOSTRO
    req.CorrespondentDetails = nil  // Required for NOSTRO

    _, err := service.InitiateInternalBankAccount(ctx, req)
    require.Error(t, err)
    assert.Contains(t, err.Error(), "correspondent")
}
```

### Table-Driven Tests

Use table-driven tests for comprehensive coverage:

```go
func TestAccountType_IsValid(t *testing.T) {
    tests := []struct {
        name     string
        input    domain.AccountType
        expected bool
    }{
        {"clearing is valid", domain.AccountTypeClearing, true},
        {"nostro is valid", domain.AccountTypeNostro, true},
        {"empty is invalid", domain.AccountType(""), false},
        {"unknown is invalid", domain.AccountType("UNKNOWN"), false},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            assert.Equal(t, tt.expected, tt.input.IsValid())
        })
    }
}
```

---

## Contributing Guidelines

### Code Review Checklist

Before submitting a PR, verify:

- [ ] All tests pass: `go test ./services/internal-bank-account/...`
- [ ] Integration tests pass: `go test -tags=integration ./services/internal-bank-account/...`
- [ ] No linting errors: `golangci-lint run ./services/internal-bank-account/...`
- [ ] Proto changes regenerated: `buf generate`
- [ ] Migrations applied: `atlas migrate apply --env local`
- [ ] Documentation updated if adding new features
- [ ] No `time.Sleep()` in tests (use `await` package)
- [ ] Both happy and unhappy paths tested

### Commit Message Standards

Use conventional commits:

```text
type(scope): brief description

[optional body explaining why]

[optional footer with references]
```

Types:

- `feat`: New feature
- `fix`: Bug fix
- `refactor`: Code change that neither fixes a bug nor adds a feature
- `docs`: Documentation only
- `test`: Adding or updating tests
- `chore`: Build process or auxiliary tool changes

Examples:

```text
feat(internal-bank-account): add ESCROW account type

Adds support for escrow accounts to enable secure fund holding
during multi-party transactions.

Closes #458

---

fix(internal-bank-account): prevent closing already-closed accounts

Previously, calling Close() on a closed account would succeed silently.
Now returns ErrInvalidStateTransition with clear error message.

---

test(internal-bank-account): add integration tests for multi-tenant isolation

Verifies that accounts in different tenant schemas are properly isolated
and cannot be accessed cross-tenant.
```

### Branch Naming

Use descriptive branch names:

```text
feature/add-escrow-account-type
fix/prevent-duplicate-account-codes
refactor/extract-state-machine
docs/update-developer-guide
```

### Pull Request Process

1. Create feature branch from `develop`
2. Implement changes with tests
3. Run full test suite locally
4. Push branch and create PR
5. Address review comments
6. Squash and merge after approval

---

## Additional Resources

- [README.md](./README.md) - Service overview and API documentation
- [ADR-0015: Service Directory Structure](../../docs/adr/0015-standard-service-directory-structure.md)
- [ADR-0023: Balance Delegation to Position Keeping](../../docs/adr/0023-balance-delegation-to-position-keeping.md)
- [ADR-0024: Internal Bank Account Service](../../docs/adr/0024-internal-bank-account-service.md)
- [Proto Definitions](../../api/proto/meridian/internal_bank_account/v1/)
- [Benchmarks README](./benchmarks/README.md)
