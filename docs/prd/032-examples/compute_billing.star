# Saga: compute_billing
# Version: 1.0.0
# Previous: none
# Changed: Initial version
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
# Input data (from event payload via input_data dictionary):
#   - correlation_id: string - Idempotency key from source event
#   - account_type_code: string - Account type code for valuation method lookup
#   - amount: string - Decimal amount as string (GPU hours)
#   - billing_account_id: string - Customer billing account for USD charge

# Define the saga
compute_billing_saga = saga(name="compute_billing")

def execute_compute_billing():
    ctx = input_data

    correlation_id = ctx["correlation_id"]
    billing_account_id = ctx["billing_account_id"]

    # Idempotency check
    step(name="check_idempotency")
    existing = position_keeping.query_logs(
        correlation_id=correlation_id,
        instrument_code="USD",
        account_id=billing_account_id,
    )

    if existing.count > 0:
        return {"status": "ALREADY_BILLED", "correlation_id": correlation_id}

    # Look up account type for its default conversion method
    step(name="lookup_account_type")
    account_type = reference_data.get_account_type(
        code=ctx["account_type_code"],
    )

    # Convert GPU hours to USD
    step(name="compute_charge")
    charge = valuation_engine.compute(
        method_id=account_type.default_conversion_method_id,
        amount=Decimal(ctx["amount"]),
        from_instrument="GPU_HOUR",
        to_instrument="USD",
    )

    # Book USD charge on customer billing account
    step(name="book_charge")
    position_keeping.initiate_log(
        account_id=billing_account_id,
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
