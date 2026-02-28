---
name: prd-internal-account
description: BIAN Internal Account service for managing non-customer-facing accounts
triggers:
  - Creating or working on internal accounts
  - Implementing clearing, nostro, vostro, or holding accounts
  - Working on the "other leg" of double-entry transactions
  - Managing correspondent bank relationships
  - Tracking balances for non-customer accounts
  - Integrating with FinancialAccounting for balance updates
instructions: |
  This PRD defines the Internal Account service following BIAN v14.0.
  Key patterns: Multi-asset support via InstrumentAmount, real-time O(1) balance queries.
  Uses Dimension from reference_data/v1, InstrumentAmount from quantity/v1.
  Service structure follows ADR-0015. Proto package: internal_account (with underscores).
  BIAN spec: https://github.com/bian-official/public/blob/main/release14.0.0/semantic-apis/oas3%20/yamls/InternalBankAccount.yaml
---

# PRD: Internal Account Service

**Status:** Implemented
**Version:** 1.0
**Date:** 2026-01-06
**Author:** Architecture Team
**Task Master Tag:** `internal-account` (33/33 tasks done)

**ADRs:**

- [0002 - Microservices Per BIAN Domain](../adr/0002-microservices-per-bian-domain.md)
- [0013 - Universal Quantity Type System](../adr/0013-generic-asset-quantity-types.md)
- [0014 - Financial Instrument Reference Data](../adr/0014-financial-instrument-reference-data.md)
- [0015 - Standard Service Directory Structure](../adr/0015-standard-service-directory-structure.md)
- [0016 - Tenant ID Naming Strategy](../adr/0016-tenant-id-naming-strategy.md)

**Related PRDs:**

- **Position Keeping Balance Ownership** (Task Master tag: `position-keeping-balance`) - **Required dependency**
  - Defines that Position Keeping owns balance computation
  - This service queries Position Keeping for balance, does NOT store it locally
  - Must be implemented before or alongside this service

**Target Task Master Tag:** `internal-account`

---

## Table of Contents

