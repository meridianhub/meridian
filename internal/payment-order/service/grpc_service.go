// Package service implements gRPC services for the payment order domain
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	"github.com/meridianhub/meridian/internal/current-account/clients"
	cadomain "github.com/meridianhub/meridian/internal/current-account/domain"
	"github.com/meridianhub/meridian/internal/payment-order/adapters/gateway"
	"github.com/meridianhub/meridian/internal/payment-order/adapters/persistence"
	"github.com/meridianhub/meridian/internal/payment-order/domain"
	"github.com/meridianhub/meridian/internal/platform/kafka"
	"github.com/meridianhub/meridian/internal/platform/observability"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Service errors
var (
	ErrRepositoryNil             = errors.New("repository cannot be nil")
	ErrCurrentAccountClientNil   = errors.New("current account client cannot be nil")
	ErrPaymentGatewayNil         = errors.New("payment gateway cannot be nil")
	ErrKafkaProducerNil          = errors.New("kafka producer cannot be nil")
	ErrCurrentAccountTargetEmpty = errors.New("current account target cannot be empty")
	ErrAmountRequired            = errors.New("amount is required")
	ErrPaymentRejected           = errors.New("payment rejected by gateway")
	ErrUnexpectedGatewayStatus   = errors.New("unexpected gateway status")
)

// Kafka topic constants
const (
	TopicPaymentOrderInitiated = "payment-order.initiated.v1"
	TopicPaymentOrderReserved  = "payment-order.reserved.v1"
	TopicPaymentOrderExecuting = "payment-order.executing.v1"
	TopicPaymentOrderCompleted = "payment-order.completed.v1"
	TopicPaymentOrderFailed    = "payment-order.failed.v1"
)

// CurrentAccountClient defines the interface for communicating with the CurrentAccount service
// for lien operations (fund reservation).
type CurrentAccountClient interface {
	// InitiateLien creates a fund reservation on an account
	InitiateLien(ctx context.Context, req *currentaccountv1.InitiateLienRequest) (*currentaccountv1.InitiateLienResponse, error)
	// TerminateLien releases a reservation without executing
	TerminateLien(ctx context.Context, req *currentaccountv1.TerminateLienRequest) (*currentaccountv1.TerminateLienResponse, error)
	// ExecuteLien converts a reservation to an actual debit
	ExecuteLien(ctx context.Context, req *currentaccountv1.ExecuteLienRequest) (*currentaccountv1.ExecuteLienResponse, error)
	// Close terminates the client connection
	Close() error
}

// Service implements the PaymentOrderService gRPC service
type Service struct {
	pb.UnimplementedPaymentOrderServiceServer
	repo                 persistence.Repository
	currentAccountClient CurrentAccountClient
	paymentGateway       gateway.PaymentGateway
	kafkaProducer        *kafka.ProtoProducer
	logger               *slog.Logger
	tracer               *observability.Tracer
}

// Config contains configuration for creating a new Service
type Config struct {
	Repository           persistence.Repository
	CurrentAccountClient CurrentAccountClient
	PaymentGateway       gateway.PaymentGateway
	KafkaProducer        *kafka.ProtoProducer
	Logger               *slog.Logger
	Tracer               *observability.Tracer
}

// NewService creates a new payment order service with minimal dependencies.
// This is primarily used for testing. For production use, prefer NewServiceWithClients.
func NewService(repo persistence.Repository) *Service {
	if repo == nil {
		panic("repository cannot be nil")
	}
	return &Service{
		repo:   repo,
		logger: slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}
}

