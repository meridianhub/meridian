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
	"github.com/meridianhub/meridian/services/current-account/config"
	"github.com/meridianhub/meridian/services/current-account/domain"
	caobservability "github.com/meridianhub/meridian/services/current-account/observability"
	"github.com/meridianhub/meridian/shared/domain/money"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/pkg/proto/mappers"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// WithdrawalOrchestrator encapsulates withdrawal saga orchestration logic.
// It handles the multi-step withdrawal workflow including position keeping logging,
// ledger posting with double-entry bookkeeping (reversed from deposits), and account balance persistence.
type WithdrawalOrchestrator struct {
	logger           *slog.Logger
	repo             *persistence.Repository
	posKeepingClient PositionKeepingClient
	finAcctClient    FinancialAccountingClient
	accountConfig    *config.AccountConfig
}

// WithdrawalOrchestratorConfig contains dependencies for creating a WithdrawalOrchestrator
type WithdrawalOrchestratorConfig struct {
	Logger           *slog.Logger
	Repo             *persistence.Repository
	PosKeepingClient PositionKeepingClient
	FinAcctClient    FinancialAccountingClient
	AccountConfig    *config.AccountConfig
}

// NewWithdrawalOrchestrator creates a new withdrawal orchestrator with the given dependencies.
// Returns an error if required dependencies (Logger, Repo, PosKeepingClient, FinAcctClient) are nil.
func NewWithdrawalOrchestrator(cfg WithdrawalOrchestratorConfig) (*WithdrawalOrchestrator, error) {
	if cfg.Logger == nil {
		return nil, ErrOrchestratorLoggerNil
	}
	if cfg.Repo == nil {
		return nil, ErrOrchestratorRepositoryNil
	}
	if cfg.PosKeepingClient == nil {
		return nil, ErrOrchestratorPosKeepingClientNil
	}
	if cfg.FinAcctClient == nil {
		return nil, ErrOrchestratorFinAcctClientNil
	}
	return &WithdrawalOrchestrator{
		logger:           cfg.Logger,
		repo:             cfg.Repo,
		posKeepingClient: cfg.PosKeepingClient,
		finAcctClient:    cfg.FinAcctClient,
		accountConfig:    cfg.AccountConfig,
	}, nil
}

// Orchestrate executes the withdrawal saga with compensation on failure.
//
// Saga Steps (executed strictly sequentially - no concurrent execution):
//  1. log_position: Create DEBIT entry in PositionKeeping service (balance source of truth)
//  2. post_ledger: Create booking log and dual ledger postings in FinancialAccounting service
//     (Customer account DEBIT, Bank cash account CREDIT - reversed from deposit)
//  3. save_account: Persist account metadata (status, version) - balance NOT stored locally
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
// processing withdrawals for the same account concurrently. The repository layer enforces
// optimistic locking via ErrVersionConflict.
func (o *WithdrawalOrchestrator) Orchestrate(ctx context.Context, account domain.CurrentAccount, amount domain.Money, transactionID string) (*pb.ExecuteWithdrawalResponse, error) {
	sagaStart := time.Now()
	sagaStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordSagaDuration("withdrawal", sagaStatus, time.Since(sagaStart))
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

	// Get clearing account ID from config (for withdrawals, this is the bank cash account)
	var withdrawalClearingAccountID string
	if o.accountConfig != nil {
		withdrawalClearingAccountID = o.accountConfig.WithdrawalClearingAccountID
	}

	// Step 1: Log position in PositionKeeping service with DEBIT direction (opposite of deposit)
	o.addLogPositionStep(saga, account, amount, transactionID, &positionLogID, &positionLogVersion)

	// Step 2: Post to ledger in FinancialAccounting service with double-entry bookkeeping
	// For withdrawal: DEBIT customer account, CREDIT clearing account (reversed from deposit)
	o.addPostLedgerStep(saga, account, amount, transactionID, withdrawalClearingAccountID,
		&bookingLogID, &debitPostingID, &creditPostingID, &debitPosted, &creditPosted)

	// Step 3: Save account to database
	o.addSaveAccountStep(saga, account, transactionID)

	// Execute saga
	o.logger.Info("executing withdrawal saga",
		"account_id", account.AccountID(),
		"transaction_id", transactionID,
		"correlation_id", correlationID,
		"steps", saga.StepCount())

	result := saga.Execute(ctx)

	// Handle saga result
	if !result.Success {
		sagaStatus = operationStatusFailed
		caobservability.RecordSagaFailure("withdrawal", result.FailedStep)

		o.logger.Error("withdrawal saga failed",
			"account_id", account.AccountID(),
			"transaction_id", transactionID,
			"failed_step", result.FailedStep,
			"completed_steps", result.CompletedSteps,
			"compensated_steps", result.CompensatedSteps,
			"error", result.Error)

		return nil, status.Errorf(codes.Internal,
			"withdrawal transaction failed at step %s: %v (compensated %d/%d steps)",
			result.FailedStep, result.Error, result.CompensatedSteps, result.CompletedSteps)
	}

	o.logger.Info("withdrawal saga completed successfully",
		"account_id", account.AccountID(),
		"transaction_id", transactionID,
		"correlation_id", correlationID,
		"completed_steps", result.CompletedSteps)

	// Return successful response
	return &pb.ExecuteWithdrawalResponse{
		AccountId:        account.AccountID(),
		TransactionId:    transactionID,
		NewBalance:       toMoneyAmount(account.Balance()),
		AvailableBalance: toMoneyAmount(account.AvailableBalance()),
		Status:           pb.WithdrawalStatus_WITHDRAWAL_STATUS_COMPLETED,
		Timestamp:        timestamppb.Now(),
	}, nil
}

