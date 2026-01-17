// Package service implements gRPC services for the financial accounting domain.
package service

import (
	"context"

	internalbankaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/internal_bank_account/v1"
)

// InternalBankAccountClient defines the interface for communicating with the Internal Bank Account service.
//
// This interface represents the subset of InternalBankAccount operations used by FinancialAccounting
// for resolving clearing account IDs dynamically. The actual implementation is provided by
// services/internal-bank-account/client.Client which implements this interface directly.
//
// The InternalBankAccount service manages non-customer-facing accounts including clearing,
// nostro, vostro, holding, suspense, revenue, expense, and inventory accounts. FinancialAccounting
// uses this service to resolve clearing accounts for deposit, withdrawal, and settlement operations.
type InternalBankAccountClient interface {
	// ListInternalBankAccounts queries accounts with filtering and pagination.
	//
	// Used by AccountResolver to find active clearing accounts for a specific instrument.
	// Supports filtering by account type, instrument code, and status.
	ListInternalBankAccounts(ctx context.Context, req *internalbankaccountv1.ListInternalBankAccountsRequest) (*internalbankaccountv1.ListInternalBankAccountsResponse, error)

	// RetrieveInternalBankAccount fetches a single account by ID.
	//
	// Used to verify account existence and status.
	RetrieveInternalBankAccount(ctx context.Context, req *internalbankaccountv1.RetrieveInternalBankAccountRequest) (*internalbankaccountv1.RetrieveInternalBankAccountResponse, error)

	// Close terminates the client connection gracefully.
	Close() error
}
