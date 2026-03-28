package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/domain"
	caobservability "github.com/meridianhub/meridian/services/current-account/observability"
	"github.com/meridianhub/meridian/shared/platform/db"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
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
		var err error
		lienAmount, valuationResult, operationStatus, err = s.computeMultiAssetLienAmount(ctx, req, prefetchedAccount)
		if err != nil {
			return nil, err
		}
	} else {
		var err error
		lienAmount, operationStatus, err = computeLegacyLienAmount(req, prefetchedAccount)
		if err != nil {
			return nil, err
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
		operationStatus, err = mapInitiateLienTxError(txErr, req.AccountId)
		return nil, err
	}

	s.logger.Info("lien created",
		"lien_id", lien.ID.String(),
		"account_id", account.AccountID(),
		"amount_cents", safeMinorUnits(lienAmount),
		"has_valuation", lien.HasValuation(),
		"payment_order_ref", req.PaymentOrderReference)

	return s.buildInitiateLienResponse(account, lien, lienAmount, availableBalance, useValuation, valuationResult), nil
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

	// Idempotency: check Redis cache, acquire distributed lock
	idempotencyKeyStr, idempKey, idempotencyLockAcquired, cachedResp, err := s.acquireExecuteLienIdempotency(ctx, req)
	if err != nil {
		operationStatus = opStatusRetrieveFailed
		return nil, err
	}
	if cachedResp != nil {
		operationStatus = opStatusIdempotent
		return cachedResp, nil
	}

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

	// Read-only idempotency check + prefetch account/balance data
	lien, prefetchedBalanceCents, opStatus, err := s.prefetchExecuteLienData(ctx, lienID, req.LienId)
	if err != nil {
		operationStatus = opStatus
		return nil, err
	}
	if lien.Status == domain.LienStatusExecuted {
		return s.returnExecuteLienIdempotent(ctx, lien, "lien already executed (idempotent)", &operationStatus)
	}

	// Execute atomically in a transaction
	updatedLien, account, txErr := s.executeLienTransaction(ctx, lienID, lien, prefetchedBalanceCents)
	if txErr != nil {
		operationStatus, err = mapExecuteLienTxError(txErr, req.LienId, lien)
		return nil, err
	}
	lien = updatedLien

	// Handle case where lien was already executed by concurrent request
	if account == nil {
		return s.returnExecuteLienIdempotent(ctx, lien, "lien executed by concurrent request (idempotent)", &operationStatus)
	}

	// Build final response with post-execution processing
	resp := s.buildExecuteLienFinalResponse(ctx, lien, account)
	s.storeIdempotencyResult(ctx, idempotencyKeyStr, idempKey, resp)

	return resp, nil
}

// returnExecuteLienIdempotent logs and returns an idempotent response for an already-executed lien.
func (s *Service) returnExecuteLienIdempotent(ctx context.Context, lien *domain.Lien, logMsg string, operationStatus *string) (*pb.ExecuteLienResponse, error) {
	s.logger.Info(logMsg, "lien_id", lien.ID.String())
	resp, err := s.buildExecuteLienIdempotentResponse(ctx, lien)
	if err != nil {
		*operationStatus = opStatusRetrieveAccountFailed
		return nil, err
	}
	return resp, nil
}

