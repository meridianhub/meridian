---
name: prd-meridian-zero
description: Stripe Atlas for financial operations - describe your business, get banking-grade infrastructure
triggers:
  - Planning the Meridian product vision
  - Working on conversational configuration
  - Implementing tenant provisioning from config
  - Building the landing page or chat interface
instructions: |
  Meridian Zero is the product vision: a conversational interface where users describe
  their business and receive a working ledger with payment integration. The engine is
  built; this PRD covers the product wrapper that makes it accessible.
---

# Meridian Zero: The Economy Compiler

Date: 2026-02-07

## Status

Draft - Master Roadmap

---

## The Vision

```
┌─────────────────────────────────────────────────────────────────┐
│                       meridianzero.com                          │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│     ▌                                                           │
│     Tell me about your business...                              │
│                                                                 │
│     "I run a solar installation company. We buy panels          │
│      wholesale, install them, and take 20% of energy            │
│      savings for 5 years."                                      │
│                                                                 │
│     ─────────────────────────────────────────────────────────   │
│                                                                 │
│     Opus: I'll set up:                                          │
│       • Supplier liability account (for panel purchases)        │
│       • Customer receivable per installation                    │
│       • Energy position account per site (kWh tracking)         │
│       • Revenue split: 20% of savings → your account            │
│                                                                 │
│     Connect Stripe to collect payments.                         │
│     Here's your API endpoint for meter readings.                │
│                                                                 │
│     [Connect Stripe]  [Get API Keys]  [View Dashboard]          │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

**Tagline:** "Describe your business model → we'll give you banking-grade infrastructure."

---

## What We're Building

| Layer | What It Does | Status |
|-------|--------------|--------|
| **The Engine** | Processes transactions with banking rigour | ✓ Done |
| **Conversation Layer** | Opus extracts business model from natural language | Not Started |
| **Business Model → Config** | Translates conversation into `meridian_manifest.json` | Not Started |
| **Config → Tenant** | Provisions schema, assets, accounts, policies from config | Partial |
| **Payment Rail** | Stripe Connect for customer billing | Not Started |
| **Landing Page** | The flashing cursor experience | Not Started |

---

## The Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        FRONTEND                                 │
│                     (Next.js + Clerk)                           │
├─────────────────────────────────────────────────────────────────┤
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────────┐ │
│  │    Chat     │  │  Blueprint  │  │       Dashboard         │ │
│  │  Interface  │  │   Preview   │  │  (post-provisioning)    │ │
│  └──────┬──────┘  └──────┬──────┘  └───────────┬─────────────┘ │
└─────────┼────────────────┼─────────────────────┼───────────────┘
          │                │                     │
          ▼                ▼                     ▼
┌─────────────────────────────────────────────────────────────────┐
│                      PLATFORM LAYER                             │
├─────────────────────────────────────────────────────────────────┤
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────────┐ │
│  │   Opus      │  │ Provisioning│  │     API Gateway         │ │
│  │  Compiler   │  │ Orchestrator│  │   (tenant routing)      │ │
│  │             │  │             │  │                         │ │
│  │ NL → JSON   │  │ JSON → gRPC │  │  API Key validation     │ │
│  └──────┬──────┘  └──────┬──────┘  └───────────┬─────────────┘ │
└─────────┼────────────────┼─────────────────────┼───────────────┘
          │                │                     │
          ▼                ▼                     ▼
┌─────────────────────────────────────────────────────────────────┐
│                       ENGINE LAYER                              │
│                      (✓ Already Built)                          │
├─────────────────────────────────────────────────────────────────┤
│  Reference    Current      Position    Financial    Payment    │
│   Data       Account       Keeping     Accounting    Order     │
│                                                                 │
│  Instruments  Accounts     Positions    Ledger      Payments   │
│  Policies     Liens        Balances     Postings    Sagas      │
└─────────────────────────────────────────────────────────────────┘
```

---

## Phase 0: Infrastructure (Unblocks Everything)

**Goal:** Meridian runs in the cloud, not on a laptop

| Task | Description | Complexity |
|------|-------------|------------|
| 0.1 | Helm charts for all services | 5 |
| 0.2 | Kubernetes cluster (DO/GKE) | 3 |
| 0.3 | Managed Postgres (Neon) | 2 |
| 0.4 | CI/CD pipeline (GitHub Actions → deploy) | 3 |
| 0.5 | Domain + TLS (meridianzero.com) | 1 |

