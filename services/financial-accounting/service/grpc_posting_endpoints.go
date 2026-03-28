package service

import (
	"context"
	"errors"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
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

		cachedResp, err := s.checkCapturePostingIdempotency(ctx, idempotencyKey, req.IdempotencyKey.Key)
		if cachedResp != nil || err != nil {
			return cachedResp, err
		}
	}

	// Build and persist the posting
	posting, correlationID, err := s.buildAndPersistPosting(ctx, req)
	if err != nil {
		return nil, err
	}

	// Publish LedgerPostingCapturedEvent (best-effort)
	event := buildPostingCapturedEvent(posting, correlationID)
	if err := s.eventPublisher.Publish(ctx, event); err != nil {
		slog.Error("failed to publish LedgerPostingCapturedEvent",
			"error", err,
			"posting_id", posting.ID.String(),
			"booking_log_id", posting.FinancialBookingLogID.String())
	}

	response := &financialaccountingv1.CaptureLedgerPostingResponse{
		LedgerPosting: toProtoLedgerPosting(posting),
	}

	// Store result for idempotency (only if service configured and key provided)
	if s.idempotency != nil && req.IdempotencyKey != nil && req.IdempotencyKey.Key != "" {
		ttl := idempotencyTTLFromKey(req.IdempotencyKey.TtlSeconds)
		s.storeIdempotencyResult(ctx, idempotencyKey, ttl, response, "capture-posting")
	}

	return response, nil
}

// checkCapturePostingIdempotency checks if a capture posting request was already processed.
// Returns (cached response, nil) if found, (nil, nil) to proceed, or (nil, error) on failure.
func (s *FinancialAccountingService) checkCapturePostingIdempotency(
	ctx context.Context,
	key idempotency.Key,
	idempotencyKeyStr string,
) (*financialaccountingv1.CaptureLedgerPostingResponse, error) {
	result, err := s.idempotency.Check(ctx, key)
	if err != nil && !errors.Is(err, idempotency.ErrResultNotFound) {
		if errors.Is(err, idempotency.ErrOperationAlreadyProcessed) {
			if result != nil && result.Status == idempotency.StatusCompleted && len(result.Data) > 0 {
				var cachedResponse financialaccountingv1.CaptureLedgerPostingResponse
				if unmarshalErr := proto.Unmarshal(result.Data, &cachedResponse); unmarshalErr != nil {
					slog.Error("failed to deserialize cached idempotency response",
						"error", unmarshalErr,
						"idempotency_key", idempotencyKeyStr,
						"operation", "capture-posting")
					return nil, status.Error(codes.AlreadyExists, "request with this idempotency key already processed")
				}
				return &cachedResponse, nil
			}
			return nil, status.Error(codes.AlreadyExists, "request with this idempotency key already processed")
		}
		return nil, status.Errorf(codes.Internal, "failed to check idempotency: %v", err)
	}

	if err := s.idempotency.MarkPending(ctx, key, defaultIdempotencyTTL); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to mark operation as pending: %v", err)
	}
	return nil, nil
}

