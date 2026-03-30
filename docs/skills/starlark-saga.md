---
name: starlark-saga-generation
description: Generate Starlark saga scripts following Meridian conventions and style guide
triggers:
  - Creating new saga scripts
  - Writing Starlark files for saga orchestration
  - Implementing business workflows in Starlark
  - Migrating sagas to typed service modules
instructions: |
  When generating Starlark saga scripts, follow the comprehensive style guide at docs/guides/starlark-style-guide.md.
  Key points: Use `!=` not `is not`, always use `Decimal()` for money, call `step()` before handlers, avoid while loops.
  Check handlers.yaml schema for available service modules. Store in services/reference-data/saga/defaults/.
---

# Starlark Saga Script Generation

Generate type-safe Starlark saga scripts for Meridian's distributed transaction orchestration.

**Related:**

- **[Saga Contract Specification](../spec/saga-contract.md)** - Formal specification for how services transact together
- **[Starlark Saga Architecture](../architecture/starlark-saga-architecture.md)** - Component diagrams, data flow, dependency injection
- **[Starlark Style Guide](../guides/starlark-style-guide.md)** - Comprehensive syntax and conventions

---

## Quick Start

### Prerequisites

1. **Check handlers.yaml schema:**

   ```bash
   cat shared/pkg/saga/schema/handlers.yaml
   ```

   Verify that the service modules and handlers you need are registered.

2. **Review existing saga scripts:**

   ```bash
   ls services/reference-data/saga/defaults/*/
   ```

   Use similar scripts as templates (e.g., `withdrawal/v1.0.0.star`).

### Generation Steps

#### 1. Define Saga Requirements

Document:

- **Domain:** What business operation? (e.g., withdrawal, deposit, transfer)
- **Steps:** What service calls in what order?
- **Input:** What data does the saga need?
- **Output:** What should it return?
- **Compensation:** What happens on failure?

#### 2. Create File with Header

**File location:**

```text
services/reference-data/saga/defaults/<domain>/v1.0.0.star
```

**Template:**

```python
# Saga: <domain>_<operation>
# Version: 1.0.0
# Previous: none
# Changed: Initial implementation
# Author: <Your Name/Team>
# Date: <YYYY-MM-DD>
#
# Brief description of what this saga does.
#
# Steps (executed sequentially):
#   1. step_name: Description
#   2. step_name: Description
#
# Compensation Order (LIFO - Last In, First Out):
#   Compensation strategy description.
#
# Input data (provided via input_data dictionary):
#   - field_name: type - Description
```

#### 3. Define Saga and Execution Function

**Structure:**

```python
# Define the saga
<domain>_saga = saga(name="<saga_name>")

# Define the saga execution function
def execute_<domain>():
    # Extract input data
    field1 = input_data["required_field"]
    field2 = input_data.get("optional_field", "default")
    amount = Decimal(input_data["amount"])

    # Step 1: Description
    step(name="step_name")
    result1 = service_module.handler(
        param=value,
    )

    # Return result
    result = {
        "status": "COMPLETED",
        "field": result1.field,
    }
    return result

# Execute the saga
execute_<domain>()
```

#### 4. Critical Syntax Rules

**❌ NEVER use Python identity operators:**

```python
# WRONG - Causes syntax error
if value is not None:
    ...

# WRONG
if value is None:
    ...
```

**✅ ALWAYS use equality operators:**

```python
# CORRECT
if value != None:
    ...

# CORRECT
if value == None:
    ...
```

**✅ ALWAYS use Decimal for money:**

```python
# CORRECT
amount = Decimal(input_data["amount"])
total = amount * rate
```

**✅ ALWAYS call step() before handlers:**

```python
# CORRECT
step(name="log_position")
result = position_keeping.initiate_log(...)
```

#### 5. Test the Script

**Load in Go test:**

```go
// Read saga script
scriptPath := filepath.Join(repoRoot, "services", "reference-data", "saga", "defaults", "withdrawal", "v1.0.0.star")
scriptBytes, err := os.ReadFile(scriptPath)
require.NoError(t, err)
script := string(scriptBytes)

// Create saga runner
sagaRunner, err := saga.NewStarlarkSagaRunner(saga.StarlarkSagaRunnerConfig{
    Runtime:        runtime,
    Registry:       handlerRegistry,
    ServiceModules: serviceModules,
    Logger:         logger,
})
require.NoError(t, err)

// Execute saga
inputData := map[string]interface{}{
    "account_id": "ACC-001",
    "amount":     "100.50",
    // ... other fields
}

result, err := sagaRunner.Execute(ctx, script, inputData)
require.NoError(t, err)
```

---

## Available Service Modules

From `shared/pkg/saga/schema/handlers.yaml`:

### position_keeping

- `initiate_log` - Initiate a position log entry for a DEBIT or CREDIT transaction (compensate: `cancel_log`)
- `update_log` - Update an existing position log entry
- `cancel_log` - Cancel a position log entry (compensation handler)

### current_account

