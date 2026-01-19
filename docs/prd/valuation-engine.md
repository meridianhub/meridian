# PRD: Valuation Engine Integration

**Version**: 1.0
**Status**: Draft
**Author**: Technical Architecture
**Date**: 2026-01-19

---

## Executive Summary

This PRD defines the integration of valuation capabilities into Meridian's existing architecture. The Valuation Engine bridges the **Asset Ledger** (Position Keeping) with the **Fiat Ledger** (Financial Accounting) by computing values using market data and creating auditable receipts.

**Key Insight**: Valuation is not a new service—it's a step within Payment Order that leverages existing components (Reference Data, Market Information, Position Keeping, Financial Accounting) with minimal new infrastructure.

---

## Problem Statement

### The Physics-to-Finance Linkage Gap

1. **The Core Conflict**: Position Keeping tracks physical reality (kWh, GPU-hours, Carbon) which is "noisy"—estimates become actuals, meters are corrected. Financial Accounting tracks obligations (Money) which is "rigid"—bills and payments cannot be changed without legal chaos.

2. **The Basis-Drift Problem**: Without a systematic bridge, auditors cannot prove why a customer paid £3.50 for 10kWh on Tuesday when the database now shows 12kWh and a different rate.

3. **The Reconciliation Dead-End**: When measurements are corrected (Wash & Reload), the system must calculate adjustments using the *original* valuation assumptions, not current data.

### The Goal

Create a **High-Integrity Stateless Bridge** that ensures every financial entry can be traced back through a **Valuation Receipt** to the specific **Market Observation** and **Physical Measurement** at the exact moment the obligation was created.

---

## Architecture Overview

### Existing Components (No Changes Required)

| Component | Role | Status |
|-----------|------|--------|
| Position Keeping | Asset Ledger (kWh, CO2, GPU-hours) | ✅ Live |
| Market Information | Bi-temporal rate storage | ✅ Live |
| Financial Accounting | Fiat Ledger (£, $, €) | ✅ Live |
| Reference Data | Instrument definitions + CEL | ✅ Live |
| Payment Order | Saga orchestration | ✅ Live |

### New Components

| Component | Role | Scope |
|-----------|------|-------|
| ValuationBinding | Instrument → Market Data mapping | Extension to Reference Data |
| ValuationReceipt | Immutable proof of computation | New table in Payment Order |
| Valuation Step | Compute value within saga | ~100 lines in Payment Order |

---

## Detailed Design

### 1. Extend Reference Data: ValuationBinding

Add valuation configuration to `InstrumentDefinition`:

```go
// Extension to services/reference-data/registry/registry.go

type InstrumentDefinition struct {
    // ... existing fields ...

    // NEW: Valuation binding (optional - nil for terminal instruments like GBP)
    Valuation *ValuationBinding
}

type ValuationBinding struct {
    // Target instrument this valuates TO
    // "GBP" = terminal (Monetary dimension)
    // "CO2-KG" = intermediate hop (Commodity dimension)
    TargetInstrumentCode string

    // Dataset in Market Information to query for rates
    // e.g., "UK_ENERGY_TARIFF", "UK_GRID_CARBON_INTENSITY"
    DatasetCode string

    // CEL expression to derive rate lookup key from measurement attributes
    // Input: attributes map[string]string
    // Output: string (rate key)
    // Example: attributes.tariff_zone + "/" + attributes.tou_period
    RateKeyExpression string

    // CEL expression to compute value
    // Input: amount string, rate string
    // Output: decimal (as string)
    // Example: decimal(amount) * decimal(rate)
    ValueExpression string
}
```

**Database Schema Extension**:

```sql
-- Add to instrument_definitions table
ALTER TABLE instrument_definitions ADD COLUMN IF NOT EXISTS valuation_target_code VARCHAR(32);
ALTER TABLE instrument_definitions ADD COLUMN IF NOT EXISTS valuation_dataset_code VARCHAR(64);
ALTER TABLE instrument_definitions ADD COLUMN IF NOT EXISTS valuation_rate_key_expr TEXT;
ALTER TABLE instrument_definitions ADD COLUMN IF NOT EXISTS valuation_value_expr TEXT;

-- Constraint: if one valuation field is set, all must be set
ALTER TABLE instrument_definitions ADD CONSTRAINT chk_valuation_complete
    CHECK (
        (valuation_target_code IS NULL AND valuation_dataset_code IS NULL
         AND valuation_rate_key_expr IS NULL AND valuation_value_expr IS NULL)
        OR
        (valuation_target_code IS NOT NULL AND valuation_dataset_code IS NOT NULL
         AND valuation_rate_key_expr IS NOT NULL AND valuation_value_expr IS NOT NULL)
    );
```

