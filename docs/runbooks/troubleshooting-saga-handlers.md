# Troubleshooting Saga Handlers Runbook

**When to use this runbook**: Issues with Starlark service bindings, handler registration failures, or saga
execution errors related to service integration.

## Quick Reference

| Issue Type | Symptom | Quick Fix | Prevention |
|------------|---------|-----------|------------|
| **Handler Not Found** | `handler not found: service.operation` | Check registration in main.go | Verify handler exists before script deployment |
| **Invalid Parameter Type** | `expected string, got int64` | Fix Starlark script params | Use schema validation |
| **Nil Client Panic** | `panic: nil pointer dereference` | Check client initialisation order | Add nil checks in handlers |
| **Metadata Validation Failure** | `handler produced EUR but declared USD` | Fix ProducesInstruments | Follow Conservation Rule |
| **gRPC Timeout** | `context deadline exceeded` | Check service health | Increase timeout or fix service |
| **Idempotency Key Collision** | `duplicate idempotency key` | Check saga replay logic | Verify key uniqueness |

## Common Errors and Solutions

### 1. Handler Not Found

**Error Message:**

```text
level=ERROR msg="saga execution failed" error="handler not found: current_account.create_lien"
  saga_name=deposit.star step_index=2
```

**Root Cause:**

- Handler name mismatch between Starlark script and registration
- Service bindings not registered in main.go
- Typo in handler name

**Solution:**

```bash
# 1. Verify handler is registered
kubectl logs -n production deployment/payment-order | grep "registered.*handlers"

# Expected output:
# level=INFO msg="registered current-account handlers" count=4
# level=INFO msg="registered position-keeping handlers" count=3

# 2. Check handler names match script
# In script: current_account.create_lien(...)
# In registration: "current_account.create_lien"

# 3. Verify service client is initialized before registration
# In cmd/main.go:
# 1. Initialize client
# 2. Create registry
# 3. Call RegisterStarlarkHandlers
# 4. Create saga runner
```

**Prevention:**

- Run dry-run validation before deploying saga scripts
- Add integration test that verifies all script handlers are registered
- Use consistent naming convention: `{service}_{resource}.{operation}`

### 2. Invalid Parameter Type

**Error Message:**

```text
level=ERROR msg="handler parameter validation failed"
  error="parameter 'amount' validation failed: expected decimal.Decimal, got int64"
  handler=current_account.create_lien
```

**Root Cause:**

- Starlark script passes wrong type (e.g., integer instead of decimal)
- Handler expects different parameter name or type
- Missing required parameter

**Solution:**

```bash
# 1. Check Starlark script parameter types
cat services/payment-order/sagas/withdrawal.star | grep -A 5 "create_lien"

# Incorrect:
# amount=100  # ❌ Integer, should be decimal

# Correct:
# amount=decimal("100.00")  # ✅ Decimal type

# 2. Verify handler signature in starlark.go
# Required params use saga.RequireDecimalParam
# Optional params use saga.GetDecimalParam with default
```

**Parameter Type Reference:**

| Starlark Type | Go Type | Helper Function |
|---------------|---------|-----------------|
| `str` | `string` | `saga.RequireStringParam` |
| `decimal("X")` | `decimal.Decimal` | `saga.RequireDecimalParam` |
| `int` | `int64` | `saga.RequireIntParam` |
| `bool` | `bool` | `saga.RequireBoolParam` |
| `None` (optional) | default value | `saga.Get*Param` |

**Prevention:**

- Use saga.schema.json to validate Starlark scripts
- Add handler documentation with parameter types
- Write unit tests for each handler with various parameter types

### 3. Nil Client Panic

**Error Message:**

```text
level=ERROR msg="panic in handler" handler=position_keeping.initiate_log
  error="runtime error: invalid memory address or nil pointer dereference"
```

**Root Cause:**

- gRPC client not initialized before RegisterStarlarkHandlers call
- Client cleanup called before saga execution
- Race condition in client initialisation

**Solution:**

