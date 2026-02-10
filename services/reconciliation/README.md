---
name: reconciliation-service
description: BIAN Account Reconciliation service for matching and reconciling positions across services
triggers:
  - Reconciling account positions between services
  - Matching financial transactions across ledgers
  - Settlement processing and scheduling
  - Investigating reconciliation discrepancies
instructions: |
  Reconciliation manages the matching and verification of account positions across
  Position Keeping, Financial Accounting, and Current Account services.

  Key concepts:
  - Reconciliation runs compare positions across multiple sources
  - Discrepancies are identified and tracked for resolution
  - Settlement scheduler automates periodic reconciliation

  Port: 50060 (gRPC)
---

# Reconciliation Service

BIAN-compliant Account Reconciliation service for matching and reconciling positions across Meridian services.

## Overview

| Attribute | Value |
|-----------|-------|
| **BIAN Domain** | Account Reconciliation |
| **Port** | 50060 (gRPC) |
| **Language** | Go |
| **Database** | CockroachDB |
| **Standalone** | Yes |

## Architecture

The Reconciliation service acts as a cross-cutting concern, comparing positions across
multiple upstream services to verify consistency and identify discrepancies.

### Service Dependencies

| Service | Purpose |
|---------|---------|
| Position Keeping | Source of transaction logs and balance positions |
| Financial Accounting | Source of ledger entries and journal postings |
| Current Account | Source of account state and balances |
| Reference Data | Instrument definitions and validation rules |
| Payment Order | Settlement execution for resolved discrepancies |

## Configuration

| Variable | Default | Purpose |
|----------|---------|---------|
| `GRPC_PORT` | 50060 | gRPC server port |
| `METRICS_PORT` | 9090 | HTTP metrics and health port |
| `DATABASE_URL` | - | CockroachDB connection string |
| `KAFKA_BROKERS` | - | Kafka broker addresses |
| `REDIS_URL` | - | Redis connection URL |
| `POSITION_KEEPING_URL` | - | Position Keeping gRPC address |
| `FINANCIAL_ACCOUNTING_URL` | - | Financial Accounting gRPC address |
| `CURRENT_ACCOUNT_URL` | - | Current Account gRPC address |
| `REFERENCE_DATA_URL` | - | Reference Data gRPC address |
| `PAYMENT_ORDER_URL` | - | Payment Order gRPC address |
| `SETTLEMENT_SCHEDULER_ENABLED` | false | Enable automated settlement scheduling |

## Local Development

### Prerequisites

- Go 1.25+
- CockroachDB (via Docker or local install)
- Protocol Buffers compiler (`buf`)

### Running Locally

```bash
# Set required environment variables
export DATABASE_URL="postgres://meridian_reconciliation_user@localhost:26257/meridian_reconciliation?sslmode=disable"

# Run the service
go run ./services/reconciliation/cmd

# Run tests
go test ./services/reconciliation/...
```

### Building

```bash
# Build binary
go build -o reconciliation ./services/reconciliation/cmd

# Build Docker image
docker build -f services/reconciliation/cmd/Dockerfile -t reconciliation:latest .
```

## References

- [BIAN Account Reconciliation](https://bian.org/semantic-apis/account-reconciliation/)
- [Service Architecture](../README.md)