### 2. Valuation Receipt Table

Add to Payment Order service:

```sql
-- services/payment-order/migrations/XXXXXX_valuation_receipts.sql

CREATE TABLE valuation_receipts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    payment_order_id UUID NOT NULL REFERENCES payment_orders(id),

    -- Knowledge Time: when this valuation was computed
    knowledge_time TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Frozen Inputs: Measurement
    measurement_id UUID NOT NULL,
    measurement_instrument_code VARCHAR(32) NOT NULL,
    measurement_value DECIMAL(38, 18) NOT NULL,
    measurement_bucket_id VARCHAR(64),

    -- Frozen Inputs: Rate
    rate_observation_id UUID NOT NULL,
    rate_dataset_code VARCHAR(64) NOT NULL,
    rate_key VARCHAR(128) NOT NULL,
    rate_value DECIMAL(38, 18) NOT NULL,
    rate_observed_at TIMESTAMPTZ NOT NULL,
    rate_valid_from TIMESTAMPTZ NOT NULL,
    rate_valid_to TIMESTAMPTZ NOT NULL,

    -- CEL Expressions Used (for audit replay)
    rate_key_expression TEXT NOT NULL,
    value_expression TEXT NOT NULL,

    -- Output
    output_instrument_code VARCHAR(32) NOT NULL,
    output_value DECIMAL(38, 18) NOT NULL,

    -- Linkage
    posting_id UUID,  -- FK to financial_accounting posting

    -- For corrections: links to original receipt
    supersedes_receipt_id UUID REFERENCES valuation_receipts(id),
    correction_reason VARCHAR(64),  -- 'MEASUREMENT_CORRECTION', 'RATE_CORRECTION'

    -- Audit
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Append-only: no updates or deletes
    CONSTRAINT pk_valuation_receipts PRIMARY KEY (id)
);

-- Indexes for common queries
CREATE INDEX idx_receipts_payment_order ON valuation_receipts(payment_order_id);
CREATE INDEX idx_receipts_measurement ON valuation_receipts(measurement_id);
CREATE INDEX idx_receipts_supersedes ON valuation_receipts(supersedes_receipt_id)
    WHERE supersedes_receipt_id IS NOT NULL;
CREATE INDEX idx_receipts_knowledge_time ON valuation_receipts(knowledge_time);
```

### 3. Payment Order: Commodity Order Flow

Extend Payment Order to handle commodity measurements with dual-posting:

