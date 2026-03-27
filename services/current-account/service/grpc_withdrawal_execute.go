package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/domain"
	caobservability "github.com/meridianhub/meridian/services/current-account/observability"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/events/topics"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// ExecuteWithdrawal processes a withdrawal transaction with Redis-based idempotency protection.
//
// This method supports two modes:
//  1. Direct withdrawal: Provide account_id and amount for immediate execution
//  2. Execute pending withdrawal: Provide withdrawal_id to execute a previously initiated withdrawal
//
// Concurrency: This method relies on optimistic locking in the repository layer
// to handle concurrent modifications to the same account. If two requests attempt
// to modify the same account simultaneously, one will succeed and the other will
// receive ErrVersionConflict, which surfaces as an Internal error to the client.
// Redis-based idempotency provides request deduplication for retried requests.
func (s *Service) ExecuteWithdrawal(ctx context.Context, req *pb.ExecuteWithdrawalRequest) (*pb.ExecuteWithdrawalResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordOperationDuration("execute_withdrawal", operationStatus, time.Since(start))
	}()

	// Resolve withdrawal source - either pending withdrawal lookup or direct params
	accountID, reqAmount, pendingWithdrawal, opStatus, err := s.resolveWithdrawalSource(ctx, req)
	if err != nil {
		operationStatus = opStatus
		return nil, err
	}

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
				"account_id", accountID)
		}
		idempKey = idempotency.Key{
			TenantID:  string(tenantID),
			Namespace: idempotencyNamespace,
			Operation: "withdrawal",
			EntityID:  accountID,
			RequestID: idempotencyKey,
		}

		// Check Redis for existing result
		result, err := s.idempotencyService.Check(ctx, idempKey)
		if errors.Is(err, idempotency.ErrOperationAlreadyProcessed) && result != nil && result.Data != nil {
			var cachedResp pb.ExecuteWithdrawalResponse
			unmarshalErr := proto.Unmarshal(result.Data, &cachedResp)
			if unmarshalErr == nil {
				s.logger.Info("returning cached withdrawal response from Redis",
					"account_id", accountID,
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
			if errors.Is(err, idempotency.ErrOperationAlreadyProcessed) {
				s.logger.Info("operation already in progress, please retry",
					"idempotency_key", idempotencyKey)
				return nil, status.Error(codes.Aborted, "operation already in progress, please retry")
			}
			s.logger.Error("failed to mark operation pending", "error", err)
			return nil, status.Error(codes.Aborted, "failed to acquire idempotency lock, please retry")
		}
		idempotencyLockAcquired = true

		// Cleanup pending state on failure
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

	// Retrieve account (context carries organization for multi-tenant routing)
	account, err := s.repo.FindByID(ctx, accountID)
	if err != nil {
		if errors.Is(err, persistence.ErrAccountNotFound) {
			operationStatus = opStatusAccountNotFound
			return nil, status.Errorf(codes.NotFound, "account not found: %s", accountID)
		}
		operationStatus = opStatusRetrieveFailed
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	// Hydrate account with balance from Position Keeping (balance no longer persisted locally)
	account, err = s.hydrateAccountWithBalance(ctx, account)
	if err != nil {
		operationStatus = opStatusRetrieveFailed
		s.logger.Error("failed to hydrate account balance from Position Keeping",
			"account_id", accountID,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to retrieve account balance: %v", err)
	}

	// Check account status - cannot withdraw from frozen or closed accounts
	if account.Status() == domain.AccountStatusFrozen {
		operationStatus = opStatusAccountFrozen
		return nil, status.Errorf(codes.FailedPrecondition, "cannot withdraw from frozen account: %s", accountID)
	}
	if account.Status() == domain.AccountStatusClosed {
		operationStatus = opStatusAccountClosed
		return nil, status.Errorf(codes.FailedPrecondition, "cannot withdraw from closed account: %s", accountID)
	}

	// Validate and convert amount
	amount, opStatus, amountErr := validateWithdrawalAmount(reqAmount, account)
	if amountErr != nil {
		operationStatus = opStatus
		return nil, amountErr
	}

	amountCents, _ := amount.ToMinorUnits()

	// Generate transaction ID (full UUID required by position-keeping service)
	transactionID := uuid.New().String()

	// Prepare account for debit transaction (validates status, funds, increments version)
	account, err = account.PrepareForDebit(amount)
	if err != nil {
		if errors.Is(err, domain.ErrInsufficientFunds) {
			operationStatus = opStatusInsufficientFunds
			availCents, _ := account.AvailableBalance().ToMinorUnits()
			return nil, status.Errorf(codes.FailedPrecondition,
				"insufficient funds: requested %d cents, available %d cents", amountCents, availCents)
		}
		if errors.Is(err, domain.ErrAccountFrozen) {
			operationStatus = opStatusAccountFrozen
			return nil, status.Errorf(codes.FailedPrecondition, "account is frozen")
		}
		if errors.Is(err, domain.ErrAccountClosed) {
			operationStatus = opStatusAccountClosed
			return nil, status.Errorf(codes.FailedPrecondition, "account is closed")
		}
		operationStatus = opStatusWithdrawalFailed
		return nil, status.Errorf(codes.InvalidArgument, "withdrawal failed: %v", err)
	}

	// Orchestrate transaction with saga pattern - Position Keeping is the source of truth for balance
	resp, err := s.withdrawalOrchestrator.Orchestrate(ctx, account, amount, transactionID, req.Attributes)
	if err != nil {
		operationStatus = opStatusSagaFailed
		return nil, err
	}

	// Record withdrawal transaction (the withdrawal itself succeeded regardless of balance fetch)
	caobservability.RecordWithdrawal(amount.InstrumentCode())

	// After saga completes, query Position Keeping for the new balance
	account, err = s.hydrateAccountWithBalance(ctx, account)
	if err != nil {
		s.logger.Error("failed to retrieve updated balance from Position Keeping after withdrawal",
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

	// Mark pending withdrawal as completed (if executing a pending withdrawal)
	// Uses transactional outbox pattern to ensure atomicity between status update and event publication.
	// If the outbox write fails, the withdrawal status update is rolled back, ensuring consistency.
	if pendingWithdrawal != nil && s.withdrawalRepo != nil {
		// Use internal UUID from withdrawal (not the business account ID which is like "ACC-xxxx")
		accountUUID := pendingWithdrawal.AccountID
		if err := s.completeWithdrawalWithOutbox(ctx, pendingWithdrawal, accountUUID); err != nil {
			// CRITICAL: Outbox write failed but funds already moved. Must not leave withdrawal PENDING
			// as that would allow re-execution. Fall back to direct status update without outbox.
			s.logger.Error("outbox withdrawal completion failed, attempting fallback direct update",
				"withdrawal_id", pendingWithdrawal.Reference,
				"account_id", accountUUID,
				"outbox_error", err)

			// Fallback: Mark withdrawal completed directly (idempotent, safe to retry)
			if fallbackErr := pendingWithdrawal.Complete(); fallbackErr != nil {
				s.logger.Error("fallback withdrawal completion also failed - withdrawal stuck in PENDING",
					"withdrawal_id", pendingWithdrawal.Reference,
					"fallback_error", fallbackErr,
					"original_error", err)
				// Don't fail the RPC - funds already moved, but log critical issue
			} else if fallbackErr := s.withdrawalRepo.Update(ctx, pendingWithdrawal); fallbackErr != nil {
				s.logger.Error("fallback withdrawal persistence failed - withdrawal stuck in PENDING",
					"withdrawal_id", pendingWithdrawal.Reference,
					"fallback_error", fallbackErr,
					"original_error", err)
			} else {
				s.logger.Warn("withdrawal marked completed via fallback (outbox events lost)",
					"withdrawal_id", pendingWithdrawal.Reference)
			}
		}
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

// completeWithdrawalWithOutbox atomically updates withdrawal status and writes status change event to outbox.
// This ensures that the withdrawal status update and event publication happen in the same transaction,
// providing at-least-once delivery guarantees for withdrawal completion events.
func (s *Service) completeWithdrawalWithOutbox(ctx context.Context, withdrawal *domain.Withdrawal, accountID uuid.UUID) error {
	// If outbox repo is not configured, fall back to direct update (graceful degradation)
	if s.outboxRepo == nil || s.db == nil {
		if err := withdrawal.Complete(); err != nil {
			return fmt.Errorf("failed to transition withdrawal to completed status: %w", err)
		}
		if err := s.withdrawalRepo.Update(ctx, withdrawal); err != nil {
			return fmt.Errorf("failed to persist withdrawal completion: %w", err)
		}
		return nil
	}

	// Create typed protobuf event for publication
	now := time.Now().UTC()
	event := &eventsv1.WithdrawalStatusUpdatedEvent{
		EventId:       uuid.New().String(),
		WithdrawalId:  withdrawal.Reference,
		AccountId:     accountID.String(),
		Status:        "COMPLETED",
		CorrelationId: uuid.New().String(),
		CausationId:   withdrawal.Reference,
		Timestamp:     timestamppb.New(now),
		Version:       int64(withdrawal.Version),
	}

	// Marshal event payload as protobuf
	eventPayload, err := proto.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal withdrawal status event: %w", err)
	}

	// Use transaction to ensure atomicity between withdrawal update and outbox entry
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		// Update withdrawal status within transaction
		if err := withdrawal.Complete(); err != nil {
			return fmt.Errorf("failed to transition withdrawal to completed status: %w", err)
		}

		// Save withdrawal state using transactional repository
		withdrawalRepoTx := s.withdrawalRepo.WithTx(tx)
		if err := withdrawalRepoTx.Update(ctx, withdrawal); err != nil {
			return fmt.Errorf("failed to persist withdrawal completion: %w", err)
		}

		// Create outbox entry within the same transaction (new canonical topic)
		outboxEntry := &events.EventOutbox{
			ID:            uuid.New(),
			EventType:     "WithdrawalStatusUpdated",
			AggregateID:   withdrawal.Reference,
			AggregateType: "Withdrawal",
			EventPayload:  eventPayload,
			Status:        events.StatusPending,
			Topic:         topics.CurrentAccountWithdrawalStatusV1,
			PartitionKey:  accountID.String(),
			CreatedAt:     time.Now(),
			RetryCount:    0,
			ServiceName:   "current-account",
		}

		// Insert outbox entry within the transaction
		if err := s.outboxRepo.Insert(ctx, tx, outboxEntry); err != nil {
			return fmt.Errorf("failed to create outbox entry: %w", err)
		}

		return nil
	}); err != nil {
		return err
	}

	// Best-effort: insert deprecated outbox entry outside the main transaction
	// so a failure does not abort the committed canonical entry (CockroachDB
	// aborts the entire transaction on any statement error).
	deprecatedEntry := &events.EventOutbox{
		ID:            uuid.New(),
		EventType:     "WithdrawalStatusUpdated",
		AggregateID:   withdrawal.Reference,
		AggregateType: "Withdrawal",
		EventPayload:  eventPayload,
		Status:        events.StatusPending,
		Topic:         "current-account.withdrawal.status",
		PartitionKey:  accountID.String(),
		CreatedAt:     time.Now(),
		RetryCount:    0,
		ServiceName:   "current-account",
	}

	if err := s.outboxRepo.Insert(ctx, s.db, deprecatedEntry); err != nil {
		s.logger.Warn("failed to create deprecated outbox entry (continuing)",
			"topic", deprecatedEntry.Topic,
			"error", err,
		)
	}

	return nil
}
