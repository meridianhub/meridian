# Claude Code Skills Integration

This document explains how documentation in this repository functions as contextual skills for Claude Code.

## Overview

ADRs (Architecture Decision Records), runbooks, and skills contain YAML frontmatter metadata that makes them
discoverable as "skills" - focused pieces of context that Claude Code can load just-in-time when needed.

**Context efficiency**: Load only relevant documentation based on conversation context, rather than loading everything.

For detailed information about creating and using skills, see [docs/skills/README.md](skills/README.md).

## AI Navigability Docs

For AI contributors and new engineers, these documents describe the codebase structure:

- [architecture-layers.md](architecture-layers.md) - 8-layer functional grouping with service-to-layer mapping
- [patterns.md](patterns.md) - 6 cross-service patterns with canonical locations
- [data-flows.md](data-flows.md) - 4 sequence diagrams: payment, audit, tenant provisioning, manifest apply
- [saga-handler-loading.md](saga-handler-loading.md) - Starlark saga runtime loading flow
- [service-readme-template.md](service-readme-template.md) - required structure for per-service READMEs
- [../cookbook/README.md](../cookbook/README.md) - pattern templates vs reference-data distinction

Every service has its own `README.md` following the template. When a service-level question
comes up, read that service's README first.

## Available Skills

**Architecture Decision Records** (in `docs/adr/`):

- ADR-0002: Microservices per BIAN domain
- ADR-0003: Database schema migrations
- ADR-0004: Event schema evolution
- ADR-0005: Adapter pattern for layer translation
- ADR-0006: Tilt for local development
- [See complete list](adr/README.md)

**Operational Skills** (in `docs/skills/`):

- Docker configuration
- Tilt and Kubernetes development
- Schema evolution with protobuf
- Kustomize deployments
- Security scanning
- Testing standards (await, testcontainers, defensive testing)
- [See complete list](skills/README.md)

**Runbooks** (in `docs/runbooks/`):

- Incident response procedures
- Disaster recovery procedures
- Database-per-service migration guide

**Service Documentation** (in `services/*/`):

BIAN service domains:

- [CurrentAccount](../services/current-account/README.md) - Customer-facing account management
- [FinancialAccounting](../services/financial-accounting/README.md) - Double-entry general ledger
- [Party](../services/party/README.md) - Customer and party reference data
- [PaymentOrder](../services/payment-order/README.md) - Payment execution and settlement
- [PositionKeeping](../services/position-keeping/README.md) - Pre-ledger transaction log

Infrastructure services:

- [Tenant](../services/tenant/README.md) - Multi-tenant platform management

## Skill Metadata Format

All skills use YAML frontmatter:

```yaml
---
name: skill-name
description: Brief description
triggers:

  - When to use this

instructions: |
  Quick guidance on applying this knowledge
---
```

See [docs/skills/README.md](skills/README.md) for:

- Detailed metadata format guide
- How to create new skills
- Troubleshooting YAML frontmatter
- Best practices for triggers and instructions
