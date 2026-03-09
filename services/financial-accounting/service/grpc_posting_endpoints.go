package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	"github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
	"github.com/meridianhub/meridian/services/financial-accounting/domain"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
)

// validatePostingPair performs comprehensive validation on a debit/credit posting pair.
// This includes:
// 1. Double-entry validation (instrument matching, direction validation)
// 2. Fungibility validation (attribute compatibility for non-fungible instruments)
//
// The fungibility validation is only performed if:
// - The registry client is configured
// - The instrument has a fungibility_key_expression defined
//
// Error behavior (fail-closed):
// - If the registry is unavailable, the transaction is rejected
// - If CEL evaluation fails, the transaction is rejected
// - This ensures data integrity is never compromised by infrastructure issues
//
// Returns nil if validation passes, or a gRPC status error if validation fails.
func (s *FinancialAccountingService) validatePostingPair(
	ctx context.Context,
	debit, credit *domain.LedgerPosting,
) error {
	// Step 1: Perform double-entry validation (instrument match, directions)
	if err := domain.ValidateDoubleEntryPair(debit, credit); err != nil {
		if errors.Is(err, domain.ErrDoubleEntryInstrumentMismatch) {
			return status.Errorf(codes.InvalidArgument, "double-entry validation failed: %v", err)
		}
		if errors.Is(err, domain.ErrNilPosting) {
			return status.Error(codes.InvalidArgument, "posting cannot be nil")
		}
		if errors.Is(err, domain.ErrInvalidDebitDirection) || errors.Is(err, domain.ErrInvalidCreditDirection) {
			return status.Errorf(codes.InvalidArgument, "invalid posting direction: %v", err)
		}
		return status.Errorf(codes.InvalidArgument, "validation failed: %v", err)
	}

	// Step 2: Perform fungibility validation if registry is configured
	if s.registry != nil {
		instrument := debit.Amount.Instrument
		// Cast uint32 version to int for registry API compatibility
		instrumentDef, err := s.registry.GetInstrument(ctx, instrument.Code, int(instrument.Version))
		if err != nil {
			// Fail-closed: reject transaction if registry is unavailable
			slog.Error("failed to fetch instrument for fungibility validation",
				"error", err,
				"instrument_code", instrument.Code,
				"instrument_version", instrument.Version)
			return status.Errorf(codes.Unavailable, "%v: cannot validate fungibility", ErrRegistryUnavailable)
		}

		// Get the pre-compiled CEL program for fungibility key evaluation
		program := instrumentDef.GetFungibilityKeyProgram()

		// Perform fungibility validation
		if err := domain.ValidateFungibility(program, debit.Attributes, credit.Attributes); err != nil {
			if errors.Is(err, domain.ErrFungibilityMismatch) {
				slog.Warn("fungibility validation failed",
					"instrument_code", instrument.Code,
					"debit_attributes", debit.Attributes,
					"credit_attributes", credit.Attributes,
					"error", err)
				return status.Errorf(codes.InvalidArgument, "fungibility validation failed: %v", err)
			}
			if errors.Is(err, domain.ErrFungibilityKeyEvaluation) {
				// CEL evaluation error - fail-closed
				slog.Error("CEL evaluation error during fungibility validation",
					"error", err,
					"instrument_code", instrument.Code)
				return status.Errorf(codes.Internal, "fungibility key evaluation failed: %v", err)
			}
			return status.Errorf(codes.Internal, "fungibility validation error: %v", err)
		}
	}

	return nil
}

