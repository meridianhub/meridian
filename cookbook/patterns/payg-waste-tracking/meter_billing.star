# Saga: meter_billing
# schema-validation: skip
#   internal_account.get_balance's registered result shape does not expose
#   .amount in the strict harness (same reason every payg-energy saga skips).
# Version: 1.0.0
# Author: Tenant Configuration (Waste Tracking)
# Date: 2026-06-11
#
# Allowance-then-overage billing for both waste meters. When a metered
# position is captured (a movement unit or tonnes), this saga drains the
# customer's monthly allowance first and charges GBP only for the remainder.
#
# Allowances are ledger-native unit balances: generate_waste_invoice credits
# the month's included movements and tonnes to the allowance accounts at
# period start, and this saga consumes them. "You have 130 movements left"
# is a balance query, not a calculation.
#
# Pricing is data-driven via market data (enables tariff changes without
# script changes):
#   - WASTE_MOVEMENT_STD_RATE: GBP per standard movement (overage)
#   - WASTE_MOVEMENT_HAZ_RATE: GBP per hazardous movement (overage premium)
#   - WASTE_TONNE_RATE: GBP per tonne (overage, rate-flat regardless of class:
#     hazardous loads are low-tonnage/high-overhead, so the premium lives on
#     the movement meter where the compliance cost actually sits)
#
# Allowance units are classification-blind: a hazardous movement consumes one
# included movement, not two. The hazardous premium applies only to overage.
#
# Trigger: event:position-keeping.transaction-captured.v1
# Filter:  (event.instrument_code == 'WASTE_MOVEMENT' || event.instrument_code == 'TONNE_WASTE') && event.direction == 'DEBIT'
#
# The filter terminates the chain: this saga books GBP positions, which never
# match the WASTE_MOVEMENT/TONNE_WASTE filter.
#
# Input data (from TransactionCapturedEvent):
#   - correlation_id: string - Idempotency key from the metering log
#   - account_id: string - The metered account that triggered the event
#   - instrument_code: string - "WASTE_MOVEMENT" or "TONNE_WASTE"
#   - instrument_amount: dict - {amount, instrument_code}
#
# Entity graph resolution:
#   - billing_account_id, party_id: from account.metadata
#   - allowance account: "<ALLOWANCE_TYPE>:<party_id>" convention

meter_billing_saga = saga(name="meter_billing")

def execute_meter_billing():
    ctx = input_data

    correlation_id = ctx["correlation_id"]
    source_account_id = ctx["account_id"]
    instrument = ctx["instrument_code"]
    metered = Decimal(ctx["instrument_amount"]["amount"])
    attributes = ctx.get("attributes", {})
    billing_period = attributes.get("billing_period", "")
    hazard_class = attributes.get("hazard_class", "standard")

    # Resolve billing context from the entity graph
    step(name="lookup_account")
    account = reference_data.get_account(id=source_account_id)
    billing_account_id = account.metadata["billing_account_id"]
    party_id = account.metadata["party_id"]

    # Idempotency: one billing decision per metering event
    step(name="check_idempotency")
    existing = position_keeping.query_logs(
        correlation_id=correlation_id,
        instrument_code="GBP",
        position_id=billing_account_id,
    )
    if existing.count > 0:
        return {"status": "ALREADY_BILLED", "correlation_id": correlation_id}

    # Select the meter-specific allowance account and overage rate
    if instrument == "WASTE_MOVEMENT":
        allowance_account = "MOVEMENT_ALLOWANCE:" + party_id
        rate_dataset = "WASTE_MOVEMENT_STD_RATE"
        if hazard_class == "hazardous":
            rate_dataset = "WASTE_MOVEMENT_HAZ_RATE"
    else:
        allowance_account = "TONNAGE_ALLOWANCE:" + party_id
        rate_dataset = "WASTE_TONNE_RATE"

    # Drain allowance first
    step(name="check_allowance")
    allowance_balance = internal_account.get_balance(
        account_id=allowance_account,
    )
    available = Decimal(str(allowance_balance.amount)) if allowance_balance.amount > 0 else Decimal("0")

    consumed = metered if metered <= available else available
    overage = metered - consumed

    if consumed > Decimal("0"):
        step(name="consume_allowance")
        position_keeping.initiate_log(
            position_id=allowance_account,
            amount=consumed,
            instrument_code=instrument,
            direction="DEBIT",
            correlation_id=correlation_id + ":allowance",
            attributes={
                "billing_period": billing_period,
                "hazard_class": hazard_class,
            },
        )

    if overage == Decimal("0"):
        return {
            "status": "WITHIN_ALLOWANCE",
            "consumed": str(consumed),
            "correlation_id": correlation_id,
        }

    # Charge overage at the tariff rate
    step(name="lookup_rate")
    rate_obs = market_data.get_observation(
        dataset_code=rate_dataset,
        effective_at=billing_period,
    )
    rate = Decimal(str(rate_obs.value))
    charge = overage * rate

    step(name="book_overage_charge")
    position_keeping.initiate_log(
        position_id=billing_account_id,
        amount=charge,
        instrument_code="GBP",
        direction="DEBIT",
        correlation_id=correlation_id,
        attributes={
            "billing_period": billing_period,
            "charge_type": "MOVEMENT_OVERAGE" if instrument == "WASTE_MOVEMENT" else "TONNAGE_OVERAGE",
            "hazard_class": hazard_class,
            "rate_dataset": rate_dataset,
        },
    )

    return {
        "status": "OVERAGE_BILLED",
        "consumed": str(consumed),
        "overage": str(overage),
        "charge": str(charge),
        "correlation_id": correlation_id,
    }

output = execute_meter_billing()
