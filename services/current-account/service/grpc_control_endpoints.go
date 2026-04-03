package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/domain"
	caobservability "github.com/meridianhub/meridian/services/current-account/observability"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/events/topics"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// ControlCurrentAccount performs lifecycle state transitions on an account.
// BIAN: Control Control Record (CoCR) - Freeze, Unfreeze, or Close accounts.
// All control actions are logged with timestamps and reasons for audit compliance.
//
// State transitions:
//   - FREEZE: ACTIVE -> FROZEN (requires reason of at least 10 characters)
//   - UNFREEZE: FROZEN -> ACTIVE
//   - CLOSE: ACTIVE or FROZEN -> CLOSED (requires zero balance and no active liens)
func (s *Service) ControlCurrentAccount(ctx context.Context, req *pb.ControlCurrentAccountRequest) (*pb.ControlCurrentAccountResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordOperationDuration("control_account", operationStatus, time.Since(start))
	}()

	// Validate required fields
	if req.AccountId == "" {
		operationStatus = opStatusMissingAccountID
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}

	// Retrieve account
	account, err := s.repo.FindByID(ctx, req.AccountId)
	if err != nil {
		if errors.Is(err, persistence.ErrAccountNotFound) {
			operationStatus = opStatusAccountNotFound
			return nil, status.Errorf(codes.NotFound, "account not found: %s", req.AccountId)
		}
		operationStatus = opStatusRetrieveFailed
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	actionTimestamp := time.Now()

	// Apply control action based on the action type
	switch req.ControlAction {
	case pb.ControlAction_CONTROL_ACTION_FREEZE:
		account, operationStatus, err = s.applyFreezeAction(req, account)
		if err != nil {
			return nil, err
		}

	case pb.ControlAction_CONTROL_ACTION_UNFREEZE:
		account, operationStatus, err = s.applyUnfreezeAction(req, account)
		if err != nil {
			return nil, err
		}

	case pb.ControlAction_CONTROL_ACTION_CLOSE:
		account, operationStatus, err = s.applyCloseAction(ctx, req, account)
		if err != nil {
			return nil, err
		}

	case pb.ControlAction_CONTROL_ACTION_UNSPECIFIED:
		operationStatus = "unspecified_action"
		return nil, status.Error(codes.InvalidArgument, "control_action is required and cannot be UNSPECIFIED")

	default:
		operationStatus = "unknown_action"
		return nil, status.Errorf(codes.InvalidArgument, "unknown control action: %v", req.ControlAction)
	}

	// Atomically persist account state change and write event to outbox.
	// Using transactional outbox ensures guaranteed at-least-once event delivery
	// without the fire-and-forget reliability issues of direct Kafka publishing.
	if err := s.saveWithOutboxEvent(ctx, req, &account, actionTimestamp); err != nil {
		if errors.Is(err, persistence.ErrVersionConflict) {
			operationStatus = "version_conflict"
			s.logger.Warn("version conflict during control action",
				"account_id", req.AccountId,
				"action", req.ControlAction.String())
			return nil, status.Errorf(codes.Aborted, "version conflict: account was modified by another transaction, please retry")
		}
		operationStatus = opStatusSaveFailed
		return nil, status.Errorf(codes.Internal, "failed to save account: %v", err)
	}

	// Emit webhook notifications for FREEZE and CLOSE actions (regulatory compliance)
	// Webhooks are sent asynchronously with retry logic - errors are logged but don't fail the operation.
	// Note: UNFREEZE does not require webhook notification per regulatory requirements.
	s.sendControlActionWebhook(ctx, req, &account, actionTimestamp)

	s.logger.Info("control action executed successfully",
		"account_id", req.AccountId,
		"action", req.ControlAction.String(),
		"new_status", account.Status(),
		"new_version", account.Version())

	return &pb.ControlCurrentAccountResponse{
		Facility:        toProtoFacility(account),
		ActionTimestamp: timestamppb.New(actionTimestamp),
	}, nil
}

