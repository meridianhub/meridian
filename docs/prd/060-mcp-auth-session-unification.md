# PRD: MCP OAuth Session Unification

## Problem Statement

The MCP (Model Context Protocol) OAuth flow bypasses the Meridian UI entirely and redirects
users to the embedded Dex OIDC login page directly. This creates three problems:

1. **Double login**: Users must enter credentials twice - once for the UI, once for MCP -
   even though both systems share the same JWT signing key and identity backend.
2. **No session reuse**: Dex has no session mechanism (no cookies, no server-side sessions).
   There is no "already logged in" state to leverage.
3. **Leaky abstraction**: Dex is an internal identity backend. Users should never see the
   Dex login page. The Meridian UI should be the single authentication surface.

### Current Architecture

Three auth flows exist:

| Flow | Path | User Experience |
|------|------|-----------------|
| BFF Password | `POST /api/auth/login` | Meridian UI login form, no Dex involvement |
| BFF SSO | `GET /api/auth/sso/{connector}` -> Dex -> `GET /api/auth/callback` | Dex is invisible - BFF controls redirects |
| MCP OAuth | `GET /oauth/authorize` -> Dex login page directly | User sees raw Dex UI, must re-authenticate |

The BFF SSO flow is the correct pattern - Dex stays behind the BFF and the user never
interacts with it. MCP should follow the same pattern.

### Shared Infrastructure (Already Exists)

- **Same JWT signing key**: BFF and MCP use the same RSA key (`JWT_SIGNING_KEY`). Tokens
  are already interchangeable.
- **Same identity backend**: Both validate against the same identity database via the
  embedded Dex connector.
- **Same Dex client**: Both use `meridian-service` client ID.
- **Same tenant resolution**: Both extract tenant from subdomain.

## Proposed Solution

Replace MCP's direct Dex redirect with a redirect to the Meridian UI. The SPA consent page
checks the user's existing session (JWT in sessionStorage). If logged in, it shows a consent
screen. If not, it shows the normal login flow first, then consent. After implementation,
remove the old Dex-direct flow entirely - no feature flags, no fallback.

### End-to-End Flow

```text
1. Claude Code POST /mcp -> 401 with auth metadata (unchanged)

2. Claude Code opens browser -> GET /oauth/authorize
   -> MCP validates client_id, PKCE, redirect_uri (unchanged)
   -> MCP stores OIDCFlowState {PKCE challenge, client_id, redirect_uri,
      state, tenant, scopes}
   -> MCP 302 -> https://{tenant}.{baseDomain}/oauth/consent?mcp_state=
      {key}&client_id={id}
   [CHANGED: was Dex redirect, now UI redirect]

3. Browser loads SPA consent page
   -> SPA checks sessionStorage for JWT
   -> If no JWT: redirect to /login with return_url
   -> After login (or if already logged in):
      SPA fetches GET /oauth/consent-info?client_id=...&mcp_state=...
   -> MCP server validates state exists, returns trusted client metadata
   -> SPA renders consent card with client name, redirect URI, scopes,
      tenant, approve/deny
   [NEW: entire step]

4. User clicks "Authorize"
   -> SPA POST /api/auth/mcp-consent {mcp_state, client_id}
      with Authorization: Bearer {jwt}
   -> BFF auth middleware validates JWT, tenant resolver sets tenant context
   -> BFF extracts email + tenant from JWT claims
   -> BFF generates one-time consent code, stores {email, tenant,
      mcp_state, client_id, scopes}
   -> BFF returns JSON {redirect_url: "/oauth/callback?code=
      {consent_code}&state={mcp_state}"}
   [NEW: entire step]

5. SPA navigates to /oauth/callback?code={consent_code}&state={mcp_state}
   -> MCP HandleCallback consumes state (same as today)
   -> MCP consumes consent code from shared store
      [REPLACES: Dex code exchange]
   -> MCP cross-validates: consent code's mcp_state + client_id match
      flow state
   -> MCP extracts email + tenant from consent code entry
   -> MCP signs fresh scoped JWT {sub, email, x-tenant-id, scopes}
      (same pattern as today)
   -> MCP generates MCP auth code, stores in CodeStore (same as today)
   -> MCP 302 -> Claude Code's redirect_uri?code={mcp_code}&state=
      {mcp_state}
   [CHANGED: identity source is consent code, not Dex ID token]

6. Claude Code exchanges auth code for JWT at POST /oauth/token (unchanged)
```

