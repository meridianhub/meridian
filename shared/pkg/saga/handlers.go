package saga

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Handler errors.
var (
	// ErrHandlerAlreadyRegistered is returned when attempting to register a handler with a name that already exists.
	ErrHandlerAlreadyRegistered = errors.New("handler already registered")

	// ErrHandlerNotFound is returned when a requested handler does not exist in the registry.
	ErrHandlerNotFound = errors.New("handler not found")

	// ErrInvalidHandlerName is returned when a handler name is empty or invalid.
	ErrInvalidHandlerName = errors.New("invalid handler name")

	// ErrInvalidPercentage is returned when progress percentage is out of valid range [0, 100].
	ErrInvalidPercentage = errors.New("percentage must be between 0 and 100")

	// ErrMissingParam is returned when a required parameter is missing.
	ErrMissingParam = errors.New("missing required parameter")

	// ErrInvalidParamType is returned when a parameter has an unexpected type.
	ErrInvalidParamType = errors.New("invalid parameter type")

	// ErrInvalidDirection is returned when a direction parameter is not DEBIT or CREDIT.
	ErrInvalidDirection = errors.New("direction must be DEBIT or CREDIT")

	// ErrPartyScopeViolation is returned when a step tries to access a party outside its visible scope.
	// This is a fatal error that cannot be retried - it indicates a security/authorization failure.
	ErrPartyScopeViolation = errors.New("party scope violation: party_id not in visible_parties")

	// ErrConservationViolation is returned when a saga violates the Conservation of Dimension Rule.
	// This prevents Physics instruments (KWH, GAS) from producing more Physics instruments in settlement sagas,
	// which would create infinite causation loops.
	// This is a fatal error (FR-28) as it indicates a logical impossibility in the domain model.
	ErrConservationViolation = errors.New("conservation violation: settlement saga triggered by Physics instrument cannot produce Physics instruments")
)

// Direction constants for transaction operations.
const (
	DirectionDebit  = "DEBIT"
	DirectionCredit = "CREDIT"
)

// Public parameter validation helpers for use in Handler implementations.
// These helpers extract and validate parameters from the Starlark params map.
// They return ErrMissingParam if the parameter is not present, and ErrInvalidParamType
// if the parameter has an unexpected type.

// RequireStringParam extracts a required string parameter from Starlark params.
// Returns ErrMissingParam if not present, ErrInvalidParamType if wrong type.
// Use in Handler implementations to validate string inputs.
func RequireStringParam(params map[string]any, key string) (string, error) {
	val, ok := params[key]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrMissingParam, key)
	}
	str, ok := val.(string)
	if !ok {
		return "", fmt.Errorf("%w: %s must be string, got %T", ErrInvalidParamType, key, val)
	}
	return str, nil
}

// RequireDecimalParam extracts a required decimal parameter from Starlark params.
// Accepts decimal.Decimal, string (parsed), float64, int, or int64 values.
// Returns ErrMissingParam if not present, ErrInvalidParamType if wrong type.
// Use in Handler implementations to validate decimal/monetary inputs.
func RequireDecimalParam(params map[string]any, key string) (decimal.Decimal, error) {
	val, ok := params[key]
	if !ok {
		return decimal.Zero, fmt.Errorf("%w: %s", ErrMissingParam, key)
	}
	switch v := val.(type) {
	case decimal.Decimal:
		return v, nil
	case string:
		d, err := decimal.NewFromString(v)
		if err != nil {
			return decimal.Zero, fmt.Errorf("%w: %s must be decimal, got invalid string", ErrInvalidParamType, key)
		}
		return d, nil
	case float64:
		return decimal.NewFromFloat(v), nil
	case int:
		return decimal.NewFromInt(int64(v)), nil
	case int64:
		return decimal.NewFromInt(v), nil
	default:
		return decimal.Zero, fmt.Errorf("%w: %s must be decimal, got %T", ErrInvalidParamType, key, val)
	}
}

