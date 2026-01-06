# Product Requirements Documents (PRDs)

This directory contains Product Requirements Documents for Meridian features. Like ADRs, these
documents are configured as Claude Code skills that automatically load when relevant triggers match.

## Master Roadmap

**Start here:** [000-master-roadmap.md](000-master-roadmap.md) - The PRD of PRDs

## What are PRDs?

PRDs define the requirements, design, and implementation approach for significant features. They:

- Document the "what" and "why" of features before implementation
- Provide context for AI assistants during development
- Define Task Master tags for tracking work
- Link to related ADRs for architectural decisions

## PRD Index

| PRD | Title | Status | Task Master Tag |
|-----|-------|--------|-----------------|
| [**000-Master Roadmap**](000-master-roadmap.md) | PRD of PRDs - all work streams | Draft | `master-roadmap` |
| [Universal Asset System](universal-asset-system.md) | Multi-asset ledger support | Draft | `universal-asset-system` |
| [Temporal Model Alignment](temporal-model-alignment-q1-2026.md) | Align with ADR-0017 periods | Draft | `temporal-model-alignment` |
| [Platform Foundation](platform-foundation-q1-2026.md) | Deploy as hosted SaaS | Draft | `platform-foundation` |

## Categories

### Core Platform

- [Universal Asset System](universal-asset-system.md) - Multi-asset support with dimensional safety
- [Temporal Model Alignment](temporal-model-alignment-q1-2026.md) - Explicit period columns per ADR-0017

### Infrastructure & Operations

- [Platform Foundation](platform-foundation-q1-2026.md) - Deployment, identity, self-billing, UI, Stripe

## How PRD Skills Work

Each PRD has YAML frontmatter that enables Claude Code to automatically load it when relevant:

```yaml
---
name: prd-universal-asset-system
description: Extend Meridian's ledger from fiat-only to multi-asset support
triggers:
  - Implementing multi-asset or universal asset support
  - Working on InstrumentType, Quantity, or asset definitions
instructions: |
  Key patterns and guidance for implementing this PRD...
---
```

Claude Code loads PRDs when:

- Discussion topics match the triggers
- Keywords align with the PRD's domain
- The instructions would be helpful for the current task

## Creating New PRDs

1. Create a new markdown file with descriptive name (e.g., `feature-name.md`)
2. Add YAML frontmatter with:
   - `name`: Unique identifier (e.g., `prd-feature-name`)
   - `description`: One-line summary
   - `triggers`: List of scenarios when this PRD should load
   - `instructions`: Key guidance for Claude Code
3. Write the PRD content following the existing structure
4. Update this README with the new entry
5. If applicable, create related ADRs for architectural decisions

## PRD Structure

PRDs typically include:

- **Overview**: Goals and non-goals
- **Requirements**: Functional and non-functional requirements
- **Design**: Technical approach and contracts
- **Work Streams**: Implementation phases with Task Master tags
- **Success Metrics**: How to measure completion

## Related Documentation

- [Architecture Decision Records](../adr/README.md) - Architectural decisions
- [Claude Code Skills](../claude-code-skills.md) - How skills work in Meridian