// prefetchExecuteLienData retrieves the lien, account, and balance before entering the transaction.
// This avoids holding database locks during external service calls (deadlock prevention).
func (s *Service) prefetchExecuteLienData(ctx context.Context, lienID uuid.UUID, lienIDStr string) (*domain.Lien, int64, string, error) {
	// Read-only check: if already executed, return early
	lien, err := s.lienRepo.FindByID(ctx, lienID)
	if err != nil {
		if errors.Is(err, persistence.ErrLienNotFound) {
			return nil, 0, opStatusLienNotFound, status.Errorf(codes.NotFound, "lien not found: %s", lienIDStr)
		}
		return nil, 0, opStatusRetrieveFailed, status.Errorf(codes.Internal, "failed to retrieve lien: %v", err)
	}

	if lien.Status == domain.LienStatusExecuted {
		return lien, 0, "", nil
	}

	// Prefetch account
	prefetchAccount, err := s.repo.FindByUUID(ctx, lien.AccountID)
	if err != nil {
		if errors.Is(err, persistence.ErrAccountNotFound) {
			return nil, 0, opStatusAccountNotFound, status.Errorf(codes.NotFound, "account not found for lien: %s", lien.AccountID)
		}
		return nil, 0, opStatusRetrieveAccountFailed, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	// Prefetch balance from Position Keeping
	prefetchedBalanceCents, err := s.getAccountBalanceMinorUnits(ctx, prefetchAccount.AccountID(), prefetchAccount.InstrumentCode(), prefetchAccount.Balance().Precision())
	if err != nil {
		s.logger.Error("failed to prefetch balance from Position Keeping",
			"account_id", prefetchAccount.AccountID(),
			"bucket_id", lien.BucketID,
			"error", err)
		return nil, 0, opStatusRetrieveFailed, status.Errorf(codes.Internal, "failed to retrieve account balance: %v", err)
	}

	return lien, prefetchedBalanceCents, "", nil
}

// executeLienTransaction executes the lien atomically in a transaction with pessimistic locking.
// Returns the updated account (nil if lien was already executed by concurrent request).
func (s *Service) executeLienTransaction(ctx context.Context, lienID uuid.UUID, lien *domain.Lien, prefetchedBalanceCents int64) (*domain.Lien, *domain.CurrentAccount, error) {
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

		if !lien.CanExecute() {
			return errTxInvalidLienStatus
		}

		// Retrieve account with FOR UPDATE lock
		accountResult, txErr := txRepo.FindByUUIDForUpdate(ctx, lien.AccountID)
		if txErr != nil {
			return fmt.Errorf("%w: %v", errTxSaveAccount, txErr) //nolint:errorlint // second error is context-only to preserve errors.Is() for sentinel
		}

		// Reconstruct account with pre-fetched balance (no external call inside tx)
		accountResult, txErr = s.hydrateAccountWithPrefetchedBalance(accountResult, prefetchedBalanceCents)
		if txErr != nil {
			return fmt.Errorf("%w: %v", errTxDomainError, txErr) //nolint:errorlint // second error is context-only to preserve errors.Is() for sentinel
		}

		if err := lien.Execute(); err != nil {
			return fmt.Errorf("%w: %v", errTxExecuteFailed, err) //nolint:errorlint // second error is context-only to preserve errors.Is() for sentinel
		}

		accountResult, err := accountResult.Withdraw(lien.Amount)
		if err != nil {
			return fmt.Errorf("%w: %v", errTxWithdrawFailed, err) //nolint:errorlint // second error is context-only to preserve errors.Is() for sentinel
		}
		account = &accountResult

		if err := txLienRepo.Update(ctx, lien); err != nil {
			return fmt.Errorf("%w: %v", errTxUpdateLien, err) //nolint:errorlint // second error is context-only to preserve errors.Is() for sentinel
		}

		if err := txRepo.Save(ctx, accountResult); err != nil {
			return fmt.Errorf("%w: %v", errTxSaveAccount, err) //nolint:errorlint // second error is context-only to preserve errors.Is() for sentinel
		}

		return nil
	})

	return lien, account, txErr
}

// buildExecuteLienFinalResponse logs execution, checks basis drift, releases reservations,
// and builds the final ExecuteLienResponse.
func (s *Service) buildExecuteLienFinalResponse(ctx context.Context, lien *domain.Lien, account *domain.CurrentAccount) *pb.ExecuteLienResponse {
	transactionID := fmt.Sprintf("TXN-LIEN-%s", lien.ID.String()[:8])
	s.logger.Info("lien executed",
		"lien_id", lien.ID.String(),
		"account_id", account.AccountID(),
		"amount_cents", safeMinorUnits(lien.Amount),
		"has_valuation", lien.HasValuation(),
		"transaction_id", transactionID)

	// Basis drift detection: warn if the valuation basis is stale.
	if lien.HasValuation() && lien.ValuationAnalysis != nil {
		s.checkBasisDrift(lien)
	}

	// Release the Position Keeping reservation (best-effort).
	s.releaseReservation(ctx, lien.ID.String(), positionkeepingv1.ReservationStatus_RESERVATION_STATUS_EXECUTED)

	// Calculate available balance scoped to the lien's bucket (if any)
	availableMoney := s.calculateAvailableBalanceByBucket(ctx, lien.AccountID, lien.BucketID, account.Balance())
	return &pb.ExecuteLienResponse{
		Lien:             toLienProto(lien),
		NewBalance:       toMoneyAmount(account.Balance()),
		AvailableBalance: toMoneyAmount(availableMoney),
		TransactionId:    transactionID,
	}
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
		operationStatus, err = mapTerminateLienTxError(txErr, req.LienId, lien)
		return nil, err
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
