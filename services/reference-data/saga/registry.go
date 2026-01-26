// Package saga provides the SagaRegistry interface and implementation
// for managing saga definitions with lifecycle management.
package saga

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Status represents the lifecycle status of a saga definition.
type Status string

const (
	// StatusDraft indicates a saga that is not yet active and can be modified.
	StatusDraft Status = "DRAFT"

	// StatusActive indicates a saga that is in use and immutable (script frozen).
	StatusActive Status = "ACTIVE"

	// StatusDeprecated indicates a saga that should no longer be used for new executions
	// but existing executions with this saga remain valid.
	StatusDeprecated Status = "DEPRECATED"
)

// Error types for the saga registry.
var (
	// ErrNotFound is returned when a saga definition cannot be found.
	ErrNotFound = errors.New("saga definition not found")

	// ErrSystemSagaReadOnly is returned when attempting to modify a system saga.
	// System sagas are seeded during tenant provisioning with is_system=true
	// and cannot be modified through the registry API.
	ErrSystemSagaReadOnly = errors.New("system sagas are read-only")

	// ErrInvalidStatus is returned when a saga is not in the required status.
	ErrInvalidStatus = errors.New("invalid saga status")

	// ErrInvalidStateTransition is returned for illegal status transitions.
	// Valid transitions: DRAFT→ACTIVE, ACTIVE→DEPRECATED.
	ErrInvalidStateTransition = errors.New("invalid state transition")

	// ErrNotDraft is returned when attempting to modify a saga that is not in DRAFT status.
	ErrNotDraft = errors.New("saga must be in DRAFT status")

	// ErrNotActive is returned when attempting operations that require ACTIVE status.
	ErrNotActive = errors.New("saga must be in ACTIVE status")

	// ErrOptimisticLock is returned when concurrent modification is detected.
	ErrOptimisticLock = errors.New("optimistic lock failure: saga was modified")

	// ErrAlreadyExists is returned when creating a saga with existing name+version.
	ErrAlreadyExists = errors.New("saga with this name and version already exists")

	// ErrSuccessorInvalid is returned when the specified successor saga is invalid.
	// A valid successor must exist, be in ACTIVE status, have the same name, and not be self-referential.
	ErrSuccessorInvalid = errors.New("successor saga is invalid: must exist, be ACTIVE, have same name, and not be self-referential")

	// ErrValidationFailed is returned when the saga script fails validation.
	ErrValidationFailed = errors.New("saga validation failed")

	// ErrPlatformDefinitionNotFound is returned when a saga references a platform definition
	// via platform_ref but the platform definition no longer exists.
	ErrPlatformDefinitionNotFound = errors.New("referenced platform saga definition not found")

	// ErrNoScriptSource is returned when a saga has neither a custom script nor a platform_ref.
	ErrNoScriptSource = errors.New("saga has no script source: neither custom script nor platform reference")

	// ErrScriptHashMismatch is returned during replay when the pinned script hash
	// does not match the current script, indicating potential corruption.
	ErrScriptHashMismatch = errors.New("script hash mismatch: pinned version differs from current")

	// ErrPinnedVersionNotFound is returned when attempting to replay a saga
	// whose pinned platform version no longer exists.
	ErrPinnedVersionNotFound = errors.New("pinned platform saga version not found")
)

// Definition represents a Starlark saga workflow definition
// that can be executed by the saga orchestrator.
type Definition struct {
	// ID is the unique identifier for this definition.
	ID uuid.UUID

	// Name is the human-readable saga name (e.g., "withdrawal", "deposit").
	Name string

	// Version allows multiple versions of the same saga name.
	// Versions start at 1. Use GetActive to retrieve the latest active version.
	Version int

	// Script is the Starlark source code defining the saga workflow.
	Script string

	// Status is the current lifecycle status.
	Status Status

	// IsSystem indicates this is a system saga seeded during tenant provisioning.
	// System sagas are read-only - CreateDraft, UpdateDefinition, ActivateSaga,
	// and DeprecateSaga all reject operations on is_system=true sagas.
	IsSystem bool

	// PreconditionsExpression is an optional CEL expression for validating
	// preconditions before saga execution. Available variables depend on context.
	PreconditionsExpression string

	// DisplayName is a human-readable name.
	DisplayName string

	// Description provides additional context about this saga.
	Description string

	// CreatedAt is when this definition was created.
	CreatedAt time.Time

	// UpdatedAt is when this definition was last modified.
	UpdatedAt time.Time

	// ActivatedAt is when this definition transitioned to ACTIVE (nil if never activated).
	ActivatedAt *time.Time

	// DeprecatedAt is when this definition transitioned to DEPRECATED (nil if not deprecated).
	DeprecatedAt *time.Time

	// SuccessorID is the UUID of the saga that replaces this one when deprecated.
	// This creates a forward lineage chain: when querying a deprecated saga,
	// clients can follow SuccessorID to find the current replacement.
	// Only set when Status is DEPRECATED. Nil if no successor designated.
	SuccessorID *uuid.UUID

	// PlatformRef is an optional FK to public.platform_saga_definition.
	// When set, this saga inherits its script from the platform template.
	// Mutually exclusive with Script: either PlatformRef OR Script is set, never both.
	PlatformRef *uuid.UUID

	// OverrideReason is an audit trail explaining why the tenant deviated from the platform default.
	OverrideReason string

	// PlatformVersionAtOverride tracks which platform saga version was active
	// when this override was created. Useful for migration impact analysis.
	PlatformVersionAtOverride string

	// ResolvedScript is the effective script after fallback resolution.
	// When Script is set, this equals Script (tenant override).
	// When PlatformRef is set and Script is empty, this is populated from the platform definition.
	// This field is populated by GetActive/GetByID queries, not stored in the database.
	ResolvedScript string

	// UsedPlatformFallback indicates whether the resolved script came from
	// the platform definition (true) or the tenant's own script (false).
	// This is populated during query resolution, not stored in the database.
	UsedPlatformFallback bool
}

