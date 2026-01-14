// Package service implements gRPC services for the internal bank account domain.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_bank_account/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"github.com/meridianhub/meridian/services/internal-bank-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/internal-bank-account/domain"
	ibaobservability "github.com/meridianhub/meridian/services/internal-bank-account/observability"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Operation status constants for metrics and logging.
const (
	operationStatusSuccess = "success"
	operationStatusFailed  = "failed"

	opStatusAccountNotFound         = "account_not_found"
	opStatusInvalidAccountType      = "invalid_account_type"
	opStatusInvalidStatusTransition = "invalid_status_transition"
	opStatusVersionConflict         = "version_conflict"
	opStatusDuplicateCode           = "duplicate_code"
	opStatusInstrumentNotFound      = "instrument_not_found"
	opStatusPositionKeepingError    = "position_keeping_error"
)

// Service implements the InternalBankAccountService gRPC service.
type Service struct {
	pb.UnimplementedInternalBankAccountServiceServer
	repo                  domain.Repository
	positionKeepingClient PositionKeepingClient
	referenceDataClient   ReferenceDataClient
	logger                *slog.Logger
	tracer                *observability.Tracer
}

// NewService creates a new internal bank account service with minimal dependencies.
// This is primarily used for testing. For production use, prefer NewServiceWithClients.
// Returns an error if repository is nil.
func NewService(repo domain.Repository) (*Service, error) {
	if repo == nil {
		return nil, ErrRepositoryNil
	}
	return &Service{
		repo:   repo,
		logger: slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}, nil
}

// NewServiceWithClients creates a new service with external client dependencies.
// Use this constructor for production deployments where Position Keeping and Reference Data
// service integrations are required.
func NewServiceWithClients(
	repo domain.Repository,
	posKeepingClient PositionKeepingClient,
	refDataClient ReferenceDataClient,
	logger *slog.Logger,
	tracer *observability.Tracer,
) (*Service, error) {
	if repo == nil {
		return nil, ErrRepositoryNil
	}

	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}

	return &Service{
		repo:                  repo,
		positionKeepingClient: posKeepingClient,
		referenceDataClient:   refDataClient,
		logger:                logger,
		tracer:                tracer,
	}, nil
}

// InitiateInternalBankAccount creates a new internal bank account.
func (s *Service) InitiateInternalBankAccount(ctx context.Context, req *pb.InitiateInternalBankAccountRequest) (*pb.InitiateInternalBankAccountResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		ibaobservability.RecordOperationDuration("initiate_internal_bank_account", operationStatus, time.Since(start))
	}()

	// Map proto account type to domain
	accountType, err := protoToAccountType(req.AccountType)
	if err != nil {
		operationStatus = opStatusInvalidAccountType
		return nil, status.Errorf(codes.InvalidArgument, "invalid account type: %v", err)
	}

	// Validate instrument exists via Reference Data service (if client is configured)
	if s.referenceDataClient != nil {
		_, err := s.referenceDataClient.RetrieveInstrument(ctx, &referencedatav1.RetrieveInstrumentRequest{
			Code: req.InstrumentCode,
		})
		if err != nil {
			operationStatus = opStatusInstrumentNotFound
			s.logger.Warn("instrument validation failed",
				"instrument_code", req.InstrumentCode,
				"error", err)
			if status.Code(err) == codes.NotFound {
				return nil, status.Errorf(codes.InvalidArgument, "instrument not found: %s", req.InstrumentCode)
			}
			return nil, status.Errorf(codes.Internal, "failed to validate instrument: %v", err)
		}
	}

	// Generate account ID
	accountID := fmt.Sprintf("IBA-%s", uuid.New().String()[:8])

	// Create domain entity
	account, err := domain.NewInternalBankAccount(
		accountID,
		req.AccountCode,
		req.Name,
		accountType,
		req.InstrumentCode,
		"", // dimension - could be populated from Reference Data response
	)
	if err != nil {
		operationStatus = operationStatusFailed
		return nil, mapDomainErrorToGRPC(err)
	}

	// Handle correspondent details for NOSTRO/VOSTRO accounts
	if req.CorrespondentDetails != nil {
		correspondent, err := domain.NewCorrespondentDetailsWithOptions(
			req.CorrespondentDetails.BankId,
			req.CorrespondentDetails.BankName,
			req.CorrespondentDetails.ExternalAccountRef,
			req.CorrespondentDetails.SwiftCode,
			nil,
		)
		if err != nil {
			operationStatus = operationStatusFailed
			return nil, status.Errorf(codes.InvalidArgument, "invalid correspondent details: %v", err)
		}
		account, err = account.UpdateCorrespondent(correspondent)
		if err != nil {
			operationStatus = operationStatusFailed
			return nil, mapDomainErrorToGRPC(err)
		}
	} else if accountType.RequiresCorrespondent() {
		operationStatus = operationStatusFailed
		return nil, status.Errorf(codes.InvalidArgument, "correspondent details required for %s accounts", accountType)
	}

	// Persist via repository
	if err := s.repo.Save(ctx, account); err != nil {
		if errors.Is(err, persistence.ErrDuplicateCode) {
			operationStatus = opStatusDuplicateCode
			return nil, status.Errorf(codes.AlreadyExists, "account code already exists: %s", req.AccountCode)
		}
		operationStatus = operationStatusFailed
		s.logger.Error("failed to save account", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to create account: %v", err)
	}

	s.logger.Info("created internal bank account",
		"account_id", accountID,
		"account_code", req.AccountCode,
		"account_type", accountType.String())

	return &pb.InitiateInternalBankAccountResponse{
		AccountId: accountID,
		Facility:  toProtoFacility(account),
	}, nil
}

