// Package service implements gRPC services for the current account domain
package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/internal/current-account/clients"
	"github.com/meridianhub/meridian/internal/current-account/domain"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// DepositTransactionContext holds the context for a deposit transaction saga
type DepositTransactionContext struct {
	AccountID         string
	TransactionID     string
	Amount            domain.Money
	Description       string
	Reference         string
	CorrelationID     string
	Timestamp         time.Time
	Account           *domain.CurrentAccount
	PreDepositBalance domain.Money // Snapshot of balance before deposit for compensation
	PositionLogID     string
	LedgerPostingID   string
}

// orchestrateDeposit orchestrates a complete deposit transaction using saga pattern
// Flow: Update account balance → Log position → Post to ledger
func (s *Service) orchestrateDeposit(ctx context.Context, txCtx *DepositTransactionContext) error {
	saga := clients.NewSagaOrchestrator(s.logger)

	// Step 1: Update account balance in local database
	saga.AddStep(
		"update-account-balance",
		func(_ context.Context) error {
			s.logger.Info("updating account balance",
				"account_id", txCtx.AccountID,
				"transaction_id", txCtx.TransactionID,
				"amount_cents", txCtx.Amount.AmountCents(),
				"correlation_id", txCtx.CorrelationID)

			// Capture pre-deposit balance snapshot for deterministic compensation
			txCtx.PreDepositBalance = txCtx.Account.Balance

			if err := txCtx.Account.Deposit(txCtx.Amount); err != nil {
				return fmt.Errorf("deposit failed: %w", err)
			}

			if err := s.repo.Save(txCtx.Account); err != nil {
				return fmt.Errorf("failed to save account: %w", err)
			}

			return nil
		},
		func(_ context.Context) error {
			// Compensation: Restore pre-deposit balance snapshot
			s.logger.Warn("compensating account balance update",
				"account_id", txCtx.AccountID,
				"transaction_id", txCtx.TransactionID,
				"pre_deposit_balance_cents", txCtx.PreDepositBalance.AmountCents(),
				"correlation_id", txCtx.CorrelationID)

			// Reload account from database to get current state
			account, err := s.repo.FindByID(txCtx.AccountID)
			if err != nil {
				return fmt.Errorf("failed to reload account for compensation: %w", err)
			}

			// Calculate the amount to subtract (difference between current and pre-deposit balance)
			currentBalance := account.Balance
			balanceDiff, err := currentBalance.Subtract(txCtx.PreDepositBalance)
			if err != nil {
				return fmt.Errorf("failed to calculate balance difference: %w", err)
			}

			// Withdraw the difference to restore pre-deposit balance
			if err := account.Withdraw(balanceDiff); err != nil {
				return fmt.Errorf("compensation withdrawal failed: %w", err)
			}

			if err := s.repo.Save(account); err != nil {
				return fmt.Errorf("failed to save compensated account: %w", err)
			}

			return nil
		},
	)

	// Step 2: Log transaction to PositionKeeping service
	saga.AddStep(
		"log-position",
		func(stepCtx context.Context) error {
			s.logger.Info("logging position to position keeping service",
				"account_id", txCtx.AccountID,
				"transaction_id", txCtx.TransactionID,
				"correlation_id", txCtx.CorrelationID)

			// Propagate correlation ID
			stepCtx = PropagateCorrelationID(stepCtx, txCtx.CorrelationID)

			// Create transaction log entry
			entry := &positionkeepingv1.TransactionLogEntry{
				TransactionId: txCtx.TransactionID,
				AccountId:     txCtx.AccountID,
				Amount: &commonpb.MoneyAmount{
					Amount: &money.Money{
						CurrencyCode: txCtx.Amount.Currency(),
						Units:        txCtx.Amount.AmountCents() / 100,
						// #nosec G115 - remainder is always -99 to 99, multiplication result fits in int32
						Nanos: int32((txCtx.Amount.AmountCents() % 100) * 10000000),
					},
				},
				Direction:   commonpb.PostingDirection_POSTING_DIRECTION_DEBIT,
				Timestamp:   timestamppb.New(txCtx.Timestamp),
				Description: txCtx.Description,
				Reference:   txCtx.Reference,
			}

			req := &positionkeepingv1.InitiateFinancialPositionLogRequest{
				AccountId:    txCtx.AccountID,
				InitialEntry: entry,
			}

			resp, err := s.positionClient.InitiateFinancialPositionLog(stepCtx, req)
			if err != nil {
				return fmt.Errorf("failed to log position: %w", err)
			}

			txCtx.PositionLogID = resp.Log.LogId
			s.logger.Info("position logged successfully",
				"position_log_id", txCtx.PositionLogID,
				"correlation_id", txCtx.CorrelationID)

			return nil
		},
		nil, // No compensation - PositionKeeping service should handle idempotency
	)

	// Step 3: Post to FinancialAccounting ledger
	saga.AddStep(
		"post-to-ledger",
		func(stepCtx context.Context) error {
			s.logger.Info("posting to financial accounting ledger",
				"account_id", txCtx.AccountID,
				"transaction_id", txCtx.TransactionID,
				"correlation_id", txCtx.CorrelationID)

			// Propagate correlation ID
			stepCtx = PropagateCorrelationID(stepCtx, txCtx.CorrelationID)

			// Generate idempotency key for ledger posting
			idempotencyKey := &commonpb.IdempotencyKey{
				Key: fmt.Sprintf("%s-%s", txCtx.TransactionID, txCtx.AccountID),
			}

			req := &financialaccountingv1.CaptureLedgerPostingRequest{
				FinancialBookingLogId: txCtx.AccountID, // Use account ID as booking log ID for now
				PostingDirection:      commonpb.PostingDirection_POSTING_DIRECTION_DEBIT,
				PostingAmount: &money.Money{
					CurrencyCode: txCtx.Amount.Currency(),
					Units:        txCtx.Amount.AmountCents() / 100,
					// #nosec G115 - remainder is always -99 to 99, multiplication result fits in int32
					Nanos: int32((txCtx.Amount.AmountCents() % 100) * 10000000),
				},
				AccountId:      txCtx.AccountID,
				ValueDate:      timestamppb.New(txCtx.Timestamp),
				IdempotencyKey: idempotencyKey,
			}

			resp, err := s.accountingClient.CaptureLedgerPosting(stepCtx, req)
			if err != nil {
				return fmt.Errorf("failed to post to ledger: %w", err)
			}

			txCtx.LedgerPostingID = resp.LedgerPosting.Id
			s.logger.Info("ledger posting completed successfully",
				"posting_id", txCtx.LedgerPostingID,
				"correlation_id", txCtx.CorrelationID)

			return nil
		},
		nil, // No compensation - FinancialAccounting should handle idempotency
	)

	// Execute saga
	result := saga.Execute(ctx)

	if !result.Success {
		s.logger.Error("deposit transaction saga failed",
			"account_id", txCtx.AccountID,
			"transaction_id", txCtx.TransactionID,
			"failed_step", result.FailedStep,
			"completed_steps", result.CompletedSteps,
			"compensated_steps", result.CompensatedSteps,
			"correlation_id", txCtx.CorrelationID,
			"error", result.Error)
		return fmt.Errorf("transaction saga failed at step '%s': %w", result.FailedStep, result.Error)
	}

	s.logger.Info("deposit transaction saga completed successfully",
		"account_id", txCtx.AccountID,
		"transaction_id", txCtx.TransactionID,
		"completed_steps", result.CompletedSteps,
		"correlation_id", txCtx.CorrelationID)

	return nil
}

// generateTransactionID creates a new transaction ID
func generateTransactionID() string {
	return fmt.Sprintf("TXN-%s", uuid.New().String())
}
