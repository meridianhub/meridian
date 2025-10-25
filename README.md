# Meridian - BIAN-Compliant Open Banking Ledger

A learning-focused reference implementation of an open banking ledger following BIAN (Banking Industry Architecture Network) standards. This project demonstrates how to build a distributed banking system using modern microservices patterns and BIAN-compliant service domains.

## Project Goals

This is a **reference implementation** for educational purposes, demonstrating:

- BIAN-compliant service domain architecture
- Modern microservices patterns for financial systems
- Double-entry accounting principles in distributed systems
- Protocol Buffer-based API design for banking services
- Event-driven architecture with Kafka
- Kubernetes-native deployment patterns

**Note**: This is not production-ready software. It's designed as a learning tool and architectural reference for building BIAN-compliant banking systems.

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
- **AccountReconciliation**: Transaction verification and reconciliation

Each service domain follows BIAN's control record pattern with behavior qualifiers for operations.

Reference specifications: BIAN Service Landscape 13.0.0

## Technology Stack

- **Language**: Go 1.23+
- **API Protocol**: Protocol Buffers 3 + gRPC
- **API Tooling**: buf CLI for linting and code generation
- **Database**: CockroachDB or YugabyteDB (distributed SQL)
- **Event Streaming**: Apache Kafka 3.x with Schema Registry
- **Cache**: Redis 7.x
- **Container Orchestration**: Kubernetes 1.28+
- **Local Development**: Tilt for local Kubernetes workflows
- **Observability**: OpenTelemetry, Prometheus, Grafana

## Development Workflow

### Prerequisites

- Go 1.23 or later
- buf CLI for protobuf development
- Docker and Kubernetes (kind/minikube for local development)
- Make

### Getting Started

1. **Clone the repository**:
   ```bash
   git clone git@github.com:bjcoombs/meridian.git
   cd meridian
   ```

2. **Install dependencies**:
   ```bash
   go mod download
   ```

3. **Generate protobuf code**:
   ```bash
   make proto
   ```

4. **Build the project**:
   ```bash
   make build
   ```

5. **Run tests**:
   ```bash
   make test
   ```

### Working with Protocol Buffers

All API contracts are defined using Protocol Buffers in `api/proto/`:

```bash
# Lint protobuf files
make proto-lint

# Generate Go code from proto definitions
make proto

# Check for breaking changes
make proto-breaking
```

See `api/proto/README.md` for detailed protobuf development guidelines.

### Local Development

Use Tilt for local Kubernetes development:

```bash
# Start local development environment
tilt up
```

This will:
- Build container images
- Deploy to local Kubernetes cluster
- Enable hot-reload on code changes
- Provide logs and resource monitoring

## Architecture Decision Records

All architectural decisions are documented in `docs/adr/`:

- **ADR-0001**: Record Architecture Decisions (MADR format)
- **ADR-0002**: Microservices Per BIAN Domain
- **ADR-0003**: Database Schema Migrations (golang-migrate)
- **ADR-0004**: Kafka Schema Registry with Protobuf

See [docs/adr/README.md](docs/adr/README.md) for the complete list.

## API Documentation

### Common Types

Located in `api/proto/meridian/common/v1/`:

- **types.proto**: Shared types for Money, AccountType, Currency, Pagination
- **error.proto**: Standardized error codes and error handling
- **health.proto**: Health check service for all components

### Error Codes

Errors are categorized for different domains:

- **1000-1999**: General errors (INTERNAL, INVALID_ARGUMENT, NOT_FOUND, etc.)
- **2000-2999**: Financial errors (INSUFFICIENT_FUNDS, POSTING_FAILED, etc.)
- **3000-3999**: BIAN-specific errors (CONTROL_RECORD_NOT_FOUND, etc.)

## Contributing

Contributions are welcome! This is a learning project, so:

1. **Fork the repository**
2. **Create a feature branch** (`git checkout -b feature/my-feature`)
3. **Make your changes** following existing patterns
4. **Run tests** (`make test`)
5. **Lint your code** (`make lint`)
6. **Create a Pull Request**

### Code Standards

- Follow Go conventions and idioms
- Add tests for new functionality
- Update ADRs for architectural changes
- Keep protobuf definitions backward-compatible
- Document BIAN compliance in comments

## Learning Resources

### BIAN Standards

- [BIAN Service Landscape](https://bian.org/servicelandscape/)
- [BIAN Semantic APIs](https://bian.org/semantic-apis/)
- BIAN specifications in `../bian/bian-public-main/release13.0.0/`

### Related Technologies

- [Protocol Buffers](https://protobuf.dev/)
- [buf CLI](https://buf.build/docs/)
- [gRPC](https://grpc.io/)
- [CockroachDB](https://www.cockroachlabs.com/docs/)
- [Apache Kafka](https://kafka.apache.org/documentation/)

## License

Apache License 2.0 - See LICENSE file for details.

## Disclaimer

This software is for educational and reference purposes only. It is not audited, tested, or designed for production use in financial systems. Do not use this code in production environments without thorough review, testing, and compliance verification.
