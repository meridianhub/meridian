package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	"github.com/meridianhub/meridian/services/internal-account/domain"
	ibaobservability "github.com/meridianhub/meridian/services/internal-account/observability"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RetrieveInternalAccount fetches a single account by ID.
func (s *Service) RetrieveInternalAccount(ctx context.Context, req *pb.RetrieveInternalAccountRequest) (*pb.RetrieveInternalAccountResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		ibaobservability.RecordOperationDuration("retrieve_internal_account", operationStatus, time.Since(start))
	}()

	account, err := s.findAccountByID(ctx, req.AccountId)
	if err != nil {
		operationStatus = opStatusAccountNotFound
		return nil, err
	}

	return &pb.RetrieveInternalAccountResponse{
		Facility: toProtoFacility(account),
	}, nil
}

// ListInternalAccounts queries accounts with filtering and pagination.
func (s *Service) ListInternalAccounts(ctx context.Context, req *pb.ListInternalAccountsRequest) (*pb.ListInternalAccountsResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		ibaobservability.RecordOperationDuration("list_internal_accounts", operationStatus, time.Since(start))
	}()

	// Build filter from request
	filter, err := buildListFilter(req)
	if err != nil {
		operationStatus = operationStatusFailed
		return nil, err
	}

	// Query repository
	accounts, err := s.repo.List(ctx, filter)
	if err != nil {
		operationStatus = operationStatusFailed
		return nil, status.Errorf(codes.Internal, "failed to list accounts: %v", err)
	}

	// Convert to proto
	facilities := make([]*pb.InternalAccountFacility, len(accounts))
	for i, account := range accounts {
		facilities[i] = toProtoFacility(account)
	}

	// Build pagination response
	var nextPageToken string
	if len(accounts) == filter.Limit {
		nextPageToken = fmt.Sprintf("%d", filter.Offset+filter.Limit)
	}

	return &pb.ListInternalAccountsResponse{
		Facilities: facilities,
		Pagination: &commonpb.PaginationResponse{
			NextPageToken: nextPageToken,
		},
	}, nil
}

// buildListFilter constructs a domain ListFilter from a proto request.
func buildListFilter(req *pb.ListInternalAccountsRequest) (domain.ListFilter, error) {
	filter := domain.ListFilter{
		Limit:  50,
		Offset: 0,
	}

	if req.BehaviorClassFilter != "" {
		accountType := domain.AccountType(req.BehaviorClassFilter)
		filter.AccountType = &accountType
	}

	if req.InstrumentCodeFilter != "" {
		filter.InstrumentCode = &req.InstrumentCodeFilter
	}

	if req.StatusFilter != pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_UNSPECIFIED {
		accountStatus, err := protoToAccountStatus(req.StatusFilter)
		if err == nil {
			filter.Status = &accountStatus
		}
	}

	if req.ClearingPurposeFilter != pb.ClearingPurpose_CLEARING_PURPOSE_UNSPECIFIED {
		clearingPurpose, err := protoToClearingPurpose(req.ClearingPurposeFilter)
		if err == nil {
			filter.ClearingPurpose = &clearingPurpose
		}
	}

	if req.OrgPartyIdFilter != "" {
		orgPartyID, err := uuid.Parse(req.OrgPartyIdFilter)
		if err != nil {
			return filter, status.Errorf(codes.InvalidArgument, "invalid org_party_id_filter: %v", err)
		}
		filter.OrgPartyID = &orgPartyID
	}

	if req.Pagination != nil {
		if req.Pagination.PageSize > 0 {
			filter.Limit = int(req.Pagination.PageSize)
		}
		if req.Pagination.PageToken != "" {
			var offset int
			if _, err := fmt.Sscanf(req.Pagination.PageToken, "%d", &offset); err == nil {
				filter.Offset = offset
			}
		}
	}

	return filter, nil
}
