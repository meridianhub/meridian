// Package service implements gRPC services for the internal account domain.
package service

import (
	"context"

	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
)

// PositionKeepingClient defines the interface for communicating with the PositionKeeping service.
//
// # Architecture: Position Keeping as Source of Truth for Balance
//
// The PositionKeeping service is the SINGLE SOURCE OF TRUTH for all account balance data.
// InternalAccount is a registry service that manages account metadata (status, ownership,
// configuration) but deliberately does NOT store or compute balances locally.
//
// This architectural separation provides:
//   - Single source of truth: All balance queries go through Position Keeping
//   - Consistency: No risk of balance drift between services
//   - Clear responsibilities: InternalAccount = metadata, PositionKeeping = positions
//
// # Performance: O(1) Balance Queries
//
// Balance queries via GetAccountBalances are O(1) operations because Position Keeping
// pre-computes and maintains running balance totals. Unlike systems that compute balance
// by summing transactions (O(n) where n = transaction count), Position Keeping updates
// running totals on each transaction, enabling constant-time balance retrieval regardless
// of account history length.
//
// # Integration Pattern
//
// InternalAccount calls Position Keeping to:
//   - Query current/available/ledger balances for account status display
//   - Validate balance thresholds for account status transitions
//   - Provide balance data in RetrieveInternalAccount responses
//
// InternalAccount does NOT call Position Keeping to:
//   - Record transactions (that's CurrentAccount's responsibility)
//   - Modify balances (PositionKeeping handles this via transaction processing)
//   - Initialize position logs (done by CurrentAccount when linking accounts)
//
// This interface represents the subset of PositionKeeping operations used by InternalAccount.
// The actual implementation is provided by services/position-keeping/client.Client.
type PositionKeepingClient interface {
	// GetAccountBalances retrieves all balance types for an account by instrument.
	//
	// This is an O(1) operation - Position Keeping maintains pre-computed running balances,
	// so retrieval time is constant regardless of transaction history length.
	//
	// Returns all balance types (opening, closing, current, available, ledger, reserve, free)
	// for a specific instrument in a single call. Useful for comprehensive account status display.
	//
	// Supports both currency instruments (GBP, USD, EUR) and non-currency instruments
	// (KWH, GPU_HOUR, CARBON_TONNE) for multi-asset account types.
	//
	// Returns:
	//   - GetAccountBalancesResponse with all balance types and as_of timestamp on success
	//   - NotFound error if no position exists for the account/instrument combination
	//   - InvalidArgument error if account_id format is invalid
	GetAccountBalances(ctx context.Context, req *positionkeepingv1.GetAccountBalancesRequest) (*positionkeepingv1.GetAccountBalancesResponse, error)

	// GetAccountBalance retrieves a specific balance type for an account by instrument.
	//
	// Used by the valuation engine to query the current balance for an account's native instrument.
	// Returns the balance amount as InstrumentAmount with the requested balance type.
	//
	// Returns:
	//   - GetAccountBalanceResponse with the balance amount on success
	//   - NotFound error if no position exists for the account/instrument combination
	//   - InvalidArgument error if account_id format is invalid
	GetAccountBalance(ctx context.Context, req *positionkeepingv1.GetAccountBalanceRequest) (*positionkeepingv1.GetAccountBalanceResponse, error)

	// Close terminates the client connection gracefully.
	// Should be called during service shutdown to release gRPC resources.
	Close() error
}

// ReferenceDataClient defines the interface for communicating with the ReferenceData service.
//
// This interface represents the subset of ReferenceData operations used by InternalAccount.
// The actual implementation is provided by services/reference-data/client.Client.
//
// The ReferenceData service manages instruments (currencies, commodities, etc.).
// InternalAccount uses this service to validate that instrument_code exists before creating accounts.
type ReferenceDataClient interface {
	// RetrieveInstrument retrieves an instrument by its code.
	//
	// Returns the instrument if found, or an error if not found.
	// Used to validate instrument_code during account initiation.
	RetrieveInstrument(ctx context.Context, req *referencedatav1.RetrieveInstrumentRequest) (*referencedatav1.RetrieveInstrumentResponse, error)

	// Close terminates the client connection gracefully.
	Close() error
}
