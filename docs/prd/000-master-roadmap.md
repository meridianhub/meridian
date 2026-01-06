---
name: prd-master-roadmap
description: Master roadmap of all PRDs and work streams to go-live
triggers:
  - Planning overall Meridian development priorities
  - Asking what needs to be done to launch
  - Reviewing project status or gaps
instructions: |
  This is the PRD of PRDs. It defines all work streams, their dependencies,
  and complexity. Use this to understand what exists, what's missing, and
  where to focus. Complexity uses Fibonacci (1,2,3,5,8,13,21).
---

# Master Roadmap: Meridian Go-Live

Date: 2026-01-06

## Status

Draft - Living Document

---

## Executive Summary

Meridian is a universal position-keeping engine. To become a hosted SaaS platform, we need to close gaps across 8 work streams.

```
┌─────────────────────────────────────────────────────────────┐
│                    MERIDIAN PLATFORM                        │
├──────────────┬──────────────┬──────────────┬───────────────┤
│   CORE       │   PLATFORM   │   PRODUCT    │   GROWTH      │
│   ENGINE     │   INFRA      │   SURFACE    │   FEATURES    │
├──────────────┼──────────────┼──────────────┼───────────────┤
│ ✓ Position   │ ○ Deploy     │ ✗ Web UI     │ ✗ AI Assist   │
│ ✓ Accounting │ ○ Identity   │ ✗ Marketing  │ ✗ Valuation   │
│ ✓ Accounts   │ ○ Self-Bill  │ ○ API/DX     │ ✗ Marketplace │
│ ✓ Reference  │ ○ Gateway    │              │               │
└──────────────┴──────────────┴──────────────┴───────────────┘

✓ = Exists   ○ = Partial/Needs Work   ✗ = Not Started
```

---

## Work Stream Index

| ID | Work Stream | Status | Complexity | Dependencies | PRD |
|----|-------------|--------|------------|--------------|-----|
| **WS-1** | Core Engine | 80% | — | — | Various ADRs |
| **WS-2** | Deployment | 10% | 13 | WS-1 | [Platform Foundation](platform-foundation-q1-2026.md) |
| **WS-3** | Identity & Access | 5% | 13 | WS-2 | [Platform Foundation](platform-foundation-q1-2026.md) |
| **WS-4** | Self-Billing | 0% | 8 | WS-3 | [Platform Foundation](platform-foundation-q1-2026.md) |
| **WS-5** | API Gateway & DX | 40% | 8 | WS-2, WS-3 | TBD |
| **WS-6** | Web UI | 0% | 21 | WS-3, WS-5 | TBD |
| **WS-7** | Marketing Site | 0% | 5 | WS-4 | TBD |
| **WS-8** | AI Assistance | 0% | 13 | WS-6 | TBD |
| **WS-9** | Valuation Engine | 0% | 13 | WS-1 | TBD |

---

## Dependency Graph

```
                    WS-1: Core Engine
                           │
              ┌────────────┼────────────┐
              ▼            ▼            ▼
        WS-2: Deploy   WS-9: Valuation  │
              │                         │
              ▼                         │
        WS-3: Identity                  │
              │                         │
       ┌──────┴──────┐                  │
       ▼             ▼                  │
  WS-4: Self    WS-5: API/DX            │
  Billing            │                  │
       │             │                  │
       ▼             ▼                  │
  WS-7: Marketing    WS-6: Web UI ◄─────┘
  Site                    │
                          ▼
                    WS-8: AI Assist
```

**Critical Path:** WS-1 → WS-2 → WS-3 → WS-4 → WS-7 (Minimum viable signup)

**Parallel Track:** WS-9 (Valuation) can proceed independently after WS-1

---

## Work Stream Details

### WS-1: Core Engine

**Status:** 80% complete

The BIAN-inspired services that form Meridian's foundation. This has evolved beyond Bank-in-a-Box into a universal position-keeping engine.

| Component | Status | Notes |
|-----------|--------|-------|
| Position Keeping | ✓ | Measurements, positions, buckets |
| Financial Accounting | ✓ | Ledger, journal entries |
| Current Accounts | ✓ | Account lifecycle |
| Reference Data | ✓ | Instrument registry, CEL validation |
| Party Management | ○ | Basic exists, needs enrichment |
| Temporal Model | ○ | See [Temporal PRD](temporal-model-alignment-q1-2026.md) |

**Gaps:**
- [ ] Temporal period columns (PRD exists)
- [ ] Settlement/reconciliation service
- [ ] Event publishing (domain events → integration events)

