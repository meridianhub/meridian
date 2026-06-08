---
name: adr-027-market-information-management
description: Bi-temporal market data service with quality ladder, CEL validation, and supersession chains for reliable price data
triggers:
  - Designing market data or reference price services
  - Implementing bi-temporal data patterns
  - Building data quality hierarchies (estimates vs actuals)
  - Creating configurable validation with CEL expressions
  - Tracking data lineage through supersession
instructions: |
  Market Information Management provides bi-temporal storage for market data with
  quality ladder (ESTIMATE < ACTUAL < VERIFIED). Use CEL expressions for validation
  and resolution key generation. Higher quality observations automatically supersede
  lower quality ones. Trust levels (0-100) from data sources resolve ties.
---

# 27. Market Information Management Service Architecture

Date: 2026-01-19

## Status

Accepted

## Context

Meridian requires a centralized service for managing market data, reference prices, and rate information. This data is critical for:

- **Energy pricing**: Spot prices, tariff rates, carbon credit prices
- **Foreign exchange**: Currency pair rates from sources like ECB
- **Weather derivatives**: Temperature observations for hedging
- **General reference data**: Benchmark rates, indices, and contextual information

The service aligns with the **BIAN Market Information Management** service domain, which "supports the distribution and management of market pricing and information, including price benchmarks, indices, and reference data."

### Key Challenges

| Challenge | Impact |
|-----------|--------|
| **Estimates vs Actuals reconciliation** | Grid operators submit estimates, then actuals arrive later - must not lose either |
| **Late-arriving data corrections** | Verified data may arrive days/weeks after initial observation |
| **Multi-source data** | Same price from Bloomberg, ECB, manual entry - need conflict resolution |
| **Time-travel queries** | "What did we know about EUR/USD at midnight on Jan 15th?" for audits |
| **High-volume ingestion** | Energy grids generate millions of half-hourly data points |
| **Configurable validation** | Different datasets have different validation rules |

### Why Not Use Existing Services?

Position Keeping and Financial Accounting handle transactional data, not reference/market data:

- **Position Keeping**: Transaction logs with debit/credit entries - wrong model for price observations
- **Financial Accounting**: Double-entry bookkeeping - market data has no "balanced entry" concept

Market data is fundamentally different: it represents **observed facts about the external world** rather than **internal business transactions**.

## Decision Drivers

* BIAN compliance for Market Information Management domain
* Bi-temporal queries for regulatory audit and reconciliation
* Quality-based automatic supersession (estimates replaced by actuals)
* Configurable validation without code deployment
* Multi-tenant data isolation
* High-throughput batch ingestion (target: 100k TPS as per ADR-0026)
* Forward-compatible with time-series optimizations

## Decision Outcome

Implement a dedicated **Market Information Management** service with:

1. **Bi-temporal data model** (observed time + knowledge time)
2. **Quality ladder** for automatic supersession
3. **CEL expressions** for configurable validation and resolution keys
4. **Supersession chains** for data lineage tracking
5. **Trust levels** for source-based conflict resolution

### Core Domain Model

#### MarketPriceObservation (Immutable Aggregate)

```text
MarketPriceObservation
├── id: UUID (primary key)
├── dataSetCode: string (e.g., "FX_RATE", "ENERGY_SPOT")
├── sourceID: UUID (reference to DataSource)
├── resolutionKey: string (computed via CEL, e.g., "EUR/USD")
├── value: Decimal (high-precision numeric value)
├── unit: string (e.g., "USD", "kWh")
│
├── Bi-Temporal Fields:
│   ├── observedAt: timestamp (when measurement was taken)
│   ├── validFrom: timestamp (effective start)
│   ├── validTo: timestamp (effective end)
│   └── createdAt: timestamp (when we learned about it)
│
├── Quality & Trust:
│   ├── qualityLevel: enum (ESTIMATE=1, ACTUAL=2, VERIFIED=3)
│   └── trustLevel: int (0-100, from DataSource)
│
└── Lineage:
    ├── supersededAt: timestamp (when replaced, nullable)
    ├── supersededBy: UUID (forward reference, nullable)
    └── causationID: UUID (event sourcing correlation)
```

