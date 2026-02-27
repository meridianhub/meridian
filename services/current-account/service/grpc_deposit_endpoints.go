package service

import (
	"context"
	"errors"
	"time"

	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/domain"
	caobservability "github.com/meridianhub/meridian/services/current-account/observability"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/tenant"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// ExecuteDeposit processes a deposit transaction with Redis-based idempotency protection.
//
// Concurrency: This method relies on optimistic locking in the repository layer
// to handle concurrent modifications to the same account. If two requests attempt
// to modify the same account simultaneously, one will succeed and the other will
// receive ErrVersionConflict, which surfaces as an Internal error to the client.
// Redis-based idempotency provides request deduplication for retried requests.
func (s *Service) ExecuteDeposit(ctx context.Context, req *pb.ExecuteDepositRequest) (*pb.ExecuteDepositResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordOperationDuration("execute_deposit", operationStatus, time.Since(start))
	}()

	// Get idempotency key if provided
	var idempotencyKey string
	if req.IdempotencyKey != nil && req.IdempotencyKey.Key != "" {
		idempotencyKey = req.IdempotencyKey.Key
	}

	// Build idempotency key structure for Redis
	var idempKey idempotency.Key
	var idempotencyLockAcquired bool
	if idempotencyKey != "" && s.idempotencyService != nil {
		tenantID, ok := tenant.FromContext(ctx)
		if !ok {
			s.logger.Debug("tenant not found in context for idempotency key",
				"account_id", req.AccountId)
		}
		idempKey = idempotency.Key{
			TenantID:  string(tenantID),
			Namespace: idempotencyNamespace,
			Operation: "deposit",
			EntityID:  req.AccountId,
			RequestID: idempotencyKey,
		}

		// Check Redis for existing result
		result, err := s.idempotencyService.Check(ctx, idempKey)
		if errors.Is(err, idempotency.ErrOperationAlreadyProcessed) && result != nil && result.Data != nil {
			var cachedResp pb.ExecuteDepositResponse
			unmarshalErr := proto.Unmarshal(result.Data, &cachedResp)
			if unmarshalErr == nil {
				s.logger.Info("returning cached deposit response from Redis",
					"account_id", req.AccountId,
					"transaction_id", cachedResp.TransactionId,
					"idempotency_key", idempotencyKey)
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
					"idempotency_key", idempotencyKey)
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
						"idempotency_key", idempotencyKey)
				}
			}
		}()
	}

	// Validate amount is present before account fetch to fail fast on malformed requests.
	if req.Amount == nil || req.Amount.Amount == nil {
		operationStatus = opStatusMissingAmount
		return nil, status.Error(codes.InvalidArgument, "amount is required")
	}

	// Retrieve account (context carries organization for multi-tenant routing)
	account, err := s.repo.FindByID(ctx, req.AccountId)
	if err != nil {
		if errors.Is(err, persistence.ErrAccountNotFound) {
			operationStatus = opStatusAccountNotFound
			return nil, status.Errorf(codes.NotFound, "account not found: %s", req.AccountId)
		}
		operationStatus = opStatusRetrieveFailed
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	// Validate currency matches account currency
	if req.Amount.Amount.CurrencyCode != account.Balance().InstrumentCode() {
		operationStatus = opStatusCurrencyMismatch
		return nil, status.Errorf(codes.InvalidArgument,
			"currency mismatch: expected %s, got %s",
			account.Balance().InstrumentCode(), req.Amount.Amount.CurrencyCode)
	}

	// Convert amount from proto (MoneyAmount wraps google.type.Money) using the account's instrument.
	// The account's instrument determines dimension and precision; the proto CurrencyCode is already
	// validated against account.Balance().InstrumentCode() above.
	amount, err := protoMoneyToAmount(req.Amount, account)
	if err != nil {
		operationStatus = opStatusInvalidAmount
		return nil, status.Errorf(codes.InvalidArgument, "invalid amount: %v", err)
	}

	// Validate amount is positive
	amountCents, err := amount.ToMinorUnits()
	if err != nil {
		operationStatus = opStatusAmountOverflow
		return nil, status.Errorf(codes.InvalidArgument,
			"deposit amount overflow: %v", err)
	}
	if amountCents <= 0 {
		operationStatus = opStatusInvalidAmount
		return nil, status.Errorf(codes.InvalidArgument,
			"deposit amount must be positive, got %d minor units", amountCents)
	}

	// Generate transaction ID (full UUID required by position-keeping service)
	transactionID := uuid.New().String()

	// Prepare account for credit transaction (validates status, increments version for optimistic locking)
	account, err = account.PrepareForCredit()
	if err != nil {
		operationStatus = "deposit_failed"
		if errors.Is(err, domain.ErrAccountFrozen) || errors.Is(err, domain.ErrAccountClosed) {
			return nil, status.Errorf(codes.FailedPrecondition, "deposit failed: %v", err)
		}
		return nil, status.Errorf(codes.InvalidArgument, "deposit failed: %v", err)
	}

	// Orchestrate transaction with saga pattern - Position Keeping is the source of truth for balance
	resp, err := s.depositOrchestrator.Orchestrate(ctx, account, amount, transactionID, req.Attributes, req.ClearingAccountId)
	if err != nil {
		operationStatus = opStatusSagaFailed
		return nil, err
	}

	// Record deposit transaction (the deposit itself succeeded regardless of balance fetch)
	caobservability.RecordDeposit(amount.InstrumentCode())

	// After saga completes, query Position Keeping for the new balance
	account, err = s.hydrateAccountWithBalance(ctx, account)
	if err != nil {
		s.logger.Error("failed to retrieve updated balance from Position Keeping after deposit",
			"account_id", account.AccountID(),
			"transaction_id", transactionID,
			"error", err)
		// Transaction succeeded but balance fetch failed - leave balance fields nil
		// Client should call RetrieveCurrentAccount to get accurate balance
	} else {
		// Update response with balance from Position Keeping
		resp.NewBalance = toMoneyAmount(account.Balance())
		resp.AvailableBalance = toMoneyAmount(account.AvailableBalance())
		// Record balance gauge only when we have accurate post-transaction balance
		caobservability.RecordBalance(safeMinorUnits(account.Balance()), account.InstrumentCode())
	}

	// Store successful result in Redis for future idempotency checks
	if idempotencyKey != "" && s.idempotencyService != nil {
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
