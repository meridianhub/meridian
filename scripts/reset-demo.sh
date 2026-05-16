#!/usr/bin/env bash
# reset-demo.sh — Destroy and recreate the demo environment from scratch.
#
# This script:
#   1. Pulls the latest demo Docker image
#   2. Stops the meridian containers (keeps postgres running)
#   3. Drops and recreates all meridian per-service databases
#   4. Runs pre-migration scripts for PostgreSQL compatibility
#   5. Starts meridian and runs migrations
#   6. Seeds the demo tenant with fixture data via gRPC
#   7. Verifies the environment is healthy
#
# Usage:
#   ./scripts/reset-demo.sh
#   DEMO_HOST=user@custom-host ./scripts/reset-demo.sh
#   NO_CONFIRM=1 ./scripts/reset-demo.sh          # skip confirmation (for CI)
#
# Prerequisites:
#   - SSH config entry for the demo host (default alias: "meridian-demo")
#     Add to ~/.ssh/config:
#       Host meridian-demo
#           HostName <droplet-ip>
#           User deploy
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
if ! ssh -o ConnectTimeout=5 -o BatchMode=yes "${DEMO_HOST}" "true" 2>/dev/null; then
  echo "ERROR: Cannot connect to ${DEMO_HOST}"
  echo ""
  echo "Ensure your ~/.ssh/config has an entry:"
  echo "  Host meridian-demo"
  echo "      HostName <droplet-ip>"
  echo "      User deploy"
  echo ""
  echo "Or override: DEMO_HOST=user@host ./scripts/reset-demo.sh"
  exit 1
fi

echo "=== Resetting Demo Environment on ${DEMO_HOST} ==="
echo ""

if [ "${NO_CONFIRM:-0}" != "1" ]; then
  echo "WARNING: This will destroy all data in the demo database."
  echo "Press Ctrl+C within 5 seconds to abort..."
  sleep 5
fi

echo ""
echo "=== Step 1: Pull latest demo image ==="
ssh "${DEMO_HOST}" "cd ${DEMO_DIR} && docker compose pull meridian"

echo ""
echo "=== Step 2: Stop meridian (keep postgres) ==="
ssh "${DEMO_HOST}" "cd ${DEMO_DIR} && docker compose stop meridian"

echo ""
echo "=== Step 3: Drop and recreate databases ==="

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
echo "=== Step 4: Run pre-migration scripts ==="
# PostgreSQL requires ALTER TABLE DROP CONSTRAINT for constraint-backed unique indexes.
# CockroachDB uses DROP INDEX CASCADE instead. This pre-migration resolves the divergence.
# See deploy/demo/pg-pre-migration.sql for details.
ssh "${DEMO_HOST}" "docker exec ${PG_CONTAINER} psql -U ${PG_USER} -d meridian_reference_data -c \"ALTER TABLE IF EXISTS public.platform_saga_definition DROP CONSTRAINT IF EXISTS uq_platform_saga_definition_name;\""

echo ""
echo "=== Step 5: Start meridian and run migrations ==="
ssh "${DEMO_HOST}" "cd ${DEMO_DIR} && docker compose up -d meridian"

# Wait for meridian container to start before running migrations
attempt=0
until [ $attempt -ge 20 ]; do
  attempt=$((attempt + 1))
  if ssh "${DEMO_HOST}" "docker inspect --format '{{.State.Running}}' ${APP_CONTAINER} 2>/dev/null" | grep -q "true"; then
    echo "  meridian container running after ${attempt} attempts"
    break
  fi
  if [ $attempt -ge 20 ]; then
    echo "  meridian container failed to start"
    ssh "${DEMO_HOST}" "cd ${DEMO_DIR} && docker compose logs meridian --tail=20"
    exit 1
  fi
  sleep 3
done

ssh "${DEMO_HOST}" "docker exec ${APP_CONTAINER} /meridian --migrate"

echo ""
echo "=== Step 6: Restart meridian (post-migration) ==="
ssh "${DEMO_HOST}" "cd ${DEMO_DIR} && docker compose restart meridian"

echo ""
echo "=== Step 7: Seed demo tenant with fixtures ==="
# seed-dev has its own gateway health polling (2-minute timeout, 2s interval)
# so no extra wait needed between restart and seed
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
echo "=== Step 7b: Seed PAYG Energy demo tenant ==="
ssh "${DEMO_HOST}" "docker exec ${APP_CONTAINER} /seed-dev \
  --gateway-url=http://localhost:8090 \
  --grpc-addr=localhost:50051 \
  --tenant-id=payg_energy \
  --tenant-slug=payg-energy \
  --display-name='PAYG Energy' \
  --subdomain=payg-energy.demo.meridianhub.cloud \
  --manifest=/app/examples/manifests/payg-energy.manifest.json \
  --with-fixtures"

echo ""
echo "=== Step 8: Health check ==="
attempt=0
until [ $attempt -ge 24 ]; do
  attempt=$((attempt + 1))
  if ssh "${DEMO_HOST}" "curl -sf http://localhost:80/healthz -H 'Host: demo.meridianhub.cloud'" > /dev/null 2>&1; then
    echo "  Health check passed after ${attempt} attempts"
    break
  fi
  if [ $attempt -ge 24 ]; then
    echo "  Health check failed after ${attempt} attempts"
    ssh "${DEMO_HOST}" "cd ${DEMO_DIR} && docker compose logs --tail=30"
    exit 1
  fi
  sleep 5
done

echo ""
echo "=== Demo Reset Complete ==="
echo "  Tenant:  volterra_energy (slug: volterra-energy)"
echo "  URL:     https://volterra-energy.demo.meridianhub.cloud"
echo ""
echo "  Tenant:  payg_energy (slug: payg-energy)"
echo "  URL:     https://payg-energy.demo.meridianhub.cloud"
echo ""
echo "  Verify:  ssh ${DEMO_HOST} 'docker logs ${APP_CONTAINER} --tail 10'"