```bash
# 1. Check service initialisation order in main.go
# CORRECT ORDER:
# a) Initialize all gRPC clients
# b) Create handler registry
# c) Register handlers (passing initialized clients)
# d) Create saga runner
# e) Start HTTP server

# 2. Verify client cleanup deferred properly
# WRONG:
# client, cleanup, _ := New(...)
# cleanup()  # ❌ Called too early
# RegisterStarlarkHandlers(registry, client)

# RIGHT:
# client, cleanup, _ := New(...)
# defer cleanup()  # ✅ Deferred until main() exits
# RegisterStarlarkHandlers(registry, client)

# 3. Add nil check in handler (defensive)
func createLienHandler(client *Client) saga.Handler {
    if client == nil {
        return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
            return nil, errors.New("client not initialized")
        }
    }
    return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
        // Normal handler logic
    }
}
```

**Prevention:**

- Use integration test that calls handlers with real clients
- Add startup health check that verifies all clients connected
- Log successful handler registration at INFO level

### 4. Metadata Validation Failure (Conservation Rule Violation)

**Error Message:**

```text
level=ERROR msg="handler metadata validation failed"
  error="handler current_account.create_lien produced instrument 'EUR' but only declared ['USD', 'GBP']"
  handler=current_account.create_lien
```

**Root Cause:**

- Handler's `ProducesInstruments` metadata doesn't match actual implementation
- Handler creates position with undeclared currency
- Multi-currency handler missing currency in metadata

**Solution:**

```bash
# 1. Check handler metadata declaration
cat services/current-account/client/starlark.go | grep -A 10 "create_lien"

# Check ProducesInstruments field:
ProducesInstruments: []string{"USD", "GBP"}  # ❌ Missing EUR

# 2. Update metadata to include all currencies handler can produce
ProducesInstruments: []string{"USD", "EUR", "GBP", "NZD"}  # ✅ Complete list

# 3. Or restrict handler to only create declared currencies
# Add validation in handler:
if currency != "USD" && currency != "GBP" {
    return nil, fmt.Errorf("unsupported currency: %s", currency)
}
```

**Conservation Rule Guidelines:**

| Handler Type | ProducesInstruments | Example |
|--------------|---------------------|---------|
| **Ingestion** (creates new positions) | List all asset types | `["KWH", "GAS", "WATER"]` |
| **Settlement** (financial operations) | List all currencies | `["USD", "EUR", "GBP"]` |
| **Update** (modifies existing) | Empty list | `[]` |
| **Multi-currency** (any currency) | List all supported | `["USD", "EUR", "GBP", "NZD"]` |

**Prevention:**

- Add integration test that verifies metadata matches actual behaviour
- Code review checklist: ProducesInstruments matches handler implementation
- Use linter to detect metadata/implementation mismatches

### 5. gRPC Timeout

**Error Message:**

```text
level=ERROR msg="handler gRPC call failed" handler=position_keeping.initiate_log
  error="context deadline exceeded" duration=30s
```

**Root Cause:**

- Downstream service slow or unresponsive
- Database lock contention
- Network latency
- Handler timeout too short for operation

**Solution:**

```bash
# 1. Check downstream service health
kubectl get pods -n production | grep position-keeping
# If CrashLoopBackOff or pending → service is down

# 2. Check service logs for slow queries
kubectl logs -n production deployment/position-keeping | grep "slow query"

# 3. Check database connection pool
kubectl exec -n production deployment/position-keeping -- \
  curl localhost:8080/metrics | grep "db_connections"

# If connections exhausted → increase pool size or fix connection leaks

# 4. Increase handler timeout (if operation legitimately slow)
# In client config:
client, cleanup, err := positionkeepingclient.New(
    positionkeepingclient.Config{
        Timeout: 60 * time.Second,  # Increased from 30s
    },
)
```

**Timeout Recommendations:**

| Operation Type | Recommended Timeout | Rationale |
|----------------|---------------------|-----------|
| Simple read | 5s | Fast database query |
| Write operation | 30s | Database write + validation |
| Batch operation | 60s | Multiple database operations |
| External API call | 10s | Third-party service SLA |

**Prevention:**

- Monitor p99 latency for each handler
- Set alerts for timeout rate > 1%
- Use circuit breaker to fail fast when service is down
- Add retry logic with exponential backoff

### 6. Idempotency Key Collision

**Error Message:**

```text
level=ERROR msg="idempotency key collision detected"
  key="saga-abc123-step-2-retry-1" saga_id=abc123 step_index=2
  error="duplicate key value violates unique constraint"
```

