# Saga: consumption_block_tariff
# Version: 1.0.0
# Previous: none
# Changed: Initial implementation
# Author: Meridian Platform Team
# Date: 2026-03-26
#
# Block tariff consumption valuation for Utilita's no-standing-charge model.
#
# When half-hourly meter data arrives (via DCC or MHHS Data Integration
# Platform), this saga values the kWh consumption at the correct tiered
# rate based on daily cumulative usage:
#
#   First Rate: first 2 kWh per day (embeds standing charge recovery)
#   Saver Rate: all consumption above 2 kWh daily threshold
#
# The daily block counter tracks cumulative consumption and resets at
# midnight. Each HH read (0.5 kWh typical, up to ~3 kWh for high users)
# may straddle the threshold - e.g., if daily total is 1.5 kWh and the
# read is 1.0 kWh, then 0.5 kWh is valued at First Rate and 0.5 kWh
# at Saver Rate.
#
# Revenue is recognised at consumption, not at top-up (IFRS 15 / FRS 102
# Section 23). The performance obligation is energy delivery.
#
# A parallel wholesale valuation is booked for hedging P&L attribution,
# enabling margin analysis per GSP group.
#
# Trigger: event:position-keeping.transaction-captured.v1
# Filter:  (event.instrument_code == 'KWH_ELEC' || event.instrument_code == 'KWH_GAS') && event.direction == 'DEBIT'
#
# Double-Entry (1.0 kWh electricity, daily total at 1.5 kWh before this read):
#
#   Split: 0.5 kWh @ First Rate (51.85p) = GBP 0.2593
#          0.5 kWh @ Saver Rate (26.01p) = GBP 0.1301
#          Total retail charge:           = GBP 0.3894
#
#   DR Prepayment Liability    GBP 0.3709  (customer balance decremented, ex-VAT)
#   CR Revenue - Energy        GBP 0.3709  (revenue recognised at delivery)
#
#   DR Wholesale Cost          GBP 0.0850  (wholesale position, 8.50p/kWh)
#   (Counterparty wholesale credit is booked by the platform's cross-instrument
#    valuation pipeline, not by this saga directly.)
#
# Input data (from TransactionCapturedEvent):
#   - correlation_id: string - Idempotency key
#   - account_id: string - Metered consumption account
#   - instrument_code: string - "KWH_ELEC" or "KWH_GAS"
#   - instrument_amount: dict - {amount, instrument_code}
#   - timestamp: string - ISO 8601 settlement period start

consumption_saga = saga(name="consumption_block_tariff")

