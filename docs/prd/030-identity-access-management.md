---
name: prd-identity-access-management
description: >-
  Extend Party service with Identity and RoleAssignment business qualifiers
  for dynamic user management, role assignment, and multi-level access control.
  Dex as sole OIDC provider across all environments.
triggers:
  - Working on authentication, authorization, or access control
  - Implementing user management, invitation flows, or role assignment
  - Connecting Party service to login credentials
  - Configuring Dex OIDC, JWT claims, or token issuance
  - Working on platform admin bootstrap or tenant owner onboarding
  - Modifying DEFAULT_ROLES, DEFAULT_TENANT_ID, or auth interceptors
instructions: |
  The Party service handles two distinct access control concerns:

  1. Customer Access Control (KYC path) — Party.Individual + verification
     - Handled by: Party service (ExchangeDemographics RPC, verification adapters)
     - PRD: 020-party-kyc-aml-provider-integration
     - Purpose: Verify customer identity for regulatory compliance

  2. Staff/Operator Access Control (IAM path) — Identity + RoleAssignment
     - Handled by: Party service (new business qualifiers, this PRD 030)
     - Purpose: Authenticate staff, assign roles, populate JWT claims
     - Dex issues JWTs; Party service owns user CRUD and credentials

  Both paths create Party.Individual records, but serve different purposes:
  - KYC path: "Is this person who they claim to be?" (regulatory)
  - IAM path: "Can this person log in, and what can they do?" (operational)

  Key files:
  - services/party/ (Identity + RoleAssignment added here)
  - shared/platform/auth/rbac.go (existing RBAC framework)
  - shared/platform/auth/grpc_interceptor.go (JWT validation + tenant context)
  - shared/platform/auth/jwt.go (Claims structure)
  - services/gateway/auth/ (HTTP auth middleware)
  - deploy/demo/dex.yaml (static users to be replaced)

  Identity and RoleAssignment tables live in the public schema (cross-tenant)
  because users may have roles in multiple tenants. Party data remains
  tenant-scoped. Dex is the sole OIDC provider (no Keycloak).
---

# PRD 030: Identity and Access Management

**Status:** Not Started
**Version:** 2.0
**Date:** 2026-03-01
**Author:** Architecture Team
**Task Master Tag:** TBD

**ADRs:**

- [0002 - Microservices Per BIAN Domain](../adr/0002-microservices-per-bian-domain.md)
- [0021 - KYC/AML Verification Provider Architecture](../adr/0021-kyc-aml-verification-provider-architecture.md)

**Related PRDs:**

<!-- markdownlint-disable MD013 -->

- [020 - Party KYC/AML Provider Integration](020-party-kyc-aml-provider-integration.md) —
  Customer-facing identity verification

<!-- markdownlint-enable MD013 -->

---

## The Two Access Control Concerns

The Party service handles two distinct access control problems. Both live
within the Party domain but serve different purposes.

### Concern 1: Customer Access Control (KYC Path)

**Question answered:** "Is this person who they claim to be?"

This is the existing Party service capability, covered by PRD 020:

```text
Customer → Party.Individual → KYC Provider (Onfido/Stripe Identity)
  → Verification result (APPROVED/REJECTED)
  → Party status updated
  → Customer can open accounts, transact
```

- **Regulatory requirement** — financial services must verify customer identity
- **Tenant-scoped** — each tenant onboards their own customers
- **Handled by:** Party service `ExchangeDemographics` RPC + verification adapters
- **Data lives in:** tenant schema (`org_{tenant_id}`)

### Concern 2: Staff/Operator Access Control (IAM Path)

**Question answered:** "Can this person log in, and what can they do?"

This is the gap that this PRD addresses:

```text
Staff member → Party.Identity (email + credentials)
  → Dex validates via gRPC connector → JWT (tenant + roles)
  → RBAC enforcement → Access granted/denied
```

- **Operational requirement** — control who can operate the system
- **Cross-tenant** — a user may have roles in multiple tenants
- **Handled by:** Party service (new Identity + RoleAssignment qualifiers)
- **Data lives in:** public schema (cross-tenant)

### How They Connect

Both concerns anchor to `Party.Individual`, but as different business
qualifiers — just like Demographics, References, and BankRelation:

```text
Party.Individual
  ├── Demographics    (employment, income, education)
  ├── Reference       (passport, driver's license)
  ├── BankRelation    (account officer, branch)
  ├── Verification    (KYC result — PRD 020)
  ├── Identity        (login credentials — PRD 030)   ← NEW
  └── RoleAssignment  (tenant + role grants — PRD 030) ← NEW
```

A staff member who is also a customer of the same tenant has:

- One Party.Individual (their identity)
- One Verification record (KYC compliance — PRD 020)
- One Identity record (login credentials — PRD 030)
- One or more RoleAssignments (what they can do — PRD 030)

---

## Problem Statement

