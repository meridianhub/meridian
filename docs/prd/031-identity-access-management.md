---
name: prd-identity-access-management
description: >-
  Standalone Identity service (BIAN Employee Access) for staff/operator
  authentication, role assignment, and multi-level access control.
  Dex as sole OIDC provider across all environments.
triggers:
  - Working on authentication, authorization, or access control
  - Implementing user management, invitation flows, or role assignment
  - Creating or modifying the Identity service
  - Configuring Dex OIDC, JWT claims, or token issuance
  - Working on platform admin bootstrap or tenant owner onboarding
  - Modifying DEFAULT_ROLES, DEFAULT_TENANT_ID, or auth interceptors
instructions: |
  Meridian separates access control into two distinct services, following
  BIAN's explicit guidance that employee and customer access are different
  service domains:

  1. Customer Access Control (Party Authentication)
     - Handled by: Party service (ExchangeDemographics RPC, verification adapters)
     - PRD: 020-party-kyc-aml-provider-integration
     - Purpose: Verify customer identity for regulatory compliance
     - BIAN: Party Authentication service domain

  2. Staff/Operator Access Control (Employee Access)
     - Handled by: Identity service (this PRD 031)
     - Purpose: Authenticate staff, assign roles, populate JWT claims
     - Dex issues JWTs; Identity service owns user CRUD and credentials
     - BIAN: Employee Access service domain

  Key files:
  - services/identity/ (NEW — Identity + RoleAssignment)
  - shared/pkg/credentials/ (NEW — shared password hashing, validation)
  - shared/platform/auth/rbac.go (existing RBAC framework)
  - shared/platform/auth/grpc_interceptor.go (JWT validation + tenant context)
  - shared/platform/auth/jwt.go (Claims structure)
  - services/gateway/auth/ (HTTP auth middleware)
  - deploy/demo/dex.yaml (static users to be replaced)

  Identity and RoleAssignment tables live in the Identity service's own
  database (cross-tenant, like the Tenant service). Dex is the sole
  OIDC provider (no Keycloak).
---

# PRD 031: Identity and Access Management

**Status:** Not Started
**Version:** 3.0
**Date:** 2026-03-02
**Author:** Architecture Team
**Task Master Tag:** TBD

**ADRs:**

- [0002 - Microservices Per BIAN Domain](../adr/0002-microservices-per-bian-domain.md)
- [0015 - Standard Service Directory Structure](../adr/0015-standard-service-directory-structure.md)

**Related PRDs:**

<!-- markdownlint-disable MD013 -->

- [020 - Party KYC/AML Provider Integration](020-party-kyc-aml-provider-integration.md) —
  Customer-facing identity verification (BIAN: Party Authentication)

<!-- markdownlint-enable MD013 -->

---

## The Two Access Control Concerns

BIAN explicitly separates employee and customer access control into
different service domains. Meridian follows this guidance.

> "A similar type of facility is used for customer access control but
> as the profile differs, here being for internal applications as opposed
> to externally visible bank products and services, **the two functions
> are captured as different service domains**."
> — BIAN Employee Access service domain definition

### Concern 1: Customer Access Control (Party Authentication)

**Question answered:** "Is this person who they claim to be?"

<!-- markdownlint-disable MD013 -->

| Aspect | Detail |
|--------|--------|
| **Service** | Party service (existing) |
| **BIAN domain** | Party Authentication |
| **PRD** | 020 — Party KYC/AML Provider Integration |
| **Purpose** | Regulatory identity verification for customers |
| **Scope** | Tenant-scoped — each tenant onboards their own customers |
| **Data location** | Tenant schema (`org_{tenant_id}`) |

<!-- markdownlint-enable MD013 -->

### Concern 2: Staff/Operator Access Control (Employee Access)

**Question answered:** "Can this person log in, and what can they do?"

<!-- markdownlint-disable MD013 -->

| Aspect | Detail |
|--------|--------|
| **Service** | Identity service (NEW — this PRD) |
| **BIAN domain** | Employee Access |
| **PRD** | 031 — Identity and Access Management |
| **Purpose** | Authenticate staff/operators, assign roles, populate JWT claims |
| **Scope** | Cross-tenant — a user may have roles in multiple tenants |
| **Data location** | Identity service database (cross-tenant) |

<!-- markdownlint-enable MD013 -->

### Why Two Services

A customer logging in to view their account balance and a platform admin
applying a manifest are categorically different operations:

- **Different security profiles** — customer auth is regulatory
  (KYC/AML), staff auth is operational (RBAC)
- **Different lifecycle** — customers are onboarded per-tenant, staff
  may span tenants
