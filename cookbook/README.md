# Meridian Cookbook

The cookbook is a registry of reference implementations for Meridian manifest patterns and UI
components. Each pattern is a self-contained bundle of manifest primitives - instruments, account
types, valuation rules, and Starlark sagas - that solves a specific business problem. Patterns are
authored templates, not installed software: copying a pattern into your tenant configuration is the
deployment step. The registry exists so AI assistants, the control plane UI, and human authors can
discover available patterns, understand their dependencies, and compose them safely.

## When to Use the Cookbook

Use a pattern when starting a tenant configuration for a known domain and you want a vetted starting
point rather than writing primitives from scratch. Energy billing tenants start from `energy-settlement`
or `payg-energy`. Payment collection starts from `payment-gateway-stripe` or `default-stripe-payment`.
Use the cookbook to understand how Meridian primitives compose - reading `saas-billing` shows how
webhook triggers, event-driven sagas, and scheduled billing fit together in a single economy. Use
patterns as debugging references: if a valuation rule or CEL filter behaves unexpectedly, compare
your configuration against the canonical example.

## Structure

| Directory | Contents |
|-----------|---------|
| `patterns/` | Domain pattern bundles: `manifest-fragment.yaml`, Starlark `.star` saga files, and `pattern.json` metadata |
| `schema/` | JSON Schema definitions for registry validation (`registry.json`, `registry-item.json`) |
| `docs/` | Authoring guides for patterns ([`authoring-patterns.md`](docs/authoring-patterns.md)) and UI components ([`authoring-components.md`](docs/authoring-components.md)) |
| `ui/` | UI component registry entries: `component.json` metadata pointing to frontend source files |

## Relationship to Reference Data

Cookbook patterns are templates, not runtime configuration. Copying a pattern's
`manifest-fragment.yaml` into your tenant configuration and applying it via the control plane is
what activates the instruments, account types, and sagas it declares. Nothing in the cookbook
directory is loaded automatically. Canonical saga defaults that ship with the platform live in
`services/reference-data/saga/defaults/` - these are the built-in behaviours Meridian applies
before any tenant customisation. The cookbook contains composable extensions you layer on top.
