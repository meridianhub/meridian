# Tests for the BlockDevModeInProduction OPA Gatekeeper policy
# Run with: opa test deployments/k8s/policies/tests/ -v

package blockdevmodeinproduction

# Import the policy module
import data.blockdevmodeinproduction

# =============================================================================
# Test: Production namespace detection
# =============================================================================

# Test: "prod" namespace is detected as production
test_is_production_namespace_prod {
    is_production_namespace("prod")
}

# Test: "production" namespace is detected as production
test_is_production_namespace_production {
    is_production_namespace("production")
}

# Test: "prod-eu" namespace (starts with prod) is production
test_is_production_namespace_prod_eu {
    is_production_namespace("prod-eu")
}

# Test: "prod-us-west" namespace is production
test_is_production_namespace_prod_us_west {
    is_production_namespace("prod-us-west")
}

# Test: "my-production-service" namespace (contains production) is production
test_is_production_namespace_contains_production {
    is_production_namespace("my-production-service")
}

# Test: "production-critical" namespace is production
test_is_production_namespace_production_critical {
    is_production_namespace("production-critical")
}

# Test: "dev" namespace is NOT production
test_is_not_production_namespace_dev {
    not is_production_namespace("dev")
}

# Test: "staging" namespace is NOT production
test_is_not_production_namespace_staging {
    not is_production_namespace("staging")
}

# Test: "default" namespace is NOT production
test_is_not_production_namespace_default {
    not is_production_namespace("default")
}

# Test: "meridian-dev" namespace is NOT production
test_is_not_production_namespace_meridian_dev {
    not is_production_namespace("meridian-dev")
}

# Test: "product" namespace (starts with "prod" but different word) IS detected
# Note: This is by design - false positives are safer than false negatives
test_is_production_namespace_product {
    is_production_namespace("product")
}

# =============================================================================
# Test: ConfigMap with LOCAL_DEV_MODE=true in production should be blocked
# =============================================================================

