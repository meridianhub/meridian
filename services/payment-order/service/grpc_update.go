package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	"github.com/meridianhub/meridian/services/payment-order/adapters/persistence"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	poobservability "github.com/meridianhub/meridian/services/payment-order/observability"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// updateResult holds the result of a status update operation.
type updateResult struct {
	po           *domain.PaymentOrder
	isIdempotent bool
}

// UpdatePaymentOrder handles asynchronous gateway callbacks.
// Implements idempotency, audit logging, and observability per task 11 requirements.
func (s *Service) UpdatePaymentOrder(ctx context.Context, req *pb.UpdatePaymentOrderRequest) (*pb.UpdatePaymentOrderResponse, error) {
	start := time.Now()
	operationStatus := opStatusSuccess
	gatewayStatusStr := req.GatewayStatus.String()

	defer func() {
		elapsed := time.Since(start)
		poobservability.RecordOperationDuration("update_payment_order", operationStatus, elapsed)
		poobservability.RecordGatewayCallback(gatewayStatusStr, operationStatus)
		s.logger.Info("update_payment_order completed",
			"duration_ms", elapsed.Milliseconds(),
			"gateway_status", gatewayStatusStr,
			"result", operationStatus)
	}()

	webhookIdempotencyKey := validateWebhookIdempotencyKey(req)
	if webhookIdempotencyKey == "" {
		operationStatus = opStatusError
		return nil, status.Error(codes.InvalidArgument, "idempotency_key is required for webhook callbacks")
	}

	po, err := s.lookupPaymentOrder(ctx, req)
	if err != nil {
		operationStatus = opStatusError
		return nil, err
	}

	idempKey := s.buildUpdateIdempotencyKey(ctx, po.ID.String(), webhookIdempotencyKey)

	// Check Redis idempotency for webhook (prevents duplicate processing)
	if resp, checkErr := s.checkUpdateIdempotency(ctx, idempKey, po.ID.String(), webhookIdempotencyKey); checkErr != nil {
		operationStatus = opStatusError
		return nil, checkErr
	} else if resp != nil {
		operationStatus = opStatusIdempotent
		return resp, nil
	}

	if err := s.idempotencyService.MarkPending(ctx, idempKey, idempotencyPendingTTL); err != nil {
		s.logger.Error("failed to mark webhook operation pending", "error", err)
		operationStatus = opStatusError
		return nil, status.Error(codes.Internal, "failed to acquire webhook idempotency lock")
	}

	s.logger.Info("processing gateway callback",
		"payment_order_id", po.ID.String(),
		"gateway_reference_id", po.GatewayReferenceID,
		"current_status", po.Status,
		"gateway_status", gatewayStatusStr,
		"correlation_id", po.CorrelationID)

	response, statusResult, err := s.dispatchGatewayStatus(ctx, req, po, idempKey)
	if err != nil {
		operationStatus = opStatusError
		return nil, err
	}
	if statusResult == opStatusIdempotent {
		operationStatus = opStatusIdempotent
	}

	s.cacheIdempotencyResult(ctx, idempKey, response)

	return response, nil
}

// validateWebhookIdempotencyKey extracts the idempotency key from a webhook request.
func validateWebhookIdempotencyKey(req *pb.UpdatePaymentOrderRequest) string {
	if req.IdempotencyKey != nil {
		return req.IdempotencyKey.Key
	}
	return ""
}

// buildUpdateIdempotencyKey constructs the idempotency key for an update operation.
func (s *Service) buildUpdateIdempotencyKey(ctx context.Context, entityID, requestID string) idempotency.Key {
	tenantID, _ := tenant.FromContext(ctx)
	return idempotency.Key{
		TenantID:  string(tenantID),
		Namespace: idempotencyNamespace,
		Operation: "update",
		EntityID:  entityID,
		RequestID: requestID,
	}
}

