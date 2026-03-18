package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Saga orchestration errors.
var (
	// ErrSagaOrchestrationDisabled is returned when ExecutePaymentSaga is called
	// but USE_SAGA_ORCHESTRATION is not enabled.
	ErrSagaOrchestrationDisabled = errors.New("saga orchestration is not enabled")

	// ErrSagaDepsNotConfigured is returned when required saga dependencies
	// (CurrentAccountClient, PaymentGateway) are nil at execution time.
	ErrSagaDepsNotConfigured = errors.New("saga dependencies not configured")

	// ErrRefDataClientNotConfigured is returned when the reference data client
	// is nil and a saga definition cannot be fetched.
	ErrRefDataClientNotConfigured = errors.New("reference data client not configured")
)

// Orchestrate executes the payment saga using Starlark script execution.
// The saga script is fetched from reference-data service and executed via StarlarkSagaRunner.
// Compensation is handled automatically by the Starlark runtime on failure.
//
// When sagaOrchestrationEnabled is false, this method logs a warning and marks the
// payment order as failed because the Go-based orchestration was removed in favor of
// Starlark. Enable USE_SAGA_ORCHESTRATION=true to use Starlark saga execution.
func (o *PaymentOrchestrator) Orchestrate(ctx context.Context, po *domain.PaymentOrder) {
	if !o.sagaOrchestrationEnabled {
		o.logger.Warn("saga orchestration disabled, payment order will not be processed",
			"payment_order_id", po.ID.String(),
			"hint", "set USE_SAGA_ORCHESTRATION=true to enable Starlark saga execution")
		if err := o.failPaymentOrder(ctx, po, "saga orchestration is disabled (USE_SAGA_ORCHESTRATION=false)", "SAGA_DISABLED"); err != nil {
			o.logger.Error("failed to mark payment order as failed",
				"payment_order_id", po.ID.String(),
				"error", err)
		}
		return
	}

	output, err := o.ExecutePaymentSaga(ctx, po.ID, "payment_execution", po)
	if err != nil {
		o.logger.Error("ExecutePaymentSaga returned error",
			"payment_order_id", po.ID.String(),
			"error", err)
		return
	}

	o.logger.Info("Orchestrate completed via ExecutePaymentSaga",
		"payment_order_id", po.ID.String(),
		"success", output.Success)
}

