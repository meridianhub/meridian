// Package service implements the gRPC service for the financial-gateway domain.
package service

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/sony/gobreaker/v2"

	financialgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_gateway/v1"
	stripeadapter "github.com/meridianhub/meridian/services/financial-gateway/adapters/stripe"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// FinancialGatewayService implements FinancialGatewayServiceServer.
type FinancialGatewayService struct {
	financialgatewayv1.UnimplementedFinancialGatewayServiceServer
	stripeAdapter *stripeadapter.PaymentIntentAdapter
	clientFactory *stripeadapter.ClientFactory
	logger        *slog.Logger
}

// Config holds configuration for the FinancialGatewayService.
type Config struct {
	// StripeAdapter is the Stripe payment intent adapter for dispatching payments.
	// If nil, Stripe-related RPCs return Unavailable.
	StripeAdapter *stripeadapter.PaymentIntentAdapter

	// ClientFactory creates tenant-scoped Stripe clients.
	// Required when StripeAdapter is set.
	ClientFactory *stripeadapter.ClientFactory

	// Logger is the structured logger. If nil, a default JSON logger is used.
	Logger *slog.Logger
}

// NewFinancialGatewayService creates a new FinancialGatewayService.
// If logger is nil a default JSON logger writing to stdout is used.
func NewFinancialGatewayService(cfg Config) (*FinancialGatewayService, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}
	return &FinancialGatewayService{
		stripeAdapter: cfg.StripeAdapter,
		clientFactory: cfg.ClientFactory,
		logger:        logger,
	}, nil
}

// DispatchPayment submits a financial payment for outbound dispatch via a payment rail.
func (s *FinancialGatewayService) DispatchPayment(
	ctx context.Context,
	req *financialgatewayv1.DispatchPaymentRequest,
) (*financialgatewayv1.DispatchPaymentResponse, error) {
	switch req.GetRail() { //nolint:exhaustive // only Stripe is supported; all others fall through to default
	case financialgatewayv1.PaymentRail_PAYMENT_RAIL_STRIPE:
		return s.dispatchStripePayment(ctx, req)
	default:
		return nil, status.Errorf(codes.Unimplemented, "payment rail %s is not yet supported", req.GetRail())
	}
}

// dispatchStripePayment handles Stripe-specific payment dispatch.
func (s *FinancialGatewayService) dispatchStripePayment(
	ctx context.Context,
	req *financialgatewayv1.DispatchPaymentRequest,
) (*financialgatewayv1.DispatchPaymentResponse, error) {
	if s.stripeAdapter == nil || s.clientFactory == nil {
		return nil, status.Error(codes.Unavailable, "stripe adapter is not configured")
	}

	// Resolve tenant-scoped Stripe client to get the Connected Account
	client, err := s.clientFactory.NewClient(ctx)
	if err != nil {
		s.logger.Error("failed to create stripe client",
			"payment_order_id", req.GetPaymentOrderId(),
			"error", err,
		)
		return nil, mapClientFactoryError(err)
	}

	// Inject the Stripe Connected Account ID into context
	ctx = stripeadapter.WithStripeAccount(ctx, client.AccountID)

	result, err := s.stripeAdapter.DispatchPayment(ctx, req)
	if err != nil {
		s.logger.Error("stripe dispatch failed",
			"payment_order_id", req.GetPaymentOrderId(),
			"error", err,
		)
		return nil, mapStripeError(err)
	}

	dispatchID := uuid.New().String()

	return &financialgatewayv1.DispatchPaymentResponse{
		DispatchId:        dispatchID,
		PaymentOrderId:    req.GetPaymentOrderId(),
		Rail:              financialgatewayv1.PaymentRail_PAYMENT_RAIL_STRIPE,
		Status:            result.Status,
		ProviderReference: result.ProviderReference,
		CreatedAt:         timestamppb.Now(),
	}, nil
}

// DispatchRefund submits a financial refund for outbound dispatch via the original payment rail.
// Returns Unimplemented until refund support is added.
func (s *FinancialGatewayService) DispatchRefund(
	_ context.Context,
	_ *financialgatewayv1.DispatchRefundRequest,
) (*financialgatewayv1.DispatchRefundResponse, error) {
	return nil, status.Error(codes.Unimplemented, "DispatchRefund not yet implemented")
}

// CancelPayment cancels a pending payment dispatch before it is delivered to the payment rail.
// Returns Unimplemented until cancellation support is added.
func (s *FinancialGatewayService) CancelPayment(
	_ context.Context,
	_ *financialgatewayv1.CancelPaymentRequest,
) (*financialgatewayv1.CancelPaymentResponse, error) {
	return nil, status.Error(codes.Unimplemented, "CancelPayment not yet implemented")
}

