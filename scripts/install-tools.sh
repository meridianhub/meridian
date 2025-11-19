#!/usr/bin/env bash

# Deprecated: This script is maintained for backward compatibility.
# Please use: ./scripts/doctor.sh --fix

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "╔══════════════════════════════════════════════════════════╗"
echo "║                                                          ║"
echo "║  Note: install-tools.sh is deprecated                    ║"
echo "║  Please use: ./scripts/doctor.sh --fix                   ║"
echo "║                                                          ║"
echo "║  Forwarding to doctor script...                          ║"
echo "║                                                          ║"
echo "╚══════════════════════════════════════════════════════════╝"
echo ""
sleep 2

exec "$SCRIPT_DIR/doctor.sh" --fix "$@"