```go
// services/payment-order/service/commodity_order.go

type CommodityPaymentOrder struct {
    // Input
    AccountID       string
    PartyID         string
    InstrumentCode  string              // "KWH", "CO2-KG"
    Amount          decimal.Decimal
    Attributes      map[string]string   // tou_period, tariff_zone, etc.
    Timestamp       time.Time

    // Accounts for posting
    AssetDebitAccount  string           // Position Keeping account
    FiatDebitAccount   string           // Financial Accounting debit
    FiatCreditAccount  string           // Financial Accounting credit

    // Internal state (populated during saga)
    measurementID  *uuid.UUID
    bucketID       string
    receipt        *ValuationReceipt
}

func (s *PaymentOrderService) ProcessCommodityOrder(
    ctx context.Context,
    order *CommodityPaymentOrder,
) error {
    saga := s.sagaOrchestrator.New("commodity-dual-post")

    // Step 1: Load instrument definition with valuation binding
    var instrument *CompiledInstrument
    saga.AddStep(SagaStep{
        Name: "load_instrument",
        Execute: func() error {
            inst, err := s.referenceData.GetActiveDefinition(ctx, order.InstrumentCode)
            if err != nil {
                return fmt.Errorf("load instrument: %w", err)
            }
            if inst.Valuation == nil {
                return fmt.Errorf("instrument %s has no valuation binding", order.InstrumentCode)
            }
            instrument = inst
            return nil
        },
    })

    // Step 2: Compute bucket key and validate attributes
    saga.AddStep(SagaStep{
        Name: "validate_attributes",
        Execute: func() error {
            // Validate using existing CEL validation
            result, err := s.referenceData.ValidateAttributes(ctx,
                order.InstrumentCode, 1, AttributeBag{
                    Attributes: order.Attributes,
                    Amount:     order.Amount.String(),
                })
            if err != nil {
                return err
            }
            if !result.Valid {
                return fmt.Errorf("attribute validation failed: %s", result.ErrorMessage)
            }

            // Compute bucket key
            order.bucketID, err = s.computeBucketKey(ctx, instrument, order.Attributes)
            return err
        },
    })

    // Step 3: Derive rate key and fetch rate from Market Information
    var rate *RateObservation
    saga.AddStep(SagaStep{
        Name: "lookup_rate",
        Execute: func() error {
            // Evaluate rate key expression
            rateKey, err := s.evalRateKeyExpr(ctx, instrument.Valuation.RateKeyExpression, order.Attributes)
            if err != nil {
                return fmt.Errorf("rate key expression: %w", err)
            }

            // Fetch rate from Market Information
            r, err := s.marketInfo.GetRate(ctx, GetRateRequest{
                DatasetCode: instrument.Valuation.DatasetCode,
                Key:         rateKey,
                AsOfTime:    order.Timestamp,
            })
            if err != nil {
                return fmt.Errorf("rate lookup: %w", err)
            }
            rate = r
            return nil
        },
    })

    // Step 4: Compute value and create receipt
    saga.AddStep(SagaStep{
        Name: "create_receipt",
        Execute: func() error {
            // Evaluate value expression
            value, err := s.evalValueExpr(ctx,
                instrument.Valuation.ValueExpression,
                order.Amount.String(),
                rate.Value.String())
            if err != nil {
                return fmt.Errorf("value expression: %w", err)
            }

            // Create receipt
            order.receipt = &ValuationReceipt{
                ID:                     uuid.New(),
                PaymentOrderID:         order.ID,
                KnowledgeTime:          time.Now(),
                MeasurementInstrument:  order.InstrumentCode,
                MeasurementValue:       order.Amount,
                MeasurementBucketID:    order.bucketID,
                RateObservationID:      rate.ID,
                RateDatasetCode:        instrument.Valuation.DatasetCode,
                RateKey:                rate.Key,
                RateValue:              rate.Value,
                RateObservedAt:         rate.ObservedAt,
                RateValidFrom:          rate.ValidFrom,
                RateValidTo:            rate.ValidTo,
                RateKeyExpression:      instrument.Valuation.RateKeyExpression,
                ValueExpression:        instrument.Valuation.ValueExpression,
                OutputInstrumentCode:   instrument.Valuation.TargetInstrumentCode,
                OutputValue:            value,
            }

            return s.receiptStore.Save(ctx, order.receipt)
        },
    })

    // Step 5: Post to Asset Ledger (Position Keeping)
    saga.AddStep(SagaStep{
        Name: "post_asset_ledger",
        Execute: func() error {
            m, err := s.positionKeeping.RecordMeasurement(ctx, RecordMeasurementRequest{
                AccountID:      order.AssetDebitAccount,
                InstrumentCode: order.InstrumentCode,
                Amount:         order.Amount,
                Attributes:     order.Attributes,
                BucketID:       order.bucketID,
                Timestamp:      order.Timestamp,
                ReceiptID:      order.receipt.ID,
            })
            if err != nil {
                return err
            }
            order.measurementID = &m.ID
            order.receipt.MeasurementID = m.ID
            return nil
        },
        Compensate: func() error {
            if order.measurementID != nil {
                return s.positionKeeping.VoidMeasurement(ctx, *order.measurementID)
            }
            return nil
        },
    })

    // Step 6: Post to Fiat Ledger (Financial Accounting)
    saga.AddStep(SagaStep{
        Name: "post_fiat_ledger",
        Execute: func() error {
            posting, err := s.financialAccounting.CreatePosting(ctx, CreatePostingRequest{
                DebitAccount:  order.FiatDebitAccount,
                CreditAccount: order.FiatCreditAccount,
                Amount:        order.receipt.OutputValue,
                Instrument:    order.receipt.OutputInstrumentCode,
                BucketID:      order.bucketID,  // Same bucket links asset & fiat
                ReceiptID:     order.receipt.ID,
            })
            if err != nil {
                return err
            }
            order.receipt.PostingID = &posting.ID
            return s.receiptStore.UpdatePostingID(ctx, order.receipt.ID, posting.ID)
        },
        Compensate: func() error {
            if order.receipt.PostingID != nil {
                return s.financialAccounting.ReversePosting(ctx, *order.receipt.PostingID)
            }
            return nil
        },
    })

    return saga.Execute(ctx)
}
```

