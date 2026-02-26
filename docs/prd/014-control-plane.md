---
name: prd-control-plane
description: >-
  Control Plane PRD for operating Meridian as a commercial SaaS product.
  Defines the Manifest-centric model, Staff Identity Registry,
  Apply Manifest Orchestrator, CFO Glass Box UI, Stripe integration,
  and full SaaS work streams.
triggers:
  - Designing the SaaS operations layer
  - Working on tenant manifest management
  - Implementing staff identity or API key management
  - Building the admin console or onboarding flows
  - Integrating Stripe billing or payment rails
  - Planning the CFO Glass Box dashboard
instructions: |
  This PRD defines the Control Plane for operating Meridian commercially.
  Two execution paths: First Client (43 points) and Full SaaS (106 points).
  Core insight: the Manifest is the product. AI is one way to generate it.
  The ApplyManifest orchestrator is itself a Starlark Saga (recursive elegance).
  Staff identity is separate from Party (customers with ledger positions).
  See ADR-0016 for tenant ID strategy, ADR-0028 for Starlark/CEL architecture.
---

# Meridian Control Plane PRD

> **Status**: Not Started
> **Task Master Tag**: `control-plane`
> **Complexity**: 43 (First Client) / 106 (Full SaaS)
> **Last Updated**: 2026-02-08

---

## Executive Summary

The Meridian Control Plane is the **"Economy Compiler"** - the management
layer that transforms declarative business model definitions into a running
financial operations platform.

This PRD defines two paths:

1. **First Client (43 points)** - Minimum viable path to demo
   a paying client
2. **Full SaaS Build (106 points)** - Complete self-service platform

The key insight: **the Manifest is the product, not a feature**. The JSON
schema that defines a business model is the core primitive. AI is just one
way to generate it.

---

## What Already Exists

| Component | Location | Status |
|-----------|----------|--------|
| **Tenant Service** | `services/tenant/` | Full CRUD, async provisioning, status tracking |
| **Usage Metering** | `services/utilization-metering-consumer/` | Transforms audit events into measurements |
| **RBAC** | `shared/platform/auth/rbac.go` | Roles (admin, operator, auditor, service), permissions |
| **API Gateway** | `services/gateway/` | Subdomain routing, JWT auth, rate limiting |
| **Party Service** | `services/party/` | Organisation/party registration (customers) |
| **Causation Tree** | `api/proto/meridian/saga/v1/saga_admin.proto` | GetCausationTree RPC for audit trails |
| **Dry-Run Validation** | Reference Data, Position Keeping | Validate before commit |
| **tenantctl CLI** | `cmd/tenantctl/` | Register, list, get, deprovision tenants |

**Critical Distinction**:

- `Party` = **Customers** with ledger positions (kWh balances, GBP holdings)
- `Staff` = **Employees** with Admin Console access (push Manifests,
  own API keys) -- **MISSING**

**Key Insight**: The core multi-tenant infrastructure is production-ready.
The Control Plane extends it for commercial operation, not replaces it.

---

## Service Architecture: New `services/control-plane/`

The Control Plane is a **new service**, not an extension of the
existing tenant service. The boundary is Infrastructure vs Product:

| Concern | `services/tenant/` | `services/control-plane/` |
|---------|-------------------|--------------------------|
| **Role** | Infrastructure provisioning | Business operation |
| **Analogy** | Terraform (create infrastructure) | Application (use infrastructure) |
| **Operations** | `CREATE SCHEMA`, seed migrations, deprovision | Manifest compile, staff CRUD, billing |
| **Privilege** | High (DDL, schema creation) | Normal (DML, gRPC calls) |
| **Triggered by** | Platform operator / provisioning pipeline | Tenant staff / API keys / webhooks |

### Control Plane Responsibilities

```mermaid
graph TD
    subgraph "services/control-plane/"
        CP1["ValidateAPIKey RPC\n(called by Gateway)"]
        CP2["ApplyManifest Compiler\n(Manifest → gRPC calls)"]
        CP3["Stripe Webhook Handler\n(payment → saga trigger)"]
        CP4["Admin API\n(CFO Glass Box backend)"]
        CP5["Staff CRUD\n(invite, activate, deactivate)"]
    end

    GW[Gateway] -->|key validation| CP1
    CLI["meridian-cli"] -->|apply manifest| CP2
    Stripe[Stripe] -->|webhooks| CP3
    UI["Admin Console"] -->|dashboard queries| CP4
    UI -->|user management| CP5

    CP2 -->|gRPC| RD[reference-data]
    CP2 -->|gRPC| IBA[internal-account]
    CP2 -->|gRPC| PK[position-keeping]
    CP3 -->|Kafka| SAGA[Saga Engine]
```

### What Stays in Existing Services

- **`services/gateway/`**: Continues to handle routing, JWT auth.
  Calls control-plane's `ValidateAPIKey` RPC for database-backed
  key validation (replacing env-var keys).
- **`services/tenant/`**: Continues to own schema provisioning
  and lifecycle. Control-plane calls tenant service to resolve
  slugs, not the other way around.
- **`services/payment-order/`**: Existing webhook handler remains
  for tenant-facing payment flows. Control-plane handles
  Meridian's own Stripe integration (billing, cash-in rail).
- **`services/reference-data/`**: Continues to own saga definitions,
  instruments, handler schemas. Control-plane reads these via gRPC
  during manifest compilation.

---

## First Client (43 Points)

This is the "behind-the-curtain" sequence to demo a paying client.

