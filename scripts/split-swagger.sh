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
  echo "Error: jq is required. Install with your package manager (e.g. brew install jq, apt install jq)." >&2
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

# Initialize empty catalog
echo '[]' > "$OUTPUT_DIR/catalog.json"

printf '%s\n' "$TAGS" | while IFS= read -r TAG; do
  [ -z "$TAG" ] && continue
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

  # Count endpoints and append to catalog using jq
  COUNT=$(jq '.paths | [to_entries[].value | keys[]] | length' "$OUTPUT_DIR/$FILENAME.swagger.json")

  jq --arg name "$TAG" --arg file "$FILENAME.swagger.json" --argjson count "$COUNT" \
    '. += [{"name": $name, "file": $file, "endpoints": $count}]' \
    "$OUTPUT_DIR/catalog.json" > "$OUTPUT_DIR/catalog.tmp" && mv "$OUTPUT_DIR/catalog.tmp" "$OUTPUT_DIR/catalog.json"

  echo "  $FILENAME.swagger.json ($COUNT endpoints)"
done

TOTAL=$(jq 'length' "$OUTPUT_DIR/catalog.json")
echo ""
echo "Split into $TOTAL service files in $OUTPUT_DIR/"
echo "Catalog written to $OUTPUT_DIR/catalog.json"
