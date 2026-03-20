# Saga: apply_manifest
# Version: 1.3.0
# Previous: 1.2.0
# Changed: Added all resource phases with correct dependency ordering:
#          - Phase 10: Instruments (register + activate)
#          - Phase 20: Account types (draft + activate)
#          - Phase 30: Market data sources
#          - Phase 35: Market data sets (register + activate)
#          - Phase 40: Valuation rules
#          - Phase 55: Organizations
#          - Phase 60: Internal accounts (explicit or auto-derived from account types)
#          - Phase 70: Saga definitions
#          - Phase 90: Operational gateway (connections + routes)
# Author: Platform Team
# Date: 2026-03-16
#
# This Starlark script defines the apply_manifest saga workflow for the Control Plane.
# It executes phased gRPC calls to provision tenant resources from a manifest definition.
#
# Phases (executed sequentially, parallel within each phase):
#   Phase 10: Register and activate instruments with Reference Data service
#   Phase 20: Register account types (CreateDraft + Activate)
#   Phase 30: Register market data sources
#   Phase 35: Register and activate market data sets
#   Phase 40: Register valuation rules
#   Phase 55: Register organizations with Party service
#   Phase 60: Initiate internal accounts (explicit or auto-derived from account types)
#   Phase 70: Register saga definitions
#   Phase 90: Configure Operational Gateway (provider connections + instruction routes)
#
# Compensation Order (LIFO - Last In, First Out):
#   Failures trigger compensation of completed steps in reverse order.
#
# Input data (provided via input_data dictionary):
#   - manifest_version: string - The manifest version being applied
#   - instruments: list - List of instrument definitions to register
#   - account_types: list - List of account type definitions to register
#   - market_data_sources: list - List of market data source definitions to register
#   - market_data_sets: list - List of market data set definitions to register
#   - valuation_rules: list - List of valuation rule definitions to register
#   - organizations: list - List of organization definitions to register
#   - internal_accounts: list - List of internal account definitions to initiate
#   - saga_definitions: list - List of saga definitions to register
#   - provider_connections: list - List of provider connection configs to upsert
#   - instruction_routes: list - List of instruction route configs to upsert

apply_manifest_saga = saga(name="apply_manifest")

