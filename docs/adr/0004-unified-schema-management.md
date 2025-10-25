# 4. Separated Schema Management with Adapters

Date: 2025-10-25 (Revised: 2025-10-25)

## Status

Accepted

Supersedes initial unified schema approach. This ADR establishes separated concerns architecture with explicit adapters between layers.

## Context

The system requires **three representations with different concerns**:
1. **Domain models** - Business logic, invariants, behavior (pure Go)
2. **Database schema** - Persistence, audit trails, indexes, denormalization (PostgreSQL tables)
3. **Event schemas** - Serialization, versioning, compatibility (Kafka Protobuf messages)

Each layer has **distinct requirements**:
- **Domain layer:** Rich types, domain methods, business rules (no infrastructure concerns)
- **Persistence layer:** Audit fields (`created_by`, `updated_by`, `deleted_at`), indexes, GORM tags, optimizations
- **Event layer:** Event metadata (`event_id`, `correlation_id`, `occurred_at`), versioning, backward compatibility

**Initial approach (unified schema)** seemed elegant but proved inflexible in practice:
* Database audit fields polluted domain models
* API versioning forced domain model changes
* Event metadata (correlation IDs, headers) had no place in domain
* Tight coupling between infrastructure and business logic

Based on real-world experience and industry best practices (Google, LinkedIn, Netflix, AWS all separate these concerns), we need explicit boundaries between layers.

## Decision Drivers

* **Separation of concerns** - each layer optimizes for its purpose
* Type safety across boundaries with explicit translation
* Schema evolution independence (database, domain, events version separately)
* Flexibility for layer-specific requirements (audit fields, event metadata, rich domain types)
* Domain-Driven Design principles (domain model not polluted by infrastructure)
* Integration with existing gRPC/Protobuf and database tooling
* Automated validation of mapping correctness
* Industry best practices (Hexagonal Architecture, Anti-Corruption Layer pattern)

## Decision Outcome

**Separated architecture with explicit adapters between layers:**

1. **Domain models** define business logic (pure Go, no infrastructure tags)
2. **Database entities** define persistence (GORM tags, Atlas generates migrations)
3. **Protobuf schemas** define events/APIs (manually maintained, Schema Registry validates)
4. **Adapters** translate between layers with automated compatibility tests

### Architecture

```
┌────────────────────────────────────────────────┐
│   Domain Layer (internal/domain)              │
│   - Pure business logic                        │
│   - No persistence/serialization concerns      │
│   - Rich types (Money, Status enums)           │
└────────────────────────────────────────────────┘
                    ↓ (Adapters)
        ┌───────────┼────────────────┐
        ↓           ↓                ↓
┌──────────────┐ ┌──────────────┐ ┌──────────────┐
│ Persistence  │ │   Events     │ │   gRPC API   │
│  (Adapter)   │ │  (Adapter)   │ │  (Adapter)   │
├──────────────┤ ├──────────────┤ ├──────────────┤
│ BookingLog   │ │ BookingLog   │ │ BookingLog   │
│ Entity       │ │ Created      │ │ Response     │
│ - GORM tags  │ │ Event        │ │ - Proto v1   │
│ - Audit cols │ │ - Event meta │ │ - Versioned  │
│              │ │ - Proto      │ │              │
└──────────────┘ └──────────────┘ └──────────────┘
      ↓                ↓                ↓
┌──────────────┐ ┌──────────────┐ ┌──────────────┐
│   Database   │ │    Kafka     │ │  gRPC Svc    │
│  (Postgres)  │ │   Topics     │ │  Endpoints   │
└──────────────┘ └──────────────┘ └──────────────┘
      ↓                ↓
┌──────────────┐ ┌──────────────┐
│    Atlas     │ │   Schema     │
│  Migrations  │ │   Registry   │
└──────────────┘ └──────────────┘
```

### Benefits of This Approach

✅ **Separation of concerns** - domain, persistence, and events evolve independently
✅ **Flexibility** - each layer optimized for its purpose (audit fields, rich types, event metadata)
✅ **Type safety** - explicit adapters with compile-time validation
✅ **Domain purity** - business logic not polluted by infrastructure concerns (DDD principles)
✅ **Independent versioning** - API v2 doesn't force domain changes, database can add audit columns without domain changes
✅ **Schema evolution** - Atlas for migrations, Schema Registry for events, both independent
✅ **Testability** - adapter tests validate mapping correctness, domain tests focus on business logic
✅ **Industry alignment** - follows patterns used by Google, LinkedIn, Netflix, AWS

