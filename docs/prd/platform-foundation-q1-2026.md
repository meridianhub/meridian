---
name: prd-platform-foundation
description: Foundational infrastructure to deploy Meridian as a hosted SaaS platform
triggers:
  - Setting up Meridian deployment infrastructure
  - Implementing tenant signup and billing
  - Working on platform identity and authentication
  - Building the marketing site or signup flow
instructions: |
  This PRD covers the gap between "code on laptop" and "hosted platform with paying customers".
  Key concerns: deployment, identity hierarchy, self-billing, UI, and Stripe integration.
---

# Platform Foundation PRD

Date: 2026-01-06

## Status

Draft

## Overview

### Current State

Meridian exists as a working codebase on a development machine. To become a hosted SaaS platform, we need:

| Component | Current State | Required State |
|-----------|---------------|----------------|
| Deployment | None | Kubernetes + Helm |
| Identity/Auth | Basic tenant isolation | Full IAM with ownership hierarchy |
| Self-billing | None | Meridian billing Meridian customers |
| UI | None | Signup flow + dashboard |
| Marketing site | None | Pricing page + signup |
| Payment collection | None | Stripe integration |
| AI assistance | None | CEL generation + guardrails |
| Valuation engine | None | Market data + CEL valuation |

### The Identity Problem

There are THREE levels of identity to solve:

```
┌─────────────────────────────────────────────────────────────────┐
│                     MERIDIAN PLATFORM                           │
├─────────────────────────────────────────────────────────────────┤
│  Platform User: "alice@solarcoop.com"                           │
│  └── Authenticated via: Auth0/Clerk/etc                         │
│      └── Role: Owner                                            │
│          └── Organization: "Solar Coop Ltd" (Tenant)            │
│              └── Maps to: Party in Meridian's billing tenant    │
│                                                                 │
│  Inside Solar Coop's tenant:                                    │
│  └── Party: "Household 47" (their customer)                     │
│      └── Account: "Energy Credits"                              │
│          └── Position: 142 kWh                                  │
└─────────────────────────────────────────────────────────────────┘
```

**Three distinct concepts:**

| Concept | What It Is | Where It Lives |
|---------|------------|----------------|
| **Platform User** | Human who logs into Meridian | Auth provider (Auth0/Clerk) |
| **Organization (Tenant)** | Company using Meridian | Meridian tenant registry |
| **Party** | Entity being billed/tracked | Within a tenant's schema |

**The key insight:** When SolarCoop signs up, they become:
1. A **Platform User** (alice@solarcoop.com) - for authentication
2. An **Organization** (solarcoop) - which provisions a tenant schema
3. A **Party** in Meridian's own billing tenant - so we can bill them

---

## Architecture

### Identity Hierarchy

```
┌──────────────────────────────────────────────────────────────┐
│                    AUTH PROVIDER                              │
│                 (Auth0 / Clerk / etc)                         │
├──────────────────────────────────────────────────────────────┤
│  Users:                                                       │
│  ├── alice@solarcoop.com (user_123)                          │
│  ├── bob@solarcoop.com (user_456)                            │
│  └── charlie@acmecorp.com (user_789)                         │
└──────────────────────────────────────────────────────────────┘
                              │
                              │ JWT with user_id + org_id
                              ▼
┌──────────────────────────────────────────────────────────────┐
│                  MERIDIAN GATEWAY                             │
├──────────────────────────────────────────────────────────────┤
│  Organization Registry:                                       │
│  ├── org_solarcoop → tenant schema: "org_solarcoop"          │
│  └── org_acmecorp  → tenant schema: "org_acmecorp"           │
│                                                               │
│  User → Organization mapping:                                 │
│  ├── user_123 → org_solarcoop (role: owner)                  │
│  ├── user_456 → org_solarcoop (role: member)                 │
│  └── user_789 → org_acmecorp (role: owner)                   │
└──────────────────────────────────────────────────────────────┘
                              │
                              │ Sets search_path = org_solarcoop
                              ▼
┌──────────────────────────────────────────────────────────────┐
│               TENANT SCHEMA: org_solarcoop                    │
├──────────────────────────────────────────────────────────────┤
│  Parties (SolarCoop's customers):                            │
│  ├── party_household_47                                       │
│  ├── party_household_48                                       │
│  └── party_household_49                                       │
│                                                               │
│  Accounts, Positions, Measurements...                        │
└──────────────────────────────────────────────────────────────┘
```

### Meridian's Own Billing Tenant

Meridian itself is tenant `org_meridian_billing`:

