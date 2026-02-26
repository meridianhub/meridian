# ADR 0010: gRPC Client-Side Load Balancing with Headless Services

<!-- markdownlint-disable MD003 -->
---
name: adr-010-grpc-load-balancing
description: Client-side load balancing for gRPC using Kubernetes headless services
triggers:

- Configuring gRPC client connections
- Implementing inter-service communication
- Load balancing gRPC requests
- Scaling microservices

instructions: |
  Use pkg/platform/grpc.NewClient() for all inter-service gRPC connections.
  Configure Kubernetes services as headless (clusterIP: None).
  DNS returns all pod IPs for round_robin load balancing
---
<!-- markdownlint-enable MD003 -->

## Status

Accepted

## Context

Meridian microservices communicate via gRPC and run as stateless pods in Kubernetes. Without proper load balancing
configuration, gRPC's HTTP/2 connection reuse can cause uneven traffic distribution - all requests from one client flow
through a single long-lived connection to one pod, leaving other pods idle even under high load.

### Problem

- **Default Kubernetes behaviour**: ClusterIP services use iptables/IPVS for L4 load balancing, which balances
connections (not requests)
- **gRPC HTTP/2 multiplexing**: Single connection carries many requests, defeating L4 load balancing
- **Consequence**: Horizontal pod scaling doesn't improve performance - new pods receive no traffic from existing
client connections

### Requirements

1. Even distribution of gRPC requests across all backend pods
2. Automatic rebalancing when pods scale up or down
3. No additional infrastructure complexity
4. Works with standard Kubernetes primitives

## Decision

Implement **client-side load balancing** using:

1. **Headless Kubernetes services** (`clusterIP: None`)
   - DNS returns all pod IPs instead of single cluster IP
   - Clients can connect directly to individual pods

1. **DNS resolver with round_robin policy**
   - gRPC clients use `dns:///` scheme for service discovery
   - `{"loadBalancingPolicy":"round_robin"}` distributes requests evenly
   - DNS-based discovery automatically picks up pod changes

1. **Centralised client factory** (`pkg/platform/grpc/client.go`)
   - Enforces consistent configuration across services
   - Encapsulates DNS target construction
   - Provides sensible defaults (keepalive, timeout, blocking dial)

## Implementation

### Kubernetes Service Configuration

All gRPC services configured as headless:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: financial-accounting
spec:
  type: ClusterIP
  clusterIP: None  # Headless for client-side load balancing
  ports:

  - name: grpc

    port: 50052
    targetPort: grpc
  selector:
    app: financial-accounting
```

### RBAC Requirements

Client-side load balancing via DNS requires minimal RBAC permissions:

**ServiceAccount**: Each service runs with the `meridian` ServiceAccount (configured in
`deployments/k8s/base/serviceaccount.yaml`):

- `automountServiceAccountToken: true` - Required for in-cluster DNS resolution

**DNS Resolution**: Built into Kubernetes - no additional RBAC permissions needed:

- DNS queries for headless services use cluster DNS (CoreDNS/kube-dns)
- Pod can resolve `service-name.namespace.svc.cluster.local` without explicit permissions
- DNS returns all pod IPs for headless services automatically

**Current RBAC Policy** (`deployments/k8s/base/role.yaml`):

```yaml
apiVersion: rbac.authorisation.k8s.io/v1
kind: Role
metadata:
  name: meridian
rules:

# DNS resolution requires no explicit permissions

# CoreDNS handles service discovery transparently

# Application permissions (minimal)

- apiGroups: [""]

  resources: ["configmaps"]
  verbs: ["get", "watch"]
  resourceNames: ["meridian-config", "meridian-build-info"]

- apiGroups: [""]

  resources: ["secrets"]
  verbs: ["get"]
  resourceNames: ["meridian-secrets"]
```

**Key Points**:

- DNS-based load balancing works without additional RBAC permissions
- No need for Endpoints or Service resource access
- Cluster DNS handles headless service resolution automatically
- ServiceAccount token primarily used for ConfigMap/Secret access

### Client Connection Pattern

```go
import platformgrpc "github.com/meridianhub/meridian/pkg/platform/grpc"

conn, err := platformgrpc.NewClient(ctx, platformgrpc.ClientConfig{
    ServiceName: "financial-accounting",
    Namespace:   "default",
    Port:        50052,
})
if err != nil {
    return err
}
defer conn.Close()

client := accountingv1.NewFinancialAccountingServiceClient(conn)
```

The client factory automatically:

- Constructs DNS target: `dns:///financial-accounting.default.svc.cluster.local:50052`
- Applies round_robin load balancing policy
- Configures keepalive for connection health
- Non-blocking connection (grpc.NewClient returns immediately)

### Integration Guidance

**For New Services**: Use `pkg/platform/grpc.NewClient()` from the start.

**Migrating Existing Clients**: Follow this pattern in service initialisation:

