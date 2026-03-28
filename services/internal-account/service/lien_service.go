package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"github.com/meridianhub/meridian/services/internal-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/internal-account/domain"
	ibaobservability "github.com/meridianhub/meridian/services/internal-account/observability"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Lien-specific operation status constants for metrics.
const (
	opStatusLienRepoNil         = "lien_repo_nil"
	opStatusLienNotFound        = "lien_not_found"
	opStatusLienAlreadyExists   = "lien_already_exists"
	opStatusLienExpired         = "lien_expired"
	opStatusLienNotActive       = "lien_not_active"
	opStatusLienVersionConflict = "lien_version_conflict"
	opStatusLienCreateFailed    = "lien_create_failed"
	opStatusLienUpdateFailed    = "lien_update_failed"
	opStatusIdempotent          = "idempotent"
)

// Redis idempotency constants for internal-account lien operations.
const (
	// idempotencyNamespace is the Redis key namespace for internal-account idempotency.
	idempotencyNamespace = "internal-account"

	// idempotencyPendingTTL is how long a pending idempotency record remains valid.
	idempotencyPendingTTL = 5 * time.Minute

	// idempotencyResultTTL is how long completed results are cached.
	idempotencyResultTTL = 24 * time.Hour
)

// InitiateLien creates a new fund reservation on an internal account.
// Supports multi-asset input with atomic valuation (price lock) via valuateInternal().
// CRITICAL: Uses the same valuateInternal() logic as EvaluateAssetValuation to prevent Ghost Pricing.
func (s *Service) InitiateLien(ctx context.Context, req *pb.InitiateLienRequest) (*pb.InitiateLienResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		ibaobservability.RecordOperationDuration("initiate_lien", operationStatus, time.Since(start))
	}()

	if s.lienRepo == nil {
		operationStatus = opStatusLienRepoNil
		return nil, status.Error(codes.FailedPrecondition, "lien operations not configured")
	}

	// Validate input fields and parse amount
	inputAmount, opStatus, err := validateInitiateLienInput(req)
	if err != nil {
		operationStatus = opStatus
		return nil, err
	}

	// Resolve account
	account, err := s.findAccountByID(ctx, req.AccountId)
	if err != nil {
		operationStatus = opStatusAccountNotFound
		return nil, err
	}

	// Check idempotency: if a lien already exists for this payment order reference, return it
	if resp, opStatus, err := s.checkInitiateLienIdempotency(ctx, req.PaymentOrderReference, account); err != nil {
		operationStatus = opStatus
		return nil, err
	} else if resp != nil {
		operationStatus = opStatus
		return resp, nil
	}

	// Determine knowledge_at for valuation
	knowledgeAt := time.Now()
	if req.KnowledgeAt != nil {
		knowledgeAt = req.KnowledgeAt.AsTime()
	}

	// Determine expires_at
	var expiresAt *time.Time
	if req.ExpiresAt != nil {
		t := req.ExpiresAt.AsTime()
		expiresAt = &t
	}

	// Create lien (same-instrument or cross-instrument with valuation)
	var lien *domain.Lien
	nativeInstrument := account.InstrumentCode()
	if req.Input.InstrumentCode == nativeInstrument {
		lien, opStatus, err = s.createSameInstrumentLien(ctx, account, inputAmount, nativeInstrument, req, expiresAt)
	} else {
		lien, opStatus, err = s.createCrossInstrumentLien(ctx, account, inputAmount, nativeInstrument, req, knowledgeAt, expiresAt)
	}
	if err != nil {
		operationStatus = opStatus
		return nil, err
	}

	// Persist the lien (with race condition handling for idempotency)
	if resp, opStatus, err := s.persistLienWithRaceHandling(ctx, lien, req.PaymentOrderReference); err != nil {
		operationStatus = opStatus
		return nil, err
	} else if resp != nil {
		return resp, nil
	}

	s.logger.Info("created lien",
		"lien_id", lien.ID,
		"account_id", req.AccountId,
		"amount_cents", lien.AmountCents,
		"instrument_code", lien.InstrumentCode,
		"has_valuation", lien.HasValuation())

	return s.buildInitiateLienResponse(ctx, lien)
}

