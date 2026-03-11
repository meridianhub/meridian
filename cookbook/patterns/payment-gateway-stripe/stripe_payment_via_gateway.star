# Saga: stripe_payment_via_gateway
# Version: 1.0.0
# Previous: none
# Changed: Initial implementation using Financial Gateway
# Author: Platform Team
# Date: 2026-03-11
#
# Initiates a Stripe payment for a customer. Resolves the party's
# stored payment method from the Party Service, dispatches the
# payment via the Financial Gateway, and records the double-entry
# ledger posting.
#
# The Party Service holds Stripe customer IDs and payment method
# references (e.g., pm_xxx). Step 1 resolves these from the
# party_id, so the saga caller only needs to know the internal
# party identifier — not Stripe-specific details.
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
#   1. Cancel payment (financial_gateway.cancel_payment)

def stripe_payment_via_gateway():
    ctx = input_data

    party_id = ctx["party_id"]
    amount_cents = ctx["amount_cents"]
    currency = ctx.get("currency", "GBP").strip().upper()
    amount = Decimal(str(amount_cents)) / Decimal("100")

    # Step 1: Resolve the party's default Stripe payment method
    # The Party Service stores Stripe customer IDs and payment
    # method references against each party.
    step(name="get_payment_method")
    pm_result = party.get_default_payment_method(
        party_id=party_id,
    )

    # Step 2: Dispatch payment via Financial Gateway
    # Uses the resolved Stripe customer and payment method IDs.
    # The Financial Gateway handles Stripe API calls, retries,
    # rate limiting, and circuit breaking internally.
    step(name="dispatch_payment")
    gateway_result = financial_gateway.dispatch_payment(
        payment_order_id=ctx["payment_order_id"],
        amount_minor_units=amount_cents,
        currency=currency,
        customer_reference=pm_result.provider_customer_id,
        payment_method_reference=pm_result.provider_method_id,
        idempotency_key=ctx["idempotency_key"],
        rail="STRIPE",
        metadata={
            "party_id": party_id,
        },
    )

    # Step 3: Debit payment clearing account (cash received)
    step(name="debit_clearing")
    position_keeping.initiate_log(
        account_type="PAYMENT_CLEARING",
        party_id=party_id,
        instrument_code=currency,
        amount=amount,
        direction="DEBIT",
        attributes={
            "provider_reference_id": gateway_result.provider_reference_id,
        },
    )

    # Step 4: Credit the customer current account
    step(name="credit_customer")
    position_keeping.initiate_log(
        account_type="CUSTOMER_CURRENT",
        party_id=party_id,
        instrument_code=currency,
        amount=amount,
        direction="CREDIT",
        attributes={
            "provider_reference_id": gateway_result.provider_reference_id,
        },
    )

    return {
        "party_id": party_id,
        "amount_cents": amount_cents,
        "currency": currency,
        "provider_reference_id": gateway_result.provider_reference_id,
        "gateway_status": gateway_result.status,
    }

output = stripe_payment_via_gateway()
