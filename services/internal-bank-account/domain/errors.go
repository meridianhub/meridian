// Package domain contains the core business logic for internal bank accounts.
package domain

import "errors"

// Domain errors for internal bank account operations.
// These follow the sentinel error pattern for consistent error handling.
var (
	// ErrAccountNotFound indicates the requested account does not exist.
	ErrAccountNotFound = errors.New("account not found")

	// ErrAccountClosed indicates an operation was attempted on a closed account.
	ErrAccountClosed = errors.New("account is closed")

	// ErrAccountSuspended indicates an operation was attempted on a suspended account.
	ErrAccountSuspended = errors.New("account is suspended")

	// ErrInvalidAccountType indicates an unrecognized account type was provided.
	ErrInvalidAccountType = errors.New("invalid account type")

	// ErrCorrespondentRequired indicates that correspondent details are required
	// for NOSTRO/VOSTRO accounts but were not provided.
	ErrCorrespondentRequired = errors.New("correspondent details required for NOSTRO/VOSTRO accounts")

	// ErrCorrespondentNotAllowed indicates that correspondent details were provided
	// for an account type that does not support correspondent relationships.
	ErrCorrespondentNotAllowed = errors.New("correspondent details not allowed for this account type")

	// ErrDuplicateAccountCode indicates an account with the given code already exists.
	ErrDuplicateAccountCode = errors.New("account code already exists")

	// ErrInvalidInstrumentCode indicates the provided instrument code is not valid.
	ErrInvalidInstrumentCode = errors.New("invalid instrument code")

	// ErrVersionMismatch indicates an optimistic locking conflict occurred.
	// The account was modified by another process since it was read.
	ErrVersionMismatch = errors.New("version mismatch: account was modified")

	// Validation errors for required fields.

	// ErrAccountIDRequired indicates the account ID was not provided.
	ErrAccountIDRequired = errors.New("account ID is required")

	// ErrAccountCodeRequired indicates the account code was not provided.
	ErrAccountCodeRequired = errors.New("account code is required")

	// ErrNameRequired indicates the account name was not provided.
	ErrNameRequired = errors.New("name is required")
)