// validateInitiateLienInput validates the request fields and parses the input amount.
func validateInitiateLienInput(req *pb.InitiateLienRequest) (decimal.Decimal, string, error) {
	if req.Input == nil || req.Input.Amount == "" {
		return decimal.Zero, opStatusInvalidRequest, status.Error(codes.InvalidArgument, "input amount is required")
	}
	if req.Input.InstrumentCode == "" {
		return decimal.Zero, opStatusInvalidRequest, status.Error(codes.InvalidArgument, "input instrument_code is required")
	}
	if strings.TrimSpace(req.PaymentOrderReference) == "" {
		return decimal.Zero, opStatusInvalidRequest, status.Error(codes.InvalidArgument, "payment_order_reference is required")
	}

	inputAmount, err := decimal.NewFromString(req.Input.Amount)
	if err != nil {
		return decimal.Zero, opStatusInvalidInputAmount, status.Errorf(codes.InvalidArgument, "invalid input amount: %v", err)
	}
	if !inputAmount.IsPositive() {
		return decimal.Zero, opStatusInputAmountNonPositive, status.Error(codes.InvalidArgument, "input amount must be positive")
	}

	return inputAmount, "", nil
}

// checkInitiateLienIdempotency checks if a lien already exists for the payment order reference.
func (s *Service) checkInitiateLienIdempotency(ctx context.Context, paymentOrderRef string, account domain.InternalAccount) (*pb.InitiateLienResponse, string, error) {
	existingLien, err := s.lienRepo.FindByPaymentOrderReference(ctx, paymentOrderRef)
	if err == nil {
		if existingLien.AccountID != account.ID() {
			return nil, opStatusInvalidRequest, status.Errorf(codes.InvalidArgument,
				"payment_order_reference already used for a different account")
		}
		resp, buildErr := s.buildInitiateLienResponse(ctx, existingLien)
		if buildErr != nil {
			return nil, operationStatusFailed, buildErr
		}
		return resp, opStatusLienAlreadyExists, nil
	}
	if !errors.Is(err, persistence.ErrLienNotFound) {
		return nil, operationStatusFailed, status.Errorf(codes.Internal, "failed to check lien idempotency: %v", err)
	}
	return nil, "", nil
}

// createSameInstrumentLien creates a lien when the input instrument matches the account's native instrument.
func (s *Service) createSameInstrumentLien(ctx context.Context, account domain.InternalAccount, inputAmount decimal.Decimal, nativeInstrument string, req *pb.InitiateLienRequest, expiresAt *time.Time) (*domain.Lien, string, error) {
	precision, precisionErr := s.getInstrumentPrecision(ctx, nativeInstrument)
	if precisionErr != nil {
		return nil, operationStatusFailed, precisionErr
	}
	if !inputAmount.Equal(inputAmount.Truncate(precision)) {
		return nil, opStatusInvalidInputAmount, status.Errorf(codes.InvalidArgument, "input amount has more than %d decimal places for instrument %s", precision, nativeInstrument)
	}
	amountCents := inputAmount.Shift(precision).IntPart()

	lien, err := domain.NewLien(account.ID(), amountCents, nativeInstrument, req.BucketId, req.PaymentOrderReference, expiresAt)
	if err != nil {
		return nil, opStatusInvalidRequest, status.Errorf(codes.InvalidArgument, "failed to create lien: %v", err)
	}
	return lien, "", nil
}

// createCrossInstrumentLien creates a lien with atomic valuation when the input instrument differs from the account's native instrument.
func (s *Service) createCrossInstrumentLien(ctx context.Context, account domain.InternalAccount, inputAmount decimal.Decimal, nativeInstrument string, req *pb.InitiateLienRequest, knowledgeAt time.Time, expiresAt *time.Time) (*domain.Lien, string, error) {
	result, err := s.valuateInternal(ctx, req.AccountId, inputAmount, req.Input.InstrumentCode, knowledgeAt)
	if err != nil {
		opStatus := mapValuationError(err)
		return nil, opStatus, mapValuationErrorToGRPC(err)
	}

	precision, precisionErr := s.getInstrumentPrecision(ctx, nativeInstrument)
	if precisionErr != nil {
		return nil, operationStatusFailed, precisionErr
	}
	if !result.OutputAmount.Equal(result.OutputAmount.Truncate(precision)) {
		return nil, opStatusValuationFailed, status.Errorf(codes.Internal, "valued amount has more than %d decimal places for instrument %s", precision, nativeInstrument)
	}
	amountCents := result.OutputAmount.Shift(precision).IntPart()

	reservedQuantity := &domain.InstrumentAmount{
		Amount:         inputAmount,
		InstrumentCode: req.Input.InstrumentCode,
	}
	valuedAmount := &domain.InstrumentAmount{
		Amount:         result.OutputAmount,
		InstrumentCode: result.OutputCode,
	}

	var analysisJSON json.RawMessage
	if result.Analysis != nil {
		data, marshalErr := json.Marshal(result.Analysis)
		if marshalErr != nil {
			s.logger.Warn("failed to marshal valuation analysis", "error", marshalErr)
		} else {
			analysisJSON = data
		}
	}

	lien, err := domain.NewValuedLien(
		account.ID(), amountCents, nativeInstrument, req.BucketId,
		req.PaymentOrderReference, expiresAt,
		reservedQuantity, valuedAmount, analysisJSON,
	)
	if err != nil {
		return nil, opStatusInvalidRequest, status.Errorf(codes.InvalidArgument, "failed to create valued lien: %v", err)
	}
	return lien, "", nil
}

