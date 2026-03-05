# DEPRECATED: This saga dispatches payment.* instructions to the operational-gateway,
# which no longer accepts financial instruction types. Payment instructions must be
# routed through the financial-gateway. Use the financial-gateway saga for new deployments.
# This file is retained for reference only and must not be deployed to new tenants.
#
# Saga: stripe_payment
# Version: 2.0.0
# Previous: 1.0.0
# Changed: Migrated from direct payment_order.send_to_gateway to
#          operational_gateway.dispatch_instruction for Stripe payment dispatch.
#          The Operational Gateway now handles outbound payload transformation
#          (payment-collect-to-stripe mapping), authentication, retry logic,
#          circuit breaking, and inbound response normalization (stripe-response-to-ack).
# Author: Platform Team
# Date: 2026-03-02
#
# Stripe Payment saga orchestrating end-to-end payment flow with Stripe via
# the Operational Gateway. Resolves the party's default payment method,
# reserves funds via lien, dispatches to Stripe through the gateway, posts
# ledger entries, and executes the lien.
#
# Migration from v1.0.0:
#   - Step 3 changed from payment_order.send_to_gateway (direct Stripe call)
#     to operational_gateway.dispatch_instruction (gateway-abstracted dispatch)
#   - The instruction_type "payment.collect" is resolved by the gateway to the
#     stripe-payments provider connection via the instruction route config
#   - Outbound mapping "payment-collect-to-stripe" transforms the payload:
#       * amount_cents passed through as Stripe's amount (already in cents)
#       * currency lowercased (Stripe expects lowercase ISO codes)
#       * customer_id -> customer, payment_method_id -> payment_method
#       * confirm=true computed field added automatically
#   - Inbound mapping "stripe-response-to-ack" normalizes the response:
#       * Stripe status enum mapped to Meridian instruction statuses
#       * Provider reference ID extracted from Stripe PaymentIntent ID
#
# Steps:
#   1. get_payment_method    - Resolve party's default Stripe payment method
#   2. create_lien           - Reserve funds with payment_attributes from step 1
#   3. dispatch_to_gateway   - Dispatch via Operational Gateway (NEW in v2.0.0)
#   4. post_ledger           - Post double-entry ledger records (webhook-triggered)
#   5. execute_lien          - Finalize lien after ledger posted (webhook-triggered)
#
# Compensation:
#   - Step 2: payment_order.terminate_lien (built-in compensation)
#   - Step 3: operational_gateway.cancel_instruction (cancels pending instruction)
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
    via the Operational Gateway.
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

    # Step 3: Dispatch payment via Operational Gateway
    #
    # This replaces the v1.0.0 payment_order.send_to_gateway call.
    # The Operational Gateway:
    #   a) Resolves the instruction route for "payment.collect"
    #   b) Applies outbound mapping "payment-collect-to-stripe":
    #      - amount_cents -> amount (integer, Stripe expects cents)
    #      - currency -> lowercase (e.g., "GBP" -> "gbp")
    #      - customer_id -> customer
    #      - payment_method_id -> payment_method
    #      - confirm=true added as computed field
    #   c) Dispatches HTTPS POST to https://api.stripe.com/v1/payment_intents
    #   d) Applies inbound mapping "stripe-response-to-ack":
    #      - Maps Stripe status to Meridian status (succeeded->ACKNOWLEDGED, etc.)
    #      - Extracts provider_reference_id from Stripe PaymentIntent ID
    #   e) Handles retries (3 attempts, exponential backoff 1s->2s->4s, max 30s)
    #   f) Enforces rate limit (25 rps, burst 50)
    #   g) Circuit breaker protects against Stripe outages
    step(name="dispatch_to_gateway")
    gateway_result = operational_gateway.dispatch_instruction(
        instruction_type="payment.collect",
        payload={
            "payment_order_id": ctx.get("payment_order_id"),
            "amount_cents": ctx.get("amount_cents"),
            "currency": ctx.get("currency"),
            "customer_id": pm_result.provider_customer_id,
            "payment_method_id": pm_result.provider_method_id,
            "creditor_reference": ctx.get("creditor_reference"),
            "metadata": {
                "payment_order_id": ctx.get("payment_order_id"),
                "debtor_account_id": ctx.get("debtor_account_id"),
            },
        },
        priority="HIGH",
        correlation_id=ctx.get("payment_order_id"),
    )

    instruction_id = gateway_result.instruction_id

    # Build result with payment method and gateway info
    result = {
        "lien_id": lien_id,
        "instruction_id": instruction_id,
        "gateway_status": gateway_result.status,
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
            gateway_reference_id=instruction_id,
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
