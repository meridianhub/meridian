# Saga Validation Guide

Starlark saga scripts are automatically validated before deployment using
auto-generated mocks from `handlers.yaml`. This catches the majority of runtime
errors at upload time with zero tenant effort.

## Overview

Meridian validates saga scripts at multiple levels:

| Layer | What It Catches | When It Runs |
|-------|----------------|--------------|
| **Static validation** | Syntax errors, blocked functions, loop nesting depth, script size | Script parse time |
| **Semantic linting** | Decimal arithmetic outside CEL, magic numbers, missing pre-checks, hardcoded codes | Draft upload |
| **Dry-run execution** | Undefined handlers, type mismatches, runtime failures, timeouts | Activation / CLI |
| **Reference validation** | Missing instruments, accounts, sagas, handler parameters | Service-level validation |
| **Visibility validation** | Party scope violations, unauthorized lookups | Pre-flight check |

## What Validation Catches

### Static Validation

Runs at parse time before any execution. Enforces security constraints:

- **Syntax errors**: Starlark parse failures (e.g., using `is` instead of `==`)
- **Blocked functions**: `load`, `exec`, `compile`, `open`, `eval`, `__import__`, `setattr`, `delattr`
- **Excessive loop nesting**: Maximum 3 levels of nested loops
- **Script too large**: Maximum 64KB script size

### Semantic Linting

AST-based analysis that catches common mistakes:

| Issue | Severity | Description |
|-------|----------|-------------|
| Decimal arithmetic | Warning | Decimal maths should use CEL expressions |
| Magic numbers | Warning | Hardcoded numeric literals |
| Nested conditionals | Warning | Deep if/else nesting |
| Hardcoded codes | Error | Hardcoded instrument or account codes |
| Missing pre-check | Error | External handler call without `verify_external_state` |

**Warning** severity allows activation but is reported. **Error** severity blocks activation.

### Dry-Run Execution

Executes the script with mock handlers generated from `handlers.yaml`:

- **Undefined handlers**: Typos or wrong module names (category: `UNDEFINED_HANDLER`)
- **Type mismatches**: Wrong parameter types (category: `TYPE_MISMATCH`)
- **Runtime errors**: Script calls `fail()` or raises an exception (category: `RUNTIME`)
- **Timeouts**: Execution exceeds 5 seconds (category: `TIMEOUT`)

### Complexity Metrics

Every validation produces complexity metrics:

| Metric | Description | Formula |
|--------|-------------|---------|
| Handler Call Count | Number of service handler calls | Direct count from execution |
| Operation Count | Starlark operations (loops, conditionals, assignments) | AST analysis |
| Complexity Score | 0-10 scale | `min(10, HandlerCallCount / 2)` |
| Estimated Duration | Projected execution time | `HandlerCallCount * 10ms` |

## Usage

### CLI (Local Testing)

Validate a script before deployment:

```bash
# Validate with human-readable output
meridian-cli saga validate withdrawal.star

# JSON output for CI/CD pipelines
meridian-cli saga validate --json deposit.star

# Custom handlers.yaml path
meridian-cli saga validate --handlers /path/to/handlers.yaml transfer.star
```

Exit codes:

- `0` - Script is valid
- `1` - Validation failed

The CLI automatically loads `shared/pkg/saga/schema/handlers.yaml` when run
from the repository root. Use `--handlers` to specify an alternative schema file.

**Example output (success):**

```text
Validation Result: PASS

Complexity Metrics:
  Handler Calls:    6
  Operations:       24
  Complexity Score: 3/10
  Est. Duration:    60ms

No errors found.
```

**Example output (failure):**

```text
Validation Result: FAIL

Errors:
  [UNDEFINED_HANDLER] line 15: handler "position_keeping.initiate_logg" not found
    Did you mean: position_keeping.initiate_log?

  [TYPE_MISMATCH] line 22: parameter "amount" expects Decimal, got string

2 error(s) found.
```

### gRPC API

#### ValidateSaga (Dry-Run)

Validates a script without deployment:

```bash
grpcurl -d '{
  "saga_name": "withdrawal",
  "script": "withdrawal_saga = saga(name=\"current_account_withdrawal\")\n...",
  "version": "1.0.0"
}' localhost:9090 meridian.saga.v1.SagaRegistry/ValidateSaga
```

Response includes `success`, `errors[]`, `metrics`, and `formatted_report`.

#### ValidateSagaDraft

Validates an existing draft saga by ID:

```bash
grpcurl -d '{
  "id": "550e8400-e29b-41d4-a716-446655440000"
}' localhost:9090 meridian.saga.v1.SagaRegistry/ValidateSagaDraft
```

Returns `ValidationResult` with status (`READY`, `WARNINGS`, `BLOCKED`), warnings, and critical errors.

#### ActivateSaga

Transitions a draft to active, running full validation including semantic linting:

```bash
grpcurl -d '{
  "id": "550e8400-e29b-41d4-a716-446655440000"
}' localhost:9090 meridian.saga.v1.SagaRegistry/ActivateSaga
```

Activation fails if the script has any errors or blocking lint issues.

### Validation Levels