// CaptureLedgerPosting creates a new ledger posting with validation and event publishing.
//
// Workflow:
// 1. Check idempotency using request's IdempotencyKey
// 2. Validate that the financial booking log exists
// 3. Parse and validate all request fields
// 4. Create domain entity with business logic validation
// 5. Persist posting in transaction
// 6. Publish domain event (LedgerPostingCapturedEvent)
// 7. Return gRPC response with created posting
//
// Error mapping:
// - Invalid request fields -> codes.InvalidArgument
// - Duplicate idempotency key -> codes.AlreadyExists
// - Booking log not found -> codes.NotFound
// - Internal errors -> codes.Internal
func (s *FinancialAccountingService) CaptureLedgerPosting(
	ctx context.Context,
	req *financialaccountingv1.CaptureLedgerPostingRequest,
) (*financialaccountingv1.CaptureLedgerPostingResponse, error) {
	// Check idempotency (only if service is configured and key is provided)
	var idempotencyKey idempotency.Key
	if s.idempotency != nil && req.IdempotencyKey != nil && req.IdempotencyKey.Key != "" {
		idempotencyKey = idempotency.Key{
			Namespace: "financial-accounting",
			Operation: "capture-posting",
			EntityID:  req.GetFinancialBookingLogId(),
			RequestID: req.IdempotencyKey.Key,
		}

		result, err := s.idempotency.Check(ctx, idempotencyKey)
		if err != nil && !errors.Is(err, idempotency.ErrResultNotFound) {
			if errors.Is(err, idempotency.ErrOperationAlreadyProcessed) {
				if result != nil && result.Status == idempotency.StatusCompleted && len(result.Data) > 0 {
					// Deserialize cached response from protobuf
					var cachedResponse financialaccountingv1.CaptureLedgerPostingResponse
					if unmarshalErr := proto.Unmarshal(result.Data, &cachedResponse); unmarshalErr != nil {
						// Log deserialization error but fall back to generic AlreadyExists response
						slog.Error("failed to deserialize cached idempotency response",
							"error", unmarshalErr,
							"idempotency_key", req.IdempotencyKey.Key,
							"operation", "capture-posting")
						return nil, status.Error(codes.AlreadyExists, "request with this idempotency key already processed")
					}
					// Return cached response for idempotent behavior
					return &cachedResponse, nil
				}
				// No cached data available - return generic AlreadyExists
				return nil, status.Error(codes.AlreadyExists, "request with this idempotency key already processed")
			}
			return nil, status.Errorf(codes.Internal, "failed to check idempotency: %v", err)
		}

		// Mark as pending to prevent concurrent processing
		if err := s.idempotency.MarkPending(ctx, idempotencyKey, defaultIdempotencyTTL); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to mark operation as pending: %v", err)
		}
	}

	// Parse booking log ID
	bookingLogID, err := parseUUID(req.GetFinancialBookingLogId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid financial_booking_log_id: %v", err)
	}

	// Validate booking log exists (optional check - could be deferred to database constraint)
	// For now we'll trust the database foreign key constraint

	// Parse and validate posting amount
	postingAmount, err := fromProtoMoney(req.GetPostingAmount())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid posting_amount: %v", err)
	}

	// Validate posting direction
	if req.PostingDirection == commonv1.PostingDirection_POSTING_DIRECTION_UNSPECIFIED {
		return nil, status.Error(codes.InvalidArgument, "posting_direction must be specified")
	}
	direction := fromProtoPostingDirection(req.PostingDirection)

	// Validate account ID
	if req.AccountId == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}

	// Validate value date
	if req.ValueDate == nil {
		return nil, status.Error(codes.InvalidArgument, "value_date is required")
	}
	valueDate := req.ValueDate.AsTime()

	// Extract correlation ID from idempotency key (or use empty string)
	correlationID := ""
	if req.IdempotencyKey != nil {
		correlationID = req.IdempotencyKey.Key
	}

	// Create domain entity with validation
	posting, err := domain.NewLedgerPosting(
		bookingLogID,
		direction,
		postingAmount,
		req.AccountId,
		valueDate,
		correlationID,
	)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid posting data: %v", err)
	}

	// Set account service domain from request (caller-provided, e.g., from saga scripts)
	posting.AccountServiceDomain = fromProtoAccountServiceDomain(req.AccountServiceDomain)

	// Persist posting
	if err := s.repository.SavePosting(ctx, posting); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to save posting: %v", err)
	}

	// Publish LedgerPostingCapturedEvent for inter-service coordination
	// Event publishing is best-effort - errors are logged but don't fail the operation
	event := &eventsv1.LedgerPostingCapturedEvent{
		PostingId:        posting.ID.String(),
		BookingLogId:     posting.FinancialBookingLogID.String(),
		PostingDirection: toProtoPostingDirection(posting.Direction),
		PostingAmount:    toProtoMoney(posting.Amount),
		AccountId:        posting.AccountID,
		ValueDate:        timestamppb.New(posting.ValueDate),
		Status:           toProtoTransactionStatus(posting.Status),
		CorrelationId:    correlationID,
		CausationId:      correlationID, // Request caused this event
		Timestamp:        timestamppb.Now(),
		Version:          1, // Initial version for newly created posting
	}
	if err := s.eventPublisher.Publish(ctx, event); err != nil {
		slog.Error("failed to publish LedgerPostingCapturedEvent",
			"error", err,
			"posting_id", posting.ID.String(),
			"booking_log_id", posting.FinancialBookingLogID.String())
	}

	// Convert to proto response
	response := &financialaccountingv1.CaptureLedgerPostingResponse{
		LedgerPosting: toProtoLedgerPosting(posting),
	}

	// Store result for idempotency (only if service configured and key provided)
	if s.idempotency != nil && req.IdempotencyKey != nil && req.IdempotencyKey.Key != "" {
		ttl := defaultIdempotencyTTL
		if req.IdempotencyKey.TtlSeconds > 0 {
			ttl = time.Duration(req.IdempotencyKey.TtlSeconds) * time.Second
		}

		// Serialize response using protobuf for idempotent storage
		responseData, marshalErr := proto.Marshal(response)
		if marshalErr != nil {
			// Log serialization error but don't fail the operation - response was successful
			slog.Error("failed to serialize response for idempotency cache",
				"error", marshalErr,
				"idempotency_key", req.IdempotencyKey.Key,
				"operation", "capture-posting")
		} else {
			result := idempotency.Result{
				Key:         idempotencyKey,
				Status:      idempotency.StatusCompleted,
				Data:        responseData,
				CompletedAt: time.Now(),
				TTL:         ttl,
			}

			// Store result in idempotency cache (best-effort, failures are logged but don't fail request)
			if storeErr := s.idempotency.StoreResult(ctx, result); storeErr != nil {
				slog.Error("failed to store idempotency result",
					"error", storeErr,
					"idempotency_key", req.IdempotencyKey.Key,
					"operation", "capture-posting")
			}
		}
	}

	return response, nil
}

