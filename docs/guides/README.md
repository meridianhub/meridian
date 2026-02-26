# Developer Guides

This directory contains how-to guides and usage documentation for Meridian developers.

## Available Guides

| Guide | Description |
|-------|-------------|
| [Calling Meridian APIs](calling-meridian-apis.md) | HTTP/JSON, Connect, and gRPC — authentication, tenant isolation, quick start |
| [Multi-Asset Integration](multi-asset-integration.md) | End-to-end guide for tracking non-fiat instruments (energy, carbon, compute) |
| [New BIAN Service Checklist](new-bian-service-checklist.md) | Complete checklist for creating a new BIAN service domain |
| [Circuit Breaker Usage](circuit-breaker-usage.md) | Using circuit breakers for resilient inter-service calls |
| [Testcontainers Usage](testcontainers-usage.md) | PostgreSQL testcontainers for integration testing |
| [Financial Accounting Proto](financial-accounting-proto.md) | Protocol buffer schemas for Financial Accounting service |
| [Coupling Analysis](coupling-analysis.md) | Analyzing service coupling using dependency graphs |
| [Saga Validation](saga-validation.md) | Automatic validation workflow, error interpretation, and troubleshooting |
| [Starlark Style Guide](starlark-style-guide.md) | Writing conventions and syntax for Starlark saga scripts |
| [Starlark Built-ins Reference](starlark-built-ins-reference.md) | Available functions, types, and DSL built-ins |

## Documentation Organisation

All substantial documentation belongs in `docs/`, organised by type:

```text
docs/
├── adr/            # Architecture Decision Records
├── architecture/   # Architecture diagrams and designs
├── guides/         # Usage guides and how-tos (this directory)
├── runbooks/       # Operational procedures
├── skills/         # Claude Code skills documentation
└── testing/        # Testing strategies and coverage
```

## Contributing

When adding new documentation:

1. **Guides** - Step-by-step how-to documents → `docs/guides/`
2. **Architecture decisions** - ADRs for significant choices → `docs/adr/`
3. **Operational procedures** - Runbooks for incidents/recovery → `docs/runbooks/`
4. **Package documentation** - Brief README.md at package root (Go convention)

Avoid creating documentation files scattered throughout service directories.
If a document is more than a brief package description, it belongs in `docs/`.

## See Also

- [ADR-015: Standard Project and Service Directory Structure](../adr/0015-standard-service-directory-structure.md)
- [API Proto Documentation](../../api/proto/README.md)
