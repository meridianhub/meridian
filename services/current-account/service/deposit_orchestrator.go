// Package service implements gRPC services for the current account domain
//
//nolint:staticcheck // Uses AmountCents() for logging (deprecated for backward compatibility)
package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/clients"
	"github.com/meridianhub/meridian/services/current-account/config"
	"github.com/meridianhub/meridian/services/current-account/domain"
	caobservability "github.com/meridianhub/meridian/services/current-account/observability"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/pkg/proto/mappers"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// DepositOrchestrator encapsulates deposit saga orchestration logic.
// It handles the multi-step deposit workflow including position keeping logging,
// ledger posting with double-entry bookkeeping, and account balance persistence.
type DepositOrchestrator struct {
	logger           *slog.Logger
	repo             *persistence.Repository
	posKeepingClient clients.PositionKeepingClient
	finAcctClient    clients.FinancialAccountingClient
	accountConfig    *config.AccountConfig
}

// DepositOrchestratorConfig contains dependencies for creating a DepositOrchestrator
type DepositOrchestratorConfig struct {
	Logger           *slog.Logger
	Repo             *persistence.Repository
	PosKeepingClient clients.PositionKeepingClient
	FinAcctClient    clients.FinancialAccountingClient
	AccountConfig    *config.AccountConfig
}

// NewDepositOrchestrator creates a new deposit orchestrator with the given dependencies.
// Panics if required dependencies (Logger, Repo, PosKeepingClient, FinAcctClient) are nil.
func NewDepositOrchestrator(cfg DepositOrchestratorConfig) *DepositOrchestrator {
	if cfg.Logger == nil {
		panic("deposit orchestrator: logger cannot be nil")
	}
	if cfg.Repo == nil {
		panic("deposit orchestrator: repository cannot be nil")
	}
	if cfg.PosKeepingClient == nil {
		panic("deposit orchestrator: position keeping client cannot be nil")
	}
	if cfg.FinAcctClient == nil {
		panic("deposit orchestrator: financial accounting client cannot be nil")
	}
	return &DepositOrchestrator{
		logger:           cfg.Logger,
		repo:             cfg.Repo,
		posKeepingClient: cfg.PosKeepingClient,
		finAcctClient:    cfg.FinAcctClient,
		accountConfig:    cfg.AccountConfig,
	}
}

