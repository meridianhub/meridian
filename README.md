# Meridian - BIAN-Compliant Open Banking Ledger

An open source, cloud-native core banking engine following BIAN (Banking Industry Architecture Network) standards.

**What it demonstrates:**

- BIAN-compliant service domain architecture
- Modern microservices patterns for financial systems
- Double-entry accounting in distributed systems
- Protocol Buffer-based API design
- Event-driven architecture with Kafka
- Kubernetes-native deployment

**Features**:

- Production-grade architecture patterns for financial systems
- Comprehensive API design with Protocol Buffers
- Distributed transaction handling and event-driven workflows

## Project Structure

```text
meridian/
├── services/                    # BIAN service domains (domain-centric)
│   ├── current-account/         # Customer-facing account management
│   │   ├── cmd/                 # Entry point and Dockerfile
│   │   ├── domain/              # Business logic and entities
│   │   ├── adapters/            # Persistence, messaging adapters
│   │   ├── service/             # gRPC service implementation
│   │   ├── client/              # Service-owned gRPC client
│   │   ├── migrations/          # Database migrations
│   │   ├── atlas/               # Atlas schema config
│   │   └── k8s/                 # Kubernetes manifests
│   ├── financial-accounting/    # Double-entry general ledger
│   ├── party/                   # Customer and party reference data
│   ├── payment-order/           # Payment execution
│   ├── position-keeping/        # Pre-ledger transaction log
│   └── tenant/                  # Multi-tenant platform management
├── shared/                      # Cross-service shared code
│   ├── platform/                # Infrastructure (auth, db, kafka, observability)
│   ├── domain/                  # Shared domain models and primitives
│   └── pkg/                     # Shared utilities (health, idempotency)
├── utilities/                   # CLI tools
│   ├── meridian/                # Main CLI
│   ├── atlas-loader/            # Migration schema loader
│   └── horizon-demo/            # Demo utility
├── api/proto/                   # Protocol Buffer API definitions
├── deployments/k8s/             # Shared Kubernetes resources (base, overlays)
└── docs/                        # Documentation and ADRs
```

## BIAN Service Domains

This implementation includes the following BIAN service domains:

| Service | BIAN Domain | Purpose | Standalone | BIAN Spec |
|---------|-------------|---------|:----------:|-----------|
| [**CurrentAccount**][svc-ca] | Current Account | Customer-facing account management and transaction orchestration | No | [OAS3][bian-ca] |
| [**FinancialAccounting**][svc-fa] | Financial Standard Management | Double-entry bookkeeping and general ledger | Yes | [OAS3][bian-fa] |
| [**Party**][svc-party] | Party Reference Data Directory | Customer and party reference data management | Yes | [OAS3][bian-party] |
| [**PaymentOrder**][svc-po] | Payment Order | Payment initiation, saga orchestration, and settlement | No | [OAS3][bian-po] |
| [**PositionKeeping**][svc-pk] | Position Keeping | Pre-ledger transaction log and position tracking | Yes | [OAS3][bian-pk] |

Each service domain follows BIAN's control record pattern with behavior qualifiers for operations.
Services marked as "Standalone" can operate independently; others require upstream dependencies.