**Complexity:** Ongoing maintenance, not a discrete deliverable

---

### WS-2: Deployment Infrastructure

**Status:** 10% (Docker exists, nothing else)

| Task | Complexity | Status |
|------|------------|--------|
| Helm charts for all services | 5 | ✗ |
| Kubernetes cluster setup | 3 | ✗ |
| Managed Postgres (Neon/CloudSQL) | 2 | ✗ |
| Redis for caching | 1 | ✗ |
| CI/CD pipeline | 3 | ○ Partial |
| Monitoring + alerting | 3 | ✗ |
| Log aggregation | 2 | ✗ |

**Total Complexity:** 13

**Deliverable:** `helm install meridian` on fresh cluster

**Blocked by:** Nothing

**Blocks:** WS-3, WS-5

---

### WS-3: Identity & Access Management

**Status:** 5% (tenant isolation exists, no auth)

| Task | Complexity | Status |
|------|------------|--------|
| Choose auth provider (Clerk/Auth0) | 1 | ✗ |
| JWT validation in gateway | 2 | ✗ |
| Organization registry | 3 | ✗ |
| User → Org mapping + roles | 3 | ✗ |
| Signup flow (create user + org + schema) | 5 | ✗ |
| Invite flow (add user to org) | 2 | ✗ |
| SSO/SAML (enterprise) | 3 | ✗ Defer |

**Total Complexity:** 13 (excluding SSO)

**Key Decision:** Auth provider choice

**Deliverable:** User can sign up, org is created, tenant schema provisioned

**Blocked by:** WS-2

**Blocks:** WS-4, WS-5, WS-6

---

### WS-4: Self-Billing (Meridian Billing Meridian)

**Status:** 0%

| Task | Complexity | Status |
|------|------------|--------|
| Create billing tenant schema | 1 | ✗ |
| Define billing account types | 2 | ✗ |
| Link org → party on signup | 2 | ✗ |
| Usage metering middleware | 3 | ✗ |
| Plan assignment (Starter/Growth/Scale) | 2 | ✗ |
| Billing run job | 3 | ✗ |
| Stripe integration (checkout, webhooks) | 5 | ✗ |
| Invoice generation | 3 | ✗ |
| Overage calculation | 2 | ✗ |

**Total Complexity:** 8 (core) + 5 (Stripe) = 13

**Deliverable:** Customer signs up, pays via Stripe, usage is tracked, invoice generated

**Blocked by:** WS-3

**Blocks:** WS-7

---

### WS-5: API Gateway & Developer Experience

**Status:** 40% (gateway exists, docs incomplete)

| Task | Complexity | Status |
|------|------------|--------|
| OpenAPI spec complete | 3 | ○ Partial |
| API documentation site | 2 | ✗ |
| API key management | 3 | ✗ |
| Rate limiting | 2 | ✗ |
| Webhook delivery system | 5 | ✗ |
| SDK generation (Go, TS) | 3 | ✗ |

**Total Complexity:** 8 (MVP) / 18 (full)

**Deliverable:** Developer can read docs, get API key, make authenticated calls

**Blocked by:** WS-2, WS-3

**Blocks:** WS-6

---

### WS-6: Web UI (Dashboard)

**Status:** 0%

| Task | Complexity | Status |
|------|------------|--------|
| Frontend stack setup (Next.js) | 2 | ✗ |
| Auth integration (Clerk SDK) | 2 | ✗ |
| Dashboard home (usage summary) | 3 | ✗ |
| Party list + CRUD | 3 | ✗ |
| Account type configuration | 5 | ✗ |
| Position/measurement views | 3 | ✗ |
| Billing & invoice view | 3 | ✗ |
| Settings (team, API keys) | 3 | ✗ |
| CEL editor with syntax highlighting | 5 | ✗ |

**Total Complexity:** 21

**Deliverable:** Customer can log in, see their data, configure accounts, view billing

**Blocked by:** WS-3, WS-5

**Blocks:** WS-8

---

### WS-7: Marketing Site & Signup

**Status:** 0%

| Task | Complexity | Status |
|------|------------|--------|
| Landing page design | 2 | ✗ |
| Pricing page (tiers + add-ons) | 2 | ✗ |
| Signup → Stripe → Provision flow | 3 | ✗ |
| Email verification + welcome | 2 | ✗ |

**Total Complexity:** 5

**Deliverable:** Visitor can see pricing, sign up, pay, land in dashboard

**Blocked by:** WS-4

**Blocks:** Nothing (enables revenue)

---

### WS-8: AI Assistance

