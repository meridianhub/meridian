# Meridian Services Architecture

This document describes the runtime architecture of Meridian services, including all communication
protocols, infrastructure dependencies, and data flows.

## System Architecture

The diagram below shows how services communicate at runtime across all protocols.

```mermaid
flowchart LR
    subgraph External["External Systems"]
        User["User/Client"]
        PayGW["Payment Gateway"]
        AuthProvider["Auth Provider<br/>(JWKS)"]
    end

    subgraph Admin["Admin Tools"]
        TenantCtl["tenantctl<br/>(CLI)"]
    end

    subgraph Platform["Meridian Platform"]
        subgraph Edge["Edge Layer"]
            GW["Gateway<br/>:8080"]
        end

        subgraph Services["Domain Services"]
            CA["CurrentAccount<br/>:50051"]
            PK["PositionKeeping<br/>:50053"]
            FA["FinancialAccounting<br/>:50052"]
            Party["Party<br/>:50055"]
            PO["PaymentOrder<br/>:50054, :8080"]
            RD["ReferenceData<br/>:50051"]
            MI["MarketInformation<br/>:50058"]
            IBA["InternalAccount<br/>:50057"]
            Recon["Reconciliation<br/>:50060"]
            FC["Forecasting<br/>:50061"]
        end

        subgraph Infrastructure["Infrastructure"]
            Tenant["Tenant<br/>:50056"]
            CP["ControlPlane"]
            AW["audit-worker<br/>:8080"]
            UMC["event-router<br/>:8080"]
            DB[("CockroachDB<br/>:26257")]
            Kafka["Kafka<br/>:9092"]
            Redis["Redis<br/>:6379"]
        end
    end

    %% External connections via Gateway
    User -->|"HTTP/REST"| GW
    AuthProvider -.->|"JWKS fetch"| GW
    GW -->|"Proxy (gRPC)"| CA
    GW -->|"Proxy (gRPC)"| PO
    GW -->|"Proxy (gRPC)"| Party
    GW -->|"Proxy (gRPC)"| MI
    GW -->|"Proxy (gRPC)"| IBA
    GW -->|"Proxy (gRPC)"| Recon
    PayGW -->|"HTTP Webhook"| PO

    %% Admin tool connections
    TenantCtl -->|"gRPC"| Tenant

    %% gRPC inter-service calls
    CA -->|"RetrieveParty (gRPC)"| Party
    CA -->|"InitiateFinancialPositionLog (gRPC)"| PK
    CA -->|"CaptureLedgerPosting (gRPC)"| FA
    PO -->|"InitiateLien (gRPC)"| CA
    Tenant -.->|"RegisterParty (gRPC, optional)"| Party
    Tenant -->|"Seed instruments"| RD
    PK -.->|"GetInstrument (gRPC)"| RD
    IBA -->|"GetBalance (gRPC)"| PK
    Recon -->|"Query positions (gRPC)"| PK
    Recon -->|"Query ledger (gRPC)"| FA
    Recon -->|"Query accounts (gRPC)"| CA
    FC -->|"GetMarketData (gRPC)"| MI

    %% Kafka event streaming
    PK -->|"Publish Events"| Kafka
    Kafka -->|"Consume Events"| FA

    %% Utilization metering (billing)
    Kafka -->|"Audit Events"| UMC
    UMC -->|"RecordMeasurement (gRPC)"| PK

    %% Database connections
    CA -->|"SQL"| DB
    PK -->|"SQL"| DB
    FA -->|"SQL"| DB
    Party -->|"SQL"| DB
    PO -->|"SQL"| DB
    Tenant -->|"SQL"| DB
    RD -->|"SQL"| DB
    MI -->|"SQL"| DB
    IBA -->|"SQL"| DB
    Recon -->|"SQL"| DB
    FC -->|"SQL"| DB
    CP -->|"SQL"| DB
    GW -->|"SQL (tenant lookup)"| DB

    %% Redis (optional caching/idempotency)
    PK -.->|"Idempotency"| Redis
    FA -.->|"Idempotency"| Redis
    PO -.->|"Idempotency"| Redis
    GW -.->|"Tenant cache"| Redis
    RD -.->|"Instrument cache"| Redis

    %% audit-worker connections
    AW -->|"Poll outbox"| DB
    AW -->|"Write audit log"| DB

    classDef service fill:#4a90d9,stroke:#2d5a87,color:#fff
    classDef edge fill:#e91e63,stroke:#880e4f,color:#fff
    classDef storage fill:#50c878,stroke:#2d7a4a,color:#fff
    classDef external fill:#ff9800,stroke:#e65100,color:#fff
    classDef admin fill:#9c27b0,stroke:#6a1b9a,color:#fff

    class CA,PK,FA,Party,PO,RD,MI,IBA,Recon,FC service
    class GW edge
    class Tenant,CP,AW,UMC,DB,Kafka,Redis storage
    class User,PayGW,AuthProvider external
    class TenantCtl admin
```

**Legend:**

- Solid arrows (`-->`) = Required runtime dependency
- Dashed arrows (`-.->`) = Optional runtime dependency
- Pink boxes = Edge layer (API gateway)
- Blue boxes = Domain services (BIAN service domains)
- Green boxes = Infrastructure (platform services, databases, messaging)
- Purple boxes = Admin tools (CLI)
- Orange boxes = External systems

