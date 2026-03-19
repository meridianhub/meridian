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
	"github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// InitiatePaymentOrder creates a new payment order and begins the saga.
func (s *Service) InitiatePaymentOrder(ctx context.Context, req *pb.InitiatePaymentOrderRequest) (*pb.InitiatePaymentOrderResponse, error) {
	start := time.Now()
	defer func() {
		s.logger.Info("initiate_payment_order completed",
			"duration_ms", time.Since(start).Milliseconds())
	}()

	// Extract or generate correlation ID
	correlationID := req.CorrelationId
	if correlationID == "" {
		correlationID = uuid.New().String()
		s.logger.Info("generated correlation ID", "correlation_id", correlationID)
	}

	// Get idempotency key (required)
	var idempotencyKey string
	if req.IdempotencyKey != nil {
		idempotencyKey = req.IdempotencyKey.Key
	}
	if idempotencyKey == "" {
		return nil, status.Error(codes.InvalidArgument, "idempotency_key is required")
	}
	if len(idempotencyKey) > s.maxIdempotencyKeyLength {
		return nil, status.Errorf(codes.InvalidArgument, "idempotency_key exceeds maximum length of %d", s.maxIdempotencyKeyLength)
	}

	// Check Redis idempotency FIRST (before database check) - provides distributed lock protection
	tenantID, _ := tenant.FromContext(ctx)
	idempKey := idempotency.Key{
		TenantID:  string(tenantID),
		Namespace: idempotencyNamespace,
		Operation: "initiate",
		EntityID:  req.DebtorAccountId, // Use account as entity scope
		RequestID: idempotencyKey,
	}

	result, err := s.idempotencyService.Check(ctx, idempKey)
	// If operation already processed, return cached result
	if errors.Is(err, idempotency.ErrOperationAlreadyProcessed) && result != nil && result.Data != nil {
		var cachedResp pb.InitiatePaymentOrderResponse
		unmarshalErr := proto.Unmarshal(result.Data, &cachedResp)
		if unmarshalErr == nil {
			s.logger.Info("returning cached initiate result from Redis",
				"payment_order_id", cachedResp.PaymentOrder.PaymentOrderId,
				"idempotency_key", idempotencyKey)
			poobservability.RecordIdempotentRequest("initiate_payment_order_redis")
			return &cachedResp, nil
		}
		s.logger.Warn("failed to unmarshal cached idempotency result, falling back to database check",
			"error", unmarshalErr)
	} else if err != nil && !errors.Is(err, idempotency.ErrResultNotFound) {
		s.logger.Error("idempotency check failed", "error", err)
		return nil, status.Error(codes.Internal, "failed to check idempotency")
	}

	// Mark operation as pending (distributed lock to prevent concurrent duplicates).
	// Note: If this succeeds but the operation later fails with a transient error (e.g., DB unavailable),
	// the pending lock remains until TTL expires (5 minutes). This is intentional - it prevents
	// concurrent retry attempts from creating duplicates. The client should retry after TTL expiry.
	if err := s.idempotencyService.MarkPending(ctx, idempKey, idempotencyPendingTTL); err != nil {
		s.logger.Error("failed to mark operation pending", "error", err)
		return nil, status.Error(codes.Internal, "failed to acquire idempotency lock")
	}

	// Check for existing payment order with same idempotency key (idempotent).
	// Note: This check has a TOCTOU race window where concurrent requests with the same
	// idempotency key could both pass this check. The database unique constraint on
	// idempotency_key is the authoritative guard - concurrent inserts will fail with a
	// constraint violation and should be handled by returning the existing record.
	existingPO, err := s.repo.FindByIdempotencyKey(ctx, idempotencyKey)
	if err != nil && !errors.Is(err, persistence.ErrPaymentOrderNotFound) {
		s.logger.Error("failed to check idempotency", "error", err)
		// Don't cache internal errors - allow retry on recovery
		return nil, status.Error(codes.Internal, "failed to check idempotency")
	}
	if existingPO != nil {
		s.logger.Info("returning existing payment order (idempotent)",
			"payment_order_id", existingPO.ID.String(),
			"idempotency_key", idempotencyKey)
		return &pb.InitiatePaymentOrderResponse{
			PaymentOrder: toProto(existingPO),
		}, nil
	}

	// Validate and convert amount
	amount, err := protoToMoney(req.Amount)
	if err != nil {
		// Store validation failure for idempotent error responses
		s.storeIdempotencyFailure(ctx, idempKey, fmt.Sprintf("invalid amount: %v", err))
		return nil, status.Errorf(codes.InvalidArgument, "invalid amount: %v", err)
	}
	if !amount.IsPositive() {
		s.storeIdempotencyFailure(ctx, idempKey, "amount must be positive")
		return nil, status.Error(codes.InvalidArgument, "amount must be positive")
	}

	// Create domain payment order
	po, err := domain.NewPaymentOrder(
		req.DebtorAccountId,
		req.CreditorReference,
		amount,
		idempotencyKey,
		correlationID,
	)
	if err != nil {
		s.logger.Error("failed to create payment order", "error", err)
		s.storeIdempotencyFailure(ctx, idempKey, fmt.Sprintf("failed to create payment order: %v", err))
		return nil, status.Errorf(codes.InvalidArgument, "failed to create payment order: %v", err)
	}

	// Persist to database
	if err := s.repo.Create(ctx, po); err != nil {
		// Handle idempotency key conflict (TOCTOU race): another request won the race
		// Reload and return the existing payment order for idempotent behavior
		if errors.Is(err, persistence.ErrIdempotencyKeyConflict) {
			existingPO, findErr := s.repo.FindByIdempotencyKey(ctx, idempotencyKey)
			if findErr != nil {
				s.logger.Error("failed to retrieve existing payment order after idempotency conflict",
					"error", findErr,
					"idempotency_key", idempotencyKey)
				// Don't cache internal errors - allow retry on recovery
				return nil, status.Error(codes.Internal, "failed to retrieve payment order")
			}
			s.logger.Info("returning existing payment order (idempotency race)",
				"payment_order_id", existingPO.ID.String(),
				"idempotency_key", idempotencyKey)
			return &pb.InitiatePaymentOrderResponse{
				PaymentOrder: toProto(existingPO),
			}, nil
		}
		s.logger.Error("failed to save payment order", "error", err)
		// Don't cache internal errors - allow retry on recovery
		return nil, status.Error(codes.Internal, "failed to save payment order")
	}

	s.logger.Info("payment order created",
		"payment_order_id", po.ID.String(),
		"debtor_account_id", po.DebtorAccountID,
		"amount_cents", safeMinorUnits(po.Amount),
		"currency", domain.CurrencyCode(po.Amount),
		"idempotency_key", po.IdempotencyKey,
		"correlation_id", correlationID)

	// Publish PaymentOrderInitiated event to Kafka
	// Publish event (publishEvent handles nil kafkaPublisher)
	s.publishEvent(ctx, TopicPaymentOrderInitiated, po.ID.String(), &eventsv1.PaymentOrderInitiatedEvent{
		EventId:           uuid.New().String(),
		PaymentOrderId:    po.ID.String(),
		DebtorAccountId:   po.DebtorAccountID,
		CreditorReference: po.CreditorReference,
		Amount:            toMoneyAmount(po.Amount),
		CorrelationId:     po.CorrelationID,
		CausationId:       po.ID.String(), // Initial event caused by the order creation
		Timestamp:         timestamppb.Now(),
		Version:           int64(po.Version),
		IdempotencyKey:    po.IdempotencyKey,
	})

	// Convert to proto BEFORE starting the async goroutine to avoid data race
	// The saga may modify po while toProto reads from it
	responseProto := toProto(po)

	// Store successful result in Redis for future idempotency checks
	response := &pb.InitiatePaymentOrderResponse{PaymentOrder: responseProto}
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
			// Continue - operation succeeded, caching is optimization
		}
	} else {
		s.logger.Error("failed to marshal response for idempotency cache", "error", marshalErr)
	}

	// Use tenantID from idempotency check above
	hasTenant := tenantID != ""

	// Start saga orchestration asynchronously
	// The saga runs in the background after returning the response
	//nolint:contextcheck // Intentionally using background context for async saga orchestration
	go func(paymentOrderID uuid.UUID, tid tenant.TenantID, hasTenantCtx bool) {
		// Recover from panics to prevent silent goroutine termination
		defer func() {
			if r := recover(); r != nil {
				s.logger.Error("panic in payment saga orchestration",
					"panic", r,
					"payment_order_id", paymentOrderID.String(),
					"correlation_id", correlationID)
				// Reload fresh state before failing - the original po may be stale
				// if the saga made state transitions before panicking
				failCtx := context.Background()
				if hasTenantCtx {
					failCtx = tenant.WithTenant(failCtx, tid)
				}
				freshPO, err := s.repo.FindByID(failCtx, paymentOrderID)
				if err != nil {
					s.logger.Error("failed to reload payment order after panic",
						"payment_order_id", paymentOrderID.String(),
						"error", err)
					return
				}
				// Async path: log and swallow error - best effort failure handling
				if err := s.failPaymentOrder(failCtx, freshPO, "internal panic during saga orchestration", "INTERNAL_ERROR"); err != nil {
					s.logger.Error("failed to mark payment order as failed after panic",
						"payment_order_id", paymentOrderID.String(),
						"error", err)
				}
			}
		}()
		// Create saga context with timeout to prevent indefinite hangs
		sagaCtx := context.Background()
		if hasTenantCtx {
			sagaCtx = tenant.WithTenant(sagaCtx, tid)
		}
		sagaCtx, cancel := context.WithTimeout(sagaCtx, s.sagaTimeout)
		defer cancel()
		if s.tracer != nil {
			sagaCtx = observability.WithCorrelationID(sagaCtx, correlationID)
		}

		// Reload fresh state to avoid race with caller who may still reference po
		freshPO, err := s.repo.FindByID(sagaCtx, paymentOrderID)
		if err != nil {
			s.logger.Error("failed to reload payment order for saga",
				"payment_order_id", paymentOrderID.String(),
				"error", err)
			return
		}
		s.orchestrator.Orchestrate(sagaCtx, freshPO)
	}(po.ID, tenantID, hasTenant)

	// Prevent accidental access to po after goroutine launch - the goroutine
	// reloads fresh state from DB, so any access to po here would be stale
	po = nil

	return &pb.InitiatePaymentOrderResponse{
		PaymentOrder: responseProto,
	}, nil
}

// storeIdempotencyFailure stores a failure result for idempotency tracking.
// This is called when a request fails validation or processing, allowing
// subsequent retries with the same idempotency key to receive the same error.
func (s *Service) storeIdempotencyFailure(ctx context.Context, key idempotency.Key, errorMsg string) {
	result := idempotency.Result{
		Key:         key,
		Status:      idempotency.StatusFailed,
		Error:       errorMsg,
		CompletedAt: time.Now(),
		TTL:         idempotencyResultTTL,
	}
	if err := s.idempotencyService.StoreResult(ctx, result); err != nil {
		s.logger.Warn("failed to store idempotency failure result",
			"error", err,
			"idempotency_key", key.String())
	}
}
