---
name: adr-016-tenant-id-naming-strategy
description: Keep user-defined tenant IDs for schema naming; document namespace limitations as acceptable trade-off
triggers:
  - Designing tenant provisioning workflows
  - Evaluating namespace exhaustion concerns
  - Considering UUID vs human-readable identifiers for multi-tenancy
  - Deprovisioning tenants and ID reuse questions
instructions: |
  Use user-defined alphanumeric tenant IDs (pattern: ^[a-zA-Z0-9_]{1,50}$) that map directly
  to PostgreSQL schema names (org_{tenant_id}). Accept namespace exhaustion as a documented
  limitation acceptable for infrastructure multi-tenancy. Revisit if scale exceeds 1,000
  tenants or commercial SaaS model is adopted.
---

# 16. Tenant ID Naming Strategy

Date: 2025-12-13

## Status

Accepted

## Context

The Tenant Service uses **user-defined alphanumeric IDs** (e.g., `acme_bank`) as both the
public API identifier and the PostgreSQL schema name (`org_acme_bank`). This creates a
potential **namespace exhaustion problem**: deprovisioned tenant IDs cannot be reused
because the schema namespace is finite and human-readable IDs are desirable for new tenants.

### Current Implementation

| Aspect | Implementation |
|--------|---------------|
| **ID Validation** | `^[a-zA-Z0-9_]{1,50}$` |
| **Schema Naming** | `org_{lowercase(tenant_id)}` |
| **Public Exposure** | API paths, JWT claims (`x-tenant-id`), subdomains |
| **Status Lifecycle** | ACTIVE → SUSPENDED → DEPROVISIONED (terminal) |
| **ID Reuse** | Not supported (deprovisioned IDs are consumed forever) |

### The Problem

Once a tenant is deprovisioned, its ID (e.g., `acme_bank`) is permanently consumed:

1. A new organisation cannot claim this desirable name
2. Namespace pollution accumulates over time
3. Schema names remain visible in logs, connection strings, and error messages

### Relevant Context

- **Deployment Model**: Meridian is infrastructure, not commercial SaaS. Organizations
  own and operate their own instances with data sovereignty requirements.
- **Primary Use Case**: Demonstration infrastructure where multiple tenants share a
  cluster for cost efficiency (Post Office, Motive, UN WFP scenarios).
- **Expected Scale**: Tens to low hundreds of tenants per deployment, not thousands.
- **Debuggability Priority**: Operators rely heavily on human-readable schema names for
  troubleshooting (`org_post_office` vs `org_550e8400_e29b_41d4`).

## Decision Drivers

* **Operational debuggability**: Schema names visible in logs, query plans, error messages
* **Implementation simplicity**: Avoid migration complexity for current deployments
* **Namespace sustainability**: Long-term ID pool viability
* **Privacy**: Tenant identity exposure in technical artifacts
* **API consistency**: Alignment with existing Party Service patterns
* **Migration cost**: Effort to rename schemas, update JWT claims, modify routing

## Considered Options

### Option 1: Keep Current User-Defined Approach

Maintain status quo with documented limitations.

**Implementation**: No changes to existing codebase.

### Option 2: System-Generated UUIDs for Schema Naming

Use UUIDv7 (time-ordered) internally for schemas while keeping `display_name` for human
readability.

**Implementation Sketch**:

```protobuf
message Tenant {
  string tenant_id = 1;       // Internal UUID: "550e8400-e29b-41d4-a716-446655440000"
  string display_name = 2;    // Human-friendly: "Acme Bank"
  string slug = 3;            // URL-safe: "acme-bank" (optional for subdomains)
}
```

**Schema naming**: `org_550e8400_e29b_41d4` (normalised UUID prefix)

### Option 3: Hybrid Approach (Internal UUID + External Slug)

Separate internal identifier (UUID for schemas) from external identifier (slug for API/JWT).

**Implementation Sketch**:

```protobuf
message Tenant {
  string internal_id = 1;     // Internal UUID (not exposed in API responses)
  string slug = 2;            // External identifier for API, JWT (e.g., "acme_bank")
  string display_name = 3;    // Human-readable name
}
```

