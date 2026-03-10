# Saga: compute_billing
# Version: 2.0.0
# Previous: 1.0.0
# Changed: Thin event pattern — resolve billing account from entity graph
# Author: Tenant Configuration (Cloud Compute)
# Date: 2026-03-03
#
# Usage billing saga for compute resources.
# When GPU_HOUR positions are captured, this saga converts them to USD charges
# using the account type's default conversion method.
#
# Trigger: event:position-keeping.transaction-captured.v1
# Filter:  event.instrument_code == 'GPU_HOUR' && event.direction == 'DEBIT'
#
# Single-leg valuation (simpler than energy which has retail + wholesale).
#
# Input data (from TransactionCapturedEvent via AsyncAPI-driven deserialization):
#   - correlation_id: string - Idempotency key from standard headers
#   - account_id: string - The usage account that triggered the event
#   - instrument_code: string - "GPU_HOUR"
#   - instrument_amount: dict - Multi-asset amount {amount, instrument_code}
#
# Entity graph resolution (via service module calls):
#   - account_type_code: from reference_data.get_account(id=account_id)
#   - billing_account_id: from account.metadata
#   - conversion method: from reference_data.get_account_type(code=...)

# Define the saga
compute_billing_saga = saga(name="compute_billing")

def execute_compute_billing():
    ctx = input_data

    correlation_id = ctx["correlation_id"]
    source_account_id = ctx["account_id"]
    amount = Decimal(ctx["instrument_amount"]["amount"])

    # Resolve account details from entity graph
    step(name="lookup_account")
    account = reference_data.get_account(id=source_account_id)

    billing_account_id = account.metadata["billing_account_id"]

    # Idempotency check
    step(name="check_idempotency")
    existing = position_keeping.query_logs(
        correlation_id=correlation_id,
        instrument_code="USD",
        position_id=billing_account_id,
    )

    if existing.count > 0:
        return {"status": "ALREADY_BILLED", "correlation_id": correlation_id}

    # Look up account type for its default conversion method
    step(name="lookup_account_type")
    account_type = reference_data.get_account_type(
        code=account.account_type_code,
    )

    # Convert GPU hours to USD
    step(name="compute_charge")
    charge = valuation_engine.compute(
        method_id=account_type.default_conversion_method_id,
        amount=amount,
        from_instrument="GPU_HOUR",
        to_instrument="USD",
    )

    # Book USD charge on customer billing account
    step(name="book_charge")
    position_keeping.initiate_log(
        position_id=billing_account_id,
        instrument_code="USD",
        direction="DEBIT",
        amount=charge.amount,
        correlation_id=correlation_id,
    )

    return {
        "status": "BILLED",
        "amount": str(charge.amount),
        "correlation_id": correlation_id,
    }

# Execute the saga
output = execute_compute_billing()
