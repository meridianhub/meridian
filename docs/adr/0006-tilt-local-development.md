---
name: adr-006-tilt-local-development
description: Use Tilt for fast local Kubernetes development with live reload and dependency management
triggers:

  - Setting up local development environment
  - Debugging microservices locally
  - Rapid iteration on code changes
  - Managing local service dependencies

instructions: |
  Use Tilt for local dev. Tiltfile defines all services and dependencies. Tilt provides
  live reload (<5s feedback), logs aggregation, and status UI. Matches production K8s
  patterns. Handles CockroachDB, Redis, Kafka automatically.
---

# 6. Tilt for Local Kubernetes Development

Date: 2025-10-25

## Status

Accepted

## Context

Developers need a fast, productive local development environment for building microservices. The environment should:

* Support rapid iteration cycles (< 5 second feedback loop)
* Match production deployment patterns (Kubernetes)
* Handle complex service dependencies (CockroachDB, Redis, Kafka)
* Provide excellent observability (logs, status, debugging)
* Be easy to onboard new team members (< 5 minutes to running system)

The project deploys to **Kubernetes in production**, using:

* Kubernetes manifests in `deployments/k8s/base/`
* Kustomize for environment-specific overlays
* StatefulSets for stateful services (CockroachDB, Kafka)

**The question:** How do developers run and iterate on this stack locally?

## Decision Drivers

* **Production parity** - Dev environment should match production as closely as possible
* **Fast feedback loops** - Changes should be visible in < 5 seconds, not 30-60 seconds
* **Kubernetes-native** - Production uses K8s, dev should too (avoid "works on my machine")
* **Developer experience** - Single command to start everything, unified observability
* **Microservices complexity** - Need to orchestrate 6+ services with dependencies
* **Team onboarding** - New developers should get up and running quickly
* **Cloud-native patterns** - Test K8s features locally (readiness probes, resource limits, affinity)

## Considered Options

1. **Tilt** - Kubernetes development environment with live reload
2. **Docker Compose** - Traditional multi-container orchestration
3. **Manual kubectl** - Raw Kubernetes manifests with manual rebuilds
4. **Skaffold** - Alternative Kubernetes dev tool from Google

## Decision Outcome

Chosen option: **"Tilt"**, because:

* **Production parity**: Uses same Kubernetes manifests as production (no translation layer)
* **Fast feedback**: Live reload with incremental builds (2-3 seconds vs 30-60 seconds)
* **Superior DX**: Unified UI for logs, status, and debugging across all services
* **Intelligent builds**: File syncing and incremental compilation without full container rebuilds
* **Dependency management**: Automatic resource ordering with readiness checks
* **Team-proven**: Widely adopted in cloud-native organizations

### Positive Consequences

* ✅ **Fast iteration**: Edit Go code → 2-3 seconds → changes live in K8s
* ✅ **Production parity**: Same K8s manifests, same networking, same behavior
* ✅ **Unified observability**: All logs, status, and errors in one UI
* ✅ **Easy onboarding**: `tilt up` → full stack running in < 5 minutes
* ✅ **Catch K8s issues early**: Test readiness probes, resource limits, networking locally
* ✅ **Resource organization**: Label-based grouping (app, database, messaging, tests)
* ✅ **Parallel operations**: Tests run while services start
* ✅ **Manual triggers**: Run linters on-demand without slowing startup

### Negative Consequences

* ❌ **Learning curve**: Developers must understand Kubernetes basics
* ❌ **Local K8s required**: Need local Kubernetes cluster
* ❌ **Resource usage**: K8s overhead (~1-2GB RAM) vs plain Docker
* ❌ **Complexity for simple projects**: Overkill if not using K8s in production

**Mitigation:**

* Use **Kind + ctlptl** for fast, reproducible local clusters
* Provide comprehensive onboarding docs ([docs/skills/tilt.md](../skills/tilt.md))
* Include setup automation script (`scripts/doctor.sh`)
* Single command cluster creation with local registry: `ctlptl create cluster kind --registry=ctlptl-registry
--name=kind-meridian-local`
* Tiltfile comments explain each section
* Startup banner shows all service URLs

## Pros and Cons of the Options

### Tilt - Kubernetes development environment

<https://tilt.dev/>

* Good, because **production parity** - same K8s manifests in dev and prod
* Good, because **fast live reload** - incremental builds in 2-3 seconds
* Good, because **unified UI** - all logs and status in one place
* Good, because **smart file syncing** - only syncs changed files, no full rebuild
* Good, because **dependency management** - services start in correct order with health checks
* Good, because **resource organization** - labels group related services
* Good, because **extensibility** - Python DSL allows custom workflows
* Good, because **widely adopted** - used by major cloud-native companies
* Bad, because **requires local Kubernetes** - more setup than Docker alone
* Bad, because **learning curve** - team must understand K8s concepts
* Bad, because **resource overhead** - K8s control plane uses ~1-2GB RAM

