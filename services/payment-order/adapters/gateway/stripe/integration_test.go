package stripe

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stripego "github.com/stripe/stripe-go/v82"

	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// skipWithoutStripeKey skips the test if STRIPE_TEST_API_KEY is not set.
func skipWithoutStripeKey(t *testing.T) string {
	t.Helper()
	key := os.Getenv("STRIPE_TEST_API_KEY")
	if key == "" {
		t.Skip("STRIPE_TEST_API_KEY not set")
	}
	return key
}

// skipWithoutStripeAccount skips if STRIPE_TEST_CONNECTED_ACCOUNT is not set.
func skipWithoutStripeAccount(t *testing.T) string {
	t.Helper()
	acct := os.Getenv("STRIPE_TEST_CONNECTED_ACCOUNT")
	if acct == "" {
		t.Skip("STRIPE_TEST_CONNECTED_ACCOUNT not set")
	}
	return acct
}

func TestIntegration_CreatePaymentIntent(t *testing.T) {
	apiKey := skipWithoutStripeKey(t)
	connectedAccount := skipWithoutStripeAccount(t)

	cfg := DefaultConfig()
	cfg.APIKey = apiKey

	provider := newMockProvider(map[string]TenantConfig{
		"integration_test": {
			ConnectedAccountID: connectedAccount,
		},
	})

	factory, err := NewClientFactory(cfg, provider, nil)
	require.NoError(t, err)

	ctx := tenant.WithTenant(t.Context(), tenant.TenantID("integration_test"))
	client, err := factory.NewClient(ctx)
	require.NoError(t, err)

	// Create a PaymentIntent on the Connected Account
	params := &stripego.PaymentIntentCreateParams{
		Amount:   stripego.Int64(1000),
		Currency: stripego.String(string(stripego.CurrencyUSD)),
	}
	client.ApplyAccount(&params.Params)

	pi, err := client.Raw.V1PaymentIntents.Create(ctx, params)
	require.NoError(t, err)
	assert.NotEmpty(t, pi.ID)
	assert.Equal(t, int64(1000), pi.Amount)
	assert.Equal(t, stripego.CurrencyUSD, pi.Currency)
}
