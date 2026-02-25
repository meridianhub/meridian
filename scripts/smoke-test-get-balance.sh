#!/usr/bin/env bash
#
# Smoke test for GetBalance in a Tilt environment.
#
# Verifies that:
#   1. internal-account and position-keeping services are healthy
#   2. A test account can be created via InitiateInternalAccount
#   3. GetBalance returns a response (not an error) for the created account
#
# Prerequisites:
#   - grpcurl installed
#   - Services running in Tilt (internal-account on 50057, position-keeping on 50053)
#
# Usage:
#   ./scripts/smoke-test-get-balance.sh
#   tilt trigger smoke-test-get-balance

set -euo pipefail

IBA_ADDR="${IBA_ADDR:-localhost:50057}"
PK_ADDR="${PK_ADDR:-localhost:50053}"
HEALTH_TIMEOUT="${HEALTH_TIMEOUT:-60}"
POLL_INTERVAL=2

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
NC='\033[0m'

log_info()  { echo -e "${GREEN}[INFO]${NC}  $*"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
log_error() { echo -e "${RED}[ERROR]${NC} $*"; }

# Wait for a gRPC health check to return SERVING.
# Args: $1=address $2=service-name $3=display-name
wait_for_health() {
    local addr="$1" svc="$2" name="$3"
    local elapsed=0

    log_info "Waiting for ${name} health check at ${addr} (timeout: ${HEALTH_TIMEOUT}s)..."
    while [ "$elapsed" -lt "$HEALTH_TIMEOUT" ]; do
        if grpcurl -plaintext -d "{\"service\": \"${svc}\"}" \
            "${addr}" grpc.health.v1.Health/Check 2>/dev/null | grep -q "SERVING"; then
            log_info "${name} is SERVING"
            return 0
        fi
        sleep "$POLL_INTERVAL"
        elapsed=$((elapsed + POLL_INTERVAL))
    done

    log_error "${name} did not become healthy within ${HEALTH_TIMEOUT}s"
    return 1
}

# --- Step 1: Wait for services to be healthy ---

wait_for_health "$IBA_ADDR" "internal-account" "Internal Account"
wait_for_health "$PK_ADDR"  "position-keeping"      "Position Keeping"

# --- Step 2: Create a test account ---

IDEMPOTENCY_KEY="smoke-test-getbalance-$(date +%s)"
ACCOUNT_CODE="SMOKE-BAL-$(date +%s)"

log_info "Creating test account (code: ${ACCOUNT_CODE})..."

CREATE_RESP=$(grpcurl -plaintext -d "{
  \"account_code\": \"${ACCOUNT_CODE}\",
  \"name\": \"Smoke Test GetBalance Account\",
  \"account_type\": \"INTERNAL_ACCOUNT_TYPE_CLEARING\",
  \"instrument_code\": \"GBP\",
  \"clearing_purpose\": \"CLEARING_PURPOSE_GENERAL\",
  \"idempotency_key\": {\"key\": \"${IDEMPOTENCY_KEY}\"}
}" "$IBA_ADDR" meridian.internal_account.v1.InternalAccountService/InitiateInternalAccount 2>&1) || true

ACCOUNT_ID=$(echo "$CREATE_RESP" | grep -o '"accountId":[[:space:]]*"[^"]*"' | head -1 | sed 's/.*"accountId":[[:space:]]*"\([^"]*\)".*/\1/')

if [ -z "$ACCOUNT_ID" ]; then
    log_error "Failed to create test account. Response:"
    echo "$CREATE_RESP"
    exit 1
fi

log_info "Created account: ${ACCOUNT_ID}"

# --- Step 3: Call GetBalance and verify response ---

log_info "Calling GetBalance for account ${ACCOUNT_ID}..."

BALANCE_RESP=$(grpcurl -plaintext -d "{\"account_id\": \"${ACCOUNT_ID}\"}" \
    "$IBA_ADDR" meridian.internal_account.v1.InternalAccountService/GetBalance 2>&1) || BALANCE_EXIT=$?
BALANCE_EXIT=${BALANCE_EXIT:-0}

if [ "$BALANCE_EXIT" -ne 0 ]; then
    log_error "GetBalance call failed (exit code: ${BALANCE_EXIT}). Response:"
    echo "$BALANCE_RESP"
    exit 1
fi

# Verify the response contains accountId (basic structural check)
if echo "$BALANCE_RESP" | grep -q '"accountId"'; then
    log_info "GetBalance returned a valid response"
else
    log_error "GetBalance response missing accountId field. Response:"
    echo "$BALANCE_RESP"
    exit 1
fi

# Check for currentBalance presence (may be null if no position exists yet, which is acceptable)
if echo "$BALANCE_RESP" | grep -q '"currentBalance"'; then
    log_info "Response includes currentBalance field"
else
    log_warn "Response does not include currentBalance (position may not exist yet - acceptable for smoke test)"
fi

# --- Done ---

log_info "Smoke test PASSED"
echo ""
echo "  Account ID: ${ACCOUNT_ID}"
echo "  Response:"
echo "$BALANCE_RESP" | sed 's/^/    /'
echo ""
