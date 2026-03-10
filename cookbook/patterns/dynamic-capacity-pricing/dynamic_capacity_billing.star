# Saga: dynamic_capacity_billing
# Version: 2.0.0
# Previous: 1.0.0
# Changed: Thin event pattern — resolve billing account and region from entity graph
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
# Input data (from TransactionCapturedEvent via AsyncAPI-driven deserialization):
#   - correlation_id: string - Idempotency key from standard headers
#   - account_id: string - The usage account that triggered the event
#   - instrument_amount: dict - Multi-asset amount {amount, instrument_code}
#   - timestamp: string - ISO 8601 timestamp of consumption
#
# Entity graph resolution (via service module calls):
#   - billing_account_id: from account.metadata
#   - region_code: from account.metadata (each usage account is per-region)

# Define the saga
dynamic_billing_saga = saga(name="dynamic_capacity_billing")

def execute_dynamic_billing():
    ctx = input_data

    correlation_id = ctx["correlation_id"]
    source_account_id = ctx["account_id"]
    tokens = Decimal(ctx["instrument_amount"]["amount"])
    consumption_time = ctx["timestamp"]

    # Resolve account details from entity graph.
    # Each usage account is scoped to a specific data centre region.
    # The region_code in account metadata determines which price curve to query.
    step(name="lookup_account")
    account = reference_data.get_account(id=source_account_id)

    billing_account_id = account.metadata["billing_account_id"]
    region_code = account.metadata["region_code"]

    # Idempotency check
    step(name="check_idempotency")
    existing = position_keeping.query_logs(
        correlation_id=correlation_id,
        instrument_code="USD",
        position_id=billing_account_id,
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
        effective_at=consumption_time,
    )

    # Compute charge: tokens x dynamic regional rate
    rate = Decimal(price_observation.value)
    charge_amount = tokens * rate

    # Book USD charge on customer billing account
    step(name="book_charge")
    position_keeping.initiate_log(
        position_id=billing_account_id,
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
