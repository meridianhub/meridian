// Package service implements gRPC services for the internal bank account domain.
package service

import (
	"context"

	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
)

// PositionKeepingClient defines the interface for communicating with the PositionKeeping service.
//
// This interface represents the subset of PositionKeeping operations used by InternalBankAccount.
// The actual implementation is provided by services/position-keeping/client.Client.
//
// The PositionKeeping service maintains balances for all accounts in the system.
// InternalBankAccount uses this service to query balances - it does NOT store balance locally.
type PositionKeepingClient interface {
	// GetAccountBalances retrieves all balance types for an account by instrument.
	//
	// Returns all balance types (opening, closing, current, available, ledger, reserve, free)
	// for a specific instrument in a single call.
	GetAccountBalances(ctx context.Context, req *positionkeepingv1.GetAccountBalancesRequest) (*positionkeepingv1.GetAccountBalancesResponse, error)

	// Close terminates the client connection gracefully.
	Close() error
}

// ReferenceDataClient defines the interface for communicating with the ReferenceData service.
//
// This interface represents the subset of ReferenceData operations used by InternalBankAccount.
// The actual implementation is provided by services/reference-data/client.Client.
//
// The ReferenceData service manages instruments (currencies, commodities, etc.).
// InternalBankAccount uses this service to validate that instrument_code exists before creating accounts.
type ReferenceDataClient interface {
	// RetrieveInstrument retrieves an instrument by its code.
	//
	// Returns the instrument if found, or an error if not found.
	// Used to validate instrument_code during account initiation.
	RetrieveInstrument(ctx context.Context, req *referencedatav1.RetrieveInstrumentRequest) (*referencedatav1.RetrieveInstrumentResponse, error)

	// Close terminates the client connection gracefully.
	Close() error
}