```go
// Before: Direct grpc.Dial
// conn, err := grpc.Dial("financial-accounting:50052", grpc.WithInsecure())

// After: Use platform client factory
import platformgrpc "github.com/meridianhub/meridian/pkg/platform/grpc"

conn, err := platformgrpc.NewClient(ctx, platformgrpc.ClientConfig{
    ServiceName: "financial-accounting",
    Port:        50052,
})
```

**Service Constants**: Define service connection configs as constants:

```go
const (
    FinancialAccountingPort = 50052
    PositionKeepingPort    = 50053
    CurrentAccountPort     = 50051
)
```

**Dependency Injection**: Create clients in main.go, inject into services:

```go
// cmd/current-account/main.go
func main() {
    ctx := context.Background()

    // Create inter-service gRPC clients
    posKeepingConn, err := platformgrpc.NewClient(ctx, platformgrpc.ClientConfig{
        ServiceName: "position-keeping",
        Port:        PositionKeepingPort,
    })
    if err != nil {
        log.Fatalf("Failed to connect to position-keeping: %v", err)
    }
    defer posKeepingConn.Close()

    posClient := positionkeepingv1.NewPositionKeepingServiceClient(posKeepingConn)

    // Inject into service
    svc := service.NewCurrentAccountService(repo, posClient, eventPub)
    // ...
}
```

### Validation Criteria

Load testing confirms:

- ✅ Requests distribute evenly across all pods (within 5% variance)
- ✅ Scaling from 2→5 replicas automatically adds connections
- ✅ No idle pods under sustained load
- ✅ Pod restarts trigger reconnection to healthy pods

## Security Considerations

### Transport Security

**Current Implementation**: Uses `insecure.NewCredentials()` for internal cluster communication.

**Rationale**:

- Internal services communicate within trusted Kubernetes cluster network
- Network policies restrict pod-to-pod communication
- TLS termination handled at ingress layer for external traffic

**Production Recommendations**:

1. **Service Mesh mTLS**: When adopting service mesh (Istio/Linkerd), enable mutual TLS
   - Automatic certificate rotation
   - Zero-trust security model
   - Encrypted inter-service communication

1. **Manual TLS Configuration**: If not using service mesh:

   ```go
   import "google.golang.org/grpc/credentials"

   creds, err := credentials.NewClientTLSFromFile("ca.pem", "")
   conn, err := grpc.NewClient(cfg, grpc.WithTransportCredentials(creds))

```

1. **Certificate Management**:
   - Use cert-manager for automatic certificate provisioning
   - Rotate certificates before expiration
   - Monitor certificate validity in observability stack

### Authentication & Authorisation

- gRPC interceptors handle authentication token validation
- Authorisation policies enforced at application layer
- Consider gRPC metadata for request context propagation

## Consequences

### Positive

- **Simple**: Uses standard Kubernetes DNS, no additional infrastructure
- **Automatic**: Scales with pod count without manual intervention
- **Efficient**: Round-robin provides fair distribution for most workloads
- **Observable**: Standard gRPC metrics show per-connection load

### Negative

- **Client-side complexity**: Each client must configure DNS resolver and load balancing
  - *Mitigation*: Centralised `pkg/platform/grpc` package enforces consistency
- **DNS TTL delays**: Pod changes may take 10-30s to propagate
  - *Acceptable*: Brief delay is preferable to complex infrastructure
- **No weighted routing**: Round-robin assumes uniform pod capacity
  - *Acceptable*: Pods are homogeneous in reference implementation

### Neutral

- **No connection pooling**: Each client creates dedicated connections
  - *Trade-off*: Simpler than connection pool management, acceptable for microservice scale

## Alternatives Considered

### Service Mesh (Istio, Linkerd)

**Rejected** for reference implementation due to:

- Operational complexity (sidecars, control plane, upgrades)
- Performance overhead (additional proxy hop)
- Overkill for internal gRPC-only communication

**When to reconsider**:

- Need for advanced routing (weighted, canary, circuit breaking)
- mTLS required for zero-trust security model
- Multi-cluster or multi-region deployment

### Envoy Proxy Sidecar

**Rejected** as middle ground between no mesh and full mesh:

- Still requires sidecar injection and configuration
- Misses advanced Istio features while retaining operational burden
- Client-side balancing simpler for current requirements

### Server-Side Load Balancer (gRPC-LB, Envoy xDS)

**Rejected** due to:

- Requires separate load balancer infrastructure
- Additional network hop and failure point
- Complexity not justified by current scale

## References

- [gRPC Load Balancing Guide](https://grpc.io/blog/grpc-load-balancing/)
- [Kubernetes Headless Services](https://kubernetes.io/docs/concepts/services-networking/service/#headless-services)
- [gRPC Name Resolution](https://github.com/grpc/grpc/blob/master/doc/naming.md)

## Related ADRs

- ADR-0002: Microservices Per BIAN Domain (establishes inter-service communication needs)
- ADR-0006: Tilt for Local Development (headless services work in local Kubernetes)
