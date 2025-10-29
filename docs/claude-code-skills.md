# Claude Code Skills Integration

This document explains how ADRs (Architecture Decision Records) and runbooks in this repository function as contextual skills for Claude Code.

## Overview

ADRs and runbooks contain metadata headers that make them discoverable as "skills" - focused pieces of context that Claude Code can load just-in-time when needed, rather than loading all documentation into every conversation.

## Why This Approach?

**Context efficiency**: Instead of loading all ADRs and runbooks into every conversation (expensive and potentially distracting), Claude Code can:
1. Recognize when a decision or operational question relates to documented patterns
2. Load only the relevant ADR or runbook
3. Apply that specific context to the current task

**Standard format**: The skill metadata uses YAML frontmatter, a widely-adopted format for documentation metadata that's human-readable and machine-parseable.

## Skill Metadata Format

Each ADR and runbook starts with YAML frontmatter like this:

```yaml
---
name: adr-002-microservices-per-bian-domain
description: One microservice per BIAN domain for independent scaling, deployment, and failure isolation
triggers:
  - Designing service boundaries
  - Deciding between microservices vs monolith
  - Planning service deployment architecture
instructions: |
  Create one service per BIAN domain (FinancialAccounting, PositionKeeping, CurrentAccount).
  Each service independently deployable with own database. Use gRPC for sync communication,
  Kafka for async events.
---
```

This metadata:
- **Standard YAML frontmatter** - familiar format used across documentation tools
- **Provides structured info** for Claude Code to understand document purpose
- **Enables smart loading** based on conversation context via triggers
- **Instructions field** - concise guidance for applying the knowledge

## How Claude Code Uses These Skills

When you're working with Claude Code on the Meridian project:

1. **Architecture decisions**: If discussing service boundaries, database patterns, or platform choices, Claude Code can load relevant ADRs like:
   - ADR-0002: Microservices per BIAN domain
   - ADR-0003: Database schema migrations
   - ADR-0006: Tilt for local development

2. **Operational scenarios**: If handling incidents, Claude Code can load:
   - Incident Response runbook for active issues
   - Disaster Recovery runbook for catastrophic failures

3. **Just-in-time context**: Skills are loaded only when relevant, keeping conversations focused and token-efficient.

## Creating New Skills

### For ADRs

When creating a new ADR from `docs/adr/template.md`:

1. Copy the template (which includes YAML frontmatter placeholders)
2. Fill in the frontmatter fields:
   - `name`: Unique identifier like `adr-007-brief-slug`
   - `description`: One-line summary of the architectural decision
   - `triggers`: List of scenarios when this ADR should be referenced
   - `instructions`: Concise guidance for applying this decision

3. Write the ADR content as normal

### For Runbooks

When creating a new runbook:

1. Start with YAML frontmatter:
   ```yaml
   ---
   name: [runbook-name]
   description: [What operational scenario this covers]
   triggers:
     - [When to reference this runbook]
     - [Another trigger scenario]
   instructions: |
     [Concise operational guidance]
   ---
   ```

2. Write the runbook content with clear procedures

## Benefits

1. **Smaller context windows**: Only load what's needed for the current task
2. **Faster responses**: Less text to process means faster Claude Code responses
3. **Better focus**: Relevant context without noise from unrelated documents
4. **Self-documenting**: The metadata acts as a catalog of available knowledge
5. **Standard format**: YAML frontmatter is widely used and human-readable
6. **Tooling compatible**: Many documentation tools recognize and use YAML frontmatter

## Implementation Note

This is an adaptive use of Claude Code's skills system. While typically skills are standalone tool integrations, we're using the same metadata pattern to make our documentation "skillable" - loadable on demand when contextually relevant.

The key insight: **good documentation with structured metadata becomes intelligent, context-aware documentation**.
