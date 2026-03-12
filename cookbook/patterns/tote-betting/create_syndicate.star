# schema-validation: skip
# Reason: Uses repository service module (entity CRUD) which requires
# runtime mocks beyond schema validation scope. Handler schema compliance
# for financial_gateway and position_keeping is covered by other patterns.
#
# Saga: create_syndicate
# Version: 1.0.0
# Previous: none
# Changed: Initial implementation
# Author: Platform Team
# Date: 2026-03-11
#
# Creates a new syndicate entity with the specified match, stake
# amount, and maximum members. The syndicate starts in OPEN status,
# ready to accept members via join_syndicate.
#
# No financial transactions occur at creation time — money only
# moves when members join (join_syndicate) or when the syndicate
# settles/refunds.
#
# Input data:
#   - syndicate_id: string    - Unique syndicate identifier
#   - match_id: string        - Match this syndicate is betting on
#   - stake_amount: string    - Entry stake per member (e.g., "10.00")
#   - max_members: int        - Maximum syndicate size
#   - created_by: string      - Party ID of the creator

def create_syndicate():
    ctx = input_data

    step(name="create_entity")
    repository.create_entity(
        entity_type="syndicate",
        entity_id=ctx["syndicate_id"],
        attributes={
            "match_id": ctx["match_id"],
            "stake_amount": ctx["stake_amount"],
            "max_members": str(ctx["max_members"]),
            "created_by": ctx["created_by"],
            "status": "OPEN",
        },
    )

    return {
        "syndicate_id": ctx["syndicate_id"],
        "match_id": ctx["match_id"],
        "stake_amount": ctx["stake_amount"],
        "status": "OPEN",
    }

output = create_syndicate()
