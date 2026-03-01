package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/shared/pkg/clients"
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
		if err := applyStatusTransition(ctx, log, req); err != nil {
			return nil, err
		}
		statusChanged = true
	}

	// Add audit trail entry if provided (and not already added by status lifecycle method)
	if req.AuditEntry != nil && !statusChanged {
		auditEntry, err := protoAuditEntryToDomain(ctx, req.AuditEntry)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid audit_entry: %v", err)
		}

		if err := log.AddAuditEntry(auditEntry); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "failed to add audit entry: %v", err)
		}
	}

	// Collect domain events for the changes
	updateEvents := s.collectUpdateEvents(ctx, log, req, newEntryAdded, statusChanged)

	// Update the log in repository atomically with outbox event writes.
	outboxFn := s.outboxPublisher.BuildOutboxFn(ctx, updateEvents)
	if err := s.repository.UpdateWithOutbox(ctx, log, outboxFn); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "financial position log not found: %s", logID)
		}
		if errors.Is(err, domain.ErrOptimisticLock) {
			return nil, status.Errorf(codes.Aborted, "version conflict during update: %v", err)
		}
		return nil, status.Errorf(codes.Internal, "failed to update financial position log: %v", err)
	}

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
func applyStatusTransition(ctx context.Context, log *domain.FinancialPositionLog, req *positionkeepingv1.UpdateFinancialPositionLogRequest) error {
	newStatus := fromProtoTransactionStatus(req.StatusUpdate.CurrentStatus)
	auditEntry, err := protoAuditEntryToDomain(ctx, req.AuditEntry)
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
// Extracts IP address and system context from gRPC context for audit trail enrichment.
// Returns (nil, nil) for nil input to handle optional proto fields.
func protoAuditEntryToDomain(ctx context.Context, proto *positionkeepingv1.AuditTrailEntry) (*domain.AuditTrailEntry, error) {
	if proto == nil {
		return nil, nil //nolint:nilnil // Intentional: nil input returns nil output for optional field handling
	}

	ipAddress := extractIPAddress(ctx)
	systemContext := extractSystemContext(ctx)

	return domain.NewAuditTrailEntry(
		proto.UserId,
		proto.Action,
		proto.Details,
		ipAddress,
		systemContext,
	)
}

// extractIPAddress extracts client IP from gRPC context.
// Checks x-forwarded-for header first (for proxied requests), then falls back to peer address.
func extractIPAddress(ctx context.Context) string {
	if ctx == nil {
		return ""
	}

	// Check metadata for forwarded IP (common in Kubernetes/Istio environments)
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		// x-forwarded-for may contain multiple IPs (client, proxy1, proxy2...)
		// First IP is the original client
		if vals := md.Get("x-forwarded-for"); len(vals) > 0 && vals[0] != "" {
			// Split on comma and take first IP
			if ips := strings.Split(vals[0], ","); len(ips) > 0 {
				return strings.TrimSpace(ips[0])
			}
		}
		// Fallback to x-real-ip header
		if vals := md.Get("x-real-ip"); len(vals) > 0 && vals[0] != "" {
			return vals[0]
		}
	}

	// Fallback to peer address (direct gRPC connection)
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		// Extract just the IP portion (peer address may include port like "192.168.1.1:50051")
		addr := p.Addr.String()
		if idx := strings.LastIndex(addr, ":"); idx > 0 {
			return addr[:idx]
		}
		return addr
	}

	return "" // Empty string is acceptable for system operations
}

