// Package domain contains the core business logic for internal accounts.
package domain

import "errors"

// Domain errors for internal account operations.
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

	// ErrInvalidClearingPurpose indicates an unrecognized clearing purpose was provided.
	ErrInvalidClearingPurpose = errors.New("invalid clearing purpose")

	// ErrClearingPurposeNotAllowed indicates that a clearing purpose was specified
	// for an account type that is not CLEARING.
	ErrClearingPurposeNotAllowed = errors.New("clearing purpose only allowed for CLEARING account type")

	// ErrClearingPurposeRequired indicates that a CLEARING account was created
	// without specifying a clearing purpose (cannot be UNSPECIFIED).
	ErrClearingPurposeRequired = errors.New("clearing purpose required for CLEARING account type")

	// ErrOrgScopedClearingNotAllowed indicates that an org-scoped account
	// was created with CLEARING type, which is not permitted.
	// CLEARING accounts are global by design and cannot be scoped to an organization.
	ErrOrgScopedClearingNotAllowed = errors.New("org-scoped accounts cannot be CLEARING type")
)
