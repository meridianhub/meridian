---
name: adr-029-bounded-languages-termination-guarantees
description: Use non-Turing-complete languages (Starlark/CEL) for guaranteed termination and DoS prevention
triggers:
  - Implementing tenant-configurable business logic
  - Preventing runaway scripts in multi-tenant environments
  - Guaranteeing predictable execution costs
  - Evaluating scripting language choices for platform extensibility
instructions: |
  NEVER allow Turing-complete languages for tenant scripts. Use Starlark (no while/recursion)
  for workflows and CEL (expression-only) for validation. This guarantees every tenant script
  terminates in bounded time, preventing DoS attacks and enabling predictable billing.
  Bounded expressiveness is a feature, not a limitation.
---

# 29. Bounded Languages for Termination Guarantees

Date: 2025-02-03

## Status

Accepted

## Context

Multi-tenant platforms face a critical security challenge: how to give tenants programmability without allowing them to crash the platform or starve other tenants' resources.

### The Halting Problem in Production

If tenants can write arbitrary code in Turing-complete languages (Python, JavaScript, Go), they can:

```python
# DoS Attack - Infinite loop
while True:
    pass

# Memory Bomb - Unbounded recursion
def bomb(n):
    return bomb(n + 1)

# CPU Exhaustion - Exponential complexity
def fib(n):
    return fib(n-1) + fib(n-2)  # O(2^n) without memoization
```

**Traditional solutions all have problems:**

| Approach | Problem |
|----------|---------|
| **Runtime timeouts** | Requires polling/preemption, adds overhead, timeouts are arbitrary |
| **Resource quotas** | Can't distinguish legitimate long operations from attacks |
| **Sandboxing** | Expensive (containers per tenant), still allows resource exhaustion |
| **Code review** | Doesn't scale, can't catch all issues |

### Real-World Impact

When Meridian processes millions of transactions/day for multiple tenants:
- One runaway script can starve CPU for all tenants
- Non-deterministic execution times make billing unpredictable
- Debugging "why did this timeout?" is impossible with Turing-complete code

**Example from energy settlement:**
```python
# Tenant writes seemingly innocent code
for meter_read in get_meter_reads():  # Could be 10M records
    if complex_validation(meter_read):  # Could take 100ms each
        process(meter_read)
# Total: 10M * 100ms = 11.5 days execution time
```

With Turing-complete languages, we can't know if this will finish in 1 second or 1 week.

## Decision Drivers

* **Security**: Prevent DoS attacks via infinite loops
* **Predictability**: Guarantee maximum execution time for billing/SLAs
* **Multi-tenancy**: Isolate tenants without expensive sandboxing
* **Determinism**: Same input must produce same output in same time
* **AI Generation**: LLMs can reliably generate code with bounded semantics

## Considered Options

1. **Turing-complete with runtime monitoring** (Python/JavaScript + timeouts)
2. **Custom DSL** (build our own language)
3. **Non-Turing-complete languages** (Starlark + CEL)

## Decision Outcome

Chosen option: **"Non-Turing-complete languages (Starlark + CEL)"**, because it's the only option that provides mathematical guarantees of termination without runtime overhead.

### Positive Consequences

* **DoS Prevention**: Compiler rejects infinite loops (no runtime checks needed)
* **Predictable Costs**: Maximum execution time computable from script structure
* **Deterministic Testing**: Tests never flake due to timing issues
* **Multi-tenant Safety**: No tenant can starve others' resources
* **AI-Friendly**: Simpler syntax makes LLM generation more reliable
* **Battle-Tested**: Google uses Starlark (Bazel) and CEL (Kubernetes) at massive scale

### Negative Consequences

* **Learning Curve**: Developers must adapt to "no while loops"
* **Pattern Changes**: Some algorithms require restructuring (recursion → iteration)
* **Marketing Challenge**: "Not Turing-complete" sounds like limitation

## Pros and Cons of the Options

### Turing-complete with runtime monitoring

Python/JavaScript with timeout enforcement:

```python
import signal
def timeout_handler(signum, frame):
    raise TimeoutError()
signal.signal(signal.SIGALRM, timeout_handler)
signal.alarm(10)  # 10 second timeout
try:
    tenant_script()
except TimeoutError:
    # Tenant gets charged for failed execution
```

* Good, because familiar languages (Python/JS)
* Good, because maximum expressiveness
* Bad, because timeouts are arbitrary (10s? 60s? How do you know?)
* Bad, because tenant gets charged for failed execution
* Bad, because requires signal handling (platform-specific)
* Bad, because doesn't prevent memory bombs
* Bad, because AI-generated code can still create infinite loops

### Custom DSL

Build a proprietary workflow language:

```yaml
workflow:
  steps:
    - validate:
        condition: "amount > 0"
    - execute:
        action: "transfer"
```

