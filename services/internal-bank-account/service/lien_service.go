package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_bank_account/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/internal-bank-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/internal-bank-account/domain"
	ibaobservability "github.com/meridianhub/meridian/services/internal-bank-account/observability"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
)

// InitiateLien creates a new fund reservation on an internal bank account.
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

	// Validate input
	if req.Input == nil || req.Input.Amount == "" {
		operationStatus = opStatusInvalidRequest
		return nil, status.Error(codes.InvalidArgument, "input amount is required")
	}
	if req.Input.InstrumentCode == "" {
		operationStatus = opStatusInvalidRequest
		return nil, status.Error(codes.InvalidArgument, "input instrument_code is required")
	}
	if strings.TrimSpace(req.PaymentOrderReference) == "" {
		operationStatus = opStatusInvalidRequest
		return nil, status.Error(codes.InvalidArgument, "payment_order_reference is required")
	}

	inputAmount, err := decimal.NewFromString(req.Input.Amount)
	if err != nil {
		operationStatus = opStatusInvalidInputAmount
		return nil, status.Errorf(codes.InvalidArgument, "invalid input amount: %v", err)
	}
	if !inputAmount.IsPositive() {
		operationStatus = opStatusInputAmountNonPositive
		return nil, status.Error(codes.InvalidArgument, "input amount must be positive")
	}

	// Resolve account
	account, err := s.findAccountByID(ctx, req.AccountId)
	if err != nil {
		operationStatus = opStatusAccountNotFound
		return nil, err
	}

	nativeInstrument := account.InstrumentCode()

	// Check idempotency: if a lien already exists for this payment order reference, return it
	existingLien, err := s.lienRepo.FindByPaymentOrderReference(ctx, req.PaymentOrderReference)
	if err == nil {
		// Lien already exists - return it for idempotency
		operationStatus = opStatusLienAlreadyExists
		return s.buildInitiateLienResponse(existingLien), nil
	}
	if !errors.Is(err, persistence.ErrLienNotFound) {
		operationStatus = operationStatusFailed
		return nil, status.Errorf(codes.Internal, "failed to check lien idempotency: %v", err)
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

	var lien *domain.Lien

	if req.Input.InstrumentCode == nativeInstrument {
		// Same-instrument lien: no valuation needed.
		// Store the amount in minor units (cents). For fiat currencies this is
		// amount * 100; for non-decimal instruments (kWh, GPU_HOUR) the raw
		// integer part is used (the proto contract sends whole units).
		amountCents := inputAmount.Shift(2).IntPart()
		if amountCents == 0 {
			// Non-decimal instrument — use integer part directly
			amountCents = inputAmount.IntPart()
		}

		lien, err = domain.NewLien(account.ID(), amountCents, nativeInstrument, req.BucketId, req.PaymentOrderReference, expiresAt)
		if err != nil {
			operationStatus = opStatusInvalidRequest
			return nil, status.Errorf(codes.InvalidArgument, "failed to create lien: %v", err)
		}
	} else {
		// Cross-instrument lien: perform atomic valuation via valuateInternal()
		result, err := s.valuateInternal(ctx, req.AccountId, inputAmount, req.Input.InstrumentCode, knowledgeAt)
		if err != nil {
			switch {
			case errors.Is(err, ErrValuationAccountNotFound):
				operationStatus = opStatusAccountNotFound
				return nil, status.Errorf(codes.NotFound, "%v", err)
			case errors.Is(err, ErrNoActiveValuationFeature):
				operationStatus = opStatusNoValuationFeature
				return nil, status.Errorf(codes.FailedPrecondition, "%v", err)
			case errors.Is(err, ErrValuationFeatureNotActive):
				operationStatus = opStatusFeatureNotActive
				return nil, status.Errorf(codes.FailedPrecondition, "%v", err)
			case errors.Is(err, ErrValuationRepoNotConfigured):
				operationStatus = opStatusValuationFeatureRepoNil
				return nil, status.Errorf(codes.FailedPrecondition, "%v", err)
			case errors.Is(err, ErrValuationEngineFailed):
				operationStatus = opStatusValuationFailed
				return nil, status.Errorf(codes.Internal, "%v", err)
			default:
				operationStatus = opStatusValuationFailed
				return nil, status.Errorf(codes.Internal, "valuation failed: %v", err)
			}
		}

		// Build valued lien: convert output amount to minor units.
		// TODO: Use instrument precision from reference data instead of hardcoded 2.
		amountCents := result.OutputAmount.Shift(2).IntPart()
		if amountCents == 0 {
			amountCents = result.OutputAmount.IntPart()
		}

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

		lien, err = domain.NewValuedLien(
			account.ID(), amountCents, nativeInstrument, req.BucketId,
			req.PaymentOrderReference, expiresAt,
			reservedQuantity, valuedAmount, analysisJSON,
		)
		if err != nil {
			operationStatus = opStatusInvalidRequest
			return nil, status.Errorf(codes.InvalidArgument, "failed to create valued lien: %v", err)
		}
	}

	// Persist the lien
	if err := s.lienRepo.Create(ctx, lien); err != nil {
		if isDuplicatePaymentOrderRef(err) {
			// Race condition: another request created the lien between our check and create.
			// Return the existing lien for idempotency.
			existingLien, findErr := s.lienRepo.FindByPaymentOrderReference(ctx, req.PaymentOrderReference)
			if findErr != nil {
				operationStatus = opStatusLienCreateFailed
				return nil, status.Errorf(codes.Internal, "lien creation race condition: %v", err)
			}
			return s.buildInitiateLienResponse(existingLien), nil
		}
		operationStatus = opStatusLienCreateFailed
		return nil, status.Errorf(codes.Internal, "failed to create lien: %v", err)
	}

	s.logger.Info("created lien",
		"lien_id", lien.ID,
		"account_id", req.AccountId,
		"amount_cents", lien.AmountCents,
		"currency", lien.Currency,
		"has_valuation", lien.HasValuation())

	return s.buildInitiateLienResponse(lien), nil
}

