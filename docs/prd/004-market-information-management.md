---
name: prd-market-information-management
description: BIAN Market Information Management service for external market data, pricing feeds, and contextual datasets
triggers:
  - Working on market data or pricing feeds
  - Implementing FX rates, energy tariffs, or commodity prices
  - Building valuation or mark-to-market functionality
  - Integrating external data providers
  - Working with weather data or other contextual datasets
  - Configuring tenant-specific data sources
instructions: |
  This PRD defines the Market Information Management service following BIAN v14.0.
  Key patterns: Unified CEL validation (mirrors InstrumentDefinition), loose coupling to Reference Data.
  Uses DataSetDefinition for schema, MarketPriceObservation for data points.
  Supports Time-Bound Quality Ladder (ADR-0017) for source authority.
  Service structure follows ADR-0015. Proto package: market_information (with underscores).
---

# PRD: Market Information Management Service

**Status:** Implemented
**Version:** 1.3
**Date:** 2026-01-17
**Author:** Architecture Team
**Task Master Tag:** `market-information-management` (17/18 tasks done, 1 cancelled)

**Version History:**

- v1.3 (2026-01-17): Added Service Boundaries section defining Canonical Ingestion Contract,
  clarified in-scope vs out-of-scope responsibilities, positioned external adapters as
  reference utilities per BIAN Service Domain encapsulation principle
- v1.2 (2026-01-16): PR review feedback - causation_id in request, UpdateDataSource/
  DeactivateDataSource RPCs, structured batch errors, CEL map conversion docs, bi-temporal index
- v1.1 (2026-01-16): Added bi-temporal integrity (knowledge_base_time), knowledge lineage
  (superseded_by, causation_id), deterministic resolution precedence, Market Data Switch
  (FR-2.8), and technical hardening specifications
- v1.0 (2026-01-16): Initial draft

**ADRs:**

- [0002 - Microservices Per BIAN Domain](../adr/0002-microservices-per-bian-domain.md)
- [0005 - Adapter Pattern for Layer Translation](../adr/0005-adapter-pattern-layer-translation.md)
- [0013 - Universal Quantity Type System](../adr/0013-generic-asset-quantity-types.md)
- [0014 - Financial Instrument Reference Data](../adr/0014-financial-instrument-reference-data.md)
- [0015 - Standard Service Directory Structure](../adr/0015-standard-service-directory-structure.md)
- [0016 - Tenant ID Naming Strategy](../adr/0016-tenant-id-naming-strategy.md)
- [0017 - Temporal Quality Ladder](../adr/0017-temporal-quality-ladder.md)
- [0026 - Canonical Ingestion Contract](../adr/0026-canonical-ingestion-contract.md)

**Related PRDs:**

- **Universal Asset System** (Task Master tag: `universal-asset-system`) - **Foundation dependency**
  - Defines InstrumentDefinition, Quantity types, and CEL validation patterns
  - Market Information follows the same CEL-based schema approach
- **Valuation Engine** (Future PRD) - **Primary consumer**
  - Will consume market data to value positions
  - This service provides the data, Valuation Engine applies it

**Target Task Master Tag:** `market-information-management`

---

## Table of Contents

