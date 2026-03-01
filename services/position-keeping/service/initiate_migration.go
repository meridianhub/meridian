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
	// Validate request
	if err := validateMigrationRequest(req); err != nil {
		return nil, err
	}

	// Check idempotency and acquire lock if key provided
	idempotencyKey, cachedResponse, err := s.checkMigrationIdempotencyAndAcquireLock(ctx, req)
	if err != nil {
		return nil, err
	}
	if cachedResponse != nil {
		return cachedResponse, nil
	}

	// Clean up pending idempotency key on error to prevent 5-minute lockout
	if idempotencyKey != nil {
		defer func() {
			if err != nil {
				_ = s.idempotency.Delete(ctx, *idempotencyKey)
			}
		}()
	}

	// Check for context cancellation after potentially slow idempotency check
	if err := ctx.Err(); err != nil {
		return nil, status.Errorf(codes.Canceled, "request cancelled: %v", err)
	}

	// Convert opening balance from proto to domain Money
	openingBalance, err := protoMoneyAmountToDomain(req.OpeningBalance)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid opening_balance: %v", err)
	}

	// Validate instrument and attributes using CEL (if instrument_code provided)
	if req.InstrumentCode != "" {
		// Reuse the already-converted domain amount for string formatting.
		// This uses the battle-tested decimal library and handles all edge cases correctly.
		amountStr := openingBalance.Amount.String()
		if err := s.validateOpeningBalanceWithCEL(ctx, req.InstrumentCode, amountStr, req.Attributes); err != nil {
			return nil, err // Already a gRPC status error
		}
	}

	// Extract effective date from proto timestamp
	effectiveDate := req.EffectiveDate.AsTime()

	// Create new financial position log with opening balance
	log, err := domain.NewFinancialPositionLogWithOpeningBalance(
		req.AccountId,
		openingBalance,
		effectiveDate,
		req.MigrationReference,
	)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidEffectiveDate) {
			return nil, status.Errorf(codes.InvalidArgument, "effective_date cannot be in the future")
		}
		return nil, status.Errorf(codes.InvalidArgument, "failed to create financial position log: %v", err)
	}

	// Extract correlation ID from context for end-to-end request tracing,
	// falling back to log ID if not present in request metadata
	correlationID := clients.ExtractCorrelationID(ctx)
	if correlationID == "" {
		correlationID = log.LogID.String()
	}

	// Build the OpeningBalanceRecorded event and persist it atomically with the position log.
	event := &domain.OpeningBalanceRecorded{
		LogID:              log.LogID,
		AccountID:          log.AccountID,
		OpeningBalance:     openingBalance,
		EffectiveDate:      effectiveDate,
		MigrationReference: req.MigrationReference,
		CorrelationID:      correlationID,
		Timestamp:          time.Now().UTC(),
		Version:            log.Version,
	}
	outboxFn := s.outboxPublisher.BuildOutboxFn(ctx, []domain.DomainEvent{event})
	if err := s.repository.CreateWithOutbox(ctx, log, outboxFn); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to save financial position log: %v", err)
	}

	// Store idempotency result if key was provided
	if idempotencyKey != nil {
		resultData, err := json.Marshal(map[string]string{
			"log_id": log.LogID.String(),
		})
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to marshal idempotency result: %v", err)
		}

		if err := s.idempotency.StoreResult(ctx, idempotency.Result{
			Key:         *idempotencyKey,
			Status:      idempotency.StatusCompleted,
			Data:        resultData,
			CompletedAt: time.Now(),
			TTL:         24 * time.Hour,
		}); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to store idempotency result: %v", err)
		}
	}

	resp = &positionkeepingv1.InitiateWithOpeningBalanceResponse{
		Log: toProtoFinancialPositionLog(log),
	}
	return resp, nil
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
	// No idempotency key provided or idempotency service not configured
	if req.IdempotencyKey == nil || req.IdempotencyKey.Key == "" || s.idempotency == nil {
		return nil, nil, nil
	}

	key := idempotency.Key{
		Namespace: "position-keeping",
		Operation: "initiate-with-opening-balance",
		EntityID:  req.AccountId,
		RequestID: req.IdempotencyKey.Key,
	}

	// Check if operation was already completed or in progress
	result, err := s.idempotency.Check(ctx, key)
	if err != nil && !errors.Is(err, idempotency.ErrResultNotFound) {
		// Transient store error (Redis timeout, connection failure) - don't bypass idempotency
		return nil, nil, status.Errorf(codes.Internal, "failed to check idempotency: %v", err)
	}
	if err == nil {
		switch result.Status {
		case idempotency.StatusCompleted:
			// Return cached result
			var cachedData struct {
				LogID string `json:"log_id"`
			}
			if err := json.Unmarshal(result.Data, &cachedData); err != nil {
				return nil, nil, status.Errorf(codes.Internal, "failed to decode cached idempotency response: %v", err)
			}

			logID, err := parseUUID(cachedData.LogID)
			if err != nil {
				return nil, nil, status.Errorf(codes.Internal, "cached idempotency response contains invalid log_id: %v", err)
			}

			log, err := s.repository.FindByID(ctx, logID)
			if err != nil {
				return nil, nil, status.Errorf(codes.Internal, "failed to load cached financial position log: %v", err)
			}

			return &key, &positionkeepingv1.InitiateWithOpeningBalanceResponse{
				Log: toProtoFinancialPositionLog(log),
			}, nil

		case idempotency.StatusPending:
			// Another request is currently processing this operation
			return nil, nil, status.Errorf(codes.Aborted, "operation already in progress, please retry")

		case idempotency.StatusFailed:
			// Previous attempt failed - allow retry by proceeding to MarkPending
		}
	}
	// ErrResultNotFound means key doesn't exist - continue to mark pending

	// Mark operation as pending to prevent concurrent execution
	if err := s.idempotency.MarkPending(ctx, key, 5*time.Minute); err != nil {
		return nil, nil, status.Errorf(codes.Internal, "failed to mark operation as pending: %v", err)
	}

	return &key, nil, nil
}

