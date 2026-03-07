package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	"github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
	"github.com/meridianhub/meridian/services/financial-accounting/domain"
	"github.com/meridianhub/meridian/services/financial-accounting/observability"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/pkg/refdata"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
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

	// Check idempotency (skip if service not configured - e.g., Redis unavailable in dev)
	if s.idempotency != nil {
		result, err := s.idempotency.Check(ctx, idempotencyKey)
		if err != nil && !errors.Is(err, idempotency.ErrResultNotFound) {
			if errors.Is(err, idempotency.ErrOperationAlreadyProcessed) {
				if result != nil && result.Status == idempotency.StatusCompleted {
					return nil, status.Error(codes.AlreadyExists, "request with this idempotency key already processed")
				}
			}
			return nil, status.Errorf(codes.Internal, "failed to check idempotency: %v", err)
		}

		// Mark as pending to prevent concurrent processing
		if err := s.idempotency.MarkPending(ctx, idempotencyKey, defaultIdempotencyTTL); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to mark operation as pending: %v", err)
		}
	}

	// Validate account type
	if req.FinancialAccountType == "" {
		return nil, status.Error(codes.InvalidArgument, "financial_account_type must be specified")
	}
	accountType := fromProtoAccountType(req.FinancialAccountType)

	// Validate product service reference
	if req.ProductServiceReference == "" {
		return nil, status.Error(codes.InvalidArgument, "product_service_reference is required")
	}

	// Validate business unit reference
	if req.BusinessUnitReference == "" {
		return nil, status.Error(codes.InvalidArgument, "business_unit_reference is required")
	}

	// Validate chart of accounts rules
	if req.ChartOfAccountsRules == "" {
		return nil, status.Error(codes.InvalidArgument, "chart_of_accounts_rules is required")
	}

	// Validate base instrument code
	if req.BaseInstrumentCode == "" {
		return nil, status.Error(codes.InvalidArgument, "base_instrument_code must be specified")
	}
	if s.instrumentResolver != nil {
		if _, err := s.instrumentResolver.Resolve(ctx, req.BaseInstrumentCode); err != nil {
			if errors.Is(err, refdata.ErrUnknownInstrument) {
				return nil, status.Errorf(codes.InvalidArgument, "unknown base_instrument_code: %s", req.BaseInstrumentCode)
			}
			return nil, status.Errorf(codes.Unavailable, "instrument lookup failed for %s, please retry", req.BaseInstrumentCode)
		}
	}
	baseCurrency := domain.Currency(req.BaseInstrumentCode)

	// Create domain entity
	bookingLog := domain.NewFinancialBookingLog(
		accountType,
		req.ProductServiceReference,
		req.BusinessUnitReference,
		req.ChartOfAccountsRules,
		baseCurrency,
	)

	// Persist booking log
	if err := s.repository.SaveBookingLog(ctx, bookingLog, req.IdempotencyKey.Key); err != nil {
		if errors.Is(err, persistence.ErrDuplicateIdempotencyKey) {
			return nil, status.Error(codes.AlreadyExists, "request with this idempotency key already processed")
		}
		return nil, status.Errorf(codes.Internal, "failed to save booking log: %v", err)
	}

	// Publish FinancialBookingLogInitiatedEvent for inter-service coordination
	// Event publishing is best-effort - errors are logged but don't fail the operation
	correlationID := ""
	if req.IdempotencyKey != nil {
		correlationID = req.IdempotencyKey.Key
	}
	event := &eventsv1.FinancialBookingLogInitiatedEvent{
		BookingLogId:            bookingLog.ID.String(),
		FinancialAccountType:    toProtoAccountType(bookingLog.FinancialAccountType),
		ProductServiceReference: bookingLog.ProductServiceReference,
		BusinessUnitReference:   bookingLog.BusinessUnitReference,
		BaseInstrumentCode:      string(bookingLog.BaseCurrency),
		CorrelationId:           correlationID,
		CausationId:             correlationID, // Request caused this event
		Timestamp:               timestamppb.Now(),
		Version:                 1, // Initial version for newly created booking log
	}
	if err := s.eventPublisher.Publish(ctx, event); err != nil {
		slog.Error("failed to publish FinancialBookingLogInitiatedEvent",
			"error", err,
			"booking_log_id", bookingLog.ID.String())
	}

	// Store idempotency result (only if service configured)
	if s.idempotency != nil {
		ttl := defaultIdempotencyTTL
		if req.IdempotencyKey.TtlSeconds > 0 {
			ttl = time.Duration(req.IdempotencyKey.TtlSeconds) * time.Second
		}
		idempResult := idempotency.Result{
			Key:         idempotencyKey,
			Status:      idempotency.StatusCompleted,
			Data:        nil,
			CompletedAt: time.Now(),
			TTL:         ttl,
		}
		_ = s.idempotency.StoreResult(ctx, idempResult)
	}

	// Convert to proto response
	return &financialaccountingv1.InitiateFinancialBookingLogResponse{
		FinancialBookingLog: toProtoFinancialBookingLog(bookingLog),
	}, nil
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

	// Determine TTL for idempotency key
	ttl := defaultIdempotencyTTL
	if req.IdempotencyKey.TtlSeconds > 0 {
		ttl = time.Duration(req.IdempotencyKey.TtlSeconds) * time.Second
	}

	// Use idempotency executor to wrap business logic with atomic PENDING cleanup.
	// This ensures orphaned PENDING keys are cleaned up if the operation fails.
	var response *financialaccountingv1.UpdateFinancialBookingLogResponse

	execResult, err := s.idempotencyExecutor.Execute(ctx, idempotencyKey, ttl, func(ctx context.Context) ([]byte, error) {
		// Execute business logic
		resp, execErr := s.executeUpdateFinancialBookingLog(ctx, req)
		if execErr != nil {
			return nil, execErr
		}

		// Serialize response for idempotency cache
		responseData, marshalErr := proto.Marshal(resp)
		if marshalErr != nil {
			slog.Error("failed to serialize response for idempotency cache",
				"error", marshalErr,
				"idempotency_key", req.IdempotencyKey.Key,
				"operation", "update-booking-log")
			// Still return success - the operation completed, just caching failed
			responseData = nil
		}

		response = resp
		return responseData, nil
	})
	if err != nil {
		// Handle specific idempotency errors
		if errors.Is(err, idempotency.ErrOperationInProgress) {
			return nil, status.Error(codes.Aborted, "operation already in progress")
		}
		// ExecutorErrors wrap idempotency layer errors - return as Internal
		var execErr *idempotency.ExecutorError
		if errors.As(err, &execErr) {
			return nil, status.Errorf(codes.Internal, "idempotency error: %v", err)
		}
		// Business logic errors from the fn() callback pass through directly
		// These are already gRPC status errors, so return as-is
		return nil, err
	}

	// Handle cached result
	if execResult.FromCache {
		if len(execResult.Data) > 0 {
			var cachedResponse financialaccountingv1.UpdateFinancialBookingLogResponse
			if unmarshalErr := proto.Unmarshal(execResult.Data, &cachedResponse); unmarshalErr != nil {
				slog.Error("failed to deserialize cached idempotency response",
					"error", unmarshalErr,
					"idempotency_key", req.IdempotencyKey.Key,
					"operation", "update-booking-log")
				return nil, status.Error(codes.AlreadyExists, "request with this idempotency key already processed")
			}
			slog.Info("returning cached idempotent response",
				"idempotency_key", req.IdempotencyKey.Key,
				"operation", "update-booking-log",
				"booking_log_id", req.GetId())
			return &cachedResponse, nil
		}
		return nil, status.Error(codes.AlreadyExists, "request with this idempotency key already processed")
	}

	return response, nil
}

