package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	"github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
	"github.com/meridianhub/meridian/services/financial-accounting/domain"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// InitiateFinancialBookingLog creates a new financial booking log.
//
// Workflow:
// 1. Check idempotency using request's IdempotencyKey
// 2. Validate all request fields
// 3. Create domain entity
// 4. Persist booking log
// 5. Return gRPC response with created booking log
//
// Error mapping:
// - Invalid request fields -> codes.InvalidArgument
// - Duplicate idempotency key -> codes.AlreadyExists
// - Internal errors -> codes.Internal
func (s *FinancialAccountingService) InitiateFinancialBookingLog(
	ctx context.Context,
	req *financialaccountingv1.InitiateFinancialBookingLogRequest,
) (*financialaccountingv1.InitiateFinancialBookingLogResponse, error) {
	// Validate idempotency key is provided
	if req.IdempotencyKey == nil || req.IdempotencyKey.Key == "" {
		return nil, status.Error(codes.InvalidArgument, "idempotency_key is required")
	}

	idempotencyKey := idempotency.Key{
		Namespace: "financial-accounting",
		Operation: "initiate-booking-log",
		EntityID:  req.IdempotencyKey.Key,
		RequestID: req.IdempotencyKey.Key,
	}

	// Check idempotency and mark pending (skip if service not configured)
	if err := s.checkAndMarkPendingSimple(ctx, idempotencyKey); err != nil {
		return nil, err
	}

	// Validate request fields
	params, err := s.validateInitiateBookingLogRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	// Create domain entity
	bookingLog := domain.NewFinancialBookingLog(
		params.AccountType,
		params.ProductServiceReference,
		params.BusinessUnitReference,
		params.ChartOfAccountsRules,
		params.BaseCurrency,
	)

	// Persist booking log
	if err := s.repository.SaveBookingLog(ctx, bookingLog, req.IdempotencyKey.Key); err != nil {
		if errors.Is(err, persistence.ErrDuplicateIdempotencyKey) {
			return nil, status.Error(codes.AlreadyExists, "request with this idempotency key already processed")
		}
		return nil, status.Errorf(codes.Internal, "failed to save booking log: %v", err)
	}

	// Publish FinancialBookingLogInitiatedEvent (best-effort)
	correlationID := ""
	if req.IdempotencyKey != nil {
		correlationID = req.IdempotencyKey.Key
	}
	event := buildBookingLogInitiatedEvent(bookingLog, correlationID)
	if err := s.eventPublisher.Publish(ctx, event); err != nil {
		slog.Error("failed to publish FinancialBookingLogInitiatedEvent",
			"error", err,
			"booking_log_id", bookingLog.ID.String())
	}

	// Store idempotency result (only if service configured)
	s.storeInitiateIdempotencyResult(ctx, idempotencyKey, req.IdempotencyKey)

	return &financialaccountingv1.InitiateFinancialBookingLogResponse{
		FinancialBookingLog: toProtoFinancialBookingLog(bookingLog),
	}, nil
}

// checkAndMarkPendingSimple checks idempotency and marks the operation as pending.
// Used by create operations where no cached response data is returned (just AlreadyExists).
// Returns nil if idempotency service is not configured.
func (s *FinancialAccountingService) checkAndMarkPendingSimple(ctx context.Context, key idempotency.Key) error {
	if s.idempotency == nil {
		return nil
	}

	result, err := s.idempotency.Check(ctx, key)
	if err != nil && !errors.Is(err, idempotency.ErrResultNotFound) {
		if errors.Is(err, idempotency.ErrOperationAlreadyProcessed) {
			if result != nil && result.Status == idempotency.StatusCompleted {
				return status.Error(codes.AlreadyExists, "request with this idempotency key already processed")
			}
		}
		return status.Errorf(codes.Internal, "failed to check idempotency: %v", err)
	}

	if err := s.idempotency.MarkPending(ctx, key, defaultIdempotencyTTL); err != nil {
		return status.Errorf(codes.Internal, "failed to mark operation as pending: %v", err)
	}
	return nil
}

// storeInitiateIdempotencyResult stores a completed idempotency result for create operations.
// Uses the TTL from the idempotency key if provided, otherwise defaults.
func (s *FinancialAccountingService) storeInitiateIdempotencyResult(ctx context.Context, key idempotency.Key, idempKey *commonv1.IdempotencyKey) {
	if s.idempotency == nil {
		return
	}
	ttl := defaultIdempotencyTTL
	if idempKey.TtlSeconds > 0 {
		ttl = time.Duration(idempKey.TtlSeconds) * time.Second
	}
	idempResult := idempotency.Result{
		Key:         key,
		Status:      idempotency.StatusCompleted,
		Data:        nil,
		CompletedAt: time.Now(),
		TTL:         ttl,
	}
	_ = s.idempotency.StoreResult(ctx, idempResult)
}