- [Executive Summary](#executive-summary)
- [BIAN Alignment](#bian-alignment)
- [Service Boundaries (Canonical Ingestion Contract)](#service-boundaries-canonical-ingestion-contract)
- [Requirements](#requirements)
- [Technical Design](#technical-design)
- [Implementation Tasks](#implementation-tasks)
- [Success Criteria](#success-criteria)
- [Appendix A: Dataset Definition Examples](#appendix-a-dataset-definition-examples)
- [Appendix B: Quality Ladder Resolution](#appendix-b-quality-ladder-resolution)
- [Appendix C: Comparison with Reference Data Service](#appendix-c-comparison-with-reference-data-service)

---

## Executive Summary

This PRD defines the requirements for implementing the **Market Information Management** service
in Meridian, following the BIAN v14.0 Service Domain specification. This service provides the
foundational market data layer that enables position valuation across all asset classes.

### Problem Statement

Meridian's Universal Asset System can track positions in any instrument (currency, energy, carbon,
compute), but there is currently no systematic way to:

1. **Obtain** market prices and rates from external providers
2. **Store** observations with temporal and quality metadata
3. **Retrieve** the correct rate for a given point in time
4. **Validate** incoming data against tenant-defined schemas
5. **Configure** data sources with tenant-specific overrides

This creates several problems:

1. No way to answer "What was USD/GBP at timestamp X?" without external lookups
2. No registry for data sources (ECB, Bloomberg, internal tariff systems)
3. No quality/authority tracking (is this an estimate, actual, or verified value?)
4. Cannot support tenant-specific pricing (custom tariffs, negotiated rates)
5. No foundation for the Valuation Engine to build upon
6. Cannot replay valuations exactly as they executed (no bi-temporal / knowledge time support)
7. No audit trail for "why was this rate used?" (no supersession tracking)

### Solution

Implement the **BIAN Market Information Management** service domain as a multi-purpose data
platform with CEL-validated schemas, enabling:

- **Unified schema pattern**: DataSetDefinition mirrors InstrumentDefinition (same CEL approach)
- **Runtime validation**: All observations validated against dataset schemas before storage
- **Bi-temporal queries**: Both Event Time (observed_at) and Knowledge Time (created_at)
- **Quality ladder**: Estimates, actuals, verified values per ADR-0017 with knowledge lineage
- **Tenant isolation**: Shared platform sources with tenant override capability
- **Generic ingestion**: Not just pricing - weather, load forecasts, any external dataset
- **Market Data Switch**: Real-time event publishing for downstream valuation triggers

---

## BIAN Alignment

### Primary Service Domain

**Market Information Management** (BIAN v14.0)

> "Consolidates and improves market information from multiple sources to build up a bank
> knowledge base in targeted areas."

**BIAN Semantic API Specification:**

- [Market Information Management](https://bian.org/servicelandscape-14-0-0/views/view_50991.html)

**BIAN References:**

- Control Record: **Financial Market Information Administrative Plan**
- Related: **Market Data Switch Operation** (real-time dissemination - FR-2.8)
- Related: Market Analysis (forecasting - out of scope for this PRD)

**Market Data Switch Integration:** Per FR-2.8, when an ACTUAL or VERIFIED observation is
successfully ingested, the service publishes a domain event to Kafka. This enables real-time
"Mark-to-Market" and event-driven valuation downstream. Event topic:
`meridian.market_information.v1.ObservationRecorded`

### Functional Pattern

**Administer** - This service administers market information through configuration,
ingestion, and retrieval operations.

### Relationship to Other BIAN Domains

| Service Domain | Relationship | Notes |
|----------------|--------------|-------|
| **Financial Instrument Reference Data** | Loose coupling | Market data references instrument codes but doesn't require FK validation |
| **Position Keeping** | Consumer (via Valuation) | Positions need market prices for valuation |
| **Financial Accounting** | Consumer | FX rates for multi-currency settlements |
| **Market Analysis** | Future extension | Forecasting, forward curves (separate PRD) |

### Architectural Relationship to Reference Data

This service follows the **same CEL-based schema pattern** as Reference Data but serves a
different purpose:

| Reference Data Service | Market Information Management |
|------------------------|-------------------------------|
| Defines what assets CAN exist on the ledger | Defines what observations we ACCEPT about the world |
| `InstrumentDefinition` | `DataSetDefinition` |
| `InstrumentAmount` | `MarketPriceObservation` |
| `validation_expression` (CEL) | `validation_expression` (CEL) |
| `fungibility_key_expression` | `resolution_key_expression` |
| `Dimension` enum | `DataCategory` enum |

**Loose coupling benefit**: Market Information can ingest data for instruments not yet defined
in Reference Data (onboarding, external feeds, non-instrument data like weather).

---

## Service Boundaries (Canonical Ingestion Contract)

This section defines the strict boundary between Meridian's responsibility and external systems.
Following BIAN's Service Domain encapsulation principle, Market Information Management provides
the **Vault** (storage) and **Rules** (validation), while external systems provide the
**Translation** (data structuring).

### In-Scope (Meridian's Responsibility)

| Capability | Description |
|------------|-------------|
| **Schema Definition** | Maintaining `DataSetDefinition` with `attribute_schema` and validation rules |
| **Contract Enforcement** | Validating incoming data against CEL expressions (`validation_expression`) |
| **Bi-Temporal Storage** | Storing observations with Event Time (observed_at) and Knowledge Time (created_at) |
| **Quality Resolution** | Resolving conflicts via Quality Ladder (ESTIMATE < ACTUAL < VERIFIED) |
| **Knowledge Lineage** | Tracking supersession chains and causation_id for audit |
| **Temporal Queries** | Answering "What was the value at time X with knowledge at time Y?" |
| **Event Publishing** | Market Data Switch events on ACTUAL/VERIFIED ingestion |

### Out-of-Scope (External Adapter Responsibility)

| Capability | Description | Example |
|------------|-------------|---------|
| **Connectivity** | Maintaining connections to external sources | WebSocket to Bloomberg, TCP to Smart Meters |
| **Extraction** | Polling APIs, scraping, reading raw feeds | Calling ECB SDMX API, reading meter registers |
| **Normalization** | Converting source-specific formats to Meridian Protobuf | XML → `MarketPriceObservation`, CSV → gRPC request |
| **Scheduling** | Managing ingestion timing and frequency | Cron jobs, event-driven triggers |
| **Error Recovery** | Handling source-specific failure modes | API rate limits, connection timeouts |

### The Formatted Data Contract

Meridian's `RecordObservation` and `RecordObservationBatch` endpoints are **Strict Gatekeepers**.
The service accepts ONLY pre-structured data conforming to the `MarketPriceObservation` schema.

**Key Principles:**

1. **No Protocol Adapters in Core**: The service SHALL NOT contain source-specific logic
   (e.g., no code for weather APIs or smart meter protocols inside the service).

2. **Validation-on-Arrival**: The system MUST validate every observation against the
   `DataSetDefinition` CEL expressions. Invalid data is rejected with `INVALID_ARGUMENT`.

3. **Caller Responsibility**: It is the responsibility of the *caller* (external adapter)
   to structure data into the Meridian `MarketPriceObservation` format before calling.

4. **No Implicit Transformation**: If an observation doesn't match the schema, the service
   does not attempt to fix it. The "messy ETL" stays on the external side.

### CEL as Contract Enforcer

The CEL validator (FR-2.2, FR-2.6) becomes the **Compliance Auditor** at the boundary:

```text
External World                    │  Meridian Core
                                  │
┌─────────────────┐               │   ┌───────────────────────┐
│ Smart Meter     │               │   │                       │
│ ─────────────── │               │   │  DataSetDefinition    │
│ Raw Binary Data │               │   │  ─────────────────    │
└────────┬────────┘               │   │  validation_expr:     │
         │                        │   │  "decimal(value) > 0  │
         ▼                        │   │   && tou_period in    │
┌─────────────────┐               │   │   ['PEAK','OFF_PEAK']"│
│ External        │               │   └───────────┬───────────┘
│ Adapter         │  Formatted    │               │
│ ─────────────── │  Protobuf     │               ▼
│ Normalize to    ├───────────────┼──►┌───────────────────────┐
│ MarketPrice     │               │   │ CEL Validator         │
│ Observation     │               │   │ ─────────────────     │
└─────────────────┘               │   │ PASS → Store          │
                                  │   │ FAIL → INVALID_ARG    │
                                  │   └───────────────────────┘
```

### External Adapters as Reference Utilities

Adapters (like the ECB Daily Rates example) are **demonstration utilities**, not core service
features. They show how external systems should structure data for Meridian:

| Component | Location | Purpose |
|-----------|----------|---------|
| `market-data-tool` | `cmd/market-data-tool/` | Reference CLI for tenant bulk imports |
| ECB Adapter | `cmd/market-data-tool/adapters/ecb/` | Example of external API → Meridian format |

These utilities are **operationally independent** from the core service. Tenants may:

- Use the reference utilities as-is
- Build their own adapters following the same pattern
- Integrate via any system that can call gRPC with properly structured payloads

**This approach mirrors ADR-0005 (Adapter Pattern): Meridian owns the Domain and the Port;
the external world owns the Adapter.**

---

## Requirements

### Functional Requirements

#### FR-1: Dataset Definition Registry

| ID | Requirement | Priority |
|----|-------------|----------|
| FR-1.1 | System SHALL maintain a registry of dataset definitions (tenant isolation via schema-per-tenant) | P0 |
| FR-1.2 | Each dataset SHALL have a unique code within its schema | P0 |
| FR-1.3 | Datasets SHALL support categories: PRICING, CONTEXTUAL | P0 |
| FR-1.4 | Datasets SHALL define CEL validation expressions for observations | P0 |
| FR-1.5 | Datasets SHALL define CEL resolution key expressions for temporal queries | P0 |
| FR-1.6 | Datasets SHALL support versioning (observations reference dataset version) | P0 |
| FR-1.7 | System datasets SHALL be seeded during tenant provisioning (ECB rates, etc.) | P1 |

#### FR-2: Observation Ingestion

| ID | Requirement | Priority |
|----|-------------|----------|
| FR-2.1 | System SHALL accept observations referencing a dataset code and version | P0 |
| FR-2.2 | System SHALL validate observations against dataset's CEL validation expression | P0 |
| FR-2.3 | Observations SHALL include temporal bounds (observed_at, valid_from, valid_to) | P0 |
| FR-2.4 | Observations SHALL include source attribution (source_id) | P0 |
| FR-2.5 | Observations SHALL include quality level (ESTIMATE, ACTUAL, VERIFIED) | P0 |
| FR-2.6 | System SHALL reject observations that fail CEL validation | P0 |
| FR-2.7 | System SHALL support batch ingestion for bulk data loads | P1 |
| FR-2.8 | System SHALL publish domain event on ACTUAL/VERIFIED ingestion (Market Data Switch) | P0 |

#### FR-3: Temporal Queries (Bi-Temporal)

| ID | Requirement | Priority |
|----|-------------|----------|
| FR-3.1 | System SHALL support point-in-time queries ("rate at timestamp X") | P0 |
| FR-3.2 | System SHALL support effective date queries ("rate valid for date range") | P0 |
| FR-3.3 | System SHALL use resolution_key_expression for efficient temporal lookup | P0 |
| FR-3.4 | System SHALL return the highest-quality observation when multiple exist | P0 |
| FR-3.5 | System SHALL support historical range queries for analytics | P1 |
| FR-3.6 | System SHALL support bi-temporal queries via knowledge_base_time parameter | P0 |
| FR-3.7 | Bi-temporal queries SHALL exclude observations with created_at > knowledge_base_time | P0 |

#### FR-4: Data Source Configuration

| ID | Requirement | Priority |
|----|-------------|----------|
| FR-4.1 | System SHALL maintain a registry of data sources | P0 |
| FR-4.2 | Sources SHALL have trust levels for quality ladder resolution | P0 |
| FR-4.3 | Tenants SHALL be able to add custom sources | P0 |
| FR-4.4 | Tenants SHALL be able to override platform source priority | P1 |
| FR-4.5 | System SHALL support source-specific ingestion credentials (future) | P2 |

#### FR-5: Quality Ladder and Knowledge Lineage

| ID | Requirement | Priority |
|----|-------------|----------|
| FR-5.1 | Observations SHALL support quality levels per ADR-0017 | P0 |
| FR-5.2 | ESTIMATE < ACTUAL < VERIFIED in resolution precedence | P0 |
| FR-5.3 | Higher quality observations SHALL supersede lower quality for same key | P0 |
| FR-5.4 | System SHALL track quality transitions for audit | P1 |
| FR-5.5 | Corrections SHALL mark old observations as superseded (superseded_by FK) | P0 |
| FR-5.6 | Observations SHALL track causation_id for data lineage | P0 |
| FR-5.7 | Supersession chain SHALL be traversable for "Truth Evolution" audit | P1 |

#### FR-6: BIAN Control Record Operations

| ID | Requirement | Priority |
|----|-------------|----------|
| FR-6.1 | RegisterDataSet - Create new dataset definition | P0 |
| FR-6.2 | UpdateDataSet - Modify DRAFT dataset settings | P0 |
| FR-6.3 | ActivateDataSet - Transition DRAFT → ACTIVE | P0 |
| FR-6.4 | DeprecateDataSet - Transition ACTIVE → DEPRECATED | P0 |
| FR-6.5 | RetrieveDataSet - Get dataset by code and version | P0 |
| FR-6.6 | ListDataSets - Query datasets with filters | P0 |
| FR-6.7 | RecordObservation - Ingest market data point | P0 |
| FR-6.8 | RetrieveObservation - Get observation(s) for temporal query | P0 |
| FR-6.9 | RegisterDataSource - Add data provider configuration | P0 |
| FR-6.10 | EvaluateDataSet - CEL playground for testing expressions | P1 |

### Non-Functional Requirements

#### NFR-1: Performance

| ID | Requirement | Target |
|----|-------------|--------|
| NFR-1.1 | Point-in-time query latency | < 10ms p99 |
| NFR-1.2 | Observation ingestion latency | < 50ms p99 |
| NFR-1.3 | Batch ingestion throughput | 1,000 obs/sec |

#### NFR-2: Reliability

| ID | Requirement | Target |
|----|-------------|--------|
| NFR-2.1 | Service availability | 99.9% |
| NFR-2.2 | Data durability | 99.999999% |

#### NFR-3: Scalability

| ID | Requirement | Target |
|----|-------------|--------|
| NFR-3.1 | Observations per dataset per day | 10,000+ |
| NFR-3.2 | Datasets per tenant | 1,000+ |
| NFR-3.3 | Historical retention | 7 years (configurable) |

---

## Technical Design

> **Implementation Note**: This service follows the same patterns as Reference Data service.
> Reuse CEL evaluation infrastructure from `pkg/platform/cel/` where possible.

### Service Structure

Following [ADR-0015](../adr/0015-standard-service-directory-structure.md):

```text
services/market-information/
├── cmd/
│   ├── main.go
│   └── Dockerfile
├── domain/
│   ├── dataset.go              # DataSetDefinition entity (aggregate root)
│   ├── observation.go          # MarketPriceObservation entity
│   ├── data_source.go          # DataSource entity
│   ├── quality_level.go        # ESTIMATE, ACTUAL, VERIFIED
│   ├── data_category.go        # PRICING, CONTEXTUAL
│   ├── repository.go           # Repository interfaces (ports)
│   └── errors.go               # Domain errors
├── service/
│   ├── server.go               # gRPC service implementation
│   ├── dataset_service.go      # Dataset CRUD operations
│   ├── observation_service.go  # Ingestion and query operations
│   ├── source_service.go       # Data source management
│   ├── cel_validator.go        # CEL expression compilation and evaluation
│   └── mappers.go              # Proto <-> Domain mappers
├── adapters/
│   └── persistence/
│       ├── dataset_repository.go
│       ├── observation_repository.go
│       ├── source_repository.go
│       ├── entities.go
│       └── mappers.go
├── client/
│   └── client.go               # gRPC client for other services
├── observability/
│   ├── metrics.go
│   └── health.go
├── atlas/
│   └── atlas.hcl
├── migrations/
│   └── 20260116000001_initial.sql
└── k8s/
    ├── deployment.yaml
    └── service.yaml
```

### Proto Definitions

Location: `api/proto/meridian/market_information/v1/market_information.proto`

```protobuf
syntax = "proto3";

package meridian.market_information.v1;

import "buf/validate/validate.proto";
import "google/api/annotations.proto";
import "google/protobuf/timestamp.proto";
import "meridian/common/v1/types.proto";

option go_package = "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1;marketinformationv1";

// =============================================================================
// Enums
// =============================================================================

// DataCategory classifies the type of market information.
enum DataCategory {
  DATA_CATEGORY_UNSPECIFIED = 0;

  // PRICING - Data used for valuation (FX rates, commodity prices, tariffs)
  DATA_CATEGORY_PRICING = 1;

  // CONTEXTUAL - Data that influences pricing but isn't a price itself (weather, load forecasts)
  DATA_CATEGORY_CONTEXTUAL = 2;
}

// DataSetStatus defines the lifecycle state of a dataset definition.
enum DataSetStatus {
  DATA_SET_STATUS_UNSPECIFIED = 0;
  DATA_SET_STATUS_DRAFT = 1;
  DATA_SET_STATUS_ACTIVE = 2;
  DATA_SET_STATUS_DEPRECATED = 3;
}

// QualityLevel defines the authority/certainty of an observation.
// Per ADR-0017 Time-Bound Quality Ladder.
// IMPORTANT: Values are ordered for database INDEX sorting (higher = more authoritative).
// This enables efficient ORDER BY quality DESC without CASE expressions.
enum QualityLevel {
  QUALITY_LEVEL_UNSPECIFIED = 0;

  // ESTIMATE - Projected or forecasted value, may be revised (lowest authority)
  QUALITY_LEVEL_ESTIMATE = 1;

  // ACTUAL - Observed value from primary source
  QUALITY_LEVEL_ACTUAL = 2;

  // VERIFIED - Audited/reconciled value, highest authority
  QUALITY_LEVEL_VERIFIED = 3;
}

// =============================================================================
// Core Messages
// =============================================================================

// AttributeEntry is a key-value pair for observation context.
// Same pattern as quantity/v1 for consistency.
message AttributeEntry {
  string key = 1 [(buf.validate.field).string = {
    min_len: 1
    max_len: 64
    pattern: "^[a-z][a-z0-9_]*$"
  }];
  string value = 2 [(buf.validate.field).string.max_len = 1024];
}

// DataSetDefinition defines a type of market data with validation rules.
// Mirrors InstrumentDefinition from Reference Data - same CEL pattern.
message DataSetDefinition {
  // id is the unique identifier (UUID).
  string id = 1 [(buf.validate.field).string.uuid = true];

  // code is the unique dataset code (e.g., "FX_RATE", "ENERGY_SPOT", "WEATHER_TEMP").
  string code = 2 [(buf.validate.field).string = {
    min_len: 1
    max_len: 32
    pattern: "^[A-Z][A-Z0-9_]*$"
  }];

  // version is the schema version (monotonically increasing).
  int32 version = 3 [(buf.validate.field).int32.gte = 1];

  // category classifies the dataset type.
  DataCategory category = 4 [(buf.validate.field).enum = {
    defined_only: true
    not_in: [0]
  }];

  // status is the lifecycle state.
  DataSetStatus status = 5;

  // validation_expression is a CEL expression to validate observations.
  // Input: {value, observation_context, observed_at, valid_from, valid_to, source_id, quality}
  // Output: bool (true = valid)
  // Example: "has(observation_context.base_code) && decimal(value) > 0"
  string validation_expression = 6 [(buf.validate.field).string.max_len = 4096];

  // resolution_key_expression is a CEL expression to generate temporal lookup keys.
  // Input: {observation_context}
  // Output: string (deterministic key for grouping related observations)
  // Example: "observation_context.base_code + '/' + observation_context.quote_code"
  string resolution_key_expression = 7 [(buf.validate.field).string.max_len = 2048];

  // error_message_expression is a CEL expression for custom validation error messages.
  string error_message_expression = 8 [(buf.validate.field).string.max_len = 2048];

  // attribute_schema is a JSON Schema defining valid observation_context attributes.
  string attribute_schema = 9 [(buf.validate.field).string.max_len = 16384];

  // display_name is the human-readable name.
  string display_name = 10 [(buf.validate.field).string.max_len = 128];

  // description provides additional context.
  string description = 11 [(buf.validate.field).string.max_len = 1024];

  // is_system indicates this dataset was seeded during tenant provisioning.
  bool is_system = 12;

  // created_at timestamp.
  google.protobuf.Timestamp created_at = 13;

  // activated_at timestamp (null if never activated).
  google.protobuf.Timestamp activated_at = 14;

  // deprecated_at timestamp (null if not deprecated).
  google.protobuf.Timestamp deprecated_at = 15;
}

// MarketPriceObservation is a single data point in the market information system.
// Mirrors InstrumentAmount from quantity/v1 - same sealed envelope pattern.
message MarketPriceObservation {
  // id is the unique identifier (UUID).
  string id = 1 [(buf.validate.field).string.uuid = true];

  // dataset_code references the DataSetDefinition.
  string dataset_code = 2 [(buf.validate.field).string = {
    min_len: 1
    max_len: 32
    pattern: "^[A-Z][A-Z0-9_]*$"
  }];

  // dataset_version references the schema version.
  int32 dataset_version = 3 [(buf.validate.field).int32.gte = 1];

  // value is the observed price/rate/measurement (decimal string for precision).
  string value = 4 [(buf.validate.field).string = {
    min_len: 1
    max_len: 64
  }];

  // observation_context holds typed attributes (e.g., base_code, quote_code, region).
  // CEL validation expression validates this payload.
  repeated AttributeEntry observation_context = 5;

  // observed_at is when the observation was captured at source.
  google.protobuf.Timestamp observed_at = 6 [(buf.validate.field).required = true];

  // valid_from is when this observation becomes effective (null = immediate).
  google.protobuf.Timestamp valid_from = 7;

  // valid_to is when this observation expires (null = until superseded).
  google.protobuf.Timestamp valid_to = 8;

  // source_id references the DataSource that provided this observation.
  string source_id = 9 [(buf.validate.field).string = {
    min_len: 1
    max_len: 64
  }];

  // quality indicates the authority level of this observation.
  QualityLevel quality = 10 [(buf.validate.field).enum = {
    defined_only: true
    not_in: [0]
  }];

  // resolution_key is the computed key for temporal queries (from CEL expression).
  // Populated by the service during ingestion.
  string resolution_key = 11;

  // created_at is when this observation was ingested (knowledge time).
  // Used for bi-temporal queries to filter by knowledge state.
  google.protobuf.Timestamp created_at = 12;

  // ===========================================================================
  // Knowledge Lineage (ADR-0017 "Wash and Reload" audit trail)
  // ===========================================================================

  // superseded_by points to the observation that replaced this one (if any).
  // When a correction arrives for the same resolution_key and observed_at,
  // the old record is marked as superseded. Creates traversable audit trail.
  string superseded_by = 13 [(buf.validate.field).ignore = IGNORE_IF_UNPOPULATED];

  // causation_id links this observation to the upstream event that caused it.
  // Used for tracing data lineage back to source systems.
  // Examples: "ecb-feed-2026-01-15", "tariff-upload-batch-123"
  string causation_id = 14 [(buf.validate.field).string.max_len = 256];
}

// DataSource represents an external or internal data provider.
message DataSource {
  // id is the unique identifier.
  string id = 1 [(buf.validate.field).string = {
    min_len: 1
    max_len: 64
    pattern: "^[a-zA-Z0-9_-]+$"
  }];

  // name is the human-readable name.
  string name = 2 [(buf.validate.field).string = {
    min_len: 1
    max_len: 128
  }];

  // description provides context about this source.
  string description = 3 [(buf.validate.field).string.max_len = 1024];

  // trust_level determines quality ladder precedence (higher = more trusted).
  // Used when multiple sources provide data for the same resolution key.
  int32 trust_level = 4 [(buf.validate.field).int32 = {
    gte: 0
    lte: 100
  }];

  // is_system indicates this source was seeded during tenant provisioning.
  bool is_system = 5;

  // is_active indicates whether this source is currently accepting observations.
  bool is_active = 6;

  // metadata holds source-specific configuration.
  map<string, string> metadata = 7;

  // created_at timestamp.
  google.protobuf.Timestamp created_at = 8;

  // updated_at timestamp.
  google.protobuf.Timestamp updated_at = 9;
}

// =============================================================================
// Domain Events (FR-2.8: Market Data Switch)
// =============================================================================

// ObservationRecorded is published when an ACTUAL or VERIFIED observation is ingested.
// Topic: meridian.market_information.v1.ObservationRecorded
// This enables real-time valuation triggers and event-driven downstream processing.
message ObservationRecorded {
  // observation_id is the unique identifier of the recorded observation.
  string observation_id = 1 [(buf.validate.field).string.uuid = true];

  // dataset_code identifies the dataset this observation belongs to.
  string dataset_code = 2;

  // resolution_key is the computed temporal lookup key.
  string resolution_key = 3;

  // value is the observed price/rate/measurement (decimal string).
  string value = 4;

  // observed_at is when the observation was captured at source (event time).
  google.protobuf.Timestamp observed_at = 5;

  // quality indicates the authority level of this observation.
  QualityLevel quality = 6;

  // source_id references the data source that provided this observation.
  string source_id = 7;

  // recorded_at is when this event was published (system time).
  google.protobuf.Timestamp recorded_at = 8;
}

// =============================================================================
// Request/Response Messages
// =============================================================================

// --- Dataset Operations ---

message RegisterDataSetRequest {
  string code = 1 [(buf.validate.field).string = {
    min_len: 1
    max_len: 32
    pattern: "^[A-Z][A-Z0-9_]*$"
  }];
  DataCategory category = 2 [(buf.validate.field).enum = {
    defined_only: true
    not_in: [0]
  }];
  string validation_expression = 3 [(buf.validate.field).string.max_len = 4096];
  string resolution_key_expression = 4 [(buf.validate.field).string.max_len = 2048];
  string error_message_expression = 5 [(buf.validate.field).string.max_len = 2048];
  string attribute_schema = 6 [(buf.validate.field).string.max_len = 16384];
  string display_name = 7 [(buf.validate.field).string.max_len = 128];
  string description = 8 [(buf.validate.field).string.max_len = 1024];
}

message RegisterDataSetResponse {
  DataSetDefinition dataset = 1;
}

message UpdateDataSetRequest {
  string code = 1 [(buf.validate.field).string = {
    min_len: 1
    max_len: 32
    pattern: "^[A-Z][A-Z0-9_]*$"
  }];
  int32 version = 2 [(buf.validate.field).int32.gte = 1];
  string validation_expression = 3;
  string resolution_key_expression = 4;
  string error_message_expression = 5;
  string attribute_schema = 6;
  string display_name = 7;
  string description = 8;
}

message UpdateDataSetResponse {
  DataSetDefinition dataset = 1;
}

message ActivateDataSetRequest {
  string code = 1 [(buf.validate.field).string = {
    min_len: 1
    max_len: 32
    pattern: "^[A-Z][A-Z0-9_]*$"
  }];
  int32 version = 2 [(buf.validate.field).int32.gte = 1];
}

message ActivateDataSetResponse {
  DataSetDefinition dataset = 1;
}

message DeprecateDataSetRequest {
  string code = 1 [(buf.validate.field).string = {
    min_len: 1
    max_len: 32
    pattern: "^[A-Z][A-Z0-9_]*$"
  }];
  int32 version = 2 [(buf.validate.field).int32.gte = 1];
}

message DeprecateDataSetResponse {
  DataSetDefinition dataset = 1;
}

message RetrieveDataSetRequest {
  string code = 1 [(buf.validate.field).string = {
    min_len: 1
    max_len: 32
    pattern: "^[A-Z][A-Z0-9_]*$"
  }];
  // version = 0 means latest ACTIVE version
  int32 version = 2 [(buf.validate.field).int32.gte = 0];
}

message RetrieveDataSetResponse {
  DataSetDefinition dataset = 1;
}

message ListDataSetsRequest {
  DataSetStatus status_filter = 1;
  DataCategory category_filter = 2;
  int32 page_size = 3 [(buf.validate.field).int32 = {gte: 0, lte: 100}];
  string page_token = 4;
}

message ListDataSetsResponse {
  repeated DataSetDefinition datasets = 1;
  string next_page_token = 2;
}

// --- Observation Operations ---

message RecordObservationRequest {
  string dataset_code = 1 [(buf.validate.field).string = {
    min_len: 1
    max_len: 32
    pattern: "^[A-Z][A-Z0-9_]*$"
  }];
  string value = 2 [(buf.validate.field).string = {
    min_len: 1
    max_len: 64
  }];
  repeated AttributeEntry observation_context = 3;
  google.protobuf.Timestamp observed_at = 4 [(buf.validate.field).required = true];
  google.protobuf.Timestamp valid_from = 5;
  google.protobuf.Timestamp valid_to = 6;
  string source_id = 7 [(buf.validate.field).string = {
    min_len: 1
    max_len: 64
  }];
  QualityLevel quality = 8 [(buf.validate.field).enum = {
    defined_only: true
    not_in: [0]
  }];
  meridian.common.v1.IdempotencyKey idempotency_key = 9;

  // causation_id links this observation to the upstream event that caused it.
  // Used for tracing data lineage back to source systems.
  // Examples: "ecb-feed-2026-01-15", "tariff-upload-batch-123"
  string causation_id = 10 [(buf.validate.field).string.max_len = 256];
}

message RecordObservationResponse {
  MarketPriceObservation observation = 1;
}

message RecordObservationBatchRequest {
  repeated RecordObservationRequest observations = 1 [(buf.validate.field).repeated = {
    min_items: 1
    max_items: 1000
  }];
}

// BatchObservationResult represents the outcome of a single observation in a batch.
message BatchObservationResult {
  // index is the position in the original request (0-based).
  int32 index = 1;

  // Result is either success (observation) or failure (error).
  oneof result {
    MarketPriceObservation observation = 2;
    BatchError error = 3;
  }
}

// BatchError provides structured error information for failed observations.
message BatchError {
  // code is a machine-readable error code (e.g., "VALIDATION_FAILED", "DATASET_NOT_FOUND").
  string code = 1;
  // message is a human-readable error description.
  string message = 2;
}

message RecordObservationBatchResponse {
  // results contains the outcome for each observation in request order.
  repeated BatchObservationResult results = 1;
  // success_count is the number of successfully recorded observations.
  int32 success_count = 2;
  // failure_count is the number of failed observations.
  int32 failure_count = 3;
}

// RetrieveObservationRequest queries observations for a specific resolution key and time.
// Supports bi-temporal queries via knowledge_base_time for audit and replay scenarios.
message RetrieveObservationRequest {
  string dataset_code = 1 [(buf.validate.field).string = {
    min_len: 1
    max_len: 32
    pattern: "^[A-Z][A-Z0-9_]*$"
  }];

  // resolution_key identifies what we're looking for (e.g., "USD/GBP")
  string resolution_key = 2 [(buf.validate.field).string = {
    min_len: 1
    max_len: 256
  }];

  // Query mode: point-in-time OR effective date
  oneof temporal_query {
    // as_of returns the observation valid at this specific timestamp
    google.protobuf.Timestamp as_of = 3;

    // effective_for returns observations whose valid_from/valid_to spans this date
    google.protobuf.Timestamp effective_for = 4;
  }

  // min_quality filters to observations at or above this quality level
  QualityLevel min_quality = 5;

  // knowledge_base_time enables bi-temporal queries for audit and valuation replay.
  // When set, excludes any observations with created_at > knowledge_base_time.
  // This answers: "What would the system have returned at this point in time?"
  // Required for regulatory audit trails and idempotent valuation replay.
  // If omitted, defaults to current time (latest knowledge state).
  google.protobuf.Timestamp knowledge_base_time = 6;
}

message RetrieveObservationResponse {
  // observation is the best matching observation (highest quality, most recent)
  MarketPriceObservation observation = 1;

  // alternatives contains other observations that matched (for audit/comparison)
  repeated MarketPriceObservation alternatives = 2;
}

message ListObservationsRequest {
  string dataset_code = 1 [(buf.validate.field).string = {
    min_len: 1
    max_len: 32
    pattern: "^[A-Z][A-Z0-9_]*$"
  }];
  string resolution_key = 2;
  google.protobuf.Timestamp from_time = 3;
  google.protobuf.Timestamp to_time = 4;
  string source_id = 5;
  QualityLevel quality_filter = 6;
  int32 page_size = 7 [(buf.validate.field).int32 = {gte: 0, lte: 1000}];
  string page_token = 8;
}

message ListObservationsResponse {
  repeated MarketPriceObservation observations = 1;
  string next_page_token = 2;
}

// --- Data Source Operations ---

message RegisterDataSourceRequest {
  string id = 1 [(buf.validate.field).string = {
    min_len: 1
    max_len: 64
    pattern: "^[a-zA-Z0-9_-]+$"
  }];
  string name = 2 [(buf.validate.field).string = {
    min_len: 1
    max_len: 128
  }];
  string description = 3 [(buf.validate.field).string.max_len = 1024];
  int32 trust_level = 4 [(buf.validate.field).int32 = {gte: 0, lte: 100}];
  map<string, string> metadata = 5;
}

message RegisterDataSourceResponse {
  DataSource source = 1;
}

message ListDataSourcesRequest {
  bool include_inactive = 1;
  int32 page_size = 2 [(buf.validate.field).int32 = {gte: 0, lte: 100}];
  string page_token = 3;
}

message ListDataSourcesResponse {
  repeated DataSource sources = 1;
  string next_page_token = 2;
}

message UpdateDataSourceRequest {
  string id = 1 [(buf.validate.field).string = {
    min_len: 1
    max_len: 64
    pattern: "^[a-zA-Z0-9_-]+$"
  }];
  // Fields to update (only non-null fields are applied)
  string name = 2;
  string description = 3;
  int32 trust_level = 4 [(buf.validate.field).int32 = {gte: 0, lte: 100}];
  map<string, string> metadata = 5;
}

message UpdateDataSourceResponse {
  DataSource source = 1;
}

message DeactivateDataSourceRequest {
  string id = 1 [(buf.validate.field).string = {
    min_len: 1
    max_len: 64
    pattern: "^[a-zA-Z0-9_-]+$"
  }];
}

message DeactivateDataSourceResponse {
  DataSource source = 1;
}

// --- CEL Playground ---

message EvaluateDataSetRequest {
  string validation_expression = 1;
  string resolution_key_expression = 2;
  string error_message_expression = 3;
  string test_value = 4;
  repeated AttributeEntry test_context = 5;
  google.protobuf.Timestamp test_observed_at = 6;
  google.protobuf.Timestamp test_valid_from = 7;
  google.protobuf.Timestamp test_valid_to = 8;
  string test_source_id = 9;
  QualityLevel test_quality = 10;
}

message EvaluateDataSetResponse {
  repeated string compile_errors = 1;
  bool validation_result = 2;
  string resolution_key = 3;
  string error_message = 4;
}

// =============================================================================
// Service Definition
// =============================================================================

// MarketInformationService provides BIAN-compliant market data management.
//
// BIAN Service Domain: Market Information Management
// Functional Pattern: Administer
//
// This service manages market data observations with CEL-validated schemas,
// supporting temporal queries and quality-based resolution.
service MarketInformationService {
  // --- Dataset Definition Operations ---

  // RegisterDataSet creates a new dataset definition in DRAFT status.
  rpc RegisterDataSet(RegisterDataSetRequest) returns (RegisterDataSetResponse) {
    option (google.api.http) = {
      post: "/v1/datasets"
      body: "*"
    };
  }

  // UpdateDataSet modifies a DRAFT dataset definition.
  rpc UpdateDataSet(UpdateDataSetRequest) returns (UpdateDataSetResponse) {
    option (google.api.http) = {
      put: "/v1/datasets/{code}/versions/{version}"
      body: "*"
    };
  }

  // ActivateDataSet transitions a dataset from DRAFT to ACTIVE.
  rpc ActivateDataSet(ActivateDataSetRequest) returns (ActivateDataSetResponse) {
    option (google.api.http) = {
      post: "/v1/datasets/{code}/versions/{version}/activate"
      body: "*"
    };
  }

  // DeprecateDataSet transitions a dataset from ACTIVE to DEPRECATED.
  rpc DeprecateDataSet(DeprecateDataSetRequest) returns (DeprecateDataSetResponse) {
    option (google.api.http) = {
      post: "/v1/datasets/{code}/versions/{version}/deprecate"
      body: "*"
    };
  }

  // RetrieveDataSet gets a dataset definition by code and version.
  rpc RetrieveDataSet(RetrieveDataSetRequest) returns (RetrieveDataSetResponse) {
    option (google.api.http) = {
      get: "/v1/datasets/{code}"
    };
  }

  // ListDataSets returns datasets matching filter criteria.
  rpc ListDataSets(ListDataSetsRequest) returns (ListDataSetsResponse) {
    option (google.api.http) = {
      get: "/v1/datasets"
    };
  }

  // --- Observation Operations ---

  // RecordObservation ingests a single market data point.
  rpc RecordObservation(RecordObservationRequest) returns (RecordObservationResponse) {
    option (google.api.http) = {
      post: "/v1/observations"
      body: "*"
    };
  }

  // RecordObservationBatch ingests multiple observations in a single request.
  rpc RecordObservationBatch(RecordObservationBatchRequest) returns (RecordObservationBatchResponse) {
    option (google.api.http) = {
      post: "/v1/observations/batch"
      body: "*"
    };
  }

  // RetrieveObservation queries for observations matching temporal criteria.
  rpc RetrieveObservation(RetrieveObservationRequest) returns (RetrieveObservationResponse) {
    option (google.api.http) = {
      get: "/v1/observations/{dataset_code}/{resolution_key}"
    };
  }

  // ListObservations returns historical observations for analysis.
  rpc ListObservations(ListObservationsRequest) returns (ListObservationsResponse) {
    option (google.api.http) = {
      get: "/v1/observations"
    };
  }

  // --- Data Source Operations ---

  // RegisterDataSource adds a new data provider configuration.
  rpc RegisterDataSource(RegisterDataSourceRequest) returns (RegisterDataSourceResponse) {
    option (google.api.http) = {
      post: "/v1/sources"
      body: "*"
    };
  }

  // ListDataSources returns configured data providers.
  rpc ListDataSources(ListDataSourcesRequest) returns (ListDataSourcesResponse) {
    option (google.api.http) = {
      get: "/v1/sources"
    };
  }

  // UpdateDataSource modifies an existing data source configuration.
  rpc UpdateDataSource(UpdateDataSourceRequest) returns (UpdateDataSourceResponse) {
    option (google.api.http) = {
      put: "/v1/sources/{id}"
      body: "*"
    };
  }

  // DeactivateDataSource marks a data source as inactive.
  // Inactive sources cannot accept new observations but existing data is preserved.
  rpc DeactivateDataSource(DeactivateDataSourceRequest) returns (DeactivateDataSourceResponse) {
    option (google.api.http) = {
      post: "/v1/sources/{id}/deactivate"
      body: "*"
    };
  }

  // --- CEL Playground ---

  // EvaluateDataSet tests CEL expressions without persisting.
  rpc EvaluateDataSet(EvaluateDataSetRequest) returns (EvaluateDataSetResponse) {
    option (google.api.http) = {
      post: "/v1/datasets/evaluate"
      body: "*"
    };
  }
}
```

### Database Schema

Location: `services/market-information/migrations/20260116000001_initial.sql`

```sql
-- Market Information Management initial schema
-- BIAN Service Domain: Market Information Management
-- Manages market data, pricing feeds, and contextual datasets

-- =============================================================================
-- Dataset Definitions
-- =============================================================================

CREATE TABLE dataset_definition (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Dataset identity
    code VARCHAR(32) NOT NULL,
    version INTEGER NOT NULL DEFAULT 1,

    -- Classification
    category VARCHAR(20) NOT NULL CHECK (category IN ('PRICING', 'CONTEXTUAL')),

    -- Lifecycle
    status VARCHAR(20) NOT NULL DEFAULT 'DRAFT' CHECK (status IN (
        'DRAFT', 'ACTIVE', 'DEPRECATED'
    )),

    -- CEL expressions (same pattern as instrument_definition)
    validation_expression TEXT NOT NULL DEFAULT 'true',
    resolution_key_expression TEXT NOT NULL,
    error_message_expression TEXT,

    -- Attribute schema (JSON Schema for client guidance)
    attribute_schema TEXT,

    -- Display
    display_name VARCHAR(128),
    description TEXT,

    -- System flag
    is_system BOOLEAN NOT NULL DEFAULT FALSE,

    -- Timestamps
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    activated_at TIMESTAMPTZ,
    deprecated_at TIMESTAMPTZ,

    UNIQUE(code, version),
    CHECK (validation_expression <> ''),
    CHECK (resolution_key_expression <> '')
);

CREATE INDEX idx_dataset_definition_code ON dataset_definition(code);
CREATE INDEX idx_dataset_definition_status ON dataset_definition(status);
CREATE INDEX idx_dataset_definition_category ON dataset_definition(category);

COMMENT ON TABLE dataset_definition IS 'BIAN Market Information Management - Dataset type definitions with CEL validation';

-- =============================================================================
-- Data Sources
-- =============================================================================

CREATE TABLE data_source (
    id VARCHAR(64) PRIMARY KEY,

    name VARCHAR(128) NOT NULL,
    description TEXT,

    -- Trust level for quality ladder resolution (0-100, higher = more trusted)
    trust_level INTEGER NOT NULL DEFAULT 50 CHECK (trust_level >= 0 AND trust_level <= 100),

    -- System flag
    is_system BOOLEAN NOT NULL DEFAULT FALSE,

    -- Active flag
    is_active BOOLEAN NOT NULL DEFAULT TRUE,

    -- Source-specific metadata (JSON)
    metadata JSONB NOT NULL DEFAULT '{}',

    -- Timestamps
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_data_source_active ON data_source(is_active);

COMMENT ON TABLE data_source IS 'External and internal data providers for market information';

-- =============================================================================
-- Market Price Observations
-- =============================================================================

CREATE TABLE market_price_observation (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Dataset reference
    dataset_code VARCHAR(32) NOT NULL,
    dataset_version INTEGER NOT NULL,

    -- The observed value (decimal string for precision)
    value VARCHAR(64) NOT NULL,

    -- Observation context (validated by CEL expression)
    observation_context JSONB NOT NULL DEFAULT '{}',

    -- Temporal bounds
    observed_at TIMESTAMPTZ NOT NULL,       -- When captured at source
    valid_from TIMESTAMPTZ,                 -- Effective period start (null = immediate)
    valid_to TIMESTAMPTZ,                   -- Effective period end (null = until superseded)

    -- Source attribution
    source_id VARCHAR(64) NOT NULL REFERENCES data_source(id),

    -- Quality ladder (INTEGER for correct index ordering)
    -- 1 = ESTIMATE, 2 = ACTUAL, 3 = VERIFIED (higher = more authoritative)
    quality INTEGER NOT NULL CHECK (quality IN (1, 2, 3)),

    -- Computed resolution key (from CEL expression, for efficient lookup)
    resolution_key VARCHAR(256) NOT NULL,

    -- Ingestion timestamp (knowledge time for bi-temporal queries)
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Knowledge lineage (ADR-0017 "Wash and Reload" audit trail)
    -- Points to the observation that superseded this one (NULL = current)
    superseded_by UUID REFERENCES market_price_observation(id),
    -- Links to upstream event that caused this observation
    causation_id VARCHAR(256),

    -- Foreign key to dataset (code+version)
    FOREIGN KEY (dataset_code, dataset_version) REFERENCES dataset_definition(code, version)
);

-- Primary query path: resolution_key + temporal bounds + quality
CREATE INDEX idx_observation_resolution_temporal ON market_price_observation(
    resolution_key,
    observed_at DESC,
    quality DESC
);

-- For effective date queries
CREATE INDEX idx_observation_effective_period ON market_price_observation(
    resolution_key,
    valid_from,
    valid_to
);

-- For source filtering
CREATE INDEX idx_observation_source ON market_price_observation(source_id);

-- For dataset queries
CREATE INDEX idx_observation_dataset ON market_price_observation(dataset_code, dataset_version);

-- For time-range analytics
CREATE INDEX idx_observation_observed_at ON market_price_observation(observed_at DESC);

-- For knowledge lineage traversal
CREATE INDEX idx_observation_superseded ON market_price_observation(superseded_by) WHERE superseded_by IS NOT NULL;

-- For bi-temporal queries (filter by knowledge time)
CREATE INDEX idx_observation_created_at ON market_price_observation(created_at);

-- Composite index for full bi-temporal resolution query with supersession filter
-- Optimized for: WHERE resolution_key = ? AND superseded_by IS NULL AND created_at <= ?
-- ORDER BY quality DESC, observed_at DESC, created_at DESC
CREATE INDEX idx_observation_resolution_bitemporal ON market_price_observation(
    resolution_key,
    quality DESC,
    observed_at DESC,
    created_at DESC
) WHERE superseded_by IS NULL;

COMMENT ON TABLE market_price_observation IS 'Market data observations with bi-temporal and quality metadata';
COMMENT ON COLUMN market_price_observation.resolution_key IS 'Computed from CEL expression for efficient temporal queries';
COMMENT ON COLUMN market_price_observation.quality IS 'ADR-0017: 1=ESTIMATE, 2=ACTUAL, 3=VERIFIED';
COMMENT ON COLUMN market_price_observation.created_at IS 'Knowledge time - when system learned of this observation';
COMMENT ON COLUMN market_price_observation.superseded_by IS 'FK to replacement observation (NULL = current)';

-- =============================================================================
-- Seed System Data Sources
-- =============================================================================

INSERT INTO data_source (id, name, description, trust_level, is_system, is_active) VALUES
    ('ECB_DAILY', 'European Central Bank Daily Rates', 'ECB reference exchange rates published daily', 80, TRUE, TRUE),
    ('INTERNAL_ADMIN', 'Internal Administration', 'Manually configured rates and tariffs', 70, TRUE, TRUE),
    ('SYSTEM_DEFAULT', 'System Default', 'Fallback source for system-seeded data', 50, TRUE, TRUE);

-- =============================================================================
-- Seed System Dataset Definitions
-- =============================================================================

INSERT INTO dataset_definition (
    code, version, category, status,
    validation_expression, resolution_key_expression,
    display_name, description, is_system, activated_at
) VALUES
(
    'FX_RATE', 1, 'PRICING', 'ACTIVE',
    -- Validation: must have base and quote codes, positive rate
    'has(observation_context.base_code) && has(observation_context.quote_code) && ' ||
    'size(observation_context.base_code) == 3 && size(observation_context.quote_code) == 3 && ' ||
    'decimal(value) > 0',
    -- Resolution key: base/quote pair
    'observation_context.base_code + "/" + observation_context.quote_code',
    'Foreign Exchange Rates',
    'Currency exchange rates (e.g., USD/GBP, EUR/GBP)',
    TRUE,
    NOW()
),
(
    'ENERGY_SPOT', 1, 'PRICING', 'ACTIVE',
    -- Validation: must have region, positive price
    'has(observation_context.region) && decimal(value) >= 0',
    -- Resolution key: region + optional product
    'observation_context.region + "/" + (has(observation_context.product) ? observation_context.product : "DEFAULT")',
    'Energy Spot Prices',
    'Wholesale energy prices by region and product',
    TRUE,
    NOW()
),
(
    'ENERGY_TARIFF', 1, 'PRICING', 'ACTIVE',
    -- Validation: must have tariff_zone, tou_period (0-47), positive rate
    'has(observation_context.tariff_zone) && has(observation_context.tou_period) && ' ||
    'int(observation_context.tou_period) >= 0 && int(observation_context.tou_period) <= 47 && ' ||
    'decimal(value) >= 0',
    -- Resolution key: zone + period
    'observation_context.tariff_zone + "/" + observation_context.tou_period',
    'Energy Tariffs',
    'Time-of-use energy tariffs by zone and half-hourly period',
    TRUE,
    NOW()
),
(
    'CARBON_PRICE', 1, 'PRICING', 'ACTIVE',
    -- Validation: must have registry, positive price
    'has(observation_context.registry) && decimal(value) >= 0',
    -- Resolution key: registry + optional vintage
    'observation_context.registry + "/" + (has(observation_context.vintage) ? observation_context.vintage : "CURRENT")',
    'Carbon Credit Prices',
    'Voluntary carbon unit prices by registry and vintage',
    TRUE,
    NOW()
),
(
    'WEATHER_TEMP', 1, 'CONTEXTUAL', 'ACTIVE',
    -- Validation: must have location, temperature in reasonable range
    'has(observation_context.location) && ' ||
    'decimal(value) >= -100 && decimal(value) <= 100',
    -- Resolution key: location
    'observation_context.location',
    'Weather Temperature',
    'Temperature observations by location (Celsius)',
    TRUE,
    NOW()
);
```

### Technical Hardening Requirements

#### Decimal Safety (Critical for Valuation)

The `value` field in observations is stored as `VARCHAR(64)` to preserve arbitrary precision from
source systems. **Implementers MUST use `shopspring/decimal` for all value manipulation.**

```go
import "github.com/shopspring/decimal"

// CORRECT: Parse and compute with shopspring/decimal
rate, err := decimal.NewFromString(observation.Value)
if err != nil {
    return fmt.Errorf("invalid decimal value: %w", err)
}
result := rate.Mul(quantity)

// WRONG: Never use float64 for financial values
// rate, _ := strconv.ParseFloat(observation.Value, 64)  // PRECISION LOSS
```

#### CEL Context for Validation

The `validation_expression` CEL environment MUST provide:

1. **`value` as string** - The raw observation value (preserved precision)
2. **`decimal(value)` function** - Converts string to decimal for numeric comparisons
3. **`observation_context` as map[string]string** - Key-value attributes (see below)
4. **`observed_at`, `valid_from`, `valid_to`** - Temporal bounds as timestamps
5. **`source_id`** - The data source identifier
6. **`quality`** - The quality level as integer (1, 2, or 3)

##### observation_context Conversion

In the proto definition, `observation_context` is `repeated AttributeEntry` (a list of
key-value pairs). Before CEL evaluation, the service MUST convert this to a `map[string]string`
for natural map-style access in expressions:

```go
// Convert repeated AttributeEntry to map for CEL
func toContextMap(entries []*AttributeEntry) map[string]string {
    result := make(map[string]string, len(entries))
    for _, e := range entries {
        result[e.Key] = e.Value
    }
    return result
}
```

This allows CEL expressions to use map-style access:

- `has(observation_context.base_code)` - Check if key exists
- `observation_context.base_code` - Access value by key
- `size(observation_context.base_code)` - String length operations

Example validation expression using numeric checks:

```cel
has(observation_context.base_code) &&
size(observation_context.base_code) == 3 &&
decimal(value) > 0 &&
decimal(value) < 1000
```

#### Dataset Versioning and Temporal Consistency

When processing temporal queries, the system MUST use the `DataSetDefinition` version that was
**ACTIVE at the requested observation time**, not the current active version. This ensures:

1. Observations are interpreted using the schema that was valid when they were recorded
2. Historical queries return consistent results even after schema evolution
3. Audit trails accurately reflect the business rules in effect at each point in time

Implementation note: Store `dataset_version` on each observation and join on that version
when retrieving schema metadata for validation or interpretation.

### Domain Events

#### ObservationRecorded Event (FR-2.8)

Published to Kafka when an ACTUAL or VERIFIED observation is successfully ingested.
See `ObservationRecorded` message in the Proto Definitions section above.

**Topic:** `meridian.market_information.v1.ObservationRecorded`

This enables:

- **Real-time valuation triggers** - Valuation Engine can recompute positions on price updates
- **Event-driven architecture** - Downstream services react to market data changes
- **Audit trail** - All price updates are captured as events

### Integration Points

#### 1. Valuation Engine (Future Consumer)

The primary consumer of Market Information. Valuation queries market data to convert positions
to settlement currency:

```go
// In future valuation-engine/service/fx_provider.go
func (p *FXProvider) GetRate(ctx context.Context, base, quote string, asOf time.Time) (decimal.Decimal, error) {
    resp, err := p.marketInfoClient.RetrieveObservation(ctx, &RetrieveObservationRequest{
        DatasetCode:   "FX_RATE",
        ResolutionKey: base + "/" + quote,
        TemporalQuery: &RetrieveObservationRequest_AsOf{AsOf: timestamppb.New(asOf)},
        MinQuality:    QualityLevel_QUALITY_LEVEL_ACTUAL,
    })
    if err != nil {
        return decimal.Zero, fmt.Errorf("FX rate lookup failed: %w", err)
    }

    return decimal.RequireFromString(resp.Observation.Value), nil
}
```

#### 2. Financial Accounting (FX Settlement)

Multi-currency settlements require FX rate lookup:

```go
// In financial-accounting/service/multi_currency.go
func (s *Service) GetSettlementRate(
    ctx context.Context, from, to string, settlementDate time.Time,
) (decimal.Decimal, error) {
    // Query Market Information for FX rate
    return s.marketInfoClient.GetRate(ctx, from, to, settlementDate)
}
```

#### 3. Energy Tariff Provider

Time-of-use energy valuation:

```go
// In future energy-tariff-provider/service/tariff_provider.go
func (p *TariffProvider) GetTariff(
    ctx context.Context, zone string, period int, asOf time.Time,
) (decimal.Decimal, error) {
    resp, err := p.marketInfoClient.RetrieveObservation(ctx, &RetrieveObservationRequest{
        DatasetCode:   "ENERGY_TARIFF",
        ResolutionKey: zone + "/" + strconv.Itoa(period),
        TemporalQuery: &RetrieveObservationRequest_EffectiveFor{EffectiveFor: timestamppb.New(asOf)},
        MinQuality:    QualityLevel_QUALITY_LEVEL_ACTUAL,
    })
    if err != nil {
        return decimal.Zero, err
    }

    return decimal.RequireFromString(resp.Observation.Value), nil
}
```

#### 4. Tenant Provisioning

Seed system datasets and sources for new tenants:

```go
// In tenant/worker/provisioning_worker.go
func (w *Worker) provisionMarketInformation(ctx context.Context, tenantID string) error {
    // System datasets and sources are seeded by migration
    // Tenant-specific sources can be added here
    return nil
}
```

---

## Implementation Tasks

### Phase 1: Core Service (P0)

| Task ID | Description | Estimate |
|---------|-------------|----------|
| MIM-001 | Create service skeleton following ADR-0015 structure | 2 |
| MIM-002 | Define proto file with all messages and service | 3 |
| MIM-003 | Implement domain models (DataSetDefinition, MarketPriceObservation, DataSource) | 3 |
| MIM-004 | Create database migrations | 2 |
| MIM-005 | Implement CEL validator (reuse from pkg/platform/cel if available) | 3 |
| MIM-006 | Implement dataset repository layer | 3 |
| MIM-007 | Implement observation repository layer | 3 |
| MIM-008 | Implement source repository layer | 2 |
| MIM-009 | Implement dataset service (Register, Update, Activate, Deprecate) | 3 |
| MIM-010 | Implement observation service (Record, Retrieve, List) | 5 |
| MIM-011 | Implement gRPC handlers | 3 |
| MIM-012 | Add observability (metrics, health checks) | 2 |
| MIM-013 | Write unit tests (80% coverage) | 5 |
| MIM-014 | Write integration tests | 3 |

### Phase 2: Temporal Query Optimization (P0)

| Task ID | Description | Estimate |
|---------|-------------|----------|
| MIM-015 | Implement point-in-time query with quality resolution | 3 |
| MIM-016 | Implement effective date range query | 3 |
| MIM-017 | Add caching for hot resolution keys | 3 |
| MIM-018 | Performance benchmarks for query paths | 2 |

### Phase 3: Integration (P0)

| Task ID | Description | Estimate |
|---------|-------------|----------|
| MIM-019 | Create gRPC client package | 2 |
| MIM-020 | Update Kubernetes manifests | 2 |
| MIM-021 | Add to Tilt local development | 1 |
| MIM-022 | Integrate with tenant provisioning | 2 |

### Phase 4: Batch Ingestion (P1)

| Task ID | Description | Estimate |
|---------|-------------|----------|
| MIM-023 | Implement batch ingestion endpoint | 3 |
| MIM-024 | Add batch validation and partial success handling | 2 |
| MIM-025 | Create sample data ingestion scripts | 2 |

### Phase 5: External Provider Integration (P2)

| Task ID | Description | Estimate |
|---------|-------------|----------|
| MIM-026 | ECB daily rates adapter | 3 |
| MIM-027 | Generic HTTP/REST adapter framework | 5 |
| MIM-028 | Scheduled ingestion worker | 3 |

### Phase 6: Documentation & ADR (P0)

| Task ID | Description | Estimate |
|---------|-------------|----------|
| MIM-029 | Write ADR-0025 for Market Information Management | 2 |
| MIM-030 | Update architecture diagrams | 2 |
| MIM-031 | Create runbook for operations | 2 |

---

## Success Criteria

### Functional Success

- [ ] All dataset operations implemented (Register, Update, Activate, Deprecate, Retrieve, List)
- [ ] Observation ingestion with CEL validation working
- [ ] Point-in-time queries return correct observation (< 10ms p99)
- [ ] Effective date queries return correct observation
- [ ] Quality ladder resolution selects highest quality observation
- [ ] Batch ingestion handles 1,000 observations/request
- [ ] System datasets seeded on tenant provisioning
- [ ] Bi-temporal queries with knowledge_base_time work correctly
- [ ] Supersession tracking creates traversable audit trail
- [ ] Domain events published for ACTUAL/VERIFIED observations

### Technical Success

- [ ] 80% unit test coverage
- [ ] All integration tests passing
- [ ] Service follows ADR-0015 directory structure
- [ ] Proto follows existing conventions (buf lint passes)
- [ ] Database migration works in multi-tenant schema
- [ ] CEL validation reuses platform infrastructure

### Business Success

- [ ] Can query "What is USD/GBP at timestamp X?" and get correct rate
- [ ] Can configure tenant-specific tariffs with priority over system defaults
- [ ] Valuation Engine (future) has a stable API to build upon
- [ ] Data quality (estimate vs actual vs verified) is tracked for audit
- [ ] Can replay valuations exactly as they would have executed at any past moment (bi-temporal)
- [ ] Auditors can trace "Truth Evolution" - why a rate was used at a specific time
- [ ] Downstream services receive real-time price update notifications (Market Data Switch)

---

## Appendix A: Dataset Definition Examples

### A.1: FX Rate Dataset

```yaml
code: FX_RATE
category: PRICING
validation_expression: |
  has(observation_context.base_code) &&
  has(observation_context.quote_code) &&
  size(observation_context.base_code) == 3 &&
  size(observation_context.quote_code) == 3 &&
  decimal(value) > 0
resolution_key_expression: |
  observation_context.base_code + "/" + observation_context.quote_code

# Example observation:
# {
#   "dataset_code": "FX_RATE",
#   "value": "0.79",
#   "observation_context": {"base_code": "USD", "quote_code": "GBP"},
#   "observed_at": "2026-01-15T16:00:00Z",
#   "source_id": "ECB_DAILY",
#   "quality": "ACTUAL"
# }
# Resolution key: "USD/GBP"
```

### A.2: Energy Tariff Dataset

```yaml
code: ENERGY_TARIFF
category: PRICING
validation_expression: |
  has(observation_context.tariff_zone) &&
  has(observation_context.tou_period) &&
  int(observation_context.tou_period) >= 0 &&
  int(observation_context.tou_period) <= 47 &&
  decimal(value) >= 0
resolution_key_expression: |
  observation_context.tariff_zone + "/" + observation_context.tou_period

# Example observation:
# {
#   "dataset_code": "ENERGY_TARIFF",
#   "value": "0.35",
#   "observation_context": {"tariff_zone": "UK_DOMESTIC", "tou_period": "14"},
#   "observed_at": "2026-01-01T00:00:00Z",
#   "valid_from": "2026-01-01T00:00:00Z",
#   "valid_to": "2026-03-31T23:59:59Z",
#   "source_id": "INTERNAL_ADMIN",
#   "quality": "VERIFIED"
# }
# Resolution key: "UK_DOMESTIC/14"
```

### A.3: Weather Temperature Dataset (Contextual)

```yaml
code: WEATHER_TEMP
category: CONTEXTUAL
validation_expression: |
  has(observation_context.location) &&
  decimal(value) >= -100 &&
  decimal(value) <= 100
resolution_key_expression: |
  observation_context.location

# Example observation:
# {
#   "dataset_code": "WEATHER_TEMP",
#   "value": "18.5",
#   "observation_context": {"location": "LONDON_HEATHROW", "unit": "CELSIUS"},
#   "observed_at": "2026-01-15T14:00:00Z",
#   "source_id": "MET_OFFICE",
#   "quality": "ACTUAL"
# }
# Resolution key: "LONDON_HEATHROW"
```

---

## Appendix B: Quality Ladder Resolution

Per ADR-0017, when multiple observations exist for the same resolution key and time window,
the system resolves using this **deterministic precedence** (required for idempotent replay):

1. **Quality Level** (VERIFIED > ACTUAL > ESTIMATE) - Authority of data
2. **Observation Time** (more recent observed_at wins) - Freshness within quality tier
3. **Source Trust Level** (higher trust_level wins) - Tie-breaker for same quality and time
4. **System Sequence** (more recent created_at wins) - Final deterministic tie-breaker

**Why this order?** Within a single quality tier, the most recent observation represents the
market's latest state. Source trust only matters when two sources report at the same time.
The created_at tie-breaker ensures deterministic results even in edge cases.

```sql
-- Example resolution query with bi-temporal support
-- quality is INTEGER: 1=ESTIMATE, 2=ACTUAL, 3=VERIFIED
-- Joins data_source for trust_level tie-breaking
SELECT o.* FROM market_price_observation o
JOIN data_source s ON o.source_id = s.id
WHERE o.resolution_key = 'USD/GBP'
  AND o.observed_at <= '2026-01-15T14:30:00Z'
  AND o.superseded_by IS NULL                    -- Only current observations
  AND o.created_at <= :knowledge_base_time       -- Bi-temporal: knowledge cutoff
ORDER BY
  o.quality DESC,       -- 3 (VERIFIED) > 2 (ACTUAL) > 1 (ESTIMATE)
  o.observed_at DESC,   -- Freshest observation within quality tier
  s.trust_level DESC,   -- Higher trust source wins ties
  o.created_at DESC     -- System sequence as final tie-breaker
LIMIT 1;
```

**Bi-temporal query example**: To replay a valuation as of 9:00 AM on Jan 15th:

```sql
-- What rate would the system have used at 9:00 AM?
-- knowledge_base_time = '2026-01-15T09:00:00Z'
-- This excludes any "Verified" data that arrived later that day
```

---

## Appendix C: Comparison with Reference Data Service

| Aspect | Reference Data | Market Information |
|--------|----------------|-------------------|
| **Purpose** | Define what assets exist | Observe values about the world |
| **Primary Entity** | InstrumentDefinition | DataSetDefinition |
| **Data Entity** | InstrumentAmount (positions) | MarketPriceObservation |
| **CEL Validation** | Yes (same pattern) | Yes (same pattern) |
| **Temporal Model** | Version-based | Time-based (observed_at, valid_from/to) |
| **Quality Ladder** | No (definition is definition) | Yes (ESTIMATE, ACTUAL, VERIFIED) |
| **Consumer** | Position Keeping, Accounting | Valuation Engine, Risk, Analytics |

<!-- End of PRD -->
