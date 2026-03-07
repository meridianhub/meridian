# Saga: corporate_action_cost_adjustment
# Version: 3.0.0
# Previous: 2.0.0
# Changed: Use observation-recorded.v1 channel instead of aspirational corporate-action.v1
# Author: Tenant Configuration (Wealth Management)
# Date: 2026-03-03
#
# Adjusts cost basis for all holdings of an instrument when a corporate action
# occurs. Handles "phantom events" where no cash or units move but the tax
# position changes (e.g., accumulating ETF dividends).
#
# Trigger: event:market-information.observation-recorded.v1
# Filter:  event.observation_type == 'CORPORATE_ACTION' && event.metadata.action_type == 'ACCUMULATING_DIVIDEND'
#
# Uses the market-information.observation-recorded.v1 channel with a CEL filter
# to select corporate action observations. The observation payload carries
# instrument_code, action_type, amount_per_unit, and ex_date in its metadata.
# This demonstrates how wealth management use cases compose on top of the
# existing market information event infrastructure.
#
# Account model:
#   - Custody Account (instrument units): what you own (unchanged by this saga)
#   - Cost Basis Account (GBP): what it cost (adjusted by this saga)
#   - Market Value: not an account, computed by valuation engine at query time
#
# The cost basis account is the key design choice: it's a GBP position that
# changes when economic reality changes, even when no cash or units move.
# The position log on this account IS the audit trail for HMRC.
#
# Input data (from hypothetical CorporateActionEvent):
#   - correlation_id: string - Idempotency key from standard headers
#   - instrument_code: string - Instrument affected by the corporate action
#   - action_type: string - Type of corporate action (filtered by CEL)
#   - amount_per_unit: string - Dividend amount per unit as decimal string
#   - ex_date: string - Ex-dividend date (ISO 8601)
#
# Entity graph resolution (via service module calls):
#   - holdings: from position_keeping.query_accounts(instrument_code=...)
#   - balances: from position_keeping.get_balance(account_id=...)
#   - cost_basis_account_id: from holding metadata

# Define the saga
cost_adjustment_saga = saga(name="corporate_action_cost_adjustment")

def execute_cost_adjustment():
    ctx = input_data

    correlation_id = ctx["correlation_id"]
    instrument_code = ctx["instrument_code"]
    amount_per_unit = Decimal(ctx["amount_per_unit"])
    ex_date = ctx["ex_date"]

    # Idempotency check: has this corporate action already been processed?
    step(name="check_idempotency")
    existing = position_keeping.query_logs(
        correlation_id=correlation_id,
        instrument_code="GBP",
    )

    if existing.count > 0:
        return {"status": "ALREADY_ADJUSTED", "correlation_id": correlation_id}

    # Find all custody accounts holding this instrument
    step(name="find_holdings")
    holdings = position_keeping.query_accounts(
        instrument_code=instrument_code,
    )

    adjustment_count = 0

    for holding in holdings:
        # Get current units on this account
        step(name="get_balance_" + str(adjustment_count))
        position = position_keeping.get_balance(
            account_id=holding.account_id,
        )

        if position.amount == 0:
            continue

        # Cost basis adjustment: units x dividend per unit
        # No cash moves. No units change. But the tax position changed.
        adjustment = position.amount * amount_per_unit

        step(name="book_adjustment_" + str(adjustment_count))
        position_keeping.initiate_log(
            account_id=holding.metadata.cost_basis_account_id,
            instrument_code="GBP",
            direction="CREDIT",
            amount=adjustment,
            correlation_id=correlation_id,
            description="Accumulating dividend: " + instrument_code,
            reference=ex_date,
        )

        adjustment_count = adjustment_count + 1

    return {
        "status": "ADJUSTED",
        "instrument_code": instrument_code,
        "holdings_adjusted": adjustment_count,
        "amount_per_unit": str(amount_per_unit),
        "ex_date": ex_date,
    }

# Execute the saga
output = execute_cost_adjustment()
