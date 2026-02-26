---
name: adr-007-raw-yaml-over-helm-for-initial-development
description: Use raw Kubernetes YAML instead of Helm charts during initial development phase
triggers:

  - Setting up local infrastructure services
  - Creating Kubernetes manifests for backing services
  - Evaluating Helm adoption timeline

instructions: |
  Use raw Kubernetes YAML for all infrastructure services in Tiltfile (CockroachDB, Redis,
  Kafka, Zookeeper). Defer Helm adoption until service topology stabilizes and
  multi-environment deployment becomes necessary.
---

# 7. Raw YAML Over Helm for Initial Development

Date: 2025-10-29

## Status

Accepted

## Context

Meridian uses Kubernetes for deployment and Tilt for local development. The project needs to define backing services
(CockroachDB, Redis, Kafka, Zookeeper) for local development.

**The question:** Should we use Helm charts or raw Kubernetes YAML for defining these services in the Tiltfile?

### Helm's Value Proposition

Helm is primarily a templating engine that allows:

```text
Same Helm Chart + Different Values Files = Different Environments
```

The benefits are:

- **Environment parity**: Same chart for local/staging/prod with different values
- **Dependency management**: Charts can depend on other charts
- **Versioning**: Chart versions track configuration changes
- **Community**: Large ecosystem of pre-built charts

### The Complexity Tax

Using Helm introduces additional abstraction layers:

1. Application code
2. Container (Docker)
3. Kubernetes primitives (Pods, Services, Deployments)
4. **Helm templates** (Go templating over Kubernetes YAML)
5. **Helm values** (configuration inputs to templates)
6. Tilt (orchestrating everything)

When debugging a failing pod, you must reason through all six layers:

- Is my code wrong?
- Is the Dockerfile wrong?
- Is the Kubernetes YAML wrong?
- Did the Helm template render correctly?
- Are my values correct?
- Is Tilt configured properly?

This is particularly challenging when:

- Bootstrapping a new project with evolving infrastructure requirements
- Debugging service connectivity issues
- Rapidly iterating on infrastructure setup
- Maintaining transparency about what's actually running in the cluster

## Decision

**For initial development, use raw Kubernetes YAML instead of Helm charts:**

1. **Use raw YAML for all backing services**: CockroachDB, Redis, Kafka, and Zookeeper are all defined as inline YAML
in the Tiltfile
2. **Keep configurations simple**: Single-node deployments with minimal but complete configuration
3. **Defer Helm migration**: Plan to migrate to Helm charts once service topology stabilizes and multi-environment
deployment becomes necessary

This is a **conscious architectural decision**, not an oversight. We are explicitly choosing transparency and iteration
velocity over environment abstraction during the bootstrap phase, prioritizing rapid development over premature
optimisation.

## Decision Drivers

- **Transparency**: Direct visibility into deployed resources accelerates debugging and understanding
- **Iteration speed**: Faster feedback loops when configuration changes are immediately visible
- **Reduced complexity**: Minimise abstraction layers during bootstrap phase when requirements are evolving
- **Service stability**: Service topology is still evolving; premature abstraction creates unnecessary churn
- **Deferred value**: Helm's multi-environment capabilities aren't needed until we have multiple deployment targets
- **Complete but minimal**: Even complex services like Kafka can be configured simply for local development

## Consequences

### Positive

- **Transparent**: What you see in the Tiltfile is what runs in Kubernetes
- **Fast iteration**: Direct YAML editing, no template rendering to debug
- **Lower cognitive overhead**: Fewer abstraction layers when troubleshooting
- **Direct control**: Explicit configuration without indirection through templating
- **Simpler debugging**: Fewer places for configuration to go wrong

### Negative

- **Environment-specific configuration**: Will require separate YAML files for staging/prod
- **No dependency management**: Service startup order handled by Tilt, not Helm
- **Manual version management**: No chart versioning for infrastructure configuration
- **Duplication**: Some YAML patterns may be repeated across services

### Migration Path

When service topology stabilizes, migrate to Helm:

1. **Identify parameterisation points**: What differs between environments?
   - Resource limits (CPU, memory)
   - Storage (local vs cloud)
   - Networking (NodePort vs LoadBalancer)
   - Secrets (dev vs prod credentials)

1. **Create Helm charts**: Package stable service definitions
   - `charts/meridian/` - Main application chart
   - `charts/backing-services/` - Infrastructure services chart

1. **Multi-environment values**:
   - `values-local.yaml` - Minimal resources, NodePort
   - `values-stage.yaml` - Moderate resources, cloud storage
   - `values-prod.yaml` - HA, multiple replicas, production secrets

1. **Update Tiltfile**: Replace raw YAML with `helm_remote()` calls

1. **Production deployment**: Use Helm for staging and production environments

## Examples

### Current Approach (Raw YAML)

```python

# Tiltfile

k8s_yaml(blob('''
apiVersion: v1
kind: Service
metadata:
  name: redis
spec:
  ports:

  - port: 6379

  selector:
    app: redis
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: redis
spec:
  replicas: 1
  template:
    spec:
      containers:

      - name: redis

        image: redis:7-alpine
        resources:
          limits:
            memory: 512Mi
'''))
```

**Benefits**: Direct visibility, easy to modify, clear what's deployed

### Future Helm Approach

```python

# Tiltfile

helm_remote(
  'redis',
  repo_name='bitnami',
  repo_url='https://charts.bitnami.com/bitnami',
  values=['deployments/helm/redis-values-local.yaml']
)
```

```yaml

# deployments/helm/redis-values-local.yaml

replica:
  replicaCount: 1
resources:
  limits:
    memory: 512Mi
```

**Benefits**: Environment abstraction, versioning, community support
**Costs**: Additional file, template indirection, chart maintenance

## Related Decisions

- ADR-0006: Tilt for Local Kubernetes Development
- Future ADR: Multi-environment deployment strategy

## References

- [Helm Documentation](https://helm.sh/docs/)
- [Tilt Helm Integration](https://docs.tilt.dev/helm.html)
- [Kubernetes Documentation](https://kubernetes.io/docs/concepts/)
