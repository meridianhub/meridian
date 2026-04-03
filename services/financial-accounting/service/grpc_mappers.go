package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"

	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	"github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
	"github.com/meridianhub/meridian/services/financial-accounting/domain"
	"github.com/meridianhub/meridian/services/financial-accounting/observability"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/events/topics"
)

// buildBookingLogInitiatedEvent creates a FinancialBookingLogInitiatedEvent from a booking log.
func buildBookingLogInitiatedEvent(bookingLog *domain.FinancialBookingLog, correlationID string) *eventsv1.FinancialBookingLogInitiatedEvent {
	return &eventsv1.FinancialBookingLogInitiatedEvent{
		BookingLogId:            bookingLog.ID.String(),
		FinancialAccountType:    toProtoAccountType(bookingLog.FinancialAccountType),
		ProductServiceReference: bookingLog.ProductServiceReference,
		BusinessUnitReference:   bookingLog.BusinessUnitReference,
		BaseInstrumentCode:      string(bookingLog.BaseCurrency),
		CorrelationId:           correlationID,
		CausationId:             correlationID,
		Timestamp:               timestamppb.Now(),
		Version:                 1,
	}
}

// buildBookingLogUpdatedEvent creates a FinancialBookingLogUpdatedEvent for a status transition.
func buildBookingLogUpdatedEvent(
	bookingLogID uuid.UUID,
	updated *domain.FinancialBookingLog,
	previousStatus, newStatus domain.TransactionStatus,
	correlationID, updatedBy string,
) *eventsv1.FinancialBookingLogUpdatedEvent {
	return &eventsv1.FinancialBookingLogUpdatedEvent{
		BookingLogId:         bookingLogID.String(),
		Status:               toProtoTransactionStatus(newStatus),
		PreviousStatus:       toProtoTransactionStatus(previousStatus),
		ChartOfAccountsRules: updated.ChartOfAccountsRules,
		Reason:               fmt.Sprintf("Status updated from %s to %s", previousStatus, newStatus),
		UpdatedBy:            updatedBy,
		CorrelationId:        correlationID,
		CausationId:          correlationID,
		Timestamp:            timestamppb.Now(),
		Version:              1,
	}
}

// buildPostingCapturedEvent creates a LedgerPostingCapturedEvent from a posting.
func buildPostingCapturedEvent(posting *domain.LedgerPosting, correlationID string) *eventsv1.LedgerPostingCapturedEvent {
	return &eventsv1.LedgerPostingCapturedEvent{
		PostingId:        posting.ID.String(),
		BookingLogId:     posting.FinancialBookingLogID.String(),
		PostingDirection: toProtoPostingDirection(posting.Direction),
		PostingAmount:    toProtoInstrumentAmount(posting.Amount),
		AccountId:        posting.AccountID,
		ValueDate:        timestamppb.New(posting.ValueDate),
		Status:           toProtoTransactionStatus(posting.Status),
		CorrelationId:    correlationID,
		CausationId:      correlationID,
		Timestamp:        timestamppb.Now(),
		Version:          1,
	}
}

// buildPostingAmendedEvent creates a LedgerPostingAmendedEvent for a posting update.
func buildPostingAmendedEvent(
	posting *domain.LedgerPosting,
	previousAmount domain.Money,
	previousStatus, newStatus domain.TransactionStatus,
	correlationID string,
) *eventsv1.LedgerPostingAmendedEvent {
	return &eventsv1.LedgerPostingAmendedEvent{
		PostingId:      posting.ID.String(),
		BookingLogId:   posting.FinancialBookingLogID.String(),
		PreviousAmount: toProtoInstrumentAmount(previousAmount),
		NewAmount:      toProtoInstrumentAmount(posting.Amount),
		Reason:         fmt.Sprintf("Status updated from %v to %v", previousStatus, newStatus),
		AmendedBy:      "system",
		CorrelationId:  correlationID,
		CausationId:    correlationID,
		Timestamp:      timestamppb.Now(),
		Version:        1,
	}
}