// checkUpdateIdempotency checks Redis for a cached update result.
func (s *Service) checkUpdateIdempotency(ctx context.Context, idempKey idempotency.Key, paymentOrderID, webhookIdempotencyKey string) (*pb.UpdatePaymentOrderResponse, error) {
	idempResult, err := s.idempotencyService.Check(ctx, idempKey)
	if errors.Is(err, idempotency.ErrOperationAlreadyProcessed) && idempResult != nil && idempResult.Data != nil {
		var cachedResp pb.UpdatePaymentOrderResponse
		unmarshalErr := proto.Unmarshal(idempResult.Data, &cachedResp)
		if unmarshalErr == nil {
			s.logger.Info("returning cached update result from Redis",
				"payment_order_id", paymentOrderID,
				"idempotency_key", webhookIdempotencyKey)
			poobservability.RecordIdempotentRequest("update_payment_order_redis")
			return &cachedResp, nil
		}
		s.logger.Warn("failed to unmarshal cached idempotency result, continuing with normal processing",
			"error", unmarshalErr)
	} else if err != nil && !errors.Is(err, idempotency.ErrResultNotFound) {
		s.logger.Error("idempotency check failed", "error", err)
		return nil, status.Error(codes.Internal, "failed to check idempotency")
	}
	return nil, nil //nolint:nilnil // nil,nil signals "no cached result, continue processing"
}

// dispatchGatewayStatus routes the gateway callback to the appropriate status handler.
// Returns (response, operationStatus, error).
func (s *Service) dispatchGatewayStatus(ctx context.Context, req *pb.UpdatePaymentOrderRequest, po *domain.PaymentOrder, idempKey idempotency.Key) (*pb.UpdatePaymentOrderResponse, string, error) {
	switch req.GatewayStatus {
	case pb.GatewayStatus_GATEWAY_STATUS_SETTLED:
		result, err := s.handleSettledStatus(ctx, po)
		if err != nil {
			return nil, opStatusError, err
		}
		opStatus := opStatusSuccess
		if result.isIdempotent {
			opStatus = opStatusIdempotent
		}
		return &pb.UpdatePaymentOrderResponse{PaymentOrder: toProto(result.po)}, opStatus, nil

	case pb.GatewayStatus_GATEWAY_STATUS_REJECTED:
		result, err := s.handleRejectedStatus(ctx, po, req.GatewayMessage)
		if err != nil {
			return nil, opStatusError, err
		}
		opStatus := opStatusSuccess
		if result.isIdempotent {
			opStatus = opStatusIdempotent
		}
		return &pb.UpdatePaymentOrderResponse{PaymentOrder: toProto(result.po)}, opStatus, nil

	case pb.GatewayStatus_GATEWAY_STATUS_PENDING:
		if err := s.handlePendingStatus(po, req.GatewayReferenceId); err != nil {
			return nil, opStatusError, err
		}
		return &pb.UpdatePaymentOrderResponse{PaymentOrder: toProto(po)}, opStatusSuccess, nil

	case pb.GatewayStatus_GATEWAY_STATUS_REFUNDED:
		s.logger.Info("refund webhook received, acknowledgment only",
			"payment_order_id", po.ID,
			"gateway_reference_id", req.GatewayReferenceId)
		return &pb.UpdatePaymentOrderResponse{PaymentOrder: toProto(po)}, opStatusSuccess, nil

	case pb.GatewayStatus_GATEWAY_STATUS_DISPUTED:
		s.logger.Warn("dispute webhook received, acknowledgment only",
			"payment_order_id", po.ID,
			"gateway_reference_id", req.GatewayReferenceId,
			"gateway_message", req.GatewayMessage)
		return &pb.UpdatePaymentOrderResponse{PaymentOrder: toProto(po)}, opStatusSuccess, nil

	case pb.GatewayStatus_GATEWAY_STATUS_UNSPECIFIED:
		s.storeIdempotencyFailure(ctx, idempKey, "gateway status is required")
		return nil, opStatusError, status.Error(codes.InvalidArgument, "gateway status is required")

	default:
		s.storeIdempotencyFailure(ctx, idempKey, fmt.Sprintf("unknown gateway status: %v", req.GatewayStatus))
		return nil, opStatusError, status.Errorf(codes.InvalidArgument, "unknown gateway status: %v", req.GatewayStatus)
	}
}

