// Package service implements gRPC services for the payment order domain
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	currentaccountclient "github.com/meridianhub/meridian/services/current-account/client"
	"github.com/meridianhub/meridian/services/payment-order/adapters/gateway"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	poobservability "github.com/meridianhub/meridian/services/payment-order/observability"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/platform/defaults"
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

// createPaymentOrderLienHandler creates a handler for the payment_order.create_lien step.
// This handler includes bucket evaluation for non-fungible instruments.
func createPaymentOrderLienHandler(deps *PaymentOrderHandlerDeps, logger *slog.Logger) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		const handlerName = "payment_order.create_lien"

		// Extract required parameters
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

		// Optional: instrument_code for bucket evaluation
		instrumentCode := getStringParamOrEmpty(params, "instrument_code")
		paymentAttributes := getMapParamOrEmpty(params, "payment_attributes")

		logger.Info("creating lien with bucket evaluation",
			"saga_execution_id", ctx.SagaExecutionID,
			"payment_order_id", paymentOrderID,
			"account_id", accountID,
			"amount_cents", amountCents,
			"currency", currency,
			"instrument_code", instrumentCode,
		)

		// Check required dependency
		if deps.CurrentAccountClient == nil {
			return nil, wrapHandlerError(handlerName, ErrCurrentAccountClientNotConfigured)
		}

		// Evaluate bucket ID for bucket-aware solvency validation
		bucketID, err := evaluateBucketIDForHandler(ctx.Context, deps, instrumentCode, paymentAttributes, paymentOrderID, logger)
		if err != nil {
			// Log but continue - graceful degradation
			logger.Warn("bucket evaluation failed, using default bucket",
				"payment_order_id", paymentOrderID,
				"instrument_code", instrumentCode,
				"error", err)
			bucketID = ""
		}

		// Build Money amount using domain types
		amount := mustNewMoney(currency, amountCents)

		// Build lien request
		lienRequest := &currentaccountv1.InitiateLienRequest{
			AccountId:             accountID,
			Amount:                toMoneyAmount(amount),
			PaymentOrderReference: paymentOrderID,
		}
		if bucketID != "" {
			lienRequest.BucketId = bucketID
			logger.Info("requesting bucket-scoped lien",
				"payment_order_id", paymentOrderID,
				"bucket_id", bucketID)
		}

		// Call current account service
		resp, err := deps.CurrentAccountClient.InitiateLien(ctx.Context, lienRequest)
		if err != nil {
			return nil, wrapHandlerError(handlerName, fmt.Errorf("failed to create lien: %w", err))
		}

		// Validate response
		if resp == nil || resp.Lien == nil || resp.Lien.LienId == "" {
			return nil, wrapHandlerError(handlerName, ErrMalformedLienResponse)
		}

		logger.Info("lien created successfully",
			"saga_execution_id", ctx.SagaExecutionID,
			"payment_order_id", paymentOrderID,
			"lien_id", resp.Lien.LienId,
			"bucket_id", bucketID,
		)

		result := map[string]any{
			"lien_id":   resp.Lien.LienId,
			"bucket_id": bucketID,
			"status":    "ACTIVE",
		}

		// Forward valuation_analysis if basis is present (atomic valuation audit trail)
		if basis := resp.GetBasis(); basis != nil {
			result["valuation_analysis"] = currentaccountclient.ConvertValuationAnalysisToMap(basis)
		}

		return result, nil
	}
}

