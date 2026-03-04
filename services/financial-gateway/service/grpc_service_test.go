package service

import (
	"context"
	"log/slog"
	"testing"

	"github.com/sony/gobreaker/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	financialgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_gateway/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestDispatchPayment_UnsupportedRail(t *testing.T) {
	svc, err := NewFinancialGatewayService(Config{Logger: slog.Default()})
	require.NoError(t, err)

	req := &financialgatewayv1.DispatchPaymentRequest{
		PaymentOrderId:    "11111111-1111-1111-1111-111111111111",
		Rail:              financialgatewayv1.PaymentRail_PAYMENT_RAIL_SWIFT,
		AmountUnits:       1000,
		InstrumentCode:    "USD",
		DebtorAccountId:   "cus_test",
		CreditorAccountId: "acct_cred",
		Reference:         "ref",
		IdempotencyKey:    &commonv1.IdempotencyKey{Key: "test-key"},
	}

	_, err = svc.DispatchPayment(context.Background(), req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
}

func TestDispatchPayment_StripeNotConfigured(t *testing.T) {
	svc, err := NewFinancialGatewayService(Config{Logger: slog.Default()})
	require.NoError(t, err)

	req := &financialgatewayv1.DispatchPaymentRequest{
		PaymentOrderId:    "11111111-1111-1111-1111-111111111111",
		Rail:              financialgatewayv1.PaymentRail_PAYMENT_RAIL_STRIPE,
		AmountUnits:       1000,
		InstrumentCode:    "USD",
		DebtorAccountId:   "cus_test",
		CreditorAccountId: "acct_cred",
		Reference:         "ref",
		IdempotencyKey:    &commonv1.IdempotencyKey{Key: "test-key"},
	}

	_, err = svc.DispatchPayment(context.Background(), req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unavailable, st.Code())
}

func TestDispatchRefund_Unimplemented(t *testing.T) {
	svc, err := NewFinancialGatewayService(Config{Logger: slog.Default()})
	require.NoError(t, err)

	_, err = svc.DispatchRefund(context.Background(), &financialgatewayv1.DispatchRefundRequest{})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
}

func TestGetProviderHealth_StripeNotConfigured(t *testing.T) {
	svc, err := NewFinancialGatewayService(Config{Logger: slog.Default()})
	require.NoError(t, err)

	resp, err := svc.GetProviderHealth(context.Background(), &financialgatewayv1.GetProviderHealthRequest{
		Rail: financialgatewayv1.PaymentRail_PAYMENT_RAIL_STRIPE,
	})
	require.NoError(t, err)
	assert.Equal(t, financialgatewayv1.ProviderHealth_PROVIDER_HEALTH_UNSPECIFIED, resp.Health)
	assert.Contains(t, resp.Message, "not configured")
}

func TestGetProviderHealth_UnsupportedRail(t *testing.T) {
	svc, err := NewFinancialGatewayService(Config{Logger: slog.Default()})
	require.NoError(t, err)

	_, err = svc.GetProviderHealth(context.Background(), &financialgatewayv1.GetProviderHealthRequest{
		Rail: financialgatewayv1.PaymentRail_PAYMENT_RAIL_ACH,
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
}

func TestMapCircuitBreakerHealth(t *testing.T) {
	tests := []struct {
		name           string
		state          gobreaker.State
		expectedHealth financialgatewayv1.ProviderHealth
	}{
		{"closed maps to healthy", gobreaker.StateClosed, financialgatewayv1.ProviderHealth_PROVIDER_HEALTH_HEALTHY},
		{"half-open maps to degraded", gobreaker.StateHalfOpen, financialgatewayv1.ProviderHealth_PROVIDER_HEALTH_DEGRADED},
		{"open maps to unhealthy", gobreaker.StateOpen, financialgatewayv1.ProviderHealth_PROVIDER_HEALTH_UNHEALTHY},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expectedHealth, mapCircuitBreakerHealth(tt.state))
		})
	}
}
