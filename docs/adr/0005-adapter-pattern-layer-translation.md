---
name: adr-005-adapter-pattern-layer-translation
description: Use adapter pattern to translate between domain, persistence, and event representations
triggers:

  - Translating between domain and database models
  - Converting domain objects to Kafka events
  - Mapping gRPC requests to domain models
  - Handling layer separation

instructions: |
  Create dedicated adapter layers for translation: domain ↔ persistence, domain ↔ events,
  domain ↔ API. Keep business logic in domain layer only. Adapters are pure translation
  with no business rules. Use explicit mapping to prevent data loss.
---

# 5. Adapter Pattern for Layer Translation

Date: 2025-10-25

## Status

Accepted

Implements the separated concerns architecture from [ADR-0004](0004-event-schema-evolution.md).

## Context

With three distinct representations of our domain model (domain, persistence, events), we need a strategy for
translating between these layers without:

* Duplicating business logic
* Losing data during translation
* Creating tight coupling between layers
* Making versioning difficult

The **Adapter Pattern** (from Hexagonal Architecture / Ports & Adapters) provides explicit boundaries between the
domain and infrastructure concerns.

## Decision Drivers

* **Explicit boundaries** - Clear separation between domain and infrastructure
* **Testability** - Adapters can be tested independently
* **Flexibility** - Each layer can evolve independently
* **Type safety** - Compilation catches mapping errors
* **No data loss** - Round-trip testing ensures correctness
* **Domain purity** - Business logic not polluted by infrastructure tags
* **Industry alignment** - Patterns used by Google, Netflix, AWS, Uber

## Decision Outcome

Implement **explicit adapter functions** for translating between layers, with **automated compatibility testing** to
prevent drift.

### Architecture

```text
┌─────────────────────────────────────┐
│   Domain Layer                      │
│   (Pure business logic)             │
│   - No infrastructure dependencies  │
│   - Rich types                      │
│   - Domain methods                  │
└─────────────────────────────────────┘
           ↑ Port (interface)
           │
┌──────────┴────────────────────────────┐
│   Adapters (Translation Layer)        │
│   - Domain ↔ Entity                   │
│   - Domain ↔ Event                    │
│   - Domain ↔ DTO                      │
└───────────────────────────────────────┘
           ↓
┌───────────────────────────────────────┐
│   Infrastructure                      │
│   - Database (GORM entities)          │
│   - Kafka (Protobuf events)           │
│   - gRPC (API responses)              │
└───────────────────────────────────────┘
```

## Implementation

> **Path/package note (refreshed 2026):** The adapter pattern below is still the live architecture, but the code no
> longer lives under `internal/`. The current layout per service is:
>
> | Concern | Current location | Package |
> |---------|------------------|---------|
> | Domain models + ports | `services/<svc>/domain/` | `domain` |
> | Persistence adapters | `services/<svc>/adapters/persistence/` | `persistence` |
> | Event publishers | `services/<svc>/adapters/messaging/` | `messaging` |
> | gRPC handlers | `services/<svc>/service/` | `service` |
>
> The single Go module is `github.com/meridianhub/meridian`, so imports are
> `github.com/meridianhub/meridian/services/<svc>/domain`, etc. The `toEntity`/`toDomain` translators are typically
> package-level functions (e.g. `toBookingLogEntity`/`toBookingLogDomain`) rather than repository methods. Event
> publishing goes through the **transactional outbox** (`shared/platform/events`) rather than a direct
> `producer.Publish(...)`; see [ADR-0004](0004-event-schema-evolution.md). The code samples below are illustrative of
> the pattern; field lists are simplified and may not match current structs exactly.

### Repository Port (Domain Interface)

The domain defines what it needs, not how it's implemented:

```go
// services/financial-accounting/domain/booking_log_repository.go
package domain

import (
    "context"
    "github.com/google/uuid"
)

// BookingLogRepository - Port (interface) defined by domain
type BookingLogRepository interface {
    Save(ctx context.Context, booking *FinancialBookingLog) error
    FindByID(ctx context.Context, id uuid.UUID) (*FinancialBookingLog, error)
    FindByControlRecordID(ctx context.Context, recordID string) (*FinancialBookingLog, error)
}

// Domain layer depends on the interface, not the implementation
type BookingLogService struct {
    repo BookingLogRepository  // Interface, not concrete type
}

func (s *BookingLogService) CreateBooking(ctx context.Context, booking *FinancialBookingLog) error {
    // Business logic
    if err := booking.Validate(); err != nil {
        return err
    }

    // Save via interface
    return s.repo.Save(ctx, booking)
}
```

