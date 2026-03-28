package service

import (
	"context"
	"errors"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
)

// RecordReservation creates a reservation linked to a lien.
// Idempotent by lien_id: if a reservation already exists, returns success with already_exists=true.
func (s *PositionKeepingService) RecordReservation(
	ctx context.Context,
	req *positionkeepingv1.RecordReservationRequest,
) (*positionkeepingv1.RecordReservationResponse, error) {
	if s.reservationRepo == nil {
		return nil, status.Error(codes.FailedPrecondition, "reservation repository not configured")
	}

	// Parse and validate lien_id
	lienID, err := parseUUID(req.GetLienId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid lien_id: %v", err)
	}

	// Parse reserved amount
	reservedAmount, err := decimal.NewFromString(req.GetReservedAmount())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid reserved_amount: %v", err)
	}

	// Validate required fields
	if req.GetAccountId() == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}
	if req.GetInstrumentCode() == "" {
		return nil, status.Error(codes.InvalidArgument, "instrument_code is required")
	}

	// Create domain reservation
	reservation, err := domain.NewReservation(
		lienID,
		req.GetAccountId(),
		req.GetInstrumentCode(),
		req.GetBucketId(),
		reservedAmount,
	)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid reservation: %v", err)
	}

	// Persist
	err = s.reservationRepo.Create(ctx, reservation)
	if err != nil {
		if errors.Is(err, domain.ErrConflict) {
			// Idempotent: reservation already exists for this lien_id
			return &positionkeepingv1.RecordReservationResponse{
				ReservationId: lienID.String(),
				AlreadyExists: true,
			}, nil
		}
		return nil, status.Errorf(codes.Internal, "failed to create reservation: %v", err)
	}

	return &positionkeepingv1.RecordReservationResponse{
		ReservationId: lienID.String(),
		AlreadyExists: false,
	}, nil
}

// ReleaseReservation transitions a reservation to EXECUTED or TERMINATED.
func (s *PositionKeepingService) ReleaseReservation(
	ctx context.Context,
	req *positionkeepingv1.ReleaseReservationRequest,
) (*positionkeepingv1.ReleaseReservationResponse, error) {
	if s.reservationRepo == nil {
		return nil, status.Error(codes.FailedPrecondition, "reservation repository not configured")
	}

	// Parse lien_id
	lienID, err := parseUUID(req.GetLienId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid lien_id: %v", err)
	}

	// Convert proto reason to domain status
	var newStatus domain.ReservationStatus
	switch req.GetReason() {
	case positionkeepingv1.ReservationStatus_RESERVATION_STATUS_EXECUTED:
		newStatus = domain.ReservationStatusExecuted
	case positionkeepingv1.ReservationStatus_RESERVATION_STATUS_TERMINATED:
		newStatus = domain.ReservationStatusTerminated
	case positionkeepingv1.ReservationStatus_RESERVATION_STATUS_UNSPECIFIED,
		positionkeepingv1.ReservationStatus_RESERVATION_STATUS_ACTIVE:
		return nil, status.Errorf(codes.InvalidArgument, "reason must be EXECUTED or TERMINATED, got %v", req.GetReason())
	}

	// Update status
	err = s.reservationRepo.UpdateStatus(ctx, lienID, newStatus)
	if err != nil {
		if errors.Is(err, domain.ErrReservationNotFound) {
			return nil, status.Errorf(codes.NotFound, "reservation not found for lien_id: %s", lienID)
		}
		if errors.Is(err, domain.ErrReservationAlreadyFinal) {
			return nil, status.Errorf(codes.FailedPrecondition, "reservation is already in a terminal state")
		}
		return nil, status.Errorf(codes.Internal, "failed to release reservation: %v", err)
	}

	return &positionkeepingv1.ReleaseReservationResponse{
		Released: true,
	}, nil
}

// GetProjectedBalance calculates the projected balance accounting for active reservations.
func (s *PositionKeepingService) GetProjectedBalance(
	ctx context.Context,
	req *positionkeepingv1.GetProjectedBalanceRequest,
) (*positionkeepingv1.GetProjectedBalanceResponse, error) {
	if err := validateProjectedBalanceRequest(req, s.reservationRepo, s.positionRepo); err != nil {
		return nil, err
	}

	accountID := req.GetAccountId()
	instrumentCode := req.GetInstrumentCode()
	bucketID := req.GetBucketId()

	currentBalance, err := s.queryCurrentBalance(ctx, accountID, instrumentCode, bucketID)
	if err != nil {
		return nil, err
	}

	activeReservationsTotal, err := s.reservationRepo.SumActiveReservations(ctx, accountID, instrumentCode, bucketID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to sum active reservations: %v", err)
	}

	projectedBalance := currentBalance.Sub(activeReservationsTotal)

	return &positionkeepingv1.GetProjectedBalanceResponse{
		CurrentBalance:          currentBalance.String(),
		ActiveReservationsTotal: activeReservationsTotal.String(),
		ProjectedBalance:        projectedBalance.String(),
		BucketId:                bucketID,
		InstrumentCode:          instrumentCode,
		AsOf:                    timestamppb.New(time.Now().UTC()),
	}, nil
}

// validateProjectedBalanceRequest validates the GetProjectedBalance request preconditions.
func validateProjectedBalanceRequest(req *positionkeepingv1.GetProjectedBalanceRequest, reservationRepo interface{}, positionRepo interface{}) error {
	if reservationRepo == nil {
		return status.Error(codes.FailedPrecondition, "reservation repository not configured")
	}
	if positionRepo == nil {
		return status.Error(codes.FailedPrecondition, "position repository not configured")
	}
	if req.GetAccountId() == "" {
		return status.Error(codes.InvalidArgument, "account_id is required")
	}
	if req.GetInstrumentCode() == "" {
		return status.Error(codes.InvalidArgument, "instrument_code is required")
	}
	return nil
}

// queryCurrentBalance retrieves the current balance from position entries.
func (s *PositionKeepingService) queryCurrentBalance(ctx context.Context, accountID, instrumentCode, bucketID string) (decimal.Decimal, error) {
	if bucketID != "" {
		agg, err := s.positionRepo.GetAggregatedPosition(ctx, accountID, instrumentCode, bucketID)
		if err != nil {
			return decimal.Decimal{}, status.Errorf(codes.Internal, "failed to query position balance: %v", err)
		}
		if agg != nil {
			return agg.TotalAmount, nil
		}
		return decimal.Decimal{}, nil
	}

	aggs, err := s.positionRepo.GetAggregatedPositions(ctx, accountID, instrumentCode)
	if err != nil {
		return decimal.Decimal{}, status.Errorf(codes.Internal, "failed to query position balances: %v", err)
	}
	var total decimal.Decimal
	for _, agg := range aggs {
		total = total.Add(agg.TotalAmount)
	}
	return total, nil
}
