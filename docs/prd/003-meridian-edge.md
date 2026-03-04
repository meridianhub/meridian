---
name: prd-meridian-edge
description: Embedded financial kernel for IoT devices and browser deployment
triggers:
  - Implementing Edge or IoT deployment target
  - Working on SQLite adapter or persistence abstraction
  - Discussing offline-first or local-first architecture
  - Browser WASM deployment
  - Single-tenant embedded deployment
  - Local event dispatcher or replacing Kafka
instructions: |
  Meridian Edge is a modular monolith build target that collapses the microservices
  into a single binary. Key patterns:
  - Domain logic is UNCHANGED - only adapters are swapped
  - SQLite replaces CockroachDB (use modernc.org/sqlite for CGO-free)
  - LocalEventDispatcher implements KafkaPublisher interface
  - Same outbox pattern for durability, different transport
  - CEL expressions are portable - same rules run everywhere
  - Build tags separate Edge vs Cloud adapters
---

# PRD: Meridian Edge (The Fractal Ledger)

| Meta | Details |
|:-----|:--------|
| **Project Name** | Meridian Edge |
| **Status** | Not Started |
| **Type** | New Build Target (Modular Monolith) |
| **Target Hardware** | Raspberry Pi Zero 2 W / ARM64 / Android / Linux IoT / Browser (WASM) |
| **Core Philosophy** | "Local Authority, Global Consistency" |
| **Task Master Tag** | `meridian-edge` |

## 1. Executive Summary

**Meridian Edge** is a lightweight, offline-first version of the Meridian Ledger designed to run on
resource-constrained IoT devices and in web browsers.

By leveraging Go's static compilation and Hexagonal Architecture, we "collapse" the cloud
microservices architecture into a single binary. It replaces heavy infrastructure
(Kafka/CockroachDB) with lightweight alternatives (Go Channels/SQLite) while maintaining
**100% logic parity** with the Cloud.

The same codebase produces three deployment targets:

| Target | Runtime | Persistence | Messaging | Use Case |
|--------|---------|-------------|-----------|----------|
| **Cloud** | k8s pods | CockroachDB | Kafka | Multi-tenant SaaS |
| **Edge** | systemd/native | SQLite (WAL) | Channels + Outbox | IoT devices, smart meters |
| **Browser** | WASM | SQLite (OPFS) | PostMessage | Personal ledger, PWA |

**The Commercial Goal:** Enable "Smart" devices (Meters, POS, Wallets) to calculate value and
settle transactions locally, solving the latency, connectivity, and trust issues inherent in
cloud-only financial systems.

## 2. Problem Statement

Current IoT financial systems (Smart Meters, Payment Terminals) are "Dumb Terminals."

### 2.1 The "Agile" Gap

Smart Meters record volume (kWh) but rely on the cloud to calculate value (£). If the cloud is
unreachable or the pricing is complex (half-hourly), the device displays wrong information.

### 2.2 The "Horizon" Risk

Centralized ledgers allow remote tampering. The user has no local, cryptographic proof of their
transaction history. The Fujitsu/Post Office Horizon scandal demonstrated the catastrophic
consequences of systems where operators can modify transaction records without cryptographic
accountability.

### 2.3 Connectivity Dependency

Commerce stops when the internet stops. In emerging markets, refugee camps, and rural areas,
connectivity is intermittent. Financial infrastructure must be resilient to this reality.

## 3. Solution Architecture: The Modular Monolith

We do not rewrite the domain logic. We implement new **Adapters** for the existing **Ports**.

### 3.1 The "Collapse" Strategy

| Layer | Meridian Cloud (Existing) | Meridian Edge (New) |
|:------|:--------------------------|:--------------------|
| **Runtime** | Multiple Microservices (k8s) | Single Process (Systemd/Native) |
| **Transport** | gRPC over TCP | Go Interface Calls (In-Memory) |
| **Event Bus** | Kafka Cluster | Go Channels + SQLite Outbox |
| **Persistence** | CockroachDB (PGX) | SQLite (CGO-free / modernc.org) |
| **Tenancy** | Multi-Tenant (Schema per Tenant) | Single Tenant (Multi-Account) |
| **Auth** | JWKS/JWT | Device Certificates or API Keys |

### 3.2 Why This Works

The architectural constraints we embraced for CockroachDB compatibility are precisely what
SQLite needs:

| CockroachDB Constraint | Why We Did It | SQLite Benefit |
|------------------------|---------------|----------------|
| No stored procedures | Distributed SQL limitation | SQLite has none |
| Append-only positions | Avoid distributed locks | Single-writer WAL loves this |
| Simple queries | Distributed query planning | SQLite optimizer is simple |
| Optimistic locking | Avoid row locks | Works identically |
| Event outbox pattern | Can't do 2PC with Kafka | Works with any database |

