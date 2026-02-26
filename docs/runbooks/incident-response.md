---
name: incident-response-runbook
description: Step-by-step procedures for responding to security incidents, service degradation, and production outages
triggers:

  - Handling active security incidents
  - Responding to production outages
  - Managing service degradation
  - Executing emergency rollbacks
  - Investigating production errors

instructions: |
  Follow severity-based response times (P0: immediate, P1: <15min, P2: <1hr).
  Use containment procedures for security incidents. Execute rollback procedures
  for failed deployments. Document timeline and evidence for post-incident review.
---

# Incident Response Runbook

**When to use this runbook**: Active security incident, service degradation, or production outage.

> **⚠️ Customisation Note**: This is a template runbook for Meridian. In a production deployment, you should customise
this with your specific:
>
> - Contact details and escalation paths
> - Monitoring dashboards and log locations
> - Rollback procedures specific to your infrastructure
> - Post-incident review process

## Quick Reference

| Severity | Response Time | Escalation |
| -------- | ------------- | ---------- |
| **P0 - Critical** | Immediate | Page on-call engineer + CTO |
| **P1 - High** | < 15 minutes | On-call engineer |
| **P2 - Medium** | < 1 hour | Engineering team |
| **P3 - Low** | Next business day | Backlog |

## Incident Response Process

### 1. Detection & Initial Assessment (0-5 minutes)

**Symptoms observed:**

- [ ] Security alert triggered
- [ ] Monitoring alert (CPU, memory, errors)
- [ ] User report of service unavailability
- [ ] Unusual traffic patterns
- [ ] Failed security scans

**Immediate actions:**

```bash

# Check pod status

kubectl get pods -n production

# Check recent events

kubectl get events -n production --sort-by='.lastTimestamp' | tail -20

# Check logs for errors

kubectl logs -n production -l app=meridian --tail=100

# Check resource usage

kubectl top pods -n production
```

**Initial severity assessment:**

- Is production affected? → P0/P1
- Is staging affected only? → P2
- Is development affected only? → P3

### 2. Containment (5-10 minutes)

**For security incidents:**

```bash

# Isolate compromised pods

kubectl delete pod <compromised-pod> -n production

# Scale down to stop spread

kubectl scale deployment meridian -n production --replicas=0

# Review NetworkPolicy violations

kubectl describe networkpolicy meridian -n production
```

**For service degradation:**

```bash

# Check if rollback needed

kubectl rollout history deployment/meridian -n production

# Rollback to last known good version

kubectl rollout undo deployment/meridian -n production

# Scale up if needed

kubectl scale deployment meridian -n production --replicas=3
```

### 3. Investigation (10-30 minutes)

**Check logs:**

```bash

# Application logs

stern -n production meridian --since 1h

# Security scan results

gh api repos/{owner}/meridian/code-scanning/alerts

# GitHub Actions workflow runs

gh run list --workflow=security.yml --limit 5
```

**Gather evidence:**

- [ ] Export logs: `kubectl logs -n production <pod> > incident-logs.txt`
- [ ] Export events: `kubectl get events -n production > incident-events.txt`
- [ ] Screenshot dashboards
- [ ] Note timeline of events

### 4. Eradication (30-60 minutes)

**For vulnerabilities:**

```bash

# Check dependency vulnerabilities

go list -json -m all | govulncheck -json -

# Update vulnerable dependencies

go get -u <vulnerable-package>
go mod tidy

# Rebuild and redeploy

docker build -t meridian:hotfix-$(date +%s) .
kubectl set image deployment/meridian meridian=meridian:hotfix-$(date +%s) -n production
```

**For configuration issues:**

```bash

# Update ConfigMap

kubectl edit configmap meridian-config -n production

# Restart pods to pick up changes

kubectl rollout restart deployment/meridian -n production
```

### 5. Recovery (60-120 minutes)

**Verify service health:**

```bash

# Check pod status

kubectl get pods -n production

# Check deployment rollout

kubectl rollout status deployment/meridian -n production

# Test endpoints

curl -I https://meridian.production.svc.cluster.local:8080/health

# Check metrics

kubectl top pods -n production
```

