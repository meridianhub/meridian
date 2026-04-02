# Saga: apply_manifest
# Version: 1.4.0
# Previous: 1.3.0
# Changed: Action-aware execution. Each resource checks its "action" field
#          (set by the live-state diff planner) and routes to the correct
#          handler: CREATE runs register+activate, UPDATE runs update,
#          DEPRECATE runs deprecate. Resources without an action field
#          default to CREATE for backward compatibility.
# Author: Platform Team
# Date: 2026-04-02
#
# This Starlark script defines the apply_manifest saga workflow for the Control Plane.
# It executes phased gRPC calls to provision tenant resources from a manifest definition.
#
# Phases (executed sequentially, parallel within each phase):
#   Phase 10: Instruments (CREATE: register+activate, UPDATE: update)
#   Phase 20: Account types (CREATE: draft+activate, UPDATE: update)
#   Phase 30: Market data sources (CREATE: register, UPDATE: update)
#   Phase 35: Market data sets (CREATE: register+activate, UPDATE: update)
#   Phase 40: Valuation rules (CREATE: register, UPDATE: update)
#   Phase 55: Organizations (CREATE: register, UPDATE: register [idempotent])
#   Phase 60: Internal accounts (CREATE: initiate, UPDATE: update)
#   Phase 70: Saga definitions (CREATE: register, UPDATE: update)
#   Phase 90: Operational gateway (upsert for both CREATE and UPDATE)

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

    # Phase 10: Instruments
    for instrument in instruments:
        action = instrument.get("action", "CREATE")
        if action == "UPDATE":
            step(name="update_instrument_" + instrument["code"])
            reference_data.update_instrument(
                instrument_code=instrument["code"],
                display_name=instrument.get("display_name", instrument["code"]),
                dimension=instrument.get("dimension", "CURRENCY"),
                decimal_places=instrument.get("decimal_places", 2),
                description=instrument.get("description", ""),
            )
        else:
            step(name="register_instrument_" + instrument["code"])
            result = reference_data.register_instrument(
                instrument_code=instrument["code"],
                display_name=instrument.get("display_name", instrument["code"]),
                dimension=instrument.get("dimension", "CURRENCY"),
                decimal_places=instrument.get("decimal_places", 2),
                description=instrument.get("description", ""),
            )

            step(name="activate_instrument_" + instrument["code"])
            reference_data.activate_instrument(
                instrument_code=instrument["code"],
            )

        registered_instruments.append({
            "instrument_code": instrument["code"],
            "status": "ACTIVE",
        })

    # Phase 20: Account types
    for account_type in account_types:
        action = account_type.get("action", "CREATE")
        if action == "UPDATE":
            step(name="update_account_type_" + account_type["code"])
            reference_data.update_account_type(
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
        else:
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

    # Phase 30: Market data sources
    for source in market_data_sources:
        action = source.get("action", "CREATE")
        if action == "UPDATE":
            step(name="update_data_source_" + source["code"])
            market_information.update_data_source(
                code=source["code"],
                name=source.get("name", source["code"]),
                description=source.get("description", ""),
                trust_level=source.get("trust_level", 0),
            )
        else:
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

    # Phase 35: Market data sets
    for dataset in market_data_sets:
        action = dataset.get("action", "CREATE")
        if action == "UPDATE":
            step(name="update_data_set_" + dataset["code"])
            market_information.update_data_set(
                code=dataset["code"],
                category=dataset.get("category", ""),
                unit=dataset.get("unit", ""),
                source_code=dataset.get("source_code", ""),
                display_name=dataset.get("display_name", dataset["code"]),
                description=dataset.get("description", ""),
                validation_expression=dataset.get("validation_expression", "") or "true",
                resolution_key_expression=dataset.get("resolution_key_expression", "") or "observed_at",
            )
        else:
            step(name="register_data_set_" + dataset["code"])
            ds_result = market_information.register_data_set(
                code=dataset["code"],
                category=dataset.get("category", ""),
                unit=dataset.get("unit", ""),
                source_code=dataset.get("source_code", ""),
                display_name=dataset.get("display_name", dataset["code"]),
                description=dataset.get("description", ""),
                validation_expression=dataset.get("validation_expression", "") or "true",
                resolution_key_expression=dataset.get("resolution_key_expression", "") or "observed_at",
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

    # Phase 40: Valuation rules
    for rule in valuation_rules:
        action = rule.get("action", "CREATE")
        if action == "UPDATE":
            step(name="update_valuation_rule_" + rule["from_instrument"] + "_" + rule["to_instrument"])
            reference_data.update_instrument(
                from_instrument=rule["from_instrument"],
                to_instrument=rule["to_instrument"],
                rule_type=rule.get("rule_type", "FIXED_RATE"),
                expression=rule.get("expression", ""),
                description=rule.get("description", ""),
            )
        else:
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

    # Phase 55: Organizations
    for org in organizations:
        step(name="register_organization_" + org["code"])
        party.register_organization(
            legal_name=org.get("legal_name", org.get("name", org["code"])),
            display_name=org.get("display_name", org.get("legal_name", org.get("name", org["code"]))),
            party_type=org.get("party_type", "ORGANIZATION"),
            external_reference=org.get("external_reference", org["code"]),
            external_reference_type=org.get("external_reference_type", ""),
            attributes=org.get("attributes", {}),
        )
        registered_organizations.append({
            "code": org["code"],
            "status": "REGISTERED",
        })

    # Phase 60: Internal accounts
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
        action = ia.get("action", "CREATE")
        if action == "UPDATE":
            step(name="update_account_" + ia["code"])
            internal_account.update(
                account_code=ia["code"],
                name=ia.get("name", ia["code"]),
                account_type=ia.get("account_type", "CLEARING"),
                instrument_code=ia.get("instrument_code", ""),
                description=ia.get("description", ""),
                owner_organization=ia.get("owner_organization", ""),
            )
            registered_internal_accounts.append({
                "account_code": ia["code"],
                "status": "UPDATED",
            })
        else:
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

    # Phase 70: Saga definitions
    for saga_def in saga_definitions:
        action = saga_def.get("action", "CREATE")
        if action == "UPDATE":
            step(name="update_saga_definition_" + saga_def["name"])
            reference_data.update_saga_definition(
                saga_name=saga_def["name"],
                display_name=saga_def.get("display_name", saga_def["name"]),
                description=saga_def.get("description", ""),
                script=saga_def.get("script", ""),
                version=saga_def.get("version", "1.0.0"),
            )
        else:
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

    # Phase 90: Operational gateway (upsert handles both CREATE and UPDATE)
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
