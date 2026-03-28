package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	"github.com/meridianhub/meridian/services/payment-order/adapters/persistence"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	poobservability "github.com/meridianhub/meridian/services/payment-order/observability"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// failPaymentOrder handles payment order failure with proper state transition and event publishing.
// Returns an error if the state transition or persistence fails. Callers in synchronous paths
// (e.g., UpdatePaymentOrder) should propagate this error to clients. Callers in async paths
// (e.g., saga orchestration) may log and swallow the error.
//
// Compensation logic:
// - Reverses ledger entries if LedgerBookingID exists (defensive - normally empty on failure)
// - Releases lien if payment was in RESERVED or EXECUTING state
func (s *Service) failPaymentOrder(ctx context.Context, po *domain.PaymentOrder, reason string, errorCode string) error {
	// Capture original status before transitioning (for event)
	failedAtStatus := po.Status

	// Check if lien needs to be released before transitioning
	needsLienRelease := po.RequiresLienRelease()
	lienID := po.LienID
	originalLedgerBookingID := po.LedgerBookingID

	// Reverse ledger posting if it exists (defensive - normally empty before COMPLETED)
	// This handles edge cases where ledger posting succeeded but state transition failed
	if originalLedgerBookingID != "" {
		_, err := s.reverseLedgerPosting(ctx, po, fmt.Sprintf("Payment failure: %s", reason))
		if err != nil {
			// Log but continue - ledger reversal failure shouldn't block payment failure
			// The orphaned booking log will be flagged for reconciliation
			s.logger.Error("failed to reverse ledger posting on payment failure",
				"error", err,
				"payment_order_id", po.ID.String(),
				"original_ledger_booking_id", originalLedgerBookingID)
		}
	}

	// Transition to FAILED
	if err := po.Fail(reason, errorCode); err != nil {
		s.logger.Error("failed to transition to FAILED state",
			"error", err,
			"payment_order_id", po.ID.String())
		return fmt.Errorf("failed to transition to FAILED state: %w", err)
	}

	if err := s.repo.Update(ctx, po); err != nil {
		s.logger.Error("failed to persist FAILED state",
			"error", err,
			"payment_order_id", po.ID.String())
		return fmt.Errorf("failed to persist FAILED state: %w", err)
	}

	s.releaseLienIfNeeded(ctx, needsLienRelease, lienID, po.ID.String(), reason, "failed")

	// Publish PaymentOrderFailed event
	s.publishEvent(ctx, TopicPaymentOrderFailed, po.ID.String(), &eventsv1.PaymentOrderFailedEvent{
		EventId:         uuid.New().String(),
		PaymentOrderId:  po.ID.String(),
		DebtorAccountId: po.DebtorAccountID,
		Amount:          toMoneyAmount(po.Amount),
		FailureReason:   reason,
		ErrorCode:       errorCode,
		FailedAtStatus:  mapStatusToProto(failedAtStatus),
		LienId:          lienID,
		CorrelationId:   po.CorrelationID,
		CausationId:     po.ID.String(),
		Timestamp:       timestamppb.Now(),
		Version:         int64(po.Version),
		IdempotencyKey:  po.IdempotencyKey,
	})

	s.logger.Info("payment order failed",
		"payment_order_id", po.ID.String(),
		"reason", reason,
		"error_code", errorCode,
		"original_ledger_booking_id", originalLedgerBookingID,
		"idempotency_key", po.IdempotencyKey,
		"correlation_id", po.CorrelationID)

	return nil
}