// mapValuationError maps a valuation error to an operation status string.
func mapValuationError(err error) string {
	switch {
	case errors.Is(err, ErrValuationAccountNotFound):
		return opStatusAccountNotFound
	case errors.Is(err, ErrNoActiveValuationFeature):
		return opStatusNoValuationFeature
	case errors.Is(err, ErrValuationFeatureNotActive):
		return opStatusFeatureNotActive
	case errors.Is(err, ErrValuationRepoNotConfigured):
		return opStatusValuationFeatureRepoNil
	default:
		return opStatusValuationFailed
	}
}

// mapValuationErrorToGRPC maps a valuation error to a gRPC status error.
func mapValuationErrorToGRPC(err error) error {
	switch {
	case errors.Is(err, ErrValuationAccountNotFound):
		return status.Errorf(codes.NotFound, "%v", err)
	case errors.Is(err, ErrNoActiveValuationFeature),
		errors.Is(err, ErrValuationFeatureNotActive),
		errors.Is(err, ErrValuationRepoNotConfigured):
		return status.Errorf(codes.FailedPrecondition, "%v", err)
	case errors.Is(err, ErrValuationEngineFailed):
		return status.Errorf(codes.Internal, "%v", err)
	default:
		return status.Errorf(codes.Internal, "valuation failed: %v", err)
	}
}

// persistLienWithRaceHandling persists the lien, handling duplicate payment order reference race conditions.
func (s *Service) persistLienWithRaceHandling(ctx context.Context, lien *domain.Lien, paymentOrderRef string) (*pb.InitiateLienResponse, string, error) {
	if err := s.lienRepo.Create(ctx, lien); err != nil {
		if isDuplicatePaymentOrderRef(err) {
			// Race condition: another request created the lien between our check and create.
			existingLien, findErr := s.lienRepo.FindByPaymentOrderReference(ctx, paymentOrderRef)
			if findErr != nil {
				return nil, opStatusLienCreateFailed, status.Errorf(codes.Internal, "lien creation race condition: %v", err)
			}
			resp, buildErr := s.buildInitiateLienResponse(ctx, existingLien)
			if buildErr != nil {
				return nil, opStatusLienCreateFailed, buildErr
			}
			return resp, "", nil
		}
		return nil, opStatusLienCreateFailed, status.Errorf(codes.Internal, "failed to create lien: %v", err)
	}
	return nil, "", nil
}

