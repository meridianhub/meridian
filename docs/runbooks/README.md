# Runbooks

This directory contains operational runbooks for incident response, recovery procedures, and
troubleshooting guides. Each runbook provides step-by-step procedures for specific operational
scenarios.

## Available Runbooks

### Deployment

- **[production-deployment.md](production-deployment.md)** - Production deployment guide covering
  infrastructure prerequisites, service startup order, health verification, and rollback

### Emergency Response

- **[incident-response.md](incident-response.md)** - General incident response procedures, escalation
  paths, and communication protocols
- **[disaster-recovery.md](disaster-recovery.md)** - Disaster recovery procedures, backup
  restoration, and business continuity
- **[saga-failure-recovery.md](saga-failure-recovery.md)** - Recovering from failed saga executions,
  compensation, and rollback
- **[saga-validation-failure.md](saga-validation-failure.md)** - Responding to saga validation failures
  in upload, activation, and CI pipelines

### Operations & Troubleshooting

- **[database-per-service-migration.md](database-per-service-migration.md)** - Procedures for
  migrating to database-per-service architecture
- **[internal-account-operations.md](internal-account-operations.md)** - Internal account
  operational procedures and reconciliation
- **[market-information-operations.md](market-information-operations.md)** - Market data operations,
  pricing updates, and rate management
- **[troubleshooting-saga-handlers.md](troubleshooting-saga-handlers.md)** - Debugging Starlark saga
  scripts, handler errors, and execution failures
- **[event-router.md](event-router.md)** - Event-router service operations: dispatching Kafka events
  to tenant sagas, CEL filter troubleshooting, chain depth incidents

## Runbook Structure

Each runbook typically includes:

1. **Overview** - What situation this runbook addresses and when to use it
2. **Prerequisites** - Required access, tools, or knowledge before starting
3. **Procedures** - Step-by-step instructions with command examples
4. **Verification** - How to confirm successful resolution
5. **Rollback** - How to reverse changes if needed (where applicable)
6. **Escalation** - When and how to escalate to senior engineers or on-call

## When to Use Runbooks

Use runbooks when:

- Responding to production incidents or outages
- Performing operational procedures (migrations, updates, etc.)
- Troubleshooting complex system behaviors
- Training new team members on operational procedures
- Documenting resolution steps for recurring issues

## Creating New Runbooks

1. **Identify the scenario** - What operational situation needs documentation?
2. **Choose a template** - Use an existing runbook as a starting point
3. **Write step-by-step procedures** - Be specific with commands and expected output
4. **Test the procedures** - Verify steps work in a non-production environment
5. **Add to this index** - Update the appropriate category above
6. **Review with team** - Get feedback from engineers who might use it

### Runbook Template

```markdown
# [Runbook Title]

## Overview

[Brief description of what situation this runbook addresses]

## Prerequisites

- Access: [Required permissions or credentials]
- Tools: [Required CLI tools, scripts, or access]
- Knowledge: [Prerequisite understanding or related docs]

## Symptoms

- [Observable symptom 1]
- [Observable symptom 2]
- [Observable symptom 3]

## Procedures

### Step 1: [Action]

\```bash
# [Command with explanation]
command --flag value
\```

**Expected output:**
\```
[Show expected output]
\```

### Step 2: [Next Action]

[Continue with detailed steps...]

## Verification

[How to confirm the issue is resolved]

\```bash
# Verification command
verify-command
\```

## Rollback (if applicable)

[Steps to reverse changes if procedure fails]

## Escalation

**When to escalate:**
- [Condition 1]
- [Condition 2]

**Who to contact:**
- [On-call engineer]
- [Team lead]
- [Platform team]

## Related Documentation

- [Link to related ADR]
- [Link to related skill]
- [Link to service documentation]
```

## Related Documentation

- [Skills](../skills/README.md) - Development and operational skills documentation
- [ADRs](../adr/README.md) - Architectural decisions that inform these procedures
- [PRDs](../prd/README.md) - Feature specifications and designs
- [Architecture](../architecture/) - System architecture documentation

## Emergency Contacts

For production incidents:

1. Check [incident-response.md](incident-response.md) for escalation procedures
2. Use team communication channels (Slack, PagerDuty, etc.)
3. Follow on-call rotation for after-hours incidents
4. Document all actions in incident tracking system

## Contributing

When you resolve an incident or perform an operational procedure:

1. **Document your steps** - What worked, what didn't
2. **Update existing runbooks** - Add learnings or corrections
3. **Create new runbooks** - If no existing documentation covers the scenario
4. **Share with team** - Review documentation with engineers who might use it

**Keep runbooks current** - Runbooks are living documents. Update them when:

- Procedures change
- New tools are introduced
- Better approaches are discovered
- Errors or omissions are found
