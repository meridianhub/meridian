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

### Issue 3: Confusing Manifest Input Format

**Current behaviour:** The `meridian_manifest_validate` tool
description says "Validate a manifest YAML/JSON" but the input
schema declares `manifest` as `type: "object"`. This creates
confusion: the description suggests YAML strings are accepted,
but only a parsed JSON object works. Passing a YAML string
produces a type error (`expected object, but got string`) with
no guidance on the correct format.

**Location:**
`services/mcp-server/internal/tools/economy.go:86-97` — the
description mentions "YAML/JSON" but `InputSchema` requires
`type: "object"`.

**Expected behaviour:** Either:

- (a) Accept both formats: if the input is a string, attempt to
  parse it as YAML first, then JSON, before proto conversion.
  This matches the tool description and is more forgiving for
  LLM callers that naturally produce YAML.
- (b) Update the description to explicitly state "JSON object"
  and remove the YAML reference, so callers know to pass a
  parsed object rather than a string.

Option (a) is preferred since YAML is the canonical format used
in cookbook patterns and documentation. LLMs generating manifests
will naturally produce YAML strings.

### Issue 4: Economy Generator Service Unavailable

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
3. **Input format confusion** — The tool description says
   "YAML/JSON" but only accepts a JSON object, causing silent
   failures and trial-and-error for LLM callers.
4. **Blocked primary workflow** — The AI generator being unavailable
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

- `mode`: `"create"` or `"amend"` (default: `"create"`)
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

### 3. Accept YAML string input in manifest tools

Update `manifestJSONToProto` (or a wrapper) to detect whether the
input is a string (YAML/JSON text) or an object (already-parsed
JSON). If a string is received, parse it as YAML first (which is
a superset of JSON), then proceed with proto conversion.

This applies to `meridian_manifest_validate`,
`meridian_manifest_plan`, and `meridian_manifest_apply`.

### 4. Deploy or gracefully degrade the Economy Generator Service

Either:

- (a) Deploy `EconomyGeneratorService` on the demo instance
  alongside the control plane, or
- (b) Wrap the gRPC client call in the MCP tool handler to detect
  "unknown service" errors and return a user-friendly message:
  "Economy generator is not available on this instance. Use
  meridian_manifest_validate to check manually composed manifests."

### 5. Add `meridian_manifest_schema` reference tool

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

1. `meridian_manifest_validate` with `mode: "create"` validates
   a manifest without comparing against existing tenant state
2. Accessing the MCP server at the base domain does not silently
   execute tools against an arbitrary tenant
3. Manifest tools accept both YAML strings and JSON objects as
   input without type errors
4. `meridian_economy_generate` either works on demo or returns a
   clear "service unavailable" message
5. Tenant-agnostic reference tools exist as defined in Issue 5:
   `meridian_manifest_schema` (schema only),
   `meridian_topics_list`, `meridian_starlark_reference`,
   `meridian_cookbook_list/get`, `meridian_gateway_guide`
6. Cookbook patterns correctly guide financial vs operational
   gateway selection for payment flows

## Files Affected

| File | Change |
|------|--------|
| `services/mcp-server/internal/tools/economy.go` | Add mode/tenant_id to validate |
| `services/mcp-server/internal/tools/reference.go` | New file: reference tools (see Issue 5) |
| `services/mcp-server/internal/auth/subdomain.go` | Explicit tenant context enforcement |
| `services/control-plane/.../manifest_validator.go` | Support skip-immutability mode |
| `api/proto/.../manifest.proto` | Optional: `skip_immutability_checks` field |
| `services/mcp-server/.../economy_generator.go` | Graceful degradation for unavailable service |

## Issue 5: Source Code Knowledge Not Exposed via MCP

During the economy design session, several critical decisions
required reading source code that an MCP-only consumer would not
have access to. The MCP server is designed as an AI view over
existing gRPC endpoints with RBAC — but to be effective for
economy design, it needs to expose the reference data that
currently lives only in source files.

### What was needed and where it came from

