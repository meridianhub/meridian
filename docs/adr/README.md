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
| [ADR-0002](0002-microservices-per-bian-domain.md) | Microservices Architecture with One Service per BIAN Domain | Accepted | 2025-10-25 |
| [ADR-0003](0003-database-schema-migrations.md) | Database Schema Migrations with Atlas | Accepted | 2025-10-25 (Revised) |
| [ADR-0004](0004-event-schema-evolution.md) | Event Schema Evolution with Protobuf and Buf | Accepted | 2025-10-25 |
| [ADR-0005](0005-adapter-pattern-layer-translation.md) | Adapter Pattern for Layer Translation | Accepted | 2025-10-25 |
| [ADR-0006](0006-tilt-local-development.md) | Tilt for Local Kubernetes Development | Accepted | 2025-10-25 |
| [ADR-0007](0007-raw-yaml-over-helm-for-initial-development.md) | Raw YAML over Helm for Initial Development | Accepted | 2025-10-25 |
| [ADR-0008](0008-defensive-testing-standards.md) | Defensive Testing Standards | Accepted | 2025-10-25 |
| [ADR-0009](0009-application-level-audit-logging.md) | Application-Level Audit Logging | Proposed | 2025-11-04 |
| [ADR-0010](0010-grpc-client-side-load-balancing.md) | gRPC Client-Side Load Balancing with Headless Services | Accepted | 2025-11-14 |

## Categories

### Project Structure
- [ADR-0001](0001-record-architecture-decisions.md) - Record Architecture Decisions
- [ADR-0002](0002-microservices-per-bian-domain.md) - Microservices Architecture

### Data Management & Architecture Patterns
- [ADR-0003](0003-database-schema-migrations.md) - Database Schema Migrations with Atlas
- [ADR-0004](0004-event-schema-evolution.md) - Event Schema Evolution with Protobuf and Buf
- [ADR-0005](0005-adapter-pattern-layer-translation.md) - Adapter Pattern for Layer Translation
- [ADR-0009](0009-application-level-audit-logging.md) - Application-Level Audit Logging

### Development Environment & Infrastructure
- [ADR-0006](0006-tilt-local-development.md) - Tilt for Local Kubernetes Development
- [ADR-0007](0007-raw-yaml-over-helm-for-initial-development.md) - Raw YAML over Helm for Initial Development
- [ADR-0010](0010-grpc-client-side-load-balancing.md) - gRPC Client-Side Load Balancing with Headless Services

### Quality & Testing
- [ADR-0008](0008-defensive-testing-standards.md) - Defensive Testing Standards

## Key Architectural Changes

**2025-10-25 Revision:** Moved from unified schema management to separated concerns:
- **Previous approach:** Go structs with tags as single source of truth for database, events, and APIs
- **New approach:** Separate domain models, persistence entities, and event schemas with explicit adapters
- **Rationale:** Real-world experience showed unified approach was too rigid. Separated concerns allow:
  - Database audit fields without polluting domain
  - Event metadata without cluttering business logic
  - Independent versioning of database, events, and APIs
  - Follows industry best practices (Google, LinkedIn, Netflix, AWS)

See [ADR-0004](0004-separated-schema-management.md) and [ADR-0005](0005-adapter-pattern-layer-translation.md) for details.

## Future ADRs to Consider

Based on the Meridian project requirements, these ADRs may be created as implementation progresses:

- **Database Choice: CockroachDB vs YugabyteDB** - Distributed SQL database selection
- **Multi-Currency Decimal Precision** - How we handle money types across currencies
- **Idempotency Implementation** - Redis-based idempotency strategy
- **Test Strategy for Financial Systems** - TDD approach for zero-tolerance systems
- **Service Mesh vs API Gateway** - Cross-cutting concerns for microservices
- **Event Versioning Strategy** - How to handle breaking changes in Kafka events

## References

- [Documenting Architecture Decisions](http://thinkrelevance.com/blog/2011/11/15/documenting-architecture-decisions) - Michael Nygard
- [MADR](https://adr.github.io/madr/) - Markdown Architectural Decision Records
- [adr-tools](https://github.com/npryce/adr-tools) - Command-line tools for working with ADRs
- [BIAN Standards](https://bian.org/) - Banking Industry Architecture Network
