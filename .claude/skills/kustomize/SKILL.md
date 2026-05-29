---
name: skill-kustomize-deployments
description: Kustomize configuration for environment-specific Kubernetes deployments
triggers:

  - Deploying to different environments
  - Managing environment-specific configs
  - Understanding Kustomize overlays

instructions: |
  Use Kustomize for environment-specific Kubernetes manifests.
  Base configurations in deployments/k8s/base/, overlays in deployments/k8s/overlays/.
  Deploy with: kubectl apply -k deployments/k8s/overlays/dev
---

# Kustomize Overlays for Environment-Specific Configuration

This document explains how to use Kustomize overlays to manage environment-specific Kubernetes configurations for
Meridian.

## Overview

We use Kustomize to manage Kubernetes manifests across multiple environments without duplicating configuration. The
structure follows the recommended base + overlay pattern:

```text
deployments/k8s/
├── base/                     # Common configuration for all environments
│   ├── deployment.yaml
│   ├── service.yaml
│   ├── serviceaccount.yaml
│   ├── configmap.yaml
│   └── kustomization.yaml
└── overlays/                 # Environment-specific configurations
    ├── dev/
    ├── staging/
    └── production/
```

## Environment Configurations

### Development (dev)

Optimized for local development with minimal resource usage:

- **Replicas**: 1 (single instance)
- **Resources**:
  - CPU: 50m request, 200m limit
  - Memory: 64Mi request, 256Mi limit
- **Logging**: Debug level with JSON format
- **Health Checks**: Fast intervals (5s liveness, 3s readiness)
- **Image Tag**: `dev-latest`
- **Environment Variables**:
  - `ENVIRONMENT=development`
  - `DEBUG_MODE=true`

**Use Case**: Local development, debugging, rapid iteration

### Staging (staging)

Production-like configuration for pre-production testing:

- **Replicas**: 2 (production-like availability)
- **Resources**:
  - CPU: 100m request, 500m limit
  - Memory: 128Mi request, 512Mi limit
- **Logging**: Info level with JSON format
- **Health Checks**: Standard intervals
- **Image Tag**: `staging-latest`
- **Monitoring**: Datadog integration enabled
- **Environment Variables**:
  - `ENVIRONMENT=staging`
  - `DEBUG_MODE=false`

**Use Case**: Pre-production validation, integration testing, performance testing

### Production (production)

High-availability configuration with strict policies:

- **Replicas**: 3 (high availability minimum)
- **Resources**:
  - CPU: 100m request, 500m limit
  - Memory: 128Mi request, 512Mi limit
- **Logging**: Warn level with JSON format (reduced logging overhead)
- **Health Checks**:
  - Liveness: 3 failure threshold
  - Readiness: 3 failure threshold
  - Startup: 30 failure threshold (5 minutes maximum startup time)
- **Rolling Update Strategy**:
  - `maxUnavailable: 0` (zero-downtime deployments)
  - `maxSurge: 1` (one extra pod during updates)
- **Termination Grace Period**: 60 seconds (vs 30s in other environments)
- **Image Tag**: `stable`
- **Monitoring**: Datadog integration with `criticality:high` tag
- **Environment Variables**:
  - `ENVIRONMENT=production`
  - `DEBUG_MODE=false`

**Use Case**: Production workloads requiring high availability and reliability

## Usage

### Building Manifests

Generate the final Kubernetes manifests for a specific environment:

```bash

# Development

kubectl kustomize deployments/k8s/overlays/dev

# Staging

kubectl kustomize deployments/k8s/overlays/staging

# Production

kubectl kustomize deployments/k8s/overlays/production
```

### Applying to Kubernetes

Deploy to a specific environment:

```bash

# Development

kubectl apply -k deployments/k8s/overlays/dev

# Staging

kubectl apply -k deployments/k8s/overlays/staging

# Production

kubectl apply -k deployments/k8s/overlays/production
```

### Viewing Differences

Compare what will change before applying:

```bash

# Show what would be applied

kubectl diff -k deployments/k8s/overlays/production

# Compare staging vs production

diff <(kubectl kustomize deployments/k8s/overlays/staging) \
     <(kubectl kustomize deployments/k8s/overlays/production)
```