### 3.3 What Stays The Same

The following components are **identical** across all deployment targets:

- Domain models (`Position`, `FinancialPositionLog`, `Money`, `Measurement`)
- Business rules (double-entry validation, status transitions)
- Event schemas (Protobuf definitions)
- CEL expressions (validation, bucket keys, valuation)
- Repository interfaces (`FinancialPositionLogRepository`, `PositionRepository`)
- Event publisher interface (`EventPublisher`)

### 3.4 The Data Model (Local)

The Edge device acts as a Sovereign Bank for a single Tenant (The Device Owner).

- **Accounts:** Supports multiple internal accounts (e.g., `Grid_Import`, `Solar_Gen`, `Prepaid_Wallet`)
- **Ledger:** Standard Double-Entry (Sum of Debits = Sum of Credits)
- **Security:** Every `LedgerPosting` is signed by the Device's key
  (software ECDSA for MVP, hardware TPM/ATECC608A for production)

### 3.5 The Authority Model (Conflict Prevention)

To prevent split-brain scenarios and eliminate conflict resolution complexity, we define strict
authority domains by asset class:

| Authority | Domain | Examples |
|-----------|--------|----------|
| **Edge** | Usage, Flow, Local Transfers | kWh consumed, GPU-hours used, local account moves |
| **Cloud** | Deposits, Tariffs, Configuration | Top-ups, pricing rules, firmware updates |

**Why This Works:**

- The Cloud accepts Edge measurements as truth (because they are device-signed)
- The Edge accepts Cloud deposits as truth (because they are cloud-signed)
- No entity can modify the other's authoritative domain
- Conflicts are impossible by design - not resolved, but prevented

This segregation mirrors real-world authority: the meter measures consumption (Edge), the utility
sets the price (Cloud). Neither can forge the other's signature.

## 4. Key Functional Requirements

### 4.1 Feature: Local Valuation (The "Smart" Meter)

**Description:** The device calculates the monetary value of physical events in real-time using CEL rules.

The device executes the same CEL expressions as the cloud:

- **Validation:** `attributes["meter_type"] == "export" && parse_decimal(amount) > 0`
- **Bucket Key:** `bucket_key([attributes["tariff"], attributes["period"]])`
- **Valuation:** `quantity * market_data.price[instrument][timestamp]`

Rules are pushed from cloud as strings. The device compiles and caches them locally.
No code deployment required to change pricing logic.

**Input:** `Measurement` (e.g., 1 kWh)
**Context:** `MarketData` (received via MQTT from Cloud)
**Logic:** Executes the identical CEL (`qty * price[now]`) as the cloud
**Output:** `FinancialPositionLog` (Debit Customer / Credit Revenue)

### 4.2 Feature: Store & Forward Sync

**Description:** The device operates indefinitely offline.

**Mechanism:**

1. Transactions are committed to local SQLite
2. Events are written to `sync_outbox` table (same outbox pattern as cloud)
3. **Sync Worker** connects to MQTT when available
4. Uploads batch of signed events
5. Cloud acknowledges ("Cursor updated to Tx #500")
6. Device prunes `sync_outbox` (keeping Ledger intact)

### 4.3 Feature: Cryptographic Non-Repudiation

**Description:** Prevent "Fujitsu/Horizon" style tampering.

**Requirement:** The device signs the hash of every transaction chain.

**Verification:** The Cloud validates the signature. If the Cloud database is manually altered,
the cryptographic chain breaks. The Device is the Source of Truth.

### 4.4 Feature: Browser Deployment (WASM)

**Description:** Run the same ledger logic in a web browser.

**Mechanism:**

- Compile to WASM: `GOOS=js GOARCH=wasm go build`
- Persistence via **OPFS (Origin Private File System)** using `sqlite-wasm` with
  `file-system-access` API
- All SQLite operations run in a **Web Worker** to prevent blocking the UI thread
- Sync via fetch API / WebSocket to cloud endpoints
- Same CEL expressions, same domain logic

**Why OPFS over alternatives:**

- `sql.js` loads entire database into RAM - not viable for larger ledgers
- IndexedDB is asynchronous and slow for transactional workloads
- OPFS provides a virtual filesystem with near-native SQLite performance
- Works offline as a Progressive Web App (PWA)

**Use Cases:**

- Offline PWA - Works without internet
- Local-first - Transactions commit to OPFS immediately
- Portable - Export ledger as signed SQLite file
- Verifiable - Anyone can validate the transaction chain

## 5. Technical Implementation Plan

### Phase 1: The Build Target

**Objective:** Compile the existing services into one binary.

**Actions:**

