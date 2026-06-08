---
name: market-information-operations
description: Operational procedures for Market Information Management service including dataset management, observation handling, bi-temporal queries, and troubleshooting
triggers:
  - Troubleshooting Market Information service issues
  - Managing dataset lifecycle (DRAFT -> ACTIVE -> DEPRECATED)
  - Investigating observation ingestion problems
  - Querying bi-temporal data (as-of queries)
  - Configuring data sources and trust levels
  - Debugging CEL validation expression failures
  - Quality ladder supersession issues
instructions: |
  Use this runbook for Market Information Management service operations.
  Port: 50058 (gRPC). Database: market_information.
  Bi-temporal model: observed_at (event time) + created_at (knowledge time).
  Quality ladder: ESTIMATE < PROVISIONAL < ACTUAL < VERIFIED.
  Trust levels: 0-100 (higher = more authoritative source).
---

# Market Information Operations Runbook

**When to use this runbook**: Managing market data sets, troubleshooting observation ingestion,
debugging bi-temporal queries, configuring data sources, or investigating CEL validation failures.

> **Note**: This service implements the BIAN Market Information Management service domain.
> It manages price benchmarks, market data feeds, and reference prices with bi-temporal support.

## Service Overview

| Property | Value |
|----------|-------|
| **Service Name** | market-information |
| **gRPC Port** | 50058 |
| **HTTP Port** | 8082 (`/metrics`, `/health`, `/ready`) |
| **Database** | market_information (schema-per-tenant) |
| **Namespace** | production / staging |
| **Deployment** | market-information |

### Dependencies

| Service | Port | Purpose |
|---------|------|---------|
| Tenant | 50056 | Tenant provisioning and multi-tenancy |
| CockroachDB | 26257 | Persistent storage |
| Kafka | 9092 | Event publishing (optional) |

### Key Concepts

#### Data Categories

| Category | Description | Example |
|----------|-------------|---------|
| FX_RATE | Foreign exchange rates | EUR/USD = 1.0850 |
| INTEREST_RATE | Interest rates (LIBOR, SOFR) | USD-SOFR-1M = 5.33% |
| COMMODITY_PRICE | Commodity prices | BRENT_CRUDE = 78.50 USD/BBL |
| ENERGY_PRICE | Energy prices | ELECTRICITY_UK = 85.20 GBP/MWh |
| CARBON_PRICE | Carbon credit prices | EU_ETS = 62.50 EUR/tCO2 |

#### Quality Ladder

Observations are ranked by quality level (higher takes precedence):

```text
VERIFIED (4)     ← Cross-checked, audited values (highest)
    │
ACTUAL (3)       ← Metered, validated values from data sources
    │
PROVISIONAL (2)  ← Metered but not yet validated
    │
ESTIMATE (1)     ← Forecasted or projected values (lowest)
```

> **Note**: This is Axis A (confidence grade) of the two-axis quality model (ADR-0017). The domain enum
> has four levels (ESTIMATE=1, PROVISIONAL=2, ACTUAL=3, VERIFIED=4). The proto enum slot 4 is still
> spelled `QUALITY_LEVEL_REVISED` but is semantically VERIFIED; the symbol rename is deferred. COEFFICIENT
> is a data source (maps to ESTIMATE), not a level. REVISED (revision>0) is a lifecycle event on Axis B,
> not a confidence tier.

#### Bi-Temporal Model

```text
                    Knowledge Time (created_at)
                    ──────────────────────────────►
                    │
                    │   ┌─────────────────────────┐
                    │   │ Current knowledge state │
Event Time          │   │ (superseded_by IS NULL) │
(observed_at)       │   └─────────────────────────┘
                    │              │
                    ▼              │
                                   ▼
                    ┌─────────────────────────────┐
                    │ Historical knowledge states │
                    │ (superseded_by IS NOT NULL) │
                    └─────────────────────────────┘
```

### Dataset Lifecycle

```text
    ┌──────────┐
    │  DRAFT   │  ← Can edit validation rules
    └────┬─────┘
         │ ACTIVATE
         ▼
    ┌──────────┐
    │  ACTIVE  │  ← Accepts observations
    └────┬─────┘
         │ DEPRECATE
         ▼
    ┌──────────────┐
    │  DEPRECATED  │  ← No new observations
    └──────────────┘
```

---

## Common Operations

### Register a New Dataset

**Use case**: Creating a new market data type definition for FX rates.

```bash
# Using grpcurl
grpcurl -plaintext \
  -d '{
    "code": "USD_EUR_FX",
    "display_name": "USD/EUR Exchange Rate",
    "description": "Daily USD to EUR exchange rate",
    "category": "DATA_CATEGORY_FX_RATE",
    "validation_expression": "parse_decimal(observation_context.rate) > 0",
    "resolution_key_expression": "observation_context.base_currency + \"/\" + observation_context.quote_currency",
    "error_message_expression": "\"Invalid exchange rate: must be positive\""
  }' \
  market-information.production.svc.cluster.local:50058 \
  meridian.market_information.v1.MarketInformationService/RegisterDataSet
```