Meridian has two halves of an access control system that don't connect:

**What exists and works well:**

- JWT validation, JWKS key rotation, gRPC/HTTP interceptors
- RBAC framework (admin, operator, auditor, service roles + permission matrix)
- Schema-based multi-tenant isolation with subdomain-hopping prevention
- Party service with Organization, Individual, KYC verification, references
- Tenant service with provisioning, lifecycle, Party.Organization linkage
- Platform admin vs tenant admin interceptor separation

**What's missing:**

- No dynamic user management (Dex has 2 hard-coded users)
- No way to create, invite, or onboard users
- No storage for "which user has which roles in which tenant"
- No link from Party.Individual to "this person can log in"
- No self-service: tenants can't manage their own users
- Roles come from JWT claims, but nothing populates those claims dynamically

**The result:** A fully-featured authorization enforcement layer with no way
to actually manage who is authorized.

## Access Control Levels

Meridian requires four distinct access tiers:

<!-- markdownlint-disable MD013 -->

| Level | Name | Scope | Who | Role |
|-------|------|-------|-----|------|
| 0 | Platform Administrator | `meridian_master` | Meridian operators | `platform-admin`, `super-admin` |
| 1 | Tenant Owner | Single tenant | Organization that contracted Meridian | `tenant-owner` (new) |
| 2 | Tenant Administrator | Single tenant | Staff appointed by tenant owner | `admin` |
| 3 | Tenant User | Single tenant, restricted | Customers onboarded by tenant | `operator`, `auditor`, custom |

<!-- markdownlint-enable MD013 -->

**Level 0** creates/suspends tenants, applies platform manifests,
views cross-tenant metrics.
**Level 1** manages tenant admins, views billing, applies tenant manifests.
**Level 2** manages users, configures account types, runs operations.
**Level 3** views own accounts, initiates transactions, completes KYC.

## Architecture

### OIDC Provider: Dex Everywhere

**Decision:** Dex is the sole OIDC provider across all environments.
Keycloak is removed entirely.

| Responsibility | Owner |
|---------------|-------|
| User CRUD (create, invite, suspend) | Party service |
| Password storage + validation | Party service |
| Role assignment | Party service (RoleAssignment table) |
| JWT issuance + signing | Dex |
| JWKS endpoint + key rotation | Dex |
| Federation (upstream IdPs) | Dex connectors |
| Admin UI for user management | Meridian frontend (future) |

**Why not Keycloak?** Once Identity management lives in the Party service,
Keycloak becomes a ~500MB middleman duplicating what Meridian already owns.
Dex does the one thing needed externally — issue and sign JWTs — at ~20MB.
Dex is also what Kubernetes itself uses for OIDC.

**Environment parity:**

| Environment | OIDC Provider | Auth Enabled | Users |
|-------------|--------------|:---:|-------|
| Local (Tilt) | Dex | Yes | Dev users from dex.yaml |
| Demo | Dex | Yes | Bootstrap from env vars |
| Production | Dex | Yes | Dynamic via Party service |

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
User → Dex (gRPC connector → Party service)
  → Party validates credentials, looks up RoleAssignments
  → Dex issues JWT (sub, email, x-tenant-id, roles)
  → Gateway validates JWT (no defaults needed)
  → Interceptor injects claims into context
  → RBAC enforcement (unchanged)
```

For production with external IdPs:

```text
User → Dex (upstream connector → Auth0/Okta/Google)
  → Dex calls Party service to enrich claims
  → JWT (sub, email, x-tenant-id, roles)
  → Gateway validates JWT
  → RBAC enforcement (unchanged)