// NewServiceWithConfig creates a new payment order service with full configuration.
// Validates all required dependencies and applies defaults where appropriate.
func NewServiceWithConfig(config Config) (*Service, error) {
	// Validate required dependencies
	if config.Repository == nil {
		return nil, ErrRepositoryNil
	}
	if config.CurrentAccountClient == nil {
		return nil, ErrCurrentAccountClientNil
	}
	if config.PaymentGateway == nil {
		return nil, ErrPaymentGatewayNil
	}
	if config.KafkaProducer == nil {
		return nil, ErrKafkaProducerNil
	}

	// Apply default logger if not provided
	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}

	return &Service{
		repo:                 config.Repository,
		currentAccountClient: config.CurrentAccountClient,
		paymentGateway:       config.PaymentGateway,
		kafkaProducer:        config.KafkaProducer,
		logger:               logger,
		tracer:               config.Tracer,
	}, nil
}

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

	// Get idempotency key
	idempotencyKey := req.IdempotencyKey.Key

	// Check for existing payment order with same idempotency key (idempotent)
	existingPO, err := s.repo.FindByIdempotencyKey(idempotencyKey)
	if err != nil && !errors.Is(err, persistence.ErrPaymentOrderNotFound) {
		s.logger.Error("failed to check idempotency", "error", err)
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
		return nil, status.Errorf(codes.InvalidArgument, "invalid amount: %v", err)
	}
	if !amount.IsPositive() {
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
		return nil, status.Errorf(codes.InvalidArgument, "failed to create payment order: %v", err)
	}

	// Persist to database
	if err := s.repo.Create(po); err != nil {
		s.logger.Error("failed to save payment order", "error", err)
		return nil, status.Error(codes.Internal, "failed to save payment order")
	}

	s.logger.Info("payment order created",
		"payment_order_id", po.ID.String(),
		"debtor_account_id", po.DebtorAccountID,
		"amount_cents", po.Amount.AmountCents(),
		"correlation_id", correlationID)

	// Publish PaymentOrderInitiated event to Kafka
	if s.kafkaProducer != nil {
		event := &eventsv1.PaymentOrderInitiatedEvent{
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
		}

		if err := s.kafkaProducer.Publish(ctx, TopicPaymentOrderInitiated, po.ID.String(), event); err != nil {
			s.logger.Error("failed to publish PaymentOrderInitiated event",
				"error", err,
				"payment_order_id", po.ID.String())
			// Don't fail the request - event will be recovered via outbox pattern or retry
		} else {
			s.logger.Info("published PaymentOrderInitiated event",
				"payment_order_id", po.ID.String(),
				"topic", TopicPaymentOrderInitiated)
		}
	}

	// Start saga orchestration asynchronously
	// The saga runs in the background after returning the response
	// nolint:contextcheck // Intentionally using background context for async saga orchestration
	go func() {
		sagaCtx := context.Background()
		if s.tracer != nil {
			sagaCtx = observability.WithCorrelationID(sagaCtx, correlationID)
		}
		s.orchestratePayment(sagaCtx, po)
	}()

	return &pb.InitiatePaymentOrderResponse{
		PaymentOrder: toProto(po),
	}, nil
}

