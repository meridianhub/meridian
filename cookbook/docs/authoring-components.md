# Authoring Components

This guide describes how to create and publish Meridian Cookbook UI components.

A component is a reusable frontend building block tied to a Meridian feature module.
Components are described by `registry:ui` entries conforming to the `registry-item.json` schema.

---

## Component Structure

Every component lives in its own directory under `cookbook/ui/<component-name>/` and contains:

```text
cookbook/ui/<component-name>/
└── component.json    # Registry metadata (required)
```

Unlike patterns, component entries do not bundle source files within the cookbook. The `files[]`
array in `component.json` lists paths relative to the repository root where the component's
source file(s) live.

---

## The `component.json` Schema

All fields come from `cookbook/schema/registry-item.json`. For `registry:ui` entries:

```json
{
    "$schema": "https://cookbook.meridianhub.org/schema/registry-item.json",
    "name": "my-component",
    "type": "registry:ui",
    "title": "My Component",
    "description": "One sentence: what this component renders and when you'd use it.",
    "categories": ["display", "ledger"],
    "files": [
        {
            "path": "frontend/src/features/my-feature/components/my-component.tsx",
            "type": "registry:ui"
        }
    ],
    "meta": {
        "feature_module": "my-feature",
        "tenant_configurable": false,
        "used_by": ["ledger", "reconciliation"]
    }
}
```

### Field Reference

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| `name` | string | yes | Kebab-case, matches directory name. Pattern: `^[a-z][a-z0-9-]*$` |
| `type` | string | yes | Always `"registry:ui"` |
| `title` | string | yes | Human-readable display name |
| `description` | string | yes | One or two sentences describing what it renders |
| `categories` | array | no | Taxonomy tags: `"display"`, `"editor"`, `"form"`, `"timeline"`, etc. |
| `files` | array | yes | Source file paths relative to the repository root |
| `meta.feature_module` | string | yes | The Meridian feature module this component belongs to |
| `meta.tenant_configurable` | boolean | no | Whether tenants can configure props. Defaults to false |
| `meta.configurable_props` | array | no | List of prop names tenants can configure (if `tenant_configurable: true`) |
| `meta.used_by` | array | no | Feature modules that consume this component |

**`registryDependencies`** is optional for UI entries. Use it when the component depends on another
registered component (e.g., a form dialog that embeds a `cel-editor`).

---

## Identifying Tenant-Configurable Props

A component is `tenant_configurable: true` when operators or administrators can meaningfully
change its appearance or behaviour from the Meridian control plane — without modifying source code.

Use `tenant_configurable: false` (the default) for components that:

- Display read-only data from a fixed data model (e.g., `balance-indicator`, `direction-badge`)
- Visualise saga or ledger state without presentational options (e.g., `saga-timeline`)
- Are developer tools embedded in the manifest editor (e.g., `starlark-editor`, `cel-editor`)

Set `tenant_configurable: true` when the component accepts configuration that varies by tenant,
such as column visibility toggles, labelling, or feature-flag-driven behaviour. List the
configurable prop names in `meta.configurable_props`.

---

## Mapping Components to Feature Modules

The `meta.feature_module` field ties a component to the Meridian service domain it belongs to.
Use the same module names that appear in the frontend's `src/features/` directory structure.

| Feature module | Domain |
|---------------|--------|
| `ledger` | Ledger postings, double-entry accounting display |
| `reconciliation` | Reconciliation cycles, variance reports |
| `sagas` | Saga execution, step timelines, script editors |
| `reference-data` | Account types, instruments, valuation features |
| `payments` | Payment flows, gateway instructions |
| `transactions` | Transaction history, position logs |
| `manifests` | Manifest authoring, YAML/CEL editors |
| `market-data` | Market observations, price curves |
| `internal-accounts` | Internal bank accounts, ledger structure |

A component belongs to the module where its primary data source lives, not necessarily where it
is rendered. `balance-indicator` belongs to `ledger` because it reads ledger postings, even though
it might appear on a reconciliation page.

The `meta.used_by` array lists all modules that import the component. A shared display primitive
like `direction-badge` may be `used_by: ["ledger", "transactions", "payments"]` while its
`feature_module` is `ledger` (where its data model originates).

---

## Declaring `registryDependencies` Between Components

Use `registryDependencies` when a component embeds another registered component as a composable
building block, and consumers installing this component should also install the dependency.

**When to declare a dependency:**

- The component renders another registered component inside it (composition)
- The dependency is a shared primitive that must be co-installed for the component to work

**When not to declare a dependency:**

- The relationship is import-level only and managed by the build system
- The dependency is a design system primitive not tracked in the cookbook

