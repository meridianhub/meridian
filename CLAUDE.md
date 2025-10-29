# Meridian Project - Claude Code Instructions

## Skills: ADRs and Runbooks

This project uses ADRs (Architecture Decision Records) and runbooks as contextual skills. When the conversation context matches the triggers defined in these documents, load them for relevant guidance.

**When to load these skills:**
- Reference ADRs when discussing architectural decisions, service design, or implementation patterns
- Reference runbooks when handling operational scenarios, incidents, or system recovery

## Available Architecture Decision Records (ADRs)

All ADRs are located in `docs/adr/` and should be loaded when their triggers match:

- `docs/adr/0001-record-architecture-decisions.md` - Foundation for using ADRs in this project
- `docs/adr/0002-microservices-per-bian-domain.md` - Service boundary decisions, microservices architecture
- `docs/adr/0003-database-schema-migrations.md` - Atlas for database migrations
- `docs/adr/0004-event-schema-evolution.md` - Protobuf events with buf breaking change detection
- `docs/adr/0005-adapter-pattern-layer-translation.md` - Layer translation patterns between domain/persistence/events
- `docs/adr/0006-tilt-local-development.md` - Local development environment with Tilt

## Available Runbooks

All runbooks are located in `docs/runbooks/` and should be loaded when operational scenarios match:

- `docs/runbooks/incident-response.md` - Procedures for security incidents, outages, and service degradation
- `docs/runbooks/disaster-recovery.md` - Full system recovery from catastrophic failures

## How Skills Work

Each ADR and runbook has YAML frontmatter like this:

```yaml
---
name: adr-002-microservices-per-bian-domain
description: One microservice per BIAN domain for independent scaling
triggers:
  - Designing service boundaries
  - Deciding between microservices vs monolith
instructions: |
  Create one service per BIAN domain. Each service independently deployable
  with own database. Use gRPC for sync, Kafka for async.
---
```

Claude Code automatically loads relevant skills when:
- Discussion topics match the triggers
- Keywords align with the skill's domain
- The instructions would be helpful for the current task

## Benefits

1. **Efficient context**: Load only relevant documentation
2. **Faster responses**: Smaller context windows mean faster processing
3. **Better focus**: Get relevant guidance without unrelated information
4. **Self-updating**: As ADRs evolve, skills automatically reflect changes

## Creating New Skills

When creating new ADRs or runbooks, follow the template in `docs/adr/template.md` which includes the required YAML frontmatter structure.

### Skill Frontmatter Fields

- **name**: Unique identifier (e.g., `adr-007-brief-slug`)
- **description**: One-line summary of what this skill covers
- **triggers**: List of scenarios when this skill is relevant
- **instructions**: Concise guidance for applying this knowledge

## Additional Documentation

See `docs/claude-code-skills.md` for detailed information about the skills system and how to create effective skill metadata.