def execute_consumption():
    ctx = input_data

    correlation_id = ctx["correlation_id"]
    source_account_id = ctx["account_id"]
    kwh_consumed = Decimal(ctx["instrument_amount"]["amount"])
    instrument_code = ctx["instrument_code"]
    settlement_period = ctx["timestamp"]

    # Resolve account to get billing and wholesale account references
    step(name="lookup_account")
    account = reference_data.get_account(id=source_account_id)
    metadata = account.metadata or {}

    billing_account_id = metadata.get("billing_account_id")
    wholesale_account_id = metadata.get("wholesale_account_id")
    party_id = metadata.get("party_id")

    if not billing_account_id:
        return {"status": "CONFIG_ERROR", "correlation_id": correlation_id}

    # Idempotency check - ensure we haven't already valued this read
    step(name="check_idempotency")
    existing = position_keeping.query_logs(
        correlation_id=correlation_id,
        position_id=billing_account_id,
    )
    if existing.count > 0:
        return {"status": "ALREADY_VALUED", "correlation_id": correlation_id}

    # Determine fuel type from instrument code
    fuel_type = "electricity"
    if instrument_code == "KWH_GAS":
        fuel_type = "gas"

    # Look up the two tariff rates from Market Data.
    # The block tariff has two fixed rates per fuel, set quarterly
    # to comply with the Ofgem prepayment price cap.
    step(name="lookup_tariff_rates")
    first_rate_dataset = "UTILITA_ELEC_FIRST_RATE"
    saver_rate_dataset = "UTILITA_ELEC_SAVER_RATE"
    if fuel_type == "gas":
        first_rate_dataset = "UTILITA_GAS_FIRST_RATE"
        saver_rate_dataset = "UTILITA_GAS_SAVER_RATE"

    first_rate_obs = market_information.get_rate(
        dataset_code=first_rate_dataset,
        value_date=settlement_period,
    )
    saver_rate_obs = market_information.get_rate(
        dataset_code=saver_rate_dataset,
        value_date=settlement_period,
    )

    # Market data stores VAT-inclusive rates (as published by Utilita).
    # Prepayment liability is credited net-of-VAT (topup_waterfall strips VAT
    # at top-up per HMRC Reg 86), so convert rates to ex-VAT basis here.
    vat_rate = Decimal("0.05")
    vat_divisor = Decimal("1") + vat_rate
    first_rate = Decimal(str(first_rate_obs.value)) / vat_divisor
    saver_rate = Decimal(str(saver_rate_obs.value)) / vat_divisor

    # Query the daily cumulative consumption to determine block position.
    # The daily threshold is 2 kWh (1 kWh for E7 daytime).
    # This counter resets at midnight via the settlement_date bucketing.
    step(name="query_daily_consumption")
    daily_threshold = Decimal("2.0")

    daily_balance = internal_account.get_balance(
        account_id=source_account_id,
    )
    # daily_consumed is the cumulative kWh consumed today BEFORE this read
    daily_consumed = Decimal(str(daily_balance.amount)) if daily_balance.amount > 0 else Decimal("0")
    daily_consumed_before = daily_consumed - kwh_consumed
    if daily_consumed_before < Decimal("0"):
        daily_consumed_before = Decimal("0")

    # Calculate the tiered charge.
    # If daily total straddles the 2 kWh threshold, split the read:
    #   - portion below threshold valued at First Rate
    #   - portion above threshold valued at Saver Rate
    step(name="calculate_tiered_charge")
    remaining_first_rate_kwh = daily_threshold - daily_consumed_before
    if remaining_first_rate_kwh < Decimal("0"):
        remaining_first_rate_kwh = Decimal("0")

    first_rate_kwh = kwh_consumed
    if first_rate_kwh > remaining_first_rate_kwh:
        first_rate_kwh = remaining_first_rate_kwh

    saver_rate_kwh = kwh_consumed - first_rate_kwh

    first_rate_charge = first_rate_kwh * first_rate
    saver_rate_charge = saver_rate_kwh * saver_rate
    total_retail_charge = first_rate_charge + saver_rate_charge

    # No VAT entry needed - VAT was recognised at top-up (HMRC Reg 86).
    # Revenue is recognised ex-VAT.

    # Book retail charge: debit prepayment liability, credit revenue
    step(name="book_retail_charge")
    position_keeping.initiate_log(
        position_id=billing_account_id,
        amount=total_retail_charge,
        direction="DEBIT",
        correlation_id=correlation_id,
        description=fuel_type + " " + str(kwh_consumed) + "kWh (" + str(first_rate_kwh) + " @ first, " + str(saver_rate_kwh) + " @ saver)",
    )

    step(name="book_revenue")
    revenue_account = "UTILITA_REVENUE_ELEC"
    if fuel_type == "gas":
        revenue_account = "UTILITA_REVENUE_GAS"

    position_keeping.initiate_log(
        position_id=revenue_account,
        amount=total_retail_charge,
        direction="CREDIT",
        correlation_id=correlation_id,
        description="Revenue: " + settlement_period,
    )

    # Book wholesale cost for hedging P&L (if wholesale account configured)
    wholesale_amount = Decimal("0")
    if wholesale_account_id:
        step(name="compute_wholesale_valuation")
        wholesale_dataset = "WHOLESALE_ELEC_GBP_KWH"
        if fuel_type == "gas":
            wholesale_dataset = "WHOLESALE_GAS_GBP_KWH"

        wholesale_obs = market_information.get_rate(
            dataset_code=wholesale_dataset,
            value_date=settlement_period,
        )
        wholesale_rate = Decimal(str(wholesale_obs.value))
        wholesale_amount = kwh_consumed * wholesale_rate

        step(name="book_wholesale_cost")
        position_keeping.initiate_log(
            position_id=wholesale_account_id,
            amount=wholesale_amount,
            direction="DEBIT",
            correlation_id=correlation_id,
            description="Wholesale: " + settlement_period,
        )

    return {
        "status": "VALUED",
        "fuel_type": fuel_type,
        "kwh_consumed": str(kwh_consumed),
        "first_rate_kwh": str(first_rate_kwh),
        "saver_rate_kwh": str(saver_rate_kwh),
        "first_rate_charge": str(first_rate_charge),
        "saver_rate_charge": str(saver_rate_charge),
        "total_retail_charge": str(total_retail_charge),
        "wholesale_cost": str(wholesale_amount),
        "retail_margin": str(total_retail_charge - wholesale_amount),
        "settlement_period": settlement_period,
        "correlation_id": correlation_id,
    }

output = execute_consumption()
