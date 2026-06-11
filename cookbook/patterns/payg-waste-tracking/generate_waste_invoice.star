# Saga: generate_waste_invoice
# Version: 1.0.0
# Author: Tenant Configuration (Waste Tracking)
# Date: 2026-06-11
#
# Monthly billing cycle for one customer: charges the base platform fee and
# grants the new period's included allowances as unit balances. Runs once per
# customer per billing period via the scheduler.
#
# The hybrid model in one saga:
#   1. Base fee: a GBP DEBIT on the billing receivable (the subscription floor)
#   2. Allowance grant: CREDIT included movements and tonnes to the allowance
#      accounts. meter_billing drains these during the month.
#      Reference simplification: unused allowance remains on the balance until
#      the next grant. A no-rollover tenant adds a period-close saga that
#      zeroes the allowance accounts before this one credits the new period.
#
# Plan parameters come from market data, not code, so tariff changes are
# data changes:
#   - WASTE_BASE_FEE: GBP per month for the customer's tier
#   - WASTE_INCLUDED_MOVEMENTS: movements included per month
#   - WASTE_INCLUDED_TONNES: tonnes included per month
#
# Trigger: scheduled:monthly_waste_billing
#
# Input data (from scheduler):
#   - party_id: string - Customer party identifier
#   - billing_account: string - GBP billing receivable account ID
#   - billing_period: string - Period being opened (e.g. "2026-07")

generate_waste_invoice_saga = saga(name="generate_waste_invoice")

def execute_generate_waste_invoice():
    ctx = input_data

    party_id = ctx["party_id"]
    billing_period = ctx["billing_period"]
    invoice_ref = "waste-inv:" + party_id + ":" + billing_period

    # Idempotency: one base fee per customer per period
    step(name="check_idempotency")
    existing = position_keeping.query_logs(
        correlation_id=invoice_ref,
        instrument_code="GBP",
        position_id=ctx["billing_account"],
    )
    if existing.count > 0:
        return {"status": "ALREADY_INVOICED", "billing_period": billing_period}

    # Plan parameters for this period
    step(name="lookup_plan")
    base_fee_obs = market_data.get_observation(
        dataset_code="WASTE_BASE_FEE",
        effective_at=billing_period,
    )
    included_movements_obs = market_data.get_observation(
        dataset_code="WASTE_INCLUDED_MOVEMENTS",
        effective_at=billing_period,
    )
    included_tonnes_obs = market_data.get_observation(
        dataset_code="WASTE_INCLUDED_TONNES",
        effective_at=billing_period,
    )

    base_fee = Decimal(str(base_fee_obs.value))
    included_movements = Decimal(str(included_movements_obs.value))
    included_tonnes = Decimal(str(included_tonnes_obs.value))

    # 1. Base platform fee
    step(name="charge_base_fee")
    position_keeping.initiate_log(
        position_id=ctx["billing_account"],
        amount=base_fee,
        instrument_code="GBP",
        direction="DEBIT",
        correlation_id=invoice_ref,
        attributes={
            "billing_period": billing_period,
            "charge_type": "BASE_FEE",
        },
    )

    # 2. Grant the period's included allowances
    if included_movements > Decimal("0"):
        step(name="grant_movement_allowance")
        position_keeping.initiate_log(
            position_id="MOVEMENT_ALLOWANCE:" + party_id,
            amount=included_movements,
            instrument_code="WASTE_MOVEMENT",
            direction="CREDIT",
            correlation_id=invoice_ref + ":mv-allowance",
            attributes={
                "billing_period": billing_period,
            },
        )

    if included_tonnes > Decimal("0"):
        step(name="grant_tonnage_allowance")
        position_keeping.initiate_log(
            position_id="TONNAGE_ALLOWANCE:" + party_id,
            amount=included_tonnes,
            instrument_code="TONNE_WASTE",
            direction="CREDIT",
            correlation_id=invoice_ref + ":tn-allowance",
            attributes={
                "billing_period": billing_period,
            },
        )

    return {
        "status": "INVOICED",
        "billing_period": billing_period,
        "base_fee": str(base_fee),
        "included_movements": str(included_movements),
        "included_tonnes": str(included_tonnes),
    }

output = execute_generate_waste_invoice()