| Level | Function | Use Case |
|-------|----------|----------|
| `ValidateSagaScript()` | Basic syntax + security | Quick parse check |
| `ValidateDraft()` | Full validation, warnings allowed | Draft upload |
| `ValidateActivation()` | Strict, errors block activation | Production deployment |
| `DryRunValidator.Validate()` | Mock execution with metrics | CLI and API |

## Security Constraints

The Starlark runtime enforces strict security boundaries:

| Constraint | Value | Rationale |
|------------|-------|-----------|
| Max script size | 64 KB | Prevent resource exhaustion |
| Max loop nesting | 3 levels | Bound computational complexity |
| Execution timeout | 5 seconds | Prevent runaway scripts |
| Memory warning | 10 MB | Early detection of memory issues |
| Max steps | 1,000,000 | Hard limit on Starlark operations |
| `while` loops | Forbidden | Guarantees termination |
| Recursion | Forbidden | Prevents stack overflow |
| `load()` / imports | Blocked | Sandboxed execution |

## Capacity Planning

Use complexity metrics for capacity estimation:

**Formula**: `Expected RPS * Avg Handler Calls * 10ms = Required concurrency`

**Example**: If average complexity is 5 handler calls and expected RPS is 100:

- Load: 100 RPS *5 handlers* 10ms = 5,000 ms/s
- Required: 5 concurrent workers minimum

## Monitoring

### Prometheus Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `saga_validation_total` | Counter | `saga_name`, `status` | Validation count (success/failed) |
| `saga_validation_errors_total` | Counter | `saga_name`, `error_category` | Errors by category |
| `saga_complexity_score` | Histogram | `saga_name` | Complexity distribution (0-10) |
| `saga_handler_call_count` | Histogram | `saga_name` | Handler calls per validation |

**Error categories for `saga_validation_errors_total`:**

- `SYNTAX` - Parse failures
- `UNDEFINED_HANDLER` - Unknown handler references
- `TYPE_MISMATCH` - Wrong parameter types
- `RUNTIME` - Script execution errors
- `TIMEOUT` - Execution exceeded 5s

### Useful PromQL Queries

```promql
# Validation failure rate (last 1h)
rate(saga_validation_total{status="failed"}[1h])
  / rate(saga_validation_total[1h])

# Most common error categories
topk(5, sum by (error_category) (
  rate(saga_validation_errors_total[1h])
))

# Average complexity by saga
avg by (saga_name) (saga_complexity_score)

# Sagas with high handler call counts (potential performance concern)
histogram_quantile(0.95, rate(saga_handler_call_count[1h]))
```

## Troubleshooting

### Validation passes but script fails in production

Validation uses mock handlers. Real handlers may fail due to:

- Database constraints (insufficient balance, account frozen)
- External API failures (gateway timeouts, rate limiting)
- Business rule violations (amount limits, duplicate transactions)

Validation catches syntax and type errors, not business logic failures.
For production issues, see the
[Saga Failure Recovery Runbook](../runbooks/saga-failure-recovery.md).

### How do I see available handlers?

Check the schema file directly:

```bash
cat shared/pkg/saga/schema/handlers.yaml
```

Or use the CLI (when available):

```bash
meridian-cli saga list-handlers
```

Available service modules: `position_keeping`, `financial_accounting`,
`current_account`, `valuation_engine`, `repository`, `notification`,
`payment_order`.

### Validation timeout (5 seconds exceeded)

Causes:

- Too many handler calls (reduce operations or simplify loops)
- Complex conditional logic creating many execution paths
- Iterating over large collections

**Fix**: Reduce handler calls or simplify the script. Check the complexity
score -- scripts scoring above 7 should be reviewed for simplification.

### "Undefined handler" but I'm sure it exists

1. Verify the handler name matches `handlers.yaml` exactly (case-sensitive, snake_case)
2. Check you're using `module.handler()` format (e.g., `position_keeping.initiate_log`)
3. If using the CLI with `--handlers`, verify the path is correct

### "Blocked function" error

The following functions are blocked for security:

```text
load, exec, compile, open, eval, __import__, setattr, delattr
```

These prevent module loading, code execution, and file system access.
Saga scripts must use only built-in functions and registered service modules.

### Semantic lint blocking activation

If activation is blocked by a lint error:

- `LintIssueTypeHardcodedCode`: Replace hardcoded instrument/account codes
  with `resolve_instrument()` or `resolve_account()`
- `LintIssueTypeMissingPreCheck`: Add `verify_external_state()` before
  external handler calls

Lint warnings (non-blocking) are informational and reported but do not prevent activation.

## Related Resources

- [Starlark Style Guide](starlark-style-guide.md) - Writing conventions for saga scripts
- [Starlark Built-ins Reference](starlark-built-ins-reference.md) - Available functions and types
- [Saga Failure Recovery Runbook](../runbooks/saga-failure-recovery.md) - Production incident response
- [Saga Validation Failure Runbook](../runbooks/saga-validation-failure.md) - Validation-specific incident response
- Handler schema: `shared/pkg/saga/schema/handlers.yaml`
- Proto definition: `api/proto/meridian/saga/v1/saga_registry.proto`