// lookupPaymentOrder finds a payment order by ID or gateway reference ID.
func (s *Service) lookupPaymentOrder(ctx context.Context, req *pb.UpdatePaymentOrderRequest) (*domain.PaymentOrder, error) {
	var po *domain.PaymentOrder
	var err error

	if req.PaymentOrderId != "" {
		poID, parseErr := uuid.Parse(req.PaymentOrderId)
		if parseErr != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid payment order ID: %v", parseErr)
		}
		po, err = s.repo.FindByID(ctx, poID)
	} else if req.GatewayReferenceId != "" {
		po, err = s.repo.FindByGatewayReferenceID(ctx, req.GatewayReferenceId)
	} else {
		return nil, status.Error(codes.InvalidArgument, "either payment_order_id or gateway_reference_id must be provided")
	}

	if err != nil {
		if errors.Is(err, persistence.ErrPaymentOrderNotFound) {
			return nil, status.Error(codes.NotFound, "payment order not found")
		}
		s.logger.Error("failed to find payment order", "error", err)
		return nil, status.Error(codes.Internal, "failed to find payment order")
	}

	return po, nil
}

// handleSettledStatus processes a SETTLED gateway callback.
// Implements idempotency: returns success if already COMPLETED.
// Posts double-entry ledger journal entries BEFORE completing the payment.
func (s *Service) handleSettledStatus(ctx context.Context, po *domain.PaymentOrder) (*updateResult, error) {
	// Idempotency check: if already completed, return success without modification
	if po.Status == domain.PaymentOrderStatusCompleted {
		s.logger.Info("idempotent settled callback - payment already completed",
			"payment_order_id", po.ID.String(),
			"correlation_id", po.CorrelationID)
		poobservability.RecordIdempotentRequest("update_payment_order_settled")
		return &updateResult{po: po, isIdempotent: true}, nil
	}

	// Post ledger entries BEFORE completing the payment order
	ledgerBookingID, err := s.postLedgerAndComplete(ctx, po)
	if err != nil {
		return nil, err
	}

	// Record metrics
	poobservability.RecordCompletion(domain.CurrencyCode(po.Amount))
	poobservability.RecordPaymentAmount(domain.CurrencyCode(po.Amount), "completed", safeMinorUnits(po.Amount))

	// Execute lien asynchronously with retry mechanism
	s.startAsyncLienExecution(ctx, po)

	// Publish PaymentOrderCompleted event
	s.publishSettledEvent(ctx, po)

	// Audit log for successful completion
	s.logger.Info("payment order completed via gateway callback",
		"payment_order_id", po.ID.String(),
		"gateway_reference_id", po.GatewayReferenceID,
		"ledger_booking_id", ledgerBookingID,
		"amount_cents", safeMinorUnits(po.Amount),
		"currency", domain.CurrencyCode(po.Amount),
		"lien_id", po.LienID,
		"idempotency_key", po.IdempotencyKey,
		"correlation_id", po.CorrelationID)

	return &updateResult{po: po, isIdempotent: false}, nil
}

// postLedgerAndComplete posts ledger entries and transitions the payment order to COMPLETED.
func (s *Service) postLedgerAndComplete(ctx context.Context, po *domain.PaymentOrder) (string, error) {
	ledgerBookingID, err := s.orchestrator.PostLedgerEntries(ctx, po)
	if err != nil {
		s.logger.Error("failed to post ledger entries",
			"payment_order_id", po.ID.String(),
			"error", err)
		if failErr := s.failPaymentOrder(ctx, po, fmt.Sprintf("ledger posting failed: %v", err), "LEDGER_POSTING_FAILED"); failErr != nil {
			s.logger.Error("failed to mark payment as failed after ledger posting failure",
				"payment_order_id", po.ID.String(),
				"error", failErr)
		}
		return "", status.Errorf(codes.Internal, "failed to post ledger entries: %v", err)
	}

	if err := po.Complete(ledgerBookingID); err != nil {
		if errors.Is(err, domain.ErrInvalidPaymentOrderTransition) {
			return "", status.Errorf(codes.FailedPrecondition,
				"cannot complete payment order in %s state: %v", po.Status, err)
		}
		return "", status.Errorf(codes.Internal, "failed to complete payment: %v", err)
	}

	if po.LienID != "" {
		po.SetLienExecutionPending()
	}

	if err := s.repo.Update(ctx, po); err != nil {
		s.logger.Error("failed to update payment order to COMPLETED",
			"error", err,
			"payment_order_id", po.ID.String())
		return "", status.Error(codes.Internal, "failed to update payment order")
	}

	return ledgerBookingID, nil
}

