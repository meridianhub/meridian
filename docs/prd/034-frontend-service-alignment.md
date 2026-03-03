# PRD: Frontend Service Alignment & Tenant UI Architecture

## Problem Statement

The Meridian frontend has grown to 18 page directories,
25+ shared components, and 17 service clients. Pages call
service clients inline via `useClients()` with ad-hoc query
construction. There is no intermediate layer tying a UI
feature area to the service that powers it. Additionally,
the current architecture assumes a single fixed UI experience,
with no mechanism for tenant-level customisation or
differentiation between staff and customer views.

This makes it difficult to:

1. **Find what to update** when a service API changes
2. **Reuse data-fetching logic** across pages calling the
   same service
3. **Develop and test components in isolation**
4. **Offer tenant-specific UI experiences** (branding,
   feature visibility, layout)
5. **Distinguish staff operations views from customer-facing
   views**

## Vision

Meridian is a completely programmable financial platform.
Tenants already configure instruments, account types, saga
workflows, and validation rules through manifests - the UI
should be no different. Tenant UI customisation is a
**runtime concern**, not a deployment. No rebuild, no
redeploy - a tenant changes their UI config and the console
reflects it on the next page load.

The frontend should:

- **Default experience**: A production-grade operations
  console that works out of the box for any tenant, zero
  configuration required
- **Staff customisation**: Tenants configure which features
  their operations staff see, dashboard layout, and
  branding - all at runtime via the same manifest/config
  mechanism that drives the backend
- **Customer portal**: Tenants offer their end-customers a
  self-service view (account balances, transaction history,
  statements) using the same component library but with a
  different shell and reduced scope

The component library (via Storybook) becomes the catalogue
of building blocks. Storybook serves two audiences:
developers building features, and tenant administrators
previewing what's available to configure.

### Runtime, Not Deployable

Tenant UI configuration follows the same pattern as every
other Meridian config surface:

1. **Stored in tenant config** (alongside existing tenant
   entity or manifest)
2. **Loaded at login** (fetched with tenant context, cached
   in React Query with cache key including tenant slug)
   - On tenant switch or logout, config cache is
     invalidated and re-fetched
   - On fetch failure, app falls back to default UI config
     and shows a non-blocking warning banner
   - Background revalidation on window focus applies
     updates without requiring page reload
3. **Applied immediately** (CSS variables for theme, feature
   flags for visibility, layout config for dashboard
   composition)
4. **No build step** - the same compiled SPA serves every
   tenant, every customisation is data-driven

This is the same architectural principle as Starlark sagas:
the platform ships the execution engine, tenants supply
the configuration. For the UI, the platform ships the
component library and app shell, tenants supply theme +
feature visibility + layout preferences.

## Goals

1. Restructure frontend into **feature modules** aligned to
   backend services
2. Extract **service-aligned data hooks** to replace inline
   query construction
3. Set up **Storybook** as a component catalogue and
   development environment
4. Design the **tenant UI customisation** architecture
   (theme, feature toggles, layout)
5. Establish a **service coverage map** linking RPCs to UI
   elements

## Non-Goals

- Building a full customer portal (future work, but
  architecture should not preclude it)
- Visual redesign of existing components
- Changing the API layer (Connect-RPC, transport,
  interceptors)
- Adding new backend RPCs
- Micro-frontend architecture (see Architectural Decision
  below)

## Architectural Decision: Centralised Frontend

**Decision**: Keep UI centralised in `frontend/` with
feature modules, not colocated in `services/<service>/`.

**Rationale**:

| Approach | Pros | Cons |
|----------|------|------|
| Colocated | Service team owns full stack | Module federation, shared state, N builds |
| Centralised | Single build, shared state, proto works | Logical separation only |

The centralised approach gives us service alignment through
directory structure and import boundaries, without
micro-frontend infrastructure costs. The proto-generated
clients already provide compile-time coupling between
frontend and backend - the feature module structure makes
that coupling visible in the file tree.