// applyFreezeAction validates and applies the FREEZE action to an account.
func (s *Service) applyFreezeAction(req *pb.ControlCurrentAccountRequest, account domain.CurrentAccount) (domain.CurrentAccount, string, error) {
	// Validate reason length (domain layer also validates, but we provide clearer error message)
	if len(req.Reason) < 10 {
		return account, "invalid_freeze_reason",
			status.Errorf(codes.InvalidArgument, "freeze reason must be at least 10 characters, got %d", len(req.Reason))
	}

	frozen, err := account.Freeze(req.Reason)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidStatusTransition) {
			return account, opStatusInvalidStatusTransition,
				status.Errorf(codes.FailedPrecondition, "cannot freeze account in status %s: only ACTIVE accounts can be frozen", account.Status())
		}
		if errors.Is(err, domain.ErrInvalidFreezeReason) {
			return account, "invalid_freeze_reason",
				status.Errorf(codes.InvalidArgument, "freeze reason must be at least 10 characters")
		}
		return account, "freeze_failed",
			status.Errorf(codes.Internal, "failed to freeze account: %v", err)
	}

	s.logger.Info("account frozen",
		"account_id", req.AccountId,
		"reason", req.Reason)

	return frozen, operationStatusSuccess, nil
}

// applyUnfreezeAction validates and applies the UNFREEZE action to an account.
func (s *Service) applyUnfreezeAction(req *pb.ControlCurrentAccountRequest, account domain.CurrentAccount) (domain.CurrentAccount, string, error) {
	unfrozen, err := account.Unfreeze()
	if err != nil {
		if errors.Is(err, domain.ErrInvalidStatusTransition) {
			return account, opStatusInvalidStatusTransition,
				status.Errorf(codes.FailedPrecondition, "cannot unfreeze account in status %s: only FROZEN accounts can be unfrozen", account.Status())
		}
		return account, "unfreeze_failed",
			status.Errorf(codes.Internal, "failed to unfreeze account: %v", err)
	}

	s.logger.Info("account unfrozen",
		"account_id", req.AccountId)

	return unfrozen, operationStatusSuccess, nil
}

// applyCloseAction validates and applies the CLOSE action to an account.
func (s *Service) applyCloseAction(ctx context.Context, req *pb.ControlCurrentAccountRequest, account domain.CurrentAccount) (domain.CurrentAccount, string, error) {
	// Hydrate account with balance from Position Keeping before close validation
	account, err := s.hydrateAccountWithBalance(ctx, account)
	if err != nil {
		s.logger.Error("failed to hydrate account balance from Position Keeping for close validation",
			"account_id", req.AccountId,
			"error", err)
		return account, opStatusRetrieveFailed,
			status.Errorf(codes.Internal, "failed to retrieve account balance: %v", err)
	}

	// Validate balance is zero before attempting close
	if !account.Balance().IsZero() {
		balanceCents, _ := account.Balance().ToMinorUnits()
		return account, "non_zero_balance",
			status.Errorf(codes.FailedPrecondition, "cannot close account with non-zero balance: %d cents", balanceCents)
	}

	// Check for active liens (requires lienRepo)
	if s.lienRepo != nil {
		if opStatus, err := s.validateNoActiveLiens(ctx, req.AccountId, account); err != nil {
			return account, opStatus, err
		}
	}

	// Attempt to close via domain (pass reason for audit trail)
	closed, err := account.Close(req.Reason)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidStatusTransition) {
			return account, opStatusInvalidStatusTransition,
				status.Errorf(codes.FailedPrecondition, "cannot close account in status %s: account is already closed", account.Status())
		}
		if errors.Is(err, domain.ErrNonZeroBalance) {
			return account, "non_zero_balance",
				status.Errorf(codes.FailedPrecondition, "cannot close account with non-zero balance")
		}
		return account, "close_failed",
			status.Errorf(codes.Internal, "failed to close account: %v", err)
	}

	s.logger.Info("account closed",
		"account_id", req.AccountId,
		"reason", req.Reason)

	return closed, operationStatusSuccess, nil
}

// validateNoActiveLiens checks that no active liens exist on the account.
func (s *Service) validateNoActiveLiens(ctx context.Context, accountID string, account domain.CurrentAccount) (string, error) {
	activeLienCount, err := s.lienRepo.CountActiveByAccountID(ctx, account.ID())
	if err != nil {
		s.logger.Error("failed to check active liens for account close",
			"account_id", accountID,
			"error", err)
		return "lien_check_failed",
			status.Errorf(codes.Internal, "failed to check active liens: %v", err)
	}
	if activeLienCount > 0 {
		return "active_liens_exist",
			status.Errorf(codes.FailedPrecondition, "cannot close account with %d active liens", activeLienCount)
	}
	return "", nil
}