### 4. Wash & Reload (Correction Flow)

```go
// services/payment-order/service/correction_order.go

type CorrectionPaymentOrder struct {
    OriginalReceiptID   uuid.UUID
    NewMeasurementValue decimal.Decimal
    Reason              CorrectionReason  // MEASUREMENT_CORRECTION, RATE_CORRECTION

    // Accounts
    FiatDebitAccount    string
    FiatCreditAccount   string  // Usually "revenue:adjustment"
}

type CorrectionReason string
const (
    CorrectionReasonMeasurement CorrectionReason = "MEASUREMENT_CORRECTION"
    CorrectionReasonRate        CorrectionReason = "RATE_CORRECTION"
)

func (s *PaymentOrderService) ProcessCorrection(
    ctx context.Context,
    correction *CorrectionPaymentOrder,
) error {
    saga := s.sagaOrchestrator.New("wash-and-reload")

    var original *ValuationReceipt
    var delta decimal.Decimal
    var deltaValue decimal.Decimal
    var correctionReceipt *ValuationReceipt

    // Step 1: Load original receipt
    saga.AddStep(SagaStep{
        Name: "load_original",
        Execute: func() error {
            r, err := s.receiptStore.Get(ctx, correction.OriginalReceiptID)
            if err != nil {
                return fmt.Errorf("load original receipt: %w", err)
            }
            original = r
            return nil
        },
    })

    // Step 2: Compute delta using ORIGINAL rate
    saga.AddStep(SagaStep{
        Name: "compute_delta",
        Execute: func() error {
            // Delta = new measurement - original measurement
            delta = correction.NewMeasurementValue.Sub(original.MeasurementValue)

            // Delta value = delta × ORIGINAL rate (NOT current rate)
            deltaValue, err := s.evalValueExpr(ctx,
                original.ValueExpression,
                delta.String(),
                original.RateValue.String())
            if err != nil {
                return fmt.Errorf("compute delta value: %w", err)
            }

            correctionReceipt = &ValuationReceipt{
                ID:                     uuid.New(),
                PaymentOrderID:         correction.PaymentOrderID,
                KnowledgeTime:          time.Now(),
                SupersedesReceiptID:    &original.ID,
                CorrectionReason:       string(correction.Reason),

                // New measurement value
                MeasurementInstrument:  original.MeasurementInstrument,
                MeasurementValue:       delta,
                MeasurementBucketID:    original.MeasurementBucketID,

                // ORIGINAL rate (frozen from original receipt)
                RateObservationID:      original.RateObservationID,
                RateDatasetCode:        original.RateDatasetCode,
                RateKey:                original.RateKey,
                RateValue:              original.RateValue,
                RateObservedAt:         original.RateObservedAt,
                RateValidFrom:          original.RateValidFrom,
                RateValidTo:            original.RateValidTo,
                RateKeyExpression:      original.RateKeyExpression,
                ValueExpression:        original.ValueExpression,

                // Output
                OutputInstrumentCode:   original.OutputInstrumentCode,
                OutputValue:            deltaValue,
            }

            return s.receiptStore.Save(ctx, correctionReceipt)
        },
    })

    // Step 3: Post delta to Asset Ledger
    saga.AddStep(SagaStep{
        Name: "post_asset_delta",
        Execute: func() error {
            _, err := s.positionKeeping.RecordMeasurement(ctx, RecordMeasurementRequest{
                AccountID:      original.AssetAccountID,
                InstrumentCode: original.MeasurementInstrument,
                Amount:         delta,
                BucketID:       original.MeasurementBucketID,
                ReceiptID:      correctionReceipt.ID,
            })
            return err
        },
    })

    // Step 4: Post delta to Fiat Ledger
    saga.AddStep(SagaStep{
        Name: "post_fiat_delta",
        Execute: func() error {
            posting, err := s.financialAccounting.CreatePosting(ctx, CreatePostingRequest{
                DebitAccount:  correction.FiatDebitAccount,
                CreditAccount: correction.FiatCreditAccount,
                Amount:        deltaValue,
                Instrument:    correctionReceipt.OutputInstrumentCode,
                BucketID:      original.MeasurementBucketID,
                ReceiptID:     correctionReceipt.ID,
            })
            if err != nil {
                return err
            }
            correctionReceipt.PostingID = &posting.ID
            return nil
        },
    })

    return saga.Execute(ctx)
}
```

