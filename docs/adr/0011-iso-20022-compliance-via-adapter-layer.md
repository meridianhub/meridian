---
name: adr-011-iso-20022-compliance-via-adapter-layer
description: Implement ISO 20022 compliance at external boundaries using hexagonal architecture adapters
triggers:
  - Designing external payment system integrations
  - Implementing regulatory compliance interfaces
  - Connecting to ISO 20022-compliant banking networks
  - Supporting multiple external message standards
instructions: |
  Keep internal domain models (protobuf) aligned with BIAN semantics. Implement
  ISO 20022 compliance via dedicated adapter layer that translates between internal
  domain representations and external ISO 20022 XML/JSON formats. Each external
  integration creates a new adapter implementation of the port interface. Domain
  model remains stable while external message formats are handled at boundaries.
---

# 11. ISO 20022 Compliance via Adapter Layer

Date: 2025-11-14

## Status

Accepted

## Context

Meridian implements BIAN service domains using protobuf-based internal schemas for gRPC inter-service communication.
External systems (payment networks, correspondent banks, regulatory reporting) require ISO 20022 message formats for
interoperability and compliance.

ISO 20022 is the international standard for financial messaging, mandated by many payment networks (SEPA, SWIFT,
Faster Payments) and regulatory frameworks. Supporting ISO 20022 is essential for:

- Integration with external payment networks and clearing systems
- Regulatory compliance for cross-border payments
- Interoperability with correspondent banking partners
- Future-proofing against evolving industry standards

The architectural challenge: how to support ISO 20022 external interfaces without compromising internal domain model
simplicity and BIAN alignment.

## Decision Drivers

- **Separation of Concerns**: Domain logic should not depend on external message formats
- **Hexagonal Architecture**: External protocols belong in adapter layer, not domain
- **BIAN Fidelity**: Internal schemas must faithfully represent BIAN service domain semantics
- **Multiple Standards**: Platform must support ISO 20022, SWIFT MT, and proprietary formats simultaneously
- **Domain Model Stability**: Changes to external standards should not require domain model changes
- **Regulatory Compliance**: External interfaces must conform to ISO 20022 specifications precisely
- **Maintainability**: Translation logic should be isolated and testable

## Considered Options

### Option 1: Align Internal Schemas with ISO 20022

Modify internal protobuf schemas to match ISO 20022 data structures and naming conventions.

- Good, because ensures direct mapping to external format
- Good, because reduces translation complexity at boundaries
- Bad, because couples domain model to external standard
- Bad, because ISO 20022 verbosity pollutes internal schemas
- Bad, because prevents supporting multiple external standards cleanly
- Bad, because breaks BIAN semantic alignment
- Bad, because external standard evolution forces domain changes

### Option 2: ISO 20022 Adapter Layer (Hexagonal Architecture)

Keep internal protobuf schemas BIAN-aligned, implement ISO 20022 compliance via dedicated adapter layer.

- Good, because domain model remains simple and BIAN-focused
- Good, because follows hexagonal architecture principles (ports and adapters)
- Good, because allows supporting multiple external standards independently
- Good, because isolates translation logic for testing and maintenance
- Good, because external standard changes don't impact domain
- Good, because enables parallel adapter implementations (XML, JSON, etc.)
- Bad, because requires maintaining translation mappings
- Bad, because adds one layer of indirection for external calls

### Option 3: Parallel Schema Sets

Maintain separate internal and external schema sets with no shared types.

- Good, because complete independence between internal and external
- Bad, because creates significant duplication
- Bad, because synchronization burden between schema sets
- Bad, because translation errors harder to catch at compile time
- Bad, because violates DRY principle excessively

## Decision Outcome

Chosen option: **"Option 2: ISO 20022 Adapter Layer"**, because it correctly applies hexagonal architecture to
maintain clean domain boundaries while supporting external compliance requirements.

### Architectural Pattern

```text
External Systems (ISO 20022)
        ↕
   [Adapter Layer]
   - ISO 20022 XML/JSON serialization
   - Domain ↔ ISO 20022 mapping
   - Validation against XSD schemas
        ↕
    [Port Interface]
   - Abstract payment/account operations
        ↕
  [Domain Services]
   - BIAN-aligned protobuf
   - Business logic
   - gRPC inter-service
```

**Key Principles:**

1. **Domain Integrity**: Internal protobuf schemas remain BIAN-focused, simple, and stable
2. **Adapter Responsibility**: ISO 20022 adapters handle all translation, validation, and serialization
3. **Port Abstraction**: Port interfaces define domain operations independent of external formats
4. **Multiple Implementations**: Each external standard gets its own adapter (ISO 20022, SWIFT MT, etc.)

### Implementation Strategy

### Phase 1: Core ISO 20022 Adapter Infrastructure

Create adapter framework and implement high-value message types:

1. **Payment Initiation (pain.001)** - Customer credit transfer initiation
   - Maps: `ExecuteDepositRequest` → `pain.001.001.09`
   - Validates: XSD schema compliance, IBAN checksums, BIC format
   - Handles: Currency precision, party identification, remittance info

2. **Account Reporting (camt.053)** - Bank-to-customer statement
   - Maps: `TransactionHistory` → `camt.053.001.08`
   - Generates: ISO 20022 transaction codes, booking/value dates
   - Structures: Entry details, balance reporting

3. **Payment Status (pacs.002)** - Payment status report
   - Maps: `TransactionStatus` + reason codes → `pacs.002.001.10`
   - Provides: ISO 20022 status reason taxonomy
   - Supports: Acceptance, rejection, pending states

### Phase 2: Extended Message Types

1. **Account Opening (acmt.001)** - Account opening instruction
2. **Customer Credit Transfer (pacs.008)** - FI-to-FI credit transfer

#### Adapter Module Structure

```text
adapters/
├── iso20022/
│   ├── payment-initiation/
│   │   ├── pain001_adapter.go          # Domain → pain.001 XML
│   │   ├── pain001_mapper.go           # Field mapping logic
│   │   └── pain001_validator.go        # XSD validation
│   ├── account-reporting/
│   │   ├── camt053_adapter.go
│   │   └── camt053_mapper.go
│   ├── common/
│   │   ├── party_mapper.go             # Party identification translation
│   │   ├── currency_mapper.go          # Currency code conversion
│   │   └── code_sets.go                # ISO 20022 external code sets
│   └── validation/
│       ├── xsd_validator.go
│       └── schemas/                    # ISO 20022 XSD files
└── ports/
    └── external_payments.go            # Port interface definition
```

#### Mapping Examples

```go
// Domain → ISO 20022 Currency Mapping
// Internal: Currency enum (CURRENCY_GBP)
// ISO 20022: ISO 4217 alpha-3 code ("GBP")
func mapCurrency(c commonv1.Currency) string {
    switch c {
    case commonv1.Currency_CURRENCY_GBP: return "GBP"
    case commonv1.Currency_CURRENCY_USD: return "USD"
    // ...
    }
}

// Domain → ISO 20022 Decimal Precision
// Internal: google.type.Money (units + nanos)
// ISO 20022: DecimalNumber with currency-specific precision
func mapAmount(m *money.Money) pain001.ActiveCurrencyAndAmount {
    return pain001.ActiveCurrencyAndAmount{
        Currency: m.CurrencyCode,
        Value:    formatDecimal(m.Units, m.Nanos, getPrecision(m.CurrencyCode)),
    }
}
```

### Positive Consequences

- Domain model remains clean, BIAN-aligned, and stable
- ISO 20022 compliance proven via adapter implementation
- Multiple external standards supported without domain pollution
- Translation logic isolated for testing and maintenance
- External standard evolution handled in adapter layer only
- Enables A/B testing different message format versions
- Facilitates compliance validation (adapter outputs testable against XSD)
- Clear architectural boundaries simplify onboarding and reasoning

### Negative Consequences

- Translation layer adds runtime overhead (mitigated by one-time serialization cost)
- Mapping logic must be maintained separately from domain model
- Potential for translation bugs between domain and external format
- Requires discipline to keep domain pure and resist leaking external concerns
- Documentation needed to explain domain ↔ ISO 20022 mappings

## Compliance Mapping Documentation

### Money Amount Translation

| Domain (Protobuf) | ISO 20022 | Notes |
|------------------|-----------|-------|
| `google.type.Money` | `ActiveCurrencyAndAmount` | For positive transaction amounts |
| `google.type.Money` | `ImpliedCurrencyAndAmount` | When currency is contextual |
| Currency enum | ISO 4217 alpha-3 string | CURRENCY_GBP → "GBP" |
| units + nanos | Decimal with precision | JPY: 0 decimals, EUR: 2 decimals, BHD: 3 decimals |

### Account Identification

| Domain (Protobuf) | ISO 20022 | Notes |
|------------------|-----------|-------|
| `account_identification` (IBAN string) | `AccountIdentification/IBAN` | Direct mapping |
| Internal account_id | `AccountIdentification/Other` | Proprietary scheme |
| N/A (add to adapter) | `FinancialInstitutionIdentification` | BIC for account servicer |

### Transaction Codes

| Domain (Protobuf) | ISO 20022 | Notes |
|------------------|-----------|-------|
| `PostingDirection.DEBIT` | `CreditDebitIndicator.DBIT` | Debit entry |
| `PostingDirection.CREDIT` | `CreditDebitIndicator.CRDT` | Credit entry |
| `description` (free text) | `RemittanceInformation/Unstructured` | Unstructured remittance |
| N/A (derive in adapter) | `BankTransactionCode` | Domain/Family/Subfamily structure |

### Status Codes

