# Saga: settle_syndicate
# Version: 1.0.0
# Previous: none
# Changed: Initial implementation
# Author: Platform Team
# Date: 2026-03-11
#
# Settles a syndicate when market data confirms the result.
# Triggered by market-information.observation-recorded.v1 with
# observation_type == 'MATCH_RESULT'.
#
# Settlement logic:
#   1. Query all bet positions for the syndicate
#   2. Identify winners (positions matching the winning selection)
#   3. Take 15% platform commission from the pool
#   4. Distribute remaining 85% equally among winners via Stripe
#   5. Burn all bet units for the syndicate
#
# Double-Entry Accounting (settlement):
#   DEBIT  SYNDICATE_POOL       (liability decreases - pool emptied)
#   CREDIT PLATFORM_COMMISSION  (revenue - 15% rake)
#   CREDIT STRIPE_NOSTRO        (cash leaves via Stripe payouts)
#
# Payouts use the Financial Gateway's dispatch_refund (payout to
# connected accounts), not the Operational Gateway.
#
# Input data (from market-information.observation-recorded.v1):
#   - metadata.syndicate_id: string  - Syndicate to settle
#   - metadata.result: string        - Winning selection
#
# Compensation:
#   Settlement is a terminal operation. Once commission is taken
#   and winnings distributed, the syndicate is marked SETTLED.
#   Partial failures during distribution are handled by saga
#   retry semantics (each step is idempotent via position_keeping).

def settle_syndicate():
    ctx = input_data
    syndicate_id = ctx["metadata"]["syndicate_id"]
    winning_selection = ctx["metadata"]["result"]

    # Look up syndicate details
    step(name="get_syndicate")
    syndicate = repository.get_entity(
        entity_type="syndicate",
        entity_id=syndicate_id,
    )
    stake = Decimal(syndicate.attributes["stake_amount"])

    # Query all bet positions for this syndicate
    step(name="query_positions")
    positions = position_keeping.query_positions(
        position_id="BET_POSITION:" + syndicate_id,
        instrument_code="BET_UNIT",
    )

    # Identify winners and calculate pool
    total_bets = len(positions)
    winners = []
    for pos in positions:
        if pos.attributes["selection"] == winning_selection:
            winners.append(pos)

    pool_total = stake * total_bets
    commission = pool_total * Decimal("0.15")
    winnings_total = pool_total - commission

    # Take 15% commission
    step(name="take_commission")
    position_keeping.initiate_log(
        position_id="SYNDICATE_POOL:" + syndicate_id,
        instrument_code="GBP",
        amount=commission,
        direction="DEBIT",
        correlation_id=syndicate_id,
    )
    position_keeping.initiate_log(
        position_id="PLATFORM_COMMISSION:PLATFORM",
        instrument_code="GBP",
        amount=commission,
        direction="CREDIT",
        correlation_id=syndicate_id,
    )

    # Distribute winnings equally among winners
    if len(winners) > 0:
        # Round down to avoid over-paying; remainder goes to platform
        winner_count = Decimal(str(len(winners)))
        per_winner = (winnings_total / winner_count).quantize(
            Decimal("0.01"),
        )
        distributed = per_winner * winner_count
        remainder = winnings_total - distributed

        for winner in winners:
            step(name="payout_" + winner.party_id)

            # Dispatch payout via Financial Gateway (before ledger,
            # so books only reflect successful payouts)
            financial_gateway.dispatch_refund(
                payment_order_id=syndicate_id + ":payout:" + winner.party_id,
                amount_minor_units=int(per_winner * Decimal("100")),
                currency="GBP",
                customer_reference=winner.party_id,
                rail="STRIPE",
                metadata={
                    "syndicate_id": syndicate_id,
                    "payout_type": "winnings",
                },
            )

            # Debit pool (liability decreases)
            position_keeping.initiate_log(
                position_id="SYNDICATE_POOL:" + syndicate_id,
                instrument_code="GBP",
                amount=per_winner,
                direction="DEBIT",
                correlation_id=syndicate_id,
            )

            # Credit nostro (payout via Stripe)
            position_keeping.initiate_log(
                position_id="STRIPE_NOSTRO:" + winner.party_id,
                instrument_code="GBP",
                amount=per_winner,
                direction="CREDIT",
                correlation_id=syndicate_id,
            )

        # Any rounding remainder goes to platform commission
        if remainder > Decimal("0"):
            step(name="remainder_to_commission")
            position_keeping.initiate_log(
                position_id="SYNDICATE_POOL:" + syndicate_id,
                instrument_code="GBP",
                amount=remainder,
                direction="DEBIT",
                correlation_id=syndicate_id,
            )
            position_keeping.initiate_log(
                position_id="PLATFORM_COMMISSION:PLATFORM",
                instrument_code="GBP",
                amount=remainder,
                direction="CREDIT",
                correlation_id=syndicate_id,
            )
    else:
        # No winners: unclaimed winnings go to platform commission
        step(name="unclaimed_to_commission")
        position_keeping.initiate_log(
            position_id="SYNDICATE_POOL:" + syndicate_id,
            instrument_code="GBP",
            amount=winnings_total,
            direction="DEBIT",
            correlation_id=syndicate_id,
        )
        position_keeping.initiate_log(
            position_id="PLATFORM_COMMISSION:PLATFORM",
            instrument_code="GBP",
            amount=winnings_total,
            direction="CREDIT",
            correlation_id=syndicate_id,
        )

    # Burn all bet units for this syndicate
    step(name="burn_positions")
    for pos in positions:
        position_keeping.initiate_log(
            position_id="BET_POSITION:" + pos.party_id,
            instrument_code="BET_UNIT",
            amount=Decimal("1"),
            direction="CREDIT",
            correlation_id=syndicate_id,
        )

    # Mark syndicate as settled
    step(name="mark_settled")
    repository.update_entity(
        entity_type="syndicate",
        entity_id=syndicate_id,
        attributes={
            "status": "SETTLED",
            "winning_selection": winning_selection,
        },
    )

    return {
        "syndicate_id": syndicate_id,
        "winning_selection": winning_selection,
        "total_pool": str(pool_total),
        "commission": str(commission),
        "winners": len(winners),
        "per_winner": str(per_winner) if len(winners) > 0 else "0",
    }

output = settle_syndicate()