```mermaid
graph TD
    subgraph P1["Phase 1: Foundation"]
        A1[Manifest Schema]
        A2[Staff Registry]
        A3[API Key Persistence]
    end

    subgraph P2["Phase 2: Compiler"]
        B1[ApplyManifest Orchestrator]
        B2[Idempotent Execution]
    end

    subgraph P3["Phase 3: Glass Box"]
        C1[Causation Visualizer]
        C2[Multi-Asset Balance Sheet]
        C3[CFO View]
    end

    subgraph P4["Phase 4: Cash Rail"]
        D1[Stripe Webhooks]
        D2["Payment -> Position Saga"]
    end

    P1 --> P2
    P2 --> P3
    P2 --> P4
```

---

## Work Streams (Resequenced)

### WS-1: Meridian Manifest Schema (Complexity: 9) P0

**Service**: `services/control-plane/`

**Objective**: Define the declarative business model specification -
the **"Administrative Control Record"**.

The Manifest is the single source of truth that prevents configuration
drift. It defines the complete economy: assets, accounts, policies,
and workflows.

#### What Needs Building

| Task | ID | Description | Complexity |
|------|-----|-------------|------------|
| **1.1** | `cp.manifest.proto` | Define Manifest as Protobuf (`api/proto/control_plane/v1/manifest.proto`); auto-generate JSON Schema via `protoc-gen-jsonschema` | 3 |
| **1.2** | `cp.manifest.validator` | Validate manifest against generated schema (structure check) | 2 |
| **1.3** | `cp.manifest.dryrun` | Dry-run validation using existing service mocks | 2 |
| **1.4** | `cp.manifest.examples` | Reference manifests for common industries | 1 |
| **1.5** | `cp.manifest.ci-schema` | CI step: regenerate JSON Schema from proto, fail if output differs (guarantees no manual drift) | 1 |

#### Meridian Manifest Schema v1

```json
{
  "$schema": "https://meridian.dev/manifest/v1",
  "version": "1.0.0",
  "metadata": {
    "name": "Acme Energy Co",
    "industry": "energy",
    "description": "Prepaid energy metering for residential customers"
  },

  "instruments": [
    {
      "code": "KWH",
      "name": "Kilowatt Hours",
      "type": "COMMODITY",
      "dimensions": { "unit": "energy", "precision": 3 }
    },
    {
      "code": "GBP",
      "name": "British Pounds",
      "type": "FIAT",
      "dimensions": { "unit": "currency", "precision": 2 }
    }
  ],

  "account_types": [
    {
      "code": "CUSTOMER_PREPAID",
      "name": "Customer Prepaid Balance",
      "normal_balance": "CREDIT",
      "instruments": ["KWH", "GBP"],
      "policies": {
        "validation": "balance.quantity >= 0",
        "bucketing": "tariff_code + '_' + period_month"
      }
    },
    {
      "code": "REVENUE",
      "name": "Revenue Recognition",
      "normal_balance": "CREDIT",
      "instruments": ["GBP"]
    }
  ],

  "valuation_rules": [
    {
      "from": "KWH",
      "to": "GBP",
      "method": "SPOT_RATE",
      "source": "tariff_schedule"
    }
  ],

  "sagas": [
    {
      "name": "record_meter_reading",
      "trigger": "api:POST:/meters/{meter_id}/readings",
      "script": "def execute(ctx, event):\n    ..."
    },
    {
      "name": "process_topup",
      "trigger": "webhook:stripe:payment_intent.succeeded",
      "script": "def execute(ctx, event):\n    ..."
    }
  ],

  "payment_rails": [],

  "seed_data": {
    "tariffs": [
      { "code": "STANDARD", "rate_per_kwh": "0.28" },
      { "code": "ECONOMY7_DAY", "rate_per_kwh": "0.32" },
      { "code": "ECONOMY7_NIGHT", "rate_per_kwh": "0.12" }
    ]
  }
}
```

#### Design Decision: Inline Scripts (Wire Format)

The Manifest wire format requires `script` and `validation_expression`
as **inline strings**, not file references.

**Rationale**:

1. **Atomicity**: The Manifest is a complete snapshot of the economy.
   External file references break the guarantee that "this JSON is the
   complete truth."
2. **AI Compatibility**: LLMs generate a single cohesive JSON object
   more reliably than coordinating multi-file output.

**Developer Workflow**: The `meridian-cli apply` command includes a
**hydrator** that reads `script_ref: "file.star"` from local
developer-friendly YAML/JSON and injects file contents as inline
`script` strings before sending to the API. Developers get file-based
IDE support; the system gets atomic wire format.

#### AI-Native Validation Feedback (Already Exists)

The Starlark and CEL runtimes already produce **structured, actionable
error messages** at compilation time. This is the key to the AI-native
feedback loop:

```mermaid
flowchart TD
    A["User/AI generates Manifest"] --> B["ValidateManifest()"]
    B --> C{"Starlark Compiler\n- Syntax errors\n- Undefined symbols\n- Import failures"}
    B --> D{"CEL Type Checker\n- Type mismatches\n- Missing fields\n- Invalid operators"}
    C --> E["Structured Error Response\n(machine-readable)"]
    D --> E
    E --> F["Feed back to AI /\nDisplay to user"]
    F --> G{Valid?}
    G -- No --> A
    G -- Yes --> H["Manifest Ready"]
```

**Example Validation Response** (what we already get):

