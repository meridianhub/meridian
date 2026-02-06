---
name: audit-worker-service
description: Fallback audit logging worker for Kafka outage recovery
triggers:
  - Audit log processing and outbox draining
  - Kafka unavailability and fallback mode
  - Audit metrics and monitoring
  - Audit event retention and recovery
instructions: |
  The audit-worker processes audit_outbox entries when Kafka is unavailable.
  It polls the outbox table every 5 seconds and moves entries to audit_log.
  Monitor via Prometheus metrics: meridian_audit_worker_outbox_depth (alert if > 1000).

  Port: 8080 (HTTP for health/metrics)
---

# audit-worker

**Fallback service** that processes audit log entries from the outbox table when Kafka is unavailable.

## Purpose

The audit-worker implements the **fallback path** of the dual-path audit system described in
[ADR-0009][adr-0009]. Under normal operation, audit events flow through Kafka to dedicated audit consumers.
When Kafka is unavailable (network partition, broker outage), GORM hooks automatically write to the `audit_outbox`
table instead, and this worker:

[adr-0009]: ../../docs/adr/0009-application-level-audit-logging.md

**Note**: "Unavailable" refers to runtime detection of Kafka connectivity issues (5s timeout), not a configuration
flag. There is no feature flag to enable/disable this worker - it is always running in production to process outbox
entries written during Kafka outages.

1. Polls the `audit_outbox` table every 5 seconds (for entries written during Kafka outages)
2. Processes records in batches of 100
3. Moves entries to the `audit_log` table
4. Implements retry logic (max 3 retries)
5. Exposes Prometheus metrics for monitoring

**Normal flow**: GORM hooks → Kafka → Audit Consumers → `audit_log`
**Fallback flow**: GORM hooks → `audit_outbox` → audit-worker → `audit_log`

## Endpoints

| Endpoint | Port | Purpose |
|----------|------|---------|
| `/health/live` | 8080 | Kubernetes liveness probe |
| `/health/ready` | 8080 | Kubernetes readiness probe |
| `/health/startup` | 8080 | Kubernetes startup probe |
| `/metrics` | 8080 | Prometheus metrics |
| `/` | 8080 | Version info |

## Metrics

**Worker-specific metrics:**

- `meridian_audit_worker_outbox_depth` - Current number of entries in outbox (gauge)
- `meridian_audit_worker_outbox_processed_total` - Total entries processed (counter)
- `meridian_audit_worker_outbox_failed_total` - Total entries failed (counter)
- `meridian_audit_worker_processing_duration_seconds` - Batch processing duration (histogram)
- `meridian_audit_worker_entry_age_seconds` - Age of entries when processed (histogram)

**System-wide metrics** (for overall audit health):

- `meridian_audit_kafka_events_published_total` - Primary path usage (Kafka) (counter)
- `meridian_audit_kafka_fallback_used_total` - Fallback path activations (counter)
- `meridian_audit_kafka_events_consumed_total` - Consumer throughput (counter)

**Alerting thresholds:**

- Alert if `meridian_audit_worker_outbox_depth` > 1000 for 5 minutes (indicates Kafka outage or worker lag)
- Alert if `entry_age_seconds` p99 > 60s (indicates processing delays)

See [ADR-0009](../../docs/adr/0009-application-level-audit-logging.md) for complete Prometheus metrics
reference and monitoring strategy.

## Configuration

| Environment Variable | Default | Description |
|---------------------|---------|-------------|
| `PORT` | `8080` | HTTP server port |
| `DATABASE_URL` | (local dev default) | PostgreSQL connection string |

## Directory Structure

```text
services/audit-worker/
├── cmd/                    # Entry point (main.go)
├── domain/                 # Domain models (Measurement, AuditOperation, Transformer)
├── adapters/
│   ├── kafka/              # Kafka consumer adapter (AuditConsumer)
│   └── persistence/        # Database adapter (TenantAuditWriter)
├── app/                    # Configuration and dependency injection (Container)
├── observability/          # Metrics and health checks
└── README.md
```

## Development

```bash
# Run locally
go run ./services/audit-worker/cmd

# Run tests
go test ./services/audit-worker/...
```

## Deployment

Deployed via Kubernetes manifests in `deployments/k8s/base/`.

- **Replicas**: 3 (production)
- **Resource limits**: 500m CPU, 512Mi memory
