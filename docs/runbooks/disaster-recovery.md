---
name: disaster-recovery-runbook
description: Procedures for recovering from complete infrastructure failures, data corruption, regional outages, or
catastrophic events
triggers:

  - Complete infrastructure failure
  - Data corruption requiring restore
  - Regional cloud outage
  - Catastrophic system failures
  - Full system recovery operations

instructions: |
  Follow RTO/RPO objectives. Execute database restoration from backups.
  Rebuild infrastructure in alternate region if needed. Verify data integrity
  post-recovery. Document recovery actions and update DR procedures.
---

# Disaster Recovery Runbook

**When to use this runbook**: Complete infrastructure failure, data corruption, regional outage, or catastrophic events
requiring full system recovery.

> **⚠️ Customisation Note**: This is a template runbook for Meridian. In a production deployment, you should customise
this with your specific:
>
> - Backup locations and credentials
> - Recovery time objectives (RTO) and recovery point objectives (RPO)
> - Actual infrastructure provider details (AWS, GCP, Azure, on-prem)
> - Tested and validated recovery procedures

## Recovery Objectives

> **⚠️ Production Setup Required**: Define your actual RTO/RPO based on business requirements

| Component | RTO (Recovery Time) | RPO (Data Loss) |
| --------- | ------------------- | --------------- |
| **Kubernetes Cluster** | 4 hours | 0 (infrastructure as code) |
| **Database (CockroachDB)** | 2 hours | 1 hour (hourly backups) |
| **Application State** | 1 hour | 5 minutes (real-time replication) |
| **Configuration** | 30 minutes | 0 (Git-backed) |

## Pre-Disaster Preparedness

**Regular testing schedule:**

- [ ] Monthly: Restore database from backup to staging
- [ ] Quarterly: Full DR drill to alternate region
- [ ] Annually: Complete infrastructure rebuild from scratch

**Required access:**

- [ ] Cloud provider console access (with MFA)
- [ ] GitHub repository access
- [ ] Backup storage credentials
- [ ] DNS management access
- [ ] Certificate authority access

## Disaster Scenarios

### Scenario 1: Complete Cluster Failure

**Symptoms:** All Kubernetes nodes unavailable, cluster API unreachable

**Recovery steps:**

1. **Assess scope** (0-15 minutes)

   ```bash

   # Check cluster status

   kubectl cluster-info

   # Check node status

   kubectl get nodes

   # If completely down, proceed to rebuild

   ```

1. **Deploy new cluster** (15-60 minutes)

   ```bash

   # Using infrastructure as code (example: Terraform)

   cd infrastructure/
   terraform init
   terraform plan -out=recovery.plan
   terraform apply recovery.plan

   # Verify cluster

   kubectl get nodes
   kubectl get namespaces
   ```

1. **Restore configurations** (60-90 minutes)

   ```bash

   # Apply base resources

   kubectl apply -k deployments/k8s/base/

   # Apply production overlay

   kubectl apply -k deployments/k8s/overlays/production/

   # Verify deployments

   kubectl get all -n production

   ```

1. **Restore database** (90-150 minutes)

   ```bash

   # Restore from backup (specific to backup solution)

   # Example using CockroachDB backup:

   # Restore all service databases from backup
   cockroach sql --execute="RESTORE DATABASE meridian_current_account FROM 's3://backups/latest/current_account';"
   cockroach sql --execute="RESTORE DATABASE meridian_financial_accounting FROM 's3://backups/latest/financial_accounting';"
   cockroach sql --execute="RESTORE DATABASE meridian_position_keeping FROM 's3://backups/latest/position_keeping';"
   cockroach sql --execute="RESTORE DATABASE meridian_payment_order FROM 's3://backups/latest/payment_order';"
   cockroach sql --execute="RESTORE DATABASE meridian_party FROM 's3://backups/latest/party';"
   cockroach sql --execute="RESTORE DATABASE meridian_platform FROM 's3://backups/latest/platform';"

   # Verify data integrity for each service database
   cockroach sql --execute="SELECT COUNT(*) FROM meridian_current_account.accounts;"
   cockroach sql --execute="SELECT COUNT(*) FROM meridian_position_keeping.financial_position_logs;"
   ```

1. **Verify and test** (150-180 minutes)

   ```bash

   # Test application endpoints

   curl https://api.meridian.production/health

   # Run smoke tests

   ./scripts/smoke-tests.sh production

   # Monitor logs

   stern -n production meridian --since 5m

   ```

### Scenario 2: Database Corruption

**Symptoms:** Data inconsistencies, transaction failures, database errors

**Recovery steps:**

1. **Stop writes immediately**

   ```bash

   # Scale application to 0

   kubectl scale deployment meridian -n production --replicas=0

   # Verify no active connections

   cockroach sql --execute="SHOW SESSIONS;"
   ```

