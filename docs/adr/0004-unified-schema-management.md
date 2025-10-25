# 4. Unified Schema Management with Protobuf

Date: 2025-10-25

## Status

Accepted (Revised)

## Context

The system requires **three representations of the same domain model**:
1. **Database schema** (PostgreSQL tables)
2. **Go structs** (application domain model)
3. **Kafka events** (Protobuf messages)

These must stay synchronized to prevent runtime errors and data inconsistencies. Maintaining three separate schema definitions leads to drift, type mismatches, and production bugs.

## Decision Drivers

* **Single source of truth** for domain model
* Type safety across all layers (database, application, messaging)
* Schema evolution without breaking changes
* Immutability and compile-time validation
* Integration with existing gRPC/Protobuf infrastructure
* Automated synchronization between layers
* Reduce manual schema maintenance overhead

## Decision Outcome

**Unified approach using Go structs as the primary source of truth:**

1. **Go structs** define domain model (with GORM tags)
2. **Atlas** generates database migrations from Go structs
3. **Custom code generator** creates Protobuf schemas from Go structs
4. **Schema Registry** validates Protobuf compatibility

### Architecture

```
┌─────────────────────────────────────┐
│   Go Structs (Source of Truth)     │
│   internal/domain/models.go         │
│   - GORM tags for database          │
│   - Custom tags for Protobuf        │
└─────────────────────────────────────┘
              ↓
    ┌─────────┴─────────┐
    ↓                   ↓
┌─────────┐      ┌──────────────┐
│ Atlas   │      │ protogen     │
│ (GORM)  │      │ (custom)     │
└─────────┘      └──────────────┘
    ↓                   ↓
┌─────────┐      ┌──────────────┐
│Database │      │Protobuf      │
│Migration│      │Schema        │
└─────────┘      └──────────────┘
                        ↓
                 ┌──────────────┐
                 │Schema        │
                 │Registry      │
                 └──────────────┘
```

### Benefits of This Approach

✅ **Single source of truth** (Go structs)
✅ **Type safety** across all layers
✅ **Automatic synchronization** (generators keep schemas in sync)
✅ **Compile-time validation** (Go compiler catches errors)
✅ **Schema evolution** (Atlas + Schema Registry validate compatibility)
✅ **Immutability** (Go structs, Protobuf messages are immutable)
✅ **CI/CD integration** (automated validation and deployment)
✅ **Reduced manual work** (no SQL or Protobuf writing for most changes)

### Trade-offs

* Requires custom Protobuf generator (one-time investment)
* Go structs must accommodate both database and Protobuf constraints
* Schema changes require running multiple generators
* Custom tooling maintenance overhead

## Implementation

### 1. Define Go Struct (Single Source of Truth)

**internal/domain/models.go:**
```go
package domain

import (
    "time"
    "github.com/google/uuid"
)

// FinancialBookingLog represents a financial booking entry
// @proto:message FinancialBookingLogCreated
type FinancialBookingLog struct {
    ID              uuid.UUID  `gorm:"type:uuid;primary_key" proto:"id,1"`
    ControlRecordID string     `gorm:"uniqueIndex;not null" proto:"control_record_id,2"`
    BookingPurpose  string     `gorm:"not null" proto:"booking_purpose,3"`
    Amount          float64    `gorm:"not null" proto:"amount,4"`
    Currency        string     `gorm:"not null;size:3" proto:"currency,5"`
    ValueDate       time.Time  `gorm:"not null" proto:"value_date,6"`
    CreatedAt       time.Time  `gorm:"not null" proto:"created_at,7"`
    Version         int        `gorm:"not null" proto:"version,8"`
}
```

### 2. Generate Database Migration (Atlas)

```bash
atlas migrate diff add_booking_log \
  --env gorm \
  --to "gorm://internal/domain"
```

**Generated SQL (migrations/20250125120000_add_booking_log.sql):**
```sql
CREATE TABLE "financial_booking_logs" (
  "id" uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  "control_record_id" text NOT NULL UNIQUE,
  "booking_purpose" text NOT NULL,
  "amount" double precision NOT NULL,
  "currency" varchar(3) NOT NULL,
  "value_date" timestamptz NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "version" integer NOT NULL DEFAULT 1
);
```