- **Different audit requirements** — customer identity is PII under
  GDPR, staff access is SOC2/operational audit
- **Different schemas** — customer data is tenant-isolated, staff
  identity is cross-tenant

The fact that both need "a login" doesn't make them the same domain.

### Shared Libraries

Common primitives live in shared packages, used by both services:

```text
shared/pkg/credentials/    — password hashing, validation, history
shared/pkg/tokens/         — token generation, hashing, expiry
shared/platform/auth/      — RBAC, JWT claims, interceptors (existing)
```

This avoids duplication while preserving domain separation. If the
Party service later needs customer login credentials (Phase 4+), it
imports the same shared packages.

---

## Problem Statement

Meridian has two halves of an access control system that don't connect:

**What exists and works well:**

- JWT validation, JWKS key rotation, gRPC/HTTP interceptors
- RBAC framework (admin, operator, auditor, service roles + permission
  matrix)
- Schema-based multi-tenant isolation with subdomain-hopping prevention
- Party service with Organization, Individual, KYC verification,
  references
- Tenant service with provisioning, lifecycle, Party.Organization linkage
- Platform admin vs tenant admin interceptor separation

**What's missing:**

- No dynamic user management (Dex has 2 hard-coded users)
- No way to create, invite, or onboard staff/operators
- No storage for "which user has which roles in which tenant"
- No self-service: tenants can't manage their own staff
- Roles come from JWT claims, but nothing populates those claims
  dynamically

**The result:** A fully-featured authorization enforcement layer with no
way to actually manage who is authorized.

## Access Control Levels

Meridian requires four distinct access tiers:

<!-- markdownlint-disable MD013 -->

| Level | Name | Scope | Who | Role |
|-------|------|-------|-----|------|
| 0 | Platform Administrator | `meridian_master` | Meridian operators | `platform-admin`, `super-admin` |
| 1 | Tenant Owner | Single tenant | Organization that contracted Meridian | `tenant-owner` (new) |
| 2 | Tenant Administrator | Single tenant | Staff appointed by tenant owner | `admin` |
| 3 | Tenant Operator | Single tenant, restricted | Staff with scoped access | `operator`, `auditor`, custom |

<!-- markdownlint-enable MD013 -->

**Level 0** creates/suspends tenants, applies platform manifests,
views cross-tenant metrics.
**Level 1** manages tenant admins, views billing, applies tenant
manifests.
**Level 2** manages users, configures account types, runs operations.
**Level 3** accesses scoped resources per assigned role (e.g., view
accounts, run reports). Customer access is handled separately by
PRD 020.

## Architecture

### OIDC Provider: Dex Everywhere

**Decision:** Dex is the sole OIDC provider across all environments.
Keycloak is removed entirely.

<!-- markdownlint-disable MD013 -->

| Responsibility | Owner |
|---------------|-------|
| User CRUD (create, invite, suspend) | Identity service |
| Password storage + validation | Identity service (via `shared/pkg/credentials`) |
| Role assignment | Identity service (RoleAssignment table) |
| JWT issuance + signing | Dex |
| JWKS endpoint + key rotation | Dex |
| Federation (upstream IdPs) | Dex connectors |
| Customer identity verification | Party service (unchanged) |

<!-- markdownlint-enable MD013 -->

**Why not Keycloak?** Once Identity management lives in a dedicated
Meridian service, Keycloak becomes a ~500MB middleman duplicating what
Meridian already owns. Dex does the one thing needed externally — issue
and sign JWTs — at ~20MB. Dex is also what Kubernetes itself uses for
OIDC.

**Environment parity:**

| Environment | OIDC Provider | Auth Enabled | Users |
|-------------|--------------|:---:|-------|
| Local (Tilt) | Dex | Yes | Dev users from dex.yaml |
| Demo | Dex | Yes | Bootstrap from env vars |
| Production | Dex | Yes | Dynamic via Identity service |

### Current Authentication Flow (Demo)

```text
User → Dex (static password check) → JWT (sub, email, name)
  → Gateway adds DEFAULT_TENANT_ID + DEFAULT_ROLES
  → Interceptor injects claims into context
  → RBAC enforcement checks role+resource+permission
```

**Problem:** `DEFAULT_TENANT_ID` and `DEFAULT_ROLES` are env vars.
Every user gets the same tenant and same roles.

### Target Authentication Flow

```text
User → Dex (gRPC connector → Identity service)
  → Identity validates credentials, looks up RoleAssignments
  → Dex issues JWT (sub, email, x-tenant-id, roles)
  → Gateway validates JWT (no defaults needed)
  → Interceptor injects claims into context
  → RBAC enforcement (unchanged)
```

