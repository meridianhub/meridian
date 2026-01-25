# Product Requirements Documents (PRDs)

This directory contains Product Requirements Documents for Meridian features. Like ADRs, these
documents are configured as Claude Code skills that automatically load when relevant triggers match.

## What are PRDs?

PRDs define the requirements, design, and implementation approach for significant features. They:

- Document the "what" and "why" of features before implementation
- Provide context for AI assistants during development
- Define Task Master tags for tracking work
- Link to related ADRs for architectural decisions

## PRD Index

| PRD | Title | Status | Task Master Tag |
|-----|-------|--------|-----------------|
| [Universal Asset System](universal-asset-system.md) | Multi-asset ledger support | Draft | `universal-asset-system` |
| [Internal Bank Account](internal-bank-account.md) | Non-customer-facing account management | Draft | `internal-bank-account` |
| [Market Information Management](market-information-management.md) | Market data, pricing feeds, and external datasets | Draft | `market-information-management` |
| [Meridian Edge](meridian-edge.md) | Embedded financial kernel for IoT/Browser | Proposed | `meridian-edge` |
| [Starlark Typed Service Clients](starlark-typed-service-clients.md) | Type-safe service handlers for saga orchestration | Draft | `starlark-typed-clients` |

## Categories

### Core Platform

- [Universal Asset System](universal-asset-system.md) - Multi-asset support with dimensional safety
- [Internal Bank Account](internal-bank-account.md) - BIAN service for clearing, nostro/vostro accounts
- [Market Information Management](market-information-management.md) - BIAN service for market data and pricing
- [Starlark Typed Service Clients](starlark-typed-service-clients.md) - Type-safe service handlers for saga orchestration

### Deployment Targets

- [Meridian Edge](meridian-edge.md) - Embedded modular monolith for IoT devices and browser (WASM)

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