```json
{
  "valid": false,
  "errors": [
    {
      "location": "policies.validation.customer_account_balance",
      "expression": "balance.quanity >= 0",
      "error_type": "CEL_UNDEFINED_FIELD",
      "message": "undefined field 'quanity' on type 'Balance'",
      "suggestion": "Did you mean 'quantity'?",
      "available_fields": [
        "quantity", "instrument", "bucket_id", "as_of"
      ]
    },
    {
      "location": "sagas[0].script",
      "expression": "def execute(ctx, event): ...",
      "error_type": "STARLARK_COMPILE_ERROR",
      "message": "undefined: ctx.position_keepng",
      "line": 12,
      "column": 5,
      "suggestion": "Did you mean 'ctx.position_keeping'?"
    }
  ],
  "warnings": [
    {
      "location": "instruments[0]",
      "message": "Instrument 'KWH' has no valuation rule to base currency",
      "severity": "WARN"
    }
  ]
}
```

**Why This Matters**: The validation layer speaks the same language as
the AI. When Opus generates a Manifest with a typo, the compiler tells
it exactly what's wrong and how to fix it. No human in the loop
required for iteration.

> **Implementation Note**: The validator MUST return `available_fields`
> on every undefined field error. This "Reflection API" is what allows
> an LLM to self-correct without needing a huge context window or
> multiple documentation lookups.

#### Design Decision: Code is Immutable Primary Key

Resource `code` fields (e.g., `"CUSTOMER_PREPAID"`, `"KWH"`) are
**immutable identifiers** used as primary keys by downstream services.
The Differ interprets a code change as DELETE old + CREATE new, which
the live-balance safety check would reject.

**Rename strategy**: Codes cannot be renamed once created. Only
display `name` fields can change. This is deliberate — account codes
appear in audit trails, position entries, and external integrations.
Renaming them would break referential integrity across services.

If a tenant genuinely needs a new code, the migration path is:

1. Create new resource with new code
2. Migrate positions/balances via a dedicated saga
3. Deprecate (not delete) the old resource

#### Acceptance Criteria

- [ ] JSON Schema published with full documentation
- [ ] Schema validation catches structural errors before API calls
- [ ] Dry-run validates CEL syntax and Starlark compilation
- [ ] Validation errors include `suggestion` field for AI feedback loop
- [ ] Error responses are JSON-structured, not just strings
- [ ] Example manifests for: energy, carbon credits, SaaS billing,
  loyalty points
- [ ] Validator rejects code changes on existing resources (codes are
  immutable, only display names are mutable)
- [ ] Manifest payload size validated against gRPC message limits
  (default 4MB). CLI warns on large manifests

---

### WS-2: Staff Identity Registry (Complexity: 8) P0

**Service**: `services/control-plane/` + `services/gateway/`
(key validation RPC)

**Objective**: Separate identity layer for tenant employees who manage
the system.

This is distinct from `Party` (customers with ledger positions). Staff
members:

- Log into the Admin Console
- Own and manage API keys
- Push Manifest updates
- View audit trails

#### What Exists

- RBAC with roles/permissions in `shared/platform/auth/rbac.go`
- JWT claims with `tenant_id`, `roles`, `scopes`
- API key middleware (environment variable based)

#### What Needs Building

| Task | ID | Description | Complexity |
|------|-----|-------------|------------|
| **2.1** | `cp.auth.staff-table` | Create `staff_users` table in tenant schema (`org_{id}`) | 2 |
| **2.2** | `cp.auth.staff-service` | Staff CRUD: invite, activate, deactivate, list | 2 |
| **2.3** | `cp.auth.apikey-table` | Create `api_keys` in tenant schema with prefixed keys and hashed storage | 2 |
| **2.4** | `cp.auth.apikey-gateway` | Update Gateway to route API keys by prefix to correct tenant schema | 2 |

#### Design Decision: Tenant-Scoped Staff (ADR-0016 Alignment)

Staff and API key data live in tenant schemas (`org_{id}`), not a
shared platform schema. This maintains strict data isolation consistent
with the schema-per-tenant architecture (ADR-0016).

**Gateway Routing via Prefixed Keys**: API keys embed a routable
tenant slug: `pk_{tenant_slug}_{entropy}` (e.g., `pk_motive_a8b9c...`).

```mermaid
sequenceDiagram
    participant Client
    participant Gateway
    participant Cache
    participant TenantDB as org_{id}.api_keys

    Client->>Gateway: Authorisation: pk_motive_a8b9c...
    Gateway->>Gateway: Parse prefix → slug "motive"
    Gateway->>Cache: Resolve slug → tenant_id
    Cache-->>Gateway: tenant_id = "motive"
    Gateway->>TenantDB: SELECT key_hash FROM org_motive.api_keys<br/>WHERE key_prefix = 'pk_motive_a8b9'
    TenantDB-->>Gateway: key_hash (SHA-256)
    Gateway->>Gateway: Verify SHA-256(full_key) = key_hash
    Gateway-->>Client: 200 OK (tenant context set)
```

This achieves O(1) routing without a centralised secrets table.

**Human Login Routing**: API keys self-route via prefix, but human
login via Auth0/Keycloak only provides an email — no tenant context.
The Admin Console URL must include the tenant slug to force routing
context before authentication begins:
`console.meridian.com/{slug}/login`. The Control Plane resolves the
slug to a tenant ID, then queries `org_{id}.staff_users` to verify
the user's role and status. This avoids scanning all tenant schemas
and avoids a global staff index.

#### Data Model

