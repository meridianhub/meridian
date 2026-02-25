// Package service implements gRPC services for the financial accounting domain.
package service

import (
	"context"

	internalaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
)

// InternalAccountClient defines the interface for communicating with the Internal Account service.
//
// This interface represents the subset of InternalAccount operations used by FinancialAccounting
// for resolving clearing account IDs dynamically. The actual implementation is provided by
// services/internal-account/client.Client which implements this interface directly.
//
// The InternalAccount service manages non-customer-facing accounts including clearing,
// nostro, vostro, holding, suspense, revenue, expense, and inventory accounts. FinancialAccounting
// uses this service to resolve clearing accounts for deposit, withdrawal, and settlement operations.
type InternalAccountClient interface {
	// ListInternalAccounts queries accounts with filtering and pagination.
	//
	// Used by AccountResolver to find active clearing accounts for a specific instrument.
	// Supports filtering by account type, instrument code, and status.
	ListInternalAccounts(ctx context.Context, req *internalaccountv1.ListInternalAccountsRequest) (*internalaccountv1.ListInternalAccountsResponse, error)

	// RetrieveInternalAccount fetches a single account by ID.
	//
	// Used to verify account existence and status.
	RetrieveInternalAccount(ctx context.Context, req *internalaccountv1.RetrieveInternalAccountRequest) (*internalaccountv1.RetrieveInternalAccountResponse, error)

	// Close terminates the client connection gracefully.
	Close() error
}
