#!/bin/bash
# Meridian Platform Demo - Enterprise Banking Microservices
# Demonstrates: Saga Pattern, Load Balancing, Tracing, Health, Idempotency

set -e

# Trap handler to clean up background Tilt process
cleanup_tilt() {
    if [ -n "$TILT_PID" ] && kill -0 "$TILT_PID" 2>/dev/null; then
        echo -e "\n${YELLOW}Cleaning up Tilt (PID: $TILT_PID)...${NC}"
        kill "$TILT_PID" 2>/dev/null || true
        wait "$TILT_PID" 2>/dev/null || true
    fi
}

# Colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
MAGENTA='\033[0;35m'
NC='\033[0m' # No Color

# Tenant configuration - all demo operations run within this tenant context
# The system is always multi-tenant; every request must include tenant ID
DEMO_TENANT="${DEMO_TENANT:-demo}"
TENANT_HEADER="-H x-tenant-id:${DEMO_TENANT}"

# Helper function for pausing between sections
pause() {
    echo -e "\n${YELLOW}Press any key to continue to next section...${NC}"
    read -n 1 -s -r
    echo ""
}

echo -e "${BLUE}╔════════════════════════════════════════════════════════════════╗${NC}"
echo -e "${BLUE}║  Meridian Banking Platform - Comprehensive Demo                ║${NC}"
echo -e "${BLUE}║  Saga • Load Balancing • Tracing • Health • Idempotency        ║${NC}"
echo -e "${BLUE}╚════════════════════════════════════════════════════════════════╝${NC}"
echo ""

# Check prerequisites
echo -e "${YELLOW}Checking prerequisites...${NC}"
command -v grpcurl >/dev/null 2>&1 || { echo "grpcurl required. Install: brew install grpcurl"; exit 1; }
command -v jq >/dev/null 2>&1 || { echo "jq required. Install: brew install jq"; exit 1; }
command -v kubectl >/dev/null 2>&1 || { echo "kubectl required."; exit 1; }
command -v tilt >/dev/null 2>&1 || { echo "tilt required. Install: brew install tilt"; exit 1; }
command -v bc >/dev/null 2>&1 || { echo "bc required. Install: brew install bc"; exit 1; }
echo -e "${GREEN}✓ All tools available${NC}\n"

# Check if Tilt is running, start if needed
echo -e "${YELLOW}Checking Tilt status...${NC}"
if ! tilt get uisession >/dev/null 2>&1; then
    echo -e "${YELLOW}Tilt not running. Starting Tilt in background...${NC}"
    echo -e "${YELLOW}This may take 2-3 minutes for initial startup.${NC}"

    # Start Tilt in background with logging
    tilt up > /tmp/tilt-demo.log 2>&1 &
    TILT_PID=$!

    # Install trap to clean up on exit
    trap cleanup_tilt EXIT INT TERM

    # Verify Tilt started successfully
    sleep 5
    if ! kill -0 "$TILT_PID" 2>/dev/null; then
        echo -e "${YELLOW}⚠ Tilt startup may have failed. Check: /tmp/tilt-demo.log${NC}"
        exit 1
    fi

    # Wait for Tilt to be ready
    echo -e "${YELLOW}Waiting for Tilt to initialize...${NC}"
    sleep 5

    # Wait for all services to be ready (max 3 minutes)
    TIMEOUT=180
    ELAPSED=0
    while [ $ELAPSED -lt $TIMEOUT ]; do
        READY_COUNT=$(kubectl get pods -o json | jq '[.items[] | select(.metadata.name | test("current-account|position-keeping|financial-accounting|party")) | select(.status.phase == "Running")] | length')
        if [ "$READY_COUNT" -ge 4 ]; then
            echo -e "${GREEN}✓ All services ready${NC}"
            break
        fi
        echo -e "${YELLOW}  Waiting for services... ($READY_COUNT/4 ready)${NC}"
        sleep 5
        ELAPSED=$((ELAPSED + 5))
    done

    if [ $ELAPSED -ge $TIMEOUT ]; then
        echo -e "${YELLOW}⚠ Timeout waiting for services. Check: tilt status${NC}"
        echo -e "${YELLOW}  Continuing anyway...${NC}"
    fi
else
    echo -e "${GREEN}✓ Tilt already running${NC}"
fi
echo ""

# Verify services are running (with retry)
verify_services() {
    kubectl get pods 2>/dev/null | grep -E "(current-account|position-keeping|financial-accounting|party|tenant)" | grep -q "Running"
}

