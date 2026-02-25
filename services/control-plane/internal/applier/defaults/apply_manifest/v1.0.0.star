# Saga: apply_manifest
# Version: 1.0.0
# Previous: none
# Changed: Initial implementation of ApplyManifest saga with phased execution
# Author: Platform Team
# Date: 2026-02-09
#
# This Starlark script defines the apply_manifest saga workflow for the Control Plane.
# It executes phased gRPC calls to provision tenant resources from a manifest definition.
#
# Phases (executed sequentially, parallel within each phase):
#   Phase 1: Register instruments with Reference Data service
#   Phase 2: Register account types + initiate internal accounts
#   Phase 3: Register valuation rules with Reference Data service
#   Phase 4: Register saga definitions with Reference Data service
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

apply_manifest_saga = saga(name="apply_manifest")

def execute_apply_manifest():
    manifest_version = input_data["manifest_version"]
    instruments = input_data.get("instruments", [])
    account_types = input_data.get("account_types", [])
    valuation_rules = input_data.get("valuation_rules", [])
    saga_definitions = input_data.get("saga_definitions", [])

    registered_instruments = []
    registered_account_types = []
    registered_valuation_rules = []
    registered_saga_definitions = []

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
        # Register the account type as reference data
        step(name="register_account_type_" + account_type["code"])
        reference_data.register_account_type(
            account_type_code=account_type["code"],
            display_name=account_type.get("display_name", account_type["code"]),
            description=account_type.get("description", ""),
            instrument_code=account_type["instrument_code"],
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

    result = {
        "status": "applied",
        "version": manifest_version,
        "instruments_registered": len(registered_instruments),
        "account_types_registered": len(registered_account_types),
        "valuation_rules_registered": len(registered_valuation_rules),
        "saga_definitions_registered": len(registered_saga_definitions),
    }
    return result

# Execute the saga
execute_apply_manifest()
