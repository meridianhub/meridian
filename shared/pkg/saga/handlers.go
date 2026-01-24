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
)

// StepHandler is the function signature for saga step handlers.
// Handlers receive context for cancellation/logging and params from the saga step.
// They return a result (any type) or an error.
type StepHandler func(ctx *StarlarkContext, params map[string]any) (result any, err error)

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

	// KnowledgeAt enables bi-temporal queries - what we knew at a specific point in time.
	KnowledgeAt time.Time

	// Logger for structured logging within handlers.
	Logger *slog.Logger

	// Suspension state
	suspended     bool
	SuspendReason string
	ResumeAfter   time.Duration
}

// PartyScope defines the data access boundary for a saga execution.
// Handlers must respect this scope when querying or modifying data.
type PartyScope struct {
	// PartyID is the owning party's identifier.
	PartyID uuid.UUID

	// TenantID is the tenant context (for multi-tenant deployments).
	TenantID uuid.UUID

	// Permissions lists the allowed operations for this scope.
	Permissions []string
}

// NewUUID generates a deterministic V5 UUID using the given namespace and name.
// This ensures idempotent saga replay - the same step will generate the same UUIDs.
func (c *StarlarkContext) NewUUID(namespace uuid.UUID, name string) uuid.UUID {
	return uuid.NewSHA1(namespace, []byte(name))
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

// StepHandlerRegistry manages the collection of registered step handlers.
// It is thread-safe for concurrent read/write access.
type StepHandlerRegistry struct {
	mu       sync.RWMutex
	handlers map[string]StepHandler
}

// NewStepHandlerRegistry creates a new empty handler registry.
func NewStepHandlerRegistry() *StepHandlerRegistry {
	return &StepHandlerRegistry{
		handlers: make(map[string]StepHandler),
	}
}

// Register adds a handler to the registry under the given name.
// Returns ErrInvalidHandlerName if name is empty, or ErrHandlerAlreadyRegistered if name exists.
func (r *StepHandlerRegistry) Register(name string, handler StepHandler) error {
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
func (r *StepHandlerRegistry) Get(name string) (StepHandler, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	handler, exists := r.handlers[name]
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrHandlerNotFound, name)
	}

	return handler, nil
}

// Has returns true if a handler with the given name is registered.
func (r *StepHandlerRegistry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, exists := r.handlers[name]
	return exists
}

// List returns a sorted list of all registered handler names.
func (r *StepHandlerRegistry) List() []string {
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
	defaultRegistry     *StepHandlerRegistry
	defaultRegistryOnce sync.Once
)

// DefaultRegistry returns the global registry with all default handlers registered.
// This is initialized on first call using sync.Once for thread-safe lazy initialization.
func DefaultRegistry() *StepHandlerRegistry {
	defaultRegistryOnce.Do(func() {
		defaultRegistry = NewStepHandlerRegistry()
		registerDefaultHandlers(defaultRegistry)
	})
	return defaultRegistry
}

// registerDefaultHandlers registers all platform-provided step handlers.
func registerDefaultHandlers(r *StepHandlerRegistry) {
	// Position Keeping handlers
	_ = r.Register("position_keeping.initiate_log", positionKeepingInitiateLog)
	_ = r.Register("position_keeping.update_log", positionKeepingUpdateLog)
	_ = r.Register("position_keeping.cancel_log", positionKeepingCancelLog)

	// Financial Accounting handlers
	_ = r.Register("financial_accounting.post_entries", financialAccountingPostEntries)
	_ = r.Register("financial_accounting.reverse_entries", financialAccountingReverseEntries)
	_ = r.Register("financial_accounting.create_booking", financialAccountingCreateBooking)

	// Current Account handlers
	_ = r.Register("current_account.create_lien", currentAccountCreateLien)
	_ = r.Register("current_account.execute_lien", currentAccountExecuteLien)
	_ = r.Register("current_account.terminate_lien", currentAccountTerminateLien)

	// Valuation Engine handler
	_ = r.Register("valuation_engine.valuate", valuationEngineValuate)

	// Repository handler
	_ = r.Register("repository.save", repositorySave)

	// Notification handler
	_ = r.Register("notification.send", notificationSend)
}

// Helper functions for parameter validation

func requireStringParam(params map[string]any, key string) (string, error) {
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

func requireDecimalParam(params map[string]any, key string) (decimal.Decimal, error) {
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

func wrapHandlerError(handlerName string, err error) error {
	return fmt.Errorf("%s: %w", handlerName, err)
}

// Position Keeping handlers

func positionKeepingInitiateLog(ctx *StarlarkContext, params map[string]any) (any, error) {
	const handlerName = "position_keeping.initiate_log"

	positionID, err := requireStringParam(params, "position_id")
	if err != nil {
		return nil, wrapHandlerError(handlerName, err)
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
	if direction != "DEBIT" && direction != "CREDIT" {
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

	ctx.Logger.Info("sending notification",
		"saga_execution_id", ctx.SagaExecutionID,
		"notification_type", notificationType,
		"recipient", recipient,
	)

	return map[string]any{
		"notification_id": ctx.NewUUID(ctx.SagaExecutionID, "notification_"+recipient).String(),
		"type":            notificationType,
		"recipient":       recipient,
		"status":          "SENT",
	}, nil
}