### What Gets Removed

- `buildDexRedirect` - replaced by `buildConsentRedirect`
- `exchangeDexCode` - replaced by consent code consumption
- `BuildTenantScopedDexURL` (for MCP) - consent page is on same origin
- Inner PKCE leg (MCP-to-Dex) - no longer needed
- `DexCodeVerifier` field in `OIDCFlowState` - removed
- `MCP_DEX_ISSUER_URL`, `MCP_DEX_CLIENT_ID`, `MCP_DEX_CALLBACK_URL` env vars from
  MCP server - no longer needed for MCP flow
- All Dex-related imports and helpers in MCP OAuth handler

### What Stays

- All Dex infrastructure (BFF SSO still uses it)
- Outer PKCE chain (client-to-MCP) - unchanged
- MCP state store and code store - unchanged patterns
- JWT signing and JWKS endpoint - unchanged

## Component Changes

### 1. MCP Server (`services/mcp-server/internal/auth/oidc.go`)

**Modify `HandleAuthorize`**: Replace `buildDexRedirect` with `buildConsentRedirect`
that redirects to the UI consent page URL with `mcp_state` and `client_id` query params.
Store `requested_scopes` from the authorize request in `OIDCFlowState`.

**New endpoint `GET /oauth/consent-info`**: Returns trusted client metadata (client_name,
redirect_uri, scopes) after validating the `mcp_state` exists in the state store.
Unauthenticated endpoint - returns display data only. Cross-checks `client_id` in URL
matches client_id in state. For dynamically registered clients, include `is_dynamic: true`
so the consent page can flag them as unverified.

**Modify `HandleCallback`**: Accept consent codes from the BFF instead of Dex authorization
codes. Consume the consent code from the shared `ConsentCodeStore`, cross-validate
`mcp_state` and `client_id` against the flow state, extract identity (email, tenant), then
proceed to `issueCodeAndRedirect` (unchanged). Include `scopes` claim in the signed JWT.

**New `OIDCStateStore.Peek` method**: Non-consuming read that returns selected fields
(client_id, redirect_uri, scopes) for the consent-info endpoint.

**Remove Dex-direct code**: Delete `buildDexRedirect`, `exchangeDexCode`,
`BuildTenantScopedDexURL`, inner PKCE generation, `DexCodeVerifier` from `OIDCFlowState`,
and all Dex-specific env var handling (`MCP_DEX_ISSUER_URL`, `MCP_DEX_CLIENT_ID`,
`MCP_DEX_CALLBACK_URL`). Remove the OIDC discovery client, Dex token exchange HTTP client,
and related helpers. The MCP server no longer talks to Dex.

### 2. BFF / API Gateway (`services/api-gateway/`)

**New endpoint `POST /api/auth/mcp-consent`**: Behind full auth middleware chain (JWT
validated, tenant resolved). Accepts `{mcp_state, client_id, denied?}`. On approve:
generates one-time consent code, stores identity in `ConsentCodeStore`, returns
`{redirect_url}` pointing to MCP callback. On deny: consumes MCP state, returns
`{redirect_url}` pointing to client's redirect_uri with `error=access_denied` and the
client's original state.

**New `ConsentCodeStore`**: In-memory store, same pattern as MCP's `CodeStore`. One-time
consumption, 2-minute TTL, capped at 10,000 entries, background eviction.

### 3. Frontend (`frontend/src/`)

