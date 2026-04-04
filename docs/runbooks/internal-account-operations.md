---
name: internal-account-operations
description: Operational procedures for Internal Account service including account management, troubleshooting, and disaster recovery
triggers:
  - Troubleshooting Internal Account issues
  - Managing account lifecycle operations
  - Investigating balance query problems
  - Handling correspondent bank account setup
  - Creating or suspending internal accounts
  - Position Keeping integration failures
instructions: |
  Use this runbook for Internal Account service operations.
  Port: 50057 (gRPC). Database: internal_account.
  Balance queries route to Position Keeping (50053).
  Account types: CLEARING, NOSTRO, VOSTRO, HOLDING, SUSPENSE, REVENUE, EXPENSE, INVENTORY.
  Status transitions: ACTIVE <-> SUSPENDED -> CLOSED (CLOSED is permanent).
---

# Internal Account Operations Runbook

**When to use this runbook**: Managing internal accounts, troubleshooting balance queries,
handling account lifecycle operations, or recovering from service failures.

> **Note**: This service manages non-customer-facing accounts (clearing, nostro/vostro, holding,
> suspense, revenue, expense, inventory). Customer accounts are managed by CurrentAccount service.

## Service Overview

| Property | Value |
|----------|-------|
| **Service Name** | internal-account |
| **gRPC Port** | 50057 |
| **HTTP/REST Port** | 8057 (via gRPC gateway) |
| **Database** | internal_account (schema-per-tenant) |
| **Namespace** | production / staging |
| **Deployment** | internal-account |

### Dependencies

| Service | Port | Purpose |
|---------|------|---------|
| Position Keeping | 50053 | Balance queries (source of truth) |
| Reference Data | 50052 | Instrument validation |
| CockroachDB | 26257 | Persistent storage |

### Account Types

| Type | Purpose | Normal Balance |
|------|---------|----------------|
| **CLEARING** | Settlement and clearing operations | Varies |
| **NOSTRO** | Our account at another bank | Debit (Asset) |
| **VOSTRO** | Another bank's account at us | Credit (Liability) |
| **HOLDING** | Temporary holding of funds | Varies |
| **SUSPENSE** | Unidentified/pending transactions | Varies |
| **REVENUE** | Income tracking (fees, interest) | Credit |
| **EXPENSE** | Cost tracking | Debit |
| **INVENTORY** | Non-cash assets (energy, compute, carbon) | Debit (Asset) |

### Status Lifecycle

```text
    ┌──────────┐
    │  ACTIVE  │◄──────────────────┐
    └────┬─────┘                   │
         │ SUSPEND                 │ ACTIVATE
         ▼                         │
    ┌──────────┐                   │
    │ SUSPENDED├───────────────────┘
    └────┬─────┘
         │ CLOSE (irreversible)
         ▼
    ┌──────────┐
    │  CLOSED  │ (permanent, no reactivation)
    └──────────┘
```

---

## Common Operations

### Create New Internal Account

**Use case**: Setting up a new clearing account, revenue account, or correspondent bank account for a tenant.

```bash
# Using grpcurl
grpcurl -plaintext \
  -d '{
    "account_code": "CLR-GBP-001",
    "name": "GBP Deposit Clearing Account",
    "account_type": "INTERNAL_ACCOUNT_TYPE_CLEARING",
    "instrument_code": "GBP",
    "description": "Primary clearing account for GBP deposits"
  }' \
  internal-account.production.svc.cluster.local:50057 \
  meridian.internal_account.v1.InternalAccountService/InitiateInternalAccount
```

**Expected response:**

```json
{
  "account_id": "2gKVPLwqhSJPgQKX4L8tH3Rymkv",
  "facility": {
    "account_id": "2gKVPLwqhSJPgQKX4L8tH3Rymkv",
    "account_code": "CLR-GBP-001",
    "name": "GBP Deposit Clearing Account",
    "account_type": "INTERNAL_ACCOUNT_TYPE_CLEARING",
    "account_status": "INTERNAL_ACCOUNT_STATUS_ACTIVE",
    "instrument_code": "GBP",
    "version": 1
  }
}
```

### Query Account Balance

**Important**: Balance is retrieved from Position Keeping service. Internal Account does not store balance locally.