```

No separate Token Exchange service needed — Dex's connector model
handles federation natively.

### BIAN Alignment

BIAN defines a **Party Authentication** service domain. Identity and
RoleAssignment fit naturally as Party business qualifiers, consistent
with how Demographics, References, and Verification already work:

| Meridian Concept | BIAN Mapping |
|-----------------|--------------|
| Identity (login credentials) | Party Authentication Assessment |
| RoleAssignment | Party Role / Access Right |
| Session/Token | Authentication Token |
| Password reset | Authentication Maintenance |

## Core Features

### 1. Identity — Party Business Qualifier

Links a Party.Individual to authentication credentials, just as
Demographics links to employment history or Reference links to
identity documents.

```go
type Identity struct {
    ID             uuid.UUID
    PartyID        uuid.UUID       // FK to Party.Individual
    TenantID       tenant.TenantID // nullable for platform admins
    Email          string          // Login identifier (unique per tenant)
    PasswordHash   string          // bcrypt (Meridian-managed auth)
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

- One Party.Individual can have at most one Identity per tenant
- Email unique within a tenant (can exist across tenants)
- Password-based and federated auth are mutually exclusive
- Identity can exist without a Party (invite flow — Party created on accept)

**Why in Party service, not a separate service:**

- **Same domain**: Identity is a Party business qualifier, like Demographics
- **Simpler deployment**: No additional service to operate
- **Shared context**: Party already has the Individual, Verification, and
  Reference entities that Identity relates to
- **Cross-tenant data**: The `identity` table lives in the public schema
  (same pattern as the `tenant` table in the Tenant service)

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

When a user authenticates, Dex calls the Party service's gRPC connector
to validate credentials and populate JWT claims:

```text
Dex receives login request (email + password)
  → Calls Party service Authenticate RPC
  → Party validates password hash
  → Party looks up RoleAssignments for this identity
  → Returns: sub, email, name, x-tenant-id, roles[]
  → Dex signs JWT with these claims
```

For federated auth (Phase 3), Dex's upstream connectors handle the
external IdP interaction, then call Party service to enrich claims
with tenant and role information.

### 4. User Lifecycle Operations

**Invitation:** `InviteUser(email, role)` creates a PENDING_INVITE identity
with a time-limited token. User sets password or links IdP, then
Party.Individual is created and KYC initiated if required.

**Password management:** Self-service change, admin reset, forgot-password
flow with time-limited reset tokens delivered via webhook.

**Lifecycle:** Suspend (disable login), reactivate, deprovision (soft-delete
with PII anonymization after retention).

### 5. Multi-Tenant User Resolution

A user with roles in multiple tenants selects their tenant at login time.
Single-tenant users are auto-selected. Tenant switching issues a new JWT
without re-authentication.

### 6. Platform Admin Bootstrap

On first boot, Meridian creates a platform admin identity from
`PLATFORM_ADMIN_EMAIL` and `PLATFORM_ADMIN_PASSWORD` env vars with forced
password change. Subsequent admins are created via invitation.

## Database Schema

Identity and RoleAssignment tables live in the **public schema** because
they span tenants. This is the same pattern used by the `tenant` table
in the Tenant service.

```sql
CREATE TABLE identity (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    party_id        UUID,
    tenant_id       VARCHAR(50),
    email           VARCHAR(255) NOT NULL,
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

    UNIQUE (tenant_id, email),
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

## Proto Definition

New RPCs added to the existing Party service proto, grouped as a
business qualifier (same pattern as RetrieveDemographics,
RetrieveReference, etc.):

```protobuf
// Identity business qualifier RPCs (added to PartyService)
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
```

## Implementation Phases

### Phase 1: Dex Everywhere + Replace Static Users (Critical Path)

1. Remove Keycloak from Tilt; add Dex to local dev stack
2. Enable `AUTH_ENABLED=true` by default in all environments
3. Add Identity + RoleAssignment tables to Party service migrations
4. Implement Authenticate RPC in Party service
5. Write Dex gRPC connector that calls Party service
6. Bootstrap creates platform admin identity from env vars
7. Remove `DEFAULT_TENANT_ID` and `DEFAULT_ROLES` env vars
8. JWT claims populated from RoleAssignment table

**Demo and local dev work identically**, but credentials are in the
database, not in dex.yaml static passwords.

**Estimate:** 13 story points

### Phase 2: Dynamic User Management

1. InviteUser, AcceptInvitation flows
2. GrantRole, RevokeRole APIs
3. Password management (self-service change, admin reset, forgot-password)
4. Tenant admins can invite operators and auditors

**Estimate:** 8 story points

### Phase 3: Federated Authentication

1. Configure Dex upstream connectors (Auth0, Okta, Google Workspace)
2. Party service claim enrichment for federated users
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

- Access tokens: 15-minute expiry
- Refresh tokens: 7-day expiry, single-use rotation
- Invitation tokens: 72-hour expiry, single-use
- Password reset tokens: 1-hour expiry, single-use
- All tokens stored as bcrypt hashes

### Audit Trail

- All identity operations logged to audit service
- Login attempts (success/failure) with IP and user agent
- Role changes with who, what, when
- SOC2 Type II compliant

## Success Criteria

1. Dex is sole OIDC provider (local, demo, production) — no Keycloak
2. Demo works with dynamic users (no hard-coded Dex passwords)
3. Platform admin bootstrapped from env var, subsequent admins invited
4. Tenant owner can invite admins, admins can invite users
5. JWT claims reflect actual RoleAssignments, not env var defaults
6. Identity.party_id links to Party.Individual for KYC-verified users
7. User with roles in multiple tenants can switch between them
8. All auth events appear in audit trail

## Non-Goals

- Frontend UI for user management (API-first; UI is separate)
- SSO/SAML support (OIDC via Dex connectors only)
- Custom permission definitions (Phase 4)
- Biometric authentication (KYC provider concern)
- Session management (stateless JWT)

## Dependencies

- Dex (gRPC connector for Phase 1)
- Tenant service (tenant validation, Party.Organization mapping)
- Audit service (auth event logging)
- Gateway (remove DEFAULT_TENANT_ID / DEFAULT_ROLES fallback)