```sql
-- Lives in tenant schema (org_{id}), not shared platform schema.
-- Maintains strict data isolation per ADR-0016.
-- Gateway routes to correct schema via prefixed API keys.
CREATE TABLE staff_users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email VARCHAR(255) NOT NULL,
    name VARCHAR(255),
    role VARCHAR(50) NOT NULL DEFAULT 'operator',
    status VARCHAR(20) NOT NULL DEFAULT 'invited',
    auth_provider_id VARCHAR(255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(email)
);

CREATE TABLE api_keys (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    staff_user_id UUID REFERENCES staff_users(id) ON DELETE CASCADE,
    key_prefix VARCHAR(100) NOT NULL, -- pk_{slug}_{8} → up to ~74 chars
    key_hash BYTEA NOT NULL, -- SHA-256 (fast; keys are high-entropy)
    name VARCHAR(255),
    scopes TEXT[] NOT NULL DEFAULT '{}',
    rate_limit_rps INTEGER DEFAULT 100,
    last_used_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at TIMESTAMPTZ,
    UNIQUE(key_prefix)
);

CREATE INDEX idx_api_keys_prefix
    ON api_keys(key_prefix)
    WHERE revoked_at IS NULL;
```

**Column notes**:

- `role`: admin, operator, auditor
- `status`: invited, active, suspended
- `auth_provider_id`: Auth0/Clerk user ID
- `staff_user_id`: nullable for service keys
- `key_prefix`: structured as `pk_{tenant_slug}_{first8}` for
  Gateway routing (e.g., `pk_motive_a8b9c123`)
- `key_hash`: SHA-256 hash (not argon2id -- API keys are
  high-entropy machine-generated strings validated on every request;
  SHA-256 keeps the Gateway hot path fast)
- `scopes`: e.g., `["read:positions", "write:transactions"]`

#### Acceptance Criteria

- [ ] Staff users can be invited to a tenant
- [ ] API keys are hashed (never stored plaintext)
- [ ] API key format enforces routable prefix: `pk_{slug}_{entropy}`
- [ ] Gateway parses key prefix, resolves tenant, queries tenant schema
- [ ] Gateway validates keys from database with caching
- [ ] Keys can be scoped to specific permissions
- [ ] Key usage is tracked (last_used_at)
- [ ] No staff or key data in shared/platform schema

---

### WS-3: Apply Manifest Orchestrator (Complexity: 13) P0

**Service**: `services/control-plane/`

**Objective**: The engine that turns JSON into gRPC calls.
**Must be idempotent.**

This is the core "compiler" that reads a Manifest and orchestrates
calls to existing services:

- `ReferenceData.RegisterInstrument`
- `ReferenceData.RegisterAccountType`
- `CurrentAccount.InitiateCurrentAccount`
- etc.

#### Critical Requirement: ApplyManifest IS a Durable Saga

> **Risk**: Since Meridian is a distributed system, `ApplyManifest`
> could fail halfway (e.g., Reference Data succeeds, but Current
> Account times out).
>
> **Solution**: The ApplyManifest orchestrator **must be implemented
> as a Starlark Saga itself**. It uses the very engine it is
> configuring to ensure the configuration is atomic.

This is elegantly recursive: the system that runs sagas is configured
by a saga.

> **Bootstrap**: On startup, the Control Plane service upserts the
> `ApplyManifest` script into `public.platform_saga_definition` in
> Reference Data. New tenants inherit it automatically via the
> platform default fallback mechanism (ADR-0028). This avoids coupling
> the Tenant Service (infrastructure) to Control Plane application
> logic — the Tenant Service never needs to know the script contents.
>
> **Cross-tenant execution**: ApplyManifest resolves the saga
> definition from the platform default (not the target tenant's local
> definitions) but executes all handler calls within the target
> tenant's data scope (`SET LOCAL search_path TO org_{id}, public`).
> This means a freshly-provisioned tenant with zero local saga
> definitions can still have its manifest applied.
>
> **Required test**: "Apply manifest to a tenant that has 0 local
> saga definitions" — validates the platform default fallback path
> end-to-end.

```python
# sagas/apply_manifest.star
def execute(ctx, manifest):
    """Apply a Manifest atomically using durable execution."""

    # Phase 1: Instruments (no dependencies)
    for instrument in manifest.instruments:
        ctx.reference_data.register_instrument(
            code = instrument.code,
            name = instrument.name,
            instrument_type = instrument.type,
        )

    # Phase 2: Account Types (depend on instruments)
    for account_type in manifest.account_types:
        ctx.reference_data.register_account_type(
            code = account_type.code,
            normal_balance = account_type.normal_balance,
            allowed_instruments = account_type.instruments,
        )

    # Phase 3: Valuation Rules
    for rule in manifest.valuation_rules:
        ctx.reference_data.register_valuation_rule(
            from_instrument = rule["from"],
            to_instrument = rule["to"],
            method = rule["method"],
        )

    # Phase 4: Saga Definitions
    for saga in manifest.sagas:
        ctx.saga_registry.register_saga(
            name = saga.name,
            trigger = saga.trigger,
            script = saga.script,
        )

    return {"status": "applied", "version": manifest.version}
```

**Why This Matters**:

- If the saga fails at Phase 2, Phase 1 changes are already committed
- On retry, the differ sees Phase 1 resources exist and skips them
- Idempotency is achieved through the saga's durable execution model

#### What Needs Building

| Task | ID | Description | Complexity |
|------|-----|-------------|------------|
| **3.1** | `cp.engine.differ` | Compute diff between current state and desired Manifest; reject deletions of resources with live balances | 3 |
| **3.2** | `cp.engine.planner` | Generate ordered list of gRPC calls from diff | 3 |
| **3.3** | `cp.engine.executor` | Execute plan with rollback on failure | 3 |
| **3.4** | `cp.engine.status` | Track apply status: pending, applying, applied, failed | 2 |
| **3.5** | `cp.engine.history` | Store Manifest versions with who/when/diff | 2 |

#### API Design