// UpdateInternalBankAccount modifies account settings.
func (s *Service) UpdateInternalBankAccount(ctx context.Context, req *pb.UpdateInternalBankAccountRequest) (*pb.UpdateInternalBankAccountResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		ibaobservability.RecordOperationDuration("update_internal_bank_account", operationStatus, time.Since(start))
	}()

	// Load existing account
	accountUUID, err := uuid.Parse(req.AccountId)
	if err != nil {
		// Try finding by account_id string if not a UUID
		account, err := s.findAccountByID(ctx, req.AccountId)
		if err != nil {
			operationStatus = opStatusAccountNotFound
			return nil, err
		}
		return s.updateAccount(ctx, account, req, &operationStatus)
	}

	account, err := s.repo.FindByID(ctx, accountUUID)
	if err != nil {
		if errors.Is(err, domain.ErrAccountNotFound) {
			operationStatus = opStatusAccountNotFound
			return nil, status.Errorf(codes.NotFound, "account not found: %s", req.AccountId)
		}
		operationStatus = operationStatusFailed
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	return s.updateAccount(ctx, account, req, &operationStatus)
}

func (s *Service) updateAccount(ctx context.Context, account domain.InternalBankAccount, req *pb.UpdateInternalBankAccountRequest, operationStatus *string) (*pb.UpdateInternalBankAccountResponse, error) {
	// Check version for optimistic locking if provided
	if req.ExpectedVersion > 0 && int64(req.ExpectedVersion) != account.Version() {
		*operationStatus = opStatusVersionConflict
		return nil, status.Errorf(codes.Aborted, "version mismatch: expected %d, got %d", req.ExpectedVersion, account.Version())
	}

	// Cannot update closed accounts
	if account.Status() == domain.AccountStatusClosed {
		*operationStatus = operationStatusFailed
		return nil, status.Error(codes.FailedPrecondition, "cannot update closed account")
	}

	// Update correspondent details if provided
	if req.CorrespondentDetails != nil {
		correspondent, err := domain.NewCorrespondentDetailsWithOptions(
			req.CorrespondentDetails.BankId,
			req.CorrespondentDetails.BankName,
			req.CorrespondentDetails.ExternalAccountRef,
			req.CorrespondentDetails.SwiftCode,
			nil,
		)
		if err != nil {
			*operationStatus = operationStatusFailed
			return nil, status.Errorf(codes.InvalidArgument, "invalid correspondent details: %v", err)
		}
		account, err = account.UpdateCorrespondent(correspondent)
		if err != nil {
			*operationStatus = operationStatusFailed
			return nil, mapDomainErrorToGRPC(err)
		}
	}

	// Persist changes
	if err := s.repo.Save(ctx, account); err != nil {
		if errors.Is(err, persistence.ErrVersionConflict) {
			*operationStatus = opStatusVersionConflict
			return nil, status.Error(codes.Aborted, "account was modified by another transaction")
		}
		*operationStatus = operationStatusFailed
		return nil, status.Errorf(codes.Internal, "failed to save account: %v", err)
	}

	return &pb.UpdateInternalBankAccountResponse{
		Facility: toProtoFacility(account),
	}, nil
}

