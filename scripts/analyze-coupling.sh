#!/usr/bin/env bash
#
# analyze-coupling.sh - Automated service coupling analysis for Meridian
#
# This script analyzes the codebase for service coupling violations and generates
# a structured JSON report covering:
#   - Cross-service internal imports
#   - Proto message usage across boundaries
#   - gRPC client instantiation patterns
#   - Shared database schemas
#   - Kafka event patterns
#
# Usage:
#   ./scripts/analyze-coupling.sh > coupling-report.json
#
# Exit codes:
#   0 - Success (analysis completed)
#   1 - Error (invalid repository structure or analysis failure)

set -euo pipefail

# Colors for output (stderr only)
readonly RED='\033[0;31m'
readonly YELLOW='\033[1;33m'
readonly NC='\033[0m' # No Color

# Repository root detection
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# Validate we're in a Meridian repository
if [[ ! -f "${REPO_ROOT}/go.mod" ]] || ! grep -q "github.com/meridianhub/meridian" "${REPO_ROOT}/go.mod"; then
    echo -e "${RED}Error: Not in a Meridian repository root${NC}" >&2
    exit 1
fi

cd "${REPO_ROOT}"

# Service list (dynamically discovered)
#
# Post-ADR-0017, each service lives in its own directory under services/.
# Every subdirectory of services/ is a service; plain files (embed.go,
# README.md) are ignored by the -type d filter.
SERVICES=()
if [[ -d "services" ]]; then
    while IFS= read -r service_dir; do
        service_name="$(basename "${service_dir}")"
        SERVICES+=("${service_name}")
    done < <(find services -mindepth 1 -maxdepth 1 -type d | sort)
fi