If Meridian ever needs independent deployment of UI modules
(e.g., a marketplace of tenant plugins), that's a future
migration from feature modules to micro-frontends. The
feature module structure makes that migration easier, not
harder.

## Current State

### Frontend Structure

```text
src/
├── api/              # Connect-RPC clients + transport
├── components/
│   ├── ui/           # shadcn/ui primitives
│   ├── shared/       # 25+ domain components (mixed)
│   ├── layout/       # AppShell, Header, Sidebar
│   └── reconciliation/
├── contexts/         # Auth + Tenant
├── hooks/            # 5 hooks, all cross-cutting
├── lib/              # query-client, query-keys, helpers
├── pages/            # 18 flat directories
└── App.tsx           # All routes in one file
```

### Service-to-Page Mapping

| Backend Service | Pages | Client Key |
|----------------|-------|------------|
| current-account | `/accounts`, `/transactions` | `currentAccount` |
| payment-order | `/payments` | `paymentOrder` |
| financial-accounting | `/ledger` | `financialAccounting` |
| position-keeping | `/positions` | `positionKeeping` |
| reconciliation | `/reconciliation` | `accountReconciliation` |
| party | `/parties` | `party` |
| tenant | `/tenants` | `tenant` |
| reference-data | `/reference-data/*` | `referenceData` |
| internal-account | `/internal-accounts` | `internalAccount` |
| market-information | `/market-data` | `marketInformation` |
| forecasting | `/forecasting` | `forecasting` |
| control-plane | `/manifests` | `manifestHistory` |
| saga | `/starlark-config` | `sagaRegistry` |
| mapping | `/gateway-mappings` | `mapping` |
| mcp-server | `/mcp-config` | (static config) |
| (cross-service) | `/dashboard` | multiple |
| (audit-worker) | `/audit-log` | REST/events |

### Pain Points

1. **No data hooks**: Every page constructs `useQuery()`
   with inline query keys and client calls. Same pattern
   repeated ~30 times.
2. **Query keys scattered**: `query-keys.ts` exists but
   pages construct ad-hoc keys.
3. **Shared components are a grab-bag**: `AuditTrail`,
   `CELEditor`, `SagaTimeline`, and
   `CreateValuationFeatureDialog` live together despite
   serving different services.
4. **No visual testing**: Component changes require running
   the full app.
5. **No tenant customisation**: Every tenant sees the same
   UI.

## Proposed Architecture

### Feature Module Structure

```text
src/
├── api/                        # Keep as-is
├── components/
│   ├── ui/                     # shadcn/ui (keep as-is)
│   └── layout/                 # AppShell, Header, Sidebar
├── contexts/                   # Keep as-is
├── lib/                        # Keep as-is
│
├── features/                   # Service-aligned modules
│   ├── accounts/
│   │   ├── components/         # Account-specific UI
│   │   ├── hooks/              # useAccounts(), useAccount()
│   │   ├── pages/              # List + detail pages
│   │   └── index.ts
│   ├── payments/
│   ├── ledger/
│   ├── positions/
│   ├── reconciliation/
│   ├── parties/
│   ├── tenants/
│   ├── reference-data/
│   ├── internal-accounts/
│   ├── market-data/
│   ├── forecasting/
│   ├── sagas/                  # starlark-config → sagas
│   ├── manifests/
│   ├── mappings/
│   ├── audit/
│   ├── mcp-config/             # MCP server configuration
│   └── dashboard/              # Cross-service aggregation
│
├── shared/                     # Cross-cutting components
│   ├── data-table.tsx
│   ├── money-display.tsx
│   ├── direction-badge.tsx
│   ├── status-badge.tsx
│   ├── entity-link.tsx
│   ├── detail-skeleton.tsx
│   ├── breadcrumbs.tsx
│   ├── time-display.tsx
│   └── handler-reference.tsx
│
└── App.tsx                     # Route definitions
```

