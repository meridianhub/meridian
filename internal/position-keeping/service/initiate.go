package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/internal/position-keeping/domain"
	"github.com/meridianhub/meridian/pkg/platform/idempotency"
)

// InitiateFinancialPositionLog creates a new financial position log.
func (s *PositionKeepingService) InitiateFinancialPositionLog(
	ctx context.Context,
	req *positionkeepingv1.InitiateFinancialPositionLogRequest,
) (*positionkeepingv1.InitiateFinancialPositionLogResponse, error) {
	// Validate request
	if err := validateInitiateRequest(req); err != nil {
		return nil, err
	}

	// Check idempotency if key is provided
	if req.IdempotencyKey != nil && req.IdempotencyKey.Key != "" {
		idempotencyKey := idempotency.Key{
			Namespace: "position-keeping",
			Operation: "initiate",
			EntityID:  req.AccountId,
			RequestID: req.IdempotencyKey.Key,
		}

		// Check if operation was already completed
		result, err := s.idempotency.Check(ctx, idempotencyKey)
		if err == nil && result.Status == idempotency.StatusCompleted {
			// Return cached result - must not retry the operation once completed
			var cachedData struct {
				LogID string `json:"log_id"`
			}
			if err := json.Unmarshal(result.Data, &cachedData); err != nil {
				return nil, status.Errorf(codes.Internal, "failed to decode cached idempotency response: %v", err)
			}

			logID, err := uuid.Parse(cachedData.LogID)
			if err != nil {
				return nil, status.Errorf(codes.Internal, "cached idempotency response contains invalid log_id: %v", err)
			}

			log, err := s.repository.FindByID(ctx, logID)
			if err != nil {
				return nil, status.Errorf(codes.Internal, "failed to load cached financial position log: %v", err)
			}

			return &positionkeepingv1.InitiateFinancialPositionLogResponse{
				Log: toProtoFinancialPositionLog(log),
			}, nil
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

	// Persist to repository
	if err := s.repository.Create(ctx, log); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to save financial position log: %v", err)
	}

	// Publish event
	if initialEntry != nil {
		event := &domain.TransactionCaptured{
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
		}
		// Event publishing is fire-and-forget; errors are logged internally
		_ = s.eventPublisher.Publish(ctx, event)
	}

	// Store idempotency result if key was provided
	if req.IdempotencyKey != nil && req.IdempotencyKey.Key != "" {
		idempotencyKey := idempotency.Key{
			Namespace: "position-keeping",
			Operation: "initiate",
			EntityID:  req.AccountId,
			RequestID: req.IdempotencyKey.Key,
		}

		resultData, _ := json.Marshal(map[string]string{
			"log_id": log.LogID.String(),
		})

		_ = s.idempotency.StoreResult(ctx, idempotency.Result{
			Key:         idempotencyKey,
			Status:      idempotency.StatusCompleted,
			Data:        resultData,
			CompletedAt: time.Now(),
			TTL:         24 * time.Hour,
		})
	}

	return &positionkeepingv1.InitiateFinancialPositionLogResponse{
		Log: toProtoFinancialPositionLog(log),
	}, nil
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

// protoEntryToDomain converts a proto TransactionLogEntry to domain
func protoEntryToDomain(proto *positionkeepingv1.TransactionLogEntry) (*domain.TransactionLogEntry, error) {
	if proto == nil {
		return nil, nil
	}

	transactionID, err := uuid.Parse(proto.TransactionId)
	if err != nil {
		return nil, err
	}

	// Convert google.type.Money to domain.Money
	amount := decimal.NewFromInt(proto.Amount.Amount.Units)
	nanos := decimal.NewFromInt(int64(proto.Amount.Amount.Nanos)).Div(decimal.NewFromInt(1_000_000_000))
	totalAmount := amount.Add(nanos)

	money, err := domain.NewMoney(totalAmount, domain.Currency(proto.Amount.Amount.CurrencyCode))
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

// protoLineageToDomain converts a proto TransactionLineage to domain
func protoLineageToDomain(proto *positionkeepingv1.TransactionLineage) (*domain.TransactionLineage, error) {
	if proto == nil {
		return nil, nil
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
