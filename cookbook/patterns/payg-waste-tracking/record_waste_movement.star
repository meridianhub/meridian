# Saga: record_waste_movement
# Version: 1.0.0
# Author: Tenant Configuration (Waste Tracking)
# Date: 2026-06-11
#
# Records a completed waste movement from the compliance system of record.
# The waste tracking application (system of record for waste transfer notes,
# hazardous consignment notes, and regulator submissions) posts this webhook
# when a movement first transitions to COMPLETED. Draft and cancelled
# movements are never sent - drafts are free.
#
# Two meters are recorded from one event:
#   - WASTE_MOVEMENT: 1 unit per completed movement
#   - TONNE_WASTE: declared tonnage, summed across the movement's waste items
#
# Tonnage may be estimated at completion (driver declaration); the
# weighbridge_true_up saga corrects it when actuals arrive. The movement
# meter is exact and final at completion.
#
# Trigger: webhook:waste_movement_completed
#
# Input data (from webhook payload):
#   - movement_id: string - Movement identifier in the compliance system
#   - movement_account: string - WASTE_MOVEMENT metering account ID
#   - tonnage_account: string - TONNE_WASTE metering account ID
#   - tonnes: decimal - Total declared tonnage across waste items
#   - is_hazardous: bool - True if any waste item carries a hazardous EWC code
#   - ewc_chapter: string - Dominant EWC chapter (e.g. "17" construction)
#   - is_estimated: bool - True if tonnage is a declaration, not weighed
#   - billing_period: string - Billing period identifier (e.g. "2026-06")

record_waste_movement_saga = saga(name="record_waste_movement")

def execute_record_waste_movement():
    ctx = input_data

    movement_id = ctx["movement_id"]
    hazard_class = "hazardous" if ctx.get("is_hazardous", False) else "standard"

    # Idempotency: a movement is metered once, ever. The compliance system
    # may re-deliver the webhook; the movement_id correlation makes that safe.
    step(name="check_idempotency")
    existing = position_keeping.query_logs(
        correlation_id="waste-mv:" + movement_id,
        instrument_code="WASTE_MOVEMENT",
        position_id=ctx["movement_account"],
    )
    if existing.count > 0:
        return {"status": "ALREADY_RECORDED", "movement_id": movement_id}

    # Meter 1: one movement unit
    step(name="record_movement")
    position_keeping.initiate_log(
        position_id=ctx["movement_account"],
        amount=Decimal("1"),
        instrument_code="WASTE_MOVEMENT",
        direction="DEBIT",
        correlation_id="waste-mv:" + movement_id,
        attributes={
            "movement_id": movement_id,
            "billing_period": ctx["billing_period"],
            "hazard_class": hazard_class,
        },
    )

    # Meter 2: declared tonnage (estimate until weighbridge actuals arrive)
    step(name="record_tonnage")
    position_keeping.initiate_log(
        position_id=ctx["tonnage_account"],
        amount=Decimal(str(ctx["tonnes"])),
        instrument_code="TONNE_WASTE",
        direction="DEBIT",
        correlation_id="waste-tn:" + movement_id,
        attributes={
            "movement_id": movement_id,
            "billing_period": ctx["billing_period"],
            "ewc_chapter": ctx.get("ewc_chapter", ""),
            "hazard_class": hazard_class,
            "quality": "ESTIMATE" if ctx.get("is_estimated", False) else "ACTUAL",
        },
    )

    return {
        "status": "RECORDED",
        "movement_id": movement_id,
        "hazard_class": hazard_class,
    }

output = execute_record_waste_movement()
