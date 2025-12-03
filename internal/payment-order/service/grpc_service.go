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
	poobservability "github.com/meridianhub/meridian/internal/payment-order/observability"
	"github.com/meridianhub/meridian/internal/platform/observability"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Service errors
var (
	ErrRepositoryNil           = errors.New("repository cannot be nil")
	ErrCurrentAccountClientNil = errors.New("current account client cannot be nil")
	ErrPaymentGatewayNil       = errors.New("payment gateway cannot be nil")
	ErrAmountRequired          = errors.New("amount is required")
	ErrInvalidNanos            = errors.New("nanos must be in range [-999999999, 999999999]")
	ErrPaymentRejected         = errors.New("payment rejected by gateway")
	ErrUnexpectedGatewayStatus = errors.New("unexpected gateway status")
	ErrIdempotencyKeyTooLong   = errors.New("idempotency key exceeds maximum length")
	ErrMalformedLienResponse   = errors.New("current account service returned empty or malformed lien response")
)

// Kafka topic constants
const (
	TopicPaymentOrderInitiated = "payment-order.initiated.v1"
	TopicPaymentOrderReserved  = "payment-order.reserved.v1"
	TopicPaymentOrderExecuting = "payment-order.executing.v1"
	TopicPaymentOrderCompleted = "payment-order.completed.v1"
	TopicPaymentOrderFailed    = "payment-order.failed.v1"
	TopicPaymentOrderCancelled = "payment-order.cancelled.v1"
	TopicPaymentOrderReversed  = "payment-order.reversed.v1"
)

// Operation result status constants for observability
const (
	opStatusSuccess    = "success"
	opStatusError      = "error"
	opStatusIdempotent = "idempotent"
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

// KafkaPublisher defines the interface for publishing protobuf messages to Kafka.
// This abstraction allows for mocking in tests and alternative implementations.
type KafkaPublisher interface {
	// Publish sends a protobuf message to the specified Kafka topic.
	Publish(ctx context.Context, topic string, key string, msg proto.Message) error
}

// Configuration defaults
const (
	// DefaultSagaTimeout is the default timeout for payment saga orchestration.
	// This allows for typical payment gateway latency plus retries.
	DefaultSagaTimeout = 5 * time.Minute

	// DefaultPageSize is the default number of items per page for list operations.
	DefaultPageSize = 50

	// DefaultMaxPageSize is the maximum allowed page size for list operations.
	DefaultMaxPageSize = 1000

	// DefaultMaxIdempotencyKeyLength is the maximum allowed length for idempotency keys.
	DefaultMaxIdempotencyKeyLength = 256

	// DefaultLienExecutionMaxRetries is the maximum number of retry attempts for ExecuteLien.
	DefaultLienExecutionMaxRetries = 5

	// DefaultLienExecutionRetryTimeout is the timeout for the entire retry sequence.
	DefaultLienExecutionRetryTimeout = 2 * time.Minute

	// lienStatusUpdateMaxRetries is the number of times to retry status updates on version conflict.
	lienStatusUpdateMaxRetries = 5
	// lienStatusUpdateBackoffBase is the base duration for exponential backoff between retries.
	lienStatusUpdateBackoffBase = 100 * time.Millisecond
	// lienStatusUpdateTimeout is the timeout for the entire status update operation.
	lienStatusUpdateTimeout = 30 * time.Second
)

// Money conversion constants for Google Money proto (nanos have 9 decimal places)
const (
	// nanosPerCent is the number of nanos in one cent (1 cent = 0.01 = 10^7 nanos)
	nanosPerCent = 10000000
	// nanosRoundingOffset is half a cent in nanos, used for rounding
	nanosRoundingOffset = 5000000
)

// Service implements the PaymentOrderService gRPC service
type Service struct {
	pb.UnimplementedPaymentOrderServiceServer
	repo                     persistence.Repository
	currentAccountClient     CurrentAccountClient
	paymentGateway           gateway.PaymentGateway
	kafkaPublisher           KafkaPublisher
	logger                   *slog.Logger
	tracer                   *observability.Tracer
	sagaTimeout              time.Duration
	defaultPageSize          int
	maxPageSize              int
	maxIdempotencyKeyLength  int
	lienExecutionRetryConfig *clients.RetryConfig // nil means use default
}

// Config contains configuration for creating a new Service
type Config struct {
	Repository           persistence.Repository
	CurrentAccountClient CurrentAccountClient
	PaymentGateway       gateway.PaymentGateway
	KafkaPublisher       KafkaPublisher
	Logger               *slog.Logger
	Tracer               *observability.Tracer
	// SagaTimeout is the maximum duration for saga orchestration.
	// If zero, DefaultSagaTimeout is used.
	SagaTimeout time.Duration
	// DefaultPageSize is the default number of items per page. If zero, DefaultPageSize is used.
	DefaultPageSize int
	// MaxPageSize is the maximum allowed page size. If zero, DefaultMaxPageSize is used.
	MaxPageSize int
	// MaxIdempotencyKeyLength is the maximum allowed idempotency key length.
	// If zero, DefaultMaxIdempotencyKeyLength is used.
	MaxIdempotencyKeyLength int
	// LienExecutionRetryConfig configures retry behavior for async lien execution.
	// If nil, default retry config is used. Primarily useful for testing.
	LienExecutionRetryConfig *clients.RetryConfig
}

// NewService creates a new payment order service with minimal dependencies.
// This is primarily used for testing. For production use, prefer NewServiceWithConfig.
// Returns ErrRepositoryNil if the repository is nil.
func NewService(repo persistence.Repository) (*Service, error) {
	if repo == nil {
		return nil, ErrRepositoryNil
	}
	return &Service{
		repo:                    repo,
		logger:                  slog.New(slog.NewJSONHandler(os.Stdout, nil)),
		sagaTimeout:             DefaultSagaTimeout,
		defaultPageSize:         DefaultPageSize,
		maxPageSize:             DefaultMaxPageSize,
		maxIdempotencyKeyLength: DefaultMaxIdempotencyKeyLength,
	}, nil
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
	// KafkaPublisher is optional - nil is handled gracefully by publishEvent

	// Apply default logger if not provided
	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}

	// Apply defaults for optional config values
	sagaTimeout := config.SagaTimeout
	if sagaTimeout == 0 {
		sagaTimeout = DefaultSagaTimeout
	}

	defaultPageSize := config.DefaultPageSize
	if defaultPageSize == 0 {
		defaultPageSize = DefaultPageSize
	}

	maxPageSize := config.MaxPageSize
	if maxPageSize == 0 {
		maxPageSize = DefaultMaxPageSize
	}

	maxIdempotencyKeyLength := config.MaxIdempotencyKeyLength
	if maxIdempotencyKeyLength == 0 {
		maxIdempotencyKeyLength = DefaultMaxIdempotencyKeyLength
	}

	return &Service{
		repo:                     config.Repository,
		currentAccountClient:     config.CurrentAccountClient,
		paymentGateway:           config.PaymentGateway,
		kafkaPublisher:           config.KafkaPublisher,
		logger:                   logger,
		tracer:                   config.Tracer,
		sagaTimeout:              sagaTimeout,
		defaultPageSize:          defaultPageSize,
		maxPageSize:              maxPageSize,
		maxIdempotencyKeyLength:  maxIdempotencyKeyLength,
		lienExecutionRetryConfig: config.LienExecutionRetryConfig, // nil means use default
	}, nil
}