## Communication Protocols

### gRPC (Synchronous)

All inter-service communication uses gRPC with Protocol Buffers:

| Source | Target | Method | Purpose |
|--------|--------|--------|---------|
| Gateway | Backend Services | HTTP Proxy | Route authenticated requests to gRPC services |
| CurrentAccount | Party | `RetrieveParty()` | Verify party exists and is active |
| CurrentAccount | PositionKeeping | `InitiateFinancialPositionLog()` | Create position log for account |
| CurrentAccount | FinancialAccounting | `CaptureLedgerPosting()` | Record double-entry posting |
| PaymentOrder | CurrentAccount | `InitiateLien()` | Reserve funds for payment |
| Tenant | Party | `RegisterParty()` | Register org party (optional) |
| Tenant | ReferenceData | SQL seed | Seed system instruments during provisioning |
| PositionKeeping | ReferenceData | `GetInstrument()` | Retrieve instrument definitions |
| InternalAccount | PositionKeeping | `GetBalance()` | Query balance for internal accounts |
| Reconciliation | PositionKeeping | Query positions | Compare position data across services |
| Reconciliation | FinancialAccounting | Query ledger | Compare ledger entries across services |
| Reconciliation | CurrentAccount | Query accounts | Compare account state across services |
| Forecasting | MarketInformation | `GetMarketData()` | Retrieve market data for forecast models |
| UtilizationMeteringConsumer | PositionKeeping | `RecordMeasurement()` | Record billing measurements to tenant-zero |

**Note:** CurrentAccount uses a `ValidateParty()` client wrapper that calls `RetrieveParty()` and
validates the party status is ACTIVE.

**Configuration:**

- Default timeout: 30 seconds
- Service discovery: Kubernetes DNS (`service.namespace.svc.cluster.local`)
- Load balancing: Round-robin across pod IPs

### Kafka (Asynchronous Events)

Event-driven communication for eventual consistency:

| Publisher | Topic Pattern | Consumer | Purpose |
|-----------|---------------|----------|---------|
| PositionKeeping | `position-keeping.transaction-*.v1` | FinancialAccounting | Trigger ledger postings |
| All Services | `*.audit.events` | UtilizationMeteringConsumer | Platform billing via tenant-zero |

**Event Types:**

- `transaction-captured` - New transaction recorded
- `transaction-amended` - Transaction modified
- `transaction-reconciled` - Transaction reconciled
- `transaction-posted` - Transaction posted to ledger
- `transaction-rejected` - Transaction rejected
- `transaction-failed` - Transaction processing failed
- `transaction-cancelled` - Transaction cancelled
- `bulk-transaction-captured` - Batch transactions recorded

**Configuration:**

- Default broker: `kafka:9092`
- Serialization: Protocol Buffers
- Partition key: `AggregateID` (ensures ordering per entity)

### HTTP (External Webhooks)

External payment gateway integration:

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/webhook/payment-gateway` | POST | Receive payment status updates |
| `/health` | GET | Health check endpoint |

**Security:**

- HMAC-SHA256 signature validation
- Timestamp validation (5-minute max age)
- Rate limiting: 100 req/sec per IP

## Infrastructure Dependencies

### CockroachDB (Primary Database)

All services persist data to CockroachDB using PostgreSQL wire protocol:

- **Connection:** `postgres://user:pass@cockroachdb:26257/meridian`
- **Multi-tenancy:** Schema-per-tenant isolation (`org_{tenant_id}`)
- **Migrations:** Atlas-managed schema migrations

### Kafka (Event Streaming)

Apache Kafka provides event streaming for asynchronous workflows:

- **Cluster:** 3-broker KRaft cluster (no ZooKeeper)
- **Topics:** Auto-created with `position-keeping.*` pattern
- **Retention:** Configurable per topic

### Redis (Optional Idempotency)

Redis provides optional distributed idempotency for exactly-once semantics:

- **Use case:** Idempotency key storage for duplicate request detection
- **Services:** PositionKeeping, FinancialAccounting, PaymentOrder
- **Configuration:** Disabled by default (`REDIS_ENABLED=false`)
- **Fallback:** Services degrade gracefully when Redis unavailable

**When to enable Redis idempotency:**

| Scenario | Recommendation |
|----------|----------------|
| Single replica deployment | Not needed (in-memory sufficient) |
| Multi-replica with load balancer | Recommended (distributed state) |
| High retry/duplicate risk | Recommended (payment workflows) |
| Development/testing | Not needed (simpler setup) |

**Trade-offs:**

- **With Redis:** Stronger exactly-once guarantees across replicas, additional infrastructure dependency
- **Without Redis:** Simpler deployment, per-instance idempotency only (request retries may hit different pods)

## Service Ports

| Service | gRPC Port | HTTP Port | Metrics Port |
|---------|-----------|-----------|--------------|
| Gateway | - | 8080 | 8080 |
| CurrentAccount | 50051 | - | 9090 |
| FinancialAccounting | 50052 | - | 9090 |
| PositionKeeping | 50053 | - | 9090 |
| PaymentOrder | 50054 | 8080 | 9090 |
| Party | 50055 | - | 9090 |
| Tenant | 50056 | - | 9090 |
| InternalAccount | 50057 | - | 9090 |
| MarketInformation | 50058 | - | 8082 |
| Reconciliation | 50060 | - | 9090 |
| Forecasting | 50061 | - | 9090 |
| ControlPlane | - | - | - |
| audit-worker | - | 8080 | 8080 |
| event-router | - | 8080 | 8080 |

