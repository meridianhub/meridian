---
name: saga-failure-recovery
description: Procedures for detecting and recovering from saga transaction failures in distributed service orchestration
triggers:

  - Saga compensation failures detected in logs
  - Inconsistent state between CurrentAccount, PositionKeeping, and FinancialAccounting
  - Failed deposits with partial external service updates
  - Correlation ID tracing shows incomplete saga execution

instructions: |
  Use correlation IDs to trace saga execution across services. Verify transaction
  state in all three systems. Execute manual compensation if needed. Follow
  escalation procedures for unrecoverable inconsistencies.
---

# Saga Failure Recovery Runbook

**When to use this runbook**: Saga transaction failures detected in CurrentAccount service orchestration with
PositionKeeping and FinancialAccounting services.

> **⚠️ Note**: This runbook is specific to the saga orchestration pattern implemented in
CurrentAccount→PositionKeeping→FinancialAccounting transaction flow (PR #86).

## Quick Reference

| Failure Type | Impact | Recovery Time | Escalation |
| ------------ | ------ | ------------- | ---------- |
| **Step 1 (Position Log) failure** | No state change | Automatic | None - retry succeeds |
| **Step 2 (Ledger) failure** | Position log compensated | Automatic | None - retry succeeds |
| **Step 3 (DB Save) failure** | All external services compensated | Automatic | None - retry succeeds |
| **Compensation failure** | Inconsistent external state | Manual | Engineering team |
| **Network partition** | Unknown saga state | Manual investigation | On-call engineer |

## Saga Transaction Flow

The CurrentAccount `ExecuteDeposit` operation uses a 3-step saga:

```text
User Request
    ↓
[Step 1: Log Position] → PositionKeeping Service
    ↓
[Step 2: Post Ledger] → FinancialAccounting Service
    ↓
[Step 3: Save Account] → CurrentAccount Database
    ↓
Success Response
```

**Compensation Order** (LIFO):

- Step 2 fails → Compensate Step 1 (reverse position entry)
- Step 3 fails → Compensate Step 2 (reverse ledger), then Step 1

## Detection

### 1. Monitoring Alerts

**Prometheus Metrics** (when implemented):

```promql

# High saga failure rate

rate(saga_failures_total{service="current-account"}[5m]) > 0.1

# Compensation failures

saga_compensation_failures_total{service="current-account"} > 0
```

**Log Queries** (Loki/CloudWatch):

```logql
{service="current-account"} |= "saga failed"
{service="current-account"} |= "compensation failed"
```

### 2. User Reports

Symptoms:

- "Deposit failed but I was charged"
- "Transaction shows in one system but not another"
- "My balance doesn't match my transaction history"

### 3. Log Inspection

**Identify failed saga:**

```bash

# Search for failed sagas by correlation ID

kubectl logs -n production deployment/current-account | grep "saga failed"

# Example output:

# level=ERROR msg="deposit saga failed" account_id=ACC-123 transaction_id=TXN-456

#   failed_step=post_ledger completed_steps=1 compensated_steps=1

#   correlation_id=abc-def-ghi error="step post_ledger failed: timeout"

```

**Key fields to extract:**

- `correlation_id`: Traces the request across all services
- `account_id`: Affected account
- `transaction_id`: Failed transaction
- `failed_step`: Which step failed (log_position, post_ledger, save_account)
- `completed_steps`: How many steps completed before failure
- `compensated_steps`: How many steps were compensated

## Investigation

### Step 1: Verify Current State

#### Check CurrentAccount Database

```sql
-- Verify account balance hasn't changed
SELECT account_id, balance_cents, version, updated_at
FROM account
WHERE account_id = 'ACC-123';

-- Check recent transactions
SELECT * FROM transaction_history
WHERE account_id = 'ACC-123'
ORDER BY created_at DESC LIMIT 10;
```

#### Check PositionKeeping Service

```bash

# Query position logs for the account

curl -H "Authorization: Bearer $TOKEN" \
  "https://api.positionkeeping/v1/logs?account_id=ACC-123&limit=10"

# Look for the transaction_id from the failed saga

# Check if there's a compensating DEBIT entry

```

#### Check FinancialAccounting Service

```bash

# Query ledger postings

curl -H "Authorization: Bearer $TOKEN" \
  "https://api.financialaccounting/v1/postings?account_id=ACC-123&limit=10"

# Look for the transaction_id

# Check if there's a compensating DEBIT posting

```

### Step 2: Trace Full Saga Execution

Using the `correlation_id` from logs:

```bash

# PositionKeeping logs

kubectl logs -n production deployment/positionkeeping | grep "correlation_id=abc-def-ghi"

# FinancialAccounting logs

kubectl logs -n production deployment/financialaccounting | grep "correlation_id=abc-def-ghi"

# CurrentAccount logs

kubectl logs -n production deployment/current-account | grep "correlation_id=abc-def-ghi"
```

**What to look for:**

- Did the request reach each service?
- What errors occurred?
- Were compensation calls made?
- Did compensation succeed?

## Recovery Procedures

### Scenario 1: Step 1 (Position Log) Failure - No Recovery Needed ✅

**Symptoms:**

```text
failed_step=log_position completed_steps=0 compensated_steps=0
```

**State:**

- ✅ CurrentAccount: Balance unchanged
- ✅ PositionKeeping: No entries
- ✅ FinancialAccounting: No postings

**Action:** None needed. User can retry the deposit.

---

### Scenario 2: Step 2 (Ledger) Failure - Automatic Compensation ✅

**Symptoms:**

```text
failed_step=post_ledger completed_steps=1 compensated_steps=1
```

**Expected State:**

- ✅ CurrentAccount: Balance unchanged
- ✅ PositionKeeping: CREDIT entry + compensating DEBIT entry (net zero)
- ✅ FinancialAccounting: No postings

**Verification:**

```bash

# Check PositionKeeping has both entries

# Should see two entries with same transaction ID:

#   1. CREDIT (original)

#   2. DEBIT (compensation with COMP-TXN-456 ID)

```

**Action:** Verify compensation occurred. If compensation succeeded, no manual action needed.

---

### Scenario 3: Step 3 (DB Save) Failure - Automatic Compensation ✅

**Symptoms:**

```text
failed_step=save_account completed_steps=2 compensated_steps=2
```

**Expected State:**

- ✅ CurrentAccount: Balance unchanged
- ✅ PositionKeeping: CREDIT + DEBIT entries (net zero)
- ✅ FinancialAccounting: CREDIT + DEBIT postings (net zero)

**Verification:**

```bash

# Verify both external services have compensating entries

# All entries should have matching CREDIT/DEBIT pairs

```

**Action:** None if compensation succeeded. User can retry deposit.

---

### Scenario 4: Compensation Failure - Manual Recovery Required ⚠️

**Symptoms:**

```text
failed_step=post_ledger completed_steps=1 compensated_steps=0
compensation failed: timeout
```

**Inconsistent State:**

- ❌ CurrentAccount: Balance unchanged (correct)
- ❌ PositionKeeping: CREDIT entry (orphaned, needs reversal)
- ✅ FinancialAccounting: No postings

**Manual Recovery Steps:**

1. **Create manual compensating position entry:**

   ```bash

   # Prepare compensation request

   cat > compensation.json <<EOF
   {
     "log_id": "ACC-123",
     "entry": {
       "entry_id": "MANUAL-COMP-$(uuidgen)",
       "transaction_id": "COMP-TXN-456",
       "account_id": "ACC-123",
       "amount": {"currency": "GBP", "amount_cents": 10050},
       "direction": "POSTING_DIRECTION_DEBIT",
       "timestamp": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
       "description": "Manual compensation for failed saga TXN-456"
     }
   }
   EOF

   # Submit compensation

   curl -X POST \
     -H "Authorization: Bearer $TOKEN" \
     -H "Content-Type: application/json" \
     -d @compensation.json \
     "https://api.positionkeeping/v1/logs/ACC-123/entries"

   ```

1. **Verify net balance is zero:**

   ```bash

   # Query all entries for the transaction

   curl "https://api.positionkeeping/v1/logs/ACC-123/entries?transaction_id=TXN-456"

   # Verify: sum(CREDIT) - sum(DEBIT) = 0

   ```

1. **Document in incident log:**
   - Correlation ID
   - Transaction ID
   - Compensation action taken
   - Verification results

1. **Notify customer support** if user was impacted

---

### Scenario 5: Network Partition During Saga ⚠️

**Symptoms:**

- Saga logs show timeout errors
- Can't determine if external services received requests
- `completed_steps` and `compensated_steps` don't match expected values

**Investigation:**

1. **Check idempotency keys in external services:**

   ```bash

   # PositionKeeping

   curl "https://api.positionkeeping/v1/logs/ACC-123/entries?idempotency_key=TXN-456"

   # FinancialAccounting

   curl "https://api.financialaccounting/v1/postings?idempotency_key=TXN-456"

   ```

1. **Determine actual state:**
   - If external services show entries: Execute compensation manually
   - If no entries exist: Safe to retry

1. **Recovery depends on findings** - follow appropriate scenario above

---

## Escalation Procedures

### When to Escalate

Escalate to on-call engineer if:

- Compensation failures persist after 3 retry attempts
- Data inconsistency affects multiple accounts
- User funds are at risk
- Unable to determine saga state after investigation

### Escalation Information to Provide

```text

Saga Failure Escalation Report
==============================

Correlation ID: abc-def-ghi
Account ID: ACC-123
Transaction ID: TXN-456
Failed Step: post_ledger
Completed Steps: 1
Compensated Steps: 0
Error Message: connection timeout after 30s

Investigation Summary:

- CurrentAccount balance: unchanged (correct)
- PositionKeeping: CREDIT entry exists (needs compensation)
- FinancialAccounting: no entries

Manual Recovery Attempted:

- [ ] Manual compensation request submitted
- [ ] Verification completed
- [ ] Customer notified

Escalating because: Unable to reach PositionKeeping service to complete compensation

```

## Prevention

### 1. Monitoring

Set up alerts for:

```yaml

# Prometheus alert rules

- alert: HighSagaFailureRate

  expr: rate(saga_failures_total[5m]) > 0.05
  for: 5m
  annotations:
    summary: "High saga failure rate detected"

- alert: CompensationFailure

  expr: saga_compensation_failures_total > 0
  for: 1m
  annotations:
    summary: "Saga compensation failed - manual intervention required"
```

### 2. Circuit Breaker Tuning

Monitor circuit breaker states:

```bash

# Check if circuit breakers are frequently opening

kubectl logs deployment/current-account | grep "circuit breaker opened"
```

If circuit breakers open frequently:

- Increase timeout values
- Review downstream service health
- Consider bulkhead pattern for better isolation

### 3. Retry Configuration

Review retry settings in `ResilientClient`:

- Exponential backoff parameters
- Maximum retry attempts
- Retry-after header handling

### 4. Regular Reconciliation

Schedule periodic reconciliation jobs:

```bash

# Compare CurrentAccount balance with PositionKeeping transaction sum

# Run daily at 2 AM

0 2 * * * /usr/local/bin/reconciliation-job --mode=verify
```

## Testing Compensation

To test compensation logic in staging:

```bash

# 1. Trigger controlled failure

curl -X POST "https://staging-api/v1/accounts/ACC-TEST/deposits" \
  -H "Content-Type: application/json" \
  -H "X-Chaos-Inject: fail-ledger-posting" \
  -d '{"amount": {"amount": 100.00, "currency": "GBP"}}'

# 2. Verify compensation occurred

kubectl logs deployment/current-account | grep "compensation completed"

# 3. Verify state consistency

# Run verification queries from "Step 1: Verify Current State"

```

## Post-Incident Actions

After resolving a saga failure:

1. **Document the incident:**
   - Root cause
   - Recovery steps taken
   - Time to resolution
   - Customer impact

1. **Review alerting:**
   - Did alerts fire appropriately?
   - Was response time adequate?
   - Do we need additional alerts?

1. **Code improvements:**
   - Are there patterns in failures?
   - Should retry logic be adjusted?
   - Do timeout values need tuning?

1. **Update this runbook** with lessons learned

## References

- [ADR-0005: Adapter Pattern for Layer Translation](../adr/0005-adapter-pattern-layer-translation.md)
- [CurrentAccount Service Implementation](../../services/current-account/service/)
- [Saga Orchestration Tests](../../services/current-account/service/grpc_service_integration_test.go)
- [PR #86: Service Integration](https://github.com/meridianhub/meridian/pull/86)

## Contact Information

| Role | Contact | Escalation Path |
|------|---------|----------------|
| On-call Engineer | #on-call-alerts | Immediate |
| Engineering Lead | #engineering | < 15 minutes |
| Platform Team | #platform-support | < 1 hour |

## Changelog

| Date | Change | Author |
|------|--------|--------|
| 2025-11-05 | Initial version - saga failure recovery procedures | Claude + Ben |
