package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/domain"
	caobservability "github.com/meridianhub/meridian/services/current-account/observability"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/db"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
)

// InitiateLien creates a fund reservation on an account.
// Supports two modes:
//  1. Legacy (MoneyAmount): Same-currency lien, no valuation needed.
//  2. Multi-asset (InstrumentAmount input): Atomic valuation using valuateInternal(),
//     producing a price-locked valued_amount and full audit trail.
func (s *Service) InitiateLien(ctx context.Context, req *pb.InitiateLienRequest) (*pb.InitiateLienResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordOperationDuration("initiate_lien", operationStatus, time.Since(start))
	}()

	// Validate lien repository is configured
	if s.lienRepo == nil {
		operationStatus = opStatusLienRepoNil
		return nil, status.Error(codes.FailedPrecondition, "lien operations not configured")
	}

	// Check for idempotency using PaymentOrderReference
	if idempotentResp, found, err := s.checkLienIdempotency(ctx, req.PaymentOrderReference); err != nil {
		operationStatus = opStatusRetrieveFailed
		return nil, err
	} else if found {
		return idempotentResp, nil
	}

	// Determine mode: multi-asset (input) or legacy (amount)
	useValuation := req.Input != nil && req.Input.Amount != "" && req.Input.InstrumentCode != ""

	// Prefetch account BEFORE amount parsing so we can use its instrument for dimension-agnostic
	// amount construction. This also validates account existence early.
	prefetchedAccount, err := s.repo.FindByID(ctx, req.AccountId)
	if err != nil {
		if errors.Is(err, persistence.ErrAccountNotFound) {
			operationStatus = opStatusAccountNotFound
			return nil, status.Errorf(codes.NotFound, "account not found: %s", req.AccountId)
		}
		operationStatus = opStatusRetrieveFailed
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	var lienAmount domain.Amount
	var valuationResult *valuateInternalResult

	if useValuation {
		// Multi-asset mode: parse input and run atomic valuation
		inputAmount, err := decimal.NewFromString(req.Input.Amount)
		if err != nil {
			operationStatus = opStatusInvalidInputAmount
			return nil, status.Errorf(codes.InvalidArgument, "invalid input amount: %v", err)
		}
		if !inputAmount.IsPositive() {
			operationStatus = opStatusInputAmountNonPositive
			return nil, status.Error(codes.InvalidArgument, "input amount must be positive")
		}

		knowledgeAt := time.Now()
		if req.KnowledgeAt != nil {
			knowledgeAt = req.KnowledgeAt.AsTime()
		}

		// Run shared valuation function (same logic as EvaluateAssetValuation - Ghost Pricing prevention)
		valuationResult, err = s.valuateInternal(ctx, req.AccountId, inputAmount, req.Input.InstrumentCode, knowledgeAt)
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

		// Convert valued output to domain Amount for lien amount (the actual reservation).
		// Use the account's instrument precision so non-2-decimal instruments work correctly.
		precision := prefetchedAccount.Balance().Instrument().Precision
		// #nosec G115 - precision is bounded by instrument definition (0-9 in practice)
		scale := decimal.NewFromInt(1).Shift(int32(precision))
		valuedMinor := valuationResult.OutputAmount.Mul(scale).RoundBank(0)
		maxInt64 := decimal.NewFromInt(math.MaxInt64)
		minInt64 := decimal.NewFromInt(math.MinInt64)
		if valuedMinor.GreaterThan(maxInt64) || valuedMinor.LessThan(minInt64) {
			operationStatus = opStatusInvalidAmount
			return nil, status.Error(codes.InvalidArgument, ErrAmountOverflow.Error())
		}
		lienAmount, err = domain.NewAmountFromInstrument(
			valuationResult.OutputCode,
			prefetchedAccount.Dimension(),
			precision,
			valuedMinor.IntPart(),
		)
		if err != nil {
			operationStatus = opStatusInvalidAmount
			return nil, status.Errorf(codes.Internal, "failed to create valued amount: %v", err)
		}
	} else {
		// Legacy mode: validate instrument match and convert MoneyAmount using the account's
		// instrument for dimension-agnostic support.
		if req.Amount == nil || req.Amount.Amount == nil {
			operationStatus = opStatusInvalidAmount
			return nil, status.Error(codes.InvalidArgument, "amount is required")
		}
		if req.Amount.Amount.CurrencyCode != prefetchedAccount.InstrumentCode() {
			operationStatus = opStatusCurrencyMismatch
			return nil, status.Errorf(codes.InvalidArgument,
				"currency mismatch: lien currency %s does not match account currency %s",
				req.Amount.Amount.CurrencyCode, prefetchedAccount.InstrumentCode())
		}
		var err error
		lienAmount, err = protoMoneyToAmount(req.Amount, prefetchedAccount)
		if err != nil {
			operationStatus = opStatusInvalidAmount
			return nil, status.Errorf(codes.InvalidArgument, "invalid amount: %v", err)
		}
	}

	if !lienAmount.IsPositive() {
		operationStatus = opStatusInvalidAmount
		return nil, status.Error(codes.InvalidArgument, "lien amount must be positive")
	}

	bucketID := req.BucketId

	prefetchedBalanceCents, err := s.getAccountBalanceMinorUnits(ctx, req.AccountId, prefetchedAccount.InstrumentCode(), prefetchedAccount.Balance().Precision())
	if err != nil {
		operationStatus = opStatusRetrieveFailed
		s.logger.Error("failed to prefetch balance from Position Keeping",
			"account_id", req.AccountId,
			"bucket_id", bucketID,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to retrieve account balance: %v", err)
	}

	// Use a transaction with pessimistic locking to prevent race conditions.
	var lien *domain.Lien
	var account *domain.CurrentAccount
	var availableBalance int64

	txErr := db.WithGormTenantTransaction(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		txRepo := s.repo.WithTx(tx)
		txLienRepo := s.lienRepo.WithTx(tx)

		var txErr error
		accountResult, txErr := txRepo.FindByIDForUpdate(ctx, req.AccountId)
		if txErr != nil {
			return txErr
		}
		account = &accountResult

		if account.Status() != domain.AccountStatusActive {
			return errTxAccountNotActive
		}

		// Validate currency matches account
		if lienAmount.InstrumentCode() != account.Balance().InstrumentCode() {
			return errTxCurrencyMismatch
		}

		// Calculate available balance
		var activeLiensTotal int64
		if bucketID != "" {
			activeLiensTotal, err = txLienRepo.SumActiveAmountByAccountIDAndBucket(ctx, account.ID(), bucketID)
		} else {
			activeLiensTotal, err = txLienRepo.SumActiveAmountByAccountID(ctx, account.ID())
		}
		if err != nil {
			return fmt.Errorf("%w: %v", errTxSumLiensFailed, err) //nolint:errorlint // second error is context-only to preserve errors.Is() for sentinel
		}

		availableBalance = prefetchedBalanceCents - activeLiensTotal

		lienCents, err := lienAmount.ToMinorUnits()
		if err != nil {
			return fmt.Errorf("%w: %v", errTxDomainError, err) //nolint:errorlint // second error is context-only to preserve errors.Is() for sentinel
		}
		if lienCents > availableBalance {
			return errTxInsufficientFunds
		}

		// Create lien domain object
		if useValuation {
			// Multi-asset: create valued lien with price lock
			reservedQty := &domain.InstrumentAmount{
				Amount:         decimal.RequireFromString(req.Input.Amount),
				InstrumentCode: req.Input.InstrumentCode,
			}
			valuedAmt := &domain.InstrumentAmount{
				Amount:         valuationResult.OutputAmount,
				InstrumentCode: valuationResult.OutputCode,
			}
			analysisJSONB, _ := protojson.Marshal(valuationResult.Analysis)
			lien, err = domain.NewValuedLien(account.ID(), lienAmount, bucketID, req.PaymentOrderReference, nil, reservedQty, valuedAmt, analysisJSONB)
		} else {
			lien, err = domain.NewLien(account.ID(), lienAmount, bucketID, req.PaymentOrderReference, nil)
		}
		if err != nil {
			return fmt.Errorf("%w: %v", errTxDomainError, err) //nolint:errorlint // second error is context-only to preserve errors.Is() for sentinel
		}

		if err := txLienRepo.Create(ctx, lien); err != nil {
			return fmt.Errorf("%w: %v", errTxSaveLien, err) //nolint:errorlint // second error is context-only to preserve errors.Is() for sentinel
		}

		return nil
	})

	if txErr != nil {
		switch {
		case errors.Is(txErr, persistence.ErrAccountNotFound):
			operationStatus = opStatusAccountNotFound
			return nil, status.Errorf(codes.NotFound, "account not found: %s", req.AccountId)
		case errors.Is(txErr, errTxAccountNotActive):
			operationStatus = opStatusAccountNotActive
			return nil, status.Errorf(codes.FailedPrecondition, "account is not active")
		case errors.Is(txErr, errTxCurrencyMismatch):
			operationStatus = opStatusCurrencyMismatch
			return nil, status.Errorf(codes.InvalidArgument, "lien currency must match account currency")
		case errors.Is(txErr, errTxInsufficientFunds):
			operationStatus = opStatusInsufficientFunds
			return nil, status.Errorf(codes.FailedPrecondition, "insufficient available balance")
		case errors.Is(txErr, errTxDomainError):
			operationStatus = opStatusDomainError
			return nil, status.Errorf(codes.InvalidArgument, "failed to create lien: %v", txErr)
		case errors.Is(txErr, errTxSaveLien):
			operationStatus = opStatusSaveFailed
			return nil, status.Errorf(codes.Internal, "failed to save lien: %v", txErr)
		default:
			operationStatus = opStatusRetrieveFailed
			return nil, status.Errorf(codes.Internal, "failed to create lien: %v", txErr)
		}
	}

	s.logger.Info("lien created",
		"lien_id", lien.ID.String(),
		"account_id", account.AccountID(),
		"amount_cents", safeMinorUnits(lienAmount),
		"has_valuation", lien.HasValuation(),
		"payment_order_ref", req.PaymentOrderReference)

	// Calculate new available balance after this lien
	newAvailableBalance := availableBalance - lienAmount.ToMinorUnitsUnchecked()
	availableMoney, err := domain.NewAmountFromInstrument(
		account.Balance().InstrumentCode(),
		account.Balance().Dimension(),
		account.Balance().Instrument().Precision,
		newAvailableBalance,
	)
	if err != nil {
		s.logger.Error("failed to create available balance amount", "error", err)
		// Fall back to the pre-lien available balance so the response is not zero.
		availableMoney = account.AvailableBalance()
	}

	resp := &pb.InitiateLienResponse{
		Lien:             toLienProto(lien),
		AvailableBalance: toMoneyAmount(availableMoney),
	}

	// Add valuation fields to response when atomic valuation was performed
	if useValuation && valuationResult != nil {
		resp.ValuedAmount = &quantityv1.InstrumentAmount{
			Amount:         valuationResult.OutputAmount.String(),
			InstrumentCode: valuationResult.OutputCode,
			Version:        1,
		}
		resp.Basis = valuationResult.Analysis
	}

	return resp, nil
}