## Observability

### Distributed Tracing

OpenTelemetry OTLP export to tracing backends:

- Automatic trace context propagation via gRPC interceptors
- Correlation ID propagation for request tracking
- Configurable sampling rate

### Prometheus Metrics

Each service exposes metrics on port 9090:

- `*_grpc_requests_total` - Request counts by method and status
- `*_grpc_request_duration_seconds` - Request latency histograms
- `*_health_check_total` - Health check results

### Health Checks

Aggregated health endpoints check:

- Database connectivity
- Kafka producer/consumer status
- Redis connectivity (if enabled)
- Downstream service availability

## Cross-Cutting Concerns

### Async Audit System

The async audit system provides guaranteed audit logging with dual-path delivery (Kafka primary, outbox fallback).
See [ADR-0009](../docs/adr/0009-application-level-audit-logging.md) for architecture rationale.

**Implementation Status:**

| Service | Audit Tables | GORM Hooks | Kafka Publisher | Audit Consumer |
|---------|:------------:|:----------:|:---------------:|:--------------:|
| CurrentAccount | ✅ | ✅ | ✅ | ✅ |
| PositionKeeping | ✅ | ✅ | ✅ | ✅ |
| FinancialAccounting | ✅ | ✅ | ✅ | ✅ |
| Party | ✅ | ✅ | ✅ | ✅ |
| PaymentOrder | ✅ | ✅ | ✅ | ✅ |
| Tenant | ✅ | ✅ | ✅ | ✅ |

**Architecture Components:**

1. **Audit Tables**: `audit_log` (permanent trail) and `audit_outbox` (fallback queue)
2. **GORM Hooks**: `AfterCreate`, `BeforeUpdate`, `AfterUpdate`, `AfterDelete` hooks capture changes
3. **Dual-Path Delivery**:
   - **Primary**: Publish audit event to Kafka → Audit Consumer → `audit_log` table
   - **Fallback**: Write to `audit_outbox` table → audit-worker → `audit_log` table
4. **Kafka Topics**: Per-service audit event topics (e.g., `audit.events.current-account`)
5. **Audit Consumers**: One Kafka consumer deployment per service (auto-scaling 2-20 replicas)
6. **Audit Worker**: Centralized service processes outbox entries when Kafka unavailable

**Key Guarantees:**

- **High Throughput**: Kafka primary path handles normal load asynchronously
- **Atomicity**: Outbox fallback committed with business operation (same transaction)
- **No Lost Audits**: Dual-path ensures delivery even during Kafka outages
- **Eventual Consistency**: Audit records appear in `audit_log` within ~100ms

### Gateway Service

The Gateway provides a multi-tenant API gateway for authenticated access to backend services.

**Responsibilities:**

- **JWT Authentication**: Validates Bearer tokens using JWKS (JSON Web Key Set)
- **API Key Authentication**: Validates service-to-service API keys with rate limiting
- **Tenant Resolution**: Extracts tenant identity from subdomain or headers
- **Request Proxying**: Routes authenticated requests to backend gRPC services

**Configuration:**

| Variable | Required | Description |
|----------|----------|-------------|
| `BASE_DOMAIN` | Yes | Base domain for subdomain-based tenant identification |
| `DATABASE_URL` | Yes | PostgreSQL connection string for tenant lookups |
| `AUTH_ENABLED` | No | Enable JWT/API key authentication (default: false) |
| `JWKS_URL` | When AUTH_ENABLED | JWKS endpoint URL for JWT validation |
| `BACKENDS` | No | JSON array of backend route mappings |

See [services/gateway/README.md](gateway/README.md) for full configuration options.

### Reference Data Service

The Reference Data service manages instrument definitions for the Universal Quantity Type System.
It is aligned with the BIAN Reference Data Directory domain.

**Responsibilities:**

- **Instrument Registry**: Manage currency, energy, and asset type definitions
- **CEL Validation**: Compile and execute validation rules for quantities
- **Fungibility Rules**: Define bucket key generation for position aggregation
- **System vs Tenant Instruments**: Pre-defined instruments seeded by Tenant service

**Instrument Lifecycle:**

```text
DRAFT → ACTIVE → DEPRECATED
```

- **DRAFT**: Editable, not usable in transactions
- **ACTIVE**: Immutable, validation enforced
- **DEPRECATED**: Read-only, not for new transactions

See [services/reference-data/README.md](reference-data/README.md) for full documentation.

### Utilization Metering Consumer

The Utilization Metering Consumer is a centralized Kafka consumer for platform billing.

**Responsibilities:**

- Consumes audit events from all 6 domain services
- Transforms audit events into utilization measurements
- Records measurements to Position Keeping's tenant-zero for billing

**Architecture:**

- **Single deployment** consuming from multiple topics (not per-service)
- **HPA scaling** based on Kafka consumer lag (1-5 replicas)
- **Tenant-zero isolation** for platform billing data

