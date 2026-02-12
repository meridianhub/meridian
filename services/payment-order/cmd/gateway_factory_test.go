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

	gw, err := createPaymentGateway(cfg, logger)

	require.NoError(t, err)
	assert.NotNil(t, gw)
}

func TestCreatePaymentGateway_ExplicitMock(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := config.ServiceConfig{
		PaymentGatewayProvider: gateway.ProviderMock,
	}

	gw, err := createPaymentGateway(cfg, logger)

	require.NoError(t, err)
	assert.NotNil(t, gw)
}

func TestCreatePaymentGateway_StripeProvider(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := config.ServiceConfig{
		PaymentGatewayProvider: gateway.ProviderStripe,
		StripeAPIKey:           "sk_test_fake_key_for_unit_test",
	}

	gw, err := createPaymentGateway(cfg, logger)

	require.NoError(t, err)
	assert.NotNil(t, gw)
}

func TestCreatePaymentGateway_UnsupportedProvider(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := config.ServiceConfig{
		PaymentGatewayProvider: "paypal",
	}

	gw, err := createPaymentGateway(cfg, logger)

	assert.ErrorIs(t, err, config.ErrInvalidGatewayProvider)
	assert.Nil(t, gw)
}