1. **Assess corruption scope**

   ```bash

   # Check database consistency

   cockroach debug check-store /path/to/store

   # Identify affected tables

   cockroach sql --execute="SELECT * FROM system.range_log WHERE info LIKE '%corruption%';"

   ```

1. **Restore from backup**

   ```bash

   # List available backups

   cockroach sql --execute="SHOW BACKUPS IN 's3://backups/';"

   # Restore to point in time before corruption

   # Restore each service database to point in time before corruption
   cockroach sql --execute="RESTORE DATABASE meridian_current_account FROM 's3://backups/2025-10-28-00-00/current_account';"
   cockroach sql --execute="RESTORE DATABASE meridian_financial_accounting FROM 's3://backups/2025-10-28-00-00/financial_accounting';"
   cockroach sql --execute="RESTORE DATABASE meridian_position_keeping FROM 's3://backups/2025-10-28-00-00/position_keeping';"
   cockroach sql --execute="RESTORE DATABASE meridian_payment_order FROM 's3://backups/2025-10-28-00-00/payment_order';"
   cockroach sql --execute="RESTORE DATABASE meridian_party FROM 's3://backups/2025-10-28-00-00/party';"
   cockroach sql --execute="RESTORE DATABASE meridian_platform FROM 's3://backups/2025-10-28-00-00/platform';"
   ```

1. **Verify data integrity**

   ```bash

   # Run consistency checks

   cockroach sql --execute="SELECT * FROM crdb_internal.check_consistency(true, '', '');"

   # Validate critical data

   cockroach sql --execute="SELECT COUNT(*) FROM meridian_current_account.accounts;"

   ```

1. **Resume operations**

   ```bash

   # Scale application back up

   kubectl scale deployment meridian -n production --replicas=3

   # Monitor for errors

   kubectl logs -n production -l app=meridian --tail=100
   ```

### Scenario 3: Regional Outage

**Symptoms:** Entire cloud region unavailable

**Recovery steps:**

1. **Activate DR region** (0-30 minutes)

   ```bash

   # Update DNS to point to DR region

   # (Specific to DNS provider - Route53, CloudFlare, etc.)

   # Example using Route53:

   aws route53 change-resource-record-sets \
     --hosted-zone-id Z123456 \
     --change-batch file://dns-failover.json

   ```

1. **Verify DR cluster** (30-60 minutes)

   ```bash

   # Connect to DR cluster

   kubectl config use-context meridian-dr

   # Check pod status

   kubectl get pods -n production

   # Verify database replication

   # Verify replication for all service databases
   cockroach sql --execute="SHOW RANGES FROM DATABASE meridian_current_account;"
   cockroach sql --execute="SHOW RANGES FROM DATABASE meridian_platform;"
   ```

1. **Monitor switchover** (60-120 minutes)

   ```bash

   # Watch traffic shift

   kubectl top pods -n production

   # Check application logs

   stern -n production meridian

   # Verify no errors

   kubectl get events -n production --sort-by='.lastTimestamp'

   ```

1. **Post-recovery actions** (when primary region recovers)

   ```bash

   # Sync data back to primary region

   # Re-establish replication

   # Plan switchback during maintenance window

   ```

### Scenario 4: Ransomware / Security Breach

**Symptoms:** Encrypted files, unauthorized access, data exfiltration

**Recovery steps:**

1. **Isolate immediately**

   ```bash

   # Block all egress traffic

   kubectl delete networkpolicy meridian -n production
   kubectl apply -f - <<EOF
   apiVersion: networking.k8s.io/v1
   kind: NetworkPolicy
   metadata:
     name: lockdown
     namespace: production
   spec:
     podSelector: {}
     policyTypes:

     - Ingress
     - Egress

   EOF

   # Scale to 0 to prevent spread

   kubectl scale deployment meridian -n production --replicas=0

   ```

1. **Preserve evidence**

   ```bash

   # Export all logs

   kubectl logs -n production --all-containers --prefix > incident-logs-$(date +%s).txt

   # Export pod descriptions

   kubectl describe pods -n production > incident-pods-$(date +%s).txt

   # Export network policies and RBAC

   kubectl get networkpolicies,roles,rolebindings -n production -o yaml > incident-rbac-$(date +%s).yaml
   ```

1. **Engage security team**
   - Contact: <security@your-domain.com>
   - Escalate to CTO/CISO
   - Consider engaging external incident response firm

1. **Rebuild from clean backups**

   ```bash

   # Deploy new cluster from scratch

   # DO NOT restore from potentially compromised backups

   # Use known-good infrastructure code

   git checkout tags/v1.0.0  # Last known good version

   # Deploy with fresh credentials

   # Rotate all secrets, API keys, certificates

   ```