// Orchestrate executes the deposit saga with compensation on failure.
//
// Saga Steps (executed strictly sequentially - no concurrent execution):
//  1. log_position: Create financial position log in PositionKeeping service
//  2. post_ledger: Create booking log and dual ledger postings in FinancialAccounting service
//  3. save_account: Persist updated account balance to database
//
// The saga uses the SagaOrchestrator which ensures steps run one at a time. Domain objects
// (account, amount) are safely shared across steps since only one step executes at a time.
//
// Compensation Order (LIFO - Last In, First Out):
//   - save_account fails → compensate post_ledger (reverse postings), then log_position
//   - post_ledger fails → compensate log_position only
//   - log_position fails → no compensation needed
//
// Thread Safety: This method is not thread-safe for concurrent calls with the same account.
// Callers must use optimistic locking (version field) or database-level locking when
// processing deposits for the same account concurrently. The repository layer enforces
// optimistic locking via ErrVersionConflict.
func (o *DepositOrchestrator) Orchestrate(ctx context.Context, account domain.CurrentAccount, amount domain.Money, transactionID string) (*pb.ExecuteDepositResponse, error) {
	sagaStart := time.Now()
	sagaStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordSagaDuration("deposit", sagaStatus, time.Since(sagaStart))
	}()

	// Extract or generate correlation ID
	correlationID := sharedclients.ExtractCorrelationID(ctx)
	if correlationID == "" {
		correlationID = uuid.New().String()
		o.logger.Info("generated new correlation ID", "correlation_id", correlationID)
		ctx = observability.WithCorrelationID(ctx, correlationID)
	} else {
		o.logger.Info("using existing correlation ID", "correlation_id", correlationID)
	}

	// Create saga orchestrator
	saga := sharedclients.NewSagaOrchestrator(o.logger)

	// Track state for compensation
	var positionLogID string
	var positionLogVersion int64
	var bookingLogID string
	var debitPostingID string
	var creditPostingID string
	var debitPosted bool
	var creditPosted bool

	// Get clearing account ID from config
	var clearingAccountID string
	if o.accountConfig != nil {
		clearingAccountID = o.accountConfig.DepositClearingAccountID
	}

	// Step 1: Log position in PositionKeeping service
	o.addLogPositionStep(saga, account, amount, transactionID, &positionLogID, &positionLogVersion)

	// Step 2: Post to ledger in FinancialAccounting service with double-entry bookkeeping
	o.addPostLedgerStep(saga, account, amount, transactionID, clearingAccountID,
		&bookingLogID, &debitPostingID, &creditPostingID, &debitPosted, &creditPosted)

	// Step 3: Save account to database
	o.addSaveAccountStep(saga, account, transactionID)

	// Execute saga
	o.logger.Info("executing deposit saga",
		"account_id", account.AccountID(),
		"transaction_id", transactionID,
		"correlation_id", correlationID,
		"steps", saga.StepCount())

	result := saga.Execute(ctx)

	// Handle saga result
	if !result.Success {
		sagaStatus = operationStatusFailed
		caobservability.RecordSagaFailure("deposit", result.FailedStep)

		o.logger.Error("deposit saga failed",
			"account_id", account.AccountID(),
			"transaction_id", transactionID,
			"failed_step", result.FailedStep,
			"completed_steps", result.CompletedSteps,
			"compensated_steps", result.CompensatedSteps,
			"error", result.Error)

		return nil, status.Errorf(codes.Internal,
			"deposit transaction failed at step %s: %v (compensated %d/%d steps)",
			result.FailedStep, result.Error, result.CompensatedSteps, result.CompletedSteps)
	}

	o.logger.Info("deposit saga completed successfully",
		"account_id", account.AccountID(),
		"transaction_id", transactionID,
		"correlation_id", correlationID,
		"completed_steps", result.CompletedSteps)

	// Return successful response
	return &pb.ExecuteDepositResponse{
		AccountId:        account.AccountID(),
		TransactionId:    transactionID,
		NewBalance:       toMoneyAmount(account.Balance()),
		AvailableBalance: toMoneyAmount(account.AvailableBalance()),
		Status:           pb.TransactionStatus_TRANSACTION_STATUS_COMPLETED,
	}, nil
}

