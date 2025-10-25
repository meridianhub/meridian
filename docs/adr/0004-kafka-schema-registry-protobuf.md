# 4. Kafka Schema Registry with Protobuf for Strongly-Typed Events

Date: 2025-10-25

## Status

Accepted

## Context

Microservices communicate asynchronously via Kafka events. Without schema enforcement, producers and consumers can drift (producer sends field `customer_id` as string, consumer expects integer), leading to runtime failures in production.

Requirements:
* Strongly-typed event schemas (coming from Java background where immutability and type safety are valued)
* Schema evolution support (add fields without breaking consumers)
* Producer/consumer compatibility validation
* Immutable event definitions (events cannot be modified once published)
* Integration with existing gRPC/Protobuf API contracts

## Decision Drivers

* Already using Protobuf for gRPC APIs (reuse schemas for Kafka events)
* Type safety critical for financial transactions (no runtime parsing errors)
* Schema evolution required (must add fields without redeploying all consumers)
* Compatibility checks needed (prevent breaking changes)
* Immutability valued (events are facts, should not change)
* Need centralized schema registry (single source of truth)

## Considered Options

1. Confluent Schema Registry with Protobuf
2. Avro with Confluent Schema Registry
3. JSON Schema with Confluent Schema Registry
4. No Schema Registry (plain JSON or Protobuf without validation)

## Decision Outcome

Chosen option: "Confluent Schema Registry with Protobuf", because:

* Reuses existing Protobuf definitions from gRPC APIs (single schema language)
* Strongly-typed with compile-time validation in Go
* Schema evolution with backward/forward compatibility checks
* Immutable schemas with version history
* Industry-standard Schema Registry (Confluent open source)
* Protobuf generates Go structs (type-safe, immutable by design)

### Positive Consequences

* Single schema language (Protobuf) for both gRPC and Kafka
* Compile-time type safety (Go compiler validates event structure)
* Schema Registry prevents incompatible changes from being deployed
* Automatic backward/forward compatibility validation
* Protobuf efficiency (smaller messages, faster serialization than JSON/Avro)
* Generated Go code enforces immutability patterns (value types, not pointers)
* Schema versioning provides audit trail of event evolution

### Negative Consequences

* Schema Registry adds operational dependency (must be highly available)
* Protobuf learning curve for teams unfamiliar with it (though already using for gRPC)
* Schema changes require registry update before deploying new code
* Protobuf less human-readable than JSON (debugging requires tools)
* Additional latency for schema validation on first produce/consume

## Pros and Cons of the Options

### Confluent Schema Registry with Protobuf

Centralized registry validates Protobuf schemas before allowing Kafka produce/consume.

* Good, because reuses existing Protobuf definitions (gRPC and Kafka share schemas)
* Good, because strongly-typed with compile-time Go validation
* Good, because backward/forward compatibility enforced by registry
* Good, because efficient (smaller messages, faster than JSON/Avro)
* Good, because generates immutable Go structs
* Good, because industry-standard registry with proven stability
* Bad, because additional operational dependency (Schema Registry must be HA)
* Bad, because schema updates require registry coordination
* Bad, because less human-readable than JSON

### Avro with Confluent Schema Registry

Binary format with dynamic schema resolution.

* Good, because Schema Registry native format (best integration)
* Good, because dynamic schema evolution (readers get schema from message)
* Good, because efficient binary format
* Bad, because different schema language than gRPC (maintain two schema systems)
* Bad, because Go Avro libraries less mature than Protobuf
* Bad, because dynamic typing loses compile-time safety
* Bad, because no immutability guarantees

### JSON Schema with Confluent Schema Registry

JSON with schema validation.

* Good, because human-readable
* Good, because simple for debugging
* Bad, because not type-safe at compile time (runtime validation only)
* Bad, because less efficient than binary formats
* Bad, because JSON not naturally immutable in Go
* Bad, because different schema language than gRPC

### No Schema Registry (Plain Protobuf or JSON)

Services define their own event formats without centralized validation.

* Good, because no additional infrastructure dependency
* Bad, because no compatibility validation (breaking changes go undetected)
* Bad, because schema drift between producers/consumers
* Bad, because no centralized schema documentation
* Bad, because no version history or audit trail

## Links

