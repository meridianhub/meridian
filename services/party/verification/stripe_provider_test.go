package verification

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/party/config"
)

func newStripeTestConfig(baseURL string) *config.VerificationConfig {
	return &config.VerificationConfig{
		Provider: "stripe",
		ProviderConfig: map[string]string{
			"api_key":  "sk_test_abc123",
			"base_url": baseURL,
		},
		WebhookSecret: "webhook-secret",
		WebhookURL:    "https://example.com/webhook",
	}
}

func TestNewStripeIdentityProvider_Success(t *testing.T) {
	cfg := newStripeTestConfig("https://api.stripe.com")

	provider, err := NewStripeIdentityProvider(cfg, newTestLogger())

	require.NoError(t, err)
	require.NotNil(t, provider)
	assert.Equal(t, "sk_test_abc123", provider.apiKey)
	assert.Equal(t, "https://api.stripe.com", provider.baseURL)
	assert.Empty(t, provider.stripeAccount)
}

func TestNewStripeIdentityProvider_DefaultBaseURL(t *testing.T) {
	cfg := &config.VerificationConfig{
		Provider: "stripe",
		ProviderConfig: map[string]string{
			"api_key": "sk_test_abc",
		},
	}

	provider, err := NewStripeIdentityProvider(cfg, newTestLogger())

	require.NoError(t, err)
	assert.Equal(t, stripeDefaultBaseURL, provider.baseURL)
}

func TestNewStripeIdentityProvider_CustomBaseURL(t *testing.T) {
	cfg := &config.VerificationConfig{
		Provider: "stripe",
		ProviderConfig: map[string]string{
			"api_key":  "sk_test_abc",
			"base_url": "https://custom.stripe.example.com",
		},
	}

	provider, err := NewStripeIdentityProvider(cfg, newTestLogger())

	require.NoError(t, err)
	assert.Equal(t, "https://custom.stripe.example.com", provider.baseURL)
}

func TestNewStripeIdentityProvider_MissingAPIKey(t *testing.T) {
	cfg := &config.VerificationConfig{
		Provider:       "stripe",
		ProviderConfig: map[string]string{},
	}

	provider, err := NewStripeIdentityProvider(cfg, newTestLogger())

	assert.Nil(t, provider)
	assert.ErrorIs(t, err, ErrStripeMissingAPIKey)
}

func TestNewStripeIdentityProvider_NilProviderConfig(t *testing.T) {
	cfg := &config.VerificationConfig{
		Provider:       "stripe",
		ProviderConfig: nil,
	}

	provider, err := NewStripeIdentityProvider(cfg, newTestLogger())

	assert.Nil(t, provider)
	assert.ErrorIs(t, err, ErrStripeMissingAPIKey)
}

func TestNewStripeIdentityProvider_ConnectedAccount(t *testing.T) {
	cfg := &config.VerificationConfig{
		Provider: "stripe",
		ProviderConfig: map[string]string{
			"api_key":        "sk_test_abc",
			"stripe_account": "acct_123456",
		},
	}

	provider, err := NewStripeIdentityProvider(cfg, newTestLogger())

	require.NoError(t, err)
	assert.Equal(t, "acct_123456", provider.stripeAccount)
}

func TestStripeIdentityProvider_VerifyIdentity_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer sk_test_abc123", r.Header.Get("Authorization"))
		assert.Equal(t, "application/x-www-form-urlencoded", r.Header.Get("Content-Type"))
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/v1/identity/verification_sessions", r.URL.Path)

		require.NoError(t, r.ParseForm())
		assert.Equal(t, "document", r.FormValue("type"))
		assert.NotEmpty(t, r.FormValue("metadata[party_id]"))
		assert.NotEmpty(t, r.FormValue("metadata[party_type]"))

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(stripeVerificationSessionResponse{
			ID:           "vs_001",
			Status:       "verified",
			ClientSecret: "vs_001_secret_abc",
		})
	}))
	defer server.Close()

	cfg := newStripeTestConfig(server.URL)
	provider, err := NewStripeIdentityProvider(cfg, newTestLogger())
	require.NoError(t, err)

	party := newTestParty(t, "Jane Smith")
	result, err := provider.VerifyIdentity(context.Background(), party)

	require.NoError(t, err)
	assert.Equal(t, "vs_001", result.VerificationID)
	assert.Equal(t, StatusApproved, result.Status)
	assert.Equal(t, "Identity verification passed", result.Reason)
	assert.InDelta(t, 0.1, result.RiskScore, 0.001)
	assert.NotNil(t, result.CompletedAt)
	assert.Equal(t, "stripe", result.Metadata["provider"])
	assert.Equal(t, "verified", result.Metadata["session_status"])
}