// RetrieveFinancialBookingLog retrieves a specific booking log by ID.
//
// gRPC Error Codes:
//   - codes.InvalidArgument: Invalid booking log ID format
//   - codes.NotFound: Booking log does not exist
//   - codes.Internal: Database or system errors
func (s *FinancialAccountingService) RetrieveFinancialBookingLog(
	ctx context.Context,
	req *financialaccountingv1.RetrieveFinancialBookingLogRequest,
) (*financialaccountingv1.RetrieveFinancialBookingLogResponse, error) {
	// Parse and validate booking log ID
	bookingLogID, err := parseUUID(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid booking log id: %v", err)
	}

	// Retrieve from repository
	bookingLog, err := s.repository.GetBookingLog(ctx, bookingLogID)
	if err != nil {
		if errors.Is(err, persistence.ErrBookingLogNotFound) {
			return nil, status.Errorf(codes.NotFound, "financial booking log not found: %s", bookingLogID)
		}
		return nil, status.Error(codes.Internal, "failed to retrieve booking log")
	}

	// Load postings separately (not embedded in GetBookingLog to avoid N+1 in list queries)
	postings, err := s.repository.GetPostingsByBookingLogID(ctx, bookingLogID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to retrieve postings for booking log: %v", err)
	}
	enriched := *bookingLog
	for _, p := range postings {
		enriched = enriched.WithPosting(p)
	}
	bookingLog = &enriched

	// Convert to protobuf and return
	return &financialaccountingv1.RetrieveFinancialBookingLogResponse{
		FinancialBookingLog: toProtoFinancialBookingLog(bookingLog),
	}, nil
}

// UpdateFinancialBookingLog updates an existing booking log's status and rules.
//
// Workflow:
// 1. Check idempotency using request's IdempotencyKey
// 2. Parse and validate request fields
// 3. Retrieve existing booking log by ID
// 4. Validate state transition rules
// 5. Apply updates using domain methods
// 6. Persist updated booking log
// 7. Return updated booking log
//
// Idempotency Note:
// Unlike InitiateFinancialBookingLog where idempotency is optional (create operations
// naturally fail on duplicate IDs), update operations REQUIRE idempotency keys
// because state-machine transitions must be exactly-once. A duplicate update
// could incorrectly transition an entity through multiple states.
//
// Error mapping:
// - Invalid request fields -> codes.InvalidArgument
// - Duplicate idempotency key -> codes.AlreadyExists
// - Booking log not found -> codes.NotFound
// - Invalid state transition -> codes.FailedPrecondition
// - Internal errors -> codes.Internal
func (s *FinancialAccountingService) UpdateFinancialBookingLog(
	ctx context.Context,
	req *financialaccountingv1.UpdateFinancialBookingLogRequest,
) (*financialaccountingv1.UpdateFinancialBookingLogResponse, error) {
	// Validate idempotency key is provided
	if req.IdempotencyKey == nil || req.IdempotencyKey.Key == "" {
		return nil, status.Error(codes.InvalidArgument, "idempotency_key is required")
	}

	idempotencyKey := idempotency.Key{
		Namespace: "financial-accounting",
		Operation: "update-booking-log",
		EntityID:  req.GetId(),
		RequestID: req.IdempotencyKey.Key,
	}

	ttl := idempotencyTTLFromKey(req.IdempotencyKey.TtlSeconds)

	// Use idempotency executor to wrap business logic with atomic PENDING cleanup.
	var response *financialaccountingv1.UpdateFinancialBookingLogResponse

	execResult, err := s.idempotencyExecutor.Execute(ctx, idempotencyKey, ttl, func(ctx context.Context) ([]byte, error) {
		resp, execErr := s.executeUpdateFinancialBookingLog(ctx, req)
		if execErr != nil {
			return nil, execErr
		}
		response = resp
		return marshalForCache(resp, req.IdempotencyKey.Key, "update-booking-log"), nil
	})
	if err != nil {
		return nil, mapIdempotencyExecutorError(err)
	}

	if execResult.FromCache {
		return handleCachedUpdateBookingLogResponse(execResult.Data, req.IdempotencyKey.Key, req.GetId())
	}

	return response, nil
}