// executeUpdateFinancialBookingLog contains the core business logic for UpdateFinancialBookingLog.
// This is separated from the main method to allow the idempotency executor to wrap it.
func (s *FinancialAccountingService) executeUpdateFinancialBookingLog(
	ctx context.Context,
	req *financialaccountingv1.UpdateFinancialBookingLogRequest,
) (*financialaccountingv1.UpdateFinancialBookingLogResponse, error) {
	// Parse booking log ID
	bookingLogID, err := parseUUID(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid id: %v", err)
	}

	// Validate status
	if req.Status == commonv1.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED {
		return nil, status.Error(codes.InvalidArgument, "status must be specified")
	}
	newStatus := fromProtoTransactionStatus(req.Status)

	// Retrieve existing booking log
	bookingLog, err := s.repository.GetBookingLog(ctx, bookingLogID)
	if err != nil {
		if errors.Is(err, persistence.ErrBookingLogNotFound) {
			return nil, status.Errorf(codes.NotFound, "financial booking log not found: %s", bookingLogID)
		}
		return nil, status.Errorf(codes.Internal, "failed to retrieve booking log: %v", err)
	}

	// Capture previous status BEFORE update for event publishing
	previousStatus := bookingLog.Status

	// Validate state transition using the state machine
	// This handles all valid transitions including POSTED -> REVERSED for reversals
	if !isValidBookingLogTransition(bookingLog.Status, newStatus) {
		return nil, status.Errorf(codes.FailedPrecondition,
			"invalid status transition from %s to %s", bookingLog.Status, newStatus)
	}

	// Enforce double-entry bookkeeping constraint when transitioning to POSTED
	if newStatus == domain.TransactionStatusPosted {
		validationStart := time.Now()
		postings, err := s.repository.GetPostingsByBookingLogID(ctx, bookingLogID)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to retrieve postings for balance validation: %v", err)
		}

		// Empty postings are not allowed for POSTED status
		if len(postings) == 0 {
			observability.RecordBalanceValidationDuration(time.Since(validationStart))
			observability.RecordDoubleEntryValidation(observability.ValidationResultUnbalanced, observability.CurrencyUnknown)
			observability.LogBalanceValidationFailure(
				bookingLogID.String(),
				observability.CurrencyUnknown,
				"0",
				"0",
				"0",
			)
			return nil, status.Error(codes.FailedPrecondition,
				"cannot post booking log with no postings")
		}

		// Calculate debit and credit totals
		debitTotal := decimal.Zero
		creditTotal := decimal.Zero
		var currency string
		for _, posting := range postings {
			// Capture currency from first posting
			if currency == "" {
				currency = posting.Amount.Instrument.Code
			}
			switch posting.Direction {
			case domain.PostingDirectionDebit:
				debitTotal = debitTotal.Add(posting.Amount.Amount)
			case domain.PostingDirectionCredit:
				creditTotal = creditTotal.Add(posting.Amount.Amount)
			}
		}

		observability.RecordBalanceValidationDuration(time.Since(validationStart))

		// Validate double-entry balance
		if !debitTotal.Equal(creditTotal) {
			imbalance := debitTotal.Sub(creditTotal)
			observability.RecordDoubleEntryValidation(observability.ValidationResultUnbalanced, currency)
			observability.LogBalanceValidationFailure(
				bookingLogID.String(),
				currency,
				debitTotal.String(),
				creditTotal.String(),
				imbalance.String(),
			)
			return nil, status.Error(codes.FailedPrecondition,
				fmt.Sprintf("cannot post unbalanced booking log: debits=%s credits=%s imbalance=%s",
					debitTotal.String(), creditTotal.String(), imbalance.String()))
		}

		// Record successful balance validation
		observability.RecordDoubleEntryValidation(observability.ValidationResultBalanced, currency)
	}

	// Apply status update
	updated := bookingLog.WithStatus(newStatus)

	// Apply chart of accounts rules update if provided
	if req.ChartOfAccountsRules != "" {
		updated = updated.WithChartOfAccountsRules(req.ChartOfAccountsRules)
	}

	// Persist updated booking log
	if err := s.repository.UpdateBookingLog(ctx, &updated); err != nil {
		if errors.Is(err, persistence.ErrBookingLogNotFound) {
			return nil, status.Errorf(codes.NotFound, "financial booking log not found: %s", bookingLogID)
		}
		return nil, status.Errorf(codes.Internal, "failed to update booking log: %v", err)
	}

	// Publish FinancialBookingLogUpdatedEvent for inter-service coordination
	// Event publishing is best-effort - errors are logged but don't fail the operation
	correlationID := ""
	if req.IdempotencyKey != nil {
		correlationID = req.IdempotencyKey.Key
	}
	event := &eventsv1.FinancialBookingLogUpdatedEvent{
		BookingLogId:         bookingLogID.String(),
		Status:               toProtoTransactionStatus(newStatus),
		PreviousStatus:       toProtoTransactionStatus(previousStatus),
		ChartOfAccountsRules: updated.ChartOfAccountsRules,
		Reason:               fmt.Sprintf("Status updated from %s to %s", previousStatus, newStatus),
		UpdatedBy:            extractUserFromContext(ctx),
		CorrelationId:        correlationID,
		CausationId:          correlationID, // Request caused this event
		Timestamp:            timestamppb.Now(),
		Version:              1, // Version tracking would need to be added to domain model
	}

	if err := s.eventPublisher.Publish(ctx, event); err != nil {
		slog.Error("failed to publish FinancialBookingLogUpdatedEvent",
			"error", err,
			"booking_log_id", bookingLogID.String(),
			"previous_status", previousStatus,
			"new_status", newStatus)
	}

	// Convert to proto response
	return &financialaccountingv1.UpdateFinancialBookingLogResponse{
		FinancialBookingLog: toProtoFinancialBookingLog(&updated),
	}, nil
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
