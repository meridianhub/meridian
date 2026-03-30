# Audit System Monitoring Guide

This guide covers operational monitoring of the Meridian async audit system.

## Architecture Overview

The audit system uses a dual-path approach:

1. **Primary Path**: Business operations publish to Kafka, consumers write to `audit_log`
2. **Fallback Path**: When Kafka unavailable, events write to `audit_outbox`, processed by workers

Both paths ultimately write to the `audit_log` table in each service's database.

## Prometheus Metrics Reference

### Outbox Worker Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `meridian_audit_worker_outbox_depth` | Gauge | `schema` | Pending entries in outbox |
| `meridian_audit_worker_outbox_processed_total` | Counter | `schema` | Successfully processed entries |
| `meridian_audit_worker_outbox_failed_total` | Counter | `schema` | Failed entries (retries exhausted) |
| `meridian_audit_worker_processing_duration_seconds` | Histogram | - | Batch processing time |
| `meridian_audit_worker_batch_size` | Histogram | - | Entries processed per batch |
| `meridian_audit_worker_poll_interval_seconds` | Gauge | `schema` | Current adaptive poll interval |
| `meridian_audit_worker_empty_polls_consecutive` | Gauge | `schema` | Consecutive empty polls |
| `meridian_audit_worker_entry_age_seconds` | Histogram | - | Time from creation to processing |

### Kafka Publisher Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `meridian_audit_kafka_events_published_total` | Counter | `schema`, `operation`, `status` | Events published to Kafka |
| `meridian_audit_kafka_publish_duration_seconds` | Histogram | - | Time to publish to Kafka |
| `meridian_audit_kafka_fallback_used_total` | Counter | `schema`, `reason` | Fallback to outbox activations |

### Kafka Consumer Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `meridian_audit_kafka_events_consumed_total` | Counter | `schema`, `operation`, `status` | Events consumed from Kafka |
| `meridian_audit_kafka_consume_duration_seconds` | Histogram | - | Time to process consumed events |
| `meridian_audit_kafka_consumer_lag_messages` | Gauge | - | Consumer lag behind latest offset |
| `meridian_audit_kafka_dlq_messages_total` | Counter | `schema`, `reason` | Messages sent to DLQ |

## Recommended Alert Thresholds

### Critical Alerts

```yaml
# High outbox depth - audit processing is falling behind
- alert: AuditOutboxDepthCritical
  expr: meridian_audit_worker_outbox_depth > 1000
  for: 5m
  labels:
    severity: critical
  annotations:
    summary: "Audit outbox depth exceeds 1000 entries"
    description: "Schema {{ $labels.schema }} has {{ $value }} pending audit entries"

# Failed entries accumulating
- alert: AuditFailedEntriesAccumulating
  expr: rate(meridian_audit_worker_outbox_failed_total[5m]) > 0.1
  for: 5m
  labels:
    severity: critical
  annotations:
    summary: "Audit entries failing at {{ $value }}/sec"
    description: "Check audit_outbox for errors in schema {{ $labels.schema }}"

# Consumer lag too high
- alert: AuditConsumerLagCritical
  expr: meridian_audit_kafka_consumer_lag_messages > 10000
  for: 5m
  labels:
    severity: critical
  annotations:
    summary: "Audit consumer lag exceeds 10000 messages"
    description: "Consider scaling audit consumers"
```

### Warning Alerts

```yaml
# Moderate outbox depth
- alert: AuditOutboxDepthWarning
  expr: meridian_audit_worker_outbox_depth > 100
  for: 10m
  labels:
    severity: warning
  annotations:
    summary: "Audit outbox depth elevated"

# Frequent fallback usage
- alert: AuditKafkaFallbackFrequent
  expr: rate(meridian_audit_kafka_fallback_used_total[5m]) > 1
  for: 5m
  labels:
    severity: warning
  annotations:
    summary: "Audit falling back to outbox frequently"
    description: "Reason: {{ $labels.reason }}, Rate: {{ $value }}/sec"

# DLQ messages
- alert: AuditDLQMessages
  expr: increase(meridian_audit_kafka_dlq_messages_total[1h]) > 0
  labels:
    severity: warning
  annotations:
    summary: "Audit events sent to DLQ"
    description: "Check DLQ topic for failed events"
```

## Grafana Dashboard Queries

### Overview Panel

```promql
# Total audit throughput across all paths
sum(rate(meridian_audit_kafka_events_published_total{status="success"}[5m])) +
sum(rate(meridian_audit_worker_outbox_processed_total[5m]))
```

### Outbox Depth by Schema

```promql
meridian_audit_worker_outbox_depth
```

### Primary vs Fallback Path Usage

```promql
# Primary path (Kafka)
sum(rate(meridian_audit_kafka_events_published_total{status="success"}[5m])) by (schema)

# Fallback path (Outbox)
sum(rate(meridian_audit_kafka_fallback_used_total[5m])) by (schema, reason)
```

### Processing Latency (p95)