// startAsyncLienExecution starts lien execution in a background goroutine if needed.
func (s *Service) startAsyncLienExecution(ctx context.Context, po *domain.PaymentOrder) {
	if s.currentAccountClient == nil || po.LienID == "" {
		return
	}
	asyncCtx := context.Background()
	if tenantID, hasTenant := tenant.FromContext(ctx); hasTenant {
		asyncCtx = tenant.WithTenant(asyncCtx, tenantID)
	}
	go s.orchestrator.ExecuteLienWithRetry(asyncCtx, po.ID, po.LienID) //nolint:contextcheck // intentional background context for async retry after webhook response
}

// publishSettledEvent publishes the PaymentOrderCompleted event to Kafka.
func (s *Service) publishSettledEvent(ctx context.Context, po *domain.PaymentOrder) {
	s.publishEvent(ctx, TopicPaymentOrderCompleted, po.ID.String(), &eventsv1.PaymentOrderCompletedEvent{
		EventId:            uuid.New().String(),
		PaymentOrderId:     po.ID.String(),
		DebtorAccountId:    po.DebtorAccountID,
		Amount:             toMoneyAmount(po.Amount),
		LienId:             po.LienID,
		GatewayReferenceId: po.GatewayReferenceID,
		LedgerBookingId:    po.LedgerBookingID,
		CorrelationId:      po.CorrelationID,
		CausationId:        po.ID.String(),
		Timestamp:          timestamppb.Now(),
		Version:            int64(po.Version),
		IdempotencyKey:     po.IdempotencyKey,
	})
}

// handleRejectedStatus processes a REJECTED gateway callback.
// Implements idempotency: returns success if already FAILED.
func (s *Service) handleRejectedStatus(ctx context.Context, po *domain.PaymentOrder, gatewayMessage string) (*updateResult, error) {
	// Idempotency check: if already failed, return success without modification
	if po.Status == domain.PaymentOrderStatusFailed {
		s.logger.Info("idempotent rejected callback - payment already failed",
			"payment_order_id", po.ID.String(),
			"correlation_id", po.CorrelationID)
		poobservability.RecordIdempotentRequest("update_payment_order_rejected")
		return &updateResult{po: po, isIdempotent: true}, nil
	}

	// Fail the payment - synchronous path: propagate error to client
	if err := s.failPaymentOrder(ctx, po, gatewayMessage, "GATEWAY_REJECTED"); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to mark payment as rejected: %v", err)
	}

	// Record metrics after successful persistence to ensure accuracy
	poobservability.RecordRejection(domain.CurrencyCode(po.Amount), poobservability.ErrorCategoryGatewayRejected)
	poobservability.RecordPaymentAmount(domain.CurrencyCode(po.Amount), "rejected", safeMinorUnits(po.Amount))

	// Audit log for rejection
	s.logger.Info("payment order rejected via gateway callback",
		"payment_order_id", po.ID.String(),
		"gateway_reference_id", po.GatewayReferenceID,
		"gateway_message", gatewayMessage,
		"amount_cents", safeMinorUnits(po.Amount),
		"currency", domain.CurrencyCode(po.Amount),
		"lien_id", po.LienID,
		"idempotency_key", po.IdempotencyKey,
		"correlation_id", po.CorrelationID)

	return &updateResult{po: po, isIdempotent: false}, nil
}

// handlePendingStatus processes a PENDING gateway callback.
// Validates state and logs - no state transition needed.
func (s *Service) handlePendingStatus(po *domain.PaymentOrder, gatewayRefID string) error {
	// Validate that we're still in EXECUTING state - PENDING callbacks for
	// terminal states (COMPLETED, FAILED, etc.) should be rejected as stale
	if po.Status != domain.PaymentOrderStatusExecuting {
		return status.Errorf(codes.FailedPrecondition,
			"cannot process PENDING callback: payment order is in %s state", po.Status)
	}

	// No state change needed - still waiting for final confirmation
	s.logger.Info("payment still pending at gateway",
		"payment_order_id", po.ID.String(),
		"gateway_reference_id", gatewayRefID,
		"correlation_id", po.CorrelationID)

	return nil
}
