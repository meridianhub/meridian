#!/usr/bin/env bash
#
# Multi-Organization Demo - Validation Checklist
# Automated validation of multi-tenancy demo environment
#
# Validates:
# - Organization provisioning
# - Organization isolation (schema separation)
# - JWT authentication with organization claims
# - Service health and readiness
# - Observability stack with org-scoped metrics
# - Tilt environment health
#
# Prerequisites:
# - Tilt cluster running
# - Organizations provisioned
# - Seed data created

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# Configuration
CURRENT_ACCOUNT_URL="${CURRENT_ACCOUNT_URL:-localhost:50051}"
POSITION_KEEPING_URL="${POSITION_KEEPING_URL:-localhost:50053}"
FINANCIAL_ACCOUNTING_URL="${FINANCIAL_ACCOUNTING_URL:-localhost:50052}"
PAYMENT_ORDER_URL="${PAYMENT_ORDER_URL:-localhost:50054}"
KEYCLOAK_URL="${KEYCLOAK_URL:-http://localhost:18080}"
GRAFANA_URL="${GRAFANA_URL:-http://localhost:3000}"
PROMETHEUS_URL="${PROMETHEUS_URL:-http://localhost:9090}"
DATABASE_URL="${DATABASE_URL:-localhost:26257}"

# Track validation results
TOTAL_CHECKS=0
PASSED_CHECKS=0
FAILED_CHECKS=0
WARNINGS=0

# Demo organizations
ORGS=("meridian" "post_office" "motive" "un_wfp")

echo -e "${BLUE}╔════════════════════════════════════════════════════════════════╗${NC}"
echo -e "${BLUE}║  Multi-Organization Demo - Validation Suite                    ║${NC}"
echo -e "${BLUE}╚════════════════════════════════════════════════════════════════╝${NC}"
echo ""

# Check result helper
check_result() {
    local description=$1
    local result=$2  # 0 = pass, 1 = fail, 2 = warning

    TOTAL_CHECKS=$((TOTAL_CHECKS + 1))

    if [ "$result" -eq 0 ]; then
        PASSED_CHECKS=$((PASSED_CHECKS + 1))
        echo -e "  ${GREEN}✓${NC} ${description}"
    elif [ "$result" -eq 2 ]; then
        WARNINGS=$((WARNINGS + 1))
        echo -e "  ${YELLOW}⚠${NC} ${description}"
    else
        FAILED_CHECKS=$((FAILED_CHECKS + 1))
        echo -e "  ${RED}✗${NC} ${description}"
    fi
}

# Section header
section_header() {
    echo ""
    echo -e "${CYAN}═══════════════════════════════════════════════════════════════════${NC}"
    echo -e "${CYAN}  $1${NC}"
    echo -e "${CYAN}═══════════════════════════════════════════════════════════════════${NC}"
    echo ""
}

# 1. Service Health Checks
validate_service_health() {
    section_header "1. Service Health Checks"

    # Check current-account
    if grpcurl -plaintext "${CURRENT_ACCOUNT_URL}" grpc.health.v1.Health/Check > /dev/null 2>&1; then
        check_result "Current Account service (localhost:50051)" 0
    else
        check_result "Current Account service (localhost:50051)" 1
    fi

    # Check position-keeping
    if grpcurl -plaintext "${POSITION_KEEPING_URL}" grpc.health.v1.Health/Check > /dev/null 2>&1; then
        check_result "Position Keeping service (localhost:50053)" 0
    else
        check_result "Position Keeping service (localhost:50053)" 1
    fi

    # Check financial-accounting
    if grpcurl -plaintext "${FINANCIAL_ACCOUNTING_URL}" grpc.health.v1.Health/Check > /dev/null 2>&1; then
        check_result "Financial Accounting service (localhost:50052)" 0
    else
        check_result "Financial Accounting service (localhost:50052)" 1
    fi

    # Check payment-order
    if grpcurl -plaintext "${PAYMENT_ORDER_URL}" grpc.health.v1.Health/Check > /dev/null 2>&1; then
        check_result "Payment Order service (localhost:50054)" 0
    else
        check_result "Payment Order service (localhost:50054)" 1
    fi
}

