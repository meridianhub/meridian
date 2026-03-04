package main

import (
	"log/slog"
	"os"
	"testing"

	"github.com/meridianhub/meridian/services/payment-order/adapters/gateway"
	"github.com/meridianhub/meridian/services/payment-order/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreatePaymentGateway_DefaultMock(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := config.ServiceConfig{
		PaymentGatewayProvider: gateway.ProviderMock,
	}

	gw, cleanup, err := createPaymentGateway(cfg, logger)
	t.Cleanup(cleanup)

	require.NoError(t, err)
	assert.NotNil(t, gw)
}

func TestCreatePaymentGateway_ExplicitMock(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := config.ServiceConfig{
		PaymentGatewayProvider: gateway.ProviderMock,
	}

	gw, cleanup, err := createPaymentGateway(cfg, logger)
	t.Cleanup(cleanup)

	require.NoError(t, err)
	assert.NotNil(t, gw)
}

func TestCreatePaymentGateway_StripeProvider(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := config.ServiceConfig{
		PaymentGatewayProvider: gateway.ProviderStripe,
		StripeAPIKey:           "sk_test_fake_key_for_unit_test",
	}

	gw, cleanup, err := createPaymentGateway(cfg, logger)
	t.Cleanup(cleanup)

	require.NoError(t, err)
	assert.NotNil(t, gw)
}

func TestCreatePaymentGateway_FinancialGatewayProvider(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := config.ServiceConfig{
		PaymentGatewayProvider: gateway.ProviderFinancialGateway,
		FinancialGatewayAddr:   "localhost:50064",
	}

	gw, cleanup, err := createPaymentGateway(cfg, logger)
	t.Cleanup(cleanup)

	require.NoError(t, err)
	assert.NotNil(t, gw)
}

func TestCreatePaymentGateway_UnsupportedProvider(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := config.ServiceConfig{
		PaymentGatewayProvider: "paypal",
	}

	gw, cleanup, err := createPaymentGateway(cfg, logger)
	if cleanup != nil {
		t.Cleanup(cleanup)
	}

	assert.ErrorIs(t, err, config.ErrInvalidGatewayProvider)
	assert.Nil(t, gw)
}