```
┌──────────────────────────────────────────────────────────────┐
│            TENANT SCHEMA: org_meridian_billing                │
├──────────────────────────────────────────────────────────────┤
│  Parties (Meridian's customers = other tenants):             │
│  ├── party_solarcoop  ←── linked to org_solarcoop            │
│  └── party_acmecorp   ←── linked to org_acmecorp             │
│                                                               │
│  Account Types:                                               │
│  ├── SUBSCRIPTION_BASE (GBP)                                 │
│  ├── PARTY_USAGE (count)                                     │
│  ├── TRANSACTION_USAGE (count)                               │
│  ├── AI_GENERATION_CREDITS (count)                           │
│  └── API_CALL_USAGE (count)                                  │
│                                                               │
│  Positions:                                                   │
│  ├── party_solarcoop.SUBSCRIPTION_BASE: -£199 (owes us)      │
│  ├── party_solarcoop.PARTY_USAGE: 847 parties                │
│  └── party_solarcoop.TRANSACTION_USAGE: 47,231 this month    │
└──────────────────────────────────────────────────────────────┘
```

**This is the dog-fooding:** We bill our customers using Meridian.

---

## Work Streams

### 1. Deployment Infrastructure

| Task | Description | Priority |
|------|-------------|----------|
| 1.1 | Create Helm charts for all services | P0 |
| 1.2 | Set up Kubernetes cluster (GKE/EKS/DO) | P0 |
| 1.3 | Configure Postgres (Cloud SQL / RDS / managed) | P0 |
| 1.4 | Set up Redis for caching | P0 |
| 1.5 | Configure ingress + TLS | P0 |
| 1.6 | Set up CI/CD pipeline (GitHub Actions → deploy) | P0 |
| 1.7 | Monitoring + alerting (Prometheus/Grafana or managed) | P1 |
| 1.8 | Log aggregation (Loki or managed) | P1 |

**Deliverable:** `helm install meridian` works on a fresh cluster.

---

### 2. Identity & Authentication

| Task | Description | Priority |
|------|-------------|----------|
| 2.1 | Choose auth provider (Auth0 / Clerk / Supabase Auth) | P0 |
| 2.2 | Implement JWT validation in gateway | P0 |
| 2.3 | Create Organization registry table | P0 |
| 2.4 | Create User → Organization mapping table | P0 |
| 2.5 | Implement role-based access (owner, admin, member, viewer) | P0 |
| 2.6 | Signup flow: create user + org + tenant schema | P0 |
| 2.7 | Invite flow: add user to existing org | P1 |
| 2.8 | SSO/SAML for enterprise (Scale tier) | P2 |

**Key Decision: Auth Provider**

| Option | Pros | Cons |
|--------|------|------|
| **Auth0** | Mature, SAML, M2M tokens | Expensive at scale |
| **Clerk** | Modern DX, React components | Newer, less enterprise |
| **Supabase Auth** | Cheap, Postgres-native | Less mature RBAC |
| **Roll own (OIDC)** | Full control | Maintenance burden |

**Recommendation:** Start with Clerk (fast to implement, good DX), migrate to Auth0 if enterprise needs arise.

---

### 3. Self-Billing (Meridian Billing Meridian)

| Task | Description | Priority |
|------|-------------|----------|
| 3.1 | Create `org_meridian_billing` tenant | P0 |
| 3.2 | Define account types for usage tracking | P0 |
| 3.3 | Implement usage metering hooks | P0 |
| 3.4 | Link org to party in billing tenant on signup | P0 |
| 3.5 | Implement plan assignment (Starter/Growth/Scale) | P0 |
| 3.6 | Create billing run job (monthly) | P0 |
| 3.7 | Generate invoices from positions | P1 |
| 3.8 | Overage calculation CEL | P1 |

**Metering Hooks:**

```go
// Every API call increments usage
func (m *MeteringMiddleware) Handle(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        orgID := auth.GetOrgID(r.Context())

        // Record API call in billing tenant
        m.billingClient.RecordUsage(context.Background(), &RecordUsageRequest{
            PartyID:   orgID,  // Org is a party in billing tenant
            AssetType: "API_CALLS",
            Quantity:  1,
            Timestamp: time.Now(),
        })

        next.ServeHTTP(w, r)
    })
}
```

---

### 4. Stripe Integration

| Task | Description | Priority |
|------|-------------|----------|
| 4.1 | Create Stripe account for Meridian | P0 |
| 4.2 | Define Stripe products/prices for tiers | P0 |
| 4.3 | Implement Checkout Session for signup | P0 |
| 4.4 | Webhook handler for subscription events | P0 |
| 4.5 | Link Stripe customer ID to org | P0 |
| 4.6 | Sync Meridian invoices to Stripe for collection | P1 |
| 4.7 | Handle failed payments (dunning) | P1 |
| 4.8 | Customer portal for payment method updates | P1 |

**The Stripe ↔ Meridian Relationship:**

```
┌─────────────┐         ┌─────────────┐         ┌─────────────┐
│   Stripe    │         │  Meridian   │         │  Meridian   │
│  Payments   │◄───────►│  Gateway    │◄───────►│  Billing    │
│             │         │             │         │  Tenant     │
└─────────────┘         └─────────────┘         └─────────────┘
      │                        │                       │
      │                        │                       │
 Collects $$$          Maps customer         Tracks usage
 Handles cards         Syncs status          Calculates bill
 Dunning               Provisions tenant     Generates invoice
```

