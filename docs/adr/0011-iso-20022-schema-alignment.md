---
name: adr-011-iso-20022-schema-alignment
description: Align protobuf schemas with ISO 20022 message structures and naming conventions
triggers:
  - Implementing payment message structures
  - Designing financial transaction schemas
  - Ensuring regulatory compliance with ISO 20022
  - Mapping BIAN service domains to international standards
instructions: |
  Use ISO 20022 naming conventions and data structures for financial messages.
  Key elements: Amount types require currency and decimal precision; account
  identification supports IBAN format; transaction codes align with ISO 20022
  External Code Sets; party identification includes BIC/SWIFT codes; timestamps
  use ISO 8601 format; message headers include correlation IDs and business
  message identifiers.
---

# 11. ISO 20022 Schema Alignment Audit

Date: 2025-11-14

## Status

Accepted

## Context

Meridian is building a BIAN-compliant banking platform with protobuf-based service definitions. ISO 20022 is the international standard for electronic data interchange between financial institutions, defining message formats, business processes, and data dictionaries. Alignment with ISO 20022 ensures interoperability with external systems, regulatory compliance, and adherence to industry best practices.

This ADR documents an audit of current protobuf schemas against ISO 20022 message structures and field naming conventions to identify gaps and guide future enhancements.

## Audit Scope

The following protobuf schemas were analyzed:

1. `api/proto/meridian/common/v1/types.proto` - Common types and enums
2. `api/proto/meridian/current_account/v1/current_account.proto` - Current account facility
3. `api/proto/meridian/financial_accounting/v1/financial_accounting.proto` - Financial booking logs
4. `api/proto/meridian/position_keeping/v1/position_keeping.proto` - Transaction position tracking

## Key Findings

### 1. Money Amount Representation

**Current State:**
- Uses `google.type.Money` wrapped in `MoneyAmount` message
- Supports currency code and amount with units/nanos
- Allows negative amounts for balances

**ISO 20022 Standard:**
- Defines `ActiveCurrencyAndAmount` for transaction amounts (strictly positive)
- Defines `ImpliedCurrencyAndAmount` for amounts where currency is context-dependent
- Requires explicit decimal places (typically 2-5 depending on currency)
- Uses `CurrencyCode` (ISO 4217 alpha-3 codes like "GBP", "USD", "EUR")

**Gap:**
- Current enum-based `Currency` type (types.proto:54-72) uses integer values instead of ISO 4217 alpha-3 string codes
- Missing explicit decimal precision tracking
- No distinction between active/implied currency amounts
- Limited currency support (only 7 major currencies)

**Recommendation:**
Enhance `MoneyAmount` to support:
- ISO 4217 alpha-3 currency codes as strings
- Explicit decimal precision field
- Validation rules for currency-specific precision (e.g., JPY has 0 decimal places, BHD has 3)
- Separate types for positive-only transaction amounts vs. signed balances

### 2. Account Identification

**Current State:**
- `CurrentAccountFacility.account_identification` field with IBAN validation pattern
- Pattern validates basic IBAN structure: `^[A-Z]{2}[0-9]{2}[A-Z0-9]{1,30}$`
- Max length 34 characters (IBAN standard)

**ISO 20022 Standard:**
- `AccountIdentification` complex type supporting multiple schemes:
  - IBAN (International Bank Account Number)
  - BBAN (Basic Bank Account Number)
  - UPIC (Universal Payment Identification Code)
  - Other proprietary schemes
- Includes optional account name, currency, and account servicer (BIC)

**Gap:**
- Current implementation only supports IBAN format
- No support for alternative account identification schemes
- Missing account servicer identification (BIC/SWIFT codes)
- No structured party information (account owner details)

**Recommendation:**
Create structured `AccountIdentification` message supporting:
- Multiple identification schemes with discriminator
- Optional BIC/SWIFT code for account servicer
- Account owner party information
- Account currency and type metadata

### 3. Transaction Codes and Purpose

**Current State:**
- Basic transaction direction: `PostingDirection` enum (DEBIT/CREDIT)
- Transaction status lifecycle: `TransactionStatus` enum
- Free-text `description` and `reference` fields
- No structured transaction type or purpose codes

