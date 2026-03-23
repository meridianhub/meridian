# Demo Sandbox: Self-Service AI Economy Creation

## Problem Statement

Meridian's core value proposition - "define an economy by conversation,
run it continuously" - has no live demonstration. The demo environment
at demo.meridianhub.cloud exists but lacks self-service onboarding,
fixture data, and the nightly reset needed to function as a public
sandbox.

## Technical Context

### What Exists

| Capability | Status | Evidence |
|-----------|--------|---------|
| Multi-tenant schema provisioning | Working | PR #1839 |
| Manifest validation + apply via saga | Working | PRs #1831, #1833, #1834 |
| Economy visualization in UI | Working | Confirmed on demo 2026-03-22 |
| MCP server with `meridian_economy_generate` | Working | economy-generator marathon |
| Dex identity / BFF auth flow | Working | 1598-auth marathon |
| seed-dev with fixture data | Broken | Task 11 |
| Manifest apply on existing tenants | Broken | Task 12 |
| Party -> Account -> Transactions UI flow | Unverified | Task 13 |

### Infrastructure

- Docker Compose: meridian (unified binary), postgres, caddy, dex
- CI builds `ghcr.io/meridianhub/meridian:demo` on push to `demo` branch
- Domain: demo.meridianhub.cloud (Cloudflare origin cert via Caddy)

## Solution

### The Demo Loop

```text
1. Visit demo.meridianhub.cloud
2. Sign up / select tenant slug (e.g., "my-solar-company")
3. Tenant provisioned automatically (schema, admin user, manifest)
4. Log in via Dex SSO
5. Dashboard shows MCP connection details:
   - Server URL: demo.meridianhub.cloud/mcp
   - Tenant: my-solar-company
   - Auth: Bearer token from login
6. User connects Claude/GPT via MCP
7. AI generates manifest: "I run a solar panel company..."
8. AI applies manifest via economy_generate + apply_manifest
9. User refreshes UI - sees their economy
10. Tomorrow at midnight UTC - database wiped, fresh start
```

### Nightly Reset (GitHub Action)

```yaml
# .github/workflows/demo-reset.yml
schedule:
  - cron: '0 0 * * *'  # Midnight UTC daily

steps:
  - SSH to droplet
  - docker compose down
  - Drop and recreate database
  - docker compose up -d
  - Run --migrate
  - Run --bootstrap (seeds platform manifest, provisions schemas)
  - Run seed-dev (creates demo tenant with admin user,
    applies manifest, seeds fixture data)
  - Health check
```

**Why nightly reset:**

- Prevents data accumulation from experiments
- No one can use demo as production (data is ephemeral)
- Clean slate means reproducible demos
- Reduces support burden (broken state self-heals)

### Self-Service Tenant Creation

The tenant creation API exists (`TenantService.CreateTenant`).
What's needed:

1. **Public registration endpoint** in the API gateway:
   - POST /api/v1/register with `{slug, email, password}`
   - Creates tenant via gRPC
   - Provisions admin user in Dex
   - Returns login URL

2. **Registration UI page** (unauthenticated):
   - Slug input with availability check
   - Email + password
   - Submit -> redirect to login

3. **MCP connection card** on dashboard:
   - Show connection URL, tenant slug, how to get auth token
   - Copy-to-clipboard for MCP config JSON
   - Link to Claude Code / ChatGPT / Cursor setup docs

### Guardrails for Economy Definitions

The manifest validation pipeline already catches structural errors.
For semantic validation ("this economy makes sense"), add:

1. **Referential integrity**: Every account type references a valid
   instrument code
2. **Settlement completeness**: Settlement flows reference valid
   clearing accounts
3. **Instrument consistency**: Valuation rules reference defined
   instruments
4. **Organization completeness**: Account types assigned to
   organizations that exist

These are CEL validation expressions on the manifest proto -
evaluated at apply time, before the saga executes.

## Scope

### In Scope

- Fix seed-dev fixtures (manifest-integrity task 11)
- Fix manifest apply org deletion (manifest-integrity task 12)
- Verify party -> account -> transactions flow (task 13)
- Nightly reset GitHub Action
- Self-service tenant registration (API + UI)
- MCP connection details card in UI
- Manifest semantic validation (referential integrity checks)

### Out of Scope

- Production multi-tenant (this is demo-only)
- Billing / usage metering
- Custom domain per tenant
- Data export / backup
- SLA guarantees

## Success Criteria

1. A new user can go from demo.meridianhub.cloud to a running
   economy in under 10 minutes
2. The AI conversation (via MCP) can generate and apply a valid
   manifest for any industry described in natural language
3. After manifest apply, the Economy page shows all defined
   instruments, account types, and organizations
4. The nightly reset runs without manual intervention and the
   demo is healthy by 00:15 UTC
5. Invalid economies are rejected at apply time with clear
   error messages

## Complexity Estimate

| Component | Points | Notes |
|-----------|--------|-------|
| Fix seed-dev + manifest apply (tasks 11-13) | 5 | Existing tasks |
| Nightly reset GitHub Action | 2 | SSH + docker compose |
| Self-service registration API | 5 | Gateway + Dex |
| Registration UI page | 3 | Single form |
| MCP connection card | 2 | Dashboard widget |
| Manifest semantic validation | 5 | CEL expressions |
| **Total** | **22** | Critical path: 11-13 -> reg -> MCP |
