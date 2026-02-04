# Starlark Patterns: Common Workflows Without While Loops

**Purpose:** Guide for implementing common business logic patterns using Starlark's bounded expressiveness.

**Key Principle:** Starlark prohibits `while` loops and recursion to guarantee termination.
This document shows how to achieve the same goals using finite iteration.

---

## Why No While Loops?

**The Problem:

```python
# This could run forever
while not success:
    success = try_operation()
```

**The Solution:

```python
# This runs at most 5 times
for attempt in range(5):
    if try_operation():
        break  # Exit early on success
```

**Why it matters:** In multi-tenant systems processing millions of transactions, one infinite loop
can crash the entire platform. Bounded iteration guarantees maximum execution time.

---

## Pattern 1: Retry Logic with Backoff

### ❌ Turing-Complete (Forbidden)

```python
# WRONG - while loop could run forever
attempt = 0
while attempt < max_retries:
    try:
        result = api_call()
        break  # Success
    except Exception:
        attempt += 1
        time.sleep(2 ** attempt)  # Exponential backoff
```

### ✅ Bounded (Starlark)

```python
# RIGHT - finite retries guaranteed
max_retries = 5
for attempt in range(max_retries):
    result = api_call()
    if result.success:
        return result

    # Exponential backoff (but bounded)
    wait_seconds = 2 ** attempt  # 1, 2, 4, 8, 16 seconds
    # Note: time.sleep not available in Starlark
    # Platform handles delays via saga step scheduling

# If we get here, all retries failed
fail("Max retries exceeded")
```

**Platform Integration:

```python
# In saga orchestration
def payment_with_retry():
    step(name="attempt_payment", max_retries=5, backoff="exponential")
    result = payment_gateway.charge(
        customer_id=input_data["customer_id"],
        amount=Decimal(input_data["amount"])
    )
    return result
```

---

## Pattern 2: Processing Unknown-Sized Collections

### ❌ Turing-Complete (Forbidden)

```python
# WRONG - while loop processes indefinite stream
while has_more_data():
    item = fetch_next()
    process(item)
```

### ✅ Bounded (Starlark)

```python
# RIGHT - fetch finite collection first
items = fetch_all_items()  # Returns finite list
for item in items:
    process(item)

# Alternative: Batch processing with known size
batch_size = 1000
for batch_index in range(total_batches):
    items = fetch_batch(batch_index, batch_size)
    for item in items:
        process(item)
```

### Real Example: Energy Settlement

```python
def settle_meter_reads():
    # Fetch all reads for settlement period (finite)
    reads = market_information.get_meter_reads(
        gsp_id=input_data["gsp_id"],
        from_date=input_data["settlement_date"],
        to_date=input_data["settlement_date"]
    )

    # Process each read (bounded by settlement period)
    for read in reads:
        step(name=f"settle_meter_{read.id}")

        # Calculate cost based on read quality
        cost = position_keeping.calculate_cost(
            quantity=read.quantity,
            quality=read.quality,  # ESTIMATE, COEFFICIENT, ACTUAL
            tariff_id=read.tariff_id
        )

        # Post to ledger
        financial_accounting.post_settlement(
            account_id=read.account_id,
            amount=cost,
            meter_read_id=read.id
        )
```

---

## Pattern 3: Conditional Accumulation

### ❌ Turing-Complete (Forbidden)

```python
# WRONG - while loop with condition
total = 0
while total < threshold:
    amount = next_transaction()
    total += amount
```

### ✅ Bounded (Starlark)

```python
# RIGHT - iterate over known collection
transactions = get_pending_transactions()
total = Decimal("0")

for txn in transactions:
    total += txn.amount

    if total >= threshold:
        # Early exit when threshold reached
        break

return {"total": total, "count": len(transactions)}
```

### Real Example: Carbon Credit Purchase

