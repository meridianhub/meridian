#!/usr/bin/env bash
# gen-asyncapi.sh - Generate AsyncAPI 3.0.0 specs from proto events and topic registry
#
# Usage: ./scripts/gen-asyncapi.sh [topics-file] [output-dir]
#
# Reads the Kafka topic registry (topics.yaml) and proto event definitions to
# produce one AsyncAPI 3.0.0 YAML file per BIAN service domain.
#
# Inputs:
#   - shared/platform/events/topics/topics.yaml  (topic registry)
#   - api/proto/meridian/events/v1/*.proto        (event message definitions)
#
# Output:
#   - api/asyncapi/<service>.yaml  (one per BIAN service domain)

set -euo pipefail

TOPICS_FILE="${1:-shared/platform/events/topics/topics.yaml}"
OUTPUT_DIR="${2:-api/asyncapi}"
PROTO_DIR="api/proto/meridian/events/v1"

# --- Dependency checks ---

if [ ! -f "$TOPICS_FILE" ]; then
  echo "Error: $TOPICS_FILE not found." >&2
  exit 1
fi

if ! command -v yq &>/dev/null; then
  echo "Error: yq is required. Install with: brew install yq" >&2
  exit 1
fi

if ! command -v python3 &>/dev/null; then
  echo "Error: python3 is required for JSON processing." >&2
  exit 1
fi

mkdir -p "$OUTPUT_DIR"

# --- Helper: convert service key to title ---
# "position-keeping" -> "Position Keeping"
to_title() {
  echo "$1" | sed 's/-/ /g' | awk '{for(i=1;i<=NF;i++) $i=toupper(substr($i,1,1)) tolower(substr($i,2))}1'
}

# --- Helper: convert kebab-case to CamelCase ---
# "transaction-captured" -> "TransactionCaptured"
kebab_to_camel() {
  echo "$1" | awk -F'-' '{for(i=1;i<=NF;i++) printf "%s", toupper(substr($i,1,1)) substr($i,2)}'
}

