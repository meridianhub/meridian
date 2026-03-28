package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	"github.com/meridianhub/meridian/services/internal-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/internal-account/domain"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"time"
)

// validateInitiateLienInput validates the request fields and parses the input amount.
func validateInitiateLienInput(req *pb.InitiateLienRequest) (decimal.Decimal, string, error) {
	if req.Input == nil || req.Input.Amount == "" {
		return decimal.Zero, opStatusInvalidRequest, status.Error(codes.InvalidArgument, "input amount is required")
	}
	if req.Input.InstrumentCode == "" {
		return decimal.Zero, opStatusInvalidRequest, status.Error(codes.InvalidArgument, "input instrument_code is required")
	}
	if strings.TrimSpace(req.PaymentOrderReference) == "" {
		return decimal.Zero, opStatusInvalidRequest, status.Error(codes.InvalidArgument, "payment_order_reference is required")
	}

	inputAmount, err := decimal.NewFromString(req.Input.Amount)
	if err != nil {
		return decimal.Zero, opStatusInvalidInputAmount, status.Errorf(codes.InvalidArgument, "invalid input amount: %v", err)
	}
	if !inputAmount.IsPositive() {
		return decimal.Zero, opStatusInputAmountNonPositive, status.Error(codes.InvalidArgument, "input amount must be positive")
	}

	return inputAmount, "", nil
}

// checkInitiateLienIdempotency checks if a lien already exists for the payment order reference.
func (s *Service) checkInitiateLienIdempotency(ctx context.Context, paymentOrderRef string, account domain.InternalAccount) (*pb.InitiateLienResponse, string, error) {
	existingLien, err := s.lienRepo.FindByPaymentOrderReference(ctx, paymentOrderRef)
	if err == nil {
		if existingLien.AccountID != account.ID() {
			return nil, opStatusInvalidRequest, status.Errorf(codes.InvalidArgument,
				"payment_order_reference already used for a different account")
		}
		resp, buildErr := s.buildInitiateLienResponse(ctx, existingLien)
		if buildErr != nil {
			return nil, operationStatusFailed, buildErr
		}
		return resp, opStatusLienAlreadyExists, nil
	}
	if !errors.Is(err, persistence.ErrLienNotFound) {
		return nil, operationStatusFailed, status.Errorf(codes.Internal, "failed to check lien idempotency: %v", err)
	}
	return nil, "", nil
}

// createSameInstrumentLien creates a lien when the input instrument matches the account's native instrument.
func (s *Service) createSameInstrumentLien(ctx context.Context, account domain.InternalAccount, inputAmount decimal.Decimal, nativeInstrument string, req *pb.InitiateLienRequest, expiresAt *time.Time) (*domain.Lien, string, error) {
	precision, precisionErr := s.getInstrumentPrecision(ctx, nativeInstrument)
	if precisionErr != nil {
		return nil, operationStatusFailed, precisionErr
	}
	if !inputAmount.Equal(inputAmount.Truncate(precision)) {
		return nil, opStatusInvalidInputAmount, status.Errorf(codes.InvalidArgument, "input amount has more than %d decimal places for instrument %s", precision, nativeInstrument)
	}
	amountCents := inputAmount.Shift(precision).IntPart()

	lien, err := domain.NewLien(account.ID(), amountCents, nativeInstrument, req.BucketId, req.PaymentOrderReference, expiresAt)
	if err != nil {
		return nil, opStatusInvalidRequest, status.Errorf(codes.InvalidArgument, "failed to create lien: %v", err)
	}
	return lien, "", nil
}

