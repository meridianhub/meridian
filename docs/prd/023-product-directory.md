---
name: prd-product-directory
description: BIAN-aligned AccountTypeRegistry for runtime-configurable product catalog within Reference Data
triggers:
  - Working on account types or product types
  - Adding new account types to the system
  - Configuring product-specific business logic (Starlark sagas, CEL validation per product)
  - Multi-tenant product catalog or blueprint configuration
  - AI-assisted product definition generation
  - Questions about the relationship between accounts and product types
  - BIAN Product Directory service domain
instructions: |
  The AccountTypeRegistry lives within Reference Data (not a separate service).
  It follows the InstrumentRegistry pattern exactly: DRAFT/ACTIVE/DEPRECATED lifecycle,
  is_system flag for platform blueprints, CEL compilation at draft creation, optimistic locking.
  Product definitions reference sagas by name convention ({prefix}.{operation}), not by embedding scripts.
  The manifest remains the authoring surface; the registry is the runtime query surface.
---

# Product Directory - Account Type Registry and Runtime-Configurable Product Catalog

## Status: Not Started

## Executive Summary

Meridian's account type system is fragmented across three separate definitions
with no single source of truth. Account types are hardcoded as proto enums,
preventing tenants from defining custom account types at runtime. This PRD
introduces an AccountTypeRegistry within the Reference Data service -- following
the established InstrumentRegistry pattern -- to provide a runtime-configurable,
BIAN-aligned product catalog that supports multi-tenant customization,
bi-temporal versioning, and AI-assisted product definition.

## Problem Statement

### Current Fragmentation

Account type definitions exist in three disconnected locations:

| Location | Type | Enforcement | Scope |
|----------|------|-------------|-------|
| `internal_bank_account/v1::InternalAccountType` | Proto enum (9 values) | Compile-time + SQL CHECK | Internal accounts only |
| `common/v1/types.proto::AccountType` | Proto enum (6 values) | Compile-time | Financial accounting classification |
| `control_plane/v1/manifest.proto::AccountTypeDefinition` | Manifest message with CEL policies | Runtime validation | Tenant configuration |

Additionally, the Current Account service has an `account_type varchar(50)`
column hardcoded to `"current"` with no validation against any registry.

### What Cannot Be Done Today

1. **Cannot specify a product type when creating an account** --
   `InitiateCurrentAccountRequest` has no `product_type` field.
2. **Cannot define new account types at runtime** -- Adding a type
   requires modifying a Go enum, updating a SQL CHECK constraint,
   recompiling, and redeploying.
3. **Cannot associate different business logic with different product
   types** -- Saga routing is by operation name, not by product type.
4. **Cannot query what account types are available** -- No runtime API
   to list, filter, or inspect account type definitions.

### Existing Infrastructure (Already Built)

The infrastructure needed for a product catalog largely exists:

| Capability | Location | Status |
|------------|----------|--------|
| InstrumentRegistry (DRAFT/ACTIVE/DEPRECATED lifecycle) | `services/reference-data/registry/` | Production |
| CEL compiler with security constraints | `services/reference-data/cel/compiler.go` | Production |
| Saga registry with platform defaults + tenant overrides | `services/reference-data/saga/` | Production |
| Bi-temporal reference data nodes | `services/reference-data/node/` | Production |
| `register_account_type` handler in applier | `services/control-plane/internal/applier/handlers.yaml:57` | Production |
| Manifest `AccountTypeDefinition` with CEL policies | `api/proto/meridian/control_plane/v1/manifest.proto:165` | Production |
| Manifest validation (CEL compilation, cross-references) | `services/control-plane/internal/validator/` | Production |
| Schema-per-tenant isolation | `shared/platform/db/tenant_scope.go` | Production |
| Starlark execution with typed service modules | `shared/pkg/saga/` | Production |
| handlers.yaml schema registry (760 lines, 10 namespaces) | `shared/pkg/saga/schema/handlers.yaml` | Production |

