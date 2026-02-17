#!/usr/bin/env bash
# split-swagger.sh - Split monolithic swagger into per-service files
#
# Usage: ./scripts/split-swagger.sh [input-file] [output-dir]
#
# Generates one swagger JSON per service tag, plus a catalog.json
# index file for the Swagger UI service picker.

set -euo pipefail

INPUT="${1:-api/openapi/meridian.swagger.json}"
OUTPUT_DIR="${2:-api/openapi/services}"

if [ ! -f "$INPUT" ]; then
  echo "Error: $INPUT not found. Run 'make proto' first." >&2
  exit 1
fi

if ! command -v jq &>/dev/null; then
  echo "Error: jq is required. Install with: brew install jq" >&2
  exit 1
fi

mkdir -p "$OUTPUT_DIR"

# Extract all service tags
TAGS=$(jq -r '.tags[].name' "$INPUT")

# Convert CamelCase tag to kebab-case filename
# CurrentAccountService -> current-account
to_filename() {
  echo "$1" \
    | sed 's/Service$//' \
    | sed 's/\([A-Z]\)/-\1/g' \
    | sed 's/^-//' \
    | tr '[:upper:]' '[:lower:]'
}

# Build catalog entries
CATALOG="["
FIRST=true

for TAG in $TAGS; do
  FILENAME=$(to_filename "$TAG")

  # Split: keep global metadata, filter paths by tag, include all definitions
  jq --arg tag "$TAG" --arg filename "$FILENAME" '{
    swagger: .swagger,
    info: {
      title: ($tag | gsub("(?<a>[A-Z])"; " \(.a)") | ltrimstr(" ")),
      description: .info.description,
      version: .info.version,
      contact: .info.contact,
      license: .info.license
    },
    tags: [.tags[] | select(.name == $tag)],
    schemes: .schemes,
    consumes: .consumes,
    produces: .produces,
    paths: (
      .paths | to_entries | map(
        select(
          .value | to_entries | any(
            .value.tags // [] | index($tag)
          )
        )
      ) | from_entries
    ),
    definitions: .definitions
  }' "$INPUT" > "$OUTPUT_DIR/$FILENAME.swagger.json"

  # Count endpoints in this service
  COUNT=$(jq '.paths | [to_entries[].value | keys[]] | length' "$OUTPUT_DIR/$FILENAME.swagger.json")

  if [ "$FIRST" = true ]; then
    FIRST=false
  else
    CATALOG="$CATALOG,"
  fi
  CATALOG="$CATALOG{\"name\":\"$TAG\",\"file\":\"$FILENAME.swagger.json\",\"endpoints\":$COUNT}"

  echo "  $FILENAME.swagger.json ($COUNT endpoints)"
done

CATALOG="$CATALOG]"
echo "$CATALOG" | jq '.' > "$OUTPUT_DIR/catalog.json"

TOTAL=$(echo "$CATALOG" | jq 'length')
echo ""
echo "Split into $TOTAL service files in $OUTPUT_DIR/"
echo "Catalog written to $OUTPUT_DIR/catalog.json"
