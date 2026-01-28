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

	// Logger for structured logging within handlers.
	Logger *slog.Logger

	// LookupCache stores external lookup results for deterministic replay (FR-34).
	// When replaying a saga, this cache is pre-populated from the input snapshot
	// to ensure the same lookup results are returned even if Reference Data changed.
	LookupCache *LookupResultCache

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

// NewUUID generates a deterministic V5 UUID using the given namespace and name.
// This ensures idempotent saga replay - the same step will generate the same UUIDs.
func (c *StarlarkContext) NewUUID(namespace uuid.UUID, name string) uuid.UUID {
	return uuid.NewSHA1(namespace, []byte(name))
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
}

// NewHandlerRegistry creates a new empty handler registry.
func NewHandlerRegistry() *HandlerRegistry {
	return &HandlerRegistry{
		handlers: make(map[string]Handler),
	}
}

// Register adds a handler to the registry under the given name.
// Returns ErrInvalidHandlerName if name is empty, or ErrHandlerAlreadyRegistered if name exists.
func (r *HandlerRegistry) Register(name string, handler Handler) error {
	if name == "" {
		return ErrInvalidHandlerName
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.handlers[name]; exists {
		return fmt.Errorf("%w: %s", ErrHandlerAlreadyRegistered, name)
	}

	r.handlers[name] = handler
	return nil
}

// Get retrieves a handler by name.
// Returns ErrHandlerNotFound if the handler does not exist.
func (r *HandlerRegistry) Get(name string) (Handler, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	handler, exists := r.handlers[name]
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrHandlerNotFound, name)
	}

	return handler, nil
}

