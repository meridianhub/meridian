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

Amended: 2025-11-19 - Added event topic naming convention, outbox pattern, and idempotency pattern

Amended: 2026-02-28 - Added BIAN v14 AsyncAPI specification awareness

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
- [BIAN Service Landscape 14.0.0](https://bian.org/servicelandscape-14-0-0/)
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

## Amendment: Event Topic Naming Convention (2025-11-19)

### Context

The original ADR mentioned "one topic per event type" with examples like `account-updated` and `account-suspended`, but didn't specify a formal naming convention. As we scaled to 26+ events across 3 services, we needed an explicit convention to prevent naming collisions and support versioning.

**Problems without formal convention:**

- Namespace collision risk (e.g., `transaction-failed` could come from CurrentAccount or PositionKeeping)
- Unclear version management (where does version info go?)
- Inconsistent naming across services

### Decision

Adopt **`<service>.<event-name>.<version>`** topic naming convention.

**Examples:**

```text
current-account.account-created.v1
current-account.transaction-initiated.v1
current-account.account-transaction-failed.v1

position-keeping.transaction-recorded.v1
position-keeping.position-updated.v1
position-keeping.transaction-failed.v1

financial-accounting.posting-captured.v1
financial-accounting.booking-log-created.v1
```

**Rationale:**

- **Namespace isolation**: Service prefix prevents collisions
- **Explicit versioning**: Version suffix enables backward-compatible evolution
- **Discovery**: Easy to filter by service (`current-account.*`) or pattern
- **BIAN alignment**: Service name matches BIAN domain

### Alternatives Considered

#### 1. Flat Naming (Original ADR Examples)

```text
account-updated
account-suspended
transaction-failed
```

**Pros:**

- Simpler, shorter names
- Less typing

**Cons:**

- **Namespace collision**: `transaction-failed` ambiguous (which service?)
- **No versioning**: Where does version go? `transaction-failed-v2`?
- **Filtering**: Can't filter by service in Kafka tools

**Why rejected:** Namespace collisions and no clear versioning path.

#### 2. Hierarchical Topic Structure

```text
meridian/current-account/account-created/v1
meridian/position-keeping/transaction-recorded/v1
```

**Pros:**

- Even more explicit hierarchy
- Organization-level namespace

**Cons:**

- **Kafka doesn't support `/` in topic names** (must use `.` or `-`)
- Overly verbose for internal-only events
- Harder to type and reference

**Why rejected:** Kafka limitation and unnecessary complexity for internal events.

#### 3. Version in Event Message (Not Topic)

Keep topic names flat, embed version in event payload:

```protobuf
message AccountCreated {
  string version = 1;  // "v1"
  ...
}
```

**Pros:**

- Simpler topic names
- Version travels with message

**Cons:**

- **Consumer complexity**: Must inspect message to know version
- **Topic retention**: Can't have different retention per version
- **Kafka tools**: Can't filter by version in Kafka UI/CLI

**Why rejected:** Versioning needs to be visible at topic level for operational clarity.

### Implementation

**Topic creation pattern:**

```bash
kafka-topics --create \
  --topic current-account.account-created.v1 \
  --partitions 3 \
  --replication-factor 3 \
  --config retention.ms=604800000  # 7 days
```

**Producer code:**

```go
func (p *EventPublisher) PublishAccountCreated(ctx context.Context, event *pb.AccountCreatedEvent) error {
    return p.producer.Publish(ctx, "current-account.account-created.v1", event)
}
```

**Consumer subscription:**

```go
consumer.Subscribe([]string{
    "current-account.account-created.v1",
    "current-account.account-updated.v1",
})
```

**Versioning strategy:**

- **v1 → v2**: Create new topic `current-account.account-created.v2`
- **Consumers**: Subscribe to both v1 and v2 during migration
- **Producers**: Publish to v2 only after all consumers support it
- **Deprecation**: Delete v1 topic after migration complete (30 days)

### Consequences

**Positive:**

- ✅ No namespace collisions across services
- ✅ Explicit versioning enables blue-green event migrations
- ✅ Easy discovery and filtering in Kafka tools
- ✅ Self-documenting topic names

**Negative:**

- ❌ Longer topic names (vs flat naming)
- ❌ Version migration requires new topic creation

**Mitigations:**

- **Length**: Acceptable trade-off for clarity (max 50 chars)
- **Migration**: Automate topic creation in deployment scripts

### References

- [Event-Driven Architecture](../architecture/event-driven-architecture.md#topic-naming)
- [CurrentAccount Events Proto](../../api/proto/meridian/events/v1/current_account_events.proto)

---

## Amendment: Outbox Pattern for Reliable Event Publishing (2025-11-19)

### Context

The original ADR focused on event schema evolution but didn't address **reliable event publishing**. We need to guarantee that domain events are published exactly once when state changes are persisted to the database.

**Problem:** Dual-write problem

```go
// ❌ Unsafe: Database and Kafka are separate transactions
db.Insert(account)         // Transaction 1
kafka.Publish(event)       // Transaction 2 - what if this fails?
```

**Failure scenarios:**

- Database succeeds, Kafka fails → State changed but no event published
- Kafka succeeds, database fails → Event published but state unchanged
- Process crashes between operations → Inconsistent state

### Decision

Use **transactional outbox pattern** for at-least-once event delivery guarantees.

**Pattern:**

1. Write domain event to `outbox` table in **same transaction** as business data
2. Background process polls outbox table and publishes to Kafka
3. Mark event as published after successful Kafka acknowledgment
4. Consumers use idempotency keys to handle duplicates

**Implementation:**

```sql
-- Outbox table (per service database)
CREATE TABLE outbox (
    id UUID PRIMARY KEY,
    aggregate_type VARCHAR(100) NOT NULL,
    aggregate_id VARCHAR(100) NOT NULL,
    event_type VARCHAR(100) NOT NULL,
    event_payload JSONB NOT NULL,
    topic_name VARCHAR(255) NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    published_at TIMESTAMP,
    retry_count INT NOT NULL DEFAULT 0,
    INDEX idx_unpublished (published_at, created_at) WHERE published_at IS NULL
);
```

**Producer code:**

```go
func (s *AccountService) CreateAccount(ctx context.Context, req *pb.CreateAccountRequest) error {
    return s.db.RunInTransaction(ctx, func(tx *sql.Tx) error {
        // Step 1: Insert account
        account := &domain.Account{...}
        if err := s.repo.Insert(tx, account); err != nil {
            return err
        }

        // Step 2: Insert event to outbox (same transaction)
        event := &events.AccountCreatedEvent{
            EventId:   uuid.New().String(),
            AccountId: account.ID,
            ...
        }
        if err := s.outbox.Insert(tx, event); err != nil {
            return err
        }

        return nil  // Commit transaction atomically
    })
}
```

**Outbox publisher (background process):**

```go
func (p *OutboxPublisher) Run(ctx context.Context) {
    ticker := time.NewTicker(100 * time.Millisecond)
    for {
        select {
        case <-ticker.C:
            events := p.outbox.FetchUnpublished(limit=100)
            for _, event := range events {
                if err := p.kafka.Publish(event.Topic, event.Payload); err != nil {
                    p.outbox.IncrementRetry(event.ID)
                    continue
                }
                p.outbox.MarkPublished(event.ID, time.Now())
            }
        case <-ctx.Done():
            return
        }
    }
}
```

### Alternatives Considered

#### 1. Direct Kafka Publish (Dual Write)

**Approach:** Publish to Kafka directly after database commit.

**Pros:**

- Simpler code
- Lower latency

**Cons:**

- **Not transactional**: Database and Kafka are separate operations
- **Failure modes**: Publish can fail after DB commit
- **Inconsistency**: State and events can diverge

**Why rejected:** Unacceptable for financial transactions where state-event consistency is critical.

#### 2. Kafka Transaction API

**Approach:** Use Kafka's transactional producer API.

**Pros:**

- Exactly-once semantics within Kafka
- Built-in transaction support

**Cons:**

- **Database not included**: Kafka transactions only cover Kafka operations
- **Complex**: Requires transaction coordinator, more moving parts
- **Performance**: Higher latency due to transaction protocol

**Why rejected:** Kafka transactions don't solve the dual-write problem (database + Kafka).

#### 3. Change Data Capture (CDC)

**Approach:** Use Debezium to stream database changes to Kafka.

**Pros:**

- Zero application code changes
- Guaranteed delivery (database is source of truth)
- Works for legacy systems

**Cons:**

- **Operational complexity**: Debezium, Kafka Connect, schema registry
- **Event structure**: CDC events are table-centric, not domain-event-centric
- **Transformation**: Need to transform CDC events to domain events
- **Coupling**: Events coupled to database schema

**Why rejected:** Operational overhead and table-centric events don't align with BIAN domain events.

### Implementation Guidelines

**Polling strategy:**

- Frequency: 100ms (10 events/sec per service minimum)
- Batch size: 100 events per poll
- Retry: Exponential backoff (1s, 2s, 4s, 8s, max 60s)
- Dead letter: Move to DLQ after 10 retries

**Outbox cleanup:**

- Retention: 7 days (matches Kafka retention)
- Cleanup: Daily cron job to delete old published events
- Monitoring: Alert if outbox grows beyond threshold (1000 unpublished events)

**Idempotency:**

- Consumers must use `event_id` for deduplication (see Idempotency Amendment)
- At-least-once delivery guarantees may cause duplicates
- Outbox publisher may retry on transient Kafka failures

### Consequences

**Positive:**

- ✅ Transactional consistency: Events always match database state
- ✅ At-least-once delivery: Events guaranteed to be published
- ✅ Resilience: Survives Kafka outages (events queued in outbox)
- ✅ Debugging: Outbox table provides audit trail

**Negative:**

- ❌ Additional table: Outbox table adds storage overhead
- ❌ Latency: 100ms delay before event published (vs direct publish)
- ❌ Duplicates: Consumers must handle at-least-once semantics

**Mitigations:**

- **Storage**: Outbox cleanup after 7 days (auto-vacuuming)
- **Latency**: 100ms acceptable for event-driven coordination
- **Duplicates**: Idempotency pattern (see next amendment)

### References

- [Event-Driven Architecture: Outbox Pattern](../architecture/event-driven-architecture.md#outbox-pattern)
- [Saga Orchestration](0002-microservices-per-bian-domain.md#amendment-saga-orchestration-pattern-2025-11-19)

---

## Amendment: Idempotency Pattern for Event Consumers (2025-11-19)

### Context

With the Outbox Pattern providing at-least-once delivery, consumers must handle duplicate events gracefully. Kafka's at-least-once semantics mean the same event may be delivered multiple times during retries, rebalancing, or failures.

**Problem: Duplicate processing**

```text
Publisher fails after Kafka ack but before marking outbox → Retries → Duplicate event
Consumer crashes after processing but before committing offset → Reprocesses event
```

**Without idempotency:**

```go
func HandleAccountCreated(event *events.AccountCreatedEvent) {
    balance.Credit(event.Amount)  // ❌ Duplicate = double credit!
}
```

### Decision

Implement **event_id-based idempotency** for all event consumers using Redis or database deduplication.

**Pattern:**

1. Check if `event_id` already processed before handling event
2. Process event only if `event_id` not seen
3. Store `event_id` with TTL matching event retention (7 days)

**Implementation (Redis):**

```go
func (c *EventConsumer) HandleAccountCreated(ctx context.Context, event *events.AccountCreatedEvent) error {
    // Step 1: Check if already processed
    key := fmt.Sprintf("event:processed:%s", event.EventId)
    exists, err := c.redis.Exists(ctx, key).Result()
    if err != nil {
        return fmt.Errorf("redis check failed: %w", err)
    }
    if exists {
        c.logger.Debug("Event already processed", "event_id", event.EventId)
        return nil  // Idempotent: skip duplicate
    }

    // Step 2: Process event
    if err := c.accountService.CreateAccount(ctx, event); err != nil {
        return fmt.Errorf("failed to create account: %w", err)
    }

    // Step 3: Mark as processed (TTL = 7 days)
    if err := c.redis.Set(ctx, key, "1", 7*24*time.Hour).Err(); err != nil {
        return fmt.Errorf("failed to mark processed: %w", err)
    }

    return nil
}
```

**Implementation (Database):**

```sql
CREATE TABLE processed_events (
    event_id UUID PRIMARY KEY,
    event_type VARCHAR(100) NOT NULL,
    processed_at TIMESTAMP NOT NULL DEFAULT NOW(),
    INDEX idx_processed_at (processed_at)
);

-- Cleanup old events (daily cron)
DELETE FROM processed_events WHERE processed_at < NOW() - INTERVAL '7 days';
```

### Alternatives Considered

#### 1. Exactly-Once Kafka Semantics

**Approach:** Use Kafka's exactly-once transactional producers and consumers.

**Pros:**

- Built-in deduplication
- No application-level idempotency needed

**Cons:**

- **Performance**: Higher latency (~100ms overhead)
- **Complexity**: Requires transactional coordinator, more config
- **Limited scope**: Only works within Kafka (doesn't cover database operations)
- **Operational**: More failure modes to monitor

**Why rejected:** Performance overhead and limited scope don't justify complexity for our use case.

#### 2. Kafka Consumer Offset Management

**Approach:** Only commit offset after successful processing.

**Pros:**

- No external storage needed
- Kafka native

**Cons:**

- **Not idempotent**: Rebalancing or crashes still cause reprocessing
- **Unsafe**: Process → crash → offset not committed → reprocess
- **No guarantee**: At-least-once semantics still apply

**Why rejected:** Doesn't solve the fundamental duplicate problem.

#### 3. Database Unique Constraint on Event ID

**Approach:** Use database unique constraint to prevent duplicate processing.

**Pros:**

- Transactional with business data
- No external dependencies

**Cons:**

- **Coupling**: Event deduplication coupled to business tables
- **Schema**: Requires event_id column in all tables
- **Cleanup**: Hard to clean up old event IDs

**Why rejected:** Couples deduplication to business schema; Redis provides better separation of concerns.

### Implementation Guidelines

**Storage choice:**

- **Redis**: Preferred for high throughput (O(1) lookups, TTL built-in)
- **Database**: Alternative if Redis unavailable (add index on event_id)

**TTL strategy:**

- Match Kafka retention: 7 days
- Rationale: Event won't be replayed after 7 days (purged from Kafka)

**Error handling:**

- Redis failure → Log warning, process anyway (risk: potential duplicate)
- Database failure → Return error, retry later (safer but impacts availability)

**Monitoring:**

- Track duplicate events: `event.duplicate.count`
- Alert if duplicate rate > 5% (indicates producer retry issues)

**Testing:**

```go
func TestIdempotency(t *testing.T) {
    event := &events.AccountCreatedEvent{EventId: "test-123", ...}

    // First processing
    err := consumer.HandleAccountCreated(ctx, event)
    assert.NoError(t, err)
    assert.Equal(t, 1, accountRepo.Count())

    // Second processing (duplicate)
    err = consumer.HandleAccountCreated(ctx, event)
    assert.NoError(t, err)
    assert.Equal(t, 1, accountRepo.Count())  // Still 1, not 2!
}
```

### Consequences

**Positive:**

- ✅ Safe at-least-once semantics: Duplicates handled gracefully
- ✅ Simple pattern: Check → Process → Mark
- ✅ Observability: Track duplicate rates in metrics
- ✅ Performance: Redis provides fast lookups (sub-millisecond)

**Negative:**

- ❌ External dependency: Requires Redis or database
- ❌ Storage: Stores event IDs for 7 days
- ❌ Failure mode: Redis failure risks duplicate processing

**Mitigations:**

- **Redis HA**: Deploy Redis with replication (3 replicas)
- **Storage**: Auto-cleanup with TTL (low overhead)
- **Failure**: Log duplicates as warnings (business logic should be retry-safe anyway)

### Related Patterns

- **Outbox Pattern**: Guarantees at-least-once delivery (this pattern handles the duplicates)
- **Event Sourcing**: Can use event_id as aggregate version for optimistic locking
- **Circuit Breaker**: Protect against Redis cascading failures

### References

- [Event-Driven Architecture: Idempotent Consumers](../architecture/event-driven-architecture.md#idempotent-consumers)
- [Outbox Pattern Amendment](#amendment-outbox-pattern-for-reliable-event-publishing-2025-11-19)
- [CurrentAccount API Contract: Idempotency](../architecture/api-contracts/current-account-contract.md#idempotency)

---

## Amendment: BIAN v14 AsyncAPI Specification Awareness (2026-02-28)

### Context

BIAN v14.0.0 introduces formal [AsyncAPI 3.0.0](https://www.asyncapi.com/docs/reference/specification/v3.0.0) specifications for all 259 service domains. This is the first time BIAN has provided machine-readable event definitions alongside its existing OpenAPI (REST) specs. Each service domain now includes an AsyncAPI YAML defining channels with `Created` and `Updated` message patterns (e.g., `OutboundMessage/Created`, `OutboundMessage/Updated`).

**Example (BIAN AsyncAPI for CurrentAccount):**

```yaml
channels:
  OutboundMessage/Created:
    messages:
      CurrentAccountOutbound:
        payload:
          $ref: '#/components/schemas/CurrentAccountOutbound'
  OutboundMessage/Updated:
    messages:
      CurrentAccountOutbound:
        payload:
          $ref: '#/components/schemas/CurrentAccountOutbound'
```

**BIAN's approach is transport-agnostic**: channel names like `OutboundMessage/Created` are abstract and don't prescribe Kafka topic names, message broker topology, or serialization format.

### Decision

Acknowledge BIAN AsyncAPI specs as a **reference for payload structure** but continue using Meridian's existing topic naming convention (`<service>.<event-name>.<version>`) and protobuf serialization.

**Rationale:**

1. **Topic naming**: Meridian's dot-notation convention (`current-account.account-created.v1`) encodes service ownership directly in the topic name, which is operationally valuable for filtering, access control, and debugging. BIAN's `OutboundMessage/Created` channel naming is transport-agnostic and doesn't provide this operational benefit.

2. **Payload alignment**: BIAN AsyncAPI schemas define JSON payloads matching the OpenAPI models. Meridian's Kafka events use protobuf serialization (per this ADR) with domain-specific event types rather than generic `ServiceDomainOutbound` wrappers. Our adapter layer (ADR-0005) handles the translation between BIAN's model structure and Meridian's internal event schemas.

3. **Serialization**: BIAN AsyncAPI specs assume JSON payloads. Meridian uses protobuf for type safety, performance, and consistency with gRPC APIs (per this ADR's original decision). This remains the correct choice for internal event coordination.

4. **Granularity**: BIAN defines two channels per domain (Created/Updated). Meridian uses fine-grained event types per BIAN behavior qualifier (AccountCreated, AccountSuspended, TransactionInitiated, etc.), which provides better consumer filtering and clearer domain semantics.

### Future Considerations

- **Payload structure review**: When implementing new service domains, cross-reference BIAN AsyncAPI payload schemas to ensure Meridian's protobuf event fields capture equivalent domain data.
- **External integration**: If Meridian ever exposes event streams to external consumers, BIAN AsyncAPI specs could inform the external-facing contract while the internal protobuf events remain unchanged.
- **BIAN evolution**: Monitor future BIAN releases for more prescriptive async patterns (e.g., CloudEvents envelope, transport-specific bindings) that may warrant revisiting this position.

### References

- [BIAN v14 AsyncAPI Specs](https://github.com/bian-official/public/tree/main/release14.0.0/semantic-apis/asyncapi)
- [BIAN v14.0 Release Notes](https://bian.org/wp-content/uploads/2025/01/BIAN-v14.0-Release-Notes-v1.0.pdf)
- [Topic Naming Amendment](#amendment-event-topic-naming-convention-2025-11-19)
