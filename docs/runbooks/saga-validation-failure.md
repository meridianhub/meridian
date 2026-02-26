---
name: saga-validation-failure
description: Incident response procedures for saga validation failures in upload, activation, and CI pipelines
triggers:
  - Spike in saga_validation_errors_total metric
  - Tenant reports unable to upload or activate a saga script
  - CI pipeline fails on saga validation step
  - saga_validation_total{status="failed"} rate increase
instructions: |
  Classify the validation error category. Use the error message and line number
  to identify the root cause. Follow the resolution steps for each category.
  Escalate if the issue is in the platform validation code, not the tenant script.
---

# Saga Validation Failure Runbook

**When to use this runbook**: Saga validation failures are reported through
metrics, tenant support requests, or CI pipeline failures.

## Quick Reference

| Error Category | Typical Cause | Recovery Time | Escalation |
|---------------|---------------|---------------|------------|
| **SYNTAX** | Script parse error | Immediate | None - tenant fixes script |
| **UNDEFINED_HANDLER** | Typo in handler name | Immediate | None - check handlers.yaml |
| **TYPE_MISMATCH** | Wrong parameter type | Immediate | None - check schema |
| **RUNTIME** | Script calls fail() | Minutes | None - review script logic |
| **TIMEOUT** | Script too complex | Minutes | Platform team if under 5s |

## 1. Triage

### Check validation metrics

```promql
# Failure rate (last 15 minutes)
rate(saga_validation_total{status="failed"}[15m])

# Errors by category
sum by (error_category) (
  rate(saga_validation_errors_total[15m])
)

# Affected sagas
topk(10, sum by (saga_name) (
  rate(saga_validation_errors_total[15m])
))
```

### Classify the failure

**Single tenant, single saga**: Likely a script issue. Proceed to section 2.

**Multiple tenants, same error category**: Possible platform issue. Proceed to section 3.

**All validations failing**: Platform service issue. Proceed to section 4.

## 2. Script-Level Failures (Single Tenant)

### SYNTAX errors

**Symptoms**: `saga_validation_errors_total{error_category="SYNTAX"}` increasing for one saga.

**Common causes**:

- Using `is` / `is not` instead of `==` / `!=` (most common)
- Using `while` loops (forbidden in Starlark)
- Using `import` statements
- Missing colons, brackets, or indentation

**Resolution**:

1. Ask the tenant for their script or retrieve from the saga registry
2. Run local validation: `meridian-cli saga validate <script.star>`
3. The error message includes line and column numbers
4. Direct the tenant to the [Starlark Style Guide](../guides/starlark-style-guide.md)

### UNDEFINED_HANDLER errors

**Symptoms**: `saga_validation_errors_total{error_category="UNDEFINED_HANDLER"}` for specific saga.

**Resolution**:

1. Check the error message for the handler name and suggestion
2. Verify the handler exists in `shared/pkg/saga/schema/handlers.yaml`
3. Common issues:
   - Typo in module or handler name (`initiate_logg` vs `initiate_log`)
   - Wrong module prefix (`pos_keeping` vs `position_keeping`)
   - Handler removed in a schema update

### TYPE_MISMATCH errors

**Symptoms**: `saga_validation_errors_total{error_category="TYPE_MISMATCH"}` for specific saga.

**Resolution**:

1. Check the error message for the parameter name and expected type
2. Verify parameter types against `handlers.yaml` schema
3. Common issues:
   - Passing string instead of `Decimal` for amount fields
   - Passing integer instead of string for IDs
   - Wrong enum values for direction fields

### RUNTIME errors

**Symptoms**: `saga_validation_errors_total{error_category="RUNTIME"}` for specific saga.

**Resolution**:

1. Check if the script explicitly calls `fail()` during validation
2. Review the script logic for conditions that trigger failure with mock data
3. Mock handlers return default values - ensure the script handles them

### TIMEOUT errors

**Symptoms**: `saga_validation_errors_total{error_category="TIMEOUT"}` for specific saga.

**Resolution**:

