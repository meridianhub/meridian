# schema-validation: skip
# Saga: reconciliation_adjustment
# Version: 1.0.0
# Previous: none
# Changed: Initial reconciliation adjustment saga
# Author: Platform Team
# Date: 2026-02-09
#
# This Starlark script defines the reconciliation adjustment saga workflow.
# It is triggered when a variance is resolved and requires booking adjustments
# to correct the ledger entries.
#
# Steps (executed sequentially):
#   1. initiate_booking: Create a booking log for the adjustment in Financial Accounting
#   2. post_debit: Post a DEBIT entry to reverse the incorrect amount
#   3. post_credit: Post a CREDIT entry with the corrected amount
#   4. finalize: Finalize the booking log and mark the variance as resolved
#
# Compensation Order (LIFO - Last In, First Out):
#   On failure, completed steps are compensated in reverse using
#   reversal entries (not state rollbacks), consistent with the
#   platform's immutable ledger pattern:
#   - finalize compensation: create a reversal booking log entry
#   - post_credit compensation: post a reversal DEBIT entry
#   - post_debit compensation: post a reversal CREDIT entry
#   - initiate_booking compensation: mark booking log as CANCELLED
#
# Input data (provided via input_data dictionary):
#   - variance_id: string - The variance being adjusted
#   - dispute_id: string - The dispute that triggered the adjustment (optional)
#   - account_id: string - The account requiring adjustment
#   - instrument_code: string - The instrument code (e.g., "GBP", "kWh")
#   - adjustment_amount: string - Decimal amount to adjust
#   - resolved_by: string - The user/system that resolved the variance
#   - resolution: string - Description of the resolution

# Define the reconciliation adjustment saga
adjustment_saga = saga(name="reconciliation_adjustment")

def execute_adjustment():
    # Extract input data
    variance_id = input_data["variance_id"]
    dispute_id = input_data.get("dispute_id", "")
    account_id = input_data["account_id"]
    instrument_code = input_data.get("instrument_code", "GBP")
    adjustment_amount = Decimal(input_data.get("adjustment_amount", "0"))
    resolved_by = input_data.get("resolved_by", "system")
    resolution = input_data.get("resolution", "")
    transaction_id = input_data.get("transaction_id", variance_id)

    # Step 1: Initiate a booking log for the adjustment
    step(name="initiate_booking")
    booking_result = financial_accounting.initiate_booking_log(
        account_id=account_id,
        instrument_code=instrument_code,
        transaction_id=transaction_id,
        transaction_type="RECONCILIATION_ADJUSTMENT",
    )

    # Step 2: Post DEBIT entry (reverse the incorrect amount)
    step(name="post_debit")
    debit_result = financial_accounting.capture_posting(
        booking_log_id=booking_result.booking_log_id,
        account_id=account_id,
        amount=adjustment_amount,
        instrument_code=instrument_code,
        direction="DEBIT",
        transaction_id=transaction_id,
        posting_type="debit",
    )

    # Step 3: Post CREDIT entry (apply the corrected amount)
    step(name="post_credit")
    credit_result = financial_accounting.capture_posting(
        booking_log_id=booking_result.booking_log_id,
        account_id=account_id,
        amount=adjustment_amount,
        instrument_code=instrument_code,
        direction="CREDIT",
        transaction_id=transaction_id,
        posting_type="credit",
    )

    # Step 4: Finalize the booking log
    step(name="finalize")
    finalize_result = financial_accounting.update_booking_log(
        booking_log_id=booking_result.booking_log_id,
        status="POSTED",
    )

    # Output the saga result
    result = {
        "status": "COMPLETED",
        "variance_id": variance_id,
        "dispute_id": dispute_id,
        "booking_log_id": booking_result.booking_log_id,
        "debit_posting_id": debit_result.posting_id,
        "credit_posting_id": credit_result.posting_id,
        "resolved_by": resolved_by,
    }
    return result

# Execute the saga
execute_adjustment()
