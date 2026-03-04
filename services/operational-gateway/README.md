# Operational Gateway

The operational-gateway handles non-financial outbound dispatch: IoT telemetry,
KYC verification, partner file transfers, settlement notifications, and similar
operational instruction types.

## Supported Instruction Types

Instructions with the following type prefixes are accepted:

| Prefix | Examples | Description |
|--------|----------|-------------|
| `kyc.*` | `kyc.verify`, `kyc.refresh` | Identity and KYC provider calls |
| `device.*` | `device.ping`, `device.register` | IoT and device management |
| `settlement.*` | `settlement.initiate`, `settlement.notify` | Settlement notifications |
| `partner.*` | `partner.file.upload`, `partner.notify` | Partner integrations |

## Unsupported Instruction Types

**`payment.*` instruction types are NOT supported by the operational-gateway.**

Payment instructions must be dispatched through the **financial-gateway**, which
provides the double-entry ledger integration, lien management, and regulatory
audit trail required for financial transactions.

Attempting to resolve a `payment.*` route via the operational-gateway returns:

```text
InvalidArgument: payment instructions must use financial-gateway, not operational-gateway
```

## Architecture

The operational-gateway follows a ports-and-adapters (hexagonal) architecture:

- **domain/** — core instruction and connection models
- **ports/** — interface definitions (repositories, dispatcher, resolver)
- **adapters/persistence/** — CockroachDB-backed repositories and route resolver
- **adapters/httpadapter/** — outbound HTTP dispatcher
- **adapters/mapping/** — payload mapping transformer
- **adapters/passthrough/** — no-op passthrough transformer
- **service/** — gRPC service implementations
- **worker/** — dispatch and expiry background workers

## Key Invariants

- Instructions are persisted before dispatch (write-ahead, at-least-once delivery)
- The dispatch worker uses `SELECT FOR UPDATE SKIP LOCKED` for concurrent-safe batching
- Retry logic uses exponential backoff per the connection `RetryPolicy`
- Circuit breaker state is tracked per provider connection