def execute_apply_manifest():
    manifest_version = input_data["manifest_version"]
    instruments = input_data.get("instruments", [])
    account_types = input_data.get("account_types", [])
    market_data_sources = input_data.get("market_data_sources", [])
    market_data_sets = input_data.get("market_data_sets", [])
    valuation_rules = input_data.get("valuation_rules", [])
    organizations = input_data.get("organizations", [])
    internal_accounts = input_data.get("internal_accounts", [])
    saga_definitions = input_data.get("saga_definitions", [])
    provider_connections = input_data.get("provider_connections", [])
    instruction_routes = input_data.get("instruction_routes", [])

    registered_instruments = []
    registered_account_types = []
    registered_market_data_sources = []
    registered_market_data_sets = []
    registered_valuation_rules = []
    registered_organizations = []
    registered_internal_accounts = []
    registered_saga_definitions = []
    registered_connections = []
    registered_routes = []

    # Phase 10: Register and activate instruments (no dependencies)
    for instrument in instruments:
        step(name="register_instrument_" + instrument["code"])
        result = reference_data.register_instrument(
            instrument_code=instrument["code"],
            display_name=instrument.get("display_name", instrument["code"]),
            dimension=instrument.get("dimension", "CURRENCY"),
            decimal_places=instrument.get("decimal_places", 2),
            description=instrument.get("description", ""),
        )

        # Activate the instrument (DRAFT → ACTIVE)
        step(name="activate_instrument_" + instrument["code"])
        reference_data.activate_instrument(
            instrument_code=instrument["code"],
        )

        registered_instruments.append({
            "instrument_code": instrument["code"],
            "status": "ACTIVE",
        })

    # Phase 20: Register account types (depends on instruments from Phase 10)
    for account_type in account_types:
        step(name="register_account_type_" + account_type["code"])
        reference_data.register_account_type(
            code=account_type["code"],
            display_name=account_type.get("display_name", account_type["code"]),
            behavior_class=account_type.get("behavior_class", "CLEARING"),
            normal_balance=account_type.get("normal_balance", "DEBIT"),
            instrument_code=account_type.get("instrument_code", ""),
            description=account_type.get("description", ""),
            default_saga_prefix=account_type.get("default_saga_prefix", ""),
            default_conversion_method=account_type.get("default_conversion_method", ""),
            validation_cel=account_type.get("validation_cel", ""),
            eligibility_cel=account_type.get("eligibility_cel", ""),
            attribute_schema=account_type.get("attribute_schema", ""),
            valuation_methods=account_type.get("valuation_methods", []),
        )

        registered_account_types.append({
            "account_type_code": account_type["code"],
            "status": "REGISTERED",
        })

    # Phase 30: Register market data sources (no dependency on instruments/account types)
    for source in market_data_sources:
        step(name="register_data_source_" + source["code"])
        market_information.register_data_source(
            code=source["code"],
            name=source.get("name", source["code"]),
            description=source.get("description", ""),
            trust_level=source.get("trust_level", 0),
        )
        registered_market_data_sources.append({
            "code": source["code"],
            "status": "REGISTERED",
        })

    # Phase 35: Register and activate market data sets (depends on data sources from Phase 30)
    for dataset in market_data_sets:
        step(name="register_data_set_" + dataset["code"])
        ds_result = market_information.register_data_set(
            code=dataset["code"],
            category=dataset.get("category", ""),
            unit=dataset.get("unit", ""),
            source_code=dataset.get("source_code", ""),
            display_name=dataset.get("display_name", dataset["code"]),
            description=dataset.get("description", ""),
            validation_expression=dataset.get("validation_expression", "true"),
            resolution_key_expression=dataset.get("resolution_key_expression", "observed_at"),
        )

        step(name="activate_data_set_" + dataset["code"])
        market_information.activate_data_set(
            code=dataset["code"],
            version=getattr(ds_result, "version", 1),
        )

        registered_market_data_sets.append({
            "code": dataset["code"],
            "status": "ACTIVE",
        })

    # Phase 40: Register valuation rules (depends on instruments from Phase 10)
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

    # Phase 55: Register organizations with Party service
    for org in organizations:
        step(name="register_organization_" + org["code"])
        org_name = org.get("name", org["code"])
        party.register_organization(
            legal_name=org_name,
            display_name=org_name,
            party_type=org.get("party_type", "ORGANIZATION"),
            external_reference=org.get("code", ""),
            external_reference_type=org.get("external_reference_type", "COMPANIES_HOUSE"),
            attributes=org.get("attributes", {}),
        )
        registered_organizations.append({
            "code": org["code"],
            "status": "REGISTERED",
        })

    # Phase 60: Initiate internal accounts
    # Backward compatibility: if no explicit internal_accounts, auto-derive from account types
    effective_internal_accounts = internal_accounts
    if len(effective_internal_accounts) == 0:
        for account_type in account_types:
            effective_internal_accounts.append({
                "code": account_type["code"],
                "account_type": account_type.get("account_type", "CLEARING"),
                "instrument_code": account_type.get("instrument_code", ""),
                "description": account_type.get("description", ""),
            })

    for ia in effective_internal_accounts:
        step(name="initiate_account_" + ia["code"])
        account_result = internal_account.initiate(
            account_code=ia["code"],
            name=ia.get("name", ia["code"]),
            account_type=ia.get("account_type", "CLEARING"),
            instrument_code=ia.get("instrument_code", ""),
            description=ia.get("description", ""),
            owner_organization=ia.get("owner_organization", ""),
        )
        registered_internal_accounts.append({
            "account_code": ia["code"],
            "account_id": account_result.account_id,
            "status": "ACTIVE",
        })

    # Phase 70: Register saga definitions
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

    # Phase 90: Configure Operational Gateway
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
            "status": getattr(result, "status", "UPSERTED"),
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
            "status": getattr(result, "status", "UPSERTED"),
        })

    result = {
        "status": "applied",
        "version": manifest_version,
        "instruments_registered": len(registered_instruments),
        "account_types_registered": len(registered_account_types),
        "market_data_sources_registered": len(registered_market_data_sources),
        "market_data_sets_registered": len(registered_market_data_sets),
        "valuation_rules_registered": len(registered_valuation_rules),
        "organizations_registered": len(registered_organizations),
        "internal_accounts_initiated": len(registered_internal_accounts),
        "saga_definitions_registered": len(registered_saga_definitions),
        "provider_connections_upserted": len(registered_connections),
        "instruction_routes_upserted": len(registered_routes),
    }
    return result

# Execute the saga
execute_apply_manifest()