For production with external IdPs:

```text
User → Dex (upstream connector → Auth0/Okta/Google)
  → Dex calls Identity service to enrich claims
  → JWT (sub, email, x-tenant-id, roles)
  → Gateway validates JWT
  → RBAC enforcement (unchanged)
```

No separate Token Exchange service needed — Dex's connector model
handles federation natively.

### BIAN Alignment

BIAN explicitly separates these into distinct service domains:

<!-- markdownlint-disable MD013 -->

| Meridian Concept | BIAN Service Domain | BIAN Mapping |
|-----------------|---------------------|--------------|
| Identity service (this PRD) | Employee Access | Access Profile Management |
| Identity (login credentials) | Employee Access | Employee Authentication |
| RoleAssignment | Employee Access | Access Right / Entitlement |
| Party service (PRD 020) | Party Authentication | Customer Identity Verification |

<!-- markdownlint-enable MD013 -->

## Service Structure

The Identity service follows
[ADR-015](../adr/0015-standard-service-directory-structure.md) and the
[New BIAN Service Checklist](../guides/new-bian-service-checklist.md).
Use `services/party/` as the primary reference implementation.

```text
services/identity/
  ├── cmd/                  # Service entrypoint
  ├── domain/               # Identity, RoleAssignment, Invitation entities
  ├── adapters/persistence/ # Repository implementations
  ├── service/              # Business logic
  ├── grpc/                 # gRPC handlers
  ├── connector/            # Dex gRPC connector implementation
  ├── observability/        # Metrics, health checks
  ├── migrations/           # Atlas migrations (own database)
  ├── atlas/                # Atlas config
  └── k8s/                  # Kubernetes manifests
```

The Identity service has its own database (same pattern as Tenant,
Party, and other services). Identity data is inherently cross-tenant,
so it does not use per-tenant schemas.

## Core Features

### 1. Identity — Staff Authentication Record

An Identity represents a staff member or operator who can authenticate
to Meridian.

```go
type Identity struct {
    ID             uuid.UUID
    Email          string          // Global login identifier
    PasswordHash   string          // bcrypt (via shared/pkg/credentials)
    ExternalIDPSub string          // Subject from external IdP
    ExternalIDP    string          // "google", "auth0", "okta"
    Status         IdentityStatus  // ACTIVE, PENDING_INVITE, etc.
    LastLoginAt    *time.Time
    FailedAttempts int
    LockedUntil    *time.Time
    MFAEnabled     bool
    CreatedAt      time.Time
    UpdatedAt      time.Time
    Version        int
}
```

**Constraints:**

- Email is globally unique (one identity per person)
- Password-based and federated auth are mutually exclusive per identity
- Identity exists independently of Party.Individual (different domain)

### 2. RoleAssignment — Dynamic Authorization

Stores which roles a user has within which tenant. Replaces the static
`DEFAULT_ROLES` mechanism.

```go
type RoleAssignment struct {
    ID         uuid.UUID
    IdentityID uuid.UUID
    TenantID   tenant.TenantID
    Role       auth.Role       // admin, operator, auditor, tenant-owner
    GrantedBy  uuid.UUID
    GrantedAt  time.Time
    ExpiresAt  *time.Time      // Optional time-bound roles
    Revoked    bool
    RevokedAt  *time.Time
    RevokedBy  *uuid.UUID
}
```

**Role hierarchy (who can grant what):**

```text
platform-admin → can grant: tenant-owner, admin, operator, auditor
tenant-owner   → can grant: admin, operator, auditor
admin          → can grant: operator, auditor
operator       → can grant: (nothing)
auditor        → can grant: (nothing)
```

Role changes take effect at next token refresh (not mid-session).

### 3. Dex gRPC Connector — Claims Population

When a user authenticates, Dex calls the Identity service's gRPC
connector to validate credentials and populate JWT claims:

```text
Dex receives login request (email + password)
  → Calls Identity service Authenticate RPC
  → Identity validates password hash
  → Identity looks up RoleAssignments for this identity
  → Returns: sub, email, name, x-tenant-id, roles[]
  → Dex signs JWT with these claims
```

For federated auth (Phase 3), Dex's upstream connectors handle the
external IdP interaction, then call the Identity service to enrich
claims with tenant and role information.

### 4. User Lifecycle Operations

**Invitation:** `InviteUser(email, role)` creates a PENDING_INVITE
identity with a time-limited token. User sets password or links IdP
on accept.

**Password management:** Self-service change, admin reset,
forgot-password flow with time-limited reset tokens delivered via
webhook.