// orchestratePayment executes the payment saga with compensation on failure.
// nolint:gocognit // Complex saga orchestration requires multiple nested steps
func (s *Service) orchestratePayment(ctx context.Context, po *domain.PaymentOrder) {
	s.logger.Info("starting payment saga",
		"payment_order_id", po.ID.String(),
		"correlation_id", po.CorrelationID)

	// Check if all dependencies are available
	if s.currentAccountClient == nil || s.paymentGateway == nil {
		s.logger.Error("saga dependencies not configured",
			"payment_order_id", po.ID.String())
		s.failPaymentOrder(ctx, po, "service configuration error", "INTERNAL_ERROR")
		return
	}

	// Create saga orchestrator
	saga := clients.NewSagaOrchestrator(s.logger)

	// Track saga state for compensation
	var lienID string

	// Step 1: Reserve funds via CurrentAccount.InitiateLien
	saga.AddStep("reserve_funds",
		// Action: Create lien to reserve funds
		func(stepCtx context.Context) error {
			s.logger.Info("executing reserve_funds step",
				"payment_order_id", po.ID.String(),
				"debtor_account_id", po.DebtorAccountID)

			resp, err := s.currentAccountClient.InitiateLien(stepCtx, &currentaccountv1.InitiateLienRequest{
				AccountId:             po.DebtorAccountID,
				Amount:                toMoneyAmount(po.Amount),
				PaymentOrderReference: po.ID.String(),
			})
			if err != nil {
				return fmt.Errorf("failed to reserve funds: %w", err)
			}

			lienID = resp.Lien.LienId

			// Update payment order with lien ID and transition to RESERVED
			if err := po.Reserve(lienID); err != nil {
				return fmt.Errorf("failed to transition to RESERVED: %w", err)
			}

			if err := s.repo.Update(po); err != nil {
				return fmt.Errorf("failed to update payment order: %w", err)
			}

			s.logger.Info("reserve_funds step completed",
				"payment_order_id", po.ID.String(),
				"lien_id", lienID)

			// Publish PaymentOrderReserved event
			if s.kafkaProducer != nil {
				event := &eventsv1.PaymentOrderReservedEvent{
					EventId:         uuid.New().String(),
					PaymentOrderId:  po.ID.String(),
					DebtorAccountId: po.DebtorAccountID,
					LienId:          lienID,
					Amount:          toMoneyAmount(po.Amount),
					CorrelationId:   po.CorrelationID,
					CausationId:     po.ID.String(),
					Timestamp:       timestamppb.Now(),
					Version:         int64(po.Version),
					IdempotencyKey:  po.IdempotencyKey,
				}
				if err := s.kafkaProducer.Publish(stepCtx, TopicPaymentOrderReserved, po.ID.String(), event); err != nil {
					s.logger.Error("failed to publish PaymentOrderReserved event", "error", err)
				}
			}

			return nil
		},
		// Compensate: Release lien
		func(stepCtx context.Context) error {
			if lienID == "" {
				s.logger.Warn("no lien to release in compensation")
				return nil
			}

			s.logger.Info("compensating reserve_funds step",
				"payment_order_id", po.ID.String(),
				"lien_id", lienID)

			_, err := s.currentAccountClient.TerminateLien(stepCtx, &currentaccountv1.TerminateLienRequest{
				LienId: lienID,
				Reason: fmt.Sprintf("Payment order %s saga compensation", po.ID.String()),
			})
			if err != nil {
				s.logger.Error("failed to release lien in compensation",
					"error", err,
					"lien_id", lienID)
				return err
			}

			s.logger.Info("reserve_funds compensation completed",
				"lien_id", lienID)

			return nil
		},
	)

	// Step 2: Send to payment gateway
	saga.AddStep("send_to_gateway",
		// Action: Send payment to gateway
		func(stepCtx context.Context) error {
			s.logger.Info("executing send_to_gateway step",
				"payment_order_id", po.ID.String())

			resp, err := s.paymentGateway.SendPayment(stepCtx, gateway.PaymentRequest{
				PaymentOrderID:    po.ID,
				DebtorAccountID:   po.DebtorAccountID,
				CreditorReference: po.CreditorReference,
				Amount:            po.Amount,
				IdempotencyKey:    po.IdempotencyKey,
			})
			if err != nil {
				return fmt.Errorf("failed to send payment to gateway: %w", err)
			}

			// Check gateway response status
			switch resp.Status {
			case gateway.StatusAccepted, gateway.StatusPending:
				// Transition to EXECUTING
				if err := po.Execute(resp.GatewayReferenceID); err != nil {
					return fmt.Errorf("failed to transition to EXECUTING: %w", err)
				}

				if err := s.repo.Update(po); err != nil {
					return fmt.Errorf("failed to update payment order: %w", err)
				}

				s.logger.Info("send_to_gateway step completed",
					"payment_order_id", po.ID.String(),
					"gateway_reference_id", resp.GatewayReferenceID,
					"gateway_status", resp.Status)

				// Publish PaymentOrderExecuting event
				if s.kafkaProducer != nil {
					event := &eventsv1.PaymentOrderExecutingEvent{
						EventId:            uuid.New().String(),
						PaymentOrderId:     po.ID.String(),
						GatewayReferenceId: resp.GatewayReferenceID,
						CorrelationId:      po.CorrelationID,
						CausationId:        po.ID.String(),
						Timestamp:          timestamppb.Now(),
						Version:            int64(po.Version),
						IdempotencyKey:     po.IdempotencyKey,
					}
					if err := s.kafkaProducer.Publish(stepCtx, TopicPaymentOrderExecuting, po.ID.String(), event); err != nil {
						s.logger.Error("failed to publish PaymentOrderExecuting event", "error", err)
					}
				}

				return nil

			case gateway.StatusRejected:
				return fmt.Errorf("%w: %s", ErrPaymentRejected, resp.Message)

			default:
				return fmt.Errorf("%w: %s", ErrUnexpectedGatewayStatus, resp.Status)
			}
		},
		// Compensate: Mark as failed (lien will be released by previous step's compensation)
		func(_ context.Context) error {
			s.logger.Info("send_to_gateway compensation (no-op - lien released by reserve_funds compensation)",
				"payment_order_id", po.ID.String())
			return nil
		},
	)

	// Execute saga
	result := saga.Execute(ctx)

	if !result.Success {
		s.logger.Error("payment saga failed",
			"payment_order_id", po.ID.String(),
			"failed_step", result.FailedStep,
			"error", result.Error,
			"completed_steps", result.CompletedSteps,
			"compensated_steps", result.CompensatedSteps)

		// Reload payment order to get latest state
		latestPO, err := s.repo.FindByID(po.ID)
		if err != nil {
			s.logger.Error("failed to reload payment order for failure handling", "error", err)
			return
		}

		s.failPaymentOrder(ctx, latestPO, result.Error.Error(), "SAGA_FAILED")
		return
	}

	s.logger.Info("payment saga completed successfully",
		"payment_order_id", po.ID.String(),
		"completed_steps", result.CompletedSteps)

	// Note: The payment is now in EXECUTING state, awaiting async gateway callback
	// via UpdatePaymentOrder to transition to COMPLETED or FAILED
}

