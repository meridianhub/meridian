---
name: prd-identity-access-management
description: Bridge Party service identity (KYC) to authentication and authorization with dynamic user management, role assignment, and multi-level access control
triggers:
  - Working on authentication, authorization, or access control
  - Implementing user management, invitation flows, or role assignment
  - Connecting Party service to login credentials
  - Configuring Dex OIDC, JWT claims, or token issuance
  - Working on platform admin bootstrap or tenant owner onboarding
  - Modifying DEFAULT_ROLES, DEFAULT_TENANT_ID, or auth interceptors
instructions: |
  The Party service handles two distinct access control concerns:

  1. Customer Access Control (KYC path) — Party.Individual + verification providers
     - Handled by: Party service (ExchangeDemographics RPC, verification adapters)
     - PRD: 020-party-kyc-aml-provider-integration
     - Purpose: Verify customer identity for regulatory compliance

  2. Staff/Operator Access Control (IAM path) — Identity + RoleAssignment + JWT claims
     - Handled by: Identity service (this PRD, 030)
     - Purpose: Authenticate staff, assign roles, populate JWT claims

  Both paths create Party.Individual records, but serve different purposes:
  - KYC path: "Is this person who they claim to be?" (regulatory)
  - IAM path: "Can this person log in, and what can they do?" (operational)

  Key files:
  - shared/platform/auth/rbac.go (existing RBAC framework)
  - shared/platform/auth/grpc_interceptor.go (JWT validation + tenant context)
  - shared/platform/auth/jwt.go (Claims structure)
  - services/gateway/auth/ (HTTP auth middleware)
  - deploy/demo/dex.yaml (static users to be replaced)

  The Identity service lives in the public schema (cross-tenant) because
  users may have roles in multiple tenants. Party data remains tenant-scoped.
---

# PRD 030: Identity and Access Management

**Status:** Not Started
**Version:** 1.0
**Date:** 2026-03-01
**Author:** Architecture Team
**Task Master Tag:** TBD

**ADRs:**

- [0002 - Microservices Per BIAN Domain](../adr/0002-microservices-per-bian-domain.md)
- [0021 - KYC/AML Verification Provider Architecture](../adr/0021-kyc-aml-verification-provider-architecture.md)

**Related PRDs:**

- [020 - Party KYC/AML Provider Integration](020-party-kyc-aml-provider-integration.md) — Customer-facing identity verification
- [Multi-Tenancy](.taskmaster/docs/prd-multi-tenancy.md) — Tenant provisioning and isolation

---

## The Two Access Control Concerns

The Party service sits at the intersection of two distinct access control problems.
Understanding this separation is essential to this PRD.

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
Staff member → Identity (email + credentials) → JWT (tenant + roles)
  → RBAC enforcement → Access granted/denied
```

- **Operational requirement** — control who can operate the system
- **Cross-tenant** — a user may have roles in multiple tenants
- **Handled by:** New Identity service (this PRD)
- **Data lives in:** public schema (cross-tenant)

### How They Connect

Both concerns create `Party.Individual` records, but for different reasons:

```text
                    Party.Individual
                   /                \
    KYC Path (PRD 020)        IAM Path (PRD 030)
    "Verify identity"         "Grant system access"
    Tenant-scoped             Cross-tenant
    Regulatory                Operational
    Async (days)              Sync (seconds)
    External providers        Internal credentials
```

A staff member who is also a customer of the same tenant has:

- One Party.Individual (their identity)
- One KYC verification record (customer compliance)
- One Identity record (login credentials)
- One or more RoleAssignments (what they can do)

---

## Problem Statement

Meridian has two halves of an access control system that don't talk to each other:

**What exists and works well:**

- JWT validation, JWKS key rotation, gRPC/HTTP interceptors
- RBAC framework (admin, operator, auditor, service roles with permission matrix)
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

**The result:** A fully-featured authorization enforcement layer with no way to actually manage who is authorized.

## Access Control Levels

Meridian requires four distinct access tiers:

| Level | Name | Scope | Who | Role |
|-------|------|-------|-----|------|
| 0 | Platform Administrator | `meridian_master` | Meridian operators | `platform-admin`, `super-admin` |
| 1 | Tenant Owner | Single tenant | Organization that contracted Meridian | `tenant-owner` (new) |
| 2 | Tenant Administrator | Single tenant | Staff appointed by tenant owner | `admin` |
| 3 | Tenant User | Single tenant, restricted | Customers onboarded by tenant | `operator`, `auditor`, custom |

**Level 0** can create/suspend tenants, apply platform manifests, view cross-tenant metrics.
**Level 1** can manage tenant admins, view billing, apply tenant manifests, access all tenant data.
**Level 2** can manage users, configure account types, run operations, view audit trails.
**Level 3** can view own accounts, initiate transactions, complete KYC.

## Architecture Context

### Current Authentication Flow (Demo)

```text
User → Dex (static password check) → JWT (sub, email, name)
  → Gateway adds DEFAULT_TENANT_ID + DEFAULT_ROLES
  → Interceptor injects claims into context
  → RBAC enforcement checks role+resource+permission
