#!/usr/bin/env bash
#
# Multi-Organization Demo - Organization Provisioning
# Provisions four demo organizations for the multi-tenancy demonstration:
# 1. meridian (control plane - Tenant Zero)
# 2. post_office (UK Post Office - fiat banking)
# 3. motive (Motive AI - GPU compute marketplace)
# 4. un_wfp (UN World Food Programme - humanitarian vouchers)
#
# This script is idempotent - safe to run multiple times

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# Configuration
ORGANIZATION_SERVICE_URL="${ORGANIZATION_SERVICE_URL:-dns:///organization.default.svc.cluster.local:50056}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

echo -e "${BLUE}╔════════════════════════════════════════════════════════════════╗${NC}"
echo -e "${BLUE}║  Multi-Organization Demo - Organization Provisioning           ║${NC}"
echo -e "${BLUE}╚════════════════════════════════════════════════════════════════╝${NC}"
echo ""

# Check prerequisites
check_prerequisites() {
    echo -e "${YELLOW}Checking prerequisites...${NC}"

    # Check if orgctl binary exists, if not build it
    if [ ! -f "${PROJECT_ROOT}/orgctl" ]; then
        echo -e "${YELLOW}  Building orgctl...${NC}"
        (cd "${PROJECT_ROOT}" && go build -o orgctl ./cmd/orgctl)
    fi

    if [ ! -x "${PROJECT_ROOT}/orgctl" ]; then
        echo -e "${RED}Error: orgctl is not executable${NC}"
        exit 1
    fi

    echo -e "${GREEN}✓ Prerequisites met${NC}"
    echo ""
}

# Wait for Organization Service to be ready
wait_for_organization_service() {
    echo -e "${YELLOW}Waiting for Organization Service to be ready...${NC}"

    local max_attempts=30
    local attempt=0

    while [ $attempt -lt $max_attempts ]; do
        # Try to list organizations (will fail if service not ready)
        if "${PROJECT_ROOT}/orgctl" list > /dev/null 2>&1; then
            echo -e "${GREEN}✓ Organization Service is ready${NC}"
            return 0
        fi

        attempt=$((attempt + 1))
        echo -e "${YELLOW}  Attempt ${attempt}/${max_attempts} - waiting 2 seconds...${NC}"
        sleep 2
    done

    echo -e "${RED}Error: Organization Service did not become ready in time${NC}"
    exit 1
}

# Register an organization (idempotent)
register_organization() {
    local id=$1
    local name=$2
    local asset=$3
    local subdomain=${4:-""}
    local metadata=${5:-""}

    echo -e "${CYAN}► Registering organization: ${id}${NC}"

    local cmd="${PROJECT_ROOT}/orgctl register --id=${id} --name=\"${name}\" --settlement-asset=${asset}"

    if [ -n "${subdomain}" ]; then
        cmd="${cmd} --subdomain=${subdomain}"
    fi

    if [ -n "${metadata}" ]; then
        cmd="${cmd} --metadata ${metadata}"
    fi

    # Execute the command
    if eval "${cmd}" 2>&1; then
        echo -e "${GREEN}  ✓ Organization '${id}' registered successfully${NC}"
    else
        # Check if it's an AlreadyExists error (which is fine for idempotency)
        if eval "${cmd}" 2>&1 | grep -q "AlreadyExists\|already exists"; then
            echo -e "${YELLOW}  ℹ Organization '${id}' already exists (idempotent)${NC}"
        else
            echo -e "${RED}  ✗ Failed to register organization '${id}'${NC}"
            return 1
        fi
    fi
    echo ""
}