# --- Helper: find proto file containing a message ---
# Returns the file path, or empty string if not found.
find_proto_file() {
  local message_name="$1"
  for f in "$PROTO_DIR"/*.proto; do
    if [ -f "$f" ] && grep -q "^message ${message_name} " "$f" 2>/dev/null; then
      echo "$f"
      return
    fi
  done
  echo ""
}

# --- Helper: resolve proto_message from topics.yaml to actual proto message name ---
# topics.yaml may use "TransactionCaptured" while proto defines "TransactionCapturedEvent".
# Returns: "proto_file|proto_msg_name" or "|" if not found.
resolve_proto_message() {
  local topic_msg="$1"
  if [ -z "$topic_msg" ]; then
    echo "|"
    return
  fi

  # Try exact name first
  local pf
  pf=$(find_proto_file "$topic_msg")
  if [ -n "$pf" ]; then
    echo "${pf}|${topic_msg}"
    return
  fi

  # Try with Event suffix
  pf=$(find_proto_file "${topic_msg}Event")
  if [ -n "$pf" ]; then
    echo "${pf}|${topic_msg}Event"
    return
  fi

  echo "|"
}

# --- Helper: extract proto fields as JSON array ---
extract_proto_fields() {
  local proto_file="$1"
  local message_name="$2"

  if [ ! -f "$proto_file" ]; then
    echo "[]"
    return
  fi

  awk -v msg="$message_name" '
    BEGIN { in_msg=0; depth=0; first=0; print "[" }
    /^message / {
      split($0, parts, " ")
      if (parts[2] == msg) {
        in_msg=1; depth=1
        next
      }
    }
    !in_msg { next }
    /{/ { depth++ }
    /}/ {
      depth--
      if (depth <= 0) { in_msg=0; next }
    }
    /^[[:space:]]*(message|oneof|enum) / { next }
    /^[[:space:]]+[a-zA-Z]/ {
      line = $0
      gsub(/^[[:space:]]+/, "", line)
      if (line ~ /^\/\//) next

      n = split(line, parts, " ")
      # Standard field: type name = N
      if (n >= 4 && parts[3] == "=") {
        type = parts[1]
        name = parts[2]
        json_type = "string"
        if (type ~ /^(int32|int64|uint32|uint64|sint32|sint64|fixed32|fixed64|sfixed32|sfixed64)$/) json_type = "integer"
        else if (type ~ /^(float|double)$/) json_type = "number"
        else if (type == "bool") json_type = "boolean"
        else if (type == "google.protobuf.Timestamp") json_type = "string"
        else if (type ~ /^map/) json_type = "object"
        if (first) printf ","
        printf "\n  {\"name\":\"%s\",\"type\":\"%s\"}", name, json_type
        first = 1
      }
      # Repeated field: repeated type name = N
      else if (n >= 5 && parts[1] == "repeated" && parts[4] == "=") {
        name = parts[3]
        if (first) printf ","
        printf "\n  {\"name\":\"%s\",\"type\":\"array\"}", name
        first = 1
      }
    }
    END { print "\n]" }
  ' "$proto_file"
}

# --- Helper: infer partition key from proto message ---
# Convention: first string field ending in _id (excluding event_id, correlation_id, etc.)
infer_partition_key() {
  local proto_file="$1"
  local message_name="$2"

  if [ ! -f "$proto_file" ]; then
    echo ""
    return
  fi

  awk -v msg="$message_name" '
    BEGIN { in_msg=0; depth=0; found="" }
    /^message / {
      split($0, parts, " ")
      if (parts[2] == msg) { in_msg=1; depth=1; next }
    }
    !in_msg { next }
    /{/ { depth++ }
    /}/ { depth--; if (depth <= 0) { in_msg=0; next } }
    /^[[:space:]]+string [a-z]/ {
      line = $0
      gsub(/^[[:space:]]+/, "", line)
      n = split(line, parts, " ")
      if (n >= 4 && parts[3] == "=") {
        name = parts[2]
        if (name == "event_id" || name == "correlation_id" || name == "causation_id" || name == "idempotency_key") next
        if (name ~ /_id$/ && found == "") found = name
      }
    }
    END { print found }
  ' "$proto_file"
}

# --- Helper: describe a partition key field ---
describe_partition_key() {
  local key="$1"
  case "$key" in
    account_id)        echo "Account ID" ;;
    log_id)            echo "Position log ID" ;;
    batch_id)          echo "Batch operation ID" ;;
    withdrawal_id)     echo "Withdrawal ID" ;;
    transaction_id)    echo "Transaction ID" ;;
    facility_id)       echo "Facility ID" ;;
    party_id)          echo "Party ID" ;;
    order_id)          echo "Payment order ID" ;;
    payment_order_id)  echo "Payment order ID" ;;
    run_id)            echo "Reconciliation run ID" ;;
    dispute_id)        echo "Dispute ID" ;;
    *)                 echo "$key" ;;
  esac
}

# --- Helper: write schema properties from proto fields ---
write_schema_properties() {
  local proto_file="$1"
  local proto_msg="$2"
  local output="$3"

  if [ -z "$proto_file" ] || [ ! -f "$proto_file" ]; then
    cat >> "$output" <<'PROPEOF'
        event_type:
          type: string
          description: "Proto definition not yet available in api/proto/meridian/events/v1/"
PROPEOF
    return
  fi

  local fields_json
  fields_json=$(extract_proto_fields "$proto_file" "$proto_msg")
  local field_count
  field_count=$(echo "$fields_json" | python3 -c "import sys,json; print(len(json.load(sys.stdin)))" 2>/dev/null || echo "0")

  if [ "$field_count" -gt 0 ]; then
    echo "$fields_json" | python3 -c "
import sys, json
fields = json.load(sys.stdin)
for f in fields:
    name = f['name']
    ftype = f['type']
    if ftype == 'array':
        print(f'        {name}:')
        print(f'          type: array')
        print(f'          items:')
        print(f'            type: string')
    elif ftype == 'object':
        print(f'        {name}:')
        print(f'          type: object')
    else:
        print(f'        {name}:')
        print(f'          type: {ftype}')
" >> "$output"
  else
    cat >> "$output" <<'PROPEOF'
        event_type:
          type: string
PROPEOF
  fi
}

# --- Main: iterate over services in topics.yaml ---

SERVICES=$(yq -r '.services | keys | .[]' "$TOPICS_FILE")

FILE_COUNT=0

for SERVICE in $SERVICES; do
  SERVICE_DESC=$(yq -r ".services.\"${SERVICE}\".description // \"\"" "$TOPICS_FILE")
  SERVICE_TITLE="$(to_title "$SERVICE") Events"
  OUTPUT_FILE="${OUTPUT_DIR}/${SERVICE}.yaml"

  # Count non-deprecated topics
  TOPIC_COUNT=$(yq -r ".services.\"${SERVICE}\".topics[] | select(.deprecated != true) | .name" "$TOPICS_FILE" | wc -l | tr -d ' ')
  if [ "$TOPIC_COUNT" -eq 0 ]; then
    echo "  Skipping $SERVICE (no active topics)"
    continue
  fi

  # --- Header ---
  cat > "$OUTPUT_FILE" <<EOF
asyncapi: 3.0.0
info:
  title: "${SERVICE_TITLE}"
  version: 1.0.0
  description: "${SERVICE_DESC}"
servers:
  kafka:
    host: kafka:9092
    protocol: kafka
channels:
EOF

  # --- Channels ---
  TOPICS=$(yq -r ".services.\"${SERVICE}\".topics[] | select(.deprecated != true) | .name" "$TOPICS_FILE")

  for TOPIC in $TOPICS; do
    PROTO_MSG_RAW=$(yq -r ".services.\"${SERVICE}\".topics[] | select(.name == \"${TOPIC}\") | .proto_message // \"\"" "$TOPICS_FILE")
    TOPIC_DESC=$(yq -r ".services.\"${SERVICE}\".topics[] | select(.name == \"${TOPIC}\") | .description // \"\"" "$TOPICS_FILE")

    # Use the topics.yaml name as the AsyncAPI reference name
    MSG_REF="$PROTO_MSG_RAW"

    cat >> "$OUTPUT_FILE" <<EOF
  ${TOPIC}:
    address: "${TOPIC}"
    description: "${TOPIC_DESC}"
EOF

    if [ -n "$MSG_REF" ]; then
      cat >> "$OUTPUT_FILE" <<EOF
    messages:
      ${MSG_REF}:
        \$ref: '#/components/messages/${MSG_REF}'
EOF
    fi
  done

  # --- Operations ---
  cat >> "$OUTPUT_FILE" <<EOF
operations:
EOF

  for TOPIC in $TOPICS; do
    PROTO_MSG_RAW=$(yq -r ".services.\"${SERVICE}\".topics[] | select(.name == \"${TOPIC}\") | .proto_message // \"\"" "$TOPICS_FILE")

    # Build operation ID from all segments except the service prefix and version suffix.
    # position-keeping.transaction-captured.v1 -> publishTransactionCaptured
    # audit.events.v1.dlq -> publishEventsDlq
    # Strip service prefix (first segment) and join remaining segments.
    EVENT_PARTS=$(echo "$TOPIC" | awk -F. '{for(i=2;i<=NF;i++){if($i ~ /^v[0-9]+$/) continue; printf "%s\n", $i}}')
    CAMEL=""
    for PART in $EVENT_PARTS; do
      CAMEL="${CAMEL}$(kebab_to_camel "$PART")"
    done
    OP_ID="publish${CAMEL}"

    cat >> "$OUTPUT_FILE" <<EOF
  ${OP_ID}:
    action: send
    channel:
      \$ref: '#/channels/${TOPIC}'
EOF
  done

  # --- Components: messages ---
  cat >> "$OUTPUT_FILE" <<EOF
components:
  messages:
EOF

  for TOPIC in $TOPICS; do
    PROTO_MSG_RAW=$(yq -r ".services.\"${SERVICE}\".topics[] | select(.name == \"${TOPIC}\") | .proto_message // \"\"" "$TOPICS_FILE")
    if [ -z "$PROTO_MSG_RAW" ]; then
      continue
    fi

    MSG_REF="$PROTO_MSG_RAW"
    TOPIC_DESC=$(yq -r ".services.\"${SERVICE}\".topics[] | select(.name == \"${TOPIC}\") | .description // \"\"" "$TOPICS_FILE")

    # Resolve proto message for partition key inference
    RESOLVED=$(resolve_proto_message "$PROTO_MSG_RAW")
    PROTO_FILE="${RESOLVED%%|*}"
    PROTO_MSG="${RESOLVED##*|}"

    # Infer partition key
    PARTITION_KEY=""
    PARTITION_DESC=""
    if [ -n "$PROTO_FILE" ] && [ -n "$PROTO_MSG" ]; then
      PARTITION_KEY=$(infer_partition_key "$PROTO_FILE" "$PROTO_MSG")
      if [ -n "$PARTITION_KEY" ]; then
        PARTITION_DESC=$(describe_partition_key "$PARTITION_KEY")
      fi
    fi

    cat >> "$OUTPUT_FILE" <<EOF
    ${MSG_REF}:
      name: "${MSG_REF}"
      title: "${MSG_REF}"
      description: "${TOPIC_DESC}"
      contentType: application/protobuf
EOF

    if [ -n "$PARTITION_KEY" ]; then
      cat >> "$OUTPUT_FILE" <<EOF
      bindings:
        kafka:
          key:
            type: string
            description: "${PARTITION_DESC}"
EOF
    fi

    cat >> "$OUTPUT_FILE" <<EOF
      headers:
        type: object
        properties:
          event_type:
            type: string
            description: Logical event type identifier
          correlation_id:
            type: string
            format: uuid
            description: Links related events across services
          tenant_id:
            type: string
            description: Tenant identifier for multi-tenant isolation
      payload:
        \$ref: '#/components/schemas/${MSG_REF}'
EOF
  done

  # --- Components: schemas ---
  cat >> "$OUTPUT_FILE" <<EOF
  schemas:
EOF

  for TOPIC in $TOPICS; do
    PROTO_MSG_RAW=$(yq -r ".services.\"${SERVICE}\".topics[] | select(.name == \"${TOPIC}\") | .proto_message // \"\"" "$TOPICS_FILE")
    if [ -z "$PROTO_MSG_RAW" ]; then
      continue
    fi

    MSG_REF="$PROTO_MSG_RAW"

    # Resolve to actual proto message name for field extraction
    RESOLVED=$(resolve_proto_message "$PROTO_MSG_RAW")
    PROTO_FILE="${RESOLVED%%|*}"
    PROTO_MSG="${RESOLVED##*|}"

    cat >> "$OUTPUT_FILE" <<EOF
    ${MSG_REF}:
      type: object
      description: "Derived from protobuf message meridian.events.v1.${PROTO_MSG:-$MSG_REF}"
      properties:
EOF

    write_schema_properties "$PROTO_FILE" "$PROTO_MSG" "$OUTPUT_FILE"
  done

  FILE_COUNT=$((FILE_COUNT + 1))
  echo "  ${OUTPUT_FILE} (${TOPIC_COUNT} channels)"
done

echo ""
echo "Generated ${FILE_COUNT} AsyncAPI spec files in ${OUTPUT_DIR}/"
