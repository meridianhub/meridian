// Package service implements gRPC services for the current account domain
package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/internal/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/internal/current-account/domain"
	caobservability "github.com/meridianhub/meridian/internal/current-account/observability"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// Lien-specific errors
var (
	ErrLienRepositoryNil     = errors.New("lien repository cannot be nil")
	ErrInsufficientFunds     = errors.New("insufficient available balance for lien")
	ErrLienCurrencyMismatch  = errors.New("lien currency must match account currency")
	ErrLienAmountNotPositive = errors.New("lien amount must be positive")
	ErrAmountRequired        = errors.New("amount is required")
	ErrAmountOverflow        = errors.New("amount too large: would overflow")
	// Transaction operation errors for error detection
	errTxSaveAccount       = errors.New("save_account")
	errTxUpdateLien        = errors.New("update_lien")
	errTxSaveLien          = errors.New("save_lien")
	errTxAccountNotActive  = errors.New("account_not_active")
	errTxCurrencyMismatch  = errors.New("currency_mismatch")
	errTxSumLiensFailed    = errors.New("sum_liens_failed")
	errTxInsufficientFunds = errors.New("insufficient_funds")
	errTxDomainError       = errors.New("domain_error")
)

// Lien operation status constants for metrics
const (
	opStatusLienRepoNil           = "lien_repo_nil"
	opStatusInvalidLienID         = "invalid_lien_id"
	opStatusLienNotFound          = "lien_not_found"
	opStatusRetrieveAccountFailed = "retrieve_account_failed"
	opStatusAccountNotFound       = "account_not_found"
	opStatusRetrieveFailed        = "retrieve_failed"
	opStatusAccountNotActive      = "account_not_active"
	opStatusInvalidAmount         = "invalid_amount"
	opStatusCurrencyMismatch      = "currency_mismatch"
	opStatusSumLiensFailed        = "sum_liens_failed"
	opStatusInsufficientFunds     = "insufficient_funds"
	opStatusDomainError           = "domain_error"
	opStatusSaveFailed            = "save_failed"
	opStatusInvalidLienStatus     = "invalid_lien_status"
	opStatusExecuteFailed         = "execute_failed"
	opStatusWithdrawFailed        = "withdraw_failed"
	opStatusVersionConflict       = "version_conflict"
	opStatusUpdateLienFailed      = "update_lien_failed"
	opStatusSaveAccountFailed     = "save_account_failed"
	opStatusTerminateFailed       = "terminate_failed"
	opStatusUpdateFailed          = "update_failed"
)