### Trade-offs

* **More code** - adapter/mapper functions (~10-15% code overhead)
* **Manual mapping** - must write translation logic between layers
* **Potential for drift** - layers can become inconsistent without good tests
* **Learning curve** - team must understand adapter pattern and when to use it

**Mitigation:** Automated compatibility tests catch drift, adapters are simple and testable, benefits outweigh costs for long-term maintainability.

## Implementation

### Layer 1: Domain Model (Pure Business Logic)

**internal/domain/booking_log.go:**
```go
package domain

import (
    "time"
    "github.com/google/uuid"
)

// FinancialBookingLog - Pure domain model
// No persistence or serialization tags
type FinancialBookingLog struct {
    ID              uuid.UUID
    ControlRecordID string
    BookingPurpose  string
    Amount          Money          // Rich domain type
    ValueDate       time.Time
    Status          BookingStatus  // Domain enum
}

// Money represents monetary value with currency
type Money struct {
    AmountCents int64
    Currency    Currency
}

type Currency string

const (
    CurrencyGBP Currency = "GBP"
    CurrencyUSD Currency = "USD"
    CurrencyEUR Currency = "EUR"
)

type BookingStatus string

const (
    BookingStatusPending BookingStatus = "pending"
    BookingStatusPosted  BookingStatus = "posted"
    BookingStatusFailed  BookingStatus = "failed"
)

// Domain behavior
func (b *FinancialBookingLog) Post() error {
    if b.Status != BookingStatusPending {
        return ErrInvalidStatusTransition
    }
    b.Status = BookingStatusPosted
    return nil
}

func (b *FinancialBookingLog) Validate() error {
    if b.Amount.AmountCents <= 0 {
        return ErrInvalidAmount
    }
    if b.ControlRecordID == "" {
        return ErrMissingControlRecord
    }
    return nil
}
```

### Layer 2: Persistence Adapter (Database Entity)

**internal/adapters/persistence/booking_log_entity.go:**
```go
package persistence

import (
    "time"
    "github.com/google/uuid"
)

// BookingLogEntity - Database persistence model
// Optimized for database concerns: audit fields, indexes, constraints
type BookingLogEntity struct {
    // Primary key
    ID              uuid.UUID  `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`

    // Business fields (flattened from domain)
    ControlRecordID string     `gorm:"uniqueIndex;not null;size:255"`
    BookingPurpose  string     `gorm:"not null;size:500"`
    AmountCents     int64      `gorm:"not null"`
    Currency        string     `gorm:"not null;size:3;index"`
    ValueDate       time.Time  `gorm:"not null;index"`
    Status          string     `gorm:"not null;size:50;index"`

    // Audit fields (NOT in domain model)
    CreatedAt       time.Time  `gorm:"not null;default:now()"`
    UpdatedAt       time.Time  `gorm:"not null;default:now()"`
    CreatedBy       string     `gorm:"size:255"`
    UpdatedBy       string     `gorm:"size:255"`

    // Optimistic locking
    Version         int        `gorm:"not null;default:1"`

    // Soft delete
    DeletedAt       *time.Time `gorm:"index"`
}

func (BookingLogEntity) TableName() string {
    return "financial_booking_logs"
}
```

**Generate Migration with Atlas:**
```bash
atlas migrate diff add_booking_log \
  --env gorm \
  --to "gorm://internal/adapters/persistence"
```

**Generated SQL (migrations/20250125120000_add_booking_log.sql):**
```sql
CREATE TABLE "financial_booking_logs" (
  "id" uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  "control_record_id" text NOT NULL UNIQUE,
  "booking_purpose" text NOT NULL,
  "amount_cents" bigint NOT NULL,
  "currency" varchar(3) NOT NULL,
  "value_date" timestamptz NOT NULL,
  "status" varchar(50) NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  "created_by" varchar(255),
  "updated_by" varchar(255),
  "version" integer NOT NULL DEFAULT 1,
  "deleted_at" timestamptz
);

CREATE INDEX "idx_financial_booking_logs_currency" ON "financial_booking_logs" ("currency");
CREATE INDEX "idx_financial_booking_logs_value_date" ON "financial_booking_logs" ("value_date");
CREATE INDEX "idx_financial_booking_logs_status" ON "financial_booking_logs" ("status");
CREATE INDEX "idx_financial_booking_logs_deleted_at" ON "financial_booking_logs" ("deleted_at");
```