Example: a form dialog that embeds `cel-editor` could declare `"registryDependencies": ["cel-editor"]`
to signal that the cel-editor component must also be present.

All names in `registryDependencies` must exist in `registry.json`. Tests enforce this.

---

## Step-by-Step: Documenting a New Component

### 1. Create the directory

```bash
mkdir -p cookbook/ui/my-component
```

### 2. Write `component.json`

```json
{
    "$schema": "https://cookbook.meridianhub.org/schema/registry-item.json",
    "name": "my-component",
    "type": "registry:ui",
    "title": "My Component",
    "description": "Renders X for Y use case. Shows Z when the condition is met.",
    "categories": ["display"],
    "files": [
        {
            "path": "frontend/src/features/my-feature/components/my-component.tsx",
            "type": "registry:ui"
        }
    ],
    "meta": {
        "feature_module": "my-feature",
        "tenant_configurable": false,
        "used_by": ["my-feature"]
    }
}
```

### 3. Register in `registry.json`

Add an entry to `cookbook/registry.json`:

```json
{
    "name": "my-component",
    "type": "registry:ui"
}
```

### 4. Run the tests

```bash
cd cookbook && go test ./schema/...
```

Tests enforce: schema validation, `name` field matches directory name, `type` is `registry:ui`,
all `files[]` paths exist on disk relative to the repository root, and the component appears in
`registry.json`.

---

## Example: Shared Component (`saga-timeline`)

`saga-timeline` visualises saga execution state as a horizontal step timeline. It is used by
multiple feature modules — any page that shows transaction or payment status embeds it.

**Key decisions in `component.json`:**

```json
{
    "name": "saga-timeline",
    "type": "registry:ui",
    "title": "Saga Timeline",
    "description": "Visualises the lifecycle of a Meridian saga as a horizontal step timeline...",
    "categories": ["saga", "timeline", "display", "status"],
    "files": [
        {
            "path": "frontend/src/features/sagas/components/saga-timeline.tsx",
            "type": "registry:ui"
        }
    ],
    "meta": {
        "feature_module": "sagas",
        "tenant_configurable": false,
        "used_by": ["payments", "transactions", "reconciliation"]
    }
}
```

- `feature_module: "sagas"` — the component lives in the sagas feature domain even though it
  appears in payments and transactions pages
- `tenant_configurable: false` — saga state is a platform truth, not something operators configure
- `used_by` lists three consuming modules, reflecting the component's shared nature
- No `registryDependencies` — `saga-timeline` is a standalone display primitive

---

## Example: Feature-Specific Widget (`create-valuation-feature-dialog`)

`create-valuation-feature-dialog` is a modal dialog for attaching a valuation feature to an
account. It belongs to `reference-data` because that is where valuation feature management lives,
and it is used by `ledger` and `internal-accounts` pages that expose account configuration.

**Key decisions in `component.json`:**

```json
{
    "name": "create-valuation-feature-dialog",
    "type": "registry:ui",
    "title": "Create Valuation Feature Dialog",
    "description": "A modal dialog for attaching a valuation feature to a current or internal bank account...",
    "categories": ["valuation", "account", "dialog", "form"],
    "files": [
        {
            "path": "frontend/src/features/reference-data/components/create-valuation-feature-dialog.tsx",
            "type": "registry:ui"
        }
    ],
    "meta": {
        "feature_module": "reference-data",
        "tenant_configurable": false,
        "used_by": ["ledger", "internal-accounts"]
    }
}
```

- `categories` uses both the domain tag (`"valuation"`, `"account"`) and the component type
  (`"dialog"`, `"form"`) to aid discovery
- `feature_module: "reference-data"` reflects the RPC it calls (`CreateValuationFeature`)
- `tenant_configurable: false` — the dialog calls a fixed RPC, there is nothing for a tenant to configure

---

## Testing Requirements

Every component must pass the suite in `cookbook/schema/ui_components_test.go`.

**What the tests check:**

1. **Schema validation** — `component.json` conforms to `registry-item.json`. The `meta.feature_module`
   field is required for `registry:ui` entries.

2. **Name matches directory** — the `name` field must equal the directory name under `cookbook/ui/`.

3. **Type is `registry:ui`** — the `type` field must be exactly `"registry:ui"`.

4. **Files exist on disk** — every path in `files[]` must exist relative to the repository root.
   Paths must be relative (not absolute) and must not escape the repository root with `..` traversal.

5. **Present in `registry.json`** — `registry.json` must contain an entry with a matching `name`.

6. **`registryDependencies` are valid** — all names in `registryDependencies` must exist in `registry.json`.

Run with: `cd cookbook && go test ./schema/...`

To add a new component to the test suite, append its name to `uiComponentNames` in
`cookbook/schema/ui_components_test.go`.