```python
def aggregate_carbon_credits():
    # Get all available credits (finite)
    available_credits = reference_data.list_carbon_credits(
        registry="VERRA",
        status="AVAILABLE"
    )

    target_tonnes = Decimal(input_data["target_tonnes"])
    accumulated = Decimal("0")
    selected_credits = []

    # Accumulate until target reached (bounded by available credits)
    for credit in available_credits:
        if accumulated >= target_tonnes:
            break

        selected_credits.append(credit.id)
        accumulated += credit.tonnes_co2e

    # Reserve selected credits
    for credit_id in selected_credits:
        step(name=f"reserve_{credit_id}")
        position_keeping.create_lien(
            credit_id=credit_id,
            buyer_id=input_data["buyer_id"]
        )

    return {
        "total_tonnes": accumulated,
        "credits_reserved": len(selected_credits)
    }
```

---

## Pattern 4: State Machine Traversal

### ❌ Turing-Complete (Forbidden)

```python
# WRONG - while loop for state machine
state = "PENDING"
while state != "COMPLETED":
    state = transition(state)
```

### ✅ Bounded (Starlark)

```python
# RIGHT - explicit state transitions (max depth)
state = "PENDING"
max_transitions = 10

for _ in range(max_transitions):
    if state == "COMPLETED":
        break

    # Explicit state transitions
    if state == "PENDING":
        state = "VALIDATING"
    elif state == "VALIDATING":
        state = "APPROVED" if validate() else "REJECTED"
    elif state == "APPROVED":
        state = "EXECUTING"
    elif state == "EXECUTING":
        state = "COMPLETED" if execute() else "FAILED"
    elif state == "FAILED":
        state = "RETRYING"
    elif state == "RETRYING":
        state = "EXECUTING"
    else:
        fail(f"Unknown state: {state}")

if state != "COMPLETED":
    fail("Max state transitions exceeded")
```

### Better Alternative: Saga Steps

```python
# BEST - Let saga orchestrator handle state machine
def payment_workflow():
    # Each step is explicit state transition
    step(name="validate")
    validation = validate_payment_request(input_data)

    step(name="approve")
    approval = approve_payment(validation.request_id)

    step(name="execute")
    result = execute_payment(approval.payment_id)

    step(name="confirm")
    confirmation = confirm_payment(result.transaction_id)

    return confirmation
```

---

## Pattern 5: Tree/Graph Traversal

### ❌ Turing-Complete (Forbidden)

```python
# WRONG - recursive tree traversal
def traverse(node):
    process(node)
    if node.left:
        traverse(node.left)
    if node.right:
        traverse(node.right)
```

### ✅ Bounded (Starlark)

```python
# RIGHT - iterative traversal with explicit queue
def traverse_tree(root):
    queue = [root]
    processed = []

    # Bounded by tree size (finite nodes)
    max_nodes = 10000  # Safety limit
    for _ in range(max_nodes):
        if len(queue) == 0:
            break

        node = queue.pop(0)  # BFS
        processed.append(node.id)

        # Add children to queue
        children = get_children(node.id)
        for child in children:
            queue.append(child)

    if len(queue) > 0:
        fail("Tree too large (exceeded 10,000 nodes)")

    return processed
```

### Real Example: Organizational Hierarchy

```python
def calculate_department_budget():
    root_dept = reference_data.get_department(input_data["dept_id"])

    departments = [root_dept]
    total_budget = Decimal("0")

    # Traverse organization tree (bounded by company size)
    for dept in departments:
        total_budget += dept.allocated_budget

        # Add sub-departments
        sub_depts = reference_data.get_sub_departments(dept.id)
        for sub in sub_depts:
            departments.append(sub)

    return {"total_budget": total_budget}
```

---

## Pattern 6: Polling with Timeout

### ❌ Turing-Complete (Forbidden)

```python
# WRONG - while loop with timeout
start = time.now()
while time.now() - start < timeout:
    if check_condition():
        return success()
    time.sleep(1)
fail("Timeout")
```

### ✅ Bounded (Starlark)

```python
# RIGHT - finite polling attempts
max_polls = 30  # 30 attempts
poll_interval_seconds = 1

for attempt in range(max_polls):
    step(name=f"poll_attempt_{attempt}")

    status = check_external_system()
    if status == "COMPLETED":
        return success()

    # Platform scheduler handles delay between steps
    # (Starlark doesn't have time.sleep)

fail("Polling timeout after 30 attempts")
```

