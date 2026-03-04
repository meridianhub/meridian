# Saga: generate_monthly_invoice
# Version: 1.0.0
# Author: Tenant Configuration (Cloud Compute)
# Date: 2026-03-04
#
# Generates monthly invoices by iterating all usage accounts, valuing
# accumulated consumption (GPU hours, API calls, storage) in USD, and
# charging the associated billing account.
#
# Trigger: scheduled:monthly_billing
#
# Input data (from scheduled trigger):
#   - billing_period: string - The billing period to invoice (e.g., "2026-03")

# Define the saga
generate_monthly_invoice_saga = saga(name="generate_monthly_invoice")

def execute_generate_monthly_invoice():
    ctx = input_data
    billing_period = ctx["billing_period"]

    # Get all usage accounts for this period
    step(name="list_usage_accounts")
    accounts = position_keeping.list_accounts(
        account_type="USAGE",
    )

    for account in accounts:
        # Calculate total usage cost per instrument
        for instrument in ["GPU_HOUR", "API_CALL", "STORAGE_GB"]:
            step(name="retrieve_balance_" + instrument)
            balance = position_keeping.retrieve_balance(
                account_id=account.account_id,
                instrument_code=instrument,
            )

            if balance.amount > 0:
                # Value usage in USD
                step(name="valuate_" + instrument)
                valuation = current_account.evaluate_asset_valuation(
                    account_id=account.billing_account_id,
                    instrument_code=instrument,
                    amount=balance.amount,
                )

                # Charge billing account
                step(name="charge_" + instrument)
                current_account.execute_withdrawal(
                    account_id=account.billing_account_id,
                    amount=valuation.output.amount,
                    instrument_code="USD",
                    reference="invoice:" + billing_period + ":" + instrument,
                )

    return {"status": "invoiced", "billing_period": billing_period}

# Execute the saga
output = execute_generate_monthly_invoice()