func TestStripeIdentityProvider_VerifyIdentity_Pending_RequiresInput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(stripeVerificationSessionResponse{
			ID:     "vs_002",
			Status: "requires_input",
		})
	}))
	defer server.Close()

	cfg := newStripeTestConfig(server.URL)
	provider, err := NewStripeIdentityProvider(cfg, newTestLogger())
	require.NoError(t, err)

	party := newTestParty(t, "John Doe")
	result, err := provider.VerifyIdentity(context.Background(), party)

	require.NoError(t, err)
	assert.Equal(t, StatusPending, result.Status)
	assert.Nil(t, result.CompletedAt)
}

func TestStripeIdentityProvider_VerifyIdentity_Pending_Processing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(stripeVerificationSessionResponse{
			ID:     "vs_003",
			Status: "processing",
		})
	}))
	defer server.Close()

	cfg := newStripeTestConfig(server.URL)
	provider, err := NewStripeIdentityProvider(cfg, newTestLogger())
	require.NoError(t, err)

	party := newTestParty(t, "Alice Jones")
	result, err := provider.VerifyIdentity(context.Background(), party)

	require.NoError(t, err)
	assert.Equal(t, StatusPending, result.Status)
	assert.Nil(t, result.CompletedAt)
}

func TestStripeIdentityProvider_VerifyIdentity_Rejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(stripeVerificationSessionResponse{
			ID:     "vs_004",
			Status: "canceled",
		})
	}))
	defer server.Close()

	cfg := newStripeTestConfig(server.URL)
	provider, err := NewStripeIdentityProvider(cfg, newTestLogger())
	require.NoError(t, err)

	party := newTestParty(t, "Bob Builder")
	result, err := provider.VerifyIdentity(context.Background(), party)

	require.NoError(t, err)
	assert.Equal(t, StatusRejected, result.Status)
	assert.Equal(t, "Verification was canceled", result.Reason)
	assert.InDelta(t, 0.0, result.RiskScore, 0.001)
	assert.NotNil(t, result.CompletedAt)
}

func TestStripeIdentityProvider_VerifyIdentity_ManualReview(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(stripeVerificationSessionResponse{
			ID:     "vs_005",
			Status: "requires_action",
		})
	}))
	defer server.Close()

	cfg := newStripeTestConfig(server.URL)
	provider, err := NewStripeIdentityProvider(cfg, newTestLogger())
	require.NoError(t, err)

	party := newTestParty(t, "Mary Jane Watson")
	result, err := provider.VerifyIdentity(context.Background(), party)

	require.NoError(t, err)
	assert.Equal(t, StatusManualReview, result.Status)
	assert.Equal(t, "Verification requires manual review", result.Reason)
	assert.InDelta(t, 0.5, result.RiskScore, 0.001)
	assert.NotNil(t, result.CompletedAt)
}

func TestStripeIdentityProvider_GetVerificationStatus_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/v1/identity/verification_sessions/vs_020", r.URL.Path)

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(stripeVerificationSessionResponse{
			ID:     "vs_020",
			Status: "verified",
		})
	}))
	defer server.Close()

	cfg := newStripeTestConfig(server.URL)
	provider, err := NewStripeIdentityProvider(cfg, newTestLogger())
	require.NoError(t, err)

	result, err := provider.GetVerificationStatus(context.Background(), "vs_020")

	require.NoError(t, err)
	assert.Equal(t, "vs_020", result.VerificationID)
	assert.Equal(t, StatusApproved, result.Status)
	assert.NotNil(t, result.CompletedAt)
}

func TestStripeIdentityProvider_GetVerificationStatus_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(stripeErrorResponse{
			Error: struct {
				Type    string `json:"type"`
				Code    string `json:"code"`
				Message string `json:"message"`
			}{
				Type:    "invalid_request_error",
				Code:    "resource_missing",
				Message: "No such verification session: 'vs_nonexistent'",
			},
		})
	}))
	defer server.Close()

	cfg := newStripeTestConfig(server.URL)
	provider, err := NewStripeIdentityProvider(cfg, newTestLogger())
	require.NoError(t, err)

	_, err = provider.GetVerificationStatus(context.Background(), "vs_nonexistent")

	assert.ErrorIs(t, err, ErrVerificationNotFound)
}

func TestStripeIdentityProvider_CheckSanctions_ReturnsUnsupported(t *testing.T) {
	cfg := newStripeTestConfig("https://api.stripe.com")
	provider, err := NewStripeIdentityProvider(cfg, newTestLogger())
	require.NoError(t, err)

	party := newTestParty(t, "Safe Person")
	result, err := provider.CheckSanctions(context.Background(), party)

	require.NoError(t, err)
	assert.Equal(t, SanctionsStatusClear, result.Status)
	assert.Empty(t, result.Matches)
	assert.Equal(t, "stripe", result.Metadata["provider"])
	assert.Equal(t, "sanctions screening not supported by Stripe Identity", result.Metadata["note"])
	assert.False(t, result.ScreenedAt.IsZero())
}

