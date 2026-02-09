#!/usr/bin/env bash
# validate-manifest-jsonschema.sh
#
# Validates that the generated JSON Schema (api/jsonschema/manifest.v1.schema.json)
# is in sync with the protobuf definition (api/proto/meridian/control_plane/v1/manifest.proto).
#
# This script regenerates the JSON Schema into a temp directory and compares it
# against the committed version. If they differ, the proto was changed without
# regenerating the schema.
#
# Usage: ./scripts/validate-manifest-jsonschema.sh
# Exit code: 0 if in sync, 1 if out of sync or error

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

SCHEMA_FILE="${PROJECT_ROOT}/api/jsonschema/manifest.v1.schema.json"
PROTO_FILE="api/proto/meridian/control_plane/v1/manifest.proto"

# Check prerequisites
if ! command -v buf &> /dev/null; then
    echo "ERROR: buf is not installed. Run 'make install'."
    exit 1
fi

if ! command -v protoc-gen-jsonschema &> /dev/null; then
    # Check in ~/go/bin as fallback
    if [ -f "${HOME}/go/bin/protoc-gen-jsonschema" ]; then
        export PATH="${PATH}:${HOME}/go/bin"
    else
        echo "ERROR: protoc-gen-jsonschema is not installed."
        echo "Install: go install github.com/chrusty/protoc-gen-jsonschema/cmd/protoc-gen-jsonschema@latest"
        exit 1
    fi
fi

if [ ! -f "${SCHEMA_FILE}" ]; then
    echo "ERROR: ${SCHEMA_FILE} not found. Run 'make proto-jsonschema' to generate it."
    exit 1
fi

# Generate into a temp directory
TEMP_DIR=$(mktemp -d)
trap 'rm -rf "${TEMP_DIR}"' EXIT

cd "${PROJECT_ROOT}"

buf generate \
    --template buf.gen.jsonschema.yaml \
    --path "${PROTO_FILE}" \
    2>/dev/null

# The generator creates files in a subdirectory
GENERATED_FILE="${PROJECT_ROOT}/api/jsonschema/meridian.control_plane.v1/Manifest.json"

if [ ! -f "${GENERATED_FILE}" ]; then
    echo "ERROR: JSON Schema generation produced no output."
    exit 1
fi

# Compare
if diff -q "${SCHEMA_FILE}" "${GENERATED_FILE}" > /dev/null 2>&1; then
    echo "JSON Schema is in sync with proto definition."
    rm -rf "${PROJECT_ROOT}/api/jsonschema/meridian.control_plane.v1"
    exit 0
else
    echo "ERROR: JSON Schema is out of sync with proto definition."
    echo ""
    echo "The file api/jsonschema/manifest.v1.schema.json does not match"
    echo "the current proto definition at ${PROTO_FILE}."
    echo ""
    echo "To fix: run 'make proto-jsonschema' and commit the updated schema."
    echo ""
    echo "Diff:"
    diff "${SCHEMA_FILE}" "${GENERATED_FILE}" || true
    rm -rf "${PROJECT_ROOT}/api/jsonschema/meridian.control_plane.v1"
    exit 1
fi
