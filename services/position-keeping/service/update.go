package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
)

// UpdateFinancialPositionLog updates an existing financial position log.
// Supports adding new transaction entries, updating status, and audit trail entries.
func (s *PositionKeepingService) UpdateFinancialPositionLog(
	ctx context.Context,
	req *positionkeepingv1.UpdateFinancialPositionLogRequest,
) (resp *positionkeepingv1.UpdateFinancialPositionLogResponse, err error) {
	// Parse and validate log ID
	logID, err := parseUUID(req.GetLogId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid log_id: %v", err)
	}

	// Check idempotency and acquire lock if key provided
	idempotencyKey, cachedResponse, err := s.checkUpdateIdempotencyAndAcquireLock(ctx, req, logID)
	if err != nil {
		return nil, err
	}
	if cachedResponse != nil {
		return cachedResponse, nil
	}

	// Clean up pending idempotency key on error
	if idempotencyKey != nil {
		defer func() {
			if err != nil {
				_ = s.idempotency.Delete(ctx, *idempotencyKey)
			}
		}()
	}

	// Check for context cancellation
	if err := ctx.Err(); err != nil {
		return nil, status.Errorf(codes.Canceled, "request cancelled: %v", err)
	}

	// Retrieve existing log
	log, err := s.repository.FindByID(ctx, logID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "financial position log not found: %s", logID)
		}
		return nil, status.Errorf(codes.Internal, "failed to retrieve financial position log: %v", err)
	}

	// Validate request and check version
	if err := validateUpdateRequest(req, log); err != nil {
		return nil, err
	}

	// Track if we made any changes (for event publishing)
	var newEntryAdded *domain.TransactionLogEntry
	statusChanged := false

	// Add new transaction entry if provided
	if req.NewEntry != nil {
		entry, err := protoEntryToDomain(req.NewEntry)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid new_entry: %v", err)
		}

		if err := log.AddEntry(entry); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "failed to add transaction entry: %v", err)
		}

		newEntryAdded = entry
	}

	// Update status if provided using domain lifecycle methods
	if req.StatusUpdate != nil {
		if err := applyStatusTransition(log, req); err != nil {
			return nil, err
		}
		statusChanged = true
	}

	// Add audit trail entry if provided (and not already added by status lifecycle method)
	if req.AuditEntry != nil && !statusChanged {
		auditEntry, err := protoAuditEntryToDomain(req.AuditEntry)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid audit_entry: %v", err)
		}

		if err := log.AddAuditEntry(auditEntry); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "failed to add audit entry: %v", err)
		}
	}

	// Update the log in repository
	if err := s.repository.Update(ctx, log); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update financial position log: %v", err)
	}

	// Publish events for the changes
	s.publishUpdateEvents(ctx, log, req, newEntryAdded, statusChanged)

	// Store idempotency result if key was provided
	if idempotencyKey != nil {
		if err := storeUpdateIdempotencyResult(ctx, s.idempotency, *idempotencyKey, log); err != nil {
			return nil, err
		}
	}

	resp = &positionkeepingv1.UpdateFinancialPositionLogResponse{
		Log: toProtoFinancialPositionLog(log),
	}
	return resp, nil
}

