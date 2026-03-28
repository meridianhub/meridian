package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
)

// InitiateFinancialPositionLog creates a new financial position log.
func (s *PositionKeepingService) InitiateFinancialPositionLog(
	ctx context.Context,
	req *positionkeepingv1.InitiateFinancialPositionLogRequest,
) (resp *positionkeepingv1.InitiateFinancialPositionLogResponse, err error) {
	if err := validateInitiateRequest(req); err != nil {
		return nil, err
	}

	idempotencyKey, cachedResponse, err := s.checkIdempotencyAndAcquireLock(ctx, req)
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

	accountServiceDomain, valErr := s.resolveAccountDomain(ctx, req.AccountId)
	if valErr != nil {
		return nil, valErr
	}

	initialEntry, lineage, err := convertInitiateProtoToDomain(req)
	if err != nil {
		return nil, err
	}

	log, err := domain.NewFinancialPositionLog(req.AccountId, initialEntry, lineage)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to create financial position log: %v", err)
	}
	log.AccountServiceDomain = string(accountServiceDomain)

	events := buildTransactionCapturedEvents(log, initialEntry)

	outboxFn := s.outboxPublisher.BuildOutboxFn(ctx, events)
	if err := s.repository.CreateWithOutbox(ctx, log, outboxFn); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to save financial position log: %v", err)
	}

	if idempotencyKey != nil {
		if err := storeLogIdempotencyResult(ctx, s.idempotency, *idempotencyKey, log.LogID); err != nil {
			return nil, err
		}
	}

	return &positionkeepingv1.InitiateFinancialPositionLogResponse{
		Log: toProtoFinancialPositionLog(log),
	}, nil
}

// resolveAccountDomain validates the account and resolves its service domain.
// Returns empty string if validation is disabled or the account cannot be resolved.
// Returns an error if validation is enabled and the account is invalid.
func (s *PositionKeepingService) resolveAccountDomain(ctx context.Context, accountID string) (AccountServiceDomain, error) {
	if !s.accountValidationEnabled || s.accountValidator == nil {
		return "", nil
	}
	if err := s.accountValidator.ValidateExists(ctx, accountID); err != nil {
		return "", err
	}
	if resolver, ok := s.accountValidator.(AccountResolver); ok {
		return resolver.ResolveServiceDomain(ctx, accountID), nil
	}
	return "", nil
}

// convertInitiateProtoToDomain converts proto initial entry and lineage to domain types.
func convertInitiateProtoToDomain(req *positionkeepingv1.InitiateFinancialPositionLogRequest) (*domain.TransactionLogEntry, *domain.TransactionLineage, error) {
	var initialEntry *domain.TransactionLogEntry
	if req.InitialEntry != nil {
		entry, err := protoEntryToDomain(req.InitialEntry)
		if err != nil {
			return nil, nil, status.Errorf(codes.InvalidArgument, "invalid initial entry: %v", err)
		}
		initialEntry = entry
	}

	var lineage *domain.TransactionLineage
	if req.TransactionLineage != nil {
		lin, err := protoLineageToDomain(req.TransactionLineage)
		if err != nil {
			return nil, nil, status.Errorf(codes.InvalidArgument, "invalid transaction lineage: %v", err)
		}
		lineage = lin
	}

	return initialEntry, lineage, nil
}

// buildTransactionCapturedEvents builds domain events for a newly created log with an initial entry.
func buildTransactionCapturedEvents(log *domain.FinancialPositionLog, initialEntry *domain.TransactionLogEntry) []domain.DomainEvent {
	if initialEntry == nil {
		return nil
	}
	return []domain.DomainEvent{&domain.TransactionCaptured{
		LogID:         log.LogID,
		AccountID:     log.AccountID,
		TransactionID: initialEntry.TransactionID,
		Amount:        initialEntry.Amount,
		Direction:     initialEntry.Direction,
		Source:        initialEntry.Source,
		Description:   initialEntry.Description,
		Reference:     initialEntry.Reference,
		Timestamp:     initialEntry.Timestamp,
		Version:       log.Version,
	}}
}