**Platform Integration:

```python
# Saga orchestration handles polling delay
def wait_for_settlement():
    step(name="poll_settlement", poll_interval_seconds=5, max_attempts=60)
    result = market_information.get_settlement_status(
        settlement_id=input_data["settlement_id"]
    )

    if result.status != "SETTLED":
        fail("Settlement not complete")

    return result
```

---

## Pattern 7: Dynamic Collection Building

### ❌ Turing-Complete (Forbidden)

```python
# WRONG - while loop building collection
results = []
while should_continue():
    item = fetch_next()
    if meets_criteria(item):
        results.append(item)
```

### ✅ Bounded (Starlark)

```python
# RIGHT - filter finite collection
all_items = fetch_all()  # Finite collection
results = [item for item in all_items if meets_criteria(item)]

# Alternative: Explicit loop with criteria
results = []
for item in all_items:
    if meets_criteria(item):
        results.append(item)
```

### Real Example: Account Reconciliation

```python
def reconcile_accounts():
    # Get all accounts for tenant (finite)
    accounts = position_keeping.list_accounts(
        tenant_id=input_data["tenant_id"]
    )

    # Find accounts needing reconciliation
    unreconciled = []
    for account in accounts:
        ledger_balance = financial_accounting.get_balance(account.id)
        position_balance = position_keeping.get_balance(account.id)

        if ledger_balance != position_balance:
            unreconciled.append({
                "account_id": account.id,
                "ledger": ledger_balance,
                "position": position_balance,
                "difference": ledger_balance - position_balance
            })

    # Reconcile each discrepancy
    for discrepancy in unreconciled:
        step(name=f"reconcile_{discrepancy['account_id']}")
        financial_accounting.post_reconciliation(
            account_id=discrepancy["account_id"],
            amount=discrepancy["difference"],
            reason="POSITION_LEDGER_MISMATCH"
        )

    return {"reconciled_count": len(unreconciled)}
```

---

## When Bounded Languages Feel Limiting

### Symptom: "I need a while loop for this!"

**Question to ask:** "What's the maximum iterations needed in practice?"

- Payment retries? Max 5-10 attempts
- Tree traversal? Bounded by org size (< 10,000 nodes)
- Batch processing? Known batch size
- State machine? Finite states (< 20 transitions)

**If you genuinely can't bound it:

1. **Challenge the requirement** - Does the business logic truly need unbounded iteration?
2. **Move to platform** - Implement as first-class feature in Go (we control execution)
3. **Use saga pagination** - Break into smaller bounded chunks

### Example: "Process all customer records" (millions)

```python
# ❌ WRONG - Can't iterate millions in one saga
customers = fetch_all_customers()  # 10M records!
for customer in customers:
    process(customer)  # This would take hours

# ✅ RIGHT - Batch processing with continuation
batch_size = 1000
batch_index = int(input_data.get("batch_index", "0"))

customers = fetch_customer_batch(batch_index, batch_size)
for customer in customers:
    process(customer)

# If more batches exist, trigger next batch
if len(customers) == batch_size:
    # Platform will schedule next batch
    trigger_next_batch(batch_index + 1)
```

---

## Summary: Bounded is Better

**What you lose:

- `while` loops (use `for` with known range)
- Recursion (use iterative with explicit stack)
- Unbounded iteration (batch instead)

**What you gain:

- **Mathematical guarantee** of termination
- **Predictable costs** (execution time = f(input size))
- **No DoS vulnerabilities** (compiler enforces)
- **Deterministic testing** (no flaky timeouts)
- **Multi-tenant safety** (no resource starvation)

**The Trade:

- 98% of business logic is naturally bounded
- 2% that seems unbounded can be refactored to bounded patterns
- 0% needs true Turing-completeness in production financial systems

**Remember:** SQL is also not Turing-complete, yet powers the world's databases.
Bounded expressiveness is a feature, not a bug.
