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

		lienParams, err := validateCreateLienParams(params)
		if err != nil {
			return nil, wrapHandlerError(handlerName, err)
		}

		logger.Info("creating lien with bucket evaluation",
			"saga_execution_id", ctx.SagaExecutionID,
			"payment_order_id", lienParams.paymentOrderID,
			"account_id", lienParams.accountID,
			"amount_cents", lienParams.amountCents,
			"currency", lienParams.currency,
			"instrument_code", lienParams.instrumentCode,
		)

		if deps.CurrentAccountClient == nil {
			return nil, wrapHandlerError(handlerName, ErrCurrentAccountClientNotConfigured)
		}

		// Evaluate bucket ID for bucket-aware solvency validation
		bucketID, err := evaluateBucketIDForHandler(ctx.Context, deps, lienParams.instrumentCode, lienParams.paymentAttributes, lienParams.paymentOrderID, logger)
		if err != nil {
			logger.Warn("bucket evaluation failed, using default bucket",
				"payment_order_id", lienParams.paymentOrderID,
				"instrument_code", lienParams.instrumentCode,
				"error", err)
			bucketID = ""
		}

		lienRequest := buildInitiateLienRequest(lienParams, bucketID, logger)

		resp, err := deps.CurrentAccountClient.InitiateLien(ctx.Context, lienRequest)
		if err != nil {
			return nil, wrapHandlerError(handlerName, fmt.Errorf("failed to create lien: %w", err))
		}

		if resp == nil || resp.Lien == nil || resp.Lien.LienId == "" {
			return nil, wrapHandlerError(handlerName, ErrMalformedLienResponse)
		}

		logger.Info("lien created successfully",
			"saga_execution_id", ctx.SagaExecutionID,
			"payment_order_id", lienParams.paymentOrderID,
			"lien_id", resp.Lien.LienId,
			"bucket_id", bucketID,
		)

		return buildCreateLienResult(resp, bucketID), nil
	}
}

// createLienParams holds validated parameters for the create_lien handler.
type createLienParams struct {
	accountID         string
	amountCents       int64
	currency          string
	paymentOrderID    string
	instrumentCode    string
	paymentAttributes map[string]string
}

// validateCreateLienParams extracts and validates required parameters for lien creation.
func validateCreateLienParams(params map[string]any) (createLienParams, error) {
	accountID, err := requireStringParam(params, "account_id")
	if err != nil {
		return createLienParams{}, err
	}
	amountCents, err := requireInt64Param(params, "amount_cents")
	if err != nil {
		return createLienParams{}, err
	}
	currency, err := requireStringParam(params, "currency")
	if err != nil {
		return createLienParams{}, err
	}
	paymentOrderID, err := requireStringParam(params, "payment_order_id")
	if err != nil {
		return createLienParams{}, err
	}
	return createLienParams{
		accountID:         accountID,
		amountCents:       amountCents,
		currency:          currency,
		paymentOrderID:    paymentOrderID,
		instrumentCode:    getStringParamOrEmpty(params, "instrument_code"),
		paymentAttributes: getMapParamOrEmpty(params, "payment_attributes"),
	}, nil
}

// buildInitiateLienRequest constructs the InitiateLienRequest proto from validated parameters.
func buildInitiateLienRequest(p createLienParams, bucketID string, logger *slog.Logger) *currentaccountv1.InitiateLienRequest {
	amount := mustNewMoney(p.currency, p.amountCents)
	req := &currentaccountv1.InitiateLienRequest{
		AccountId:             p.accountID,
		Amount:                toMoneyAmount(amount),
		PaymentOrderReference: p.paymentOrderID,
	}
	if bucketID != "" {
		req.BucketId = bucketID
		logger.Info("requesting bucket-scoped lien",
			"payment_order_id", p.paymentOrderID,
			"bucket_id", bucketID)
	}
	return req
}

// buildCreateLienResult constructs the handler result map from a successful lien response.
func buildCreateLienResult(resp *currentaccountv1.InitiateLienResponse, bucketID string) map[string]any {
	result := map[string]any{
		"lien_id":   resp.Lien.LienId,
		"bucket_id": bucketID,
		"status":    "ACTIVE",
	}
	if basis := resp.GetBasis(); basis != nil {
		result["valuation_analysis"] = currentaccountclient.ConvertValuationAnalysisToMap(basis)
	}
	return result
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

		if deps.CurrentAccountClient == nil {
			return nil, wrapHandlerError(handlerName, ErrCurrentAccountClientNotConfigured)
		}

		retryConfig := buildLienRetryConfig(deps.LienExecutionRetryConfig)

		attempts, err := executeLienWithRetries(ctx, deps, retryConfig, lienID, logger)
		if err != nil {
			poobservability.RecordLienExecutionRetry(poobservability.LienRetryOutcomeExhausted)
			poobservability.RecordLienExecutionRetriesExhausted()
			logger.Error("lien execution failed after retries",
				"saga_execution_id", ctx.SagaExecutionID,
				"lien_id", lienID,
				"total_attempts", attempts,
				"error", err,
			)
			return nil, wrapHandlerError(handlerName, fmt.Errorf("lien execution failed after %d attempts: %w", attempts, err))
		}

		poobservability.RecordLienExecutionRetry(poobservability.LienRetryOutcomeSuccess)
		logger.Info("lien executed successfully",
			"saga_execution_id", ctx.SagaExecutionID,
			"lien_id", lienID,
			"total_attempts", attempts,
		)

		return map[string]any{
			"execution_status": map[string]any{
				"success":  true,
				"attempts": attempts,
			},
		}, nil
	}
}