// ExecuteLien converts a reservation to an actual debit atomically with Redis idempotency
func (s *Service) ExecuteLien(ctx context.Context, req *pb.ExecuteLienRequest) (*pb.ExecuteLienResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordOperationDuration("execute_lien", operationStatus, time.Since(start))
	}()

	// Validate lien repository is configured
	if s.lienRepo == nil {
		operationStatus = opStatusLienRepoNil
		return nil, status.Error(codes.FailedPrecondition, "lien operations not configured")
	}

	// Parse lien ID
	lienID, err := uuid.Parse(req.LienId)
	if err != nil {
		operationStatus = opStatusInvalidLienID
		return nil, status.Errorf(codes.InvalidArgument, "invalid lien ID: %v", err)
	}

	// Get idempotency key if provided
	var idempotencyKeyStr string
	if req.IdempotencyKey != nil && req.IdempotencyKey.Key != "" {
		idempotencyKeyStr = req.IdempotencyKey.Key
	}

	// Build idempotency key structure for Redis
	var idempKey idempotency.Key
	var idempotencyLockAcquired bool
	if idempotencyKeyStr != "" && s.idempotencyService != nil {
		tenantID, ok := tenant.FromContext(ctx)
		if !ok {
			s.logger.Debug("tenant not found in context for idempotency key",
				"lien_id", req.LienId)
		}
		idempKey = idempotency.Key{
			TenantID:  string(tenantID),
			Namespace: idempotencyNamespace,
			Operation: "execute_lien",
			EntityID:  req.LienId,
			RequestID: idempotencyKeyStr,
		}

		// Check Redis for existing result
		result, err := s.idempotencyService.Check(ctx, idempKey)
		if errors.Is(err, idempotency.ErrOperationAlreadyProcessed) && result != nil && result.Data != nil {
			var cachedResp pb.ExecuteLienResponse
			unmarshalErr := proto.Unmarshal(result.Data, &cachedResp)
			if unmarshalErr == nil {
				s.logger.Info("returning cached execute lien response from Redis",
					"lien_id", req.LienId,
					"transaction_id", cachedResp.TransactionId,
					"idempotency_key", idempotencyKeyStr)
				operationStatus = opStatusIdempotent
				return &cachedResp, nil
			}
			s.logger.Warn("failed to unmarshal cached idempotency result",
				"error", unmarshalErr)
		} else if err != nil && !errors.Is(err, idempotency.ErrResultNotFound) {
			s.logger.Error("idempotency check failed", "error", err)
			return nil, status.Error(codes.Internal, "failed to check idempotency")
		}

		// Mark operation as pending (distributed lock)
		if err := s.idempotencyService.MarkPending(ctx, idempKey, idempotencyPendingTTL); err != nil {
			// Check if another request is already processing this operation
			if errors.Is(err, idempotency.ErrOperationAlreadyProcessed) {
				s.logger.Info("operation already in progress, please retry",
					"idempotency_key", idempotencyKeyStr)
				return nil, status.Error(codes.Aborted, "operation already in progress, please retry")
			}
			s.logger.Error("failed to mark operation pending", "error", err)
			return nil, status.Error(codes.Aborted, "failed to acquire idempotency lock, please retry")
		}
		idempotencyLockAcquired = true

		// Cleanup pending state on failure - ensures retries aren't blocked for 5 minutes
		defer func() {
			if idempotencyLockAcquired && operationStatus != operationStatusSuccess {
				if delErr := s.idempotencyService.Delete(ctx, idempKey); delErr != nil {
					s.logger.Warn("failed to cleanup pending idempotency state",
						"error", delErr,
						"idempotency_key", idempotencyKeyStr)
				}
			}
		}()
	}

	// First, check for idempotency without locking (read-only check)
	// Note: Context is passed to enable organization scoping in multi-org mode
	lien, err := s.lienRepo.FindByID(ctx, lienID)
	if err != nil {
		if errors.Is(err, persistence.ErrLienNotFound) {
			operationStatus = opStatusLienNotFound
			return nil, status.Errorf(codes.NotFound, "lien not found: %s", req.LienId)
		}
		operationStatus = opStatusRetrieveFailed
		return nil, status.Errorf(codes.Internal, "failed to retrieve lien: %v", err)
	}

	// Check if already executed (idempotent) - no transaction needed for read-only
	if lien.Status == domain.LienStatusExecuted {
		s.logger.Info("lien already executed (idempotent)", "lien_id", lien.ID.String())
		resp, err := s.buildExecuteLienIdempotentResponse(ctx, lien)
		if err != nil {
			operationStatus = opStatusRetrieveAccountFailed
			return nil, err
		}
		return resp, nil
	}

	// Prefetch account and balance from Position Keeping BEFORE entering transaction
	// to avoid holding database locks during external service calls (deadlock prevention).
	prefetchAccount, err := s.repo.FindByUUID(ctx, lien.AccountID)
	if err != nil {
		if errors.Is(err, persistence.ErrAccountNotFound) {
			operationStatus = opStatusAccountNotFound
			return nil, status.Errorf(codes.NotFound, "account not found for lien: %s", lien.AccountID)
		}
		operationStatus = opStatusRetrieveAccountFailed
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	// Prefetch balance from Position Keeping.
	// Note: Currently Position Keeping returns total balance regardless of bucket.
	// The lien's bucket_id is used for bucket-scoped lien calculations within Current Account,
	// but the balance comes from total Position Keeping balance.
	prefetchedBalanceCents, err := s.getAccountBalanceMinorUnits(ctx, prefetchAccount.AccountID(), prefetchAccount.InstrumentCode(), prefetchAccount.Balance().Precision())
	if err != nil {
		operationStatus = opStatusRetrieveFailed
		s.logger.Error("failed to prefetch balance from Position Keeping",
			"account_id", prefetchAccount.AccountID(),
			"bucket_id", lien.BucketID,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to retrieve account balance: %v", err)
	}

	// Execute atomically in a transaction with pessimistic locking to prevent race conditions.
	// We lock both the lien and account to prevent concurrent execute/terminate operations.
	var account *domain.CurrentAccount
	txErr := db.WithGormTenantTransaction(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		txRepo := s.repo.WithTx(tx)
		txLienRepo := s.lienRepo.WithTx(tx)

		// Retrieve lien with FOR UPDATE lock to prevent concurrent modifications
		var txErr error
		lien, txErr = txLienRepo.FindByIDForUpdate(ctx, lienID)
		if txErr != nil {
			return txErr
		}

		// Re-check if already executed (could have been executed by concurrent request)
		if lien.Status == domain.LienStatusExecuted {
			return nil // Will be handled as idempotent response
		}

		// Validate lien can be executed
		if !lien.CanExecute() {
			return errTxInvalidLienStatus
		}

		// Retrieve account with FOR UPDATE lock to prevent concurrent modifications
		accountResult, txErr := txRepo.FindByUUIDForUpdate(ctx, lien.AccountID)
		if txErr != nil {
			return fmt.Errorf("%w: %v", errTxSaveAccount, txErr) //nolint:errorlint // second error is context-only to preserve errors.Is() for sentinel
		}

		// Reconstruct account with pre-fetched balance (no external call inside tx)
		// Balance was fetched from Position Keeping before entering transaction.
		accountResult, txErr = s.hydrateAccountWithPrefetchedBalance(accountResult, prefetchedBalanceCents)
		if txErr != nil {
			return fmt.Errorf("%w: %v", errTxDomainError, txErr) //nolint:errorlint // second error is context-only to preserve errors.Is() for sentinel
		}

		// Execute lien (domain logic - marks status as executed)
		if err := lien.Execute(); err != nil {
			return fmt.Errorf("%w: %v", errTxExecuteFailed, err) //nolint:errorlint // second error is context-only to preserve errors.Is() for sentinel
		}

		// Debit the account (immutable: capture returned value)
		accountResult, err := accountResult.Withdraw(lien.Amount)
		if err != nil {
			return fmt.Errorf("%w: %v", errTxWithdrawFailed, err) //nolint:errorlint // second error is context-only to preserve errors.Is() for sentinel
		}
		account = &accountResult

		// Update lien status
		if err := txLienRepo.Update(ctx, lien); err != nil {
			return fmt.Errorf("%w: %v", errTxUpdateLien, err) //nolint:errorlint // second error is context-only to preserve errors.Is() for sentinel
		}

		// Save account with balance change (context carries audit user info)
		if err := txRepo.Save(ctx, accountResult); err != nil {
			return fmt.Errorf("%w: %v", errTxSaveAccount, err) //nolint:errorlint // second error is context-only to preserve errors.Is() for sentinel
		}

		return nil
	})

	if txErr != nil {
		// Determine which operation failed for proper error reporting
		switch {
		case errors.Is(txErr, persistence.ErrLienNotFound):
			operationStatus = opStatusLienNotFound
			return nil, status.Errorf(codes.NotFound, "lien not found: %s", req.LienId)
		case errors.Is(txErr, persistence.ErrLienVersionConflict):
			operationStatus = opStatusVersionConflict
			return nil, status.Error(codes.Aborted, "concurrent modification detected, please retry")
		case errors.Is(txErr, persistence.ErrVersionConflict):
			operationStatus = opStatusVersionConflict
			return nil, status.Error(codes.Aborted, "concurrent modification detected, please retry")
		case errors.Is(txErr, errTxInvalidLienStatus):
			operationStatus = opStatusInvalidLienStatus
			return nil, status.Errorf(codes.FailedPrecondition, "lien cannot be executed: status=%s, expired=%v", lien.Status, lien.IsExpired())
		case errors.Is(txErr, errTxSaveAccount):
			operationStatus = opStatusSaveAccountFailed
			return nil, status.Errorf(codes.Internal, "failed to execute lien transaction: %v", txErr)
		case errors.Is(txErr, errTxUpdateLien):
			operationStatus = opStatusUpdateLienFailed
			return nil, status.Errorf(codes.Internal, "failed to execute lien transaction: %v", txErr)
		case errors.Is(txErr, errTxExecuteFailed):
			operationStatus = opStatusExecuteFailed
			return nil, status.Errorf(codes.FailedPrecondition, "failed to execute lien: %v", txErr)
		case errors.Is(txErr, errTxWithdrawFailed):
			operationStatus = opStatusWithdrawFailed
			return nil, status.Errorf(codes.FailedPrecondition, "failed to debit account: %v", txErr)
		default:
			operationStatus = opStatusRetrieveAccountFailed
			return nil, status.Errorf(codes.Internal, "failed to execute lien transaction: %v", txErr)
		}
	}

	// Handle case where lien was already executed by concurrent request
	if account == nil {
		s.logger.Info("lien executed by concurrent request (idempotent)", "lien_id", lien.ID.String())
		resp, err := s.buildExecuteLienIdempotentResponse(ctx, lien)
		if err != nil {
			operationStatus = opStatusRetrieveAccountFailed
			return nil, err
		}
		return resp, nil
	}

	transactionID := fmt.Sprintf("TXN-LIEN-%s", lien.ID.String()[:8])
	s.logger.Info("lien executed",
		"lien_id", lien.ID.String(),
		"account_id", account.AccountID(),
		"amount_cents", safeMinorUnits(lien.Amount),
		"has_valuation", lien.HasValuation(),
		"transaction_id", transactionID)

	// Basis drift detection: warn if the valuation basis is stale.
	// This does NOT block execution - the price lock is always used.
	if lien.HasValuation() && lien.ValuationAnalysis != nil {
		s.checkBasisDrift(lien)
	}

	// Release the Position Keeping reservation (best-effort).
	// The lien execution succeeded, so the reservation should transition to EXECUTED.
	s.releaseReservation(ctx, lien.ID.String(), positionkeepingv1.ReservationStatus_RESERVATION_STATUS_EXECUTED)

	// Calculate available balance scoped to the lien's bucket (if any)
	availableMoney := s.calculateAvailableBalanceByBucket(ctx, lien.AccountID, lien.BucketID, account.Balance())
	resp := &pb.ExecuteLienResponse{
		Lien:             toLienProto(lien),
		NewBalance:       toMoneyAmount(account.Balance()),
		AvailableBalance: toMoneyAmount(availableMoney),
		TransactionId:    transactionID,
	}

	// Store successful result in Redis for future idempotency checks
	if idempotencyKeyStr != "" && s.idempotencyService != nil {
		responseData, marshalErr := proto.Marshal(resp)
		if marshalErr == nil {
			storeErr := s.idempotencyService.StoreResult(ctx, idempotency.Result{
				Key:         idempKey,
				Status:      idempotency.StatusCompleted,
				Data:        responseData,
				CompletedAt: time.Now(),
				TTL:         idempotencyResultTTL,
			})
			if storeErr != nil {
				s.logger.Error("failed to store idempotency result", "error", storeErr)
				// Continue - operation succeeded, caching is optimization
			}
		} else {
			s.logger.Error("failed to marshal response for idempotency cache", "error", marshalErr)
		}
	}

	return resp, nil
}

