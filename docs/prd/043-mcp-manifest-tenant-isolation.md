# PRD-043: MCP Manifest Validation Tenant Isolation

## Problem Statement

The MCP manifest validation tooling has three related issues that
prevent effective economy design for new tenants and create silent
tenant leakage when the MCP server is accessed without a tenant
subdomain.

These issues were discovered during a live economy design session
where a complete tote betting platform manifest was composed from
scratch. The manifest passed all structural, CEL, Starlark, and
event channel validation, but produced false-positive errors because
the validator compared against an existing energy-trading tenant's
manifest — despite the intent being to create a new, independent
tenant.

## Issues

### Issue 1: Manifest Validate Tool Lacks Create vs Amend Mode

**Current behaviour:** `meridian_manifest_validate` always calls
`ApplyManifest` with `DryRun: true` against the current tenant's
existing manifest. This produces `IMMUTABLE_FIELD_CHANGED` and
`DESTRUCTIVE_INSTRUMENT_REMOVAL` errors when validating a manifest
intended for a new tenant.

**Expected behaviour:** The tool should support a `mode` parameter
(`create` | `amend`), consistent with `meridian_economy_generate`
which already has this distinction. In `create` mode, immutability
and destructive removal checks should be skipped since there is no
prior state to compare against.

**Location:**
`services/mcp-server/internal/tools/economy.go:82-103` —
`buildManifestValidateTool` has no mode/tenant_id parameters.

**Contrast with economy_generate:**
`services/mcp-server/internal/tools/economy_generator.go` already
accepts `mode: "create" | "amend"` and an optional `tenant_id`. The
validate tool should follow the same pattern.

### Issue 2: Base Domain MCP Access Silently Inherits Tenant Context

**Current behaviour:** When the MCP server is accessed at the base
domain (`demo.meridianhub.cloud/mcp`) without a tenant subdomain
(e.g., `acme.demo.meridianhub.cloud/mcp`), the
`TenantSubdomainMiddleware` allows the request through with no
tenant scoping (lines 67-73 of
`services/mcp-server/internal/auth/subdomain.go`):

```go
subdomainSlug := extractSubdomain(r.Host, m.baseDomain)
if subdomainSlug == "" {
    // No subdomain present — request is to the base domain directly.
    // Allow it through (no tenant scoping needed).
    next.ServeHTTP(w, r)
    return
}
```

However, downstream gRPC calls (e.g., `ApplyManifest`) resolve
tenant context from the request context. When no subdomain is
present, the tenant context is either:

- Inherited from the JWT token's tenant claim (if provided), or
- Resolved to a system default / first-available tenant

This means tools that are conceptually tenant-agnostic (like
validating a manifest for a new tenant) silently execute against an
existing tenant's state, producing confusing validation errors with
no indication of which tenant was used.

**Expected behaviour:** One of:

- (a) Tools that operate against tenant state should fail explicitly
  when no tenant context is available, with a clear error:
  "No tenant context — use a tenant subdomain or provide a
  tenant_id parameter."
- (b) Tools that support tenant-agnostic operation (like validate in
  `create` mode) should be explicitly marked as such and bypass
  tenant state comparison.

### Issue 3: Economy Generator Service Unavailable

**Current behaviour:** `meridian_economy_generate` and
`meridian_economy_generate_context` fail with
`unknown service meridian.control_plane.v1.EconomyGeneratorService`
on the demo instance.

**Impact:** The AI-assisted manifest generation workflow — the
primary intended path for economy design — is completely
unavailable. This forces manual manifest composition by
reverse-engineering the schema from proto definitions and cookbook
patterns, which is slower and error-prone.

**Expected behaviour:** The Economy Generator Service should be
deployed and registered on the demo instance, or the MCP server
should gracefully degrade with a clear error message indicating
the service is not available (rather than a raw gRPC
"unknown service" error).

**Note:** `RegisterEconomyTools` in `economy.go:48-68` already
skips tool registration when a dependency is nil. The issue is
likely that the gRPC client connects successfully but the service
is not registered on the target server.