**Root Cause:**

- Saga replayed with same idempotency key
- Concurrent saga executions with overlapping keys
- Idempotency key format not unique enough

**Solution:**

```bash
# 1. Check saga instance state
psql -d meridian -c "
  SELECT id, status, current_step_index, replay_count, lease_expires_at
  FROM saga_instances
  WHERE id = 'abc123'
"

# If replay_count > 5 → saga stuck in retry loop

# 2. Check for concurrent executions
psql -d meridian -c "
  SELECT id, pod_id, lease_expires_at
  FROM saga_instances
  WHERE id = 'abc123'
  ORDER BY lease_expires_at DESC
"

# If multiple pods claimed same saga → race condition

# 3. Verify idempotency key uniqueness
# Key format should be: saga_{id}_step_{index}_retry_{count}
# Check prepareClientContext in starlark.go

# 4. If saga genuinely stuck, manually advance
# ⚠️ WARNING: Only perform this in emergencies with proper authorisation
# Take a backup before proceeding:
pg_dump -d meridian -t saga_instances -t step_results > saga_backup_$(date +%Y%m%d_%H%M%S).sql

# Then run the UPDATE inside a transaction with safety guards:
psql -d meridian -c "
  BEGIN;
  -- Verify saga is actually stuck (high replay count, expired lease)
  SELECT id, status, current_step_index, replay_count, lease_expires_at
  FROM saga_instances
  WHERE id = 'abc123'
    AND replay_count > 5
    AND lease_expires_at < NOW()
  FOR UPDATE;

  -- If above query returns the saga, proceed with manual advance
  UPDATE saga_instances
  SET current_step_index = current_step_index + 1,
      replay_count = 0,
      lease_expires_at = NOW() + INTERVAL '5 minutes'
  WHERE id = 'abc123'
    AND replay_count > 5
    AND lease_expires_at < NOW();

  -- Verify update succeeded
  SELECT id, current_step_index, replay_count FROM saga_instances WHERE id = 'abc123';

  -- If everything looks correct, commit; otherwise ROLLBACK
  COMMIT;
"
```

**Prevention:**

- Use id + step_index + retry_count for idempotency keys
- Add unique constraint on idempotency_key in step_results table
- Monitor replay_count and alert when > threshold
- Implement exponential backoff for retries

## Verifying Handler Registration

### Check Registered Handlers

```bash
# 1. Grep logs for registration messages
kubectl logs -n production deployment/payment-order | grep "register.*handlers"

# Expected output:
# level=INFO msg="registered current-account handlers" count=4
# level=INFO msg="registered position-keeping handlers" count=3
# level=INFO msg="registered financial-accounting handlers" count=2

# 2. List all registered handlers (requires debug endpoint)
curl http://payment-order.production.svc.cluster.local:8080/debug/handlers

# Expected JSON response:
# {
#   "handlers": [
#     {"name": "current_account.create_lien", "category": "settlement"},
#     {"name": "current_account.execute_lien", "category": "settlement"},
#     {"name": "position_keeping.initiate_log", "category": "ingestion"},
#     ...
#   ]
# }
```

### Verify Handler Metadata

```bash
# Check handler metadata in code
cd services/current-account/client
grep -A 10 "RegisterStarlarkHandlers" starlark.go

# Verify:
# 1. All handlers have Category set
# 2. ProducesInstruments matches implementation
# 3. Handler names match Starlark script calls
```

## Debugging Handler Failures

### Enable Debug Logging

```bash
# 1. Increase log level for saga package
kubectl set env deployment/payment-order -n production LOG_LEVEL=debug

# 2. Watch logs in real-time
kubectl logs -f deployment/payment-order -n production | grep saga

# 3. Filter for specific handler
kubectl logs deployment/payment-order -n production | \
  grep "handler=current_account.create_lien"
```

### Inspect Handler Context

Add logging in handler to inspect Starlark context:

```go
func createLienHandler(client *Client) saga.Handler {
    return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
        // Log context for debugging
        log.Debug("handler invoked",
            "correlation_id", ctx.CorrelationID(),
            "knowledge_at", ctx.KnowledgeAt(),
            "idempotency_key", ctx.IdempotencyKey(),
            "params", params,
        )

        // Handler logic...
    }
}
```

