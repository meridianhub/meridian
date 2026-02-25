// Package adapters provides infrastructure adapters for the Internal Account service.
//
// This package follows the hexagonal architecture (ports and adapters) pattern,
// providing concrete implementations of domain interfaces for external systems.
//
// # Sub-packages
//
// The adapters package is organized into sub-packages by integration type:
//
//   - [persistence]: PostgreSQL-based repository implementation for account storage
//   - [grpc]: gRPC client adapters for external service communication
//
// # Architecture
//
// Adapters implement interfaces defined in the domain layer, allowing the business
// logic to remain independent of infrastructure concerns:
//
//	+---------------+     +----------------+     +------------------+
//	| gRPC Service  | --> | Domain Layer   | --> | Adapters Layer   |
//	| (service pkg) |     | (domain pkg)   |     | (adapters pkg)   |
//	+---------------+     +----------------+     +------------------+
//	                              |                       |
//	                              v                       v
//	                      domain.Repository      persistence.Repository
//	                                             grpc.PositionKeepingClient
//
// # Multi-tenancy
//
// All persistence adapters automatically scope queries to the tenant extracted
// from the gRPC context. This ensures data isolation between organizations.
//
// # Observability
//
// Adapters include instrumentation for tracing and metrics, propagating trace
// context through external service calls and database operations.
package adapters
