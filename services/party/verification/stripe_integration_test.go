package verification_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/party/config"
	"github.com/meridianhub/meridian/services/party/domain"
	"github.com/meridianhub/meridian/services/party/verification"
)

// stripeSessionResponse mirrors the Stripe API response shape.
type stripeSessionResponse struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	ClientSecret string `json:"client_secret"`
}

// newFakeStripeServer creates a mock HTTP server that simulates the Stripe Identity API.
// It handles:
//   - POST /v1/identity/verification_sessions → creates a session
//   - GET  /v1/identity/verification_sessions/:id → retrieves session status
func newFakeStripeServer(t *testing.T) *httptest.Server {
	t.Helper()

	sessions := map[string]stripeSessionResponse{}

	mux := http.NewServeMux()

	mux.HandleFunc("/v1/identity/verification_sessions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		sess := stripeSessionResponse{
			ID:           "vs_test_abc123",
			Status:       "requires_input",
			ClientSecret: "vs_test_abc123_secret_xyz",
		}
		sessions[sess.ID] = sess

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(sess)
	})

	mux.HandleFunc("/v1/identity/verification_sessions/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Extract session ID from path
		sessionID := r.URL.Path[len("/v1/identity/verification_sessions/"):]

		// Return verified status for the session
		sess, ok := sessions[sessionID]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"No such identity verification session"}}`))
			return
		}

		// Return session with verified status to simulate completion
		sess.Status = "verified"
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(sess)
	})

	return httptest.NewServer(mux)
}

// TestStripeProvider_FullLifecycleViaFactory tests the full Stripe provider lifecycle
// using the factory. It validates that:
//  1. A Stripe config with only api_key (no api_secret) passes validation
//  2. The factory creates a StripeIdentityProvider
//  3. VerifyIdentity creates a verification session and returns PENDING
//  4. GetVerificationStatus returns APPROVED when Stripe reports "verified"
//  5. CheckSanctions returns CLEAR (Stripe Identity doesn't support sanctions)
func TestStripeProvider_FullLifecycleViaFactory(t *testing.T) {
	server := newFakeStripeServer(t)
	defer server.Close()

	// Build config pointing at the fake server, with only api_key (no api_secret)
	cfg := &config.VerificationConfig{
		Provider:      "stripe",
		WebhookSecret: "webhook-secret",
		WebhookURL:    "https://example.com/webhooks/verification",
		ProviderConfig: map[string]string{
			"api_key":  "sk_test_fake_key",
			"base_url": server.URL,
		},
	}

	// Validate config passes without api_secret
	require.NoError(t, cfg.Validate(), "stripe config with only api_key should be valid")

	// Create provider via factory
	provider, err := verification.NewProvider(cfg)
	require.NoError(t, err)
	require.NotNil(t, provider)

	// Verify it's a StripeIdentityProvider (via factory, not direct construction)
	_, ok := provider.(*verification.StripeIdentityProvider)
	require.True(t, ok, "factory should return *StripeIdentityProvider for stripe config")

	// Create a test party
	party, err := domain.NewParty(domain.PartyTypePerson, "Alice Test")
	require.NoError(t, err)

	ctx := context.Background()

	// Step 1: Initiate verification — Stripe returns "requires_input" which maps to PENDING
	result, err := provider.VerifyIdentity(ctx, party)
	require.NoError(t, err)
	assert.Equal(t, "vs_test_abc123", result.VerificationID)
	assert.Equal(t, verification.StatusPending, result.Status, "requires_input should map to PENDING")
	assert.Nil(t, result.CompletedAt, "PENDING result should have no completion time")
	assert.Equal(t, "stripe", result.Metadata["provider"])

	// Step 2: Retrieve status — fake server returns "verified" which maps to APPROVED
	statusResult, err := provider.GetVerificationStatus(ctx, result.VerificationID)
	require.NoError(t, err)
	assert.Equal(t, "vs_test_abc123", statusResult.VerificationID)
	assert.Equal(t, verification.StatusApproved, statusResult.Status, "verified should map to APPROVED")
	assert.NotNil(t, statusResult.CompletedAt, "APPROVED result should have completion time")

	// Step 3: Sanctions check — Stripe Identity does not support this
	sanctionsResult, err := provider.CheckSanctions(ctx, party)
	require.NoError(t, err)
	assert.Equal(t, verification.SanctionsStatusClear, sanctionsResult.Status)
	assert.Equal(t, "stripe", sanctionsResult.Metadata["provider"])
	assert.Contains(t, sanctionsResult.Metadata["note"], "sanctions screening not supported")
}

// TestStripeProvider_GetVerificationStatus_NotFound validates that a missing session
// returns ErrVerificationNotFound.
func TestStripeProvider_GetVerificationStatus_NotFound(t *testing.T) {
	server := newFakeStripeServer(t)
	defer server.Close()

	cfg := &config.VerificationConfig{
		Provider:      "stripe",
		WebhookSecret: "webhook-secret",
		WebhookURL:    "https://example.com/webhooks/verification",
		ProviderConfig: map[string]string{
			"api_key":  "sk_test_fake_key",
			"base_url": server.URL,
		},
	}

	provider, err := verification.NewProvider(cfg)
	require.NoError(t, err)

	_, err = provider.GetVerificationStatus(context.Background(), "vs_nonexistent")
	assert.ErrorIs(t, err, verification.ErrVerificationNotFound)
}

// TestStripeProvider_FactoryWithOptions validates NewProviderWithOptions also creates StripeIdentityProvider.
func TestStripeProvider_FactoryWithOptions(t *testing.T) {
	server := newFakeStripeServer(t)
	defer server.Close()

	cfg := &config.VerificationConfig{
		Provider:      "stripe",
		WebhookSecret: "webhook-secret",
		WebhookURL:    "https://example.com/webhooks/verification",
		ProviderConfig: map[string]string{
			"api_key":  "sk_test_fake_key",
			"base_url": server.URL,
		},
	}

	provider, err := verification.NewProviderWithOptions(cfg, verification.DefaultProviderOptions())
	require.NoError(t, err)
	require.NotNil(t, provider)

	_, ok := provider.(*verification.StripeIdentityProvider)
	assert.True(t, ok, "NewProviderWithOptions should return *StripeIdentityProvider for stripe config")
}
