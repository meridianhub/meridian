---
name: adr-004-event-schema-evolution
description: Use buf breaking change detection for protobuf event schema evolution without external schema registry
triggers:

  - Evolving Kafka event schemas
  - Managing BIAN specification updates
  - Ensuring backward compatibility
  - Coordinating between services via events

instructions: |
  Use protobuf for all Kafka events. Run `buf breaking` in CI to prevent breaking changes.
  Follow BIAN versioning (13.0 → 14.0). No external schema registry needed - buf provides
  compile-time validation. Events are append-only with version numbers.
---

# 4. Event Schema Evolution Strategy

Date: 2025-10-26 (Revised from original Schema Registry decision)

## Status

Accepted

Supersedes initial decision to use Confluent Schema Registry.

## Context

Meridian uses Apache Kafka for internal coordination between BIAN service domains. Events represent domain state
changes (e.g., `CurrentAccountUpdated`, `FinancialBookingLogCreated`) and must evolve as BIAN specifications update
across releases (13.0 → 14.0 → 15.0).

### Architectural Facts

- **Kafka topics are internal** - Used for coordination between Meridian services, not exposed to external consumers
- **External integration via gRPC** - External systems interact through gRPC APIs, not by consuming Kafka topics
- **Database is source of truth** - CockroachDB/YugabyteDB provides persistent state; Kafka is ephemeral coordination
- **Monorepo structure** - All services share `api/proto/` directory with compile-time validation
- **High throughput goal** - Minimize latency and operational overhead
- **Learning-focused** - Reference implementation should demonstrate patterns without unnecessary complexity

### The Schema Registry Question

We initially considered Confluent Schema Registry to manage event schema evolution. However, Schema Registry's primary
value propositions don't align with our architecture:

**Schema Registry provides:**

- Centralized schema storage and runtime discovery
- Governance for external consumers
- Compatibility enforcement at registration time
- Historical schema versions for replay

**Our reality:**

- All consumers are internal (same git repo)
- Compile-time validation via `buf breaking` already enforces compatibility
- Database is source of truth (Kafka replay not needed for recovery)
- No external consumers requiring runtime schema discovery

**Conclusion:** Schema Registry adds operational complexity without meaningful benefit for internal-only Kafka usage.

## Decision

Use **protobuf's native versioning** with **BIAN-aligned semantic event types** for event evolution. No Schema Registry.

### Event Evolution Patterns

### Pattern 1: Backward-Compatible Changes (Same Event Type)

For minor additions that don't change semantic meaning:

```protobuf
// api/proto/events/current_account/v1/events.proto

// Before
message AccountUpdated {
  string event_id = 1;
  google.protobuf.Timestamp occurred_at = 2;
  string account_id = 3;
  string account_status = 4;
}

// After - add optional metadata
message AccountUpdated {
  string event_id = 1;
  google.protobuf.Timestamp occurred_at = 2;
  string account_id = 3;
  string account_status = 4;
  string correlation_id = 5;  // New optional field
  string updated_by = 6;      // New optional field
}
```

**Validation:** `buf breaking --against main` ensures no breaking changes.

**Consumer behavior:** Old consumers automatically ignore new fields (protobuf feature).

### Pattern 2: New BIAN Behavior Qualifier (New Event Type)

For new BIAN operations with distinct semantics:

```protobuf
// api/proto/events/current_account/v1/events.proto

// Existing event
message AccountUpdated {
  string event_id = 1;
  google.protobuf.Timestamp occurred_at = 2;
  string account_id = 3;
  string account_status = 4;
}

// New BIAN 14.0 behavior qualifier = new event type
message AccountSuspended {
  string event_id = 1;
  google.protobuf.Timestamp occurred_at = 2;
  string account_id = 3;
  string suspension_reason = 4;
  google.protobuf.Timestamp suspended_until = 5;
  string suspended_by = 6;
}
```

**Rationale:** BIAN behavior qualifiers (Initiate, Update, Suspend, Terminate) represent distinct operations. Map these
to distinct event types rather than overloading a single event schema.

**Topic strategy:** New event type = new Kafka topic (`account-suspended`)

### Topic Naming Strategy

- **One topic per event type:** `account-updated`, `account-suspended`, `booking-created`
- **No version suffixes:** Use semantic names, not `account-updated-v2`
- **BIAN alignment:** Topic names reflect BIAN behavior qualifiers
- **Retention policy:** 7 days (events are coordination, not system of record)

### Compatibility Validation

**CI/CD enforcement via buf:**

```yaml

# .github/workflows/proto-validation.yml

- name: Lint protobuf schemas

  run: buf lint

- name: Check for breaking changes

  run: buf breaking --against '.git#branch=main'
```

