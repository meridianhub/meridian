# Documentation Archive

This directory contains documentation that is no longer actively maintained but preserved for historical reference.

## Archived Documents

- [task-8-service-integration.md](task-8-service-integration.md) - Implementation report for service-to-service
integration. Preserved for historical context but superseded by actual implementation.
- [immutability-audit.md](immutability-audit.md) - Analysis document from code review. Implementation details now
covered in CONTRIBUTING.md under Code Standards.
- [rbac-testing-guide.md](rbac-testing-guide.md) - RBAC testing guidance. Content has been integrated into relevant
test documentation.
- [audit-analysis-findings.md](audit-analysis-findings.md) - One-time audit analysis report. Findings addressed.
- [panic-audit-inventory.md](panic-audit-inventory.md) - One-time panic audit inventory. Findings have been addressed.
- [database-per-service-migration.md](database-per-service-migration.md) - Database-per-service migration runbook.
Preserved for historical context.
- [ARCHITECTURE.md](ARCHITECTURE.md) - Early demo architecture diagram describing a 2-service system.
  Superseded by `services/README.md` and production deployment runbook.
- [DEMO_GUIDE.md](DEMO_GUIDE.md) - Early demo walkthrough. Superseded by current demo environment docs.
- [authentication-integration.md](authentication-integration.md) - Auth integration guide referencing old
  `internal/platform/auth` path and Keycloak. Superseded by identity service.

## When to Archive

Documents should be archived when:

- They are task-specific reports no longer relevant to ongoing work
- Their content has been integrated into permanent documentation
- They are outdated but contain historical value
- They are analysis/planning docs completed and superseded

## When NOT to Archive

Keep documents active when:

- They describe current architecture (use ADRs in `docs/adr/` instead)
- They provide operational guidance (use runbooks in `docs/runbooks/` or skills in `.claude/skills/`)
- They explain how to use the system (keep in root docs like README.md, CONTRIBUTING.md, or `docs/`)
- They are referenced by active code or other documentation
