---
name: skill-schema-evolution
description: Protobuf schema evolution workflow with buf breaking change detection and BIAN patterns
triggers:

  - Evolving protobuf schemas
  - Adding fields to proto definitions
  - Creating new event types
  - BIAN specification updates
  - Schema compatibility issues
  - Pre-commit hook failures for proto
  - buf breaking change errors
  - Deciding between backward-compatible changes vs new event types

instructions: |
  Follow ADR-0004 for protobuf native versioning. Use Pattern 1 (add optional fields) for
  backward-compatible changes. Use Pattern 2 (new event type) for new BIAN behavior qualifiers.
  Run 'make proto-breaking' before commit. Pre-commit hooks enforce validation automatically.
  New BIAN behaviors = new event types, not schema modifications.
---

# Schema Evolution Developer Guide

This guide explains how to safely evolve protobuf schemas in Meridian following ADR-0004.

## Quick Reference

```bash

# Local validation (before commit)

make proto-lint              # Check style
make proto-breaking          # Check compatibility against develop
make proto                   # Regenerate Go code

# With git hooks (recommended)

.githooks/install.sh         # One-time setup
git commit                   # Hooks run automatically
```

## Table of Contents

1. [When to Evolve Schemas](#when-to-evolve-schemas)
2. [Decision Tree](#decision-tree)
3. [Pattern 1: Backward-Compatible Changes](#pattern-1-backward-compatible-changes)
4. [Pattern 2: New Event Types](#pattern-2-new-event-types)
5. [Local Development Workflow](#local-development-workflow)
6. [CI/CD Integration](#cicd-integration)
7. [Common Scenarios](#common-scenarios)
8. [Troubleshooting](#troubleshooting)

## When to Evolve Schemas

Protobuf schemas need evolution when:

- **BIAN specification updates** (13.0 → 14.0 adds new fields or operations)
- **New domain requirements** (additional metadata for observability)
- **New event types** (new BIAN behavior qualifiers)
- **Field additions** (enriching existing events with optional data)

## Decision Tree

```text
Need to change a proto schema?
│
├─ Is this a NEW operation/behavior?
│  └─ YES → Create new event type (Pattern 2)
│
├─ Is this ADDITIONAL optional data?
│  └─ YES → Add optional fields (Pattern 1)
│
├─ Does this REMOVE or CHANGE existing fields?
│  └─ YES → STOP! This is breaking. Consider:
│      ├─ Can you add a new field instead?
│      ├─ Can you deprecate the old field?
│      └─ Do you need a new event type?
│
└─ Unsure? → Ask in #engineering channel
```

## Pattern 1: Backward-Compatible Changes

Use when adding **optional fields** to existing messages.

### Example: Adding Correlation Tracking

**Before:**

```protobuf
// api/proto/events/current_account/v1/events.proto

message AccountUpdated {
  string event_id = 1;
  google.protobuf.Timestamp occurred_at = 2;
  string account_id = 3;
  string account_status = 4;
}
```

**After:**

```protobuf
message AccountUpdated {
  string event_id = 1;
  google.protobuf.Timestamp occurred_at = 2;
  string account_id = 3;
  string account_status = 4;

  // New optional fields (added 2025-10-31)
  string correlation_id = 5;  // Trace request across services
  string causation_id = 6;    // Event that caused this event
}
```

### Validation Steps

```bash

# 1. Make your changes to .proto files

vim api/proto/events/current_account/v1/events.proto

# 2. Validate style

make proto-lint

# 3. Check for breaking changes

make proto-breaking

# ✅ Output: "No breaking changes detected"

# 4. Regenerate Go code

make proto

# 5. Commit

git add api/proto/events/current_account/v1/events.proto
git commit -m "feat: Add correlation_id and causation_id to AccountUpdated event"

# Pre-commit hooks run buf-lint and buf-breaking automatically

```

### What Happens

- **Old consumers**: Ignore new fields (protobuf default behavior)
- **New consumers**: Can read new fields (will be empty in old events)
- **CI/CD**: `buf breaking` passes ✅
- **No coordination needed**: Deploy producers and consumers independently

## Pattern 2: New Event Types

Use when adding **new BIAN operations** or **semantically distinct events**.

### Example: BIAN 14.0 Adds "Suspend" Operation

**New file or add to existing:**

```protobuf
// api/proto/events/current_account/v1/events.proto

// Existing event (unchanged)
message AccountUpdated {
  string event_id = 1;
  google.protobuf.Timestamp occurred_at = 2;
  string account_id = 3;
  string account_status = 4;
}

// New event for new BIAN behavior qualifier
message AccountSuspended {
  // Event metadata
  string event_id = 1;
  google.protobuf.Timestamp occurred_at = 2;
  string correlation_id = 3;
  string causation_id = 4;

  // Business data
  string account_id = 5;
  string suspension_reason = 6;
  google.protobuf.Timestamp suspended_until = 7;
  string suspended_by = 8;
}
```

### Implementation Steps

```bash

# 1. Add new message to proto file

vim api/proto/events/current_account/v1/events.proto

# 2. Validate (new messages can't break existing schemas)

make proto-lint
make proto-breaking  # ✅ Passes

# 3. Generate Go code

make proto

# 4. Create Kafka topic

kubectl exec -it kafka-0 -- kafka-topics --create \
  --topic account-suspended \
  --partitions 3 \
  --replication-factor 2 \
  --config retention.ms=604800000  # 7 days

# 5. Implement producer

vim internal/adapters/events/current_account_publisher.go

# 6. Commit

git add api/proto/events/current_account/v1/events.proto
git add internal/adapters/events/current_account_publisher.go
git commit -m "feat: Add AccountSuspended event for BIAN 14.0 Suspend operation"
```

### Topic Strategy

- **One topic per event type**: `account-suspended` (not `account-updated-v2`)
- **Semantic names**: Reflect BIAN behavior qualifiers
- **No version suffixes**: Use new event types instead of versioning topics
- **7-day retention**: Events are coordination, not source of truth

## Local Development Workflow

### Initial Setup

```bash

# Install git hooks (one-time)

.githooks/install.sh

# Verify installation

ls -la .git/hooks/pre-commit

# buf will be installed automatically by the hook if needed

```

### Daily Workflow

```bash

# 1. Create feature branch

git checkout -b feature/add-account-suspension

# 2. Make schema changes

vim api/proto/events/current_account/v1/events.proto

# 3. Validate locally

make proto-lint        # Check style
make proto-breaking    # Check compatibility
make proto             # Regenerate Go code

# 4. Run tests

make test

# 5. Commit (git hooks run automatically)

git add .
git commit -m "feat: Add AccountSuspended event"

# Git hook will:

# - Run buf lint on proto files

# - Run buf breaking against develop

# - Run gofumpt on Go files

# - Run golangci-lint on Go files

```

### Bypassing Git Hooks (Emergency Only)

```bash

# Skip hooks for emergency hotfix (NOT RECOMMENDED)

git commit --no-verify -m "hotfix: Critical production fix"

# You MUST run validation after:

make proto-lint
make proto-breaking
```

## CI/CD Integration

### GitHub Actions Workflow

The `.github/workflows/proto.yml` workflow runs on every PR and push to `develop`/`main`:

```yaml
jobs:
  proto-lint:

    - Run buf lint

  proto-breaking:

    - Run buf breaking --against develop
    - Comment on PR if breaking changes detected

  proto-validate:

    - Validate directory structure
    - Check for v1 proto files

```

### What Triggers CI Failures

**buf-lint failures:**

- Missing package declaration
- Improper field naming (must be snake_case)
- Missing comments on public messages
- Incorrect import paths

**buf-breaking failures:**

- Removed fields
- Changed field types
- Changed field numbers
- Renamed fields (wire-incompatible)

**proto-validate failures:**

- Missing `api/proto/meridian` directory
- No v1 proto files found

### Fixing CI Failures

```bash

# 1. Pull latest develop

git fetch origin develop:develop

# 2. Run validation locally

make proto-lint
make proto-breaking

# 3. Fix issues in proto files

# 4. Push updated branch

git add api/proto/
git commit -m "fix: Resolve buf lint errors"
git push
```

## Common Scenarios

### Scenario 1: Adding Observability Metadata

**Goal**: Add `trace_id` to all events for distributed tracing.

**Solution**: Pattern 1 (Backward-compatible)

```protobuf
message AccountUpdated {
  // Existing fields
  string event_id = 1;
  google.protobuf.Timestamp occurred_at = 2;
  string account_id = 3;
  string account_status = 4;

  // New observability field
  string trace_id = 5;  // OpenTelemetry trace ID
}
```

**Impact**: None. Old consumers ignore `trace_id`. New consumers read it.

### Scenario 2: BIAN Adds New Field to Specification

**Goal**: BIAN 14.0 adds `account_classification` field to Current Account.

**Solution**: Pattern 1 (Backward-compatible)

```protobuf
message AccountUpdated {
  string event_id = 1;
  google.protobuf.Timestamp occurred_at = 2;
  string account_id = 3;
  string account_status = 4;

  // BIAN 14.0 addition
  string account_classification = 5;  // personal, business, etc.
}
```

**Impact**: None. Optional field, backward compatible.

### Scenario 3: BIAN Adds New Behavior Qualifier

**Goal**: BIAN 14.0 adds "Freeze" operation distinct from "Suspend".

**Solution**: Pattern 2 (New event type)

```protobuf
message AccountFrozen {
  string event_id = 1;
  google.protobuf.Timestamp occurred_at = 2;
  string correlation_id = 3;
  string causation_id = 4;

  string account_id = 5;
  string freeze_reason = 6;        // regulatory, fraud, etc.
  string frozen_by = 7;
  bool requires_investigation = 8;
}
```

**Implementation**:

1. Add message to proto file
2. Create new Kafka topic: `account-frozen`
3. Implement producer in `internal/adapters/events/`
4. Consumers subscribe when ready

### Scenario 4: Need to Remove a Field (BREAKING)

**Goal**: Remove deprecated field that's no longer used.

**Problem**: This is a breaking change!

**Solutions (in order of preference)**:

1. **Mark as deprecated** (best for now):

```protobuf
message AccountUpdated {
  string event_id = 1;
  google.protobuf.Timestamp occurred_at = 2;
  string account_id = 3;
  string account_status = 4;

  string old_field = 5 [deprecated = true];  // Don't use, will remove in v2
}
```

1. **Stop populating** (intermediate step):

```go
// Stop setting old_field, but keep it in schema
event := &eventspb.AccountUpdated{
    EventId:    uuid.New().String(),
    // ... other fields
    // OldField: "",  // Intentionally not set
}
```

1. **Create v2 event** (if truly breaking):

```protobuf
// api/proto/events/current_account/v2/events.proto
message AccountUpdated {
  // New schema without old_field
}
```

### Scenario 5: Changing Field Type (BREAKING)

**Goal**: Change `account_balance` from `string` to `int64`.

**Problem**: This is wire-incompatible!

**Solution**: Add new field, deprecate old:

```protobuf
message AccountUpdated {
  string event_id = 1;
  google.protobuf.Timestamp occurred_at = 2;
  string account_id = 3;

  string account_balance = 4 [deprecated = true];  // Use account_balance_cents instead
  int64 account_balance_cents = 5;                 // New field with proper type
}
```

**Migration**:

1. Deploy with both fields populated
2. Update consumers to read `account_balance_cents`
3. After all consumers updated, stop populating `account_balance`
4. In future v2, remove deprecated field

## Troubleshooting

### buf-breaking fails locally but passes in CI

**Cause**: Local develop branch is stale.

**Fix**:

```bash
git fetch origin develop:develop
make proto-breaking
```

### buf-lint reports style errors

**Common issues**:

```protobuf
// ❌ Wrong: PascalCase field names
message AccountUpdated {
  string EventId = 1;        // Wrong!
  string AccountId = 2;      // Wrong!
}

// ✅ Correct: snake_case field names
message AccountUpdated {
  string event_id = 1;       // Correct
  string account_id = 2;     // Correct
}
```

```protobuf
// ❌ Wrong: Missing package declaration
message AccountUpdated {
  string event_id = 1;
}

// ✅ Correct: Proper package
syntax = "proto3";

package meridian.events.current_account.v1;

option go_package = "github.com/meridianhub/meridian/api/proto/events/current_account/v1;current_accountv1";

message AccountUpdated {
  string event_id = 1;
}
```

### Git hooks fail

**Skip hook temporarily for debugging**:

```bash
git commit --no-verify -m "test commit"

# Remember to run validation manually afterward!

```

**Reinstall hooks**:

```bash
.githooks/install.sh
```

### Generated Go code has compilation errors

**Cause**: Stale generated code.

**Fix**:

```bash

# Clean and regenerate

rm -rf api/proto/*/v*/*.pb.go
make proto

# Verify

go build ./...
```

### CI workflow doesn't detect my changes

**Check trigger conditions**:

```yaml

# proto.yml only runs on these branches

on:
  push:
    branches: [develop, main]
  pull_request:
    branches: [develop, main]
```

**Solution**: Create PR targeting `develop` or `main`.

## Best Practices

### ✅ DO

- **Add optional fields** for backward-compatible changes
- **Create new event types** for new BIAN behaviors
- **Run `make proto-breaking`** before every commit
- **Use semantic event names** aligned with BIAN
- **Document schema changes** in commit messages
- **Test with old and new consumers** during evolution
- **Use git hooks** to catch issues early (`.githooks/install.sh`)

### ❌ DON'T

- **Remove existing fields** (use deprecation instead)
- **Change field types** (add new field with new type)
- **Reuse field numbers** from deleted fields
- **Skip `buf breaking` checks** (unless emergency hotfix)
- **Version topic names** (use new event types instead)
- **Deploy breaking changes** without coordination

## Additional Resources

### Related Skills

- [Tilt Development](./tilt.md) - Local Kubernetes development with fast iteration
- [Docker Configuration](./docker.md) - Container builds and multi-stage Dockerfiles
- [Security Scanning](./security.md) - Vulnerability detection and compliance

### Documentation

- [ADR-0004: Event Schema Evolution](../adr/0004-event-schema-evolution.md)
- [Protocol Buffers Language Guide](https://protobuf.dev/programming-guides/proto3/)
- [buf CLI Documentation](https://buf.build/docs/)
- [BIAN Service Landscape](https://bian.org/servicelandscape-14-0-0/)
- [Git Hooks Documentation](../../.githooks/README.md)

## Getting Help

- **Schema questions**: Ask in `#engineering` Slack channel
- **buf errors**: Check [buf documentation](https://buf.build/docs/)
- **BIAN alignment**: Consult [BIAN Semantic APIs](https://bian.org/semantic-apis/)
- **Emergency**: Contact platform team lead

## Changelog

- **2025-10-31**: Initial skill created as part of buf breaking CI/CD integration (subtask 4.6)
- **Reference**: Implements ADR-0004 schema evolution patterns with automated validation