**ISO 20022 Standard:**
- `ExternalCodeSets` for transaction type codes:
  - `PaymentPurpose` (SALA for salary, PENS for pension, etc.)
  - `CategoryPurpose` (CASH, TRAD, SECU, etc.)
  - `ExternalPaymentTransactionCode` (domain, family, subfamily structure)
- Structured transaction references (end-to-end ID, transaction ID, mandate reference)
- Remittance information (structured vs. unstructured)

**Gap:**
- No transaction type classification system
- Missing payment purpose codes
- Lack of structured remittance information
- Single generic reference field instead of multiple reference types

**Recommendation:**
Introduce structured transaction codes:
- Payment purpose code (ISO 20022 external code set)
- Transaction category code
- Domain/family/subfamily code structure
- Multiple reference fields (end-to-end ID, instruction ID, mandate reference)
- Structured remittance information with creditor/debtor reference support

### 4. Party Identification

**Current State:**
- Simple string `customer_id` field in current account creation
- No structured party information
- Missing counterparty details in transactions

**ISO 20022 Standard:**
- `Party` complex type with:
  - Name (organization or person)
  - Postal address (structured)
  - Identification (BIC, LEI, national ID, proprietary)
  - Contact details
- Separate debtor/creditor party structures
- Agent identification (intermediary banks)

**Gap:**
- No structured party data model
- Missing debtor/creditor party information in transactions
- No support for BIC/LEI codes
- Lack of postal address structures
- No intermediary agent identification

**Recommendation:**
Create comprehensive `Party` message including:
- Name (organization name or person name with structured fields)
- Identification (BIC, LEI, national ID, tax ID, proprietary schemes)
- Postal address (street, city, postal code, country ISO code)
- Contact details (email, phone)
- Apply to debtor, creditor, and agent roles in transactions

### 5. Date and Time Handling

**Current State:**
- Uses `google.protobuf.Timestamp` for all temporal data
- `DateRange` uses string dates with YYYY-MM-DD pattern validation

**ISO 20022 Standard:**
- `ISODateTime` for precise timestamps with timezone
- `ISODate` for date-only fields (value date, settlement date)
- `ISOTime` for time-only fields
- Clear distinction between booking date and value date

**Gap:**
- Mixed usage of timestamp vs. date strings
- No explicit value date tracking in many transactions
- Missing booking date vs. value date distinction

**Recommendation:**
Standardize temporal fields:
- Use timestamp for audit trails (created_at, updated_at)
- Use YYYY-MM-DD string dates for value dates and booking dates
- Add explicit `booking_date` and `value_date` to all financial postings
- Document timezone handling (recommend UTC for storage, local time for display)

### 6. Message Headers and Correlation

**Current State:**
- `IdempotencyKey` for exactly-once processing
- Transaction IDs and account IDs
- No message-level headers or correlation

**ISO 20022 Standard:**
- `BusinessApplicationHeader` (BAH) for message routing:
  - Business message identifier (unique per message)
  - Message definition identifier (message type)
  - Creation date/time
  - Copy duplicate indicator
  - Possible duplicate flag
- `GroupHeader` in payment messages:
  - Message identification
  - Creation date/time
  - Number of transactions
  - Control sum (total of all amounts)

**Gap:**
- No standardized message header structure
- Missing message correlation across services
- No batch/group header for multiple transactions
- Lack of duplicate detection beyond idempotency

**Recommendation:**
Introduce message headers:
- `MessageHeader` for all service requests (message ID, creation timestamp, originating system)
- `BatchHeader` for bulk operations (batch ID, total count, control sum)
- Correlation ID for request/response tracking across services
- Reference original message ID in responses

### 7. Status Codes and Error Handling

**Current State:**
- Basic `TransactionStatus` enum (PENDING, POSTED, FAILED, CANCELLED, REVERSED)
- Free-text error fields
- No standardized error codes

**ISO 20022 Standard:**
- `StatusReasonCode` external code sets
- `TransactionStatusReason` for detailed status information
- Structured error reporting with:
  - Status code (ACCP for accepted, RJCT for rejected)
  - Reason code (AC01 for incorrect account, etc.)
  - Additional information (free text)