**Persistence Adapter (Repository):**
```go
// internal/adapters/persistence/booking_log_repository.go
package persistence

import (
    "context"
    "github.com/bjcoombs/meridian/internal/domain"
    "gorm.io/gorm"
)

type BookingLogRepository struct {
    db *gorm.DB
}

func NewBookingLogRepository(db *gorm.DB) *BookingLogRepository {
    return &BookingLogRepository{db: db}
}

// Save - Domain to database
func (r *BookingLogRepository) Save(ctx context.Context, booking *domain.FinancialBookingLog) error {
    entity := r.toEntity(booking)

    // Get user from context for audit
    if userID, ok := ctx.Value("user_id").(string); ok {
        if entity.CreatedAt.IsZero() {
            entity.CreatedBy = userID
        }
        entity.UpdatedBy = userID
    }

    return r.db.WithContext(ctx).Save(entity).Error
}

// FindByID - Database to domain
func (r *BookingLogRepository) FindByID(ctx context.Context, id uuid.UUID) (*domain.FinancialBookingLog, error) {
    var entity BookingLogEntity
    err := r.db.WithContext(ctx).First(&entity, id).Error
    if err != nil {
        return nil, err
    }
    return r.toDomain(&entity), nil
}

// Adapter: Domain → Entity
func (r *BookingLogRepository) toEntity(d *domain.FinancialBookingLog) *BookingLogEntity {
    return &BookingLogEntity{
        ID:              d.ID,
        ControlRecordID: d.ControlRecordID,
        BookingPurpose:  d.BookingPurpose,
        AmountCents:     d.Amount.AmountCents,
        Currency:        string(d.Amount.Currency),
        ValueDate:       d.ValueDate,
        Status:          string(d.Status),
    }
}

// Adapter: Entity → Domain
func (r *BookingLogRepository) toDomain(e *BookingLogEntity) *domain.FinancialBookingLog {
    return &domain.FinancialBookingLog{
        ID:              e.ID,
        ControlRecordID: e.ControlRecordID,
        BookingPurpose:  e.BookingPurpose,
        Amount: domain.Money{
            AmountCents: e.AmountCents,
            Currency:    domain.Currency(e.Currency),
        },
        ValueDate: e.ValueDate,
        Status:    domain.BookingStatus(e.Status),
    }
}
```

### Layer 3: Event Adapter (Kafka Protobuf)

**api/proto/events/financial_accounting/v1/events.proto** (manually maintained):

```protobuf
syntax = "proto3";

package meridian.events.financial_accounting.v1;

import "google/protobuf/timestamp.proto";

// Event envelope with metadata (NOT in domain or database)
message FinancialBookingLogCreated {
  // Event metadata
  string event_id = 1;
  google.protobuf.Timestamp occurred_at = 2;
  string correlation_id = 3;
  string causation_id = 4;

  // Domain data (selective - only what consumers need)
  string id = 5;
  string control_record_id = 6;
  string booking_purpose = 7;
  int64 amount_cents = 8;
  string currency = 9;
  google.protobuf.Timestamp value_date = 10;
  string status = 11;
}
```

**Compile and Register:**
```bash
# Compile Protobuf
buf generate

# Register with Schema Registry (CI/CD)
curl -X POST http://schema-registry:8081/subjects/booking-log-created-value/versions \
  -H "Content-Type: application/vnd.schemaregistry.v1+json" \
  -d @- <<EOF
{
  "schemaType": "PROTOBUF",
  "schema": "$(cat api/proto/events/financial_accounting/v1/events.proto | base64)"
}
EOF
```

