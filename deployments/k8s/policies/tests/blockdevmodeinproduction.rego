# OPA Gatekeeper policy to prevent LOCAL_DEV_MODE=true in production namespaces
# This file is the Rego policy extracted from the ConstraintTemplate for testing.
# The canonical source is gateway-dev-mode-block.yaml - keep these in sync.

package blockdevmodeinproduction

violation[{"msg": msg}] {
    # Check if the object is a ConfigMap
    input.review.object.kind == "ConfigMap"

    # Check if LOCAL_DEV_MODE is set to "true"
    input.review.object.data.LOCAL_DEV_MODE == "true"

    # Get namespace name
    ns := input.review.object.metadata.namespace

    # Check if namespace matches production patterns (prod* or *production*)
    is_production_namespace(ns)

    msg := sprintf("ConfigMap '%s' in namespace '%s' cannot have LOCAL_DEV_MODE=true. Development mode is not allowed in production namespaces.", [input.review.object.metadata.name, ns])
}

# Match namespaces starting with 'prod'
is_production_namespace(ns) {
    startswith(ns, "prod")
}

# Match namespaces containing 'production'
is_production_namespace(ns) {
    contains(ns, "production")
}
