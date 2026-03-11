# Saga: join_syndicate
# Version: 1.0.0
# Previous: none
# Changed: Initial implementation
# Author: Platform Team
# Date: 2026-03-11
#
# Customer joins a syndicate by placing a bet. Charges the stake
# via Stripe (Financial Gateway), records the double-entry pool
# contribution, and mints a BET_UNIT position.
#
# Double-Entry Accounting:
#   DEBIT  STRIPE_NOSTRO     (cash received from Stripe)
#   CREDIT SYNDICATE_POOL    (liability to syndicate increases)
#   DEBIT  BET_POSITION      (customer's bet position minted)
#
# The stake amount is defined at syndicate creation time. All
# members pay the same entry stake (equal-weight parimutuel).
#
# Input data:
#   - party_id: string        - Customer placing the bet
#   - syndicate_id: string    - Syndicate to join
#   - selection: string       - Bet selection (e.g., "HOME_WIN")
#   - idempotency_key: string - Idempotency key for Stripe
#
# Compensation Order (LIFO):
#   3. Burn BET_UNIT (credit BET_POSITION)
#   2. Reverse pool credit (debit SYNDICATE_POOL)
#   1. Cancel Stripe payment (financial_gateway.cancel_payment)

def join_syndicate():
    ctx = input_data

    # Look up syndicate to get stake amount and match details
    step(name="get_syndicate")
    syndicate = repository.get_entity(
        entity_type="syndicate",
        entity_id=ctx["syndicate_id"],
    )
    stake = Decimal(syndicate.attributes["stake_amount"])
    max_members = int(syndicate.attributes["max_members"])

    # Check capacity before accepting payment
    step(name="check_capacity")
    existing = position_keeping.query_positions(
        position_id="BET_POSITION:" + ctx["syndicate_id"],
        instrument_code="BET_UNIT",
    )
    if len(existing) >= max_members:
        fail("syndicate is full: %d/%d members" % (len(existing), max_members))

    # Step 1: Collect payment via Financial Gateway (Stripe)
    step(name="collect_payment")
    gateway_result = financial_gateway.dispatch_payment(
        payment_order_id=ctx["syndicate_id"] + ":" + ctx["party_id"],
        amount_minor_units=int(stake * Decimal("100")),
        currency="GBP",
        customer_reference=ctx["party_id"],
        idempotency_key=ctx["idempotency_key"],
        rail="STRIPE",
        metadata={
            "syndicate_id": ctx["syndicate_id"],
            "selection": ctx["selection"],
        },
    )

    # Step 2: Record cash received into nostro
    step(name="debit_nostro")
    position_keeping.initiate_log(
        position_id="STRIPE_NOSTRO:" + ctx["party_id"],
        instrument_code="GBP",
        amount=stake,
        direction="DEBIT",
        correlation_id=ctx["syndicate_id"],
    )

    # Step 3: Credit the syndicate pool (liability increases)
    step(name="credit_pool")
    position_keeping.initiate_log(
        position_id="SYNDICATE_POOL:" + ctx["syndicate_id"],
        instrument_code="GBP",
        amount=stake,
        direction="CREDIT",
        correlation_id=ctx["syndicate_id"],
    )

    # Step 4: Mint bet unit into customer's position
    step(name="mint_bet_unit")
    position_keeping.initiate_log(
        position_id="BET_POSITION:" + ctx["party_id"],
        instrument_code="BET_UNIT",
        amount=Decimal("1"),
        direction="DEBIT",
        correlation_id=ctx["syndicate_id"],
    )

    return {
        "syndicate_id": ctx["syndicate_id"],
        "party_id": ctx["party_id"],
        "selection": ctx["selection"],
        "stake": str(stake),
        "provider_reference_id": gateway_result.provider_reference_id,
    }

output = join_syndicate()