# 2. Kubernetes Pod Status
validate_k8s_pods() {
    section_header "2. Kubernetes Pod Status"

    local services=("current-account" "position-keeping" "financial-accounting" "payment-order" "cockroachdb" "redis" "kafka" "keycloak")

    for svc in "${services[@]}"; do
        local pod_status
        pod_status=$(kubectl get pods -l "app=${svc}" -o jsonpath='{.items[0].status.phase}' 2>/dev/null || echo "NotFound")

        if [ "$pod_status" = "Running" ]; then
            check_result "${svc} pod running" 0
        elif [ "$pod_status" = "Pending" ]; then
            check_result "${svc} pod pending (may need time)" 2
        else
            check_result "${svc} pod status: ${pod_status}" 1
        fi
    done
}

# 3. Organization Provisioning
validate_organizations() {
    section_header "3. Organization Provisioning"

    local script_dir
    script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    local project_root="${script_dir}/.."

    # Check if orgctl exists
    if [ ! -f "${project_root}/orgctl" ]; then
        check_result "orgctl binary exists" 1
        return
    fi

    check_result "orgctl binary exists" 0

    # Try to list organizations
    local org_list
    org_list=$("${project_root}/orgctl" list 2>&1) || true

    for org in "${ORGS[@]}"; do
        if echo "$org_list" | grep -q "$org"; then
            check_result "Organization '${org}' provisioned" 0
        else
            check_result "Organization '${org}' provisioned" 1
        fi
    done
}

# 4. Organization Isolation
validate_isolation() {
    section_header "4. Organization Isolation (Schema Separation)"

    # Test that each tenant can only see their own data
    for org in "${ORGS[@]}"; do
        # Try to access an account that should exist in this tenant
        local result
        result=$(grpcurl -plaintext -H "x-tenant-id:${org}" \
            -d '{"account_id": "test-nonexistent"}' \
            "${CURRENT_ACCOUNT_URL}" \
            meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount 2>&1) || true

        # We expect "not found" - if we get "permission denied" or similar isolation error, that's also good
        if echo "$result" | grep -qi "not found\|denied\|does not exist"; then
            check_result "Organization '${org}' context isolation active" 0
        else
            check_result "Organization '${org}' context isolation active (unexpected response)" 2
        fi
    done

    # Cross-tenant access test
    echo ""
    echo -e "  ${YELLOW}Cross-tenant access test:${NC}"

    # Try to access post_office data with motive tenant context
    local cross_result
    cross_result=$(grpcurl -plaintext -H "x-tenant-id:motive" \
        -d '{"account_id": "po-customer-1"}' \
        "${CURRENT_ACCOUNT_URL}" \
        meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount 2>&1) || true

    if echo "$cross_result" | grep -qi "not found\|denied"; then
        check_result "Cross-org access blocked (motive → post_office)" 0
    else
        check_result "Cross-org access blocked (motive → post_office)" 1
    fi
}

# 5. Keycloak Configuration
validate_keycloak() {
    section_header "5. Keycloak Configuration"

    # Check Keycloak is responding
    if curl -sf "${KEYCLOAK_URL}/" > /dev/null 2>&1; then
        check_result "Keycloak accessible at localhost:18080" 0
    else
        check_result "Keycloak accessible at localhost:18080" 1
        return
    fi

    # Check meridian realm exists
    local realms
    realms=$(curl -sf "${KEYCLOAK_URL}/realms/meridian/.well-known/openid-configuration" 2>&1) || true

    if echo "$realms" | grep -q "meridian"; then
        check_result "Meridian realm configured" 0
    else
        check_result "Meridian realm configured" 2
    fi

    # Check we can get a token
    local token
    token=$(curl -sf -X POST "${KEYCLOAK_URL}/realms/meridian/protocol/openid-connect/token" \
        -d "grant_type=password" \
        -d "client_id=meridian-service" \
        -d "username=developer@meridian.local" \
        -d "password=developer" 2>/dev/null | jq -r '.access_token' 2>/dev/null) || true

    if [ -n "$token" ] && [ "$token" != "null" ]; then
        check_result "Test user can authenticate" 0
    else
        check_result "Test user can authenticate (may need keycloak-setup)" 2
    fi
}

