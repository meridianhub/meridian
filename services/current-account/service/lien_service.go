// Package service implements gRPC services for the current account domain
package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/domain"
	caobservability "github.com/meridianhub/meridian/services/current-account/observability"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/db"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// Lien-specific errors
var (
	ErrLienRepositoryNil      = errors.New("lien repository cannot be nil")
	ErrInsufficientFunds      = errors.New("insufficient available balance for lien")
	ErrLienCurrencyMismatch   = errors.New("lien currency must match account currency")
	ErrLienAmountNotPositive  = errors.New("lien amount must be positive")
	ErrAmountRequired         = errors.New("amount is required")
	ErrAmountOverflow         = errors.New("amount too large: would overflow")
	ErrInstrumentCodeMismatch = errors.New("instrument code mismatch in balance response")
	// Transaction operation errors for error detection
	errTxSaveAccount       = errors.New("save_account")
	errTxUpdateLien        = errors.New("update_lien")
	errTxSaveLien          = errors.New("save_lien")
	errTxAccountNotActive  = errors.New("account_not_active")
	errTxCurrencyMismatch  = errors.New("currency_mismatch")
	errTxSumLiensFailed    = errors.New("sum_liens_failed")
	errTxInsufficientFunds = errors.New("insufficient_funds")
	errTxDomainError       = errors.New("domain_error")
	errTxExecuteFailed     = errors.New("execute_failed")
	errTxWithdrawFailed    = errors.New("withdraw_failed")
	errTxTerminateFailed   = errors.New("terminate_failed")
	errTxInvalidLienStatus = errors.New("invalid_lien_status")
)

// Default termination reason when none provided
const defaultTerminationReason = "Terminated via API"

// Lien operation status constants for metrics
const (
	opStatusLienRepoNil           = "lien_repo_nil"
	opStatusInvalidLienID         = "invalid_lien_id"
	opStatusLienNotFound          = "lien_not_found"
	opStatusRetrieveAccountFailed = "retrieve_account_failed"
	opStatusAccountNotFound       = "account_not_found"
	opStatusRetrieveFailed        = "retrieve_failed"
	opStatusAccountNotActive      = "account_not_active"
	opStatusInvalidAmount         = "invalid_amount"
	opStatusCurrencyMismatch      = "currency_mismatch"
	opStatusSumLiensFailed        = "sum_liens_failed"
	opStatusInsufficientFunds     = "insufficient_funds"
	opStatusDomainError           = "domain_error"
	opStatusSaveFailed            = "save_failed"
	opStatusInvalidLienStatus     = "invalid_lien_status"
	opStatusExecuteFailed         = "execute_failed"
	opStatusWithdrawFailed        = "withdraw_failed"
	opStatusVersionConflict       = "version_conflict"
	opStatusUpdateLienFailed      = "update_lien_failed"
	opStatusSaveAccountFailed     = "save_account_failed"
	opStatusTerminateFailed       = "terminate_failed"
	opStatusUpdateFailed          = "update_failed"
)