```promql
histogram_quantile(0.95, rate(meridian_audit_worker_entry_age_seconds_bucket[5m]))
```

### Kafka Publish Latency (p99)

```promql
histogram_quantile(0.99, rate(meridian_audit_kafka_publish_duration_seconds_bucket[5m]))
```

## Common Failure Modes

### 1. Kafka Unavailable

**Symptoms:**

- `meridian_audit_kafka_fallback_used_total{reason="publish_error"}` increasing
- `meridian_audit_worker_outbox_depth` rising

**Impact:** Minimal - fallback path handles audit delivery

**Resolution:**

1. Check Kafka broker health: `kubectl get pods -n kafka`
2. Check Kafka connectivity from services
3. Once Kafka recovers, outbox will drain and primary path resumes

### 2. Database Connection Issues

**Symptoms:**

- `meridian_audit_worker_outbox_failed_total` increasing
- Errors in audit-worker logs containing "connection refused"

**Impact:** Critical - audit events may be lost if outbox write fails

**Resolution:**

1. Check database connectivity
2. Verify database credentials
3. Check connection pool exhaustion

### 3. Outbox Processing Backlog

**Symptoms:**

- `meridian_audit_worker_outbox_depth` consistently high
- `meridian_audit_worker_entry_age_seconds` showing old entries

**Impact:** Audit records delayed but not lost

**Resolution:**

1. **Scale workers**: Deploy additional audit-worker replicas
2. **Increase batch size**: Adjust `WithBatchSize(200)` or higher
3. **Check for stuck entries**: Query below for stuck entries
   `SELECT * FROM audit_outbox WHERE status = 'processing' AND created_at < NOW() - INTERVAL '5 min'`
4. **Reset stuck entries**: Entries stuck in 'processing' are auto-reset after 5 minutes

### 4. Consumer Processing Failures

**Symptoms:**

- `meridian_audit_kafka_events_consumed_total{status="failure"}` increasing
- `meridian_audit_kafka_dlq_messages_total` increasing

**Impact:** Events sent to DLQ require manual processing

**Resolution:**

1. Check consumer logs for error details
2. Examine DLQ topic: `kafka-console-consumer --topic audit.events.dlq`
3. Fix root cause and replay DLQ messages

## Operational Procedures

### Check Audit System Health

```bash
# Check all audit-related pods
kubectl get pods -A | grep audit

# Check outbox depth across all services
for svc in current-account party tenant payment-order position-keeping financial-accounting; do
  echo "=== $svc ==="
  kubectl exec -it deploy/$svc -- psql -c "SELECT COUNT(*) AS pending FROM audit_outbox WHERE status = 'pending'"
done
```

### Query Failed Entries

```sql
SELECT
    table_name,
    operation,
    record_id,
    retry_count,
    last_error,
    created_at
FROM audit_outbox
WHERE status = 'failed'
ORDER BY created_at DESC
LIMIT 20;
```

### Retry Failed Entries

```sql
-- Reset failed entries to pending for reprocessing
UPDATE audit_outbox
SET status = 'pending', retry_count = 0, last_error = NULL
WHERE status = 'failed'
  AND created_at > NOW() - INTERVAL '1 day';
```

### View Recent Audit Activity

```sql
SELECT
    table_name,
    operation,
    COUNT(*) as count,
    MAX(changed_at) as latest
FROM audit_log
WHERE changed_at > NOW() - INTERVAL '1 hour'
GROUP BY table_name, operation
ORDER BY count DESC;
```

### Check Audit Lag

```sql
-- Compare outbox creation time to audit_log creation time
WITH outbox_stats AS (
    SELECT
        MIN(created_at) as oldest_pending,
        COUNT(*) as pending_count
    FROM audit_outbox
    WHERE status = 'pending'
)
SELECT
    EXTRACT(EPOCH FROM (NOW() - oldest_pending)) as lag_seconds,
    pending_count
FROM outbox_stats;
```

## Performance Tuning

### Worker Configuration

| Setting | Default | Recommended Range | Notes |
|---------|---------|-------------------|-------|
| Batch Size | 100 | 50-500 | Higher for high-volume services |
| Poll Interval | 5s | 1s-30s | Lower for real-time requirements |
| Max Retries | 3 | 3-5 | Higher for transient failures |
| Adaptive Min | 100ms | 50ms-500ms | Lower bound when busy |
| Adaptive Max | 30s | 10s-60s | Upper bound when idle |

### Scaling Guidelines

| Outbox Depth | Action |
|--------------|--------|
| < 100 | Normal operation |
| 100-500 | Monitor, may need tuning |
| 500-1000 | Increase batch size or worker replicas |
| > 1000 | Critical - scale immediately |

## Related Documentation

- [ADR-0009: Application-Level Audit Logging](../adr/0009-application-level-audit-logging.md)
- [ADR-0020: Per-Service Audit Workers](../adr/0020-per-service-audit-workers.md)
- [Audit Package README](../../shared/platform/audit/README.md)
- [Incident Response Runbook](../runbooks/incident-response.md)
