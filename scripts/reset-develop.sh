#!/usr/bin/env bash
# reset-develop.sh — Destroy and recreate the develop environment from scratch.
#
# This script:
#   1. Pulls the latest develop Docker image
#   2. Stops the meridian-develop and mcp-server-develop containers (keeps postgres running)
#   3. Drops and recreates all dev_ per-service databases
#   4. Runs pre-migration scripts for PostgreSQL compatibility
#   5. Starts meridian-develop and runs migrations
#   6. Restarts meridian-develop post-migration
#   7. Seeds the develop tenant with fixture data via gRPC
#   8. Starts mcp-server-develop
#   9. Verifies the environment is healthy
#
# Usage:
#   ./scripts/reset-develop.sh
#   DEVELOP_HOST=user@custom-host ./scripts/reset-develop.sh
#   NO_CONFIRM=1 ./scripts/reset-develop.sh          # skip confirmation (for CI)
#
# Prerequisites:
#   - SSH config entry for the develop host (default alias: "meridian-develop-host")
#     Add to ~/.ssh/config:
#       Host meridian-develop-host
#           HostName <droplet-ip>
#           User deploy
#   - The develop stack must already be deployed (docker-compose.develop.yml in /opt/meridian-develop)
#
# Container names use custom container_name values defined in docker-compose.develop.yml.
# The develop stack has its own postgres container (postgres-develop).

set -euo pipefail

DEVELOP_HOST="${DEVELOP_HOST:-meridian-develop-host}"
DEVELOP_DIR="/opt/meridian-develop"
PG_USER="${PG_USER:-meridian}"
PG_DB="${PG_DB:-meridian}"
PG_CONTAINER="postgres-develop"
APP_CONTAINER="meridian-develop"
COMPOSE_CMD="docker compose -f docker-compose.develop.yml"

# Verify SSH connectivity before proceeding
if ! ssh -o ConnectTimeout=5 -o BatchMode=yes "${DEVELOP_HOST}" "true" 2>/dev/null; then
  echo "ERROR: Cannot connect to ${DEVELOP_HOST}"
  echo ""
  echo "Ensure your ~/.ssh/config has an entry:"
  echo "  Host meridian-develop-host"
  echo "      HostName <droplet-ip>"
  echo "      User deploy"
  echo ""
  echo "Or override: DEVELOP_HOST=user@host ./scripts/reset-develop.sh"
  exit 1
fi

echo "=== Resetting Develop Environment on ${DEVELOP_HOST} ==="
echo ""

if [ "${NO_CONFIRM:-0}" != "1" ]; then
  echo "WARNING: This will destroy all data in the develop database."
  echo "Press Ctrl+C within 5 seconds to abort..."
  sleep 5
fi

echo ""
echo "=== Step 1: Pull latest develop image ==="
ssh "${DEVELOP_HOST}" "cd ${DEVELOP_DIR} && ${COMPOSE_CMD} pull meridian-develop mcp-server-develop"

echo ""
echo "=== Step 2: Stop meridian-develop + mcp-server-develop (keep postgres) ==="
ssh "${DEVELOP_HOST}" "cd ${DEVELOP_DIR} && ${COMPOSE_CMD} stop meridian-develop mcp-server-develop"

echo ""
echo "=== Step 3: Drop and recreate databases ==="

# Terminate active connections to meridian_* databases
# (develop has its own postgres, so all meridian_* databases belong to us)
ssh "${DEVELOP_HOST}" "docker exec ${PG_CONTAINER} psql -U ${PG_USER} -d ${PG_DB} -c \"
  SELECT pg_terminate_backend(pid) FROM pg_stat_activity
  WHERE datname LIKE 'meridian\\_%' ESCAPE '\\' AND pid <> pg_backend_pid();
\" 2>/dev/null || true"

# Drop all meridian_* per-service databases
ssh "${DEVELOP_HOST}" "docker exec ${PG_CONTAINER} psql -U ${PG_USER} -d ${PG_DB} -tc \"
  SELECT datname FROM pg_database WHERE datname LIKE 'meridian\\_%' ESCAPE '\\' AND datname != 'meridian';
