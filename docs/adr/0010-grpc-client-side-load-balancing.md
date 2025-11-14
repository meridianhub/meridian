# ADR 0010: gRPC Client-Side Load Balancing with Headless Services

## Status

Accepted

## Context

Meridian microservices communicate via gRPC and run as stateless pods in Kubernetes. Without proper load balancing configuration, gRPC's HTTP/2 connection reuse can cause uneven traffic distribution - all requests from one client flow through a single long-lived connection to one pod, leaving other pods idle even under high load.

### Problem

- **Default Kubernetes behavior**: ClusterIP services use iptables/IPVS for L4 load balancing, which balances connections (not requests)
- **gRPC HTTP/2 multiplexing**: Single connection carries many requests, defeating L4 load balancing
- **Consequence**: Horizontal pod scaling doesn't improve performance - new pods receive no traffic from existing client connections

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

2. **DNS resolver with round_robin policy**
   - gRPC clients use `dns:///` scheme for service discovery
   - `{"loadBalancingPolicy":"round_robin"}` distributes requests evenly
   - DNS-based discovery automatically picks up pod changes

3. **Centralized client factory** (`pkg/platform/grpc/client.go`)
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
- Uses blocking dial to fail fast on misconfiguration

### Validation Criteria

Load testing confirms:
- ✅ Requests distribute evenly across all pods (within 5% variance)
- ✅ Scaling from 2→5 replicas automatically adds connections
- ✅ No idle pods under sustained load
- ✅ Pod restarts trigger reconnection to healthy pods

## Consequences

### Positive

- **Simple**: Uses standard Kubernetes DNS, no additional infrastructure
- **Automatic**: Scales with pod count without manual intervention
- **Efficient**: Round-robin provides fair distribution for most workloads
- **Observable**: Standard gRPC metrics show per-connection load

### Negative

- **Client-side complexity**: Each client must configure DNS resolver and load balancing
  - *Mitigation*: Centralized `pkg/platform/grpc` package enforces consistency
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
