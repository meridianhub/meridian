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
)

// Lien-specific errors
var (
	ErrLienRepositoryNil     = errors.New("lien repository cannot be nil")
	ErrInsufficientFunds     = errors.New("insufficient available balance for lien")
	ErrLienCurrencyMismatch  = errors.New("lien currency must match account currency")
	ErrLienAmountNotPositive = errors.New("lien amount must be positive")
	ErrAmountRequired        = errors.New("amount is required")
	ErrAmountOverflow        = errors.New("amount too large: would overflow")
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

	// Retrieve account
	account, err := s.repo.FindByID(req.AccountId)
	if err != nil {
		if errors.Is(err, persistence.ErrAccountNotFound) {
			operationStatus = opStatusAccountNotFound
			return nil, status.Errorf(codes.NotFound, "account not found: %s", req.AccountId)
		}
		operationStatus = opStatusRetrieveFailed
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	// Validate account is active
	if account.Status != domain.AccountStatusActive {
		operationStatus = opStatusAccountNotActive
		return nil, status.Errorf(codes.FailedPrecondition, "account is not active: %s", account.Status)
	}

	// Convert and validate amount
	lienAmount, err := s.protoToMoney(req.Amount)
	if err != nil {
		operationStatus = opStatusInvalidAmount
		return nil, status.Errorf(codes.InvalidArgument, "invalid amount: %v", err)
	}

	if !lienAmount.IsPositive() {
		operationStatus = opStatusInvalidAmount
		return nil, status.Error(codes.InvalidArgument, "lien amount must be positive")
	}

	// Validate currency matches account
	if lienAmount.Currency() != account.Balance.Currency() {
		operationStatus = opStatusCurrencyMismatch
		return nil, status.Errorf(codes.InvalidArgument,
			"lien currency (%s) must match account currency (%s)",
			lienAmount.Currency(), account.Balance.Currency())
	}

	// Calculate available balance
	activeLiensTotal, err := s.lienRepo.SumActiveAmountByAccountID(account.ID)
	if err != nil {
		operationStatus = opStatusSumLiensFailed
		return nil, status.Errorf(codes.Internal, "failed to calculate active liens: %v", err)
	}

	// Available = Current Balance - Active Liens
	availableBalance := account.Balance.AmountCents() - activeLiensTotal

	// Check sufficient funds
	if lienAmount.AmountCents() > availableBalance {
		operationStatus = opStatusInsufficientFunds
		return nil, status.Errorf(codes.FailedPrecondition,
			"insufficient available balance: requested %d cents, available %d cents",
			lienAmount.AmountCents(), availableBalance)
	}

	// Create lien domain object
	lien, err := domain.NewLien(account.ID, lienAmount, req.PaymentOrderReference, nil)
	if err != nil {
		operationStatus = opStatusDomainError
		return nil, status.Errorf(codes.InvalidArgument, "failed to create lien: %v", err)
	}

	// Persist lien
	if err := s.lienRepo.Create(lien); err != nil {
		operationStatus = opStatusSaveFailed
		return nil, status.Errorf(codes.Internal, "failed to save lien: %v", err)
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

		activeLiensTotal, sumErr := s.lienRepo.SumActiveAmountByAccountID(lien.AccountID)
		if sumErr != nil {
			s.logger.Error("failed to sum active liens for idempotent response", "error", sumErr)
		}
		availableBalance := account.Balance.AmountCents() - activeLiensTotal
		availableMoney, moneyErr := domain.NewMoney(account.Balance.Currency(), availableBalance)
		if moneyErr != nil {
			s.logger.Error("failed to create available balance for idempotent response", "error", moneyErr)
		}

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

	// Update lien status
	if err := s.lienRepo.Update(lien); err != nil {
		if errors.Is(err, persistence.ErrLienVersionConflict) {
			operationStatus = opStatusVersionConflict
			return nil, status.Error(codes.Aborted, "concurrent modification detected, please retry")
		}
		operationStatus = opStatusUpdateLienFailed
		return nil, status.Errorf(codes.Internal, "failed to update lien: %v", err)
	}

	// Save account
	if err := s.repo.Save(account); err != nil {
		operationStatus = opStatusSaveAccountFailed
		return nil, status.Errorf(codes.Internal, "failed to save account: %v", err)
	}

	transactionID := fmt.Sprintf("TXN-LIEN-%s", lien.ID.String()[:8])

	s.logger.Info("lien executed",
		"lien_id", lien.ID.String(),
		"account_id", account.AccountID,
		"amount_cents", lien.Amount.AmountCents(),
		"transaction_id", transactionID)

	// Calculate new available balance
	activeLiensTotal, sumErr := s.lienRepo.SumActiveAmountByAccountID(lien.AccountID)
	if sumErr != nil {
		s.logger.Error("failed to sum active liens for response", "error", sumErr)
	}
	availableBalance := account.Balance.AmountCents() - activeLiensTotal
	availableMoney, moneyErr := domain.NewMoney(account.Balance.Currency(), availableBalance)
	if moneyErr != nil {
		s.logger.Error("failed to create available balance for response", "error", moneyErr)
	}

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
		activeLiensTotal, sumErr := s.lienRepo.SumActiveAmountByAccountID(lien.AccountID)
		if sumErr != nil {
			s.logger.Error("failed to sum active liens for idempotent response", "error", sumErr)
		}
		account, acctErr := s.repo.FindByUUID(lien.AccountID)
		if acctErr != nil {
			s.logger.Error("failed to find account for idempotent response", "error", acctErr)
			return &pb.TerminateLienResponse{Lien: toLienProto(lien)}, nil
		}
		availableBalance := account.Balance.AmountCents() - activeLiensTotal
		availableMoney, moneyErr := domain.NewMoney(account.Balance.Currency(), availableBalance)
		if moneyErr != nil {
			s.logger.Error("failed to create available balance for idempotent response", "error", moneyErr)
		}

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

	activeLiensTotal, sumErr := s.lienRepo.SumActiveAmountByAccountID(lien.AccountID)
	if sumErr != nil {
		s.logger.Error("failed to sum active liens for response", "error", sumErr)
	}
	availableBalance := account.Balance.AmountCents() - activeLiensTotal
	availableMoney, moneyErr := domain.NewMoney(account.Balance.Currency(), availableBalance)
	if moneyErr != nil {
		s.logger.Error("failed to create available balance for response", "error", moneyErr)
	}

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

	// Validate overflow
	if amount.Amount.Units > math.MaxInt64/100 || amount.Amount.Units < math.MinInt64/100 {
		return domain.Money{}, ErrAmountOverflow
	}

	// Convert to cents
	unitsCents := amount.Amount.Units * 100
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
