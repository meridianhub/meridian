package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	"github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
	"github.com/meridianhub/meridian/services/financial-accounting/domain"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/events/topics"
)

// ControlFinancialBookingLog applies a control action (SUSPEND, RESUME, TERMINATE) to a booking log.
//
// This method implements the BIAN CoCR (Control Correlation Reference) pattern for
// administrative control operations. It uses the transactional outbox pattern to ensure
// atomic persistence of both the state change and the corresponding event.
//
// Control Actions:
//   - SUSPEND: Temporarily suspends processing (PENDING -> FAILED/suspended)
//   - RESUME: Reactivates a suspended booking log (FAILED/suspended -> PENDING)
//   - TERMINATE: Permanently ends the booking log lifecycle (PENDING/FAILED -> CANCELLED)
//
// Transactional Guarantees:
//   - State change and event are written in the same database transaction
//   - Either both succeed or both are rolled back
//   - Background worker publishes events to Kafka for at-least-once delivery
//
// Idempotency:
//   - Operations are idempotent via the provided idempotency key
//   - Duplicate requests with the same key return the original result
//
// gRPC Error Codes:
//   - codes.InvalidArgument: Invalid booking log ID, control action, or missing reason
//   - codes.NotFound: Booking log does not exist
//   - codes.FailedPrecondition: Control action not allowed for current state
//   - codes.AlreadyExists: Duplicate idempotency key
//   - codes.Internal: Database or system errors
func (s *FinancialAccountingService) ControlFinancialBookingLog(
	ctx context.Context,
	req *financialaccountingv1.ControlFinancialBookingLogRequest,
) (*financialaccountingv1.ControlFinancialBookingLogResponse, error) {
	// Validate idempotency key is provided
	if req.IdempotencyKey == nil || req.IdempotencyKey.Key == "" {
		return nil, status.Error(codes.InvalidArgument, "idempotency_key is required")
	}

	idempotencyKey := idempotency.Key{
		Namespace: "financial-accounting",
		Operation: "control-booking-log",
		EntityID:  req.GetId(),
		RequestID: req.IdempotencyKey.Key,
	}

	// Check idempotency
	if s.idempotency != nil {
		result, err := s.idempotency.Check(ctx, idempotencyKey)
		if err != nil && !errors.Is(err, idempotency.ErrResultNotFound) {
			if errors.Is(err, idempotency.ErrOperationAlreadyProcessed) {
				if result != nil && result.Status == idempotency.StatusCompleted && len(result.Data) > 0 {
					// Deserialize cached response from protobuf
					var cachedResponse financialaccountingv1.ControlFinancialBookingLogResponse
					if unmarshalErr := proto.Unmarshal(result.Data, &cachedResponse); unmarshalErr != nil {
						slog.Error("failed to deserialize cached idempotency response",
							"error", unmarshalErr,
							"idempotency_key", req.IdempotencyKey.Key,
							"operation", "control-booking-log")
						return nil, status.Error(codes.AlreadyExists, "request with this idempotency key already processed")
					}
					slog.Info("returning cached idempotent response",
						"idempotency_key", req.IdempotencyKey.Key,
						"operation", "control-booking-log",
						"booking_log_id", req.GetId())
					return &cachedResponse, nil
				}
				return nil, status.Error(codes.AlreadyExists, "request with this idempotency key already processed")
			}
			return nil, status.Errorf(codes.Internal, "failed to check idempotency: %v", err)
		}

		// Mark as pending to prevent concurrent processing
		if err := s.idempotency.MarkPending(ctx, idempotencyKey, defaultIdempotencyTTL); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to mark operation as pending: %v", err)
		}
	}

	// Parse booking log ID
	bookingLogID, err := parseUUID(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid booking log id: %v", err)
	}

	// Validate control action
	if req.ControlAction == financialaccountingv1.ControlAction_CONTROL_ACTION_UNSPECIFIED {
		return nil, status.Error(codes.InvalidArgument, "control_action must be specified")
	}

	// Convert proto control action to domain
	var domainAction domain.ControlAction
	switch req.ControlAction {
	case financialaccountingv1.ControlAction_CONTROL_ACTION_SUSPEND:
		domainAction = domain.ControlActionSuspend
	case financialaccountingv1.ControlAction_CONTROL_ACTION_RESUME:
		domainAction = domain.ControlActionResume
	case financialaccountingv1.ControlAction_CONTROL_ACTION_TERMINATE:
		domainAction = domain.ControlActionTerminate
	case financialaccountingv1.ControlAction_CONTROL_ACTION_UNSPECIFIED:
		// Already handled above, but included for exhaustive switch linter
		return nil, status.Error(codes.InvalidArgument, "control_action must be specified")
	}

	// Validate reason is provided
	if req.Reason == "" {
		return nil, status.Error(codes.InvalidArgument, "reason is required for control operations")
	}

	// Extract correlation ID
	correlationID := req.IdempotencyKey.Key

	// Variables to capture results from transaction
	var previousStatus domain.TransactionStatus
	var newStatus domain.TransactionStatus
	var controlledAt time.Time
	var updatedBookingLog *domain.FinancialBookingLog

	// Execute state change and event write atomically using transactional outbox pattern
	err = s.repository.WithTransaction(ctx, func(tx *gorm.DB) error {
		// 1. Retrieve and lock booking log entity within transaction (FOR UPDATE)
		var entity persistence.FinancialBookingLogEntity
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&entity, "id = ?", bookingLogID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return persistence.ErrBookingLogNotFound
			}
			return err
		}

		// 2. Reconstruct domain model from locked entity
		bookingLog := &domain.FinancialBookingLog{
			ID:                      entity.ID,
			FinancialAccountType:    entity.FinancialAccountType,
			ProductServiceReference: entity.ProductServiceReference,
			BusinessUnitReference:   entity.BusinessUnitReference,
			ChartOfAccountsRules:    entity.ChartOfAccountsRules,
			BaseCurrency:            domain.Currency(entity.BaseCurrency),
			Status:                  domain.TransactionStatus(entity.Status),
			CreatedAt:               entity.CreatedAt,
			UpdatedAt:               entity.UpdatedAt,
		}

		// 3. Capture previous status for event
		previousStatus = bookingLog.Status

		// 4. Apply control action using domain method
		updated, err := bookingLog.ControlLog(domainAction, req.Reason)
		if err != nil {
			return err // Will be handled by error mapping below
		}

		// 5. Apply domain updates to locked entity
		entity.Status = string(updated.Status)
		entity.UpdatedAt = updated.UpdatedAt
		if err := tx.Save(&entity).Error; err != nil {
			return err
		}

		// 6. Capture results for response and event
		newStatus = updated.Status
		controlledAt = updated.UpdatedAt
		updatedBookingLog = &updated

		// 7. Build and write control event to outbox within the same transaction
		controlEvent := &eventsv1.FinancialBookingLogControlledEvent{
			BookingLogId:   bookingLogID.String(),
			ControlAction:  domainAction.String(),
			PreviousStatus: toProtoTransactionStatus(previousStatus),
			NewStatus:      toProtoTransactionStatus(newStatus),
			Reason:         req.Reason,
			ControlledBy:   extractUserFromContext(ctx),
			CorrelationId:  correlationID,
			CausationId:    correlationID,
			Timestamp:      timestamppb.New(controlledAt),
			Version:        1,
		}

		// Publish to canonical v1 topic
		if err := s.outboxPublisher.PublishControlEvent(
			ctx,
			tx,
			controlEvent,
			"financial_accounting.booking_log_controlled.v1",
			bookingLogID.String(),
			"FinancialBookingLog",
			topics.FinancialAccountingBookingLogControlledV1,
			correlationID,
		); err != nil {
			return fmt.Errorf("failed to write event to outbox: %w", err)
		}

		// Dual-publish to legacy topic for backwards compatibility during migration.
		//nolint:staticcheck // SA1019: intentional use of deprecated topic for dual-publish
		legacyTopic := topics.FinancialAccountingBookingLogControlled
		if err := s.outboxPublisher.PublishControlEvent(
			ctx,
			tx,
			controlEvent,
			"financial_accounting.booking_log_controlled.v1",
			bookingLogID.String(),
			"FinancialBookingLog",
			legacyTopic,
			correlationID,
		); err != nil {
			return fmt.Errorf("failed to write legacy event to outbox: %w", err)
		}

		return nil
	})
	if err != nil {
		// Map domain errors to gRPC status codes
		switch {
		case errors.Is(err, persistence.ErrBookingLogNotFound):
			return nil, status.Errorf(codes.NotFound, "financial booking log not found: %s", bookingLogID)
		case errors.Is(err, domain.ErrInvalidControlAction):
			return nil, status.Errorf(codes.InvalidArgument, "invalid control action: %v", err)
		case errors.Is(err, domain.ErrReasonRequired):
			return nil, status.Error(codes.InvalidArgument, "reason is required for control operations")
		case errors.Is(err, domain.ErrCannotSuspendTerminal):
			return nil, status.Error(codes.FailedPrecondition, "cannot suspend booking log in terminal state")
		case errors.Is(err, domain.ErrCannotResumePending):
			return nil, status.Error(codes.FailedPrecondition, "cannot resume booking log that is not suspended")
		case errors.Is(err, domain.ErrCannotTerminateTerminal):
			return nil, status.Error(codes.FailedPrecondition, "cannot terminate booking log already in terminal state")
		default:
			return nil, status.Errorf(codes.Internal, "failed to apply control operation: %v", err)
		}
	}

	// Log success
	slog.Info("control operation applied successfully",
		"booking_log_id", bookingLogID.String(),
		"control_action", domainAction.String(),
		"previous_status", previousStatus.String(),
		"new_status", newStatus.String(),
		"reason", req.Reason)

	// Convert to proto response
	response := &financialaccountingv1.ControlFinancialBookingLogResponse{
		FinancialBookingLog: toProtoFinancialBookingLog(updatedBookingLog),
	}

	// Store idempotency result
	if s.idempotency != nil {
		ttl := defaultIdempotencyTTL
		if req.IdempotencyKey.TtlSeconds > 0 {
			ttl = time.Duration(req.IdempotencyKey.TtlSeconds) * time.Second
		}

		responseData, marshalErr := proto.Marshal(response)
		if marshalErr != nil {
			slog.Error("failed to serialize response for idempotency cache",
				"error", marshalErr,
				"idempotency_key", req.IdempotencyKey.Key,
				"operation", "control-booking-log")
		} else {
			result := idempotency.Result{
				Key:         idempotencyKey,
				Status:      idempotency.StatusCompleted,
				Data:        responseData,
				CompletedAt: time.Now(),
				TTL:         ttl,
			}

			if storeErr := s.idempotency.StoreResult(ctx, result); storeErr != nil {
				slog.Error("failed to store idempotency result",
					"error", storeErr,
					"idempotency_key", req.IdempotencyKey.Key,
					"operation", "control-booking-log")
			}
		}
	}

	return response, nil
}

// extractUserFromContext extracts the user ID from the authentication context.
// If the auth context is present and contains a valid user ID, it returns that user ID.
// Otherwise, it falls back to "system" for operations without authentication context
// (e.g., system-initiated operations, background jobs, or legacy code paths).
func extractUserFromContext(ctx context.Context) string {
	if userID, ok := auth.GetUserIDFromContext(ctx); ok && userID != "" {
		return userID
	}
	return "system"
}