// RequireUUIDParam extracts a required UUID parameter from Starlark params.
// Accepts uuid.UUID or string (parsed) values.
// Returns ErrMissingParam if not present, ErrInvalidParamType if wrong type or parse fails.
// Use in Handler implementations to validate UUID inputs.
func RequireUUIDParam(params map[string]any, key string) (uuid.UUID, error) {
	val, ok := params[key]
	if !ok {
		return uuid.Nil, fmt.Errorf("%w: %s", ErrMissingParam, key)
	}
	switch v := val.(type) {
	case uuid.UUID:
		return v, nil
	case string:
		parsed, err := uuid.Parse(v)
		if err != nil {
			return uuid.Nil, fmt.Errorf("%w: %s must be UUID, got invalid string %q", ErrInvalidParamType, key, v)
		}
		return parsed, nil
	default:
		return uuid.Nil, fmt.Errorf("%w: %s must be UUID, got %T", ErrInvalidParamType, key, val)
	}
}

// RequireDirectionParam extracts a required direction parameter from Starlark params.
// Accepts only "DEBIT" or "CREDIT" string values.
// Returns ErrMissingParam if not present, ErrInvalidDirection if wrong value.
// Use in Handler implementations to validate transaction direction.
func RequireDirectionParam(params map[string]any, key string) (string, error) {
	val, ok := params[key]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrMissingParam, key)
	}
	str, ok := val.(string)
	if !ok {
		return "", fmt.Errorf("%w: %s must be string, got %T", ErrInvalidParamType, key, val)
	}
	if str != DirectionDebit && str != DirectionCredit {
		return "", fmt.Errorf("%w: got %q", ErrInvalidDirection, str)
	}
	return str, nil
}

// Handler is a service binding function that adapts Starlark parameters
// to gRPC service calls. Handlers receive StarlarkContext for platform
// features and return typed results.
//
// Handlers bridge the Starlark runtime to real backend services, providing
// PartyScope for tenant isolation, deterministic UUIDs, progress emission,
// and suspension capabilities. They are registered in the HandlerRegistry
// and invoked by the step executor when processing saga steps.
type Handler func(ctx *StarlarkContext, params map[string]any) (result any, err error)

// StarlarkContext provides execution context for step handlers.
// It includes the parent context for cancellation, saga metadata, and helper methods.
type StarlarkContext struct {
	// Context is the parent context for cancellation propagation.
	context.Context

	// PartyScope restricts data access to a specific party (tenant isolation).
	// This should be checked by handlers when accessing data.
	PartyScope *PartyScope

	// SagaExecutionID is the unique identifier for this saga execution.
	SagaExecutionID uuid.UUID

	// CorrelationID groups ALL related actions across the entire business operation (FR-24).
	// This ID is propagated to all downstream events for distributed tracing.
	CorrelationID uuid.UUID

	// KnowledgeAt enables bi-temporal queries - what we knew at a specific point in time.
	KnowledgeAt time.Time

	// IdempotencyKey is the unique idempotency key for this saga step execution.
	// Format: saga_{execution_id}_step_{step_index}
	// This key is used to ensure idempotent service calls - replaying the same step
	// will use the same idempotency key, allowing downstream services to deduplicate.
	// Handlers should propagate this key using shared/pkg/clients.PropagateIdempotencyKey
	// before making external service calls.
	IdempotencyKey string

	// Logger for structured logging within handlers.
	Logger *slog.Logger

	// LookupCache stores external lookup results for deterministic replay (FR-34).
	// When replaying a saga, this cache is pre-populated from the input snapshot
	// to ensure the same lookup results are returned even if Reference Data changed.
	LookupCache *LookupResultCache

	// TriggerInstrument is the instrument code that triggered this saga execution (e.g., "KWH", "GAS", "USD").
	// Used for conservation rule enforcement: settlement sagas triggered by Physics instruments (KWH, GAS)
	// cannot produce new Physics instruments to prevent causation loops.
	// Empty string means no specific instrument trigger (e.g., user-initiated actions).
	// This field is immutable after creation and set based on the event/transaction that triggered the saga.
	TriggerInstrument string

	// stepCounter tracks the number of handler invocations for idempotency key generation.
	// This is incremented atomically before each handler call to generate unique step IDs.
	// Protected by stepMutex for thread-safe access.
	stepCounter int
	stepMutex   sync.Mutex

	// Suspension state
	suspended     bool
	SuspendReason string
	ResumeAfter   time.Duration
}

