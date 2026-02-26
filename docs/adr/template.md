---
name: adr-[NNN]-[brief-slug]  # Example: adr-007-event-sourcing-pattern (use zero-padded number)
description: [One sentence, ~50-80 chars describing the decision] # Example: Use event sourcing for audit trail with
event store as source of truth
triggers:

  - [Specific scenario when to reference this]  # Example: Implementing event-driven workflows
  - [Another trigger scenario]  # Example: Designing systems requiring complete audit history
  - [Additional trigger if needed]  # Example: Building temporal query capabilities

instructions: |
  [2-3 sentences of actionable guidance. Focus on the "what" and "why" of applying
  this decision, not the full rationale (that's in the ADR body below). Be specific
  about key patterns, tools, or approaches to use.]

  Example: Use event sourcing with an append-only event store. Events are immutable
  facts representing state changes. Rebuild current state by replaying events from
  the beginning. Use snapshots for performance optimisation.
---

# [number]. [title]

Date: [YYYY-MM-DD]

## Status

[Proposed | Accepted | Deprecated | Superseded by ADR-XXX]

## Context

[Describe the issue motivating this decision, and any context that influences or constrains the decision.]

## Decision Drivers

* [Driver 1, e.g., performance requirements]
* [Driver 2, e.g., compliance requirements]
* [Driver 3, e.g., team capabilities]

## Considered Options

1. [Option 1]
2. [Option 2]
3. [Option 3]

## Decision Outcome

Chosen option: "[option X]", because [justification. e.g., only option that meets requirements].

### Positive Consequences

* [e.g., improvement of quality attribute X]
* [e.g., follows established patterns]

### Negative Consequences

* [e.g., increased complexity]
* [e.g., requires additional training]

## Pros and Cons of the Options

### [Option 1]

[Description]

* Good, because [argument a]
* Good, because [argument b]
* Bad, because [argument c]

### [Option 2]

[Description]

* Good, because [argument a]
* Bad, because [argument b]

### [Option 3]

[Description]

* Good, because [argument a]
* Bad, because [argument b]

## Links

* [Link to relevant documentation]
* [Link to related ADR]
* [Link to GitHub issue]

## Notes

[Any additional notes, future refactoring triggers, or conditions that would lead to reconsidering this decision]