**Lifecycle:** Suspend (disable login), reactivate, deprovision
(soft-delete with PII anonymization after retention).

### 5. Multi-Tenant User Resolution

A user with roles in multiple tenants selects their tenant at login
time. Single-tenant users are auto-selected. Tenant switching issues
a new JWT without re-authentication.

### 6. Platform Admin Bootstrap

On first boot, Meridian creates a platform admin identity from
`PLATFORM_ADMIN_EMAIL` and `PLATFORM_ADMIN_PASSWORD` env vars.
Bootstrap credentials are one-time use: the first login forces an
immediate password reset, after which the env var value is no longer
valid. In production, inject these via a secret manager (e.g.,
Kubernetes Secrets, Vault) — never store plaintext passwords in
environment files or container images. Subsequent admins are created
via the invitation flow.

## Database Schema

The Identity service owns its own database. Identity data is
inherently cross-tenant (a user may have roles in multiple tenants),
so tables do not use per-tenant schemas.

```sql
CREATE TABLE identity (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email           VARCHAR(255) NOT NULL UNIQUE,
    password_hash   VARCHAR(255),
    external_idp    VARCHAR(50),
    external_idp_sub VARCHAR(255),
    status          VARCHAR(20) NOT NULL DEFAULT 'PENDING_INVITE',
    last_login_at   TIMESTAMPTZ,
    failed_attempts INT NOT NULL DEFAULT 0,
    locked_until    TIMESTAMPTZ,
    mfa_enabled     BOOLEAN NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    version         INT NOT NULL DEFAULT 1,

    UNIQUE (external_idp, external_idp_sub)
      WHERE external_idp IS NOT NULL
);

CREATE TABLE role_assignment (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    identity_id  UUID NOT NULL REFERENCES identity(id),
    tenant_id    VARCHAR(50) NOT NULL,
    role         VARCHAR(30) NOT NULL,
    granted_by   UUID NOT NULL REFERENCES identity(id),
    granted_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ,
    revoked      BOOLEAN NOT NULL DEFAULT false,
    revoked_at   TIMESTAMPTZ,
    revoked_by   UUID REFERENCES identity(id),

    UNIQUE (identity_id, tenant_id, role)
      WHERE revoked = false
);

CREATE TABLE invitation (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    identity_id  UUID NOT NULL REFERENCES identity(id),
    token_hash   VARCHAR(255) NOT NULL UNIQUE,
    invited_by   UUID NOT NULL REFERENCES identity(id),
    expires_at   TIMESTAMPTZ NOT NULL,
    accepted_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

**Key simplifications vs v2.0:**

- `email` is globally unique (not per-tenant) — one identity per person
- No `tenant_id` on Identity — tenant scoping lives in RoleAssignment
- No `party_id` — Identity and Party are separate domains

## Proto Definition

New `IdentityService` proto at `api/proto/meridian/identity/v1/`:

```protobuf
service IdentityService {
  // Identity CRUD
  rpc CreateIdentity(CreateIdentityRequest)
      returns (CreateIdentityResponse);
  rpc RetrieveIdentity(RetrieveIdentityRequest)
      returns (RetrieveIdentityResponse);
  rpc UpdateIdentity(UpdateIdentityRequest)
      returns (UpdateIdentityResponse);
  rpc ListIdentities(ListIdentitiesRequest)
      returns (ListIdentitiesResponse);

  // Authentication (called by Dex gRPC connector)
  rpc Authenticate(AuthenticateRequest)
      returns (AuthenticateResponse);
  rpc RefreshToken(RefreshTokenRequest)
      returns (RefreshTokenResponse);

  // Password management
  rpc SetPassword(SetPasswordRequest)
      returns (SetPasswordResponse);
  rpc ChangePassword(ChangePasswordRequest)
      returns (ChangePasswordResponse);
  rpc RequestPasswordReset(RequestPasswordResetRequest)
      returns (RequestPasswordResetResponse);
  rpc CompletePasswordReset(CompletePasswordResetRequest)
      returns (CompletePasswordResetResponse);

  // Role management
  rpc GrantRole(GrantRoleRequest)
      returns (GrantRoleResponse);
  rpc RevokeRole(RevokeRoleRequest)
      returns (RevokeRoleResponse);
  rpc ListRoleAssignments(ListRoleAssignmentsRequest)
      returns (ListRoleAssignmentsResponse);

  // Invitation
  rpc InviteUser(InviteUserRequest)
      returns (InviteUserResponse);
  rpc AcceptInvitation(AcceptInvitationRequest)
      returns (AcceptInvitationResponse);

  // Tenant resolution
  rpc ListUserTenants(ListUserTenantsRequest)
      returns (ListUserTenantsResponse);
  rpc SwitchTenant(SwitchTenantRequest)
      returns (SwitchTenantResponse);

  // Lifecycle
  rpc SuspendIdentity(SuspendIdentityRequest)
      returns (SuspendIdentityResponse);
  rpc ReactivateIdentity(ReactivateIdentityRequest)
      returns (ReactivateIdentityResponse);
}
```

## Shared Libraries

Common auth primitives extracted to shared packages so both the
Identity service and Party service (for future customer auth) can
use them without coupling:

<!-- markdownlint-disable MD013 -->

| Package | Purpose | Used by |
|---------|---------|---------|
| `shared/pkg/credentials` | Password hashing (bcrypt), validation, history checking | Identity service, future Party customer auth |
| `shared/pkg/tokens` | Token generation, hashing, expiry validation | Identity service, future Party customer auth |
| `shared/platform/auth` | RBAC, JWT claims, gRPC interceptors (existing) | Gateway, all services |

<!-- markdownlint-enable MD013 -->

## Implementation Phases

### Phase 1: Identity Service + Dex Everywhere (Critical Path)

1. Scaffold `services/identity/` per the
   [New BIAN Service Checklist](../guides/new-bian-service-checklist.md)
   (proto, domain, adapters, service, gRPC, atlas, k8s, Tilt)
2. Extract `shared/pkg/credentials` and `shared/pkg/tokens`
3. Create Identity + RoleAssignment + Invitation tables (Atlas
   migrations)
4. Implement Authenticate RPC
5. Write Dex gRPC connector that calls Identity service
6. Remove Keycloak from Tilt; add Dex to local dev stack
7. Enable `AUTH_ENABLED=true` by default in all environments
8. Bootstrap creates platform admin identity from env vars
9. Remove `DEFAULT_TENANT_ID` and `DEFAULT_ROLES` env vars
10. JWT claims populated from RoleAssignment table

**Demo and local dev work identically**, but credentials are in the
database, not in dex.yaml static passwords.

**Estimate:** 13 story points

### Phase 2: Dynamic User Management

1. InviteUser, AcceptInvitation flows
2. GrantRole, RevokeRole APIs
3. Password management (self-service change, admin reset,
   forgot-password)
4. Tenant admins can invite operators and auditors

**Estimate:** 8 story points

### Phase 3: Federated Authentication

1. Configure Dex upstream connectors (Auth0, Okta, Google Workspace)
2. Identity service claim enrichment for federated users
3. SCIM provisioning for enterprise directory sync
4. MFA support

**Estimate:** 8 story points

### Phase 4: Advanced Access Control

1. Custom roles (tenant-defined, beyond built-in roles)
2. Resource-scoped permissions
3. Attribute-based access control (ABAC) via CEL expressions
4. API key management tied to identities

**Estimate:** 13 story points

## Security Requirements

### Password Policy

- Minimum 12 characters, bcrypt cost factor 12
- Account lockout after 5 failed attempts (30-minute lock)
- Password history (prevent reuse of last 5)

### Token Security

- Access tokens: stateless JWTs, 15-minute expiry, validated via JWKS
- Refresh tokens: 7-day expiry, single-use rotation, stored as bcrypt
  hash
- Invitation tokens: 72-hour expiry, single-use, stored as bcrypt hash
- Password reset tokens: 1-hour expiry, single-use, stored as bcrypt
  hash

### Audit Trail

- All identity operations logged to audit service
- Login attempts (success/failure) with IP and user agent
- Role changes with who, what, when
- SOC2 Type II compliant

## Success Criteria

1. Identity service operational as a standalone BIAN Employee Access
   service
2. Dex is sole OIDC provider (local, demo, production) — no Keycloak
3. Demo works with dynamic users (no hard-coded Dex passwords)
4. Platform admin bootstrapped from env var, subsequent admins invited
5. Tenant owner can invite admins, admins can invite operators
6. JWT claims reflect actual RoleAssignments, not env var defaults
7. User with roles in multiple tenants can switch between them
8. All auth events appear in audit trail
9. Party service unchanged — remains customer-only (KYC/AML)

## Non-Goals

- Frontend UI for user management (API-first; UI is separate)
- SSO/SAML support (OIDC via Dex connectors only)
- Custom permission definitions (Phase 4)
- Customer authentication (Party service concern, PRD 020)
- Session management (stateless JWT)

## Dependencies

- Dex (gRPC connector for Phase 1)
- Tenant service (tenant validation)
- Audit service (auth event logging)
- Gateway (remove DEFAULT_TENANT_ID / DEFAULT_ROLES fallback)
