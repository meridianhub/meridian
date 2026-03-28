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

	// Retrieve and validate account
	account, opStatus, err := s.retrieveAndValidateWithdrawalAccount(ctx, req.AccountId)
	if err != nil {
		operationStatus = opStatus
		return nil, err
	}

	// Validate and convert amount
	amount, opStatus, amountErr := validateWithdrawalAmount(req.Amount, account)
	if amountErr != nil {
		operationStatus = opStatus
		return nil, amountErr
	}

	// Build validation messages (balance warning)
	validationMessages := buildWithdrawalBalanceWarnings(amount, account)

	// Create and persist withdrawal
	reference := req.Reference
	if reference == "" {
		reference = fmt.Sprintf("WTH-%s", uuid.New().String()[:8])
	}

	domainWithdrawal, opStatus, err := s.createAndPersistWithdrawal(ctx, account, amount, reference, req.AccountId)
	if err != nil {
		operationStatus = opStatus
		return nil, err
	}

	amountMinor, _ := amount.ToMinorUnits()
	s.logger.Info("withdrawal initiated",
		"withdrawal_id", domainWithdrawal.ID,
		"withdrawal_reference", reference,
		"account_id", req.AccountId,
		"amount_minor_units", amountMinor)

	withdrawal := toProtoWithdrawal(domainWithdrawal, req.AccountId)
	withdrawal.Description = req.Description

	return &pb.InitiateWithdrawalResponse{
		Withdrawal:         withdrawal,
		ValidationPassed:   len(validationMessages) == 0,
		ValidationMessages: validationMessages,
	}, nil
}

// retrieveAndValidateWithdrawalAccount retrieves the account, hydrates its balance, and validates its status.
func (s *Service) retrieveAndValidateWithdrawalAccount(ctx context.Context, accountID string) (domain.CurrentAccount, string, error) {
	account, err := s.repo.FindByID(ctx, accountID)
	if err != nil {
		if errors.Is(err, persistence.ErrAccountNotFound) {
			return account, opStatusAccountNotFound,
				status.Errorf(codes.NotFound, "account not found: %s", accountID)
		}
		return account, opStatusRetrieveFailed,
			status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	account, err = s.hydrateAccountWithBalance(ctx, account)
	if err != nil {
		s.logger.Error("failed to hydrate account balance from Position Keeping",
			"account_id", accountID,
			"error", err)
		return account, opStatusRetrieveFailed,
			status.Errorf(codes.Internal, "failed to retrieve account balance: %v", err)
	}

	if account.Status() == domain.AccountStatusFrozen {
		return account, opStatusAccountFrozen,
			status.Errorf(codes.FailedPrecondition, "cannot initiate withdrawal on frozen account: %s", accountID)
	}
	if account.Status() == domain.AccountStatusClosed {
		return account, opStatusAccountClosed,
			status.Errorf(codes.FailedPrecondition, "cannot initiate withdrawal on closed account: %s", accountID)
	}

	return account, "", nil
}

// buildWithdrawalBalanceWarnings checks if the amount exceeds available balance and returns warnings.
func buildWithdrawalBalanceWarnings(amount domain.Amount, account domain.CurrentAccount) []string {
	amountMinor, _ := amount.ToMinorUnits()
	availMinor, _ := account.AvailableBalance().ToMinorUnits()
	if amountMinor > availMinor {
		return []string{
			fmt.Sprintf("Warning: requested amount (%d minor units) exceeds current available balance (%d minor units)", amountMinor, availMinor),
		}
	}
	return nil
}

// createAndPersistWithdrawal creates a domain withdrawal and persists it.
func (s *Service) createAndPersistWithdrawal(ctx context.Context, account domain.CurrentAccount, amount domain.Amount, reference, accountID string) (*domain.Withdrawal, string, error) {
	domainWithdrawal, err := domain.NewWithdrawal(account.ID(), amount, reference)
	if err != nil {
		return nil, opStatusWithdrawalFailed,
			status.Errorf(codes.InvalidArgument, "failed to create withdrawal: %v", err)
	}

	if s.withdrawalRepo != nil {
		if err := s.withdrawalRepo.Create(ctx, domainWithdrawal); err != nil {
			s.logger.Error("failed to persist withdrawal",
				"withdrawal_id", domainWithdrawal.ID,
				"account_id", accountID,
				"error", err)
			return nil, opStatusSaveFailed,
				status.Errorf(codes.Internal, "failed to persist withdrawal: %v", err)
		}
	}

	return domainWithdrawal, "", nil
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

	// Retrieve and validate withdrawal and account
	withdrawal, account, opStatus, err := s.retrievePendingWithdrawalWithAccount(ctx, req.WithdrawalId)
	if err != nil {
		operationStatus = opStatus
		return nil, err
	}

	// Build validation messages for unsupported updates
	validationMessages := buildUpdateWithdrawalWarnings(req, withdrawal)

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

// retrievePendingWithdrawalWithAccount retrieves a withdrawal and its account, validating the withdrawal is pending.
func (s *Service) retrievePendingWithdrawalWithAccount(ctx context.Context, withdrawalID string) (*domain.Withdrawal, domain.CurrentAccount, string, error) {
	var zeroAccount domain.CurrentAccount

	withdrawal, err := s.withdrawalRepo.FindByReference(ctx, withdrawalID)
	if err != nil {
		if errors.Is(err, persistence.ErrWithdrawalNotFound) {
			return nil, zeroAccount, opStatusWithdrawalNotFound,
				status.Errorf(codes.NotFound, "withdrawal not found: %s", withdrawalID)
		}
		s.logger.Error("failed to retrieve withdrawal",
			"withdrawal_id", withdrawalID,
			"error", err)
		return nil, zeroAccount, opStatusRetrieveFailed,
			status.Errorf(codes.Internal, "failed to retrieve withdrawal: %v", err)
	}

	if !withdrawal.IsPending() {
		return nil, zeroAccount, "withdrawal_not_pending",
			status.Errorf(codes.FailedPrecondition,
				"cannot update withdrawal %s: not in pending status (current: %s)",
				withdrawalID, withdrawal.Status)
	}

	account, err := s.repo.FindByUUID(ctx, withdrawal.AccountID)
	if err != nil {
		if errors.Is(err, persistence.ErrAccountNotFound) {
			return nil, zeroAccount, opStatusAccountNotFound,
				status.Errorf(codes.NotFound, "account not found for withdrawal")
		}
		return nil, zeroAccount, opStatusRetrieveFailed,
			status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	return withdrawal, account, "", nil
}

// buildUpdateWithdrawalWarnings generates warnings for unsupported field updates.
func buildUpdateWithdrawalWarnings(req *pb.UpdateWithdrawalRequest, withdrawal *domain.Withdrawal) []string {
	var msgs []string
	if req.Amount != nil {
		msgs = append(msgs, "Warning: amount updates are not yet supported; withdrawal amount unchanged")
	}
	if req.Description != "" {
		msgs = append(msgs, "Warning: description updates are not yet supported")
	}
	if req.Reference != "" && req.Reference != withdrawal.Reference {
		msgs = append(msgs, "Warning: reference updates are not yet supported")
	}
	return msgs
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
