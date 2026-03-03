# Saga: dynamic_capacity_billing
# Version: 1.0.0
# Previous: none
# Changed: Initial version
# Author: Tenant Configuration (Cloud Compute)
# Date: 2026-03-03
#
# Dynamic regional pricing for compute tokens. Bills token consumption at
# rates derived from the platform's own utilisation forecasts per data centre
# region — creating a feedback loop where usage patterns drive pricing, and
# pricing shapes usage patterns.
#
# The feedback loop:
#   1. TOKEN positions accumulate per region (position-keeping)
#   2. Forecasting Service analyses historical utilisation to generate
#      demand curves per region (forecasting -> market-data)
#   3. This saga reads the regional dynamic price at the consumption timestamp
#      and bills accordingly (market-data -> position-keeping)
#   4. The resulting charges are also positions, closing the loop
#
# Trigger: event:position-keeping.transaction-captured.v1
# Filter:  event.instrument_code == 'TOKEN' && event.direction == 'DEBIT'
#
# Account model:
#   - Usage Account (TOKEN): source transaction (token consumption)
#   - Billing Account (USD): dynamic charge at regional rate
#
# The saga queries Market Data directly for the regional price observation
# at the consumption timestamp, rather than delegating to the valuation engine.
# This demonstrates the market data query pattern for time-and-region-specific
# dynamic pricing.
#
# Input data (from event payload via input_data dictionary):
#   - correlation_id: string - Idempotency key from source event
#   - amount: string - Decimal amount (tokens consumed)
#   - region_code: string - Data centre region (e.g., "US_EAST_1", "EU_WEST_1")
#   - billing_account_id: string - Customer billing account for USD charge
#   - value_date: string - ISO 8601 timestamp of consumption

# Define the saga
dynamic_billing_saga = saga(name="dynamic_capacity_billing")

def execute_dynamic_billing():
    ctx = input_data

    correlation_id = ctx["correlation_id"]
    billing_account_id = ctx["billing_account_id"]
    region_code = ctx["region_code"]

    # Idempotency check
    step(name="check_idempotency")
    existing = position_keeping.query_logs(
        correlation_id=correlation_id,
        instrument_code="USD",
        account_id=billing_account_id,
    )

    if existing.count > 0:
        return {"status": "ALREADY_BILLED", "correlation_id": correlation_id}

    # Query the regional dynamic price from Market Data.
    # The dataset code encodes the region: TOKEN_PRICE_{REGION}.
    # The Forecasting Service publishes these as ESTIMATE quality observations
    # derived from the platform's own utilisation data.
    dataset_code = "TOKEN_PRICE_" + region_code
    step(name="lookup_regional_price")
    price_observation = market_data.get_observation(
        dataset_code=dataset_code,
        effective_at=ctx["value_date"],
    )

    # Compute charge: tokens x dynamic regional rate
    tokens = Decimal(ctx["amount"])
    rate = Decimal(price_observation.value)
    charge_amount = tokens * rate

    # Book USD charge on customer billing account
    step(name="book_charge")
    position_keeping.initiate_log(
        account_id=billing_account_id,
        instrument_code="USD",
        direction="DEBIT",
        amount=charge_amount,
        correlation_id=correlation_id,
        description="Dynamic rate: " + region_code + " @ " + str(rate),
    )

    return {
        "status": "BILLED",
        "amount": str(charge_amount),
        "rate": str(rate),
        "region": region_code,
        "correlation_id": correlation_id,
    }

# Execute the saga
output = execute_dynamic_billing()
