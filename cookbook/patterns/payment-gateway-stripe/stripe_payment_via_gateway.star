# Saga: stripe_payment_via_gateway
# Version: 1.0.0
# Previous: none
# Changed: Initial implementation using Financial Gateway
# Author: Platform Team
# Date: 2026-03-11
#
# Initiates a Stripe payment for a customer via the Financial Gateway
# and records the double-entry ledger posting.
#
# The Financial Gateway resolves the party's Stripe customer ID and
# payment method internally via the Party Service. The saga only
# needs the internal party_id — Stripe-specific details are handled
# by the gateway.
#
# Double-Entry Accounting:
#   DEBIT  PAYMENT_CLEARING   (cash received from Stripe)
#   CREDIT CUSTOMER_CURRENT   (customer balance increases)
#
# Input data:
#   - party_id: string         - Internal party ID (required)
#   - amount_cents: int        - Payment amount in minor units
#   - currency: string         - ISO 4217 currency code (e.g., "GBP")
#   - payment_order_id: string - Unique payment order reference
#   - idempotency_key: string  - Idempotency key for Stripe
#
# Compensation Order (LIFO):
#   3. Reverse customer credit (debit CUSTOMER_CURRENT)
#   2. Reverse clearing debit (credit PAYMENT_CLEARING)
#   1. Cancel payment via gateway (financial_gateway.cancel_payment)

def stripe_payment_via_gateway():
    ctx = input_data

    party_id = ctx["party_id"]
    amount_cents = ctx["amount_cents"]
    currency = ctx.get("currency", "GBP").strip().upper()
    payment_order_id = ctx.get("payment_order_id", "po_" + party_id)
    idempotency_key = ctx.get("idempotency_key", payment_order_id)
    amount = Decimal(str(amount_cents)) / Decimal("100")

    # Step 1: Dispatch payment via Financial Gateway
    # The Financial Gateway resolves the party's stored Stripe
    # payment method internally. The saga provides party_id as
    # customer_reference; the gateway looks up the Stripe customer
    # ID and payment method via the Party Service.
    step(name="dispatch_payment")
    gateway_result = financial_gateway.dispatch_payment(
        payment_order_id=payment_order_id,
        amount_minor_units=amount_cents,
        currency=currency,
        customer_reference=party_id,
        payment_method_reference=party_id,
        idempotency_key=idempotency_key,
        rail="STRIPE",
        metadata={
            "party_id": party_id,
        },
    )

    # Step 2: Debit payment clearing account (cash received)
    step(name="debit_clearing")
    position_keeping.initiate_log(
        position_id="PAYMENT_CLEARING:" + party_id,
        instrument_code=currency,
        amount=amount,
        direction="DEBIT",
        correlation_id=payment_order_id,
    )

    # Step 3: Credit the customer current account
    step(name="credit_customer")
    position_keeping.initiate_log(
        position_id="CUSTOMER_CURRENT:" + party_id,
        instrument_code=currency,
        amount=amount,
        direction="CREDIT",
        correlation_id=payment_order_id,
    )

    return {
        "party_id": party_id,
        "amount_cents": amount_cents,
        "currency": currency,
        "dispatch_id": gateway_result.dispatch_id,
        "gateway_status": gateway_result.status,
    }

output = stripe_payment_via_gateway()