// failPaymentOrder handles payment order failure with proper state transition and event publishing.
func (s *Service) failPaymentOrder(ctx context.Context, po *domain.PaymentOrder, reason string, errorCode string) {
	// Check if lien needs to be released before transitioning
	needsLienRelease := po.RequiresLienRelease()
	lienID := po.LienID

	// Transition to FAILED
	if err := po.Fail(reason, errorCode); err != nil {
		s.logger.Error("failed to transition to FAILED state",
			"error", err,
			"payment_order_id", po.ID.String())
		return
	}

	if err := s.repo.Update(po); err != nil {
		s.logger.Error("failed to persist FAILED state",
			"error", err,
			"payment_order_id", po.ID.String())
		return
	}

	// Release lien if needed
	if needsLienRelease && lienID != "" && s.currentAccountClient != nil {
		_, err := s.currentAccountClient.TerminateLien(ctx, &currentaccountv1.TerminateLienRequest{
			LienId: lienID,
			Reason: fmt.Sprintf("Payment order %s failed: %s", po.ID.String(), reason),
		})
		if err != nil {
			s.logger.Error("failed to release lien after failure",
				"error", err,
				"lien_id", lienID,
				"payment_order_id", po.ID.String())
		}
	}

	// Publish PaymentOrderFailed event
	if s.kafkaProducer != nil {
		event := &eventsv1.PaymentOrderFailedEvent{
			EventId:         uuid.New().String(),
			PaymentOrderId:  po.ID.String(),
			DebtorAccountId: po.DebtorAccountID,
			Amount:          toMoneyAmount(po.Amount),
			FailureReason:   reason,
			ErrorCode:       errorCode,
			FailedAtStatus:  mapStatusToProto(po.Status),
			LienId:          lienID,
			CorrelationId:   po.CorrelationID,
			CausationId:     po.ID.String(),
			Timestamp:       timestamppb.Now(),
			Version:         int64(po.Version),
			IdempotencyKey:  po.IdempotencyKey,
		}
		if err := s.kafkaProducer.Publish(ctx, TopicPaymentOrderFailed, po.ID.String(), event); err != nil {
			s.logger.Error("failed to publish PaymentOrderFailed event", "error", err)
		}
	}

	s.logger.Info("payment order failed",
		"payment_order_id", po.ID.String(),
		"reason", reason,
		"error_code", errorCode)
}

