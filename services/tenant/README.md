---
name: tenant-service
description: Multi-tenant platform management with PostgreSQL schema-per-tenant isolation
triggers:
  - Implementing multi-tenancy patterns
  - Managing tenant lifecycle (active, suspended, deprovisioned)
  - Schema isolation for data segregation
  - Building tenant registry services
  - Platform infrastructure for shared clusters
instructions: |
  Tenant service manages platform multi-tenancy (NOT a BIAN component).

  Key patterns:
  - Schema isolation: Each tenant gets org_{tenant_id} PostgreSQL schema
  - Status lifecycle: ACTIVE → SUSPENDED → DEPROVISIONED (terminal)
  - Tenant ID: Alphanumeric + underscore, 1-50 chars
  - Optimistic locking via version field
  - Cached registry with async refresh (60s interval)

  Note: Distinct from BIAN Party.Organization which represents legal entities.

  Port: 50056 (gRPC)
---

# Tenant Service

Platform infrastructure service for multi-tenant management with PostgreSQL schema isolation.

## Overview

| Attribute | Value |
|-----------|-------|
| **Type** | Infrastructure (not BIAN) |
| **Port** | 50056 (gRPC) |
| **Language** | Go |
| **Database** | PostgreSQL/CockroachDB |
| **Standalone** | Yes |

**Note**: This service is not part of the BIAN standard. It provides essential multi-tenancy
infrastructure for shared-cluster deployments requiring data isolation between organizations.

## gRPC Methods

| Method | HTTP | Purpose |
|--------|------|---------|
| `InitiateTenant` | `POST /v1/tenants` | Create new tenant |
| `RetrieveTenant` | `GET /v1/tenants/{tenant_id}` | Get tenant details |
| `UpdateTenantStatus` | `PATCH /v1/tenants/{tenant_id}/status` | Change lifecycle status |
| `ListTenants` | `GET /v1/tenants` | List with filters |

## Domain Model

### Tenant

```text
Tenant {
  ID: OrganizationID (alphanumeric + underscore, 1-50 chars)
  DisplayName: string (1-255 chars)
  SettlementAsset: string (e.g., GBP, USD, GPU-HOUR)
  Subdomain: string (optional, unique)
  Status: ACTIVE | SUSPENDED | DEPROVISIONED
  CreatedAt: time.Time
  DeprovisionedAt: *time.Time
  Metadata: map[string]interface{} (JSONB)
  Version: int
}
```

### Tenant Status

| Status | Description |
|--------|-------------|
| `ACTIVE` | Tenant is operational |
| `SUSPENDED` | Temporarily disabled (recoverable) |
| `DEPROVISIONED` | Terminal state (no operations) |

**Status Transitions:**

```text
ACTIVE ──→ SUSPENDED ──→ DEPROVISIONED
   │                          ↑
   └──────────────────────────┘
```

- DEPROVISIONED is terminal (cannot be reactivated)
- SUSPENDED can return to ACTIVE

## Schema Isolation

Each tenant's data is isolated in a dedicated PostgreSQL schema:

```text
org_{tenant_id}
```

Example: Tenant `acme_bank` → Schema `org_acme_bank`

The tenant registry itself is stored in the shared `platform` schema.

## Database Schema

**Schema**: `platform`

### tenants Table

| Column | Type | Purpose |
|--------|------|---------|
| `id` | VARCHAR(50) | Primary key (tenant ID) |
| `display_name` | VARCHAR(255) | Human-readable name |
| `settlement_asset` | VARCHAR(20) | Primary currency/asset |
| `subdomain` | VARCHAR(255) | API subdomain (unique) |
| `status` | VARCHAR(20) | Lifecycle state |
| `created_at` | TIMESTAMPTZ | Registration time |
| `deprovisioned_at` | TIMESTAMPTZ | Deprovisioning time |
| `metadata` | JSONB | Flexible configuration |
| `version` | INTEGER | Optimistic locking |

**Constraints:**

- `valid_status`: status IN ('active', 'suspended', 'deprovisioned')
- `valid_org_id`: id matches `^[a-zA-Z0-9_]{1,50}$`
- Subdomain unique when not null

## Cached Registry

In-memory tenant cache for validation middleware:

| Setting | Value |
|---------|-------|
| Refresh interval | 60 seconds |
| Per-refresh timeout | 30 seconds |
| Strategy | Fail-open (uses stale cache if refresh fails) |

## Configuration

| Variable | Default | Purpose |
|----------|---------|---------|
| `GRPC_PORT` | 50056 | gRPC server port |
| `DATABASE_URL` | - | PostgreSQL connection string |
| `DB_MAX_OPEN_CONNS` | 25 | Connection pool size |
| `DB_MAX_IDLE_CONNS` | 5 | Idle connections |
| `DB_CONN_MAX_LIFETIME` | 5m | Connection max age |

## Key Patterns

### Tenant ID Validation

Must match: `^[a-zA-Z0-9_]{1,50}$`

Used for:

- PostgreSQL schema routing (`org_{id}`)
- API subdomain routing

### No Delete Operations

Tenants are managed through status transitions, not deletion:

- Audit compliance (full history preserved)
- Recoverable: suspended can be reactivated
- Data integrity: no cascade delete complexities

### Optimistic Locking

Updates check `WHERE version = expected_version`. Returns conflict error on mismatch.

## Kubernetes Deployment

| Setting | Value |
|---------|-------|
| Replicas | 2 |
| CPU | 50m-200m |
| Memory | 64Mi-256Mi |
| User | 65532 (non-root) |
| Filesystem | Read-only |

## References

- [Service Architecture](../README.md)
- [Proto Definitions](../../api/proto/meridian/tenant/v1/)
- [ADR-0002: Microservices per BIAN Domain](../../docs/adr/0002-microservices-per-bian-domain.md)