### Service-Aligned Data Hooks

Each feature module exports hooks that encapsulate React
Query + client calls:

```typescript
// features/accounts/hooks/use-accounts.ts
export function useAccounts(opts?: { enabled?: boolean }) {
  const clients = useClients()
  const tenantSlug = useTenantSlug()

  return useQuery({
    queryKey: tenantKeys.accounts(tenantSlug ?? ''),
    queryFn: () =>
      clients.currentAccount.listAccounts({}),
    enabled: !!tenantSlug && (opts?.enabled ?? true),
  })
}

export function useAccount(accountId: string) {
  const clients = useClients()
  const tenantSlug = useTenantSlug()

  return useQuery({
    queryKey: tenantKeys.account(tenantSlug ?? '', accountId),
    queryFn: () =>
      clients.currentAccount.getAccount({ accountId }),
    enabled: !!tenantSlug && !!accountId,
  })
}
```

Pages become thin - render logic only, no query
construction:

```typescript
// features/accounts/pages/accounts-page.tsx
export function AccountsPage() {
  const { data, isLoading } = useAccounts()
  if (isLoading) return <DetailSkeleton />
  return (
    <DataTable
      columns={accountColumns}
      data={data?.accounts ?? []}
    />
  )
}
```

### Component Relocation

**Move into feature modules** (used by single service):

| Component | From | To |
|-----------|------|-----|
| `cel-editor` | `shared/` | `features/sagas/` |
| `starlark-editor` | `shared/` | `features/sagas/` |
| `saga-timeline` | `shared/` | `features/sagas/` |
| `quality-ladder-badge` | `shared/` | `features/positions/` |
| `create-valuation-feature-dialog` | `shared/` | `features/reference-data/` |

**Keep in `shared/`** (used across 2+ features): DataTable,
MoneyDisplay, DirectionBadge, StatusBadge, EntityLink,
DetailSkeleton, Breadcrumbs, TimeDisplay, HandlerReference,
AuditTrail. Note: `audit-trail` is rendered on entity detail
pages across many services (accounts, payments, etc.), making
it a cross-cutting concern. The `features/audit/` module owns
the dedicated `/audit-log` page but the reusable component
stays in shared.

### Storybook

**Why Storybook matters for this project specifically:**

1. **Component catalogue**: When building tenant-customisable
   UI, you need a visual inventory of what's available.
   Storybook is that inventory.
2. **Theme testing**: Storybook's theme decorator lets you
   preview components under different tenant themes without
   running the full app.
3. **Feature flag testing**: Stories can render components
   with different feature toggle states to verify
   conditional rendering.
4. **Staff vs customer views**: Stories can show the same
   data component in "operations" vs "customer portal"
   contexts.
5. **Design review**: PRs that change components include
   Storybook previews (Chromatic or similar), so reviewers
   see visual impact without pulling the branch.

**Setup:**

- Storybook 8.x with `@storybook/react-vite` (matches
  build tool)
- MSW addon (already using MSW in tests) for realistic
  API data
- a11y addon for accessibility auditing
- Stories colocated with components:
  `data-table.stories.tsx` next to `data-table.tsx`

### Tenant UI Customisation Architecture

All customisation is **runtime configuration**, loaded when
the tenant context is established. The same compiled SPA
serves every tenant.

#### Layer 1: Theme (CSS variables, loaded at login)

Tenant branding applied by overriding CSS custom properties
on the root element. shadcn/ui already uses CSS variables
(`--primary`, `--background`, etc.), so the mechanism is
built-in.

```yaml
# In tenant config / manifest
ui:
  theme:
    primary_color: "#1e40af"
    logo_url: "/tenant-assets/acme/logo.svg"
    favicon_url: "/tenant-assets/acme/favicon.ico"
    font_family: "Inter"
```

