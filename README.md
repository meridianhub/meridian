# Meridian

**Trust Your Numbers.** Open-source treasury infrastructure for the modern economy.

> **Status: Active Development** | Core ledger, audit trails, and saga orchestration implemented.
> Valuation/settlement integrations are architectural placeholders.

## Mission

When your system accuses someone of a shortfall, you need absolute certainty. Meridian is
open-source treasury infrastructure designed to prove itself - every position recorded with
atomic audit trails, every transaction path traceable, every balance verifiable.

### Measure

Every position recorded with atomic audit trails. Parent-child transaction lineage preserved.
Idempotent operations mean the same request twice produces the same result once. The accused
can demand proof - and get it.

### Value

Value does not always look like currency anymore. Kilowatt-hours, tonnes of CO₂, commodity
units - the economy runs on assets that traditional ledgers cannot handle. Multi-asset
architecture handles diverse units with the same rigour as pounds and euros. Proper
dimensional typing prevents nonsense calculations at compile time.

### Settle

When settlement fails, it cascades. Livelihoods depend on money arriving when promised,
not stuck in a partial state that requires manual intervention. Lien-based fund reservation
ensures availability before commitment. Saga orchestration with automatic compensation -
if anything fails, the system unwinds cleanly. Settlement completes or reverts.

### Why Trust It

Meridian is infrastructure you actually own. BIAN-compliant architecture means your team speaks
the same technical language as the world's largest banks. Kubernetes-native, horizontally scalable,
built for growth. Every deployment builds institutional expertise that stays with you.
Full sovereignty. Open access. Verify everything.

## What it Demonstrates

- BIAN-compliant service domain architecture
- Modern microservices patterns for financial systems
- Double-entry accounting in distributed systems
- Protocol Buffer-based API design
- Event-driven architecture with Kafka
- Kubernetes-native deployment
- Multi-asset ledger capabilities with dimensional type safety
  ([ADR-0013](docs/adr/0013-generic-asset-quantity-types.md))
- Tenant-defined instrument catalogs with CEL validation
  ([ADR-0014](docs/adr/0014-financial-instrument-reference-data.md))

## Multi-Asset Capabilities

Meridian's [Quantity\[D\] type system](docs/adr/0013-generic-asset-quantity-types.md) separates
physics (compile-time dimensional safety) from policy (runtime instrument definitions), enabling
universal transaction integrity across asset classes.

### Monetary Dimension

| Instrument Type | Example | Valuation Approach |
|-----------------|---------|---------------------|
| Currency | USD, EUR, GBP | Identity (implemented) |
| Debt | Bonds, Loans | Market price × accrued interest |
| Equity | Shares, Stock | Market price |
| Derivatives | Options, Futures | Pricing model |

### Commodity Dimension

| Instrument Type | Example | Valuation Approach |
|-----------------|---------|---------------------|
| Utility Units | kWh, therms | Rate schedule |
| Compute Resources | GPU-hours, vCPU-seconds | Spot pricing |
| Environmental Credits | tCO₂e | Exchange pricing |
| Physical Goods | kg, units | Market pricing |

*Valuation providers are pluggable. Currency identity is implemented; other valuation
approaches show the extensibility model. See [ADR-0013](docs/adr/0013-generic-asset-quantity-types.md)
for the ValuationProvider interface.*

## Features

- Production-grade architecture patterns for financial systems
- Comprehensive API design with Protocol Buffers
- Distributed transaction handling and event-driven workflows

## Project Structure