// ExecuteLien converts a lien reservation to an actual debit.
// Transitions the lien from ACTIVE to EXECUTED (terminal state, idempotent).
func (s *Service) ExecuteLien(ctx context.Context, req *pb.ExecuteLienRequest) (*pb.ExecuteLienResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		ibaobservability.RecordOperationDuration("execute_lien", operationStatus, time.Since(start))
	}()

	lienID, err := uuid.Parse(req.LienId)
	if err != nil {
		operationStatus = opStatusInvalidRequest
		return nil, status.Errorf(codes.InvalidArgument, "invalid lien_id: %v", err)
	}

	// Redis idempotency guard: check/mark-pending/store-result cycle.
	// Placed before lienRepo nil check so cached responses are returned without DB access.
	var idempKey idempotency.Key
	var idempotencyKeyStr string
	var idempotencyLockAcquired bool
	if req.IdempotencyKey != nil && req.IdempotencyKey.Key != "" && s.idempotencyService != nil {
		idempotencyKeyStr = req.IdempotencyKey.Key
		tenantID, tenantOk := tenant.FromContext(ctx)
		if !tenantOk {
			s.logger.Warn("tenant context missing for idempotency key, using empty tenant")
		}
		idempKey = idempotency.Key{
			TenantID:  string(tenantID),
			Namespace: idempotencyNamespace,
			Operation: "execute_lien",
			EntityID:  req.LienId,
			RequestID: idempotencyKeyStr,
		}

		// Check Redis for a cached result from a prior successful execution.
		result, checkErr := s.idempotencyService.Check(ctx, idempKey)
		if errors.Is(checkErr, idempotency.ErrOperationAlreadyProcessed) && result != nil && result.Data != nil {
			var cachedResp pb.ExecuteLienResponse
			unmarshalErr := proto.Unmarshal(result.Data, &cachedResp)
			if unmarshalErr == nil {
				s.logger.Info("returning cached execute lien response",
					"lien_id", req.LienId,
					"idempotency_key", idempotencyKeyStr)
				operationStatus = opStatusIdempotent
				return &cachedResp, nil
			}
			s.logger.Warn("failed to unmarshal cached idempotency result", "error", unmarshalErr)
		} else if checkErr != nil && !errors.Is(checkErr, idempotency.ErrResultNotFound) {
			s.logger.Error("idempotency check failed", "error", checkErr)
			return nil, status.Error(codes.Internal, "failed to check idempotency")
		}

		// Mark operation as pending to block concurrent duplicate requests.
		if markErr := s.idempotencyService.MarkPending(ctx, idempKey, idempotencyPendingTTL); markErr != nil {
			if errors.Is(markErr, idempotency.ErrOperationAlreadyProcessed) {
				s.logger.Info("operation already in progress, please retry",
					"idempotency_key", idempotencyKeyStr)
				return nil, status.Error(codes.Aborted, "operation already in progress, please retry")
			}
			s.logger.Error("failed to mark operation pending", "error", markErr)
			return nil, status.Error(codes.Aborted, "failed to acquire idempotency lock, please retry")
		}
		idempotencyLockAcquired = true

		// On failure, clean up the pending key so retries are not blocked.
		defer func() {
			if idempotencyLockAcquired && operationStatus != operationStatusSuccess {
				if delErr := s.idempotencyService.Delete(ctx, idempKey); delErr != nil {
					s.logger.Warn("failed to clean up pending idempotency state",
						"error", delErr,
						"idempotency_key", idempotencyKeyStr)
				}
			}
		}()
	}

	if s.lienRepo == nil {
		operationStatus = opStatusLienRepoNil
		return nil, status.Error(codes.FailedPrecondition, "lien operations not configured")
	}

	// Read-only idempotency check: if already executed, return without lock
	lien, err := s.lienRepo.FindByID(ctx, lienID)
	if err != nil {
		if errors.Is(err, persistence.ErrLienNotFound) {
			operationStatus = opStatusLienNotFound
			return nil, status.Errorf(codes.NotFound, "lien not found: %s", req.LienId)
		}
		operationStatus = operationStatusFailed
		return nil, status.Errorf(codes.Internal, "failed to retrieve lien: %v", err)
	}

	if lien.Status == domain.LienStatusExecuted {
		// Idempotent: already executed — cache result and release pending lock.
		protoLien, protoErr := s.domainToProtoLien(ctx, lien)
		if protoErr != nil {
			operationStatus = operationStatusFailed
			return nil, protoErr
		}
		resp := &pb.ExecuteLienResponse{Lien: protoLien}
		s.storeIdempotencyResultOrCleanup(ctx, idempKey, resp, "execute_lien:pre-lock")
		return resp, nil
	}

	// Acquire pessimistic lock for mutation
	lien, err = s.lienRepo.FindByIDForUpdate(ctx, lienID)
	if err != nil {
		if errors.Is(err, persistence.ErrLienNotFound) {
			operationStatus = opStatusLienNotFound
			return nil, status.Errorf(codes.NotFound, "lien not found: %s", req.LienId)
		}
		operationStatus = operationStatusFailed
		return nil, status.Errorf(codes.Internal, "failed to lock lien: %v", err)
	}

	// Re-check after lock: another request may have executed between read and lock.
	if lien.Status == domain.LienStatusExecuted {
		protoLien, protoErr := s.domainToProtoLien(ctx, lien)
		if protoErr != nil {
			operationStatus = operationStatusFailed
			return nil, protoErr
		}
		resp := &pb.ExecuteLienResponse{Lien: protoLien}
		s.storeIdempotencyResultOrCleanup(ctx, idempKey, resp, "execute_lien:post-lock")
		return resp, nil
	}

	// Execute the domain transition
	if err := lien.Execute(); err != nil {
		if errors.Is(err, domain.ErrLienNotActive) {
			operationStatus = opStatusLienNotActive
			return nil, status.Errorf(codes.FailedPrecondition, "cannot execute lien: %v", err)
		}
		if errors.Is(err, domain.ErrLienExpired) {
			operationStatus = opStatusLienExpired
			return nil, status.Errorf(codes.FailedPrecondition, "lien has expired: %s", req.LienId)
		}
		operationStatus = operationStatusFailed
		return nil, status.Errorf(codes.Internal, "failed to execute lien: %v", err)
	}

	// Persist with optimistic locking
	if err := s.lienRepo.Update(ctx, lien); err != nil {
		if errors.Is(err, persistence.ErrLienVersionConflict) {
			operationStatus = opStatusLienVersionConflict
			return nil, status.Error(codes.Aborted, "concurrent modification detected, please retry")
		}
		operationStatus = opStatusLienUpdateFailed
		return nil, status.Errorf(codes.Internal, "failed to update lien: %v", err)
	}

	s.logger.Info("executed lien",
		"lien_id", lien.ID,
		"account_id", lien.AccountID)

	protoLien, protoErr := s.domainToProtoLien(ctx, lien)
	if protoErr != nil {
		operationStatus = operationStatusFailed
		return nil, protoErr
	}
	resp := &pb.ExecuteLienResponse{Lien: protoLien}

	// Store successful result in Redis for future idempotency checks.
	s.storeIdempotencyResultOrCleanup(ctx, idempKey, resp, "execute_lien")

	return resp, nil
}

