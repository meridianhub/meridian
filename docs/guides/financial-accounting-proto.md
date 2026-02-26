# Financial Accounting Service - Protocol Buffer Guide

**Location**: `api/proto/meridian/financial_accounting/v1/`

This guide covers the Protocol Buffer definitions for the Financial Accounting service, following the BIAN
(Banking Industry Architecture Network) Financial Accounting service domain.

## Schema Structure

### Service Definition

**File**: `financial_accounting.proto`

The FinancialAccountingService provides gRPC APIs for managing financial booking logs and ledger postings:

- `InitiateFinancialBookingLog` - Creates a new financial booking log
- `UpdateFinancialBookingLog` - Updates an existing booking log (status, accounting rules)
- `RetrieveFinancialBookingLog` - Retrieves a specific booking log by ID
- `ListFinancialBookingLogs` - Lists booking logs with optional filtering
- `CaptureLedgerPosting` - Creates a new ledger posting for a booking log
- `RetrieveLedgerPosting` - Retrieves a specific posting by ID

### Domain Entities

#### FinancialBookingLog

Represents the BIAN Financial Booking Log aggregate root. Maintains records of financial transactions and their
processing status.

Key fields:

- `id` - Unique identifier
- `financial_account_type` - Type of account (debit, credit, vostro, nostro, current, savings)
- `product_service_reference` - Financial product identifier
- `business_unit_reference` - Business unit responsible for this log
- `chart_of_accounts_rules` - Accounting rules to apply
- `base_currency` - Currency for this booking log
- `status` - Current lifecycle state (pending, posted, failed, cancelled, reversed)
- `postings` - Array of associated ledger postings

#### LedgerPosting

Represents a single posting operation in double-entry bookkeeping.

Key fields:

- `id` - Unique identifier
- `financial_booking_log_id` - Parent booking log reference
- `posting_direction` - Debit or credit
- `posting_amount` - Monetary amount (must be positive)
- `account_id` - Target account identifier
- `value_date` - Effective date for this posting
- `posting_result` - Outcome description
- `status` - Current state of this posting

### Double-Entry Bookkeeping Semantics

Individual postings are created separately (not as balanced pairs). Balance validation occurs at the service layer:

- Booking log can only transition to POSTED status when total debits equal total credits
- Service layer validates balance before posting
- Consider using batch operations for balanced posting pairs

## Event Schemas

**File**: `../events/v1/financial_accounting_events.proto`

Event schemas follow the event-sourced architecture pattern, with events published to Kafka for inter-service coordination.

### Event Lifecycle

#### Financial Booking Log Events

1. `FinancialBookingLogInitiatedEvent` - New booking log created (pending state)
2. `FinancialBookingLogUpdatedEvent` - Status or accounting rules changed
3. `FinancialBookingLogPostedEvent` - All postings balanced and posted to general ledger
4. `FinancialBookingLogClosedEvent` - Terminal state, no further modifications allowed

#### Ledger Posting Events

1. `LedgerPostingCapturedEvent` - New posting created for a booking log
2. `LedgerPostingAmendedEvent` - Posting modified before booking log is posted
3. `LedgerPostingPostedEvent` - Posting finalised
4. `LedgerPostingRejectedEvent` - Posting rejected during validation (terminal state)

#### Validation Events

1. `BalanceValidationFailedEvent` - Published when attempting to post an unbalanced booking log (debits ≠ credits)

### Event Metadata

All events include standard metadata fields:

- `correlation_id` - Links related events across services
- `causation_id` - Identifies the event or command that caused this event
- `timestamp` - When the event was created
- `version` - Aggregate version for optimistic locking

## Schema Evolution Strategy

Following ADR-0004 (Event Schema Evolution Strategy), this service uses **manual schema definition** with
**buf tooling** for validation:

### Evolution Patterns

#### Pattern 1: Add Optional Fields

Add new optional fields to existing messages. This is backwards-compatible and validated by `buf breaking`:

```protobuf
message FinancialBookingLog {
  // Existing fields...

  // New optional field - backwards compatible
  string regulatory_reference = 11;
}
```

#### Pattern 2: New Event Types for New Behaviours

New BIAN behaviour qualifiers should create new event types:

```protobuf
// New event for a new behaviour
message FinancialBookingLogSuspendedEvent {
  string booking_log_id = 1;
  string reason = 2;
  // ...
}
```

### Kafka Topic Strategy

One topic per event type for internal coordination events:

- `financial-booking-log-initiated`
- `financial-booking-log-posted`
- `financial-booking-log-closed`
- `ledger-posting-captured`
- `balance-validation-failed`

**Retention**: 7 days for internal coordination events (long enough for consumer lag recovery)

### Breaking Change Detection

Use `buf breaking --against <branch>` in CI/CD to validate schema changes:

```bash
# Check for breaking changes against main branch
buf breaking --against ../../meridian-main
```

Breaking changes include:

- Removing fields
- Changing field types
- Changing field numbers
- Removing messages
- Changing package names

## Validation

### Field Validation

All schemas use `buf.validate` for declarative field validation:

```protobuf
import "buf/validate/validate.proto";

message FinancialBookingLog {
  string id = 1 [(buf.validate.field).string = {
    min_len: 1
    max_len: 255
  }];

  google.type.Money posting_amount = 2 [
    (buf.validate.field).required = true,
    (buf.validate.field).cel = {
      id: "positive_posting_amount"
      message: "posting amount must be greater than zero"
      expression: "this.units > 0 || (this.units == 0 && this.nanos > 0)"
    }
  ];
}
```

### Money Field Validation Limitations

**Important**: buf.validate currently does not generate runtime validation code for CEL constraints on
`google.type.Money` fields. The CEL constraints in the proto file serve as documentation of validation
requirements, but enforcement must happen at the service/application layer.

Money field validation rules:

- `LedgerPostingCapturedEvent.posting_amount` - Must be positive (units > 0 or nanos > 0)
- `LedgerPostingAmendedEvent.previous_amount` - Must be positive
- `LedgerPostingAmendedEvent.new_amount` - Must be positive
- `FinancialBookingLogPostedEvent.total_debits` - Must be non-negative (>= 0)
- `FinancialBookingLogPostedEvent.total_credits` - Must be non-negative (>= 0)
- `BalanceValidationFailedEvent.total_debits` - Must be non-negative (>= 0)
- `BalanceValidationFailedEvent.total_credits` - Must be non-negative (>= 0)
- `BalanceValidationFailedEvent.variance` - Can be negative (it's the difference)

Service layer must validate these constraints before accepting events.

### Schema Validation

Run `buf lint` to validate schema style and correctness:

```bash
buf lint
```

## Code Generation

Schemas are compiled to Go code using `buf generate`:

```bash
buf generate
```

Generated files:

- `*.pb.go` - Protocol buffer Go code
- `*_grpc.pb.go` - gRPC service stubs
- `*.pb.validate.go` - Field validation code

## Testing

Event serialisation tests are located in `../events/v1/financial_accounting_events_test.go`.

Run tests:

```bash
go test ./api/proto/meridian/events/v1/... -run TestFinancial
```

Tests verify:

- Event marshaling/unmarshaling
- Field preservation across serialisation
- Type correctness
- Validation rules

## Integration with Domain Layer

### Adapter Pattern (ADR-0005)

Use adapter classes to translate between:

1. **Domain Layer** - Pure business logic with domain entities
2. **API Layer** - Protocol buffer messages for external communication
3. **Persistence Layer** - Database entities

Example:

```go
// Domain -> Protobuf
func ToProtoFinancialBookingLog(domain *domain.FinancialBookingLog) *financialaccountingv1.FinancialBookingLog {
    return &financialaccountingv1.FinancialBookingLog{
        Id: domain.ID,
        FinancialAccountType: toProtoAccountType(domain.AccountType),
        // ... map other fields
    }
}

// Protobuf -> Domain
func FromProtoFinancialBookingLog(proto *financialaccountingv1.FinancialBookingLog) *domain.FinancialBookingLog {
    return &domain.FinancialBookingLog{
        ID: proto.Id,
        AccountType: fromProtoAccountType(proto.FinancialAccountType),
        // ... map other fields
    }
}
```

## References

- [ADR-0004: Event Schema Evolution Strategy](../../../../docs/adr/0004-event-schema-evolution.md)
- [ADR-0005: Adapter Pattern for Layer Translation](../../../../docs/adr/0005-adapter-pattern-layer-translation.md)
- [BIAN Financial Accounting Service Domain](https://bian.org/servicedomain/financialaccounting/)
- [Protocol Buffers Documentation](https://protobuf.dev/)
- [buf Documentation](https://buf.build/docs/)
- [buf.validate Documentation](https://buf.build/bufbuild/protovalidate)