```bash
# Query balance via Internal Account (routes to Position Keeping)
grpcurl -plaintext \
  -d '{"account_id": "2gKVPLwqhSJPgQKX4L8tH3Rymkv"}' \
  internal-account.production.svc.cluster.local:50057 \
  meridian.internal_account.v1.InternalAccountService/GetBalance

# Direct query to Position Keeping (if debugging integration)
grpcurl -plaintext \
  -d '{"account_id": "2gKVPLwqhSJPgQKX4L8tH3Rymkv"}' \
  position-keeping.production.svc.cluster.local:50053 \
  meridian.position_keeping.v1.PositionKeepingService/GetAccountBalances
```

### Suspend an Account

**Use case**: Temporarily disable an account due to suspected fraud, reconciliation issues, or compliance review.

```bash
grpcurl -plaintext \
  -d '{
    "account_id": "2gKVPLwqhSJPgQKX4L8tH3Rymkv",
    "control_action": "CONTROL_ACTION_SUSPEND",
    "reason": "Compliance review - pending investigation of unusual activity pattern detected on 2026-01-15"
  }' \
  internal-account.production.svc.cluster.local:50057 \
  meridian.internal_account.v1.InternalAccountService/ControlInternalAccount
```

### Reactivate a Suspended Account

```bash
grpcurl -plaintext \
  -d '{
    "account_id": "2gKVPLwqhSJPgQKX4L8tH3Rymkv",
    "control_action": "CONTROL_ACTION_ACTIVATE",
    "reason": "Compliance review completed - no issues found. Approved by compliance officer Jane Smith"
  }' \
  internal-account.production.svc.cluster.local:50057 \
  meridian.internal_account.v1.InternalAccountService/ControlInternalAccount
```

### Close an Account (Permanent)

> **Warning**: Closing an account is irreversible. Ensure the account has zero balance and no pending transactions.

```bash
# 1. Verify zero balance first
grpcurl -plaintext \
  -d '{"account_id": "2gKVPLwqhSJPgQKX4L8tH3Rymkv"}' \
  internal-account.production.svc.cluster.local:50057 \
  meridian.internal_account.v1.InternalAccountService/GetBalance

# 2. Close the account (only if balance is zero)
grpcurl -plaintext \
  -d '{
    "account_id": "2gKVPLwqhSJPgQKX4L8tH3Rymkv",
    "control_action": "CONTROL_ACTION_CLOSE",
    "reason": "Account no longer required - replaced by CLR-GBP-002. Authorized by operations manager"
  }' \
  internal-account.production.svc.cluster.local:50057 \
  meridian.internal_account.v1.InternalAccountService/ControlInternalAccount
```

### Add Correspondent Bank Account (NOSTRO/VOSTRO)

**NOSTRO account** (our money at another bank):

```bash
grpcurl -plaintext \
  -d '{
    "account_code": "NOSTRO-USD-HSBC",
    "name": "USD Nostro Account at HSBC London",
    "account_type": "INTERNAL_ACCOUNT_TYPE_NOSTRO",
    "instrument_code": "USD",
    "correspondent_details": {
      "bank_id": "HSBCGB2L",
      "bank_name": "HSBC Bank plc",
      "external_account_ref": "GB82HSBC40121234567890",
      "swift_code": "HSBCGB2L",
      "correspondent_type": "CORRESPONDENT_TYPE_NOSTRO"
    },
    "description": "Primary USD nostro account for international settlements"
  }' \
  internal-account.production.svc.cluster.local:50057 \
  meridian.internal_account.v1.InternalAccountService/InitiateInternalAccount
```

**VOSTRO account** (their money at our bank):

```bash
grpcurl -plaintext \
  -d '{
    "account_code": "VOSTRO-EUR-DEUTSCHE",
    "name": "Deutsche Bank EUR Vostro",
    "account_type": "INTERNAL_ACCOUNT_TYPE_VOSTRO",
    "instrument_code": "EUR",
    "correspondent_details": {
      "bank_id": "DEUTDEFF",
      "bank_name": "Deutsche Bank AG",
      "external_account_ref": "CORR-2024-001",
      "swift_code": "DEUTDEFF",
      "correspondent_type": "CORRESPONDENT_TYPE_VOSTRO"
    },
    "description": "Vostro account for Deutsche Bank EUR transactions"
  }' \
  internal-account.production.svc.cluster.local:50057 \
  meridian.internal_account.v1.InternalAccountService/InitiateInternalAccount
```

