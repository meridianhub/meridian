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

### Deployment
- **[kustomize.md](kustomize.md)** - Environment-specific Kubernetes deployments

### Security
- **[security.md](security.md)** - Security scanning and vulnerability management

## Format

Skills follow the same metadata format as ADRs:

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

## Usage

These skills are designed to be:
- **Searchable** by AI assistants via triggers and descriptions
- **Actionable** with concrete commands and examples
- **Contextual** with enough detail to understand when and how to use them

## Related Documentation

- Main docs (human-focused): `../` (parent directory)
- Architecture decisions: `../adr/`
- Claude Code skills config: `../claude-code-skills.md`
