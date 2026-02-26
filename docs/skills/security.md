---
name: skill-security-practices
description: Security scanning, vulnerability management, and security best practices
triggers:

  - Running security scans
  - Fixing vulnerabilities
  - Understanding security tools
  - Configuring security workflows

instructions: |
  Use gosec for Go code scanning, Trivy for container and dependency scanning.
  Generate SBOMs for supply chain security. Run security checks in CI/CD.
  Fix high/critical vulnerabilities before merging.
---

# Security Architecture and Hardening

This document outlines the security measures implemented in Meridian to ensure production-grade security and compliance.

> **⚠️ Note for Learning Environment**: This is a comprehensive security architecture guide for the Meridian learning
project. When deploying to production, you should customise the following with your organisation's specific details:
>
> - Contact information and escalation procedures
> - Monitoring and alerting integrations
> - Backup locations and credentials
> - DNS and certificate management
> - External secret management systems
> - Compliance requirements specific to your industry

## Table of Contents

- [Security Principles](#security-principles)
- [Container Security](#container-security)
- [Kubernetes Security](#kubernetes-security)
- [Network Security](#network-security)
- [Access Control](#access-control)
- [Security Scanning](#security-scanning)
- [Secret Management](#secret-management)
- [Incident Response](#incident-response)

## Security Principles

Meridian follows defence-in-depth security principles:

1. **Least Privilege**: Minimal permissions at every layer
2. **Secure by Default**: Security features enabled out-of-the-box
3. **Zero Trust**: Never trust, always verify
4. **Immutable Infrastructure**: Containers cannot be modified at runtime
5. **Continuous Security**: Automated scanning and monitoring

## Container Security

### Base Image Security

- **Distroless Base**: Using `gcr.io/distroless/static:nonroot` runtime image
  - No shell, package managers, or unnecessary utilities
  - Minimal attack surface (~2-3MB)
  - Regular security updates from Google

- **Static Binary**: Compiled with `CGO_ENABLED=0`
  - No dynamic library dependencies
  - Eliminates shared library vulnerabilities
  - Size: ~1.4MB (statically linked)

### Build-Time Security

```dockerfile
FROM gcr.io/distroless/static:nonroot
COPY --from=builder --chown=nonroot:nonroot /app/meridian /meridian
USER nonroot:nonroot
```

- Non-root user (UID 65532) at build time
- Files owned by non-root user
- No privilege escalation possible

### Container Security Contexts

Pod-level security context:

```yaml
securityContext:
  runAsNonRoot: true
  runAsUser: 65532
  runAsGroup: 65532
  fsGroup: 65532
  seccompProfile:
    type: RuntimeDefault
```

Container-level security context:

```yaml
securityContext:
  allowPrivilegeEscalation: false
  readOnlyRootFilesystem: true
  runAsNonRoot: true
  runAsUser: 65532
  capabilities:
    drop:

    - ALL

```

### Read-Only Filesystem

The container runs with a read-only root filesystem. Writable directories are mounted as `emptyDir` volumes:

```yaml
volumeMounts:

- name: tmp

  mountPath: /tmp
volumes:

- name: tmp

  emptyDir: {}
```

This prevents:

- Runtime file modification
- Malware persistence
- Unauthorized file writes

## Kubernetes Security

### Role-Based Access Control (RBAC)

Meridian uses least-privilege RBAC policies:

**Role** (`deployments/k8s/base/role.yaml`):

- **Read-only pod information**: For health checks and metadata
- **Read ConfigMaps**: `meridian-config`, `meridian-build-info` (configuration reload)
- **Read Secrets**: `meridian-secrets` only (credentials)
- **No write permissions**: Application cannot modify Kubernetes resources
- **Namespace scoped**: No cluster-wide access

**RoleBinding** (`deployments/k8s/base/rolebinding.yaml`):

- Binds the Role to the `meridian` ServiceAccount
- Enforced per namespace (dev/staging/production)

### Service Account

Dedicated ServiceAccount with minimal permissions:

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: meridian
automountServiceAccountToken: true  # Required for API access
```

### Pod Security Standards

Meridian meets the **Restricted** Pod Security Standard:

- ✅ Non-root user (UID 65532)
- ✅ Read-only root filesystem
- ✅ No privilege escalation
- ✅ All capabilities dropped
- ✅ Seccomp profile: RuntimeDefault
- ✅ No host namespaces
- ✅ No host ports
- ✅ No privileged containers

## Network Security

### NetworkPolicy (Production Only)

Production environment enforces strict network policies (`deployments/k8s/overlays/production/networkpolicy.yaml`):

**Ingress Rules**:

- Allow HTTP/gRPC from within cluster (ports 8080, 9090)
- Allow metrics scraping from monitoring namespace (port 8080)
- **Default deny** all other ingress

**Egress Rules**:

- Allow DNS resolution (kube-system namespace, UDP 53)
- Allow database connections (CockroachDB port 26257)
- Allow Kafka connections (port 9092)
- Allow Redis connections (port 6379)
- Allow HTTPS outbound (port 443 for external APIs)
- **Default deny** all other egress

### Zero-Downtime Deployments

Production rolling update strategy:

```yaml
strategy:
  type: RollingUpdate
  rollingUpdate:
    maxUnavailable: 0  # Always maintain minimum capacity
    maxSurge: 1        # One extra pod during updates
```

### TLS/HTTPS

- **Internal traffic**: Service mesh (future: Istio/Linkerd) for mTLS
- **External traffic**: Ingress Controller with TLS termination
- **Certificate management**: cert-manager with Let's Encrypt

## Access Control

### Authentication

- **Service-to-service**: Kubernetes ServiceAccount tokens
- **External APIs**: API keys stored in Kubernetes Secrets
- **Monitoring**: Datadog agent authentication

### Authorisation

- **RBAC**: Least-privilege roles for Kubernetes access
- **API Authorisation**: Role-based access in application layer (future)

### Audit Logging

Kubernetes audit logs capture:

- All API server requests
- RBAC decisions
- Secret access attempts
- Network policy violations

## Security Scanning

### Automated Security Scanning

GitHub Actions workflow (`.github/workflows/security.yml`) runs:

1. **govulncheck**: Go vulnerability database scanning
2. **Gosec**: Static analysis security scanner
3. **Trivy**:
   - Repository scan (source code, dependencies)
   - Container image scan (OS packages, application dependencies)
4. **Dependency Review**: PR dependency vulnerability check
5. **SBOM Generation**: Software Bill of Materials (SPDX format)
6. **Gitleaks**: Secret detection in git history

### Scan Frequency

- **On every push**: To develop and main branches
- **On every PR**: Before merge
- **Daily schedule**: 2 AM UTC (catch new vulnerabilities)

### Vulnerability Response

**Critical/High Severity**:

1. CI pipeline fails immediately
2. SARIF results uploaded to GitHub Security tab
3. Security team notified via Slack (future)
4. Patch within 24 hours

**Medium Severity**:

1. Logged but does not block deployment
2. Fix within 1 week

**Low Severity**:

1. Tracked in backlog
2. Fix in next sprint

### SBOM (Software Bill of Materials)

Generated with Syft in SPDX-JSON format:

- Lists all dependencies and versions
- Stored as build artifact (90-day retention)
- Used for vulnerability tracking and compliance

## Secret Management

### Current Approach

Secrets stored as Kubernetes Secrets:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: meridian-secrets
type: Opaque
data:
  database-url: <base64-encoded>
  api-key: <base64-encoded>
```

### Best Practices

- **Never commit secrets** to git
- **Use external secret management** in production:
  - AWS Secrets Manager
  - HashiCorp Vault
  - Azure Key Vault
- **Rotate secrets regularly**: Quarterly minimum
- **Audit secret access**: Monitor who accesses which secrets

### Environment-Specific Secrets

- **Development**: Local environment variables
- **Staging**: Kubernetes Secrets
- **Production**: External secret manager with auto-rotation

## Incident Response

### Security Incident Process

1. **Detection**: Security scanning, monitoring alerts, or manual report
2. **Assessment**: Determine severity and impact
3. **Containment**: Isolate affected resources
4. **Eradication**: Remove vulnerability or malware
5. **Recovery**: Restore services
6. **Post-Mortem**: Document lessons learned

### Contact Information

> **⚠️ Production Setup Required**: Configure with your organisation's actual contact details

- **Security Team**: `security@your-domain.com`
- **On-Call**: [PagerDuty rotation or on-call schedule]
- **Escalation**: [CTO/CISO contact for critical incidents]

### Runbook Locations

- Incident response: `docs/runbooks/incident-response.md`
- Disaster recovery: `docs/runbooks/disaster-recovery.md`

## Compliance and Auditing

### Security Standards

Meridian is designed to meet:

- **OWASP Top 10**: Web application security risks
- **CIS Kubernetes Benchmark**: Container orchestration hardening
- **NIST Cybersecurity Framework**: Risk management

### Audit Trail

All security-relevant events are logged:

- Authentication attempts
- Authorisation failures
- Secret access
- Configuration changes
- Network policy violations

Logs retained for 90 days (configurable per compliance requirements).

## Security Checklist for Deployment

Before deploying to production:

- [ ] All security scans pass (zero critical/high vulnerabilities)
- [ ] RBAC policies configured and tested
- [ ] NetworkPolicy applied and tested
- [ ] Secrets managed via external secret manager
- [ ] TLS certificates configured
- [ ] Monitoring and alerting enabled
- [ ] Incident response plan documented
- [ ] Security team has reviewed configuration
- [ ] Penetration testing completed (if required)
- [ ] Compliance requirements verified

## Future Security Enhancements

1. **Service Mesh**: Implement Istio/Linkerd for mTLS and observability
2. **Policy-as-Code**: Use Open Policy Agent (OPA) for runtime policy enforcement
3. **Image Signing**: Cosign for container image signatures
4. **Vulnerability Management**: Automated patching pipeline
5. **Runtime Security**: Falco for runtime threat detection
6. **Zero Trust Network**: Implement full zero-trust architecture

## References

- [Kubernetes Security Best Practices](https://kubernetes.io/docs/concepts/security/)
- [OWASP Kubernetes Security Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Kubernetes_Security_Cheat_Sheet.html)
- [CIS Kubernetes Benchmark](https://www.cisecurity.org/benchmark/kubernetes)
- [NIST Container Security Guide](https://nvlpubs.nist.gov/nistpubs/SpecialPublications/NIST.SP.800-190.pdf)
- [Distroless Container Images](https://github.com/GoogleContainerTools/distroless)