// checkUpdateIdempotencyAndAcquireLock checks for completed operations and acquires a pending lock.
func (s *PositionKeepingService) checkUpdateIdempotencyAndAcquireLock(
	ctx context.Context,
	req *positionkeepingv1.UpdateFinancialPositionLogRequest,
	logID uuid.UUID,
) (*idempotency.Key, *positionkeepingv1.UpdateFinancialPositionLogResponse, error) {
	// No idempotency key provided
	if req.IdempotencyKey == nil || req.IdempotencyKey.Key == "" {
		return nil, nil, nil
	}

	key := idempotency.Key{
		Namespace: "position-keeping",
		Operation: "update",
		EntityID:  logID.String(),
		RequestID: req.IdempotencyKey.Key,
	}

	// Check if operation was already completed
	result, err := s.idempotency.Check(ctx, key)
	if err == nil && result.Status == idempotency.StatusCompleted {
		// Return cached result
		var cachedData struct {
			LogID   string `json:"log_id"`
			Version int64  `json:"version"`
		}
		if err := json.Unmarshal(result.Data, &cachedData); err != nil {
			return nil, nil, status.Errorf(codes.Internal, "failed to decode cached idempotency response: %v", err)
		}

		cachedLogID, err := uuid.Parse(cachedData.LogID)
		if err != nil {
			return nil, nil, status.Errorf(codes.Internal, "cached idempotency response contains invalid log_id: %v", err)
		}

		log, err := s.repository.FindByID(ctx, cachedLogID)
		if err != nil {
			return nil, nil, status.Errorf(codes.Internal, "failed to load cached financial position log: %v", err)
		}

		return &key, &positionkeepingv1.UpdateFinancialPositionLogResponse{
			Log: toProtoFinancialPositionLog(log),
		}, nil
	}

	// Mark operation as pending
	if err := s.idempotency.MarkPending(ctx, key, 5*time.Minute); err != nil {
		return nil, nil, status.Errorf(codes.Internal, "failed to mark operation as pending: %v", err)
	}

	return &key, nil, nil
}

// validateUpdateRequest validates the update request and checks version
func validateUpdateRequest(req *positionkeepingv1.UpdateFinancialPositionLogRequest, log *domain.FinancialPositionLog) error {
	// Check version for optimistic concurrency control
	if req.Version != log.Version {
		return status.Errorf(codes.Aborted,
			"version conflict: expected version %d, got version %d",
			log.Version, req.Version)
	}

	// Validate audit entry is provided when making changes
	// Audit trail is required for compliance when adding entries or changing status
	if (req.NewEntry != nil || req.StatusUpdate != nil) && req.AuditEntry == nil {
		return status.Error(codes.InvalidArgument, "audit_entry is required when adding entries or updating status")
	}

	return nil
}

