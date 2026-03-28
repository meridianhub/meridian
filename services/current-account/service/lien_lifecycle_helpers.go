package service

import (
	"context"
	"errors"
	"math"
	"time"

	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// computeMultiAssetLienAmount runs atomic valuation and converts the result to a domain Amount.
func (s *Service) computeMultiAssetLienAmount(
	ctx context.Context, req *pb.InitiateLienRequest, prefetchedAccount domain.CurrentAccount,
) (domain.Amount, *valuateInternalResult, string, error) {
	inputAmount, err := decimal.NewFromString(req.Input.Amount)
	if err != nil {
		return domain.Amount{}, nil, opStatusInvalidInputAmount,
			status.Errorf(codes.InvalidArgument, "invalid input amount: %v", err)
	}
	if !inputAmount.IsPositive() {
		return domain.Amount{}, nil, opStatusInputAmountNonPositive,
			status.Error(codes.InvalidArgument, "input amount must be positive")
	}

	knowledgeAt := time.Now()
	if req.KnowledgeAt != nil {
		knowledgeAt = req.KnowledgeAt.AsTime()
	}

	valuationResult, err := s.valuateInternal(ctx, req.AccountId, inputAmount, req.Input.InstrumentCode, knowledgeAt)
	if err != nil {
		opStatus, grpcErr := mapValuationError(err)
		return domain.Amount{}, nil, opStatus, grpcErr
	}

	// Convert valued output to domain Amount for lien amount (the actual reservation).
	// Use the account's instrument precision so non-2-decimal instruments work correctly.
	precision := prefetchedAccount.Balance().Instrument().Precision
	// #nosec G115 - precision is bounded by instrument definition (0-9 in practice)
	scale := decimal.NewFromInt(1).Shift(int32(precision))
	valuedMinor := valuationResult.OutputAmount.Mul(scale).RoundBank(0)
	maxInt64 := decimal.NewFromInt(math.MaxInt64)
	minInt64 := decimal.NewFromInt(math.MinInt64)
	if valuedMinor.GreaterThan(maxInt64) || valuedMinor.LessThan(minInt64) {
		return domain.Amount{}, nil, opStatusInvalidAmount,
			status.Error(codes.InvalidArgument, ErrAmountOverflow.Error())
	}
	lienAmount, err := domain.NewAmountFromInstrument(
		valuationResult.OutputCode,
		prefetchedAccount.Dimension(),
		precision,
		valuedMinor.IntPart(),
	)
	if err != nil {
		return domain.Amount{}, nil, opStatusInvalidAmount,
			status.Errorf(codes.Internal, "failed to create valued amount: %v", err)
	}
	return lienAmount, valuationResult, "", nil
}

// mapValuationError converts a valuation error to an operation status and gRPC error.
func mapValuationError(err error) (string, error) {
	switch {
	case errors.Is(err, ErrValuationAccountNotFound):
		return opStatusAccountNotFound, status.Errorf(codes.NotFound, "%v", err)
	case errors.Is(err, ErrNoActiveValuationFeature):
		return opStatusNoValuationFeature, status.Errorf(codes.FailedPrecondition, "%v", err)
	case errors.Is(err, ErrValuationFeatureNotActive):
		return opStatusFeatureNotActive, status.Errorf(codes.FailedPrecondition, "%v", err)
	case errors.Is(err, ErrValuationRepoNotConfigured):
		return opStatusValuationFeatureRepoNil, status.Errorf(codes.FailedPrecondition, "%v", err)
	case errors.Is(err, ErrValuationEngineFailed):
		return opStatusValuationFailed, status.Errorf(codes.Internal, "%v", err)
	default:
		return opStatusValuationFailed, status.Errorf(codes.Internal, "valuation failed: %v", err)
	}
}

// computeLegacyLienAmount validates instrument match and converts MoneyAmount to a domain Amount.
func computeLegacyLienAmount(req *pb.InitiateLienRequest, prefetchedAccount domain.CurrentAccount) (domain.Amount, string, error) {
	if req.Amount == nil || req.Amount.Amount == nil {
		return domain.Amount{}, opStatusInvalidAmount,
			status.Error(codes.InvalidArgument, "amount is required")
	}
	if req.Amount.Amount.CurrencyCode != prefetchedAccount.InstrumentCode() {
		return domain.Amount{}, opStatusCurrencyMismatch,
			status.Errorf(codes.InvalidArgument,
				"currency mismatch: lien currency %s does not match account currency %s",
				req.Amount.Amount.CurrencyCode, prefetchedAccount.InstrumentCode())
	}
	lienAmount, err := protoMoneyToAmount(req.Amount, prefetchedAccount)
	if err != nil {
		return domain.Amount{}, opStatusInvalidAmount,
			status.Errorf(codes.InvalidArgument, "invalid amount: %v", err)
	}
	return lienAmount, "", nil
}

// mapInitiateLienTxError converts an InitiateLien transaction error to operation status and gRPC error.
func mapInitiateLienTxError(txErr error, accountID string) (string, error) {
	switch {
	case errors.Is(txErr, persistence.ErrAccountNotFound):
		return opStatusAccountNotFound, status.Errorf(codes.NotFound, "account not found: %s", accountID)
	case errors.Is(txErr, errTxAccountNotActive):
		return opStatusAccountNotActive, status.Errorf(codes.FailedPrecondition, "account is not active")
	case errors.Is(txErr, errTxCurrencyMismatch):
		return opStatusCurrencyMismatch, status.Errorf(codes.InvalidArgument, "lien currency must match account currency")
	case errors.Is(txErr, errTxInsufficientFunds):
		return opStatusInsufficientFunds, status.Errorf(codes.FailedPrecondition, "insufficient available balance")
	case errors.Is(txErr, errTxDomainError):
		return opStatusDomainError, status.Errorf(codes.InvalidArgument, "failed to create lien: %v", txErr)
	case errors.Is(txErr, errTxSaveLien):
		return opStatusSaveFailed, status.Errorf(codes.Internal, "failed to save lien: %v", txErr)
	default:
		return opStatusRetrieveFailed, status.Errorf(codes.Internal, "failed to create lien: %v", txErr)
	}
}

// buildInitiateLienResponse constructs the response for a successful InitiateLien operation.
func (s *Service) buildInitiateLienResponse(
	account *domain.CurrentAccount, lien *domain.Lien, lienAmount domain.Amount,
	availableBalance int64, useValuation bool, valuationResult *valuateInternalResult,
) *pb.InitiateLienResponse {
	newAvailableBalance := availableBalance - lienAmount.ToMinorUnitsUnchecked()
	availableMoney, err := domain.NewAmountFromInstrument(
		account.Balance().InstrumentCode(),
		account.Balance().Dimension(),
		account.Balance().Instrument().Precision,
		newAvailableBalance,
	)
	if err != nil {
		s.logger.Error("failed to create available balance amount", "error", err)
		availableMoney = account.AvailableBalance()
	}

	resp := &pb.InitiateLienResponse{
		Lien:             toLienProto(lien),
		AvailableBalance: toMoneyAmount(availableMoney),
	}

	if useValuation && valuationResult != nil {
		resp.ValuedAmount = &quantityv1.InstrumentAmount{
			Amount:         valuationResult.OutputAmount.String(),
			InstrumentCode: valuationResult.OutputCode,
			Version:        1,
		}
		resp.Basis = valuationResult.Analysis
	}

	return resp
}

// acquireExecuteLienIdempotency checks Redis cache and acquires a distributed lock for ExecuteLien.
// Returns the idempotency key string, key struct, lock status, and optionally a cached response.
func (s *Service) acquireExecuteLienIdempotency(
	ctx context.Context, req *pb.ExecuteLienRequest,
) (string, idempotency.Key, bool, *pb.ExecuteLienResponse, error) {
	var idempotencyKeyStr string
	if req.IdempotencyKey != nil && req.IdempotencyKey.Key != "" {
		idempotencyKeyStr = req.IdempotencyKey.Key
	}

	var idempKey idempotency.Key
	if idempotencyKeyStr == "" || s.idempotencyService == nil {
		return idempotencyKeyStr, idempKey, false, nil, nil
	}

	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		s.logger.Debug("tenant not found in context for idempotency key", "lien_id", req.LienId)
	}
	idempKey = idempotency.Key{
		TenantID: string(tenantID), Namespace: idempotencyNamespace,
		Operation: "execute_lien", EntityID: req.LienId, RequestID: idempotencyKeyStr,
	}

	// Check Redis for existing result
	if cachedResp, err := s.checkIdempotencyCache(ctx, idempKey, req.LienId, idempotencyKeyStr); err != nil {
		return idempotencyKeyStr, idempKey, false, nil, err
	} else if cachedResp != nil {
		return idempotencyKeyStr, idempKey, false, cachedResp, nil
	}

	// Mark operation as pending (distributed lock)
	if err := s.idempotencyService.MarkPending(ctx, idempKey, idempotencyPendingTTL); err != nil {
		if errors.Is(err, idempotency.ErrOperationAlreadyProcessed) {
			s.logger.Info("operation already in progress, please retry", "idempotency_key", idempotencyKeyStr)
			return idempotencyKeyStr, idempKey, false, nil,
				status.Error(codes.Aborted, "operation already in progress, please retry")
		}
		s.logger.Error("failed to mark operation pending", "error", err)
		return idempotencyKeyStr, idempKey, false, nil,
			status.Error(codes.Aborted, "failed to acquire idempotency lock, please retry")
	}

	return idempotencyKeyStr, idempKey, true, nil, nil
}

