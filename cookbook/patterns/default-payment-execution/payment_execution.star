# schema-validation: skip
# Saga: payment_execution
# Version: 1.0.0
# Previous: none
# Changed: Migrated from step(action=...) to typed service modules
# Author: Platform Team
# Date: 2026-01-27
#
# Payment Order execution saga with bucket-aware lien, gateway submission, and ledger posting.
#
# This saga orchestrates the complete payment flow:
# 1. Reserve funds via lien with optional bucket-aware solvency
# 2. Send payment to external gateway
# 3. Post ledger entries (triggered by webhook after gateway confirmation)
# 4. Execute lien to finalize debit (triggered by webhook after ledger posted)
#
# Steps 3 and 4 are conditional - they are invoked asynchronously by webhook handlers
# rather than immediately after step 2.
#
# Compensation handlers are declared in handlers.yaml schema.
#
# Input parameters (from input_data dict):
#   - payment_order_id: string (required)
#   - debtor_account_id: string (required)
#   - creditor_reference: string (required)
#   - amount_cents: int64 (required)
#   - currency: string (required, e.g., "GBP", "USD")
#   - idempotency_key: string (required)
#   - instrument_code: string (optional, for bucket evaluation)
#   - payment_attributes: dict (optional, attributes for CEL bucket expression)
#   - should_post_ledger: bool (optional, default false - set by webhook)
#   - should_execute_lien: bool (optional, default false - set by webhook)
#   - internal_clearing_enabled: bool (optional, for 4-posting ledger flow)

def payment_execution():
    """
    Main saga entry point.
    Executes payment order with reserve -> send -> post ledger -> execute lien flow.

    Handles:
    - Bucket-aware solvency for non-fungible instruments
    - Gateway submission with idempotency
    - Internal clearing account routing (2 or 4 posting flow)
    - Lien execution with retry
    """

    # Extract input data
    ctx = input_data

    # Step 1: Reserve funds with bucket-aware lien
    step(name="reserve_funds")
    lien_result = payment_order.create_lien(
        account_id=ctx.get("debtor_account_id"),
        amount_cents=ctx.get("amount_cents"),
        currency=ctx.get("currency"),
        payment_order_id=ctx.get("payment_order_id"),
        instrument_code=ctx.get("instrument_code", ""),
        payment_attributes=ctx.get("payment_attributes", {}),
    )

    # Store lien results in context for subsequent steps
    lien_id = lien_result.lien_id
    bucket_id = lien_result.bucket_id
    # Extract valuation_analysis if present (atomic valuation audit trail).
    # Handler results are Starlark structs, so use getattr for optional fields.
    valuation_analysis = getattr(lien_result, "valuation_analysis", None)

    # Step 2: Send payment to gateway
    step(name="send_to_gateway")
    gateway_result = payment_order.send_to_gateway(
        payment_order_id=ctx.get("payment_order_id"),
        debtor_account_id=ctx.get("debtor_account_id"),
        creditor_reference=ctx.get("creditor_reference"),
        amount_cents=ctx.get("amount_cents"),
        currency=ctx.get("currency"),
        idempotency_key=ctx.get("idempotency_key"),
    )

    gateway_reference_id = gateway_result.gateway_reference_id
    gateway_status = gateway_result.gateway_status

    # Build initial result (returned after step 2 completes)
    # Steps 3 and 4 are conditional based on webhook flags
    result = {
        "lien_id": lien_id,
        "bucket_id": bucket_id,
        "gateway_reference_id": gateway_reference_id,
        "gateway_status": gateway_status,
    }

    # Step 3: Post ledger entries (conditional - triggered by webhook)
    # This step is called after the gateway confirms the payment via webhook
    if ctx.get("should_post_ledger", False):
        step(name="post_ledger_entries")
        ledger_params = {
            "payment_order_id": ctx.get("payment_order_id"),
            "debtor_account_id": ctx.get("debtor_account_id"),
            "gateway_reference_id": gateway_reference_id,
            "amount_cents": ctx.get("amount_cents"),
            "currency": ctx.get("currency"),
            "idempotency_key": ctx.get("idempotency_key"),
            "internal_clearing_enabled": ctx.get("internal_clearing_enabled", False),
        }
        # Forward valuation_analysis for position keeping audit trail
        if valuation_analysis:
            ledger_params["valuation_analysis"] = valuation_analysis
        ledger_result = payment_order.post_ledger_entries(**ledger_params)
        result["booking_log_id"] = ledger_result.booking_log_id

    # Include valuation_analysis in saga output for audit trail
    if valuation_analysis:
        result["valuation_analysis"] = valuation_analysis

    # Step 4: Execute lien (conditional - triggered by webhook after ledger posted)
    # This converts the reservation to an actual debit
    if ctx.get("should_execute_lien", False):
        if lien_id:
            step(name="execute_lien")
            execution_result = payment_order.execute_lien(
                lien_id=lien_id,
            )
            result["lien_execution_status"] = execution_result.execution_status

    return result

# Execute the saga
output = payment_execution()
