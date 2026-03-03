# PRD-032 Manifest Examples

Reference Starlark saga scripts showing how tenants use the `event:` trigger type
across different industries. These are **tenant configuration, not platform code** —
they validate that the platform infrastructure is sufficiently general.

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

The saga looks up the account type's `DefaultConversionMethodID` and `ValuationMethods`
to determine which valuation scripts to use. This makes the account type metadata
load-bearing — it drives the valuation logic without the saga hard-coding rates.

### compute\_billing.star — Cloud: Usage Billing

A cloud provider converts GPU-hours to USD charges.

| Aspect | Detail |
|--------|--------|
| Trigger | `event:position-keeping.transaction-captured.v1` |
| Filter | `event.instrument_code == 'GPU_HOUR' && event.direction == 'DEBIT'` |
| Pattern | Single-leg valuation |
| Idempotency | Checks USD charge exists for correlation\_id |

Simpler than energy (one leg instead of two). Same pattern: look up account type,
get conversion method, compute, book.

### race\_result\_distribution.star — Betting: Party Hierarchy Distribution

A betting platform distributes pot winnings across syndicate members when a
horse race completes.

| Aspect | Detail |
|--------|--------|
| Trigger | `event:market-information.observation-recorded.v1` |
| Filter | `event.dataset_code == 'HORSE_RACING' && event.status == 'OFFICIAL'` |
| Pattern | Entity graph traversal (market data -> party hierarchy -> positions) |
| Key modules | `reference_data.query`, `party.list_participants`, `party.get_structuring_data` |

This demonstrates the full entity graph: market data event triggers lookup of a party
organization, traversal of its syndicate hierarchy, retrieval of each participant's
allocation share, and position booking on each participant's payout account. The event
source is market data, not position-keeping.

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

Account model per client per instrument:

- **Custody account** (instrument units) — what you own, unchanged by this saga
- **Cost basis account** (GBP) — what it cost, adjusted by this saga
- **Market value** — not an account, computed by valuation engine at query time

The position log on the cost basis account IS the audit trail. Not reconstructed
from trade confirmations — retrieved directly.

## Common Patterns

All examples share these patterns:

1. **`saga(name=...)`** — declare the saga at the top
2. **`input_data`** — global dict containing event payload fields
3. **`step(name=...)`** — mark each durable step for compensation
4. **`Decimal(...)`** — safe decimal arithmetic (no floating point)
5. **Idempotency check first** — query for existing work before proceeding
6. **Service module calls** — `position_keeping.*`, `reference_data.*`, `party.*`, `valuation_engine.*`

## Validation

These scripts are validated by `docs/prd/032-examples/validate_examples_test.go`
which checks Starlark syntax using the saga validator with mock handler bindings.

Run:

```bash
go test ./docs/prd/032-examples/ -v
```
