// Package service implements gRPC services for the payment order domain
//
//nolint:staticcheck // Uses AmountCents() for payment processing (deprecated for backward compatibility)
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

// PaymentOrchestrator encapsulates payment saga orchestration logic.
// It handles the multi-step payment workflow including fund reservation,
// gateway communication, ledger posting, and lien execution.
type PaymentOrchestrator struct {
	logger                    *slog.Logger
	repo                      persistence.Repository
	currentAccountClient      CurrentAccountClient
	paymentGateway            gateway.PaymentGateway
	financialAccountingClient FinancialAccountingClient
	gatewayAccountConfig      *config.GatewayAccountConfig
	kafkaPublisher            KafkaPublisher
	lienExecutionRetryConfig  *sharedclients.RetryConfig
}

// PaymentOrchestratorConfig contains dependencies for creating a PaymentOrchestrator
type PaymentOrchestratorConfig struct {
	Logger                    *slog.Logger
	Repo                      persistence.Repository
	CurrentAccountClient      CurrentAccountClient
	PaymentGateway            gateway.PaymentGateway
	FinancialAccountingClient FinancialAccountingClient
	GatewayAccountConfig      *config.GatewayAccountConfig
	KafkaPublisher            KafkaPublisher
	LienExecutionRetryConfig  *sharedclients.RetryConfig
}

// NewPaymentOrchestrator creates a new payment orchestrator with the given dependencies.
// Returns an error if required dependencies (Logger, Repo) are nil. CurrentAccountClient and
// PaymentGateway are validated at runtime in Orchestrate() with graceful error handling.
func NewPaymentOrchestrator(cfg PaymentOrchestratorConfig) (*PaymentOrchestrator, error) {
	if cfg.Logger == nil {
		return nil, ErrOrchestratorLoggerNil
	}
	if cfg.Repo == nil {
		return nil, ErrOrchestratorRepoNil
	}
	return &PaymentOrchestrator{
		logger:                    cfg.Logger,
		repo:                      cfg.Repo,
		currentAccountClient:      cfg.CurrentAccountClient,
		paymentGateway:            cfg.PaymentGateway,
		financialAccountingClient: cfg.FinancialAccountingClient,
		gatewayAccountConfig:      cfg.GatewayAccountConfig,
		kafkaPublisher:            cfg.KafkaPublisher,
		lienExecutionRetryConfig:  cfg.LienExecutionRetryConfig,
	}, nil
}