// InitiateLien creates a fund reservation on an account
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

	// Convert and validate amount first (before transaction)
	lienAmount, err := s.protoToMoney(req.Amount)
	if err != nil {
		operationStatus = opStatusInvalidAmount
		return nil, status.Errorf(codes.InvalidArgument, "invalid amount: %v", err)
	}

	if !lienAmount.IsPositive() {
		operationStatus = opStatusInvalidAmount
		return nil, status.Error(codes.InvalidArgument, "lien amount must be positive")
	}

	// Prefetch account and balance from Position Keeping BEFORE entering transaction
	// to avoid holding database locks during external service calls (deadlock prevention).
	// We'll re-validate sufficient funds inside the transaction with fresh lien totals.
	//
	// RACE WINDOW NOTE: There's a small window between prefetch and transaction lock where
	// the balance could change. However, this is acceptable because:
	// 1. Position Keeping's ExecuteDebit operation will atomically verify and debit funds
	// 2. Liens are reservations - actual fund movement happens only on ExecuteLien
	// 3. The alternative (external calls inside transaction) risks deadlocks
	// 4. False rejections are recoverable (retry), false acceptances fail at execution time
	// Verify account exists before prefetching balance from Position Keeping
	if _, err := s.repo.FindByID(ctx, req.AccountId); err != nil {
		if errors.Is(err, persistence.ErrAccountNotFound) {
			operationStatus = opStatusAccountNotFound
			return nil, status.Errorf(codes.NotFound, "account not found: %s", req.AccountId)
		}
		operationStatus = opStatusRetrieveFailed
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	// Get bucket_id from request (may be empty for non-bucket-scoped liens)
	bucketID := req.BucketId

	// Prefetch balance from Position Keeping.
	// Note: Currently Position Keeping returns total balance regardless of bucket.
	// Bucket-aware balance tracking will be added in future iterations when Position Keeping
	// supports bucket-scoped balance queries. For now, we validate against total balance
	// but scope lien reservations to buckets.
	prefetchedBalanceCents, err := s.getAccountBalanceCents(ctx, req.AccountId)
	if err != nil {
		operationStatus = opStatusRetrieveFailed
		s.logger.Error("failed to prefetch balance from Position Keeping",
			"account_id", req.AccountId,
			"bucket_id", bucketID,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to retrieve account balance: %v", err)
	}

	// Use a transaction with pessimistic locking to prevent race conditions.
	// Without FOR UPDATE, concurrent InitiateLien calls could both check available
	// balance, see sufficient funds, and both create liens - resulting in over-reservation.
	var lien *domain.Lien
	var account *domain.CurrentAccount
	var availableBalance int64

	txErr := db.WithGormTenantTransaction(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		txRepo := s.repo.WithTx(tx)
		txLienRepo := s.lienRepo.WithTx(tx)

		// Retrieve account with FOR UPDATE lock to prevent concurrent modifications
		var txErr error
		accountResult, txErr := txRepo.FindByIDForUpdate(ctx, req.AccountId)
		if txErr != nil {
			return txErr
		}
		account = &accountResult

		// Validate account is active
		if account.Status() != domain.AccountStatusActive {
			return errTxAccountNotActive
		}

		// Validate currency matches account
		if lienAmount.Currency() != account.Balance().Currency() {
			return errTxCurrencyMismatch
		}

		// Calculate available balance using pre-fetched balance (no external call inside tx).
		// When bucket_id is provided, sum only liens scoped to that bucket for solvency validation.
		// This ensures liens within a bucket don't exceed the bucket's share of funds, providing
		// partial isolation even though Position Keeping doesn't yet support bucket-scoped balances.
		var activeLiensTotal int64
		if bucketID != "" {
			activeLiensTotal, err = txLienRepo.SumActiveAmountByAccountIDAndBucket(ctx, account.ID(), bucketID)
		} else {
			activeLiensTotal, err = txLienRepo.SumActiveAmountByAccountID(ctx, account.ID())
		}
		if err != nil {
			return fmt.Errorf("%w: %v", errTxSumLiensFailed, err) //nolint:errorlint // second error is context-only to preserve errors.Is() for sentinel
		}

		// Available = Pre-fetched Balance - Active Liens (scoped to bucket if bucket_id provided)
		// Balance was fetched from Position Keeping before entering transaction.
		availableBalance = prefetchedBalanceCents - activeLiensTotal

		// Check sufficient funds
		lienCents, err := lienAmount.ToMinorUnits()
		if err != nil {
			return fmt.Errorf("%w: %v", errTxDomainError, err) //nolint:errorlint // second error is context-only to preserve errors.Is() for sentinel
		}
		if lienCents > availableBalance {
			return errTxInsufficientFunds
		}

		// Create lien domain object with bucket awareness
		// bucket_id was extracted from request before entering transaction
		lien, err = domain.NewLien(account.ID(), lienAmount, bucketID, req.PaymentOrderReference, nil)
		if err != nil {
			return fmt.Errorf("%w: %v", errTxDomainError, err) //nolint:errorlint // second error is context-only to preserve errors.Is() for sentinel
		}

		// Persist lien (within the transaction)
		if err := txLienRepo.Create(ctx, lien); err != nil {
			return fmt.Errorf("%w: %v", errTxSaveLien, err) //nolint:errorlint // second error is context-only to preserve errors.Is() for sentinel
		}

		return nil
	})

	// Handle transaction errors with appropriate status codes
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
		"payment_order_ref", req.PaymentOrderReference)

	// Calculate new available balance after this lien
	// ToMinorUnitsUnchecked is safe here: amount was validated in transaction above (line 151)
	newAvailableBalance := availableBalance - lienAmount.ToMinorUnitsUnchecked()
	availableMoney, err := domain.NewMoney(string(account.Balance().Currency()), newAvailableBalance)
	if err != nil {
		// This should never happen if validation passed - log and return without available balance
		s.logger.Error("failed to create available balance money", "error", err)
	}

	return &pb.InitiateLienResponse{
		Lien:             toLienProto(lien),
		AvailableBalance: toMoneyAmount(availableMoney),
	}, nil
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
	prefetchedBalanceCents, err := s.getAccountBalanceCents(ctx, prefetchAccount.AccountID())
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
		"transaction_id", transactionID)

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