// addLogPositionStep adds the log_position saga step.
func (o *DepositOrchestrator) addLogPositionStep(
	saga *sharedclients.SagaOrchestrator,
	account domain.CurrentAccount,
	amount domain.Money,
	transactionID string,
	positionLogID *string,
	positionLogVersion *int64,
) {
	saga.AddStep("log_position",
		// Action: Create position log entry
		func(stepCtx context.Context) error {
			o.logger.Info("executing log_position step",
				"account_id", account.AccountID(),
				"transaction_id", transactionID)

			stepCtx = sharedclients.PropagateCorrelationID(stepCtx)

			resp, err := o.posKeepingClient.InitiateFinancialPositionLog(stepCtx,
				&positionkeepingv1.InitiateFinancialPositionLogRequest{
					AccountId: account.AccountIdentification(),
					InitialEntry: &positionkeepingv1.TransactionLogEntry{
						EntryId:       uuid.New().String(),
						TransactionId: transactionID,
						AccountId:     account.AccountIdentification(),
						Amount:        toMoneyAmount(amount),
						Direction:     commonpb.PostingDirection_POSTING_DIRECTION_CREDIT,
						Timestamp:     timestamppb.Now(),
						Description:   fmt.Sprintf("Deposit to account %s", account.AccountID()),
					},
					IdempotencyKey: &commonpb.IdempotencyKey{
						Key: fmt.Sprintf("deposit-%s-%s", account.AccountID(), transactionID),
					},
				},
			)
			if err != nil {
				caobservability.RecordExternalServiceError("position_keeping", "initiate_log")
				return fmt.Errorf("failed to log position: %w", err)
			}
			if resp.Log == nil {
				caobservability.RecordExternalServiceError("position_keeping", "initiate_log")
				return fmt.Errorf("%w for transaction %s", ErrNilPositionLog, transactionID)
			}

			*positionLogID = resp.Log.LogId
			*positionLogVersion = resp.Log.Version

			o.logger.Info("log_position step completed",
				"position_log_id", *positionLogID,
				"position_log_version", *positionLogVersion,
				"transaction_id", transactionID)

			return nil
		},
		// Compensate: Mark position log as cancelled
		func(stepCtx context.Context) error {
			o.logger.Info("compensating log_position step",
				"position_log_id", *positionLogID,
				"position_log_version", *positionLogVersion,
				"transaction_id", transactionID)

			if *positionLogID == "" {
				o.logger.Warn("cannot compensate log_position: position log ID not captured")
				return ErrPositionLogIDNotFound
			}

			stepCtx = sharedclients.PropagateCorrelationID(stepCtx)

			_, err := o.posKeepingClient.UpdateFinancialPositionLog(stepCtx,
				&positionkeepingv1.UpdateFinancialPositionLogRequest{
					LogId:   *positionLogID,
					Version: *positionLogVersion,
					StatusUpdate: &positionkeepingv1.StatusTracking{
						CurrentStatus:   commonpb.TransactionStatus_TRANSACTION_STATUS_CANCELLED,
						StatusUpdatedAt: timestamppb.Now(),
						StatusReason:    fmt.Sprintf("Saga compensation for failed deposit transaction %s", transactionID),
					},
					AuditEntry: &positionkeepingv1.AuditTrailEntry{
						AuditId:   uuid.New().String(),
						Timestamp: timestamppb.Now(),
						UserId:    "system",
						Action:    "saga_compensation",
						Details:   fmt.Sprintf("Cancelled position log due to deposit saga failure for transaction %s", transactionID),
					},
					IdempotencyKey: &commonpb.IdempotencyKey{
						Key: fmt.Sprintf("compensate-deposit-%s-%s", account.AccountID(), transactionID),
					},
				},
			)
			if err != nil {
				caobservability.RecordExternalServiceError("position_keeping", "compensate_log")
				return fmt.Errorf("failed to compensate position log: %w", err)
			}

			caobservability.RecordSagaCompensation("deposit", "log_position")

			o.logger.Info("log_position compensation completed",
				"position_log_id", *positionLogID)

			return nil
		},
	)
}

