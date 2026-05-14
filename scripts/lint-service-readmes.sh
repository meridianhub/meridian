#!/usr/bin/env bash
# lint-service-readmes.sh — checks that every Go service under services/ has a
# README.md with the 8 required sections defined in docs/service-readme-template.md.
#
# Exit codes:
#   0  all services pass
#   1  one or more services are missing a README or a required section
#
# Usage: bash scripts/lint-service-readmes.sh [services_dir]
#   services_dir defaults to "services" relative to the repo root.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SERVICES_DIR="${1:-${REPO_ROOT}/services}"

if [ ! -d "${SERVICES_DIR}" ]; then
    echo "Error: services directory not found: ${SERVICES_DIR}" >&2
    exit 1
fi

# The 8 required H2 sections (frontmatter checked separately).
REQUIRED_SECTIONS=(
    "## Overview"
    "## API Surface"
    "## Domain Model"
    "## Dependencies"
    "## Dependents"
    "## Load-Bearing Files"
    "## Configuration"
)

failures=()
checked=0

is_go_service() {
    local dir="$1"
    # A directory counts as a Go service if it contains a cmd/ or service/
    # subdirectory, or any .go file anywhere within it.
    if [ -d "${dir}/cmd" ] || [ -d "${dir}/service" ]; then
        return 0
    fi
    if find "${dir}" -name "*.go" -quit 2>/dev/null | grep -q .; then
        return 0
    fi
    return 1
}

for svc_dir in "${SERVICES_DIR}"/*/; do
    [ -d "${svc_dir}" ] || continue
    svc_name="$(basename "${svc_dir}")"

    # Skip non-Go directories (shared libs, scaffolding, etc.)
    if ! is_go_service "${svc_dir}"; then
        continue
    fi

    readme="${svc_dir}README.md"
    svc_failures=()

    # Check README exists.
    if [ ! -f "${readme}" ]; then
        failures+=("${svc_name}: missing README.md")
        checked=$((checked + 1))
        continue
    fi

    # Check frontmatter: file must start with "---".
    first_line="$(head -n 1 "${readme}")"
    if [ "${first_line}" != "---" ]; then
        svc_failures+=("missing YAML frontmatter (file must start with ---)")
    fi

    # Check each required H2 section.
    for section in "${REQUIRED_SECTIONS[@]}"; do
        if ! grep -qF "${section}" "${readme}"; then
            svc_failures+=("missing section: ${section}")
        fi
    done

    if [ "${#svc_failures[@]}" -gt 0 ]; then
        for msg in "${svc_failures[@]}"; do
            failures+=("${svc_name}: ${msg}")
        done
    fi

    checked=$((checked + 1))
done

echo "Checked ${checked} Go service(s) under ${SERVICES_DIR}"

if [ "${#failures[@]}" -eq 0 ]; then
    echo "All services pass README lint."
    exit 0
fi

echo ""
echo "README lint failures (${#failures[@]}):"
for f in "${failures[@]}"; do
    echo "  - ${f}"
done
exit 1