### List Accounts by Type

```bash
# List all clearing accounts
grpcurl -plaintext \
  -d '{"account_type_filter": "INTERNAL_ACCOUNT_TYPE_CLEARING"}' \
  internal-account.production.svc.cluster.local:50057 \
  meridian.internal_account.v1.InternalAccountService/ListInternalAccounts

# List all active NOSTRO accounts
grpcurl -plaintext \
  -d '{
    "account_type_filter": "INTERNAL_ACCOUNT_TYPE_NOSTRO",
    "status_filter": "INTERNAL_ACCOUNT_STATUS_ACTIVE"
  }' \
  internal-account.production.svc.cluster.local:50057 \
  meridian.internal_account.v1.InternalAccountService/ListInternalAccounts

# List accounts for specific instrument
grpcurl -plaintext \
  -d '{"instrument_code_filter": "GBP"}' \
  internal-account.production.svc.cluster.local:50057 \
  meridian.internal_account.v1.InternalAccountService/ListInternalAccounts
```

---

## Monitoring and Alerts

### Key Metrics

| Metric | Description | Warning Threshold | Critical Threshold |
|--------|-------------|-------------------|-------------------|
| `iba_account_creation_total` | Total accounts created | - | - |
| `iba_account_creation_errors_total` | Failed account creations | > 5/min | > 20/min |
| `iba_balance_query_duration_seconds` | Balance query latency | p99 > 100ms | p99 > 500ms |
| `iba_balance_query_errors_total` | Failed balance queries | > 10/min | > 50/min |
| `iba_control_action_total` | Lifecycle transitions | - | - |
| `iba_position_keeping_errors_total` | Position Keeping failures | > 5/min | > 20/min |

### Prometheus Queries

**Balance query latency:**

```promql
# P99 balance query latency
histogram_quantile(0.99,
  sum(rate(iba_balance_query_duration_seconds_bucket{service="internal-account"}[5m])) by (le)
)

# Balance query error rate
sum(rate(iba_balance_query_errors_total{service="internal-account"}[5m]))
```

**Account operations:**

```promql
# Account creation rate by type
sum(rate(iba_account_creation_total{service="internal-account"}[1h])) by (account_type)

# Suspended accounts count
iba_accounts_by_status{status="SUSPENDED"}
```

**Position Keeping integration:**

```promql
# Position Keeping call success rate
1 - (
  sum(rate(iba_position_keeping_errors_total[5m])) /
  sum(rate(iba_balance_query_total[5m]))
)

# Circuit breaker status (1 = open, 0 = closed)
iba_circuit_breaker_state{target="position-keeping"}
```

### Alert Definitions

**High balance query latency:**

```yaml
- alert: InternalAccountBalanceQuerySlow
  expr: histogram_quantile(0.99, sum(rate(iba_balance_query_duration_seconds_bucket[5m])) by (le)) > 0.5
  for: 5m
  labels:
    severity: warning
  annotations:
    summary: "Internal Account balance queries are slow"
    description: "P99 balance query latency is {{ $value }}s (threshold: 500ms)"
    runbook: "docs/runbooks/internal-account-operations.md#balance-queries-slow"
```

**Position Keeping integration failure:**

```yaml
- alert: InternalAccountPositionKeepingDown
  expr: sum(rate(iba_position_keeping_errors_total[5m])) > 20
  for: 2m
  labels:
    severity: critical
  annotations:
    summary: "Position Keeping integration failing"
    description: "High error rate communicating with Position Keeping: {{ $value }} errors/min"
    runbook: "docs/runbooks/internal-account-operations.md#position-keeping-integration-failures"
```

### Grafana Dashboards

> **Setup Required**: Configure these dashboard links for your environment

- **Service Dashboard**: [Grafana - Internal Account Overview]
- **Position Keeping Integration**: [Grafana - Cross-Service Dependencies]
- **Account Operations**: [Grafana - Internal Account Operations]

---

## Troubleshooting