See [services/event-router/README.md](event-router/README.md) for full
documentation and [k8s/README.md](event-router/k8s/README.md) for deployment details.

### Internal Account Service

The Internal Account service manages non-customer accounts used for internal accounting
and correspondent banking operations.

**Responsibilities:**

- **Counterparty Accounts**: Nostro/vostro accounts for correspondent banking
- **Operational Accounts**: Clearing, suspense, holding, revenue, expense accounts
- **Multi-Asset Support**: Fiat, energy, carbon credits, compute hours
- **Balance Delegation**: Balances queried from Position Keeping (not stored locally)

**Account Types:** CLEARING, NOSTRO, VOSTRO, HOLDING, SUSPENSE, REVENUE, EXPENSE, INVENTORY

See [services/internal-account/README.md](internal-account/README.md) for full documentation.

### Reconciliation Service

The Reconciliation service verifies consistency across Position Keeping, Financial Accounting,
and Current Account services by matching positions and identifying discrepancies.

**Responsibilities:**

- **Position Matching**: Compare positions across upstream services
- **Discrepancy Tracking**: Identify and track variances for resolution
- **Settlement Scheduling**: Automated periodic reconciliation runs

See [services/reconciliation/README.md](reconciliation/README.md) for full documentation.

### Forecasting Service

The Forecasting service generates forward curves and forecasts using configurable Starlark-based
strategies with market data from the Market Information service.

**Responsibilities:**

- **Strategy Management**: DRAFT/ACTIVE/DEPRECATED lifecycle for forecast strategies
- **Starlark Execution**: Sandboxed script execution with built-in math functions
- **Scheduled Runs**: Cron-based execution with lease management for distributed safety
- **Template Library**: Pre-built strategies (moving average, linear regression, etc.)

See [services/forecasting/README.md](forecasting/README.md) for full documentation.

### Control Plane Service

The Control Plane manages tenant provisioning workflows, manifest-based configuration,
Stripe billing integration, and administrative operations.

**Responsibilities:**

- **Manifest Management**: Declarative tenant configuration with diff/apply workflow
- **Stripe Integration**: Payment processing, webhook handling, reconciliation
- **Admin Operations**: Balance sheet queries, causation tree visualization, CSV exports
- **Staff Identity**: Internal operator authentication and authorization

See [services/control-plane/README.md](control-plane/README.md) for full documentation.

## Service-Owned Client Libraries

Each service exports a client library for other services to use. This follows the idiomatic Go pattern
where the service is responsible for maintaining its own client, rather than each consumer implementing
their own client.

### Directory Structure

```text
services/<service-name>/
├── client/                 # Service-owned client library
│   ├── client.go           # Client implementation
│   └── client_test.go      # Client tests
└── ...
```

### Using a Service Client

```go
import (
    partyclient "github.com/meridianhub/meridian/services/party/client"
    sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
)

// Create client with DNS-based load balancing and resilience patterns
client, cleanup, err := partyclient.New(partyclient.Config{
    ServiceName: partyclient.ServiceName,       // "party"
    Namespace:   "default",
    Port:        partyclient.DefaultPort,       // 50055
    Timeout:     30 * time.Second,
    Tracer:      tracer,                        // OpenTelemetry tracer
    Resilience: &sharedclients.ResilientClientConfig{
        Logger:             logger,
        CircuitBreakerName: "party",
    },
})
if err != nil {
    return fmt.Errorf("failed to create party client: %w", err)
}
defer cleanup()

// Use the client
resp, err := client.RetrieveParty(ctx, &partyv1.RetrievePartyRequest{
    PartyId: partyID,
})
```

### Client Config Reference

All service clients follow a standard `Config` struct:

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `ServiceName` | `string` | Yes* | Kubernetes service name for DNS discovery |
| `Target` | `string` | Yes* | Direct address (e.g., `localhost:50055`) - deprecated |
| `Namespace` | `string` | No | Kubernetes namespace (default: `"default"`) |
| `Port` | `int` | No | Service port (each client has a default) |
| `Timeout` | `time.Duration` | No | RPC timeout (default: 30s) |
| `Tracer` | `*observability.Tracer` | No | OpenTelemetry tracer for distributed tracing |
| `Resilience` | `*clients.ResilientClientConfig` | No | Circuit breaker and retry configuration |
| `DialOptions` | `[]grpc.DialOption` | No | Custom gRPC dial options |

*Either `ServiceName` or `Target` is required. Prefer `ServiceName` for production.

### Built-in Resilience

When `Resilience` is configured, clients automatically include:

1. **Circuit Breaker**: Fails fast when downstream service is unhealthy
   - Opens after 5 consecutive failures
   - Half-open state after 60 seconds
   - Trips on gRPC UNAVAILABLE, DEADLINE_EXCEEDED, RESOURCE_EXHAUSTED

2. **Retry with Backoff**: Retries transient failures for idempotent operations
   - 3 retries with exponential backoff
   - Initial backoff: 100ms, max: 5s
   - Jitter: 0-100ms random addition
   - Only retries idempotent operations (reads)

3. **Trace Context Propagation**: Automatic correlation ID and trace propagation

### Available Service Clients

