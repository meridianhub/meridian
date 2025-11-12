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
//   - Idempotency: Leverages pkg/platform/idempotency for exactly-once semantics
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
// The service implements all operations defined in position_keeping.proto:
//   - InitiateFinancialPositionLog: Create a new financial position log
//   - UpdateFinancialPositionLog: Update an existing log
//   - RetrieveFinancialPositionLog: Fetch a log by ID
//   - BulkImportTransactions: Import multiple transactions atomically
//   - ListFinancialPositionLogs: Query logs with filtering and pagination
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