// createCrossInstrumentLien creates a lien with atomic valuation when the input instrument differs from the account's native instrument.
func (s *Service) createCrossInstrumentLien(ctx context.Context, account domain.InternalAccount, inputAmount decimal.Decimal, nativeInstrument string, req *pb.InitiateLienRequest, knowledgeAt time.Time, expiresAt *time.Time) (*domain.Lien, string, error) {
	result, err := s.valuateInternal(ctx, req.AccountId, inputAmount, req.Input.InstrumentCode, knowledgeAt)
	if err != nil {
		opStatus := mapValuationError(err)
		return nil, opStatus, mapValuationErrorToGRPC(err)
	}

	precision, precisionErr := s.getInstrumentPrecision(ctx, nativeInstrument)
	if precisionErr != nil {
		return nil, operationStatusFailed, precisionErr
	}
	if !result.OutputAmount.Equal(result.OutputAmount.Truncate(precision)) {
		return nil, opStatusValuationFailed, status.Errorf(codes.Internal, "valued amount has more than %d decimal places for instrument %s", precision, nativeInstrument)
	}
	amountCents := result.OutputAmount.Shift(precision).IntPart()

	reservedQuantity := &domain.InstrumentAmount{
		Amount:         inputAmount,
		InstrumentCode: req.Input.InstrumentCode,
	}
	valuedAmount := &domain.InstrumentAmount{
		Amount:         result.OutputAmount,
		InstrumentCode: result.OutputCode,
	}

	var analysisJSON json.RawMessage
	if result.Analysis != nil {
		data, marshalErr := json.Marshal(result.Analysis)
		if marshalErr != nil {
			s.logger.Warn("failed to marshal valuation analysis", "error", marshalErr)
		} else {
			analysisJSON = data
		}
	}

	lien, err := domain.NewValuedLien(
		account.ID(), amountCents, nativeInstrument, req.BucketId,
		req.PaymentOrderReference, expiresAt,
		reservedQuantity, valuedAmount, analysisJSON,
	)
	if err != nil {
		return nil, opStatusInvalidRequest, status.Errorf(codes.InvalidArgument, "failed to create valued lien: %v", err)
	}
	return lien, "", nil
}

// mapValuationError maps a valuation error to an operation status string.
func mapValuationError(err error) string {
	switch {
	case errors.Is(err, ErrValuationAccountNotFound):
		return opStatusAccountNotFound
	case errors.Is(err, ErrNoActiveValuationFeature):
		return opStatusNoValuationFeature
	case errors.Is(err, ErrValuationFeatureNotActive):
		return opStatusFeatureNotActive
	case errors.Is(err, ErrValuationRepoNotConfigured):
		return opStatusValuationFeatureRepoNil
	default:
		return opStatusValuationFailed
	}
}

// mapValuationErrorToGRPC maps a valuation error to a gRPC status error.
func mapValuationErrorToGRPC(err error) error {
	switch {
	case errors.Is(err, ErrValuationAccountNotFound):
		return status.Errorf(codes.NotFound, "%v", err)
	case errors.Is(err, ErrNoActiveValuationFeature),
		errors.Is(err, ErrValuationFeatureNotActive),
		errors.Is(err, ErrValuationRepoNotConfigured):
		return status.Errorf(codes.FailedPrecondition, "%v", err)
	case errors.Is(err, ErrValuationEngineFailed):
		return status.Errorf(codes.Internal, "%v", err)
	default:
		return status.Errorf(codes.Internal, "valuation failed: %v", err)
	}
}

// persistLienWithRaceHandling persists the lien, handling duplicate payment order reference race conditions.
func (s *Service) persistLienWithRaceHandling(ctx context.Context, lien *domain.Lien, paymentOrderRef string) (*pb.InitiateLienResponse, string, error) {
	if err := s.lienRepo.Create(ctx, lien); err != nil {
		if isDuplicatePaymentOrderRef(err) {
			// Race condition: another request created the lien between our check and create.
			existingLien, findErr := s.lienRepo.FindByPaymentOrderReference(ctx, paymentOrderRef)
			if findErr != nil {
				return nil, opStatusLienCreateFailed, status.Errorf(codes.Internal, "lien creation race condition: %v", err)
			}
			resp, buildErr := s.buildInitiateLienResponse(ctx, existingLien)
			if buildErr != nil {
				return nil, opStatusLienCreateFailed, buildErr
			}
			return resp, "", nil
		}
		return nil, opStatusLienCreateFailed, status.Errorf(codes.Internal, "failed to create lien: %v", err)
	}
	return nil, "", nil
}

// isDuplicatePaymentOrderRef checks if the error indicates a unique constraint violation
// on the payment_order_reference column.
func isDuplicatePaymentOrderRef(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "idx_lien_payment_order") ||
		strings.Contains(errStr, "23505") ||
		strings.Contains(errStr, "duplicate key")
}