**Expected response:**

```json
{
  "dataset": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "code": "USD_EUR_FX",
    "status": "DATA_SET_STATUS_DRAFT",
    "version": 1
  }
}
```

### Activate a Dataset

**Use case**: Making a draft dataset available for observations.

```bash
grpcurl -plaintext \
  -d '{"code": "USD_EUR_FX", "version": 1}' \
  market-information.production.svc.cluster.local:50058 \
  meridian.market_information.v1.MarketInformationService/ActivateDataSet
```

### Register a Data Source

**Use case**: Setting up ECB as a trusted data provider.

```bash
grpcurl -plaintext \
  -d '{
    "code": "ECB_DAILY",
    "name": "ECB Daily Reference Rates",
    "description": "European Central Bank daily FX reference rates",
    "trust_level": 90
  }' \
  market-information.production.svc.cluster.local:50058 \
  meridian.market_information.v1.MarketInformationService/RegisterDataSource
```

**Trust level guidelines:**

- 100: Internal admin overrides
- 90: Central banks (ECB, Fed)
- 80: Major data vendors (Reuters, Bloomberg)
- 50: System defaults/fallbacks
- 30: Third-party APIs

### Record an Observation

**Use case**: Ingesting a new FX rate observation.

```bash
grpcurl -plaintext \
  -d '{
    "dataset_code": "USD_EUR_FX",
    "source_code": "ECB_DAILY",
    "observed_at": "2026-01-19T16:00:00Z",
    "quality": "QUALITY_LEVEL_ACTUAL",
    "observation_context": {
      "base_currency": "USD",
      "quote_currency": "EUR",
      "rate": "0.9215"
    },
    "numeric_value": {"value": "0.9215", "scale": 4}
  }' \
  market-information.production.svc.cluster.local:50058 \
  meridian.market_information.v1.MarketInformationService/RecordObservation
```

### Query Best Known Value

**Use case**: Get the best known FX rate for a specific point in time.

```bash
# As-of query: What was the best known EUR/USD rate at 16:00 UTC on Jan 19?
grpcurl -plaintext \
  -d '{
    "dataset_code": "USD_EUR_FX",
    "resolution_key": "USD/EUR",
    "as_of_time": "2026-01-19T16:00:00Z"
  }' \
  market-information.production.svc.cluster.local:50058 \
  meridian.market_information.v1.MarketInformationService/QueryBestKnownValue
```

---

## Troubleshooting

### Observation Rejected: CEL Validation Failed

**Symptoms:**

```text
rpc error: code = InvalidArgument desc = validation failed: rate must be positive
```

**Diagnosis:**

1. Check the dataset's validation expression:

   ```bash
   grpcurl -plaintext \
     -d '{"code": "USD_EUR_FX", "version": 0}' \
     market-information.production.svc.cluster.local:50058 \
     meridian.market_information.v1.MarketInformationService/RetrieveDataSet
   ```

2. Verify observation_context matches expected schema

3. Check for common CEL issues:
   - Missing fields in observation_context
   - Type mismatches (string vs decimal)
   - Null/missing values

**Resolution:**

- Update observation to include required fields
- Ensure numeric values are strings for `parse_decimal()`
- Check dataset schema for required attributes

### Data Source Not Active

**Symptoms:**

```text
rpc error: code = FailedPrecondition desc = data source EXTERNAL_API is not active
```

**Diagnosis:**

```bash
# Check data source status
grpcurl -plaintext \
  market-information.production.svc.cluster.local:50058 \
  meridian.market_information.v1.MarketInformationService/ListDataSources
```

**Resolution:**

- Sources are active by default when created
- Soft-deleted sources cannot be used
- Check if source was recently removed

### Dataset Not Found

**Symptoms:**

```text
rpc error: code = NotFound desc = dataset not found: UNKNOWN_CODE
```

**Diagnosis:**

```bash
# List all datasets
grpcurl -plaintext \
  market-information.production.svc.cluster.local:50058 \
  meridian.market_information.v1.MarketInformationService/ListDataSets
```

**Resolution:**

- Verify dataset code spelling (case-sensitive, uppercase with underscores)
- Check if dataset exists in the correct tenant
- Ensure tenant header is set correctly

### Bi-Temporal Query Returns Unexpected Value

**Symptoms:**
Query returns an older/different value than expected.

**Diagnosis:**

1. Check for superseded observations:

   ```sql
   SELECT id, observed_at, created_at, quality, superseded_by
   FROM market_price_observation
   WHERE resolution_key = 'USD/EUR'
   ORDER BY created_at DESC
   LIMIT 10;
   ```