## Impact

These issues collectively degrade the economy design workflow:

1. **False confidence erosion** — A valid manifest for a new tenant
   shows errors, making the operator question whether the manifest
   is actually correct.
2. **Silent tenant leakage** — Operating on the wrong tenant's data
   without explicit indication is a security and correctness
   concern.
3. **Blocked primary workflow** — The AI generator being unavailable
   forces the manual path, which requires deep knowledge of the
   proto schema, registered event topics, handler signatures, and
   Starlark built-ins.

## Proposed Changes

### 1. Add `mode` and `tenant_id` to `meridian_manifest_validate`

```go
// manifestValidateParams — updated
type manifestValidateParams struct {
    Manifest json.RawMessage `json:"manifest"`
    Mode     string          `json:"mode"`
    TenantID string          `json:"tenant_id"`
}
```

- `mode`: `"create"` or `"amend"` (default: `"amend"`)
- `tenant_id`: required when mode is `"create"` to specify target

In `create` mode, the `ApplyManifest` request should signal that
no prior manifest comparison should be performed. This may require:

- A new field on `ApplyManifestRequest`
  (e.g., `skip_immutability_checks: true`), or
- A separate validation endpoint that performs structural-only
  validation without tenant state

### 2. Explicit tenant context handling in MCP middleware

When no subdomain is present and a tool requires tenant context,
the MCP server should:

- Check if the tool's handler requires tenant context
- If yes: return a clear error before invoking the gRPC call
- If no: proceed without tenant scoping

Alternatively, the `ApplyManifest` gRPC handler itself should
return a clear error when tenant context is missing, rather than
falling back to a default.

### 3. Deploy or gracefully degrade the Economy Generator Service

Either:

- (a) Deploy `EconomyGeneratorService` on the demo instance
  alongside the control plane, or
- (b) Wrap the gRPC client call in the MCP tool handler to detect
  "unknown service" errors and return a user-friendly message:
  "Economy generator is not available on this instance. Use
  meridian_manifest_validate to check manually composed manifests."

### 4. Add `meridian_manifest_schema` reference tool

A new read-only tool that returns the full manifest schema (field
names, types, enums, constraints) without requiring any tenant
context. This supports manual manifest composition when the
generator is unavailable.

This tool should return:

- All instrument types
- All normal balance values
- All valuation methods
- All registered event topics
  (from `shared/platform/events/topics/topics.go`)
- Trigger format patterns
  (`api:`, `webhook:`, `scheduled:`, `event:`)
- Field constraints (max lengths, regex patterns, required fields)
- Available Starlark service modules and built-ins

## Success Criteria

1. `meridian_manifest_validate` with `mode: "create"` validates a
   manifest without comparing against existing tenant state
2. Accessing the MCP server at the base domain does not silently
   execute tools against an arbitrary tenant
3. `meridian_economy_generate` either works on demo or returns a
   clear "service unavailable" message
4. A new `meridian_manifest_schema` tool returns the full schema
   reference without tenant context

## Files Affected

| File | Change |
|------|--------|
| `services/mcp-server/internal/tools/economy.go` | Add mode/tenant_id to validate, add schema tool |
| `services/mcp-server/internal/auth/subdomain.go` | Explicit tenant context enforcement |
| `services/control-plane/.../manifest_validator.go` | Support skip-immutability mode |
| `api/proto/.../manifest.proto` | Optional: `skip_immutability_checks` field |
| `services/mcp-server/.../economy_generator.go` | Graceful degradation for unavailable service |

## Context: The Economy Design Session

For reference, the tote betting platform manifest that exposed
these issues included:

- 2 instruments (GBP fiat, BET_UNIT voucher)
- 4 account types
  (STRIPE_NOSTRO, SYNDICATE_POOL, BET_POSITION, PLATFORM_COMMISSION)
- 4 sagas
  (create, join, settle, refund syndicate)
- Stripe Connect payment rails and operational gateway
- Event-driven settlement via
  `market-information.observation-recorded.v1`

All structural validation passed. The only errors were false
positives from tenant state comparison.
