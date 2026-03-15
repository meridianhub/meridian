---
name: prd-auth-flow-architecture
description: >-
  Defines the three authentication entry points (BFF password, BFF SSO, MCP OAuth),
  how tenant context flows through each, and the constraints that Dex's internal
  HTTP handlers impose. Addresses the pattern of circular fixes in auth routing.
triggers:
  - Modifying Dex handler mounting or tenant resolution for /dex/ paths
  - Changing MCP OAuth authorize/callback flow
  - Changing BFF SSO or BFF password login
  - Adding or removing platformPaths entries for auth endpoints
  - Debugging "tenant context missing" errors in the connector
instructions: |
  This PRD exists because auth routing changes have historically created
  circular fix patterns. Before making changes to any auth flow, read this
  document to understand all three flows and their tenant context requirements.

  Key constraint: Dex's internal HTTP handler (login form → connector.Login)
  does NOT propagate custom context (like tenant). Any solution must work
  within this limitation.

  Key files:
  - services/api-gateway/server.go (route mounting, middleware chains)
  - services/api-gateway/auth_handler.go (BFF password login)
  - services/api-gateway/auth_sso_handler.go (BFF SSO flow)
  - services/mcp-server/internal/auth/oidc.go (MCP OAuth → Dex)
  - services/identity/dex/connector_adapter.go (Dex → Meridian connector bridge)
  - services/identity/connector/connector.go (tenant-scoped credential validation)
  - shared/platform/gateway/tenant_resolver.go (subdomain → tenant resolution)
  - deploy/demo/Caddyfile (reverse proxy routing)
---

# PRD 044: Auth Flow Architecture

**Status:** Draft
**Version:** 1.0
**Date:** 2026-03-15
**Author:** Architecture Team

**Related PRDs:**

- [031 - Identity and Access Management](031-identity-access-management.md) —
  Identity service domain model, roles, Dex connector
- [027 - MCP Server](027-mcp-server.md) — MCP transport and tooling
- [033 - Gateway Architecture](033-gateway-architecture.md) — Gateway routing

---

## Problem Statement

Between 2026-03-09 and 2026-03-14, **18 PRs** modified auth routing. The
pattern is circular: each fix for one auth flow breaks another because
there is no shared specification for how the three flows interact.

### The Circular Fix Pattern

| Date | PR | Action | Side Effect |
|------|----|--------|-------------|
| Mar 9 | #1518 | Embed Dex with connector adapter | — |
| Mar 9 | #1523 | Wire embedded Dex into gateway | — |
| Mar 9 | #1536 | **Revert** to sidecar (go.mod issues) | Lost embedded work |
| Mar 10 | #1563 | Wire connector into Dex with tenant context | — |
| Mar 11 | #1600 | BFF bypasses Dex for password auth | Dex now SSO-only |
| Mar 11 | #1606 | Remove DexPasswordConnector (dead code) | — |
| Mar 12 | #1635 | MCP OAuth re-introduces Dex password login | Reintroduces removed flow |
| Mar 13 | #1651 | Fix: Dex browser redirects used Docker URL | — |
| Mar 13 | #1656 | **Re-embed** Dex (second time) | — |
| Mar 14 | #1665 | Fix: exempt /dex/ from tenant resolution | Breaks connector Login |

**Root cause:** No specification defines how tenant context flows through
each auth entry point, so each change optimizes for one flow without
considering the others.

## The Three Auth Flows

Meridian has three distinct authentication entry points. Each has
different constraints on how tenant context is established.

### Flow 1: BFF Password Login (Works)

**Entry point:** `POST /api/auth/login`
**Used by:** Frontend direct password form
**Tenant source:** Subdomain (e.g., `acme.demo.meridianhub.cloud`)

