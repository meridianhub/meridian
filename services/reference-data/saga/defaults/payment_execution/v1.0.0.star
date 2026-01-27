# Saga: payment_execution
# Version: 1.0.0
# Previous: none
# Changed: Initial version
# Author: Platform Team
# Date: 2026-01-26
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
    lien_result = step(
        name = "reserve_funds",
        action = "payment_order.create_lien",
        params = {
            "account_id": ctx.get("debtor_account_id"),
            "amount_cents": ctx.get("amount_cents"),
            "currency": ctx.get("currency"),
            "payment_order_id": ctx.get("payment_order_id"),
            "instrument_code": ctx.get("instrument_code", ""),
            "payment_attributes": ctx.get("payment_attributes", {}),
        },
        compensate = {
            "action": "payment_order.terminate_lien",
            "params_from_result": ["lien_id"],
            "additional_params": {
                "reason": "Payment order saga compensation",
            },
        },
    )

    # Store lien results in context for subsequent steps
    lien_id = lien_result.get("lien_id", "")
    bucket_id = lien_result.get("bucket_id", "")

    # Step 2: Send payment to gateway
    gateway_result = step(
        name = "send_to_gateway",
        action = "payment_order.send_to_gateway",
        params = {
            "payment_order_id": ctx.get("payment_order_id"),
            "debtor_account_id": ctx.get("debtor_account_id"),
            "creditor_reference": ctx.get("creditor_reference"),
            "amount_cents": ctx.get("amount_cents"),
            "currency": ctx.get("currency"),
            "idempotency_key": ctx.get("idempotency_key"),
        },
        # No compensation for gateway send - lien release handles rollback
    )

    gateway_reference_id = gateway_result.get("gateway_reference_id", "")
    gateway_status = gateway_result.get("gateway_status", "")

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
        ledger_result = step(
            name = "post_ledger_entries",
            action = "payment_order.post_ledger_entries",
            params = {
                "payment_order_id": ctx.get("payment_order_id"),
                "debtor_account_id": ctx.get("debtor_account_id"),
                "gateway_reference_id": gateway_reference_id,
                "amount_cents": ctx.get("amount_cents"),
                "currency": ctx.get("currency"),
                "idempotency_key": ctx.get("idempotency_key"),
                "internal_clearing_enabled": ctx.get("internal_clearing_enabled", False),
            },
        )
        result["booking_log_id"] = ledger_result.get("booking_log_id", "")

    # Step 4: Execute lien (conditional - triggered by webhook after ledger posted)
    # This converts the reservation to an actual debit
    if ctx.get("should_execute_lien", False):
        if lien_id:
            execution_result = step(
                name = "execute_lien",
                action = "payment_order.execute_lien",
                params = {
                    "lien_id": lien_id,
                },
            )
            result["lien_execution_status"] = execution_result.get("execution_status", {})

    return result

# Execute the saga
output = payment_execution()