**Breaking changes fail the build**, forcing developers to either:

1. Create a new event type (if semantically different)
2. Make the change backward-compatible (add optional fields)

**No runtime compatibility checks needed** - compile-time validation is stronger and catches issues earlier.

## Decision Drivers

- ✅ **Simplicity** - No Schema Registry to operate; protobuf native capabilities are sufficient
- ✅ **Performance** - No external schema lookup; no network dependency for serialization
- ✅ **BIAN semantic alignment** - New behavior qualifiers naturally map to new event types
- ✅ **Database-centric architecture** - Persistent layer is source of truth; Kafka is ephemeral
- ✅ **Monorepo benefits** - Shared `api/proto/` enables compile-time validation across services
- ✅ **High throughput** - Minimize latency by eliminating Schema Registry network calls
- ✅ **Operational simplicity** - One less stateful service to manage, monitor, and backup

## Example Workflow: BIAN Evolution

**Scenario:** BIAN 14.0 adds "Suspend" behavior qualifier to Current Account service domain.

### Step 1: Decide Event Strategy

Question: Is "Suspend" semantically different from "Update"?

**Answer:** Yes - it's a distinct BIAN behavior qualifier with different business semantics.

**Decision:** Create new event type `AccountSuspended`.

### Step 2: Define New Event

```protobuf
// api/proto/events/current_account/v1/events.proto

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

### Step 3: Generate Go Code

```bash
buf generate
```

### Step 4: Validate Compatibility

```bash
buf lint
buf breaking --against main

# ✅ No breaking changes - new file, no modifications to existing schemas

```

### Step 5: Create Kafka Topic

```bash
kafka-topics --create \
  --topic account-suspended \
  --partitions 3 \
  --replication-factor 3 \
  --config retention.ms=604800000  # 7 days
```

### Step 6: Implement Producer

```go
// internal/adapters/events/current_account_publisher.go