## Customization Patterns

### Adding Environment-Specific Configuration

Each overlay uses JSON Patch operations to modify the base configuration. Common patterns:

#### Changing Replicas

```yaml
patches:

- target:

    kind: Deployment
    name: meridian
  patch: |-

    - op: replace

      path: /spec/replicas
      value: 3
```

#### Adding Environment Variables

```yaml
patches:

- target:

    kind: Deployment
    name: meridian
  patch: |-

    - op: add

      path: /spec/template/spec/containers/0/env/-
      value:
        name: FEATURE_FLAG_X
        value: "true"
```

#### Changing Resource Limits

```yaml
patches:

- target:

    kind: Deployment
    name: meridian
  patch: |-

    - op: replace

      path: /spec/template/spec/containers/0/resources
      value:
        requests:
          cpu: 200m
          memory: 256Mi
        limits:
          cpu: 1000m
          memory: 1Gi
```

#### Adding Annotations

```yaml
patches:

- target:

    kind: Deployment
    name: meridian
  patch: |-

    - op: add

      path: /spec/template/metadata/annotations/custom-annotation
      value: "custom-value"
```

### ConfigMap Customization

Each environment merges ConfigMap values with the base configuration:

```yaml
configMapGenerator:

- name: meridian-config

  behavior: merge
  literals:

  - log_level=debug
  - custom_setting=value

```

The `behavior: merge` ensures base configuration is preserved while adding/overriding specific values.

## CI/CD Integration

### GitHub Actions

The deploy workflow uses Kustomize overlays for environment-specific deployments:

```yaml

- name: Deploy to staging

  run: kubectl apply -k deployments/k8s/overlays/staging

- name: Deploy to production

  run: kubectl apply -k deployments/k8s/overlays/production
```

### Tilt (Local Development)

Tilt automatically uses the dev overlay for local development. See `Tiltfile` for configuration.

## Best Practices

### 1. Never Edit Base for Environment-Specific Needs

Always use overlays for environment-specific configuration. The base should contain common configuration that applies
to all environments.

### 2. Use Semantic Image Tags

- Dev: `dev-latest` (automatic updates)
- Staging: `staging-latest` or `v1.2.3-rc1` (release candidates)
- Production: `stable` or `v1.2.3` (immutable releases)

### 3. Test Overlays Locally

Always test overlay builds before applying:

```bash
kubectl kustomize deployments/k8s/overlays/production | kubectl apply --dry-run=client -f -
```

### 4. Resource Limits

- **Dev**: Generous limits for debugging (2-4x requests)
- **Staging**: Production-like limits
- **Production**: Strict limits based on actual usage patterns

### 5. Health Check Tuning

- **Dev**: Fast intervals for rapid feedback
- **Staging**: Production-like intervals
- **Production**: Conservative thresholds to avoid false positives

### 6. Secrets Management

Never store secrets in Kustomize overlays. Use:

- Kubernetes Secrets with encryption at rest
- External secret management (Vault, AWS Secrets Manager)
- GitHub Actions secrets for CI/CD

## Troubleshooting

### Overlay Not Applying

Check the target selector matches your base resources:

```bash
kubectl kustomize deployments/k8s/overlays/production | grep -A5 "kind: Deployment"
```

### Patch Operation Failed

Verify the path exists in the base resource:

```bash

# Show the base deployment structure

kubectl kustomize deployments/k8s/base | yq eval 'select(.kind == "Deployment")'
```

### Namespace Conflicts

Each overlay defines its own namespace. Ensure you're deploying to the correct namespace:

```bash

# Check current context

kubectl config current-context

# List resources in environment namespace

kubectl get all -n production
```

### ConfigMap Merge Not Working

Ensure `behavior: merge` is set in the overlay's configMapGenerator:

```yaml
configMapGenerator:

- name: meridian-config

  behavior: merge  # Required for merging with base
  literals:

  - key=value

```

## References

- [Kustomize Documentation](https://kustomize.io/)
- [Kubernetes Kustomize](https://kubernetes.io/docs/tasks/manage-kubernetes-objects/kustomization/)
- [JSON Patch RFC 6902](https://tools.ietf.org/html/rfc6902)
- [Meridian Architecture Decision Records](../adr/)