### Persistence Adapter (Infrastructure Implementation)

The adapter implements the port and handles translation:

```go
// services/financial-accounting/adapters/persistence/booking_log_repository.go
package persistence

import (
    "context"
    "github.com/meridianhub/meridian/services/financial-accounting/domain"
    "github.com/google/uuid"
    "gorm.io/gorm"
)

// BookingLogRepository - Adapter implementing domain port
type BookingLogRepository struct {
    db *gorm.DB
}

// Verify at compile-time that we implement the interface
var _ domain.BookingLogRepository = (*BookingLogRepository)(nil)

func NewBookingLogRepository(db *gorm.DB) *BookingLogRepository {
    return &BookingLogRepository{db: db}
}

// Save - Implements domain port
func (r *BookingLogRepository) Save(ctx context.Context, booking *domain.FinancialBookingLog) error {
    entity := r.toEntity(booking, ctx)
    return r.db.WithContext(ctx).Save(entity).Error
}

// FindByID - Implements domain port
func (r *BookingLogRepository) FindByID(ctx context.Context, id uuid.UUID) (*domain.FinancialBookingLog, error) {
    var entity BookingLogEntity
    err := r.db.WithContext(ctx).First(&entity, "id = ?", id).Error
    if err != nil {
        return nil, err
    }
    return r.toDomain(&entity), nil
}

// FindByControlRecordID - Implements domain port
func (r *BookingLogRepository) FindByControlRecordID(ctx context.Context, recordID string)
(*domain.FinancialBookingLog, error) {
    var entity BookingLogEntity
    err := r.db.WithContext(ctx).First(&entity, "control_record_id = ?", recordID).Error
    if err != nil {
        return nil, err
    }
    return r.toDomain(&entity), nil
}

// --- Adapter Functions (Private) ---

// toEntity: Domain → Database Entity
func (r *BookingLogRepository) toEntity(d *domain.FinancialBookingLog, ctx context.Context) *BookingLogEntity {
    entity := &BookingLogEntity{
        ID:              d.ID,
        ControlRecordID: d.ControlRecordID,
        BookingPurpose:  d.BookingPurpose,
        AmountCents:     d.Amount.AmountCents,
        Currency:        string(d.Amount.Currency),
        ValueDate:       d.ValueDate,
        Status:          string(d.Status),
    }

    // Add audit fields from context (infrastructure concern)
    if userID, ok := ctx.Value("user_id").(string); ok {
        entity.UpdatedBy = userID
        if entity.CreatedAt.IsZero() {
            entity.CreatedBy = userID
        }
    }

    return entity
}

// toDomain: Database Entity → Domain
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
    // Note: Audit fields (CreatedAt, UpdatedBy) are NOT mapped to domain
}
```

### Event Publisher Adapter

```go
// services/financial-accounting/adapters/messaging/booking_log_publisher.go
package messaging

import (
    "context"
    "github.com/meridianhub/meridian/services/financial-accounting/domain"
    eventspb "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
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

// toCreatedEvent: Domain → Kafka Event
func (p *BookingLogPublisher) toCreatedEvent(ctx context.Context, d *domain.FinancialBookingLog)
*eventspb.FinancialBookingLogCreated {
    return &eventspb.FinancialBookingLogCreated{
        // Event metadata (infrastructure concern)
        EventId:       uuid.New().String(),
        OccurredAt:    timestamppb.Now(),
        CorrelationId: getCorrelationID(ctx),
        CausationId:   getCausationID(ctx),

        // Domain data (selective - only what consumers need)
        Id:              d.ID.String(),
        ControlRecordId: d.ControlRecordID,
        BookingPurpose:  d.BookingPurpose,
        AmountCents:     d.Amount.AmountCents,
        Currency:        string(d.Amount.Currency),
        ValueDate:       timestamppb.New(d.ValueDate),
        Status:          string(d.Status),
    }
}

// Helper to extract correlation ID from context
func getCorrelationID(ctx context.Context) string {
    if id, ok := ctx.Value("correlation_id").(string); ok {
        return id
    }
    return uuid.New().String()
}

func getCausationID(ctx context.Context) string {
    if id, ok := ctx.Value("causation_id").(string); ok {
        return id
    }
    return ""
}
```

### gRPC Service Adapter