**Event Adapter (Publisher):**
```go
// internal/adapters/events/booking_log_publisher.go
package events

import (
    "context"
    "github.com/bjcoombs/meridian/internal/domain"
    eventspb "github.com/bjcoombs/meridian/api/proto/events/financial_accounting/v1"
    "github.com/google/uuid"
    "google.golang.org/protobuf/types/known/timestamppb"
)

type BookingLogPublisher struct {
    producer KafkaProducer
}

func NewBookingLogPublisher(producer KafkaProducer) *BookingLogPublisher {
    return &BookingLogPublisher{producer: producer}
}

// PublishCreated - Domain to event
func (p *BookingLogPublisher) PublishCreated(ctx context.Context, booking *domain.FinancialBookingLog) error {
    event := p.toCreatedEvent(ctx, booking)
    return p.producer.Publish(ctx, "booking-log-created", event)
}

// Adapter: Domain → Event
func (p *BookingLogPublisher) toCreatedEvent(ctx context.Context, d *domain.FinancialBookingLog) *eventspb.FinancialBookingLogCreated {
    // Extract correlation ID from context
    correlationID, _ := ctx.Value("correlation_id").(string)
    causationID, _ := ctx.Value("causation_id").(string)

    return &eventspb.FinancialBookingLogCreated{
        // Event metadata
        EventId:       uuid.New().String(),
        OccurredAt:    timestamppb.Now(),
        CorrelationId: correlationID,
        CausationId:   causationID,

        // Domain data
        Id:              d.ID.String(),
        ControlRecordId: d.ControlRecordID,
        BookingPurpose:  d.BookingPurpose,
        AmountCents:     d.Amount.AmountCents,
        Currency:        string(d.Amount.Currency),
        ValueDate:       timestamppb.New(d.ValueDate),
        Status:          string(d.Status),
    }
}
```

### Automated Compatibility Testing

**Test that adapters don't lose data:**

```go
// internal/adapters/compatibility_test.go
package adapters_test

import (
    "testing"
    "github.com/bjcoombs/meridian/internal/domain"
    "github.com/bjcoombs/meridian/internal/adapters/persistence"
    "github.com/stretchr/testify/assert"
    "github.com/google/uuid"
    "time"
)

func TestPersistenceAdapter_Roundtrip(t *testing.T) {
    // Original domain model
    original := &domain.FinancialBookingLog{
        ID:              uuid.New(),
        ControlRecordID: "CR-001",
        BookingPurpose:  "Customer payment",
        Amount: domain.Money{
            AmountCents: 10050,  // £100.50
            Currency:    domain.CurrencyGBP,
        },
        ValueDate: time.Now(),
        Status:    domain.BookingStatusPending,
    }

    // Domain → Entity → Domain
    repo := persistence.NewBookingLogRepository(nil)
    entity := repo.ToEntity(original)  // Make method public for testing
    restored := repo.ToDomain(entity)

    // Verify no data loss
    assert.Equal(t, original.ID, restored.ID)
    assert.Equal(t, original.ControlRecordID, restored.ControlRecordID)
    assert.Equal(t, original.Amount.AmountCents, restored.Amount.AmountCents)
    assert.Equal(t, original.Amount.Currency, restored.Amount.Currency)
    assert.Equal(t, original.Status, restored.Status)
}

func TestEventAdapter_ContainsAllRequiredFields(t *testing.T) {
    booking := &domain.FinancialBookingLog{
        ID:              uuid.New(),
        ControlRecordID: "CR-001",
        BookingPurpose:  "Test",
        Amount: domain.Money{
            AmountCents: 1000,
            Currency:    domain.CurrencyGBP,
        },
        ValueDate: time.Now(),
        Status:    domain.BookingStatusPending,
    }

    publisher := events.NewBookingLogPublisher(nil)
    event := publisher.ToCreatedEvent(context.Background(), booking)

    // Verify event has all required fields
    assert.NotEmpty(t, event.EventId)
    assert.NotNil(t, event.OccurredAt)
    assert.Equal(t, booking.ID.String(), event.Id)
    assert.Equal(t, booking.ControlRecordID, event.ControlRecordId)
    assert.Equal(t, booking.Amount.AmountCents, event.AmountCents)
}

func TestAdapterFieldCoverage(t *testing.T) {
    // Ensure no fields are forgotten in mappings
    domainType := reflect.TypeOf(domain.FinancialBookingLog{})
    entityType := reflect.TypeOf(persistence.BookingLogEntity{})

    domainFields := getDomainFields(domainType)
    entityFields := getEntityFields(entityType)

    // Entity should have all domain fields plus audit fields
    for _, field := range domainFields {
        assert.Contains(t, entityFields, field,
            "Entity missing domain field: %s", field)
    }
}
```