```text
meridian/
├── services/                    # Service implementations
│   ├── current-account/         # CurrentAccount service
│   │   ├── cmd/                 # Entry point and Dockerfile
│   │   ├── domain/              # Business logic and entities
│   │   ├── adapters/            # Persistence, messaging adapters
│   │   ├── service/             # gRPC service implementation
│   │   ├── client/              # Service-owned gRPC client
│   │   ├── atlas/               # Atlas schema config
│   │   ├── migrations/          # Database migrations
│   │   └── k8s/                 # Kubernetes manifests
│   ├── financial-accounting/    # FinancialAccounting service
│   ├── gateway/                 # Gateway service
│   ├── party/                   # Party service
│   ├── payment-order/           # PaymentOrder service
│   ├── position-keeping/        # PositionKeeping service
│   ├── reference-data/          # ReferenceData service
│   ├── tenant/                  # Tenant service
│   ├── audit-worker/            # Audit log processor
│   └── utilization-metering-consumer/  # Usage metering
├── shared/                      # Cross-service shared code
│   ├── platform/                # Infrastructure (auth, db, kafka, observability)
│   ├── domain/                  # Shared domain models and primitives
│   └── pkg/                     # Shared utilities (health, idempotency, clients)
├── cmd/                         # CLI tools
│   └── tenantctl/               # Tenant management CLI
├── utilities/                   # Development utilities
│   ├── atlas-loader/            # Migration schema loader
│   └── horizon-demo/            # Demo utility
├── api/proto/                   # Protocol Buffer API definitions
├── deployments/k8s/             # Shared Kubernetes resources
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
| [**ReferenceData**][svc-rd] | Financial Instrument Reference Data Management | Tenant-defined instrument catalog with CEL validation | Yes | [OAS3][bian-rd] |

Each service domain follows BIAN's control record pattern with behavior qualifiers for operations.
Services marked as "Standalone" can operate independently; others require upstream dependencies.

Reference specifications: [BIAN Service Landscape 13.0.0](https://github.com/bian-official/public/tree/main/release13.0.0)

[bian-ca]: https://github.com/bian-official/public/blob/main/release13.0.0/semantic-apis/oas3/yamls/CurrentAccount.yaml
[bian-fa]: https://github.com/bian-official/public/blob/main/release13.0.0/semantic-apis/oas3/yamls/FinancialAccounting.yaml
[bian-party]: https://github.com/bian-official/public/blob/main/release13.0.0/semantic-apis/oas3/yamls/PartyReferenceDataDirectory.yaml
[bian-po]: https://github.com/bian-official/public/blob/main/release13.0.0/semantic-apis/oas3/yamls/PaymentOrder.yaml
[bian-pk]: https://github.com/bian-official/public/blob/main/release13.0.0/semantic-apis/oas3/yamls/PositionKeeping.yaml
[bian-rd]: https://github.com/bian-official/public/blob/main/release13.0.0/semantic-apis/oas3/yamls/FinancialInstrumentReferenceDataManagement.yaml
[svc-ca]: services/current-account/
[svc-fa]: services/financial-accounting/
[svc-party]: services/party/
[svc-po]: services/payment-order/
[svc-pk]: services/position-keeping/
[svc-rd]: services/reference-data/
[svc-tenant]: services/tenant/

### Infrastructure Services

| Service | Purpose |
|---------|---------|
| [**Tenant**][svc-tenant] | Multi-tenant platform management with schema-per-tenant isolation |
| **Gateway** | API gateway for external access |
| **audit-worker** | Processes audit log outbox entries |
| **utilization-metering-consumer** | Usage metering for billing |

These services are not part of the BIAN standard but provide platform infrastructure.

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
- [Universal Quantity Type System](docs/adr/0013-generic-asset-quantity-types.md) - Dimensional type
  safety for multi-asset ledger
- [Financial Instrument Reference Data](docs/adr/0014-financial-instrument-reference-data.md) -
  Tenant-defined instrument catalog with CEL validation

**Architecture diagrams:**

- [Service API Interfaces](api/proto/README.md#service-architecture) - gRPC methods and service dependencies
- [Runtime Architecture](services/README.md) - All protocols (gRPC, Kafka, HTTP, Database, Redis)

See [docs/adr/README.md](docs/adr/README.md) for the complete catalog.

## API Documentation

Protocol Buffer definitions for all services are in [api/proto/](api/proto/).

**Key APIs**:

- Common types: Quantity[D], Money, AccountType, Currency, Pagination
- Financial instruments: Dimensional type system with runtime instrument definitions
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