// TerminateLien releases reserved funds without executing.
// Transitions the lien from ACTIVE to TERMINATED (terminal state, idempotent).
func (s *Service) TerminateLien(ctx context.Context, req *pb.TerminateLienRequest) (*pb.TerminateLienResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		ibaobservability.RecordOperationDuration("terminate_lien", operationStatus, time.Since(start))
	}()

	lienID, err := uuid.Parse(req.LienId)
	if err != nil {
		operationStatus = opStatusInvalidRequest
		return nil, status.Errorf(codes.InvalidArgument, "invalid lien_id: %v", err)
	}

	// Redis idempotency guard: check/mark-pending/store-result cycle.
	// Placed before lienRepo nil check so cached responses are returned without DB access.
	var idempKey idempotency.Key
	var idempotencyKeyStr string
	var idempotencyLockAcquired bool
	if req.IdempotencyKey != nil && req.IdempotencyKey.Key != "" && s.idempotencyService != nil {
		idempotencyKeyStr = req.IdempotencyKey.Key
		tenantID, tenantOk := tenant.FromContext(ctx)
		if !tenantOk {
			s.logger.Warn("tenant context missing for idempotency key, using empty tenant")
		}
		idempKey = idempotency.Key{
			TenantID:  string(tenantID),
			Namespace: idempotencyNamespace,
			Operation: "terminate_lien",
			EntityID:  req.LienId,
			RequestID: idempotencyKeyStr,
		}

		// Check Redis for a cached result from a prior successful termination.
		result, checkErr := s.idempotencyService.Check(ctx, idempKey)
		if errors.Is(checkErr, idempotency.ErrOperationAlreadyProcessed) && result != nil && result.Data != nil {
			var cachedResp pb.TerminateLienResponse
			unmarshalErr := proto.Unmarshal(result.Data, &cachedResp)
			if unmarshalErr == nil {
				s.logger.Info("returning cached terminate lien response",
					"lien_id", req.LienId,
					"idempotency_key", idempotencyKeyStr)
				operationStatus = opStatusIdempotent
				return &cachedResp, nil
			}
			s.logger.Warn("failed to unmarshal cached idempotency result", "error", unmarshalErr)
		} else if checkErr != nil && !errors.Is(checkErr, idempotency.ErrResultNotFound) {
			s.logger.Error("idempotency check failed", "error", checkErr)
			return nil, status.Error(codes.Internal, "failed to check idempotency")
		}

		// Mark operation as pending to block concurrent duplicate requests.
		if markErr := s.idempotencyService.MarkPending(ctx, idempKey, idempotencyPendingTTL); markErr != nil {
			if errors.Is(markErr, idempotency.ErrOperationAlreadyProcessed) {
				s.logger.Info("operation already in progress, please retry",
					"idempotency_key", idempotencyKeyStr)
				return nil, status.Error(codes.Aborted, "operation already in progress, please retry")
			}
			s.logger.Error("failed to mark operation pending", "error", markErr)
			return nil, status.Error(codes.Aborted, "failed to acquire idempotency lock, please retry")
		}
		idempotencyLockAcquired = true

		// On failure, clean up the pending key so retries are not blocked.
		defer func() {
			if idempotencyLockAcquired && operationStatus != operationStatusSuccess {
				if delErr := s.idempotencyService.Delete(ctx, idempKey); delErr != nil {
					s.logger.Warn("failed to clean up pending idempotency state",
						"error", delErr,
						"idempotency_key", idempotencyKeyStr)
				}
			}
		}()
	}

	if s.lienRepo == nil {
		operationStatus = opStatusLienRepoNil
		return nil, status.Error(codes.FailedPrecondition, "lien operations not configured")
	}

	// Read-only idempotency check: if already terminated, return without lock
	lien, err := s.lienRepo.FindByID(ctx, lienID)
	if err != nil {
		if errors.Is(err, persistence.ErrLienNotFound) {
			operationStatus = opStatusLienNotFound
			return nil, status.Errorf(codes.NotFound, "lien not found: %s", req.LienId)
		}
		operationStatus = operationStatusFailed
		return nil, status.Errorf(codes.Internal, "failed to retrieve lien: %v", err)
	}

	if lien.Status == domain.LienStatusTerminated {
		// Idempotent: already terminated — cache result and release pending lock.
		protoLien, protoErr := s.domainToProtoLien(ctx, lien)
		if protoErr != nil {
			operationStatus = operationStatusFailed
			return nil, protoErr
		}
		resp := &pb.TerminateLienResponse{Lien: protoLien}
		s.storeIdempotencyResultOrCleanup(ctx, idempKey, resp, "terminate_lien:pre-lock")
		return resp, nil
	}

	// Acquire pessimistic lock for mutation
	lien, err = s.lienRepo.FindByIDForUpdate(ctx, lienID)
	if err != nil {
		if errors.Is(err, persistence.ErrLienNotFound) {
			operationStatus = opStatusLienNotFound
			return nil, status.Errorf(codes.NotFound, "lien not found: %s", req.LienId)
		}
		operationStatus = operationStatusFailed
		return nil, status.Errorf(codes.Internal, "failed to lock lien: %v", err)
	}

	// Re-check after lock: another request may have terminated between read and lock.
	if lien.Status == domain.LienStatusTerminated {
		protoLien, protoErr := s.domainToProtoLien(ctx, lien)
		if protoErr != nil {
			operationStatus = operationStatusFailed
			return nil, protoErr
		}
		resp := &pb.TerminateLienResponse{Lien: protoLien}
		s.storeIdempotencyResultOrCleanup(ctx, idempKey, resp, "terminate_lien:post-lock")
		return resp, nil
	}

	// Terminate the domain transition
	if err := lien.Terminate(req.Reason); err != nil {
		if errors.Is(err, domain.ErrLienNotActive) {
			operationStatus = opStatusLienNotActive
			return nil, status.Errorf(codes.FailedPrecondition, "cannot terminate lien: %v", err)
		}
		operationStatus = operationStatusFailed
		return nil, status.Errorf(codes.Internal, "failed to terminate lien: %v", err)
	}

	// Persist with optimistic locking
	if err := s.lienRepo.Update(ctx, lien); err != nil {
		if errors.Is(err, persistence.ErrLienVersionConflict) {
			operationStatus = opStatusLienVersionConflict
			return nil, status.Error(codes.Aborted, "concurrent modification detected, please retry")
		}
		operationStatus = opStatusLienUpdateFailed
		return nil, status.Errorf(codes.Internal, "failed to update lien: %v", err)
	}

	s.logger.Info("terminated lien",
		"lien_id", lien.ID,
		"account_id", lien.AccountID,
		"reason", req.Reason)

	protoLien, protoErr := s.domainToProtoLien(ctx, lien)
	if protoErr != nil {
		operationStatus = operationStatusFailed
		return nil, protoErr
	}
	resp := &pb.TerminateLienResponse{Lien: protoLien}

	// Store successful result in Redis for future idempotency checks.
	s.storeIdempotencyResultOrCleanup(ctx, idempKey, resp, "terminate_lien")

	return resp, nil
}

