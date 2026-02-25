// Package domain contains the domain model for the Internal Account service.
package domain

import (
	"errors"
)

// Package-level validation errors for CorrespondentDetails.
var (
	errBankIDRequired             = errors.New("bank ID is required")
	errBankNameTooShort           = errors.New("bank name must be at least 3 characters")
	errExternalAccountRefRequired = errors.New("external account reference is required")
)

// CorrespondentDetails represents the correspondent bank relationship details for an internal account.
// This is a value object (no identity) that captures the relationship between an internal account
// and its corresponding account at an external correspondent bank.
//
// Value object semantics: immutable after construction, equality based on all fields.
type CorrespondentDetails struct {
	// bankID is the identifier for the correspondent bank (e.g., internal bank code, routing number).
	bankID string

	// bankName is the human-readable name of the correspondent bank.
	bankName string

	// externalAccountRef is the correspondent bank's reference for this account
	// (e.g., their internal account number, nostro/vostro reference).
	externalAccountRef string

	// swiftCode is the optional BIC/SWIFT code for international routing.
	swiftCode string

	// attributes provides extensibility for additional correspondent-specific metadata.
	attributes map[string]string
}

// NewCorrespondentDetails creates a new CorrespondentDetails with required fields.
// Returns an error if validation fails.
//
// Validation rules:
//   - bankID: required, minimum 1 character
//   - bankName: required, minimum 3 characters
//   - externalAccountRef: required, minimum 1 character
func NewCorrespondentDetails(bankID, bankName, externalAccountRef string) (*CorrespondentDetails, error) {
	return NewCorrespondentDetailsWithOptions(bankID, bankName, externalAccountRef, "", nil)
}

// NewCorrespondentDetailsWithOptions creates a new CorrespondentDetails with all fields including optional ones.
// Returns an error if validation fails.
//
// Validation rules:
//   - bankID: required, minimum 1 character
//   - bankName: required, minimum 3 characters
//   - externalAccountRef: required, minimum 1 character
//   - swiftCode: optional (no validation)
//   - attributes: optional (nil is valid)
func NewCorrespondentDetailsWithOptions(
	bankID, bankName, externalAccountRef, swiftCode string,
	attributes map[string]string,
) (*CorrespondentDetails, error) {
	// Validate required fields
	if bankID == "" {
		return nil, errBankIDRequired
	}
	if len(bankName) < 3 {
		return nil, errBankNameTooShort
	}
	if externalAccountRef == "" {
		return nil, errExternalAccountRefRequired
	}

	// Deep copy attributes map to ensure immutability
	var attrsCopy map[string]string
	if attributes != nil {
		attrsCopy = make(map[string]string, len(attributes))
		for k, v := range attributes {
			attrsCopy[k] = v
		}
	}

	return &CorrespondentDetails{
		bankID:             bankID,
		bankName:           bankName,
		externalAccountRef: externalAccountRef,
		swiftCode:          swiftCode,
		attributes:         attrsCopy,
	}, nil
}

// BankID returns the correspondent bank identifier.
func (c *CorrespondentDetails) BankID() string {
	if c == nil {
		return ""
	}
	return c.bankID
}

// BankName returns the human-readable correspondent bank name.
func (c *CorrespondentDetails) BankName() string {
	if c == nil {
		return ""
	}
	return c.bankName
}

// ExternalAccountRef returns the correspondent bank's reference for this account.
func (c *CorrespondentDetails) ExternalAccountRef() string {
	if c == nil {
		return ""
	}
	return c.externalAccountRef
}

// SwiftCode returns the optional BIC/SWIFT code.
// Returns empty string if not set.
func (c *CorrespondentDetails) SwiftCode() string {
	if c == nil {
		return ""
	}
	return c.swiftCode
}

// Attributes returns a copy of the extensibility attributes.
// Returns nil if no attributes are set.
// The returned map is a copy to preserve immutability.
func (c *CorrespondentDetails) Attributes() map[string]string {
	if c == nil || c.attributes == nil {
		return nil
	}
	// Return a copy to preserve immutability
	result := make(map[string]string, len(c.attributes))
	for k, v := range c.attributes {
		result[k] = v
	}
	return result
}

// Equals compares two CorrespondentDetails for value equality.
// Two nil values are considered equal.
// A nil compared to a non-nil is not equal.
func (c *CorrespondentDetails) Equals(other *CorrespondentDetails) bool {
	// Both nil are equal
	if c == nil && other == nil {
		return true
	}
	// One nil, one non-nil are not equal
	if c == nil || other == nil {
		return false
	}

	// Compare all fields
	if c.bankID != other.bankID {
		return false
	}
	if c.bankName != other.bankName {
		return false
	}
	if c.externalAccountRef != other.externalAccountRef {
		return false
	}
	if c.swiftCode != other.swiftCode {
		return false
	}

	// Compare attributes maps
	if len(c.attributes) != len(other.attributes) {
		return false
	}
	for k, v := range c.attributes {
		if otherV, ok := other.attributes[k]; !ok || v != otherV {
			return false
		}
	}

	return true
}
