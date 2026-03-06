// Package service provides the gRPC service implementation for Position Keeping operations.
//
// The PositionKeepingService implements the position_keeping.proto service definition, providing
// gRPC endpoints for managing financial position logs. It coordinates between the domain layer,
// persistence layer, and event publishing.
//
// # Architecture
//
// The service follows a layered architecture:
//   - Adapters (adapters.go): Convert between protobuf and domain models
//   - Service (service.go): Implements gRPC service interface and orchestrates operations
//   - Domain Integration: Uses domain.FinancialPositionLogRepository for persistence
//   - Event Publishing: Uses domain.EventPublisher for async event notifications
//   - Idempotency: Leverages shared/platform/idempotency for exactly-once semantics
//
// # Dependencies
//
// The service requires three dependencies injected via constructor:
//   - FinancialPositionLogRepository: Handles persistence operations
//   - EventPublisher: Publishes domain events for external consumers
//   - Idempotency.Service: Provides distributed locking and idempotency guarantees
//
// # gRPC Operations
//
// Implemented operations:
//   - InitiateFinancialPositionLog: Create a new financial position log with idempotency support
//   - InitiateFinancialPositionLogBatch: Create multiple logs atomically in a single batch
//   - RetrieveFinancialPositionLog: Fetch a log by ID
//   - ListFinancialPositionLogs: Query logs with filtering and pagination
//   - UpdateFinancialPositionLog: Update an existing log with optimistic locking
//
// Planned:
//   - BulkImportTransactions: Import multiple transactions atomically.
//     Proto defined (position_keeping.proto), client stub exists (client/client.go),
//     consumed by current-account service. Server implementation not yet built.
//
// Removed from scope:
//   - ControlFinancialPositionLog: Originally planned for log lifecycle transitions.
//     No proto definition, no consumer references, no implementation. Log lifecycle
//     is managed through UpdateFinancialPositionLog with optimistic locking instead.
//
// # Error Handling
//
// All methods return gRPC status codes following standard conventions:
//   - InvalidArgument: Validation failures or malformed requests
//   - NotFound: Requested resource does not exist
//   - Internal: Unexpected errors (repository, events, etc.)
//   - AlreadyExists: Conflict during creation (handled via optimistic locking)
//
// # Concurrency Control
//
// The service uses optimistic concurrency control via version numbers in the domain model.
// All update operations include version checking to prevent lost updates in concurrent scenarios.
package service