**Status:** 0%

| Task | Complexity | Status |
|------|------------|--------|
| CEL generation prompt engineering | 5 | ✗ |
| Guardrail validation | 3 | ✗ |
| Executable example runner | 5 | ✗ |
| Chat interface in UI | 3 | ✗ |
| Template library | 2 | ✗ |

**Total Complexity:** 13

**Deliverable:** User describes pricing in English, AI generates CEL, examples validate it

**Blocked by:** WS-6

**Blocks:** Nothing (growth feature)

---

### WS-9: Valuation Engine

**Status:** 0%

| Task | Complexity | Status |
|------|------------|--------|
| Market data adapter interface | 3 | ✗ |
| FX rate provider | 2 | ✗ |
| Energy spot price provider | 3 | ✗ |
| Valuation CEL context | 3 | ✗ |
| Scheduled revaluation jobs | 3 | ✗ |
| Position → Financial Accounting push | 5 | ✗ |

**Total Complexity:** 13

**Deliverable:** Positions valued against market data, pushed to ledger

**Blocked by:** WS-1

**Blocks:** Nothing (can be parallel track)

---

## Phased Milestones

### Phase 1: "Deployable" (Complexity: 13)

**Goal:** Meridian runs in the cloud, not on laptop

- [ ] WS-2: Helm charts + cluster + managed DB
- [ ] Basic CI/CD (push → deploy)

**Exit Criteria:** `helm install meridian` works, services healthy

---

### Phase 2: "Authenticated" (Complexity: 13)

**Goal:** Users can sign up and own a tenant

- [ ] WS-3: Auth provider integrated
- [ ] Signup creates user + org + schema
- [ ] Basic RBAC (owner, member)

**Exit Criteria:** New signup gets isolated tenant schema

---

### Phase 3: "Billable" (Complexity: 13)

**Goal:** Meridian can charge customers

- [ ] WS-4: Self-billing tenant live
- [ ] Stripe checkout integrated
- [ ] Usage metering active
- [ ] Monthly billing run

**Exit Criteria:** Signup → Stripe payment → usage tracked → invoice generated

---

### Phase 4: "Usable" (Complexity: 26)

**Goal:** Customers can operate without API-only

- [ ] WS-6: Dashboard MVP (parties, accounts, positions)
- [ ] WS-7: Marketing site with pricing
- [ ] WS-5: API docs + keys

**Exit Criteria:** Customer can self-serve from signup to daily operations

---

### Phase 5: "Differentiated" (Complexity: 26)

**Goal:** Features that justify premium pricing

- [ ] WS-8: AI-assisted CEL generation
- [ ] WS-9: Valuation with market data

**Exit Criteria:** "Describe your pricing" → working CEL with examples

---

## Complexity Summary

| Phase | Work Streams | Complexity | Cumulative |
|-------|--------------|------------|------------|
| 1: Deployable | WS-2 | 13 | 13 |
| 2: Authenticated | WS-3 | 13 | 26 |
| 3: Billable | WS-4 | 13 | 39 |
| 4: Usable | WS-5, WS-6, WS-7 | 26 | 65 |
| 5: Differentiated | WS-8, WS-9 | 26 | 91 |

**Total to MVP (Phase 4):** 65 complexity points

**Total to Full Vision (Phase 5):** 91 complexity points

---

## Where To Focus Now

**Critical Path Priority:**

1. **WS-2: Deployment** - Unblocks everything else
2. **WS-3: Identity** - Required for multi-tenant operation
3. **WS-4: Self-Billing** - Proves the product on itself

**Parallel Opportunity:**

- **WS-9: Valuation** can start now (only depends on WS-1)
- **WS-7: Marketing site** design can happen while WS-4 is built

---

## PRD Status

| PRD | Work Streams | Status |
|-----|--------------|--------|
| [Universal Asset System](universal-asset-system.md) | WS-1 | Draft |
| [Temporal Model Alignment](temporal-model-alignment-q1-2026.md) | WS-1 | Draft |
| [Platform Foundation](platform-foundation-q1-2026.md) | WS-2, WS-3, WS-4, WS-6, WS-7 | Draft |
| API & Developer Experience | WS-5 | TBD |
| AI Assistance | WS-8 | TBD |
| Valuation Engine | WS-9 | TBD |

---

## Notes

- Complexity is Fibonacci (1, 2, 3, 5, 8, 13, 21)
- No time estimates - complexity only
- This document is the index; each WS should have its own detailed PRD
- Taskmaster consumes PRDs for dependency and priority management