// Validator validates saga definitions before activation.
// Implementations may check Starlark syntax, required functions, etc.
type Validator interface {
	// Validate checks if the saga definition is valid for activation.
	// Returns nil if valid, or an error describing validation failures.
	Validate(ctx context.Context, def *Definition) error
}

// Registry defines the interface for managing saga definitions.
// All methods extract tenant context from ctx using shared/platform/tenant.
// Schema routing is handled by PostgreSQL search_path.
//
// System Saga Semantics:
// System sagas are COPIED into each tenant's schema during tenant provisioning
// with is_system=true. This registry does NOT seed system sagas - it only
// enforces the read-only constraint.
//
// Tenant Resolution (GetActive):
// When resolving the active saga for a name, the registry checks in order:
//  1. Tenant's custom saga (is_system=FALSE, status=ACTIVE, highest version)
//  2. Platform default saga (is_system=TRUE, status=ACTIVE, highest version)
//
// This allows tenants to override platform defaults with custom sagas.
type Registry interface {
	// GetByID retrieves a specific saga by its UUID.
	// Returns ErrNotFound if the saga doesn't exist.
	GetByID(ctx context.Context, id uuid.UUID) (*Definition, error)

	// GetDefinition retrieves a specific saga by name and version.
	// Returns ErrNotFound if the saga doesn't exist.
	// The tenant schema is determined from ctx via tenant.FromContext.
	GetDefinition(ctx context.Context, name string, version int) (*Definition, error)

	// GetActive retrieves the active saga for a name using tenant resolution.
	// Resolution order:
	//  1. Tenant override (is_system=FALSE, status=ACTIVE, highest version)
	//  2. Platform default (is_system=TRUE, status=ACTIVE, highest version)
	// Returns ErrNotFound if no active version exists.
	GetActive(ctx context.Context, name string) (*Definition, error)

	// ListByStatus retrieves all sagas with the specified status.
	// Returns both system sagas (is_system=true) and tenant-specific sagas.
	ListByStatus(ctx context.Context, status Status) ([]*Definition, error)

	// CreateDraft creates a new saga definition in DRAFT status.
	// Returns ErrSystemSagaReadOnly if is_system=true is attempted.
	// Returns ErrAlreadyExists if a saga with the same name+version exists.
	// The saga's script is NOT validated at creation time - validation
	// occurs at activation (see ActivateSaga).
	CreateDraft(ctx context.Context, def *Definition) error

	// UpdateDefinition updates a DRAFT saga definition.
	// Returns ErrSystemSagaReadOnly if the saga has is_system=true.
	// Returns ErrNotDraft if the saga is not in DRAFT status.
	// Uses optimistic locking via UpdatedAt.
	UpdateDefinition(ctx context.Context, id uuid.UUID, updates *Definition) error

	// ActivateSaga transitions a saga from DRAFT to ACTIVE.
	// Returns ErrSystemSagaReadOnly if the saga has is_system=true.
	// Returns ErrNotDraft if not currently in DRAFT status.
	// Returns ErrValidationFailed if the saga script fails validation.
	// Once activated, the script becomes immutable.
	ActivateSaga(ctx context.Context, id uuid.UUID) error

	// DeprecateSaga transitions a saga from ACTIVE to DEPRECATED.
	// Returns ErrSystemSagaReadOnly if the saga has is_system=true.
	// Returns ErrNotActive if not currently in ACTIVE status.
	// Returns ErrSuccessorInvalid if successorID is provided but refers to an invalid saga.
	// Sagas in DEPRECATED status remain valid for existing executions but are not used for new ones.
	// The successorID optionally points to the replacement saga (must be ACTIVE with same name).
	DeprecateSaga(ctx context.Context, id uuid.UUID, successorID *uuid.UUID) error
}
