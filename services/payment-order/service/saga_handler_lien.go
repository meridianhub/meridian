package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	currentaccountclient "github.com/meridianhub/meridian/services/current-account/client"
	poobservability "github.com/meridianhub/meridian/services/payment-order/observability"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/platform/defaults"
)

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