// PartyScope defines the data access boundary for a saga execution.
// Handlers must respect this scope when querying or modifying data.
//
// The scope is resolved at saga start based on the executing party's type:
//   - INDIVIDUAL: Can only see own data
//   - ORGANIZATION: Can see own data plus all descendant parties
//   - SYSTEM: Can see all parties in the tenant
type PartyScope struct {
	// PartyID is the owning party's identifier.
	PartyID uuid.UUID

	// PartyType indicates the classification of the party (INDIVIDUAL, ORGANIZATION, SYSTEM).
	PartyType string

	// VisibleParties is the list of party IDs whose data the executing party can access.
	// This is immutable after creation.
	VisibleParties []uuid.UUID

	// TenantID is the tenant context (for multi-tenant deployments).
	TenantID string

	// Permissions lists the allowed operations for this scope.
	Permissions []string
}

// IsPhysicsInstrument returns true if the instrument represents a physical quantity (KWH, GAS).
// Physics instruments are subject to conservation laws - they cannot be created from nothing in settlement sagas.
func IsPhysicsInstrument(instrument string) bool {
	return instrument == "KWH" || instrument == "GAS"
}

// CheckConservationRule enforces the Conservation of Dimension Rule.
// Returns ErrConservationViolation if a settlement saga triggered by a Physics instrument
// attempts to produce the same Physics instrument, which would create a causation loop.
//
// Conservation Rule:
//   - Settlement sagas triggered by Physics instruments (KWH, GAS) cannot produce Physics instruments
//   - This prevents infinite causation loops (e.g., KWH settlement creating more KWH)
//   - Ingestion and valuation handlers are exempt from this rule
//   - Financial instruments (USD, NZD) can always be produced
//
// Parameters:
//   - ctx: The Starlark execution context (contains TriggerInstrument)
//   - metadata: Handler metadata (contains Category and ProducesInstruments)
//   - handlerName: The name of the handler being checked (for error messages)
//
// Returns:
//   - nil if the rule is satisfied or not applicable
//   - ErrConservationViolation if the rule is violated
func CheckConservationRule(ctx *StarlarkContext, metadata *HandlerMetadata, handlerName string) error {
	// Skip check if no metadata (backward compatibility)
	if metadata == nil {
		return nil
	}

	// Skip check if no trigger instrument (user-initiated sagas)
	if ctx.TriggerInstrument == "" {
		return nil
	}

	// Skip check if handler doesn't produce instruments
	if len(metadata.ProducesInstruments) == 0 {
		return nil
	}

	// Rule only applies to settlement handlers
	if metadata.Category != HandlerCategorySettlement {
		return nil
	}

	// Check if trigger is a Physics instrument
	triggerIsPhysics := IsPhysicsInstrument(ctx.TriggerInstrument)
	if !triggerIsPhysics {
		return nil
	}

	// Check if handler produces any Physics instruments
	for _, instrument := range metadata.ProducesInstruments {
		if IsPhysicsInstrument(instrument) {
			return fmt.Errorf("%w: handler=%s, trigger=%s, produces=%v",
				ErrConservationViolation, handlerName, ctx.TriggerInstrument, metadata.ProducesInstruments)
		}
	}

	return nil
}

