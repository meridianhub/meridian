#!/usr/bin/env bash
#
# check-ts-prune.sh - unused-export gate for the frontend.
#
# Runs ts-prune over the TypeScript sources and gates the result against a
# committed baseline count. Two categories are excluded:
#   - Generated protobuf clients (src/api/gen) - regenerated from .proto, so
#     their export surface is not hand-maintained and would make the count
#     volatile.
#   - Exports flagged "(used in module)" - these are used within their own
#     module and are not actually dead.
#
# This is a ratchet: the number of unused exports must not increase. The full
# list of findings is printed for visibility - nothing is hidden. As exports
# are removed, lower BASELINE to lock in the improvement.
#
# Regenerate the current count from frontend/:
#   npx ts-prune -p tsconfig.app.json -i 'src/api/gen' | grep -v '(used in module)' | grep -cE ' - '
#
set -euo pipefail

# Maximum number of unused exports allowed (ratchet - lower as exports are
# removed). Tracked in assess-2026-05-22.
BASELINE=233

cd "$(dirname "$0")/.." || exit 1

echo "Running ts-prune unused-export analysis..."
# Run ts-prune in its own command substitution (not piped into grep) so that a
# genuine ts-prune failure aborts the script under "set -e" instead of being
# masked by the pipeline. The "|| true" on the grep filters only guards the
# benign "no matching lines" exit code (grep returns 1), not tool errors.
raw_findings="$(npx ts-prune -p tsconfig.app.json -i 'src/api/gen')"
findings="$(printf '%s\n' "$raw_findings" | grep -v '(used in module)' || true)"
count="$(printf '%s\n' "$findings" | grep -cE ' - ' || true)"

echo "----------------------------------------------------------------------"
printf '%s\n' "$findings"
echo "----------------------------------------------------------------------"
echo "Unused exports: ${count} (baseline: ${BASELINE})"

if [ "$count" -gt "$BASELINE" ]; then
  echo "::error::ts-prune found ${count} unused exports, exceeding the baseline of ${BASELINE}."
  echo "New unused exports were introduced. Remove them, or - if the export is part"
  echo "of a deliberate public API - raise BASELINE in frontend/scripts/check-ts-prune.sh"
  echo "with a justification."
  exit 1
fi

if [ "$count" -lt "$BASELINE" ]; then
  echo "::warning::ts-prune count (${count}) is below the baseline (${BASELINE})."
  echo "Lower BASELINE in frontend/scripts/check-ts-prune.sh to lock in this improvement."
fi

echo "ts-prune check passed."
