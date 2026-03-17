# Tests for the BlockAuthDisabledInProduction OPA Gatekeeper policy
# Run with: opa test deployments/k8s/policies/tests/ --v0-compatible -v

package blockauthdiabledinproduction

# =============================================================================
# Test: Production namespace detection
# =============================================================================

test_is_production_namespace_prod {
    is_production_namespace("prod")
}

test_is_production_namespace_production {
    is_production_namespace("production")
}

test_is_production_namespace_prod_eu {
    is_production_namespace("prod-eu")
}

test_is_production_namespace_prod_us_west {
    is_production_namespace("prod-us-west")
}

test_is_production_namespace_contains_production {
    is_production_namespace("my-production-service")
}

test_is_not_production_namespace_dev {
    not is_production_namespace("dev")
}

test_is_not_production_namespace_staging {
    not is_production_namespace("staging")
}

test_is_not_production_namespace_default {
    not is_production_namespace("default")
}

# =============================================================================
# Test: ConfigMap with AUTH_ENABLED=false in production should be blocked
# =============================================================================

test_violation_auth_enabled_false_in_prod {
    input := {
        "review": {
            "object": {
                "kind": "ConfigMap",
                "metadata": {
                    "name": "service-config",
                    "namespace": "prod"
                },
                "data": {
                    "AUTH_ENABLED": "false"
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 1
}

test_violation_auth_enabled_false_in_production_namespace {
    input := {
        "review": {
            "object": {
                "kind": "ConfigMap",
                "metadata": {
                    "name": "service-config",
                    "namespace": "production"
                },
                "data": {
                    "AUTH_ENABLED": "false"
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 1
}

test_violation_auth_enabled_false_in_prod_eu {
    input := {
        "review": {
            "object": {
                "kind": "ConfigMap",
                "metadata": {
                    "name": "service-config",
                    "namespace": "prod-eu"
                },
                "data": {
                    "AUTH_ENABLED": "false"
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 1
}

# =============================================================================
# Test: Case-insensitive AUTH_ENABLED value checking
# =============================================================================

test_violation_auth_enabled_False_in_prod {
    input := {
        "review": {
            "object": {
                "kind": "ConfigMap",
                "metadata": {
                    "name": "service-config",
                    "namespace": "prod"
                },
                "data": {
                    "AUTH_ENABLED": "False"
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 1
}

test_violation_auth_enabled_FALSE_in_prod {
    input := {
        "review": {
            "object": {
                "kind": "ConfigMap",
                "metadata": {
                    "name": "service-config",
                    "namespace": "prod"
                },
                "data": {
                    "AUTH_ENABLED": "FALSE"
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 1
}

# =============================================================================
# Test: ConfigMap with AUTH_ENABLED=false in dev/staging should be allowed
# =============================================================================

test_no_violation_auth_enabled_false_in_dev {
    input := {
        "review": {
            "object": {
                "kind": "ConfigMap",
                "metadata": {
                    "name": "service-config",
                    "namespace": "dev"
                },
                "data": {
                    "AUTH_ENABLED": "false"
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 0
}

test_no_violation_auth_enabled_false_in_staging {
    input := {
        "review": {
            "object": {
                "kind": "ConfigMap",
                "metadata": {
                    "name": "service-config",
                    "namespace": "staging"
                },
                "data": {
                    "AUTH_ENABLED": "false"
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 0
}

# =============================================================================
# Test: ConfigMap with AUTH_ENABLED=true or missing should be allowed in prod
# =============================================================================

test_no_violation_auth_enabled_true_in_prod {
    input := {
        "review": {
            "object": {
                "kind": "ConfigMap",
                "metadata": {
                    "name": "service-config",
                    "namespace": "prod"
                },
                "data": {
                    "AUTH_ENABLED": "true"
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 0
}

test_no_violation_no_auth_enabled_in_prod {
    input := {
        "review": {
            "object": {
                "kind": "ConfigMap",
                "metadata": {
                    "name": "service-config",
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
                    "name": "service-config",
                    "namespace": "prod"
                },
                "data": {}
            }
        }
    }

    violations := violation with input as input
    count(violations) == 0
}

# =============================================================================
# Test: Non-ConfigMap objects should not trigger violations
# =============================================================================

test_no_violation_secret_in_prod {
    input := {
        "review": {
            "object": {
                "kind": "Secret",
                "metadata": {
                    "name": "service-secret",
                    "namespace": "prod"
                },
                "data": {
                    "AUTH_ENABLED": "false"
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
                    "name": "my-service-config",
                    "namespace": "prod"
                },
                "data": {
                    "AUTH_ENABLED": "false"
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 1
    some v
    violations[v]
    contains(v.msg, "my-service-config")
    contains(v.msg, "AUTH_ENABLED=false")
    contains(v.msg, "Authentication must be enabled")
}

# =============================================================================
# Test: Pod/workload container env var with AUTH_ENABLED=false
# =============================================================================

test_violation_pod_container_env_auth_enabled_false_in_prod {
    input := {
        "review": {
            "object": {
                "kind": "Pod",
                "metadata": {
                    "name": "service-pod",
                    "namespace": "prod"
                },
                "spec": {
                    "containers": [{
                        "name": "service",
                        "image": "service:latest",
                        "env": [{
                            "name": "AUTH_ENABLED",
                            "value": "false"
                        }]
                    }]
                }
            }
        }
    }

    violations := violation with input as input
    count(violations) == 1
}

test_violation_deployment_container_env_auth_enabled_false_in_prod {
    input := {
        "review": {
            "object": {
                "kind": "Deployment",
                "metadata": {
                    "name": "service-deployment",
                    "namespace": "prod"
                },
                "spec": {
                    "template": {
                        "spec": {
                            "containers": [{
                                "name": "service",
                                "image": "service:latest",
                                "env": [{
                                    "name": "AUTH_ENABLED",
                                    "value": "false"
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

test_violation_statefulset_container_env_auth_enabled_false_in_prod {
    input := {
        "review": {
            "object": {
                "kind": "StatefulSet",
                "metadata": {
                    "name": "service-sts",
                    "namespace": "production"
                },
                "spec": {
                    "template": {
                        "spec": {
                            "containers": [{
                                "name": "service",
                                "image": "service:latest",
                                "env": [{
                                    "name": "AUTH_ENABLED",
                                    "value": "False"
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

test_violation_cronjob_container_env_auth_enabled_false_in_prod {
    input := {
        "review": {
            "object": {
                "kind": "CronJob",
                "metadata": {
                    "name": "service-cronjob",
                    "namespace": "prod"
                },
                "spec": {
                    "jobTemplate": {
                        "spec": {
                            "template": {
                                "spec": {
                                    "containers": [{
                                        "name": "service",
                                        "image": "service:latest",
                                        "env": [{
                                            "name": "AUTH_ENABLED",
                                            "value": "false"
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
# Test: initContainers with AUTH_ENABLED=false
# =============================================================================

test_violation_pod_initcontainer_env_auth_enabled_false_in_prod {
    input := {
        "review": {
            "object": {
                "kind": "Pod",
                "metadata": {
                    "name": "service-pod",
                    "namespace": "prod"
                },
                "spec": {
                    "containers": [{
                        "name": "service",
                        "image": "service:latest"
                    }],
                    "initContainers": [{
                        "name": "init-service",
                        "image": "service-init:latest",
                        "env": [{
                            "name": "AUTH_ENABLED",
                            "value": "false"
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
# Test: Workloads with AUTH_ENABLED=false in dev/staging should be allowed
# =============================================================================

test_no_violation_pod_env_auth_enabled_false_in_dev {
    input := {
        "review": {
            "object": {
                "kind": "Pod",
                "metadata": {
                    "name": "service-pod",
                    "namespace": "dev"
                },
                "spec": {
                    "containers": [{
                        "name": "service",
                        "image": "service:latest",
                        "env": [{
                            "name": "AUTH_ENABLED",
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

test_no_violation_deployment_env_auth_enabled_false_in_staging {
    input := {
        "review": {
            "object": {
                "kind": "Deployment",
                "metadata": {
                    "name": "service-deployment",
                    "namespace": "staging"
                },
                "spec": {
                    "template": {
                        "spec": {
                            "containers": [{
                                "name": "service",
                                "image": "service:latest",
                                "env": [{
                                    "name": "AUTH_ENABLED",
                                    "value": "false"
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
# Test: Workloads with AUTH_ENABLED=true in prod should be allowed
# =============================================================================

test_no_violation_pod_env_auth_enabled_true_in_prod {
    input := {
        "review": {
            "object": {
                "kind": "Pod",
                "metadata": {
                    "name": "service-pod",
                    "namespace": "prod"
                },
                "spec": {
                    "containers": [{
                        "name": "service",
                        "image": "service:latest",
                        "env": [{
                            "name": "AUTH_ENABLED",
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

test_no_violation_deployment_no_env_in_prod {
    input := {
        "review": {
            "object": {
                "kind": "Deployment",
                "metadata": {
                    "name": "service-deployment",
                    "namespace": "prod"
                },
                "spec": {
                    "template": {
                        "spec": {
                            "containers": [{
                                "name": "service",
                                "image": "service:latest"
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
# Test: Workload violation message contains relevant information
# =============================================================================

test_workload_violation_message_contains_kind_and_name {
    input := {
        "review": {
            "object": {
                "kind": "Deployment",
                "metadata": {
                    "name": "my-service-deployment",
                    "namespace": "prod"
                },
                "spec": {
                    "template": {
                        "spec": {
                            "containers": [{
                                "name": "service-container",
                                "image": "service:latest",
                                "env": [{
                                    "name": "AUTH_ENABLED",
                                    "value": "false"
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
    contains(v.msg, "my-service-deployment")
    contains(v.msg, "service-container")
    contains(v.msg, "prod")
    contains(v.msg, "AUTH_ENABLED=false")
}
