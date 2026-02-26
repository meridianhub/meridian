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
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/domain"
	caobservability "github.com/meridianhub/meridian/services/current-account/observability"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// InitiateWithdrawal creates a pending withdrawal for validation before execution.
// This implements a two-phase withdrawal pattern where the withdrawal is first validated
// and created with INITIATED status, then can be executed later via ExecuteWithdrawal.
func (s *Service) InitiateWithdrawal(ctx context.Context, req *pb.InitiateWithdrawalRequest) (*pb.InitiateWithdrawalResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordOperationDuration("initiate_withdrawal", operationStatus, time.Since(start))
	}()

	// Validate required fields
	if req.AccountId == "" {
		operationStatus = opStatusMissingAccountID
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}
	if req.Amount == nil || req.Amount.Amount == nil {
		operationStatus = opStatusMissingAmount
		return nil, status.Error(codes.InvalidArgument, "amount is required")
	}

	// Retrieve account to validate
	account, err := s.repo.FindByID(ctx, req.AccountId)
	if err != nil {
		if errors.Is(err, persistence.ErrAccountNotFound) {
			operationStatus = opStatusAccountNotFound
			return nil, status.Errorf(codes.NotFound, "account not found: %s", req.AccountId)
		}
		operationStatus = opStatusRetrieveFailed
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	// Hydrate account with balance from Position Keeping (balance no longer persisted locally)
	account, err = s.hydrateAccountWithBalance(ctx, account)
	if err != nil {
		operationStatus = opStatusRetrieveFailed
		s.logger.Error("failed to hydrate account balance from Position Keeping",
			"account_id", req.AccountId,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to retrieve account balance: %v", err)
	}

	var validationMessages []string

	// Validate account status
	if account.Status() == domain.AccountStatusFrozen {
		operationStatus = opStatusAccountFrozen
		return nil, status.Errorf(codes.FailedPrecondition, "cannot initiate withdrawal on frozen account: %s", req.AccountId)
	}
	if account.Status() == domain.AccountStatusClosed {
		operationStatus = opStatusAccountClosed
		return nil, status.Errorf(codes.FailedPrecondition, "cannot initiate withdrawal on closed account: %s", req.AccountId)
	}

	// Validate currency matches
	if req.Amount.Amount.CurrencyCode != account.Balance().InstrumentCode() {
		operationStatus = opStatusCurrencyMismatch
		return nil, status.Errorf(codes.InvalidArgument,
			"currency mismatch: expected %s, got %s",
			account.Balance().InstrumentCode(), req.Amount.Amount.CurrencyCode)
	}

	// Validate overflow: Units*100 must not overflow int64
	if req.Amount.Amount.Units > math.MaxInt64/100 || req.Amount.Amount.Units < math.MinInt64/100 {
		operationStatus = opStatusAmountOverflow
		return nil, status.Errorf(codes.InvalidArgument,
			"amount too large: units %d would overflow", req.Amount.Amount.Units)
	}

	// Convert and validate amount
	unitsCents := req.Amount.Amount.Units * 100
	nanosCents := (req.Amount.Amount.Nanos + 5000000) / 10000000
	amountCents := unitsCents + int64(nanosCents)

	if amountCents <= 0 {
		operationStatus = opStatusInvalidAmount
		return nil, status.Errorf(codes.InvalidArgument, "withdrawal amount must be positive")
	}

	// Check available balance (warning only - balance could change before execution)
	availCents, _ := account.AvailableBalance().ToMinorUnits()
	if amountCents > availCents {
		validationMessages = append(validationMessages,
			fmt.Sprintf("Warning: requested amount (%d cents) exceeds current available balance (%d cents)", amountCents, availCents))
	}

	// Use provided reference if available, otherwise generate one
	reference := req.Reference
	if reference == "" {
		reference = fmt.Sprintf("WTH-%s", uuid.New().String()[:8])
	}

	// Create domain Money from amount cents
	amount, err := domain.NewMoney(req.Amount.Amount.CurrencyCode, amountCents)
	if err != nil {
		operationStatus = operationStatusInvalidCurrency
		return nil, status.Errorf(codes.InvalidArgument, "invalid currency: %v", err)
	}

	// Create domain withdrawal
	domainWithdrawal, err := domain.NewWithdrawal(account.ID(), amount, reference)
	if err != nil {
		operationStatus = opStatusWithdrawalFailed
		return nil, status.Errorf(codes.InvalidArgument, "failed to create withdrawal: %v", err)
	}

	// Persist withdrawal to database (if repository is configured)
	if s.withdrawalRepo != nil {
		if err := s.withdrawalRepo.Create(ctx, domainWithdrawal); err != nil {
			operationStatus = opStatusSaveFailed
			s.logger.Error("failed to persist withdrawal",
				"withdrawal_id", domainWithdrawal.ID,
				"account_id", req.AccountId,
				"error", err)
			return nil, status.Errorf(codes.Internal, "failed to persist withdrawal: %v", err)
		}
	}

	s.logger.Info("withdrawal initiated",
		"withdrawal_id", domainWithdrawal.ID,
		"withdrawal_reference", reference,
		"account_id", req.AccountId,
		"amount_cents", amountCents)

	// Convert domain withdrawal to proto
	withdrawal := toProtoWithdrawal(domainWithdrawal, req.AccountId)
	withdrawal.Description = req.Description // Add description from request

	return &pb.InitiateWithdrawalResponse{
		Withdrawal:         withdrawal,
		ValidationPassed:   len(validationMessages) == 0,
		ValidationMessages: validationMessages,
	}, nil
}

// UpdateWithdrawal modifies a pending withdrawal before execution.
// Only withdrawals with INITIATED status can be updated.
// Note: Currently supports retrieval and validation only. Field updates (amount, description)
// are not yet supported as the domain model treats these as immutable.
func (s *Service) UpdateWithdrawal(ctx context.Context, req *pb.UpdateWithdrawalRequest) (*pb.UpdateWithdrawalResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordOperationDuration("update_withdrawal", operationStatus, time.Since(start))
	}()

	if req.WithdrawalId == "" {
		operationStatus = opStatusMissingWithdrawalID
		return nil, status.Error(codes.InvalidArgument, "withdrawal_id is required")
	}

	// Lookup withdrawal by reference
	withdrawal, err := s.withdrawalRepo.FindByReference(ctx, req.WithdrawalId)
	if err != nil {
		if errors.Is(err, persistence.ErrWithdrawalNotFound) {
			operationStatus = opStatusWithdrawalNotFound
			return nil, status.Errorf(codes.NotFound, "withdrawal not found: %s", req.WithdrawalId)
		}
		operationStatus = opStatusRetrieveFailed
		s.logger.Error("failed to retrieve withdrawal",
			"withdrawal_id", req.WithdrawalId,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to retrieve withdrawal: %v", err)
	}

	// Only pending withdrawals can be updated
	if !withdrawal.IsPending() {
		operationStatus = "withdrawal_not_pending"
		return nil, status.Errorf(codes.FailedPrecondition,
			"cannot update withdrawal %s: not in pending status (current: %s)",
			req.WithdrawalId, withdrawal.Status)
	}

	// Get account to retrieve business account ID for response
	account, err := s.repo.FindByUUID(ctx, withdrawal.AccountID)
	if err != nil {
		if errors.Is(err, persistence.ErrAccountNotFound) {
			operationStatus = opStatusAccountNotFound
			return nil, status.Errorf(codes.NotFound, "account not found for withdrawal")
		}
		operationStatus = opStatusRetrieveFailed
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	var validationMessages []string

	// Note: Field updates (amount, description, reference) are not yet implemented
	// as the domain model treats these as immutable after creation.
	// This would require domain model enhancements.
	if req.Amount != nil {
		validationMessages = append(validationMessages,
			"Warning: amount updates are not yet supported; withdrawal amount unchanged")
	}
	if req.Description != "" {
		validationMessages = append(validationMessages,
			"Warning: description updates are not yet supported")
	}
	if req.Reference != "" && req.Reference != withdrawal.Reference {
		validationMessages = append(validationMessages,
			"Warning: reference updates are not yet supported")
	}

	s.logger.Info("withdrawal update requested",
		"withdrawal_id", req.WithdrawalId,
		"has_amount_update", req.Amount != nil,
		"has_description_update", req.Description != "",
		"warnings", len(validationMessages))

	return &pb.UpdateWithdrawalResponse{
		Withdrawal:         toProtoWithdrawal(withdrawal, account.AccountID()),
		ValidationPassed:   len(validationMessages) == 0,
		ValidationMessages: validationMessages,
	}, nil
}

// RetrieveWithdrawal gets withdrawal details by ID or lists withdrawals by account.
// When withdrawal_id is provided, returns a single withdrawal.
// When account_id is provided, returns a paginated list of withdrawals for that account.
func (s *Service) RetrieveWithdrawal(ctx context.Context, req *pb.RetrieveWithdrawalRequest) (*pb.RetrieveWithdrawalResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordOperationDuration("retrieve_withdrawal", operationStatus, time.Since(start))
	}()

	// Validate that at least one identifier is provided first
	if req.WithdrawalId == "" && req.AccountId == "" {
		operationStatus = opStatusMissingIdentifier
		return nil, status.Error(codes.InvalidArgument, "either withdrawal_id or account_id is required")
	}

	// Single withdrawal lookup by ID
	if req.WithdrawalId != "" {
		withdrawal, err := s.withdrawalRepo.FindByReference(ctx, req.WithdrawalId)
		if err != nil {
			if errors.Is(err, persistence.ErrWithdrawalNotFound) {
				operationStatus = opStatusWithdrawalNotFound
				return nil, status.Errorf(codes.NotFound, "withdrawal not found: %s", req.WithdrawalId)
			}
			operationStatus = opStatusRetrieveFailed
			s.logger.Error("failed to retrieve withdrawal",
				"withdrawal_id", req.WithdrawalId,
				"error", err)
			return nil, status.Errorf(codes.Internal, "failed to retrieve withdrawal: %v", err)
		}

		// Get account to retrieve business account ID for response
		account, err := s.repo.FindByUUID(ctx, withdrawal.AccountID)
		if err != nil {
			if errors.Is(err, persistence.ErrAccountNotFound) {
				operationStatus = opStatusAccountNotFound
				return nil, status.Errorf(codes.NotFound, "account not found for withdrawal")
			}
			operationStatus = opStatusRetrieveFailed
			return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
		}

		s.logger.Debug("withdrawal retrieved",
			"withdrawal_id", req.WithdrawalId,
			"account_id", account.AccountID(),
			"status", withdrawal.Status)

		return &pb.RetrieveWithdrawalResponse{
			Withdrawals: []*pb.Withdrawal{toProtoWithdrawal(withdrawal, account.AccountID())},
			Pagination: &commonpb.PaginationResponse{
				TotalCount: 1,
			},
		}, nil
	}

	// List withdrawals by account
	if req.AccountId != "" {
		// Validate account exists and get the internal UUID
		account, err := s.repo.FindByID(ctx, req.AccountId)
		if err != nil {
			if errors.Is(err, persistence.ErrAccountNotFound) {
				operationStatus = opStatusAccountNotFound
				return nil, status.Errorf(codes.NotFound, "account not found: %s", req.AccountId)
			}
			operationStatus = opStatusRetrieveFailed
			return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
		}

		// Parse pagination parameters
		pagination := persistence.PaginationParams{
			Limit:  50, // Default limit
			Offset: 0,
		}
		if req.Pagination != nil {
			if req.Pagination.PageSize > 0 {
				pagination.Limit = int(req.Pagination.PageSize)
			}
			// Simple offset-based pagination from page token
			// In production, consider cursor-based pagination for better performance
			if req.Pagination.PageToken != "" {
				// Page token contains the offset
				var offset int
				if _, err := fmt.Sscanf(req.Pagination.PageToken, "%d", &offset); err != nil {
					operationStatus = opStatusRetrieveFailed
					return nil, status.Errorf(codes.InvalidArgument, "invalid page token: %s", req.Pagination.PageToken)
				}
				pagination.Offset = offset
			}
		}

		// List withdrawals for the account
		withdrawals, err := s.withdrawalRepo.List(ctx, account.ID(), pagination)
		if err != nil {
			operationStatus = opStatusRetrieveFailed
			s.logger.Error("failed to list withdrawals",
				"account_id", req.AccountId,
				"error", err)
			return nil, status.Errorf(codes.Internal, "failed to list withdrawals: %v", err)
		}

		// Convert domain withdrawals to proto
		protoWithdrawals := make([]*pb.Withdrawal, 0, len(withdrawals))
		for _, w := range withdrawals {
			protoWithdrawals = append(protoWithdrawals, toProtoWithdrawal(w, req.AccountId))
		}

		// Build pagination response
		var nextPageToken string
		if len(withdrawals) == pagination.Limit {
			// There might be more results
			nextPageToken = fmt.Sprintf("%d", pagination.Offset+pagination.Limit)
		}

		s.logger.Debug("withdrawals listed",
			"account_id", req.AccountId,
			"count", len(withdrawals),
			"has_more", nextPageToken != "")

		return &pb.RetrieveWithdrawalResponse{
			Withdrawals: protoWithdrawals,
			Pagination: &commonpb.PaginationResponse{
				NextPageToken: nextPageToken,
				TotalCount:    int64(len(withdrawals)),
			},
		}, nil
	}

	operationStatus = opStatusMissingIdentifier
	return nil, status.Error(codes.InvalidArgument, "either withdrawal_id or account_id is required")
}