// InitiateLien creates a fund reservation on an account
func (s *Service) InitiateLien(_ context.Context, req *pb.InitiateLienRequest) (*pb.InitiateLienResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordOperationDuration("initiate_lien", operationStatus, time.Since(start))
	}()

	// Validate lien repository is configured
	if s.lienRepo == nil {
		operationStatus = opStatusLienRepoNil
		return nil, status.Error(codes.FailedPrecondition, "lien operations not configured")
	}

	// Check for idempotency using PaymentOrderReference
	if idempotentResp, found, err := s.checkLienIdempotency(req.PaymentOrderReference); err != nil {
		operationStatus = opStatusRetrieveFailed
		return nil, err
	} else if found {
		return idempotentResp, nil
	}

	// Convert and validate amount first (before transaction)
	lienAmount, err := s.protoToMoney(req.Amount)
	if err != nil {
		operationStatus = opStatusInvalidAmount
		return nil, status.Errorf(codes.InvalidArgument, "invalid amount: %v", err)
	}

	if !lienAmount.IsPositive() {
		operationStatus = opStatusInvalidAmount
		return nil, status.Error(codes.InvalidArgument, "lien amount must be positive")
	}

	// Use a transaction with pessimistic locking to prevent race conditions.
	// Without FOR UPDATE, concurrent InitiateLien calls could both check available
	// balance, see sufficient funds, and both create liens - resulting in over-reservation.
	var lien *domain.Lien
	var account *domain.CurrentAccount
	var availableBalance int64

	txErr := s.repo.DB().Transaction(func(tx *gorm.DB) error {
		txRepo := s.repo.WithTx(tx)
		txLienRepo := s.lienRepo.WithTx(tx)

		// Retrieve account with FOR UPDATE lock to prevent concurrent modifications
		var txErr error
		account, txErr = txRepo.FindByIDForUpdate(req.AccountId)
		if txErr != nil {
			return txErr
		}

		// Validate account is active
		if account.Status != domain.AccountStatusActive {
			return errTxAccountNotActive
		}

		// Validate currency matches account
		if lienAmount.Currency() != account.Balance.Currency() {
			return errTxCurrencyMismatch
		}

		// Calculate available balance (within the lock)
		activeLiensTotal, err := txLienRepo.SumActiveAmountByAccountID(account.ID)
		if err != nil {
			return fmt.Errorf("%w: %w", errTxSumLiensFailed, err)
		}

		// Available = Current Balance - Active Liens
		availableBalance = account.Balance.AmountCents() - activeLiensTotal

		// Check sufficient funds
		if lienAmount.AmountCents() > availableBalance {
			return errTxInsufficientFunds
		}

		// Create lien domain object
		lien, err = domain.NewLien(account.ID, lienAmount, req.PaymentOrderReference, nil)
		if err != nil {
			return fmt.Errorf("%w: %w", errTxDomainError, err)
		}

		// Persist lien (within the transaction)
		if err := txLienRepo.Create(lien); err != nil {
			return fmt.Errorf("%w: %w", errTxSaveLien, err)
		}

		return nil
	})

	// Handle transaction errors with appropriate status codes
	if txErr != nil {
		switch {
		case errors.Is(txErr, persistence.ErrAccountNotFound):
			operationStatus = opStatusAccountNotFound
			return nil, status.Errorf(codes.NotFound, "account not found: %s", req.AccountId)
		case errors.Is(txErr, errTxAccountNotActive):
			operationStatus = opStatusAccountNotActive
			return nil, status.Errorf(codes.FailedPrecondition, "account is not active")
		case errors.Is(txErr, errTxCurrencyMismatch):
			operationStatus = opStatusCurrencyMismatch
			return nil, status.Errorf(codes.InvalidArgument, "lien currency must match account currency")
		case errors.Is(txErr, errTxInsufficientFunds):
			operationStatus = opStatusInsufficientFunds
			return nil, status.Errorf(codes.FailedPrecondition, "insufficient available balance")
		case errors.Is(txErr, errTxDomainError):
			operationStatus = opStatusDomainError
			return nil, status.Errorf(codes.InvalidArgument, "failed to create lien: %v", txErr)
		case errors.Is(txErr, errTxSaveLien):
			operationStatus = opStatusSaveFailed
			return nil, status.Errorf(codes.Internal, "failed to save lien: %v", txErr)
		default:
			operationStatus = opStatusRetrieveFailed
			return nil, status.Errorf(codes.Internal, "failed to create lien: %v", txErr)
		}
	}

	s.logger.Info("lien created",
		"lien_id", lien.ID.String(),
		"account_id", account.AccountID,
		"amount_cents", lienAmount.AmountCents(),
		"payment_order_ref", req.PaymentOrderReference)

	// Calculate new available balance after this lien
	newAvailableBalance := availableBalance - lienAmount.AmountCents()
	availableMoney, err := domain.NewMoney(account.Balance.Currency(), newAvailableBalance)
	if err != nil {
		// This should never happen if validation passed - log and return without available balance
		s.logger.Error("failed to create available balance money", "error", err)
	}

	return &pb.InitiateLienResponse{
		Lien:             toLienProto(lien),
		AvailableBalance: toMoneyAmount(availableMoney),
	}, nil
}