```protobuf
service EconomyEngineService {
  // Validate manifest without applying
  rpc ValidateManifest(ValidateManifestRequest)
      returns (ValidationResult);

  // Compute what would change
  rpc PlanManifest(PlanManifestRequest)
      returns (ManifestPlan);

  // Apply manifest (idempotent).
  // For manifests exceeding gRPC size limits, the CLI uploads to
  // blob storage (S3/MinIO) and passes a manifest_ref URI instead
  // of the full JSON body.
  rpc ApplyManifest(ApplyManifestRequest)
      returns (ApplyManifestResponse);

  // Get current applied manifest
  rpc GetCurrentManifest(GetCurrentManifestRequest)
      returns (Manifest);

  // List manifest history
  rpc ListManifestVersions(ListManifestVersionsRequest)
      returns (ManifestVersionList);
}

message ManifestPlan {
  repeated PlannedAction actions = 1;
  bool has_breaking_changes = 2;
  string summary = 3;
}

message PlannedAction {
  string resource_type = 1; // "instrument", "account_type", "saga"
  string resource_id = 2;
  ActionType action = 3;    // CREATE, UPDATE, DELETE, NO_CHANGE
  string description = 4;
}
```

#### Idempotency Contract

```text
Apply(Manifest_v1) -> State_A
Apply(Manifest_v1) -> State_A  (no-op, same result)
Apply(Manifest_v2) -> State_B  (diff applied)
Apply(Manifest_v1) -> State_A  (rollback to v1)
```

#### Differ Strategy: Last-Applied Manifest

The Differ computes changes by comparing `Last Applied Manifest` vs
`New Manifest` (not `Current DB State` vs `New Manifest`). This
follows the Kubernetes `last-applied-configuration` pattern:

- The Control Plane stores each successfully applied manifest as a
  versioned snapshot (task 3.5 `cp.engine.history`)
- Diffing two JSON documents is deterministic and testable
- Diffing DB state is fragile (state drifts from manual changes,
  migrations, etc.)
- If DB state has drifted from the last-applied manifest, `PlanManifest`
  surfaces the drift as warnings before applying

#### Acceptance Criteria

- [ ] Applying the same manifest twice results in "No Changes"
- [ ] Plan shows exactly what will change before apply
- [ ] Failed applies leave system in consistent state
- [ ] Manifest history is auditable (who, when, diff)
- [ ] Breaking changes require explicit confirmation
- [ ] Differ rejects deletion of resources with live balances
  (e.g., removing an AccountType that holds positions)

---

### WS-4: CFO Glass Box UI (Complexity: 8)

**Service**: `services/control-plane/` (Admin API backend) +
frontend TBD

**Objective**: The "Horizon-Proof" screen - show the numbers, then
click to show the *why*.

This is the visualization layer that proves Meridian is trustworthy.
It uses existing infrastructure:

- `GetCausationTree` RPC (exists in saga admin)
- Position aggregation from Position Keeping

#### What Needs Building

| Task | ID | Description | Complexity |
|------|-----|-------------|------------|
| **4.1** | `ui.causation.visualizer` | Interactive tree view of saga causation chains | 3 |
| **4.2** | `ui.balance.multiasset` | Multi-asset balance sheet (all instruments) | 2 |
| **4.3** | `ui.balance.drill` | Click position -> see transactions -> see causation | 2 |
| **4.4** | `ui.export.csv` | Export any view to CSV for auditors | 1 |

#### Causation Tree Visualization

```mermaid
graph TD
    A["Stripe Webhook\npayment_intent.succeeded\n2026-02-08 14:23:01\nGBP 50.00"]
    B["Saga: process_topup\nsaga_exec_id: sge_abc123\n2026-02-08 14:23:02\nduration: 45ms"]
    C["Debit\nStripe Nostro\nGBP 50.00"]
    D["Credit\nCustomer Prepaid\nGBP 50.00"]

    A --> B
    B --> C
    B --> D
```

#### Multi-Asset Balance Sheet

> **Balance Sheet: Acme Energy** -- As of: 2026-02-08

| **ASSETS** | **GBP** | **KWH** |
|---|---:|---:|
| Stripe Nostro | 12,450.00 | - |
| Customer Receivables | 3,200.00 | - |
| Energy Inventory | - | 45,000 kWh |
| **Total Assets** | **15,650.00** | **45,000 kWh** |

| **LIABILITIES** | **GBP** | **KWH** |
|---|---:|---:|
| Customer Prepaid Balances | 8,900.00 | 12,500 kWh |
| Deferred Revenue | 2,100.00 | - |
| **Total Liabilities** | **11,000.00** | **12,500 kWh** |

| **EQUITY** | **GBP** | **KWH** |
|---|---:|---:|
| Retained Earnings | 4,650.00 | 32,500 kWh |

*Click any row to drill down to positions and transactions.*

#### Acceptance Criteria

- [ ] Any number can be clicked to show its source transactions
- [ ] Causation tree shows complete audit trail
- [ ] Balance sheet supports multiple instruments
- [ ] CSV export for auditor compliance

---

### WS-5: Stripe Cash-In Rail (Complexity: 5)

**Service**: `services/control-plane/` (Meridian's own Stripe
integration, not tenant payment flows in `services/payment-order/`)

**Objective**: Prove the system touches "Real Money" with the
"Everything is a Position" invariant.

**Key Architectural Decision**: Billing records revenue as positions
FIRST, then settles to Stripe. This maintains ledger integrity.

```mermaid
flowchart LR
    A[Stripe Webhook] --> B[Revenue Position\nmeridian-ops tenant]
    B --> C[Stripe Settlement]
    B -. "Internal Ledger is\nSource of Truth" .-> B
```

#### What Needs Building