// sendToGatewayHandler creates a handler for the payment_order.send_to_gateway step.
func sendToGatewayHandler(deps *PaymentOrderHandlerDeps, logger *slog.Logger) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		const handlerName = "payment_order.send_to_gateway"

		// Extract required parameters
		paymentOrderIDStr, err := requireStringParam(params, "payment_order_id")
		if err != nil {
			return nil, wrapHandlerError(handlerName, err)
		}

		paymentOrderID, err := uuid.Parse(paymentOrderIDStr)
		if err != nil {
			return nil, wrapHandlerError(handlerName, fmt.Errorf("invalid payment_order_id: %w", err))
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

		logger.Info("sending payment to gateway",
			"saga_execution_id", ctx.SagaExecutionID,
			"payment_order_id", paymentOrderIDStr,
		)

		// Check required dependency
		if deps.PaymentGateway == nil {
			return nil, wrapHandlerError(handlerName, ErrPaymentGatewayNotConfigured)
		}

		// Build gateway request
		resp, err := deps.PaymentGateway.SendPayment(ctx.Context, gateway.PaymentRequest{
			PaymentOrderID:    paymentOrderID,
			DebtorAccountID:   debtorAccountID,
			CreditorReference: creditorReference,
			Amount:            mustNewMoney(currency, amountCents),
			IdempotencyKey:    idempotencyKey,
		})
		if err != nil {
			return nil, wrapHandlerError(handlerName, fmt.Errorf("failed to send payment: %w", err))
		}

		// Process gateway response
		switch resp.Status {
		case gateway.StatusRejected:
			return nil, wrapHandlerError(handlerName, fmt.Errorf("%w: %s", ErrPaymentRejected, resp.Message))
		case gateway.StatusAccepted, gateway.StatusPending:
			// Success
		default:
			return nil, wrapHandlerError(handlerName, fmt.Errorf("%w: %s", ErrUnexpectedGatewayStatus, resp.Status))
		}

		logger.Info("payment sent to gateway successfully",
			"saga_execution_id", ctx.SagaExecutionID,
			"payment_order_id", paymentOrderIDStr,
			"gateway_reference_id", resp.GatewayReferenceID,
			"gateway_status", resp.Status,
		)

		return map[string]any{
			"gateway_reference_id": resp.GatewayReferenceID,
			"gateway_status":       string(resp.Status),
		}, nil
	}
}

// postLedgerEntriesHandler creates a handler for the payment_order.post_ledger_entries step.
// This handler delegates to the orchestrator's PostLedgerEntriesFromParams method.
func postLedgerEntriesHandler(deps *PaymentOrderHandlerDeps, logger *slog.Logger) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		const handlerName = "payment_order.post_ledger_entries"

		logger.Info("posting ledger entries",
			"saga_execution_id", ctx.SagaExecutionID,
		)

		// Check required dependency
		if deps.Orchestrator == nil {
			return nil, wrapHandlerError(handlerName, ErrOrchestratorNotConfigured)
		}

		// Delegate to the orchestrator's PostLedgerEntriesFromParams method
		// which handles the complex multi-gRPC-call ledger posting flow.
		bookingLogID, err := deps.Orchestrator.PostLedgerEntriesFromParams(ctx.Context, params)
		if err != nil {
			return nil, wrapHandlerError(handlerName, fmt.Errorf("failed to post ledger entries: %w", err))
		}

		logger.Info("ledger entries posted successfully",
			"saga_execution_id", ctx.SagaExecutionID,
			"booking_log_id", bookingLogID,
		)

		return map[string]any{
			"booking_log_id": bookingLogID,
			"status":         "POSTED",
		}, nil
	}
}