// saveWithOutboxEvent atomically persists the account state change and writes the
// corresponding lifecycle event to the outbox within a single database transaction.
// This replaces the previous fire-and-forget PublishWithTenant pattern, guaranteeing
// at-least-once event delivery via the background outbox worker.
//
// If the outbox publisher is not configured (e.g., in tests), falls back to repo.Save only.
func (s *Service) saveWithOutboxEvent(
	ctx context.Context,
	req *pb.ControlCurrentAccountRequest,
	account *domain.CurrentAccount,
	actionTimestamp time.Time,
) error {
	// Fall back to direct save if outbox is not available (e.g., unit tests)
	if s.outboxPublisher == nil || s.db == nil {
		return s.repo.Save(ctx, *account)
	}

	// Extract actor identity from auth context (falls back to "system" if not available)
	actorID := "system"
	if userID, ok := auth.GetUserIDFromContext(ctx); ok && userID != "" {
		actorID = userID
	}

	correlationID := uuid.New().String()
	now := time.Now().UTC()
	accountID := account.AccountID()

	return s.db.Transaction(func(tx *gorm.DB) error {
		// Save the account state using a transaction-scoped repository
		txRepo := s.repo.WithTx(tx)
		if err := txRepo.Save(ctx, *account); err != nil {
			return err
		}

		// Publish the lifecycle event to the outbox within the same transaction
		return s.publishControlEvent(ctx, tx, req.ControlAction, account, accountID, req.Reason, actorID, correlationID, actionTimestamp, now)
	})
}

// publishControlEvent publishes a lifecycle event to the outbox for the given control action.
func (s *Service) publishControlEvent(
	ctx context.Context,
	tx *gorm.DB,
	action pb.ControlAction,
	account *domain.CurrentAccount,
	accountID, reason, actorID, correlationID string,
	actionTimestamp, now time.Time,
) error {
	switch action {
	case pb.ControlAction_CONTROL_ACTION_FREEZE:
		event := &eventsv1.AccountFrozenEvent{
			EventId:       uuid.New().String(),
			AccountId:     accountID,
			Reason:        reason,
			FrozenAt:      timestamppb.New(actionTimestamp),
			FrozenBy:      actorID,
			CorrelationId: correlationID,
			CausationId:   correlationID,
			Timestamp:     timestamppb.New(now),
			Version:       account.Version(),
		}
		if err := s.outboxPublisher.Publish(ctx, tx, event, events.PublishConfig{
			EventType:     "current_account.account_frozen.v1",
			Topic:         topics.CurrentAccountAccountFrozenV1,
			AggregateType: "CurrentAccount",
			AggregateID:   accountID,
			CorrelationID: correlationID,
			CausationID:   correlationID,
		}); err != nil {
			return fmt.Errorf("failed to write account frozen event to outbox: %w", err)
		}

	case pb.ControlAction_CONTROL_ACTION_UNFREEZE:
		event := &eventsv1.AccountUnfrozenEvent{
			EventId:       uuid.New().String(),
			AccountId:     accountID,
			UnfrozenAt:    timestamppb.New(actionTimestamp),
			UnfrozenBy:    actorID,
			CorrelationId: correlationID,
			CausationId:   correlationID,
			Timestamp:     timestamppb.New(now),
			Version:       account.Version(),
		}
		if err := s.outboxPublisher.Publish(ctx, tx, event, events.PublishConfig{
			EventType:     "current_account.account_unfrozen.v1",
			Topic:         topics.CurrentAccountAccountUnfrozenV1,
			AggregateType: "CurrentAccount",
			AggregateID:   accountID,
			CorrelationID: correlationID,
			CausationID:   correlationID,
		}); err != nil {
			return fmt.Errorf("failed to write account unfrozen event to outbox: %w", err)
		}

	case pb.ControlAction_CONTROL_ACTION_CLOSE:
		if err := s.publishAccountClosedEvent(ctx, tx, account, accountID, reason, actorID, correlationID, actionTimestamp, now); err != nil {
			return err
		}

	case pb.ControlAction_CONTROL_ACTION_UNSPECIFIED:
		// No event for unspecified action
	}

	return nil
}

// publishAccountClosedEvent builds and publishes an account closed event to the outbox.
func (s *Service) publishAccountClosedEvent(
	ctx context.Context,
	tx *gorm.DB,
	account *domain.CurrentAccount,
	accountID, reason, actorID, correlationID string,
	actionTimestamp, now time.Time,
) error {
	closingBalance := &quantityv1.InstrumentAmount{
		Amount:         account.Balance().Amount().String(),
		InstrumentCode: account.Balance().InstrumentCode(),
		Version:        1,
	}
	event := &eventsv1.AccountClosedEvent{
		EventId:        uuid.New().String(),
		AccountId:      accountID,
		ClosingBalance: closingBalance,
		ClosureReason:  reason,
		ClosedBy:       actorID,
		ClosureDate:    timestamppb.New(actionTimestamp),
		CorrelationId:  correlationID,
		CausationId:    correlationID,
		Timestamp:      timestamppb.New(now),
		Version:        account.Version(),
	}
	if err := s.outboxPublisher.Publish(ctx, tx, event, events.PublishConfig{
		EventType:     "current_account.account_closed.v1",
		Topic:         topics.CurrentAccountAccountClosedV1,
		AggregateType: "CurrentAccount",
		AggregateID:   accountID,
		CorrelationID: correlationID,
		CausationID:   correlationID,
	}); err != nil {
		return fmt.Errorf("failed to write account closed event to outbox: %w", err)
	}
	return nil
}

