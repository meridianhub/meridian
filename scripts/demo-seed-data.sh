#!/usr/bin/env bash
#
# Multi-Organization Demo - Seed Data
# Creates demo accounts and balances for each organization:
# 1. Post Office: 5 customer accounts with GBP balances
# 2. Motive: 3 provider accounts with GPU_HOUR balances (non-fiat commodity)
# 3. UN WFP: 10 beneficiary accounts with RICE_VOUCHER balances (non-fiat voucher)
# 4. Meridian: 1 treasury account for control plane (USD)
#
# Requires: grpcurl, jq, running Tilt cluster with Keycloak configured
# Organizations must be provisioned first (./scripts/demo-provision-organizations.sh)
#
# This script is idempotent - safe to run multiple times (accounts use deterministic IDs)

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# Configuration
KEYCLOAK_URL="${KEYCLOAK_URL:-http://localhost:18080}"
CURRENT_ACCOUNT_URL="${CURRENT_ACCOUNT_URL:-localhost:50051}"
REALM_NAME="meridian"
CLIENT_ID="meridian-service"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo -e "${BLUE}╔════════════════════════════════════════════════════════════════╗${NC}"
echo -e "${BLUE}║  Multi-Organization Demo - Seed Data                           ║${NC}"
echo -e "${BLUE}╚════════════════════════════════════════════════════════════════╝${NC}"
echo ""

# Check prerequisites
check_prerequisites() {
    echo -e "${YELLOW}Checking prerequisites...${NC}"

    command -v grpcurl >/dev/null 2>&1 || { echo -e "${RED}Error: grpcurl required. Install: brew install grpcurl${NC}"; exit 1; }
    command -v jq >/dev/null 2>&1 || { echo -e "${RED}Error: jq required. Install: brew install jq${NC}"; exit 1; }
    command -v curl >/dev/null 2>&1 || { echo -e "${RED}Error: curl required${NC}"; exit 1; }

    echo -e "${GREEN}✓ Prerequisites met${NC}"
    echo ""
}

# Get Keycloak token for an organization
# In production, each organization would have its own Keycloak realm/client
# For demo, we use the same client with organization_id metadata
get_org_token() {
    local org_id=$1

    # For demo purposes, we use a simple test token mechanism
    # In production, each org would have dedicated OAuth clients
    echo -e "${CYAN}  Getting token for organization: ${org_id}${NC}"

    local token
    token=$(curl -sf -X POST "${KEYCLOAK_URL}/realms/${REALM_NAME}/protocol/openid-connect/token" \
        -d "grant_type=password" \
        -d "client_id=${CLIENT_ID}" \
        -d "username=developer@meridian.local" \
        -d "password=developer" \
        2>/dev/null | jq -r '.access_token')

    if [ -z "$token" ] || [ "$token" == "null" ]; then
        echo -e "${YELLOW}  Warning: Could not get Keycloak token, using demo mode${NC}"
        # Return empty - scripts will continue without auth
        echo ""
        return 0
    fi

    echo "$token"
}

# Wait for current-account service
wait_for_current_account() {
    echo -e "${YELLOW}Waiting for Current Account service...${NC}"

    local max_attempts=30
    local attempt=0

    while [ $attempt -lt $max_attempts ]; do
        if grpcurl -plaintext "${CURRENT_ACCOUNT_URL}" grpc.health.v1.Health/Check > /dev/null 2>&1; then
            echo -e "${GREEN}✓ Current Account service is ready${NC}"
            return 0
        fi

        attempt=$((attempt + 1))
        echo -e "${YELLOW}  Attempt ${attempt}/${max_attempts} - waiting 2 seconds...${NC}"
        sleep 2
    done

    echo -e "${RED}Error: Current Account service did not become ready${NC}"
    exit 1
}

