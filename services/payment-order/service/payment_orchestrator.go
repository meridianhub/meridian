// Package service implements gRPC services for the payment order domain
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	"github.com/meridianhub/meridian/services/payment-order/adapters/gateway"
	"github.com/meridianhub/meridian/services/payment-order/adapters/persistence"
	"github.com/meridianhub/meridian/services/payment-order/config"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	poobservability "github.com/meridianhub/meridian/services/payment-order/observability"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/pkg/proto/mappers"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Orchestrator configuration errors.
// Core errors are re-exported from shared/pkg/clients for consistency across services.
// When service startup fails due to these errors, the application will:
// 1. Exit with a non-zero status code
// 2. Log the specific error with context about which dependency is missing
// 3. Enter crash loop backoff in Kubernetes until the configuration is fixed
var (
	ErrOrchestratorLoggerNil = sharedclients.ErrConfigLoggerNil
	ErrOrchestratorRepoNil   = sharedclients.ErrConfigRepositoryNil
)

// Runtime configuration errors for optional dependencies.
// These are checked at runtime during Orchestrate() with graceful error handling.
var (
	ErrGatewayAccountConfigNotSet      = errors.New("gateway account config not configured")
	ErrFinancialAccountingClientNotSet = errors.New("financial accounting client not configured")
	ErrNilBookingLogResponse           = errors.New("financial accounting returned nil booking log")
)

// Parameter validation errors for PostLedgerEntriesFromParams.
var (
	ErrMissingPaymentOrderID     = errors.New("missing or invalid payment_order_id")
	ErrMissingDebtorAccountID    = errors.New("missing or invalid debtor_account_id")
	ErrMissingGatewayReferenceID = errors.New("missing or invalid gateway_reference_id")
	ErrMissingAmountCents        = errors.New("missing or invalid amount_cents")
	ErrMissingCurrency           = errors.New("missing or invalid currency")
	ErrMissingIdempotencyKey     = errors.New("missing or invalid idempotency_key")
	ErrParamKeyNotFound          = errors.New("param key not found")
	ErrParamInvalidType          = errors.New("param has invalid type")
)

// PaymentOrchestrator encapsulates payment saga orchestration logic.
// It handles the multi-step payment workflow including fund reservation,
// gateway communication, ledger posting, and lien execution.
type PaymentOrchestrator struct {
	logger                    *slog.Logger
	repo                      persistence.Repository
	currentAccountClient      CurrentAccountClient
	paymentGateway            gateway.PaymentGateway
	financialAccountingClient FinancialAccountingClient
	internalBankAccountClient InternalBankAccountClient // Optional - for internal clearing
	referenceDataClient       ReferenceDataClient       // Optional - for bucket-aware solvency and GetSaga()
	bucketEvaluator           *BucketEvaluator          // Cached CEL evaluator for bucket IDs
	accountResolver           *AccountResolver          // Optional - resolves clearing accounts dynamically
	gatewayAccountConfig      *config.GatewayAccountConfig
	kafkaPublisher            KafkaPublisher
	lienExecutionRetryConfig  *sharedclients.RetryConfig
	internalClearingEnabled   bool
	lockClient                LockClient // Distributed lock client for preventing concurrent lien execution

	// Starlark saga execution fields
	starlarkRunner  *saga.StarlarkSagaRunner // Executes saga scripts
	handlerRegistry *saga.HandlerRegistry    // Registry of payment-order handlers
}

// PaymentOrchestratorConfig contains dependencies for creating a PaymentOrchestrator
type PaymentOrchestratorConfig struct {
	Logger                    *slog.Logger
	Repo                      persistence.Repository
	CurrentAccountClient      CurrentAccountClient
	PaymentGateway            gateway.PaymentGateway
	FinancialAccountingClient FinancialAccountingClient
	InternalBankAccountClient InternalBankAccountClient // Optional - for internal clearing
	ReferenceDataClient       ReferenceDataClient       // Optional - for bucket-aware solvency validation
	AccountResolver           *AccountResolver          // Optional - auto-created if InternalBankAccountClient is provided
	GatewayAccountConfig      *config.GatewayAccountConfig
	KafkaPublisher            KafkaPublisher
	LienExecutionRetryConfig  *sharedclients.RetryConfig
	InternalClearingEnabled   bool
	LockClient                LockClient // Distributed lock client for preventing concurrent lien execution
}

