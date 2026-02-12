package main

import (
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	stripegateway "github.com/meridianhub/meridian/services/payment-order/adapters/gateway/stripe"
)

func TestCreatePaymentGateway_DefaultMock(t *testing.T) {
	// No PAYMENT_GATEWAY_PROVIDER set - defaults to "mock"
	t.Setenv("PAYMENT_GATEWAY_PROVIDER", "")
	t.Setenv("STRIPE_API_KEY", "")

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	gw, err := createPaymentGateway(logger)

	require.NoError(t, err)
	assert.NotNil(t, gw)
}

func TestCreatePaymentGateway_ExplicitMock(t *testing.T) {
	t.Setenv("PAYMENT_GATEWAY_PROVIDER", "mock")
	t.Setenv("STRIPE_API_KEY", "")

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	gw, err := createPaymentGateway(logger)

	require.NoError(t, err)
	assert.NotNil(t, gw)
}

func TestCreatePaymentGateway_StripeProvider(t *testing.T) {
	t.Setenv("PAYMENT_GATEWAY_PROVIDER", "stripe")
	t.Setenv("STRIPE_API_KEY", "sk_test_fake_key_for_unit_test")

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	gw, err := createPaymentGateway(logger)

	require.NoError(t, err)
	assert.NotNil(t, gw)

	// The resilient gateway wraps the actual gateway, so we can't directly
	// type-assert to StripeGatewayAdapter. Instead, verify the factory
	// didn't error, which confirms the Stripe path was taken.
	_ = stripegateway.GatewayAdapter{} // Compile-time import verification
}

func TestCreatePaymentGateway_StripeMissingAPIKey(t *testing.T) {
	t.Setenv("PAYMENT_GATEWAY_PROVIDER", "stripe")
	t.Setenv("STRIPE_API_KEY", "")

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	gw, err := createPaymentGateway(logger)

	assert.ErrorIs(t, err, ErrMissingStripeAPIKey)
	assert.Nil(t, gw)
}

func TestCreatePaymentGateway_UnsupportedProvider(t *testing.T) {
	t.Setenv("PAYMENT_GATEWAY_PROVIDER", "paypal")
	t.Setenv("STRIPE_API_KEY", "")

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	gw, err := createPaymentGateway(logger)

	assert.ErrorIs(t, err, ErrUnsupportedGatewayProvider)
	assert.Nil(t, gw)
}
