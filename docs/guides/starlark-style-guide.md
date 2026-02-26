# Starlark Style Guide for Meridian

**Purpose:** Comprehensive style guide for writing Starlark saga scripts in Meridian.

**Audience:** Developers and AI assistants generating Starlark code.

---

## Table of Contents

1. [Critical Syntax Differences from Python](#critical-syntax-differences-from-python)
2. [File Structure and Organisation](#file-structure-and-organisation)
3. [Naming Conventions](#naming-conventions)
4. [Documentation Standards](#documentation-standards)
5. [Type Safety and Validation](#type-safety-and-validation)
6. [Error Handling](#error-handling)
7. [Testing Considerations](#testing-considerations)
8. [Common Pitfalls](#common-pitfalls)
9. [Meridian-Specific Patterns](#meridian-specific-patterns)

---

## Critical Syntax Differences from Python

### Identity vs Equality Operators

**❌ WRONG - Python syntax:**

```python
if clearing_account_id is not None:
    process(clearing_account_id)

if value is None:
    return default
```

**✅ CORRECT - Starlark syntax:**

```python
if clearing_account_id != None:
    process(clearing_account_id)

if value == None:
    return default
```

**Why:** Starlark doesn't support Python's identity operators (`is`, `is not`). Always use equality operators (`==`, `!=`).

**Compiler error you'll see:**

```text
script syntax error
filename.star:76:30: got is, want ':'
```

### Empty String Checks

**✅ GOOD:**

```python
# Explicit comparison (preferred)
if clearing_account_id != None and clearing_account_id.strip() != "":
    use_clearing_account(clearing_account_id)

# Alternative: Use .get() with default
clearing_account_id = input_data.get("clearing_account_id", "")
if clearing_account_id != "":
    use_clearing_account(clearing_account_id)
```

**⚠️ AVOID:**

```python
# Pythonic truthiness - works but less explicit
if clearing_account_id:
    use_clearing_account(clearing_account_id)
```

### No While Loops or Recursion

**❌ FORBIDDEN:**

```python
# While loops cause compilation error
while condition:
    do_something()

# Recursion is not allowed
def factorial(n):
    if n <= 1:
        return 1
    return n * factorial(n - 1)
```

**✅ USE INSTEAD:**

```python
# Bounded iteration with for loops
for attempt in range(max_retries):
    if try_operation():
        break  # Early exit on success
```

### String Formatting

**✅ GOOD:**

```python
# Use f-strings (Starlark supports them)
message = f"Processing account {account_id} with amount {amount}"

# String concatenation
log_name = "settle_meter_" + read.id
```

**⚠️ LIMITED:**

```python
# .format() has limited support - prefer f-strings
message = "Processing {}".format(account_id)
```

### List Comprehensions

**✅ SUPPORTED:**

```python
# List comprehensions work
amounts = [txn.amount for txn in transactions]
filtered = [x for x in items if x.active]
```

**❌ NOT SUPPORTED:**

```python
# Dictionary comprehensions don't work
mapping = {k: v for k, v in pairs}  # Use dict() constructor instead
```

### Import Statements

**❌ WRONG:**

```python
import json
from decimal import Decimal
```

**✅ CORRECT:**

```python
# No imports needed - built-ins are available
# Decimal is provided by Meridian runtime
amount = Decimal("100.50")
```

---

## File Structure and Organisation

### File Naming

**Convention:**

```text
services/reference-data/saga/defaults/<domain>/<version>.star
```

**Examples:**

- `services/reference-data/saga/defaults/withdrawal/v1.0.0.star`
- `services/reference-data/saga/defaults/deposit/v1.0.0.star`
- `services/reference-data/saga/defaults/transfer/v1.0.0.star`

**Rules:**

- Use lowercase with underscores for domain names
- Use semantic versioning: `v{major}.{minor}.{patch}.star`
- Store in `reference-data` service (canonical location)
- One saga per file

### File Header Template

**Required header format:**

```python
# Saga: <saga_name>
# Version: <version>
# Previous: <previous_version_or_none>
# Changed: <summary_of_changes>
# Author: <author_or_team>
# Date: <YYYY-MM-DD>
#
# Brief description of what this saga does.
#
# Steps (executed sequentially):
#   1. step_name: Description
#   2. step_name: Description
#   ...
#
# Compensation Order (LIFO - Last In, First Out):
#   Description of compensation strategy.
#
# Input data (provided via input_data dictionary):
#   - field_name: type - Description
#   - field_name: type - Description
#   ...
```

**Example:**

```python
# Saga: current_account_withdrawal
# Version: 1.0.0
# Previous: none
# Changed: Migrated from invoke_handler() to typed service modules
# Author: Platform Team
# Date: 2026-01-27
#
# This Starlark script defines the withdrawal saga workflow for the Current Account service.
# The saga executes a multi-step withdrawal operation with compensation on failure.
#
# Steps (executed sequentially):
#   1. log_position: Create DEBIT entry in PositionKeeping service
#   2. initiate_booking_log: Create booking log in FinancialAccounting service
#   3. capture_debit_posting: Post DEBIT to customer account
#   4. capture_credit_posting: Post CREDIT to clearing account (double-entry)
#   5. finalize_booking_log: Transition booking log to POSTED
#   6. save_account: Persist account metadata
#
# Compensation Order (LIFO - Last In, First Out):
#   Failures trigger compensation of completed steps in reverse order.
#   Compensation handlers are declared in handlers.yaml schema.
#
# Input data (provided via input_data dictionary):
#   - account_id: string - Account identifier
#   - account_identification: string - Account identification for external services
#   - amount: string - Decimal amount as string (e.g., "100.50")
#   - currency: string - Currency code (e.g., "GBP")
#   - transaction_id: string - Unique transaction identifier
#   - clearing_account_id: string - Clearing account for double-entry (optional)
```

### File Structure Template

```python
# [Header - see above]

# Define the saga
<domain>_saga = saga(name="<saga_name>")

# Define the saga execution function
def execute_<domain>():
    # Extract input data
    field1 = input_data["required_field"]
    field2 = input_data.get("optional_field", "default")

    # Convert strings to Decimals
    amount = Decimal(input_data["amount"])

    # Step 1: Description
    step(name="step_name")
    result1 = service_module.handler(
        param1=field1,
        param2=amount,
    )

    # Step 2: Conditional logic
    if condition:
        step(name="conditional_step")
        result2 = service_module.handler(...)

    # Return result dictionary
    result = {
        "status": "COMPLETED",
        "field1": result1.field,
        "field2": result2.field,
    }
    return result

# Execute the saga
execute_<domain>()
```

---

## Naming Conventions

### Variables

**Use snake_case:**

```python
# ✅ GOOD
account_id = input_data["account_id"]
transaction_id = input_data["transaction_id"]
booking_log_result = financial_accounting.initiate_booking_log(...)
clearing_account_id = input_data.get("clearing_account_id", "")
```

**❌ AVOID:**

```python
accountId = input_data["accountId"]  # camelCase
AccountID = input_data["AccountID"]  # PascalCase
```

### Functions

**Use snake_case with verb prefix:**

```python
# ✅ GOOD
def execute_withdrawal():
    ...

def process_settlement():
    ...

def calculate_total_cost():
    ...
```

### Step Names

**Use snake_case, descriptive, action-oriented:**

```python
# ✅ GOOD
step(name="log_position")
step(name="initiate_booking_log")
step(name="capture_debit_posting")
step(name=f"settle_meter_{read.id}")  # Dynamic with ID

# ❌ AVOID
step(name="step1")  # Not descriptive
step(name="doStuff")  # camelCase
step(name="CAPTURE")  # SCREAMING_CASE
```

### Input Data Keys

**Use snake_case (matches protobuf conventions):**

```python
# ✅ GOOD
account_id = input_data["account_id"]
transaction_id = input_data["transaction_id"]
clearing_account_id = input_data.get("clearing_account_id", "")
```

### Service Module Calls

**Use snake_case for handler names:**

```python
# ✅ GOOD - Follows handlers.yaml schema
position_keeping.initiate_log(...)
financial_accounting.capture_posting(...)
current_account.save(...)

# ❌ WRONG - Doesn't match schema
positionKeeping.initiateLog(...)  # camelCase not in schema
```

---

## Documentation Standards

### Inline Comments

**Use comments to explain WHY, not WHAT:**

**✅ GOOD:**

```python
# Step 4: Capture CREDIT posting to clearing account (if double-entry enabled)
if clearing_account_id != None and clearing_account_id.strip() != "":
    step(name="capture_credit_posting")
    credit_result = financial_accounting.capture_posting(...)
```

**❌ AVOID:**

```python
# Check if clearing account ID is not None
if clearing_account_id != None:
    # Call capture posting
    credit_result = financial_accounting.capture_posting(...)
```

### Step Documentation

**Document each step with a comment:**

```python
# Step 1: Log position in PositionKeeping service with DEBIT direction
step(name="log_position")
log_position_result = position_keeping.initiate_log(...)

# Step 2: Initiate booking log in FinancialAccounting service
step(name="initiate_booking_log")
booking_log_result = financial_accounting.initiate_booking_log(...)
```

### Complex Logic Documentation

**Document non-obvious decisions:**

```python
# The mock returns the POST-withdrawal balance since Position Keeping is the source of truth
# and would have already recorded the DEBIT by the time we query the balance.
# Pre-withdrawal: $1000.00, Withdrawal: $100.50, Post-withdrawal: $899.50 (89950 cents)
mockPosKeeping := &mockPositionKeepingClient{
    accountBalances: map[string]int64{
        "ACC-WTH-001": 89950, // $899.50 post-withdrawal
    },
}
```

---

## Type Safety and Validation

### Using Decimal for Money

**❌ WRONG:**

```python
# Floats lose precision
amount = 100.50
total = amount * 1.1  # Floating point errors

# Integers require conversion
amount_cents = 10050
amount_pounds = amount_cents / 100  # Float division
```

**✅ CORRECT:**

```python
# Always use Decimal for money
amount = Decimal("100.50")
rate = Decimal("1.1")
total = amount * rate

# Convert strings to Decimal
amount = Decimal(input_data["amount"])
```

### Extracting Input Data

**Validate and extract input defensively:**

```python
# ✅ GOOD - Required field
account_id = input_data["account_id"]  # Fails if missing (desired)

# ✅ GOOD - Optional field with default
clearing_account_id = input_data.get("clearing_account_id", "")

# ✅ GOOD - Optional field with None check
clearing_account_id = input_data.get("clearing_account_id", None)
if clearing_account_id != None:
    use_clearing_account(clearing_account_id)

# ❌ AVOID - Silent failures
clearing_account_id = input_data.get("clearing_account_id")  # Returns None, might cause issues later
```

### Service Module Parameter Types

**Follow handlers.yaml schema exactly:**

```python
# ✅ CORRECT - Matches schema types
position_keeping.initiate_log(
    position_id=account_identification,  # string
    amount=amount,                       # Decimal
    currency=currency,                   # string
    direction="DEBIT",                   # enum string
    transaction_id=transaction_id,       # string
)

# ❌ WRONG - Type mismatch
position_keeping.initiate_log(
    amount="100.50",  # Should be Decimal, not string
    direction=0,      # Should be "DEBIT", not integer
)
```

---

## Error Handling

### Using fail()

**Fail fast with descriptive messages:**

```python
# ✅ GOOD - Descriptive error
if amount <= Decimal("0"):
    fail(f"Amount must be positive, got: {amount}")

# ✅ GOOD - Context included
if account_status == "FROZEN":
    fail(f"Cannot withdraw from frozen account: {account_id}")

# ❌ AVOID - Generic errors
if not valid:
    fail("Invalid")  # What's invalid?
```

### Early Validation

**Validate inputs at the start:**

```python
def execute_withdrawal():
    # Extract and validate inputs first
    account_id = input_data["account_id"]
    amount = Decimal(input_data["amount"])

    # Validate business rules
    if amount <= Decimal("0"):
        fail(f"Withdrawal amount must be positive: {amount}")

    if amount > Decimal("1000000"):
        fail(f"Withdrawal amount exceeds maximum: {amount}")

    # Now proceed with saga steps
    step(name="log_position")
    ...
```

### No Try/Except

**Starlark doesn't support exceptions:**

```python
# ❌ NOT AVAILABLE
try:
    result = api_call()
except Exception as e:
    handle_error(e)

# ✅ USE INSTEAD
# Saga runtime handles errors and triggers compensation
result = api_call()  # Failure triggers automatic compensation
```

---

## Testing Considerations

### Test Saga Scripts in Go Tests

**Load saga scripts from reference-data:**

```go
// services/current-account/service/withdrawal_test.go
func testWithdrawalOrchestrator(repo *persistence.Repository, ...) *WithdrawalOrchestrator {
    // Load withdrawal saga script from reference-data canonical source
    _, filename, _, ok := runtime.Caller(0)
    if !ok {
        panic("failed to get current file path")
    }
    serviceDir := filepath.Dir(filename)
    repoRoot := filepath.Join(serviceDir, "..", "..", "..")
    withdrawalScriptPath := filepath.Join(repoRoot, "services", "reference-data", "saga", "defaults", "withdrawal", "v1.0.0.star")
    withdrawalScriptBytes, err := os.ReadFile(withdrawalScriptPath)
    if err != nil {
        panic("failed to read withdrawal script: " + err.Error())
    }
    withdrawalScript := string(withdrawalScriptBytes)

    // Create saga runner with script
    sagaRunner, err := saga.NewStarlarkSagaRunner(...)
    ...
}
```

### Test Input Data

**Provide all required fields:**

```go
inputData := map[string]interface{}{
    "account_id":              "ACC-001",
    "account_identification":  "ACC-001-IDENT",
    "amount":                  "100.50",  // String, converted to Decimal in script
    "currency":                "GBP",
    "transaction_id":          uuid.New().String(),
    "clearing_account_id":     "CLEARING-001",  // Optional but included
}
```

### Common Test Failures

**Syntax errors break tests:**

```text
script syntax error
current_account_withdrawal.star:76:30: got is, want ':'
```

**Fix:** Replace `is not None` with `!= None`

---

## Common Pitfalls

### 1. Using Python Identity Operators

**Problem:**

```python
if value is not None:  # ❌ Syntax error
```

**Solution:**

```python
if value != None:  # ✅ Works
```

### 2. Forgetting to Convert Amount to Decimal

**Problem:**

```python
amount = input_data["amount"]  # ❌ String, not Decimal
total = amount * rate  # Type error
```

**Solution:**

```python
amount = Decimal(input_data["amount"])  # ✅ Decimal
total = amount * rate  # Works
```

### 3. Using Undefined Service Modules

**Problem:**

```python
result = my_service.my_handler(...)  # ❌ Not in handlers.yaml
```

**Solution:**

```python
# Check shared/pkg/saga/schema/handlers.yaml
# Use only registered handlers:
result = position_keeping.initiate_log(...)  # ✅ Defined in schema
```

### 4. Not Checking Optional Fields

**Problem:**

```python
clearing_account_id = input_data.get("clearing_account_id")
credit_result = financial_accounting.capture_posting(
    account_id=clearing_account_id,  # ❌ Might be None
    ...
)
```

**Solution:**

```python
clearing_account_id = input_data.get("clearing_account_id", "")
if clearing_account_id != "":  # ✅ Check before using
    credit_result = financial_accounting.capture_posting(...)
```

### 5. Infinite Loops (Caught at Compile Time)

**Problem:**

```python
while True:  # ❌ Syntax error - while loops forbidden
    do_something()
```

**Solution:**

```python
for attempt in range(max_attempts):  # ✅ Bounded iteration
    if do_something():
        break
```

### 6. Missing step() Calls

**Problem:**

```python
# No step() call before handler
result = position_keeping.initiate_log(...)  # ❌ No compensation tracking
```

**Solution:**

```python
step(name="log_position")  # ✅ Required for compensation
result = position_keeping.initiate_log(...)
```

### 7. Incorrect Return Value

**Problem:**

```python
def execute_withdrawal():
    ...
    # Missing return statement
```

**Solution:**

```python
def execute_withdrawal():
    ...
    result = {
        "status": "COMPLETED",
        "transaction_id": transaction_id,
    }
    return result  # ✅ Required
```

---

## Meridian-Specific Patterns

### Saga Definition

**Always define saga at module level:**

```python
# Define the saga (required)
withdrawal_saga = saga(name="current_account_withdrawal")

# Define execution function
def execute_withdrawal():
    ...

# Execute (required)
execute_withdrawal()
```

### Step Declaration

**Call step() before each service module operation:**

```python
step(name="log_position")
result = position_keeping.initiate_log(...)
```

**Why:** Enables compensation tracking. If this step fails, previous steps are compensated in LIFO order.

### Input Data Access

**Use input_data dictionary:**

```python
# input_data is provided by saga runtime
account_id = input_data["account_id"]
amount = Decimal(input_data["amount"])
```

### Service Modules

**Available service modules (from handlers.yaml):**

- `position_keeping` - Position logs, balances
- `financial_accounting` - Booking logs, postings
- `current_account` - Account operations
- `reference_data` - Instruments, validation
- `party` - Party information
- `market_information` - Market data, settlement

**Check `shared/pkg/saga/schema/handlers.yaml` for available handlers.**

### Conditional Steps

**Only call step() for paths that execute:**

```python
# ✅ CORRECT - step() inside conditional
if clearing_account_id != None and clearing_account_id.strip() != "":
    step(name="capture_credit_posting")
    credit_result = financial_accounting.capture_posting(...)

# ❌ WRONG - step() called unconditionally
step(name="capture_credit_posting")
if clearing_account_id != None:
    credit_result = financial_accounting.capture_posting(...)
```

### Result Format

**Return a dictionary with status:**

```python
result = {
    "status": "COMPLETED",
    "transaction_id": transaction_id,
    "log_id": log_position_result.log_id,
    "booking_log_id": booking_log_result.booking_log_id,
}
return result
```

---

## Quick Reference

### Syntax Gotchas

| Python | Starlark | Why |
|--------|----------|-----|
| `is`, `is not` | `==`, `!=` | Identity operators not supported |
| `while` loops | `for` loops | Guarantees termination |
| `def factorial(n): return n * factorial(n-1)` | Iterative solution | No recursion |
| `try/except` | No equivalent | Saga runtime handles errors |
| `import` statements | No imports | Built-ins provided by runtime |
| `100.50` (float) | `Decimal("100.50")` | Precision for money |

### File Checklist

Before committing a Starlark saga script:

- [ ] File header with version, author, date
- [ ] Steps documented in header
- [ ] Compensation strategy documented
- [ ] Input data documented
- [ ] No `is` or `is not` operators (use `==`, `!=`)
- [ ] No `while` loops (use `for` loops)
- [ ] No recursion (use iteration)
- [ ] All amounts use `Decimal()`
- [ ] `step()` called before each handler
- [ ] Optional fields checked before use
- [ ] Return dictionary with status
- [ ] Tests pass in Go test suite

### Testing Checklist

- [ ] Script loads without syntax errors
- [ ] All input_data fields provided
- [ ] Service module handlers exist in handlers.yaml
- [ ] Compensation triggers on failure
- [ ] Result matches expected format

---

## Validation

All saga scripts are validated before deployment. Run validation locally to catch errors early:

```bash
# Validate before committing
meridian-cli saga validate my_saga.star

# JSON output for CI integration
meridian-cli saga validate --json my_saga.star
```

### What Validation Catches

```python
# SYNTAX - Parse errors caught immediately
if value is not None:  # "got is, want ':'" error
    process(value)

# UNDEFINED_HANDLER - Unknown handler references
result = position_keeping.initiate_logg(...)  # Typo: "logg" vs "log"

# TYPE_MISMATCH - Wrong parameter types
position_keeping.initiate_log(
    amount="100.50",  # Should be Decimal("100.50")
)

# RUNTIME - Script logic errors
fail("Amount must be positive")  # Caught during dry-run
```

### Passing Validation

Scripts that follow this style guide will pass validation. The key rules:

1. Use `==` / `!=` instead of `is` / `is not`
2. Use `Decimal()` for all monetary amounts
3. Only call handlers defined in `handlers.yaml`
4. Match parameter types to the schema exactly
5. Keep complexity score below 7 (check with `--json` output)

For detailed validation documentation, see the [Saga Validation Guide](saga-validation.md).

---

## Further Reading

- **[Saga Validation Guide](saga-validation.md)** - Validation workflow, error interpretation, and monitoring
- **[Saga Handlers Schema](../saga-handlers.schema.json)** - Available service modules and handlers
- **[Saga Service Catalogue](../saga-service-catalogue.md)** - Service module documentation
- **[ADR-0028: Starlark Saga & CEL Valuation](../adr/0028-starlark-saga-cel-valuation.md)** - Architecture decision