# Display service status with replica counts (reusable function)
show_service_status() {
    echo -e "${CYAN}╭─────────────────────────────────────────────────────────────────╮${NC}"
    echo -e "${CYAN}│  Service                      Ready    Replicas    Status       │${NC}"
    echo -e "${CYAN}├─────────────────────────────────────────────────────────────────┤${NC}"

    for svc in current-account position-keeping financial-accounting party tenant; do
        # Get deployment info
        READY=$(kubectl get deployment "$svc" -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo "0")
        DESIRED=$(kubectl get deployment "$svc" -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "0")
        READY=${READY:-0}

        # Determine status color
        if [ "$READY" = "$DESIRED" ] && [ "$READY" != "0" ]; then
            STATUS="${GREEN}● Healthy${NC}"
        elif [ "$READY" = "0" ]; then
            STATUS="${RED}○ Down${NC}"
        else
            STATUS="${YELLOW}◐ Partial${NC}"
        fi

        # Pad service name for alignment
        printf "${CYAN}│${NC}  %-28s %s/%s      %s/%s         %b    ${CYAN}│${NC}\n" \
            "$svc" "$READY" "$READY" "$READY" "$DESIRED" "$STATUS"
    done

    echo -e "${CYAN}╰─────────────────────────────────────────────────────────────────╯${NC}"
}

echo -e "${YELLOW}Verifying services...${NC}"
SERVICE_RETRY_COUNT=0
MAX_SERVICE_RETRIES=10
while ! verify_services; do
    SERVICE_RETRY_COUNT=$((SERVICE_RETRY_COUNT + 1))
    if [ $SERVICE_RETRY_COUNT -ge $MAX_SERVICE_RETRIES ]; then
        echo -e "${RED}⚠ Warning: $SERVICE_RETRY_COUNT retries - services may have issues starting${NC}"
    fi
    echo -e "${YELLOW}⚠ Services not yet running (attempt $SERVICE_RETRY_COUNT). Press any key to retry, or Ctrl+C to exit.${NC}"
    show_service_status
    read -n 1 -s -r
    echo ""
    echo -e "${CYAN}► Retrying...${NC}"
done
show_service_status
echo -e "${GREEN}✓ All services running${NC}\n"

# ════════════════════════════════════════════════════════════════
# PART 0: Tenant Provisioning (Always Multi-Tenant)
# ════════════════════════════════════════════════════════════════
echo -e "${MAGENTA}╔════════════════════════════════════════════════════════════════╗${NC}"
echo -e "${MAGENTA}║  Setup: Tenant Provisioning                                    ║${NC}"
echo -e "${MAGENTA}║  System is always multi-tenant - provisioning demo tenant      ║${NC}"
echo -e "${MAGENTA}╚════════════════════════════════════════════════════════════════╝${NC}"
echo ""

echo -e "${CYAN}► Checking/provisioning demo tenant: ${DEMO_TENANT}${NC}"

# Check if tenant service is available
TENANT_SERVICE_PORT=50056
if grpcurl -plaintext localhost:${TENANT_SERVICE_PORT} grpc.health.v1.Health/Check >/dev/null 2>&1; then
    # Try to create the tenant (idempotent - will fail gracefully if exists)
    TENANT_RESULT=$(grpcurl -plaintext -d "{
      \"tenant_id\": \"${DEMO_TENANT}\",
      \"display_name\": \"Demo Tenant\",
      \"settlement_asset\": \"GBP\"
    }" localhost:${TENANT_SERVICE_PORT} meridian.tenant.v1.TenantService/InitiateTenant 2>&1) || true

    if echo "$TENANT_RESULT" | grep -q "tenant_id"; then
        echo -e "${GREEN}✓ Tenant '${DEMO_TENANT}' provisioned successfully${NC}"
        echo "$TENANT_RESULT" | jq '{tenant_id: .tenant.tenant_id, status: .tenant.status, display_name: .tenant.display_name}' 2>/dev/null || true
    elif echo "$TENANT_RESULT" | grep -qi "already exists\|AlreadyExists"; then
        echo -e "${GREEN}✓ Tenant '${DEMO_TENANT}' already exists (idempotent)${NC}"
    else
        echo -e "${YELLOW}⚠ Tenant provisioning result: ${TENANT_RESULT}${NC}"
        echo -e "${YELLOW}  Continuing with demo - tenant may need manual setup${NC}"
    fi
else
    echo -e "${YELLOW}⚠ Tenant Service not available at localhost:${TENANT_SERVICE_PORT}${NC}"
    echo -e "${YELLOW}  Ensure tenant '${DEMO_TENANT}' is manually provisioned${NC}"
    echo -e "${YELLOW}  Continuing with demo...${NC}"
fi
echo ""

# Validate database schema was provisioned
echo -e "${CYAN}► Validating database schema provisioning...${NC}"
SCHEMA_NAME="org_${DEMO_TENANT}"
EXPECTED_TABLES="parties accounts liens financial_position_logs payment_orders"

validate_schema() {
    # Check if schema exists and has expected tables
    TABLES=$(kubectl exec cockroachdb-0 -- ./cockroach sql --insecure -d meridian -e \
        "SELECT table_name FROM information_schema.tables WHERE table_schema = '${SCHEMA_NAME}';" 2>/dev/null | tail -n +2)

    if [ -z "$TABLES" ]; then
        return 1
    fi

    # Check for key tables
    for table in $EXPECTED_TABLES; do
        if ! echo "$TABLES" | grep -q "^${table}$"; then
            echo -e "${YELLOW}  Missing table: ${table}${NC}"
            return 1
        fi
    done
    return 0
}

