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

```mermaid
stateDiagram-v2
    [*] --> Not_Started: PRD created
    Not_Started --> In_Progress: Parse PRD, create tasks
    In_Progress --> Paused: Some tasks deferred
    In_Progress --> Implemented: All tasks complete
    Paused --> In_Progress: Resume work
    Implemented --> [*]
```

### Git-Tracked PRDs (`docs/prd/`)

#### Implemented

| PRD | Task Master Tag | Tasks |
|-----|-----------------|-------|
| [Codebase Health Audit](012-codebase-health-audit.md) | `codebase-health-audit` | 22/22 done |
| [Durable Execution Engine](005-durable-execution-engine.md) | `starlark-saga-orchestration` | 24/24 done |
| [Internal Account](002-internal-account.md) | `internal-account` | 33/33 done |
| [Market Information Management](004-market-information-management.md) | `market-information-management` | 17/18 done, 1 cancelled |
| [Market Data & Dynamic Pricing](016-market-data-dynamic-pricing.md) | `market-data-dynamic-pricing` | 12/12 done |
| [Production Readiness Review](009-production-readiness-review.md) | `production-readiness` | 10/10 done |
| [Reconciliation Service](013-reconciliation-service.md) | `reconciliation-service-completed-2026-02-12` | 24/24 done |
| [Reconciliation gRPC Wiring](017-reconciliation-grpc-wiring.md) | `reconciliation-service-completed-2026-02-12` (tasks 17-23) | Included in reconciliation-service completion |
| [Starlark Saga Orchestration (Core)](006-starlark-saga-orchestration-core.md) | `starlark-saga-orchestration` | 24/24 done |
| [Starlark Typed Service Clients](007-starlark-typed-service-clients.md) | `starlark-typed-clients` | 10/10 done |
| [Stripe Connect](015-stripe-connect.md) | `stripe-connect` | 12/12 done |
| [Universal Asset System](001-universal-asset-system.md) | `universal-asset-system` | 36/36 done |
| [Starlark Service Bindings](008-starlark-service-bindings.md) | N/A (tracked across other tags) | Implemented 2026-02-04 |
| [Starlark Testing Framework](010-starlark-testing-framework.md) | N/A (tracked across other tags) | Implemented 2026-02-06 |
| [Structured Mapping Layer](024-structured-inbound-mapping.md) | `structured-mapping-layer` | 16/16 done |
| [Valuation Service](011-valuation-service.md) | `valuation-engine` | 14/14 done |

#### Paused (Deferred Items Remain)

| PRD | Task Master Tag | Tasks | Deferred |
|-----|-----------------|-------|----------|
| [Control Plane](014-control-plane.md) | `control-plane-completed-2026-02-10` | 12/13 done | 1 deferred |

#### In Progress

| PRD | Task Master Tag | Tasks |
|-----|-----------------|-------|
| [Platform Scheduler](021-platform-scheduler.md) | `platform-scheduler` | 3/10 done, 1 review, 6 pending |

#### Near Completion

| PRD | Task Master Tag | Tasks | Remaining |
|-----|-----------------|-------|-----------|
| [Stripe Connect Wiring](015-stripe-connect.md) | `stripe-connect-wiring` | 9/10 done | Task 10: E2E integration test (in-progress) |
| [Reconciliation Phase 2](013-reconciliation-service.md) | `reconciliation-service-phase2` | 9/10 done | Task 10: E2E test suite (review) |

#### Planned (Task Master Tags Created)

| PRD | Task Master Tag | Description |
|-----|-----------------|-------------|
| [Current Account Withdrawal Persistence](018-current-account-withdrawal-persistence.md) | `account-service-wiring` | Wire withdrawal-by-ID gRPC handlers in current-account service |
| [Internal Account - Position Keeping Client](019-internal-account-position-keeping-client.md) | `account-service-wiring` | Wire Position Keeping gRPC client in internal-account service |
| [Party KYC/AML Provider Integration](020-party-kyc-aml-provider-integration.md) | `party-kyc-aml` | External KYC/AML provider adapter for production party onboarding |

#### Not Started

| PRD | Description |
|-----|-------------|
| [Asset-Agnostic Accounts](028-asset-agnostic-accounts.md) | Generalize account fields for non-fiat asset classes |
| [Meridian Edge](003-meridian-edge.md) | Embedded modular monolith for IoT devices and browser (WASM) |
| [MCP Server](027-mcp-server.md) | Model Context Protocol server bridging LLMs to Meridian Core |
| [Operational Gateway](029-operational-gateway.md) | Bidirectional asset-agnostic gateway for outbound instructions and inbound messages |

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
| `prd-technical-debt-remediation.md` | `tech-debt-cleanup` | 84/84 done |
| `prd-position-keeping-balance-ownership.md` | `position-keeping-balance` | 17/17 done |
| `prd-database-per-service.md` | `database-per-service` | 14/15 done, 1 cancelled |
| `prd.md` (Master) | `master` | 5/5 done |
| N/A (saga-script-versioning has no dedicated PRD) | `saga-script-versioning` | 34/34 done |

#### Paused (Deferred Items Remain)

