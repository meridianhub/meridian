# PRD-032 Manifest Examples

Reference Starlark saga scripts showing how tenants use the `event:` trigger type
across different industries. These are **tenant configuration, not platform code** —
they validate that the platform infrastructure is sufficiently general.

## Thin Events, Rich Sagas

All examples follow the **thin event pattern** (PRD-032 section 2.10): the Starlark
`input_data` dictionary contains only the fields from the event proto message (as
deserialized via the AsyncAPI channel → proto type registry). Fields not present in
the event — such as `billing_account_id`, `account_type_code`, or `region_code` —
are resolved by the saga via service module calls against the entity graph.

This pattern:
- Keeps event schemas stable (adding a new saga doesn't change the publisher)
- Makes the entity graph load-bearing (account type metadata drives valuation logic)
- Enables upfront validation (section 2.11) of `input_data` field accesses at manifest apply

## Examples

### usage\_to\_value.star — Energy: Cross-Instrument Valuation

An energy retailer values kWh meter reads at retail and wholesale rates.

| Aspect | Detail |
|--------|--------|
| Trigger | `event:position-keeping.transaction-captured.v1` |
| Filter | `event.instrument_code != 'GBP' && event.direction == 'DEBIT'` |
| Pattern | Two-leg valuation (retail + wholesale) |
| Idempotency | Checks both GBP legs exist for correlation\_id |
| Chain termination | GBP positions rejected by filter (`instrument_code != 'GBP'`) |
| Entity graph | `reference_data.get_account` → `reference_data.get_account_type` |

The saga resolves the account from the event's `account_id`, looks up the account
type's `DefaultConversionMethodID` and `ValuationMethods`, and uses them to drive
the valuation logic. The account type metadata is load-bearing.

### compute\_billing.star — Cloud: Usage Billing

A cloud provider converts GPU-hours to USD charges.

| Aspect | Detail |
|--------|--------|
| Trigger | `event:position-keeping.transaction-captured.v1` |
| Filter | `event.instrument_code == 'GPU_HOUR' && event.direction == 'DEBIT'` |
| Pattern | Single-leg valuation |
| Idempotency | Checks USD charge exists for correlation\_id |
| Entity graph | `reference_data.get_account` → `reference_data.get_account_type` |

Simpler than energy (one leg instead of two). Same pattern: resolve account from
event, look up account type, get conversion method, compute, book.

### race\_result\_distribution.star — Betting: Party Hierarchy Distribution

A betting platform distributes pot winnings across syndicate members when a
horse race completes.

| Aspect | Detail |
|--------|--------|
| Trigger | `event:market-information.observation-recorded.v1` |
| Filter | `event.dataset_code == 'HORSE_RACING' && event.quality == 'ACTUAL'` |
| Pattern | Entity graph traversal (market data -> party hierarchy -> positions) |
| Key modules | `reference_data.query`, `party.list_participants`, `party.get_structuring_data` |

This demonstrates the full entity graph: market data event triggers lookup of a party
organization, traversal of its syndicate hierarchy, retrieval of each participant's
allocation share, and position booking on each participant's payout account. The event
source is market data, not position-keeping. Uses `resolution_key_value` as the race
identifier and `value` as the total pot from the ObservationRecorded proto.

### corporate\_action\_cost\_adjustment.star — Wealth: Phantom Event Cost Basis

A wealth platform adjusts cost basis when a corporate action occurs (e.g.,
accumulating ETF dividend). No cash moves. No units change. But the tax position
changes.

| Aspect | Detail |
|--------|--------|
| Trigger | `event:market-information.corporate-action.v1` |
| Filter | `event.action_type == 'ACCUMULATING_DIVIDEND'` |
| Pattern | One position, multiple views |
| Key insight | Cost basis is a separate GBP account adjusted by phantom events |
| Event status | Aspirational — `corporate-action.v1` channel not yet in event inventory |

Account model per client per instrument:

- **Custody account** (instrument units) — what you own, unchanged by this saga
- **Cost basis account** (GBP) — what it cost, adjusted by this saga
- **Market value** — not an account, computed by valuation engine at query time

The position log on the cost basis account IS the audit trail. Not reconstructed
from trade confirmations — retrieved directly.

### tou\_energy\_valuation.star — Energy: Time-of-Use Valuation

An energy retailer values kWh consumption at dynamic half-hourly rates from a
forecast-derived price curve.

| Aspect | Detail |
|--------|--------|
| Trigger | `event:position-keeping.transaction-captured.v1` |
| Filter | `event.instrument_code == 'KWH' && event.direction == 'DEBIT'` |
| Pattern | Time-dependent valuation via forecast-derived price curves |
| Key innovation | Event `timestamp` used as `value_date` for temporal rate lookup |
| Entity graph | `reference_data.get_account` → `reference_data.get_account_type` |

Extends the cross-instrument valuation pattern with temporal awareness: different
settlement periods have different prices (peak, off-peak, overnight). The event's
`timestamp` field (the settlement period start) is passed as `value_date` to the
valuation engine, which looks up the correct rate from the forecast-derived price
curve in Market Data.

### dynamic\_capacity\_billing.star — Compute: Dynamic Regional Pricing

A compute platform bills token consumption at rates derived from its own
utilisation forecasts per data centre region.

| Aspect | Detail |
|--------|--------|
| Trigger | `event:position-keeping.transaction-captured.v1` |
| Filter | `event.instrument_code == 'TOKEN' && event.direction == 'DEBIT'` |
| Pattern | Self-referential feedback loop (positions → forecasts → prices → billing) |
| Key innovation | Direct market data query for region-specific dynamic pricing |
| Entity graph | `reference_data.get_account` → `market_data.get_observation` |

The feedback loop: TOKEN positions accumulate per region → Forecasting Service
analyses utilisation to generate demand curves → demand curves published as
dynamic prices in Market Data → this saga reads the regional price at the
consumption timestamp → books USD charge → which is itself a position. The
`region_code` is resolved from account metadata (each usage account is per-region).

## Common Patterns

All examples share these patterns:

1. **`saga(name=...)`** — declare the saga at the top
2. **`input_data`** — global dict containing event proto fields + standard headers
3. **Entity graph resolution** — first step resolves account/entity details from event IDs
4. **`step(name=...)`** — mark each durable step for compensation
5. **`Decimal(...)`** — safe decimal arithmetic (no floating point)
6. **Idempotency check** — query for existing work before proceeding
7. **Service module calls** — `position_keeping.*`, `reference_data.*`, `party.*`, `valuation_engine.*`, `market_data.*`

## Validation

These scripts are validated by `services/control-plane/internal/applier/validate_examples_test.go`
which checks Starlark syntax using the same parser options as the saga runtime.

At manifest apply time, the platform additionally validates that `input_data` field
accesses reference fields that exist in the channel's AsyncAPI payload schema (section 2.11).

Run:

```bash
go test ./services/control-plane/internal/applier/ -run TestTenantExample -v
```
