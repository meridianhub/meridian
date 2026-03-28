package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/quantity"
)

// InitiateWithOpeningBalance creates a new financial position log with an opening balance.
// This RPC is used for migrating existing accounts from legacy systems where the full
// transaction history is not available.
func (s *PositionKeepingService) InitiateWithOpeningBalance(
	ctx context.Context,
	req *positionkeepingv1.InitiateWithOpeningBalanceRequest,
) (resp *positionkeepingv1.InitiateWithOpeningBalanceResponse, err error) {
	if err := validateMigrationRequest(req); err != nil {
		return nil, err
	}

	idempotencyKey, cachedResponse, err := s.checkMigrationIdempotencyAndAcquireLock(ctx, req)
	if err != nil {
		return nil, err
	}
	if cachedResponse != nil {
		return cachedResponse, nil
	}

	if idempotencyKey != nil {
		defer func() {
			if err != nil {
				_ = s.idempotency.Delete(ctx, *idempotencyKey)
			}
		}()
	}

	if err := ctx.Err(); err != nil {
		return nil, status.Errorf(codes.Canceled, "request cancelled: %v", err)
	}

	openingBalance, err := protoMoneyAmountToDomain(req.OpeningBalance)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid opening_balance: %v", err)
	}

	if req.InstrumentCode != "" {
		amountStr := openingBalance.Amount.String()
		if err := s.validateOpeningBalanceWithCEL(ctx, req.InstrumentCode, amountStr, req.Attributes); err != nil {
			return nil, err
		}
	}

	log, err := s.createMigrationLog(req, openingBalance)
	if err != nil {
		return nil, err
	}

	if err := s.persistMigrationLog(ctx, req, log, openingBalance); err != nil {
		return nil, err
	}

	if idempotencyKey != nil {
		if err := storeLogIdempotencyResult(ctx, s.idempotency, *idempotencyKey, log.LogID); err != nil {
			return nil, err
		}
	}

	return &positionkeepingv1.InitiateWithOpeningBalanceResponse{
		Log: toProtoFinancialPositionLog(log),
	}, nil
}

// createMigrationLog creates a new financial position log with opening balance from the request.
func (s *PositionKeepingService) createMigrationLog(
	req *positionkeepingv1.InitiateWithOpeningBalanceRequest,
	openingBalance domain.Money,
) (*domain.FinancialPositionLog, error) {
	effectiveDate := req.EffectiveDate.AsTime()

	log, err := domain.NewFinancialPositionLogWithOpeningBalance(
		req.AccountId, openingBalance, effectiveDate, req.MigrationReference,
	)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidEffectiveDate) {
			return nil, status.Errorf(codes.InvalidArgument, "effective_date cannot be in the future")
		}
		return nil, status.Errorf(codes.InvalidArgument, "failed to create financial position log: %v", err)
	}
	return log, nil
}

// persistMigrationLog builds the opening balance event and persists the log atomically.
func (s *PositionKeepingService) persistMigrationLog(
	ctx context.Context,
	req *positionkeepingv1.InitiateWithOpeningBalanceRequest,
	log *domain.FinancialPositionLog,
	openingBalance domain.Money,
) error {
	correlationID := clients.ExtractCorrelationID(ctx)
	if correlationID == "" {
		correlationID = log.LogID.String()
	}

	event := &domain.OpeningBalanceRecorded{
		LogID:              log.LogID,
		AccountID:          log.AccountID,
		OpeningBalance:     openingBalance,
		EffectiveDate:      req.EffectiveDate.AsTime(),
		MigrationReference: req.MigrationReference,
		CorrelationID:      correlationID,
		Timestamp:          time.Now().UTC(),
		Version:            log.Version,
	}
	outboxFn := s.outboxPublisher.BuildOutboxFn(ctx, []domain.DomainEvent{event})
	if err := s.repository.CreateWithOutbox(ctx, log, outboxFn); err != nil {
		return status.Errorf(codes.Internal, "failed to save financial position log: %v", err)
	}
	return nil
}