// NewUUID generates a deterministic V5 UUID using the given namespace and name.
// This ensures idempotent saga replay - the same step will generate the same UUIDs.
func (c *StarlarkContext) NewUUID(namespace uuid.UUID, name string) uuid.UUID {
	return uuid.NewSHA1(namespace, []byte(name))
}

// NextIdempotencyKey generates the next idempotency key for this saga execution.
// The key format is: saga_{execution_id}_step_{step_index}
// This method is thread-safe and atomically increments the internal step counter.
// Each call generates a unique key for the current saga execution.
//
// This method is called internally before each handler invocation to ensure
// deterministic idempotency keys during saga replay.
func (c *StarlarkContext) NextIdempotencyKey() string {
	c.stepMutex.Lock()
	defer c.stepMutex.Unlock()

	c.stepCounter++
	return fmt.Sprintf("saga_%s_step_%d", c.SagaExecutionID.String(), c.stepCounter)
}

// ValidatePartyAccess checks if the given party ID is within the visible scope.
// Returns ErrPartyScopeViolation if the party is not accessible.
// If PartyScope is nil (party isolation disabled), all access is allowed.
//
// Step handlers should call this method when accessing party-specific data:
//
//	if err := ctx.ValidatePartyAccess(targetPartyID); err != nil {
//	    return nil, err
//	}
func (c *StarlarkContext) ValidatePartyAccess(partyID uuid.UUID) error {
	// If no party scope is configured, allow all access (backward compatibility)
	if c.PartyScope == nil {
		return nil
	}

	// Check if the party is in the visible scope
	if !c.PartyScope.Contains(partyID) {
		if c.Logger != nil {
			c.Logger.Warn("party scope violation",
				"saga_execution_id", c.SagaExecutionID,
				"executing_party_id", c.PartyScope.PartyID,
				"requested_party_id", partyID,
				"party_type", c.PartyScope.PartyType,
				"visible_parties_count", len(c.PartyScope.VisibleParties),
			)
		}
		return fmt.Errorf("%w: requested party %s, executing party %s (type: %s)",
			ErrPartyScopeViolation, partyID, c.PartyScope.PartyID, c.PartyScope.PartyType)
	}

	return nil
}

// ValidatePartyAccessFromString validates party access for a string party ID.
// Returns ErrPartyScopeViolation if the party is not accessible, or an error if the ID is invalid.
func (c *StarlarkContext) ValidatePartyAccessFromString(partyIDStr string) error {
	partyID, err := uuid.Parse(partyIDStr)
	if err != nil {
		return fmt.Errorf("%w: party_id must be a valid UUID, got %q", ErrInvalidParamType, partyIDStr)
	}
	return c.ValidatePartyAccess(partyID)
}

// EmitProgress logs progress information for monitoring/UI updates.
// Percentage must be between 0 and 100.
func (c *StarlarkContext) EmitProgress(stepName string, percentage int, message string) error {
	if percentage < 0 || percentage > 100 {
		return fmt.Errorf("%w: got %d", ErrInvalidPercentage, percentage)
	}

	c.Logger.Info("saga step progress",
		"saga_execution_id", c.SagaExecutionID,
		"step", stepName,
		"percentage", percentage,
		"message", message,
	)
	return nil
}

// Suspend marks the saga as suspended, waiting for external input.
// The saga will be resumed after the specified duration or when explicitly continued.
func (c *StarlarkContext) Suspend(reason string, resumeAfter time.Duration) error {
	c.suspended = true
	c.SuspendReason = reason
	c.ResumeAfter = resumeAfter

	c.Logger.Info("saga suspended",
		"saga_execution_id", c.SagaExecutionID,
		"reason", reason,
		"resume_after", resumeAfter,
	)
	return nil
}

// IsSuspended returns whether the context has been marked as suspended.
func (c *StarlarkContext) IsSuspended() bool {
	return c.suspended
}