**Gap:**
- Limited status granularity
- No structured reason codes
- Missing distinction between business validation failures and technical errors
- No standardized rejection reason taxonomy

**Recommendation:**
Enhance status tracking:
- Add `status_reason_code` enum based on ISO 20022 external code sets
- Add `rejection_reason` message with structured fields
- Document status transition rules
- Align with ISO 20022 payment status lifecycle

### 8. Financial Instrument and Security Information

**Current State:**
- No financial instrument representation
- Missing security identification codes

**ISO 20022 Standard:**
- `FinancialInstrument` identification:
  - ISIN (International Securities Identification Number)
  - SEDOL, CUSIP, Bloomberg ticker
  - Name and classification
- Price and quantity representations
- Corporate action codes

**Gap:**
- Not applicable to current scope (current accounts and basic transactions)
- Future consideration for investment services

**Recommendation:**
Defer until investment/securities services are in scope. Document for future reference.

## Decision Drivers

* **Regulatory Compliance**: Many jurisdictions mandate ISO 20022 for payment messages
* **Interoperability**: External banks and payment networks use ISO 20022
* **Industry Best Practices**: ISO 20022 represents decades of financial messaging evolution
* **Data Quality**: Structured codes and validations improve data consistency
* **BIAN Alignment**: BIAN service domains reference ISO 20022 data structures
* **Future-Proofing**: Global migration to ISO 20022 for SWIFT messages (completed 2023)

## Considered Options

### Option 1: Full ISO 20022 Schema Adoption (XSD to Protobuf)

Directly translate ISO 20022 XSD schemas to protobuf equivalents.

* Good, because ensures 100% compliance with standard
* Good, because simplifies mapping to external systems
* Bad, because ISO 20022 schemas are extremely verbose and complex
* Bad, because protobuf has different design patterns (no XML attributes, different extension mechanisms)
* Bad, because BIAN model already abstracts some ISO 20022 details

### Option 2: Selective ISO 20022 Alignment (Pragmatic Approach)

Adopt ISO 20022 concepts where they align with BIAN models, using protobuf-idiomatic designs.

* Good, because balances compliance with practical implementation
* Good, because maintains protobuf best practices (simple hierarchies, clear field semantics)
* Good, because allows incremental adoption
* Good, because focuses on interoperability points (account IDs, transaction codes, amounts)
* Bad, because requires careful mapping documentation
* Bad, because may miss some edge cases in standard

### Option 3: Parallel ISO 20022 Adapter Layer

Keep internal schemas as-is, build separate ISO 20022 message adapters for external integration.

* Good, because separates internal domain models from external message formats
* Good, because allows internal evolution without breaking external contracts
* Good, because follows hexagonal architecture (ports and adapters)
* Bad, because requires maintaining two schema sets
* Bad, because adds translation complexity and potential bugs
* Bad, because loses opportunity to standardize internal data quality

## Decision Outcome

Chosen option: "Option 2: Selective ISO 20022 Alignment", because it provides the best balance of compliance, pragmatism, and protobuf best practices.

### Implementation Strategy

**Phase 1 - Core Data Types** (Highest Priority):
1. Enhance `MoneyAmount` with ISO 4217 alpha-3 currency codes and decimal precision
2. Create structured `AccountIdentification` supporting IBAN and BIC
3. Implement ISO 20022 transaction code taxonomy
4. Standardize date/time handling (value date vs. booking date)

**Phase 2 - Party and Reference Data**:
5. Implement `Party` identification structures (BIC, LEI, postal address)
6. Add structured transaction references (end-to-end ID, instruction ID)
7. Create message headers for correlation and duplicate detection

**Phase 3 - Status and Error Handling**:
8. Adopt ISO 20022 status reason codes
9. Implement structured rejection reason messages
10. Document status transition rules

**Phase 4 - Advanced Features**:
11. Add remittance information structures (structured creditor/debtor references)
12. Implement batch/group headers with control sums
13. Consider FX rate information for multi-currency transactions

### Positive Consequences

* Improved interoperability with external payment networks
* Better regulatory compliance posture
* Structured data quality improvements
* Clearer semantics for financial transactions
* Industry-standard naming conventions
* Easier onboarding for financial domain experts
* Future-proof architecture aligned with global standards