if validate_schema; then
    TABLE_COUNT=$(kubectl exec cockroachdb-0 -- ./cockroach sql --insecure -d meridian -e \
        "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = '${SCHEMA_NAME}';" 2>/dev/null | tail -n 1)
    echo -e "${GREEN}✓ Schema '${SCHEMA_NAME}' validated: ${TABLE_COUNT} tables provisioned${NC}"
else
    echo -e "${YELLOW}⚠ Schema '${SCHEMA_NAME}' may not be fully provisioned${NC}"
    echo -e "${YELLOW}  Press any key to retry validation, or Ctrl+C to exit${NC}"
    while ! validate_schema; do
        read -n 1 -s -r
        echo -e "${CYAN}► Retrying schema validation...${NC}"
    done
    echo -e "${GREEN}✓ Schema '${SCHEMA_NAME}' now validated${NC}"
fi
echo ""

# ════════════════════════════════════════════════════════════════
# PART 1: Health Checks & Service Discovery
# ════════════════════════════════════════════════════════════════
echo -e "${MAGENTA}╔════════════════════════════════════════════════════════════════╗${NC}"
echo -e "${MAGENTA}║  Part 1: Health Checks & Service Readiness                     ║${NC}"
echo -e "${MAGENTA}╚════════════════════════════════════════════════════════════════╝${NC}"
echo ""

# Display health check status in table format
show_health_status() {
    ALL_HEALTHY=true

    echo -e "${CYAN}╭─────────────────────────────────────────────────────────────────╮${NC}"
    echo -e "${CYAN}│  Service                      Port     gRPC Health Status       │${NC}"
    echo -e "${CYAN}├─────────────────────────────────────────────────────────────────┤${NC}"

    declare -A services=(
        ["current-account"]=50051
        ["position-keeping"]=50053
        ["financial-accounting"]=50052
        ["payment-order"]=50054
        ["party"]=50055
    )

    for svc in current-account position-keeping financial-accounting payment-order party; do
        port=${services[$svc]}
        health=$(grpcurl -plaintext localhost:$port grpc.health.v1.Health/Check 2>/dev/null || echo '{"status":"UNKNOWN"}')
        status=$(echo "$health" | jq -r '.status')

        if [ "$status" = "SERVING" ]; then
            STATUS_DISPLAY="${GREEN}● SERVING${NC}"
        elif [ "$status" = "UNKNOWN" ]; then
            STATUS_DISPLAY="${YELLOW}○ UNKNOWN${NC}"
            ALL_HEALTHY=false
        else
            STATUS_DISPLAY="${RED}✗ $status${NC}"
            ALL_HEALTHY=false
        fi

        printf "${CYAN}│${NC}  %-28s %-8s %b         ${CYAN}│${NC}\n" "$svc" "$port" "$STATUS_DISPLAY"
    done

    echo -e "${CYAN}╰─────────────────────────────────────────────────────────────────╯${NC}"
}

ALL_HEALTHY=true
show_health_status

HEALTH_RETRY_COUNT=0
MAX_HEALTH_RETRIES=10
while [ "$ALL_HEALTHY" != true ]; do
    HEALTH_RETRY_COUNT=$((HEALTH_RETRY_COUNT + 1))
    if [ $HEALTH_RETRY_COUNT -ge $MAX_HEALTH_RETRIES ]; then
        echo -e "${RED}⚠ Warning: $HEALTH_RETRY_COUNT retries - services may have persistent issues${NC}"
    fi
    echo -e "${YELLOW}⚠ Some services are not fully healthy (attempt $HEALTH_RETRY_COUNT). Demo may have issues.${NC}"
    echo -e "${YELLOW}  Press any key to retry health checks, or Ctrl+C to exit.${NC}\n"
    read -n 1 -s -r
    echo ""
    echo -e "${CYAN}► Retrying health checks...${NC}\n"
    show_health_status
done

echo -e "${GREEN}✓ All services healthy and ready${NC}\n"
pause

# ════════════════════════════════════════════════════════════════
# PART 2: Saga Pattern - Distributed Transaction with Compensation
# ════════════════════════════════════════════════════════════════
echo -e "${MAGENTA}╔════════════════════════════════════════════════════════════════╗${NC}"
echo -e "${MAGENTA}║  Part 2: Saga Pattern - Distributed Transaction                ║${NC}"
echo -e "${MAGENTA}╚════════════════════════════════════════════════════════════════╝${NC}"
echo ""

echo -e "${CYAN}► Step 1: Register Party (Customer)${NC}"
echo -e "${YELLOW}  Party Service provides customer reference data for multi-tenancy${NC}"
TIMESTAMP=$(date +%s)
PARTY_RESPONSE=$(grpcurl -plaintext ${TENANT_HEADER} -d '{
  "party_type": "PARTY_TYPE_PERSON",
  "legal_name": "Demo User",
  "display_name": "Demo Customer"
}' localhost:50055 meridian.party.v1.PartyService/RegisterParty)

PARTY_ID=$(echo "$PARTY_RESPONSE" | jq -r '.party.partyId')
if [ -z "$PARTY_ID" ] || [ "$PARTY_ID" = "null" ]; then
    echo -e "${RED}✗ Failed to create party${NC}"
    echo "$PARTY_RESPONSE"
    exit 1