// addPostLedgerStep adds the post_ledger saga step for double-entry bookkeeping.
func (o *DepositOrchestrator) addPostLedgerStep(
	saga *sharedclients.SagaOrchestrator,
	account domain.CurrentAccount,
	amount domain.Money,
	transactionID string,
	clearingAccountID string,
	bookingLogID, debitPostingID, creditPostingID *string,
	debitPosted, creditPosted *bool,
) {
	saga.AddStep("post_ledger",
		// Action: Create booking log and dual ledger postings
		func(stepCtx context.Context) error {
			o.logger.Info("executing post_ledger step",
				"account_id", account.AccountID(),
				"clearing_account_id", clearingAccountID,
				"transaction_id", transactionID)

			stepCtx = sharedclients.PropagateCorrelationID(stepCtx)
			moneyAmt := toMoneyAmount(amount)

			// Step 2a: Initiate a financial booking log
			bookingLogResp, err := o.finAcctClient.InitiateFinancialBookingLog(stepCtx,
				&financialaccountingv1.InitiateFinancialBookingLogRequest{
					FinancialAccountType:    commonpb.AccountType_ACCOUNT_TYPE_CURRENT,
					ProductServiceReference: account.AccountID(),
					BusinessUnitReference:   "current-account-service",
					ChartOfAccountsRules:    "DEPOSIT",
					BaseCurrency:            mappers.DomainCurrencyToProto(amount.Currency()),
					IdempotencyKey: &commonpb.IdempotencyKey{
						Key: fmt.Sprintf("booking-log-%s", transactionID),
					},
				},
			)
			if err != nil {
				caobservability.RecordExternalServiceError("financial_accounting", "initiate_booking_log")
				return fmt.Errorf("failed to initiate booking log: %w", err)
			}
			if bookingLogResp.FinancialBookingLog == nil {
				caobservability.RecordExternalServiceError("financial_accounting", "initiate_booking_log")
				return fmt.Errorf("%w for transaction %s", ErrNilBookingLog, transactionID)
			}
			*bookingLogID = bookingLogResp.FinancialBookingLog.Id

			o.logger.Info("booking log created",
				"booking_log_id", *bookingLogID,
				"transaction_id", transactionID)

			// Step 2b: Post DEBIT to clearing account (if configured)
			if clearingAccountID != "" {
				debitResp, err := o.finAcctClient.CaptureLedgerPosting(stepCtx,
					&financialaccountingv1.CaptureLedgerPostingRequest{
						FinancialBookingLogId: *bookingLogID,
						PostingDirection:      commonpb.PostingDirection_POSTING_DIRECTION_DEBIT,
						PostingAmount:         moneyAmt.Amount,
						AccountId:             clearingAccountID,
						ValueDate:             timestamppb.Now(),
						IdempotencyKey: &commonpb.IdempotencyKey{
							Key: fmt.Sprintf("%s-debit", transactionID),
						},
					},
				)
				if err != nil {
					caobservability.RecordExternalServiceError("financial_accounting", "capture_debit_posting")
					return fmt.Errorf("failed to post debit to clearing account: %w", err)
				}
				if debitResp.LedgerPosting == nil {
					caobservability.RecordExternalServiceError("financial_accounting", "capture_debit_posting")
					return fmt.Errorf("%w for transaction %s", ErrNilDebitPosting, transactionID)
				}
				*debitPostingID = debitResp.LedgerPosting.Id
				*debitPosted = true

				o.logger.Info("debit posting to clearing account completed",
					"debit_posting_id", *debitPostingID,
					"clearing_account_id", clearingAccountID,
					"booking_log_id", *bookingLogID,
					"transaction_id", transactionID)
			}

			// Step 2c: Post CREDIT to customer account
			creditResp, err := o.finAcctClient.CaptureLedgerPosting(stepCtx,
				&financialaccountingv1.CaptureLedgerPostingRequest{
					FinancialBookingLogId: *bookingLogID,
					PostingDirection:      commonpb.PostingDirection_POSTING_DIRECTION_CREDIT,
					PostingAmount:         moneyAmt.Amount,
					AccountId:             account.AccountID(),
					ValueDate:             timestamppb.Now(),
					IdempotencyKey: &commonpb.IdempotencyKey{
						Key: fmt.Sprintf("%s-credit", transactionID),
					},
				},
			)
			if err != nil {
				caobservability.RecordExternalServiceError("financial_accounting", "capture_credit_posting")
				// Inline compensation for debit if needed
				if *debitPosted {
					o.compensateDebitPosting(stepCtx, *bookingLogID, clearingAccountID, transactionID, moneyAmt)
				}
				return fmt.Errorf("failed to post credit to customer account: %w", err)
			}
			if creditResp.LedgerPosting == nil {
				caobservability.RecordExternalServiceError("financial_accounting", "capture_credit_posting")
				// Inline compensation for debit if needed
				if *debitPosted {
					o.compensateDebitPosting(stepCtx, *bookingLogID, clearingAccountID, transactionID, moneyAmt)
				}
				return fmt.Errorf("%w for transaction %s", ErrNilCreditPosting, transactionID)
			}
			*creditPostingID = creditResp.LedgerPosting.Id
			*creditPosted = true

			o.logger.Info("credit posting to customer account completed",
				"credit_posting_id", *creditPostingID,
				"account_id", account.AccountID(),
				"booking_log_id", *bookingLogID,
				"transaction_id", transactionID)

			// Step 2d: Transition BookingLog to POSTED
			_, err = o.finAcctClient.UpdateFinancialBookingLog(stepCtx,
				&financialaccountingv1.UpdateFinancialBookingLogRequest{
					Id:     *bookingLogID,
					Status: commonpb.TransactionStatus_TRANSACTION_STATUS_POSTED,
				},
			)
			if err != nil {
				caobservability.RecordExternalServiceError("financial_accounting", "update_booking_log")
				// Inline compensation for both postings
				o.compensatePostingsInline(stepCtx, account, *bookingLogID, clearingAccountID, transactionID, moneyAmt, *debitPosted, *creditPosted)
				return fmt.Errorf("failed to transition booking log to POSTED: %w", err)
			}

			o.logger.Info("post_ledger step completed",
				"debit_posting_id", *debitPostingID,
				"credit_posting_id", *creditPostingID,
				"booking_log_id", *bookingLogID,
				"transaction_id", transactionID)

			return nil
		},
		// Compensate: Reverse both ledger postings
		func(stepCtx context.Context) error {
			o.logger.Info("compensating post_ledger step",
				"debit_posting_id", *debitPostingID,
				"credit_posting_id", *creditPostingID,
				"booking_log_id", *bookingLogID,
				"transaction_id", transactionID)

			if *bookingLogID == "" {
				o.logger.Warn("cannot compensate post_ledger: booking log ID not captured")
				return ErrLedgerPostingIDNotFound
			}

			stepCtx = sharedclients.PropagateCorrelationID(stepCtx)
			moneyAmt := toMoneyAmount(amount)

			o.compensatePostingsInline(stepCtx, account, *bookingLogID, clearingAccountID, transactionID, moneyAmt, *debitPosted, *creditPosted)

			caobservability.RecordSagaCompensation("deposit", "post_ledger")

			o.logger.Info("post_ledger compensation completed",
				"debit_posting_id", *debitPostingID,
				"credit_posting_id", *creditPostingID,
				"booking_log_id", *bookingLogID)

			return nil
		},
	)
}