### Negative Consequences

* Increased schema complexity (more message types and fields)
* Breaking changes to existing protobuf definitions
* Additional validation logic required
* Learning curve for ISO 20022 code sets
* More verbose protobuf messages
* Potential performance impact from larger messages
* Need to maintain mapping documentation

## Pros and Cons of the Options

### Full ISO 20022 Schema Adoption

See Option 1 above. While most compliant, the verbosity of ISO 20022 XSD (e.g., `urn:iso:std:iso:20022:tech:xsd:pacs.008.001.08`) doesn't translate well to protobuf's lean design philosophy. A `FIToFICustomerCreditTransferV08` message has 50+ optional nested structures that would create an unwieldy protobuf hierarchy.

### Selective ISO 20022 Alignment

See Option 2 above. This approach recognizes that:
- BIAN already abstracts ISO 20022 at the service level
- Protobuf users expect simpler, more focused messages
- Full ISO 20022 compliance can be achieved at the translation layer (service adapters)
- Internal schemas can adopt ISO 20022 semantics without the full complexity

### Parallel ISO 20022 Adapter Layer

See Option 3 above. While architecturally clean (hexagonal architecture), this creates significant operational overhead. Experience from payment systems shows that maintaining dual schemas leads to:
- Synchronization issues between internal and external representations
- Translation bugs (e.g., currency precision loss, timezone issues)
- Confusion about source of truth for business rules
- Duplicated validation logic

Better to align internal schemas with standards where possible, reserving adapters for truly external-specific concerns (like SWIFT MT to MX message translation).

## Links

* [ISO 20022 Official Website](https://www.iso20022.org/)
* [ISO 20022 Data Dictionary](https://www.iso20022.org/iso-20022-message-definitions)
* [BIAN Semantic API Metamodel](https://bian.org/semantic-api/)
* [Task Master Tag: iso-standards-alignment](/.taskmaster/tags/iso-standards-alignment)
* [Related: ADR-0004 Event Schema Evolution](./0004-event-schema-evolution.md)
* [Related: ADR-0005 Adapter Pattern Layer Translation](./0005-adapter-pattern-layer-translation.md)

## Implementation Tasks

This audit has identified work captured in Task Master under tag `iso-standards-alignment`:

1. **Task 2**: Enhance Money type for ISO 20022 compliance (currency codes, decimal precision)
2. **Task 3**: Implement IBAN validation and account identification structures
3. **Task 4**: Align transaction codes with ISO 20022 External Code Sets
4. **Task 5**: Update gRPC service definitions for ISO 20022 alignment
5. **Task 6**: Add ISO 20022 field mappings as protobuf comments
6. **Task 7**: Implement protovalidate rules for ISO 20022 data quality

## Notes

**Migration Strategy**: Since protobuf supports backward-compatible field additions, most enhancements can be introduced incrementally:
- Add new fields as optional
- Maintain existing fields as deprecated (with `deprecated = true` option)
- Provide 2-3 release cycles for clients to migrate
- Use buf breaking change detection to enforce compatibility

**Performance Considerations**: Larger, more structured messages will increase serialization size and parsing time. However, the benefits of data quality and interoperability outweigh minor performance impacts. Monitor message sizes post-implementation.

**Validation Strategy**: Use `protovalidate` (buf.validate) extensively for ISO 20022 compliance:
- Currency code validation against ISO 4217
- IBAN checksum validation
- BIC format validation (8 or 11 characters, valid country code)
- Transaction code validation against allowed values

**Testing Requirements**: ISO 20022 compliance testing should include:
- Round-trip conversion tests (protobuf → ISO 20022 XML → protobuf)
- Validation rule tests for all ISO code sets
- Interoperability tests with external ISO 20022 systems
- Negative tests for invalid codes and malformed identifiers

**Future Reconsideration Triggers**:
- If migration to ISO 20022 MX messages becomes mandatory
- If verbosity of enhanced schemas causes significant performance issues
- If protobuf v4+ introduces features better suited to ISO 20022 modeling
- If BIAN releases updated semantic API guidance
