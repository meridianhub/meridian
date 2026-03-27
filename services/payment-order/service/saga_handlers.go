// Package service implements gRPC services for the payment order domain
package service

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/meridianhub/meridian/services/payment-order/adapters/gateway"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/pkg/saga"
)

// Saga handler errors.
var (
	// ErrRegistryNil is returned when the handler registry is nil.
	ErrRegistryNil = errors.New("registry cannot be nil")

	// ErrDepsNil is returned when handler dependencies are nil.
	ErrDepsNil = errors.New("dependencies cannot be nil")

	// ErrCurrentAccountClientNotConfigured is returned when current account client is not set.
	ErrCurrentAccountClientNotConfigured = errors.New("current account client not configured")

	// ErrPaymentGatewayNotConfigured is returned when payment gateway is not set.
	ErrPaymentGatewayNotConfigured = errors.New("payment gateway not configured")

	// ErrOrchestratorNotConfigured is returned when orchestrator is not set.
	ErrOrchestratorNotConfigured = errors.New("orchestrator not configured - cannot post ledger entries")
)

// PaymentOrderHandlerDeps contains dependencies for Payment Order saga handlers.
// These are injected at service initialization time.
type PaymentOrderHandlerDeps struct {
	CurrentAccountClient      CurrentAccountClient
	PaymentGateway            gateway.PaymentGateway
	FinancialAccountingClient FinancialAccountingClient
	ReferenceDataClient       ReferenceDataClient
	BucketEvaluator           *BucketEvaluator
	LienExecutionRetryConfig  *sharedclients.RetryConfig
	Logger                    *slog.Logger

	// Orchestrator is needed for ledger posting which has complex internal logic.
	// This is optional - if nil, ledger posting handler will return an error.
	Orchestrator *PaymentOrchestrator
}

// RegisterPaymentOrderHandlers registers all Payment Order saga step handlers
// with the domain handler registry. These handlers call the actual gRPC clients
// and integrate with the bucket evaluation and retry logic.
func RegisterPaymentOrderHandlers(registry *saga.HandlerRegistry, deps *PaymentOrderHandlerDeps) error {
	if registry == nil {
		return ErrRegistryNil
	}
	if deps == nil {
		return ErrDepsNil
	}

	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// Handler: payment_order.create_lien
	// Creates a lien with bucket-aware solvency validation.
	// This is specific to Payment Order (different from the generic current_account.create_lien).
	if err := registry.Register("payment_order.create_lien", createPaymentOrderLienHandler(deps, logger)); err != nil {
		return fmt.Errorf("failed to register payment_order.create_lien handler: %w", err)
	}

	// Handler: payment_order.send_to_gateway
	// Sends payment to the external gateway and processes the response.
	if err := registry.Register("payment_order.send_to_gateway", sendToGatewayHandler(deps, logger)); err != nil {
		return fmt.Errorf("failed to register payment_order.send_to_gateway handler: %w", err)
	}

	// Handler: payment_order.post_ledger_entries
	// Creates double-entry bookkeeping entries (2 or 4 posting flow).
	if err := registry.Register("payment_order.post_ledger_entries", postLedgerEntriesHandler(deps, logger)); err != nil {
		return fmt.Errorf("failed to register payment_order.post_ledger_entries handler: %w", err)
	}

	// Handler: payment_order.execute_lien
	// Executes a lien with retry logic (converts reservation to actual debit).
	if err := registry.Register("payment_order.execute_lien", executeLienHandler(deps, logger)); err != nil {
		return fmt.Errorf("failed to register payment_order.execute_lien handler: %w", err)
	}

	// Compensating handlers for rollback

	// Handler: payment_order.terminate_lien
	// Releases a lien during saga compensation.
	if err := registry.Register("payment_order.terminate_lien", terminateLienHandler(deps, logger)); err != nil {
		return fmt.Errorf("failed to register payment_order.terminate_lien handler: %w", err)
	}

	return nil
}

// Parameter extraction helpers

func requireStringParam(params map[string]any, key string) (string, error) {
	val, ok := params[key]
	if !ok {
		return "", fmt.Errorf("%w: %s", saga.ErrMissingParam, key)
	}
	str, ok := val.(string)
	if !ok {
		return "", fmt.Errorf("%w: %s must be string, got %T", saga.ErrInvalidParamType, key, val)
	}
	return str, nil
}

func requireInt64Param(params map[string]any, key string) (int64, error) {
	val, ok := params[key]
	if !ok {
		return 0, fmt.Errorf("%w: %s", saga.ErrMissingParam, key)
	}
	switch v := val.(type) {
	case int64:
		return v, nil
	case int:
		return int64(v), nil
	case float64:
		return int64(v), nil
	default:
		return 0, fmt.Errorf("%w: %s must be numeric, got %T", saga.ErrInvalidParamType, key, val)
	}
}

func getStringParamOrEmpty(params map[string]any, key string) string {
	val, ok := params[key]
	if !ok {
		return ""
	}
	str, ok := val.(string)
	if !ok {
		return ""
	}
	return str
}

func getStringParamOrDefault(params map[string]any, key string, defaultVal string) string {
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

func getMapParamOrEmpty(params map[string]any, key string) map[string]string {
	val, ok := params[key]
	if !ok {
		return nil
	}

	// Handle map[string]any (common from JSON/Starlark)
	if m, ok := val.(map[string]any); ok {
		result := make(map[string]string, len(m))
		for k, v := range m {
			if str, ok := v.(string); ok {
				result[k] = str
			}
		}
		return result
	}

	// Handle map[string]string directly
	if m, ok := val.(map[string]string); ok {
		return m
	}

	return nil
}

func wrapHandlerError(handlerName string, err error) error {
	return fmt.Errorf("%s: %w", handlerName, err)
}

// mustNewMoney creates Money from currency and amount cents, returning zero on error.
// Used in saga handlers where currency is already validated.
func mustNewMoney(currency string, amountCents int64) domain.Money {
	m, err := domain.NewMoney(currency, amountCents)
	if err != nil {
		// Return zero money - error will be caught elsewhere
		return domain.Money{}
	}
	return m
}