**Immutability**: Observations are append-only. The `Supersede()` method returns a new instance with `supersededAt` and `supersededBy` set. The original observation remains unchanged in the database for full audit trail.

#### DataSetDefinition (Configuration Aggregate)

```text
DataSetDefinition
├── id: UUID
├── code: string (unique, e.g., "FX_RATE")
├── version: int (optimistic locking)
├── name: string
├── description: string
├── dataCategory: enum (PRICING, CONTEXTUAL)
│
├── CEL Expressions:
│   ├── validationExpression: string (e.g., "decimal(value) > 0")
│   ├── resolutionKeyExpression: string (e.g., "observation_context.base + '/' + observation_context.quote")
│   └── errorMessageExpression: string (custom error messages)
│
└── Lifecycle:
    ├── status: enum (DRAFT, ACTIVE, DEPRECATED)
    ├── activatedAt: timestamp
    └── deprecatedAt: timestamp
```

**Lifecycle Rules**:
- CEL expressions become immutable when status transitions from DRAFT to ACTIVE
- Prevents breaking existing data by changing validation rules retroactively
- Create new version to change validation rules

#### DataSource (Entity)

```text
DataSource
├── id: UUID
├── code: string (unique, e.g., "BLOOMBERG", "ECB_DAILY")
├── name: string
├── sourceType: enum (API, MANUAL, SCHEDULED)
├── trustLevel: int (0-100)
└── isActive: bool
```

### Bi-Temporal Data Model

The service implements **bi-temporal modeling** to answer two distinct questions:

| Dimension | Question | Field |
|-----------|----------|-------|
| **Valid Time** | "When did this rate apply?" | `validFrom`, `validTo` |
| **Transaction Time** | "When did we learn about it?" | `createdAt`, `supersededAt` |
| **Event Time** | "When was measurement taken?" | `observedAt` |

**Query Examples**:

```sql
-- Current knowledge: What's the latest EUR/USD rate?
SELECT * FROM market_price_observation
WHERE resolution_key = 'EUR/USD'
  AND superseded_by IS NULL
ORDER BY quality DESC, observed_at DESC, created_at DESC
LIMIT 1;

-- Historical knowledge: What EUR/USD rate did we know at midnight Jan 15?
SELECT * FROM market_price_observation
WHERE resolution_key = 'EUR/USD'
  AND created_at <= '2026-01-15 00:00:00'
  AND (superseded_at IS NULL OR superseded_at > '2026-01-15 00:00:00')
ORDER BY quality DESC, observed_at DESC, created_at DESC
LIMIT 1;

-- Effective time: What rate was valid on Jan 15 (regardless of when we knew)?
SELECT * FROM market_price_observation
WHERE resolution_key = 'EUR/USD'
  AND valid_from <= '2026-01-15'
  AND valid_to > '2026-01-15'
  AND superseded_by IS NULL
ORDER BY quality DESC, observed_at DESC
LIMIT 1;
```

### Quality Ladder

> **Canonical model:** ADR-0017 defines the authoritative provenance model. The
> single ladder shown here is the coarse confidence axis (Axis A) of the
> **Two-Axis Provenance Model**. The canonical Axis A enum is 4-level -
> `ESTIMATE(1) -> PROVISIONAL(2) -> ACTUAL(3) -> VERIFIED(4)` - and revision
> state is a separate axis (the `supersededBy` pointer + `revision` counter),
> not a ladder rung. The 3-level enum below reflects the market-information
> domain as currently shipped; it aligns with Axis A once `PROVISIONAL` is
> added and the integers are renumbered to match the proto wire numbers. See
> ADR-0017 for the precedence key (`QualityScore`) and the full rationale.

