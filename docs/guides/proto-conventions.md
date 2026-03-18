# Proto Import Alias Conventions

This guide documents the canonical import alias pattern for generated protobuf packages
and maps every proto path in the repository to its Go alias.

## The Rule

Always use the **Go package name as declared in the generated file** as the import alias.
Generated package names follow the pattern `<domain>v1` (all lowercase, underscores removed).

```go
// Correct — alias matches the declared Go package name
import (
    auditv1         "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
    controlplanev1  "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
    commonv1        "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
)
```

The generated package name already encodes the version (`v1`), so no alias is strictly
required when there is no ambiguity. Explicit aliases are required when the import path
diverges from what `goimports` would infer, which is always the case for multi-word
proto paths like `control_plane` (path segment) → `controlplanev1` (package name).

### What to Avoid

| Pattern | Problem |
|---------|---------|
| `pb ".../<domain>/v1"` | Generic — ambiguous when multiple proto packages are imported |
| `commonpb "..."` | Inconsistent suffix (`pb` vs `v1`) |
| `quantitypb "..."` | Non-canonical — use `quantityv1` |
| `tenantpb "..."` | Non-canonical — use `tenantv1` |

The `pb` shorthand appears in some existing files where only one proto package is
imported. Prefer the canonical alias in new code; update existing files opportunistically
when touching them.

### Blank Imports

Use `_` only for side-effect registration (proto type registration for gateway routing,
reflection, etc.):

```go
import (
    _ "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
)
```

---

## Proto Path → Go Alias Map

| Proto path | Go import path | Canonical alias |
|------------|---------------|-----------------|
| `meridian/audit/v1` | `api/proto/meridian/audit/v1` | `auditv1` |
| `meridian/common/v1` | `api/proto/meridian/common/v1` | `commonv1` |
| `meridian/control_plane/v1` | `api/proto/meridian/control_plane/v1` | `controlplanev1` |
| `meridian/current_account/v1` | `api/proto/meridian/current_account/v1` | `currentaccountv1` |
| `meridian/events/v1` | `api/proto/meridian/events/v1` | `eventsv1` |
| `meridian/financial_accounting/v1` | `api/proto/meridian/financial_accounting/v1` | `financialaccountingv1` |
| `meridian/financial_gateway/v1` | `api/proto/meridian/financial_gateway/v1` | `financialgatewayv1` |
| `meridian/financial_gateway_events/v1` | `api/proto/meridian/financial_gateway_events/v1` | `financialgatewayeventsv1` |
| `meridian/forecasting/v1` | `api/proto/meridian/forecasting/v1` | `forecastingv1` |
| `meridian/identity/v1` | `api/proto/meridian/identity/v1` | `identityv1` |
| `meridian/internal_account/v1` | `api/proto/meridian/internal_account/v1` | `internalaccountv1` |
| `meridian/mapping/v1` | `api/proto/meridian/mapping/v1` | `mappingv1` |
| `meridian/market_information/v1` | `api/proto/meridian/market_information/v1` | `marketinformationv1` |
| `meridian/operational_gateway/v1` | `api/proto/meridian/operational_gateway/v1` | `operationalgatewayv1` |
| `meridian/party/v1` | `api/proto/meridian/party/v1` | `partyv1` |
| `meridian/payment_order/v1` | `api/proto/meridian/payment_order/v1` | `paymentorderv1` |
| `meridian/platform/v1` | `api/proto/meridian/platform/v1` | `platformv1` |
| `meridian/position_keeping/v1` | `api/proto/meridian/position_keeping/v1` | `positionkeepingv1` |
| `meridian/quantity/v1` | `api/proto/meridian/quantity/v1` | `quantityv1` |
| `meridian/reconciliation/v1` | `api/proto/meridian/reconciliation/v1` | `reconciliationv1` |
| `meridian/reference_data/v1` | `api/proto/meridian/reference_data/v1` | `referencedatav1` |
| `meridian/saga/v1` | `api/proto/meridian/saga/v1` | `sagav1` |
| `meridian/tenant/v1` | `api/proto/meridian/tenant/v1` | `tenantv1` |
| `meridian/valuation_feature/v1` | `api/proto/meridian/valuation_feature/v1` | `valuationfeaturev1` |

The full module prefix is `github.com/meridianhub/meridian/`. A complete import for
`audit/v1` looks like:

```go
auditv1 "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
```

---

## Regenerating Proto Files

Proto-generated files (`*.pb.go`, `*_grpc.pb.go`) and OpenAPI specs (`api/openapi/`) are
committed to the repository. Regenerate them whenever a `.proto` file changes:

```bash
buf generate api/proto
```

Run from the repository root. The `buf.gen.yaml` file controls output paths:

- Go bindings → `api/proto/meridian/<domain>/v1/`
- OpenAPI specs → `api/openapi/`

After regenerating, commit the changed files alongside the `.proto` changes in the same
commit so the two are always in sync.

CI enforces this with the `proto-freshness` job in `.github/workflows/quality.yml`.

---

## Adding a New Proto Package

1. Create `.proto` files under `api/proto/meridian/<domain>/v1/`.
2. Set the `go_package` option to `github.com/meridianhub/meridian/api/proto/meridian/<domain>/v1;<domain>v1`.
3. Run `buf generate api/proto` from the repo root.
4. Add the new entry to the table above.
5. Use `<domain>v1` as the import alias in all consuming Go files.