**Validation checklist:**

- [ ] All pods running and ready
- [ ] Health checks passing
- [ ] Metrics within normal range
- [ ] No error logs
- [ ] Security scans passing
- [ ] Monitoring dashboards green

### 6. Post-Incident Review (Within 48 hours)

**Document:**

- Timeline of events
- Root cause analysis
- Actions taken
- What worked / What didn't
- Preventative measures

**Template location:** `docs/runbooks/post-incident-template.md` (to be created)

## Contact Information

> **⚠️ Production Setup Required**: Configure these with your actual contact details

- **On-Call Engineer**: [PagerDuty rotation link]
- **Security Team**: <security@your-domain.com>
- **Engineering Manager**: [Contact details]
- **CTO/Escalation**: [Contact details]

## Common Scenarios

### Scenario: High CPU Usage

**Symptoms:** CPU > 80% sustained, slow response times

**Quick fix:**

```bash

# Scale up replicas

kubectl scale deployment meridian -n production --replicas=5

# Check for resource leaks

kubectl top pods -n production --sort-by=cpu
```

### Scenario: Database Connection Failures

**Symptoms:** Logs show "connection refused" to CockroachDB

**Quick fix:**

```bash

# Check database pods

kubectl get pods -n production -l app=cockroachdb

# Check NetworkPolicy allows DB traffic

kubectl describe networkpolicy meridian -n production | grep -A5 "cockroachdb"

# Test connectivity

kubectl exec -it <meridian-pod> -n production -- nc -zv cockroachdb 26257
```

### Scenario: Security Scan Blocking Deployment

**Symptoms:** GitHub Actions security workflow failing

**Quick fix:**

```bash

# Check security alerts

gh api repos/{owner}/meridian/code-scanning/alerts | jq '.[] | select(.state=="open")'

# Review Trivy results

gh run view <run-id> --log | grep -i "critical\|high"

# Fix vulnerabilities before force-deploy

# DO NOT bypass security scans in production

```

## Monitoring & Dashboards

> **⚠️ Production Setup Required**: Add links to your monitoring systems

- **Application Dashboard**: [Datadog/Grafana link]
- **Infrastructure Dashboard**: [Cloud provider console]
- **Security Dashboard**: [GitHub Security tab]
- **Logs**: [Log aggregation system]

## Rollback Procedures

**Standard rollback:**

```bash

# Rollback to previous deployment

kubectl rollout undo deployment/meridian -n production

# Verify rollback

kubectl rollout status deployment/meridian -n production
```

**Rollback to specific revision:**

```bash

# List revisions

kubectl rollout history deployment/meridian -n production

# Rollback to specific revision

kubectl rollout undo deployment/meridian -n production --to-revision=5
```

## Decision Trees

### Is this a security incident?

```text
Security alert triggered?
├── Yes → Assume P0, page security team
└── No → Continue assessment

User data exposed?
├── Yes → P0, page security team + CTO
└── No → Continue assessment

Vulnerability actively exploited?
├── Yes → P0, containment phase
└── No → P1-P2, investigation phase
```

### Should I rollback?

```text
Production affected?
├── Yes → Consider rollback
│   ├── Is fix quick (< 10 min)? → Fix forward
│   └── Is fix complex? → Rollback
└── No → Investigate before action
```

## Tools & Commands Reference

**kubectl shortcuts:**

```bash
alias kgp='kubectl get pods -n production'
alias kd='kubectl describe -n production'
alias kl='kubectl logs -n production'
alias ke='kubectl get events -n production --sort-by=.lastTimestamp'
```

**Useful one-liners:**

```bash

# Find pods with high restart count

kubectl get pods -n production --sort-by='.status.containerStatuses[0].restartCount'

# Get all pod IPs

kubectl get pods -n production -o wide

# Export all resources for backup

kubectl get all -n production -o yaml > production-backup.yaml
```

## Emergency Contacts

> **⚠️ Production Setup Required**: Add your organisation's emergency contacts

- **Security Hotline**: [Phone number]
- **Infrastructure Team**: [Slack channel]
- **Engineering Team**: [Slack channel]
- **Management Escalation**: [Contact details]