// ExecutePaymentSaga loads a saga definition, executes it via StarlarkSagaRunner,
// persists the execution record, and handles the result. This is the primary
// integration point between the payment orchestrator and the saga engine.
//
// Flow: load saga -> build input -> execute via sagaRunner.Run() -> persist execution -> return output
func (o *PaymentOrchestrator) ExecutePaymentSaga(ctx context.Context, paymentOrderID uuid.UUID, sagaName string, po *domain.PaymentOrder) (*saga.RunnerOutput, error) {
	if !o.sagaOrchestrationEnabled {
		return nil, ErrSagaOrchestrationDisabled
	}

	startTime := time.Now()
	executionID := uuid.New()

	o.logger.Info("starting payment saga execution",
		"payment_order_id", paymentOrderID.String(),
		"saga_name", sagaName,
		"execution_id", executionID.String(),
		"correlation_id", po.CorrelationID)

	// Check if all dependencies are available
	if o.currentAccountClient == nil || o.paymentGateway == nil {
		o.logger.Error("saga dependencies not configured",
			"payment_order_id", po.ID.String())
		if err := o.failPaymentOrder(ctx, po, "service configuration error", "INTERNAL_ERROR"); err != nil {
			o.logger.Error("failed to mark payment order as failed",
				"payment_order_id", po.ID.String(),
				"error", err)
		}
		return nil, ErrSagaDepsNotConfigured
	}

	// Check if reference data client is available for GetSaga
	if o.referenceDataClient == nil {
		o.logger.Error("reference data client not configured - cannot fetch saga script",
			"payment_order_id", po.ID.String())
		if err := o.failPaymentOrder(ctx, po, "reference data client not configured", "INTERNAL_ERROR"); err != nil {
			o.logger.Error("failed to mark payment order as failed",
				"payment_order_id", po.ID.String(),
				"error", err)
		}
		return nil, ErrRefDataClientNotConfigured
	}

	// Fetch saga script from reference-data service
	sagaDef, err := o.referenceDataClient.GetSaga(ctx, sagaName, 0) // 0 = fetch ACTIVE version
	if err != nil {
		o.logger.Error("failed to fetch saga definition from reference-data",
			"payment_order_id", po.ID.String(),
			"saga_name", sagaName,
			"error", err)
		if err := o.failPaymentOrder(ctx, po, fmt.Sprintf("failed to fetch saga: %v", err), "INTERNAL_ERROR"); err != nil {
			o.logger.Error("failed to mark payment order as failed",
				"payment_order_id", po.ID.String(),
				"error", err)
		}
		return nil, fmt.Errorf("failed to fetch saga definition: %w", err)
	}

	o.logger.Info("fetched saga definition from reference-data",
		"saga_name", sagaDef.Name,
		"saga_version", sagaDef.Version,
		"saga_status", sagaDef.Status)

	// Parse correlation ID - if invalid, generate a new one and log warning
	correlationID, err := uuid.Parse(po.CorrelationID)
	if err != nil {
		o.logger.Warn("invalid correlation_id, generating new one",
			"payment_order_id", po.ID.String(),
			"invalid_correlation_id", po.CorrelationID,
			"error", err)
		correlationID = uuid.New()
	}

	// Build saga input from payment order
	sagaInput := map[string]interface{}{
		"payment_order_id":   po.ID.String(),
		"debtor_account_id":  po.DebtorAccountID,
		"creditor_reference": po.CreditorReference,
		"amount_cents":       domain.ToMinorUnits(po.Amount),
		"currency":           domain.CurrencyCode(po.Amount),
		"idempotency_key":    po.IdempotencyKey,
		"instrument_code":    po.InstrumentCode,
		"payment_attributes": po.PaymentAttributes,
	}

	runnerInput := saga.RunnerInput{
		SagaExecutionID: executionID,
		CorrelationID:   correlationID,
		Input:           sagaInput,
	}

	// Persist initial execution record (RUNNING)
	o.logSagaExecution(ctx, &domain.SagaExecution{
		ID:             executionID,
		PaymentOrderID: paymentOrderID,
		SagaName:       sagaName,
		SagaVersion:    sagaDef.Version,
		Status:         domain.SagaExecutionStatusRunning,
		CorrelationID:  correlationID.String(),
		Input:          sagaInput,
		StartedAt:      startTime,
	})

	// Execute saga via StarlarkSagaRunner
	result, err := o.starlarkRunner.ExecuteSaga(ctx, sagaName, sagaDef.Script, runnerInput)
	durationMs := time.Since(startTime).Milliseconds()

	if err != nil {
		o.logger.Error("starlark saga runner returned error",
			"payment_order_id", po.ID.String(),
			"execution_id", executionID.String(),
			"error", err,
			"duration_ms", durationMs)

		// Detach from the saga context (may be cancelled due to timeout) so
		// failure recording and compensation complete successfully.
		failCtx, failCancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer failCancel()

		now := time.Now()
		o.logSagaExecution(failCtx, &domain.SagaExecution{
			ID:             executionID,
			PaymentOrderID: paymentOrderID,
			SagaName:       sagaName,
			SagaVersion:    sagaDef.Version,
			Status:         domain.SagaExecutionStatusFailed,
			CorrelationID:  correlationID.String(),
			Input:          sagaInput,
			ErrorMessage:   err.Error(),
			DurationMs:     durationMs,
			StartedAt:      startTime,
			CompletedAt:    &now,
		})

		if err := o.failPaymentOrder(failCtx, po, fmt.Sprintf("saga execution error: %v", err), "SAGA_FAILED"); err != nil {
			o.logger.Error("failed to mark payment order as failed",
				"payment_order_id", po.ID.String(),
				"error", err)
		}
		return nil, fmt.Errorf("saga execution failed: %w", err)
	}

	// Persist completed execution record
	now := time.Now()
	execStatus := domain.SagaExecutionStatusCompleted
	errorMsg := ""
	if !result.Success {
		execStatus = domain.SagaExecutionStatusFailed
		errorMsg = result.Error
	}
	o.logSagaExecution(ctx, &domain.SagaExecution{
		ID:             executionID,
		PaymentOrderID: paymentOrderID,
		SagaName:       sagaName,
		SagaVersion:    sagaDef.Version,
		Status:         execStatus,
		CorrelationID:  correlationID.String(),
		Input:          sagaInput,
		Output:         result.Output,
		ErrorMessage:   errorMsg,
		StepCount:      len(result.StepResults),
		DurationMs:     durationMs,
		StartedAt:      startTime,
		CompletedAt:    &now,
	})

	// Handle saga result (state transitions, events)
	o.handleStarlarkSagaResult(ctx, po, result)

	return result, nil
}

