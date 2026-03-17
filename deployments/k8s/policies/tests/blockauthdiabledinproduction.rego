# OPA Gatekeeper policy to prevent AUTH_ENABLED=false in production namespaces
# This file is the Rego policy extracted from the ConstraintTemplate for testing.
# The canonical source is auth-enabled-block.yaml - keep these in sync.

package blockauthdiabledinproduction

# =============================================================================
# ConfigMap violation rule
# =============================================================================

violation[{"msg": msg}] {
    # Check if the object is a ConfigMap
    input.review.object.kind == "ConfigMap"

    # Check if data field exists
    input.review.object.data

    # Check if AUTH_ENABLED is set to "false" (case-insensitive)
    lower(input.review.object.data.AUTH_ENABLED) == "false"

    # Get namespace name
    ns := input.review.object.metadata.namespace

    # Check if namespace matches production patterns (prod* or *production*)
    is_production_namespace(ns)

    msg := sprintf("ConfigMap '%s' in namespace '%s' cannot have AUTH_ENABLED=false. Authentication must be enabled in production namespaces.", [input.review.object.metadata.name, ns])
}

# =============================================================================
# Pod/workload environment variable violation rules
# Check for AUTH_ENABLED=false in container env vars for:
# - Pods, Deployments, StatefulSets, DaemonSets, Jobs, CronJobs
# =============================================================================

# Supported workload kinds that can contain container specs
workload_kinds := {"Pod", "Deployment", "StatefulSet", "DaemonSet", "Job", "CronJob", "ReplicaSet"}

# Helper to get the pod spec from different workload types
get_pod_spec(obj) = spec {
    obj.kind == "Pod"
    spec := obj.spec
}

get_pod_spec(obj) = spec {
    workload_kinds[obj.kind]
    obj.kind != "Pod"
    obj.kind != "CronJob"
    spec := obj.spec.template.spec
}

get_pod_spec(obj) = spec {
    obj.kind == "CronJob"
    spec := obj.spec.jobTemplate.spec.template.spec
}

# Check containers for AUTH_ENABLED=false env var
violation[{"msg": msg}] {
    obj := input.review.object
    workload_kinds[obj.kind]

    pod_spec := get_pod_spec(obj)
    container := pod_spec.containers[_]
    env_var := container.env[_]

    env_var.name == "AUTH_ENABLED"
    lower(env_var.value) == "false"

    ns := obj.metadata.namespace
    is_production_namespace(ns)

    msg := sprintf("%s '%s' in namespace '%s' has container '%s' with AUTH_ENABLED=false environment variable. Authentication must be enabled in production namespaces.", [obj.kind, obj.metadata.name, ns, container.name])
}

# Check initContainers for AUTH_ENABLED=false env var
violation[{"msg": msg}] {
    obj := input.review.object
    workload_kinds[obj.kind]

    pod_spec := get_pod_spec(obj)
    container := pod_spec.initContainers[_]
    env_var := container.env[_]

    env_var.name == "AUTH_ENABLED"
    lower(env_var.value) == "false"

    ns := obj.metadata.namespace
    is_production_namespace(ns)

    msg := sprintf("%s '%s' in namespace '%s' has initContainer '%s' with AUTH_ENABLED=false environment variable. Authentication must be enabled in production namespaces.", [obj.kind, obj.metadata.name, ns, container.name])
}

# Check ephemeralContainers for AUTH_ENABLED=false env var
violation[{"msg": msg}] {
    obj := input.review.object
    workload_kinds[obj.kind]

    pod_spec := get_pod_spec(obj)
    container := pod_spec.ephemeralContainers[_]
    env_var := container.env[_]

    env_var.name == "AUTH_ENABLED"
    lower(env_var.value) == "false"

    ns := obj.metadata.namespace
    is_production_namespace(ns)

    msg := sprintf("%s '%s' in namespace '%s' has ephemeralContainer '%s' with AUTH_ENABLED=false environment variable. Authentication must be enabled in production namespaces.", [obj.kind, obj.metadata.name, ns, container.name])
}

# =============================================================================
# Namespace detection helpers
# =============================================================================

# Match namespaces starting with 'prod'
is_production_namespace(ns) {
    startswith(ns, "prod")
}

# Match namespaces containing 'production'
is_production_namespace(ns) {
    contains(ns, "production")
}