// buildBookingLogControlledEvent creates a FinancialBookingLogControlledEvent.
func buildBookingLogControlledEvent(
	bookingLogID uuid.UUID,
	domainAction domain.ControlAction,
	previousStatus, newStatus domain.TransactionStatus,
	reason, controlledBy, correlationID string,
	controlledAt time.Time,
) *eventsv1.FinancialBookingLogControlledEvent {
	return &eventsv1.FinancialBookingLogControlledEvent{
		BookingLogId:   bookingLogID.String(),
		ControlAction:  domainAction.String(),
		PreviousStatus: toProtoTransactionStatus(previousStatus),
		NewStatus:      toProtoTransactionStatus(newStatus),
		Reason:         reason,
		ControlledBy:   controlledBy,
		CorrelationId:  correlationID,
		CausationId:    correlationID,
		Timestamp:      timestamppb.New(controlledAt),
		Version:        1,
	}
}

// controlTransactionResult holds the outputs from the control booking log transaction.
type controlTransactionResult struct {
	PreviousStatus domain.TransactionStatus
	NewStatus      domain.TransactionStatus
	ControlledAt   time.Time
	UpdatedBooking *domain.FinancialBookingLog
}

// reconstructBookingLogFromEntity creates a domain FinancialBookingLog from a persistence entity.
func reconstructBookingLogFromEntity(entity *persistence.FinancialBookingLogEntity) *domain.FinancialBookingLog {
	return &domain.FinancialBookingLog{
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
}

// mapControlDomainError maps domain control errors to gRPC status errors.
func mapControlDomainError(err error, bookingLogID uuid.UUID) error {
	switch {
	case errors.Is(err, persistence.ErrBookingLogNotFound):
		return status.Errorf(codes.NotFound, "financial booking log not found: %s", bookingLogID)
	case errors.Is(err, domain.ErrInvalidControlAction):
		return status.Errorf(codes.InvalidArgument, "invalid control action: %v", err)
	case errors.Is(err, domain.ErrReasonRequired):
		return status.Error(codes.InvalidArgument, "reason is required for control operations")
	case errors.Is(err, domain.ErrCannotSuspendTerminal):
		return status.Error(codes.FailedPrecondition, "cannot suspend booking log in terminal state")
	case errors.Is(err, domain.ErrCannotResumePending):
		return status.Error(codes.FailedPrecondition, "cannot resume booking log that is not suspended")
	case errors.Is(err, domain.ErrCannotTerminateTerminal):
		return status.Error(codes.FailedPrecondition, "cannot terminate booking log already in terminal state")
	default:
		return status.Error(codes.Internal, "failed to apply control operation")
	}
}

// validateDoubleEntryBalance validates that a booking log's postings are balanced for the POSTED transition.
// Returns nil if balanced, or a gRPC status error if not.
func (s *FinancialAccountingService) validateDoubleEntryBalance(ctx context.Context, bookingLogID uuid.UUID) error {
	validationStart := time.Now()
	postings, err := s.repository.GetPostingsByBookingLogID(ctx, bookingLogID)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to retrieve postings for balance validation: %v", err)
	}

	if len(postings) == 0 {
		observability.RecordBalanceValidationDuration(time.Since(validationStart))
		observability.RecordDoubleEntryValidation(observability.ValidationResultUnbalanced, observability.CurrencyUnknown)
		observability.LogBalanceValidationFailure(bookingLogID.String(), observability.CurrencyUnknown, "0", "0", "0")
		return status.Error(codes.FailedPrecondition, "cannot post booking log with no postings")
	}

	debitTotal := decimal.Zero
	creditTotal := decimal.Zero
	var currency string
	for _, posting := range postings {
		if currency == "" {
			currency = posting.Amount.Instrument.Code
		}
		switch posting.Direction {
		case domain.PostingDirectionDebit:
			debitTotal = debitTotal.Add(posting.Amount.Amount)
		case domain.PostingDirectionCredit:
			creditTotal = creditTotal.Add(posting.Amount.Amount)
		}
	}

	observability.RecordBalanceValidationDuration(time.Since(validationStart))

	if !debitTotal.Equal(creditTotal) {
		imbalance := debitTotal.Sub(creditTotal)
		observability.RecordDoubleEntryValidation(observability.ValidationResultUnbalanced, currency)
		observability.LogBalanceValidationFailure(bookingLogID.String(), currency, debitTotal.String(), creditTotal.String(), imbalance.String())
		return status.Error(codes.FailedPrecondition,
			fmt.Sprintf("cannot post unbalanced booking log: debits=%s credits=%s imbalance=%s",
				debitTotal.String(), creditTotal.String(), imbalance.String()))
	}

	observability.RecordDoubleEntryValidation(observability.ValidationResultBalanced, currency)
	return nil
}