| Task | ID | Description | Complexity |
|------|-----|-------------|------------|
| **5.1** | `cp.stripe.webhook` | Webhook listener for payment events | 2 |
| **5.2** | `cp.stripe.saga` | Saga: payment_intent.succeeded -> credit ledger | 2 |
| **5.3** | `cp.stripe.reconcile` | Daily reconciliation report (Stripe vs Ledger) | 1 |

#### Webhook to Saga Flow

```go
// Webhook handler
func (h *StripeWebhookHandler) HandlePaymentIntentSucceeded(
    ctx context.Context, event stripe.Event,
) error {
    pi := event.Data.Object.(*stripe.PaymentIntent)

    // Trigger saga via Kafka.
    // StripeChargeID flows through as external_reference_id on position
    // entries for O(1) reconciliation. CausationID remains a UUIDv5
    // (deterministic from saga_instance_id + step_index) per existing
    // saga architecture.
    return h.publisher.Publish(ctx, &events.PaymentReceived{
        TenantID:        pi.Metadata["tenant_id"],
        PartyID:         pi.Metadata["party_id"],
        Amount:          pi.Amount,
        Currency:        pi.Currency,
        StripePaymentID: pi.ID,
        ExternalRef:     pi.LatestCharge.ID, // ch_12345
    })
}
```

```python
# sagas/stripe_payment_received.star
def execute(ctx, event):
    # Record revenue position (nostro account)
    nostro_result = ctx.position_keeping.record_transaction(
        account_id = "stripe_nostro",
        instrument = event.currency.upper(),
        quantity = event.amount / 100,
        direction = "DEBIT",
        external_reference_id = event.external_ref,  # ch_12345
    )

    # Credit customer's prepaid balance (primary posting)
    prepaid_result = ctx.position_keeping.record_transaction(
        account_id = event.party_id + "_prepaid",
        instrument = event.currency.upper(),
        quantity = event.amount / 100,
        direction = "CREDIT",
        external_reference_id = event.external_ref,  # ch_12345
    )

    return {
        "status": "completed",
        "posting_id": prepaid_result.posting_id,
        "position_log_id": prepaid_result.position_log_id,
        "booking_log_id": prepaid_result.booking_log_id,
    }
```

#### Preflight: meridian-ops Tenant

The Control Plane **must not** process Stripe webhooks or record
billing positions until the `meridian-ops` tenant exists with the
required internal accounts (`stripe_nostro`, `revenue`). On startup,
the Control Plane verifies this precondition and panics if unmet.

The seeding sequence:

1. Tenant Service provisions `meridian-ops` schema
2. Internal Account Service seeds nostro/revenue accounts
   (via existing post-provisioning hook)
3. Control Plane startup verifies accounts exist
4. Stripe webhook processing enabled

#### Acceptance Criteria

- [ ] Stripe webhooks are verified (signature check)
- [ ] Payment creates double-entry in ledger
- [ ] Control Plane startup panics if `meridian-ops` tenant or
  required internal accounts do not exist
- [ ] `external_reference_id` on position entries contains the Stripe
  Charge ID (e.g., `ch_12345`) for O(1) reconciliation joins.
  `causation_id` remains a UUIDv5 for internal saga causation trees
- [ ] Daily reconciliation catches discrepancies
- [ ] Webhook failures are retried with idempotency
- [ ] Stripe saga uses event ID as correlation ID; saga engine's
  deterministic idempotency keys (`saga_{id}_step_{idx}`) guarantee
  exactly-once execution per step on replay

#### Future: Tenant Payment Rails (Closed Loop)

WS-5 covers **Meridian's own** Stripe cash-in. The full end-to-end
vision is a closed payment loop for tenants:

```mermaid
graph LR
    A["Tenant's Customer"] -->|pays via Stripe Connect| B[Stripe]
    B -->|webhook| C["Tenant Ledger\n(position-keeping)"]
    C -->|settlement snapshots| D["Reconciliation Service"]
    D -->|variance detection| E["Settlement Finality"]
    E -->|adjustment sagas| C
```

This requires:

1. **Manifest declares payment rails**: `"payment_rails"` field
   (reserved, not yet implemented) specifying Stripe Connect mode
2. **Tenant Stripe Connect onboarding**: Store tenant's Connected
   Account ID during manifest apply or separate onboarding flow
3. **Party records include Stripe Customer IDs**: Party service
   extensible attributes already support this
4. **Reconciliation closes the loop**: `reconciliation-service`
   TM tag (55 points, 10 tasks) covers settlement snapshots,
   variance detection, dispute workflows, and settlement finality

> **Scope**: Tenant payment rails are a separate PRD (estimated
> 13+ points for Connect onboarding, customer payment methods,
> multi-party settlement). This PRD reserves the Manifest field
> and documents the integration points.

---

### WS-6: Billing Service (Complexity: 21) -- Full SaaS Only

**Objective**: Connect usage metering to Stripe for automated
subscription billing.

> **Note**: This is for billing **Meridian's customers**, not the
> tenant's customers. Uses a dedicated `meridian-ops` tenant as the
> billing ledger (dogfooding).

| Task | Description | Complexity |
|------|-------------|------------|
| **6.1** | Create `services/billing/` service structure | 3 |
| **6.2** | Define billing protobuf contracts | 2 |
| **6.3** | Implement Stripe Customer sync on tenant creation | 3 |
| **6.4** | Implement Stripe Subscription management | 3 |
| **6.5** | Implement usage reporting to Stripe | 3 |
| **6.6** | Implement Stripe webhook handler for Meridian billing | 3 |
| **6.7** | Add plan tier enforcement middleware | 2 |
| **6.8** | Add graceful degradation for expired subscriptions | 2 |

---

### WS-7: Admin Console (Complexity: 21) -- Full SaaS Only