- `create_lien` - Create a lien (hold) on an account for a specified amount (compensate: `terminate_lien`)
- `execute_lien` - Execute (consume) a previously created lien
- `terminate_lien` - Terminate (release) a lien without execution (compensation handler)
- `save` - Persist current account metadata for a transaction
- `control` - Perform lifecycle control action on an account (FREEZE, UNFREEZE, CLOSE)

### financial_accounting

- `initiate_booking_log` - Initiate a booking log for a deposit or withdrawal transaction
- `update_booking_log` - Update the status of an existing booking log
- `capture_posting` - Capture a single-sided posting entry within a booking log (compensate: `compensate_posting`)
- `compensate_posting` - Compensate (reverse) a captured posting entry
- `create_booking` - Create a booking log entry for audit purposes
- `post_entries` - Post double-entry accounting entries to the ledger (compensate: `reverse_entries`)
- `reverse_entries` - Reverse previously posted accounting entries (compensation handler)

### financial_gateway (external)

- `dispatch_payment` - Dispatch a payment to an external provider (compensate: `cancel_payment`)
- `cancel_payment` - Cancel a pending payment dispatch (compensation handler)
- `dispatch_refund` - Dispatch a refund for a previously processed payment

### operational_gateway (external)

- `dispatch_instruction` - Queue an instruction for dispatch to an external provider (compensate: `cancel_instruction`)
- `cancel_instruction` - Cancel a pending instruction before dispatch (compensation handler)
- `get_instruction` - Get instruction status and details by ID

### reconciliation

- `initiate_run` - Initiate a new settlement reconciliation run (compensate: `cancel_run`)
- `execute_run` - Trigger execution of a pending settlement run
- `retrieve_run` - Retrieve a settlement run summary
- `cancel_run` - Cancel a settlement run (compensation handler)
- `assert_balance` - Evaluate a balance assertion against current positions
- `initiate_dispute` - Raise a formal dispute against a detected variance

### party

- `get_default_payment_method` - Retrieve the default payment method for a party
- `list_participants` - List active participants for a syndicate organization
- `get_structuring_data` - Retrieve structuring metadata for a participant in a syndicate

### internal_account

- `initiate` - Initiate a new internal account
- `retrieve` - Retrieve an internal account by ID
- `get_balance` - Query the current balance for an internal account

### market_information

- `get_rate` - Fetch FX rates for currency pair conversion

### reference_data

- `retrieve_instrument` - Retrieve an instrument definition by code and version

### notification

- `send` - Send a notification (email) to a party

**Always check `shared/pkg/saga/schema/handlers.yaml` for the latest available handlers and their compensation pairs.**

---

## Common Patterns

### Pattern 1: Simple Linear Saga

**Use case:** Straightforward sequence of operations.

```python
def execute_operation():
    # Extract input
    account_id = input_data["account_id"]
    amount = Decimal(input_data["amount"])

    # Step 1
    step(name="validate")
    validation = service1.validate(account_id=account_id)

    # Step 2
    step(name="process")
    result = service2.process(amount=amount)

    # Step 3
    step(name="finalize")
    final = service3.finalize(result_id=result.id)

    return {
        "status": "COMPLETED",
        "result_id": final.id,
    }
```

### Pattern 2: Conditional Steps

**Use case:** Optional operations based on input.

```python
def execute_operation():
    account_id = input_data["account_id"]
    amount = Decimal(input_data["amount"])
    clearing_account = input_data.get("clearing_account_id", "")

    # Step 1: Always execute
    step(name="debit_customer")
    debit = financial_accounting.capture_posting(
        account_id=account_id,
        amount=amount,
        direction="DEBIT",
    )

    # Step 2: Only if double-entry enabled
    if clearing_account != None and clearing_account.strip() != "":
        step(name="credit_clearing")
        credit = financial_accounting.capture_posting(
            account_id=clearing_account,
            amount=amount,
            direction="CREDIT",
        )

    return {"status": "COMPLETED"}
```

### Pattern 3: Iterating Over Collection

**Use case:** Process multiple items (bounded).

```python
def execute_operation():
    settlement_date = input_data["settlement_date"]

    # Fetch finite collection
    reads = market_information.get_meter_reads(
        from_date=settlement_date,
        to_date=settlement_date,
    )

    # Process each (bounded by settlement period)
    for read in reads:
        step(name=f"settle_meter_{read.id}")

        cost = position_keeping.calculate_cost(
            quantity=read.quantity,
            quality=read.quality,
            tariff_id=read.tariff_id,
        )

        financial_accounting.post_settlement(
            account_id=read.account_id,
            amount=cost,
            meter_read_id=read.id,
        )

    return {
        "status": "COMPLETED",
        "processed_count": len(reads),
    }
```

### Pattern 4: Accumulation with Early Exit

**Use case:** Accumulate until threshold.

```python
def execute_operation():
    target = Decimal(input_data["target_amount"])

    # Get finite collection
    items = reference_data.list_available_items()

    accumulated = Decimal("0")
    selected = []

    for item in items:
        if accumulated >= target:
            break  # Early exit

        selected.append(item.id)
        accumulated += item.amount

    # Reserve selected items
    for item_id in selected:
        step(name=f"reserve_{item_id}")
        position_keeping.create_lien(item_id=item_id)

    return {
        "status": "COMPLETED",
        "total": accumulated,
        "count": len(selected),
    }
```