### Docker Compose - Multi-container orchestration

<https://docs.docker.com/compose/>

* Good, because **simple** - easy YAML, low learning curve
* Good, because **lightweight** - no K8s overhead, just Docker daemon
* Good, because **fast onboarding** - most developers know Docker Compose
* Good, because **good for simple stacks** - works well for < 5 services
* Bad, because **no production parity** - production uses K8s, dev uses Compose (different behaviors)
* Bad, because **slow rebuilds** - full container rebuild on code changes (30-60 seconds)
* Bad, because **limited dependency management** - `depends_on` only waits for start, not readiness
* Bad, because **different mental model** - developers learn Compose for dev, K8s for prod
* Bad, because **can't test K8s features** - readiness probes, resource limits, affinity rules
* Bad, because **scaling issues** - complex microservices stacks become unwieldy

### Manual kubectl - Raw Kubernetes

* Good, because **full control** - no abstraction layer
* Good, because **production parity** - identical to production
* Bad, because **extremely slow** - manual rebuilds, manual deployments (minutes per change)
* Bad, because **no hot reload** - must rebuild and redeploy for every change
* Bad, because **poor DX** - no unified logs, manual port-forwarding, scattered commands
* Bad, because **error-prone** - easy to forget steps, manual cleanup required

### Skaffold - Alternative K8s dev tool

<https://skaffold.dev/>

* Good, because **Kubernetes-native** - production parity like Tilt
* Good, because **Google-backed** - well-maintained, integrated with GKE
* Good, because **CI/CD friendly** - designed for pipelines
* Bad, because **less polished DX** - no unified UI (terminal only)
* Bad, because **slower iteration** - not as optimized for hot reload as Tilt
* Bad, because **less observability** - requires separate tools for logs/debugging
* Bad, because **more configuration** - requires more YAML for similar functionality

## Implementation

### Project Structure

```text
meridian/
├── Tiltfile                        # Tilt configuration (Python DSL)
├── deployments/
│   └── k8s/base/                   # Kubernetes manifests (used by Tilt)
├── docs/
│   ├── tilt.md                     # Tilt usage guide
│   └── docker.md                   # Docker troubleshooting
└── scripts/
    └── doctor.sh                   # Unified setup/verification script
```

### Tiltfile Configuration

**Key features of our Tiltfile:**

#### 1. Live Reload for Go Services

```python
docker_build(
  'ghcr.io/meridianhub/meridian',
  context='.',
  dockerfile='Dockerfile',
  live_update=[

    # Sync Go source files instantly

    sync('./cmd', '/app/cmd'),
    sync('./internal', '/app/internal'),
    sync('./pkg', '/app/pkg'),

    # Incremental rebuild (2-3 seconds)

    run(
      'cd /app && go build -o meridian ./cmd/meridian',
      trigger=['./cmd', './internal', './pkg'],
    ),

    # Restart service (no container rebuild)

    restart_container(),
  ],
)
```

**Result:** Edit Go file → 2-3 seconds → changes live

#### 2. Resource Dependencies

```python
k8s_resource(
  'meridian',
  resource_deps=[
    'cockroachdb',     # Wait for database
    'redis',           # Wait for cache
    'kafka-cluster',   # Wait for Kafka cluster (3 brokers)
  ],
)
```

Ensures services start in correct order, waits for health checks.

#### 3. Backing Services

**CockroachDB** (StatefulSet with persistent storage):

```python
k8s_yaml('''
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: cockroachdb
spec:
  serviceName: cockroachdb
  replicas: 1

  # ... (matches production pattern)

''')
```

**Kafka** (3-broker StatefulSet with KRaft):

```python

# Multi-broker Kafka cluster (no Zookeeper - uses KRaft consensus)

k8s_yaml(blob('''
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: kafka
spec:
  serviceName: kafka-headless
  replicas: 3  # Minimum for quorum

  # ... (KRaft quorum configuration)

'''))
```

**Redis** (standard deployment):

```python
k8s_yaml('''
apiVersion: apps/v1
kind: Deployment
metadata:
  name: redis

# ... (production-like config)

''')
```

#### 4. Development Helpers

**Automatic testing:**

```python
local_resource(
  'test',
  cmd='make test',
  deps=['./cmd', './internal', './pkg'],
  labels=['tests'],
  allow_parallel=True,  # Runs while services start
)
```