// executeLienHandler creates a handler for the payment_order.execute_lien step.
// This handler includes retry logic with exponential backoff.
// Records metrics for monitoring lien execution health and retry exhaustion.
func executeLienHandler(deps *PaymentOrderHandlerDeps, logger *slog.Logger) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		const handlerName = "payment_order.execute_lien"

		lienID, err := requireStringParam(params, "lien_id")
		if err != nil {
			return nil, wrapHandlerError(handlerName, err)
		}

		logger.Info("executing lien with retry",
			"saga_execution_id", ctx.SagaExecutionID,
			"lien_id", lienID,
		)

		// Check required dependency
		if deps.CurrentAccountClient == nil {
			return nil, wrapHandlerError(handlerName, ErrCurrentAccountClientNotConfigured)
		}

		// Use configured retry config or default
		retryConfig := deps.LienExecutionRetryConfig
		if retryConfig == nil {
			retryConfig = &sharedclients.RetryConfig{
				MaxRetries:          DefaultLienExecutionMaxRetries,
				InitialInterval:     500 * time.Millisecond,
				MaxInterval:         defaults.DefaultRPCTimeout,
				Multiplier:          2.0,
				RandomizationFactor: 0.5,
			}
		}

		var attempts int
		var lastErr error

		err = sharedclients.Retry(ctx.Context, *retryConfig, func() error {
			attempts++
			poobservability.RecordLienExecutionRetry(poobservability.LienRetryOutcomeAttempt)
			logger.Info("attempting lien execution", "attempt", attempts, "lien_id", lienID)

			_, execErr := deps.CurrentAccountClient.ExecuteLien(ctx.Context, &currentaccountv1.ExecuteLienRequest{
				LienId: lienID,
			})
			if execErr != nil {
				lastErr = execErr
				poobservability.RecordLienExecutionRetry(poobservability.LienRetryOutcomeFailed)
				logger.Warn("lien execution attempt failed",
					"attempt", attempts,
					"lien_id", lienID,
					"error", execErr)
				return execErr
			}
			return nil
		})

		executionStatus := map[string]any{
			"success":  err == nil,
			"attempts": attempts,
		}
		if err != nil {
			if lastErr != nil {
				executionStatus["error"] = lastErr.Error()
			} else {
				executionStatus["error"] = err.Error()
			}
		}

		if err != nil {
			// Record retry exhaustion metric - indicates payment may require manual reconciliation
			poobservability.RecordLienExecutionRetry(poobservability.LienRetryOutcomeExhausted)
			poobservability.RecordLienExecutionRetriesExhausted()
			logger.Error("lien execution failed after retries",
				"saga_execution_id", ctx.SagaExecutionID,
				"lien_id", lienID,
				"total_attempts", attempts,
				"error", err,
			)
			return map[string]any{
				"execution_status": executionStatus,
			}, wrapHandlerError(handlerName, fmt.Errorf("lien execution failed after %d attempts: %w", attempts, err))
		}

		poobservability.RecordLienExecutionRetry(poobservability.LienRetryOutcomeSuccess)
		logger.Info("lien executed successfully",
			"saga_execution_id", ctx.SagaExecutionID,
			"lien_id", lienID,
			"total_attempts", attempts,
		)

		return map[string]any{
			"execution_status": executionStatus,
		}, nil
	}
}

// terminateLienHandler creates a handler for the payment_order.terminate_lien step.
// This is used as compensation when the saga needs to rollback.
func terminateLienHandler(deps *PaymentOrderHandlerDeps, logger *slog.Logger) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		const handlerName = "payment_order.terminate_lien"

		lienID, err := requireStringParam(params, "lien_id")
		if err != nil {
			return nil, wrapHandlerError(handlerName, err)
		}

		reason := getStringParamOrDefault(params, "reason", "Saga compensation")

		logger.Info("terminating lien for compensation",
			"saga_execution_id", ctx.SagaExecutionID,
			"lien_id", lienID,
			"reason", reason,
		)

		// Check required dependency
		if deps.CurrentAccountClient == nil {
			return nil, wrapHandlerError(handlerName, ErrCurrentAccountClientNotConfigured)
		}

		_, err = deps.CurrentAccountClient.TerminateLien(ctx.Context, &currentaccountv1.TerminateLienRequest{
			LienId: lienID,
			Reason: reason,
		})
		if err != nil {
			return nil, wrapHandlerError(handlerName, fmt.Errorf("failed to terminate lien: %w", err))
		}

		logger.Info("lien terminated successfully",
			"saga_execution_id", ctx.SagaExecutionID,
			"lien_id", lienID,
		)

		return map[string]any{
			"lien_id": lienID,
			"status":  "TERMINATED",
		}, nil
	}
}