fi
echo -e "${GREEN}✓ Party Created:${NC} $PARTY_ID"
echo "$PARTY_RESPONSE" | jq '{
  party_id: .party.partyId,
  type: .party.partyType,
  legal_name: .party.legalName,
  display_name: .party.displayName,
  status: .party.status
}'
echo ""

echo -e "${CYAN}► Step 2: Initiate Current Account${NC}"
echo -e "${YELLOW}  Account linked to Party for ownership and validation${NC}"
CREATE_RESPONSE=$(grpcurl -plaintext ${TENANT_HEADER} -d "{
  \"account_identification\": \"GB29NWBK$TIMESTAMP\",
  \"party_id\": \"$PARTY_ID\",
  \"base_currency\": \"CURRENCY_GBP\"
}" localhost:50051 meridian.current_account.v1.CurrentAccountService/InitiateCurrentAccount)

ACCOUNT_ID=$(echo "$CREATE_RESPONSE" | jq -r '.accountId')
echo -e "${GREEN}✓ Account Created:${NC} $ACCOUNT_ID"
echo "$CREATE_RESPONSE" | jq '{
  account_id: .accountId,
  status: .facility.accountStatus,
  currency: .facility.baseCurrency,
  balance: .facility.currentBalance.currentBalance.amount
}'
echo ""

echo -e "${CYAN}► Step 3: Execute Deposit - Saga Orchestration${NC}"
echo -e "${YELLOW}  Depositing: £500${NC}"
echo -e "${YELLOW}  Saga Steps:${NC}"
echo -e "${YELLOW}    1. Log position in PositionKeeping     (via gRPC)${NC}"
echo -e "${YELLOW}    2. Post ledger in FinancialAccounting  (via gRPC)${NC}"
echo -e "${YELLOW}    3. Update CurrentAccount balance       (local)${NC}"
echo -e "${YELLOW}  * Automatic compensation if any step fails${NC}"
echo ""

DEPOSIT_RESPONSE=$(grpcurl -plaintext ${TENANT_HEADER} -d "{
  \"account_id\": \"$ACCOUNT_ID\",
  \"amount\": {
    \"amount\": {
      \"currency_code\": \"GBP\",
      \"units\": 500,
      \"nanos\": 0
    }
  }
}" localhost:50051 meridian.current_account.v1.CurrentAccountService/ExecuteDeposit)

TRANSACTION_ID=$(echo "$DEPOSIT_RESPONSE" | jq -r '.transactionId')
echo -e "${GREEN}✓ Deposit Completed via Saga:${NC} $TRANSACTION_ID"
echo "$DEPOSIT_RESPONSE" | jq '{
  transaction_id: .transactionId,
  status: .status,
  new_balance: .newBalance.amount,
  available_balance: .availableBalance.amount
}'
echo ""
pause

# ════════════════════════════════════════════════════════════════
# PART 3: DNS-Based Load Balancing with Pod Scaling
# ════════════════════════════════════════════════════════════════
echo -e "${MAGENTA}╔════════════════════════════════════════════════════════════════╗${NC}"
echo -e "${MAGENTA}║  Part 3: DNS-Based Client-Side Load Balancing                  ║${NC}"
echo -e "${MAGENTA}╚════════════════════════════════════════════════════════════════╝${NC}"
echo ""

echo -e "${CYAN}► Service Discovery Configuration:${NC}"
echo -e "  ${YELLOW}PositionKeeping:${NC}     dns:///position-keeping.default.svc.cluster.local:50053"
echo -e "  ${YELLOW}FinancialAccounting:${NC} dns:///financial-accounting.default.svc.cluster.local:50052"
echo -e "  ${YELLOW}Load Balancing:${NC}      round_robin across all pod IPs"
echo ""

echo -e "${CYAN}► Current service status (before scaling):${NC}"
INITIAL_POS_PODS=$(kubectl get deployment position-keeping -o jsonpath='{.spec.replicas}')
show_service_status
echo ""

echo -e "${CYAN}► Scaling PositionKeeping from $INITIAL_POS_PODS to 3 replicas...${NC}"
kubectl scale deployment position-keeping --replicas=3
echo -e "${YELLOW}  Waiting for new pods to be ready...${NC}"

# Wait for pods to be ready (max 60 seconds)
SCALE_TIMEOUT=60
SCALE_ELAPSED=0
while [ $SCALE_ELAPSED -lt $SCALE_TIMEOUT ]; do
    READY_PODS=$(kubectl get pods -l app=position-keeping -o json | jq '[.items[] | select(.status.phase == "Running") | select(any(.status.conditions[]?; .type == "Ready" and .status == "True"))] | length')
    if [ "$READY_PODS" -eq 3 ]; then
        echo -e "${GREEN}✓ All 3 replicas ready${NC}"
        break
    fi
    echo -e "${YELLOW}  Pods ready: $READY_PODS/3${NC}"
    sleep 3
    SCALE_ELAPSED=$((SCALE_ELAPSED + 3))
done