// RetrievePaymentOrder gets payment order details by ID.
func (s *Service) RetrievePaymentOrder(_ context.Context, req *pb.RetrievePaymentOrderRequest) (*pb.RetrievePaymentOrderResponse, error) {
	// Parse payment order ID
	poID, err := uuid.Parse(req.PaymentOrderId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid payment order ID: %v", err)
	}

	// Retrieve from repository
	po, err := s.repo.FindByID(poID)
	if err != nil {
		if errors.Is(err, persistence.ErrPaymentOrderNotFound) {
			return nil, status.Errorf(codes.NotFound, "payment order not found: %s", req.PaymentOrderId)
		}
		s.logger.Error("failed to retrieve payment order", "error", err)
		return nil, status.Error(codes.Internal, "failed to retrieve payment order")
	}

	return &pb.RetrievePaymentOrderResponse{
		PaymentOrder: toProto(po),
	}, nil
}

// UpdatePaymentOrder handles asynchronous gateway callbacks.
// nolint:gocognit // Gateway callback handling requires multiple state transitions
func (s *Service) UpdatePaymentOrder(ctx context.Context, req *pb.UpdatePaymentOrderRequest) (*pb.UpdatePaymentOrderResponse, error) {
	start := time.Now()
	defer func() {
		s.logger.Info("update_payment_order completed",
			"duration_ms", time.Since(start).Milliseconds())
	}()

	// Lookup payment order
	var po *domain.PaymentOrder
	var err error

	if req.PaymentOrderId != "" {
		poID, parseErr := uuid.Parse(req.PaymentOrderId)
		if parseErr != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid payment order ID: %v", parseErr)
		}
		po, err = s.repo.FindByID(poID)
	} else if req.GatewayReferenceId != "" {
		po, err = s.repo.FindByGatewayReferenceID(req.GatewayReferenceId)
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

	// Process based on gateway status
	switch req.GatewayStatus {
	case pb.GatewayStatus_GATEWAY_STATUS_SETTLED:
		// Complete the payment
		if err := po.Complete(""); err != nil {
			if errors.Is(err, domain.ErrInvalidPaymentOrderTransition) {
				// Already completed (idempotent)
				if po.Status == domain.PaymentOrderStatusCompleted {
					return &pb.UpdatePaymentOrderResponse{PaymentOrder: toProto(po)}, nil
				}
				return nil, status.Errorf(codes.FailedPrecondition, "cannot complete payment: %v", err)
			}
			return nil, status.Errorf(codes.Internal, "failed to complete payment: %v", err)
		}

		if err := s.repo.Update(po); err != nil {
			s.logger.Error("failed to update payment order", "error", err)
			return nil, status.Error(codes.Internal, "failed to update payment order")
		}

		// Execute lien (convert reservation to actual debit)
		if s.currentAccountClient != nil && po.LienID != "" {
			_, execErr := s.currentAccountClient.ExecuteLien(ctx, &currentaccountv1.ExecuteLienRequest{
				LienId: po.LienID,
			})
			if execErr != nil {
				s.logger.Error("failed to execute lien", "error", execErr, "lien_id", po.LienID)
				// Continue - the payment is still complete, lien execution can be retried
			}
		}

		// Publish PaymentOrderCompleted event
		if s.kafkaProducer != nil {
			event := &eventsv1.PaymentOrderCompletedEvent{
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
			}
			if err := s.kafkaProducer.Publish(ctx, TopicPaymentOrderCompleted, po.ID.String(), event); err != nil {
				s.logger.Error("failed to publish PaymentOrderCompleted event", "error", err)
			}
		}

		s.logger.Info("payment order completed",
			"payment_order_id", po.ID.String(),
			"gateway_reference_id", po.GatewayReferenceID)

	case pb.GatewayStatus_GATEWAY_STATUS_REJECTED:
		// Fail the payment
		s.failPaymentOrder(ctx, po, req.GatewayMessage, "GATEWAY_REJECTED")

	case pb.GatewayStatus_GATEWAY_STATUS_PENDING:
		// No state change needed - still waiting
		s.logger.Info("payment still pending at gateway",
			"payment_order_id", po.ID.String(),
			"gateway_reference_id", req.GatewayReferenceId)

	case pb.GatewayStatus_GATEWAY_STATUS_UNSPECIFIED:
		return nil, status.Error(codes.InvalidArgument, "gateway status is required")
	}

	return &pb.UpdatePaymentOrderResponse{
		PaymentOrder: toProto(po),
	}, nil
}

