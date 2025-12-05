---
name: adr-015-standard-service-directory-structure
description: Standardized directory structure for all microservices in the Meridian platform
triggers:
  - Creating a new microservice
  - Restructuring an existing service
  - Adding new functionality to a service (where to place files)
  - Reviewing service organization or architecture
instructions: |
  Follow the standard directory structure: cmd/, domain/, adapters/, service/, observability/.
  Place persistence code in adapters/persistence/, messaging in adapters/messaging/.
  Use observability/ for metrics and health checks. Use clients/ for inter-service clients.
  The app/ directory pattern is optional for complex services needing DI containers.
---

# 15. Standard Service Directory Structure

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
| `clients/` | ✓ | ✓ (.gitkeep) | ✗ | ✗ |
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

### Standard Directory Structure

```
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
├── clients/                # OPTIONAL: Inter-service gRPC clients
│   ├── {service}_client.go
│   └── circuitbreaker.go
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
| `clients/` | Service calls other services | current-account calling financial-accounting |
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

The `repository/` directory at service root level should **not** be used. Repository implementations belong in `adapters/persistence/`. The domain defines repository interfaces (ports) in `domain/`, and implementations (adapters) live in `adapters/persistence/`.

This aligns with ADR-005 (Adapter Pattern Layer Translation):
- `domain/repository.go` - Interface definition (port)
- `adapters/persistence/repository.go` - Implementation (adapter)

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
- `shared/pkg/interceptors/` - gRPC interceptors (recovery, logging, auth)
- `shared/pkg/health/` - Common health check interfaces
- `shared/pkg/grpc/` - gRPC utilities

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

## Notes

Future services should be created using this structure from the start. Consider creating a service template or generator to bootstrap new services with the correct structure.