// RetrieveLedgerPosting retrieves a specific ledger posting by ID.
//
// This method implements subtask 9.3 - simple retrieve operation.
//
// gRPC Error Codes:
//   - codes.InvalidArgument: Invalid posting ID format
//   - codes.NotFound: Posting does not exist
//   - codes.Internal: Database or system errors
//
// Example:
//
//	req := &financialaccountingv1.RetrieveLedgerPostingRequest{
//	    Id: "550e8400-e29b-41d4-a716-446655440000",
//	}
//	resp, err := service.RetrieveLedgerPosting(ctx, req)
func (s *FinancialAccountingService) RetrieveLedgerPosting(
	ctx context.Context,
	req *financialaccountingv1.RetrieveLedgerPostingRequest,
) (*financialaccountingv1.RetrieveLedgerPostingResponse, error) {
	// Parse and validate posting ID
	postingID, err := parseUUID(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid posting id: %v", err)
	}

	// Retrieve from repository
	posting, err := s.repository.GetPosting(ctx, postingID)
	if err != nil {
		if errors.Is(err, persistence.ErrPostingNotFound) {
			return nil, status.Errorf(codes.NotFound, "ledger posting not found: %s", postingID)
		}
		// Don't expose internal errors to clients (security best practice)
		return nil, status.Error(codes.Internal, "failed to retrieve ledger posting")
	}

	// Convert to protobuf and return
	return &financialaccountingv1.RetrieveLedgerPostingResponse{
		LedgerPosting: toProtoLedgerPosting(posting),
	}, nil
}

