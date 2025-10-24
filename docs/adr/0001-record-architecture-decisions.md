# 1. Record architecture decisions

Date: 2025-10-24

## Status

Accepted

## Context

We need to record the architectural decisions made on this project. Meridian is a production-grade open banking ledger implementing BIAN standards, and documenting our architectural choices is critical for:

- Maintaining consistency across the codebase
- Onboarding new contributors
- Providing context for AI-assisted development tools
- Ensuring compliance with financial industry standards

## Decision

We will use Architecture Decision Records, as [described by Michael Nygard](http://thinkrelevance.com/blog/2011/11/15/documenting-architecture-decisions).

## Consequences

See Michael Nygard's article, linked above. For a lightweight ADR toolset, see Nat Pryce's [adr-tools](https://github.com/npryce/adr-tools).

### Positive Consequences

* Clear documentation of why decisions were made
* Reduces context switching when revisiting past choices
* AI assistants (like Claude Code) can reference ADRs for better context
* New team members understand the rationale behind the architecture

### Negative Consequences

* Requires discipline to keep ADRs up to date
* Additional overhead when making architectural changes
* Risk of ADRs becoming stale if not maintained