**Schema naming**: `org_{uuid}` (internal)
**API surface**: `/v1/tenants/{slug}`, JWT claim `x-tenant-id=acme_bank`

## Decision Outcome

Chosen option: **Option 1 (Keep Current User-Defined Approach)**, because the namespace
exhaustion problem is theoretical for the current deployment model and expected scale,
while the debugging and operational benefits of human-readable schema names are immediate
and significant.

### Rationale

1. **Scale Reality**: Demonstration infrastructure with tens of tenants will not exhaust
   the namespace for years. At 100 tenants/year with 10% churn, reaching 10,000 consumed
   IDs takes 100+ years.

2. **Debugging Value**: Schema names like `org_post_office` in query plans, connection
   strings, and error logs provide immediate context. UUID-based names require constant
   lookup to correlate with tenant identity.

3. **Migration Cost**: Options 2 and 3 require:
   - Schema renames for existing tenants
   - JWT claim format changes
   - Middleware updates for slug → UUID resolution
   - API breaking changes or dual-identifier periods
   - Test suite updates across all services

4. **SaaS Model Not Current**: Meridian is infrastructure for organisations to operate,
   not a commercial SaaS platform. Multi-tenancy is for demonstration, not production
   customer isolation with billing and churn.

5. **Industry Precedent**: Stripe uses prefixed human-readable IDs (`cus_`, `pi_`) rather
   than pure UUIDs because debuggability outweighs namespace concerns at their scale.
   Auth0 recommends UUIDs for portability but acknowledges the debugging trade-off.

### Documented Limitations

The following limitations are explicitly accepted:

| Limitation | Mitigation |
|------------|------------|
| **Namespace exhaustion** | Monitor deprovisioned count; revisit if approaching 5,000 |
| **ID reuse impossible** | Document that names are consumed permanently |
| **Schema name privacy** | Accepted for infrastructure (not end-user-facing SaaS) |
| **Tenant renames** | Not supported (display_name can change, ID cannot) |

### Reconsidering This Decision

Revisit Option 2 or 3 if:

- Tenant count exceeds 1,000 active tenants per deployment
- Commercial SaaS model is adopted with high customer churn
- Privacy requirements emerge (GDPR concern about schema name exposure)
- Cross-deployment tenant portability becomes a requirement

## Pros and Cons of the Options

### Option 1: Keep Current User-Defined Approach

**Description**: Maintain existing `^[a-zA-Z0-9_]{1,50}$` tenant IDs that map directly
to PostgreSQL schema names (`org_{tenant_id}`).

* Good, because zero implementation effort required
* Good, because schema names are immediately debuggable (`org_post_office` is self-explanatory)
* Good, because consistent with existing JWT claims, API paths, subdomain routing
* Good, because aligns with Party Service pattern (party_id is also user-facing)
* Bad, because deprovisioned IDs cannot be reused (namespace exhaustion)
* Bad, because schema names are visible in logs/errors (privacy trade-off)
* Bad, because tenant renames require schema rename (complex, risky)

### Option 2: System-Generated UUIDs for Schema Naming

**Description**: Generate UUIDv7 internally for schema isolation while keeping
`display_name` for human readability.

* Good, because unlimited namespace (UUIDs never collide)
* Good, because privacy improved (schema names are opaque)
* Good, because enables future ID recycling (deprovisioned schemas can be dropped)
* Bad, because breaking change requiring migration of existing tenants
* Bad, because debugging complexity (correlating `org_550e8400` to "Post Office" requires lookup)
* Bad, because JWT claims lose human-readability
* Bad, because inconsistency with Party Service (party_id is user-facing, not UUID-based)

### Option 3: Hybrid Approach (Internal UUID + External Slug)

**Description**: Separate internal identifier (UUID for schemas) from external identifier
(slug for API/JWT).