func (p *CurrentAccountPublisher) PublishSuspended(
    ctx context.Context,
    account *domain.CurrentAccount,
) error {
    event := &eventspb.AccountSuspended{
        EventId:          uuid.New().String(),
        OccurredAt:       timestamppb.Now(),
        CorrelationId:    getCorrelationID(ctx),
        CausationId:      getCausationID(ctx),
        AccountId:        account.ID.String(),
        SuspensionReason: account.SuspensionReason,
        SuspendedUntil:   timestamppb.New(account.SuspendedUntil),
        SuspendedBy:      account.SuspendedBy,
    }

    return p.producer.Publish(ctx, "account-suspended", event)
}
```

### Step 7: Deploy

- CurrentAccount service begins publishing `AccountSuspended` events
- Consuming services subscribe to new topic when ready
- No coordination needed (new topic, not modified topic)
- Old consumers continue processing `AccountUpdated` events normally

## Consequences

### Positive

- ✅ **Simpler operations** - No Schema Registry service to deploy, monitor, backup
- ✅ **Compile-time safety** - `buf breaking` catches incompatibilities before deployment
- ✅ **Better performance** - No schema registry lookup overhead (~0.01-100ms depending on cache)
- ✅ **BIAN semantic alignment** - Event taxonomy matches BIAN behavior qualifiers
- ✅ **Database-centric** - Leverages persistent layer as authoritative source
- ✅ **Monorepo benefits** - All services have access to latest proto definitions
- ✅ **Reduced complexity** - Fewer moving parts in production environment

### Negative

- ❌ **No centralized schema registry** - Cannot discover schemas at runtime (not needed for internal use)
- ❌ **Monorepo coupling** - Services must share `api/proto/` directory (acceptable for internal services)
- ❌ **No polyglot runtime support** - External consumers would need proto file access (mitigated: use gRPC APIs)
- ❌ **Manual topic creation** - Must explicitly create new topics for new event types (automation possible)

### Mitigations

**Schema discovery:**

- Document event schemas in `docs/events/event-catalog.md`
- Generate schema documentation via `buf` plugins
- Maintain event type registry in documentation

**External integration:**

- Use gRPC APIs for external consumers, not Kafka topics
- gRPC provides schema evolution via protobuf versioning
- External systems never directly consume internal Kafka topics

**Future flexibility:**

- Can add Schema Registry later if external Kafka consumers are needed
- Protobuf messages remain unchanged; only add middleware layer
- No architectural rework required

## When Schema Registry Would Be Needed

Consider adding Schema Registry if:

1. **External Kafka consumers** - Systems outside Meridian directly consume Kafka topics
2. **Polyglot consumers** - Non-Go services need runtime schema discovery
3. **Multi-team governance** - 20+ independent teams need centralized schema governance
4. **Compliance requirements** - Audit trail of schema changes required for regulatory purposes

**Currently:** None of these apply. Kafka is internal coordination; external integration uses gRPC.

## Comparison with Alternatives

### Alternative 1: Schema Registry with Protobuf

**Approach:** Use Confluent Schema Registry for centralized governance.

**Pros:**

- Centralized schema storage and discovery
- Runtime compatibility validation
- Historical schema versions preserved
- Multi-language support via schema ID lookup

**Cons:**

- Operational overhead (another stateful service)
- Performance overhead (network calls for schema lookup)
- Complexity for internal-only use case
- Our monorepo already provides compile-time validation

**Why rejected:** Adds complexity without meaningful benefit for internal Kafka usage.

### Alternative 2: Avro with Schema Registry

**Approach:** Use Avro instead of Protobuf.

**Pros:**

- Schema Registry's native format
- Dynamic schema evolution
- Compact serialization

**Cons:**

- Inconsistent with gRPC APIs (already using Protobuf)
- Less efficient than Protobuf for most workloads
- No compile-time type safety
- Steeper learning curve

**Why rejected:** We've standardized on Protobuf for gRPC APIs. Using different serialization for events would fragment
tooling.

### Alternative 3: JSON with JSON Schema

**Approach:** Human-readable JSON events.

**Pros:**

- Easy to debug (human-readable)
- Ubiquitous tooling
- No code generation

**Cons:**

- Verbose (larger messages)
- No compile-time type safety
- Slower serialization/deserialization
- Inconsistent with gRPC APIs

**Why rejected:** JSON's verbosity and lack of type safety make it unsuitable for high-throughput financial event
streams.

### Chosen: Protobuf Native Versioning (No Schema Registry)

**Rationale:** Balances type safety, performance, simplicity, and consistency with our gRPC APIs. Protobuf's optional
fields handle 90% of evolution needs. New event types handle the remaining 10% (new BIAN behaviors).

## Links

- [ADR-0005: Adapter Pattern for Layer Translation](./0005-adapter-pattern-layer-translation.md)
- [ADR-0006: Schema Management with Adapters](./0006-schema-management-adapters.md)
- [Protocol Buffers Language Guide](https://protobuf.dev/programming-guides/proto3/)
- [buf CLI Documentation](https://buf.build/docs/)
- [BIAN Service Landscape 13.0.0](https://bian.org/servicelandscape-13-0-0/)
- [BIAN Semantic APIs](https://bian.org/semantic-apis/)

## Notes

### Protobuf Evolution Best Practices

**Safe changes (backward compatible):**

- ✅ Add new optional fields
- ✅ Add new message types
- ✅ Add new enum values (with unknown handling)
- ✅ Add new RPC methods

**Breaking changes (require new event type):**

- ❌ Remove existing fields
- ❌ Change field types
- ❌ Change field numbers
- ❌ Rename fields (wire format uses numbers, but breaks code)

**Rule of thumb:** If unsure, create a new event type. It's safer and aligns with BIAN's semantic model.

### BIAN Behavior Qualifiers

BIAN defines standard operations for each service domain:

- **Initiate** - Create new control record
- **Update** - Modify existing control record
- **Retrieve** - Query control record
- **Terminate** - Close/end control record
- **Control** - Manage control record state
- **Exchange** - Exchange information
- **Execute** - Perform an action
- **Request** - Request an action
- **Grant** - Grant permission
- **Notify** - Send notification

When BIAN adds new behavior qualifiers in future releases, map each to a distinct event type.

### Testing Strategy

**Unit tests:** Verify adapter mappings (domain → event)

**Integration tests:** Publish and consume events with test Kafka cluster

**Compatibility tests:** Automated validation in CI/CD via `buf breaking`

**Contract tests:** Verify consumer expectations against producer schemas

See `internal/adapters/events/*_test.go` for examples.

### Event Catalog

Maintain a living document of all event types:

```markdown

# docs/events/event-catalog.md

## Current Account Events

### AccountUpdated

- **Topic:** account-updated
- **Schema:** api/proto/events/current_account/v1/events.proto
- **Producers:** current-account-service
- **Consumers:** position-keeping-service, financial-accounting-service
- **BIAN Qualifier:** Update

### AccountSuspended

- **Topic:** account-suspended
- **Schema:** api/proto/events/current_account/v1/events.proto
- **Producers:** current-account-service
- **Consumers:** position-keeping-service, risk-management-service
- **BIAN Qualifier:** Control (Suspend)
- **Since:** BIAN 14.0

```

Update this catalog when adding new event types.
