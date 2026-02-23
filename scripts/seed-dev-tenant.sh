#!/usr/bin/env bash
#
# Seed a dev tenant for local manual testing.
# Idempotent: exits 0 if tenant already exists.
#
# Usage:
#   ./scripts/seed-dev-tenant.sh                     # default: localhost:8090
#   GATEWAY_HOST=meridian:8090 ./scripts/seed-dev-tenant.sh  # inside docker network

set -euo pipefail

GATEWAY_HOST="${GATEWAY_HOST:-localhost:8090}"
GATEWAY_URL="http://${GATEWAY_HOST}"
MAX_RETRIES=30
RETRY_INTERVAL=2

echo "Waiting for gateway at ${GATEWAY_URL} ..."

for i in $(seq 1 "$MAX_RETRIES"); do
  if curl -sf "${GATEWAY_URL}/healthz" >/dev/null 2>&1; then
    echo "Gateway is healthy."
    break
  fi
  if [ "$i" -eq "$MAX_RETRIES" ]; then
    echo "ERROR: Gateway did not become healthy after $((MAX_RETRIES * RETRY_INTERVAL))s"
    exit 1
  fi
  sleep "$RETRY_INTERVAL"
done

echo "Creating dev tenant ..."

HTTP_CODE=$(curl -s -o /tmp/seed-response.json -w "%{http_code}" \
  -X POST "${GATEWAY_URL}/meridian.tenant.v1.TenantService/InitiateTenant" \
  -H "Content-Type: application/json" \
  -d '{
    "tenantId": "dev_tenant",
    "displayName": "Dev Tenant",
    "settlementAsset": "GBP",
    "subdomain": "dev-tenant"
  }')

BODY=$(cat /tmp/seed-response.json 2>/dev/null || echo "")

case "$HTTP_CODE" in
  200)
    echo "Dev tenant created successfully."
    ;;
  409)
    echo "Dev tenant already exists (idempotent, no action needed)."
    ;;
  *)
    # Connect protocol returns 200 even for AlreadyExists in some configs;
    # check the response body for the "already exists" code.
    if echo "$BODY" | grep -qi "already.exist"; then
      echo "Dev tenant already exists (idempotent, no action needed)."
    else
      echo "ERROR: Unexpected response (HTTP ${HTTP_CODE}):"
      echo "$BODY"
      exit 1
    fi
    ;;
esac

rm -f /tmp/seed-response.json
echo "Seed complete."
