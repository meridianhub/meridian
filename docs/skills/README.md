# Skills Documentation

This directory contains operational skills documentation for AI assistants working with the Meridian codebase.

Each skill document includes:

- **Frontmatter metadata** (name, description, triggers, instructions)
- **Detailed guide** for performing the skill
- **Examples and troubleshooting**

## Available Skills

### Development Workflow

- **[tilt.md](tilt.md)** - Fast Kubernetes development with Tilt and live reload
- **[docker.md](docker.md)** - Docker configuration and multi-stage builds
- **[schema-evolution.md](schema-evolution.md)** - Protobuf schema evolution with buf breaking change detection
- **[starlark-saga.md](starlark-saga.md)** - Generate Starlark saga scripts following Meridian conventions
<!-- markdownlint-disable-next-line MD013 -->
- **[event-triggered-sagas.md](event-triggered-sagas.md)** - Configure sagas that fire reactively on Kafka events with CEL filters

### Deployment

- **[kustomize.md](kustomize.md)** - Environment-specific Kubernetes deployments

### Security

- **[security.md](security.md)** - Security scanning and vulnerability management

### Testing

- **[testing.md](testing.md)** - Testing standards: await package, testcontainers, defensive testing

### Tooling

- **[generate-llm-blueprint.md](generate-llm-blueprint.md)** - Generate codebase blueprint for LLMs

## Additional Skill Locations

Skills are also found in these directories - each document has YAML frontmatter with triggers:

### Architecture Decision Records (ADRs)

**Location:** [`../adr/`](../adr/README.md)

ADRs capture architectural decisions with context and rationale. Load when discussing:

- Service design and boundaries
- Technology choices and trade-offs
- Implementation patterns

### Runbooks

**Location:** [`../runbooks/`](../runbooks/)

Operational procedures for incidents and recovery. Load when handling:

- Security incidents or outages
- Disaster recovery scenarios
- Service degradation

### Product Requirements (PRDs)

**Location:** [`../prd/`](../prd/)

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

- **[migrations](../../shared/migrations/README.md)** - Database migrations
- **[bootstrap](../../shared/platform/bootstrap/README.md)** - Service initialization
- **[audit](../../shared/platform/audit/README.md)** - Audit hook helpers
- **[observability](../../shared/platform/observability/README.md)** - OpenTelemetry tracing

## Skill Metadata Format

Skills use YAML frontmatter at the start of each document:

```yaml
---
name: skill-name
description: Brief description of what this skill covers
triggers:

  - When to use this skill
  - Situations that require this knowledge

instructions: |
  Quick summary of how to use this skill.
  Key commands and workflows.
---
```

**Required fields**:

- `name`: Unique identifier (e.g., `tilt-development`, `schema-evolution`)
- `description`: One-line summary (~50-80 chars)
- `triggers`: List of scenarios when this skill is relevant
- `instructions`: 2-3 sentences of actionable guidance

**Format notes**:

- Use YAML frontmatter with `---` delimiters
- First `---` must be on line 1 (no blank lines before)
- Use spaces for indentation (not tabs)
- Multi-line `instructions` require pipe (`|`) character

## Creating New Skills

1. **Copy an existing skill as a template** - Use files like `tilt.md` or `docker.md` in this directory as starting
points
2. **Fill in YAML frontmatter metadata**:
   - `name`: Choose a descriptive name (lowercase-with-hyphens)
   - `description`: Write a concise one-line summary
   - `triggers`: List 2-4 specific scenarios when this skill applies
   - `instructions`: Provide 2-3 sentences of actionable guidance with key commands
3. **Write the skill content** - Include:
   - Detailed procedures with concrete examples
   - Command-line examples with expected output
   - Troubleshooting section for common issues
4. **Add to this README** in the appropriate category above

**Naming conventions**:

- Use lowercase with hyphens: `my-skill-name`
- Be specific: `tilt-development` not just `tilt`
- Focus on the task: `schema-evolution` not `protobuf-stuff`

## How Skills Are Used

Claude Code can load skills just-in-time based on conversation context:

1. **Pattern matching**: Triggers match conversation topics
2. **Context loading**: Only relevant skills are loaded
3. **Guidance application**: Instructions provide actionable steps

**Example**: When discussing "Kubernetes local development", Claude Code might load the `tilt.md` skill to provide
specific guidance.

## Troubleshooting

### YAML Syntax Errors

**Symptoms**: Skill doesn't load or metadata appears as text

**Common fixes**:

- Ensure `---` delimiters are on their own lines
- Use spaces (not tabs) for indentation
- Validate with `yq` command or online YAML validator
- Quote strings with special characters
- Use `|` for multi-line instructions

**Valid example**:

```yaml
---
name: example-skill
description: Example skill description
triggers:

  - Example scenario

instructions: |
  Line one of instructions.
  Line two continues here.
---

# Skill Title

Content starts here...
```

### Skills Not Loading

**Possible causes**:

- Triggers don't match conversation context
- YAML syntax errors
- File not listed in project's `CLAUDE.md` configuration (if used)
- Missing required fields

**Debug steps**:

1. Validate YAML frontmatter syntax
2. Make triggers more specific and descriptive
3. If using a `CLAUDE.md` configuration file, ensure the skill is listed there
4. Verify all four required fields are present

## Related Documentation

- Claude Code skills system: `../claude-code-skills.md` - How the skills system works
