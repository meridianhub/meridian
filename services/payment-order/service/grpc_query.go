package service

import (
	"context"
	"errors"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	"github.com/meridianhub/meridian/services/payment-order/adapters/persistence"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/samber/lo"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RetrievePaymentOrder gets payment order details by ID.
func (s *Service) RetrievePaymentOrder(ctx context.Context, req *pb.RetrievePaymentOrderRequest) (*pb.RetrievePaymentOrderResponse, error) {
	// Parse payment order ID
	poID, err := uuid.Parse(req.PaymentOrderId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid payment order ID: %v", err)
	}

	// Retrieve from repository
	po, err := s.repo.FindByID(ctx, poID)
	if err != nil {
		if errors.Is(err, persistence.ErrPaymentOrderNotFound) {
			return nil, status.Errorf(codes.NotFound, "payment order not found: %s", req.PaymentOrderId)
		}
		s.logger.Error("failed to retrieve payment order", "error", err)
		return nil, status.Error(codes.Internal, "failed to retrieve payment order")
	}

	return &pb.RetrievePaymentOrderResponse{
		PaymentOrder: toProto(po),
	}, nil
}

// ListPaymentOrders returns a paginated list of payment orders.
// Uses cursor-based pagination for consistent results even when items are inserted/deleted.
// The cursor is an opaque token encoding (created_at, id) for deterministic ordering.
func (s *Service) ListPaymentOrders(ctx context.Context, req *pb.ListPaymentOrdersRequest) (*pb.ListPaymentOrdersResponse, error) {
	if req.DebtorAccountId == "" {
		return nil, status.Error(codes.InvalidArgument, "debtor_account_id is required for listing")
	}

	// Parse and validate pagination parameters
	pageSize := s.defaultPageSize
	if req.Pagination != nil && req.Pagination.PageSize > 0 {
		pageSize = int(req.Pagination.PageSize)
		if pageSize > s.maxPageSize {
			pageSize = s.maxPageSize
		}
	}

	// Decode cursor from page token (empty token = first page)
	var cursor persistence.Cursor
	if req.Pagination != nil && req.Pagination.PageToken != "" {
		var err error
		cursor, err = persistence.DecodeCursor(req.Pagination.PageToken)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid page_token")
		}
	}

	// Query with cursor-based pagination
	result, err := s.repo.FindByDebtorAccountIDWithCursor(ctx, req.DebtorAccountId, pageSize, cursor)
	if err != nil {
		s.logger.Error("failed to list payment orders", "error", err)
		return nil, status.Error(codes.Internal, "failed to list payment orders")
	}

	return &pb.ListPaymentOrdersResponse{
		PaymentOrders: lo.Map(result.PaymentOrders, func(po *domain.PaymentOrder, _ int) *pb.PaymentOrder {
			return toProto(po)
		}),
		Pagination: &commonpb.PaginationResponse{
			NextPageToken: result.NextCursor,
			TotalCount:    result.TotalCount,
		},
	}, nil
}