// RetrieveLien gets lien details
func (s *Service) RetrieveLien(ctx context.Context, req *pb.RetrieveLienRequest) (*pb.RetrieveLienResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordOperationDuration("retrieve_lien", operationStatus, time.Since(start))
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

	// Retrieve lien (context is passed for organization scoping in multi-org mode)
	lien, err := s.lienRepo.FindByID(ctx, lienID)
	if err != nil {
		if errors.Is(err, persistence.ErrLienNotFound) {
			operationStatus = opStatusLienNotFound
			return nil, status.Errorf(codes.NotFound, "lien not found: %s", req.LienId)
		}
		operationStatus = opStatusRetrieveFailed
		return nil, status.Errorf(codes.Internal, "failed to retrieve lien: %v", err)
	}

	return &pb.RetrieveLienResponse{
		Lien: toLienProto(lien),
	}, nil
}

// Helper functions for lien service

// protoToMoney converts a proto MoneyAmount to domain Money
func (s *Service) protoToMoney(amount *commonpb.MoneyAmount) (domain.Money, error) {
	if amount == nil || amount.Amount == nil {
		return domain.Money{}, ErrAmountRequired
	}

	// Calculate nanosCents first to include in overflow check
	// nanosCents is at most 100 (nanos range 0-999999999, (999999999+5000000)/10000000 = 100)
	nanosCents := (amount.Amount.Nanos + 5000000) / 10000000

	// Validate units won't overflow when multiplied by 100 and added to nanosCents
	// Reserve space for nanosCents (max 100) to prevent overflow in final addition
	if amount.Amount.Units > (math.MaxInt64-100)/100 || amount.Amount.Units < (math.MinInt64+100)/100 {
		return domain.Money{}, ErrAmountOverflow
	}

	// Convert to cents
	// unitsCents is safe due to overflow check above
	unitsCents := amount.Amount.Units * 100
	totalCents := unitsCents + int64(nanosCents)

	return domain.NewMoney(amount.Amount.CurrencyCode, totalCents)
}

// toLienProto converts a domain Lien to proto Lien
func toLienProto(lien *domain.Lien) *pb.Lien {
	return &pb.Lien{
		LienId:                lien.ID.String(),
		AccountId:             lien.AccountID.String(),
		Amount:                toMoneyAmount(lien.Amount),
		Status:                mapLienStatusToProto(lien.Status),
		PaymentOrderReference: lien.PaymentOrderReference,
		CreatedAt:             timestamppb.New(lien.CreatedAt),
		UpdatedAt:             timestamppb.New(lien.UpdatedAt),
		BucketId:              lien.BucketID,
	}
}

// mapLienStatusToProto maps domain LienStatus to proto LienStatus
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

// buildExecuteLienIdempotentResponse builds an idempotent response for an already-executed lien.
func (s *Service) buildExecuteLienIdempotentResponse(ctx context.Context, lien *domain.Lien) (*pb.ExecuteLienResponse, error) {
	account, err := s.repo.FindByUUID(ctx, lien.AccountID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	// Hydrate account with balance from Position Keeping.
	// Balance is no longer persisted - it comes from Position Keeping service.
	account, err = s.hydrateAccountWithBalance(ctx, account)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get account balance: %v", err)
	}

	// Calculate available balance scoped to the lien's bucket (if any)
	availableMoney := s.calculateAvailableBalanceByBucket(ctx, lien.AccountID, lien.BucketID, account.Balance())
	return &pb.ExecuteLienResponse{
		Lien:             toLienProto(lien),
		NewBalance:       toMoneyAmount(account.Balance()),
		AvailableBalance: toMoneyAmount(availableMoney),
		TransactionId:    fmt.Sprintf("TXN-LIEN-%s", lien.ID.String()[:8]),
	}, nil
}