**Total Complexity:** 13

**Deliverable:** `helm install meridian` works, services healthy

---

## Phase 1: The Business Model Schema

**Goal:** Define what Opus extracts, create the contract between AI and provisioner

### 1.1 The Manifest Schema

```yaml
# meridian_manifest.json
{
  "version": "1.0",
  "business": {
    "name": "Solar Installation Co",
    "description": "Install solar panels, take 20% of savings"
  },

  "instruments": [
    {
      "code": "SOLAR_PANEL",
      "dimension": "Physical",
      "unit": "unit",
      "description": "Solar panel inventory"
    },
    {
      "code": "KWH",
      "dimension": "Energy",
      "unit": "kWh",
      "description": "Energy generated/consumed"
    },
    {
      "code": "GBP",
      "dimension": "Monetary",
      "unit": "GBP",
      "description": "British Pounds"
    }
  ],

  "party_roles": [
    { "code": "SUPPLIER", "description": "Panel suppliers" },
    { "code": "CUSTOMER", "description": "Installation sites" }
  ],

  "account_templates": [
    {
      "code": "SUPPLIER_PAYABLE",
      "type": "liability",
      "instrument": "GBP",
      "party_role": "SUPPLIER",
      "description": "What we owe suppliers"
    },
    {
      "code": "CUSTOMER_RECEIVABLE",
      "type": "receivable",
      "instrument": "GBP",
      "party_role": "CUSTOMER",
      "description": "What customers owe us"
    },
    {
      "code": "ENERGY_POSITION",
      "type": "position",
      "instrument": "KWH",
      "party_role": "CUSTOMER",
      "description": "Energy tracked per site"
    }
  ],

  "valuation_policies": [
    {
      "name": "energy_savings_split",
      "input_instrument": "KWH",
      "output_instrument": "GBP",
      "cel_expression": "quantity * grid_rate * 0.20",
      "description": "20% of energy savings at grid rate"
    }
  ],

  "billing": {
    "frequency": "monthly",
    "trigger": "meter_reading",
    "payment_method": "stripe"
  }
}
```

### 1.2 Tasks

| Task | Description | Complexity |
|------|-------------|------------|
| 1.1 | Define `meridian_manifest.json` JSON Schema | 3 |
| 1.2 | Opus system prompt for business model extraction | 5 |
| 1.3 | Prompt chain: questions → structured output | 5 |
| 1.4 | Validation: manifest → dry-run check | 3 |

**Total Complexity:** 13

**Deliverable:** Describe business → get valid JSON manifest

---

## Phase 2: Provisioning Orchestrator

**Goal:** JSON config in, live tenant out

### 2.1 The Orchestration Flow

```
meridian_manifest.json
         │
         ▼
┌─────────────────────────────────────────┐
│     ProvisioningOrchestrator            │
├─────────────────────────────────────────┤
│ 1. Create Organization (platform)       │
│ 2. Create Tenant Schema                 │
│ 3. Register Instruments (Reference Data)│
│ 4. Register Valuation Policies          │
│ 5. Create Account Templates             │
│ 6. Generate API Keys                    │
│ 7. Store Stripe Connect placeholder     │
└─────────────────────────────────────────┘
         │
         ▼
   Tenant Ready
   + API Keys
   + Endpoints
```

### 2.2 Tasks

| Task | Description | Complexity |
|------|-------------|------------|
| 2.1 | Organization registry (user → org → tenant mapping) | 3 |
| 2.2 | `ApplyManifest` RPC in Tenant Service | 5 |
| 2.3 | Instrument registration from manifest | 2 |
| 2.4 | Valuation policy registration from manifest | 3 |
| 2.5 | Account template creation from manifest | 3 |
| 2.6 | API key generation + storage | 3 |
| 2.7 | Provisioning status webhook | 2 |

**Total Complexity:** 21

**Deliverable:** `POST /v1/provision` with manifest → working tenant

---

## Phase 3: Payment Rail (Stripe Connect)

**Goal:** Money flows through the ledger

### 3.1 The Flow

```
Customer pays via Stripe Checkout
              │
              ▼
      Stripe webhook received
              │
              ▼
      Match to Meridian account
              │
              ▼
      Trigger PaymentReceived saga
              │
              ▼
      Credit Customer account
      Debit Stripe Clearing account
              │
              ▼
      Position updated
```

