# Saga: weighbridge_true_up
# Version: 1.0.0
# Author: Tenant Configuration (Waste Tracking)
# Date: 2026-06-11
#
# Reconciles estimated tonnage against weighbridge actuals. Drivers declare
# tonnage at collection; the receiving site weighs the load on arrival. The
# compliance system posts this webhook when the weighbridge ticket lands.
#
# This is the quality ladder in miniature: the original TONNE_WASTE position
# was recorded at ESTIMATE quality, and this saga books the delta at ACTUAL.
#
#   - Positive delta (under-declared): an additional TONNE_WASTE DEBIT, which
#     flows through meter_billing like any other tonnage - allowance first,
#     then overage. No special billing path.
#   - Negative delta (over-declared): a TONNE_WASTE CREDIT on the metered
#     account. Credits do not match meter_billing's DEBIT filter, so no money
#     moves here; the monthly invoice nets metered tonnage when it closes the
#     period. The customer is never charged for weight that was not real.
#
# Trigger: webhook:weighbridge_actual
#
# Input data (from webhook payload):
#   - movement_id: string - Movement identifier in the compliance system
#   - tonnage_account: string - TONNE_WASTE metering account ID
#   - declared_tonnes: decimal - Tonnage recorded at completion
#   - actual_tonnes: decimal - Weighbridge measurement
#   - weighbridge_ticket: string - Ticket reference for the audit trail
#   - billing_period: string - Billing period identifier (e.g. "2026-06")

weighbridge_true_up_saga = saga(name="weighbridge_true_up")

def execute_weighbridge_true_up():
    ctx = input_data

    movement_id = ctx["movement_id"]
    declared = Decimal(str(ctx["declared_tonnes"]))
    actual = Decimal(str(ctx["actual_tonnes"]))
    delta = actual - declared

    # Idempotency: one true-up per movement
    step(name="check_idempotency")
    existing = position_keeping.query_logs(
        correlation_id="waste-tu:" + movement_id,
        instrument_code="TONNE_WASTE",
        position_id=ctx["tonnage_account"],
    )
    if existing.count > 0:
        return {"status": "ALREADY_TRUED_UP", "movement_id": movement_id}

    if delta == Decimal("0"):
        return {"status": "EXACT_MATCH", "movement_id": movement_id}

    direction = "DEBIT" if delta > Decimal("0") else "CREDIT"
    magnitude = delta if delta > Decimal("0") else -delta

    step(name="book_true_up")
    position_keeping.initiate_log(
        position_id=ctx["tonnage_account"],
        amount=magnitude,
        instrument_code="TONNE_WASTE",
        direction=direction,
        correlation_id="waste-tu:" + movement_id,
        attributes={
            "movement_id": movement_id,
            "billing_period": ctx["billing_period"],
            "quality": "ACTUAL",
            "weighbridge_ticket": ctx.get("weighbridge_ticket", ""),
            "declared_tonnes": str(declared),
            "actual_tonnes": str(actual),
        },
    )

    return {
        "status": "TRUED_UP",
        "movement_id": movement_id,
        "delta": str(delta),
        "direction": direction,
    }

output = execute_weighbridge_true_up()