- Create `cmd/meridian-edge/main.go`
- Manually instantiate Service Structs (`NewService(...)`) and inject them into each other
- Bypass gRPC Client generation - use direct interface calls
- Create `LocalEventDispatcher` implementing `KafkaPublisher` interface

**Success Criteria:** A working binary where `CurrentAccount` calls `PositionKeeping` in-memory.

### Phase 2: The Persistence Swap

**Objective:** Replace Postgres/CockroachDB with SQLite.

**Actions:**

- Implement `adapters/persistence/sqlite` package
- Use `modernc.org/sqlite` for CGO-free pure Go (WASM-compatible)
- Remove `WithTenantScope` (search_path) logic - SQLite is single-tenant
- Map existing SQL to SQLite dialect (minimal changes due to standard SQL)

**Success Criteria:** All repository interface tests pass with SQLite backend.

### Phase 3: The Event Bus

**Objective:** Replace Kafka with local dispatch.

**Actions:**

- Implement `adapters/messaging/local_dispatcher.go`
- `LocalEventDispatcher` implements existing `KafkaPublisher` interface
- Events dispatched to registered handlers via Go channels
- SQLite outbox for durability (same `event_outbox` table schema)

**Success Criteria:** Outbox worker publishes to local handlers instead of Kafka.

### Phase 4: MQTT Integration

**Objective:** Bridge to the Cloud.

**Actions:**

- Implement `services/api-gateway-mqtt` package
- **Subscribe:** `/config/tariff` -> Updates local Reference Data
- **Publish:** `/ledger/sync` -> Pushes Outbox events
- Use MQTT for IoT (low overhead, pub/sub, works on 2G networks, QoS levels)

**Success Criteria:** Device syncs with cloud when connectivity available.

### Phase 5: Browser Target (WASM)

**Objective:** Run in web browsers.

**Actions:**

- Create `cmd/meridian-browser/main.go` with WASM build tags
- Integrate SQLite WASM with OPFS VFS for persistent storage
- Wrap network calls with fetch API via `syscall/js`
- Create web worker wrapper for non-blocking operation

**Success Criteria:** Ledger operations work in Chrome/Firefox with offline support using OPFS.

## 6. Non-Functional Requirements

| Requirement | Target | Rationale |
|-------------|--------|-----------|
| **Binary Size** | < 15MB (static, stripped) | SD card constraints, download time |
| **Memory Usage** | < 50MB RAM under load | Raspberry Pi Zero has 512MB |
| **Storage** | Auto-pruning of logs | Prevent SD card exhaustion |
| **Resilience** | Clean recovery from power loss | ACID properties of SQLite WAL |
| **Startup Time** | < 2 seconds to ready | Systemd service expectations |
| **WASM Size** | < 10MB gzipped | Acceptable for SPA load |
| **RTO** | < 5 seconds after power loss | Proves ACID compliance and WAL recovery |

### 6.1 Storage & Wear Leveling

To ensure device longevity on flash storage (SD/eMMC):

- **WAL Mode:** SQLite must run in WAL mode (`PRAGMA journal_mode = WAL`)
- **Synchronous Normal:** Reduce fsync frequency while maintaining consistency
  (`PRAGMA synchronous = NORMAL` - safe in WAL mode, reduces fsyncs by ~90%)
- **Batching:** Position Keeping buffers measurements in RAM and flushes to disk every N seconds
  or M events to minimize write cycles
- **Pruning:** `SyncWorker` aggressively prunes `sync_outbox` after Cloud acknowledgement to
  maintain fixed disk usage footprint (< 100MB ledger data)

### 6.2 Schema Migration Strategy

Unlike Cloud deployments with orchestrated rollouts, Edge devices require self-healing migrations:

- **Embedded Migrations:** On binary startup, the application runs embedded migrations (Atlas or
  go-migrate) before starting domain services
- **Version Check:** Device stores schema version; binary refuses to start if forward migration
  is not possible
- **A/B Partitioning:** For critical deployments, maintain dual root partitions - if migration
  fails, rollback to previous binary automatically
- **Safe Mode:** If migration fails and rollback unavailable, enter read-only Safe Mode that
  allows sync of existing data but blocks new transactions

## 7. Parity Testing Strategy

To guarantee "100% logic parity" between Cloud, Edge, and Browser:

### 7.1 Shared Test Suite

```go
// tests/parity/ledger_test.go
func TestDoubleEntryInvariant(t *testing.T, repo domain.FinancialPositionLogRepository) {
    // Same test runs against Postgres, SQLite, and IndexedDB
}
```

### 7.2 Build Tag Separation

```go
//go:build edge

package persistence

// SQLite implementation
```

```go
//go:build !edge

package persistence

// PostgreSQL implementation
```

