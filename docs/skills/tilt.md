---
name: skill-tilt-development
description: Fast Kubernetes development workflow using Tilt with live reload
triggers:
  - Starting local development environment
  - Setting up Tilt for the first time
  - Debugging Tilt issues
  - Configuring local registry for faster builds
instructions: |
  Use Tilt for local Kubernetes development with fast rebuilds and live reload.
  Create Kind cluster with local registry using ctlptl for optimal performance.
  Access Tilt UI at http://localhost:10350 to monitor all services.
---

# Tilt Development Guide

This guide covers using Tilt for fast Kubernetes development with Meridian.

## Prerequisites

1. **Kubernetes Cluster** (one of):
   - Docker Desktop with Kubernetes enabled
   - kind (Kubernetes in Docker) with local registry (recommended):
     ```bash
     # Install ctlptl first: brew install tilt-dev/tap/ctlptl
     ctlptl create cluster kind --registry=ctlptl-registry --name=kind-meridian-local
     ```
     Or without registry: `kind create cluster`
   - Minikube: `minikube start`
   - Colima: `colima start --kubernetes`

2. **Tilt**: Install from https://tilt.dev/
   ```bash
   # macOS
   brew install tilt-dev/tap/tilt

   # Linux
   curl -fsSL https://raw.githubusercontent.com/tilt-dev/tilt/master/scripts/install.sh | bash
   ```

3. **kubectl**: Kubernetes CLI
   ```bash
   brew install kubectl
   ```

4. **Helm**: Package manager for Kubernetes
   ```bash
   brew install helm
   ```

## Quick Start

### 1. Start Tilt

```bash
# From repository root
tilt up
```

Tilt will:
- Deploy CockroachDB, Redis, and Kafka
- Build and deploy the Meridian service
- Set up port forwarding for all services
- Enable live reload for Go code changes

### 2. Access Services

Once all resources are green in the Tilt UI:

- **Tilt UI**: http://localhost:10350
- **Meridian HTTP API**: http://localhost:8080
- **Meridian gRPC API**: localhost:9090
- **CockroachDB SQL**: localhost:26257
- **Redis**: localhost:6379
- **Kafka**: localhost:9092

### 3. Development Workflow

Edit any Go file in `cmd/`, `internal/`, or `pkg/`:

```bash
# Make changes to Go code
vim internal/server/server.go

# Tilt automatically:
# 1. Syncs files to container
# 2. Rebuilds binary (typically < 3 seconds)
# 3. Restarts the service
# 4. Shows logs in real-time
```

### 4. Run Tests

Tests run automatically on file changes:

```bash
# Manually trigger tests in Tilt UI or:
tilt trigger test
```

### 5. Run Linters

Linters are available but don't run automatically:

```bash
# Trigger linting manually
tilt trigger lint
```

## Resource Labels

Resources are organized with labels in the Tilt UI:

- **app**: Main application (Meridian)
- **database**: CockroachDB
- **cache**: Redis
- **messaging**: Kafka
- **tests**: Test runner
- **quality**: Linter

## Service Details

### CockroachDB

Single-node insecure cluster for local development:

```bash
# Connect with SQL client
cockroach sql --insecure --host=localhost:26257

# Or via kubectl
kubectl exec -it cockroachdb-0 -- cockroach sql --insecure
```

**Features**:
- 10Gi persistent volume for data
- Admin UI available at http://localhost:8080 (when port-forwarded)
- No authentication required (insecure mode)

### Redis

Standard Redis 7 with append-only file persistence:

```bash
# Connect with redis-cli
redis-cli -h localhost -p 6379

# Or via kubectl
kubectl exec -it deployment/redis -- redis-cli
```

**Features**:
- AOF persistence enabled
- No authentication required
- Default configuration

### Kafka

3-broker Kafka cluster using KRaft consensus for event streaming:

```bash
# List topics
kafka-topics.sh --bootstrap-server localhost:9092 --list

# Create topic
kafka-topics.sh --bootstrap-server localhost:9092 \
  --create --topic test --partitions 3 --replication-factor 1

# Consume messages
kafka-console-consumer.sh --bootstrap-server localhost:9092 \
  --topic test --from-beginning
```

**Features**:
- 3-broker cluster (kafka-0, kafka-1, kafka-2)
- KRaft mode (no Zookeeper dependency)
- Auto-create topics enabled
- Replication factor 2 for high availability
- No authentication required

## Tilt Commands

### Start Development

```bash
# Start with UI (recommended)
tilt up

# Start without opening browser
tilt up --stream

# Start specific resources only
tilt up meridian redis
```

### Manage Resources

```bash
# Rebuild specific resource
tilt trigger meridian

# Disable resource temporarily
tilt disable kafka

# Enable resource
tilt enable kafka

# Restart resource
tilt restart meridian
```

