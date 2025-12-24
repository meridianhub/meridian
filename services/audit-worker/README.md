# audit-worker

**Fallback service** that processes audit log entries from the outbox table when Kafka is unavailable.

## Purpose

The audit-worker implements the **fallback path** of the dual-path audit system described in
[ADR-0009][adr-0009]. Under normal operation, audit events flow through Kafka to dedicated audit consumers.
When Kafka is unavailable or disabled, GORM hooks write to the `audit_outbox` table, and this worker:

[adr-0009]: ../../docs/adr/0009-application-level-audit-logging.md

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

- `meridian_audit_worker_outbox_depth_total` - Current number of entries in outbox (gauge)
- `meridian_audit_worker_outbox_processed_total` - Total entries processed (counter)
- `meridian_audit_worker_outbox_failed_total` - Total entries failed (counter)
- `meridian_audit_worker_processing_duration_seconds` - Batch processing duration (histogram)
- `meridian_audit_worker_entry_age_seconds` - Age of entries when processed (histogram)

## Configuration

| Environment Variable | Default | Description |
|---------------------|---------|-------------|
| `PORT` | `8080` | HTTP server port |
| `DATABASE_URL` | (local dev default) | PostgreSQL connection string |

## Development

```bash
# Run locally
go run ./services/audit-worker

# Run tests
go test ./services/audit-worker/...
```

## Deployment

Deployed via Kubernetes manifests in `deployments/k8s/base/`.

- **Replicas**: 3 (production)
- **Resource limits**: 500m CPU, 512Mi memory
