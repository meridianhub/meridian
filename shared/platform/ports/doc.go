// Package ports provides centralized port constant definitions for all Meridian services.
//
// All services, Kubernetes manifests, Tilt configurations, and integration tests should
// reference these constants instead of hardcoding port numbers.
//
// # Port Allocation
//
//   - 50051–50099: internal gRPC service-to-service ports
//   - 8080: HTTP/Connect gateway (client-facing)
//   - 8081: HTTP health check (Kubernetes probes)
//   - 9090: Prometheus metrics
package ports
