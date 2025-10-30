# Meridian Demo Guide - Tuesday Meeting

## Overview
Demonstrates event-driven microservices with Kafka for CurrentAccount and FinancialAccounting BIAN domains.

## Architecture

```
┌─────────────────┐      Kafka Topic:           ┌─────────────────────────┐
│                 │   current-account.deposits  │                         │
│  CurrentAccount │─────────────────────────────▶│  FinancialAccounting   │
│    Service      │                             │       Service           │
│   (gRPC 9091)   │   ExecuteDepositRequest     │     (gRPC 9092)         │
│                 │◀─────────────────────────────│                         │
└─────────────────┘  financial-accounting.      └─────────────────────────┘
                         postings
                     LedgerPosting
```

## Event Flow

1. **User → CurrentAccount**: Execute deposit via gRPC
2. **CurrentAccount → Kafka**: Publish `ExecuteDepositRequest` proto to `current-account.deposits`
3. **Kafka → FinancialAccounting**: Consumer deserializes proto message
4. **FinancialAccounting**: Creates double-entry ledger postings (debit + credit)
5. **FinancialAccounting → Kafka**: Publish `LedgerPosting` proto to `financial-accounting.postings`
6. **Kafka → CurrentAccount**: Consumer updates account status to "posted"

## Key Features Demonstrated

✅ **BIAN Compliance**: Two BIAN service domains (CurrentAccount, FinancialAccounting)
✅ **Proto-First**: Protocol Buffers for gRPC APIs and Kafka messages
✅ **Event-Driven**: Asynchronous communication via Kafka
✅ **Type Safety**: Proto messages ensure schema consistency
✅ **Double-Entry**: Proper accounting with debit/credit postings
✅ **Eventual Consistency**: Account status updated after ledger confirms
✅ **Cloud-Native**: Kubernetes, CockroachDB, Kafka 3.9.1 with KRaft

## Running the Demo

### Prerequisites
```bash
brew install grpcurl jq kubectl
tilt up  # Ensure all services running
```

### Quick Demo (5 minutes)
```bash
./scripts/demo.sh
```

### Watch Kafka Events (separate terminal)
```bash
./scripts/kafka-watch.sh
```

### Manual Step-by-Step

**1. Create Account**
```bash
grpcurl -plaintext -d '{
  "customer_reference": "CUST-001",
  "product_service_type": {"type": "STANDARD_CURRENT_ACCOUNT"},
  "account_currency": "GBP"
}' localhost:9091 meridian.current_account.v1.CurrentAccountService/InitiateCurrentAccount
```

**2. Execute Deposit**
```bash
grpcurl -plaintext -d '{
  "current_account_facility_reference": "ACC-123",
  "amount": {"currency": "GBP", "units": 100}
}' localhost:9091 meridian.current_account.v1.CurrentAccountService/ExecuteDeposit
```

**3. Check Ledger**
```bash
grpcurl -plaintext -d '{
  "account_reference": "ACC-123"
}' localhost:9092 meridian.financial_accounting.v1.FinancialAccountingService/ListLedgerPostings
```

## Integration Tests

Run automated tests:
```bash
go test ./test/integration/... -v
```

Tests validate:
- Account creation
- Deposit execution
- Kafka event propagation
- Double-entry postings
- Balance updates
- Multiple deposits

## OpenCoreOS Interview Talking Points

### What We've Built (3 days)
1. **Platform utilities**: Generic Kafka producer/consumer for protobuf (52.4% test coverage)
2. **CurrentAccount service**: BIAN-compliant with gRPC + Kafka publisher
3. **FinancialAccounting service**: Double-entry ledger with Kafka consumer
4. **Integration tests**: End-to-end validation

### Architecture Highlights
- **BIAN patterns**: Control Records, Behavior Qualifiers, Service Operations
- **Proto everywhere**: gRPC APIs + Kafka messages (no JSON)
- **Event-driven**: Services communicate via typed proto events
- **Schema evolution**: Buf breaking change detection in CI

### Questions for OpenCoreOS

**Interest Engine:**
"You list Interest Engine as a core product. How does this integrate with BIAN patterns? We implemented account features as Behavior Qualifiers per BIAN spec—is your Interest Engine similar or standalone?"

**Reconciliation:**
"Your Reconciliation Engine handles 1:1, 1:N, N:M matching. How does it consume events from multiple services? We're using proto messages in Kafka—do you use Schema Registry?"

**Compliance:**
"How does your Compliance Rules Engine integrate with event streams? Do rules evaluate events in real-time or batch?"

**Scale:**
"You mention 100M+ accounts and 300M+ daily transactions. What's your event throughput on Kafka? We're using KRaft mode—do you use Zookeeper or KRaft?"

**MCP for AX:**
"You have an MCP server for 'Agent Experience.' How does that integrate with BIAN service domains? Is it an orchestration layer?"

## Technical Deep Dives (if asked)

### Why Proto in Kafka?
- Type safety across service boundaries
- Buf validates schema compatibility in CI
- Smaller message size vs JSON
- Industry standard (Uber, Netflix, Confluent)

### Why Separate Services?
- Independent scaling (deposits vs ledger posting)
- Database per service (no shared DB anti-pattern)
- Failure isolation
- BIAN alignment (one service per domain)

### Eventual Consistency Approach
- Account updates immediately in CurrentAccount DB
- Ledger posting happens asynchronously
- Status updated when confirmed
- At-least-once delivery semantics

## Troubleshooting

**Services not responding:**
```bash
kubectl get pods  # Check all running
tilt logs meridian  # Check app logs
```

**Kafka events not flowing:**
```bash
kubectl logs -l app=kafka  # Check Kafka logs
./scripts/kafka-watch.sh  # Monitor topics
```

**gRPC connection refused:**
```bash
kubectl port-forward service/meridian 9091:9091  # CurrentAccount
kubectl port-forward service/meridian 9092:9092  # FinancialAccounting
```

## Next Steps (Post-Demo)

1. **Payment Stack**: PaymentInitiation → PaymentExecution → PaymentRailOperations
2. **Regulatory Compliance**: RegulatoryCompliance rules engine
3. **Lending**: ConsumerLoan with Interest BQ
4. **Multi-broker Kafka**: KRaft quorum for HA