**Runtime flow**: `TenantProvider` fetches tenant config,
extracts `ui.theme`, applies CSS variable overrides to
`document.documentElement`. Entire app re-themes without
reload. If config fetch fails or returns invalid data,
`TenantProvider` applies safe defaults (platform theme,
all features visible) and records an observability event
(`tenant_ui_config_fallback`).

**Asset security**: Tenant-supplied URLs (`logo_url`,
`favicon_url`) must not be loaded directly from arbitrary
origins. Requirements:

- Assets served through the gateway proxy
  (`/tenant-assets/:slug/`) or from an allowlisted CDN
  origin
- Server-side validation: content-type allowlist (SVG, PNG,
  ICO, WEBP), file size limit (e.g., 512 KB)
- CSP `img-src` directive restricted to `'self'` and the
  configured asset origin
- Tenant config validation rejects non-allowlisted URLs at
  write time

#### Layer 2: Feature Visibility (route + sidebar, loaded at login)

Tenant configuration controls which feature modules are
visible. Features not in the list don't render sidebar items
or accept route navigation.

```yaml
ui:
  features:
    enabled:
      - accounts
      - payments
      - positions
      - ledger
      - reconciliation
    # Omitted features are hidden
    # deny-by-default for customer portal
    # allow-by-default for staff portal
```

**Runtime flow**: `useTenantFeatures()` hook reads config,
`Sidebar` filters nav items, `App.tsx` route guards redirect
disabled features to 404. No rebuild needed.

**Security note**: Feature visibility is a **UX concern**,
not a security boundary. The compiled SPA ships all feature
code to every tenant. All authorization is enforced at the
gateway/service layer via RBAC (see
`shared/platform/auth/rbac.go`). Route guards prevent
accidental navigation to irrelevant features, not
unauthorized access. Backend services must deny access and
return appropriate errors regardless of UI visibility.

#### Layer 3: Layout Composition (dashboard + table config)

Dashboard widgets, column visibility, default filters - all
tenant preferences loaded as data:

```yaml
ui:
  layout:
    dashboard:
      widgets:
        - feature: accounts
          component: AccountSummaryCard
          position: 1
        - feature: payments
          component: RecentPayments
          position: 2
    table_defaults:
      accounts:
        visible_columns: [id, holder, balance, status]
        default_sort: created_at_desc
```

**Runtime flow**: `useTenantLayout()` hook provides config.
Dashboard reads widget list, `DataTable` reads column/sort
defaults. Tenants change layout, refresh, done.

**Component validation**: Widget component names are
validated against a registry of allowed components per
context (staff/customer). The registry maps string names to
lazy-loaded component imports:

```typescript
const STAFF_DASHBOARD_WIDGETS: Record<string, () => Promise<ComponentType>> = {
  AccountSummaryCard: () => import('@/features/accounts/...'),
  RecentPayments: () => import('@/features/payments/...'),
}
```

Validation occurs at two points:

- **Config write time** (manifest apply or tenant entity
  update): reject configurations referencing unknown
  component names with a descriptive error
- **Render time**: skip unresolvable components with a
  warning log, render remaining widgets normally

#### Layer 4: Customer Portal (same SPA, different shell)

A `CustomerShell` component (vs `AppShell`) that uses the
same feature components but with:

- Reduced navigation (only customer-relevant features)
- Read-only views (no operations actions)
- Different auth scopes (customer JWT vs staff JWT)
- Tenant theme applied by default

**Runtime flow**: Auth context determines the lens from the
token, renders `AppShell` or `CustomerShell`. Same feature
components, different wrapping. No separate deployment.

**Dependency on PRD-031 (IAM)**: The staff/customer lens
determination depends on the identity architecture defined
in PRD-031. Staff tokens come from the Identity service
(Employee Access via Dex OIDC), while customer tokens may
come from a separate Party Authentication flow. The lens
may be derived from the token issuer or audience rather
than a claim within a single JWT type. This is an open
dependency to resolve before implementing Layer 4.

