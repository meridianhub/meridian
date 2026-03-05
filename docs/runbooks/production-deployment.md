---
name: production-deployment-runbook
description: Step-by-step guide for deploying Meridian to a production Kubernetes cluster, including infrastructure prerequisites, service startup order, health verification, and rollback procedures
triggers:

  - Initial production deployment
  - New environment provisioning
  - Major version upgrade deployment
  - Disaster recovery re-deployment

instructions: |
  Follow infrastructure prerequisites before deploying services. Apply database
  migrations per service before starting applications. Deploy services in dependency
  order (platform first, then core, then orchestration). Verify health checks at
  each stage before proceeding. Use rollback procedures if any stage fails.
---

# Production Deployment Guide

**When to use this runbook**: Initial production deployment, new environment provisioning, major version upgrades,
or re-deployment after disaster recovery.

> **Customization Note**: This guide documents Meridian's architecture and deployment order. You must customize
infrastructure details (cloud provider, DNS, TLS certificates, backup locations) for your specific environment.

## 1. Infrastructure Prerequisites

All infrastructure components must be provisioned and healthy before deploying Meridian services.

### 1.1 CockroachDB Cluster

CockroachDB is the primary database for all Meridian services.

**Requirements:**

- CockroachDB v23.1+ (PostgreSQL wire-compatible)
- Minimum 3-node cluster for production (multi-region recommended)
- TLS/SSL enabled with valid certificates
- Authentication enabled (username/password or client certificates)
- Port 26257 (SQL) and 8080 (Admin UI) accessible from the Kubernetes cluster

**Database-per-service architecture** -- each service uses a dedicated database:

| Database Name | Service |
|---------------|---------|
| `meridian_platform` | Shared platform (tenant provisioning) |
| `meridian_tenant` | Tenant service |
| `meridian_current_account` | Current Account service |
| `meridian_financial_accounting` | Financial Accounting service |
| `meridian_position_keeping` | Position Keeping service |
| `meridian_payment_order` | Payment Order service |
| `meridian_party` | Party service |
| `meridian_reference_data` | Reference Data service |
| `meridian_market_information` | Market Information service |
| `meridian_internal_account` | Internal Account service |

```bash
# Create databases (run from CockroachDB SQL shell)
cockroach sql --certs-dir=/certs --host=<cockroachdb-host>:26257 <<'SQL'
CREATE DATABASE IF NOT EXISTS meridian_platform;
CREATE DATABASE IF NOT EXISTS meridian_tenant;
CREATE DATABASE IF NOT EXISTS meridian_current_account;
CREATE DATABASE IF NOT EXISTS meridian_financial_accounting;
CREATE DATABASE IF NOT EXISTS meridian_position_keeping;
CREATE DATABASE IF NOT EXISTS meridian_payment_order;
CREATE DATABASE IF NOT EXISTS meridian_party;
CREATE DATABASE IF NOT EXISTS meridian_reference_data;
CREATE DATABASE IF NOT EXISTS meridian_market_information;
CREATE DATABASE IF NOT EXISTS meridian_internal_account;
SQL
```

**CockroachDB limitations** (see `CLAUDE.md` for details):

- No `LISTEN/NOTIFY` -- Meridian uses polling and outbox patterns instead
- No range types (`TSTZRANGE`) -- services use separate start/end columns
- Schema changes cannot run inside transactions

### 1.2 Apache Kafka Cluster

Kafka provides event streaming for asynchronous workflows and audit event delivery.

**Requirements:**

- Apache Kafka 3.9+ with KRaft mode (no ZooKeeper)
- Minimum 3-broker cluster for production
- TLS/SASL authentication configured
- Port 9092 (broker) accessible from the Kubernetes cluster

**Required topics** (create before deploying services):