### Trace gRPC Calls

```bash
# Enable gRPC tracing
kubectl set env deployment/payment-order -n production GRPC_TRACE=all

# Watch for handler gRPC calls
kubectl logs -f deployment/payment-order -n production | \
  grep "gRPC\|handler"

# Expected flow:
# 1. Handler invoked with params
# 2. gRPC request prepared
# 3. gRPC call to downstream service
# 4. gRPC response received
# 5. Response converted to map[string]any
```

### Parameter Validation Debugging

```bash
# Add parameter validation logging in handler
func createLienHandler(client *Client) saga.Handler {
    return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
        // Log all parameters before validation
        log.Debug("validating parameters", "params", params)

        accountID, err := saga.RequireStringParam(params, "account_id")
        if err != nil {
            log.Error("account_id validation failed", "error", err, "value", params["account_id"])
            return nil, err
        }

        amount, err := saga.RequireDecimalParam(params, "amount")
        if err != nil {
            log.Error("amount validation failed", "error", err, "value", params["amount"])
            return nil, err
        }

        log.Debug("parameters validated",
            "account_id", accountID,
            "amount", amount.String(),
        )

        // Continue with handler logic...
    }
}
```

## Testing Compensation Logic

### Unit Test Pattern

```go
func TestCreateLienHandler_Compensation(t *testing.T) {
    // 1. Setup: Create lien
    handler := createLienHandler(client)
    ctx := &saga.StarlarkContext{...}
    params := map[string]any{
        "account_id": "acc-123",
        "amount":     decimal.NewFromFloat(100),
        "currency":   "USD",
    }

    result, err := handler(ctx, params)
    require.NoError(t, err)
    lienID := result.(map[string]any)["lien_id"].(string)

    // 2. Execute: Compensation
    compensateHandler := terminateLienHandler(client)
    compensateParams := map[string]any{
        "lien_id": lienID,
    }

    _, err = compensateHandler(ctx, compensateParams)
    require.NoError(t, err)

    // 3. Verify: Lien terminated
    lien, err := client.GetLien(context.Background(), &pb.GetLienRequest{
        LienId: lienID,
    })
    require.NoError(t, err)
    assert.Equal(t, "TERMINATED", lien.GetStatus())
}
```

### Integration Test Pattern

```go
func TestDepositSaga_CompensationOnPositionKeepingFailure(t *testing.T) {
    // Setup: Real database with testcontainers
    db, cleanup := testdb.SetupCockroachDB(t, nil)
    defer cleanup()

    // Setup: Inject failure at position-keeping step
    posKeepingClient := &MockPositionKeepingClient{
        InitiateLogFunc: func(ctx context.Context, req *pb.Request) (*pb.Response, error) {
            return nil, errors.New("simulated failure")
        },
    }

    // Execute: Run saga (should fail and compensate)
    sagaRunner := setupSagaRunner(t, db, posKeepingClient)
    _, err := sagaRunner.Execute(ctx, "deposit.star", params)

    // Verify: Saga failed
    assert.Error(t, err)

    // Verify: Prior steps compensated (lien terminated)
    liens, _ := db.Query("SELECT status FROM liens WHERE account_id = $1", accountID)
    for liens.Next() {
        var status string
        liens.Scan(&status)
        assert.Equal(t, "TERMINATED", status, "lien should be compensated")
    }
}
```

### Testing Idempotency

```go
func TestCreateLienHandler_Idempotency(t *testing.T) {
    handler := createLienHandler(client)
    ctx := &saga.StarlarkContext{
        // Same idempotency key for both calls
        idempotencyKey: "test-saga-step-1",
    }
    params := map[string]any{
        "account_id": "acc-123",
        "amount":     decimal.NewFromFloat(100),
        "currency":   "USD",
    }

    // Call 1: Should create lien
    result1, err1 := handler(ctx, params)
    require.NoError(t, err1)
    lienID1 := result1.(map[string]any)["lien_id"].(string)

    // Call 2: Should return cached result (same lien_id)
    result2, err2 := handler(ctx, params)
    require.NoError(t, err2)
    lienID2 := result2.(map[string]any)["lien_id"].(string)

    // Verify: Same lien returned (idempotent)
    assert.Equal(t, lienID1, lienID2)

    // Verify: Only one lien in database
    count := 0
    db.QueryRow("SELECT COUNT(*) FROM liens WHERE account_id = $1", "acc-123").Scan(&count)
    assert.Equal(t, 1, count, "should only create one lien despite two calls")
}
```