// GetProviderHealth returns the current health status of a payment rail provider.
// When tenant context is present, returns the per-tenant circuit breaker state.
// Without tenant context, returns UNSPECIFIED health (per-tenant breakers require tenant).
func (s *FinancialGatewayService) GetProviderHealth(
	ctx context.Context,
	req *financialgatewayv1.GetProviderHealthRequest,
) (*financialgatewayv1.GetProviderHealthResponse, error) {
	switch req.GetRail() { //nolint:exhaustive // only Stripe is supported; all others fall through to default
	case financialgatewayv1.PaymentRail_PAYMENT_RAIL_STRIPE:
		return s.getStripeHealth(ctx)
	default:
		return nil, status.Errorf(codes.Unimplemented, "health check for rail %s is not yet supported", req.GetRail())
	}
}

// getStripeHealth reports Stripe provider health based on the per-tenant circuit breaker state.
func (s *FinancialGatewayService) getStripeHealth(ctx context.Context) (*financialgatewayv1.GetProviderHealthResponse, error) {
	if s.stripeAdapter == nil || s.clientFactory == nil {
		return &financialgatewayv1.GetProviderHealthResponse{
			Rail:          financialgatewayv1.PaymentRail_PAYMENT_RAIL_STRIPE,
			Health:        financialgatewayv1.ProviderHealth_PROVIDER_HEALTH_UNSPECIFIED,
			Message:       "stripe adapter not configured",
			LastCheckedAt: timestamppb.Now(),
		}, nil
	}

	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return &financialgatewayv1.GetProviderHealthResponse{
			Rail:          financialgatewayv1.PaymentRail_PAYMENT_RAIL_STRIPE,
			Health:        financialgatewayv1.ProviderHealth_PROVIDER_HEALTH_UNSPECIFIED,
			Message:       "tenant context required for per-tenant health check",
			LastCheckedAt: timestamppb.Now(),
		}, nil
	}

	cbState := s.clientFactory.CircuitBreakerState(tenantID)
	health := mapCircuitBreakerHealth(cbState)

	return &financialgatewayv1.GetProviderHealthResponse{
		Rail:          financialgatewayv1.PaymentRail_PAYMENT_RAIL_STRIPE,
		Health:        health,
		Message:       "circuit breaker state: " + cbState.String(),
		LastCheckedAt: timestamppb.New(time.Now()),
	}, nil
}

// mapClientFactoryError maps client factory errors to appropriate gRPC status codes
// without leaking internal error details.
func mapClientFactoryError(err error) error {
	switch {
	case errors.Is(err, stripeadapter.ErrMissingTenant):
		return status.Error(codes.FailedPrecondition, "tenant context required")
	case errors.Is(err, stripeadapter.ErrTenantConfigNotFound):
		return status.Error(codes.FailedPrecondition, "stripe not configured for tenant")
	case errors.Is(err, stripeadapter.ErrCircuitOpen):
		return status.Error(codes.Unavailable, "stripe service temporarily unavailable")
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, "request canceled")
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, "request deadline exceeded")
	default:
		return status.Error(codes.Internal, "failed to resolve stripe configuration")
	}
}

// mapStripeError maps adapter errors to appropriate gRPC status codes.
func mapStripeError(err error) error {
	switch {
	case errors.Is(err, stripeadapter.ErrMissingStripeAccount):
		return status.Error(codes.FailedPrecondition, "stripe connected account not configured")
	case errors.Is(err, stripeadapter.ErrInvalidRequest):
		return status.Error(codes.InvalidArgument, "invalid payment parameters")
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, "request canceled")
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, "request deadline exceeded")
	default:
		return status.Error(codes.Unavailable, "stripe dispatch failed")
	}
}

// mapCircuitBreakerHealth maps a gobreaker circuit breaker state to a proto ProviderHealth.
func mapCircuitBreakerHealth(state gobreaker.State) financialgatewayv1.ProviderHealth {
	switch state {
	case gobreaker.StateClosed:
		return financialgatewayv1.ProviderHealth_PROVIDER_HEALTH_HEALTHY
	case gobreaker.StateHalfOpen:
		return financialgatewayv1.ProviderHealth_PROVIDER_HEALTH_DEGRADED
	case gobreaker.StateOpen:
		return financialgatewayv1.ProviderHealth_PROVIDER_HEALTH_UNHEALTHY
	default:
		return financialgatewayv1.ProviderHealth_PROVIDER_HEALTH_UNSPECIFIED
	}
}
