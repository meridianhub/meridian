# Saga: apply_manifest
# Version: 1.2.0
# Previous: 1.1.0
# Changed: Added Phase 5 for Operational Gateway configuration —
#          provider connections and instruction routes are applied
#          after all reference data and saga definitions are registered.
# Author: Platform Team
# Date: 2026-03-02
#
# This Starlark script defines the apply_manifest saga workflow for the Control Plane.
# It executes phased gRPC calls to provision tenant resources from a manifest definition.
#
# Phases (executed sequentially, parallel within each phase):
#   Phase 1: Register instruments with Reference Data service
#   Phase 2: Register account types + initiate internal accounts
#   Phase 3: Register valuation rules with Reference Data service
#   Phase 4: Register saga definitions with Reference Data service
#   Phase 5: Configure Operational Gateway (provider connections + instruction routes)
#
# Compensation Order (LIFO - Last In, First Out):
#   Failures trigger compensation of completed steps in reverse order.
#
# Input data (provided via input_data dictionary):
#   - manifest_version: string - The manifest version being applied
#   - instruments: list - List of instrument definitions to register
#   - account_types: list - List of account type definitions to register and provision
#   - valuation_rules: list - List of valuation rule definitions to register
#   - saga_definitions: list - List of saga definitions to register
#   - provider_connections: list - List of provider connection configs to upsert
#   - instruction_routes: list - List of instruction route configs to upsert

apply_manifest_saga = saga(name="apply_manifest")

def execute_apply_manifest():
    manifest_version = input_data["manifest_version"]
    instruments = input_data.get("instruments", [])
    account_types = input_data.get("account_types", [])
    valuation_rules = input_data.get("valuation_rules", [])
    saga_definitions = input_data.get("saga_definitions", [])
    provider_connections = input_data.get("provider_connections", [])
    instruction_routes = input_data.get("instruction_routes", [])

    registered_instruments = []
    registered_account_types = []
    registered_valuation_rules = []
    registered_saga_definitions = []
    registered_connections = []
    registered_routes = []

    # Phase 1: Register instruments (no dependencies)
    for instrument in instruments:
        step(name="register_instrument_" + instrument["code"])
        result = reference_data.register_instrument(
            instrument_code=instrument["code"],
            display_name=instrument.get("display_name", instrument["code"]),
            dimension=instrument.get("dimension", "CURRENCY"),
            decimal_places=instrument.get("decimal_places", 2),
            description=instrument.get("description", ""),
        )
        registered_instruments.append({
            "instrument_code": instrument["code"],
            "status": "REGISTERED",
        })

    # Phase 2: Register account types and initiate internal accounts
    for account_type in account_types:
        # Register the account type as reference data (idempotent: CreateDraft + Activate)
        step(name="register_account_type_" + account_type["code"])
        at_result = reference_data.register_account_type(
            code=account_type["code"],
            display_name=account_type.get("display_name", account_type["code"]),
            behavior_class=account_type.get("behavior_class", "CLEARING"),
            normal_balance=account_type.get("normal_balance", "DEBIT"),
            instrument_code=account_type["instrument_code"],
            description=account_type.get("description", ""),
            default_saga_prefix=account_type.get("default_saga_prefix", ""),
            default_conversion_method=account_type.get("default_conversion_method", ""),
            validation_cel=account_type.get("validation_cel", ""),
            eligibility_cel=account_type.get("eligibility_cel", ""),
            attribute_schema=account_type.get("attribute_schema", ""),
            valuation_methods=account_type.get("valuation_methods", []),
        )

        # Initiate the internal account
        step(name="initiate_account_" + account_type["code"])
        account_result = internal_account.initiate(
            account_code=account_type["code"],
            name=account_type.get("display_name", account_type["code"]),
            account_type=account_type.get("account_type", "CLEARING"),
            instrument_code=account_type["instrument_code"],
            description=account_type.get("description", ""),
        )

        registered_account_types.append({
            "account_type_code": account_type["code"],
            "account_id": account_result.account_id,
            "status": "REGISTERED",
        })

    # Phase 3: Register valuation rules
    for rule in valuation_rules:
        step(name="register_valuation_rule_" + rule["from_instrument"] + "_" + rule["to_instrument"])
        reference_data.register_valuation_rule(
            from_instrument=rule["from_instrument"],
            to_instrument=rule["to_instrument"],
            rule_type=rule.get("rule_type", "FIXED_RATE"),
            expression=rule.get("expression", ""),
            description=rule.get("description", ""),
        )
        registered_valuation_rules.append({
            "from_instrument": rule["from_instrument"],
            "to_instrument": rule["to_instrument"],
            "status": "REGISTERED",
        })

    # Phase 4: Register saga definitions
    for saga_def in saga_definitions:
        step(name="register_saga_definition_" + saga_def["name"])
        reference_data.register_saga_definition(
            saga_name=saga_def["name"],
            display_name=saga_def.get("display_name", saga_def["name"]),
            description=saga_def.get("description", ""),
            script=saga_def.get("script", ""),
            version=saga_def.get("version", "1.0.0"),
        )
        registered_saga_definitions.append({
            "saga_name": saga_def["name"],
            "status": "REGISTERED",
        })

    # Phase 5: Configure Operational Gateway
    # Provider connections must be upserted before instruction routes because
    # routes reference connections by connection_id.

    for conn in provider_connections:
        step(name="upsert_provider_connection_" + conn["connection_id"])
        result = operational_gateway.upsert_connection(
            connection_id=conn["connection_id"],
            provider_name=conn["provider_name"],
            provider_type=conn.get("provider_type", ""),
            protocol=conn["protocol"],
            base_url=conn["base_url"],
            auth_type=conn["auth_type"],
            auth_config=conn.get("auth_config", {}),
            retry_policy=conn.get("retry_policy", {}),
            rate_limit_config=conn.get("rate_limit_config", {}),
        )
        registered_connections.append({
            "connection_id": conn["connection_id"],
            "status": result.get("status", "UPSERTED"),
        })

    for route in instruction_routes:
        step(name="upsert_instruction_route_" + route["instruction_type"])
        result = operational_gateway.upsert_route(
            instruction_type=route["instruction_type"],
            connection_id=route["connection_id"],
            fallback_connection_id=route.get("fallback_connection_id", ""),
            outbound_mapping=route.get("outbound_mapping", ""),
            inbound_mapping=route.get("inbound_mapping", ""),
            http_method=route.get("http_method", ""),
            path_template=route.get("path_template", ""),
        )
        registered_routes.append({
            "instruction_type": route["instruction_type"],
            "status": result.get("status", "UPSERTED"),
        })

    result = {
        "status": "applied",
        "version": manifest_version,
        "instruments_registered": len(registered_instruments),
        "account_types_registered": len(registered_account_types),
        "valuation_rules_registered": len(registered_valuation_rules),
        "saga_definitions_registered": len(registered_saga_definitions),
        "provider_connections_upserted": len(registered_connections),
        "instruction_routes_upserted": len(registered_routes),
    }
    return result

# Execute the saga
execute_apply_manifest()
