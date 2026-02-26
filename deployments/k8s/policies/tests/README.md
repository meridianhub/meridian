# OPA Gatekeeper Policy Tests

This directory contains tests for the `BlockDevModeInProduction` OPA Gatekeeper policy
that prevents `LOCAL_DEV_MODE=true` from being deployed in production namespaces.

## Quick Start

### Run OPA Unit Tests

```bash
# From repository root
./deployments/k8s/policies/tests/run_tests.sh

# Or with verbose output
./deployments/k8s/policies/tests/run_tests.sh -v

# Or directly with opa (--v0-compatible required for OPA 1.x)
opa test deployments/k8s/policies/tests/ --v0-compatible -v
```

### Prerequisites

- **opa**: `brew install opa` or download from <https://www.openpolicyagent.org/docs/latest/>

## Test Coverage

The test suite verifies:

| Scenario | Expected Result |
|----------|-----------------|
| ConfigMap with `LOCAL_DEV_MODE=true` in `prod` namespace | BLOCKED |
| ConfigMap with `LOCAL_DEV_MODE=true` in `production` namespace | BLOCKED |
| ConfigMap with `LOCAL_DEV_MODE=true` in `prod-eu` namespace | BLOCKED |
| ConfigMap with `LOCAL_DEV_MODE=true` in `prod-us-west` namespace | BLOCKED |
| ConfigMap with `LOCAL_DEV_MODE=true` in `my-production-service` namespace | BLOCKED |
| ConfigMap with `LOCAL_DEV_MODE=true` in `dev` namespace | ALLOWED |
| ConfigMap with `LOCAL_DEV_MODE=true` in `staging` namespace | ALLOWED |
| ConfigMap with `LOCAL_DEV_MODE=true` in `default` namespace | ALLOWED |
| ConfigMap with `LOCAL_DEV_MODE=false` in `prod` namespace | ALLOWED |
| ConfigMap without `LOCAL_DEV_MODE` in `prod` namespace | ALLOWED |
| Non-ConfigMap objects (Deployment, Secret) in `prod` | ALLOWED |

## Manual Kubernetes Cluster Testing

For full integration testing with an actual Gatekeeper deployment:

### 1. Prerequisites

- Kubernetes cluster (kind, minikube, or cloud)
- OPA Gatekeeper installed: <https://open-policy-agent.github.io/gatekeeper/website/docs/install/>

### 2. Install Gatekeeper (if not already installed)

```bash
kubectl apply -f https://raw.githubusercontent.com/open-policy-agent/gatekeeper/v3.15.0/deploy/gatekeeper.yaml

# Wait for Gatekeeper to be ready
kubectl -n gatekeeper-system wait --for=condition=Ready pod -l control-plane=controller-manager --timeout=60s
```

### 3. Apply the Policy

```bash
# Apply the ConstraintTemplate and Constraint
kubectl apply -f deployments/k8s/policies/gateway-dev-mode-block.yaml

# Wait for the constraint to be ready
kubectl get constraint gateway-dev-mode-constraint -o jsonpath='{.status.byPod}'
```

### 4. Test: Verify Blocking in Production Namespace

```bash
# Create production namespace with appropriate label
kubectl create namespace prod-test --dry-run=client -o yaml | \
  kubectl label --local -f - environment=production -o yaml | \
  kubectl apply -f -

# Attempt to create ConfigMap with LOCAL_DEV_MODE=true (should be denied)
cat <<EOF | kubectl apply -f - 2>&1 || echo "Expected denial"
apiVersion: v1
kind: ConfigMap
metadata:
  name: gateway-config-test
  namespace: prod-test
data:
  LOCAL_DEV_MODE: "true"
EOF
# Expected: denied by gateway-dev-mode-constraint

# Cleanup
kubectl delete namespace prod-test --ignore-not-found
```

### 5. Test: Verify Allowing in Dev Namespace

```bash
# Create dev namespace
kubectl create namespace dev-test --dry-run=client -o yaml | \
  kubectl label --local -f - environment=development -o yaml | \
  kubectl apply -f -

# Create ConfigMap with LOCAL_DEV_MODE=true (should succeed)
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: ConfigMap
metadata:
  name: gateway-config-test
  namespace: dev-test
data:
  LOCAL_DEV_MODE: "true"
EOF
# Expected: configmap/gateway-config-test created

# Cleanup
kubectl delete namespace dev-test --ignore-not-found
```

### 6. Verify Violation Messages

```bash
# Check constraint status for violations
kubectl get constraint gateway-dev-mode-constraint -o yaml

# Check audit logs
kubectl logs -n gatekeeper-system -l control-plane=audit-controller --tail=50
```

## Files

| File | Description |
|------|-------------|
| `blockdevmodeinproduction.rego` | Rego policy extracted from ConstraintTemplate for testing |
| `policy_test.rego` | OPA unit tests for the policy |
| `run_tests.sh` | Script to run all tests |
| `README.md` | This file |

## Keeping Tests in Sync

The `blockdevmodeinproduction.rego` file is extracted from the ConstraintTemplate in
`gateway-dev-mode-block.yaml`. When updating the policy:

1. Update `gateway-dev-mode-block.yaml` (the canonical source)
2. Copy the Rego code from the `spec.targets[].rego` field to `blockdevmodeinproduction.rego`
3. Run the tests to ensure the policy works as expected

## CI Integration

Add to your CI pipeline:

```yaml
- name: Install OPA
  uses: open-policy-agent/setup-opa@v2
  with:
    version: '1.12.1'

- name: Test OPA Gatekeeper Policies
  run: |
    # --v0-compatible is required because Gatekeeper uses Rego v0 syntax
    opa test deployments/k8s/policies/tests/ --v0-compatible -v
```

## Known Limitations

1. **`envFrom` references are not checked at the Pod/Deployment level**: When a container uses
   `envFrom` to reference a ConfigMap, the policy cannot detect `LOCAL_DEV_MODE=true` at the
   workload level. However, the ConfigMap itself is validated when created/updated, providing
   defence-in-depth.

2. **"product" namespace prefix matches are intentional**: Namespaces like `product-catalogue` will
   be flagged as production due to the `prod*` prefix match. This is by design - false positives
   are safer than false negatives for security policies. If needed, adjust the namespace matching
   logic in the Rego policy.