// logSagaExecution persists a saga execution record if the logger is configured.
func (o *PaymentOrchestrator) logSagaExecution(ctx context.Context, execution *domain.SagaExecution) {
	if o.sagaExecutionLogger == nil {
		return
	}
	if err := o.sagaExecutionLogger.PersistExecution(ctx, execution); err != nil {
		o.logger.Error("failed to persist saga execution record",
			"execution_id", execution.ID.String(),
			"payment_order_id", execution.PaymentOrderID.String(),
			"status", execution.Status,
			"error", err)
	}
}

// handleStarlarkSagaResult processes the result from StarlarkSagaRunner execution.
// On failure, it logs failed steps and marks the payment order as failed.
// On success, it extracts outputs (lien_id, gateway_reference_id) and logs completion.
func (o *PaymentOrchestrator) handleStarlarkSagaResult(ctx context.Context, po *domain.PaymentOrder, result *saga.RunnerOutput) {
	if !result.Success {
		o.handleSagaFailure(ctx, po, result)
		return
	}

	o.handleSagaSuccess(ctx, po, result)
}

// handleSagaFailure processes a failed saga result, extracting partial outputs and marking
// the payment order as failed.
func (o *PaymentOrchestrator) handleSagaFailure(ctx context.Context, po *domain.PaymentOrder, result *saga.RunnerOutput) {
	// Detach from the caller's context so failure handling succeeds even when
	// the saga context has been cancelled (e.g. timeout). The detached context
	// preserves values (tenant, correlation ID) but removes the deadline.
	// Add a fresh timeout to prevent indefinite hangs.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()

	o.logger.Error("payment saga failed in Starlark execution",
		"payment_order_id", po.ID.String(),
		"error", result.Error,
		"step_count", len(result.StepResults))

	// Log failed steps for debugging
	for _, step := range result.StepResults {
		if !step.Success {
			o.logger.Error("saga step failed",
				"payment_order_id", po.ID.String(),
				"step_name", step.StepName,
				"error", step.Error,
				"duration", step.Duration)
		}
	}

	// Extract partial outputs from successful steps before failure
	// This is important for tracking which resources were created (e.g., lien_id)
	// so they can be properly cleaned up during compensation
	//
	// When a saga fails, result.Output is nil because the script never returns.
	// But successful steps have their outputs in result.StepResults.
	// We reconstruct relevant outputs from completed steps.
	lienID := ""
	for _, step := range result.StepResults {
		if !step.Success {
			continue
		}
		// Check if this step returned a lien_id
		if stepOutput, ok := step.Output.(map[string]any); ok {
			if lienIDVal, ok := stepOutput["lien_id"].(string); ok && lienIDVal != "" {
				lienID = lienIDVal
				break
			}
		}
	}

	// Reload payment order to get latest state
	latestPO, err := o.repo.FindByID(ctx, po.ID)
	if err != nil {
		o.logger.Error("failed to reload payment order for failure handling", "error", err)
		return
	}

	// If lien was created before failure, transition to RESERVED state first
	// This ensures the lien_id is persisted for cleanup
	if lienID != "" && latestPO.Status == domain.PaymentOrderStatusInitiated {
		if err := latestPO.Reserve(lienID); err != nil {
			o.logger.Warn("failed to transition to RESERVED during failure handling",
				"payment_order_id", latestPO.ID.String(),
				"lien_id", lienID,
				"error", err)
		} else {
			if err := o.repo.Update(ctx, latestPO); err != nil {
				o.logger.Error("failed to persist RESERVED state during failure handling",
					"payment_order_id", latestPO.ID.String(),
					"error", err)
			} else {
				o.logger.Info("persisted lien_id before marking payment as failed",
					"payment_order_id", latestPO.ID.String(),
					"lien_id", lienID)
			}
		}
	}

	// Mark as failed
	if err := o.failPaymentOrder(ctx, latestPO, result.Error, "SAGA_FAILED"); err != nil {
		o.logger.Error("failed to mark payment order as failed after saga failure",
			"payment_order_id", po.ID.String(),
			"error", err)
	}
}