// ControlInternalBankAccount performs lifecycle state transitions.
func (s *Service) ControlInternalBankAccount(ctx context.Context, req *pb.ControlInternalBankAccountRequest) (*pb.ControlInternalBankAccountResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		ibaobservability.RecordOperationDuration("control_internal_bank_account", operationStatus, time.Since(start))
	}()

	// Load existing account
	account, err := s.findAccountByID(ctx, req.AccountId)
	if err != nil {
		operationStatus = opStatusAccountNotFound
		return nil, err
	}

	// Execute state transition based on control action
	switch req.ControlAction {
	case pb.ControlAction_CONTROL_ACTION_SUSPEND:
		account, err = account.Suspend(req.Reason)
	case pb.ControlAction_CONTROL_ACTION_ACTIVATE:
		account, err = account.Activate()
	case pb.ControlAction_CONTROL_ACTION_CLOSE:
		account, err = account.Close(req.Reason)
	case pb.ControlAction_CONTROL_ACTION_UNSPECIFIED:
		operationStatus = operationStatusFailed
		return nil, status.Errorf(codes.InvalidArgument, "control action must be specified")
	default:
		operationStatus = operationStatusFailed
		return nil, status.Errorf(codes.InvalidArgument, "invalid control action: %v", req.ControlAction)
	}

	if err != nil {
		if errors.Is(err, domain.ErrInvalidStatusTransition) {
			operationStatus = opStatusInvalidStatusTransition
			return nil, status.Errorf(codes.FailedPrecondition, "invalid status transition: %v", err)
		}
		operationStatus = operationStatusFailed
		return nil, mapDomainErrorToGRPC(err)
	}

	// Persist changes
	if err := s.repo.Save(ctx, account); err != nil {
		if errors.Is(err, persistence.ErrVersionConflict) {
			operationStatus = opStatusVersionConflict
			return nil, status.Error(codes.Aborted, "account was modified by another transaction")
		}
		operationStatus = operationStatusFailed
		return nil, status.Errorf(codes.Internal, "failed to save account: %v", err)
	}

	s.logger.Info("executed control action on internal bank account",
		"account_id", req.AccountId,
		"action", req.ControlAction.String(),
		"new_status", string(account.Status()))

	return &pb.ControlInternalBankAccountResponse{
		Facility:        toProtoFacility(account),
		ActionTimestamp: timestamppb.Now(),
	}, nil
}

// RetrieveInternalBankAccount fetches a single account by ID.
func (s *Service) RetrieveInternalBankAccount(ctx context.Context, req *pb.RetrieveInternalBankAccountRequest) (*pb.RetrieveInternalBankAccountResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		ibaobservability.RecordOperationDuration("retrieve_internal_bank_account", operationStatus, time.Since(start))
	}()

	account, err := s.findAccountByID(ctx, req.AccountId)
	if err != nil {
		operationStatus = opStatusAccountNotFound
		return nil, err
	}

	return &pb.RetrieveInternalBankAccountResponse{
		Facility: toProtoFacility(account),
	}, nil
}

// ListInternalBankAccounts queries accounts with filtering and pagination.
func (s *Service) ListInternalBankAccounts(ctx context.Context, req *pb.ListInternalBankAccountsRequest) (*pb.ListInternalBankAccountsResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		ibaobservability.RecordOperationDuration("list_internal_bank_accounts", operationStatus, time.Since(start))
	}()

	// Build filter
	filter := domain.ListFilter{
		Limit:  50, // Default
		Offset: 0,
	}

	// Apply account type filter
	if req.AccountTypeFilter != pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_UNSPECIFIED {
		accountType, err := protoToAccountType(req.AccountTypeFilter)
		if err == nil {
			filter.AccountType = &accountType
		}
	}

	// Apply instrument code filter
	if req.InstrumentCodeFilter != "" {
		filter.InstrumentCode = &req.InstrumentCodeFilter
	}

	// Apply status filter
	if req.StatusFilter != pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_UNSPECIFIED {
		accountStatus, err := protoToAccountStatus(req.StatusFilter)
		if err == nil {
			filter.Status = &accountStatus
		}
	}

	// Apply pagination
	if req.Pagination != nil {
		if req.Pagination.PageSize > 0 {
			filter.Limit = int(req.Pagination.PageSize)
		}
		// Parse page_token as offset (simple offset-based pagination)
		if req.Pagination.PageToken != "" {
			var offset int
			if _, err := fmt.Sscanf(req.Pagination.PageToken, "%d", &offset); err == nil {
				filter.Offset = offset
			}
		}
	}

	// Query repository
	accounts, err := s.repo.List(ctx, filter)
	if err != nil {
		operationStatus = operationStatusFailed
		return nil, status.Errorf(codes.Internal, "failed to list accounts: %v", err)
	}

	// Convert to proto
	facilities := make([]*pb.InternalBankAccountFacility, len(accounts))
	for i, account := range accounts {
		facilities[i] = toProtoFacility(account)
	}

	// Build pagination response
	var nextPageToken string
	if len(accounts) == filter.Limit {
		nextPageToken = fmt.Sprintf("%d", filter.Offset+filter.Limit)
	}

	return &pb.ListInternalBankAccountsResponse{
		Facilities: facilities,
		Pagination: &commonpb.PaginationResponse{
			NextPageToken: nextPageToken,
		},
	}, nil
}

