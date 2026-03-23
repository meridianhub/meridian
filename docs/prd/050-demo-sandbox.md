# Demo Sandbox: Self-Service AI Economy Creation

## Problem Statement

Meridian's core value proposition - "define an economy by conversation, run it continuously" - has no live demonstration. The demo environment at demo.meridianhub.cloud exists but lacks self-service onboarding, fixture data, and the nightly reset needed to function as a public sandbox.

## Technical Context

### What Exists

| Capability | Status | Evidence |
|-----------|--------|---------|
| Multi-tenant schema provisioning | Working | PR #1839 - bootstrap derives DSN from DATABASE_URL |
| Manifest validation + apply via saga | Working | PRs #1831, #1833, #1834 - handler param validation |
| Economy visualization in UI | Working | Confirmed on demo 2026-03-22 |
| MCP server with `meridian_economy_generate` | Working | economy-generator marathon (12 PRs) |
| Dex identity / BFF auth flow | Working | 1598-auth marathon (6 PRs) |
| seed-dev with fixture data (parties, accounts, 30 days deposits) | Broken | Task 11 - fixtures skip with --skip-manifest |
| Manifest apply on existing tenants | Broken | Task 12 - org deletion not supported |
| Party -> Account -> Transactions UI flow | Unverified | Task 13 - needs fixture data first |

### Infrastructure

- DigitalOcean Droplet (68.183.40.239)
- Docker Compose: meridian (unified binary), postgres, caddy (reverse proxy), dex (OIDC)
- CI: GitHub Actions builds `ghcr.io/meridianhub/meridian:demo` on push to `demo` branch
- Domain: demo.meridianhub.cloud (Cloudflare origin cert via Caddy)

## Solution

### The Demo Loop

```
1. Visit demo.meridianhub.cloud
2. Sign up / select tenant slug (e.g., "my-solar-company")
3. Tenant provisioned automatically (schema, admin user, default manifest)
4. Log in via Dex SSO
5. Dashboard shows MCP connection details:
   - Server URL: demo.meridianhub.cloud/mcp
   - Tenant: my-solar-company
   - Auth: Bearer token from login
6. User connects Claude/GPT via MCP
7. AI generates manifest: "I run a solar panel installation company..."
8. AI applies manifest via meridian_economy_generate + apply_manifest
9. User refreshes UI - sees their economy (instruments, account types, orgs)
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
  - Run --bootstrap (seeds platform manifest)
  - Health check
```

**Why nightly reset:**
- Prevents data accumulation from experiments
- No one can use demo as production (data is ephemeral)
- Clean slate means reproducible demos
- Reduces support burden (broken state self-heals)

### Self-Service Tenant Creation

The tenant creation API exists (`TenantService.CreateTenant`). What's needed:

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

The manifest validation pipeline already catches structural errors. For semantic validation ("this economy makes sense"), add:

1. **Referential integrity**: Every account type references a valid instrument code
2. **Settlement completeness**: Settlement flows reference valid clearing accounts
3. **Instrument consistency**: Valuation rules reference defined instruments
4. **Organization completeness**: Account types assigned to organizations that exist

These are CEL validation expressions on the manifest proto - evaluated at apply time, before the saga executes.

## Scope

### In Scope
- Fix seed-dev fixtures (manifest-integrity task 11)
- Fix manifest apply org deletion (manifest-integrity task 12)
- Verify party -> account -> transactions demo flow (manifest-integrity task 13)
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

1. A new user can go from demo.meridianhub.cloud to a running economy in under 10 minutes
2. The AI conversation (via MCP) can generate and apply a valid manifest for any industry described in natural language
3. After manifest apply, the Economy page shows all defined instruments, account types, and organizations
4. The nightly reset runs without manual intervention and the demo is healthy by 00:15 UTC
5. Invalid economies (missing instruments, circular dependencies, orphaned account types) are rejected at apply time with clear error messages

## Complexity Estimate

| Component | Points | Notes |
|-----------|--------|-------|
| Fix seed-dev + manifest apply (tasks 11-13) | 5 | Existing tasks, clear fixes |
| Nightly reset GitHub Action | 2 | SSH + docker compose script |
| Self-service registration API | 5 | New gateway endpoint, Dex user provisioning |
| Registration UI page | 3 | Single form, availability check |
| MCP connection card | 2 | Dashboard widget, copy-to-clipboard |
| Manifest semantic validation | 5 | CEL expressions, referential integrity |
| **Total** | **22** | Critical path: tasks 11-13 -> registration -> MCP card |
