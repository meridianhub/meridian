package service

import (
	"context"
	"errors"
	"fmt"
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

	// Validate and convert amount
	amount, opStatus, amountErr := validateWithdrawalAmount(req.Amount, account)
	if amountErr != nil {
		operationStatus = opStatus
		return nil, amountErr
	}

	amountMinor, _ := amount.ToMinorUnits()

	// Check available balance (warning only - balance could change before execution)
	availMinor, _ := account.AvailableBalance().ToMinorUnits()
	if amountMinor > availMinor {
		validationMessages = append(validationMessages,
			fmt.Sprintf("Warning: requested amount (%d minor units) exceeds current available balance (%d minor units)", amountMinor, availMinor))
	}

	// Use provided reference if available, otherwise generate one
	reference := req.Reference
	if reference == "" {
		reference = fmt.Sprintf("WTH-%s", uuid.New().String()[:8])
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
		"amount_minor_units", amountMinor)

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

	if req.WithdrawalId == "" && req.AccountId == "" {
		operationStatus = opStatusMissingIdentifier
		return nil, status.Error(codes.InvalidArgument, "either withdrawal_id or account_id is required")
	}

	if req.WithdrawalId != "" {
		resp, opStatus, err := s.retrieveSingleWithdrawal(ctx, req.WithdrawalId)
		if err != nil {
			operationStatus = opStatus
			return nil, err
		}
		return resp, nil
	}

	resp, opStatus, err := s.listWithdrawalsByAccount(ctx, req.AccountId, req.Pagination)
	if err != nil {
		operationStatus = opStatus
		return nil, err
	}
	return resp, nil
}

// retrieveSingleWithdrawal looks up a single withdrawal by reference ID.
func (s *Service) retrieveSingleWithdrawal(ctx context.Context, withdrawalID string) (*pb.RetrieveWithdrawalResponse, string, error) {
	withdrawal, err := s.withdrawalRepo.FindByReference(ctx, withdrawalID)
	if err != nil {
		if errors.Is(err, persistence.ErrWithdrawalNotFound) {
			return nil, opStatusWithdrawalNotFound,
				status.Errorf(codes.NotFound, "withdrawal not found: %s", withdrawalID)
		}
		s.logger.Error("failed to retrieve withdrawal",
			"withdrawal_id", withdrawalID,
			"error", err)
		return nil, opStatusRetrieveFailed,
			status.Errorf(codes.Internal, "failed to retrieve withdrawal: %v", err)
	}

	account, err := s.repo.FindByUUID(ctx, withdrawal.AccountID)
	if err != nil {
		if errors.Is(err, persistence.ErrAccountNotFound) {
			return nil, opStatusAccountNotFound,
				status.Errorf(codes.NotFound, "account not found for withdrawal")
		}
		return nil, opStatusRetrieveFailed,
			status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	s.logger.Debug("withdrawal retrieved",
		"withdrawal_id", withdrawalID,
		"account_id", account.AccountID(),
		"status", withdrawal.Status)

	return &pb.RetrieveWithdrawalResponse{
		Withdrawals: []*pb.Withdrawal{toProtoWithdrawal(withdrawal, account.AccountID())},
		Pagination: &commonpb.PaginationResponse{
			TotalCount: 1,
		},
	}, "", nil
}

// listWithdrawalsByAccount returns a paginated list of withdrawals for an account.
func (s *Service) listWithdrawalsByAccount(ctx context.Context, accountID string, paginationReq *commonpb.Pagination) (*pb.RetrieveWithdrawalResponse, string, error) {
	account, err := s.repo.FindByID(ctx, accountID)
	if err != nil {
		if errors.Is(err, persistence.ErrAccountNotFound) {
			return nil, opStatusAccountNotFound,
				status.Errorf(codes.NotFound, "account not found: %s", accountID)
		}
		return nil, opStatusRetrieveFailed,
			status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	pagination := persistence.PaginationParams{Limit: 50, Offset: 0}
	if paginationReq != nil {
		if paginationReq.PageSize > 0 {
			pagination.Limit = int(paginationReq.PageSize)
		}
		if paginationReq.PageToken != "" {
			var offset int
			if _, err := fmt.Sscanf(paginationReq.PageToken, "%d", &offset); err != nil {
				return nil, opStatusRetrieveFailed,
					status.Errorf(codes.InvalidArgument, "invalid page token: %s", paginationReq.PageToken)
			}
			pagination.Offset = offset
		}
	}

	withdrawals, err := s.withdrawalRepo.List(ctx, account.ID(), pagination)
	if err != nil {
		s.logger.Error("failed to list withdrawals",
			"account_id", accountID,
			"error", err)
		return nil, opStatusRetrieveFailed,
			status.Errorf(codes.Internal, "failed to list withdrawals: %v", err)
	}

	protoWithdrawals := make([]*pb.Withdrawal, 0, len(withdrawals))
	for _, w := range withdrawals {
		protoWithdrawals = append(protoWithdrawals, toProtoWithdrawal(w, accountID))
	}

	var nextPageToken string
	if len(withdrawals) == pagination.Limit {
		nextPageToken = fmt.Sprintf("%d", pagination.Offset+pagination.Limit)
	}

	s.logger.Debug("withdrawals listed",
		"account_id", accountID,
		"count", len(withdrawals),
		"has_more", nextPageToken != "")

	return &pb.RetrieveWithdrawalResponse{
		Withdrawals: protoWithdrawals,
		Pagination: &commonpb.PaginationResponse{
			NextPageToken: nextPageToken,
			TotalCount:    int64(len(withdrawals)),
		},
	}, "", nil
}