// GetBalance queries the balance for an internal account from Position Keeping service.
func (s *Service) GetBalance(ctx context.Context, req *pb.GetBalanceRequest) (*pb.GetBalanceResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		ibaobservability.RecordOperationDuration("get_balance", operationStatus, time.Since(start))
	}()

	// Validate account exists and is active
	account, err := s.findAccountByID(ctx, req.AccountId)
	if err != nil {
		operationStatus = opStatusAccountNotFound
		return nil, err
	}

	// Only active accounts have queryable balances
	if account.Status() != domain.AccountStatusActive {
		operationStatus = operationStatusFailed
		return nil, status.Errorf(codes.FailedPrecondition, "account not active: %s", string(account.Status()))
	}

	// Position Keeping client must be configured for balance queries
	if s.positionKeepingClient == nil {
		operationStatus = operationStatusFailed
		return nil, status.Error(codes.Unimplemented, "position keeping service not configured")
	}

	// Query Position Keeping service (source of truth for balance)
	balanceResp, err := s.positionKeepingClient.GetAccountBalances(ctx, &positionkeepingv1.GetAccountBalancesRequest{
		AccountId:      account.AccountID(),
		InstrumentCode: account.InstrumentCode(),
	})
	if err != nil {
		operationStatus = opStatusPositionKeepingError
		s.logger.Error("failed to query balance from Position Keeping",
			"account_id", req.AccountId,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to retrieve balance: %v", err)
	}

	// Find the current balance from the response.
	// Note: LastUpdated reflects when this service queried Position Keeping,
	// not when the balance was last modified. Position Keeping is the source
	// of truth for balance timestamps if needed.
	var currentBalance *pb.GetBalanceResponse
	for _, entry := range balanceResp.GetBalances() {
		if entry.GetBalanceType() == positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT {
			currentBalance = &pb.GetBalanceResponse{
				AccountId:   req.AccountId,
				Balance:     entry.GetAmount(),
				LastUpdated: timestamppb.Now(), // Query time, not balance update time
			}
			break
		}
	}

	if currentBalance == nil {
		// No current balance found - return zero balance
		return &pb.GetBalanceResponse{
			AccountId:   req.AccountId,
			Balance:     nil,
			LastUpdated: timestamppb.Now(), // Query time
		}, nil
	}

	return currentBalance, nil
}

// findAccountByID finds an account by its ID (UUID or business ID).
func (s *Service) findAccountByID(ctx context.Context, accountID string) (domain.InternalBankAccount, error) {
	// First try to parse as UUID
	if id, err := uuid.Parse(accountID); err == nil {
		account, err := s.repo.FindByID(ctx, id)
		if err != nil {
			if errors.Is(err, domain.ErrAccountNotFound) {
				return domain.InternalBankAccount{}, status.Errorf(codes.NotFound, "account not found: %s", accountID)
			}
			return domain.InternalBankAccount{}, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
		}
		return account, nil
	}

	// Try to find by account code
	account, err := s.repo.FindByCode(ctx, accountID)
	if err != nil {
		if errors.Is(err, domain.ErrAccountNotFound) {
			return domain.InternalBankAccount{}, status.Errorf(codes.NotFound, "account not found: %s", accountID)
		}
		return domain.InternalBankAccount{}, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}
	return account, nil
}