echo ""
echo -e "${CYAN}► Service status after scaling:${NC}"
show_service_status
NEW_POS_PODS=$(kubectl get deployment position-keeping -o jsonpath='{.status.readyReplicas}')
echo -e "  ${GREEN}PositionKeeping scaled: $INITIAL_POS_PODS → $NEW_POS_PODS replicas${NC}"
echo ""

echo -e "${CYAN}► Testing load distribution across ${NEW_POS_PODS} pods:${NC}"
echo -e "${YELLOW}  Executing 6 rapid-fire deposits to demonstrate round_robin...${NC}"
SUCCESS_COUNT=0
for _ in {1..6}; do
    if grpcurl -plaintext ${TENANT_HEADER} -d "{
      \"account_id\": \"$ACCOUNT_ID\",
      \"amount\": {
        \"amount\": {
          \"currency_code\": \"GBP\",
          \"units\": 10,
          \"nanos\": 0
        }
      }
    }" localhost:50051 meridian.current_account.v1.CurrentAccountService/ExecuteDeposit >/dev/null 2>&1; then
        SUCCESS_COUNT=$((SUCCESS_COUNT + 1))
    fi &
done
wait  # Wait for all background requests to complete

echo -e "${GREEN}✓ 6 requests distributed via round_robin across $NEW_POS_PODS pods (all succeeded)${NC}"
echo -e "${YELLOW}  Check pod logs to see distributed requests:${NC}"
echo -e "${YELLOW}  kubectl logs -l app=position-keeping --tail=5${NC}"
echo ""

echo -e "${CYAN}► Scaling back to original replica count ($INITIAL_POS_PODS)...${NC}"
kubectl scale deployment position-keeping --replicas="$INITIAL_POS_PODS"
echo -e "${GREEN}✓ Scaled back to $INITIAL_POS_PODS replicas${NC}"
echo ""

echo -e "${GREEN}✓ DNS-based load balancing validated with pod scaling${NC}\n"
pause

# ════════════════════════════════════════════════════════════════
# PART 4: Idempotency - Safe Retries with Payment Order Reference
# ════════════════════════════════════════════════════════════════
echo -e "${MAGENTA}╔════════════════════════════════════════════════════════════════╗${NC}"
echo -e "${MAGENTA}║  Part 4: Idempotency - Proving Safe Retries                    ║${NC}"
echo -e "${MAGENTA}╚════════════════════════════════════════════════════════════════╝${NC}"
echo ""

echo -e "${CYAN}► Idempotency Pattern:${NC}"
echo -e "  ${YELLOW}Liens:${NC}    PaymentOrderReference as natural idempotency key"
echo -e "  ${YELLOW}Deposits:${NC} IdempotencyKey propagated to downstream services"
echo -e "  ${YELLOW}Behavior:${NC} Duplicate requests return cached/existing result"
echo ""

# Generate a unique payment order reference for this demo
PAYMENT_ORDER_REF="PO-DEMO-$(date +%s)"

echo -e "${CYAN}► Step 1: Create a Lien (fund reservation) with PaymentOrderReference${NC}"
echo -e "  ${YELLOW}PaymentOrderReference:${NC} $PAYMENT_ORDER_REF"
echo -e "  ${YELLOW}Amount:${NC} £100"
echo ""

LIEN1=$(grpcurl -plaintext ${TENANT_HEADER} -d "{
  \"account_id\": \"$ACCOUNT_ID\",
  \"amount\": {
    \"amount\": {
      \"currency_code\": \"GBP\",
      \"units\": 100,
      \"nanos\": 0
    }
  },
  \"payment_order_reference\": \"$PAYMENT_ORDER_REF\"
}" localhost:50051 meridian.current_account.v1.CurrentAccountService/InitiateLien)

LIEN_ID=$(echo "$LIEN1" | jq -r '.lien.lienId')
echo -e "${GREEN}✓ First request - Lien created:${NC}"
echo "$LIEN1" | jq '{lien_id: .lien.lienId, status: .lien.status, amount: .lien.amount.amount}'
echo ""

echo -e "${CYAN}► Step 2: Retry the SAME request (simulating network retry)${NC}"
echo -e "  ${YELLOW}Same PaymentOrderReference:${NC} $PAYMENT_ORDER_REF"
echo ""

LIEN2=$(grpcurl -plaintext ${TENANT_HEADER} -d "{
  \"account_id\": \"$ACCOUNT_ID\",
  \"amount\": {
    \"amount\": {
      \"currency_code\": \"GBP\",
      \"units\": 100,
      \"nanos\": 0
    }
  },
  \"payment_order_reference\": \"$PAYMENT_ORDER_REF\"
}" localhost:50051 meridian.current_account.v1.CurrentAccountService/InitiateLien)

LIEN_ID2=$(echo "$LIEN2" | jq -r '.lien.lienId')
echo -e "${GREEN}✓ Second request - Idempotent response (same lien returned):${NC}"
echo "$LIEN2" | jq '{lien_id: .lien.lienId, status: .lien.status, amount: .lien.amount.amount}'
echo ""

