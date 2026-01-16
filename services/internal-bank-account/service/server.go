// Package service implements gRPC services for the internal bank account domain.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
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
	opStatusInstrumentNotActive     = "instrument_not_active"
	opStatusInstrumentValidationErr = "instrument_validation_error"
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

	// Validate instrument exists and is ACTIVE via Reference Data service (if client is configured)
	var dimension string
	if s.referenceDataClient != nil {
		validationStart := time.Now()
		refDataCtx, refDataCancel := context.WithTimeout(ctx, 5*time.Second)
		defer refDataCancel()

		refDataResp, err := s.referenceDataClient.RetrieveInstrument(refDataCtx, &referencedatav1.RetrieveInstrumentRequest{
			Code: req.InstrumentCode,
		})
		if err != nil {
			validationDuration := time.Since(validationStart)
			errCode := status.Code(err)
			s.logger.Warn("instrument validation failed",
				"instrument_code", req.InstrumentCode,
				"error", err)

			if errCode == codes.NotFound {
				operationStatus = opStatusInstrumentNotFound
				ibaobservability.RecordInstrumentValidation("not_found", validationDuration)
				return nil, status.Errorf(codes.InvalidArgument, "instrument not found: %s", req.InstrumentCode)
			}
			if errCode == codes.DeadlineExceeded || errCode == codes.Canceled {
				operationStatus = opStatusInstrumentValidationErr
				ibaobservability.RecordInstrumentValidation("timeout", validationDuration)
				return nil, status.Errorf(codes.DeadlineExceeded, "instrument validation timed out for: %s", req.InstrumentCode)
			}
			operationStatus = opStatusInstrumentValidationErr
			ibaobservability.RecordInstrumentValidation("error", validationDuration)
			return nil, status.Errorf(codes.Internal, "failed to validate instrument: %v", err)
		}

		// Guard against nil instrument in response (defensive programming)
		if refDataResp.Instrument == nil {
			validationDuration := time.Since(validationStart)
			operationStatus = opStatusInstrumentValidationErr
			s.logger.Error("reference data service returned nil instrument",
				"instrument_code", req.InstrumentCode)
			ibaobservability.RecordInstrumentValidation("error", validationDuration)
			return nil, status.Errorf(codes.Internal, "reference data service returned invalid response for instrument: %s", req.InstrumentCode)
		}

		// Validate instrument status is ACTIVE
		if refDataResp.Instrument.Status != referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE {
			validationDuration := time.Since(validationStart)
			operationStatus = opStatusInstrumentNotActive
			s.logger.Warn("instrument not active",
				"instrument_code", req.InstrumentCode,
				"status", refDataResp.Instrument.Status.String())
			ibaobservability.RecordInstrumentValidation("not_active", validationDuration)
			return nil, status.Errorf(codes.InvalidArgument, "instrument %s is not active (status: %s)",
				req.InstrumentCode, refDataResp.Instrument.Status.String())
		}

		// Extract dimension from validated instrument (strip DIMENSION_ prefix for domain consistency)
		dimension = strings.TrimPrefix(refDataResp.Instrument.Dimension.String(), "DIMENSION_")
		ibaobservability.RecordInstrumentValidation("success", time.Since(validationStart))
	}

	// Generate account ID using full UUID for uniqueness
	accountID := fmt.Sprintf("IBA-%s", uuid.New().String())

	// Convert clearing purpose from proto to domain
	clearingPurpose, err := protoToClearingPurpose(req.ClearingPurpose)
	if err != nil {
		operationStatus = operationStatusFailed
		return nil, status.Errorf(codes.InvalidArgument, "invalid clearing purpose: %v", err)
	}

	// Create domain entity with dimension from Reference Data
	account, err := domain.NewInternalBankAccount(
		accountID,
		req.AccountCode,
		req.Name,
		accountType,
		clearingPurpose,
		req.InstrumentCode,
		dimension,
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

	// Record metric for account creation
	ibaobservability.RecordAccountCreated(accountType.String())

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

	var err error

	// Update name if provided
	if req.Name != "" {
		account, err = account.Rename(req.Name)
		if err != nil {
			*operationStatus = operationStatusFailed
			return nil, mapDomainErrorToGRPC(err)
		}
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

	// Capture previous status for metrics
	previousStatus := account.Status()

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

	// Record status change metric
	ibaobservability.RecordAccountStatusChange(string(previousStatus), string(account.Status()))

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

	// Apply clearing purpose filter
	if req.ClearingPurposeFilter != pb.ClearingPurpose_CLEARING_PURPOSE_UNSPECIFIED {
		clearingPurpose, err := protoToClearingPurpose(req.ClearingPurposeFilter)
		if err == nil {
			filter.ClearingPurpose = &clearingPurpose
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

	// Query Position Keeping service (source of truth for balance) with timeout
	pkCtx, pkCancel := context.WithTimeout(ctx, 5*time.Second)
	defer pkCancel()

	pkStart := time.Now()
	balanceResp, err := s.positionKeepingClient.GetAccountBalances(pkCtx, &positionkeepingv1.GetAccountBalancesRequest{
		AccountId:      account.AccountID(),
		InstrumentCode: account.InstrumentCode(),
	})
	pkDuration := time.Since(pkStart)

	if err != nil {
		operationStatus = opStatusPositionKeepingError
		ibaobservability.RecordBalanceQueryDuration(operationStatusFailed, pkDuration)
		s.logger.Error("failed to query balance from Position Keeping",
			"account_id", req.AccountId,
			"duration_ms", pkDuration.Milliseconds(),
			"error", err)
		// Map Position Keeping errors to appropriate gRPC codes
		return nil, mapPositionKeepingErrorToGRPC(err)
	}

	// Record successful balance query duration (target <50ms p99)
	ibaobservability.RecordBalanceQueryDuration(operationStatusSuccess, pkDuration)

	// Find the current balance from the response.
	// AsOf reflects the timestamp from Position Keeping when the balance was calculated.
	var currentBalanceResp *pb.GetBalanceResponse
	for _, entry := range balanceResp.GetBalances() {
		if entry.GetBalanceType() == positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT {
			currentBalanceResp = &pb.GetBalanceResponse{
				AccountId:      req.AccountId,
				CurrentBalance: entry.GetAmount(),
				AsOf:           balanceResp.GetAsOf(), // Use Position Keeping's as_of timestamp
			}
			break
		}
	}

	if currentBalanceResp == nil {
		// No current balance found - return zero balance with current time
		return &pb.GetBalanceResponse{
			AccountId:      req.AccountId,
			CurrentBalance: nil,
			AsOf:           timestamppb.Now(),
		}, nil
	}

	return currentBalanceResp, nil
}

// mapPositionKeepingErrorToGRPC maps Position Keeping service errors to appropriate gRPC status codes.
func mapPositionKeepingErrorToGRPC(err error) error {
	st, ok := status.FromError(err)
	if !ok {
		// Non-gRPC error - treat as unavailable
		return status.Errorf(codes.Unavailable, "position keeping service unavailable: %v", err)
	}

	//exhaustive:ignore
	switch st.Code() {
	case codes.NotFound:
		// Position/account not found in Position Keeping - internal error from our perspective
		return status.Errorf(codes.Internal, "balance not found in position keeping: %v", st.Message())
	case codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted:
		// Service unavailable - map to Unavailable
		return status.Errorf(codes.Unavailable, "position keeping service unavailable: %v", st.Message())
	case codes.InvalidArgument:
		// Bad request to Position Keeping - internal error (our code is wrong)
		return status.Errorf(codes.Internal, "invalid request to position keeping: %v", st.Message())
	default:
		// Other errors - map to Internal
		return status.Errorf(codes.Internal, "failed to retrieve balance: %v", st.Message())
	}
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