// RetrieveLien fetches a lien by ID.
func (s *Service) RetrieveLien(ctx context.Context, req *pb.RetrieveLienRequest) (*pb.RetrieveLienResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		ibaobservability.RecordOperationDuration("retrieve_lien", operationStatus, time.Since(start))
	}()

	if s.lienRepo == nil {
		operationStatus = opStatusLienRepoNil
		return nil, status.Error(codes.FailedPrecondition, "lien operations not configured")
	}

	lienID, err := uuid.Parse(req.LienId)
	if err != nil {
		operationStatus = opStatusInvalidRequest
		return nil, status.Errorf(codes.InvalidArgument, "invalid lien_id: %v", err)
	}

	lien, err := s.lienRepo.FindByID(ctx, lienID)
	if err != nil {
		if errors.Is(err, persistence.ErrLienNotFound) {
			operationStatus = opStatusLienNotFound
			return nil, status.Errorf(codes.NotFound, "lien not found: %s", req.LienId)
		}
		operationStatus = operationStatusFailed
		return nil, status.Errorf(codes.Internal, "failed to retrieve lien: %v", err)
	}

	protoLien, protoErr := s.domainToProtoLien(ctx, lien)
	if protoErr != nil {
		operationStatus = operationStatusFailed
		return nil, protoErr
	}
	return &pb.RetrieveLienResponse{Lien: protoLien}, nil
}

