# Saga: usage_to_value
# Version: 1.0.0
# Previous: none
# Changed: Initial version
# Author: Tenant Configuration (Energy)
# Date: 2026-03-03
#
# Cross-instrument valuation saga for usage-metered positions.
# When a non-settlement-currency position is captured (e.g., kWh, GPU_HOUR),
# this saga values it and books the settlement currency equivalent.
#
# Trigger: event:position-keeping.transaction-captured.v1
# Filter:  event.instrument_code != 'GBP' && event.direction == 'DEBIT'
#
# The saga creates two settlement currency positions:
#   1. Customer billing (retail rate via account type's default conversion method)
#   2. Counterparty wholesale (wholesale rate via second valuation method)
#
# Idempotency: checks both legs exist for this correlation_id before proceeding.
# Chain termination: GBP positions emitted downstream are rejected by the CEL filter.
#
# Input data (from event payload via input_data dictionary):
#   - correlation_id: string - Idempotency key from source event
#   - account_type_code: string - Account type code for valuation method lookup
#   - amount: string - Decimal amount as string
#   - instrument_code: string - Source instrument (e.g., "KWH", "GPU_HOUR")
#   - billing_account_id: string - Customer billing account
#   - counterparty_account_id: string - Wholesale counterparty account

# Define the saga
usage_to_value_saga = saga(name="usage_to_value")

def execute_usage_to_value():
    ctx = input_data

    correlation_id = ctx["correlation_id"]
    billing_account_id = ctx["billing_account_id"]
    counterparty_account_id = ctx["counterparty_account_id"]

    # Idempotency: check both legs are complete (not just one)
    step(name="check_retail_idempotency")
    retail_logs = position_keeping.query_logs(
        correlation_id=correlation_id,
        instrument_code="GBP",
        account_id=billing_account_id,
    )

    step(name="check_wholesale_idempotency")
    wholesale_logs = position_keeping.query_logs(
        correlation_id=correlation_id,
        instrument_code="GBP",
        account_id=counterparty_account_id,
    )

    if retail_logs.count > 0 and wholesale_logs.count > 0:
        return {"status": "ALREADY_PROCESSED", "correlation_id": correlation_id}

    # Look up account type to get valuation method references
    # These are the DefaultConversionMethodID and ValuationMethods fields
    # defined on the account type in reference-data
    step(name="lookup_account_type")
    account_type = reference_data.get_account_type(
        code=ctx["account_type_code"],
    )

    # Value at retail rate using the account type's default conversion method
    step(name="compute_retail_valuation")
    retail = valuation_engine.compute(
        method_id=account_type.default_conversion_method_id,
        amount=Decimal(ctx["amount"]),
        from_instrument=ctx["instrument_code"],
        to_instrument="GBP",
    )

    # Book customer billing position
    step(name="book_retail_position")
    position_keeping.initiate_log(
        account_id=billing_account_id,
        instrument_code="GBP",
        direction="DEBIT",
        amount=retail.amount,
        correlation_id=correlation_id,
    )

    # Value at wholesale rate (second entry in ValuationMethods array)
    wholesale_method = account_type.valuation_methods[1]
    step(name="compute_wholesale_valuation")
    wholesale = valuation_engine.compute(
        method_id=wholesale_method.method_id,
        amount=Decimal(ctx["amount"]),
        from_instrument=ctx["instrument_code"],
        to_instrument="GBP",
    )

    step(name="book_wholesale_position")
    position_keeping.initiate_log(
        account_id=counterparty_account_id,
        instrument_code="GBP",
        direction="CREDIT",
        amount=wholesale.amount,
        correlation_id=correlation_id,
    )

    return {
        "status": "VALUED",
        "retail_amount": str(retail.amount),
        "wholesale_amount": str(wholesale.amount),
        "correlation_id": correlation_id,
    }

# Execute the saga
output = execute_usage_to_value()
