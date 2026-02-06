// Package valuation provides storage and retrieval for ValuationMethods
// and ValuationPolicies with bi-temporal support and SYSTEM defaults.
package valuation

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// LifecycleStatus represents the lifecycle status of a valuation method or policy.
type LifecycleStatus string

// Lifecycle statuses for valuation methods and policies.
const (
	StatusInitiated  LifecycleStatus = "INITIATED"
	StatusActive     LifecycleStatus = "ACTIVE"
	StatusDeprecated LifecycleStatus = "DEPRECATED"
)

// Error types for valuation storage.
var (
	ErrNotFound              = errors.New("not found")
	ErrAlreadyExists         = errors.New("already exists with this name and version")
	ErrSystemReadOnly        = errors.New("system entries are read-only")
	ErrNotInitiated          = errors.New("must be in INITIATED status")
	ErrNotActive             = errors.New("must be in ACTIVE status")
	ErrInvalidCEL            = errors.New("invalid CEL expression")
	ErrRequiredPolicyMissing = errors.New("required policy does not exist or is not active")
)

// Method represents a Starlark valuation procedure that converts between instruments.
type Method struct {
	ID               uuid.UUID
	Name             string
	Version          int
	InputInstrument  string
	OutputInstrument string
	LogicScript      string
	LogicHash        string
	RequiredPolicies []string
	LifecycleStatus  LifecycleStatus
	IsSystem         bool
	Description      string
	CreatedAt        time.Time
	ActivatedAt      *time.Time
	DeprecatedAt     *time.Time
	ValidFrom        time.Time
	ValidTo          *time.Time
}

// Policy represents a named CEL expression used by valuation methods.
type Policy struct {
	ID              uuid.UUID
	Name            string
	Version         int
	CelExpression   string
	CelHash         string
	InputSchema     []byte
	OutputType      string
	EstimatedCost   int
	LifecycleStatus LifecycleStatus
	IsSystem        bool
	Description     string
	CreatedAt       time.Time
	ActivatedAt     *time.Time
	DeprecatedAt    *time.Time
	ValidFrom       time.Time
	ValidTo         *time.Time
}

// DryRunResult contains the result of a policy dry-run evaluation.
type DryRunResult struct {
	Success       bool
	Output        string
	EstimatedCost int
	Errors        []string
}

// MethodRepository defines the interface for managing valuation methods.
// All methods extract tenant context from ctx using shared/platform/tenant.
type MethodRepository interface {
	// Create creates a new valuation method in INITIATED status.
	// Returns ErrSystemReadOnly if is_system=true is attempted.
	// Returns ErrAlreadyExists if a method with the same name+version exists.
	Create(ctx context.Context, m *Method) error

	// GetByID retrieves a method by its UUID.
	// If knowledgeAt is non-nil, performs a bi-temporal query.
	GetByID(ctx context.Context, id uuid.UUID, knowledgeAt *time.Time) (*Method, error)

	// Resolve finds the active method for given instruments, checking tenant first then SYSTEM.
	Resolve(ctx context.Context, inputInstrument, outputInstrument string) (*Method, error)

	// Activate transitions a method from INITIATED to ACTIVE.
	// Validates that all required_policies exist and are active.
	Activate(ctx context.Context, id uuid.UUID) error

	// Deprecate transitions a method from ACTIVE to DEPRECATED.
	Deprecate(ctx context.Context, id uuid.UUID) error
}

// PolicyRepository defines the interface for managing valuation policies.
// All methods extract tenant context from ctx using shared/platform/tenant.
type PolicyRepository interface {
	// Create creates a new valuation policy in INITIATED status.
	// Returns ErrSystemReadOnly if is_system=true is attempted.
	// Returns ErrAlreadyExists if a policy with the same name+version exists.
	// Returns ErrInvalidCEL if the CEL expression fails compilation.
	Create(ctx context.Context, p *Policy) error

	// GetByName retrieves a policy by name.
	// If knowledgeAt is non-nil, performs a bi-temporal query.
	GetByName(ctx context.Context, name string, knowledgeAt *time.Time) (*Policy, error)

	// Resolve finds the active policy by name, checking tenant first then SYSTEM.
	Resolve(ctx context.Context, name string) (*Policy, error)

	// Activate transitions a policy from INITIATED to ACTIVE.
	Activate(ctx context.Context, id uuid.UUID) error

	// Deprecate transitions a policy from ACTIVE to DEPRECATED.
	Deprecate(ctx context.Context, id uuid.UUID) error

	// DryRun evaluates a policy's CEL expression with sample inputs.
	DryRun(ctx context.Context, policyName string, sampleInputs map[string]string) (*DryRunResult, error)
}
