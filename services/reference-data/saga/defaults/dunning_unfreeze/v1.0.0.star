# Saga: dunning_unfreeze
# Version: 1.0.0
# Previous: none
# Changed: Initial implementation of dunning unfreeze saga
# Author: Platform Team
# Date: 2026-02-11
#
# Dunning Unfreeze saga for resolving frozen accounts after payment method
# update. When a party updates their payment method on a frozen account,
# this saga unfreezes the account and retries the failed payment.
#
# Steps:
#   1. unfreeze_account      - Restore account to ACTIVE status
#   2. retry_payment         - Attempt payment with new payment method
#   3. send_confirmation     - Notify party of resolution
#
# Compensation:
#   - If retry_payment fails, the account is re-frozen via compensation
#   - The party is notified of the continued freeze
#
# Input parameters (from input_data dict):
#   - account_id: string (required) - frozen account to unfreeze
#   - party_id: string (required) - party who updated payment method
#   - billing_run_id: string (required) - original failed billing run
#   - payment_order_id: string (required) - payment order for retry
#   - debtor_account_id: string (required) - debtor account for payment
#   - creditor_reference: string (required) - creditor reference
#   - amount_cents: int64 (required) - amount to retry
#   - currency: string (required) - currency code
#   - idempotency_key: string (required) - idempotency key for retry
#   - instrument_code: string (optional) - for bucket evaluation

def dunning_unfreeze():
    """
    Main saga entry point.
    Unfreezes account and retries payment with updated payment method.
    """

    ctx = input_data

    account_id = ctx.get("account_id")
    party_id = ctx.get("party_id")

    result = {
        "account_id": account_id,
        "party_id": party_id,
    }

    # Step 1: Unfreeze the account
    step(name="unfreeze_account")
    unfreeze_result = current_account.control(
        account_id=account_id,
        action="UNFREEZE",
        reason="Payment method updated, retrying failed payment for billing run " + ctx.get("billing_run_id", ""),
    )
    result["new_account_status"] = unfreeze_result.new_status
    result["unfreeze_timestamp"] = unfreeze_result.action_timestamp

    # Step 2: Resolve the party's updated payment method
    step(name="get_payment_method")
    pm_result = party.get_default_payment_method(
        party_id=party_id,
    )

    # Build payment attributes from resolved payment method
    payment_attrs = {}
    payment_attrs["provider"] = pm_result.provider
    payment_attrs["provider_customer_id"] = pm_result.provider_customer_id
    payment_attrs["provider_method_id"] = pm_result.provider_method_id
    payment_attrs["method_type"] = pm_result.method_type

    # Step 3: Reserve funds via lien with new payment method
    step(name="create_lien")
    lien_result = payment_order.create_lien(
        account_id=ctx.get("debtor_account_id"),
        amount_cents=ctx.get("amount_cents"),
        currency=ctx.get("currency"),
        payment_order_id=ctx.get("payment_order_id"),
        instrument_code=ctx.get("instrument_code", ""),
        payment_attributes=payment_attrs,
    )
    result["lien_id"] = lien_result.lien_id

    # Step 4: Send payment to gateway with new payment method
    step(name="send_to_gateway")
    gateway_result = payment_order.send_to_gateway(
        payment_order_id=ctx.get("payment_order_id"),
        debtor_account_id=ctx.get("debtor_account_id"),
        creditor_reference=ctx.get("creditor_reference"),
        amount_cents=ctx.get("amount_cents"),
        currency=ctx.get("currency"),
        idempotency_key=ctx.get("idempotency_key"),
    )
    result["gateway_reference_id"] = gateway_result.gateway_reference_id
    result["gateway_status"] = gateway_result.gateway_status

    # Step 5: Send confirmation notification
    step(name="send_confirmation")
    notification.send(
        type="EMAIL",
        recipient=party_id,
        template="dunning-resolved",
        data={
            "amount_cents": ctx.get("amount_cents"),
            "currency": ctx.get("currency"),
            "billing_run_id": ctx.get("billing_run_id"),
        },
        idempotency_key="dunning-resolved-" + ctx.get("billing_run_id", ""),
    )
    result["notification_sent"] = True

    return result

# Execute the saga
output = dunning_unfreeze()