// ExecuteLien converts a reservation to an actual debit atomically
func (s *Service) ExecuteLien(_ context.Context, req *pb.ExecuteLienRequest) (*pb.ExecuteLienResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordOperationDuration("execute_lien", operationStatus, time.Since(start))
	}()

	// Validate lien repository is configured
	if s.lienRepo == nil {
		operationStatus = opStatusLienRepoNil
		return nil, status.Error(codes.FailedPrecondition, "lien operations not configured")
	}

	// Parse lien ID
	lienID, err := uuid.Parse(req.LienId)
	if err != nil {
		operationStatus = opStatusInvalidLienID
		return nil, status.Errorf(codes.InvalidArgument, "invalid lien ID: %v", err)
	}

	// Retrieve lien
	lien, err := s.lienRepo.FindByID(lienID)
	if err != nil {
		if errors.Is(err, persistence.ErrLienNotFound) {
			operationStatus = opStatusLienNotFound
			return nil, status.Errorf(codes.NotFound, "lien not found: %s", req.LienId)
		}
		operationStatus = opStatusRetrieveFailed
		return nil, status.Errorf(codes.Internal, "failed to retrieve lien: %v", err)
	}

	// Check if already executed (idempotent)
	if lien.Status == domain.LienStatusExecuted {
		s.logger.Info("lien already executed (idempotent)",
			"lien_id", lien.ID.String())

		// Retrieve account for response
		account, err := s.repo.FindByUUID(lien.AccountID)
		if err != nil {
			operationStatus = opStatusRetrieveAccountFailed
			return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
		}

		availableMoney := s.calculateAvailableBalance(lien.AccountID, account.Balance)
		return &pb.ExecuteLienResponse{
			Lien:             toLienProto(lien),
			NewBalance:       toMoneyAmount(account.Balance),
			AvailableBalance: toMoneyAmount(availableMoney),
			TransactionId:    fmt.Sprintf("TXN-LIEN-%s", lien.ID.String()[:8]),
		}, nil
	}

	// Validate lien can be executed
	if !lien.CanExecute() {
		operationStatus = opStatusInvalidLienStatus
		return nil, status.Errorf(codes.FailedPrecondition,
			"lien cannot be executed: status=%s, expired=%v", lien.Status, lien.IsExpired())
	}

	// Retrieve account
	account, err := s.repo.FindByUUID(lien.AccountID)
	if err != nil {
		operationStatus = opStatusRetrieveAccountFailed
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	// Execute lien (domain logic)
	if err := lien.Execute(); err != nil {
		operationStatus = opStatusExecuteFailed
		return nil, status.Errorf(codes.FailedPrecondition, "failed to execute lien: %v", err)
	}

	// Debit the account
	if err := account.Withdraw(lien.Amount); err != nil {
		operationStatus = opStatusWithdrawFailed
		return nil, status.Errorf(codes.FailedPrecondition, "failed to debit account: %v", err)
	}

	// Execute both updates atomically in a transaction to prevent double-debit on retry.
	// If we updated lien first and account save failed, retry would see EXECUTED lien
	// and return idempotent success without debiting - funds would never be debited.
	// If we updated account first and lien update failed, retry would see ACTIVE lien
	// and debit again - resulting in double-debit.
	// Using a transaction ensures both succeed or both fail together.
	txErr := s.repo.DB().Transaction(func(tx *gorm.DB) error {
		txRepo := s.repo.WithTx(tx)
		txLienRepo := s.lienRepo.WithTx(tx)

		// Update lien status first (lighter operation)
		if err := txLienRepo.Update(lien); err != nil {
			return fmt.Errorf("%w: %w", errTxUpdateLien, err)
		}

		// Save account (heavier operation with balance)
		if err := txRepo.Save(account); err != nil {
			return fmt.Errorf("%w: %w", errTxSaveAccount, err)
		}

		return nil
	})

	if txErr != nil {
		// Determine which operation failed for proper error reporting
		if errors.Is(txErr, persistence.ErrLienVersionConflict) {
			operationStatus = opStatusVersionConflict
			return nil, status.Error(codes.Aborted, "concurrent modification detected, please retry")
		}
		if errors.Is(txErr, errTxSaveAccount) {
			operationStatus = opStatusSaveAccountFailed
		} else {
			operationStatus = opStatusUpdateLienFailed
		}
		return nil, status.Errorf(codes.Internal, "failed to execute lien transaction: %v", txErr)
	}

	transactionID := fmt.Sprintf("TXN-LIEN-%s", lien.ID.String()[:8])

	s.logger.Info("lien executed",
		"lien_id", lien.ID.String(),
		"account_id", account.AccountID,
		"amount_cents", lien.Amount.AmountCents(),
		"transaction_id", transactionID)

	availableMoney := s.calculateAvailableBalance(lien.AccountID, account.Balance)
	return &pb.ExecuteLienResponse{
		Lien:             toLienProto(lien),
		NewBalance:       toMoneyAmount(account.Balance),
		AvailableBalance: toMoneyAmount(availableMoney),
		TransactionId:    transactionID,
	}, nil
}

