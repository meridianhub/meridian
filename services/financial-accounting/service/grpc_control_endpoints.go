package service

import (
	"context"
	"errors"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	"github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
	"github.com/meridianhub/meridian/services/financial-accounting/domain"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/auth"
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

	// Check idempotency with cached response support
	if s.idempotency != nil {
		cachedResp, err := s.checkControlIdempotency(ctx, idempotencyKey, req.IdempotencyKey.Key, req.GetId())
		if cachedResp != nil || err != nil {
			return cachedResp, err
		}
	}

	// Parse booking log ID
	bookingLogID, err := parseUUID(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid booking log id: %v", err)
	}

	// Validate control action and reason
	domainAction, err := validateControlRequest(req)
	if err != nil {
		return nil, err
	}

	// Execute control operation
	correlationID := req.IdempotencyKey.Key
	txResult, err := s.executeControlTransaction(ctx, bookingLogID, domainAction, req.Reason, correlationID)
	if err != nil {
		return nil, mapControlDomainError(err, bookingLogID)
	}

	slog.Info("control operation applied successfully",
		"booking_log_id", bookingLogID.String(),
		"control_action", domainAction.String(),
		"previous_status", txResult.PreviousStatus.String(),
		"new_status", txResult.NewStatus.String(),
		"reason", req.Reason)

	response := &financialaccountingv1.ControlFinancialBookingLogResponse{
		FinancialBookingLog: toProtoFinancialBookingLog(txResult.UpdatedBooking),
	}

	// Store idempotency result
	if s.idempotency != nil {
		ttl := idempotencyTTLFromKey(req.IdempotencyKey.TtlSeconds)
		s.storeIdempotencyResult(ctx, idempotencyKey, ttl, response, "control-booking-log")
	}

	return response, nil
}

// checkControlIdempotency checks if a control operation was already processed
// and returns the cached response if available.
func (s *FinancialAccountingService) checkControlIdempotency(
	ctx context.Context,
	key idempotency.Key,
	idempotencyKeyStr, entityID string,
) (*financialaccountingv1.ControlFinancialBookingLogResponse, error) {
	result, err := s.idempotency.Check(ctx, key)
	if err != nil && !errors.Is(err, idempotency.ErrResultNotFound) {
		if errors.Is(err, idempotency.ErrOperationAlreadyProcessed) {
			if result != nil && result.Status == idempotency.StatusCompleted && len(result.Data) > 0 {
				var cachedResponse financialaccountingv1.ControlFinancialBookingLogResponse
				if unmarshalErr := proto.Unmarshal(result.Data, &cachedResponse); unmarshalErr != nil {
					slog.Error("failed to deserialize cached idempotency response",
						"error", unmarshalErr,
						"idempotency_key", idempotencyKeyStr,
						"operation", "control-booking-log")
					return nil, status.Error(codes.AlreadyExists, "request with this idempotency key already processed")
				}
				slog.Info("returning cached idempotent response",
					"idempotency_key", idempotencyKeyStr,
					"operation", "control-booking-log",
					"booking_log_id", entityID)
				return &cachedResponse, nil
			}
			return nil, status.Error(codes.AlreadyExists, "request with this idempotency key already processed")
		}
		return nil, status.Errorf(codes.Internal, "failed to check idempotency: %v", err)
	}

	if err := s.idempotency.MarkPending(ctx, key, defaultIdempotencyTTL); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to mark operation as pending: %v", err)
	}
	return nil, nil
}

// executeControlTransaction executes the control action within a database transaction.
func (s *FinancialAccountingService) executeControlTransaction(
	ctx context.Context,
	bookingLogID [16]byte,
	domainAction domain.ControlAction,
	reason, correlationID string,
) (*controlTransactionResult, error) {
	var result controlTransactionResult

	err := s.repository.WithTransaction(ctx, func(tx *gorm.DB) error {
		var entity persistence.FinancialBookingLogEntity
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&entity, "id = ?", bookingLogID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return persistence.ErrBookingLogNotFound
			}
			return err
		}

		bookingLog := reconstructBookingLogFromEntity(&entity)
		result.PreviousStatus = bookingLog.Status

		updated, err := bookingLog.ControlLog(domainAction, reason)
		if err != nil {
			return err
		}

		entity.Status = string(updated.Status)
		entity.UpdatedAt = updated.UpdatedAt
		if err := tx.Save(&entity).Error; err != nil {
			return err
		}

		result.NewStatus = updated.Status
		result.ControlledAt = updated.UpdatedAt
		result.UpdatedBooking = &updated

		controlEvent := buildBookingLogControlledEvent(
			bookingLogID, domainAction, result.PreviousStatus, result.NewStatus,
			reason, extractUserFromContext(ctx), correlationID, result.ControlledAt,
		)
		return s.publishControlEventsInTx(ctx, tx, controlEvent, bookingLogID, correlationID)
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
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
