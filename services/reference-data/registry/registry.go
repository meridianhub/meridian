// Package registry provides the InstrumentRegistry interface and implementation
// for managing instrument definitions with lifecycle management and CEL validation.
package registry

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Status represents the lifecycle status of an instrument definition.
type Status string

const (
	// StatusDraft indicates an instrument that is not yet active and can be modified.
	StatusDraft Status = "DRAFT"

	// StatusActive indicates an instrument that is in use and immutable (validation rules frozen).
	StatusActive Status = "ACTIVE"

	// StatusDeprecated indicates an instrument that should no longer be used for new positions
	// but existing positions with this instrument remain valid.
	StatusDeprecated Status = "DEPRECATED"
)

// Dimension represents the type of value an instrument measures.
type Dimension string

// Dimension constants define the types of values an instrument can measure.
const (
	DimensionMonetary Dimension = "MONETARY"
	DimensionEnergy   Dimension = "ENERGY"
	DimensionQuantity Dimension = "QUANTITY"
	DimensionCompute  Dimension = "COMPUTE"
	DimensionTime     Dimension = "TIME"
	DimensionMass     Dimension = "MASS"
	DimensionVolume   Dimension = "VOLUME"
	DimensionCarbon   Dimension = "CARBON"
	DimensionData     Dimension = "DATA"
)

// Error types for the registry.
var (
	// ErrNotFound is returned when an instrument definition cannot be found.
	ErrNotFound = errors.New("instrument definition not found")

	// ErrSystemInstrumentReadOnly is returned when attempting to modify a system instrument.
	// System instruments (USD, EUR, GBP, etc.) are seeded during tenant provisioning with
	// is_system=true and cannot be modified through the registry API.
	ErrSystemInstrumentReadOnly = errors.New("system instruments are read-only")

	// ErrInvalidStatus is returned when an instrument is not in the required status.
	ErrInvalidStatus = errors.New("invalid instrument status")

	// ErrInvalidStateTransition is returned for illegal status transitions.
	// Valid transitions: DRAFT→ACTIVE, ACTIVE→DEPRECATED.
	ErrInvalidStateTransition = errors.New("invalid state transition")

	// ErrNotDraft is returned when attempting to modify an instrument that is not in DRAFT status.
	ErrNotDraft = errors.New("instrument must be in DRAFT status")

	// ErrNotActive is returned when attempting operations that require ACTIVE status.
	ErrNotActive = errors.New("instrument must be in ACTIVE status")

	// ErrInvalidCEL is returned when a CEL expression fails compilation.
	// CEL expressions are compiled at creation time (fail-fast) to catch errors early.
	ErrInvalidCEL = errors.New("invalid CEL expression")

	// ErrOptimisticLock is returned when concurrent modification is detected.
	ErrOptimisticLock = errors.New("optimistic lock failure: instrument was modified")

	// ErrAlreadyExists is returned when creating an instrument with existing code+version.
	ErrAlreadyExists = errors.New("instrument with this code and version already exists")

	// ErrSuccessorInvalid is returned when the specified successor instrument is invalid.
	// A valid successor must exist, be in ACTIVE status, have the same dimension, and not be self-referential.
	ErrSuccessorInvalid = errors.New("successor instrument is invalid: must exist, be ACTIVE, have same dimension, and not be self-referential")
)

// InstrumentDefinition represents a measurement unit, currency, or asset type
// that can be tracked in the ledger.
type InstrumentDefinition struct {
	// ID is the unique identifier for this definition.
	ID uuid.UUID

	// Code is the human-readable instrument code (e.g., "USD", "EUR", "KWH").
	Code string

	// Version allows multiple versions of the same instrument code.
	// Versions start at 1. Use GetActiveDefinition to retrieve the latest active version.
	Version int

	// Dimension categorizes what this instrument measures.
	Dimension Dimension

	// Precision is the number of decimal places for amounts (0-18).
	Precision int

	// Status is the current lifecycle status.
	Status Status

	// IsSystem indicates this is a system instrument seeded during tenant provisioning.
	// System instruments are read-only - CreateDraft, UpdateDefinition, ActivateInstrument,
	// and DeprecateInstrument all reject operations on is_system=true instruments.
	IsSystem bool

	// ValidationExpression is an optional CEL expression for validating quantities.
	// Compiled at creation time (fail-fast). Available variables:
	//   - attributes: map[string]string
	//   - amount: string (decimal)
	//   - valid_from: timestamp
	//   - valid_to: timestamp
	//   - source: string
	ValidationExpression string

	// FungibilityKeyExpression is a CEL expression for generating bucket keys.
	// Empty string means default fungibility (all quantities are fungible).
	FungibilityKeyExpression string

	// ErrorMessageExpression is a CEL expression for custom validation error messages.
	ErrorMessageExpression string

	// AttributeSchema defines the JSON schema for allowed attributes (optional).
	AttributeSchema []byte

	// DisplayName is a human-readable name.
	DisplayName string

	// Description provides additional context about this instrument.
	Description string

	// CreatedAt is when this definition was created.
	CreatedAt time.Time

	// UpdatedAt is when this definition was last modified.
	UpdatedAt time.Time

	// ActivatedAt is when this definition transitioned to ACTIVE (nil if never activated).
	ActivatedAt *time.Time

	// DeprecatedAt is when this definition transitioned to DEPRECATED (nil if not deprecated).
	DeprecatedAt *time.Time

	// SuccessorID is the UUID of the instrument that replaces this one when deprecated.
	// This creates a forward lineage chain: when querying a deprecated instrument,
	// clients can follow SuccessorID to find the current replacement.
	// Only set when Status is DEPRECATED. Nil if no successor designated.
	SuccessorID *uuid.UUID
}