// buildAndPersistPosting validates, creates, and persists a new ledger posting.
func (s *FinancialAccountingService) buildAndPersistPosting(
	ctx context.Context,
	req *financialaccountingv1.CaptureLedgerPostingRequest,
) (*domain.LedgerPosting, string, error) {
	bookingLogID, err := parseUUID(req.GetFinancialBookingLogId())
	if err != nil {
		return nil, "", status.Errorf(codes.InvalidArgument, "invalid financial_booking_log_id: %v", err)
	}

	validated, err := validateCapturePostingRequest(req)
	if err != nil {
		return nil, "", err
	}

	correlationID := ""
	if req.IdempotencyKey != nil {
		correlationID = req.IdempotencyKey.Key
	}

	posting, err := domain.NewLedgerPosting(
		bookingLogID,
		validated.Direction,
		validated.PostingAmount,
		validated.AccountID,
		validated.ValueDate,
		correlationID,
	)
	if err != nil {
		return nil, "", status.Errorf(codes.InvalidArgument, "invalid posting data: %v", err)
	}

	posting.AccountServiceDomain = fromProtoAccountServiceDomain(validated.AccountServiceDomain)

	if err := s.repository.SavePosting(ctx, posting); err != nil {
		return nil, "", status.Errorf(codes.Internal, "failed to save posting: %v", err)
	}

	return posting, correlationID, nil
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

	ttl := idempotencyTTLFromKey(req.IdempotencyKey.TtlSeconds)

	// Use idempotency executor to wrap business logic with atomic PENDING cleanup.
	var response *financialaccountingv1.UpdateLedgerPostingResponse

	execResult, err := s.idempotencyExecutor.Execute(ctx, idempotencyKey, ttl, func(ctx context.Context) ([]byte, error) {
		resp, execErr := s.executeUpdateLedgerPosting(ctx, req)
		if execErr != nil {
			return nil, execErr
		}
		response = resp
		return marshalForCache(resp, req.IdempotencyKey.Key, "update-posting"), nil
	})
	if err != nil {
		return nil, mapIdempotencyExecutorError(err)
	}

	if execResult.FromCache {
		return handleCachedUpdatePostingResponse(execResult.Data, req.IdempotencyKey.Key, req.GetId())
	}

	return response, nil
}

// executeUpdateLedgerPosting contains the core business logic for UpdateLedgerPosting.
// This is separated from the main method to allow the idempotency executor to wrap it.
func (s *FinancialAccountingService) executeUpdateLedgerPosting(
	ctx context.Context,
	req *financialaccountingv1.UpdateLedgerPostingRequest,
) (*financialaccountingv1.UpdateLedgerPostingResponse, error) {
	postingID, err := parseUUID(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid id: %v", err)
	}

	if req.Status == commonv1.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED {
		return nil, status.Error(codes.InvalidArgument, "status must be specified")
	}
	newStatus := fromProtoTransactionStatus(req.Status)

	correlationID := ""
	if req.IdempotencyKey != nil {
		correlationID = req.IdempotencyKey.Key
	}

	// Retrieve, apply transition, persist, and publish
	posting, err := s.applyAndPersistPostingUpdate(ctx, postingID, newStatus, req.PostingResult, correlationID)
	if err != nil {
		return nil, err
	}

	return &financialaccountingv1.UpdateLedgerPostingResponse{
		LedgerPosting: toProtoLedgerPosting(posting),
	}, nil
}

// applyAndPersistPostingUpdate retrieves a posting, applies the status transition,
// persists the update, and publishes the amended event.
func (s *FinancialAccountingService) applyAndPersistPostingUpdate(
	ctx context.Context,
	postingID [16]byte,
	newStatus domain.TransactionStatus,
	postingResult, correlationID string,
) (*domain.LedgerPosting, error) {
	posting, err := s.repository.GetPosting(ctx, postingID)
	if err != nil {
		if errors.Is(err, persistence.ErrPostingNotFound) {
			return nil, status.Errorf(codes.NotFound, "ledger posting not found: %s", postingID)
		}
		return nil, status.Errorf(codes.Internal, "failed to retrieve posting: %v", err)
	}

	previousAmount := posting.Amount
	previousStatus := posting.Status

	if postingResult == "" {
		postingResult = posting.PostingResult
	}
	if err := applyPostingStatusTransition(posting, newStatus, postingResult); err != nil {
		return nil, err
	}

	if err := s.repository.UpdatePosting(ctx, posting); err != nil {
		if errors.Is(err, persistence.ErrPostingNotFound) {
			return nil, status.Errorf(codes.NotFound, "ledger posting not found: %s", postingID)
		}
		return nil, status.Errorf(codes.Internal, "failed to update posting: %v", err)
	}

	// Publish LedgerPostingAmendedEvent (best-effort)
	event := buildPostingAmendedEvent(posting, previousAmount, previousStatus, newStatus, correlationID)
	if err := s.eventPublisher.Publish(ctx, event); err != nil {
		slog.Error("failed to publish LedgerPostingAmendedEvent",
			"error", err,
			"posting_id", posting.ID.String(),
			"booking_log_id", posting.FinancialBookingLogID.String(),
			"status", newStatus)
	}

	return posting, nil
}
