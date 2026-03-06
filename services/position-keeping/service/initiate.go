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
	// Validate request
	if err := validateInitiateRequest(req); err != nil {
		return nil, err
	}

	// Check idempotency and acquire lock if key provided
	idempotencyKey, cachedResponse, err := s.checkIdempotencyAndAcquireLock(ctx, req)
	if err != nil {
		return nil, err
	}
	if cachedResponse != nil {
		return cachedResponse, nil
	}

	// Clean up pending idempotency key on error to prevent 5-minute lockout
	// If the operation fails after MarkPending, release the key so retries can proceed
	if idempotencyKey != nil {
		defer func() {
			if err != nil {
				// Best-effort cleanup; ignore delete errors as TTL will eventually expire
				_ = s.idempotency.Delete(ctx, *idempotencyKey)
			}
		}()
	}

	// Check for context cancellation after potentially slow idempotency check
	if err := ctx.Err(); err != nil {
		return nil, status.Errorf(codes.Canceled, "request cancelled: %v", err)
	}

	// Validate account exists if validation is enabled
	// This check ensures we don't create position logs for non-existent accounts.
	// The validator uses graceful degradation: if Current Account service is unavailable,
	// validation is skipped to avoid blocking operations during service outages.
	if s.accountValidationEnabled && s.accountValidator != nil {
		if err := s.accountValidator.ValidateExists(ctx, req.AccountId); err != nil {
			return nil, err // Returns codes.InvalidArgument if account not found
		}
	}

	// Convert initial entry from proto to domain if provided
	var initialEntry *domain.TransactionLogEntry
	if req.InitialEntry != nil {
		entry, err := protoEntryToDomain(req.InitialEntry)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid initial entry: %v", err)
		}
		initialEntry = entry
	}

	// Convert lineage from proto to domain if provided
	var lineage *domain.TransactionLineage
	if req.TransactionLineage != nil {
		lin, err := protoLineageToDomain(req.TransactionLineage)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid transaction lineage: %v", err)
		}
		lineage = lin
	}

	// Create new financial position log
	log, err := domain.NewFinancialPositionLog(req.AccountId, initialEntry, lineage)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to create financial position log: %v", err)
	}

	// Build the domain event (if applicable) before persisting, then write both
	// the position log and the outbox entry atomically in a single transaction.
	var events []domain.DomainEvent
	if initialEntry != nil {
		events = append(events, &domain.TransactionCaptured{
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
		})
	}

	// Persist to repository atomically with outbox event write.
	outboxFn := s.outboxPublisher.BuildOutboxFn(ctx, events)
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

	resp = &positionkeepingv1.InitiateFinancialPositionLogResponse{
		Log: toProtoFinancialPositionLog(log),
	}
	return resp, nil
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
// Accepts both ISO 4217 currency codes (GBP, USD) and non-currency instrument codes (KWH, GPU_HOUR).
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