| Domain (Protobuf) | ISO 20022 | Notes |
|------------------|-----------|-------|
| `TRANSACTION_STATUS_PENDING` | `PDNG` | Pending |
| `TRANSACTION_STATUS_POSTED` | `ACCP` | Accepted/Posted |
| `TRANSACTION_STATUS_FAILED` | `RJCT` | Rejected |
| N/A (add reason enum) | `StatusReasonCode` | AC01, AM05, etc. |

### Party Identification

| Domain (Protobuf) | ISO 20022 | Notes |
|------------------|-----------|-------|
| `customer_id` | `Party/Identification/Other` | Internal identifier |
| N/A (add to adapter config) | `Party/Name` | Customer name from registry |
| N/A (add to adapter) | `PostalAddress` | Structured address if available |

## Testing Strategy

**Adapter Validation:**

1. **Unit Tests**: Domain model → ISO 20022 mapping correctness
2. **XSD Validation**: Generated XML validates against official ISO 20022 schemas
3. **Round-Trip Tests**: Domain → ISO 20022 → Domain preserves semantics
4. **Example Message Tests**: Official ISO 20022 test cases validate correctly
5. **Edge Cases**: Currency precision, timezone handling, character encoding

**Compliance Verification:**

- Use official ISO 20022 XSD schemas from [iso20022.org](https://www.iso20022.org/)
- Validate generated messages with ISO validation tools
- Test against external ISO 20022 validators (SWIFT MyStandards, etc.)
- Maintain test corpus of valid ISO 20022 messages from real systems

## Links

- [ISO 20022 Official Website](https://www.iso20022.org/)
- [ISO 20022 Message Definitions](https://www.iso20022.org/iso-20022-message-definitions)
- [SWIFT ISO 20022 Resources](https://www.swift.com/standards/iso-20022)
- [Related: ADR-0004 Event Schema Evolution](./0004-event-schema-evolution.md) - Schema evolution principles
- [Related: ADR-0005 Adapter Pattern Layer Translation](./0005-adapter-pattern-layer-translation.md) - Adapter pattern guidance

## Implementation Phases

### Phase 1 (5-8 story points): Core Payment Messages — PLANNED

No implementation exists. No `adapters/iso20022/` directory has been created.

- Implement pain.001 (payment initiation) adapter
- Implement camt.053 (account reporting) adapter
- Create XSD validation framework
- Document mapping patterns

### Phase 2 (3-5 points): Status and Error Handling — PLANNED

Depends on Phase 1 adapter infrastructure.

- Implement pacs.002 (payment status) adapter
- Map transaction status to ISO reason codes
- Add structured rejection reason handling

### Phase 3 (5-8 points): Account Management — PLANNED

Depends on Phase 1 adapter infrastructure.

- Implement acmt.001 (account opening) adapter
- Add party identification structures
- Support BIC/LEI code validation

### Phase 4 (Optional): Additional Standards — DEFERRED

No current demand for legacy or alternative format support. Re-evaluate when
external payment network integration becomes a priority.

- SWIFT MT adapter (legacy format support)
- Proprietary bank formats as needed
- FIX protocol for securities (future)

## Notes

### Migration Path from Current State

Current internal schemas require no changes. Adapter implementation is additive:

1. Define port interface for external payments
2. Implement ISO 20022 adapter satisfying port interface
3. Deploy adapter as sidecar or integrated service
4. Configure routing to use adapter for external calls
5. Domain services remain unchanged

### Performance Considerations

- Adapter serialization is one-time cost at system boundary
- Caching compiled XSD schemas for validation
- Streaming XML generation for large message sets
- Monitor translation overhead, optimize hot paths if needed

### Extensibility

This pattern supports future requirements cleanly:

- **New ISO 20022 messages**: Add new adapter implementation
- **SWIFT MT legacy support**: New adapter, same port interface
- **Proprietary formats**: Bank-specific adapters
- **JSON variants**: ISO 20022 JSON (emerging standard)
- **Regional standards**: NACHA, BACS, etc.

### Domain Model Evolution

Internal protobuf schemas can evolve independently:

- Adapter layer absorbs mapping differences
- External format changes don't propagate to domain
- Versioning handled per adapter (e.g., pain.001.001.03 vs .09)
- Multiple adapter versions can coexist for transition periods

### Alternative Rejected: Why Not Align Domain Model?

Aligning internal schemas with ISO 20022 was considered and rejected because:

1. **Standard Coupling**: Domain becomes coupled to external standard evolution
2. **Verbosity**: ISO 20022 schemas are extremely verbose (50+ optional fields)
3. **Multiple Standards**: Cannot align with ISO 20022, SWIFT MT, and FIX simultaneously
4. **BIAN Divergence**: Loses BIAN semantic clarity for external format compatibility
5. **Protobuf Mismatch**: ISO 20022 patterns (XML attributes, choice groups) don't map cleanly

**The adapter pattern is the correct architectural choice for this problem.**