// handleSagaSuccess processes a successful saga result, applying state transitions and
// publishing domain events.
func (o *PaymentOrchestrator) handleSagaSuccess(ctx context.Context, po *domain.PaymentOrder, result *saga.RunnerOutput) {
	// Extract outputs from saga execution
	lienID := ""
	bucketID := ""
	gatewayReferenceID := ""

	if lienIDVal, ok := result.Output["lien_id"].(string); ok {
		lienID = lienIDVal
	}
	if bucketIDVal, ok := result.Output["bucket_id"].(string); ok {
		bucketID = bucketIDVal
	}
	if gatewayRefVal, ok := result.Output["gateway_reference_id"].(string); ok {
		gatewayReferenceID = gatewayRefVal
	}

	// Reload payment order to get latest state
	latestPO, err := o.repo.FindByID(ctx, po.ID)
	if err != nil {
		o.logger.Error("failed to reload payment order after saga success", "error", err)
		return
	}

	// Validate required outputs based on successful steps
	if !o.validateSagaOutputs(ctx, latestPO, result, lienID, gatewayReferenceID) {
		return
	}

	// Apply state transitions based on what the saga accomplished
	// The handlers call external services but don't update PaymentOrder state
	// This orchestrator method applies domain state transitions and publishes events
	o.applyReservedTransition(ctx, latestPO, lienID, bucketID)
	o.applyExecutingTransition(ctx, latestPO, gatewayReferenceID)

	o.logger.Info("payment saga completed successfully via Starlark",
		"payment_order_id", latestPO.ID.String(),
		"lien_id", lienID,
		"gateway_reference_id", gatewayReferenceID,
		"step_count", len(result.StepResults),
		"final_status", latestPO.Status)
}

