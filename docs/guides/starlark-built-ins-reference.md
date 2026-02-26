# Starlark Built-ins Reference

**Purpose:** Complete API reference for all built-in functions available in Meridian's Starlark runtime.

**Audience:** Developers, AI assistants, and tenants writing Starlark scripts.

**Related:**

- **[Style Guide](starlark-style-guide.md)** - Syntax and conventions
- **[Patterns](../starlark-patterns.md)** - Common workflow patterns
- **[Service Catalogue](../saga-service-catalogue.md)** - Service module handlers

---

## Table of Contents

1. [Core DSL Functions](#core-dsl-functions)
2. [Financial Types](#financial-types)
3. [Service Orchestration](#service-orchestration)
4. [Expression Evaluation](#expression-evaluation)
5. [Reference Resolution](#reference-resolution)
6. [Ledger Operations](#ledger-operations)
7. [Logging & Debugging](#logging--debugging)
8. [Safe Stdlib Functions](#safe-stdlib-functions)
9. [Blocked Functions](#blocked-functions)

---

## Core DSL Functions

### `saga(name)`

Define a saga workflow.

**Parameters:**

- `name` (string, required): Unique saga name

**Returns:** `SagaDefinition` object

**Example:**

```python
withdrawal_saga = saga(name="current_account_withdrawal")
```

**Usage:** Call once at module level to define saga metadata.

---

### `step(name)`

Define a saga step for compensation tracking.

**Parameters:**

- `name` (string, required): Step name (descriptive, action-oriented)

**Returns:** `StepDefinition` object

**Example:**

```python
step(name="log_position")
result = position_keeping.initiate_log(...)
```

**Compensation:** If this step fails, all previous steps are compensated in LIFO order (declared in `handlers.yaml`).

**Best practice:** Call immediately before each service module operation.

---

### `fail(message)`

Explicitly fail the saga with a custom error message.

**Parameters:**

- `message` (string, required): Error message for audit trail

**Returns:** Never returns (raises error)

**Example:**

```python
if amount <= Decimal("0"):
    fail("Withdrawal amount must be positive")
```

**Compensation:** Triggers automatic compensation of all completed steps.

**Audit:** Failure reason logged to audit trail.

---

## Financial Types

### `Decimal(value)`

Create arbitrary-precision decimal for financial calculations.

**Parameters:**

- `value` (string or number, required): Initial value

**Returns:** `Decimal` object

**Example:**

```python
# ALWAYS use Decimal for money
amount = Decimal("100.50")
rate = Decimal("1.05")
total = amount * rate  # Decimal("105.525")

# Convert string input
amount = Decimal(input_data["amount"])
```

**Supported operations:**

- Arithmetic: `+`, `-`, `*`, `/`
- Comparison: `<`, `<=`, `>`, `>=`, `==`, `!=`
- String conversion: `str(amount)` → `"100.50"`

**Why Decimal:**

- No floating-point errors
- Maintains precision for regulatory compliance
- Deterministic across platforms

**Anti-pattern:**

```python
# ❌ WRONG - Float loses precision
amount = 100.50
total = amount * 1.05  # Floating point errors

# ✅ CORRECT
amount = Decimal("100.50")
total = amount * Decimal("1.05")
```

---

## Service Orchestration

### `invoke_saga(saga_name, input)`

Invoke a child saga (saga composition).

**Parameters:**

- `saga_name` (string, required): Name of child saga
- `input` (dict, optional): Input data for child saga

**Returns:** `SagaResult` object with fields:

- `execution_id` (string): Child saga execution ID
- `status` (string): `"COMPLETED"`, `"FAILED"`, etc.
- `output` (dict): Child saga output data
- `steps_completed` (int): Number of steps executed

**Example:**

```python
# Invoke child saga
result = invoke_saga(
    saga_name="send_notification",
    input={
        "recipient": customer_id,
        "message": "Withdrawal complete"
    }
)

if result.status != "COMPLETED":
    fail(f"Notification failed: {result.execution_id}")
```

**Scope inheritance:** Child saga inherits parent's `PartyScope` (cannot escalate permissions).

**Circular detection:** Runtime prevents circular saga invocations.

**Compensation:** If parent fails, child saga's compensation is triggered automatically.

---

## Expression Evaluation

### `cel_eval(expression, variables)`

Evaluate a CEL (Common Expression Language) expression.

**Parameters:**

- `expression` (string, required): CEL expression
- `variables` (dict, optional): Variables for expression context

**Returns:** Expression result (type depends on expression)

**Example:**

```python
# Simple calculation
rate = cel_eval("spot * coefficient * markup", {
    "spot": Decimal("50.00"),
    "coefficient": Decimal("1.02"),
    "markup": Decimal("1.05")
})
# Result: Decimal("53.55")

# Conditional logic
valid = cel_eval("amount > 0 && amount <= limit", {
    "amount": Decimal("100.00"),
    "limit": Decimal("1000.00")
})
# Result: True

# Access saga context
execution_id = cel_eval("ctx.saga_execution_id", {})
```

**Available variables:**

- `ctx.saga_execution_id` - Current saga execution ID
- `ctx.correlation_id` - Correlation ID
- `input.<key>` - Variables passed to `cel_eval()`

**Use cases:**

- Financial calculations (pricing, valuation)
- Validation rules
- Conditional logic that's tenant-configurable

**CEL reference:** [CEL Specification](https://github.com/google/cel-spec)

**Performance:** ~100ns per evaluation (much faster than Starlark)

---

## Reference Resolution

### `resolve_account(reference)`

Resolve account ID from a reference (bi-temporal lookup).

**Parameters:**

- `reference` (string, required): Account reference

**Returns:** Account ID (string)

**Example:**

```python
# Resolve clearing account
clearing_id = resolve_account("CLEARING_GBP")

# Use in posting
financial_accounting.capture_posting(
    account_id=clearing_id,
    amount=amount,
    direction="CREDIT"
)
```

**Bi-temporal:** Uses saga's `knowledge_at` timestamp for deterministic replay.

**Caching:** Results cached for duration of saga execution.

**Error:** Raises error if reference not found.

---

### `resolve_instrument(reference)`

Resolve instrument ID from a reference (bi-temporal lookup).

**Parameters:**

- `reference` (string, required): Instrument reference

**Returns:** Instrument ID (string)

**Example:**

```python
# Resolve currency instrument
gbp_id = resolve_instrument("GBP")

# Use in quantity
quantity = {
    "amount": Decimal("100.00"),
    "instrument_id": gbp_id
}
```

**Bi-temporal:** Uses `knowledge_at` for deterministic replay.

**Caching:** Results cached per saga execution.

---

## Ledger Operations

### `posting(debit, credit, amount)`

Create a ledger posting (double-entry accounting).

**Parameters:**

- `debit` (string, required): Debit account ID
- `credit` (string, required): Credit account ID
- `amount` (string or Decimal, required): Posting amount

**Returns:** `Posting` object

**Example:**

```python
# Create posting
p = posting(
    debit="CUSTOMER_ACCOUNT",
    credit="CLEARING_ACCOUNT",
    amount=Decimal("100.00")
)

# Use in batch
postings = [
    posting(debit=customer_id, credit=clearing_id, amount=amount),
    posting(debit=fee_account, credit=income_account, amount=fee)
]
```

**Note:** This function creates a posting object. To actually post to the ledger, use `financial_accounting.post_entries()`.

---

## Logging & Debugging

### `log(message)`

Log a message to the audit trail.

**Parameters:**

- `message` (string, required): Log message

**Returns:** `None`

**Example:**

```python
log(f"Processing withdrawal for account {account_id}")

# Conditional logging
if debug_mode:
    log(f"Intermediate calculation: {rate}")
```

**Audit trail:** All logs are persisted with saga execution metadata.

**Performance:** Non-blocking (asynchronous audit write).

---

### `print(*args)`

Print values (routed to audit logger).

**Parameters:**

- `*args` (any, variadic): Values to print

**Returns:** `None`

**Example:**

```python
print("Amount:", amount, "Currency:", currency)
# Output: "Amount: 100.50 Currency: GBP"
```

**Note:** Unlike standard Python `print()`, this routes to structured logging.

**Audit:** Logged as `saga script print` event.

---

## Safe Stdlib Functions

Meridian includes a whitelisted subset of Starlark's standard library.

### Type Constructors

- `str(x)` - Convert to string
- `int(x)` - Convert to integer
- `float(x)` - Convert to float (prefer `Decimal()` for money)
- `bool(x)` - Convert to boolean
- `list(x)` - Convert to list
- `dict(**kwargs)` - Create dictionary
- `tuple(x)` - Convert to tuple

### Collection Operations

- `len(x)` - Length of collection
- `range(start, stop, step)` - Generate range
- `enumerate(iterable)` - Enumerate with indices
- `zip(*iterables)` - Zip multiple iterables
- `sorted(iterable, key=None, reverse=False)` - Sort collection
- `reversed(iterable)` - Reverse collection

### Aggregation

- `min(iterable)` - Minimum value
- `max(iterable)` - Maximum value
- `sum(iterable)` - Sum values
- `any(iterable)` - True if any element truthy
- `all(iterable)` - True if all elements truthy

### Numeric

- `abs(x)` - Absolute value

### Introspection

- `type(x)` - Get type name
- `repr(x)` - Get representation string
- `dir(x)` - List attributes
- `hasattr(x, name)` - Check if attribute exists
- `getattr(x, name, default)` - Get attribute value
- `hash(x)` - Get hash value

### Constants

- `True` - Boolean true
- `False` - Boolean false
- `None` - Null value

---

## Blocked Functions

For security and determinism, these functions are **not available**:

### File I/O

- ❌ `open()` - File operations
- ❌ `load()` - Load external modules

### Time & Randomness

- ❌ `time.now()` - Current time (use bi-temporal `knowledge_at`)
- ❌ `random()` - Random numbers (non-deterministic)

### Code Execution

- ❌ `exec()` - Execute code
- ❌ `compile()` - Compile code
- ❌ `eval()` - Evaluate string as code (use `cel_eval()` instead)

### Network

- ❌ `http.*` - HTTP requests

### Why blocked:**

- **Determinism:** Sagas must replay identically
- **Security:** Prevent arbitrary code execution
- **Audit:** All external calls must route through service modules

**Alternative:**

- Time: Use saga's `knowledge_at` parameter
- Random: Generate IDs in Go, pass to saga
- External data: Fetch via service module handlers
- File I/O: Not needed (scripts are stateless)

---

## Service Module Handlers

See **[handlers.yaml](../../shared/pkg/saga/schema/handlers.yaml)** for complete list of service module handlers.

**Available modules:**

- `position_keeping` - Position logs, balances
- `financial_accounting` - Booking logs, postings
- `current_account` - Account operations
- `reference_data` - Instruments, validation
- `party` - Party information
- `market_information` - Market data, settlement

**Example:**

```python
# Position keeping
result = position_keeping.initiate_log(
    position_id=account_id,
    amount=amount,
    direction="DEBIT"
)

# Financial accounting
financial_accounting.capture_posting(
    account_id=account_id,
    amount=amount,
    direction="DEBIT"
)
```

---

## Runtime Context

Sagas execute with thread-local context:

**Available via thread locals:**

- `saga.StarlarkContext` - Execution metadata
  - `saga_execution_id` - Current execution ID
  - `correlation_id` - Request correlation
  - `knowledge_at` - Bi-temporal timestamp
  - `party_scope` - Authorisation scope

**Accessed via:**

- `cel_eval()` - Exposes `ctx.*` variables
- `resolve_account()`, `resolve_instrument()` - Use `knowledge_at`
- `invoke_saga()` - Inherits `party_scope`

---

## Error Handling

**Starlark doesn't support try/except.**

**Error propagation:**

1. Service module handler fails → Error returned to Starlark
2. Starlark raises error → Saga runtime catches
3. Saga runtime triggers compensation → LIFO order
4. Compensation completes → Error reported to caller

**Explicit failure:**

```python
if not valid:
    fail("Validation failed: amount too large")
```

**Implicit failure:**

```python
# Service call fails
result = position_keeping.initiate_log(...)
# Error propagates automatically, compensation triggered
```

---

## Testing Your Scripts

**Unit tests in Go:**

```go
// Load script
scriptBytes, _ := os.ReadFile("my_saga.star")
script := string(scriptBytes)

// Create runner
runner, _ := saga.NewStarlarkSagaRunner(config)

// Execute with input
inputData := map[string]interface{}{
    "account_id": "ACC-001",
    "amount": "100.50",
}

result, err := runner.Execute(ctx, script, inputData)
require.NoError(t, err)
assert.Equal(t, "COMPLETED", result["status"])
```

**Validation:**

- Syntax errors caught at script load time
- Type errors caught at runtime
- Service module calls validated against `handlers.yaml`

---

## Performance Characteristics

| Operation | Latency | Use Case |
|-----------|---------|----------|
| `cel_eval()` | ~100ns | High-frequency calculations |
| `Decimal()` operations | ~1μs | Financial maths |
| `resolve_account()` (cached) | ~10ns | Repeated lookups |
| `resolve_account()` (uncached) | ~5ms | First lookup (RPC) |
| Service module handler | ~10-50ms | Database operations |
| `invoke_saga()` | Variable | Child saga execution time |

**Optimisation:**

- Use CEL for stateless calculations
- Cache reference resolutions (automatic)
- Minimise service module calls
- Batch operations when possible

---

## Security & Sandboxing

**Guarantees:**

- ✅ No file I/O
- ✅ No network access
- ✅ No arbitrary code execution
- ✅ Bounded execution (no `while` loops)
- ✅ Memory limits enforced
- ✅ CPU timeout enforced
- ✅ Party scope enforcement (authorisation)

**Threat model:**

- Malicious tenants cannot DoS platform
- Scripts cannot access other tenants' data
- Scripts cannot escalate privileges
- Scripts are deterministic (replay-safe)

---

## Further Reading

- **[Starlark Language Spec](https://github.com/google/starlark-go/blob/master/doc/spec.md)** - Full language reference
- **[CEL Specification](https://github.com/google/cel-spec)** - Expression language
- **[handlers.yaml](../../shared/pkg/saga/schema/handlers.yaml)** - Service module API
- **[Style Guide](starlark-style-guide.md)** - Conventions and best practices
- **[Patterns](../starlark-patterns.md)** - Common workflow patterns
