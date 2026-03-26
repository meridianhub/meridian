# Saga: dunning_escalation
# Version: 1.0.0
# Previous: none
# Changed: Initial implementation of dunning escalation saga
# Author: Platform Team
# Date: 2026-02-11
#
# Dunning Escalation saga for handling payment failures through progressive
# dunning levels. When a billing run payment fails, this saga escalates
# through notification and account freeze stages.
#
# Dunning levels:
#   0 -> 1: Send payment failure notification, schedule retry in 24h
#   1 -> 2: Send second notice, schedule retry in 72h
#   2 -> 3: Send final warning, freeze the account, send freeze notification
#
# Steps:
#   1. check_dunning_level - Evaluate current level and determine action
#   2. send_notification   - Notify party of payment failure/escalation
#   3. freeze_account      - Freeze account at max dunning level (level 3)
#
# Input parameters (from input_data dict):
#   - billing_run_id: string (required) - billing run that failed
#   - account_id: string (required) - debtor account
#   - party_id: string (required) - party to notify
#   - dunning_level: int (required) - current dunning level (0-2 pre-escalation)
#   - amount_cents: int64 (required) - outstanding amount
#   - currency: string (required) - currency code
#   - invoice_number: string (optional) - invoice reference for notifications

def dunning_escalation():
    """
    Main saga entry point.
    Escalates dunning level and takes appropriate action.
    """

    ctx = input_data

    # Validate required fields before any side effects
    missing = []
    for key in ["billing_run_id", "dunning_level", "account_id", "party_id", "amount_cents", "currency"]:
        if ctx.get(key) == None:
            missing.append(key)
    if missing:
        return {
            "action_taken": "invalid_input",
            "missing_fields": missing,
        }

    current_level = ctx["dunning_level"]
    if current_level < 0 or current_level > 2:
        return {
            "action_taken": "invalid_input",
            "invalid_dunning_level": current_level,
        }
    new_level = current_level + 1
    account_id = ctx["account_id"]
    party_id = ctx["party_id"]
    amount_cents = ctx["amount_cents"]
    currency = ctx["currency"]
    invoice_number = ctx.get("invoice_number", "")

    result = {
        "previous_level": current_level,
        "new_level": new_level,
        "account_id": account_id,
        "action_taken": "none",
    }

    billing_run_id = ctx["billing_run_id"]

    # Step 1: Send notification based on escalation level
    if new_level == 1:
        step(name="send_first_notice")
        notification.send(
            type="EMAIL",
            recipient=party_id,
            template="dunning-notice",
            data={
                "severity": 1,
                "days_overdue": 0,
                "amount_cents": amount_cents,
                "currency": currency,
                "invoice_number": invoice_number,
            },
            idempotency_key="dunning-1-" + billing_run_id,
        )
        result["action_taken"] = "first_notice_sent"

    elif new_level == 2:
        step(name="send_second_notice")
        notification.send(
            type="EMAIL",
            recipient=party_id,
            template="dunning-notice",
            data={
                "severity": 2,
                "days_overdue": 3,
                "amount_cents": amount_cents,
                "currency": currency,
                "invoice_number": invoice_number,
            },
            idempotency_key="dunning-2-" + billing_run_id,
        )
        result["action_taken"] = "second_notice_sent"

    elif new_level >= 3:
        # At max dunning level: send final warning, freeze the account, notify
        step(name="send_final_warning")
        notification.send(
            type="EMAIL",
            recipient=party_id,
            template="dunning-notice",
            data={
                "severity": 3,
                "amount_cents": amount_cents,
                "currency": currency,
                "invoice_number": invoice_number,
            },
            idempotency_key="dunning-3-" + billing_run_id,
        )

        # Step 2: Freeze the account at max dunning level
        step(name="freeze_account")
        invoice_ref = " for invoice " + invoice_number if invoice_number else ""
        freeze_result = current_account.control(
            account_id=account_id,
            action="FREEZE",
            reason="Dunning level 3 reached: payment overdue" + invoice_ref + " (" + str(amount_cents) + " " + currency + ")",
        )
        result["action_taken"] = "account_frozen"
        result["new_account_status"] = freeze_result.new_status
        result["freeze_timestamp"] = freeze_result.action_timestamp

        # Send freeze notification
        step(name="send_freeze_notice")
        notification.send(
            type="EMAIL",
            recipient=party_id,
            template="account-frozen",
            data={
                "amount_cents": amount_cents,
                "currency": currency,
                "invoice_number": invoice_number,
            },
            idempotency_key="dunning-frozen-" + billing_run_id,
        )

    return result

# Execute the saga
output = dunning_escalation()
