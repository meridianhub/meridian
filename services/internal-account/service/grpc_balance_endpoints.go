package service

import (
	"context"
	"strings"
	"time"

	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/internal-account/domain"
	ibaobservability "github.com/meridianhub/meridian/services/internal-account/observability"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// GetBalance queries the balance for an internal account from Position Keeping service.
func (s *Service) GetBalance(ctx context.Context, req *pb.GetBalanceRequest) (*pb.GetBalanceResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		ibaobservability.RecordOperationDuration("get_balance", operationStatus, time.Since(start))
	}()

	if strings.TrimSpace(req.AccountId) == "" {
		operationStatus = operationStatusFailed
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}

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

	// Position Keeping client must be configured for balance queries.
	// Decision: KEEP this nil guard (see ADR-0031). Rationale:
	//   - Provides explicit error message instead of nil pointer panic
	//   - Supports constructors that omit PK client (NewService, NewServiceWithValuationFeatures)
	//   - Zero performance cost (single pointer comparison)
	//   - Future refactoring may make PK optional for other balance sources
	if s.positionKeepingClient == nil {
		operationStatus = operationStatusFailed
		return nil, status.Error(codes.Unimplemented, "position keeping service not configured")
	}

	balanceResp, err := s.queryPositionKeepingBalance(ctx, account, req.AccountId)
	if err != nil {
		operationStatus = opStatusPositionKeepingError
		return nil, err
	}

	return buildGetBalanceResponse(req.AccountId, balanceResp), nil
}

// queryPositionKeepingBalance calls the Position Keeping service to fetch account balances.
func (s *Service) queryPositionKeepingBalance(ctx context.Context, account domain.InternalAccount, accountID string) (*positionkeepingv1.GetAccountBalancesResponse, error) {
	pkCtx, pkCancel := context.WithTimeout(ctx, 5*time.Second)
	defer pkCancel()

	pkStart := time.Now()
	balanceResp, err := s.positionKeepingClient.GetAccountBalances(pkCtx, &positionkeepingv1.GetAccountBalancesRequest{
		AccountId:      account.AccountID(),
		InstrumentCode: account.InstrumentCode(),
	})
	pkDuration := time.Since(pkStart)

	if err != nil {
		ibaobservability.RecordBalanceQueryDuration(operationStatusFailed, pkDuration)
		s.logger.Error("failed to query balance from Position Keeping",
			"account_id", accountID,
			"duration_ms", pkDuration.Milliseconds(),
			"error", err)
		return nil, mapPositionKeepingErrorToGRPC(err)
	}

	ibaobservability.RecordBalanceQueryDuration(operationStatusSuccess, pkDuration)
	return balanceResp, nil
}

// buildGetBalanceResponse constructs a GetBalanceResponse from a Position Keeping balance response.
func buildGetBalanceResponse(accountID string, balanceResp *positionkeepingv1.GetAccountBalancesResponse) *pb.GetBalanceResponse {
	asOf := balanceResp.GetAsOf()
	if asOf == nil {
		asOf = timestamppb.Now()
	}

	var currentBalance *quantityv1.InstrumentAmount
	for _, entry := range balanceResp.GetBalances() {
		if entry.GetBalanceType() == positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT {
			currentBalance = entry.GetAmount()
			break
		}
	}

	return &pb.GetBalanceResponse{
		AccountId:      accountID,
		CurrentBalance: currentBalance,
		AsOf:           asOf,
	}
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