// CancelPaymentOrder cancels a payment order before completion.
func (s *Service) CancelPaymentOrder(ctx context.Context, req *pb.CancelPaymentOrderRequest) (*pb.CancelPaymentOrderResponse, error) {
	// Parse payment order ID
	poID, err := uuid.Parse(req.PaymentOrderId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid payment order ID: %v", err)
	}

	// Retrieve payment order
	po, err := s.repo.FindByID(poID)
	if err != nil {
		if errors.Is(err, persistence.ErrPaymentOrderNotFound) {
			return nil, status.Errorf(codes.NotFound, "payment order not found: %s", req.PaymentOrderId)
		}
		return nil, status.Error(codes.Internal, "failed to retrieve payment order")
	}

	// Check if already cancelled (idempotent)
	if po.Status == domain.PaymentOrderStatusCancelled {
		return &pb.CancelPaymentOrderResponse{PaymentOrder: toProto(po)}, nil
	}

	// Check if can be cancelled
	if !po.CanCancel() {
		return nil, status.Errorf(codes.FailedPrecondition,
			"payment order cannot be cancelled in status %s", po.Status)
	}

	// Check if lien needs to be released
	needsLienRelease := po.RequiresLienRelease()
	lienID := po.LienID

	// Cancel the payment order
	if err := po.Cancel(req.CancellationReason); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to cancel payment order: %v", err)
	}

	if err := s.repo.Update(po); err != nil {
		return nil, status.Error(codes.Internal, "failed to update payment order")
	}

	// Release lien if needed
	if needsLienRelease && lienID != "" && s.currentAccountClient != nil {
		_, termErr := s.currentAccountClient.TerminateLien(ctx, &currentaccountv1.TerminateLienRequest{
			LienId: lienID,
			Reason: fmt.Sprintf("Payment order %s cancelled: %s", po.ID.String(), req.CancellationReason),
		})
		if termErr != nil {
			s.logger.Error("failed to release lien after cancellation",
				"error", termErr,
				"lien_id", lienID)
			// Continue - cancellation succeeded, lien release can be retried
		}
	}

	// Publish PaymentOrderCancelled event
	if s.kafkaProducer != nil {
		event := &eventsv1.PaymentOrderCancelledEvent{
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
		}
		if err := s.kafkaProducer.Publish(ctx, "payment-order.cancelled.v1", po.ID.String(), event); err != nil {
			s.logger.Error("failed to publish PaymentOrderCancelled event", "error", err)
		}
	}

	s.logger.Info("payment order cancelled",
		"payment_order_id", po.ID.String(),
		"reason", req.CancellationReason,
		"cancelled_by", req.CancelledBy)

	return &pb.CancelPaymentOrderResponse{
		PaymentOrder: toProto(po),
	}, nil
}

// ListPaymentOrders returns a paginated list of payment orders.
func (s *Service) ListPaymentOrders(_ context.Context, req *pb.ListPaymentOrdersRequest) (*pb.ListPaymentOrdersResponse, error) {
	// For now, implement a simple filter by debtor account ID
	if req.DebtorAccountId == "" {
		return nil, status.Error(codes.InvalidArgument, "debtor_account_id is required for listing")
	}

	paymentOrders, err := s.repo.FindByDebtorAccountID(req.DebtorAccountId)
	if err != nil {
		s.logger.Error("failed to list payment orders", "error", err)
		return nil, status.Error(codes.Internal, "failed to list payment orders")
	}

	// Convert to proto
	protoOrders := make([]*pb.PaymentOrder, 0, len(paymentOrders))
	for _, po := range paymentOrders {
		protoOrders = append(protoOrders, toProto(po))
	}

	return &pb.ListPaymentOrdersResponse{
		PaymentOrders: protoOrders,
		Pagination: &commonpb.PaginationResponse{
			TotalCount: int64(len(protoOrders)),
		},
	}, nil
}

