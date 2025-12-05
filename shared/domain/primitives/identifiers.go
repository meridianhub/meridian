// Package primitives provides strongly-typed domain identifier newtypes.
//
// These ID types prevent accidental mixing of different identifier types
// (e.g., passing an AccountID where a CustomerID is expected) by using
// distinct Go types with compile-time safety.
//
// Each ID type:
//   - Validates UUID v4 format on construction
//   - Returns Result[T] for explicit error handling
//   - Supports JSON marshal/unmarshal with validation
//   - Implements fmt.Stringer for easy logging
package primitives

import (
	"errors"
	"regexp"
	"strings"

	"github.com/meridianhub/meridian/shared/pkg/types"
)

// Sentinel errors for identifier validation.
var (
	ErrInvalidIDFormat = errors.New("invalid ID format: expected UUID v4")
	ErrEmptyID         = errors.New("ID cannot be empty")
)

// uuidV4Pattern matches UUID v4 format (case-insensitive).
// UUID v4 has version nibble '4' in position 13 and variant nibble [89ab] in position 17.
var uuidV4Pattern = regexp.MustCompile(
	`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
)

// validateUUID checks if a string is a valid UUID v4.
func validateUUID(s string) error {
	if s == "" {
		return ErrEmptyID
	}
	if !uuidV4Pattern.MatchString(strings.ToLower(s)) {
		return ErrInvalidIDFormat
	}
	return nil
}

// AccountID represents a validated account identifier.
type AccountID string

// NewAccountID validates and creates an AccountID from a UUID string.
func NewAccountID(s string) types.Result[AccountID] {
	if err := validateUUID(s); err != nil {
		return types.Err[AccountID](err)
	}
	return types.Ok(AccountID(s))
}

// String returns the string representation of the AccountID.
func (id AccountID) String() string { return string(id) }

// MarshalJSON implements json.Marshaler.
func (id AccountID) MarshalJSON() ([]byte, error) {
	return []byte(`"` + string(id) + `"`), nil
}

// UnmarshalJSON implements json.Unmarshaler with validation.
func (id *AccountID) UnmarshalJSON(data []byte) error {
	s := strings.Trim(string(data), `"`)
	result := NewAccountID(s)
	if result.IsError() {
		return result.Error()
	}
	*id = result.MustGet()
	return nil
}

// CustomerID represents a validated customer identifier.
type CustomerID string

// NewCustomerID validates and creates a CustomerID from a UUID string.
func NewCustomerID(s string) types.Result[CustomerID] {
	if err := validateUUID(s); err != nil {
		return types.Err[CustomerID](err)
	}
	return types.Ok(CustomerID(s))
}

// String returns the string representation of the CustomerID.
func (id CustomerID) String() string { return string(id) }

// MarshalJSON implements json.Marshaler.
func (id CustomerID) MarshalJSON() ([]byte, error) {
	return []byte(`"` + string(id) + `"`), nil
}

// UnmarshalJSON implements json.Unmarshaler with validation.
func (id *CustomerID) UnmarshalJSON(data []byte) error {
	s := strings.Trim(string(data), `"`)
	result := NewCustomerID(s)
	if result.IsError() {
		return result.Error()
	}
	*id = result.MustGet()
	return nil
}

// TransactionID represents a validated transaction identifier.
type TransactionID string

// NewTransactionID validates and creates a TransactionID from a UUID string.
func NewTransactionID(s string) types.Result[TransactionID] {
	if err := validateUUID(s); err != nil {
		return types.Err[TransactionID](err)
	}
	return types.Ok(TransactionID(s))
}

// String returns the string representation of the TransactionID.
func (id TransactionID) String() string { return string(id) }

// MarshalJSON implements json.Marshaler.
func (id TransactionID) MarshalJSON() ([]byte, error) {
	return []byte(`"` + string(id) + `"`), nil
}

// UnmarshalJSON implements json.Unmarshaler with validation.
func (id *TransactionID) UnmarshalJSON(data []byte) error {
	s := strings.Trim(string(data), `"`)
	result := NewTransactionID(s)
	if result.IsError() {
		return result.Error()
	}
	*id = result.MustGet()
	return nil
}

// PostingID represents a validated posting identifier.
type PostingID string

// NewPostingID validates and creates a PostingID from a UUID string.
func NewPostingID(s string) types.Result[PostingID] {
	if err := validateUUID(s); err != nil {
		return types.Err[PostingID](err)
	}
	return types.Ok(PostingID(s))
}

// String returns the string representation of the PostingID.
func (id PostingID) String() string { return string(id) }

// MarshalJSON implements json.Marshaler.
func (id PostingID) MarshalJSON() ([]byte, error) {
	return []byte(`"` + string(id) + `"`), nil
}

// UnmarshalJSON implements json.Unmarshaler with validation.
func (id *PostingID) UnmarshalJSON(data []byte) error {
	s := strings.Trim(string(data), `"`)
	result := NewPostingID(s)
	if result.IsError() {
		return result.Error()
	}
	*id = result.MustGet()
	return nil
}

// LedgerID represents a validated ledger identifier.
type LedgerID string

// NewLedgerID validates and creates a LedgerID from a UUID string.
func NewLedgerID(s string) types.Result[LedgerID] {
	if err := validateUUID(s); err != nil {
		return types.Err[LedgerID](err)
	}
	return types.Ok(LedgerID(s))
}

// String returns the string representation of the LedgerID.
func (id LedgerID) String() string { return string(id) }

// MarshalJSON implements json.Marshaler.
func (id LedgerID) MarshalJSON() ([]byte, error) {
	return []byte(`"` + string(id) + `"`), nil
}

// UnmarshalJSON implements json.Unmarshaler with validation.
func (id *LedgerID) UnmarshalJSON(data []byte) error {
	s := strings.Trim(string(data), `"`)
	result := NewLedgerID(s)
	if result.IsError() {
		return result.Error()
	}
	*id = result.MustGet()
	return nil
}
