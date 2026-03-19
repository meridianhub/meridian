#!/usr/bin/env bash
#
# Seed a dev tenant with manifest configuration.
# Delegates to the seed-dev Go binary (cmd/seed-dev).
#
# Usage:
#   ./scripts/seed-dev-tenant.sh                         # default: localhost
#   GATEWAY_HOST=meridian:8090 ./scripts/seed-dev-tenant.sh  # inside docker network
#   GATEWAY_URL=http://meridian:8090 ./scripts/seed-dev-tenant.sh
#   MANIFEST_PATH=examples/manifests/carbon.json ./scripts/seed-dev-tenant.sh
#   ./scripts/seed-dev-tenant.sh --skip-manifest         # tenant creation only
#
# Environment variables (all optional):
#   GATEWAY_URL      - HTTP URL for gateway health check (default: http://localhost:8090)
#   GRPC_ADDR        - gRPC server address (default: localhost:50051)
#   MANIFEST_PATH    - Path to manifest JSON file (default: examples/manifests/energy.json)
#   TENANT_ID        - Tenant ID to create (default: dev_tenant)
#   TENANT_SLUG      - Tenant URL slug (default: dev-tenant)
#   SEED_FIXTURES    - Set to "true" to seed demo fixture data (customers, accounts, etc.)
#
# Legacy variable support:
#   GATEWAY_HOST     - If set and GATEWAY_URL is not, derived as http://${GATEWAY_HOST}

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
BIN="${REPO_ROOT}/bin/seed-dev"

# Support the legacy GATEWAY_HOST variable used by docker-compose.
if [ -z "${GATEWAY_URL:-}" ] && [ -n "${GATEWAY_HOST:-}" ]; then
  export GATEWAY_URL="http://${GATEWAY_HOST}"
fi

# Build the binary if it does not exist.
if [ ! -f "${BIN}" ]; then
  echo "Building seed-dev binary..."
  (cd "${REPO_ROOT}" && go build -o bin/seed-dev ./cmd/seed-dev)
fi

FIXTURES_FLAG=""
if [ "${SEED_FIXTURES:-false}" = "true" ]; then
  FIXTURES_FLAG="--with-fixtures"
fi

exec "${BIN}" ${FIXTURES_FLAG} "$@"