// CancelPaymentOrder cancels a payment order before completion.
func (s *Service) CancelPaymentOrder(ctx context.Context, req *pb.CancelPaymentOrderRequest) (*pb.CancelPaymentOrderResponse, error) {
	if req.CancellationReason == "" {
		return nil, status.Error(codes.InvalidArgument, "cancellation_reason is required")
	}

	poID, err := uuid.Parse(req.PaymentOrderId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid payment order ID: %v", err)
	}

	po, err := s.repo.FindByID(ctx, poID)
	if err != nil {
		if errors.Is(err, persistence.ErrPaymentOrderNotFound) {
			return nil, status.Errorf(codes.NotFound, "payment order not found: %s", req.PaymentOrderId)
		}
		return nil, status.Error(codes.Internal, "failed to retrieve payment order")
	}

	if po.Status == domain.PaymentOrderStatusCancelled {
		return &pb.CancelPaymentOrderResponse{PaymentOrder: toProto(po)}, nil
	}

	if !po.CanCancel() {
		return nil, status.Errorf(codes.FailedPrecondition,
			"payment order cannot be cancelled in status %s", po.Status)
	}

	needsLienRelease := po.RequiresLienRelease()
	lienID := po.LienID

	if err := po.Cancel(req.CancellationReason); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to cancel payment order: %v", err)
	}

	if err := s.repo.Update(ctx, po); err != nil {
		return nil, status.Error(codes.Internal, "failed to update payment order")
	}

	s.releaseLienIfNeeded(ctx, needsLienRelease, lienID, po.ID.String(), req.CancellationReason, "cancelled")

	s.publishCancelledEvent(ctx, po, req, lienID)

	s.logger.Info("payment order cancelled",
		"payment_order_id", po.ID.String(),
		"reason", req.CancellationReason,
		"cancelled_by", req.CancelledBy,
		"amount_cents", safeMinorUnits(po.Amount),
		"currency", domain.CurrencyCode(po.Amount),
		"idempotency_key", po.IdempotencyKey,
		"correlation_id", po.CorrelationID)

	return &pb.CancelPaymentOrderResponse{PaymentOrder: toProto(po)}, nil
}

// releaseLienIfNeeded terminates a lien if the payment order requires it.
func (s *Service) releaseLienIfNeeded(ctx context.Context, needsRelease bool, lienID, paymentOrderID, reason, operation string) {
	if !needsRelease || lienID == "" || s.currentAccountClient == nil {
		return
	}
	_, err := s.currentAccountClient.TerminateLien(ctx, &currentaccountv1.TerminateLienRequest{
		LienId: lienID,
		Reason: fmt.Sprintf("Payment order %s %s: %s", paymentOrderID, operation, reason),
	})
	if err != nil {
		s.logger.Error("failed to release lien after "+operation,
			"error", err,
			"lien_id", lienID,
			"payment_order_id", paymentOrderID)
	}
}

// publishCancelledEvent publishes the PaymentOrderCancelled event to Kafka.
func (s *Service) publishCancelledEvent(ctx context.Context, po *domain.PaymentOrder, req *pb.CancelPaymentOrderRequest, lienID string) {
	s.publishEvent(ctx, TopicPaymentOrderCancelled, po.ID.String(), &eventsv1.PaymentOrderCancelledEvent{
		EventId:            uuid.New().String(),
		PaymentOrderId:     po.ID.String(),
		DebtorAccountId:    po.DebtorAccountID,
		Amount:             toMoneyAmount(po.Amount),
		CancellationReason: req.CancellationReason,
		CancelledBy:        req.CancelledBy,
		LienId:             lienID,
		CorrelationId:      po.CorrelationID,
		CausationId:        po.ID.String(),
		Timestamp:          timestamppb.Now(),
		Version:            int64(po.Version),
		IdempotencyKey:     po.IdempotencyKey,
	})
}