// NewPaymentOrchestrator creates a new payment orchestrator with the given dependencies.
// Returns an error if required dependencies (Logger, Repo) are nil. CurrentAccountClient and
// PaymentGateway are validated at runtime in Orchestrate() with graceful error handling.
//
// If InternalBankAccountClient is provided but AccountResolver is nil, an AccountResolver
// is automatically created using the client and logger.
func NewPaymentOrchestrator(cfg PaymentOrchestratorConfig) (*PaymentOrchestrator, error) {
	if cfg.Logger == nil {
		return nil, ErrOrchestratorLoggerNil
	}
	if cfg.Repo == nil {
		return nil, ErrOrchestratorRepoNil
	}

	// Auto-create AccountResolver if InternalBankAccountClient is provided but AccountResolver is nil
	accountResolver := cfg.AccountResolver
	if cfg.InternalBankAccountClient != nil && accountResolver == nil {
		var err error
		accountResolver, err = NewAccountResolver(AccountResolverConfig{
			Client: cfg.InternalBankAccountClient,
			Logger: cfg.Logger,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create account resolver: %w", err)
		}
	}

	// Create bucket evaluator for CEL expression caching across requests
	bucketEvaluator, err := NewBucketEvaluator(cfg.Logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create bucket evaluator: %w", err)
	}

	// Create handler registry and register payment-order handlers
	handlerRegistry := saga.NewHandlerRegistry()
	handlerDeps := &PaymentOrderHandlerDeps{
		CurrentAccountClient:      cfg.CurrentAccountClient,
		PaymentGateway:            cfg.PaymentGateway,
		FinancialAccountingClient: cfg.FinancialAccountingClient,
		ReferenceDataClient:       cfg.ReferenceDataClient,
		BucketEvaluator:           bucketEvaluator,
		LienExecutionRetryConfig:  cfg.LienExecutionRetryConfig,
		Logger:                    cfg.Logger,
		Orchestrator:              nil, // Will be set after orchestrator creation
	}

	if err := RegisterPaymentOrderHandlers(handlerRegistry, handlerDeps); err != nil {
		return nil, fmt.Errorf("failed to register payment order handlers: %w", err)
	}

	// Create Starlark runtime and runner
	runtime, err := saga.NewRuntime(cfg.Logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create starlark runtime: %w", err)
	}

	starlarkRunner, err := saga.NewStarlarkSagaRunner(saga.StarlarkSagaRunnerConfig{
		Runtime:  runtime,
		Registry: handlerRegistry,
		Logger:   cfg.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create starlark saga runner: %w", err)
	}

	orchestrator := &PaymentOrchestrator{
		logger:                    cfg.Logger,
		repo:                      cfg.Repo,
		currentAccountClient:      cfg.CurrentAccountClient,
		paymentGateway:            cfg.PaymentGateway,
		financialAccountingClient: cfg.FinancialAccountingClient,
		internalBankAccountClient: cfg.InternalBankAccountClient,
		referenceDataClient:       cfg.ReferenceDataClient,
		bucketEvaluator:           bucketEvaluator,
		accountResolver:           accountResolver,
		gatewayAccountConfig:      cfg.GatewayAccountConfig,
		kafkaPublisher:            cfg.KafkaPublisher,
		lienExecutionRetryConfig:  cfg.LienExecutionRetryConfig,
		internalClearingEnabled:   cfg.InternalClearingEnabled,
		lockClient:                cfg.LockClient,
		starlarkRunner:            starlarkRunner,
		handlerRegistry:           handlerRegistry,
	}

	// Set orchestrator reference in handler deps for PostLedgerEntries callback
	handlerDeps.Orchestrator = orchestrator

	return orchestrator, nil
}

// Orchestrate executes the payment saga using Starlark script execution.
// The saga script is fetched from reference-data service and executed via StarlarkSagaRunner.
// Compensation is handled automatically by the Starlark runtime on failure.
func (o *PaymentOrchestrator) Orchestrate(ctx context.Context, po *domain.PaymentOrder) {
	o.logger.Info("starting payment saga with Starlark execution",
		"payment_order_id", po.ID.String(),
		"correlation_id", po.CorrelationID)

	// Check if all dependencies are available
	if o.currentAccountClient == nil || o.paymentGateway == nil {
		o.logger.Error("saga dependencies not configured",
			"payment_order_id", po.ID.String())
		// Async path: log and swallow error - best effort failure handling
		if err := o.failPaymentOrder(ctx, po, "service configuration error", "INTERNAL_ERROR"); err != nil {
			o.logger.Error("failed to mark payment order as failed",
				"payment_order_id", po.ID.String(),
				"error", err)
		}
		return
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
		return
	}

	// Fetch saga script from reference-data service
	sagaDef, err := o.referenceDataClient.GetSaga(ctx, "payment_execution", 0) // 0 = fetch ACTIVE version
	if err != nil {
		o.logger.Error("failed to fetch saga definition from reference-data",
			"payment_order_id", po.ID.String(),
			"saga_name", "payment_execution",
			"error", err)
		if err := o.failPaymentOrder(ctx, po, fmt.Sprintf("failed to fetch saga: %v", err), "INTERNAL_ERROR"); err != nil {
			o.logger.Error("failed to mark payment order as failed",
				"payment_order_id", po.ID.String(),
				"error", err)
		}
		return
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

	// Map PaymentOrder domain object to RunnerInput.Input map
	runnerInput := saga.RunnerInput{
		SagaExecutionID: uuid.New(),
		CorrelationID:   correlationID,
		Input: map[string]interface{}{
			"payment_order_id":   po.ID.String(),
			"debtor_account_id":  po.DebtorAccountID,
			"creditor_reference": po.CreditorReference,
			"amount_cents":       domain.ToMinorUnits(po.Amount),
			"currency":           domain.CurrencyCode(po.Amount),
			"idempotency_key":    po.IdempotencyKey,
			"instrument_code":    po.InstrumentCode,
			"payment_attributes": po.PaymentAttributes,
		},
	}

	// Execute saga via StarlarkSagaRunner
	result, err := o.starlarkRunner.ExecuteSaga(ctx, "payment_execution", sagaDef.Script, runnerInput)
	if err != nil {
		o.logger.Error("starlark saga runner returned error",
			"payment_order_id", po.ID.String(),
			"error", err)
		if err := o.failPaymentOrder(ctx, po, fmt.Sprintf("saga execution error: %v", err), "SAGA_FAILED"); err != nil {
			o.logger.Error("failed to mark payment order as failed",
				"payment_order_id", po.ID.String(),
				"error", err)
		}
		return
	}

	// Handle saga result
	o.handleStarlarkSagaResult(ctx, po, result)
}

// handleStarlarkSagaResult processes the result from StarlarkSagaRunner execution.
// On failure, it logs failed steps and marks the payment order as failed.
// On success, it extracts outputs (lien_id, gateway_reference_id) and logs completion.
func (o *PaymentOrchestrator) handleStarlarkSagaResult(ctx context.Context, po *domain.PaymentOrder, result *saga.RunnerOutput) {
	if !result.Success {
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
		return
	}

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
	// If a step succeeded but its output is missing, fail the payment order
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
			"payment_order_id", latestPO.ID.String(),
			"output", result.Output)
		if err := o.failPaymentOrder(ctx, latestPO, "saga output missing lien_id", "SAGA_OUTPUT_INVALID"); err != nil {
			o.logger.Error("failed to mark payment order as failed after missing lien_id",
				"payment_order_id", latestPO.ID.String(),
				"error", err)
		}
		return
	}

	if sendToGatewaySucceeded && gatewayReferenceID == "" {
		o.logger.Error("saga output missing gateway_reference_id after successful send_to_gateway handler",
			"payment_order_id", latestPO.ID.String(),
			"output", result.Output)
		if err := o.failPaymentOrder(ctx, latestPO, "saga output missing gateway_reference_id", "SAGA_OUTPUT_INVALID"); err != nil {
			o.logger.Error("failed to mark payment order as failed after missing gateway_reference_id",
				"payment_order_id", latestPO.ID.String(),
				"error", err)
		}
		return
	}

	// Apply state transitions based on what the saga accomplished
	// The handlers call external services but don't update PaymentOrder state
	// This orchestrator method applies domain state transitions and publishes events

	// If lien was created and we're still in INITIATED state, transition to RESERVED
	if lienID != "" && latestPO.Status == domain.PaymentOrderStatusInitiated {
		if err := latestPO.Reserve(lienID); err != nil {
			o.logger.Error("failed to transition to RESERVED after lien creation",
				"payment_order_id", latestPO.ID.String(),
				"lien_id", lienID,
				"error", err)
			return
		}

		// Store bucket_id in payment order if provided
		if bucketID != "" {
			latestPO.BucketID = bucketID
		}

		if err := o.repo.Update(ctx, latestPO); err != nil {
			o.logger.Error("failed to persist RESERVED state",
				"payment_order_id", latestPO.ID.String(),
				"error", err)
			return
		}

		o.logger.Info("payment order transitioned to RESERVED",
			"payment_order_id", latestPO.ID.String(),
			"lien_id", lienID,
			"bucket_id", bucketID)

		// Publish PaymentOrderReserved event
		o.publishEvent(ctx, TopicPaymentOrderReserved, latestPO.ID.String(), &eventsv1.PaymentOrderReservedEvent{
			EventId:         uuid.New().String(),
			PaymentOrderId:  latestPO.ID.String(),
			DebtorAccountId: latestPO.DebtorAccountID,
			LienId:          lienID,
			Amount:          toMoneyAmount(latestPO.Amount),
			CorrelationId:   latestPO.CorrelationID,
			CausationId:     latestPO.ID.String(),
			Timestamp:       timestamppb.Now(),
			Version:         int64(latestPO.Version),
			IdempotencyKey:  latestPO.IdempotencyKey,
		})
	}

	// If gateway reference was created and we're in RESERVED state, transition to EXECUTING
	if gatewayReferenceID != "" && latestPO.Status == domain.PaymentOrderStatusReserved {
		if err := latestPO.Execute(gatewayReferenceID); err != nil {
			o.logger.Error("failed to transition to EXECUTING after gateway submission",
				"payment_order_id", latestPO.ID.String(),
				"gateway_reference_id", gatewayReferenceID,
				"error", err)
			return
		}

		if err := o.repo.Update(ctx, latestPO); err != nil {
			o.logger.Error("failed to persist EXECUTING state",
				"payment_order_id", latestPO.ID.String(),
				"error", err)
			return
		}

		o.logger.Info("payment order transitioned to EXECUTING",
			"payment_order_id", latestPO.ID.String(),
			"gateway_reference_id", gatewayReferenceID)

		// Publish PaymentOrderExecuting event
		o.publishEvent(ctx, TopicPaymentOrderExecuting, latestPO.ID.String(), &eventsv1.PaymentOrderExecutingEvent{
			EventId:            uuid.New().String(),
			PaymentOrderId:     latestPO.ID.String(),
			GatewayReferenceId: gatewayReferenceID,
			CorrelationId:      latestPO.CorrelationID,
			CausationId:        latestPO.ID.String(),
			Timestamp:          timestamppb.Now(),
			Version:            int64(latestPO.Version),
			IdempotencyKey:     latestPO.IdempotencyKey,
		})
	}

	o.logger.Info("payment saga completed successfully via Starlark",
		"payment_order_id", latestPO.ID.String(),
		"lien_id", lienID,
		"gateway_reference_id", gatewayReferenceID,
		"step_count", len(result.StepResults),
		"final_status", latestPO.Status)
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

// PostLedgerEntries creates double-entry bookkeeping entries for a completed payment.
// It creates a BookingLog in PENDING status, captures debit and credit postings, then
// updates the BookingLog to POSTED status. Returns the booking log ID on success.
//
// Double-entry accounting supports two flows:
//
// Standard Flow (2 postings):
//   - DEBIT: Customer's account (funds leaving their account)
//   - CREDIT: Gateway's contra-account (liability to payment processor)
//
// Internal Clearing Flow (4 postings) - when internalClearingEnabled and clearing account resolved:
//   - DEBIT: Customer's account (funds leaving their account)
//   - CREDIT: Clearing account (funds enter internal clearing)
//   - DEBIT: Clearing account (funds leave internal clearing)
//   - CREDIT: Gateway's contra-account (liability to payment processor)
//
// The 4-posting flow maintains double-entry balance while routing through the internal
// clearing account, enabling enhanced reconciliation and settlement tracking.
//
// Atomicity considerations:
// Standard flow makes 4 sequential gRPC calls, clearing flow makes 6:
//  1. InitiateFinancialBookingLog (creates BookingLog in PENDING)
//  2. CaptureLedgerPosting (DEBIT customer)
//  3. CaptureLedgerPosting (CREDIT clearing) - only in clearing flow
//  4. CaptureLedgerPosting (DEBIT clearing) - only in clearing flow
//  5. CaptureLedgerPosting (CREDIT gateway)
//  6. UpdateFinancialBookingLog (marks as POSTED)
//
// Partial failure scenarios (documented for operational runbooks):
//   - Step 1 fails: No orphaned state - safe to retry
//   - Posting step fails: BookingLog in PENDING, unbalanced - needs reversal/cleanup
//   - Final status update fails: BookingLog in PENDING, balanced entries exist - just needs status update
//
// All partial failures are logged with RECONCILIATION_REQUIRED prefix and include
// the booking_log_id for manual resolution. See runbook: docs/runbooks/saga-failure-recovery.md
//
// Error handling: If any step fails, the error is returned and the calling code
// should mark the payment as FAILED. The BookingLog will remain in PENDING status
// for reconciliation purposes.
//
// Clearing account fallback: If internal clearing is enabled but the clearing account
// lookup fails, the method falls back to the standard 2-posting flow gracefully.
func (o *PaymentOrchestrator) PostLedgerEntries(ctx context.Context, po *domain.PaymentOrder) (string, error) {
	// Check required dependencies - these may be nil in minimal test configuration
	if o.gatewayAccountConfig == nil {
		return "", ErrGatewayAccountConfigNotSet
	}
	if o.financialAccountingClient == nil {
		return "", ErrFinancialAccountingClientNotSet
	}

	// Get the gateway contra-account from configuration
	// Extract gateway ID from the GatewayReferenceID prefix (e.g., "GW-uuid" -> "mock" for mock gateway)
	gatewayID := extractGatewayIDFromRef(po.GatewayReferenceID)
	contraAccountID, err := o.gatewayAccountConfig.GetContraAccount(gatewayID)
	if err != nil {
		return "", fmt.Errorf("failed to get contra-account for gateway %s: %w", gatewayID, err)
	}

	// Convert domain currency to proto currency
	currencyCode := domain.CurrencyCode(po.Amount)
	protoCurrency := mappers.CurrencyCodeToProto(currencyCode)
	if protoCurrency == commonpb.Currency_CURRENCY_UNSPECIFIED {
		o.logger.Warn("unsupported currency for ledger posting - payment will be marked as failed",
			"currency", currencyCode,
			"payment_order_id", po.ID.String(),
			"supported_currencies", "GBP, USD, EUR")
		return "", fmt.Errorf("%w: %s", ErrUnsupportedCurrency, currencyCode)
	}

	// Step 1: Create a BookingLog in PENDING status
	bookingLogIDempKey := fmt.Sprintf("booking-log-%s", po.IdempotencyKey)
	bookingLogResp, err := o.financialAccountingClient.InitiateFinancialBookingLog(ctx, &financialaccountingv1.InitiateFinancialBookingLogRequest{
		FinancialAccountType:    commonpb.AccountType_ACCOUNT_TYPE_CURRENT,
		ProductServiceReference: "payment-order",
		BusinessUnitReference:   "payment-order-service",
		ChartOfAccountsRules:    "outbound-payment",
		BaseCurrency:            protoCurrency,
		IdempotencyKey: &commonpb.IdempotencyKey{
			Key: bookingLogIDempKey,
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to create booking log: %w", err)
	}
	if bookingLogResp.FinancialBookingLog == nil {
		return "", fmt.Errorf("%w: payment order %s", ErrNilBookingLogResponse, po.ID.String())
	}
	bookingLogID := bookingLogResp.FinancialBookingLog.Id

	o.logger.Debug("created booking log for ledger posting",
		"booking_log_id", bookingLogID,
		"payment_order_id", po.ID.String())

	// Convert amount from cents to google.type.Money format.
	// google.type.Money uses Units (whole currency units) + Nanos (10^-9 fraction).
	// Example: 199 cents = 1 unit + 990,000,000 nanos = 1.99 currency units.
	// Formula: Units = cents / 100, Nanos = (cents % 100) * 10,000,000
	amountCents := domain.ToMinorUnits(po.Amount)
	postingAmount := &money.Money{
		CurrencyCode: currencyCode,
		Units:        amountCents / 100,
		Nanos:        int32((amountCents % 100) * 10000000),
	}
	valueDate := timestamppb.Now()

	// Determine if we should use the 4-posting flow with internal clearing
	var clearingAccountID string
	useClearingFlow := false

	if o.internalClearingEnabled && o.accountResolver != nil {
		var resolveErr error
		clearingAccountID, resolveErr = o.accountResolver.GetSettlementClearingAccount(ctx, currencyCode)
		if resolveErr != nil {
			// Log the fallback but continue with standard 2-posting flow
			o.logger.Info("clearing account lookup failed, falling back to standard posting flow",
				"payment_order_id", po.ID.String(),
				"currency", currencyCode,
				"reason", resolveErr.Error())
		} else {
			useClearingFlow = true
			o.logger.Info("using internal clearing flow with 4 postings",
				"payment_order_id", po.ID.String(),
				"clearing_account_id", clearingAccountID,
				"currency", currencyCode)
		}
	} else if o.internalClearingEnabled {
		o.logger.Debug("internal clearing enabled but account resolver not configured, using standard flow",
			"payment_order_id", po.ID.String())
	}

	if useClearingFlow {
		// 4-posting flow: Customer DEBIT -> Clearing CREDIT -> Clearing DEBIT -> Gateway CREDIT
		return o.postLedgerEntriesWithClearing(ctx, po, bookingLogID, postingAmount, valueDate,
			clearingAccountID, contraAccountID, amountCents, currencyCode)
	}

	// Standard 2-posting flow: Customer DEBIT -> Gateway CREDIT
	return o.postLedgerEntriesStandard(ctx, po, bookingLogID, postingAmount, valueDate,
		contraAccountID, amountCents, currencyCode)
}

// postLedgerEntriesStandard creates the standard 2-posting flow for ledger entries.
// Posts: Customer DEBIT -> Gateway CREDIT
func (o *PaymentOrchestrator) postLedgerEntriesStandard(
	ctx context.Context,
	po *domain.PaymentOrder,
	bookingLogID string,
	postingAmount *money.Money,
	valueDate *timestamppb.Timestamp,
	contraAccountID string,
	amountCents int64,
	currencyCode string,
) (string, error) {
	// Step 2: Create DEBIT posting (customer account - funds leaving)
	debitIdempKey := fmt.Sprintf("debit-customer-%s", po.IdempotencyKey)
	_, err := o.financialAccountingClient.CaptureLedgerPosting(ctx, &financialaccountingv1.CaptureLedgerPostingRequest{
		FinancialBookingLogId: bookingLogID,
		PostingDirection:      commonpb.PostingDirection_POSTING_DIRECTION_DEBIT,
		PostingAmount:         postingAmount,
		AccountId:             po.DebtorAccountID,
		ValueDate:             valueDate,
		IdempotencyKey: &commonpb.IdempotencyKey{
			Key: debitIdempKey,
		},
	})
	if err != nil {
		// RECONCILIATION: BookingLog created but debit posting failed - requires manual cleanup
		o.logger.Error("RECONCILIATION_REQUIRED: booking log orphaned after debit posting failure",
			"booking_log_id", bookingLogID,
			"booking_log_status", "PENDING",
			"payment_order_id", po.ID.String(),
			"failed_step", "debit_customer_posting",
			"posting_flow", "standard",
			"debtor_account", po.DebtorAccountID,
			"error", err.Error())
		return "", fmt.Errorf("failed to create debit posting for account %s: %w", po.DebtorAccountID, err)
	}

	o.logger.Debug("created debit posting (customer)",
		"booking_log_id", bookingLogID,
		"account_id", po.DebtorAccountID,
		"amount_cents", amountCents,
		"payment_order_id", po.ID.String())

	// Step 3: Create CREDIT posting (gateway contra-account - liability to processor)
	creditIdempKey := fmt.Sprintf("credit-gateway-%s", po.IdempotencyKey)
	_, err = o.financialAccountingClient.CaptureLedgerPosting(ctx, &financialaccountingv1.CaptureLedgerPostingRequest{
		FinancialBookingLogId: bookingLogID,
		PostingDirection:      commonpb.PostingDirection_POSTING_DIRECTION_CREDIT,
		PostingAmount:         postingAmount,
		AccountId:             contraAccountID,
		ValueDate:             valueDate,
		IdempotencyKey: &commonpb.IdempotencyKey{
			Key: creditIdempKey,
		},
	})
	if err != nil {
		// RECONCILIATION: BookingLog has debit but no credit - unbalanced ledger requires cleanup
		o.logger.Error("RECONCILIATION_REQUIRED: booking log orphaned after credit posting failure",
			"booking_log_id", bookingLogID,
			"booking_log_status", "PENDING",
			"payment_order_id", po.ID.String(),
			"failed_step", "credit_gateway_posting",
			"posting_flow", "standard",
			"debtor_account", po.DebtorAccountID,
			"contra_account", contraAccountID,
			"has_debit_customer_posting", true,
			"error", err.Error())
		return "", fmt.Errorf("failed to create credit posting for account %s: %w", contraAccountID, err)
	}

	o.logger.Debug("created credit posting (gateway)",
		"booking_log_id", bookingLogID,
		"account_id", contraAccountID,
		"amount_cents", amountCents,
		"payment_order_id", po.ID.String())

	// Step 4: Update BookingLog status to POSTED (balanced entries are now complete)
	_, err = o.financialAccountingClient.UpdateFinancialBookingLog(ctx, &financialaccountingv1.UpdateFinancialBookingLogRequest{
		Id:     bookingLogID,
		Status: commonpb.TransactionStatus_TRANSACTION_STATUS_POSTED,
	})
	if err != nil {
		// RECONCILIATION: BookingLog has balanced entries but status update failed
		// The ledger entries exist and are balanced - just need status update
		o.logger.Error("RECONCILIATION_REQUIRED: booking log status update failed after successful postings",
			"booking_log_id", bookingLogID,
			"booking_log_status", "PENDING",
			"target_status", "POSTED",
			"payment_order_id", po.ID.String(),
			"failed_step", "status_update",
			"posting_flow", "standard",
			"has_debit_customer_posting", true,
			"has_credit_gateway_posting", true,
			"resolution", "manually update booking log status to POSTED",
			"error", err.Error())
		return "", fmt.Errorf("failed to update booking log to POSTED: %w", err)
	}

	o.logger.Info("ledger posting completed successfully (standard flow)",
		"booking_log_id", bookingLogID,
		"payment_order_id", po.ID.String(),
		"debtor_account", po.DebtorAccountID,
		"contra_account", contraAccountID,
		"posting_count", 2,
		"amount_cents", amountCents,
		"currency", currencyCode)

	return bookingLogID, nil
}

// postLedgerEntriesWithClearing creates the 4-posting flow for ledger entries with internal clearing.
// Posts: Customer DEBIT -> Clearing CREDIT -> Clearing DEBIT -> Gateway CREDIT
// This maintains double-entry balance while routing through the internal clearing account.
func (o *PaymentOrchestrator) postLedgerEntriesWithClearing(
	ctx context.Context,
	po *domain.PaymentOrder,
	bookingLogID string,
	postingAmount *money.Money,
	valueDate *timestamppb.Timestamp,
	clearingAccountID string,
	contraAccountID string,
	amountCents int64,
	currencyCode string,
) (string, error) {
	// Step 2: Create DEBIT posting (customer account - funds leaving)
	debitCustomerIdempKey := fmt.Sprintf("debit-customer-%s", po.IdempotencyKey)
	_, err := o.financialAccountingClient.CaptureLedgerPosting(ctx, &financialaccountingv1.CaptureLedgerPostingRequest{
		FinancialBookingLogId: bookingLogID,
		PostingDirection:      commonpb.PostingDirection_POSTING_DIRECTION_DEBIT,
		PostingAmount:         postingAmount,
		AccountId:             po.DebtorAccountID,
		ValueDate:             valueDate,
		IdempotencyKey: &commonpb.IdempotencyKey{
			Key: debitCustomerIdempKey,
		},
	})
	if err != nil {
		o.logger.Error("RECONCILIATION_REQUIRED: booking log orphaned after debit posting failure",
			"booking_log_id", bookingLogID,
			"booking_log_status", "PENDING",
			"payment_order_id", po.ID.String(),
			"failed_step", "debit_customer_posting",
			"posting_flow", "clearing",
			"debtor_account", po.DebtorAccountID,
			"clearing_account", clearingAccountID,
			"error", err.Error())
		return "", fmt.Errorf("failed to create debit posting for customer account %s: %w", po.DebtorAccountID, err)
	}

	o.logger.Debug("created debit posting (customer) in clearing flow",
		"booking_log_id", bookingLogID,
		"account_id", po.DebtorAccountID,
		"amount_cents", amountCents,
		"payment_order_id", po.ID.String())

	// Step 3: Create CREDIT posting (clearing account - funds enter clearing)
	creditClearingIdempKey := fmt.Sprintf("credit-clearing-%s", po.IdempotencyKey)
	_, err = o.financialAccountingClient.CaptureLedgerPosting(ctx, &financialaccountingv1.CaptureLedgerPostingRequest{
		FinancialBookingLogId: bookingLogID,
		PostingDirection:      commonpb.PostingDirection_POSTING_DIRECTION_CREDIT,
		PostingAmount:         postingAmount,
		AccountId:             clearingAccountID,
		ValueDate:             valueDate,
		IdempotencyKey: &commonpb.IdempotencyKey{
			Key: creditClearingIdempKey,
		},
	})
	if err != nil {
		o.logger.Error("RECONCILIATION_REQUIRED: booking log orphaned after credit clearing posting failure",
			"booking_log_id", bookingLogID,
			"booking_log_status", "PENDING",
			"payment_order_id", po.ID.String(),
			"failed_step", "credit_clearing_posting",
			"posting_flow", "clearing",
			"debtor_account", po.DebtorAccountID,
			"clearing_account", clearingAccountID,
			"has_debit_customer_posting", true,
			"error", err.Error())
		return "", fmt.Errorf("failed to create credit posting for clearing account %s: %w", clearingAccountID, err)
	}

	o.logger.Debug("created credit posting (clearing) in clearing flow",
		"booking_log_id", bookingLogID,
		"account_id", clearingAccountID,
		"amount_cents", amountCents,
		"payment_order_id", po.ID.String())

	// Step 4: Create DEBIT posting (clearing account - funds leave clearing)
	debitClearingIdempKey := fmt.Sprintf("debit-clearing-%s", po.IdempotencyKey)
	_, err = o.financialAccountingClient.CaptureLedgerPosting(ctx, &financialaccountingv1.CaptureLedgerPostingRequest{
		FinancialBookingLogId: bookingLogID,
		PostingDirection:      commonpb.PostingDirection_POSTING_DIRECTION_DEBIT,
		PostingAmount:         postingAmount,
		AccountId:             clearingAccountID,
		ValueDate:             valueDate,
		IdempotencyKey: &commonpb.IdempotencyKey{
			Key: debitClearingIdempKey,
		},
	})
	if err != nil {
		o.logger.Error("RECONCILIATION_REQUIRED: booking log orphaned after debit clearing posting failure",
			"booking_log_id", bookingLogID,
			"booking_log_status", "PENDING",
			"payment_order_id", po.ID.String(),
			"failed_step", "debit_clearing_posting",
			"posting_flow", "clearing",
			"debtor_account", po.DebtorAccountID,
			"clearing_account", clearingAccountID,
			"has_debit_customer_posting", true,
			"has_credit_clearing_posting", true,
			"error", err.Error())
		return "", fmt.Errorf("failed to create debit posting for clearing account %s: %w", clearingAccountID, err)
	}

	o.logger.Debug("created debit posting (clearing) in clearing flow",
		"booking_log_id", bookingLogID,
		"account_id", clearingAccountID,
		"amount_cents", amountCents,
		"payment_order_id", po.ID.String())

	// Step 5: Create CREDIT posting (gateway contra-account - liability to processor)
	creditGatewayIdempKey := fmt.Sprintf("credit-gateway-%s", po.IdempotencyKey)
	_, err = o.financialAccountingClient.CaptureLedgerPosting(ctx, &financialaccountingv1.CaptureLedgerPostingRequest{
		FinancialBookingLogId: bookingLogID,
		PostingDirection:      commonpb.PostingDirection_POSTING_DIRECTION_CREDIT,
		PostingAmount:         postingAmount,
		AccountId:             contraAccountID,
		ValueDate:             valueDate,
		IdempotencyKey: &commonpb.IdempotencyKey{
			Key: creditGatewayIdempKey,
		},
	})
	if err != nil {
		o.logger.Error("RECONCILIATION_REQUIRED: booking log orphaned after credit gateway posting failure",
			"booking_log_id", bookingLogID,
			"booking_log_status", "PENDING",
			"payment_order_id", po.ID.String(),
			"failed_step", "credit_gateway_posting",
			"posting_flow", "clearing",
			"debtor_account", po.DebtorAccountID,
			"clearing_account", clearingAccountID,
			"contra_account", contraAccountID,
			"has_debit_customer_posting", true,
			"has_credit_clearing_posting", true,
			"has_debit_clearing_posting", true,
			"error", err.Error())
		return "", fmt.Errorf("failed to create credit posting for gateway account %s: %w", contraAccountID, err)
	}

	o.logger.Debug("created credit posting (gateway) in clearing flow",
		"booking_log_id", bookingLogID,
		"account_id", contraAccountID,
		"amount_cents", amountCents,
		"payment_order_id", po.ID.String())

	// Step 6: Update BookingLog status to POSTED (all 4 balanced entries are complete)
	_, err = o.financialAccountingClient.UpdateFinancialBookingLog(ctx, &financialaccountingv1.UpdateFinancialBookingLogRequest{
		Id:     bookingLogID,
		Status: commonpb.TransactionStatus_TRANSACTION_STATUS_POSTED,
	})
	if err != nil {
		o.logger.Error("RECONCILIATION_REQUIRED: booking log status update failed after successful postings",
			"booking_log_id", bookingLogID,
			"booking_log_status", "PENDING",
			"target_status", "POSTED",
			"payment_order_id", po.ID.String(),
			"failed_step", "status_update",
			"posting_flow", "clearing",
			"has_debit_customer_posting", true,
			"has_credit_clearing_posting", true,
			"has_debit_clearing_posting", true,
			"has_credit_gateway_posting", true,
			"resolution", "manually update booking log status to POSTED",
			"error", err.Error())
		return "", fmt.Errorf("failed to update booking log to POSTED: %w", err)
	}

	o.logger.Info("ledger posting completed successfully (clearing flow)",
		"booking_log_id", bookingLogID,
		"payment_order_id", po.ID.String(),
		"debtor_account", po.DebtorAccountID,
		"clearing_account", clearingAccountID,
		"contra_account", contraAccountID,
		"posting_count", 4,
		"amount_cents", amountCents,
		"currency", currencyCode)

	return bookingLogID, nil
}

// ExecuteLienWithRetry executes a lien asynchronously with exponential backoff retry.
// This is called in a goroutine after a payment order is marked COMPLETED.
// The lien execution status is tracked in the payment order for reconciliation.
//
// The method:
// 1. Creates a context with timeout for the entire retry sequence
// 2. Uses exponential backoff for retries with the existing sharedclients.Retry infrastructure
// 3. Updates the payment order's lien execution status on success or final failure
// 4. Logs all attempts for monitoring and alerting
//
// nolint:contextcheck // Context is intentionally created fresh for async operation
func (o *PaymentOrchestrator) ExecuteLienWithRetry(parentCtx context.Context, paymentOrderID uuid.UUID, lienID string) {
	// Defensive check: guard against nil currentAccountClient even though callers currently check
	if o.currentAccountClient == nil {
		o.logger.Error("ExecuteLienWithRetry called with nil currentAccountClient",
			"payment_order_id", paymentOrderID.String(),
			"lien_id", lienID)
		return
	}

	// Recover from panics to prevent silent goroutine crashes
	defer func() {
		if r := recover(); r != nil {
			o.logger.Error("panic in ExecuteLienWithRetry",
				"panic", r,
				"payment_order_id", paymentOrderID.String(),
				"lien_id", lienID)
			// Attempt to mark as FAILED to prevent stuck PENDING state
			// Use a fresh context since the original may be cancelled
			panicCtx := context.Background()
			if tenantID, hasTenant := tenant.FromContext(parentCtx); hasTenant {
				panicCtx = tenant.WithTenant(panicCtx, tenantID)
			}
			panicCtx, panicCancel := context.WithTimeout(panicCtx, 10*time.Second) //nolint:contextcheck
			defer panicCancel()
			po, findErr := o.repo.FindByID(panicCtx, paymentOrderID) //nolint:contextcheck
			if findErr != nil {
				o.logger.Error("failed to fetch payment order after panic",
					"payment_order_id", paymentOrderID.String(),
					"error", findErr)
				return
			}
			po.SetLienExecutionFailed(fmt.Sprintf("panic: %v", r))
			if updateErr := o.repo.Update(panicCtx, po); updateErr != nil { //nolint:contextcheck
				o.logger.Error("failed to update payment order status after panic",
					"payment_order_id", paymentOrderID.String(),
					"error", updateErr)
			}
		}
	}()

	// Create a context with timeout for the entire retry sequence
	ctx, cancel := context.WithTimeout(parentCtx, DefaultLienExecutionRetryTimeout)
	defer cancel()

	logger := o.logger.With(
		"payment_order_id", paymentOrderID.String(),
		"lien_id", lienID,
		"operation", "execute_lien_async",
	)

	logger.Info("starting async lien execution with retry")

	// Use configured retry config or default
	retryConfig := o.lienExecutionRetryConfig
	if retryConfig == nil {
		retryConfig = &sharedclients.RetryConfig{
			MaxRetries:          DefaultLienExecutionMaxRetries,
			InitialInterval:     500 * time.Millisecond,
			MaxInterval:         defaults.DefaultRPCTimeout,
			Multiplier:          2.0,
			RandomizationFactor: 0.5,
		}
	}

	var lastErr error
	var attempts int

	// Execute with retry
	err := sharedclients.Retry(ctx, *retryConfig, func() error {
		attempts++
		logger.Info("attempting lien execution", "attempt", attempts)

		_, execErr := o.currentAccountClient.ExecuteLien(ctx, &currentaccountv1.ExecuteLienRequest{
			LienId: lienID,
		})

		if execErr != nil {
			logger.Warn("lien execution attempt failed",
				"attempt", attempts,
				"error", execErr)
			lastErr = execErr
			return execErr
		}

		logger.Info("lien execution succeeded", "attempt", attempts)
		return nil
	})

	// Update payment order with final status
	o.updateLienExecutionStatus(ctx, paymentOrderID, attempts, err, lastErr, logger)
}

// updateLienExecutionStatus updates the payment order's lien execution status after retry completion.
// This is called after all retry attempts have finished (success or failure).
// Uses distributed locking to prevent concurrent updates across service instances, combined with
// optimistic locking (version conflict retry) for additional safety.
// Note: Uses a fresh context to ensure the status update completes even if the parent context has timed out.
//
//nolint:contextcheck // Intentionally uses fresh context to outlive parent context
func (o *PaymentOrchestrator) updateLienExecutionStatus(
	parentCtx context.Context,
	paymentOrderID uuid.UUID,
	totalLienAttempts int,
	retryErr error,
	lastErr error,
	logger *slog.Logger,
) {
	// Use a fresh context to ensure status update isn't cancelled by parent timeout.
	// This is intentional - the parent context may have timed out during retries,
	// but we must still persist the final status for reconciliation purposes.
	//nolint:contextcheck // Intentionally using fresh context to ensure status persistence
	updateCtx := context.Background()
	if tenantID, hasTenant := tenant.FromContext(parentCtx); hasTenant {
		updateCtx = tenant.WithTenant(updateCtx, tenantID)
	}
	updateCtx, cancel := context.WithTimeout(updateCtx, lienStatusUpdateTimeout)
	defer cancel()

	// Acquire distributed lock if lock client is configured
	// This prevents concurrent status updates across multiple service instances
	var lock Lock
	if o.lockClient != nil {
		lockKey := fmt.Sprintf("lien:execution:%s", paymentOrderID.String())
		lockStart := time.Now()

		var lockErr error
		//nolint:contextcheck // updateCtx is intentionally fresh to outlive parent context
		lock, lockErr = o.lockClient.Obtain(updateCtx, lockKey, 30*time.Second)

		// Record lock wait duration
		poobservability.RecordLienExecutionLockWaitDuration(time.Since(lockStart).Seconds())

		if IsLockNotObtained(lockErr) {
			// Lock contention - another process is updating this payment order
			logger.Warn("failed to acquire distributed lock for lien execution status update",
				"payment_order_id", paymentOrderID,
				"error", "lock already held by another process")
			poobservability.RecordLienExecutionLockContention()
			return
		} else if lockErr != nil {
			// Unexpected lock error - log and continue without lock (optimistic locking will protect us)
			logger.Error("failed to obtain distributed lock for lien execution status update",
				"payment_order_id", paymentOrderID,
				"error", lockErr)
			// Continue without lock - optimistic locking still provides safety
		} else {
			// Lock acquired successfully - ensure it's released
			defer func() {
				if releaseErr := lock.Release(updateCtx); releaseErr != nil {
					logger.Error("failed to release distributed lock",
						"payment_order_id", paymentOrderID,
						"error", releaseErr)
				}
			}()
		}
	}

	for updateAttempt := 1; updateAttempt <= lienStatusUpdateMaxRetries; updateAttempt++ {
		// Apply exponential backoff for retries to reduce contention
		if updateAttempt > 1 {
			backoff := time.Duration(updateAttempt-1) * lienStatusUpdateBackoffBase
			select {
			case <-updateCtx.Done():
				logger.Error("context cancelled during update retry backoff",
					"update_attempt", updateAttempt)
				return
			case <-time.After(backoff):
			}
		}

		// Fetch the current payment order (fresh version)
		po, err := o.repo.FindByID(updateCtx, paymentOrderID) //nolint:contextcheck
		if err != nil {
			logger.Error("failed to fetch payment order for lien execution status update",
				"error", err,
				"update_attempt", updateAttempt)
			return
		}

		// Update lien execution tracking fields
		po.LienExecutionAttempts = totalLienAttempts

		// Determine error message if failed
		var errMsg string
		if retryErr != nil {
			// Prefer lastErr (the underlying error) over retryErr (the retry wrapper)
			if lastErr != nil {
				errMsg = lastErr.Error()
			} else {
				errMsg = retryErr.Error()
			}
		}

		// Set status on domain object
		if retryErr == nil {
			po.SetLienExecutionSucceeded()
		} else {
			po.SetLienExecutionFailed(errMsg)
		}

		// Persist the updated status
		updateErr := o.repo.Update(updateCtx, po) //nolint:contextcheck
		if updateErr == nil {
			// Record metrics only after successful persistence to avoid double-counting
			// on version conflict retries
			if retryErr == nil {
				logger.Info("lien execution completed successfully",
					"total_attempts", totalLienAttempts)
				poobservability.RecordLienExecution("success")
			} else {
				logger.Error("lien execution failed after all retries",
					"total_attempts", totalLienAttempts,
					"error", errMsg)
				poobservability.RecordLienExecution("failure")
				poobservability.RecordExternalServiceError("current_account", "execute_lien")
			}
			logger.Info("payment order lien execution status updated",
				"status", po.LienExecutionStatus,
				"attempts", po.LienExecutionAttempts)
			return
		}

		// Check if this is a version conflict (optimistic locking failure)
		if isVersionConflict(updateErr) {
			logger.Warn("version conflict updating lien execution status, retrying",
				"update_attempt", updateAttempt,
				"max_attempts", lienStatusUpdateMaxRetries)
			continue
		}

		// Non-recoverable error
		logger.Error("failed to update payment order lien execution status",
			"error", updateErr,
			"update_attempt", updateAttempt)
		return
	}

	// Log and record metric for exhausted retries - this will leave the payment order
	// in PENDING state which will be caught by the reconciliation query using the
	// idx_payment_orders_lien_execution partial index
	logger.Error("failed to update lien execution status after max retries due to version conflicts",
		"max_attempts", lienStatusUpdateMaxRetries,
		"payment_order_id", paymentOrderID.String())
	poobservability.RecordLienExecutionStatusUpdateExhausted()
}

// isVersionConflict checks if an error is a version conflict error
func isVersionConflict(err error) bool {
	return errors.Is(err, persistence.ErrPaymentOrderVersionConflict)
}

// PostLedgerEntriesFromParams creates double-entry bookkeeping entries using map params.
// This method is used by Starlark saga handlers that pass parameters as maps.
// It constructs a minimal PaymentOrder from the params and delegates to PostLedgerEntries.
//
// Required params:
//   - payment_order_id: string
//   - debtor_account_id: string
//   - gateway_reference_id: string
//   - amount_cents: int64
//   - currency: string
//   - idempotency_key: string
//
// Optional params:
//   - internal_clearing_enabled: bool (overrides orchestrator setting if present)
func (o *PaymentOrchestrator) PostLedgerEntriesFromParams(ctx context.Context, params map[string]any) (string, error) {
	// Extract required parameters
	paymentOrderIDStr, ok := params["payment_order_id"].(string)
	if !ok || paymentOrderIDStr == "" {
		return "", ErrMissingPaymentOrderID
	}
	paymentOrderID, err := uuid.Parse(paymentOrderIDStr)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrMissingPaymentOrderID, err)
	}

	debtorAccountID, ok := params["debtor_account_id"].(string)
	if !ok || debtorAccountID == "" {
		return "", ErrMissingDebtorAccountID
	}

	gatewayReferenceID, ok := params["gateway_reference_id"].(string)
	if !ok || gatewayReferenceID == "" {
		return "", ErrMissingGatewayReferenceID
	}

	amountCents, err := extractInt64Param(params, "amount_cents")
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrMissingAmountCents, err)
	}

	currency, ok := params["currency"].(string)
	if !ok || currency == "" {
		return "", ErrMissingCurrency
	}

	idempotencyKey, ok := params["idempotency_key"].(string)
	if !ok || idempotencyKey == "" {
		return "", ErrMissingIdempotencyKey
	}

	// Construct Money - NewMoney takes (currency, amountCents)
	amount, err := domain.NewMoney(currency, amountCents)
	if err != nil {
		return "", fmt.Errorf("invalid currency %s: %w", currency, err)
	}

	// Construct minimal PaymentOrder for PostLedgerEntries
	po := &domain.PaymentOrder{
		ID:                 paymentOrderID,
		DebtorAccountID:    debtorAccountID,
		GatewayReferenceID: gatewayReferenceID,
		Amount:             amount,
		IdempotencyKey:     idempotencyKey,
	}

	return o.PostLedgerEntries(ctx, po)
}

// extractInt64Param extracts an int64 from params, handling various numeric types.
func extractInt64Param(params map[string]any, key string) (int64, error) {
	val, ok := params[key]
	if !ok {
		return 0, ErrParamKeyNotFound
	}
	switch v := val.(type) {
	case int64:
		return v, nil
	case int:
		return int64(v), nil
	case float64:
		return int64(v), nil
	default:
		return 0, fmt.Errorf("%w: got %T", ErrParamInvalidType, val)
	}
}

// publishEvent publishes a Kafka event if the publisher is configured.
// This is best-effort/fire-and-forget: errors are logged but not retried or persisted.
func (o *PaymentOrchestrator) publishEvent(ctx context.Context, topic string, key string, event proto.Message) {
	if o.kafkaPublisher == nil {
		return
	}
	if err := o.kafkaPublisher.Publish(ctx, topic, key, event); err != nil {
		o.logger.Error("failed to publish event",
			"topic", topic,
			"key", key,
			"error", err)
	} else {
		o.logger.Info("published event",
			"topic", topic,
			"key", key)
	}
}