**New route**: `/oauth/consent` in `App.tsx`.

**New page component**: `OAuthConsentPage` - checks auth state, fetches client metadata
from `/oauth/consent-info`, renders consent card, handles approve/deny.

**New display component**: `ConsentCard` - shows application name, tenant context, scope
description, redirect URI, approve/deny buttons. For dynamically registered clients (where
`is_dynamic: true`), show "Unverified application" badge. Styled consistently with existing
login page.

**No changes to**: login page, callback page, auth context, auth interceptor, SSO flow.

### 4. Wiring (`cmd/meridian/`)

Shared `ConsentCodeStore` created once and passed to both BFF's `MCPConsentHandler` and
MCP's `OIDCHandler`. Shared `OIDCStateStore` also passed to BFF handler (needed for deny
flow and redirect_uri lookup).

### 5. Cleanup

**Remove from docker-compose env vars**: `MCP_DEX_ISSUER_URL`, `MCP_DEX_CLIENT_ID`,
`MCP_DEX_CALLBACK_URL` from both demo and develop compose files and `.env` templates.
These are no longer consumed by the MCP server.

**Remove from Dex client registration**: The `/oauth/callback` redirect URI in
`DefaultDemoClient` is no longer needed for Dex (the MCP callback now receives consent
codes from the BFF, not Dex codes). Keep it only if the BFF SSO flow still uses it.
If not, remove.

**Update deploy docs**: Remove any references to MCP-specific Dex configuration from
`deploy/demo/README.md` and related documentation.

## Security Requirements

All mandatory - no "recommended" tier. Everything listed here ships or the PRD isn't done.

1. **No bearer tokens in URLs**: JWTs must never appear as URL query parameters. Only
   opaque one-time codes travel in redirects.
2. **One-time code consumption**: `Consume()` must be atomic - concurrent calls for the
   same code return success for exactly one caller.
3. **Tenant binding chain**: `MCP state.tenantSlug == BFF JWT.x-tenant-id ==
   consent code.tenantID` must hold end-to-end. Break at any link means rejection.
4. **PKCE integrity**: The outer PKCE chain (client-to-MCP) must be preserved unmodified
   by the consent flow.
5. **Explicit consent**: BFF consent endpoint only callable via POST with Bearer auth.
   No auto-approve, no GET-based approval.
6. **Fresh scoped JWT**: MCP callback signs a new JWT with minimal claims (`sub`, `email`,
   `x-tenant-id`, `scopes`). BFF JWT roles and other claims must not propagate.
7. **Client identity binding**: Consent code `client_id` must match flow state `client_id`.
8. **Server-side client metadata**: Consent page fetches client_name from server by
   client_id. Never trusts URL params for display.
9. **Display redirect_uri**: Consent screen shows where credentials will be sent
   (unforgeable client identifier).
10. **Scope model**: `requested_scopes` in `OIDCFlowState`, `approved_scopes` in
    `ConsentCodeEntry`, `scopes` claim in MCP JWT. v1 value: `["mcp:default"]`.
11. **Dynamic client flagging**: Consent screen shows "Unverified application" badge for
    dynamically registered clients.

### Consent Code Specification

