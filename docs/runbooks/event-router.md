---
name: event-router-runbook
description: Operational runbook for the event-router service that dispatches Kafka events to tenant sagas
triggers:
  - Event-triggered sagas are not firing when expected
  - Kafka consumer lag is growing on event-router topics
  - Chain depth exceeded alerts are firing
  - Saga trigger failures or CEL filter errors are reported
  - Event-router service is failing health checks or not starting
instructions: |
  The event-router service consumes Kafka events and dispatches them to tenant sagas via control-plane ExecuteSaga gRPC.
  Check Prometheus metrics (events_received, sagas_triggered, filter_evaluation_errors, chain_depth_exceeded) first.
  Use kubectl logs deployment/event-router to inspect structured logs with channel, saga_name, and correlation_id fields.
  The saga registry reloads on manifest apply; a missed event window is expected during reload.
  CockroachDB-backed idempotency store deduplicates by (sagaName, correlationID); duplicate_events_total is expected to be non-zero.
---

# Event Router Runbook

Operational procedures for the event-router service, which consumes Kafka events and dispatches them to
tenant-configured sagas via the `event:` trigger.

## Service Overview

| Attribute | Value |
|-----------|-------|
| **Service Name** | `event-router` |
| **Port** | 8080 (HTTP — health checks and metrics) |
| **Kubernetes Deployment** | `deployment/event-router` |
| **Language** | Go |
| **Dependency: Kafka** | Multi-channel consumer (all registered event channels) |
| **Dependency: Control Plane** | gRPC `ExecuteSaga` RPC |
| **Dependency: CockroachDB** | Idempotency store |

## Architecture

```text
Kafka Topics
    │
    ▼
Event Router
    ├── Saga Registry (channel → [CompiledSaga with CEL filter])
    ├── CEL Filter Evaluation (< 1ms per saga)
    ├── Idempotency Store (CockroachDB-backed dedup)
    └── gRPC → control-plane:ExecuteSaga
```

The event-router performs no business logic itself. It is a routing layer: for each Kafka event, it
evaluates registered CEL filters and triggers matching sagas.

## Configuration Reference

| Environment Variable | Required | Default | Description |
|---------------------|----------|---------|-------------|
| `KAFKA_BOOTSTRAP_SERVERS` | Yes | — | Kafka broker addresses (e.g., `kafka:9092`) |
| `CONSUMER_GROUP_ID` | No | `event-router` | Consumer group ID for offset management |
| `POSITION_KEEPING_ENDPOINT` | Yes | — | gRPC endpoint for Position Keeping (legacy, billing path) |
| `TENANT_ZERO_ID` | Yes | — | UUID of the platform billing tenant |
| `TENANT_ACCOUNT_MAPPING` | No | `{}` | JSON mapping of tenant UUIDs to billing account UUIDs |
| `HTTP_PORT` | No | `8080` | HTTP port for health checks and metrics |
| `ENABLE_MDS_OUTPUT` | No | `true` | Feature flag for Market Data Service output |
| `MDS_SERVICE_ADDR` | No | — | gRPC address for Market Data Service |
| `MDS_AGGREGATION_WINDOW` | No | `1h` | Aggregation window for buffered observations |
| `MDS_FLUSH_INTERVAL` | No | `5m` | Flush interval for buffered observations |

## HTTP Endpoints

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/healthz` | GET | Liveness probe (always returns 200 OK) |
| `/ready` | GET | Readiness probe (checks Kafka consumer initialization) |
| `/metrics` | GET | Prometheus metrics endpoint |

## Prometheus Metrics

### Dispatch Pipeline

| Metric | Type | Labels | Alert Threshold |
|--------|------|--------|-----------------|
| `meridian_event_router_events_received_total` | Counter | `channel` | Monitor for sudden drops |
| `meridian_event_router_sagas_triggered_total` | Counter | `saga_name`, `channel` | Monitor for unexpected drops |
| `meridian_event_router_filter_evaluation_duration_seconds` | Histogram | `saga_name` | p99 > 10ms warrants investigation |
| `meridian_event_router_filter_evaluation_errors_total` | Counter | `saga_name` | Any non-zero rate is an alert |
| `meridian_event_router_chain_depth_exceeded_total` | Counter | — | Any non-zero rate warrants investigation |
| `meridian_event_router_saga_trigger_failures_total` | Counter | `saga_name`, `channel` | Any non-zero rate is an alert |
| `meridian_event_router_duplicate_events_total` | Counter | `saga_name` | Non-zero is expected; track trend |

### Recommended Alert Rules

```yaml
# Critical: Saga trigger failures
- alert: EventRouterSagaTriggerFailures
  expr: rate(meridian_event_router_saga_trigger_failures_total[5m]) > 0
  for: 2m
  annotations:
    summary: "Event-router saga trigger failures detected"
    description: "Saga {{ $labels.saga_name }} is failing to trigger on channel {{ $labels.channel }}"

