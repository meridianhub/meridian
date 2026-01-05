// Package quantity provides instrument types for the Universal Asset System.
//
// # Instrument Type
//
// Instrument is a lightweight identifier that uniquely identifies an asset type
// within the system. It contains the minimal information needed to validate
// that two quantities are compatible for arithmetic operations.
//
// For full instrument metadata (CEL expressions, attribute schema, status),
// see InstrumentDefinition in interfaces.go and the reference-data service.
//
// # Design Rationale
//
// The Dimension field is stored as a string rather than a type parameter because:
//   - Go generics are erased at runtime, making deserialization of type parameters impossible
//   - Dimension validation occurs at instrument creation time, not at compile time
//   - The Quantity[D] type provides compile-time safety at the quantity level
//
// The Instrument type is designed to be:
//   - Immutable: all fields are exported but should be treated as read-only after creation
//   - Comparable: can be used as map keys for caching and lookups
//   - Lightweight: minimal fields for identity, no CEL expressions or metadata
package quantity

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// MaxPrecision is the maximum allowed decimal precision for instruments.
// This aligns with the proto definition constraint and ensures decimal.Decimal
// can represent all values without loss of precision.
const MaxPrecision = 18

// InstrumentCodePattern defines the valid format for instrument codes.
// Codes must start with an uppercase letter and contain only uppercase letters,
// digits, and underscores. Examples: "USD", "KWH", "GPU_HOUR", "CARBON_CREDIT".
var InstrumentCodePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// ValidDimensions is the set of valid dimension string values.
// These correspond to the proto enum values in instrument.proto.
var ValidDimensions = map[string]bool{
	"CURRENCY": true,
	"ENERGY":   true,
	"MASS":     true,
	"VOLUME":   true,
	"TIME":     true,
	"COMPUTE":  true,
	"CARBON":   true,
	"DATA":     true,
	"COUNT":    true,
}

// Sentinel errors for instrument validation.
var (
	// ErrEmptyCode is returned when an instrument code is empty.
	ErrEmptyCode = errors.New("instrument code cannot be empty")

	// ErrInvalidCodeFormat is returned when an instrument code doesn't match the required pattern.
	ErrInvalidCodeFormat = errors.New("instrument code must start with uppercase letter and contain only uppercase letters, digits, and underscores")

	// ErrCodeTooLong is returned when an instrument code exceeds the maximum length.
	ErrCodeTooLong = errors.New("instrument code exceeds maximum length of 32 characters")

	// ErrInvalidDimension is returned when a dimension string is not recognized.
	ErrInvalidDimension = errors.New("invalid dimension")

	// ErrNegativePrecision is returned when precision is negative.
	ErrNegativePrecision = errors.New("precision cannot be negative")

	// ErrPrecisionTooHigh is returned when precision exceeds the maximum.
	ErrPrecisionTooHigh = errors.New("precision exceeds maximum of 18")
)

// Instrument represents a unique identifier for an asset type.
// It contains the minimal information needed for quantity compatibility checks.
//
// Two quantities can only be combined (added, subtracted) if they have
// the same instrument Code and Version. Dimension is used for compile-time
// type safety via the Quantity[D] generic type.
//
// Instrument is designed to be immutable after creation. Use NewInstrument
// to create validated instances.
type Instrument struct {
	// Code is the unique identifier for this instrument (e.g., "USD", "KWH").
	// Must match InstrumentCodePattern: start with uppercase letter,
	// contain only uppercase letters, digits, and underscores.
	Code string

	// Version is the schema version for this instrument definition.
	// Version 0 is valid and represents an unversioned or initial instrument.
	Version uint32

	// Dimension is the physical or conceptual dimension this instrument measures.
	// Stored as string for deserialization support. Must be one of ValidDimensions.
	Dimension string

	// Precision is the number of decimal places for this instrument (0-18).
	// Used for rounding and display formatting.
	Precision int
}

// NewInstrument creates a validated Instrument instance.
//
// Validation rules:
//   - Code must not be empty
//   - Code must match InstrumentCodePattern (uppercase letters, digits, underscores, starting with letter)
//   - Code must not exceed 32 characters
//   - Dimension must be one of ValidDimensions
//   - Precision must be in range [0, 18]
//
// Version 0 is allowed and represents an unversioned or initial instrument.
func NewInstrument(code string, version uint32, dimension string, precision int) (Instrument, error) {
	if err := validateCode(code); err != nil {
		return Instrument{}, err
	}

	if err := validateDimension(dimension); err != nil {
		return Instrument{}, err
	}

	if err := validatePrecision(precision); err != nil {
		return Instrument{}, err
	}

	return Instrument{
		Code:      code,
		Version:   version,
		Dimension: dimension,
		Precision: precision,
	}, nil
}

// validateCode checks that an instrument code is valid.
func validateCode(code string) error {
	if code == "" {
		return ErrEmptyCode
	}

	if len(code) > 32 {
		return ErrCodeTooLong
	}

	if !InstrumentCodePattern.MatchString(code) {
		return ErrInvalidCodeFormat
	}

	return nil
}

// validateDimension checks that a dimension string is valid.
func validateDimension(dimension string) error {
	// Normalize to uppercase for comparison
	normalized := strings.ToUpper(dimension)
	if !ValidDimensions[normalized] {
		return fmt.Errorf("%w: %s", ErrInvalidDimension, dimension)
	}
	return nil
}

// validatePrecision checks that precision is within valid range.
func validatePrecision(precision int) error {
	if precision < 0 {
		return ErrNegativePrecision
	}
	if precision > MaxPrecision {
		return ErrPrecisionTooHigh
	}
	return nil
}

// Equal returns true if this instrument has the same identity as other.
// Two instruments are equal if they have the same Code and Version.
// Dimension and Precision are not compared because they should be
// consistent for the same Code+Version from the reference data service.
func (i Instrument) Equal(other Instrument) bool {
	return i.Code == other.Code && i.Version == other.Version
}

// String returns a human-readable representation of the instrument.
func (i Instrument) String() string {
	return fmt.Sprintf("%s(v%d)", i.Code, i.Version)
}

// IsMonetary returns true if this instrument represents a monetary value (currency).
func (i Instrument) IsMonetary() bool {
	return i.Dimension == "CURRENCY"
}

// IsCommodity returns true if this instrument represents a non-monetary asset.
func (i Instrument) IsCommodity() bool {
	return i.Dimension != "CURRENCY" && i.Dimension != ""
}

// Validate checks that the instrument is valid.
// This can be used to validate instruments that were created without using NewInstrument
// (e.g., deserialized from storage or received over the wire).
func (i Instrument) Validate() error {
	if err := validateCode(i.Code); err != nil {
		return err
	}

	if err := validateDimension(i.Dimension); err != nil {
		return err
	}

	return validatePrecision(i.Precision)
}
