# Starlark Saga Development Guide

Starlark is a Python-like scripting language used to define saga workflows in Meridian.
It is intentionally bounded: no `while` loops, no recursion, guaranteed termination.

## Key Constraints

- **No while loops**: Only `for` loops over finite iterables.
- **No recursion**: Functions cannot call themselves.
- **No mutable global state**: All state is local to the saga execution.
- **Deterministic**: Same inputs always produce the same outputs.

## Saga Structure

A saga script receives a `ctx` object and uses service module clients to perform operations.
Each step calls a handler, which is type-checked against the schema at load time.

```python
# Example: Simple transfer saga
def run(ctx):
    params = ctx.params()
    amount = params["amount"]
    from_account = params["from_account_id"]
    to_account = params["to_account_id"]

    # Debit the source account
    position_keeping.initiate_log(
        amount=Decimal(amount),
        direction="DEBIT",
        account_id=from_account,
        instrument_code="GBP",
    )

    # Credit the destination account
    position_keeping.initiate_log(
        amount=Decimal(amount),
        direction="CREDIT",
        account_id=to_account,
        instrument_code="GBP",
    )
```

## Available Service Modules

Service modules are auto-generated from `handlers.yaml` and provide type-safe handler calls.

### position_keeping

- `initiate_log(amount, direction, account_id, instrument_code)` — Record a position movement.
- `get_balance(account_id, instrument_code)` — Retrieve current balance.

### financial_accounting

- `record_journal_entry(entries)` — Record a double-entry journal.

### reference_data

- `get_instrument(code)` — Retrieve instrument definition.
- `get_account_type(code)` — Retrieve account type definition.

## CEL Expressions

CEL (Common Expression Language) is used for validation and bucketing rules:

```cel
# Validation: amount must be positive and within credit limit
amount > 0 && amount <= account.credit_limit

# Bucketing: group by date and account type
date_trunc(transaction.created_at, "day") + "_" + account.type_code
```

CEL is evaluated in < 10ms, is purely functional (no side effects), and cannot loop.

## Compensation

Each saga step can define a compensation action that runs if a later step fails:

```python
def run(ctx):
    lien_id = create_lien(ctx, amount)
    # If the next step fails, release_lien is called automatically
    ctx.compensate(release_lien, lien_id=lien_id)

    charge_card(ctx, amount)
```

## Error Handling

Use `ctx.fail(reason)` to abort a saga with a clear error message:

```python
if balance < amount:
    ctx.fail("insufficient_funds: balance=%s, required=%s" % (balance, amount))
```

## Best Practices

1. **Validate inputs early** — Check params before calling any service.
2. **Register compensations immediately** — Register before the step that might fail.
3. **Use descriptive correlation IDs** — Pass `correlation_id` to all handler calls.
4. **Keep sagas short** — Each saga should do one logical business operation.
5. **Avoid reading your own writes** — Sagas are not ACID transactions; reads may be stale.
