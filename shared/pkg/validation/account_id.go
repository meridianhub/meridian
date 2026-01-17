// Package validation provides input validation utilities for client-side
// pre-validation before making RPC calls.
package validation

import (
	"errors"
	"fmt"
	"regexp"
)

// accountIDPattern matches proto validation: ^[a-zA-Z0-9_-]+$
// Length: 1-100 characters
var accountIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,100}$`)

// ErrAccountIDRequired is returned when account ID is empty.
var ErrAccountIDRequired = errors.New("account_id is required")

// ErrAccountIDTooLong is returned when account ID exceeds maximum length.
var ErrAccountIDTooLong = errors.New("account_id exceeds maximum length of 100 characters")

// ErrAccountIDInvalidCharacters is returned when account ID contains invalid characters.
var ErrAccountIDInvalidCharacters = errors.New("account_id contains invalid characters: must match ^[a-zA-Z0-9_-]+$")

// ValidateAccountID checks if an account ID matches the required format.
// Returns nil if valid, error with descriptive message if invalid.
func ValidateAccountID(accountID string) error {
	if accountID == "" {
		return ErrAccountIDRequired
	}
	if len(accountID) > 100 {
		return fmt.Errorf("%w: got %d", ErrAccountIDTooLong, len(accountID))
	}
	if !accountIDPattern.MatchString(accountID) {
		return fmt.Errorf("%w (got: %q)", ErrAccountIDInvalidCharacters, accountID)
	}
	return nil
}
