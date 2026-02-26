# Saga: current_account_deposit
# Version: 1.0.0
# Previous: none
# Changed: Updated field names: account_identification -> external_identifier, currency -> instrument_code
# Author: Platform Team
# Date: 2026-01-27
#
# This Starlark script defines the deposit saga workflow for the Current Account service.
# The saga executes a multi-step deposit operation with compensation on failure.
#
# Steps (executed sequentially):
#   1. log_position: Create CREDIT entry in PositionKeeping service
#   2. initiate_booking_log: Create booking log in FinancialAccounting service
#   3. capture_debit_posting: Post DEBIT to clearing account (double-entry source)
#   4. capture_credit_posting: Post CREDIT to customer account
#   5. finalize_booking_log: Transition booking log to POSTED
#   6. save_account: Persist account metadata
#
# Compensation Order (LIFO - Last In, First Out):
#   Failures trigger compensation of completed steps in reverse order.
#   Compensation handlers are declared in handlers.yaml schema.
#
# Input data (provided via input_data dictionary):
#   - account_id: string - Account identifier
#   - external_identifier: string - External account identifier (e.g., IBAN)
#   - amount: string - Decimal amount as string (e.g., "100.50")
#   - instrument_code: string - Instrument code (e.g., "GBP", "kWh")
#   - transaction_id: string - Unique transaction identifier
#   - clearing_account_id: string - Clearing account for double-entry (optional)

# Define the deposit saga
deposit_saga = saga(name="current_account_deposit")

# Define the saga execution function (required for conditional logic)
def execute_deposit():
    # Extract input data
    account_id = input_data["account_id"]
    external_identifier = input_data["external_identifier"]
    amount = Decimal(input_data["amount"])
    instrument_code = input_data["instrument_code"]
    transaction_id = input_data["transaction_id"]
    clearing_account_id = input_data.get("clearing_account_id", "")

    # Step 1: Log position in PositionKeeping service with CREDIT direction
    step(name="log_position")
    log_position_result = position_keeping.initiate_log(
        position_id=external_identifier,
        amount=amount,
        instrument_code=instrument_code,
        direction="CREDIT",
        transaction_id=transaction_id,
    )

    # Step 2: Initiate booking log in FinancialAccounting service
    step(name="initiate_booking_log")
    booking_log_result = financial_accounting.initiate_booking_log(
        account_id=account_id,
        instrument_code=instrument_code,
        transaction_id=transaction_id,
        transaction_type="DEPOSIT",
    )

    # Step 3: Capture DEBIT posting to clearing account (if double-entry enabled)
    # For deposits: DEBIT the clearing account (where funds come from)
    if clearing_account_id != "":
        step(name="capture_debit_posting")
        debit_result = financial_accounting.capture_posting(
            booking_log_id=booking_log_result.booking_log_id,
            account_id=clearing_account_id,
            amount=amount,
            instrument_code=instrument_code,
            direction="DEBIT",
            transaction_id=transaction_id,
            posting_type="debit",
        )

    # Step 4: Capture CREDIT posting to customer account
    step(name="capture_credit_posting")
    credit_result = financial_accounting.capture_posting(
        booking_log_id=booking_log_result.booking_log_id,
        account_id=account_id,
        amount=amount,
        instrument_code=instrument_code,
        direction="CREDIT",
        transaction_id=transaction_id,
        posting_type="credit",
    )

    # Step 5: Finalize booking log (transition to POSTED)
    step(name="finalize_booking_log")
    finalize_result = financial_accounting.update_booking_log(
        booking_log_id=booking_log_result.booking_log_id,
        status="POSTED",
    )

    # Step 6: Save account metadata
    step(name="save_account")
    save_result = current_account.save(
        account_id=account_id,
        transaction_id=transaction_id,
    )

    # Output the saga result
    result = {
        "status": "COMPLETED",
        "transaction_id": transaction_id,
        "position_log_id": log_position_result.log_id,
        "booking_log_id": booking_log_result.booking_log_id,
        "credit_posting_id": credit_result.posting_id,
    }
    return result

# Execute the saga
execute_deposit()