if [[ ${#SERVICES[@]} -eq 0 ]]; then
    echo -e "${RED}Error: No services discovered under services/${NC}" >&2
    exit 1
fi

echo -e "${YELLOW}Analyzing ${#SERVICES[@]} services: ${SERVICES[*]}${NC}" >&2

# JSON output arrays
violations=()
proto_usage=()
grpc_clients=()
database_schemas=()
kafka_patterns=()

#
# Section 1: Find cross-service internal imports
#
# Pattern: Service A importing services/service-b/internal/...
# Go's internal-package rule already forbids this across service roots, so any
# match is a genuine encapsulation violation. Services should depend on each
# other through their public client packages (services/<svc>/client) or proto.
#
analyze_cross_service_imports() {
    local from_service="$1"
    local to_service="$2"

    local search_path="services/${from_service}"
    [[ ! -d "${search_path}" ]] && return

    # Find Go files importing the target service's internal package
    while IFS=: read -r file line_num content; do
        # Skip test files and vendor
        [[ "${file}" =~ _test\.go$ ]] && continue
        [[ "${file}" =~ /vendor/ ]] && continue

        # Extract the imported package path
        local import_path=""
        if [[ "${content}" =~ \"([^\"]+/services/${to_service}/internal[^\"]*)\" ]]; then
            import_path="${BASH_REMATCH[1]}"
        fi

        [[ -z "${import_path}" ]] && continue

        violations+=("$(cat <<EOF
    {
      "from": "${from_service}",
      "to": "services/${to_service}/internal",
      "file": "${file}",
      "line": ${line_num},
      "import_path": "${import_path}",
      "type": "cross-service-internal-import",
      "severity": "high",
      "message": "Service '${from_service}' directly imports internal package of '${to_service}'. Use the public client package or gRPC/proto interface instead."
    }
EOF
)")
    done < <(rg -n "services/${to_service}/internal" "${search_path}" --type go 2>/dev/null || true)
}

echo -e "${YELLOW}[1/6] Analyzing cross-service imports...${NC}" >&2
for from_svc in "${SERVICES[@]}"; do
    for to_svc in "${SERVICES[@]}"; do
        [[ "${from_svc}" == "${to_svc}" ]] && continue
        analyze_cross_service_imports "${from_svc}" "${to_svc}"
    done
done

#
# Section 2: Analyze internal/platform usage in services
#
# Post-ADR-0017 the public platform utilities live in shared/platform, which
# every service is expected to import (not a violation). A private
# internal/platform package would be a violation; none exist today, so this
# section normally reports nothing while still exercising the real services.
#
analyze_platform_imports() {
    echo -e "${YELLOW}[2/6] Analyzing internal/platform usage...${NC}" >&2

    for service in "${SERVICES[@]}"; do
        local search_path="services/${service}"
        [[ ! -d "${search_path}" ]] && continue

        while IFS=: read -r file line_num content; do
            # Skip vendor
            [[ "${file}" =~ /vendor/ ]] && continue

            # Extract the imported package
            local import_path=""
            if [[ "${content}" =~ \"([^\"]+/internal/platform[^\"]*)\" ]]; then
                import_path="${BASH_REMATCH[1]}"
            fi

            [[ -z "${import_path}" ]] && continue

            # Determine if the public shared/platform equivalent exists
            local package_name="${import_path##*/}"
            local should_be_public="false"
            local message="Service '${service}' imports internal/platform/${package_name}."

            # Check if similar functionality exists in shared/platform
            if [[ -d "shared/platform/${package_name}" ]]; then
                should_be_public="true"
                message="Service '${service}' imports internal/platform/${package_name} but shared/platform/${package_name} exists. Use the public API."
            fi

            violations+=("$(cat <<EOF
    {
      "from": "${service}",
      "to": "${import_path}",
      "file": "${file}",
      "line": ${line_num},
      "type": "internal-platform-import",
      "severity": "medium",
      "should_be_public": ${should_be_public},
      "message": "${message}"
    }
EOF
)")
        done < <(rg -n "internal/platform" "${search_path}" --type go 2>/dev/null || true)
    done
}
analyze_platform_imports

#
# Section 3: Trace proto message usage across service boundaries
#
# Pattern: Service using proto messages from another service's domain
# This indicates coupling through data structures
#
analyze_proto_usage() {
    echo -e "${YELLOW}[3/6] Analyzing proto message usage...${NC}" >&2

    for service in "${SERVICES[@]}"; do
        for other_svc in "${SERVICES[@]}"; do
            [[ "${service}" == "${other_svc}" ]] && continue

            # Convert service name to proto package format (e.g., position-keeping -> position_keeping)
            local proto_pkg="${other_svc//-/_}"

            local search_path="services/${service}"
            [[ ! -d "${search_path}" ]] && continue

            # Look for proto package imports
            while IFS=: read -r file line_num content; do
                [[ "${file}" =~ /vendor/ ]] && continue

                # Extract proto import
                if [[ "${content}" =~ api/proto/meridian/${proto_pkg} ]]; then
                    proto_usage+=("$(cat <<EOF
    {
      "from_service": "${service}",
      "proto_package": "${proto_pkg}",
      "target_service": "${other_svc}",
      "file": "${file}",
      "line": ${line_num},
      "severity": "low",
      "message": "Service '${service}' uses proto definitions from '${other_svc}'. This is expected for gRPC clients."
    }
EOF
)")
                fi
            done < <(rg -n "api/proto/meridian/${proto_pkg}" "${search_path}" --type go 2>/dev/null || true)
        done
    done
}
analyze_proto_usage

#
# Section 4: Map gRPC client instantiation
#
# Pattern: NewXServiceClient calls indicate service dependencies
# These are expected but should be documented
#
analyze_grpc_clients() {
    echo -e "${YELLOW}[4/6] Analyzing gRPC client instantiation...${NC}" >&2

    # Common gRPC client patterns
    local client_patterns=(
        "NewFinancialAccountingServiceClient"
        "NewCurrentAccountServiceClient"
        "NewPositionKeepingServiceClient"
        "New.*ServiceClient"
    )

    for pattern in "${client_patterns[@]}"; do
        while IFS=: read -r file line_num content; do
            [[ "${file}" =~ /vendor/ ]] && continue
            [[ "${file}" =~ _test\.go$ ]] && continue

            # Determine which service is instantiating the client
            local from_service=""
            if [[ "${file}" =~ services/([^/]+)/ ]]; then
                from_service="${BASH_REMATCH[1]}"
            fi

            # Extract the client type
            local client_type=""
            if [[ "${content}" =~ (New[A-Za-z]+ServiceClient) ]]; then
                client_type="${BASH_REMATCH[1]}"
            fi

            [[ -z "${from_service}" || -z "${client_type}" ]] && continue

            grpc_clients+=("$(cat <<EOF
    {
      "from_service": "${from_service}",
      "client_type": "${client_type}",
      "file": "${file}",
      "line": ${line_num},
      "message": "Service '${from_service}' instantiates gRPC client '${client_type}'"
    }
EOF
)")
        done < <(rg -n "${pattern}" services --type go 2>/dev/null || true)
    done
}
analyze_grpc_clients

#
# Section 5: Identify shared database schemas
#
# Pattern: Multiple services having migrations for the same tables
# This indicates shared database anti-pattern
#
analyze_database_schemas() {
    echo -e "${YELLOW}[5/6] Analyzing database schemas...${NC}" >&2

    [[ ! -d "services" ]] && return

    # Track tables across services (using temporary file instead of associative array for compatibility)
    local table_tracking_file
    table_tracking_file="$(mktemp)"
    trap "rm -f ${table_tracking_file}" RETURN

    for service in "${SERVICES[@]}"; do
        # Each service owns its migrations under services/<service>/migrations
        local migration_dir="services/${service}/migrations"
        [[ ! -d "${migration_dir}" ]] && continue

        while IFS= read -r migration_file; do
            # Extract CREATE TABLE statements
            while IFS= read -r line; do
                # Match CREATE TABLE with optional IF NOT EXISTS.
                # Identifiers may be double-quoted and/or schema-qualified
                # (e.g. "public"."saga_definitions"), so allow quotes in the
                # capture, then strip quotes and any schema prefix below.
                if [[ "${line}" =~ CREATE[[:space:]]+TABLE[[:space:]]+(IF[[:space:]]+NOT[[:space:]]+EXISTS[[:space:]]+)?(\"?[A-Za-z_][A-Za-z0-9_.\"]*) ]]; then
                    local table_name="${BASH_REMATCH[2]//\"/}"
                    table_name="${table_name##*.}"

                    # Track which services define this table
                    echo "${table_name}:${service}" >> "${table_tracking_file}"

                    database_schemas+=("$(cat <<EOF
    {
      "service": "${service}",
      "table_name": "${table_name}",
      "migration_file": "${migration_file}",
      "message": "Service '${service}' owns table '${table_name}'"
    }
EOF
)")
                fi
            done < "${migration_file}"
        done < <(find "${migration_dir}" -name "*.sql" 2>/dev/null || true)
    done

    # Check for shared tables (same table in multiple services)
    if [[ -f "${table_tracking_file}" ]]; then
        while IFS= read -r table_name; do
            local services
            services=$(grep "^${table_name}:" "${table_tracking_file}" | cut -d: -f2 | tr '\n' ' ' | sed 's/ $//')
            local service_count
            service_count=$(echo "${services}" | wc -w | tr -d ' ')

            if [[ ${service_count} -gt 1 ]]; then
                violations+=("$(cat <<EOF
    {
      "type": "shared-database-table",
      "table_name": "${table_name}",
      "services": "${services}",
      "severity": "critical",
      "message": "Table '${table_name}' is defined in multiple services: ${services}. This violates service isolation."
    }
EOF
)")
            fi
        done < <(cut -d: -f1 "${table_tracking_file}" | sort -u)
    fi
}
analyze_database_schemas

#
# Section 6: Document Kafka event patterns
#
# Pattern: Producer/Consumer usage indicates event-driven communication
# This is the preferred inter-service communication pattern
#
analyze_kafka_patterns() {
    echo -e "${YELLOW}[6/6] Analyzing Kafka event patterns...${NC}" >&2

    # Look for Kafka producer patterns
    while IFS=: read -r file line_num content; do
        [[ "${file}" =~ /vendor/ ]] && continue
        [[ "${file}" =~ _test\.go$ ]] && continue  # Skip test files for cleaner output

        local service=""
        if [[ "${file}" =~ services/([^/]+)/ ]]; then
            service="${BASH_REMATCH[1]}"
        fi

        [[ -z "${service}" ]] && continue

        kafka_patterns+=("$(cat <<EOF
    {
      "service": "${service}",
      "pattern_type": "producer_creation",
      "file": "${file}",
      "line": ${line_num},
      "message": "Service '${service}' creates Kafka producer"
    }
EOF
)")
    done < <(rg -n "kafka\.NewProtoProducer|NewProtoProducer" services --type go 2>/dev/null || true)

    # Look for Kafka consumer patterns
    while IFS=: read -r file line_num content; do
        [[ "${file}" =~ /vendor/ ]] && continue
        [[ "${file}" =~ _test\.go$ ]] && continue  # Skip test files for cleaner output

        local service=""
        if [[ "${file}" =~ services/([^/]+)/ ]]; then
            service="${BASH_REMATCH[1]}"
        fi

        [[ -z "${service}" ]] && continue

        kafka_patterns+=("$(cat <<EOF
    {
      "service": "${service}",
      "pattern_type": "consumer_creation",
      "file": "${file}",
      "line": ${line_num},
      "message": "Service '${service}' creates Kafka consumer"
    }
EOF
)")
    done < <(rg -n "kafka\.NewProtoConsumer|NewProtoConsumer" services --type go 2>/dev/null || true)

    # Look for EventPublisher (domain pattern)
    while IFS=: read -r file line_num content; do
        [[ "${file}" =~ /vendor/ ]] && continue
        [[ "${file}" =~ _test\.go$ ]] && continue

        local service=""
        if [[ "${file}" =~ services/([^/]+)/ ]]; then
            service="${BASH_REMATCH[1]}"
        fi

        [[ -z "${service}" ]] && continue

        kafka_patterns+=("$(cat <<EOF
    {
      "service": "${service}",
      "pattern_type": "event_publisher",
      "file": "${file}",
      "line": ${line_num},
      "message": "Service '${service}' uses EventPublisher interface"
    }
EOF
)")
    done < <(rg -n "EventPublisher|event_publisher" services --type go 2>/dev/null || true)
}
analyze_kafka_patterns

#
# Section 7: Generate JSON output
#
generate_json_output() {
    local timestamp
    timestamp="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"

    echo "{"
    echo "  \"analysis_timestamp\": \"${timestamp}\","
    echo "  \"repository_root\": \"${REPO_ROOT}\","
    echo "  \"services_analyzed\": [$(printf '"%s",' "${SERVICES[@]}" | sed 's/,$//')],"
    echo "  \"violations\": ["

    # Output violations
    if [[ ${#violations[@]} -gt 0 ]]; then
        local first=true
        for violation in "${violations[@]}"; do
            if [[ "${first}" == "true" ]]; then
                first=false
            else
                echo ","
            fi
            echo -n "${violation}"
        done
        echo ""
    fi

    echo "  ],"
    echo "  \"proto_usage\": ["

    # Output proto usage
    if [[ ${#proto_usage[@]} -gt 0 ]]; then
        local first=true
        for usage in "${proto_usage[@]}"; do
            if [[ "${first}" == "true" ]]; then
                first=false
            else
                echo ","
            fi
            echo -n "${usage}"
        done
        echo ""
    fi

    echo "  ],"
    echo "  \"grpc_clients\": ["

    # Output gRPC clients
    if [[ ${#grpc_clients[@]} -gt 0 ]]; then
        local first=true
        for client in "${grpc_clients[@]}"; do
            if [[ "${first}" == "true" ]]; then
                first=false
            else
                echo ","
            fi
            echo -n "${client}"
        done
        echo ""
    fi

    echo "  ],"
    echo "  \"database_schemas\": ["

    # Output database schemas
    if [[ ${#database_schemas[@]} -gt 0 ]]; then
        local first=true
        for schema in "${database_schemas[@]}"; do
            if [[ "${first}" == "true" ]]; then
                first=false
            else
                echo ","
            fi
            echo -n "${schema}"
        done
        echo ""
    fi

    echo "  ],"
    echo "  \"kafka_patterns\": ["

    # Output Kafka patterns
    if [[ ${#kafka_patterns[@]} -gt 0 ]]; then
        local first=true
        for pattern in "${kafka_patterns[@]}"; do
            if [[ "${first}" == "true" ]]; then
                first=false
            else
                echo ","
            fi
            echo -n "${pattern}"
        done
        echo ""
    fi

    echo "  ],"
    echo "  \"summary\": {"
    echo "    \"total_violations\": ${#violations[@]},"
    echo "    \"total_proto_usage\": ${#proto_usage[@]},"
    echo "    \"total_grpc_clients\": ${#grpc_clients[@]},"
    echo "    \"total_database_schemas\": ${#database_schemas[@]},"
    echo "    \"total_kafka_patterns\": ${#kafka_patterns[@]}"
    echo "  }"
    echo "}"
}

echo -e "${YELLOW}Generating JSON report...${NC}" >&2
generate_json_output

echo -e "${YELLOW}Analysis complete!${NC}" >&2
echo -e "${YELLOW}Summary: ${#violations[@]} violations, ${#proto_usage[@]} proto usage, ${#grpc_clients[@]} gRPC clients, ${#database_schemas[@]} database schemas, ${#kafka_patterns[@]} Kafka patterns${NC}" >&2
