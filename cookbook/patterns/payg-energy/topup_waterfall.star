# Saga: topup_waterfall
# Version: 1.0.0
# Previous: none
# Changed: Initial implementation
# Author: Meridian Platform Team
# Date: 2026-03-26
#
# Prepayment top-up allocation waterfall for Utilita's PAYG model.
#
# When a customer tops up (e.g., GBP 50.00), the payment is allocated
# through a strict priority waterfall:
#
#   1. Emergency credit repayment (clear outstanding EC receivable)
#   2. Debt recovery siphon (25% of post-EC net, configurable 20-100%)
#   3. Prepayment liability credit (remainder funds the meter balance)
#
# VAT is recognised at top-up, not at consumption (HMRC Regulation 86:
# tax point for continuous energy supply is earlier of payment receipt
# or invoice issuance; domestic PAYG customers receive no invoice).
#
# After ledger entries, dispatches DCC SRV 2.2 (Top Up Device) via
# the Operational Gateway to Procode's IDA, which delivers the credit
# to the SMETS2 meter over the WAN.
#
# Trigger: api:/v1/topups
#
# Double-Entry Accounting (GBP 50.00 top-up, GBP 10 debt, GBP 5 EC):
#
#   DR Cash / Bank                GBP 50.00   (payment received)
#   CR VAT Output Tax             GBP  2.38   (5% VAT on gross)
#   CR Debt Recovery Receivable   GBP 10.00   (25% of GBP 40 ex-EC net)
#   CR Emergency Credit Recv      GBP  5.00   (clear outstanding EC)
#   CR Prepayment Liability       GBP 32.62   (remainder to meter)
#
# Steps (executed sequentially):
#   1. validate_topup: Check amount and customer exist
#   2. check_idempotency: Prevent duplicate processing
#   3. recognise_vat: VAT at top-up (HMRC Reg 86)
#   4. check_emergency_credit: Query outstanding EC balance
#   5. repay_emergency_credit: Clear EC receivable if outstanding
#   6. check_debt: Query outstanding debt balance
#   7. allocate_debt_recovery: Siphon 25% for debt repayment
#   8. credit_prepayment: Remainder to prepayment liability
#   9. dispatch_meter_topup: DCC SRV 2.2 via Procode IDA
#
# Compensation Order (LIFO):
#   9. Cancel DCC instruction (operational_gateway.cancel_instruction)
#   8. Reverse prepayment credit
#   7. Reverse debt allocation
#   5. Reverse EC repayment
#   3. Reverse VAT recognition
#
# Input data:
#   - party_id: string - Customer party identifier
#   - amount_pence: int - Top-up amount in pence (e.g., 5000 = GBP 50.00)
#   - mpxn: string - Meter Point Reference Number (MPAN or MPRN)
#   - fuel_type: string - "electricity" or "gas"
#   - payment_reference: string - External payment reference (required, must be unique per top-up)
#   - debt_recovery_rate: string - Override rate (default "25", clamped to 20-100)

topup_saga = saga(name="topup_waterfall")