### 3.2 Tasks

| Task | Description | Complexity |
|------|-------------|------------|
| 3.1 | Stripe Connect onboarding flow | 5 |
| 3.2 | Store Stripe account ID per tenant | 2 |
| 3.3 | Webhook handler for payment events | 3 |
| 3.4 | PaymentReceived saga definition | 3 |
| 3.5 | Stripe Clearing account (IBA) | 2 |
| 3.6 | Settlement posting to ledger | 3 |

**Total Complexity:** 13

**Deliverable:** Customer pays → ledger updates automatically

---

## Phase 4: The Landing Page

**Goal:** The flashing cursor experience

### 4.1 The UI

```
┌─────────────────────────────────────────────────────────────────┐
│  meridianzero.com                              [Login] [Signup] │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  ┌─────────────────────────┐  ┌───────────────────────────────┐│
│  │                         │  │                               ││
│  │    CHAT INTERFACE       │  │     BLUEPRINT PREVIEW         ││
│  │                         │  │                               ││
│  │  ▌ Tell me about        │  │  Instruments:                 ││
│  │    your business...     │  │  ├── GBP (Monetary)           ││
│  │                         │  │  ├── KWH (Energy)             ││
│  │                         │  │  └── PANEL (Physical)         ││
│  │                         │  │                               ││
│  │                         │  │  Accounts:                    ││
│  │                         │  │  ├── Supplier Payable         ││
│  │                         │  │  ├── Customer Receivable      ││
│  │                         │  │  └── Energy Position          ││
│  │                         │  │                               ││
│  │                         │  │  Policies:                    ││
│  │                         │  │  └── 20% savings split        ││
│  │                         │  │                               ││
│  └─────────────────────────┘  └───────────────────────────────┘│
│                                                                 │
│                    [Go Live] [Edit Config] [Export]             │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

### 4.2 Tasks

| Task | Description | Complexity |
|------|-------------|------------|
| 4.1 | Next.js project setup + Tailwind | 2 |
| 4.2 | Clerk auth integration | 2 |
| 4.3 | Chat component (Opus API) | 5 |
| 4.4 | Blueprint preview component | 3 |
| 4.5 | "Go Live" → provisioning trigger | 2 |
| 4.6 | Post-provisioning dashboard | 5 |
| 4.7 | API key display + docs link | 2 |

**Total Complexity:** 21

**Deliverable:** User goes from "tell me about your business" to "here's your API key" in one session

---

## Summary

| Phase | Goal | Complexity | Cumulative |
|-------|------|------------|------------|
| **0** | Infrastructure (deploy) | 13 | 13 |
| **1** | Business Model Schema + Opus | 13 | 26 |
| **2** | Provisioning Orchestrator | 21 | 47 |
| **3** | Stripe Connect | 13 | 60 |
| **4** | Landing Page | 21 | 81 |

**Total to Launch:** 81 complexity points

---

## Critical Path

```
Phase 0 (Deploy)
     │
     ├──► Phase 1 (Schema + Opus) ──► Phase 4 (UI)
     │                                    │
     └──► Phase 2 (Provisioner) ◄─────────┘
              │
              └──► Phase 3 (Stripe)
```

**Minimum Viable Product:** Phase 0 + 2 + 4 (without Opus, manual config upload)
**Full Product:** All phases

---

## Why This Wins

| Advantage | Explanation |
|-----------|-------------|
| **Speed** | 5-minute chat → working treasury (vs 3-month integration) |
| **Integrity** | Generated config is dry-run validated before provisioning |
| **Flexibility** | Same engine handles solar, compute, carbon, anything |
| **Scalability** | Meridian Edge means cloud → IoT migration later |

---

## The Competitive Position

> **Stripe Atlas:** "Describe your company → we'll incorporate it."
>
> **Meridian Zero:** "Describe your business model → we'll give you banking-grade infrastructure."

---

## Related Documents

- [Durable Execution Engine](durable-execution-engine.md) - Saga runtime
- [Universal Asset System](universal-asset-system.md) - Multi-asset support
- [Valuation Service](valuation-service.md) - CEL + Starlark valuation
- [Starlark Saga Orchestration](starlark-saga-orchestration-core.md) - Saga definitions
