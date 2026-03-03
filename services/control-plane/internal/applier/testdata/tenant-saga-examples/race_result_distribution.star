# Saga: race_result_distribution
# Version: 2.0.0
# Previous: 1.0.0
# Changed: Thin event pattern — use actual ObservationRecorded proto fields
# Author: Tenant Configuration (Betting/Syndicate)
# Date: 2026-03-03
#
# Distributes pot winnings across a syndicate's participants when a horse race
# completes. Demonstrates event-triggered saga with full entity graph traversal:
# market data event -> party organization lookup -> hierarchy traversal ->
# structuring data (allocation shares) -> position booking per participant.
#
# Trigger: event:market-information.observation-recorded.v1
# Filter:  event.dataset_code == 'HORSE_RACING' && event.quality == 'ACTUAL'
#
# The event originates from market data, not position-keeping. The saga navigates
# the party hierarchy to determine who gets paid and how much.
#
# Input data (from ObservationRecorded via AsyncAPI-driven deserialization):
#   - correlation_id: string - Idempotency key from standard headers
#   - observation_id: string (UUID) - Observation identifier
#   - dataset_code: string - "HORSE_RACING"
#   - resolution_key_value: string - Race identifier (e.g., "CHELTENHAM-GOLD-CUP-2026")
#   - observed_at: string - ISO 8601 timestamp of race completion
#   - quality: string - Quality level ("ACTUAL" for official results)
#   - value: string - Observation value (total pot amount as decimal string)
#   - source_id: string (UUID) - Data source identifier
#   - recorded_at: string - ISO 8601 timestamp of recording
#
# Entity graph resolution (via service module calls):
#   - syndicate party: from reference_data.query(entity_type="party", ...)
#   - participants: from party.list_participants(org_id, relationship_type)
#   - allocation shares: from party.get_structuring_data(...)
#   - payout accounts: from participant metadata

# Define the saga
race_distribution_saga = saga(name="race_result_distribution")

def execute_race_distribution():
    ctx = input_data

    correlation_id = ctx["correlation_id"]
    race_id = ctx["resolution_key_value"]
    pot = Decimal(ctx["value"])

    # Idempotency check: has this race already been distributed?
    step(name="check_idempotency")
    existing = position_keeping.query_logs(
        correlation_id=correlation_id,
        instrument_code="GBP",
    )

    if existing.count > 0:
        return {"status": "ALREADY_DISTRIBUTED", "correlation_id": correlation_id}

    # Find the syndicate organization that placed bets on this race.
    # The race identifier from the observation's resolution_key_value
    # is used to locate the syndicate via reference data query.
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

    # Distribute pot by allocation share
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