## Production Debugging Commands

### Quick Health Check

```bash
# Check if saga-enabled services are healthy
kubectl get pods -n production -l saga-enabled=true

# Check service endpoints
kubectl get endpoints -n production payment-order

# Check for recent crashes
kubectl get events -n production --sort-by='.lastTimestamp' | grep Error
```

### Saga Execution Tracing

```bash
# Find saga by correlation ID
CORRELATION_ID="saga-abc123-deposit"
kubectl logs -n production deployment/payment-order | grep "$CORRELATION_ID"

# Expected log sequence:
# 1. Saga started
# 2. Step 1 executing (handler name, params)
# 3. Step 1 completed (result)
# 4. Step 2 executing
# 5. Step 2 completed
# ...
# N. Saga completed (total duration)
```

### Database Saga State

```bash
# Check saga instance state
kubectl exec -n production deployment/payment-order -- \
  psql -d meridian -c "
    SELECT id, status, current_step_index, total_steps, replay_count
    FROM saga_instances
    WHERE created_at > NOW() - INTERVAL '1 hour'
    ORDER BY created_at DESC
    LIMIT 10
  "

# Check step results for specific saga
kubectl exec -n production deployment/payment-order -- \
  psql -d meridian -c "
    SELECT step_index, step_name, status, error_message
    FROM step_results
    WHERE saga_instance_id = 'abc123'
    ORDER BY step_index
  "
```

### Handler Performance Metrics

```bash
# Check handler execution time (requires metrics endpoint)
curl http://payment-order.production.svc.cluster.local:8080/metrics | \
  grep saga_handler_duration_seconds

# Expected output:
# saga_handler_duration_seconds{handler="current_account.create_lien",quantile="0.5"} 0.025
# saga_handler_duration_seconds{handler="current_account.create_lien",quantile="0.99"} 0.150

# Check error rate
curl http://payment-order.production.svc.cluster.local:8080/metrics | \
  grep saga_handler_errors_total

# High error rate → investigate specific handler
```

## Escalation

### When to Escalate

Escalate to engineering team if:

- Compensation failed (inconsistent state across services)
- Saga stuck in retry loop (replay_count > 10)
- Handler consistently timing out (timeout rate > 10%)
- Metadata validation errors after deployment
- Nil pointer panics (indicates code bug)

### Information to Provide

When escalating, include:

1. Saga correlation ID
2. Service logs (all involved services)
3. Saga instance state from database
4. Step results from database
5. Recent deployments (within 24 hours)
6. Error rate metrics

### Recovery Procedures

For inconsistent state:

```bash
# 1. Document current state
psql -d meridian -c "
  SELECT * FROM saga_instances WHERE id = 'abc123'
" > saga_state.txt

psql -d meridian -c "
  SELECT * FROM step_results WHERE saga_instance_id = 'abc123'
" >> saga_state.txt

# 2. Manual compensation (if automated failed)
# Run compensation handlers manually in reverse order
curl -X POST http://payment-order/debug/compensate \
  -d '{"id": "abc123", "force": true}'

# 3. Mark saga as manually resolved
psql -d meridian -c "
  UPDATE saga_instances
  SET status = 'MANUALLY_COMPENSATED',
      resolved_by = 'operator@example.com',
      resolved_at = NOW()
  WHERE id = 'abc123'
"
```

## See Also

- [Adding Starlark Service Bindings](../guides/adding-starlark-service-bindings.md) - Implementation guide
- [Starlark Saga Architecture](../architecture/starlark-saga-architecture.md) - Architecture overview
- [Saga Failure Recovery](./saga-failure-recovery.md) - General saga recovery procedures
- [ADR-028: Starlark Saga Orchestration](../adr/0028-starlark-saga-cel-valuation.md) - Design decisions

---

**Last Updated:** 2026-02-04
**Maintained By:** Platform Team