// storeLogIdempotencyResult stores an idempotency result keyed by log ID.
func storeLogIdempotencyResult(ctx context.Context, svc idempotency.Service, key idempotency.Key, logID uuid.UUID) error {
	resultData, err := json.Marshal(map[string]string{
		"log_id": logID.String(),
	})
	if err != nil {
		return status.Errorf(codes.Internal, "failed to marshal idempotency result: %v", err)
	}

	if err := svc.StoreResult(ctx, idempotency.Result{
		Key:         key,
		Status:      idempotency.StatusCompleted,
		Data:        resultData,
		CompletedAt: time.Now(),
		TTL:         24 * time.Hour,
	}); err != nil {
		return status.Errorf(codes.Internal, "failed to store idempotency result: %v", err)
	}

	return nil
}

// validateInitiateRequest validates the initiate request
func validateInitiateRequest(req *positionkeepingv1.InitiateFinancialPositionLogRequest) error {
	if req.AccountId == "" {
		return status.Error(codes.InvalidArgument, "account_id is required")
	}

	if req.InitialEntry != nil {
		if req.InitialEntry.Amount == nil || req.InitialEntry.Amount.Amount == nil {
			return status.Error(codes.InvalidArgument, "amount is required")
		}

		if req.InitialEntry.Direction == commonv1.PostingDirection_POSTING_DIRECTION_UNSPECIFIED {
			return status.Error(codes.InvalidArgument, "direction cannot be unspecified")
		}
	}

	return nil
}

// googleMoneyToDomain converts google.type.Money to domain.Money.
// This is the shared conversion logic for all proto-to-domain money conversions.
// Accepts both ISO 4217 currency codes (GBP, USD) and non-currency instrument codes (KWH, GAS).
func googleMoneyToDomain(m *money.Money) (domain.Money, error) {
	if m == nil {
		return domain.Money{}, nil
	}

	// Units is the whole amount, nanos is the fractional amount (billionths)
	amount := decimal.NewFromInt(m.Units)
	nanos := decimal.NewFromInt(int64(m.Nanos)).Div(decimal.NewFromInt(1_000_000_000))
	totalAmount := amount.Add(nanos)

	return domain.NewMoneyFromInstrumentCode(totalAmount, m.CurrencyCode)
}

// protoEntryToDomain converts a proto TransactionLogEntry to domain.
// Returns (nil, nil) for nil input to handle optional proto fields.
func protoEntryToDomain(proto *positionkeepingv1.TransactionLogEntry) (*domain.TransactionLogEntry, error) {
	if proto == nil {
		return nil, nil //nolint:nilnil // Intentional: nil input returns nil output for optional field handling
	}

	transactionID, err := uuid.Parse(proto.TransactionId)
	if err != nil {
		return nil, err
	}

	money, err := googleMoneyToDomain(proto.Amount.Amount)
	if err != nil {
		return nil, err
	}

	var direction domain.PostingDirection
	switch proto.Direction {
	case commonv1.PostingDirection_POSTING_DIRECTION_DEBIT:
		direction = domain.PostingDirectionDebit
	case commonv1.PostingDirection_POSTING_DIRECTION_CREDIT:
		direction = domain.PostingDirectionCredit
	case commonv1.PostingDirection_POSTING_DIRECTION_UNSPECIFIED:
		direction = domain.PostingDirectionDebit // Unspecified defaults to debit
	default:
		direction = domain.PostingDirectionDebit
	}

	timestamp := time.Now().UTC()
	if proto.Timestamp != nil {
		timestamp = proto.Timestamp.AsTime()
	}

	return domain.NewTransactionLogEntry(
		transactionID,
		proto.AccountId,
		money,
		direction,
		timestamp,
		proto.Description,
		proto.Reference,
		domain.TransactionSourceManual, // Default to manual source
	)
}