| Information Needed | Source (code) | MCP Tool Gap |
|---|---|---|
| Registered event topics | `shared/platform/events/topics/topics.go` | No tool exposes this list |
| Manifest field schema (enums, constraints, patterns) | `api/proto/.../manifest.proto` | No schema introspection tool |
| Available Starlark built-ins and service modules | Starlark VM registration code | No tool lists available ctx bindings |
| Cookbook pattern examples | `cookbook/patterns/*/manifest-fragment.yaml` | `meridian_economy_generate_context` should serve this but the service is down |
| Gateway distinction (financial vs operational) | Service architecture knowledge | No tool explains when to use which |
| Handler signatures and parameters | `handlers.yaml` / proto definitions | `meridian_handlers_describe` exists but requires tenant context |

### What MCP should expose (without source code access)

**1. `meridian_manifest_schema`** — covers schema enums,
constraints, and field definitions only (not event topics or
Starlark bindings, which have dedicated tools below).

**2. `meridian_topics_list`** — returns all registered event
topics from `topics.All()`. This is a static list that does not
require tenant context. Without it, an MCP consumer cannot know
which event triggers are valid and must guess or fail validation
repeatedly.

**3. `meridian_starlark_reference`** — returns available service
module bindings (`ctx.position_keeping`, `ctx.repository`, etc.),
their methods, and parameter signatures. Currently this requires
reading the Starlark VM registration code.

**4. `meridian_cookbook_list` / `meridian_cookbook_get`** — browse
and retrieve cookbook patterns without needing the generator
service. The patterns are static YAML files that provide
worked examples. An MCP consumer composing a manifest manually
needs these as reference material.

**5. `meridian_gateway_guide`** — returns guidance on when to
use the financial gateway vs the operational gateway (see
Issue 6 below). This could be a static reference or derived
from the handler registry.

### Design principle

These tools should be **tenant-agnostic** (no tenant context
required) and **read-only** (no state mutation). They expose
platform reference data, not tenant-specific configuration.
This matches the MCP design of providing an AI-friendly view
over existing system knowledge.

## Issue 6: Financial Gateway vs Operational Gateway Confusion

### The problem

During the design session, the generated manifest routed Stripe
payment collection and payouts through the **operational
gateway** (`operational_gateway.instruction_routes`). However,
Meridian has a dedicated **financial gateway** service
(`financial-gateway`) specifically designed for payment
processing, with:

- Built-in Stripe webhook handling
  (`financial-gateway.payment-captured.v1`, etc.)
- Payment-specific event topics
- Dedicated proto definitions for payment lifecycle

The operational gateway is designed for general-purpose external
provider integrations (APIs, webhooks, MQTT, AMQP), not for
payment-specific flows that the financial gateway already
handles.

### Why this happened

1. The `payment-gateway-stripe` cookbook pattern uses
   `operational_gateway` configuration, which the manifest
   composer followed as precedent
2. No MCP tool or cookbook guidance explains the distinction
   between financial and operational gateways
3. The manifest schema accepts payment-related instruction
   routes in `operational_gateway` without warning that a
   more appropriate gateway exists

### Cookbook improvements needed

- The `payment-gateway-stripe` cookbook pattern should be
  updated to use the financial gateway where appropriate,
  or clearly document when operational gateway is the
  correct choice for payment flows
- A new cookbook topic or pattern preamble should explain:
  - **Financial gateway**: Use for payment collection,
    refunds, disputes — any flow where Meridian manages
    the payment lifecycle and emits payment-specific events
  - **Operational gateway**: Use for general external API
    calls, non-payment webhooks, IoT/MQTT, or providers
    without a dedicated gateway integration
- The `meridian_economy_generate_context` tool should
  surface this guidance when payment-related patterns are
  matched

### Files affected

| File | Change |
|---|---|
| `cookbook/patterns/payment-gateway-stripe/` | Review and update to use financial gateway |
| Cookbook authoring docs | Add gateway selection guidance |
| MCP generator context | Include gateway guidance in matched patterns |

## Context: The Economy Design Session

For reference, the tote betting platform manifest that exposed
these issues included:

- 2 instruments (GBP fiat, BET_UNIT voucher)
- 4 account types
  (STRIPE_NOSTRO, SYNDICATE_POOL, BET_POSITION,
  PLATFORM_COMMISSION)
- 4 sagas
  (create, join, settle, refund syndicate)
- Stripe Connect payment rails and operational gateway
- Event-driven settlement via
  `market-information.observation-recorded.v1`

All structural validation passed. The only errors were false
positives from tenant state comparison. The Stripe integration
was routed through the operational gateway rather than the
financial gateway, which should be corrected in future
iterations.