* [Confluent Schema Registry Documentation](https://docs.confluent.io/platform/current/schema-registry/index.html)
* [Protobuf in Schema Registry](https://docs.confluent.io/platform/current/schema-registry/serdes-develop/serdes-protobuf.html)
* [ADR-0002: Microservices Architecture](./0002-microservices-per-bian-domain.md)
* [GitHub Issue #3: Platform Services](https://github.com/bjcoombs/meridian/issues/3)

## Notes

### Event Schema Structure

Event schemas defined in `.proto` files alongside API contracts:

```
api/proto/events/
├── financial_accounting/
│   ├── ledger_posting_events.proto
│   └── booking_log_events.proto
├── position_keeping/
│   └── transaction_events.proto
└── current_account/
    └── account_events.proto
```

### Example Event Schema

**ledger_posting_events.proto:**
```protobuf
syntax = "proto3";

package meridian.events.financial_accounting.v1;

import "google/protobuf/timestamp.proto";
import "google/type/money.proto";

message LedgerPostingCreated {
  string event_id = 1;              // Unique event identifier
  google.protobuf.Timestamp occurred_at = 2;

  string posting_id = 3;            // Domain entity ID
  string booking_log_id = 4;
  string debit_account = 5;
  string credit_account = 6;
  google.type.Money amount = 7;
  google.protobuf.Timestamp value_date = 8;
  string narrative = 9;
}

message LedgerPostingCompleted {
  string event_id = 1;
  google.protobuf.Timestamp occurred_at = 2;

  string posting_id = 3;
  google.protobuf.Timestamp completed_at = 4;
}
```

Generated Go code is strongly-typed and immutable:

```go
event := &LedgerPostingCreated{
    EventId:   uuid.New().String(),
    OccurredAt: timestamppb.Now(),
    PostingId: posting.ID,
    // ... immutable struct fields
}
```

### Schema Registry Integration

Producers register schema on first publish:

```go
import (
    "github.com/confluentinc/confluent-kafka-go/v2/schemaregistry"
    "github.com/confluentinc/confluent-kafka-go/v2/schemaregistry/serde"
    "github.com/confluentinc/confluent-kafka-go/v2/schemaregistry/serde/protobuf"
)

func NewProducer(registryURL string) (*protobuf.Serializer, error) {
    client, err := schemaregistry.NewClient(schemaregistry.NewConfig(registryURL))
    if err != nil {
        return nil, err
    }

    return protobuf.NewSerializer(client, serde.ValueSerde, protobuf.NewSerializerConfig())
}

func publishEvent(serializer *protobuf.Serializer, topic string, event *LedgerPostingCreated) error {
    payload, err := serializer.Serialize(topic, event)
    if err != nil {
        return err // Schema validation failed
    }

    // Produce to Kafka with validated payload
    return producer.Produce(topic, payload)
}
```

Consumers validate against registered schema:

```go
func NewConsumer(registryURL string) (*protobuf.Deserializer, error) {
    client, err := schemaregistry.NewClient(schemaregistry.NewConfig(registryURL))
    if err != nil {
        return nil, err
    }

    return protobuf.NewDeserializer(client, serde.ValueSerde, protobuf.NewDeserializerConfig())
}

func consumeEvent(deserializer *protobuf.Deserializer, msg []byte) (*LedgerPostingCreated, error) {
    var event LedgerPostingCreated
    err := deserializer.DeserializeInto(topic, msg, &event)
    if err != nil {
        return nil, err // Schema incompatible
    }

    return &event, nil
}
```

### Schema Evolution Rules

Following backward compatibility (consumers can read new schemas):

* ✅ **Add optional fields** - New fields with default values
* ✅ **Remove optional fields** - Old consumers ignore missing fields
* ❌ **Change field types** - Breaking change (int32 → string)
* ❌ **Rename fields** - Breaking change (use field numbers, not names)
* ❌ **Change field numbers** - Breaking change

Schema Registry enforces compatibility on registration:

```bash
# Register new schema version (backward compatible check)
curl -X POST http://schema-registry:8081/subjects/ledger-posting-created-value/versions \
  -H "Content-Type: application/vnd.schemaregistry.v1+json" \
  -d '{"schemaType": "PROTOBUF", "schema": "..."}'
```

### Immutability Patterns in Go

Protobuf generates structs that encourage immutability:

```go
// Good: Create immutable event
event := &LedgerPostingCreated{
    EventId: uuid.New().String(),
    Amount: &money.Money{
        CurrencyCode: "USD",
        Units: 100,
    },
}

// Bad: Modifying event after creation (avoid this)
event.Amount.Units = 200  // Mutations are visible, but discouraged

// Good: Return new copy instead of modifying
func WithNarrative(event *LedgerPostingCreated, narrative string) *LedgerPostingCreated {
    return &LedgerPostingCreated{
        EventId: event.EventId,
        OccurredAt: event.OccurredAt,
        // ... copy all fields
        Narrative: narrative,
    }
}
```

### Deployment Process

1. Update `.proto` schema with new fields
2. Generate Go code: `protoc --go_out=. ledger_posting_events.proto`
3. Register schema with Schema Registry (CI/CD step)
4. Deploy producers with new schema
5. Deploy consumers (backward compatible, can read old and new)

### Future Considerations

* Consider schema governance policies (who can modify schemas)
* May need schema versioning strategy per BIAN domain
* Watch for schema registry performance at scale
* Consider schema registry high availability and disaster recovery
* May add schema linting rules for consistency