# 6. Database Schema Validation
validate_database() {
    section_header "6. Database Schema Validation"

    # Check if we can connect to CockroachDB
    local schemas
    schemas=$(kubectl exec cockroachdb-0 -- ./cockroach sql --insecure \
        -e "SELECT schema_name FROM information_schema.schemata WHERE schema_name LIKE 'org_%';" 2>/dev/null) || true

    if [ -n "$schemas" ]; then
        for org in "${ORGS[@]}"; do
            if echo "$schemas" | grep -q "org_${org}"; then
                check_result "Schema 'org_${org}' exists" 0
            else
                check_result "Schema 'org_${org}' exists" 2
            fi
        done
    else
        check_result "Database schema query (requires kubectl exec access)" 2
    fi
}

# 7. Observability Stack
validate_observability() {
    section_header "7. Observability Stack"

    # Check Grafana
    if curl -sf "${GRAFANA_URL}/api/health" > /dev/null 2>&1; then
        check_result "Grafana accessible at localhost:3000" 0
    else
        check_result "Grafana accessible at localhost:3000" 1
    fi

    # Check Prometheus
    if curl -sf "${PROMETHEUS_URL}/-/ready" > /dev/null 2>&1; then
        check_result "Prometheus accessible at localhost:9090" 0
    else
        check_result "Prometheus accessible at localhost:9090" 1
    fi

    # Check for organization_id metric label
    local metrics
    metrics=$(curl -sf "${PROMETHEUS_URL}/api/v1/label/organization_id/values" 2>/dev/null) || true

    if echo "$metrics" | grep -q "data"; then
        check_result "Prometheus has organization_id label" 0
    else
        check_result "Prometheus has organization_id label (metrics may need time)" 2
    fi
}

# 8. Kafka Cluster
validate_kafka() {
    section_header "8. Kafka Cluster"

    # Check Kafka pods
    local kafka_pods
    kafka_pods=$(kubectl get pods -l app=kafka -o jsonpath='{.items[*].status.phase}' 2>/dev/null) || true

    local running_count
    running_count=$(echo "$kafka_pods" | tr ' ' '\n' | grep -c "Running" || echo "0")

    if [ "$running_count" -ge 3 ]; then
        check_result "Kafka cluster (3 brokers running)" 0
    elif [ "$running_count" -ge 1 ]; then
        check_result "Kafka cluster (${running_count}/3 brokers running)" 2
    else
        check_result "Kafka cluster (no brokers running)" 1
    fi

    # Check Kafka port forwarding
    if nc -z localhost 9092 2>/dev/null; then
        check_result "Kafka port forward (localhost:9092)" 0
    else
        check_result "Kafka port forward (localhost:9092)" 2
    fi
}

# 9. Tilt Environment
validate_tilt() {
    section_header "9. Tilt Environment"

    # Check Tilt is running
    if tilt get uisession > /dev/null 2>&1; then
        check_result "Tilt running" 0
    else
        check_result "Tilt running" 1
        return
    fi

    # Check Tilt UI
    if curl -sf "http://localhost:10350/api/view" > /dev/null 2>&1; then
        check_result "Tilt UI accessible (localhost:10350)" 0
    else
        check_result "Tilt UI accessible (localhost:10350)" 2
    fi
}

