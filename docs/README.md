# Meridian Documentation

The documentation index for Meridian, the source-available transaction integrity
engine for the real-world economy. Start with the [project README](../README.md)
for a product overview, then use this index to navigate the technical
documentation.

## Start Here

- [Architecture Layers](architecture-layers.md) - 8-layer functional grouping with service-to-layer mapping
- [Cross-Service Patterns](patterns.md) - canonical locations for the 6 recurring implementation patterns
- [Data Flows](data-flows.md) - sequence diagrams for payment, audit, tenant provisioning, and manifest apply
- [Service README Template](service-readme-template.md) - required structure for per-service READMEs

## Architecture and Design

- [BIAN Service Boundaries](architecture/bian-service-boundaries.md)
- [BIAN Service Boundary Migration Plan](architecture/boundary-migration-plan.md)
- [Data Model Reference](architecture/data-model.md)
- [Event-Driven Architecture](architecture/event-driven-architecture.md)
- [Service Coupling Analysis](architecture/service-coupling-analysis.md)
- [Starlark Saga Architecture](architecture/starlark-saga-architecture.md)
- [Architecture Decision Records (ADRs)](adr/README.md) - the full decision log
- [Service Coupling Visualization Example](coupling-visualization-example.md)

### Behavioral API Contracts

- [CurrentAccount Contract](architecture/api-contracts/current-account-contract.md)
- [FinancialAccounting Contract](architecture/api-contracts/financial-accounting-contract.md)
- [PositionKeeping Contract](architecture/api-contracts/position-keeping-contract.md)

### Sagas

- [The Saga Contract Specification](spec/saga-contract.md)
- [Saga Handler Loading](saga-handler-loading.md)
- [Saga Service Catalog](saga-service-catalog.md)

## Developer Guides

- [Developer Guides Index](guides/README.md) - conventions, value types, Starlark, proto, and testing guides
- [Adding Audit to a New Service](development/audit-adding-new-service.md)
- [Claude Code Skills Integration](claude-code-skills.md)
- [Local Go Documentation Server](local-documentation.md)
- [Skills Documentation](../.claude/skills/README.md) - task-specific runbook-style skills for AI contributors

## Operations

- [Runbooks Index](runbooks/README.md) - incident response, disaster recovery, deployment, and service operations
- [Audit System Monitoring](operations/audit-monitoring.md)
- [Secrets Management](secrets-management.md)
- [Tilt Fast Startup Mode](tilt-fast-startup.md)

## Integrations

- [Mapping Layer](mapping-layer/README.md) - configuration-driven JSON transformation engine
- [KYC/AML Verification Provider Integration](integrations/kyc-aml-providers.md)
- [KYC/AML Verification Developer Guide](services/party/verification-guide.md)

## API Reference

- [Saga Validation REST API](api/saga-validation.md)

## Product Requirements

- [PRD Index](prd/README.md) - the full catalogue of product requirement documents

## Reports, Audits, and Analysis

- [Saga Handler Audit Report](reports/saga-handler-audit.md)
- [CockroachDB Migration Audit](reports/cockroachdb-migration-audit.md)
- [Market Information Go-Live Readiness](reports/market-information-go-live-readiness.md)
- [Market Information Cross-Service Integration](reports/market-information-integration.md)
- [Market Information Requirements Traceability](reports/market-information-traceability.md)
- [Frontend vs Backend RPC Audit](audit/frontend-backend-gaps.md)
- [Multi-Asset Purity Audit](audit/multi-asset-purity.md)
- [Tenant Isolation Audit (2026-04-04)](audits/tenant-isolation-audit-2026-04-04.md)
- [pgx Tenant Guard Audit](security/pgx-tenant-audit.md)
- [cmd/ Package Test Coverage Assessment](analysis/cmd-coverage-assessment.md)

## Testing

- [Chaos Testing Strategy](testing/CHAOS_TESTING.md)
- [Test Coverage Analysis](testing/COVERAGE_ANALYSIS.md)

## Demos

- [Multi-Organization Settlement Demo](demos/multi-organization-settlement.md)

## Archive

Historical documentation, preserved for reference. See the
[Documentation Archive](archive/README.md) for the full list and archival policy.
