# Architecture Decision Records

This directory contains Architecture Decision Records (ADRs) for Meridian.

## What are ADRs?

Architecture Decision Records capture important architectural decisions made in the project, along with their context and consequences. They help:

- Document the reasoning behind architectural choices
- Prevent relitigating already-decided trade-offs
- Onboard new team members with historical context
- Guide AI assistants and tools with appropriate context

## Creating New ADRs

We use the [adr-tools](https://github.com/npryce/adr-tools) CLI for managing ADRs:

```bash
# Install adr-tools (if not already installed)
brew install adr-tools

# Create a new ADR
adr new "Title of Decision"

# Link ADRs (when one supersedes another)
adr link <source> "Supersedes" <target>
```

For more complex ADRs, use the [template.md](template.md) file as a starting point, which follows the MADR (Markdown Architectural Decision Records) format.

## ADR Index

| ADR | Title | Status | Date |
|-----|-------|--------|------|
| [ADR-0001](0001-record-architecture-decisions.md) | Record Architecture Decisions | Accepted | 2025-10-24 |

## Categories

### Project Structure
- [ADR-0001](0001-record-architecture-decisions.md) - Record Architecture Decisions

## Future ADRs to Consider

Based on the Meridian project requirements, these ADRs should be created:

- **BIAN API Implementation Strategy** - How we map BIAN specifications to gRPC/protobuf
- **Database Choice: CockroachDB vs YugabyteDB** - Distributed SQL database selection
- **Tilt for Local Development** - Why Tilt over docker-compose
- **Event-Driven Architecture with Kafka** - Kafka vs NATS vs other message brokers
- **Multi-Currency Decimal Precision** - How we handle money types across currencies
- **Idempotency Implementation** - Redis-based idempotency strategy
- **Test Strategy for Financial Systems** - TDD approach for zero-tolerance systems
- **Worktree-Based Development Workflow** - Git worktree pattern for parallel development

## References

- [Documenting Architecture Decisions](http://thinkrelevance.com/blog/2011/11/15/documenting-architecture-decisions) - Michael Nygard
- [MADR](https://adr.github.io/madr/) - Markdown Architectural Decision Records
- [adr-tools](https://github.com/npryce/adr-tools) - Command-line tools for working with ADRs
- [BIAN Standards](https://bian.org/) - Banking Industry Architecture Network