**Objective**: Web UI for Meridian operators to manage tenants.

| Task | Description | Complexity |
|------|-------------|------------|
| **7.1** | Set up Next.js admin console project | 3 |
| **7.2** | Implement authentication flow (Auth0/Clerk) | 3 |
| **7.3** | Build tenant list and detail views | 5 |
| **7.4** | Build tenant creation wizard with Manifest editor | 5 |
| **7.5** | Build usage analytics dashboard | 3 |
| **7.6** | Add real-time provisioning updates | 2 |

---

### WS-8: Self-Service Onboarding (Complexity: 13) -- Full SaaS Only

**Objective**: Customers can sign up without operator intervention.

```mermaid
flowchart LR
    A[Email Signup] --> B[Verify Email]
    B --> C[Org Setup]
    C --> D[Plan Selection]
    D --> E[Payment Setup]
```

| Task | Description | Complexity |
|------|-------------|------------|
| **8.1** | Set up onboarding web app | 3 |
| **8.2** | Implement email verification | 2 |
| **8.3** | Build organisation setup step | 1 |
| **8.4** | Build plan selection with Stripe Checkout | 3 |
| **8.5** | Implement provisioning progress view | 2 |
| **8.6** | Add welcome email with getting started | 2 |

---

### WS-9: Declarative Economy Engine (Complexity: 8) -- Full SaaS Only

**Objective**: AI-assisted Manifest generation (Opus integration).

> **Renamed from "AI Configuration Assistant"** - The Manifest is the
> product; AI is one input method.

#### AI-Native Architecture (Leverage Existing Compiler)

The system is **AI-native by design** because the compiler feedback
loop is already structured for machine consumption:

```mermaid
sequenceDiagram
    actor User
    participant Opus
    participant Compiler as ValidateManifest()
    participant Engine as ApplyManifest()

    User->>Opus: "I run a prepaid energy company<br/>with day/night tariffs"
    Opus->>Compiler: Manifest v1 (KWH, GBP, balance >= 0)
    Compiler-->>Opus: Error: undefined field 'quanity'<br/>Suggestion: Did you mean 'quantity'?
    Opus->>Compiler: Manifest v2 (auto-corrected)
    Compiler-->>Opus: Valid
    Compiler-->>User: PlanManifest: 2 instruments, 3 accounts
    User->>Engine: "Looks good, apply it"
    Engine-->>User: Running tenant in < 5 minutes
```

**Key Insight**: We don't need to build "AI validation" - the compiler
IS the validator. Opus just needs to:

1. Generate JSON that conforms to the schema
2. Read structured errors from the compiler
3. Self-correct until valid

This is why Starlark and CEL were chosen: **they have excellent error
messages by design**.

| Task | Description | Complexity |
|------|-------------|------------|
| **9.1** | Create Opus system prompt with schema + error handling | 2 |
| **9.2** | Implement conversational UI with validate-on-change | 3 |
| **9.3** | Add "executable examples" corpus for few-shot learning | 2 |
| **9.4** | Implement export to YAML for GitOps workflows | 1 |

#### Acceptance Criteria

- [ ] Opus can self-correct from compiler errors without human help
- [ ] < 3 iterations from natural language to valid Manifest (p95)
- [ ] Generated CEL/Starlark passes type checking on first valid attempt
- [ ] User can export to YAML and apply via `git push`

---

## Dependencies and Sequencing

### First Client (43 Points)

```mermaid
graph LR
    WS1[WS-1: Manifest Schema] --> WS3[WS-3: Apply Orchestrator]
    WS2[WS-2: Staff Identity] --> WS3
    WS3 --> WS4[WS-4: CFO Glass Box]
    WS3 --> WS5[WS-5: Stripe Cash-In]
```

| Order | Task Master ID | Work Stream | Points |
|:------|:---------------|:------------|:-------|
| 1 | `cp.manifest.*` | WS-1: Manifest Schema | 9 |
| 2 | `cp.auth.*` | WS-2: Staff Identity | 8 |
| 3 | `cp.engine.*` | WS-3: Apply Orchestrator | 13 |
| 4 | `ui.causation.*`, `ui.balance.*` | WS-4: CFO Glass Box | 8 |
| 5 | `cp.stripe.*` | WS-5: Stripe Cash-In | 5 |
| | | **Total** | **43** |

> **Note:** Adjusted to 43 after detailed task breakdown (original
> estimate was 34). Includes proto-sync CI check (+1).

### Full SaaS Build (106 Points)

After First Client milestone:

- WS-6: Billing Service (21)
- WS-7: Admin Console (21)
- WS-8: Self-Service Onboarding (13)
- WS-9: Declarative Economy Engine (8)

---

## Implementation Phases

### First Client

| Phase | Focus | Deliverables |
|-------|-------|--------------|
| **Foundation** | WS-1, WS-2 | Manifest JSON Schema, staff/api_keys tables, Gateway API key validation |
| **Compiler** | WS-3 | ApplyManifest orchestrator, idempotent execution, manifest versioning |
| **Glass Box** | WS-4 | Causation tree visualizer, multi-asset balance sheet, drill-down UI |
| **Cash Rail** | WS-5 | Stripe webhooks, payment->ledger saga, reconciliation report |

**Demo Milestone**: Show client their business model as JSON, apply it,
show the CFO balance sheet, process a Stripe payment, click to show the
audit trail.

### Full SaaS

| Phase | Work Streams |
|-------|--------------|
| **Revenue Engine** | WS-6: Billing Service |
| **Operator Experience** | WS-7: Admin Console |
| **Customer Experience** | WS-8: Onboarding, WS-9: AI Engine |

