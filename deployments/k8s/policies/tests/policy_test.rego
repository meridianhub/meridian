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