// checkIdempotencyCache checks Redis for an existing ExecuteLien result.
func (s *Service) checkIdempotencyCache(
	ctx context.Context, idempKey idempotency.Key, lienID, idempotencyKeyStr string,
) (*pb.ExecuteLienResponse, error) {
	result, err := s.idempotencyService.Check(ctx, idempKey)
	if errors.Is(err, idempotency.ErrOperationAlreadyProcessed) && result != nil && result.Data != nil {
		var cachedResp pb.ExecuteLienResponse
		if unmarshalErr := proto.Unmarshal(result.Data, &cachedResp); unmarshalErr == nil {
			s.logger.Info("returning cached execute lien response from Redis",
				"lien_id", lienID, "transaction_id", cachedResp.TransactionId,
				"idempotency_key", idempotencyKeyStr)
			return &cachedResp, nil
		}
		s.logger.Warn("failed to unmarshal cached idempotency result", "error", err)
	} else if err != nil && !errors.Is(err, idempotency.ErrResultNotFound) {
		s.logger.Error("idempotency check failed", "error", err)
		return nil, status.Error(codes.Internal, "failed to check idempotency")
	}
	return nil, nil
}

// mapExecuteLienTxError converts an ExecuteLien transaction error to operation status and gRPC error.
func mapExecuteLienTxError(txErr error, lienID string, lien *domain.Lien) (string, error) {
	switch {
	case errors.Is(txErr, persistence.ErrLienNotFound):
		return opStatusLienNotFound, status.Errorf(codes.NotFound, "lien not found: %s", lienID)
	case errors.Is(txErr, persistence.ErrLienVersionConflict):
		return opStatusVersionConflict, status.Error(codes.Aborted, "concurrent modification detected, please retry")
	case errors.Is(txErr, persistence.ErrVersionConflict):
		return opStatusVersionConflict, status.Error(codes.Aborted, "concurrent modification detected, please retry")
	case errors.Is(txErr, errTxInvalidLienStatus):
		return opStatusInvalidLienStatus, status.Errorf(codes.FailedPrecondition, "lien cannot be executed: status=%s, expired=%v", lien.Status, lien.IsExpired())
	case errors.Is(txErr, errTxSaveAccount):
		return opStatusSaveAccountFailed, status.Errorf(codes.Internal, "failed to execute lien transaction: %v", txErr)
	case errors.Is(txErr, errTxUpdateLien):
		return opStatusUpdateLienFailed, status.Errorf(codes.Internal, "failed to execute lien transaction: %v", txErr)
	case errors.Is(txErr, errTxExecuteFailed):
		return opStatusExecuteFailed, status.Errorf(codes.FailedPrecondition, "failed to execute lien: %v", txErr)
	case errors.Is(txErr, errTxWithdrawFailed):
		return opStatusWithdrawFailed, status.Errorf(codes.FailedPrecondition, "failed to debit account: %v", txErr)
	default:
		return opStatusRetrieveAccountFailed, status.Errorf(codes.Internal, "failed to execute lien transaction: %v", txErr)
	}
}