def execute_topup():
    ctx = input_data

    party_id = ctx["party_id"]
    amount_pence = int(ctx["amount_pence"])
    mpxn = ctx["mpxn"]
    fuel_type = ctx.get("fuel_type", "electricity")
    payment_ref = ctx.get("payment_reference", "")
    if not payment_ref:
        return {"status": "VALIDATION_ERROR", "reason": "payment_reference is required"}

    # Clamp debt recovery rate to Ofgem-permitted range (20-100%)
    debt_recovery_rate = Decimal(ctx.get("debt_recovery_rate", "25"))
    if debt_recovery_rate < Decimal("20"):
        debt_recovery_rate = Decimal("20")
    if debt_recovery_rate > Decimal("100"):
        debt_recovery_rate = Decimal("100")

    gross_amount = Decimal(str(amount_pence)) / Decimal("100")
    vat_rate = Decimal("0.05")

    # Step 1: Validate the top-up
    step(name="validate_topup")
    if amount_pence <= 0:
        return {"status": "VALIDATION_ERROR", "reason": "amount must be positive"}

    customer = reference_data.get_account(id="PREPAYMENT_LIABILITY:" + party_id)
    if not customer:
        return {"status": "VALIDATION_ERROR", "reason": "customer not found"}

    # Step 2: Idempotency - check if this top-up was already processed
    step(name="check_idempotency")
    existing = position_keeping.query_logs(
        correlation_id=payment_ref,
        instrument_code="GBP",
        position_id="PREPAYMENT_LIABILITY:" + party_id,
    )
    if existing.count > 0:
        return {"status": "ALREADY_PROCESSED", "correlation_id": payment_ref}

    # Step 3: Recognise VAT at top-up (HMRC Regulation 86)
    # VAT tax point is payment receipt for domestic PAYG energy.
    # This is a critical compliance point: VAT follows cash, not consumption.
    step(name="recognise_vat")
    # Round VAT to penny precision so ledger and meter amounts match exactly
    vat_exact = gross_amount * vat_rate / (Decimal("1") + vat_rate)
    vat_pence = int(vat_exact * Decimal("100"))
    vat_amount = Decimal(str(vat_pence)) / Decimal("100")
    net_amount = gross_amount - vat_amount

    position_keeping.initiate_log(
        position_id="UTILITA_VAT_OUTPUT",
        instrument_code="GBP",
        amount=vat_amount,
        direction="CREDIT",
        correlation_id=payment_ref,
        description="VAT on top-up (HMRC Reg 86): " + payment_ref,
    )

    # Remaining amount after VAT for allocation waterfall
    allocatable = net_amount

    # Step 4: Check for outstanding emergency credit
    step(name="check_emergency_credit")
    ec_balance = internal_account.get_balance(
        account_code="EMERGENCY_CREDIT:" + party_id,
        instrument_code="GBP",
    )
    ec_outstanding = Decimal(str(ec_balance.amount)) if ec_balance.amount > 0 else Decimal("0")

    # Step 5: Repay emergency credit first (highest priority after VAT)
    ec_repaid = Decimal("0")
    if ec_outstanding > Decimal("0"):
        step(name="repay_emergency_credit")
        ec_repaid = ec_outstanding
        if ec_repaid > allocatable:
            ec_repaid = allocatable

        # Clear the receivable
        position_keeping.initiate_log(
            position_id="EMERGENCY_CREDIT:" + party_id,
            instrument_code="GBP",
            amount=ec_repaid,
            direction="CREDIT",
            correlation_id=payment_ref,
            description="EC repayment from top-up",
        )
        allocatable = allocatable - ec_repaid

    # Step 6: Check for outstanding debt
    step(name="check_debt")
    debt_balance = internal_account.get_balance(
        account_code="DEBT_RECOVERY:" + party_id,
        instrument_code="GBP",
    )
    debt_outstanding = Decimal(str(debt_balance.amount)) if debt_balance.amount > 0 else Decimal("0")

    # Step 7: Allocate debt recovery (25% of remaining, capped at outstanding debt)
    debt_allocated = Decimal("0")
    if debt_outstanding > Decimal("0") and allocatable > Decimal("0"):
        step(name="allocate_debt_recovery")
        debt_siphon = allocatable * debt_recovery_rate / Decimal("100")
        debt_allocated = debt_siphon
        if debt_allocated > debt_outstanding:
            debt_allocated = debt_outstanding

        position_keeping.initiate_log(
            position_id="DEBT_RECOVERY:" + party_id,
            instrument_code="GBP",
            amount=debt_allocated,
            direction="CREDIT",
            correlation_id=payment_ref,
            description="Debt recovery at " + str(debt_recovery_rate) + "% from top-up",
        )
        allocatable = allocatable - debt_allocated

    # Step 8: Credit remainder to prepayment liability (funds the meter)
    step(name="credit_prepayment")
    position_keeping.initiate_log(
        position_id="PREPAYMENT_LIABILITY:" + party_id,
        instrument_code="GBP",
        amount=allocatable,
        direction="CREDIT",
        correlation_id=payment_ref,
        description="Prepayment credit from top-up",
    )

    # Step 9: Dispatch DCC SRV 2.2 (Top Up Device) to the smart meter
    # Procode IDA handles DUIS XML construction, cryptographic signing,
    # and WAN delivery via the appropriate CSP (Telefonica/Arqiva).
    step(name="dispatch_meter_topup")
    meter_credit_pence = int(allocatable * Decimal("100"))

    dcc_result = operational_gateway.dispatch_instruction(
        instruction_type="meter.topup",
        correlation_id=payment_ref,
        payload={
            "mpxn": mpxn,
            "amount_pence": str(meter_credit_pence),
            "fuel_type": fuel_type,
            "srv_type": "SRV_2_2",
        },
    )

    return {
        "status": "TOPPED_UP",
        "gross_amount": str(gross_amount),
        "vat_amount": str(vat_amount),
        "ec_repaid": str(ec_repaid),
        "debt_allocated": str(debt_allocated),
        "meter_credit": str(allocatable),
        "dcc_instruction_id": dcc_result.instruction_id,
        "correlation_id": payment_ref,
    }

output = execute_topup()