// ExecuteLien converts a lien reservation to an actual debit.
// Transitions the lien from ACTIVE to EXECUTED (terminal state, idempotent).
func (s *Service) ExecuteLien(ctx context.Context, req *pb.ExecuteLienRequest) (*pb.ExecuteLienResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		ibaobservability.RecordOperationDuration("execute_lien", operationStatus, time.Since(start))
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
		// Idempotent: already executed
		return &pb.ExecuteLienResponse{
			Lien: s.domainToProtoLien(lien),
		}, nil
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

	// Re-check after lock: another request may have executed between read and lock
	if lien.Status == domain.LienStatusExecuted {
		return &pb.ExecuteLienResponse{
			Lien: s.domainToProtoLien(lien),
		}, nil
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

	return &pb.ExecuteLienResponse{
		Lien: s.domainToProtoLien(lien),
	}, nil
}

// TerminateLien releases reserved funds without executing.
// Transitions the lien from ACTIVE to TERMINATED (terminal state, idempotent).
func (s *Service) TerminateLien(ctx context.Context, req *pb.TerminateLienRequest) (*pb.TerminateLienResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		ibaobservability.RecordOperationDuration("terminate_lien", operationStatus, time.Since(start))
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
		// Idempotent: already terminated
		return &pb.TerminateLienResponse{
			Lien: s.domainToProtoLien(lien),
		}, nil
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

	// Re-check after lock: another request may have terminated between read and lock
	if lien.Status == domain.LienStatusTerminated {
		return &pb.TerminateLienResponse{
			Lien: s.domainToProtoLien(lien),
		}, nil
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

	return &pb.TerminateLienResponse{
		Lien: s.domainToProtoLien(lien),
	}, nil
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

	return &pb.RetrieveLienResponse{
		Lien: s.domainToProtoLien(lien),
	}, nil
}

// buildInitiateLienResponse constructs a consistent InitiateLienResponse
// including valuation fields when present.
func (s *Service) buildInitiateLienResponse(lien *domain.Lien) *pb.InitiateLienResponse {
	resp := &pb.InitiateLienResponse{
		Lien: s.domainToProtoLien(lien),
	}
	if lien.HasValuation() {
		resp.ValuedAmount = &quantityv1.InstrumentAmount{
			Amount:         lien.ValuedAmount.Amount.String(),
			InstrumentCode: lien.ValuedAmount.InstrumentCode,
		}
		if lien.ValuationAnalysis != nil {
			var analysis pb.ValuationAnalysis
			if err := json.Unmarshal(lien.ValuationAnalysis, &analysis); err == nil {
				resp.Basis = &analysis
			}
		}
	}
	return resp
}

// domainToProtoLien converts a domain Lien to proto Lien.
// AmountCents is stored as minor units (e.g. 10000 = 100.00 GBP).
// TODO: Use instrument precision from reference data instead of hardcoded 2.
func (s *Service) domainToProtoLien(lien *domain.Lien) *pb.Lien {
	displayAmount := decimal.NewFromInt(lien.AmountCents).Shift(-2).String()

	protoLien := &pb.Lien{
		LienId:    lien.ID.String(),
		AccountId: lien.AccountID.String(),
		Amount: &quantityv1.InstrumentAmount{
			Amount:         displayAmount,
			InstrumentCode: lien.Currency,
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

	return protoLien
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