### Service Won't Start

**Symptoms:** Pod in CrashLoopBackOff, logs show connection errors

**Check database connectivity:**

```bash
# Check pod status
kubectl get pods -n production -l app=internal-account

# Check logs for startup errors
kubectl logs -n production -l app=internal-account --tail=100

# Verify database connectivity
kubectl exec -it <pod-name> -n production -- nc -zv cockroachdb 26257

# Check database migration status
kubectl exec -it <pod-name> -n production -- \
  cockroach sql --execute="SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 5;"
```

**Check configuration:**

```bash
# Verify ConfigMap
kubectl describe configmap internal-account-config -n production

# Check secrets are mounted
kubectl describe pod <pod-name> -n production | grep -A10 "Mounts:"

# Verify environment variables
kubectl exec -it <pod-name> -n production -- env | grep -E "(DB_|GRPC_|POSITION_)"
```

### Balance Queries Slow

**Symptoms:** GetBalance RPC latency > 100ms, timeouts

**1. Check Position Keeping health:**

```bash
# Direct health check to Position Keeping
kubectl exec -it <iba-pod> -n production -- \
  grpcurl -plaintext position-keeping:50053 grpc.health.v1.Health/Check

# Check Position Keeping metrics
kubectl exec -it <pk-pod> -n production -- \
  curl -s localhost:9090/metrics | grep position_keeping
```

**2. Check network latency:**

```bash
# Measure RTT to Position Keeping
kubectl exec -it <iba-pod> -n production -- \
  time grpcurl -plaintext -d '{"account_id": "test"}' \
    position-keeping:50053 \
    meridian.position_keeping.v1.PositionKeepingService/GetAccountBalances
```

**3. Check circuit breaker status:**

```bash
# Check if circuit breaker is open
kubectl exec -it <iba-pod> -n production -- \
  curl -s localhost:9090/metrics | grep circuit_breaker
```

**4. Verify caching is enabled:**

```bash
# Check cache hit rate
kubectl exec -it <iba-pod> -n production -- \
  curl -s localhost:9090/metrics | grep iba_cache
```

### Position Keeping Integration Failures

**Symptoms:** GetBalance returns errors, circuit breaker open

**1. Check circuit breaker:**

```bash
# View circuit breaker state
kubectl logs -n production -l app=internal-account --tail=200 | grep -i "circuit"

# If open, wait for half-open state (typically 30s) or restart pod
kubectl delete pod <iba-pod> -n production
```

**2. Verify Position Keeping is healthy:**

```bash
# Check Position Keeping pods
kubectl get pods -n production -l app=position-keeping

# Check Position Keeping logs
kubectl logs -n production -l app=position-keeping --tail=100

# Test direct connectivity
kubectl exec -it <iba-pod> -n production -- \
  grpcurl -plaintext position-keeping:50053 \
  grpc.health.v1.Health/Check
```

**3. Check network policies:**

```bash
# Verify NetworkPolicy allows traffic
kubectl describe networkpolicy internal-account -n production | grep -A10 "position-keeping"
```

**4. Check DNS resolution:**

```bash
kubectl exec -it <iba-pod> -n production -- nslookup position-keeping.production.svc.cluster.local
```

### Schema Migration Failures

**Symptoms:** Service starts but database operations fail, migration errors in logs

**1. Check migration status:**

```bash
# Connect to database
kubectl exec -it cockroachdb-0 -n production -- cockroach sql

# Check migration history (replace tenant_schema with actual schema)
USE tenant_schema;
SELECT * FROM schema_migrations ORDER BY version DESC LIMIT 10;

# Check for dirty migrations
SELECT * FROM schema_migrations WHERE dirty = true;
```

**2. Fix dirty migration:**

```bash
# Mark migration as clean (use with caution)
UPDATE schema_migrations SET dirty = false WHERE version = <version>;

# Or rollback and retry
DELETE FROM schema_migrations WHERE version = <version>;
```

**3. Manual migration (emergency):**

```bash
# Apply migration manually
kubectl exec -it cockroachdb-0 -n production -- cockroach sql < /path/to/migration.sql
```

### Duplicate Account Errors

**Symptoms:** InitiateInternalAccount returns ALREADY_EXISTS error

**1. Check if account exists:**