### View Logs

```bash
# Follow logs for a resource
tilt logs meridian

# View logs in UI (recommended)
# Navigate to http://localhost:10350
```

### Stop Development

```bash
# Stop Tilt and clean up resources
tilt down

# Keep resources running
# Just press Ctrl+C to exit Tilt
```

## Troubleshooting

### Resources Not Starting

Check Kubernetes cluster is running:
```bash
kubectl cluster-info
kubectl get nodes
```

### Slow Builds

Clear Tilt build cache:
```bash
tilt down
tilt up --clear-build-cache
```

### Port Already in Use

Check for conflicting services:
```bash
# macOS/Linux
lsof -i :8080
lsof -i :9090

# Kill process using port
kill -9 <PID>
```

### Database Connection Issues

Verify CockroachDB is ready:
```bash
kubectl get pods -l app=cockroachdb
kubectl logs statefulset/cockroachdb
```

### Kafka Not Connecting

Check all Kafka brokers are ready:
```bash
kubectl get pods -l app=kafka
kubectl logs kafka-0
kubectl logs kafka-1
kubectl logs kafka-2
```

All 3 brokers must be running for the cluster to be healthy.

### Local Registry Issues

If you see warnings about missing local registry or slow image pushes:

**Symptom**: "Running Kind without a local image registry"

**Solution**: Recreate cluster with registry support:
```bash
# Delete existing cluster
ctlptl delete cluster kind-meridian-local

# Create new cluster with local registry
ctlptl create cluster kind --registry=ctlptl-registry --name=kind-meridian-local

# Restart Tilt
tilt up
```

**Symptom**: "Error: registry 'ctlptl-registry' not found"

**Diagnosis**: The Tiltfile expects a registry named `ctlptl-registry` but it doesn't exist.

**Solution**: Ensure you created the cluster with the correct registry name:
```bash
# Verify registry container exists
docker ps | grep ctlptl-registry

# If not found, recreate cluster with correct command
ctlptl create cluster kind --registry=ctlptl-registry --name=kind-meridian-local
```

**Note**: The local registry is only used when the Kubernetes context is `kind-meridian-local`. Other contexts (docker-desktop, minikube) will use the default registry.

### Custom Registry Name

If you created your cluster with a different registry name, set the `TILT_REGISTRY_NAME` environment variable:

```bash
# Create cluster with custom registry name
ctlptl create cluster kind --registry=my-custom-registry --name=kind-meridian-local

# Tell Tilt to use the custom registry
export TILT_REGISTRY_NAME=my-custom-registry
tilt up
```

The Tiltfile will automatically validate that the registry exists and provide helpful error messages if it's not found.

## Performance Optimization

### Fast Rebuilds

Tilt uses live_update to achieve ~3 second rebuilds:

1. **File Sync**: Changes sync directly to container
2. **Incremental Build**: Only changed packages rebuild
3. **Hot Restart**: Service restarts without container rebuild

### Resource Limits

Kafka resource limits can be adjusted in the Tiltfile (around line 208) if your machine is constrained. The 3-broker setup uses approximately 1.5GB total memory (384Mi per broker).

### Parallel Updates

Tilt builds up to 3 resources in parallel by default. Adjust in `Tiltfile`:

```python
update_settings(max_parallel_updates=5)
```

## Advanced Usage

### Custom Docker Registry

Set environment variable before running Tilt:

```bash
export DOCKER_REGISTRY=my-registry.com/myorg
tilt up
```

### Multiple Kubernetes Contexts

Add your context to allowed list in `Tiltfile`:

```python
allow_k8s_contexts(['my-context'])
```

### Debug Mode

Add debug port forwarding in `Tiltfile`:

```python
k8s_resource(
  'meridian',
  port_forwards=[
    '8080:8080',
    '9090:9090',
    '2345:2345',  # Delve debugger
  ],
)
```

Then attach your debugger to `localhost:2345`.

## CI/CD Integration

While Tilt is primarily for local development, you can run it in CI:

```bash
# Run in CI mode (non-interactive)
tilt ci

# Run specific resources only
tilt ci meridian
```

This is useful for integration testing in CI pipelines.

## Further Reading

### Related Skills
- [Schema Evolution](./schema-evolution.md) - Protobuf schema evolution and buf validation
- [Docker Configuration](./docker.md) - Multi-stage builds and container optimization
- [Kustomize Deployments](./kustomize.md) - Environment-specific configurations
- [Security Scanning](./security.md) - Vulnerability detection in images

### Documentation
- [Tilt Documentation](https://docs.tilt.dev/)
- [Tilt Best Practices](https://docs.tilt.dev/best_practices.html)
- [Live Update Reference](https://docs.tilt.dev/live_update_reference.html)
- [Tiltfile API Reference](https://docs.tilt.dev/api.html)