| Service | Import Path | Default Port |
|---------|-------------|--------------|
| CurrentAccount | `services/current-account/client` | 50051 |
| FinancialAccounting | `services/financial-accounting/client` | 50052 |
| PositionKeeping | `services/position-keeping/client` | 50053 |
| Party | `services/party/client` | 50055 |
| Tenant | `services/tenant/client` | 50056 |
| InternalAccount | `services/internal-account/client` | 50057 |
| MarketInformation | `services/market-information/client` | 50058 |
| Reconciliation | `services/reconciliation/client` | 50060 |
| ReferenceData | `services/reference-data/client` | 50051 |

## Service Directory Structure

Each service follows hexagonal architecture:

```text
services/<service-name>/
├── cmd/                    # Entry point, main.go, Dockerfile
├── domain/                 # Business logic, entities, value objects
├── service/                # gRPC service implementation
├── adapters/               # External adapters
│   ├── persistence/        # Database repositories
│   └── messaging/          # Kafka producers/consumers (if applicable)
├── client/                 # Service-owned client library
├── atlas/                  # Atlas schema config
├── migrations/             # Database migrations
└── k8s/                    # Kubernetes manifests
```

## Admin Tools

### tenantctl

Command-line interface for tenant lifecycle management. Communicates with the Tenant service via gRPC.

**Source:** [`cmd/tenantctl/`](../cmd/tenantctl/)

**Build:**

```bash
go build -o tenantctl ./cmd/tenantctl
```

**Commands:**

| Command | Purpose | Example |
|---------|---------|---------|
| `register` | Create new tenant | `tenantctl register --id=acme_bank --name="Acme Bank" --settlement-asset=GBP` |
| `list` | List tenants | `tenantctl list --status=active` |
| `get` | Retrieve tenant details | `tenantctl get acme_bank -o json` |
| `deprovision` | Deactivate tenant | `tenantctl deprovision acme_bank --confirm` |

**Configuration:**

| Variable | Default | Purpose |
|----------|---------|---------|
| `TENANT_SERVICE_URL` | `localhost:50056` | Tenant service address |

**Global Flags:**

- `--service-url` - Override service address
- `--timeout` - Request timeout (default: 30s)
- `-o, --output` - Output format (`text`, `json`)

**Demo Provisioning:**

The `scripts/demo-provision-organizations.sh` script provisions demo tenants for local development:

```bash
./scripts/demo-provision-organizations.sh
```

This creates: `meridian`, `post_office`, `motive`, `un_wfp`

## Data Model

Entity relationship diagrams showing all database tables across services,
split into two diagrams by connectivity. Solid lines (`--`) are
intra-service foreign key constraints; dotted lines (`..`) are
cross-service logical references (no FK due to database-per-service
architecture).