# Warning: CEL filter evaluation errors
- alert: EventRouterFilterEvaluationErrors
  expr: rate(meridian_event_router_filter_evaluation_errors_total[5m]) > 0
  for: 1m
  annotations:
    summary: "CEL filter evaluation errors in event-router"
    description: "Saga {{ $labels.saga_name }} has a misconfigured CEL filter"

# Warning: Chain depth exceeded
- alert: EventRouterChainDepthExceeded
  expr: rate(meridian_event_router_chain_depth_exceeded_total[5m]) > 0
  for: 2m
  annotations:
    summary: "Event-router chain depth limit exceeded — events are being dropped"
    description: "A saga pipeline has reached max chain depth. Check CEL filter chain termination."

# Warning: No events received on channel (possible Kafka issue)
- alert: EventRouterNoEventsOnChannel
  expr: increase(meridian_event_router_events_received_total[10m]) == 0
  for: 10m
  annotations:
    summary: "No events received on any channel for 10 minutes"
    description: "Event-router may have lost Kafka connection or consumer group is stalled"
```

---

## Procedures

### Check Service Health

```bash
# Liveness
kubectl exec -it deployment/event-router -- curl -s http://localhost:8080/healthz

# Readiness (includes Kafka consumer initialization check)
kubectl exec -it deployment/event-router -- curl -s http://localhost:8080/ready

# Recent logs
kubectl logs deployment/event-router --tail=50

# Structured log fields to look for:
# "component": "saga_dispatch_handler"
# "channel": the Kafka channel
# "saga_name": the saga that fired
# "correlation_id": the idempotency key
# "chain_depth": current chain hop count
```

### Investigate: Saga Not Firing

**Step 1:** Confirm events are arriving on the channel.

```bash
# Check events received counter
kubectl exec -it deployment/event-router -- \
  curl -s http://localhost:8080/metrics | grep 'events_received_total'

# Expected output:
# meridian_event_router_events_received_total{channel="position-keeping.transaction-captured.v1"} 1234
```

**Step 2:** Confirm the saga is registered and filter is compiling.

```bash
# Check for filter evaluation errors at startup
kubectl logs deployment/event-router | grep "compile CEL filter"

# Expected output if OK: nothing
# Expected output if error:
# level=ERROR msg="compile CEL filter for saga" saga_name="my_saga" error="..."
```

**Step 3:** Check if events are matching the filter.

```bash
# Check sagas triggered counter
kubectl exec -it deployment/event-router -- \
  curl -s http://localhost:8080/metrics | grep 'sagas_triggered_total'

# Check filter did not match logs
kubectl logs deployment/event-router | grep "CEL filter did not match"
# Log fields: saga_name, channel, correlation_id

# Check filter evaluation errors
kubectl logs deployment/event-router | grep "CEL filter evaluation error"
```

**Step 4:** Check control-plane is reachable.

```bash
# Check saga trigger failures
kubectl exec -it deployment/event-router -- \
  curl -s http://localhost:8080/metrics | grep 'saga_trigger_failures_total'