### 3. Generate Protobuf Schema (Custom Tool)

```bash
# Custom code generator reads Go structs
go run tools/protogen/main.go \
  --input internal/domain \
  --output api/proto/events
```

**Generated Protobuf (api/proto/events/financial_accounting.proto):**

```protobuf
syntax = "proto3";

package meridian.events.financial_accounting.v1;

import "google/protobuf/timestamp.proto";

message FinancialBookingLogCreated {
  string event_id = 1;
  google.protobuf.Timestamp occurred_at = 2;

  // Domain fields from Go struct
  string id = 3;
  string control_record_id = 4;
  string booking_purpose = 5;
  double amount = 6;
  string currency = 7;
  google.protobuf.Timestamp value_date = 8;
  google.protobuf.Timestamp created_at = 9;
  int32 version = 10;
}
```

### 4. Register with Schema Registry

```go
// Automated in CI/CD
func registerSchema(client *schemaregistry.Client, schema string) error {
    _, err := client.CreateSchema(
        "financial-booking-log-created-value",
        schema,
        schemaregistry.Protobuf,
    )
    return err
}
```

## Custom Protobuf Generator

**tools/protogen/main.go:**

```go
package main

import (
    "go/ast"
    "go/parser"
    "go/token"
    "reflect"
    "strings"
)

func generateProto(structName string, fields []Field) string {
    var sb strings.Builder

    sb.WriteString(fmt.Sprintf("message %sCreated {\n", structName))
    sb.WriteString("  string event_id = 1;\n")
    sb.WriteString("  google.protobuf.Timestamp occurred_at = 2;\n\n")

    fieldNum := 3
    for _, field := range fields {
        protoType := goTypeToProto(field.Type)
        protoName := toSnakeCase(field.Name)

        sb.WriteString(fmt.Sprintf("  %s %s = %d;\n",
            protoType, protoName, fieldNum))
        fieldNum++
    }

    sb.WriteString("}\n")
    return sb.String()
}

func goTypeToProto(goType string) string {
    switch goType {
    case "string":
        return "string"
    case "int", "int32":
        return "int32"
    case "int64":
        return "int64"
    case "float64":
        return "double"
    case "time.Time":
        return "google.protobuf.Timestamp"
    case "uuid.UUID":
        return "string"
    default:
        return "string"
    }
}
```

## Workflow

### When Adding a New Field

**1. Update Go struct:**
```go
type FinancialBookingLog struct {
    // ... existing fields
    Status string `gorm:"not null;default:'pending'" proto:"status,11"`
}
```

**2. Run generators:**
```bash
# Generate database migration
atlas migrate diff add_status --env gorm

# Generate Protobuf schema
go run tools/protogen/main.go

# Compile Protobuf
protoc --go_out=. api/proto/events/*.proto
```

**3. CI/CD validates and deploys:**
```yaml
- name: Lint database migration
  run: atlas migrate lint --env gorm

- name: Register Protobuf schema
  run: go run tools/register-schema/main.go

- name: Deploy
  run: kubectl apply -f k8s/
```

## Kafka Event Integration

### Producer with Schema Registry

**Publishing events with generated Protobuf:**

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

func publishEvent(serializer *protobuf.Serializer, topic string, event *FinancialBookingLogCreated) error {
    payload, err := serializer.Serialize(topic, event)
    if err != nil {
        return err // Schema validation failed
    }

    // Produce to Kafka with validated payload
    return producer.Produce(topic, payload)
}
```

### Consumer with Schema Registry

```go
func NewConsumer(registryURL string) (*protobuf.Deserializer, error) {
    client, err := schemaregistry.NewClient(schemaregistry.NewConfig(registryURL))
    if err != nil {
        return nil, err
    }

    return protobuf.NewDeserializer(client, serde.ValueSerde, protobuf.NewDeserializerConfig())
}

