# Market Data & Dynamic Pricing PRD

**Status:** Draft
**Owner:** Platform Team
**Last Updated:** 2026-02-09

## Executive Summary

This PRD defines the capabilities required to transform Meridian from a position-keeping engine into a **closed-loop dynamic pricing platform**. By extending the existing `utilization-metering-consumer` to publish usage patterns as market data, and introducing a new **Forecasting Service** with pluggable Starlark-based algorithms, Meridian enables tenants to implement sophisticated time-of-use tariffs and demand shaping strategies.

### The Metronome Gap

Metronome (Stripe's $1B acquisition) demonstrates market demand for usage-based billing, but has fundamental limitations:
- **34-day correction window** — cannot reprocess older billing periods
- **No bi-temporal support** — cannot model "as-at" vs "as-of" distinctions
- **Pre-loaded rate cards** — pricing logic evaluated at ingest time, not settlement
- **Webhooks only** — no durable saga patterns for complex billing orchestration
- **No forward curves** — purely reactive to historical usage

Meridian's existing capabilities (bi-temporal positions, wash-and-reload settlement, CEL runtime evaluation, durable sagas) already surpass Metronome. This PRD adds the final piece: **forward-looking market data and algorithmic pricing**.

## Problem Statement

Modern pricing scenarios require forward-looking price signals:

1. **AI Compute Infrastructure**: Data center operators need to price GPU time based on predicted demand, incentivizing off-peak usage to maximize utilization
2. **Energy Markets**: Utilities publish day-ahead prices so consumers can shift load (EV charging, industrial processes) to lower-cost periods
3. **Telecommunications**: 5G slicing requires dynamic bandwidth pricing based on network congestion forecasts
4. **Financial Services**: FX forwards, interest rate curves, commodity futures all require forward curve modeling

The common pattern: **publish future prices to change behavior, not just react to historical usage**.

Meridian already collects the raw signal (usage via `utilization-metering-consumer`) but doesn't expose it as market data for tenant-level dynamic pricing.

## Goals

| Goal | Success Metric |
|------|---------------|
| Enable closed-loop demand shaping | Tenants can publish forward curves that influence customer behavior |
| Support pluggable forecasting algorithms | At least 3 algorithm types deployable via Starlark |
| Hierarchical reference data | Tenants can model capacity at any granularity (region → zone → rack) |
| External forecast ingestion | Tenants can import third-party forecasts (weather, demand, market) |
| Bi-temporal forecast tracking | Full audit trail of forecast vs actual for model improvement |

## Non-Goals

- Building a general-purpose time-series database (use existing Market Information Management service)
- Real-time streaming analytics (batch forecasting is sufficient for day-ahead markets)
- Replacing tenant-specific pricing engines (we provide curves; they apply business logic)

---

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         Closed-Loop Dynamic Pricing                          │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│   ┌──────────────┐     ┌──────────────────┐     ┌────────────────────┐      │
│   │   Tenant     │────▶│  Meridian API    │────▶│  Position Keeper   │      │
│   │  Customers   │     │  Gateway         │     │  (Usage Recording) │      │
│   └──────────────┘     └──────────────────┘     └─────────┬──────────┘      │
│          ▲                                                 │                 │
│          │                                                 ▼                 │
│          │                                    ┌────────────────────────┐     │
│          │                                    │ Utilization Metering   │     │
│          │                                    │ Consumer (Extended)    │     │
│          │                                    └─────────┬──────────────┘     │
│          │                                              │                    │
│          │                         ┌────────────────────┼────────────────┐   │
│          │                         ▼                    ▼                │   │
│          │            ┌──────────────────┐   ┌───────────────────┐      │   │
│          │            │  Market Data     │   │  Reference Data   │      │   │
│          │            │  Service         │   │  Service          │      │   │
│          │            │  (Bi-Temporal)   │   │  (Hierarchical)   │      │   │
│          │            └────────┬─────────┘   └─────────┬─────────┘      │   │
│          │                     │                       │                │   │
│          │                     └───────────┬───────────┘                │   │
│          │                                 ▼                            │   │
│          │                    ┌────────────────────────┐                │   │
│          │                    │   Forecasting Service  │                │   │
│          │                    │   (Starlark Algorithms)│                │   │
│          │                    └─────────┬──────────────┘                │   │
│          │                              │                               │   │
│          │                              ▼                               │   │
│          │                    ┌────────────────────────┐                │   │
│          │                    │   Forward Curves       │                │   │
│          │                    │   (Published to MDS)   │                │   │
│          │                    └─────────┬──────────────┘                │   │
│          │                              │                               │   │
│          └──────────────────────────────┘                               │   │
│                     (Prices influence behavior)                          │   │
│                                                                          │   │
└──────────────────────────────────────────────────────────────────────────────┘
```

---

## Work Streams

### WS-1: Utilization Metering Extension (P0)

**Objective:** Extend `utilization-metering-consumer` to publish aggregated usage patterns as market data observations.

#### Current State

The `utilization-metering-consumer` currently:
- Consumes `BillingEvent` messages from Kafka
- Aggregates usage for Meridian's own billing (tenant → Meridian)
- Does not expose data for tenant-level analysis

#### Target State

The consumer will additionally:
- Publish usage aggregates to Market Information Management service
- Support configurable aggregation windows (hourly, daily)
- Enable per-tenant market data isolation
- Track usage by hierarchical reference data keys

#### Key Changes

| Component | Change |
|-----------|--------|
| `utilization-metering-consumer` | Add `MarketDataPublisher` output adapter |
| Market Data observation types | New `UTILIZATION_AGGREGATE` observation type |
| Resolution keys | Support hierarchical keys: `{tenant}/{region}/{zone}/{resource}` |

#### API Extensions

```protobuf
message UtilizationAggregate {
  string tenant_id = 1;
  string resolution_key = 2;  // Hierarchical: "region/zone/rack"
  google.protobuf.Timestamp window_start = 3;
  google.protobuf.Timestamp window_end = 4;

  // Aggregate metrics
  double total_units = 5;
  double peak_units = 6;
  double avg_units = 7;
  int64 observation_count = 8;

  // Quality indicator
  ObservationQuality quality = 9;  // ESTIMATE, ACTUAL, VERIFIED
}
```

#### Tasks

| ID | Task | Points | Dependencies |
|----|------|--------|--------------|
| UM-1 | Add `MarketDataPublisher` interface to metering consumer | 3 | - |
| UM-2 | Implement aggregation window configuration (hourly/daily) | 2 | UM-1 |
| UM-3 | Add hierarchical resolution key parsing | 2 | UM-1 |
| UM-4 | Create `UTILIZATION_AGGREGATE` observation type in MDS | 3 | - |
| UM-5 | Implement tenant isolation for published market data | 3 | UM-4 |
| UM-6 | Add quality ladder support (estimate → actual → verified) | 2 | UM-4 |
| UM-7 | Integration tests: metering → market data flow | 3 | UM-1, UM-4 |

**WS-1 Total:** 18 points

---

### WS-2: Hierarchical Reference Data (P0)

**Objective:** Enable tenants to define arbitrary hierarchical reference data structures with bi-temporal validity.

#### Design Principles

Reference data must be:
1. **Generic** — No rigid schemas; tenants define their own structures (like asset types)
2. **Hierarchical** — Natural tree structures: DNO → GSP → Meter; Region → Zone → Rack
3. **Bi-temporal** — Valid-time and transaction-time tracking for auditable seed data
4. **Tenant-scoped** — Full isolation between tenants

#### Data Model

```protobuf
message ReferenceDataNode {
  string id = 1;
  string tenant_id = 2;
  string type = 3;              // e.g., "region", "zone", "gsp", "meter"
  string parent_id = 4;         // Hierarchical link (nullable for roots)

  // Flexible attributes (like asset metadata)
  google.protobuf.Struct attributes = 5;

  // Bi-temporal validity
  TimeRange valid_time = 6;
  google.protobuf.Timestamp transaction_time = 7;  // System-managed

  // Resolution key for market data correlation
  string resolution_key = 8;    // Computed: "parent_key/type:id"
}

message TimeRange {
  google.protobuf.Timestamp from = 1;  // Inclusive
  google.protobuf.Timestamp to = 2;    // Exclusive, null = unbounded
}
```

#### Example: Energy Grid Hierarchy

```json
{
  "id": "dno-001",
  "type": "dno",
  "parent_id": null,
  "attributes": {"name": "Western Power Distribution", "region": "South West"},
  "resolution_key": "dno:dno-001"
}

{
  "id": "gsp-exeter",
  "type": "gsp",
  "parent_id": "dno-001",
  "attributes": {"name": "Exeter GSP", "capacity_mw": 450},
  "resolution_key": "dno:dno-001/gsp:gsp-exeter"
}

{
  "id": "meter-12345",
  "type": "meter",
  "parent_id": "gsp-exeter",
  "attributes": {"mpan": "12345678901234", "profile_class": 1},
  "resolution_key": "dno:dno-001/gsp:gsp-exeter/meter:meter-12345"
}
```

#### Example: AI Compute Hierarchy

```json
{
  "id": "us-east-1",
  "type": "region",
  "parent_id": null,
  "attributes": {"provider": "aws", "tier": "primary"},
  "resolution_key": "region:us-east-1"
}

{
  "id": "us-east-1a",
  "type": "zone",
  "parent_id": "us-east-1",
  "attributes": {"gpu_types": ["A100", "H100"]},
  "resolution_key": "region:us-east-1/zone:us-east-1a"
}

{
  "id": "rack-gpu-42",
  "type": "rack",
  "parent_id": "us-east-1a",
  "attributes": {"gpu_count": 64, "cooling": "liquid"},
  "resolution_key": "region:us-east-1/zone:us-east-1a/rack:rack-gpu-42"
}
```

#### RPCs

```protobuf
service ReferenceDataService {
  // CRUD operations
  rpc CreateNode(CreateNodeRequest) returns (ReferenceDataNode);
  rpc UpdateNode(UpdateNodeRequest) returns (ReferenceDataNode);
  rpc GetNode(GetNodeRequest) returns (ReferenceDataNode);

  // Hierarchy traversal
  rpc GetChildren(GetChildrenRequest) returns (GetChildrenResponse);
  rpc GetAncestors(GetAncestorsRequest) returns (GetAncestorsResponse);
  rpc GetSubtree(GetSubtreeRequest) returns (GetSubtreeResponse);

  // Bi-temporal queries
  rpc GetNodeAsAt(GetNodeAsAtRequest) returns (ReferenceDataNode);
  rpc GetNodeHistory(GetNodeHistoryRequest) returns (GetNodeHistoryResponse);

  // Bulk operations for seeding
  rpc ImportNodes(stream ReferenceDataNode) returns (ImportNodesResponse);
}
```

#### Tasks

| ID | Task | Points | Dependencies |
|----|------|--------|--------------|
| RD-1 | Design reference data schema with bi-temporal support | 3 | - |
| RD-2 | Implement `ReferenceDataNode` entity and repository | 5 | RD-1 |
| RD-3 | Add hierarchical resolution key computation | 2 | RD-2 |
| RD-4 | Implement bi-temporal query methods (as-at, history) | 5 | RD-2 |
| RD-5 | Create gRPC service with CRUD operations | 3 | RD-2 |
| RD-6 | Add hierarchy traversal RPCs (children, ancestors, subtree) | 3 | RD-5 |
| RD-7 | Implement bulk import for seeding | 3 | RD-5 |
| RD-8 | Add tenant isolation and authorization | 2 | RD-5 |
| RD-9 | Integration tests: hierarchy + bi-temporal queries | 3 | RD-4, RD-6 |

**WS-2 Total:** 29 points

---

### WS-3: Forecasting Service (P1)

**Objective:** Create a new service that computes forward curves using pluggable Starlark-based algorithms.

#### Core Concepts

| Concept | Description |
|---------|-------------|
| **Forecasting Strategy** | A named Starlark script that computes future values from historical data |
| **Forward Curve** | A series of future prices/values published to Market Data Service |
| **Forecast Horizon** | How far into the future the curve extends (e.g., 24 hours, 7 days) |
| **Computation Schedule** | When forecasts are regenerated (e.g., hourly, daily at 16:00) |

#### Forecasting Strategy Definition

```protobuf
message ForecastingStrategy {
  string id = 1;
  string tenant_id = 2;
  string name = 3;
  string description = 4;

  // The algorithm
  string starlark_script = 5;

  // Input configuration
  repeated string input_resolution_keys = 6;  // Market data to read
  string input_observation_type = 7;          // e.g., "UTILIZATION_AGGREGATE"
  Duration lookback_window = 8;               // Historical data window

  // Output configuration
  string output_resolution_key = 9;           // Where to publish curve
  string output_observation_type = 10;        // e.g., "FORWARD_PRICE"
  Duration forecast_horizon = 11;             // How far to forecast
  Duration granularity = 12;                  // Point spacing (e.g., 1 hour)

  // Scheduling
  string cron_schedule = 13;                  // When to compute

  // Bi-temporal tracking
  google.protobuf.Timestamp created_at = 14;
  google.protobuf.Timestamp updated_at = 15;
}
```

#### Starlark Algorithm Interface

Strategies receive a standardized context and must return a list of forecast points:

```python
# Available in Starlark context:
# - observations: list of historical observations
# - reference_data: hierarchical reference data for resolution key
# - horizon: forecast horizon in hours
# - granularity: point spacing in hours
# - now: current timestamp

def compute_forecast(ctx):
    """
    Simple moving average with capacity constraint.
    """
    observations = ctx.observations
    reference_data = ctx.reference_data
    horizon = ctx.horizon
    granularity = ctx.granularity

    # Calculate historical average by hour-of-day
    hourly_avgs = {}
    for obs in observations:
        hour = obs.timestamp.hour
        if hour not in hourly_avgs:
            hourly_avgs[hour] = []
        hourly_avgs[hour].append(obs.value)

    for hour in hourly_avgs:
        hourly_avgs[hour] = sum(hourly_avgs[hour]) / len(hourly_avgs[hour])

    # Get capacity constraint from reference data
    capacity = reference_data.attributes.get("capacity", 100.0)

    # Generate forecast points
    points = []
    for i in range(0, horizon, granularity):
        forecast_time = ctx.now + duration(hours=i)
        hour = forecast_time.hour
        base_value = hourly_avgs.get(hour, 0.0)

        # Apply capacity-based pricing
        utilization = base_value / capacity
        if utilization > 0.8:
            price_multiplier = 1.5  # Peak pricing
        elif utilization > 0.5:
            price_multiplier = 1.0  # Standard pricing
        else:
            price_multiplier = 0.7  # Off-peak pricing

        points.append({
            "timestamp": forecast_time,
            "value": price_multiplier,
            "metadata": {
                "expected_utilization": utilization,
                "pricing_tier": "peak" if utilization > 0.8 else "standard" if utilization > 0.5 else "off_peak"
            }
        })

    return points
```

#### Built-in Algorithm Templates

| Template | Description |
|----------|-------------|
| `moving_average` | Simple/exponential moving average projection |
| `seasonal_decomposition` | Hour-of-day, day-of-week patterns |
| `capacity_pricing` | Price curves based on utilization vs capacity |
| `external_blend` | Blend internal forecasts with external feeds |

#### External Forecast Ingestion

Tenants can import third-party forecasts (weather, market prices, demand forecasts):

```protobuf
message IngestExternalForecastRequest {
  string tenant_id = 1;
  string resolution_key = 2;
  string observation_type = 3;
  string source = 4;  // e.g., "met_office", "epex_spot"

  repeated ForecastPoint points = 5;
}

message ForecastPoint {
  google.protobuf.Timestamp timestamp = 1;
  double value = 2;
  google.protobuf.Struct metadata = 3;
  ObservationQuality quality = 4;
}
```

#### RPCs

```protobuf
service ForecastingService {
  // Strategy management
  rpc CreateStrategy(CreateStrategyRequest) returns (ForecastingStrategy);
  rpc UpdateStrategy(UpdateStrategyRequest) returns (ForecastingStrategy);
  rpc GetStrategy(GetStrategyRequest) returns (ForecastingStrategy);
  rpc ListStrategies(ListStrategiesRequest) returns (ListStrategiesResponse);
  rpc DeleteStrategy(DeleteStrategyRequest) returns (google.protobuf.Empty);

  // Validation (AI-native feedback)
  rpc ValidateStrategy(ValidateStrategyRequest) returns (ValidateStrategyResponse);

  // Manual execution
  rpc ComputeForwardCurve(ComputeForwardCurveRequest) returns (ComputeForwardCurveResponse);

  // External data ingestion
  rpc IngestExternalForecast(IngestExternalForecastRequest) returns (IngestExternalForecastResponse);

  // Forecast vs actual comparison (for model tuning)
  rpc GetForecastAccuracy(GetForecastAccuracyRequest) returns (GetForecastAccuracyResponse);
}
```

#### AI-Native Validation Feedback

Strategy validation returns structured errors for LLM self-correction:

```protobuf
message ValidateStrategyResponse {
  bool valid = 1;
  repeated ValidationError errors = 2;

  // For AI correction
  repeated string available_context_fields = 3;  // ["observations", "reference_data", ...]
  repeated string available_functions = 4;       // ["duration", "sum", "avg", ...]
}

message ValidationError {
  int32 line = 1;
  int32 column = 2;
  string message = 3;
  string suggestion = 4;  // AI-friendly fix suggestion
}
```

#### Tasks

| ID | Task | Points | Dependencies |
|----|------|--------|--------------|
| FS-1 | Design Forecasting Service domain model | 3 | - |
| FS-2 | Implement `ForecastingStrategy` entity and repository | 5 | FS-1 |
| FS-3 | Create Starlark execution sandbox with context injection | 5 | FS-2 |
| FS-4 | Implement built-in algorithm templates | 5 | FS-3 |
| FS-5 | Add strategy validation with AI-native feedback | 3 | FS-3 |
| FS-6 | Create gRPC service with strategy CRUD | 3 | FS-2 |
| FS-7 | Implement `ComputeForwardCurve` RPC | 5 | FS-3, WS-1, WS-2 |
| FS-8 | Add scheduled computation via cron | 3 | FS-7 |
| FS-9 | Implement external forecast ingestion | 3 | FS-6 |
| FS-10 | Add forecast vs actual accuracy tracking | 5 | FS-7 |
| FS-11 | Publish forward curves to Market Data Service | 3 | FS-7 |
| FS-12 | Integration tests: end-to-end forecasting flow | 5 | FS-7, FS-11 |

**WS-3 Total:** 48 points

---

### WS-4: Forward Curve Consumption (P1)

**Objective:** Enable tenants to consume forward curves in their pricing logic via CEL expressions.

#### Market Data Service Extensions

```protobuf
// New observation type
enum ObservationType {
  // ... existing types ...
  FORWARD_PRICE = 10;
  FORWARD_UTILIZATION = 11;
}

// Query forward curves
message GetForwardCurveRequest {
  string tenant_id = 1;
  string resolution_key = 2;
  google.protobuf.Timestamp as_of = 3;      // Which forecast vintage
  google.protobuf.Timestamp from = 4;        // Curve start
  google.protobuf.Timestamp to = 5;          // Curve end
}

message GetForwardCurveResponse {
  repeated ForwardCurvePoint points = 1;
  google.protobuf.Timestamp computed_at = 2;
  string strategy_id = 3;
}
```

#### CEL Extension Functions

Expose forward curves in CEL for real-time pricing decisions:

```cel
// Get forward price for a specific timestamp
forward_price("region:us-east-1/zone:us-east-1a", timestamp("2026-02-10T14:00:00Z"))

// Get pricing tier for next hour
forward_metadata("region:us-east-1", now() + duration("1h")).pricing_tier

// Calculate average forward price over a window
avg_forward_price("gsp:exeter", now(), now() + duration("24h"))
```

#### Tasks

| ID | Task | Points | Dependencies |
|----|------|--------|--------------|
| FC-1 | Add `FORWARD_PRICE` observation type to Market Data Service | 2 | WS-3 |
| FC-2 | Implement `GetForwardCurve` RPC | 3 | FC-1 |
| FC-3 | Create CEL extension functions for forward curve access | 5 | FC-2 |
| FC-4 | Add forward curve caching for CEL performance | 3 | FC-3 |
| FC-5 | Documentation: using forward curves in pricing rules | 2 | FC-3 |
| FC-6 | Integration tests: CEL + forward curves | 3 | FC-3 |

**WS-4 Total:** 18 points

---

## Implementation Sequence

### Phase 1: Foundation (Weeks 1-3)

Focus: Enable usage data to flow into market data system.

| Week | Work Stream | Tasks | Points |
|------|-------------|-------|--------|
| 1 | WS-1 | UM-1, UM-2, UM-3, UM-4 | 10 |
| 2 | WS-1, WS-2 | UM-5, UM-6, UM-7, RD-1, RD-2 | 13 |
| 3 | WS-2 | RD-3, RD-4, RD-5, RD-6 | 13 |

**Phase 1 Total:** 36 points

### Phase 2: Forecasting Core (Weeks 4-6)

Focus: Build the Forecasting Service with Starlark algorithms.

| Week | Work Stream | Tasks | Points |
|------|-------------|-------|--------|
| 4 | WS-2, WS-3 | RD-7, RD-8, RD-9, FS-1, FS-2 | 16 |
| 5 | WS-3 | FS-3, FS-4, FS-5 | 13 |
| 6 | WS-3 | FS-6, FS-7, FS-8 | 11 |

**Phase 2 Total:** 40 points

### Phase 3: Integration (Weeks 7-8)

Focus: Complete forecasting capabilities and enable consumption.

| Week | Work Stream | Tasks | Points |
|------|-------------|-------|--------|
| 7 | WS-3, WS-4 | FS-9, FS-10, FS-11, FC-1, FC-2 | 16 |
| 8 | WS-3, WS-4 | FS-12, FC-3, FC-4, FC-5, FC-6 | 18 |

**Phase 3 Total:** 34 points

---

## Total Effort

| Work Stream | Points | Priority |
|-------------|--------|----------|
| WS-1: Utilization Metering Extension | 18 | P0 |
| WS-2: Hierarchical Reference Data | 29 | P0 |
| WS-3: Forecasting Service | 48 | P1 |
| WS-4: Forward Curve Consumption | 18 | P1 |
| **Total** | **113** | |

---

## Success Criteria

### Minimum Viable Product (P0 Complete)

1. Usage data from `utilization-metering-consumer` flows to Market Data Service
2. Tenants can define hierarchical reference data (e.g., region → zone → rack)
3. Reference data supports bi-temporal queries
4. Market data observations correlate with reference data resolution keys

### Full Feature Set (P1 Complete)

1. Tenants can define Starlark forecasting strategies
2. Forward curves are computed on schedule and published to Market Data Service
3. CEL expressions can access forward prices for real-time pricing decisions
4. External forecasts can be ingested and blended with internal predictions
5. Forecast vs actual accuracy is tracked for model improvement

---

## Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Starlark sandbox escape | High | Use go.starlark.net with restricted globals |
| Forecast computation latency | Medium | Async computation with caching |
| Reference data explosion | Medium | Pagination and efficient indexing |
| External forecast quality | Low | Quality ladder (ESTIMATE < EXTERNAL < VERIFIED) |

---

## Dependencies

| Dependency | Status | Notes |
|------------|--------|-------|
| Market Information Management Service | Implemented | 17/18 tasks complete |
| Kafka infrastructure | Implemented | Used by metering consumer |
| Starlark runtime | Available | go.starlark.net library |
| CEL runtime | Implemented | Used across multiple services |

---

## Open Questions

1. **Forecast granularity limits**: Should we enforce minimum/maximum granularity for forward curves?
2. **Multi-tenant forecast sharing**: Can tenants opt to share anonymized forecasts for collective intelligence?
3. **Forecast versioning**: How do we handle strategy updates mid-forecast-period?

---

## Appendix: Closed-Loop Example

### AI Compute Dynamic Pricing

```
Day 1, 16:00 - Forecast Computation
├── Input: Last 7 days of GPU utilization by zone
├── Reference: Zone capacity constraints
├── Algorithm: capacity_pricing with seasonal_decomposition
└── Output: 24-hour forward curve for each zone

Day 2, 00:00-23:59 - Customer Behavior
├── Customer A checks forward prices, sees 2am-6am is 0.7x
├── Schedules batch training job for 3am
├── Customer B sees 14:00-18:00 is 1.5x (peak)
└── Delays non-urgent inference to evening

Day 2, 16:00 - Cycle Repeats
├── Usage patterns shifted due to price signals
├── Peak utilization reduced by 15%
├── New forecast reflects changed behavior
└── Prices adjust to maintain target utilization
```

This closed-loop system transforms Meridian from a passive record-keeper into an **active demand-shaping platform**.