// Has returns true if a handler with the given name is registered.
func (r *HandlerRegistry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, exists := r.handlers[name]
	return exists
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

// defaultRegistry is the global registry with default handlers.
var (
	defaultRegistry     *HandlerRegistry
	defaultRegistryOnce sync.Once
)

// DefaultRegistry returns the global registry with all default handlers registered.
// This is initialized on first call using sync.Once for thread-safe lazy initialization.
func DefaultRegistry() *HandlerRegistry {
	defaultRegistryOnce.Do(func() {
		defaultRegistry = NewHandlerRegistry()
		registerDefaultHandlers(defaultRegistry)
	})
	return defaultRegistry
}

// registerDefaultHandlers registers all platform-provided step handlers.
func registerDefaultHandlers(r *HandlerRegistry) {
	// Position Keeping handlers
	_ = r.Register("position_keeping.initiate_log", positionKeepingInitiateLog)
	_ = r.Register("position_keeping.update_log", positionKeepingUpdateLog)
	_ = r.Register("position_keeping.cancel_log", positionKeepingCancelLog)

	// Financial Accounting handlers
	_ = r.Register("financial_accounting.post_entries", financialAccountingPostEntries)
	_ = r.Register("financial_accounting.reverse_entries", financialAccountingReverseEntries)
	_ = r.Register("financial_accounting.create_booking", financialAccountingCreateBooking)
	_ = r.Register("financial_accounting.initiate_booking_log", financialAccountingInitiateBookingLog)
	_ = r.Register("financial_accounting.capture_posting", financialAccountingCapturePosting)
	_ = r.Register("financial_accounting.compensate_posting", financialAccountingCompensatePosting)
	_ = r.Register("financial_accounting.update_booking_log", financialAccountingUpdateBookingLog)

	// Current Account handlers
	_ = r.Register("current_account.create_lien", currentAccountCreateLien)
	_ = r.Register("current_account.execute_lien", currentAccountExecuteLien)
	_ = r.Register("current_account.terminate_lien", currentAccountTerminateLien)
	_ = r.Register("current_account.save", currentAccountSave)

	// Valuation Engine handler
	_ = r.Register("valuation_engine.valuate", valuationEngineValuate)

	// Repository handler
	_ = r.Register("repository.save", repositorySave)

	// Notification handler
	_ = r.Register("notification.send", notificationSend)

	// Payment Order handlers
	_ = r.Register("payment_order.create_lien", paymentOrderCreateLien)
	_ = r.Register("payment_order.terminate_lien", paymentOrderTerminateLien)
	_ = r.Register("payment_order.send_to_gateway", paymentOrderSendToGateway)
	_ = r.Register("payment_order.post_ledger_entries", paymentOrderPostLedgerEntries)
	_ = r.Register("payment_order.execute_lien", paymentOrderExecuteLien)
}

// Helper functions for parameter validation (private wrappers for backward compatibility)

func requireStringParam(params map[string]any, key string) (string, error) {
	return RequireStringParam(params, key)
}

func requireDecimalParam(params map[string]any, key string) (decimal.Decimal, error) {
	return RequireDecimalParam(params, key)
}

func requireInt64Param(params map[string]any, key string) (int64, error) {
	val, ok := params[key]
	if !ok {
		return 0, fmt.Errorf("%w: %s", ErrMissingParam, key)
	}
	switch v := val.(type) {
	case int64:
		return v, nil
	case int:
		return int64(v), nil
	case float64:
		if v != float64(int64(v)) {
			return 0, fmt.Errorf("%w: %s must be a whole number, got %v", ErrInvalidParamType, key, v)
		}
		return int64(v), nil
	case string:
		d, err := decimal.NewFromString(v)
		if err != nil {
			return 0, fmt.Errorf("%w: %s must be int64, got invalid string", ErrInvalidParamType, key)
		}
		if !d.Equal(d.Truncate(0)) {
			return 0, fmt.Errorf("%w: %s must be a whole number, got %s", ErrInvalidParamType, key, v)
		}
		return d.IntPart(), nil
	default:
		return 0, fmt.Errorf("%w: %s must be int64, got %T", ErrInvalidParamType, key, val)
	}
}

func optionalStringParam(params map[string]any, key string, defaultVal string) string {
	val, ok := params[key]
	if !ok {
		return defaultVal
	}
	str, ok := val.(string)
	if !ok {
		return defaultVal
	}
	return str
}

func optionalBoolParam(params map[string]any, key string, defaultVal bool) bool {
	val, ok := params[key]
	if !ok {
		return defaultVal
	}
	b, ok := val.(bool)
	if !ok {
		return defaultVal
	}
	return b
}

func wrapHandlerError(handlerName string, err error) error {
	return fmt.Errorf("%s: %w", handlerName, err)
}

// Position Keeping handlers

func positionKeepingInitiateLog(ctx *StarlarkContext, params map[string]any) (any, error) {
	const handlerName = "position_keeping.initiate_log"

	// Accept either "position_id" or "account_id" as the position identifier.
	// The deposit/withdrawal .star scripts pass "account_id" while the canonical
	// parameter name is "position_id".
	positionID, err := requireStringParam(params, "position_id")
	if err != nil {
		// Fall back to account_id if position_id is missing
		positionID, err = requireStringParam(params, "account_id")
		if err != nil {
			return nil, wrapHandlerError(handlerName, fmt.Errorf("%w: position_id (or account_id)", ErrMissingParam))
		}
	}

	amount, err := requireDecimalParam(params, "amount")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	direction, err := requireStringParam(params, "direction")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	// Validate direction
	if direction != DirectionDebit && direction != DirectionCredit {
		return nil, wrapHandlerError(handlerName, fmt.Errorf("%w: got %s", ErrInvalidDirection, direction))
	}

	ctx.Logger.Info("position log initiated",
		"saga_execution_id", ctx.SagaExecutionID,
		"position_id", positionID,
		"amount", amount.String(),
		"direction", direction,
	)

	// Return a result indicating the log was created
	return map[string]any{
		"log_id":      ctx.NewUUID(ctx.SagaExecutionID, "position_log_"+positionID).String(),
		"position_id": positionID,
		"amount":      amount,
		"direction":   direction,
		"status":      "INITIATED",
	}, nil
}

func positionKeepingUpdateLog(ctx *StarlarkContext, params map[string]any) (any, error) {
	const handlerName = "position_keeping.update_log"

	logID, err := requireStringParam(params, "log_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	ctx.Logger.Info("position log updated",
		"saga_execution_id", ctx.SagaExecutionID,
		"log_id", logID,
	)

	return map[string]any{
		"log_id": logID,
		"status": "UPDATED",
	}, nil
}

func positionKeepingCancelLog(ctx *StarlarkContext, params map[string]any) (any, error) {
	const handlerName = "position_keeping.cancel_log"

	logID, err := requireStringParam(params, "log_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	ctx.Logger.Info("position log cancelled",
		"saga_execution_id", ctx.SagaExecutionID,
		"log_id", logID,
	)

	return map[string]any{
		"log_id": logID,
		"status": "CANCELLED",
	}, nil
}

// Financial Accounting handlers

func financialAccountingPostEntries(ctx *StarlarkContext, params map[string]any) (any, error) {
	const handlerName = "financial_accounting.post_entries"

	entries, ok := params["entries"]
	if !ok {
		return nil, wrapHandlerError(handlerName, fmt.Errorf("%w: entries", ErrMissingParam))
	}

	// Validate entries is a slice
	entriesSlice, ok := entries.([]any)
	if !ok {
		return nil, wrapHandlerError(handlerName, fmt.Errorf("%w: entries must be array, got %T", ErrInvalidParamType, entries))
	}

	ctx.Logger.Info("posting accounting entries",
		"saga_execution_id", ctx.SagaExecutionID,
		"entry_count", len(entriesSlice),
	)

	// Generate posting IDs for each entry
	postingIDs := make([]string, len(entriesSlice))
	for i := range entriesSlice {
		postingIDs[i] = ctx.NewUUID(ctx.SagaExecutionID, fmt.Sprintf("posting_%d", i)).String()
	}

	return map[string]any{
		"posting_ids": postingIDs,
		"status":      "POSTED",
	}, nil
}

func financialAccountingReverseEntries(ctx *StarlarkContext, params map[string]any) (any, error) {
	const handlerName = "financial_accounting.reverse_entries"

	postingIDs, ok := params["posting_ids"]
	if !ok {
		return nil, wrapHandlerError(handlerName, fmt.Errorf("%w: posting_ids", ErrMissingParam))
	}

	ctx.Logger.Info("reversing accounting entries",
		"saga_execution_id", ctx.SagaExecutionID,
		"posting_ids", postingIDs,
	)

	return map[string]any{
		"original_posting_ids": postingIDs,
		"status":               "REVERSED",
	}, nil
}

func financialAccountingCreateBooking(ctx *StarlarkContext, params map[string]any) (any, error) {
	const handlerName = "financial_accounting.create_booking"

	description, err := requireStringParam(params, "description")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	ctx.Logger.Info("creating booking",
		"saga_execution_id", ctx.SagaExecutionID,
		"description", description,
	)

	return map[string]any{
		"booking_id":  ctx.NewUUID(ctx.SagaExecutionID, "booking_"+description).String(),
		"description": description,
		"status":      "CREATED",
	}, nil
}

func financialAccountingInitiateBookingLog(ctx *StarlarkContext, params map[string]any) (any, error) {
	const handlerName = "financial_accounting.initiate_booking_log"

	accountID, err := requireStringParam(params, "account_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	currency, err := requireStringParam(params, "currency")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	transactionID, err := requireStringParam(params, "transaction_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	transactionType, err := requireStringParam(params, "transaction_type")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	ctx.Logger.Info("initiating booking log",
		"saga_execution_id", ctx.SagaExecutionID,
		"account_id", accountID,
		"currency", currency,
		"transaction_id", transactionID,
		"transaction_type", transactionType,
	)

	return map[string]any{
		"booking_log_id": ctx.NewUUID(ctx.SagaExecutionID, "booking_log_"+transactionID).String(),
		"status":         "INITIATED",
	}, nil
}

func financialAccountingCapturePosting(ctx *StarlarkContext, params map[string]any) (any, error) {
	const handlerName = "financial_accounting.capture_posting"

	bookingLogID, err := requireStringParam(params, "booking_log_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	accountID, err := requireStringParam(params, "account_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	amount, err := requireDecimalParam(params, "amount")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	currency, err := requireStringParam(params, "currency")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	direction, err := requireStringParam(params, "direction")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	if direction != DirectionDebit && direction != DirectionCredit {
		return nil, wrapHandlerError(handlerName, fmt.Errorf("%w: got %s", ErrInvalidDirection, direction))
	}

	transactionID, err := requireStringParam(params, "transaction_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	postingType, err := requireStringParam(params, "posting_type")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	ctx.Logger.Info("capturing posting",
		"saga_execution_id", ctx.SagaExecutionID,
		"booking_log_id", bookingLogID,
		"account_id", accountID,
		"amount", amount.String(),
		"currency", currency,
		"direction", direction,
		"transaction_id", transactionID,
		"posting_type", postingType,
	)

	return map[string]any{
		"posting_id": ctx.NewUUID(ctx.SagaExecutionID, "posting_"+transactionID+"_"+postingType).String(),
		"status":     "CAPTURED",
	}, nil
}

func financialAccountingCompensatePosting(ctx *StarlarkContext, params map[string]any) (any, error) {
	const handlerName = "financial_accounting.compensate_posting"

	bookingLogID, err := requireStringParam(params, "booking_log_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	accountID, err := requireStringParam(params, "account_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	amount, err := requireDecimalParam(params, "amount")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	currency, err := requireStringParam(params, "currency")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	direction, err := requireStringParam(params, "direction")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	if direction != DirectionDebit && direction != DirectionCredit {
		return nil, wrapHandlerError(handlerName, fmt.Errorf("%w: got %s", ErrInvalidDirection, direction))
	}

	transactionID, err := requireStringParam(params, "transaction_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	postingType, err := requireStringParam(params, "posting_type")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	ctx.Logger.Info("compensating posting",
		"saga_execution_id", ctx.SagaExecutionID,
		"booking_log_id", bookingLogID,
		"account_id", accountID,
		"amount", amount.String(),
		"currency", currency,
		"direction", direction,
		"transaction_id", transactionID,
		"posting_type", postingType,
	)

	return map[string]any{
		"posting_id": ctx.NewUUID(ctx.SagaExecutionID, "compensate_posting_"+transactionID+"_"+postingType).String(),
		"status":     "COMPENSATED",
	}, nil
}

func financialAccountingUpdateBookingLog(ctx *StarlarkContext, params map[string]any) (any, error) {
	const handlerName = "financial_accounting.update_booking_log"

	bookingLogID, err := requireStringParam(params, "booking_log_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	status, err := requireStringParam(params, "status")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	ctx.Logger.Info("updating booking log",
		"saga_execution_id", ctx.SagaExecutionID,
		"booking_log_id", bookingLogID,
		"status", status,
	)

	return map[string]any{
		"booking_log_id": bookingLogID,
		"status":         status,
	}, nil
}

// Current Account handlers

func currentAccountCreateLien(ctx *StarlarkContext, params map[string]any) (any, error) {
	const handlerName = "current_account.create_lien"

	accountID, err := requireStringParam(params, "account_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	amount, err := requireDecimalParam(params, "amount")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	ctx.Logger.Info("creating lien",
		"saga_execution_id", ctx.SagaExecutionID,
		"account_id", accountID,
		"amount", amount.String(),
	)

	return map[string]any{
		"lien_id":    ctx.NewUUID(ctx.SagaExecutionID, "lien_"+accountID).String(),
		"account_id": accountID,
		"amount":     amount,
		"status":     "ACTIVE",
	}, nil
}

func currentAccountExecuteLien(ctx *StarlarkContext, params map[string]any) (any, error) {
	const handlerName = "current_account.execute_lien"

	lienID, err := requireStringParam(params, "lien_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	ctx.Logger.Info("executing lien",
		"saga_execution_id", ctx.SagaExecutionID,
		"lien_id", lienID,
	)

	return map[string]any{
		"lien_id": lienID,
		"status":  "EXECUTED",
	}, nil
}

func currentAccountTerminateLien(ctx *StarlarkContext, params map[string]any) (any, error) {
	const handlerName = "current_account.terminate_lien"

	lienID, err := requireStringParam(params, "lien_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	ctx.Logger.Info("terminating lien",
		"saga_execution_id", ctx.SagaExecutionID,
		"lien_id", lienID,
	)

	return map[string]any{
		"lien_id": lienID,
		"status":  "TERMINATED",
	}, nil
}

func currentAccountSave(ctx *StarlarkContext, params map[string]any) (any, error) {
	const handlerName = "current_account.save"

	accountID, err := requireStringParam(params, "account_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	transactionID, err := requireStringParam(params, "transaction_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	ctx.Logger.Info("saving current account",
		"saga_execution_id", ctx.SagaExecutionID,
		"account_id", accountID,
		"transaction_id", transactionID,
	)

	return map[string]any{
		"account_id": accountID,
		"status":     "SAVED",
	}, nil
}

// Valuation Engine handler

func valuationEngineValuate(ctx *StarlarkContext, params map[string]any) (any, error) {
	const handlerName = "valuation_engine.valuate"

	instrument, err := requireStringParam(params, "instrument")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	quantity, err := requireDecimalParam(params, "quantity")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	contextType, err := requireStringParam(params, "context_type")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	ctx.Logger.Info("valuating instrument",
		"saga_execution_id", ctx.SagaExecutionID,
		"instrument", instrument,
		"quantity", quantity.String(),
		"context_type", contextType,
		"knowledge_at", ctx.KnowledgeAt,
	)

	// Mock valuation - in production this would call the valuation service
	// Using a representative value for demonstration
	unitPrice := decimal.NewFromFloat(42.50)
	totalValue := quantity.Mul(unitPrice)

	return map[string]any{
		"instrument":   instrument,
		"quantity":     quantity,
		"unit_price":   unitPrice,
		"value":        totalValue,
		"context_type": contextType,
		"currency":     "NZD",
		"valued_at":    ctx.KnowledgeAt,
	}, nil
}

// Repository handler

func repositorySave(ctx *StarlarkContext, params map[string]any) (any, error) {
	const handlerName = "repository.save"

	entityType, err := requireStringParam(params, "entity_type")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	entity, ok := params["entity"]
	if !ok {
		return nil, wrapHandlerError(handlerName, fmt.Errorf("%w: entity", ErrMissingParam))
	}

	ctx.Logger.Info("saving entity",
		"saga_execution_id", ctx.SagaExecutionID,
		"entity_type", entityType,
	)

	return map[string]any{
		"entity_id":   ctx.NewUUID(ctx.SagaExecutionID, "entity_"+entityType).String(),
		"entity_type": entityType,
		"entity":      entity,
		"status":      "SAVED",
	}, nil
}

// Notification handler

func notificationSend(ctx *StarlarkContext, params map[string]any) (any, error) {
	const handlerName = "notification.send"

	notificationType, err := requireStringParam(params, "type")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	recipient, err := requireStringParam(params, "recipient")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	notificationID := ctx.NewUUID(ctx.SagaExecutionID, "notification_"+recipient).String()

	ctx.Logger.Info("sending notification",
		"saga_execution_id", ctx.SagaExecutionID,
		"notification_type", notificationType,
		"notification_id", notificationID,
	)

	return map[string]any{
		"notification_id": notificationID,
		"type":            notificationType,
		"recipient":       recipient,
		"status":          "SENT",
	}, nil
}

// Payment Order handlers

func paymentOrderCreateLien(ctx *StarlarkContext, params map[string]any) (any, error) {
	const handlerName = "payment_order.create_lien"

	accountID, err := requireStringParam(params, "account_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	amountCents, err := requireInt64Param(params, "amount_cents")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	currency, err := requireStringParam(params, "currency")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	paymentOrderID, err := requireStringParam(params, "payment_order_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	instrumentCode := optionalStringParam(params, "instrument_code", "")
	_ = instrumentCode // Used for bucket-aware solvency evaluation

	// payment_attributes is optional and may be a map
	_ = params["payment_attributes"] // Used for CEL bucket expression evaluation

	ctx.Logger.Info("creating payment order lien",
		"saga_execution_id", ctx.SagaExecutionID,
		"account_id", accountID,
		"amount_cents", amountCents,
		"currency", currency,
		"payment_order_id", paymentOrderID,
	)

	return map[string]any{
		"lien_id":   ctx.NewUUID(ctx.SagaExecutionID, "po_lien_"+paymentOrderID).String(),
		"bucket_id": ctx.NewUUID(ctx.SagaExecutionID, "bucket_"+accountID).String(),
		"status":    "ACTIVE",
	}, nil
}

func paymentOrderTerminateLien(ctx *StarlarkContext, params map[string]any) (any, error) {
	const handlerName = "payment_order.terminate_lien"

	lienID, err := requireStringParam(params, "lien_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	reason := optionalStringParam(params, "reason", "")

	ctx.Logger.Info("terminating payment order lien",
		"saga_execution_id", ctx.SagaExecutionID,
		"lien_id", lienID,
		"reason", reason,
	)

	return map[string]any{
		"lien_id": lienID,
		"status":  "TERMINATED",
	}, nil
}

func paymentOrderSendToGateway(ctx *StarlarkContext, params map[string]any) (any, error) {
	const handlerName = "payment_order.send_to_gateway"

	paymentOrderID, err := requireStringParam(params, "payment_order_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	debtorAccountID, err := requireStringParam(params, "debtor_account_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	creditorReference, err := requireStringParam(params, "creditor_reference")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	amountCents, err := requireInt64Param(params, "amount_cents")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	currency, err := requireStringParam(params, "currency")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	idempotencyKey, err := requireStringParam(params, "idempotency_key")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	ctx.Logger.Info("sending payment to gateway",
		"saga_execution_id", ctx.SagaExecutionID,
		"payment_order_id", paymentOrderID,
		"debtor_account_id", debtorAccountID,
		"creditor_reference", creditorReference,
		"amount_cents", amountCents,
		"currency", currency,
		"idempotency_key", idempotencyKey,
	)

	return map[string]any{
		"gateway_reference_id": ctx.NewUUID(ctx.SagaExecutionID, "gw_ref_"+paymentOrderID).String(),
		"gateway_status":       "ACCEPTED",
	}, nil
}

func paymentOrderPostLedgerEntries(ctx *StarlarkContext, params map[string]any) (any, error) {
	const handlerName = "payment_order.post_ledger_entries"

	paymentOrderID, err := requireStringParam(params, "payment_order_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	debtorAccountID, err := requireStringParam(params, "debtor_account_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	gatewayReferenceID, err := requireStringParam(params, "gateway_reference_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	amountCents, err := requireInt64Param(params, "amount_cents")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	currency, err := requireStringParam(params, "currency")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	idempotencyKey, err := requireStringParam(params, "idempotency_key")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	internalClearingEnabled := optionalBoolParam(params, "internal_clearing_enabled", false)

	ctx.Logger.Info("posting ledger entries for payment order",
		"saga_execution_id", ctx.SagaExecutionID,
		"payment_order_id", paymentOrderID,
		"debtor_account_id", debtorAccountID,
		"gateway_reference_id", gatewayReferenceID,
		"amount_cents", amountCents,
		"currency", currency,
		"idempotency_key", idempotencyKey,
		"internal_clearing_enabled", internalClearingEnabled,
	)

	return map[string]any{
		"booking_log_id": ctx.NewUUID(ctx.SagaExecutionID, "po_booking_"+paymentOrderID).String(),
		"status":         "POSTED",
	}, nil
}

func paymentOrderExecuteLien(ctx *StarlarkContext, params map[string]any) (any, error) {
	const handlerName = "payment_order.execute_lien"

	lienID, err := requireStringParam(params, "lien_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
	}

	ctx.Logger.Info("executing payment order lien",
		"saga_execution_id", ctx.SagaExecutionID,
		"lien_id", lienID,
	)

	return map[string]any{
		"execution_status": "EXECUTED",
	}, nil
}