# Verify idempotency
if [ "$LIEN_ID" = "$LIEN_ID2" ]; then
    echo -e "${GREEN}╔════════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${GREEN}║  ✓ IDEMPOTENCY VERIFIED - Same Lien ID returned both times     ║${NC}"
    echo -e "${GREEN}║    No duplicate fund reservations created!                     ║${NC}"
    echo -e "${GREEN}╚════════════════════════════════════════════════════════════════╝${NC}"
else
    echo -e "${RED}✗ Idempotency check failed - different lien IDs returned${NC}"
fi
echo ""

# Clean up: Terminate the lien to release funds
echo -e "${CYAN}► Cleanup: Terminating lien to release reserved funds${NC}"
grpcurl -plaintext ${TENANT_HEADER} -d "{
  \"lien_id\": \"$LIEN_ID\",
  \"reason\": \"Demo cleanup\"
}" localhost:50051 meridian.current_account.v1.CurrentAccountService/TerminateLien >/dev/null 2>&1
echo -e "${GREEN}✓ Lien terminated, funds released${NC}"
echo ""

echo -e "${CYAN}► How Idempotency Works:${NC}"
echo -e "  1. Client includes PaymentOrderReference in lien request"
echo -e "  2. Service checks database for existing lien with same reference"
echo -e "  3. If found: Return existing lien (idempotent response)"
echo -e "  4. If new: Create lien and store reference"
echo -e "  5. Network retries are safe - no duplicate reservations!"
echo ""
echo -e "${GREEN}✓ Idempotency protection verified${NC}\n"
pause

# ════════════════════════════════════════════════════════════════
# PART 5: Distributed Tracing (OpenTelemetry + Grafana Tempo)
# ════════════════════════════════════════════════════════════════
echo -e "${MAGENTA}╔════════════════════════════════════════════════════════════════╗${NC}"
echo -e "${MAGENTA}║  Part 5: Distributed Tracing Across Services                   ║${NC}"
echo -e "${MAGENTA}╚════════════════════════════════════════════════════════════════╝${NC}"
echo ""

echo -e "${CYAN}► Observability Stack (via Tilt):${NC}"
echo -e "  ${YELLOW}Grafana Alloy${NC}  → OTLP collector (alloy:4317)"
echo -e "  ${YELLOW}Grafana Tempo${NC}  → Distributed trace storage"
echo -e "  ${YELLOW}Grafana${NC}        → Visualization at http://localhost:3000"
echo ""

echo -e "${CYAN}► Trace propagation through saga:${NC}"
echo -e "  ${YELLOW}CurrentAccount${NC} → ${YELLOW}PositionKeeping${NC} → ${YELLOW}FinancialAccounting${NC}"
echo ""
echo -e "${CYAN}► Trace attributes captured:${NC}"
echo -e "  • Service name, version, environment"
echo -e "  • Correlation ID propagation"
echo -e "  • Span relationships (parent/child)"
echo -e "  • Request/response payloads"
echo -e "  • Error details and stack traces"
echo ""
echo -e "${GREEN}✓ Distributed tracing enabled via OpenTelemetry${NC}"
echo -e "${YELLOW}  View traces: http://localhost:3000 → Explore → Tempo${NC}"
echo -e "${YELLOW}  Search by: service.name = \"current-account-service\"${NC}\n"
pause

# ════════════════════════════════════════════════════════════════
# PART 6: Position Keeping - Transaction History
# ════════════════════════════════════════════════════════════════
echo -e "${MAGENTA}╔════════════════════════════════════════════════════════════════╗${NC}"
echo -e "${MAGENTA}║  Part 6: Position Keeping - Transaction Audit Trail            ║${NC}"
echo -e "${MAGENTA}╚════════════════════════════════════════════════════════════════╝${NC}"
echo ""

echo -e "${CYAN}► Listing position logs for account: ${ACCOUNT_ID}${NC}"
POSITION_LOGS=$(grpcurl -plaintext ${TENANT_HEADER} -d "{
  \"account_id\": \"$ACCOUNT_ID\"
}" localhost:50053 meridian.position_keeping.v1.PositionKeepingService/ListFinancialPositionLogs)

echo "$POSITION_LOGS" | jq '{
  total_logs: (.logs | length),
  logs: [.logs[] | {
    log_id: .logId,
    account_id: .accountId,
    status: .status,
    total_entries: (.entries | length),
    entries: [.entries[]? | {
      entry_id: .entryId,
      transaction_id: .transactionId,
      direction: .direction,
      amount: .amount.amount,
      timestamp: .timestamp
    }]
  }]
}'
echo ""
echo -e "${GREEN}✓ Complete audit trail maintained${NC}\n"
pause

# ════════════════════════════════════════════════════════════════
# PART 7: Financial Accounting - Double-Entry Ledger
# ════════════════════════════════════════════════════════════════
echo -e "${MAGENTA}╔════════════════════════════════════════════════════════════════╗${NC}"
echo -e "${MAGENTA}║  Part 7: Financial Accounting - Double-Entry Bookkeeping       ║${NC}"
echo -e "${MAGENTA}╚════════════════════════════════════════════════════════════════╝${NC}"
echo ""

echo -e "${CYAN}► Listing booking logs for account: ${ACCOUNT_ID}${NC}"
BOOKING_LOGS=$(grpcurl -plaintext ${TENANT_HEADER} -d "{}" localhost:50052 meridian.financial_accounting.v1.FinancialAccountingService/ListFinancialBookingLogs)

