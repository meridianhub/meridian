# Saga: race_result_distribution
# Version: 1.0.0
# Previous: none
# Changed: Initial version
# Author: Tenant Configuration (Betting/Syndicate)
# Date: 2026-03-03
#
# Distributes pot winnings across a syndicate's participants when a horse race
# completes. Demonstrates event-triggered saga with full entity graph traversal:
# market data event -> party organization lookup -> hierarchy traversal ->
# structuring data (allocation shares) -> position booking per participant.
#
# Trigger: event:market-information.observation-recorded.v1
# Filter:  event.dataset_code == 'HORSE_RACING' && event.status == 'OFFICIAL'
#
# The event originates from market data, not position-keeping. The saga navigates
# the party hierarchy to determine who gets paid and how much.
#
# Input data (from event payload via input_data dictionary):
#   - correlation_id: string - Idempotency key from source event
#   - reference: string - Race identifier
#   - attributes: dict - Race results including total_pot

# Define the saga
race_distribution_saga = saga(name="race_result_distribution")

def execute_race_distribution():
    ctx = input_data

    correlation_id = ctx["correlation_id"]
    race_id = ctx["reference"]
    results = ctx["attributes"]

    # Find the syndicate organization that placed bets on this race
    step(name="find_syndicate")
    syndicate = reference_data.query(
        entity_type="party",
        filter="attributes.active_race_id == '" + race_id + "'",
    )

    if syndicate.count == 0:
        return {"status": "NO_SYNDICATE", "race_id": race_id}

    syndicate_party_id = syndicate.items[0].party_id

    # Traverse party hierarchy to get all syndicate participants
    step(name="list_participants")
    participants = party.list_participants(
        org_id=syndicate_party_id,
        relationship_type="SYNDICATE_PARTICIPANT",
    )

    # Calculate pot and distribute by allocation share
    pot = Decimal(results["total_pot"])
    distributed_count = 0

    for p in participants:
        step(name="get_structuring_" + str(distributed_count))
        structuring = party.get_structuring_data(
            party_id=p.party_id,
            org_id=syndicate_party_id,
            relationship_type="SYNDICATE_PARTICIPANT",
        )

        payout = pot * Decimal(structuring.allocation_share)

        step(name="book_payout_" + str(distributed_count))
        position_keeping.initiate_log(
            account_id=p.metadata.payout_account_id,
            instrument_code="GBP",
            direction="CREDIT",
            amount=payout,
            correlation_id=correlation_id,
            description="Race " + race_id + " payout: " + str(structuring.allocation_share),
        )

        distributed_count = distributed_count + 1

    return {
        "status": "DISTRIBUTED",
        "race_id": race_id,
        "participants": distributed_count,
        "total_pot": str(pot),
    }

# Execute the saga
output = execute_race_distribution()