# Check gRPC connectivity to control-plane
kubectl logs deployment/event-router | grep "trigger saga"
```

**Step 5:** Check manifest is applied and saga is registered.

```bash
# Via Meridian MCP: confirm saga exists and has event: trigger
mcp__meridian__meridian_handlers_describe(trigger_prefix: "webhook")
# Note: use the control-plane describe API to inspect active sagas
```

---

### Investigate: Chain Depth Exceeded

**Symptom:** `meridian_event_router_chain_depth_exceeded_total` is non-zero. Events are being dropped.

**Impact:** Sagas in the chain after the depth limit are not executing. Positions may be missing.

**Step 1:** Identify which sagas are in the chain.

```bash
kubectl logs deployment/event-router | grep "chain depth exceeded"
# Log fields: channel, chain_depth, max_chain_depth
```

**Step 2:** Review the saga's CEL filter for chain termination.

The filter must exclude events produced by the saga itself. Common pattern: if the saga produces
GBP positions, the filter must exclude `instrument_code == 'GBP'`:

```cel
# Correct: terminates at GBP
event.instrument_code != 'GBP' && event.direction == 'DEBIT'

# Wrong: does not terminate, loops on GBP output
event.direction == 'DEBIT'
```

**Step 3:** Update the manifest with a corrected filter and re-apply.

```bash
# Via Meridian MCP: validate the updated filter
mcp__meridian__meridian_cel_validate(
  expression: "event.instrument_code != 'GBP' && event.direction == 'DEBIT'",
  environment: "eligibility"
)

# Apply manifest with plan-then-apply workflow
mcp__meridian__meridian_manifest_plan(manifest: {...})
mcp__meridian__meridian_manifest_apply(manifest: {...}, plan_hash: "...", applied_by: "operator@example.com")
```

**Step 4:** If immediate relief is needed, temporarily increase `MAX_CHAIN_DEPTH` (not recommended for
permanent use — fix the filter instead):

```bash
kubectl set env deployment/event-router MAX_CHAIN_DEPTH=20
```

---

### Investigate: High Kafka Consumer Lag

**Symptom:** Consumer group `event-router` is falling behind on one or more partitions.

**Step 1:** Check lag per topic/partition.

```bash
# Kafka consumer lag via kubectl
kubectl exec -it deployment/kafka -- \
  kafka-consumer-groups.sh --bootstrap-server localhost:9092 \
  --group event-router --describe

# Or via metrics
kubectl exec -it deployment/event-router -- \
  curl -s http://localhost:8080/metrics | grep 'consumer_lag'
```

**Step 2:** Check if the service is healthy and processing.

```bash
kubectl get pods -l app=event-router
kubectl logs deployment/event-router --tail=20
```

**Step 3:** Scale up if lag is due to high throughput.

```bash
kubectl scale deployment/event-router --replicas=3
```

Note: Kafka partitions cap the effective parallelism. Each partition is processed by at most one
consumer in the group. If lag persists, check partition count vs. replica count.

**Step 4:** Check for slow downstream (control-plane gRPC latency causing backpressure).

```bash
# Check saga trigger duration in logs
kubectl logs deployment/event-router | grep "saga triggered"

# Check control-plane health
kubectl get pods -l app=control-plane
```

---

### Investigate: Saga Trigger Failures

**Symptom:** `meridian_event_router_saga_trigger_failures_total` is non-zero.

These are infrastructure-level failures where the control-plane `ExecuteSaga` gRPC call returned an
error after all retries were exhausted.

**Step 1:** Identify which sagas and channels are failing.

```bash
kubectl exec -it deployment/event-router -- \
  curl -s http://localhost:8080/metrics | grep 'saga_trigger_failures_total'

kubectl logs deployment/event-router | grep "trigger saga"
# Log fields: saga_name, channel, correlation_id, error
```

**Step 2:** Check control-plane health.

```bash
kubectl get pods -l app=control-plane
kubectl logs deployment/control-plane --tail=50
```

**Step 3:** Check for saga definition errors in the control-plane.

```bash
kubectl logs deployment/control-plane | grep "ExecuteSaga"
```

**Note:** The event-router does not retry saga trigger failures after they return an error. If the
control-plane was temporarily unavailable, affected events are not replayed. Events must be replayed
from Kafka if recovery is needed.

---

### Investigate: CEL Filter Evaluation Errors

**Symptom:** `meridian_event_router_filter_evaluation_errors_total` is non-zero.

CEL filter evaluation errors indicate a filter expression that compiled successfully but fails at
runtime (e.g., accessing a field that does not exist on the event proto).

**Step 1:** Identify the failing saga.

```bash
kubectl exec -it deployment/event-router -- \
  curl -s http://localhost:8080/metrics | grep 'filter_evaluation_errors_total'