* Good, because best of both worlds (slugs for APIs, UUIDs for isolation)
* Good, because namespace reuse possible (slugs reclaimed after grace period)
* Good, because API backward compatibility (slugs remain stable)
* Good, because privacy improved (internal schema names opaque)
* Bad, because highest complexity (dual-identifier system requires careful indexing)
* Bad, because slug conflicts possible (must enforce uniqueness + grace periods)
* Bad, because migration challenge (existing tenant_id serves both roles)
* Bad, because schema routing overhead (middleware must resolve slug → UUID)
* Bad, because most implementation effort and risk

## Industry Research

### Stripe's Approach

Stripe uses **prefixed human-readable IDs** (e.g., `cus_xyz123`, `pi_abc456`):

- Type prefix makes IDs self-documenting for debugging
- Random suffix provides uniqueness without full UUID length
- Stripe stores the full prefixed ID as primary key (not separated)

This pattern prioritises debuggability over namespace concerns, even at Stripe's scale.

### Auth0's Approach

Auth0 recommends **UUIDs for portability**:

- If tenants migrate between Auth0 accounts, UUID-based associations don't break
- User IDs are affected by IdP configuration, so separate UUIDs are more stable

However, Auth0 acknowledges this adds debugging complexity.

### AWS Multi-Tenant Guidance

AWS emphasizes **tenant isolation over ID strategy**:

- Focus on access control and policy enforcement
- ID format is secondary to isolation boundaries
- Recommends identity providers (Cognito, Auth0) for tenant management

### PostgreSQL Considerations

- **Schema name limit**: 63 bytes (NAMEDATALEN - 1)
- **Performance**: No significant difference between short names and UUID-based names
- **UUIDv7**: PostgreSQL 18 introduces native support with 33% better performance than v4
- **Identifier case**: PostgreSQL folds unquoted identifiers to lowercase

## Implementation Notes

### If Option 2 or 3 Were Chosen (Future Reference)

**Migration Steps** (for future reference if revisiting this decision):

1. Add new `internal_id` (UUID) column to `platform.tenants` table
2. Populate with UUIDv7 for existing tenants
3. Create new schemas with UUID-based names (`org_{uuid_prefix}`)
4. Migrate data from old schemas to new schemas
5. Update middleware to resolve slug → UUID
6. Update JWT claim format (or add dual-claim period)
7. Deprecate old schema names after grace period
8. Update test suites across all services

**Estimated Effort**: 3-5 story points per service, plus 8-13 points for platform changes.
Total: 30-50 story points with significant risk.

### Monitoring Recommendations

Track the following to detect when reconsideration is needed:

```sql
-- Namespace consumption query
SELECT
  COUNT(*) FILTER (WHERE status = 'active') AS active_tenants,
  COUNT(*) FILTER (WHERE status = 'deprovisioned') AS consumed_ids,
  COUNT(*) AS total_consumed_namespace
FROM platform.tenants;
```

Alert if `consumed_ids` exceeds 1,000 or `consumed_ids / active_tenants` exceeds 5:1.

## Links

* [Stripe Object IDs Design](https://dev.to/4thzoa/designing-apis-for-humans-object-ids-3o5a) - Prefixed ID best practices
* [Auth0 Multi-Tenant Best Practices](https://auth0.com/docs/get-started/auth0-overview/create-tenants/multi-tenant-apps-best-practices)
* [AWS Multi-Tenant Authorisation](https://docs.aws.amazon.com/prescriptive-guidance/latest/saas-multitenant-api-access-authorization/introduction.html)
* [PostgreSQL UUID Documentation](https://www.postgresql.org/docs/current/datatype-uuid.html)
* GitHub Issue: Multi-tenancy namespace strategy evaluation (Task 51)

## Notes

This ADR explicitly documents the trade-off between namespace sustainability and
operational debuggability. The decision favors the latter based on:

1. Current deployment model (infrastructure, not SaaS)
2. Expected scale (tens of tenants, not thousands)
3. Operational priority (debugging ease over theoretical namespace concerns)
4. Migration cost (high effort for uncertain benefit)

The decision should be reconsidered if the deployment model shifts toward commercial
SaaS with high tenant churn, or if regulatory requirements emerge around tenant
identifier privacy.
