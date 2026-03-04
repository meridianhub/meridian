# Saga: stripe_payment
# Version: 3.0.0
# Previous: 2.0.0
# Changed: Migrated from operational_gateway.dispatch_instruction to
#          financial_gateway.dispatch_payment for Stripe payment dispatch.
#          The Financial Gateway provides a typed, payment-specific API that
#          accepts structured payment parameters directly (no instruction_type
#          or payload wrapping), returning provider_reference_id, status, and
#          platform_fee_minor_units from the Stripe payment intent.
# Author: Platform Team
# Date: 2026-03-04
#
# Stripe Payment saga orchestrating end-to-end payment flow with Stripe via
# the Financial Gateway. Resolves the party's default payment method,
# reserves funds via lien, dispatches to Stripe through the financial gateway,
# posts ledger entries, and executes the lien.
#
# Migration from v2.0.0:
#   - Step 3 changed from operational_gateway.dispatch_instruction (generic
#     instruction-based dispatch) to financial_gateway.dispatch_payment
#     (typed, payment-specific dispatch)
#   - instruction_id replaced by provider_reference_id in downstream references
#   - gateway_status sourced directly from financial_gateway response
#   - platform_fee_minor_units available in result from financial gateway
#
# Steps:
#   1. get_payment_method    - Resolve party's default Stripe payment method
#   2. create_lien           - Reserve funds with payment_attributes from step 1
#   3. dispatch_payment      - Dispatch via Financial Gateway (NEW in v3.0.0)
#   4. post_ledger           - Post double-entry ledger records (webhook-triggered)
#   5. execute_lien          - Finalize lien after ledger posted (webhook-triggered)
#
# Compensation:
#   - Step 2: payment_order.terminate_lien (built-in compensation)
#   - Step 3: financial_gateway.cancel_payment (cancels pending payment)
#   - Step 1: read-only, no compensation needed
#
# Input parameters (from input_data dict):
#   - party_id: string (required) - party whose default payment method to use
#   - payment_order_id: string (required)
#   - debtor_account_id: string (required)
#   - creditor_reference: string (required)
#   - amount_cents: int64 (required)
#   - currency: string (required, e.g., "GBP", "USD")
#   - idempotency_key: string (required)
#   - instrument_code: string (optional, for bucket evaluation)
#   - payment_attributes: dict (optional, base attributes for CEL bucket expression)
#   - should_post_ledger: bool (optional, default false - set by webhook)
#   - should_execute_lien: bool (optional, default false - set by webhook)
#   - internal_clearing_enabled: bool (optional, for 4-posting ledger flow)

def stripe_payment():
    """
    Main saga entry point.
    Resolves Stripe payment method then executes payment order flow
    via the Financial Gateway.
    """

    ctx = input_data

    # Step 1: Resolve the party's default payment method
    step(name="get_payment_method")
    pm_result = party.get_default_payment_method(
        party_id=ctx.get("party_id"),
    )

    # Build payment_attributes by merging resolved payment method details
    # with any additional attributes provided in the input
    payment_attrs = dict(ctx.get("payment_attributes") or {})
    payment_attrs["provider"] = pm_result.provider
    payment_attrs["provider_customer_id"] = pm_result.provider_customer_id
    payment_attrs["provider_method_id"] = pm_result.provider_method_id
    payment_attrs["method_type"] = pm_result.method_type

    # Step 2: Reserve funds via lien with payment method attributes
    step(name="create_lien")
    lien_result = payment_order.create_lien(
        account_id=ctx.get("debtor_account_id"),
        amount_cents=ctx.get("amount_cents"),
        currency=ctx.get("currency"),
        payment_order_id=ctx.get("payment_order_id"),
        instrument_code=ctx.get("instrument_code", ""),
        payment_attributes=payment_attrs,
    )

    lien_id = lien_result.lien_id

    # Step 3: Dispatch payment via Financial Gateway
    #
    # This replaces the v2.0.0 operational_gateway.dispatch_instruction call.
    # The Financial Gateway provides a typed payment API that:
    #   a) Accepts structured payment parameters directly (no instruction_type wrapping)
    #   b) Routes to the appropriate payment rail (Stripe for rail="STRIPE")
    #   c) Returns provider_reference_id (Stripe PaymentIntent ID), status, and
    #      platform_fee_minor_units
    #   d) Handles retries, rate limiting, and circuit breaking internally
    step(name="dispatch_payment")
    gateway_result = financial_gateway.dispatch_payment(
        payment_order_id=ctx.get("payment_order_id"),
        amount_minor_units=ctx.get("amount_cents"),
        currency=ctx.get("currency"),
        customer_reference=pm_result.provider_customer_id,
        payment_method_reference=pm_result.provider_method_id,
        idempotency_key=ctx.get("idempotency_key"),
        rail="STRIPE",
        metadata={
            "debtor_account_id": ctx.get("debtor_account_id"),
            "creditor_reference": ctx.get("creditor_reference"),
        },
    )

    provider_reference_id = gateway_result.provider_reference_id

    # Build result with payment method and gateway info
    result = {
        "lien_id": lien_id,
        "provider_reference_id": provider_reference_id,
        "gateway_status": gateway_result.status,
        "platform_fee_minor_units": gateway_result.platform_fee_minor_units,
        "provider": pm_result.provider,
        "provider_customer_id": pm_result.provider_customer_id,
        "provider_method_id": pm_result.provider_method_id,
    }

    # Step 4: Post ledger entries (conditional - triggered by webhook)
    if ctx.get("should_post_ledger", False):
        step(name="post_ledger")
        ledger_result = payment_order.post_ledger_entries(
            payment_order_id=ctx.get("payment_order_id"),
            debtor_account_id=ctx.get("debtor_account_id"),
            gateway_reference_id=provider_reference_id,
            amount_cents=ctx.get("amount_cents"),
            currency=ctx.get("currency"),
            idempotency_key=ctx.get("idempotency_key"),
            internal_clearing_enabled=ctx.get("internal_clearing_enabled", False),
        )
        result["booking_log_id"] = ledger_result.booking_log_id

    # Step 5: Execute lien (conditional - triggered by webhook after ledger posted)
    if ctx.get("should_execute_lien", False):
        if lien_id:
            step(name="execute_lien")
            execution_result = payment_order.execute_lien(
                lien_id=lien_id,
            )
            result["lien_execution_status"] = execution_result.execution_status

    return result

# Execute the saga
output = stripe_payment()
