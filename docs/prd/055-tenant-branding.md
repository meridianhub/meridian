# Tenant Branding: Display Name Propagation

## Problem Statement

When a tenant user visits their subdomain (e.g.,
`volterra-energy.demo.meridianhub.cloud/login`), the UI shows
"Meridian" everywhere - on the login page, in the header after
login, and in the page title. There is no indication of which
tenant organization the user is accessing.

This matters because:

1. **Trust** - Users landing on their org's subdomain expect to
   see their org name. Seeing "Meridian" is confusing and erodes
   confidence that they're in the right place.
2. **Multi-tenant clarity** - Platform admins switching between
   tenants need clear visual feedback about which tenant context
   they're operating in.
3. **Demo credibility** - During demos, showing the prospect's
   org name instead of "Meridian" makes the product feel real.

## Technical Context

### What Exists

| Component | Current State |
|-----------|--------------|
| Tenant `DisplayName` field | Stored in DB, exposed in proto, set during tenant creation |
| Tenant slug | Extracted from subdomain by `TenantResolverMiddleware`, injected into context |
| JWT claims | Include `x-tenant-slug` and `x-tenant-id`, but NOT display name |
| Frontend tenant context | Has `tenantSlug` and `currentTenant` (platform admins only) |
| Header component | Hardcoded "Meridian" text |
| Login page | Hardcoded "Meridian Operations Console" title |
| `<title>` tag | Hardcoded "Meridian Operations Console" in index.html |

### Architecture

The tenant resolver (`shared/platform/gateway/tenant_resolver.go`)
already loads the full `domain.Tenant` entity (including `DisplayName`)
when resolving slug to tenant ID. It discards the display name after
resolution, only injecting `tenantID` and `slug` into the request
context.

JWT minting happens in two places:

- `services/api-gateway/auth_handler.go` - password-based login
- `services/api-gateway/auth_sso_handler.go` - SSO/OIDC callback

Both have access to the tenant context at the point where claims
are constructed.

The login page is pre-authentication - no JWT exists yet. The only
tenant signal available is the subdomain slug. To show the real
display name pre-login, we need a public (unauthenticated) endpoint.

## Solution

### 1. Propagate display name through tenant context (backend)

Extend the tenant context package to carry `DisplayName` alongside
`TenantID` and `Slug`:

- Add `WithDisplayName()` / `DisplayNameFromContext()` to
  `shared/platform/tenant/context.go`
- Update `TenantResolverMiddleware` to inject display name into
  context after resolving the tenant entity
- This makes display name available to all downstream handlers

**Cache consideration:** The current `slugCache` only stores
`TenantID`. On cache hits (common path), the full entity is not
loaded. The resolver should extend the cache to store display name
alongside tenant ID, or fall back to a DB query when display name
is needed. Either approach is acceptable - the display name lookup
is low-frequency (once per login, once per page load) and the
tenant table is small.

### 2. Add display name to JWT claims (backend)

In both auth handlers (`auth_handler.go` and `auth_sso_handler.go`),
read the display name from context and add it as a JWT claim
(`x-tenant-display-name`). This propagates the real name to the
frontend for the entire session without any additional API calls.

**Staleness policy:** The display name in the JWT may become stale
if a tenant is renamed mid-session. This is acceptable - tenant
renames are rare administrative operations. The frontend will pick
up the updated name on next token refresh (session expiry or
explicit re-login). No token invalidation or forced refresh is
needed for this claim.

### 3. Public tenant info endpoint (backend)

Add `GET /api/tenant-info` to the gateway as a public endpoint
(no auth required, like `/api/auth/providers`). The endpoint:

- Uses the tenant resolver to identify the tenant from the subdomain
- Returns `{ slug, displayName }` as JSON
- Returns 404 if no valid tenant subdomain is present
- This serves the login page where no JWT exists yet

**Abuse protections:**

- The endpoint resolves tenant from the request's subdomain (Host
  header), not from a query parameter. There is no lookup-by-slug
  input - a caller must already be on the tenant's subdomain to
  get a response. This prevents enumeration: you cannot discover
  tenants you don't already have the subdomain for.
- Rate limited per IP (reuse the gateway's existing rate limiter)
- Positive responses cached via `Cache-Control: public, s-maxage=300`
  to reduce repeated lookups
- Error responses return a uniform 404 with identical body and
  timing regardless of whether the subdomain is invalid, unknown,
  or absent - the existing tenant resolver already does this
- Only slug and display name are returned - no tenant IDs, status,
  or internal metadata are exposed
- Suspended tenants still return their display name (users need to
  know where they are). Deprovisioned tenants return 404.

### 4. Frontend: consume tenant display name (frontend)

**Login page:**

- Call `/api/tenant-info` on mount when on a tenant subdomain
- Show the tenant's display name as the page title
- Fall back to formatted slug if the endpoint is unavailable
- Update document title to match

**Header:**

- For tenant users: read `x-tenant-display-name` from JWT claims
- For platform admins: use `currentTenant.name` (already available
  from tenant selector)
- Fall back to formatted slug (title-case, hyphens to spaces:
  `volterra-energy` becomes `Volterra Energy`), then to "Meridian"

**Document title:**

- Set `document.title` dynamically based on tenant context
- Pattern: `"{Tenant Name} - Operations Console"` on tenant
  subdomains, `"Meridian Operations Console"` on bare domain

## Non-Goals

- Tenant logos or custom color themes (future work, separate PRD)
- Custom favicon per tenant
- Tenant-specific email templates
- Whitelabeling (removing Meridian branding entirely)

## Success Criteria

1. Visiting `volterra-energy.demo.meridianhub.cloud/login` shows
   "Volterra Energy" as the heading (not "Meridian")
2. After login, the header shows "Volterra Energy" (not "Meridian")
3. Browser tab shows "Volterra Energy - Operations Console"
4. Platform admins see the selected tenant's name in the header,
   or "Meridian" when no tenant is selected
5. On bare domain (`demo.meridianhub.cloud`), "Meridian" branding
   is preserved

## Complexity Estimate

**8 story points total** across 3-4 PRs:

| PR | Points | Description |
|----|--------|-------------|
| Backend context + JWT | 3 | Tenant context display name, JWT claim propagation |
| Public endpoint | 2 | `/api/tenant-info` handler + tests |
| Frontend consumption | 3 | Login page, header, document title, auth context changes |

Backend PRs can merge independently. Frontend PR depends on both
backend PRs being deployed.
