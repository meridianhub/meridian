# Documentation Structure Review

**Date:** 2026-02-04
**Reviewer:** Claude Code
**Blueprint Reference:** ~/dev/github.com/agentskills/agentskills

## Executive Summary

Your documentation structure is **significantly more mature and comprehensive** than the agentskills
blueprint. The agentskills repo is a reference implementation showing the format, while meridianhub
has built a production-grade documentation system with excellent organization.

**Key strengths:**

- ✅ Excellent use of README.md indexes with categorization
- ✅ Comprehensive YAML frontmatter for skills (name, description, triggers, instructions)
- ✅ Well-organized directory structure (adr/, prd/, runbooks/, skills/)
- ✅ Clear status tracking for PRDs with Task Master integration
- ✅ Good cross-referencing between related documents

**Improvement opportunities:**

- ⚠️ Missing README.md in `docs/runbooks/` directory
- ⚠️ Some inconsistency between git-tracked PRDs (docs/prd/) and Task Master PRDs (.taskmaster/docs/)
- ⚠️ Could add more visual navigation aids (table of contents) to longer documents

---

## Comparison Matrix

| Aspect | Agentskills Blueprint | Meridianhub Current | Recommendation |
|--------|----------------------|---------------------|----------------|
| **README Indexes** | ✅ Basic structure | ✅ **Comprehensive** with status tables | Keep current approach |
| **YAML Frontmatter** | ✅ Simple example | ✅ **Production-grade** with detailed triggers | Keep current approach |
| **Directory Organization** | ⚠️ Flat structure | ✅ **Multi-level** (adr/, prd/, skills/, runbooks/) | Keep current approach |
| **Cross-References** | ❌ Minimal | ✅ **Extensive** linking between docs | Keep current approach |
| **Table of Contents** | ⚠️ In some docs | ⚠️ **Inconsistent** (some PRDs have, some don't) | Add ToC to all long docs |
| **Visual Navigation** | ❌ None | ⚠️ **Limited** (tables used well, could add diagrams) | Consider Mermaid diagrams |
| **Status Tracking** | ❌ Not applicable | ✅ **Excellent** PRD status tables | Keep current approach |

---

## Detailed Findings

### 1. Skills Documentation (`docs/skills/`)

**Strengths:**

- Excellent README.md with clear categorization
- Comprehensive list of available skills with links
- Well-documented metadata format with examples
- Good troubleshooting section
- Cross-references to related documentation

**Structure:**

```text
docs/skills/
├── README.md          ✅ Comprehensive index
├── tilt.md           ✅ YAML frontmatter
├── docker.md         ✅ YAML frontmatter
├── testing.md        ✅ YAML frontmatter
└── [7 other skills]  ✅ All properly formatted
```text

**Comparison to agentskills:**

- Agentskills: Simple README with basic structure
- Meridianhub: **Production-grade** with categorization, troubleshooting, and examples

**Recommendation:** ✅ **No changes needed** - this is exemplary

---

### 2. Architecture Decision Records (`docs/adr/`)

**Strengths:**

- Outstanding README.md with:
  - Comprehensive index table with status and dates
  - Category-based organization
  - Links to all ADRs
  - Section on "Key Architectural Changes"
  - Future ADRs to consider
  - References to external resources

**Structure:**

```text
docs/adr/
├── README.md                    ✅ Comprehensive index with status table
├── template.md                  ✅ MADR template for new ADRs
├── 0001-record-architecture.md  ✅ Numbered ADRs
├── 0002-microservices.md        ✅ Clear naming
└── [20+ other ADRs]             ✅ Well organized
```text

**Example index entry:**

```markdown
| ADR | Title | Status | Date |
|-----|-------|--------|------|
| [ADR-0001](0001-record-architecture-decisions.md) | Record Architecture Decisions | Accepted | 2025-10-24 |
```text

**Recommendation:** ✅ **No changes needed** - this is best-in-class

---

### 3. Product Requirements Documents (`docs/prd/`)

**Strengths:**

- Excellent README.md with:
  - Status overview with clear definitions
  - Separate tables for git-tracked vs Task Master PRDs
  - Task completion tracking (e.g., "24/24 done")
  - Category-based organization
  - Clear guidance on creating new PRDs

**Structure:**

```text
docs/prd/
├── README.md                            ✅ Comprehensive with status tables
├── universal-asset-system.md            ✅ YAML frontmatter + detailed ToC
├── starlark-saga-orchestration-core.md  ✅ Well-structured
├── internal-bank-account.md             ✅ Detailed implementation guidance
└── [8 other PRDs]                       ✅ Consistent format
```text

**Example PRD header:**

```yaml
---
name: prd-universal-asset-system
description: Extend Meridian's ledger from fiat-only to multi-asset support
triggers:
  - Implementing multi-asset or universal asset support
  - Working on InstrumentType, Quantity, or asset definitions
instructions: |
  Key patterns: Use Go generics for dimensional safety.
  Assets are configured via database, not code.
---

# PRD: Universal Asset System

**Status:** Implemented
**Task Master Tag:** `universal-asset-system` (36/36 tasks done)
**ADRs:**
- [0013 - Universal Quantity Type System](../adr/0013-generic-asset-quantity-types.md)

## Table of Contents
- [Zero-State Contract](#zero-state-contract)
- [Work Streams](#work-streams)
...
```text

**Recommendations:**

1. ⚠️ Ensure all PRDs have comprehensive ToC for long documents
2. ⚠️ Consider consolidating git-tracked and Task Master PRDs into single location
3. ✅ Current frontmatter and status tracking is excellent

---

### 4. Runbooks (`docs/runbooks/`)

**Structure:**

```text
docs/runbooks/
├── (MISSING) README.md                          ❌ No index
├── disaster-recovery.md                         ✅ Good content
├── incident-response.md                         ✅ Good content
├── saga-failure-recovery.md                     ✅ Good content
├── database-per-service-migration.md            ✅ Good content
├── internal-bank-account-operations.md          ✅ Good content
├── market-information-operations.md             ✅ Good content
└── troubleshooting-saga-handlers.md             ✅ Good content
```text

**Problem:** No README.md index for runbooks directory

**Recommendation:** ⚠️ **Create runbooks README.md** (see template below)

---

### 5. Task Master Documentation (`.taskmaster/docs/`)

**Strengths:**

- Comprehensive PRDs with detailed implementation guidance
- Good use of tables for gap analysis
- Clear priority markings

**Structure:**

```text
.taskmaster/docs/
├── prd-bian-alignment.md           ✅ Detailed gap analysis
├── prd-multi-tenancy.md            ✅ Comprehensive
├── prd-position-keeping.md         ✅ Well-structured
└── [25+ other PRDs]                ✅ Consistent format
```text

**Observations:**

- These PRDs are more implementation-focused (Task Master generation)
- Git-tracked PRDs in `docs/prd/` are more architectural/strategic

**Recommendation:**

- ⚠️ **Consider documenting the distinction** between the two PRD locations in both READMEs
- Current approach works, but clarify:
  - `docs/prd/` = Strategic PRDs (committed to git, reviewed, architectural)
  - `.taskmaster/docs/` = Tactical PRDs (Task Master generation, implementation-focused)

---

## Comparison with Agentskills Blueprint

### What Agentskills Provides

The agentskills repo is a **format reference**, not a full system:

**Structure:**

```text
agentskills/
├── README.md                 (Project overview)
├── docs/                     (Mintlify documentation site)
│   ├── README.md            (Basic structure)
│   ├── CLAUDE.md            (Mintlify dev instructions)
│   └── docs.json            (Navigation config)
└── skills-ref/              (Python library for validation)
    ├── README.md            (Installation instructions)
    └── CLAUDE.md            (Library usage)
```text

**Key differences:**

| Aspect | Agentskills | Meridianhub |
|--------|------------|-------------|
| **Purpose** | Format reference | Production system |
| **Documentation depth** | Basic examples | Comprehensive guides |
| **Organization** | Flat structure | Multi-level hierarchy |
| **Status tracking** | N/A | Detailed status tables |
| **Cross-referencing** | Minimal | Extensive |
| **Real-world usage** | Demonstration | Active development |

---

## Recommended Actions

### Priority 1: Add Missing README (Runbooks)

Create `docs/runbooks/README.md`:

```markdown
# Runbooks

This directory contains operational runbooks for incident response, recovery procedures, and troubleshooting guides.

## Available Runbooks

### Emergency Response

- **[incident-response.md](incident-response.md)** - General incident response procedures
- **[disaster-recovery.md](disaster-recovery.md)** - Disaster recovery and backup restoration
- **[saga-failure-recovery.md](saga-failure-recovery.md)** - Recovering from failed saga executions

### Operations & Troubleshooting

- **[database-per-service-migration.md](database-per-service-migration.md)** - Migrating to database-per-service architecture
- **[internal-bank-account-operations.md](internal-bank-account-operations.md)** - Internal bank account operational procedures
- **[market-information-operations.md](market-information-operations.md)** - Market data and pricing operations
- **[troubleshooting-saga-handlers.md](troubleshooting-saga-handlers.md)** - Debugging Starlark saga scripts

## Runbook Structure

Each runbook typically includes:

- **Overview**: What situation this runbook addresses
- **Prerequisites**: Required access, tools, or knowledge
- **Procedures**: Step-by-step instructions with examples
- **Verification**: How to confirm successful resolution
- **Escalation**: When and how to escalate

## Related Documentation

- [ADRs](../adr/README.md) - Architectural decisions that inform these procedures
- [Skills](../skills/README.md) - Development and operational skills
- [PRDs](../prd/README.md) - Feature specifications and designs
```text

### Priority 2: Improve Table of Contents Consistency

**Current state:** Some PRDs have comprehensive ToCs, others don't

**Recommendation:** Add ToC to all PRDs longer than 200 lines

**Format** (already used in some docs):

```markdown
## Table of Contents

- [Overview](#overview)
- [Requirements](#requirements)
- [Design](#design)
- [Work Streams](#work-streams)
  - [Stream A: Core Types](#stream-a-core-types-package)
  - [Stream B: Currency Definitions](#stream-b-currency-definitions)
- [Success Metrics](#success-metrics)
```text

### Priority 3: Document PRD Location Strategy

Add to both `docs/prd/README.md` and `.taskmaster/README.md`:

```markdown
## PRD Locations

Meridian uses two PRD locations with different purposes:

| Location | Purpose | Version Control | Usage |
|----------|---------|-----------------|-------|
| `docs/prd/` | Strategic PRDs | Git-tracked | Architectural decisions, feature design, reviewed by team |
| `.taskmaster/docs/` | Tactical PRDs | Not tracked | Task Master generation, implementation details, working docs |

**When to use each:**

- **Strategic PRDs** (`docs/prd/`):
  - Major architectural changes
  - Cross-service features
  - Decisions requiring team review
  - Long-term reference documentation

- **Tactical PRDs** (`.taskmaster/docs/`):
  - Task breakdown for specific work
  - Implementation checklists
  - Gap analysis (e.g., BIAN alignment)
  - Working documents for active development
```text

### Priority 4: Add Visual Navigation (Optional Enhancement)

Consider adding Mermaid diagrams for complex relationships:

**Example for ADR relationships:**

```mermaid
graph LR
    ADR-0002[Microservices per BIAN] --> ADR-0003[Database Migrations]
    ADR-0002 --> ADR-0015[Service Directory Structure]
    ADR-0004[Event Schema Evolution] --> ADR-0005[Adapter Pattern]
    ADR-0003 --> ADR-0015
```text

**Example for PRD status flow:**

```mermaid
stateDiagram-v2
    [*] --> Not_Started
    Not_Started --> In_Progress: Parse PRD
    In_Progress --> Paused: Some tasks deferred
    In_Progress --> Implemented: All tasks complete
    Paused --> In_Progress: Resume work
    Implemented --> [*]
```text

---

## Conclusion

Your documentation structure is **significantly ahead** of the agentskills blueprint:

**What you're doing better than the blueprint:**

1. ✅ Comprehensive README indexes with status tracking
2. ✅ Detailed YAML frontmatter with rich triggers
3. ✅ Multi-level directory organization
4. ✅ Extensive cross-referencing
5. ✅ Real-world status tracking and Task Master integration

**Small improvements to consider:**

1. ⚠️ Add README to runbooks/ (Priority 1)
2. ⚠️ Standardize ToC in long documents (Priority 2)
3. ⚠️ Document PRD location strategy (Priority 3)
4. ⚠️ Consider Mermaid diagrams for visual navigation (Priority 4 - optional)

**Overall assessment:** Your documentation structure is production-ready and serves as an excellent
model for AI-assisted development. The agentskills blueprint validates your approach - you've
independently arrived at and exceeded their recommendations.