# Create account and deposit funds (idempotent via deterministic IDs)
# For fiat currencies, pass instrument as "CURRENCY_GBP" etc.
# For non-fiat instruments, pass the instrument code directly (e.g., "KWH", "GPU_HOUR").
create_account_with_deposit() {
    local org_id=$1
    local account_id=$2
    local party_id=$3
    local iban=$4
    local instrument=$5
    local deposit_amount=$6
    local description=$7

    echo -e "${CYAN}  Creating account: ${account_id}${NC}"

    # Build metadata headers for tenant context
    local metadata_args=""
    if [ -n "$org_id" ]; then
        metadata_args="-H x-tenant-id:${org_id}"
    fi

    # Strip CURRENCY_ prefix if present (legacy convention from fiat-only era)
    local instrument_code="${instrument#CURRENCY_}"

    # Try to create account (will fail if exists - that's OK, idempotent)
    # Proto field is instrument_code for all asset types (fiat and non-fiat)
    local create_payload="{
        \"party_id\": \"${party_id}\",
        \"account_identification\": \"${iban}\",
        \"instrument_code\": \"${instrument_code}\"
    }"

    local create_result
    # shellcheck disable=SC2086 # metadata_args must word-split for grpcurl -H flag
    create_result=$(grpcurl -plaintext ${metadata_args} \
        -d "${create_payload}" \
        "${CURRENT_ACCOUNT_URL}" \
        meridian.current_account.v1.CurrentAccountService/InitiateCurrentAccount 2>&1) || true

    # Check if account was created or already exists
    if echo "$create_result" | grep -q "account_id"; then
        local created_id
        created_id=$(echo "$create_result" | jq -r '.account_id // .facility.account_id')
        echo -e "${GREEN}    ✓ Account created: ${created_id}${NC}"
    elif echo "$create_result" | grep -qi "already exists"; then
        echo -e "${YELLOW}    ℹ Account already exists (idempotent)${NC}"
    else
        echo -e "${YELLOW}    ⚠ Account creation result: ${create_result}${NC}"
    fi

    # Execute deposit
    if [ "$deposit_amount" -gt 0 ]; then
        echo -e "${CYAN}  Depositing ${deposit_amount} ${instrument_code}${NC}"

        # Extract account_id from create response or use the IBAN-derived ID
        local target_account_id
        target_account_id=$(echo "$create_result" | jq -r '.account_id // .facility.account_id // empty' 2>/dev/null || echo "")

        if [ -z "$target_account_id" ]; then
            echo -e "${YELLOW}    Using account lookup by IBAN pattern...${NC}"
            target_account_id="${account_id}"
        fi

        # Use input (InstrumentAmount) for all asset types - works for both fiat and non-fiat
        local deposit_payload="{
            \"account_id\": \"${target_account_id}\",
            \"input\": {
                \"amount\": \"${deposit_amount}\",
                \"instrument_code\": \"${instrument_code}\"
            },
            \"description\": \"${description}\",
            \"reference\": \"DEMO-SEED-${org_id}-${RANDOM}\"
        }"

        local deposit_result
        # shellcheck disable=SC2086 # metadata_args must word-split for grpcurl -H flag
        deposit_result=$(grpcurl -plaintext ${metadata_args} \
            -d "${deposit_payload}" \
            "${CURRENT_ACCOUNT_URL}" \
            meridian.current_account.v1.CurrentAccountService/ExecuteDeposit 2>&1) || true

        if echo "$deposit_result" | grep -q "transaction_id"; then
            local txn_id
            txn_id=$(echo "$deposit_result" | jq -r '.transaction_id')
            echo -e "${GREEN}    ✓ Deposit completed: ${txn_id}${NC}"
        else
            echo -e "${YELLOW}    ⚠ Deposit result: ${deposit_result}${NC}"
        fi
    fi
    echo ""
}

# Seed Post Office accounts (GBP)
seed_post_office() {
    echo -e "${BLUE}═══════════════════════════════════════════════════════════════════${NC}"
    echo -e "${BLUE}  Post Office - Creating 5 Customer Accounts (GBP)${NC}"
    echo -e "${BLUE}═══════════════════════════════════════════════════════════════════${NC}"
    echo ""

    local org_id="post_office"

    for i in {1..5}; do
        local acc_id="po-customer-${i}"
        local party_id="PO-CUST-$(printf '%05d' "$i")"
        local iban="GB82WEST1234500000$(printf '%03d' "$i")"

        create_account_with_deposit \
            "${org_id}" \
            "${acc_id}" \
            "${party_id}" \
            "${iban}" \
            "CURRENCY_GBP" \
            1000 \
            "Initial deposit for Post Office customer ${i}"
    done

    echo -e "${GREEN}✓ Post Office seeding complete: 5 accounts with £1,000 each${NC}"
    echo ""
}