# Main provisioning logic
provision_organizations() {
    echo -e "${BLUE}═══════════════════════════════════════════════════════════════════${NC}"
    echo -e "${BLUE}  Provisioning Demo Organizations${NC}"
    echo -e "${BLUE}═══════════════════════════════════════════════════════════════════${NC}"
    echo ""

    # Organization 0: Meridian Control Plane (Tenant Zero)
    # This is special - it hosts the control plane data and represents Meridian itself
    echo -e "${CYAN}Organization 0: Meridian Control Plane (Tenant Zero)${NC}"
    echo -e "  ${YELLOW}Purpose: Control plane infrastructure, billing, console${NC}"
    echo -e "  ${YELLOW}Note: Dogfooding pattern - Meridian runs on Meridian${NC}"
    register_organization \
        "meridian" \
        "Meridian Control Plane" \
        "USD" \
        "" \
        "tier=control-plane,is_control_plane=true"

    # Organization 1: UK Post Office (fiat banking)
    echo -e "${CYAN}Organization 1: UK Post Office (Fiat Banking)${NC}"
    echo -e "  ${YELLOW}Purpose: Traditional banking with GBP settlement${NC}"
    register_organization \
        "post_office" \
        "UK Post Office" \
        "GBP" \
        "" \
        "tier=enterprise,industry=banking"

    # Organization 2: Motive AI (GPU compute marketplace)
    echo -e "${CYAN}Organization 2: Motive AI (GPU Compute Marketplace)${NC}"
    echo -e "  ${YELLOW}Purpose: GPU compute trading with GPU-HOUR settlement${NC}"
    register_organization \
        "motive" \
        "Motive AI Compute" \
        "GPU-HOUR" \
        "" \
        "tier=enterprise,industry=compute"

    # Organization 3: UN World Food Programme (humanitarian vouchers)
    echo -e "${CYAN}Organization 3: UN World Food Programme (Humanitarian)${NC}"
    echo -e "  ${YELLOW}Purpose: Humanitarian aid with RICE-VOUCHER settlement${NC}"
    register_organization \
        "un_wfp" \
        "UN World Food Programme" \
        "RICE-VOUCHER" \
        "" \
        "tier=humanitarian,industry=aid"

    echo -e "${BLUE}═══════════════════════════════════════════════════════════════════${NC}"
    echo -e "${GREEN}✓ All organizations provisioned successfully${NC}"
    echo -e "${BLUE}═══════════════════════════════════════════════════════════════════${NC}"
    echo ""
}

# List all organizations
list_organizations() {
    echo -e "${BLUE}═══════════════════════════════════════════════════════════════════${NC}"
    echo -e "${BLUE}  Registered Organizations${NC}"
    echo -e "${BLUE}═══════════════════════════════════════════════════════════════════${NC}"
    echo ""

    "${PROJECT_ROOT}/orgctl" list
    echo ""
}

# Display summary
display_summary() {
    echo -e "${BLUE}╔════════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║  Organization Provisioning Complete                            ║${NC}"
    echo -e "${BLUE}╚════════════════════════════════════════════════════════════════╝${NC}"
    echo ""
    echo -e "${GREEN}Organizations provisioned:${NC}"
    echo -e "  ${CYAN}meridian${NC}     - Control Plane (Tenant Zero, USD)"
    echo -e "  ${CYAN}post_office${NC}  - UK Post Office (GBP)"
    echo -e "  ${CYAN}motive${NC}       - Motive AI (GPU-HOUR)"
    echo -e "  ${CYAN}un_wfp${NC}       - UN World Food Programme (RICE-VOUCHER)"
    echo ""
    echo -e "${YELLOW}Each organization has:${NC}"
    echo -e "  • Dedicated PostgreSQL schema: org_<organization_id>"
    echo -e "  • Isolated data access via JWT organization claims"
    echo -e "  • Independent settlement asset configuration"
    echo ""
    echo -e "${YELLOW}Next steps:${NC}"
    echo -e "  1. Run ./scripts/demo-seed-data.sh to create demo accounts"
    echo -e "  2. Run ./scripts/demo-cross-org-settlement.sh for cross-org demo"
    echo -e "  3. Access Grafana at http://localhost:3000 for org-scoped metrics"
    echo ""
}

# Main execution
main() {
    check_prerequisites
    wait_for_organization_service
    provision_organizations
    list_organizations
    display_summary
}

main "$@"