// HandlerRegistry manages service bindings for Starlark step execution.
// Service packages register their handlers via RegisterStarlarkHandlers() functions.
// It is thread-safe for concurrent read/write access.
type HandlerRegistry struct {
	mu       sync.RWMutex
	handlers map[string]Handler
	metadata map[string]*HandlerMetadata
}

// NewHandlerRegistry creates a new empty handler registry.
func NewHandlerRegistry() *HandlerRegistry {
	return &HandlerRegistry{
		handlers: make(map[string]Handler),
		metadata: make(map[string]*HandlerMetadata),
	}
}

// Register adds a handler to the registry under the given name without metadata.
// For backward compatibility with existing handlers.
// Returns ErrInvalidHandlerName if name is empty, or ErrHandlerAlreadyRegistered if name exists.
func (r *HandlerRegistry) Register(name string, handler Handler) error {
	return r.RegisterWithMetadata(name, handler, nil)
}

// RegisterWithMetadata adds a handler to the registry with operational metadata.
// Metadata is used for conservation rule enforcement and handler categorization.
// Pass nil metadata for backward compatibility with handlers that don't need metadata.
// Returns ErrInvalidHandlerName if name is empty, or ErrHandlerAlreadyRegistered if name exists.
func (r *HandlerRegistry) RegisterWithMetadata(name string, handler Handler, metadata *HandlerMetadata) error {
	if name == "" {
		return ErrInvalidHandlerName
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.handlers[name]; exists {
		return fmt.Errorf("%w: %s", ErrHandlerAlreadyRegistered, name)
	}

	r.handlers[name] = handler
	r.metadata[name] = metadata
	return nil
}

// Get retrieves a handler by name without metadata.
// For backward compatibility with existing code.
// Returns ErrHandlerNotFound if the handler does not exist.
func (r *HandlerRegistry) Get(name string) (Handler, error) {
	h, _, err := r.GetWithMetadata(name)
	return h, err
}

// GetWithMetadata retrieves a handler and its metadata by name.
// Returns nil metadata if the handler was registered without metadata (backward compatibility).
// Returns ErrHandlerNotFound if the handler does not exist.
func (r *HandlerRegistry) GetWithMetadata(name string) (Handler, *HandlerMetadata, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	handler, exists := r.handlers[name]
	if !exists {
		return nil, nil, fmt.Errorf("%w: %s", ErrHandlerNotFound, name)
	}

	metadata := r.metadata[name] // May be nil for handlers registered without metadata

	return handler, metadata, nil
}

// Has returns true if a handler with the given name is registered.
func (r *HandlerRegistry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, exists := r.handlers[name]
	return exists
}

// AllWithMetadata returns a snapshot map of all registered handler names to their metadata.
// The returned metadata values are shallow copies; callers may read freely without affecting
// the registry. For deeply nested mutable fields (slices, maps), treat as read-only.
func (r *HandlerRegistry) AllWithMetadata() map[string]*HandlerMetadata {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]*HandlerMetadata, len(r.metadata))
	for name, meta := range r.metadata {
		if meta == nil {
			result[name] = nil
			continue
		}
		clone := *meta
		if meta.ProducesInstruments != nil {
			clone.ProducesInstruments = make([]string, len(meta.ProducesInstruments))
			copy(clone.ProducesInstruments, meta.ProducesInstruments)
		}
		if meta.ParamOverrides != nil {
			clone.ParamOverrides = make(map[string]ParamOverride, len(meta.ParamOverrides))
			for k, v := range meta.ParamOverrides {
				clone.ParamOverrides[k] = v
			}
		}
		if meta.Conversions != nil {
			clone.Conversions = make([]HandlerConversion, len(meta.Conversions))
			copy(clone.Conversions, meta.Conversions)
		}
		result[name] = &clone
	}
	return result
}

// List returns a sorted list of all registered handler names.
func (r *HandlerRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.handlers))
	for name := range r.handlers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
