# Saga: stripe_payment_received
# Version: 1.0.0
# Previous: none
# Changed: Initial implementation of Stripe cash-in double-entry posting
# Author: Platform Team
# Date: 2026-02-10
#
# This Starlark script defines the stripe_payment_received saga workflow.
# When Stripe reports a successful payment (payment_intent.succeeded), this saga
# records the cash-in as a double-entry ledger posting in the meridian-ops tenant.
#
# Double-Entry Accounting:
#   DEBIT  stripe_nostro          (cash received from Stripe)
#   CREDIT {party_id}_prepaid     (customer prepaid balance increases)
#
# The Stripe charge ID is stored as external_reference_id for O(1) reconciliation
# against Stripe's settlement reports.
#
# Input data (provided via input_data dictionary):
#   - tenant_id: string        - The tenant receiving the payment (e.g., "meridian-ops")
#   - party_id: string         - The customer party identifier
#   - amount_cents: int        - Payment amount in smallest currency unit (e.g., pence)
#   - instrument_code: string  - Instrument code (e.g., "GBP"). Replaces currency field.
#   - currency: string         - Deprecated: use instrument_code instead. ISO 4217 currency code (e.g., "gbp")
#   - charge_id: string        - Stripe Charge ID for reconciliation
#   - payment_intent_id: string - Stripe PaymentIntent ID
#   - stripe_event_id: string  - Original Stripe event ID for audit trail
#
# Compensation Order (LIFO):
#   If the credit step fails, the debit to stripe_nostro is reversed.

stripe_payment_received_saga = saga(name="stripe_payment_received")

def execute_stripe_payment_received():
    # Extract input parameters
    tenant_id = input_data["tenant_id"]
    party_id = input_data["party_id"]
    amount_cents = input_data["amount_cents"]
    # Accept instrument_code (preferred) or currency (deprecated alias).
    # Normalize once: strip whitespace, uppercase, default to "GBP".
    instrument_code = input_data.get("instrument_code", "") or input_data.get("currency", "")
    instrument_code = instrument_code.strip().upper()
    if instrument_code == "":
        instrument_code = "GBP"
    charge_id = input_data["charge_id"]
    payment_intent_id = input_data.get("payment_intent_id", "")
    stripe_event_id = input_data.get("stripe_event_id", "")

    # Convert from cents to major currency unit (e.g., pence to pounds)
    # Stripe amounts are in the smallest currency unit
    amount = Decimal(str(amount_cents)) / Decimal("100")

    # Derive account identifiers
    nostro_account = "stripe_nostro"
    prepaid_account = party_id + "_prepaid"

    # Step 1: Debit the stripe nostro account (cash received from Stripe)
    # This represents Stripe holding funds on our behalf
    step(name="debit_stripe_nostro")
    debit_result = position_keeping.initiate_log(
        account_id=nostro_account,
        amount=amount,
        instrument_code=instrument_code,
        direction="DEBIT",
        description="Stripe payment received: " + charge_id,
        external_reference_id=charge_id,
    )

    # Step 2: Credit the customer prepaid balance
    # This increases the customer's available balance
    step(name="credit_customer_prepaid")
    credit_result = position_keeping.initiate_log(
        account_id=prepaid_account,
        amount=amount,
        instrument_code=instrument_code,
        direction="CREDIT",
        description="Payment from Stripe: " + charge_id,
        external_reference_id=charge_id,
    )

    result = {
        "status": "completed",
        "tenant_id": tenant_id,
        "party_id": party_id,
        "prepaid_log_id": credit_result["log_id"],
        "amount_cents": amount_cents,
        "instrument_code": instrument_code,
        "charge_id": charge_id,
        "payment_intent_id": payment_intent_id,
        "stripe_event_id": stripe_event_id,
    }
    return result

# Execute the saga
output = execute_stripe_payment_received()