// compensateDebitPosting creates a compensating credit entry for a debit posting
// and transitions the BookingLog to CANCELLED. This is called when the credit posting
// fails after a successful debit posting.
func (o *DepositOrchestrator) compensateDebitPosting(
	ctx context.Context,
	bookingLogID, clearingAccountID, transactionID string,
	moneyAmt *commonpb.MoneyAmount,
) {
	// Create compensating credit to reverse the debit
	compDebitID := fmt.Sprintf("COMP-%s-debit", transactionID)
	_, err := o.finAcctClient.CaptureLedgerPosting(ctx,
		&financialaccountingv1.CaptureLedgerPostingRequest{
			FinancialBookingLogId: bookingLogID,
			PostingDirection:      commonpb.PostingDirection_POSTING_DIRECTION_CREDIT,
			PostingAmount:         moneyAmt.Amount,
			AccountId:             clearingAccountID,
			ValueDate:             timestamppb.Now(),
			IdempotencyKey:        &commonpb.IdempotencyKey{Key: compDebitID},
		},
	)
	if err != nil {
		// CRITICAL: Manual intervention required - ledger may be inconsistent.
		// Alert on metric: current_account_inline_compensation_failures_total
		o.logger.Error("CRITICAL: failed to compensate debit posting - manual ledger reconciliation required",
			"booking_log_id", bookingLogID,
			"clearing_account_id", clearingAccountID,
			"transaction_id", transactionID,
			"error", err,
			"runbook", "docs/runbooks/saga-failure-recovery.md")
		caobservability.RecordInlineCompensationFailure("deposit", "debit")
	}

	// Transition BookingLog to CANCELLED
	_, err = o.finAcctClient.UpdateFinancialBookingLog(ctx,
		&financialaccountingv1.UpdateFinancialBookingLogRequest{
			Id:     bookingLogID,
			Status: commonpb.TransactionStatus_TRANSACTION_STATUS_CANCELLED,
		},
	)
	if err != nil {
		o.logger.Warn("failed to transition booking log to CANCELLED after credit failure",
			"booking_log_id", bookingLogID,
			"transaction_id", transactionID,
			"error", err)
	} else {
		o.logger.Info("booking log transitioned to CANCELLED after credit failure",
			"booking_log_id", bookingLogID,
			"transaction_id", transactionID)
	}
}