### 5. Multi-Hop Valuation (kWh → CO2 → GBP)

The termination rule: **Monetary dimension = terminal** (no more hops).

```go
func (s *PaymentOrderService) ProcessCommodityOrderWithChain(
    ctx context.Context,
    order *CommodityPaymentOrder,
) error {
    currentInstrument := order.InstrumentCode
    currentAmount := order.Amount
    currentAttributes := order.Attributes
    receipts := []*ValuationReceipt{}

    for hop := 0; hop < MaxValuationHops; hop++ {
        // Load instrument
        inst, err := s.referenceData.GetActiveDefinition(ctx, currentInstrument)
        if err != nil {
            return err
        }

        // Check termination: Monetary dimension = done
        if inst.Dimension == "Monetary" {
            break
        }

        // Must have valuation binding
        if inst.Valuation == nil {
            return fmt.Errorf("instrument %s has no valuation binding", currentInstrument)
        }

        // Perform valuation (same logic as single-hop)
        receipt, err := s.valuateHop(ctx, inst, currentAmount, currentAttributes, order.Timestamp)
        if err != nil {
            return err
        }
        receipts = append(receipts, receipt)

        // Prepare for next hop
        currentInstrument = inst.Valuation.TargetInstrumentCode
        currentAmount = receipt.OutputValue
        // Attributes may transform or clear for intermediate instruments
    }

    // Final hop resulted in Monetary - post to Financial Accounting
    finalReceipt := receipts[len(receipts)-1]
    _, err := s.financialAccounting.CreatePosting(ctx, CreatePostingRequest{
        DebitAccount:  order.FiatDebitAccount,
        CreditAccount: order.FiatCreditAccount,
        Amount:        finalReceipt.OutputValue,
        Instrument:    finalReceipt.OutputInstrumentCode,
        BucketID:      order.bucketID,
        ReceiptID:     finalReceipt.ID,
    })

    return err
}

const MaxValuationHops = 5  // Safety limit
```

---

## Instrument Definition Examples

### KWH → GBP (Single Hop)

```yaml
code: "KWH"
version: 1
dimension: "Commodity"
precision: 4

validation_expression: |
  has(attrs.tou_period) &&
  int(attrs.tou_period) >= 0 &&
  int(attrs.tou_period) <= 47 &&
  has(attrs.tariff_zone)

fungibility_key_expression: |
  sha256(attrs.tou_period + "|" + attrs.tariff_zone)

valuation:
  target_instrument_code: "GBP"
  dataset_code: "UK_ENERGY_TARIFF"
  rate_key_expression: |
    attrs.tariff_zone + "/" + attrs.tou_period
  value_expression: |
    decimal(amount) * decimal(rate)
```

### KWH → CO2-KG → GBP (Multi-Hop)

```yaml
# Hop 1: KWH to Carbon
code: "KWH"
valuation:
  target_instrument_code: "CO2-KG"  # Intermediate (Commodity)
  dataset_code: "UK_GRID_CARBON_INTENSITY"
  rate_key_expression: |
    attrs.region + "/" + attrs.tou_period
  value_expression: |
    decimal(amount) * decimal(rate)

---
# Hop 2: Carbon to GBP
code: "CO2-KG"
dimension: "Commodity"
valuation:
  target_instrument_code: "GBP"  # Terminal (Monetary)
  dataset_code: "CARBON_TARIFF"
  rate_key_expression: |
    "standard"
  value_expression: |
    decimal(amount) * decimal(rate)
```

---

## API Extensions

### Payment Order gRPC

```protobuf
// Extend existing PaymentOrder service

message CreateCommodityPaymentOrderRequest {
    string account_id = 1;
    string party_id = 2;
    string instrument_code = 3;
    string amount = 4;  // Decimal as string
    map<string, string> attributes = 5;
    google.protobuf.Timestamp timestamp = 6;

    // Posting accounts
    string asset_debit_account = 7;
    string fiat_debit_account = 8;
    string fiat_credit_account = 9;
}

message CreateCorrectionPaymentOrderRequest {
    string original_receipt_id = 1;
    string new_measurement_value = 2;
    CorrectionReason reason = 3;
    string fiat_debit_account = 4;
    string fiat_credit_account = 5;
}

enum CorrectionReason {
    CORRECTION_REASON_UNSPECIFIED = 0;
    CORRECTION_REASON_MEASUREMENT = 1;
    CORRECTION_REASON_RATE = 2;
}
```