// storeIdempotencyResultOrCleanup marshals resp, stores it in the idempotency
// service, and—if either step fails—deletes the pending marker so that
// subsequent retries are not blocked by a stale lock. It is a no-op when the
// idempotency service is not configured or no key is present.
func (s *Service) storeIdempotencyResultOrCleanup(ctx context.Context, idempKey idempotency.Key, resp proto.Message, logPrefix string) {
	if s.idempotencyService == nil || idempKey.RequestID == "" {
		return
	}
	responseData, marshalErr := proto.Marshal(resp)
	if marshalErr != nil {
		s.logger.Error(logPrefix+": failed to marshal response for idempotency cache", "error", marshalErr)
		if delErr := s.idempotencyService.Delete(ctx, idempKey); delErr != nil {
			s.logger.Warn(logPrefix+": failed to clear pending idempotency state after marshal error", "error", delErr)
		}
		return
	}
	if storeErr := s.idempotencyService.StoreResult(ctx, idempotency.Result{
		Key:         idempKey,
		Status:      idempotency.StatusCompleted,
		Data:        responseData,
		CompletedAt: time.Now(),
		TTL:         idempotencyResultTTL,
	}); storeErr != nil {
		s.logger.Error(logPrefix+": failed to store idempotency result", "error", storeErr)
		if delErr := s.idempotencyService.Delete(ctx, idempKey); delErr != nil {
			s.logger.Warn(logPrefix+": failed to clear pending idempotency state after cache error", "error", delErr)
		}
	}
}

// buildInitiateLienResponse constructs a consistent InitiateLienResponse
// including valuation fields when present.
func (s *Service) buildInitiateLienResponse(ctx context.Context, lien *domain.Lien) (*pb.InitiateLienResponse, error) {
	protoLien, err := s.domainToProtoLien(ctx, lien)
	if err != nil {
		return nil, err
	}
	resp := &pb.InitiateLienResponse{Lien: protoLien}
	if lien.HasValuation() {
		resp.ValuedAmount = &quantityv1.InstrumentAmount{
			Amount:         lien.ValuedAmount.Amount.String(),
			InstrumentCode: lien.ValuedAmount.InstrumentCode,
		}
		if lien.ValuationAnalysis != nil {
			var analysis pb.ValuationAnalysis
			if unmarshalErr := json.Unmarshal(lien.ValuationAnalysis, &analysis); unmarshalErr == nil {
				resp.Basis = &analysis
			}
		}
	}
	return resp, nil
}