func TestStripeIdentityProvider_Unauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(stripeErrorResponse{
			Error: struct {
				Type    string `json:"type"`
				Code    string `json:"code"`
				Message string `json:"message"`
			}{
				Type:    "invalid_request_error",
				Message: "No such API key: sk_test_invalid",
			},
		})
	}))
	defer server.Close()

	cfg := newStripeTestConfig(server.URL)
	provider, err := NewStripeIdentityProvider(cfg, newTestLogger())
	require.NoError(t, err)

	party := newTestParty(t, "John Doe")
	_, err = provider.VerifyIdentity(context.Background(), party)

	assert.ErrorIs(t, err, ErrStripeUnauthorized)
}

func TestStripeIdentityProvider_RateLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	cfg := newStripeTestConfig(server.URL)
	provider, err := NewStripeIdentityProvider(cfg, newTestLogger())
	require.NoError(t, err)

	party := newTestParty(t, "John Doe")
	_, err = provider.VerifyIdentity(context.Background(), party)

	assert.ErrorIs(t, err, ErrStripeRateLimited)
}

func TestStripeIdentityProvider_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg := newStripeTestConfig(server.URL)
	provider, err := NewStripeIdentityProvider(cfg, newTestLogger())
	require.NoError(t, err)

	party := newTestParty(t, "John Doe")
	_, err = provider.VerifyIdentity(context.Background(), party)

	assert.ErrorIs(t, err, ErrStripeServerError)
}

func TestStripeIdentityProvider_ConnectedAccountHeader(t *testing.T) {
	var capturedStripeAccount string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedStripeAccount = r.Header.Get("Stripe-Account")

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(stripeVerificationSessionResponse{
			ID:     "vs_connect",
			Status: "requires_input",
		})
	}))
	defer server.Close()

	cfg := newStripeTestConfig(server.URL)
	cfg.ProviderConfig["stripe_account"] = "acct_connected_123"
	provider, err := NewStripeIdentityProvider(cfg, newTestLogger())
	require.NoError(t, err)

	party := newTestParty(t, "John Doe")
	_, err = provider.VerifyIdentity(context.Background(), party)

	require.NoError(t, err)
	assert.Equal(t, "acct_connected_123", capturedStripeAccount)
}

func TestStripeIdentityProvider_NoConnectedAccountHeader(t *testing.T) {
	var capturedStripeAccount string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedStripeAccount = r.Header.Get("Stripe-Account")

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(stripeVerificationSessionResponse{
			ID:     "vs_no_connect",
			Status: "requires_input",
		})
	}))
	defer server.Close()

	cfg := newStripeTestConfig(server.URL)
	provider, err := NewStripeIdentityProvider(cfg, newTestLogger())
	require.NoError(t, err)

	party := newTestParty(t, "John Doe")
	_, err = provider.VerifyIdentity(context.Background(), party)

	require.NoError(t, err)
	assert.Empty(t, capturedStripeAccount)
}

func TestStripeIdentityProvider_AuthorizationHeader(t *testing.T) {
	var capturedAuthHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuthHeader = r.Header.Get("Authorization")

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(stripeVerificationSessionResponse{
			ID:     "vs_auth",
			Status: "requires_input",
		})
	}))
	defer server.Close()

	cfg := newStripeTestConfig(server.URL)
	cfg.ProviderConfig["api_key"] = "sk_live_secret_key"
	provider, err := NewStripeIdentityProvider(cfg, newTestLogger())
	require.NoError(t, err)

	party := newTestParty(t, "John Doe")
	_, err = provider.VerifyIdentity(context.Background(), party)

	require.NoError(t, err)
	assert.Equal(t, "Bearer sk_live_secret_key", capturedAuthHeader)
}

func TestStripeIdentityProvider_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	cfg := newStripeTestConfig(server.URL)
	provider, err := NewStripeIdentityProvider(cfg, newTestLogger())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	party := newTestParty(t, "John Doe")
	_, err = provider.VerifyIdentity(ctx, party)

	assert.Error(t, err)
}

func TestMapStripeSessionStatus(t *testing.T) {
	tests := []struct {
		name           string
		stripeStatus   string
		expectedStatus Status
		expectedScore  float64
	}{
		{"requires_input", "requires_input", StatusPending, 0.0},
		{"processing", "processing", StatusPending, 0.0},
		{"verified", "verified", StatusApproved, 0.1},
		{"canceled", "canceled", StatusRejected, 0.0},
		{"requires_action", "requires_action", StatusManualReview, 0.5},
		{"unknown", "some_unknown_status", StatusPending, 0.0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, _, riskScore := mapStripeSessionStatus(tc.stripeStatus)
			assert.Equal(t, tc.expectedStatus, status)
			assert.InDelta(t, tc.expectedScore, riskScore, 0.001)
		})
	}
}