**On-demand linting:**

```python
local_resource(
  'lint',
  cmd='make lint',
  deps=['./cmd', './internal', './pkg'],
  labels=['quality'],
  auto_init=False,  # Run manually: 'tilt trigger lint'
)
```

#### 5. Resource Organization

```python
k8s_resource('meridian', labels=['app'])
k8s_resource('cockroachdb', labels=['database'])
k8s_resource('redis', labels=['cache'])
k8s_resource('kafka-cluster', labels=['messaging'])
```

**Tilt UI groups by label:**

```text
┌─ app ──────────────────────┐
│ ✓ meridian                 │
├─ database ─────────────────┤
│ ✓ cockroachdb              │
├─ cache ────────────────────┤
│ ✓ redis                    │
├─ messaging ────────────────┤
│ ✓ kafka-cluster (3 pods)   │
├─ tests ────────────────────┤
│ ✓ test (passed)            │
├─ quality ──────────────────┤
│ ○ lint (manual)            │
└────────────────────────────┘
```

#### 6. Port Forwarding

```python
k8s_resource(
  'meridian',
  port_forwards=[
    '8080:8080',  # HTTP API
    '9090:9090',  # gRPC API
  ],
)

k8s_resource('cockroachdb', port_forwards='26257:26257')
k8s_resource('redis', port_forwards='6379:6379')
k8s_resource('kafka-cluster', port_forwards='9092:9092')  # Forwards to kafka-0
```

**Automatic** - no manual `kubectl port-forward` needed!

**Note:** Port forwarding connects to kafka-0 (first broker). All 3 brokers (kafka-0, kafka-1, kafka-2) are accessible
within the cluster via the headless service.

### Developer Workflow

#### Initial Setup (One-Time)

```bash

# 1. Verify prerequisites and auto-fix issues

./scripts/doctor.sh --fix

# 3. Create local Kubernetes cluster with local registry (recommended: Kind with ctlptl)

ctlptl create cluster kind --registry=ctlptl-registry --name=kind-meridian-local

# Verify cluster is ready

kubectl cluster-info
```

#### Daily Development

```bash

# Start everything

tilt up

# Tilt UI opens at: http://localhost:10350

# Wait for all resources to be green (ready)

# Services available at:

#   - Meridian API:  http://localhost:8080

#   - Meridian gRPC: localhost:9090

#   - CockroachDB:   localhost:26257

#   - Redis:         localhost:6379

#   - Kafka Cluster: localhost:9092 (3 brokers: kafka-0, kafka-1, kafka-2)

# Make code changes

vim internal/domain/booking_log.go

# Changes automatically:

#   1. Sync to container (instant)

#   2. Rebuild binary (2-3 seconds)

#   3. Restart service

#   4. Logs appear in Tilt UI

# View logs for specific service

tilt logs meridian

# Run tests manually (auto-runs on file changes)

tilt trigger test

# Run linters on-demand

tilt trigger lint

# Stop everything (clean shutdown)

tilt down
```

**Time from code change to running: ~2-3 seconds** 🚀

### Performance Optimizations

#### File Watching

Tilt watches files and only syncs changes:

```python
sync('./internal', '/app/internal')  # Only changed files sync
```

#### Incremental Builds

```python
run('go build -o meridian ./cmd/meridian')
```

Go compiler caches unchanged packages → fast rebuilds.

#### Parallel Operations

```python
update_settings(max_parallel_updates=3)
```

Up to 3 resources build simultaneously.

#### Smart Triggers

```python
run(..., trigger=['./cmd', './internal', './pkg'])
```

Only rebuild when relevant directories change.

### Troubleshooting

See comprehensive troubleshooting guide in [docs/skills/tilt.md](../skills/tilt.md):

* Resources not starting → Check K8s cluster
* Slow builds → Clear Tilt cache
* Port conflicts → Kill processes using ports
* Database connection issues → Check CockroachDB logs
* Kafka not connecting → Check all 3 broker pods are ready (kubectl get pods -l app=kafka)

### CI/CD Integration

Tilt can run in CI for integration testing:

```bash

# Non-interactive mode

tilt ci

# Test specific resources

tilt ci meridian test
```

Useful for pre-merge integration tests in GitHub Actions.

## Comparison with Production

### What's the Same (Production Parity)

✅ **Kubernetes manifests** - Exact same YAML from `deployments/k8s/base/`
✅ **Kustomize** - Same tool for environment overlays
✅ **StatefulSets** - CockroachDB and Kafka use StatefulSets (like prod)
✅ **Services & networking** - Same ClusterIP services, DNS resolution
✅ **Resource limits** - Can test CPU/memory constraints
✅ **Readiness probes** - Tests service health checks
✅ **Volume mounts** - CockroachDB uses PVC (like prod)