// validateMigrationRequest validates the InitiateWithOpeningBalanceRequest.
func validateMigrationRequest(req *positionkeepingv1.InitiateWithOpeningBalanceRequest) error {
	if req.AccountId == "" {
		return status.Error(codes.InvalidArgument, "account_id is required")
	}

	if req.OpeningBalance == nil || req.OpeningBalance.Amount == nil {
		return status.Error(codes.InvalidArgument, "opening_balance is required")
	}

	if req.EffectiveDate == nil {
		return status.Error(codes.InvalidArgument, "effective_date is required")
	}

	return nil
}

// protoMoneyAmountToDomain converts a proto MoneyAmount to domain.Money.
// This is a thin wrapper around googleMoneyToDomain for the MoneyAmount proto type.
func protoMoneyAmountToDomain(proto *commonv1.MoneyAmount) (domain.Money, error) {
	if proto == nil || proto.Amount == nil {
		return domain.Money{}, nil
	}
	return googleMoneyToDomain(proto.Amount)
}

// checkMigrationIdempotencyAndAcquireLock checks for completed operations and acquires a pending lock.
func (s *PositionKeepingService) checkMigrationIdempotencyAndAcquireLock(
	ctx context.Context,
	req *positionkeepingv1.InitiateWithOpeningBalanceRequest,
) (*idempotency.Key, *positionkeepingv1.InitiateWithOpeningBalanceResponse, error) {
	if req.IdempotencyKey == nil || req.IdempotencyKey.Key == "" || s.idempotency == nil {
		return nil, nil, nil
	}

	key := idempotency.Key{
		Namespace: "position-keeping",
		Operation: "initiate-with-opening-balance",
		EntityID:  req.AccountId,
		RequestID: req.IdempotencyKey.Key,
	}

	result, err := s.idempotency.Check(ctx, key)
	if err != nil && !errors.Is(err, idempotency.ErrResultNotFound) {
		return nil, nil, status.Errorf(codes.Internal, "failed to check idempotency: %v", err)
	}
	if err == nil {
		resp, retErr := s.handleMigrationIdempotencyResult(ctx, key, result)
		if resp != nil || retErr != nil {
			return &key, resp, retErr
		}
		// StatusFailed falls through to MarkPending
	}

	if err := s.idempotency.MarkPending(ctx, key, 5*time.Minute); err != nil {
		return nil, nil, status.Errorf(codes.Internal, "failed to mark operation as pending: %v", err)
	}

	return &key, nil, nil
}

// handleMigrationIdempotencyResult handles an existing idempotency result for the migration operation.
// Returns (response, nil) for completed, (nil, error) for pending, (nil, nil) for failed (retry allowed).
func (s *PositionKeepingService) handleMigrationIdempotencyResult(
	ctx context.Context,
	_ idempotency.Key,
	result *idempotency.Result,
) (*positionkeepingv1.InitiateWithOpeningBalanceResponse, error) {
	switch result.Status {
	case idempotency.StatusCompleted:
		var cachedData struct {
			LogID string `json:"log_id"`
		}
		if err := json.Unmarshal(result.Data, &cachedData); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to decode cached idempotency response: %v", err)
		}
		logID, err := parseUUID(cachedData.LogID)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "cached idempotency response contains invalid log_id: %v", err)
		}
		log, err := s.repository.FindByID(ctx, logID)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to load cached financial position log: %v", err)
		}
		return &positionkeepingv1.InitiateWithOpeningBalanceResponse{
			Log: toProtoFinancialPositionLog(log),
		}, nil

	case idempotency.StatusPending:
		return nil, status.Errorf(codes.Aborted, "operation already in progress, please retry")

	case idempotency.StatusFailed:
		// Allow retry for failed operations
		return nil, nil

	default:
		return nil, status.Errorf(codes.Internal, "unexpected idempotency status: %s", result.Status)
	}
}