---

## Success Metrics

### First Client

| Metric | Target |
|--------|--------|
| Manifest to Running Tenant | < 5 minutes |
| Apply idempotency | 100% (same manifest = no changes) |
| Causation tree depth | Visible to leaf transactions |
| Stripe to Ledger latency | < 1 second |

### Full SaaS

| Metric | Target |
|--------|--------|
| Time to first API call | < 5 minutes |
| Self-service signup rate | > 80% |
| MRR per tenant | > $100 |
| Churn rate | < 5%/month |

---

## Task Master Entry Format

Each task should be entered as:

```yaml
- id: cp.manifest.schema
  title: Define Meridian Manifest JSON Schema
  description: |
    Create the JSON Schema specification for the complete tenant
    business model configuration. Must cover:
    - Instruments (assets, currencies, commodities)
    - Account types with CEL policies
    - Valuation rules
    - Saga definitions with triggers
    - Seed data
  complexity: 3
  dependencies: []
  tags: [control-plane, p0, first-client]

- id: cp.auth.staff-registry
  title: Implement Staff Identity Registry
  description: |
    Create the admin/staff identity layer separate from Party.
    Staff users own API keys and access the Admin Console.
    Includes:
    - staff_users table (tenant schema, org_{id})
    - api_keys table with hashed keys and prefixed routing
    - Staff CRUD service
  complexity: 4
  dependencies: []
  tags: [control-plane, p0, first-client]

- id: cp.engine.apply-orchestrator
  title: Implement ApplyManifest Orchestrator
  description: |
    The core "compiler" that turns Manifest JSON into gRPC calls.
    Must be idempotent - applying same manifest twice = no changes.
    Includes differ, planner, executor, and status tracking.
  complexity: 13
  dependencies: [cp.manifest.schema, cp.auth.staff-registry]
  tags: [control-plane, p0, first-client]
```

---

## Related Documents

- [durable-execution-engine.md](005-durable-execution-engine.md) -
  Durable execution engine for saga orchestration
- [starlark-saga-orchestration-core.md](006-starlark-saga-orchestration-core.md) -
  Starlark saga orchestration core
- [starlark-service-bindings.md](008-starlark-service-bindings.md) -
  Service bindings for Starlark sagas
- ADR-0016: Tenant ID Naming Strategy
- ADR-0028: Starlark Saga and CEL Valuation
- ADR-0015: Standard Service Directory Structure
- ADR-0009: Application-Level Audit Logging

---

## Appendix: Architectural Decisions

### A. "Everything is a Position" Invariant

All financial state flows through the ledger, including Meridian's own
billing:

```mermaid
flowchart TD
    A[Usage Event] --> B[Utilization Metering]
    B --> C["Position (meridian-ops tenant)"]
    C --> D[Billing Service]
    D --> E[Stripe Invoice]
    E --> F[Payment Webhook]
    F --> G["Revenue Position (meridian-ops tenant)"]
```

This means:

1. Stripe is a settlement rail, not source of truth
2. Reconciliation compares Stripe to ledger (not the reverse)
3. Revenue recognition happens at position creation

### B. Staff vs Party Identity

| Aspect | Party | Staff |
|--------|-------|-------|
| **Purpose** | Customer with ledger positions | Employee managing the system |
| **Lives in** | Tenant schema (`org_xxx.party`) | Tenant schema (`org_xxx.staff_users`) |
| **Has** | Balances, transactions | API keys, console access |
| **Created by** | Tenant API calls | Admin invitation |
| **Examples** | "John Smith - Customer #123" | "Jane Doe - Acme Admin" |

### C. Manifest Idempotency

The Apply Orchestrator follows Terraform-style semantics:

```text
State = f(Manifest)

Apply(M) when State = {} -> Create all resources
Apply(M) when State = M  -> No-op
Apply(M') when State = M -> Create/Update/Delete delta
```

This enables:

- GitOps workflows (commit manifest, auto-apply)
- Rollback by applying previous version
- Preview changes before apply (plan mode)

### D. AI-Native by Design

Meridian's technology choices were made with AI-assisted configuration
in mind:

| Choice | Why AI-Native |
|--------|---------------|
| **Starlark** | Hermetic, deterministic, excellent error messages with line/column info |
| **CEL** | Strongly typed with inference, "Did you mean X?" suggestions built-in |
| **JSON Schema** | LLMs are pre-trained on JSON, schema provides guardrails |
| **Structured Errors** | Machine-readable errors feed directly back to LLM context |
| **Dry-Run Validation** | Test without side effects, iterate until correct |

#### The Compiler as AI Pair Programmer

Traditional systems require humans to interpret error messages and fix
code. Meridian's compiler produces errors that are:

1. **Specific**: "Line 12, column 5: undefined 'ctx.position_keepng'"
2. **Actionable**: "Did you mean 'ctx.position_keeping'?"
3. **Contextual**: "Available fields: quantity, instrument,
   bucket_id, as_of"
4. **Structured**: JSON format, not prose

This means an LLM can:

1. Generate a Manifest
2. Receive structured validation errors
3. Self-correct without human interpretation
4. Iterate until valid

**Existing Infrastructure That Enables This**:

```go
// shared/pkg/saga/validation/validator.go
type ValidationError struct {
    Line     int    `json:"line"`
    Column   int    `json:"column"`
    Message  string `json:"message"`
    Category string `json:"category"`
}

// services/reference-data/saga/reference_validator.go
type ValidationError struct {
    Reference  Reference
    Message    string
    Suggestion string
    IsCritical bool
}
```

This is not "adding AI" - this is exposing existing compiler
intelligence to external consumers (including AI).