// validateSagaOutputs checks that required outputs are present after successful handler execution.
// Returns false if validation fails and the payment order has been marked as failed.
func (o *PaymentOrchestrator) validateSagaOutputs(
	ctx context.Context,
	po *domain.PaymentOrder,
	result *saga.RunnerOutput,
	lienID string,
	gatewayReferenceID string,
) bool {
	// Note: StepName contains the handler name (e.g., "payment_order.create_lien"),
	// not the step() name from the Starlark script (e.g., "reserve_funds")
	createLienSucceeded := false
	sendToGatewaySucceeded := false
	for _, step := range result.StepResults {
		if step.StepName == "payment_order.create_lien" && step.Success {
			createLienSucceeded = true
		}
		if step.StepName == "payment_order.send_to_gateway" && step.Success {
			sendToGatewaySucceeded = true
		}
	}

	if createLienSucceeded && lienID == "" {
		o.logger.Error("saga output missing lien_id after successful create_lien handler",
			"payment_order_id", po.ID.String(),
			"output", result.Output)
		if err := o.failPaymentOrder(ctx, po, "saga output missing lien_id", "SAGA_OUTPUT_INVALID"); err != nil {
			o.logger.Error("failed to mark payment order as failed after missing lien_id",
				"payment_order_id", po.ID.String(),
				"error", err)
		}
		return false
	}

	if sendToGatewaySucceeded && gatewayReferenceID == "" {
		o.logger.Error("saga output missing gateway_reference_id after successful send_to_gateway handler",
			"payment_order_id", po.ID.String(),
			"output", result.Output)
		if err := o.failPaymentOrder(ctx, po, "saga output missing gateway_reference_id", "SAGA_OUTPUT_INVALID"); err != nil {
			o.logger.Error("failed to mark payment order as failed after missing gateway_reference_id",
				"payment_order_id", po.ID.String(),
				"error", err)
		}
		return false
	}

	return true
}

// applyReservedTransition transitions the payment order to RESERVED state if a lien was created.
func (o *PaymentOrchestrator) applyReservedTransition(ctx context.Context, po *domain.PaymentOrder, lienID string, bucketID string) {
	if lienID == "" || po.Status != domain.PaymentOrderStatusInitiated {
		return
	}

	if err := po.Reserve(lienID); err != nil {
		o.logger.Error("failed to transition to RESERVED after lien creation",
			"payment_order_id", po.ID.String(),
			"lien_id", lienID,
			"error", err)
		return
	}

	// Store bucket_id in payment order if provided
	if bucketID != "" {
		po.BucketID = bucketID
	}

	if err := o.repo.Update(ctx, po); err != nil {
		o.logger.Error("failed to persist RESERVED state",
			"payment_order_id", po.ID.String(),
			"error", err)
		return
	}

	o.logger.Info("payment order transitioned to RESERVED",
		"payment_order_id", po.ID.String(),
		"lien_id", lienID,
		"bucket_id", bucketID)

	// Publish PaymentOrderReserved event
	o.publishEvent(ctx, TopicPaymentOrderReserved, po.ID.String(), &eventsv1.PaymentOrderReservedEvent{
		EventId:         uuid.New().String(),
		PaymentOrderId:  po.ID.String(),
		DebtorAccountId: po.DebtorAccountID,
		LienId:          lienID,
		Amount:          toMoneyAmount(po.Amount),
		CorrelationId:   po.CorrelationID,
		CausationId:     po.ID.String(),
		Timestamp:       timestamppb.Now(),
		Version:         int64(po.Version),
		IdempotencyKey:  po.IdempotencyKey,
	})
}

// applyExecutingTransition transitions the payment order to EXECUTING state if a gateway reference was created.
func (o *PaymentOrchestrator) applyExecutingTransition(ctx context.Context, po *domain.PaymentOrder, gatewayReferenceID string) {
	if gatewayReferenceID == "" || po.Status != domain.PaymentOrderStatusReserved {
		return
	}

	if err := po.Execute(gatewayReferenceID); err != nil {
		o.logger.Error("failed to transition to EXECUTING after gateway submission",
			"payment_order_id", po.ID.String(),
			"gateway_reference_id", gatewayReferenceID,
			"error", err)
		return
	}

	if err := o.repo.Update(ctx, po); err != nil {
		o.logger.Error("failed to persist EXECUTING state",
			"payment_order_id", po.ID.String(),
			"error", err)
		return
	}

	o.logger.Info("payment order transitioned to EXECUTING",
		"payment_order_id", po.ID.String(),
		"gateway_reference_id", gatewayReferenceID)

	// Publish PaymentOrderExecuting event
	o.publishEvent(ctx, TopicPaymentOrderExecuting, po.ID.String(), &eventsv1.PaymentOrderExecutingEvent{
		EventId:            uuid.New().String(),
		PaymentOrderId:     po.ID.String(),
		GatewayReferenceId: gatewayReferenceID,
		CorrelationId:      po.CorrelationID,
		CausationId:        po.ID.String(),
		Timestamp:          timestamppb.Now(),
		Version:            int64(po.Version),
		IdempotencyKey:     po.IdempotencyKey,
	})
}

