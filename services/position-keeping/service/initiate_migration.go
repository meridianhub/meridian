package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
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
	openingBalance, err := protoMoneyToDomain(req.OpeningBalance)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid opening_balance: %v", err)
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

	// Persist to repository
	if err := s.repository.Create(ctx, log); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to save financial position log: %v", err)
	}

	// Extract correlation ID from context for end-to-end request tracing,
	// falling back to log ID if not present in request metadata
	correlationID := clients.ExtractCorrelationID(ctx)
	if correlationID == "" {
		correlationID = log.LogID.String()
	}

	// Publish OpeningBalanceRecorded event using fire-and-forget pattern
	// (consistent with other endpoints in this service - Kafka producer configured with retries)
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
	_ = s.eventPublisher.Publish(ctx, event)

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

// protoMoneyToDomain converts a proto MoneyAmount to domain.Money.
func protoMoneyToDomain(proto *commonv1.MoneyAmount) (domain.Money, error) {
	if proto == nil || proto.Amount == nil {
		return domain.Money{}, nil
	}

	// Convert google.type.Money to domain.Money
	// Units is the whole amount, nanos is the fractional amount (billionths)
	amount := decimal.NewFromInt(proto.Amount.Units)
	nanos := decimal.NewFromInt(int64(proto.Amount.Nanos)).Div(decimal.NewFromInt(1_000_000_000))
	totalAmount := amount.Add(nanos)

	return domain.NewMoney(totalAmount, domain.Currency(proto.Amount.CurrencyCode))
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
