# Meridian - BIAN-Compliant Open Banking Ledger

A learning-focused reference implementation of an open banking ledger following BIAN (Banking Industry Architecture Network) standards.

**What it demonstrates:**
- BIAN-compliant service domain architecture
- Modern microservices patterns for financial systems
- Double-entry accounting in distributed systems
- Protocol Buffer-based API design
- Event-driven architecture with Kafka
- Kubernetes-native deployment

**Note**: This is a learning tool, not production-ready software. It's designed as an architectural reference for building BIAN-compliant banking systems.

## Project Structure

```
meridian/
├── api/proto/              # Protocol Buffer API definitions
│   └── meridian/
│       └── common/v1/      # Common types and error handling
├── cmd/                    # Application entry points
├── deployments/            # Kubernetes manifests and deployment configs
│   └── k8s/base/          # Base Kubernetes resources
├── docs/                   # Documentation
│   └── adr/               # Architecture Decision Records
├── internal/              # Private application code
└── pkg/                   # Public library code
```

## BIAN Service Domains

This implementation includes the following BIAN service domains:

- **FinancialAccounting**: Double-entry general ledger with audit trail
- **PositionKeeping**: Pre-ledger transaction log and position tracking
- **CurrentAccount**: Customer-facing account management

Each service domain follows BIAN's control record pattern with behavior qualifiers for operations.

Reference specifications: BIAN Service Landscape 13.0.0

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

**For 8GB RAM machines**: See [Resource-Constrained Development](docs/skills/tilt.md#resource-constrained-development) for single-broker Kafka configuration.

## Quick Start

**Prerequisites**: Go 1.25+, Docker, kubectl, kind, tilt ([see detailed setup](CONTRIBUTING.md#development-environment-setup))

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
- Tilt UI: http://localhost:10350
- Meridian API: http://localhost:8080
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

**Working with protobuf**: See [api/proto/README.md](api/proto/README.md) and [Schema Evolution skill](docs/skills/schema-evolution.md)

## Architecture

**Key architectural decisions** are documented in [docs/adr/](docs/adr/) including:
- Microservices per BIAN domain
- Database schema migrations with Atlas
- Event schema evolution with protobuf
- Adapter pattern for layer translation
- Local development with Tilt

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

Contributions welcome! This is a learning project - questions and mistakes are opportunities for growth.

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

This software is for educational and reference purposes only. It is not audited, tested, or designed for production use in financial systems. Do not use this code in production environments without thorough review, testing, and compliance verification.