// evaluateBucketID evaluates the bucket ID for a payment order.
// This is a convenience wrapper around evaluateBucketIDForHandler for backward compatibility
// with tests and any other callers that work with PaymentOrder domain objects.
func (o *PaymentOrchestrator) evaluateBucketID(ctx context.Context, po *domain.PaymentOrder) (string, error) {
	// Create minimal deps for bucket evaluation - only requires ReferenceDataClient and BucketEvaluator
	deps := &PaymentOrderHandlerDeps{
		ReferenceDataClient: o.referenceDataClient,
		BucketEvaluator:     o.bucketEvaluator,
		Logger:              o.logger,
	}

	return evaluateBucketIDForHandler(
		ctx,
		deps,
		po.InstrumentCode,
		po.PaymentAttributes,
		po.ID.String(),
		o.logger,
	)
}

// failPaymentOrder handles payment order failure with proper state transition and event publishing.
// Returns an error if the state transition or persistence fails. Callers in synchronous paths
// (e.g., UpdatePaymentOrder) should propagate this error to clients. Callers in async paths
// (e.g., saga orchestration) may log and swallow the error.
func (o *PaymentOrchestrator) failPaymentOrder(ctx context.Context, po *domain.PaymentOrder, reason string, errorCode string) error {
	// Capture original status before transitioning (for event)
	failedAtStatus := po.Status

	// Check if lien needs to be released before transitioning
	needsLienRelease := po.RequiresLienRelease()
	lienID := po.LienID

	// Transition to FAILED
	if err := po.Fail(reason, errorCode); err != nil {
		o.logger.Error("failed to transition to FAILED state",
			"error", err,
			"payment_order_id", po.ID.String())
		return fmt.Errorf("failed to transition to FAILED state: %w", err)
	}

	if err := o.repo.Update(ctx, po); err != nil {
		o.logger.Error("failed to persist FAILED state",
			"error", err,
			"payment_order_id", po.ID.String())
		return fmt.Errorf("failed to persist FAILED state: %w", err)
	}

	// Release lien if needed
	if needsLienRelease && lienID != "" && o.currentAccountClient != nil {
		_, err := o.currentAccountClient.TerminateLien(ctx, &currentaccountv1.TerminateLienRequest{
			LienId: lienID,
			Reason: fmt.Sprintf("Payment order %s failed: %s", po.ID.String(), reason),
		})
		if err != nil {
			o.logger.Error("failed to release lien after failure",
				"error", err,
				"lien_id", lienID,
				"payment_order_id", po.ID.String())
		}
	}

	// Publish PaymentOrderFailed event
	o.publishEvent(ctx, TopicPaymentOrderFailed, po.ID.String(), &eventsv1.PaymentOrderFailedEvent{
		EventId:         uuid.New().String(),
		PaymentOrderId:  po.ID.String(),
		DebtorAccountId: po.DebtorAccountID,
		Amount:          toMoneyAmount(po.Amount),
		FailureReason:   reason,
		ErrorCode:       errorCode,
		FailedAtStatus:  mapStatusToProto(failedAtStatus),
		LienId:          lienID,
		CorrelationId:   po.CorrelationID,
		CausationId:     po.ID.String(),
		Timestamp:       timestamppb.Now(),
		Version:         int64(po.Version),
		IdempotencyKey:  po.IdempotencyKey,
	})

	o.logger.Info("payment order failed",
		"payment_order_id", po.ID.String(),
		"reason", reason,
		"error_code", errorCode,
		"idempotency_key", po.IdempotencyKey,
		"correlation_id", po.CorrelationID)

	return nil
}
