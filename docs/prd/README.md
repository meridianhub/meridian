# Product Requirements Documents (PRDs)

This directory contains Product Requirements Documents for Meridian features. Like ADRs, these
documents are configured as Claude Code skills that automatically load when relevant triggers match.

## PRD Status Overview

### Status Definitions

| Status | Meaning |
|--------|---------|
| **Implemented** | All tasks completed (remaining tasks cancelled or deferred) |
| **Paused** | Mostly implemented with deferred items remaining |
| **In Progress** | Active work ongoing |
| **Not Started** | PRD exists but no Task Master tasks created |

### Git-Tracked PRDs (`docs/prd/`)

| PRD | Status | Task Master Tag | Tasks |
|-----|--------|-----------------|-------|
| [Durable Execution Engine](durable-execution-engine.md) | Implemented | `starlark-saga-orchestration` | 24/24 done |
| [Internal Bank Account](internal-bank-account.md) | Implemented | `internal-bank-account` | 33/33 done |
| [Market Information Management](market-information-management.md) | Implemented | `market-information-management` | 17/18 done, 1 cancelled |
| [Meridian Edge](meridian-edge.md) | Not Started | N/A | N/A |
| [Starlark Saga Orchestration (Core)](starlark-saga-orchestration-core.md) | Implemented | `starlark-saga-orchestration` | 24/24 done |
| [Starlark Typed Service Clients](starlark-typed-service-clients.md) | Implemented | `starlark-typed-clients` | 10/10 done |
| [Universal Asset System](universal-asset-system.md) | Implemented | `universal-asset-system` | 36/36 done |

### Task Master PRDs (`.taskmaster/docs/`)

These PRDs are used to generate Task Master tasks and are not tracked in git.

#### Implemented

| PRD | Task Master Tag | Tasks |
|-----|-----------------|-------|
| `prd-infra.md` | `1-infra-completed-2025-10-30` | 11/11 done |
| `prd-api-contracts.md` | `2-api-contracts-completed-2024-12-15` | 16/19 done, 3 cancelled |
| `prd-platform.md` | `3-platform` | 10/10 done |
| `prd-financial-accounting.md` | `4-financial-accounting` | 9/10 done, 1 cancelled |
| `prd-position-keeping.md` | `5-position-keeping` | 15/15 done |
| `prd-current-account.md` | `6-current-account` | 10/10 done |
| `prd-payment-order.md` | `7-payment-order` | 19/19 done |
| `prd-99-horizon-proof.md` | `99-horizon-proof` | 10/10 done |
| `go-compile-time-safety-prd.md` | `go-compile-time-safety-completed-2024-12-15` | 7/10 done, 3 cancelled |
| `prd-technical-debt-remediation.md` | `tech-debt-cleanup` | 71/71 done |
| `prd-position-keeping-balance-ownership.md` | `position-keeping-balance` | 17/17 done |
| `prd-database-per-service.md` | `database-per-service` | 14/15 done, 1 cancelled |
| `prd.md` (Master) | `master` | 5/5 done |

#### Paused (Deferred Items Remain)

| PRD | Task Master Tag | Tasks | Deferred |
|-----|-----------------|-------|----------|
| `prd-multi-tenancy.md` | `8-multi-tenancy` | 89/95 done, 5 cancelled | 1 deferred |
| `ledger-integrity-prd.md` | `ledger-integrity` | 14/15 done | 1 deferred |
| `prd-audit-foundation.md` | `75-async-audit` | 19/20 done | 1 deferred |
| `prd-bian-alignment.md` | `bian-alignment` | 6/15 done, 4 cancelled | 5 deferred |
| `prd-iso-standards-alignment.md` | `iso-standards-alignment` | 4/15 done, 6 cancelled | 5 deferred |

#### In Progress

| PRD | Task Master Tag | Tasks |
|-----|-----------------|-------|
| N/A (saga-script-versioning has no dedicated PRD) | `saga-script-versioning` | 2/3 done, 1 in-progress |

#### Implemented Under Other Tags (No Dedicated Tag)

These PRDs were implemented by appending tasks to existing tags rather than creating dedicated tags.

| PRD | Where Tracked | Notes |
|-----|---------------|-------|
| `prd-party-service.md` | `8-multi-tenancy`, `bian-alignment`, `tech-debt-cleanup`, `75-async-audit` | Party service fully operational at `services/party/` |
| `api-gateway-service-prd.md` | `8-multi-tenancy`, `tech-debt-cleanup` | Gateway service at `services/gateway/`, JWT auth, subdomain routing |
| `async-schema-provisioning-prd.md` | `8-multi-tenancy` (tasks 46-48) | Schema provisioner integrated into InitiateTenant workflow |
| `external-tenant-isolation-prd.md` | `8-multi-tenancy`, `tech-debt-cleanup` | Subdomain resolution, slug cache, auth interceptor |
| `prd-current-account-refactor.md` | `223-shared-client-patterns`, `position-keeping-balance`, `universal-asset-system`, `starlark-saga-orchestration` | Refactored across multiple initiatives |
| `prd-docs-sync-q1-2026.md` | `tech-debt-cleanup` (task 53) | Documentation sync completed |
| `prd-multi-tenancy-phase2.md` | `8-multi-tenancy` | Most critical gaps addressed (89/95 done), 1 deferred (K8s wildcard ingress) |

#### Partially Addressed

| PRD | Where Tracked | Remaining |
|-----|---------------|-----------|
| `prd-concurrency-reliability-q1-2026.md` | `tech-debt-cleanup` (deadlock fix), `8-multi-tenancy` (retry logic) | Needs review to identify unaddressed items |

#### Not Started

| PRD | Description | Date Created |
|-----|-------------|--------------|
| `prd-internal-bank-account-integration-phase2.md` | Phase 2: FA, Payment Order, Position Keeping integration | 2026-01-15 |

### Other Documents (`.taskmaster/docs/`)

| File | Type |
|------|------|
| `panic-audit-inventory.md` | Audit inventory (reference doc, not a PRD) |

## What are PRDs?

PRDs define the requirements, design, and implementation approach for significant features. They:

- Document the "what" and "why" of features before implementation
- Provide context for AI assistants during development
- Define Task Master tags for tracking work
- Link to related ADRs for architectural decisions

## Categories

### Core Platform

- [Universal Asset System](universal-asset-system.md) - Multi-asset support with dimensional safety
- [Internal Bank Account](internal-bank-account.md) - BIAN service for clearing, nostro/vostro accounts
- [Market Information Management](market-information-management.md) - BIAN service for market data and pricing
- [Starlark Typed Service Clients](starlark-typed-service-clients.md) - Type-safe service handlers for saga orchestration

### Execution Engine

- [Starlark Saga Orchestration (Core)](starlark-saga-orchestration-core.md) - Runtime-configurable saga definitions
- [Durable Execution Engine](durable-execution-engine.md) - Resilience layer for saga execution

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
4. Set **Status** to `Not Started` until Task Master tasks are created
5. Update this README with the new entry
6. If applicable, create related ADRs for architectural decisions

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
