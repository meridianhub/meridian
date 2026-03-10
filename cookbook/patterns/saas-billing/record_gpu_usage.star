# Saga: record_gpu_usage
# Version: 1.0.0
# Author: Tenant Configuration (Cloud Compute)
# Date: 2026-03-04
#
# Records GPU compute usage from webhook meter events into the usage
# metering account. Each GPU job emits a webhook event with hours consumed,
# GPU type, and billing period. This saga captures the raw usage position.
#
# Trigger: webhook:gpu_meter_event
#
# Input data (from webhook payload):
#   - usage_account: string - The usage metering account ID
#   - gpu_hours: decimal - Number of GPU hours consumed
#   - billing_period: string - Billing period identifier (e.g., "2026-03")
#   - gpu_type: string - GPU model (e.g., "A100", "H100")
#   - job_id: string - Compute job identifier

# Define the saga
record_gpu_usage_saga = saga(name="record_gpu_usage")

def execute_record_gpu_usage():
    ctx = input_data

    # Record usage in metering account
    step(name="record_usage")
    position_keeping.initiate_log(
        position_id=ctx["usage_account"],
        amount=Decimal(str(ctx["gpu_hours"])),
        instrument_code="GPU_HOUR",
        direction="DEBIT",
        attributes={
            "billing_period": ctx["billing_period"],
            "gpu_type": ctx["gpu_type"],
            "job_id": ctx["job_id"],
        },
    )

    return {"status": "recorded", "job_id": ctx["job_id"]}

# Execute the saga
output = execute_record_gpu_usage()