// checkLienIdempotency checks if a lien with the given PaymentOrderReference already exists.
// Returns (response, true) if idempotent response should be returned, (nil, false) otherwise.
// Returns error status if a non-recoverable error occurs.
func (s *Service) checkLienIdempotency(ctx context.Context, paymentOrderRef string) (*pb.InitiateLienResponse, bool, error) {
	if paymentOrderRef == "" {
		return nil, false, nil
	}

	existingLien, err := s.lienRepo.FindByPaymentOrderReference(ctx, paymentOrderRef)
	if err != nil {
		if errors.Is(err, persistence.ErrLienNotFound) {
			return nil, false, nil // Not found - continue with creation
		}
		return nil, false, status.Errorf(codes.Internal, "failed to check idempotency: %v", err)
	}

	// Lien already exists - return idempotent response
	s.logger.Info("lien already exists (idempotent)",
		"lien_id", existingLien.ID.String(),
		"payment_order_ref", paymentOrderRef)

	// Retrieve account for available balance calculation
	account, acctErr := s.repo.FindByUUID(ctx, existingLien.AccountID)
	if acctErr != nil {
		s.logger.Error("failed to retrieve account for idempotent response", "error", acctErr)
		return &pb.InitiateLienResponse{Lien: toLienProto(existingLien)}, true, nil
	}
	// Hydrate account with balance from Position Keeping.
	account, acctErr = s.hydrateAccountWithBalance(ctx, account)
	if acctErr != nil {
		s.logger.Error("failed to get account balance for idempotent response", "error", acctErr)
		return &pb.InitiateLienResponse{Lien: toLienProto(existingLien)}, true, nil
	}
	// Calculate available balance scoped to the lien's bucket (if any)
	availableMoney := s.calculateAvailableBalanceByBucket(ctx, existingLien.AccountID, existingLien.BucketID, account.Balance())
	return &pb.InitiateLienResponse{
		Lien:             toLienProto(existingLien),
		AvailableBalance: toMoneyAmount(availableMoney),
	}, true, nil
}

// hydrateAccountWithBalance returns a new CurrentAccount with balance populated from Position Keeping.
// Position Keeping is the source of truth for account balances - this method MUST be called
// before any operation that requires the current balance.
func (s *Service) hydrateAccountWithBalance(ctx context.Context, account domain.CurrentAccount) (domain.CurrentAccount, error) {
	balanceCents, err := s.getAccountBalanceCents(ctx, account.AccountID())
	if err != nil {
		return domain.CurrentAccount{}, fmt.Errorf("failed to get balance from Position Keeping: %w", err)
	}

	// Create balance Money object
	balance, err := domain.NewMoney(string(account.Balance().Currency()), balanceCents)
	if err != nil {
		return domain.CurrentAccount{}, fmt.Errorf("failed to create balance: %w", err)
	}

	// Calculate available balance: balance + overdraft (if enabled) - active liens
	availableBalance := s.calculateAvailableBalance(ctx, account.ID(), balance)
	if account.OverdraftEnabled() {
		overdraftLimitCents, _ := account.OverdraftLimit().ToMinorUnits()
		if overdraftLimitCents > 0 {
			// Add overdraft to available balance
			availableWithOverdraft, err := availableBalance.Add(account.OverdraftLimit())
			if err == nil {
				availableBalance = availableWithOverdraft
			}
		}
	}

	// Use builder to reconstruct account with new balance
	return domain.NewCurrentAccountBuilder().
		WithID(account.ID()).
		WithAccountID(account.AccountID()).
		WithAccountIdentification(account.AccountIdentification()).
		WithPartyID(account.PartyID()).
		WithBalance(balance).
		WithAvailableBalance(availableBalance).
		WithStatus(account.Status()).
		WithFreezeReason(account.FreezeReason()).
		WithStatusHistory(account.StatusHistory()).
		WithOverdraftLimit(account.OverdraftLimit()).
		WithOverdraftEnabled(account.OverdraftEnabled()).
		WithOverdraftRate(account.OverdraftRate()).
		WithVersion(account.Version()).
		WithCreatedAt(account.CreatedAt()).
		WithUpdatedAt(account.UpdatedAt()).
		Build(), nil
}