```text
Browser (on acme.demo.meridianhub.cloud)
  → POST /api/auth/login {email, password}
  → Caddy → meridian:8090
  → TenantResolver extracts "acme" from Host header
  → tenant.WithTenant(ctx, tenantID)
  → AuthHandler.HandleLogin validates credentials via connector
  → connector.Login(ctx_with_tenant, email, password)
  → Signs Meridian JWT with tenant claims
  → Returns JWT to browser
```

**Why it works:** The request arrives on a tenant subdomain. The tenant
resolver middleware runs before the handler. The connector receives a
context with tenant already set.

**Dex involvement:** None. BFF password login bypasses Dex entirely
(PR #1600).

### Flow 2: BFF SSO Login (Works)

**Entry point:** `GET /api/auth/sso/{connector_id}`
**Used by:** Frontend SSO buttons (Google, GitHub, etc.)
**Tenant source:** Subdomain on initiation, state parameter on callback

```text
Browser (on acme.demo.meridianhub.cloud)
  → GET /api/auth/sso/meridian
  → TenantResolver extracts "acme" → tenantID
  → SSOHandler stores tenantID in PKCE state
  → Redirect to /dex/auth/meridian (with connector ID in path)
  → Dex authenticates user (external IdP or password form)
  → Dex redirects to /api/auth/callback?code=...&state=...
  → SSOHandler recovers tenantID from state
  → tenant.WithTenant(ctx, tenantID)
  → resolver.Resolve(ctx_with_tenant, email) looks up identity
  → Signs Meridian JWT with tenant claims
  → Redirect to frontend with token
```

**Why it works:** Tenant context is captured on the subdomain during
initiation and stored in the state parameter. The callback recovers it.
Dex's connector is NOT called for credential validation in this flow
— Dex only handles the OAuth redirect dance. Identity resolution
happens in the callback handler with proper tenant context.

**Key detail:** The SSO flow redirects to `/dex/auth/{connector_id}`
(with connector ID in the path), which for external IdPs (Google, etc.)
skips Dex's password form entirely. For the "meridian" password
connector, Dex DOES show a login form, but after authentication Dex
issues a code that comes back to the BFF callback — where tenant
context is restored from state.

### Flow 3: MCP OAuth Login (BROKEN)

**Entry point:** `GET /oauth/authorize` (on MCP server)
**Used by:** Claude.ai, other MCP clients
**Tenant source:** Subdomain on authorize, BUT lost in Dex redirect

```text
MCP Client
  → GET /oauth/authorize?client_id=...&code_challenge=...
  → Caddy → mcp-server:8090
  → MCP OIDCHandler extracts tenant slug from subdomain
  → Stores tenant slug in OIDCFlowState
  → Redirect to demo.meridianhub.cloud/dex/auth?... (NO tenant subdomain)
  → Caddy → meridian:8090
  → /dex/ is in platformPaths → tenant resolution SKIPPED
  → Dex shows login form at /dex/auth/meridian/login
  → User submits credentials
  → Dex internally calls connector.Login(ctx_WITHOUT_tenant, email, password)
  → ❌ "connector: tenant context missing"
```

**Why it breaks:** Three compounding issues:

1. **MCP redirects to bare domain** — `demo.meridianhub.cloud/dex/auth`
   has no tenant subdomain, so even if tenant resolution ran, there's
   no subdomain to extract.

2. **`/dex/` is a platform path** — Added in PR #1665 to fix a
   different problem (Dex infrastructure endpoints like `/dex/keys`
   don't need tenant context). But this also exempts `/dex/auth/*/login`
   where tenant IS needed.

3. **Dex's internal routing is opaque** — When Dex renders the login
   form and the user submits it, Dex's own HTTP handler processes
   the POST. The connector adapter receives whatever context Dex's
   handler created — which has no tenant information. There is no
   hook to inject tenant context into Dex's internal request pipeline.

## Architectural Constraints

These constraints are fixed — solutions must work within them.

### C1: Dex Does Not Know About Tenants

Dex is a general-purpose OIDC server. Its `PasswordConnector` interface
receives a plain `context.Context` from Dex's own HTTP handler. There
is no mechanism to inject custom middleware into Dex's internal request
processing pipeline.

**Implication:** Tenant context cannot be added to the `ctx` that Dex
passes to `connector.Login()` without modifying Dex itself (which we
do not want to do — it's an upstream dependency).

### C2: Some Dex Endpoints Are Platform-Level

Endpoints like `/dex/keys`, `/dex/token`, and
`/dex/.well-known/openid-configuration` are infrastructure endpoints
called server-to-server. They do not have tenant context and must not
require it.

**Implication:** Cannot simply require tenant resolution for all
`/dex/*` paths.

### C3: Caddy Routes by Path, Not Subdomain

The Caddyfile matches `*.demo.meridianhub.cloud` but routes by path
prefix. `/oauth/*` goes to `mcp-server:8090`. `/dex/*` goes to
`meridian:8090`. The MCP server and Meridian binary are separate
containers.

**Implication:** The MCP server cannot serve Dex endpoints. When MCP
redirects to Dex, the browser hits a different container.

### C4: MCP Clients May Not Support Tenant Subdomains

MCP clients (Claude.ai) are configured with a single server URL
(e.g., `demo.meridianhub.cloud/mcp`). They may not support or
preserve tenant subdomains in their OAuth redirects.

**Implication:** Cannot rely on MCP clients adding tenant subdomain
to the authorize URL. The MCP server must handle tenant routing.

### C5: Embedded Dex Shares Process with Gateway

Since PR #1656, Dex runs embedded in the Meridian binary. The Dex
HTTP handler is set via `EmbeddedDex.SetHandler()`. This means
we CAN wrap the Dex handler with custom middleware before mounting.

**Implication:** We have the ability to intercept requests to Dex
before they reach Dex's internal routing — this is the key
architectural lever.

## Requirements

### R1: All Three Auth Flows Must Work Simultaneously

All three flows must be functional. A fix to one flow must not break
another. This is verified by integration tests covering all three
paths.

### R2: Tenant Context Must Reach the Connector for Password Auth

When the Meridian password connector's `Login()` is called (by any
flow), the `context.Context` must contain a valid tenant ID. This is
a hard requirement — the connector validates credentials against
per-tenant schema data.

### R3: Platform-Level Dex Endpoints Must Not Require Tenant

`/dex/keys`, `/dex/token`, `/dex/.well-known/openid-configuration`
must work without tenant context. These are called server-to-server
by the MCP server and BFF during code exchange.

### R4: MCP Flow Must Work from Bare Domain

MCP clients connect to `demo.meridianhub.cloud/mcp` (no tenant
subdomain). The auth flow must establish tenant context without
requiring the MCP client to know about subdomains.

### R5: Single Tenant Demo Simplification

For the demo environment (single tenant), the system should work
without requiring the user to navigate to a tenant subdomain for
MCP auth. Tenant can be inferred from a default or from the MCP
server configuration.

### R6: Multi-Tenant Production Readiness

For production (multiple tenants), the MCP auth flow must support
tenant selection or tenant-scoped MCP endpoints. This may be
deferred but the architecture must not preclude it.

## Proposed Solution

### Option A: Tenant-Aware Dex Handler Wrapper (Recommended)

Wrap the embedded Dex handler with middleware that injects tenant
context based on available signals, without modifying Dex itself.

**Mechanism:**

1. Remove `/dex/` from `platformPaths`
2. Add `OptionalTenantHandler` to tenant resolver — resolves tenant
   from subdomain if present, passes through without error if not
3. Mount Dex with `OptionalTenantHandler` instead of `wrapWithTenantOnly`
4. Add a `DefaultTenantMiddleware` that injects a configured default
   tenant when no tenant is in context (for demo single-tenant mode)
5. MCP `HandleAuthorize` redirects to tenant-scoped Dex URL when
   tenant slug is available (e.g., `acme.demo.meridianhub.cloud/dex/auth`)

**Tenant context flow for MCP with this fix:**

```text
MCP Client → GET /oauth/authorize (on demo.meridianhub.cloud)
  → MCP server extracts tenant slug (from subdomain or default config)
  → Stores slug in OIDCFlowState
  → Redirects to acme.demo.meridianhub.cloud/dex/auth?...
  → OptionalTenantHandler resolves "acme" → tenantID
  → tenant.WithTenant(ctx, tenantID)
  → Dex shows login form
  → User submits → Dex calls connector.Login(ctx_WITH_tenant)
  → ✅ Works
```

**For platform-level Dex endpoints (no subdomain):**

```text
MCP Server → POST demo.meridianhub.cloud/dex/token
  → OptionalTenantHandler: no subdomain → no tenant in ctx (OK)
  → Dex processes token exchange (doesn't need tenant)
  → ✅ Works
```

**Changes required:**

| File | Change |
|------|--------|
| `shared/platform/gateway/tenant_resolver.go` | Add `OptionalTenantHandler()` method |
| `shared/platform/gateway/tenant_resolver.go` | Remove `/dex/` from `platformPaths` |
| `services/api-gateway/server.go` | Mount Dex with optional tenant resolution |
| `services/mcp-server/internal/auth/oidc.go` | Redirect to tenant-scoped Dex URL |
| `services/mcp-server/cmd/main.go` | Add default tenant config for demo mode |

**Risk:** Low. `OptionalTenantHandler` is additive — existing paths
that provide subdomain continue working. Paths without subdomain
get no tenant context (same as today's `platformPaths` behavior).

### Option B: Bypass Dex for MCP Password Auth

Have the MCP server collect credentials directly (like BFF does) and
validate via the connector without going through Dex.

**Rejected because:** Duplicates BFF auth logic in MCP server. Creates
a fourth auth flow to maintain. Does not solve the fundamental problem
for federated auth (Phase 3 of PRD 031).

### Option C: Custom Dex Fork with Tenant Support

Fork Dex and add tenant-aware context propagation.

**Rejected because:** Maintenance burden of a fork. Dex is an upstream
dependency with active development. Meridian should not own Dex
internals.

## Test Plan

### Integration Tests Required

Each test must run against a real CockroachDB testcontainer with
tenant-scoped schema.

1. **BFF Password Login** — POST `/api/auth/login` on tenant subdomain
   returns valid JWT with tenant claims
2. **BFF SSO Initiate + Callback** — GET `/api/auth/sso/meridian` on
   tenant subdomain → Dex → callback → valid JWT with tenant claims
3. **MCP OAuth Full Flow** — GET `/oauth/authorize` → Dex login form
   → submit credentials → callback → MCP code → token exchange →
   valid JWT with tenant claims
4. **Dex Keys (No Tenant)** — GET `/dex/keys` without subdomain
   returns JWKS (no 404 or tenant error)
5. **Dex Token Exchange (No Tenant)** — POST `/dex/token` without
   subdomain succeeds for valid authorization code
6. **Cross-Flow Token Validity** — JWT issued by MCP flow is accepted
   by gateway auth middleware (shared signing key)
7. **Multi-Tenant Isolation** — MCP login for tenant A cannot access
   tenant B's identities

### Regression Guard

Add a CI test that exercises all three flows in sequence. If any flow
breaks, the test fails before merge. This prevents the circular fix
pattern.

## Success Criteria

1. All three auth flows produce valid JWTs with correct tenant claims
2. `connector: tenant context missing` error does not occur in any flow
3. Dex infrastructure endpoints work without tenant context
4. No auth-related PRs needed as follow-up fixes after merge
5. Integration tests cover all three flows as a regression guard

## Non-Goals

- Federated IdP support (Google, GitHub SSO) — PRD 031 Phase 3
- Multi-tenant MCP endpoints (tenant selection UI) — future work
- Token refresh flows — out of scope for this architectural fix
- Frontend auth UI changes — no frontend changes needed
- Identity CRUD operations — PRD 031 scope
