# Audit Consumer Kubernetes Deployments

This directory contains Kustomize-based Kubernetes deployments for the per-service audit consumer.

## Architecture

The audit consumer is deployed once per BIAN service domain to consume audit events from that
service's Kafka topic and write to tenant-scoped audit_log tables.

### Base Template

The `base/` directory contains the common deployment template with:

- **Deployment**: Pod template with health checks, security context, resource limits
- **Service**: ClusterIP service for health/metrics endpoints
- **ConfigMap**: Configuration for Kafka, database, and observability
- **ServiceAccount**: Kubernetes service account for RBAC
- **HorizontalPodAutoscaler**: Auto-scaling based on CPU/memory utilization
- **Secret**: Example database credentials (use external secret management in production)

### Service Overlays

Each overlay in `overlays/<service-name>/` configures a deployment for a specific service:

| Service | Topic | Initial Replicas | HPA Min/Max | Resource Profile |
|---------|-------|-----------------|-------------|------------------|
| `current-account` | `audit.events.current-account` | 5 | 5-20 | High (200m-1000m CPU, 256Mi-1Gi RAM) |
| `financial-accounting` | `audit.events.financial-accounting` | 2 | 2-8 | Standard (100m-500m CPU, 128Mi-512Mi RAM) |
| `position-keeping` | `audit.events.position-keeping` | 3 | 3-12 | Medium (150m-750m CPU, 192Mi-768Mi RAM) |
| `party` | `audit.events.party` | 2 | 2-8 | Standard (100m-500m CPU, 128Mi-512Mi RAM) |
| `payment-order` | `audit.events.payment-order` | 4 | 4-16 | High (150m-750m CPU, 192Mi-768Mi RAM) |
| `tenant` | `audit.events.tenant` | 2 | 2-8 | Standard (100m-500m CPU, 128Mi-512Mi RAM) |

Resource profiles are based on expected audit volume per service (Current Account and
Payment Order handle the highest transaction volumes).

## Deployment

### Prerequisites

- Kubernetes cluster (1.23+)
- kubectl configured with cluster access
- Kustomize (built into kubectl 1.14+)
- Kafka cluster accessible from Kubernetes
- PostgreSQL database with tenant-scoped schemas

### Deploy a Service

Deploy a specific service audit consumer:

```bash
# Deploy current-account audit consumer
kubectl apply -k deployments/k8s/audit-consumer/overlays/current-account

# Deploy all services
for service in current-account financial-accounting position-keeping party payment-order tenant; do
  kubectl apply -k deployments/k8s/audit-consumer/overlays/$service
done
```

### Verify Deployment

```bash
# Check deployment status for a service
kubectl get deployment -n meridian current-account-audit-consumer

# View pods
kubectl get pods -n meridian -l service=current-account

# Check HPA status
kubectl get hpa -n meridian current-account-audit-consumer

# View logs
kubectl logs -n meridian -l service=current-account --tail=100
```

### Configuration

Each deployment requires:

1. **Database Secret**: Create `audit-consumer-db` secret with `DATABASE_URL` for the service's database
2. **Kafka Bootstrap**: Update `kafka_bootstrap_servers` in overlay ConfigMap if not using default
3. **Service Name**: Configured in overlay (e.g., `current-account`)
4. **Audit Topic**: Service-specific Kafka topic (e.g., `audit.events.current-account`)

Example secret creation:

```bash
kubectl create secret generic current-account-audit-consumer-db \
  --namespace=meridian \
  --from-literal=DATABASE_URL="postgresql://user:pass@postgres:5432/current_account?sslmode=require"
```

### Scaling

Deployments auto-scale based on CPU/memory utilization via HPA. Manual scaling:

```bash
# Scale current-account consumer to 10 replicas
kubectl scale deployment current-account-audit-consumer -n meridian --replicas=10

# HPA will override manual scaling based on metrics
```

### Health Checks

Each deployment includes:

- **Startup Probe**: `/health/startup` - ensures consumer initialized (60s max)
- **Liveness Probe**: `/health/live` - restarts pod if consumer unhealthy
- **Readiness Probe**: `/health/ready` - removes from service if not ready

### Monitoring

Prometheus metrics exposed on port 8080 at `/metrics`:

- `audit_consumer_messages_processed_total{service="current-account"}`
- `audit_consumer_messages_failed_total{service="current-account"}`
- `audit_consumer_processing_duration_seconds{service="current-account"}`
- `audit_consumer_dlq_messages_total{service="current-account"}`

## Security

- **Non-root**: Runs as user 65532
- **Read-only root filesystem**: Prevents container modification
- **No privilege escalation**: Blocks privilege escalation
- **Dropped capabilities**: All Linux capabilities dropped
- **Secrets management**: Use external secret management (Sealed Secrets, External Secrets Operator) in production

## High Availability

- **Pod Anti-Affinity**: Spreads replicas across nodes
- **Rolling Updates**: Zero-downtime deployments (maxSurge: 1, maxUnavailable: 0)
- **Graceful Shutdown**: 30s termination grace period for in-flight message completion
- **Consumer Groups**: Kafka consumer groups ensure each message processed once

## Testing

Validate manifests without applying:

```bash
# Validate base
kubectl kustomize deployments/k8s/audit-consumer/base

# Validate specific overlay
kubectl kustomize deployments/k8s/audit-consumer/overlays/current-account

# Dry-run deployment
kubectl apply -k deployments/k8s/audit-consumer/overlays/current-account --dry-run=client
```

## Troubleshooting

### Pod CrashLoopBackOff

Check logs and configuration:

```bash
kubectl logs -n meridian current-account-audit-consumer-xxxxx
kubectl describe pod -n meridian current-account-audit-consumer-xxxxx
```

Common issues:

- Database connection failure (check secret `DATABASE_URL`)
- Kafka connection failure (check `kafka_bootstrap_servers`)
- Missing environment variables (check ConfigMap)

### High Memory Usage

Adjust resource limits in overlay:

```yaml
patches:
- target:
    kind: Deployment
    name: audit-consumer
  patch: |-
    - op: replace
      path: /spec/template/spec/containers/0/resources/limits/memory
      value: 2Gi
```

### Kafka Consumer Lag

Check HPA scaling and increase replicas if needed:

```bash
kubectl get hpa -n meridian
kubectl describe hpa -n meridian current-account-audit-consumer
```

## Development

For local development, see `deployments/k8s/local/` for minimal single-replica configurations
suitable for Tilt or kind clusters.