// storeIdempotencyResult stores a successful ExecuteLien response in Redis for future idempotency checks.
func (s *Service) storeIdempotencyResult(ctx context.Context, idempotencyKeyStr string, idempKey idempotency.Key, resp *pb.ExecuteLienResponse) {
	if idempotencyKeyStr == "" || s.idempotencyService == nil {
		return
	}
	responseData, marshalErr := proto.Marshal(resp)
	if marshalErr != nil {
		s.logger.Error("failed to marshal response for idempotency cache", "error", marshalErr)
		return
	}
	storeErr := s.idempotencyService.StoreResult(ctx, idempotency.Result{
		Key:         idempKey,
		Status:      idempotency.StatusCompleted,
		Data:        responseData,
		CompletedAt: time.Now(),
		TTL:         idempotencyResultTTL,
	})
	if storeErr != nil {
		s.logger.Error("failed to store idempotency result", "error", storeErr)
	}
}

// mapTerminateLienTxError converts a TerminateLien transaction error to operation status and gRPC error.
func mapTerminateLienTxError(txErr error, lienID string, lien *domain.Lien) (string, error) {
	switch {
	case errors.Is(txErr, persistence.ErrLienNotFound):
		return opStatusLienNotFound, status.Errorf(codes.NotFound, "lien not found: %s", lienID)
	case errors.Is(txErr, persistence.ErrLienVersionConflict):
		return opStatusVersionConflict, status.Error(codes.Aborted, "concurrent modification detected, please retry")
	case errors.Is(txErr, errTxInvalidLienStatus):
		return opStatusInvalidLienStatus, status.Errorf(codes.FailedPrecondition, "lien cannot be terminated: status=%s", lien.Status)
	case errors.Is(txErr, errTxTerminateFailed):
		return opStatusTerminateFailed, status.Errorf(codes.FailedPrecondition, "failed to terminate lien: %v", txErr)
	case errors.Is(txErr, errTxUpdateLien):
		return opStatusUpdateFailed, status.Errorf(codes.Internal, "failed to update lien: %v", txErr)
	default:
		return opStatusRetrieveFailed, status.Errorf(codes.Internal, "failed to terminate lien: %v", txErr)
	}
}