> **Naming:** Tables with identical names across services (e.g., `lien`,
> `valuation_features`) are prefixed with service abbreviations (`ca_`,
> `iba_`) for diagram clarity only -- actual DB table names are unprefixed.
> Shared infrastructure tables (`event_outbox`, `audit_log`,
> `audit_outbox`) follow a common schema and are omitted; see
> [Async Audit System](#async-audit-system).

### Core Transaction Engine (26 tables)

```mermaid
erDiagram
    %% ════════════════════════════════════
    %% TENANT MANAGEMENT SERVICE
    %% DB: meridian_tenant
    %% ════════════════════════════════════
    tenant {
        varchar id PK "slug e.g. acme_bank"
        varchar display_name
        varchar settlement_asset "default instrument"
        varchar slug UK "URL-safe"
        varchar party_id "ref Party"
        varchar status "provisioning|active|suspended"
    }

    tenant_provisioning {
        varchar tenant_id PK "FK tenant"
        varchar state "pending|in_progress|active|failed"
        jsonb service_schemas "per-service status"
    }

    tenant_provisioning_status {
        serial id PK
        varchar tenant_id FK
        varchar service_name
        varchar status "pending|completed|failed"
    }

    tenant ||--|| tenant_provisioning : "tracked-by"
    tenant ||--o{ tenant_provisioning_status : "provisions"

    %% ════════════════════════════════════
    %% PARTY SERVICE
    %% DB: meridian_party (org_{tenant} schema)
    %% ════════════════════════════════════

    party {
        uuid id PK
        varchar party_type "INDIVIDUAL|ORGANIZATION"
        varchar legal_name
        varchar status "ACTIVE|INACTIVE"
        varchar external_reference UK
    }

    party_association {
        uuid id PK
        uuid party_id FK
        varchar association_type
        varchar associated_party_reference
    }

    party_address {
        uuid id PK
        uuid party_id FK
        varchar address_type
    }

    party_qualification {
        uuid id PK
        uuid party_id FK
        varchar qualification_type
        varchar qualification_value
    }

    party_verification {
        uuid id PK
        uuid party_id FK
        varchar verification_type
        varchar status "PENDING|VERIFIED|FAILED|EXPIRED"
    }

    party_payment_method {
        uuid id PK
        uuid party_id FK
        varchar method_type "BANK_TRANSFER|CARD|DIRECT_DEBIT"
        varchar status "ACTIVE|SUSPENDED|REMOVED"
    }

    party ||--o{ party_association : "has"
    party ||--o{ party_address : "has"
    party ||--o{ party_qualification : "has"
    party ||--o{ party_verification : "has"
    party ||--o{ party_payment_method : "has"

    %% ════════════════════════════════════
    %% CURRENT ACCOUNT SERVICE
    %% DB: meridian_current_account (org_{tenant} schema)
    %% ════════════════════════════════════

    account {
        uuid id PK
        varchar account_id UK "unique slug"
        varchar account_identification UK "IBAN"
        uuid party_id "ref Party"
        varchar account_type
        varchar currency "3-char ISO"
        varchar status "active|disabled|pending|closed"
    }

    ca_lien {
        uuid id PK
        uuid account_id FK
        bigint amount_cents
        varchar currency
        varchar status "ACTIVE|EXECUTED|TERMINATED"
        varchar payment_order_reference UK "ref PO"
    }

    withdrawal {
        uuid id PK
        uuid account_id FK
        bigint amount_cents
        varchar currency
        varchar status
    }

    ca_valuation_features {
        uuid id PK
        uuid account_id FK
        varchar instrument_code "ref RD instrument"
        uuid valuation_method_id "ref RD method"
        varchar lifecycle_status "INITIATED|ACTIVE|TERMINATED"
    }

    account ||--o{ ca_lien : "holds"
    account ||--o{ withdrawal : "has"
    account ||--o{ ca_valuation_features : "valued-by"

    %% ════════════════════════════════════
    %% FINANCIAL ACCOUNTING SERVICE
    %% DB: meridian_financial_accounting (org_{tenant} schema)
    %% ════════════════════════════════════

    financial_booking_log {
        uuid id PK
        varchar financial_account_type
        varchar base_currency
        varchar status
        varchar idempotency_key UK
    }

    ledger_posting {
        uuid id PK
        uuid financial_booking_log_id FK
        varchar posting_direction "DEBIT|CREDIT"
        bigint amount_cents
        varchar currency
        varchar account_id "ref IBA"
        varchar correlation_id
    }

    financial_booking_log ||--o{ ledger_posting : "has"

    %% ════════════════════════════════════
    %% POSITION KEEPING SERVICE
    %% DB: meridian_position_keeping (org_{tenant} schema)
    %% ════════════════════════════════════

    financial_position_log {
        uuid id PK
        uuid log_id UK
        varchar account_id "ref CurrentAccount"
        varchar current_status "PENDING|POSTED|RECONCILED"
        varchar instrument_code "ref RD instrument"
        varchar bucket_id "bucketing key"
        varchar dimension "Monetary|Energy|Compute|Carbon"
        decimal opening_balance_amount
    }

    transaction_log_entry {
        uuid id PK
        uuid entry_id UK
        uuid financial_position_log_id FK
        uuid transaction_id
        varchar account_id "ref CurrentAccount"
        bigint amount_cents
        varchar direction "DEBIT|CREDIT"
    }

    transaction_lineage {
        uuid id PK
        uuid financial_position_log_id FK "unique"
        uuid transaction_id
        uuid parent_transaction_id
        varchar transaction_type
    }

    audit_trail_entry {
        uuid id PK
        uuid audit_id UK
        uuid financial_position_log_id FK
        varchar user_id
        varchar action
    }

    measurement {
        uuid id PK
        uuid financial_position_log_id FK
        timestamptz observation_at
        int quality "1-EST 2-COEFF 3-ACT 4-REV"
        decimal amount
    }

    financial_position_log ||--o{ transaction_log_entry : "records"
    financial_position_log ||--o| transaction_lineage : "traced-by"
    financial_position_log ||--o{ audit_trail_entry : "audited-by"
    financial_position_log ||--o{ measurement : "measured-by"

    %% ════════════════════════════════════
    %% PAYMENT ORDER SERVICE
    %% DB: meridian_payment_order (org_{tenant} schema)
    %% ════════════════════════════════════

    payment_order {
        uuid id PK
        varchar debtor_account_id "ref CurrentAccount"
        bigint amount_cents
        varchar currency
        varchar status "INITIATED|RESERVED|COMPLETED|FAILED"
        varchar lien_id "ref CA lien"
        varchar ledger_booking_id "ref FA booking"
        varchar idempotency_key UK
    }

    billing_run {
        uuid id PK
        varchar tenant_id "ref Tenant"
        timestamptz cycle_start
        timestamptz cycle_end
        varchar status "INITIATED|PROCESSING|COMPLETED"
    }

    invoice {
        uuid id PK
        uuid billing_run_id FK
        varchar party_id "ref Party"
        varchar account_id "ref CurrentAccount"
        varchar invoice_number UK
        varchar status "DRAFT|ISSUED|PAID|VOID|OVERDUE"
        uuid payment_order_id "ref PaymentOrder"
    }

    dunning {
        uuid id PK
        uuid invoice_id FK
        int dunning_level
        varchar status
    }

    billing_run ||--o{ invoice : "generates"
    invoice ||--o{ dunning : "escalates"

    %% ════════════════════════════════════
    %% INTERNAL ACCOUNT SERVICE
    %% DB: meridian_iba (org_{tenant} schema)
    %% ════════════════════════════════════

    internal_account {
        uuid id PK
        varchar account_id UK
        varchar account_code
        varchar name
        varchar account_type "CLEARING|NOSTRO|VOSTRO|HOLDING"
        varchar instrument_code "ref RD instrument"
        varchar dimension "CURRENCY|ENERGY|CARBON|COMPUTE"
        varchar status "ACTIVE|SUSPENDED|CLOSED"
    }

    iba_lien {
        uuid id PK
        uuid account_id FK
        bigint amount_cents
        varchar currency
        varchar status "ACTIVE|EXECUTED|TERMINATED"
    }

    iba_valuation_features {
        uuid id PK
        uuid account_id FK
        varchar instrument_code "ref RD instrument"
        uuid valuation_method_id "ref RD method"
        varchar lifecycle_status "INITIATED|ACTIVE|TERMINATED"
    }

    internal_account ||--o{ iba_lien : "holds"
    internal_account ||--o{ iba_valuation_features : "valued-by"

    %% ════════════════════════════════════
    %% REFERENCE DATA SERVICE (Central Registry)
    %% DB: meridian_reference_data (org_{tenant} schema)
    %% ════════════════════════════════════

    instrument_definition {
        uuid id PK
        varchar code "GBP KWH TONNE_CO2E GPU_HR"
        int version
        varchar dimension "MONETARY|ENERGY|COMPUTE|CARBON"
        int precision "0-18 decimal places"
        varchar status "DRAFT|ACTIVE|DEPRECATED"
        text validation_expression "CEL"
    }

    valuation_method {
        uuid id PK
        varchar name
        int version
        varchar input_instrument "ref instrument"
        varchar output_instrument "ref instrument"
        text logic_script "Starlark"
        varchar lifecycle_status "INITIATED|ACTIVE|DEPRECATED"
    }

    %% ════════════════════════════════════════════════
    %% CROSS-SERVICE DOMAIN RELATIONSHIPS (Core)
    %% Dotted lines = logical references (no FK)
    %% ════════════════════════════════════════════════

    party ||..o{ account : "owns"
    tenant }o..o| party : "represented-by"
    account ||..o{ financial_position_log : "positions"
    account ||..o{ payment_order : "debtor"
    ca_lien ||..o| payment_order : "reserves"
    financial_booking_log ||..o| payment_order : "books"
    internal_account ||..o{ ledger_posting : "posted-to"
    instrument_definition ||..o{ financial_position_log : "denominated"
    instrument_definition ||..o{ internal_account : "denominated"
    valuation_method ||..o{ ca_valuation_features : "applied"
    valuation_method ||..o{ iba_valuation_features : "applied"
    tenant ||..o{ billing_run : "billed"
```

Tenant Management (3), Party (6), Current Account (4),
Financial Accounting (2), Position Keeping (5), Payment Order (4),
Internal Account (3), Reference Data (2). These services form
the densely interconnected transaction processing core.

### Market Data, Reconciliation & Operations (17 tables)

```mermaid
erDiagram
    %% ════════════════════════════════════
    %% MARKET INFORMATION SERVICE
    %% DB: meridian_market_information
    %% ════════════════════════════════════

    data_source {
        uuid id PK
        varchar code UK "ECB_DAILY etc"
        varchar name
        int trust_level "0-100"
    }

    dataset_definition {
        uuid id PK
        varchar code "FX_RATE ENERGY_SPOT"
        int version
        varchar data_category
        varchar status "DRAFT|ACTIVE|DEPRECATED"
        text validation_expression "CEL"
    }

    market_price_observation {
        uuid id PK
        uuid dataset_definition_id FK
        uuid data_source_id FK
        varchar resolution_key "e.g. EUR-USD"
        timestamptz observed_at "event time"
        timestamptz created_at "knowledge time"
        int quality "1-EST 2-ACT 3-VER"
        numeric numeric_value
        uuid superseded_by "self-ref"
    }

    dataset_definition ||--o{ market_price_observation : "has"
    data_source ||--o{ market_price_observation : "provides"
    market_price_observation }o--o| market_price_observation : "supersedes"

    %% ════════════════════════════════════
    %% RECONCILIATION SERVICE
    %% DB: meridian_reconciliation (org_{tenant} schema)
    %% ════════════════════════════════════

    settlement_run {
        uuid id PK
        uuid run_id UK
        varchar account_id "ref Core: account"
        varchar scope "ACCOUNT|INSTRUMENT|PORTFOLIO"
        varchar settlement_type "DAILY|WEEKLY|MONTHLY"
        varchar status "PENDING|RUNNING|COMPLETED|FAILED"
    }

    settlement_snapshot {
        uuid id PK
        uuid snapshot_id UK
        uuid run_id FK
        varchar account_id
        varchar instrument_code "ref Core: instrument"
        decimal expected_balance
        decimal actual_balance
        decimal variance_amount
    }

    variance {
        uuid id PK
        uuid variance_id UK
        uuid run_id FK
        uuid snapshot_id FK
        varchar reason "AMOUNT_MISMATCH|TIMING_DIFF"
        varchar status "DETECTED|OPEN|RESOLVED"
    }

    dispute {
        uuid id PK
        uuid dispute_id UK
        uuid variance_id FK
        uuid run_id FK
        varchar status "OPEN|UNDER_REVIEW|RESOLVED"
    }

    balance_assertion {
        uuid id PK
        uuid assertion_id UK
        uuid run_id FK
        varchar account_id
        varchar instrument_code "ref Core: instrument"
        text expression "CEL"
        varchar status "PENDING|PASSED|FAILED|OVERRIDE"
    }

    settlement_run ||--o{ settlement_snapshot : "captures"
    settlement_run ||--o{ variance : "detects"
    settlement_snapshot ||--o{ variance : "has"
    variance ||--o{ dispute : "raises"
    settlement_run ||--o{ balance_assertion : "asserts"

    %% ════════════════════════════════════
    %% FORECASTING SERVICE
    %% DB: meridian_forecasting
    %% ════════════════════════════════════

    forecasting_strategy {
        uuid id PK
        varchar tenant_id "ref Core: tenant"
        varchar name
        text starlark_code "Starlark"
        int horizon_hours "1-168"
        varchar schedule "cron expression"
        varchar output_dataset_code "ref MI dataset"
        varchar status "DRAFT|ACTIVE|DEPRECATED"
    }

    %% ════════════════════════════════════
    %% CONTROL PLANE SERVICE
    %% DB: meridian_control_plane (org_{tenant} schema)
    %% ════════════════════════════════════

    staff_user {
        uuid id PK
        varchar email UK
        varchar role "admin|operator|auditor"
        varchar status "invited|active|suspended"
    }

    api_key {
        uuid id PK
        uuid staff_user_id FK
        varchar key_prefix UK "pk_slug_entropy"
        bytea key_hash "SHA-256"
        int rate_limit_rps "default 100"
    }

    manifest_version {
        uuid id PK
        int version
        varchar config_hash
    }

    manifest_apply_job {
        uuid id PK
        uuid manifest_version_id FK
        varchar status "PENDING|APPLYING|APPLIED|FAILED"
    }

    staff_user ||--o{ api_key : "has"
    manifest_version ||--o{ manifest_apply_job : "tracked-by"

    %% ════════════════════════════════════
    %% REFERENCE DATA (supplementary tables)
    %% Not connected to core transaction graph
    %% ════════════════════════════════════

    saga_definition {
        uuid id PK
        varchar name
        int version
        text script "Starlark"
        varchar status "DRAFT|ACTIVE|DEPRECATED"
        text preconditions_expression "CEL"
        uuid successor_id "self-ref FK"
    }

    saga_execution {
        uuid id PK
        varchar saga_definition_name "ref RD saga"
        int saga_definition_version
        varchar current_step
        varchar status
        jsonb context_data
    }

    valuation_policy {
        uuid id PK
        varchar name
        int version
        text cel_expression "CEL"
        varchar output_type
        varchar lifecycle_status "INITIATED|ACTIVE|DEPRECATED"
    }

    reference_data_node {
        uuid id PK
        varchar code
        uuid parent_id "self-ref FK"
        varchar node_type
        jsonb attributes
    }

    saga_definition }o--o| saga_definition : "successor"
    reference_data_node }o--o| reference_data_node : "parent"

    %% ════════════════════════════════════════════════
    %% CROSS-SERVICE RELATIONSHIPS (within this group)
    %% ════════════════════════════════════════════════

    dataset_definition ||..o{ forecasting_strategy : "feeds"
    saga_definition ||..o{ saga_execution : "defines"
```

Market Information (3), Reconciliation (5), Forecasting (1),
Control Plane (4), Reference Data supplementary (4). These
services connect to the core engine through thin references
(`account_id`, `instrument_code`, `tenant_id`) annotated as
`ref Core:` in column comments.

**Cross-Service Reference Patterns:**

| Reference Column | Used In | Target |
| ----------------------- | --------------------------------- | ---------- |
| `party_id` | account, invoice, tenant | Party |
| `account_id` | position_log, txn_entry, PO, recon | CA / IBA |
| `instrument_code` | position_log, IBA, recon, val feat | Ref Data |
| `valuation_method_id` | ca/iba valuation_features | Ref Data |
| `saga_definition_name` | saga_execution | Ref Data |
| `dataset_code` | forecasting_strategy (in + out) | Market Info |
| `tenant_id` | billing_run, forecasting_strategy | Tenant Mgmt |
| `payment_order_ref` | ca_lien | Payment Ord |
| `ledger_booking_id` | payment_order | Fin Acct |

**Shared Infrastructure Tables** (omitted from diagram, present in most services):

| Table            | Pattern | Purpose                            |
| ---------------- | ------- | ---------------------------------- |
| `event_outbox`   | Outbox  | Transactional event publishing     |
| `audit_log`      | Audit   | Permanent change history           |
| `audit_outbox`   | Audit   | Fallback queue (Kafka unavailable) |

## References

- [Protocol Buffer API Definitions](../api/proto/README.md) - gRPC service interfaces
- [ADR-0002: Microservices per BIAN Domain](../docs/adr/0002-microservices-per-bian-domain.md)
- [ADR-0004: Event Schema Evolution](../docs/adr/0004-event-schema-evolution.md)
- [ADR-0009: Application-Level Audit Logging](../docs/adr/0009-application-level-audit-logging.md)
- [Tilt Development Guide](../docs/skills/tilt.md) - Local development