### What's Different (Local Simplifications)

⚠️ **Replicas** - Single replicas (prod has 3+)
⚠️ **Security** - CockroachDB runs insecure (prod uses TLS)
⚠️ **Ingress** - No Ingress controller (use port-forwards)
⚠️ **Secrets** - Plain ConfigMaps (prod uses sealed secrets)
⚠️ **Persistence** - Local volumes (prod uses cloud storage)
⚠️ **Monitoring** - No Prometheus/Grafana (add if needed)
⚠️ **Service mesh** - No Istio/Linkerd (add if needed)

**Philosophy:** Match production patterns, simplify where safe for dev.

## Team Onboarding

### For Developers New to Kubernetes

**Learning path:**

1. Read [CONTRIBUTING.md](../../CONTRIBUTING.md) - Prerequisites section
2. Run `./scripts/doctor.sh` - Verify environment
3. Follow [docs/skills/tilt.md](../skills/tilt.md) - Quick start guide
4. Watch Tilt UI to understand resource dependencies
5. Learn basic kubectl commands (`get pods`, `logs`, `describe`)

**Estimated time to productivity:** 1-2 hours (including K8s setup)

### For Developers Familiar with Docker Compose

**Mental model shift:**

```text
Docker Compose              Tilt + Kubernetes
--------------              -----------------
docker-compose.yml    →     Tiltfile + K8s manifests
docker-compose up     →     tilt up
docker-compose logs   →     Tilt UI (http://localhost:10350)
docker-compose ps     →     kubectl get pods
docker-compose restart →    tilt restart <resource>
volumes: ./code       →     live_update (smarter sync)
depends_on            →     resource_deps (with health checks)
```

**Key difference:** Tilt uses real Kubernetes, not just Docker containers.

### Setup Automation

Provided scripts make onboarding easy:

```bash

# Check if environment is ready

./scripts/doctor.sh

# Output:

# ✓ Go 1.25.7 installed

# ✓ Docker running

# ✓ kubectl installed

# ✓ kind installed

# ✓ ctlptl installed

# ✓ Tilt v0.36.0 installed

# ✗ Kubernetes cluster not accessible

#

# ACTION REQUIRED: Create a local cluster

# ctlptl create cluster kind --registry=ctlptl-registry --name=kind-meridian-local

# Auto-fix all issues

./scripts/doctor.sh --fix

# Create Kind cluster with local registry

ctlptl create cluster kind --registry=ctlptl-registry --name=kind-meridian-local

# Start development

tilt up
```

## Local Kubernetes with Kind + ctlptl

### Why Kind + ctlptl?

We use **Kind (Kubernetes in Docker)** with **ctlptl (Cattle Patrol)** for local cluster management because:

* ✅ **Fast cluster creation** - Clusters spin up in ~30 seconds
* ✅ **Reproducible** - Same cluster config across all developers
* ✅ **Registry integration** - Built-in local registry support
* ✅ **Tilt optimized** - ctlptl configures clusters specifically for Tilt
* ✅ **Lightweight** - Runs entirely in Docker containers
* ✅ **Easy cleanup** - `ctlptl delete cluster kind-meridian-local` removes everything

### Cluster Creation

```bash

# Create cluster optimized for Tilt with local registry

ctlptl create cluster kind --registry=ctlptl-registry --name=kind-meridian-local

# This automatically:

# - Creates a Kind cluster

# - Sets up local registry (for faster image pushes)

# - Configures port mappings

# - Optimizes for development workflows

```

### Cluster Management

```bash

# List clusters

ctlptl get clusters

# Delete cluster

ctlptl delete cluster kind-meridian-local

# Verify cluster is working

kubectl config use-context kind-meridian-local
kubectl get nodes
```

### Why Not Docker Desktop Kubernetes?

Docker Desktop Kubernetes works but:

* Slower to start/restart
* Less reproducible across team
* No registry integration
* Harder to reset to clean state
* Kind + ctlptl is more predictable

**Recommendation**: Use Kind + ctlptl for local development, Docker Desktop K8s as fallback option.

## When to Use Tilt vs Other Tools

### Use Tilt when

* ✅ Production runs on Kubernetes
* ✅ Multiple microservices with dependencies
* ✅ Need fast iteration (hot reload critical)
* ✅ Team comfortable with or learning Kubernetes
* ✅ Want production parity in dev

### Use Docker Compose when

