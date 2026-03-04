# Saga: kyc_on_party
# Version: 1.0.0
# Author: Tenant Configuration (Financial Services)
# Date: 2026-03-04
#
# Initiates KYC verification workflow when an individual party is created.
# Demonstrates the entity graph resolution pattern for party-triggered events:
# resolve party details, check existing KYC state, and book a compliance
# reserve position to track the outstanding verification obligation.
#
# Trigger: event:party.created.v1
# Filter:  event.party_type == 'INDIVIDUAL'
#
# NOTE: The party.created.v1 channel is aspirational — it does not exist in
# the current event inventory. When implemented, the proto would define fields
# such as party_id, party_type, jurisdiction_code, and created_at. This
# example validates the platform's ability to support compliance use cases
# once the event is defined.
#
# Pattern:
#   1. Resolve party details from entity graph
#   2. Idempotency check via position log correlation_id
#   3. Look up the compliance account for this jurisdiction
#   4. Book a nominal compliance reserve position (tracks outstanding KYC)
#
# The compliance reserve is a GBP position at zero amount used as a durable
# record that KYC has been triggered for this party. Downstream reconciliation
# processes query for these positions to drive the KYC workflow.
#
# Input data (from hypothetical PartyCreatedEvent):
#   - correlation_id: string - Idempotency key from standard headers
#   - party_id: string (UUID) - The newly created party identifier
#   - party_type: string - Party classification (filtered by CEL to 'INDIVIDUAL')
#   - jurisdiction_code: string - Regulatory jurisdiction (e.g., 'GB', 'US')
#   - created_at: string - ISO 8601 timestamp of party creation
#
# Entity graph resolution (via service module calls):
#   - party details: from party.get(party_id=...)
#   - compliance account: from reference_data.query(entity_type="account", ...)

# Define the saga
kyc_on_party_saga = saga(name="kyc_on_party")

def execute_kyc_on_party():
    ctx = input_data

    correlation_id = ctx["correlation_id"]
    party_id = ctx["party_id"]
    jurisdiction_code = ctx["jurisdiction_code"]

    # Resolve the newly created party from the entity graph.
    # Confirms the party exists and loads its jurisdiction metadata.
    # In a real implementation, party.jurisdiction_code would be used here
    # instead of relying on the event field directly.
    step(name="lookup_party")
    party.get(party_id=party_id)

    # Idempotency check: has KYC already been triggered for this party?
    step(name="check_idempotency")
    existing = position_keeping.query_logs(
        correlation_id=correlation_id,
    )

    if existing.count > 0:
        return {"status": "ALREADY_INITIATED", "party_id": party_id}

    # Find the compliance account for this jurisdiction.
    # Each jurisdiction has a dedicated compliance account used to track
    # outstanding KYC obligations as position logs.
    #
    # jurisdiction_code is validated to strictly 2 ASCII alpha characters (ISO 3166-1
    # alpha-2) before embedding in the filter string to prevent predicate injection.
    step(name="find_compliance_account")
    valid_jc = len(jurisdiction_code) == 2 and jurisdiction_code[0].isalpha() and jurisdiction_code[1].isalpha()
    if not valid_jc:
        return {
            "status": "INVALID_JURISDICTION_CODE",
            "jurisdiction_code": jurisdiction_code,
            "party_id": party_id,
        }

    compliance_accounts = reference_data.query(
        entity_type="account",
        filter="metadata.jurisdiction_code == '" + jurisdiction_code + "' && metadata.account_purpose == 'KYC_COMPLIANCE'",
    )

    if compliance_accounts.count == 0:
        return {
            "status": "NO_COMPLIANCE_ACCOUNT",
            "jurisdiction_code": jurisdiction_code,
            "party_id": party_id,
        }

    if compliance_accounts.count > 1:
        return {
            "status": "AMBIGUOUS_COMPLIANCE_ACCOUNT",
            "jurisdiction_code": jurisdiction_code,
            "party_id": party_id,
        }

    compliance_account_id = compliance_accounts.items[0].account_id

    # Book a compliance reserve position to record the outstanding KYC obligation.
    # Amount is zero — this is a marker position, not a financial movement.
    # The position log reference field carries the party_id for downstream lookup.
    # Description is PII-free; reference=party_id is the linkage for downstream queries.
    step(name="book_kyc_marker")
    position_keeping.initiate_log(
        account_id=compliance_account_id,
        instrument_code="GBP",
        direction="DEBIT",
        amount=Decimal("0"),
        correlation_id=correlation_id,
        description="KYC initiated",
        reference=party_id,
    )

    return {
        "status": "KYC_INITIATED",
        "party_id": party_id,
        "jurisdiction_code": jurisdiction_code,
        "compliance_account_id": compliance_account_id,
    }

# Execute the saga
output = execute_kyc_on_party()