```go
// services/financial-accounting/service/booking_log_service.go
package service

import (
    "context"
    "github.com/meridianhub/meridian/services/financial-accounting/domain"
    pb "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
    "google.golang.org/protobuf/types/known/timestamppb"
)

type BookingLogServiceServer struct {
    pb.UnimplementedBookingLogServiceServer
    service *domain.BookingLogService
}

// InitiateBookingLog - gRPC handler
func (s *BookingLogServiceServer) InitiateBookingLog(
    ctx context.Context,
    req *pb.InitiateBookingLogRequest,
) (*pb.InitiateBookingLogResponse, error) {
    // Request → Domain
    booking := s.requestToDomain(req)

    // Business logic (domain layer)
    err := s.service.CreateBooking(ctx, booking)
    if err != nil {
        return nil, err
    }

    // Domain → Response
    return s.toResponse(booking), nil
}

// requestToDomain: gRPC Request → Domain
func (s *BookingLogServiceServer) requestToDomain(req *pb.InitiateBookingLogRequest) *domain.FinancialBookingLog {
    return &domain.FinancialBookingLog{
        ID:              uuid.New(),
        ControlRecordID: req.ControlRecordId,
        BookingPurpose:  req.BookingPurpose,
        Amount: domain.Money{
            AmountCents: req.AmountCents,
            Currency:    domain.Currency(req.Currency),
        },
        ValueDate: req.ValueDate.AsTime(),
        Status:    domain.BookingStatusPending,
    }
}

// toResponse: Domain → gRPC Response
func (s *BookingLogServiceServer) toResponse(d *domain.FinancialBookingLog) *pb.InitiateBookingLogResponse {
    return &pb.InitiateBookingLogResponse{
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

## BIAN Evolution Support

The adapter pattern provides critical flexibility for adopting new BIAN releases without coordinated infrastructure
changes.

### Scenario: BIAN Adds New Behavior Qualifier

#### BIAN 14.0 → 15.0 adds "Suspend" to Current Account

#### Step 1: Update domain model (business logic)

```go
// services/current-account/domain/current_account.go
// (Illustrative BIAN-evolution example. The real current-account domain models a "Frozen"
//  status rather than "Suspended"; the mechanics are identical.)

// Updated for BIAN 15.0
type CurrentAccount struct {
    ID              uuid.UUID
    ControlRecordID string
    AccountStatus   AccountStatus

    // New in BIAN 15.0
    SuspensionReason string
    SuspendedUntil   time.Time
    SuspendedBy      string
}

type AccountStatus string

const (
    AccountStatusActive    AccountStatus = "active"
    AccountStatusClosed    AccountStatus = "closed"
    AccountStatusSuspended AccountStatus = "suspended"  // New
)

// New BIAN 15.0 behavior qualifier
func (a *CurrentAccount) Suspend(reason string, until time.Time, by string) error {
    if a.AccountStatus != AccountStatusActive {
        return ErrInvalidStatusTransition
    }

    a.AccountStatus = AccountStatusSuspended
    a.SuspensionReason = reason
    a.SuspendedUntil = until
    a.SuspendedBy = by
    return nil
}
```

#### Step 2: Update persistence adapter (database mapping)

```go
// services/current-account/adapters/persistence/current_account_repository.go

// Add new fields to entity
type CurrentAccountEntity struct {
    // ... existing fields
    AccountStatus    string     `gorm:"not null;size:50;index"`

    // BIAN 15.0 fields (nullable for backward compatibility)
    SuspensionReason *string    `gorm:"size:500"`
    SuspendedUntil   *time.Time `gorm:"index"`
    SuspendedBy      *string    `gorm:"size:255"`
}

// Update adapter: Domain → Entity
func (r *CurrentAccountRepository) toEntity(d *domain.CurrentAccount) *CurrentAccountEntity {
    entity := &CurrentAccountEntity{
        // ... existing mappings
        AccountStatus: string(d.AccountStatus),
    }

    // Map BIAN 15.0 fields (conditional based on status)
    if d.AccountStatus == domain.AccountStatusSuspended {
        entity.SuspensionReason = &d.SuspensionReason
        entity.SuspendedUntil = &d.SuspendedUntil
        entity.SuspendedBy = &d.SuspendedBy
    }

    return entity
}

// Update adapter: Entity → Domain
func (r *CurrentAccountRepository) toDomain(e *CurrentAccountEntity) *domain.CurrentAccount {
    account := &domain.CurrentAccount{
        // ... existing mappings
        AccountStatus: domain.AccountStatus(e.AccountStatus),
    }

    // Map BIAN 15.0 fields if present
    if e.SuspensionReason != nil {
        account.SuspensionReason = *e.SuspensionReason
    }
    if e.SuspendedUntil != nil {
        account.SuspendedUntil = *e.SuspendedUntil
    }
    if e.SuspendedBy != nil {
        account.SuspendedBy = *e.SuspendedBy
    }

    return account
}
```

#### Step 3: Update event adapter (new event type per ADR-0004)

```go
// services/current-account/adapters/messaging/current_account_publisher.go

