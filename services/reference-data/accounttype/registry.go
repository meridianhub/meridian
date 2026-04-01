package accounttype

import (
	"context"

	"github.com/google/uuid"
)

// AttributeBag represents the input data for CEL validation and eligibility checks.
type AttributeBag struct {
	Attributes map[string]string
	Amount     string
}

// ValidationResult contains the outcome of a validation or eligibility check.
type ValidationResult struct {
	Valid  bool
	Errors []string
}

// Registry defines the interface for managing account type definitions.
// All methods extract tenant context from ctx using shared/platform/tenant.
// Schema routing is handled by PostgreSQL search_path.
type Registry interface {
	// GetDefinitionByID retrieves a specific account type by its UUID.
	// Returns ErrNotFound if the definition doesn't exist.
	GetDefinitionByID(ctx context.Context, id uuid.UUID) (*Definition, error)

	// GetDefinition retrieves a specific account type by code and version.
	// Returns ErrNotFound if the definition doesn't exist.
	GetDefinition(ctx context.Context, code string, version int) (*Definition, error)

	// GetActiveDefinition retrieves the latest ACTIVE version of an account type.
	// Returns ErrNotFound if no active version exists.
	GetActiveDefinition(ctx context.Context, code string) (*Definition, error)

	// ListActive retrieves all account type definitions with ACTIVE status.
	ListActive(ctx context.Context) ([]*Definition, error)

	// ListAll retrieves account type definitions across all statuses.
	// Pass statusFilter to restrict results; an empty slice returns all statuses.
	ListAll(ctx context.Context, statusFilter []Status) ([]*Definition, error)

	// CreateDraft creates a new account type definition in DRAFT status.
	// Uses INSERT ... ON CONFLICT (code, version) DO NOTHING returning existing row.
	// Returns the existing definition if conflict (idempotent).
	// Compiles all CEL fields before persisting.
	// Validates AttributeSchema via jsonschema.
	CreateDraft(ctx context.Context, def *Definition) error

	// UpdateDefinition updates a DRAFT account type definition.
	// Returns ErrNotDraft if the definition is not in DRAFT status.
	// Returns ErrFieldImmutable if caller attempts to change Code, IsSystem, or BehaviorClass.
	// Uses optimistic locking via UpdatedAt.
	UpdateDefinition(ctx context.Context, code string, version int, updates *Definition) error

	// ActivateAccountType transitions a definition from DRAFT to ACTIVE.
	// Performs pre-checks (fail-fast, returns ALL errors):
	//   - Instrument exists and is ACTIVE in InstrumentRegistry
	//   - DefaultConversionMethodID references existing valuation method (if set)
	//   - All ValuationMethodTemplate entries reference valid methods and instruments
	//   - All CEL fields recompile successfully
	//   - AttributeSchema is valid JSON Schema; Attributes validate against it
	//   - If DefaultSagaPrefix non-empty: at least one saga starting with prefix exists
	//   - No duplicate ACTIVE code (check before hitting DB constraint)
	// Calling ActivateAccountType on already-ACTIVE definition returns nil (idempotent).
	ActivateAccountType(ctx context.Context, code string, version int) error

	// DeprecateAccountType transitions a definition from ACTIVE to DEPRECATED.
	// Sets successor_id if provided (write-once; returns ErrSuccessorWriteOnce if already set to different value).
	DeprecateAccountType(ctx context.Context, code string, version int, successorID *uuid.UUID) error

	// ValidateTransaction executes the CEL validation expression against the provided attributes.
	// Returns ValidationResult indicating whether the attributes are valid.
	// If no validation expression is defined, always returns valid.
	ValidateTransaction(ctx context.Context, code string, version int, attrs AttributeBag) (ValidationResult, error)

	// CheckEligibility executes the CEL eligibility expression against the provided attributes.
	// Returns ValidationResult indicating eligibility.
	// If no eligibility expression is defined, always returns valid (eligible).
	CheckEligibility(ctx context.Context, code string, version int, attrs AttributeBag) (ValidationResult, error)

	// GetProductFeatures returns the attributes (product features) for an account type.
	GetProductFeatures(ctx context.Context, code string, version int) (map[string]any, error)
}