// validateOpeningBalanceWithCEL validates opening balance attributes against the instrument definition.
// This is optional - if instrumentCache is nil or instrumentCode is empty, validation is skipped
// for backwards compatibility.
//
// Returns nil if validation passes or is skipped.
// Returns gRPC INVALID_ARGUMENT error if validation fails.
// Returns gRPC NOT_FOUND error if instrument is not found.
// Returns gRPC FAILED_PRECONDITION error if instrument is not active.
func (s *PositionKeepingService) validateOpeningBalanceWithCEL(
	ctx context.Context,
	instrumentCode string,
	amount string,
	attributes map[string]string,
) error {
	if s.instrumentCache == nil || instrumentCode == "" {
		return nil
	}

	instrument, err := s.loadInstrument(ctx, instrumentCode)
	if err != nil {
		if errors.Is(err, ErrInstrumentNotFound) {
			RecordOpeningBalanceValidationFailure(instrumentCode, ValidationFailureReasonInstrumentNotFound)
			return status.Errorf(codes.NotFound,
				"instrument definition not found for instrument code '%s': %v", instrumentCode, err)
		}
		RecordOpeningBalanceValidationFailure(instrumentCode, ValidationFailureReasonCELError)
		return status.Errorf(codes.Internal,
			"failed to load instrument definition for instrument code '%s': %v", instrumentCode, err)
	}

	activation := buildCELActivation(attributes, amount)

	return evalValidationProgram(instrument, instrumentCode, activation, func(code string, reason string) {
		RecordOpeningBalanceValidationFailure(code, reason)
	})
}

// loadInstrument loads a cached instrument by code from the instrument cache.
func (s *PositionKeepingService) loadInstrument(ctx context.Context, instrumentCode string) (*CachedInstrument, error) {
	const instrumentVersion = 1
	return s.instrumentCache.GetOrLoad(ctx, instrumentCode, instrumentVersion, func() (*CachedInstrument, error) {
		return nil, fmt.Errorf("%w: %s", ErrInstrumentNotFound, instrumentCode)
	})
}

// buildCELActivation builds the CEL activation context from attributes and amount.
func buildCELActivation(attributes map[string]string, amount string) map[string]any {
	bag := quantity.AcquireAttributeBag()
	defer quantity.ReleaseAttributeBag(bag)

	for k, v := range attributes {
		bag.Set(k, v)
	}

	source := attributes["source"]
	return map[string]any{
		"attributes": bag.ToMap(),
		"amount":     amount,
		"valid_from": time.Time{},
		"valid_to":   time.Time{},
		"source":     source,
	}
}

// evalValidationProgram runs the instrument's CEL validation program against the activation.
// The recordFailure callback is called with (instrumentCode, reason) on failure.
func evalValidationProgram(instrument *CachedInstrument, instrumentCode string, activation map[string]any, recordFailure func(string, string)) error {
	if instrument.ValidationProgram == nil {
		return nil
	}

	result, _, err := instrument.ValidationProgram.Eval(activation)
	if err != nil {
		recordFailure(instrumentCode, ValidationFailureReasonCELError)
		return status.Errorf(codes.InvalidArgument,
			"validation error for instrument '%s': %v", instrumentCode, err)
	}

	valid, ok := result.Value().(bool)
	if !ok {
		recordFailure(instrumentCode, ValidationFailureReasonCELError)
		return status.Errorf(codes.InvalidArgument,
			"validation error for instrument '%s': expression did not return boolean", instrumentCode)
	}

	if !valid {
		recordFailure(instrumentCode, ValidationFailureReasonCELRejected)
		return status.Errorf(codes.InvalidArgument,
			"validation failed for instrument '%s': attributes do not satisfy validation rules", instrumentCode)
	}

	return nil
}
