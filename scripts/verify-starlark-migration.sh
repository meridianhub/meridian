#!/bin/bash
set -e

echo "=== Starlark Migration Verification ==="
echo ""

# Track overall success
FAILED=0

# 1. Script consolidation
echo "[1/6] Checking script consolidation..."
CURRENT_ACCOUNT_SAGAS=$(fd -t f -e star . services/current-account/sagas/ 2>/dev/null | wc -l | tr -d ' ')
PAYMENT_ORDER_SAGAS=$(fd -t f -e star . services/payment-order/sagas/ 2>/dev/null | wc -l | tr -d ' ')
REFERENCE_DATA_SAGAS=$(fd -t f -e star . services/reference-data/saga/defaults/ 2>/dev/null | wc -l | tr -d ' ')

if [ "$CURRENT_ACCOUNT_SAGAS" -ne 0 ]; then
  echo "  ❌ FAIL: Found $CURRENT_ACCOUNT_SAGAS saga scripts in current-account/sagas/ (expected 0)"
  FAILED=1
else
  echo "  ✅ PASS: No duplicate saga scripts in current-account/sagas/"
fi

if [ "$PAYMENT_ORDER_SAGAS" -ne 0 ]; then
  echo "  ❌ FAIL: Found $PAYMENT_ORDER_SAGAS saga scripts in payment-order/sagas/ (expected 0)"
  FAILED=1
else
  echo "  ✅ PASS: No duplicate saga scripts in payment-order/sagas/"
fi

if [ "$REFERENCE_DATA_SAGAS" -ne 3 ]; then
  echo "  ❌ FAIL: Found $REFERENCE_DATA_SAGAS saga scripts in reference-data/saga/defaults/ (expected 3)"
  FAILED=1
else
  echo "  ✅ PASS: Found 3 canonical saga scripts in reference-data/saga/defaults/"
fi

echo ""

# 2. No AddStep pattern in production code
echo "[2/6] Checking for saga.AddStep() pattern in production code..."
ADDSTEP_MATCHES=$(rg "saga\.AddStep" --type go services/ 2>/dev/null | grep -v "_test.go" | grep -v "mock" | wc -l | tr -d ' ')

if [ "$ADDSTEP_MATCHES" -ne 0 ]; then
  echo "  ❌ FAIL: Found saga.AddStep() in production code:"
  rg "saga\.AddStep" --type go services/ 2>/dev/null | grep -v "_test.go" | grep -v "mock" | head -5
  FAILED=1
else
  echo "  ✅ PASS: No saga.AddStep() found in production code"
fi

echo ""

# 3. StarlarkSagaRunner usage in orchestrators
echo "[3/6] Checking StarlarkSagaRunner usage in orchestrators..."
ORCHESTRATORS_CHECKED=0
ORCHESTRATORS_USING_STARLARK=0

for orch in services/current-account/service/*orchestrator.go services/payment-order/service/*orchestrator.go; do
  if [ -f "$orch" ]; then
    ORCHESTRATORS_CHECKED=$((ORCHESTRATORS_CHECKED + 1))
    if rg -q "StarlarkSagaRunner" "$orch" 2>/dev/null; then
      ORCHESTRATORS_USING_STARLARK=$((ORCHESTRATORS_USING_STARLARK + 1))
      echo "  ✅ $(basename $orch) uses StarlarkSagaRunner"
    else
      echo "  ❌ $(basename $orch) does NOT use StarlarkSagaRunner"
      FAILED=1
    fi
  fi
done

if [ "$ORCHESTRATORS_CHECKED" -eq "$ORCHESTRATORS_USING_STARLARK" ]; then
  echo "  ✅ PASS: All $ORCHESTRATORS_CHECKED orchestrators use StarlarkSagaRunner"
else
  echo "  ❌ FAIL: Only $ORCHESTRATORS_USING_STARLARK of $ORCHESTRATORS_CHECKED orchestrators use StarlarkSagaRunner"
  FAILED=1
fi

echo ""

# 4. Handler implementation check (no NoOp or stub patterns)
echo "[4/6] Checking handler implementations for NoOp/stub patterns..."
if [ -d "services/position-keeping/client" ] || [ -d "services/financial-accounting/client" ]; then
  NOOP_MATCHES=$(rg "(NoOp|stub)" services/*/client/starlark.go 2>/dev/null | wc -l | tr -d ' ')

  if [ "$NOOP_MATCHES" -ne 0 ]; then
    echo "  ❌ FAIL: Found NoOp or stub handlers:"
    rg "(NoOp|stub)" services/*/client/starlark.go 2>/dev/null | head -5
    FAILED=1
  else
    echo "  ✅ PASS: No NoOp or stub handlers found"
  fi
else
  echo "  ⚠️  SKIP: No service client directories found (handlers may not be implemented yet)"
fi

echo ""

# 5. Verify saga scripts exist and are readable
echo "[5/6] Verifying canonical saga scripts..."
SAGA_SCRIPTS=(
  "services/reference-data/saga/defaults/deposit/v1.0.0.star"
  "services/reference-data/saga/defaults/withdrawal/v1.0.0.star"
  "services/reference-data/saga/defaults/payment_execution/v1.0.0.star"
)

for script in "${SAGA_SCRIPTS[@]}"; do
  if [ -f "$script" ]; then
    # Check if script has content
    if [ -s "$script" ]; then
      echo "  ✅ $(basename $(dirname $script))/$(basename $script) exists and has content"
    else
      echo "  ❌ FAIL: $script is empty"
      FAILED=1
    fi
  else
    echo "  ❌ FAIL: $script does not exist"
    FAILED=1
  fi
done

echo ""

# 6. Summary
echo "[6/6] Summary"
if [ "$FAILED" -eq 0 ]; then
  echo "  ✅ ALL VERIFICATIONS PASSED"
  echo ""
  echo "=== MIGRATION STATUS: READY FOR E2E TESTING ==="
  exit 0
else
  echo "  ❌ SOME VERIFICATIONS FAILED"
  echo ""
  echo "=== MIGRATION STATUS: NOT READY ==="
  exit 1
fi
