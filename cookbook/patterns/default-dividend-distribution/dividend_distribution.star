# schema-validation: skip
# Saga: dividend_distribution
# Version: 1.0.0
# Previous: none
# Changed: Initial implementation of org-scoped dividend distribution
# Author: Platform Team
# Date: 2026-02-16
#
# This Starlark script defines the dividend distribution saga for org-scoped
# syndicate workflows. It demonstrates how to use party.list_participants,
# party.get_structuring_data, build_org_account_ref, and resolve_account
# with composite references to distribute funds across syndicate members.
#
# Steps (executed sequentially):
#   1. list_participants: Retrieve all active syndicate participants
#   2. For each participant:
#      a. get_structuring_data: Get allocation share metadata
#      b. resolve_account: Resolve participant's org-scoped account
#      c. log_position: Create CREDIT entry in PositionKeeping
#   3. Return distribution result with per-participant details
#
# Input data (provided via input_data dictionary):
#   - org_id: string - Syndicate organization party ID
#   - total_amount: string - Total dividend amount as decimal string
#   - instrument_code: string - Instrument code (e.g., "GBP", "kWh")
#   - transaction_id: string - Unique transaction identifier

# Define the dividend distribution saga
distribution_saga = saga(name="dividend_distribution")

def execute_distribution():
    # Extract input data
    org_id = input_data["org_id"]
    total_amount = Decimal(input_data["total_amount"])
    instrument_code = input_data["instrument_code"]
    transaction_id = input_data["transaction_id"]

    # Step 1: List all active syndicate participants
    step(name="list_participants")
    participants = party.list_participants(
        org_id=org_id,
        relationship_type="RELATIONSHIP_TYPE_SYNDICATE_PARTICIPANT",
    )

    distributions = []

    # Step 2: For each participant, calculate and distribute
    for participant in participants:
        party_id = participant["party_id"]
        metadata = participant["metadata"]

        # Get detailed structuring data if needed
        step(name="get_structuring_data_" + party_id)
        structuring = party.get_structuring_data(
            party_id=party_id,
            org_id=org_id,
            relationship_type="RELATIONSHIP_TYPE_SYNDICATE_PARTICIPANT",
        )

        # Calculate participant's share
        allocation_share = Decimal(str(structuring.get("allocation_share", "0")))
        participant_amount = total_amount * allocation_share

        # Build composite account reference for org-scoped resolution
        account_ref = build_org_account_ref(
            party_id=party_id,
            org_id=org_id,
            currency=instrument_code,
        )

        # Resolve the org-scoped account
        account_id = resolve_account(reference=account_ref)

        # Log the credit position for this participant
        step(name="log_position_" + party_id)
        log_result = position_keeping.initiate_log(
            position_id=account_id,
            amount=participant_amount,
            instrument_code=instrument_code,
            direction="CREDIT",
            transaction_id=transaction_id,
        )

        distributions.append({
            "party_id": party_id,
            "account_id": account_id,
            "amount": str(participant_amount),
            "log_id": log_result.log_id,
        })

    # Output the saga result
    result = {
        "status": "COMPLETED",
        "transaction_id": transaction_id,
        "org_id": org_id,
        "participant_count": len(distributions),
        "distributions": distributions,
    }
    return result

# Execute the saga
execute_distribution()
