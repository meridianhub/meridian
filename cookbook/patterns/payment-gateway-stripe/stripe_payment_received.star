# Saga: stripe_payment_received
# Version: 2.0.0
# Previous: 1.0.0
# Changed: Migrated to use position_id-based position_keeping API
#          and clarified this is a webhook handler (no gateway dispatch).
# Author: Platform Team
# Date: 2026-03-11
#
# Webhook handler for Stripe payment_intent.succeeded events.
# When Stripe confirms a successful payment, this saga records
# the cash-in as a double-entry ledger posting.
#
# This saga does NOT dispatch a payment — the payment was already
# initiated and confirmed by Stripe. For customer-initiated payment
# dispatch, see stripe_payment_via_gateway.star.
#
# Idempotency: payment_intent_id is required and used as the
# correlation_id on all ledger postings, making webhook replays safe.
#
# Double-Entry Accounting:
#   DEBIT  PAYMENT_CLEARING   (cash received from Stripe)
#   CREDIT CUSTOMER_CURRENT   (customer balance increases)
#
# Input data (from Stripe webhook payload):
#   - party_id: string          - The customer party identifier
#   - amount_cents: int         - Payment amount in minor units
#   - instrument_code: string   - Instrument code (e.g., "GBP")
#   - charge_id: string         - Stripe Charge ID for reconciliation
#   - payment_intent_id: string - Stripe PaymentIntent ID (required)
#
# Compensation Order (LIFO):
#   If the credit step fails, the debit to PAYMENT_CLEARING is reversed.

def execute_stripe_payment_received():
    ctx = input_data

    party_id = ctx["party_id"]
    amount_cents = ctx["amount_cents"]
    instrument_code = ctx.get("instrument_code", "GBP").strip().upper()
    charge_id = ctx["charge_id"]
    payment_intent_id = ctx["payment_intent_id"]

    # Convert from minor units to major currency units
    amount = Decimal(str(amount_cents)) / Decimal("100")

    # Step 1: Debit the payment clearing account (cash received)
    # correlation_id ensures idempotency on webhook replay
    step(name="debit_clearing")
    debit_result = position_keeping.initiate_log(
        position_id="PAYMENT_CLEARING:" + party_id,
        instrument_code=instrument_code,
        amount=amount,
        direction="DEBIT",
        correlation_id=payment_intent_id + ":debit",
    )

    # Step 2: Credit the customer current account
    step(name="credit_customer")
    credit_result = position_keeping.initiate_log(
        position_id="CUSTOMER_CURRENT:" + party_id,
        instrument_code=instrument_code,
        amount=amount,
        direction="CREDIT",
        correlation_id=payment_intent_id + ":credit",
    )

    return {
        "party_id": party_id,
        "amount_cents": amount_cents,
        "instrument_code": instrument_code,
        "charge_id": charge_id,
        "payment_intent_id": payment_intent_id,
        "debit_log_id": debit_result.log,
        "credit_log_id": credit_result.log,
    }

output = execute_stripe_payment_received()
