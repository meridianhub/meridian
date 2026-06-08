# Kubernetes Deployments

Kustomize-based Kubernetes manifests for deploying Meridian and its supporting workloads.

## Layout

| Directory | Purpose |
|-----------|---------|
| `base/` | Base Kustomize manifests shared across environments |
| `overlays/` | Environment-specific overlays layered on top of `base/` |
| `local/` | Local-cluster manifests for development |
| `observability/` | Tracing, metrics, and logging stack manifests |
| `audit-consumer/` | Per-service audit consumer deployments |
| `policies/` | OPA Gatekeeper policies and their tests |

## Component Docs

- [Audit Consumer Deployments](audit-consumer/README.md) - Kustomize deployments for the per-service audit consumer
- [OPA Gatekeeper Policy Tests](policies/tests/README.md) - tests for the `BlockDevModeInProduction` admission policy