# 10. Demo Data Verification
validate_demo_data() {
    section_header "10. Demo Data Verification"

    # Check Post Office accounts
    local po_result
    po_result=$(grpcurl -plaintext -H "x-tenant-id:post_office" \
        -d '{"account_id": "po-customer-1"}' \
        "${CURRENT_ACCOUNT_URL}" \
        meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount 2>&1) || true

    if echo "$po_result" | grep -q "facility"; then
        check_result "Post Office demo accounts exist" 0
    else
        check_result "Post Office demo accounts exist (run demo-seed-data.sh)" 2
    fi

    # Check Meridian treasury
    local meridian_result
    meridian_result=$(grpcurl -plaintext -H "x-tenant-id:meridian" \
        -d '{"account_id": "meridian-treasury"}' \
        "${CURRENT_ACCOUNT_URL}" \
        meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount 2>&1) || true

    if echo "$meridian_result" | grep -q "facility"; then
        check_result "Meridian treasury account exists" 0
    else
        check_result "Meridian treasury account exists (run demo-seed-data.sh)" 2
    fi
}

# Summary
display_summary() {
    echo ""
    echo -e "${BLUE}╔════════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║  Validation Summary                                            ║${NC}"
    echo -e "${BLUE}╚════════════════════════════════════════════════════════════════╝${NC}"
    echo ""

    local pass_rate
    if [ "$TOTAL_CHECKS" -gt 0 ]; then
        pass_rate=$((PASSED_CHECKS * 100 / TOTAL_CHECKS))
    else
        pass_rate=0
    fi

    echo -e "  Total checks:  ${TOTAL_CHECKS}"
    echo -e "  ${GREEN}Passed:${NC}        ${PASSED_CHECKS}"
    echo -e "  ${RED}Failed:${NC}        ${FAILED_CHECKS}"
    echo -e "  ${YELLOW}Warnings:${NC}      ${WARNINGS}"
    echo -e "  Pass rate:     ${pass_rate}%"
    echo ""

    if [ "$FAILED_CHECKS" -eq 0 ] && [ "$WARNINGS" -eq 0 ]; then
        echo -e "${GREEN}╔════════════════════════════════════════════════════════════════╗${NC}"
        echo -e "${GREEN}║  ALL VALIDATIONS PASSED                                        ║${NC}"
        echo -e "${GREEN}╚════════════════════════════════════════════════════════════════╝${NC}"
    elif [ "$FAILED_CHECKS" -eq 0 ]; then
        echo -e "${YELLOW}╔════════════════════════════════════════════════════════════════╗${NC}"
        echo -e "${YELLOW}║  VALIDATION PASSED WITH WARNINGS                               ║${NC}"
        echo -e "${YELLOW}╚════════════════════════════════════════════════════════════════╝${NC}"
    else
        echo -e "${RED}╔════════════════════════════════════════════════════════════════╗${NC}"
        echo -e "${RED}║  VALIDATION FAILED                                             ║${NC}"
        echo -e "${RED}╚════════════════════════════════════════════════════════════════╝${NC}"
        echo ""
        echo -e "${YELLOW}Troubleshooting:${NC}"
        echo -e "  1. Ensure Tilt is running: tilt up"
        echo -e "  2. Wait for all pods to be ready: kubectl get pods"
        echo -e "  3. Run organization provisioning: ./scripts/demo-provision-organizations.sh"
        echo -e "  4. Run seed data: ./scripts/demo-seed-data.sh"
        echo -e "  5. Check Tilt UI for errors: http://localhost:10350"
    fi
    echo ""
}

# Main execution
main() {
    validate_service_health
    validate_k8s_pods
    validate_organizations
    validate_isolation
    validate_keycloak
    validate_database
    validate_observability
    validate_kafka
    validate_tilt
    validate_demo_data
    display_summary

    # Exit with appropriate code
    if [ "$FAILED_CHECKS" -gt 0 ]; then
        exit 1
    fi
}

main "$@"