The quality ladder implements **automatic supersession** based on data reliability:

```text
Quality Ladder (Higher supersedes Lower)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  3 │ VERIFIED  │ Cross-checked, audited values
  2 │ ACTUAL    │ Real measured values from sources
  1 │ ESTIMATE  │ Forecasted/projected values
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
```

**Supersession Rules**:

1. Higher quality always supersedes lower quality for the same resolution key and time period
2. Within same quality level, trust level (from DataSource) breaks ties
3. Within same trust level, later `createdAt` wins (latest knowledge)
4. Superseded observations are never deleted - maintain full lineage

**Example: Estimates vs Actuals Reconciliation**

```text
Day 1 09:00: Estimate arrives for 10:00 half-hour period
             → Stored as ESTIMATE (quality=1)

Day 1 11:00: Actual measurement arrives
             → Stored as ACTUAL (quality=2)
             → ESTIMATE automatically superseded

Day 5 14:00: Verified value after reconciliation
             → Stored as VERIFIED (quality=3)
             → ACTUAL automatically superseded
             → Full lineage preserved: ESTIMATE → ACTUAL → VERIFIED
```

### CEL Expression Engine

The service uses **Common Expression Language (CEL)** for configurable business logic:

#### Validation Expressions

```cel
// FX Rate: Must be positive
decimal(observation_context.rate) > 0

// Energy Spot: Non-negative price
decimal(observation_context.price) >= 0

// Temperature: Within physical bounds
decimal(observation_context.temperature_celsius) >= -100 &&
decimal(observation_context.temperature_celsius) <= 100
```

#### Resolution Key Expressions

Resolution keys uniquely identify what the observation is about:

```cel
// FX Rate: Currency pair
observation_context.base_currency + "/" + observation_context.quote_currency
// Result: "EUR/USD"

// Energy Spot: Market/Commodity/Period
observation_context.market + "/" + observation_context.commodity + "/" + observation_context.delivery_period
// Result: "EPEX/ELECTRICITY/2026-01-15T10:00"

// Weather: Station/Date
observation_context.station_code + "/" + string(observation_context.observation_date)
// Result: "LHR/2026-01-15"
```

#### Security Constraints

CEL expressions are sandboxed with limits:

| Constraint | Value | Rationale |
|------------|-------|-----------|
| Max length | 4,096 bytes | Prevent memory exhaustion |
| Max depth | 10 levels | Prevent stack overflow |
| Cost limit | 10,000 | Prevent DoS via expensive evaluation |
| Immutable after activation | Yes | Prevent breaking existing data |

### Database Schema Design

#### Primary Bi-Temporal Index

```sql
-- Critical for point-in-time queries with quality precedence
CREATE INDEX idx_observation_resolution_bitemporal
  ON market_price_observation (
    resolution_key,
    quality DESC,
    observed_at DESC,
    created_at DESC
  )
  WHERE superseded_by IS NULL;
```

**Index strategy**:
- Partial index (`WHERE superseded_by IS NULL`) for current-knowledge queries
- Quality descending ensures higher quality returned first
- Observed/created descending for recency within same quality

#### Dataset Lifecycle Enforcement

> **CockroachDB note:** PL/pgSQL triggers are not supported. Lifecycle enforcement
> is implemented at the Go application layer (DatasetDefinitionRepository).

The repository enforces:
- Prevents modification of CEL expressions once dataset is ACTIVE
- Enforces valid status transitions (DRAFT → ACTIVE → DEPRECATED)
- Sets lifecycle timestamps automatically

### Service Boundaries

#### What This Service OWNS

**Entities:**
- `MarketPriceObservation` - Market data observations
- `DataSetDefinition` - Dataset configurations with CEL expressions
- `DataSource` - External/internal data source definitions

**Operations:**
- Recording single and batch observations
- Bi-temporal queries (current knowledge, historical knowledge, effective time)
- Dataset lifecycle management (DRAFT → ACTIVE → DEPRECATED)
- Resolution key computation via CEL
- Validation via CEL expressions