```bash
# Search by account_code
grpcurl -plaintext \
  -d '{}' \
  internal-account.production.svc.cluster.local:50057 \
  meridian.internal_account.v1.InternalAccountService/ListInternalAccounts \
  | jq '.facilities[] | select(.account_code == "CLR-GBP-001")'
```

**2. Check database directly:**

```sql
-- Connect to tenant schema
USE tenant_schema;

-- Find account by code
SELECT account_id, account_code, name, status
FROM internal_account
WHERE account_code = 'CLR-GBP-001';

-- Check for closed accounts with same code
SELECT account_id, account_code, status
FROM internal_account
WHERE account_code LIKE 'CLR-GBP%';
```

**3. Resolution options:**

- If account is CLOSED, create with different account_code
- If account is SUSPENDED, reactivate it instead of creating new
- If duplicate was created in error, close the duplicate

### Correspondent Bank Validation Errors

**Symptoms:** NOSTRO/VOSTRO account creation fails with validation error

**1. Check validation rules:**

- `bank_id`: Required, 1-100 characters, alphanumeric with `_` and `-`
- `bank_name`: Required, 1-255 characters
- `external_account_ref`: Required, 1-255 characters
- `swift_code`: Optional, must be BIC8 (8 chars) or BIC11 (11 chars) format
- `correspondent_type`: Required, must match account_type (NOSTRO account needs NOSTRO type)

**2. Common issues:**

```bash
# Invalid SWIFT code format (must be uppercase letters)
# Wrong: "hsbcgb2l" -> Correct: "HSBCGB2L"

# Missing correspondent_type
# NOSTRO account must have correspondent_type = CORRESPONDENT_TYPE_NOSTRO

# Mismatch between account_type and correspondent_type
# INTERNAL_ACCOUNT_TYPE_NOSTRO requires CORRESPONDENT_TYPE_NOSTRO
```

---

## Disaster Recovery

### Database Restore Procedures

**1. Stop the service:**

```bash
kubectl scale deployment internal-account -n production --replicas=0
```

**2. Restore from backup:**

```bash
# List available backups
cockroach sql --execute="SHOW BACKUPS IN 's3://meridian-backups/production/';"

# Restore specific tenant schema
cockroach sql --execute="
  RESTORE DATABASE tenant_schema
  FROM 's3://meridian-backups/production/2026-01-15-00-00/'
  WITH into_db = 'tenant_schema_restored';
"

# Verify restore
cockroach sql --execute="
  SELECT COUNT(*) FROM tenant_schema_restored.internal_account;
"
```

**3. Swap schemas (if verified):**

```sql
-- Rename current schema
ALTER DATABASE tenant_schema RENAME TO tenant_schema_corrupted;

-- Rename restored schema
ALTER DATABASE tenant_schema_restored RENAME TO tenant_schema;
```

**4. Restart service:**

```bash
kubectl scale deployment internal-account -n production --replicas=3
```

### Replay from Event Log (Kafka)

If account state becomes inconsistent, replay events from Kafka:

**1. Identify affected accounts:**

```sql
SELECT account_id, version, updated_at
FROM internal_account
WHERE updated_at > '2026-01-15 00:00:00';
```

**2. Find events in Kafka:**

```bash
# List events for specific account
kafka-console-consumer \
  --bootstrap-server kafka:9092 \
  --topic internal-account-events \
  --from-beginning \
  | jq 'select(.account_id == "2gKVPLwqhSJPgQKX4L8tH3Rymkv")'
```

**3. Replay events (requires replay tool):**

```bash
# Use event replay tool
./event-replay \
  --source kafka:9092 \
  --topic internal-account-events \
  --from-timestamp "2026-01-15T00:00:00Z" \
  --target internal-account:50057
```

### Tenant Schema Recovery

**1. Identify affected tenant:**

```bash
# Check which tenant schemas exist
cockroach sql --execute="SHOW DATABASES;" | grep -E "^org_|^tenant_"
```

**2. Restore tenant schema from backup:**

```bash
cockroach sql --execute="
  RESTORE DATABASE org_acme_bank
  FROM 's3://meridian-backups/production/2026-01-15-00-00/internal_account/'
  AS OF SYSTEM TIME '2026-01-14 23:00:00';
"
```