// storeUpdateIdempotencyResult stores the idempotency result for an update operation
func storeUpdateIdempotencyResult(
	ctx context.Context,
	idempotencySvc idempotency.Service,
	key idempotency.Key,
	log *domain.FinancialPositionLog,
) error {
	resultData, err := json.Marshal(map[string]interface{}{
		"log_id":  log.LogID.String(),
		"version": log.Version,
	})
	if err != nil {
		return status.Errorf(codes.Internal, "failed to marshal idempotency result: %v", err)
	}

	if err := idempotencySvc.StoreResult(ctx, idempotency.Result{
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

// applyStatusTransition applies a status transition to the log using domain lifecycle methods
func applyStatusTransition(log *domain.FinancialPositionLog, req *positionkeepingv1.UpdateFinancialPositionLogRequest) error {
	newStatus := fromProtoTransactionStatus(req.StatusUpdate.CurrentStatus)
	auditEntry, err := protoAuditEntryToDomain(req.AuditEntry)
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "invalid audit_entry: %v", err)
	}

	switch newStatus {
	case domain.TransactionStatusPosted:
		if err := log.MarkPosted(req.StatusUpdate.StatusReason, auditEntry); err != nil {
			return status.Errorf(codes.InvalidArgument, "failed to mark as posted: %v", err)
		}
	case domain.TransactionStatusFailed:
		if err := log.Fail(req.StatusUpdate.StatusReason, auditEntry); err != nil {
			return status.Errorf(codes.InvalidArgument, "failed to mark as failed: %v", err)
		}
	case domain.TransactionStatusCancelled:
		if err := log.Cancel(req.StatusUpdate.StatusReason, auditEntry); err != nil {
			return status.Errorf(codes.InvalidArgument, "failed to cancel: %v", err)
		}
	case domain.TransactionStatusRejected:
		if err := log.Reject(req.StatusUpdate.StatusReason, auditEntry); err != nil {
			return status.Errorf(codes.InvalidArgument, "failed to reject: %v", err)
		}
	case domain.TransactionStatusAmended:
		if err := log.Amend(req.StatusUpdate.StatusReason, auditEntry); err != nil {
			return status.Errorf(codes.InvalidArgument, "failed to amend: %v", err)
		}
	case domain.TransactionStatusPending, domain.TransactionStatusReconciled, domain.TransactionStatusReversed:
		return status.Errorf(codes.InvalidArgument, "status %v cannot be directly set via Update operation", newStatus)
	default:
		return status.Errorf(codes.InvalidArgument, "unsupported status transition: %v", newStatus)
	}

	return nil
}

// protoAuditEntryToDomain converts proto AuditTrailEntry to domain.
// Returns (nil, nil) for nil input to handle optional proto fields.
func protoAuditEntryToDomain(proto *positionkeepingv1.AuditTrailEntry) (*domain.AuditTrailEntry, error) {
	if proto == nil {
		return nil, nil //nolint:nilnil // Intentional: nil input returns nil output for optional field handling
	}

	// TODO: Extract IP address from gRPC context/metadata
	ipAddress := ""

	// TODO: Extract system context from gRPC context/metadata
	systemContext := make(map[string]string)

	return domain.NewAuditTrailEntry(
		proto.UserId,
		proto.Action,
		proto.Details,
		ipAddress,
		systemContext,
	)
}

// publishUpdateEvents publishes domain events for update operations
func (s *PositionKeepingService) publishUpdateEvents(
	ctx context.Context,
	log *domain.FinancialPositionLog,
	req *positionkeepingv1.UpdateFinancialPositionLogRequest,
	newEntryAdded *domain.TransactionLogEntry,
	statusChanged bool,
) {
	if newEntryAdded != nil {
		event := &domain.TransactionAmended{
			LogID:         log.LogID,
			AccountID:     log.AccountID,
			Reason:        "Transaction entry added",
			AmendedBy:     req.AuditEntry.GetUserId(),
			CorrelationID: "", // TODO: Extract from context
			Timestamp:     time.Now().UTC(),
			Version:       log.Version,
		}
		_ = s.eventPublisher.Publish(ctx, event)
	}

	if statusChanged {
		s.publishStatusChangeEvent(ctx, log, req)
	}
}

// publishStatusChangeEvent publishes appropriate event for status transitions
func (s *PositionKeepingService) publishStatusChangeEvent(
	ctx context.Context,
	log *domain.FinancialPositionLog,
	req *positionkeepingv1.UpdateFinancialPositionLogRequest,
) {
	switch req.StatusUpdate.CurrentStatus {
	case commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED:
		event := &domain.TransactionPosted{
			LogID:            log.LogID,
			AccountID:        log.AccountID,
			PostingReference: "", // TODO: Add to proto if needed
			Reason:           req.StatusUpdate.StatusReason,
			PostedBy:         req.AuditEntry.GetUserId(),
			CorrelationID:    "",
			Timestamp:        time.Now().UTC(),
			Version:          log.Version,
		}
		_ = s.eventPublisher.Publish(ctx, event)
	case commonv1.TransactionStatus_TRANSACTION_STATUS_FAILED:
		event := &domain.TransactionFailed{
			LogID:         log.LogID,
			AccountID:     log.AccountID,
			FailureReason: req.StatusUpdate.StatusReason,
			ErrorCode:     "", // TODO: Add to proto if needed
			CorrelationID: "",
			Timestamp:     time.Now().UTC(),
			Version:       log.Version,
		}
		_ = s.eventPublisher.Publish(ctx, event)
	case commonv1.TransactionStatus_TRANSACTION_STATUS_CANCELLED:
		event := &domain.TransactionCancelled{
			LogID:         log.LogID,
			AccountID:     log.AccountID,
			Reason:        req.StatusUpdate.StatusReason,
			CancelledBy:   req.AuditEntry.GetUserId(),
			CorrelationID: "",
			Timestamp:     time.Now().UTC(),
			Version:       log.Version,
		}
		_ = s.eventPublisher.Publish(ctx, event)
	case commonv1.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED,
		commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING,
		commonv1.TransactionStatus_TRANSACTION_STATUS_REVERSED:
		// No events for these statuses (REVERSED cannot be set via Update operation)
	default:
		// No event for other statuses
	}
}