// addLogPositionStep adds the log_position saga step for withdrawals.
// Key difference from deposit: Uses DEBIT direction instead of CREDIT.
func (o *WithdrawalOrchestrator) addLogPositionStep(
	saga *sharedclients.SagaOrchestrator,
	account domain.CurrentAccount,
	amount domain.Money,
	transactionID string,
	positionLogID *string,
	positionLogVersion *int64,
) {
	saga.AddStep("log_position",
		// Action: Create position log entry with DEBIT direction
		func(stepCtx context.Context) error {
			o.logger.Info("executing log_position step for withdrawal",
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
						Direction:     commonpb.PostingDirection_POSTING_DIRECTION_DEBIT, // DEBIT for withdrawal (opposite of deposit)
						Timestamp:     timestamppb.Now(),
						Description:   fmt.Sprintf("Withdrawal from account %s", account.AccountID()),
					},
					IdempotencyKey: &commonpb.IdempotencyKey{
						Key: fmt.Sprintf("withdrawal-%s-%s", account.AccountID(), transactionID),
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

			o.logger.Info("log_position step completed for withdrawal",
				"position_log_id", *positionLogID,
				"position_log_version", *positionLogVersion,
				"transaction_id", transactionID)

			return nil
		},
		// Compensate: Mark position log as cancelled
		func(stepCtx context.Context) error {
			o.logger.Info("compensating log_position step for withdrawal",
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
						StatusReason:    fmt.Sprintf("Saga compensation for failed withdrawal transaction %s", transactionID),
					},
					AuditEntry: &positionkeepingv1.AuditTrailEntry{
						AuditId:   uuid.New().String(),
						Timestamp: timestamppb.Now(),
						UserId:    "system",
						Action:    "saga_compensation",
						Details:   fmt.Sprintf("Cancelled position log due to withdrawal saga failure for transaction %s", transactionID),
					},
					IdempotencyKey: &commonpb.IdempotencyKey{
						Key: fmt.Sprintf("compensate-withdrawal-%s-%s", account.AccountID(), transactionID),
					},
				},
			)
			if err != nil {
				caobservability.RecordExternalServiceError("position_keeping", "compensate_log")
				return fmt.Errorf("failed to compensate position log: %w", err)
			}

			caobservability.RecordSagaCompensation("withdrawal", "log_position")

			o.logger.Info("log_position compensation completed for withdrawal",
				"position_log_id", *positionLogID)

			return nil
		},
	)
}

