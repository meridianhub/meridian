// Package service implements the gRPC service for the financial-gateway domain.
package service

import (
	"context"
	"errors"
	"log/slog"
	"os"

	financialgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_gateway/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrLoggerNil is returned when a nil logger is provided to a constructor.
var ErrLoggerNil = errors.New("logger cannot be nil")

// FinancialGatewayService implements FinancialGatewayServiceServer.
// All RPCs return codes.Unimplemented until the Stripe adapter is wired in task 5.
type FinancialGatewayService struct {
	financialgatewayv1.UnimplementedFinancialGatewayServiceServer
	logger *slog.Logger
}

// NewFinancialGatewayService creates a new FinancialGatewayService stub.
// If logger is nil a default JSON logger writing to stdout is used.
func NewFinancialGatewayService(logger *slog.Logger) (*FinancialGatewayService, error) {
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}
	return &FinancialGatewayService{
		logger: logger,
	}, nil
}

// DispatchPayment submits a financial payment for outbound dispatch via a payment rail.
// Returns Unimplemented until the Stripe adapter is wired (task 5).
func (s *FinancialGatewayService) DispatchPayment(
	_ context.Context,
	_ *financialgatewayv1.DispatchPaymentRequest,
) (*financialgatewayv1.DispatchPaymentResponse, error) {
	return nil, status.Error(codes.Unimplemented, "DispatchPayment not yet implemented")
}

// DispatchRefund submits a financial refund for outbound dispatch via the original payment rail.
// Returns Unimplemented until the Stripe adapter is wired (task 5).
func (s *FinancialGatewayService) DispatchRefund(
	_ context.Context,
	_ *financialgatewayv1.DispatchRefundRequest,
) (*financialgatewayv1.DispatchRefundResponse, error) {
	return nil, status.Error(codes.Unimplemented, "DispatchRefund not yet implemented")
}

// GetProviderHealth returns the current health status of a payment rail provider.
// Returns Unimplemented until the Stripe adapter is wired (task 5).
func (s *FinancialGatewayService) GetProviderHealth(
	_ context.Context,
	_ *financialgatewayv1.GetProviderHealthRequest,
) (*financialgatewayv1.GetProviderHealthResponse, error) {
	return nil, status.Error(codes.Unimplemented, "GetProviderHealth not yet implemented")
}