// hydrateAccountWithPrefetchedBalance reconstructs account with a pre-fetched balance.
// Use this inside transactions to avoid making external service calls while holding database locks.
// The balanceCents parameter should be fetched from Position Keeping BEFORE entering the transaction.
func (s *Service) hydrateAccountWithPrefetchedBalance(account domain.CurrentAccount, balanceCents int64) (domain.CurrentAccount, error) {
	// Create balance Money object
	balance, err := domain.NewMoney(string(account.Balance().Currency()), balanceCents)
	if err != nil {
		return domain.CurrentAccount{}, fmt.Errorf("failed to create balance: %w", err)
	}

	// For available balance, just use balance (no lien subtraction needed for ExecuteLien
	// since the lien will be executed immediately - the reservation converts to actual debit)
	availableBalance := balance
	if account.OverdraftEnabled() {
		overdraftLimitCents, _ := account.OverdraftLimit().ToMinorUnits()
		if overdraftLimitCents > 0 {
			availableWithOverdraft, err := availableBalance.Add(account.OverdraftLimit())
			if err == nil {
				availableBalance = availableWithOverdraft
			}
		}
	}

	// Use builder to reconstruct account with new balance
	return domain.NewCurrentAccountBuilder().
		WithID(account.ID()).
		WithAccountID(account.AccountID()).
		WithAccountIdentification(account.AccountIdentification()).
		WithPartyID(account.PartyID()).
		WithBalance(balance).
		WithAvailableBalance(availableBalance).
		WithStatus(account.Status()).
		WithFreezeReason(account.FreezeReason()).
		WithStatusHistory(account.StatusHistory()).
		WithOverdraftLimit(account.OverdraftLimit()).
		WithOverdraftEnabled(account.OverdraftEnabled()).
		WithOverdraftRate(account.OverdraftRate()).
		WithVersion(account.Version()).
		WithCreatedAt(account.CreatedAt()).
		WithUpdatedAt(account.UpdatedAt()).
		Build(), nil
}

// currentAccountInstrumentCode is the instrument code used for Current Account balance queries.
// Current Account operates exclusively with GBP currency (CURRENCY dimension).
// The Internal Bank Account service will use different instrument codes for multi-asset support.
const currentAccountInstrumentCode = "GBP"

// getAccountBalanceCents gets the account balance in cents from Position Keeping service.
// Position Keeping is the mandatory source of truth for all account balances.
// Uses the multi-asset API with explicit instrument_code="GBP" for currency operations.
// Returns balance in minor units (cents/pence).
func (s *Service) getAccountBalanceCents(ctx context.Context, accountID string) (int64, error) {
	resp, err := s.posKeepingClient.GetAccountBalance(ctx, &positionkeepingv1.GetAccountBalanceRequest{
		AccountId:      accountID,
		BalanceType:    positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
		InstrumentCode: currentAccountInstrumentCode, // Explicit instrument for multi-asset API
	})
	if err != nil {
		s.logger.Error("failed to get balance from Position Keeping",
			"account_id", accountID, "instrument_code", currentAccountInstrumentCode, "error", err)
		return 0, fmt.Errorf("failed to get balance from Position Keeping: %w", err)
	}

	if resp.Amount == nil || resp.Amount.Amount == "" {
		return 0, nil
	}

	// Validate that the response instrument matches the requested instrument.
	// This guards against configuration mismatches where Position Keeping might
	// return a different currency than expected.
	if resp.Amount.InstrumentCode != currentAccountInstrumentCode {
		s.logger.Error("instrument code mismatch in balance response",
			"account_id", accountID,
			"expected", currentAccountInstrumentCode,
			"received", resp.Amount.InstrumentCode)
		return 0, fmt.Errorf("%w: expected %s, got %s",
			ErrInstrumentCodeMismatch, currentAccountInstrumentCode, resp.Amount.InstrumentCode)
	}

	// Parse InstrumentAmount as decimal
	amount, err := decimal.NewFromString(resp.Amount.Amount)
	if err != nil {
		s.logger.Error("failed to parse balance amount",
			"account_id", accountID, "amount", resp.Amount.Amount, "error", err)
		return 0, fmt.Errorf("failed to parse balance amount: %w", err)
	}

	// Convert to minor units (cents/pence) - multiply by 100 for 2 decimal currencies.
	// Uses banker's rounding (round-to-even) which differs from half-up at .5 boundaries:
	// e.g., 0.015 -> 2 (rounds to even), 0.025 -> 2 (rounds to even), 0.035 -> 4 (rounds to even)
	cents := amount.Mul(decimal.NewFromInt(100)).RoundBank(0)

	// Check for overflow using int64 bounds
	maxInt64 := decimal.NewFromInt(math.MaxInt64)
	minInt64 := decimal.NewFromInt(math.MinInt64)
	if cents.GreaterThan(maxInt64) || cents.LessThan(minInt64) {
		return 0, ErrAmountOverflow
	}

	return cents.IntPart(), nil
}

// calculateAvailableBalance calculates available balance with active liens.
// Logs errors but returns best-effort values since primary operations already succeeded.
// Context is required for organization scoping in multi-org mode.
func (s *Service) calculateAvailableBalance(ctx context.Context, accountID uuid.UUID, currentBalance domain.Money) domain.Money {
	return s.calculateAvailableBalanceByBucket(ctx, accountID, "", currentBalance)
}