kubectl logs deployment/event-router | grep "CEL filter evaluation error"
# Log fields: saga_name, channel, correlation_id, error
```

**Step 2:** Review the filter expression and event payload structure.

The filter expression may be accessing a field that does not exist in the channel's event proto.
Compare the filter against the AsyncAPI channel definition.

**Step 3:** Fix the filter and re-apply the manifest.

```bash
# Validate the corrected expression
mcp__meridian__meridian_cel_validate(
  expression: "event.correct_field_name == 'VALUE'",
  environment: "eligibility"
)
```

The saga is skipped (not retried) when the filter evaluation fails. Events that arrived while the
filter was broken are not replayed. If data consistency is critical, replay from Kafka.

---

### Restart the Service

```bash
# Rolling restart (preserves availability)
kubectl rollout restart deployment/event-router

# Verify rollout completed
kubectl rollout status deployment/event-router

# Check new pods are healthy
kubectl get pods -l app=event-router
```

**Note:** During restart, the consumer group rebalances. There is a brief window where events are
buffered in Kafka and not processed. This is normal and self-corrects after rebalance completes.

---

## Failure Mode Reference

| Failure | Behaviour | Impact | Recovery |
|---------|-----------|--------|----------|
| CEL filter compile error at startup | Saga not registered (event-router logs error) | Saga never fires | Fix filter in manifest and re-apply |
| CEL filter evaluation error at runtime | Saga skipped for this event (warning log) | Single event not processed | Fix filter; replay event if needed |
| Chain depth exceeded | Event dropped (warning log) | Sagas beyond depth limit don't fire | Fix CEL filter chain termination |
| Control-plane gRPC failure | Trigger fails; error logged | Saga not executed | Replay event from Kafka |
| Kafka consumer lag | Events processed late | Saga execution latency increases | Scale deployment; check partition count |
| Idempotency store unavailable | Dispatch attempted without deduplication | Risk of duplicate executions | Restore CockroachDB connectivity |
| Event-router pod crash | Consumer group rebalances; events queue in Kafka | Temporary processing delay | Kubernetes restarts pod automatically |

---

## Escalation

### When to Escalate

Escalate to the on-call engineering team if:

- Saga trigger failures persist for more than 5 minutes
- Kafka consumer lag exceeds 5,000 messages and does not stabilize
- Chain depth exceeded events are causing missing positions (data consistency concern)
- CEL filter errors affect a high-value saga (billing, settlement, compliance)
- Service fails to restart after rollout

### Escalation Information

```text
Event Router Incident Report
=============================

Affected Sagas: [saga names]
Affected Channels: [channel names]
Duration: [start time to now]
Event Count Affected: [from metrics]

Symptoms:
- [metric name and value]
- [log excerpt]

Investigation Steps Taken:
- [step 1 and finding]
- [step 2 and finding]

Data Consistency Assessment:
- Are positions missing? [yes/no/unknown]
- Can missing events be replayed from Kafka? [yes/no - retention period?]
```

---

## Post-Incident Actions

After resolving an incident:

1. **Document root cause** and timeline
2. **Verify metrics** return to baseline
3. **Check downstream data consistency**: query position logs for affected accounts/channels
4. **Replay events if needed** (within Kafka retention window)
5. **Update alert thresholds** if current thresholds were insufficient
6. **Update this runbook** with any new troubleshooting steps discovered

---

## Related Documentation

- [ADR-0033: Event-Triggered Sagas](../adr/0033-event-triggered-sagas.md) — Architecture decisions
- [Event-Triggered Sagas Skill](../skills/event-triggered-sagas.md) — Configuration guide
- [Saga Failure Recovery Runbook](saga-failure-recovery.md) — Recovering from saga execution failures
- [Event Router Service README](../../services/event-router/README.md) — Service documentation
- [Starlark Style Guide](../guides/starlark-style-guide.md) — Saga authoring conventions

## Changelog

| Date | Change | Author |
|------|--------|--------|
| 2026-03-04 | Initial version — event-router operational runbook | Platform Team |
