# Saga: valuation_on_capture
# Version: 1.0.0
# Author: Tenant Configuration (Precious Metals Trading)
# Date: 2026-03-04
#
# Books GBP settlement value when a precious metals position is captured.
# When a GOLD, SILVER, or PLATINUM position is captured, this saga looks up
# the current spot price via market data and creates a GBP settlement posting.
#
# Trigger: event:position-keeping.transaction-captured.v1
# Filter:  event.instrument_code in ['GOLD', 'SILVER', 'PLATINUM']
#
# Single-leg valuation: precious metal quantity -> GBP at spot rate.
#
# Idempotency: checks a GBP posting exists for this correlation_id before
# proceeding.
# Chain termination: GBP positions emitted downstream are rejected by the
# CEL filter (instrument_code in [...] does not include 'GBP').
#
# Input data (from TransactionCapturedEvent via AsyncAPI-driven deserialization):
#   - correlation_id: string - Idempotency key from standard headers
#   - log_id: string (UUID) - Position log identifier
#   - account_id: string - The trading account that triggered the event
#   - transaction_id: string (UUID) - Transaction identifier
#   - amount_cents: int - Amount in smallest unit
#   - instrument_code: string - Source instrument ('GOLD', 'SILVER', 'PLATINUM')
#   - direction: string - 'DEBIT' or 'CREDIT'
#   - instrument_amount: dict - Multi-asset amount {amount, instrument_code}
#
# Entity graph resolution (via service module calls):
#   - settlement_account_id: from reference_data.get_account(id=account_id)
#   - valuation method: from reference_data.get_account_type(code=...)

# Define the saga
valuation_on_capture_saga = saga(name="valuation_on_capture")

def execute_valuation_on_capture():
    ctx = input_data

    correlation_id = ctx["correlation_id"]
    source_account_id = ctx["account_id"]
    amount = Decimal(ctx["instrument_amount"]["amount"])
    instrument_code = ctx["instrument_code"]
    direction = ctx["direction"]

    # Resolve account details from the entity graph.
    # The event carries only the account_id — the saga looks up the account
    # type and settlement account via reference data service modules.
    step(name="lookup_account")
    account = reference_data.get_account(id=source_account_id)

    settlement_account_id = account.metadata["settlement_account_id"]

    # Idempotency check: has the GBP settlement already been booked?
    step(name="check_idempotency")
    existing = position_keeping.query_logs(
        correlation_id=correlation_id,
        instrument_code="GBP",
        position_id=settlement_account_id,
    )

    if existing.count > 0:
        return {"status": "ALREADY_VALUED", "correlation_id": correlation_id}

    # Look up account type for the spot rate valuation method.
    step(name="lookup_account_type")
    account_type = reference_data.get_account_type(
        code=account.account_type_code,
    )

    # Compute GBP settlement value at current spot rate.
    step(name="compute_valuation")
    valuation = valuation_engine.compute(
        method_id=account_type.default_conversion_method_id,
        amount=amount,
        from_instrument=instrument_code,
        to_instrument="GBP",
    )

    # Book GBP settlement posting — direction mirrors the source position.
    step(name="book_settlement")
    position_keeping.initiate_log(
        position_id=settlement_account_id,
        instrument_code="GBP",
        direction=direction,
        amount=valuation.amount,
        correlation_id=correlation_id,
        description="Spot valuation: " + instrument_code + " -> GBP",
    )

    return {
        "status": "VALUED",
        "instrument_code": instrument_code,
        "gbp_amount": str(valuation.amount),
        "correlation_id": correlation_id,
    }

# Execute the saga
output = execute_valuation_on_capture()
