# Secrets Management in Meridian

This document describes how secrets are managed across different environments in the Meridian microservices platform.

## Overview

Meridian uses Kubernetes Secrets to manage sensitive configuration data such as database credentials, API keys, and
certificates. Secrets are kept separate from application code and configuration to maintain security and flexibility.

## Development Environment (Local/Tilt)

### Current Implementation

For local development with Tilt, secrets are stored in YAML files:

```yaml

# deployments/k8s/current-account/secret.yaml

apiVersion: v1
kind: Secret
metadata:
  name: current-account-db
type: Opaque
stringData:
  DATABASE_URL: "postgres://meridian:meridian@cockroachdb:26257/meridian?sslmode=disable"
```

**⚠️ WARNING:** These secrets contain development-only credentials. **NEVER** use these credentials in production.

### Why Plain YAML in Development?

1. **Convenience**: Developers can start working immediately without additional secret management setup
2. **Transparency**: Easy to see and modify connection strings for local debugging
3. **Version Control**: Development secrets can be committed to git (they're not real secrets)
4. **Simplicity**: No external dependencies or tools required for local development

### Development Secrets Location

| Service | Secret File | Contains |
|---------|-------------|----------|
| current-account | `deployments/k8s/current-account/secret.yaml` | `DATABASE_URL` |
| financial-accounting | `deployments/k8s/financial-accounting/secret.yaml` | `DATABASE_URL` |

## Production Environment

### Requirements

Production secrets **MUST**:

- ✅ Use strong, randomly generated credentials
- ✅ Be stored in a secure secret management system
- ✅ **NEVER** be committed to version control
- ✅ Have access controls and audit logging
- ✅ Be rotated regularly

### Recommended Tools

#### 1. External Secrets Operator (Recommended)

External Secrets Operator syncs secrets from external secret management systems into Kubernetes.

**Supported Backends:**

- AWS Secrets Manager
- Google Secret Manager
- Azure Key Vault
- HashiCorp Vault
- 1Password
- Doppler

**Example ExternalSecret:**

```yaml
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: current-account-db
spec:
  refreshInterval: 1h
  secretStoreRef:
    name: aws-secrets-manager
    kind: SecretStore
  target:
    name: current-account-db
    template:
      data:
        DATABASE_URL: |
          postgres://{{ .username }}:{{ .password }}@{{ .host }}:{{ .port }}/{{ .database }}?sslmode=require
  data:

  - secretKey: username

    remoteRef:
      key: prod/meridian/current-account/db
      property: username

  - secretKey: password

    remoteRef:
      key: prod/meridian/current-account/db
      property: password

  - secretKey: host

    remoteRef:
      key: prod/meridian/current-account/db
      property: host

  - secretKey: port

    remoteRef:
      key: prod/meridian/current-account/db
      property: port

  - secretKey: database

    remoteRef:
      key: prod/meridian/current-account/db
      property: database
```

**Installation:**

```bash
helm repo add external-secrets https://charts.external-secrets.io
helm install external-secrets \
  external-secrets/external-secrets \
  -n external-secrets-system \
  --create-namespace
```

#### 2. Sealed Secrets

Sealed Secrets encrypts secrets so they can be safely stored in git.

**Example:**

```bash

# Create a secret

kubectl create secret generic current-account-db \
  --from-literal=DATABASE_URL="postgres://..." \
  --dry-run=client -o yaml > secret.yaml

# Seal it

kubeseal -f secret.yaml -w sealed-secret.yaml

# Commit sealed-secret.yaml to git

git add sealed-secret.yaml
```

#### 3. HashiCorp Vault

Enterprise-grade secret management with dynamic credentials and encryption.

**Example Vault Integration:**

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: current-account
---
apiVersion: secrets-store.csi.x-k8s.io/v1
kind: SecretProviderClass
metadata:
  name: current-account-vault
spec:
  provider: vault
  parameters:
    roleName: "current-account"
    vaultAddress: "https://vault.example.com:8200"
    objects: |

      - objectName: "database-url"

        secretPath: "secret/data/prod/current-account/database"
        secretKey: "url"
```

## Secret Rotation

### Best Practices

1. **Regular Rotation Schedule:**
   - Database credentials: Every 90 days
   - API keys: Every 30 days
   - Certificates: Automated with cert-manager

1. **Zero-Downtime Rotation:**
   - Use dual credentials during transition period
   - Update secrets in external system first
   - Wait for External Secrets Operator to sync
   - Restart pods to pickup new secrets

1. **Emergency Rotation:**
   - Compromised credentials should be rotated immediately
   - Follow incident response procedures
   - Update all dependent services
   - Audit access logs

### Rotation Process

```bash

# 1. Create new credentials in secret manager

aws secretsmanager put-secret-value \
  --secret-id prod/meridian/current-account/db \
  --secret-string '{"password":"NEW_PASSWORD"}'

# 2. Wait for External Secrets Operator to sync (check refreshInterval)

kubectl get externalsecret current-account-db -w

# 3. Restart pods to pickup new secret

kubectl rollout restart deployment current-account

# 4. Verify service health

kubectl get pods -l app=current-account
kubectl logs -l app=current-account --tail=50 | grep "database connection established"
```

## Migration from Development to Production

### Pre-Production Checklist

- [ ] Remove development secret YAML files from production manifests
- [ ] Set up external secret management system (AWS Secrets Manager, etc.)
- [ ] Create ExternalSecret resources for each service
- [ ] Test secret sync in staging environment
- [ ] Configure RBAC for secret access
- [ ] Set up secret rotation schedule
- [ ] Document emergency rotation procedures
- [ ] Enable audit logging for secret access

### Example Migration

**Before (Development):**

```yaml

# deployments/k8s/current-account/secret.yaml

apiVersion: v1
kind: Secret
metadata:
  name: current-account-db
stringData:
  DATABASE_URL: "postgres://meridian:meridian@localhost:5432/meridian"
```

**After (Production):**

```yaml

# deployments/k8s/current-account/external-secret.yaml

apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: current-account-db
spec:
  refreshInterval: 1h
  secretStoreRef:
    name: aws-secrets-manager
    kind: SecretStore
  target:
    name: current-account-db
  data:

  - secretKey: DATABASE_URL

    remoteRef:
      key: prod/meridian/current-account/database-url
```

## Environment-Specific Configuration

Use Kustomize to manage environment-specific secrets:

```text
deployments/
├── base/
│   ├── deployment.yaml
│   └── service.yaml
├── overlays/
    ├── development/
    │   ├── kustomization.yaml
    │   └── secret.yaml              # Plain secrets for dev
    ├── staging/
    │   ├── kustomization.yaml
    │   └── external-secret.yaml     # External secrets for staging
    └── production/
        ├── kustomization.yaml
        └── external-secret.yaml     # External secrets for production
```

## Security Best Practices

1. **Principle of Least Privilege:**
   - Each service has its own secret
   - Secrets are not shared across services
   - RBAC limits who can read secrets

1. **Encryption:**
   - Secrets encrypted at rest in etcd
   - TLS for all database connections (`sslmode=require`)
   - Consider encrypting secrets in external systems

1. **Audit Logging:**
   - Enable Kubernetes audit logging for secret access
   - Monitor secret manager access logs
   - Alert on unusual access patterns

1. **Secrets in Code:**
   - Never hardcode secrets in application code
   - Use environment variables from Kubernetes secrets
   - Validate secrets at startup but don't log values

## Troubleshooting

### Secret Not Found

```bash

# Check if secret exists

kubectl get secret current-account-db

# Check if ExternalSecret synced

kubectl get externalsecret current-account-db
kubectl describe externalsecret current-account-db

# Check logs

kubectl logs -n external-secrets-system deployment/external-secrets
```

### Connection Failures

```bash

# Verify secret contains correct data

kubectl get secret current-account-db -o jsonpath='{.data.DATABASE_URL}' | base64 -d

# Test connection from pod

kubectl exec -it deployment/current-account -- sh

# Inside pod:

# Check DATABASE_URL environment variable (don't log the value!)

env | grep DATABASE_URL
```

### Rotation Issues

```bash

# Force External Secrets Operator to refresh

kubectl annotate externalsecret current-account-db \
  force-sync=$(date +%s) --overwrite

# Check secret update timestamp

kubectl get secret current-account-db \
  -o jsonpath='{.metadata.creationTimestamp}'
```

## References

- [Kubernetes Secrets Documentation](https://kubernetes.io/docs/concepts/configuration/secret/)
- [External Secrets Operator](https://external-secrets.io/)
- [Sealed Secrets](https://github.com/bitnami-labs/sealed-secrets)
- [HashiCorp Vault](https://www.vaultproject.io/)
- [AWS Secrets Manager](https://aws.amazon.com/secrets-manager/)
