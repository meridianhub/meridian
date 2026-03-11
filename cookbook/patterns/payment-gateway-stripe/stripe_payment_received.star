# Saga: stripe_payment_received
# Version: 2.0.0
# Previous: 1.0.0
# Changed: Migrated from operational_gateway to financial_gateway.
#          The Financial Gateway provides typed payment APIs
#          (dispatch_payment, dispatch_refund) with built-in Stripe
#          integration, webhook handling, and payment event topics.
# Author: Platform Team
# Date: 2026-03-11
#
# This Starlark script defines the stripe_payment_received saga workflow.
# When Stripe reports a successful payment (payment_intent.succeeded),
# this saga records the cash-in as a double-entry ledger posting.
#
# Double-Entry Accounting:
#   DEBIT  PAYMENT_CLEARING   (cash received from Stripe via Financial Gateway)
#   CREDIT CUSTOMER_CURRENT   (customer balance increases)
#
# The Stripe charge ID is stored as external_reference_id for O(1)
# reconciliation against Stripe's settlement reports.
#
# Input data (provided via input_data dictionary):
#   - party_id: string         - The customer party identifier
#   - amount_cents: int        - Payment amount in minor units (e.g., pence)
#   - instrument_code: string  - Instrument code (e.g., "GBP")
#   - charge_id: string        - Stripe Charge ID for reconciliation
#   - payment_intent_id: string - Stripe PaymentIntent ID
#   - idempotency_key: string  - Idempotency key for gateway dispatch
#
# Compensation Order (LIFO):
#   If the credit step fails, the debit to PAYMENT_CLEARING is reversed.

def execute_stripe_payment_received():
    ctx = input_data

    party_id = ctx["party_id"]
    amount_cents = ctx["amount_cents"]
    instrument_code = ctx.get("instrument_code", "GBP").strip().upper()
    charge_id = ctx["charge_id"]
    payment_intent_id = ctx.get("payment_intent_id", "")

    # Convert from minor units to major currency units
    amount = Decimal(str(amount_cents)) / Decimal("100")

    # Step 1: Dispatch payment via Financial Gateway
    # The Financial Gateway handles Stripe API calls, retries,
    # rate limiting, and circuit breaking internally.
    step(name="dispatch_payment")
    gateway_result = financial_gateway.dispatch_payment(
        payment_order_id=charge_id,
        amount_minor_units=amount_cents,
        currency=instrument_code,
        customer_reference=party_id,
        idempotency_key=ctx.get("idempotency_key", charge_id),
        rail="STRIPE",
        metadata={
            "charge_id": charge_id,
            "payment_intent_id": payment_intent_id,
        },
    )

    # Step 2: Debit the payment clearing account (cash received)
    step(name="debit_clearing")
    position_keeping.initiate_log(
        account_type="PAYMENT_CLEARING",
        party_id=party_id,
        instrument_code=instrument_code,
        amount=amount,
        direction="DEBIT",
        attributes={
            "charge_id": charge_id,
            "provider_reference_id": gateway_result.provider_reference_id,
        },
    )

    # Step 3: Credit the customer current account
    step(name="credit_customer")
    position_keeping.initiate_log(
        account_type="CUSTOMER_CURRENT",
        party_id=party_id,
        instrument_code=instrument_code,
        amount=amount,
        direction="CREDIT",
        attributes={
            "charge_id": charge_id,
            "provider_reference_id": gateway_result.provider_reference_id,
        },
    )

    return {
        "party_id": party_id,
        "amount_cents": amount_cents,
        "instrument_code": instrument_code,
        "charge_id": charge_id,
        "provider_reference_id": gateway_result.provider_reference_id,
        "gateway_status": gateway_result.status,
    }

output = execute_stripe_payment_received()