- [Executive Summary](#executive-summary)
- [BIAN Alignment](#bian-alignment)
- [Requirements](#requirements)
- [Technical Design](#technical-design)
- [Implementation Tasks](#implementation-tasks)
- [Success Criteria](#success-criteria)
- [Appendix A: Use Case Examples](#appendix-a-use-case-examples)
- [Appendix B: BIAN Semantic API Reference](#appendix-b-bian-semantic-api-reference)

---

## Executive Summary

This PRD defines the requirements for implementing the **Internal Account** service
in Meridian, following the BIAN v14.0 Service Domain specification. This service fills a
critical architectural gap: managing the "other leg" of double-entry transactions that
are not customer-facing accounts.

### Problem Statement

Currently, Meridian's ledger architecture has a gap:

- **CurrentAccount** service manages customer-facing accounts with full lifecycle (IBAN, overdraft, compliance)
- **FinancialAccounting** service manages the general ledger with double-entry postings
- **The counterparty accounts** (clearing, nostro, vostro, holding, revenue, expense)
  are **hardcoded environment variables** with no registry, validation, or real-time
  balance tracking

This creates several problems:

1. No way to query "What's the balance of our GBP clearing account?" without O(n) ledger aggregation
2. No account registry for the bank's internal accounts
3. No lifecycle management (create, suspend, close) for internal accounts
4. Cannot support multi-tenant SaaS where the "other leg" may belong to a different tenant
5. Cannot track non-fiat asset positions (energy, compute, carbon credits) for internal accounts

### Solution

Implement the **BIAN Internal Account** service domain as a multi-asset account
registry with real-time balance tracking, enabling:

- O(1) balance queries for any internal account
- Full account lifecycle management
- Support for any instrument type (fiat, energy, compute, carbon, custom)
- Tenant-scoped account configuration
- Integration with FinancialAccounting for balance updates on every posting

---

## BIAN Alignment

### Primary Service Domain

**Internal Bank Account** (BIAN v14.0)

> "Manages holding accounts, mirror accounts, working accounts etc. that are required
> for the booking of that part of a transaction in the bank world (so not in the
> accounting world) that is not to be booked on a customer account."

**BIAN Semantic API Specification:**

- [InternalBankAccount.yaml](https://github.com/bian-official/public/blob/main/release14.0.0/semantic-apis/oas3%20/yamls/InternalBankAccount.yaml)

**BIAN References:**

- [BIAN Internal Bank Account Service Domain](https://bian.org/servicelandscape-14-0-0/)
- [BIAN v14.0.0 Release Notes](https://bian.org/wp-content/uploads/2026/02/BIAN-v14.0-Release-Notes-v1.0_-Final-Version.pdf)

### Functional Pattern

**Fulfill** (changed from "Track" in BIAN v12.0)

This indicates the service actively manages account lifecycles, not just tracks balances.

### Related BIAN Service Domains

| Service Domain | Relationship | Future Integration |
|----------------|--------------|-------------------|
| **Correspondent Bank Operations** | Nostro/vostro relationship management | Phase 2 |
| **Account Reconciliation** | Automated reconciliation between accounts | Phase 2 |
| **Financial Accounting** | Source of ledger postings | Phase 1 (immediate) |

---

## Requirements

### Functional Requirements

#### FR-1: Account Registry

| ID | Requirement | Priority |
|----|-------------|----------|
| FR-1.1 | System SHALL maintain a registry of internal accounts (tenant isolation via schema-per-tenant) | P0 |
| FR-1.2 | Each account SHALL have a unique account_id within its schema | P0 |
| FR-1.3 | Accounts SHALL support multiple types: CLEARING, NOSTRO, VOSTRO, HOLDING, SUSPENSE, REVENUE, EXPENSE | P0 |
| FR-1.4 | Accounts SHALL be scoped to a single instrument (currency or asset type) | P0 |
| FR-1.5 | System SHALL validate account existence before accepting ledger postings | P0 |
| FR-1.6 | System SHALL support custom attributes for account metadata | P1 |

#### FR-2: Multi-Asset Support

| ID | Requirement | Priority |
|----|-------------|----------|
| FR-2.1 | Accounts SHALL support any instrument dimension: CURRENCY, ENERGY, COMPUTE, CARBON, COUNT, DATA, etc. | P0 |
| FR-2.2 | Account instrument SHALL reference the Instrument Registry (Reference Data service) | P0 |
| FR-2.3 | Balance operations SHALL use the `InstrumentAmount` type from `quantity/v1` | P0 |
| FR-2.4 | System SHALL enforce instrument consistency for all postings to an account | P0 |

#### FR-3: Balance Retrieval (via Position Keeping)

> **Note**: Per BIAN architecture, Position Keeping owns balance computation. Internal Account
> queries Position Keeping for balance - it does NOT store balance locally. See
> Position Keeping Balance Ownership PRD (Task Master tag: `position-keeping-balance`).

| ID | Requirement | Priority |
|----|-------------|----------|
| FR-3.1 | System SHALL query Position Keeping for current balance | P0 |
| FR-3.2 | Balance queries SHALL return O(1) response (Position Keeping pre-computes) | P0 |
| FR-3.3 | System SHALL support all BIAN balance types: CURRENT, AVAILABLE, LEDGER, RESERVE | P1 |
| FR-3.4 | System SHALL pass overdraft configuration to Position Keeping for available balance calculation | P1 |

#### FR-4: Account Lifecycle Management

| ID | Requirement | Priority |
|----|-------------|----------|
| FR-4.1 | System SHALL support account creation (Initiate) | P0 |
| FR-4.2 | System SHALL support account updates (Update) | P0 |
| FR-4.3 | System SHALL support lifecycle transitions: ACTIVE -> SUSPENDED -> CLOSED | P0 |
| FR-4.4 | System SHALL prevent postings to non-ACTIVE accounts | P0 |
| FR-4.5 | System SHALL maintain audit trail of all lifecycle transitions | P0 |

#### FR-5: BIAN Control Record Operations

| ID | Requirement | Priority |
|----|-------------|----------|
| FR-5.1 | InitiateInternalAccountFacility - Create new account | P0 |
| FR-5.2 | UpdateInternalAccountFacility - Modify account settings | P0 |
| FR-5.3 | ControlInternalAccountFacility - Lifecycle state transitions | P0 |
| FR-5.4 | RetrieveInternalAccountFacility - Get account by ID | P0 |
| FR-5.5 | ListInternalAccountFacilities - Query accounts with filters | P0 |
| FR-5.6 | GetBalance - Query balance from Position Keeping | P0 |

> **Note**: No `RecordPosting` RPC - postings are recorded via FinancialAccounting → Position Keeping.
> This service is a registry/metadata service, not a balance mutator.

#### FR-6: Correspondent Bank Support (Nostro/Vostro)

| ID | Requirement | Priority |
|----|-------------|----------|
| FR-6.1 | NOSTRO accounts SHALL support correspondent bank details | P1 |
| FR-6.2 | VOSTRO accounts SHALL support correspondent bank details | P1 |
| FR-6.3 | Correspondent details SHALL include: bank_id, bank_name, external_account_ref | P1 |

### Non-Functional Requirements

#### NFR-1: Performance

| ID | Requirement | Target |
|----|-------------|--------|
| NFR-1.1 | Balance query latency (via Position Keeping) | < 50ms p99 |
| NFR-1.2 | Account creation latency | < 50ms p99 |
| NFR-1.3 | Account lookup latency | < 5ms p99 |

#### NFR-2: Reliability

| ID | Requirement | Target |
|----|-------------|--------|
| NFR-2.1 | Service availability | 99.9% |
| NFR-2.2 | Data durability (account registry) | 99.999999% |

#### NFR-3: Scalability

| ID | Requirement | Target |
|----|-------------|--------|
| NFR-3.1 | Accounts per schema (tenant) | 10,000+ |
| NFR-3.2 | Schemas per deployment | 1,000+ |
| NFR-3.3 | Balance queries per day | 10M+ |

---

## Technical Design

> **Implementation Note**: This service should be implemented as a sibling to CurrentAccount.
> Reuse patterns, structures, and utilities from `services/current-account/` where possible.
> The two services share similar concerns (account lifecycle, balance tracking, status management)
> but differ in their domain focus (customer-facing vs internal/operational accounts).

### Service Structure

Following [ADR-0015](../adr/0015-standard-service-directory-structure.md) (Standard Service Directory Structure):

```text
services/internal-account/
├── cmd/
│   ├── main.go
│   └── Dockerfile
├── domain/
│   ├── internal_account.go       # Account entity (aggregate root)
│   ├── account_type.go           # CLEARING, NOSTRO, VOSTRO, etc.
│   ├── account_status.go         # ACTIVE, SUSPENDED, CLOSED
│   ├── correspondent.go          # Correspondent bank details
│   ├── balance.go                # Balance tracking logic
│   └── repository.go             # Repository interface (port)
├── service/
│   ├── server.go                 # gRPC service implementation
│   └── mappers.go                # Proto <-> Domain mappers
├── adapters/
│   └── persistence/
│       ├── repository.go         # Repository implementation
│       ├── entity.go             # Database entities
│       └── mappers.go            # Entity <-> Domain mappers
├── client/
│   └── client.go                 # gRPC client for other services
├── observability/
│   ├── metrics.go
│   └── health.go
├── atlas/
│   └── atlas.hcl
├── migrations/
│   └── 20260106000001_initial.sql
└── k8s/
    ├── deployment.yaml
    └── service.yaml
```

### Proto Definition

Location: `api/proto/meridian/internal_account/v1/internal_account.proto`

> **Note**: Package uses underscores (`internal_account`) to match existing conventions
> (`current_account`, `financial_accounting`, `reference_data`).

```protobuf
syntax = "proto3";

package meridian.internal_account.v1;

import "buf/validate/validate.proto";
import "google/api/annotations.proto";
import "google/protobuf/timestamp.proto";
import "meridian/common/v1/types.proto";
import "meridian/quantity/v1/quantity.proto";
import "meridian/reference_data/v1/instrument.proto";

option go_package = "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1;internalaccountv1";

// =============================================================================
// Enums
// =============================================================================

// InternalAccountType defines the purpose of the internal account.
// Maps to BIAN Internal Account types.
enum InternalAccountType {
  INTERNAL_ACCOUNT_TYPE_UNSPECIFIED = 0;

  // CLEARING - Temporary holding during transaction settlement
  // Used for deposit/withdrawal clearing before final settlement
  INTERNAL_ACCOUNT_TYPE_CLEARING = 1;

  // NOSTRO - "Ours" - Our money held at another bank
  // Asset account (normal debit balance)
  INTERNAL_ACCOUNT_TYPE_NOSTRO = 2;

  // VOSTRO - "Yours" - Their money held at our bank
  // Liability account (normal credit balance)
  INTERNAL_ACCOUNT_TYPE_VOSTRO = 3;

  // HOLDING - General internal holding account
  // Used for suspense, transit, or operational purposes
  INTERNAL_ACCOUNT_TYPE_HOLDING = 4;

  // SUSPENSE - Unallocated or pending items
  // Items awaiting classification or resolution
  INTERNAL_ACCOUNT_TYPE_SUSPENSE = 5;

  // REVENUE - Fee income, interest income, etc.
  // Credit account for income recognition
  INTERNAL_ACCOUNT_TYPE_REVENUE = 6;

  // EXPENSE - Operating costs, charges, etc.
  // Debit account for expense recognition
  INTERNAL_ACCOUNT_TYPE_EXPENSE = 7;

  // INVENTORY - Asset inventory (for non-fiat assets)
  // Used for energy, compute credits, carbon credits, etc.
  INTERNAL_ACCOUNT_TYPE_INVENTORY = 8;
}

// InternalAccountStatus defines the lifecycle state of the account.
enum InternalAccountStatus {
  INTERNAL_ACCOUNT_STATUS_UNSPECIFIED = 0;
  INTERNAL_ACCOUNT_STATUS_ACTIVE = 1;
  INTERNAL_ACCOUNT_STATUS_SUSPENDED = 2;
  INTERNAL_ACCOUNT_STATUS_CLOSED = 3;
}

// ControlAction defines lifecycle operations.
enum ControlAction {
  CONTROL_ACTION_UNSPECIFIED = 0;
  CONTROL_ACTION_SUSPEND = 1;
  CONTROL_ACTION_ACTIVATE = 2;
  CONTROL_ACTION_CLOSE = 3;
}

// =============================================================================
// Messages
// =============================================================================

// CorrespondentDetails holds information about a correspondent bank relationship.
// Used for NOSTRO and VOSTRO accounts.
message CorrespondentDetails {
  // bank_id is the BIC/SWIFT code or internal identifier
  string bank_id = 1 [(buf.validate.field).string = {
    min_len: 1
    max_len: 50
  }];

  // bank_name is the human-readable name
  string bank_name = 2 [(buf.validate.field).string.max_len = 255];

  // external_account_ref is their reference for our account
  string external_account_ref = 3 [(buf.validate.field).string.max_len = 100];
}

// InternalAccountFacility is the BIAN Control Record for internal accounts.
// This is the aggregate root for the Internal Account domain.
message InternalAccountFacility {
  // account_id is the unique identifier (replaces hardcoded env vars)
  string account_id = 1 [(buf.validate.field).string = {
    min_len: 1
    max_len: 100
    pattern: "^[a-zA-Z0-9_-]+$"
  }];

  // account_code is a human-readable code (e.g., "CLR-GBP-001")
  string account_code = 2 [(buf.validate.field).string = {
    min_len: 1
    max_len: 50
  }];

  // name is the display name
  string name = 3 [(buf.validate.field).string = {
    min_len: 1
    max_len: 255
  }];

  // account_type defines the purpose (CLEARING, NOSTRO, etc.)
  InternalAccountType account_type = 4 [(buf.validate.field).enum = {
    defined_only: true
    not_in: [0]
  }];

  // instrument_code identifies the asset type (e.g., "GBP", "KWH", "GPU_HOUR")
  string instrument_code = 5 [(buf.validate.field).string = {
    min_len: 1
    max_len: 32
  }];

  // dimension classifies the asset (CURRENCY, ENERGY, COMPUTE, etc.)
  // Uses Dimension from reference_data/v1/instrument.proto
  meridian.reference_data.v1.Dimension dimension = 6 [(buf.validate.field).enum = {
    defined_only: true
    not_in: [0]
  }];

  // status is the lifecycle state
  InternalAccountStatus status = 7;

  // correspondent holds correspondent bank details (for NOSTRO/VOSTRO)
  CorrespondentDetails correspondent = 8;

  // NOTE: No balance fields - balance is queried from Position Keeping service
  // Use GetBalance RPC to retrieve current balance

  // attributes holds tenant-specific metadata
  map<string, string> attributes = 9;

  // version for optimistic locking
  int64 version = 10;

  // created_at timestamp
  google.protobuf.Timestamp created_at = 11;

  // updated_at timestamp
  google.protobuf.Timestamp updated_at = 12;
}

// =============================================================================
// Request/Response Messages
// =============================================================================

// InitiateInternalAccountRequest creates a new internal account.
// BIAN: Initiate Control Record (InCR)
message InitiateInternalAccountRequest {
  string account_code = 1 [(buf.validate.field).string = {
    min_len: 1
    max_len: 50
  }];

  string name = 2 [(buf.validate.field).string = {
    min_len: 1
    max_len: 255
  }];

  InternalAccountType account_type = 3 [(buf.validate.field).enum = {
    defined_only: true
    not_in: [0]
  }];

  string instrument_code = 4 [(buf.validate.field).string = {
    min_len: 1
    max_len: 32
  }];

  meridian.reference_data.v1.Dimension dimension = 5 [(buf.validate.field).enum = {
    defined_only: true
    not_in: [0]
  }];

  CorrespondentDetails correspondent = 6;

  map<string, string> attributes = 7;

  meridian.common.v1.IdempotencyKey idempotency_key = 8;
}

message InitiateInternalAccountResponse {
  InternalAccountFacility facility = 1;
}

// UpdateInternalAccountRequest modifies account settings.
// BIAN: Update Control Record (UpCR)
message UpdateInternalAccountRequest {
  string account_id = 1 [(buf.validate.field).string = {
    min_len: 1
    max_len: 100
    pattern: "^[a-zA-Z0-9_-]+$"
  }];

  string name = 2;

  CorrespondentDetails correspondent = 3;

  map<string, string> attributes = 4;
}

message UpdateInternalAccountResponse {
  InternalAccountFacility facility = 1;
}

// ControlInternalAccountRequest performs lifecycle transitions.
// BIAN: Control Control Record (CoCR)
message ControlInternalAccountRequest {
  string account_id = 1 [(buf.validate.field).string = {
    min_len: 1
    max_len: 100
    pattern: "^[a-zA-Z0-9_-]+$"
  }];

  ControlAction action = 2 [(buf.validate.field).enum = {
    defined_only: true
    not_in: [0]
  }];

  string reason = 3 [(buf.validate.field).string.max_len = 1000];
}

message ControlInternalAccountResponse {
  InternalAccountFacility facility = 1;
  google.protobuf.Timestamp action_timestamp = 2;
}

// RetrieveInternalAccountRequest gets an account by ID.
// BIAN: Retrieve Control Record (ReCR)
message RetrieveInternalAccountRequest {
  string account_id = 1 [(buf.validate.field).string = {
    min_len: 1
    max_len: 100
    pattern: "^[a-zA-Z0-9_-]+$"
  }];
}

message RetrieveInternalAccountResponse {
  InternalAccountFacility facility = 1;
}

// ListInternalAccountsRequest queries accounts with filters.
message ListInternalAccountsRequest {
  InternalAccountType account_type = 1;
  string instrument_code = 2;
  meridian.reference_data.v1.Dimension dimension = 3;
  InternalAccountStatus status = 4;
  int32 page_size = 5 [(buf.validate.field).int32 = {gte: 1, lte: 100}];
  string page_token = 6;
}

message ListInternalAccountsResponse {
  repeated InternalAccountFacility facilities = 1;
  string next_page_token = 2;
}

// GetBalanceRequest retrieves the current balance (O(1) operation).
message GetBalanceRequest {
  string account_id = 1 [(buf.validate.field).string = {
    min_len: 1
    max_len: 100
    pattern: "^[a-zA-Z0-9_-]+$"
  }];
}

message GetBalanceResponse {
  string account_id = 1;
  meridian.quantity.v1.InstrumentAmount current_balance = 2;
  google.protobuf.Timestamp balance_updated_at = 3;
}

// NOTE: No RecordPostingRequest/Response - postings go through:
//   FinancialAccounting → Position Keeping
// This service queries Position Keeping for balance, it does not mutate balance.

// =============================================================================
// Service Definition
// =============================================================================

// InternalAccountService provides BIAN-compliant internal account management.
//
// BIAN Service Domain: Internal Account
// Functional Pattern: Fulfill
//
// This service manages accounts that are not customer-facing but are required
// for the bank's internal bookkeeping: clearing accounts, nostro/vostro accounts,
// holding accounts, suspense accounts, and revenue/expense accounts.
//
// Key capabilities:
// - Account registry with full lifecycle management
// - Balance queries via Position Keeping (source of truth)
// - Multi-asset support (fiat, energy, compute, carbon, custom instruments)
service InternalAccountService {
  // InitiateInternalAccount creates a new internal account.
  // BIAN: Initiate Control Record (InCR)
  rpc InitiateInternalAccount(InitiateInternalAccountRequest)
      returns (InitiateInternalAccountResponse) {
    option (google.api.http) = {
      post: "/v1/internal-accounts"
      body: "*"
    };
  }

  // UpdateInternalAccount modifies account settings.
  // BIAN: Update Control Record (UpCR)
  rpc UpdateInternalAccount(UpdateInternalAccountRequest)
      returns (UpdateInternalAccountResponse) {
    option (google.api.http) = {
      put: "/v1/internal-accounts/{account_id}"
      body: "*"
    };
  }

  // ControlInternalAccount performs lifecycle state transitions.
  // BIAN: Control Control Record (CoCR)
  rpc ControlInternalAccount(ControlInternalAccountRequest)
      returns (ControlInternalAccountResponse) {
    option (google.api.http) = {
      post: "/v1/internal-accounts/{account_id}/control"
      body: "*"
    };
  }

  // RetrieveInternalAccount gets account details by ID.
  // BIAN: Retrieve Control Record (ReCR)
  rpc RetrieveInternalAccount(RetrieveInternalAccountRequest)
      returns (RetrieveInternalAccountResponse) {
    option (google.api.http) = {
      get: "/v1/internal-accounts/{account_id}"
    };
  }

  // ListInternalAccounts queries accounts with filters.
  rpc ListInternalAccounts(ListInternalAccountsRequest)
      returns (ListInternalAccountsResponse) {
    option (google.api.http) = {
      get: "/v1/internal-accounts"
    };
  }

  // GetBalance retrieves balance from Position Keeping service.
  // This service does not store balance - Position Keeping is the source of truth.
  rpc GetBalance(GetBalanceRequest) returns (GetBalanceResponse) {
    option (google.api.http) = {
      get: "/v1/internal-accounts/{account_id}/balance"
    };
  }

  // NOTE: No RecordPosting RPC - postings are recorded via:
  //   FinancialAccounting → Position Keeping
  // This service is a registry for account metadata, not a balance mutator.
}
```

### Database Schema

Location: `services/internal-account/migrations/20260106000001_initial.sql`

```sql
-- Internal Account initial schema
-- BIAN Service Domain: Internal Account
-- Manages non-customer-facing accounts for bank operations

-- Create internal_account table (singular, unqualified per ADR-0015)
-- Tenant isolation is at schema level (schema-per-tenant), not via tenant_id column
CREATE TABLE internal_account (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Unique identifier (replaces hardcoded env vars)
    account_id VARCHAR(100) NOT NULL UNIQUE,

    -- Human-readable code (e.g., "CLR-GBP-001")
    account_code VARCHAR(50) NOT NULL,

    -- Display name
    name VARCHAR(255) NOT NULL,

    -- Account type: CLEARING, NOSTRO, VOSTRO, HOLDING, SUSPENSE, REVENUE, EXPENSE, INVENTORY
    account_type VARCHAR(20) NOT NULL CHECK (account_type IN (
        'CLEARING', 'NOSTRO', 'VOSTRO', 'HOLDING',
        'SUSPENSE', 'REVENUE', 'EXPENSE', 'INVENTORY'
    )),

    -- Instrument (asset type)
    instrument_code VARCHAR(32) NOT NULL,

    -- Dimension: CURRENCY, ENERGY, MASS, VOLUME, TIME, COMPUTE, CARBON, DATA, COUNT
    dimension VARCHAR(20) NOT NULL CHECK (dimension IN (
        'CURRENCY', 'ENERGY', 'MASS', 'VOLUME', 'TIME',
        'COMPUTE', 'CARBON', 'DATA', 'COUNT'
    )),

    -- Lifecycle status
    status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE' CHECK (status IN (
        'ACTIVE', 'SUSPENDED', 'CLOSED'
    )),

    -- NOTE: No balance columns - balance is computed by Position Keeping service
    -- See: Position Keeping Balance Ownership PRD (position-keeping-balance tag)

    -- Correspondent bank details (for NOSTRO/VOSTRO)
    correspondent_bank_id VARCHAR(50),
    correspondent_bank_name VARCHAR(255),
    correspondent_external_ref VARCHAR(100),

    -- Flexible metadata
    attributes JSONB NOT NULL DEFAULT '{}',

    -- Optimistic locking
    version INTEGER NOT NULL DEFAULT 1,

    -- Timestamps
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Indexes for common queries
CREATE INDEX idx_internal_account_type ON internal_account(account_type);
CREATE INDEX idx_internal_account_instrument ON internal_account(instrument_code);
CREATE INDEX idx_internal_account_status ON internal_account(status);
CREATE INDEX idx_internal_account_code ON internal_account(account_code);

-- Status history for audit trail
CREATE TABLE internal_account_status_history (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id VARCHAR(100) NOT NULL REFERENCES internal_account(account_id),
    from_status VARCHAR(20) NOT NULL,
    to_status VARCHAR(20) NOT NULL,
    reason TEXT,
    changed_by VARCHAR(100),
    changed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_status_history_account ON internal_account_status_history(account_id);
CREATE INDEX idx_status_history_changed_at ON internal_account_status_history(changed_at);

-- Comments
COMMENT ON TABLE internal_account IS 'BIAN Internal Account - Non-customer-facing accounts for bank operations';
COMMENT ON COLUMN internal_account.account_id IS 'Unique identifier replacing hardcoded environment variables';
COMMENT ON COLUMN internal_account.dimension IS 'Asset dimension from Universal Asset System';
```

### Integration Points

#### 1. Position Keeping Integration (Balance Queries)

Internal Account queries Position Keeping for balance - it does NOT store balance locally:

```go
// In internal-account/service/balance_service.go
func (s *Service) GetBalance(ctx context.Context, req *GetBalanceRequest) (*GetBalanceResponse, error) {
    // 1. Validate account exists and is active
    account, err := s.accountRepo.FindByID(ctx, req.AccountId)
    if err != nil {
        return nil, err
    }
    if account.Status() != StatusActive {
        return nil, ErrAccountNotActive
    }

    // 2. Query Position Keeping for balance (source of truth)
    balances, err := s.positionKeepingClient.GetAccountBalances(ctx, &positionkeeping.GetAccountBalancesRequest{
        AccountId: account.AccountID(),
        Currency:  account.InstrumentCode(),
    })
    if err != nil {
        return nil, fmt.Errorf("failed to get balance from Position Keeping: %w", err)
    }

    // 3. Return balance from Position Keeping
    return &GetBalanceResponse{
        AccountId:        account.AccountID(),
        CurrentBalance:   balances.CurrentBalance,
        AvailableBalance: balances.AvailableBalance,
        AsOf:             balances.AsOf,
    }, nil
}
```

#### 2. FinancialAccounting Flow (Postings)

Postings flow through FinancialAccounting → Position Keeping. Internal Account is NOT involved:

```text
┌─────────────────────┐     ┌─────────────────────┐     ┌─────────────────────┐
│ FinancialAccounting │────►│   Position Keeping  │     │ InternalAccount │
│   (creates posting) │     │ (tracks positions)  │     │  (account registry) │
└─────────────────────┘     └─────────────────────┘     └─────────────────────┘
         │                           │                           │
         │ 1. Create ledger entry    │                           │
         │    (debit/credit)         │                           │
         ├──────────────────────────►│                           │
         │                           │ 2. Record transaction     │
         │                           │    in position log        │
         │                           │                           │
         │                           │◄──────────────────────────┤
         │                           │ 3. Query balance          │
         │                           │    (when needed)          │
         │                           │────────────────────────────►
         │                           │                           │
```

> **Key Insight**: Internal Account is a registry/metadata service.
> It does NOT receive posting notifications or update balance.
> Balance is always computed by Position Keeping from the transaction log.

#### 3. CurrentAccount Migration

Replace hardcoded environment variables with registry lookup:

```go
// Before (current-account/config/accounts.go):
DepositClearingAccountID: os.Getenv("DEPOSIT_CLEARING_ACCOUNT_ID")

// After:
func (s *Service) initializeAccounts(ctx context.Context) error {
    resp, err := s.internalAccountClient.ListInternalAccounts(ctx, &ListRequest{
        AccountType:    INTERNAL_ACCOUNT_TYPE_CLEARING,
        InstrumentCode: "GBP",
        Status:         INTERNAL_ACCOUNT_STATUS_ACTIVE,
    })
    if err != nil || len(resp.Facilities) == 0 {
        return fmt.Errorf("no active GBP clearing account found")
    }
    s.depositClearingAccountID = resp.Facilities[0].AccountId
    return nil
}
```

#### 4. Tenant Provisioning

When a new tenant is provisioned, create default internal accounts:

```go
func (p *SchemaProvisioner) createDefaultAccounts(ctx context.Context, tenantID string) error {
    defaults := []struct {
        Code       string
        Name       string
        Type       InternalAccountType
        Instrument string
        Dimension  Dimension
    }{
        {"CLR-GBP-001", "GBP Deposit Clearing", CLEARING, "GBP", CURRENCY},
        {"CLR-GBP-002", "GBP Withdrawal Clearing", CLEARING, "GBP", CURRENCY},
        {"REV-FEES-001", "Transaction Fee Revenue", REVENUE, "GBP", CURRENCY},
    }

    for _, acct := range defaults {
        _, err := s.client.InitiateInternalAccount(ctx, &InitiateRequest{
            AccountCode:    acct.Code,
            Name:           acct.Name,
            AccountType:    acct.Type,
            InstrumentCode: acct.Instrument,
            Dimension:      acct.Dimension,
        })
        if err != nil {
            return err
        }
    }
    return nil
}
```

---

## Implementation Tasks

### Phase 1: Core Service (P0)

| Task ID | Description | Estimate |
|---------|-------------|----------|
| IBA-001 | Create service skeleton following ADR-0015 structure | 2 |
| IBA-002 | Define proto file with BIAN-aligned messages | 3 |
| IBA-003 | Implement domain model (InternalAccount entity) | 3 |
| IBA-004 | Create database migration | 2 |
| IBA-005 | Implement repository layer | 3 |
| IBA-006 | Implement service layer with BIAN operations | 5 |
| IBA-007 | Implement gRPC handler | 3 |
| IBA-008 | Add observability (metrics, health checks) | 2 |
| IBA-009 | Write unit tests (80% coverage) | 5 |
| IBA-010 | Write integration tests | 3 |

### Phase 2: Integration (P0)

| Task ID | Description | Estimate |
|---------|-------------|----------|
| IBA-011 | Create gRPC client package | 2 |
| IBA-012 | Integrate with FinancialAccounting (RecordPosting) | 3 |
| IBA-013 | Migrate CurrentAccount from env vars to registry | 3 |
| IBA-014 | Add to tenant provisioning (default accounts) | 3 |
| IBA-015 | Update Kubernetes manifests | 2 |
| IBA-016 | Add to Tilt local development | 1 |

### Phase 3: Multi-Asset Support (P0)

| Task ID | Description | Estimate |
|---------|-------------|----------|
| IBA-017 | Validate instrument_code against Reference Data | 2 |
| IBA-018 | Add energy account examples (KWH) | 2 |
| IBA-019 | Add compute account examples (GPU_HOUR) | 2 |
| IBA-020 | Add utilization metering integration | 3 |

### Phase 4: Correspondent Banking (P1)

| Task ID | Description | Estimate |
|---------|-------------|----------|
| IBA-021 | Implement correspondent details for NOSTRO | 2 |
| IBA-022 | Implement correspondent details for VOSTRO | 2 |
| IBA-023 | Add correspondent bank search/filter | 2 |

### Phase 5: Documentation & ADR (P0)

| Task ID | Description | Estimate |
|---------|-------------|----------|
| IBA-024 | Write ADR-0023 for Internal Account | 2 |
| IBA-025 | Update architecture diagrams | 2 |
| IBA-026 | Create runbook for account management | 2 |

---

## Success Criteria

### Functional Success

- [ ] All BIAN operations implemented (Initiate, Update, Control, Retrieve, List)
- [ ] Balance queries are O(1) (< 5ms p99)
- [ ] Account lifecycle transitions work correctly
- [ ] Integration with FinancialAccounting updates balances on every posting
- [ ] Multi-asset accounts work (currency, energy, compute, etc.)

### Technical Success

- [ ] 80% unit test coverage
- [ ] All integration tests passing
- [ ] Service follows ADR-0015 directory structure
- [ ] Proto follows existing conventions (buf lint passes)
- [ ] Database migration works in multi-tenant schema

### Business Success

- [ ] No more hardcoded account IDs in environment variables
- [ ] Can query "What's our GBP clearing balance?" in < 5ms
- [ ] Tenant provisioning creates default accounts automatically
- [ ] Meridian can track its own billing accounts (tenant-zero)

---

## Appendix A: Use Case Examples

### A.1: Energy Trading Tenant

```text
Tenant: uk_energy_retailer

Internal Accounts:
+---------------------+-----------+------------+-----------+---------+
| Account ID          | Type      | Instrument | Dimension | Balance |
+---------------------+-----------+------------+-----------+---------+
| GRID-PURCHASE-KWH   | INVENTORY | KWH        | ENERGY    | 50,000  |
| CUSTOMER-USAGE-KWH  | HOLDING   | KWH        | ENERGY    | 12,000  |
| IMBALANCE-KWH       | SUSPENSE  | KWH        | ENERGY    | -500    |
| SETTLEMENT-GBP      | CLEARING  | GBP        | CURRENCY  | 25,000  |
| REVENUE-ENERGY      | REVENUE   | GBP        | CURRENCY  | 150,000 |
+---------------------+-----------+------------+-----------+---------+

Customer uses 100 KWH:
  DEBIT:  CUSTOMER-USAGE-KWH    100 KWH  (reduce liability)
  CREDIT: GRID-PURCHASE-KWH    100 KWH  (reduce inventory)
```

### A.2: Meridian Platform Billing

```text
Tenant: meridian (platform tenant)

Internal Accounts:
+---------------------+-----------+--------------+-----------+---------+
| Account ID          | Type      | Instrument   | Dimension | Balance |
+---------------------+-----------+--------------+-----------+---------+
| AR-TENANT-FEES      | REVENUE   | GBP          | CURRENCY  | 50,000  |
| AR-API-USAGE        | REVENUE   | GBP          | CURRENCY  | 12,000  |
| USAGE-TRANSACTIONS  | INVENTORY | TRANSACTION  | COUNT     | 500,000 |
| USAGE-API-CALLS     | INVENTORY | API_CALL     | COUNT     | 2.5M    |
| SETTLEMENT-CLEARING | CLEARING  | GBP          | CURRENCY  | 0       |
+---------------------+-----------+--------------+-----------+---------+

Tenant A processes 1,000 transactions:
  // Usage metering:
  DEBIT:  USAGE-TRANSACTIONS      1,000 TRANSACTION
  CREDIT: [Tenant A usage position]

  // Monthly billing (convert to revenue):
  DEBIT:  AR-TENANT-FEES          £50 GBP
  CREDIT: REVENUE-TRANSACTIONS    £50 GBP
```

### A.3: Cross-Tenant Settlement

```text
When Meridian bills Tenant A:

Tenant A's Ledger (org_acme_bank):
  DEBIT:  FEES-PAYABLE         £50 GBP  (Tenant A's liability)
  CREDIT: SETTLEMENT-CLEARING  £50 GBP  (Clearing account)

Meridian's Ledger (org_meridian):
  DEBIT:  SETTLEMENT-CLEARING  £50 GBP  (Clearing account)
  CREDIT: AR-TENANT-FEES       £50 GBP  (Meridian's receivable)

Each ledger stays balanced. External bank transfer settles the clearing accounts.
```

---

## Appendix B: BIAN Semantic API Reference

The implementation should align with the BIAN v14.0 Internal Bank Account semantic API.

BIAN Semantic API specification:
[InternalBankAccount.yaml](https://github.com/bian-official/public/blob/main/release14.0.0/semantic-apis/oas3%20/yamls/InternalBankAccount.yaml)

Key BIAN operations from the spec:

- **Initiate** - Create new internal account
- **Update** - Modify account settings
- **Control** - Lifecycle state transitions
- **Retrieve** - Get account by ID
- **Capture** - Record activity against the account (maps to RecordPosting)
- **Notify** - Subscribe to account events (future work)

<!-- End of PRD -->