// New method for BIAN 15.0 behavior qualifier
func (p *CurrentAccountPublisher) PublishSuspended(
    ctx context.Context,
    account *domain.CurrentAccount,
) error {
    event := &eventspb.AccountSuspended{
        EventId:          uuid.New().String(),
        OccurredAt:       timestamppb.Now(),
        CorrelationId:    getCorrelationID(ctx),
        AccountId:        account.ID.String(),
        SuspensionReason: account.SuspensionReason,
        SuspendedUntil:   timestamppb.New(account.SuspendedUntil),
        SuspendedBy:      account.SuspendedBy,
    }

    return p.producer.Publish(ctx, "account-suspended", event)
}
```

**Key Insight:** Domain model changes once, adapters translate to infrastructure needs. Each layer evolves at its own
pace.

### Benefits for BIAN Adoption

#### 1. Independent layer evolution

```text
Domain:      BIAN 15.0 (updated immediately)
             ↓
Persistence: BIAN 14.0 schema + new nullable columns (gradual migration)
             ↓
Events:      New event type with BIAN 15.0 semantics (backward compatible)
```

#### 2. Backward compatibility

Old consumers (BIAN 14.0) continue working:

* Database: Nullable columns don't break existing queries
* Events: New event type on new topic (old consumers unaffected)
* Domain: New behavior qualifiers only used by v15 clients

#### 3. Gradual rollout

```text
Week 1: Update CurrentAccount domain (BIAN 15.0)
Week 2: Deploy database migration (add suspension columns)
Week 3: Deploy new event type (account-suspended topic)
Week 4+: Consuming services adopt BIAN 15.0 independently
```

**Without adapters:** Single BIAN upgrade would require coordinated deployment across all services, risking production
disruption.

**With adapters:** Each layer upgrades independently, minimizing risk and enabling continuous delivery.

## Testing Strategy

### 1. Round-Trip Tests (No Data Loss)

Ensure adapters don't lose data:

```go
// services/financial-accounting/adapters/persistence/booking_log_repository_test.go
package persistence_test

import (
    "context"
    "testing"
    "time"

    "github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
    "github.com/meridianhub/meridian/services/financial-accounting/domain"
    "github.com/google/uuid"
    "github.com/stretchr/testify/assert"
)

func TestBookingLogRepository_RoundTrip(t *testing.T) {
    original := &domain.FinancialBookingLog{
        ID:              uuid.New(),
        ControlRecordID: "CR-12345",
        BookingPurpose:  "Customer payment",
        Amount: domain.Money{
            AmountCents: 10050,  // £100.50
            Currency:    domain.CurrencyGBP,
        },
        ValueDate: time.Now().Truncate(time.Second),
        Status:    domain.BookingStatusPending,
    }

    repo := persistence.NewBookingLogRepository(nil)

    // Domain → Entity → Domain
    entity := repo.ToEntity(original, context.Background())  // Expose for testing
    restored := repo.ToDomain(entity)

    // Verify no data loss
    assert.Equal(t, original.ID, restored.ID)
    assert.Equal(t, original.ControlRecordID, restored.ControlRecordID)
    assert.Equal(t, original.BookingPurpose, restored.BookingPurpose)
    assert.Equal(t, original.Amount, restored.Amount)
    assert.Equal(t, original.ValueDate.Unix(), restored.ValueDate.Unix())
    assert.Equal(t, original.Status, restored.Status)
}
```

### 2. Field Coverage Tests

Ensure no fields are forgotten:

```go
func TestBookingLogAdapter_AllFieldsMapped(t *testing.T) {
    // Use reflection to verify all domain fields are mapped
    domainType := reflect.TypeOf(domain.FinancialBookingLog{})
    entityType := reflect.TypeOf(persistence.BookingLogEntity{})

    domainFields := extractFieldNames(domainType)
    entityFields := extractFieldNames(entityType)

    // Every domain field should exist in entity (plus audit fields)
    for _, field := range domainFields {
        assert.Contains(t, entityFields, field,
            "Entity missing domain field: %s", field)
    }
}