* Good, because full control over semantics
* Good, because guaranteed termination (if designed correctly)
* Bad, because years of development (parser, compiler, tooling)
* Bad, because no ecosystem (no LSP, no syntax highlighting)
* Bad, because vendor lock-in for tenants
* Bad, because AI can't generate it reliably (no training data)
* Bad, because limited expressiveness (YAML isn't programmable)

### Non-Turing-complete languages (Starlark + CEL)

Use Google's proven languages with termination guarantees:

**Starlark for workflows:**
```python
# ✅ Allowed - finite iteration
for customer in customers:
    if customer.balance > 0:
        notify(customer)

# ❌ Rejected by compiler - no while
while condition():  # Syntax error
    process()

# ❌ Rejected by compiler - no recursion
def factorial(n):
    return n * factorial(n-1)  # Runtime error
```

**CEL for validation:**
```python
# Sub-millisecond execution guaranteed
"amount > 0 && amount <= account.credit_limit && currency == 'GBP'"
```

* Good, because **mathematically proven termination** (no Turing-completeness)
* Good, because **battle-tested at Google scale** (billions of evaluations/day)
* Good, because **great tooling** (LSP servers, syntax highlighting exist)
* Good, because **AI-friendly** (simpler than Python, lots of training data)
* Good, because **deterministic execution** (same input = same time, always)
* Good, because **no runtime overhead** (compiler enforces, not runtime monitors)
* Bad, because **learning curve** (developers must adapt patterns)
* Bad, because **98% expressiveness** (lose while/recursion, but rarely need them)

## Implementation Details

### What Starlark Prohibits

```python
# ❌ No while loops
while True:
    x += 1

# ❌ No recursion
def fib(n):
    return fib(n-1) + fib(n-2)

# ❌ No threads
import threading  # Module doesn't exist

# ❌ No unbounded iteration
for i in range(sys.maxsize):  # range() requires finite bounds
    process(i)
```

### How to Implement Common Patterns

**Pattern 1: Retry Logic**

```python
# ❌ WRONG (Turing-complete - while loop)
while not success:
    success = try_operation()

# ✅ RIGHT (Bounded - finite retries)
for attempt in range(5):  # Max 5 retries
    if try_operation():
        break
```

**Pattern 2: Conditional Processing**

```python
# ❌ WRONG (Turing-complete - while loop)
while has_more_items():
    process(next_item())

# ✅ RIGHT (Bounded - process known collection)
items = get_all_items()  # Finite collection
for item in items:
    process(item)
```

**Pattern 3: Tree Traversal**

```python
# ❌ WRONG (Turing-complete - recursion)
def traverse(node):
    if node.left:
        traverse(node.left)
    process(node)
    if node.right:
        traverse(node.right)

# ✅ RIGHT (Bounded - iterative with explicit stack)
def traverse(root):
    stack = [root]
    for _ in range(len(stack)):  # Bounded by initial size
        node = stack.pop()
        process(node)
        if node.right:
            stack.append(node.right)
        if node.left:
            stack.append(node.left)
```

### CEL for Hot Path Validation

CEL guarantees **sub-millisecond execution** because it:
- Has no loops (not even `for`)
- Is purely functional (no side effects)
- Has bounded recursion depth (configurable, default 100)

```python
# Energy validation rule (evaluates in < 1ms)
validation = """
    amount > 0 &&
    amount <= 1000000 &&
    quality in ['ESTIMATE', 'COEFFICIENT', 'ACTUAL', 'REVISED'] &&
    timestamp > now - duration('24h')
"""

# Financial validation (evaluates in < 1ms)
pricing = """
    quantity * market_data.price * (1 + fee_rate)
"""
```

## Comparison with Competitors

| Platform | Scripting | Termination Guarantee | Prevent DoS |
|----------|-----------|----------------------|-------------|
| **Temporal** | Python/Go/TypeScript | ❌ No (Turing-complete) | Runtime timeouts only |
| **AWS Step Functions** | JSON (no scripting) | ✅ Yes (state machine) | N/A (too limited) |
| **Stripe Workflows** | Proprietary DSL | ✅ Yes (no loops) | N/A (closed source) |
| **Zapier** | No-code only | ✅ Yes (no scripting) | N/A (GUI only) |
| **Meridian** | **Starlark + CEL** | **✅ Yes (proven)** | **Compiler enforced** |

## Links

* [ADR-0028: Starlark Saga Orchestration with CEL Valuation](0028-starlark-saga-cel-valuation.md)
* [Starlark Language Spec](https://github.com/bazelbuild/starlark/blob/master/spec.md)
* [CEL Language Spec](https://github.com/google/cel-spec)
* [Bazel: Why Starlark?](https://bazel.build/rules/language)
* [Kubernetes: CEL for Admission Control](https://kubernetes.io/docs/reference/using-api/cel/)

## Notes

**Future Considerations:**

If tenants require functionality that seems to need Turing-completeness:
1. First, challenge the requirement (98% of workflows don't need it)
2. Implement as first-class platform feature (we control execution)
3. Offer pre-built templates (e.g., "exponential backoff retry")

**Reconsidering This Decision:**

This decision should be reconsidered if:
- Tenants consistently require patterns impossible in bounded languages (has not happened yet)
- A better bounded language emerges (unlikely - Google chose these for good reasons)
- We move to expensive per-tenant isolation (Firecracker VMs) where DoS is less concerning

**Educational Resources for Developers:**

When onboarding developers used to Turing-complete languages:
- Show examples of common patterns in Starlark
- Emphasize that "no while loops" prevents more bugs than it causes
- Demonstrate that 98% of business logic is naturally bounded
- Compare to SQL (also not Turing-complete, universally adopted)
