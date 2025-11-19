#!/usr/bin/env bash

# Deprecated: This script is maintained for backward compatibility.
# Please use: ./scripts/doctor.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "╔══════════════════════════════════════════════════════════╗"
echo "║                                                          ║"
echo "║  Note: setup-check.sh is deprecated                      ║"
echo "║  Please use: ./scripts/doctor.sh                         ║"
echo "║                                                          ║"
echo "║  Forwarding to doctor script...                          ║"
echo "║                                                          ║"
echo "╚══════════════════════════════════════════════════════════╝"
echo ""
sleep 2

exec "$SCRIPT_DIR/doctor.sh" --check "$@"
