---
name: prd-org-scoped-accounts
description: Organizational scoping for accounts and enhanced party associations
triggers:
  - Implementing syndicate or group betting features
  - Building multi-party pooled account structures
  - Tracking member positions within organizations
  - Implementing revenue sharing or profit distribution
  - Building marketplace or platform models with sub-entities
  - Any scenario requiring "Alice's balance within Org X"
instructions: |
  This PRD extends the Account and Party models to support organizational scoping.
  Key concept: An account can be owned by a Party AND scoped to an Organization (another Party).
  This enables proper normalized tracking of member positions within groups/syndicates.
  Follows double-entry principles: every balance is stored, not calculated from metadata.
  Builds on existing Party associations but adds metadata for governance/rules.
---

# PRD: Organizational-Scoped Accounts and Enhanced Party Associations

**Status:** Draft
**Version:** 1.0
**Date:** 2026-02-13
**Author:** Architecture Team

**ADRs:**

- [0002 - Microservices Per BIAN Domain](../adr/0002-microservices-per-bian-domain.md)
- [0013 - Universal Quantity Type System](../adr/0013-generic-asset-quantity-types.md)

**Related PRDs:**

- [Internal Bank Account](./internal-bank-account.md) - Account registry patterns
- [Control Plane](./control-plane.md) - Manifest-based configuration

---

## Table of Contents