// protoLineageToDomain converts a proto TransactionLineage to domain.
// Returns (nil, nil) for nil input to handle optional proto fields.
func protoLineageToDomain(proto *positionkeepingv1.TransactionLineage) (*domain.TransactionLineage, error) {
	if proto == nil {
		return nil, nil //nolint:nilnil // Intentional: nil input returns nil output for optional field handling
	}

	transactionID, err := uuid.Parse(proto.TransactionId)
	if err != nil {
		return nil, err
	}

	var parentID *uuid.UUID
	if proto.ParentTransactionId != "" {
		pid, err := uuid.Parse(proto.ParentTransactionId)
		if err != nil {
			return nil, err
		}
		parentID = &pid
	}

	childIDs := make([]uuid.UUID, 0, len(proto.ChildTransactionIds))
	for _, idStr := range proto.ChildTransactionIds {
		id, err := uuid.Parse(idStr)
		if err != nil {
			return nil, err
		}
		childIDs = append(childIDs, id)
	}

	relatedIDs := make([]uuid.UUID, 0, len(proto.RelatedTransactionIds))
	for _, idStr := range proto.RelatedTransactionIds {
		id, err := uuid.Parse(idStr)
		if err != nil {
			return nil, err
		}
		relatedIDs = append(relatedIDs, id)
	}

	return domain.NewTransactionLineage(
		transactionID,
		proto.TransactionType,
		parentID,
		childIDs,
		relatedIDs,
	)
}

// checkIdempotencyAndAcquireLock checks for completed operations and acquires a pending lock.
// Returns the idempotency key (if provided), cached response (if exists), and any error.
//
// This function should be updated to handle StatusPending, StatusFailed, and transient errors
// consistently with checkMigrationIdempotencyAndAcquireLock. See initiate_migration.go for the
// improved pattern.
func (s *PositionKeepingService) checkIdempotencyAndAcquireLock(
	ctx context.Context,
	req *positionkeepingv1.InitiateFinancialPositionLogRequest,
) (*idempotency.Key, *positionkeepingv1.InitiateFinancialPositionLogResponse, error) {
	// No idempotency key provided or idempotency service not configured
	if req.IdempotencyKey == nil || req.IdempotencyKey.Key == "" || s.idempotency == nil {
		return nil, nil, nil
	}

	key := idempotency.Key{
		Namespace: "position-keeping",
		Operation: "initiate",
		EntityID:  req.AccountId,
		RequestID: req.IdempotencyKey.Key,
	}

	// Check if operation was already completed
	result, err := s.idempotency.Check(ctx, key)
	if err == nil && result.Status == idempotency.StatusCompleted {
		// Return cached result - must not retry the operation once completed
		var cachedData struct {
			LogID string `json:"log_id"`
		}
		if err := json.Unmarshal(result.Data, &cachedData); err != nil {
			return nil, nil, status.Errorf(codes.Internal, "failed to decode cached idempotency response: %v", err)
		}

		logID, err := uuid.Parse(cachedData.LogID)
		if err != nil {
			return nil, nil, status.Errorf(codes.Internal, "cached idempotency response contains invalid log_id: %v", err)
		}

		log, err := s.repository.FindByID(ctx, logID)
		if err != nil {
			return nil, nil, status.Errorf(codes.Internal, "failed to load cached financial position log: %v", err)
		}

		return &key, &positionkeepingv1.InitiateFinancialPositionLogResponse{
			Log: toProtoFinancialPositionLog(log),
		}, nil
	}

	// Mark operation as pending to prevent concurrent execution
	// Use 5-minute TTL for operation lock
	if err := s.idempotency.MarkPending(ctx, key, 5*time.Minute); err != nil {
		return nil, nil, status.Errorf(codes.Internal, "failed to mark operation as pending: %v", err)
	}

	return &key, nil, nil
}