**Database:**
- `meridian_market_information` database (per ADR-0002 database-per-service)

#### What This Service MUST NOT Do

1. **Transaction processing** - Use Position Keeping for debit/credit entries
2. **Accounting entries** - Use Financial Accounting for double-entry bookkeeping
3. **Account balance computation** - Balances are Position Keeping's domain
4. **ETL from external sources** - Keep ETL off critical path (ADR-0026)

### Event Publishing

Observations are published to Kafka for downstream consumers:

```protobuf
message ObservationRecordedEvent {
  string observation_id = 1;
  string dataset_code = 2;
  string resolution_key = 3;
  string value = 4;
  QualityLevel quality = 5;
  google.protobuf.Timestamp observed_at = 6;
}
```

**Publishing rules**:
- Only publish for ACTUAL and VERIFIED quality levels
- ESTIMATE observations are not published (too noisy, will be superseded)
- Publishing failure does not fail the request (observation already persisted)

## Consequences

### Positive

* **Full audit trail**: Supersession chains preserve complete data lineage for regulatory compliance
* **Estimates vs Actuals solved**: Quality ladder automatically handles the classic reconciliation problem
* **Configurable validation**: CEL expressions allow business rule changes without code deployment
* **Multi-source conflict resolution**: Trust levels provide deterministic winner selection
* **Time-travel queries**: Bi-temporal model enables "what did we know when?" questions
* **BIAN aligned**: Maps directly to Market Information Management domain

### Negative

* **Storage growth**: Never-delete policy means unbounded growth of superseded observations
  - *Mitigation*: Implement archival policy for observations older than retention period
* **Query complexity**: Bi-temporal queries require careful index design
  - *Mitigation*: Pre-built indexes for common query patterns
* **CEL learning curve**: Teams must learn CEL syntax for custom validation
  - *Mitigation*: Provide library of common expressions, comprehensive documentation

### Technical Debt Acknowledged

1. **Cursor pagination not implemented**: `ListObservations` uses offset-based pagination
2. **Dataset version tracking**: Observations don't track which dataset version validated them
3. **Bulk supersession**: No efficient mechanism to supersede many observations atomically

## Implementation Notes

### Seed Data

The migration includes pre-configured datasets for common use cases:

| Code | Purpose | Resolution Key Example |
|------|---------|----------------------|
| `FX_RATE` | Currency exchange rates | `EUR/USD` |
| `ENERGY_SPOT` | Energy spot prices | `EPEX/ELECTRICITY/2026-01-15T10:00` |
| `ENERGY_TARIFF` | Published tariff rates | `ACME_ENERGY/STANDARD/2026-01-01` |
| `CARBON_PRICE` | Carbon credit prices | `EU_ETS/EUA/2025` |
| `WEATHER_TEMP` | Temperature observations | `LHR/2026-01-15` |

### Trust Level Guidelines

| Trust Level | Source Type | Example |
|-------------|-------------|---------|
| 90-100 | Authoritative | ECB Daily Rates, Exchange official prices |
| 70-89 | Reliable third-party | Bloomberg, Reuters |
| 50-69 | Secondary sources | Aggregator APIs, derived data |
| 30-49 | Manual entry | Admin overrides, corrections |
| 0-29 | Low confidence | Estimates, forecasts, unverified |

## Links

* [BIAN Market Information Management](https://bian.org/semantic-apis/market-data/) - BIAN semantic API specification
* [ADR-0002: Microservices Per BIAN Domain](./0002-microservices-per-bian-domain.md) - Service architecture
* [ADR-0026: Canonical Ingestion Contract](./0026-canonical-ingestion-contract.md) - ETL off critical path
* [CEL Specification](https://github.com/google/cel-spec) - Common Expression Language
* [Temporal Data Modeling](https://martinfowler.com/articles/temporal-modeling.html) - Bi-temporal patterns reference
