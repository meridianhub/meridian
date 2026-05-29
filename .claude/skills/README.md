# Claude Code Skills

This directory holds **native Claude Code skills** for working with the Meridian codebase.
Each skill is a subdirectory containing a `SKILL.md` file, which Claude Code discovers
automatically - no settings opt-in required. Skills load just-in-time when the conversation
matches a skill's `description`, so only relevant context is pulled in.

```
.claude/skills/
  <skill-name>/
    SKILL.md     # entrypoint - frontmatter + guide
```

The directory name (`<skill-name>`) is the skill's command. The frontmatter `description`
is what Claude matches against to decide when to load the skill.

## Available Skills

### Development Workflow

- **[tilt](tilt/SKILL.md)** - Fast Kubernetes development with Tilt and live reload
- **[docker](docker/SKILL.md)** - Docker configuration and multi-stage builds
- **[schema-evolution](schema-evolution/SKILL.md)** - Protobuf schema evolution with buf breaking change detection
- **[starlark-saga](starlark-saga/SKILL.md)** - Generate Starlark saga scripts following Meridian conventions
<!-- markdownlint-disable-next-line MD013 -->
- **[event-triggered-sagas](event-triggered-sagas/SKILL.md)** - Configure sagas that fire reactively on Kafka events with CEL filters

### Deployment

- **[kustomize](kustomize/SKILL.md)** - Environment-specific Kubernetes deployments

### Security

- **[security](security/SKILL.md)** - Security scanning and vulnerability management

### Testing

- **[testing](testing/SKILL.md)** - Testing standards: await package, testcontainers, defensive testing

### Tooling

- **[generate-llm-blueprint](generate-llm-blueprint/SKILL.md)** - Generate codebase blueprint for LLMs

## Additional Skill Locations

Other docs also carry YAML frontmatter so AI assistants can load them as context. These are
reference material, not native `SKILL.md` skills:

### Architecture Decision Records (ADRs)

**Location:** [`../../docs/adr/`](../../docs/adr/README.md)

ADRs capture architectural decisions with context and rationale. Load when discussing:

- Service design and boundaries
- Technology choices and trade-offs
- Implementation patterns

### Runbooks

**Location:** [`../../docs/runbooks/`](../../docs/runbooks/)

Operational procedures for incidents and recovery. Load when handling:

- Security incidents or outages
- Disaster recovery scenarios
- Service degradation

### Product Requirements (PRDs)

**Location:** [`../../docs/prd/`](../../docs/prd/)

Product specifications and feature designs. Load when understanding:

- Feature scope and requirements
- Business context and goals

### Services

Service documentation includes YAML frontmatter for Claude Code discovery:

**Domain Services:**

- **[current-account](../../services/current-account/README.md)** - BIAN current account with lien-based fund reservations
- **[position-keeping](../../services/position-keeping/README.md)** - Transaction log and balance queries
- **[financial-accounting](../../services/financial-accounting/README.md)** - Double-entry bookkeeping
- **[payment-order](../../services/payment-order/README.md)** - Payment saga orchestrator
- **[party](../../services/party/README.md)** - Party reference data directory
- **[internal-account](../../services/internal-account/README.md)** - Internal account registry
- **[reference-data](../../services/reference-data/README.md)** - Instrument definitions and CEL validation

**Infrastructure Services:**

- **[gateway](../../services/api-gateway/README.md)** - Multi-tenant API gateway
- **[tenant](../../services/tenant/README.md)** - Multi-tenant platform infrastructure
- **[audit-worker](../../services/audit-worker/README.md)** - Fallback audit logging worker

**Shared Modules:**

- **[shared](../../shared/README.md)** - Shared packages and platform libraries
- **[bootstrap](../../shared/platform/bootstrap/README.md)** - Service initialization
- **[audit](../../shared/platform/audit/README.md)** - Audit hook helpers
- **[observability](../../shared/platform/observability/README.md)** - OpenTelemetry tracing

## Skill Frontmatter Format

Claude Code reads two frontmatter fields. Everything else is ignored (but harmless, and useful
as human-readable hints):

```yaml
---
name: skill-name            # optional; defaults to the directory name
description: What this skill covers and when to use it. Include trigger phrases here -
  this text is what Claude matches against to decide whether to load the skill.
---

# Skill Title

Content starts here...
```

- **`description`** is the only field that affects discovery. Make it specific and include
  "use when..." phrasing so the skill loads at the right moments.
- **`name`** is a display label; the **directory name** is what becomes the skill command.
- Extra fields like `triggers:` and `instructions:` are retained in some of these skills as
  readable hints but are **not** read by the loader.

## Creating New Skills

1. Create a directory: `.claude/skills/<skill-name>/`
2. Add `SKILL.md` with a `description` (lead with what it does + when to use it)
3. Write the guide: procedures, command examples, troubleshooting
4. Add it to the **Available Skills** list above

**Naming**: lowercase-with-hyphens, specific and task-focused
(`schema-evolution`, not `protobuf-stuff`).

## How Skills Are Used

Claude Code loads skills just-in-time based on conversation context:

1. **Match**: the conversation matches a skill's `description`
2. **Load**: only that skill's content is pulled into context
3. **Apply**: Claude follows the skill's guidance

Edits, additions, and removals under `.claude/skills/` take effect within the current session.

## Related Documentation

- Claude Code skills convention: [../../docs/claude-code-skills.md](../../docs/claude-code-skills.md)