The Product Directory is a **composition of existing capabilities behind a new API surface**, not a new system from scratch.

## BIAN Alignment

### Service Domain Mapping

BIAN 13.0 defines a **Product Directory** service domain (SD-CR-006) that
maintains product/service specifications. BIAN service domains are logical
separations, not deployment mandates. The Product Directory maps to a
**module within Reference Data**, consistent with how instruments, sagas,
and nodes already coexist within Reference Data.

### Behavior Qualifier Mapping

| BIAN BQ | Meridian Mapping | Implementation |
|---------|------------------|----------------|
| **Operations** | CEL policy evaluation, saga routing | Existing CEL compiler + saga naming convention |
| **Servicing** | AccountTypeRegistry CRUD + lifecycle | New (follows InstrumentRegistry pattern) |
| **Production** | Manifest compilation and application | Existing manifest applier + validation pipeline |
| **SalesAndMarketing** | Product catalog browsing and discovery | New read-only projections from registry |

### Separation of Concerns

BIAN Product Directory stores metadata about products, not executable code. Meridian follows this separation:

- **Product catalog** (AccountTypeRegistry): Stores metadata, CEL policies,
  allowed instruments, default saga names. Consistent with how
  InstrumentRegistry stores CEL validation expressions.
- **Saga registry** (existing): Stores executable Starlark scripts.
  Product definitions reference sagas by name, not by embedding scripts.
- **Convention-based routing**: Saga names follow
  `{product_type_code}.{operation}` pattern (e.g., `SAVINGS.deposit`),
  with fallback to `{operation}` for backwards compatibility.

## Solution Design

### AccountTypeRegistry -- New Module in Reference Data

A new Go package `services/reference-data/accounttype/` following the InstrumentRegistry pattern exactly.

#### Data Model

```go
type AccountTypeDefinition struct {
    ID                     uuid.UUID
    Code                   string        // Immutable PK: "CURRENT", "SAVINGS", "ENERGY_SETTLEMENT"
    Version                int           // Allows multiple versions of the same code
    DisplayName            string        // "Personal Current Account"
    Description            string        // Detailed description of this product type
    NormalBalance          string        // "DEBIT" or "CREDIT"
    AllowedInstrumentCodes []string      // ["GBP", "EUR"] -- empty = all allowed
    DefaultSagaPrefix      string        // e.g., "SAVINGS" for "{prefix}.deposit" routing
    ValidationCEL          string        // CEL expression for transaction validation
    BucketingCEL           string        // CEL expression for fungibility bucketing
    EligibilityCEL         string        // CEL expression for account opening eligibility
    AttributeSchema        []byte        // JSON Schema for extensible product attributes
    Attributes             map[string]any // Extensible metadata (fee config, interest config, etc.)
    Status                 Status        // DRAFT, ACTIVE, DEPRECATED
    IsSystem               bool          // Platform blueprint (read-only for tenants)
    SuccessorID            *uuid.UUID    // Points to replacement when deprecated
    CreatedAt              time.Time
    UpdatedAt              time.Time
    ActivatedAt            *time.Time
    DeprecatedAt           *time.Time
}
```

#### Registry Interface

```go
type AccountTypeRegistry interface {
    // Servicing BQ -- Core CRUD + lifecycle
    GetDefinition(ctx context.Context, code string, version int) (*AccountTypeDefinition, error)
    GetActiveDefinition(ctx context.Context, code string) (*AccountTypeDefinition, error)
    ListActive(ctx context.Context) ([]*AccountTypeDefinition, error)
    ListByStatus(ctx context.Context, status Status) ([]*AccountTypeDefinition, error)
    CreateDraft(ctx context.Context, def *AccountTypeDefinition) error
    UpdateDefinition(ctx context.Context, code string, version int, updates *AccountTypeDefinition) error
    ActivateAccountType(ctx context.Context, code string, version int) error
    DeprecateAccountType(ctx context.Context, code string, version int, successorID *uuid.UUID) error

    // Operations BQ -- Policy evaluation
    ValidateTransaction(ctx context.Context, code string, version int, attrs AttributeBag) (ValidationResult, error)
    CheckEligibility(ctx context.Context, code string, version int, attrs AttributeBag) (ValidationResult, error)

    // SalesAndMarketing BQ -- Catalog browsing
    ListByInstrument(ctx context.Context, instrumentCode string) ([]*AccountTypeDefinition, error)
    GetProductFeatures(ctx context.Context, code string, version int) (map[string]any, error)
}
```

