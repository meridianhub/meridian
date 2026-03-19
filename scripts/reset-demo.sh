#!/usr/bin/env bash
# reset-demo.sh — Destroy and recreate the demo environment from scratch.
#
# This script:
#   1. Stops the meridian containers (keeps postgres running)
#   2. Drops and recreates all meridian per-service databases
#   3. Runs pre-migration scripts for PostgreSQL compatibility
#   4. Starts meridian and runs migrations
#   5. Seeds the demo tenant with fixture data via gRPC
#
# Usage:
#   ./scripts/reset-demo.sh
#   DEMO_HOST=user@custom-host ./scripts/reset-demo.sh
#
# Prerequisites:
#   - SSH config entry for the demo host (default alias: "meridian-demo")
#     Add to ~/.ssh/config:
#       Host meridian-demo
#           HostName <droplet-ip>
#           User root
#   - The demo stack must already be deployed (docker-compose.yml in /opt/meridian)
#
# Container names assume the docker-compose project name is "meridian"
# (derived from /opt/meridian directory name on the droplet).
# The postgres user is "meridian" (docker-compose default).

set -euo pipefail

DEMO_HOST="${DEMO_HOST:-meridian-demo}"
DEMO_DIR="/opt/meridian"
PG_USER="meridian"
PG_CONTAINER="meridian-postgres-1"
APP_CONTAINER="meridian-meridian-1"

# Verify SSH connectivity before proceeding
if ! ssh -o ConnectTimeout=5 "${DEMO_HOST}" "true" 2>/dev/null; then
  echo "ERROR: Cannot connect to ${DEMO_HOST}"
  echo ""
  echo "Ensure your ~/.ssh/config has an entry:"
  echo "  Host meridian-demo"
  echo "      HostName <droplet-ip>"
  echo "      User root"
  echo ""
  echo "Or override: DEMO_HOST=user@host ./scripts/reset-demo.sh"
  exit 1
fi

echo "=== Resetting Demo Environment on ${DEMO_HOST} ==="
echo ""
echo "WARNING: This will destroy all data in the demo database."
echo "Press Ctrl+C within 5 seconds to abort..."
sleep 5

echo ""
echo "=== Step 1: Stop meridian + mcp-server (keep postgres) ==="
ssh "${DEMO_HOST}" "cd ${DEMO_DIR} && docker compose stop meridian mcp-server"

echo ""
echo "=== Step 2: Drop and recreate databases ==="

# Terminate active connections to meridian_* databases
ssh "${DEMO_HOST}" "docker exec ${PG_CONTAINER} psql -U ${PG_USER} -d ${PG_USER} -c \"
  SELECT pg_terminate_backend(pid) FROM pg_stat_activity
  WHERE datname LIKE 'meridian_%' AND pid <> pg_backend_pid();
\" 2>/dev/null || true"

# Drop all meridian_* per-service databases
ssh "${DEMO_HOST}" "docker exec ${PG_CONTAINER} psql -U ${PG_USER} -d ${PG_USER} -tc \"
  SELECT datname FROM pg_database WHERE datname LIKE 'meridian_%' AND datname != 'meridian';
\" | while read -r db; do
  db=\$(echo \"\$db\" | xargs)
  [ -z \"\$db\" ] && continue
  echo \"  Dropping: \$db\"
  docker exec ${PG_CONTAINER} psql -U ${PG_USER} -d ${PG_USER} -c \"DROP DATABASE IF EXISTS \\\"\$db\\\";\"
done"

# Re-run init-databases.sql to recreate empty per-service databases
ssh "${DEMO_HOST}" "docker exec ${PG_CONTAINER} psql -U ${PG_USER} -d ${PG_USER} -f /docker-entrypoint-initdb.d/init-databases.sql"

echo ""
echo "=== Step 3: Run pre-migration scripts ==="
# PostgreSQL requires ALTER TABLE DROP CONSTRAINT for constraint-backed unique indexes.
# CockroachDB uses DROP INDEX CASCADE instead. This pre-migration resolves the divergence.
# See deploy/demo/pg-pre-migration.sql for details.
ssh "${DEMO_HOST}" "docker exec ${PG_CONTAINER} psql -U ${PG_USER} -d meridian_reference_data -c \"ALTER TABLE IF EXISTS public.platform_saga_definition DROP CONSTRAINT IF EXISTS uq_platform_saga_definition_name;\""

echo ""
echo "=== Step 4: Start meridian and run migrations ==="
ssh "${DEMO_HOST}" "cd ${DEMO_DIR} && docker compose up -d meridian"
sleep 3
ssh "${DEMO_HOST}" "docker exec ${APP_CONTAINER} /meridian --migrate"

echo ""
echo "=== Step 5: Restart meridian (post-migration) ==="
ssh "${DEMO_HOST}" "cd ${DEMO_DIR} && docker compose restart meridian"

echo ""
echo "=== Step 6: Seed demo tenant with fixtures ==="
# seed-dev has its own gateway health polling (60s timeout, 2s interval)
# so no sleep needed between restart and seed
ssh "${DEMO_HOST}" "docker exec ${APP_CONTAINER} /seed-dev \
  --gateway-url=http://localhost:8090 \
  --grpc-addr=localhost:50051 \
  --tenant-id=volterra_energy \
  --tenant-slug=volterra-energy \
  --display-name='Volterra Energy' \
  --subdomain=volterra-energy.demo.meridianhub.cloud \
  --manifest=/app/examples/manifests/volterra-energy-demo.json \
  --with-fixtures"

echo ""
echo "=== Step 7: Start mcp-server ==="
ssh "${DEMO_HOST}" "cd ${DEMO_DIR} && docker compose up -d mcp-server"

echo ""
echo "=== Demo Reset Complete ==="
echo "  Tenant:  volterra_energy (slug: volterra-energy)"
echo "  URL:     https://volterra-energy.demo.meridianhub.cloud"
echo ""
echo "  Verify:  ssh ${DEMO_HOST} 'docker logs ${APP_CONTAINER} --tail 10'"