// ReversePaymentOrder reverses a completed payment order (post-completion compensation).
// This creates compensating ledger entries and transitions the order to REVERSED.
// Idempotent: returns success if already reversed.
func (s *Service) ReversePaymentOrder(ctx context.Context, req *pb.ReversePaymentOrderRequest) (*pb.ReversePaymentOrderResponse, error) {
	if err := validateReverseRequest(req); err != nil {
		return nil, err
	}

	poID, err := uuid.Parse(req.PaymentOrderId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid payment order ID: %v", err)
	}

	idempKey := s.buildReversalIdempotencyKey(ctx, req.PaymentOrderId)

	// Check Redis idempotency for reversal
	if resp, err := s.checkReversalIdempotency(ctx, idempKey, req.PaymentOrderId); err != nil {
		return nil, err
	} else if resp != nil {
		return resp, nil
	}

	// Mark operation as pending (distributed lock)
	if err := s.idempotencyService.MarkPending(ctx, idempKey, idempotencyPendingTTL); err != nil {
		s.logger.Error("failed to mark reversal operation pending", "error", err)
		return nil, status.Error(codes.Internal, "failed to acquire reversal idempotency lock")
	}

	// Retrieve and validate payment order
	po, err := s.fetchAndValidateForReversal(ctx, poID, idempKey, req.PaymentOrderId)
	if err != nil {
		return nil, err
	}

	// Check if already reversed (idempotent)
	if po.Status == domain.PaymentOrderStatusReversed {
		return &pb.ReversePaymentOrderResponse{PaymentOrder: toProto(po)}, nil
	}

	// Execute the reversal: ledger compensation, state transition, persistence
	originalLedgerBookingID := po.LedgerBookingID
	compensatingBookingID, err := s.executeReversal(ctx, po, req)
	if err != nil {
		return nil, err
	}

	// Publish PaymentOrderReversed event with compensating booking ID
	s.publishReversedEvent(ctx, po, req, originalLedgerBookingID, compensatingBookingID)

	s.logger.Info("payment order reversed",
		"payment_order_id", po.ID.String(),
		"reason", req.ReversalReason,
		"reversed_by", req.ReversedBy,
		"amount_cents", safeMinorUnits(po.Amount),
		"currency", domain.CurrencyCode(po.Amount),
		"original_ledger_booking_id", originalLedgerBookingID,
		"compensating_booking_id", compensatingBookingID,
		"correlation_id", po.CorrelationID)

	// Store successful result in Redis for future idempotency checks
	response := &pb.ReversePaymentOrderResponse{PaymentOrder: toProto(po)}
	s.cacheIdempotencyResult(ctx, idempKey, response)

	return response, nil
}

// validateReverseRequest validates the required fields on a reversal request.
func validateReverseRequest(req *pb.ReversePaymentOrderRequest) error {
	if req.ReversalReason == "" {
		return status.Error(codes.InvalidArgument, "reversal_reason is required")
	}
	if req.ReversedBy == "" {
		return status.Error(codes.InvalidArgument, "reversed_by is required")
	}
	return nil
}

// buildReversalIdempotencyKey constructs the idempotency key for a reversal operation.
func (s *Service) buildReversalIdempotencyKey(ctx context.Context, paymentOrderID string) idempotency.Key {
	tenantID, _ := tenant.FromContext(ctx)
	return idempotency.Key{
		TenantID:  string(tenantID),
		Namespace: idempotencyNamespace,
		Operation: "reverse",
		EntityID:  paymentOrderID,
		RequestID: paymentOrderID,
	}
}

// checkReversalIdempotency checks Redis for a cached reversal result.
func (s *Service) checkReversalIdempotency(ctx context.Context, idempKey idempotency.Key, paymentOrderID string) (*pb.ReversePaymentOrderResponse, error) {
	idempResult, err := s.idempotencyService.Check(ctx, idempKey)
	if errors.Is(err, idempotency.ErrOperationAlreadyProcessed) && idempResult != nil && idempResult.Data != nil {
		var cachedResp pb.ReversePaymentOrderResponse
		unmarshalErr := proto.Unmarshal(idempResult.Data, &cachedResp)
		if unmarshalErr == nil {
			s.logger.Info("returning cached reversal result from Redis",
				"payment_order_id", paymentOrderID)
			poobservability.RecordIdempotentRequest("reverse_payment_order_redis")
			return &cachedResp, nil
		}
		s.logger.Warn("failed to unmarshal cached idempotency result, falling back to database check",
			"error", unmarshalErr)
	} else if err != nil && !errors.Is(err, idempotency.ErrResultNotFound) {
		s.logger.Error("idempotency check failed", "error", err)
		return nil, status.Error(codes.Internal, "failed to check idempotency")
	}
	return nil, nil //nolint:nilnil // nil,nil signals "no cached result, continue processing"
}