Reference specifications: [BIAN Service Landscape 13.0.0](https://github.com/bian-official/public/tree/main/release13.0.0)

[bian-ca]: https://github.com/bian-official/public/blob/main/release13.0.0/semantic-apis/oas3/yamls/CurrentAccount.yaml
[bian-fa]: https://github.com/bian-official/public/blob/main/release13.0.0/semantic-apis/oas3/yamls/FinancialAccounting.yaml
[bian-party]: https://github.com/bian-official/public/blob/main/release13.0.0/semantic-apis/oas3/yamls/PartyReferenceDataDirectory.yaml
[bian-po]: https://github.com/bian-official/public/blob/main/release13.0.0/semantic-apis/oas3/yamls/PaymentOrder.yaml
[bian-pk]: https://github.com/bian-official/public/blob/main/release13.0.0/semantic-apis/oas3/yamls/PositionKeeping.yaml
[svc-ca]: services/current-account/
[svc-fa]: services/financial-accounting/
[svc-party]: services/party/
[svc-po]: services/payment-order/
[svc-pk]: services/position-keeping/
[svc-tenant]: services/tenant/

### Infrastructure Services

| Service | Purpose | Standalone |
|---------|---------|:----------:|
| [**Tenant**][svc-tenant] | Multi-tenant platform management with PostgreSQL schema-per-tenant isolation | Yes |

The Tenant service is not part of the BIAN standard but is essential for shared-cluster deployments
requiring data isolation between organizations. It provides schema-based multi-tenancy where each
tenant's data is isolated in a dedicated PostgreSQL schema (`org_{tenant_id}`).

## Technology Stack

- **Language**: Go 1.25+
- **API Protocol**: Protocol Buffers 3 + gRPC
- **API Tooling**: buf CLI for linting and code generation
- **Database**: CockroachDB or YugabyteDB (distributed SQL)
- **Event Streaming**: Apache Kafka 3.x (3-broker cluster with KRaft)
- **Cache**: Redis 7.x
- **Container Orchestration**: Kubernetes 1.28+
- **Local Development**: Kind + ctlptl + Tilt for fast local Kubernetes workflows
- **Observability**: OpenTelemetry, Prometheus, Grafana

## System Requirements

**Minimum**: 12GB RAM (may experience swap with other applications running)
**Recommended**: 16GB RAM (comfortable development with IDE, browser, etc.)

**Resource Breakdown**:

- Kubernetes (Kind): ~1GB
- CockroachDB: ~1-2GB
- Kafka cluster (3 brokers): ~1.5GB
- Redis: ~128MB
- Meridian service: ~256MB
- OS overhead: ~2GB

**For 8GB RAM machines**: See [Resource-Constrained Development](docs/skills/tilt.md#resource-constrained-development)
for single-broker Kafka configuration.

## Quick Start

**Prerequisites**: Go 1.25+, Docker, kubectl, kind, tilt ([see detailed
setup](CONTRIBUTING.md#development-environment-setup))

```bash

# 1. Clone and install dependencies

git clone git@github.com:meridianhub/meridian.git
cd meridian
go mod download
./setup-hooks.sh

# 2. Setup local development secrets

./scripts/setup-local-secrets.sh

# 3. Create local Kubernetes cluster (Docker must be running)

ctlptl create cluster kind --registry=ctlptl-registry --name=kind-meridian-local

# 4. Start development environment

tilt up
```

**Access services**:

- Tilt UI: <http://localhost:10350>
- Meridian API: <http://localhost:8080>
- Meridian gRPC: localhost:9090

For detailed development instructions, see [CONTRIBUTING.md](CONTRIBUTING.md).

## Development

**Common commands**:

```bash
make proto         # Generate protobuf code
make build         # Build the project
make test          # Run tests
make lint          # Run linters
```

**Local development**: Use `tilt up` for hot-reload Kubernetes development ([Tilt guide](docs/skills/tilt.md))

**Working with protobuf**: See [api/proto/README.md](api/proto/README.md) and [Schema Evolution
skill](docs/skills/schema-evolution.md)

## Architecture

**Key architectural decisions** are documented in [docs/adr/](docs/adr/) including:

- Microservices per BIAN domain
- Database schema migrations with Atlas
- Event schema evolution with protobuf
- Adapter pattern for layer translation
- Local development with Tilt
- Standard service directory structure

**Architecture diagrams:**

- [Service API Interfaces](api/proto/README.md#service-architecture) - gRPC methods and service dependencies
- [Runtime Architecture](services/README.md) - All protocols (gRPC, Kafka, HTTP, Database, Redis)

See [docs/adr/README.md](docs/adr/README.md) for the complete catalog.

## API Documentation

Protocol Buffer definitions for all services are in [api/proto/](api/proto/).

**Key APIs**:

- Common types: Money, AccountType, Currency, Pagination
- Error codes: Categorized by domain (general, financial, BIAN-specific)
- Health checks: Standard health service for all components

See [api/proto/README.md](api/proto/README.md) for detailed API documentation.

## Troubleshooting

For setup and development issues, see:

- [CONTRIBUTING.md](CONTRIBUTING.md) - Development setup troubleshooting
- [docs/skills/docker.md](docs/skills/docker.md) - Docker configuration
- [docs/skills/tilt.md](docs/skills/tilt.md) - Tilt and Kubernetes issues
- [docs/runbooks/](docs/runbooks/) - Operational procedures

## Contributing

Contributions welcome! We value collaboration and view questions and feedback as opportunities for continuous improvement.

**Quick start**:

1. Fork and create a feature branch
2. Make changes following [code standards](CONTRIBUTING.md#code-standards)
3. Run `make test` and `make lint`
4. Create a Pull Request

See [CONTRIBUTING.md](CONTRIBUTING.md) for detailed guidelines on:

- Development environment setup
- Code standards (immutability, TDD, defensive testing)
- Pull request process
- Architecture decision records

## Learning Resources

**BIAN Standards**:

- [BIAN Service Landscape](https://bian.org/servicelandscape/) - Banking service domain architecture
- [BIAN Semantic APIs](https://bian.org/semantic-apis/) - API design standards

**Technologies**:

- [Protocol Buffers](https://protobuf.dev/) & [buf CLI](https://buf.build/docs/)
- [gRPC](https://grpc.io/) - API framework
- [CockroachDB](https://www.cockroachlabs.com/docs/) - Distributed SQL database
- [Apache Kafka](https://kafka.apache.org/documentation/) - Event streaming

## License

Apache License 2.0 - See LICENSE file for details.

## Disclaimer

This software is provided "as-is" under the Apache License 2.0. Before deploying in production environments, ensure
thorough security audits, compliance verification, and testing appropriate for your regulatory requirements and risk
profile.