// extractSystemContext extracts service metadata from gRPC context.
// Includes service name, correlation ID, and tenant ID for multi-tenant tracking.
func extractSystemContext(ctx context.Context) map[string]string {
	systemCtx := map[string]string{
		"service": "position-keeping",
	}

	if ctx == nil {
		return systemCtx
	}

	if md, ok := metadata.FromIncomingContext(ctx); ok {
		// Add correlation ID if present
		if vals := md.Get("x-correlation-id"); len(vals) > 0 && vals[0] != "" {
			systemCtx["correlation_id"] = vals[0]
		}
		// Also check other common correlation ID header names
		if _, exists := systemCtx["correlation_id"]; !exists {
			for _, key := range []string{"correlation-id", "x-request-id", "request-id"} {
				if vals := md.Get(key); len(vals) > 0 && vals[0] != "" {
					systemCtx["correlation_id"] = vals[0]
					break
				}
			}
		}
		// Add tenant ID if present (multi-tenant context)
		if vals := md.Get("x-tenant-id"); len(vals) > 0 && vals[0] != "" {
			systemCtx["tenant_id"] = vals[0]
		}
		// Add user agent if present
		if vals := md.Get("user-agent"); len(vals) > 0 && vals[0] != "" {
			systemCtx["user_agent"] = vals[0]
		}
	}

	return systemCtx
}

// collectUpdateEvents collects domain events for update operations.
// Returns the events to be written to the outbox within the same transaction as the update.
func (s *PositionKeepingService) collectUpdateEvents(
	ctx context.Context,
	log *domain.FinancialPositionLog,
	req *positionkeepingv1.UpdateFinancialPositionLogRequest,
	newEntryAdded *domain.TransactionLogEntry,
	statusChanged bool,
) []domain.DomainEvent {
	correlationID := clients.ExtractCorrelationID(ctx)
	var evts []domain.DomainEvent

	if newEntryAdded != nil {
		evts = append(evts, &domain.TransactionAmended{
			LogID:         log.LogID,
			AccountID:     log.AccountID,
			Reason:        "Transaction entry added",
			AmendedBy:     req.AuditEntry.GetUserId(),
			CorrelationID: correlationID,
			Timestamp:     time.Now().UTC(),
			Version:       log.Version,
		})
	}

	if statusChanged {
		if evt := s.buildStatusChangeEvent(ctx, log, req, correlationID); evt != nil {
			evts = append(evts, evt)
		}
	}

	return evts
}

// buildStatusChangeEvent returns the appropriate domain event for a status transition,
// or nil if the status does not produce an event.
func (s *PositionKeepingService) buildStatusChangeEvent(
	_ context.Context,
	log *domain.FinancialPositionLog,
	req *positionkeepingv1.UpdateFinancialPositionLogRequest,
	correlationID string,
) domain.DomainEvent {
	switch req.StatusUpdate.CurrentStatus {
	case commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED:
		return &domain.TransactionPosted{
			LogID:            log.LogID,
			AccountID:        log.AccountID,
			PostingReference: "", // Populate when PostingReference is added to proto
			Reason:           req.StatusUpdate.StatusReason,
			PostedBy:         req.AuditEntry.GetUserId(),
			CorrelationID:    correlationID,
			Timestamp:        time.Now().UTC(),
			Version:          log.Version,
		}
	case commonv1.TransactionStatus_TRANSACTION_STATUS_FAILED:
		return &domain.TransactionFailed{
			LogID:         log.LogID,
			AccountID:     log.AccountID,
			FailureReason: req.StatusUpdate.StatusReason,
			ErrorCode:     commonv1.ErrorCode_ERROR_CODE_INTERNAL,
			CorrelationID: correlationID,
			Timestamp:     time.Now().UTC(),
			Version:       log.Version,
		}
	case commonv1.TransactionStatus_TRANSACTION_STATUS_CANCELLED:
		return &domain.TransactionCancelled{
			LogID:         log.LogID,
			AccountID:     log.AccountID,
			Reason:        req.StatusUpdate.StatusReason,
			CancelledBy:   req.AuditEntry.GetUserId(),
			CorrelationID: correlationID,
			Timestamp:     time.Now().UTC(),
			Version:       log.Version,
		}
	case commonv1.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED,
		commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING,
		commonv1.TransactionStatus_TRANSACTION_STATUS_REVERSED:
		// No event for unspecified, pending, or reversed status transitions
		return nil
	default:
		return nil
	}
}
