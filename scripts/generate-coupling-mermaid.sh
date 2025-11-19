#!/usr/bin/env bash
#
# generate-coupling-mermaid.sh - Generate Mermaid dependency graph from coupling analysis
#
# This script reads JSON output from analyze-coupling.sh and generates Mermaid diagram syntax
# that visualizes service coupling violations and dependencies.
#
# Usage:
#   ./scripts/analyze-coupling.sh | ./scripts/generate-coupling-mermaid.sh
#   ./scripts/analyze-coupling.sh > report.json && ./scripts/generate-coupling-mermaid.sh report.json
#   ./scripts/generate-coupling-mermaid.sh < report.json
#
# Output:
#   Mermaid flowchart syntax (stdout) ready for embedding in markdown
#
# Exit codes:
#   0 - Success
#   1 - Error (invalid JSON or missing jq)

set -euo pipefail

# Check for jq dependency
if ! command -v jq &> /dev/null; then
    echo "Error: jq is required but not installed. Install with: brew install jq" >&2
    exit 1
fi

# Read JSON from file argument or stdin
JSON_INPUT=""
if [[ $# -eq 1 ]]; then
    JSON_INPUT=$(cat "$1")
elif [[ ! -t 0 ]]; then
    JSON_INPUT=$(cat)
else
    echo "Usage: $0 [report.json]" >&2
    echo "  Or pipe JSON: ./analyze-coupling.sh | $0" >&2
    exit 1
fi

# Validate JSON
if ! echo "${JSON_INPUT}" | jq empty 2>/dev/null; then
    echo "Error: Invalid JSON input" >&2
    exit 1
fi

# Extract services
SERVICES=$(echo "${JSON_INPUT}" | jq -r '.services_analyzed[]' | sort -u)

# Start Mermaid diagram
cat <<'EOF'
```mermaid
graph TD
    %% Service Nodes
EOF

# Generate service nodes with abbreviated IDs
# Use a function to map service names to node IDs (compatible with older bash)
get_service_id() {
    local service="$1"
    case "${service}" in
        "current-account")
            echo "CA"
            ;;
        "financial-accounting")
            echo "FA"
            ;;
        "position-keeping")
            echo "PK"
            ;;
        *)
            # Fallback: use initials
            echo "${service}" | tr '[:lower:]' '[:upper:]' | sed 's/-\([A-Z]\)/\1/g' | grep -o '^.\|[A-Z]' | tr -d '\n'
            ;;
    esac
}

for service in ${SERVICES}; do
    node_id=$(get_service_id "${service}")

    # Format service name for display
    display_name=$(echo "${service}" | sed 's/-/ /g' | awk '{for(i=1;i<=NF;i++) $i=toupper(substr($i,1,1)) tolower(substr($i,2))}1')

    echo "    ${node_id}[${display_name}]"
done

# Add platform node for internal/platform violations
echo "    PLAT[Platform]"

echo ""
echo "    %% Proto Dependencies (Safe - Green)"

# Generate proto usage edges (safe dependencies)
echo "${JSON_INPUT}" | jq -r '.proto_usage[] |
    select(.from_service != .target_service) |
    "\(.from_service)|\(.target_service)|proto-only"' | sort -u | while IFS='|' read -r from to label; do
    from_id=$(get_service_id "${from}")
    to_id=$(get_service_id "${to}")
    echo "    ${from_id} -->|proto only| ${to_id}"
done

echo ""
echo "    %% Internal/Platform Usage (Warning - Yellow)"

# Generate internal/platform edges (warnings)
echo "${JSON_INPUT}" | jq -r '.violations[] |
    select(.type == "internal-platform-import") |
    "\(.from)|platform|internal/platform/\(.to | split("/") | .[-1])"' | sort -u | while IFS='|' read -r from to component; do
    from_id=$(get_service_id "${from}")
    echo "    ${from_id} -.->|${component}| PLAT"
done

echo ""
echo "    %% Cross-Service Violations (Critical - Red)"

# Generate cross-service internal import edges (violations)
echo "${JSON_INPUT}" | jq -r '.violations[] |
    select(.type == "cross-service-internal-import") |
    "\(.from)|\(.to | split("/") | .[1])|VIOLATION: \(.to | split("/") | .[1])/\(.import_path | split("/") | .[-1])"' | sort -u | while IFS='|' read -r from to label; do
    from_id=$(get_service_id "${from}")
    to_id=$(get_service_id "${to}")
    echo "    ${from_id} ==>|${label}| ${to_id}"
done

# Determine which services have violations
VIOLATION_SERVICES=$(echo "${JSON_INPUT}" | jq -r '.violations[] |
    select(.type == "cross-service-internal-import" or .type == "internal-platform-import") |
    .from' | sort -u)

WARNING_SERVICES=$(echo "${JSON_INPUT}" | jq -r '.violations[] |
    select(.type == "internal-platform-import") |
    .from' | sort -u)

SAFE_SERVICES=""
for service in ${SERVICES}; do
    if ! echo "${VIOLATION_SERVICES}" | grep -q "^${service}$"; then
        SAFE_SERVICES="${SAFE_SERVICES} ${service}"
    fi
done

# Apply styling classes
echo ""
echo "    %% Styling"
echo "    classDef violation fill:#ff6b6b,stroke:#c92a2a,stroke-width:2px,color:#fff"
echo "    classDef warning fill:#ffd43b,stroke:#fab005,stroke-width:2px,color:#000"
echo "    classDef safe fill:#51cf66,stroke:#37b24d,stroke-width:2px,color:#000"
echo "    classDef platform fill:#748ffc,stroke:#4c6ef5,stroke-width:2px,color:#fff"

# Classify services by coupling severity
# Critical: Has cross-service internal imports
CRITICAL_SERVICES=$(echo "${JSON_INPUT}" | jq -r '.violations[] |
    select(.type == "cross-service-internal-import") |
    .from' | sort -u)

# Warning: Only has internal/platform imports
WARNING_ONLY_SERVICES=""
for service in ${WARNING_SERVICES}; do
    if ! echo "${CRITICAL_SERVICES}" | grep -q "^${service}$" 2>/dev/null; then
        WARNING_ONLY_SERVICES="${WARNING_ONLY_SERVICES} ${service}"
    fi
done

# Apply classes
if [[ -n "${CRITICAL_SERVICES}" ]]; then
    critical_ids=""
    for svc in ${CRITICAL_SERVICES}; do
        [[ -n "${critical_ids}" ]] && critical_ids="${critical_ids},"
        critical_ids="${critical_ids}$(get_service_id "${svc}")"
    done
    echo "    class ${critical_ids} violation"
fi

if [[ -n "${WARNING_ONLY_SERVICES}" ]]; then
    warning_ids=""
    for svc in ${WARNING_ONLY_SERVICES}; do
        [[ -n "${warning_ids}" ]] && warning_ids="${warning_ids},"
        warning_ids="${warning_ids}$(get_service_id "${svc}")"
    done
    echo "    class ${warning_ids} warning"
fi

if [[ -n "${SAFE_SERVICES}" ]]; then
    safe_ids=""
    for svc in ${SAFE_SERVICES}; do
        [[ -n "${safe_ids}" ]] && safe_ids="${safe_ids},"
        safe_ids="${safe_ids}$(get_service_id "${svc}")"
    done
    echo "    class ${safe_ids} safe"
fi

echo "    class PLAT platform"

# Add legend
echo ""
echo "    %% Legend"
echo "    subgraph Legend"
echo "        direction LR"
echo "        L1[Safe: Proto only]:::safe"
echo "        L2[Warning: Platform usage]:::warning"
echo "        L3[Violation: Cross-service internal]:::violation"
echo "    end"

echo '```'

# Generate summary report
echo ""
echo "<!-- Coupling Analysis Summary"
echo "Generated: $(date -u +"%Y-%m-%d %H:%M:%S UTC")"
echo ""
echo "Services analyzed: $(echo "${SERVICES}" | wc -w | tr -d ' ')"
echo ""

# Count violations by type
total_violations=$(echo "${JSON_INPUT}" | jq '.violations | length')
cross_service=$(echo "${JSON_INPUT}" | jq '.violations[] | select(.type == "cross-service-internal-import") | .type' | wc -l | tr -d ' ')
platform_imports=$(echo "${JSON_INPUT}" | jq '.violations[] | select(.type == "internal-platform-import") | .type' | wc -l | tr -d ' ')
proto_deps=$(echo "${JSON_INPUT}" | jq '.proto_usage | length')

echo "Total violations: ${total_violations}"
echo "  - Cross-service internal imports: ${cross_service} (CRITICAL)"
echo "  - Internal/platform usage: ${platform_imports} (WARNING)"
echo "Proto dependencies (safe): ${proto_deps}"
echo ""

# List violations by service
if [[ ${cross_service} -gt 0 ]]; then
    echo "Cross-service violations:"
    echo "${JSON_INPUT}" | jq -r '.violations[] |
        select(.type == "cross-service-internal-import") |
        "  - \(.from) -> \(.to) (\(.file):\(.line))"'
    echo ""
fi

if [[ ${platform_imports} -gt 0 ]]; then
    echo "Platform coupling (by service):"
    echo "${JSON_INPUT}" | jq -r '.violations[] |
        select(.type == "internal-platform-import") |
        "\(.from)|\(.to | split("/") | .[-1])"' | sort | uniq -c | sort -rn | while read -r count entry; do
        service=$(echo "${entry}" | cut -d'|' -f1)
        component=$(echo "${entry}" | cut -d'|' -f2)
        echo "  - ${service}: ${component} (${count} imports)"
    done
fi

echo "-->"
