# Claude Code Skills Integration

This document explains how documentation in this repository functions as contextual skills for Claude Code.

## Overview

ADRs (Architecture Decision Records), runbooks, and skills contain YAML frontmatter metadata that makes them
discoverable as "skills" - focused pieces of context that Claude Code can load just-in-time when needed.

**Context efficiency**: Load only relevant documentation based on conversation context, rather than loading everything.

For detailed information about creating and using skills, see [docs/skills/README.md](skills/README.md).

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
- [See complete list](skills/README.md)

**Runbooks** (in `docs/runbooks/`):

- Incident response procedures
- Disaster recovery procedures

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
```text

See [docs/skills/README.md](skills/README.md) for:

- Detailed metadata format guide
- How to create new skills
- Troubleshooting YAML frontmatter
- Best practices for triggers and instructions
