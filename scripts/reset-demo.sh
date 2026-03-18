#!/usr/bin/env bash
# reset-demo.sh — Destroy and recreate the demo environment from scratch.
#
# This script:
#   1. Stops the meridian containers (keeps postgres running)
#   2. Drops and recreates all meridian per-service databases
#   3. Restarts meridian and runs migrations
#   4. Runs seed-dev with fixtures to provision tenant + demo data
#
# Usage:
#   ./scripts/reset-demo.sh
#   DEMO_HOST=root@your-host ./scripts/reset-demo.sh
#
# Prerequisites:
#   - SSH access to the demo droplet
#   - The demo stack must already be deployed (docker-compose.yml in /opt/meridian)
#
# Container names assume the docker-compose project name is "meridian"
# (derived from /opt/meridian directory name on the droplet).

set -euo pipefail

DEMO_HOST="${DEMO_HOST:-root@68.183.40.239}"
DEMO_DIR="/opt/meridian"

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
ssh "${DEMO_HOST}" "docker exec meridian-postgres-1 psql -U meridian -d meridian -c \"
  SELECT pg_terminate_backend(pid) FROM pg_stat_activity
  WHERE datname LIKE 'meridian_%' AND pid <> pg_backend_pid();
\" 2>/dev/null || true"

# Drop all meridian_* per-service databases
ssh "${DEMO_HOST}" "docker exec meridian-postgres-1 psql -U meridian -d meridian -tc \"
  SELECT datname FROM pg_database WHERE datname LIKE 'meridian_%' AND datname != 'meridian';
\" | while read -r db; do
  db=\$(echo \"\$db\" | xargs)
  [ -z \"\$db\" ] && continue
  echo \"  Dropping: \$db\"
  docker exec meridian-postgres-1 psql -U meridian -d meridian -c \"DROP DATABASE IF EXISTS \\\"\$db\\\";\"
done"

# Re-run init-databases.sql to recreate empty per-service databases
ssh "${DEMO_HOST}" "docker exec meridian-postgres-1 psql -U meridian -d meridian -f /docker-entrypoint-initdb.d/init-databases.sql"

echo ""
echo "=== Step 3: Start meridian and run migrations ==="
ssh "${DEMO_HOST}" "cd ${DEMO_DIR} && docker compose up -d meridian"
sleep 3
ssh "${DEMO_HOST}" "docker exec meridian-meridian-1 /meridian --migrate"

echo ""
echo "=== Step 4: Restart meridian (post-migration) ==="
ssh "${DEMO_HOST}" "cd ${DEMO_DIR} && docker compose restart meridian"

echo ""
echo "=== Step 5: Seed demo tenant with fixtures ==="
# seed-dev has its own gateway health polling (60s timeout, 2s interval)
# so no sleep needed between restart and seed
ssh "${DEMO_HOST}" "docker exec meridian-meridian-1 /seed-dev \
  --gateway-url=http://localhost:8090 \
  --grpc-addr=localhost:50051 \
  --tenant-id=volterra_energy \
  --tenant-slug=volterra-energy \
  --display-name='Volterra Energy' \
  --subdomain=volterra-energy.demo.meridianhub.cloud \
  --manifest=/app/examples/manifests/volterra-energy-demo.json \
  --with-fixtures"

echo ""
echo "=== Step 6: Start mcp-server ==="
ssh "${DEMO_HOST}" "cd ${DEMO_DIR} && docker compose up -d mcp-server"

echo ""
echo "=== Demo Reset Complete ==="
echo "  Tenant:  volterra_energy (slug: volterra-energy)"
echo "  URL:     https://volterra-energy.demo.meridianhub.cloud"
echo ""
echo "  Verify:  ssh ${DEMO_HOST} 'docker logs meridian-meridian-1 --tail 10'"