```

**Problem:** `DEFAULT_TENANT_ID` and `DEFAULT_ROLES` are env vars.
Every user gets the same tenant and same roles. No per-user differentiation.

### Target Authentication Flow

```text
User → Dex (connector → Meridian Identity Service)
  → JWT (sub, email, x-tenant-id, roles)
  → Gateway validates JWT (no defaults needed)
  → Interceptor injects claims into context
  → RBAC enforcement (unchanged)
```

Or, for production deployments with external IdPs:

```text
User → External IdP (Auth0, Okta, Google Workspace)
  → Meridian Token Exchange enriches token with tenant + roles
  → JWT (sub, email, x-tenant-id, roles)
  → Gateway validates JWT
  → RBAC enforcement (unchanged)
```

### BIAN Alignment

BIAN defines a **Party Authentication** service domain for verifying party
identity for access control:

| Meridian Concept | BIAN Mapping |
|-----------------|--------------|
| Identity (login credentials) | Party Authentication Assessment |
| Role Assignment | Party Role / Access Right |
| Session/Token | Authentication Token |
| Password reset | Authentication Maintenance |

### Local Development: Keycloak vs Dex Mismatch

The local Tilt environment deploys **Keycloak** (v26.0, ~500MB RAM) as its OIDC
provider, but keeps `AUTH_ENABLED=false` by default — meaning it's running but
never exercised. The demo environment uses **Dex** (~20MB RAM) with static users
and `AUTH_ENABLED=true`.

This creates two problems:

1. **Environment divergence**: Different OIDC providers means JWT claim formats,
   token endpoints, and JWKS paths differ between local and demo
2. **Auth is never tested locally**: Developers only discover auth issues when
   deploying to demo

**Recommendation:** Replace Keycloak with Dex in Tilt to match the demo stack.
Dex is 25x lighter and aligns environments. Enable `AUTH_ENABLED=true` by
default so auth code is always exercised locally.

```yaml
# Replace Keycloak deployment with Dex
# Tilt: deploy/local/dex.yaml (~20MB vs ~500MB for Keycloak)
# Reuse deploy/demo/dex.yaml as base, add local dev users
```

This is a prerequisite for Phase 1 — the Dex gRPC connector must be testable locally.

## Core Features

### 1. Identity Entity — The Party-to-Login Bridge

Links a Party.Individual to authentication credentials. A Party represents a
business entity (KYC, demographics, references). An Identity represents
"this party can authenticate."

```go
type Identity struct {
    ID             uuid.UUID
    PartyID        uuid.UUID       // FK to Party.Individual (nullable until KYC)
    TenantID       tenant.TenantID
    Email          string          // Login identifier (unique per tenant)
    PasswordHash   string          // bcrypt (Meridian-managed auth)
    ExternalIDPSub string          // Subject from external IdP (federated auth)
    ExternalIDP    string          // IdP name ("google", "auth0", "okta")
    Status         IdentityStatus  // ACTIVE, PENDING_INVITE, SUSPENDED, LOCKED, DEPROVISIONED
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
- Password-based and federated auth are mutually exclusive per identity
- Identity can exist without a Party (invite flow — Party created during KYC)

### 2. Role Assignment — Dynamic Authorization

Stores which roles a user has within which tenant. Replaces the static `DEFAULT_ROLES` mechanism.

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

### 3. Token Issuance — Claims Population

When a user authenticates, their JWT is populated with the correct
`x-tenant-id` and `roles` claims from RoleAssignment records.

**3a. Meridian-Managed Auth (Dex + Custom Connector):**

Write a Dex gRPC connector that calls the Identity service to validate
credentials and return claims.

**3b. Federated Auth (External IdP + Token Exchange):**

Implement OAuth 2.0 Token Exchange (RFC 8693) to enrich external IdP tokens
with Meridian tenant and role claims.

### 4. User Lifecycle Operations

**Invitation:** `InviteUser(email, role)` creates a PENDING_INVITE identity with
a time-limited token. User sets password or links IdP, then Party.Individual is
created and KYC initiated if required.

**Password management:** Self-service change, admin reset, forgot-password flow
with time-limited reset tokens delivered via webhook.

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

## Service Design

**Recommendation: New Identity Service** (separate from Party)

Rationale:

- **Security boundary**: Credentials isolated from business data
- **BIAN alignment**: Party Authentication is distinct from Party Directory
- **Scaling**: Auth endpoints have different performance characteristics
- **Schema**: Identity data is cross-tenant; Party data is tenant-scoped

### Database Schema

Identity data lives in the **public schema** (cross-tenant):

```sql
CREATE TABLE identity (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       VARCHAR(50) NOT NULL,
    party_id        UUID,
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
    UNIQUE (external_idp, external_idp_sub) WHERE external_idp IS NOT NULL
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

    UNIQUE (identity_id, tenant_id, role) WHERE revoked = false
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

### Proto Definition

```protobuf
service IdentityService {
  // Identity CRUD
  rpc CreateIdentity(CreateIdentityRequest) returns (CreateIdentityResponse);
  rpc RetrieveIdentity(RetrieveIdentityRequest) returns (RetrieveIdentityResponse);
  rpc UpdateIdentity(UpdateIdentityRequest) returns (UpdateIdentityResponse);
  rpc ListIdentities(ListIdentitiesRequest) returns (ListIdentitiesResponse);

  // Authentication
  rpc Authenticate(AuthenticateRequest) returns (AuthenticateResponse);
  rpc RefreshToken(RefreshTokenRequest) returns (RefreshTokenResponse);
  rpc ExchangeToken(ExchangeTokenRequest) returns (ExchangeTokenResponse);

  // Password management
  rpc SetPassword(SetPasswordRequest) returns (SetPasswordResponse);
  rpc ChangePassword(ChangePasswordRequest) returns (ChangePasswordResponse);
  rpc RequestPasswordReset(RequestPasswordResetRequest) returns (RequestPasswordResetResponse);
  rpc CompletePasswordReset(CompletePasswordResetRequest) returns (CompletePasswordResetResponse);

  // Role management
  rpc GrantRole(GrantRoleRequest) returns (GrantRoleResponse);
  rpc RevokeRole(RevokeRoleRequest) returns (RevokeRoleResponse);
  rpc ListRoleAssignments(ListRoleAssignmentsRequest) returns (ListRoleAssignmentsResponse);

  // Invitation
  rpc InviteUser(InviteUserRequest) returns (InviteUserResponse);
  rpc AcceptInvitation(AcceptInvitationRequest) returns (AcceptInvitationResponse);

  // Tenant resolution
  rpc ListUserTenants(ListUserTenantsRequest) returns (ListUserTenantsResponse);
  rpc SwitchTenant(SwitchTenantRequest) returns (SwitchTenantResponse);

  // Lifecycle
  rpc SuspendIdentity(SuspendIdentityRequest) returns (SuspendIdentityResponse);
  rpc ReactivateIdentity(ReactivateIdentityRequest) returns (ReactivateIdentityResponse);
}
```

## Implementation Phases

### Phase 1: Replace Static Dex Users (Critical Path)

1. Replace Keycloak with Dex in local Tilt stack (environment parity)
2. Enable `AUTH_ENABLED=true` by default in local dev
3. Add Identity + RoleAssignment tables to migrations
4. Bootstrap creates identities for existing demo users
5. Write Dex gRPC connector to validate against Identity service
6. Remove `DEFAULT_TENANT_ID` and `DEFAULT_ROLES` env vars
7. JWT claims populated from RoleAssignment table

**Demo works identically**, but credentials are now in the database, not in dex.yaml.

**Estimate:** 13 story points

### Phase 2: Dynamic User Management

1. InviteUser, AcceptInvitation flows
2. GrantRole, RevokeRole APIs
3. Password management (self-service change, admin reset, forgot-password)
4. Tenant admins can invite operators and auditors

**Estimate:** 8 story points

### Phase 3: Federated Authentication

1. Token Exchange endpoint (RFC 8693)
2. External IdP support (Auth0, Okta, Google Workspace)
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

1. Demo environment works with dynamic users (no hard-coded Dex passwords)
2. First platform admin created from env var, subsequent admins via invitation
3. Tenant owner can invite admins, admins can invite users
4. JWT claims reflect actual role assignments, not env var defaults
5. Identity.party_id links to Party.Individual for KYC-verified users
6. User with roles in multiple tenants can switch between them
7. All auth events appear in audit trail

## Non-Goals

- Frontend UI for user management (API-first; UI is separate)
- SSO/SAML support (OIDC only)
- Custom permission definitions (Phase 4)
- Biometric authentication (KYC provider concern)
- Session management (stateless JWT)

## Dependencies

- Party service (Party.Individual creation during invitation)
- Tenant service (tenant validation, Party.Organization mapping)
- Audit service (auth event logging)
- Dex (gRPC connector for Phase 1)
- Gateway (remove DEFAULT_TENANT_ID / DEFAULT_ROLES fallback)