// storeIdempotencyResult serializes a proto response and stores it in the idempotency cache.
// Errors are logged but not returned - idempotency storage is best-effort.
func (s *FinancialAccountingService) storeIdempotencyResult(
	ctx context.Context,
	key idempotency.Key,
	ttl time.Duration,
	response proto.Message,
	operation string,
) {
	responseData, marshalErr := proto.Marshal(response)
	if marshalErr != nil {
		slog.Error("failed to serialize response for idempotency cache",
			"error", marshalErr,
			"idempotency_key", key.RequestID,
			"operation", operation)
		return
	}

	result := idempotency.Result{
		Key:         key,
		Status:      idempotency.StatusCompleted,
		Data:        responseData,
		CompletedAt: time.Now(),
		TTL:         ttl,
	}
	if storeErr := s.idempotency.StoreResult(ctx, result); storeErr != nil {
		slog.Error("failed to store idempotency result",
			"error", storeErr,
			"idempotency_key", key.RequestID,
			"operation", operation)
	}
}

// idempotencyTTLFromKey returns the TTL from an idempotency key, falling back to the default.
func idempotencyTTLFromKey(ttlSeconds int32) time.Duration {
	if ttlSeconds > 0 {
		return time.Duration(ttlSeconds) * time.Second
	}
	return defaultIdempotencyTTL
}

// marshalForCache serializes a proto message for idempotency caching (best-effort).
// Returns nil on marshal failure rather than propagating the error.
func marshalForCache(msg proto.Message, idempotencyKey, operation string) []byte {
	data, err := proto.Marshal(msg)
	if err != nil {
		slog.Error("failed to serialize response for idempotency cache",
			"error", err,
			"idempotency_key", idempotencyKey,
			"operation", operation)
		return nil
	}
	return data
}

// mapIdempotencyExecutorError maps errors from the idempotency executor to gRPC status errors.
func mapIdempotencyExecutorError(err error) error {
	if errors.Is(err, idempotency.ErrOperationInProgress) {
		return status.Error(codes.Aborted, "operation already in progress")
	}
	var execErr *idempotency.ExecutorError
	if errors.As(err, &execErr) {
		return status.Errorf(codes.Internal, "idempotency error: %v", err)
	}
	// Business logic errors pass through directly (already gRPC status errors)
	return err
}

// handleCachedUpdateBookingLogResponse deserializes a cached UpdateFinancialBookingLogResponse.
func handleCachedUpdateBookingLogResponse(data []byte, idempotencyKey, entityID string) (*financialaccountingv1.UpdateFinancialBookingLogResponse, error) {
	if len(data) > 0 {
		var cached financialaccountingv1.UpdateFinancialBookingLogResponse
		if err := proto.Unmarshal(data, &cached); err != nil {
			slog.Error("failed to deserialize cached idempotency response",
				"error", err,
				"idempotency_key", idempotencyKey,
				"operation", "update-booking-log")
			return nil, status.Error(codes.AlreadyExists, "request with this idempotency key already processed")
		}
		slog.Info("returning cached idempotent response",
			"idempotency_key", idempotencyKey,
			"operation", "update-booking-log",
			"booking_log_id", entityID)
		return &cached, nil
	}
	return nil, status.Error(codes.AlreadyExists, "request with this idempotency key already processed")
}

// handleCachedUpdatePostingResponse deserializes a cached UpdateLedgerPostingResponse.
func handleCachedUpdatePostingResponse(data []byte, idempotencyKey, entityID string) (*financialaccountingv1.UpdateLedgerPostingResponse, error) {
	if len(data) > 0 {
		var cached financialaccountingv1.UpdateLedgerPostingResponse
		if err := proto.Unmarshal(data, &cached); err != nil {
			slog.Error("failed to deserialize cached idempotency response",
				"error", err,
				"idempotency_key", idempotencyKey,
				"operation", "update-posting")
			return nil, status.Error(codes.AlreadyExists, "request with this idempotency key already processed")
		}
		slog.Info("returning cached idempotent response",
			"idempotency_key", idempotencyKey,
			"operation", "update-posting",
			"posting_id", entityID)
		return &cached, nil
	}
	return nil, status.Error(codes.AlreadyExists, "request with this idempotency key already processed")
}