### 7.3 CEL Expression Validation

CEL expressions are the key to parity. The same expression string produces identical results regardless of runtime:

```go
// Cloud, Edge, and Browser all evaluate identically
expr := `bucket_key([attributes["tariff"], attributes["period"]])`
// SHA256 hash ensures deterministic output
```

## 8. The User Story (The Demo)

**Scenario:** The "Agile" Smart Plug.

1. **Setup:** User plugs a heater into a Meridian Edge Smart Plug
2. **Cloud:** Pushes tariff: "Price is -5p between 2:00 and 3:00"
3. **Action:** Device turns heater ON at 2:00
4. **Local Ledger:** Records consumption. *Credits* the user's local wallet (earning money)
5. **Display:** Shows "Earnings: £0.05"
6. **Sync:** Device uploads signed proof of consumption. Cloud credits the actual billing account

**Why This Matters:** The user sees real-time, accurate value - not a delayed estimate from the
cloud. The signed proof prevents disputes.

## 9. Security Considerations

### 9.1 Device Identity

- Each device has a unique key pair generated at provisioning
- Public key registered with cloud during setup
- All transactions signed with device private key

### 9.2 Key Storage (Progressive)

| Environment | Key Storage | Security Level |
|-------------|-------------|----------------|
| MVP / Dev | Encrypted file on disk | Basic |
| Production IoT | ATECC608A secure element | Hardware-backed |
| Browser | Web Crypto API + password-derived key | User-controlled |

### 9.3 Sync Protocol

- mTLS for device-to-cloud communication
- Signed event batches prevent tampering in transit
- Cloud validates signature chain before accepting sync

### 9.4 Device Genesis (Provisioning)

The device enrollment lifecycle establishes trust before any transactions occur:

1. **Factory Mode:** Device boots for first time, generates asymmetric key pair
2. **Enrollment Request:** Device sends CSR (Certificate Signing Request) to Cloud provisioning
   endpoint, including hardware serial number
3. **Attestation:** Cloud verifies device hardware ID against manufacturing records
4. **Certificate Issuance:** Cloud issues a "Ledger Certificate" binding the device's public key
   to specific authorized accounts
5. **Genesis Sync:** Device receives its initial state (tariffs, account structure, reference data)
6. **Operational Mode:** Device can now sign valid transactions for its authorized scope

**Revocation:** If a device is compromised, the Cloud revokes the Ledger Certificate. The device
can still operate offline, but its signed events will be rejected on next sync attempt.

### 9.5 Trusted Time Enforcement

Financial ledgers require monotonic, trustworthy time. Raspberry Pi Zero lacks an RTC battery -
after power loss without internet, it defaults to epoch (1970-01-01).

**Requirements:**

- **Boot Check:** Service refuses to record financial transactions until time sync is confirmed
  (NTP, cellular, or GPS)
- **Drift Detection:** If internal clock detects significant drift vs. Cloud timestamp in MQTT
  heartbeat (> 30 seconds), suspend transaction recording and alert
- **Monotonic Guard:** Reject any transaction with timestamp earlier than the last recorded
  transaction (prevents clock rollback attacks)
- **Hardware RTC:** Recommended for critical metering applications (DS3231 module, ~$2)

**Degraded Mode:** If time cannot be verified, device continues measuring (kWh counter) but
defers valuation until time sync is restored. Measurements are tagged as "pending-valuation".

## 10. Success Metrics

| Metric | Target | Measurement |
|--------|--------|-------------|
| Logic Parity | 100% | Same integration tests pass on all targets |
| Offline Duration | Unlimited | Device operates indefinitely without cloud |
| Sync Latency | < 5 seconds | Time from connectivity to sync complete |
| Binary Size | < 15MB | `ls -la meridian-edge` |
| Memory Usage | < 50MB | Prometheus metrics under load test |

## 11. Related Documentation

- [ADR-0002: Microservices per BIAN Domain](../adr/0002-microservices-per-bian-domain.md) -
  Service boundaries that we're collapsing
- [ADR-0005: Adapter Pattern for Layer Translation](../adr/0005-adapter-pattern-layer-translation.md) -
  The pattern that enables this swap
- [Universal Asset System PRD](001-universal-asset-system.md) - Multi-asset support that Edge inherits

## 12. Conclusion

Meridian Edge validates the core architecture. It proves that by adhering to **BIAN** and
**Hexagonal** patterns in **Go**, we can deploy the same financial rigor to a Bank's Data Center
and a Refugee Camp's Solar Battery.

It transforms the product from "Accounting Software" into a **Universal Value Protocol**.

> *"One codebase, every scale. The ledger is fractal: self-similar regardless of where it runs."*