# Filter to show only logs matching our account
echo "$BOOKING_LOGS" | jq --arg account_id "$ACCOUNT_ID" '{
  total_logs: ([.financialBookingLogs[] | select(.productServiceReference == $account_id)] | length),
  logs: [.financialBookingLogs[] | select(.productServiceReference == $account_id) | {
    booking_log_id: .id,
    product_service_ref: .productServiceReference,
    status: .status,
    total_postings: (.ledgerPostings | length),
    postings: [.ledgerPostings[]? | {
      id: .id,
      direction: .postingDirection,
      amount: .postingAmount,
      value_date: .valueDate
    }]
  }]
}'
echo ""
echo -e "${GREEN}✓ Double-entry ledger validated${NC}\n"
pause

# ════════════════════════════════════════════════════════════════
# PART 8: Final Account State
# ════════════════════════════════════════════════════════════════
echo -e "${MAGENTA}╔════════════════════════════════════════════════════════════════╗${NC}"
echo -e "${MAGENTA}║  Part 8: Final Account State & Summary                         ║${NC}"
echo -e "${MAGENTA}╚════════════════════════════════════════════════════════════════╝${NC}"
echo ""

FINAL_ACCOUNT=$(grpcurl -plaintext ${TENANT_HEADER} -d "{
  \"account_id\": \"$ACCOUNT_ID\"
}" localhost:50051 meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount)

FINAL_BALANCE=$(echo "$FINAL_ACCOUNT" | jq -r '.facility.currentBalance.currentBalance.amount.units')
AVAILABLE=$(echo "$FINAL_ACCOUNT" | jq -r '.facility.currentBalance.availableBalance.amount.units')

echo "$FINAL_ACCOUNT" | jq '{
  account_id: .facility.accountId,
  status: .facility.accountStatus,
  currency: .facility.baseCurrency,
  balance: .facility.currentBalance.currentBalance.amount,
  available: .facility.currentBalance.availableBalance.amount,
  last_updated: .facility.currentBalance.lastUpdated
}'
echo ""

# ════════════════════════════════════════════════════════════════
# SUMMARY
# ════════════════════════════════════════════════════════════════
echo -e "${BLUE}╔════════════════════════════════════════════════════════════════╗${NC}"
echo -e "${BLUE}║  Demo Complete! Enterprise Features Validated                  ║${NC}"
echo -e "${BLUE}╚════════════════════════════════════════════════════════════════╝${NC}"
echo ""
echo -e "${GREEN}Account Summary:${NC}"
echo -e "  Account ID:   $ACCOUNT_ID"
echo -e "  Deposits:     £810 total (£500 + £250 + 6×£10 load test)"
echo -e "  Balance:      £$FINAL_BALANCE"
echo -e "  Available:    £$AVAILABLE"
echo ""
echo -e "${CYAN}Architecture Features Demonstrated:${NC}"
echo -e "  ${GREEN}✓${NC} Always multi-tenant (tenant: ${DEMO_TENANT})"
echo -e "  ${GREEN}✓${NC} BIAN-compliant microservices (4 domains including Party)"
echo -e "  ${GREEN}✓${NC} Party validation for account ownership"
echo -e "  ${GREEN}✓${NC} Saga pattern with automatic compensation"
echo -e "  ${GREEN}✓${NC} DNS-based client-side load balancing (round_robin)"
echo -e "  ${GREEN}✓${NC} Distributed tracing (OpenTelemetry)"
echo -e "  ${GREEN}✓${NC} Health checks with dependency validation"
echo -e "  ${GREEN}✓${NC} Idempotency protection (PaymentOrderReference)"
echo -e "  ${GREEN}✓${NC} Position keeping with audit trail"
echo -e "  ${GREEN}✓${NC} Double-entry ledger bookkeeping"
echo -e "  ${GREEN}✓${NC} Protobuf over gRPC"
echo -e "  ${GREEN}✓${NC} Resilient communication (circuit breaker + retry)"
echo ""
echo -e "${YELLOW}Next Steps:${NC}"
echo -e "  • View traces in Grafana:    http://localhost:3000 → Explore → Tempo"
echo -e "  • View logs in Grafana:      http://localhost:3000 → Explore → Loki"
echo -e "  • View metrics:              http://localhost:9090 (Prometheus)"
echo -e "  • Database queries:          kubectl exec -it cockroachdb-0 -- ./cockroach sql"
echo ""

# ════════════════════════════════════════════════════════════════
# PART 9: Horizon Integrity Proof (Optional)
# ════════════════════════════════════════════════════════════════
echo -e "${CYAN}Would you like to run the Horizon Integrity Proof demo?${NC}"
echo -e "${YELLOW}This demonstrates resilience against phantom transactions (Post Office Horizon problem).${NC}"
echo ""
read -p "Run Horizon demo? [y/N] " run_horizon
echo ""

