# Meridian Services Architecture

This document describes the runtime architecture of Meridian services, including all communication
protocols, infrastructure dependencies, and data flows.

## System Architecture

The diagram below shows how services communicate at runtime across all protocols.

```mermaid
flowchart LR
    subgraph External["External Systems"]
        User["User/Client"]
        Gateway["Payment Gateway"]
    end

    subgraph Admin["Admin Tools"]
        TenantCtl["tenantctl<br/>(CLI)"]
    end

    subgraph Platform["Meridian Platform"]
        subgraph Services["Microservices"]
            CA["CurrentAccount<br/>:50057"]
            PK["PositionKeeping<br/>:50053"]
            FA["FinancialAccounting<br/>:50052"]
            Party["Party<br/>:50055"]
            PO["PaymentOrder<br/>:50054, :8080"]
            Tenant["Tenant<br/>:50056"]
        end

        subgraph Platform_Services["Platform Services"]
            AW["audit-worker<br/>:8080"]
        end

        subgraph Infrastructure["Infrastructure"]
            DB[("CockroachDB<br/>:26257")]
            Kafka@{ shape: das, label: "Kafka :9092" }
            Redis@{ shape: das, label: "Redis :6379" }
        end
    end

    %% Admin tool connections
    TenantCtl -->|"gRPC"| Tenant

    %% External connections
    User -->|"gRPC / REST"| CA
    User -->|"gRPC / REST"| PO
    Gateway -->|"HTTP Webhook"| PO

    %% gRPC inter-service calls
    CA -->|"RetrieveParty (gRPC)"| Party
    CA -->|"InitiateFinancialPositionLog (gRPC)"| PK
    CA -->|"CaptureLedgerPosting (gRPC)"| FA
    PO -->|"InitiateLien (gRPC)"| CA
    Tenant -.->|"RegisterParty (gRPC, optional)"| Party

    %% Kafka event streaming
    PK -->|"Publish Events"| Kafka
    Kafka -->|"Consume Events"| FA

    %% Database connections
    CA -->|"SQL"| DB
    PK -->|"SQL"| DB
    FA -->|"SQL"| DB
    Party -->|"SQL"| DB
    PO -->|"SQL"| DB
    Tenant -->|"SQL"| DB

    %% Redis (optional idempotency)
    PK -.->|"Idempotency"| Redis
    FA -.->|"Idempotency"| Redis
    PO -.->|"Idempotency"| Redis

    %% audit-worker connections
    AW -->|"Poll outbox"| DB
    AW -->|"Write audit log"| DB

    classDef service fill:#4a90d9,stroke:#2d5a87,color:#fff
    classDef platform fill:#607d8b,stroke:#455a64,color:#fff
    classDef storage fill:#50c878,stroke:#2d7a4a,color:#fff
    classDef external fill:#ff9800,stroke:#e65100,color:#fff
    classDef admin fill:#9c27b0,stroke:#6a1b9a,color:#fff

    class CA,PK,FA,Party,PO,Tenant service
    class AW platform
    class DB,Kafka,Redis storage
    class User,Gateway external
    class TenantCtl admin
```

**Legend:**

- Solid arrows (`-->`) = Required runtime dependency
- Dashed arrows (`-.->`) = Optional runtime dependency
- Blue boxes = Microservices (domain services)
- Grey boxes = Platform services (infrastructure)
- Purple boxes = Admin tools (CLI)
- Vertical cylinder `[(" ")]` = Database (CockroachDB)
- Horizontal cylinder `@{ shape: das }` = Direct access storage (Kafka, Redis)
- Orange boxes = External systems

## Communication Protocols

### gRPC (Synchronous)

All inter-service communication uses gRPC with Protocol Buffers:

| Source | Target | Method | Purpose |
|--------|--------|--------|---------|
| CurrentAccount | Party | `RetrieveParty()` | Verify party exists and is active |
| CurrentAccount | PositionKeeping | `InitiateFinancialPositionLog()` | Create position log for account |
| CurrentAccount | FinancialAccounting | `CaptureLedgerPosting()` | Record double-entry posting |
| PaymentOrder | CurrentAccount | `InitiateLien()` | Reserve funds for payment |
| Tenant | Party | `RegisterParty()` | Register org party (optional) |

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
| CurrentAccount | 50057 | - | 9090 |
| PositionKeeping | 50053 | - | 9090 |
| FinancialAccounting | 50052 | - | 9090 |
| Party | 50055 | - | 9090 |
| PaymentOrder | 50054 | 8080 | 9090 |
| Tenant | 50056 | - | 9090 |
| audit-worker | - | 8080 | 8080 |

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

### Audit Outbox Pattern

The transactional outbox pattern provides guaranteed audit logging with eventual delivery.
See [ADR-0009](../docs/adr/0009-application-level-audit-logging.md) for architecture rationale.

**Implementation Status:**

| Service | Audit Schema | Outbox Table | GORM Hooks | Worker |
|---------|:------------:|:------------:|:----------:|:------:|
| CurrentAccount | ✅ | ✅ | ❌ | ❌ |
| PositionKeeping | ✅ (per-aggregate) | ❌ | ❌ | ❌ |
| FinancialAccounting | ❌ | ❌ | ❌ | ❌ |
| Party | ❌ | ❌ | ❌ | ❌ |
| PaymentOrder | ❌ | ❌ | ❌ | ❌ |
| Tenant | ❌ | ❌ | ❌ | ❌ |

**Pattern Components:**

1. **Audit Schema**: Dedicated `{service}_audit` schema with `audit_log` table
2. **Outbox Table**: `audit_outbox` table written atomically with business transaction
3. **GORM Hooks**: `AfterCreate`, `BeforeUpdate`, `AfterUpdate`, `AfterDelete` hooks
4. **Worker**: The `audit-worker` service polls the outbox and moves entries to `audit_log`

**Key Guarantees:**

- Atomicity: Audit intent committed with business operation
- No lost audits: Outbox survives application crashes
- Idempotency: Retry-safe processing without duplicates

## Service Directory Structure

Each service follows hexagonal architecture:

```text
services/<service-name>/
├── cmd/                    # Entry point, main.go, Dockerfile
├── app/                    # Application bootstrap, DI container
├── domain/                 # Business logic, entities, value objects
├── service/                # Use cases, gRPC service implementation
├── adapters/               # External adapters
│   ├── persistence/        # Database repositories
│   ├── messaging/          # Kafka producers/consumers
│   └── http/               # HTTP handlers (if applicable)
├── clients/                # gRPC clients for upstream services
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

## References

- [Protocol Buffer API Definitions](../api/proto/README.md) - gRPC service interfaces
- [ADR-0002: Microservices per BIAN Domain](../docs/adr/0002-microservices-per-bian-domain.md)
- [ADR-0004: Event Schema Evolution](../docs/adr/0004-event-schema-evolution.md)
- [ADR-0009: Application-Level Audit Logging](../docs/adr/0009-application-level-audit-logging.md)
- [Tilt Development Guide](../docs/skills/tilt.md) - Local development