// calculateAvailableBalanceByBucket calculates available balance with active liens scoped to bucket.
// If bucketID is empty, calculates against all liens for the account (backward compatible).
// Logs errors but returns best-effort values since primary operations already succeeded.
// Context is required for organization scoping in multi-org mode.
func (s *Service) calculateAvailableBalanceByBucket(ctx context.Context, accountID uuid.UUID, bucketID string, currentBalance domain.Money) domain.Money {
	if s.lienRepo == nil {
		// Lien repository not configured - return balance without lien adjustment
		return currentBalance
	}

	var activeLiensTotal int64
	var err error
	if bucketID != "" {
		activeLiensTotal, err = s.lienRepo.SumActiveAmountByAccountIDAndBucket(ctx, accountID, bucketID)
	} else {
		activeLiensTotal, err = s.lienRepo.SumActiveAmountByAccountID(ctx, accountID)
	}
	if err != nil {
		s.logger.Error("failed to sum active liens for response",
			"account_id", accountID,
			"bucket_id", bucketID,
			"error", err)
		return currentBalance // Best effort: return current balance if liens can't be summed
	}

	currentBalanceCents, err := currentBalance.ToMinorUnits()
	if err != nil {
		s.logger.Error("failed to convert balance to minor units", "error", err)
		return currentBalance // Best effort
	}

	availableBalance := currentBalanceCents - activeLiensTotal
	availableMoney, err := domain.NewMoney(string(currentBalance.Currency()), availableBalance)
	if err != nil {
		s.logger.Error("failed to create available balance for response", "error", err)
		return currentBalance // Best effort
	}
	return availableMoney
}

// GetActiveAmountBlocks retrieves active fund reservations for Position Keeping.
// Returns all ACTIVE (non-expired) liens mapped to AmountBlock representation.
// Used by Position Keeping service to query blocked amounts without coupling to lien details.
func (s *Service) GetActiveAmountBlocks(ctx context.Context, req *pb.GetActiveAmountBlocksRequest) (*pb.GetActiveAmountBlocksResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordOperationDuration("get_active_amount_blocks", operationStatus, time.Since(start))
	}()

	// Validate lien repository is configured
	if s.lienRepo == nil {
		operationStatus = opStatusLienRepoNil
		return nil, status.Error(codes.FailedPrecondition, "lien operations not configured")
	}

	// Retrieve account to validate it exists and get the internal UUID
	account, err := s.repo.FindByID(ctx, req.AccountId)
	if err != nil {
		if errors.Is(err, persistence.ErrAccountNotFound) {
			operationStatus = opStatusAccountNotFound
			return nil, status.Errorf(codes.NotFound, "account not found: %s", req.AccountId)
		}
		operationStatus = opStatusRetrieveFailed
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	// Retrieve active liens for the account
	liens, err := s.lienRepo.FindActiveByAccountID(ctx, account.ID())
	if err != nil {
		operationStatus = opStatusRetrieveFailed
		s.logger.Error("failed to retrieve active liens",
			"account_id", req.AccountId,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to retrieve active liens: %v", err)
	}

	// Convert liens to AmountBlock representation
	blocks := make([]*pb.AmountBlock, 0, len(liens))
	for _, lien := range liens {
		block := toAmountBlockProto(lien)
		blocks = append(blocks, block)
	}

	s.logger.Debug("retrieved active amount blocks",
		"account_id", req.AccountId,
		"block_count", len(blocks))

	return &pb.GetActiveAmountBlocksResponse{
		Blocks: blocks,
	}, nil
}

// toAmountBlockProto converts a domain Lien to proto AmountBlock.
// Liens are mapped to PENDING block type since they represent holds awaiting settlement.
func toAmountBlockProto(lien *domain.Lien) *pb.AmountBlock {
	block := &pb.AmountBlock{
		BlockId:   lien.ID.String(),
		Amount:    toMoneyAmount(lien.Amount),
		BlockType: pb.AmountBlockType_AMOUNT_BLOCK_TYPE_PENDING, // All liens are pending holds
		Purpose:   fmt.Sprintf("Payment Order: %s", lien.PaymentOrderReference),
	}

	// Only set expires_at if the lien has an expiry time
	if lien.ExpiresAt != nil {
		block.ExpiresAt = timestamppb.New(*lien.ExpiresAt)
	}

	return block
}
