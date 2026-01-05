# Reference Data Service

The Reference Data service manages instrument definitions for the Meridian ledger system.
It is aligned with the BIAN Reference Data Directory domain.

## Overview

Instrument definitions describe measurement units, currencies, and asset types that can be
tracked in the ledger. Each instrument specifies:

- **Dimension**: What type of value it measures (MONETARY, ENERGY, QUANTITY, etc.)
- **Precision**: Decimal places for amounts (0-18)
- **Validation Rules**: CEL expressions for validating quantities
- **Fungibility Rules**: CEL expressions for bucket key generation

## Architecture

### System Instruments vs Tenant Instruments

The service supports two types of instruments:

**System Instruments** (`is_system=true`):

- Pre-defined instruments like USD, EUR, GBP, KWH
- **Seeded during tenant provisioning** by the tenant provisioner service
- Read-only through the InstrumentRegistry API
- Visible to all operations within a tenant's schema

**Tenant Instruments** (`is_system=false`):

- Created by tenants through the InstrumentRegistry API
- Full lifecycle management (DRAFT → ACTIVE → DEPRECATED)
- Tenant-specific and isolated

### Multi-Tenant Isolation

This service uses schema-per-tenant architecture:

- Each tenant has an isolated PostgreSQL schema (`org_<tenant_id>`)
- System instruments are COPIED into each tenant's schema during provisioning
- No cross-schema queries or SystemTenantID constants needed
- Tenant context extracted from ctx via `shared/platform/tenant`

## Component Responsibilities

### Reference-Data Service (This Service)

- Enforces `is_system` read-only constraint
- Manages tenant instrument lifecycle
- Compiles and executes CEL validation rules
- **Does NOT seed system instruments**

### Tenant Provisioner Service (services/tenant)

- Creates tenant schemas
- Seeds system instruments with `is_system=true`
- See: `services/tenant/provisioner/postgres_provisioner.go`

## Instrument Lifecycle

```text
DRAFT ─────► ACTIVE ─────► DEPRECATED
  │            │               │
  │ Editable   │ Immutable     │ Read-only
  │            │ validation    │ not for new
  └────────────┴───────────────┴─────────────
```

### Status Transitions

| From | To | Allowed | Notes |
|------|----|---------|-------|
| DRAFT | ACTIVE | ✓ | Sets `activated_at` |
| ACTIVE | DEPRECATED | ✓ | Sets `deprecated_at` |
| DRAFT | DEPRECATED | ✗ | Must activate first |
| ACTIVE | DRAFT | ✗ | Cannot revert activation |
| DEPRECATED | * | ✗ | Terminal state |

### System Instrument Protection

All modification operations reject system instruments:

- `CreateDraft` - Cannot create with `is_system=true`
- `UpdateDefinition` - Cannot update system instruments
- `ActivateInstrument` - Cannot activate system instruments
- `DeprecateInstrument` - Cannot deprecate system instruments

Read operations work normally for system instruments:

- `GetDefinition` - Returns system instruments
- `GetActiveDefinition` - Returns active system instruments
- `ListActive` - Includes both system and tenant instruments

## CEL Validation

Instrument definitions can include CEL expressions for validation.

### Available Variables

```cel
attributes: map[string]string  // Key-value attributes from quantity
amount: string                 // Decimal amount as string
valid_from: timestamp          // Optional validity start
valid_to: timestamp           // Optional validity end
source: string                // Origin identifier
```

### Example Expressions

```cel
// Validate positive amounts
parse_int(amount) > 0

// Validate renewable energy source
attributes["source_type"] == "renewable"

// Validate time range
valid_from < valid_to

// Complex validation
parse_int(amount) > 0 &&
attributes.exists("batch_id") &&
parse_iso_date(attributes["expiry"]) > now()
```

### CEL Compilation

Expressions are compiled at creation time (fail-fast):

- Invalid expressions reject the CreateDraft/UpdateDefinition call
- Compiled programs are cached for performance
- CEL cost limits prevent expensive expressions (max 10,000 cost units)

## Database Schema

### instrument_definition Table

| Column | Type | Description |
|--------|------|-------------|
| id | UUID | Primary key |
| code | VARCHAR(50) | Instrument code (e.g., "USD") |
| version | INTEGER | Version number |
| dimension | VARCHAR(20) | MONETARY, ENERGY, QUANTITY, etc. |
| precision | INTEGER | Decimal places (0-18) |
| status | VARCHAR(20) | DRAFT, ACTIVE, DEPRECATED |
| is_system | BOOLEAN | True for system instruments |
| validation_expression | TEXT | CEL validation rule |
| fungibility_key_expression | TEXT | CEL bucket key rule |
| error_message_expression | TEXT | CEL custom error message |
| attribute_schema | JSONB | JSON schema for attributes |
| display_name | VARCHAR(255) | Human-readable name |
| description | TEXT | Additional context |
| created_at | TIMESTAMPTZ | Creation timestamp |
| updated_at | TIMESTAMPTZ | Last modification |
| activated_at | TIMESTAMPTZ | When status became ACTIVE |
| deprecated_at | TIMESTAMPTZ | When status became DEPRECATED |

### Migrations

Located in `migrations/`:

- `20260104000001_initial.sql` - Initial schema and triggers
- `20260105000001_add_is_system.sql` - Adds is_system column

**Note**: System instruments are NOT seeded by migrations. They are seeded by the tenant provisioning service.

## Usage Example

```go
// Create registry
pool, _ := pgxpool.New(ctx, connStr)
registry, _ := registry.NewPostgresRegistry(pool)

// Create tenant instrument
def := &registry.InstrumentDefinition{
    Code:                 "CARBON_CREDIT",
    Version:              1,
    Dimension:            registry.DimensionQuantity,
    Precision:            0,
    ValidationExpression: `parse_int(amount) > 0 && attributes.exists("vintage_year")`,
    DisplayName:          "Carbon Credit",
}
registry.CreateDraft(ctx, def)
registry.ActivateInstrument(ctx, "CARBON_CREDIT", 1)

// Validate a quantity
result, _ := registry.ValidateAttributes(ctx, "CARBON_CREDIT", 1, registry.AttributeBag{
    Amount:     "100",
    Attributes: map[string]string{"vintage_year": "2024"},
})
// result.Valid == true
```

## Testing

Integration tests use Testcontainers:

```bash
go test ./services/reference-data/... -v
```

Tests cover:

- CRUD operations
- Lifecycle transitions
- System instrument protection
- CEL validation
- Tenant isolation