#### Database Schema

```sql
CREATE TABLE account_type_definitions (
    id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code                     VARCHAR(50) NOT NULL,
    version                  INT NOT NULL DEFAULT 1,
    display_name             VARCHAR(255) NOT NULL,
    description              TEXT,
    normal_balance           VARCHAR(10) NOT NULL CHECK (normal_balance IN ('DEBIT', 'CREDIT')),
    allowed_instrument_codes TEXT[],
    default_saga_prefix      VARCHAR(100),
    validation_cel           TEXT,
    bucketing_cel            TEXT,
    eligibility_cel          TEXT,
    attribute_schema         JSONB,
    attributes               JSONB DEFAULT '{}',
    status                   VARCHAR(20) NOT NULL DEFAULT 'DRAFT'
                             CHECK (status IN ('DRAFT', 'ACTIVE', 'DEPRECATED')),
    is_system                BOOLEAN NOT NULL DEFAULT FALSE,
    successor_id             UUID REFERENCES account_type_definitions(id),
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    activated_at             TIMESTAMPTZ,
    deprecated_at            TIMESTAMPTZ,

    CONSTRAINT uq_account_type_code_version UNIQUE (code, version),
    CONSTRAINT chk_successor_not_self CHECK (successor_id != id)
);

CREATE INDEX idx_account_type_status ON account_type_definitions (status);
CREATE INDEX idx_account_type_code ON account_type_definitions (code);
```

### Saga Routing Convention

Product types carry a `default_saga_prefix` field. Saga resolution follows:

```text
1. Try: "{default_saga_prefix}.{operation}" with tenant override
2. Try: "{default_saga_prefix}.{operation}" platform default
3. Fallback: "{operation}" (backwards compatible)
```

Example: Product type `SAVINGS` with `default_saga_prefix = "SAVINGS"`:

- Deposit operation resolves: `SAVINGS.deposit` (tenant) -> `SAVINGS.deposit` (platform) -> `deposit` (fallback)

### Multi-Tenant Product Catalogs

Following the saga registry pattern (platform defaults + tenant overrides):

- **Platform blueprints**: Seeded with `is_system = true` during tenant provisioning. Read-only for tenants.
- **Tenant products**: Created by tenants with `is_system = false`. Full CRUD lifecycle.
- **Blueprint extension**: Tenants create their own definitions with custom
  CEL policies and saga prefixes. No inheritance mechanism in v1 -- tenants
  create independent definitions.

### AI-as-Configurator Loop

The manifest remains the authoring surface for AI-generated product definitions:

```yaml
# manifest.yaml -- AI generates this
account_types:
  - code: PREPAID_VOUCHER
    name: "Prepaid Food Voucher"
    normal_balance: DEBIT
    allowed_instruments: ["FOOD_VOUCHER_GBP"]
    policies:
      validation: "amount > 0 && amount <= 500"
      bucketing: ""

sagas:
  - name: PREPAID_VOUCHER.deposit
    script: |
      def execute(ctx):
          position_keeping.initiate_log(
              position_id = ctx.params["account_id"],
              amount = ctx.params["amount"],
              direction = "CREDIT",
          )
```

The compilation pipeline validates:

1. CEL expressions compile successfully (existing CEL compiler)
2. Saga scripts parse and pass dry-run validation (existing saga validator)
3. Cross-references are valid (allowed_instruments reference defined instruments)
4. Structured errors returned for AI iteration

### Compilation Pipeline Endpoint

New endpoint for validating product definitions without persisting:

```protobuf
rpc ValidateProductDefinition(ValidateProductDefinitionRequest) returns (ValidateProductDefinitionResponse);

message ValidateProductDefinitionRequest {
    AccountTypeDefinition definition = 1;
    repeated SagaDefinition associated_sagas = 2;
}

message ValidateProductDefinitionResponse {
    bool valid = 1;
    repeated ValidationError errors = 2;
}

message ValidationError {
    string field = 1;        // e.g., "policies.validation"
    string error_code = 2;   // e.g., "CEL_COMPILATION_FAILED"
    string message = 3;      // Human/AI-readable error description
    int32 line = 4;
    int32 column = 5;
}
```

## Migration Strategy

### Phase 0: Foundation -- AccountTypeRegistry (8 story points)

| Subtask | Points | Description |
|---------|--------|-------------|
| Domain model + interface | 2 | `AccountTypeDefinition`, `AccountTypeRegistry` interface, errors, following InstrumentRegistry |
| PostgreSQL repository + migration | 3 | Table creation, CRUD, lifecycle transitions, is_system, versioning, CEL compilation at draft creation |
| gRPC handler + proto | 2 | Service definition within Reference Data proto, REST annotations |
| Integration tests | 1 | Testcontainer-based tests following existing e2e patterns |

**Dependencies**: None.
**Delivers**: Runtime-queryable product catalog with lifecycle management.

### Phase 1: Account Linkage (5 story points)

| Subtask | Points | Description |
|---------|--------|-------------|
| Add `product_type_code` to Current Account | 1 | New column (nullable initially), update domain model, entity, proto |
| Add `product_type_code` to Internal Bank Account | 1 | New column alongside existing `account_type`, nullable initially |
| Update manifest applier handler | 1 | `register_account_type` creates AccountTypeRegistry entries |
| API surface for account creation | 2 | Accept `product_type_code` in create requests, validate against registry, return in responses |

**Dependencies**: Phase 0.
**Delivers**: Accounts linked to dynamic product definitions. Existing enum continues to work in parallel.

### Phase 2: Consumer Migration (5 story points)

| Subtask | Points | Description |
|---------|--------|-------------|
| Saga routing convention | 2 | Implement `{prefix}.{operation}` resolution with fallback in saga executor |
| Payment-order account resolver | 1 | Replace enum switch with registry lookup |
| Financial-accounting account resolver | 1 | Replace enum switch with registry lookup |
| Remaining consumers | 1 | Smaller services with fewer references |

**Dependencies**: Phase 1.
**Delivers**: All runtime code uses the registry. Convention-based saga routing enables per-product-type workflows.

### Phase 3: Enum Cleanup (3 story points)

| Subtask | Points | Description |
|---------|--------|-------------|
| Remove `InternalAccountType` proto enum | 1 | Replace with string `product_type_code` in proto |
| Remove `common/v1/AccountType` enum | 1 | Consolidate to single source of truth in registry |
| Database migration | 1 | Drop CHECK constraint, add FK to registry, backfill existing accounts |

**Dependencies**: Phase 2 fully deployed and verified.
**Delivers**: Single source of truth. Zero enum fragmentation.

### Phase 4: AI Compilation Pipeline (5 story points) -- Parallel with Phases 1-3

| Subtask | Points | Description |
|---------|--------|-------------|
| `ValidateProductDefinition` endpoint | 2 | Accept manifest fragment, compile CEL, validate references, return structured errors |
| Dry-run mode | 2 | Full validation without persisting, including cross-reference checks |
| Structured error format | 1 | Machine-readable errors with field paths, error codes, line/column for AI feedback loop |