// TerminateLien releases a reservation without executing
func (s *Service) TerminateLien(ctx context.Context, req *pb.TerminateLienRequest) (*pb.TerminateLienResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordOperationDuration("terminate_lien", operationStatus, time.Since(start))
	}()

	// Validate lien repository is configured
	if s.lienRepo == nil {
		operationStatus = opStatusLienRepoNil
		return nil, status.Error(codes.FailedPrecondition, "lien operations not configured")
	}

	// Parse lien ID
	lienID, err := uuid.Parse(req.LienId)
	if err != nil {
		operationStatus = opStatusInvalidLienID
		return nil, status.Errorf(codes.InvalidArgument, "invalid lien ID: %v", err)
	}

	// First, check for idempotency without locking (read-only check)
	// Note: Context is passed to enable organization scoping in multi-org mode
	lien, err := s.lienRepo.FindByID(ctx, lienID)
	if err != nil {
		if errors.Is(err, persistence.ErrLienNotFound) {
			operationStatus = opStatusLienNotFound
			return nil, status.Errorf(codes.NotFound, "lien not found: %s", req.LienId)
		}
		operationStatus = opStatusRetrieveFailed
		return nil, status.Errorf(codes.Internal, "failed to retrieve lien: %v", err)
	}

	// Check if already terminated (idempotent) - no transaction needed for read-only
	if lien.Status == domain.LienStatusTerminated {
		s.logger.Info("lien already terminated (idempotent)",
			"lien_id", lien.ID.String())

		// Calculate available balance - errors logged but don't fail idempotent response
		account, acctErr := s.repo.FindByUUID(ctx, lien.AccountID)
		if acctErr != nil {
			s.logger.Error("failed to find account for idempotent response", "error", acctErr)
			return &pb.TerminateLienResponse{Lien: toLienProto(lien)}, nil
		}
		// Hydrate account with balance from Position Keeping.
		account, acctErr = s.hydrateAccountWithBalance(ctx, account)
		if acctErr != nil {
			s.logger.Error("failed to get account balance for idempotent response", "error", acctErr)
			return &pb.TerminateLienResponse{Lien: toLienProto(lien)}, nil
		}
		// Calculate available balance scoped to the lien's bucket (if any)
		availableMoney := s.calculateAvailableBalanceByBucket(ctx, lien.AccountID, lien.BucketID, account.Balance())
		return &pb.TerminateLienResponse{
			Lien:             toLienProto(lien),
			AvailableBalance: toMoneyAmount(availableMoney),
		}, nil
	}

	// Determine termination reason
	reason := req.Reason
	if reason == "" {
		reason = defaultTerminationReason
	}

	// Terminate atomically in a transaction with pessimistic locking to prevent race conditions.
	// Without FOR UPDATE, concurrent TerminateLien calls could both pass CanTerminate() checks.
	txErr := db.WithGormTenantTransaction(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		txLienRepo := s.lienRepo.WithTx(tx)

		// Retrieve lien with FOR UPDATE lock to prevent concurrent modifications
		lien, err = txLienRepo.FindByIDForUpdate(ctx, lienID)
		if err != nil {
			return err
		}

		// Re-check if already terminated (could have been terminated by concurrent request)
		if lien.Status == domain.LienStatusTerminated {
			return nil // Will be handled as idempotent response
		}

		// Validate lien can be terminated
		if !lien.CanTerminate() {
			return errTxInvalidLienStatus
		}

		// Terminate lien (domain logic)
		if err := lien.Terminate(reason); err != nil {
			return fmt.Errorf("%w: %v", errTxTerminateFailed, err) //nolint:errorlint // second error is context-only to preserve errors.Is() for sentinel
		}

		// Update lien status
		if err := txLienRepo.Update(ctx, lien); err != nil {
			return fmt.Errorf("%w: %v", errTxUpdateLien, err) //nolint:errorlint // second error is context-only to preserve errors.Is() for sentinel
		}

		return nil
	})

	if txErr != nil {
		switch {
		case errors.Is(txErr, persistence.ErrLienNotFound):
			operationStatus = opStatusLienNotFound
			return nil, status.Errorf(codes.NotFound, "lien not found: %s", req.LienId)
		case errors.Is(txErr, persistence.ErrLienVersionConflict):
			operationStatus = opStatusVersionConflict
			return nil, status.Error(codes.Aborted, "concurrent modification detected, please retry")
		case errors.Is(txErr, errTxInvalidLienStatus):
			operationStatus = opStatusInvalidLienStatus
			return nil, status.Errorf(codes.FailedPrecondition, "lien cannot be terminated: status=%s", lien.Status)
		case errors.Is(txErr, errTxTerminateFailed):
			operationStatus = opStatusTerminateFailed
			return nil, status.Errorf(codes.FailedPrecondition, "failed to terminate lien: %v", txErr)
		case errors.Is(txErr, errTxUpdateLien):
			operationStatus = opStatusUpdateFailed
			return nil, status.Errorf(codes.Internal, "failed to update lien: %v", txErr)
		default:
			operationStatus = opStatusRetrieveFailed
			return nil, status.Errorf(codes.Internal, "failed to terminate lien: %v", txErr)
		}
	}

	// Handle case where lien was already terminated by concurrent request
	if lien.Status == domain.LienStatusTerminated && lien.TerminationReason != reason {
		s.logger.Info("lien terminated by concurrent request (idempotent)",
			"lien_id", lien.ID.String())
	} else {
		s.logger.Info("lien terminated",
			"lien_id", lien.ID.String(),
			"reason", reason)
	}

	// Release the Position Keeping reservation (best-effort).
	// The lien termination succeeded, so the reservation should transition to TERMINATED.
	s.releaseReservation(ctx, lien.ID.String(), positionkeepingv1.ReservationStatus_RESERVATION_STATUS_TERMINATED)

	// Calculate new available balance (funds released)
	account, err := s.repo.FindByUUID(ctx, lien.AccountID)
	if err != nil {
		operationStatus = opStatusRetrieveAccountFailed
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	// Hydrate account with balance from Position Keeping.
	// Balance is no longer persisted - it comes from Position Keeping service.
	account, err = s.hydrateAccountWithBalance(ctx, account)
	if err != nil {
		operationStatus = opStatusRetrieveAccountFailed
		return nil, status.Errorf(codes.Internal, "failed to get account balance: %v", err)
	}

	// Calculate available balance scoped to the lien's bucket (if any)
	availableMoney := s.calculateAvailableBalanceByBucket(ctx, lien.AccountID, lien.BucketID, account.Balance())
	return &pb.TerminateLienResponse{
		Lien:             toLienProto(lien),
		AvailableBalance: toMoneyAmount(availableMoney),
	}, nil
}
