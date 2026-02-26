package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/domain"
	caobservability "github.com/meridianhub/meridian/services/current-account/observability"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
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
		// Validate reason length (domain layer also validates, but we provide clearer error message)
		if len(req.Reason) < 10 {
			operationStatus = "invalid_freeze_reason"
			return nil, status.Errorf(codes.InvalidArgument, "freeze reason must be at least 10 characters, got %d", len(req.Reason))
		}

		account, err = account.Freeze(req.Reason)
		if err != nil {
			if errors.Is(err, domain.ErrInvalidStatusTransition) {
				operationStatus = opStatusInvalidStatusTransition
				return nil, status.Errorf(codes.FailedPrecondition, "cannot freeze account in status %s: only ACTIVE accounts can be frozen", account.Status())
			}
			if errors.Is(err, domain.ErrInvalidFreezeReason) {
				operationStatus = "invalid_freeze_reason"
				return nil, status.Errorf(codes.InvalidArgument, "freeze reason must be at least 10 characters")
			}
			operationStatus = "freeze_failed"
			return nil, status.Errorf(codes.Internal, "failed to freeze account: %v", err)
		}

		s.logger.Info("account frozen",
			"account_id", req.AccountId,
			"reason", req.Reason)

	case pb.ControlAction_CONTROL_ACTION_UNFREEZE:
		account, err = account.Unfreeze()
		if err != nil {
			if errors.Is(err, domain.ErrInvalidStatusTransition) {
				operationStatus = opStatusInvalidStatusTransition
				return nil, status.Errorf(codes.FailedPrecondition, "cannot unfreeze account in status %s: only FROZEN accounts can be unfrozen", account.Status())
			}
			operationStatus = "unfreeze_failed"
			return nil, status.Errorf(codes.Internal, "failed to unfreeze account: %v", err)
		}

		s.logger.Info("account unfrozen",
			"account_id", req.AccountId)

	case pb.ControlAction_CONTROL_ACTION_CLOSE:
		// Hydrate account with balance from Position Keeping before close validation
		account, err = s.hydrateAccountWithBalance(ctx, account)
		if err != nil {
			operationStatus = opStatusRetrieveFailed
			s.logger.Error("failed to hydrate account balance from Position Keeping for close validation",
				"account_id", req.AccountId,
				"error", err)
			return nil, status.Errorf(codes.Internal, "failed to retrieve account balance: %v", err)
		}

		// Validate balance is zero before attempting close
		if !account.Balance().IsZero() {
			operationStatus = "non_zero_balance"
			balanceCents, _ := account.Balance().ToMinorUnits()
			return nil, status.Errorf(codes.FailedPrecondition, "cannot close account with non-zero balance: %d cents", balanceCents)
		}

		// Check for active liens (requires lienRepo)
		if s.lienRepo != nil {
			activeLienCount, err := s.lienRepo.CountActiveByAccountID(ctx, account.ID())
			if err != nil {
				operationStatus = "lien_check_failed"
				s.logger.Error("failed to check active liens for account close",
					"account_id", req.AccountId,
					"error", err)
				return nil, status.Errorf(codes.Internal, "failed to check active liens: %v", err)
			}
			if activeLienCount > 0 {
				operationStatus = "active_liens_exist"
				return nil, status.Errorf(codes.FailedPrecondition, "cannot close account with %d active liens", activeLienCount)
			}
		}

		// Attempt to close via domain (pass reason for audit trail)
		account, err = account.Close(req.Reason)
		if err != nil {
			if errors.Is(err, domain.ErrInvalidStatusTransition) {
				operationStatus = opStatusInvalidStatusTransition
				return nil, status.Errorf(codes.FailedPrecondition, "cannot close account in status %s: account is already closed", account.Status())
			}
			if errors.Is(err, domain.ErrNonZeroBalance) {
				operationStatus = "non_zero_balance"
				return nil, status.Errorf(codes.FailedPrecondition, "cannot close account with non-zero balance")
			}
			operationStatus = "close_failed"
			return nil, status.Errorf(codes.Internal, "failed to close account: %v", err)
		}

		s.logger.Info("account closed",
			"account_id", req.AccountId,
			"reason", req.Reason)

	case pb.ControlAction_CONTROL_ACTION_UNSPECIFIED:
		operationStatus = "unspecified_action"
		return nil, status.Error(codes.InvalidArgument, "control_action is required and cannot be UNSPECIFIED")

	default:
		operationStatus = "unknown_action"
		return nil, status.Errorf(codes.InvalidArgument, "unknown control action: %v", req.ControlAction)
	}

	// Persist with optimistic locking
	if err := s.repo.Save(ctx, account); err != nil {
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

	// Emit Kafka events for account lifecycle changes (fire-and-forget pattern)
	// Event publishing errors are logged but don't fail the operation to ensure
	// the business operation completes successfully regardless of messaging issues.
	s.publishControlActionEvent(ctx, req, &account, actionTimestamp)

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

// publishControlActionEvent emits lifecycle events to Kafka based on the control action.
// This method uses fire-and-forget semantics - errors are logged but don't fail the operation.
// The account balance and reason information is captured from the domain object and request.
func (s *Service) publishControlActionEvent(
	ctx context.Context,
	req *pb.ControlCurrentAccountRequest,
	account *domain.CurrentAccount,
	actionTimestamp time.Time,
) {
	// Skip if event publisher is not configured
	if s.eventPublisher == nil {
		return
	}

	// Extract actor identity from auth context (falls back to "system" if not available)
	actorID := "system"
	if userID, ok := auth.GetUserIDFromContext(ctx); ok && userID != "" {
		actorID = userID
	}

	// Generate correlation ID for event tracing
	correlationID := uuid.New().String()

	// Generate event timestamp
	now := time.Now().UTC()

	// Use AccountID() which returns the business account ID as string
	accountID := account.AccountID()

	switch req.ControlAction {
	case pb.ControlAction_CONTROL_ACTION_FREEZE:
		event := &eventsv1.AccountFrozenEvent{
			EventId:       uuid.New().String(),
			AccountId:     accountID,
			Reason:        req.Reason,
			FrozenAt:      timestamppb.New(actionTimestamp),
			FrozenBy:      actorID,
			CorrelationId: correlationID,
			CausationId:   correlationID,
			Timestamp:     timestamppb.New(now),
			Version:       account.Version(),
		}
		if err := s.eventPublisher.PublishWithTenant(ctx, TopicAccountFrozen, accountID, event); err != nil {
			s.logger.Error("failed to publish account frozen event",
				"account_id", accountID,
				"error", err)
		} else {
			s.logger.Debug("published account frozen event",
				"account_id", accountID,
				"event_id", event.EventId,
				"correlation_id", correlationID)
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
		if err := s.eventPublisher.PublishWithTenant(ctx, TopicAccountUnfrozen, accountID, event); err != nil {
			s.logger.Error("failed to publish account unfrozen event",
				"account_id", accountID,
				"error", err)
		} else {
			s.logger.Debug("published account unfrozen event",
				"account_id", accountID,
				"event_id", event.EventId,
				"correlation_id", correlationID)
		}

	case pb.ControlAction_CONTROL_ACTION_CLOSE:
		// Convert domain balance to google.type.Money
		balanceCents, _ := account.Balance().ToMinorUnits()
		closingBalance := &money.Money{
			CurrencyCode: account.Balance().InstrumentCode(),
			Units:        balanceCents / 100,
			Nanos:        int32((balanceCents % 100) * 10000000),
		}

		event := &eventsv1.AccountClosedEvent{
			EventId:        uuid.New().String(),
			AccountId:      accountID,
			ClosingBalance: closingBalance,
			ClosureReason:  req.Reason,
			ClosedBy:       actorID,
			ClosureDate:    timestamppb.New(actionTimestamp),
			CorrelationId:  correlationID,
			CausationId:    correlationID,
			Timestamp:      timestamppb.New(now),
			Version:        account.Version(),
		}
		if err := s.eventPublisher.PublishWithTenant(ctx, TopicAccountClosed, accountID, event); err != nil {
			s.logger.Error("failed to publish account closed event",
				"account_id", accountID,
				"error", err)
		} else {
			s.logger.Debug("published account closed event",
				"account_id", accountID,
				"event_id", event.EventId,
				"correlation_id", correlationID)
		}

	case pb.ControlAction_CONTROL_ACTION_UNSPECIFIED:
		// No event for unspecified action (validation catches this earlier)
	}
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