- **Entropy**: 32 bytes crypto/rand, base64url-encoded (43 chars)
- **TTL**: 2 minutes (shorter than MCP state's 10-min TTL)
- **Store cap**: 10,000 entries with background eviction
- **Binding**: email, tenant_id, tenant_slug, mcp_state, client_id, approved_scopes,
  created_at

## UX Specification

### Consent Card

```text
+-------------------------------------------+
|         Authorize Application              |
|                                            |
|  "Claude Code" wants to access your        |
|  Volterra Energy account.                  |
|                                            |
|  [! Unverified application]                |
|                                            |
|  This will allow:                          |
|  - Full access to your account             |
|                                            |
|  Credentials will be sent to:              |
|  http://localhost:12345/callback            |
|                                            |
|  [Deny]                   [Authorize]      |
+-------------------------------------------+
```

The "Unverified application" badge only appears for dynamically registered clients
(`is_dynamic: true`).

### States

1. **Loading**: SPA bundle loading, auth check, client metadata fetch - show spinner
2. **Unauthenticated**: No JWT in sessionStorage - redirect to `/login?return_url=...`
3. **Authenticated**: JWT found - render consent card with server-fetched metadata
4. **Error - invalid state**: "This authorization request has expired. Please try again
   from Claude Code."
5. **Error - invalid client**: "This authorization request is invalid. The application
   could not be found."
6. **Submitting**: Approve button disabled, spinner overlay
7. **Denied**: Redirect to client with `error=access_denied`

### API Contracts (Frontend View)

**GET `/oauth/consent-info?client_id=...&mcp_state=...`** (MCP server, unauthenticated)

- 200: `{ client_id, client_name, redirect_uri, scopes, is_dynamic }`
- 400: invalid/expired state or client_id mismatch

**POST `/api/auth/mcp-consent`** (BFF, requires Bearer JWT)

- Request: `{ mcp_state, client_id, denied?: boolean }`
- 200 (approved): `{ redirect_url: "/oauth/callback?code=...&state=..." }`
- 200 (denied): `{ redirect_url: "https://client/callback?error=access_denied&state=..." }`
- 400: `{ error: "invalid_state" | "state_expired" | "client_mismatch" }`
- 401: invalid/expired JWT

## Acceptance Criteria

### Backend

1. MCP `/oauth/authorize` redirects to UI consent page (not Dex)
2. MCP `/oauth/consent-info` returns trusted client metadata including `redirect_uri`,
   `scopes`, and `is_dynamic` after validating state
3. BFF `POST /api/auth/mcp-consent` requires valid JWT, issues one-time consent code
4. MCP `/oauth/callback` accepts consent codes and cross-validates against flow state
5. MCP `/oauth/token` returns scoped JWT with `sub`, `email`, `x-tenant-id`, `scopes`
6. BFF SSO flow via Dex continues to work unchanged
7. Consent codes have 2-min TTL, one-time-use, capped store with eviction
8. Tenant cross-check: consent code tenant must match flow state tenant
9. Deny flow redirects client with `error=access_denied` and original client state
10. All Dex-direct code removed from MCP OAuth handler (no `buildDexRedirect`,
    no `exchangeDexCode`, no inner PKCE)
11. `MCP_DEX_ISSUER_URL`, `MCP_DEX_CLIENT_ID`, `MCP_DEX_CALLBACK_URL` env vars removed
    from MCP server and docker-compose configs

### Frontend

1. User with existing session sees consent card within 2 seconds
2. User without session is redirected to login, then back to consent
3. Client name on consent card matches server-registered name
4. Redirect URI is visible on consent card
5. "Authorize" button disabled until client metadata loads
6. "Deny" redirects MCP client with `error=access_denied`
7. Expired/invalid `mcp_state` shows clear error message
8. Invalid `client_id` shows clear error message
9. Consent page uses same styling as login page
10. Dynamically registered clients show "Unverified application" badge

### Security

1. No JWTs appear in any redirect URL at any point in the flow
2. Consent codes are consumed exactly once (atomic)
3. PKCE chain works end-to-end (client code_verifier verified at `/oauth/token`)
4. Cross-tenant state replay is rejected
5. `scopes` claim present in MCP-issued JWT

### End-to-End

1. Full flow works: Claude Code -> authorize -> consent -> approve -> callback ->
   token exchange -> authenticated MCP session
2. Full flow works when user is NOT logged in: Claude Code -> authorize -> consent ->
   login -> consent -> approve -> callback -> token exchange
3. Deny flow works: Claude Code -> authorize -> consent -> deny -> Claude Code receives
   `error=access_denied`
4. Demo environment: Volterra Energy operator can authenticate MCP via the new consent flow