test_violation_local_dev_mode_true_in_prod {
    input := {
        "review": {
            "object": {
                "kind": "ConfigMap",
                "metadata": {
                    "name": "gateway-config",
                    "namespace": "prod"
                },
                "data": {
                    "LOCAL_DEV_MODE": "true"
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 1
}

test_violation_local_dev_mode_true_in_production_namespace {
    input := {
        "review": {
            "object": {
                "kind": "ConfigMap",
                "metadata": {
                    "name": "gateway-config",
                    "namespace": "production"
                },
                "data": {
                    "LOCAL_DEV_MODE": "true"
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 1
}

test_violation_local_dev_mode_true_in_prod_eu {
    input := {
        "review": {
            "object": {
                "kind": "ConfigMap",
                "metadata": {
                    "name": "gateway-config",
                    "namespace": "prod-eu"
                },
                "data": {
                    "LOCAL_DEV_MODE": "true"
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 1
}

# =============================================================================
# Test: Case-insensitive LOCAL_DEV_MODE value checking
# =============================================================================

test_violation_local_dev_mode_True_in_prod {
    input := {
        "review": {
            "object": {
                "kind": "ConfigMap",
                "metadata": {
                    "name": "gateway-config",
                    "namespace": "prod"
                },
                "data": {
                    "LOCAL_DEV_MODE": "True"
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 1
}

test_violation_local_dev_mode_TRUE_in_prod {
    input := {
        "review": {
            "object": {
                "kind": "ConfigMap",
                "metadata": {
                    "name": "gateway-config",
                    "namespace": "prod"
                },
                "data": {
                    "LOCAL_DEV_MODE": "TRUE"
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 1
}

test_violation_local_dev_mode_TrUe_in_prod {
    input := {
        "review": {
            "object": {
                "kind": "ConfigMap",
                "metadata": {
                    "name": "gateway-config",
                    "namespace": "prod"
                },
                "data": {
                    "LOCAL_DEV_MODE": "TrUe"
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 1
}

# =============================================================================
# Test: ConfigMap with LOCAL_DEV_MODE=true in dev/staging should be allowed
# =============================================================================

test_no_violation_local_dev_mode_true_in_dev {
    input := {
        "review": {
            "object": {
                "kind": "ConfigMap",
                "metadata": {
                    "name": "gateway-config",
                    "namespace": "dev"
                },
                "data": {
                    "LOCAL_DEV_MODE": "true"
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 0
}

test_no_violation_local_dev_mode_true_in_staging {
    input := {
        "review": {
            "object": {
                "kind": "ConfigMap",
                "metadata": {
                    "name": "gateway-config",
                    "namespace": "staging"
                },
                "data": {
                    "LOCAL_DEV_MODE": "true"
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 0
}

test_no_violation_local_dev_mode_true_in_default {
    input := {
        "review": {
            "object": {
                "kind": "ConfigMap",
                "metadata": {
                    "name": "gateway-config",
                    "namespace": "default"
                },
                "data": {
                    "LOCAL_DEV_MODE": "true"
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 0
}

# =============================================================================
# Test: ConfigMap without LOCAL_DEV_MODE or with false value should be allowed
# =============================================================================

test_no_violation_local_dev_mode_false_in_prod {
    input := {
        "review": {
            "object": {
                "kind": "ConfigMap",
                "metadata": {
                    "name": "gateway-config",
                    "namespace": "prod"
                },
                "data": {
                    "LOCAL_DEV_MODE": "false"
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 0
}

test_no_violation_no_local_dev_mode_in_prod {
    input := {
        "review": {
            "object": {
                "kind": "ConfigMap",
                "metadata": {
                    "name": "gateway-config",
                    "namespace": "prod"
                },
                "data": {
                    "SOME_OTHER_CONFIG": "value"
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 0
}

test_no_violation_empty_configmap_in_prod {
    input := {
        "review": {
            "object": {
                "kind": "ConfigMap",
                "metadata": {
                    "name": "gateway-config",
                    "namespace": "prod"
                },
                "data": {}
            }
        }
    }

    violations := violation with input as input
    count(violations) == 0
}

# Test: ConfigMap with only binaryData (no data field) should not trigger violations
test_no_violation_configmap_with_only_binarydata_in_prod {
    input := {
        "review": {
            "object": {
                "kind": "ConfigMap",
                "metadata": {
                    "name": "gateway-config",
                    "namespace": "prod"
                },
                "binaryData": {
                    "config.bin": "YmluYXJ5IGRhdGE="
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 0
}

# =============================================================================
# Test: Non-ConfigMap objects should not trigger violations
# =============================================================================

test_no_violation_deployment_in_prod {
    input := {
        "review": {
            "object": {
                "kind": "Deployment",
                "metadata": {
                    "name": "gateway",
                    "namespace": "prod"
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 0
}

test_no_violation_secret_with_local_dev_mode_in_prod {
    input := {
        "review": {
            "object": {
                "kind": "Secret",
                "metadata": {
                    "name": "gateway-secret",
                    "namespace": "prod"
                },
                "data": {
                    "LOCAL_DEV_MODE": "true"
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 0
}

# =============================================================================
# Test: Violation message is clear and actionable
# =============================================================================

test_violation_message_contains_configmap_name {
    input := {
        "review": {
            "object": {
                "kind": "ConfigMap",
                "metadata": {
                    "name": "my-gateway-config",
                    "namespace": "prod"
                },
                "data": {
                    "LOCAL_DEV_MODE": "true"
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 1
    some v
    violations[v]
    contains(v.msg, "my-gateway-config")
}

test_violation_message_contains_namespace {
    input := {
        "review": {
            "object": {
                "kind": "ConfigMap",
                "metadata": {
                    "name": "gateway-config",
                    "namespace": "prod-eu"
                },
                "data": {
                    "LOCAL_DEV_MODE": "true"
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 1
    some v
    violations[v]
    contains(v.msg, "prod-eu")
}

test_violation_message_mentions_dev_mode_not_allowed {
    input := {
        "review": {
            "object": {
                "kind": "ConfigMap",
                "metadata": {
                    "name": "gateway-config",
                    "namespace": "prod"
                },
                "data": {
                    "LOCAL_DEV_MODE": "true"
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 1
    some v
    violations[v]
    contains(v.msg, "LOCAL_DEV_MODE=true")
    contains(v.msg, "not allowed in production")
}

# =============================================================================
# Test: Pod/workload container env var with LOCAL_DEV_MODE=true
# =============================================================================

test_violation_pod_container_env_local_dev_mode_true_in_prod {
    input := {
        "review": {
            "object": {
                "kind": "Pod",
                "metadata": {
                    "name": "gateway-pod",
                    "namespace": "prod"
                },
                "spec": {
                    "containers": [{
                        "name": "gateway",
                        "image": "gateway:latest",
                        "env": [{
                            "name": "LOCAL_DEV_MODE",
                            "value": "true"
                        }]
                    }]
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 1
}

test_violation_deployment_container_env_local_dev_mode_true_in_prod {
    input := {
        "review": {
            "object": {
                "kind": "Deployment",
                "metadata": {
                    "name": "gateway-deployment",
                    "namespace": "prod"
                },
                "spec": {
                    "template": {
                        "spec": {
                            "containers": [{
                                "name": "gateway",
                                "image": "gateway:latest",
                                "env": [{
                                    "name": "LOCAL_DEV_MODE",
                                    "value": "true"
                                }]
                            }]
                        }
                    }
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 1
}

test_violation_statefulset_container_env_local_dev_mode_true_in_prod {
    input := {
        "review": {
            "object": {
                "kind": "StatefulSet",
                "metadata": {
                    "name": "gateway-sts",
                    "namespace": "production"
                },
                "spec": {
                    "template": {
                        "spec": {
                            "containers": [{
                                "name": "gateway",
                                "image": "gateway:latest",
                                "env": [{
                                    "name": "LOCAL_DEV_MODE",
                                    "value": "True"
                                }]
                            }]
                        }
                    }
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 1
}

test_violation_daemonset_container_env_local_dev_mode_true_in_prod {
    input := {
        "review": {
            "object": {
                "kind": "DaemonSet",
                "metadata": {
                    "name": "gateway-ds",
                    "namespace": "prod-eu"
                },
                "spec": {
                    "template": {
                        "spec": {
                            "containers": [{
                                "name": "gateway",
                                "image": "gateway:latest",
                                "env": [{
                                    "name": "LOCAL_DEV_MODE",
                                    "value": "TRUE"
                                }]
                            }]
                        }
                    }
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 1
}

test_violation_job_container_env_local_dev_mode_true_in_prod {
    input := {
        "review": {
            "object": {
                "kind": "Job",
                "metadata": {
                    "name": "gateway-job",
                    "namespace": "prod"
                },
                "spec": {
                    "template": {
                        "spec": {
                            "containers": [{
                                "name": "gateway",
                                "image": "gateway:latest",
                                "env": [{
                                    "name": "LOCAL_DEV_MODE",
                                    "value": "true"
                                }]
                            }]
                        }
                    }
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 1
}

test_violation_cronjob_container_env_local_dev_mode_true_in_prod {
    input := {
        "review": {
            "object": {
                "kind": "CronJob",
                "metadata": {
                    "name": "gateway-cronjob",
                    "namespace": "prod"
                },
                "spec": {
                    "jobTemplate": {
                        "spec": {
                            "template": {
                                "spec": {
                                    "containers": [{
                                        "name": "gateway",
                                        "image": "gateway:latest",
                                        "env": [{
                                            "name": "LOCAL_DEV_MODE",
                                            "value": "true"
                                        }]
                                    }]
                                }
                            }
                        }
                    }
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 1
}

# =============================================================================
# Test: initContainers with LOCAL_DEV_MODE=true
# =============================================================================

test_violation_pod_initcontainer_env_local_dev_mode_true_in_prod {
    input := {
        "review": {
            "object": {
                "kind": "Pod",
                "metadata": {
                    "name": "gateway-pod",
                    "namespace": "prod"
                },
                "spec": {
                    "containers": [{
                        "name": "gateway",
                        "image": "gateway:latest"
                    }],
                    "initContainers": [{
                        "name": "init-gateway",
                        "image": "gateway-init:latest",
                        "env": [{
                            "name": "LOCAL_DEV_MODE",
                            "value": "true"
                        }]
                    }]
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 1
}

test_violation_deployment_initcontainer_env_local_dev_mode_true_in_prod {
    input := {
        "review": {
            "object": {
                "kind": "Deployment",
                "metadata": {
                    "name": "gateway-deployment",
                    "namespace": "prod"
                },
                "spec": {
                    "template": {
                        "spec": {
                            "containers": [{
                                "name": "gateway",
                                "image": "gateway:latest"
                            }],
                            "initContainers": [{
                                "name": "init-gateway",
                                "image": "gateway-init:latest",
                                "env": [{
                                    "name": "LOCAL_DEV_MODE",
                                    "value": "true"
                                }]
                            }]
                        }
                    }
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 1
}

# =============================================================================
# Test: ephemeralContainers with LOCAL_DEV_MODE=true
# =============================================================================

test_violation_pod_ephemeralcontainer_env_local_dev_mode_true_in_prod {
    input := {
        "review": {
            "object": {
                "kind": "Pod",
                "metadata": {
                    "name": "gateway-pod",
                    "namespace": "prod"
                },
                "spec": {
                    "containers": [{
                        "name": "gateway",
                        "image": "gateway:latest"
                    }],
                    "ephemeralContainers": [{
                        "name": "debug-container",
                        "image": "debug:latest",
                        "env": [{
                            "name": "LOCAL_DEV_MODE",
                            "value": "true"
                        }]
                    }]
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 1
}

# =============================================================================
# Test: Workloads with LOCAL_DEV_MODE=true in dev/staging should be allowed
# =============================================================================

test_no_violation_pod_container_env_local_dev_mode_true_in_dev {
    input := {
        "review": {
            "object": {
                "kind": "Pod",
                "metadata": {
                    "name": "gateway-pod",
                    "namespace": "dev"
                },
                "spec": {
                    "containers": [{
                        "name": "gateway",
                        "image": "gateway:latest",
                        "env": [{
                            "name": "LOCAL_DEV_MODE",
                            "value": "true"
                        }]
                    }]
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 0
}

test_no_violation_deployment_container_env_local_dev_mode_true_in_staging {
    input := {
        "review": {
            "object": {
                "kind": "Deployment",
                "metadata": {
                    "name": "gateway-deployment",
                    "namespace": "staging"
                },
                "spec": {
                    "template": {
                        "spec": {
                            "containers": [{
                                "name": "gateway",
                                "image": "gateway:latest",
                                "env": [{
                                    "name": "LOCAL_DEV_MODE",
                                    "value": "true"
                                }]
                            }]
                        }
                    }
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 0
}

# =============================================================================
# Test: Workloads with LOCAL_DEV_MODE=false or missing should be allowed
# =============================================================================

test_no_violation_pod_container_env_local_dev_mode_false_in_prod {
    input := {
        "review": {
            "object": {
                "kind": "Pod",
                "metadata": {
                    "name": "gateway-pod",
                    "namespace": "prod"
                },
                "spec": {
                    "containers": [{
                        "name": "gateway",
                        "image": "gateway:latest",
                        "env": [{
                            "name": "LOCAL_DEV_MODE",
                            "value": "false"
                        }]
                    }]
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 0
}

test_no_violation_pod_container_no_local_dev_mode_env_in_prod {
    input := {
        "review": {
            "object": {
                "kind": "Pod",
                "metadata": {
                    "name": "gateway-pod",
                    "namespace": "prod"
                },
                "spec": {
                    "containers": [{
                        "name": "gateway",
                        "image": "gateway:latest",
                        "env": [{
                            "name": "OTHER_ENV",
                            "value": "some-value"
                        }]
                    }]
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 0
}

test_no_violation_deployment_container_no_env_in_prod {
    input := {
        "review": {
            "object": {
                "kind": "Deployment",
                "metadata": {
                    "name": "gateway-deployment",
                    "namespace": "prod"
                },
                "spec": {
                    "template": {
                        "spec": {
                            "containers": [{
                                "name": "gateway",
                                "image": "gateway:latest"
                            }]
                        }
                    }
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 0
}

# =============================================================================
# Test: Multiple violations in same workload
# =============================================================================

test_multiple_violations_multiple_containers_with_local_dev_mode_in_prod {
    input := {
        "review": {
            "object": {
                "kind": "Deployment",
                "metadata": {
                    "name": "gateway-deployment",
                    "namespace": "prod"
                },
                "spec": {
                    "template": {
                        "spec": {
                            "containers": [
                                {
                                    "name": "gateway",
                                    "image": "gateway:latest",
                                    "env": [{
                                        "name": "LOCAL_DEV_MODE",
                                        "value": "true"
                                    }]
                                },
                                {
                                    "name": "sidecar",
                                    "image": "sidecar:latest",
                                    "env": [{
                                        "name": "LOCAL_DEV_MODE",
                                        "value": "true"
                                    }]
                                }
                            ]
                        }
                    }
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 2
}

# =============================================================================
# Test: Workload violation message contains relevant information
# =============================================================================

test_workload_violation_message_contains_kind_and_name {
    input := {
        "review": {
            "object": {
                "kind": "Deployment",
                "metadata": {
                    "name": "my-gateway-deployment",
                    "namespace": "prod"
                },
                "spec": {
                    "template": {
                        "spec": {
                            "containers": [{
                                "name": "gateway-container",
                                "image": "gateway:latest",
                                "env": [{
                                    "name": "LOCAL_DEV_MODE",
                                    "value": "true"
                                }]
                            }]
                        }
                    }
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 1
    some v
    violations[v]
    contains(v.msg, "Deployment")
    contains(v.msg, "my-gateway-deployment")
    contains(v.msg, "gateway-container")
    contains(v.msg, "prod")
}