// TerminateLien releases a reservation without executing
func (s *Service) TerminateLien(_ context.Context, req *pb.TerminateLienRequest) (*pb.TerminateLienResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordOperationDuration("terminate_lien", operationStatus, time.Since(start))
	}()

	// Validate lien repository is configured
	if s.lienRepo == nil {
		operationStatus = opStatusLienRepoNil
		return nil, status.Error(codes.FailedPrecondition, "lien operations not configured")
	}

	// Parse lien ID
	lienID, err := uuid.Parse(req.LienId)
	if err != nil {
		operationStatus = opStatusInvalidLienID
		return nil, status.Errorf(codes.InvalidArgument, "invalid lien ID: %v", err)
	}

	// Retrieve lien
	lien, err := s.lienRepo.FindByID(lienID)
	if err != nil {
		if errors.Is(err, persistence.ErrLienNotFound) {
			operationStatus = opStatusLienNotFound
			return nil, status.Errorf(codes.NotFound, "lien not found: %s", req.LienId)
		}
		operationStatus = opStatusRetrieveFailed
		return nil, status.Errorf(codes.Internal, "failed to retrieve lien: %v", err)
	}

	// Check if already terminated (idempotent)
	if lien.Status == domain.LienStatusTerminated {
		s.logger.Info("lien already terminated (idempotent)",
			"lien_id", lien.ID.String())

		// Calculate available balance - errors logged but don't fail idempotent response
		account, acctErr := s.repo.FindByUUID(lien.AccountID)
		if acctErr != nil {
			s.logger.Error("failed to find account for idempotent response", "error", acctErr)
			return &pb.TerminateLienResponse{Lien: toLienProto(lien)}, nil
		}
		availableMoney := s.calculateAvailableBalance(lien.AccountID, account.Balance)
		return &pb.TerminateLienResponse{
			Lien:             toLienProto(lien),
			AvailableBalance: toMoneyAmount(availableMoney),
		}, nil
	}

	// Validate lien can be terminated
	if !lien.CanTerminate() {
		operationStatus = opStatusInvalidLienStatus
		return nil, status.Errorf(codes.FailedPrecondition,
			"lien cannot be terminated: status=%s", lien.Status)
	}

	// Terminate lien (domain logic)
	reason := req.Reason
	if reason == "" {
		reason = "Terminated via API"
	}
	if err := lien.Terminate(reason); err != nil {
		operationStatus = opStatusTerminateFailed
		return nil, status.Errorf(codes.FailedPrecondition, "failed to terminate lien: %v", err)
	}

	// Update lien status
	if err := s.lienRepo.Update(lien); err != nil {
		if errors.Is(err, persistence.ErrLienVersionConflict) {
			operationStatus = opStatusVersionConflict
			return nil, status.Error(codes.Aborted, "concurrent modification detected, please retry")
		}
		operationStatus = opStatusUpdateFailed
		return nil, status.Errorf(codes.Internal, "failed to update lien: %v", err)
	}

	s.logger.Info("lien terminated",
		"lien_id", lien.ID.String(),
		"reason", reason)

	// Calculate new available balance (funds released)
	account, err := s.repo.FindByUUID(lien.AccountID)
	if err != nil {
		operationStatus = opStatusRetrieveAccountFailed
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	availableMoney := s.calculateAvailableBalance(lien.AccountID, account.Balance)
	return &pb.TerminateLienResponse{
		Lien:             toLienProto(lien),
		AvailableBalance: toMoneyAmount(availableMoney),
	}, nil
}

// RetrieveLien gets lien details
func (s *Service) RetrieveLien(_ context.Context, req *pb.RetrieveLienRequest) (*pb.RetrieveLienResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordOperationDuration("retrieve_lien", operationStatus, time.Since(start))
	}()

	// Validate lien repository is configured
	if s.lienRepo == nil {
		operationStatus = opStatusLienRepoNil
		return nil, status.Error(codes.FailedPrecondition, "lien operations not configured")
	}

	// Parse lien ID
	lienID, err := uuid.Parse(req.LienId)
	if err != nil {
		operationStatus = opStatusInvalidLienID
		return nil, status.Errorf(codes.InvalidArgument, "invalid lien ID: %v", err)
	}

	// Retrieve lien
	lien, err := s.lienRepo.FindByID(lienID)
	if err != nil {
		if errors.Is(err, persistence.ErrLienNotFound) {
			operationStatus = opStatusLienNotFound
			return nil, status.Errorf(codes.NotFound, "lien not found: %s", req.LienId)
		}
		operationStatus = opStatusRetrieveFailed
		return nil, status.Errorf(codes.Internal, "failed to retrieve lien: %v", err)
	}

	return &pb.RetrieveLienResponse{
		Lien: toLienProto(lien),
	}, nil
}

// Helper functions for lien service