1. **Post-incident hardening**
   - Review all RBAC permissions
   - Audit all secrets and rotate
   - Review NetworkPolicies
   - Update security scanning rules
   - Implement additional monitoring

## Backup Verification

**Regular backup testing:**

```bash

# Weekly: Verify backups exist

aws s3 ls s3://meridian-backups/production/ --recursive | grep $(date +%Y-%m-%d)

# Monthly: Restore to staging

# Restore all service databases to staging
cockroach sql --database=staging --execute="RESTORE DATABASE meridian_current_account FROM 's3://backups/latest/current_account';"
cockroach sql --database=staging --execute="RESTORE DATABASE meridian_platform FROM 's3://backups/latest/platform';"
# Repeat for other service databases as needed

# Quarterly: Full DR drill

# Document in: docs/runbooks/dr-drill-YYYY-MM-DD.md

```

## Communication Plan

### Internal Communication

**During incident:**

- Engineering Team: [Slack channel]
- Executive Team: [Email distribution list]
- All Staff: [Status page]

**Communication cadence:**

- **P0**: Updates every 30 minutes
- **P1**: Updates every hour
- **P2**: Daily updates

### External Communication

> **⚠️ Production Setup Required**: Configure status page and customer notification process

**Status page updates:**

- Incident detected: "Investigating service degradation"
- Recovery in progress: "Implementing fix, ETA: X hours"
- Resolved: "Services restored, root cause analysis to follow"

**Customer notifications:**

- P0 incidents: Immediate notification
- P1 incidents: Notification within 4 hours
- Post-mortem: Published within 7 days

## Recovery Checklists

### Pre-Recovery Checklist

- [ ] Incident severity confirmed (P0 disaster recovery)
- [ ] Key stakeholders notified
- [ ] Backup locations verified and accessible
- [ ] Recovery environment provisioned
- [ ] Communication channels established
- [ ] Evidence preserved (for security incidents)

### Post-Recovery Checklist

- [ ] All services operational
- [ ] Data integrity verified
- [ ] Performance metrics normal
- [ ] Monitoring and alerting restored
- [ ] Security scans passing
- [ ] Customer-facing services validated
- [ ] Post-mortem scheduled
- [ ] Lessons learned documented

## Infrastructure as Code

**Critical repositories:**

- Kubernetes manifests: `meridian/deployments/k8s/`
- Terraform/infrastructure: `meridian/infrastructure/`
- CI/CD workflows: `meridian/.github/workflows/`

**Recovery from Git:**

```bash

# Clone repository

git clone https://github.com/your-org/meridian.git
cd meridian

# Check out last known good version

git checkout tags/v1.2.0

# Apply all resources

kubectl apply -k deployments/k8s/overlays/production/
```

## Testing & Drills

**Monthly drill checklist:**

1. [ ] Select recovery scenario
2. [ ] Document start time
3. [ ] Execute recovery steps
4. [ ] Document completion time
5. [ ] Compare to RTO
6. [ ] Document blockers encountered
7. [ ] Update runbook with improvements

**Drill report template:**

```text
Date: YYYY-MM-DD
Scenario: [Database corruption / Cluster failure / etc.]
Start time: HH:MM
Completion time: HH:MM
Actual RTO: X hours Y minutes
Target RTO: Z hours

What went well:
-

What needs improvement:
-

Action items:

- [ ]

```

## Emergency Contacts

> **⚠️ Production Setup Required**: Add your organisation's emergency contacts and escalation procedures

| Role | Contact | Backup |
|------|---------|--------|
| **On-Call Engineer** | [PagerDuty] | [Phone] |
| **Database Admin** | [Email/Phone] | [Email/Phone] |
| **Infrastructure Lead** | [Email/Phone] | [Email/Phone] |
| **Security Team** | <security@your-domain.com> | [Phone] |
| **CTO** | [Email/Phone] | - |

**External contacts:**

- Cloud provider support: [Support ticket system]
- Database vendor support: [Support phone/email]
- DNS provider: [Support contact]
- Certificate authority: [Support contact]

## Backup Locations

> **⚠️ Production Setup Required**: Document your actual backup locations and access procedures

**Database backups:**

- Location: `s3://meridian-backups/production/database/`
- Frequency: Hourly incremental, daily full
- Retention: 30 days incremental, 90 days full

**Configuration backups:**

- Location: Git repository (GitHub)
- Frequency: Every commit
- Retention: Unlimited

**Cluster state backups:**

- Location: `s3://meridian-backups/production/etcd/`
- Frequency: Daily
- Retention: 7 days

## Post-Disaster Review

**Required within 7 days:**

1. Timeline of events
2. Root cause analysis
3. Actual RTO vs target RTO
4. Actual RPO vs target RPO
5. What worked well
6. What needs improvement
7. Action items with owners and due dates

**Template location:** `docs/runbooks/post-disaster-template.md` (to be created)