| PRD | Task Master Tag | Tasks | Deferred |
|-----|-----------------|-------|----------|
| `prd-multi-tenancy.md` | `8-multi-tenancy` | 89/95 done, 5 cancelled | 1 deferred |
| `ledger-integrity-prd.md` | `ledger-integrity` | 14/15 done | 1 deferred |
| `prd-audit-foundation.md` | `75-async-audit` | 19/20 done | 1 deferred |
| `prd-bian-alignment.md` | `bian-alignment` | 6/15 done, 4 cancelled | 5 deferred |
| `prd-iso-standards-alignment.md` | `iso-standards-alignment` | 4/15 done, 6 cancelled | 5 deferred |

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
| `prd-internal-account-integration-phase2.md` | Phase 2: FA, Payment Order, Position Keeping integration | 2026-01-15 |

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

## PRD Locations: Strategic vs Tactical

Meridian uses two PRD locations with different purposes:

| Location | Purpose | Version Control | Usage |
|----------|---------|-----------------|-------|
| `docs/prd/` | **Strategic PRDs** | Git-tracked | Architectural decisions, feature design, team-reviewed |
| `../../.taskmaster/docs/` | **Tactical PRDs** | Not tracked | Task Master generation, implementation details, working docs |

### When to Use Each Location

**Strategic PRDs** (`docs/prd/`) - Use for:

- Major architectural changes or system-wide features
- Cross-service features requiring coordination
- Decisions requiring team review and consensus
- Long-term reference documentation
- Features with significant design complexity

**Examples:** Universal Asset System, Starlark Saga Orchestration, Internal Account Service

**Tactical PRDs** (`.taskmaster/docs/`) - Use for:

- Task breakdown for specific work packages
- Implementation checklists and tracking
- Gap analysis (e.g., BIAN alignment, ISO standards)
- Working documents for active development
- Feature-specific implementation plans

**Examples:** BIAN Alignment PRD, Multi-Tenancy Phase 2, Technical Debt Remediation

### Migration Path

PRDs often start as tactical documents in `.taskmaster/docs/` for implementation, then graduate
to strategic PRDs in `docs/prd/` once the feature stabilizes and becomes architectural reference
material.

## Categories

### Core Platform

- [Universal Asset System](001-universal-asset-system.md) - Multi-asset support with dimensional safety
- [Internal Account](002-internal-account.md) - BIAN service for clearing, nostro/vostro accounts
- [Market Information Management](004-market-information-management.md) - BIAN service for market data and pricing
- [Starlark Typed Service Clients](007-starlark-typed-service-clients.md) - Type-safe service handlers for saga orchestration
- [Valuation Service](011-valuation-service.md) - BIAN-native multi-asset valuation engine
- [Asset-Agnostic Accounts](028-asset-agnostic-accounts.md) - Generalize account fields for non-fiat asset classes

### Execution Engine

- [Starlark Saga Orchestration (Core)](006-starlark-saga-orchestration-core.md) - Runtime-configurable saga definitions
- [Durable Execution Engine](005-durable-execution-engine.md) - Resilience layer for saga execution
- [Starlark Service Bindings](008-starlark-service-bindings.md) - Service binding layer for Starlark saga scripts
- [Starlark Testing Framework](010-starlark-testing-framework.md) - Auto-validate Starlark scripts at upload time

### Settlement & Reconciliation

- [Reconciliation Service](013-reconciliation-service.md) - BIAN-native settlement lifecycle and variance detection
- [Stripe Connect](015-stripe-connect.md) - Payment-reconciliation-settlement loop with Stripe
- [Reconciliation gRPC Wiring](017-reconciliation-grpc-wiring.md) - Wire reconciliation RPCs to service layer

### Market Data & Pricing

- [Market Data & Dynamic Pricing](016-market-data-dynamic-pricing.md) - Hierarchical reference data and Starlark-based forecasting

### Integration & External Systems

- [Operational Gateway](029-operational-gateway.md) - Asset-agnostic control signal gateway for outbound instructions
- [Structured Mapping Layer](024-structured-inbound-mapping.md) - Bidirectional JSON mapping engine for data transformation
- [Real-Time Event Streaming](025-real-time-event-streaming.md) - WebSocket event delivery for operations console

### Operations & SaaS

- [Control Plane](014-control-plane.md) - SaaS operations layer for manifest management
- [MCP Server](027-mcp-server.md) - Model Context Protocol server bridging LLMs to Meridian Core
- [Codebase Health Audit](012-codebase-health-audit.md) - Remediation for documentation, CI/CD, and code hygiene
- [Production Readiness Review](009-production-readiness-review.md) - Audit and remediation for production gaps

### Service Wiring (Micro-PRDs)

- [Current Account Withdrawal Persistence](018-current-account-withdrawal-persistence.md) - Wire withdrawal-by-ID gRPC handlers
- [Internal Account - PK Client](019-internal-account-position-keeping-client.md) -
  Wire PK gRPC client in Internal Account service
- [Party KYC/AML Provider Integration](020-party-kyc-aml-provider-integration.md) - External KYC/AML provider adapter

### Deployment Targets

- [Meridian Edge](003-meridian-edge.md) - Embedded modular monolith for IoT devices and browser (WASM)

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

1. Create a new markdown file with the next available 3-digit prefix (e.g., `022-feature-name.md`)
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
