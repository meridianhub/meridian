// Package clients provides consumer-specific client interfaces for inter-service communication.
//
// MIGRATION NOTE: This package now re-exports interfaces that are implemented by the
// service-owned client packages. The actual client implementations have been moved to:
//   - github.com/meridianhub/meridian/services/position-keeping/client
//   - github.com/meridianhub/meridian/services/financial-accounting/client
//   - github.com/meridianhub/meridian/services/party/client
//
// These interfaces are kept here for:
//  1. Backward compatibility with existing code that imports from this package
//  2. Allowing mock implementations for testing without importing the full client packages
//  3. Consumer-specific interface definitions (subset of methods actually used)
//
// For new code, prefer importing directly from the service-owned client packages.
package clients

import (
	"context"

	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
)

// PositionKeepingClient defines the interface for communicating with the PositionKeeping service.
//
// This interface represents the subset of PositionKeeping operations used by CurrentAccount.
// The actual implementation is provided by services/position-keeping/client.Client which
// implements this interface directly.
//
// The PositionKeeping service maintains comprehensive financial position logs,
// capturing transaction entries, lineage, audit trails, and status tracking.
// CurrentAccount uses this service to record transaction history for compliance
// and position tracking purposes.
type PositionKeepingClient interface {
	// InitiateFinancialPositionLog creates a new financial position log for an account
	//
	// This should be called when establishing transaction tracking for a new account
	// or transaction set. The log serves as a container for all related transaction entries.
	InitiateFinancialPositionLog(ctx context.Context, req *positionkeepingv1.InitiateFinancialPositionLogRequest) (*positionkeepingv1.InitiateFinancialPositionLogResponse, error)

	// UpdateFinancialPositionLog adds entries or updates status for an existing position log
	//
	// Called after executing transactions (deposits, withdrawals) to capture
	// the transaction details in the position log. Supports adding transaction entries,
	// status updates, and audit trail entries.
	UpdateFinancialPositionLog(ctx context.Context, req *positionkeepingv1.UpdateFinancialPositionLogRequest) (*positionkeepingv1.UpdateFinancialPositionLogResponse, error)

	// RetrieveFinancialPositionLog retrieves a specific financial position log by ID
	//
	// Used to fetch position log details for reporting, reconciliation,
	// or verification purposes.
	RetrieveFinancialPositionLog(ctx context.Context, req *positionkeepingv1.RetrieveFinancialPositionLogRequest) (*positionkeepingv1.RetrieveFinancialPositionLogResponse, error)

	// BulkImportTransactions imports multiple transaction entries in a single operation
	//
	// Used during batch processing or reconciliation when multiple transactions
	// need to be recorded efficiently.
	BulkImportTransactions(ctx context.Context, req *positionkeepingv1.BulkImportTransactionsRequest) (*positionkeepingv1.BulkImportTransactionsResponse, error)

	// ListFinancialPositionLogs lists position logs with filtering and pagination
	//
	// Used for querying position logs by account, status, or date range.
	// Supports pagination for efficient retrieval of large result sets.
	ListFinancialPositionLogs(ctx context.Context, req *positionkeepingv1.ListFinancialPositionLogsRequest) (*positionkeepingv1.ListFinancialPositionLogsResponse, error)

	// Close terminates the client connection gracefully
	Close() error
}

// FinancialAccountingClient defines the interface for communicating with the FinancialAccounting service.
//
// This interface represents the subset of FinancialAccounting operations used by CurrentAccount.
// The actual implementation is provided by services/financial-accounting/client.Client which
// implements this interface directly.
//
// The FinancialAccounting service implements double-entry bookkeeping, managing
// financial booking logs and ledger postings. CurrentAccount uses this service
// after reconciliation to post validated transactions to the general ledger.
type FinancialAccountingClient interface {
	// InitiateFinancialBookingLog creates a new financial booking log
	//
	// Called when establishing a new accounting context for an account or product.
	// The booking log serves as a container for all ledger postings related to
	// the financial account type and product service.
	InitiateFinancialBookingLog(ctx context.Context, req *financialaccountingv1.InitiateFinancialBookingLogRequest) (*financialaccountingv1.InitiateFinancialBookingLogResponse, error)

	// UpdateFinancialBookingLog updates the status or rules of an existing booking log
	//
	// Used to update accounting rules or transition the booking log status
	// (e.g., from PENDING to POSTED after validation).
	UpdateFinancialBookingLog(ctx context.Context, req *financialaccountingv1.UpdateFinancialBookingLogRequest) (*financialaccountingv1.UpdateFinancialBookingLogResponse, error)

	// RetrieveFinancialBookingLog retrieves a specific financial booking log by ID
	//
	// Used to fetch booking log details including all associated postings
	// for verification, reporting, or reconciliation.
	RetrieveFinancialBookingLog(ctx context.Context, req *financialaccountingv1.RetrieveFinancialBookingLogRequest) (*financialaccountingv1.RetrieveFinancialBookingLogResponse, error)

	// ListFinancialBookingLogs lists booking logs with filtering and pagination
	//
	// Used for querying booking logs by business unit, status, or other criteria.
	ListFinancialBookingLogs(ctx context.Context, req *financialaccountingv1.ListFinancialBookingLogsRequest) (*financialaccountingv1.ListFinancialBookingLogsResponse, error)

	// CaptureLedgerPosting creates a new ledger posting
	//
	// Called to post individual debit or credit entries to the ledger.
	// Multiple postings form balanced journal entries following double-entry
	// bookkeeping principles. Balance validation (debits = credits) occurs
	// at the service layer.
	CaptureLedgerPosting(ctx context.Context, req *financialaccountingv1.CaptureLedgerPostingRequest) (*financialaccountingv1.CaptureLedgerPostingResponse, error)

	// RetrieveLedgerPosting retrieves a specific ledger posting by ID
	//
	// Used to fetch posting details for verification or audit purposes.
	RetrieveLedgerPosting(ctx context.Context, req *financialaccountingv1.RetrieveLedgerPostingRequest) (*financialaccountingv1.RetrieveLedgerPostingResponse, error)

	// Close terminates the client connection gracefully
	Close() error
}

// PartyClient defines the interface for communicating with the Party service.
//
// This interface represents the subset of Party operations used by CurrentAccount.
// The actual implementation is provided by services/party/client.Client, but
// CurrentAccount requires additional methods (ValidateParty, GetParty) beyond
// the raw gRPC operations.
//
// The Party service manages party reference data (customers, counterparties,
// legal entities). CurrentAccount uses this service to validate party ownership
// before account operations.
type PartyClient interface {
	// ValidateParty checks if a party exists and is active.
	//
	// Returns nil if the party exists and has ACTIVE status.
	// Returns ErrPartyNotFound if the party does not exist.
	// Returns ErrPartyNotActive if the party exists but is not ACTIVE.
	ValidateParty(ctx context.Context, partyID string) error

	// GetParty retrieves full party details by ID.
	//
	// Returns the party data if found, or an error if not found.
	GetParty(ctx context.Context, partyID string) (*partyv1.Party, error)

	// Close terminates the client connection gracefully.
	Close() error
}