// addPostLedgerStep adds the post_ledger saga step for double-entry bookkeeping.
// For withdrawals: DEBIT customer account, CREDIT clearing account (reversed from deposit)
func (o *WithdrawalOrchestrator) addPostLedgerStep(
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
			o.logger.Info("executing post_ledger step for withdrawal",
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
					ChartOfAccountsRules:    "WITHDRAWAL",
					BaseCurrency:            mappers.DomainCurrencyToProto(money.Currency(amount.Currency())),
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

			o.logger.Info("booking log created for withdrawal",
				"booking_log_id", *bookingLogID,
				"transaction_id", transactionID)

			// Step 2b: Post DEBIT to customer account (withdrawal reduces customer balance)
			debitResp, err := o.finAcctClient.CaptureLedgerPosting(stepCtx,
				&financialaccountingv1.CaptureLedgerPostingRequest{
					FinancialBookingLogId: *bookingLogID,
					PostingDirection:      commonpb.PostingDirection_POSTING_DIRECTION_DEBIT,
					PostingAmount:         moneyAmt.Amount,
					AccountId:             account.AccountID(),
					ValueDate:             timestamppb.Now(),
					IdempotencyKey: &commonpb.IdempotencyKey{
						Key: fmt.Sprintf("%s-debit", transactionID),
					},
				},
			)
			if err != nil {
				caobservability.RecordExternalServiceError("financial_accounting", "capture_debit_posting")
				return fmt.Errorf("failed to post debit to customer account: %w", err)
			}
			if debitResp.LedgerPosting == nil {
				caobservability.RecordExternalServiceError("financial_accounting", "capture_debit_posting")
				return fmt.Errorf("%w for transaction %s", ErrNilDebitPosting, transactionID)
			}
			*debitPostingID = debitResp.LedgerPosting.Id
			*debitPosted = true

			o.logger.Info("debit posting to customer account completed for withdrawal",
				"debit_posting_id", *debitPostingID,
				"account_id", account.AccountID(),
				"booking_log_id", *bookingLogID,
				"transaction_id", transactionID)

			// Step 2c: Post CREDIT to clearing account (if configured)
			if clearingAccountID != "" {
				creditResp, err := o.finAcctClient.CaptureLedgerPosting(stepCtx,
					&financialaccountingv1.CaptureLedgerPostingRequest{
						FinancialBookingLogId: *bookingLogID,
						PostingDirection:      commonpb.PostingDirection_POSTING_DIRECTION_CREDIT,
						PostingAmount:         moneyAmt.Amount,
						AccountId:             clearingAccountID,
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
						o.compensateDebitPosting(stepCtx, account, *bookingLogID, transactionID, moneyAmt)
						*debitPosted = false // Already compensated inline
					}
					return fmt.Errorf("failed to post credit to clearing account: %w", err)
				}
				if creditResp.LedgerPosting == nil {
					caobservability.RecordExternalServiceError("financial_accounting", "capture_credit_posting")
					// Inline compensation for debit if needed
					if *debitPosted {
						o.compensateDebitPosting(stepCtx, account, *bookingLogID, transactionID, moneyAmt)
						*debitPosted = false // Already compensated inline
					}
					return fmt.Errorf("%w for transaction %s", ErrNilCreditPosting, transactionID)
				}
				*creditPostingID = creditResp.LedgerPosting.Id
				*creditPosted = true

				o.logger.Info("credit posting to clearing account completed for withdrawal",
					"credit_posting_id", *creditPostingID,
					"clearing_account_id", clearingAccountID,
					"booking_log_id", *bookingLogID,
					"transaction_id", transactionID)
			}

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
				_ = o.compensatePostingsInline(stepCtx, account, *bookingLogID, clearingAccountID, transactionID, moneyAmt, *debitPosted, *creditPosted)
				*debitPosted = false  // Already compensated inline
				*creditPosted = false // Already compensated inline
				return fmt.Errorf("failed to transition booking log to POSTED: %w", err)
			}

			o.logger.Info("post_ledger step completed for withdrawal",
				"debit_posting_id", *debitPostingID,
				"credit_posting_id", *creditPostingID,
				"booking_log_id", *bookingLogID,
				"transaction_id", transactionID)

			return nil
		},
		// Compensate: Reverse both ledger postings
		func(stepCtx context.Context) error {
			o.logger.Info("compensating post_ledger step for withdrawal",
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

			if err := o.compensatePostingsInline(stepCtx, account, *bookingLogID, clearingAccountID, transactionID, moneyAmt, *debitPosted, *creditPosted); err != nil {
				return fmt.Errorf("post_ledger compensation failed: %w", err)
			}

			caobservability.RecordSagaCompensation("withdrawal", "post_ledger")

			o.logger.Info("post_ledger compensation completed for withdrawal",
				"debit_posting_id", *debitPostingID,
				"credit_posting_id", *creditPostingID,
				"booking_log_id", *bookingLogID)

			return nil
		},
	)
}