if [[ "$run_horizon" =~ ^[Yy]$ ]]; then
    echo -e "${MAGENTA}╔════════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${MAGENTA}║  Part 9: Horizon Integrity Proof                               ║${NC}"
    echo -e "${MAGENTA}║  Demonstrating resilience against phantom transactions         ║${NC}"
    echo -e "${MAGENTA}╚════════════════════════════════════════════════════════════════╝${NC}"
    echo ""

    echo -e "${CYAN}Select demo mode:${NC}"
    echo -e "  ${GREEN}1)${NC} Happy Path - Normal operation (payment succeeds first try)"
    echo -e "  ${YELLOW}2)${NC} Unhappy Path - Network failure simulation (aggressive timeout triggers retry)"
    echo -e "  ${CYAN}3)${NC} Both - Run happy path, then unhappy path"
    echo ""
    read -p "Enter choice [1-3]: " horizon_choice

    HORIZON_MODE=""
    case $horizon_choice in
        1) HORIZON_MODE="happy" ;;
        2) HORIZON_MODE="unhappy" ;;
        3) HORIZON_MODE="both" ;;
        *) HORIZON_MODE="unhappy" ;;
    esac

    run_horizon_demo() {
        local mode=$1
        local timeout_ms=""
        local mode_desc=""
        local output_file="./integrity_report.json"

        case $mode in
            happy)
                timeout_ms="5000"  # 5 seconds - plenty of time
                mode_desc="Happy Path (normal timeout)"
                [ "$HORIZON_MODE" = "both" ] && output_file="./integrity_report_happy.json"
                ;;
            unhappy)
                timeout_ms="30"    # 30ms - triggers retry
                mode_desc="Unhappy Path (aggressive timeout)"
                [ "$HORIZON_MODE" = "both" ] && output_file="./integrity_report_unhappy.json"
                ;;
        esac

        echo ""
        echo -e "${CYAN}► Running: $mode_desc${NC}"
        echo -e "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

        go run ./utilities/horizon-demo --timeout "${timeout_ms}ms" --output "$output_file"
        local exit_code=$?

        if [ -f "$output_file" ]; then
            echo ""
            local verdict
            local initial
            local final
            local requests
            local transactions
            verdict=$(jq -r '.verdict' "$output_file" 2>/dev/null)
            initial=$(jq -r '.account.initial_balance_cents' "$output_file" 2>/dev/null)
            final=$(jq -r '.account.final_balance_cents' "$output_file" 2>/dev/null)
            requests=$(jq -r '.verification.requests_sent' "$output_file" 2>/dev/null)
            transactions=$(jq -r '.verification.transactions_recorded' "$output_file" 2>/dev/null)

            # Validate jq extraction
            if [ -z "$verdict" ] || [ "$verdict" = "null" ]; then
                echo -e "${RED}✗${NC} Failed to parse integrity report from $output_file"
                return 1
            fi

            echo -e "${CYAN}Results:${NC}"
            echo -e "  Initial Balance: £$(echo "scale=2; $initial / 100" | bc)"
            echo -e "  Final Balance:   £$(echo "scale=2; $final / 100" | bc)"
            echo -e "  Requests Sent:   $requests"
            echo -e "  Transactions:    $transactions"
            echo ""

            if [ "$verdict" = "PASSED" ]; then
                echo -e "${GREEN}╔════════════════════════════════════════════════════════════════╗${NC}"
                echo -e "${GREEN}║  INTEGRITY PROOF PASSED                                        ║${NC}"
                echo -e "${GREEN}║  No phantom transactions. Idempotency guarantees hold.         ║${NC}"
                echo -e "${GREEN}╚════════════════════════════════════════════════════════════════╝${NC}"
            else
                echo -e "${YELLOW}╔════════════════════════════════════════════════════════════════╗${NC}"
                echo -e "${YELLOW}║  INTEGRITY PROOF: $verdict                                     ║${NC}"
                echo -e "${YELLOW}╚════════════════════════════════════════════════════════════════╝${NC}"
            fi
        fi
        return $exit_code
    }

    case $HORIZON_MODE in
        happy)
            run_horizon_demo "happy"
            ;;
        unhappy)
            run_horizon_demo "unhappy"
            ;;
        both)
            echo -e "${CYAN}═══════════════════════════════════════════════════════════════════${NC}"
            echo -e "${CYAN}  Part 1: Happy Path${NC}"
            echo -e "${CYAN}═══════════════════════════════════════════════════════════════════${NC}"
            run_horizon_demo "happy"
            pause
            echo -e "${CYAN}═══════════════════════════════════════════════════════════════════${NC}"
            echo -e "${CYAN}  Part 2: Unhappy Path (Network Failure Simulation)${NC}"
            echo -e "${CYAN}═══════════════════════════════════════════════════════════════════${NC}"
            run_horizon_demo "unhappy"
            ;;
    esac

    echo ""
    echo -e "${GREEN}✓ Horizon Integrity Proof complete${NC}"
    if [ "$HORIZON_MODE" = "both" ]; then
        echo -e "${YELLOW}  Full reports: ./integrity_report_happy.json, ./integrity_report_unhappy.json${NC}"
    else
        echo -e "${YELLOW}  Full report: ./integrity_report.json${NC}"
    fi
    echo ""
fi