// protoToMoney converts a proto MoneyAmount to domain Money
func (s *Service) protoToMoney(amount *commonpb.MoneyAmount) (domain.Money, error) {
	if amount == nil || amount.Amount == nil {
		return domain.Money{}, ErrAmountRequired
	}

	// Validate units won't overflow when multiplied by 100
	if amount.Amount.Units > math.MaxInt64/100 || amount.Amount.Units < math.MinInt64/100 {
		return domain.Money{}, ErrAmountOverflow
	}

	// Convert to cents
	// unitsCents is safe due to overflow check above
	unitsCents := amount.Amount.Units * 100

	// nanosCents is at most 99 (nanos range 0-999999999, divided by 10000000)
	// Adding to unitsCents is safe since we checked unitsCents won't overflow
	nanosCents := (amount.Amount.Nanos + 5000000) / 10000000
	totalCents := unitsCents + int64(nanosCents)

	return domain.NewMoney(amount.Amount.CurrencyCode, totalCents)
}

// toLienProto converts a domain Lien to proto Lien
func toLienProto(lien *domain.Lien) *pb.Lien {
	return &pb.Lien{
		LienId:                lien.ID.String(),
		AccountId:             lien.AccountID.String(),
		Amount:                toMoneyAmount(lien.Amount),
		Status:                mapLienStatusToProto(lien.Status),
		PaymentOrderReference: lien.PaymentOrderReference,
		CreatedAt:             timestamppb.New(lien.CreatedAt),
		UpdatedAt:             timestamppb.New(lien.UpdatedAt),
	}
}

// mapLienStatusToProto maps domain LienStatus to proto LienStatus
func mapLienStatusToProto(status domain.LienStatus) pb.LienStatus {
	switch status {
	case domain.LienStatusActive:
		return pb.LienStatus_LIEN_STATUS_ACTIVE
	case domain.LienStatusExecuted:
		return pb.LienStatus_LIEN_STATUS_EXECUTED
	case domain.LienStatusTerminated:
		return pb.LienStatus_LIEN_STATUS_TERMINATED
	default:
		return pb.LienStatus_LIEN_STATUS_UNSPECIFIED
	}
}

// checkLienIdempotency checks if a lien with the given PaymentOrderReference already exists.
// Returns (response, true) if idempotent response should be returned, (nil, false) otherwise.
// Returns error status if a non-recoverable error occurs.
func (s *Service) checkLienIdempotency(paymentOrderRef string) (*pb.InitiateLienResponse, bool, error) {
	if paymentOrderRef == "" {
		return nil, false, nil
	}

	existingLien, err := s.lienRepo.FindByPaymentOrderReference(paymentOrderRef)
	if err != nil {
		if errors.Is(err, persistence.ErrLienNotFound) {
			return nil, false, nil // Not found - continue with creation
		}
		return nil, false, status.Errorf(codes.Internal, "failed to check idempotency: %v", err)
	}

	// Lien already exists - return idempotent response
	s.logger.Info("lien already exists (idempotent)",
		"lien_id", existingLien.ID.String(),
		"payment_order_ref", paymentOrderRef)

	// Retrieve account for available balance calculation
	account, acctErr := s.repo.FindByUUID(existingLien.AccountID)
	if acctErr != nil {
		s.logger.Error("failed to retrieve account for idempotent response", "error", acctErr)
		return &pb.InitiateLienResponse{Lien: toLienProto(existingLien)}, true, nil
	}
	availableMoney := s.calculateAvailableBalance(existingLien.AccountID, account.Balance)
	return &pb.InitiateLienResponse{
		Lien:             toLienProto(existingLien),
		AvailableBalance: toMoneyAmount(availableMoney),
	}, true, nil
}

// calculateAvailableBalance calculates available balance with active liens.
// Logs errors but returns best-effort values since primary operations already succeeded.
func (s *Service) calculateAvailableBalance(accountID uuid.UUID, currentBalance domain.Money) domain.Money {
	activeLiensTotal, err := s.lienRepo.SumActiveAmountByAccountID(accountID)
	if err != nil {
		s.logger.Error("failed to sum active liens for response", "error", err)
		return currentBalance // Best effort: return current balance if liens can't be summed
	}
	availableBalance := currentBalance.AmountCents() - activeLiensTotal
	availableMoney, err := domain.NewMoney(currentBalance.Currency(), availableBalance)
	if err != nil {
		s.logger.Error("failed to create available balance for response", "error", err)
		return currentBalance // Best effort
	}
	return availableMoney
}