**Dependencies**: Phase 0 only.
**Delivers**: AI-as-configurator loop.

### Total: 26 story points across 5 phases

```text
Phase 0 (8pt) --> Phase 1 (5pt) --> Phase 2 (5pt) --> Phase 3 (3pt)
      |
      +---------> Phase 4 (5pt) [parallel]
```

## Platform Blueprint Seed Data

System account types seeded during tenant provisioning:

| Code | Display Name | Normal Balance | Default Instruments | Saga Prefix |
|------|-------------|----------------|---------------------|-------------|
| `CURRENT` | Current Account | DEBIT | All monetary | `CURRENT` |
| `SAVINGS` | Savings Account | DEBIT | All monetary | `SAVINGS` |
| `CLEARING` | Clearing Account | DEBIT | All | `CLEARING` |
| `NOSTRO` | Nostro Account | DEBIT | All monetary | `NOSTRO` |
| `VOSTRO` | Vostro Account | CREDIT | All monetary | `VOSTRO` |
| `HOLDING` | Holding Account | DEBIT | All | `HOLDING` |
| `SUSPENSE` | Suspense Account | DEBIT | All | `SUSPENSE` |
| `REVENUE` | Revenue Account | CREDIT | All monetary | `REVENUE` |
| `EXPENSE` | Expense Account | DEBIT | All monetary | `EXPENSE` |
| `INVENTORY` | Inventory Account | DEBIT | All | `INVENTORY` |

## Non-Goals for v1

- **Product inheritance/composition**: No "SAVINGS extends DEPOSIT" or
  mixin system. Tenants create independent definitions. Composition
  deferred to a future Product Combination service.
- **Product bundles**: No bundling of multiple products into a combined
  offering.
- **Pricing engine integration**: Fee schedules stored as extensible
  attributes, not as a first-class pricing domain.
- **Approval workflows**: No multi-step approval chain for product
  definition changes. Lifecycle transitions are immediate. Governance
  deferred to a future Product Design service domain.
- **UI**: Control plane UI is out of scope. REST API is the interaction surface.
- **MCP server**: MCP integration for AI interaction is a future enhancement.

## Success Criteria

1. A tenant can define a new account type (e.g., `ENERGY_SETTLEMENT`) via manifest without any code changes or deployments.
2. Accounts created with `product_type_code = "ENERGY_SETTLEMENT"`
   automatically route to the correct Starlark saga.
3. The AI-as-configurator loop works: AI generates a manifest with a new
   product type, submits for validation, receives structured errors,
   iterates until compilation succeeds.
4. Existing accounts and services continue to work unchanged during migration (backwards compatible).
5. Platform blueprints are seeded during tenant provisioning and are read-only for tenants.
6. `InternalAccountType` and `common/v1/AccountType` proto enums are fully removed by Phase 3 completion.

## Testing Strategy

- **Unit tests**: Domain model validation, lifecycle transitions, CEL
  compilation, optimistic locking. Following InstrumentRegistry test
  patterns.
- **Integration tests**: Testcontainer-based (CockroachDB). Full registry
  CRUD, multi-tenant isolation, platform blueprint seeding.
- **E2E tests**: Manifest apply with account type registration, account creation with product_type_code, saga routing verification.
- **Migration tests**: Verify existing accounts work during and after each phase.

## References

- **InstrumentRegistry** (pattern template): `services/reference-data/registry/registry.go`
- **Saga registry**: `services/reference-data/saga/`
- **Manifest AccountTypeDefinition**: `api/proto/meridian/control_plane/v1/manifest.proto:165`
- **Applier handlers.yaml**: `services/control-plane/internal/applier/handlers.yaml:57`
- **CEL compiler**: `services/reference-data/cel/compiler.go`
- **BIAN alignment PRD**: `.taskmaster/docs/prd-bian-alignment.md`
- **BIAN 13.0 Product Directory**: SD-CR-006 (external)