- [Business Context](#business-context)
- [Regulatory Framework](#regulatory-framework)

- [Executive Summary](#executive-summary)
- [Problem Statement](#problem-statement)
- [Proposed Solution](#proposed-solution)
- [Requirements](#requirements)
- [Technical Design](#technical-design)
- [Data Model Changes](#data-model-changes)
- [API Changes](#api-changes)
- [Migration Strategy](#migration-strategy)
- [Implementation Tasks](#implementation-tasks)
- [Success Criteria](#success-criteria)
- [Appendix A: Use Case Examples](#appendix-a-use-case-examples)
- [Appendix B: Alternative Approaches Considered](#appendix-b-alternative-approaches-considered)

---

## Business Context

### What We Are Building

A **parimutuel (tote) sports betting platform** with **syndicate pooling** capabilities, using Meridian as the ledger/treasury backend.

| Aspect | Description |
|--------|-------------|
| **Betting type** | Parimutuel/tote (pool betting) — odds determined by distribution of bets, not fixed by bookmaker |
| **Event type** | Sports events — outcomes determined by real-world results, not random number generation |
| **Group betting** | Syndicates pool funds, share entries, split winnings proportionally |
| **Payment model** | Stripe holds reserve funds until settlement; platform takes percentage fee |
| **Settlement** | Automated via Meridian sagas when sports results arrive via Market Data Service |

### Revenue Model

- **Platform commission** — percentage deducted from each pool before distribution
- **Syndicate management fees** — optional value-add services

### What We Are NOT

| Not This | Because |
|----------|---------|
| Casino | No RNG-based games |
| Fixed-odds bookmaker | We don't set odds or take position risk |
| Lottery | Outcomes from sports results, not random draws |
| Betting intermediary | We operate the pool directly, not facilitating peer-to-peer bets |

---

## Regulatory Framework

### UK Gambling Act 2005 Classification

Under **Section 12 of the Gambling Act 2005**, this platform is classified as **pool betting**:

> *"Betting is pool betting if made on terms that all or part of winnings shall be determined by reference to the aggregate of stakes paid... [and] shall be divided among the winners"*

### Required Licence

**Remote Pool Betting Operating Licence** from the UK Gambling Commission.

| Fee Category | GGY Threshold | Application Fee | Annual Fee |
|--------------|---------------|-----------------|------------|
| F1 | < £1.5 million | £938 | £2,406 |
| G1 | £1.5m – £3m | £1,414 | £16,053 |
| G2 | £3m – £7.5m | £1,414 | £19,054 |

The F1 tier provides a viable runway to test and grow the business.

### Pool Betting Model Classification

Per UKGC guidance, we are **Model B — Actual Co-mingling**:

> *"The customer's funds would be directly entered into the Pool, thereby affecting the Pool dividend. The licensed operator and the Pool would each be required to hold a pool betting operating licence."*

We operate the pool directly:
- Accept customer funds into the pool
- Calculate dividends based on aggregate stakes
- Take commission from the pool
- Settle automatically via Meridian

### Syndicate Design: Staying Within Pool Betting

**Critical Design Constraint:** Syndicates must be structured as **collective entries into the platform's pool**, NOT as bets between syndicate members.

```
✅ CORRECT: Pool Betting Model
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│   Member    │────▶│  Syndicate  │────▶│  PLATFORM   │
│  (Alice)    │     │ (Lucky 7)   │     │    POOL     │
└─────────────┘     └─────────────┘     └─────────────┘
     │                    │                    │
     │ Contribution       │ Entry Purchase     │ Payout
     ▼                    ▼                    ▼
  Internal            Syndicate            Winning
  Transfer            bets INTO            syndicates
                      the pool             paid FROM pool

✗ AVOID: Intermediary Model (triggers additional licence)
┌─────────────┐            ┌─────────────┐
│   Member    │◀──────────▶│   Member    │
│  (Alice)    │   Bet      │   (Bob)     │
└─────────────┘  between   └─────────────┘
                 members
```

**How Meridian's org-scoped accounts support this:**

1. **Members contribute TO their syndicate** — internal transfer, not a bet
2. **Syndicates purchase entries FROM the platform pool** — the betting relationship
3. **Platform pays winning syndicates** — pool to syndicate
4. **Syndicates distribute to members** — internal distribution, not winnings from member-to-member bets

The betting relationship is always **Syndicate ↔ Platform Pool**, never **Member ↔ Member**.

### Compliance Requirements

Once licensed, the platform must implement:

| Requirement | Meridian Support |
|-------------|------------------|
| **AML/KYC** | Party service stores verification status; Stripe handles identity |
| **Responsible Gambling** | Account limits via CEL policies; self-exclusion via account status |
| **Fair & Transparent** | Bi-temporal audit trail; Market Data Service for verifiable results |
| **Protect Vulnerable** | Spending limits; cooling-off periods via saga rules |

### Recommended Next Step

Before committing to licence application, contact the Gambling Commission at info@gamblingcommission.gov.uk with the exact model description. Key question: *"Does our parimutuel sports platform with syndicate pooling require just a Remote Pool Betting licence, or also a Remote Betting Intermediary licence?"*

---

## Executive Summary

This PRD proposes extending Meridian's account and party models to support **organizational scoping** - the ability for an account to be owned by one party while being scoped to another organizational party. This enables proper normalized accounting for syndicate betting, group investments, marketplace platforms, and any multi-party pooled structure.

### Key Changes

1. **Accounts gain optional `org_party_id`** - Scope an account to an organization
2. **Party associations gain `metadata`** - Store governance rules (share %, roles, permissions)
3. **New relationship types** - MEMBER, SYNDICATE_MEMBER for group membership
4. **Multi-asset position tracking** - Per-member, per-org, per-instrument balances

---

## Problem Statement

### Current State

**Party Associations:**
```go
type PartyAssociation struct {
    RelatedPartyID   uuid.UUID
    RelationshipType RelationshipType  // SPOUSE, DEPENDENT, BUSINESS_PARTNER, GUARANTOR, BENEFICIAL_OWNER
    CreatedAt        time.Time
    // No metadata field for share %, role, permissions, etc.
}
```

**Current Account:**
```go
type CurrentAccount struct {
    partyID string  // Owner - single dimension only
    // No organizational scope
}
```

### The Gap

Consider a lottery syndicate use case:

- **Alice** (Individual) is a member of **Lucky Seven** (Organization)
- Alice contributes £100 to the syndicate
- The syndicate holds lottery entries worth £400
- Alice's 25% share = £100 cash + 2.5 entries

**Question:** Where is Alice's £100 contribution tracked?

**Current Options (All Flawed):**

| Approach | Problem |
|----------|---------|
| Track in association metadata | Not a proper ledger entry, no audit trail |
| Single syndicate pool account | Can't query "What's Alice's position?" |
| Calculate from share % × pool | Derived data, not normalized, loses transaction history |

### Why This Matters

Meridian is a **normalized billing engine**. Core principles:

1. **Every balance is stored, not calculated**
2. **Every movement is a ledger entry**
3. **Full audit trail for every position**

The current model cannot represent "Alice's position within Lucky Seven" as a proper account with transaction history.

---

## Proposed Solution

### 1. Add Organizational Scope to Accounts

```go
type CurrentAccount struct {
    partyID     string   // Owner (who can act on the account)
    orgPartyID  *string  // NEW: Optional organizational scope
    instrument  string
    accountType string
    // ... existing fields
}
```

This enables:

```
ALICE_PERSONAL
  party_id: alice
  org_party_id: null
  instrument: GBP
  balance: £500

ALICE_LUCKY7_GBP
  party_id: alice
  org_party_id: lucky_seven
  instrument: GBP
  balance: £100

ALICE_LUCKY7_ENTRIES
  party_id: alice
  org_party_id: lucky_seven
  instrument: LOTTERY_ENTRY
  balance: 2.5
```

### 2. Add Metadata to Party Associations

```go
type PartyAssociation struct {
    RelatedPartyID   uuid.UUID
    RelationshipType RelationshipType
    CreatedAt        time.Time
    Metadata         map[string]interface{}  // NEW: Governance data
}
```

Metadata stores governance rules, **not balances**:

```json
{
  "share_pct": 25,
  "role": "member",
  "voting_weight": 1,
  "joined_at": "2025-01-15T00:00:00Z",
  "auto_contribute": true,
  "contribution_cap": 1000
}
```

### 3. Add New Relationship Types

```go
const (
    // Existing
    RelationshipTypeSpouse          RelationshipType = "SPOUSE"
    RelationshipTypeDependent       RelationshipType = "DEPENDENT"
    RelationshipTypeBusinessPartner RelationshipType = "BUSINESS_PARTNER"
    RelationshipTypeGuarantor       RelationshipType = "GUARANTOR"
    RelationshipTypeBeneficialOwner RelationshipType = "BENEFICIAL_OWNER"

    // NEW
    RelationshipTypeMember          RelationshipType = "MEMBER"
    RelationshipTypeSyndicateMember RelationshipType = "SYNDICATE_MEMBER"
    RelationshipTypeOperator        RelationshipType = "OPERATOR"
    RelationshipTypeBeneficiary     RelationshipType = "BENEFICIARY"
)
```

---

## Requirements

### Functional Requirements

#### FR-1: Organizational Account Scoping

| ID | Requirement | Priority |
|----|-------------|----------|
| FR-1.1 | CurrentAccount SHALL support an optional `org_party_id` field | P0 |
| FR-1.2 | InternalBankAccount SHALL support an optional `org_party_id` field | P0 |
| FR-1.3 | `org_party_id` MUST reference a valid Party of type ORGANIZATION | P0 |
| FR-1.4 | Accounts with same `party_id` + `org_party_id` + `instrument` SHALL be unique | P0 |
| FR-1.5 | System SHALL support querying all accounts for a party within an organization | P0 |
| FR-1.6 | System SHALL support querying all member accounts within an organization | P0 |
| FR-1.7 | Null `org_party_id` SHALL indicate a personal/unscoped account | P0 |

#### FR-2: Enhanced Party Associations

| ID | Requirement | Priority |
|----|-------------|----------|
| FR-2.1 | PartyAssociation SHALL support a `metadata` field (JSONB) | P0 |
| FR-2.2 | Metadata schema SHALL be validated per relationship type via CEL | P1 |
| FR-2.3 | System SHALL support MEMBER and SYNDICATE_MEMBER relationship types | P0 |
| FR-2.4 | System SHALL support OPERATOR and BENEFICIARY relationship types | P1 |
| FR-2.5 | Association metadata SHALL be immutable; updates create new version | P1 |
| FR-2.6 | System SHALL track association history (joined_at, left_at) | P1 |

#### FR-3: Multi-Asset Position Tracking

| ID | Requirement | Priority |
|----|-------------|----------|
| FR-3.1 | Member SHALL have separate org-scoped account per instrument | P0 |
| FR-3.2 | All asset types (fiat, commodity, voucher) SHALL be supported | P0 |
| FR-3.3 | Transfers between personal and org-scoped accounts SHALL be tracked | P0 |
| FR-3.4 | System SHALL support querying aggregate position across instruments | P1 |

#### FR-4: Distribution and Settlement

| ID | Requirement | Priority |
|----|-------------|----------|
| FR-4.1 | Saga SHALL distribute funds based on association metadata (share %) | P0 |
| FR-4.2 | Distribution logic SHALL be configurable via Starlark per org | P1 |
| FR-4.3 | Settlement SHALL create proper ledger entries per member | P0 |
| FR-4.4 | System SHALL support equal-split and weighted distribution modes | P0 |

### Non-Functional Requirements

| ID | Requirement | Priority |
|----|-------------|----------|
| NFR-1 | Query for member's org-scoped accounts SHALL complete in < 10ms p99 | P0 |
| NFR-2 | Query for all members of an org SHALL complete in < 50ms p99 | P0 |
| NFR-3 | Migration SHALL be backward compatible (existing accounts unaffected) | P0 |
| NFR-4 | Association metadata SHALL support up to 64KB JSON | P1 |

---

## Technical Design

### Account Model Extension

#### Current Account Domain

```go
// services/current-account/domain/account.go

type CurrentAccount struct {
    id              uuid.UUID
    partyID         string
    orgPartyID      *string  // NEW: Optional organizational scope
    accountType     AccountType
    currency        Currency
    status          AccountStatus
    // ... existing fields
}

// Builder extension
func (b *CurrentAccountBuilder) WithOrgPartyID(orgPartyID string) *CurrentAccountBuilder {
    b.account.orgPartyID = &orgPartyID
    return b
}
```

#### Internal Bank Account Domain

```go
// services/internal-bank-account/domain/account.go

type InternalBankAccount struct {
    id              uuid.UUID
    partyID         string
    orgPartyID      *string  // NEW: Optional organizational scope
    accountType     AccountType
    instrumentCode  string
    // ... existing fields
}
```

### Party Association Extension

```go
// services/party/domain/party.go

type PartyAssociation struct {
    ID               uuid.UUID              // NEW: Unique identifier for versioning
    RelatedPartyID   uuid.UUID
    RelationshipType RelationshipType
    Metadata         map[string]interface{} // NEW: Governance data
    Status           AssociationStatus      // NEW: ACTIVE, SUSPENDED, TERMINATED
    CreatedAt        time.Time
    UpdatedAt        time.Time              // NEW: For metadata updates
    TerminatedAt     *time.Time             // NEW: When membership ended
}

// New relationship types
const (
    RelationshipTypeMember          RelationshipType = "MEMBER"
    RelationshipTypeSyndicateMember RelationshipType = "SYNDICATE_MEMBER"
    RelationshipTypeOperator        RelationshipType = "OPERATOR"
    RelationshipTypeBeneficiary     RelationshipType = "BENEFICIARY"
)

// Association status
type AssociationStatus string

const (
    AssociationStatusActive     AssociationStatus = "ACTIVE"
    AssociationStatusSuspended  AssociationStatus = "SUSPENDED"
    AssociationStatusTerminated AssociationStatus = "TERMINATED"
)
```

### Database Schema Changes

#### CurrentAccount Table

```sql
-- Migration: Add org_party_id to current_account
ALTER TABLE current_account
ADD COLUMN org_party_id UUID NULL;

-- Index for org-scoped queries
CREATE INDEX idx_current_account_org_party
ON current_account (org_party_id, party_id)
WHERE org_party_id IS NOT NULL;

-- Unique constraint: one account per party+org+instrument
CREATE UNIQUE INDEX idx_current_account_party_org_instrument
ON current_account (party_id, COALESCE(org_party_id, '00000000-0000-0000-0000-000000000000'), currency);

-- Foreign key to party (org must exist and be ORGANIZATION type)
-- Note: Enforced at application layer to check party_type = 'ORGANIZATION'
```

#### Party Association Table

```sql
-- Migration: Add metadata and status to party_association
ALTER TABLE party_association
ADD COLUMN id UUID NOT NULL DEFAULT gen_random_uuid(),
ADD COLUMN metadata JSONB DEFAULT '{}',
ADD COLUMN status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE',
ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
ADD COLUMN terminated_at TIMESTAMPTZ NULL;

-- Index for metadata queries (GIN for JSONB)
CREATE INDEX idx_party_association_metadata
ON party_association USING GIN (metadata);

-- Index for active associations
CREATE INDEX idx_party_association_active
ON party_association (related_party_id, relationship_type)
WHERE status = 'ACTIVE';
```

### Proto Changes

#### Current Account Proto

```protobuf
// api/proto/meridian/current_account/v1/current_account.proto

message CurrentAccount {
  string account_id = 1;
  string party_id = 2;
  optional string org_party_id = 3;  // NEW
  // ... existing fields
}

message InitiateCurrentAccountRequest {
  string party_id = 1;
  optional string org_party_id = 2;  // NEW
  // ... existing fields
}

message ListAccountsRequest {
  string party_id = 1;
  optional string org_party_id = 2;  // NEW: Filter by org
  // ... existing fields
}

// NEW: Query all member accounts in an org
message ListOrgMemberAccountsRequest {
  string org_party_id = 1;
  optional string instrument_code = 2;
  int32 page_size = 3;
  string page_token = 4;
}
```

#### Party Proto

```protobuf
// api/proto/meridian/party/v1/party.proto

message PartyAssociation {
  string id = 1;  // NEW
  string related_party_id = 2;
  RelationshipType relationship_type = 3;
  google.protobuf.Struct metadata = 4;  // NEW
  AssociationStatus status = 5;  // NEW
  google.protobuf.Timestamp created_at = 6;
  google.protobuf.Timestamp updated_at = 7;  // NEW
  optional google.protobuf.Timestamp terminated_at = 8;  // NEW
}

enum RelationshipType {
  RELATIONSHIP_TYPE_UNSPECIFIED = 0;
  RELATIONSHIP_TYPE_SPOUSE = 1;
  RELATIONSHIP_TYPE_DEPENDENT = 2;
  RELATIONSHIP_TYPE_BUSINESS_PARTNER = 3;
  RELATIONSHIP_TYPE_GUARANTOR = 4;
  RELATIONSHIP_TYPE_BENEFICIAL_OWNER = 5;
  // NEW
  RELATIONSHIP_TYPE_MEMBER = 6;
  RELATIONSHIP_TYPE_SYNDICATE_MEMBER = 7;
  RELATIONSHIP_TYPE_OPERATOR = 8;
  RELATIONSHIP_TYPE_BENEFICIARY = 9;
}

enum AssociationStatus {
  ASSOCIATION_STATUS_UNSPECIFIED = 0;
  ASSOCIATION_STATUS_ACTIVE = 1;
  ASSOCIATION_STATUS_SUSPENDED = 2;
  ASSOCIATION_STATUS_TERMINATED = 3;
}

// NEW: List members of an organization
message ListOrgMembersRequest {
  string org_party_id = 1;
  repeated RelationshipType relationship_types = 2;  // Filter by type
  bool include_terminated = 3;
  int32 page_size = 4;
  string page_token = 5;
}

message ListOrgMembersResponse {
  repeated PartyAssociation members = 1;
  string next_page_token = 2;
}
```

---

## API Changes

### Current Account Service

| Method | Change |
|--------|--------|
| `InitiateCurrentAccount` | Add optional `org_party_id` parameter |
| `RetrieveCurrentAccount` | Include `org_party_id` in response |
| `ListAccounts` | Add `org_party_id` filter |
| **NEW** `ListOrgMemberAccounts` | Query all member accounts in an org |

### Party Service

| Method | Change |
|--------|--------|
| `RegisterParty` | No change (associations added separately) |
| `UpdateParty` | Support updating association metadata |
| **NEW** `AddAssociation` | Create association with metadata |
| **NEW** `UpdateAssociation` | Update association metadata (creates version) |
| **NEW** `TerminateAssociation` | Soft-delete association |
| **NEW** `ListOrgMembers` | Query members of an organization |
| **NEW** `GetMemberAssociation` | Get specific member's association details |

### Internal Bank Account Service

| Method | Change |
|--------|--------|
| `InitiateInternalBankAccount` | Add optional `org_party_id` parameter |
| `RetrieveInternalBankAccount` | Include `org_party_id` in response |
| `ListInternalBankAccounts` | Add `org_party_id` filter |

---

## Migration Strategy

### Phase 1: Schema Migration (Non-Breaking)

1. Add `org_party_id` column to `current_account` (nullable)
2. Add `org_party_id` column to `internal_bank_account` (nullable)
3. Add columns to `party_association` (nullable with defaults)
4. Create indexes

**Impact:** Zero. Existing accounts have `org_party_id = NULL` (personal accounts).

### Phase 2: API Extension (Non-Breaking)

1. Add optional `org_party_id` to proto messages
2. Implement new endpoints
3. Update domain models

**Impact:** Zero. Existing clients don't send `org_party_id`.

### Phase 3: Saga Updates

1. Update distribution sagas to use org-scoped accounts
2. Create sample syndicate sagas

**Impact:** Zero. New sagas for new use cases.

### Rollback Strategy

All changes are additive. Rollback = ignore new fields.

---

## Implementation Tasks

### Phase 1: Data Model (Est. 3-5 tasks)

- [ ] Add `org_party_id` to CurrentAccount domain model
- [ ] Add `org_party_id` to InternalBankAccount domain model
- [ ] Extend PartyAssociation with metadata, status, timestamps
- [ ] Add new RelationshipType constants
- [ ] Database migrations for all schema changes

### Phase 2: Persistence Layer (Est. 4-6 tasks)

- [ ] Update CurrentAccount repository for org-scoped queries
- [ ] Update InternalBankAccount repository for org-scoped queries
- [ ] Update PartyAssociation repository for metadata storage
- [ ] Add ListOrgMemberAccounts query
- [ ] Add ListOrgMembers query
- [ ] Add indexes and optimize queries

### Phase 3: Service Layer (Est. 4-6 tasks)

- [ ] Update CurrentAccount service with org_party_id support
- [ ] Update InternalBankAccount service with org_party_id support
- [ ] Implement AddAssociation, UpdateAssociation, TerminateAssociation
- [ ] Implement ListOrgMembers
- [ ] Add validation for org_party_id (must be ORGANIZATION type)

### Phase 4: Proto/API (Est. 3-4 tasks)

- [ ] Update current_account.proto
- [ ] Update internal_bank_account.proto
- [ ] Update party.proto
- [ ] Generate and integrate updated clients

### Phase 5: Sagas (Est. 2-3 tasks)

- [ ] Create syndicate contribution saga
- [ ] Create syndicate distribution saga
- [ ] Create sample Starlark templates for distribution logic

### Phase 6: Testing (Est. 3-4 tasks)

- [ ] Unit tests for domain model changes
- [ ] Integration tests for org-scoped account queries
- [ ] E2E test for full syndicate lifecycle
- [ ] Performance benchmarks for new queries

---

## Success Criteria

### Functional

- [ ] Can create account scoped to an organization
- [ ] Can query all of Alice's accounts within Lucky Seven syndicate
- [ ] Can query all member accounts within Lucky Seven syndicate
- [ ] Can store and retrieve association metadata (share %, role)
- [ ] Can track membership lifecycle (join, suspend, terminate)
- [ ] Distribution saga correctly splits funds based on metadata

### Performance

- [ ] ListOrgMemberAccounts < 10ms p99 for 100 members
- [ ] ListOrgMembers < 50ms p99 for 1000 members

### Compatibility

- [ ] Existing accounts (null org_party_id) continue to work
- [ ] Existing API calls without org_party_id continue to work
- [ ] No breaking changes to proto wire format

---

## Appendix A: Use Case Examples

### Syndicate Betting

```
Syndicate: Lucky Seven (Party, type=ORGANIZATION)
Members: Alice, Bob, Charlie, Diana (each 25% share)

Accounts:
- LUCKY7_POOL (owned by syndicate, no org scope)
- ALICE_LUCKY7_GBP (owned by Alice, scoped to Lucky Seven)
- ALICE_LUCKY7_ENTRIES (owned by Alice, scoped to Lucky Seven)
- BOB_LUCKY7_GBP (owned by Bob, scoped to Lucky Seven)
- ... etc for each member × instrument

Transactions:
1. Alice contributes £100:
   DR ALICE_PERSONAL     £100
   CR ALICE_LUCKY7_GBP   £100

2. Syndicate buys 10 entries from pool:
   DR LUCKY7_POOL (GBP)           £100
   CR PLATFORM_REVENUE            £100
   DR LUCKY7_POOL (LOTTERY_ENTRY) 10

3. Distribute entries to members:
   DR LUCKY7_POOL (LOTTERY_ENTRY) 10
   CR ALICE_LUCKY7_ENTRIES        2.5
   CR BOB_LUCKY7_ENTRIES          2.5
   CR CHARLIE_LUCKY7_ENTRIES      2.5
   CR DIANA_LUCKY7_ENTRIES        2.5

4. Syndicate wins £500:
   DR PRIZE_POOL                  £500
   CR LUCKY7_POOL (GBP)           £500

5. Distribute winnings:
   DR LUCKY7_POOL (GBP)           £500
   CR ALICE_LUCKY7_GBP            £125
   CR BOB_LUCKY7_GBP              £125
   CR CHARLIE_LUCKY7_GBP          £125
   CR DIANA_LUCKY7_GBP            £125
```

### Revenue Sharing Platform

```
Platform: Acme Marketplace (Party, type=ORGANIZATION)
Sellers: Store A, Store B (each with revenue accounts)

Accounts:
- ACME_PLATFORM_FEE (owned by Acme)
- STORE_A_ACME_REVENUE (owned by Store A, scoped to Acme)
- STORE_B_ACME_REVENUE (owned by Store B, scoped to Acme)

Transaction (customer buys from Store A):
1. Customer pays £100:
   DR CUSTOMER_WALLET            £100
   CR ACME_ESCROW                £100

2. Platform takes 10% fee:
   DR ACME_ESCROW                £10
   CR ACME_PLATFORM_FEE          £10

3. Remainder to seller (org-scoped):
   DR ACME_ESCROW                £90
   CR STORE_A_ACME_REVENUE       £90
```

---

## Appendix B: Alternative Approaches Considered

### Option 1: Metadata-Only (No Org-Scoped Accounts)

Track member share % in association metadata. Calculate balance as `pool × share_pct`.

**Rejected because:**
- Derived data, not normalized
- No transaction history per member
- Cannot support contribution-weighted shares
- Violates double-entry accounting principles

### Option 2: Virtual Accounts

Create virtual sub-ledgers without actual account records.

**Rejected because:**
- Adds complexity without benefit
- Still need storage somewhere
- Harder to query than real accounts

### Option 3: Bucket-Based Tracking

Use Position Keeping buckets to track member positions.

**Rejected because:**
- Buckets are for fungibility grouping, not ownership
- Doesn't provide account-level features (status, type, policies)
- Would conflate two different concerns

### Chosen: Org-Scoped Accounts

Real accounts with explicit `org_party_id`. Proper normalized accounting.

**Advantages:**
- Every balance is stored, not calculated
- Full transaction history per member per org
- Supports any distribution model
- Follows BIAN account patterns
- Works with existing account features (liens, holds, policies)
