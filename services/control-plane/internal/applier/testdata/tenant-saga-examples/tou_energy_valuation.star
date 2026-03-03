# Saga: tou_energy_valuation
# Version: 1.0.0
# Previous: none
# Changed: Initial version
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
# Input data (from event payload via input_data dictionary):
#   - correlation_id: string - Idempotency key from source event
#   - account_type_code: string - Account type code for valuation method lookup
#   - amount: string - Decimal amount (kWh consumed in this settlement period)
#   - settlement_period_start: string - ISO 8601 start of the half-hour period
#   - billing_account_id: string - Customer billing account for GBP charge

# Define the saga
tou_valuation_saga = saga(name="tou_energy_valuation")

def execute_tou_valuation():
    ctx = input_data

    correlation_id = ctx["correlation_id"]
    billing_account_id = ctx["billing_account_id"]

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
        code=ctx["account_type_code"],
    )

    # Compute value at the time-of-use rate for this settlement period.
    # The valuation engine uses value_date to look up the correct rate from
    # the forecast-derived price curve in Market Data. Different half-hours
    # have different rates (peak, off-peak, overnight).
    step(name="compute_tou_valuation")
    charge = valuation_engine.compute(
        method_id=account_type.default_conversion_method_id,
        amount=Decimal(ctx["amount"]),
        from_instrument="KWH",
        to_instrument="GBP",
        value_date=ctx["settlement_period_start"],
    )

    # Book GBP charge on customer billing account
    step(name="book_charge")
    position_keeping.initiate_log(
        account_id=billing_account_id,
        instrument_code="GBP",
        direction="DEBIT",
        amount=charge.amount,
        correlation_id=correlation_id,
        description="ToU charge: " + ctx["settlement_period_start"],
    )

    return {
        "status": "VALUED",
        "amount": str(charge.amount),
        "settlement_period": ctx["settlement_period_start"],
        "correlation_id": correlation_id,
    }

# Execute the saga
output = execute_tou_valuation()