// Helper functions

// evaluateBucketIDForHandler evaluates the bucket ID for bucket-aware solvency.
// Returns empty string if bucket evaluation is not applicable or fails gracefully.
// Records metrics for monitoring bucket evaluation health.
func evaluateBucketIDForHandler(
	ctx context.Context,
	deps *PaymentOrderHandlerDeps,
	instrumentCode string,
	paymentAttributes map[string]string,
	paymentOrderID string,
	logger *slog.Logger,
) (string, error) {
	start := time.Now()

	// Skip if no instrument code
	if instrumentCode == "" {
		poobservability.RecordBucketEvaluation(poobservability.BucketEvalStatusSkipped)
		return "", nil
	}

	// Skip if reference data client not configured
	if deps.ReferenceDataClient == nil {
		logger.Debug("bucket evaluation skipped - reference data client not configured",
			"payment_order_id", paymentOrderID)
		poobservability.RecordBucketEvaluationFailure(poobservability.BucketEvalErrNoClient)
		poobservability.RecordBucketEvaluation(poobservability.BucketEvalStatusFallback)
		return "", nil
	}

	// Fetch instrument definition
	instrument, err := deps.ReferenceDataClient.RetrieveInstrument(ctx, instrumentCode)
	if err != nil {
		// Gracefully degrade if instrument not found or lookup fails
		logger.Debug("failed to retrieve instrument, using default bucket",
			"payment_order_id", paymentOrderID,
			"instrument_code", instrumentCode,
			"error", err)
		poobservability.RecordBucketEvaluationFailure(poobservability.BucketEvalErrInstrumentFetch)
		poobservability.RecordBucketEvaluation(poobservability.BucketEvalStatusFallback)
		return "", nil
	}

	// Check if instrument has fungibility expression
	if instrument.FungibilityKeyExpression == "" {
		logger.Debug("instrument has no fungibility expression, using default bucket",
			"payment_order_id", paymentOrderID,
			"instrument_code", instrumentCode)
		poobservability.RecordBucketEvaluation(poobservability.BucketEvalStatusSkipped)
		return "", nil
	}

	// Skip if bucket evaluator not configured
	if deps.BucketEvaluator == nil {
		logger.Debug("bucket evaluation skipped - bucket evaluator not configured",
			"payment_order_id", paymentOrderID)
		poobservability.RecordBucketEvaluationFailure(poobservability.BucketEvalErrNoEvaluator)
		poobservability.RecordBucketEvaluation(poobservability.BucketEvalStatusFallback)
		return "", nil
	}

	// Evaluate the bucket ID
	bucketID, err := deps.BucketEvaluator.Evaluate(ctx, instrument.FungibilityKeyExpression, BucketEvalContext{
		InstrumentCode: instrumentCode,
		Attributes:     paymentAttributes,
	})

	// Record duration for successful or failed evaluations (not skipped)
	poobservability.RecordBucketEvaluationDuration(time.Since(start))

	if err != nil {
		// Gracefully degrade to default bucket on CEL evaluation failures
		// (e.g., missing required attributes, invalid expressions)
		logger.Warn("bucket evaluation failed, using default bucket",
			"payment_order_id", paymentOrderID,
			"instrument_code", instrumentCode,
			"error", err)
		poobservability.RecordBucketEvaluationFailure(poobservability.BucketEvalErrCELEvaluation)
		poobservability.RecordBucketEvaluation(poobservability.BucketEvalStatusFallback)
		return "", nil
	}

	logger.Info("evaluated bucket ID",
		"payment_order_id", paymentOrderID,
		"instrument_code", instrumentCode,
		"bucket_id", bucketID)
	poobservability.RecordBucketEvaluation(poobservability.BucketEvalStatusSuccess)

	return bucketID, nil
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