// Helper functions

// toProto converts a domain PaymentOrder to proto PaymentOrder
func toProto(po *domain.PaymentOrder) *pb.PaymentOrder {
	proto := &pb.PaymentOrder{
		PaymentOrderId:     po.ID.String(),
		DebtorAccountId:    po.DebtorAccountID,
		CreditorReference:  po.CreditorReference,
		Amount:             toMoneyAmount(po.Amount),
		Status:             mapStatusToProto(po.Status),
		LienId:             po.LienID,
		GatewayReferenceId: po.GatewayReferenceID,
		CorrelationId:      po.CorrelationID,
		IdempotencyKey:     po.IdempotencyKey,
		FailureReason:      po.FailureReason,
		CreatedAt:          timestamppb.New(po.CreatedAt),
		UpdatedAt:          timestamppb.New(po.UpdatedAt),
		Version:            int64(po.Version),
		LedgerBookingId:    po.LedgerBookingID,
		CausationId:        po.CausationID,
		ErrorCode:          po.ErrorCode,
	}

	// Set optional timestamps
	if po.ReservedAt != nil {
		proto.ReservedAt = timestamppb.New(*po.ReservedAt)
	}
	if po.ExecutingAt != nil {
		proto.ExecutingAt = timestamppb.New(*po.ExecutingAt)
	}
	if po.CompletedAt != nil {
		proto.CompletedAt = timestamppb.New(*po.CompletedAt)
	}
	if po.FailedAt != nil {
		proto.FailedAt = timestamppb.New(*po.FailedAt)
	}
	if po.CancelledAt != nil {
		proto.CancelledAt = timestamppb.New(*po.CancelledAt)
	}
	if po.ReversedAt != nil {
		proto.ReversedAt = timestamppb.New(*po.ReversedAt)
	}

	return proto
}

// toMoneyAmount converts domain Money to proto MoneyAmount
func toMoneyAmount(m cadomain.Money) *commonpb.MoneyAmount {
	amountCents := m.AmountCents()
	units := amountCents / 100
	remainder := amountCents % 100
	// #nosec G115 - remainder is always -99 to 99
	nanos := int32(remainder * 10000000)

	return &commonpb.MoneyAmount{
		Amount: &money.Money{
			CurrencyCode: m.Currency(),
			Units:        units,
			Nanos:        nanos,
		},
	}
}

// protoToMoney converts proto MoneyAmount to domain Money
func protoToMoney(amount *commonpb.MoneyAmount) (cadomain.Money, error) {
	if amount == nil || amount.Amount == nil {
		return cadomain.Money{}, ErrAmountRequired
	}

	// Convert to cents
	unitsCents := amount.Amount.Units * 100
	nanosCents := (amount.Amount.Nanos + 5000000) / 10000000
	totalCents := unitsCents + int64(nanosCents)

	return cadomain.NewMoney(amount.Amount.CurrencyCode, totalCents)
}

// mapStatusToProto maps domain status to proto status
func mapStatusToProto(status domain.PaymentOrderStatus) pb.PaymentOrderStatus {
	switch status {
	case domain.PaymentOrderStatusInitiated:
		return pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_INITIATED
	case domain.PaymentOrderStatusReserved:
		return pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_RESERVED
	case domain.PaymentOrderStatusExecuting:
		return pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_EXECUTING
	case domain.PaymentOrderStatusCompleted:
		return pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_COMPLETED
	case domain.PaymentOrderStatusFailed:
		return pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_FAILED
	case domain.PaymentOrderStatusCancelled:
		return pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_CANCELLED
	case domain.PaymentOrderStatusReversed:
		return pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_REVERSED
	default:
		return pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_UNSPECIFIED
	}
}