// domainToProtoLien converts a domain Lien to proto Lien.
// AmountCents is stored as minor units (e.g. 10000 = 100.00 GBP).
// Requires reference data for instrument precision resolution.
func (s *Service) domainToProtoLien(ctx context.Context, lien *domain.Lien) (*pb.Lien, error) {
	precision, err := s.getInstrumentPrecision(ctx, lien.InstrumentCode)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "precision lookup failed for instrument %s: %v", lien.InstrumentCode, err)
	}
	displayAmount := decimal.NewFromInt(lien.AmountCents).Shift(-precision).String()

	protoLien := &pb.Lien{
		LienId:    lien.ID.String(),
		AccountId: lien.AccountID.String(),
		Amount: &quantityv1.InstrumentAmount{
			Amount:         displayAmount,
			InstrumentCode: lien.InstrumentCode,
		},
		Status:                mapLienStatusToProto(lien.Status),
		PaymentOrderReference: lien.PaymentOrderReference,
		CreatedAt:             timestamppb.New(lien.CreatedAt),
		UpdatedAt:             timestamppb.New(lien.UpdatedAt),
		BucketId:              lien.BucketID,
		TerminationReason:     lien.TerminationReason,
		Version:               int32(lien.Version),
	}

	if lien.ExpiresAt != nil {
		protoLien.ExpiresAt = timestamppb.New(*lien.ExpiresAt)
	}

	if lien.ReservedQuantity != nil {
		protoLien.ReservedQuantity = &quantityv1.InstrumentAmount{
			Amount:         lien.ReservedQuantity.Amount.String(),
			InstrumentCode: lien.ReservedQuantity.InstrumentCode,
		}
	}

	if lien.ValuedAmount != nil {
		protoLien.ValuedAmount = &quantityv1.InstrumentAmount{
			Amount:         lien.ValuedAmount.Amount.String(),
			InstrumentCode: lien.ValuedAmount.InstrumentCode,
		}
	}

	return protoLien, nil
}

// mapLienStatusToProto converts domain LienStatus to proto LienStatus.
func mapLienStatusToProto(status domain.LienStatus) pb.LienStatus {
	switch status {
	case domain.LienStatusActive:
		return pb.LienStatus_LIEN_STATUS_ACTIVE
	case domain.LienStatusExecuted:
		return pb.LienStatus_LIEN_STATUS_EXECUTED
	case domain.LienStatusTerminated:
		return pb.LienStatus_LIEN_STATUS_TERMINATED
	default:
		return pb.LienStatus_LIEN_STATUS_UNSPECIFIED
	}
}

// getInstrumentPrecision retrieves the decimal precision for an instrument from reference data.
// Fails closed: returns FailedPrecondition if the reference data client is not configured.
func (s *Service) getInstrumentPrecision(ctx context.Context, instrumentCode string) (int32, error) {
	if s.referenceDataClient == nil {
		return 0, status.Error(codes.FailedPrecondition, "reference data client is required for instrument precision lookup")
	}

	refCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	resp, err := s.referenceDataClient.RetrieveInstrument(refCtx, &referencedatav1.RetrieveInstrumentRequest{
		Code: instrumentCode,
	})
	if err != nil {
		return 0, status.Errorf(codes.Internal, "failed to retrieve instrument precision for %s: %v", instrumentCode, err)
	}
	if resp.Instrument == nil {
		return 0, status.Errorf(codes.Internal, "reference data returned nil instrument for %s", instrumentCode)
	}
	return resp.Instrument.GetPrecision(), nil
}

// toMinorUnits converts a major-unit decimal amount to minor units (integer) using the given precision.
// For example, with precision=2: 100.50 -> 10050; with precision=0 (JPY): 1000 -> 1000.
func toMinorUnits(amount decimal.Decimal, precision int32) int64 {
	return amount.Shift(precision).IntPart()
}

// toMajorUnits converts a minor-unit integer to a major-unit decimal string using the given precision.
// For example, with precision=2: 10050 -> "100.5"; with precision=0 (JPY): 1000 -> "1000".
func toMajorUnits(amountCents int64, precision int32) string {
	return decimal.NewFromInt(amountCents).Shift(-precision).String()
}

// isDuplicatePaymentOrderRef checks if the error indicates a unique constraint violation
// on the payment_order_reference column.
func isDuplicatePaymentOrderRef(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "idx_lien_payment_order") ||
		strings.Contains(errStr, "23505") ||
		strings.Contains(errStr, "duplicate key")
}