// validateOpeningBalanceWithCEL validates opening balance attributes against the instrument definition.
// This is optional - if instrumentCache is nil or instrumentCode is empty, validation is skipped
// for backwards compatibility.
//
// The CEL program receives the following variables:
//   - attributes: map[string]string of opening balance attributes
//   - amount: string representation of the opening balance amount
//   - valid_from: zero time (for future use)
//   - valid_to: zero time (for future use)
//   - source: extracted from attributes["source"] or empty string
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
	// Skip validation if instrument cache is not configured (backwards compatibility)
	if s.instrumentCache == nil {
		return nil
	}

	// Skip validation if no instrument code provided (backwards compatibility)
	if instrumentCode == "" {
		return nil
	}

	// Acquire an AttributeBag from pool for efficient memory reuse
	bag := quantity.AcquireAttributeBag()
	defer quantity.ReleaseAttributeBag(bag)

	// Populate AttributeBag from attributes
	for k, v := range attributes {
		bag.Set(k, v)
	}

	// Look up instrument from cache using instrument_code
	// Use version=1 for now; could be made configurable in the future
	const instrumentVersion = 1
	instrument, err := s.instrumentCache.GetOrLoad(ctx, instrumentCode, instrumentVersion, func() (*CachedInstrument, error) {
		// The loadFn should never be called if the cache is properly configured
		// with a backing repository. For now, return not found to trigger the error path.
		return nil, fmt.Errorf("%w: %s", ErrInstrumentNotFound, instrumentCode)
	})
	if err != nil {
		// Distinguish instrument-not-found from other cache/backend errors
		if errors.Is(err, ErrInstrumentNotFound) {
			RecordOpeningBalanceValidationFailure(instrumentCode, ValidationFailureReasonInstrumentNotFound)
			return status.Errorf(codes.NotFound,
				"instrument definition not found for instrument code '%s': %v", instrumentCode, err)
		}
		// Other errors (cache failures, backend timeouts, etc.) are internal errors
		RecordOpeningBalanceValidationFailure(instrumentCode, ValidationFailureReasonCELError)
		return status.Errorf(codes.Internal,
			"failed to load instrument definition for instrument code '%s': %v", instrumentCode, err)
	}

	// Build the activation context for CEL evaluation
	// The CEL program expects these specific variable names
	// Note: Go map access on nil returns zero value, so no nil check needed
	source := attributes["source"]
	attributesMap := bag.ToMap()
	activation := map[string]any{
		"attributes": attributesMap,
		"amount":     amount,
		"valid_from": time.Time{}, // Zero time for now
		"valid_to":   time.Time{}, // Zero time for now
		"source":     source,
	}

	// Run validation if instrument has a validation program
	if instrument.ValidationProgram != nil {
		result, _, err := instrument.ValidationProgram.Eval(activation)
		if err != nil {
			// CEL evaluation error - record metric and return error
			RecordOpeningBalanceValidationFailure(instrumentCode, ValidationFailureReasonCELError)
			return status.Errorf(codes.InvalidArgument,
				"validation error for instrument '%s': %v", instrumentCode, err)
		}

		// The validation program should return a boolean
		valid, ok := result.Value().(bool)
		if !ok {
			// Unexpected return type from CEL - treat as error
			RecordOpeningBalanceValidationFailure(instrumentCode, ValidationFailureReasonCELError)
			return status.Errorf(codes.InvalidArgument,
				"validation error for instrument '%s': expression did not return boolean", instrumentCode)
		}

		if !valid {
			// Validation rejected the opening balance - record metric and return error
			RecordOpeningBalanceValidationFailure(instrumentCode, ValidationFailureReasonCELRejected)
			return status.Errorf(codes.InvalidArgument,
				"opening balance validation failed for instrument '%s': attributes do not satisfy validation rules", instrumentCode)
		}
	}

	return nil
}