## Workflow

### Scenario 1: Adding Business Logic Field

**Example:** Add `narrative` field to booking logs

**1. Update domain model:**
```go
// internal/domain/booking_log.go
type FinancialBookingLog struct {
    // ... existing fields
    Narrative string  // New domain field
}
```

**2. Update persistence entity:**
```go
// internal/adapters/persistence/booking_log_entity.go
type BookingLogEntity struct {
    // ... existing fields
    Narrative string `gorm:"type:text"`  // Add to database
}

// Update adapters
func (r *BookingLogRepository) toEntity(d *domain.FinancialBookingLog) *BookingLogEntity {
    return &BookingLogEntity{
        // ... existing mappings
        Narrative: d.Narrative,  // Add mapping
    }
}
```

**3. Generate database migration:**
```bash
atlas migrate diff add_narrative \
  --env gorm \
  --to "gorm://internal/adapters/persistence"
```

**4. Update event schema:**
```protobuf
// api/proto/events/financial_accounting/v1/events.proto
message FinancialBookingLogCreated {
  // ... existing fields
  string narrative = 12;  // Add to events
}
```

**5. Update event adapter:**
```go
// internal/adapters/events/booking_log_publisher.go
func (p *BookingLogPublisher) toCreatedEvent(...) *eventspb.FinancialBookingLogCreated {
    return &eventspb.FinancialBookingLogCreated{
        // ... existing mappings
        Narrative: d.Narrative,  // Add mapping
    }
}
```

**6. Run tests:**
```bash
go test ./internal/adapters/...  # Verify adapters work
make proto                       # Compile protobuf
atlas migrate lint --env gorm   # Validate migration
```

### Scenario 2: Adding Database-Only Field (Audit)

**Example:** Add `last_accessed_at` audit timestamp

**1. Only update persistence entity (skip domain):**
```go
// internal/adapters/persistence/booking_log_entity.go
type BookingLogEntity struct {
    // ... existing fields
    LastAccessedAt *time.Time `gorm:"index"`  // Database only
}
```

**2. Generate migration:**
```bash
atlas migrate diff add_last_accessed --env gorm
```

**3. Update repository to populate field:**
```go
func (r *BookingLogRepository) FindByID(...) {
    // ... load entity
    entity.LastAccessedAt = timePtr(time.Now())  // Update audit field
    r.db.Save(entity)
    // ... convert to domain (audit field not mapped)
}
```

**Domain model unchanged - audit concerns stay in persistence layer!**

### Scenario 3: Adding Event-Only Field (Metadata)

**Example:** Add `producer_version` to events

**1. Only update event schema (skip domain/database):**
```protobuf
message FinancialBookingLogCreated {
    // ... existing fields
    string producer_version = 13;  // Event metadata
}
```

**2. Update event adapter:**
```go
func (p *BookingLogPublisher) toCreatedEvent(...) {
    return &eventspb.FinancialBookingLogCreated{
        // ... existing mappings
        ProducerVersion: "v1.2.0",  // Add metadata
    }
}
```

**Domain and database unchanged - event metadata stays in event layer!**

## Independent Versioning

### Database Versioning
- Linear migration history (append-only)
- Atlas tracks checksums and applied migrations
- Can add audit fields without changing domain or events
- Example: Add `last_modified_by` column → no domain impact

### Event Versioning
- Protobuf schema evolution via Schema Registry
- Multiple event versions can coexist (v1, v2 consumers)
- Backward/forward compatibility modes
- Example: Add `correlation_id` field → old consumers ignore it

### Domain Versioning
- Evolves independently based on business needs
- Not tied to API or database versions
- Rich domain types don't leak to infrastructure
- Example: Change `Money` from float to int64 → adapters handle conversion

### API Versioning
- gRPC services can have v1, v2, v3 simultaneously
- Each version maps to same domain model via adapters
- Breaking API changes don't force domain rewrites
- Example: API v2 adds pagination → domain model unchanged

