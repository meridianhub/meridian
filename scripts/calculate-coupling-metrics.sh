#!/usr/bin/env bash
# Calculate coupling metrics for all services based on coupling analysis
# Outputs quantitative metrics: Ca, Ce, Instability, Distance from Main Sequence

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# Output paths
OUTPUT_DIR="${REPO_ROOT}/docs/architecture"
METRICS_FILE="${OUTPUT_DIR}/coupling-metrics.json"
TEMP_ANALYSIS="/tmp/coupling-analysis-$$.json"

echo -e "${BLUE}Calculating Coupling Metrics${NC}"
echo "Repository: ${REPO_ROOT}"
echo

# Step 1: Run coupling analysis to get raw data
echo -e "${YELLOW}[1/4] Running coupling analysis...${NC}"
"${SCRIPT_DIR}/analyze-coupling.sh" > "${TEMP_ANALYSIS}"

# Step 2: Extract services list
SERVICES=$(jq -r '.services_analyzed[]' "${TEMP_ANALYSIS}")
echo -e "${YELLOW}[2/4] Services analyzed: $(echo "$SERVICES" | tr '\n' ' ')${NC}"

# Step 3: Calculate metrics using jq
echo -e "${YELLOW}[3/4] Calculating coupling metrics...${NC}"

# Build the metrics JSON structure
cat > "${METRICS_FILE}" << 'EOF_TEMPLATE'
{
  "timestamp": "",
  "services": {}
}
EOF_TEMPLATE

# Update timestamp
TIMESTAMP=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
jq --arg ts "$TIMESTAMP" '.timestamp = $ts' "${METRICS_FILE}" > "${METRICS_FILE}.tmp" && mv "${METRICS_FILE}.tmp" "${METRICS_FILE}"

# Process each service
for service in $SERVICES; do
    echo "  Analyzing ${service}..."

    # Calculate Efferent Coupling (Ce): How many services THIS service depends on
    # Count unique target services from proto_usage where this is the from_service
    CE=$(jq --arg svc "$service" '
        [.proto_usage[] |
         select(.from_service == $svc) |
         .target_service] |
        unique |
        length
    ' "${TEMP_ANALYSIS}")

    # Calculate Afferent Coupling (Ca): How many services depend on THIS service
    # Count unique services that use proto from this service
    CA=$(jq --arg svc "$service" '
        [.proto_usage[] |
         select(.target_service == $svc) |
         .from_service] |
        unique |
        length
    ' "${TEMP_ANALYSIS}")

    # Calculate Instability (I): Ce / (Ca + Ce)
    # Handle division by zero (isolated service)
    if [ $((CA + CE)) -eq 0 ]; then
        INSTABILITY="0.0"
    else
        INSTABILITY=$(echo "scale=2; $CE / ($CA + $CE)" | bc)
    fi

    # For now, use 0.5 as placeholder for abstractness (A)
    # In future, this could be calculated from interface/concrete type ratio
    ABSTRACTNESS="0.5"

    # Calculate Distance from Main Sequence: |A + I - 1|
    # Main Sequence: A + I = 1 (ideal balance)
    DISTANCE=$(echo "scale=2; sqrt(($ABSTRACTNESS + $INSTABILITY - 1)^2)" | bc)

    # Determine assessment based on instability and coupling counts
    ASSESSMENT=""
    if (( $(echo "$INSTABILITY < 0.3" | bc -l) )); then
        if [ "$CA" -gt 3 ]; then
            ASSESSMENT="too-rigid"
        else
            ASSESSMENT="stable"
        fi
    elif (( $(echo "$INSTABILITY > 0.7" | bc -l) )); then
        if [ "$CE" -gt "$CA" ]; then
            ASSESSMENT="too-dependent"
        else
            ASSESSMENT="acceptable"
        fi
    else
        ASSESSMENT="acceptable"
    fi

    # Add service metrics to JSON
    jq --arg svc "$service" \
       --argjson ca "$CA" \
       --argjson ce "$CE" \
       --arg inst "$INSTABILITY" \
       --arg assess "$ASSESSMENT" \
       --arg abs "$ABSTRACTNESS" \
       --arg dist "$DISTANCE" \
       '.services[$svc] = {
           afferent_coupling: $ca,
           efferent_coupling: $ce,
           instability: ($inst | tonumber),
           assessment: $assess,
           abstractness: ($abs | tonumber),
           distance_from_main_sequence: ($dist | tonumber)
       }' "${METRICS_FILE}" > "${METRICS_FILE}.tmp" && mv "${METRICS_FILE}.tmp" "${METRICS_FILE}"

    # Visual feedback
    case "$ASSESSMENT" in
        stable)
            echo -e "    ${GREEN}✓${NC} Ca=$CA, Ce=$CE, I=$INSTABILITY → ${GREEN}${ASSESSMENT}${NC}"
            ;;
        acceptable)
            echo -e "    ${BLUE}•${NC} Ca=$CA, Ce=$CE, I=$INSTABILITY → ${BLUE}${ASSESSMENT}${NC}"
            ;;
        too-dependent)
            echo -e "    ${YELLOW}⚠${NC} Ca=$CA, Ce=$CE, I=$INSTABILITY → ${YELLOW}${ASSESSMENT}${NC}"
            ;;
        too-rigid)
            echo -e "    ${RED}✗${NC} Ca=$CA, Ce=$CE, I=$INSTABILITY → ${RED}${ASSESSMENT}${NC}"
            ;;
    esac
done

# Step 4: Cleanup and report
echo -e "${YELLOW}[4/4] Finalizing metrics...${NC}"
rm -f "${TEMP_ANALYSIS}"

echo
echo -e "${GREEN}✓ Coupling metrics calculated successfully!${NC}"
echo
echo "Output: ${METRICS_FILE}"
echo

# Display summary
echo -e "${BLUE}=== Coupling Metrics Summary ===${NC}"
echo

jq -r '
  .services |
  to_entries |
  sort_by(.value.instability) |
  .[] |
  "\(.key):\n  Afferent Coupling (Ca): \(.value.afferent_coupling)\n  Efferent Coupling (Ce): \(.value.efferent_coupling)\n  Instability (I): \(.value.instability)\n  Assessment: \(.value.assessment)\n  Distance from Main Sequence: \(.value.distance_from_main_sequence)\n"
' "${METRICS_FILE}"

# Show assessment distribution
echo -e "${BLUE}=== Assessment Distribution ===${NC}"
echo
jq -r '
  .services |
  [.[] | .assessment] |
  group_by(.) |
  map({assessment: .[0], count: length}) |
  .[] |
  "  \(.assessment): \(.count) service(s)"
' "${METRICS_FILE}"

echo
