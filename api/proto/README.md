# Meridian Protocol Buffer Definitions

This directory contains Protocol Buffer (protobuf) definitions for all Meridian services and events.

## Directory Structure

```text
api/proto/
├── meridian/
│   ├── common/v1/              # Common types shared across services
│   ├── financial_accounting/v1/  # FinancialAccounting BIAN service
│   ├── position_keeping/v1/      # PositionKeeping BIAN service
│   ├── current_account/v1/       # CurrentAccount BIAN service
│   └── events/                   # Kafka event schemas
│       ├── financial_accounting/v1/
│       ├── position_keeping/v1/
│       └── current_account/v1/
```text

## Tooling

Meridian uses [buf](https://buf.build) for protobuf management:

- **buf.yaml**: Lint and breaking change detection configuration
- **buf.gen.yaml**: Code generation configuration
- **Makefile**: Convenience targets for proto operations

### Available Commands

```bash

# Generate Go code from proto definitions

make proto

# Lint proto files

make proto-lint

# Check for breaking changes against develop branch

make proto-breaking

# Install all protobuf tools

make install
```text

## Code Generation

Generated code is placed alongside proto definitions:

```text
api/proto/meridian/common/v1/
├── health.proto          # Source definition
├── health.pb.go          # Generated protobuf code
├── health_grpc.pb.go     # Generated gRPC service code
└── health.pb.validate.go # Generated validation code
```text

**Note**: Generated files (`*.pb.go`) are committed to version control for reproducibility.

## Writing Proto Definitions

### Package Naming

Follow the pattern: `meridian.<domain>.<version>`

```protobuf
syntax = "proto3";

package meridian.financial_accounting.v1;

option go_package = "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1;financialaccountingv1";
```text

### Directory Structure

Proto files must be in a directory matching their package:

- Package: `meridian.common.v1`
- Directory: `api/proto/meridian/common/v1/`

### Linting Rules

Buf enforces strict linting rules (configured in buf.yaml):

1. **STANDARD**: Follow standard protobuf style guide
2. **COMMENTS**: All public elements must have comments
3. **Enum zero values**: Must end with `_UNSPECIFIED`
4. **Service suffix**: Services must end with `Service`
5. **RPC naming**: Request/Response types should match RPC name

Example:

```protobuf
// HealthService provides health check endpoints.
service HealthService {
  // Check performs a health check.
  rpc Check(CheckRequest) returns (CheckResponse);
}

// CheckRequest is the request for a health check.
message CheckRequest {
  // service is the name of the service to check (optional).
  string service = 1;
}

// CheckResponse is the response for a health check.
message CheckResponse {
  // ServingStatus describes the health status.
  enum ServingStatus {
    // SERVING_STATUS_UNSPECIFIED means the status is unknown.
    SERVING_STATUS_UNSPECIFIED = 0;
    // SERVING_STATUS_SERVING means healthy and serving.
    SERVING_STATUS_SERVING = 1;
  }

  // status is the health status of the service.
  ServingStatus status = 1;
}
```text

## Breaking Change Detection

Buf compares proto changes against the `develop` branch to detect breaking changes:

```bash
make proto-breaking
```text

This prevents accidental API breakages before merging to develop.

## Event Schema Evolution

Event schemas in `api/proto/meridian/events/` use protobuf's native versioning (per ADR-0004):

- Schema compatibility validated via `buf breaking` in CI/CD
- No runtime schema registry needed (Kafka is internal-only)
- Same protobuf definitions used for both gRPC and Kafka events
- New BIAN behavior qualifiers → new event types (new topics)
- Backward-compatible changes → add optional fields

See [ADR-0004: Event Schema Evolution Strategy](../../docs/adr/0004-event-schema-evolution.md) for details.

## References

- [buf Documentation](https://buf.build/docs)
- [Protocol Buffers Guide](https://protobuf.dev/programming-guides/proto3/)
- [gRPC Go Quick Start](https://grpc.io/docs/languages/go/quickstart/)
- [ADR-0004: Event Schema Evolution Strategy](../../docs/adr/0004-event-schema-evolution.md)