// fetchAndValidateForReversal retrieves the payment order and validates it can be reversed.
func (s *Service) fetchAndValidateForReversal(ctx context.Context, poID uuid.UUID, idempKey idempotency.Key, paymentOrderIDStr string) (*domain.PaymentOrder, error) {
	po, err := s.repo.FindByID(ctx, poID)
	if err != nil {
		if errors.Is(err, persistence.ErrPaymentOrderNotFound) {
			s.storeIdempotencyFailure(ctx, idempKey, fmt.Sprintf("payment order not found: %s", paymentOrderIDStr))
			return nil, status.Errorf(codes.NotFound, "payment order not found: %s", paymentOrderIDStr)
		}
		return nil, status.Error(codes.Internal, "failed to retrieve payment order")
	}

	if po.Status != domain.PaymentOrderStatusReversed && !po.CanReverse() {
		s.storeIdempotencyFailure(ctx, idempKey, fmt.Sprintf("payment order cannot be reversed in status %s", po.Status))
		return nil, status.Errorf(codes.FailedPrecondition,
			"payment order cannot be reversed in status %s (only COMPLETED orders can be reversed)", po.Status)
	}

	return po, nil
}

// executeReversal performs the ledger compensation, state transition, and persistence for a reversal.
func (s *Service) executeReversal(ctx context.Context, po *domain.PaymentOrder, req *pb.ReversePaymentOrderRequest) (string, error) {
	originalLedgerBookingID := po.LedgerBookingID

	compensatingBookingID, err := s.reverseLedgerPosting(ctx, po, req.ReversalReason)
	if err != nil {
		s.logger.Error("failed to create compensating ledger entries for reversal",
			"payment_order_id", po.ID.String(),
			"original_ledger_booking_id", originalLedgerBookingID,
			"error", err)
		return "", status.Errorf(codes.Internal, "failed to create compensating ledger entries: %v", err)
	}

	if err := po.Reverse(req.ReversalReason); err != nil {
		return "", status.Errorf(codes.Internal, "failed to reverse payment order: %v", err)
	}

	if err := s.repo.Update(ctx, po); err != nil {
		return "", status.Error(codes.Internal, "failed to update payment order")
	}

	return compensatingBookingID, nil
}

// publishReversedEvent publishes the PaymentOrderReversed event to Kafka.
func (s *Service) publishReversedEvent(ctx context.Context, po *domain.PaymentOrder, req *pb.ReversePaymentOrderRequest, originalLedgerBookingID, compensatingBookingID string) {
	s.publishEvent(ctx, TopicPaymentOrderReversed, po.ID.String(), &eventsv1.PaymentOrderReversedEvent{
		EventId:                     uuid.New().String(),
		PaymentOrderId:              po.ID.String(),
		DebtorAccountId:             po.DebtorAccountID,
		Amount:                      toMoneyAmount(po.Amount),
		ReversalReason:              req.ReversalReason,
		ReversedBy:                  req.ReversedBy,
		OriginalLedgerBookingId:     originalLedgerBookingID,
		CompensatingLedgerBookingId: compensatingBookingID,
		CorrelationId:               po.CorrelationID,
		CausationId:                 po.ID.String(),
		Timestamp:                   timestamppb.Now(),
		Version:                     int64(po.Version),
		IdempotencyKey:              po.IdempotencyKey,
	})
}

// cacheIdempotencyResult stores a successful result in Redis for future idempotency checks.
func (s *Service) cacheIdempotencyResult(ctx context.Context, idempKey idempotency.Key, response proto.Message) {
	responseData, marshalErr := proto.Marshal(response)
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
		}
	} else {
		s.logger.Error("failed to marshal response for idempotency cache", "error", marshalErr)
	}
}

