---
name: adr-015-standard-service-directory-structure
description: Standardized directory structure for the Meridian platform including root layout and microservices
triggers:
  - Creating a new microservice
  - Restructuring an existing service
  - Adding new functionality to a service (where to place files)
  - Reviewing service organization or architecture
  - Adding documentation (where should it go?)
  - Working with API definitions (proto, OpenAPI)
instructions: |
  Root layout: api/ for proto/OpenAPI, docs/ for all documentation, services/ for microservices, shared/ for libraries.
  Service structure: cmd/, domain/, adapters/, service/, observability/.
  Place persistence code in adapters/persistence/, messaging in adapters/messaging/.
  Use observability/ for metrics and health checks. Use client/ for service-owned gRPC clients.
  All documentation belongs in docs/ - not scattered in service directories.
---

# 15. Standard Project and Service Directory Structure

Date: 2025-12-05

## Status

Accepted

## Context

Following the codebase reorganization (#215, #216) that moved from a monolithic structure to domain-centric service directories, analysis revealed inconsistent directory structures across services:

| Directory | current-account | position-keeping | financial-accounting | payment-order |
|-----------|-----------------|------------------|----------------------|---------------|
| `cmd/` | ✓ | ✓ | ✓ | ✓ |
| `domain/` | ✓ | ✓ | ✓ | ✓ |
| `adapters/` | ✓ | ✓ | ✓ | ✓ |
| `service/` | ✓ | ✓ | ✓ | ✓ |
| `client/` | ✓ | ✓ | ✓ | ✗ |
| `observability/` | ✓ | ✗ (uses `app/`) | ✗ | ✓ |
| `app/` | ✗ | ✓ | ✗ | ✗ |
| `interceptors/` | ✗ | ✓ | ✗ | ✗ |
| `repository/` | ✗ | ✓ | ✓ (doc.go only) | ✗ |

These inconsistencies create confusion about where to place new code, make onboarding harder, and prevent sharing of common patterns.

## Decision Drivers

* **Consistency**: Developers should know exactly where to find and place code
* **Discoverability**: New team members should easily navigate the codebase
* **Separation of concerns**: Clear boundaries between domain, infrastructure, and adapters
* **Flexibility**: Support varying service complexities without forcing unnecessary structure
* **Alignment with ADR-005**: Maintain consistency with the adapter pattern for layer translation

## Considered Options

1. **Strict standard structure** - Every service must have all directories
2. **Flexible standard with required core** - Required directories plus optional ones as needed
3. **Service templates per complexity** - Different templates for simple vs. complex services

## Decision Outcome

Chosen option: **Flexible standard with required core**, because it provides consistency for essential components while allowing services to evolve based on their needs.

### Root Project Structure

The Meridian monorepo follows this root-level organization:

```text
meridian-main/
├── api/                    # API definitions (proto + OpenAPI)
│   ├── proto/             # Protocol Buffer definitions
│   │   ├── buf.yaml       # Buf configuration
│   │   └── meridian/      # Service-specific protos
│   │       ├── common/v1/
│   │       ├── current_account/v1/
│   │       ├── financial_accounting/v1/
│   │       ├── payment_order/v1/
│   │       ├── position_keeping/v1/
│   │       ├── events/v1/
│   │       └── platform/v1/
│   └── openapi/           # Generated OpenAPI specs
│       ├── current-account.swagger.json
│       ├── financial-accounting.swagger.json
│       ├── payment-order.swagger.json
│       └── position-keeping.swagger.json
│
├── docs/                   # All documentation
│   ├── adr/               # Architecture Decision Records
│   ├── architecture/      # Architecture diagrams and docs
│   ├── guides/            # Usage guides and how-tos
│   ├── runbooks/          # Operational runbooks
│   └── testing/           # Testing guides
│
├── services/              # Microservices (see below)
│   ├── current-account/
│   ├── financial-accounting/
│   ├── payment-order/
│   └── position-keeping/
│
├── shared/                # Shared libraries
│   ├── domain/           # Shared domain types (Money, etc.)
│   ├── platform/         # Platform utilities (auth, observability)
│   ├── pkg/              # Shared packages
│   └── migrations/       # Shared migration utilities
│
├── deployments/           # Kubernetes manifests
│   └── k8s/
│
├── scripts/               # Build and tooling scripts
│
├── utilities/             # Standalone utility programs
│
└── tests/                 # Integration test suites
```

#### Root Directory Guidelines

| Directory | Purpose | What Goes Here |
|-----------|---------|----------------|
| `api/` | API contracts | Proto definitions, generated OpenAPI specs (see note below) |
| `docs/` | All documentation | ADRs, guides, runbooks, architecture docs |
| `services/` | Microservices | One directory per BIAN service domain |
| `shared/` | Shared code | Libraries used by multiple services |
| `deployments/` | Infrastructure | Kubernetes manifests, Helm charts |
| `scripts/` | Tooling | Build scripts, analysis tools |
| `utilities/` | Utilities | Standalone programs (demo tools, loaders) |

#### Why Centralized `api/` at Root?

We use a centralized `api/proto/` directory rather than placing proto files inside each service for several reasons:

1. **Shared types** - Common types (`common/v1/`, `events/v1/`) are used across multiple services. Centralized location avoids circular dependencies.
2. **Contract-first development** - Protos define contracts between services. Having them together makes the API surface visible at a glance.
3. **Buf tooling** - Single `buf.yaml` workspace simplifies linting, breaking change detection, and code generation.
4. **BIAN domain pattern** - Banking services share domain concepts (Money, Account references) that naturally live together.

This follows industry practice for Go monorepos with interdependent services. See [References](#references) for supporting documentation.

**When per-service protos make sense**: If services are truly independent (different teams, no shared types, could be separate repos), consider placing protos inside each service.

#### Documentation Location Rules

**All documentation belongs in `docs/`**, not scattered in service directories:

| Document Type | Location | Example |
|--------------|----------|---------|
| Architecture decisions | `docs/adr/` | `0015-standard-service-directory-structure.md` |
| Usage guides | `docs/guides/` | [`circuit-breaker-usage.md`](../guides/circuit-breaker-usage.md) |
| Operational runbooks | `docs/runbooks/` | [`incident-response.md`](../runbooks/incident-response.md) |
| Architecture diagrams | `docs/architecture/` | [`service-coupling-analysis.md`](../architecture/service-coupling-analysis.md) |
| Testing guides | `docs/testing/` | [`COVERAGE_ANALYSIS.md`](../testing/COVERAGE_ANALYSIS.md) |

**Exceptions**: README.md files may exist at package roots to document package purpose (Go convention), but substantial documentation belongs in `docs/`.

### Service Directory Structure

```text
services/{service-name}/
├── cmd/                    # REQUIRED: Entry point and Dockerfile
│   ├── main.go            # Application entry point
│   └── Dockerfile         # Container build definition
│
├── domain/                 # REQUIRED: Business logic and domain models
│   ├── models.go          # Domain entities
│   ├── repository.go      # Repository interfaces (ports)
│   ├── service.go         # Domain services
│   └── events.go          # Domain events
│
├── adapters/               # REQUIRED: External world adapters
│   ├── persistence/       # Database implementations
│   │   ├── entities.go    # Database entities
│   │   ├── repository.go  # Repository implementation
│   │   └── mappers.go     # Entity <-> Domain mappers
│   ├── messaging/         # Kafka consumers/producers (if needed)
│   ├── gateway/           # External service adapters (if needed)
│   └── http/              # HTTP handlers (if needed)
│
├── service/                # REQUIRED: gRPC service implementation
│   ├── server.go          # gRPC server implementation
│   └── mappers.go         # Proto <-> Domain mappers
│
├── observability/          # REQUIRED: Metrics and health checks
│   ├── metrics.go         # Prometheus metrics
│   └── health.go          # Health check implementations
│
├── client/                 # OPTIONAL: Service-owned gRPC client
│   └── client.go           # Client for other services to import
│
├── app/                    # OPTIONAL: Application bootstrap (complex services)
│   ├── config.go          # Configuration loading
│   └── container.go       # Dependency injection container
│
├── atlas/                  # REQUIRED: Database migration config
│   └── atlas.hcl
│
├── migrations/             # REQUIRED: SQL migration files
│   └── *.sql
│
└── k8s/                    # REQUIRED: Kubernetes manifests
    ├── deployment.yaml
    └── service.yaml
```

### Directory Guidelines

#### Required Directories

| Directory | Purpose | Key Files |
|-----------|---------|-----------|
| `cmd/` | Application entry point | `main.go`, `Dockerfile` |
| `domain/` | Business logic, models, interfaces | `models.go`, `repository.go` |
| `adapters/` | Infrastructure implementations | `persistence/`, `messaging/` |
| `service/` | gRPC service layer | `server.go`, `mappers.go` |
| `observability/` | Metrics and health checks | `metrics.go`, `health.go` |
| `atlas/` | Database schema management | `atlas.hcl` |
| `migrations/` | SQL migration files | `*.sql` |
| `k8s/` | Kubernetes deployment | `deployment.yaml` |

#### Optional Directories

| Directory | When to Add | Example Use Case |
|-----------|-------------|------------------|
| `client/` | Service exports client for others | party exporting client for current-account |
| `app/` | Complex initialization logic | Services needing DI containers, complex config |
| `adapters/messaging/` | Kafka integration | Event publishing/consuming |
| `adapters/gateway/` | External API integration | ISO 20022 gateway adapters |
| `adapters/http/` | REST endpoints | Health probes, admin endpoints |

### Positive Consequences

* Clear expectations for where code belongs
* Easier onboarding for new developers
* Consistent tooling and scripts across services
* Simplified code review (reviewers know where to look)
* Alignment with hexagonal architecture principles

### Negative Consequences

* Some services may have nearly-empty directories initially
* Migration effort for existing services
* May feel prescriptive for simple services

## Implementation Notes

### Repository vs Adapters/Persistence

The `repository/` directory at service root level should **not** be used for new code. Repository implementations belong in `adapters/persistence/`. The domain defines repository interfaces (ports) in `domain/`, and implementations (adapters) live in `adapters/persistence/`.

This aligns with ADR-005 (Adapter Pattern Layer Translation):
- `domain/repository.go` - Interface definition (port)
- `adapters/persistence/repository.go` - Implementation (adapter)

**Note on position-keeping**: The position-keeping service currently has a `repository/` directory with working code. This pre-dates the standard and will be migrated to `adapters/persistence/` in a future PR. New services should not follow this pattern.

### Observability Location

All observability code (metrics, health checks) should live in the `observability/` directory, not scattered in `app/` or other locations. This includes:
- Prometheus metrics definitions and recording functions
- Health check implementations (database, Redis, external services)
- Any monitoring-related utilities

### App Directory Pattern

The `app/` directory is optional and should only be added when a service has complex initialization needs:
- Dependency injection containers
- Multi-stage configuration loading
- Complex resource lifecycle management

Simple services should keep initialization logic in `cmd/main.go`.

### Shared Libraries

Cross-service utilities should be extracted to `shared/pkg/`:
- `shared/pkg/interceptors/` - gRPC interceptors (metrics, recovery, logging, auth)
- `shared/pkg/health/` - Common health check interfaces
- `shared/pkg/grpc/` - gRPC utilities

The `MetricsInterceptor` and `RecoveryUnaryInterceptor` in `shared/pkg/interceptors/` should be used by all services instead of defining their own.

## Migration Plan

1. **Document** - This ADR establishes the standard (complete)
2. **Observability** - Move position-keeping metrics/health to `observability/`
3. **Health checks** - Add observability to financial-accounting
4. **Cleanup** - Remove `.gitkeep` placeholder files or complete directories
5. **Shared** - Extract panic recovery interceptor to shared library
6. **Documentation** - Update service READMEs

## Links

* [GitHub Issue #221](https://github.com/meridianhub/meridian/issues/221) - Standardize service directory structure
* [ADR-002](0002-microservices-per-bian-domain.md) - Microservices per BIAN domain
* [ADR-005](0005-adapter-pattern-layer-translation.md) - Adapter pattern layer translation

## References

Industry best practices supporting centralized API definitions in Go monorepos:

* [Buf: Files and Packages](https://buf.build/docs/reference/protobuf-files-and-packages/) - Official buf.build guidance on proto file organization
* [Structuring repositories with protocol buffers](https://dev.to/davidsbond/golang-structuring-repositories-with-protocol-buffers-3012) - Go-specific patterns for proto organization
* [golang/protobuf Issue #391](https://github.com/golang/protobuf/issues/391) - Community discussion on project structure with multiple services
* [gRPC organization in microservices (Stack Overflow)](https://stackoverflow.com/questions/56082458/grpc-organization-in-microservices) - Centralized vs distributed proto patterns

## Notes

Future services should be created using this structure from the start. Consider creating a service template or generator to bootstrap new services with the correct structure.