1. Check the saga's complexity metrics: `saga_complexity_score{saga_name="<name>"}`
2. If handler call count is high (>20), suggest script simplification
3. If the script has deeply nested loops (up to 3 levels), suggest flattening
4. The timeout is 5 seconds - most scripts complete in under 100ms

## 3. Platform-Level Failures (Multiple Tenants)

### Schema registry issue

**Symptoms**: Multiple tenants getting UNDEFINED_HANDLER for previously valid handlers.

**Check**:

```bash
# Verify handlers.yaml is intact
cat shared/pkg/saga/schema/handlers.yaml | head -20

# Check schema loading in service logs
kubectl logs -l app=reference-data -n meridian --tail=100 | grep -i "schema\|handler"
```

**Resolution**:

1. Verify `handlers.yaml` hasn't been modified incorrectly
2. Check if a deployment changed the schema
3. Roll back the schema change if needed

### Mock registry generation failure

**Symptoms**: All validations failing with internal errors (not categorized).

**Check**:

```bash
kubectl logs -l app=reference-data -n meridian --tail=100 | grep -i "mock\|registry\|validation"
```

**Resolution**:

1. Check if the mock registry generates successfully from the current schema
2. Verify the schema format is valid YAML
3. Check for new handler parameter types that the mock generator doesn't support

## 4. Service-Level Failures (All Validations)

### Validation service unavailable

**Symptoms**: gRPC errors on ValidateSaga / ValidateSagaDraft endpoints.

**Check**:

```bash
# Check pod status
kubectl get pods -l app=reference-data -n meridian

# Check service health
grpcurl -plaintext localhost:9090 grpc.health.v1.Health/Check

# Check recent events
kubectl get events -n meridian --sort-by='.lastTimestamp' | head -20
```

**Resolution**:

1. If pods are crashing, check logs for OOM or panic
2. If pods are healthy but endpoints fail, check gRPC routing
3. Restart the service: `kubectl rollout restart deployment/reference-data -n meridian`

### Runtime initialisation failure

**Symptoms**: Validation returns internal errors mentioning "runtime" or "timeout".

**Check**:

```bash
kubectl logs -l app=reference-data -n meridian --tail=100 | grep -i "runtime\|starlark"
```

**Resolution**:

1. The Starlark runtime is created per validation request
2. Check for resource exhaustion (CPU, memory) on the node
3. Check if the 5-second timeout is appropriate for the workload

## 5. CI Pipeline Failures

### Validation step fails in CI

**Symptoms**: CI job fails on `meridian-cli saga validate` step.

**Check**:

1. Review CI logs for the specific validation error
2. Verify `handlers.yaml` is available in the CI environment
3. Check if the script was modified in the PR

**Resolution**:

1. Fix the script based on the error message
2. If using `--handlers`, verify the path is correct in CI
3. Run validation locally first: `meridian-cli saga validate --json <script.star>`

## 6. Prevention

### Pre-deployment checklist

- [ ] Run `meridian-cli saga validate` locally before pushing
- [ ] Check complexity score is below 7
- [ ] Verify all handler names exist in `handlers.yaml`
- [ ] Review semantic lint warnings even if not blocking

### Monitoring alerts

Recommended alert rules:

```yaml
# High validation failure rate
- alert: SagaValidationFailureRateHigh
  expr: |
    rate(saga_validation_total{status="failed"}[15m])
    / rate(saga_validation_total[15m]) > 0.5
  for: 10m
  labels:
    severity: warning
  annotations:
    summary: "Saga validation failure rate above 50%"

# Unexpected error category spike
- alert: SagaValidationErrorSpike
  expr: |
    rate(saga_validation_errors_total[5m]) > 10
  for: 5m
  labels:
    severity: warning
  annotations:
    summary: "Spike in saga validation errors"
```

## Related Resources

- [Saga Validation Guide](../guides/saga-validation.md) - Usage documentation
- [Saga Failure Recovery Runbook](saga-failure-recovery.md) - Production saga execution failures
- [Starlark Style Guide](../guides/starlark-style-guide.md) - Script writing conventions