**Key Insight:** With separated concerns, you can:
- Add database indexes without changing code
- Version APIs without domain changes
- Evolve events independently of persistence
- Keep domain focused on business logic

## CI/CD Integration

### Adapter Compatibility Testing

**.github/workflows/adapter-tests.yml:**
```yaml
name: Adapter Compatibility Tests

on:
  pull_request:
    paths:
      - 'internal/domain/**'
      - 'internal/adapters/**'
      - 'api/proto/**'

jobs:
  test-adapters:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3

      - name: Setup Go
        uses: actions/setup-go@v4
        with:
          go-version: '1.23'

      - name: Run adapter compatibility tests
        run: |
          go test -v ./internal/adapters/... \
            -cover -coverprofile=coverage.out

      - name: Check coverage
        run: |
          coverage=$(go tool cover -func=coverage.out | grep total | awk '{print $3}' | sed 's/%//')
          if (( $(echo "$coverage < 80" | bc -l) )); then
            echo "Adapter coverage below 80%: $coverage%"
            exit 1
          fi

      - name: Lint database migrations
        run: |
          atlas migrate lint --env gorm --latest 1

      - name: Compile Protobuf schemas
        run: |
          buf lint
          buf generate

      - name: Validate schema compatibility
        run: |
          # Check Schema Registry compatibility (if available)
          buf breaking --against '.git#branch=main'
```

## Comparison with Alternatives

### Initial Approach: Unified Schema (Go Structs with Tags)

**Considered but rejected:**
```go
type BookingLog struct {
    ID uuid.UUID `gorm:"primaryKey" proto:"id,1"`
    CreatedBy string `gorm:"size:255" proto:"-"` // Can't hide from proto
    CorrelationID string `gorm:"-" proto:"correlation_id,2"` // Can't hide from DB
}
```

**Problems:**
* ❌ Database audit fields pollute domain model
* ❌ Event metadata (correlation IDs) clutter business logic
* ❌ API versioning forces domain changes
* ❌ Can't optimize each layer independently
* ❌ Tight coupling between infrastructure and domain

### Chosen Approach: Separated Concerns with Adapters

**Three distinct representations:**
* ✅ Domain: Pure business logic, rich types, domain methods
* ✅ Persistence: Audit fields, indexes, database optimizations
* ✅ Events: Event metadata, versioning, serialization concerns

**Benefits:**
* ✅ Each layer evolves independently
* ✅ Database can add audit columns without domain changes
* ✅ Events can add metadata without persistence changes
* ✅ Domain stays focused on business rules
* ✅ Follows industry best practices (Google, LinkedIn, Netflix, AWS)

**Trade-off:** ~10-15% more code (adapters), but tests ensure correctness

## Links

* [ADR-0003: Database Schema Migrations with Atlas](./0003-database-schema-migrations.md)
* [ADR-0005: Adapter Pattern for Layer Translation](./0005-adapter-pattern-layer-translation.md)
* [Atlas Documentation](https://atlasgo.io/)
* [Confluent Schema Registry](https://docs.confluent.io/platform/current/schema-registry/index.html)
* [Domain-Driven Design](https://www.domainlanguage.com/ddd/)
* [Hexagonal Architecture](https://alistair.cockburn.us/hexagonal-architecture/)
* [GitHub Issue #3: Platform Services](https://github.com/bjcoombs/meridian/issues/3)

## Notes

### Industry References

This approach aligns with architectural patterns used by:
* **Google**: "Protocol buffers are for serialization, not domain modeling"
* **LinkedIn**: Separate schemas for APIs, events, and database models
* **Netflix**: Domain models with explicit mappers to DTOs and entities
* **Uber**: Learned that unified approach creates tight coupling
* **AWS**: Different schemas per layer (API, domain, storage)

See "Implementing Domain-Driven Design" (Vaughn Vernon) and "Building Microservices" (Sam Newman) for theoretical foundations.

### Maintenance Considerations

* **Adapter overhead**: ~10-15% more code, but isolated and testable
* **Team understanding**: Clear separation makes boundaries explicit
* **Testing strategy**: Compatibility tests prevent drift between layers
* **Evolution safety**: Each layer can change without affecting others
* **Long-term flexibility**: Much easier to maintain than unified approach