func extractFieldNames(t reflect.Type) []string {
    var names []string
    for i := 0; i < t.NumField(); i++ {
        names = append(names, t.Field(i).Name)
    }
    return names
}
```

### 3. Contract Tests (Against Real Infrastructure)

Test with actual database:

```go
func TestBookingLogRepository_Integration(t *testing.T) {
    // Setup test database
    db := setupTestDB(t)
    repo := persistence.NewBookingLogRepository(db)

    // Create domain object
    booking := &domain.FinancialBookingLog{
        ID:              uuid.New(),
        ControlRecordID: "CR-TEST",
        BookingPurpose:  "Integration test",
        Amount: domain.Money{
            AmountCents: 1000,
            Currency:    domain.CurrencyGBP,
        },
        ValueDate: time.Now(),
        Status:    domain.BookingStatusPending,
    }

    // Save and retrieve
    ctx := context.Background()
    err := repo.Save(ctx, booking)
    assert.NoError(t, err)

    retrieved, err := repo.FindByID(ctx, booking.ID)
    assert.NoError(t, err)
    assert.Equal(t, booking.ControlRecordID, retrieved.ControlRecordID)
}
```

### 4. Mutation Tests

Ensure immutability:

```go
func TestBookingLogAdapter_ImmutableDomain(t *testing.T) {
    original := createTestBooking()
    entity := repo.ToEntity(original, context.Background())

    // Mutate entity
    entity.Status = "posted"

    // Original should be unchanged
    assert.Equal(t, domain.BookingStatusPending, original.Status)
}
```

## Best Practices

### DO

✅ **Keep adapters thin** - Only translation logic, no business rules
✅ **Test round-trips** - Ensure no data loss during translation
✅ **Use interfaces** - Domain defines ports, adapters implement them
✅ **Handle context data** - Extract correlation IDs, user IDs from context in adapters
✅ **Document mapping decisions** - Comment why certain fields are not mapped
✅ **Use compile-time checks** - `var _ DomainInterface = (*Adapter)(nil)`

### DON'T

❌ **Don't put business logic in adapters** - That belongs in domain
❌ **Don't make adapters bidirectional** - Separate `toEntity` and `toDomain`
❌ **Don't share types across layers** - Each layer has its own types
❌ **Don't map everything** - Audit fields stay in persistence, event metadata stays in events
❌ **Don't mutate inputs** - Create new objects instead

## When to Use This Pattern

### Use adapters when

* Translating between domain and infrastructure (database, messaging, API)
* Different layers have different concerns (audit fields, event metadata)
* Versioning requirements differ per layer
* You need testable boundaries

### Don't use adapters when

* Layers have identical structure (consider if separation is needed)
* Performance is critical and zero-copy is required (rare)
* Simple CRUD with no business logic (though still beneficial for future flexibility)

## Comparison with Alternatives

### Alternative 1: Direct Mapping (No Adapters)

```go
// Anti-pattern: Domain model IS the database entity
type BookingLog struct {
    ID        uuid.UUID `gorm:"primaryKey" json:"id" proto:"id,1"`
    CreatedBy string    `gorm:"size:255" json:"-" proto:"-"`  // Leaks into domain
}
```

**Problems:**

* ❌ Infrastructure concerns leak into domain
* ❌ Can't version layers independently
* ❌ Tight coupling

### Alternative 2: ORM Magic (Active Record)

```go
func (b *BookingLog) Save() error {
    return db.Save(b).Error  // Domain knows about database
}
```

**Problems:**

* ❌ Domain depends on database
* ❌ Hard to test without database
* ❌ Not CQRS-friendly

### Chosen: Adapter Pattern (Hexagonal Architecture)

**Benefits:**

* ✅ Clear boundaries
* ✅ Testable without infrastructure
* ✅ Layers evolve independently
* ✅ Industry-standard pattern

## Links

* [ADR-0003: Database Schema Migrations](./0003-database-schema-migrations.md)
* [ADR-0004: Event Schema Evolution Strategy](./0004-event-schema-evolution.md)
* [Hexagonal Architecture (Alistair Cockburn)](https://alistair.cockburn.us/hexagonal-architecture/)
* [Implementing Domain-Driven Design (Vaughn Vernon)](https://www.amazon.com/Implementing-Domain-Driven-Design-Vaughn-Vernon/dp/0321834577)
* [Clean Architecture (Robert C. Martin)](https://blog.cleancoder.com/uncle-bob/2012/08/13/the-clean-architecture.html)
* [GitHub Issue #3: Platform Services](https://github.com/meridianhub/meridian/issues/3)

## Notes

### Performance Considerations

* **Overhead:** Adapter functions add ~1-5% overhead (negligible)
* **Optimization:** If profiling shows adapter bottlenecks, consider pooling or caching
* **Bulk operations:** Batch translations for large datasets

### Team Considerations

* **Learning curve:** Developers must understand port/adapter pattern
* **Code review:** Ensure adapters stay thin and don't accumulate business logic
* **Documentation:** Comment why certain fields are not mapped between layers
* **Testing discipline:** Maintain high coverage on adapter tests (>90%)