// InitiatePaymentOrder creates a new payment order and begins the saga.
// nolint:gocognit // Complexity justified by validation, idempotency handling (TOCTOU), event publishing, and saga startup
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

	// Check for existing payment order with same idempotency key (idempotent).
	// Note: This check has a TOCTOU race window where concurrent requests with the same
	// idempotency key could both pass this check. The database unique constraint on
	// idempotency_key is the authoritative guard - concurrent inserts will fail with a
	// constraint violation and should be handled by returning the existing record.
	existingPO, err := s.repo.FindByIdempotencyKey(ctx, idempotencyKey)
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
	if err := s.repo.Create(ctx, po); err != nil {
		// Handle idempotency key conflict (TOCTOU race): another request won the race
		// Reload and return the existing payment order for idempotent behavior
		if errors.Is(err, persistence.ErrIdempotencyKeyConflict) {
			existingPO, findErr := s.repo.FindByIdempotencyKey(ctx, idempotencyKey)
			if findErr != nil {
				s.logger.Error("failed to retrieve existing payment order after idempotency conflict",
					"error", findErr,
					"idempotency_key", idempotencyKey)
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
		return nil, status.Error(codes.Internal, "failed to save payment order")
	}

	s.logger.Info("payment order created",
		"payment_order_id", po.ID.String(),
		"debtor_account_id", po.DebtorAccountID,
		"amount_cents", po.Amount.AmountCents(),
		"currency", po.Amount.Currency(),
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

	// Start saga orchestration asynchronously
	// The saga runs in the background after returning the response
	// nolint:contextcheck // Intentionally using background context for async saga orchestration
	go func(paymentOrderID uuid.UUID) {
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
		sagaCtx, cancel := context.WithTimeout(context.Background(), s.sagaTimeout)
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
		s.orchestratePayment(sagaCtx, freshPO)
	}(po.ID)

	// Prevent accidental access to po after goroutine launch - the goroutine
	// reloads fresh state from DB, so any access to po here would be stale
	po = nil //nolint:ineffassign // Intentional: prevent future code from accidentally using stale po

	return &pb.InitiatePaymentOrderResponse{
		PaymentOrder: responseProto,
	}, nil
}

// orchestratePayment executes the payment saga with compensation on failure.
// The saga steps (reserve_funds, send_to_gateway) are executed strictly sequentially by
// the SagaOrchestrator - there is no concurrent step execution. The same PaymentOrder
// pointer is safely shared across steps since only one step runs at a time.
// Compensation is also sequential, running in reverse order (LIFO) on failure.
func (s *Service) orchestratePayment(ctx context.Context, po *domain.PaymentOrder) {
	s.logger.Info("starting payment saga",
		"payment_order_id", po.ID.String(),
		"correlation_id", po.CorrelationID)

	// Check if all dependencies are available
	if s.currentAccountClient == nil || s.paymentGateway == nil {
		s.logger.Error("saga dependencies not configured",
			"payment_order_id", po.ID.String())
		// Async path: log and swallow error - best effort failure handling
		if err := s.failPaymentOrder(ctx, po, "service configuration error", "INTERNAL_ERROR"); err != nil {
			s.logger.Error("failed to mark payment order as failed",
				"payment_order_id", po.ID.String(),
				"error", err)
		}
		return
	}

	// Create saga orchestrator and track lien state for compensation
	saga := clients.NewSagaOrchestrator(s.logger)
	var lienID string

	// Add saga steps
	s.addReserveFundsStep(saga, po, &lienID)
	s.addSendToGatewayStep(saga, po)

	// Execute saga
	result := saga.Execute(ctx)
	s.handleSagaResult(ctx, po, result)
}

// addReserveFundsStep adds the reserve_funds saga step that creates a lien to reserve funds.
func (s *Service) addReserveFundsStep(saga *clients.SagaOrchestrator, po *domain.PaymentOrder, lienID *string) {
	saga.AddStep("reserve_funds",
		// Action: Create lien to reserve funds
		func(stepCtx context.Context) error {
			// Check context cancellation early to avoid unnecessary work
			if err := stepCtx.Err(); err != nil {
				return fmt.Errorf("context cancelled before reserve_funds: %w", err)
			}

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

			// Defensive check: ensure response is well-formed to avoid panics
			if resp == nil || resp.Lien == nil || resp.Lien.LienId == "" {
				return ErrMalformedLienResponse
			}

			*lienID = resp.Lien.LienId

			// Update payment order with lien ID and transition to RESERVED
			if err := po.Reserve(*lienID); err != nil {
				return fmt.Errorf("failed to transition to RESERVED: %w", err)
			}

			if err := s.repo.Update(stepCtx, po); err != nil {
				return fmt.Errorf("failed to update payment order: %w", err)
			}

			s.logger.Info("reserve_funds step completed",
				"payment_order_id", po.ID.String(),
				"lien_id", *lienID)

			// Publish PaymentOrderReserved event
			s.publishEvent(stepCtx, TopicPaymentOrderReserved, po.ID.String(), &eventsv1.PaymentOrderReservedEvent{
				EventId:         uuid.New().String(),
				PaymentOrderId:  po.ID.String(),
				DebtorAccountId: po.DebtorAccountID,
				LienId:          *lienID,
				Amount:          toMoneyAmount(po.Amount),
				CorrelationId:   po.CorrelationID,
				CausationId:     po.ID.String(),
				Timestamp:       timestamppb.Now(),
				Version:         int64(po.Version),
				IdempotencyKey:  po.IdempotencyKey,
			})

			return nil
		},
		// Compensate: Release lien
		func(stepCtx context.Context) error {
			if *lienID == "" {
				s.logger.Warn("no lien to release in compensation")
				return nil
			}

			s.logger.Info("compensating reserve_funds step",
				"payment_order_id", po.ID.String(),
				"lien_id", *lienID)

			_, err := s.currentAccountClient.TerminateLien(stepCtx, &currentaccountv1.TerminateLienRequest{
				LienId: *lienID,
				Reason: fmt.Sprintf("Payment order %s saga compensation", po.ID.String()),
			})
			if err != nil {
				s.logger.Error("failed to release lien in compensation",
					"error", err,
					"lien_id", *lienID)
				return err
			}

			s.logger.Info("reserve_funds compensation completed",
				"lien_id", *lienID)

			return nil
		},
	)
}

// addSendToGatewayStep adds the send_to_gateway saga step that sends payment to the external gateway.
func (s *Service) addSendToGatewayStep(saga *clients.SagaOrchestrator, po *domain.PaymentOrder) {
	saga.AddStep("send_to_gateway",
		// Action: Send payment to gateway
		func(stepCtx context.Context) error {
			// Check context cancellation early to avoid unnecessary work
			if err := stepCtx.Err(); err != nil {
				return fmt.Errorf("context cancelled before send_to_gateway: %w", err)
			}

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

			return s.processGatewayResponse(stepCtx, po, resp)
		},
		// Compensate: No-op (lien will be released by reserve_funds compensation)
		func(_ context.Context) error {
			s.logger.Info("send_to_gateway compensation (no-op - lien released by reserve_funds compensation)",
				"payment_order_id", po.ID.String())
			return nil
		},
	)
}

// processGatewayResponse handles the gateway response and transitions payment order state.
func (s *Service) processGatewayResponse(ctx context.Context, po *domain.PaymentOrder, resp gateway.PaymentResponse) error {
	switch resp.Status {
	case gateway.StatusAccepted, gateway.StatusPending:
		// Transition to EXECUTING
		if err := po.Execute(resp.GatewayReferenceID); err != nil {
			return fmt.Errorf("failed to transition to EXECUTING: %w", err)
		}

		if err := s.repo.Update(ctx, po); err != nil {
			return fmt.Errorf("failed to update payment order: %w", err)
		}

		s.logger.Info("send_to_gateway step completed",
			"payment_order_id", po.ID.String(),
			"gateway_reference_id", resp.GatewayReferenceID,
			"gateway_status", resp.Status)

		// Publish PaymentOrderExecuting event
		s.publishEvent(ctx, TopicPaymentOrderExecuting, po.ID.String(), &eventsv1.PaymentOrderExecutingEvent{
			EventId:            uuid.New().String(),
			PaymentOrderId:     po.ID.String(),
			GatewayReferenceId: resp.GatewayReferenceID,
			CorrelationId:      po.CorrelationID,
			CausationId:        po.ID.String(),
			Timestamp:          timestamppb.Now(),
			Version:            int64(po.Version),
			IdempotencyKey:     po.IdempotencyKey,
		})

		return nil

	case gateway.StatusRejected:
		return fmt.Errorf("%w: %s", ErrPaymentRejected, resp.Message)

	default:
		return fmt.Errorf("%w: %s", ErrUnexpectedGatewayStatus, resp.Status)
	}
}

// handleSagaResult processes the saga execution result and handles failure scenarios.
func (s *Service) handleSagaResult(ctx context.Context, po *domain.PaymentOrder, result clients.SagaResult) {
	if !result.Success {
		s.logger.Error("payment saga failed",
			"payment_order_id", po.ID.String(),
			"failed_step", result.FailedStep,
			"error", result.Error,
			"completed_steps", result.CompletedSteps,
			"compensated_steps", result.CompensatedSteps)

		// Reload payment order to get latest state
		latestPO, err := s.repo.FindByID(ctx, po.ID)
		if err != nil {
			s.logger.Error("failed to reload payment order for failure handling", "error", err)
			return
		}

		// Async path: log and swallow error - best effort failure handling
		if err := s.failPaymentOrder(ctx, latestPO, result.Error.Error(), "SAGA_FAILED"); err != nil {
			s.logger.Error("failed to mark payment order as failed after saga failure",
				"payment_order_id", po.ID.String(),
				"error", err)
		}
		return
	}

	s.logger.Info("payment saga completed successfully",
		"payment_order_id", po.ID.String(),
		"completed_steps", result.CompletedSteps)

	// Note: The payment is now in EXECUTING state, awaiting async gateway callback
	// via UpdatePaymentOrder to transition to COMPLETED or FAILED
}

// failPaymentOrder handles payment order failure with proper state transition and event publishing.
// Returns an error if the state transition or persistence fails. Callers in synchronous paths
// (e.g., UpdatePaymentOrder) should propagate this error to clients. Callers in async paths
// (e.g., saga orchestration) may log and swallow the error.
func (s *Service) failPaymentOrder(ctx context.Context, po *domain.PaymentOrder, reason string, errorCode string) error {
	// Capture original status before transitioning (for event)
	failedAtStatus := po.Status

	// Check if lien needs to be released before transitioning
	needsLienRelease := po.RequiresLienRelease()
	lienID := po.LienID

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
		"idempotency_key", po.IdempotencyKey,
		"correlation_id", po.CorrelationID)

	return nil
}

// RetrievePaymentOrder gets payment order details by ID.
func (s *Service) RetrievePaymentOrder(ctx context.Context, req *pb.RetrievePaymentOrderRequest) (*pb.RetrievePaymentOrderResponse, error) {
	// Parse payment order ID
	poID, err := uuid.Parse(req.PaymentOrderId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid payment order ID: %v", err)
	}

	// Retrieve from repository
	po, err := s.repo.FindByID(ctx, poID)
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
// Implements idempotency, audit logging, and observability per task 11 requirements.
// nolint:gocognit // Gateway callback handling requires multiple state transitions and idempotency checks
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

	// Lookup payment order by ID or gateway reference ID
	po, err := s.lookupPaymentOrder(ctx, req)
	if err != nil {
		operationStatus = opStatusError
		return nil, err
	}

	s.logger.Info("processing gateway callback",
		"payment_order_id", po.ID.String(),
		"gateway_reference_id", po.GatewayReferenceID,
		"current_status", po.Status,
		"gateway_status", gatewayStatusStr,
		"correlation_id", po.CorrelationID)

	// Process based on gateway status
	switch req.GatewayStatus {
	case pb.GatewayStatus_GATEWAY_STATUS_SETTLED:
		result, err := s.handleSettledStatus(ctx, po)
		if err != nil {
			operationStatus = opStatusError
			return nil, err
		}
		if result.isIdempotent {
			operationStatus = opStatusIdempotent
		}
		return &pb.UpdatePaymentOrderResponse{PaymentOrder: toProto(result.po)}, nil

	case pb.GatewayStatus_GATEWAY_STATUS_REJECTED:
		result, err := s.handleRejectedStatus(ctx, po, req.GatewayMessage)
		if err != nil {
			operationStatus = opStatusError
			return nil, err
		}
		if result.isIdempotent {
			operationStatus = opStatusIdempotent
		}
		return &pb.UpdatePaymentOrderResponse{PaymentOrder: toProto(result.po)}, nil

	case pb.GatewayStatus_GATEWAY_STATUS_PENDING:
		if err := s.handlePendingStatus(po, req.GatewayReferenceId); err != nil {
			operationStatus = opStatusError
			return nil, err
		}
		return &pb.UpdatePaymentOrderResponse{PaymentOrder: toProto(po)}, nil

	case pb.GatewayStatus_GATEWAY_STATUS_UNSPECIFIED:
		operationStatus = opStatusError
		return nil, status.Error(codes.InvalidArgument, "gateway status is required")

	default:
		operationStatus = opStatusError
		return nil, status.Errorf(codes.InvalidArgument, "unknown gateway status: %v", req.GatewayStatus)
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

// updateResult holds the result of a status update operation.
type updateResult struct {
	po           *domain.PaymentOrder
	isIdempotent bool
}

// handleSettledStatus processes a SETTLED gateway callback.
// Implements idempotency: returns success if already COMPLETED.
func (s *Service) handleSettledStatus(ctx context.Context, po *domain.PaymentOrder) (*updateResult, error) {
	// Idempotency check: if already completed, return success without modification
	if po.Status == domain.PaymentOrderStatusCompleted {
		s.logger.Info("idempotent settled callback - payment already completed",
			"payment_order_id", po.ID.String(),
			"correlation_id", po.CorrelationID)
		poobservability.RecordIdempotentRequest("update_payment_order_settled")
		return &updateResult{po: po, isIdempotent: true}, nil
	}

	// Attempt state transition
	if err := po.Complete(""); err != nil {
		if errors.Is(err, domain.ErrInvalidPaymentOrderTransition) {
			return nil, status.Errorf(codes.FailedPrecondition,
				"cannot complete payment order in %s state: %v", po.Status, err)
		}
		return nil, status.Errorf(codes.Internal, "failed to complete payment: %v", err)
	}

	// Mark lien execution as pending before saving
	if po.LienID != "" {
		po.SetLienExecutionPending()
	}

	// Persist state change
	if err := s.repo.Update(ctx, po); err != nil {
		s.logger.Error("failed to update payment order to COMPLETED",
			"error", err,
			"payment_order_id", po.ID.String())
		return nil, status.Error(codes.Internal, "failed to update payment order")
	}

	// Record metrics
	poobservability.RecordCompletion(po.Amount.Currency())
	poobservability.RecordPaymentAmount(po.Amount.Currency(), "completed", po.Amount.AmountCents())

	// Execute lien asynchronously with retry mechanism
	// The lien execution status is tracked in the payment order for reconciliation
	if s.currentAccountClient != nil && po.LienID != "" {
		// Start async retry goroutine - this won't block the webhook response
		// We create a new background context since the request context will be cancelled
		// after the webhook response is sent
		//nolint:contextcheck // Intentionally using fresh context for async operation
		go s.executeLienWithRetry(context.Background(), po.ID, po.LienID)
	}

	// Publish PaymentOrderCompleted event
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

	// Audit log for successful completion
	s.logger.Info("payment order completed via gateway callback",
		"payment_order_id", po.ID.String(),
		"gateway_reference_id", po.GatewayReferenceID,
		"amount_cents", po.Amount.AmountCents(),
		"currency", po.Amount.Currency(),
		"lien_id", po.LienID,
		"idempotency_key", po.IdempotencyKey,
		"correlation_id", po.CorrelationID)

	return &updateResult{po: po, isIdempotent: false}, nil
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
	poobservability.RecordRejection(po.Amount.Currency(), poobservability.ErrorCategoryGatewayRejected)
	poobservability.RecordPaymentAmount(po.Amount.Currency(), "rejected", po.Amount.AmountCents())

	// Audit log for rejection
	s.logger.Info("payment order rejected via gateway callback",
		"payment_order_id", po.ID.String(),
		"gateway_reference_id", po.GatewayReferenceID,
		"gateway_message", gatewayMessage,
		"amount_cents", po.Amount.AmountCents(),
		"currency", po.Amount.Currency(),
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

// CancelPaymentOrder cancels a payment order before completion.
func (s *Service) CancelPaymentOrder(ctx context.Context, req *pb.CancelPaymentOrderRequest) (*pb.CancelPaymentOrderResponse, error) {
	// Validate cancellation reason - required for audit purposes
	if req.CancellationReason == "" {
		return nil, status.Error(codes.InvalidArgument, "cancellation_reason is required")
	}

	// Parse payment order ID
	poID, err := uuid.Parse(req.PaymentOrderId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid payment order ID: %v", err)
	}

	// Retrieve payment order
	po, err := s.repo.FindByID(ctx, poID)
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

	if err := s.repo.Update(ctx, po); err != nil {
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

	s.logger.Info("payment order cancelled",
		"payment_order_id", po.ID.String(),
		"reason", req.CancellationReason,
		"cancelled_by", req.CancelledBy,
		"amount_cents", po.Amount.AmountCents(),
		"currency", po.Amount.Currency(),
		"idempotency_key", po.IdempotencyKey,
		"correlation_id", po.CorrelationID)

	return &pb.CancelPaymentOrderResponse{
		PaymentOrder: toProto(po),
	}, nil
}

// ListPaymentOrders returns a paginated list of payment orders.
// Uses cursor-based pagination for consistent results even when items are inserted/deleted.
// The cursor is an opaque token encoding (created_at, id) for deterministic ordering.
func (s *Service) ListPaymentOrders(ctx context.Context, req *pb.ListPaymentOrdersRequest) (*pb.ListPaymentOrdersResponse, error) {
	if req.DebtorAccountId == "" {
		return nil, status.Error(codes.InvalidArgument, "debtor_account_id is required for listing")
	}

	// Parse and validate pagination parameters
	pageSize := s.defaultPageSize
	if req.Pagination != nil && req.Pagination.PageSize > 0 {
		pageSize = int(req.Pagination.PageSize)
		if pageSize > s.maxPageSize {
			pageSize = s.maxPageSize
		}
	}

	// Decode cursor from page token (empty token = first page)
	var cursor persistence.Cursor
	if req.Pagination != nil && req.Pagination.PageToken != "" {
		var err error
		cursor, err = persistence.DecodeCursor(req.Pagination.PageToken)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid page_token")
		}
	}

	// Query with cursor-based pagination
	result, err := s.repo.FindByDebtorAccountIDWithCursor(ctx, req.DebtorAccountId, pageSize, cursor)
	if err != nil {
		s.logger.Error("failed to list payment orders", "error", err)
		return nil, status.Error(codes.Internal, "failed to list payment orders")
	}

	// Convert to proto
	protoOrders := make([]*pb.PaymentOrder, 0, len(result.PaymentOrders))
	for _, po := range result.PaymentOrders {
		protoOrders = append(protoOrders, toProto(po))
	}

	return &pb.ListPaymentOrdersResponse{
		PaymentOrders: protoOrders,
		Pagination: &commonpb.PaginationResponse{
			NextPageToken: result.NextCursor,
			TotalCount:    result.TotalCount,
		},
	}, nil
}

// ReversePaymentOrder reverses a completed payment order (post-completion compensation).
// This creates compensating ledger entries and transitions the order to REVERSED.
// Idempotent: returns success if already reversed.
func (s *Service) ReversePaymentOrder(ctx context.Context, req *pb.ReversePaymentOrderRequest) (*pb.ReversePaymentOrderResponse, error) {
	// Validate reversal reason - required for audit purposes
	if req.ReversalReason == "" {
		return nil, status.Error(codes.InvalidArgument, "reversal_reason is required")
	}

	// Validate reversed_by - required for audit purposes
	if req.ReversedBy == "" {
		return nil, status.Error(codes.InvalidArgument, "reversed_by is required")
	}

	// Parse payment order ID
	poID, err := uuid.Parse(req.PaymentOrderId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid payment order ID: %v", err)
	}

	// Retrieve payment order
	po, err := s.repo.FindByID(ctx, poID)
	if err != nil {
		if errors.Is(err, persistence.ErrPaymentOrderNotFound) {
			return nil, status.Errorf(codes.NotFound, "payment order not found: %s", req.PaymentOrderId)
		}
		return nil, status.Error(codes.Internal, "failed to retrieve payment order")
	}

	// Check if already reversed (idempotent)
	if po.Status == domain.PaymentOrderStatusReversed {
		return &pb.ReversePaymentOrderResponse{PaymentOrder: toProto(po)}, nil
	}

	// Check if can be reversed
	if !po.CanReverse() {
		return nil, status.Errorf(codes.FailedPrecondition,
			"payment order cannot be reversed in status %s (only COMPLETED orders can be reversed)", po.Status)
	}

	// Store original ledger booking ID for the event
	originalLedgerBookingID := po.LedgerBookingID

	// Reverse the payment order
	if err := po.Reverse(req.ReversalReason); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to reverse payment order: %v", err)
	}

	// Update in database
	if err := s.repo.Update(ctx, po); err != nil {
		return nil, status.Error(codes.Internal, "failed to update payment order")
	}

	// Publish PaymentOrderReversed event
	// Note: In a full implementation, this would trigger compensating ledger entries
	// via FinancialAccounting service. For now, we publish the event for downstream
	// consumers to handle the compensation.
	s.publishEvent(ctx, TopicPaymentOrderReversed, po.ID.String(), &eventsv1.PaymentOrderReversedEvent{
		EventId:                     uuid.New().String(),
		PaymentOrderId:              po.ID.String(),
		DebtorAccountId:             po.DebtorAccountID,
		Amount:                      toMoneyAmount(po.Amount),
		ReversalReason:              req.ReversalReason,
		ReversedBy:                  req.ReversedBy,
		OriginalLedgerBookingId:     originalLedgerBookingID,
		CompensatingLedgerBookingId: "", // Will be populated when FA service creates compensating entry
		CorrelationId:               po.CorrelationID,
		CausationId:                 po.ID.String(),
		Timestamp:                   timestamppb.Now(),
		Version:                     int64(po.Version),
		IdempotencyKey:              po.IdempotencyKey,
	})

	s.logger.Info("payment order reversed",
		"payment_order_id", po.ID.String(),
		"reason", req.ReversalReason,
		"reversed_by", req.ReversedBy,
		"amount_cents", po.Amount.AmountCents(),
		"currency", po.Amount.Currency(),
		"original_ledger_booking_id", originalLedgerBookingID,
		"correlation_id", po.CorrelationID)

	return &pb.ReversePaymentOrderResponse{PaymentOrder: toProto(po)}, nil
}

// Helper functions

// publishEvent publishes a Kafka event if the publisher is configured.
// This is best-effort/fire-and-forget: errors are logged but not retried or persisted.
// Failed events are NOT automatically recovered. For guaranteed delivery, consider
// implementing a transactional outbox pattern in the future.
func (s *Service) publishEvent(ctx context.Context, topic string, key string, event proto.Message) {
	if s.kafkaPublisher == nil {
		return
	}
	if err := s.kafkaPublisher.Publish(ctx, topic, key, event); err != nil {
		s.logger.Error("failed to publish event",
			"topic", topic,
			"key", key,
			"error", err)
	} else {
		s.logger.Info("published event",
			"topic", topic,
			"key", key)
	}
}

// toProto converts a domain PaymentOrder to proto PaymentOrder
func toProto(po *domain.PaymentOrder) *pb.PaymentOrder {
	proto := &pb.PaymentOrder{
		PaymentOrderId:        po.ID.String(),
		DebtorAccountId:       po.DebtorAccountID,
		CreditorReference:     po.CreditorReference,
		Amount:                toMoneyAmount(po.Amount),
		Status:                mapStatusToProto(po.Status),
		LienId:                po.LienID,
		GatewayReferenceId:    po.GatewayReferenceID,
		CorrelationId:         po.CorrelationID,
		IdempotencyKey:        po.IdempotencyKey,
		FailureReason:         po.FailureReason,
		CreatedAt:             timestamppb.New(po.CreatedAt),
		UpdatedAt:             timestamppb.New(po.UpdatedAt),
		Version:               int64(po.Version),
		LedgerBookingId:       po.LedgerBookingID,
		CausationId:           po.CausationID,
		ErrorCode:             po.ErrorCode,
		LienExecutionStatus:   mapLienExecutionStatusToProto(po.LienExecutionStatus),
		LienExecutionAttempts: safeIntToInt32(po.LienExecutionAttempts),
		LienExecutionError:    po.LienExecutionError,
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

// toMoneyAmount converts domain Money (in cents) to proto MoneyAmount (units + nanos).
// The conversion splits cents into units (dollars) and nanos:
//   - units = cents / 100 (integer division)
//   - nanos = (cents % 100) * 10^7 (remainder converted to nanos)
//
// This is a lossless conversion since cents are exact representations.
// The inverse operation protoToMoney uses symmetric rounding for any sub-cent precision.
func toMoneyAmount(m cadomain.Money) *commonpb.MoneyAmount {
	amountCents := m.AmountCents()
	units := amountCents / 100
	remainder := amountCents % 100
	// #nosec G115 - remainder is always -99 to 99
	nanos := int32(remainder * nanosPerCent)

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

	// Validate nanos is within bounds (per Google Money spec)
	const maxNanos = 999999999
	if amount.Amount.Nanos < -maxNanos || amount.Amount.Nanos > maxNanos {
		return cadomain.Money{}, ErrInvalidNanos
	}

	// Convert to cents with symmetric rounding (round half away from zero)
	unitsCents := amount.Amount.Units * 100
	var nanosCents int64
	if amount.Amount.Nanos >= 0 {
		nanosCents = int64((amount.Amount.Nanos + nanosRoundingOffset) / nanosPerCent)
	} else {
		nanosCents = int64((amount.Amount.Nanos - nanosRoundingOffset) / nanosPerCent)
	}
	totalCents := unitsCents + nanosCents

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

// mapLienExecutionStatusToProto maps domain LienExecutionStatus to proto
func mapLienExecutionStatusToProto(status domain.LienExecutionStatus) pb.LienExecutionStatus {
	switch status {
	case domain.LienExecutionStatusUnspecified:
		return pb.LienExecutionStatus_LIEN_EXECUTION_STATUS_UNSPECIFIED
	case domain.LienExecutionStatusPending:
		return pb.LienExecutionStatus_LIEN_EXECUTION_STATUS_PENDING
	case domain.LienExecutionStatusSucceeded:
		return pb.LienExecutionStatus_LIEN_EXECUTION_STATUS_SUCCEEDED
	case domain.LienExecutionStatusFailed:
		return pb.LienExecutionStatus_LIEN_EXECUTION_STATUS_FAILED
	default:
		return pb.LienExecutionStatus_LIEN_EXECUTION_STATUS_UNSPECIFIED
	}
}

// safeIntToInt32 safely converts an int to int32 with bounds checking.
// Returns math.MaxInt32 if the value exceeds int32 range.
// This is used for lien execution attempts which should never exceed
// a small number in practice, but we need safe conversion for gosec.
func safeIntToInt32(n int) int32 {
	const maxInt32 = 1<<31 - 1
	if n > maxInt32 {
		return maxInt32
	}
	if n < -maxInt32-1 {
		return -maxInt32 - 1
	}
	return int32(n)
}

// executeLienWithRetry executes a lien asynchronously with exponential backoff retry.
// This is called in a goroutine after a payment order is marked COMPLETED.
// The lien execution status is tracked in the payment order for reconciliation.
//
// The method:
// 1. Creates a context with timeout for the entire retry sequence
// 2. Uses exponential backoff for retries with the existing clients.Retry infrastructure
// 3. Updates the payment order's lien execution status on success or final failure
// 4. Logs all attempts for monitoring and alerting
//
// nolint:contextcheck // Context is intentionally created fresh for async operation
func (s *Service) executeLienWithRetry(parentCtx context.Context, paymentOrderID uuid.UUID, lienID string) {
	// Defensive check: guard against nil currentAccountClient even though callers currently check
	if s.currentAccountClient == nil {
		s.logger.Error("executeLienWithRetry called with nil currentAccountClient",
			"payment_order_id", paymentOrderID.String(),
			"lien_id", lienID)
		return
	}

	// Recover from panics to prevent silent goroutine crashes
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("panic in executeLienWithRetry",
				"panic", r,
				"payment_order_id", paymentOrderID.String(),
				"lien_id", lienID)
			// Attempt to mark as FAILED to prevent stuck PENDING state
			// Use a fresh context since the original may be cancelled
			panicCtx, panicCancel := context.WithTimeout(context.Background(), 10*time.Second) //nolint:contextcheck
			defer panicCancel()
			po, findErr := s.repo.FindByID(panicCtx, paymentOrderID) //nolint:contextcheck
			if findErr != nil {
				s.logger.Error("failed to fetch payment order after panic",
					"payment_order_id", paymentOrderID.String(),
					"error", findErr)
				return
			}
			po.SetLienExecutionFailed(fmt.Sprintf("panic: %v", r))
			if updateErr := s.repo.Update(panicCtx, po); updateErr != nil { //nolint:contextcheck
				s.logger.Error("failed to update payment order status after panic",
					"payment_order_id", paymentOrderID.String(),
					"error", updateErr)
			}
		}
	}()

	// Create a context with timeout for the entire retry sequence
	ctx, cancel := context.WithTimeout(parentCtx, DefaultLienExecutionRetryTimeout)
	defer cancel()

	logger := s.logger.With(
		"payment_order_id", paymentOrderID.String(),
		"lien_id", lienID,
		"operation", "execute_lien_async",
	)

	logger.Info("starting async lien execution with retry")

	// Use configured retry config or default
	retryConfig := s.lienExecutionRetryConfig
	if retryConfig == nil {
		retryConfig = &clients.RetryConfig{
			MaxRetries:          DefaultLienExecutionMaxRetries,
			InitialInterval:     500 * time.Millisecond,
			MaxInterval:         30 * time.Second,
			Multiplier:          2.0,
			RandomizationFactor: 0.5,
		}
	}

	var lastErr error
	var attempts int

	// Execute with retry
	err := clients.Retry(ctx, *retryConfig, func() error {
		attempts++
		logger.Info("attempting lien execution", "attempt", attempts)

		_, execErr := s.currentAccountClient.ExecuteLien(ctx, &currentaccountv1.ExecuteLienRequest{
			LienId: lienID,
		})

		if execErr != nil {
			logger.Warn("lien execution attempt failed",
				"attempt", attempts,
				"error", execErr)
			lastErr = execErr
			return execErr
		}

		logger.Info("lien execution succeeded", "attempt", attempts)
		return nil
	})

	// Update payment order with final status
	s.updateLienExecutionStatus(paymentOrderID, attempts, err, lastErr, logger)
}

// updateLienExecutionStatus updates the payment order's lien execution status after retry completion.
// This is called after all retry attempts have finished (success or failure).
// Uses optimistic locking with retry on version conflict to handle concurrent updates.
// Note: Uses a fresh context to ensure the status update completes even if the parent context has timed out.
func (s *Service) updateLienExecutionStatus(
	paymentOrderID uuid.UUID,
	totalLienAttempts int,
	retryErr error,
	lastErr error,
	logger *slog.Logger,
) {
	// Use a fresh context to ensure status update isn't cancelled by parent timeout.
	// This is intentional - the parent context may have timed out during retries,
	// but we must still persist the final status for reconciliation purposes.
	//nolint:contextcheck // Intentionally using fresh context to ensure status persistence
	updateCtx, cancel := context.WithTimeout(context.Background(), lienStatusUpdateTimeout)
	defer cancel()

	for updateAttempt := 1; updateAttempt <= lienStatusUpdateMaxRetries; updateAttempt++ {
		// Apply exponential backoff for retries to reduce contention
		if updateAttempt > 1 {
			backoff := time.Duration(updateAttempt-1) * lienStatusUpdateBackoffBase
			select {
			case <-updateCtx.Done():
				logger.Error("context cancelled during update retry backoff",
					"update_attempt", updateAttempt)
				return
			case <-time.After(backoff):
			}
		}

		// Fetch the current payment order (fresh version)
		po, err := s.repo.FindByID(updateCtx, paymentOrderID) //nolint:contextcheck
		if err != nil {
			logger.Error("failed to fetch payment order for lien execution status update",
				"error", err,
				"update_attempt", updateAttempt)
			return
		}

		// Update lien execution tracking fields
		po.LienExecutionAttempts = totalLienAttempts

		// Determine error message if failed
		var errMsg string
		if retryErr != nil {
			switch {
			case lastErr != nil:
				errMsg = lastErr.Error()
			case retryErr != nil:
				errMsg = retryErr.Error()
			default:
				errMsg = "unknown error"
			}
		}

		// Set status on domain object
		if retryErr == nil {
			po.SetLienExecutionSucceeded()
		} else {
			po.SetLienExecutionFailed(errMsg)
		}

		// Persist the updated status
		updateErr := s.repo.Update(updateCtx, po) //nolint:contextcheck
		if updateErr == nil {
			// Record metrics only after successful persistence to avoid double-counting
			// on version conflict retries
			if retryErr == nil {
				logger.Info("lien execution completed successfully",
					"total_attempts", totalLienAttempts)
				poobservability.RecordLienExecution("success")
			} else {
				logger.Error("lien execution failed after all retries",
					"total_attempts", totalLienAttempts,
					"error", errMsg)
				poobservability.RecordLienExecution("failure")
				poobservability.RecordExternalServiceError("current_account", "execute_lien")
			}
			logger.Info("payment order lien execution status updated",
				"status", po.LienExecutionStatus,
				"attempts", po.LienExecutionAttempts)
			return
		}

		// Check if this is a version conflict (optimistic locking failure)
		if errors.Is(updateErr, persistence.ErrPaymentOrderVersionConflict) {
			logger.Warn("version conflict updating lien execution status, retrying",
				"update_attempt", updateAttempt,
				"max_attempts", lienStatusUpdateMaxRetries)
			continue
		}

		// Non-recoverable error
		logger.Error("failed to update payment order lien execution status",
			"error", updateErr,
			"update_attempt", updateAttempt)
		return
	}

	// Log and record metric for exhausted retries - this will leave the payment order
	// in PENDING state which will be caught by the reconciliation query using the
	// idx_payment_orders_lien_execution partial index
	logger.Error("failed to update lien execution status after max retries due to version conflicts",
		"max_attempts", lienStatusUpdateMaxRetries,
		"payment_order_id", paymentOrderID.String())
	poobservability.RecordLienExecutionStatusUpdateExhausted()
}
