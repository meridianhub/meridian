---
name: party-service
description: BIAN party reference data directory for customer and counterparty identity management
triggers:
  - Implementing party registration
  - Managing party lifecycle (status transitions)
  - External reference validation (Companies House, LEI, Tax ID)
  - Building gRPC party reference services
  - Database schema design for party entities
  - Optimistic locking patterns
instructions: |
  Party service manages customer and counterparty identities with external reference validation.

  Key patterns:
  - PartyType: PERSON (individual) or ORGANIZATION (legal entity)
  - PartyStatus: ACTIVE → RESTRICTED → TERMINATED (state machine)
  - External references: Write-once, unique per type, format-validated
  - Optimistic locking via version field

  External reference validation (regex):
  - COMPANIES_HOUSE: ^[A-Z]{0,2}\d{6,8}$
  - LEI: ^[A-Z0-9]{20}$
  - NATIONAL_ID: ^[A-Z0-9]{5,20}$
  - TAX_ID: ^[A-Z0-9]{5,20}$

  Port: 50055 (gRPC)
---

# Party Service

BIAN-compliant party reference data directory for managing customer and counterparty identities.

## Overview

| Attribute | Value |
|-----------|-------|
| **BIAN Domain** | Party Reference Data Directory |
| **Port** | 50055 (gRPC) |
| **Language** | Go |
| **Database** | PostgreSQL/CockroachDB |
| **Standalone** | Yes |

## gRPC Methods

| Method | HTTP | Purpose |
|--------|------|---------|
| `RegisterParty` | `POST /v1/parties` | Create new party |
| `RetrieveParty` | `GET /v1/parties/{party_id}` | Get party details |

## Domain Model

```mermaid
classDiagram
    class Party {
        +UUID ID
        +PartyType PartyType
        +string LegalName
        +string DisplayName
        +PartyStatus Status
        +string ExternalReference
        +ExternalReferenceType ExternalReferenceType
        +int64 Version
        +Time CreatedAt
        +Time UpdatedAt
    }

    class PartyType {
        <<enumeration>>
        PERSON
        ORGANIZATION
    }

    class PartyStatus {
        <<enumeration>>
        ACTIVE
        RESTRICTED
        TERMINATED
    }

    class ExternalReferenceType {
        <<enumeration>>
        COMPANIES_HOUSE
        NATIONAL_ID
        LEI
        TAX_ID
    }

    Party --> PartyType
    Party --> PartyStatus
    Party --> ExternalReferenceType
```

**Party Types:**

| Type | Description |
|------|-------------|
| `PERSON` | Natural person (individual) |
| `ORGANIZATION` | Legal entity (company, partnership) |

**Party Status:**

| Status | Description |
|--------|-------------|
| `ACTIVE` | Can participate in banking operations |
| `RESTRICTED` | Limited access (e.g., pending KYC) |
| `TERMINATED` | Relationship ended (terminal) |

**Status Transitions:**

```mermaid
stateDiagram-v2
    [*] --> ACTIVE
    ACTIVE --> RESTRICTED
    ACTIVE --> TERMINATED
    RESTRICTED --> ACTIVE
    RESTRICTED --> TERMINATED
    TERMINATED --> [*]
```

- TERMINATED is terminal (cannot be reactivated)
- RESTRICTED can return to ACTIVE

## External Reference Validation

External references are write-once and unique per type:

| Type | Pattern | Example |
|------|---------|---------|
| `COMPANIES_HOUSE` | `^[A-Z]{0,2}\d{6,8}$` | `12345678`, `GB12345678` |
| `LEI` | `^[A-Z0-9]{20}$` | `5493001KJTIIGC8K1K12` |
| `NATIONAL_ID` | `^[A-Z0-9]{5,20}$` | `AB12345` |
| `TAX_ID` | `^[A-Z0-9]{5,20}$` | `TB123456789` |

**Note:** Validation is format-only (regex pattern matching). LEI checksum (ISO 17442 MOD 97-10)
and Companies House registry lookups are not performed. External validation should be done
upstream before registration if required.

## Database Schema

**Schema**: `party`

```mermaid
erDiagram
    parties {
        uuid id PK
        varchar(20) party_type "PERSON, ORGANIZATION"
        varchar(255) legal_name
        varchar(255) display_name "nullable"
        varchar(20) status "ACTIVE, RESTRICTED, TERMINATED"
        varchar(100) external_reference "nullable, write-once"
        varchar(30) external_reference_type "nullable"
        bigint version "optimistic lock"
        timestamptz created_at
        timestamptz updated_at
        timestamptz deleted_at "nullable, soft-delete"
    }
```

**Indexes:**

- `idx_parties_party_type`: Query by type
- `idx_parties_status`: Query by status
- `idx_party_external_ref`: Unique on (reference, type) where `deleted_at IS NULL`
- `idx_party_parties_deleted_at`: Query active (non-deleted) records

## Configuration

| Variable | Default | Purpose |
|----------|---------|---------|
| `GRPC_PORT` | 50055 | gRPC server port |
| `DATABASE_URL` | - | PostgreSQL connection string |
| `DB_MAX_OPEN_CONNS` | 25 | Connection pool size |

## Key Patterns

### Registration Flow

1. Validate party_type (PERSON or ORGANIZATION)
2. Create Party aggregate with ACTIVE status
3. If external_reference provided:
   - Check uniqueness
   - Validate format against type-specific regex
   - Set reference (write-once)
4. Persist with version = 1

### Optimistic Locking

Updates check `WHERE version = expected_version`. Returns conflict error on mismatch.

## References

- [BIAN Party Reference Data Directory Specification](https://github.com/bian-official/public/blob/main/release14.0.0/semantic-apis/oas3%20/yamls/PartyReferenceDataDirectory.yaml)
- [Service Architecture](../README.md)
- [Proto Definitions](../../api/proto/meridian/party/v1/)
