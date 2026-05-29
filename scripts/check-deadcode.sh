#!/usr/bin/env bash
#
# check-deadcode.sh - whole-program dead-code gate for Go.
#
# Runs golang.org/x/tools/cmd/deadcode across all packages (with -test so that
# test entry points count as reachable, which removes false positives for
# test-only helpers) and gates the result against a committed baseline count.
#
# This is a ratchet: the number of unreachable functions must not increase.
# The full list of findings is printed for visibility - nothing is hidden. As
# dead code is removed, lower BASELINE to lock in the improvement.
#
# Regenerate the current count from the repository root:
#   deadcode -test ./... | grep -c 'unreachable func:'
#
set -euo pipefail

# Maximum number of unreachable functions allowed (ratchet - lower as dead code
# is removed). Tracked in assess-2026-05-22.
BASELINE=87

cd "$(git rev-parse --show-toplevel)" || exit 1

echo "Running deadcode reachability analysis (deadcode -test ./...)..."
findings="$(deadcode -test ./... || true)"
count="$(printf '%s\n' "$findings" | grep -c 'unreachable func:' || true)"

echo "----------------------------------------------------------------------"
printf '%s\n' "$findings"
echo "----------------------------------------------------------------------"
echo "Unreachable functions: ${count} (baseline: ${BASELINE})"

if [ "$count" -gt "$BASELINE" ]; then
  echo "::error::deadcode found ${count} unreachable functions, exceeding the baseline of ${BASELINE}."
  echo "New dead code was introduced. Remove it, or - if the reachability analysis"
  echo "is wrong (e.g. a function reached only via reflection or linkname) - raise"
  echo "BASELINE in scripts/check-deadcode.sh with a justification."
  exit 1
fi

if [ "$count" -lt "$BASELINE" ]; then
  echo "::warning::deadcode count (${count}) is below the baseline (${BASELINE})."
  echo "Lower BASELINE in scripts/check-deadcode.sh to lock in this improvement."
fi

echo "deadcode check passed."
