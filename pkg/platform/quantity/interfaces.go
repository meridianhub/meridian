// Package quantity defines the core interfaces for the Universal Asset System.
// These interfaces establish contracts for working with multi-dimensional quantities
// that can represent currencies, energy, commodities, carbon credits, and other assets.
package quantity

import (
	"context"
	"time"
)

// Dimension represents a physical or conceptual dimension that an instrument measures.
// Implementations must be comparable and suitable for use as map keys.
type Dimension interface {
	// String returns the canonical string representation of the dimension (e.g., "CURRENCY", "ENERGY").
	String() string

	// Validate returns an error if the dimension is not valid.
	Validate() error
}

// Attribute represents a key-value pair attached to a quantity.
type Attribute struct {
	Key   string
	Value string
}

// Quantity represents an amount of a specific instrument with optional attributes.
// The type parameter D constrains the dimension type for compile-time safety.
type Quantity[D Dimension] interface {
	// Amount returns the decimal amount as a string for arbitrary precision.
	Amount() string

	// InstrumentCode returns the instrument identifier (e.g., "USD", "KWH").
	InstrumentCode() string

	// Version returns the instrument definition version.
	Version() int32

	// Attributes returns the key-value attributes attached to this quantity.
	// Returns a copy to prevent mutation.
	Attributes() []Attribute

	// Dimension returns the dimension of this quantity.
	Dimension() D

	// ValidFrom returns the time from which this quantity is valid, or nil if unbounded.
	ValidFrom() *time.Time

	// ValidTo returns the time until which this quantity is valid, or nil if unbounded.
	ValidTo() *time.Time

	// Source returns the origin identifier for this quantity.
	Source() string

	// Add returns a new quantity that is the sum of this quantity and other.
	// Returns an error if the quantities cannot be added (different instruments, incompatible attributes).
	Add(other Quantity[D]) (Quantity[D], error)

	// Subtract returns a new quantity that is this quantity minus other.
	// Returns an error if the quantities cannot be subtracted.
	Subtract(other Quantity[D]) (Quantity[D], error)

	// Multiply returns a new quantity with the amount multiplied by the given factor.
	// The factor is a string decimal for precision.
	Multiply(factor string) (Quantity[D], error)

	// IsNegative returns true if the amount is negative.
	IsNegative() bool

	// IsZero returns true if the amount is zero.
	IsZero() bool

	// FungibilityKey returns the poolability key for this quantity.
	// Quantities with the same key can be pooled together.
	FungibilityKey() string
}

// InstrumentDefinition represents the reference data for an instrument type.
// Note: TenantID is NOT included here. Tenant isolation is handled at the database
// schema level - each tenant has their own schema. The tenant context flows through
// request headers and is extracted by interceptors for schema routing.
type InstrumentDefinition struct {
	ID                       string
	Code                     string
	Version                  int32
	Dimension                string
	Precision                int32
	Status                   string
	ValidationExpression     string
	FungibilityKeyExpression string
	ErrorMessageExpression   string
	AttributeSchema          string
	DisplayName              string
	Description              string
	CreatedAt                time.Time
	ActivatedAt              *time.Time
	DeprecatedAt             *time.Time
	IsSystem                 bool // true = seeded during tenant provisioning, read-only
}

// InstrumentRegistry provides access to instrument definitions.
// Tenant context is extracted from ctx by the implementation (schema-per-tenant routing).
type InstrumentRegistry interface {
	// GetDefinition retrieves an instrument definition by code and version.
	// Tenant is determined from ctx (extracted from request headers by interceptors).
	// Returns an error if the instrument is not found.
	GetDefinition(ctx context.Context, code string, version int32) (*InstrumentDefinition, error)

	// GetActiveDefinition retrieves the active version of an instrument.
	// Returns an error if no active version exists.
	GetActiveDefinition(ctx context.Context, code string) (*InstrumentDefinition, error)

	// ListActive returns all active instrument definitions for the tenant (from ctx).
	ListActive(ctx context.Context) ([]*InstrumentDefinition, error)

	// CreateDraft creates a new instrument definition in DRAFT status.
	// Returns the created definition with assigned ID.
	CreateDraft(ctx context.Context, def *InstrumentDefinition) (*InstrumentDefinition, error)

	// ActivateInstrument transitions an instrument from DRAFT to ACTIVE status.
	// Returns an error if the instrument is not in DRAFT status.
	ActivateInstrument(ctx context.Context, code string, version int32) error

	// DeprecateInstrument transitions an instrument to DEPRECATED status.
	// Returns an error if the instrument is not in ACTIVE status.
	DeprecateInstrument(ctx context.Context, code string, version int32) error
}

// CachedInstrumentRegistry wraps an InstrumentRegistry with caching capabilities.
// Cache keys are internally scoped by tenant (determined from ctx during lookups).
type CachedInstrumentRegistry interface {
	InstrumentRegistry

	// InvalidateCache removes cached entries for the specified instrument within the
	// current tenant context (from ctx). Pass empty code to invalidate all instruments.
	InvalidateCache(ctx context.Context, code string)

	// InvalidateAll removes all cached entries across all tenants.
	// Use with caution - typically only for system-wide cache refresh.
	InvalidateAll()
}

// CELEvaluationResult contains the result of a CEL expression evaluation.
type CELEvaluationResult struct {
	Valid        bool
	ErrorMessage string
	ResultValue  any
}

// InstrumentContext provides instrument metadata for CEL expression evaluation.
// CEL expressions can access these values via the "instrument" variable.
// Example: "instrument.precision == 2 && instrument.dimension == 'CURRENCY'"
type InstrumentContext struct {
	Code      string // Instrument code (e.g., "USD", "KWH")
	Version   int32  // Instrument definition version
	Dimension string // Physical/conceptual dimension (e.g., "CURRENCY", "ENERGY")
	Precision int32  // Number of decimal places (0-18)
}

// CELEvaluator evaluates Common Expression Language expressions for instrument validation.
type CELEvaluator interface {
	// Validate evaluates a validation expression against the given attributes.
	// The instrument context is available in CEL as "instrument" (e.g., "instrument.precision == 2").
	// Returns the validation result including any error message.
	Validate(ctx context.Context, expression string, amount string, instrument InstrumentContext, attributes []Attribute) (*CELEvaluationResult, error)

	// GenerateFungibilityKey evaluates a fungibility key expression.
	// The instrument context is available in CEL as "instrument".
	// Returns the computed key as a string.
	GenerateFungibilityKey(ctx context.Context, expression string, amount string, instrument InstrumentContext, attributes []Attribute) (string, error)

	// GenerateErrorMessage evaluates an error message expression.
	// The instrument context is available in CEL as "instrument".
	// Returns the computed error message.
	GenerateErrorMessage(ctx context.Context, expression string, amount string, instrument InstrumentContext, attributes []Attribute) (string, error)
}