**3. Verify account data:**

```sql
USE org_acme_bank;
SELECT COUNT(*) as total_accounts,
       COUNT(CASE WHEN status = 'ACTIVE' THEN 1 END) as active,
       COUNT(CASE WHEN status = 'SUSPENDED' THEN 1 END) as suspended,
       COUNT(CASE WHEN status = 'CLOSED' THEN 1 END) as closed
FROM internal_account;
```

### Rollback Procedures

**Rollback deployment:**

```bash
# List deployment history
kubectl rollout history deployment/internal-account -n production

# Rollback to previous version
kubectl rollout undo deployment/internal-account -n production

# Rollback to specific revision
kubectl rollout undo deployment/internal-account -n production --to-revision=5

# Verify rollback
kubectl rollout status deployment/internal-account -n production
```

**Rollback database migration:**

```bash
# Check current migration version
cockroach sql --execute="
  SELECT version, dirty FROM schema_migrations ORDER BY version DESC LIMIT 1;
"

# Rollback requires manual DDL (migrations are forward-only)
# Apply rollback script if available
cockroach sql < migrations/rollback/20260115000001_rollback.sql
```

---

## Quick Reference Commands

### kubectl Shortcuts

```bash
# Get Internal Account pods
alias kiba='kubectl get pods -n production -l app=internal-account'

# Logs for IBA
alias kiblog='kubectl logs -n production -l app=internal-account --tail=100'

# Exec into IBA pod (use pod name from kiba)
alias kibexec='kubectl exec -it -n production --'
```

### Database Queries

```sql
-- Count accounts by type
SELECT account_type, COUNT(*)
FROM internal_account
GROUP BY account_type;

-- Find recently modified accounts
SELECT account_id, account_code, status, updated_at
FROM internal_account
ORDER BY updated_at DESC
LIMIT 20;

-- Audit trail for specific account
SELECT from_status, to_status, reason, changed_at
FROM internal_account_status_history
WHERE account_id = '2gKVPLwqhSJPgQKX4L8tH3Rymkv'
ORDER BY changed_at DESC;

-- Find accounts with correspondent details
SELECT account_id, account_code, account_type,
       correspondent_bank_id, correspondent_bank_name
FROM internal_account
WHERE account_type IN ('NOSTRO', 'VOSTRO');
```

### gRPC Diagnostic Commands

```bash
# Health check
grpcurl -plaintext internal-account:50057 grpc.health.v1.Health/Check

# List available services
grpcurl -plaintext internal-account:50057 list

# Describe service methods
grpcurl -plaintext internal-account:50057 describe meridian.internal_account.v1.InternalAccountService

# Get account by ID
grpcurl -plaintext -d '{"account_id": "XXX"}' internal-account:50057 \
  meridian.internal_account.v1.InternalAccountService/RetrieveInternalAccount
```

### Useful One-Liners

```bash
# Find pod with highest memory usage
kubectl top pods -n production -l app=internal-account --sort-by=memory

# Watch pod status
watch -n 2 'kubectl get pods -n production -l app=internal-account'

# Stream logs from all IBA pods
stern -n production internal-account

# Check connectivity to dependencies
# Position Keeping on gRPC port
kubectl exec -it <iba-pod> -n production -- nc -zv position-keeping 50053
# CockroachDB on SQL port
kubectl exec -it <iba-pod> -n production -- nc -zv cockroachdb 26257
```

---

## Emergency Contacts

> **Production Setup Required**: Configure these with your actual contact details

| Role | Contact | Escalation |
|------|---------|------------|
| **On-Call Engineer** | [PagerDuty rotation] | First responder |
| **Platform Team** | [Slack: #platform-support] | Service issues |
| **Database Admin** | [Contact details] | Schema/migration issues |
| **Security Team** | <security@your-domain.com> | Suspected fraud/breach |

## Related Runbooks

- [Incident Response](./incident-response.md) - General incident handling
- [Disaster Recovery](./disaster-recovery.md) - Full system recovery
- [Saga Failure Recovery](./saga-failure-recovery.md) - Transaction saga failures
- [Data Model Reference](../architecture/data-model.md) - Database topology, tenant schemas, table ownership