// publishControlEventsInTx writes control events to the outbox within a transaction.
func (s *FinancialAccountingService) publishControlEventsInTx(
	ctx context.Context,
	tx *gorm.DB,
	controlEvent *eventsv1.FinancialBookingLogControlledEvent,
	bookingLogID uuid.UUID,
	correlationID string,
) error {
	if err := s.outboxPublisher.PublishControlEvent(
		ctx, tx, controlEvent,
		"financial_accounting.booking_log_controlled.v1",
		bookingLogID.String(), "FinancialBookingLog",
		topics.FinancialAccountingBookingLogControlledV1, correlationID,
	); err != nil {
		return fmt.Errorf("failed to write event to outbox: %w", err)
	}

	//nolint:staticcheck // SA1019: intentional use of deprecated topic for dual-publish
	legacyTopic := topics.FinancialAccountingBookingLogControlled
	if err := s.outboxPublisher.PublishControlEvent(
		ctx, tx, controlEvent,
		"financial_accounting.booking_log_controlled.v1",
		bookingLogID.String(), "FinancialBookingLog",
		legacyTopic, correlationID,
	); err != nil {
		return fmt.Errorf("failed to write legacy event to outbox: %w", err)
	}

	return nil
}

// toProtoMoney converts domain Money to protobuf google.type.Money.
// Used for event publishing where the events proto still uses google.type.Money.
// This will be removed when events are migrated to InstrumentAmount.
func toProtoMoney(m domain.Money) *money.Money {
	amount := m.Amount
	units := amount.IntPart()
	fraction := amount.Sub(amount.Truncate(0))
	nanos := fraction.Mul(decimal.NewFromInt(1_000_000_000)).IntPart()

	if nanos > 999_999_999 {
		nanos = 999_999_999
	} else if nanos < -999_999_999 {
		nanos = -999_999_999
	}

	return &money.Money{
		CurrencyCode: m.Instrument.Code,
		Units:        units,
		Nanos:        int32(nanos), // #nosec G115 -- Safely clamped to int32 range above
	}
}

// applyPostingStatusTransition applies a status transition to a ledger posting using domain methods.
func applyPostingStatusTransition(posting *domain.LedgerPosting, newStatus domain.TransactionStatus, postingResult string) error {
	switch newStatus {
	case domain.TransactionStatusPosted:
		if err := posting.Post(postingResult); err != nil {
			if errors.Is(err, domain.ErrAlreadyPosted) {
				return status.Error(codes.FailedPrecondition, "posting already posted")
			}
			return status.Errorf(codes.InvalidArgument, "cannot post: %v", err)
		}
	case domain.TransactionStatusFailed:
		if err := posting.Fail(postingResult); err != nil {
			if errors.Is(err, domain.ErrCannotFailPosted) {
				return status.Error(codes.FailedPrecondition, "cannot fail a posted transaction")
			}
			return status.Errorf(codes.InvalidArgument, "cannot fail: %v", err)
		}
	case domain.TransactionStatusPending:
		if posting.Status == domain.TransactionStatusPosted {
			return status.Error(codes.FailedPrecondition, "cannot revert a posted posting to pending")
		}
		posting.Status = newStatus
		if postingResult != "" {
			posting.PostingResult = postingResult
		}
	case domain.TransactionStatusCancelled:
		if posting.Status == domain.TransactionStatusPosted {
			return status.Error(codes.FailedPrecondition, "cannot cancel a posted posting")
		}
		posting.Status = newStatus
		if postingResult != "" {
			posting.PostingResult = postingResult
		}
	case domain.TransactionStatusReversed:
		if posting.Status != domain.TransactionStatusPosted {
			return status.Error(codes.FailedPrecondition, "can only reverse a posted posting")
		}
		posting.Status = newStatus
		if postingResult != "" {
			posting.PostingResult = postingResult
		}
	default:
		return status.Errorf(codes.InvalidArgument, "unsupported status: %v", newStatus)
	}
	return nil
}