// Orchestrate executes the payment saga with compensation on failure.
// The saga steps (reserve_funds, send_to_gateway) are executed strictly sequentially by
// the SagaOrchestrator - there is no concurrent step execution. The same PaymentOrder
// pointer is safely shared across steps since only one step runs at a time.
// Compensation is also sequential, running in reverse order (LIFO) on failure.
func (o *PaymentOrchestrator) Orchestrate(ctx context.Context, po *domain.PaymentOrder) {
	o.logger.Info("starting payment saga",
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

	// Create saga orchestrator and track lien state for compensation
	saga := sharedclients.NewSagaOrchestrator(o.logger)
	var lienID string

	// Add saga steps
	o.addReserveFundsStep(saga, po, &lienID)
	o.addSendToGatewayStep(saga, po)

	// Execute saga
	result := saga.Execute(ctx)
	o.handleSagaResult(ctx, po, result)
}

// addReserveFundsStep adds the reserve_funds saga step that creates a lien to reserve funds.
func (o *PaymentOrchestrator) addReserveFundsStep(saga *sharedclients.SagaOrchestrator, po *domain.PaymentOrder, lienID *string) {
	saga.AddStep("reserve_funds",
		// Action: Create lien to reserve funds
		func(stepCtx context.Context) error {
			// Check context cancellation early to avoid unnecessary work
			if err := stepCtx.Err(); err != nil {
				return fmt.Errorf("context cancelled before reserve_funds: %w", err)
			}

			o.logger.Info("executing reserve_funds step",
				"payment_order_id", po.ID.String(),
				"debtor_account_id", po.DebtorAccountID)

			resp, err := o.currentAccountClient.InitiateLien(stepCtx, &currentaccountv1.InitiateLienRequest{
				AccountId:             po.DebtorAccountID,
				Amount:                toMoneyAmount(po.Amount),
				PaymentOrderReference: po.ID.String(),
			})
			if err != nil {
				return fmt.Errorf("failed to reserve funds: %w", err)
			}

			// Defensive check: ensure response is well-formed to avoid panics
			if resp == nil || resp.Lien == nil || resp.Lien.LienId == "" {
				return ErrMalformedLienResponse
			}

			*lienID = resp.Lien.LienId

			// Update payment order with lien ID and transition to RESERVED
			if err := po.Reserve(*lienID); err != nil {
				return fmt.Errorf("failed to transition to RESERVED: %w", err)
			}

			if err := o.repo.Update(stepCtx, po); err != nil {
				return fmt.Errorf("failed to update payment order: %w", err)
			}

			o.logger.Info("reserve_funds step completed",
				"payment_order_id", po.ID.String(),
				"lien_id", *lienID)

			// Publish PaymentOrderReserved event
			o.publishEvent(stepCtx, TopicPaymentOrderReserved, po.ID.String(), &eventsv1.PaymentOrderReservedEvent{
				EventId:         uuid.New().String(),
				PaymentOrderId:  po.ID.String(),
				DebtorAccountId: po.DebtorAccountID,
				LienId:          *lienID,
				Amount:          toMoneyAmount(po.Amount),
				CorrelationId:   po.CorrelationID,
				CausationId:     po.ID.String(),
				Timestamp:       timestamppb.Now(),
				Version:         int64(po.Version),
				IdempotencyKey:  po.IdempotencyKey,
			})

			return nil
		},
		// Compensate: Release lien
		func(stepCtx context.Context) error {
			if *lienID == "" {
				o.logger.Warn("no lien to release in compensation")
				return nil
			}

			o.logger.Info("compensating reserve_funds step",
				"payment_order_id", po.ID.String(),
				"lien_id", *lienID)

			_, err := o.currentAccountClient.TerminateLien(stepCtx, &currentaccountv1.TerminateLienRequest{
				LienId: *lienID,
				Reason: fmt.Sprintf("Payment order %s saga compensation", po.ID.String()),
			})
			if err != nil {
				o.logger.Error("failed to release lien in compensation",
					"error", err,
					"lien_id", *lienID)
				return err
			}

			o.logger.Info("reserve_funds compensation completed",
				"lien_id", *lienID)

			return nil
		},
	)
}

// addSendToGatewayStep adds the send_to_gateway saga step that sends payment to the external gateway.
func (o *PaymentOrchestrator) addSendToGatewayStep(saga *sharedclients.SagaOrchestrator, po *domain.PaymentOrder) {
	saga.AddStep("send_to_gateway",
		// Action: Send payment to gateway
		func(stepCtx context.Context) error {
			// Check context cancellation early to avoid unnecessary work
			if err := stepCtx.Err(); err != nil {
				return fmt.Errorf("context cancelled before send_to_gateway: %w", err)
			}

			o.logger.Info("executing send_to_gateway step",
				"payment_order_id", po.ID.String())

			resp, err := o.paymentGateway.SendPayment(stepCtx, gateway.PaymentRequest{
				PaymentOrderID:    po.ID,
				DebtorAccountID:   po.DebtorAccountID,
				CreditorReference: po.CreditorReference,
				Amount:            po.Amount,
				IdempotencyKey:    po.IdempotencyKey,
			})
			if err != nil {
				return fmt.Errorf("failed to send payment to gateway: %w", err)
			}

			return o.processGatewayResponse(stepCtx, po, resp)
		},
		// Compensate: No-op (lien will be released by reserve_funds compensation)
		func(_ context.Context) error {
			o.logger.Info("send_to_gateway compensation (no-op - lien released by reserve_funds compensation)",
				"payment_order_id", po.ID.String())
			return nil
		},
	)
}

// processGatewayResponse handles the gateway response and transitions payment order state.
func (o *PaymentOrchestrator) processGatewayResponse(ctx context.Context, po *domain.PaymentOrder, resp gateway.PaymentResponse) error {
	switch resp.Status {
	case gateway.StatusAccepted, gateway.StatusPending:
		// Transition to EXECUTING
		if err := po.Execute(resp.GatewayReferenceID); err != nil {
			return fmt.Errorf("failed to transition to EXECUTING: %w", err)
		}

		if err := o.repo.Update(ctx, po); err != nil {
			return fmt.Errorf("failed to update payment order: %w", err)
		}

		o.logger.Info("send_to_gateway step completed",
			"payment_order_id", po.ID.String(),
			"gateway_reference_id", resp.GatewayReferenceID,
			"gateway_status", resp.Status)

		// Publish PaymentOrderExecuting event
		o.publishEvent(ctx, TopicPaymentOrderExecuting, po.ID.String(), &eventsv1.PaymentOrderExecutingEvent{
			EventId:            uuid.New().String(),
			PaymentOrderId:     po.ID.String(),
			GatewayReferenceId: resp.GatewayReferenceID,
			CorrelationId:      po.CorrelationID,
			CausationId:        po.ID.String(),
			Timestamp:          timestamppb.Now(),
			Version:            int64(po.Version),
			IdempotencyKey:     po.IdempotencyKey,
		})

		return nil

	case gateway.StatusRejected:
		return fmt.Errorf("%w: %s", ErrPaymentRejected, resp.Message)

	default:
		return fmt.Errorf("%w: %s", ErrUnexpectedGatewayStatus, resp.Status)
	}
}

// handleSagaResult processes the saga execution result and handles failure scenarios.
func (o *PaymentOrchestrator) handleSagaResult(ctx context.Context, po *domain.PaymentOrder, result sharedclients.SagaResult) {
	if !result.Success {
		o.logger.Error("payment saga failed",
			"payment_order_id", po.ID.String(),
			"failed_step", result.FailedStep,
			"error", result.Error,
			"completed_steps", result.CompletedSteps,
			"compensated_steps", result.CompensatedSteps)

		// Reload payment order to get latest state
		latestPO, err := o.repo.FindByID(ctx, po.ID)
		if err != nil {
			o.logger.Error("failed to reload payment order for failure handling", "error", err)
			return
		}

		// Async path: log and swallow error - best effort failure handling
		if err := o.failPaymentOrder(ctx, latestPO, result.Error.Error(), "SAGA_FAILED"); err != nil {
			o.logger.Error("failed to mark payment order as failed after saga failure",
				"payment_order_id", po.ID.String(),
				"error", err)
		}
		return
	}

	o.logger.Info("payment saga completed successfully",
		"payment_order_id", po.ID.String(),
		"completed_steps", result.CompletedSteps)

	// Note: The payment is now in EXECUTING state, awaiting async gateway callback
	// via UpdatePaymentOrder to transition to COMPLETED or FAILED
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
// Double-entry accounting for outbound payments:
//   - DEBIT: Customer's account (funds leaving their account)
//   - CREDIT: Gateway's contra-account (liability to payment processor)
//
// Atomicity considerations:
// This function makes 4 sequential gRPC calls to the FinancialAccounting service:
//  1. InitiateFinancialBookingLog (creates BookingLog in PENDING)
//  2. CaptureLedgerPosting (DEBIT entry)
//  3. CaptureLedgerPosting (CREDIT entry)
//  4. UpdateFinancialBookingLog (marks as POSTED)
//
// Partial failure scenarios (documented for operational runbooks):
//   - Step 1 fails: No orphaned state - safe to retry
//   - Step 2 fails: BookingLog in PENDING, no postings - needs cleanup
//   - Step 3 fails: BookingLog in PENDING, unbalanced (debit only) - needs reversal
//   - Step 4 fails: BookingLog in PENDING, balanced entries exist - just needs status update
//
// All partial failures are logged with RECONCILIATION_REQUIRED prefix and include
// the booking_log_id for manual resolution. See runbook: docs/runbooks/saga-failure-recovery.md
//
// Error handling: If any step fails, the error is returned and the calling code
// should mark the payment as FAILED. The BookingLog will remain in PENDING status
// for reconciliation purposes.
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
	protoCurrency := mappers.DomainCurrencyToProto(po.Amount.Currency())
	if protoCurrency == commonpb.Currency_CURRENCY_UNSPECIFIED {
		o.logger.Warn("unsupported currency for ledger posting - payment will be marked as failed",
			"currency", string(po.Amount.Currency()),
			"payment_order_id", po.ID.String(),
			"supported_currencies", "GBP, USD, EUR")
		return "", fmt.Errorf("%w: %s", ErrUnsupportedCurrency, po.Amount.Currency())
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
	postingAmount := &money.Money{
		CurrencyCode: string(po.Amount.Currency()),
		Units:        po.Amount.AmountCents() / 100,
		Nanos:        int32((po.Amount.AmountCents() % 100) * 10000000),
	}
	valueDate := timestamppb.Now()

	// Step 2: Create DEBIT posting (customer account - funds leaving)
	debitIdempKey := fmt.Sprintf("debit-%s", po.IdempotencyKey)
	_, err = o.financialAccountingClient.CaptureLedgerPosting(ctx, &financialaccountingv1.CaptureLedgerPostingRequest{
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
			"failed_step", "debit_posting",
			"debtor_account", po.DebtorAccountID,
			"error", err.Error())
		return "", fmt.Errorf("failed to create debit posting for account %s: %w", po.DebtorAccountID, err)
	}

	o.logger.Debug("created debit posting",
		"booking_log_id", bookingLogID,
		"account_id", po.DebtorAccountID,
		"amount_cents", po.Amount.AmountCents(),
		"payment_order_id", po.ID.String())

	// Step 3: Create CREDIT posting (gateway contra-account - liability to processor)
	creditIdempKey := fmt.Sprintf("credit-%s", po.IdempotencyKey)
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
			"failed_step", "credit_posting",
			"debtor_account", po.DebtorAccountID,
			"contra_account", contraAccountID,
			"has_debit_posting", true,
			"error", err.Error())
		return "", fmt.Errorf("failed to create credit posting for account %s: %w", contraAccountID, err)
	}

	o.logger.Debug("created credit posting",
		"booking_log_id", bookingLogID,
		"account_id", contraAccountID,
		"amount_cents", po.Amount.AmountCents(),
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
			"has_debit_posting", true,
			"has_credit_posting", true,
			"resolution", "manually update booking log status to POSTED",
			"error", err.Error())
		return "", fmt.Errorf("failed to update booking log to POSTED: %w", err)
	}

	o.logger.Info("ledger posting completed successfully",
		"booking_log_id", bookingLogID,
		"payment_order_id", po.ID.String(),
		"debtor_account", po.DebtorAccountID,
		"contra_account", contraAccountID,
		"amount_cents", po.Amount.AmountCents(),
		"currency", string(po.Amount.Currency()))

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
			MaxInterval:         30 * time.Second,
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
// Uses optimistic locking with retry on version conflict to handle concurrent updates.
// Note: Uses a fresh context to ensure the status update completes even if the parent context has timed out.
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