// buildLienRetryConfig returns the provided config or a sensible default.
func buildLienRetryConfig(configured *sharedclients.RetryConfig) sharedclients.RetryConfig {
	if configured != nil {
		return *configured
	}
	return sharedclients.RetryConfig{
		MaxRetries:          DefaultLienExecutionMaxRetries,
		InitialInterval:     500 * time.Millisecond,
		MaxInterval:         defaults.DefaultRPCTimeout,
		Multiplier:          2.0,
		RandomizationFactor: 0.5,
	}
}

// executeLienWithRetries performs the lien execution with retry logic and metrics recording.
func executeLienWithRetries(ctx *saga.StarlarkContext, deps *PaymentOrderHandlerDeps, retryConfig sharedclients.RetryConfig, lienID string, logger *slog.Logger) (int, error) {
	var attempts int

	err := sharedclients.Retry(ctx.Context, retryConfig, func() error {
		attempts++
		poobservability.RecordLienExecutionRetry(poobservability.LienRetryOutcomeAttempt)
		logger.Info("attempting lien execution", "attempt", attempts, "lien_id", lienID)

		_, execErr := deps.CurrentAccountClient.ExecuteLien(ctx.Context, &currentaccountv1.ExecuteLienRequest{
			LienId: lienID,
		})
		if execErr != nil {
			poobservability.RecordLienExecutionRetry(poobservability.LienRetryOutcomeFailed)
			logger.Warn("lien execution attempt failed",
				"attempt", attempts,
				"lien_id", lienID,
				"error", execErr)
			return execErr
		}
		return nil
	})

	return attempts, err
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

	if instrumentCode == "" {
		poobservability.RecordBucketEvaluation(poobservability.BucketEvalStatusSkipped)
		return "", nil
	}

	expression, ok := fetchFungibilityExpression(ctx, deps, instrumentCode, paymentOrderID, logger)
	if !ok {
		return "", nil
	}

	if deps.BucketEvaluator == nil {
		logger.Debug("bucket evaluation skipped - bucket evaluator not configured",
			"payment_order_id", paymentOrderID)
		poobservability.RecordBucketEvaluationFailure(poobservability.BucketEvalErrNoEvaluator)
		poobservability.RecordBucketEvaluation(poobservability.BucketEvalStatusFallback)
		return "", nil
	}

	bucketID, err := deps.BucketEvaluator.Evaluate(ctx, expression, BucketEvalContext{
		InstrumentCode: instrumentCode,
		Attributes:     paymentAttributes,
	})

	poobservability.RecordBucketEvaluationDuration(time.Since(start))

	if err != nil {
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

// fetchFungibilityExpression retrieves the CEL expression for bucket evaluation from the instrument definition.
// Returns the expression and true if evaluation should proceed, or empty string and false if it should be skipped.
func fetchFungibilityExpression(ctx context.Context, deps *PaymentOrderHandlerDeps, instrumentCode, paymentOrderID string, logger *slog.Logger) (string, bool) {
	if deps.ReferenceDataClient == nil {
		logger.Debug("bucket evaluation skipped - reference data client not configured",
			"payment_order_id", paymentOrderID)
		poobservability.RecordBucketEvaluationFailure(poobservability.BucketEvalErrNoClient)
		poobservability.RecordBucketEvaluation(poobservability.BucketEvalStatusFallback)
		return "", false
	}

	instrument, err := deps.ReferenceDataClient.RetrieveInstrument(ctx, instrumentCode)
	if err != nil {
		logger.Debug("failed to retrieve instrument, using default bucket",
			"payment_order_id", paymentOrderID,
			"instrument_code", instrumentCode,
			"error", err)
		poobservability.RecordBucketEvaluationFailure(poobservability.BucketEvalErrInstrumentFetch)
		poobservability.RecordBucketEvaluation(poobservability.BucketEvalStatusFallback)
		return "", false
	}

	if instrument.FungibilityKeyExpression == "" {
		logger.Debug("instrument has no fungibility expression, using default bucket",
			"payment_order_id", paymentOrderID,
			"instrument_code", instrumentCode)
		poobservability.RecordBucketEvaluation(poobservability.BucketEvalStatusSkipped)
		return "", false
	}

	return instrument.FungibilityKeyExpression, true
}