2. Verify quality ladder precedence:
   - Higher quality supersedes lower quality
   - Same quality: later created_at wins

3. Check as_of_time parameter:
   - Must be >= observation's observed_at
   - Knowledge time (created_at) must be <= query time

**Resolution:**

- Use specific as_of_time for point-in-time queries
- Check supersession chain for data corrections
- Verify source trust levels for cross-source precedence

---

## Database Operations

### Check Observation Count by Dataset

```sql
SELECT
  d.code as dataset_code,
  COUNT(o.id) as observation_count,
  MIN(o.observed_at) as earliest_observation,
  MAX(o.observed_at) as latest_observation
FROM dataset_definition d
LEFT JOIN market_price_observation o ON o.dataset_definition_id = d.id
GROUP BY d.code
ORDER BY observation_count DESC;
```

### Find Supersession Chains

```sql
WITH RECURSIVE chain AS (
  SELECT id, resolution_key, quality, created_at, superseded_by, 1 as depth
  FROM market_price_observation
  WHERE superseded_by IS NULL
    AND resolution_key = 'USD/EUR'

  UNION ALL

  SELECT o.id, o.resolution_key, o.quality, o.created_at, o.superseded_by, c.depth + 1
  FROM market_price_observation o
  JOIN chain c ON o.superseded_by = c.id
)
SELECT * FROM chain ORDER BY depth;
```

### Check Data Source Trust Levels

```sql
SELECT code, name, trust_level, created_at
FROM data_source
WHERE deleted_at IS NULL
ORDER BY trust_level DESC;
```

---

## Monitoring

### Key Metrics

| Metric | Description | Alert Threshold |
|--------|-------------|-----------------|
| `market_information_observations_total` | Total observations recorded | N/A (counter) |
| `market_information_validation_failures_total` | CEL validation failures | > 10/min |
| `market_information_query_latency_seconds` | Query response time | p99 > 500ms |
| `market_information_supersession_rate` | Rate of superseded observations | > 50% |

### Health Check

```bash
# gRPC health check
grpcurl -plaintext \
  market-information.production.svc.cluster.local:50058 \
  grpc.health.v1.Health/Check
```

### Log Queries

```bash
# Find validation errors
kubectl logs -l app=market-information -n production | grep "validation failed"

# Find supersession events
kubectl logs -l app=market-information -n production | grep "observation superseded"

# Find data source issues
kubectl logs -l app=market-information -n production | grep "data source"
```

---

## Disaster Recovery

### Restore Observation from Supersession Chain

If an incorrect observation was recorded:

1. Record a corrected observation (at the appropriate confidence level, e.g. VERIFIED) with the
   correct value; the correction carries revision>0 (Axis B)
2. The system automatically supersedes the incorrect observation
3. Bi-temporal queries will return the corrected value

```bash
grpcurl -plaintext \
  -d '{
    "dataset_code": "USD_EUR_FX",
    "source_code": "INTERNAL_ADMIN",
    "observed_at": "2026-01-19T16:00:00Z",
    "quality": "QUALITY_LEVEL_REVISED",
    "observation_context": {"base_currency": "USD", "quote_currency": "EUR", "rate": "0.9220"},
    "numeric_value": {"value": "0.9220", "scale": 4}
  }' \
  market-information.production.svc.cluster.local:50058 \
  meridian.market_information.v1.MarketInformationService/RecordObservation
```

### Audit Trail Query

Query the full knowledge history for regulatory audit:

```sql
SELECT
  o.id,
  d.code as dataset_code,
  s.code as source_code,
  o.resolution_key,
  o.observed_at,
  o.created_at as knowledge_time,
  o.quality,
  o.numeric_value,
  o.superseded_by
FROM market_price_observation o
JOIN dataset_definition d ON o.dataset_definition_id = d.id
JOIN data_source s ON o.data_source_id = s.id
WHERE o.resolution_key = 'USD/EUR'
ORDER BY o.created_at DESC;
```

---

## Configuration Reference

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `GRPC_PORT` | gRPC server port | 50058 |
| `METRICS_PORT` | HTTP port for `/metrics`, `/health`, `/ready` | 8082 |
| `DATABASE_URL` | CockroachDB connection string | Required |
| `KAFKA_BROKERS` | Kafka broker addresses | Optional |
| `LOG_LEVEL` | Logging level | info |

### Feature Flags

| Flag | Description | Default |
|------|-------------|---------|
| `enable_kafka_events` | Publish observation events to Kafka | false |
| `max_batch_size` | Maximum batch ingestion size | 1000 |
| `cel_cache_size` | CEL expression cache size | 100 |

---

## Related Documentation

- [ADR-0027: Market Information Management Architecture](../adr/0027-market-information-management.md)
- [BIAN Market Information Management Service Domain](https://bian.org/servicelandscape-12-0-0/views/view_51081.html)
- [CEL Expression Language](https://github.com/google/cel-spec)
