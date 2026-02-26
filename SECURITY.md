# Security Policy

## Reporting a Vulnerability

We take the security of Meridian seriously. If you discover a security vulnerability, please report it privately
through one of the following channels:

### GitHub Security Advisories (Recommended)

Report vulnerabilities using GitHub's private vulnerability reporting:

1. Navigate to the [Security Advisories](https://github.com/meridianhub/meridian/security/advisories) page
2. Click "Report a vulnerability"
3. Provide detailed information about the vulnerability

### What to Include

When reporting a vulnerability, please include:

- Description of the vulnerability
- Steps to reproduce the issue
- Potential impact and severity assessment
- Any suggested fixes or mitigations (if applicable)
- Your contact information for follow-up questions

## Response Timeline

- **Acknowledgment**: Within 48 hours of submission
- **Initial Assessment**: Within 5 business days
- **Status Updates**: Regular updates as investigation progresses
- **Resolution**: Timeline depends on severity and complexity

## Disclosure Policy

- Do not publicly disclose vulnerabilities until a fix has been released
- We will work with you to understand and address the issue
- Credit will be given to researchers who report vulnerabilities responsibly (if desired)

## Threat Model for Financial Systems

Meridian is a transaction integrity engine handling financial, energy, and infrastructure
assets. The threat model addresses risks specific to multi-tenant financial platforms.

### Transaction Tampering

Unauthorized modification of ledger entries, account balances, or transaction records.
Meridian mitigates this through:

- **Immutable audit trails**: All entity mutations (INSERT, UPDATE, DELETE) are captured
  via GORM hooks and written to per-service `audit_log` tables through a transactional
  outbox pattern (see [ADR-0009](docs/adr/0009-application-level-audit-logging.md)).
- **Dual-path audit delivery**: Primary path via Kafka, fallback to outbox table. Audit
  intent is committed atomically with the business transaction, so no mutation can occur
  without a corresponding audit record.
- **Saga orchestration**: Distributed transactions use saga state machines with automatic
  compensation. Each step is recorded, and partial failures trigger deterministic rollback.

### Multi-Tenant Data Leakage

Cross-tenant data access where one tenant reads or modifies another tenant's data. See
[Multi-Tenancy Security Boundaries](#multi-tenancy-security-boundaries) below for
detailed controls.

### Replay Attacks

Re-submission of previously valid requests to duplicate transactions or bypass
authorisation. Mitigations include:

- **Idempotency keys**: Critical operations (payment orders, ledger postings) require
  idempotency keys. Duplicate submissions return the original result without
  re-execution.
- **Audit outbox deduplication**: The outbox worker processes entries idempotently;
  retries do not create duplicate audit records.

### Authorisation Bypass

Accessing operations or data without proper permissions. Controls include:

- **JWT-based authentication**: Production deployments require valid JWT tokens with
  embedded tenant claims. The `auth.Interceptor` validates that the `x-tenant-id`
  header matches the JWT `tenant_id` claim.
- **gRPC interceptor chain**: The `TenantExtractionInterceptor` (development only) and
  `auth.Interceptor` (production) are mutually exclusive. Production must use
  `auth.Interceptor` which enforces JWT validation.
- **Starlark sandboxing**: Tenant-defined business logic runs in Starlark, which is
  intentionally not Turing-complete. No while loops, no recursion, guaranteed
  termination. This prevents tenants from executing arbitrary code.
- **CEL expression evaluation**: Validation rules use CEL (Common Expression Language),
  which is pure-function and read-only. CEL expressions cannot modify state.

### Temporal Attacks

Exploiting bi-temporal data (valid-time and transaction-time) to manipulate historical
records or create backdated entries. Mitigations:

- **Server-side timestamps**: `created_at` and `changed_at` fields use server-generated
  `now()` defaults, not client-supplied values.
- **Audit trail immutability**: Audit records are append-only. Historical entries cannot
  be modified or deleted through application code.

## Data Classification

| Classification | Examples | Handling |
|----------------|----------|----------|
| **Restricted** | DB credentials, API keys, JWT signing keys | Env vars only. Never in source control. Rotate regularly. |
| **Confidential** | PII, financial amounts, account balances | Encrypted at rest. Tenant-scoped access. Audited. |
| **Internal** | Tenant config, Starlark sagas, CEL rules | Authenticated operator access. Changes audited. |
| **Audit** | Audit logs, outbox records, Kafka events | Append-only. Tampering detection. Regulatory retention. |
| **Public** | Protobuf schemas, docs, open-source code | No special handling required. |

### Sensitive Data in Logs

- Tenant IDs are hashed before logging using SHA-256 truncated to 16 hex characters
  (`hashTenantID` in `shared/platform/db/gorm_tenant_scope.go`). This allows log
  correlation without exposing tenant identity.
- Application logs must not contain PII, financial amounts, or credentials. Use
  structured logging with parameterized fields.

## Multi-Tenancy Security Boundaries

Meridian uses schema-based multi-tenancy on CockroachDB. Each tenant's data is isolated
in a dedicated database schema.

### Schema Isolation

- **Schema naming convention**: Each tenant's data lives in a schema named
  `org_{tenant_id}` (see
  [ADR-0016](docs/adr/0016-tenant-id-naming-strategy.md)).
- **Tenant ID validation**: IDs are validated against `^[a-zA-Z0-9_]{1,50}$` at
  construction time (`shared/platform/tenant/tenant_id.go`). Invalid IDs are
  rejected before any database operation.
- **SQL injection prevention**: Schema names are quoted using `pq.QuoteIdentifier()`
  before inclusion in SQL statements (`shared/platform/db/gorm_tenant_scope.go`).

### Tenant Context Propagation

Tenant identity flows through the entire request lifecycle:

1. **API Gateway**: The `TenantResolverMiddleware` extracts tenant from the request
   subdomain and sets the `x-tenant-id` header.
2. **gRPC Metadata**: The `x-tenant-id` key (`shared/platform/tenant/keys.go`)
   propagates tenant identity across service boundaries via gRPC metadata.
3. **Context Injection**: `tenant.WithTenant(ctx, tenantID)` injects the tenant into
   Go context. All downstream operations extract the tenant from context.
4. **Database Scoping**: `WithGormTenantScope` executes
   `SET LOCAL search_path TO <schema>, public` within each transaction. `SET LOCAL`
   is transaction-scoped and automatically reverts on commit or rollback.
5. **Kafka Headers**: Audit events carry tenant identity in Kafka message headers for
   cross-service audit correlation.

### Security Controls

- **Fail-fast on missing tenant**: `RequireFromContext` returns
  `ErrMissingTenantContext` if tenant context is absent. `MustFromContext` panics,
  treating missing context as a programming error. No database operation can proceed
  without tenant context.
- **JWT-tenant validation**: In production (`AUTH_ENABLED=true`), `auth.Interceptor`
  validates that the `x-tenant-id` gRPC metadata matches the JWT's `tenant_id` claim.
  This prevents a caller from specifying a different tenant than their credentials
  authorise.
- **No cross-tenant database access**: Each service connects to its own database with
  its own credentials. The centralised audit worker pattern was explicitly rejected to
  maintain bounded context isolation (see
  [ADR-0020](docs/adr/0020-per-service-audit-workers.md)).

## Audit Trail Protection

### Append-Only Design

Audit records are designed to be append-only:

- Business operations write audit intent to an `audit_outbox` table atomically within
  the same database transaction. This guarantees that every committed business operation
  has a corresponding audit record.
- The audit worker moves entries from `audit_outbox` to `audit_log`. Processing is
  idempotent; retries do not create duplicates.
- Application code does not provide UPDATE or DELETE operations on `audit_log` tables.

### Dual-Path Delivery

The audit system uses two delivery paths for resilience:

1. **Primary (Kafka)**: Audit events are published to service-specific Kafka topics
   (e.g., `audit.events.current-account`). Dedicated audit consumer deployments write
   to `audit_log`.
2. **Fallback (Outbox)**: When Kafka is unavailable (timeout: 5 seconds), audit entries
   remain in the `audit_outbox` table. A centralised `audit-worker` service polls and
   processes these entries.

This dual-path design ensures no audit records are lost during infrastructure failures.

### Monitoring and Tampering Detection

Prometheus metrics for audit system health:

| Metric | Type | Purpose |
|--------|------|---------|
| `meridian_audit_kafka_events_published_total` | Counter | Primary path volume |
| `meridian_audit_kafka_fallback_used_total` | Counter | Detect Kafka failures |
| `meridian_audit_worker_outbox_depth` | Gauge | Audit processing lag |
| `meridian_audit_kafka_events_consumed_total` | Counter | Consumer throughput |

Alert if outbox depth exceeds 1,000 entries for more than 5 minutes, which may indicate
audit processing failures.

### Retention

Audit log retention policies should be configured based on regulatory requirements for
the deployment context (e.g., financial services may require 7+ years). Operators are
responsible for configuring retention and archival to immutable storage (e.g., S3
Glacier) for their specific compliance needs.

## Database Security

### Encryption

- **At rest**: CockroachDB supports encryption at rest for the storage layer. Enable
  this in production deployments.
- **In transit**: All database connections should use TLS. Configure CockroachDB with
  valid certificates and require TLS for client connections.
- **Backup encryption**: Database backups must be encrypted before storage. Use
  CockroachDB's built-in encrypted backup or encrypt at the storage layer.

### Connection Security

- Store database connection strings in environment variables or a secrets manager
  (e.g., Kubernetes Secrets, HashiCorp Vault). Never commit connection strings to
  source control.
- Use separate database credentials per service. Each microservice connects only to its
  own schema (enforced by
  [ADR-0020](docs/adr/0020-per-service-audit-workers.md) which rejected cross-service
  database access).
- Rotate database credentials regularly. Use short-lived credentials where possible.

### Migration Security

- Database migrations are managed through versioned SQL files. Each migration is
  immutable once applied.
- Migration files must not contain secrets (credentials, API keys, encryption keys).
  Use parameterized values or environment variable references for sensitive
  configuration.
- Review all migration files for SQL injection vectors before applying to production.

## Security Best Practices

When contributing to or deploying Meridian:

- Keep dependencies up to date
- Follow secure coding practices outlined in our contribution guidelines
- Use environment variables for sensitive configuration
- Enable authentication and authorisation in production deployments
  (`AUTH_ENABLED=true`)
- Regular security audits of your deployment configuration
- Use `pq.QuoteIdentifier()` for all dynamic SQL identifiers
- All database access must use GORM through tenant-scoped transactions; avoid raw SQL
  queries that bypass audit hooks
- Validate tenant IDs at system boundaries using `tenant.NewTenantID()`

## Scope

This security policy applies to:

- The core Meridian platform and all services
- Official container images and deployment configurations
- Documentation that affects security posture

## Out of Scope

The following are generally not considered security vulnerabilities:

- Issues requiring unauthorized physical access to infrastructure
- Social engineering attacks against users
- Vulnerabilities in third-party dependencies (report to upstream projects)
- Issues already publicly disclosed or known

## Questions

For questions about this security policy, open a GitHub Discussion or contact the
project maintainers.