# Seed Motive accounts (GPU_HOUR)
seed_motive() {
    echo -e "${BLUE}═══════════════════════════════════════════════════════════════════${NC}"
    echo -e "${BLUE}  Motive AI - Creating 3 Provider Accounts (GPU_HOUR)${NC}"
    echo -e "${BLUE}═══════════════════════════════════════════════════════════════════${NC}"
    echo ""

    local org_id="motive"

    for i in {1..3}; do
        local acc_id="motive-provider-${i}"
        local party_id="MOT-PROV-$(printf '%05d' "$i")"
        local iban="GB82MOTI1234500000$(printf '%03d' "$i")"

        create_account_with_deposit \
            "${org_id}" \
            "${acc_id}" \
            "${party_id}" \
            "${iban}" \
            "GPU_HOUR" \
            100 \
            "Initial GPU compute inventory for provider ${i} (100 GPU-hours)"
    done

    echo -e "${GREEN}✓ Motive seeding complete: 3 accounts with 100 GPU-hours each${NC}"
    echo ""
}

# Seed UN WFP accounts (RICE_VOUCHER)
seed_un_wfp() {
    echo -e "${BLUE}═══════════════════════════════════════════════════════════════════${NC}"
    echo -e "${BLUE}  UN WFP - Creating 10 Beneficiary Accounts (RICE_VOUCHER)${NC}"
    echo -e "${BLUE}═══════════════════════════════════════════════════════════════════${NC}"
    echo ""

    local org_id="un_wfp"

    for i in {1..10}; do
        local acc_id="wfp-beneficiary-${i}"
        local party_id="WFP-BEN-$(printf '%05d' "$i")"
        local iban="GB82UNWF1234500000$(printf '%03d' "$i")"

        # Balance of 1000 supports the cross-org settlement demo (500 voucher transaction).
        create_account_with_deposit \
            "${org_id}" \
            "${acc_id}" \
            "${party_id}" \
            "${iban}" \
            "RICE_VOUCHER" \
            1000 \
            "Initial rice voucher allocation for beneficiary ${i} (1000 vouchers)"
    done

    echo -e "${GREEN}✓ UN WFP seeding complete: 10 accounts with 1,000 vouchers each${NC}"
    echo ""
}

# Seed Meridian treasury (Control Plane)
seed_meridian() {
    echo -e "${BLUE}═══════════════════════════════════════════════════════════════════${NC}"
    echo -e "${BLUE}  Meridian Control Plane - Creating Treasury Account (USD)${NC}"
    echo -e "${BLUE}═══════════════════════════════════════════════════════════════════${NC}"
    echo ""

    local org_id="meridian"

    create_account_with_deposit \
        "${org_id}" \
        "meridian-treasury" \
        "MRD-TREASURY-00001" \
        "GB82MERD1234500000001" \
        "CURRENCY_USD" \
        1000000 \
        "Meridian control plane treasury - initial funding"

    echo -e "${GREEN}✓ Meridian seeding complete: Treasury with $1,000,000${NC}"
    echo ""
}

# Display summary
display_summary() {
    echo -e "${BLUE}╔════════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║  Seed Data Complete                                            ║${NC}"
    echo -e "${BLUE}╚════════════════════════════════════════════════════════════════╝${NC}"
    echo ""
    echo -e "${GREEN}Accounts created:${NC}"
    echo -e "  ${CYAN}post_office${NC}  - 5 customer accounts × £1,000 = £5,000 total"
    echo -e "  ${CYAN}motive${NC}       - 3 provider accounts × 100 GPU-hours = 300 GPU-hours"
    echo -e "  ${CYAN}un_wfp${NC}       - 10 beneficiary accounts × 1,000 vouchers = 10,000 vouchers"
    echo -e "  ${CYAN}meridian${NC}     - 1 treasury account = \$1,000,000"
    echo ""
    echo -e "${YELLOW}Data Isolation:${NC}"
    echo -e "  • Each tenant's accounts are isolated in separate schemas"
    echo -e "  • x-tenant-id header (or JWT claim) determines which schema is accessed"
    echo -e "  • Cross-tenant queries are blocked by design"
    echo ""
    echo -e "${YELLOW}Next steps:${NC}"
    echo -e "  1. Run ./scripts/demo-cross-org-settlement.sh for cross-org demo"
    echo -e "  2. Run ./scripts/demo-validation.sh to verify isolation"
    echo -e "  3. Check Grafana at http://localhost:3000 for org-scoped metrics"
    echo ""
    echo -e "${YELLOW}Verify accounts via grpcurl:${NC}"
    echo -e "  grpcurl -plaintext -H 'x-tenant-id:post_office' \\"
    echo -e "    -d '{\"account_id\":\"...\"}' \\"
    echo -e "    localhost:50051 meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount"
    echo ""
}

# Main execution
main() {
    check_prerequisites
    wait_for_current_account

    seed_meridian
    seed_post_office
    seed_motive
    seed_un_wfp

    display_summary
}

main "$@"
