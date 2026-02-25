// Package grpc provides gRPC client adapters for external service communication.
//
// This package implements the service layer interfaces for communicating with
// other Meridian microservices, including Position Keeping and Reference Data.
//
// # Position Keeping Integration
//
// The Internal Account service delegates balance queries to Position Keeping,
// which maintains the authoritative transaction log and computes running balances.
// [PositionKeepingGRPCClient] implements [service.PositionKeepingClient] for this
// integration.
//
// # Service Discovery
//
// All gRPC clients use Kubernetes DNS-based service discovery with round-robin
// load balancing. The client configuration specifies service name, namespace,
// and port, which are resolved via the platform gRPC factory.
//
// Example:
//
//	client, err := grpc.NewPositionKeepingClient(&grpc.ClientConfig{
//	    ServiceName: "position-keeping",
//	    Namespace:   "default",
//	    Port:        50053,
//	    Timeout:     5 * time.Second,
//	    Logger:      logger,
//	})
//
// # Resilience Patterns
//
// Clients implement retry logic with exponential backoff for transient failures:
//
//   - UNAVAILABLE: Service temporarily unreachable (retried)
//   - INTERNAL: Server error (retried)
//   - DEADLINE_EXCEEDED: Timeout (retried)
//   - RESOURCE_EXHAUSTED: Rate limiting (retried with backoff)
//   - INVALID_ARGUMENT: Bad request (not retried - permanent error)
//   - NOT_FOUND: Resource missing (not retried - permanent error)
//
// Default retry configuration: 3 attempts, 100ms-1s exponential backoff.
//
// # Observability
//
// All RPC calls are instrumented with:
//
//   - Latency histograms per operation
//   - Success/failure counters
//   - Structured logging with request context
//   - Trace context propagation via gRPC metadata
//
// # Key Types
//
//   - [PositionKeepingGRPCClient]: Client for Position Keeping service
//   - [ClientConfig]: Configuration for Position Keeping client
package grpc