\" | while read -r db; do
  db=\$(echo \"\$db\" | xargs)
  [ -z \"\$db\" ] && continue
  echo \"  Dropping: \$db\"
  docker exec ${PG_CONTAINER} psql -U ${PG_USER} -d ${PG_DB} -c \"DROP DATABASE IF EXISTS \\\"\$db\\\";\"
done"

# Re-run init-databases to recreate empty per-service databases
ssh "${DEVELOP_HOST}" "docker exec ${PG_CONTAINER} psql -U ${PG_USER} -d ${PG_DB} -f /docker-entrypoint-initdb.d/init-databases.sql"

echo ""
echo "=== Step 4: Run pre-migration scripts ==="
# PostgreSQL requires ALTER TABLE DROP CONSTRAINT for constraint-backed unique indexes.
# CockroachDB uses DROP INDEX CASCADE instead. This pre-migration resolves the divergence.
# See deploy/demo/pg-pre-migration.sql for details.
ssh "${DEVELOP_HOST}" "docker exec ${PG_CONTAINER} psql -U ${PG_USER} -d meridian_reference_data -c \"ALTER TABLE IF EXISTS public.platform_saga_definition DROP CONSTRAINT IF EXISTS uq_platform_saga_definition_name;\""

echo ""
echo "=== Step 5: Start meridian-develop and run migrations ==="
ssh "${DEVELOP_HOST}" "cd ${DEVELOP_DIR} && ${COMPOSE_CMD} up -d meridian-develop"

# Wait for meridian-develop container to start before running migrations
attempt=0
until [ $attempt -ge 20 ]; do
  attempt=$((attempt + 1))
  if ssh "${DEVELOP_HOST}" "docker inspect --format '{{.State.Running}}' ${APP_CONTAINER} 2>/dev/null" | grep -q "true"; then
    echo "  meridian-develop container running after ${attempt} attempts"
    break
  fi
  if [ $attempt -ge 20 ]; then
    echo "  meridian-develop container failed to start"
    ssh "${DEVELOP_HOST}" "cd ${DEVELOP_DIR} && ${COMPOSE_CMD} logs meridian-develop --tail=20"
    exit 1
  fi
  sleep 3
done

ssh "${DEVELOP_HOST}" "docker exec ${APP_CONTAINER} /meridian --migrate"

echo ""
echo "=== Step 6: Restart meridian-develop (post-migration) ==="
ssh "${DEVELOP_HOST}" "cd ${DEVELOP_DIR} && ${COMPOSE_CMD} restart meridian-develop"

echo ""
echo "=== Step 7: Seed develop tenant with fixtures ==="
# seed-dev has its own gateway health polling (2-minute timeout, 2s interval)
# so no extra wait needed between restart and seed
ssh "${DEVELOP_HOST}" "docker exec ${APP_CONTAINER} /seed-dev \
  --gateway-url=http://localhost:8090 \
  --grpc-addr=localhost:50051 \
  --tenant-id=volterra_energy \
  --tenant-slug=volterra-energy \
  --display-name='Volterra Energy' \
  --subdomain=volterra-energy.develop.meridianhub.cloud \
  --manifest=/app/examples/manifests/volterra-energy-demo.json \
  --with-fixtures"

echo ""
echo "=== Step 8: Start mcp-server-develop ==="
ssh "${DEVELOP_HOST}" "cd ${DEVELOP_DIR} && ${COMPOSE_CMD} up -d mcp-server-develop"

echo ""
echo "=== Step 9: Health check ==="
attempt=0
until [ $attempt -ge 24 ]; do
  attempt=$((attempt + 1))
  if ssh "${DEVELOP_HOST}" "curl -sf --connect-timeout 2 --max-time 5 http://localhost:80/healthz -H 'Host: develop.meridianhub.cloud'" > /dev/null 2>&1; then
    echo "  Health check passed after ${attempt} attempts"
    break
  fi
  if [ $attempt -ge 24 ]; then
    echo "  Health check failed after ${attempt} attempts"
    ssh "${DEVELOP_HOST}" "cd ${DEVELOP_DIR} && ${COMPOSE_CMD} logs --tail=30"
    exit 1
  fi
  sleep 5
done

echo ""
echo "=== Develop Reset Complete ==="
echo "  Tenant:  volterra_energy (slug: volterra-energy)"
echo "  URL:     https://volterra-energy.develop.meridianhub.cloud"
echo ""
echo "  Verify:  ssh ${DEVELOP_HOST} 'docker logs ${APP_CONTAINER} --tail 10'"
