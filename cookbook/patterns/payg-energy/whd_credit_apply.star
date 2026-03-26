# Saga: whd_credit_apply
# Version: 1.0.0
# Previous: none
# Changed: Initial implementation
# Author: Meridian Platform Team
# Date: 2026-03-26
#
# Warm Home Discount (WHD) credit application.
#
# GBP 150 government-mandated credit for eligible households, applied
# to the electricity meter. WHD is a supplier-funded social obligation
# cost, socialised across all customer bills.
#
# Critical: if the customer has outstanding debt, WHD must be applied
# to clear that debt FIRST before crediting the prepayment balance.
# This priority waterfall is required by scheme rules and was the
# subject of a GBP 277,000 enforcement action against Utilita in 2025
# for missing the scheme year deadline by 12 days.
#
# Trigger: api:/v1/whd/apply
#
# Double-Entry (GBP 150 WHD, GBP 30 outstanding debt):
#   DR WHD Scheme Obligation      GBP 150.00  (social obligation cost)
#   CR Debt Recovery Receivable   GBP  30.00  (clear debt first)
#   CR Prepayment Liability       GBP 120.00  (remainder to meter)
#
# Input data:
#   - party_id: string - Customer party identifier
#   - scheme_year: string - WHD scheme year (e.g., "2025-26")
#   - mpxn: string - Electricity MPAN (WHD is electricity only)
#   - eligibility_ref: string - DWP or supplier eligibility reference

whd_saga = saga(name="whd_credit_apply")

def execute_whd():
    ctx = input_data

    party_id = ctx["party_id"]
    scheme_year = ctx.get("scheme_year", "2025-26")
    mpxn = ctx["mpxn"]
    eligibility_ref = ctx.get("eligibility_ref", "")
    whd_ref = "whd_" + party_id + "_" + scheme_year

    whd_amount = Decimal("150.00")

    # Step 1: Idempotency - ensure WHD not already applied this scheme year
    step(name="check_idempotency")
    existing = position_keeping.query_logs(
        correlation_id=whd_ref,
        position_id="UTILITA_WHD",
    )
    if existing.count > 0:
        return {"status": "ALREADY_APPLIED", "correlation_id": whd_ref}

    # Step 2: Book WHD as social obligation cost
    step(name="book_whd_obligation")
    position_keeping.initiate_log(
        position_id="UTILITA_WHD",
        amount=whd_amount,
        direction="DEBIT",
        correlation_id=whd_ref,
        description="WHD " + scheme_year + " for " + party_id,
    )

    allocatable = whd_amount

    # Step 3: Check outstanding debt (WHD clears debt first per scheme rules)
    step(name="check_debt")
    debt_balance = internal_account.get_balance(
        account_id="DEBT_RECOVERY:" + party_id,
    )
    debt_outstanding = Decimal(str(debt_balance.amount)) if debt_balance.amount > 0 else Decimal("0")

    debt_cleared = Decimal("0")
    if debt_outstanding > Decimal("0"):
        step(name="clear_debt_from_whd")
        debt_cleared = debt_outstanding
        if debt_cleared > allocatable:
            debt_cleared = allocatable

        position_keeping.initiate_log(
            position_id="DEBT_RECOVERY:" + party_id,
            amount=debt_cleared,
            direction="CREDIT",
            correlation_id=whd_ref,
            description="Debt cleared from WHD " + scheme_year,
        )
        allocatable = allocatable - debt_cleared

    # Step 4: Credit remainder to prepayment liability
    step(name="credit_prepayment")
    if allocatable > Decimal("0"):
        position_keeping.initiate_log(
            position_id="PREPAYMENT_LIABILITY:" + party_id + ":electricity",
            amount=allocatable,
            direction="CREDIT",
            correlation_id=whd_ref,
            description="WHD " + scheme_year + " credit",
        )

    # Step 5: Top up the meter with the prepayment portion
    step(name="dispatch_meter_topup")
    if allocatable > Decimal("0"):
        meter_credit_pence = int(allocatable * Decimal("100"))
        operational_gateway.dispatch_instruction(
            instruction_type="meter.topup",
            correlation_id=whd_ref,
            payload={
                "mpxn": mpxn,
                "amount_pence": str(meter_credit_pence),
                "fuel_type": "electricity",
                "srv_type": "SRV_2_2",
                "source": "WHD",
            },
        )

    return {
        "status": "WHD_APPLIED",
        "whd_amount": str(whd_amount),
        "debt_cleared": str(debt_cleared),
        "meter_credit": str(allocatable),
        "scheme_year": scheme_year,
        "correlation_id": whd_ref,
    }

output = execute_whd()
