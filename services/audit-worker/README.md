# audit-worker

Platform service that processes audit log entries from the outbox table.

## Purpose

The audit-worker implements the worker component of the
[transactional outbox pattern][adr-0009] for audit logging. It:

[adr-0009]: ../../docs/adr/0009-application-level-audit-logging.md

1. Polls the `audit_outbox` table every 5 seconds
2. Processes records in batches of 100
3. Moves entries to the `audit_log` table
4. Implements retry logic (max 3 retries)
5. Exposes Prometheus metrics for monitoring

## Endpoints

| Endpoint | Port | Purpose |
|----------|------|---------|
| `/health/live` | 8080 | Kubernetes liveness probe |
| `/health/ready` | 8080 | Kubernetes readiness probe |
| `/health/startup` | 8080 | Kubernetes startup probe |
| `/metrics` | 8080 | Prometheus metrics |
| `/` | 8080 | Version info |

## Metrics

- `audit_outbox_depth` - Current number of entries in outbox
- `audit_processed_total` - Total entries processed
- `audit_failed_total` - Total entries failed
- `audit_processing_duration_seconds` - Processing latency histogram

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