### Receipt Query gRPC

```protobuf
service ValuationReceiptService {
    rpc GetReceipt(GetReceiptRequest) returns (ValuationReceipt);
    rpc QueryReceipts(QueryReceiptsRequest) returns (QueryReceiptsResponse);
}

message GetReceiptRequest {
    string receipt_id = 1;
}

message QueryReceiptsRequest {
    string measurement_id = 1;
    string payment_order_id = 2;
    google.protobuf.Timestamp from_time = 3;
    google.protobuf.Timestamp to_time = 4;
    int32 page_size = 5;
    string page_token = 6;
}
```

---

## Work Streams

### Stream A: Reference Data Extension (1-2 weeks)

1. Add ValuationBinding to InstrumentDefinition
2. Database migration for valuation columns
3. CEL environment for rate_key and value expressions
4. Compile and cache valuation CEL programs
5. Update gRPC API for Register/Retrieve

### Stream B: Valuation Receipt (1 week)

1. Create valuation_receipts table migration
2. Domain model for ValuationReceipt
3. Repository with Save, Get, Query
4. gRPC handlers for receipt queries

### Stream C: Payment Order Integration (2 weeks)

1. CommodityPaymentOrder flow with dual-posting
2. Valuation step in saga
3. Market Information client integration
4. Rate lookup by dataset + key

### Stream D: Correction Flow (1 week)

1. CorrectionPaymentOrder implementation
2. Delta calculation using original rate
3. Supersedes linkage between receipts

### Stream E: Multi-Hop Valuation (1 week)

1. Chain traversal logic
2. Termination on Monetary dimension
3. Hop limit safety

### Stream F: Integration Tests (1 week)

1. Single-hop kWh → GBP
2. Multi-hop kWh → CO2 → GBP
3. Wash & Reload correction
4. Audit trail verification

---

## Acceptance Criteria

### AC1: Single-Hop Valuation

```gherkin
Given an instrument "KWH" with valuation binding to "GBP"
  And dataset "UK_ENERGY_TARIFF" has rate £0.35/kWh for zone "PEAK" period "14"
When a measurement of 10 kWh arrives with attributes {tariff_zone: "PEAK", tou_period: "14"}
Then Position Keeping records 10 kWh with bucket_id
  And Financial Accounting records £3.50 with same bucket_id
  And a ValuationReceipt links both with frozen inputs
```

### AC2: Wash & Reload

```gherkin
Given an existing valuation receipt for 10 kWh = £3.50 (rate: £0.35)
When measurement is corrected to 12 kWh
Then a correction receipt is created:
  - Delta: 2 kWh
  - Rate: £0.35 (ORIGINAL, not current)
  - Delta value: £0.70
  And Position Keeping records +2 kWh
  And Financial Accounting records +£0.70 adjustment
  And correction receipt links to original via supersedes_receipt_id
```

### AC3: Audit Trail

```gherkin
Given a Financial Accounting posting for £3.50
When auditor queries receipt by posting_id
Then receipt shows:
  - Measurement: 10 kWh (id, bucket_id)
  - Rate: £0.35 (observation_id, dataset, key, valid_from, valid_to)
  - CEL expressions used
  - Knowledge time when computed
  And auditor can replay: eval(value_expression, amount, rate) = £3.50
```

---

## Non-Goals (Phase 2)

1. **Indicative (real-time) valuation** - computed views without posting
2. **Aggregate settlement orders** - prosumer netting, billing periods
3. **Multi-lens valuation** - retail/wholesale/spread in single flow
4. **Rate corrections** - when market data changes after initial valuation

---

## Dependencies

| Dependency | Status | Owner |
|------------|--------|-------|
| Market Information bi-temporal queries | ✅ Live | Market Info team |
| Position Keeping BucketID support | ✅ Live | Position Keeping team |
| Financial Accounting BucketID validation | ✅ Live | FA team |
| Reference Data CEL compiler | ✅ Live | Reference Data team |
| Payment Order saga infrastructure | ✅ Live | Payment Order team |

---

## Success Metrics

| Metric | Target |
|--------|--------|
| Valuation latency (p99) | < 50ms |
| Receipt storage overhead | < 1KB per receipt |
| CEL evaluation time | < 1ms (rate_key + value combined) |
| Saga success rate | > 99.9% |
| Audit trace completeness | 100% (every posting has receipt) |