// UpdateLedgerPosting updates an existing ledger posting's status and result.
//
// Workflow:
// 1. Check idempotency using request's IdempotencyKey
// 2. Parse and validate request fields
// 3. Retrieve existing posting by ID
// 4. Validate state transition rules (e.g., cannot change POSTED status)
// 5. Apply update using domain methods (Post/Fail)
// 6. Persist updated posting
// 7. Publish domain event (LedgerPostingUpdatedEvent)
// 8. Return updated posting
//
// Idempotency Note:
// Unlike CaptureLedgerPosting where idempotency is optional (create operations
// naturally fail on duplicate IDs), update operations REQUIRE idempotency keys
// because state-machine transitions must be exactly-once. A duplicate update
// could incorrectly transition an entity through multiple states.
//
// Error mapping:
// - Invalid request fields -> codes.InvalidArgument
// - Duplicate idempotency key -> codes.AlreadyExists
// - Posting not found -> codes.NotFound
// - Invalid state transition -> codes.FailedPrecondition
// - Internal errors -> codes.Internal
func (s *FinancialAccountingService) UpdateLedgerPosting(
	ctx context.Context,
	req *financialaccountingv1.UpdateLedgerPostingRequest,
) (*financialaccountingv1.UpdateLedgerPostingResponse, error) {
	// Validate idempotency key is provided
	if req.IdempotencyKey == nil || req.IdempotencyKey.Key == "" {
		return nil, status.Error(codes.InvalidArgument, "idempotency_key is required")
	}

	idempotencyKey := idempotency.Key{
		Namespace: "financial-accounting",
		Operation: "update-posting",
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
	var response *financialaccountingv1.UpdateLedgerPostingResponse

	execResult, err := s.idempotencyExecutor.Execute(ctx, idempotencyKey, ttl, func(ctx context.Context) ([]byte, error) {
		// Execute business logic
		resp, execErr := s.executeUpdateLedgerPosting(ctx, req)
		if execErr != nil {
			return nil, execErr
		}

		// Serialize response for idempotency cache
		responseData, marshalErr := proto.Marshal(resp)
		if marshalErr != nil {
			slog.Error("failed to serialize response for idempotency cache",
				"error", marshalErr,
				"idempotency_key", req.IdempotencyKey.Key,
				"operation", "update-posting")
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
			var cachedResponse financialaccountingv1.UpdateLedgerPostingResponse
			if unmarshalErr := proto.Unmarshal(execResult.Data, &cachedResponse); unmarshalErr != nil {
				slog.Error("failed to deserialize cached idempotency response",
					"error", unmarshalErr,
					"idempotency_key", req.IdempotencyKey.Key,
					"operation", "update-posting")
				return nil, status.Error(codes.AlreadyExists, "request with this idempotency key already processed")
			}
			slog.Info("returning cached idempotent response",
				"idempotency_key", req.IdempotencyKey.Key,
				"operation", "update-posting",
				"posting_id", req.GetId())
			return &cachedResponse, nil
		}
		return nil, status.Error(codes.AlreadyExists, "request with this idempotency key already processed")
	}

	return response, nil
}

// executeUpdateLedgerPosting contains the core business logic for UpdateLedgerPosting.
// This is separated from the main method to allow the idempotency executor to wrap it.
func (s *FinancialAccountingService) executeUpdateLedgerPosting(
	ctx context.Context,
	req *financialaccountingv1.UpdateLedgerPostingRequest,
) (*financialaccountingv1.UpdateLedgerPostingResponse, error) {
	// Parse posting ID
	postingID, err := parseUUID(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid id: %v", err)
	}

	// Validate status
	if req.Status == commonv1.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED {
		return nil, status.Error(codes.InvalidArgument, "status must be specified")
	}
	newStatus := fromProtoTransactionStatus(req.Status)

	// Extract correlation ID from idempotency key
	correlationID := ""
	if req.IdempotencyKey != nil {
		correlationID = req.IdempotencyKey.Key
	}

	// Retrieve existing posting
	posting, err := s.repository.GetPosting(ctx, postingID)
	if err != nil {
		if errors.Is(err, persistence.ErrPostingNotFound) {
			return nil, status.Errorf(codes.NotFound, "ledger posting not found: %s", postingID)
		}
		return nil, status.Errorf(codes.Internal, "failed to retrieve posting: %v", err)
	}

	// Capture previous state BEFORE any modifications (for LedgerPostingAmendedEvent)
	previousAmount := posting.Amount
	previousStatus := posting.Status

	// Validate and apply state transition using domain methods
	postingResult := req.PostingResult
	if postingResult == "" {
		postingResult = posting.PostingResult // Preserve existing if not provided
	}

	switch newStatus {
	case domain.TransactionStatusPosted:
		if err := posting.Post(postingResult); err != nil {
			if errors.Is(err, domain.ErrAlreadyPosted) {
				return nil, status.Error(codes.FailedPrecondition, "posting already posted")
			}
			return nil, status.Errorf(codes.InvalidArgument, "cannot post: %v", err)
		}
	case domain.TransactionStatusFailed:
		if err := posting.Fail(postingResult); err != nil {
			if errors.Is(err, domain.ErrCannotFailPosted) {
				return nil, status.Error(codes.FailedPrecondition, "cannot fail a posted transaction")
			}
			return nil, status.Errorf(codes.InvalidArgument, "cannot fail: %v", err)
		}
	case domain.TransactionStatusPending:
		// Allow transition back to pending (for retry scenarios)
		posting.Status = newStatus
		if postingResult != "" {
			posting.PostingResult = postingResult
		}
	case domain.TransactionStatusCancelled:
		// Allow cancellation
		posting.Status = newStatus
		if postingResult != "" {
			posting.PostingResult = postingResult
		}
	case domain.TransactionStatusReversed:
		// Allow reversal
		posting.Status = newStatus
		if postingResult != "" {
			posting.PostingResult = postingResult
		}
	default:
		return nil, status.Errorf(codes.InvalidArgument, "unsupported status: %v", newStatus)
	}

	// Persist updated posting
	if err := s.repository.UpdatePosting(ctx, posting); err != nil {
		if errors.Is(err, persistence.ErrPostingNotFound) {
			return nil, status.Errorf(codes.NotFound, "ledger posting not found: %s", postingID)
		}
		return nil, status.Errorf(codes.Internal, "failed to update posting: %v", err)
	}

	// Publish LedgerPostingAmendedEvent for inter-service coordination
	// Event publishing is best-effort - errors are logged but don't fail the operation
	// Note: UpdateLedgerPosting changes status, not amount. Both previous_amount and new_amount
	// will be the same value since amount doesn't change in status transitions.
	event := &eventsv1.LedgerPostingAmendedEvent{
		PostingId:      posting.ID.String(),
		BookingLogId:   posting.FinancialBookingLogID.String(),
		PreviousAmount: toProtoMoney(previousAmount),
		NewAmount:      toProtoMoney(posting.Amount),
		Reason:         fmt.Sprintf("Status updated from %v to %v", previousStatus, newStatus),
		AmendedBy:      "system", // Status transitions are system-driven
		CorrelationId:  correlationID,
		CausationId:    correlationID, // Request caused this event
		Timestamp:      timestamppb.Now(),
		Version:        1, // Increment version for optimistic locking
	}
	if err := s.eventPublisher.Publish(ctx, event); err != nil {
		slog.Error("failed to publish LedgerPostingAmendedEvent",
			"error", err,
			"posting_id", posting.ID.String(),
			"booking_log_id", posting.FinancialBookingLogID.String(),
			"status", newStatus)
	}

	// Convert to proto response
	return &financialaccountingv1.UpdateLedgerPostingResponse{
		LedgerPosting: toProtoLedgerPosting(posting),
	}, nil
}
