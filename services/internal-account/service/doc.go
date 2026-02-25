// Package service implements the gRPC server for the Internal Account service.
//
// This package provides the BIAN-compliant InternalAccountService, handling all
// account lifecycle operations through gRPC endpoints. The service acts as the
// application layer, orchestrating domain logic, persistence, and external service
// integrations.
//
// # BIAN Operations
//
// The service implements standard BIAN Control Record operations:
//
//   - Initiate (InCR): Create new internal accounts
//   - Update (UpCR): Modify account settings (name, description, counterparty)
//   - Control (CoCR): Lifecycle transitions (suspend, activate, close)
//   - Retrieve (ReCR): Fetch individual accounts by ID
//   - List: Query accounts with filtering and pagination
//   - GetBalance: Retrieve current balance from Position Keeping service
//
// # External Service Dependencies
//
// The service integrates with other Meridian services:
//
//   - Position Keeping: Source of truth for account balances. GetBalance queries
//     delegate to Position Keeping to avoid storing balance locally.
//
//   - Reference Data: Validates instrument codes during account creation.
//     Ensures accounts reference valid, active instruments (USD, KWH, etc.).
//
// # Multi-tenancy
//
// All operations are automatically scoped to the tenant extracted from the gRPC
// context. Tenant isolation is enforced at the persistence layer.
//
// # Observability
//
// The service emits metrics and traces for all operations:
//
//   - Operation duration histograms (success/failure)
//   - Error counts by type (not_found, validation, etc.)
//   - Distributed traces via OpenTelemetry
//
// # Key Types
//
//   - [Service]: The gRPC service implementation
//   - [PositionKeepingClient]: Interface for Position Keeping integration
//   - [ReferenceDataClient]: Interface for Reference Data integration
//
// # Example Server Setup
//
// Creating and registering the service:
//
//	repo := persistence.NewRepository(db)
//	svc, err := service.NewServiceWithClients(
//	    repo,
//	    positionKeepingClient,
//	    referenceDataClient,
//	    logger,
//	    tracer,
//	)
//	if err != nil {
//	    return err
//	}
//
//	grpcServer := grpc.NewServer()
//	pb.RegisterInternalAccountServiceServer(grpcServer, svc)
package service
