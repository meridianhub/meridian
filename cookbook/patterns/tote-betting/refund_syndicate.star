# schema-validation: skip
# Reason: Uses repository service module (entity CRUD) and
# position_keeping.query_positions which require runtime mocks beyond
# schema validation scope. Handler schema compliance for financial_gateway
# and position_keeping.initiate_log is covered by other patterns.
#
# Saga: refund_syndicate
# Version: 1.0.0
# Previous: none
# Changed: Initial implementation
# Author: Platform Team
# Date: 2026-03-11
#
# Refunds all members of a syndicate when a match is cancelled or
# postponed. Returns each member's stake via Stripe and burns all
# bet positions.
#
# Double-Entry Accounting (per member):
#   DEBIT  SYNDICATE_POOL  (liability decreases)
#   CREDIT STRIPE_NOSTRO   (cash returned via Stripe)
#   CREDIT BET_POSITION    (bet unit burned)
#
# Input data:
#   - syndicate_id: string - Syndicate to refund
#
# No platform commission is taken on refunds.

def refund_syndicate():
    ctx = input_data
    syndicate_id = ctx["syndicate_id"]

    # Look up syndicate
    step(name="get_syndicate")
    syndicate = repository.get_entity(
        entity_type="syndicate",
        entity_id=syndicate_id,
    )
    stake = Decimal(syndicate.attributes["stake_amount"])

    # Guard: only refund OPEN syndicates
    status = syndicate.attributes.get("status", "")
    if status != "OPEN":
        fail("syndicate cannot be refunded: status is " + status)

    # Query all bet positions
    step(name="query_positions")
    positions = position_keeping.query_positions(
        instrument_code="BET_UNIT",
        correlation_id=syndicate_id,
    )

    # Refund each member
    for pos in positions:
        step(name="refund_" + pos.party_id)

        # Dispatch refund via Financial Gateway first, so ledger
        # entries only reflect successful refunds
        financial_gateway.dispatch_refund(
            payment_order_id=syndicate_id + ":" + pos.party_id,
            refund_amount_minor_units=int(stake * Decimal("100")),
            idempotency_key=syndicate_id + ":refund:" + pos.party_id,
            reason="match_cancelled",
            metadata={
                "syndicate_id": syndicate_id,
                "refund_type": "match_cancelled",
            },
        )

        # Debit pool (liability decreases)
        position_keeping.initiate_log(
            position_id="SYNDICATE_POOL:" + syndicate_id,
            instrument_code="GBP",
            amount=stake,
            direction="DEBIT",
            correlation_id=syndicate_id,
        )

        # Credit nostro (refund via Stripe)
        position_keeping.initiate_log(
            position_id="STRIPE_NOSTRO:" + pos.party_id,
            instrument_code="GBP",
            amount=stake,
            direction="CREDIT",
            correlation_id=syndicate_id,
        )

        # Burn bet unit
        position_keeping.initiate_log(
            position_id="BET_POSITION:" + pos.party_id,
            instrument_code="BET_UNIT",
            amount=Decimal("1"),
            direction="CREDIT",
            correlation_id=syndicate_id,
        )

    # Mark syndicate as cancelled
    step(name="mark_cancelled")
    repository.update_entity(
        entity_type="syndicate",
        entity_id=syndicate_id,
        attributes={"status": "CANCELLED"},
    )

    return {
        "syndicate_id": syndicate_id,
        "refunded_members": len(positions),
        "refund_per_member": str(stake),
    }

output = refund_syndicate()
