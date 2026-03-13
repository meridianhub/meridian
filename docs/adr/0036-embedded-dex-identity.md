---
name: adr-0036-embedded-dex-identity
description: Embed Dex OIDC server in the Meridian binary for multi-tenant federated authentication
triggers:
  - Configuring identity providers for tenant authentication
  - Debugging OIDC/SSO authentication flows
  - Adding new identity federation connectors
instructions: |
  Dex runs embedded in the Meridian unified binary, not as a sidecar container.
  Enable via DEX_ISSUER env var. The MeridianConnector bridges Dex to the
  per-tenant identity domain, resolving tenant from the request subdomain.
  Authentication happens at external IdPs (Google, Okta, etc.) — Meridian
  stores user definitions (email, roles) but not passwords.
---

# 36. Embed Dex OIDC Server in the Meridian Binary

Date: 2026-03-13

## Status

Accepted (reinstated after revert)

## Context

Meridian is a multi-tenant platform where each tenant has its own user
definitions (email, roles, permissions) stored in tenant-scoped database
schemas. Authentication is federated — users authenticate via external identity
providers (Google Workspace, Okta, GitHub, SAML) rather than passwords managed
by Meridian.

Dex is a lightweight OIDC federation hub that connects to upstream identity
providers and issues ID tokens. The MeridianConnector bridges Dex's
`PasswordConnector` interface to Meridian's per-tenant identity domain,
enabling tenant-scoped credential validation and role assignment.

### History