---

## Syntax Checklist

Before committing, verify:

- [ ] No `is` or `is not` operators (use `==`, `!=`)
- [ ] No `while` loops (use `for` loops)
- [ ] No recursion (use iteration)
- [ ] All monetary amounts use `Decimal()`
- [ ] All service handlers exist in handlers.yaml
- [ ] `step()` called before each handler
- [ ] Optional fields checked before use (`!= None`, `!= ""`)
- [ ] Return dictionary with `status` field
- [ ] File header complete (version, author, date, docs)
- [ ] Inline comments explain WHY, not WHAT

---

## Testing Your Saga

### Unit Test Template

```go
func TestMySaga_Success(t *testing.T) {
    db, ctx, cleanup := setupIntegrationTestDB(t)
    defer cleanup()

    // Load saga script
    scriptPath := filepath.Join(repoRoot, "services", "reference-data", "saga", "defaults", "my-saga", "v1.0.0.star")
    scriptBytes, err := os.ReadFile(scriptPath)
    require.NoError(t, err)
    script := string(scriptBytes)

    // Create saga runner
    sagaRunner := createTestSagaRunner(t)

    // Execute saga
    inputData := map[string]interface{}{
        "account_id": "ACC-001",
        "amount":     "100.50",
        "currency":   "GBP",
    }

    result, err := sagaRunner.Execute(ctx, script, inputData)

    // Verify success
    require.NoError(t, err, "Saga should succeed")
    assert.Equal(t, "COMPLETED", result["status"])
}
```

### Common Test Failures

**Syntax Error:**

```text
script syntax error
my_saga.star:76:30: got is, want ':'
```

**Fix:** Replace `is not None` with `!= None`

**Handler Not Found:**

```text
undefined: my_service.my_handler
```

**Fix:** Check handlers.yaml for correct service module name

**Type Error:**

```text
cannot use string as Decimal
```

**Fix:** Wrap with `Decimal()`: `amount = Decimal(input_data["amount"])`

---

## Examples

### Minimal Saga

```python
# Saga: minimal_example
# Version: 1.0.0
# Previous: none
# Changed: Initial implementation
# Author: Platform Team
# Date: 2026-02-05
#
# Minimal example saga demonstrating basic structure.
#
# Steps:
#   1. process: Single processing step
#
# Input data:
#   - account_id: string - Account identifier

# Define saga
minimal_saga = saga(name="minimal_example")

# Execution function
def execute_minimal():
    account_id = input_data["account_id"]

    step(name="process")
    result = current_account.save(account_id=account_id)

    return {
        "status": "COMPLETED",
        "account_id": account_id,
    }

# Execute
execute_minimal()
```

### Real Example: Withdrawal

See `services/reference-data/saga/defaults/withdrawal/v1.0.0.star` for a production example with:

- Multiple steps with compensation
- Conditional logic (double-entry)
- Position keeping and financial accounting integration
- Proper error handling

---

## Troubleshooting

### Problem: "got is, want ':'"

**Cause:** Using Python `is` or `is not` operators.

**Fix:**

```python
# Before
if value is not None:
    ...

# After
if value != None:
    ...
```

### Problem: "undefined: service_name.handler_name"

**Cause:** Handler not registered in handlers.yaml.

**Fix:**

1. Check `shared/pkg/saga/schema/handlers.yaml`
2. Verify service module exists: `position_keeping`, `financial_accounting`, etc.
3. Verify handler exists under that module

### Problem: Type mismatch on Decimal

**Cause:** Passing string where Decimal expected.

**Fix:**

```python
# Before
amount = input_data["amount"]  # String

# After
amount = Decimal(input_data["amount"])  # Decimal
```

### Problem: Saga never completes

**Cause:** Missing return statement or wrong return format.

**Fix:**

```python
# Ensure you return a dictionary
return {
    "status": "COMPLETED",
    "transaction_id": transaction_id,
}
```

---

## Best Practices

1. **Start with a template** - Copy existing saga script
2. **Check handlers.yaml first** - Verify service modules exist
3. **Test incrementally** - Add one step at a time
4. **Use descriptive step names** - `log_position`, not `step1`
5. **Document each step** - Inline comments for clarity
6. **Fail fast** - Validate inputs at the start
7. **Use Decimal for money** - Always, no exceptions
8. **Check optional fields** - `!= None` before using
9. **Follow naming conventions** - snake_case everywhere
10. **Read the style guide** - [`docs/guides/starlark-style-guide.md`](../guides/starlark-style-guide.md)

---

## Further Reading

- **[Starlark Style Guide](../guides/starlark-style-guide.md)** - Comprehensive syntax and conventions
- **[Saga Service Catalog](../saga-service-catalog.md)** - Service module documentation
- **[handlers.yaml](../saga-handlers.schema.json)** - Available handlers schema
- **[ADR-0028: Starlark Saga & CEL Valuation](../adr/0028-starlark-saga-cel-valuation.md)** - Architecture decision