// reverseLedgerPosting creates reversal entries for a completed payment.
// This is used for refunds and failed payment reversals.
func (s *Service) reverseLedgerPosting(ctx context.Context, po *domain.PaymentOrder, reason string) (string, error) {
	// No ledger entry to reverse if LedgerBookingID is empty
	if po.LedgerBookingID == "" {
		s.logger.Debug("no ledger entry to reverse - payment had no ledger posting",
			"payment_order_id", po.ID.String())
		return "", nil
	}

	// Skip ledger reversal if financial accounting client is not configured
	// This allows the service to operate without FA integration for testing
	if s.financialAccountingClient == nil || s.gatewayAccountConfig == nil {
		s.logger.Warn("skipping ledger reversal - financial accounting client not configured",
			"payment_order_id", po.ID.String(),
			"ledger_booking_id", po.LedgerBookingID)
		return "", nil
	}

	contraAccountID, currencyCode, err := s.validateReversalPrerequisites(po)
	if err != nil {
		return "", err
	}

	// Step 1: Create a BookingLog in PENDING status for the reversal
	reversalBookingLogID, err := s.createReversalBookingLog(ctx, po, currencyCode)
	if err != nil {
		return "", err
	}

	s.logger.Debug("created reversal booking log",
		"reversal_booking_log_id", reversalBookingLogID,
		"original_booking_log_id", po.LedgerBookingID,
		"payment_order_id", po.ID.String(),
		"reason", reason)

	// Convert amount from cents to google.type.Money format
	amountCents := domain.ToMinorUnits(po.Amount)
	postingAmount := &money.Money{
		CurrencyCode: currencyCode,
		Units:        amountCents / 100,
		Nanos:        int32((amountCents % 100) * 10000000),
	}
	valueDate := timestamppb.Now()

	// Steps 2-3: Create CREDIT and DEBIT reversal postings
	if err := s.captureReversalPostings(ctx, po, reversalBookingLogID, postingAmount, valueDate, contraAccountID, amountCents); err != nil {
		return "", err
	}

	// Step 4: Update BookingLog status to POSTED
	if err := s.finalizeReversalBookingLog(ctx, po, reversalBookingLogID); err != nil {
		return "", err
	}

	s.logger.Info("reversal ledger posting completed successfully",
		"reversal_booking_log_id", reversalBookingLogID,
		"original_booking_log_id", po.LedgerBookingID,
		"payment_order_id", po.ID.String(),
		"debtor_account", po.DebtorAccountID,
		"contra_account", contraAccountID,
		"amount_cents", amountCents,
		"currency", currencyCode,
		"reason", reason)

	return reversalBookingLogID, nil
}

// validateReversalPrerequisites resolves the contra-account and validates the currency for a reversal.
func (s *Service) validateReversalPrerequisites(po *domain.PaymentOrder) (string, string, error) {
	gatewayID := extractGatewayIDFromRef(po.GatewayReferenceID)
	contraAccountID, err := s.gatewayAccountConfig.GetContraAccount(gatewayID)
	if err != nil {
		return "", "", fmt.Errorf("failed to get contra-account for gateway %s: %w", gatewayID, err)
	}

	currencyCode := domain.CurrencyCode(po.Amount)
	if currencyCode == "" {
		s.logger.Warn("unsupported currency for reversal posting",
			"currency", currencyCode,
			"payment_order_id", po.ID.String())
		return "", "", fmt.Errorf("%w: %s", ErrUnsupportedCurrency, currencyCode)
	}

	return contraAccountID, currencyCode, nil
}