Dex was first embedded in the Meridian binary (PRs #1518, #1523, #1525, #1526)
to simplify deployment and enable multi-tenant authentication. However, the
embedding was reverted (PR #1536) because:

1. **GitHub security scanner noise**: Dex's `go.mod` declares its module path
   as `github.com/dexidp/dex` without a `/v2` suffix despite being at major
   version 2. Go resolves this as a `v0.0.0-*` pseudo-version, causing
   Dependabot to flag all Dex CVEs as affecting version `0.0.0` — even when
   the actual commit includes all fixes.

2. **Transitive dependency CVEs**: Embedding Dex as a Go library pulled in
   Dex's full dependency tree (LDAP, SAML, OAuth connectors), inheriting their
   CVEs in Meridian's security scan.

The revert moved Dex to an external sidecar container (`ghcr.io/dexidp/dex:v2.41.1-alpine`),
which isolated the dependency but introduced operational problems:

- **No multi-tenant support**: Dex's connectors are global, not per-tenant.
  The sidecar has no access to Meridian's tenant-scoped identity domain.
- **Extra container**: Additional memory footprint on resource-constrained
  environments (the demo DigitalOcean droplet).
- **Configuration drift**: Dex config (`dex.yaml`) and Meridian config must
  be kept in sync manually. Missing connectors cause Dex to fail to start
  (`server: no connectors specified`).
- **MCP OAuth complexity**: The MCP server's OIDC flow required a separate
  `MCP_DEX_ISSUER_URL` env var, and browser redirects broke because the
  internal Docker hostname (`http://dex:5556/dex`) was used for browser-facing
  URLs (see PR #1651).

## Decision Drivers

* Multi-tenant federated authentication is a core platform requirement
* Each tenant must be able to configure its own identity provider
* The MeridianConnector (already built) provides the per-tenant bridge
* Deployment simplicity — fewer containers, less configuration surface
* MCP OAuth and UI SSO must share a single Dex session for seamless auth

## Considered Options

1. **Re-embed Dex in the Meridian binary** (with CVE noise acceptance)
2. **Keep Dex as external sidecar** (with per-connector workarounds)
3. **Replace Dex with Keycloak** (full-featured multi-tenant IdP)
4. **Replace Dex with a custom OIDC provider** (built into the BFF)

## Decision Outcome

Chosen option: **Re-embed Dex in the Meridian binary**, because it is the only
option that provides multi-tenant federated authentication without significant
new development or operational overhead.

### Positive Consequences

* **Multi-tenant native**: The MeridianConnector resolves tenant from the
  request subdomain and scopes all credential validation and role lookups to
  that tenant's schema.
* **Single binary deployment**: No Dex sidecar container. Saves ~50MB memory
  on the demo droplet. One less container to monitor, restart, and configure.
* **Shared OIDC sessions**: UI and MCP both authenticate through the same
  embedded Dex instance. If the user is already authenticated via the UI, the
  MCP OAuth flow reuses the Dex session cookie — no re-authentication needed.
* **Configuration co-location**: OIDC clients are registered in code
  (`DefaultDemoClient`) rather than in a separate `dex.yaml` file. Fewer moving
  parts to keep in sync.
* **Existing code**: The `services/identity/dex/` package, connector adapter,
  and gateway wiring were previously built, tested, and reviewed.

### Negative Consequences

* **Dependabot noise**: Dex's broken `go.mod` versioning means GitHub's
  dependency graph reports `v0.0.0` for the Dex module. Resolved CVEs may still
  appear as open alerts until Dex fixes their module path. Mitigation: add
  `.github/dependabot.yml` ignore rules for the Dex pseudo-version, and pin to
  a commit that includes all known CVE fixes.
* **Larger binary**: Dex's transitive dependencies (LDAP, SAML, OAuth connector
  libraries) increase the Go binary size by ~15-20MB. Acceptable for a unified
  binary deployment.
* **Dex upgrade path**: Upgrading Dex requires updating the pseudo-version in
  `go.mod` and testing compatibility. No tagged releases follow Go module
  conventions.

## Pros and Cons of the Options

### Option 1: Re-embed Dex (chosen)

* Good, because multi-tenant authentication works out of the box via MeridianConnector
* Good, because single binary simplifies deployment and reduces resource usage
* Good, because code already exists and was previously tested
* Bad, because Dependabot noise from Dex's broken go.mod versioning

### Option 2: Keep Dex as external sidecar

* Good, because dependency isolation (Dex CVEs don't appear in Meridian scans)
* Bad, because no multi-tenant support (connectors are global)
* Bad, because extra container, config file, and network hop
* Bad, because MCP OAuth browser redirects required workarounds (PR #1651)

### Option 3: Replace Dex with Keycloak

* Good, because native multi-tenant support via "realms"
* Good, because full-featured identity management UI
* Bad, because heavy memory footprint (~500MB+), too much for demo droplet
* Bad, because significant migration effort from existing Dex integration

### Option 4: Custom OIDC provider in BFF

* Good, because full control over multi-tenant auth flow
* Bad, because significant development effort to implement OIDC correctly
* Bad, because reinventing what Dex already provides (federation, token signing, discovery)

## Links

* PR #1518 — Original embedded Dex package
* PR #1523 — Wiring into unified binary and gateway
* PR #1536 — Revert to external sidecar (security scanner noise)
* PR #1651 — MCP OAuth browser redirect fix (sidecar workaround)
* [Dex GitHub](https://github.com/dexidp/dex) — OIDC federation hub
* [Dex go.mod issue](https://github.com/dexidp/dex/issues/2942) — Module path versioning

## Notes

* **Dependabot suppression**: If scanner noise becomes unacceptable, add
  `ignore` rules in `.github/dependabot.yml` for `github.com/dexidp/dex`.
  The actual security posture is determined by the pinned commit hash, not the
  pseudo-version number.
* **Future: per-tenant IdP configuration**: The MeridianConnector currently
  handles password authentication. Federated login (Google, SAML) requires
  configuring Dex connectors. A future enhancement will allow tenants to
  self-service configure their IdP, stored in Meridian's tenant settings and
  applied to Dex at runtime.
* **Reconsidering trigger**: If Dex stops being maintained or the Go module
  versioning issue persists past v3.x, consider migrating to Zitadel or a
  custom OIDC implementation.