// executeUpdateFinancialBookingLog contains the core business logic for UpdateFinancialBookingLog.
// This is separated from the main method to allow the idempotency executor to wrap it.
func (s *FinancialAccountingService) executeUpdateFinancialBookingLog(
	ctx context.Context,
	req *financialaccountingv1.UpdateFinancialBookingLogRequest,
) (*financialaccountingv1.UpdateFinancialBookingLogResponse, error) {
	bookingLogID, err := parseUUID(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid id: %v", err)
	}

	if req.Status == commonv1.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED {
		return nil, status.Error(codes.InvalidArgument, "status must be specified")
	}
	newStatus := fromProtoTransactionStatus(req.Status)

	// Retrieve and validate transition
	bookingLog, err := s.retrieveAndValidateBookingLogTransition(ctx, bookingLogID, newStatus)
	if err != nil {
		return nil, err
	}

	previousStatus := bookingLog.Status

	// Apply updates
	updated := bookingLog.WithStatus(newStatus)
	if req.ChartOfAccountsRules != "" {
		updated = updated.WithChartOfAccountsRules(req.ChartOfAccountsRules)
	}

	// Persist and publish event
	if err := s.persistAndPublishBookingLogUpdate(ctx, bookingLogID, &updated, previousStatus, newStatus, req.IdempotencyKey); err != nil {
		return nil, err
	}

	return &financialaccountingv1.UpdateFinancialBookingLogResponse{
		FinancialBookingLog: toProtoFinancialBookingLog(&updated),
	}, nil
}

// retrieveAndValidateBookingLogTransition retrieves a booking log and validates the requested
// status transition, including double-entry balance checks for POSTED transitions.
func (s *FinancialAccountingService) retrieveAndValidateBookingLogTransition(
	ctx context.Context,
	bookingLogID [16]byte,
	newStatus domain.TransactionStatus,
) (*domain.FinancialBookingLog, error) {
	bookingLog, err := s.repository.GetBookingLog(ctx, bookingLogID)
	if err != nil {
		if errors.Is(err, persistence.ErrBookingLogNotFound) {
			return nil, status.Errorf(codes.NotFound, "financial booking log not found: %s", bookingLogID)
		}
		return nil, status.Errorf(codes.Internal, "failed to retrieve booking log: %v", err)
	}

	if !isValidBookingLogTransition(bookingLog.Status, newStatus) {
		return nil, status.Errorf(codes.FailedPrecondition,
			"invalid status transition from %s to %s", bookingLog.Status, newStatus)
	}

	if newStatus == domain.TransactionStatusPosted {
		if err := s.validateDoubleEntryBalance(ctx, bookingLogID); err != nil {
			return nil, err
		}
	}
	return bookingLog, nil
}

// persistAndPublishBookingLogUpdate persists a booking log update and publishes
// the FinancialBookingLogUpdatedEvent (best-effort).
func (s *FinancialAccountingService) persistAndPublishBookingLogUpdate(
	ctx context.Context,
	bookingLogID [16]byte,
	updated *domain.FinancialBookingLog,
	previousStatus, newStatus domain.TransactionStatus,
	idempKey *commonv1.IdempotencyKey,
) error {
	if err := s.repository.UpdateBookingLog(ctx, updated); err != nil {
		if errors.Is(err, persistence.ErrBookingLogNotFound) {
			return status.Errorf(codes.NotFound, "financial booking log not found: %s", bookingLogID)
		}
		return status.Errorf(codes.Internal, "failed to update booking log: %v", err)
	}

	correlationID := ""
	if idempKey != nil {
		correlationID = idempKey.Key
	}
	event := buildBookingLogUpdatedEvent(bookingLogID, updated, previousStatus, newStatus, correlationID, extractUserFromContext(ctx))
	if err := s.eventPublisher.Publish(ctx, event); err != nil {
		slog.Error("failed to publish FinancialBookingLogUpdatedEvent",
			"error", err,
			"booking_log_id", bookingLogID,
			"previous_status", previousStatus,
			"new_status", newStatus)
	}
	return nil
}

// isValidBookingLogTransition validates that a status transition is allowed.
//
// Valid transitions:
//
//	From PENDING:
//	  - PENDING -> PENDING (no-op, valid but does nothing)
//	  - PENDING -> POSTED (when all postings balance and are processed)
//	  - PENDING -> FAILED (validation or processing error)
//	  - PENDING -> CANCELLED (business cancellation request)
//
//	From POSTED:
//	  - POSTED -> REVERSED (for correcting errors via reversal entries)
//
// Invalid transitions:
//   - PENDING -> REVERSED (must be POSTED first to reverse)
//   - Any transition from terminal states (FAILED, CANCELLED, REVERSED)
func isValidBookingLogTransition(from, to domain.TransactionStatus) bool {
	switch from {
	case domain.TransactionStatusPending:
		switch to {
		case domain.TransactionStatusPending, // No-op but valid
			domain.TransactionStatusPosted,
			domain.TransactionStatusFailed,
			domain.TransactionStatusCancelled:
			return true
		case domain.TransactionStatusReversed:
			// PENDING -> REVERSED is invalid (must be POSTED first)
			return false
		}
	case domain.TransactionStatusPosted:
		// Only REVERSED is valid from POSTED
		return to == domain.TransactionStatusReversed
	case domain.TransactionStatusFailed,
		domain.TransactionStatusCancelled,
		domain.TransactionStatusReversed:
		// Terminal states - no transitions allowed
		return false
	}
	return false
}
