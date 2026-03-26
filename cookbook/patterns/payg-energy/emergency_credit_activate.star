# Saga: emergency_credit_activate
# Version: 1.0.0
# Previous: none
# Changed: Initial implementation
# Author: Meridian Platform Team
# Date: 2026-03-26
#
# Emergency Credit activation for PAYG customers.
#
# When a customer's prepayment balance falls below GBP 1.00, they can
# activate emergency credit (GBP 15 per fuel) via the IHD, meter keypad,
# or My Utilita app. This flips the accounting model: the customer's
# prepayment liability is exhausted, and the supplier extends credit,
# creating a receivable.
#
# The meter balance goes negative, running down to -GBP 15. During
# Friendly Credit hours (2pm-10am weekdays, all weekend/bank holidays),
# supply continues even beyond EC exhaustion.
#
# Trigger: api:/v1/emergency-credit/activate
#
# Double-Entry (GBP 15 EC activation):
#   DR Emergency Credit Receivable  GBP 15.00  (asset: customer owes us)
#   CR Prepayment Liability         GBP 15.00  (extends available balance)
#
# The EC receivable is automatically cleared at next top-up (topup_waterfall
# saga handles this in its allocation priority).
#
# Input data:
#   - party_id: string - Customer party identifier
#   - fuel_type: string - "electricity" or "gas"
#   - mpxn: string - Meter Point Reference Number
#   - activation_source: string - "ihd", "keypad", "app", or "remote"

ec_saga = saga(name="emergency_credit_activate")

def execute_ec_activation():
    ctx = input_data

    party_id = ctx["party_id"]
    fuel_type = ctx.get("fuel_type", "electricity")
    mpxn = ctx["mpxn"]
    activation_source = ctx.get("activation_source", "app")
    ec_limit = Decimal("15.00")
    activation_ref = "ec_" + party_id + "_" + fuel_type

    # Step 1: Check idempotency - is EC already active?
    step(name="check_idempotency")
    existing = position_keeping.query_logs(
        correlation_id=activation_ref,
        instrument_code="GBP",
        position_id="EMERGENCY_CREDIT:" + party_id,
    )
    if existing.count > 0:
        return {"status": "ALREADY_ACTIVE", "correlation_id": activation_ref}

    # Step 2: Verify prepayment balance is below activation threshold
    step(name="check_balance_threshold")
    prepay_balance = internal_account.get_balance(
        account_code="PREPAYMENT_LIABILITY:" + party_id,
        instrument_code="GBP",
    )
    balance = Decimal(str(prepay_balance.amount))
    activation_threshold = Decimal("1.00")

    if balance > activation_threshold:
        return {
            "status": "NOT_ELIGIBLE",
            "reason": "balance above threshold",
            "current_balance": str(balance),
        }

    # Step 3: Check existing EC balance (can't stack beyond limit)
    step(name="check_existing_ec")
    ec_balance = internal_account.get_balance(
        account_code="EMERGENCY_CREDIT:" + party_id,
        instrument_code="GBP",
    )
    ec_current = Decimal(str(ec_balance.amount)) if ec_balance.amount > 0 else Decimal("0")

    if ec_current >= ec_limit:
        return {
            "status": "EC_LIMIT_REACHED",
            "current_ec": str(ec_current),
            "limit": str(ec_limit),
        }

    ec_amount = ec_limit - ec_current

    # Step 4: Create emergency credit receivable (asset - customer owes supplier)
    step(name="create_ec_receivable")
    position_keeping.initiate_log(
        position_id="EMERGENCY_CREDIT:" + party_id,
        instrument_code="GBP",
        amount=ec_amount,
        direction="DEBIT",
        correlation_id=activation_ref,
        description="Emergency credit activated via " + activation_source,
    )

    # Step 5: Extend prepayment liability (customer can continue consuming)
    step(name="extend_prepayment")
    position_keeping.initiate_log(
        position_id="PREPAYMENT_LIABILITY:" + party_id,
        instrument_code="GBP",
        amount=ec_amount,
        direction="CREDIT",
        correlation_id=activation_ref,
        description="Emergency credit extension: " + fuel_type,
    )

    # Step 6: Dispatch DCC SRV 2.5 to activate EC on the meter
    step(name="dispatch_ec_activation")
    dcc_result = operational_gateway.dispatch_instruction(
        instruction_type="meter.activate_emergency_credit",
        correlation_id=activation_ref,
        payload={
            "mpxn": mpxn,
            "fuel_type": fuel_type,
            "ec_amount_pence": str(int(ec_amount * Decimal("100"))),
            "srv_type": "SRV_2_5",
        },
    )

    return {
        "status": "EC_ACTIVATED",
        "ec_amount": str(ec_amount),
        "fuel_type": fuel_type,
        "activation_source": activation_source,
        "dcc_instruction_id": dcc_result.instruction_id,
        "correlation_id": activation_ref,
    }

output = execute_ec_activation()
