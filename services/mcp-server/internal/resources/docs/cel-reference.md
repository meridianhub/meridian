# CEL (Common Expression Language) Reference

CEL is used in Meridian for validation rules, bucketing expressions, and precondition checks.
It is guaranteed to complete in < 10ms, is purely functional, and cannot modify state.

## Syntax Overview

CEL expressions are single-line, type-safe expressions that evaluate to a value.

### Basic Types

| Type    | Example                          |
|---------|----------------------------------|
| int     | `42`, `-7`                       |
| double  | `3.14`, `-0.5`                   |
| string  | `"hello"`, `'world'`             |
| bool    | `true`, `false`                  |
| list    | `[1, 2, 3]`                      |
| map     | `{"key": "value"}`               |
| null    | `null`                           |

### Arithmetic

```cel
amount * 1.05              # 5% markup
balance - amount           # subtraction
items.size() * unit_price  # list length times price
```

### Comparisons

```cel
amount > 0
amount >= min_amount && amount <= max_amount
status == "ACTIVE"
instrument_code in ["GBP", "USD", "EUR"]
```

### Logical Operators

```cel
amount > 0 && account.status == "ACTIVE"    # AND
amount > 0 || has_overdraft_facility        # OR
!account.is_frozen                          # NOT
```

### String Operations

```cel
account_type.startsWith("SAVINGS_")
instrument_code.matches("^[A-Z]{3}$")
"prefix_" + account_id
```

### Conditional (Ternary)

```cel
amount > 1000 ? "HIGH_VALUE" : "STANDARD"
```

### List Operations

```cel
items.size() > 0
items.all(x, x.amount > 0)           # all items match predicate
items.exists(x, x.status == "ERROR") # any item matches predicate
items.filter(x, x.active)            # filter list
items.map(x, x.amount)               # transform list
```

## Common Patterns in Meridian

### Validation Rules

Used in `account_types[].policies.validation` in the manifest:

```cel
# Basic amount validation
amount > 0 && amount <= 1000000

# Instrument restriction
instrument_code in allowed_instruments

# Balance check with overdraft
balance + overdraft_limit >= amount
```

### Bucketing Expressions

Used in `account_types[].policies.bucketing` to group positions:

```cel
# Daily buckets
string(int(timestamp / 86400))

# By instrument and day
instrument_code + "_" + string(int(timestamp / 86400))
```

### Precondition Expressions

Used in saga `preconditions_expression` to gate saga execution:

```cel
# Account must be active
account.status == "ACTIVE"

# Amount within daily limit
daily_total + amount <= daily_limit
```

## Type Safety

CEL is strongly typed. Type mismatches are caught at validation time, not runtime:

```cel
# Error: cannot compare string to int
account_id == 42   # type error

# Correct: compare string to string
account_id == "acc_123"
```

## Available Variables

Variables available depend on context:

- **Validation**: `amount`, `instrument_code`, `account`, `balance`
- **Bucketing**: `timestamp`, `instrument_code`, `account_type`
- **Preconditions**: `params` (saga input parameters)

## Limitations

- No loops or iteration (use list macros: `all`, `exists`, `filter`, `map`)
- No function definitions
- No I/O or side effects
- No recursion
- Maximum expression depth enforced by the runtime