// compensateDebitPosting creates a compensating credit entry for a debit posting
// and transitions the BookingLog to CANCELLED.
// For withdrawals: compensates customer account DEBIT with a CREDIT.
func (o *WithdrawalOrchestrator) compensateDebitPosting(
	ctx context.Context,
	account domain.CurrentAccount,
	bookingLogID, transactionID string,
	moneyAmt *commonpb.MoneyAmount,
) {
	// Create compensating credit to reverse the debit (credit the customer account back)
	compDebitID := fmt.Sprintf("COMP-%s-debit", transactionID)
	_, err := o.finAcctClient.CaptureLedgerPosting(ctx,
		&financialaccountingv1.CaptureLedgerPostingRequest{
			FinancialBookingLogId: bookingLogID,
			PostingDirection:      commonpb.PostingDirection_POSTING_DIRECTION_CREDIT, // Reverse: credit to compensate debit
			PostingAmount:         moneyAmt.Amount,
			AccountId:             account.AccountID(),
			ValueDate:             timestamppb.Now(),
			IdempotencyKey:        &commonpb.IdempotencyKey{Key: compDebitID},
		},
	)
	if err != nil {
		o.logger.Error("CRITICAL: failed to compensate debit posting - manual ledger reconciliation required",
			"booking_log_id", bookingLogID,
			"account_id", account.AccountID(),
			"transaction_id", transactionID,
			"error", err,
			"runbook", "docs/runbooks/saga-failure-recovery.md")
		caobservability.RecordInlineCompensationFailure("withdrawal", "debit")
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
// For withdrawals: reverses customer DEBIT with CREDIT, reverses clearing CREDIT with DEBIT.
func (o *WithdrawalOrchestrator) compensatePostingsInline(
	ctx context.Context,
	account domain.CurrentAccount,
	bookingLogID, clearingAccountID, transactionID string,
	moneyAmt *commonpb.MoneyAmount,
	debitPosted, creditPosted bool,
) error {
	var compensationErrors []error

	// Compensate debit leg: Create CREDIT to customer account (reverse the debit)
	if debitPosted {
		compDebitID := fmt.Sprintf("COMP-%s-debit", transactionID)
		_, err := o.finAcctClient.CaptureLedgerPosting(ctx,
			&financialaccountingv1.CaptureLedgerPostingRequest{
				FinancialBookingLogId: bookingLogID,
				PostingDirection:      commonpb.PostingDirection_POSTING_DIRECTION_CREDIT,
				PostingAmount:         moneyAmt.Amount,
				AccountId:             account.AccountID(),
				ValueDate:             timestamppb.Now(),
				IdempotencyKey:        &commonpb.IdempotencyKey{Key: compDebitID},
			},
		)
		if err != nil {
			o.logger.Error("CRITICAL: failed to compensate debit posting - manual ledger reconciliation required",
				"booking_log_id", bookingLogID,
				"account_id", account.AccountID(),
				"transaction_id", transactionID,
				"error", err,
				"runbook", "docs/runbooks/saga-failure-recovery.md")
			caobservability.RecordInlineCompensationFailure("withdrawal", "debit")
			compensationErrors = append(compensationErrors, fmt.Errorf("debit compensation failed: %w", err))
		}
	}

	// Compensate credit leg: Create DEBIT to clearing account (reverse the credit)
	if creditPosted && clearingAccountID != "" {
		compCreditID := fmt.Sprintf("COMP-%s-credit", transactionID)
		_, err := o.finAcctClient.CaptureLedgerPosting(ctx,
			&financialaccountingv1.CaptureLedgerPostingRequest{
				FinancialBookingLogId: bookingLogID,
				PostingDirection:      commonpb.PostingDirection_POSTING_DIRECTION_DEBIT,
				PostingAmount:         moneyAmt.Amount,
				AccountId:             clearingAccountID,
				ValueDate:             timestamppb.Now(),
				IdempotencyKey:        &commonpb.IdempotencyKey{Key: compCreditID},
			},
		)
		if err != nil {
			o.logger.Error("CRITICAL: failed to compensate credit posting - manual ledger reconciliation required",
				"booking_log_id", bookingLogID,
				"clearing_account_id", clearingAccountID,
				"transaction_id", transactionID,
				"error", err,
				"runbook", "docs/runbooks/saga-failure-recovery.md")
			caobservability.RecordInlineCompensationFailure("withdrawal", "credit")
			compensationErrors = append(compensationErrors, fmt.Errorf("credit compensation failed: %w", err))
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

	if len(compensationErrors) > 0 {
		return fmt.Errorf("%w: %d errors occurred", ErrCompensationFailed, len(compensationErrors))
	}
	return nil
}

// addSaveAccountStep adds the save_account saga step.
func (o *WithdrawalOrchestrator) addSaveAccountStep(
	saga *sharedclients.SagaOrchestrator,
	account domain.CurrentAccount,
	transactionID string,
) {
	saga.AddStep("save_account",
		// Action: Persist account metadata (status, version for optimistic locking).
		// Note: Balance is NOT persisted locally - Position Keeping is the source of truth.
		// The repository's Save method excludes balance fields from persistence.
		func(stepCtx context.Context) error {
			o.logger.Info("executing save_account step for withdrawal",
				"account_id", account.AccountID(),
				"transaction_id", transactionID,
				"version", account.Version())

			if err := o.repo.Save(stepCtx, account); err != nil {
				return fmt.Errorf("failed to save account: %w", err)
			}

			o.logger.Info("save_account step completed for withdrawal",
				"account_id", account.AccountID(),
				"version", account.Version())

			return nil
		},
		// Compensate: No-op by design.
		// This step is the final step in the saga. If compensation is triggered:
		// - If save_account failed: The database write never happened, nothing to undo.
		// - save_account is never compensated after success because it's the last step;
		//   only steps before a failed step are compensated.
		func(_ context.Context) error {
			o.logger.Info("compensating save_account step (no-op by design) for withdrawal",
				"account_id", account.AccountID(),
				"reason", "save_account failed before database write completed - nothing to undo")
			return nil
		},
	)
}