The feature module structure makes this possible because
components are decoupled from the shell.
`features/accounts/components/AccountSummary` works in
both contexts.

### Service Coverage Map

A generated report mapping proto RPCs to UI elements:

```markdown
## current-account (CurrentAccountService)

| RPC | Hook | Page | Status |
|-----|------|------|--------|
| ListAccounts | useAccounts() | /accounts | Covered |
| GetAccount | useAccount(id) | /accounts/:id | Covered |
| OpenAccount | - | - | No UI |
| CloseAccount | - | - | No UI |
```

Generated by scanning proto definitions + feature hook
imports. Flags new RPCs without UI coverage so service
changes naturally prompt UI parity discussion.

## Implementation Phases

### Phase 1: Feature Module Scaffold

- Create `features/` directory structure
- Move pages from `pages/<name>/` into
  `features/<name>/pages/`
- Update imports in `App.tsx`
- Verify E2E tests pass

### Phase 2: Extract Data Hooks

- Create `features/<name>/hooks/` with service-aligned hooks
- Refactor pages to use hooks instead of inline `useQuery()`
- Consolidate query key usage
- Unit test each hook

### Phase 3: Component Relocation

- Move single-service components into feature modules
- Move cross-cutting components to `src/shared/`
- Update all imports
- Verify no broken references

### Phase 4: Storybook

- Install Storybook 8 with Vite builder
- Write stories for shared components
- Write stories for feature-specific components
- Add MSW integration for page-level stories
- CI job to build Storybook

### Phase 5: Tenant Theme Foundation

- CSS variable override system from tenant config
- Tenant logo/branding in AppShell
- Theme preview in Storybook

### Phase 6: Feature Visibility

- Feature toggle system reading from tenant config
- Sidebar filtering based on enabled features
- Route guards for disabled features

### Phase 7: Service Coverage Tooling

- Script to parse proto RPCs
- Script to scan feature hooks
- Generate coverage report
- CI check for uncovered RPCs

## Open Questions

1. **Route ownership**: Should each feature module define
   its own routes (`features/accounts/routes.tsx`), or keep
   `App.tsx` as the single routing file? Single file is
   simpler to reason about; distributed routes scale better
   as features grow.

2. **Barrel exports**: Should feature modules use `index.ts`
   barrels? Convenient for imports but can hurt
   tree-shaking. Recommendation: barrels for page exports
   (needed by App.tsx), direct imports for everything else.

3. **Storybook hosting**: Chromatic (paid, best
   integration), GitHub Pages (free, manual), or dev-only
   (`npm run storybook`)? Start with dev-only, evaluate
   Chromatic when tenant admins need a preview.

4. **Tenant UI config location**: The `ui:` block shown in
   the customisation section needs a home. Options:
   - **Manifest YAML** (alongside instruments, sagas) -
     consistent with config-as-code, versioned, auditable
   - **Tenant entity field** (`ui_config JSONB`) - simpler,
     no manifest apply cycle, tenants self-serve
   - **Hybrid** - structural config (features, layout) in
     manifest, cosmetic config (theme, logo) on tenant
     entity

5. **Customer portal timeline**: Is this near-term or
   future? The architecture supports it regardless (same
   SPA, different shell based on JWT lens), but it affects
   investment in Layer 4 now vs later.

6. **Tenant asset backend storage**: The gateway serves
   assets via `/tenant-assets/:slug/` (per security
   requirements above). The open question is backend
   storage: local filesystem (dev/demo), object storage
   (S3/GCS) for production? Is a CDN layer needed?

## Success Criteria

1. Every page lives inside `features/<service>/pages/`
2. Data fetching uses feature hooks, not inline `useQuery()`
3. Storybook builds and renders all shared + feature
   components
4. A tenant can apply custom branding (logo, primary colour)
   via configuration at runtime
5. All existing E2E tests pass
6. No visual regressions
