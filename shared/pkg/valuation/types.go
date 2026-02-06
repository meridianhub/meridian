// Package valuation provides the core valuation engine library for executing
// Starlark-based valuation methods with CEL policy integration.
//
// The valuation engine enables multi-asset value transformation with:
// - Read-only security sandbox (no filesystem, no network)
// - CEL policy execution with cost limits (10,000 units)
// - Calculation path audit trails
// - In-memory caching with TTL (5 minutes)
//
// Architecture:
//   - Policy Runtime: CEL compiler wrapper with cost validation
//   - Starlark Runtime: Sandboxed VM with 5s timeout, 64MB memory limit
//   - Builtins: Read-only functions (market_data, run_policy, quantity, Decimal)
//   - Cache: L1 in-memory cache for policies and methods
//
// Performance target: <5ms in-process execution (excluding network I/O)
package valuation

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// MaxPathEntries is the maximum number of calculation path entries allowed.
// If exceeded, a warning is logged and further entries are discarded.
const MaxPathEntries = 20

// Request represents a valuation request to transform a quantity into valued amount.
type Request struct {
	// RequestID is the unique identifier for idempotency.
	RequestID uuid.UUID

	// MethodID references the ValuationMethod in Reference Data.
	MethodID uuid.UUID

	// MethodVersion specifies the version to use. Nil means latest active version.
	MethodVersion *int

	// Quantity is the input quantity to be valued.
	Quantity Quantity

	// AccountID is the account context for valuation.
	AccountID uuid.UUID

	// PartyID is the party context for valuation.
	PartyID uuid.UUID

	// KnowledgeAt is the bi-temporal point for valuation (what we knew at this time).
	KnowledgeAt time.Time

	// Parameters are method-specific parameters (e.g., {"tier": "Gold", "gsp": "P"}).
	Parameters map[string]interface{}
}

// Validate checks that the request has all required fields.
func (r *Request) Validate() error {
	if r.RequestID == uuid.Nil {
		return fmt.Errorf("%w: %w", ErrInvalidRequest, errRequestIDRequired)
	}
	if r.MethodID == uuid.Nil {
		return fmt.Errorf("%w: %w", ErrInvalidRequest, errMethodIDRequired)
	}
	if r.Quantity.InstrumentCode == "" {
		return fmt.Errorf("%w: %w", ErrInvalidRequest, errInstrumentCodeRequired)
	}
	if r.AccountID == uuid.Nil {
		return fmt.Errorf("%w: %w", ErrInvalidRequest, errAccountIDRequired)
	}
	if r.PartyID == uuid.Nil {
		return fmt.Errorf("%w: %w", ErrInvalidRequest, errPartyIDRequired)
	}
	if r.KnowledgeAt.IsZero() {
		return fmt.Errorf("%w: %w", ErrInvalidRequest, errKnowledgeAtRequired)
	}
	return nil
}

// Response represents the result of a valuation execution.
type Response struct {
	// ValuedAmount is the output quantity (converted value).
	ValuedAmount Quantity

	// Analysis contains the audit trail for this valuation.
	Analysis *Analysis

	// CacheHit indicates whether the result was served from cache.
	CacheHit bool

	// ComputedAt is the timestamp when this valuation was computed.
	ComputedAt time.Time
}

// Analysis contains the audit trail and execution details for a valuation.
type Analysis struct {
	// CalculationPath records the steps taken during valuation (max 20 entries).
	CalculationPath []PathEntry

	// PoliciesExecuted records all CEL policies invoked during valuation.
	PoliciesExecuted []PolicyExecution

	// MarketDataSources lists external data sources queried.
	MarketDataSources []string

	// Warnings records non-fatal issues (e.g., stale cache, truncated path).
	Warnings []string
}

// AddPathEntry adds a calculation step to the audit trail.
// If the path already has MaxPathEntries, a warning is logged and the entry is discarded.
func (a *Analysis) AddPathEntry(description string, data map[string]interface{}) {
	if len(a.CalculationPath) >= MaxPathEntries {
		// Already at limit - add warning if not already present
		warningMsg := fmt.Sprintf("calculation path truncated at %d entries", MaxPathEntries)
		if len(a.Warnings) == 0 || a.Warnings[len(a.Warnings)-1] != warningMsg {
			a.Warnings = append(a.Warnings, warningMsg)
		}
		return
	}

	a.CalculationPath = append(a.CalculationPath, PathEntry{
		Description: description,
		Data:        data,
		Timestamp:   time.Now(),
	})
}

