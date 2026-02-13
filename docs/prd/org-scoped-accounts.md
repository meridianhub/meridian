---
name: prd-org-scoped-accounts
description: Multi-party resource pooling following BIAN Syndicate Pattern
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

# PRD: Multi-Party Resource Pooling (BIAN Syndicate Pattern)

**Status:** Draft
**Version:** 1.3
**Date:** 2026-02-13
**Author:** Architecture Team
**Task Master Tag:** `org-scoped-accounts`

**ADRs:**

- [0002 - Microservices Per BIAN Domain](../adr/0002-microservices-per-bian-domain.md)
- [0013 - Universal Quantity Type System](../adr/0013-generic-asset-quantity-types.md)

**Related PRDs:**

- [Internal Bank Account](./internal-bank-account.md)
- [Starlark Saga Orchestration](./starlark-saga-orchestration-core.md)

---

## Table of Contents

- [BIAN Mapping & Terminology](#bian-mapping--terminology)
- [Executive Summary](#executive-summary)
- [Problem Statement](#problem-statement)
- [Solution Architecture](#solution-architecture)
- [Data Model Specification](#data-model-specification)
- [Saga Integration (Fulfillment Pattern)](#saga-integration-fulfillment-pattern)
- [Implementation Tasks](#implementation-tasks)
- [Appendix A: Syndicate Use Cases](#appendix-a-syndicate-use-cases)
- [Appendix B: Parimutuel Betting Regulatory Context](#appendix-b-parimutuel-betting-regulatory-context)

---

## BIAN Mapping & Terminology

This feature implements the **Syndicated Loan** and **Collateral Allocation Management** patterns to handle multi-party pooling.

| Meridian Concept | BIAN Service Domain | BIAN Description |
|------------------|---------------------|------------------|
| **Org-Scoped Account** | **Business Unit Management** | An account associated with a specific organizational unit/entity while owned by a party. |
| **Syndicate / Club** | **Syndicate Management** | A grouping of parties pooling resources for a shared financial purpose. |
| **Member** | **Participant** | A party involved in the syndicate arrangement. |
| **Distribution Logic** | **Collateral Allocation** | The logic determining how assets/liabilities are split among participants. |
| **Association Metadata** | **Structuring** | Configuration of the syndicate terms (share %, voting rights). |
| **Distribution Saga** | **Fulfillment** | The execution of the allocation rules. |

---

## Executive Summary

This PRD extends Meridian to support **Consortium Financial Management**. By implementing BIAN's **Syndicate Pattern**, we enable accounts to be owned by one Party (the Participant) while being operationally scoped to an Organization (the Syndicate).

### Core Capabilities

1. **Normalized Scoping:** Explicit `org_party_id` on accounts to track "Participant balance *within* Syndicate."
2. **Governance Metadata:** Enhanced `PartyAssociation` to store "Structuring" data (equity share, roles).
3. **Automated Fulfillment:** Starlark Sagas that read Governance Metadata to execute Collateral Allocation (distribution).

---

## Problem Statement

### The "Commingling" Gap

In a standard ledger, if Alice transfers £100 to a "Investment Club" account, the money moves:

`DR Alice Personal` -> `CR Investment Club Pool`

**The Issue:** The ledger loses the link between Alice and that specific £100.

1. We know the Club has £100.
2. We know Alice *sent* £100 (via transaction history).
3. We **do not** have a live balance record stating "Alice owns £100 of the Club's equity."

### Why this fails BIAN compliance

BIAN **Position Keeping** requires accurate tracking of all positions. Relying on re-calculating transaction history or storing balances in metadata fields violates the **Single Source of Truth** principle for balances.

---

## Solution Architecture

### 1. Business Unit Scoping (The "Sub-Facility" Model)

We treat the Syndicate as a **Business Unit** or **Legal Entity** context. Accounts can now carry this context.

**Account Structure:**

- **Owner (`party_id`):** The legal owner of the funds (e.g., Alice).
- **Scope (`org_party_id`):** The Syndicate context (e.g., Venture Alpha).
- **Instrument:** The asset being tracked (GBP, USD, VCU-2024).

### 2. Structuring via Associations

We extend the Party Service to act as the **Syndicate Assembly** engine.

- **Relationship:** Defines the link (e.g., `SYNDICATE_PARTICIPANT`).
- **Metadata:** Defines the rules (e.g., `{ "allocation_share": 0.25 }`).

### 3. Fulfillment via Sagas

Distribution Sagas (Collateral Allocation) read the Metadata to determine splits, then execute transfers to the Scoped Accounts.

---

## Data Model Specification

### 1. Current Account (Expansion)

*Reflects BIAN Account instances within a Business Unit.*

```sql
-- services/current-account/migrations/
ALTER TABLE current_account ADD COLUMN org_party_id UUID NULL;

-- NFR-1 Support: "Show me Alice's position in Venture Alpha"
CREATE INDEX idx_current_account_participant_syndicate
ON current_account (party_id, org_party_id)
WHERE org_party_id IS NOT NULL;

-- NFR-2 Support: "Show me all participants in Venture Alpha"
CREATE INDEX idx_current_account_syndicate_participants
ON current_account (org_party_id);

-- Integrity: One GBP account per person per syndicate
CREATE UNIQUE INDEX idx_current_account_scope_integrity
ON current_account (party_id, COALESCE(org_party_id, '00000000-0000-0000-0000-000000000000'), currency);
```

### 2. Internal Bank Account (Expansion)

*Allows Syndicates to hold their own P&L accounts.*

```sql
-- services/internal-bank-account/migrations/
ALTER TABLE internal_bank_account ADD COLUMN org_party_id UUID NULL;
```

*Validation Rule:* Org-Scoped internal accounts CANNOT act as System Settlement accounts (e.g., standard clearing).

### 3. Party Association (Enhancement)

*Implements BIAN Syndicate Assembly & Structuring.*

```protobuf
// api/proto/meridian/party/v1/party.proto

enum RelationshipType {
  // ... existing types
  RELATIONSHIP_TYPE_SYNDICATE_PARTICIPANT = 6; // BIAN: Participant
  RELATIONSHIP_TYPE_SYNDICATE_HOST = 7;        // BIAN: Lead/Arranger
  RELATIONSHIP_TYPE_BENEFICIAL_OWNER = 8;
}

message PartyAssociation {
  string id = 1;
  string related_party_id = 2;
  RelationshipType relationship_type = 3;

  // BIAN Structuring Data (e.g., allocation rules)
  google.protobuf.Struct metadata = 4;

  // BIAN Arrangement Lifecycle
  AssociationStatus status = 5;
  google.protobuf.Timestamp effective_from = 6;
  google.protobuf.Timestamp effective_to = 7;
}
```

---

## Saga Integration (Fulfillment Pattern)

The Saga runtime acts as the **Collateral Allocation Management** engine. It requires access to the **Structuring** data stored in Party Associations.

### New Starlark Handlers

**1. `party.get_structuring_data`**

Retrieves the metadata for a specific relationship.

```python
# Starlark
structuring = party.get_structuring_data(
    party_id="Alice",
    org_id="VentureAlpha",
    type="SYNDICATE_PARTICIPANT"
)
# Returns: {"allocation_share": "0.25", "role": "LP"}
```

**2. `party.list_participants`**

Retrieves all active participants in a syndicate.

```python
# Starlark
participants = party.list_participants(
    org_id="VentureAlpha",
    type="SYNDICATE_PARTICIPANT"
)
# Returns list of Party IDs
```

### Example: Dividend Distribution Saga

```python
# sagas/syndicate/distribution.star

# BIAN Pattern: Collateral Allocation Management

def distribute_yield(ctx):
    total_yield = Decimal(input.amount)

    # 1. Retrieve Participants (Syndicate Assembly)
    participants = party.list_participants(
        org_id=ctx.org_id,
        type="SYNDICATE_PARTICIPANT"
    )

    postings = []

    # 2. Calculate Allocation (Structuring)
    for p in participants:
        data = party.get_structuring_data(p.id, ctx.org_id, "SYNDICATE_PARTICIPANT")
        share = Decimal(data["allocation_share"])

        allocation = total_yield * share

        # 3. Resolve Scoped Account (Position Keeping)
        # Finds "Alice's account scoped to Venture Alpha"
        account_id = resolve_account(
            party_id=p.id,
            org_id=ctx.org_id,
            currency="GBP"
        )

        postings.append(posting(
            account_id=account_id,
            amount=allocation,
            direction="CREDIT",
            description="Dividend Distribution"
        ))

    return postings
```

---

## Implementation Tasks

### Stream 1: Core Data Models (2 Days)

- [ ] **DB Migration:** Add `org_party_id` to `current_account` and `internal_bank_account`.
- [ ] **DB Migration:** Add `metadata`, `status`, `effective_dates` to `party_association`.
- [ ] **Proto:** Update `Party`, `CurrentAccount`, `InternalBankAccount` definitions.

### Stream 2: Service Logic (3 Days)

- [ ] **Party Service:** Implement validation for Metadata JSONB.
- [ ] **Current Account:** Update `InitiateAccount` to handle scoping and enforce uniqueness.
- [ ] **Internal Bank Account:** Add validation preventing Org-Scoped accounts from being System accounts.

### Stream 3: Saga Infrastructure (3 Days)

- [ ] **Handler:** Implement `party.list_participants` in `services/party/client/starlark.go`.
- [ ] **Handler:** Implement `party.get_structuring_data`.
- [ ] **Handler:** Update `resolve_account` to accept optional `org_id`.

### Stream 4: Verification (2 Days)

- [ ] **Integration Test:** Create Syndicate, Add Members, Run Distribution Saga.
- [ ] **Performance Test:** Validate `idx_current_account_participant_syndicate` performance with 10k accounts.

---

## Appendix A: Syndicate Use Cases

### A. Investment Club (Asset Pooling)

**Scenario:** 4 partners pool money to buy 1000 Carbon Credits (TONNE_CO2E).

1. **Origination:** Syndicate created. Partners defined with 25% share in metadata.
2. **Contribution:** Partners transfer GBP to their Scoped Accounts.
3. **Purchase:** Saga debits Scoped Accounts (GBP), Credits Syndicate Inventory (TONNE_CO2E).
   - *Result:* Partners hold 0 GBP, Syndicate holds 1000 Assets.
4. **Allocation:** Saga credits Partners' Scoped Accounts (TONNE_CO2E) based on share.
   - *Result:* Each Partner holds 250 TONNE_CO2E *within* the Syndicate context.

### B. Gig Economy Fleet (Revenue Split)

**Scenario:** A Fleet Owner manages 100 Drivers.

1. **Revenue:** Platform pays Fleet £10,000.
2. **Structuring:** Each Driver has a `commission_rate` (e.g., 80%) in association metadata.
3. **Fulfillment:** Saga iterates drivers, calculates 80% split, credits Driver Scoped Account.
4. **Payout:** Drivers withdraw from Scoped Account to Personal Bank Account.

### C. Parimutuel Betting Syndicate (Initial Driver)

**Scenario:** "Lucky 7" Syndicate pools funds for a specific sports event (e.g., The Grand National).

*This represents the primary use case driving the Org-Scoped Account architecture.*

**Structure:**

- **Syndicate:** "Lucky 7" (Party Type: ORG)
- **Members:** Alice (Lead, 50% share), Bob (25%), Charlie (25%)
- **Accounts:** Each member has a `GBP` account scoped to `Lucky 7`.

**Lifecycle:**

1. **Contribution (Funding):**
   - Alice transfers £50 from her Personal Wallet to `ALICE_LUCKY7_GBP`.
   - Bob and Charlie transfer £25 each to their respective scoped accounts.
   - *State:* The Syndicate has £100 purchasing power, fully attributed to members.

2. **Bet Placement (Commitment):**
   - Syndicate Lead (Alice) initiates a bet on "Red Rum".
   - **Saga:** Debits £50 from `ALICE_LUCKY7_GBP`, £25 from `BOB...`, £25 from `CHARLIE...`.
   - **Saga:** Credits `LUCKY7_POOL` (Internal Holding Account) with £100.
   - **Saga:** Transfers £100 from `LUCKY7_POOL` to the Platform's Global Pool (External Settlement).

3. **Winnings (Distribution):**
   - "Red Rum" wins. Platform pays £500 to `LUCKY7_POOL`.
   - **Distribution Saga:** Reads Member Metadata (shares).
   - Credits `ALICE_LUCKY7_GBP`: £250.
   - Credits `BOB_LUCKY7_GBP`: £125.
   - Credits `CHARLIE_LUCKY7_GBP`: £125.

**Why this matters:**

This ensures strict segregation of funds. Even though Lucky 7 acts as a single entity to the outside world (the Betting Platform), internally, Meridian maintains a perfect, auditable ledger of exactly how much of the syndicate's balance belongs to Alice vs Bob vs Charlie at every second.

---

## Appendix B: Parimutuel Betting Regulatory Context

*This appendix provides UK-specific regulatory context for the betting syndicate use case.*

### UK Gambling Act 2005 Classification

Under **Section 12 of the Gambling Act 2005**, parimutuel betting is classified as **pool betting**:

> *"Betting is pool betting if made on terms that all or part of winnings shall be determined by reference to the aggregate of stakes paid... [and] shall be divided among the winners"*

### Required Licence

**Remote Pool Betting Operating Licence** from the UK Gambling Commission.

| Fee Category | GGY Threshold | Application Fee | Annual Fee |
|--------------|---------------|-----------------|------------|
| F1 | < £1.5 million | £938 | £2,406 |
| G1 | £1.5m – £3m | £1,414 | £16,053 |
| G2 | £3m – £7.5m | £1,414 | £19,054 |

**Personal Management Licences (PMLs):** £370 per key staff member. Required for specified management roles (financial planning, compliance, IT security).

The F1 tier provides a viable runway to test and grow the business.

### Pool Betting Model Classification

Per UKGC guidance, this model is **Model B — Actual Co-mingling**:

> *"The customer's funds would be directly entered into the Pool, thereby affecting the Pool dividend. The licensed operator and the Pool would each be required to hold a pool betting operating licence."*

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

The betting relationship is always **Syndicate ↔ Platform Pool**, never **Member ↔ Member**.

This is precisely why Meridian's org-scoped accounts matter: member contributions and distributions are **internal transfers** (not bets), while the syndicate's interaction with the platform pool is the **betting relationship**.

### Compliance Requirements

| Requirement | Meridian Support |
|-------------|------------------|
| **AML/KYC** | Party service stores verification status; Stripe handles identity |
| **Responsible Gambling** | Account limits via CEL policies; self-exclusion via account status |
| **Fair & Transparent** | Bi-temporal audit trail; Market Data Service for verifiable results |
| **Protect Vulnerable** | Spending limits; cooling-off periods via saga rules |

### Recommended Next Step

Before committing to licence application, contact the Gambling Commission at info@gamblingcommission.gov.uk with the exact model description. Key question: *"Does our parimutuel sports platform with syndicate pooling require just a Remote Pool Betting licence, or also a Remote Betting Intermediary licence?"*

---

## Success Criteria

- [ ] **NFR-1:** "My Positions" query < 20ms p99.
- [ ] **Auditability:** Full trace of Funds -> Syndicate -> Member Allocation.
- [ ] **Flexibility:** Distribution logic changes via Starlark (no code deploy).