**Stripe is the payment rail. Meridian is the billing brain.**

---

### 5. Marketing Site & Signup Flow

| Task | Description | Priority |
|------|-------------|----------|
| 5.1 | Design pricing page (Starter/Growth/Scale) | P0 |
| 5.2 | Build landing page (Next.js or similar) | P0 |
| 5.3 | Implement signup flow (email → Stripe → provision) | P0 |
| 5.4 | Email verification + welcome sequence | P1 |
| 5.5 | Onboarding wizard (first account type, first party) | P1 |
| 5.6 | Documentation site | P1 |

**Signup Flow:**

```
┌──────────┐    ┌──────────┐    ┌──────────┐    ┌──────────┐
│  Choose  │───►│  Create  │───►│  Stripe  │───►│ Provision│
│   Plan   │    │  Account │    │ Checkout │    │  Tenant  │
└──────────┘    └──────────┘    └──────────┘    └──────────┘
                     │                │               │
                     ▼                ▼               ▼
               Auth provider    Payment method   Schema created
               User created     Subscription     Party in billing
               Org created      active           Accounts set up
```

---

### 6. Dashboard UI

| Task | Description | Priority |
|------|-------------|----------|
| 6.1 | Choose frontend stack (Next.js + Tailwind recommended) | P0 |
| 6.2 | Implement auth integration (Clerk/Auth0 React SDK) | P0 |
| 6.3 | Dashboard home (usage summary, balance) | P0 |
| 6.4 | Party list + detail view | P0 |
| 6.5 | Account type configuration | P1 |
| 6.6 | Transaction/measurement history | P1 |
| 6.7 | Billing/invoice view | P1 |
| 6.8 | Settings (team members, API keys) | P1 |

---

### 7. AI Assistance (Phase 2)

| Task | Description | Priority |
|------|-------------|----------|
| 7.1 | CEL generation prompt engineering | P2 |
| 7.2 | Guardrail validation for generated CEL | P2 |
| 7.3 | Executable example runner | P2 |
| 7.4 | Chat interface for "describe your pricing" | P2 |
| 7.5 | Template library (pre-built CEL for common patterns) | P2 |

---

### 8. Valuation Engine (Phase 2)

| Task | Description | Priority |
|------|-------------|----------|
| 8.1 | Market data feed adapter interface | P2 |
| 8.2 | FX rate provider integration | P2 |
| 8.3 | Energy spot price integration | P2 |
| 8.4 | Valuation CEL evaluation with market data context | P2 |
| 8.5 | Scheduled revaluation jobs | P2 |

---

## Milestones

### M1: "It Runs Somewhere" (4-6 weeks)

- [ ] Helm charts complete
- [ ] Deployed to staging cluster
- [ ] Basic auth working (Clerk)
- [ ] Can create tenant via API
- [ ] Stripe account exists

### M2: "First Signup" (4-6 weeks after M1)

- [ ] Marketing site live
- [ ] Signup → Stripe → Tenant provisioning works
- [ ] Basic dashboard (view parties, accounts)
- [ ] Meridian billing itself (metering active)
- [ ] First non-founder signs up

### M3: "Paying Customer" (4-6 weeks after M2)

- [ ] Billing run generates invoices
- [ ] Stripe collects payment
- [ ] Overage billing works
- [ ] Customer can self-serve (add parties, view usage)
- [ ] First payment received

### M4: "Product-Market Fit Pursuit" (ongoing)

- [ ] AI assistance (CEL generation)
- [ ] Valuation engine
- [ ] Mobile/API-first customers
- [ ] Enterprise features (SSO, audit export)

---

## Open Decisions

| Decision | Options | Recommendation |
|----------|---------|----------------|
| **Cloud provider** | GCP, AWS, DigitalOcean | Start with DO (simpler), migrate later |
| **Auth provider** | Auth0, Clerk, Supabase | Clerk (fastest to implement) |
| **Frontend stack** | Next.js, Remix, SvelteKit | Next.js (ecosystem, Vercel) |
| **Managed Postgres** | Cloud SQL, RDS, Supabase, Neon | Neon (serverless, cheap to start) |

---

## Cost Estimate (Monthly at Launch)

| Component | Service | Estimated Cost |
|-----------|---------|----------------|
| Kubernetes | DigitalOcean (3 nodes) | $60 |
| Postgres | Neon (Pro) | $20 |
| Redis | Upstash | $10 |
| Auth | Clerk (Pro) | $25 |
| Monitoring | Grafana Cloud (free tier) | $0 |
| Domain + DNS | Cloudflare | $0 |
| **Total** | | **~$115/month** |

Break-even: 3 Starter customers or 1 Growth customer.

---

## Related Documents

- [ADR-0016: Tenant Isolation](../adr/0016-tenant-id-naming-strategy.md)
- [Temporal Model Alignment PRD](temporal-model-alignment-q1-2026.md)
