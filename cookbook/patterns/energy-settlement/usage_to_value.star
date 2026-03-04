# Saga: usage_to_value
# Version: 2.0.0
# Previous: 1.0.0
# Changed: Thin event pattern — resolve entity graph from event payload fields
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
# Input data (from TransactionCapturedEvent via AsyncAPI-driven deserialization):
#   - correlation_id: string - Idempotency key from standard headers
#   - log_id: string (UUID) - Position log identifier
#   - account_id: string - The metered account that triggered the event
#   - transaction_id: string (UUID) - Transaction identifier
#   - amount_cents: int - Amount in smallest unit
#   - instrument_code: string - Source instrument (e.g., "KWH")
#   - direction: string - "DEBIT" or "CREDIT"
#   - instrument_amount: dict - Multi-asset amount {amount, instrument_code}
#
# Entity graph resolution (via service module calls):
#   - account_type_code: from reference_data.get_account(id=account_id)
#   - billing_account_id: from account.metadata
#   - counterparty_account_id: from account.metadata
#   - valuation methods: from reference_data.get_account_type(code=...)

# Define the saga
usage_to_value_saga = saga(name="usage_to_value")

def execute_usage_to_value():
    ctx = input_data

    correlation_id = ctx["correlation_id"]
    source_account_id = ctx["account_id"]
    amount = Decimal(ctx["instrument_amount"]["amount"])
    instrument_code = ctx["instrument_code"]

    # Resolve account details from the entity graph.
    # The event carries only the account_id — the saga looks up
    # the account type, billing accounts, and counterparty via
    # reference data service modules.
    step(name="lookup_account")
    account = reference_data.get_account(id=source_account_id)

    metadata = account.metadata or {}
    billing_account_id = metadata.get("billing_account_id")
    counterparty_account_id = metadata.get("counterparty_account_id")
    if not billing_account_id or not counterparty_account_id:
        return {
            "status": "CONFIG_ERROR",
            "correlation_id": correlation_id,
        }

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
    if retail_logs.count > 0 or wholesale_logs.count > 0:
        return {
            "status": "PARTIAL_ALREADY_PROCESSED",
            "correlation_id": correlation_id,
        }

    # Look up account type for valuation method references.
    # The account type's DefaultConversionMethodID and ValuationMethods
    # fields drive the valuation logic.
    step(name="lookup_account_type")
    account_type = reference_data.get_account_type(
        code=account.account_type_code,
    )

    # Validate config before any side-effecting booking steps.
    # Fail fast here so retries leave no orphaned legs.
    valuation_methods = account_type.valuation_methods
    if len(valuation_methods) < 2:
        return {
            "status": "CONFIG_ERROR",
            "correlation_id": correlation_id,
        }
    wholesale_method = valuation_methods[1]

    # Compute both valuations before booking either leg.
    # This ensures that if wholesale valuation fails, no retail booking
    # has been created — retries remain safe with no partial state.
    step(name="compute_retail_valuation")
    retail = valuation_engine.compute(
        method_id=account_type.default_conversion_method_id,
        amount=amount,
        from_instrument=instrument_code,
        to_instrument="GBP",
    )

    step(name="compute_wholesale_valuation")
    wholesale = valuation_engine.compute(
        method_id=wholesale_method.method_id,
        amount=amount,
        from_instrument=instrument_code,
        to_instrument="GBP",
    )

    # Book both legs only after both valuations succeed.
    step(name="book_retail_position")
    position_keeping.initiate_log(
        account_id=billing_account_id,
        instrument_code="GBP",
        direction="DEBIT",
        amount=retail.amount,
        correlation_id=correlation_id,
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