// AttributeBag represents the input data for CEL validation.
type AttributeBag struct {
	Attributes map[string]string
	Amount     string
	ValidFrom  *time.Time
	ValidTo    *time.Time
	Source     string
}

// ValidationResult contains the outcome of CEL validation.
type ValidationResult struct {
	Valid        bool
	ErrorMessage string
}

// InstrumentRegistry defines the interface for managing instrument definitions.
// All methods extract tenant context from ctx using shared/platform/tenant.
// Schema routing is handled by GORM tenant scope.
//
// System Instrument Semantics:
// System instruments (USD, EUR, GBP, etc.) are COPIED into each tenant's schema
// during tenant provisioning with is_system=true. This registry does NOT seed
// system instruments - it only enforces the read-only constraint.
type InstrumentRegistry interface {
	// GetDefinition retrieves a specific instrument by code and version.
	// Returns ErrNotFound if the instrument doesn't exist.
	// The tenant schema is determined from ctx via tenant.FromContext.
	GetDefinition(ctx context.Context, code string, version int) (*InstrumentDefinition, error)

	// GetActiveDefinition retrieves the latest ACTIVE version of an instrument.
	// Returns ErrNotFound if no active version exists.
	// When multiple active versions exist, returns the highest version number.
	GetActiveDefinition(ctx context.Context, code string) (*InstrumentDefinition, error)

	// ListActive retrieves all instruments with ACTIVE status.
	// Returns both system instruments (is_system=true) and tenant-specific instruments.
	// All instruments are queried from the tenant's schema - system instruments were
	// copied there during tenant provisioning.
	ListActive(ctx context.Context) ([]*InstrumentDefinition, error)

	// ListByStatus retrieves all instruments with the specified status.
	// Returns both system instruments (is_system=true) and tenant-specific instruments.
	// If status is empty, returns all instruments regardless of status.
	ListByStatus(ctx context.Context, status Status) ([]*InstrumentDefinition, error)

	// CreateDraft creates a new instrument definition in DRAFT status.
	// Returns ErrSystemInstrumentReadOnly if is_system=true is attempted.
	// Returns ErrInvalidCEL if any CEL expression fails compilation.
	// Returns ErrAlreadyExists if an instrument with the same code+version exists.
	// CEL expressions are compiled at creation time (fail-fast validation).
	CreateDraft(ctx context.Context, def *InstrumentDefinition) error

	// UpdateDefinition updates a DRAFT instrument definition.
	// Returns ErrSystemInstrumentReadOnly if the instrument has is_system=true.
	// Returns ErrNotDraft if the instrument is not in DRAFT status.
	// Returns ErrInvalidCEL if any CEL expression fails compilation.
	// Uses optimistic locking via UpdatedAt.
	UpdateDefinition(ctx context.Context, code string, version int, updates *InstrumentDefinition) error

	// ActivateInstrument transitions an instrument from DRAFT to ACTIVE.
	// Returns ErrSystemInstrumentReadOnly if the instrument has is_system=true.
	// Returns ErrNotDraft if not currently in DRAFT status.
	// Once activated, validation rules become immutable.
	ActivateInstrument(ctx context.Context, code string, version int) error

	// DeprecateInstrument transitions an instrument from ACTIVE to DEPRECATED.
	// Returns ErrSystemInstrumentReadOnly if the instrument has is_system=true.
	// Returns ErrNotActive if not currently in ACTIVE status.
	// Returns ErrSuccessorInvalid if successorID is provided but refers to an invalid instrument.
	// Instruments in DEPRECATED status remain valid for existing positions but are not used for new ones.
	// The successorID optionally points to the replacement instrument (must be ACTIVE with same dimension).
	DeprecateInstrument(ctx context.Context, code string, version int, successorID *uuid.UUID) error

	// ValidateAttributes executes the CEL validation expression against the provided attributes.
	// Returns ValidationResult indicating whether the attributes are valid.
	// If no validation expression is defined, always returns valid.
	// Uses pre-compiled CEL programs for performance.
	ValidateAttributes(ctx context.Context, code string, version int, attrs AttributeBag) (ValidationResult, error)
}