* ✅ Production uses Docker Compose (rare)
* ✅ Simple 2-3 service stacks
* ✅ Team unfamiliar with K8s and no plans to adopt
* ✅ Rapid prototyping / throwaway projects
* ✅ K8s features not needed

### Use Manual kubectl when

* ✅ Debugging K8s-specific issues
* ✅ One-off testing of K8s features
* ❌ Not for daily development (too slow)

### Use Skaffold when

* ✅ Need CI/CD integration (Skaffold is more pipeline-focused)
* ✅ GKE deployment (tight integration)
* ❌ If team values DX and unified UI (Tilt is better here)

## Alternatives Considered

### Why Not Docker Compose?

**Evaluated docker-compose.yml approach:**

```yaml

# docker-compose.yml

version: '3.8'
services:
  cockroachdb:
    image: cockroachdb/cockroach:v23.1.11
    command: start-single-node --insecure
    ports:

      - "26257:26257"

    # Problem: Not a StatefulSet, different from prod

  meridian:
    build: .
    volumes:

      - ./cmd:/app/cmd
      - ./internal:/app/internal

    # Problem: Full rebuild on changes (30-60s)

    # Problem: No K8s readiness probes

    depends_on:

      - cockroachdb

    # Problem: Only waits for start, not readiness

```

**Rejected because:**

* ❌ **No production parity** - Prod uses K8s, Compose is completely different
* ❌ **Slow feedback** - Full container rebuilds take 30-60 seconds
* ❌ **Different networking** - No K8s Services, different DNS
* ❌ **Can't test K8s features** - Readiness probes, resource limits, etc. don't exist
* ❌ **Dual mental models** - Learn Compose for dev, K8s for prod

### Why Not Skaffold?

Skaffold is excellent but:

* Less polished developer experience (no unified UI)
* More pipeline-focused, less dev-focused
* Slower hot reload compared to Tilt
* Tilt's UI is superior for multi-service debugging

Both are valid choices; Tilt won on developer experience.

## Links

* [Tilt Documentation](https://docs.tilt.dev/)
* [Tilt Best Practices](https://docs.tilt.dev/best_practices.html)
* [Live Update Reference](https://docs.tilt.dev/live_update_reference.html)
* [Tiltfile API Reference](https://docs.tilt.dev/api.html)
* [Why Tilt? (Blog)](https://blog.tilt.dev/2019/04/09/designing-a-better-interface-for-microservices-development.html)
* [Tilt vs Skaffold Comparison](https://docs.tilt.dev/choosing_a_local_dev_solution.html)
* [Local Kubernetes Guide](../skills/tilt.md)
* [ADR-0001: Record Architecture Decisions](./0001-record-architecture-decisions.md)

## Notes

### Resource Usage

**Typical local development resource usage:**

* Kubernetes control plane: ~1GB RAM
* CockroachDB: ~1-2GB RAM
* Kafka cluster (3 brokers): ~1.5GB RAM
* Redis: ~128MB RAM
* Meridian service: ~256MB RAM
* **Total: ~4-5GB RAM**

**Minimum recommended:** 12GB RAM (may experience swapping with multiple applications)
**Comfortable development:** 16GB RAM (recommended for daily use)

**Note on Kafka**: Multi-broker setup uses ~1.5GB total (384Mi per broker × 3) compared to previous single-broker
~512MB. The increased resource usage enables realistic testing of partition replication, leader election, and failover
scenarios.

**For 8GB RAM machines**: The 3-broker setup may cause resource pressure. Options:

1. Close unnecessary applications (IDEs, browsers, etc.)
2. Use Colima instead of Docker Desktop (lighter overhead)
3. Modify Tiltfile for single-broker mode (see Tiltfile comments around line 208)

### Startup Time

**Cold start** (nothing cached):

* First `tilt up`: ~2-3 minutes (downloads images, builds service)
* Subsequent starts: ~30-60 seconds (uses cached images)

**Warm start** (Tilt already running):

* Code change → live: ~2-3 seconds

### Cloud Development

Tilt can connect to remote K8s clusters:

```python

# Tiltfile

allow_k8s_contexts(['gke_my-project_us-central1_my-cluster'])
```

Enables cloud-based development if local resources are limited.

### Future Enhancements

Potential additions to Tiltfile:

* **Delve debugger** integration (port 2345)
* **Prometheus + Grafana** for local observability
* **Jaeger** for distributed tracing
* **Istio/Linkerd** service mesh (when testing mesh features)

### Maintenance Considerations

* **Tilt updates** - Check for new versions quarterly
* **Helm chart versions** - Pin versions in values files, update deliberately
* **Kubernetes version** - Test against same version as production
* **Resource limits** - Adjust based on developer machine specs
