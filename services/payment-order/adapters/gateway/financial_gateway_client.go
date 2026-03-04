package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	financialgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_gateway/v1"
	fgclient "github.com/meridianhub/meridian/services/financial-gateway/client"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Sentinel errors for the FinancialGatewayClient.
var (
	// ErrFinancialGatewayInvalidArgument is returned when the gateway rejects the request as invalid.
	ErrFinancialGatewayInvalidArgument = errors.New("financial-gateway rejected payment (invalid argument)")
	// ErrFinancialGatewayPreconditionFailed is returned when tenant configuration is missing.
	ErrFinancialGatewayPreconditionFailed = errors.New("financial-gateway precondition failed")
	// ErrFinancialGatewayDuplicateDispatch is returned when the idempotency key already exists.
	ErrFinancialGatewayDuplicateDispatch = errors.New("financial-gateway duplicate dispatch (already exists)")
	// ErrFinancialGatewayUnavailable is returned on transient service failures.
	ErrFinancialGatewayUnavailable = errors.New("financial-gateway temporarily unavailable")
	// ErrFinancialGatewayTimeout is returned when the request times out or is canceled.
	ErrFinancialGatewayTimeout = errors.New("financial-gateway request timed out or canceled")
	// ErrFinancialGatewayRailUnsupported is returned when the payment rail is not supported.
	ErrFinancialGatewayRailUnsupported = errors.New("financial-gateway payment rail not supported")
	// ErrFinancialGatewayUnexpected is returned for unexpected gRPC error codes.
	ErrFinancialGatewayUnexpected = errors.New("financial-gateway unexpected error")
)

// FinancialGatewayClient implements PaymentGateway by calling the financial-gateway service via gRPC.
type FinancialGatewayClient struct {
	client *fgclient.Client
	logger *slog.Logger
}

// NewFinancialGatewayClient creates a FinancialGatewayClient wrapping the provided gRPC client.
func NewFinancialGatewayClient(client *fgclient.Client, logger *slog.Logger) *FinancialGatewayClient {
	if logger == nil {
		logger = slog.Default()
	}
	return &FinancialGatewayClient{
		client: client,
		logger: logger,
	}
}

// SendPayment dispatches a payment via the financial-gateway service using the Stripe rail.
func (c *FinancialGatewayClient) SendPayment(ctx context.Context, req PaymentRequest) (PaymentResponse, error) {
	amountUnits := domain.ToMinorUnits(req.Amount)
	instrumentCode := domain.CurrencyCode(req.Amount)

	protoReq := &financialgatewayv1.DispatchPaymentRequest{
		PaymentOrderId:    req.PaymentOrderID.String(),
		Rail:              financialgatewayv1.PaymentRail_PAYMENT_RAIL_STRIPE,
		AmountUnits:       amountUnits,
		InstrumentCode:    instrumentCode,
		DebtorAccountId:   req.DebtorAccountID,
		CreditorAccountId: req.PaymentOrderID.String(), // placeholder UUID; Stripe routing via Reference
		Reference:         req.CreditorReference,
		CorrelationId:     req.PaymentOrderID.String(),
		CausationId:       req.PaymentOrderID.String(),
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: req.IdempotencyKey,
		},
	}

	c.logger.Debug("dispatching payment via financial-gateway",
		"payment_order_id", req.PaymentOrderID.String(),
		"amount_units", amountUnits,
		"instrument_code", instrumentCode,
	)

	resp, err := c.client.DispatchPayment(ctx, protoReq)
	if err != nil {
		return PaymentResponse{}, c.mapGRPCError(err)
	}

	gatewayStatus := mapDispatchStatus(resp.GetStatus())

	c.logger.Info("financial-gateway dispatch response",
		"payment_order_id", req.PaymentOrderID.String(),
		"dispatch_id", resp.GetDispatchId(),
		"provider_reference", resp.GetProviderReference(),
		"status", resp.GetStatus(),
	)

	return PaymentResponse{
		GatewayReferenceID: resp.GetProviderReference(),
		Status:             gatewayStatus,
		Message:            resp.GetStatus().String(),
	}, nil
}

// mapDispatchStatus maps a financial-gateway DispatchStatus to a gateway.Status.
func mapDispatchStatus(s financialgatewayv1.DispatchStatus) Status {
	switch s {
	case financialgatewayv1.DispatchStatus_DISPATCH_STATUS_DELIVERED,
		financialgatewayv1.DispatchStatus_DISPATCH_STATUS_ACKNOWLEDGED:
		return StatusAccepted
	case financialgatewayv1.DispatchStatus_DISPATCH_STATUS_FAILED:
		return StatusRejected
	case financialgatewayv1.DispatchStatus_DISPATCH_STATUS_PENDING,
		financialgatewayv1.DispatchStatus_DISPATCH_STATUS_DISPATCHING,
		financialgatewayv1.DispatchStatus_DISPATCH_STATUS_RETRYING:
		return StatusPending
	case financialgatewayv1.DispatchStatus_DISPATCH_STATUS_UNSPECIFIED:
		return StatusPending
	}
	// Unknown status — treat as pending
	return StatusPending
}

// mapGRPCError translates gRPC status codes into payment-order gateway errors.
func (c *FinancialGatewayClient) mapGRPCError(err error) error {
	st, ok := status.FromError(err)
	if !ok {
		return fmt.Errorf("financial-gateway dispatch failed: %w", err)
	}

	switch st.Code() { //nolint:exhaustive // remaining codes treated as unexpected
	case codes.InvalidArgument:
		// Permanent rejection — caller should treat as terminal failure.
		return fmt.Errorf("%w: %s", ErrFinancialGatewayInvalidArgument, st.Message())

	case codes.FailedPrecondition:
		// Configuration error (missing tenant config, Stripe not set up).
		return fmt.Errorf("%w: %s", ErrFinancialGatewayPreconditionFailed, st.Message())

	case codes.AlreadyExists:
		// Idempotency collision — same key was already dispatched.
		return fmt.Errorf("%w: %s", ErrFinancialGatewayDuplicateDispatch, st.Message())

	case codes.Unavailable:
		// Transient failure — safe to retry.
		return fmt.Errorf("%w: %s", ErrFinancialGatewayUnavailable, st.Message())

	case codes.DeadlineExceeded, codes.Canceled:
		// Propagate context errors for retry by the resilience layer.
		return fmt.Errorf("%w: %w", ErrFinancialGatewayTimeout, err)

	case codes.Unimplemented:
		// Payment rail not supported — permanent error.
		return fmt.Errorf("%w: %s", ErrFinancialGatewayRailUnsupported, st.Message())

	default:
		return fmt.Errorf("%w: code=%s msg=%s", ErrFinancialGatewayUnexpected, st.Code(), st.Message())
	}
}

// Compile-time interface check.
var _ PaymentGateway = (*FinancialGatewayClient)(nil)
