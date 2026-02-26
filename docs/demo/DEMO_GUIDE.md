# Meridian Demo Guide

## Overview

Demonstrates event-driven microservices with Kafka for CurrentAccount and FinancialAccounting BIAN domains.

## Architecture

```text
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
✅ **Cloud-Native**: Kubernetes, CockroachDB, Kafka 3.9.1 with KRaft (3-broker cluster)
✅ **High Availability**: Multi-broker Kafka with replication factor 2 (tolerates 1 broker failure)

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

#### 1. Create Account

```bash
grpcurl -plaintext -d '{
  "customer_reference": "CUST-001",
  "product_service_type": {"type": "STANDARD_CURRENT_ACCOUNT"},
  "account_currency": "GBP"
}' localhost:9091 meridian.current_account.v1.CurrentAccountService/InitiateCurrentAccount
```

#### 2. Execute Deposit

```bash
grpcurl -plaintext -d '{
  "current_account_facility_reference": "ACC-123",
  "amount": {"currency": "GBP", "units": 100}
}' localhost:9091 meridian.current_account.v1.CurrentAccountService/ExecuteDeposit
```

#### 3. Check Ledger

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

## Technical Deep Dives

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
kubectl get pods -l app=kafka        # Verify all 3 brokers are running
kubectl logs kafka-0                 # Check broker 0 logs
kubectl logs kafka-1                 # Check broker 1 logs
kubectl logs kafka-2                 # Check broker 2 logs
./scripts/kafka-watch.sh             # Monitor topics
```

**gRPC connection refused:**

```bash
kubectl port-forward service/meridian 9091:9091  # CurrentAccount
kubectl port-forward service/meridian 9092:9092  # FinancialAccounting
```

## Testing Kafka Failover

The 3-broker Kafka cluster enables testing of high availability scenarios:

```bash

# View partition distribution across brokers

kubectl exec kafka-0 -- kafka-topics --describe --topic current-account.deposits --bootstrap-server localhost:9092

# Kill a broker to test failover

kubectl delete pod kafka-1

# Verify leadership transfers automatically

kubectl exec kafka-0 -- kafka-topics --describe --topic current-account.deposits --bootstrap-server localhost:9092

# Produce/consume messages to verify data persists

kubectl exec kafka-0 -- kafka-console-producer --topic current-account.deposits --bootstrap-server localhost:9092
kubectl exec kafka-0 -- kafka-console-consumer --topic current-account.deposits --from-beginning --bootstrap-server
localhost:9092
```

**Expected Behaviour:**

- Topics have replicas on 2 brokers (replication factor 2)
- Killing 1 broker triggers partition leader election
- Messages remain available (no data loss)
- System continues processing events

## Next Steps

1. **Payment Stack**: PaymentInitiation → PaymentExecution → PaymentRailOperations
2. **Regulatory Compliance**: RegulatoryCompliance rules engine
3. **Lending**: ConsumerLoan with Interest BQ
4. **OpenTelemetry**: Distributed tracing with Jaeger
