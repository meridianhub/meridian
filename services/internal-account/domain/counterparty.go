// Package domain contains the domain model for the Internal Account service.
package domain

import (
	"errors"
)

// Package-level validation errors for CounterpartyDetails.
var (
	errCounterpartyIDRequired          = errors.New("counterparty ID is required")
	errCounterpartyNameTooShort        = errors.New("counterparty name must be at least 3 characters")
	errCounterpartyExternalRefRequired = errors.New("counterparty external reference is required")
)

// CounterpartyDetails represents the counterparty relationship details for an internal account.
// This is a value object (no identity) that captures the relationship between an internal account
// and its corresponding account at an external counterparty.
//
// Value object semantics: immutable after construction, equality based on all fields.
type CounterpartyDetails struct {
	// counterpartyID is the identifier for the counterparty (e.g., internal code, routing number).
	counterpartyID string

	// counterpartyName is the human-readable name of the counterparty.
	counterpartyName string

	// externalRef is the counterparty's reference for this account
	// (e.g., their internal account number, nostro/vostro reference).
	externalRef string

	// attributes provides extensibility for additional counterparty-specific metadata.
	// Product-type-specific fields (e.g., swift_code for banking) are stored here.
	attributes map[string]string
}

// NewCounterpartyDetails creates a new CounterpartyDetails with required fields.
// Returns an error if validation fails.
//
// Validation rules:
//   - counterpartyID: required, minimum 1 character
//   - counterpartyName: required, minimum 3 characters
//   - externalRef: required, minimum 1 character
func NewCounterpartyDetails(counterpartyID, counterpartyName, externalRef string) (*CounterpartyDetails, error) {
	return NewCounterpartyDetailsWithOptions(counterpartyID, counterpartyName, externalRef, nil)
}

// NewCounterpartyDetailsWithOptions creates a new CounterpartyDetails with all fields including optional ones.
// Returns an error if validation fails.
//
// Validation rules:
//   - counterpartyID: required, minimum 1 character
//   - counterpartyName: required, minimum 3 characters
//   - externalRef: required, minimum 1 character
//   - attributes: optional (nil is valid)
func NewCounterpartyDetailsWithOptions(
	counterpartyID, counterpartyName, externalRef string,
	attributes map[string]string,
) (*CounterpartyDetails, error) {
	// Validate required fields
	if counterpartyID == "" {
		return nil, errCounterpartyIDRequired
	}
	if len(counterpartyName) < 3 {
		return nil, errCounterpartyNameTooShort
	}
	if externalRef == "" {
		return nil, errCounterpartyExternalRefRequired
	}

	// Deep copy attributes map to ensure immutability
	var attrsCopy map[string]string
	if attributes != nil {
		attrsCopy = make(map[string]string, len(attributes))
		for k, v := range attributes {
			attrsCopy[k] = v
		}
	}

	return &CounterpartyDetails{
		counterpartyID:   counterpartyID,
		counterpartyName: counterpartyName,
		externalRef:      externalRef,
		attributes:       attrsCopy,
	}, nil
}

// CounterpartyID returns the counterparty identifier.
func (c *CounterpartyDetails) CounterpartyID() string {
	if c == nil {
		return ""
	}
	return c.counterpartyID
}

// CounterpartyName returns the human-readable counterparty name.
func (c *CounterpartyDetails) CounterpartyName() string {
	if c == nil {
		return ""
	}
	return c.counterpartyName
}

// ExternalRef returns the counterparty's reference for this account.
func (c *CounterpartyDetails) ExternalRef() string {
	if c == nil {
		return ""
	}
	return c.externalRef
}

// Attributes returns a copy of the extensibility attributes.
// Returns nil if no attributes are set.
// The returned map is a copy to preserve immutability.
func (c *CounterpartyDetails) Attributes() map[string]string {
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

// Equals compares two CounterpartyDetails for value equality.
// Two nil values are considered equal.
// A nil compared to a non-nil is not equal.
func (c *CounterpartyDetails) Equals(other *CounterpartyDetails) bool {
	// Both nil are equal
	if c == nil && other == nil {
		return true
	}
	// One nil, one non-nil are not equal
	if c == nil || other == nil {
		return false
	}

	// Compare all fields
	if c.counterpartyID != other.counterpartyID {
		return false
	}
	if c.counterpartyName != other.counterpartyName {
		return false
	}
	if c.externalRef != other.externalRef {
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