// sendControlActionWebhook sends webhook notifications for regulatory compliance events.
// This method uses fire-and-forget semantics with async delivery - errors are logged but don't fail the operation.
// Only FREEZE and CLOSE actions trigger webhooks per regulatory requirements (UNFREEZE does not).
func (s *Service) sendControlActionWebhook(
	ctx context.Context,
	req *pb.ControlCurrentAccountRequest,
	account *domain.CurrentAccount,
	actionTimestamp time.Time,
) {
	// Skip if webhook notifier is not configured
	if s.webhookNotifier == nil {
		return
	}

	// Extract tenant ID from context
	tenantID, ok := tenant.FromContext(ctx)
	if !ok || tenantID.String() == "" {
		s.logger.Warn("cannot send webhook: no tenant ID in context",
			"account_id", req.AccountId,
			"action", req.ControlAction.String())
		return
	}

	// Use AccountID() which returns the business account ID as string
	accountID := account.AccountID()

	switch req.ControlAction {
	case pb.ControlAction_CONTROL_ACTION_FREEZE:
		// Send webhook notification asynchronously (fire-and-forget)
		// Using background context intentionally - webhook delivery must complete
		// even if the original request context is cancelled
		//nolint:contextcheck // Intentionally using background context for async webhook delivery
		go s.sendFreezeWebhook(tenantID.String(), accountID, req.Reason, actionTimestamp)

	case pb.ControlAction_CONTROL_ACTION_CLOSE:
		// Capture balance info for the webhook payload
		balanceCents, _ := account.Balance().ToMinorUnits()
		balanceInfo := &WebhookBalanceInfo{
			Amount:       balanceCents,
			CurrencyCode: account.Balance().InstrumentCode(),
		}

		// Send webhook notification asynchronously (fire-and-forget)
		// Using background context intentionally - webhook delivery must complete
		// even if the original request context is cancelled
		//nolint:contextcheck // Intentionally using background context for async webhook delivery
		go s.sendCloseWebhook(tenantID.String(), accountID, req.Reason, balanceInfo, actionTimestamp)

	case pb.ControlAction_CONTROL_ACTION_UNFREEZE:
		// No webhook for unfreeze action per regulatory requirements
		s.logger.Debug("skipping webhook for unfreeze action (not required)",
			"account_id", accountID)

	case pb.ControlAction_CONTROL_ACTION_UNSPECIFIED:
		// No webhook for unspecified action (validation catches this earlier)
	}
}

// sendFreezeWebhook sends a webhook notification for an account freeze event.
// This is a helper method to avoid inline goroutines which cause contextcheck linter issues.
// Uses background context intentionally to ensure delivery continues after request completes.
func (s *Service) sendFreezeWebhook(tenantID, accountID, reason string, timestamp time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := s.webhookNotifier.NotifyAccountFrozen(ctx, tenantID, accountID, reason, timestamp); err != nil {
		s.logger.Error("failed to send account frozen webhook",
			"account_id", accountID,
			"tenant_id", tenantID,
			"error", err)
	} else {
		s.logger.Debug("account frozen webhook sent successfully",
			"account_id", accountID,
			"tenant_id", tenantID)
	}
}

// sendCloseWebhook sends a webhook notification for an account close event.
// This is a helper method to avoid inline goroutines which cause contextcheck linter issues.
// Uses background context intentionally to ensure delivery continues after request completes.
func (s *Service) sendCloseWebhook(tenantID, accountID, reason string, balance *WebhookBalanceInfo, timestamp time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := s.webhookNotifier.NotifyAccountClosed(ctx, tenantID, accountID, reason, balance, timestamp); err != nil {
		s.logger.Error("failed to send account closed webhook",
			"account_id", accountID,
			"tenant_id", tenantID,
			"error", err)
	} else {
		s.logger.Debug("account closed webhook sent successfully",
			"account_id", accountID,
			"tenant_id", tenantID)
	}
}