// createReversalBookingLog creates a BookingLog in PENDING status for the reversal.
func (s *Service) createReversalBookingLog(ctx context.Context, po *domain.PaymentOrder, currencyCode string) (string, error) {
	reversalBookingLogIDempKey := fmt.Sprintf("reversal-booking-log-%s", po.IdempotencyKey)
	bookingLogResp, err := s.financialAccountingClient.InitiateFinancialBookingLog(ctx, &financialaccountingv1.InitiateFinancialBookingLogRequest{
		FinancialAccountType:    "CURRENT",
		ProductServiceReference: "payment-order-reversal",
		BusinessUnitReference:   "payment-order-service",
		ChartOfAccountsRules:    "payment-reversal",
		BaseInstrumentCode:      currencyCode,
		IdempotencyKey: &commonpb.IdempotencyKey{
			Key: reversalBookingLogIDempKey,
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to create reversal booking log: %w", err)
	}
	return bookingLogResp.FinancialBookingLog.Id, nil
}

// captureReversalPostings creates the CREDIT and DEBIT reversal ledger postings.
func (s *Service) captureReversalPostings(
	ctx context.Context,
	po *domain.PaymentOrder,
	reversalBookingLogID string,
	postingAmount *money.Money,
	valueDate *timestamppb.Timestamp,
	contraAccountID string,
	amountCents int64,
) error {
	// Step 2: Create CREDIT posting (customer account - funds returning)
	reversalCreditIdempKey := fmt.Sprintf("reversal-credit-%s", po.IdempotencyKey)
	_, err := s.financialAccountingClient.CaptureLedgerPosting(ctx, &financialaccountingv1.CaptureLedgerPostingRequest{
		FinancialBookingLogId: reversalBookingLogID,
		PostingDirection:      commonpb.PostingDirection_POSTING_DIRECTION_CREDIT,
		PostingAmount:         postingAmount,
		AccountId:             po.DebtorAccountID,
		ValueDate:             valueDate,
		IdempotencyKey: &commonpb.IdempotencyKey{
			Key: reversalCreditIdempKey,
		},
	})
	if err != nil {
		s.logger.Error("RECONCILIATION_REQUIRED: reversal booking log orphaned after credit posting failure",
			"reversal_booking_log_id", reversalBookingLogID,
			"original_booking_log_id", po.LedgerBookingID,
			"booking_log_status", "PENDING",
			"payment_order_id", po.ID.String(),
			"failed_step", "reversal_credit_posting",
			"debtor_account", po.DebtorAccountID,
			"error", err.Error())
		return fmt.Errorf("failed to create reversal credit posting for account %s: %w", po.DebtorAccountID, err)
	}

	s.logger.Debug("created reversal credit posting",
		"reversal_booking_log_id", reversalBookingLogID,
		"account_id", po.DebtorAccountID,
		"amount_cents", amountCents,
		"payment_order_id", po.ID.String())

	// Step 3: Create DEBIT posting (gateway contra-account - reducing liability)
	reversalDebitIdempKey := fmt.Sprintf("reversal-debit-%s", po.IdempotencyKey)
	_, err = s.financialAccountingClient.CaptureLedgerPosting(ctx, &financialaccountingv1.CaptureLedgerPostingRequest{
		FinancialBookingLogId: reversalBookingLogID,
		PostingDirection:      commonpb.PostingDirection_POSTING_DIRECTION_DEBIT,
		PostingAmount:         postingAmount,
		AccountId:             contraAccountID,
		ValueDate:             valueDate,
		IdempotencyKey: &commonpb.IdempotencyKey{
			Key: reversalDebitIdempKey,
		},
	})
	if err != nil {
		s.logger.Error("RECONCILIATION_REQUIRED: reversal booking log orphaned after debit posting failure",
			"reversal_booking_log_id", reversalBookingLogID,
			"original_booking_log_id", po.LedgerBookingID,
			"booking_log_status", "PENDING",
			"payment_order_id", po.ID.String(),
			"failed_step", "reversal_debit_posting",
			"debtor_account", po.DebtorAccountID,
			"contra_account", contraAccountID,
			"has_credit_posting", true,
			"error", err.Error())
		return fmt.Errorf("failed to create reversal debit posting for account %s: %w", contraAccountID, err)
	}

	s.logger.Debug("created reversal debit posting",
		"reversal_booking_log_id", reversalBookingLogID,
		"account_id", contraAccountID,
		"amount_cents", amountCents,
		"payment_order_id", po.ID.String())

	return nil
}

// finalizeReversalBookingLog updates the reversal booking log status to POSTED.
func (s *Service) finalizeReversalBookingLog(ctx context.Context, po *domain.PaymentOrder, reversalBookingLogID string) error {
	_, err := s.financialAccountingClient.UpdateFinancialBookingLog(ctx, &financialaccountingv1.UpdateFinancialBookingLogRequest{
		Id:     reversalBookingLogID,
		Status: commonpb.TransactionStatus_TRANSACTION_STATUS_POSTED,
	})
	if err != nil {
		s.logger.Error("RECONCILIATION_REQUIRED: reversal booking log status update failed after successful postings",
			"reversal_booking_log_id", reversalBookingLogID,
			"original_booking_log_id", po.LedgerBookingID,
			"booking_log_status", "PENDING",
			"target_status", "POSTED",
			"payment_order_id", po.ID.String(),
			"failed_step", "reversal_status_update",
			"has_credit_posting", true,
			"has_debit_posting", true,
			"resolution", "manually update booking log status to POSTED",
			"error", err.Error())
		return fmt.Errorf("failed to update reversal booking log to POSTED: %w", err)
	}
	return nil
}
