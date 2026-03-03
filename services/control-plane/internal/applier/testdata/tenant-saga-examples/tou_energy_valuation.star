# Saga: tou_energy_valuation
# Version: 2.0.0
# Previous: 1.0.0
# Changed: Thin event pattern — resolve billing account from entity graph,
#          use event timestamp as settlement period for temporal rate lookup
# Author: Tenant Configuration (Energy)
# Date: 2026-03-03
#
# Time-of-use energy valuation saga. Values kWh meter reads at the dynamic
# rate for the settlement period when consumption occurred, rather than a
# flat rate. The rate comes from a forecast-derived price curve published to
# Market Data by the Forecasting Service.
#
# This extends the cross-instrument valuation pattern (usage_to_value.star)
# with temporal awareness: different half-hours have different prices.
#
# Trigger: event:position-keeping.transaction-captured.v1
# Filter:  event.instrument_code == 'KWH' && event.direction == 'DEBIT'
#
# Account model:
#   - Metered Account (kWh): source transaction (consumption)
#   - Billing Account (GBP): time-of-use charge at dynamic rate
#
# The valuation engine resolves the rate by looking up the market data
# observation for the given value_date in the configured price curve dataset.
# Different settlement periods map to different rates (peak, off-peak,
# overnight) based on the forecast curve published by the Forecasting Service.
#
# Input data (from TransactionCapturedEvent via AsyncAPI-driven deserialization):
#   - correlation_id: string - Idempotency key from standard headers
#   - account_id: string - The metered account that triggered the event
#   - instrument_amount: dict - Multi-asset amount {amount, instrument_code}
#   - timestamp: string - ISO 8601 event timestamp (used as settlement period)
#   - reference: string - External reference (e.g., MPAN identifier)
#
# Entity graph resolution (via service module calls):
#   - billing_account_id: from account.metadata
#   - account_type_code: from reference_data.get_account(id=account_id)
#   - conversion method: from reference_data.get_account_type(code=...)

# Define the saga
tou_valuation_saga = saga(name="tou_energy_valuation")

def execute_tou_valuation():
    ctx = input_data

    correlation_id = ctx["correlation_id"]
    source_account_id = ctx["account_id"]
    amount = Decimal(ctx["instrument_amount"]["amount"])
    # The event timestamp represents the settlement period start.
    # For half-hourly reads, this is the start of the 30-minute window.
    settlement_period = ctx["timestamp"]

    # Resolve account details from entity graph
    step(name="lookup_account")
    account = reference_data.get_account(id=source_account_id)

    billing_account_id = account.metadata["billing_account_id"]

    # Idempotency check
    step(name="check_idempotency")
    existing = position_keeping.query_logs(
        correlation_id=correlation_id,
        instrument_code="GBP",
        account_id=billing_account_id,
    )

    if existing.count > 0:
        return {"status": "ALREADY_VALUED", "correlation_id": correlation_id}

    # Look up account type for its time-of-use valuation method
    step(name="lookup_account_type")
    account_type = reference_data.get_account_type(
        code=account.account_type_code,
    )

    # Compute value at the time-of-use rate for this settlement period.
    # The valuation engine uses value_date to look up the correct rate from
    # the forecast-derived price curve in Market Data. Different half-hours
    # have different rates (peak, off-peak, overnight).
    step(name="compute_tou_valuation")
    charge = valuation_engine.compute(
        method_id=account_type.default_conversion_method_id,
        amount=amount,
        from_instrument="KWH",
        to_instrument="GBP",
        value_date=settlement_period,
    )

    # Book GBP charge on customer billing account
    step(name="book_charge")
    position_keeping.initiate_log(
        account_id=billing_account_id,
        instrument_code="GBP",
        direction="DEBIT",
        amount=charge.amount,
        correlation_id=correlation_id,
        description="ToU charge: " + settlement_period,
    )

    return {
        "status": "VALUED",
        "amount": str(charge.amount),
        "settlement_period": settlement_period,
        "correlation_id": correlation_id,
    }

# Execute the saga
output = execute_tou_valuation()