```bash
# Audit event topics (one per domain service)
kafka-topics.sh --create --bootstrap-server <kafka-host>:9092 \
  --topic audit.events.current-account --partitions 12 --replication-factor 3
kafka-topics.sh --create --bootstrap-server <kafka-host>:9092 \
  --topic audit.events.financial-accounting --partitions 6 --replication-factor 3
kafka-topics.sh --create --bootstrap-server <kafka-host>:9092 \
  --topic audit.events.position-keeping --partitions 9 --replication-factor 3
kafka-topics.sh --create --bootstrap-server <kafka-host>:9092 \
  --topic audit.events.party --partitions 6 --replication-factor 3
kafka-topics.sh --create --bootstrap-server <kafka-host>:9092 \
  --topic audit.events.payment-order --partitions 12 --replication-factor 3
kafka-topics.sh --create --bootstrap-server <kafka-host>:9092 \
  --topic audit.events.tenant --partitions 6 --replication-factor 3

# Dead letter queue for failed audit events
kafka-topics.sh --create --bootstrap-server <kafka-host>:9092 \
  --topic audit.events.dlq --partitions 3 --replication-factor 3

# Position keeping transaction events
kafka-topics.sh --create --bootstrap-server <kafka-host>:9092 \
  --topic position-keeping.transaction-captured.v1 --partitions 12 --replication-factor 3
kafka-topics.sh --create --bootstrap-server <kafka-host>:9092 \
  --topic position-keeping.transaction-amended.v1 --partitions 6 --replication-factor 3
kafka-topics.sh --create --bootstrap-server <kafka-host>:9092 \
  --topic position-keeping.transaction-reconciled.v1 --partitions 6 --replication-factor 3

# Verify topics
kafka-topics.sh --list --bootstrap-server <kafka-host>:9092
```

Partition counts should be tuned based on expected throughput. Current Account and Payment Order handle
the highest transaction volumes.

### 1.3 Redis

Redis provides optional distributed idempotency and caching.

**Requirements:**

- Redis 7+ with authentication enabled
- TLS recommended for production
- Port 6379 accessible from the Kubernetes cluster
- Persistence enabled (`appendonly yes`)

**Services that use Redis:**

| Service | Purpose | Required |
|---------|---------|----------|
| Gateway | Tenant slug cache | Optional (degrades gracefully) |
| Tenant | Slug cache | Optional |
| Current Account | Idempotency keys | Recommended for multi-replica |
| Position Keeping | Idempotency keys | Recommended for multi-replica |
| Financial Accounting | Idempotency keys | Recommended for multi-replica |
| Payment Order | Idempotency keys | Recommended for multi-replica |
| Reference Data | Instrument cache | Optional |

Redis is not strictly required. Services degrade gracefully when Redis is unavailable, falling back
to per-instance idempotency.

### 1.4 Keycloak (Identity Provider)

Keycloak provides OAuth 2.0 / OpenID Connect authentication.

**Requirements:**

- Keycloak 26+ (or any OIDC-compliant provider)
- Production database backend (not embedded H2)
- HTTPS enabled with valid TLS certificate
- JWKS endpoint accessible from the Kubernetes cluster

**Keycloak configuration:**

1. Create realm: `meridian`
2. Create client: `meridian-service` (confidential, service account enabled)
3. Configure JWKS endpoint: `https://<keycloak-host>/realms/meridian/protocol/openid-connect/certs`
4. Define roles and scopes per your authorization requirements

### 1.5 Kubernetes Cluster

**Requirements:**

- Kubernetes 1.23+
- `kubectl` configured with cluster access
- Kustomize (built into kubectl 1.14+)
- Container registry accessible from the cluster
- Ingress controller for external traffic (NGINX, Traefik, etc.)
- TLS certificates for external endpoints

**Namespace setup:**

```bash
kubectl create namespace production
kubectl label namespace production name=production
```

### 1.6 Container Images

Build and push all service images to your container registry:

```bash
# Build all service images
# Each Dockerfile is at services/<service>/cmd/Dockerfile
# Images use multi-stage builds with distroless base (~2MB)

REGISTRY="your-registry.io/meridian"
VERSION="1.0.0"
COMMIT=$(git rev-parse --short HEAD)
BUILD_DATE=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

SERVICES=(
  "current-account"
  "financial-accounting"
  "position-keeping"
  "payment-order"
  "party"
  "tenant"
  "gateway"
  "internal-account"
  "market-information"
  "event-router"
)

# Note: reference-data is a library embedded in other services, not a standalone deployment.
# Note: audit-worker does not have a Dockerfile yet -- build via a root-level Dockerfile
# or add one before deploying.

for svc in "${SERVICES[@]}"; do
  docker build \
    --build-arg VERSION="${VERSION}" \
    --build-arg COMMIT="${COMMIT}" \
    --build-arg BUILD_DATE="${BUILD_DATE}" \
    -t "${REGISTRY}/${svc}:${VERSION}" \
    -f "services/${svc}/cmd/Dockerfile" .

  docker push "${REGISTRY}/${svc}:${VERSION}"
done
```

## 2. Initial Setup

### 2.1 Database Schema Migration

Each service manages its own schema using [Atlas](https://atlasgo.io/) migrations. Migrations must be
applied before starting services.

**Migration directory structure:**

```text
services/<service>/migrations/     # Service-specific migrations
shared/migrations/                 # Shared infrastructure (audit tables)
services/<service>/atlas/atlas.hcl # Atlas configuration per service
shared/atlas/atlas.hcl             # Shared Atlas configuration
```

**Apply shared migrations first, then per-service migrations:**

```bash
# 1. Apply shared migrations (audit infrastructure)
atlas migrate apply --env production \
  --config file://shared/atlas/atlas.hcl \
  --url "postgres://user:pass@<cockroachdb-host>:26257/meridian_platform?sslmode=verify-full"

# 2. Apply per-service migrations
SERVICES_WITH_DB=(
  "tenant"
  "party"
  "reference-data"
  "current-account"
  "financial-accounting"
  "position-keeping"
  "payment-order"
  "internal-account"
  "market-information"
)

for svc in "${SERVICES_WITH_DB[@]}"; do
  DB_NAME="meridian_$(echo $svc | tr '-' '_')"
  atlas migrate apply --env production \
    --config "file://services/${svc}/atlas/atlas.hcl" \
    --url "postgres://user:pass@<cockroachdb-host>:26257/${DB_NAME}?sslmode=verify-full"
done
```

**Verify migrations applied:**

```bash
for svc in "${SERVICES_WITH_DB[@]}"; do
  DB_NAME="meridian_$(echo $svc | tr '-' '_')"
  echo "=== ${svc} ==="
  atlas migrate status --env production \
    --config "file://services/${svc}/atlas/atlas.hcl" \
    --url "postgres://user:pass@<cockroachdb-host>:26257/${DB_NAME}?sslmode=verify-full"
done
```

### 2.2 Kubernetes Secrets

Create secrets for each service. Use External Secrets Operator or Sealed Secrets in production --
never commit real credentials to version control.

```bash
# Per-service database secrets
# Each service needs a DATABASE_URL pointing to its dedicated database
SERVICES_WITH_DB=(
  "tenant"
  "party"
  "reference-data"
  "current-account"
  "financial-accounting"
  "position-keeping"
  "payment-order"
  "internal-account"
  "market-information"
  "audit-worker"
)

for svc in "${SERVICES_WITH_DB[@]}"; do
  DB_NAME="meridian_$(echo $svc | tr '-' '_')"
  kubectl create secret generic "${svc}-db" \
    --namespace=production \
    --from-literal=DATABASE_URL="postgres://user:pass@cockroachdb:26257/${DB_NAME}?sslmode=verify-full"
done

# Gateway database secret (uses platform database for tenant lookups)
kubectl create secret generic gateway-db \
  --namespace=production \
  --from-literal=DATABASE_URL="postgres://user:pass@cockroachdb:26257/meridian_platform?sslmode=verify-full"

# Audit consumer secrets (one per domain service, each pointing to that service's database)
for svc in current-account financial-accounting position-keeping party payment-order tenant; do
  DB_NAME="meridian_$(echo $svc | tr '-' '_')"
  kubectl create secret generic "${svc}-audit-consumer-db" \
    --namespace=production \
    --from-literal=DATABASE_URL="postgres://user:pass@cockroachdb:26257/${DB_NAME}?sslmode=verify-full"
done
```

### 2.3 Kubernetes ConfigMaps

Service configuration is managed via Kustomize overlays. The production overlay at
`deployments/k8s/overlays/production/kustomization.yaml` sets production-appropriate values:

- `log_level=warn` (reduce log volume)
- `log_format=json` (structured logging for aggregation)
- `metrics_enabled=true`
- `ENVIRONMENT=production`
- `DEBUG_MODE=false`

Additional environment-specific configuration should be added to the production Kustomize overlay.

## 3. Service Deployment Order

Services must be deployed in dependency order. Wait for each tier's health checks to pass before
proceeding to the next tier.

### Service Dependency Map

```mermaid
flowchart TD
    subgraph Tier1["Tier 1: Platform Services (deploy first)"]
        Tenant["Tenant :50056"]
        Party["Party :50055"]
        RD["ReferenceData :50059"]
    end

    subgraph Tier2["Tier 2: Core Domain Services"]
        MI["MarketInformation :50058"]
        IBA["InternalAccount :50057"]
        PK["PositionKeeping :50053"]
        FA["FinancialAccounting :50052"]
    end

    subgraph Tier3["Tier 3: Orchestration Services"]
        CA["CurrentAccount :50051"]
        PO["PaymentOrder :50054"]
    end

    subgraph Tier4["Tier 4: Edge and Supporting Services"]
        GW["Gateway :8080"]
        AW["AuditWorker :8080"]
        ER["EventRouter :8080"]
        AC["AuditConsumers :8080"]
    end

    Tenant --> Party
    Tenant --> RD
    CA --> Party
    CA --> PK
    CA --> FA
    CA --> IBA
    CA --> RD
    PO --> CA
    GW --> CA
    GW --> PO
    GW --> Party
    GW --> MI
    ER --> PK
```

### Tier 1: Platform Services

These services have no dependencies on other Meridian services. They depend only on CockroachDB.

```bash
# Deploy Tenant service
kubectl apply -k deployments/k8s/overlays/production/
# Note: The base kustomization deploys the audit-worker.
# Service-specific kustomizations should be created or adjusted for each service.

# Verify Tenant is healthy before proceeding
kubectl wait --for=condition=Available deployment/tenant -n production --timeout=120s
```

**Deploy in this order:**

1. **Tenant** -- Manages multi-tenant isolation, provisions schemas for other services
2. **Party** -- Party reference data directory (Tenant optionally calls Party to register org parties)
3. **Reference Data** -- Instrument definitions (Tenant seeds system instruments during provisioning).
   Note: Reference Data does not yet have a standalone `main.go` or Dockerfile. Its handlers are
   currently embedded in other services. Deploy when a standalone entry point is added.

```bash
# Verify Tier 1 health
for svc in tenant party reference-data; do
  echo "=== ${svc} ==="
  kubectl get pods -n production -l app=${svc}
  # gRPC health check via grpcurl (install from https://github.com/fullstorydev/grpcurl)
  # grpcurl -plaintext <pod-ip>:<grpc-port> grpc.health.v1.Health/Check
done
```

### Tier 2: Core Domain Services

These services depend on Tier 1 services and CockroachDB. Some optionally depend on Kafka and Redis.

Deploy in this order:

1. **Market Information** (:50058) -- Price benchmarks and market data. No upstream service dependencies.
2. **Internal Account** (:50057) -- Counterparty and operational accounts. No upstream service dependencies.
3. **Position Keeping** (:50053) -- Financial position tracking. Optionally calls Reference Data for instrument
   lookup. Publishes transaction events to Kafka.
4. **Financial Accounting** (:50052) -- Ledger postings. Consumes transaction events from Position Keeping
   via Kafka.

```bash
# Deploy Tier 2 services
for svc in market-information internal-account position-keeping financial-accounting; do
  kubectl apply -k deployments/k8s/overlays/production/
  kubectl wait --for=condition=Available deployment/${svc} -n production --timeout=120s
done
```

### Tier 3: Orchestration Services

These services orchestrate workflows across multiple Tier 1 and Tier 2 services.

1. **Current Account** (:50051) -- The most connected service. Depends on:
   - Party (validate party exists)
   - Position Keeping (create position logs)
   - Financial Accounting (record ledger postings)
   - Internal Account (resolve clearing accounts, optional)
   - Reference Data (fungibility validation, optional)
   - Redis (idempotency, optional)
   - Kafka (outbox worker for audit events, optional)

2. **Payment Order** (:50054) -- Payment processing. Depends on:
   - Current Account (initiate liens for fund reservation)
   - Redis (idempotency, optional)
   - Kafka (outbox worker, optional)

```bash
# Deploy Tier 3 services
for svc in current-account payment-order; do
  kubectl apply -k deployments/k8s/overlays/production/
  kubectl wait --for=condition=Available deployment/${svc} -n production --timeout=120s
done
```

### Tier 4: Edge and Supporting Services

1. **Gateway** (:8080) -- API gateway. Routes external HTTP requests to backend gRPC services.
   Depends on CockroachDB (tenant lookups) and optionally Redis (tenant slug cache).

2. **Audit Worker** (:8080) -- Processes audit outbox entries when Kafka is unavailable
   (fallback path). Depends on CockroachDB only.

3. **Audit Consumers** -- Per-service Kafka consumers that write audit events to `audit_log` tables.
   One deployment per domain service.

4. **Event Router** (:8080) -- CEL-filtered saga dispatcher. Consumes domain events from Kafka
   and triggers saga workflows. Also handles platform billing via utilization metering.

```bash
# Deploy Gateway
kubectl apply -k deployments/k8s/overlays/production/
kubectl wait --for=condition=Available deployment/gateway -n production --timeout=120s

# Deploy Audit Worker
kubectl apply -k deployments/k8s/base/
kubectl wait --for=condition=Available deployment/audit-worker -n production --timeout=120s

# Deploy Audit Consumers (one per domain service)
for svc in current-account financial-accounting position-keeping party payment-order tenant; do
  kubectl apply -k deployments/k8s/audit-consumer/overlays/${svc}
done

# Deploy Event Router
kubectl apply -k services/event-router/k8s/
kubectl wait --for=condition=Available deployment/event-router -n production --timeout=120s
```

## 4. Health Check Verification

### 4.1 gRPC Health Checks

All gRPC services implement the standard `grpc.health.v1.Health` service.

```bash
# Install grpcurl if not available
# https://github.com/fullstorydev/grpcurl

# Check gRPC service health (from within the cluster)
SERVICES_GRPC=(
  "tenant:50056"
  "party:50055"
  "reference-data:50059"
  "market-information:50058"
  "internal-account:50057"
  "position-keeping:50053"
  "financial-accounting:50052"
  "current-account:50051"
  "payment-order:50054"
)

for entry in "${SERVICES_GRPC[@]}"; do
  SVC="${entry%%:*}"
  PORT="${entry##*:}"
  echo "=== ${SVC} ==="
  # From a debug pod within the cluster:
  grpcurl -plaintext "${SVC}.production.svc.cluster.local:${PORT}" \
    grpc.health.v1.Health/Check
done
```

**Expected output:**

```json
{
  "status": "SERVING"
}
```

### 4.2 HTTP Health Checks

HTTP services (Gateway, Audit Worker, Audit Consumers) expose health endpoints:

| Endpoint | Purpose |
|----------|---------|
| `/health/live` | Liveness probe -- is the process alive? |
| `/health/ready` | Readiness probe -- is the service ready to accept traffic? |
| `/health/startup` | Startup probe -- has the service finished initializing? |

```bash
# Check HTTP service health
HTTP_SERVICES=("gateway" "audit-worker")

for svc in "${HTTP_SERVICES[@]}"; do
  POD=$(kubectl get pod -n production -l app=${svc} -o jsonpath='{.items[0].metadata.name}')
  echo "=== ${svc} ==="
  kubectl exec -n production "${POD}" -- wget -qO- http://localhost:8080/health/ready 2>/dev/null \
    || echo "Health check via exec not available (distroless image)"
  # Alternative: port-forward and curl
  # kubectl port-forward -n production "${POD}" 8080:8080 &
  # curl http://localhost:8080/health/ready
done
```

### 4.3 Database Connectivity

Verify each service can reach its database:

```bash
# Check pod logs for successful database connection
for svc in tenant party reference-data current-account financial-accounting \
           position-keeping payment-order internal-account market-information; do
  echo "=== ${svc} ==="
  kubectl logs -n production -l app=${svc} --tail=20 | grep -i "database"
done
```

**Expected log message:**

```json
{"level":"INFO","msg":"database connection established"}
```

### 4.4 Kafka Consumer Lag

Monitor Kafka consumer lag to ensure audit consumers are keeping up:

```bash
# Check consumer group lag
kafka-consumer-groups.sh --bootstrap-server <kafka-host>:9092 \
  --describe --all-groups

# Per-service consumer group check
for svc in current-account financial-accounting position-keeping party payment-order tenant; do
  echo "=== ${svc}-audit-consumer ==="
  kafka-consumer-groups.sh --bootstrap-server <kafka-host>:9092 \
    --describe --group "${svc}-audit-consumer"
done
```

**Healthy state**: LAG values should be 0 or near 0 under normal load.

### 4.5 Smoke Test

Run a basic end-to-end test after deployment:

```bash
# 1. Register a test tenant via tenantctl
go build -o tenantctl ./cmd/tenantctl
./tenantctl register \
  --id=smoke_test_tenant \
  --name="Smoke Test" \
  --settlement-asset=GBP \
  --service-url=tenant.production.svc.cluster.local:50056

# 2. Verify tenant was created
./tenantctl get smoke_test_tenant \
  --service-url=tenant.production.svc.cluster.local:50056

# 3. Provision default internal accounts via ibactl
go build -o ibactl ./cmd/ibactl
./ibactl provision-defaults smoke_test_tenant \
  --service-url=internal-account.production.svc.cluster.local:50057 \
  --tenant-service-url=tenant.production.svc.cluster.local:50056

# 4. Test Gateway health endpoint (external)
curl -s https://<gateway-external-url>/health/ready

# 5. Clean up test tenant
./tenantctl deprovision smoke_test_tenant --confirm \
  --service-url=tenant.production.svc.cluster.local:50056
```

## 5. Rollback Procedure

### 5.1 Service Rollback

Kubernetes Deployments support rolling back to previous revisions:

```bash
# View deployment history
kubectl rollout history deployment/<service-name> -n production

# Rollback to previous version
kubectl rollout undo deployment/<service-name> -n production

# Rollback to specific revision
kubectl rollout undo deployment/<service-name> -n production --to-revision=<N>

# Verify rollback
kubectl rollout status deployment/<service-name> -n production
```

**Rollback order** (reverse of deployment order):

1. Tier 4: Gateway, Audit Consumers, Audit Worker, Event Router
2. Tier 3: Payment Order, Current Account
3. Tier 2: Financial Accounting, Position Keeping, Internal Account, Market Information
4. Tier 1: Reference Data, Party, Tenant

### 5.2 Database Migration Rollback

Atlas migrations support downgrade operations:

```bash
# View migration status
atlas migrate status --env production \
  --config "file://services/<service>/atlas/atlas.hcl" \
  --url "<DATABASE_URL>"

# Rollback last migration
atlas migrate down --env production \
  --config "file://services/<service>/atlas/atlas.hcl" \
  --url "<DATABASE_URL>"
```

**Precautions:**

- Always back up the database before rolling back migrations
- Some migrations (data migrations, column drops) are not reversible
- Atlas lint rules (`destructive`, `data_depend`, `incompatible`) catch dangerous changes in CI
- Test rollback procedures in staging before applying to production

### 5.3 Kafka Consumer Offset Reset

If audit consumers need to reprocess events (after a bug fix or data corruption):

```bash
# Stop the consumer deployment first
kubectl scale deployment <service>-audit-consumer -n production --replicas=0

# Reset to earliest offset (reprocess all retained messages)
kafka-consumer-groups.sh --bootstrap-server <kafka-host>:9092 \
  --group <service>-audit-consumer \
  --topic audit.events.<service> \
  --reset-offsets --to-earliest --execute

# Or reset to specific timestamp
kafka-consumer-groups.sh --bootstrap-server <kafka-host>:9092 \
  --group <service>-audit-consumer \
  --topic audit.events.<service> \
  --reset-offsets --to-datetime 2025-01-15T00:00:00.000 --execute

# Restart the consumer
kubectl scale deployment <service>-audit-consumer -n production --replicas=<previous-count>
```

## 6. Service Port Reference

| Service | gRPC Port | HTTP Port | Metrics Port | Kubernetes Service Name |
|---------|-----------|-----------|--------------|------------------------|
| Tenant | 50056 | - | 9090 | `tenant` |
| Party | 50055 | - | 9090 | `party` |
| Reference Data | 50059 | - | 9090 | `reference-data` |
| Market Information | 50058 | - | 9090 | `market-information` |
| Internal Account | 50057 | - | 9090 | `internal-account` |
| Position Keeping | 50053 | - | 9090 | `position-keeping` |
| Financial Accounting | 50052 | - | 9090 | `financial-accounting` |
| Current Account | 50051 | - | 9090 | `current-account` |
| Payment Order | 50054 | 8080 | 9090 | `payment-order` |
| Gateway | - | 8080 | 8080 | `gateway` |
| Audit Worker | - | 8080 | 8080 | `audit-worker` |
| Event Router | - | 8080 | 8080 | `event-router` |

All gRPC services use Kubernetes DNS for service discovery:
`<service-name>.<namespace>.svc.cluster.local:<port>`

## 7. Security Checklist

Before accepting production traffic, verify:

- [ ] All containers run as non-root (UID 65532, distroless base)
- [ ] Read-only root filesystem enabled on all pods
- [ ] All Linux capabilities dropped (`DROP ALL`)
- [ ] Privilege escalation blocked (`allowPrivilegeEscalation: false`)
- [ ] Network policies applied (restrict ingress/egress per service)
- [ ] TLS enabled on CockroachDB connections (`sslmode=verify-full`)
- [ ] Kafka SASL/TLS authentication configured
- [ ] Redis authentication enabled (`requirepass`)
- [ ] Keycloak HTTPS configured with valid certificates
- [ ] Gateway `AUTH_ENABLED=true` with valid JWKS URL
- [ ] Gateway `LOCAL_DEV_MODE=false` (enforced by OPA policy in production namespace)
- [ ] Kubernetes secrets managed via External Secrets Operator or Sealed Secrets
- [ ] Pod security standards enforced (restricted profile)
- [ ] Ingress TLS termination configured

## Related Documentation

- [Disaster Recovery](disaster-recovery.md) -- Backup restoration and failover procedures
- [Incident Response](incident-response.md) -- Production incident handling
- [Saga Failure Recovery](saga-failure-recovery.md) -- Recovering from failed saga executions
- [Services Architecture](../../services/README.md) -- Service dependency map and communication protocols
- [Kubernetes Manifests](../../deployments/k8s/) -- Base and overlay Kustomize configurations