// RecordPolicyExecution records a CEL policy invocation.
func (a *Analysis) RecordPolicyExecution(
	policyName string,
	policyVersion int,
	inputs map[string]interface{},
	output interface{},
	costUnits uint64,
) {
	a.PoliciesExecuted = append(a.PoliciesExecuted, PolicyExecution{
		PolicyName:    policyName,
		PolicyVersion: policyVersion,
		Inputs:        inputs,
		Output:        output,
		CostUnits:     costUnits,
	})
}

// AddWarning appends a warning message to the analysis.
func (a *Analysis) AddWarning(msg string) {
	a.Warnings = append(a.Warnings, msg)
}

// PathEntry represents a single step in the calculation audit trail.
type PathEntry struct {
	// Description is a human-readable description of this step.
	Description string

	// Data contains contextual data for this step (e.g., prices, factors).
	Data map[string]interface{}

	// Timestamp is when this step was recorded.
	Timestamp time.Time
}

// PolicyExecution captures details of a CEL policy invocation.
type PolicyExecution struct {
	// PolicyName is the name of the policy that was executed.
	PolicyName string

	// PolicyVersion is the version of the policy that was executed.
	PolicyVersion int

	// Inputs are the input values provided to the policy.
	Inputs map[string]interface{}

	// Output is the result returned by the policy.
	Output interface{}

	// CostUnits is the CEL execution cost (must be <= 10,000).
	CostUnits uint64
}

// Quantity represents a dimensional value with amount, instrument, and attributes.
type Quantity struct {
	// Amount is the numeric value (arbitrary precision decimal).
	Amount decimal.Decimal

	// InstrumentCode is the asset type (e.g., "KWH", "USD", "GBP", "TONNE_CO2E").
	InstrumentCode string

	// Attributes are key-value metadata for this quantity (e.g., {"gsp": "P", "tou_period": "peak"}).
	Attributes map[string]string
}

// IsZero returns true if the amount is zero.
func (q Quantity) IsZero() bool {
	return q.Amount.IsZero()
}

// String returns a human-readable representation of the quantity.
func (q Quantity) String() string {
	return fmt.Sprintf("%s %s", q.Amount.String(), q.InstrumentCode)
}

// Error types for valuation operations.
var (
	// ErrPolicyNotFound indicates that a named policy could not be found in Reference Data.
	ErrPolicyNotFound = errors.New("policy not found")

	// ErrPolicyCostExceeded indicates that a policy's estimated cost exceeds the 10,000 unit limit.
	ErrPolicyCostExceeded = errors.New("policy cost exceeds limit")

	// ErrInputValidationFailed indicates that input parameters do not match the policy's input schema.
	ErrInputValidationFailed = errors.New("input validation failed")

	// ErrOutputMismatch indicates that the valuation output instrument does not match the declared output_instrument.
	ErrOutputMismatch = errors.New("valuation output instrument mismatch")

	// ErrStarlarkTimeout indicates that Starlark execution exceeded the 5-second timeout.
	ErrStarlarkTimeout = errors.New("starlark execution timeout")

	// ErrStarlarkMemoryLimit indicates that Starlark execution exceeded the 64MB memory limit.
	ErrStarlarkMemoryLimit = errors.New("starlark memory limit exceeded")

	// ErrStarlarkSandboxViolation indicates an attempt to violate sandbox constraints (filesystem, network).
	ErrStarlarkSandboxViolation = errors.New("starlark sandbox violation")

	// ErrMethodNotFound indicates that a valuation method could not be found.
	ErrMethodNotFound = errors.New("valuation method not found")

	// ErrInvalidRequest indicates that the request failed validation.
	ErrInvalidRequest = errors.New("invalid request")

	// Validation error sentinels
	errRequestIDRequired      = errors.New("request ID is required")
	errMethodIDRequired       = errors.New("method ID is required")
	errInstrumentCodeRequired = errors.New("instrument code is required")
	errAccountIDRequired      = errors.New("account ID is required")
	errPartyIDRequired        = errors.New("party ID is required")
	errKnowledgeAtRequired    = errors.New("knowledge_at is required")
)