func consumeEvent(deserializer *protobuf.Deserializer, msg []byte) (*FinancialBookingLogCreated, error) {
    var event FinancialBookingLogCreated
    err := deserializer.DeserializeInto(topic, msg, &event)
    if err != nil {
        return nil, err // Schema incompatible
    }

    return &event, nil
}
```

## Schema Evolution Rules

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

## CI/CD Integration

### Automated Schema Management

**.github/workflows/schema-sync.yml:**
```yaml
name: Schema Synchronization

on:
  push:
    paths:
      - 'internal/domain/**'

jobs:
  generate-and-validate:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3

      - name: Setup Go
        uses: actions/setup-go@v4
        with:
          go-version: '1.23'

      - name: Generate database migration
        run: |
          atlas migrate diff auto_migration \
            --env gorm \
            --to "gorm://internal/domain"

      - name: Lint database migration
        run: |
          atlas migrate lint \
            --env gorm \
            --latest 1

      - name: Generate Protobuf schemas
        run: |
          go run tools/protogen/main.go \
            --input internal/domain \
            --output api/proto/events

      - name: Compile Protobuf
        run: |
          protoc --go_out=. api/proto/events/*.proto

      - name: Register schemas with Schema Registry
        run: |
          go run tools/register-schema/main.go \
            --registry ${{ secrets.SCHEMA_REGISTRY_URL }}

      - name: Commit generated files
        run: |
          git config user.name "GitHub Actions"
          git config user.email "actions@github.com"
          git add migrations/ api/proto/events/
          git commit -m "chore: auto-generate schemas from domain models" || true
          git push
```

## Immutability Patterns

Protobuf generates structs that encourage immutability:

```go
// Good: Create immutable event
event := &FinancialBookingLogCreated{
    EventId: uuid.New().String(),
    OccurredAt: timestamppb.Now(),
    Id: booking.ID.String(),
    Amount: booking.Amount,
    Currency: booking.Currency,
}

// Bad: Modifying event after creation (avoid this)
event.Amount = 200  // Mutations are visible, but discouraged

// Good: Return new copy instead of modifying
func WithNarrative(event *FinancialBookingLogCreated, narrative string) *FinancialBookingLogCreated {
    return &FinancialBookingLogCreated{
        EventId: event.EventId,
        OccurredAt: event.OccurredAt,
        // ... copy all fields
        BookingPurpose: narrative,
    }
}
```

## Comparison with Alternatives

### Why Not Separate Schema Definitions?

**Rejected approach: Manually maintain 3 schemas**
* ❌ Manual SQL migrations
* ❌ Manually write Protobuf schemas
* ❌ Keep Go structs in sync manually
* ❌ High risk of drift and runtime errors

**Chosen approach: Go structs as source of truth**
* ✅ Single definition, multiple outputs
* ✅ Compile-time validation
* ✅ Automated synchronization
* ✅ Type safety across stack

## Links

* [Atlas Documentation](https://atlasgo.io/)
* [Confluent Schema Registry](https://docs.confluent.io/platform/current/schema-registry/index.html)
* [Protobuf in Schema Registry](https://docs.confluent.io/platform/current/schema-registry/serdes-develop/serdes-protobuf.html)
* [ADR-0003: Database Schema Migrations](./0003-database-schema-migrations.md)
* [GitHub Issue #3: Platform Services](https://github.com/bjcoombs/meridian/issues/3)

## Notes

### Future Enhancements

* **Schema validation CLI**: Validate Go struct changes before commit
* **Documentation generation**: Auto-generate schema docs from Go structs
* **Migration preview**: Show SQL and Protobuf changes before applying
* **Rollback support**: Coordinate rollbacks across database and Schema Registry
* **Multi-service coordination**: Handle schema changes affecting multiple services

### Maintenance Considerations

* Custom protogen tool requires maintenance as Go/Protobuf evolves
* Team must understand the relationship between Go tags and generated schemas
* Breaking changes require careful coordination across all three layers
* Schema Registry must be highly available (critical dependency)