// compensatePostingsInline creates compensating entries for both debit and credit postings.
func (o *DepositOrchestrator) compensatePostingsInline(
	ctx context.Context,
	account domain.CurrentAccount,
	bookingLogID, clearingAccountID, transactionID string,
	moneyAmt *commonpb.MoneyAmount,
	debitPosted, creditPosted bool,
) {
	// Compensate credit leg: Create DEBIT to customer account
	if creditPosted {
		compCreditID := fmt.Sprintf("COMP-%s-credit", transactionID)
		_, err := o.finAcctClient.CaptureLedgerPosting(ctx,
			&financialaccountingv1.CaptureLedgerPostingRequest{
				FinancialBookingLogId: bookingLogID,
				PostingDirection:      commonpb.PostingDirection_POSTING_DIRECTION_DEBIT,
				PostingAmount:         moneyAmt.Amount,
				AccountId:             account.AccountID(),
				ValueDate:             timestamppb.Now(),
				IdempotencyKey:        &commonpb.IdempotencyKey{Key: compCreditID},
			},
		)
		if err != nil {
			// CRITICAL: Manual intervention required - ledger may be inconsistent.
			// Alert on metric: current_account_inline_compensation_failures_total
			o.logger.Error("CRITICAL: failed to compensate credit posting - manual ledger reconciliation required",
				"booking_log_id", bookingLogID,
				"account_id", account.AccountID(),
				"transaction_id", transactionID,
				"error", err,
				"runbook", "docs/runbooks/saga-failure-recovery.md")
			caobservability.RecordInlineCompensationFailure("deposit", "credit")
		}
	}

	// Compensate debit leg: Create CREDIT to clearing account
	if debitPosted {
		compDebitID := fmt.Sprintf("COMP-%s-debit", transactionID)
		_, err := o.finAcctClient.CaptureLedgerPosting(ctx,
			&financialaccountingv1.CaptureLedgerPostingRequest{
				FinancialBookingLogId: bookingLogID,
				PostingDirection:      commonpb.PostingDirection_POSTING_DIRECTION_CREDIT,
				PostingAmount:         moneyAmt.Amount,
				AccountId:             clearingAccountID,
				ValueDate:             timestamppb.Now(),
				IdempotencyKey:        &commonpb.IdempotencyKey{Key: compDebitID},
			},
		)
		if err != nil {
			// CRITICAL: Manual intervention required - ledger may be inconsistent.
			// Alert on metric: current_account_inline_compensation_failures_total
			o.logger.Error("CRITICAL: failed to compensate debit posting - manual ledger reconciliation required",
				"booking_log_id", bookingLogID,
				"clearing_account_id", clearingAccountID,
				"transaction_id", transactionID,
				"error", err,
				"runbook", "docs/runbooks/saga-failure-recovery.md")
			caobservability.RecordInlineCompensationFailure("deposit", "debit")
		}
	}

	// Transition BookingLog to CANCELLED
	if debitPosted || creditPosted {
		_, err := o.finAcctClient.UpdateFinancialBookingLog(ctx,
			&financialaccountingv1.UpdateFinancialBookingLogRequest{
				Id:     bookingLogID,
				Status: commonpb.TransactionStatus_TRANSACTION_STATUS_CANCELLED,
			},
		)
		if err != nil {
			o.logger.Warn("failed to transition booking log to CANCELLED during inline compensation",
				"booking_log_id", bookingLogID,
				"transaction_id", transactionID,
				"error", err)
		} else {
			o.logger.Info("booking log transitioned to CANCELLED during inline compensation",
				"booking_log_id", bookingLogID,
				"transaction_id", transactionID)
		}
	}
}

// addSaveAccountStep adds the save_account saga step.
func (o *DepositOrchestrator) addSaveAccountStep(
	saga *sharedclients.SagaOrchestrator,
	account domain.CurrentAccount,
	transactionID string,
) {
	saga.AddStep("save_account",
		// Action: Persist the updated account balance
		func(stepCtx context.Context) error {
			o.logger.Info("executing save_account step",
				"account_id", account.AccountID(),
				"transaction_id", transactionID,
				"new_balance", account.Balance().AmountCents())

			if err := o.repo.Save(stepCtx, account); err != nil {
				return fmt.Errorf("failed to save account: %w", err)
			}

			o.logger.Info("save_account step completed",
				"account_id", account.AccountID(),
				"new_balance", account.Balance().AmountCents())

			return nil
		},
		// Compensate: No-op by design.
		// This step is the final step in the saga. If compensation is triggered:
		// - If save_account failed: The database write never happened, nothing to undo.
		// - save_account is never compensated after success because it's the last step;
		//   only steps before a failed step are compensated.
		// The saga ordering (log_position → post_ledger → save_account) ensures external
		// service calls complete before the local database write, so if save_account
		// fails, the external state is compensated, not the local state.
		func(_ context.Context) error {
			o.logger.Info("compensating save_account step (no-op by design)",
				"account_id", account.AccountID(),
				"reason", "save_account failed before database write completed - nothing to undo")
			return nil
		},
	)
}
